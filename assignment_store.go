package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrAssignmentExists   = errors.New("assignment already exists")
	ErrAssignmentNotFound = errors.New("assignment not found")
	ErrUserNotFoundFK     = errors.New("user not found")
	ErrVehicleNotFoundFK  = errors.New("vehicle not found")
	// ErrAssignmentInUse is returned when an unassign would leave the driver on
	// a running trip on the vehicle, one the location ingest would refuse every
	// report for.
	ErrAssignmentInUse = errors.New("driver has an active trip on this vehicle")
	// ErrAssignToViewer refuses a vehicle for a viewer, who the driver API
	// turns away, so the assignment could never be used.
	ErrAssignToViewer = errors.New("a viewer can't drive, so no vehicles can be assigned to one")
	// ErrViewerWithTrip refuses to make an account a viewer while it has a
	// trip running: the vehicles it drops would include the trip's.
	ErrViewerWithTrip = errors.New("end this account's running trip before making it a viewer")
)

// AssignmentResponse is the API representation of a user-vehicle assignment.
type AssignmentResponse struct {
	UserID    int64     `json:"user_id"`
	VehicleID string    `json:"vehicle_id"`
	CreatedAt time.Time `json:"created_at"`
}

// AssignmentCreator creates a user-vehicle assignment.
type AssignmentCreator interface {
	CreateAssignment(ctx context.Context, userID int64, vehicleID string) (*AssignmentResponse, error)
}

// AssignmentDeleter removes a user-vehicle assignment.
type AssignmentDeleter interface {
	DeleteAssignment(ctx context.Context, userID int64, vehicleID string) error
}

// AssignmentListerByUser lists assignments for a given user.
type AssignmentListerByUser interface {
	ListAssignmentsByUser(ctx context.Context, userID int64) ([]AssignmentResponse, error)
}

// AssignmentListerByVehicle lists assignments for a given vehicle.
type AssignmentListerByVehicle interface {
	ListAssignmentsByVehicle(ctx context.Context, vehicleID string) ([]AssignmentResponse, error)
}

// CreateAssignment inserts a user-vehicle assignment and returns the DB-generated created_at.
func (s *Store) CreateAssignment(ctx context.Context, userID int64, vehicleID string) (*AssignmentResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)

	row, err := assignVehicle(ctx, s.queries.WithTx(tx), userID, vehicleID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return &AssignmentResponse{
		UserID:    row.UserID,
		VehicleID: row.VehicleID,
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

// assignVehicle assigns vehicleID to userID through q, which the caller runs in
// a transaction so checkAssignableVehicle's lock holds until it commits, and
// records the assignment in the audit log in the same transaction.
func assignVehicle(ctx context.Context, q *db.Queries, userID int64, vehicleID string) (db.UserVehicle, error) {
	if err := checkAssignableVehicle(ctx, q, vehicleID); err != nil {
		return db.UserVehicle{}, err
	}
	row, err := insertAssignment(ctx, q, userID, vehicleID)
	if err != nil {
		return db.UserVehicle{}, err
	}
	// The role is read after the insert, whose foreign key holds a KEY SHARE
	// lock on the account. A change to viewer takes the account FOR UPDATE
	// before it drops the vehicles (dropAssignments), so either this
	// assignment commits first and is dropped with the rest, or it waits for
	// the change and reads the viewer role here. The read takes no lock of its
	// own, so an admin's edit of the account doesn't hold up an assignment.
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return db.UserVehicle{}, fmt.Errorf("read the assigned account: %w", err)
	}
	if user.Role == roleViewer {
		return db.UserVehicle{}, ErrAssignToViewer
	}
	return row, nil
}

// insertAssignment is assignVehicle's write: the assignment and its row in the
// audit log. The CSV import, which checks its vehicles once for the whole file
// and holds its accounts as drivers, assigns through it.
func insertAssignment(ctx context.Context, q *db.Queries, userID int64, vehicleID string) (db.UserVehicle, error) {
	row, err := q.AssignUserVehicle(ctx, db.AssignUserVehicleParams{
		UserID:    userID,
		VehicleID: vehicleID,
	})
	if err != nil {
		return db.UserVehicle{}, classifyAssignmentError(err)
	}
	err = recordAudit(ctx, q, auditChange{
		ObjectType: auditUser, ObjectID: strconv.FormatInt(userID, 10), Action: auditAssign,
		After: assignmentAudit{VehicleID: vehicleID},
	})
	if err != nil {
		return db.UserVehicle{}, err
	}
	return row, nil
}

// checkAssignableVehicle refuses a vehicle that doesn't exist or is
// deactivated: a driver assigned to a deactivated vehicle could do nothing
// with it. The CSV import's preview asks it too, so the preview refuses
// exactly what the assignment would.
func checkAssignableVehicle(ctx context.Context, q *db.Queries, vehicleID string) error {
	active, err := q.GetVehicleActiveForShare(ctx, vehicleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVehicleNotFoundFK
	}
	if err != nil {
		return fmt.Errorf("check vehicle active: %w", err)
	}
	if !active {
		return ErrVehicleInactive
	}
	return nil
}

// classifyAssignmentError maps pgx constraint errors to domain errors.
// Both the unique and FK paths check the constraint name explicitly: unrecognized
// constraints return a wrapped 500 rather than a misleading domain sentinel, so
// future schema additions surface as server errors instead of silent misclassifications.
func classifyAssignmentError(err error) error {
	if isUniqueViolation(err) {
		name := pgConstraintName(err)
		if name == "user_vehicles_pkey" {
			return ErrAssignmentExists
		}
		return fmt.Errorf("create assignment: unrecognized unique constraint %q: %w", name, err)
	}
	if isFKViolation(err) {
		name := pgConstraintName(err)
		switch name {
		case "user_vehicles_user_id_fkey":
			return ErrUserNotFoundFK
		case "user_vehicles_vehicle_id_fkey":
			return ErrVehicleNotFoundFK
		default:
			return fmt.Errorf("create assignment: unrecognized FK constraint %q: %w", name, err)
		}
	}
	return fmt.Errorf("create assignment: %w", err)
}

// DeleteAssignment removes a user-vehicle assignment. Returns
// ErrAssignmentNotFound if no row matched, and ErrAssignmentInUse, keeping the
// assignment, while the driver has an active trip on the vehicle.
func (s *Store) DeleteAssignment(ctx context.Context, userID int64, vehicleID string) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		return unassignVehicle(ctx, q, userID, vehicleID)
	})
}

