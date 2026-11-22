package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// fetchFeed serves path from handler in protobuf and returns the feed.
func fetchFeed(t *testing.T, handler http.Handler, path string) *gtfsrt.FeedMessage {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "application/x-protobuf", w.Header().Get("Content-Type"))
	var feed gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &feed))
	return &feed
}

// tripUpdatesOf is the trip updates feed tracker publishes, by entity id.
func tripUpdatesOf(t *testing.T, tracker *Tracker) map[string]*gtfsrt.TripUpdate {
	t.Helper()
	got := make(map[string]*gtfsrt.TripUpdate)
	for _, e := range fetchFeed(t, handleGetTripUpdates(tracker), "/gtfs-rt/trip-updates").GetEntity() {
		require.NotNil(t, e.GetTripUpdate(), "entity %s carries no TripUpdate", e.GetId())
		require.Nil(t, e.GetVehicle(), "the trip updates feed carries no VehiclePosition")
		got[e.GetId()] = e.GetTripUpdate()
	}
	return got
}

// positionsOf is the positions feed tracker publishes, by entity id.
func positionsOf(t *testing.T, tracker *Tracker) map[string]*gtfsrt.VehiclePosition {
	t.Helper()
	got := make(map[string]*gtfsrt.VehiclePosition)
	for _, e := range fetchFeed(t, handleGetFeed(tracker, riderOff{}), "/gtfs-rt/vehicle-positions").GetEntity() {
		got[e.GetId()] = e.GetVehicle()
	}
	return got
}

// TestTripUpdates_MeasuredDelay: a late vehicle's delay is positive and an
// early one's negative, in whole seconds, on the trip update and on its one
// stop_time_update, the next stop the positions feed names, whose arrival time
// is the stop's in the timetable plus the delay. Everything that identifies
// the trip and the vehicle is what the positions feed says, since that is how
// the Java OneBusAway server pairs the two (E047).
func TestTripUpdates_MeasuredDelay(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)

	// Halfway from ST2 (12:05) to ST3 (12:10) the timetable has T6 at 12:07:30.
	late := deviationReport(t6, "late", deviationServiceDate, 750.5, clock(day, 12, 13, 30))
	late.RouteID, late.VehicleLabel = "R1", "Bus 7"
	early := deviationReport(t6, "early", deviationServiceDate, 750.5, clock(day, 12, 5, 30))
	for _, loc := range []*LocationReport{late, early} {
		require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
	}

	updates := tripUpdatesOf(t, tracker)
	positions := positionsOf(t, tracker)
	require.Len(t, updates, 2)

	for vehicleID, want := range map[string]struct {
		delay   int32
		arrival time.Time
	}{
		"late":  {360, clock(day, 12, 16, 0)},
		"early": {-120, clock(day, 12, 8, 0)},
	} {
		tu, vp := updates[vehicleID], positions[vehicleID]
		require.NotNil(t, tu, vehicleID)
		require.NotNil(t, vp, vehicleID)

		assert.Equal(t, want.delay, tu.GetDelay(), vehicleID)
		require.Len(t, tu.GetStopTimeUpdate(), 1, vehicleID)
		stu := tu.GetStopTimeUpdate()[0]
		assert.Equal(t, "ST3", stu.GetStopId(), vehicleID)
		assert.Equal(t, uint32(3), stu.GetStopSequence(), vehicleID)
		assert.Equal(t, want.delay, stu.GetArrival().GetDelay(), vehicleID)
		assert.Equal(t, want.arrival.Unix(), stu.GetArrival().GetTime(), vehicleID)
		// Set, not left to its default, or the validator warns W009.
		require.NotNil(t, stu.ScheduleRelationship, vehicleID)
		assert.Equal(t, gtfsrt.TripUpdate_StopTimeUpdate_SCHEDULED, stu.GetScheduleRelationship(), vehicleID)
		assert.Equal(t, vp.GetStopId(), stu.GetStopId(), "%s: the next stop is the one the positions feed names", vehicleID)
		assert.Equal(t, vp.GetCurrentStopSequence(), stu.GetStopSequence(), vehicleID)

		assert.True(t, proto.Equal(vp.GetVehicle(), tu.GetVehicle()),
			"%s: vehicle %v must match the positions feed's %v", vehicleID, tu.GetVehicle(), vp.GetVehicle())
		assert.True(t, proto.Equal(vp.GetTrip(), tu.GetTrip()),
			"%s: trip %v must match the positions feed's %v", vehicleID, tu.GetTrip(), vp.GetTrip())
		assert.Equal(t, vp.GetTimestamp(), tu.GetTimestamp(), vehicleID)
	}

	trip := updates["late"].GetTrip()
	assert.Equal(t, "T6", trip.GetTripId())
	assert.Equal(t, "R1", trip.GetRouteId())
	assert.Equal(t, deviationServiceDate, trip.GetStartDate())
	assert.Equal(t, gtfsrt.TripDescriptor_SCHEDULED, trip.GetScheduleRelationship())
	assert.Equal(t, "late", updates["late"].GetVehicle().GetId())
	assert.Equal(t, "Bus 7", updates["late"].GetVehicle().GetLabel())
	assert.Equal(t, uint64(late.Timestamp), updates["late"].GetTimestamp())
}

