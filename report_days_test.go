package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	r.saving.Wait()

	assert.Equal(t, []string{"2026-09-02"}, keptDates(store))
	kept := store.days["2026-09-02"]
	assert.Equal(t, newDayTotals(rep, testReportSettings), kept.Totals)
	assert.Equal(t, testReportSettings.onTimeEarly, kept.OnTimeEarly)
	assert.Equal(t, testReportSettings.onTimeLate, kept.OnTimeLate)

	_, err = r.report(context.Background(), "2026-09-02")
	require.NoError(t, err)
	r.saving.Wait()
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
	r.saving.Wait()
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

// waitForSaves waits for the days r is saving.
func waitForSaves(r *serviceReporter) { r.saving.Wait() }

// The requests waiting for a day get it as soon as it is worked out, while
// its totals are still being saved; and the save goes on after they leave.
func TestServiceReporter_SavesAfterTheWaitersHaveTheDay(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	gate := make(chan struct{})
	store.saveGate = gate

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, err := r.report(ctx, "2026-09-02")
		got <- err
	}()
	select {
	case err := <-got:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(gate)
		require.FailNow(t, "the request waited for the save")
	}
	// The request is over; the save it set off is not.
	cancel()
	close(gate)
	waitForSaves(r)
	assert.Equal(t, []string{"2026-09-02"}, keptDates(store))
	assert.Equal(t, []error{nil}, store.saveCtxErr, "saved on a context the request does not end")
}

// warmedDays are the dates the Analytics page opens on that warm keeps, with
// the fixture clock on 4 September: the 56 days to yesterday, less yesterday.
func warmedDays() []string {
	days := make([]string, 0, 55)
	for d := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(reportDateLayout))
	}
	return days
}

func TestServiceReporter_WarmKeepsTheDaysThePageOpensOn(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	before := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.SaveServiceDay(context.Background(), storedServiceDay{
		Date: before, OnTimeEarly: testReportSettings.onTimeEarly, OnTimeLate: testReportSettings.onTimeLate,
		Totals: dayTotals{Version: dayTotalsVersion, Routes: make([]routeDayTotals, 0), Arrivals: make([]arrivalMinute, 0)},
	}))

	require.NoError(t, r.warm(context.Background()))
	waitForSaves(r)
	assert.Equal(t, warmedDays(), keptDates(store), "the longest period and the one before, to the day before yesterday")
	assert.Equal(t, 54, store.reads, "every one of them but the day already kept")

	require.NoError(t, r.warm(context.Background()))
	assert.Equal(t, 54, store.reads, "warmed again, nothing is missing")

	// The page now opens on kept days and works out only yesterday and today.
	reads := store.reads
	reportsView(t, r, "date=2026-09-03&days=28")
	assert.Equal(t, reads+1, store.reads, "yesterday only")
}

// Warming takes one of the reporter's two slots at a time, newest day first,
// so a page always has the other.
func TestServiceReporter_WarmTakesOneSlot(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	r := newHeldReporter(store, ix)
	done := make(chan error, 1)
	go func() { done <- r.warm(context.Background()) }()

	for _, day := range []string{"2026-09-29", "2026-09-28", "2026-09-27"} {
		require.Eventually(t, func() bool { return slices.Equal([]string{day}, store.heldDays()) }, 5*time.Second, time.Millisecond)
		assert.Never(t, func() bool { return len(store.heldDays()) > 1 }, 50*time.Millisecond, time.Millisecond)
		store.release(day)
	}
	for _, d := range warmedHeldDays() {
		store.release(d)
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "warm did not finish")
	}
	assert.Equal(t, 1, store.mostHeld)
}

// warmedHeldDays are the days the held reporter, whose service date is 1
// October, warms: 6 August to 29 September.
func warmedHeldDays() []string {
	days := make([]string, 0, 55)
	for d := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(reportDateLayout))
	}
	return days
}

func TestReportWarmer_StopCancelsTheDayUnderWay(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	w := startReportWarmer(newHeldReporter(store, ix), time.Hour)
	require.Eventually(t, func() bool { return len(store.heldDays()) == 1 }, 5*time.Second, time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Stop waited for the day under way")
	}
	assert.Eventually(t, func() bool { return len(store.heldDays()) == 0 }, 5*time.Second, time.Millisecond, "its read was cancelled")
	assert.Len(t, store.readDays(), 1, "and no day was started after")
	w.Stop()
}

