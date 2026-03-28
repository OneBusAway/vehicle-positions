package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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

	bearing := pgtype.Float8{}
	if loc.Bearing != nil {
		bearing = pgtype.Float8{Float64: *loc.Bearing, Valid: true}
	}

	speed := pgtype.Float8{}
	if loc.Speed != nil {
		speed = pgtype.Float8{Float64: *loc.Speed, Valid: true}
	}

	accuracy := pgtype.Float8{}
	if loc.Accuracy != nil {
		accuracy = pgtype.Float8{Float64: *loc.Accuracy, Valid: true}
	}

	if err := qtx.InsertLocationPoint(ctx, db.InsertLocationPointParams{
		VehicleID: loc.VehicleID,
		TripID:    loc.TripID,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Bearing:   bearing,
		Speed:     speed,
		Accuracy:  accuracy,
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
			v := row.Bearing.Float64
			loc.Bearing = &v
		}
		if row.Speed.Valid {
			v := row.Speed.Float64
			loc.Speed = &v
		}
		if row.Accuracy.Valid {
			v := row.Accuracy.Float64
			loc.Accuracy = &v
		}
		locations = append(locations, loc)
	}

	return locations, nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// GetAPIKeyByHash looks up an API key by its hash for authentication.
func (s *Store) GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	row, err := s.queries.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		return nil, fmt.Errorf("get api key by hash: %w", err)
	}

	var lastUsedAt *time.Time
	if row.LastUsedAt.Valid {
		t := row.LastUsedAt.Time
		lastUsedAt = &t
	}

	return &APIKey{
		ID:         row.ID,
		Name:       row.Name,
		KeyHash:    row.KeyHash,
		Active:     row.Active,
		LastUsedAt: lastUsedAt,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
	}, nil
}

// UpdateAPIKeyLastUsed updates the last_used_at timestamp for an API key.
func (s *Store) UpdateAPIKeyLastUsed(ctx context.Context, id int64) error {
	if err := s.queries.UpdateAPIKeyLastUsed(ctx, id); err != nil {
		return fmt.Errorf("update api key last used: %w", err)
	}
	return nil
}

// CreateAPIKey creates a new API key with the given name, hash, and active status.
func (s *Store) CreateAPIKey(ctx context.Context, name, keyHash string, active bool) (*APIKey, error) {
	row, err := s.queries.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		Name:    name,
		KeyHash: keyHash,
		Active:  active,
	})
	if err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	var lastUsedAt *time.Time
	if row.LastUsedAt.Valid {
		t := row.LastUsedAt.Time
		lastUsedAt = &t
	}

	return &APIKey{
		ID:         row.ID,
		Name:       row.Name,
		KeyHash:    row.KeyHash,
		Active:     row.Active,
		LastUsedAt: lastUsedAt,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
	}, nil
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}
