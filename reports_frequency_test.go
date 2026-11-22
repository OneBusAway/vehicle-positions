package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// demoReportDay is a Wednesday of the demo feed (docs/demo.md), whose three
// routes run from a timetable every 15 minutes around the clock.
var demoReportDay = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

// demoDayRuns drives most of the demo feed's trips on demoReportDay: each
// trip off schedule by its own amount, drifting along the way, some with the
// phone silent for a stretch, some run twice and some not at all, plus the
// next day's run of some of them, which the day must leave out.
func demoDayRuns(t *testing.T, ix *rider.Index) ([]TripSummary, map[int64][]LocationPoint) {
	t.Helper()
	date := demoReportDay.Format(gtfsDateLayout)
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	require.NoError(t, err)
	var trips []TripSummary
	trails := make(map[int64][]LocationPoint)
	add := func(trip *rider.TripInfo, dayStart time.Time, legs []reportLeg, silent bool) {
		id := int64(len(trips) + 1)
		points := driveTrip(trip, dayStart, legs, nil)
		if silent {
			mid := (legs[0].at + legs[len(legs)-1].at) / 2
			points = withoutFixes(points, dayStart, mid-3*time.Minute, mid+3*time.Minute)
		}
		trips = append(trips, TripSummary{
			ID: id, VehicleID: fmt.Sprintf("bus-%d", id), VehicleLabel: fmt.Sprintf("Bus %d", id),
			DriverName: "Driver", RouteID: trip.RouteID, GtfsTripID: trip.ID,
			StartTime: dayStart.Add(legs[0].at), Status: "completed",
		})
		trails[id] = points
	}
	i := 0
	for _, route := range ix.Routes() {
		for _, trip := range ix.TripsOnRoute(route.ID, date) {
			i++
			if i%6 == 0 {
				continue
			}
			offset := time.Duration((i*37)%13-4)*time.Minute + time.Duration((i*17)%60)*time.Second
			drift := time.Duration(i%5-2) * 20 * time.Second
			legs := []reportLeg{{trip.StopTimes[0].Departure + offset - 2*time.Minute, trip.StopTimes[0].AlongShape}}
			for k, stop := range trip.StopTimes {
				shift := offset + time.Duration(k)*drift
				legs = append(legs, reportLeg{stop.Arrival + shift, stop.AlongShape}, reportLeg{stop.Departure + shift, stop.AlongShape})
			}
			add(trip, dayStart, legs, i%7 == 3)
			if i%11 == 5 {
				add(trip, dayStart, legs, false)
			}
			if i%9 == 0 {
				add(trip, dayStart.AddDate(0, 0, 1), legs, false)
			}
		}
	}
	return trips, trails
}

