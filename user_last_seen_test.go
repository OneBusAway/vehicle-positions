package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

const lastSeenMigration = 26

// TestMigration_LastSeenUpAndDown: up, stamping last_seen_at leaves
// updated_at alone while an edit still moves it; down, the column is gone and
// the trigger stamps every update again; and it goes up a second time.
func TestMigration_LastSeenUpAndDown(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	name := fmt.Sprintf("last_seen_migration_%d", time.Now().UnixNano())
	_, err := store.pool.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		assert.NoError(t, err)
	})
	dbURL, err := url.Parse(testDatabaseURL(t))
	require.NoError(t, err)
	dbURL.Path = "/" + name

	src, err := iofs.New(migrationsFS, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	conn, err := pgx.Connect(ctx, dbURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })

	require.NoError(t, m.Migrate(lastSeenMigration))
	var id int64
	require.NoError(t, conn.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, updated_at) VALUES ('x', 'x@x.org', 'hash', 'driver', '2020-01-01') RETURNING id`).Scan(&id))
	updatedAt := func() time.Time {
		t.Helper()
		var at time.Time
		require.NoError(t, conn.QueryRow(ctx, `SELECT updated_at FROM users WHERE id = $1`, id).Scan(&at))
		return at
	}
	old := updatedAt()

	_, err = conn.Exec(ctx, `UPDATE users SET last_seen_at = NOW() WHERE id = $1`, id)
	require.NoError(t, err)
	assert.True(t, updatedAt().Equal(old), "stamping last_seen_at leaves updated_at alone")
	_, err = conn.Exec(ctx, `UPDATE users SET name = 'y' WHERE id = $1`, id)
	require.NoError(t, err)
	assert.True(t, updatedAt().After(old), "an edit still moves updated_at")

	require.NoError(t, m.Migrate(lastSeenMigration-1))
	var columns int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'last_seen_at'`).Scan(&columns))
	assert.Zero(t, columns, "down drops the column")
	_, err = conn.Exec(ctx, `UPDATE users SET updated_at = '2020-01-01' WHERE id = $1`, id)
	require.NoError(t, err)
	assert.True(t, updatedAt().After(old), "down puts back the trigger that stamps every update")

	require.NoError(t, m.Migrate(lastSeenMigration))
}

// TestStore_UpdateUserLastSeen is the round trip behind the Users page's
// "Last seen": a new user has none, the stamp sets it to now, and it does not
// move updated_at, which an edit to the account still does.
func TestStore_UpdateUserLastSeen(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const email = "last-seen@example.com"
	cleanupTestUsers(t, store, email)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })

	created, err := store.CreateUser(ctx, "Last Seen", email, "securepass", "driver")
	require.NoError(t, err)
	find := func() UserResponse {
		t.Helper()
		users, err := store.ListUsersPage(ctx, UserFilter{Q: email, Limit: 10})
		require.NoError(t, err)
		require.Len(t, users, 1)
		return users[0]
	}

	before := find()
	assert.Nil(t, before.LastSeenAt, "a user who has never signed in has no last seen time")

	require.NoError(t, store.UpdateUserLastSeen(ctx, created.ID))
	after := find()
	require.NotNil(t, after.LastSeenAt)
	assert.WithinDuration(t, time.Now(), *after.LastSeenAt, time.Minute)
	assert.True(t, before.UpdatedAt.Equal(after.UpdatedAt), "signing in is not an edit to the account")

	_, err = store.UpdateUser(ctx, created.ID, "Renamed", email, "driver")
	require.NoError(t, err)
	edited := find()
	assert.True(t, edited.UpdatedAt.After(after.UpdatedAt), "an edit still moves updated_at")
	assert.True(t, edited.LastSeenAt.Equal(*after.LastSeenAt), "an edit is not a sign-in")
}

