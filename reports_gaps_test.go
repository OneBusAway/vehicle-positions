package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gapFixtureIndex is the report fixture with route 5, run from frequencies.txt
// both ways along the S1 road. GN runs north through ST1, ST2 and ST3,
// leaving ST2 five minutes after ST1, every 10 minutes from 07:00 to 08:00
// and from 19:00 to 20:00, and every 30 minutes from 23:30 to 24:30. GT turns
// short at ST2 every 10 minutes from 19:05 to 20:05, so ST1 has a vehicle due
// every 5 minutes then and ST2 every 10. GS runs south along S1R through ST3,
// ST2 and ST1 every 15 minutes from 07:00 to 08:00. GX is a timetabled trip
// north at 07:35.
func gapFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	zip := reportFixtureZip(t, func(name string, body []byte) []byte {
		switch name {
		case "routes.txt":
			return append(body, "R5,A,5,Every ten,3,5,,\n"...)
		case "shapes.txt":
			return append(body, "S1R,47.609000,-122.330000,1,0.000000\nS1R,47.604500,-122.330000,2,501.000000\nS1R,47.600000,-122.330000,3,1001.000000\n"...)
		case "trips.txt":
			return append(body, "R5,WEEKDAY,GN,S1,North,0\nR5,WEEKDAY,GT,S1,North,0\nR5,WEEKDAY,GS,S1R,South,1\nR5,WEEKDAY,GX,S1,North,0\n"...)
		case "stop_times.txt":
			return append(body, strings.Join([]string{
				"GN,06:00:00,06:00:00,ST1,1,0.000000",
				"GN,06:05:00,06:05:00,ST2,2,500.000000",
				"GN,06:10:00,06:10:00,ST3,3,1001.000000",
				"GT,06:00:00,06:00:00,ST1,1,0.000000",
				"GT,06:05:00,06:05:00,ST2,2,500.000000",
				"GS,06:00:00,06:00:00,ST3,1,0.000000",
				"GS,06:05:00,06:05:00,ST2,2,501.000000",
				"GS,06:10:00,06:10:00,ST1,3,1001.000000",
				"GX,07:35:00,07:35:00,ST1,1,0.000000",
				"GX,07:40:00,07:40:00,ST2,2,500.000000",
				"GX,07:45:00,07:45:00,ST3,3,1001.000000",
			}, "\n")+"\n"...)
		}
		return body
	}, fixtureMember{"frequencies.txt", "trip_id,start_time,end_time,headway_secs,exact_times\n" +
		"GN,07:00:00,08:00:00,600,0\nGN,19:00:00,20:00:00,600,0\nGN,23:30:00,24:30:00,1800,0\n" +
		"GT,19:05:00,20:05:00,600,0\nGS,07:00:00,08:00:00,900,0\n"})
	ix, err := rider.IndexFromBytes(zip, "edited", catalogNow)
	require.NoError(t, err)
	return ix
}

// gapRun is one driver trip of route 5: its GTFS trip, the vehicle's
// waypoints, a stretch its phone was silent, and when the trip ended, a
// minute after the last waypoint unless end is set. A run with noFixes sent
// nothing between its first waypoint, its start, and its last.
type gapRun struct {
	name    string
	tripID  string
	legs    func(trip *rider.TripInfo) []reportLeg
	silent  [2]time.Duration
	end     time.Duration
	running bool
	noFixes bool
}

// north leaves ST1 at leave and ST2 at leaveST2, having waited at ST1 for a
// minute: it passes 30 m beyond each 6 s after leaving it.
func north(leave, leaveST2 time.Duration) func(trip *rider.TripInfo) []reportLeg {
	return straightLegs(leave-time.Minute, leave, leaveST2)
}

// south leaves ST3 at leave and ST2 at leaveST2, along S1R.
func south(leave, leaveST2 time.Duration) func(trip *rider.TripInfo) []reportLeg {
	return stopLegs(leave-time.Minute, leave, leaveST2)
}