// The warmer warms at start, and again only once the schedule is reloaded or
// the service date moves on.
func TestReportWarmer_WarmsAgainAfterAReload(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	var mu sync.Mutex
	index := r.index()
	r.index = func() *rider.Index {
		mu.Lock()
		defer mu.Unlock()
		return index
	}
	w := startReportWarmer(r, 5*time.Millisecond)
	t.Cleanup(w.Stop)
	require.Eventually(t, func() bool { return len(keptDates(store)) == 55 }, 5*time.Second, time.Millisecond)

	store.mu.Lock()
	delete(store.days, "2026-08-20")
	store.mu.Unlock()
	assert.Never(t, func() bool { return len(keptDates(store)) == 55 }, 100*time.Millisecond, time.Millisecond,
		"the same schedule and day: nothing to warm")

	refreshed := refreshedReportFixtureIndex(t)
	mu.Lock()
	index = refreshed
	mu.Unlock()
	require.Eventually(t, func() bool { return len(keptDates(store)) == 55 }, 5*time.Second, time.Millisecond,
		"a reloaded schedule is warmed again")
}

// daysFrom are the dates from first to last, YYYY-MM-DD.
func daysFrom(first, last string) []string {
	from, _ := time.Parse(reportDateLayout, first)
	to, _ := time.Parse(reportDateLayout, last)
	days := make([]string, 0)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(reportDateLayout))
	}
	return days
}

// With LOCATION_RETENTION_PERIOD shorter than the 56 days, the warmer stops
// at the pruner's cutoff: it neither works out nor keeps a day whose reports
// the pruner has been deleting, and whose row it would delete again.
func TestServiceReporter_WarmStopsAtTheRetentionCutoff(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	// 12:00 PDT on 4 September less 20 days is 12:00 on 15 August, so 16
	// August is the first day the pruner has deleted none of.
	r.retention = 20 * 24 * time.Hour

	require.NoError(t, r.warm(context.Background()))
	waitForSaves(r)
	assert.Equal(t, daysFrom("2026-08-16", "2026-09-02"), keptDates(store))
	assert.Equal(t, 18, store.reads, "no day before the cutoff is asked for")
}

// A day whose totals fail to save leaves the pass incomplete, and the next
// pass keeps it. 15 July is among the last days a pass works out, so it is
// still in memory then, and kept without being worked out again.
func TestServiceReporter_WarmKeepsADayItFailedToSave(t *testing.T) {
	captureReportLogs(t)
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	store.saveFails = map[string]error{"2026-07-15": errors.New("database down")}

	err := r.warm(context.Background())
	require.ErrorContains(t, err, "2026-07-15: database down")
	assert.NotContains(t, keptDates(store), "2026-07-15")
	assert.Len(t, keptDates(store), 54, "the other days are kept")
	reads := store.reads

	store.mu.Lock()
	store.saveFails = nil
	store.mu.Unlock()
	require.NoError(t, r.warm(context.Background()))
	assert.Equal(t, warmedDays(), keptDates(store))
	assert.Equal(t, reads, store.reads, "kept from memory, not worked out again")
}

// A day that fails to work out does not stop the older days behind it.
func TestServiceReporter_WarmGoesOnPastADayThatFails(t *testing.T) {
	captureReportLogs(t)
	r, store, ix := newFixtureReporter(t, reportFixtureNow)
	// The trips read for 21 August start from the day before's start.
	from, err := rider.ServiceDayStart("20260820", ix.Timezone())
	require.NoError(t, err)
	store.readFails = map[time.Time]error{from: errors.New("statement timeout")}

	err = r.warm(context.Background())
	require.ErrorContains(t, err, "2026-08-21: statement timeout")
	waitForSaves(r)
	kept := keptDates(store)
	assert.NotContains(t, kept, "2026-08-21")
	assert.Subset(t, kept, daysFrom("2026-07-10", "2026-08-20"), "every older day is warmed")
	assert.Len(t, kept, 54)
}

// An incomplete pass is tried again at the warmer's next check, though the
// schedule and the day are the same.
func TestReportWarmer_TriesAnIncompletePassAgain(t *testing.T) {
	captureReportLogs(t)
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	store.saveFails = map[string]error{"2026-07-15": errors.New("database down")}
	w := startReportWarmer(r, 5*time.Millisecond)
	t.Cleanup(w.Stop)
	require.Eventually(t, func() bool { return len(keptDates(store)) == 54 }, 5*time.Second, time.Millisecond)

	store.mu.Lock()
	store.saveFails = nil
	store.mu.Unlock()
	require.Eventually(t, func() bool { return slices.Contains(keptDates(store), "2026-07-15") }, 5*time.Second, time.Millisecond)
}

// captureReportLogs keeps the reporter's log lines out of the test output.
func captureReportLogs(t *testing.T) {
	t.Helper()
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
}
