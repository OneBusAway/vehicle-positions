package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// testWebhookTiming checks every few milliseconds and retries almost at once.
func testWebhookTiming() alertWebhookTiming {
	return alertWebhookTiming{
		interval:  5 * time.Millisecond,
		timeout:   2 * time.Second,
		backoff:   []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		queueSize: 100,
	}
}

// idleWebhookTiming never checks by itself: the test calls check.
func idleWebhookTiming() alertWebhookTiming {
	timing := testWebhookTiming()
	timing.interval = time.Hour
	return timing
}

// scriptedAlerts is an alertSource that returns what the test last set.
type scriptedAlerts struct {
	mu     sync.Mutex
	alerts []alertEntry
	err    error
	calls  int
}

func (s *scriptedAlerts) list(context.Context, time.Time) ([]alertEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return slices.Clone(s.alerts), nil
}

func (s *scriptedAlerts) set(err error, alerts ...alertEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts, s.err = alerts, err
}

func (s *scriptedAlerts) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// waitForChecks waits until the webhook has read the alerts n more times.
func waitForChecks(t *testing.T, s *scriptedAlerts, n int) {
	t.Helper()
	target := s.callCount() + n
	require.Eventually(t, func() bool { return s.callCount() >= target }, 5*time.Second, time.Millisecond)
}

// webhookReceiver is an httptest server that records every post. The nth post
// is answered answers[n], and any after them otherwise.
type webhookReceiver struct {
	*httptest.Server

	mu        sync.Mutex
	posts     []receivedPost
	answers   []int
	otherwise int
	// hold, while set, keeps each post waiting until it is closed or the
	// client gives up.
	hold chan struct{}
}

type receivedPost struct {
	header http.Header
	body   []byte
}

func newWebhookReceiver(t *testing.T, otherwise int, answers ...int) *webhookReceiver {
	t.Helper()
	r := &webhookReceiver{answers: answers, otherwise: otherwise}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		status := r.otherwise
		if n := len(r.posts); n < len(r.answers) {
			status = r.answers[n]
		}
		r.posts = append(r.posts, receivedPost{header: req.Header.Clone(), body: body})
		hold := r.hold
		r.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-req.Context().Done():
				return
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, http.StatusText(status))
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *webhookReceiver) holdPosts() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hold = make(chan struct{})
	return r.hold
}

func (r *webhookReceiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.posts)
}

func (r *webhookReceiver) post(i int) receivedPost {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.posts[i]
}

// waitFor waits until the receiver has had n posts.
func (r *webhookReceiver) waitFor(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return r.count() >= n }, 5*time.Second, time.Millisecond)
}

// payloads decodes every post, in the order they came.
func (r *webhookReceiver) payloads(t *testing.T) []map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.posts))
	for _, p := range r.posts {
		var m map[string]any
		require.NoError(t, json.Unmarshal(p.body, &m), string(p.body))
		out = append(out, m)
	}
	return out
}

// eventsOf is each payload's event and kind, as "raised quiet".
func eventsOf(payloads []map[string]any) []string {
	out := make([]string, 0, len(payloads))
	for _, p := range payloads {
		out = append(out, fmt.Sprintf("%v %v", p["event"], p["kind"]))
	}
	return out
}

// lockedLogs is a log buffer the webhook's goroutines can write to while the
// test reads it.
type lockedLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *lockedLogs) count(s string) int { return strings.Count(l.String(), s) }

// captureWebhookLogs points the default logger at a lockedLogs for the rest of
// the test. Not safe for t.Parallel(); uses global logger. Called before the
// webhook starts, so the webhook stops before the logger is put back.
func captureWebhookLogs(t *testing.T) *lockedLogs {
	t.Helper()
	logs := &lockedLogs{}
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return logs
}

// startWebhook starts a webhook reading source, naming routes from ix, nil
// for none, and stops it when the test ends.
func startWebhook(t *testing.T, cfg alertWebhookConfig, source alertSource, ix *rider.Index, timing alertWebhookTiming) *alertWebhook {
	t.Helper()
	h := newAlertWebhook(cfg, source, offRouteSettings, func() *rider.Index { return ix }, timing)
	t.Cleanup(h.Stop)
	return h
}

func configFor(r *webhookReceiver) alertWebhookConfig {
	return alertWebhookConfig{url: r.URL + "/hook", host: strings.TrimPrefix(r.URL, "http://")}
}

// webhookAlert is a kind alert on bus-7's trip 7 on route R1, since two
// minutes into the trip.
func webhookAlert(kind string) alertEntry {
	return newAlert(kind, alertT0.Add(2*time.Minute), alertTrip("bus-7", alertT0), alertT0.Add(6*time.Minute))
}

