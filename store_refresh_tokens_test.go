package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// insertRefreshTestUser creates a user the refresh-token rows can reference
// and returns its ID. refresh_tokens.user_id is a foreign key, so every test
// here needs a real user row.
func insertRefreshTestUser(t *testing.T, store *Store) int64 {
	t.Helper()
	ctx := context.Background()
	email := uniqueEmail(t)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })

	var id int64
	err := store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role) VALUES ($1, $2, $3, $4) RETURNING id`,
		"Refresh Test User",
		email,
		"$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		"driver",
	).Scan(&id)
	require.NoError(t, err)
	require.NotZero(t, id)
	return id
}

// countRefreshRows returns how many refresh-token rows exist for a hash.
func countRefreshRows(t *testing.T, store *Store, tokenHash string) int {
	t.Helper()
	var n int
	err := store.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = $1", tokenHash).Scan(&n)
	require.NoError(t, err)
	return n
}

// newStoredRefreshToken creates a token for userID and returns the plaintext
// alongside its hash.
func newStoredRefreshToken(t *testing.T, store *Store, userID int64, expiresAt time.Time) (string, string) {
	t.Helper()
	token, err := newRefreshTokenValue()
	require.NoError(t, err)
	hash := hashRefreshToken(token)
	require.NoError(t, store.CreateRefreshToken(context.Background(), hash, userID, expiresAt))
	return token, hash
}

func TestStore_CreateAndGetRefreshToken(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	expiresAt := time.Now().Add(time.Hour)
	_, hash := newStoredRefreshToken(t, store, userID, expiresAt)

	stored, err := store.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, stored)

	assert.NotZero(t, stored.ID)
	assert.Equal(t, userID, stored.UserID)
	assert.WithinDuration(t, expiresAt, stored.ExpiresAt, time.Second)
	assert.WithinDuration(t, time.Now(), stored.CreatedAt, time.Minute, "created_at defaults to NOW()")
	assert.Nil(t, stored.UsedAt, "a fresh token must read back as never used, not as the zero time")
}

func TestStore_GetRefreshToken_Unknown(t *testing.T) {
	store := newTestStore(t)

	token, err := newRefreshTokenValue()
	require.NoError(t, err)

	stored, err := store.GetRefreshToken(context.Background(), hashRefreshToken(token))
	assert.ErrorIs(t, err, ErrRefreshTokenNotFound,
		"an unknown token must be distinguishable from a database failure")
	assert.Nil(t, stored)
}

func TestStore_RotateRefreshToken(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, oldHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	old, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)

	newToken, err := newRefreshTokenValue()
	require.NoError(t, err)
	newHash := hashRefreshToken(newToken)
	newExpiry := time.Now().Add(time.Hour)

	rotated, err := store.RotateRefreshToken(ctx, old.ID, newHash, userID, newExpiry)
	require.NoError(t, err)
	assert.True(t, rotated)

	consumed, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)
	require.NotNil(t, consumed.UsedAt, "the rotated token must be marked used")
	assert.WithinDuration(t, time.Now(), *consumed.UsedAt, time.Minute)

	replacement, err := store.GetRefreshToken(ctx, newHash)
	require.NoError(t, err)
	assert.Nil(t, replacement.UsedAt)
	assert.Equal(t, userID, replacement.UserID)
	assert.WithinDuration(t, newExpiry, replacement.ExpiresAt, time.Second)
}

// TestStore_RotateRefreshToken_AlreadyUsed is the single-use guarantee: the
// second rotation of one token reports false and issues nothing.
func TestStore_RotateRefreshToken_AlreadyUsed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, oldHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	old, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)

	first, err := newRefreshTokenValue()
	require.NoError(t, err)
	rotated, err := store.RotateRefreshToken(ctx, old.ID, hashRefreshToken(first), userID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, rotated)

	second, err := newRefreshTokenValue()
	require.NoError(t, err)
	rotated, err = store.RotateRefreshToken(ctx, old.ID, hashRefreshToken(second), userID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, rotated, "a consumed token must not rotate twice")

	assert.Equal(t, 0, countRefreshRows(t, store, hashRefreshToken(second)),
		"the losing rotation must not leave a token behind")
}

// TestStore_RotateRefreshToken_RollsBackOnDuplicate verifies the rotation
// transaction is all-or-nothing: if the replacement cannot be inserted, the
// presented token stays usable rather than being burned for nothing.
func TestStore_RotateRefreshToken_RollsBackOnDuplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, existingHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	_, oldHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	old, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)

	// Reusing a hash that is already stored violates the UNIQUE constraint.
	rotated, err := store.RotateRefreshToken(ctx, old.ID, existingHash, userID, time.Now().Add(time.Hour))
	require.Error(t, err, "a duplicate token_hash must fail the UNIQUE constraint")
	assert.False(t, rotated)

	unchanged, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)
	assert.Nil(t, unchanged.UsedAt,
		"the rolled-back rotation must leave the presented token unused")
	assert.Equal(t, 1, countRefreshRows(t, store, existingHash), "no partial row may be left behind")
}

// TestStore_RotateRefreshToken_DeletedUser covers a user deleted between the
// refresh handler reading the token and rotating it. The rotation must lose
// cleanly, as if a concurrent refresh had won, and mint nothing.
func TestStore_RotateRefreshToken_DeletedUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, oldHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	old, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	require.NoError(t, err)

	newToken, err := newRefreshTokenValue()
	require.NoError(t, err)
	rotated, err := store.RotateRefreshToken(ctx, old.ID, hashRefreshToken(newToken), userID, time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.False(t, rotated)
	assert.Equal(t, 0, countRefreshRows(t, store, hashRefreshToken(newToken)))
}

func TestStore_CreateRefreshToken_DuplicateHashRejected(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, hash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))

	err := store.CreateRefreshToken(ctx, hash, userID, time.Now().Add(time.Hour))
	require.Error(t, err, "the UNIQUE constraint must reject a repeated token_hash")
	assert.Equal(t, 1, countRefreshRows(t, store, hash), "the failed insert must leave no second row")
}

func TestStore_CreateRefreshToken_EmptyHashRejected(t *testing.T) {
	store := newTestStore(t)
	userID := insertRefreshTestUser(t, store)

	err := store.CreateRefreshToken(context.Background(), "", userID, time.Now().Add(time.Hour))
	assert.Error(t, err, "the CHECK (token_hash != '') constraint must reject an empty hash")
}

func TestStore_CreateRefreshToken_UnknownUserFK(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	token, err := newRefreshTokenValue()
	require.NoError(t, err)
	hash := hashRefreshToken(token)

	// -1 can never be a users.id (the column is a positive-only sequence).
	err = store.CreateRefreshToken(ctx, hash, -1, time.Now().Add(time.Hour))
	require.Error(t, err, "a token for a non-existent user must fail the foreign key")
	assert.Equal(t, 0, countRefreshRows(t, store, hash), "the failed insert must leave no row behind")
}

func TestStore_DeleteRefreshTokensForUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)
	otherID := insertRefreshTestUser(t, store)

	_, first := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	_, second := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	_, othersToken := newStoredRefreshToken(t, store, otherID, time.Now().Add(time.Hour))

	require.NoError(t, store.DeleteRefreshTokensForUser(ctx, userID))

	assert.Equal(t, 0, countRefreshRows(t, store, first))
	assert.Equal(t, 0, countRefreshRows(t, store, second), "every token for the user must go, not just one")
	assert.Equal(t, 1, countRefreshRows(t, store, othersToken), "another user's tokens must survive")

	assert.NoError(t, store.DeleteRefreshTokensForUser(ctx, userID),
		"deleting again must not error: logout has to be retryable")
}

// TestStore_DeleteRefreshTokensForUser_UnknownUser covers logout by a user
// deleted since their access token was issued: their tokens went with the
// row, so there is nothing to lock and nothing to revoke.
func TestStore_DeleteRefreshTokensForUser_UnknownUser(t *testing.T) {
	store := newTestStore(t)
	otherID := insertRefreshTestUser(t, store)
	_, othersToken := newStoredRefreshToken(t, store, otherID, time.Now().Add(time.Hour))

	assert.NoError(t, store.DeleteRefreshTokensForUser(context.Background(), -1))
	assert.Equal(t, 1, countRefreshRows(t, store, othersToken), "no one else's tokens may be touched")
}

// TestStore_RefreshToken_ExpiredIsStillReadable pins the division of labour:
// the store reports what is stored, and the handler decides that an expired
// token is unusable. A store that hid expired rows would make an expired
// token indistinguishable from an unknown one in the logs.
func TestStore_RefreshToken_ExpiredIsStillReadable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	expiredAt := time.Now().Add(-time.Hour)
	_, hash := newStoredRefreshToken(t, store, userID, expiredAt)

	stored, err := store.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	assert.WithinDuration(t, expiredAt, stored.ExpiresAt, time.Second)
	assert.True(t, time.Now().After(stored.ExpiresAt))
}

func TestStore_RefreshTokens_CascadeOnUserDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, hash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	require.Equal(t, 1, countRefreshRows(t, store, hash))

	_, err := store.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	require.NoError(t, err)

	assert.Equal(t, 0, countRefreshRows(t, store, hash),
		"ON DELETE CASCADE must remove the deleted user's refresh tokens")
}

// TestStore_RotateRefreshToken_ConcurrentRotationsIssueOneToken exercises the
// compare-and-set against a real database: many goroutines present the same
// token at once and exactly one may win. Without the used_at IS NULL guard on
// the UPDATE, several would.
func TestStore_RotateRefreshToken_ConcurrentRotationsIssueOneToken(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)

	_, oldHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	old, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)

	const attempts = 8
	results := make(chan bool, attempts)
	errs := make(chan error, attempts)
	for range attempts {
		token, err := newRefreshTokenValue()
		require.NoError(t, err)
		go func(hash string) {
			rotated, err := store.RotateRefreshToken(ctx, old.ID, hash, userID, time.Now().Add(time.Hour))
			errs <- err
			results <- rotated
		}(hashRefreshToken(token))
	}

	winners := 0
	for range attempts {
		require.NoError(t, <-errs)
		if <-results {
			winners++
		}
	}
	assert.Equal(t, 1, winners, "exactly one concurrent rotation may consume the token")

	var issued int
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM refresh_tokens WHERE user_id = $1", userID).Scan(&issued)
	require.NoError(t, err)
	assert.Equal(t, 2, issued, "the original token plus exactly one replacement")
}

// countUserRefreshRows returns how many refresh-token rows a user holds.
func countUserRefreshRows(t *testing.T, store *Store, userID int64) int {
	t.Helper()
	var n int
	err := store.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM refresh_tokens WHERE user_id = $1", userID).Scan(&n)
	require.NoError(t, err)
	return n
}

// revokeDuringRotation runs revoke while a rotation of userID's token is
// stopped partway through, after it has marked the old token used and while
// its insert is waiting. That is the window issue #117 describes, held open
// on purpose rather than hit by luck.
//
// The rotation is parked on a uniqueness conflict: blocker holds an
// uncommitted row, owned by another user, with the replacement's token_hash,
// so the rotation's insert waits for blocker to finish. Only once revoke is
// itself seen waiting behind the rotation does blocker roll back and let
// both run to completion. Without a shared lock, the revoker's DELETE takes
// its snapshot while the replacement is still uncommitted, never sees it, and
// the replacement survives.
func revokeDuringRotation(t *testing.T, store *Store, userID int64, revoke func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, oldHash := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	old, err := store.GetRefreshToken(ctx, oldHash)
	require.NoError(t, err)
	newToken, err := newRefreshTokenValue()
	require.NoError(t, err)
	newHash := hashRefreshToken(newToken)

	blocker, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer blocker.Rollback(context.Background())
	_, err = blocker.Exec(ctx,
		"INSERT INTO refresh_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)",
		newHash, insertRefreshTestUser(t, store), time.Now().Add(time.Hour))
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockerPID))

	// waitingOnBlocker reports whether some backend is blocked depth hops
	// away from blocker: 1 is the rotation, 2 is whatever waits behind it. It
	// polls through the pool, not blocker, because pg_stat_activity is
	// snapshotted once per transaction and blocker's would never update.
	waitingOnBlocker := func(depth int) func() bool {
		query := "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))"
		if depth == 2 {
			query = `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity waiter, unnest(pg_blocking_pids(waiter.pid)) AS holder(pid)
				WHERE $1 = ANY(pg_blocking_pids(holder.pid)))`
		}
		return func() bool {
			var waiting bool
			err := store.pool.QueryRow(ctx, query, blockerPID).Scan(&waiting)
			return err == nil && waiting
		}
	}

	type rotation struct {
		rotated bool
		err     error
	}
	rotated := make(chan rotation, 1)
	go func() {
		ok, err := store.RotateRefreshToken(ctx, old.ID, newHash, userID, time.Now().Add(time.Hour))
		rotated <- rotation{ok, err}
	}()
	require.Eventually(t, waitingOnBlocker(1), 5*time.Second, 10*time.Millisecond,
		"the rotation never reached its insert")

	revoked := make(chan error, 1)
	go func() { revoked <- revoke(ctx) }()
	require.Eventually(t, waitingOnBlocker(2), 5*time.Second, 10*time.Millisecond,
		"the revocation never queued behind the open rotation")

	require.NoError(t, blocker.Rollback(ctx))

	r := <-rotated
	require.NoError(t, r.err)
	require.True(t, r.rotated, "the rotation started first, so it must win and commit")
	require.NoError(t, <-revoked)

	assert.Equal(t, 0, countUserRefreshRows(t, store, userID),
		"the replacement minted while the revocation ran must not survive it")
}

// TestStore_PasswordResetBeatsAConcurrentRotation is issue #117: a password
// reset that lands while a stolen token is mid-rotation must still end that
// token's line, or the thief keeps rotating after the admin was told the
// account is secured.
func TestStore_PasswordResetBeatsAConcurrentRotation(t *testing.T) {
	store := newTestStore(t)
	userID := insertRefreshTestUser(t, store)

	revokeDuringRotation(t, store, userID, func(ctx context.Context) error {
		return store.SetUserPasswordAndRevokeSessions(ctx, userID, "newpassword123")
	})

	hash := storedPasswordHash(t, store, userID)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("newpassword123")))
}

func TestStore_DeactivationBeatsAConcurrentRotation(t *testing.T) {
	store := newTestStore(t)
	userID := insertRefreshTestUser(t, store)

	revokeDuringRotation(t, store, userID, func(ctx context.Context) error {
		return store.SetUserActiveAndRevokeSessions(ctx, userID, false)
	})

	user, err := store.GetUser(context.Background(), userID)
	require.NoError(t, err)
	assert.False(t, user.Active)
}

// TestStore_LogoutBeatsAConcurrentRotation covers logout. Unlike a password
// reset it writes nothing to the users row before its DELETE, so LockUser is
// the only statement that makes it wait for the rotation to commit. Waiting
// inside the DELETE itself is too late: its snapshot is already taken.
func TestStore_LogoutBeatsAConcurrentRotation(t *testing.T) {
	store := newTestStore(t)
	userID := insertRefreshTestUser(t, store)

	revokeDuringRotation(t, store, userID, func(ctx context.Context) error {
		return store.DeleteRefreshTokensForUser(ctx, userID)
	})
}

// refuseRefreshTokenDeletes makes deleting any of userID's refresh tokens
// fail until the test ends, so a revocation can be made to fail after its
// transaction has already written the users row.
func refuseRefreshTokenDeletes(t *testing.T, store *Store, userID int64) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_refuse_refresh_token_delete() RETURNS trigger AS $$
		BEGIN
			IF OLD.user_id = TG_ARGV[0]::bigint THEN
				RAISE EXCEPTION 'refresh token delete refused by test';
			END IF;
			RETURN OLD;
		END;
		$$ LANGUAGE plpgsql`)
	require.NoError(t, err)
	// Trigger arguments are literals, so the id cannot be a bind parameter.
	_, err = store.pool.Exec(ctx, fmt.Sprintf(`
		CREATE OR REPLACE TRIGGER test_refuse_refresh_token_delete
		BEFORE DELETE ON refresh_tokens
		FOR EACH ROW EXECUTE FUNCTION test_refuse_refresh_token_delete('%d')`, userID))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(),
			"DROP TRIGGER IF EXISTS test_refuse_refresh_token_delete ON refresh_tokens")
		require.NoError(t, err)
		_, err = store.pool.Exec(context.Background(),
			"DROP FUNCTION IF EXISTS test_refuse_refresh_token_delete()")
		require.NoError(t, err)
	})
}

