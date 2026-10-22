package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGTFSRuntime_LoadsIndex(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := newGTFSRuntime(ctx, "rider/testdata/fixture.zip", time.Hour, nil)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, 4, rt.Index().Stats().Trips, "the shapeless T4 is indexed too")
	assert.Equal(t, 2, rt.Index().Stats().Routes)
	assert.Same(t, rt.refresher.Current(), rt.Index())
}

func TestNewGTFSRuntime_FailsWhenTheFeedIsMissing(t *testing.T) {
	_, err := newGTFSRuntime(context.Background(), "does/not/exist.zip", time.Hour, nil)
	assert.Error(t, err)
}

func TestNewGTFSRuntime_StopReturns(t *testing.T) {
	rt, err := newGTFSRuntime(context.Background(), "rider/testdata/fixture.zip", time.Hour, nil)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { rt.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

// fakeGTFSCache is an in-memory gtfsFeedCache.
type fakeGTFSCache struct {
	feed  *CachedGTFSFeed
	saves int
}

func (c *fakeGTFSCache) LoadGTFSFeed(context.Context) (*CachedGTFSFeed, error) {
	if c.feed == nil {
		return nil, ErrNoCachedGTFSFeed
	}
	return c.feed, nil
}

func (c *fakeGTFSCache) SaveGTFSFeed(_ context.Context, source string, body []byte) error {
	c.saves++
	c.feed = &CachedGTFSFeed{Source: source, Body: body, FetchedAt: time.Now()}
	return nil
}

func TestNewGTFSRuntime_CachesTheFeedItLoads(t *testing.T) {
	cache := &fakeGTFSCache{}
	rt, err := newGTFSRuntime(context.Background(), "rider/testdata/fixture.zip", time.Hour, cache)
	require.NoError(t, err)
	defer rt.Stop()

	want, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	require.NotNil(t, cache.feed)
	assert.Equal(t, "rider/testdata/fixture.zip", cache.feed.Source)
	assert.Equal(t, want, cache.feed.Body)
}

// TestNewGTFSRuntime_StartsOnCacheWhenTheFeedIsDown is the outage the cache
// exists for: the GTFS host fails at startup, and the server starts on the
// last copy it loaded rather than exiting.
func TestNewGTFSRuntime_StartsOnCacheWhenTheFeedIsDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer down.Close()
	source := down.URL + "/gtfs.zip"
	body, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	cachedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cache := &fakeGTFSCache{feed: &CachedGTFSFeed{Source: source, Body: body, FetchedAt: cachedAt}}

	rt, err := newGTFSRuntime(context.Background(), source, time.Hour, cache)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, 4, rt.Index().Stats().Trips)
	assert.Equal(t, cachedAt, rt.Index().Stats().LoadedAt, "the index says how old the schedule is")
	assert.Zero(t, cache.saves, "a failed download must not overwrite the cache")
}

func TestNewGTFSRuntime_IgnoresCacheOfAnotherSource(t *testing.T) {
	body, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	cache := &fakeGTFSCache{feed: &CachedGTFSFeed{Source: "https://old.example/gtfs.zip", Body: body}}

	_, err = newGTFSRuntime(context.Background(), "does/not/exist.zip", time.Hour, cache)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the cached copy is of https://old.example/gtfs.zip")
}

func TestNewGTFSRuntime_FailsWithNoCachedCopy(t *testing.T) {
	_, err := newGTFSRuntime(context.Background(), "does/not/exist.zip", time.Hour, &fakeGTFSCache{})
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// zipWithoutStopTimes returns the fixture feed with every stop time removed,
// so it parses but has no trip a driver could pick.
func zipWithoutStopTimes(t *testing.T) []byte {
	t.Helper()
	zr, err := zip.OpenReader("rider/testdata/fixture.zip")
	require.NoError(t, err)
	defer zr.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		require.NoError(t, err)
		if f.Name == "stop_times.txt" {
			_, err = io.WriteString(w, "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n")
			require.NoError(t, err)
			continue
		}
		r, err := f.Open()
		require.NoError(t, err)
		_, err = io.Copy(w, r)
		r.Close()
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestNewGTFSRuntime_RefusesFeedWithNoUsableTrips: a refresh that downloads a
// feed with no usable trip keeps the schedule in force and leaves the cached
// copy alone, so a later outage still falls back on a good one.
func TestNewGTFSRuntime_RefusesFeedWithNoUsableTrips(t *testing.T) {
	good, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	var empty atomic.Bool
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if empty.Load() {
			_, _ = w.Write(zipWithoutStopTimes(t))
			return
		}
		_, _ = w.Write(good)
	}))
	defer feed.Close()
	cache := &fakeGTFSCache{}

	rt, err := newGTFSRuntime(context.Background(), feed.URL+"/gtfs.zip", time.Hour, cache)
	require.NoError(t, err)
	defer rt.Stop()
	first := rt.Index()
	require.Equal(t, 1, cache.saves)

	empty.Store(true)
	err = rt.refresher.RefreshNow(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no usable trips")
	assert.Same(t, first, rt.Index(), "the schedule in force stays")
	assert.Equal(t, 1, cache.saves, "the good cached copy is not overwritten")
	assert.Equal(t, good, cache.feed.Body)
}
