package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// TripResponse is the API representation of a trip.
type TripResponse struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	VehicleID  string     `json:"vehicle_id"`
	RouteID    string     `json:"route_id"`
	GtfsTripID string     `json:"gtfs_trip_id"`
	StartTime  time.Time  `json:"start_time"`
	EndTime    *time.Time `json:"end_time,omitempty"`
	Status     string     `json:"status"`
}

// ErrNotAssigned is returned when a driver is not assigned to the requested vehicle.
var ErrNotAssigned = errors.New("driver is not assigned to this vehicle")

// ErrActiveTripExists is returned when the driver already has an active trip.
var ErrActiveTripExists = errors.New("driver already has an active trip")

// ErrTripNotFound is returned when no matching active trip is found to end,
// or when GetTripSummary is given an id that doesn't exist.
var ErrTripNotFound = errors.New("active trip not found")

// TripSummary is the API representation of a trip for admin listing/detail views.
type TripSummary struct {
	ID           int64      `json:"id"`
	VehicleID    string     `json:"vehicle_id"`
	VehicleLabel string     `json:"vehicle_label"`
	UserID       int64      `json:"user_id"`
	DriverName   string     `json:"driver_name"`
	RouteID      string     `json:"route_id"`
	GtfsTripID   string     `json:"gtfs_trip_id"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      *time.Time `json:"end_time,omitempty"`
	Status       string     `json:"status"`
}

// TripFilter narrows ListTrips results.
type TripFilter struct {
	Status    string // "", "active", "completed"
	VehicleID string // "" = all
	Q         string // ILIKE substring on driver name, route_id, gtfs_trip_id
	Limit     int    // callers pass limit+1 to detect hasMore
	Offset    int
}

// ActiveTripInfo describes the active trip currently associated with a vehicle.
type ActiveTripInfo struct {
	TripID     int64
	RouteID    string
	GtfsTripID string
	UserID     int64
	DriverName string
}

// TripStarter is the store interface for starting trips.
type TripStarter interface {
	StartTrip(ctx context.Context, userID int64, vehicleID, routeID, gtfsTripID string) (*TripResponse, error)
}

// TripEnder is the store interface for ending trips.
type TripEnder interface {
	EndTrip(ctx context.Context, tripID, userID int64) error
}

// StartTrip validates the driver-vehicle assignment, checks for an existing active trip,
// and creates a new trip.
func (s *Store) StartTrip(ctx context.Context, userID int64, vehicleID, routeID, gtfsTripID string) (*TripResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("failed to rollback transaction", "error", err)
		}
	}()

	qtx := s.queries.WithTx(tx)

	// Verify driver is assigned to this vehicle.
	_, err = qtx.CheckUserVehicleAssignment(ctx, db.CheckUserVehicleAssignmentParams{
		UserID:    userID,
		VehicleID: vehicleID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotAssigned
		}
		return nil, fmt.Errorf("check assignment: %w", err)
	}

	// Check driver doesn't already have an active trip.
	_, err = qtx.GetActiveTripByUser(ctx, userID)
	if err == nil {
		return nil, ErrActiveTripExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check active trip: %w", err)
	}

	trip, err := qtx.StartTrip(ctx, db.StartTripParams{
		UserID:     userID,
		VehicleID:  vehicleID,
		RouteID:    routeID,
		GtfsTripID: gtfsTripID,
	})
	if err != nil {
		// The unique partial index idx_trips_one_active_per_user catches
		// concurrent inserts that pass the SELECT check above.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "idx_trips_one_active_per_user" {
			return nil, ErrActiveTripExists
		}
		return nil, fmt.Errorf("insert trip: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	resp := &TripResponse{
		ID:         trip.ID,
		UserID:     trip.UserID,
		VehicleID:  trip.VehicleID,
		RouteID:    trip.RouteID,
		GtfsTripID: trip.GtfsTripID,
		StartTime:  trip.StartTime.Time,
		Status:     trip.Status,
	}
	if trip.EndTime.Valid {
		resp.EndTime = &trip.EndTime.Time
	}
	return resp, nil
}

// EndTrip marks an active trip as completed.
func (s *Store) EndTrip(ctx context.Context, tripID, userID int64) error {
	rowsAffected, err := s.queries.EndTrip(ctx, db.EndTripParams{
		ID:     tripID,
		UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("end trip: %w", err)
	}
	if rowsAffected == 0 {
		return ErrTripNotFound
	}
	return nil
}

// ListTrips returns trip summaries newest-first with optional filters.
// Dynamic WHERE clauses make this a hand-written query rather than sqlc.
func (s *Store) ListTrips(ctx context.Context, f TripFilter) ([]TripSummary, error) {
	query := `
		SELECT t.id, t.vehicle_id, v.label, t.user_id, u.name,
		       t.route_id, t.gtfs_trip_id, t.start_time, t.end_time, t.status
		FROM trips t
		JOIN users u ON u.id = t.user_id
		JOIN vehicles v ON v.id = t.vehicle_id`
	var conds []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if f.Status != "" {
		conds = append(conds, "t.status = "+arg(f.Status))
	}
	if f.VehicleID != "" {
		conds = append(conds, "t.vehicle_id = "+arg(f.VehicleID))
	}
	if f.Q != "" {
		p := arg("%" + f.Q + "%")
		conds = append(conds, fmt.Sprintf("(u.name ILIKE %s OR t.route_id ILIKE %s OR t.gtfs_trip_id ILIKE %s)", p, p, p))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY t.start_time DESC LIMIT " + arg(f.Limit) + " OFFSET " + arg(f.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trips: %w", err)
	}
	defer rows.Close()

	var trips []TripSummary
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

// GetTripSummary returns a single trip's summary, joined with vehicle label
// and driver name. Returns ErrTripNotFound when no trip matches id.
func (s *Store) GetTripSummary(ctx context.Context, id int64) (*TripSummary, error) {
	row, err := s.queries.GetTripSummary(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTripNotFound
		}
		return nil, fmt.Errorf("get trip summary: %w", err)
	}

	tr := &TripSummary{
		ID:           row.ID,
		VehicleID:    row.VehicleID,
		VehicleLabel: row.VehicleLabel,
		UserID:       row.UserID,
		DriverName:   row.DriverName,
		RouteID:      row.RouteID,
		GtfsTripID:   row.GtfsTripID,
		StartTime:    row.StartTime.Time,
		Status:       row.Status,
	}
	if row.EndTime.Valid {
		tr.EndTime = &row.EndTime.Time
	}
	return tr, nil
}

// ListTripLocations returns the trail of location points for a trip.
// Per spec §4.5, location_points.trip_id is a client-supplied GTFS string
// (not trips.id), so the trail is derived by matching vehicle + driver +
// the trip's start/end time window rather than a direct trip_id join.
func (s *Store) ListTripLocations(ctx context.Context, tripID int64) ([]LocationPoint, error) {
	rows, err := s.queries.ListTripLocations(ctx, tripID)
	if err != nil {
		return nil, fmt.Errorf("list trip locations: %w", err)
	}

	points := make([]LocationPoint, 0, len(rows))
	for _, row := range rows {
		p := LocationPoint{
			Latitude:   row.Latitude,
			Longitude:  row.Longitude,
			Timestamp:  row.Timestamp,
			TripID:     row.TripID,
			ReceivedAt: row.ReceivedAt.Time,
		}
		if row.Bearing.Valid {
			v := row.Bearing.Float64
			p.Bearing = &v
		}
		if row.Speed.Valid {
			v := row.Speed.Float64
			p.Speed = &v
		}
		if row.Accuracy.Valid {
			v := row.Accuracy.Float64
			p.Accuracy = &v
		}
		points = append(points, p)
	}
	return points, nil
}

// ListActiveTripsByVehicle returns the current active trip for each vehicle
// that has one, keyed by vehicle ID. The schema only guarantees one active
// trip per user (not per vehicle); when multiple drivers have active trips
// on the same vehicle, the most recently started trip wins (spec §4.8).
func (s *Store) ListActiveTripsByVehicle(ctx context.Context) (map[string]ActiveTripInfo, error) {
	rows, err := s.queries.ListActiveTripsByVehicle(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active trips by vehicle: %w", err)
	}

	result := make(map[string]ActiveTripInfo, len(rows))
	for _, row := range rows {
		result[row.VehicleID] = ActiveTripInfo{
			TripID:     row.ID,
			RouteID:    row.RouteID,
			GtfsTripID: row.GtfsTripID,
			UserID:     row.UserID,
			DriverName: row.DriverName,
		}
	}
	return result, nil
}