// createPasswordTestUser creates a user through the store, so the stored hash
// is a real bcrypt of password.
func createPasswordTestUser(t *testing.T, store *Store, password string) *UserResponse {
	t.Helper()
	email := uniqueEmail(t)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })
	u, err := store.CreateUser(context.Background(), "Password Test User", email, password, "driver")
	require.NoError(t, err)
	return u
}

// storedPasswordHash reads a user's password_hash straight from the table.
func storedPasswordHash(t *testing.T, store *Store, userID int64) string {
	t.Helper()
	var hash string
	err := store.pool.QueryRow(context.Background(),
		"SELECT password_hash FROM users WHERE id = $1", userID).Scan(&hash)
	require.NoError(t, err)
	return hash
}

func TestStore_SetUserPasswordAndRevokeSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	u := createPasswordTestUser(t, store, "originalpass")
	otherID := insertRefreshTestUser(t, store)

	_, first := newStoredRefreshToken(t, store, u.ID, time.Now().Add(time.Hour))
	_, second := newStoredRefreshToken(t, store, u.ID, time.Now().Add(time.Hour))
	_, othersToken := newStoredRefreshToken(t, store, otherID, time.Now().Add(time.Hour))

	require.NoError(t, store.SetUserPasswordAndRevokeSessions(ctx, u.ID, "newpassword123"))

	hash := storedPasswordHash(t, store, u.ID)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("newpassword123")))
	assert.Error(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("originalpass")))
	assert.Equal(t, 0, countRefreshRows(t, store, first))
	assert.Equal(t, 0, countRefreshRows(t, store, second), "every token for the user must go, not just one")
	assert.Equal(t, 1, countRefreshRows(t, store, othersToken), "another user's tokens must survive")
}

