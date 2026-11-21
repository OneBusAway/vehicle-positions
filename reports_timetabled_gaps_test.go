package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// timetableFixtureIndex is the report fixture with two more routes north
// along S1 through ST1, ST2 and ST3, each trip leaving ST2 five minutes after
// ST1. Route 6 runs by timetable alone, leaving ST1 at 06:30 (6A), every 10
// minutes from 07:00 to 07:30 (6B to 6E), at 07:42 (6F), 12 minutes later,
// and at 07:55 (6G), 13 minutes after that: frequent from 07:00 to 07:42.
// Route 7 mixes the two: 7F runs from frequencies.txt every 20 minutes from
// 08:00 to 09:00, and 7B, 7C and 7D by timetable at 08:05, 08:15 and 08:25,
// so a vehicle is due every 6m40s while both run.
func timetableFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	trips := make([]string, 0)
	stopTimes := make([]string, 0)
	add := func(route, trip string, leave time.Duration) {
		trips = append(trips, route+",WEEKDAY,"+trip+",S1,North,0")
		for i, along := range []string{"0.000000", "500.000000", "1001.000000"} {
			at := serviceClock(leave + time.Duration(i)*5*time.Minute)
			stopTimes = append(stopTimes, fmt.Sprintf("%s,%s,%s,ST%d,%d,%s", trip, at, at, i+1, i+1, along))
		}
	}
	for i, leave := range []time.Duration{hms(6, 30, 0), hms(7, 0, 0), hms(7, 10, 0), hms(7, 20, 0), hms(7, 30, 0), hms(7, 42, 0), hms(7, 55, 0)} {
		add("R6", "6"+string(rune('A'+i)), leave)
	}
	add("R7", "7F", hms(6, 0, 0))
	for i, leave := range []time.Duration{hms(8, 5, 0), hms(8, 15, 0), hms(8, 25, 0)} {
		add("R7", "7"+string(rune('B'+i)), leave)
	}
	zip := reportFixtureZip(t, func(name string, body []byte) []byte {
		switch name {
		case "routes.txt":
			return append(body, "R6,A,6,Peak by timetable,3,6,,\nR7,A,7,Mixed,3,7,,\n"...)
		case "trips.txt":
			return append(body, strings.Join(trips, "\n")+"\n"...)
		case "stop_times.txt":
			return append(body, strings.Join(stopTimes, "\n")+"\n"...)
		}
		return body
	}, fixtureMember{"frequencies.txt", "trip_id,start_time,end_time,headway_secs,exact_times\n7F,08:00:00,09:00:00,1200,0\n"})
	ix, err := rider.IndexFromBytes(zip, "edited", catalogNow)
	require.NoError(t, err)
	return ix
}

// timetableDayRuns are routes 6 and 7 on the day, every run leaving ST2 five
// minutes after ST1. Route 6: 6A on time at 06:30, alone and so On time's
// only; 6B on time; 6C two minutes late, 12 minutes after 6B; 6D six minutes
// early, bunched 2 minutes behind 6C; 6E on time, 16 minutes after 6D, long
// against the 10 minutes up to its departure; 6F a minute late, 13 minutes
// after 6E against 12; and 6G five minutes early at 07:50, inside the
// stretch but not in the order, its departure 13 minutes after 6F's. Route
// 7: 7F's runs on the headway at 08:00, 08:20 and 08:40, 7B and 7D on time
// and 7C a minute late, 11 minutes after 7B and long against every 6m40s.
var timetableDayRuns = []gapRun{
	{name: "6A", tripID: "6A", legs: north(hms(6, 30, 0), hms(6, 35, 0))},
	{name: "6B", tripID: "6B", legs: north(hms(7, 0, 0), hms(7, 5, 0))},
	{name: "6C", tripID: "6C", legs: north(hms(7, 12, 0), hms(7, 17, 0))},
	{name: "6D", tripID: "6D", legs: north(hms(7, 14, 0), hms(7, 19, 0))},
	{name: "6E", tripID: "6E", legs: north(hms(7, 30, 0), hms(7, 35, 0))},
	{name: "6F", tripID: "6F", legs: north(hms(7, 43, 0), hms(7, 48, 0))},
	{name: "6G", tripID: "6G", legs: north(hms(7, 50, 0), hms(7, 55, 0))},
	{name: "F1", tripID: "7F", legs: north(hms(8, 0, 0), hms(8, 5, 0))},
	{name: "7B", tripID: "7B", legs: north(hms(8, 5, 0), hms(8, 10, 0))},
	{name: "7C", tripID: "7C", legs: north(hms(8, 16, 0), hms(8, 21, 0))},
	{name: "F2", tripID: "7F", legs: north(hms(8, 20, 0), hms(8, 25, 0))},
	{name: "7D", tripID: "7D", legs: north(hms(8, 25, 0), hms(8, 30, 0))},
	{name: "F3", tripID: "7F", legs: north(hms(8, 40, 0), hms(8, 45, 0))},
}

// newTimetableReporter is a reporter over the report fixture's day with
// routes 6 and 7 run, its clock at the fixture's now.
func newTimetableReporter(t *testing.T) (*serviceReporter, *fakeReportStore) {
	t.Helper()
	ix := timetableFixtureIndex(t)
	trips, trails := reportFixtureTrips(t, ix)
	more, moreTrails := gapTrips(t, ix, 200, timetableDayRuns)
	for id, points := range moreTrails {
		trails[id] = points
	}
	store := &fakeReportStore{trips: append(trips, more...), trails: trails}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }
	return r, store
}