// TestServiceReporter_DemoDayIsUnchanged: on a feed with no frequencies.txt
// the day's report and its kept totals are exactly what they were before
// trips in frequencies.txt were counted by run, at c9c9b691. The route lines
// say it readably; the digest covers every trip's times, runs and verdicts,
// every hour of the totals and the arrival minutes. The gaps between vehicles
// came later and are measured only on trips in frequencies.txt and on
// timetables every 12 minutes or more often, so the feed, every 15 minutes,
// has none, and the digest takes the totals in the shape they had then.
func TestServiceReporter_DemoDayIsUnchanged(t *testing.T) {
	zip, err := os.ReadFile("demo/gtfs.zip")
	require.NoError(t, err)
	ix, err := rider.IndexFromBytes(zip, "demo", catalogNow)
	require.NoError(t, err)
	trips, trails := demoDayRuns(t, ix)
	store := &fakeReportStore{trips: trips, trails: trails}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return demoReportDay.AddDate(0, 0, 3) }

	rep, err := r.report(context.Background(), demoReportDay.Format(reportDateLayout))
	require.NoError(t, err)
	totals := newDayTotals(rep, testReportSettings)

	routes := make([]string, 0, len(rep.Routes))
	digest := sha256.New()
	for _, route := range rep.Routes {
		routes = append(routes, fmt.Sprintf("%s scheduled %d run %d stops %d/%d/%d/%d",
			route.RouteID, route.TripsScheduled, route.TripsRun, route.StopsOnTime, route.StopsEarly, route.StopsLate, route.StopsUnknown))
		for _, trip := range route.Trips {
			fmt.Fprintf(digest, "%s %d %d %v %v %v %d/%d/%d/%d\n", trip.GtfsTripID,
				trip.ScheduledDeparture.Unix(), trip.ScheduledArrival.Unix(), trip.Runs,
				deref(trip.DepartureDeviationS), deref(trip.ArrivalDeviationS),
				trip.StopsOnTime, trip.StopsEarly, trip.StopsLate, trip.StopsUnknown)
		}
	}
	type routeTotalsThen struct {
		RouteID   string       `json:"route_id"`
		ShortName string       `json:"short_name"`
		LongName  string       `json:"long_name"`
		Hours     []hourTotals `json:"hours"`
	}
	then := make([]routeTotalsThen, 0, len(totals.Routes))
	for i, route := range totals.Routes {
		assert.Empty(t, rep.Routes[i].Gaps, route.RouteID)
		assert.Empty(t, route.Gaps, route.RouteID)
		then = append(then, routeTotalsThen{route.RouteID, route.ShortName, route.LongName, route.Hours})
	}
	assert.Empty(t, totals.GapRatios)
	for _, part := range []any{then, totals.Arrivals, totals.ArrivalsLeftOut, rep.serviceEnds.Unix()} {
		b, err := json.Marshal(part)
		require.NoError(t, err)
		digest.Write(b)
	}

	assert.Equal(t, []string{
		"R1 scheduled 200 run 167 stops 572/291/425/215",
		"R2 scheduled 200 run 167 stops 584/298/405/216",
		"R3 scheduled 200 run 166 stops 755/455/574/42",
	}, routes)
	assert.Equal(t, "0330a9e9db2362ee60a87f8e188671539b6fe7b1c3e39fe4484ff237f7b2fad4", hex.EncodeToString(digest.Sum(nil)))
}

func deref(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

// headwayFixtureIndex is the report fixture with route 4, two trips along S1
// run from frequencies.txt. HW's pattern is 06:00 to 06:10, and it runs by
// headway every 10 minutes from 06:00 to 08:00, every 15 from 19:00 to 21:00
// and every 30 from 23:30 to 26:00: 12, 8 and 5 runs. HX's pattern is 08:00
// to 08:10, and it runs at exact times (exact_times=1) every 20 minutes from
// 09:00 to 10:00: 09:00, 09:20 and 09:40.
func headwayFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	pattern := func(trip, st1, st2, st3 string) string {
		return strings.Join([]string{
			trip + "," + st1 + "," + st1 + ",ST1,1,0.000000",
			trip + "," + st2 + "," + st2 + ",ST2,2,500.000000",
			trip + "," + st3 + "," + st3 + ",ST3,3,1001.000000",
		}, "\n")
	}
	zip := reportFixtureZip(t, func(name string, body []byte) []byte {
		switch name {
		case "routes.txt":
			return append(body, "R4,A,4,Every ten,3,4,,\n"...)
		case "trips.txt":
			return append(body, "R4,WEEKDAY,HW,S1,North,0\nR4,WEEKDAY,HX,S1,North,0\n"...)
		case "stop_times.txt":
			return append(body, pattern("HW", "06:00:00", "06:05:00", "06:10:00")+"\n"+pattern("HX", "08:00:00", "08:05:00", "08:10:00")+"\n"...)
		}
		return body
	}, fixtureMember{"frequencies.txt", "trip_id,start_time,end_time,headway_secs,exact_times\n" +
		"HW,06:00:00,08:00:00,600,0\nHW,19:00:00,21:00:00,900,0\nHW,23:30:00,26:00:00,1800,0\n" +
		"HX,09:00:00,10:00:00,1200,1\n"})
	ix, err := rider.IndexFromBytes(zip, "edited", catalogNow)
	require.NoError(t, err)
	return ix
}

