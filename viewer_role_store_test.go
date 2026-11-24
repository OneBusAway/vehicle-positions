package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const viewerRoleMigration = 33

// TestMigration_ViewerRoleUpAndDown: up, users.role takes viewer; down, a
// viewer becomes a deactivated driver signed out everywhere, the other
// accounts are left alone, and viewer is refused again; and it goes up a
// second time.
func TestMigration_ViewerRoleUpAndDown(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	name := fmt.Sprintf("viewer_role_migration_%d", time.Now().UnixNano())
	_, err := store.pool.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		assert.NoError(t, err)
	})
	dbURL, err := url.Parse(testDatabaseURL(t))
	require.NoError(t, err)
	dbURL.Path = "/" + name

	src, err := iofs.New(migrationsFS, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	conn, err := pgx.Connect(ctx, dbURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })

	addUser := func(email, role string) (int64, error) {
		var id int64
		err := conn.QueryRow(ctx,
			`INSERT INTO users (name, email, password_hash, role) VALUES ($1, $1, 'hash', $2) RETURNING id`, email, role).Scan(&id)
		return id, err
	}
	type account struct {
		Role          string
		Active        bool
		SessionsEnded bool
	}
	read := func(id int64) account {
		t.Helper()
		var a account
		var ended pgtype.Timestamptz
		require.NoError(t, conn.QueryRow(ctx, `SELECT role, active, sessions_valid_after FROM users WHERE id = $1`, id).
			Scan(&a.Role, &a.Active, &ended))
		a.SessionsEnded = ended.Valid
		return a
	}

	require.NoError(t, m.Migrate(viewerRoleMigration-1))
	_, err = addUser("before@example.org", roleViewer)
	require.Error(t, err, "before the migration, users.role has no viewer")

	require.NoError(t, m.Migrate(viewerRoleMigration))
	viewer, err := addUser("viewer@example.org", roleViewer)
	require.NoError(t, err)
	driver, err := addUser("driver@example.org", roleDriver)
	require.NoError(t, err)
	admin, err := addUser("admin@example.org", roleAdmin)
	require.NoError(t, err)
	_, err = addUser("boss@example.org", "superuser")
	require.Error(t, err, "the constraint still refuses a role that is none of the three")
	for _, id := range []int64{viewer, driver} {
		_, err = conn.Exec(ctx, `INSERT INTO refresh_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + interval '1 day')`,
			"hash-"+strconv.FormatInt(id, 10), id)
		require.NoError(t, err)
	}

	require.NoError(t, m.Migrate(viewerRoleMigration-1))
	assert.Equal(t, account{Role: roleDriver, Active: false, SessionsEnded: true}, read(viewer))
	assert.Equal(t, account{Role: roleDriver, Active: true}, read(driver))
	assert.Equal(t, account{Role: roleAdmin, Active: true}, read(admin))
	rows, err := conn.Query(ctx, `SELECT user_id FROM refresh_tokens ORDER BY user_id`)
	require.NoError(t, err)
	tokens, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)
	assert.Equal(t, []int64{driver}, tokens, "down ends only the viewer's sessions")
	_, err = addUser("after@example.org", roleViewer)
	require.Error(t, err, "down puts back the constraint without viewer")

	require.NoError(t, m.Migrate(viewerRoleMigration))
	_, err = addUser("again@example.org", roleViewer)
	require.NoError(t, err)
}

