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

// DeactivateVehicle sets a vehicle's active flag to false.
func (s *Store) DeactivateVehicle(ctx context.Context, id string) error {
	rowsAffected, err := s.queries.DeactivateVehicle(ctx, id)
	if err != nil {
		return fmt.Errorf("deactivate vehicle: %w", err)
	}
	// DeactivateVehicle uses :execrows, which returns the count of affected rows
	// instead of the row itself. A zero count means no vehicle matched the ID.
	if rowsAffected == 0 {
		return fmt.Errorf("deactivate vehicle: %w", pgx.ErrNoRows)
	}
	return nil
}

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
