package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
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

// ErrTripNotFound is returned when no matching active trip is found to end.
var ErrTripNotFound = errors.New("active trip not found")

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
	defer tx.Rollback(ctx)

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
		Status:     trip.Status,
	}
	if trip.StartTime.Valid {
		resp.StartTime = trip.StartTime.Time
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
