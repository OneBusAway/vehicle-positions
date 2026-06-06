package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadAdminTemplates populates the package-level templates var for handler
// tests, mirroring what registerAdminUI does at startup.
func loadAdminTemplates(t *testing.T) {
	t.Helper()
	tmpls, err := loadTemplates()
	require.NoError(t, err)
	templates = tmpls
}

func TestLoadTemplates(t *testing.T) {
	tmpls, err := loadTemplates()
	require.NoError(t, err)
	require.NotNil(t, tmpls)

	for _, view := range []string{"dashboard.html", "map.html", "trips.html", "users.html", "vehicles.html"} {
		assert.Contains(t, tmpls.admin, view, "admin view %q should be parsed", view)
	}
	assert.Contains(t, tmpls.public, "login.html")
}

func TestAdminHandlersRenderOK(t *testing.T) {
	loadAdminTemplates(t)

	cases := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		want    string
	}{
		{"dashboard", AdminDashboardHandler, "/admin/dashboard", "Bus 001"},
		{"vehicles", AdminVehiclesHandler, "/admin/vehicles", "Bus 001"},
		{"users", AdminUsersHandler, "/admin/users", "Chaitanya K"},
		{"trips", AdminTripsHandler, "/admin/trips", "Route A"},
		{"map", AdminMapHandler, "/admin/map", "Live Map"},
		{"login", AdminLoginHandler, "/admin/login", "Welcome"},
		{"signup", AdminSignupHandler, "/admin/signup", "Create Account"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			tc.handler(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}
}

// TestRenderUnknownViewWritesCleanError verifies that rendering a view absent
// from the template set yields a clean 500 rather than silently falling back to
// another template or writing a partial 200 body.
func TestRenderUnknownViewWritesCleanError(t *testing.T) {
	loadAdminTemplates(t)

	for _, set := range []map[string]*template.Template{templates.admin, templates.public} {
		rec := httptest.NewRecorder()
		render(rec, set, "ghost.html", "base.html", map[string]interface{}{})

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "internal server error")
	}
}

// TestAdminUIEnabledFlag pins the gate that keeps the unauthenticated admin UI
// off by default — the single safety mechanism behind the feature.
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
	render(rec, set, "boom.html", "base.html", map[string]interface{}{"Items": []int{}})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal server error")
	assert.NotContains(t, rec.Body.String(), "PARTIAL-OUTPUT")
}

func TestRegisterAdminUI(t *testing.T) {
	mux := http.NewServeMux()
	require.NoError(t, registerAdminUI(mux))

	t.Run("admin route is served", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("static asset is served from embedded fs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/js/admin.js", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEmpty(t, rec.Body.String())
	})
}
