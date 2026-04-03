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

	"github.com/golang-jwt/jwt/v5"
)

// userIDFromClaims extracts the user ID from JWT claims on the request context.
func userIDFromClaims(r *http.Request) (int64, error, int) {
	claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
	if !ok {
		return 0, errors.New("internal server error"), http.StatusInternalServerError
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return 0, errors.New("invalid token: missing subject"), http.StatusUnauthorized
	}
	userID, err := strconv.ParseInt(sub, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid token: invalid subject"), http.StatusUnauthorized
	}
	return userID, nil, 0
}

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

		userID, claimsErr, status := userIDFromClaims(r)
		if claimsErr != nil {
			slog.Warn("handleStartTrip: invalid claims", "error", claimsErr)
			writeJSON(w, status, map[string]string{"error": claimsErr.Error()})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req StartTripRequest
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
		if len(req.RouteID) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "route_id must be at most 100 characters"})
			return
		}
		if len(req.GtfsTripID) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gtfs_trip_id must be at most 100 characters"})
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

		userID, claimsErr, status := userIDFromClaims(r)
		if claimsErr != nil {
			slog.Warn("handleEndTrip: invalid claims", "error", claimsErr)
			writeJSON(w, status, map[string]string{"error": claimsErr.Error()})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req EndTripRequest
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

		if req.TripID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id is required and must be positive"})
			return
		}

		err = store.EndTrip(r.Context(), req.TripID, userID)
		if err != nil {
			if errors.Is(err, ErrActiveTripNotFound) {
				slog.Warn("end trip: no matching active trip", "trip_id", req.TripID, "user_id", userID)
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
