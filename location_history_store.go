package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	query := `
		SELECT latitude, longitude, bearing, speed, accuracy, timestamp, trip_id, received_at
		FROM location_points
		WHERE vehicle_id = $1
		  AND timestamp >= $2
		  AND timestamp <= $3
		ORDER BY timestamp DESC
		LIMIT $4
	`

	rows, err := s.pool.Query(ctx, query, vehicleID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("query location history: %w", err)
	}
	defer rows.Close()

	points := make([]LocationPoint, 0)
	for rows.Next() {
		var p LocationPoint
		var bearing, speed, accuracy *float64
		if err := rows.Scan(&p.Latitude, &p.Longitude, &bearing, &speed, &accuracy, &p.Timestamp, &p.TripID, &p.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan location point: %w", err)
		}
		p.Bearing = bearing
		p.Speed = speed
		p.Accuracy = accuracy
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate location points: %w", err)
	}

	return points, nil
}

// VehicleExists returns true if a vehicle with the given ID exists.
func (s *Store) VehicleExists(ctx context.Context, vehicleID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vehicles WHERE id = $1)", vehicleID).Scan(&exists)
	if err != nil && err != pgx.ErrNoRows {
		return false, fmt.Errorf("check vehicle exists: %w", err)
	}
	return exists, nil
}