// alertOn is a quiet alert on vehicleID's trip id.
func alertOn(vehicleID string, id int64) alertEntry {
	trip := alertTrip(vehicleID, alertT0)
	trip.ID = id
	return newAlert(alertQuiet, alertT0, trip, alertT0.Add(5*time.Minute))
}

func TestAlertWebhook_RaisedThenCleared(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	source.set(nil, webhookAlert(alertQuiet))
	startWebhook(t, configFor(receiver), source, nil, testWebhookTiming())

	receiver.waitFor(t, 1)
	waitForChecks(t, source, 5)
	assert.Equal(t, 1, receiver.count(), "an alert that stays is posted once")

	source.set(nil)
	receiver.waitFor(t, 2)
	waitForChecks(t, source, 5)
	assert.Equal(t, 2, receiver.count(), "a cleared alert is posted once")
	assert.Equal(t, []string{"raised quiet", "cleared quiet"}, eventsOf(receiver.payloads(t)))
}

func TestAlertWebhook_NoRepeatWhileTheAlertStays(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())

	// A not-moving vehicle whose GPS creeps has its since move on while the
	// alert stays.
	for _, since := range []time.Duration{2 * time.Minute, 3 * time.Minute, time.Minute} {
		alert := webhookAlert(alertNotMoving)
		alert.Since = alertT0.Add(since)
		source.set(nil, alert)
		h.check(alertT0.Add(15 * time.Minute))
	}
	source.set(nil)
	h.check(alertT0.Add(16 * time.Minute))

	// Posts go out in order, so once the clear is in nothing is still to come.
	receiver.waitFor(t, 2)
	assert.Equal(t, []string{"raised not_moving", "cleared not_moving"}, eventsOf(receiver.payloads(t)))
	assert.Equal(t, "2026-11-14T09:02:00Z", receiver.payloads(t)[1]["since"], "the clear names the since it was raised with")
}

func TestAlertWebhook_ClearsBeforeRaises(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())

	source.set(nil, webhookAlert(alertNotMoving), alertOn("bus-8", 8))
	h.check(alertT0)
	source.set(nil, webhookAlert(alertQuiet), alertOn("bus-8", 8))
	h.check(alertT0)

	receiver.waitFor(t, 4)
	assert.Equal(t, []string{"raised not_moving", "raised quiet", "cleared not_moving", "raised quiet"}, eventsOf(receiver.payloads(t)))
	assert.Equal(t, []any{"bus-7", "bus-8", "bus-7", "bus-7"}, valuesOf(receiver.payloads(t), "vehicle_id"))
}

func TestAlertWebhook_ClearsLongestRunningFirst(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())

	alerts := make([]alertEntry, 0, 6)
	want := make([]any, 0, 6)
	for i := int64(1); i <= 6; i++ {
		alert := alertOn("bus-"+strconv.FormatInt(i, 10), i)
		alert.Since = alertT0.Add(time.Duration(i) * time.Minute)
		alerts = append(alerts, alert)
		want = append(want, alert.Trip.VehicleID)
	}
	source.set(nil, alerts...)
	h.check(alertT0)
	source.set(nil)
	h.check(alertT0)

	receiver.waitFor(t, 12)
	assert.Equal(t, want, valuesOf(receiver.payloads(t)[6:], "vehicle_id"))
}

func valuesOf(payloads []map[string]any, key string) []any {
	out := make([]any, 0, len(payloads))
	for _, p := range payloads {
		out = append(out, p[key])
	}
	return out
}

func TestAlertWebhook_FailedCheckChangesNothing(t *testing.T) {
	logs := captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	source.set(errors.New("database down"))
	h.check(alertT0)
	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	source.set(nil)
	h.check(alertT0)

	receiver.waitFor(t, 2)
	assert.Equal(t, []string{"raised quiet", "cleared quiet"}, eventsOf(receiver.payloads(t)))
	assert.Contains(t, logs.String(), "alert webhook: list alerts")
	assert.Contains(t, logs.String(), "database down")
}

func TestAlertWebhook_Payload(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	cfg := configFor(receiver)
	cfg.publicURL = "https://tracker.example.org"
	h := startWebhook(t, cfg, source, plainFixtureIndex(t), idleWebhookTiming())

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	receiver.waitFor(t, 1)

	post := receiver.post(0)
	assert.Equal(t, "application/json", post.header.Get("Content-Type"))
	assert.Empty(t, post.header.Get("X-Alert-Signature"), "unsigned without a secret")
	assert.Empty(t, post.header.Get("X-Alert-Timestamp"))
	// The whole body, so a field added later has to be added here on purpose.
	assert.Equal(t, map[string]any{
		"event":            "raised",
		"kind":             "quiet",
		"vehicle_id":       "bus-7",
		"vehicle_label":    "Bus 7",
		"route_id":         "R1",
		"route_short_name": "1",
		"gtfs_trip_id":     "T1",
		"trip_id":          float64(7),
		"since":            "2026-11-14T09:02:00Z",
		"link":             "https://tracker.example.org/admin/trips/7",
		// 09:02 UTC is 01:02 in the feed's Los Angeles.
		"text": "Bus 7 on route 1 has gone quiet: no report since 01:02. https://tracker.example.org/admin/trips/7",
	}, receiver.payloads(t)[0])
}

