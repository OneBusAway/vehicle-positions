package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxFieldLength = 255

// validateVehicleID checks that a vehicle ID meets format and length requirements.
func validateVehicleID(id string) error {
	if id == "" {
		return errors.New("vehicle id is required")
	}
	if len(id) > maxVehicleIDLength {
		return fmt.Errorf("vehicle id must be at most %d characters", maxVehicleIDLength)
	}
	if !vehicleIDPattern.MatchString(id) {
		return errors.New("vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores")
	}
	return nil
}

type upsertVehicleRequest struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	AgencyTag string `json:"agency_tag"`
}

func (r *upsertVehicleRequest) validate() error {
	if err := validateVehicleID(r.ID); err != nil {
		return err
	}
	if len(r.Label) > maxFieldLength {
		return fmt.Errorf("label must be at most %d characters", maxFieldLength)
	}
	if len(r.AgencyTag) > maxFieldLength {
		return fmt.Errorf("agency_tag must be at most %d characters", maxFieldLength)
	}
	return nil
}

func handleListVehicles(store VehicleManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vehicles, err := store.ListVehicles(r.Context())
		if err != nil {
			slog.Error("failed to list vehicles", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list vehicles"})
			return
		}
		writeJSON(w, http.StatusOK, vehicles)
	}
}

func handleGetVehicle(store VehicleManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := validateVehicleID(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		vehicle, err := store.GetVehicle(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle not found"})
				return
			}
			slog.Error("failed to get vehicle", "vehicle_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get vehicle"})
			return
		}
		writeJSON(w, http.StatusOK, vehicle)
	}
}

func handleUpsertVehicle(store VehicleManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10) // 1KB

		var req upsertVehicleRequest
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

		vehicle, err := store.UpsertVehicle(r.Context(), req.ID, req.Label, req.AgencyTag)
		if err != nil {
			slog.Error("failed to upsert vehicle", "vehicle_id", req.ID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save vehicle"})
			return
		}
		writeJSON(w, http.StatusOK, vehicle)
	}
}

func handleDeactivateVehicle(store VehicleManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := validateVehicleID(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		err := store.DeactivateVehicle(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle not found"})
				return
			}
			slog.Error("failed to deactivate vehicle", "vehicle_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to deactivate vehicle"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// sanitizeJSONError strips internal Go struct names from JSON decoder error messages.
func sanitizeJSONError(err error) string {
	var unmarshalErr *json.UnmarshalTypeError
	if errors.As(err, &unmarshalErr) {
		return fmt.Sprintf("field %q has invalid type", unmarshalErr.Field)
	}
	return err.Error()
}
