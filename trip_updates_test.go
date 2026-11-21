package main

import (
	"net/http"
	"net/http/httptest"
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
// stop_time_update, the next stop the positions feed names. Everything that
// identifies the trip and the vehicle is what the positions feed says, since
// that is how the Java OneBusAway server pairs the two (E047).
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

	for vehicleID, want := range map[string]int32{"late": 360, "early": -120} {
		tu, vp := updates[vehicleID], positions[vehicleID]
		require.NotNil(t, tu, vehicleID)
		require.NotNil(t, vp, vehicleID)

		assert.Equal(t, want, tu.GetDelay(), vehicleID)
		require.Len(t, tu.GetStopTimeUpdate(), 1, vehicleID)
		stu := tu.GetStopTimeUpdate()[0]
		assert.Equal(t, "ST3", stu.GetStopId(), vehicleID)
		assert.Equal(t, uint32(3), stu.GetStopSequence(), vehicleID)
		assert.Equal(t, want, stu.GetArrival().GetDelay(), vehicleID)
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
// seconds, rounded as the admin live answer rounds the same deviation.
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
		feed := buildTripUpdates([]*VehicleState{v})
		require.Len(t, feed.GetEntity(), 1)
		tu := feed.GetEntity()[0].GetTripUpdate()
		assert.Equal(t, tc.want, tu.GetDelay(), tc.deviation)
		assert.Equal(t, tc.want, tu.GetStopTimeUpdate()[0].GetArrival().GetDelay(), tc.deviation)
	}
}

// measuredVehicle is a driver vehicle on a timetabled trip, placed heading to
// its second stop two minutes late: one the trip updates feed publishes.
func measuredVehicle(vehicleID string) *VehicleState {
	return &VehicleState{
		VehicleID: vehicleID, TripID: "T1", RouteID: "R1", StartDate: "20260902",
		Latitude: 47.6, Longitude: -122.33, Timestamp: time.Now().Unix(),
		StopID: "ST2", StopSequence: 2, ScheduleDeviation: dur(2 * time.Minute),
	}
}

// TestBuildTripUpdates_Excluded: a vehicle is published only with a trip_id
// on a timetabled trip, a measured delay and a stop to carry it. Without any
// one of them it stays out, rather than going out as on time.
func TestBuildTripUpdates_Excluded(t *testing.T) {
	tests := []struct {
		name string
		edit func(v *VehicleState)
	}{
		{"unknown deviation", func(v *VehicleState) { v.ScheduleDeviation = nil }},
		{"route only", func(v *VehicleState) { v.TripID = "" }},
		{"a trip in frequencies.txt", func(v *VehicleState) { v.FrequencyBased, v.StartTime = true, "12:00:00" }},
		{"no stop", func(v *VehicleState) { v.StopID, v.StopSequence = "", 0 }},
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
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	tracker := newPlacingTracker(t, ix)
	ahead := time.Now().Add(4 * time.Minute)
	require.True(t, tracker.UpdateAuthorized(deviationReport(t6, "bus-1", deviationServiceDate, 750.5, ahead), time.Now()))

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
// report sent to POST /api/v1/locations, the path a driver's phone takes.
func TestTripUpdates_ThroughThePostedReport(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	post := handlePostLocation(&mockStore{}, tracker, rl)

	// T6 reaches ST2 at 12:05 on deviationServiceDate; a report now is late
	// by however long ago that was.
	pos := t6.Shape.PointAt(750.5)
	w := postLocationWithClaims(post, LocationReport{VehicleID: "bus-1", TripID: "T6", StartDate: deviationServiceDate,
		Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}, jwt.MapClaims{"sub": "driver-1"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	updates := tripUpdatesOf(t, tracker)
	require.Contains(t, updates, "bus-1")
	assert.Positive(t, updates["bus-1"].GetDelay())
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
