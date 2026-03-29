package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrAssignmentExists   = errors.New("assignment already exists")
	ErrAssignmentNotFound = errors.New("assignment not found")
	ErrUserNotFoundFK     = errors.New("user not found")
	ErrVehicleNotFoundFK  = errors.New("vehicle not found")
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
	row, err := s.queries.AssignUserVehicle(ctx, db.AssignUserVehicleParams{
		UserID:    userID,
		VehicleID: vehicleID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAssignmentExists
		}
		if isFKViolation(err) {
			switch fkConstraintName(err) {
			case "user_vehicles_user_id_fkey":
				return nil, ErrUserNotFoundFK
			case "user_vehicles_vehicle_id_fkey":
				return nil, ErrVehicleNotFoundFK
			default:
				return nil, fmt.Errorf("create assignment: unrecognized FK constraint %q: %w", fkConstraintName(err), err)
			}
		}
		return nil, fmt.Errorf("create assignment: %w", err)
	}

	return &AssignmentResponse{
		UserID:    row.UserID,
		VehicleID: row.VehicleID,
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

// DeleteAssignment removes a user-vehicle assignment. Returns ErrAssignmentNotFound if no row matched.
func (s *Store) DeleteAssignment(ctx context.Context, userID int64, vehicleID string) error {
	rowsAffected, err := s.queries.UnassignUserVehicle(ctx, db.UnassignUserVehicleParams{
		UserID:    userID,
		VehicleID: vehicleID,
	})
	if err != nil {
		return fmt.Errorf("delete assignment: %w", err)
	}
	if rowsAffected == 0 {
		return ErrAssignmentNotFound
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

func fkConstraintName(err error) string {
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
