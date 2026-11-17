package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// The two events the webhook sends for each alert.
const (
	alertRaised  = "raised"
	alertCleared = "cleared"
)

// The headers that carry a signed post's signature, as configuration.md
// describes them to whoever verifies it.
const (
	alertWebhookTimestampHeader = "X-Alert-Timestamp"
	alertWebhookSignatureHeader = "X-Alert-Signature"
)

// googleChatHost is Google Chat's webhook host. Google Chat refuses a message
// with any field it does not know, so it is sent only the text.
const googleChatHost = "chat.googleapis.com"

// alertWebhookTiming is how often the webhook works the alerts out and how it
// sends them.
type alertWebhookTiming struct {
	// interval is the time between two checks.
	interval time.Duration
	// timeout bounds one attempt at a post.
	timeout time.Duration
	// backoff is the wait before each retry; there are as many retries as
	// waits.
	backoff []time.Duration
	// queueSize is how many posts may wait to be sent.
	queueSize int
}

// defaultAlertWebhookTiming is the timing main uses.
//
// A check every 30 seconds is the Alerts page's reload, so a phone hears of an
// alert no later than a dispatcher watching the page sees it. A check is the
// read a load of the page makes, so it costs the database what one open Alerts
// page already does, and 30 seconds is a sixth of the shortest default
// threshold, ALERT_QUIET_AFTER.
//
// A chat service answers in well under a second, so the 5-second timeout only
// has to ride out a slow mobile link. The retries after 1, 2 and 4 seconds
// outlast a dropped connection or a receiver restarting, and the worst case for
// one post, four timeouts and the three waits, is 27 seconds, under one check.
// A pilot fleet does not raise or clear 100 alerts between two checks, so the
// queue fills only when the receiver has been down a while, and then the
// newest posts are the ones worth keeping.
func defaultAlertWebhookTiming() alertWebhookTiming {
	return alertWebhookTiming{
		interval:  30 * time.Second,
		timeout:   5 * time.Second,
		backoff:   []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		queueSize: 100,
	}
}

// alertWebhookConfig is where the alert webhook posts and what it puts in.
type alertWebhookConfig struct {
	// url is ALERT_WEBHOOK_URL. A chat service's webhook URL carries its
	// credential in the path, so the URL is never logged.
	url string
	// host is the URL's host, all of it the logs name.
	host string
	// secret is ALERT_WEBHOOK_SECRET, nil when posts go unsigned.
	secret []byte
	// textOnly is set for Google Chat, which gets only the text.
	textOnly bool
	// publicURL is PUBLIC_URL with no trailing slash, "" when posts carry no
	// link.
	publicURL string
}

// alertWebhookConfigFromEnv reads ALERT_WEBHOOK_URL, ALERT_WEBHOOK_SECRET and
// PUBLIC_URL. It returns nil when ALERT_WEBHOOK_URL is unset, which leaves the
// webhook off. A URL that is not http or https is an error, and so is a bad
// PUBLIC_URL whether or not the webhook is on, so a typo fails at startup
// rather than when the first alert goes out.
func alertWebhookConfigFromEnv() (*alertWebhookConfig, error) {
	publicURL, err := parsePublicURL(os.Getenv("PUBLIC_URL"))
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(os.Getenv("ALERT_WEBHOOK_URL"))
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Not wrapped: url.Parse's error repeats the URL, credential and all.
		return nil, errors.New("ALERT_WEBHOOK_URL is not a valid URL")
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, fmt.Errorf("ALERT_WEBHOOK_URL must be an http or https URL with a host, got scheme %q", u.Scheme)
	}
	cfg := &alertWebhookConfig{
		url:       raw,
		host:      u.Host,
		textOnly:  strings.EqualFold(u.Hostname(), googleChatHost),
		publicURL: publicURL,
	}
	if secret := os.Getenv("ALERT_WEBHOOK_SECRET"); secret != "" {
		cfg.secret = []byte(secret)
	}
	return cfg, nil
}

