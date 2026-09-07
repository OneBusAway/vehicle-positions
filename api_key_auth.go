package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// apiKeyHeader carries the raw feed API key.
const apiKeyHeader = "X-API-Key"

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
// denial logs attribute a request to, matching the rest of the server.
func requireAPIKey(store APIKeyStore, trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get(apiKeyHeader)
			if rawKey == "" {
				slog.Warn("feed access denied: missing api key", "path", r.URL.Path, "client_ip", clientIP(r, trustProxy))
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing API key"})
				return
			}

			apiKey, err := store.GetAPIKeyByHash(r.Context(), hashAPIKey(rawKey))
			if err != nil {
				if errors.Is(err, ErrAPIKeyNotFound) {
					slog.Warn("feed access denied: invalid api key", "path", r.URL.Path, "client_ip", clientIP(r, trustProxy))
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
					return
				}
				slog.Error("failed to look up api key", "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}

			if !apiKey.Active {
				slog.Warn("feed access denied: inactive api key", "api_key_id", apiKey.ID, "path", r.URL.Path, "client_ip", clientIP(r, trustProxy))
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "inactive API key"})
				return
			}

			// last_used_at is an audit convenience, not a security control:
			// failing to record it must not deny a consumer whose key already
			// validated, on the system's hottest endpoint.
			if err := store.UpdateAPIKeyLastUsed(r.Context(), apiKey.ID); err != nil {
				slog.Error("failed to update api key last_used_at", "api_key_id", apiKey.ID, "error", err)
			}

			next.ServeHTTP(w, r)
		})
	}
}
