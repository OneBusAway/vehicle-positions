package main

import (
	"context"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// VehicleResponse is the JSON representation of a vehicle.
type VehicleResponse struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	AgencyTag string    `json:"agency_tag"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// VehicleManager defines the interface for vehicle CRUD operations.
type VehicleManager interface {
	ListVehicles(ctx context.Context) ([]VehicleResponse, error)
	GetVehicle(ctx context.Context, id string) (*VehicleResponse, error)
	UpsertVehicle(ctx context.Context, id, label, agencyTag string) (*VehicleResponse, error)
	DeactivateVehicle(ctx context.Context, id string) error
}

// VehicleInfoUpdater updates a vehicle's label/agency tag without touching
// its active flag (unlike VehicleManager.UpsertVehicle, which reactivates).
type VehicleInfoUpdater interface {
	UpdateVehicleInfo(ctx context.Context, id, label, agencyTag string) error
}

// VehicleActivator toggles a vehicle's active flag (deactivate/reactivate).
type VehicleActivator interface {
	SetVehicleActive(ctx context.Context, id string, active bool) error
}

// VehicleCreator inserts brand-new vehicles only. Unlike
// VehicleManager.UpsertVehicle it never overwrites or reactivates an
// existing row: created is false (with no error) when the id already exists.
type VehicleCreator interface {
	CreateVehicle(ctx context.Context, id, label, agencyTag string) (created bool, err error)
}

// ListVehicles returns all vehicles ordered by creation time.
func (s *Store) ListVehicles(ctx context.Context) ([]VehicleResponse, error) {
	rows, err := s.queries.ListVehicles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}

	vehicles := make([]VehicleResponse, 0, len(rows))
	for _, row := range rows {
		vehicles = append(vehicles, toVehicleResponse(row.ID, row.Label, row.AgencyTag, row.Active, row.CreatedAt, row.UpdatedAt))
	}
	return vehicles, nil
}

// GetVehicle returns a single vehicle by ID.
func (s *Store) GetVehicle(ctx context.Context, id string) (*VehicleResponse, error) {
	row, err := s.queries.GetVehicleByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	v := toVehicleResponse(row.ID, row.Label, row.AgencyTag, row.Active, row.CreatedAt, row.UpdatedAt)
	return &v, nil
}

// UpsertVehicle creates a new vehicle or updates an existing one.
func (s *Store) UpsertVehicle(ctx context.Context, id, label, agencyTag string) (*VehicleResponse, error) {
	row, err := s.queries.UpsertAdminVehicle(ctx, db.UpsertAdminVehicleParams{
		ID:        id,
		Label:     label,
		AgencyTag: agencyTag,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert vehicle: %w", err)
	}
	v := toVehicleResponse(row.ID, row.Label, row.AgencyTag, row.Active, row.CreatedAt, row.UpdatedAt)
	return &v, nil
}

// CreateVehicle inserts a new vehicle, reporting created=false (and no
// error) when a vehicle with the same id already exists. The single
// ON CONFLICT DO NOTHING insert makes concurrent duplicate creates safe:
// exactly one wins, and the loser can't overwrite or reactivate the row the
// way a check-then-upsert sequence could.
func (s *Store) CreateVehicle(ctx context.Context, id, label, agencyTag string) (bool, error) {
	rows, err := s.queries.CreateVehicle(ctx, db.CreateVehicleParams{
		ID:        id,
		Label:     label,
		AgencyTag: agencyTag,
	})
	if err != nil {
		return false, fmt.Errorf("create vehicle: %w", err)
	}
	return rows > 0, nil
}

// DeactivateVehicle sets a vehicle's active flag to false. It delegates to
// SetVehicleActive so the active flag has a single write path.
func (s *Store) DeactivateVehicle(ctx context.Context, id string) error {
	return s.SetVehicleActive(ctx, id, false)
}

// UpdateVehicleInfo updates label/agency tag WITHOUT touching the active flag,
// unlike UpsertVehicle which force-reactivates.
func (s *Store) UpdateVehicleInfo(ctx context.Context, id, label, agencyTag string) error {
	rows, err := s.queries.UpdateVehicleInfo(ctx, db.UpdateVehicleInfoParams{ID: id, Label: label, AgencyTag: agencyTag})
	if err != nil {
		return fmt.Errorf("update vehicle info: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("update vehicle info: %w", pgx.ErrNoRows)
	}
	return nil
}

// SetVehicleActive flips a vehicle's active flag (deactivate/reactivate).
func (s *Store) SetVehicleActive(ctx context.Context, id string, active bool) error {
	rows, err := s.queries.SetVehicleActive(ctx, db.SetVehicleActiveParams{ID: id, Active: active})
	if err != nil {
		return fmt.Errorf("set vehicle active: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("set vehicle active: %w", pgx.ErrNoRows)
	}
	return nil
}

// DriverVehicleLister lists the active vehicles assigned to a driver.
type DriverVehicleLister interface {
	ListActiveVehiclesByUser(ctx context.Context, userID int64) ([]VehicleResponse, error)
}

// ListActiveVehiclesByUser returns the active vehicles assigned to the given user.
func (s *Store) ListActiveVehiclesByUser(ctx context.Context, userID int64) ([]VehicleResponse, error) {
	rows, err := s.queries.ListActiveVehiclesByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list active vehicles by user: %w", err)
	}
	vehicles := make([]VehicleResponse, 0, len(rows))
	for _, row := range rows {
		vehicles = append(vehicles, toVehicleResponse(row.ID, row.Label, row.AgencyTag, row.Active, row.CreatedAt, row.UpdatedAt))
	}
	return vehicles, nil
}

var _ DriverVehicleLister = (*Store)(nil)

// toVehicleResponse maps a DB row to the API response type.
// created_at and updated_at are NOT NULL in the schema, so .Valid is always true.
func toVehicleResponse(id, label, agencyTag string, active bool, createdAt, updatedAt pgtype.Timestamptz) VehicleResponse {
	return VehicleResponse{
		ID:        id,
		Label:     label,
		AgencyTag: agencyTag,
		Active:    active,
		CreatedAt: createdAt.Time,
		UpdatedAt: updatedAt.Time,
	}
}
