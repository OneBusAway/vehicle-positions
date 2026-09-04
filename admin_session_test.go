package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetSessionCookieAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	setSessionCookie(w, req, "tok123", false)
	res := w.Result()
	require.Len(t, res.Cookies(), 1)
	c := res.Cookies()[0]
	assert.Equal(t, sessionCookieName, c.Name)
	assert.Equal(t, "tok123", c.Value)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.Equal(t, 24*60*60, c.MaxAge)
	assert.False(t, c.Secure, "plain HTTP without trusted proxy")

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	req2.Header.Set("X-Forwarded-Proto", "https")
	setSessionCookie(w2, req2, "tok123", true)
	assert.True(t, w2.Result().Cookies()[0].Secure, "trusted proxy + https → Secure")
}

func TestRequireAdminPage(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireAdminPage(testSecret, newFakeRevocations())(next)

	cases := []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{
		{"no cookie", nil, http.StatusSeeOther},
		{"garbage cookie", &http.Cookie{Name: sessionCookieName, Value: "garbage"}, http.StatusSeeOther},
		{"driver role", cookieFor(t, "driver"), http.StatusSeeOther},
		{"admin role", cookieFor(t, "admin"), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
			if tc.want == http.StatusSeeOther {
				assert.Equal(t, "/admin/login", w.Header().Get("Location"))
			}
		})
	}
}

func cookieFor(t *testing.T, role string) *http.Cookie {
	t.Helper()
	tok, err := generateJWT(&User{ID: 9, Email: role + "@test.com", Role: role, Active: true}, testSecret)
	require.NoError(t, err)
	return &http.Cookie{Name: sessionCookieName, Value: tok}
}

func TestFlashRoundTrip(t *testing.T) {
	w := httptest.NewRecorder()
	setFlash(w, "vehicle_created")
	c := w.Result().Cookies()[0]
	assert.Equal(t, flashCookieName, c.Name)
	assert.True(t, c.HttpOnly)

	req := httptest.NewRequest(http.MethodGet, "/admin/vehicles", nil)
	req.AddCookie(c)
	w2 := httptest.NewRecorder()
	msg := takeFlash(w2, req)
	assert.Equal(t, "Vehicle created.", msg)
	// clearing set-cookie present
	require.NotEmpty(t, w2.Result().Cookies())
	assert.Equal(t, -1, w2.Result().Cookies()[0].MaxAge)

	// unknown code renders nothing
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.AddCookie(&http.Cookie{Name: flashCookieName, Value: "<script>x</script>"})
	assert.Equal(t, "", takeFlash(httptest.NewRecorder(), req3))
}

// TestAdminCookiePath_RejectsRevokedToken is the divergence guard from the
// plan's D2: requireAuth and the admin UI's cookie session validate the same
// JWT through parseSessionToken, so a token revoked through
// POST /api/v1/auth/logout must end the browser session too. Without this
// test the two paths could silently drift apart.
func TestAdminCookiePath_RejectsRevokedToken(t *testing.T) {
	cookie := cookieFor(t, "admin")
	revocations := newFakeRevocations()
	revocations.revoked[jtiOf(t, cookie.Value)] = struct{}{}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	requireAdminPage(testSecret, revocations)(next).ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code, "a revoked session cookie must not reach an admin page")
	assert.Equal(t, "/admin/login", w.Header().Get("Location"))
	assert.False(t, reached, "the page handler must not run")
}

// TestAdminCookiePath_CheckerErrorFailsClosed mirrors
// TestRequireAuth_CheckerErrorFailsClosed for the browser path: an
// undecidable revocation check sends the visitor to the login page rather
// than through to the page.
func TestAdminCookiePath_CheckerErrorFailsClosed(t *testing.T) {
	revocations := newFakeRevocations()
	revocations.err = errors.New("database unavailable")

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	requireAdminPage(testSecret, revocations)(next).ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/login", w.Header().Get("Location"))
}
