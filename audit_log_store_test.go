package main

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/db"
)

// The audit log tests act on vehicles named aud-*, accounts aud-*@example.org
// and API keys named aud-*, as admins aud-*@example.org, and remove them and
// most of their audit rows before and after each test. Each test reads only
// the rows written after a mark it takes, or by an admin only it uses.
func newAuditTestStore(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	clearAuditFixture(t, store)
	t.Cleanup(func() { clearAuditFixture(t, store) })
	return store
}

func clearAuditFixture(t *testing.T, store *Store) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM trips WHERE vehicle_id LIKE 'aud-%'`,
		`DELETE FROM location_points WHERE vehicle_id LIKE 'aud-%'`,
		`DELETE FROM user_vehicles WHERE vehicle_id LIKE 'aud-%'`,
		`DELETE FROM users WHERE email LIKE 'aud-%@example.org'`,
		`DELETE FROM vehicles WHERE id LIKE 'aud-%'`,
		`DELETE FROM api_keys WHERE name LIKE 'aud-%'`,
		// The rows these tests' admins made, and the server's rows about
		// these vehicles: the other objects' ids are numbers no later test
		// reuses, but every test uses these names.
		`DELETE FROM audit_log WHERE actor_email LIKE 'aud-%' OR (object_type = 'vehicle' AND object_id LIKE 'aud-%')`,
	} {
		_, err := store.pool.Exec(context.Background(), stmt)
		require.NoError(t, err)
	}
}

// auditFixture is what each test acts on: vehicle aud-v, assigned to the
// driver Ana, a vehicle aud-v2 assigned to no one, and the API key aud-key.
// tripID is set by the cases that start a trip.
type auditFixture struct {
	userID int64
	keyID  int64
	tripID int64
}

// newAuditFixture clears the last fixture and writes a new one straight to
// the tables, so making it records nothing.
func newAuditFixture(t *testing.T, store *Store) auditFixture {
	t.Helper()
	clearAuditFixture(t, store)
	ctx := context.Background()
	var fx auditFixture
	_, err := store.pool.Exec(ctx, `INSERT INTO vehicles (id, label, agency_tag) VALUES ('aud-v', 'Old', 'north'), ('aud-v2', 'Spare', '')`)
	require.NoError(t, err)
	require.NoError(t, store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role) VALUES ('Ana', 'aud-ana@example.org', 'not-a-real-hash', 'driver') RETURNING id`,
	).Scan(&fx.userID))
	_, err = store.pool.Exec(ctx, `INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, 'aud-v')`, fx.userID)
	require.NoError(t, err)
	require.NoError(t, store.pool.QueryRow(ctx,
		`INSERT INTO api_keys (name, key_hash) VALUES ('aud-key', md5(random()::text)) RETURNING id`,
	).Scan(&fx.keyID))
	return fx
}

// testAuditActor is the admin the tests make changes as.
var testAuditActor = auditActor{UserID: 41, Email: "aud-admin@example.org", IP: "198.51.100.7"}

func asTestAdmin() context.Context {
	return context.WithValue(context.Background(), auditActorKey, testAuditActor)
}

// lastAuditID is the newest audit row's id, 0 for none: the mark a test
// reads the rows after.
func lastAuditID(t *testing.T, store *Store) int64 {
	t.Helper()
	var id int64
	require.NoError(t, store.pool.QueryRow(context.Background(), `SELECT COALESCE(max(id), 0) FROM audit_log`).Scan(&id))
	return id
}

// storedAuditRow is an audit row as stored, its fields as JSON text and ""
// for NULL, so a test can compare them with assert.JSONEq.
type storedAuditRow struct {
	ActorID    *int64
	ActorEmail string
	ActorIP    string
	ObjectType string
	ObjectID   string
	Action     string
	Before     string
	After      string
}