// gapTrips are runs as the store holds them, numbered from first, each
// started at its first waypoint.
func gapTrips(t *testing.T, ix *rider.Index, first int64, runs []gapRun) ([]TripSummary, map[int64][]LocationPoint) {
	t.Helper()
	trips := make([]TripSummary, 0, len(runs))
	trails := make(map[int64][]LocationPoint, len(runs))
	for i, run := range runs {
		trip := mustTrip(t, ix, run.tripID)
		legs := run.legs(trip)
		id := first + int64(i)
		summary := TripSummary{
			ID: id, VehicleID: "bus-" + run.name, VehicleLabel: "Bus " + run.name, DriverName: "Driver " + run.name,
			RouteID: trip.RouteID, GtfsTripID: run.tripID, StartTime: reportDayStart.Add(legs[0].at), Status: "completed",
		}
		end := legs[len(legs)-1].at + time.Minute
		if run.end != 0 {
			end = run.end
		}
		if run.running {
			summary.Status = "active"
		} else {
			ended := reportDayStart.Add(end)
			summary.EndTime = &ended
		}
		trips = append(trips, summary)
		if run.noFixes {
			continue
		}
		points := driveTrip(trip, reportDayStart, legs, nil)
		if run.silent != [2]time.Duration{} {
			points = withoutFixes(points, reportDayStart, run.silent[0], run.silent[1])
		}
		trails[id] = points
	}
	return trips, trails
}

// gapDayRuns is route 5's day. North at ST1 (every 10 minutes): A first, B
// on the gap, C and D bunched 2 minutes apart, H 16 minutes after D (long),
// W on GX, a timetabled trip that does not count, E and F on the gap, and G
// 15 minutes after F at 08:05, after the window ended: 150%, not long. North at
// ST2: C waits there while D overtakes it, H turned short before it and so
// did not serve it, leaving a long gap from C to E, and F's phone was silent
// as it left it, so the gap from E to G is unknown. I leaves at 23:58 and
// reaches ST2 after midnight, and J leaves at 00:31, after the 23:30 to 24:30
// window, dated to the day by the run late grace: 33 minutes after I, every
// 30. South at ST3 and ST2 (every 15 minutes): K first, L on the gap, M
// bunched, and N sent no fix from 07:30 to 07:45, so the gap from M to O is
// unknown at both.
var gapDayRuns = []gapRun{
	{name: "A", tripID: "GN", legs: north(hms(7, 0, 0), hms(7, 5, 0))},
	{name: "B", tripID: "GN", legs: north(hms(7, 10, 0), hms(7, 15, 0))},
	{name: "C", tripID: "GN", legs: north(hms(7, 12, 0), hms(7, 24, 0))},
	{name: "D", tripID: "GN", legs: north(hms(7, 14, 0), hms(7, 17, 0))},
	{name: "H", tripID: "GN", end: hms(7, 32, 0), legs: func(*rider.TripInfo) []reportLeg {
		return []reportLeg{{hms(7, 29, 0), 0}, {hms(7, 30, 0), 0}, {hms(7, 31, 0), 300}}
	}},
	{name: "W", tripID: "GX", legs: north(hms(7, 35, 0), hms(7, 40, 0))},
	{name: "E", tripID: "GN", legs: north(hms(7, 40, 0), hms(7, 45, 0))},
	{name: "F", tripID: "GN", legs: north(hms(7, 50, 0), hms(7, 55, 0)), silent: [2]time.Duration{hms(7, 53, 0), hms(7, 56, 0)}},
	{name: "G", tripID: "GN", legs: north(hms(8, 5, 0), hms(8, 10, 0))},
	{name: "I", tripID: "GN", legs: north(hms(23, 58, 0), hms(24, 3, 0))},
	{name: "J", tripID: "GN", legs: north(hms(24, 31, 0), hms(24, 36, 0))},
	{name: "K", tripID: "GS", legs: south(hms(7, 0, 0), hms(7, 5, 0))},
	{name: "L", tripID: "GS", legs: south(hms(7, 15, 0), hms(7, 20, 0))},
	{name: "M", tripID: "GS", legs: south(hms(7, 18, 0), hms(7, 23, 0))},
	{name: "N", tripID: "GS", noFixes: true, legs: func(*rider.TripInfo) []reportLeg {
		return []reportLeg{{hms(7, 30, 0), 0}, {hms(7, 44, 0), 0}}
	}},
	{name: "O", tripID: "GS", legs: south(hms(7, 45, 0), hms(7, 50, 0))},
}

// gapFixtureTrips is the report fixture's day with route 5's runs added.
func gapFixtureTrips(t *testing.T, ix *rider.Index) ([]TripSummary, map[int64][]LocationPoint) {
	t.Helper()
	trips, trails := reportFixtureTrips(t, ix)
	more, moreTrails := gapTrips(t, ix, 200, gapDayRuns)
	for id, points := range moreTrails {
		trails[id] = points
	}
	return append(trips, more...), trails
}

