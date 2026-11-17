package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// The import tests use vehicles named imp-* and accounts imp-*@example.org,
// and remove them before and after each test.
func newImportTestStore(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	cleanup := func() {
		ctx := context.Background()
		_, err := store.pool.Exec(ctx, `DELETE FROM user_vehicles WHERE vehicle_id LIKE 'imp-%'
			OR user_id IN (SELECT id FROM users WHERE lower(email) LIKE 'imp-%@example.org')`)
		require.NoError(t, err)
		_, err = store.pool.Exec(ctx, `DELETE FROM users WHERE lower(email) LIKE 'imp-%@example.org'`)
		require.NoError(t, err)
		_, err = store.pool.Exec(ctx, `DELETE FROM vehicles WHERE id LIKE 'imp-%'`)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	return store
}

func addImportVehicle(t *testing.T, store *Store, id, label string, active bool) {
	t.Helper()
	ctx := context.Background()
	created, err := store.CreateVehicle(ctx, id, label, "")
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, store.SetVehicleActive(ctx, id, active))
}

func addImportUser(t *testing.T, store *Store, name, email, role string) int64 {
	t.Helper()
	u, err := store.CreateUser(context.Background(), name, email, "original-pass", role)
	require.NoError(t, err)
	return u.ID
}

// importedUser is a users row as stored, password hash included.
type importedUser struct {
	Name, Email, Role, Hash string
	Active                  bool
	SessionsValidAfter      pgtype.Timestamptz
}

func readImportedUser(t *testing.T, store *Store, id int64) importedUser {
	t.Helper()
	var u importedUser
	err := store.pool.QueryRow(context.Background(),
		`SELECT name, email, role, password_hash, active, sessions_valid_after FROM users WHERE id = $1`, id,
	).Scan(&u.Name, &u.Email, &u.Role, &u.Hash, &u.Active, &u.SessionsValidAfter)
	require.NoError(t, err)
	return u
}

func assignedVehicles(t *testing.T, store *Store, userID int64) []string {
	t.Helper()
	assignments, err := store.ListAssignmentsByUser(context.Background(), userID)
	require.NoError(t, err)
	ids := make([]string, 0, len(assignments))
	for _, a := range assignments {
		ids = append(ids, a.VehicleID)
	}
	return ids
}

func countImportRows(t *testing.T, store *Store) (vehicles, users, assignments int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM vehicles WHERE id LIKE 'imp-%'`).Scan(&vehicles))
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) LIKE 'imp-%@example.org'`).Scan(&users))
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM user_vehicles WHERE vehicle_id LIKE 'imp-%'`).Scan(&assignments))
	return vehicles, users, assignments
}

// failWrites installs a trigger on table that runs body for the rows when
// matches, standing in for a write that fails partway through an import.
func failWrites(t *testing.T, store *Store, timing, table, when, body string) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE OR REPLACE FUNCTION imp_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+body+` END $$`)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER imp_test_fail %s ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION imp_test_fail()`, timing, table, when))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := store.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS imp_test_fail ON `+table)
		require.NoError(t, err)
		_, err = store.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS imp_test_fail()`)
		require.NoError(t, err)
	})
}

func statuses(outcomes []importOutcome) []importStatus {
	s := make([]importStatus, len(outcomes))
	for i, o := range outcomes {
		s[i] = o.Status
	}
	return s
}

func noPassword() string { panic("no new account was expected") }

