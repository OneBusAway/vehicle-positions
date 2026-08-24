package main

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
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
