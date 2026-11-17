package main

import (
	"context"
	"encoding/json"
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const emailLowerMigration = 24

// An upgrade with accounts whose emails differ only in case keeps the oldest
// active one at the address and moves each other one to a +duplicate address,
// and lower-cases the rest, so the unique index can be built.
func TestMigration_EmailLowerUpAndDown(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	name := fmt.Sprintf("email_lower_migration_%d", time.Now().UnixNano())
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

	require.NoError(t, m.Migrate(emailLowerMigration-1))
	insert := func(email string, active bool) int64 {
		t.Helper()
		var id int64
		require.NoError(t, conn.QueryRow(ctx,
			`INSERT INTO users (name, email, password_hash, role, active) VALUES ('x', $1, 'hash', 'driver', $2) RETURNING id`,
			email, active).Scan(&id))
		return id
	}
	oldestInactive := insert("ANA@x.org", false)
	keeper := insert("Ana@X.org", true)
	newer := insert("ana@x.org", true)
	single := insert("Bo@Y.org", true)
	noAt := insert("No-At-Sign", true)
	noAtTwin := insert("no-at-SIGN", true)

	require.NoError(t, m.Migrate(emailLowerMigration))
	emailOf := func(id int64) string {
		t.Helper()
		var email string
		require.NoError(t, conn.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&email))
		return email
	}
	assert.Equal(t, "ana@x.org", emailOf(keeper), "the oldest active account keeps the address")
	assert.Equal(t, fmt.Sprintf("ana+duplicate-%d@x.org", oldestInactive), emailOf(oldestInactive), "a deactivated account gives way even when older")
	assert.Equal(t, fmt.Sprintf("ana+duplicate-%d@x.org", newer), emailOf(newer))
	assert.Equal(t, "bo@y.org", emailOf(single))
	assert.Equal(t, "no-at-sign", emailOf(noAt))
	assert.Equal(t, fmt.Sprintf("no-at-sign+duplicate-%d", noAtTwin), emailOf(noAtTwin))

	_, err = conn.Exec(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('x', 'BO@y.org', 'hash')`)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.Code)
	assert.Equal(t, "users_email_lower_key", pgErr.ConstraintName)
	assert.True(t, isDuplicateEmail(err), "the store reports it as email already exists")

	require.NoError(t, m.Migrate(emailLowerMigration-1))
	_, err = conn.Exec(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('x', 'BO@y.org', 'hash')`)
	require.NoError(t, err, "down restores the case-sensitive constraint")
	_, err = conn.Exec(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('x', 'bo@y.org', 'hash')`)
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "users_email_key", pgErr.ConstraintName)
}

// Every account is written through CreateUser or UpdateUser, so the admin
// form, the users API, the CSV import, the bootstrap admin and reset-admin
// all store emails lower-case, and one address in another case is a
// duplicate.
func TestStore_EmailsStoredLowerCase(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "jane.doe@agency.org", "jd@agency.org", "other@agency.org")
	t.Cleanup(func() { cleanupTestUsers(t, store, "jane.doe@agency.org", "jd@agency.org", "other@agency.org") })

	jane, err := store.CreateUser(ctx, "Jane", "Jane.Doe@Agency.ORG", "password-1", roleDriver)
	require.NoError(t, err)
	assert.Equal(t, "jane.doe@agency.org", jane.Email)

	_, err = store.CreateUser(ctx, "Jane again", "JANE.DOE@agency.org", "password-1", roleDriver)
	assert.ErrorIs(t, err, ErrDuplicateEmail)

	updated, err := store.UpdateUser(ctx, jane.ID, "Jane", "JD@Agency.org", roleDriver)
	require.NoError(t, err)
	assert.Equal(t, "jd@agency.org", updated.Email)

	other, err := store.CreateUser(ctx, "Other", "other@agency.org", "password-1", roleDriver)
	require.NoError(t, err)
	_, err = store.UpdateUser(ctx, other.ID, "Other", "Jd@agency.ORG", roleDriver)
	assert.ErrorIs(t, err, ErrDuplicateEmail)

	var stored string
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, jane.ID).Scan(&stored))
	assert.Equal(t, "jd@agency.org", stored)
}

// The lookup compares in any case rather than counting on the stored case,
// so a row written some other way is still found.
func TestStore_GetUserByEmail_AnyCase(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "raw.case@example.org")
	t.Cleanup(func() { cleanupTestUsers(t, store, "raw.case@example.org") })
	_, err := store.pool.Exec(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('Raw', 'Raw.Case@Example.org', 'hash')`)
	require.NoError(t, err)

	for _, email := range []string{"raw.case@example.org", "RAW.CASE@EXAMPLE.ORG", "Raw.Case@Example.org"} {
		u, err := store.GetUserByEmail(ctx, email)
		require.NoError(t, err, email)
		assert.Equal(t, "Raw.Case@Example.org", u.Email)
	}
	_, err = store.GetUserByEmail(ctx, "raw.case@example.com")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// A driver signs in with the email typed in another case than it was entered.
func TestLogin_EmailInAnotherCase(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "jane.doe@agency.org")
	t.Cleanup(func() { cleanupTestUsers(t, store, "jane.doe@agency.org") })
	_, err := store.CreateUser(ctx, "Jane", "Jane.Doe@Agency.org", "jane-pass-123", roleDriver)
	require.NoError(t, err)

	limiter := NewLoginRateLimiter()
	t.Cleanup(limiter.Stop)
	h := handleLogin(store, store, testSecret, testTTLs, limiter, false)
	for _, email := range []string{"Jane.Doe@Agency.org", "JANE.DOE@AGENCY.ORG", "jane.doe@agency.org"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"`+email+`","password":"jane-pass-123"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", email, w.Body.String())
		var resp LoginResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.Token, email)
	}
}

