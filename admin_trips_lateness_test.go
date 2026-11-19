package main

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// newLatenessMux serves the Trips list of trips over the report fixture's
// schedule ix, with trails as the store holds them.
func newLatenessMux(t *testing.T, ix *rider.Index, trips []TripSummary, trails map[int64][]LocationPoint) (*http.ServeMux, *fakeReportStore) {
	t.Helper()
	store := &fakeReportStore{trails: trails}
	ui := newTestAdminUI(t)
	ui.reports = newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	ui.trips = &fakeTripLister{trips: trips}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux, store
}

// runningFixtureTrip is fixture trip id still running at at on the fixture
// day's clock, with the trail it had sent by then.
func runningFixtureTrip(t *testing.T, ix *rider.Index, id int64, at time.Duration) (TripSummary, []LocationPoint) {
	t.Helper()
	trip, points := fixtureTrip(t, ix, id)
	trip.Status = "active"
	cut := reportDayStart.Add(at).Unix()
	sofar := make([]LocationPoint, 0, len(points))
	for _, p := range points {
		if p.Timestamp <= cut {
			sofar = append(sofar, p)
		}
	}
	return trip, sofar
}

// tripListStatus is the Status cell of trip id's row on the Trips list.
func tripListStatus(t *testing.T, body string, id int64) string {
	t.Helper()
	row := regexp.MustCompile(`(?s)<a class="row-link" href="/admin/trips/` + strconv.FormatInt(id, 10) + `">.*?<td class="c-side[^"]*">(.*?)</td>`)
	m := row.FindStringSubmatch(body)
	require.NotNil(t, m, "trip %d is on the list", id)
	return m[1]
}

// assertNoLateness: the cell says nothing of the timetable, not even on time.
func assertNoLateness(t *testing.T, cell string, why string) {
	t.Helper()
	for _, s := range []string{`class="sub"`, "On time", "tone-", "unknown", "no report"} {
		assert.NotContains(t, cell, s, why)
	}
}

// TestTripsPage_RunningTripLateness: a running trip shows how it stood at the
// last stop it passed, in the words and colours of its own page and of
// Analytics. One that has passed no stop yet, one the schedule does not have,
// and one that has ended show nothing, and only the running trips the
// schedule has are read.
func TestTripsPage_RunningTripLateness(t *testing.T) {
	ix := reportFixtureIndex(t)
	// T1 has left ST1 6 s late and is on its way to ST2.
	onTime, onTimeTrail := runningFixtureTrip(t, ix, 1, hms(8, 1, 0))
	// RE left ST1 2:54 early and ST2 6:14 early; the last stop is what shows.
	early, earlyTrail := runningFixtureTrip(t, ix, 2, hms(8, 59, 0))
	// RL left ST1 and ST2 7:06 late.
	late, lateTrail := runningFixtureTrip(t, ix, 3, hms(11, 12, 30))
	// RG is still waiting at ST1.
	waiting, waitingTrail := runningFixtureTrip(t, ix, 4, hms(12, 59, 0))
	// RL's whole trail, under a trip the schedule does not have and under one
	// that has ended: either would show a figure if it were worked out.
	_, rlTrail := fixtureTrip(t, ix, 3)
	gone := late
	gone.ID, gone.GtfsTripID = 9, "GONE"
	ended := late
	end := late.StartTime.Add(20 * time.Minute)
	ended.ID, ended.Status, ended.EndTime = 30, "completed", &end

	mux, store := newLatenessMux(t, ix, []TripSummary{onTime, early, late, waiting, gone, ended}, map[int64][]LocationPoint{
		1: onTimeTrail, 2: earlyTrail, 3: lateTrail, 4: waitingTrail, 9: rlTrail, 30: rlTrail,
	})
	body := getAdminPage(t, mux, "/admin/trips")

	assert.Contains(t, tripListStatus(t, body, 1), `<span class="sub">On time &#43;0:06</span>`, "inside the window is on time, never late")
	assert.Contains(t, tripListStatus(t, body, 2), `<span class="sub"><span class="tone-early">6:14 early</span></span>`, "early is red")
	assert.NotContains(t, tripListStatus(t, body, 2), "2:54", "the last stop passed, not the first")
	assert.Contains(t, tripListStatus(t, body, 3), `<span class="sub"><span class="tone-late">7:06 late</span></span>`, "late is blue")
	for _, id := range []int64{1, 2, 3} {
		assert.Contains(t, tripListStatus(t, body, id), `<span class="pill pill-ok">Running</span>`)
	}

	waitingCell := tripListStatus(t, body, 4)
	assert.Contains(t, waitingCell, `<span class="pill pill-ok">Running</span>`)
	assertNoLateness(t, waitingCell, "no stop passed yet is not on time")
	assertNoLateness(t, tripListStatus(t, body, 9), "a trip the schedule does not have")
	endedCell := tripListStatus(t, body, 30)
	assert.Contains(t, endedCell, "Completed")
	assertNoLateness(t, endedCell, "an ended trip")

	assert.Equal(t, 1, store.trailReads, "one read for the page")
	assert.ElementsMatch(t, []int64{1, 2, 3, 4}, store.trailIDs, "only the running trips the schedule has")
}

