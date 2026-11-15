package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// healthTestClock is a clock the tests move by hand, so minute rollover is
// tested without sleeping. It is safe to read from many goroutines.
type healthTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func newHealthTestClock(t time.Time) *healthTestClock { return &healthTestClock{t: t} }

func (c *healthTestClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *healthTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// healthTestStart is 30 seconds into a minute, so a test that advances by
// whole minutes never lands on a minute boundary by accident.
var healthTestStart = time.Date(2026, 11, 14, 9, 15, 30, 0, time.UTC)

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		status int
		want   healthOutcome
	}{
		{http.StatusOK, outcomeOK},
		{http.StatusCreated, outcomeOK},
		{http.StatusNoContent, outcomeOK},
		{399, outcomeOK},
		{http.StatusBadRequest, outcomeInvalid},
		{http.StatusConflict, outcomeInvalid},
		{http.StatusRequestEntityTooLarge, outcomeInvalid},
		{http.StatusUnsupportedMediaType, outcomeInvalid},
		{499, outcomeInvalid},
		{http.StatusUnauthorized, outcomeDenied},
		{http.StatusForbidden, outcomeDenied},
		{http.StatusTooManyRequests, outcomeRateLimited},
		{http.StatusInternalServerError, outcomeServerError},
		{http.StatusBadGateway, outcomeServerError},
		{http.StatusServiceUnavailable, outcomeServerError},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			assert.Equal(t, tc.want, classifyStatus(tc.status), "status %d", tc.status)
		})
	}
}

func TestFeedHealth_CountsLandInTheMinuteTheyHappened(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)

	h.record(healthReports, http.StatusCreated)
	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Minute)
	h.record(healthReports, http.StatusBadRequest)
	h.record(healthFeed, http.StatusOK)

	snap := h.snapshot()
	require.Len(t, snap.Minutes, healthWindowMinutes)

	last := snap.Minutes[healthWindowMinutes-1]
	prev := snap.Minutes[healthWindowMinutes-2]
	assert.Equal(t, time.Date(2026, 11, 14, 9, 16, 0, 0, time.UTC), last.Minute, "the last row is the minute in progress")
	assert.Equal(t, time.Date(2026, 11, 14, 9, 15, 0, 0, time.UTC), prev.Minute)
	assert.Equal(t, reportOutcomes{Invalid: 1}, last.Reports)
	assert.Equal(t, feedOutcomes{Served: 1}, last.Feed)
	assert.Equal(t, reportOutcomes{Accepted: 2}, prev.Reports)
	assert.Equal(t, feedOutcomes{}, prev.Feed)

	assert.Equal(t, time.Date(2026, 11, 14, 8, 17, 0, 0, time.UTC), snap.Minutes[0].Minute, "the first row is 59 minutes before the last")
	assert.Equal(t, reportOutcomes{Accepted: 2, Invalid: 1}, snap.LastHour.Reports)
	assert.Equal(t, snap.LastHour, snap.Total)
	assert.Equal(t, healthTestStart, snap.Since)
}

func TestFeedHealth_MinutesAreConsecutive(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)

	snap := h.snapshot()
	require.Len(t, snap.Minutes, healthWindowMinutes)
	for i := 1; i < len(snap.Minutes); i++ {
		assert.Equal(t, time.Minute, snap.Minutes[i].Minute.Sub(snap.Minutes[i-1].Minute), "row %d", i)
	}
}

func TestFeedHealth_AMinuteLeavesTheHourAfterSixtyMinutes(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	h.record(healthReports, http.StatusCreated)

	clock.advance(59 * time.Minute)
	snap := h.snapshot()
	assert.Equal(t, uint64(1), snap.Minutes[0].Reports.Accepted, "59 minutes on, the report is the oldest row")
	assert.Equal(t, uint64(1), snap.LastHour.Reports.Accepted)

	clock.advance(time.Minute)
	snap = h.snapshot()
	assert.Equal(t, uint64(0), snap.LastHour.Reports.Accepted, "60 minutes on, the report has left the hour")
	assert.Equal(t, uint64(1), snap.Total.Reports.Accepted, "but it stays in the total")
}

