package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/OneBusAway/vehicle-positions/rider"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrRiderNotFound is returned when no rider matches the requested id.
var ErrRiderNotFound = errors.New("rider not found")

// ErrRideNotFound is returned when no ride matches the requested id, or when
// the ride exists but is no longer active.
var ErrRideNotFound = errors.New("ride not found")

// ErrActiveRideExists is returned when starting a ride would give a rider a
// second active one. Only a start racing another process can reach it: within
// one process riderLock has already serialised the supersede.
var ErrActiveRideExists = errors.New("rider already has an active ride")

// Rider is the stored representation of a pseudonymous rider installation.
type Rider struct {
	ID                string
	InstallationID    string
	Platform          string
	AppID             string
	AppVersion        string
	Attested          bool
	Score             int
	Tier              string // "new" | "trusted" | "blocked"
	RidesTotal        int
	RidesCorroborated int
	RidesRejected     int
	CreatedAt         time.Time
	LastSeenAt        time.Time
}

// Ride is the stored representation of one rider's trip instance.
type Ride struct {
	ID                 string
	RiderID            string
	TripID             string
	StartDate          string
	RouteID            string
	VehicleID          string
	BoardingStopID     string
	DestinationStopID  string
	Status             string // "active" | "ended"
	State              string // "pending" | "verified" | "rejected"
	Corroborated       bool
	EndReason          string
	PointsTotal        int
	PointsMatched      int
	PointsCorroborated int
	PointsContradicted int
	StartedAt          time.Time
	EndedAt            *time.Time
}

// RideProgress is the verification state of a ride at the moment points are
// recorded or the ride ends.
type RideProgress struct {
	State              string
	Corroborated       bool
	PointsTotal        int
	PointsMatched      int
	PointsCorroborated int
	PointsContradicted int
}

// RidePointRecord is one verified rider position, as stored. Points are
// write-only: nothing reads them back except retention pruning.
type RidePointRecord struct {
	Latitude                 float64
	Longitude                float64
	Accuracy                 *float64
	Speed                    *float64
	Bearing                  *float64
	Timestamp                int64
	Outcome                  string
	Corroboration            string
	AlongShape               float64
	DistanceToShape          float64
	ScheduleDeviationSeconds int
}

// RideOutcome is what finishing a ride does to the ride and to its rider.
type RideOutcome struct {
	EndReason    string
	Progress     RideProgress
	ScoreDelta   int
	Rejected     bool // increments rides_rejected
	Corroborated bool // increments rides_corroborated
}

// RiderRegistrar creates (or refreshes) the rider behind an installation id.
type RiderRegistrar interface {
	RegisterRider(ctx context.Context, installationID, platform, appID, appVersion string) (r *Rider, created bool, err error)
}

// RiderReader looks up a single rider.
type RiderReader interface {
	GetRider(ctx context.Context, id string) (*Rider, error)
}

// RideStarter opens a ride. The caller sets the ride's ID; the server-assigned
// columns are written back onto the passed ride.
type RideStarter interface {
	StartRide(ctx context.Context, ride *Ride) error
}

// RidePointRecorder persists a batch of verified points and the ride progress
// they produced.
type RidePointRecorder interface {
	RecordRidePoints(ctx context.Context, rideID, riderID string, points []RidePointRecord, progress RideProgress) error
}

// RideFinisher ends rides: one at a time with a reputation outcome, or all
// active ones at once (server restart).
type RideFinisher interface {
	FinishRide(ctx context.Context, rideID string, outcome RideOutcome) (*Rider, error)
	EndAllActiveRides(ctx context.Context, reason string) (int64, error)
}

// RideLister lists rides by status, newest first.
type RideLister interface {
	ListRides(ctx context.Context, status string, limit, offset int) ([]Ride, error)
}

// RiderStatsReader reports rider counts per tier for the admin status endpoint.
type RiderStatsReader interface {
	CountRidersByTier(ctx context.Context) (map[string]int, error)
}

// RidePointPruner enforces the ride-point retention window.
type RidePointPruner interface {
	DeleteRidePointsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// RegisterRider returns the rider for installationID, creating one on first
// sight and refreshing the app metadata (and last_seen_at) on every later
// call. created reports whether this call inserted the rider.
func (s *Store) RegisterRider(ctx context.Context, installationID, platform, appID, appVersion string) (*Rider, bool, error) {
	row, err := s.queries.UpsertRider(ctx, db.UpsertRiderParams{
		ID:             uuid.NewString(),
		InstallationID: installationID,
		Platform:       platform,
		AppID:          appID,
		AppVersion:     appVersion,
	})
	if err != nil {
		return nil, false, fmt.Errorf("upsert rider: %w", err)
	}
	// UpsertRider embeds the whole rider row beside the synthetic "created"
	// flag, so a new column reaches here without a second mapping to forget.
	return riderFromRow(row.Rider), row.Created, nil
}

// GetRider looks up a rider by id, returning ErrRiderNotFound when absent.
func (s *Store) GetRider(ctx context.Context, id string) (*Rider, error) {
	row, err := s.queries.GetRider(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRiderNotFound
		}
		return nil, fmt.Errorf("get rider: %w", err)
	}
	return riderFromRow(row), nil
}