func TestHandleLogin_RecordsLastSeen(t *testing.T) {
	t.Run("a successful sign-in", func(t *testing.T) {
		store := loginTestUser()
		w := postLogin(handleLogin(store, newFakeRefreshTokens(), testSecret, testTTLs, nil, false), "driver@test.com", "password")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, []int64{1}, store.seen)
	})

	for _, tc := range []struct {
		name     string
		store    func() *mockUserStore
		password string
	}{
		{"a wrong password", loginTestUser, "nope"},
		{"an unknown email", func() *mockUserStore { return &mockUserStore{err: ErrUserNotFound} }, "password"},
		{"a deactivated user", func() *mockUserStore { s := loginTestUser(); s.user.Active = false; return s }, "password"},
	} {
		t.Run(tc.name+" is not recorded", func(t *testing.T) {
			store := tc.store()
			w := postLogin(handleLogin(store, newFakeRefreshTokens(), testSecret, testTTLs, nil, false), "driver@test.com", tc.password)
			require.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Empty(t, store.seen)
		})
	}

	t.Run("a rate-limited attempt is not recorded", func(t *testing.T) {
		store := loginTestUser()
		limiter := NewLoginRateLimiter()
		defer limiter.Stop()
		handler := handleLogin(store, newFakeRefreshTokens(), testSecret, testTTLs, limiter, false)
		for range loginEmailLimit {
			require.Equal(t, http.StatusUnauthorized, postLogin(handler, "driver@test.com", "nope").Code)
		}
		require.Equal(t, http.StatusTooManyRequests, postLogin(handler, "driver@test.com", "password").Code)
		assert.Empty(t, store.seen)
	})

	t.Run("a sign-in that fails to issue its refresh token is not recorded", func(t *testing.T) {
		store := loginTestUser()
		tokens := newFakeRefreshTokens()
		tokens.createErr = errors.New("database is down")
		w := postLogin(handleLogin(store, tokens, testSecret, testTTLs, nil, false), "driver@test.com", "password")
		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Empty(t, store.seen)
	})
}

// TestHandleLogin_SignsInWhenLastSeenFails: the stamp is a record for the
// Users page, so a failed write must not cost the driver their sign-in.
func TestHandleLogin_SignsInWhenLastSeenFails(t *testing.T) {
	store := loginTestUser()
	store.seenErr = errors.New("database is down")

	w := postLogin(handleLogin(store, newFakeRefreshTokens(), testSecret, testTTLs, nil, false), "driver@test.com", "password")

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeTokens(t, w)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, []int64{1}, store.seen, "the write was tried")
}

func TestHandleRefresh_RecordsLastSeen(t *testing.T) {
	t.Run("a successful refresh, and not the same token spent again", func(t *testing.T) {
		f, token := refreshStoreWithUser(t)
		handler := handleRefreshToken(f, testSecret, testTTLs, nil, false)
		require.Equal(t, http.StatusOK, postRefresh(handler, token).Code)
		assert.Equal(t, []int64{42}, f.seen)

		require.Equal(t, http.StatusUnauthorized, postRefresh(handler, token).Code)
		assert.Equal(t, []int64{42}, f.seen)
	})

	t.Run("a deactivated user is not recorded", func(t *testing.T) {
		f, token := refreshStoreWithUser(t)
		f.user.Active = false
		require.Equal(t, http.StatusUnauthorized, postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token).Code)
		assert.Empty(t, f.seen)
	})

	t.Run("an expired token is not recorded", func(t *testing.T) {
		f := newFakeRefreshTokens()
		f.user = &UserResponse{ID: 42, Email: "driver@test.com", Role: "driver", Active: true}
		token := storeRefreshToken(t, f, 42, time.Now().Add(-time.Minute))
		require.Equal(t, http.StatusUnauthorized, postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token).Code)
		assert.Empty(t, f.seen)
	})

	t.Run("a rate-limited attempt is not recorded", func(t *testing.T) {
		f, token := refreshStoreWithUser(t)
		limiter := NewRefreshRateLimiter()
		defer limiter.Stop()
		handler := handleRefreshToken(f, testSecret, testTTLs, limiter, false)
		current := token
		for range refreshIPLimit {
			w := postRefresh(handler, current)
			require.Equal(t, http.StatusOK, w.Code)
			current = decodeTokens(t, w).RefreshToken
		}
		require.Len(t, f.seen, refreshIPLimit)

		require.Equal(t, http.StatusTooManyRequests, postRefresh(handler, current).Code)
		assert.Len(t, f.seen, refreshIPLimit)
	})
}

