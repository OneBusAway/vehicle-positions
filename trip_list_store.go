package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TripLister is the store interface for listing trips with filters.
type TripLister interface {
	ListTrips(ctx context.Context, status *string, vehicleID *string, userID *int64, limit, offset int32) ([]TripResponse, error)
}

// TripGetter is the store interface for fetching a single trip by ID.
type TripGetter interface {
	GetTrip(ctx context.Context, id int64) (*TripResponse, error)
}

// Compile-time interface assertions.
var _ TripLister = (*Store)(nil)
var _ TripGetter = (*Store)(nil)

// ListTrips returns trips matching the given filters, ordered by start_time DESC.
func (s *Store) ListTrips(ctx context.Context, status *string, vehicleID *string, userID *int64, limit, offset int32) ([]TripResponse, error) {
	params := db.ListTripsFilteredParams{
		QueryLimit:  limit,
		QueryOffset: offset,
	}
	if status != nil {
		params.Status = pgtype.Text{String: *status, Valid: true}
	}
	if vehicleID != nil {
		params.VehicleID = pgtype.Text{String: *vehicleID, Valid: true}
	}
	if userID != nil {
		params.UserID = pgtype.Int8{Int64: *userID, Valid: true}
	}

	rows, err := s.queries.ListTripsFiltered(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list trips: %w", err)
	}

	trips := make([]TripResponse, 0, len(rows))
	for _, row := range rows {
		trips = append(trips, tripToResponse(row))
	}
	return trips, nil
}

// GetTrip returns a single trip by ID.
func (s *Store) GetTrip(ctx context.Context, id int64) (*TripResponse, error) {
	row, err := s.queries.GetTripByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTripNotFound
		}
		return nil, fmt.Errorf("get trip: %w", err)
	}
	resp := tripToResponse(row)
	return &resp, nil
}

// tripToResponse converts a db.Trip to the API response type.
func tripToResponse(t db.Trip) TripResponse {
	resp := TripResponse{
		ID:         t.ID,
		UserID:     t.UserID,
		VehicleID:  t.VehicleID,
		RouteID:    t.RouteID,
		GtfsTripID: t.GtfsTripID,
		StartTime:  t.StartTime.Time,
		Status:     t.Status,
		CreatedAt:  t.CreatedAt.Time,
		UpdatedAt:  t.UpdatedAt.Time,
	}
	if t.EndTime.Valid {
		resp.EndTime = &t.EndTime.Time
	}
	return resp
}
