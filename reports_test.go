package main

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// reportFixtureNow is two days after the fixture day, when its service is
// long over.
var reportFixtureNow = time.Date(2026, 9, 4, 19, 0, 0, 0, time.UTC)

func TestRunServiceDate(t *testing.T) {
	ix := reportFixtureIndex(t)
	la := ix.Timezone()
	cases := []struct {
		name   string
		trip   string
		start  time.Time
		want   string
		wantOK bool
	}{
		{"a morning run on its own day", "T1", time.Date(2026, 9, 2, 7, 57, 0, 0, la), "20260902", true},
		{"24:10 is the day before's run", "RM", time.Date(2026, 9, 3, 0, 7, 0, 0, la), "20260902", true},
		{"25:00 is the day before's run", "T3", time.Date(2026, 9, 3, 0, 58, 0, 0, la), "20260902", true},
		{"started just before midnight for the next morning's run", "T1", time.Date(2026, 9, 1, 23, 58, 0, 0, la), "20260902", true},
		{"a Saturday trip on a Wednesday", "T2", time.Date(2026, 9, 2, 9, 0, 0, 0, la), "", false},
		{"T1 on Labor Day, when calendar_dates removes it, is not Tuesday's run", "T1", time.Date(2026, 9, 7, 8, 0, 0, 0, la), "", false},
		{"between two runs, the nearer one", "T3", time.Date(2026, 9, 2, 13, 0, 0, 0, la), "20260901", true},
		{"exactly twelve hours before a run counts", "T1", time.Date(2026, 9, 7, 20, 0, 0, 0, la), "20260908", true},
		{"a second more does not", "T1", time.Date(2026, 9, 7, 19, 59, 59, 0, la), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := runServiceDate(ix, mustTrip(t, ix, tc.trip), tc.start)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestServiceReporter_FixtureDay(t *testing.T) {
	r, _, index := newFixtureReporter(t, reportFixtureNow)

	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	assert.Equal(t, "2026-09-02", rep.Date)
	assert.Equal(t, "America/Los_Angeles", rep.Timezone)
	assert.Equal(t, 60, rep.OnTimeEarlyS)
	assert.Equal(t, 300, rep.OnTimeLateS)

	pct := func(p float64) *float64 { return &p }
	// route_sort_order puts R2 first.
	require.Len(t, rep.Routes, 3)
	r2, r1, r3 := rep.Routes[0], rep.Routes[1], rep.Routes[2]
	assert.Equal(t, []string{"R2", "R1", "R3"}, []string{r2.RouteID, r1.RouteID, r3.RouteID})

	// R1: T1, RE, RL, RG and RM ran; RN did not, and neither did T4. T1's run
	// the next morning, and the trips of T2 and of a trip the schedule no
	// longer has, are not this day's.
	assert.Equal(t, 7, r1.TripsScheduled)
	assert.Equal(t, 5, r1.TripsRun)
	assert.Equal(t, [4]int{9, 3, 2, 1}, [4]int{r1.StopsOnTime, r1.StopsEarly, r1.StopsLate, r1.StopsUnknown})
	assert.Equal(t, pct(64.3), r1.OnTimePercent, "9 of the 14 known stops")
	assert.Equal(t, pct(6.7), r1.UnknownPercent, "1 of the 15 stops")
	assert.Equal(t, "1", r1.ShortName)
	assert.Equal(t, "Straight", r1.LongName)

	assert.Equal(t, [2]int{1, 1}, [2]int{r2.TripsScheduled, r2.TripsRun})
	assert.Equal(t, [4]int{4, 0, 0, 0}, [4]int{r2.StopsOnTime, r2.StopsEarly, r2.StopsLate, r2.StopsUnknown})
	assert.Equal(t, pct(100), r2.OnTimePercent)
	assert.Equal(t, pct(0), r2.UnknownPercent)
	assert.Equal(t, [2]int{1, 1}, [2]int{r3.TripsScheduled, r3.TripsRun})
	assert.Equal(t, 5, r3.StopsOnTime)

	ids := make([]string, 0, len(r1.Trips))
	for _, tr := range r1.Trips {
		ids = append(ids, tr.GtfsTripID)
	}
	assert.Equal(t, []string{"T1", "RE", "T4", "RL", "RN", "RG", "RM"}, ids, "first departure order")

	t1 := r1.Trips[0]
	require.Len(t, t1.Runs, 1, "T1 the next morning is the next day's run")
	assert.Equal(t, tripRun{ID: 1, VehicleID: "bus-T1", VehicleLabel: "Bus T1", DriverName: "Driver T1"}, t1.Runs[0])
	assert.WithinDuration(t, reportDayStart.Add(hms(8, 0, 0)), t1.ScheduledDeparture, 0)
	assert.WithinDuration(t, reportDayStart.Add(hms(8, 10, 0)), t1.ScheduledArrival, 0)
	assert.Equal(t, "North", t1.Headsign)
	require.NotNil(t, t1.DepartureDeviationS)
	require.NotNil(t, t1.ArrivalDeviationS)
	assert.Equal(t, 6, *t1.DepartureDeviationS)
	assert.Equal(t, -16, *t1.ArrivalDeviationS)
	assert.Equal(t, pct(100), t1.OnTimePercent)

	rl := r1.Trips[3]
	assert.Equal(t, [4]int{1, 0, 2, 0}, [4]int{rl.StopsOnTime, rl.StopsEarly, rl.StopsLate, rl.StopsUnknown})
	assert.Equal(t, pct(33.3), rl.OnTimePercent)

	rn := r1.Trips[4]
	assert.NotNil(t, rn.Runs, "a trip not run has no runs, not null ones")
	assert.Empty(t, rn.Runs)
	assert.Nil(t, rn.DepartureDeviationS)
	assert.Nil(t, rn.OnTimePercent)
	assert.Nil(t, rn.UnknownPercent)

	rm := r1.Trips[6]
	require.Len(t, rm.Runs, 1)
	assert.Equal(t, int64(5), rm.Runs[0].ID, "RM ran after midnight, on the calendar day after")
	assert.WithinDuration(t, time.Date(2026, 9, 3, 0, 10, 0, 0, index.Timezone()), rm.ScheduledDeparture, 0)
}

// TestServiceReporter_CalendarDates: on Labor Day calendar_dates takes the
// weekday service off and runs the Saturday one, so the only trip scheduled
// is T2, and no route without service that day is listed.
func TestServiceReporter_CalendarDates(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow.AddDate(0, 0, 10))

	rep, err := r.report(context.Background(), "2026-09-07")
	require.NoError(t, err)
	require.Len(t, rep.Routes, 1)
	assert.Equal(t, "R1", rep.Routes[0].RouteID)
	assert.Equal(t, 1, rep.Routes[0].TripsScheduled)
	assert.Equal(t, 0, rep.Routes[0].TripsRun)
	assert.Equal(t, "T2", rep.Routes[0].Trips[0].GtfsTripID)
	assert.Nil(t, rep.Routes[0].OnTimePercent)
	assert.Nil(t, rep.Routes[0].UnknownPercent)
}

// TestServiceReporter_KeepsTheRunsOfOneTripApart: two driver trips of T1 on
// the day, one vehicle stopping 400 m along and another starting 600 m along
// just after. The report lists both runs and does not make up a time at ST2,
// which neither was seen passing.
func TestServiceReporter_KeepsTheRunsOfOneTripApart(t *testing.T) {
	ix := reportFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	run := func(id int64, legs []reportLeg) (TripSummary, []LocationPoint) {
		return TripSummary{
			ID: id, VehicleID: "bus-" + strconv.FormatInt(id, 10), RouteID: t1.RouteID, GtfsTripID: "T1",
			StartTime: reportDayStart.Add(legs[0].at), Status: "completed",
		}, driveTrip(t1, reportDayStart, legs, nil)
	}
	first, firstTrail := run(1, []reportLeg{{hms(7, 57, 0), 0}, {hms(8, 0, 0), 0}, {hms(8, 1, 20), 400}})
	second, secondTrail := run(2, []reportLeg{{hms(8, 1, 30), 600}, {hms(8, 2, 50), t1.Shape.Length}, {hms(8, 5, 0), t1.Shape.Length}})
	store := &fakeReportStore{
		trips:  []TripSummary{first, second},
		trails: map[int64][]LocationPoint{1: firstTrail, 2: secondTrail},
	}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }

	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	for _, route := range rep.Routes {
		for _, trip := range route.Trips {
			if trip.GtfsTripID != "T1" {
				continue
			}
			assert.Len(t, trip.Runs, 2)
			assert.Equal(t, [4]int{1, 1, 0, 1}, [4]int{trip.StopsOnTime, trip.StopsEarly, trip.StopsLate, trip.StopsUnknown})
			return
		}
	}
	t.Fatal("T1 is not in the report")
}

func TestServiceReporter_EmptyDay(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow.AddDate(0, 0, 10))

	// A Sunday: neither the weekday nor the Saturday service runs.
	rep, err := r.report(context.Background(), "2026-09-06")
	require.NoError(t, err)
	assert.NotNil(t, rep.Routes, "no routes is an empty list, not null")
	assert.Empty(t, rep.Routes)
}

