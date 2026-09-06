package rider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func feedBytes(t *testing.T, entities ...*gtfsrt.FeedEntity) []byte {
	t.Helper()
	v, inc := "2.0", gtfsrt.FeedHeader_FULL_DATASET
	b, err := proto.Marshal(&gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: &v, Incrementality: &inc, Timestamp: proto.Uint64(uint64(time.Now().Unix()))},
		Entity: entities,
	})
	require.NoError(t, err)
	return b
}

func vpEntity(id, tripID, startDate string, lat, lon float64, ts time.Time) *gtfsrt.FeedEntity {
	trip := &gtfsrt.TripDescriptor{TripId: proto.String(tripID)}
	if startDate != "" {
		trip.StartDate = proto.String(startDate)
	}
	return &gtfsrt.FeedEntity{
		Id: proto.String(id),
		Vehicle: &gtfsrt.VehiclePosition{
			Trip:      trip,
			Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh-" + id)},
			Position:  &gtfsrt.Position{Latitude: proto.Float32(float32(lat)), Longitude: proto.Float32(float32(lon))},
			Timestamp: proto.Uint64(uint64(ts.Unix())),
		},
	}
}

func TestTrustedFeed_LookupExactThenDateless(t *testing.T) {
	now := time.Now()
	body := feedBytes(t,
		vpEntity("1", "T1", "20260902", 47.60, -122.33, now),
		vpEntity("2", "T3", "", 47.61, -122.32, now),
		vpEntity("3", "T9", "20260902", 47.62, -122.31, now.Add(-10*time.Minute)), // stale
		&gtfsrt.FeedEntity{Id: proto.String("4"), Vehicle: &gtfsrt.VehiclePosition{Position: &gtfsrt.Position{Latitude: proto.Float32(1), Longitude: proto.Float32(1)}}}, // no trip
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()

	f := NewTrustedFeed([]string{srv.URL}, srv.Client(), 5*time.Minute)
	assert.True(t, f.Configured())
	f.Poll(context.Background())

	v, ok := f.Lookup(TripKey{"T1", "20260902"}, now)
	require.True(t, ok)
	assert.Equal(t, "veh-1", v.VehicleID)
	assert.InDelta(t, 47.60, v.Pos.Lat, 0.0001)

	_, ok = f.Lookup(TripKey{"T1", "20260903"}, now)
	assert.False(t, ok, "different start_date does not match a dated entity")

	v, ok = f.Lookup(TripKey{"T3", "20260902"}, now)
	require.True(t, ok, "dateless entity matches any start_date")
	assert.Equal(t, "veh-2", v.VehicleID)
	assert.True(t, f.Covers(TripKey{"T3", "20261231"}, now))

	_, ok = f.Lookup(TripKey{"T9", "20260902"}, now)
	assert.False(t, ok, "stale entity filtered")
	assert.False(t, f.Covers(TripKey{"T9", "20260902"}, now))

	h := f.Health()
	require.Len(t, h, 1)
	assert.Equal(t, 3, h[0].Entities, "entity without a trip is dropped")
	assert.Empty(t, h[0].LastError)
	assert.WithinDuration(t, now, h[0].LastSuccess, 5*time.Second)
}

func TestTrustedFeed_ETagAnd304KeepsEntities(t *testing.T) {
	now := time.Now()
	body := feedBytes(t, vpEntity("1", "T1", "20260902", 47.60, -122.33, now))
	var hits atomic.Int32
	var sawIfNoneMatch atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"abc"` {
			sawIfNoneMatch.Store(true)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	f := NewTrustedFeed([]string{srv.URL}, srv.Client(), 5*time.Minute)
	f.Poll(context.Background())
	f.Poll(context.Background())
	assert.Equal(t, int32(2), hits.Load())
	assert.True(t, sawIfNoneMatch.Load())
	_, ok := f.Lookup(TripKey{"T1", "20260902"}, now)
	assert.True(t, ok, "304 keeps the previous entities")
	assert.Equal(t, 1, f.Health()[0].Entities)
}

func TestTrustedFeed_ErrorFeedDoesNotPoisonHealthyOne(t *testing.T) {
	now := time.Now()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(feedBytes(t, vpEntity("1", "T1", "20260902", 47.60, -122.33, now)))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()

	f := NewTrustedFeed([]string{bad.URL, good.URL}, http.DefaultClient, 5*time.Minute)
	f.Poll(context.Background())
	_, ok := f.Lookup(TripKey{"T1", "20260902"}, now)
	assert.True(t, ok)
	h := f.Health()
	require.Len(t, h, 2)
	assert.Contains(t, h[0].LastError, "503")
	assert.True(t, h[0].LastSuccess.IsZero())
	assert.Empty(t, h[1].LastError)

	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not protobuf at all, definitely not"))
	}))
	defer garbage.Close()
	g := NewTrustedFeed([]string{garbage.URL}, http.DefaultClient, 5*time.Minute)
	g.Poll(context.Background())
	assert.NotEmpty(t, g.Health()[0].LastError)
}

func TestTrustedFeed_Unconfigured(t *testing.T) {
	f := NewTrustedFeed(nil, http.DefaultClient, time.Minute)
	assert.False(t, f.Configured())
	_, ok := f.Lookup(TripKey{"T1", "x"}, time.Now())
	assert.False(t, ok)
	assert.Empty(t, f.Health())
	f.Poll(context.Background()) // must not panic
}

func TestTrustedFeed_StartPollsUntilCancelled(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(feedBytes(t))
	}))
	defer srv.Close()
	f := NewTrustedFeed([]string{srv.URL}, srv.Client(), time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Start(ctx, 20*time.Millisecond); close(done) }()
	assert.Eventually(t, func() bool { return hits.Load() >= 3 }, 2*time.Second, 10*time.Millisecond)
	cancel()
	<-done
}
