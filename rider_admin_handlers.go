package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	// defaultRiderRideListLimit and maxRiderRideListLimit bound one page of the
	// admin rides list.
	defaultRiderRideListLimit = 50
	maxRiderRideListLimit     = 200
)

// riderRideStatuses are the ride statuses the admin list may be filtered by.
// They are the only two a stored ride ever has.
var riderRideStatuses = map[string]bool{"active": true, "ended": true}

// riderStatusProvider is the rider subsystem's own account of itself, as the
// admin status endpoint needs it. *riderService implements it, and riderOff
// stands in for it when rider mode is off.
type riderStatusProvider interface {
	RiderStatus(ctx context.Context) (riderStatusResponse, error)
}

// riderStatusResponse is GET /api/v1/admin/rider/status. Everything past
// Enabled is absent when rider mode is off, so the disabled answer is exactly
// {"enabled":false}.
type riderStatusResponse struct {
	Enabled      bool              `json:"enabled"`
	GTFS         *riderGTFSStatus  `json:"gtfs,omitempty"`
	TrustedFeeds []riderFeedStatus `json:"trusted_feeds,omitempty"`
	Riders       *riderTierCounts  `json:"riders,omitempty"`
	Rides        *riderRideCounts  `json:"rides,omitempty"`
}

// riderGTFSStatus describes the schedule snapshot the rider engine is matching
// against.
type riderGTFSStatus struct {
	Source   string `json:"source"`
	LoadedAt string `json:"loaded_at"` // RFC3339 UTC
	Trips    int    `json:"trips"`
	Shapes   int    `json:"shapes"`
	Timezone string `json:"timezone"`
}

// riderFeedStatus is one configured trusted feed's health.
type riderFeedStatus struct {
	URL         string `json:"url"`
	LastSuccess string `json:"last_success"` // RFC3339 UTC, "" if never
	LastError   string `json:"last_error"`
	Entities    int    `json:"entities"`
}

// riderTierCounts is how the registered riders divide by reputation.
type riderTierCounts struct {
	Total   int `json:"total"`
	Trusted int `json:"trusted"`
	Blocked int `json:"blocked"`
}

// riderRideCounts is what the aggregator is holding right now: rides in
// progress, and the trips their riders are actually positioning.
type riderRideCounts struct {
	Active      int `json:"active"`
	Publishable int `json:"publishable"`
}

// adminRideEntry is one ride in the admin list.
type adminRideEntry struct {
	ID                 string  `json:"id"`
	RiderID            string  `json:"rider_id"`
	TripID             string  `json:"trip_id"`
	StartDate          string  `json:"start_date"`
	RouteID            string  `json:"route_id"`
	State              string  `json:"state"`
	Corroborated       bool    `json:"corroborated"`
	Status             string  `json:"status"`
	EndReason          string  `json:"end_reason"`
	PointsTotal        int     `json:"points_total"`
	PointsMatched      int     `json:"points_matched"`
	PointsCorroborated int     `json:"points_corroborated"`
	StartedAt          string  `json:"started_at"` // RFC3339 UTC
	EndedAt            *string `json:"ended_at"`   // RFC3339 UTC, null while active
}

type adminRidesResponse struct {
	Count   int              `json:"count"`
	HasMore bool             `json:"has_more"`
	Rides   []adminRideEntry `json:"rides"`
}

// handleRiderAdminStatus reports the rider subsystem's health. Rider mode
// being off is a fact about the server rather than an error, and riderOff
// reports it: the answer either way is 200 with the provider's own account.
func handleRiderAdminStatus(p riderStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := p.RiderStatus(r.Context())
		if err != nil {
			slog.Error("failed to build rider status", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

// handleRiderAdminRides lists rides by status, newest first. It fetches
// limit+1 rows so has_more is known without a second count query.
func handleRiderAdminRides(store RideLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := q.Get("status")
		if status == "" {
			status = "active"
		}
		if !riderRideStatuses[status] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `status must be "active" or "ended"`})
			return
		}

		limit, offset, err := parsePage(q, defaultRiderRideListLimit, maxRiderRideListLimit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		rides, err := store.ListRides(r.Context(), status, limit+1, offset)
		if err != nil {
			slog.Error("failed to list rides", "status", status, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		hasMore := len(rides) > limit
		if hasMore {
			rides = rides[:limit]
		}

		entries := make([]adminRideEntry, 0, len(rides))
		for _, ride := range rides {
			entries = append(entries, adminRideEntryOf(ride))
		}
		writeJSON(w, http.StatusOK, adminRidesResponse{
			Count:   len(entries),
			HasMore: hasMore,
			Rides:   entries,
		})
	}
}

// adminRideEntryOf is one stored ride as the admin list reports it.
func adminRideEntryOf(ride Ride) adminRideEntry {
	entry := adminRideEntry{
		ID:                 ride.ID,
		RiderID:            ride.RiderID,
		TripID:             ride.TripID,
		StartDate:          ride.StartDate,
		RouteID:            ride.RouteID,
		State:              ride.State,
		Corroborated:       ride.Corroborated,
		Status:             ride.Status,
		EndReason:          ride.EndReason,
		PointsTotal:        ride.PointsTotal,
		PointsMatched:      ride.PointsMatched,
		PointsCorroborated: ride.PointsCorroborated,
		StartedAt:          adminRiderTime(ride.StartedAt),
	}
	if ride.EndedAt != nil {
		ended := adminRiderTime(*ride.EndedAt)
		entry.EndedAt = &ended
	}
	return entry
}

// adminRiderTime formats a timestamp for the rider admin endpoints. A zero
// time is reported as the empty string rather than as year one.
func adminRiderTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// riderFeedStatuses is the trusted feeds' health as the status endpoint
// reports it. A feed that has never succeeded has no last_success.
func riderFeedStatuses(health []rider.FeedHealth) []riderFeedStatus {
	out := make([]riderFeedStatus, 0, len(health))
	for _, h := range health {
		out = append(out, riderFeedStatus{
			URL:         h.URL,
			LastSuccess: adminRiderTime(h.LastSuccess),
			LastError:   h.LastError,
			Entities:    h.Entities,
		})
	}
	return out
}
