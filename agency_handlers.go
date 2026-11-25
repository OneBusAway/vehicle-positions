package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// driverAppConfig is what the agency has set for the driver apps, fixed at
// startup. Each is nil when it has not been.
type driverAppConfig struct {
	mapPMTilesURL    *string
	privacyPolicyURL *string
}

// agencyResponse tells the driver app about the agency it reports to.
// PrivacyPolicyURL is null when the agency has not set one.
type agencyResponse struct {
	PrivacyPolicyURL *string `json:"privacy_policy_url"`
}

func handleAgency(privacyPolicyURL *string) http.HandlerFunc {
	body := agencyResponse{PrivacyPolicyURL: privacyPolicyURL}
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, body)
	}
}

// parsePrivacyPolicyURL reads PRIVACY_POLICY_URL. Empty or only whitespace means
// the agency has set none, and returns nil. Anything else must be an https URL
// with a host and no username or password: the apps open it in the phone's
// browser as it is. As with MAP_PMTILES_URL, one they could not open, or that
// would hand every phone a credential, is refused at startup.
func parsePrivacyPolicyURL(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		// Not wrapped: url.Parse's error repeats the whole value.
		return nil, errors.New("PRIVACY_POLICY_URL is not a valid URL")
	}
	// Hostname, not Host: "https://:8080/privacy" has a port but no host.
	if u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("PRIVACY_POLICY_URL must be an https URL with a host, got %q", mapURLForLog(u))
	}
	if u.User != nil {
		return nil, errors.New("PRIVACY_POLICY_URL must not carry a username or password: it is sent to every driver's phone")
	}
	return &v, nil
}
