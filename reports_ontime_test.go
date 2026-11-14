package main

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestReportSettings_Verdict(t *testing.T) {
	cases := []struct {
		name      string
		deviation time.Duration
		want      stopVerdict
	}{
		{"one second past a minute early", -61 * time.Second, stopEarly},
		{"exactly a minute early", -60 * time.Second, stopOnTime},
		{"on schedule", 0, stopOnTime},
		{"exactly five minutes late", 300 * time.Second, stopOnTime},
		{"one second past five minutes late", 301 * time.Second, stopLate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, testReportSettings.verdict(tc.deviation))
		})
	}

	strict := reportSettings{onTimeEarly: 0, onTimeLate: time.Minute}
	assert.Equal(t, stopEarly, strict.verdict(-time.Second), "a zero early margin leaves no early tolerance")
	assert.Equal(t, stopOnTime, strict.verdict(0))
}

func TestPassedAt(t *testing.T) {
	base := time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	fix := func(s int, along float64) placedFix { return placedFix{at: at(s), along: along} }

	cases := []struct {
		name   string
		fixes  []placedFix
		target float64
		want   time.Time
		known  bool
	}{
		{"interpolated between the fixes either side", []placedFix{fix(0, 0), fix(10, 100), fix(20, 200)}, 130, at(13), true},
		{"a fix exactly at the target", []placedFix{fix(0, 0), fix(10, 100)}, 100, at(10), true},
		{"rounded to the second", []placedFix{fix(0, 0), fix(10, 30)}, 10, at(3), true},
		{"the first time it got there, not a later return", []placedFix{fix(0, 0), fix(10, 100), fix(20, 50), fix(30, 150)}, 75, at(8), true},
		{"the first fix already past it", []placedFix{fix(0, 200), fix(10, 300)}, 100, time.Time{}, false},
		{"no fix got there", []placedFix{fix(0, 0), fix(10, 90)}, 100, time.Time{}, false},
		{"no fixes", nil, 100, time.Time{}, false},
		{"exactly two minutes either side", []placedFix{fix(0, 0), fix(240, 200)}, 100, at(120), true},
		{"over two minutes since the fix before", []placedFix{fix(0, 0), fix(130, 100), fix(140, 200)}, 99, time.Time{}, false},
		{"over two minutes to the fix after", []placedFix{fix(0, 0), fix(10, 1), fix(140, 101)}, 2, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := passedAt(tc.fixes, tc.target)
			require.Equal(t, tc.known, known)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestJudgedStops(t *testing.T) {
	stops := func(timepoints ...bool) []rider.StopTimeInfo {
		out := make([]rider.StopTimeInfo, 0, len(timepoints))
		for _, tp := range timepoints {
			out = append(out, rider.StopTimeInfo{Timepoint: tp})
		}
		return out
	}
	assert.Equal(t, []int{0, 1, 2}, judgedStops(stops(true, true, true)), "every stop a timepoint")
	assert.Equal(t, []int{0, 2}, judgedStops(stops(true, false, true)), "only the timepoints")
	assert.Equal(t, []int{0, 1, 2}, judgedStops(stops(false, false, false)), "none marked: every stop")
	assert.Empty(t, judgedStops(nil))
}

func TestPlaceFixes_LeavesOutFixesOffTheRoute(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	maxShapeDistance := testReportSettings.maxShapeDistance
	at := func(along, east float64) rider.LatLon {
		return rider.OffsetMetres(t1.Shape.PointAt(along), 0, east)
	}
	accuracy := 50.0
	points := []LocationPoint{
		// Out of time order, as two driver trips of one run are merged.
		{Latitude: at(300, 0).Lat, Longitude: at(300, 0).Lon, Timestamp: 30},
		{Latitude: at(100, 0).Lat, Longitude: at(100, 0).Lon, Timestamp: 10},
		// 100 m off: past the off-route distance.
		{Latitude: at(200, 100).Lat, Longitude: at(200, 100).Lon, Timestamp: 20},
		// 100 m off too, but its own accuracy of 50 m takes the tolerance to 110 m.
		{Latitude: at(250, 100).Lat, Longitude: at(250, 100).Lon, Timestamp: 25, Accuracy: &accuracy},
	}

	fixes := placeFixes(t1, points, maxShapeDistance)
	require.Len(t, fixes, 3)
	assert.Equal(t, []int64{10, 25, 30}, []int64{fixes[0].at.Unix(), fixes[1].at.Unix(), fixes[2].at.Unix()})
	assert.InDelta(t, 100, fixes[0].along, 0.5)
	assert.InDelta(t, 250, fixes[1].along, 0.5)
	assert.InDelta(t, 300, fixes[2].along, 0.5)
}

func TestEvaluateRun_FixtureDay(t *testing.T) {
	ix := reportFixtureIndex(t)
	trips, trails := reportFixtureTrips(t, ix)

	seconds := func(s int) *time.Duration { d := time.Duration(s) * time.Second; return &d }
	want := map[string]runEvaluation{
		// Waiting at the first stop before leaving on time is not early.
		"T1": {departure: seconds(6), arrival: seconds(-16), counts: stopCounts{onTime: 3}},
		"RE": {departure: seconds(-174), arrival: seconds(-586), counts: stopCounts{early: 3}},
		"RL": {departure: seconds(426), arrival: seconds(214), counts: stopCounts{late: 2, onTime: 1}},
		// Interpolated across the silence, ST2 would be on time at 13:07:03.
		"RG": {departure: seconds(6), arrival: seconds(-16), counts: stopCounts{onTime: 2, unknown: 1}},
		"RM": {departure: seconds(6), arrival: seconds(-16), counts: stopCounts{onTime: 3}},
		"T3": {departure: seconds(3), arrival: seconds(-6), counts: stopCounts{onTime: 4}},
		"OB": {departure: seconds(6), arrival: seconds(-6), counts: stopCounts{onTime: 5}},
	}
	require.Len(t, want, len(reportFixtureRuns))
	for i, run := range reportFixtureRuns {
		t.Run(run.tripID, func(t *testing.T) {
			trip := mustTrip(t, ix, run.tripID)
			got := evaluateRuns(trip, reportDayStart, [][]LocationPoint{trails[trips[i].ID]}, testReportSettings)
			assert.Equal(t, want[run.tripID], got)
		})
	}
}

// TestEvaluateRun_OutAndBackKeepsTheReturnLeg pins what the hint is for: on
// OB's way back every fix is nearer the outbound leg, and placed with no hint
// they would land there, leaving OB4 and OB5 never reached.
func TestEvaluateRun_OutAndBackKeepsTheReturnLeg(t *testing.T) {
	ix := reportFixtureIndex(t)
	ob := mustTrip(t, ix, "OB")
	trips, trails := reportFixtureTrips(t, ix)
	var points []LocationPoint
	for _, tr := range trips {
		if tr.GtfsTripID == "OB" {
			points = trails[tr.ID]
		}
	}
	require.NotEmpty(t, points)

	returning := 0
	for _, p := range points {
		pos := rider.LatLon{Lat: p.Latitude, Lon: p.Longitude}
		if nearest := ob.Shape.Project(pos, nil); nearest.AlongShape < 1000 && time.Unix(p.Timestamp, 0).After(reportDayStart.Add(hms(14, 11, 0))) {
			returning++
		}
	}
	require.NotZero(t, returning, "the fixture's return leg must be nearer the outbound leg, or this test proves nothing")

	fixes := placeFixes(ob, points, testReportSettings.maxShapeDistance)
	assert.Greater(t, fixes[len(fixes)-1].along, ob.StopTimes[4].AlongShape-stoppedAtDistance)
}

func TestEvaluateRun_Timepoints(t *testing.T) {
	ix := reportFixtureIndex(t)
	trips, trails := reportFixtureTrips(t, ix)
	rl := mustTrip(t, ix, "RL")
	var points []LocationPoint
	for _, tr := range trips {
		if tr.GtfsTripID == "RL" {
			points = trails[tr.ID]
		}
	}

	// RL is late at ST1 and ST2 and on time at ST3: judged only at ST1 and
	// ST3, it is one of each.
	marked := *rl
	marked.StopTimes = slices.Clone(rl.StopTimes)
	marked.StopTimes[1].Timepoint = false
	got := evaluateRuns(&marked, reportDayStart, [][]LocationPoint{points}, testReportSettings)
	assert.Equal(t, stopCounts{late: 1, onTime: 1}, got.counts)
}

// TestEvaluateRun_EndsShortOfTheLastStop: a bus that pulls in 15 m short of
// its terminal has reached it, though it never gets to the end of the shape.
func TestEvaluateRun_EndsShortOfTheLastStop(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	legs := []reportLeg{
		{hms(7, 57, 0), 0}, {hms(8, 0, 0), 0},
		{hms(8, 1, 40), 500}, {hms(8, 8, 10), 500},
		{hms(8, 9, 50), t1.StopTimes[2].AlongShape - 15}, {hms(8, 15, 0), t1.StopTimes[2].AlongShape - 15},
	}

	got := evaluateRuns(t1, reportDayStart, [][]LocationPoint{driveTrip(t1, reportDayStart, legs, nil)}, testReportSettings)
	require.NotNil(t, got.arrival)
	assert.Equal(t, stopCounts{onTime: 3}, got.counts)
}

// TestEvaluateRuns_DoesNotJoinTwoVehicles: one vehicle stops reporting 400 m
// along and another starts 600 m along ten seconds later. Nobody was seen
// passing ST2 at 500 m, and a time worked out between the two vehicles would
// call it known.
func TestEvaluateRuns_DoesNotJoinTwoVehicles(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	first := driveTrip(t1, reportDayStart, []reportLeg{
		{hms(7, 57, 0), 0}, {hms(8, 0, 0), 0}, {hms(8, 1, 20), 400},
	}, nil)
	second := driveTrip(t1, reportDayStart, []reportLeg{
		{hms(8, 1, 30), 600}, {hms(8, 2, 50), t1.Shape.Length}, {hms(8, 5, 0), t1.Shape.Length},
	}, nil)

	got := evaluateRuns(t1, reportDayStart, [][]LocationPoint{first, second}, testReportSettings)
	require.NotNil(t, got.departure, "the first vehicle left ST1")
	require.NotNil(t, got.arrival, "the second vehicle reached ST3")
	// ST1 on time from the first vehicle, ST3 early from the second, and ST2
	// unknown.
	assert.Equal(t, stopCounts{onTime: 1, early: 1, unknown: 1}, got.counts)
}

// TestEvaluateRuns_TakesTheFirstRunToPassEachStop: T1 run on time and run
// again an hour later is on time, whichever run the store lists first.
func TestEvaluateRuns_TakesTheFirstRunToPassEachStop(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	onTime := driveTrip(t1, reportDayStart, straightLegs(hms(7, 57, 0), hms(8, 0, 0), hms(8, 8, 10))(t1), nil)
	anHourLater := driveTrip(t1, reportDayStart, straightLegs(hms(8, 57, 0), hms(9, 0, 0), hms(9, 8, 10))(t1), nil)

	for _, trails := range [][][]LocationPoint{{onTime, anHourLater}, {anHourLater, onTime}} {
		got := evaluateRuns(t1, reportDayStart, trails, testReportSettings)
		require.NotNil(t, got.departure)
		assert.Equal(t, 6*time.Second, *got.departure)
		assert.Equal(t, stopCounts{onTime: 3}, got.counts)
	}
}

// TestEvaluateRuns_ASecondVehicleFinishesTheTrip: the first vehicle breaks
// down past ST2 and a second one, starting short of where it stopped, takes
// the trip on to ST3 late. Each stop is judged on the vehicle that passed it.
func TestEvaluateRuns_ASecondVehicleFinishesTheTrip(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	brokeDown := driveTrip(t1, reportDayStart, []reportLeg{
		{hms(7, 57, 0), 0}, {hms(8, 0, 0), 0}, {hms(8, 1, 40), 500}, {hms(8, 5, 0), 500}, {hms(8, 5, 40), 700},
	}, nil)
	replacement := driveTrip(t1, reportDayStart, []reportLeg{
		{hms(8, 20, 0), 650}, {hms(8, 21, 10), t1.Shape.Length}, {hms(8, 23, 0), t1.Shape.Length},
	}, nil)

	got := evaluateRuns(t1, reportDayStart, [][]LocationPoint{brokeDown, replacement}, testReportSettings)
	require.NotNil(t, got.arrival)
	assert.Greater(t, *got.arrival, 10*time.Minute)
	assert.Equal(t, stopCounts{onTime: 2, late: 1}, got.counts)
}

func TestEvaluateRun_NoPoints(t *testing.T) {
	ix := reportFixtureIndex(t)
	got := evaluateRuns(mustTrip(t, ix, "T1"), reportDayStart, nil, testReportSettings)
	assert.Equal(t, runEvaluation{counts: stopCounts{unknown: 3}}, got)
}

// TestEvaluateRun_ShapeDrawnThroughStops: tripLocator does not place a
// vehicle on a shape drawn through the stops, and neither does the report, so
// a run of T4 driven exactly along that shape is unknown everywhere.
func TestEvaluateRun_ShapeDrawnThroughStops(t *testing.T) {
	ix := reportFixtureIndex(t)
	t4 := mustTrip(t, ix, "T4")
	require.True(t, t4.ShapeFromStops)
	points := driveTrip(t4, reportDayStart, stopLegs(hms(9, 58, 0), hms(10, 0, 0))(t4), nil)
	require.NotEmpty(t, points)

	got := evaluateRuns(t4, reportDayStart, [][]LocationPoint{points}, testReportSettings)
	assert.Equal(t, runEvaluation{counts: stopCounts{unknown: 2}}, got)
}

func TestStopCounts_Percentages(t *testing.T) {
	c := stopCounts{onTime: 9, early: 3, late: 2, unknown: 1}
	require.NotNil(t, c.onTimePercent())
	assert.Equal(t, 64.3, *c.onTimePercent(), "9 of the 14 known stops")
	require.NotNil(t, c.unknownPercent())
	assert.Equal(t, 6.7, *c.unknownPercent(), "1 of the 15 stops")

	allUnknown := stopCounts{unknown: 2}
	assert.Nil(t, allUnknown.onTimePercent(), "no stop known: no on-time share")
	assert.Equal(t, 100.0, *allUnknown.unknownPercent())

	assert.Nil(t, stopCounts{}.unknownPercent(), "nothing judged: no unknown share")
}