// TestTripUpdates_DelayIsRoundedToTheSecond: GTFS-RT counts a delay in whole
// seconds, rounded as the admin live answer rounds the same deviation, and the
// stop's arrival time is its time in the timetable plus that delay, to the
// second, even when the GTFS parser interpolated the stop's time to a fraction
// of one.
func TestTripUpdates_DelayIsRoundedToTheSecond(t *testing.T) {
	tests := []struct {
		deviation time.Duration
		want      int32
	}{
		{90*time.Second + 400*time.Millisecond, 90},
		{90*time.Second + 600*time.Millisecond, 91},
		{-(30*time.Second + 600*time.Millisecond), -31},
	}
	for _, tc := range tests {
		v := measuredVehicle("bus-1")
		v.ScheduleDeviation = &tc.deviation
		scheduled := v.ScheduledArrival.Add(300 * time.Millisecond)
		v.ScheduledArrival = &scheduled
		feed := buildTripUpdates([]*VehicleState{v})
		require.Len(t, feed.GetEntity(), 1)
		tu := feed.GetEntity()[0].GetTripUpdate()
		assert.Equal(t, tc.want, tu.GetDelay(), tc.deviation)
		arrival := tu.GetStopTimeUpdate()[0].GetArrival()
		assert.Equal(t, tc.want, arrival.GetDelay(), tc.deviation)
		assert.Equal(t, scheduled.Unix()+int64(tc.want), arrival.GetTime(), tc.deviation)
	}
}

// measuredVehicle is a driver vehicle on a timetabled trip, placed heading to
// its second stop, due there at 08:05 Pacific, two minutes late: one the trip
// updates feed publishes.
func measuredVehicle(vehicleID string) *VehicleState {
	scheduled := time.Date(2026, 9, 2, 15, 5, 0, 0, time.UTC)
	return &VehicleState{
		VehicleID: vehicleID, TripID: "T1", RouteID: "R1", StartDate: "20260902",
		Latitude: 47.6, Longitude: -122.33, Timestamp: time.Now().Unix(),
		StopID: "ST2", StopSequence: 2, ScheduleDeviation: dur(2 * time.Minute), ScheduledArrival: &scheduled,
	}
}

// TestBuildTripUpdates_Excluded: a vehicle is published only with a trip_id
// on a timetabled trip, a measured delay and a stop, with its time in the
// timetable, to carry it. Without any one of them it stays out, rather than
// going out as on time.
func TestBuildTripUpdates_Excluded(t *testing.T) {
	tests := []struct {
		name string
		edit func(v *VehicleState)
	}{
		{"unknown deviation", func(v *VehicleState) { v.ScheduleDeviation = nil }},
		{"route only", func(v *VehicleState) { v.TripID = "" }},
		{"a trip in frequencies.txt", func(v *VehicleState) { v.FrequencyBased, v.StartTime = true, "12:00:00" }},
		{"no stop", func(v *VehicleState) { v.StopID, v.StopSequence = "", 0 }},
		{"no scheduled arrival", func(v *VehicleState) { v.ScheduledArrival = nil }},
		{"non-positive timestamp", func(v *VehicleState) { v.Timestamp = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			excluded := measuredVehicle("excluded")
			tc.edit(excluded)
			feed := buildTripUpdates([]*VehicleState{measuredVehicle("kept"), excluded})
			require.Len(t, feed.GetEntity(), 1)
			assert.Equal(t, "kept", feed.GetEntity()[0].GetId())
		})
	}
}