// unassignVehicle is DeleteAssignment through q, recording the unassignment in
// the audit log in the same transaction. A change of an account to viewer
// unassigns all of its vehicles at once instead (dropAssignments), refusing
// on the same running trip.
func unassignVehicle(ctx context.Context, q *db.Queries, userID int64, vehicleID string) error {
	// The delete comes first: it waits for any trip start holding the
	// assignment, so a trip that start commits is visible to the check below.
	rowsAffected, err := q.UnassignUserVehicle(ctx, db.UnassignUserVehicleParams{
		UserID:    userID,
		VehicleID: vehicleID,
	})
	if err != nil {
		return fmt.Errorf("delete assignment: %w", err)
	}
	if rowsAffected == 0 {
		return ErrAssignmentNotFound
	}

	trip, err := q.GetActiveTripByVehicle(ctx, vehicleID)
	if err == nil && trip.UserID == userID {
		return ErrAssignmentInUse
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check active trip: %w", err)
	}

	return recordAudit(ctx, q, auditChange{
		ObjectType: auditUser, ObjectID: strconv.FormatInt(userID, 10), Action: auditUnassign,
		Before: assignmentAudit{VehicleID: vehicleID},
	})
}

// dropAssignments unassigns every vehicle from userID, in the transaction
// that makes the account a viewer, with a row in the audit log for each. It
// returns ErrViewerWithTrip, so the caller rolls back, while the account has a
// trip running on one of them.
//
// LockUser comes first, though updateUser has already read the account with
// GetUserForUpdate: that read takes it FOR NO KEY UPDATE, which lets through
// the KEY SHARE lock an assignment's insert takes, so an assignment could
// commit beside this change, unseen by the DELETE, and leave a viewer with a
// vehicle. LockUser's FOR UPDATE conflicts with KEY SHARE: it waits for an
// assignment being made right now to commit, and the DELETE then removes it;
// an assignment made after it waits in turn, and assignVehicle then reads the
// viewer role and refuses. A trip start, which takes the account FOR KEY
// SHARE first, queues against it the same way.
func dropAssignments(ctx context.Context, q *db.Queries, userID int64) error {
	if _, err := q.LockUser(ctx, userID); err != nil {
		return fmt.Errorf("lock the account: %w", err)
	}
	dropped, err := q.UnassignAllUserVehicles(ctx, userID)
	if err != nil {
		return fmt.Errorf("unassign the account's vehicles: %w", err)
	}
	// unassignVehicle's rule, for every vehicle at once: refused while the
	// account's own trip runs on one of them. An account runs one trip at a
	// time, so that is its active trip, when it is on a vehicle dropped.
	trip, err := q.GetActiveTripByUser(ctx, userID)
	if err == nil && slices.Contains(dropped, trip.VehicleID) {
		return ErrViewerWithTrip
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check active trip: %w", err)
	}
	for _, vehicleID := range dropped {
		err := recordAudit(ctx, q, auditChange{
			ObjectType: auditUser, ObjectID: strconv.FormatInt(userID, 10), Action: auditUnassign,
			Before: assignmentAudit{VehicleID: vehicleID},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ListAssignmentsByUser returns all assignments for a user, ordered by created_at DESC.
func (s *Store) ListAssignmentsByUser(ctx context.Context, userID int64) ([]AssignmentResponse, error) {
	rows, err := s.queries.ListVehiclesByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list assignments by user: %w", err)
	}
	return toAssignmentResponses(rows), nil
}

// ListAssignmentsByVehicle returns all assignments for a vehicle, ordered by created_at DESC.
func (s *Store) ListAssignmentsByVehicle(ctx context.Context, vehicleID string) ([]AssignmentResponse, error) {
	rows, err := s.queries.ListUsersByVehicle(ctx, vehicleID)
	if err != nil {
		return nil, fmt.Errorf("list assignments by vehicle: %w", err)
	}
	return toAssignmentResponses(rows), nil
}

func toAssignmentResponses(rows []db.UserVehicle) []AssignmentResponse {
	assignments := make([]AssignmentResponse, 0, len(rows))
	for _, row := range rows {
		assignments = append(assignments, AssignmentResponse{
			UserID:    row.UserID,
			VehicleID: row.VehicleID,
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return assignments
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func pgConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// Ensure Store satisfies the assignment interfaces at compile time.
var _ AssignmentCreator = (*Store)(nil)
var _ AssignmentDeleter = (*Store)(nil)
var _ AssignmentListerByUser = (*Store)(nil)
var _ AssignmentListerByVehicle = (*Store)(nil)