func TestServiceReporter_DefaultsToYesterdayInTheAgencyTimezone(t *testing.T) {
	// 01:30 on 3 September in Los Angeles is already 4 September in UTC.
	now := time.Date(2026, 9, 3, 8, 30, 0, 0, time.UTC)
	r, _, _ := newFixtureReporter(t, now)

	rep, err := r.report(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "2026-09-02", rep.Date)
}

func TestServiceReporter_Errors(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	for _, bad := range []string{"2026-9-2", "20260902", "2026-02-30", "yesterday", "0000-12-31", "9999-01-01"} {
		_, err := r.report(context.Background(), bad)
		assert.ErrorIs(t, err, errBadReportDate, bad)
	}
	// The first and last days whose neighbours a GTFS date can still hold.
	for _, edge := range []string{"0001-01-01", "9998-12-31"} {
		_, err := r.report(context.Background(), edge)
		assert.NoError(t, err, edge)
	}

	store.err = errors.New("database down")
	_, err := r.report(context.Background(), "2026-09-02")
	assert.ErrorIs(t, err, store.err)

	noSchedule := newServiceReporter(store, func() *rider.Index { return nil }, testReportSettings)
	_, err = noSchedule.report(context.Background(), "2026-09-02")
	assert.ErrorIs(t, err, errNoSchedule)
}

