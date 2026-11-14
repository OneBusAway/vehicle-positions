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

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func stringptr(v string) *string {
	return &v
}

// occupancyStatusNames is every occupancy_status a report may carry, in the
// order GTFS-RT lists them. It is spelled out rather than derived from
// occupancyStatuses, so dropping or renaming a value there fails these tests.
var occupancyStatusNames = []string{
	"EMPTY",
	"MANY_SEATS_AVAILABLE",
	"FEW_SEATS_AVAILABLE",
	"STANDING_ROOM_ONLY",
	"CRUSHED_STANDING_ROOM_ONLY",
	"FULL",
	"NOT_ACCEPTING_PASSENGERS",
}

// occupancyStatusMigration adds location_points.occupancy_status.
const occupancyStatusMigration = 21

// savingStore authorizes every report and keeps a copy of the last one saved.
type savingStore struct {
	last *LocationReport
}

func (s *savingStore) AuthorizeLocation(_ context.Context, _ *LocationReport) error {
	return nil
}

func (s *savingStore) SaveLocation(_ context.Context, loc *LocationReport) error {
	saved := *loc
	s.last = &saved
	return nil
}

func TestHandlePostLocation_OccupancyStatus(t *testing.T) {
	const wantError = "occupancy_status must be one of EMPTY, MANY_SEATS_AVAILABLE, FEW_SEATS_AVAILABLE, " +
		"STANDING_ROOM_ONLY, CRUSHED_STANDING_ROOM_ONLY, FULL, NOT_ACCEPTING_PASSENGERS"

	type testCase struct {
		name       string
		occupancy  *string
		wantStatus int
	}
	tests := []testCase{{"absent", nil, http.StatusCreated}}
	for _, name := range occupancyStatusNames {
		tests = append(tests, testCase{name, stringptr(name), http.StatusCreated})
	}
	tests = append(tests,
		testCase{"empty string", stringptr(""), http.StatusBadRequest},
		testCase{"lowercase", stringptr("full"), http.StatusBadRequest},
		testCase{"surrounding space", stringptr("FULL "), http.StatusBadRequest},
		testCase{"unknown name", stringptr("HALF_FULL"), http.StatusBadRequest},
		// GTFS-RT values a driver cannot report: leaving the field out says
		// NO_DATA_AVAILABLE, and NOT_BOARDABLE is for a vehicle that never
		// takes passengers.
		testCase{"NO_DATA_AVAILABLE", stringptr("NO_DATA_AVAILABLE"), http.StatusBadRequest},
		testCase{"NOT_BOARDABLE", stringptr("NOT_BOARDABLE"), http.StatusBadRequest},
	)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := NewTracker(5 * time.Minute)
			t.Cleanup(tracker.Stop)
			limiter := NewVehicleRateLimiter()
			t.Cleanup(limiter.Stop)
			store := &savingStore{}
			handler := handlePostLocation(store, tracker, limiter)

			loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix(), OccupancyStatus: tc.occupancy}
			w := postLocationWithClaims(handler, loc, jwt.MapClaims{"sub": "driver-1"})
			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())

			if tc.wantStatus != http.StatusCreated {
				var resp map[string]string
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Equal(t, wantError, resp["error"])
				assert.Nil(t, store.last, "a rejected report is not stored")
				assert.Empty(t, tracker.ActiveVehicles(), "a rejected report does not reach the feed")
				return
			}

			require.NotNil(t, store.last)
			assert.Equal(t, tc.occupancy, store.last.OccupancyStatus, "the report is stored with what it said")
			active := tracker.ActiveVehicles()
			require.Len(t, active, 1)
			var want string
			if tc.occupancy != nil {
				want = *tc.occupancy
			}
			assert.Equal(t, want, active[0].OccupancyStatus)
		})
	}
}

