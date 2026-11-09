package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// apiKeyHeader carries the raw feed API key.
const apiKeyHeader = "X-API-Key"

// apiKeyGrace is how long after a key last validated against the database it
// is still accepted while the database cannot be reached. The positions the
// feed serves are in memory, so a short database outage need not take the
// public feed down with it.
const apiKeyGrace = 15 * time.Minute

// apiKeyLookupTimeout bounds the key lookup. A database that hangs rather
// than refusing connections would otherwise hold every feed request until
// the consumer gave up, and the grace period would never get its chance.
const apiKeyLookupTimeout = 2 * time.Second

// apiKeyStampInterval is how often, at most, a key's last_used_at is written.
// Consumers poll the feed every few seconds, and an UPDATE on the same row for
// every request is what shows up first under real poll rates. An operator
// auditing a key needs to know it is in use, not the second it was last used.
const apiKeyStampInterval = time.Minute

// validatedAPIKeys remembers, by hash, when each key last validated. It is
// consulted only when the database lookup fails: while the database answers,
// every request is checked against it, so a revoked key is refused at once.
// Only keys that validated are recorded, so it holds at most one entry per
// active key.
type validatedAPIKeys struct {
	mu    sync.Mutex
	at    map[string]time.Time
	grace time.Duration
	now   func() time.Time
}

func newValidatedAPIKeys(grace time.Duration) *validatedAPIKeys {
	return &validatedAPIKeys{at: make(map[string]time.Time), grace: grace, now: time.Now}
}

func (v *validatedAPIKeys) record(hash string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.at[hash] = v.now()
}

// forget drops a key the database has said is unknown or inactive.
func (v *validatedAPIKeys) forget(hash string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.at, hash)
}

// recent reports whether the key validated within the grace period.
func (v *validatedAPIKeys) recent(hash string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	at, ok := v.at[hash]
	return ok && v.now().Sub(at) < v.grace
}

// stampedAPIKeys remembers, by key ID, when each key's last_used_at write was
// last attempted, so it is written at most once per interval. It holds at most
// one entry per key that has validated.
type stampedAPIKeys struct {
	mu       sync.Mutex
	at       map[int64]time.Time
	interval time.Duration
	now      func() time.Time
}

func newStampedAPIKeys(interval time.Duration) *stampedAPIKeys {
	return &stampedAPIKeys{at: make(map[int64]time.Time), interval: interval, now: time.Now}
}

// claim reports whether the key's last_used_at is due a write and, if it is,
// records the attempt before returning, so concurrent requests for the same
// key do not also write. A failed write is not retried until the interval
// has passed: retrying on every request would bring back the write load this
// exists to remove.
func (s *stampedAPIKeys) claim(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if at, ok := s.at[id]; ok && now.Sub(at) < s.interval {
		return false
	}
	s.at[id] = now
	return true
}

// hashAPIKey returns the hex-encoded SHA-256 of a raw key. Keys are
// high-entropy values from crypto/rand, not user-chosen passwords: bcrypt's
// work factor exists to slow brute force against low-entropy secrets and
// would only add latency to the feed, the hottest endpoint in the system.
// Because lookup is by hash there is no secret-dependent comparison, so the
// timing side-channel that handleLogin guards against does not arise here.
func hashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// generateAPIKey returns a new 256-bit random key, hex-encoded.
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// requireAPIKey is middleware that gates the GTFS-RT feed on a valid, active
// API key sent in the X-API-Key header. trustProxy selects which address the
// denial logs attribute a request to, matching the rest of the server. When
// the key lookup fails, a key that validated within apiKeyGrace is still
// served. Every request is checked against the database, but last_used_at is
// written at most once per apiKeyStampInterval per key.
func requireAPIKey(store APIKeyStore, trustProxy bool) func(http.Handler) http.Handler {
	return requireAPIKeyWith(store, trustProxy, newValidatedAPIKeys(apiKeyGrace), newStampedAPIKeys(apiKeyStampInterval))
}

func requireAPIKeyWith(store APIKeyStore, trustProxy bool, validated *validatedAPIKeys, stamped *stampedAPIKeys) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get(apiKeyHeader)
			if rawKey == "" {
				slog.Warn("feed access denied: missing api key", "path", r.URL.Path, "client_ip", clientIP(r, trustProxy))
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing API key"})
				return
			}

			hash := hashAPIKey(rawKey)
			lookupCtx, cancel := context.WithTimeout(r.Context(), apiKeyLookupTimeout)
			apiKey, err := store.GetAPIKeyByHash(lookupCtx, hash)
			cancel()
			if err != nil {
				if errors.Is(err, ErrAPIKeyNotFound) {
					validated.forget(hash)
					slog.Warn("feed access denied: invalid api key", "path", r.URL.Path, "client_ip", clientIP(r, trustProxy))
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
					return
				}
				if validated.recent(hash) {
					slog.Warn("api key lookup failed, serving a key that validated recently", "error", err)
					next.ServeHTTP(w, r)
					return
				}
				slog.Error("failed to look up api key", "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}

			if !apiKey.Active {
				validated.forget(hash)
				slog.Warn("feed access denied: inactive api key", "api_key_id", apiKey.ID, "path", r.URL.Path, "client_ip", clientIP(r, trustProxy))
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "inactive API key"})
				return
			}

			validated.record(hash)

			// last_used_at is an audit convenience, not a security control:
			// failing to record it must not deny a consumer whose key already
			// validated, on the system's hottest endpoint.
			if stamped.claim(apiKey.ID) {
				updateCtx, cancel := context.WithTimeout(r.Context(), apiKeyLookupTimeout)
				if err := store.UpdateAPIKeyLastUsed(updateCtx, apiKey.ID); err != nil {
					slog.Error("failed to update api key last_used_at", "api_key_id", apiKey.ID, "error", err)
				}
				cancel()
			}

			next.ServeHTTP(w, r)
		})
	}
}