// parsePublicURL reads PUBLIC_URL, the address dispatchers open the admin site
// at, such as https://tracker.example.org. It returns "" when it is unset.
func parsePublicURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil {
		// Not wrapped, like ALERT_WEBHOOK_URL: the value may carry a password.
		return "", errors.New("PUBLIC_URL is not a valid URL")
	}
	if u.User != nil {
		return "", errors.New("PUBLIC_URL must not carry a username or password: it is sent in every alert")
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", fmt.Errorf("PUBLIC_URL must be an http or https URL with a host, got %q", v)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("PUBLIC_URL must not have a query or a fragment, got %q", v)
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// alertSource is where the webhook reads the alerts: the alertWatcher.
type alertSource interface {
	list(ctx context.Context, now time.Time) ([]alertEntry, error)
}

// alertKey tells one alert from another. A trip has at most one alert of each
// kind at a time, and a not-started alert is about the trip after it. The
// since is left out: a not-moving vehicle's can shift while the alert stays.
type alertKey struct {
	kind     string
	tripID   int64
	nextTrip string
}

func keyOf(a alertEntry) alertKey {
	key := alertKey{kind: a.Kind, tripID: a.Trip.ID}
	if a.NextTrip != nil {
		key.nextTrip = a.NextTrip.GtfsTripID
	}
	return key
}

// alertWebhookPayload is the body of a post. It carries what a dispatcher
// needs to find the vehicle and the trip, and no driver, email or position:
// it leaves the server for a chat service.
type alertWebhookPayload struct {
	Event        string `json:"event"`
	Kind         string `json:"kind"`
	VehicleID    string `json:"vehicle_id"`
	VehicleLabel string `json:"vehicle_label"`
	// RouteID, RouteShortName and GtfsTripID are the trip a not-started
	// alert is about, the trip running for the others. RouteShortName is ""
	// when the schedule gives the route none.
	RouteID        string `json:"route_id"`
	RouteShortName string `json:"route_short_name"`
	GtfsTripID     string `json:"gtfs_trip_id"`
	// TripID is the trip whose page Link opens: the one running, or for a
	// not-started alert the vehicle's last.
	TripID int64     `json:"trip_id"`
	Since  time.Time `json:"since"`
	Link   string    `json:"link,omitempty"`
	Text   string    `json:"text"`
}

// alertWebhookEvent is one post waiting to be sent.
type alertWebhookEvent struct {
	key     alertKey
	payload alertWebhookPayload
}

// alertWebhook posts each alert to ALERT_WEBHOOK_URL once when it is raised and
// once when it clears. One goroutine works the alerts out every interval, as a
// load of the Alerts page does, and queues what changed; another sends the
// queue in order. Neither runs on a request, and a slow or dead receiver holds
// up only the sends.
type alertWebhook struct {
	cfg      alertWebhookConfig
	source   alertSource
	settings alertSettings
	schedule func() *rider.Index
	timing   alertWebhookTiming
	client   *http.Client

	// open is the alerts raised and not cleared, each as it was raised. Only
	// the checking goroutine uses it.
	open map[alertKey]alertEntry

	mu sync.Mutex
	// queue is the posts waiting, oldest first.
	queue []alertWebhookEvent
	// failed is the alerts whose failed post has been logged, so a second
	// failure for the same alert is not.
	failed map[alertKey]struct{}
	// wake tells the sending goroutine a post has been queued.
	wake chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// newAlertWebhook starts the webhook: alerts are read from source, judged by
// settings, and named from the routes and in the timezone of the schedule
// index returns.
func newAlertWebhook(cfg alertWebhookConfig, source alertSource, settings alertSettings, index func() *rider.Index, timing alertWebhookTiming) *alertWebhook {
	// A background job outlives any request, so it owns its context.
	ctx, cancel := context.WithCancel(context.Background())
	h := &alertWebhook{
		cfg:      cfg,
		source:   source,
		settings: settings,
		schedule: index,
		timing:   timing,
		client: &http.Client{
			Timeout: timing.timeout,
			// A redirect is not followed: on a 301 or 302 Go would resend the
			// POST as a GET with no body and count the page it lands on as
			// sent.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		open:   make(map[alertKey]alertEntry),
		failed: make(map[alertKey]struct{}),
		wake:   make(chan struct{}, 1),
		ctx:    ctx,
		cancel: cancel,
	}
	h.wg.Go(h.checkEvery)
	h.wg.Go(h.sendQueued)
	return h
}

// Stop cancels a check or a post in flight and waits for both goroutines to
// exit. Posts still queued are not sent. Safe to call more than once.
func (h *alertWebhook) Stop() {
	h.once.Do(h.cancel)
	h.wg.Wait()
}

func (h *alertWebhook) checkEvery() {
	ticker := time.NewTicker(h.timing.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.check(time.Now())
		case <-h.ctx.Done():
			return
		}
	}
}

// check works the alerts out at now and queues a post for each alert raised
// and each cleared since the last check. The clears go first, so a vehicle
// whose alert changes kind is cleared of the one before it is raised for the
// other. A check that fails changes nothing: an outage neither raises nor
// clears an alert.
func (h *alertWebhook) check(now time.Time) {
	ctx, cancel := context.WithTimeout(h.ctx, h.timing.interval)
	defer cancel()
	alerts, err := h.source.list(ctx, now)
	if err != nil {
		if h.ctx.Err() == nil {
			slog.Error("alert webhook: list alerts", "error", err)
		}
		return
	}

	current := make(map[alertKey]struct{}, len(alerts))
	for _, a := range alerts {
		current[keyOf(a)] = struct{}{}
	}
	cleared := make([]alertEntry, 0)
	for key, a := range h.open {
		if _, ok := current[key]; !ok {
			delete(h.open, key)
			cleared = append(cleared, a)
		}
	}
	sortAlerts(cleared)
	for _, a := range cleared {
		h.enqueue(h.event(alertCleared, a))
	}
	for _, a := range alerts {
		key := keyOf(a)
		if _, ok := h.open[key]; ok {
			continue
		}
		h.open[key] = a
		h.enqueue(h.event(alertRaised, a))
	}
}

// event is the post for a, raised or cleared.
func (h *alertWebhook) event(event string, a alertEntry) alertWebhookEvent {
	p := alertWebhookPayload{
		Event:        event,
		Kind:         a.Kind,
		VehicleID:    a.Trip.VehicleID,
		VehicleLabel: a.Trip.VehicleLabel,
		RouteID:      a.Trip.RouteID,
		GtfsTripID:   a.Trip.GtfsTripID,
		TripID:       a.Trip.ID,
		Since:        a.Since.UTC(),
	}
	if a.NextTrip != nil {
		p.RouteID, p.GtfsTripID = a.NextTrip.RouteID, a.NextTrip.GtfsTripID
	}
	route := p.RouteID
	zone := time.UTC
	if ix := h.schedule(); ix != nil {
		if r, ok := ix.Route(p.RouteID); ok {
			p.RouteShortName = r.ShortName
			route = cmp.Or(r.ShortName, r.LongName, r.ID)
		}
		zone = ix.Timezone()
	}
	vehicle := cmp.Or(a.Trip.VehicleLabel, a.Trip.VehicleID)
	p.Text = alertText(event, a, vehicle, route, a.Since.In(zone).Format("15:04"), h.settings)
	if h.cfg.publicURL != "" {
		p.Link = h.cfg.publicURL + "/admin/trips/" + strconv.FormatInt(a.Trip.ID, 10)
		p.Text += " " + p.Link
	}
	return alertWebhookEvent{key: keyOf(a), payload: p}
}

// alertText is one sentence on a, for a chat service to show as it is. at is
// the alert's since, in the schedule's timezone. A clear says only that the
// alert has gone, not why: the vehicle may have reported or moved, or its trip
// may have ended.
func alertText(event string, a alertEntry, vehicle, route, at string, settings alertSettings) string {
	if a.Kind == alertNotStarted {
		next := fmt.Sprintf("route %s, block %s, due at %s", route, a.NextTrip.BlockID, at)
		if event == alertCleared {
			return fmt.Sprintf("Cleared: %s, next trip not started: %s.", vehicle, next)
		}
		return fmt.Sprintf("%s has not started its next trip: %s.", vehicle, next)
	}

	subject := vehicle
	if route != "" {
		subject += " on route " + route
	}
	if event == alertCleared {
		return fmt.Sprintf("Cleared: %s, %s since %s.", subject, alertPhrase(a.Kind), at)
	}
	switch a.Kind {
	case alertQuiet:
		return fmt.Sprintf("%s has gone quiet: no report since %s.", subject, at)
	case alertOffRoute:
		return fmt.Sprintf("%s is off route: over %.0f m from it since %s.", subject, settings.offRouteDistance, at)
	default:
		return fmt.Sprintf("%s is not moving: under %.0f m since %s.", subject, stoppedRadius, at)
	}
}

// alertPhrase is an alert kind as a clear names it.
func alertPhrase(kind string) string {
	switch kind {
	case alertQuiet:
		return "gone quiet"
	case alertOffRoute:
		return "off route"
	default:
		return "not moving"
	}
}

// enqueue adds ev to the queue without waiting, so a slow receiver never holds
// up a check. A full queue drops its oldest post.
func (h *alertWebhook) enqueue(ev alertWebhookEvent) {
	h.mu.Lock()
	var dropped alertWebhookEvent
	full := len(h.queue) >= h.timing.queueSize
	if full {
		dropped = h.queue[0]
		h.queue = h.queue[1:]
		// The alert is over for the webhook, as if its clear had been sent.
		if dropped.payload.Event == alertCleared {
			delete(h.failed, dropped.key)
		}
	}
	h.queue = append(h.queue, ev)
	h.mu.Unlock()

	if full {
		slog.Warn("alert webhook: queue full, dropped the oldest post",
			"event", dropped.payload.Event, "kind", dropped.payload.Kind, "vehicle_id", dropped.payload.VehicleID)
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *alertWebhook) sendQueued() {
	for {
		ev, ok := h.next()
		if !ok {
			select {
			case <-h.wake:
				continue
			case <-h.ctx.Done():
				return
			}
		}
		err := h.deliver(ev.payload)
		// A post cut short by Stop did not fail.
		if h.ctx.Err() != nil {
			return
		}
		h.settle(ev, err)
	}
}

func (h *alertWebhook) next() (alertWebhookEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queue) == 0 {
		return alertWebhookEvent{}, false
	}
	ev := h.queue[0]
	h.queue = h.queue[1:]
	return ev, true
}

// settle logs a failed post, once per alert, and forgets the alert once its
// clear has been tried.
func (h *alertWebhook) settle(ev alertWebhookEvent, err error) {
	h.mu.Lock()
	_, logged := h.failed[ev.key]
	switch {
	case ev.payload.Event == alertCleared:
		delete(h.failed, ev.key)
	case err != nil:
		h.failed[ev.key] = struct{}{}
	}
	h.mu.Unlock()

	if err != nil && !logged {
		slog.Error("alert webhook: post failed", "host", h.cfg.host,
			"event", ev.payload.Event, "kind", ev.payload.Kind, "vehicle_id", ev.payload.VehicleID, "error", err)
	}
}

// deliver posts p, retrying after each wait in timing.backoff when the
// receiver could not be reached or timed out, or answered 5xx or 429 Too Many
// Requests. Any other answer is final: a 4xx says the post itself is wrong, and
// sending it again gets the same answer.
func (h *alertWebhook) deliver(p alertWebhookPayload) error {
	var body []byte
	var err error
	if h.cfg.textOnly {
		body, err = json.Marshal(struct {
			Text string `json:"text"`
		}{p.Text})
	} else {
		body, err = json.Marshal(p)
	}
	if err != nil {
		return fmt.Errorf("encode the post: %w", err)
	}

	for attempt := 0; ; attempt++ {
		retry, err := h.post(body)
		if err == nil || !retry || attempt == len(h.timing.backoff) {
			return err
		}
		select {
		case <-time.After(h.timing.backoff[attempt]):
		case <-h.ctx.Done():
			return h.ctx.Err()
		}
	}
}

// post makes one attempt at sending body. retry says whether a failure is
// worth another.
func (h *alertWebhook) post(body []byte) (retry bool, err error) {
	req, err := http.NewRequestWithContext(h.ctx, http.MethodPost, h.cfg.url, bytes.NewReader(body))
	if err != nil {
		// Not wrapped: the error repeats the URL.
		return false, errors.New("could not build the request")
	}
	req.Header.Set("Content-Type", "application/json")
	if h.cfg.secret != nil {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set(alertWebhookTimestampHeader, timestamp)
		req.Header.Set(alertWebhookSignatureHeader, "sha256="+signAlertWebhook(h.cfg.secret, timestamp, body))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		// A *url.Error repeats the URL, whose path can be the credential.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return true, err
	}
	defer resp.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	// Drained so the connection can be used again.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return false, fmt.Errorf("answered %d, a redirect, which is not followed: set ALERT_WEBHOOK_URL to where it leads", resp.StatusCode)
	}
	err = fmt.Errorf("answered %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	return resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests, err
}

// signAlertWebhook is the hex HMAC-SHA256, keyed with secret, of the
// timestamp, a dot and the body. Signing the timestamp lets a receiver refuse
// a post replayed later.
func signAlertWebhook(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