func TestAlertWebhook_NoDriverOrPosition(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, plainFixtureIndex(t), idleWebhookTiming())

	alert := webhookAlert(alertOffRoute)
	require.Equal(t, "Achieng", alert.Trip.DriverName, "the alert names a driver for the body to leave out")
	source.set(nil, alert)
	h.check(alertT0)
	receiver.waitFor(t, 1)

	body := strings.ToLower(string(receiver.post(0).body))
	for _, leaked := range []string{"achieng", "driver", "email", "user", "lat", "lon", "position"} {
		assert.NotContains(t, body, leaked)
	}
}

func TestAlertWebhook_NoLinkWithoutPublicURL(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, plainFixtureIndex(t), idleWebhookTiming())

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	receiver.waitFor(t, 1)

	payload := receiver.payloads(t)[0]
	_, hasLink := payload["link"]
	assert.False(t, hasLink)
	assert.Equal(t, "Bus 7 on route 1 has gone quiet: no report since 01:02.", payload["text"])
}

func TestAlertWebhook_Text(t *testing.T) {
	notStarted := webhookAlert(alertNotStarted)
	notStarted.NextTrip = &alertNextTrip{GtfsTripID: "T5", RouteID: "R2", BlockID: "B1"}
	unlabelled := webhookAlert(alertQuiet)
	unlabelled.Trip.VehicleLabel = ""
	routeOnly := webhookAlert(alertQuiet)
	routeOnly.Trip.RouteID = ""
	unknownRoute := webhookAlert(alertQuiet)
	unknownRoute.Trip.RouteID = "R9"
	bunched := webhookAlert(alertBunched)
	bunched.Ahead = &alertAhead{VehicleID: "bus-12", VehicleLabel: "Bus 12", SpacingS: 75, ScheduledGapS: 600}
	bunchedBehindUnlabelled := webhookAlert(alertBunched)
	bunchedBehindUnlabelled.Ahead = &alertAhead{VehicleID: "bus-12", SpacingS: 45, ScheduledGapS: 300}

	tests := []struct {
		name  string
		event string
		alert alertEntry
		noIx  bool
		text  string
	}{
		{"quiet raised", alertRaised, webhookAlert(alertQuiet), false, "Bus 7 on route 1 has gone quiet: no report since 01:02."},
		{"quiet cleared", alertCleared, webhookAlert(alertQuiet), false, "Cleared: Bus 7 on route 1, gone quiet since 01:02."},
		{"not moving raised", alertRaised, webhookAlert(alertNotMoving), false, "Bus 7 on route 1 is not moving: under 50 m since 01:02."},
		{"not moving cleared", alertCleared, webhookAlert(alertNotMoving), false, "Cleared: Bus 7 on route 1, not moving since 01:02."},
		{"off route raised", alertRaised, webhookAlert(alertOffRoute), false, "Bus 7 on route 1 is off route: over 60 m from it since 01:02."},
		{"off route cleared", alertCleared, webhookAlert(alertOffRoute), false, "Cleared: Bus 7 on route 1, off route since 01:02."},
		{"not started raised", alertRaised, notStarted, false, "Bus 7 has not started its next trip: route 2, block B1, due at 01:02."},
		{"not started cleared", alertCleared, notStarted, false, "Cleared: Bus 7, next trip not started: route 2, block B1, due at 01:02."},
		{"bunched raised", alertRaised, bunched, false, "Bus 7 on route 1 is bunched: 1 min behind Bus 12, every 10 min, since 01:02."},
		{"bunched cleared", alertCleared, bunched, false, "Cleared: Bus 7 on route 1, bunched since 01:02."},
		{"bunched behind a vehicle with no label", alertRaised, bunchedBehindUnlabelled, false, "Bus 7 on route 1 is bunched: 45s behind bus-12, every 5 min, since 01:02."},
		{"no label", alertRaised, unlabelled, false, "bus-7 on route 1 has gone quiet: no report since 01:02."},
		{"no route", alertRaised, routeOnly, false, "Bus 7 has gone quiet: no report since 01:02."},
		{"route not in the schedule", alertRaised, unknownRoute, false, "Bus 7 on route R9 has gone quiet: no report since 01:02."},
		{"no schedule", alertRaised, webhookAlert(alertQuiet), true, "Bus 7 on route R1 has gone quiet: no report since 09:02."},
	}
	ix := plainFixtureIndex(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			index := ix
			if tc.noIx {
				index = nil
			}
			h := &alertWebhook{settings: offRouteSettings, schedule: func() *rider.Index { return index }}
			assert.Equal(t, tc.text, h.event(tc.event, tc.alert).payload.Text)
		})
	}
}