func TestStore_ImportVehicles_RoundTrip(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-old", "Old", true)
	addImportVehicle(t, store, "imp-off", "Off", false)
	rows := []vehicleImportRow{
		{Line: 2, ID: "imp-new", Label: "New", AgencyTag: "north"},
		{Line: 3, ID: "imp-old", Label: "Old 2"},
		{Line: 4, ID: "imp-off", Label: "Off"},
	}

	plan, err := store.PlanVehicleImport(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, []importOutcome{
		{Status: importNew},
		{Status: importUpdate, Changes: []string{"label was Old"}},
		{Status: importUnchanged, Note: "stays deactivated"},
	}, plan)
	n, _, _ := countImportRows(t, store)
	assert.Equal(t, 2, n, "a plan writes nothing")

	got, err := store.ImportVehicles(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, plan, got)

	v, err := store.GetVehicle(ctx, "imp-new")
	require.NoError(t, err)
	assert.Equal(t, VehicleResponse{ID: "imp-new", Label: "New", AgencyTag: "north", Active: true, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}, *v)
	v, err = store.GetVehicle(ctx, "imp-old")
	require.NoError(t, err)
	assert.Equal(t, "Old 2", v.Label)
	v, err = store.GetVehicle(ctx, "imp-off")
	require.NoError(t, err)
	assert.False(t, v.Active, "an import never reactivates a vehicle")

	again, err := store.ImportVehicles(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, []importStatus{importUnchanged, importUnchanged, importUnchanged}, statuses(again))
}

func TestStore_ImportVehicles_AllOrNothing(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-old", "Old", true)
	rows := []vehicleImportRow{
		{Line: 2, ID: "imp-a", Label: "A"},
		{Line: 3, ID: "imp-old", Label: "Changed"},
		{Line: 4, ID: "imp-last", Label: "Last"},
	}

	t.Run("the last write fails", func(t *testing.T) {
		failWrites(t, store, "BEFORE INSERT", "vehicles", "NEW.id = 'imp-last'", `RAISE EXCEPTION 'test failure';`)
		_, err := store.ImportVehicles(ctx, rows)
		require.Error(t, err)
		vehicles, _, _ := countImportRows(t, store)
		assert.Equal(t, 1, vehicles, "no vehicle created")
		v, err := store.GetVehicle(ctx, "imp-old")
		require.NoError(t, err)
		assert.Equal(t, "Old", v.Label, "no vehicle updated")
	})

	t.Run("the last vehicle was created meanwhile", func(t *testing.T) {
		// The insert skips the row, as ON CONFLICT DO NOTHING does when
		// another admin created the id after the plan read it.
		failWrites(t, store, "BEFORE INSERT", "vehicles", "NEW.id = 'imp-last'", `RETURN NULL;`)
		got, err := store.ImportVehicles(ctx, rows)
		require.NoError(t, err)
		assert.Equal(t, importOutcome{Status: importError, Reason: "vehicle id already exists"}, got[2])
		vehicles, _, _ := countImportRows(t, store)
		assert.Equal(t, 1, vehicles)
		v, err := store.GetVehicle(ctx, "imp-old")
		require.NoError(t, err)
		assert.Equal(t, "Old", v.Label)
	})

	t.Run("the last vehicle was removed meanwhile", func(t *testing.T) {
		failWrites(t, store, "BEFORE UPDATE", "vehicles", "NEW.id = 'imp-old'", `RETURN NULL;`)
		got, err := store.ImportVehicles(ctx, []vehicleImportRow{rows[0], rows[1]})
		require.NoError(t, err)
		assert.Equal(t, importOutcome{Status: importError, Reason: "vehicle not found"}, got[1])
		vehicles, _, _ := countImportRows(t, store)
		assert.Equal(t, 1, vehicles, "the vehicle before it isn't created")
	})
}

func TestStore_ImportDrivers_RoundTrip(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-v1", "V1", true)
	addImportVehicle(t, store, "imp-v2", "V2", true)
	rows := []driverImportRow{{Line: 2, Email: "imp-ana@example.org", Name: "Ana", VehicleIDs: []string{"imp-v1", "imp-v2"}}}

	plan, err := store.PlanDriverImport(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, []importOutcome{{Status: importNew}}, plan)
	_, users, _ := countImportRows(t, store)
	assert.Equal(t, 0, users, "a plan writes nothing")

	got, err := store.ImportDrivers(ctx, rows, func() string { return "abcd-efgh-jkmn" })
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, importNew, got[0].Status)
	assert.Equal(t, "abcd-efgh-jkmn", got[0].Password)
	require.NotZero(t, got[0].UserID)

	u := readImportedUser(t, store, got[0].UserID)
	assert.Equal(t, "Ana", u.Name)
	assert.Equal(t, "imp-ana@example.org", u.Email)
	assert.Equal(t, roleDriver, u.Role)
	assert.True(t, u.Active)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte("abcd-efgh-jkmn")))
	assert.ElementsMatch(t, []string{"imp-v1", "imp-v2"}, assignedVehicles(t, store, got[0].UserID))

	again, err := store.ImportDrivers(ctx, rows, noPassword)
	require.NoError(t, err)
	assert.Equal(t, []importOutcome{{Status: importUnchanged, UserID: got[0].UserID}}, again)
}