// The admin sign-in form matches the email the same way.
func TestAdminLogin_EmailInAnotherCase(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cleanupTestUsers(t, store, "ops@agency.org")
	t.Cleanup(func() { cleanupTestUsers(t, store, "ops@agency.org") })
	_, err := store.CreateUser(ctx, "Ops", "Ops@Agency.org", "ops-pass-1234", roleAdmin)
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ui, err := newAdminUI(store, tracker, newAlertWatcher(store, testAlertSettings), nil, newFeedHealth(time.Now), testSecret, NewLoginRateLimiter(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute})
	require.NoError(t, err)
	t.Cleanup(ui.loginLimiter.Stop)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	form := url.Values{"email": {"OPS@AGENCY.ORG"}, "password": {"ops-pass-1234"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Equal(t, "/admin/dashboard", w.Header().Get("Location"))
	assert.True(t, strings.Contains(w.Header().Get("Set-Cookie"), sessionCookieName+"="), "signed in")
}

// Typing an email in another case must not buy a fresh window of attempts,
// now that every case reaches the same account.
func TestLoginRateLimiter_EmailCaseSharesWindow(t *testing.T) {
	l := NewLoginRateLimiter()
	t.Cleanup(l.Stop)
	variants := []string{"ana@x.org", "Ana@x.org", "ANA@x.org", "aNa@X.org", "ana@X.ORG"}
	require.Len(t, variants, loginEmailLimit)
	for i, email := range variants {
		require.True(t, l.Allow(fmt.Sprintf("10.0.0.%d", i), email), email)
	}
	assert.False(t, l.Allow("10.0.1.1", "AnA@x.OrG"), "the sixth attempt in any case is refused")

	l.RecordSuccess("10.0.1.2", "ANA@X.ORG")
	assert.True(t, l.Allow("10.0.1.3", "ana@x.org"), "a success in any case clears the window")
}

// isDuplicateEmail must keep recognizing a duplicate after the constraint
// changed from users_email_key to users_email_lower_key.
func TestIsDuplicateEmail_LowerIndex(t *testing.T) {
	assert.True(t, isDuplicateEmail(&pgconn.PgError{Code: "23505", ConstraintName: "users_email_lower_key"}))
	assert.False(t, isDuplicateEmail(&pgconn.PgError{Code: "23505", ConstraintName: "user_vehicles_pkey"}))
	assert.False(t, isDuplicateEmail(errors.New("other")))
}