// TestTripUpdates_ExcludedThroughTheTracker: the cases the proposal leaves out,
// each sent as a report through the accepted-report path. Every one is in the
// positions feed and none is in the trip updates feed.
func TestTripUpdates_ExcludedThroughTheTracker(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	headwayIx := frequencyFixtureIndex(t)
	untimedIx := untimedFixtureIndex(t)

	routeOnly := deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 12, 13, 30))
	routeOnly.TripID, routeOnly.RouteID = "", "R1"
	headwayPos := mustTrip(t, headwayIx, "T1").Shape.PointAt(750.5)

	tests := []struct {
		name string
		ix   *rider.Index
		loc  *LocationReport
	}{
		{"a trip in frequencies.txt", headwayIx, &LocationReport{VehicleID: "bus-1", TripID: "T1", StartDate: deviationServiceDate,
			Latitude: headwayPos.Lat, Longitude: headwayPos.Lon, Timestamp: clock(day, 12, 13, 30).Unix()}},
		{"route only", ix, routeOnly},
		{"not yet left the first stop", ix, deviationReport(t6, "bus-1", deviationServiceDate, 0, clock(day, 12, 2, 0))},
		{"a start_date the trip does not run on", ix, deviationReport(t6, "bus-1", deviationSaturday, 750.5, clock(day, 12, 13, 30))},
		{"over 90 minutes late", ix, deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 13, 37, 31))},
		{"over 90 minutes early", ix, deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 10, 37, 29))},
		// Just past ST1, which TE leaves at 12:00, where an untimed ST2 at
		// midnight would put the vehicle under an hour late.
		{"a stop with no time and no timed stop after it", untimedIx,
			deviationReport(mustTrip(t, untimedIx, "TE"), "bus-1", deviationServiceDate, 40, clock(day, 12, 0, 30))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newPlacingTracker(t, tc.ix)
			require.True(t, tracker.UpdateAuthorized(tc.loc, time.Now()))
			require.Contains(t, positionsOf(t, tracker), "bus-1")
			assert.Empty(t, tripUpdatesOf(t, tracker))
		})
	}

	t.Run("restored at startup", func(t *testing.T) {
		tracker := newPlacingTracker(t, ix)
		tracker.Update(deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 12, 13, 30)))
		require.Contains(t, positionsOf(t, tracker), "bus-1")
		assert.Empty(t, tripUpdatesOf(t, tracker), "a vehicle restored at startup is measured by its next report")
	})
}

// TestTripUpdates_RiderVehiclesExcluded: with rider mode on, a rider-derived
// trip is in the positions feed and never in the trip updates feed, served
// from the production mux.
func TestTripUpdates_RiderVehiclesExcluded(t *testing.T) {
	ix := loadFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	now := time.Now()
	startDate := ix.ServiceDate(now)

	thresholds := rider.DefaultThresholds()
	thresholds.ScheduleEarly, thresholds.ScheduleLate = 24*time.Hour, 24*time.Hour
	agg := rider.NewAggregator(thresholds, ix.Timezone())
	tracker := newPlacingTracker(t, ix)
	trusted := trustedSources{feed: rider.NewTrustedFeed(nil, nil, defaultTrustedMaxAge), tracker: tracker}
	riderSvc := newRiderService(newFakeRiderStore(), agg, func() *rider.Index { return ix }, trusted, testSecret, defaultRiderJWTTTL, false)
	t.Cleanup(riderSvc.Stop)

	const rideID = "tu-ride"
	agg.Add(rider.NewSession(rideID, "tu-rider", rider.TripKey{TripID: t1.ID, StartDate: startDate}, t1, rider.TierTrusted, now))
	var points []rider.Point
	for i := range 6 {
		along := float64(i) * 50
		points = append(points, rider.Point{Pos: t1.Shape.PointAt(along), Accuracy: 5, Speed: 10,
			Bearing: t1.Shape.BearingAt(along), Timestamp: now.Add(-time.Duration(5-i) * rateInterval)})
	}
	res, err := agg.ApplyBatch(rideID, points, func(key rider.TripKey) (rider.TrustedVehicle, bool) {
		return trusted.Lookup(key, now)
	}, now)
	require.NoError(t, err)
	require.True(t, res.Published, "the rider's trip must be in the positions feed: %+v", res.Summary)

	mux := newMux(&noopStore{}, tracker, nil, testSecret, testTTLs, time.Now(), nil, nil, false, false, "", riderSvc, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), nil, newFeedHealth(time.Now))
	positions := fetchFeed(t, mux, "/gtfs-rt/vehicle-positions")
	require.Len(t, positions.GetEntity(), 1)
	require.Equal(t, "rider:T1:"+startDate, positions.GetEntity()[0].GetId())

	assert.Empty(t, fetchFeed(t, mux, "/gtfs-rt/trip-updates").GetEntity())
}