func TestStore_SetUserPasswordAndRevokeSessions_UnknownUser(t *testing.T) {
	store := newTestStore(t)
	otherID := insertRefreshTestUser(t, store)
	_, othersToken := newStoredRefreshToken(t, store, otherID, time.Now().Add(time.Hour))

	// -1 can never be a users.id (the column is a positive-only sequence).
	err := store.SetUserPasswordAndRevokeSessions(context.Background(), -1, "newpassword123")

	assert.ErrorIs(t, err, ErrUserNotFound)
	assert.Equal(t, 1, countRefreshRows(t, store, othersToken), "no one else's tokens may be touched")
}

// TestStore_SetUserPasswordAndRevokeSessions_RollsBackOnRevokeFailure is the
// atomicity half of issue #117. When the revocation fails, the password must
// still be the old one: a changed password with live sessions is exactly the
// state an admin cannot see and must never be left in.
func TestStore_SetUserPasswordAndRevokeSessions_RollsBackOnRevokeFailure(t *testing.T) {
	store := newTestStore(t)
	u := createPasswordTestUser(t, store, "originalpass")
	_, token := newStoredRefreshToken(t, store, u.ID, time.Now().Add(time.Hour))
	before := storedPasswordHash(t, store, u.ID)
	refuseRefreshTokenDeletes(t, store, u.ID)

	err := store.SetUserPasswordAndRevokeSessions(context.Background(), u.ID, "newpassword123")
	require.Error(t, err)

	assert.Equal(t, before, storedPasswordHash(t, store, u.ID), "the password must be unchanged")
	assert.Equal(t, 1, countRefreshRows(t, store, token))
}

