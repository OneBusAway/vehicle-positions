package rider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadStatic_FileAndHTTP(t *testing.T) {
	zipBytes := buildFixtureZip(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "gtfs.zip")
	require.NoError(t, os.WriteFile(path, zipBytes, 0o644))

	static, err := LoadStatic(context.Background(), path, http.DefaultClient)
	require.NoError(t, err)
	assert.Len(t, static.Trips, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) }))
	defer srv.Close()
	static, err = LoadStatic(context.Background(), srv.URL+"/gtfs.zip", srv.Client())
	require.NoError(t, err)
	assert.Len(t, static.Trips, 4)

	_, err = LoadStatic(context.Background(), filepath.Join(dir, "missing.zip"), http.DefaultClient)
	assert.Error(t, err)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	_, err = LoadStatic(context.Background(), bad.URL, bad.Client())
	assert.Error(t, err)
}

func TestLoadIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gtfs.zip")
	require.NoError(t, os.WriteFile(path, buildFixtureZip(t), 0o644))

	ix, err := LoadIndex(context.Background(), path, http.DefaultClient, fixtureLoadedAt)
	require.NoError(t, err)
	assert.Equal(t, []string{"T1", "T2", "T3"}, ix.TripIDs())
	assert.Equal(t, path, ix.Stats().Source)

	_, err = LoadIndex(context.Background(), filepath.Join(t.TempDir(), "missing.zip"), http.DefaultClient, fixtureLoadedAt)
	assert.Error(t, err)
}

func TestRefresher_SwapsOnSuccessKeepsOnFailure(t *testing.T) {
	first := fixtureIndex(t)
	var calls atomic.Int32
	loader := func(ctx context.Context) (*Index, error) {
		n := calls.Add(1)
		if n == 1 {
			return nil, errors.New("boom")
		}
		return BuildIndex(fixtureStatic(t, fixtureTimezone), "second", time.Now())
	}
	r := NewRefresher(first, loader)
	assert.Same(t, first, r.Current())
	assert.Error(t, r.RefreshNow(context.Background()))
	assert.Same(t, first, r.Current(), "failed refresh keeps the old index")
	require.NoError(t, r.RefreshNow(context.Background()))
	assert.Equal(t, "second", r.Current().Stats().Source)

	// Old TripInfo pointers stay valid for rides in progress.
	trip, ok := first.Trip("T1")
	require.True(t, ok)
	assert.Equal(t, "T1", trip.ID)
}

func TestRefresher_StartRefreshesUntilContextDone(t *testing.T) {
	first := fixtureIndex(t)
	refreshed := make(chan struct{}, 1)
	r := NewRefresher(first, func(ctx context.Context) (*Index, error) {
		select {
		case refreshed <- struct{}{}:
		default:
		}
		return BuildIndex(fixtureStatic(t, fixtureTimezone), "ticked", fixtureLoadedAt)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Start(ctx, time.Millisecond)
	}()

	<-refreshed
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after the context was cancelled")
	}
	assert.Equal(t, "ticked", r.Current().Stats().Source)
}
