package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
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
	assert.Contains(t, body, `<h2 class="h1">No alerts are open</h2>`)
	assert.Contains(t, body, `<div class="empty-title">All clear</div>`)
	assert.Contains(t, body, "No vehicle on a running trip has gone quiet, left its route or stopped moving, and no block's next trip is late to start.")
	assert.NotContains(t, body, "Longest open first.")
	assert.Contains(t, body, "sent no report for over 3 min, or have moved under 50 m in 10 min")
	assert.NotRegexp(t, alertBadge, body, "no alerts, no badge")
}

func TestAlertsPage_ListsEachAlert(t *testing.T) {
	mux := newAlertsMux(t, twoAlerts(time.Now()))

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.NotContains(t, body, "All clear")
	assert.Contains(t, body, `<h2 class="h1">2 alerts are open</h2>`)
	assert.Contains(t, body, "Longest open first.")

	assert.Contains(t, body, "Gone quiet")
	assert.Contains(t, body, "No report for 5 min")
	assert.Contains(t, body, "Quiet bus")
	assert.Contains(t, body, "bus-quiet")
	assert.Contains(t, body, "Achieng")
	assert.Contains(t, body, `href="/admin/trips/7"`)
	assert.Contains(t, body, `href="/admin/trips?page=1&amp;status=active&amp;vehicle_id=bus-quiet"`)

	assert.Contains(t, body, "Not moving")
	assert.Contains(t, body, "Under 50 m in 12 min")
	assert.Contains(t, body, "Still bus")
	assert.Contains(t, body, "Baraka")
	assert.Contains(t, body, "R2")
	assert.Contains(t, body, "T2")
	assert.Contains(t, body, `href="/admin/trips/8"`)
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
	assert.Equal(t, alertsPageRefresh, w.Header().Get("Refresh"), "the error page keeps reloading")
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

		assert.Contains(t, body, `<h1 class="topbar-title">API keys</h1>`)
		assert.NotRegexp(t, alertBadge, body)
		assert.Contains(t, logs.String(), "sidebar: list alerts")
	})
}

// alertsWithWebhook is the Alerts page served with quietWatcher's alerts,
// posted by startAlertJobs to cfg as the server does.
func alertsWithWebhook(t *testing.T, cfg *alertWebhookConfig) *http.ServeMux {
	t.Helper()
	watcher := quietWatcher()
	stop := startAlertJobs(&fakeAlertHistory{}, watcher, func() *rider.Index { return nil }, cfg, testWebhookTiming())
	t.Cleanup(stop)
	return newAlertsMux(t, watcher)
}

// alertsPageOnce waits until the Alerts page shows want and returns it.
func alertsPageOnce(t *testing.T, mux *http.ServeMux, want string) string {
	t.Helper()
	var body string
	require.Eventually(t, func() bool {
		body = requestAdminPage(t, mux, "/admin/alerts").Body.String()
		return strings.Contains(body, want)
	}, 5*time.Second, 5*time.Millisecond, "the page never showed %q", want)
	return body
}

func TestAlertsPage_WebhookNotSet(t *testing.T) {
	body := requestAdminPage(t, alertsWithWebhook(t, nil), "/admin/alerts").Body.String()
	assert.Contains(t, body, `<h3 id="webhook-title" style="flex:1">Webhook</h3>`)
	assert.Contains(t, body, "Alerts are only on this page. To post them to a chat or another service as well, set ALERT_WEBHOOK_URL.")
	assert.NotContains(t, body, "Posting to")
}

func TestAlertsPage_WebhookNothingSentYet(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	cfg := configFor(receiver)
	// Nothing to post: no trip is running.
	watcher := newAlertWatcher(&lockedTripLister{}, offRouteSettings)
	stop := startAlertJobs(&fakeAlertHistory{}, watcher, func() *rider.Index { return nil }, &cfg, testWebhookTiming())
	t.Cleanup(stop)

	body := requestAdminPage(t, newAlertsMux(t, watcher), "/admin/alerts").Body.String()
	assert.Contains(t, body, `<span class="sub">Posting to `+cfg.host+`</span>`)
	assert.Contains(t, body, `<tr><td>Last post</td><td><span class="muted">None since the server started</span></td></tr>`)
	assert.Contains(t, body, `<tr><td>Waiting to post</td><td>None</td></tr>`)
	assert.Regexp(t, `Kept in memory since the server started at \d\d:\d\d, \d+ [A-Z][a-z]{2}\. A restart starts it again\.`, body)
}

func TestAlertsPage_WebhookSent(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	cfg := configFor(receiver)
	body := alertsPageOnce(t, alertsWithWebhook(t, &cfg), `<span class="pill pill-ok">Sent</span>`)
	assert.Regexp(t, `<span class="pill pill-ok">Sent</span> \d\d:\d\d, \d+ [A-Z][a-z]{2}, Gone quiet for bus-7, raised</td>`, body)
	assert.NotContains(t, body, "pill-warn\">Failed")
}

func TestAlertsPage_WebhookFailing(t *testing.T) {
	captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusInternalServerError)
	cfg := configFor(receiver)
	body := alertsPageOnce(t, alertsWithWebhook(t, &cfg), `<span class="pill pill-warn">Failed</span>`)
	assert.Contains(t, body, `Gone quiet for bus-7, raised<span class="sub">Answered 500 Internal Server Error</span>`)
}

func TestAlertsPage_WebhookCannotConnect(t *testing.T) {
	captureWebhookLogs(t)
	gone := httptest.NewServer(http.NotFoundHandler())
	cfg := alertWebhookConfig{url: gone.URL + "/hook", host: strings.TrimPrefix(gone.URL, "http://")}
	gone.Close()
	body := alertsPageOnce(t, alertsWithWebhook(t, &cfg), `<span class="pill pill-warn">Failed</span>`)
	assert.Contains(t, body, `<span class="sub">Could not connect to `+cfg.host+`</span>`)
}

// A chat service's webhook URL carries its credential in the path or the
// query. The page names the host and nothing more, whatever happens.
func TestAlertsPage_WebhookURLStaysHidden(t *testing.T) {
	captureWebhookLogs(t)
	for _, answer := range []int{http.StatusOK, http.StatusForbidden, http.StatusFound} {
		receiver := newWebhookReceiver(t, answer)
		host := strings.TrimPrefix(receiver.URL, "http://")
		cfg := alertWebhookConfig{url: receiver.URL + "/services/T0SECRET/B0SECRET/XSECRETTOKEN?token=QSECRET&key=KSECRET", host: host}
		body := alertsPageOnce(t, alertsWithWebhook(t, &cfg), "Gone quiet for bus-7, raised")
		assert.Contains(t, body, "Posting to "+host)
		for _, secret := range []string{"SECRET", "/services", "token=", "key=", "/B0", "?" + "token"} {
			assert.NotContains(t, body, secret, "answer %d", answer)
		}
	}
}

func TestNewWebhookView(t *testing.T) {
	assert.Equal(t, webhookView{}, newWebhookView(nil, time.UTC, alertT0), "no webhook, no host")
	waiting := func(sending bool, n int) string {
		return newWebhookView(&alertWebhookStatus{Host: "h", Sending: sending, Waiting: n}, time.UTC, alertT0).Waiting
	}
	assert.Equal(t, "None", waiting(false, 0))
	assert.Equal(t, "1 post waiting", waiting(false, 1))
	assert.Equal(t, "3 posts waiting", waiting(false, 3))
	assert.Equal(t, "1 being sent", waiting(true, 0))
	assert.Equal(t, "1 being sent, 2 waiting", waiting(true, 2))
}