// TestTripUpdates_TimestampCappedLikeThePositionsFeed: a report from a phone
// whose clock runs ahead is capped at the same 60 s past the feed's own time
// (E050), and the header is never older than an entity (E012).
func TestTripUpdates_TimestampCappedLikeThePositionsFeed(t *testing.T) {
	ix, startDate := nowFixtureIndex(t, time.Now())
	tracker := newPlacingTracker(t, ix)
	ahead := time.Now().Add(4 * time.Minute)
	require.True(t, tracker.UpdateAuthorized(deviationReport(mustTrip(t, ix, "TB"), "bus-1", startDate, 100, ahead), time.Now()))

	before := uint64(time.Now().Unix())
	feed := fetchFeed(t, handleGetTripUpdates(tracker), "/gtfs-rt/trip-updates")
	after := uint64(time.Now().Unix())
	require.Len(t, feed.GetEntity(), 1)
	ts := feed.GetEntity()[0].GetTripUpdate().GetTimestamp()
	assert.GreaterOrEqual(t, ts, before+60)
	assert.LessOrEqual(t, ts, after+60)
	assert.GreaterOrEqual(t, feed.GetHeader().GetTimestamp(), ts, "E012")
	assert.Equal(t, positionsOf(t, tracker)["bus-1"].GetTimestamp(), ts)

	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.Equal(t, gtfsrt.FeedHeader_FULL_DATASET, feed.GetHeader().GetIncrementality())
}

// TestTripUpdates_StaleVehicleDropsOut: a vehicle that has not reported within
// the staleness threshold leaves the trip updates feed, as it leaves the
// positions feed, though its last delay was known.
func TestTripUpdates_StaleVehicleDropsOut(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)
	require.True(t, tracker.UpdateAuthorized(deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 12, 13, 30)), time.Now()))
	require.Len(t, tripUpdatesOf(t, tracker), 1)

	tracker.mu.Lock()
	tracker.vehicles["bus-1"].UpdatedAt = time.Now().Add(-10 * time.Minute)
	tracker.mu.Unlock()

	assert.Empty(t, tripUpdatesOf(t, tracker))
	assert.Empty(t, positionsOf(t, tracker))
}

// TestTripUpdates_JSONMatchesProtobuf: ?format=json is the same feed as the
// protobuf, protojson-encoded, as on the positions feed.
func TestTripUpdates_JSONMatchesProtobuf(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)
	require.True(t, tracker.UpdateAuthorized(deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 12, 13, 30)), time.Now()))
	handler := handleGetTripUpdates(tracker)

	fromProtobuf := fetchFeed(t, handler, "/gtfs-rt/trip-updates")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/gtfs-rt/trip-updates?format=json", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var fromJSON gtfsrt.FeedMessage
	require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &fromJSON))

	require.Len(t, fromProtobuf.GetEntity(), 1)
	// The two were built a moment apart, so their headers may be a second
	// apart; everything else is the same feed.
	fromJSON.Header.Timestamp = fromProtobuf.Header.Timestamp
	assert.True(t, proto.Equal(fromProtobuf, &fromJSON), "protobuf %v\njson %v", fromProtobuf, &fromJSON)
}

// TestTripUpdates_ThroughThePostedReport: the delay reaches the feed from a
// report sent to POST /api/v1/locations, the path a driver's phone takes, as
// the same figure the live map reads. A phone that sends another day's
// start_date, which the trip runs on as well, would have the vehicle days off
// its timetable, so neither says how late it is.
func TestTripUpdates_ThroughThePostedReport(t *testing.T) {
	now := time.Now()
	ix, startDate := nowFixtureIndex(t, now)
	run, err := time.Parse("20060102", startDate)
	require.NoError(t, err)
	// TB left ST1 two to three minutes ago, and is timetabled to be 100 m on
	// a minute after that.
	pos := mustTrip(t, ix, "TB").Shape.PointAt(100)

	tests := []struct {
		name      string
		startDate string
		measured  bool
	}{
		{"the run's own start_date", startDate, true},
		{"a week-old start_date", run.AddDate(0, 0, -7).Format("20060102"), false},
		{"the next day's start_date", run.AddDate(0, 0, 1).Format("20060102"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newPlacingTracker(t, ix)
			rl := NewVehicleRateLimiter()
			t.Cleanup(rl.Stop)
			post := handlePostLocation(&mockStore{}, tracker, rl)
			w := postLocationWithClaims(post, LocationReport{VehicleID: "bus-1", TripID: "TB", StartDate: tc.startDate,
				Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: now.Unix()}, jwt.MapClaims{"sub": "driver-1"})
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.Contains(t, positionsOf(t, tracker), "bus-1")

			live := string(liveEntries(t, tracker)["bus-1"]["schedule_deviation_s"])
			updates := tripUpdatesOf(t, tracker)
			if !tc.measured {
				assert.Empty(t, updates)
				assert.Equal(t, "null", live)
				return
			}
			require.Contains(t, updates, "bus-1")
			delay := updates["bus-1"].GetDelay()
			assert.Positive(t, delay)
			assert.Equal(t, strconv.Itoa(int(delay)), live)
		})
	}
}

