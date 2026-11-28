package main

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The script marks the link that was followed and the page that waits, and
// the stylesheet draws both. The two must use the same names, and the line
// must be held back 150 ms whether or not motion is reduced, so a page that
// comes quickly never shows one.
func TestShell_AClickIsAnsweredAtOnce(t *testing.T) {
	script, err := fs.ReadFile(files, "web/static/js/app.js")
	require.NoError(t, err)
	css, err := fs.ReadFile(files, "web/static/css/admin.css")
	require.NoError(t, err)

	for _, want := range []string{
		`pressed.classList.add("going")`,
		`pressed.classList.remove("going")`,
		`document.documentElement.classList.add("waiting")`,
		`document.documentElement.classList.remove("waiting")`,
		// A page brought back by Back is cleared, and so is one left waiting.
		`window.addEventListener("pageshow", arrived)`,
		`waited = setTimeout(arrived, 10000)`,
	} {
		assert.Contains(t, string(script), want)
	}

	lines := regexp.MustCompile(`html\.waiting:after\{[^}]*\}`).FindAllString(string(css), -1)
	require.Len(t, lines, 2, "the line, and its growth where motion is wanted")
	for _, line := range lines {
		assert.Regexp(t, `animation:[^;}]*\.15s`, line, "held back 150 ms")
	}
	assert.Contains(t, lines[0], "wait-shown")
	assert.Contains(t, lines[1], "wait-line")
	assert.Regexp(t, `@media \(prefers-reduced-motion:no-preference\)\{[^@]*html\.waiting:after\{animation:wait-line`, string(css), "the line grows only where motion is wanted")

	assert.Regexp(t, `\.going\{background-image:linear-gradient\(var\(--pressed\),\s*var\(--pressed\)\)\}`, string(css))
	// The menu's choice moves to the link at once, and leaves the page's own.
	assert.Contains(t, string(css), `.rail-item.going{background:var(--selected);color:var(--on-selected)}`)
	assert.Regexp(t, `\.rail:has\(\.going\) \.rail-item\[aria-current=page\]:not\(\.going\)\{`, string(css))
	assert.Regexp(t, `\.tabbar:has\(\.going\) a\[aria-current=page\]:not\(\.going\)\{`, string(css))
	// The tint is set for light and for both ways to dark.
	assert.Len(t, regexp.MustCompile(`--pressed:#[0-9a-f]{8}`).FindAllString(string(css), -1), 3)
}
