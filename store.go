package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
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

	if _, err := qtx.InsertLocationPoint(ctx, db.InsertLocationPointParams{
		VehicleID: loc.VehicleID,
		TripID:    loc.TripID,
		RouteID:   loc.RouteID,
		StartDate: loc.StartDate,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Bearing:   optionalFloat(loc.Bearing),
		Speed:     optionalFloat(loc.Speed),
		Accuracy:  optionalFloat(loc.Accuracy),
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

// SaveLocationHistory stores a batch of fixes a phone recorded offline, in one
// transaction, once it has checked the driver is assigned to the vehicle, and
// returns how many it stored: a fix already stored, from an upload retried
// after its reply was lost, is not stored twice. The assignment's foreign key
// guarantees the vehicle exists, so none is created.
func (s *Store) SaveLocationHistory(ctx context.Context, userID int64, vehicleID string, points []*LocationReport) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)
	qtx := s.queries.WithTx(tx)

	if _, err := qtx.CheckUserVehicleAssignment(ctx, db.CheckUserVehicleAssignmentParams{UserID: userID, VehicleID: vehicleID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotAssigned
		}
		return 0, fmt.Errorf("check assignment: %w", err)
	}
	saved := 0
	for _, loc := range points {
		n, err := qtx.InsertLocationPoint(ctx, db.InsertLocationPointParams{
			VehicleID: vehicleID,
			TripID:    loc.TripID,
			RouteID:   loc.RouteID,
			StartDate: loc.StartDate,
			Latitude:  loc.Latitude,
			Longitude: loc.Longitude,
			Bearing:   optionalFloat(loc.Bearing),
			Speed:     optionalFloat(loc.Speed),
			Accuracy:  optionalFloat(loc.Accuracy),
			Timestamp: loc.Timestamp,
			DriverID:  loc.DriverID,
		})
		if err != nil {
			return 0, fmt.Errorf("insert location: %w", err)
		}
		saved += int(n)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return saved, nil
}

// GetRecentLocations retrieves the latest position for each vehicle since the
// cutoff time. Every field the feed publishes is restored, route_id and
// start_date included: the tracker is seeded from these rows at startup, and a
// report that came back without them would publish a narrower TripDescriptor
// than the driver sent — or, for a driver who named only a route, none at all.
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
			RouteID:   row.RouteID,
			StartDate: row.StartDate,
			Latitude:  row.Latitude,
			Longitude: row.Longitude,
			Timestamp: row.Timestamp,
			DriverID:  row.DriverID,
		}
		loc.Bearing = nullableFloat(row.Bearing)
		loc.Speed = nullableFloat(row.Speed)
		loc.Accuracy = nullableFloat(row.Accuracy)
		locations = append(locations, loc)
	}

	return locations, nil
}

// likeSubstringPattern builds the ILIKE pattern that matches q as a literal
// substring. LIKE metacharacters are escaped so a search for a literal % or _
// (both legal in vehicle ids and GTFS ids) matches the literal text instead
// of acting as a wildcard — without it, searching "bus_1" would also match
// "bus-1", and searching "%" would return every row. Backslash is Postgres's
// default LIKE escape character.
func likeSubstringPattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

// nullableFloat converts a pgtype.Float8 to a *float64 (nil when NULL).
func nullableFloat(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// optionalFloat converts a *float64 to a pgtype.Float8 (NULL when nil).
func optionalFloat(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// rollbackTx undoes a transaction that was not committed. An already-committed
// transaction reports ErrTxClosed, which is the normal path and not an error.
func rollbackTx(tx pgx.Tx) {
	if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("failed to rollback transaction", "error", err)
	}
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}
