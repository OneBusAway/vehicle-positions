package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestOverviewHeadline(t *testing.T) {
	assert.Equal(t, "No vehicles are on a trip", overviewHeadline(0, 0))
	assert.Equal(t, "1 of 1 vehicle is reporting", overviewHeadline(1, 1))
	assert.Equal(t, "0 of 1 vehicle is reporting", overviewHeadline(0, 1))
	assert.Equal(t, "9 of 11 vehicles are reporting", overviewHeadline(9, 11))
}

// stopsReport is a report with one route whose stops were judged so.
func stopsReport(onTime, early, late int) *serviceReport {
	return &serviceReport{Routes: []routeReport{{StopsOnTime: onTime, StopsEarly: early, StopsLate: late}}}
}

func TestOnTimeShares(t *testing.T) {
	assert.Equal(t, onTimeSplit{}, onTimeShares(stopsReport(0, 0, 0)), "nothing judged says nothing")
	assert.Equal(t, onTimeSplit{Known: true, Percent: 86, Early: 3, Late: 11}, onTimeShares(stopsReport(86, 3, 11)))

	assert.Equal(t, onTimeSplit{Known: true, Percent: 33, Early: 33, Late: 33}, onTimeShares(stopsReport(1, 1, 1)), "each share is rounded on its own")

	two := &serviceReport{Routes: []routeReport{{StopsOnTime: 3, StopsLate: 1}, {StopsOnTime: 5, StopsEarly: 1}}}
	assert.Equal(t, onTimeSplit{Known: true, Percent: 80, Early: 10, Late: 10}, onTimeShares(two), "summed over routes")
}

func TestOverviewLede(t *testing.T) {
	today := onTimeSplit{Known: true, Percent: 86}
	for _, tc := range []struct {
		name        string
		hasSchedule bool
		today       onTimeSplit
		yesterday   *serviceReport
		want        string
	}{
		{"no schedule", false, onTimeSplit{}, nil, "No schedule is loaded, so nothing is judged against a timetable."},
		{"nothing judged", true, onTimeSplit{}, stopsReport(9, 0, 1), "No stop has been passed yet today."},
		{"no yesterday", true, today, nil, "86% of the stops passed today were on time."},
		{"yesterday not judged", true, today, stopsReport(0, 0, 0), "86% of the stops passed today were on time."},
		{"up", true, today, stopsReport(83, 0, 17), "86% of the stops passed today were on time, 3 points up on yesterday."},
		{"up one", true, today, stopsReport(85, 0, 15), "86% of the stops passed today were on time, 1 point up on yesterday."},
		{"down", true, today, stopsReport(90, 0, 10), "86% of the stops passed today were on time, 4 points down on yesterday."},
		{"down one", true, today, stopsReport(87, 0, 13), "86% of the stops passed today were on time, 1 point down on yesterday."},
		{"same", true, today, stopsReport(86, 0, 14), "86% of the stops passed today were on time, the same as yesterday."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, overviewLede(tc.hasSchedule, tc.today, tc.yesterday))
		})
	}
}

func TestNewWelcomeView(t *testing.T) {
	none := newWelcomeView(0, 0)
	require.Len(t, none.Steps, 4)
	assert.Equal(t, stepLine{N: 1, Of: 4}, none.Line)
	assert.Equal(t, "Add vehicles", none.Steps[0].Action)
	assert.False(t, none.Steps[0].Done)
	assert.Empty(t, none.Steps[1].Action, "one step at a time")

	vehicles := newWelcomeView(3, 0)
	assert.Equal(t, stepLine{N: 2, Of: 4}, vehicles.Line)
	assert.True(t, vehicles.Steps[0].Done)
	assert.Equal(t, "3 vehicles added", vehicles.Steps[0].Text)
	assert.Empty(t, vehicles.Steps[0].Action)
	assert.Equal(t, "Add drivers", vehicles.Steps[1].Action)

	drivers := newWelcomeView(0, 1)
	assert.Equal(t, stepLine{N: 1, Of: 4}, drivers.Line, "vehicles come first")
	assert.Equal(t, "Add vehicles", drivers.Steps[0].Action)
	assert.True(t, drivers.Steps[1].Done)
	assert.Equal(t, "1 driver added", drivers.Steps[1].Text)
}

