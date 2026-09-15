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
// must not deny a consumer whose key already validated.
func TestRequireAPIKey_UpdateLastUsedFailure(t *testing.T) {
	store := storeWithKey("valid-key", true)
	store.lastUsedErr = errors.New("write failed")

	req := httptest.NewRequest(http.MethodGet, "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set(apiKeyHeader, "valid-key")

	w, reached := serveFeedAuth(store, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, reached, "the feed must still be served when last_used_at cannot be written")
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