func TestStore_SetUserActiveAndRevokeSessions_Deactivate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)
	otherID := insertRefreshTestUser(t, store)

	_, first := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	_, second := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	_, othersToken := newStoredRefreshToken(t, store, otherID, time.Now().Add(time.Hour))

	require.NoError(t, store.SetUserActiveAndRevokeSessions(ctx, userID, false))

	user, err := store.GetUser(ctx, userID)
	require.NoError(t, err)
	assert.False(t, user.Active)
	assert.Equal(t, 0, countRefreshRows(t, store, first))
	assert.Equal(t, 0, countRefreshRows(t, store, second))
	assert.Equal(t, 1, countRefreshRows(t, store, othersToken), "another user's tokens must survive")
}

// TestStore_SetUserActiveAndRevokeSessions_Reactivate pins the asymmetry:
// only deactivation revokes, so the way back up leaves tokens alone.
func TestStore_SetUserActiveAndRevokeSessions_Reactivate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)
	_, err := store.pool.Exec(ctx, "UPDATE users SET active = false WHERE id = $1", userID)
	require.NoError(t, err)
	_, token := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))

	require.NoError(t, store.SetUserActiveAndRevokeSessions(ctx, userID, true))

	user, err := store.GetUser(ctx, userID)
	require.NoError(t, err)
	assert.True(t, user.Active)
	assert.Equal(t, 1, countRefreshRows(t, store, token), "reactivation must not clear refresh tokens")
}

