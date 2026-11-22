package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	u, err := parseHTTPURL("ALERT_WEBHOOK_URL", raw)
	if err != nil {
		return nil, err
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
	u, err := parseHTTPURL("PUBLIC_URL", v)
	if err != nil {
		return "", err
	}
	if u.User != nil {
		return "", errors.New("PUBLIC_URL must not carry a username or password: it is sent in every alert")
	}
	if u.RawQuery != "" {
		return "", errors.New("PUBLIC_URL must not have a query")
	}
	if u.Fragment != "" {
		return "", errors.New("PUBLIC_URL must not have a fragment")
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// parseHTTPURL parses raw, the value of the setting name, as an http or https
// URL with a host. Its errors name the setting and what is wrong with it, never
// the value: they go to the startup log, and the value can be a credential.
func parseHTTPURL(name, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// Not wrapped: url.Parse's error repeats the value.
		return nil, fmt.Errorf("%s is not a valid URL", name)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("%s must start with http:// or https://", name)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%s has no host", name)
	}
	return u, nil
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

	// started is when the webhook started, the start of what last covers.
	started time.Time

	mu sync.Mutex
	// queue is the posts waiting, oldest first.
	queue []alertWebhookEvent
	// sending is set while a post taken off the queue is being sent, its
	// retries included.
	sending bool
	// last is the most recent post sent or given up on, nil until one is.
	// A delivery is never changed once made, so a status may keep it.
	last *alertWebhookDelivery
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
		started: time.Now(),
		open:    make(map[alertKey]alertEntry),
		failed:  make(map[alertKey]struct{}),
		wake:    make(chan struct{}, 1),
		ctx:     ctx,
		cancel:  cancel,
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
	case alertBunched:
		return fmt.Sprintf("%s is bunched: %s, since %s.", subject, aheadWords(a.Ahead), at)
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
	case alertBunched:
		return "bunched"
	default:
		return "not moving"
	}
}

// aheadWords says how far behind the vehicle ahead a bunched vehicle is
// running, against how often the stop is served: "1 min behind Bus 12, every
// 10 min". The Alerts page and the webhook both say it so.
func aheadWords(a *alertAhead) string {
	return fmt.Sprintf("%s behind %s, every %s",
		humanizeDuration(time.Duration(a.SpacingS)*time.Second), cmp.Or(a.VehicleLabel, a.VehicleID),
		humanizeDuration(time.Duration(a.ScheduledGapS)*time.Second))
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
	h.sending = true
	return ev, true
}

// settle logs a failed post, once per alert, and forgets the alert once its
// clear has been tried.
func (h *alertWebhook) settle(ev alertWebhookEvent, err error) {
	h.mu.Lock()
	h.sending = false
	h.last = newAlertWebhookDelivery(ev, err, time.Now())
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
		return &webhookFailure{plain: "The post could not be encoded", err: fmt.Errorf("encode the post: %w", err)}
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
		return false, &webhookFailure{plain: "The URL could not be used", err: errors.New("could not build the request")}
	}
	req.Header.Set("Content-Type", "application/json")
	if h.cfg.secret != nil {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set(alertWebhookTimestampHeader, timestamp)
		req.Header.Set(alertWebhookSignatureHeader, "sha256="+signAlertWebhook(h.cfg.secret, timestamp, body))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return true, h.noAnswer(err)
	}
	defer resp.Body.Close()
	// Drained so the connection can be used again, and never read: a receiver
	// can quote the path back, credential and all.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return false, nil
	case code >= 300 && code < 400:
		return false, &webhookFailure{
			status: code,
			plain:  fmt.Sprintf("Answered %d, a redirect, which is not followed. Set ALERT_WEBHOOK_URL to where it leads.", code),
			err:    fmt.Errorf("answered %d, a redirect, which is not followed: set ALERT_WEBHOOK_URL to where it leads", code),
		}
	}
	err = &webhookFailure{status: code, plain: fmt.Sprintf("Answered %d %s", code, http.StatusText(code)), err: fmt.Errorf("answered %d", code)}
	return code >= 500 || code == http.StatusTooManyRequests, err
}

// noAnswer says, for the log, why a post got no answer, in words that carry
// nothing of the URL past its host. Go's error repeats the URL, and when the
// reply is not HTTP it quotes the reply, which from a receiver that echoes the
// request is the path. So only a timeout, a failure to reach the host, which
// names its address, and an untrusted certificate are told apart.
func (h *alertWebhook) noAnswer(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &webhookFailure{plain: "No answer within " + h.timing.timeout.String(), err: fmt.Errorf("no answer within %s", h.timing.timeout)}
	}
	var dialErr *net.OpError
	if errors.As(err, &dialErr) && dialErr.Op == "dial" {
		return &webhookFailure{plain: "Could not connect to " + h.cfg.host, err: dialErr}
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return &webhookFailure{plain: "The certificate of " + h.cfg.host + " is not trusted", err: certErr}
	}
	return &webhookFailure{plain: "No valid HTTP answer", err: errors.New("no valid HTTP answer")}
}

// webhookFailure is why a post failed: err in the words the log has always
// used, and plain in the words the Alerts page shows. Neither carries any of
// the URL past its host. status is the HTTP status the receiver answered, 0
// when it gave none.
type webhookFailure struct {
	status int
	plain  string
	err    error
}

func (f *webhookFailure) Error() string { return f.err.Error() }
func (f *webhookFailure) Unwrap() error { return f.err }

// alertWebhookDelivery is the outcome of one post, sent or given up on after
// its retries.
type alertWebhookDelivery struct {
	At        time.Time
	Event     string
	Kind      string
	VehicleID string
	Sent      bool
	// Status is the HTTP status the receiver answered, 0 when it gave none.
	Status int
	// Problem says in plain words why a post failed; "" when it was sent.
	Problem string
}

func newAlertWebhookDelivery(ev alertWebhookEvent, err error, at time.Time) *alertWebhookDelivery {
	d := &alertWebhookDelivery{At: at, Event: ev.payload.Event, Kind: ev.payload.Kind, VehicleID: ev.payload.VehicleID, Sent: err == nil}
	if err == nil {
		return d
	}
	d.Problem = "The post failed"
	var f *webhookFailure
	if errors.As(err, &f) {
		d.Status, d.Problem = f.status, f.plain
	}
	return d
}

// alertWebhookStatus is the webhook as the Alerts page shows it, read in one
// go so the page never sees the sender half way through a change.
type alertWebhookStatus struct {
	// Host is the host of ALERT_WEBHOOK_URL, the only part of it shown: a
	// chat service's URL carries its credential in the path.
	Host    string
	Started time.Time
	// Waiting is the posts queued, Sending whether one is being sent.
	Waiting int
	Sending bool
	// Last is the most recent post sent or given up on, nil when none has
	// been since the server started.
	Last *alertWebhookDelivery
}

func (h *alertWebhook) status() alertWebhookStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return alertWebhookStatus{Host: h.cfg.host, Started: h.started, Waiting: len(h.queue), Sending: h.sending, Last: h.last}
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
