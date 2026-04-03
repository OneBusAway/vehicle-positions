package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

func handleListTrips(lister TripLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse optional status filter.
		var status *string
		if s := r.URL.Query().Get("status"); s != "" {
			if s != "active" && s != "completed" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be 'active' or 'completed'"})
				return
			}
			status = &s
		}

		// Parse optional vehicle_id filter.
		var vehicleID *string
		if v := r.URL.Query().Get("vehicle_id"); v != "" {
			if len(v) > maxVehicleIDLength {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id must be at most 50 characters"})
				return
			}
			if !vehicleIDPattern.MatchString(v) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id must contain only alphanumeric characters, dots, hyphens, and underscores"})
				return
			}
			vehicleID = &v
		}

		// Parse optional user_id filter.
		var userID *int64
		if u := r.URL.Query().Get("user_id"); u != "" {
			uid, err := strconv.ParseInt(u, 10, 64)
			if err != nil || uid <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a positive integer"})
				return
			}
			userID = &uid
		}

		// Parse limit (default 50, range 1–1000).
		limit := int32(50)
		if l := r.URL.Query().Get("limit"); l != "" {
			parsed, err := strconv.ParseInt(l, 10, 32)
			if err != nil || parsed < 1 || parsed > 1000 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 1000"})
				return
			}
			limit = int32(parsed)
		}

		// Parse offset (default 0, must be >= 0).
		offset := int32(0)
		if o := r.URL.Query().Get("offset"); o != "" {
			parsed, err := strconv.ParseInt(o, 10, 32)
			if err != nil || parsed < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be a non-negative integer"})
				return
			}
			offset = int32(parsed)
		}

		trips, err := lister.ListTrips(r.Context(), status, vehicleID, userID, limit, offset)
		if err != nil {
			slog.Error("failed to list trips", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list trips"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"trips": trips,
			"count": len(trips),
		})
	}
}

func handleGetTrip(getter TripGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid trip id"})
			return
		}

		trip, err := getter.GetTrip(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrTripNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
				return
			}
			slog.Error("failed to get trip", "id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get trip"})
			return
		}

		writeJSON(w, http.StatusOK, trip)
	}
}