// The ring has one bucket per minute of the hour, so a minute an hour later
// writes to the same bucket. The old counts must be cleared, not added to.
func TestFeedHealth_ReusedBucketStartsFromZero(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	h.record(healthReports, http.StatusCreated)
	h.record(healthReports, http.StatusCreated)

	clock.advance(healthWindowMinutes * time.Minute)
	h.record(healthReports, http.StatusBadRequest)

	snap := h.snapshot()
	assert.Equal(t, reportOutcomes{Invalid: 1}, snap.Minutes[healthWindowMinutes-1].Reports)
	assert.Equal(t, reportOutcomes{Invalid: 1}, snap.LastHour.Reports)
	assert.Equal(t, reportOutcomes{Accepted: 2, Invalid: 1}, snap.Total.Reports)
}

// A bucket left over from an earlier hour, never overwritten because nothing
// came in during its minute since, must read as empty.
func TestFeedHealth_StaleBucketReadsAsEmpty(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	h.record(healthFeed, http.StatusOK)

	clock.advance(3 * time.Hour)
	snap := h.snapshot()
	assert.Equal(t, feedOutcomes{}, snap.LastHour.Feed)
	for _, m := range snap.Minutes {
		assert.Equal(t, feedOutcomes{}, m.Feed, "minute %s", m.Minute)
	}
	assert.Equal(t, feedOutcomes{Served: 1}, snap.Total.Feed)
}

func TestFeedHealth_ConcurrentRecords(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)

	const workers, each = 16, 250
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range each {
				if i == 0 && j%50 == 0 {
					clock.advance(time.Minute)
				}
				h.record(healthReports, http.StatusCreated)
				h.record(healthFeed, http.StatusUnauthorized)
				if j%25 == 0 {
					h.snapshot()
				}
			}
		}()
	}
	wg.Wait()

	snap := h.snapshot()
	assert.Equal(t, uint64(workers*each), snap.Total.Reports.Accepted)
	assert.Equal(t, uint64(workers*each), snap.Total.Feed.Refused)
	assert.Equal(t, snap.Total, snap.LastHour, "every record happened within the hour")
}

func TestNewHealthPeriod_MapsEveryOutcome(t *testing.T) {
	var c healthCounts
	for o := range numHealthOutcomes {
		c[healthReports][o] = uint64(10 + o)
		c[healthFeed][o] = uint64(20 + o)
	}

	p := newHealthPeriod(c)
	assert.Equal(t, reportOutcomes{Accepted: 10, Invalid: 11, Unauthorized: 12, RateLimited: 13, ServerError: 14}, p.Reports)
	// A 429 on the feed is a fetch not served, counted with bad requests.
	assert.Equal(t, feedOutcomes{Served: 20, Invalid: 21 + 23, Refused: 22, ServerError: 24}, p.Feed)
}

func TestOutcomeTotals(t *testing.T) {
	r := reportOutcomes{Accepted: 1, Invalid: 2, Unauthorized: 4, RateLimited: 8, ServerError: 16}
	assert.Equal(t, uint64(30), r.Rejected())
	assert.Equal(t, uint64(31), r.Total())

	f := feedOutcomes{Served: 1, Refused: 2, Invalid: 4, ServerError: 8}
	assert.Equal(t, uint64(15), f.Total())
}