func TestAlertWebhook_NotStartedNamesTheTripNotStarted(t *testing.T) {
	alert := webhookAlert(alertNotStarted)
	alert.NextTrip = &alertNextTrip{GtfsTripID: "T5", RouteID: "R2", BlockID: "B1"}
	ix := plainFixtureIndex(t)
	h := &alertWebhook{settings: offRouteSettings, schedule: func() *rider.Index { return ix }, cfg: alertWebhookConfig{publicURL: "https://tracker.example.org"}}

	p := h.event(alertRaised, alert).payload
	assert.Equal(t, "R2", p.RouteID)
	assert.Equal(t, "2", p.RouteShortName)
	assert.Equal(t, "T5", p.GtfsTripID)
	assert.Equal(t, int64(7), p.TripID, "the trip whose page opens is the vehicle's last")
	assert.Equal(t, "https://tracker.example.org/admin/trips/7", p.Link)
}

func TestAlertWebhook_GoogleChatGetsTextOnly(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	cfg := configFor(receiver)
	cfg.textOnly = true
	h := startWebhook(t, cfg, source, plainFixtureIndex(t), idleWebhookTiming())

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	receiver.waitFor(t, 1)

	assert.Equal(t, map[string]any{"text": "Bus 7 on route 1 has gone quiet: no report since 01:02."}, receiver.payloads(t)[0])
}

func TestAlertWebhook_Signature(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	cfg := configFor(receiver)
	cfg.secret = []byte("webhook-signing-secret")
	h := startWebhook(t, cfg, source, nil, idleWebhookTiming())

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	receiver.waitFor(t, 1)

	post := receiver.post(0)
	timestamp := post.header.Get("X-Alert-Timestamp")
	sent, err := strconv.ParseInt(timestamp, 10, 64)
	require.NoError(t, err, "the timestamp is Unix seconds")
	assert.InDelta(t, time.Now().Unix(), sent, 10)

	// Verified the way configuration.md tells a receiver to.
	mac := hmac.New(sha256.New, []byte("webhook-signing-secret"))
	mac.Write([]byte(timestamp + "." + string(post.body)))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	got := post.header.Get("X-Alert-Signature")
	assert.True(t, hmac.Equal([]byte(want), []byte(got)), "signature %q, want %q", got, want)

	other := hmac.New(sha256.New, []byte("another-secret"))
	other.Write([]byte(timestamp + "." + string(post.body)))
	assert.NotEqual(t, "sha256="+hex.EncodeToString(other.Sum(nil)), got)
}

// TestAlertWebhook_Retries posts one alert and then its clear, which the
// receiver always takes. The clear is sent only once the alert's post has
// settled, so its arrival says every retry has been made.
func TestAlertWebhook_Retries(t *testing.T) {
	tests := []struct {
		name     string
		answers  []int
		attempts int
		logged   string
	}{
		{"5xx then success", []int{503, 502}, 3, ""},
		{"5xx every time", []int{500, 503, 502, 504}, 4, "answered 504"},
		{"429 is retried", []int{429}, 2, ""},
		{"400 is not", []int{400}, 1, "answered 400"},
		{"404 is not", []int{404}, 1, "answered 404"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureWebhookLogs(t)
			receiver := newWebhookReceiver(t, http.StatusOK, tc.answers...)
			source := &scriptedAlerts{}
			h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())

			source.set(nil, webhookAlert(alertQuiet))
			h.check(alertT0)
			source.set(nil)
			h.check(alertT0)

			receiver.waitFor(t, tc.attempts+1)
			events := eventsOf(receiver.payloads(t))
			require.Len(t, events, tc.attempts+1)
			for _, e := range events[:tc.attempts] {
				assert.Equal(t, "raised quiet", e)
			}
			assert.Equal(t, "cleared quiet", events[tc.attempts])
			if tc.logged == "" {
				assert.NotContains(t, logs.String(), "post failed")
				return
			}
			assert.Equal(t, 1, logs.count("alert webhook: post failed"))
			assert.Contains(t, logs.String(), tc.logged)
		})
	}
}

