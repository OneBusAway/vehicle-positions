package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPolicyURL = "https://transit.example.org/privacy?lang=en#drivers"

func TestParsePrivacyPolicyURL(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n"} {
		got, err := parsePrivacyPolicyURL(raw)
		require.NoError(t, err)
		assert.Nil(t, got, "%q is not set", raw)
	}
	for _, raw := range []string{testPolicyURL, "  " + testPolicyURL + "\n"} {
		got, err := parsePrivacyPolicyURL(raw)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, testPolicyURL, *got, "as it is, without the spaces around it")
	}

	rejected := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"plain http", "http://transit.example.org/privacy", "must be an https URL with a host"},
		{"no scheme", "transit.example.org/privacy", "must be an https URL with a host"},
		{"a script", "javascript:alert(1)", "must be an https URL with a host"},
		{"a file", "file:///srv/privacy.html", "must be an https URL with a host"},
		{"no host", "https:///privacy", "must be an https URL with a host"},
		{"a port but no host", "https://:8080/privacy", "must be an https URL with a host"},
		{"not a URL", "https://transit.example.org/%zz", "is not a valid URL"},
		{"a username and password", "https://agency:hunter2@transit.example.org/privacy", "must not carry a username or password"},
		{"a username alone", "https://agency@transit.example.org/privacy", "must not carry a username or password"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePrivacyPolicyURL(tt.raw)
			require.Error(t, err)
			assert.Nil(t, got)
			assert.ErrorContains(t, err, "PRIVACY_POLICY_URL "+tt.wantErr)
		})
	}
}

// The error goes into the startup log, so neither a password nor a token in the
// query may go with it, whichever check refuses the value.
func TestParsePrivacyPolicyURL_KeepsCredentialsOutOfTheError(t *testing.T) {
	for _, raw := range []string{
		"http://agency:hunter2@transit.example.org/privacy",
		"https://agency:hunter2@transit.example.org/%zz",
		"https://agency:hunter2@transit.example.org:abc/privacy",
		"http://transit.example.org/privacy?sig=hunter2",
	} {
		_, err := parsePrivacyPolicyURL(raw)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "hunter2", raw)
	}
}

// TestAgencyRoute_Wiring drives the production mux: the route takes a driver or
// admin token, refuses a viewer's and a rider's, and serves the URL newMux was
// given, or null, never leaving the field out.
func TestAgencyRoute_Wiring(t *testing.T) {
	token := func(role string) string {
		signed, err := generateJWT(&User{ID: 1, Email: "x@test.com", Role: role}, testSecret, defaultAccessTokenTTL)
		require.NoError(t, err)
		return "Bearer " + signed
	}
	riderToken, err := generateRiderJWT("rider-1", testSecret, time.Hour)
	require.NoError(t, err)
	policyURL := testPolicyURL
	ask := func(config *driverAppConfig, auth string) *httptest.ResponseRecorder {
		mux := newMux(&noopStore{}, nil, nil, testSecret, testTTLs, time.Time{}, nil, nil, false, false, "", nil, nil, config, newAlertWatcher(&noopStore{}, testAlertSettings), nil, newFeedHealth(time.Now))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agency", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	set := &driverAppConfig{privacyPolicyURL: &policyURL}
	const served = `{"privacy_policy_url":"` + testPolicyURL + `"}`

	tests := []struct {
		name   string
		config *driverAppConfig
		auth   string
		code   int
		body   string
	}{
		{"no token", set, "", http.StatusUnauthorized, `{"error":"missing or invalid authorization header"}`},
		{"a rider", set, "Bearer " + riderToken, http.StatusForbidden, `{"error":"forbidden"}`},
		{"a viewer", set, token(roleViewer), http.StatusForbidden, `{"error":"forbidden"}`},
		{"a driver", set, token(roleDriver), http.StatusOK, served},
		{"an admin", set, token(roleAdmin), http.StatusOK, served},
		{"none set", &driverAppConfig{}, token(roleDriver), http.StatusOK, `{"privacy_policy_url":null}`},
		{"no config", nil, token(roleDriver), http.StatusOK, `{"privacy_policy_url":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := ask(tt.config, tt.auth)
			assert.Equal(t, tt.code, w.Code)
			assert.JSONEq(t, tt.body, w.Body.String())
		})
	}
}
