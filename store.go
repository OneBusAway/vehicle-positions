package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

// ErrVehicleInactive is returned when a driver reports a position for a
// vehicle an admin has deactivated.
var ErrVehicleInactive = errors.New("vehicle is inactive")

// ErrNoActiveTrip is returned when a driver reports a position for a vehicle
// they have no active trip on.
var ErrNoActiveTrip = errors.New("driver has no active trip on this vehicle")

// ErrTripMismatch is returned when a report names a trip_id or route_id other
// than the one the driver's active trip was started with.
var ErrTripMismatch = errors.New("trip_id or route_id does not match the driver's active trip")

// AuthorizeLocation reports whether loc.DriverID may publish loc: the vehicle
// must exist and be active, the driver must be assigned to it, and the driver
// must have an active trip on it whose trip_id and route_id the report does
// not contradict. A report may leave either field empty. An unknown vehicle is
// ErrNotAssigned, since no driver can be assigned to it, and so is a DriverID
// that is not a user id: the JWT sub of every real driver is one.
func (s *Store) AuthorizeLocation(ctx context.Context, loc *LocationReport) error {
	userID, err := strconv.ParseInt(loc.DriverID, 10, 64)
	if err != nil {
		return ErrNotAssigned
	}
	auth, err := s.queries.GetLocationAuthorization(ctx, db.GetLocationAuthorizationParams{
		UserID:    userID,
		VehicleID: loc.VehicleID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotAssigned
	}
	if err != nil {
		return fmt.Errorf("get location authorization: %w", err)
	}
	switch {
	case !auth.Assigned:
		return ErrNotAssigned
	case !auth.VehicleActive:
		return ErrVehicleInactive
	case !auth.HasActiveTrip:
		return ErrNoActiveTrip
	case loc.TripID != "" && loc.TripID != strings.TrimSpace(auth.TripGtfsTripID),
		loc.RouteID != "" && loc.RouteID != strings.TrimSpace(auth.TripRouteID):
		return ErrTripMismatch
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
