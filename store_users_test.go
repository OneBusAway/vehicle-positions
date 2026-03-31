package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestStore_GetUserByEmail_Found(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM users WHERE email = 'testuser@example.com'")
	require.NoError(t, err)
	t.Cleanup(func() { cleanupTestUsers(t, store, "testuser@example.com") })

	_, err = store.pool.Exec(ctx,
		`INSERT INTO users (name, email, password_hash, role) VALUES ($1, $2, $3, $4)`,
		"Test User",
		"testuser@example.com",
		"$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		"driver",
	)
	require.NoError(t, err)

	user, err := store.GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	assert.Equal(t, "testuser@example.com", user.Email)
	assert.Equal(t, "Test User", user.Name)
	assert.Equal(t, "driver", user.Role)
	assert.NotEmpty(t, user.PasswordHash)
	assert.NotZero(t, user.ID)
}

func TestStore_GetUserByEmail_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user, err := store.GetUserByEmail(ctx, "nobody@example.com")

	assert.Error(t, err)
	assert.Nil(t, user)
	assert.ErrorIs(t, err, ErrUserNotFound, "not-found must return ErrUserNotFound so callers can distinguish from DB errors")
}

// --- Admin User CRUD Integration Tests ---

// cleanupTestUsers removes test users by email prefix to avoid cross-test pollution.
func cleanupTestUsers(t *testing.T, store *Store, emails ...string) {
	t.Helper()
	ctx := context.Background()
	for _, email := range emails {
		_, err := store.pool.Exec(ctx, "DELETE FROM users WHERE email = $1", email)
		require.NoError(t, err)
	}
}

func TestStore_CreateUser_GetUser_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "crud-roundtrip@example.com")
	t.Cleanup(func() { cleanupTestUsers(t, store, "crud-roundtrip@example.com") })

	created, err := store.CreateUser(ctx, "Round Trip", "crud-roundtrip@example.com", "securepass", "driver")
	require.NoError(t, err)
	require.NotNil(t, created)

	assert.NotZero(t, created.ID)
	assert.Equal(t, "Round Trip", created.Name)
	assert.Equal(t, "crud-roundtrip@example.com", created.Email)
	assert.Equal(t, "driver", created.Role)
	assert.False(t, created.CreatedAt.IsZero())
	assert.False(t, created.UpdatedAt.IsZero())

	got, err := store.GetUser(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, created.Name, got.Name)
	assert.Equal(t, created.Email, got.Email)
	assert.Equal(t, created.Role, got.Role)
}

func TestStore_CreateUser_PasswordHashValid(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "crud-hash@example.com")
	t.Cleanup(func() { cleanupTestUsers(t, store, "crud-hash@example.com") })

	created, err := store.CreateUser(ctx, "Hash Test", "crud-hash@example.com", "mypassword", "driver")
	require.NoError(t, err)

	// Verify bcrypt hash is stored and valid
	var hash string
	err = store.pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", created.ID).Scan(&hash)
	require.NoError(t, err)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("mypassword")))
	assert.Error(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrongpassword")))
}

func TestStore_CreateUser_DuplicateEmail(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "crud-dup@example.com")
	t.Cleanup(func() { cleanupTestUsers(t, store, "crud-dup@example.com") })

	_, err := store.CreateUser(ctx, "First", "crud-dup@example.com", "securepass", "driver")
	require.NoError(t, err)

	_, err = store.CreateUser(ctx, "Second", "crud-dup@example.com", "securepass", "admin")
	assert.ErrorIs(t, err, ErrDuplicateEmail)

	// Verify no stale second row was left behind
	var count int
	err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE email = $1", "crud-dup@example.com").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "duplicate email must not leave a stale row")
}

func TestStore_ListUsers(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "crud-list1@example.com", "crud-list2@example.com")
	t.Cleanup(func() { cleanupTestUsers(t, store, "crud-list1@example.com", "crud-list2@example.com") })

	_, err := store.CreateUser(ctx, "First", "crud-list1@example.com", "securepass", "driver")
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "Second", "crud-list2@example.com", "securepass", "admin")
	require.NoError(t, err)

	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(users), 2, "should return at least the 2 users we created")

	// Verify both users are present in the list
	var foundFirst, foundSecond bool
	for _, u := range users {
		if u.Email == "crud-list1@example.com" {
			foundFirst = true
		}
		if u.Email == "crud-list2@example.com" {
			foundSecond = true
		}
	}
	assert.True(t, foundFirst, "first user must be in list")
	assert.True(t, foundSecond, "second user must be in list")
}

func TestStore_UpdateUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "crud-update@example.com", "crud-updated@example.com")
	t.Cleanup(func() { cleanupTestUsers(t, store, "crud-update@example.com", "crud-updated@example.com") })

	created, err := store.CreateUser(ctx, "Original", "crud-update@example.com", "securepass", "driver")
	require.NoError(t, err)

	updated, err := store.UpdateUser(ctx, created.ID, "Updated Name", "crud-updated@example.com", "admin")
	require.NoError(t, err)
	require.NotNil(t, updated)

	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "crud-updated@example.com", updated.Email)
	assert.Equal(t, "admin", updated.Role)

	// Verify via GetUser
	got, err := store.GetUser(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", got.Name)
	assert.Equal(t, "crud-updated@example.com", got.Email)
	assert.Equal(t, "admin", got.Role)
}

func TestStore_UpdateUser_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.UpdateUser(ctx, 999999, "Ghost", "ghost@example.com", "driver")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestStore_GetUser_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user, err := store.GetUser(ctx, 999999)
	assert.ErrorIs(t, err, ErrUserNotFound)
	assert.Nil(t, user)
}

func TestStore_DeleteUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "crud-delete@example.com")
	t.Cleanup(func() { cleanupTestUsers(t, store, "crud-delete@example.com") })

	created, err := store.CreateUser(ctx, "To Delete", "crud-delete@example.com", "securepass", "driver")
	require.NoError(t, err)

	err = store.DeleteUser(ctx, created.ID)
	require.NoError(t, err)

	// Verify user is gone
	_, err = store.GetUser(ctx, created.ID)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestStore_DeleteUser_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.DeleteUser(ctx, 999999)
	assert.ErrorIs(t, err, ErrUserNotFound)
}
