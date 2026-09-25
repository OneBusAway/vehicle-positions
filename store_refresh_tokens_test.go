package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