// TestStore_AuditLog_ViewerRole: making an account a viewer, and taking the
// role away again, is recorded like any account change, through the same
// path the form and the API use, and the page names the roles. Ana's vehicle
// goes with the change to viewer, recorded as an unassignment.
func TestStore_AuditLog_ViewerRole(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	ctx := asTestAdmin()
	mark := lastAuditID(t, store)

	vic, err := store.CreateUser(ctx, "Vic", "aud-vic@example.org", "a-password", roleViewer)
	require.NoError(t, err)
	_, err = store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleViewer)
	require.NoError(t, err)
	_, err = store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleAdmin)
	require.NoError(t, err)

	rows := auditRowsAfter(t, store, mark)
	require.Len(t, rows, 4)
	ana := strconv.FormatInt(fx.userID, 10)
	for i, want := range []struct {
		id, action, before, after string
	}{
		{strconv.FormatInt(vic.ID, 10), auditCreate, "", `{"name":"Vic","email":"aud-vic@example.org","role":"viewer","active":true}`},
		{ana, auditUpdate, `{"name":"Ana","email":"aud-ana@example.org","role":"driver","active":true}`, `{"name":"Ana","email":"aud-ana@example.org","role":"viewer","active":true}`},
		{ana, auditUnassign, `{"vehicle_id":"aud-v"}`, ""},
		{ana, auditUpdate, `{"name":"Ana","email":"aud-ana@example.org","role":"viewer","active":true}`, `{"name":"Ana","email":"aud-ana@example.org","role":"admin","active":true}`},
	} {
		row := rows[i]
		require.NotNil(t, row.ActorID)
		assert.Equal(t, testAuditActor.UserID, *row.ActorID)
		assert.Equal(t, auditUser, row.ObjectType)
		assert.Equal(t, want.id, row.ObjectID)
		assert.Equal(t, want.action, row.Action)
		for _, side := range []struct{ want, got string }{{want.before, row.Before}, {want.after, row.After}} {
			if side.want == "" {
				assert.Empty(t, side.got)
			} else {
				assert.JSONEq(t, side.want, side.got)
			}
		}
	}

	entries, err := store.ListAuditEntries(context.Background(), auditFilter{ObjectType: auditUser, ObjectID: ana, Limit: 3})
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, []auditDetail{{Label: "Role", From: "Viewer", To: "Admin"}}, newAuditRow(entries[0], time.UTC, time.Now()).Details)
	assert.Equal(t, []auditDetail{{Label: "Role", From: "Driver", To: "Viewer"}}, newAuditRow(entries[2], time.UTC, time.Now()).Details)
}

func TestStore_ImportDrivers_NeverTouchesAViewer(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-v1", "V1", true)
	viewer := addImportUser(t, store, "Viewer", "imp-viewer@example.org", roleViewer)
	before := readImportedUser(t, store, viewer)
	rows := []driverImportRow{{Line: 2, Email: "IMP-viewer@example.org", Name: "Renamed", VehicleIDs: []string{"imp-v1"}}}

	want := []importOutcome{{Status: importError, Reason: "this email belongs to a viewer account"}}
	plan, err := store.PlanDriverImport(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, want, plan)
	got, err := store.ImportDrivers(ctx, newTestPreview(t), rows, noPassword)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, before, readImportedUser(t, store, viewer))
	assert.Empty(t, assignedVehicles(t, store, viewer))
}

