package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TripsStartedBetweenLister lists the trips a service report judges.
type TripsStartedBetweenLister interface {
	ListTripsStartedBetween(ctx context.Context, from, to time.Time) ([]TripSummary, error)
}

// ListTripsStartedBetween returns the trips started in [from, to) that name a
// GTFS trip, oldest first. A trip with no GTFS trip has no schedule to judge
// it against. It is hand-written like ListTrips, which reads the same columns.
func (s *Store) ListTripsStartedBetween(ctx context.Context, from, to time.Time) ([]TripSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.vehicle_id, v.label, t.user_id, u.name,
		       t.route_id, t.gtfs_trip_id, t.start_time, t.end_time, t.status
		FROM trips t
		JOIN users u ON u.id = t.user_id
		JOIN vehicles v ON v.id = t.vehicle_id
		WHERE t.start_time >= $1 AND t.start_time < $2 AND t.gtfs_trip_id <> ''
		ORDER BY t.start_time, t.id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("list trips started between: %w", err)
	}
	defer rows.Close()

	trips := make([]TripSummary, 0)
	for rows.Next() {
		var tr TripSummary
		var endTime pgtype.Timestamptz
		if err := rows.Scan(&tr.ID, &tr.VehicleID, &tr.VehicleLabel, &tr.UserID, &tr.DriverName,
			&tr.RouteID, &tr.GtfsTripID, &tr.StartTime, &endTime, &tr.Status); err != nil {
			return nil, fmt.Errorf("scan trip: %w", err)
		}
		if endTime.Valid {
			t := endTime.Time
			tr.EndTime = &t
		}
		trips = append(trips, tr)
	}
	return trips, rows.Err()
}