// StartRide inserts a ride and fills in the columns the database assigns
// (status, state and started_at) on the passed ride.
func (s *Store) StartRide(ctx context.Context, ride *Ride) error {
	row, err := s.queries.InsertRide(ctx, db.InsertRideParams{
		ID:                ride.ID,
		RiderID:           ride.RiderID,
		TripID:            ride.TripID,
		StartDate:         ride.StartDate,
		RouteID:           ride.RouteID,
		VehicleID:         ride.VehicleID,
		BoardingStopID:    ride.BoardingStopID,
		DestinationStopID: ride.DestinationStopID,
	})
	if err != nil {
		// The partial unique index idx_rides_one_active_per_rider catches a
		// concurrent start from another instance, which riderLock cannot see.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "idx_rides_one_active_per_rider" {
			return ErrActiveRideExists
		}
		return fmt.Errorf("insert ride: %w", err)
	}
	*ride = *rideFromRow(row)
	return nil
}

// RecordRidePoints stores a batch of points, updates the ride's progress and
// touches the rider, all in one transaction.
func (s *Store) RecordRidePoints(ctx context.Context, rideID, riderID string, points []RidePointRecord, progress RideProgress) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)

	qtx := s.queries.WithTx(tx)

	// One round trip for the whole batch: sqlc's :batchexec queues every insert
	// on a pgx.Batch and sends it on the transaction. The results must be
	// drained and closed before any further query runs on the same connection.
	if len(points) > 0 {
		params := make([]db.InsertRidePointParams, 0, len(points))
		for _, p := range points {
			params = append(params, db.InsertRidePointParams{
				RideID:                   rideID,
				Latitude:                 p.Latitude,
				Longitude:                p.Longitude,
				Accuracy:                 optionalFloat(p.Accuracy),
				Speed:                    optionalFloat(p.Speed),
				Bearing:                  optionalFloat(p.Bearing),
				Timestamp:                p.Timestamp,
				Outcome:                  p.Outcome,
				Corroboration:            p.Corroboration,
				AlongShape:               pgtype.Float8{Float64: p.AlongShape, Valid: true},
				DistanceToShape:          pgtype.Float8{Float64: p.DistanceToShape, Valid: true},
				ScheduleDeviationSeconds: pgtype.Int4{Int32: int32(p.ScheduleDeviationSeconds), Valid: true},
			})
		}
		results := qtx.InsertRidePoint(ctx, params)
		var insertErr error
		results.Exec(func(_ int, err error) {
			if err != nil && insertErr == nil {
				insertErr = err
			}
		})
		if err := results.Close(); err != nil && insertErr == nil {
			insertErr = err
		}
		if insertErr != nil {
			return fmt.Errorf("insert ride points: %w", insertErr)
		}
	}

	// No row updated means the ride ended between this batch arriving and the
	// transaction reaching here. InsertRidePoint's EXISTS guard already dropped
	// the points, so committing would only bump last_seen_at for a ride nobody
	// can add to; rolling back and reporting it lets the caller answer 409.
	updated, err := qtx.UpdateRideProgress(ctx, db.UpdateRideProgressParams{
		ID:                 rideID,
		State:              progress.State,
		Corroborated:       progress.Corroborated,
		PointsTotal:        int32(progress.PointsTotal),
		PointsMatched:      int32(progress.PointsMatched),
		PointsCorroborated: int32(progress.PointsCorroborated),
		PointsContradicted: int32(progress.PointsContradicted),
	})
	if err != nil {
		return fmt.Errorf("update ride progress: %w", err)
	}
	if updated == 0 {
		return ErrRideNotFound
	}

	if err := qtx.TouchRider(ctx, riderID); err != nil {
		return fmt.Errorf("touch rider: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// FinishRide ends an active ride and applies its outcome to the rider's
// reputation, returning the rider as it stands afterwards. A ride that does
// not exist, or has already ended, yields ErrRideNotFound.
func (s *Store) FinishRide(ctx context.Context, rideID string, outcome RideOutcome) (*Rider, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)

	qtx := s.queries.WithTx(tx)

	riderID, err := qtx.EndRide(ctx, db.EndRideParams{
		ID:                 rideID,
		EndReason:          outcome.EndReason,
		State:              outcome.Progress.State,
		Corroborated:       outcome.Progress.Corroborated,
		PointsTotal:        int32(outcome.Progress.PointsTotal),
		PointsMatched:      int32(outcome.Progress.PointsMatched),
		PointsCorroborated: int32(outcome.Progress.PointsCorroborated),
		PointsContradicted: int32(outcome.Progress.PointsContradicted),
	})
	if err != nil {
		// No row came back: the ride does not exist, or has already ended.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRideNotFound
		}
		return nil, fmt.Errorf("end ride: %w", err)
	}

	row, err := qtx.ApplyRideOutcome(ctx, db.ApplyRideOutcomeParams{
		ID:                riderID,
		Score:             int32(outcome.ScoreDelta),
		RidesCorroborated: boolCount(outcome.Corroborated),
		RidesRejected:     boolCount(outcome.Rejected),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRiderNotFound
		}
		return nil, fmt.Errorf("apply ride outcome: %w", err)
	}

	// The tier rule has one definition, in the engine.
	tier := string(rider.TierFor(int(row.Score)))
	if err := qtx.SetRiderTier(ctx, db.SetRiderTierParams{ID: row.ID, Tier: tier}); err != nil {
		return nil, fmt.Errorf("set rider tier: %w", err)
	}
	row.Tier = tier

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return riderFromRow(row), nil
}