func TestStore_ImportDrivers_ChangesOnlyNameAndAssignments(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-v1", "V1", true)
	addImportVehicle(t, store, "imp-v2", "V2", true)
	bo := addImportUser(t, store, "Bo", "Imp-Bo@Example.org", roleDriver)
	_, err := store.CreateAssignment(ctx, bo, "imp-v1")
	require.NoError(t, err)
	cy := addImportUser(t, store, "Cy", "imp-cy@example.org", roleDriver)
	require.NoError(t, store.SetUserActiveAndRevokeSessions(ctx, cy, false))
	beforeBo, beforeCy := readImportedUser(t, store, bo), readImportedUser(t, store, cy)

	rows := []driverImportRow{
		{Line: 2, Email: "imp-bo@example.org", Name: "Bo Bell", VehicleIDs: []string{"imp-v1", "imp-v2"}},
		{Line: 3, Email: "imp-cy@example.org", Name: "Cy"},
	}
	want := []importOutcome{
		{Status: importUpdate, Changes: []string{"name was Bo", "adds imp-v2"}},
		{Status: importUnchanged, Note: "stays deactivated"},
	}
	plan, err := store.PlanDriverImport(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, want, plan)

	got, err := store.ImportDrivers(ctx, rows, noPassword)
	require.NoError(t, err)
	want[0].UserID, want[1].UserID = bo, cy
	assert.Equal(t, want, got)

	afterBo := readImportedUser(t, store, bo)
	assert.Equal(t, "Bo Bell", afterBo.Name)
	afterBo.Name = beforeBo.Name
	assert.Equal(t, beforeBo, afterBo, "the email as stored, the password, the role, the active flag and the sessions stay")
	assert.ElementsMatch(t, []string{"imp-v1", "imp-v2"}, assignedVehicles(t, store, bo))
	assert.Equal(t, beforeCy, readImportedUser(t, store, cy))
	_, users, _ := countImportRows(t, store)
	assert.Equal(t, 2, users, "the mixed-case email matched its account rather than making a second one")
}

func TestStore_ImportDrivers_NeverTouchesAnAdmin(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-v1", "V1", true)
	admin := addImportUser(t, store, "Admin", "Imp-Admin@example.org", roleAdmin)
	before := readImportedUser(t, store, admin)
	rows := []driverImportRow{{Line: 2, Email: "imp-admin@example.org", Name: "Renamed", VehicleIDs: []string{"imp-v1"}}}

	want := []importOutcome{{Status: importError, Reason: "this email belongs to an admin account"}}
	plan, err := store.PlanDriverImport(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, want, plan)
	got, err := store.ImportDrivers(ctx, rows, noPassword)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, before, readImportedUser(t, store, admin))
	assert.Empty(t, assignedVehicles(t, store, admin))
}

func TestStore_ImportDrivers_RowErrors(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-off", "Off", false)
	addImportUser(t, store, "Dee", "Imp-Dee@example.org", roleDriver)
	addImportUser(t, store, "Dee 2", "imp-dee@EXAMPLE.org", roleDriver)

	plan, err := store.PlanDriverImport(ctx, []driverImportRow{
		{Line: 2, Email: "imp-eve@example.org", Name: "Eve", VehicleIDs: []string{"imp-off", "imp-missing"}},
		{Line: 3, Email: "imp-dee@example.org", Name: "Dee"},
	})
	require.NoError(t, err)
	assert.Equal(t, []importOutcome{
		{Status: importError, Reason: "vehicle imp-off is inactive; vehicle imp-missing not found"},
		{Status: importError, Reason: "more than one account has this email, in different cases"},
	}, plan)
}

