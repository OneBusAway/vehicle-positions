package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var railControl = regexp.MustCompile(`<(a|button)\b[^>]*>`)

// TestRail_EveryControlNamedWhenFolded: the live map, where signing in lands,
// opens with the menu rail folded, and folding hides each item's label with
// display: none, which takes it out of what a screen reader is told. Each rail
// control needs a name of its own (a title, as the page links have, or an
// aria-label). The brand link had none, so on the map it was read as a link
// with no name.
func TestRail_EveryControlNamedWhenFolded(t *testing.T) {
	css, err := os.ReadFile("web/styles/input.css")
	require.NoError(t, err)
	require.Contains(t, string(css), `.shell[data-rail="folded"] .rail-label { display: none; }`, "folding hides the labels")

	_, mux := newShellMux(t)
	body := getAdminPage(t, mux, "/admin/map")
	require.Contains(t, body, `<div class="shell" data-rail="folded" data-page="map">`, "the map opens folded")

	start := strings.Index(body, `<nav class="rail" aria-label="Main">`)
	require.GreaterOrEqual(t, start, 0)
	rail := body[start:]
	rail = rail[:strings.Index(rail, "</nav>")]
	tags := railControl.FindAllString(rail, -1)
	require.NotEmpty(t, tags)
	for _, tag := range tags {
		assert.True(t, strings.Contains(tag, ` title="`) || strings.Contains(tag, ` aria-label="`), "%s has no name of its own once the rail is folded", tag)
	}
	assert.Contains(t, rail, `<a class="rail-brand" href="/admin" title="Vehicle Tracker">`)
}