func TestStore_SetUserActiveAndRevokeSessions_UnknownUser(t *testing.T) {
	store := newTestStore(t)
	for _, active := range []bool{false, true} {
		err := store.SetUserActiveAndRevokeSessions(context.Background(), -1, active)
		assert.ErrorIs(t, err, ErrUserNotFound, "active=%v", active)
	}
}

func TestStore_SetUserActiveAndRevokeSessions_RollsBackOnRevokeFailure(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)
	_, token := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	refuseRefreshTokenDeletes(t, store, userID)

	require.Error(t, store.SetUserActiveAndRevokeSessions(ctx, userID, false))

	user, err := store.GetUser(ctx, userID)
	require.NoError(t, err)
	assert.True(t, user.Active, "the user must still be active")
	assert.Equal(t, 1, countRefreshRows(t, store, token))
}

// clearRefreshTokens empties the table so a prune's returned count reflects
// only the rows the calling test created.
func clearRefreshTokens(t *testing.T, store *Store) {
	t.Helper()
	_, err := store.pool.Exec(context.Background(), "DELETE FROM refresh_tokens")
	require.NoError(t, err)
}

// markRefreshTokenUsed consumes a stored token without the replacement row
// RotateRefreshToken would add, so prune tests control exactly what is stored.
func markRefreshTokenUsed(t *testing.T, store *Store, tokenHash string, usedAt time.Time) {
	t.Helper()
	tag, err := store.pool.Exec(context.Background(),
		"UPDATE refresh_tokens SET used_at = $1 WHERE token_hash = $2", usedAt, tokenHash)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected(), "the token to mark used must exist")
}