// auditRowsAfter reads the audit rows written after mark, oldest first.
func auditRowsAfter(t *testing.T, store *Store, mark int64) []storedAuditRow {
	t.Helper()
	rows, err := store.pool.Query(context.Background(),
		`SELECT actor_id, actor_email, actor_ip, object_type, object_id, action,
		        COALESCE(before::text, ''), COALESCE(after::text, '')
		 FROM audit_log WHERE id > $1 ORDER BY id`, mark)
	require.NoError(t, err)
	defer rows.Close()
	out := make([]storedAuditRow, 0)
	for rows.Next() {
		var r storedAuditRow
		require.NoError(t, rows.Scan(&r.ActorID, &r.ActorEmail, &r.ActorIP, &r.ObjectType, &r.ObjectID, &r.Action, &r.Before, &r.After))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// auditFixtureState is everything a change to the fixture could touch, and
// how many audit rows there are, as one string: equal before and after a
// failed change when it left nothing behind.
func auditFixtureState(t *testing.T, store *Store) string {
	t.Helper()
	var state string
	require.NoError(t, store.pool.QueryRow(context.Background(), `SELECT json_build_object(
		'vehicles', (SELECT json_agg(v ORDER BY v.id) FROM (SELECT id, label, agency_tag, active FROM vehicles WHERE id LIKE 'aud-%') v),
		'users', (SELECT json_agg(u ORDER BY u.email) FROM (SELECT email, name, role, active, password_hash, sessions_valid_after FROM users WHERE email LIKE 'aud-%@example.org') u),
		'assignments', (SELECT json_agg(a ORDER BY a.user_id, a.vehicle_id) FROM (SELECT user_id, vehicle_id FROM user_vehicles WHERE vehicle_id LIKE 'aud-%') a),
		'trips', (SELECT json_agg(x ORDER BY x.id) FROM (SELECT id, status, end_time FROM trips WHERE vehicle_id LIKE 'aud-%') x),
		'api_keys', (SELECT json_agg(k ORDER BY k.id) FROM (SELECT id, name, active FROM api_keys WHERE name LIKE 'aud-%') k),
		'audit_rows', (SELECT count(*) FROM audit_log)
	)::text`).Scan(&state))
	return state
}

// failAtCommit makes a transaction that does event to a row of table that
// matches when fail as it commits, once every statement in it, the audit
// row's included, has run.
func failAtCommit(t *testing.T, store *Store, table, event, when string) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE OR REPLACE FUNCTION aud_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure at commit'; END $$`)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, fmt.Sprintf(`CREATE CONSTRAINT TRIGGER aud_test_fail AFTER %s ON %s DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (%s) EXECUTE FUNCTION aud_test_fail()`, event, table, when))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS aud_test_fail ON `+table)
		require.NoError(t, err)
		_, err = store.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS aud_test_fail()`)
		require.NoError(t, err)
	})
}

// wantAuditRow is the row a change should record; Before and After are JSON,
// "" for none.
type wantAuditRow struct {
	ObjectType, ObjectID, Action string
	Before, After                string
}

// auditedWrite is one store method an admin change goes through.
type auditedWrite struct {
	name string
	// setup prepares what act needs beyond the fixture.
	setup func(t *testing.T, store *Store, fx *auditFixture)
	act   func(ctx context.Context, store *Store, fx auditFixture) error
	want  func(t *testing.T, store *Store, fx auditFixture) wantAuditRow
	// commitFails names the row change failAtCommit fails the commit on:
	// table, event and condition.
	commitFails func(fx auditFixture) (string, string, string)
}

func userIDByEmail(t *testing.T, store *Store, email string) string {
	t.Helper()
	var id int64
	require.NoError(t, store.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, email).Scan(&id))
	return strconv.FormatInt(id, 10)
}

func apiKeyIDByName(t *testing.T, store *Store, name string) string {
	t.Helper()
	var id int64
	require.NoError(t, store.pool.QueryRow(context.Background(), `SELECT id FROM api_keys WHERE name = $1`, name).Scan(&id))
	return strconv.FormatInt(id, 10)
}

func idText(n int64) string { return strconv.FormatInt(n, 10) }

const anaBefore = `{"name":"Ana","email":"aud-ana@example.org","role":"driver","active":true}`

// auditedWrites is every store method an admin change goes through, but the
// CSV imports, which TestStore_AuditLog_Imports covers.
var auditedWrites = []auditedWrite{
	{
		name: "add a vehicle",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			created, err := s.CreateVehicle(ctx, "aud-new", "New", "south")
			if err == nil && !created {
				return fmt.Errorf("aud-new was not created")
			}
			return err
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-new", auditCreate, "", `{"label":"New","agency_tag":"south","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "INSERT", "NEW.id = 'aud-new'" },
	},
	{
		name: "add a vehicle through the API",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			_, err := s.UpsertVehicle(ctx, "aud-new", "New", "south")
			return err
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-new", auditCreate, "", `{"label":"New","agency_tag":"south","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "INSERT", "NEW.id = 'aud-new'" },
	},
	{
		name: "edit and reactivate a vehicle through the API",
		setup: func(t *testing.T, s *Store, _ *auditFixture) {
			_, err := s.pool.Exec(context.Background(), `UPDATE vehicles SET active = false WHERE id = 'aud-v'`)
			require.NoError(t, err)
		},
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			_, err := s.UpsertVehicle(ctx, "aud-v", "Renamed", "north")
			return err
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-v", auditUpdate,
				`{"label":"Old","agency_tag":"north","active":false}`, `{"label":"Renamed","agency_tag":"north","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "UPDATE", "NEW.id = 'aud-v'" },
	},
	{
		name: "edit a vehicle",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			return s.UpdateVehicleInfo(ctx, "aud-v", "Renamed", "south")
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-v", auditUpdate,
				`{"label":"Old","agency_tag":"north","active":true}`, `{"label":"Renamed","agency_tag":"south","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "UPDATE", "NEW.id = 'aud-v'" },
	},
	{
		name: "deactivate a vehicle",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			return s.SetVehicleActive(ctx, "aud-v", false)
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-v", auditDeactivate,
				`{"label":"Old","agency_tag":"north","active":true}`, `{"label":"Old","agency_tag":"north","active":false}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "UPDATE", "NEW.id = 'aud-v'" },
	},
	{
		name: "activate a vehicle",
		setup: func(t *testing.T, s *Store, _ *auditFixture) {
			_, err := s.pool.Exec(context.Background(), `UPDATE vehicles SET active = false WHERE id = 'aud-v'`)
			require.NoError(t, err)
		},
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			return s.SetVehicleActive(ctx, "aud-v", true)
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-v", auditActivate,
				`{"label":"Old","agency_tag":"north","active":false}`, `{"label":"Old","agency_tag":"north","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "UPDATE", "NEW.id = 'aud-v'" },
	},
	{
		name: "deactivate a vehicle through the API",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			return s.DeactivateVehicle(ctx, "aud-v")
		},
		want: func(*testing.T, *Store, auditFixture) wantAuditRow {
			return wantAuditRow{auditVehicle, "aud-v", auditDeactivate,
				`{"label":"Old","agency_tag":"north","active":true}`, `{"label":"Old","agency_tag":"north","active":false}`}
		},
		commitFails: func(auditFixture) (string, string, string) { return "vehicles", "UPDATE", "NEW.id = 'aud-v'" },
	},
	{
		name: "create an account",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			_, err := s.CreateUser(ctx, "Bo", "AUD-Bo@example.org", "a-password", roleAdmin)
			return err
		},
		want: func(t *testing.T, s *Store, _ auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, userIDByEmail(t, s, "aud-bo@example.org"), auditCreate, "",
				`{"name":"Bo","email":"aud-bo@example.org","role":"admin","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) {
			return "users", "INSERT", "NEW.email = 'aud-bo@example.org'"
		},
	},
	{
		name: "edit an account",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			_, err := s.UpdateUser(ctx, fx.userID, "Ana Maria", "aud-ana@example.org", roleAdmin)
			return err
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, idText(fx.userID), auditUpdate, anaBefore,
				`{"name":"Ana Maria","email":"aud-ana@example.org","role":"admin","active":true}`}
		},
		commitFails: func(fx auditFixture) (string, string, string) {
			return "users", "UPDATE", "NEW.id = " + idText(fx.userID)
		},
	},
	{
		name: "set a password",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			return s.SetUserPasswordAndRevokeSessions(ctx, fx.userID, "a-new-password")
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, idText(fx.userID), auditPassword, "", ""}
		},
		commitFails: func(fx auditFixture) (string, string, string) {
			return "users", "UPDATE", "NEW.id = " + idText(fx.userID)
		},
	},
	{
		name: "deactivate an account",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			return s.SetUserActiveAndRevokeSessions(ctx, fx.userID, false)
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, idText(fx.userID), auditDeactivate, anaBefore,
				`{"name":"Ana","email":"aud-ana@example.org","role":"driver","active":false}`}
		},
		commitFails: func(fx auditFixture) (string, string, string) {
			return "users", "UPDATE", "NEW.id = " + idText(fx.userID)
		},
	},
	{
		name: "delete an account",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			return s.DeleteUser(ctx, fx.userID)
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, idText(fx.userID), auditDelete, anaBefore, ""}
		},
		commitFails: func(fx auditFixture) (string, string, string) {
			return "users", "DELETE", "OLD.id = " + idText(fx.userID)
		},
	},
	{
		name: "assign a vehicle",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			_, err := s.CreateAssignment(ctx, fx.userID, "aud-v2")
			return err
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, idText(fx.userID), auditAssign, "", `{"vehicle_id":"aud-v2"}`}
		},
		commitFails: func(auditFixture) (string, string, string) {
			return "user_vehicles", "INSERT", "NEW.vehicle_id = 'aud-v2'"
		},
	},
	{
		name: "remove a vehicle",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			return s.DeleteAssignment(ctx, fx.userID, "aud-v")
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditUser, idText(fx.userID), auditUnassign, `{"vehicle_id":"aud-v"}`, ""}
		},
		commitFails: func(auditFixture) (string, string, string) {
			return "user_vehicles", "DELETE", "OLD.vehicle_id = 'aud-v'"
		},
	},
	{
		name: "end a trip",
		setup: func(t *testing.T, s *Store, fx *auditFixture) {
			trip, err := s.StartTrip(context.Background(), fx.userID, "aud-v", "route-1", "")
			require.NoError(t, err)
			fx.tripID = trip.ID
		},
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			_, err := s.EndTripByID(ctx, fx.tripID)
			return err
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditTrip, idText(fx.tripID), auditEnd, `{"status":"active"}`, `{"status":"completed"}`}
		},
		commitFails: func(fx auditFixture) (string, string, string) {
			return "trips", "UPDATE", "NEW.id = " + idText(fx.tripID)
		},
	},
	{
		name: "create an API key",
		act: func(ctx context.Context, s *Store, _ auditFixture) error {
			_, err := s.CreateAPIKey(ctx, "aud-new-key", hashAPIKey("aud-new-key-raw"))
			return err
		},
		want: func(t *testing.T, s *Store, _ auditFixture) wantAuditRow {
			return wantAuditRow{auditAPIKey, apiKeyIDByName(t, s, "aud-new-key"), auditCreate, "", `{"name":"aud-new-key","active":true}`}
		},
		commitFails: func(auditFixture) (string, string, string) {
			return "api_keys", "INSERT", "NEW.name = 'aud-new-key'"
		},
	},
	{
		name: "deactivate an API key",
		act: func(ctx context.Context, s *Store, fx auditFixture) error {
			return s.DeactivateAPIKey(ctx, fx.keyID)
		},
		want: func(_ *testing.T, _ *Store, fx auditFixture) wantAuditRow {
			return wantAuditRow{auditAPIKey, idText(fx.keyID), auditDeactivate, `{"name":"aud-key","active":true}`, `{"name":"aud-key","active":false}`}
		},
		commitFails: func(fx auditFixture) (string, string, string) {
			return "api_keys", "UPDATE", "NEW.id = " + idText(fx.keyID)
		},
	},
}

// TestStore_AuditLog_RecordsEveryAdminChange checks each change an admin can
// make records one row, against the admin on the context, and that the row
// and the change are one: a change whose row cannot be written does not
// happen, and a row is not left behind by a change that fails as it commits.
func TestStore_AuditLog_RecordsEveryAdminChange(t *testing.T) {
	store := newAuditTestStore(t)

	for _, w := range auditedWrites {
		setup := func(t *testing.T) auditFixture {
			t.Helper()
			fx := newAuditFixture(t, store)
			if w.setup != nil {
				w.setup(t, store, &fx)
			}
			return fx
		}

		t.Run(w.name+": records the change", func(t *testing.T) {
			fx := setup(t)
			mark := lastAuditID(t, store)

			require.NoError(t, w.act(asTestAdmin(), store, fx))

			rows := auditRowsAfter(t, store, mark)
			require.Len(t, rows, 1)
			got, want := rows[0], w.want(t, store, fx)
			require.NotNil(t, got.ActorID)
			assert.Equal(t, testAuditActor.UserID, *got.ActorID)
			assert.Equal(t, testAuditActor.Email, got.ActorEmail)
			assert.Equal(t, testAuditActor.IP, got.ActorIP)
			assert.Equal(t, want.ObjectType, got.ObjectType)
			assert.Equal(t, want.ObjectID, got.ObjectID)
			assert.Equal(t, want.Action, got.Action)
			assertAuditJSON(t, want.Before, got.Before, "before")
			assertAuditJSON(t, want.After, got.After, "after")
		})

		t.Run(w.name+": no change without its row", func(t *testing.T) {
			fx := setup(t)
			// Every audit row is refused: the fixture is made by now, so the
			// only one left to write is the change's.
			failWrites(t, store, "BEFORE INSERT", "audit_log", "true", `RAISE EXCEPTION 'test failure';`)
			before := auditFixtureState(t, store)

			require.Error(t, w.act(asTestAdmin(), store, fx))

			assert.Equal(t, before, auditFixtureState(t, store), "the change was rolled back with its row")
		})

		t.Run(w.name+": no row without its change", func(t *testing.T) {
			fx := setup(t)
			table, event, when := w.commitFails(fx)
			failAtCommit(t, store, table, event, when)
			before := auditFixtureState(t, store)

			err := w.act(asTestAdmin(), store, fx)
			require.ErrorContains(t, err, "test failure at commit")

			assert.Equal(t, before, auditFixtureState(t, store), "the row was rolled back with the change")
		})
	}
}

func assertAuditJSON(t *testing.T, want, got, field string) {
	t.Helper()
	if want == "" {
		assert.Empty(t, got, "%s should be NULL", field)
		return
	}
	assert.JSONEq(t, want, got, field)
}

// TestStore_AuditLog_Imports checks a CSV import records a row for each
// vehicle and account it writes, and each vehicle it assigns, and that a file
// that fails part way leaves no row for the rows before the failure.
func TestStore_AuditLog_Imports(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := asTestAdmin()

	t.Run("vehicles", func(t *testing.T) {
		newAuditFixture(t, store)
		mark := lastAuditID(t, store)

		_, err := store.ImportVehicles(ctx, newTestPreview(t), []vehicleImportRow{
			{Line: 2, ID: "aud-new", Label: "New", AgencyTag: "south"},
			{Line: 3, ID: "aud-v", Label: "Renamed", AgencyTag: "north"},
			{Line: 4, ID: "aud-v2", Label: "Spare", AgencyTag: ""},
		})
		require.NoError(t, err)

		rows := auditRowsAfter(t, store, mark)
		require.Len(t, rows, 2, "the unchanged vehicle has no row")
		assert.Equal(t, []string{"aud-new " + auditCreate, "aud-v " + auditUpdate},
			[]string{rows[0].ObjectID + " " + rows[0].Action, rows[1].ObjectID + " " + rows[1].Action})
		assert.JSONEq(t, `{"label":"Old","agency_tag":"north","active":true}`, rows[1].Before)
		assert.JSONEq(t, `{"label":"Renamed","agency_tag":"north","active":true}`, rows[1].After)
		for _, r := range rows {
			require.NotNil(t, r.ActorID)
			assert.Equal(t, testAuditActor.UserID, *r.ActorID)
		}
	})

	t.Run("drivers", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		mark := lastAuditID(t, store)

		outcomes, err := store.ImportDrivers(ctx, newTestPreview(t), []driverImportRow{
			{Line: 2, Email: "aud-cy@example.org", Name: "Cy", VehicleIDs: []string{"aud-v2"}},
			{Line: 3, Email: "aud-ana@example.org", Name: "Ana Maria", VehicleIDs: []string{"aud-v2"}},
		}, func() string { return "abcd-efgh-jkmn" })
		require.NoError(t, err)
		require.Equal(t, []importStatus{importNew, importUpdate}, statuses(outcomes))
		cy := idText(outcomes[0].UserID)

		want := []wantAuditRow{
			{auditUser, cy, auditCreate, "", `{"name":"Cy","email":"aud-cy@example.org","role":"driver","active":true}`},
			{auditUser, cy, auditAssign, "", `{"vehicle_id":"aud-v2"}`},
			{auditUser, idText(fx.userID), auditUpdate, anaBefore, `{"name":"Ana Maria","email":"aud-ana@example.org","role":"driver","active":true}`},
			{auditUser, idText(fx.userID), auditAssign, "", `{"vehicle_id":"aud-v2"}`},
		}
		rows := auditRowsAfter(t, store, mark)
		require.Len(t, rows, len(want))
		for i, w := range want {
			got := rows[i]
			assert.Equal(t, []string{w.ObjectType, w.ObjectID, w.Action}, []string{got.ObjectType, got.ObjectID, got.Action}, "row %d", i)
			assertAuditJSON(t, w.Before, got.Before, "before")
			assertAuditJSON(t, w.After, got.After, "after")
		}
	})

	t.Run("a file that fails part way leaves no row", func(t *testing.T) {
		newAuditFixture(t, store)
		failWrites(t, store, "BEFORE INSERT", "vehicles", "NEW.id = 'aud-last'", `RAISE EXCEPTION 'test failure';`)
		before := auditFixtureState(t, store)

		_, err := store.ImportVehicles(ctx, newTestPreview(t), []vehicleImportRow{
			{Line: 2, ID: "aud-new", Label: "New"},
			{Line: 3, ID: "aud-v", Label: "Renamed", AgencyTag: "north"},
			{Line: 4, ID: "aud-last", Label: "Last"},
		})
		require.Error(t, err)

		assert.Equal(t, before, auditFixtureState(t, store), "the rows for the vehicles before the failure were rolled back")
	})
}

// TestStore_AuditLog_ServerChanges checks a change made with no admin on the
// context, as at startup or by reset-admin, is recorded with no actor.
func TestStore_AuditLog_ServerChanges(t *testing.T) {
	store := newAuditTestStore(t)
	newAuditFixture(t, store)
	mark := lastAuditID(t, store)

	require.NoError(t, resetAdmin(context.Background(), store, "aud-ana@example.org", "a-new-password"))

	rows := auditRowsAfter(t, store, mark)
	require.Len(t, rows, 2, "made an admin, then given a new password")
	for _, r := range rows {
		assert.Nil(t, r.ActorID)
		assert.Empty(t, r.ActorEmail)
		assert.Empty(t, r.ActorIP)
	}
	assert.Equal(t, auditUpdate, rows[0].Action)
	assert.JSONEq(t, `{"name":"Ana","email":"aud-ana@example.org","role":"admin","active":true}`, rows[0].After)
	assert.Equal(t, auditPassword, rows[1].Action)
}

// TestStore_AuditLog_UnchangedSaveRecordsNothing checks a save that leaves
// every field as it was is not recorded: the log is of changes.
func TestStore_AuditLog_UnchangedSaveRecordsNothing(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	ctx := asTestAdmin()
	mark := lastAuditID(t, store)

	require.NoError(t, store.UpdateVehicleInfo(ctx, "aud-v", "Old", "north"))
	require.NoError(t, store.SetVehicleActive(ctx, "aud-v", true))
	_, err := store.UpsertVehicle(ctx, "aud-v", "Old", "north")
	require.NoError(t, err)
	_, err = store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleDriver)
	require.NoError(t, err)
	require.NoError(t, store.SetUserActiveAndRevokeSessions(ctx, fx.userID, true))

	assert.Empty(t, auditRowsAfter(t, store, mark))
}

// TestStore_AuditLog_RefusedChangeRecordsNothing checks a change the store
// refuses leaves no row.
func TestStore_AuditLog_RefusedChangeRecordsNothing(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := asTestAdmin()

	tests := []struct {
		name  string
		setup func(t *testing.T, fx *auditFixture)
		act   func(fx auditFixture) error
		want  error
	}{
		{
			name: "an email another account has",
			setup: func(t *testing.T, _ *auditFixture) {
				_, err := store.pool.Exec(context.Background(), `INSERT INTO users (name, email, password_hash) VALUES ('Bo', 'aud-bo@example.org', 'x')`)
				require.NoError(t, err)
			},
			act: func(fx auditFixture) error {
				_, err := store.UpdateUser(ctx, fx.userID, "Ana", "aud-bo@example.org", roleDriver)
				return err
			},
			want: ErrDuplicateEmail,
		},
		{
			name: "deleting an account with trips",
			setup: func(t *testing.T, fx *auditFixture) {
				_, err := store.StartTrip(context.Background(), fx.userID, "aud-v", "route-1", "")
				require.NoError(t, err)
			},
			act:  func(fx auditFixture) error { return store.DeleteUser(ctx, fx.userID) },
			want: ErrUserHasTrips,
		},
		{
			name: "removing the vehicle of a running trip",
			setup: func(t *testing.T, fx *auditFixture) {
				_, err := store.StartTrip(context.Background(), fx.userID, "aud-v", "route-1", "")
				require.NoError(t, err)
			},
			act:  func(fx auditFixture) error { return store.DeleteAssignment(ctx, fx.userID, "aud-v") },
			want: ErrAssignmentInUse,
		},
		{
			name: "ending a trip already ended",
			setup: func(t *testing.T, fx *auditFixture) {
				trip, err := store.StartTrip(context.Background(), fx.userID, "aud-v", "route-1", "")
				require.NoError(t, err)
				_, err = store.EndTripByID(context.Background(), trip.ID)
				require.NoError(t, err)
				fx.tripID = trip.ID
			},
			act: func(fx auditFixture) error {
				_, err := store.EndTripByID(ctx, fx.tripID)
				return err
			},
			want: ErrTripNotActive,
		},
		{
			name: "an unknown API key",
			act:  func(fx auditFixture) error { return store.DeactivateAPIKey(ctx, fx.keyID+1_000_000) },
			want: ErrAPIKeyNotFound,
		},
		{
			name: "an unknown account",
			act:  func(fx auditFixture) error { return store.DeleteUser(ctx, fx.userID+1_000_000) },
			want: ErrUserNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newAuditFixture(t, store)
			if tc.setup != nil {
				tc.setup(t, &fx)
			}
			before := auditFixtureState(t, store)

			require.ErrorIs(t, tc.act(fx), tc.want)

			assert.Equal(t, before, auditFixtureState(t, store))
		})
	}
}

// TestStore_AuditLog_RowsCannotBeChanged checks the table refuses an UPDATE:
// a row records what happened, and nothing rewrites it.
func TestStore_AuditLog_RowsCannotBeChanged(t *testing.T) {
	store := newAuditTestStore(t)
	newAuditFixture(t, store)
	_, err := store.CreateVehicle(asTestAdmin(), "aud-new", "New", "")
	require.NoError(t, err)
	last := lastAuditID(t, store)

	_, err = store.pool.Exec(context.Background(), `UPDATE audit_log SET actor_email = 'someone-else@example.org' WHERE id = $1`, last)
	require.ErrorContains(t, err, "audit_log rows cannot be changed")

	rows := auditRowsAfter(t, store, last-1)
	require.Len(t, rows, 1)
	assert.Equal(t, testAuditActor.Email, rows[0].ActorEmail)
}

// finishesWhileHeld runs act while another transaction holds what lock
// takes, as an admin change holds its audit read until it commits, and
// reports whether act finished before that transaction ended. act's error is
// checked once the hold is let go.
func finishesWhileHeld(t *testing.T, store *Store, lock func(context.Context, *db.Queries) error, act func(context.Context) error) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(asTestAdmin(), 30*time.Second)
	defer cancel()

	holder, err := dedicatedConn(t).Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	require.NoError(t, lock(ctx, db.New(holder)))
	var pid int
	require.NoError(t, holder.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid))

	done := make(chan error, 1)
	go func() { done <- act(ctx) }()
	require.Eventually(t, func() bool { return len(done) == 1 || blockedBehind(store, pid, 1) == 1 },
		5*time.Second, 10*time.Millisecond, "neither finished nor waited for the held row")
	finished := len(done) == 1

	require.NoError(t, holder.Rollback(ctx))
	require.NoError(t, <-done)
	return finished
}

// The audit log's reads of the old fields, each as an admin change takes it.
func lockVehicleForAudit(ctx context.Context, q *db.Queries, _ auditFixture) error {
	_, err := q.GetVehicleForUpdate(ctx, "aud-v")
	return err
}

func lockUserForAudit(ctx context.Context, q *db.Queries, fx auditFixture) error {
	_, err := q.GetUserForUpdate(ctx, fx.userID)
	return err
}

func lockAPIKeyForAudit(ctx context.Context, q *db.Queries, fx auditFixture) error {
	_, err := q.GetAPIKeyForUpdate(ctx, fx.keyID)
	return err
}

// writeWhileAuditHeld is a write made while an admin change holds its audit
// read of an object.
type writeWhileAuditHeld struct {
	name string
	lock func(ctx context.Context, q *db.Queries, fx auditFixture) error
	act  func(ctx context.Context, store *Store, fx auditFixture) error
}

// finishes runs w on a new fixture and reports whether it finished while the
// audit read was still held.
func (w writeWhileAuditHeld) finishes(t *testing.T, store *Store) bool {
	t.Helper()
	fx := newAuditFixture(t, store)
	return finishesWhileHeld(t, store,
		func(ctx context.Context, q *db.Queries) error { return w.lock(ctx, q, fx) },
		func(ctx context.Context) error { return w.act(ctx, store, fx) })
}

// TestStore_AuditLog_ReadLetsReferencesThrough inserts a row that points at a
// vehicle or an account while an admin change holds its audit read of it. The
// insert's foreign key takes KEY SHARE on that row, which FOR UPDATE made
// wait for the admin's commit and FOR NO KEY UPDATE does not.
func TestStore_AuditLog_ReadLetsReferencesThrough(t *testing.T) {
	store := newAuditTestStore(t)
	for _, w := range []writeWhileAuditHeld{
		{
			name: "an offline upload from the vehicle",
			lock: lockVehicleForAudit,
			act: func(ctx context.Context, s *Store, fx auditFixture) error {
				_, err := s.SaveLocationHistory(ctx, fx.userID, "aud-v", []*LocationReport{{
					VehicleID: "aud-v", Latitude: 47.6, Longitude: -122.3, Timestamp: time.Now().Unix(), DriverID: idText(fx.userID),
				}})
				return err
			},
		},
		{
			name: "a trip start by the driver",
			lock: lockUserForAudit,
			act: func(ctx context.Context, s *Store, fx auditFixture) error {
				_, err := s.StartTrip(ctx, fx.userID, "aud-v", "route-1", "")
				return err
			},
		},
		{
			name: "an assignment for the driver",
			lock: lockUserForAudit,
			act: func(ctx context.Context, s *Store, fx auditFixture) error {
				_, err := s.CreateAssignment(ctx, fx.userID, "aud-v2")
				return err
			},
		},
	} {
		t.Run(w.name, func(t *testing.T) {
			assert.True(t, w.finishes(t, store), "waited for the admin's change to commit")
		})
	}
}

// TestStore_AuditLog_ReadHoldsOffASecondChange makes an admin change to an
// object while another holds its audit read of it. The second must wait, so
// the before it records is what the first left.
func TestStore_AuditLog_ReadHoldsOffASecondChange(t *testing.T) {
	store := newAuditTestStore(t)
	for _, w := range []writeWhileAuditHeld{
		{
			name: "a vehicle",
			lock: lockVehicleForAudit,
			act: func(ctx context.Context, s *Store, _ auditFixture) error {
				return s.UpdateVehicleInfo(ctx, "aud-v", "New", "north")
			},
		},
		{
			name: "an account",
			lock: lockUserForAudit,
			act: func(ctx context.Context, s *Store, fx auditFixture) error {
				_, err := s.UpdateUser(ctx, fx.userID, "Ana B", "aud-ana@example.org", roleDriver)
				return err
			},
		},
		{
			name: "an API key",
			lock: lockAPIKeyForAudit,
			act: func(ctx context.Context, s *Store, fx auditFixture) error {
				return s.DeactivateAPIKey(ctx, fx.keyID)
			},
		},
	} {
		t.Run(w.name, func(t *testing.T) {
			assert.False(t, w.finishes(t, store), "changed the row while another change held it")
		})
	}
}

// TestStore_ListAuditEntries checks the reads behind the Audit log page: the
// newest first, a page at a time, narrowed to a kind of object, one object or
// one admin.
func TestStore_ListAuditEntries(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	// Admins no other test uses, so their rows are only these.
	first := auditActor{UserID: 2_000_000_041, Email: "aud-first@example.org", IP: "192.0.2.10"}
	second := auditActor{UserID: 2_000_000_042, Email: "aud-second@example.org", IP: "192.0.2.11"}
	as := func(a auditActor) context.Context { return context.WithValue(context.Background(), auditActorKey, a) }

	require.NoError(t, store.UpdateVehicleInfo(as(first), "aud-v", "One", "north"))
	require.NoError(t, store.UpdateVehicleInfo(as(first), "aud-v", "Two", "north"))
	require.NoError(t, store.SetVehicleActive(as(first), "aud-v2", false))
	require.NoError(t, store.SetUserPasswordAndRevokeSessions(as(second), fx.userID, "a-new-password"))

	list := func(f auditFilter) []auditEntry {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 10
		}
		entries, err := store.ListAuditEntries(context.Background(), f)
		require.NoError(t, err)
		return entries
	}
	firstID := first.UserID

	t.Run("one admin, newest first", func(t *testing.T) {
		entries := list(auditFilter{ActorID: &firstID})
		require.Len(t, entries, 3)
		assert.Equal(t, []string{"aud-v2", "aud-v", "aud-v"}, []string{entries[0].ObjectID, entries[1].ObjectID, entries[2].ObjectID})
		assert.Greater(t, entries[0].ID, entries[1].ID)
		e := entries[1]
		require.NotNil(t, e.ActorID)
		assert.Equal(t, firstID, *e.ActorID)
		assert.Equal(t, first.Email, e.ActorEmail)
		assert.Equal(t, first.IP, e.ActorIP)
		assert.Equal(t, auditVehicle, e.ObjectType)
		assert.Equal(t, auditUpdate, e.Action)
		assert.JSONEq(t, `{"label":"One","agency_tag":"north","active":true}`, string(e.Before))
		assert.JSONEq(t, `{"label":"Two","agency_tag":"north","active":true}`, string(e.After))
		assert.WithinDuration(t, time.Now(), e.At, time.Minute)
	})

	t.Run("a page at a time", func(t *testing.T) {
		entries := list(auditFilter{ActorID: &firstID, Limit: 2, Offset: 1})
		require.Len(t, entries, 2)
		assert.Equal(t, "Two", auditFields(entries[0].After)["label"])
		assert.Equal(t, "One", auditFields(entries[1].After)["label"])
	})

	t.Run("one object", func(t *testing.T) {
		entries := list(auditFilter{ObjectType: auditVehicle, ObjectID: "aud-v"})
		require.Len(t, entries, 2)
		for _, e := range entries {
			assert.Equal(t, "aud-v", e.ObjectID)
		}
	})

	t.Run("one kind of object, from one admin", func(t *testing.T) {
		secondID := second.UserID
		entries := list(auditFilter{ObjectType: auditUser, ActorID: &secondID})
		require.Len(t, entries, 1)
		assert.Equal(t, auditPassword, entries[0].Action)
		assert.Nil(t, entries[0].Before, "a new password has no fields")
		assert.Nil(t, entries[0].After)
		assert.Equal(t, "Ana", entries[0].AccountName, "an account's row carries what it is called now")
		assert.Empty(t, list(auditFilter{ObjectType: auditVehicle, ActorID: &secondID}))
	})

	t.Run("an account's name is now's, until it is deleted", func(t *testing.T) {
		secondID := second.UserID
		_, err := store.UpdateUser(context.Background(), fx.userID, "Ana Maria", "aud-ana@example.org", roleDriver)
		require.NoError(t, err)
		entries := list(auditFilter{ActorID: &secondID})
		require.Len(t, entries, 1)
		assert.Equal(t, "Ana Maria", entries[0].AccountName)

		require.NoError(t, store.DeleteUser(context.Background(), fx.userID))
		entries = list(auditFilter{ActorID: &secondID})
		require.Len(t, entries, 1)
		assert.Empty(t, entries[0].AccountName)
		for _, e := range list(auditFilter{ActorID: &firstID}) {
			assert.Empty(t, e.AccountName, "a vehicle's row has none")
		}
	})

	t.Run("a change the server made has no actor", func(t *testing.T) {
		require.NoError(t, store.UpdateVehicleInfo(context.Background(), "aud-v", "Three", "north"))
		entries := list(auditFilter{ObjectType: auditVehicle, ObjectID: "aud-v", Limit: 1})
		require.Len(t, entries, 1)
		assert.Nil(t, entries[0].ActorID)
		assert.Empty(t, entries[0].ActorEmail)
	})
}