// TestTripUpdates_UntimedStop: at a stop whose times stop_times.txt leaves
// blank, the stop_time_update still names the stop the positions feed names,
// and its arrival time is the time the GTFS parser filled in plus the delay.
// A delay alone there would have nothing to be added to (E046).
func TestTripUpdates_UntimedStop(t *testing.T) {
	ix := untimedFixtureIndex(t)
	tb := mustTrip(t, ix, "TB")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)

	// The parser puts ST2, 500 m of TB's 1001 from ST1 (12:00) to ST3
	// (12:10), at 12:04:59.7. Halfway to it, 250 m along, the timetable has
	// TB at 12:02:29.85.
	for _, loc := range []*LocationReport{
		deviationReport(tb, "heading", deviationServiceDate, 250, clock(day, 12, 4, 0)),
		deviationReport(tb, "at", deviationServiceDate, 500, clock(day, 12, 6, 0)),
	} {
		require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
	}

	updates := tripUpdatesOf(t, tracker)
	positions := positionsOf(t, tracker)
	for vehicleID, want := range map[string]struct {
		delay   int32
		arrival time.Time
		status  gtfsrt.VehiclePosition_VehicleStopStatus
	}{
		"heading": {90, clock(day, 12, 6, 30), gtfsrt.VehiclePosition_IN_TRANSIT_TO},
		"at":      {60, clock(day, 12, 6, 0), gtfsrt.VehiclePosition_STOPPED_AT},
	} {
		tu, vp := updates[vehicleID], positions[vehicleID]
		require.NotNil(t, tu, vehicleID)
		require.NotNil(t, vp, vehicleID)
		require.Equal(t, want.status, vp.GetCurrentStatus(), vehicleID)
		assert.Equal(t, want.delay, tu.GetDelay(), vehicleID)
		require.Len(t, tu.GetStopTimeUpdate(), 1, vehicleID)
		stu := tu.GetStopTimeUpdate()[0]
		assert.Equal(t, "ST2", stu.GetStopId(), vehicleID)
		assert.Equal(t, uint32(2), stu.GetStopSequence(), vehicleID)
		assert.Equal(t, vp.GetStopId(), stu.GetStopId(), vehicleID)
		assert.Equal(t, want.delay, stu.GetArrival().GetDelay(), vehicleID)
		assert.Equal(t, want.arrival.Unix(), stu.GetArrival().GetTime(), vehicleID)
	}
}

// TestTripUpdates_ArrivalTimeAtAStopWithADwell: at a stop the timetable holds
// the vehicle at, the arrival time is the stop's arrival plus the delay, not
// its departure.
func TestTripUpdates_ArrivalTimeAtAStopWithADwell(t *testing.T) {
	ix := dwellFixtureIndex(t)
	t7 := mustTrip(t, ix, "T7")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)

	// Halfway from ST1 (12:00) to ST2, where T7 is due at 12:05 and leaves at
	// 12:07, the timetable has it at 12:02:30.
	require.True(t, tracker.UpdateAuthorized(deviationReport(t7, "bus-1", deviationServiceDate, 250, clock(day, 12, 3, 30)), time.Now()))
	tu := tripUpdatesOf(t, tracker)["bus-1"]
	require.NotNil(t, tu)
	assert.Equal(t, int32(60), tu.GetDelay())
	require.Len(t, tu.GetStopTimeUpdate(), 1)
	stu := tu.GetStopTimeUpdate()[0]
	assert.Equal(t, "ST2", stu.GetStopId())
	assert.Equal(t, clock(day, 12, 6, 0).Unix(), stu.GetArrival().GetTime())
}

