package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertRevocationTestUser creates a user the revocation rows can reference
// and returns its ID. revoked_tokens.user_id is a foreign key, so every test
// here needs a real user row.
func insertRevocationTestUser(t *testing.T, store *Store) int64 {
	t.Helper()
	ctx := context.Background()
	email := uniqueEmail(t)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })

	var id int64
	err := store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role) VALUES ($1, $2, $3, $4) RETURNING id`,
		"Revocation Test User",
		email,
		"$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		"driver",
	).Scan(&id)
	require.NoError(t, err)
	require.NotZero(t, id)
	return id
}

// countRevocationRows returns how many revocation rows exist for a jti.
func countRevocationRows(t *testing.T, store *Store, jti string) int {
	t.Helper()
	var n int
	err := store.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM revoked_tokens WHERE jti = $1", jti).Scan(&n)
	require.NoError(t, err)
	return n
}

func TestStore_RevokeAndCheckToken(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRevocationTestUser(t, store)

	jti, err := newJTI()
	require.NoError(t, err)

	revoked, err := store.IsTokenRevoked(ctx, jti)
	require.NoError(t, err)
	require.False(t, revoked, "jti must not be revoked before RevokeToken runs")

	expiresAt := time.Now().Add(24 * time.Hour)
	require.NoError(t, store.RevokeToken(ctx, jti, userID, expiresAt))

	revoked, err = store.IsTokenRevoked(ctx, jti)
	require.NoError(t, err)
	assert.True(t, revoked)

	// Round-trip the stored row: expires_at is what a future cleanup job
	// will filter on, so a wrong value would silently break it.
	var gotUserID int64
	var gotExpiresAt, gotRevokedAt time.Time
	err = store.pool.QueryRow(ctx,
		"SELECT user_id, expires_at, revoked_at FROM revoked_tokens WHERE jti = $1", jti,
	).Scan(&gotUserID, &gotExpiresAt, &gotRevokedAt)
	require.NoError(t, err)
	assert.Equal(t, userID, gotUserID)
	assert.WithinDuration(t, expiresAt, gotExpiresAt, time.Second)
	assert.WithinDuration(t, time.Now(), gotRevokedAt, time.Minute, "revoked_at defaults to NOW()")
}

func TestStore_IsTokenRevoked_Unknown(t *testing.T) {
	store := newTestStore(t)

	jti, err := newJTI()
	require.NoError(t, err)

	revoked, err := store.IsTokenRevoked(context.Background(), jti)
	assert.NoError(t, err, "an unknown jti is not an error, just not revoked")
	assert.False(t, revoked)
}

func TestStore_RevokeToken_Idempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRevocationTestUser(t, store)

	jti, err := newJTI()
	require.NoError(t, err)

	expiresAt := time.Now().Add(24 * time.Hour)
	require.NoError(t, store.RevokeToken(ctx, jti, userID, expiresAt))
	assert.NoError(t, store.RevokeToken(ctx, jti, userID, expiresAt),
		"revoking the same jti twice must not error (ON CONFLICT DO NOTHING)")

	assert.Equal(t, 1, countRevocationRows(t, store, jti), "the second revoke must not add a row")
}

func TestStore_RevokeToken_UnknownUserFK(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	jti, err := newJTI()
	require.NoError(t, err)

	// -1 can never be a users.id (the column is a positive-only sequence).
	err = store.RevokeToken(ctx, jti, -1, time.Now().Add(24*time.Hour))
	require.Error(t, err, "a revocation for a non-existent user must fail the foreign key")

	assert.Equal(t, 0, countRevocationRows(t, store, jti), "the failed insert must leave no row behind")
}

func TestStore_RevokeToken_EmptyJtiRejected(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRevocationTestUser(t, store)

	err := store.RevokeToken(ctx, "", userID, time.Now().Add(24*time.Hour))
	assert.Error(t, err, "the CHECK (jti != '') constraint must reject an empty jti")
}

// TestStore_RevokeToken_SurvivesUserDelete is the regression test for the
// review finding on #98: with ON DELETE CASCADE, hard-deleting a user (which
// DELETE /api/v1/admin/users/{id} does) dropped their revocation rows, and
// because nothing in the request path consults the users table, an already
// revoked token became valid again for the rest of its 24h life — with its
// original role claim, so a revoked admin token regained every admin route.
// The row must outlive the account; only the expiry-based cleanup job should
// ever remove it.
func TestStore_RevokeToken_SurvivesUserDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRevocationTestUser(t, store)

	jti, err := newJTI()
	require.NoError(t, err)
	require.NoError(t, store.RevokeToken(ctx, jti, userID, time.Now().Add(24*time.Hour)))
	require.Equal(t, 1, countRevocationRows(t, store, jti))

	_, err = store.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	require.NoError(t, err)

	assert.Equal(t, 1, countRevocationRows(t, store, jti),
		"deleting the user must not remove the revocation row")

	revoked, err := store.IsTokenRevoked(ctx, jti)
	require.NoError(t, err)
	assert.True(t, revoked, "the token must stay revoked after its user is deleted")

	// ON DELETE SET NULL: the row survives with its owner forgotten.
	var ownerID *int64
	err = store.pool.QueryRow(ctx, "SELECT user_id FROM revoked_tokens WHERE jti = $1", jti).Scan(&ownerID)
	require.NoError(t, err)
	assert.Nil(t, ownerID, "user_id must be NULLed, not carry a dangling id")
}
