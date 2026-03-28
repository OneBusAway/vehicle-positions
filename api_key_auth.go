package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
)

type APIKeyStore interface {
	GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error)
	UpdateAPIKeyLastUsed(ctx context.Context, id int64) error
}

// hashAPIKey hashes the raw API key using SHA-256 and returns the hex-encoded string.
func hashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// requireAPIKey is middleware that checks for a valid API key in the X-API-Key header.
func requireAPIKey(store APIKeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get("X-API-Key")
			if rawKey == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing API key"})
				return
			}

			keyHash := hashAPIKey(rawKey)

			apiKey, err := store.GetAPIKeyByHash(r.Context(), keyHash)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
					return
				}
				slog.Error("failed to fetch api key", "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}

			if !apiKey.Active {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "inactive API key"})
				return
			}

			if err := store.UpdateAPIKeyLastUsed(r.Context(), apiKey.ID); err != nil {
				slog.Error("failed to update api key last_used_at", "api_key_id", apiKey.ID, "error", err)
			}

			next.ServeHTTP(w, r)
		})
	}
}
