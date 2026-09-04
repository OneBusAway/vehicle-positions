package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

func (r *createAPIKeyRequest) validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if len(r.Name) > maxFieldLength {
		return fmt.Errorf("name must be at most %d characters", maxFieldLength)
	}
	return nil
}

// createAPIKeyResponse is the only response carrying the raw key. It is shown
// once here and cannot be recovered later, since only its hash is stored.
type createAPIKeyResponse struct {
	APIKey
	Key string `json:"key"`
}

func handleListAPIKeys(store APIKeyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keys, err := store.ListAPIKeys(r.Context())
		if err != nil {
			slog.Error("failed to list api keys", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, keys)
	}
}

func handleCreateAPIKey(store APIKeyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10) // 1KB

		var req createAPIKeyRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + sanitizeJSONError(err)})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + sanitizeJSONError(err)})
			return
		}

		if err := req.validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		rawKey, err := generateAPIKey()
		if err != nil {
			slog.Error("failed to generate api key", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		key, err := store.CreateAPIKey(r.Context(), req.Name, hashAPIKey(rawKey))
		if err != nil {
			slog.Error("failed to create api key", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		slog.Info("api key created", "api_key_id", key.ID, "name", key.Name)
		writeJSON(w, http.StatusCreated, createAPIKeyResponse{APIKey: *key, Key: rawKey})
	}
}

func handleDeactivateAPIKey(store APIKeyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid api key id"})
			return
		}

		if err := store.DeactivateAPIKey(r.Context(), id); err != nil {
			if errors.Is(err, ErrAPIKeyNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
				return
			}
			slog.Error("failed to deactivate api key", "api_key_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		slog.Info("api key revoked", "api_key_id", id)
		w.WriteHeader(http.StatusNoContent)
	}
}
