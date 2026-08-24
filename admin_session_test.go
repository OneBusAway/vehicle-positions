package main

import (
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
	h := requireAdminPage(testSecret)(next)

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