func TestAlertWebhook_Timeout(t *testing.T) {
	logs := captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	receiver.holdPosts()
	source := &scriptedAlerts{}
	timing := idleWebhookTiming()
	timing.timeout = 50 * time.Millisecond
	h := startWebhook(t, configFor(receiver), source, nil, timing)

	start := time.Now()
	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)

	require.Eventually(t, func() bool { return logs.count("post failed") == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 1+len(timing.backoff), receiver.count(), "a timeout is retried")
	assert.Contains(t, logs.String(), `error="no answer within 50ms"`)
	assert.Less(t, time.Since(start), 2*time.Second, "each attempt gave up at the timeout")
}

func TestAlertWebhook_QueueFullDropsTheOldest(t *testing.T) {
	logs := captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	release := receiver.holdPosts()
	timing := idleWebhookTiming()
	timing.queueSize = 2
	h := startWebhook(t, configFor(receiver), &scriptedAlerts{}, nil, timing)

	h.enqueue(h.event(alertRaised, alertOn("bus-1", 1)))
	// bus-1's post is with the receiver, so the queue is empty again.
	receiver.waitFor(t, 1)
	for i := int64(2); i <= 5; i++ {
		h.enqueue(h.event(alertRaised, alertOn("bus-"+strconv.FormatInt(i, 10), i)))
	}
	assert.Equal(t, 2, logs.count("queue full"))
	assert.Contains(t, logs.String(), "vehicle_id=bus-2")
	assert.Contains(t, logs.String(), "vehicle_id=bus-3")

	close(release)
	receiver.waitFor(t, 3)
	assert.Equal(t, []any{"bus-1", "bus-4", "bus-5"}, valuesOf(receiver.payloads(t), "vehicle_id"))
}

func TestAlertWebhook_RedirectIsNotFollowed(t *testing.T) {
	logs := captureWebhookLogs(t)
	var followed, posted atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Add(1)
	}))
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted.Add(1)
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	source := &scriptedAlerts{}
	h := startWebhook(t, alertWebhookConfig{url: redirecting.URL}, source, nil, idleWebhookTiming())

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)

	require.Eventually(t, func() bool { return logs.count("post failed") == 1 }, 5*time.Second, time.Millisecond)
	assert.Contains(t, logs.String(), "a redirect, which is not followed")
	assert.Equal(t, int32(1), posted.Load(), "a redirect is not retried")
	assert.Zero(t, followed.Load())
}

// failuresFor counts the logged post failures for vehicleID.
func failuresFor(logs *lockedLogs, vehicleID string) int {
	n := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "alert webhook: post failed") && strings.Contains(line, "vehicle_id="+vehicleID+" ") {
			n++
		}
	}
	return n
}

// TestAlertWebhook_LogsOncePerAlert fails every post: bus-7's alert and its
// clear log one line between them, and the same alert raised again is a new
// alert that logs one more.
func TestAlertWebhook_LogsOncePerAlert(t *testing.T) {
	logs := captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusInternalServerError)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())

	source.set(nil, alertOn("bus-7", 7))
	h.check(alertT0)
	source.set(nil)
	h.check(alertT0)
	source.set(nil, alertOn("bus-7", 7))
	h.check(alertT0)
	source.set(nil, alertOn("bus-7", 7), alertOn("bus-8", 8))
	h.check(alertT0)

	// bus-8's post is the last, so once it has failed every other has.
	require.Eventually(t, func() bool { return failuresFor(logs, "bus-8") == 1 }, 5*time.Second, time.Millisecond)
	attempts := 1 + len(idleWebhookTiming().backoff)
	events := eventsOf(receiver.payloads(t))
	require.Len(t, events, 4*attempts)
	assert.Equal(t, "cleared quiet", events[attempts])
	assert.Equal(t, 2, failuresFor(logs, "bus-7"))
}

// TestAlertWebhook_DroppedClearEndsTheAlert drops bus-7's clear from a full
// queue: the alert is over, so when it is raised again its failure is logged
// again.
func TestAlertWebhook_DroppedClearEndsTheAlert(t *testing.T) {
	logs := captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusInternalServerError)
	timing := idleWebhookTiming()
	timing.queueSize = 1
	h := startWebhook(t, configFor(receiver), &scriptedAlerts{}, nil, timing)
	attempts := 1 + len(timing.backoff)

	h.enqueue(h.event(alertRaised, alertOn("bus-7", 7)))
	require.Eventually(t, func() bool { return failuresFor(logs, "bus-7") == 1 }, 5*time.Second, time.Millisecond)

	release := receiver.holdPosts()
	h.enqueue(h.event(alertRaised, alertOn("bus-8", 8)))
	receiver.waitFor(t, attempts+1)
	h.enqueue(h.event(alertCleared, alertOn("bus-7", 7)))
	h.enqueue(h.event(alertRaised, alertOn("bus-9", 9)))
	require.Equal(t, 1, logs.count("queue full"))
	close(release)

	h.enqueue(h.event(alertRaised, alertOn("bus-7", 7)))
	require.Eventually(t, func() bool { return failuresFor(logs, "bus-7") == 2 }, 5*time.Second, time.Millisecond)
}