// TestViewer_ThroughTheServer is the whole path against the database: a
// viewer signs in to the admin and through the API, reads both, and every
// change it tries, on a page, in the admin API or as a driver, is refused
// with nothing saved and nothing recorded.
func TestViewer_ThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	_, err := store.CreateUser(ctx, "Vic", "aud-vic@example.org", "a-password", roleViewer)
	require.NoError(t, err)
	_, err = store.CreateVehicle(ctx, "aud-v", "Bus 7", "")
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(store, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// The admin's sign-in form.
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader("email=aud-vic%40example.org&password=a-password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := serve(req)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	require.Len(t, w.Result().Cookies(), 1)
	session := w.Result().Cookies()[0]

	// The JSON API's sign-in.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"aud-vic@example.org","password":"a-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w = serve(req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var login LoginResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&login))
	bearer := "Bearer " + login.AccessToken

	// Signed in, and the sign-ins recorded, so from here nothing should be.
	before := auditFixtureState(t, store)

	req = httptest.NewRequest(http.MethodGet, "/admin/vehicles", nil)
	req.AddCookie(session)
	w = serve(req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Bus 7")
	assert.Empty(t, writeControls(w.Body.String()))

	req = httptest.NewRequest(http.MethodPost, "/admin/vehicles", strings.NewReader("id=aud-new&label=New"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	w = serve(req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Your account can look but not change")

	// The JSON API, with the token its sign-in gave.
	for _, tc := range []struct {
		method, path, body string
		want               int
		wantError          string
	}{
		{http.MethodGet, "/api/v1/admin/vehicles/aud-v", "", http.StatusOK, ""},
		{http.MethodPost, "/api/v1/admin/vehicles", `{"id":"aud-api","label":"API"}`, http.StatusForbidden, "admin access required"},
		{http.MethodDelete, "/api/v1/admin/vehicles/aud-v", "", http.StatusForbidden, "admin access required"},
		{http.MethodPost, "/api/v1/trips/start", `{"vehicle_id":"aud-v","route_id":"R1"}`, http.StatusForbidden, "forbidden"},
		{http.MethodPost, "/api/v1/locations", `{"vehicle_id":"aud-v","latitude":47.6,"longitude":-122.3,"timestamp":1}`, http.StatusForbidden, "forbidden"},
	} {
		req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", bearer)
		w = serve(req)
		assert.Equal(t, tc.want, w.Code, "%s %s: %s", tc.method, tc.path, w.Body.String())
		if tc.wantError != "" {
			assert.Equal(t, tc.wantError, decodeError(t, w), "%s %s", tc.method, tc.path)
		}
	}

	assert.Equal(t, before, auditFixtureState(t, store), "nothing a viewer tried was saved or recorded")
	exists, err := store.VehicleExists(ctx, "aud-new")
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestStore_AssignToViewer_Refused: no vehicle can be assigned to a viewer,
// and the refused assignment leaves no row and no audit entry.
func TestStore_AssignToViewer_Refused(t *testing.T) {
	store := newAuditTestStore(t)
	newAuditFixture(t, store)
	vic, err := store.CreateUser(context.Background(), "Vic", "aud-vic@example.org", "a-password", roleViewer)
	require.NoError(t, err)
	before := auditFixtureState(t, store)

	_, err = store.CreateAssignment(asTestAdmin(), vic.ID, "aud-v2")
	assert.ErrorIs(t, err, ErrAssignToViewer)
	assert.Equal(t, before, auditFixtureState(t, store))
}

// TestStore_ChangeToViewer_DropsVehicles: making an account a viewer
// unassigns its vehicles in the same change, each recorded as an
// unassignment after the role change. Another role change keeps them.
func TestStore_ChangeToViewer_DropsVehicles(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := asTestAdmin()
	for _, from := range []string{roleDriver, roleAdmin} {
		t.Run("from "+from, func(t *testing.T) {
			fx := newAuditFixture(t, store)
			_, err := store.pool.Exec(context.Background(), `UPDATE users SET role = $2 WHERE id = $1`, fx.userID, from)
			require.NoError(t, err)
			_, err = store.pool.Exec(context.Background(), `INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, 'aud-v2')`, fx.userID)
			require.NoError(t, err)
			mark := lastAuditID(t, store)

			_, err = store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleViewer)
			require.NoError(t, err)

			assert.Empty(t, assignedVehicles(t, store, fx.userID))
			rows := auditRowsAfter(t, store, mark)
			require.Len(t, rows, 3)
			assert.Equal(t, auditUpdate, rows[0].Action)
			assert.JSONEq(t, `{"name":"Ana","email":"aud-ana@example.org","role":"viewer","active":true}`, rows[0].After)
			dropped := make([]string, 0, 2)
			for _, row := range rows[1:] {
				assert.Equal(t, auditUnassign, row.Action)
				assert.Equal(t, strconv.FormatInt(fx.userID, 10), row.ObjectID)
				require.NotNil(t, row.ActorID)
				assert.Equal(t, testAuditActor.UserID, *row.ActorID)
				var before struct {
					VehicleID string `json:"vehicle_id"`
				}
				require.NoError(t, json.Unmarshal([]byte(row.Before), &before))
				dropped = append(dropped, before.VehicleID)
			}
			assert.ElementsMatch(t, []string{"aud-v", "aud-v2"}, dropped)
		})
	}

	t.Run("another change keeps them", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		_, err := store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleAdmin)
		require.NoError(t, err)
		assert.Equal(t, []string{"aud-v"}, assignedVehicles(t, store, fx.userID))
	})
}

// TestStore_ChangeToViewer_RefusedOnATrip: an account with a trip running
// can't be made a viewer, since that would unassign the trip's vehicle, and
// the refused change leaves the account, its vehicles and the log as they were.
func TestStore_ChangeToViewer_RefusedOnATrip(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	_, err := store.StartTrip(context.Background(), fx.userID, "aud-v", "route-1", "")
	require.NoError(t, err)
	before := auditFixtureState(t, store)

	_, err = store.UpdateUser(asTestAdmin(), fx.userID, "Ana", "aud-ana@example.org", roleViewer)
	assert.ErrorIs(t, err, ErrViewerWithTrip)
	assert.Equal(t, before, auditFixtureState(t, store))
}

// TestStore_ChangeToViewer_PastTheListBound: an account with more vehicles
// than ListVehiclesByUser's bound of 1000 loses every one of them, each
// recorded, and a trip on the vehicle past that bound still refuses the
// change.
func TestStore_ChangeToViewer_PastTheListBound(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := asTestAdmin()
	bg := context.Background()
	// assignMany gives the account 1000 vehicles newer than its aud-v, which
	// ListVehiclesByUser, newest first, would leave out, and returns all 1001.
	assignMany := func(t *testing.T, userID int64) []string {
		t.Helper()
		_, err := store.pool.Exec(bg, `UPDATE user_vehicles SET created_at = NOW() - interval '1 hour' WHERE user_id = $1`, userID)
		require.NoError(t, err)
		_, err = store.pool.Exec(bg, `INSERT INTO vehicles (id, label, agency_tag) SELECT 'aud-many-' || i, '', '' FROM generate_series(1, 1000) i`)
		require.NoError(t, err)
		_, err = store.pool.Exec(bg, `INSERT INTO user_vehicles (user_id, vehicle_id) SELECT $1, 'aud-many-' || i FROM generate_series(1, 1000) i`, userID)
		require.NoError(t, err)
		ids := []string{"aud-v"}
		for i := 1; i <= 1000; i++ {
			ids = append(ids, fmt.Sprintf("aud-many-%d", i))
		}
		return ids
	}
	assignments := func(t *testing.T, userID int64) int {
		t.Helper()
		var n int
		require.NoError(t, store.pool.QueryRow(bg, `SELECT count(*) FROM user_vehicles WHERE user_id = $1`, userID).Scan(&n))
		return n
	}

	t.Run("every vehicle goes, each recorded", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		all := assignMany(t, fx.userID)
		mark := lastAuditID(t, store)

		_, err := store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleViewer)
		require.NoError(t, err)

		assert.Zero(t, assignments(t, fx.userID))
		rows := auditRowsAfter(t, store, mark)
		require.Equal(t, 1+len(all), len(rows), "the role change and an unassignment for each vehicle")
		assert.Equal(t, auditUpdate, rows[0].Action)
		dropped := make([]string, 0, len(all))
		for _, row := range rows[1:] {
			require.Equal(t, auditUnassign, row.Action)
			var before struct {
				VehicleID string `json:"vehicle_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(row.Before), &before))
			dropped = append(dropped, before.VehicleID)
		}
		assert.ElementsMatch(t, all, dropped)
	})

	t.Run("a trip on the vehicle past the bound refuses it", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		assignMany(t, fx.userID)
		_, err := store.StartTrip(bg, fx.userID, "aud-v", "route-1", "")
		require.NoError(t, err)
		mark := lastAuditID(t, store)

		_, err = store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleViewer)
		assert.ErrorIs(t, err, ErrViewerWithTrip)
		assert.Equal(t, roleDriver, readImportedUser(t, store, fx.userID).Role)
		assert.Equal(t, 1001, assignments(t, fx.userID))
		assert.Empty(t, auditRowsAfter(t, store, mark))
		assert.True(t, activeTripExists(t, store, fx.userID, "aud-v"))
	})
}

// TestStore_ChangeToViewer_AnotherDriversTrip: a trip another driver runs on
// one of the account's vehicles doesn't hold up the change, as it doesn't
// hold up an unassign. The account loses the vehicle, and the other driver
// keeps it and the trip.
func TestStore_ChangeToViewer_AnotherDriversTrip(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	bg := context.Background()
	var bo int64
	require.NoError(t, store.pool.QueryRow(bg,
		`INSERT INTO users (name, email, password_hash, role) VALUES ('Bo', 'aud-bo@example.org', 'not-a-real-hash', 'driver') RETURNING id`,
	).Scan(&bo))
	_, err := store.pool.Exec(bg, `INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, 'aud-v')`, bo)
	require.NoError(t, err)
	_, err = store.StartTrip(bg, bo, "aud-v", "route-1", "")
	require.NoError(t, err)

	_, err = store.UpdateUser(asTestAdmin(), fx.userID, "Ana", "aud-ana@example.org", roleViewer)
	require.NoError(t, err)

	assert.Empty(t, assignedVehicles(t, store, fx.userID))
	assert.Equal(t, []string{"aud-v"}, assignedVehicles(t, store, bo))
	assert.True(t, activeTripExists(t, store, bo, "aud-v"))
}

// TestStore_ChangeToViewer_WaitsForAStartingTrip lands a change to viewer
// between a trip start's assignment check and its insert. The change waits
// for the start, then sees its trip and is refused, so the driver isn't left
// on a running trip on a vehicle taken from them.
func TestStore_ChangeToViewer_WaitsForAStartingTrip(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	ctx, cancel := context.WithTimeout(asTestAdmin(), 30*time.Second)
	defer cancel()
	holder, release := holdVehicle(t, store, "aud-v")

	started := make(chan error, 1)
	go func() {
		_, err := store.StartTrip(ctx, fx.userID, "aud-v", "route-1", "")
		started <- err
	}()
	require.Eventually(t, func() bool { return blockedBehind(store, holder, 1) == 1 },
		5*time.Second, 10*time.Millisecond, "the start never got past its assignment check")

	changed := make(chan error, 1)
	go func() {
		_, err := store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleViewer)
		changed <- err
	}()
	require.Eventually(t, func() bool { return len(changed) == 1 || blockedBehind(store, holder, 2) == 1 },
		5*time.Second, 10*time.Millisecond, "the change neither finished nor queued behind the start")
	assert.Empty(t, changed, "the change finished inside the start's window")

	release()
	require.NoError(t, <-started)
	assert.ErrorIs(t, <-changed, ErrViewerWithTrip)
	assert.Equal(t, []string{"aud-v"}, assignedVehicles(t, store, fx.userID))
	assert.True(t, activeTripExists(t, store, fx.userID, "aud-v"))
}

// TestStore_ChangeToViewer_RacesAnAssignment parks one side of a change to
// viewer and an assignment for the same account on its own connection and
// runs the other through the store. Whichever comes first, the account ends
// up a viewer with no vehicles. An import refused that way asks for a new
// preview.
func TestStore_ChangeToViewer_RacesAnAssignment(t *testing.T) {
	store := newAuditTestStore(t)

	// hold runs stmts in a transaction on its own connection and leaves it
	// open, returning its pid and the commit that ends it.
	hold := func(t *testing.T, ctx context.Context, stmts ...string) (int, func()) {
		t.Helper()
		holder, err := dedicatedConn(t).Begin(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
		for _, stmt := range stmts {
			_, err := holder.Exec(ctx, stmt)
			require.NoError(t, err)
		}
		var pid int
		require.NoError(t, holder.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid))
		return pid, func() { require.NoError(t, holder.Commit(ctx)) }
	}

	t.Run("an assignment during the change waits, then is refused", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		ctx, cancel := context.WithTimeout(asTestAdmin(), 30*time.Second)
		defer cancel()
		// Where the change stands once dropAssignments has locked the account.
		id := strconv.FormatInt(fx.userID, 10)
		pid, commit := hold(t, ctx,
			`UPDATE users SET role = 'viewer' WHERE id = `+id,
			`SELECT id FROM users WHERE id = `+id+` FOR UPDATE`)

		done := make(chan error, 1)
		go func() {
			_, err := store.CreateAssignment(ctx, fx.userID, "aud-v2")
			done <- err
		}()
		require.Eventually(t, func() bool { return len(done) == 1 || blockedBehind(store, pid, 1) == 1 },
			5*time.Second, 10*time.Millisecond, "the assignment neither finished nor waited")
		assert.Empty(t, done, "the assignment went ahead while the change to viewer was open")
		commit()
		assert.ErrorIs(t, <-done, ErrAssignToViewer)
		assert.Equal(t, []string{"aud-v"}, assignedVehicles(t, store, fx.userID))
	})

	t.Run("a change during an assignment waits, then drops it", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		ctx, cancel := context.WithTimeout(asTestAdmin(), 30*time.Second)
		defer cancel()
		// An assignment inserted and not yet committed.
		pid, commit := hold(t, ctx,
			`INSERT INTO user_vehicles (user_id, vehicle_id) VALUES (`+strconv.FormatInt(fx.userID, 10)+`, 'aud-v2')`)

		done := make(chan error, 1)
		go func() {
			_, err := store.UpdateUser(ctx, fx.userID, "Ana", "aud-ana@example.org", roleViewer)
			done <- err
		}()
		require.Eventually(t, func() bool { return len(done) == 1 || blockedBehind(store, pid, 1) == 1 },
			5*time.Second, 10*time.Millisecond, "the change neither finished nor waited")
		assert.Empty(t, done, "the change listed the vehicles while an assignment was open")
		commit()
		require.NoError(t, <-done)
		assert.Empty(t, assignedVehicles(t, store, fx.userID), "the assignment that committed first was dropped too")
	})

	t.Run("an import's assignment during the change waits, then asks for a new preview", func(t *testing.T) {
		fx := newAuditFixture(t, store)
		ctx, cancel := context.WithTimeout(asTestAdmin(), 30*time.Second)
		defer cancel()
		id := strconv.FormatInt(fx.userID, 10)
		pid, commit := hold(t, ctx,
			`UPDATE users SET role = 'viewer' WHERE id = `+id,
			`SELECT id FROM users WHERE id = `+id+` FOR UPDATE`)
		// Both of the import's plans read Ana as a driver, since the change
		// isn't committed, and add aud-v2.
		rows := []driverImportRow{{Line: 2, Email: "aud-ana@example.org", Name: "Ana", VehicleIDs: []string{"aud-v2"}}}
		preview := newTestPreview(t)

		type result struct {
			outcomes []importOutcome
			err      error
		}
		done := make(chan result, 1)
		go func() {
			got, err := store.ImportDrivers(ctx, preview, rows, noPassword)
			done <- result{got, err}
		}()
		require.Eventually(t, func() bool { return len(done) == 1 || blockedBehind(store, pid, 1) == 1 },
			5*time.Second, 10*time.Millisecond, "the import neither finished nor waited")
		assert.Empty(t, done, "the import's assignment went ahead while the change to viewer was open")
		commit()
		got := <-done
		require.NoError(t, got.err)
		assert.Equal(t, []importOutcome{
			{Status: importError, Reason: "this account was made a viewer meanwhile; preview the file again"},
		}, got.outcomes)
		assert.Equal(t, []string{"aud-v"}, assignedVehicles(t, store, fx.userID))
	})
}
