package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPagesSaySignedOutNotNoAnswer: once the admin session ends (it lasts 24
// hours, and Sign out in another tab ends it too) every poll an open page
// makes is answered 401. The live map said "No answer from the server" and the
// header "no answer for 48 s", and Try again asked again and failed again. A
// 401 now reads as signed out, and the map's link signs in again and comes
// back to the map.
func TestPagesSaySignedOutNotNoAnswer(t *testing.T) {
	h := newTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code, "a poll with no session is answered, with a 401")
	require.Equal(t, "/admin/map", loginNext("/admin/map"), "signing in again can come back to the map")

	read := func(name string) string {
		t.Helper()
		b, err := fs.ReadFile(files, name)
		require.NoError(t, err)
		return string(b)
	}
	app, mapJS := read("web/static/js/app.js"), read("web/static/js/map.js")
	for _, want := range []string{
		`if (!r.ok) { var e = new Error("HTTP " + r.status); e.status = r.status; throw e; }`,
		`VT.signedOut = function (e) { return !!e && e.status === 401; };`,
		`VT.signIn = function () { return "/admin/login?next=" + encodeURIComponent(location.pathname + location.search); };`,
		`VT.lost(false, VT.signedOut(e));`,
		`age.textContent = out ? "signed out" : stale ?`,
	} {
		assert.Contains(t, app, want)
	}
	for _, want := range []string{
		`signedOut = VT.signedOut(e);`,
		`if (VT.lost) VT.lost(!lastOk, signedOut);`,
		`signedOut ? "You are signed out." + from`,
		`$("map-retry").href = signedOut ? VT.signIn() : "/admin/map";`,
		`if (signedOut) return; ev.preventDefault(); poll();`,
	} {
		assert.Contains(t, mapJS, want)
	}
}
