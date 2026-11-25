package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestStorableText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", true},
		{"ASCII", "Route 12", true},
		{"Devanagari", "बस", true},
		{"a replacement character, which is valid", "\uFFFD", true},
		{"a tab and a newline", "a\tb\n", true},
		{"a NUL", "a\x00b", false},
		{"a byte that starts no character", "\xff", false},
		{"a character cut short", "\xe0\xa4", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, storableText(tt.text))
		})
	}
}

func TestRefuseUnstorableQuery(t *testing.T) {
	tests := []struct {
		target string
		want   int
	}{
		{"/api/v1/admin/vehicles", http.StatusNoContent},
		{"/api/v1/admin/vehicles?q=%E0%A4%AC&agency_tag=metro", http.StatusNoContent},
		{"/api/v1/admin/vehicles?q=%00", http.StatusBadRequest},
		{"/api/v1/admin/vehicles?q=ok&q=%ff", http.StatusBadRequest},
		{"/admin/vehicles?q=%ff", http.StatusBadRequest},
		{"/admin/vehicles?q=%E0%A4%AC", http.StatusNoContent},
		// Outside the admin API and pages, no query value goes to the database.
		{"/gtfs-rt/vehicle-positions?format=%ff", http.StatusNoContent},
		{"/api/v1/rider/trips/T1/status?x=%ff", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			h := refuseUnstorableQuery(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.target, nil))

			assert.Equal(t, tt.want, w.Code)
			if tt.want == http.StatusBadRequest {
				assert.Contains(t, w.Body.String(), "query parameters must be UTF-8 with no NUL characters")
				assert.Equal(t, strings.HasPrefix(tt.target, "/api/"), strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"))
			}
		})
	}
}

// TestServer_TextPostgresCannotHold: text Postgres can't hold is the caller's
// mistake, through the whole server and a real database: a 400 in a search or a
// filter, and a wrong password at the sign-ins, which anyone can reach, where it
// is an email no account has. Nothing is logged as an error.
// Not safe for t.Parallel(); uses global logger.
func TestServer_TextPostgresCannotHold(t *testing.T) {
	store := newAuditTestStore(t)
	admin, err := store.CreateUser(context.Background(), "Ops", "aud-ops@example.org", "a-password", roleAdmin)
	require.NoError(t, err)
	session, err := generateJWT(&User{ID: admin.ID, Email: admin.Email, Role: roleAdmin}, testSecret, sessionLifetime)
	require.NoError(t, err)
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	newLimiter := func() *LoginRateLimiter {
		l := NewLoginRateLimiter()
		t.Cleanup(l.Stop)
		return l
	}
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(serverStore{store, &noopStore{}}, tracker, nil, newLimiter(), newLimiter(), nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	logs := captureLogs(t)

	for _, path := range []string{
		"/api/v1/admin/vehicles?q=", "/api/v1/admin/vehicles?agency_tag=",
		"/api/v1/admin/users?q=", "/api/v1/admin/trips?q=", "/api/v1/admin/trips?vehicle_id=",
		"/api/v1/admin/audit?type=vehicle&id=",
		"/admin/vehicles?q=", "/admin/vehicles?agency_tag=", "/admin/users?q=", "/admin/trips?q=", "/admin/audit?type=vehicle&id=",
	} {
		for _, text := range []string{"%00", "%ff"} {
			req := httptest.NewRequest(http.MethodGet, path+text, nil)
			req.Header.Set("Authorization", "Bearer "+session)
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, "%s%s: %s", path, text, w.Body.String())
		}
	}

	body, err := json.Marshal(LoginRequest{Email: "nobody\x00@example.org", Password: "a password"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	assert.Equal(t, "invalid email or password", decodeError(t, w))

	for _, email := range []string{"nobody\x00@example.org", "nobody\xff@example.org"} {
		form := url.Values{"email": {email}, "password": {"a password"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid email or password.")
	}
	assert.NotContains(t, logs.String(), "level=ERROR")
}
