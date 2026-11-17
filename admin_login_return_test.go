package main

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// loginLocation is where requireAdminPage sends a request without an admin
// session: a GET carries the page it asked for as next, any other method
// gets the plain login page.
func loginLocation(method, target string) string {
	if method != http.MethodGet {
		return "/admin/login"
	}
	return "/admin/login?" + url.Values{"next": {target}}.Encode()
}

// unsafeLoginNexts are next values a sign-in must not return to. Each one
// sends the admin to the dashboard instead.
var unsafeLoginNexts = []struct {
	name string
	next string
}{
	{"empty", ""},
	{"another host", "//evil.example"},
	{"another host with an admin path", "//evil.example/admin/trips"},
	{"backslash host", `/\evil.example`},
	{"scheme and host", "https://evil.example/admin/trips"},
	{"scheme without a host", "http:/admin/trips"},
	{"javascript scheme", "javascript:alert(1)"},
	{"encoded //", "%2F%2Fevil.example"},
	{"the login page", "/admin/login"},
	{"the login page with its own next", "/admin/login?next=%2Fadmin%2Ftrips"},
	{"the logout page", "/admin/logout"},
	{"outside /admin", "/api/v1/admin/users"},
	{"the bare /admin", "/admin"},
	{"a name that only starts with admin", "/administrator/x"},
	{"dot segments out of /admin", "/admin/../api/v1/admin/users"},
	{"encoded dot segments out of /admin", "/admin/%2e%2e/api/v1/admin/users"},
	{"control character", "/admin/trips\n/evil"},
}

func TestLoginNext(t *testing.T) {
	for _, tc := range unsafeLoginNexts {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			assert.Empty(t, loginNext(tc.next))
		})
	}

	safe := []struct {
		name string
		next string
		want string
	}{
		{"a page", "/admin/trips/42", "/admin/trips/42"},
		{"keeps the query", "/admin/trips?status=active&page=2", "/admin/trips?status=active&page=2"},
		{"cleans a dot segment that stays in /admin", "/admin/users/../trips", "/admin/trips"},
		// http.Redirect cleans the raw text, where %2F is not a separator, so
		// returning this as written would send Location: /\evil.example, which
		// browsers read as //evil.example. Rebuilding from the decoded path
		// keeps it under /admin/.
		{"rebuilds what raw-text cleaning would turn into /\\", `/admin/a%2Fb/../../\evil.example`, "/admin/%5Cevil.example"},
	}
	for _, tc := range safe {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, loginNext(tc.next))
		})
	}
}

func TestRequireAdminPage_CarriesNext(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	expired, err := generateJWT(&User{ID: 9, Email: "admin@test.com", Role: roleAdmin, Active: true}, testSecret, -time.Minute)
	require.NoError(t, err)

	cases := []struct {
		name   string
		method string
		target string
		cookie *http.Cookie
		want   string
	}{
		{"signed out", http.MethodGet, "/admin/trips?status=active", nil, "/admin/login?next=%2Fadmin%2Ftrips%3Fstatus%3Dactive"},
		{"expired session", http.MethodGet, "/admin/users/7/edit", &http.Cookie{Name: sessionCookieName, Value: expired}, "/admin/login?next=%2Fadmin%2Fusers%2F7%2Fedit"},
		{"a POST does not carry next", http.MethodPost, "/admin/vehicles", nil, "/admin/login"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, tc.want, w.Header().Get("Location"))
		})
	}
}

var loginNextField = regexp.MustCompile(`<input type="hidden" name="next" value="([^"]*)">`)

// formNext returns the login form's hidden next value, unescaped the way a
// browser reads it, and whether the form has one.
func formNext(body string) (string, bool) {
	m := loginNextField.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return html.UnescapeString(m[1]), true
}

func TestAdminLoginReturnsToNext(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcryptCost)
	require.NoError(t, err)
	admin := &User{ID: 1, Email: "boss@test.com", PasswordHash: string(hash), Role: roleAdmin, Active: true}
	ui := newTestAdminUI(t)
	ui.users = &fakeUserFetcher{users: map[string]*User{admin.Email: admin}}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	get := func(target string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	post := func(password, next string) *httptest.ResponseRecorder {
		form := url.Values{"email": {admin.Email}, "password": {password}, "next": {next}}
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("signed out link to signed in page", func(t *testing.T) {
		gate := get("/admin/trips?status=active&page=2", nil)
		require.Equal(t, http.StatusSeeOther, gate.Code)

		page := get(gate.Header().Get("Location"), nil)
		require.Equal(t, http.StatusOK, page.Code)
		next, ok := formNext(page.Body.String())
		require.True(t, ok, "the login form must carry next")
		assert.Equal(t, "/admin/trips?status=active&page=2", next)

		signIn := post("password123", next)
		assert.Equal(t, http.StatusSeeOther, signIn.Code)
		assert.Equal(t, "/admin/trips?status=active&page=2", signIn.Header().Get("Location"))
		require.NotEmpty(t, signIn.Result().Cookies())
		assert.Equal(t, sessionCookieName, signIn.Result().Cookies()[0].Name)
	})

	t.Run("login page leaves an unsafe next out of the form", func(t *testing.T) {
		page := get("/admin/login?"+url.Values{"next": {"//evil.example"}}.Encode(), nil)
		require.Equal(t, http.StatusOK, page.Code)
		_, ok := formNext(page.Body.String())
		assert.False(t, ok)
	})

	for _, tc := range unsafeLoginNexts {
		t.Run("sign-in with "+tc.name+" goes to the dashboard", func(t *testing.T) {
			w := post("password123", tc.next)
			assert.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, "/admin/dashboard", w.Header().Get("Location"))
		})
	}

	t.Run("failed sign-in keeps next in the form", func(t *testing.T) {
		w := post("wrong", "/admin/trips/42")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		next, ok := formNext(w.Body.String())
		require.True(t, ok, "the re-rendered form must still carry next")
		assert.Equal(t, "/admin/trips/42", next)
	})

	t.Run("failed sign-in drops an unsafe next", func(t *testing.T) {
		w := post("wrong", "https://evil.example/admin/trips")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		_, ok := formNext(w.Body.String())
		assert.False(t, ok)
	})

	t.Run("already signed in goes straight to next", func(t *testing.T) {
		w := get("/admin/login?next=%2Fadmin%2Ftrips%2F42", cookieFor(t, roleAdmin))
		assert.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/trips/42", w.Header().Get("Location"))
	})

	t.Run("already signed in with an unsafe next goes to the dashboard", func(t *testing.T) {
		w := get("/admin/login?next=%2F%2Fevil.example", cookieFor(t, roleAdmin))
		assert.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/dashboard", w.Header().Get("Location"))
	})
}