// TestServiceReporter_TimetabledGaps: a route run by timetable is measured
// where it runs every 12 minutes or more often, and its trips are still
// judged On time; a mixed route's timetable adds to its frequencies.txt
// trips. Routes 1 to 3 run hourly or less, so they have no gaps.
func TestServiceReporter_TimetabledGaps(t *testing.T) {
	r, _ := newTimetableReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)

	// Route 6, at ST1 and at ST2 alike: 6C 12 minutes against 10, 6D
	// bunched, 6E long and 6F 13 minutes against 12. Extra wait is
	// 4125600 / 10320 - 3196800 / 10080 = 399.8 - 317.1 = 83 s.
	assert.JSONEq(t, `[
		{"direction_id":0,"headsign":"North","gaps_judged":8,"bunched":2,"long_gaps":2,"unknown":0,"extra_wait_s":83,"scheduled_gap_s":600}
	]`, routeGapsJSON(t, reportRoute(t, rep, "R6")))
	r6 := reportRoute(t, rep, "R6")
	assert.Equal(t, 7, r6.TripsRun)
	assert.Equal(t, 21, r6.StopsOnTime+r6.StopsEarly+r6.StopsLate+r6.StopsUnknown, "each of the seven trips judged On time at its three stops")

	// Route 7, at ST1 and at ST2 alike: 7B, F2 and 7D 4 or 5 minutes and 7C
	// 11 minutes, long, against every 6m40s, then F3 15 minutes against 20
	// once the timetable's last spacing is past. 2966400 / 9600 - 4160000 /
	// 11200 = 309 - 371.4 = -62 s.
	assert.JSONEq(t, `[
		{"direction_id":0,"headsign":"North","gaps_judged":10,"bunched":0,"long_gaps":2,"unknown":0,"extra_wait_s":-62,"scheduled_gap_s":400}
	]`, routeGapsJSON(t, reportRoute(t, rep, "R7")))

	for _, route := range rep.Routes {
		if route.RouteID != "R6" && route.RouteID != "R7" {
			assert.JSONEq(t, `[]`, routeGapsJSON(t, route), "route %s", route.RouteID)
		}
	}
}

// TestNewDayTotals_TimetabledGaps: the kept totals keep a timetabled
// direction's gaps as they keep a headway one's.
func TestNewDayTotals_TimetabledGaps(t *testing.T) {
	r, _ := newTimetableReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	encoded, err := json.Marshal(newDayTotals(rep, testReportSettings))
	require.NoError(t, err)
	var totals struct {
		Routes []struct {
			RouteID string          `json:"route_id"`
			Gaps    json.RawMessage `json:"gaps"`
		} `json:"routes"`
		GapRatios json.RawMessage `json:"gap_ratios"`
	}
	require.NoError(t, json.Unmarshal(encoded, &totals))
	gaps := make(map[string]string)
	for _, route := range totals.Routes {
		gaps[route.RouteID] = string(route.Gaps)
	}
	assert.JSONEq(t, `[{"direction_id":0,"headsign":"North","hours":[
		{"hour":7,"gaps":8,"bunched":2,"long_gaps":2,"unknown":0,"sum_gap_s":5160,"sum_gap_sq":4125600,"sum_scheduled_s":5040,"sum_scheduled_sq":3196800}
	]}]`, gaps["R6"])
	assert.JSONEq(t, `[{"direction_id":0,"headsign":"North","hours":[
		{"hour":8,"gaps":10,"bunched":0,"long_gaps":2,"unknown":0,"sum_gap_s":4800,"sum_gap_sq":2966400,"sum_scheduled_s":5600,"sum_scheduled_sq":4160000}
	]}]`, gaps["R7"])
	// 20%: 6D twice. 60%: F2 twice. 70%: 7B, 7D and F3 twice. 100%: 6F
	// twice. 120%: 6C twice. 160%: 6E and 7C twice.
	assert.JSONEq(t, `[
		{"tenth":2,"gaps":2},{"tenth":6,"gaps":2},{"tenth":7,"gaps":6},{"tenth":10,"gaps":2},{"tenth":12,"gaps":2},{"tenth":16,"gaps":4}
	]`, string(totals.GapRatios))
}

// TestReportsPage_KeptDaysBeforeTimetabledGapsAreWorkedOutAgain: a day kept
// by a server that measured gaps on frequencies.txt trips alone, version 3,
// is worked out again and kept with route 6's gaps.
func TestReportsPage_KeptDaysBeforeTimetabledGapsAreWorkedOutAgain(t *testing.T) {
	r, store := newTimetableReporter(t)
	reportsView(t, r, "date=2026-09-02&range=day")
	kept := store.days["2026-09-02"]
	require.Equal(t, dayTotalsVersion, kept.Totals.Version)
	want := kept.Totals

	// What version 3 kept: no gaps on route 6, which has no trip in
	// frequencies.txt.
	old := kept
	old.Totals.Version = 3
	old.Totals.Routes = slices.Clone(want.Routes)
	i := slices.IndexFunc(old.Totals.Routes, func(route routeDayTotals) bool { return route.RouteID == "R6" })
	require.GreaterOrEqual(t, i, 0)
	old.Totals.Routes[i].Gaps = make([]directionGapTotals, 0)
	store.days["2026-09-02"] = old
	reads, saves := store.reads, store.saves

	again := newServiceReporter(store, r.index, testReportSettings)
	again.now = r.now
	days, err := again.storedDays(context.Background(), reportDay, reportDay)
	require.NoError(t, err)
	assert.Empty(t, days, "a day kept before timetabled gaps is not read as kept")

	reportsView(t, again, "date=2026-09-02&range=day")
	assert.Equal(t, reads+1, store.reads, "worked out again")
	assert.Equal(t, saves+1, store.saves, "and kept again")
	assert.Equal(t, want, store.days["2026-09-02"].Totals)
	require.NotEmpty(t, store.days["2026-09-02"].Totals.Routes[i].Gaps, "with route 6's gaps")
}