func TestServiceReporter_KeepsPastDays(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	first, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	second, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	assert.Same(t, first, second)
	assert.Equal(t, 1, store.reads)
}

func TestServiceReporter_WorksOutTodayEveryTime(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
	}{
		{"during the day", time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)},
		// 01:10 in Los Angeles: T3 is still running, and the 03:00 cutoff
		// still calls it 2 September.
		{"after midnight, before the service day ends", time.Date(2026, 9, 3, 8, 10, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, store, _ := newFixtureReporter(t, tc.now)
			for range 2 {
				_, err := r.report(context.Background(), "2026-09-02")
				require.NoError(t, err)
			}
			assert.Equal(t, 2, store.reads)
		})
	}
}

func TestServiceReporter_Settled(t *testing.T) {
	ix := reportFixtureIndex(t)
	la := ix.Timezone()
	// A day whose last scheduled arrival is 27:30, 03:30 the next morning,
	// and one whose service ends at 22:00.
	late := &serviceReport{serviceEnds: time.Date(2026, 9, 3, 3, 30, 0, 0, la)}
	early := &serviceReport{serviceEnds: time.Date(2026, 9, 2, 22, 0, 0, 0, la)}
	cases := []struct {
		name string
		rep  *serviceReport
		now  time.Time
		want bool
	}{
		{"the next service date, the last run not over an hour", late, time.Date(2026, 9, 3, 4, 29, 0, 0, la), false},
		{"an hour after the last run", late, time.Date(2026, 9, 3, 4, 30, 0, 0, la), true},
		{"service over, but still today before the 03:00 cutoff", early, time.Date(2026, 9, 3, 2, 59, 0, 0, la), false},
		{"service over, and the next service date", early, time.Date(2026, 9, 3, 3, 0, 0, 0, la), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return ix }, testReportSettings)
			r.now = func() time.Time { return tc.now }
			assert.Equal(t, tc.want, r.settled(ix, "20260902", tc.rep))
		})
	}
}

