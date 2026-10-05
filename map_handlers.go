package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// mapConfigResponse tells the driver app where to download the agency's map
// file from. PMTilesURL is null when the agency has not configured one.
type mapConfigResponse struct {
	PMTilesURL *string `json:"pmtiles_url"`
}

// handleMapConfig serves the driver app's map settings, fixed at startup. The
// server only hands out the URL: the agency hosts the file, and each phone
// downloads it from there.
func handleMapConfig(pmtilesURL *string) http.HandlerFunc {
	body := mapConfigResponse{PMTilesURL: pmtilesURL}
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, body)
	}
}

// parseMapPMTilesURL reads MAP_PMTILES_URL. Empty or only whitespace means the
// agency has no map file, and returns nil. Anything else must be an http or
// https URL with a host and no username or password. The value goes out as is
// to every driver's phone: one that could never be downloaded is refused at
// startup, while someone is watching the deploy, rather than failing on each
// phone in turn, and a credential in it would go to every phone along with it.
func parseMapPMTilesURL(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		// Not wrapped: url.Parse's error repeats the whole value, password
		// included, and this one goes into the startup log.
		return nil, errors.New("MAP_PMTILES_URL is not a valid URL")
	}
	// Hostname, not Host: "https://:8080/agency.pmtiles" has a port but no host.
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, fmt.Errorf("MAP_PMTILES_URL must be an http or https URL with a host, got %q", u.Redacted())
	}
	if u.User != nil {
		return nil, errors.New("MAP_PMTILES_URL must not carry a username or password: it is sent to every driver's phone")
	}
	return &v, nil
}

// logMapPMTilesURL records the map file setting at startup, and warns when it
// is plain http, which Android release builds refuse to download over.
func logMapPMTilesURL(mapPMTilesURL *string) {
	if mapPMTilesURL == nil {
		return
	}
	slog.Info("serving the driver map file URL", "url", *mapPMTilesURL)
	// url.Parse lowercases the scheme, so this catches "HTTP://" too.
	if u, err := url.Parse(*mapPMTilesURL); err == nil && u.Scheme == "http" {
		slog.Warn("MAP_PMTILES_URL is plain http; Android blocks cleartext downloads in release builds, so use https in production")
	}
}
