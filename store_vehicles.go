package main

import (
	"context"
	"fmt"
	"strings"
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

// VehiclePager lists vehicles one page at a time, for the paginated admin
// vehicle list and the vehicles API endpoint. Callers that need the whole
// fleet in a single call — the dashboard's label map, the assignment and
// trip-filter dropdowns, the live map — keep using
// VehicleManager.ListVehicles, which pagination would silently truncate.
type VehiclePager interface {
	ListVehiclesPage(ctx context.Context, f VehicleFilter) ([]VehicleResponse, error)
}

// VehicleFilter narrows the vehicle list. Zero values mean "no filter", so a
// zero-value filter lists the active fleet exactly as the unfiltered page
// always has.
type VehicleFilter struct {
	IncludeInactive bool   // false hides deactivated vehicles
	AgencyTag       string // "" = all agencies
	Q               string // ILIKE substring on id and label
	Limit           int32  // callers pass limit+1 to detect hasMore
	Offset          int32
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

// ListVehicles returns vehicles ordered by creation time, capped at the
// query's 1000-row safety bound. Callers that page through the fleet use
// ListVehiclesPage instead.
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

// ListVehiclesPage returns one page of vehicles in the same order as
// ListVehicles, narrowed by f. Every filter runs in SQL rather than after
// the fetch, so a page of the admin list holds a full page of rows.
//
// Dynamic WHERE clauses make this a hand-written query rather than sqlc, the
// same trade ListTrips documents: three optional filters would be eight sqlc
// variants. Every value goes through arg(), so only $N placeholders ever
// reach the SQL string.
func (s *Store) ListVehiclesPage(ctx context.Context, f VehicleFilter) ([]VehicleResponse, error) {
	query := `
		SELECT id, label, agency_tag, active, created_at, updated_at
		FROM vehicles`
	var conds []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if !f.IncludeInactive {
		conds = append(conds, "active")
	}
	if f.AgencyTag != "" {
		conds = append(conds, "agency_tag = "+arg(f.AgencyTag))
	}
	if f.Q != "" {
		p := arg(likeSubstringPattern(f.Q))
		conds = append(conds, fmt.Sprintf("(id ILIKE %s OR label ILIKE %s)", p, p))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT " + arg(f.Limit) + " OFFSET " + arg(f.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list vehicles page: %w", err)
	}
	defer rows.Close()

	vehicles := make([]VehicleResponse, 0)
	for rows.Next() {
		var id, label, agencyTag string
		var active bool
		var createdAt, updatedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &label, &agencyTag, &active, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan vehicle: %w", err)
		}
		vehicles = append(vehicles, toVehicleResponse(id, label, agencyTag, active, createdAt, updatedAt))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list vehicles page: %w", err)
	}
	return vehicles, nil
}

var _ VehiclePager = (*Store)(nil)

// GetVehicle returns a single vehicle by ID.
func (s *Store) GetVehicle(ctx context.Context, id string) (*VehicleResponse, error) {
	row, err := s.queries.GetVehicleByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	v := toVehicleResponse(row.ID, row.Label, row.AgencyTag, row.Active, row.CreatedAt, row.UpdatedAt)
	return &v, nil
}

