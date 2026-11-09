package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPIKeyStore is the read path requireAPIKey depends on. It recognizes at
// most one key and records the last_used_at writes it receives.
type fakeAPIKeyStore struct {
	key           *APIKey
	getErr        error
	lastUsedErr   error
	lastUsedCalls int
	lastUsedID    int64
}

func (f *fakeAPIKeyStore) GetAPIKeyByHash(_ context.Context, keyHash string) (*APIKey, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.key == nil || f.key.KeyHash != keyHash {
		return nil, ErrAPIKeyNotFound
	}
	return f.key, nil
}

func (f *fakeAPIKeyStore) UpdateAPIKeyLastUsed(_ context.Context, id int64) error {
	f.lastUsedCalls++
	f.lastUsedID = id
	return f.lastUsedErr
}

// storeWithKey returns a fake holding one key with the given raw value.
func storeWithKey(rawKey string, active bool) *fakeAPIKeyStore {
	return &fakeAPIKeyStore{key: &APIKey{ID: 7, Name: "consumer", KeyHash: hashAPIKey(rawKey), Active: active}}
}

// serveFeedAuth runs a request through requireAPIKey and reports whether the
// wrapped handler ran.
func serveFeedAuth(store APIKeyStore, req *http.Request) (*httptest.ResponseRecorder, bool) {
	return serveFeedAuthTrusting(store, req, false)
}

func serveFeedAuthTrusting(store APIKeyStore, req *http.Request, trustProxy bool) (*httptest.ResponseRecorder, bool) {
	reached := false
	handler := requireAPIKey(store, trustProxy)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w, reached
}

func TestHashAPIKey(t *testing.T) {
	// Published SHA-256 vectors: the hash must stay interoperable with any
	// key hashed by an earlier release.
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", hashAPIKey("abc"))
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hashAPIKey(""))
	assert.Equal(t, hashAPIKey("repeat-me"), hashAPIKey("repeat-me"), "hashing must be deterministic")
	assert.NotEqual(t, hashAPIKey("key-a"), hashAPIKey("key-b"))
}

func TestGenerateAPIKey(t *testing.T) {
	first, err := generateAPIKey()
	require.NoError(t, err)
	second, err := generateAPIKey()
	require.NoError(t, err)

	assert.Len(t, first, 64, "32 random bytes hex-encoded")
	raw, err := hex.DecodeString(first)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
	assert.NotEqual(t, first, second, "keys must not repeat")
}

func TestRequireAPIKey_Denials(t *testing.T) {
	tests := []struct {
		name       string
		store      *fakeAPIKeyStore
		header     string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing header",
			store:      storeWithKey("valid-key", true),
			header:     "",
			wantStatus: http.StatusUnauthorized,
			wantError:  "missing API key",
		},
		{
			name:       "unknown key",
			store:      storeWithKey("valid-key", true),
			header:     "some-other-key",
			wantStatus: http.StatusUnauthorized,
			wantError:  "invalid API key",
		},
		{
			name:       "revoked key",
			store:      storeWithKey("valid-key", false),
			header:     "valid-key",
			wantStatus: http.StatusUnauthorized,
			wantError:  "inactive API key",
		},
		{
			name:       "store failure",
			store:      &fakeAPIKeyStore{getErr: errors.New("connection refused")},
			header:     "valid-key",
			wantStatus: http.StatusInternalServerError,
			wantError:  "internal server error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
			if tc.header != "" {
				req.Header.Set(apiKeyHeader, tc.header)
			}

			w, reached := serveFeedAuth(tc.store, req)

			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantError, decodeError(t, w))
			assert.False(t, reached, "the feed handler must not run for a denied request")
			assert.Zero(t, tc.store.lastUsedCalls, "a denied request must not stamp last_used_at")
		})
	}
}

func TestRequireAPIKey_ValidKey(t *testing.T) {
	store := storeWithKey("valid-key", true)
	req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set(apiKeyHeader, "valid-key")

	w, reached := serveFeedAuth(store, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, reached, "a valid key must reach the feed handler")
	assert.Equal(t, 1, store.lastUsedCalls)
	assert.Equal(t, int64(7), store.lastUsedID)
}