// headwayFixtureTrips is the report fixture's day with HW run seven times
// and HX once, each run driving the pattern from when it started. HW's runs
// at 19:02 and 20:00 are over twelve hours from its pattern, and the one at
// 24:05 is after midnight; a run of HW the next morning is the next day's.
// HX's run at 09:21 is 81 minutes after its pattern, which a timetable would
// call late at every stop.
func headwayFixtureTrips(t *testing.T, ix *rider.Index) ([]TripSummary, map[int64][]LocationPoint) {
	t.Helper()
	trips, trails := reportFixtureTrips(t, ix)
	run := func(tripID string, dayStart time.Time, start time.Duration) {
		trip := mustTrip(t, ix, tripID)
		id := int64(100 + len(trips))
		legs := straightLegs(start, start+time.Minute, start+4*time.Minute)(trip)
		trips = append(trips, TripSummary{
			ID: id, VehicleID: fmt.Sprintf("bus-%d", id), VehicleLabel: fmt.Sprintf("Bus %d", id),
			DriverName: "Driver " + tripID, RouteID: trip.RouteID, GtfsTripID: tripID,
			StartTime: dayStart.Add(start), Status: "completed",
		})
		trails[id] = driveTrip(trip, dayStart, legs, nil)
	}
	for _, start := range []time.Duration{hms(6, 1, 0), hms(6, 12, 0), hms(6, 14, 0), hms(7, 58, 0), hms(19, 2, 0), hms(20, 0, 0), hms(24, 5, 0)} {
		run("HW", reportDayStart, start)
	}
	run("HW", reportDayStart.AddDate(0, 0, 1), hms(6, 3, 0))
	run("HX", reportDayStart, hms(9, 21, 0))
	return trips, trails
}

func newHeadwayReporter(t *testing.T) (*serviceReporter, *fakeReportStore, *rider.Index) {
	t.Helper()
	ix := headwayFixtureIndex(t)
	trips, trails := headwayFixtureTrips(t, ix)
	store := &fakeReportStore{trips: trips, trails: trails}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }
	return r, store, ix
}

func headwayDay(t *testing.T) (*serviceReport, *rider.Index) {
	t.Helper()
	r, _, ix := newHeadwayReporter(t)
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	return rep, ix
}

func reportRoute(t *testing.T, rep *serviceReport, routeID string) routeReport {
	t.Helper()
	for _, route := range rep.Routes {
		if route.RouteID == routeID {
			return route
		}
	}
	t.Fatalf("route %s is not in the report", routeID)
	return routeReport{}
}

func reportTrip(t *testing.T, route routeReport, tripID string) tripReport {
	t.Helper()
	for _, trip := range route.Trips {
		if trip.GtfsTripID == tripID {
			return trip
		}
	}
	t.Fatalf("trip %s is not in route %s", tripID, route.RouteID)
	return tripReport{}
}

func TestScheduledRuns(t *testing.T) {
	dayStart := reportDayStart
	at := func(d time.Duration) time.Time { return dayStart.Add(d) }
	tests := []struct {
		name string
		rows []rider.Frequency
		want []time.Time
	}{
		{"a run every headway, the end left out",
			[]rider.Frequency{{StartTime: hms(6, 0, 0), EndTime: hms(6, 30, 0), Headway: 10 * time.Minute}},
			[]time.Time{at(hms(6, 0, 0)), at(hms(6, 10, 0)), at(hms(6, 20, 0))}},
		{"a window that is not a whole number of headways",
			[]rider.Frequency{{StartTime: hms(6, 0, 0), EndTime: hms(6, 25, 0), Headway: 10 * time.Minute}},
			[]time.Time{at(hms(6, 0, 0)), at(hms(6, 10, 0)), at(hms(6, 20, 0))}},
		{"exact times are counted the same way",
			[]rider.Frequency{{StartTime: hms(9, 0, 0), EndTime: hms(10, 0, 0), Headway: 20 * time.Minute, ExactTimes: true}},
			[]time.Time{at(hms(9, 0, 0)), at(hms(9, 20, 0)), at(hms(9, 40, 0))}},
		{"two windows, in time order",
			[]rider.Frequency{
				{StartTime: hms(23, 30, 0), EndTime: hms(24, 30, 0), Headway: 30 * time.Minute},
				{StartTime: hms(6, 0, 0), EndTime: hms(6, 20, 0), Headway: 15 * time.Minute},
			},
			[]time.Time{at(hms(6, 0, 0)), at(hms(6, 15, 0)), at(hms(23, 30, 0)), at(hms(24, 0, 0))}},
		{"no headway is one run at the start",
			[]rider.Frequency{{StartTime: hms(6, 0, 0), EndTime: hms(7, 0, 0)}},
			[]time.Time{at(hms(6, 0, 0))}},
		{"a window that ends where it starts has none",
			[]rider.Frequency{{StartTime: hms(6, 0, 0), EndTime: hms(6, 0, 0), Headway: 10 * time.Minute}},
			[]time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scheduledRuns(tc.rows, dayStart))
		})
	}
}

