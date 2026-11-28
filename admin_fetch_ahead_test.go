package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchedAhead(t *testing.T) {
	for _, tc := range []struct {
		purpose string
		want    bool
	}{
		{"prefetch", true},
		// What Chrome sends when it also draws the page ahead of the click.
		{"prefetch;prerender", true},
		{"prefetch;anonymous-client-ip", true},
		{"", false},
		{"navigate", false},
		{"not-a-prefetch", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/admin/trips", nil)
		if tc.purpose != "" {
			r.Header.Set("Sec-Purpose", tc.purpose)
		}
		assert.Equal(t, tc.want, fetchedAhead(r), "Sec-Purpose: %q", tc.purpose)
	}
}

// A page fetched while the pointer rests on its link may never be shown. The
// message for the next page is still there for the page that is.
func TestShell_APageFetchedAheadLeavesTheFlash(t *testing.T) {
	_, mux := newShellMux(t)
	get := func(purpose string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/admin/vehicles", nil)
		req.AddCookie(cookieFor(t, "admin"))
		req.AddCookie(&http.Cookie{Name: flashCookieName, Value: "vehicle_created"})
		if purpose != "" {
			req.Header.Set("Sec-Purpose", purpose)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w
	}

	ahead := get("prefetch")
	assert.NotContains(t, ahead.Body.String(), "data-toast")
	assert.Empty(t, ahead.Result().Cookies(), "the flash cookie is not cleared")

	shown := get("")
	assert.Contains(t, shown.Body.String(), "<span>Vehicle created.</span>")
	require.Len(t, shown.Result().Cookies(), 1)
	assert.Equal(t, flashCookieName, shown.Result().Cookies()[0].Name)
	assert.Equal(t, -1, shown.Result().Cookies()[0].MaxAge)
}

// prefetchRule is the speculation rule the shell's script puts in the page.
type prefetchRule struct {
	Prefetch []struct {
		Where struct {
			And []struct {
				HrefMatches string `json:"href_matches"`
				Not         struct {
					HrefMatches string `json:"href_matches"`
				} `json:"not"`
			} `json:"and"`
		} `json:"where"`
		Eagerness string `json:"eagerness"`
	} `json:"prefetch"`
}

// The rule fetches admin pages ahead and nothing else: a download under
// /admin/ is not a page, and fetching one ahead only wastes it. Every GET
// route there that names a file must be one the rule leaves out, so a new
// kind of download fails here until the rule knows it.
func TestShell_FetchesPagesAheadButNotDownloads(t *testing.T) {
	script, err := fs.ReadFile(files, "web/static/js/app.js")
	require.NoError(t, err)
	found := regexp.MustCompile(`ahead\.textContent = '(\{[^']+\})';`).FindSubmatch(script)
	require.NotNil(t, found, "app.js sets the prefetch rule")
	var rule prefetchRule
	require.NoError(t, json.Unmarshal(found[1], &rule), "the rule is JSON")

	require.Len(t, rule.Prefetch, 1)
	// moderate: when the pointer has rested on a link, or at the press.
	assert.Equal(t, "moderate", rule.Prefetch[0].Eagerness)
	require.Len(t, rule.Prefetch[0].Where.And, 2)
	assert.Equal(t, "/admin/*", rule.Prefetch[0].Where.And[0].HrefMatches)
	leftOut := rule.Prefetch[0].Where.And[1].Not.HrefMatches
	require.Equal(t, "/admin/*.csv", leftOut)

	downloads := 0
	for _, route := range extractRegisteredRoutes(t) {
		if route.method != http.MethodGet || !strings.HasPrefix(route.path, "/admin/") || path.Ext(route.path) == "" {
			continue
		}
		downloads++
		assert.Equal(t, path.Ext(leftOut), path.Ext(route.path), "%s serves a file, which the prefetch rule in app.js must leave out", route)
	}
	assert.Equal(t, 1, downloads, "the CSV template is the admin's one download")

	// A copy fetched ahead is dropped after ten seconds: the rule comes out,
	// and goes back in only later, or Chrome never sees it gone.
	assert.Regexp(t, `setInterval\(function \(\) \{ ahead\.remove\(\); setTimeout\(fetchAhead, 100\); \}, 10000\);`, string(script))
}