// TestRequireAPIKey_UpdateLastUsedFailure pins that a failed last_used_at
// write is logged and ignored: it is an audit convenience, and failing it
// must not deny a consumer whose key already validated. The failed write
// still uses up the key's minute, so a broken write path costs one write a
// minute rather than one a request.
func TestRequireAPIKey_UpdateLastUsedFailure(t *testing.T) {
	store := storeWithKey("valid-key", true)
	store.lastUsedErr = errors.New("write failed")
	start := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	clock := start
	serve := serveWithStampClock(store, &clock)

	assert.True(t, serve("valid-key"), "the feed must still be served when last_used_at cannot be written")
	require.Equal(t, 1, store.lastUsedCalls)

	clock = start.Add(30 * time.Second)
	assert.True(t, serve("valid-key"))
	assert.Equal(t, 1, store.lastUsedCalls, "a failed write is not retried within the minute")

	clock = start.Add(time.Minute)
	assert.True(t, serve("valid-key"))
	assert.Equal(t, 2, store.lastUsedCalls, "it is retried once the minute is up")
}

// serveWithStampClock returns a function that sends one feed request with the
// given key through a single requireAPIKey middleware whose last_used_at
// throttle reads the time from clock. It reports whether the feed handler
// ran, and is safe to call from several goroutines at once.
func serveWithStampClock(store APIKeyStore, clock *time.Time) func(key string) bool {
	stamped := newStampedAPIKeys(apiKeyStampInterval)
	stamped.now = func() time.Time { return *clock }
	handler := requireAPIKeyWith(store, false, newValidatedAPIKeys(apiKeyGrace), stamped)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	return func(key string) bool {
		req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
		req.Header.Set(apiKeyHeader, key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code == http.StatusNoContent
	}
}

// TestRequireAPIKey_StampsLastUsedOncePerMinute: a consumer polling every few
// seconds used to write the same row on every request. It now writes once a
// minute, and each key keeps its own minute.
func TestRequireAPIKey_StampsLastUsedOncePerMinute(t *testing.T) {
	store := storeWithKey("key-a", true)
	keyA := store.key
	start := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	clock := start
	serve := serveWithStampClock(store, &clock)

	for i := range 10 {
		clock = start.Add(time.Duration(i) * 5 * time.Second)
		require.True(t, serve("key-a"), "every request in the burst is served")
	}
	assert.Equal(t, 1, store.lastUsedCalls, "a burst of requests writes once")

	clock = start.Add(59 * time.Second)
	require.True(t, serve("key-a"))
	assert.Equal(t, 1, store.lastUsedCalls, "59s after the last write is still inside the minute")

	clock = start.Add(time.Minute)
	require.True(t, serve("key-a"))
	assert.Equal(t, 2, store.lastUsedCalls, "a request a minute after the last write writes again")

	store.key = &APIKey{ID: 8, Name: "other-consumer", KeyHash: hashAPIKey("key-b"), Active: true}
	require.True(t, serve("key-b"))
	assert.Equal(t, 3, store.lastUsedCalls, "another key is not held back by key-a's minute")
	assert.Equal(t, int64(8), store.lastUsedID)

	store.key = keyA
	require.True(t, serve("key-a"))
	assert.Equal(t, 3, store.lastUsedCalls, "nor does key-b's write reset key-a's minute")
}

// stallingStampStore holds its first last_used_at write until release is
// closed, so a test can send more requests while that write is in flight.
type stallingStampStore struct {
	*fakeAPIKeyStore
	entered chan struct{}
	release chan struct{}
}

func (s *stallingStampStore) UpdateAPIKeyLastUsed(ctx context.Context, id int64) error {
	err := s.fakeAPIKeyStore.UpdateAPIKeyLastUsed(ctx, id)
	if s.lastUsedCalls == 1 {
		close(s.entered)
		<-s.release
	}
	return err
}

// TestRequireAPIKey_StampInFlightIsNotRepeated: the write is claimed before it
// starts, so requests that arrive while it is still running are served
// without writing too.
func TestRequireAPIKey_StampInFlightIsNotRepeated(t *testing.T) {
	store := &stallingStampStore{
		fakeAPIKeyStore: storeWithKey("key-a", true),
		entered:         make(chan struct{}),
		release:         make(chan struct{}),
	}
	clock := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	serve := serveWithStampClock(store, &clock)

	firstServed := make(chan bool)
	go func() { firstServed <- serve("key-a") }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first request never wrote last_used_at")
	}

	for range 5 {
		assert.True(t, serve("key-a"), "a request during the write is served")
	}
	close(store.release)

	assert.True(t, <-firstServed, "the request that wrote is served too")
	assert.Equal(t, 1, store.lastUsedCalls)
}