func TestStore_PruneExpiredRefreshTokens_DeletesExpired(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearRefreshTokens(t, store)
	userID := insertRefreshTestUser(t, store)

	// The common case: a token consumed by rotation and later left to expire.
	now := time.Now()
	_, expired := newStoredRefreshToken(t, store, userID, now.Add(-time.Hour))
	markRefreshTokenUsed(t, store, expired, now.Add(-2*time.Hour))
	_, live := newStoredRefreshToken(t, store, userID, now.Add(time.Hour))

	deleted, err := store.PruneExpiredRefreshTokens(ctx, now)
	require.NoError(t, err)

	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, 0, countRefreshRows(t, store, expired), "an expired token must be removed")
	assert.Equal(t, 1, countRefreshRows(t, store, live), "a token that can still be refreshed must survive")
}

// TestStore_PruneExpiredRefreshTokens_DeletesExpiredEvenIfUnused pins that the
// predicate does not hinge on used_at: the refresh handler rejects an expired
// token whether or not it was ever used, so both rows are equally dead.
func TestStore_PruneExpiredRefreshTokens_DeletesExpiredEvenIfUnused(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearRefreshTokens(t, store)
	userID := insertRefreshTestUser(t, store)

	now := time.Now()
	_, expired := newStoredRefreshToken(t, store, userID, now.Add(-time.Hour))

	deleted, err := store.PruneExpiredRefreshTokens(ctx, now)
	require.NoError(t, err)

	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, 0, countRefreshRows(t, store, expired), "an expired token must go even if it was never used")
}

// TestStore_PruneExpiredRefreshTokens_KeepsUsedButUnexpired guards reuse
// detection. A consumed token that has not yet expired is the row the refresh
// handler reads to tell a replayed token ("already used") from a guessed one
// ("unknown"). A predicate that also swept used rows would turn every replay
// into an unknown token, and revoking the token family on replay — the planned
// follow-up — would have nothing left to detect.
func TestStore_PruneExpiredRefreshTokens_KeepsUsedButUnexpired(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearRefreshTokens(t, store)
	userID := insertRefreshTestUser(t, store)

	now := time.Now()
	_, consumed := newStoredRefreshToken(t, store, userID, now.Add(time.Hour))
	markRefreshTokenUsed(t, store, consumed, now.Add(-time.Minute))
	// An expired row in the same pass proves the delete ran, so the survivor
	// is not explained by a prune that matched nothing.
	_, expired := newStoredRefreshToken(t, store, userID, now.Add(-time.Hour))

	deleted, err := store.PruneExpiredRefreshTokens(ctx, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, 0, countRefreshRows(t, store, expired))

	stored, err := store.GetRefreshToken(ctx, consumed)
	require.NoError(t, err, "a consumed but unexpired token must still be stored")
	assert.NotNil(t, stored.UsedAt, "it must still read as used, or a replay would look like an unknown token")
}

