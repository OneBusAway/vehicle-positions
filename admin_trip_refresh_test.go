package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTripPageRefreshKeepsWhatIsInUse: an active trip's page swaps in a fresh
// copy of its header, facts, stops and gaps every 10 s. End trip is a confirm
// form in the header, and the dialog submits the form it was opened from, so a
// swap while the dialog is open left End trip doing nothing. The swap now waits
// for the dialog to close, and finds the control the keyboard was on again in
// the fresh copy instead of dropping focus to the page.
func TestTripPageRefreshKeepsWhatIsInUse(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	trip, points := fixtureTrip(t, ix, 1)
	trip.Status = "active"
	body := getAdminPage(t, newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r), "/admin/trips/1")
	header, end, facts := strings.Index(body, `id="trip-header"`), strings.Index(body, `<form method="post" action="/admin/trips/1/end" data-confirm=`), strings.Index(body, `id="trip-facts"`)
	require.True(t, header >= 0 && end >= 0 && facts >= 0, "the page has its header, End trip and its facts")
	require.True(t, header < end && end < facts, "End trip's confirm form is in the header the refresh replaces")

	script, err := fs.ReadFile(files, "web/static/js/trip_detail.js")
	require.NoError(t, err)
	src := string(script)
	start := strings.Index(src, "async function refreshSections() {")
	require.GreaterOrEqual(t, start, 0)
	refresh := src[start:]
	refresh = refresh[:strings.Index(refresh, "\n  }\n")]

	replace := strings.Index(refresh, "old.replaceWith(fresh);")
	require.GreaterOrEqual(t, replace, 0)
	wait := strings.Index(refresh, `if (document.querySelector("dialog[open]")) return false;`)
	assert.True(t, wait >= 0 && wait < replace, "the sections are not replaced while a dialog is open")
	find := strings.Index(refresh, "refocus = twin(focused, old, fresh);")
	assert.True(t, find >= 0 && find < replace, "the focused control's counterpart is found before the swap")
	assert.Greater(t, strings.Index(refresh, "refocus.focus({ preventScroll: true });"), replace, "and focused after it")
}
