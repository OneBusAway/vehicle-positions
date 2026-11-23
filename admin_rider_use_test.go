package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingRideDays serves days as the store adds them up and remembers the
// periods it was asked for.
type recordingRideDays struct {
	days  []RideDay
	err   error
	calls []string
}

func (f *recordingRideDays) CountRidesByDay(_ context.Context, first, last string) ([]RideDay, error) {
	f.calls = append(f.calls, first+" to "+last)
	return f.days, f.err
}

// riderUsePage is the Rider mode page's use-by-day section, with rider mode
// on, days as the store's answer, the fixture schedule's clock, and now as
// the time.
func riderUsePage(t *testing.T, days *recordingRideDays, now time.Time) string {
	t.Helper()
	mux, ui := newRiderPageMux(t, fakeRiderPageSource{status: enabledRiderStatus()}, &recordingRideLister{}, loadFixtureIndex(t))
	ui.rideDays = days
	ui.reports.now = func() time.Time { return now }
	return riderPageSection(t, getAdminPage(t, mux, "/admin/rider"), "rider-use")
}

func TestRiderPage_UseByDay(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	days := &recordingRideDays{days: []RideDay{
		{StartDate: "20260806", Rides: 1, Trips: 1, PointsMatched: 4, PointsTotal: 5},
		{StartDate: "20260901", Rides: 3, Trips: 2, PointsMatched: 8, PointsTotal: 12},
		{StartDate: "20260902", Rides: 2, Trips: 2, PointsMatched: 3, PointsTotal: 3},
	}}

	use := riderUsePage(t, days, time.Date(2026, 9, 2, 8, 0, 0, 0, la))
	assert.Equal(t, []string{"20260806 to 20260902"}, days.calls, "the 28 service days to today, on the agency's clock")

	assert.Contains(t, use, "6 rides on 5 trips in the last 28 days, 15 of 20 points matched.")
	assert.Contains(t, use, `aria-label="Rides per service day over the last 28 days: 28 bars, the highest 3"`)
	assert.Contains(t, use, "Most in a day: 3")
	assert.Equal(t, 28, strings.Count(use, `class="hit"`), "a bar a day")
	assert.Equal(t, 3, strings.Count(use, `<rect class="bar"`), "only the days with rides have one")
	assert.Contains(t, use, `data-tip="6 Aug|1 ride on 1 trip, 4 of 5 points matched"`, "the first day of the period")
	assert.Contains(t, use, `data-tip="1 Sep|3 rides on 2 trips, 8 of 12 points matched"`)
	assert.Contains(t, use, `data-tip="2 Sep|2 rides on 2 trips, 3 of 3 points matched"`, "today")
	assert.Equal(t, 25, strings.Count(use, `|No rides"`), "every other day reads zero")
	assert.Contains(t, use, `data-tip="7 Aug|No rides"`)
	assert.Contains(t, use, `<div class="chart-x"><span>6 Aug</span>`)
	assert.Contains(t, use, `<span>2 Sep</span></div>`)

	assert.Contains(t, use, "Show these numbers as a table")
	assert.Equal(t, 28, strings.Count(use, "<tr><td>"), "a row a day in the table")
	assert.Contains(t, use, `<tr><td>Thu 6 Aug</td><td class="num right">1</td><td class="num right">1</td><td class="num right">4 of 5</td></tr>`, "the first day of the period")
	assert.Contains(t, use, `<tr><td>Fri 7 Aug</td><td class="num right">0</td><td class="num right">0</td><td class="num right">0 of 0</td></tr>`, "a day with no ride")
	assert.Contains(t, use, `<tr><td>Tue 1 Sep</td><td class="num right">3</td><td class="num right">2</td><td class="num right">8 of 12</td></tr>`)
	assert.Contains(t, use, `<tr><td>Wed 2 Sep</td><td class="num right">2</td><td class="num right">2</td><td class="num right">3 of 3</td></tr>`, "today")

	// Before 03:00 the service day is still yesterday's.
	days.calls = nil
	riderUsePage(t, days, time.Date(2026, 9, 3, 2, 0, 0, 0, la))
	assert.Equal(t, []string{"20260806 to 20260902"}, days.calls)
}

func TestRiderPage_UseByDayWithNoRides(t *testing.T) {
	days := &recordingRideDays{}
	use := riderUsePage(t, days, time.Now())

	require.Len(t, days.calls, 1)
	assert.Contains(t, use, "No rides in the last 28 days.")
	assert.Equal(t, 28, strings.Count(use, `|No rides"`), "a zero for every day")
	assert.Equal(t, 28, strings.Count(use, `<td class="num right">0 of 0</td></tr>`), "in the table too")
	assert.NotContains(t, use, `<rect class="bar"`)
	assert.NotContains(t, use, "Most in a day", "no peak to name")
}

// With no schedule the page has no agency clock, and its days are UTC's.
func TestRiderPage_UseByDayWithNoSchedule(t *testing.T) {
	days := &recordingRideDays{}
	mux, ui := newRiderPageMux(t, fakeRiderPageSource{status: enabledRiderStatus()}, &recordingRideLister{}, nil)
	ui.rideDays = days
	ui.reports.now = func() time.Time { return time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC) }

	assert.Contains(t, getAdminPage(t, mux, "/admin/rider"), `id="rider-use"`)
	assert.Equal(t, []string{"20260807 to 20260903"}, days.calls)
}

func TestRiderPage_UseByDayOff(t *testing.T) {
	days := &recordingRideDays{}
	mux, ui := newRiderPageMux(t, riderOff{}, &recordingRideLister{}, nil)
	ui.rideDays = days

	body := getAdminPage(t, mux, "/admin/rider")
	assert.Contains(t, body, `id="rider-off"`, "the page explains rider mode as before")
	assert.NotContains(t, body, `id="rider-use"`)
	assert.Empty(t, days.calls, "no rides are added up while rider mode is off")
}

func TestRiderPage_UseByDayFails(t *testing.T) {
	mux, ui := newRiderPageMux(t, fakeRiderPageSource{status: enabledRiderStatus()}, &recordingRideLister{}, nil)
	ui.rideDays = &recordingRideDays{err: assert.AnError}

	w := requestAdminPage(t, mux, "/admin/rider")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), serverErrorTitle)
}
