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

// The kinds of object the audit log records, as its object_type column names
// them. A session is named by its account: its object_id is the users.id.
const (
	auditVehicle = "vehicle"
	auditUser    = "user"
	auditTrip    = "trip"
	auditAPIKey  = "api_key"
	auditSession = "session"
)

// What the audit log records, as its action column names it: the changes to
// an object, and the sign-ins, failed sign-ins and sign-outs of an account
// that may use the admin UI.
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
	auditSignIn     = "sign_in"
	auditSignOut    = "sign_out"
	auditSignInFail = "sign_in_failed"
)

// The ways an account signs in and out, as a session row's via names them:
// the admin UI's form, or POST /api/v1/auth/login and /logout, which the
// driver apps use too.
const (
	auditViaAdmin = "admin"
	auditViaAPI   = "api"
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

// sessionAudit is which way an account signed in or out, the one field the
// log keeps of it: never a password, a token or its id.
type sessionAudit struct {
	Via string `json:"via"`
}

// auditActor is who a row is recorded against: the signed-in admin, or the
// account signing in or out, and the address their request came from. UserID
// is 0, and Email "", for a failed sign-in: no one knows who was at it.
type auditActor struct {
	UserID int64
	Email  string
	IP     string
	// Via is the way an account is signing in or out, and "" for an admin
	// making a change.
	Via string
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

// withSessionActor returns r's context for signing user in or out through
// via, with user as the actor the store records it against. A role that may
// not use the admin UI gets the context as it was, so a driver's sign-ins and
// sign-outs are not recorded: their trips say when they drove.
func withSessionActor(r *http.Request, user *User, via string, trustProxy bool) context.Context {
	if !adminUIRole(user.Role) {
		return r.Context()
	}
	actor := auditActor{UserID: user.ID, Email: user.Email, IP: clientIP(r, trustProxy), Via: via}
	return context.WithValue(r.Context(), auditActorKey, actor)
}

// sessionUser is the account signing out, as the claims of the session it
// ends name it.
func sessionUser(id int64, claims jwt.MapClaims) *User {
	email, _ := claims["email"].(string)
	role, _ := claims["role"].(string)
	return &User{ID: id, Email: email, Role: role}
}

// sessionActor is the account signing in or out that withSessionActor put on
// ctx, and false for a context with none.
func sessionActor(ctx context.Context) (auditActor, bool) {
	actor, ok := ctx.Value(auditActorKey).(auditActor)
	return actor, ok && actor.Via != ""
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
// The actor is the one withAuditActor or withSessionActor put on ctx; a
// change made with none there was made by the server itself, at startup or by
// the reset-admin command. A change that left every field as it was is not
// recorded.
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
		params.ActorID = pgtype.Int8{Int64: actor.UserID, Valid: actor.UserID != 0}
		params.ActorEmail = actor.Email
		params.ActorIp = actor.IP
	}
	if err := q.InsertAuditEntry(ctx, params); err != nil {
		return fmt.Errorf("record %s %s %s: %w", c.ObjectType, c.ObjectID, c.Action, err)
	}
	return nil
}

// inSessionTx runs fn, which signs an account out, in a transaction when there
// is a row to write with it, and as it is when there is none.
func (s *Store) inSessionTx(ctx context.Context, fn func(q *db.Queries) error) error {
	if _, ok := sessionActor(ctx); !ok {
		return fn(s.queries)
	}
	return s.inTx(ctx, fn)
}

// recordSession records action, a sign-in or a sign-out, through q, which
// must be the transaction that makes or ends the session when it writes
// anything else. A context that names no account to record it against
// records nothing.
func recordSession(ctx context.Context, q *db.Queries, action string) error {
	actor, ok := sessionActor(ctx)
	if !ok {
		return nil
	}
	return recordAudit(ctx, q, auditChange{
		ObjectType: auditSession, ObjectID: strconv.FormatInt(actor.UserID, 10), Action: action,
		After: sessionAudit{Via: actor.Via},
	})
}

// SignInRecorder signs an account in to the admin UI, which stores nothing but
// the row that records it: its session is only a cookie.
type SignInRecorder interface {
	RecordSignIn(ctx context.Context, user *User) (signedInAt time.Time, err error)
}

var _ SignInRecorder = (*Store)(nil)

// RecordSignIn records user's sign-in, as the account withSessionActor put on
// ctx, and returns when it counts as done: see signIn.
func (s *Store) RecordSignIn(ctx context.Context, user *User) (signedInAt time.Time, err error) {
	return s.signIn(ctx, user, nil)
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
	// ActorID is nil for a change the server made itself, and for a failed
	// sign-in, whose sender is unknown.
	ActorID    *int64
	ActorEmail string
	ActorIP    string
	ObjectType string
	ObjectID   string
	Action     string
	// Before and After are nil where the row has none.
	Before json.RawMessage
	After  json.RawMessage
	// AccountName is the name the account the row is about, or the session
	// it records, has now: "" for a row about something else, or about an
	// account since deleted.
	AccountName string
}

// auditFilter narrows the audit log to one kind of object, one object of
// that kind, or what one account did. An empty field, or a nil ActorID,
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