// The report goes in as JSON the way the apps send it and comes out of the
// protobuf feed, so the key's spelling and the wire encoding are both pinned.
// EMPTY is the enum's zero value: it must be published, not taken for unset.
func TestOccupancyStatus_FeedFollowsLatestReport(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	limiter := NewVehicleRateLimiter()
	t.Cleanup(limiter.Stop)
	post := handlePostLocation(&mockStore{}, tracker, limiter)
	feed := handleGetFeed(tracker, riderOff{})

	report := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": "driver-1"}))
		w := httptest.NewRecorder()
		post(w, req)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}
	published := func() *gtfsrt.VehiclePosition {
		t.Helper()
		w := getFeed(feed, "")
		require.Equal(t, http.StatusOK, w.Code)
		var msg gtfsrt.FeedMessage
		require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &msg))
		require.Len(t, msg.Entity, 1)
		return msg.Entity[0].GetVehicle()
	}

	now := time.Now().Unix()
	report(fmt.Sprintf(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":%d,"occupancy_status":"EMPTY"}`, now))
	assert.Equal(t, gtfsrt.VehiclePosition_EMPTY.Enum(), published().OccupancyStatus)

	report(fmt.Sprintf(`{"vehicle_id":"bus-1","latitude":1.001,"longitude":2,"timestamp":%d}`, now+1))
	vp := published()
	assert.Equal(t, float32(1.001), vp.GetPosition().GetLatitude(), "the feed shows the later report")
	assert.Nil(t, vp.OccupancyStatus, "a report that does not say clears what the last one said")
}

func TestTracker_OccupancyStatusFollowsLatestReport(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	report := func(occupancy *string) string {
		t.Helper()
		loc := &LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix(), OccupancyStatus: occupancy}
		require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
		active := tracker.ActiveVehicles()
		require.Len(t, active, 1)
		return active[0].OccupancyStatus
	}

	assert.Equal(t, "STANDING_ROOM_ONLY", report(stringptr("STANDING_ROOM_ONLY")))
	assert.Equal(t, "FULL", report(stringptr("FULL")), "a new value replaces the last")
	assert.Empty(t, report(nil), "a report that does not say clears it")

	// Update is how the tracker is seeded at startup.
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix(), OccupancyStatus: stringptr("FEW_SEATS_AVAILABLE")})
	byID := make(map[string]string)
	for _, v := range tracker.ActiveVehicles() {
		byID[v.VehicleID] = v.OccupancyStatus
	}
	assert.Equal(t, "FEW_SEATS_AVAILABLE", byID["bus-2"], "a seeded vehicle publishes its latest report's value")
}

func TestBuildFeed_OccupancyStatus(t *testing.T) {
	now := time.Now().Unix()
	est := sampleEstimate()
	// Every driver is on the rider estimate's trip, so the rider entity sits
	// beside vehicles that do carry a value.
	vehicles := []*VehicleState{
		{VehicleID: "unsaid", TripID: est.Key.TripID, StartDate: est.Key.StartDate, Latitude: 47.6, Longitude: -122.33, Timestamp: now},
	}
	for i, name := range occupancyStatusNames {
		vehicles = append(vehicles, &VehicleState{
			VehicleID: "bus-" + strconv.Itoa(i), TripID: est.Key.TripID, StartDate: est.Key.StartDate,
			Latitude: 47.6, Longitude: -122.33, Timestamp: now, OccupancyStatus: name,
		})
	}

	feed := buildFeed(vehicles, []rider.TripEstimate{est})
	assert.Empty(t, validateFeedCompliance(t, feed))

	byID := make(map[string]*gtfsrt.VehiclePosition)
	for _, e := range feed.Entity {
		byID[e.GetId()] = e.GetVehicle()
	}
	require.Len(t, byID, len(occupancyStatusNames)+2)
	for i, name := range occupancyStatusNames {
		vp := byID["bus-"+strconv.Itoa(i)]
		require.NotNil(t, vp.OccupancyStatus, "%s must be set, not left unset", name)
		assert.Equal(t, name, vp.GetOccupancyStatus().String())
	}
	assert.Nil(t, byID["unsaid"].OccupancyStatus, "a vehicle whose latest report did not say publishes none")
	riderVP := byID[riderEntityID(est.Key)]
	require.NotNil(t, riderVP)
	assert.Nil(t, riderVP.OccupancyStatus, "a rider-reported vehicle never carries one, even on a trip whose driver said")
}

// The spec's enum is what a generated client offers, so it must be exactly the
// names the handler accepts.
func TestOpenAPI_OccupancyStatusEnumMatchesCode(t *testing.T) {
	t.Parallel()
	node, ok := loadOpenAPISpec(t).mappings()["#/components/schemas/LocationReport/properties/occupancy_status"]
	require.True(t, ok, "LocationReport must document occupancy_status")
	want := make([]any, len(occupancyStatusNames))
	for i, name := range occupancyStatusNames {
		want[i] = name
	}
	assert.Equal(t, want, node["enum"])
}

