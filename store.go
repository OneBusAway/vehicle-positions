package main

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store manages persistence of vehicle locations to PostgreSQL.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	// clock is what every time compared with a token's issue time is read from,
	// under the user lock: a sign-in's, a refresh's, and the time each change
	// that ends sign-ins stamps. One clock, so that skew between this server and
	// the database can't put them out of order. Nil is time.Now; a test sets it
	// off the database's clock.
	clock func() time.Time
}

// now reads the store's clock.
func (s *Store) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

// NewStore connects to PostgreSQL.
func NewStore(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool, queries: db.New(pool)}, nil
}

// errDatabaseURL repeats none of DATABASE_URL, as a URL that can't be read has
// no part that is known not to be the password.
var errDatabaseURL = errors.New("DATABASE_URL is not a valid URL; special characters in its user name and password must be percent-encoded, such as %2F for / and %40 for @")

// parseDatabaseURL is pgxpool.ParseConfig for DATABASE_URL, with errors that
// never show a password, as the server logs them. The key=value form is refused
// because Migrate takes only a URL, and names whatever is before its first
// colon, such as the start of a password, as a driver it does not know.
func parseDatabaseURL(databaseURL string) (*pgxpool.Config, error) {
	if !strings.HasPrefix(databaseURL, "postgres://") && !strings.HasPrefix(databaseURL, "postgresql://") {
		return nil, errors.New("DATABASE_URL must start with postgres:// or postgresql://")
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return nil, errDatabaseURL
	}
	// A / ? or # in the user name or password ends the host early, and what was
	// before it can still read as a host and port: u:123/secret@host is the
	// database secret@host on the host u. The @ is then left after the host.
	_, rest, _ := strings.Cut(databaseURL, "://")
	if end := strings.IndexAny(rest, "/?#"); end >= 0 && strings.Contains(rest[end:], "@") {
		return nil, errDatabaseURL
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	// pgx's error repeats the whole URL, and hides only the password before the
	// host. Its reason names the setting that is wrong, so the query, where a
	// password can be too, isn't needed. An unescaped # or & in that password
	// puts the rest of it in the fragment or under a name of its own.
	var parseErr *pgconn.ParseConfigError
	if errors.As(err, &parseErr) {
		u.RawQuery, u.Fragment = "", ""
		parseErr.ConnString = u.String()
	}
	return config, err
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
// that is not a user id: the JWT sub of every real driver is one. When the
// report is authorized it sets loc.VehicleLabel and loc.TripStartedAt from the
// vehicle and trip rows it has just read, which is how they reach the feed
// without a query per feed request.
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
	loc.VehicleLabel = auth.VehicleLabel
	loc.TripStartedAt = auth.TripStartTime.Time
	return nil
}

// SaveLocation inserts a location point, and the vehicle's row first if it has
// none, in a single transaction. A vehicle that has a row is only read, not
// rewritten by every report.
func (s *Store) SaveLocation(ctx context.Context, loc *LocationReport) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	if err := qtx.CreateVehicleIfMissing(ctx, loc.VehicleID); err != nil {
		return fmt.Errorf("create vehicle: %w", err)
	}

	if _, err := qtx.InsertLocationPoint(ctx, db.InsertLocationPointParams{
		VehicleID:       loc.VehicleID,
		TripID:          loc.TripID,
		RouteID:         loc.RouteID,
		StartDate:       loc.StartDate,
		Latitude:        loc.Latitude,
		Longitude:       loc.Longitude,
		Bearing:         optionalFloat(loc.Bearing),
		Speed:           optionalFloat(loc.Speed),
		Accuracy:        optionalFloat(loc.Accuracy),
		Timestamp:       loc.Timestamp,
		DriverID:        loc.DriverID,
		OccupancyStatus: optionalText(loc.OccupancyStatus),
	}); err != nil {
		return fmt.Errorf("insert location: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// SaveLocationHistory stores a batch of fixes a phone recorded offline, in one
// transaction, once it has checked the driver is assigned to the vehicle, or
// else leaving out those taken outside the trips the driver ran on it, and
// returns how many it stored: a fix already stored, from an upload retried
// after its reply was lost, is not stored twice. The foreign key of the
// assignment, or of the trip, guarantees the vehicle exists, so none is created.
func (s *Store) SaveLocationHistory(ctx context.Context, userID int64, vehicleID string, points []*LocationReport) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)
	qtx := s.queries.WithTx(tx)

	_, err = qtx.CheckUserVehicleAssignment(ctx, db.CheckUserVehicleAssignmentParams{UserID: userID, VehicleID: vehicleID})
	if errors.Is(err, pgx.ErrNoRows) {
		points, err = ownTripPoints(ctx, qtx, userID, vehicleID, points)
		if err != nil {
			return 0, err
		}
		if len(points) == 0 {
			return 0, ErrNotAssigned
		}
	} else if err != nil {
		return 0, fmt.Errorf("check assignment: %w", err)
	}
	saved := 0
	for _, loc := range points {
		n, err := qtx.InsertLocationPoint(ctx, db.InsertLocationPointParams{
			VehicleID:       vehicleID,
			TripID:          loc.TripID,
			RouteID:         loc.RouteID,
			StartDate:       loc.StartDate,
			Latitude:        loc.Latitude,
			Longitude:       loc.Longitude,
			Bearing:         optionalFloat(loc.Bearing),
			Speed:           optionalFloat(loc.Speed),
			Accuracy:        optionalFloat(loc.Accuracy),
			Timestamp:       loc.Timestamp,
			DriverID:        loc.DriverID,
			OccupancyStatus: optionalText(loc.OccupancyStatus),
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

// ownTripPoints is the points taken while the driver ran a trip on the vehicle,
// give or take maxTimestampSkew, as the phone's clock may differ from this one,
// which times the trips. They are the driver's to save whoever the vehicle has
// been assigned to since.
func ownTripPoints(ctx context.Context, q *db.Queries, userID int64, vehicleID string, points []*LocationReport) ([]*LocationReport, error) {
	if len(points) == 0 {
		return nil, nil
	}
	first := slices.MinFunc(points, func(a, b *LocationReport) int { return cmp.Compare(a.Timestamp, b.Timestamp) })
	spans, err := q.ListTripSpans(ctx, db.ListTripSpansParams{
		UserID: userID, VehicleID: vehicleID,
		EndedSince: pgtype.Timestamptz{Time: time.Unix(first.Timestamp, 0).Add(-maxTimestampSkew), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list trip spans: %w", err)
	}
	own := make([]*LocationReport, 0, len(points))
	for _, p := range points {
		at := time.Unix(p.Timestamp, 0)
		if slices.ContainsFunc(spans, func(s db.ListTripSpansRow) bool {
			return !at.Before(s.StartTime.Time.Add(-maxTimestampSkew)) &&
				(!s.EndTime.Valid || !at.After(s.EndTime.Time.Add(maxTimestampSkew)))
		}) {
			own = append(own, p)
		}
	}
	return own, nil
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
			VehicleID:    row.VehicleID,
			TripID:       row.TripID,
			RouteID:      row.RouteID,
			StartDate:    row.StartDate,
			Latitude:     row.Latitude,
			Longitude:    row.Longitude,
			Timestamp:    row.Timestamp,
			DriverID:     row.DriverID,
			VehicleLabel: row.VehicleLabel,
		}
		loc.Bearing = nullableFloat(row.Bearing)
		loc.Speed = nullableFloat(row.Speed)
		loc.Accuracy = nullableFloat(row.Accuracy)
		loc.OccupancyStatus = nullableText(row.OccupancyStatus)
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

// nullableText converts a pgtype.Text to a *string (nil when NULL).
func nullableText(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// optionalText converts a *string to a pgtype.Text (NULL when nil).
func optionalText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

// rollbackTx undoes a transaction that was not committed. An already-committed
// transaction reports ErrTxClosed, which is the normal path and not an error.
func rollbackTx(tx pgx.Tx) {
	if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("failed to rollback transaction", "error", err)
	}
}

// inTx runs fn in a transaction, committing it when fn returns nil and
// rolling it back otherwise. An admin change that needs no other setup runs
// through it, so the change and its audit row land together or not at all.
func (s *Store) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)
	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}
