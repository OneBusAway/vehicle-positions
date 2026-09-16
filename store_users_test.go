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

// uniqueEmail generates a unique test email address to avoid cross-test collisions.
func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("u-%d-%s@test.com", time.Now().UnixNano(), sanitizeTestName(t.Name()))
}

// sanitizeTestName replaces characters that are invalid in email local-parts.
func sanitizeTestName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

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

func TestSetUserActive(t *testing.T) {
	store := newTestStore(t)
	u, err := store.CreateUser(context.Background(), "Deact Me", uniqueEmail(t), "password123", "driver")
	require.NoError(t, err)
	require.True(t, u.Active)

	require.NoError(t, store.SetUserActive(context.Background(), u.ID, false))
	got, err := store.GetUser(context.Background(), u.ID)
	require.NoError(t, err)
	assert.False(t, got.Active)

	require.NoError(t, store.SetUserActive(context.Background(), u.ID, true))
	got, err = store.GetUser(context.Background(), u.ID)
	require.NoError(t, err)
	assert.True(t, got.Active)

	assert.ErrorIs(t, store.SetUserActive(context.Background(), 999999999, false), ErrUserNotFound)
}

func TestGetUserByEmailIncludesActive(t *testing.T) {
	store := newTestStore(t)
	email := uniqueEmail(t)
	u, err := store.CreateUser(context.Background(), "Flag Check", email, "password123", "driver")
	require.NoError(t, err)
	require.NoError(t, store.SetUserActive(context.Background(), u.ID, false))

	fetched, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.False(t, fetched.Active)
}

func TestCountUsersByRole(t *testing.T) {
	store := newTestStore(t)
	u, err := store.CreateUser(context.Background(), "Count Me", uniqueEmail(t), "password123", "driver")
	require.NoError(t, err)

	total, err := store.CountUsersByRole(context.Background(), "driver")
	require.NoError(t, err)
	active, err := store.CountActiveUsersByRole(context.Background(), "driver")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.GreaterOrEqual(t, active, 1)

	require.NoError(t, store.SetUserActive(context.Background(), u.ID, false))
	active2, err := store.CountActiveUsersByRole(context.Background(), "driver")
	require.NoError(t, err)
	assert.Equal(t, active-1, active2)
}

// TestStore_UpdateUserPassword_RoundTrip verifies UpdateUserPassword
// bcrypt-hashes and stores a new password, that the old password no longer
// compares, and that an unknown id reports ErrUserNotFound.
func TestStore_UpdateUserPassword_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })

	u, err := store.CreateUser(ctx, "Password Rotator", email, "originalpass", "driver")
	require.NoError(t, err)

	require.NoError(t, store.UpdateUserPassword(ctx, u.ID, "newpassword123"))

	fetched, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(fetched.PasswordHash), []byte("newpassword123")))
	assert.Error(t, bcrypt.CompareHashAndPassword([]byte(fetched.PasswordHash), []byte("originalpass")))
}

func TestStore_UpdateUserPassword_NotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.UpdateUserPassword(context.Background(), 999999999, "somepassword")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// TestStore_ListUsersPage covers the paged listing: limit is honoured and
// consecutive pages tile the table exactly, with no user skipped or repeated
// across the page boundary. The five users seeded here are the newest rows,
// so they occupy the first five positions of a created_at DESC listing
// whatever else the table holds.
func TestStore_ListUsersPage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	emails := []string{
		"paged-1@example.com",
		"paged-2@example.com",
		"paged-3@example.com",
		"paged-4@example.com",
		"paged-5@example.com",
	}
	cleanupTestUsers(t, store, emails...)
	t.Cleanup(func() { cleanupTestUsers(t, store, emails...) })

	seeded := make(map[string]bool, len(emails))
	for i, email := range emails {
		_, err := store.CreateUser(ctx, fmt.Sprintf("Paged %d", i+1), email, "securepass", "driver")
		require.NoError(t, err)
		seeded[email] = true
	}

	first, err := store.ListUsersPage(ctx, UserFilter{Limit: 3, Offset: 0})
	require.NoError(t, err)
	require.Len(t, first, 3, "limit must cap the page")

	second, err := store.ListUsersPage(ctx, UserFilter{Limit: 3, Offset: 3})
	require.NoError(t, err)
	require.NotEmpty(t, second)

	seen := make(map[string]int, len(emails))
	found := 0
	for _, page := range [][]UserResponse{first, second} {
		for _, u := range page {
			if seeded[u.Email] {
				seen[u.Email]++
				found++
			}
		}
	}
	assert.Equal(t, len(emails), found, "all seeded users must land in the first two pages")
	for _, email := range emails {
		assert.Equal(t, 1, seen[email], "user %s must appear on exactly one page", email)
	}

	past, err := store.ListUsersPage(ctx, UserFilter{Limit: 3, Offset: 1_000_000})
	require.NoError(t, err)
	assert.NotNil(t, past, "an offset past the end should be [], not nil")
	assert.Empty(t, past)
}