// untimedFixtureIndex is the test feed with weekday trips added on T1's
// street, timed as a feed that times only its timepoints: TB leaves ST1 at
// 12:00 and reaches ST3 at 12:10, with ST2's times left blank, which the GTFS
// parser fills in between the two by shape_dist_traveled. TE leaves ST3's
// blank as well, so no timed stop follows ST1 and nothing fills either. TZ is
// timed at every stop, ST2 at 12:00 like ST1, as a feed that rounds to the
// minute times two stops close together.
func untimedFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	appendLines := func(body []byte, lines ...string) []byte {
		return []byte(string(body) + strings.Join(lines, "\n") + "\n")
	}
	return fixtureIndexEdited(t, func(name string, body []byte) []byte {
		switch name {
		case "trips.txt":
			return appendLines(body, "R1,WEEKDAY,TB,S1,North,0", "R1,WEEKDAY,TE,S1,North,0", "R1,WEEKDAY,TZ,S1,North,0")
		case "stop_times.txt":
			return appendLines(body,
				"TB,12:00:00,12:00:00,ST1,1,0.000000",
				"TB,,,ST2,2,500.000000",
				"TB,12:10:00,12:10:00,ST3,3,1001.000000",
				"TE,12:00:00,12:00:00,ST1,1,0.000000",
				"TE,,,ST2,2,500.000000",
				"TE,,,ST3,3,1001.000000",
				"TZ,12:00:00,12:00:00,ST1,1,0.000000",
				"TZ,12:00:00,12:00:00,ST2,2,500.000000",
				"TZ,12:10:00,12:10:00,ST3,3,1001.000000",
			)
		}
		return body
	})
}

// nowFixture is the test feed with TB added, run every day: T1's street,
// leaving ST1 two minutes before the minute now is in and reaching ST3 ten
// minutes later, with ST2's times left blank as in untimedFixtureIndex. A
// vehicle on it is minutes off its timetable whenever a test runs, where the
// fixture's own trips are hours off at most times of day, further than
// maxScheduleDeviation. It returns the feed and the start_date of TB's run.
func nowFixture(t *testing.T, now time.Time) ([]byte, string) {
	t.Helper()
	ix := loadFixtureIndex(t)
	startDate := ix.ServiceDate(now)
	dayStart, err := rider.ServiceDayStart(startDate, ix.Timezone())
	require.NoError(t, err)
	leaves := now.Sub(dayStart).Truncate(time.Minute) - 2*time.Minute
	arrives := leaves + 10*time.Minute
	appendLines := func(body []byte, lines ...string) []byte {
		return []byte(string(body) + strings.Join(lines, "\n") + "\n")
	}
	feed := fixtureZipEdited(t, func(name string, body []byte) []byte {
		switch name {
		case "calendar.txt":
			return appendLines(body, "DAILY,1,1,1,1,1,1,1,20260101,20351231")
		case "trips.txt":
			return appendLines(body, "R1,DAILY,TB,S1,North,0")
		case "stop_times.txt":
			return appendLines(body,
				fmt.Sprintf("TB,%s,%s,ST1,1,0.000000", serviceClock(leaves), serviceClock(leaves)),
				"TB,,,ST2,2,500.000000",
				fmt.Sprintf("TB,%s,%s,ST3,3,1001.000000", serviceClock(arrives), serviceClock(arrives)),
			)
		}
		return body
	})
	return feed, startDate
}

// nowFixtureIndex is an index over nowFixture's feed, and TB's start_date.
func nowFixtureIndex(t *testing.T, now time.Time) (*rider.Index, string) {
	t.Helper()
	feed, startDate := nowFixture(t, now)
	ix, err := rider.IndexFromBytes(feed, "now", now)
	require.NoError(t, err)
	return ix, startDate
}

// TestFeeds_OnlyFormatJSONIsJSON: both feeds answer through writeFeed, which
// takes only the exact string json; any other format is the protobuf feed.
func TestFeeds_OnlyFormatJSONIsJSON(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	for path, handler := range map[string]http.Handler{
		"/gtfs-rt/vehicle-positions": handleGetFeed(tracker, riderOff{}),
		"/gtfs-rt/trip-updates":      handleGetTripUpdates(tracker),
	} {
		fetchFeed(t, handler, path+"?format=xml")
		fetchFeed(t, handler, path+"?format=JSON")
	}
}
