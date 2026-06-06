package main

import (
	"context"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
)

// LocationPoint represents a single persisted location point for history queries.
type LocationPoint struct {
	Latitude   float64
	Longitude  float64
	Bearing    *float64
	Speed      *float64
	Accuracy   *float64
	Timestamp  int64
	TripID     string
	ReceivedAt time.Time
}

// LocationHistoryLister retrieves historical location points for a vehicle.
type LocationHistoryLister interface {
	GetLocationHistory(ctx context.Context, vehicleID string, from, to int64, limit int) ([]LocationPoint, error)
}

// VehicleChecker checks whether a vehicle exists.
type VehicleChecker interface {
	VehicleExists(ctx context.Context, vehicleID string) (bool, error)
}

// GetLocationHistory returns location points for a vehicle within the given
// timestamp range, ordered by most recent first.
func (s *Store) GetLocationHistory(ctx context.Context, vehicleID string, from, to int64, limit int) ([]LocationPoint, error) {
	rows, err := s.queries.GetLocationHistory(ctx, db.GetLocationHistoryParams{
		VehicleID:   vehicleID,
		Timestamp:   from,
		Timestamp_2: to,
		Limit:       int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("query location history: %w", err)
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

// VehicleExists returns true if a vehicle with the given ID exists.
func (s *Store) VehicleExists(ctx context.Context, vehicleID string) (bool, error) {
	exists, err := s.queries.VehicleExists(ctx, vehicleID)
	if err != nil {
		return false, fmt.Errorf("check vehicle exists: %w", err)
	}
	return exists, nil
}
