package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// StartTripRequest is the JSON payload for POST /api/v1/trips/start.
type StartTripRequest struct {
	VehicleID  string `json:"vehicle_id"`
	RouteID    string `json:"route_id"`
	GtfsTripID string `json:"gtfs_trip_id"`
}

// EndTripRequest is the JSON payload for POST /api/v1/trips/end.
type EndTripRequest struct {
	TripID int64 `json:"trip_id"`
}

func handleStartTrip(store TripStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		if !ok {
			slog.Warn("handleStartTrip: JWT claims missing from context")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		sub, ok := claims["sub"].(string)
		if !ok || sub == "" {
			slog.Warn("handleStartTrip: JWT sub claim missing or not a string")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token: missing subject"})
			return
		}
		userID, err := strconv.ParseInt(sub, 10, 64)
		if err != nil {
			slog.Warn("handleStartTrip: JWT sub claim is not a valid integer", "sub", sub)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token: invalid subject"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req StartTripRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain a single JSON object"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.VehicleID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id is required"})
			return
		}
		if len(req.VehicleID) > maxVehicleIDLength {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id must be at most 50 characters"})
			return
		}
		if !vehicleIDPattern.MatchString(req.VehicleID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id must contain only alphanumeric characters, dots, hyphens, and underscores"})
			return
		}

		trip, err := store.StartTrip(r.Context(), userID, req.VehicleID, req.RouteID, req.GtfsTripID)
		if err != nil {
			if errors.Is(err, ErrNotAssigned) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "driver is not assigned to this vehicle"})
				return
			}
			if errors.Is(err, ErrActiveTripExists) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "driver already has an active trip"})
				return
			}
			slog.Error("failed to start trip", "user_id", userID, "vehicle_id", req.VehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to start trip"})
			return
		}

		writeJSON(w, http.StatusCreated, trip)
	}
}

func handleEndTrip(store TripEnder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		if !ok {
			slog.Warn("handleEndTrip: JWT claims missing from context")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		sub, ok := claims["sub"].(string)
		if !ok || sub == "" {
			slog.Warn("handleEndTrip: JWT sub claim missing or not a string")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token: missing subject"})
			return
		}
		userID, err := strconv.ParseInt(sub, 10, 64)
		if err != nil {
			slog.Warn("handleEndTrip: JWT sub claim is not a valid integer", "sub", sub)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token: invalid subject"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req EndTripRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain a single JSON object"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.TripID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id is required and must be positive"})
			return
		}

		err = store.EndTrip(r.Context(), req.TripID, userID)
		if err != nil {
			if errors.Is(err, ErrTripNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "active trip not found"})
				return
			}
			slog.Error("failed to end trip", "user_id", userID, "trip_id", req.TripID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to end trip"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "trip ended"})
	}
}