func TestHandleRefresh_RefreshesWhenLastSeenFails(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	f.seenErr = errors.New("database is down")

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, decodeTokens(t, w).RefreshToken)
	assert.Equal(t, []int64{42}, f.seen, "the write was tried")
}

func TestAdminLogin_RecordsLastSeen(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcryptCost)
	require.NoError(t, err)
	admin := &User{ID: 1, Email: "boss@test.com", PasswordHash: string(hash), Role: roleAdmin, Active: true}
	driver := &User{ID: 2, Email: "drv@test.com", PasswordHash: string(hash), Role: roleDriver, Active: true}
	inactive := &User{ID: 3, Email: "gone@test.com", PasswordHash: string(hash), Role: roleAdmin, Active: false}

	// Each case gets its own admin UI, so its rate limiter starts empty.
	setup := func(t *testing.T) (*fakeUserFetcher, func(email, password string) *httptest.ResponseRecorder) {
		t.Helper()
		ui := newTestAdminUI(t)
		ui.stats = &fakeAdminStats{vehicles: 3, drivers: 2}
		users := &fakeUserFetcher{users: map[string]*User{admin.Email: admin, driver.Email: driver, inactive.Email: inactive}}
		ui.users = users
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)
		return users, func(email, password string) *httptest.ResponseRecorder {
			form := url.Values{"email": {email}, "password": {password}}
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			return w
		}
	}

	t.Run("a successful sign-in", func(t *testing.T) {
		users, post := setup(t)
		require.Equal(t, http.StatusSeeOther, post(admin.Email, "password123").Code)
		assert.Equal(t, []int64{admin.ID}, users.seen)
	})

	for _, tc := range []struct {
		name, email, password string
		code                  int
	}{
		{"a wrong password", admin.Email, "nope", http.StatusUnauthorized},
		{"an unknown email", "ghost@test.com", "password123", http.StatusUnauthorized},
		{"a deactivated user", inactive.Email, "password123", http.StatusUnauthorized},
		{"a driver, who may not use the admin", driver.Email, "password123", http.StatusForbidden},
	} {
		t.Run(tc.name+" is not recorded", func(t *testing.T) {
			users, post := setup(t)
			require.Equal(t, tc.code, post(tc.email, tc.password).Code)
			assert.Empty(t, users.seen)
		})
	}

	t.Run("a rate-limited attempt is not recorded", func(t *testing.T) {
		users, post := setup(t)
		for range loginEmailLimit {
			require.Equal(t, http.StatusUnauthorized, post(admin.Email, "nope").Code)
		}
		require.Equal(t, http.StatusTooManyRequests, post(admin.Email, "password123").Code)
		assert.Empty(t, users.seen)
	})

	t.Run("a failed write still signs in", func(t *testing.T) {
		users, post := setup(t)
		users.seenErr = errors.New("database is down")
		w := post(admin.Email, "password123")
		require.Equal(t, http.StatusSeeOther, w.Code)
		require.NotEmpty(t, w.Result().Cookies())
		assert.Equal(t, sessionCookieName, w.Result().Cookies()[0].Name)
		assert.Equal(t, []int64{admin.ID}, users.seen, "the write was tried")
	})
}

func TestUsersPage_ShowsLastSeen(t *testing.T) {
	seen := time.Date(2026, 11, 19, 7, 5, 0, 0, time.UTC)
	users := seedUsers(2)
	users[0].LastSeenAt = &seen
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, newFakeUserStore(users...), newFakeAssignmentStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/users")

	assert.Contains(t, body, "<th>Last seen</th>")
	assert.Contains(t, body, `<td class="num nowrap" data-label="Last seen">2026-11-19 07:05 UTC</td>`)
	assert.Contains(t, body, `<td class="num nowrap" data-label="Last seen"><span class="muted">never</span></td>`)
}
