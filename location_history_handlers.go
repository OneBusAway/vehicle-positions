package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
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
	HasMore   bool            `json:"has_more"`
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
	ReceivedAt string   `json:"received_at"`
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

		to, err := parseOptionalInt64(q.Get("to"), time.Now().Unix())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to must be a valid unix timestamp"})
			return
		}
		// Default from relative to to (not now) so a ?to= in the past selects
		// the 24h window ending at to instead of tripping the from > to check.
		from, err := parseOptionalInt64(q.Get("from"), to-86400)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be a valid unix timestamp"})
			return
		}
		if from > to {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be less than or equal to to"})
			return
		}

		limit, err := parseLimit(q, defaultHistoryLimit, maxHistoryLimit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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

		// Fetch one extra row to detect whether results were truncated at limit.
		points, err := lister.GetLocationHistory(r.Context(), vehicleID, from, to, limit+1)
		if err != nil {
			slog.Error("failed to get location history", "vehicle_id", vehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		hasMore := len(points) > limit
		if hasMore {
			points = points[:limit]
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
			HasMore:   hasMore,
			Locations: entries,
		})
	}
}

func writeCSV(w http.ResponseWriter, vehicleID string, points []LocationPoint) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s_locations.csv"`, vehicleID))
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)

	header := []string{"timestamp", "latitude", "longitude", "bearing", "speed", "accuracy", "trip_id", "received_at"}
	if err := writer.Write(header); err != nil {
		slog.Error("failed to write CSV header", "vehicle_id", vehicleID, "error", err)
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
			sanitizeCSVCell(p.TripID),
			p.ReceivedAt.UTC().Format(time.RFC3339),
		}
		if err := writer.Write(record); err != nil {
			slog.Error("failed to write CSV record", "vehicle_id", vehicleID, "error", err)
			return
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		slog.Error("failed to flush CSV response", "vehicle_id", vehicleID, "error", err)
	}
}

// sanitizeCSVCell prevents CSV formula injection: cells beginning with =, +, -,
// @, tab, or CR are evaluated as formulas by Excel, LibreOffice, and Google
// Sheets. Prefixing with a single quote forces text interpretation. Only
// user-supplied text cells (trip_id) need this — numeric cells are
// server-formatted floats, and escaping them would corrupt negative values.
func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
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

// parsePage reads the limit/offset pair every paginated list endpoint takes,
// bounding limit to [1, maxLimit] and offset to non-negative. The returned
// error is the message the endpoint answers 400 with, so the three lists that
// share this validation cannot drift apart in what they say.
func parsePage(q url.Values, defaultLimit, maxLimit int) (limit, offset int, err error) {
	if limit, err = parseLimit(q, defaultLimit, maxLimit); err != nil {
		return 0, 0, err
	}
	// The stores hand offset to Postgres as an int32; anything larger would
	// wrap rather than page.
	offset, err = parseOptionalInt(q.Get("offset"), 0)
	if err != nil || offset < 0 || offset > math.MaxInt32 {
		return 0, 0, errors.New("offset must be a non-negative integer")
	}
	return limit, offset, nil
}

// parseLimit is the limit half of parsePage, for an endpoint that takes no
// offset and so must neither honour nor reject one.
func parseLimit(q url.Values, defaultLimit, maxLimit int) (int, error) {
	limit, err := parseOptionalInt(q.Get("limit"), defaultLimit)
	if err != nil || limit < 1 || limit > maxLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}
	return limit, nil
}
