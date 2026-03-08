package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store manages persistence of vehicle locations to PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore connects to PostgreSQL.
func NewStore(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Migrate runs the database schema migrations.
func (s *Store) Migrate(databaseURL string) error {
	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("invalid migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		return fmt.Errorf("migration instance error: %w", err)
	}

	// Close migration source and database connection when done.
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.Printf("failed to close migration source: %v", srcErr)
		}
		if dbErr != nil {
			log.Printf("failed to close migration database connection: %v", dbErr)
		}
	}()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
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