// newEchoServer answers every connection with the first line of its request
// and hangs up: a reply that is not HTTP, and that quotes the URL's path.
func newEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				_, _ = io.WriteString(conn, line+"\r\n")
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

// TestAlertWebhook_LogsNeitherTheURLNorTheSecret fails a post each way it can
// fail, with receivers that quote the path back where they can, and checks
// the log says why with nothing of the path or the secret.
func TestAlertWebhook_LogsNeitherTheURLNorTheSecret(t *testing.T) {
	const token = "XyZ0webhookToken"
	const secret = "s3cret-signing-key"
	// A receiver that does not know the webhook, saying so with its path.
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no webhook at "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(notFound.Close)
	failing := newWebhookReceiver(t, http.StatusInternalServerError)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(untrusted.Close)

	tests := []struct {
		name   string
		base   string
		logged string
	}{
		{"404 quoting the path", notFound.URL, `error="answered 404"`},
		{"5xx", failing.URL, `error="answered 500"`},
		{"reply not HTTP, quoting the request", newEchoServer(t), `error="no valid HTTP answer"`},
		{"unreachable", closed.URL, "connection refused"},
		{"untrusted certificate", untrusted.URL, "failed to verify certificate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureWebhookLogs(t)
			base, err := url.Parse(tc.base)
			require.NoError(t, err)
			source := &scriptedAlerts{}
			cfg := alertWebhookConfig{
				url:    tc.base + "/services/T000/B000/" + token,
				host:   base.Host,
				secret: []byte(secret),
			}
			h := startWebhook(t, cfg, source, nil, idleWebhookTiming())

			source.set(nil, webhookAlert(alertQuiet))
			h.check(alertT0)

			require.Eventually(t, func() bool { return logs.count("post failed") == 1 }, 5*time.Second, time.Millisecond)
			assert.Contains(t, logs.String(), "host="+cfg.host)
			assert.Contains(t, logs.String(), tc.logged)
			assert.NotContains(t, logs.String(), token)
			assert.NotContains(t, logs.String(), "/services/")
			assert.NotContains(t, logs.String(), secret)
		})
	}
}

func TestAlertWebhook_StopIsIdempotent(t *testing.T) {
	logs := captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	receiver.holdPosts()
	source := &scriptedAlerts{}
	source.set(nil, webhookAlert(alertQuiet))
	h := startWebhook(t, configFor(receiver), source, nil, testWebhookTiming())

	// A post in flight and checks still ticking.
	receiver.waitFor(t, 1)
	waitForChecks(t, source, 2)

	stopReturns(t, h)
	stopReturns(t, h)
	checks := source.callCount()
	assert.NotContains(t, logs.String(), "post failed", "a post cut short by Stop did not fail")
	assert.Equal(t, checks, source.callCount())
	assert.Equal(t, 1, receiver.count())
}

func TestAlertWebhook_StopDuringBackoff(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusServiceUnavailable)
	source := &scriptedAlerts{}
	timing := idleWebhookTiming()
	timing.backoff = []time.Duration{time.Hour}
	h := startWebhook(t, configFor(receiver), source, nil, timing)

	source.set(nil, webhookAlert(alertQuiet))
	h.check(alertT0)
	receiver.waitFor(t, 1)

	stopReturns(t, h)
	assert.Equal(t, 1, receiver.count())
}