// The tracker is seeded from GetRecentLocations at startup, so the feed's
// occupancy_status has to come back after a restart the way the vehicle's
// latest report left it.
func TestStore_OccupancyStatusRoundTrip(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()
	now := time.Now().Unix()

	save := func(vehicleID string, ts int64, occupancy *string) {
		t.Helper()
		require.NoError(t, store.SaveLocation(ctx, &LocationReport{
			VehicleID: vehicleID, Latitude: 1, Longitude: 2, Timestamp: ts, DriverID: "7", OccupancyStatus: occupancy,
		}))
	}
	save("bus-full", now, stringptr("FULL"))
	save("bus-unsaid", now, nil)
	save("bus-cleared", now-10, stringptr("CRUSHED_STANDING_ROOM_ONLY"))
	save("bus-cleared", now, nil)

	onActiveTrips(t, store)
	locs, err := store.GetRecentLocations(ctx, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	byID := make(map[string]*LocationReport)
	for _, loc := range locs {
		byID[loc.VehicleID] = loc
	}
	require.Len(t, byID, 3)
	require.NotNil(t, byID["bus-full"].OccupancyStatus)
	assert.Equal(t, "FULL", *byID["bus-full"].OccupancyStatus)
	assert.Nil(t, byID["bus-unsaid"].OccupancyStatus)
	assert.Nil(t, byID["bus-cleared"].OccupancyStatus, "the latest report's value, not the last one that said")

	var stored *string
	require.NoError(t, store.pool.QueryRow(ctx,
		"SELECT occupancy_status FROM location_points WHERE vehicle_id = 'bus-unsaid'").Scan(&stored))
	assert.Nil(t, stored, "a report that did not say is stored as NULL, not as an empty string")
}

// A value the handler would have refused is refused by the column too, and
// the transaction leaves nothing behind.
func TestStore_SaveLocation_RejectsUnknownOccupancyStatus(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	err := store.SaveLocation(ctx, &LocationReport{
		VehicleID: "bus-lower", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix(), OccupancyStatus: stringptr("full"),
	})
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "location_points_occupancy_status_check", pgErr.ConstraintName)

	var vehicles int
	require.NoError(t, store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM vehicles WHERE id = 'bus-lower'").Scan(&vehicles))
	assert.Zero(t, vehicles, "the vehicle upsert rolled back with the point")
}

// History uploads do not take occupancy_status, but the store keeps whatever
// a report it is handed carries, so taking it there would need no store change.
func TestStore_SaveLocationHistory_KeepsOccupancyStatus(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	userID := setupTripUser(t, store, "Offline Driver", "offline-occupancy@test.com", "bus-off", "Bus Off")
	driverID := strconv.FormatInt(userID, 10)
	ts := time.Now().Add(-time.Hour).Unix()
	saved, err := store.SaveLocationHistory(ctx, userID, "bus-off", []*LocationReport{
		{VehicleID: "bus-off", DriverID: driverID, Latitude: 1, Longitude: 2, Timestamp: ts, OccupancyStatus: stringptr("STANDING_ROOM_ONLY")},
		{VehicleID: "bus-off", DriverID: driverID, Latitude: 1, Longitude: 2, Timestamp: ts + 5},
	})
	require.NoError(t, err)
	require.Equal(t, 2, saved)

	rows, err := store.pool.Query(ctx, "SELECT occupancy_status FROM location_points WHERE vehicle_id = 'bus-off' ORDER BY timestamp")
	require.NoError(t, err)
	stored, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	require.NoError(t, err)
	require.Len(t, stored, 2)
	require.NotNil(t, stored[0])
	assert.Equal(t, "STANDING_ROOM_ONLY", *stored[0])
	assert.Nil(t, stored[1])
}

// The migration runs on a database of its own: taking the shared test
// database down a version would drop the column under every other test, and
// a fresh one proves the up migration itself adds it.
func TestMigration_OccupancyStatusUpAndDown(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	name := fmt.Sprintf("occupancy_migration_%d", time.Now().UnixNano())
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

	hasColumn := func() bool {
		t.Helper()
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'location_points' AND column_name = 'occupancy_status')`).Scan(&exists))
		return exists
	}
	timestamp := 0
	insert := func(occupancy *string) error {
		timestamp++
		_, err := conn.Exec(ctx,
			"INSERT INTO location_points (vehicle_id, latitude, longitude, timestamp, occupancy_status) VALUES ('bus-1', 1, 2, $1, $2)",
			timestamp, occupancy)
		return err
	}

	require.NoError(t, m.Migrate(occupancyStatusMigration-1))
	require.False(t, hasColumn())
	_, err = conn.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('bus-1')")
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "INSERT INTO location_points (vehicle_id, latitude, longitude, timestamp) VALUES ('bus-1', 1, 2, 1000)")
	require.NoError(t, err)

	require.NoError(t, m.Migrate(occupancyStatusMigration))
	require.True(t, hasColumn())
	var existing *string
	require.NoError(t, conn.QueryRow(ctx, "SELECT occupancy_status FROM location_points WHERE timestamp = 1000").Scan(&existing))
	assert.Nil(t, existing, "a row from before the migration reads as NULL")
	for _, name := range occupancyStatusNames {
		assert.NoError(t, insert(stringptr(name)), "the column must accept %s", name)
	}
	assert.NoError(t, insert(nil))
	for _, bad := range []string{"", "full", "NO_DATA_AVAILABLE", "NOT_BOARDABLE"} {
		var pgErr *pgconn.PgError
		if assert.ErrorAs(t, insert(stringptr(bad)), &pgErr, "the column must refuse %q", bad) {
			assert.Equal(t, "location_points_occupancy_status_check", pgErr.ConstraintName)
		}
	}

	require.NoError(t, m.Migrate(occupancyStatusMigration-1))
	assert.False(t, hasColumn(), "down drops the column")
	require.NoError(t, m.Migrate(occupancyStatusMigration), "up applies again after down")
	assert.True(t, hasColumn())
}