// EndAllActiveRides closes every active ride with the given reason, without
// touching rider reputation. Used at startup, when in-memory ride state is
// gone but the rows still say "active".
func (s *Store) EndAllActiveRides(ctx context.Context, reason string) (int64, error) {
	n, err := s.queries.EndAllActiveRides(ctx, reason)
	if err != nil {
		return 0, fmt.Errorf("end all active rides: %w", err)
	}
	return n, nil
}

// ListRides returns rides with the given status, newest first.
func (s *Store) ListRides(ctx context.Context, status string, limit, offset int) ([]Ride, error) {
	rows, err := s.queries.ListRides(ctx, db.ListRidesParams{
		Status: status,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("list rides: %w", err)
	}

	rides := make([]Ride, 0, len(rows))
	for _, row := range rows {
		rides = append(rides, *rideFromRow(row))
	}
	return rides, nil
}

// CountRidersByTier returns the number of riders in each tier. Tiers with no
// riders are absent from the map.
func (s *Store) CountRidersByTier(ctx context.Context) (map[string]int, error) {
	rows, err := s.queries.CountRidersByTier(ctx)
	if err != nil {
		return nil, fmt.Errorf("count riders by tier: %w", err)
	}

	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.Tier] = int(row.Count)
	}
	return counts, nil
}

// DeleteRidePointsBefore removes ride points received before cutoff and
// returns how many were deleted.
func (s *Store) DeleteRidePointsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.queries.DeleteRidePointsBefore(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("delete ride points: %w", err)
	}
	return n, nil
}

// riderFromRow maps a stored rider onto the API/domain representation.
func riderFromRow(row db.Rider) *Rider {
	return &Rider{
		ID:                row.ID,
		InstallationID:    row.InstallationID,
		Platform:          row.Platform,
		AppID:             row.AppID,
		AppVersion:        row.AppVersion,
		Attested:          row.Attested,
		Score:             int(row.Score),
		Tier:              row.Tier,
		RidesTotal:        int(row.RidesTotal),
		RidesCorroborated: int(row.RidesCorroborated),
		RidesRejected:     int(row.RidesRejected),
		CreatedAt:         row.CreatedAt.Time,
		LastSeenAt:        row.LastSeenAt.Time,
	}
}

// rideFromRow maps a stored ride onto the API/domain representation.
func rideFromRow(row db.Ride) *Ride {
	ride := &Ride{
		ID:                 row.ID,
		RiderID:            row.RiderID,
		TripID:             row.TripID,
		StartDate:          row.StartDate,
		RouteID:            row.RouteID,
		VehicleID:          row.VehicleID,
		BoardingStopID:     row.BoardingStopID,
		DestinationStopID:  row.DestinationStopID,
		Status:             row.Status,
		State:              row.State,
		Corroborated:       row.Corroborated,
		EndReason:          row.EndReason,
		PointsTotal:        int(row.PointsTotal),
		PointsMatched:      int(row.PointsMatched),
		PointsCorroborated: int(row.PointsCorroborated),
		PointsContradicted: int(row.PointsContradicted),
		StartedAt:          row.StartedAt.Time,
	}
	if row.EndedAt.Valid {
		endedAt := row.EndedAt.Time
		ride.EndedAt = &endedAt
	}
	return ride
}

// boolCount turns a "did this happen" flag into the 0 or 1 a counter column
// is incremented by.
func boolCount(v bool) int32 {
	if v {
		return 1
	}
	return 0
}
