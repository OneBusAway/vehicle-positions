package main

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// keptDates are the dates the store keeps, oldest first.
func keptDates(store *fakeReportStore) []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	dates := make([]string, 0, len(store.days))
	for d := range store.days {
		dates = append(dates, d)
	}
	slices.Sort(dates)
	return dates
}

// analyticsFigures are the parts of the Analytics page worked out from its
// days, the ones the kept totals must give the same as the live reports.
func analyticsFigures(data map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for _, k := range []string{"Headline", "Lede", "Routes", "Metrics", "Trend", "Tiles", "Hours", "Histogram", "HistogramLeftOut", "Compare"} {
		out[k] = data[k]
	}
	return out
}

// TestReportsPage_KeptDaysGiveTheLiveNumbers: every view of the page gives
// the same figures from the kept totals as it does working the days out.
func TestReportsPage_KeptDaysGiveTheLiveNumbers(t *testing.T) {
	for _, query := range []string{
		"date=2026-09-02", "date=2026-09-02&days=28", "date=2026-09-02&range=day",
		"date=2026-09-02&metric=late", "date=2026-09-02&metric=no_report&compare=off", "date=2026-09-01&days=28&metric=trips_run",
	} {
		t.Run(query, func(t *testing.T) {
			r, store, _ := newFixtureReporter(t, reportFixtureNow)
			live, status := reportsView(t, r, query)
			require.Equal(t, 200, status)
			require.NotZero(t, store.saves)
			reads := store.reads

			restarted := newServiceReporter(store, r.index, testReportSettings)
			restarted.now = r.now
			kept, _ := reportsView(t, restarted, query)
			assert.Equal(t, reads, store.reads, "the days came from the store")
			assert.Equal(t, analyticsFigures(live), analyticsFigures(kept))
		})
	}
}

// TestServiceReporter_KeepsDaysOlderThanYesterday: a settled day older than
// yesterday is saved once, as the totals of its report. Yesterday and today
// are worked out each time they are not cached, never kept.
func TestServiceReporter_KeepsDaysOlderThanYesterday(t *testing.T) {
	// The fixture clock is on 4 September: today; the 3rd is yesterday.
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	_, err = r.report(context.Background(), "2026-09-03")
	require.NoError(t, err)
	_, err = r.report(context.Background(), "2026-09-04")
	require.NoError(t, err)

	assert.Equal(t, []string{"2026-09-02"}, keptDates(store))
	kept := store.days["2026-09-02"]
	assert.Equal(t, newDayTotals(rep, testReportSettings), kept.Totals)
	assert.Equal(t, testReportSettings.onTimeEarly, kept.OnTimeEarly)
	assert.Equal(t, testReportSettings.onTimeLate, kept.OnTimeLate)

	_, err = r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	assert.Equal(t, 1, store.saves, "saved once, then served from memory")
}

// TestReportsPage_TodayAndYesterdayStayLive: a period ending today reads its
// past days from the store and works out yesterday and today again.
func TestReportsPage_TodayAndYesterdayStayLive(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	reportsView(t, r, "date=2026-09-04&compare=off")
	assert.Equal(t, []string{"2026-08-29", "2026-08-30", "2026-08-31", "2026-09-01", "2026-09-02"}, keptDates(store))

	restarted := newServiceReporter(store, r.index, testReportSettings)
	restarted.now = r.now
	reads := store.reads
	reportsView(t, restarted, "date=2026-09-04&compare=off")
	assert.Equal(t, reads+2, store.reads, "yesterday and today, worked out again")
}

// TestReportsPage_AReloadedScheduleKeepsTheKeptDays: a kept day stays judged
// against the timetable it ran to; a schedule reload empties the reporter's
// memory but does not work the day out again.
func TestReportsPage_AReloadedScheduleKeepsTheKeptDays(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	before, _ := reportsView(t, r, "date=2026-09-02&compare=off")
	reads, saves := store.reads, store.saves

	refreshed := refreshedReportFixtureIndex(t)
	r.index = func() *rider.Index { return refreshed }
	after, _ := reportsView(t, r, "date=2026-09-02&compare=off")
	assert.Equal(t, reads, store.reads)
	assert.Equal(t, saves, store.saves)
	assert.Equal(t, analyticsFigures(before), analyticsFigures(after))
}

