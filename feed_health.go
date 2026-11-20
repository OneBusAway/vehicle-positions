package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// healthWindowMinutes is how many one-minute buckets the feed health ring
// keeps: the last hour, counting the minute in progress. The ring is a fixed
// array, so memory stays the same however busy the server is.
const healthWindowMinutes = 60

// liveReportPath is the route of a live position report, the one whose
// vehicles the Health page counts. A history upload is the phone's offline
// backlog, positions from earlier that never reach the feed, so it does not
// make its vehicle one reporting now.
const liveReportPath = "/api/v1/locations"

// healthEndpoint names a group of routes whose responses are counted together.
type healthEndpoint int

const (
	// healthReports is a driver phone sending positions: POST
	// /api/v1/locations and POST /api/v1/locations/history.
	healthReports healthEndpoint = iota
	// healthFeed is a consumer fetching GET /gtfs-rt/vehicle-positions, in
	// either format.
	healthFeed
	numHealthEndpoints
)

// healthOutcome is what became of one request, read from its status code.
type healthOutcome int

const (
	outcomeOK healthOutcome = iota
	outcomeInvalid
	outcomeDenied
	outcomeRateLimited
	outcomeServerError
	numHealthOutcomes
)

// classifyStatus sorts a response status into an outcome. Every 4xx that is
// not an auth refusal or a rate limit counts as invalid: on the report routes
// that is a malformed body (400), the wrong Content-Type (415) and a report
// that does not match the driver's running trip (409).
func classifyStatus(status int) healthOutcome {
	switch {
	case status >= 500:
		return outcomeServerError
	case status == http.StatusTooManyRequests:
		return outcomeRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return outcomeDenied
	case status >= 400:
		return outcomeInvalid
	default:
		return outcomeOK
	}
}

type healthCounts [numHealthEndpoints][numHealthOutcomes]uint64

func (c *healthCounts) add(o healthCounts) {
	for e := range c {
		for k := range c[e] {
			c[e][k] += o[e][k]
		}
	}
}

// healthBucket holds one minute's counts. minute is the Unix minute they
// belong to; a bucket whose minute is not the one being read holds counts
// from an hour or more ago and reads as empty.
type healthBucket struct {
	minute int64
	counts healthCounts
	// vehicles is how many vehicles had a live report accepted in the minute,
	// each counted once.
	vehicles int
}

// feedHealth counts report and feed responses in memory, per minute for the
// last hour and in total since the server started. Nothing is persisted, so a
// restart starts the counts again from since.
type feedHealth struct {
	now   func() time.Time
	since time.Time

	mu     sync.Mutex
	ring   [healthWindowMinutes]healthBucket
	totals healthCounts
	// lastMinute is the minute each vehicle last had a live report accepted,
	// so a minute counts a vehicle once however often it reports. It holds one
	// entry per vehicle that has reported since the server started, which only
	// the fleet's own vehicles can, so it is bounded by the fleet.
	lastMinute map[string]int64
}

func newFeedHealth(now func() time.Time) *feedHealth {
	return &feedHealth{now: now, since: now(), lastMinute: make(map[string]int64)}
}

func unixMinute(t time.Time) int64 { return t.Unix() / 60 }

func (h *feedHealth) record(endpoint healthEndpoint, status int) {
	outcome := classifyStatus(status)
	minute := unixMinute(h.now())

	h.mu.Lock()
	defer h.mu.Unlock()
	h.bucket(minute).counts[endpoint][outcome]++
	h.totals[endpoint][outcome]++
}

// recordVehicle counts vehicleID in the minute its live report was accepted
// in, unless it already counts there.
func (h *feedHealth) recordVehicle(vehicleID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// The minute is read under the lock, so two reports cannot reach the map
	// out of order and count a vehicle twice in a minute.
	minute := unixMinute(h.now())
	if last, ok := h.lastMinute[vehicleID]; ok && last >= minute {
		return
	}
	h.lastMinute[vehicleID] = minute
	h.bucket(minute).vehicles++
}

// bucket is minute's bucket, emptied first when it still holds an older
// minute. The caller holds h.mu.
func (h *feedHealth) bucket(minute int64) *healthBucket {
	b := &h.ring[minute%healthWindowMinutes]
	if b.minute != minute {
		*b = healthBucket{minute: minute}
	}
	return b
}

// count wraps a route so every response it writes is counted under endpoint.
// It goes outside the auth middleware, so a refused token or key is counted
// too. A handler that never calls WriteHeader has answered 200.
//
// A live report names its vehicle in the body, so count keeps a copy of what
// the handler reads and, when the handler accepts the report, counts the
// vehicle_id in it. The auth middleware refuses a request before anything
// reads its body, so a refused one costs no copy.
func (h *feedHealth) count(endpoint healthEndpoint) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			var body bytes.Buffer
			live := endpoint == healthReports && r.URL.Path == liveReportPath
			if live {
				r.Body = struct {
					io.Reader
					io.Closer
				}{io.TeeReader(r.Body, &body), r.Body}
			}
			next.ServeHTTP(rec, r)
			h.record(endpoint, rec.status)
			if live && classifyStatus(rec.status) == outcomeOK {
				var report struct {
					VehicleID string `json:"vehicle_id"`
				}
				if json.Unmarshal(body.Bytes(), &report) == nil && report.VehicleID != "" {
					h.recordVehicle(report.VehicleID)
				}
			}
		})
	}
}