func TestStore_PruneExpiredRefreshTokens_NoRowsIsNotAnError(t *testing.T) {
	store := newTestStore(t)
	clearRefreshTokens(t, store)

	deleted, err := store.PruneExpiredRefreshTokens(context.Background(), time.Now())

	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

func TestStore_PruneExpiredRefreshTokens_OtherUsersUntouched(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearRefreshTokens(t, store)
	staleUser := insertRefreshTestUser(t, store)
	activeUser := insertRefreshTestUser(t, store)

	now := time.Now()
	_, staleFirst := newStoredRefreshToken(t, store, staleUser, now.Add(-time.Hour))
	_, staleSecond := newStoredRefreshToken(t, store, staleUser, now.Add(-48*time.Hour))
	_, activeFirst := newStoredRefreshToken(t, store, activeUser, now.Add(time.Hour))
	_, activeSecond := newStoredRefreshToken(t, store, activeUser, now.Add(168*time.Hour))

	deleted, err := store.PruneExpiredRefreshTokens(ctx, now)
	require.NoError(t, err)

	assert.Equal(t, int64(2), deleted)
	assert.Equal(t, 0, countRefreshRows(t, store, staleFirst))
	assert.Equal(t, 0, countRefreshRows(t, store, staleSecond))
	assert.Equal(t, 1, countRefreshRows(t, store, activeFirst), "another user's live tokens must survive")
	assert.Equal(t, 1, countRefreshRows(t, store, activeSecond))
}

// TestStore_PruneExpiredRefreshTokens_BoundaryExact pins the strict predicate.
// A token at exactly the cutoff is already rejected by the handler, so keeping
// it until the next pass is harmless; nothing after the cutoff may be touched.
func TestStore_PruneExpiredRefreshTokens_BoundaryExact(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearRefreshTokens(t, store)
	userID := insertRefreshTestUser(t, store)

	// Postgres stores timestamptz at microsecond precision, so truncating here
	// keeps the boundary row exactly equal to the cutoff after the round trip.
	cutoff := time.Now().Truncate(time.Microsecond)
	_, before := newStoredRefreshToken(t, store, userID, cutoff.Add(-time.Microsecond))
	_, at := newStoredRefreshToken(t, store, userID, cutoff)
	_, after := newStoredRefreshToken(t, store, userID, cutoff.Add(time.Microsecond))

	deleted, err := store.PruneExpiredRefreshTokens(ctx, cutoff)
	require.NoError(t, err)

	assert.Equal(t, int64(1), deleted, "the predicate is < cutoff, so only the earlier row goes")
	assert.Equal(t, 0, countRefreshRows(t, store, before))
	assert.Equal(t, 1, countRefreshRows(t, store, at), "the row exactly at the cutoff is kept")
	assert.Equal(t, 1, countRefreshRows(t, store, after), "a row expiring after the cutoff must never be touched")
}

// TestHashRefreshToken covers the value actually written to token_hash: the
// column is looked up by exact match, so a non-deterministic hash would make
// every refresh fail, and a hash that leaked the token would defeat storing
// only a digest.
func TestHashRefreshToken(t *testing.T) {
	token, err := newRefreshTokenValue()
	require.NoError(t, err)
	require.Len(t, token, 64, "32 random bytes, hex-encoded")

	other, err := newRefreshTokenValue()
	require.NoError(t, err)
	assert.NotEqual(t, token, other, "tokens must not repeat")

	hash := hashRefreshToken(token)
	assert.Equal(t, hash, hashRefreshToken(token), "hashing must be deterministic, or lookups fail")
	assert.NotEqual(t, token, hash, "the stored value must not be the token itself")
	assert.NotContains(t, hash, token)
	assert.NotEqual(t, hash, hashRefreshToken(other))
}
