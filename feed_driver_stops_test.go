package main

import (
	"net/http"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// newPlacingTracker returns a tracker that places accepted reports on ix's
// trips, as main wires it.
func newPlacingTracker(t *testing.T, ix *rider.Index) *Tracker {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	tracker.SetTripLocator(newTripLocator(func() *rider.Index { return ix }, rider.DefaultThresholds().MaxShapeDistance))
	return tracker
}

// reportOn sends bus-1's report at pos on a trip through the accepted-report
// path and returns what the tracker then holds for it.
func reportOn(t *testing.T, tracker *Tracker, tripID, startDate string, pos rider.LatLon) VehicleState {
	t.Helper()
	loc := &LocationReport{VehicleID: "bus-1", TripID: tripID, StartDate: startDate,
		Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}
	require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
	vehicles := tracker.ActiveVehicles()
	require.Len(t, vehicles, 1)
	return *vehicles[0]
}

func TestTracker_LoopDoesNotJumpPasses(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t3 := mustTrip(t, ix, "T3")
	tracker := newPlacingTracker(t, ix)

	var sequences []int
	for _, along := range []float64{0, 300, 700, 1100, 1500, 1800, 1950} {
		sequences = append(sequences, reportOn(t, tracker, "T3", "20260902", t3.Shape.PointAt(along)).StopSequence)
	}
	end := reportOn(t, tracker, "T3", "20260902", loopClosingCorner(t3))
	sequences = append(sequences, end.StopSequence)

	assert.Equal(t, []int{1, 2, 2, 3, 3, 4, 4, 4}, sequences,
		"the fix at the terminal is nearer the opening pass, and must not send the vehicle back to stop 1")
	assert.Equal(t, "LP1", end.StopID)
	assert.True(t, end.StoppedAt)
}

func TestTracker_OffRouteReportKeepsTheHint(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t3 := mustTrip(t, ix, "T3")
	tracker := newPlacingTracker(t, ix)

	require.Equal(t, 4, reportOn(t, tracker, "T3", "20260902", t3.Shape.PointAt(t3.Shape.Length-30)).StopSequence)
	// The middle of the loop, about 250 m from every side of it.
	detour := reportOn(t, tracker, "T3", "20260902", rider.OffsetMetres(t3.Shape.Points[0], 250, 248))
	assert.Empty(t, detour.StopID, "an off-route report names no stop")
	assert.Zero(t, detour.StopSequence)
	assert.False(t, detour.StoppedAt)

	back := reportOn(t, tracker, "T3", "20260902", loopClosingCorner(t3))
	assert.Equal(t, 4, back.StopSequence, "the place before the detour is still the hint")
}

func TestTracker_TripChangeResetsTheHint(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t3 := mustTrip(t, ix, "T3")

	cases := []struct {
		name, tripID, startDate string
	}{
		{"the next trip of the block", "T5", "20260902"},
		{"the same trip on the next service date", "T3", "20260903"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newPlacingTracker(t, ix)
			reportOn(t, tracker, "T3", "20260902", t3.Shape.PointAt(t3.Shape.Length-30))
			finished := reportOn(t, tracker, "T3", "20260902", loopClosingCorner(t3))
			require.Equal(t, 4, finished.StopSequence, "the run ends at its last stop")

			// The next run starts where the last one ended.
			started := reportOn(t, tracker, tc.tripID, tc.startDate, loopClosingCorner(t3))
			assert.Equal(t, "LP1", started.StopID)
			assert.Equal(t, 1, started.StopSequence, "a new trip starts at its first stop, not the old trip's last")
			assert.True(t, started.StoppedAt)
		})
	}
}

func TestTracker_SeedLeavesStopFieldsUnsetUntilNextReport(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	tracker := newPlacingTracker(t, ix)

	pos := t1.Shape.PointAt(250)
	tracker.Update(&LocationReport{VehicleID: "bus-1", TripID: "T1", Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()})
	seeded := tracker.ActiveVehicles()
	require.Len(t, seeded, 1)
	assert.Empty(t, seeded[0].StopID, "the startup seed places no vehicle")

	next := reportOn(t, tracker, "T1", "", t1.Shape.PointAt(260))
	assert.Equal(t, "ST2", next.StopID, "the vehicle's next report places it")
	assert.Equal(t, 2, next.StopSequence)
}

func TestTracker_NoLocatorLeavesStopFieldsUnset(t *testing.T) {
	t1 := mustTrip(t, loadFixtureIndex(t), "T1")
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)

	v := reportOn(t, tracker, "T1", "", t1.Shape.PointAt(250))
	assert.Empty(t, v.StopID)
}

func TestPostLocation_DriverStopFieldsReachFeed(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	post := handlePostLocation(&mockStore{}, tracker, rl)

	now := time.Now().Unix()
	report := func(loc LocationReport, pos rider.LatLon) {
		t.Helper()
		loc.Latitude, loc.Longitude, loc.Timestamp = pos.Lat, pos.Lon, now
		w := postLocationWithClaims(post, loc, jwt.MapClaims{"sub": "driver-" + loc.VehicleID})
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}
	report(LocationReport{VehicleID: "moving", TripID: "T1", RouteID: "R1", StartDate: "20260902"}, t1.Shape.PointAt(250))
	report(LocationReport{VehicleID: "at-stop", TripID: "T1", RouteID: "R1", StartDate: "20260902"}, t1.Shape.PointAt(505))
	report(LocationReport{VehicleID: "detour", TripID: "T1", RouteID: "R1", StartDate: "20260902"},
		rider.OffsetMetres(t1.Shape.PointAt(250), 0, 300))
	report(LocationReport{VehicleID: "route-only", RouteID: "R1"}, t1.Shape.PointAt(250))

	w := getFeed(handleGetFeed(tracker, riderOff{}), "")
	require.Equal(t, http.StatusOK, w.Code)
	var feed gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &feed))
	assert.Empty(t, validateFeedCompliance(t, &feed))

	byID := make(map[string]*gtfsrt.VehiclePosition)
	for _, e := range feed.GetEntity() {
		byID[e.GetId()] = e.GetVehicle()
	}
	require.Len(t, byID, 4)

	moving := byID["moving"]
	assert.Equal(t, "ST2", moving.GetStopId())
	assert.Equal(t, uint32(2), moving.GetCurrentStopSequence())
	assert.Equal(t, gtfsrt.VehiclePosition_IN_TRANSIT_TO.Enum(), moving.CurrentStatus)

	atStop := byID["at-stop"]
	assert.Equal(t, "ST2", atStop.GetStopId())
	assert.Equal(t, uint32(2), atStop.GetCurrentStopSequence())
	assert.Equal(t, gtfsrt.VehiclePosition_STOPPED_AT.Enum(), atStop.CurrentStatus)

	for _, id := range []string{"detour", "route-only"} {
		vp := byID[id]
		assert.Nil(t, vp.StopId, id)
		assert.Nil(t, vp.CurrentStopSequence, id)
		assert.Nil(t, vp.CurrentStatus, "%s: unset, not the proto default IN_TRANSIT_TO", id)
	}
}
