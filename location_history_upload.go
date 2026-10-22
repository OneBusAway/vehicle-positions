package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// maxHistoryAge is how old an uploaded fix may be: a phone out of
	// coverage for a whole shift still gets its trail in.
	maxHistoryAge = 24 * time.Hour
	// maxHistoryBatch bounds one upload. A phone with more sends several.
	maxHistoryBatch = 500
	// historyUploadInterval and historyUploadBurst bound a driver's uploads,
	// separately from realtime reports: a phone back in coverage drains its
	// backlog a batch at a time without starving its live position.
	historyUploadInterval = 5 * time.Second
	historyUploadBurst    = 3
)

// HistoryPoint is one fix in a history upload: a LocationReport without the
// vehicle, which the batch names once.
type HistoryPoint struct {
	TripID    string   `json:"trip_id"`
	RouteID   string   `json:"route_id"`
	StartDate string   `json:"start_date"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Bearing   *float64 `json:"bearing,omitempty"`
	Speed     *float64 `json:"speed,omitempty"`
	Accuracy  *float64 `json:"accuracy,omitempty"`
	Timestamp int64    `json:"timestamp"`
}

// LocationHistoryRequest is the body of POST /api/v1/locations/history.
type LocationHistoryRequest struct {
	VehicleID string         `json:"vehicle_id"`
	Points    []HistoryPoint `json:"points"`
}

// LocationHistorySaver stores fixes for history only.
type LocationHistorySaver interface {
	SaveLocationHistory(ctx context.Context, userID int64, vehicleID string, points []*LocationReport) (int, error)
}

// historyUploadResponse counts the batch's points: saved are newly stored,
// invalid failed validation and were skipped. A point already stored, from an
// upload retried after its reply was lost, is in neither.
type historyUploadResponse struct {
	Saved   int `json:"saved"`
	Invalid int `json:"invalid"`
}

// handlePostLocationHistory stores fixes a phone recorded while it could not
// reach the server. They go to location history, which trip playback and
// on-time analysis read, and never to the tracker: the GTFS-RT feed is for
// where vehicles are now, and the server already refuses a realtime report
// older than maxTimestampSkew. The driver must be assigned to the vehicle. A
// point that fails validation is skipped rather than costing the rest of the
// batch, which the phone deletes once it gets 201.
func handlePostLocationHistory(store LocationHistorySaver, rl *VehicleRateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}
		userID, claimsErr, status := userIDFromClaims(r)
		if claimsErr != nil {
			writeJSON(w, status, map[string]string{"error": claimsErr.Error()})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req LocationHistoryRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + sanitizeJSONError(err)})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		}
		if len(req.Points) == 0 || len(req.Points) > maxHistoryBatch {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("points must hold 1 to %d fixes", maxHistoryBatch)})
			return
		}

		if err := validateVehicleID(req.VehicleID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		driverID := strconv.FormatInt(userID, 10)
		reports := make([]*LocationReport, 0, len(req.Points))
		var firstInvalid error
		for i, p := range req.Points {
			loc := &LocationReport{
				VehicleID: req.VehicleID, TripID: strings.TrimSpace(p.TripID), RouteID: strings.TrimSpace(p.RouteID),
				StartDate: p.StartDate, Latitude: p.Latitude, Longitude: p.Longitude,
				Bearing: p.Bearing, Speed: p.Speed, Accuracy: p.Accuracy, Timestamp: p.Timestamp,
				DriverID: driverID,
			}
			if err := loc.validateWithin(maxHistoryAge); err != nil {
				if firstInvalid == nil {
					firstInvalid = fmt.Errorf("points[%d]: %w", i, err)
				}
				continue
			}
			reports = append(reports, loc)
		}

		if !rl.Allow(driverID) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded: too many history uploads"})
			return
		}
		invalid := len(req.Points) - len(reports)
		if invalid > 0 {
			slog.Warn("skipping invalid points in location history upload", "vehicle_id", req.VehicleID, "invalid", invalid, "first", firstInvalid)
		}
		if len(reports) == 0 {
			writeJSON(w, http.StatusCreated, historyUploadResponse{Invalid: invalid})
			return
		}
		saved, err := store.SaveLocationHistory(r.Context(), userID, req.VehicleID, reports)
		if err != nil {
			if errors.Is(err, ErrNotAssigned) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "driver is not assigned to this vehicle"})
				return
			}
			slog.Error("failed to save location history", "vehicle_id", req.VehicleID, "points", len(reports), "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save location history"})
			return
		}
		writeJSON(w, http.StatusCreated, historyUploadResponse{Saved: saved, Invalid: invalid})
	}
}
