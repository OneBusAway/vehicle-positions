package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS vehicles (
    id         TEXT PRIMARY KEY,
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS drivers (
    id         TEXT PRIMARY KEY,
    phone      TEXT UNIQUE NOT NULL,
    pin_hash   TEXT NOT NULL,
    name       TEXT NOT NULL,
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trips (
    id         TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
    driver_id  TEXT NOT NULL REFERENCES drivers(id),
    route_id   TEXT NOT NULL DEFAULT '',
    start_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    end_time   TIMESTAMPTZ,
    status     TEXT NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE', 'COMPLETED', 'CANCELLED'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS location_points (
    id          BIGSERIAL PRIMARY KEY,
    vehicle_id  TEXT NOT NULL REFERENCES vehicles(id),
    trip_id     TEXT NOT NULL DEFAULT '',
    latitude    DOUBLE PRECISION NOT NULL,
    longitude   DOUBLE PRECISION NOT NULL,
    bearing     DOUBLE PRECISION,
    speed       DOUBLE PRECISION,
    accuracy    DOUBLE PRECISION,
    timestamp   BIGINT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_location_points_vehicle_id ON location_points(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_location_points_timestamp ON location_points(timestamp);
CREATE INDEX IF NOT EXISTS idx_trips_driver_id ON trips(driver_id);
`

// Store manages persistence of vehicle locations to PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore connects to PostgreSQL and runs schema migrations.
func NewStore(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return &Store{pool: pool}, nil
}

// SaveLocation upserts the vehicle and inserts a location point in a single transaction.
func (s *Store) SaveLocation(ctx context.Context, loc *LocationReport) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO vehicles (id) VALUES ($1)
		 ON CONFLICT (id) DO UPDATE SET updated_at = NOW()`,
		loc.VehicleID,
	)
	if err != nil {
		return fmt.Errorf("upsert vehicle: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		loc.VehicleID, loc.TripID, loc.Latitude, loc.Longitude,
		loc.Bearing, loc.Speed, loc.Accuracy, loc.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert location: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// GetRecentLocations retrieves the latest position for each vehicle since the cutoff time.
func (s *Store) GetRecentLocations(ctx context.Context, cutoff time.Time) ([]*LocationReport, error) {
	query := `
		SELECT DISTINCT ON (vehicle_id) vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp
		FROM location_points
		WHERE received_at > $1
		ORDER BY vehicle_id, received_at DESC
	`
	rows, err := s.pool.Query(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query recent locations: %w", err)
	}
	defer rows.Close()

	var locations []*LocationReport
	for rows.Next() {
		var loc LocationReport
		var bearing, speed, accuracy *float64

		if err := rows.Scan(&loc.VehicleID, &loc.TripID, &loc.Latitude, &loc.Longitude, &bearing, &speed, &accuracy, &loc.Timestamp); err != nil {
			return nil, fmt.Errorf("scan location: %w", err)
		}

		if bearing != nil {
			loc.Bearing = *bearing
		}
		if speed != nil {
			loc.Speed = *speed
		}
		if accuracy != nil {
			loc.Accuracy = *accuracy
		}

		locations = append(locations, &loc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	return locations, nil
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}
