package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMapURL = "https://maps.example.org/agency.pmtiles"

func TestParseMapPMTilesURL(t *testing.T) {
	accepted := []struct {
		name string
		raw  string
		want string
	}{
		{"https", testMapURL, testMapURL},
		{"surrounding spaces are trimmed", "  " + testMapURL + "\n", testMapURL},
		// Android blocks it in release builds, but it is a valid URL and a
		// development server is reachable over it; main logs a warning.
		{"plain http", "http://10.0.2.2:8090/agency.pmtiles", "http://10.0.2.2:8090/agency.pmtiles"},
		{"the scheme in capitals", "HTTPS://maps.example.org/agency.pmtiles", "HTTPS://maps.example.org/agency.pmtiles"},
		{"a query string is kept", testMapURL + "?v=20261005", testMapURL + "?v=20261005"},
		{"an IPv6 host with a port", "http://[::1]:8090/agency.pmtiles", "http://[::1]:8090/agency.pmtiles"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMapPMTilesURL(tc.raw)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
		})
	}

	unset := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"spaces", "   "},
		{"a tab and a newline", "\t\n"},
	}
	for _, tc := range unset {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMapPMTilesURL(tc.raw)
			require.NoError(t, err)
			assert.Nil(t, got, "a blank value means the agency has no map file")
		})
	}

	rejected := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"no scheme", "maps.example.org/agency.pmtiles", "must be an http or https URL with a host"},
		{"a file path", "/srv/maps/agency.pmtiles", "must be an http or https URL with a host"},
		{"ftp", "ftp://maps.example.org/agency.pmtiles", "must be an http or https URL with a host"},
		{"file", "file:///srv/maps/agency.pmtiles", "must be an http or https URL with a host"},
		// The form MapLibre takes in a style, not a URL a phone can download from.
		{"MapLibre's pmtiles prefix", "pmtiles://" + testMapURL, "must be an http or https URL with a host"},
		{"no host", "https:///agency.pmtiles", "must be an http or https URL with a host"},
		{"a port but no host", "https://:8080/agency.pmtiles", "must be an http or https URL with a host"},
		{"not a URL", "https://maps.example.org/%zz", "is not a valid URL"},
		{"a username and password", "https://agency:hunter2@maps.example.org/agency.pmtiles", "must not carry a username or password"},
		{"a username alone", "https://agency@maps.example.org/agency.pmtiles", "must not carry a username or password"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMapPMTilesURL(tc.raw)
			require.Error(t, err)
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), "MAP_PMTILES_URL", "the operator has to be told which setting is wrong")
		})
	}
}

// The error goes into the startup log, so neither a password nor a token in the
// query string may go with it, whichever check refuses the value.
func TestParseMapPMTilesURL_KeepsCredentialsOutOfTheError(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"the wrong scheme", "ftp://agency:hunter2@maps.example.org/agency.pmtiles"},
		{"a bad escape in the path", "https://agency:hunter2@maps.example.org/%zz"},
		{"a bad port", "https://agency:hunter2@maps.example.org:abc/agency.pmtiles"},
		{"a bad escape in the password", "https://agency:hunter2%zz@maps.example.org/agency.pmtiles"},
		{"a token in the query", "ftp://maps.example.org/agency.pmtiles?sig=hunter2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseMapPMTilesURL(tc.raw)

			require.Error(t, err)
			assert.NotContains(t, err.Error(), "hunter2")
		})
	}
}

func TestHandleMapConfig_ServesTheURL(t *testing.T) {
	mapURL := testMapURL
	w := httptest.NewRecorder()

	handleMapConfig(&mapURL).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/map", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"pmtiles_url":"`+testMapURL+`"}`, w.Body.String())
}