// TestStore_ListUsers_SafetyBound verifies the unpaged listing stops at the
// query's 1000-row LIMIT rather than marshalling an unbounded table. The
// rows are seeded with one generate_series insert to keep this fast.
func TestStore_ListUsers_SafetyBound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	deleteBoundUsers := func() {
		_, err := store.pool.Exec(ctx, "DELETE FROM users WHERE email LIKE 'bound-%@example.com'")
		require.NoError(t, err)
	}
	deleteBoundUsers()
	t.Cleanup(deleteBoundUsers)

	// A syntactically valid bcrypt hash; these rows are never logged in as.
	const hash = "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi"
	_, err := store.pool.Exec(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 SELECT 'Bound ' || g, 'bound-' || g || '@example.com', $1, 'driver'
		 FROM generate_series(1, 1001) AS g`, hash)
	require.NoError(t, err)

	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1000, "ListUsers must stop at its 1000-row safety bound")
}

// listUsersFilterEmails are the seeded accounts the filter tests below share.
// They cover both roles and both active states so each test can assert on the
// slice of them it cares about.
var listUsersFilterEmails = []string{
	"filter-amina@example.com",
	"filter-brian@example.com",
	"filter-admin@example.com",
	"filter-retired@example.com",
}

// seedFilterUsers creates the shared filter fixtures and returns them by
// email. The users table is not truncated between tests, so every assertion
// below narrows to these rows rather than counting the whole table.
func seedFilterUsers(t *testing.T, store *Store) map[string]*UserResponse {
	t.Helper()
	ctx := context.Background()
	cleanupTestUsers(t, store, listUsersFilterEmails...)
	t.Cleanup(func() { cleanupTestUsers(t, store, listUsersFilterEmails...) })

	seed := []struct {
		name, email, role string
		deactivate        bool
	}{
		{"Amina Otieno", "filter-amina@example.com", "driver", false},
		{"Brian Kimani", "filter-brian@example.com", "driver", false},
		{"Carol Njoroge", "filter-admin@example.com", "admin", false},
		{"Retired Driver", "filter-retired@example.com", "driver", true},
	}

	byEmail := make(map[string]*UserResponse, len(seed))
	for _, s := range seed {
		u, err := store.CreateUser(ctx, s.name, s.email, "securepass", s.role)
		require.NoError(t, err)
		if s.deactivate {
			require.NoError(t, store.SetUserActive(ctx, u.ID, false))
			u.Active = false
		}
		byEmail[s.email] = u
	}
	return byEmail
}

// seededSubset narrows a page of users to the rows this test seeded, keyed by
// email. Other tests' users share the table, so a filter assertion has to
// ignore them.
func seededSubset(users []UserResponse) map[string]UserResponse {
	got := make(map[string]UserResponse)
	for _, u := range users {
		for _, email := range listUsersFilterEmails {
			if u.Email == email {
				got[u.Email] = u
			}
		}
	}
	return got
}

// TestStore_ListUsersPage_EmptyFilterMatchesAll pins the behaviour every
// existing caller relied on before filters existed: a filter carrying only
// paging returns every user, deactivated ones included, exactly as the
// unfiltered list always did.
func TestStore_ListUsersPage_EmptyFilterMatchesAll(t *testing.T) {
	store := newTestStore(t)
	seedFilterUsers(t, store)

	users, err := store.ListUsersPage(context.Background(), UserFilter{Limit: 1000})
	require.NoError(t, err)

	got := seededSubset(users)
	assert.Len(t, got, len(listUsersFilterEmails), "a zero-value filter must not hide anyone")
	assert.Contains(t, got, "filter-retired@example.com", "deactivated users were listed before and must still be")
}

// TestStore_ListUsersPage_FilterByQ covers the free-text search: it must
// match on name as well as email, case-insensitively, since an admin looking
// up a driver has one or the other to hand.
func TestStore_ListUsersPage_FilterByQ(t *testing.T) {
	store := newTestStore(t)
	seedFilterUsers(t, store)
	ctx := context.Background()

	byName, err := store.ListUsersPage(ctx, UserFilter{Q: "amina", Limit: 1000})
	require.NoError(t, err)
	got := seededSubset(byName)
	require.Len(t, got, 1, "q must match on name, case-insensitively")
	assert.Contains(t, got, "filter-amina@example.com")

	byEmail, err := store.ListUsersPage(ctx, UserFilter{Q: "filter-brian@", Limit: 1000})
	require.NoError(t, err)
	got = seededSubset(byEmail)
	require.Len(t, got, 1, "q must match on email")
	assert.Contains(t, got, "filter-brian@example.com")

	none, err := store.ListUsersPage(ctx, UserFilter{Q: "filter-nobody@example.com", Limit: 1000})
	require.NoError(t, err)
	assert.NotNil(t, none, "no matches should be [], not nil")
	assert.Empty(t, seededSubset(none))
}

// TestStore_ListUsersPage_FilterByRole checks the role filter selects one
// role and excludes the other — in both directions, so a filter that
// happened to ignore its argument could not pass.
func TestStore_ListUsersPage_FilterByRole(t *testing.T) {
	store := newTestStore(t)
	seedFilterUsers(t, store)
	ctx := context.Background()

	drivers, err := store.ListUsersPage(ctx, UserFilter{Role: roleDriver, Q: "filter-", Limit: 1000})
	require.NoError(t, err)
	got := seededSubset(drivers)
	assert.Len(t, got, 3, "the three seeded drivers, active and deactivated")
	assert.NotContains(t, got, "filter-admin@example.com", "role=driver must exclude admins")

	admins, err := store.ListUsersPage(ctx, UserFilter{Role: roleAdmin, Q: "filter-", Limit: 1000})
	require.NoError(t, err)
	got = seededSubset(admins)
	require.Len(t, got, 1)
	assert.Contains(t, got, "filter-admin@example.com", "role=admin must exclude drivers")
}

// TestStore_ListUsersPage_FilterByActive checks the opt-in active filter:
// deactivated accounts are listed by default and hidden only when asked.
func TestStore_ListUsersPage_FilterByActive(t *testing.T) {
	store := newTestStore(t)
	seedFilterUsers(t, store)
	ctx := context.Background()

	all, err := store.ListUsersPage(ctx, UserFilter{Q: "filter-", Limit: 1000})
	require.NoError(t, err)
	assert.Contains(t, seededSubset(all), "filter-retired@example.com", "deactivated users are listed by default")

	activeOnly, err := store.ListUsersPage(ctx, UserFilter{Q: "filter-", ActiveOnly: true, Limit: 1000})
	require.NoError(t, err)
	got := seededSubset(activeOnly)
	assert.Len(t, got, 3)
	assert.NotContains(t, got, "filter-retired@example.com", "ActiveOnly must hide deactivated users")
}

// TestStore_ListUsersPage_FiltersCombine checks the filters AND together
// rather than each merely working alone.
func TestStore_ListUsersPage_FiltersCombine(t *testing.T) {
	store := newTestStore(t)
	seedFilterUsers(t, store)

	got, err := store.ListUsersPage(context.Background(), UserFilter{
		Role:       roleDriver,
		Q:          "filter-",
		ActiveOnly: true,
		Limit:      1000,
	})
	require.NoError(t, err)

	subset := seededSubset(got)
	assert.Len(t, subset, 2, "role, q and the active filter must AND together")
	assert.NotContains(t, subset, "filter-admin@example.com")
	assert.NotContains(t, subset, "filter-retired@example.com")
}

// TestStore_ListUsersPage_FilterWithPaging checks the filter survives the
// LIMIT/OFFSET window: the two pages of a filtered result tile the matching
// set exactly, with nothing skipped, repeated, or leaking in from outside
// the filter.
func TestStore_ListUsersPage_FilterWithPaging(t *testing.T) {
	store := newTestStore(t)
	seedFilterUsers(t, store)
	ctx := context.Background()

	filter := UserFilter{Role: roleDriver, Q: "filter-", ActiveOnly: true, Limit: 1}
	first, err := store.ListUsersPage(ctx, filter)
	require.NoError(t, err)
	require.Len(t, first, 1)

	filter.Offset = 1
	second, err := store.ListUsersPage(ctx, filter)
	require.NoError(t, err)
	require.Len(t, second, 1, "page 2 must still be filtered, not the unfiltered remainder")

	seen := make(map[string]int, 2)
	for _, page := range [][]UserResponse{first, second} {
		for _, u := range page {
			seen[u.Email]++
		}
	}
	assert.Len(t, seen, 2, "the two pages together must cover both matching drivers")
	for _, email := range []string{"filter-amina@example.com", "filter-brian@example.com"} {
		assert.Equal(t, 1, seen[email], "user %s must appear on exactly one page", email)
	}
}
