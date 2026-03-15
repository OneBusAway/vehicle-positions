package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	gtfslocal "github.com/OneBusAway/vehicle-positions/gtfs"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store manages persistence of vehicle locations to PostgreSQL.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
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

	return &Store{pool: pool, queries: db.New(pool)}, nil
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
			slog.Warn("failed to close migration source", "error", srcErr)
		}
		if dbErr != nil {
			slog.Warn("failed to close migration database connection", "error", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
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

	qtx := s.queries.WithTx(tx)

	if err := qtx.UpsertVehicle(ctx, loc.VehicleID); err != nil {
		return fmt.Errorf("upsert vehicle: %w", err)
	}

	if err := qtx.InsertLocationPoint(ctx, db.InsertLocationPointParams{
		VehicleID: loc.VehicleID,
		TripID:    loc.TripID,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		// TODO: LocationReport uses bare float64, so we cannot distinguish
		// "not provided" from zero. Bearing 0.0 (north) is stored as non-NULL
		// even when the field was never set. A follow-up should change these
		// fields to *float64 on LocationReport to preserve the distinction.
		Bearing:   pgtype.Float8{Float64: loc.Bearing, Valid: true},
		Speed:     pgtype.Float8{Float64: loc.Speed, Valid: true},
		Accuracy:  pgtype.Float8{Float64: loc.Accuracy, Valid: true},
		Timestamp: loc.Timestamp,
		DriverID:  loc.DriverID,
	}); err != nil {
		return fmt.Errorf("insert location: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// GetRecentLocations retrieves the latest position for each vehicle since the cutoff time.
func (s *Store) GetRecentLocations(ctx context.Context, cutoff time.Time) ([]*LocationReport, error) {
	rows, err := s.queries.GetRecentLocations(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("query recent locations: %w", err)
	}

	locations := make([]*LocationReport, 0, len(rows))
	for _, row := range rows {
		loc := &LocationReport{
			VehicleID: row.VehicleID,
			TripID:    row.TripID,
			Latitude:  row.Latitude,
			Longitude: row.Longitude,
			Timestamp: row.Timestamp,
			DriverID:  row.DriverID,
		}
		if row.Bearing.Valid {
			loc.Bearing = row.Bearing.Float64
		}
		if row.Speed.Valid {
			loc.Speed = row.Speed.Float64
		}
		if row.Accuracy.Valid {
			loc.Accuracy = row.Accuracy.Float64
		}
		locations = append(locations, loc)
	}

	return locations, nil
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) ImportGTFS(ctx context.Context, stops []gtfslocal.Stop, routes []gtfslocal.Route, trips []gtfslocal.Trip, stopTimes []gtfslocal.StopTime) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "TRUNCATE TABLE stop_times, trips, routes, stops"); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}

	batch := &pgx.Batch{}
	for _, s := range stops {
		batch.Queue(
			"INSERT INTO stops (stop_id, stop_name, stop_lat, stop_lon) VALUES ($1, $2, $3, $4)",
			s.StopID, s.Name, s.Lat, s.Lon,
		)
	}
	for _, r := range routes {
		batch.Queue(
			"INSERT INTO routes (route_id, route_short_name, route_long_name) VALUES ($1, $2, $3)",
			r.RouteID, r.ShortName, r.LongName,
		)
	}
	for _, t := range trips {
		batch.Queue(
			"INSERT INTO trips (trip_id, route_id, service_id) VALUES ($1, $2, $3)",
			t.TripID, t.RouteID, t.ServiceID,
		)
	}
	for _, st := range stopTimes {
		batch.Queue(
			"INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence) VALUES ($1, $2, $3, $4, $5)",
			st.TripID, st.ArrivalTime, st.DepartureTime, st.StopID, st.StopSequence,
		)
	}

	if batch.Len() > 0 {
		br := tx.SendBatch(ctx, batch)
		if err := br.Close(); err != nil {
			return fmt.Errorf("batch insert: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