// TestReportsPage_ALateReportLeavesAKeptDay: points that come in after a day
// was kept do not change it.
func TestReportsPage_ALateReportLeavesAKeptDay(t *testing.T) {
	r, store, ix := newFixtureReporter(t, reportFixtureNow)
	before, _ := reportsView(t, r, "date=2026-09-02&range=day")
	require.Contains(t, keptDates(store), "2026-09-02")

	// A run of a trip nobody ran that day, which its phone only now uploads.
	rep, err := r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	var notRun *rider.TripInfo
	for _, route := range rep.Routes {
		for _, trip := range route.Trips {
			if len(trip.Runs) == 0 && notRun == nil {
				notRun = mustTrip(t, ix, trip.GtfsTripID)
			}
		}
	}
	require.NotNil(t, notRun)
	first, last := notRun.StopTimes[0].Departure, notRun.StopTimes[len(notRun.StopTimes)-1].Arrival
	store.mu.Lock()
	store.trips = append(store.trips, TripSummary{
		ID: 900, VehicleID: "bus-late", RouteID: notRun.RouteID, GtfsTripID: notRun.ID,
		StartTime: reportDayStart.Add(first - time.Minute), Status: "completed",
	})
	store.trails[900] = driveTrip(notRun, reportDayStart, []reportLeg{{first - time.Minute, 0}, {first, 0}, {last, notRun.Shape.Length}}, nil)
	store.mu.Unlock()

	restarted := newServiceReporter(store, r.index, testReportSettings)
	restarted.now = r.now
	after, _ := reportsView(t, restarted, "date=2026-09-02&range=day")
	assert.Equal(t, analyticsFigures(before), analyticsFigures(after))

	// Worked out again, the day would count the late run.
	fresh := newServiceReporter(&fakeReportStore{trips: store.trips, trails: store.trails}, r.index, testReportSettings)
	fresh.now = r.now
	relive, _ := reportsView(t, fresh, "date=2026-09-02&range=day")
	assert.NotEqual(t, analyticsFigures(before), analyticsFigures(relive))
}

// TestReportsPage_AnotherOnTimeWindowWorksTheDaysOutAgain: a kept day judged
// by another window is worked out again under the new one and replaced.
func TestReportsPage_AnotherOnTimeWindowWorksTheDaysOutAgain(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	reportsView(t, r, "date=2026-09-02&range=day")
	reads, saves := store.reads, store.saves

	strict := testReportSettings
	strict.onTimeLate = time.Minute
	again := newServiceReporter(store, r.index, strict)
	again.now = r.now
	data, _ := reportsView(t, again, "date=2026-09-02&range=day")
	assert.Equal(t, reads+1, store.reads)
	assert.Equal(t, saves+1, store.saves)
	assert.Equal(t, time.Minute, store.days["2026-09-02"].OnTimeLate)
	assert.Equal(t, "1 min", data["OnTimeLate"])
}

// TestReportsPage_FillsMissingDaysOnce: the days before the store kept any
// are worked out on the first look at them and kept; a second look, or two
// at once, works none out again and keeps each day once.
func TestReportsPage_FillsMissingDaysOnce(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, _ := url.ParseQuery("date=2026-09-02")
			_, _, err := reportsPageView(context.Background(), r, &overviewReports{}, q)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.Len(t, keptDates(store), 14, "the 7 days and the 7 before them")
	assert.Equal(t, 14, store.saves, "each kept once")
	reads := store.reads

	reportsView(t, r, "date=2026-09-02")
	assert.Equal(t, reads, store.reads)
	assert.Equal(t, 14, store.saves)
}

func TestReportsPage_AKeptDaysReadThatFails(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	store.daysErr = errors.New("database down")
	q, _ := url.ParseQuery("date=2026-09-02")
	_, _, err := reportsPageView(context.Background(), r, &overviewReports{}, q)
	assert.ErrorIs(t, err, store.daysErr)
}
