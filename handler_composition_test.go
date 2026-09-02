package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHandler(t *testing.T, enabled bool) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, testSecret, time.Now(), adminUIConfig{enabled: enabled, stalenessThreshold: 5 * time.Minute}, nil)
	require.NoError(t, err)
	return h
}

func TestNewHandlerServesAPIAndAdmin(t *testing.T) {
	h := newTestHandler(t, true)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code, "admin page redirects to login when unauthenticated")
}

func TestNewHandlerAdminDisabled(t *testing.T) {
	h := newTestHandler(t, false)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNewHandlerCSRFRejectsCrossOriginPost(t *testing.T) {
	h := newTestHandler(t, true)
	form := url.Values{"email": {"a@b.c"}, "password": {"x"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code, "cross-site browser POST must be rejected")
}

func TestNewHandlerCSRFAllowsHeaderlessClients(t *testing.T) {
	h := newTestHandler(t, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.c","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusForbidden, w.Code, "non-browser clients (no Sec-Fetch-Site/Origin) pass CSRF")
}