func newGapReporter(t *testing.T) (*serviceReporter, *fakeReportStore, *rider.Index) {
	t.Helper()
	ix := gapFixtureIndex(t)
	trips, trails := gapFixtureTrips(t, ix)
	store := &fakeReportStore{trips: trips, trails: trails}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }
	return r, store, ix
}

// routeGapsJSON is the route's gaps as the API encodes them.
func routeGapsJSON(t *testing.T, route routeReport) string {
	t.Helper()
	encoded, err := json.Marshal(route)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	gaps, ok := fields["gaps"]
	require.True(t, ok, "route %s has no gaps field", route.RouteID)
	return string(gaps)
}

// TestServiceReporter_HeadwayGaps: the report gives each direction of a
// route run from frequencies.txt the gaps judged between its vehicles, and
// every route an empty list when none of its trips is.
func TestServiceReporter_HeadwayGaps(t *testing.T) {
	r, _, _ := newGapReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)

	// North: 13 gaps judged, 8 at ST1 and 5 at ST2; C and D at ST1 and D at
	// ST2 bunched; H at ST1 and E at ST2 long; G at ST2 unknown. Extra wait
	// is sum(h^2) / 2 sum(h) less the same over the scheduled gaps:
	// 12819600 / 20520 - 10440000 / 20400 = 624.7 - 511.8 = 113 s. South: L
	// and M at ST3 and ST2, M bunched at both, O unknown at both; 1684800 /
	// 4320 - 3240000 / 7200 = 390 - 450 = -60 s, the gaps judged being
	// shorter than scheduled.
	assert.JSONEq(t, `[
		{"direction_id":0,"headsign":"North","gaps_judged":13,"bunched":3,"long_gaps":2,"unknown":1,"extra_wait_s":113,"scheduled_gap_s":600},
		{"direction_id":1,"headsign":"South","gaps_judged":4,"bunched":2,"long_gaps":0,"unknown":2,"extra_wait_s":-60,"scheduled_gap_s":900}
	]`, routeGapsJSON(t, reportRoute(t, rep, "R5")))

	for _, route := range rep.Routes {
		if route.RouteID != "R5" {
			assert.JSONEq(t, `[]`, routeGapsJSON(t, route), "route %s runs by timetable", route.RouteID)
		}
	}
}

// TestServiceReporter_HeadwayGapsLeaveTheRestAlone: measuring gaps changes
// nothing else in the day's report.
func TestServiceReporter_HeadwayGapsLeaveTheRestAlone(t *testing.T) {
	r, _, _ := newGapReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	plain, _, _ := newFixtureReporter(t, reportFixtureNow)
	want, err := plain.report(context.Background(), "2026-09-02")
	require.NoError(t, err)

	got := make([]routeReport, 0, len(rep.Routes))
	for _, route := range rep.Routes {
		if route.RouteID != "R5" {
			got = append(got, route)
		}
	}
	assert.Equal(t, want.Routes, got)

	r5 := reportRoute(t, rep, "R5")
	assert.Equal(t, [2]int{6 + 6 + 2 + 6 + 4 + 1, 10 + 4 + 1}, [2]int{r5.TripsScheduled, r5.TripsRun},
		"GN's, GT's and GS's runs and GX; all ten of GN's runs on the day, I and J too, GS's four and W")
}