func TestFillRuns(t *testing.T) {
	at := func(h, m int) time.Time { return reportDayStart.Add(hms(h, m, 0)) }
	slots := []time.Time{at(6, 0), at(6, 10), at(6, 20), at(6, 30)}
	tests := []struct {
		name   string
		starts []time.Time
		want   []bool
	}{
		{"none ran", nil, []bool{false, false, false, false}},
		{"each run fills the run nearest it", []time.Time{at(6, 1), at(6, 29)}, []bool{true, false, false, true}},
		{"a run whose nearest is taken fills the next nearest", []time.Time{at(6, 12), at(6, 9)}, []bool{false, true, true, false}},
		{"half way between two, the earlier", []time.Time{at(6, 5)}, []bool{true, false, false, false}},
		{"before the first and after the last", []time.Time{at(5, 40), at(7, 30)}, []bool{true, false, false, true}},
		{"more runs than the windows schedule fill them all once", []time.Time{at(6, 0), at(6, 1), at(6, 2), at(6, 3), at(6, 4)}, []bool{true, true, true, true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, fillRuns(slots, tc.starts))
		})
	}
}

// TestRunServiceDate_Frequencies: a run of a trip in frequencies.txt is dated
// by the window that holds its start, not by the pattern of its stop times.
func TestRunServiceDate_Frequencies(t *testing.T) {
	ix := headwayFixtureIndex(t)
	la := ix.Timezone()
	cases := []struct {
		name   string
		trip   string
		start  time.Time
		want   string
		wantOK bool
	}{
		{"in the morning window", "HW", time.Date(2026, 9, 2, 6, 1, 0, 0, la), "20260902", true},
		{"thirteen hours after the pattern, in the evening window", "HW", time.Date(2026, 9, 2, 19, 2, 0, 0, la), "20260902", true},
		{"after midnight, in the window that runs to 26:00", "HW", time.Date(2026, 9, 3, 0, 5, 0, 0, la), "20260902", true},
		{"exact times", "HX", time.Date(2026, 9, 2, 9, 21, 0, 0, la), "20260902", true},
		{"a weekday trip on a Saturday", "HW", time.Date(2026, 9, 5, 6, 1, 0, 0, la), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := runServiceDate(ix, mustTrip(t, ix, tc.trip), tc.start)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestServiceReporter_HeadwayTripsCountRuns: a trip in frequencies.txt counts
// the runs its windows schedule and the runs that ran, by headway or at exact
// times, and none of its stops is judged against its pattern's times.
func TestServiceReporter_HeadwayTripsCountRuns(t *testing.T) {
	rep, ix := headwayDay(t)
	dayStart := reportDayStart.In(ix.Timezone())

	r4 := reportRoute(t, rep, "R4")
	assert.Equal(t, 25+3, r4.TripsScheduled, "HW's 12, 8 and 5 runs and HX's 3")
	assert.Equal(t, 7+1, r4.TripsRun)
	assert.Equal(t, [4]int{0, 0, 0, 0}, [4]int{r4.StopsOnTime, r4.StopsEarly, r4.StopsLate, r4.StopsUnknown})
	assert.Nil(t, r4.OnTimePercent)
	assert.Nil(t, r4.UnknownPercent)

	hw := reportTrip(t, r4, "HW")
	assert.Len(t, hw.Runs, 7, "the next morning's run is the next day's")
	assert.Nil(t, hw.DepartureDeviationS)
	assert.Nil(t, hw.ArrivalDeviationS)
	assert.Equal(t, [4]int{0, 0, 0, 0}, [4]int{hw.StopsOnTime, hw.StopsEarly, hw.StopsLate, hw.StopsUnknown})
	assert.Nil(t, hw.OnTimePercent)
	assert.WithinDuration(t, reportDayStart.Add(hms(6, 0, 0)), hw.ScheduledDeparture, 0, "the first run's departure")
	assert.WithinDuration(t, reportDayStart.Add(hms(25, 40, 0)), hw.ScheduledArrival, 0, "the 25:30 run's arrival")
	assert.WithinDuration(t, reportDayStart.Add(hms(25, 40, 0)), rep.serviceEnds, 0, "the day is not settled before its last run ends")

	window := func(from, to time.Duration, every time.Duration, exact bool) frequencyWindow {
		return frequencyWindow{Start: dayStart.Add(from), End: dayStart.Add(to), HeadwayS: int(every / time.Second), ExactTimes: exact}
	}
	require.NotNil(t, hw.Frequencies)
	assert.Equal(t, []frequencyWindow{
		window(hms(6, 0, 0), hms(8, 0, 0), 10*time.Minute, false),
		window(hms(19, 0, 0), hms(21, 0, 0), 15*time.Minute, false),
		window(hms(23, 30, 0), hms(26, 0, 0), 30*time.Minute, false),
	}, hw.Frequencies.Windows)
	assert.Equal(t, [2]int{25, 7}, [2]int{hw.Frequencies.RunsScheduled, hw.Frequencies.RunsRun})

	hx := reportTrip(t, r4, "HX")
	require.Len(t, hx.Runs, 1)
	require.NotNil(t, hx.Frequencies)
	assert.Equal(t, []frequencyWindow{window(hms(9, 0, 0), hms(10, 0, 0), 20*time.Minute, true)}, hx.Frequencies.Windows)
	assert.Equal(t, [2]int{3, 1}, [2]int{hx.Frequencies.RunsScheduled, hx.Frequencies.RunsRun})
	assert.Equal(t, [4]int{0, 0, 0, 0}, [4]int{hx.StopsOnTime, hx.StopsEarly, hx.StopsLate, hx.StopsUnknown},
		"81 minutes after its pattern, but no timetable to be late against")
	assert.Nil(t, hx.DepartureDeviationS)
	assert.WithinDuration(t, reportDayStart.Add(hms(9, 0, 0)), hx.ScheduledDeparture, 0, "its first run's, not its pattern's 08:00")
	assert.WithinDuration(t, reportDayStart.Add(hms(9, 50, 0)), hx.ScheduledArrival, 0, "the 09:40 run's arrival")

	encoded, err := json.Marshal(hx.Frequencies)
	require.NoError(t, err)
	assert.JSONEq(t, `{"windows":[{"start":"2026-09-02T09:00:00-07:00","end":"2026-09-02T10:00:00-07:00","headway_s":1200,"exact_times":true}],"runs_scheduled":3,"runs_run":1}`, string(encoded))
}

// TestServiceReporter_HeadwayTripsLeaveTheTimetabledOnesAlone: the other
// routes of the day are reported exactly as they are without route 4.
func TestServiceReporter_HeadwayTripsLeaveTheTimetabledOnesAlone(t *testing.T) {
	rep, _ := headwayDay(t)
	plain, _, _ := newFixtureReporter(t, reportFixtureNow)
	want, err := plain.report(context.Background(), "2026-09-02")
	require.NoError(t, err)

	got := make([]routeReport, 0, len(rep.Routes))
	for _, route := range rep.Routes {
		if route.RouteID != "R4" {
			got = append(got, route)
		}
	}
	assert.Equal(t, want.Routes, got)
	for _, route := range got {
		for _, trip := range route.Trips {
			assert.Nil(t, trip.Frequencies, trip.GtfsTripID)
		}
	}
	encoded, err := json.Marshal(reportTrip(t, got[1], "T1"))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"frequencies":null`)

	gotTotals, wantTotals := newDayTotals(rep, testReportSettings), newDayTotals(want, testReportSettings)
	assert.Equal(t, wantTotals.Arrivals, gotTotals.Arrivals, "a run with no timetable reaches its last stop neither early nor late")
	assert.Equal(t, wantTotals.ArrivalsLeftOut, gotTotals.ArrivalsLeftOut)
}

// TestNewDayTotals_HeadwayRunsByHour: the kept totals count a headway trip's
// runs in the hour each is scheduled to start, and each run that ran in the
// hour of the run it filled.
func TestNewDayTotals_HeadwayRunsByHour(t *testing.T) {
	rep, _ := headwayDay(t)
	totals := newDayTotals(rep, testReportSettings)
	assert.Equal(t, dayTotalsVersion, totals.Version)
	for _, route := range totals.Routes {
		if route.RouteID != "R4" {
			continue
		}
		assert.Equal(t, []hourTotals{
			{Hour: 6, Scheduled: 6, Run: 3},
			{Hour: 7, Scheduled: 6, Run: 1},
			{Hour: 9, Scheduled: 3, Run: 1},
			{Hour: 19, Scheduled: 4, Run: 1},
			{Hour: 20, Scheduled: 4, Run: 1},
			{Hour: 23, Scheduled: 1},
			{Hour: 24, Scheduled: 2, Run: 1},
			{Hour: 25, Scheduled: 2},
		}, route.Hours)
		assert.Equal(t, reportCounts{Scheduled: 28, Run: 8}, route.counts(), "the hours add up to the route's row")
		return
	}
	t.Fatal("R4 is not in the totals")
}

// TestReportsPage_HeadwayRouteDay: a route's day lists a headway trip's runs
// under how often it runs, when, and how many of its runs ran, and not with
// the timetabled trips: it has no timetable.
func TestReportsPage_HeadwayRouteDay(t *testing.T) {
	r, _, _ := newHeadwayReporter(t)
	data, status := reportsView(t, r, "date=2026-09-02&route_id=R4")
	require.Equal(t, 200, status)

	assert.Empty(t, data["Trips"], "both of its trips run from frequencies.txt")
	assert.Equal(t, true, data["HasFrequencies"])
	runs := data["RouteRuns"].(*reportRouteRuns)
	require.Len(t, runs.Directions, 1)
	assert.Equal(t, []reportHeadwayTrip{
		{GtfsTripID: "HW", Scheduled: "every 10 min, 06:00 to 08:00, then every 15 min, 19:00 to 21:00, then every 30 min, 23:30 to 02:00 next day", RunCount: "7 of 25 runs"},
		{GtfsTripID: "HX", Scheduled: "every 20 min, 09:00 to 10:00", RunCount: "1 of 3 runs"},
	}, runs.Directions[0].Schedules)
	assert.Len(t, runs.Directions[0].Runs, 8, "HW's seven runs on the day and HX's one")

	tiles := data["Tiles"].(analyticsTiles)
	assert.Equal(t, [2]int{8, 28}, [2]int{tiles.Ran, tiles.Scheduled})

	html := renderReportsContent(t, data)
	assert.NotContains(t, html, "Scheduled trips", "no timetabled trip to list")
	assert.NotContains(t, html, "no timetable")
	assert.Contains(t, html, `<span style="display:block">HX runs every 20 min, 09:00 to 10:00: 1 of 3 runs</span>`)
	assert.Contains(t, html, "A trip run from the GTFS frequencies is not judged on time; each run a driver started fills the scheduled run nearest it.")
	assert.NotContains(t, html, "A timetabled trip is listed", "the route has none")

	timetabled, _ := reportsView(t, r, "date=2026-09-02&route_id=R1")
	assert.Nil(t, timetabled["HasFrequencies"])
	assert.Nil(t, timetabled["RouteRuns"])
	assert.NotContains(t, renderReportsContent(t, timetabled), "A trip run from the GTFS frequencies")
}

func TestFrequencyWords(t *testing.T) {
	at := func(d time.Duration) time.Time { return reportDayStart.Add(d) }
	loc := reportFixtureIndex(t).Timezone()
	tests := []struct {
		name            string
		f               tripFrequencies
		scheduled, runs string
	}{
		{"a window", tripFrequencies{
			Windows:       []frequencyWindow{{Start: at(hms(6, 0, 0)), End: at(hms(22, 0, 0)), HeadwayS: 1200}},
			RunsScheduled: 48, RunsRun: 41,
		}, "every 20 min, 06:00 to 22:00", "41 of 48 runs"},
		{"one run", tripFrequencies{
			Windows:       []frequencyWindow{{Start: at(hms(6, 0, 0)), End: at(hms(6, 1, 0)), HeadwayS: 3600}},
			RunsScheduled: 1,
		}, "every 1 h, 06:00 to 06:01", "0 of 1 run"},
		{"a window with no headway", tripFrequencies{
			Windows:       []frequencyWindow{{Start: at(hms(6, 0, 0)), End: at(hms(7, 0, 0))}, {Start: at(hms(23, 0, 0)), End: at(hms(25, 0, 0)), HeadwayS: 900}},
			RunsScheduled: 9, RunsRun: 9,
		}, "once at 06:00, then every 15 min, 23:00 to 01:00 next day", "9 of 9 runs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheduled, runs := frequencyWords(&tc.f, loc, reportDay)
			assert.Equal(t, tc.scheduled, scheduled)
			assert.Equal(t, tc.runs, runs)
		})
	}
}

// TestOverview_CountsHeadwayRunsDueSoFar: today's trips scheduled so far
// counts each of a headway trip's runs due by now, not the trip once.
func TestOverview_CountsHeadwayRunsDueSoFar(t *testing.T) {
	rep, ix := headwayDay(t)
	v := newOverviewView(overviewInput{now: reportDayStart.Add(hms(7, 0, 0)), ix: ix, today: rep})
	assert.Equal(t, 7, v.TripsScheduled, "HW's runs from 06:00 to 07:00; no timetabled trip leaves before 08:00")
}

// TestReportsPage_KeptDaysOfAnOlderShapeAreWorkedOutAgain: a day kept before
// the totals counted headway trips by run says nothing of its version, and is
// worked out again and replaced, as a day judged by another on-time window is.
func TestReportsPage_KeptDaysOfAnOlderShapeAreWorkedOutAgain(t *testing.T) {
	r, store, _ := newHeadwayReporter(t)
	reportsView(t, r, "date=2026-09-02&range=day")
	kept := store.days["2026-09-02"]
	require.Equal(t, dayTotalsVersion, kept.Totals.Version)
	want := kept.Totals

	old := kept
	old.Totals.Version = 0
	old.Totals.Routes = []routeDayTotals{{RouteID: "R4", Hours: []hourTotals{{Hour: 6, Scheduled: 2, Run: 1, Late: 3}}}}
	store.days["2026-09-02"] = old
	reads, saves := store.reads, store.saves

	again := newServiceReporter(store, r.index, testReportSettings)
	again.now = r.now
	days, err := again.storedDays(context.Background(), reportDay, reportDay)
	require.NoError(t, err)
	assert.Empty(t, days, "an older version is not read as kept")

	reportsView(t, again, "date=2026-09-02&range=day")
	assert.Equal(t, reads+1, store.reads, "worked out again")
	assert.Equal(t, saves+1, store.saves, "and kept again")
	assert.Equal(t, want, store.days["2026-09-02"].Totals)
}