// stopReturns calls h.Stop and fails the test unless it returns: Stop waits for
// both of the webhook's goroutines, so its return says neither is left.
func stopReturns(t *testing.T, h *alertWebhook) {
	t.Helper()
	stopped := make(chan struct{})
	go func() {
		h.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

func TestAlertWebhookConfigFromEnv(t *testing.T) {
	const slack = "https://hooks.slack.com/services/T000/B000/XyZ0webhookToken"
	tests := []struct {
		name      string
		url       string
		secret    string
		publicURL string
		want      *alertWebhookConfig
		err       string
	}{
		{name: "unset is off", want: nil},
		{name: "unset is off, secret and all", secret: "s", publicURL: "https://tracker.example.org", want: nil},
		{name: "slack", url: slack, want: &alertWebhookConfig{url: slack, host: "hooks.slack.com"}},
		{name: "spaces trimmed", url: "  " + slack + " ", want: &alertWebhookConfig{url: slack, host: "hooks.slack.com"}},
		{name: "plain http with a port", url: "http://10.0.0.5:9000/hook", want: &alertWebhookConfig{url: "http://10.0.0.5:9000/hook", host: "10.0.0.5:9000"}},
		{name: "signed", url: slack, secret: "webhook-signing-secret",
			want: &alertWebhookConfig{url: slack, host: "hooks.slack.com", secret: []byte("webhook-signing-secret")}},
		{name: "google chat is sent text only", url: "https://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t",
			want: &alertWebhookConfig{url: "https://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t", host: "chat.googleapis.com", textOnly: true}},
		{name: "public URL", url: slack, publicURL: "https://tracker.example.org/",
			want: &alertWebhookConfig{url: slack, host: "hooks.slack.com", publicURL: "https://tracker.example.org"}},
		{name: "public URL with a path", url: slack, publicURL: " https://example.org/fleet/ ",
			want: &alertWebhookConfig{url: slack, host: "hooks.slack.com", publicURL: "https://example.org/fleet"}},

		{name: "not http", url: "ftp://hooks.example.org/XyZ0webhookToken", err: "ALERT_WEBHOOK_URL must start with http:// or https://"},
		{name: "no scheme", url: "hooks.slack.com/services/T000/B000/XyZ0webhookToken", err: "ALERT_WEBHOOK_URL must start with http:// or https://"},
		{name: "no host", url: "https:///services/T000/B000/XyZ0webhookToken", err: "ALERT_WEBHOOK_URL has no host"},
		{name: "unparseable", url: "https://[::1/XyZ0webhookToken", err: "ALERT_WEBHOOK_URL is not a valid URL"},
		{name: "public URL not http", url: slack, publicURL: "tracker.example.org/XyZ0webhookToken", err: "PUBLIC_URL must start with http:// or https://"},
		{name: "public URL with no host", url: slack, publicURL: "https:///XyZ0webhookToken", err: "PUBLIC_URL has no host"},
		{name: "public URL with a password", url: slack, publicURL: "https://admin:XyZ0webhookToken@tracker.example.org", err: "PUBLIC_URL must not carry a username or password: it is sent in every alert"},
		{name: "public URL with a query", url: slack, publicURL: "https://tracker.example.org/?token=XyZ0webhookToken", err: "PUBLIC_URL must not have a query"},
		{name: "public URL with a fragment", url: slack, publicURL: "https://tracker.example.org/#XyZ0webhookToken", err: "PUBLIC_URL must not have a fragment"},
		{name: "public URL unparseable", url: slack, publicURL: "https://[::1/XyZ0webhookToken", err: "PUBLIC_URL is not a valid URL"},
		{name: "bad public URL with the webhook off", publicURL: "ftp://tracker.example.org/XyZ0webhookToken", err: "PUBLIC_URL must start with http:// or https://"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALERT_WEBHOOK_URL", tc.url)
			t.Setenv("ALERT_WEBHOOK_SECRET", tc.secret)
			t.Setenv("PUBLIC_URL", tc.publicURL)

			got, err := alertWebhookConfigFromEnv()
			if tc.err != "" {
				// The error goes to the startup log: it names what is wrong,
				// never the value.
				assert.EqualError(t, err, tc.err)
				assert.NotContains(t, err.Error(), "XyZ0webhookToken")
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDefaultAlertWebhookTiming pins the timing configuration.md states, and
// keeps the check in step with the Alerts page's reload.
func TestDefaultAlertWebhookTiming(t *testing.T) {
	timing := defaultAlertWebhookTiming()
	assert.Equal(t, 30*time.Second, timing.interval)
	assert.Equal(t, alertsPageRefresh, strconv.Itoa(int(timing.interval.Seconds())))
	assert.Equal(t, 5*time.Second, timing.timeout)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, timing.backoff)
	assert.Equal(t, 100, timing.queueSize)
}

// lastPost waits for h to settle a post and returns it.
func lastPost(t *testing.T, h *alertWebhook) alertWebhookDelivery {
	t.Helper()
	require.Eventually(t, func() bool { return h.status().Last != nil }, 5*time.Second, time.Millisecond)
	return *h.status().Last
}

func TestAlertWebhook_StatusBeforeAnyPost(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	before := time.Now()
	h := startWebhook(t, configFor(receiver), &scriptedAlerts{}, nil, idleWebhookTiming())

	s := h.status()
	assert.Equal(t, configFor(receiver).host, s.Host)
	assert.Nil(t, s.Last)
	assert.Zero(t, s.Waiting)
	assert.False(t, s.Sending)
	assert.WithinDuration(t, before, s.Started, time.Second)
}

func TestAlertWebhook_StatusAfterASend(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())
	source.set(nil, webhookAlert(alertQuiet))
	before := time.Now()
	h.check(alertT0)

	last := lastPost(t, h)
	assert.True(t, last.Sent)
	assert.Zero(t, last.Status)
	assert.Empty(t, last.Problem)
	assert.Equal(t, []string{alertRaised, alertQuiet, "bus-7"}, []string{last.Event, last.Kind, last.VehicleID})
	assert.WithinDuration(t, before, last.At, time.Second)
}

// A post that fails says why in plain words, and the status the receiver
// answered: a 4xx at once, a 5xx after its retries.
func TestAlertWebhook_StatusAfterAFailure(t *testing.T) {
	for _, tc := range []struct {
		status  int
		problem string
	}{
		{http.StatusNotFound, "Answered 404 Not Found"},
		{http.StatusInternalServerError, "Answered 500 Internal Server Error"},
		{http.StatusMovedPermanently, "Answered 301, a redirect, which is not followed. Set ALERT_WEBHOOK_URL to where it leads."},
	} {
		t.Run(tc.problem, func(t *testing.T) {
			captureWebhookLogs(t)
			receiver := newWebhookReceiver(t, tc.status)
			source := &scriptedAlerts{}
			h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())
			source.set(nil, webhookAlert(alertQuiet))
			h.check(alertT0)

			last := lastPost(t, h)
			assert.False(t, last.Sent)
			assert.Equal(t, tc.status, last.Status)
			assert.Equal(t, tc.problem, last.Problem)
		})
	}
}

func TestAlertWebhook_StatusAfterNoAnswer(t *testing.T) {
	captureWebhookLogs(t)
	t.Run("nothing listening", func(t *testing.T) {
		gone := httptest.NewServer(http.NotFoundHandler())
		cfg := alertWebhookConfig{url: gone.URL + "/hook", host: strings.TrimPrefix(gone.URL, "http://")}
		gone.Close()
		source := &scriptedAlerts{}
		h := startWebhook(t, cfg, source, nil, idleWebhookTiming())
		source.set(nil, webhookAlert(alertQuiet))
		h.check(alertT0)

		last := lastPost(t, h)
		assert.False(t, last.Sent)
		assert.Zero(t, last.Status, "no answer, so no status")
		assert.Equal(t, "Could not connect to "+cfg.host, last.Problem)
	})
	t.Run("too slow", func(t *testing.T) {
		receiver := newWebhookReceiver(t, http.StatusOK)
		receiver.holdPosts()
		timing := idleWebhookTiming()
		timing.timeout = 20 * time.Millisecond
		source := &scriptedAlerts{}
		h := startWebhook(t, configFor(receiver), source, nil, timing)
		source.set(nil, webhookAlert(alertQuiet))
		h.check(alertT0)

		last := lastPost(t, h)
		assert.Zero(t, last.Status)
		assert.Equal(t, "No answer within 20ms", last.Problem)
	})
}

// The posts waiting are counted, and the one being sent is told apart.
func TestAlertWebhook_StatusCountsThePostsWaiting(t *testing.T) {
	receiver := newWebhookReceiver(t, http.StatusOK)
	release := receiver.holdPosts()
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, idleWebhookTiming())
	source.set(nil, alertOn("bus-1", 1), alertOn("bus-2", 2), alertOn("bus-3", 3))
	h.check(alertT0)

	require.Eventually(t, func() bool { s := h.status(); return s.Sending && s.Waiting == 2 }, 5*time.Second, time.Millisecond,
		"one held at the receiver, two behind it")
	close(release)
	require.Eventually(t, func() bool { s := h.status(); return !s.Sending && s.Waiting == 0 }, 5*time.Second, time.Millisecond)
	assert.True(t, h.status().Last.Sent)
}

// Read while the sender works, under -race: the page takes its snapshot
// under the webhook's own lock.
func TestAlertWebhook_StatusWhileSending(t *testing.T) {
	captureWebhookLogs(t)
	receiver := newWebhookReceiver(t, http.StatusOK, http.StatusInternalServerError, http.StatusOK, http.StatusNotFound)
	source := &scriptedAlerts{}
	h := startWebhook(t, configFor(receiver), source, nil, testWebhookTiming())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 20 {
			source.set(nil, alertOn(fmt.Sprintf("bus-%d", i%4), int64(i%4)))
			time.Sleep(time.Millisecond)
		}
	}()
	for {
		s := h.status()
		if s.Last != nil {
			_ = s.Last.Problem
		}
		select {
		case <-done:
			require.Eventually(t, func() bool { return h.status().Last != nil }, 5*time.Second, time.Millisecond)
			return
		default:
		}
	}
}