// UpsertVehicle creates a new vehicle or updates an existing one, and
// reactivates it, recording which in the audit log.
func (s *Store) UpsertVehicle(ctx context.Context, id, label, agencyTag string) (*VehicleResponse, error) {
	var v VehicleResponse
	err := s.inTx(ctx, func(q *db.Queries) error {
		change := auditChange{ObjectType: auditVehicle, ObjectID: id, Action: auditCreate}
		// An insert racing another on the same id waits for it, so when this
		// one inserts nothing the vehicle is there to lock and read.
		inserted, err := q.CreateVehicle(ctx, db.CreateVehicleParams{ID: id, Label: label, AgencyTag: agencyTag})
		if err != nil {
			return fmt.Errorf("upsert vehicle: %w", err)
		}
		if inserted == 0 {
			old, err := q.GetVehicleForUpdate(ctx, id)
			if err != nil {
				return fmt.Errorf("upsert vehicle: %w", err)
			}
			change.Action, change.Before = auditUpdate, vehicleAudit(old)
		}
		// On a vehicle just inserted this writes the same fields again; it is
		// what returns the row either way.
		row, err := q.UpsertAdminVehicle(ctx, db.UpsertAdminVehicleParams{
			ID:        id,
			Label:     label,
			AgencyTag: agencyTag,
		})
		if err != nil {
			return fmt.Errorf("upsert vehicle: %w", err)
		}
		change.After = vehicleAudit{Label: row.Label, AgencyTag: row.AgencyTag, Active: row.Active}
		v = toVehicleResponse(row.ID, row.Label, row.AgencyTag, row.Active, row.CreatedAt, row.UpdatedAt)
		return recordAudit(ctx, q, change)
	})
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateVehicle inserts a new vehicle, reporting created=false (and no
// error) when a vehicle with the same id already exists. The single
// ON CONFLICT DO NOTHING insert makes concurrent duplicate creates safe:
// exactly one wins, and the loser can't overwrite or reactivate the row the
// way a check-then-upsert sequence could.
func (s *Store) CreateVehicle(ctx context.Context, id, label, agencyTag string) (bool, error) {
	var created bool
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		created, err = createVehicle(ctx, q, id, label, agencyTag)
		return err
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// createVehicle is CreateVehicle through q, recording the new vehicle in the
// audit log. The vehicle form and the CSV import both create through it.
func createVehicle(ctx context.Context, q *db.Queries, id, label, agencyTag string) (bool, error) {
	rows, err := q.CreateVehicle(ctx, db.CreateVehicleParams{
		ID:        id,
		Label:     label,
		AgencyTag: agencyTag,
	})
	if err != nil {
		return false, fmt.Errorf("create vehicle: %w", err)
	}
	if rows == 0 {
		return false, nil
	}
	// A new vehicle is active: the column's default.
	err = recordAudit(ctx, q, auditChange{
		ObjectType: auditVehicle, ObjectID: id, Action: auditCreate,
		After: vehicleAudit{Label: label, AgencyTag: agencyTag, Active: true},
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// DeactivateVehicle sets a vehicle's active flag to false. It delegates to
// SetVehicleActive so the active flag has a single write path.
func (s *Store) DeactivateVehicle(ctx context.Context, id string) error {
	return s.SetVehicleActive(ctx, id, false)
}

// UpdateVehicleInfo updates label/agency tag WITHOUT touching the active flag,
// unlike UpsertVehicle which force-reactivates.
func (s *Store) UpdateVehicleInfo(ctx context.Context, id, label, agencyTag string) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		return updateVehicleInfo(ctx, q, id, label, agencyTag)
	})
}

// updateVehicleInfo is UpdateVehicleInfo through q, recording what changed in
// the audit log. The vehicle form and the CSV import both update through it.
// It returns pgx.ErrNoRows, wrapped, when no vehicle has the id.
func updateVehicleInfo(ctx context.Context, q *db.Queries, id, label, agencyTag string) error {
	old, err := q.GetVehicleForUpdate(ctx, id)
	if err != nil {
		return fmt.Errorf("update vehicle info: %w", err)
	}
	rows, err := q.UpdateVehicleInfo(ctx, db.UpdateVehicleInfoParams{ID: id, Label: label, AgencyTag: agencyTag})
	if err != nil {
		return fmt.Errorf("update vehicle info: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("update vehicle info: %w", pgx.ErrNoRows)
	}
	return recordAudit(ctx, q, auditChange{
		ObjectType: auditVehicle, ObjectID: id, Action: auditUpdate,
		Before: vehicleAudit(old),
		After:  vehicleAudit{Label: label, AgencyTag: agencyTag, Active: old.Active},
	})
}

// SetVehicleActive flips a vehicle's active flag (deactivate/reactivate).
func (s *Store) SetVehicleActive(ctx context.Context, id string, active bool) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		old, err := q.GetVehicleForUpdate(ctx, id)
		if err != nil {
			return fmt.Errorf("set vehicle active: %w", err)
		}
		rows, err := q.SetVehicleActive(ctx, db.SetVehicleActiveParams{ID: id, Active: active})
		if err != nil {
			return fmt.Errorf("set vehicle active: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("set vehicle active: %w", pgx.ErrNoRows)
		}
		after := vehicleAudit(old)
		after.Active = active
		return recordAudit(ctx, q, auditChange{
			ObjectType: auditVehicle, ObjectID: id, Action: activeAction(active),
			Before: vehicleAudit(old), After: after,
		})
	})
}

// activeAction is the audit action for setting an active flag to active.
func activeAction(active bool) string {
	if active {
		return auditActivate
	}
	return auditDeactivate
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