func TestStepLine(t *testing.T) {
	line := stepLine{N: 2, Of: 4}
	assert.Equal(t, []stepStop{{Left: "0.0", Done: true}, {Left: "33.3"}, {Left: "66.7"}, {Left: "100.0"}}, line.Stops())
	assert.Equal(t, "33.3", line.VehicleLeft())
	assert.Equal(t, "66.7", line.FillRight())

	last := stepLine{N: 3, Of: 3}
	assert.Equal(t, "100.0", last.VehicleLeft())
	assert.Equal(t, "0.0", last.FillRight())
}

// overviewTestInput is two vehicles on T1 and one on a route the schedule
// does not have: bus-1 a quarter of the way along and reporting, bus-2 quiet
// and gone from the tracker, van-9 reporting on a trip with no GTFS trip.
func overviewTestInput(t *testing.T, ix *rider.Index) overviewInput {
	t.Helper()
	t1 := mustTrip(t, ix, "T1")
	quarter := t1.Shape.Length / 4
	now := reportFixtureNow
	quietTrip := alertTrip("bus-2", now.Add(-time.Hour))
	quietTrip.VehicleLabel, quietTrip.RouteID, quietTrip.GtfsTripID = "Bus 2", "R1", "T1"
	stillTrip := alertTrip("bus-1", now.Add(-time.Hour))
	stillTrip.ID, stillTrip.VehicleLabel, stillTrip.RouteID, stillTrip.GtfsTripID = 8, "Bus 1", "R1", "T1"
	return overviewInput{
		now:      now,
		ix:       ix,
		vehicles: 14,
		trips: map[string]ActiveTripInfo{
			"bus-1": {TripID: 8, RouteID: "R1", GtfsTripID: "T1"},
			"bus-2": {TripID: 7, RouteID: "R1", GtfsTripID: "T1"},
			"van-9": {TripID: 9, RouteID: "X9"},
		},
		tracked: []*VehicleState{
			{VehicleID: "bus-1", VehicleLabel: "Bus 1", TripID: "T1", alongShape: &quarter},
			{VehicleID: "van-9", VehicleLabel: "Van 9"},
		},
		alerts: []alertEntry{
			{Kind: alertNotMoving, Since: now.Add(-12 * time.Minute), DurationSeconds: 720, Trip: stillTrip},
			{Kind: alertQuiet, Since: now.Add(-6 * time.Minute), DurationSeconds: 360, Trip: quietTrip},
		},
		settings: testAlertSettings,
		today:    &serviceReport{Date: "2026-09-02", Routes: []routeReport{{RouteID: "R1", TripsRun: 2, StopsOnTime: 86, StopsEarly: 3, StopsLate: 11, OnTimePercent: ptrFloat(87.6), Trips: []tripReport{{ScheduledDeparture: now.Add(-time.Hour)}, {ScheduledDeparture: now}, {ScheduledDeparture: now.Add(time.Minute)}}}}},
		health:   feedHealthResponse{},
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestNewOverviewView(t *testing.T) {
	ix := reportFixtureIndex(t)
	v := newOverviewView(overviewTestInput(t, ix))

	assert.Equal(t, "2 of 3 vehicles are reporting", v.Headline, "a quiet vehicle is on a trip and not reporting")
	assert.Equal(t, 3, v.OnTrip)
	assert.Equal(t, 14, v.Vehicles)
	assert.Equal(t, 2, v.Reporting)
	assert.Equal(t, 1, v.Quiet)
	assert.Equal(t, []string{"seg-ok", "seg-ok", "seg-warn"}, v.Units)
	assert.Equal(t, "America/Los_Angeles", v.Timezone)

	assert.Equal(t, onTimeSplit{Known: true, Percent: 86, Early: 3, Late: 11}, v.OnTime)
	assert.Equal(t, "/admin/reports?date=2026-09-02&range=day", v.ReportsURL)
	assert.Equal(t, 2, v.TripsRun)
	assert.Equal(t, 2, v.TripsScheduled, "the trip due in a minute is not scheduled so far; the one due now is")
	assert.True(t, v.HasSchedule)

	assert.Equal(t, 2, v.AlertCount)
	assert.Equal(t, "12 min", v.LongestOpen)
	require.NotNil(t, v.Problem)
	assert.Equal(t, overviewProblem{Text: "1 vehicle has gone quiet", URL: "/admin/map", Action: "Show on map"}, *v.Problem)

	require.Len(t, v.Attention, 2)
	assert.Equal(t, overviewAttention{Icon: "stop_circle", Title: "Bus 1: not moving", Detail: "Route 1, under 50 m in 12 min", URL: "/admin/trips/8"}, v.Attention[0])
	assert.Equal(t, "Bus 2: gone quiet", v.Attention[1].Title)

	require.Len(t, v.Routes, 2)
	r1 := v.Routes[0]
	assert.Equal(t, "Straight", r1.Name)
	assert.Equal(t, "#0077C0", r1.Route.Color)
	assert.Equal(t, "/admin/reports?date=2026-09-02&route_id=R1", r1.URL)
	assert.Equal(t, 2, r1.OnTrip)
	assert.Equal(t, 1, r1.Reporting)
	assert.Equal(t, "88%", r1.OnTime)
	assert.Equal(t, []string{"0.0", "50.0", "100.0"}, r1.Stops, "T1's three stops along its shape")
	assert.Equal(t, []overviewVehicle{{ID: "bus-1", Label: "Bus 1", Left: "25.0"}}, r1.Vehicles, "bus-2 is gone from the tracker, so it is counted and not drawn")

	x9 := v.Routes[1]
	assert.Equal(t, "X9", x9.Name, "a route the schedule does not have goes by its id, after the ones it has")
	assert.Equal(t, 1, x9.Reporting)
	assert.Empty(t, x9.Stops)
	assert.Empty(t, x9.Vehicles, "nothing to place it along")
	assert.Empty(t, x9.OnTime)
}

func TestNewOverviewView_NothingWrong(t *testing.T) {
	ix := reportFixtureIndex(t)
	in := overviewTestInput(t, ix)
	in.alerts = nil
	in.tracked = append(in.tracked, &VehicleState{VehicleID: "bus-2", TripID: "T1"})

	v := newOverviewView(in)

	assert.Equal(t, "3 of 3 vehicles are reporting", v.Headline)
	assert.Nil(t, v.Problem)
	assert.Empty(t, v.LongestOpen)
	assert.Empty(t, v.Attention)
	assert.Equal(t, []string{"seg-ok", "seg-ok", "seg-ok"}, v.Units)
	assert.Len(t, v.Routes[0].Vehicles, 1, "bus-2 has reported but has not been placed along the trip yet")
}

func TestNewOverviewView_AlertsButNoneQuiet(t *testing.T) {
	ix := reportFixtureIndex(t)
	in := overviewTestInput(t, ix)
	in.alerts = in.alerts[:1]

	v := newOverviewView(in)

	require.NotNil(t, v.Problem)
	assert.Equal(t, overviewProblem{Text: "1 alert is open", URL: "/admin/alerts", Action: "See alerts"}, *v.Problem)
}

func TestNewOverviewView_NoSchedule(t *testing.T) {
	in := overviewTestInput(t, reportFixtureIndex(t))
	in.ix, in.today = nil, nil

	v := newOverviewView(in)

	assert.False(t, v.HasSchedule)
	assert.Equal(t, "UTC", v.Timezone)
	assert.False(t, v.OnTime.Known)
	assert.Equal(t, "No schedule is loaded, so nothing is judged against a timetable.", v.Lede)
	assert.Equal(t, "/admin/reports", v.ReportsURL)
	require.Len(t, v.Routes, 2)
	assert.Equal(t, "R1", v.Routes[0].Name, "by id, in order")
	assert.Equal(t, "/admin/trips?page=1&status=active", v.Routes[0].URL)
	assert.Empty(t, v.Routes[0].Vehicles)
	assert.Equal(t, "Not loaded", v.System[1].State)
	assert.Equal(t, "warn", v.System[1].Tone)
}

// TestNewOverviewView_ManyVehicles: past overviewMaxUnits the first tile
// draws the two counts in proportion, not a mark for each vehicle.
func TestNewOverviewView_ManyVehicles(t *testing.T) {
	in := overviewInput{now: reportFixtureNow, trips: map[string]ActiveTripInfo{}}
	for i := 0; i <= overviewMaxUnits; i++ {
		in.trips["bus-"+string(rune('a'+i))] = ActiveTripInfo{RouteID: "R1"}
	}

	assert.Empty(t, newOverviewView(in).Units)

	delete(in.trips, "bus-a")
	assert.Len(t, newOverviewView(in).Units, overviewMaxUnits)
}

func TestLineStops(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	assert.Equal(t, []string{"0.0", "50.0", "100.0"}, lineStops(t1))

	back := *t1
	back.DirectionID = 1
	assert.Equal(t, []string{"100.0", "50.0", "0.0"}, lineStops(&back), "the second direction is drawn right to left")

	long := *t1
	long.StopTimes = nil
	for i := 0; i < 100; i++ {
		long.StopTimes = append(long.StopTimes, rider.StopTimeInfo{AlongShape: t1.Shape.Length * float64(i) / 99})
	}
	stops := lineStops(&long)
	assert.LessOrEqual(t, len(stops), overviewMaxStops+1)
	assert.Equal(t, "0.0", stops[0])
	assert.Equal(t, "100.0", stops[len(stops)-1], "the last stop is always drawn")
}

func TestOverviewSystem(t *testing.T) {
	ix := reportFixtureIndex(t)
	now := reportFixtureNow
	minute := now.Truncate(time.Minute)
	in := overviewInput{now: now, ix: ix}

	idle := overviewSystem(in, ix.Timezone())
	require.Len(t, idle, 3)
	assert.Equal(t, overviewSystemRow{Icon: "sensors", Name: "Feed", Detail: "No fetches in the last hour", State: "Idle", URL: "/admin/health"}, idle[0])
	assert.Equal(t, "OK", idle[1].State)
	assert.Contains(t, idle[1].Detail, "routes, ")
	assert.Equal(t, overviewSystemRow{Icon: "hail", Name: "Rider mode", Detail: "Riders cannot share where they are", State: "Off", URL: "/admin/rider"}, idle[2])

	in.health = feedHealthResponse{
		LastHour: healthPeriod{Feed: feedOutcomes{Served: 412}},
		Minutes:  []healthMinute{{Minute: minute.Add(-time.Minute), Feed: feedOutcomes{Served: 7}}, {Minute: minute}},
	}
	in.rider = riderStatusResponse{Enabled: true, Riders: &riderTierCounts{Total: 6}, Rides: &riderRideCounts{Active: 1}}
	busy := overviewSystem(in, ix.Timezone())
	assert.Equal(t, "412 fetches served in the last hour, last at "+minute.Add(-time.Minute).In(ix.Timezone()).Format("15:04"), busy[0].Detail)
	assert.Equal(t, "OK", busy[0].State)
	assert.Equal(t, "ok", busy[0].Tone)
	assert.Equal(t, "6 riders, 1 ride in progress", busy[2].Detail)
	assert.Equal(t, "On", busy[2].State)

	in.health.LastHour.Feed.ServerError = 1
	assert.Equal(t, "Errors", overviewSystem(in, ix.Timezone())[0].State)
	assert.Equal(t, "warn", overviewSystem(in, ix.Timezone())[0].Tone)

	in.rider = riderStatusResponse{Enabled: true, Riders: &riderTierCounts{}, Rides: &riderRideCounts{}}
	assert.Equal(t, "No rider has signed up yet", overviewSystem(in, ix.Timezone())[2].Detail)
}

// TestReportCache: today's report is worked out once a minute, however often
// the Overview asks, and again for another day or a reloaded schedule.
func TestReportCache(t *testing.T) {
	ix := reportFixtureIndex(t)
	trips, trails := reportFixtureTrips(t, ix)
	store := &fakeReportStore{trips: trips, trails: trails}
	index := ix
	// Noon on the fixture day: its service is not over, so the reporter
	// keeps nothing itself.
	now := reportDayStart.Add(12 * time.Hour)
	r := newServiceReporter(store, func() *rider.Index { return index }, testReportSettings)
	r.now = func() time.Time { return now }
	var cache reportCache
	get := func(date string) *serviceReport {
		rep, err := cache.get(context.Background(), r, index, date)
		require.NoError(t, err)
		return rep
	}

	first := get("2026-09-02")
	require.Equal(t, 1, store.reads)
	assert.Same(t, first, get("2026-09-02"))
	assert.Equal(t, 1, store.reads, "asked again within the minute, read once")

	now = now.Add(overviewReportTTL - time.Second)
	get("2026-09-02")
	assert.Equal(t, 1, store.reads)

	now = now.Add(time.Second)
	get("2026-09-02")
	assert.Equal(t, 2, store.reads, "a minute on, read again")

	get("2026-09-01")
	assert.Equal(t, 3, store.reads, "another day")

	index = refreshedReportFixtureIndex(t)
	get("2026-09-01")
	assert.Equal(t, 4, store.reads, "the schedule was loaded again")

	// Today again: the reporter keeps a day whose service is over itself, so
	// only today's read reaches the store every time.
	store.err = errors.New("db down")
	_, err := cache.get(context.Background(), r, index, "2026-09-02")
	require.Error(t, err)
	store.err = nil
	get("2026-09-02")
	assert.Equal(t, 6, store.reads, "a failed read is not kept")
}

func newOverviewMux(t *testing.T, vehicles, drivers int) (*adminUI, *http.ServeMux) {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.stats = &fakeAdminStats{vehicles: vehicles, drivers: drivers}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return ui, mux
}

func TestDashboardPage_WelcomeUntilThereIsAVehicleAndADriver(t *testing.T) {
	for _, tc := range []struct {
		vehicles, drivers int
		step              string
	}{
		{0, 0, "Step 1 of 4"},
		{2, 0, "Step 2 of 4"},
		{0, 2, "Step 1 of 4"},
	} {
		_, mux := newOverviewMux(t, tc.vehicles, tc.drivers)
		body := getAdminPage(t, mux, "/admin/dashboard")
		assert.Contains(t, body, "<title>Welcome · Vehicle Tracker</title>")
		assert.Contains(t, body, "Get your first vehicle on the map")
		assert.Contains(t, body, `aria-label="`+tc.step+`"`)
		assert.Contains(t, body, `<a class="btn" href="/admin/map">Skip for now</a>`)
		assert.NotContains(t, body, "line.js", "nothing on the welcome is live")
		assert.Equal(t, railOpen, railLink(body, "/admin/dashboard"))
	}

	_, mux := newOverviewMux(t, 2, 0)
	body := getAdminPage(t, mux, "/admin/dashboard")
	assert.Contains(t, body, `<a class="step done" href="/admin/vehicles">`)
	assert.Contains(t, body, "2 vehicles added")
	assert.Contains(t, body, `<a class="step current" href="/admin/users">`)
	assert.Contains(t, body, `<span class="btn btn-primary">Add drivers</span>`)

	_, mux = newOverviewMux(t, 1, 1)
	body = getAdminPage(t, mux, "/admin/dashboard")
	assert.Contains(t, body, "<title>Overview · Vehicle Tracker</title>")
	assert.NotContains(t, body, "Get your first vehicle on the map")
}

// TestDashboardPage_Overview drives the page over the report fixture's day
// with one vehicle placed on T1 by the tracker, as a driver's report places
// it.
func TestDashboardPage_Overview(t *testing.T) {
	// Noon on the fixture day, so that day is today.
	r, _, ix := newFixtureReporter(t, reportDayStart.Add(12*time.Hour))
	ui, mux := newOverviewMux(t, 14, 5)
	ui.reports = r
	ui.tracker.SetTripLocator(newTripLocator(func() *rider.Index { return ix }, testReportSettings.maxShapeDistance))
	ui.activeTrips = &fakeActiveTrips{m: map[string]ActiveTripInfo{"bus-1": {TripID: 8, RouteID: "R1", GtfsTripID: "T1", DriverName: "Achieng"}}}
	t1 := mustTrip(t, ix, "T1")
	mid := t1.Shape.PointAt(t1.Shape.Length / 2)
	ui.tracker.MarkTripStarted("bus-1")
	require.True(t, ui.tracker.UpdateAuthorized(&LocationReport{VehicleID: "bus-1", VehicleLabel: "Bus <1>", TripID: "T1", RouteID: "R1", Latitude: mid.Lat, Longitude: mid.Lon, Timestamp: time.Now().Unix()}, time.Now()))

	body := getAdminPage(t, mux, "/admin/dashboard")

	assert.Contains(t, body, `<h1 class="topbar-title">Overview</h1>`)
	assert.Contains(t, body, `<h2 class="h1" data-live="headline">1 of 1 vehicle is reporting</h2>`)
	assert.Contains(t, body, "America/Los_Angeles time")
	assert.Contains(t, body, `<span class="count-up" data-count="1" data-live="on-trip">1</span><span class="tile-unit" data-live="vehicles">of 14 vehicles</span>`)
	assert.Contains(t, body, `<a class="card tile" href="/admin/reports?date=2026-09-02&amp;range=day">`)
	assert.Contains(t, body, `<a class="line-row" href="/admin/reports?date=2026-09-02&amp;route_id=R1" data-route="R1">`)
	assert.Contains(t, body, `<span class="veh" style="left:50.0%" data-veh="bus-1" title="Bus &lt;1&gt;">`, "the vehicle halfway along its route, its label escaped")
	assert.Equal(t, 3, strings.Count(body, `<span class="stop" style="left:`), "T1's stops")
	assert.Contains(t, body, "Nothing needs a look.")
	assert.Contains(t, body, `<script src="/static/js/line.js" defer></script>`)
	assert.Contains(t, body, `role="img" aria-label="Location reports a minute over the last hour: 60 bars, the highest 0"`)
	assert.NotContains(t, body, "Bus <1>")
}

func TestDashboardPage_Errors(t *testing.T) {
	t.Run("the active trips", func(t *testing.T) {
		ui, mux := newOverviewMux(t, 1, 1)
		ui.activeTrips = &errActiveTrips{}
		assert.Equal(t, http.StatusInternalServerError, requestAdminPage(t, mux, "/admin/dashboard").Code)
	})
	t.Run("the alerts", func(t *testing.T) {
		ui, mux := newOverviewMux(t, 1, 1)
		ui.alerts = newAlertWatcher(&fakeTripLister{err: errors.New("database down")}, testAlertSettings)
		w := requestAdminPage(t, mux, "/admin/dashboard")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NotContains(t, w.Body.String(), "database down")
	})
	t.Run("the report", func(t *testing.T) {
		r, store, _ := newFixtureReporter(t, reportFixtureNow)
		store.err = errors.New("database down")
		ui, mux := newOverviewMux(t, 1, 1)
		ui.reports = r
		assert.Equal(t, http.StatusInternalServerError, requestAdminPage(t, mux, "/admin/dashboard").Code)
	})
	t.Run("rider mode", func(t *testing.T) {
		ui, mux := newOverviewMux(t, 1, 1)
		ui.rider = fakeRiderPageSource{err: errors.New("database down")}
		assert.Equal(t, http.StatusInternalServerError, requestAdminPage(t, mux, "/admin/dashboard").Code)
	})
}
