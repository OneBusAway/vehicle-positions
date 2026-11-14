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

// TestWriteFeedSnapshots writes the GTFS-RT feed to FEED_SNAPSHOT_DIR once per
// round of reports, for MobilityData's gtfs-realtime-validator to check
// against rider/testdata/fixture.zip (.github/workflows/gtfs-rt-validator.yml,
// and docs/development.md for a local run). It is skipped unless
// FEED_SNAPSHOT_DIR is set, because the rounds are rateInterval apart.
//
// The feed is the production mux's. noopStore stands in for Postgres, so every
// report is authorized and none is saved; the rest, from the auth middleware
// to the protobuf bytes, is what a deployed server runs.
func TestWriteFeedSnapshots(t *testing.T) {
	dir := os.Getenv("FEED_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("FEED_SNAPSHOT_DIR not set; skipping feed snapshot export")
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	existing, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, existing, "FEED_SNAPSHOT_DIR must be empty: the validator reads every file in it")

	ix := loadFixtureIndex(t)
	t1, ok := ix.Trip("T1")
	require.True(t, ok)
	t3, ok := ix.Trip("T3")
	require.True(t, ok)
	startDate := ix.ServiceDate(time.Now())

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

	srv := httptest.NewServer(newMux(&noopStore{}, tracker, limiter, testSecret, testTTLs, time.Now(), nil, nil, false, false, riderSvc, nil, nil))
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

		onTrip := t3.Shape.PointAt(along)
		onRoute := t1.Shape.PointAt(t1.Shape.Length/2 + along)
		parked := t3.Shape.PointAt(t3.Shape.Length / 2)
		// A driver on a trip, a driver who knows only the route, and a vehicle
		// that is not in service and sends no bearing or speed.
		postLocation(LocationReport{
			VehicleID: "bus-101", TripID: t3.ID, RouteID: t3.RouteID, StartDate: startDate,
			Latitude: onTrip.Lat, Longitude: onTrip.Lon,
			Bearing: float64ptr(t3.Shape.BearingAt(along)), Speed: float64ptr(feedSnapshotSpeed),
			Timestamp: now.Unix(),
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

		resp, err := srv.Client().Get(srv.URL + "/gtfs-rt/vehicle-positions")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s", body)
		fetched := time.Now().UTC()

		var feed gtfsrt.FeedMessage
		require.NoError(t, proto.Unmarshal(body, &feed))
		var ids []string
		for _, e := range feed.GetEntity() {
			ids = append(ids, e.GetId())
			if e.GetId() == "bus-101" {
				require.NotEmpty(t, e.GetVehicle().GetStopId(),
					"the driver on a trip must be placed on it, or the validator never sees a driver's stop fields")
			}
		}
		require.ElementsMatch(t, []string{"bus-101", "bus-102", "bus-103", "rider:T1:" + startDate}, ids,
			"a snapshot missing an entity would pass the validator while checking less")

		// With -sort name the validator takes the fetch time from the last 20
		// characters of the name, so the result does not depend on file mtimes.
		name := "vehicle-positions-" + fetched.Format("2006-01-02T15-04-05Z") + ".pb"
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), body, 0o644))
	}
}
