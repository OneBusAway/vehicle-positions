package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/OneBusAway/vehicle-positions/db"
)

// The kinds of object the audit log records changes to, as its object_type
// column names them.
const (
	auditVehicle = "vehicle"
	auditUser    = "user"
	auditTrip    = "trip"
	auditAPIKey  = "api_key"
)

// The changes the audit log records, as its action column names them.
const (
	auditCreate     = "create"
	auditUpdate     = "update"
	auditActivate   = "activate"
	auditDeactivate = "deactivate"
	auditDelete     = "delete"
	auditPassword   = "set_password"
	auditAssign     = "assign_vehicle"
	auditUnassign   = "unassign_vehicle"
	auditEnd        = "end"
)

// The fields of each kind of object the audit log keeps before and after a
// change. They are the small ones an admin edits, never a password or a key.

type vehicleAudit struct {
	Label     string `json:"label"`
	AgencyTag string `json:"agency_tag"`
	Active    bool   `json:"active"`
}

type userAudit struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

type apiKeyAudit struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type tripAudit struct {
	Status string `json:"status"`
}

// assignmentAudit is a vehicle assigned to the user the row is about.
type assignmentAudit struct {
	VehicleID string `json:"vehicle_id"`
}

// auditActor is who a change is recorded against: the signed-in admin, and
// the address their request came from.
type auditActor struct {
	UserID int64
	Email  string
	IP     string
}

const auditActorKey contextKey = "audit_actor"

// withAuditActor returns r with the admin its claims name as the actor of
// every change it makes. requireAdmin and requireAdminPage call it, so every
// admin route has one, and the store reads it inside the transaction that
// makes the change. The address is read as the login rate limiter reads it.
func withAuditActor(r *http.Request, claims jwt.MapClaims, trustProxy bool) (*http.Request, error) {
	sub, _ := claims["sub"].(string)
	id, err := strconv.ParseInt(sub, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("token subject %q is not a user id", sub)
	}
	email, _ := claims["email"].(string)
	actor := auditActor{UserID: id, Email: email, IP: clientIP(r, trustProxy)}
	return r.WithContext(context.WithValue(r.Context(), auditActorKey, actor)), nil
}

// auditChange is one change to one object, as the audit log records it.
type auditChange struct {
	ObjectType string
	ObjectID   string
	Action     string
	// Before and After are the object's fields as they were and as they
	// became: nil before a create, after a delete, and for a change that has
	// no field to show, such as a new password.
	Before, After any
}

// recordAudit writes c to the audit log through q, which must be the
// transaction that makes the change, so the two commit or roll back together.
// The actor is the one withAuditActor put on ctx; a change made with none
// there was made by the server itself, at startup or by the reset-admin
// command. A change that left every field as it was is not recorded.
func recordAudit(ctx context.Context, q *db.Queries, c auditChange) error {
	before, err := auditJSON(c.Before)
	if err != nil {
		return err
	}
	after, err := auditJSON(c.After)
	if err != nil {
		return err
	}
	if before != nil && after != nil && bytes.Equal(before, after) {
		return nil
	}
	params := db.InsertAuditEntryParams{
		ObjectType: c.ObjectType,
		ObjectID:   c.ObjectID,
		Action:     c.Action,
		Before:     before,
		After:      after,
	}
	if actor, ok := ctx.Value(auditActorKey).(auditActor); ok {
		params.ActorID = pgtype.Int8{Int64: actor.UserID, Valid: true}
		params.ActorEmail = actor.Email
		params.ActorIp = actor.IP
	}
	if err := q.InsertAuditEntry(ctx, params); err != nil {
		return fmt.Errorf("record %s %s %s: %w", c.ObjectType, c.ObjectID, c.Action, err)
	}
	return nil
}

// auditJSON is fields as the audit log stores them, nil for none.
func auditJSON(fields any) ([]byte, error) {
	if fields == nil {
		return nil, nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode audit fields: %w", err)
	}
	return b, nil
}

// auditEntry is one row of the audit log.
type auditEntry struct {
	ID int64
	At time.Time
	// ActorID is nil for a change the server made itself.
	ActorID    *int64
	ActorEmail string
	ActorIP    string
	ObjectType string
	ObjectID   string
	Action     string
	// Before and After are nil where the row has none.
	Before json.RawMessage
	After  json.RawMessage
	// AccountName is the name the account the row is about has now: "" for
	// a row about something else, or about an account since deleted.
	AccountName string
}

// auditFilter narrows the audit log to one kind of object, one object of
// that kind, or the changes one admin made. An empty field, or a nil ActorID,
// leaves the log unfiltered by it.
type auditFilter struct {
	ObjectType string
	ObjectID   string
	ActorID    *int64
	Limit      int
	Offset     int
}

// AuditLogLister reads the audit log, newest first.
type AuditLogLister interface {
	ListAuditEntries(ctx context.Context, f auditFilter) ([]auditEntry, error)
}

var _ AuditLogLister = (*Store)(nil)

// ListAuditEntries returns up to f.Limit rows of the audit log that f
// selects, newest first, skipping the first f.Offset.
func (s *Store) ListAuditEntries(ctx context.Context, f auditFilter) ([]auditEntry, error) {
	params := db.ListAuditEntriesParams{RowLimit: int32(f.Limit), RowOffset: int32(f.Offset)}
	if f.ObjectType != "" {
		params.ObjectType = pgtype.Text{String: f.ObjectType, Valid: true}
	}
	if f.ObjectID != "" {
		params.ObjectID = pgtype.Text{String: f.ObjectID, Valid: true}
	}
	if f.ActorID != nil {
		params.ActorID = pgtype.Int8{Int64: *f.ActorID, Valid: true}
	}
	rows, err := s.queries.ListAuditEntries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	entries := make([]auditEntry, 0, len(rows))
	for _, row := range rows {
		e := auditEntry{
			ID:          row.ID,
			At:          row.At.Time,
			ActorEmail:  row.ActorEmail,
			ActorIP:     row.ActorIp,
			ObjectType:  row.ObjectType,
			ObjectID:    row.ObjectID,
			Action:      row.Action,
			Before:      row.Before,
			After:       row.After,
			AccountName: row.AccountName,
		}
		if row.ActorID.Valid {
			id := row.ActorID.Int64
			e.ActorID = &id
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// AuditLogPruneStore is the store behavior needed by the audit log's
// retention pruner.
type AuditLogPruneStore interface {
	PruneAuditLog(ctx context.Context, cutoff time.Time) (int64, error)
}

var _ AuditLogPruneStore = (*Store)(nil)

// PruneAuditLog deletes the rows of the audit log written before cutoff and
// returns the number removed.
func (s *Store) PruneAuditLog(ctx context.Context, cutoff time.Time) (int64, error) {
	rows, err := s.queries.DeleteAuditEntriesBefore(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("prune audit log: %w", err)
	}
	return rows, nil
}