// TestTripsPage_LatenessOnlyForTheShownPage: the one more trip the list asks
// for, to know there is a next page, is not shown and not read.
func TestTripsPage_LatenessOnlyForTheShownPage(t *testing.T) {
	ix := reportFixtureIndex(t)
	running, trail := runningFixtureTrip(t, ix, 1, hms(8, 1, 0))
	trips := make([]TripSummary, 0, adminPageSize+1)
	trails := make(map[int64][]LocationPoint, adminPageSize+1)
	shown := make([]int64, 0, adminPageSize)
	for id := int64(1); id <= adminPageSize+1; id++ {
		trip := running
		trip.ID = id
		trips = append(trips, trip)
		trails[id] = trail
		if id <= adminPageSize {
			shown = append(shown, id)
		}
	}
	mux, store := newLatenessMux(t, ix, trips, trails)

	body := getAdminPage(t, mux, "/admin/trips")

	assert.Len(t, regexp.MustCompile(`<span class="sub">On time `).FindAllString(body, -1), adminPageSize, "every shown trip")
	assert.Equal(t, 1, store.trailReads)
	assert.Equal(t, shown, store.trailIDs)
}

// TestTripsPage_LatenessWithoutASchedule: with no schedule there is nothing
// to judge against, so no trail is read.
func TestTripsPage_LatenessWithoutASchedule(t *testing.T) {
	ix := reportFixtureIndex(t)
	running, trail := runningFixtureTrip(t, ix, 3, hms(11, 12, 30))
	mux, store := newLatenessMux(t, nil, []TripSummary{running}, map[int64][]LocationPoint{running.ID: trail})

	body := getAdminPage(t, mux, "/admin/trips")

	assertNoLateness(t, tripListStatus(t, body, running.ID), "no schedule")
	assert.Zero(t, store.trailReads)
}

// TestTripsPage_LatenessWithNoTripRunning: a page of ended trips reads no
// trail.
func TestTripsPage_LatenessWithNoTripRunning(t *testing.T) {
	ix := reportFixtureIndex(t)
	trip, trail := fixtureTrip(t, ix, 3)
	mux, store := newLatenessMux(t, ix, []TripSummary{trip}, map[int64][]LocationPoint{trip.ID: trail})

	body := getAdminPage(t, mux, "/admin/trips")

	assertNoLateness(t, tripListStatus(t, body, trip.ID), "an ended trip")
	assert.Zero(t, store.trailReads)
}

func TestTripsPage_LatenessReadFails(t *testing.T) {
	ix := reportFixtureIndex(t)
	running, trail := runningFixtureTrip(t, ix, 3, hms(11, 12, 30))
	mux, store := newLatenessMux(t, ix, []TripSummary{running}, map[int64][]LocationPoint{running.ID: trail})
	store.trailErr = errors.New("connection refused")

	w := requestAdminPage(t, mux, "/admin/trips")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "connection refused")
}