// reportOutcomes is what became of the position reports driver phones sent.
type reportOutcomes struct {
	Accepted     uint64 `json:"accepted"`
	Invalid      uint64 `json:"invalid"`
	Unauthorized uint64 `json:"unauthorized"`
	RateLimited  uint64 `json:"rate_limited"`
	ServerError  uint64 `json:"server_error"`
}

// Total is every report sent, accepted or not.
func (r reportOutcomes) Total() uint64 { return r.Accepted + r.Rejected() }

// Rejected is every report the server did not accept, for whatever reason.
func (r reportOutcomes) Rejected() uint64 {
	return r.Invalid + r.Unauthorized + r.RateLimited + r.ServerError
}

// feedOutcomes is what became of consumers' fetches of the GTFS-RT feed.
type feedOutcomes struct {
	Served      uint64 `json:"served"`
	Refused     uint64 `json:"refused"`
	Invalid     uint64 `json:"invalid"`
	ServerError uint64 `json:"server_error"`
}

// Total is every fetch, served or not.
func (f feedOutcomes) Total() uint64 { return f.Served + f.Refused + f.Invalid + f.ServerError }

// healthPeriod is the counts for one stretch of time.
type healthPeriod struct {
	Reports reportOutcomes `json:"reports"`
	Feed    feedOutcomes   `json:"feed"`
}

func newHealthPeriod(c healthCounts) healthPeriod {
	r, f := c[healthReports], c[healthFeed]
	return healthPeriod{
		Reports: reportOutcomes{
			Accepted:     r[outcomeOK],
			Invalid:      r[outcomeInvalid],
			Unauthorized: r[outcomeDenied],
			RateLimited:  r[outcomeRateLimited],
			ServerError:  r[outcomeServerError],
		},
		Feed: feedOutcomes{
			Served:  f[outcomeOK],
			Refused: f[outcomeDenied],
			// The feed has no rate limit, so a 429 there would come from
			// something new in front of it. It is still a fetch that was not
			// served, and is counted with the other refused requests rather
			// than dropped.
			Invalid:     f[outcomeInvalid] + f[outcomeRateLimited],
			ServerError: f[outcomeServerError],
		},
	}
}

// healthMinute is one minute's counts, keyed by the minute's start.
type healthMinute struct {
	Minute  time.Time      `json:"minute"`
	Reports reportOutcomes `json:"reports"`
	Feed    feedOutcomes   `json:"feed"`
	// Vehicles is how many vehicles had a live report accepted in the
	// minute, each counted once.
	Vehicles int `json:"vehicles"`
}

// feedHealthResponse is the body of GET /api/v1/admin/health, and what the
// admin pages render.
type feedHealthResponse struct {
	// Since is when counting started: the server's start.
	Since time.Time `json:"since"`
	// LastHour sums Minutes.
	LastHour healthPeriod `json:"last_hour"`
	Total    healthPeriod `json:"total"`
	// Minutes is the last hour, oldest first, ending with the minute in
	// progress. Every minute is listed, with zeros where nothing came in.
	Minutes []healthMinute `json:"minutes"`
}

func (h *feedHealth) snapshot() feedHealthResponse {
	// Read under the lock with the ring, so a report landing in the next
	// minute cannot overwrite the oldest bucket between the two.
	h.mu.Lock()
	current := unixMinute(h.now())
	ring, totals := h.ring, h.totals
	h.mu.Unlock()

	resp := feedHealthResponse{
		Since:   h.since.UTC(),
		Total:   newHealthPeriod(totals),
		Minutes: make([]healthMinute, 0, healthWindowMinutes),
	}
	var hour healthCounts
	for minute := current - healthWindowMinutes + 1; minute <= current; minute++ {
		var bucket healthBucket
		if b := ring[minute%healthWindowMinutes]; b.minute == minute {
			bucket = b
		}
		hour.add(bucket.counts)
		period := newHealthPeriod(bucket.counts)
		resp.Minutes = append(resp.Minutes, healthMinute{
			Minute:   time.Unix(minute*60, 0).UTC(),
			Reports:  period.Reports,
			Feed:     period.Feed,
			Vehicles: bucket.vehicles,
		})
	}
	resp.LastHour = newHealthPeriod(hour)
	return resp
}

// handleFeedHealth serves the report and feed counts as JSON.
func handleFeedHealth(health *feedHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, health.snapshot())
	}
}
