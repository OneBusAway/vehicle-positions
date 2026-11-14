package main

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alertBadge matches the sidebar's Alerts badge and captures its count.
var alertBadge = regexp.MustCompile(`id="alert-count"[^>]*>(\d+)</span>`)

// newAlertsMux serves the admin UI with alerts as its watcher.
func newAlertsMux(t *testing.T, alerts *alertWatcher) *http.ServeMux {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.alerts = alerts
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux
}

// twoAlerts is a watcher with one alert of each kind as of now: bus-quiet last
// reported five minutes ago, and bus-still has stood in one place for twelve.
func twoAlerts(now time.Time) *alertWatcher {
	start := now.Add(-time.Hour)
	trips := &fakeTripLister{}
	w := newAlertWatcher(trips, testAlertSettings)
	w.watchingSince = start
	w.record("bus-quiet", alertStop, now.Add(-5*time.Minute))
	reportEvery(w, "bus-still", alertStop, now.Add(-12*time.Minute), now, 5*time.Second)

	quiet := alertTrip("bus-quiet", start)
	quiet.VehicleLabel = "Quiet bus"
	still := alertTrip("bus-still", start)
	still.ID = 8
	still.VehicleLabel = "Still bus"
	still.DriverName = "Baraka"
	still.RouteID = "R2"
	still.GtfsTripID = "T2"
	trips.trips = []TripSummary{quiet, still}
	return w
}

func TestAlertsPage_Empty(t *testing.T) {
	mux := newAlertsMux(t, newAlertWatcher(&fakeTripLister{}, testAlertSettings))

	w := requestAdminPage(t, mux, "/admin/alerts")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "30", w.Header().Get("Refresh"), "the page reloads itself without JavaScript")
	body := w.Body.String()
	assert.Contains(t, body, "No alerts. Every vehicle on a running trip is reporting and moving.")
	assert.Contains(t, body, "sent no report for over 3 min, or have moved under 50 m in 10 min")
	assert.NotRegexp(t, alertBadge, body, "no alerts, no badge")
}

func TestAlertsPage_ListsEachAlert(t *testing.T) {
	mux := newAlertsMux(t, twoAlerts(time.Now()))

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.NotContains(t, body, "No alerts.")

	assert.Contains(t, body, "Gone quiet")
	assert.Contains(t, body, "No report for 5 min")
	assert.Contains(t, body, "Quiet bus")
	assert.Contains(t, body, "bus-quiet")
	assert.Contains(t, body, "Achieng")
	assert.Contains(t, body, `href="/admin/map?trip_id=7"`)
	assert.Contains(t, body, `href="/admin/trips?page=1&amp;status=active&amp;vehicle_id=bus-quiet"`)

	assert.Contains(t, body, "Not moving")
	assert.Contains(t, body, "Under 50 m in 12 min")
	assert.Contains(t, body, "Still bus")
	assert.Contains(t, body, "Baraka")
	assert.Contains(t, body, "R2")
	assert.Contains(t, body, "T2")
	assert.Contains(t, body, `href="/admin/map?trip_id=8"`)
	assert.Contains(t, body, `href="/admin/trips?page=1&amp;status=active&amp;vehicle_id=bus-still"`)

	assert.Less(t, strings.Index(body, "Still bus"), strings.Index(body, "Quiet bus"), "the longest-running alert comes first")

	badge := alertBadge.FindStringSubmatch(body)
	require.NotNil(t, badge)
	assert.Equal(t, "2", badge[1])
}

func TestAlertsPage_ListError(t *testing.T) {
	logs := captureLogs(t)
	mux := newAlertsMux(t, newAlertWatcher(&fakeTripLister{err: errors.New("database down")}, testAlertSettings))

	w := requestAdminPage(t, mux, "/admin/alerts")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, logs.String(), "database down")
}

// TestSidebar_AlertBadge checks the badge on a page other than Alerts, since
// it is meant to be seen from everywhere.
func TestSidebar_AlertBadge(t *testing.T) {
	t.Run("with alerts", func(t *testing.T) {
		mux := newAlertsMux(t, twoAlerts(time.Now()))

		body := getAdminPage(t, mux, "/admin/api-keys")

		badge := alertBadge.FindStringSubmatch(body)
		require.NotNil(t, badge)
		assert.Equal(t, "2", badge[1])
	})

	t.Run("without alerts", func(t *testing.T) {
		mux := newAlertsMux(t, newAlertWatcher(&fakeTripLister{}, testAlertSettings))

		body := getAdminPage(t, mux, "/admin/api-keys")

		assert.Contains(t, body, `href="/admin/alerts"`, "the Alerts item is there with nothing to count")
		assert.NotRegexp(t, alertBadge, body)
	})

	t.Run("the page still renders when alerts cannot be worked out", func(t *testing.T) {
		logs := captureLogs(t)
		mux := newAlertsMux(t, newAlertWatcher(&fakeTripLister{err: errors.New("database down")}, testAlertSettings))

		body := getAdminPage(t, mux, "/admin/api-keys")

		assert.Contains(t, body, "Feed API keys")
		assert.NotRegexp(t, alertBadge, body)
		assert.Contains(t, logs.String(), "sidebar: list alerts")
	})
}