// TestNewDayTotals_HeadwayGaps: the kept totals keep each direction's gaps
// by the hour the later vehicle passed in, with the sums extra wait is worked
// out from, and the day's gaps by tenths of their scheduled gap.
func TestNewDayTotals_HeadwayGaps(t *testing.T) {
	r, _, _ := newGapReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	encoded, err := json.Marshal(newDayTotals(rep, testReportSettings))
	require.NoError(t, err)
	var totals struct {
		Version int `json:"version"`
		Routes  []struct {
			RouteID string          `json:"route_id"`
			Gaps    json.RawMessage `json:"gaps"`
		} `json:"routes"`
		GapRatios json.RawMessage `json:"gap_ratios"`
	}
	require.NoError(t, json.Unmarshal(encoded, &totals))
	assert.Equal(t, dayTotalsVersion, totals.Version)

	gaps := make(map[string]string)
	for _, route := range totals.Routes {
		gaps[route.RouteID] = string(route.Gaps)
	}
	require.Contains(t, gaps, "R5")
	// North at 07: B, C, D, H, E and F at ST1 and B, D, C and E at ST2. At
	// 08: G at ST1, judged, and at ST2, unknown. At 24, after midnight on
	// the service day's clock: J at both.
	assert.JSONEq(t, `[
		{"direction_id":0,"headsign":"North","hours":[
			{"hour":7,"gaps":10,"bunched":3,"long_gaps":2,"unknown":0,"sum_gap_s":5400,"sum_gap_sq":4168800,"sum_scheduled_s":6000,"sum_scheduled_sq":3600000},
			{"hour":8,"gaps":1,"bunched":0,"long_gaps":0,"unknown":1,"sum_gap_s":900,"sum_gap_sq":810000,"sum_scheduled_s":600,"sum_scheduled_sq":360000},
			{"hour":24,"gaps":2,"bunched":0,"long_gaps":0,"unknown":0,"sum_gap_s":3960,"sum_gap_sq":7840800,"sum_scheduled_s":3600,"sum_scheduled_sq":6480000}
		]},
		{"direction_id":1,"headsign":"South","hours":[
			{"hour":7,"gaps":4,"bunched":2,"long_gaps":0,"unknown":2,"sum_gap_s":2160,"sum_gap_sq":1684800,"sum_scheduled_s":3600,"sum_scheduled_sq":3240000}
		]}
	]`, gaps["R5"])
	for id, g := range gaps {
		if id != "R5" {
			assert.JSONEq(t, `[]`, g, "route %s", id)
		}
	}
	// 20% (2 minutes or 3 of 15): C, D and M twice. 70%: C at ST2. 100%: B,
	// E and F at ST1, B at ST2, L twice. 110%: J twice. 150%: G. 160%: H.
	// 210%: E at ST2.
	assert.JSONEq(t, `[
		{"tenth":2,"gaps":5},{"tenth":7,"gaps":1},{"tenth":10,"gaps":6},{"tenth":11,"gaps":2},
		{"tenth":15,"gaps":1},{"tenth":16,"gaps":1},{"tenth":21,"gaps":1}
	]`, string(totals.GapRatios))
}