func TestStore_ImportDrivers_AllOrNothing(t *testing.T) {
	store := newImportTestStore(t)
	ctx := context.Background()
	addImportVehicle(t, store, "imp-v1", "V1", true)
	addImportVehicle(t, store, "imp-v2", "V2", true)
	addImportVehicle(t, store, "imp-off", "Off", false)
	bo := addImportUser(t, store, "Bo", "imp-bo@example.org", roleDriver)
	before := readImportedUser(t, store, bo)
	rows := func(last driverImportRow) []driverImportRow {
		return []driverImportRow{
			{Line: 2, Email: "imp-ana@example.org", Name: "Ana", VehicleIDs: []string{"imp-v1"}},
			{Line: 3, Email: "imp-bo@example.org", Name: "Bo Bell", VehicleIDs: []string{"imp-v1"}},
			last,
		}
	}
	nothingWritten := func(t *testing.T) {
		t.Helper()
		_, users, assignments := countImportRows(t, store)
		assert.Equal(t, 1, users, "no account created")
		assert.Equal(t, 0, assignments, "no vehicle assigned")
		assert.Equal(t, before, readImportedUser(t, store, bo), "no account changed")
	}

	t.Run("a bad last row", func(t *testing.T) {
		got, err := store.ImportDrivers(ctx, rows(driverImportRow{Line: 4, Email: "imp-cy@example.org", Name: "Cy", VehicleIDs: []string{"imp-off"}}), noPassword)
		require.NoError(t, err)
		assert.Equal(t, []importStatus{importNew, importUpdate, importError}, statuses(got))
		nothingWritten(t)
	})

	t.Run("the last write fails", func(t *testing.T) {
		failWrites(t, store, "BEFORE INSERT", "user_vehicles", "NEW.vehicle_id = 'imp-v2'", `RAISE EXCEPTION 'test failure';`)
		_, err := store.ImportDrivers(ctx, rows(driverImportRow{Line: 4, Email: "imp-cy@example.org", Name: "Cy", VehicleIDs: []string{"imp-v2"}}), generateDriverPassword)
		require.Error(t, err)
		nothingWritten(t)
	})

	t.Run("the last email was taken meanwhile", func(t *testing.T) {
		failWrites(t, store, "BEFORE INSERT", "users", "NEW.email = 'imp-cy@example.org'",
			`RAISE unique_violation USING CONSTRAINT = 'users_email_key';`)
		got, err := store.ImportDrivers(ctx, rows(driverImportRow{Line: 4, Email: "imp-cy@example.org", Name: "Cy"}), generateDriverPassword)
		require.NoError(t, err)
		assert.Equal(t, importOutcome{Status: importError, Reason: "email already exists"}, got[2])
		nothingWritten(t)
	})

	t.Run("the last vehicle was assigned meanwhile", func(t *testing.T) {
		failWrites(t, store, "BEFORE INSERT", "user_vehicles", "NEW.vehicle_id = 'imp-v2'",
			`RAISE unique_violation USING CONSTRAINT = 'user_vehicles_pkey';`)
		got, err := store.ImportDrivers(ctx, rows(driverImportRow{Line: 4, Email: "imp-cy@example.org", Name: "Cy", VehicleIDs: []string{"imp-v2"}}), generateDriverPassword)
		require.NoError(t, err)
		assert.Equal(t, importOutcome{Status: importError, Reason: "vehicle imp-v2 was assigned meanwhile; preview the file again"}, got[2])
		nothingWritten(t)
	})

	t.Run("an account was removed between the two plans", func(t *testing.T) {
		gone := addImportUser(t, store, "Gone", "imp-gone@example.org", roleDriver)
		got, err := store.ImportDrivers(ctx, rows(driverImportRow{Line: 4, Email: "imp-gone@example.org", Name: "Gone"}), func() string {
			_, err := store.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, gone)
			require.NoError(t, err)
			return "abcd-efgh-jkmn"
		})
		require.NoError(t, err)
		assert.Equal(t, importOutcome{Status: importError, Reason: "an account with this email was removed meanwhile; preview the file again"}, got[2])
		nothingWritten(t)
	})

	// Last, since it leaves Bo an admin.
	t.Run("an account became an admin between the two plans", func(t *testing.T) {
		got, err := store.ImportDrivers(ctx, rows(driverImportRow{Line: 4, Email: "imp-cy@example.org", Name: "Cy"}), func() string {
			_, err := store.pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, bo)
			require.NoError(t, err)
			return "abcd-efgh-jkmn"
		})
		require.NoError(t, err)
		assert.Equal(t, importOutcome{Status: importError, Reason: "this email belongs to an admin account"}, got[1])
		_, users, assignments := countImportRows(t, store)
		assert.Equal(t, 1, users, "no account created")
		assert.Equal(t, 0, assignments, "no vehicle assigned")
		assert.Equal(t, "Bo", readImportedUser(t, store, bo).Name)
	})
}

