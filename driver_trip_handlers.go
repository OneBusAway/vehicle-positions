package main

import (
	"log/slog"
	"net/http"
	"time"
)

// myTrip is one of a driver's own trips as the app lists them.
type myTrip struct {
	ID           int64  `json:"id"`
	VehicleID    string `json:"vehicle_id"`
	VehicleLabel string `json:"vehicle_label"`
	RouteID      string `json:"route_id"`
	// Route is the trip's route as the schedule has it now, nil when the trip
	// has none or the schedule no longer has it.
	Route      *catalogTripRoute `json:"route"`
	GtfsTripID string            `json:"gtfs_trip_id"`
	Headsign   string            `json:"headsign"`
	StartTime  time.Time         `json:"start_time"`
	EndTime    *time.Time        `json:"end_time,omitempty"`
	Status     string            `json:"status"`
}

type myTripListResponse struct {
	Count   int      `json:"count"`
	HasMore bool     `json:"has_more"`
	Trips   []myTrip `json:"trips"`
}

// handleListMyTrips returns a page of the calling driver's own trips, newest
// first. As in handleListMyVehicles the user ID comes from the JWT claims, so a
// driver can list no one else's.
func handleListMyTrips(store TripLister, catalog *gtfsCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, claimsErr, status := userIDFromClaims(r)
		if claimsErr != nil {
			slog.Warn("handleListMyTrips: invalid claims", "error", claimsErr)
			writeJSON(w, status, map[string]string{"error": claimsErr.Error()})
			return
		}
		// TripFilter reads 0 as every driver.
		if userID < 1 {
			slog.Warn("handleListMyTrips: subject is no account", "sub", userID)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token: invalid subject"})
			return
		}
		limit, offset, err := parsePage(r.URL.Query(), defaultTripListLimit, maxTripListLimit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// One extra row says whether there is another page.
		trips, err := store.ListTrips(r.Context(), TripFilter{UserID: userID, Limit: limit + 1, Offset: offset})
		if err != nil {
			slog.Error("failed to list driver trips", "user_id", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		hasMore := len(trips) > limit
		if hasMore {
			trips = trips[:limit]
		}
		out := myTripListResponse{Count: len(trips), HasMore: hasMore, Trips: make([]myTrip, 0, len(trips))}
		for _, trip := range trips {
			out.Trips = append(out.Trips, catalog.myTrip(trip))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// myTrip is trip with its route and headsign from the schedule, as far as
// it has them. Safe on a nil catalog.
func (c *gtfsCatalog) myTrip(trip TripSummary) myTrip {
	out := myTrip{
		ID: trip.ID, VehicleID: trip.VehicleID, VehicleLabel: trip.VehicleLabel,
		RouteID: trip.RouteID, GtfsTripID: trip.GtfsTripID,
		StartTime: trip.StartTime, EndTime: trip.EndTime, Status: trip.Status,
	}
	if c == nil {
		return out
	}
	ix := c.index()
	if ix == nil {
		return out
	}
	// A trip can be started with its GTFS trip alone.
	routeID := trip.RouteID
	if scheduled, ok := ix.Trip(trip.GtfsTripID); ok {
		out.Headsign = scheduled.Headsign
		if routeID == "" {
			routeID = scheduled.RouteID
		}
	}
	if route, ok := ix.Route(routeID); ok {
		out.Route = &catalogTripRoute{ShortName: route.ShortName, LongName: route.LongName, Color: route.Color, TextColor: route.TextColor}
	}
	return out
}
