package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	// feedSnapshotCount is how many times the feed is fetched. The validator
	// compares each snapshot with the one before it (E017, E018, W007), so it
	// needs more than one.
	feedSnapshotCount = 4
	// feedSnapshotSpeed is how fast every simulated vehicle moves, in m/s.
	feedSnapshotSpeed = 10.0
)

// TestWriteFeedSnapshots writes both GTFS-RT feeds to FEED_SNAPSHOT_DIR once
// per round of reports, for MobilityData's gtfs-realtime-validator to check
// against the GTFS feed it writes beside them, gtfs.zip
// (.github/workflows/gtfs-rt-validator.yml, and docs/development.md for a
// local run). It is skipped unless FEED_SNAPSHOT_DIR is set, because the
// rounds are rateInterval apart.
//
// gtfs.zip is rider/testdata/fixture.zip with TB added, timed around now
// (nowFixture), which bus-101 drives. The fixture's own trips would have it
// hours off its timetable at most times of day, and the trip updates feed
// leaves out a vehicle that far off (maxScheduleDeviation). TB leaves ST2's
// times blank, so bus-101's stop_time_update is at a stop with no time in the
// GTFS feed, the case E046 is about.
//
// Each feed gets its own directory, because the validator compares each
// snapshot with the one before it. A third, combined, holds both feeds'
// entities in one message: the validator runs its cross-feed rules, E047 among
// them (a trip paired with a different vehicle.id in each feed), only on a
// message that carries both.
//
// The feeds are the production mux's. noopStore stands in for Postgres, so
// every report is authorized and none is saved; the rest, from the auth
// middleware to the protobuf bytes, is what a deployed server runs.
func TestWriteFeedSnapshots(t *testing.T) {
	dir := os.Getenv("FEED_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("FEED_SNAPSHOT_DIR not set; skipping feed snapshot export")
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	existing, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, existing, "FEED_SNAPSHOT_DIR must be empty: the validator reads every file in it")
	for _, feed := range feedSnapshotDirs {
		require.NoError(t, os.Mkdir(filepath.Join(dir, feed), 0o755))
	}

	gtfsFeed, startDate := nowFixture(t, time.Now())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtfs.zip"), gtfsFeed, 0o644))
	ix, err := rider.IndexFromBytes(gtfsFeed, "gtfs.zip", time.Now())
	require.NoError(t, err)
	t1, tb, t3 := mustTrip(t, ix, "T1"), mustTrip(t, ix, "TB"), mustTrip(t, ix, "T3")

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	limiter := NewVehicleRateLimiter()
	t.Cleanup(limiter.Stop)

	// The schedule window is a full day either way, as in the rider smoke
	// test in docs/development.md, so T1 matches at whatever hour this runs.
	thresholds := rider.DefaultThresholds()
	thresholds.ScheduleEarly, thresholds.ScheduleLate = 24*time.Hour, 24*time.Hour
	// As main.go wires it, so the driver on a trip carries its stop fields.
	tracker.SetTripLocator(newTripLocator(func() *rider.Index { return ix }, thresholds.MaxShapeDistance))
	agg := rider.NewAggregator(thresholds, ix.Timezone())
	trusted := trustedSources{feed: rider.NewTrustedFeed(nil, nil, defaultTrustedMaxAge), tracker: tracker}
	riderSvc := newRiderService(newFakeRiderStore(), agg, func() *rider.Index { return ix }, trusted, testSecret, defaultRiderJWTTTL, false)
	t.Cleanup(riderSvc.Stop)

	// The ride is opened on the aggregator rather than over HTTP: the HTTP
	// start refuses a trip on a day the fixture calendar does not run it, and
	// this has to work on a Sunday and after the calendar ends. A trusted
	// rider is published without a second source to corroborate it.
	const rideID = "snapshot-ride"
	agg.Add(rider.NewSession(rideID, "snapshot-rider", rider.TripKey{TripID: t1.ID, StartDate: startDate}, t1, rider.TierTrusted, time.Now()))

	srv := httptest.NewServer(newMux(&noopStore{}, tracker, limiter, testSecret, testTTLs, time.Now(), nil, nil, false, false, "", riderSvc, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), nil, newFeedHealth(time.Now)))
	t.Cleanup(srv.Close)

	// One driver per vehicle: the report limiter is keyed on the token's subject.
	tokens := make(map[string]string)
	for i, vehicleID := range []string{"bus-101", "bus-102", "bus-103"} {
		token, err := generateJWT(&User{ID: int64(101 + i), Email: vehicleID + "@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
		require.NoError(t, err)
		tokens[vehicleID] = token
	}
	postLocation := func(report LocationReport) {
		body, err := json.Marshal(report)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/locations", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokens[report.VehicleID])
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "%s: %s", report.VehicleID, respBody)
	}

	step := feedSnapshotSpeed * rateInterval.Seconds()
	riderPoint := func(along float64, at time.Time) rider.Point {
		return rider.Point{Pos: t1.Shape.PointAt(along), Accuracy: 5, Speed: feedSnapshotSpeed, Bearing: t1.Shape.BearingAt(along), Timestamp: at}
	}
	// riderLead is how many fixes the rider has already sent when the first
	// snapshot is taken: a ride is not published until enough of them match.
	const riderLead = 5

	for round := range feedSnapshotCount {
		if round > 0 {
			// Real time has to pass: the limiter allows a driver one report
			// per rateInterval, and two snapshots that differ within the same
			// second share a header timestamp, which is E017.
			time.Sleep(rateInterval)
		}
		now := time.Now()
		along := float64(round) * step

		// A step along already, so bus-101 has left its first stop and has a
		// delay in every round. It is heading to ST2 in all of them.
		onTrip := tb.Shape.PointAt(along + step)
		onRoute := t1.Shape.PointAt(t1.Shape.Length/2 + along)
		parked := t3.Shape.PointAt(t3.Shape.Length / 2)
		// A driver on a trip who says how full the vehicle is, a driver who
		// knows only the route, and a vehicle that is not in service and sends
		// no bearing or speed.
		postLocation(LocationReport{
			VehicleID: "bus-101", TripID: tb.ID, RouteID: tb.RouteID, StartDate: startDate,
			Latitude: onTrip.Lat, Longitude: onTrip.Lon,
			Bearing: float64ptr(tb.Shape.BearingAt(along + step)), Speed: float64ptr(feedSnapshotSpeed),
			Timestamp: now.Unix(), OccupancyStatus: stringptr("FEW_SEATS_AVAILABLE"),
		})
		postLocation(LocationReport{
			VehicleID: "bus-102", RouteID: t1.RouteID, StartDate: startDate,
			Latitude: onRoute.Lat, Longitude: onRoute.Lon,
			Bearing: float64ptr(t1.Shape.BearingAt(t1.Shape.Length/2 + along)), Speed: float64ptr(feedSnapshotSpeed),
			Timestamp: now.Unix(),
		})
		postLocation(LocationReport{VehicleID: "bus-103", Latitude: parked.Lat, Longitude: parked.Lon, Timestamp: now.Unix()})

		var points []rider.Point
		if round == 0 {
			for i := range riderLead {
				points = append(points, riderPoint(float64(i)*step, now.Add(-time.Duration(riderLead-i)*rateInterval)))
			}
		}
		points = append(points, riderPoint(float64(riderLead)*step+along, now))
		res, err := agg.ApplyBatch(rideID, points, func(key rider.TripKey) (rider.TrustedVehicle, bool) {
			return trusted.Lookup(key, now)
		}, now)
		require.NoError(t, err)
		require.True(t, res.Published, "the rider's trip must be in the feed: %+v", res.Summary)

		positionsBody, positions := fetchSnapshot(t, srv, "/gtfs-rt/vehicle-positions")
		tripUpdatesBody, tripUpdates := fetchSnapshot(t, srv, "/gtfs-rt/trip-updates")
		fetched := time.Now().UTC()

		var ids []string
		for _, e := range positions.GetEntity() {
			ids = append(ids, e.GetId())
			if e.GetId() == "bus-101" {
				require.NotEmpty(t, e.GetVehicle().GetStopId(),
					"the driver on a trip must be placed on it, or the validator never sees a driver's stop fields")
			}
		}
		require.ElementsMatch(t, []string{"bus-101", "bus-102", "bus-103", "rider:T1:" + startDate}, ids,
			"a snapshot missing an entity would pass the validator while checking less")
		require.Len(t, tripUpdates.GetEntity(), 1,
			"only bus-101 has a measured delay; without it the validator checks no trip update")
		tripUpdate := tripUpdates.GetEntity()[0].GetTripUpdate()
		require.Equal(t, "bus-101", tripUpdate.GetVehicle().GetId())
		require.Equal(t, "ST2", tripUpdate.GetStopTimeUpdate()[0].GetStopId(),
			"without bus-101 heading to ST2 the validator never sees a stop_time_update at an untimed stop")

		// Both feeds give an entity its vehicle's id, and ids must be unique
		// within a message, so the trip updates go into it under ids of their
		// own.
		combined := proto.Clone(positions).(*gtfsrt.FeedMessage)
		for _, e := range tripUpdates.GetEntity() {
			e = proto.Clone(e).(*gtfsrt.FeedEntity)
			e.Id = proto.String("trip-update:" + e.GetId())
			combined.Entity = append(combined.Entity, e)
		}
		combined.Header.Timestamp = proto.Uint64(max(positions.GetHeader().GetTimestamp(), tripUpdates.GetHeader().GetTimestamp()))
		for _, feed := range []*gtfsrt.FeedMessage{positions, tripUpdates, combined} {
			requireUniqueEntityIDs(t, feed)
		}
		combinedBody, err := proto.Marshal(combined)
		require.NoError(t, err)

		// With -sort name the validator takes the fetch time from the last 20
		// characters of the name, so the result does not depend on file mtimes.
		stamp := fetched.Format("2006-01-02T15-04-05Z") + ".pb"
		for feed, body := range map[string][]byte{"vehicle-positions": positionsBody, "trip-updates": tripUpdatesBody, "combined": combinedBody} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, feed, feed+"-"+stamp), body, 0o644))
		}
	}
}

// feedSnapshotDirs are the directories TestWriteFeedSnapshots writes, one
// validator run each.
var feedSnapshotDirs = []string{"vehicle-positions", "trip-updates", "combined"}

// fetchSnapshot fetches one feed from srv as protobuf: its bytes as served,
// and the feed they hold.
func fetchSnapshot(t *testing.T, srv *httptest.Server, path string) ([]byte, *gtfsrt.FeedMessage) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", path, body)
	var feed gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(body, &feed))
	return body, &feed
}

// requireUniqueEntityIDs fails the test when two of feed's entities share an
// id, which GTFS-RT forbids within one message.
func requireUniqueEntityIDs(t *testing.T, feed *gtfsrt.FeedMessage) {
	t.Helper()
	seen := make(map[string]bool, len(feed.GetEntity()))
	for _, e := range feed.GetEntity() {
		require.False(t, seen[e.GetId()], "entity id %q appears twice in one message", e.GetId())
		seen[e.GetId()] = true
	}
}