func TestFeedHealthCount_RecordsTheStatusWritten(t *testing.T) {
	tests := []struct {
		name     string
		endpoint healthEndpoint
		handler  http.HandlerFunc
		want     healthPeriod
	}{
		{
			name:     "report accepted",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusCreated, nil) },
			want:     healthPeriod{Reports: reportOutcomes{Accepted: 1}},
		},
		{
			name:     "report invalid",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusBadRequest, nil) },
			want:     healthPeriod{Reports: reportOutcomes{Invalid: 1}},
		},
		{
			name:     "report wrong content type",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusUnsupportedMediaType, nil) },
			want:     healthPeriod{Reports: reportOutcomes{Invalid: 1}},
		},
		{
			name:     "report with no running trip",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusConflict, nil) },
			want:     healthPeriod{Reports: reportOutcomes{Invalid: 1}},
		},
		{
			name:     "report signed out",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusUnauthorized, nil) },
			want:     healthPeriod{Reports: reportOutcomes{Unauthorized: 1}},
		},
		{
			name:     "report forbidden",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusForbidden, nil) },
			want:     healthPeriod{Reports: reportOutcomes{Unauthorized: 1}},
		},
		{
			name:     "report rate limited",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusTooManyRequests, nil) },
			want:     healthPeriod{Reports: reportOutcomes{RateLimited: 1}},
		},
		{
			name:     "report server error",
			endpoint: healthReports,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusInternalServerError, nil) },
			want:     healthPeriod{Reports: reportOutcomes{ServerError: 1}},
		},
		{
			name:     "feed served with a body and no WriteHeader",
			endpoint: healthFeed,
			handler:  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("feed")) },
			want:     healthPeriod{Feed: feedOutcomes{Served: 1}},
		},
		{
			name:     "feed served with nothing written",
			endpoint: healthFeed,
			handler:  func(w http.ResponseWriter, r *http.Request) {},
			want:     healthPeriod{Feed: feedOutcomes{Served: 1}},
		},
		{
			name:     "feed refused key",
			endpoint: healthFeed,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusUnauthorized, nil) },
			want:     healthPeriod{Feed: feedOutcomes{Refused: 1}},
		},
		{
			name:     "feed bad source",
			endpoint: healthFeed,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusBadRequest, nil) },
			want:     healthPeriod{Feed: feedOutcomes{Invalid: 1}},
		},
		{
			name:     "feed server error",
			endpoint: healthFeed,
			handler:  func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusInternalServerError, nil) },
			want:     healthPeriod{Feed: feedOutcomes{ServerError: 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newFeedHealth(newHealthTestClock(healthTestStart).now)
			handler := h.count(tc.endpoint)(tc.handler)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

			snap := h.snapshot()
			assert.Equal(t, tc.want, snap.LastHour)
			assert.Equal(t, tc.want, snap.Total)
		})
	}
}

// The wrapper must pass the response through untouched: the client still gets
// the status and body the handler wrote.
func TestFeedHealthCount_PassesTheResponseThrough(t *testing.T) {
	h := newFeedHealth(newHealthTestClock(healthTestStart).now)
	handler := h.count(healthReports)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.JSONEq(t, `{"error":"rate limit exceeded"}`, rec.Body.String())
}

func TestHandleFeedHealth(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	h.record(healthReports, http.StatusCreated)
	h.record(healthFeed, http.StatusUnauthorized)

	rec := httptest.NewRecorder()
	handleFeedHealth(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Since    string `json:"since"`
		LastHour struct {
			Reports map[string]uint64 `json:"reports"`
			Feed    map[string]uint64 `json:"feed"`
		} `json:"last_hour"`
		Total struct {
			Reports map[string]uint64 `json:"reports"`
			Feed    map[string]uint64 `json:"feed"`
		} `json:"total"`
		Minutes []struct {
			Minute  string            `json:"minute"`
			Reports map[string]uint64 `json:"reports"`
			Feed    map[string]uint64 `json:"feed"`
		} `json:"minutes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "2026-11-14T09:15:30Z", body.Since)
	wantReports := map[string]uint64{"accepted": 1, "invalid": 0, "unauthorized": 0, "rate_limited": 0, "server_error": 0}
	wantFeed := map[string]uint64{"served": 0, "refused": 1, "invalid": 0, "server_error": 0}
	assert.Equal(t, wantReports, body.LastHour.Reports)
	assert.Equal(t, wantFeed, body.LastHour.Feed)
	assert.Equal(t, wantReports, body.Total.Reports)
	assert.Equal(t, wantFeed, body.Total.Feed)
	require.Len(t, body.Minutes, healthWindowMinutes)
	assert.Equal(t, "2026-11-14T09:15:00Z", body.Minutes[healthWindowMinutes-1].Minute)
	assert.Equal(t, wantReports, body.Minutes[healthWindowMinutes-1].Reports)
}
