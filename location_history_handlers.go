package main

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultHistoryLimit = 100
	maxHistoryLimit     = 1000
)

type locationHistoryResponse struct {
	VehicleID string          `json:"vehicle_id"`
	Count     int             `json:"count"`
	Locations []locationEntry `json:"locations"`
}

type locationEntry struct {
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Bearing    *float64 `json:"bearing"`
	Speed      *float64 `json:"speed"`
	Accuracy   *float64 `json:"accuracy"`
	Timestamp  int64    `json:"timestamp"`
	TripID     string   `json:"trip_id"`
	ReceivedAt string   `json:"recorded_at"`
}

func handleGetLocationHistory(lister LocationHistoryLister, checker VehicleChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vehicleID := r.PathValue("vehicleID")
		if vehicleID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id is required"})
			return
		}
		if len(vehicleID) > maxVehicleIDLength {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("vehicle_id must be at most %d characters", maxVehicleIDLength)})
			return
		}
		if !vehicleIDPattern.MatchString(vehicleID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_id must contain only alphanumeric characters, dots, hyphens, and underscores"})
			return
		}

		q := r.URL.Query()

		from, err := parseOptionalInt64(q.Get("from"), 0)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be a valid unix timestamp"})
			return
		}
		to, err := parseOptionalInt64(q.Get("to"), time.Now().Unix())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to must be a valid unix timestamp"})
			return
		}
		if from > to {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be less than or equal to to"})
			return
		}

		limit, err := parseOptionalInt(q.Get("limit"), defaultHistoryLimit)
		if err != nil || limit < 1 || limit > maxHistoryLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("limit must be between 1 and %d", maxHistoryLimit)})
			return
		}

		format := q.Get("format")
		if format == "" {
			format = "json"
		}
		if format != "json" && format != "csv" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "format must be json or csv"})
			return
		}

		exists, err := checker.VehicleExists(r.Context(), vehicleID)
		if err != nil {
			slog.Error("failed to check vehicle existence", "vehicle_id", vehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle not found"})
			return
		}

		points, err := lister.GetLocationHistory(r.Context(), vehicleID, from, to, limit)
		if err != nil {
			slog.Error("failed to get location history", "vehicle_id", vehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if format == "csv" {
			writeCSV(w, vehicleID, points)
			return
		}

		entries := make([]locationEntry, 0, len(points))
		for _, p := range points {
			entries = append(entries, locationEntry{
				Latitude:   p.Latitude,
				Longitude:  p.Longitude,
				Bearing:    p.Bearing,
				Speed:      p.Speed,
				Accuracy:   p.Accuracy,
				Timestamp:  p.Timestamp,
				TripID:     p.TripID,
				ReceivedAt: p.ReceivedAt.UTC().Format(time.RFC3339),
			})
		}

		writeJSON(w, http.StatusOK, locationHistoryResponse{
			VehicleID: vehicleID,
			Count:     len(entries),
			Locations: entries,
		})
	}
}

func writeCSV(w http.ResponseWriter, vehicleID string, points []LocationPoint) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s_locations.csv"`, vehicleID))
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	header := []string{"timestamp", "latitude", "longitude", "bearing", "speed", "accuracy", "trip_id", "recorded_at"}
	if err := writer.Write(header); err != nil {
		slog.Error("failed to write CSV header", "error", err)
		return
	}

	for _, p := range points {
		record := []string{
			strconv.FormatInt(p.Timestamp, 10),
			strconv.FormatFloat(p.Latitude, 'f', -1, 64),
			strconv.FormatFloat(p.Longitude, 'f', -1, 64),
			formatOptionalFloat(p.Bearing),
			formatOptionalFloat(p.Speed),
			formatOptionalFloat(p.Accuracy),
			p.TripID,
			p.ReceivedAt.UTC().Format(time.RFC3339),
		}
		if err := writer.Write(record); err != nil {
			slog.Error("failed to write CSV record", "error", err)
			return
		}
	}
}

func formatOptionalFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func parseOptionalInt64(s string, defaultVal int64) (int64, error) {
	if s == "" {
		return defaultVal, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseOptionalInt(s string, defaultVal int) (int, error) {
	if s == "" {
		return defaultVal, nil
	}
	return strconv.Atoi(s)
}