// With no map file the field is null, never left out: the spec lists it as
// required, and it is how a client tells "no map file" apart from a server
// older than the route, which answers 404.
func TestHandleMapConfig_NullWithoutAMapFile(t *testing.T) {
	w := httptest.NewRecorder()

	handleMapConfig(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/map", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"pmtiles_url":null}`, w.Body.String())
}

// TestMapRoute_Wiring drives the production mux: the route takes a driver or
// admin token, refuses a rider's, and serves the URL newMux was given.
func TestMapRoute_Wiring(t *testing.T) {
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	adminToken, err := generateJWT(&User{ID: 2, Email: "admin@test.com", Role: "admin"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	riderToken, err := generateRiderJWT("rider-1", testSecret, time.Hour)
	require.NoError(t, err)
	mapURL := testMapURL
	mux := newMux(&noopStore{}, nil, nil, testSecret, testTTLs, time.Time{}, nil, false, false, nil, nil, &mapURL)

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantBody   string
	}{
		{"no token", "", http.StatusUnauthorized, `{"error":"missing or invalid authorization header"}`},
		{"rider token", "Bearer " + riderToken, http.StatusForbidden, `{"error":"forbidden"}`},
		{"driver token", "Bearer " + driverToken, http.StatusOK, `{"pmtiles_url":"` + testMapURL + `"}`},
		{"admin token", "Bearer " + adminToken, http.StatusOK, `{"pmtiles_url":"` + testMapURL + `"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/map", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
			assert.JSONEq(t, tc.wantBody, w.Body.String())
		})
	}
}

// TestMapRoute_RegisteredWithoutAMapFile pins the other half of the contract:
// a server with no map file still serves the route, with a null URL, so a 404
// can only mean a server older than the route.
func TestMapRoute_RegisteredWithoutAMapFile(t *testing.T) {
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	mux := newMux(&noopStore{}, nil, nil, testSecret, testTTLs, time.Time{}, nil, false, false, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/map", nil)
	req.Header.Set("Authorization", "Bearer "+driverToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"pmtiles_url":null}`, w.Body.String())
}

// TestNewHandler_PassesTheMapURLOn pins the last link before main: newHandler
// hands the URL to the mux it builds.
func TestNewHandler_PassesTheMapURLOn(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	loginLimiter := NewLoginRateLimiter()
	t.Cleanup(loginLimiter.Stop)
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	mapURL := testMapURL
	h, err := newHandler(&noopStore{}, tracker, nil, loginLimiter, testSecret, testTTLs, time.Now(),
		adminUIConfig{stalenessThreshold: 5 * time.Minute}, false, nil, nil, &mapURL)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/map", nil)
	req.Header.Set("Authorization", "Bearer "+driverToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"pmtiles_url":"`+testMapURL+`"}`, w.Body.String())
}

func TestLogMapPMTilesURL(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantLogged string
		wantWarn   bool
	}{
		{"https", testMapURL, testMapURL, false},
		{"plain http", "http://10.0.2.2:8090/agency.pmtiles", "http://10.0.2.2:8090/agency.pmtiles", true},
		{"plain http in capitals", "HTTP://maps.example.org/agency.pmtiles", "http://maps.example.org/agency.pmtiles", true},
		// Drivers still get the query; only the log leaves it out.
		{"a token in the query", testMapURL + "?sig=hunter2", testMapURL, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			original := slog.Default()
			t.Cleanup(func() { slog.SetDefault(original) })
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
			mapURL, err := parseMapPMTilesURL(tc.raw)
			require.NoError(t, err)

			logMapPMTilesURL(mapURL)

			logged := buf.String()
			assert.Contains(t, logged, `"url":"`+tc.wantLogged+`"`, "the operator must see which file drivers are sent to")
			assert.NotContains(t, logged, "hunter2")
			if tc.wantWarn {
				assert.Contains(t, logged, `"level":"WARN"`)
				assert.Contains(t, logged, "Android blocks cleartext downloads")
			} else {
				assert.NotContains(t, logged, `"level":"WARN"`)
			}
		})
	}

	t.Run("no map file", func(t *testing.T) {
		var buf bytes.Buffer
		original := slog.Default()
		t.Cleanup(func() { slog.SetDefault(original) })
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

		logMapPMTilesURL(nil)

		assert.Empty(t, buf.String())
	})
}
