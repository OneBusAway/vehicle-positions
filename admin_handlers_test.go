package main

import (
	"html/template"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadTemplates(t *testing.T) {
	tmpls, err := loadTemplates()
	require.NoError(t, err)
	require.NotNil(t, tmpls)

	for _, view := range []string{"dashboard.html", "map.html", "trips.html", "users.html", "vehicles.html"} {
		assert.Contains(t, tmpls.admin, view, "admin view %q should be parsed", view)
	}
	assert.Contains(t, tmpls.public, "login.html")
}

// TestRenderUnknownViewWritesCleanError verifies that rendering a view absent
// from the template set yields a clean 500 rather than silently falling back to
// another template or writing a partial 200 body.
func TestRenderUnknownViewWritesCleanError(t *testing.T) {
	tmpls, err := loadTemplates()
	require.NoError(t, err)

	for _, set := range []map[string]*template.Template{tmpls.admin, tmpls.public} {
		rec := httptest.NewRecorder()
		renderInto(rec, set, "ghost.html", "base.html", map[string]interface{}{})

		assert.Equal(t, 500, rec.Code)
		assert.Contains(t, rec.Body.String(), "internal server error")
	}
}

// TestAdminUIEnabledFlag pins the gate that keeps the admin UI off by
// default — the single safety mechanism behind the feature until it's
// enabled deliberately.
func TestAdminUIEnabledFlag(t *testing.T) {
	cases := map[string]bool{
		"true":     true,
		"1":        true,
		"TRUE":     true,
		"t":        true,
		"false":    false,
		"0":        false,
		"":         false,
		"nonsense": false,
	}

	for val, want := range cases {
		t.Run("val="+val, func(t *testing.T) {
			t.Setenv("ADMIN_UI_ENABLED", val)
			assert.Equal(t, want, adminUIEnabled())
		})
	}
}

// TestRenderExecutionErrorIsCleanError verifies the buffered-write contract: a
// template that fails partway through must not leak partial output — the client
// gets a clean 500, not a half-written 200.
func TestRenderExecutionErrorIsCleanError(t *testing.T) {
	tmpl := template.Must(template.New("base.html").Parse(`PARTIAL-OUTPUT{{index .Items 99}}`))
	set := map[string]*template.Template{"boom.html": tmpl}

	rec := httptest.NewRecorder()
	renderInto(rec, set, "boom.html", "base.html", map[string]interface{}{"Items": []int{}})

	assert.Equal(t, 500, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal server error")
	assert.NotContains(t, rec.Body.String(), "PARTIAL-OUTPUT")
}
