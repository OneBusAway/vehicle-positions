package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// ActiveTripLister returns the current active trip for each vehicle that
// has one, keyed by vehicle ID.
type ActiveTripLister interface {
	ListActiveTripsByVehicle(ctx context.Context) (map[string]ActiveTripInfo, error)
}

// liveVehicleEntry is the JSON representation of a single vehicle's live
// position, joined with its label and (if any) active trip.
type liveVehicleEntry struct {
	VehicleID  string   `json:"vehicle_id"`
	Label      string   `json:"label"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Bearing    *float64 `json:"bearing"`
	Speed      *float64 `json:"speed"`
	GtfsTripID string   `json:"gtfs_trip_id"`
	TripDBID   *int64   `json:"trip_db_id"`
	RouteID    *string  `json:"route_id"`
	DriverName *string  `json:"driver_name"`
	ReportedAt int64    `json:"reported_at"`
	UpdatedAt  string   `json:"updated_at"` // RFC3339 UTC
}

type liveVehiclesResponse struct {
	Count    int                `json:"count"`
	Vehicles []liveVehicleEntry `json:"vehicles"`
}

// handleLiveVehicles returns the current positions of all actively-reporting
// vehicles, joined with their DB label (falling back to the vehicle id when
// unknown) and their current active trip, if any.
func handleLiveVehicles(tracker *Tracker, vehicles VehicleManager, trips ActiveTripLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vehicleList, err := vehicles.ListVehicles(r.Context())
		if err != nil {
			slog.Error("failed to list vehicles for live view", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list vehicles"})
			return
		}
		labels := make(map[string]string, len(vehicleList))
		for _, v := range vehicleList {
			labels[v.ID] = v.Label
		}

		activeTrips, err := trips.ListActiveTripsByVehicle(r.Context())
		if err != nil {
			slog.Error("failed to list active trips for live view", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list active trips"})
			return
		}

		active := tracker.ActiveVehicles()
		entries := make([]liveVehicleEntry, 0, len(active))
		for _, state := range active {
			label := state.VehicleID
			if l, ok := labels[state.VehicleID]; ok {
				label = l
			}

			entry := liveVehicleEntry{
				VehicleID:  state.VehicleID,
				Label:      label,
				Latitude:   state.Latitude,
				Longitude:  state.Longitude,
				Bearing:    state.Bearing,
				Speed:      state.Speed,
				GtfsTripID: state.TripID,
				ReportedAt: state.Timestamp,
				UpdatedAt:  state.UpdatedAt.UTC().Format(time.RFC3339),
			}

			if trip, ok := activeTrips[state.VehicleID]; ok {
				tripID := trip.TripID
				entry.TripDBID = &tripID
				routeID := trip.RouteID
				entry.RouteID = &routeID
				driverName := trip.DriverName
				entry.DriverName = &driverName
			}

			entries = append(entries, entry)
		}

		sort.Slice(entries, func(i, j int) bool { return entries[i].VehicleID < entries[j].VehicleID })

		writeJSON(w, http.StatusOK, liveVehiclesResponse{
			Count:    len(entries),
			Vehicles: entries,
		})
	}
}

const (
	defaultTripListLimit = 50
	maxTripListLimit     = 200
)

// TripTrailStore is the store interface required to serve a single trip's
// summary and location trail, for the admin trip detail/map view.
type TripTrailStore interface {
	GetTripSummary(ctx context.Context, id int64) (*TripSummary, error)
	ListTripLocations(ctx context.Context, tripID int64) ([]LocationPoint, error)
}

type tripListResponse struct {
	Count   int           `json:"count"`
	HasMore bool          `json:"has_more"`
	Trips   []TripSummary `json:"trips"`
}

// tripTrailPoint is the JSON representation of a single point in a trip's
// location trail. Field names are consumed directly by the admin map JS.
type tripTrailPoint struct {
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Bearing    *float64 `json:"bearing"`
	Speed      *float64 `json:"speed"`
	Accuracy   *float64 `json:"accuracy"`
	ReportedAt int64    `json:"reported_at"`
	ReceivedAt string   `json:"received_at"` // RFC3339 UTC
}

type tripTrailResponse struct {
	Trip   TripSummary      `json:"trip"`
	Points []tripTrailPoint `json:"points"`
}

// handleListTrips returns trip summaries for the admin trips list, filtered
// by status/vehicle_id/q and paginated by limit/offset. It fetches limit+1
// rows from the store to detect has_more without a separate count query.
func handleListTrips(store TripLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := q.Get("status")
		if !validTripStatus(status) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `status must be "", "active", or "completed"`})
			return
		}

		limit, err := parseOptionalInt(q.Get("limit"), defaultTripListLimit)
		if err != nil || limit < 1 || limit > maxTripListLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("limit must be between 1 and %d", maxTripListLimit)})
			return
		}

		offset, err := parseOptionalInt(q.Get("offset"), 0)
		if err != nil || offset < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be a non-negative integer"})
			return
		}

		// Absent user_id means "all drivers". A present one must be a real
		// users.id, so 0 and negatives are rejected rather than silently
		// collapsing into the no-filter sentinel.
		var userID int64
		if raw := q.Get("user_id"); raw != "" {
			userID, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || userID < 1 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a positive integer"})
				return
			}
		}

		filter := TripFilter{
			Status:    status,
			VehicleID: q.Get("vehicle_id"),
			UserID:    userID,
			Q:         q.Get("q"),
			// Fetch one extra row to detect whether results were truncated at limit.
			Limit:  limit + 1,
			Offset: offset,
		}

		trips, err := store.ListTrips(r.Context(), filter)
		if err != nil {
			slog.Error("failed to list trips", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		hasMore := len(trips) > limit
		if hasMore {
			trips = trips[:limit]
		}
		if trips == nil {
			trips = []TripSummary{}
		}

		writeJSON(w, http.StatusOK, tripListResponse{
			Count:   len(trips),
			HasMore: hasMore,
			Trips:   trips,
		})
	}
}

// handleGetTrip returns a single trip's summary for the admin trip detail
// view. It is the trail-free counterpart to handleTripLocations, so callers
// that only need trip metadata do not pay for up to 10k location points. A
// non-numeric or unknown {id} both produce 404, since neither identifies a
// real trip.
func handleGetTrip(store TripSummaryGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
			return
		}

		trip, err := store.GetTripSummary(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrTripNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
				return
			}
			slog.Error("failed to get trip summary", "trip_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if trip == nil {
			// Defensive: a well-behaved store returns ErrTripNotFound rather
			// than (nil, nil), but guard against it to avoid a nil dereference.
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
			return
		}

		writeJSON(w, http.StatusOK, trip)
	}
}

// handleTripLocations returns a single trip's summary joined with its
// location trail, for the admin trip detail/map view. A non-numeric or
// unknown {id} both produce 404, since neither identifies a real trip.
func handleTripLocations(store TripTrailStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
			return
		}

		trip, err := store.GetTripSummary(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrTripNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
				return
			}
			slog.Error("failed to get trip summary", "trip_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if trip == nil {
			// Defensive: a well-behaved store returns ErrTripNotFound rather
			// than (nil, nil), but guard against it to avoid a nil dereference.
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trip not found"})
			return
		}

		points, err := store.ListTripLocations(r.Context(), id)
		if err != nil {
			slog.Error("failed to list trip locations", "trip_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		entries := make([]tripTrailPoint, 0, len(points))
		for _, p := range points {
			entries = append(entries, tripTrailPoint{
				Latitude:   p.Latitude,
				Longitude:  p.Longitude,
				Bearing:    p.Bearing,
				Speed:      p.Speed,
				Accuracy:   p.Accuracy,
				ReportedAt: p.Timestamp,
				ReceivedAt: p.ReceivedAt.UTC().Format(time.RFC3339),
			})
		}

		writeJSON(w, http.StatusOK, tripTrailResponse{
			Trip:   *trip,
			Points: entries,
		})
	}
}
