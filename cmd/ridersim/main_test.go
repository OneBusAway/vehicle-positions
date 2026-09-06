package main

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func fixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	ix, err := rider.LoadIndex(t.Context(), "../../rider/testdata/fixture.zip", nil, time.Now())
	require.NoError(t, err)
	return ix
}

func TestJitterAndOffset(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	base := rider.LatLon{Lat: 47.6, Lon: -122.33}
	var sum float64
	for i := 0; i < 1000; i++ {
		d := rider.Distance(base, jitter(base, 8, rng))
		assert.Less(t, d, 40.0)
		sum += d
	}
	assert.Less(t, sum/1000, 12.0)
	assert.InDelta(t, 300, rider.Distance(base, offsetEast(base, 300)), 1)
}

func TestPickTrips(t *testing.T) {
	ix := fixtureIndex(t)
	got, err := pickTrips(ix, []string{"T1"}, 0, "20260902")
	require.NoError(t, err)
	assert.Equal(t, []string{"T1"}, got)
	_, err = pickTrips(ix, []string{"T2"}, 0, "20260902")
	assert.Error(t, err, "Saturday-only trip on a Wednesday")
	_, err = pickTrips(ix, []string{"NOPE"}, 0, "20260902")
	assert.Error(t, err)
	got, err = pickTrips(ix, nil, 2, "20260902")
	require.NoError(t, err)
	assert.Len(t, got, 2)
	_, err = pickTrips(ix, nil, 1, "20271231")
	assert.Error(t, err, "outside calendar range: nothing active")
}

// newFlushRun builds a riderRun wired to a test server, with one fix buffered.
func newFlushRun(t *testing.T, handler http.HandlerFunc) *riderRun {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &riderRun{
		cfg:      &config{api: &apiClient{base: srv.URL, http: srv.Client()}},
		tag:      "rider-1",
		rideID:   "ride-1",
		maxBatch: 10,
		buf:      []position{{Latitude: 47.6, Longitude: -122.33, Timestamp: 1}},
	}
}

func TestFlush_ServerAnswers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantReason string
		wantBuf    int
	}{
		{"accepted", 200, `{"state":"verified","accepted":1}`, "", 0},
		{"rate limited keeps the batch", 429, `{}`, "", 1},
		{"ride gone drops the batch", 409, `{"error":"ride ended"}`, "gone", 0},
		{"server ended the ride", 200, `{"ended":true,"end_reason":"off_route"}`, "off_route", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFlushRun(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			reason, err := r.flush(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.wantReason, reason)
			assert.Len(t, r.buf, tc.wantBuf)
		})
	}
}

func TestFlush_RetriesOnceThenSucceeds(t *testing.T) {
	var calls int
	r := newFlushRun(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"state":"verified","accepted":1}`))
	})
	reason, err := r.flush(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reason)
	assert.Equal(t, 2, calls)
	assert.Empty(t, r.buf)
}

// Cancelling during the retry backoff is the operator ending the run, not the
// upload failing. The first attempt must complete — a 500 with no transport
// error — so that re-reading it as this batch's answer would return an error,
// which walk turns into an abandoned ride and a non-zero exit.
func TestFlush_CancelledDuringBackoffIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan struct{})
	var calls int
	r := newFlushRun(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		close(served)
	})
	// Cancel once the first attempt has been answered in full, so the
	// interruption lands inside the backoff rather than inside the request.
	go func() {
		<-served
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	reason, err := r.flush(ctx)
	require.NoError(t, err, "an interrupted run must not be reported as a failed upload")
	assert.Empty(t, reason)
	assert.Equal(t, 1, calls, "the retry must not run after cancellation")
	assert.Len(t, r.buf, 1, "the batch stays buffered for the final flush")
}