func TestServiceReporter_RefreshedScheduleWorksDaysOutAgain(t *testing.T) {
	r, store, first := newFixtureReporter(t, reportFixtureNow)
	_, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)

	second := reportFixtureIndex(t)
	require.NotSame(t, first, second)
	r.index = func() *rider.Index { return second }
	_, err = r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	assert.Equal(t, 2, store.reads)
}

func TestServiceReporter_RefreshedScheduleDropsTheOldReports(t *testing.T) {
	r, _, first := newFixtureReporter(t, reportFixtureNow)
	for _, date := range []string{"2026-09-01", "2026-09-02"} {
		_, err := r.report(context.Background(), date)
		require.NoError(t, err)
	}
	require.Len(t, r.cache, 2)
	require.Same(t, first, r.schedule)

	second := reportFixtureIndex(t)
	r.index = func() *rider.Index { return second }
	// Today's report is never kept, so nothing takes the old reports' place:
	// asking is enough to let them, and the schedule they hold, go.
	_, err := r.report(context.Background(), "2026-09-04")
	require.NoError(t, err)
	assert.Empty(t, r.cache)
	assert.Empty(t, r.order)
	assert.Same(t, second, r.schedule)

	_, err = r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	assert.Len(t, r.cache, 1)
	assert.Equal(t, []string{"20260902"}, r.order)
}

func TestServiceReporter_DoesNotKeepAReportForAReplacedSchedule(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	second := reportFixtureIndex(t)
	// While the first request is reading its trips, the schedule is
	// refreshed and another day is asked for against the new one.
	store.onRead = func() {
		r.index = func() *rider.Index { return second }
		_, err := r.report(context.Background(), "2026-09-01")
		require.NoError(t, err)
	}
	stale, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	require.Equal(t, 2, store.reads)

	fresh, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	assert.NotSame(t, stale, fresh, "a report worked out against the replaced schedule was kept")
	assert.Equal(t, 3, store.reads)
	assert.Same(t, second, r.schedule)
}

func TestServiceReporter_CacheIsBounded(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow.AddDate(1, 0, 0))
	day := func(i int) string { return time.Date(2026, 9, 1+i, 0, 0, 0, 0, time.UTC).Format(reportDateLayout) }

	for i := range reportCacheSize + 1 {
		_, err := r.report(context.Background(), day(i))
		require.NoError(t, err)
	}
	require.Equal(t, reportCacheSize+1, store.reads)
	assert.Len(t, r.cache, reportCacheSize)

	// The newest day is still kept; the oldest was dropped for it.
	_, err := r.report(context.Background(), day(reportCacheSize))
	require.NoError(t, err)
	assert.Equal(t, reportCacheSize+1, store.reads)
	_, err = r.report(context.Background(), day(0))
	require.NoError(t, err)
	assert.Equal(t, reportCacheSize+2, store.reads)
}

func TestEnvNonNegativeDurationOrDefault(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
	}{
		{"", time.Minute},
		{"90s", 90 * time.Second},
		{"0", 0},
		{"-1m", time.Minute},
		{"soon", time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("REPORT_ON_TIME_EARLY", tc.value)
			assert.Equal(t, tc.want, envNonNegativeDurationOrDefault("REPORT_ON_TIME_EARLY", time.Minute))
		})
	}
}