// A request that ends while passwords hash writes nothing.
func TestStore_ImportDrivers_CanceledWhileHashing(t *testing.T) {
	store := newImportTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := store.ImportDrivers(ctx, []driverImportRow{{Line: 2, Email: "imp-ana@example.org", Name: "Ana"}}, func() string {
		cancel()
		return "abcd-efgh-jkmn"
	})
	require.ErrorIs(t, err, context.Canceled)
	_, users, _ := countImportRows(t, store)
	assert.Equal(t, 0, users)
}

// The whole path through the admin pages against the database: vehicles,
// then drivers assigned to them, each previewed and confirmed, and the new
// driver can sign in with the password the result page shows.
func TestAdminImport_DatabaseRoundTrip(t *testing.T) {
	store := newImportTestStore(t)
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ui, err := newAdminUI(store, tracker, newAlertWatcher(store, testAlertSettings), nil, newFeedHealth(time.Now), testSecret, NewLoginRateLimiter(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute})
	require.NoError(t, err)
	t.Cleanup(ui.loginLimiter.Stop)
	// The test session's admin isn't in the database, which the session check
	// would refuse; everything else is the real store.
	ui.tokenChecker = newFakeRevocations()

	preview := uploadImport(t, ui, "vehicles", vehiclesHeader+"imp-101,Bus 101,north\nimp-102,Bus 102,north\n")
	require.Equal(t, http.StatusOK, preview.Code, pageText(preview))
	w := confirmImport(t, ui, "vehicles", confirmForm(t, preview.Body.String()))
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	vehicles, _, _ := countImportRows(t, store)
	require.Equal(t, 2, vehicles)

	preview = uploadImport(t, ui, "drivers", driversHeader+"Imp-Ana@Example.org,Ana,imp-101 imp-102\n")
	require.Equal(t, http.StatusOK, preview.Code, pageText(preview))
	w = confirmImport(t, ui, "drivers", confirmForm(t, preview.Body.String()))
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	m := regexp.MustCompile(`select-all">([^<]*)</span>`).FindStringSubmatch(w.Body.String())
	require.NotNil(t, m, "the result page shows the new driver's password")

	user, err := store.GetUserByEmail(context.Background(), "imp-ana@example.org")
	require.NoError(t, err)
	assert.Equal(t, roleDriver, user.Role)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(m[1])))
	assert.Contains(t, w.Body.String(), fmt.Sprintf(`href="/admin/driver-setup?user=%d"`, user.ID))
	assert.ElementsMatch(t, []string{"imp-101", "imp-102"}, assignedVehicles(t, store, user.ID))

	// A file whose last row fails can't be imported, even with a signed
	// preview: nothing of it is written.
	require.NoError(t, store.SetVehicleActive(context.Background(), "imp-102", false))
	file := driversHeader + "imp-bo@example.org,Bo,imp-101\nimp-cy@example.org,Cy,imp-102\n"
	preview = uploadImport(t, ui, "drivers", file)
	require.Equal(t, http.StatusUnprocessableEntity, preview.Code)
	assert.Contains(t, pageText(preview), "vehicle imp-102 is inactive")
	w = confirmImport(t, ui, "drivers", signedForm("drivers", file, time.Now()))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	_, users, assignments := countImportRows(t, store)
	assert.Equal(t, 1, users)
	assert.Equal(t, 2, assignments)
}