// TestRequireAPIKey_RawKeyNeverLogged guards against a denial log leaking the
// credential a client presented.
// Not safe for t.Parallel(); uses global logger
func TestRequireAPIKey_RawKeyNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	const rawKey = "sup3r-secret-raw-key"
	req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set(apiKeyHeader, rawKey)

	w, _ := serveFeedAuth(storeWithKey("a-different-key", true), req)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	logged := buf.String()
	require.NotEmpty(t, logged, "the denial must be logged")
	assert.NotContains(t, logged, rawKey, "the raw key must never reach the logs")
	assert.NotContains(t, logged, hashAPIKey(rawKey), "the key hash must not reach the logs either")
}

// TestRequireAPIKey_LogsForwardedClientIP checks that denial logs name the
// real consumer behind a reverse proxy rather than the proxy itself, which is
// how an operator finds who is hammering the feed.
// Not safe for t.Parallel(); uses global logger
func TestRequireAPIKey_LogsForwardedClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		wantIP     string
	}{
		{"proxy trusted", true, "203.0.113.9"},
		{"proxy untrusted", false, "192.0.2.1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			original := slog.Default()
			t.Cleanup(func() { slog.SetDefault(original) })
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

			req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
			req.Header.Set("X-Forwarded-For", "203.0.113.9")

			w, _ := serveFeedAuthTrusting(&fakeAPIKeyStore{}, req, tc.trustProxy)
			require.Equal(t, http.StatusUnauthorized, w.Code)

			var entry map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
			assert.Equal(t, tc.wantIP, entry["client_ip"])
		})
	}
}

// TestRequireAPIKey_ServesRecentKeyThroughDatabaseOutage is the outage the
// grace period exists for: the positions are in memory, so a consumer whose
// key validated a moment ago keeps getting the feed while Postgres is down.
func TestRequireAPIKey_ServesRecentKeyThroughDatabaseOutage(t *testing.T) {
	store := storeWithKey("good-key", true)
	validated := newValidatedAPIKeys(time.Minute)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	validated.now = func() time.Time { return clock }
	reached := false
	handler := requireAPIKeyWith(store, false, validated, newStampedAPIKeys(apiKeyStampInterval))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}))
	serve := func(key string) int {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
		req.Header.Set(apiKeyHeader, key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, http.StatusOK, serve("good-key"))

	store.getErr = errors.New("dial tcp: connection refused")
	clock = clock.Add(30 * time.Second)
	assert.Equal(t, http.StatusOK, serve("good-key"), "validated 30s ago")
	assert.True(t, reached)
	assert.Equal(t, http.StatusInternalServerError, serve("never-seen-key"), "a key never validated is not let through")
	assert.False(t, reached)

	clock = clock.Add(time.Minute)
	assert.Equal(t, http.StatusInternalServerError, serve("good-key"), "past the grace period")
}

// TestRequireAPIKey_RevokedKeyIsNotGracedThroughOutage: once the database
// says a key is inactive, an outage straight after must not bring it back.
func TestRequireAPIKey_RevokedKeyIsNotGracedThroughOutage(t *testing.T) {
	store := storeWithKey("good-key", true)
	validated := newValidatedAPIKeys(time.Hour)
	handler := requireAPIKeyWith(store, false, validated, newStampedAPIKeys(apiKeyStampInterval))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	serve := func() int {
		req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
		req.Header.Set(apiKeyHeader, "good-key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, http.StatusOK, serve())
	store.key.Active = false
	require.Equal(t, http.StatusUnauthorized, serve())
	store.getErr = errors.New("dial tcp: connection refused")
	assert.Equal(t, http.StatusInternalServerError, serve())
}