// TestReportsPage_KeptDaysBeforeGapsAreWorkedOutAgain: a day kept by a
// server that counted headway runs but not their gaps, version 1, is worked
// out again and kept with its gaps.
func TestReportsPage_KeptDaysBeforeGapsAreWorkedOutAgain(t *testing.T) {
	r, store, _ := newGapReporter(t)
	reportsView(t, r, "date=2026-09-02&range=day")
	kept := store.days["2026-09-02"]
	require.Equal(t, dayTotalsVersion, kept.Totals.Version)
	want := kept.Totals

	old := kept
	old.Totals = dayTotals{Version: 1, Routes: make([]routeDayTotals, 0), Arrivals: make([]arrivalMinute, 0)}
	store.days["2026-09-02"] = old
	reads, saves := store.reads, store.saves

	again := newServiceReporter(store, r.index, testReportSettings)
	again.now = r.now
	days, err := again.storedDays(context.Background(), reportDay, reportDay)
	require.NoError(t, err)
	assert.Empty(t, days, "a day kept before gaps is not read as kept")

	reportsView(t, again, "date=2026-09-02&range=day")
	assert.Equal(t, reads+1, store.reads, "worked out again")
	assert.Equal(t, saves+1, store.saves, "and kept again")
	assert.Equal(t, want, store.days["2026-09-02"].Totals)
	encoded, err := json.Marshal(store.days["2026-09-02"].Totals)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"gaps":[{"direction_id":0`, "with route 5's gaps")
}

// forgottenTripRuns is a day north on route 5 whose Z was never ended: it
// leaves ST1 at 07:30 between A and B, its phone goes silent a minute later,
// short of ST2, and it is still running. P and Q run late at night, every
// 30 minutes. Every run leaves ST2 five minutes after ST1.
var forgottenTripRuns = []gapRun{
	{name: "A", tripID: "GN", legs: north(hms(7, 20, 0), hms(7, 25, 0))},
	{name: "Z", tripID: "GN", running: true, legs: func(*rider.TripInfo) []reportLeg {
		return []reportLeg{{hms(7, 29, 0), 0}, {hms(7, 30, 0), 0}, {hms(7, 31, 0), 300}}
	}},
	{name: "B", tripID: "GN", legs: north(hms(7, 40, 0), hms(7, 45, 0))},
	{name: "P", tripID: "GN", legs: north(hms(23, 40, 0), hms(23, 45, 0))},
	{name: "Q", tripID: "GN", legs: north(hms(24, 10, 0), hms(24, 15, 0))},
}

// newForgottenTripReporter is a reporter over forgottenTripRuns alone, its
// clock at the fixture's now.
func newForgottenTripReporter(t *testing.T) (*serviceReporter, *fakeReportStore) {
	t.Helper()
	ix := gapFixtureIndex(t)
	trips, trails := gapTrips(t, ix, 1, forgottenTripRuns)
	store := &fakeReportStore{trips: trips, trails: trails}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }
	return r, store
}

// TestServiceReporter_HeadwayGapsAfterAForgottenTrip: Z could have left ST2
// no later than 09:11, GN's 10-minute run time and passingLateGrace after
// its last fix, so of ST2's gaps only A to B is unknown and the night's is
// judged, in the day's report and in its kept totals.
func TestServiceReporter_HeadwayGapsAfterAForgottenTrip(t *testing.T) {
	r, _ := newForgottenTripReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)

	// ST1: A to Z, Z to B and P to Q. ST2: A to B unknown, and P to Q.
	none := `"gaps_judged":0,"bunched":0,"long_gaps":0,"unknown":0,"extra_wait_s":null,"scheduled_gap_s":null`
	assert.JSONEq(t, `[
		{"direction_id":0,"headsign":"North","gaps_judged":4,"bunched":0,"long_gaps":0,"unknown":1,"extra_wait_s":0,"scheduled_gap_s":600},
		{"direction_id":1,"headsign":"South",`+none+`}
	]`, routeGapsJSON(t, reportRoute(t, rep, "R5")))

	encoded, err := json.Marshal(newDayTotals(rep, testReportSettings))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"gaps":[{"direction_id":0,"headsign":"North","hours":[`+
		`{"hour":7,"gaps":2,"bunched":0,"long_gaps":0,"unknown":1,"sum_gap_s":1200,"sum_gap_sq":720000,"sum_scheduled_s":1200,"sum_scheduled_sq":720000},`+
		`{"hour":24,"gaps":2,"bunched":0,"long_gaps":0,"unknown":0,"sum_gap_s":3600,"sum_gap_sq":6480000,"sum_scheduled_s":3600,"sum_scheduled_sq":6480000}]}`)
}

// TestReportsPage_KeptDaysBeforeTheLateGraceAreWorkedOutAgain: a day kept
// by a server that held a forgotten trip's passing open for the rest of the
// day, version 2, is worked out again and kept with the night's gap at ST2
// judged.
func TestReportsPage_KeptDaysBeforeTheLateGraceAreWorkedOutAgain(t *testing.T) {
	r, store := newForgottenTripReporter(t)
	reportsView(t, r, "date=2026-09-02&range=day")
	kept := store.days["2026-09-02"]
	require.Equal(t, dayTotalsVersion, kept.Totals.Version)
	want := kept.Totals

	// What version 2 kept: P to Q at ST2 unknown, as A to B is.
	old := kept
	old.Totals.Version = 2
	old.Totals.Routes = []routeDayTotals{{RouteID: "R5", ShortName: "5", LongName: "Every ten", Hours: make([]hourTotals, 0),
		Gaps: []directionGapTotals{{DirectionID: ptr(0), Headsign: "North", Hours: []gapHourTotals{
			{Hour: 7, gapCounts: gapCounts{Gaps: 2, Unknown: 1, SumGapS: 1200, SumGapSq: 720000, SumScheduledS: 1200, SumScheduledSq: 720000}},
			{Hour: 24, gapCounts: gapCounts{Gaps: 1, Unknown: 1, SumGapS: 1800, SumGapSq: 3240000, SumScheduledS: 1800, SumScheduledSq: 3240000}},
		}}}}}
	old.Totals.GapRatios = []gapRatioCount{{Tenth: 10, Gaps: 3}}
	store.days["2026-09-02"] = old
	reads, saves := store.reads, store.saves

	again := newServiceReporter(store, r.index, testReportSettings)
	again.now = r.now
	days, err := again.storedDays(context.Background(), reportDay, reportDay)
	require.NoError(t, err)
	assert.Empty(t, days, "a day kept before the late grace is not read as kept")

	reportsView(t, again, "date=2026-09-02&range=day")
	assert.Equal(t, reads+1, store.reads, "worked out again")
	assert.Equal(t, saves+1, store.saves, "and kept again")
	assert.Equal(t, want, store.days["2026-09-02"].Totals)
	encoded, err := json.Marshal(store.days["2026-09-02"].Totals)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `{"hour":24,"gaps":2,"bunched":0,"long_gaps":0,"unknown":0,`, "with the night's gap at ST2 judged")
}

// serviceClock is d on the service day's clock, which runs past 24:00.
func serviceClock(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}
