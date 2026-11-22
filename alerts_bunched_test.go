package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// bunchedSettings are main's defaults.
var bunchedSettings = alertSettings{
	quietAfter:       defaultAlertQuietAfter,
	stoppedAfter:     defaultAlertStoppedAfter,
	offRouteDistance: rider.DefaultThresholds().MaxShapeDistance,
	offRouteAfter:    defaultAlertOffRouteAfter,
	notStartedAfter:  defaultAlertNotStartedAfter,
	bunchedAfter:     defaultAlertBunchedAfter,
}

// bunchTest is a watcher over the gap fixture whose tracker holds the cars
// of the latest look. Fix times count from bunchClock, and the server's
// times from heard.
type bunchTest struct {
	t       *testing.T
	ix      *rider.Index
	w       *alertWatcher
	heard   time.Time
	mu      sync.Mutex
	tracked []*VehicleState
}

func newBunchTest(t *testing.T, heard time.Time) *bunchTest {
	b := &bunchTest{t: t, ix: gapFixtureIndex(t), heard: heard}
	b.w = newAlertWatcher(&fakeTripLister{}, bunchedSettings)
	b.w.watchingSince = heard.Add(-time.Hour)
	b.w.watchRoutes(func() *rider.Index { return b.ix })
	b.w.watchBunching(func() []*VehicleState {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.tracked
	})
	return b
}

// report has each car report after after: the tracker keeps it, and the
// watcher records where it was. A car's own fix and heard are added on.
func (b *bunchTest) report(after time.Duration, cars ...liveCar) []TripSummary {
	b.t.Helper()
	cars = slices.Clone(cars)
	for i := range cars {
		cars[i].fix += after
		cars[i].heard += after
	}
	trips, states := liveCars(b.t, b.ix, bunchClock, b.heard, cars...)
	for _, v := range states {
		info := mustTrip(b.t, b.ix, v.TripID)
		b.w.record(v.VehicleID, info.Shape.PointAt(*v.alongShape), v.UpdatedAt)
	}
	b.mu.Lock()
	b.tracked = states
	b.mu.Unlock()
	return trips
}

// look works the alerts out after after.
func (b *bunchTest) look(trips []TripSummary, after time.Duration) []alertEntry {
	return b.w.alerts(trips, b.heard.Add(after))
}

// bunchedPair is bus-F 75 s behind bus-L on GN, every 10 minutes at ST2.
func bunchedPair() []liveCar {
	return []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250}}
}

func TestAlerts_BunchedAfter(t *testing.T) {
	after := bunchedSettings.bunchedAfter
	tests := []struct {
		name string
		held time.Duration
		want bool
	}{
		{"just under", after - time.Second, false},
		{"exactly at", after, true},
		{"just over", after + time.Second, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newBunchTest(t, bunchClock)
			trips := b.report(0, bunchedPair()...)
			require.Empty(t, b.look(trips, 0), "the first look only starts the spell")

			trips = b.report(tc.held, bunchedPair()...)
			got := b.look(trips, tc.held)

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertBunched, got[0].Kind)
			assert.Equal(t, bunchClock, got[0].Since)
			assert.Equal(t, int64(tc.held/time.Second), got[0].DurationSeconds)
			assert.Equal(t, trips[0], got[0].Trip, "raised on the vehicle behind")
			assert.Equal(t, &alertAhead{VehicleID: "bus-L", VehicleLabel: "Bus L", SpacingS: 75, ScheduledGapS: 600}, got[0].Ahead)
			assert.Nil(t, got[0].NextTrip)
		})
	}
}

// TestAlerts_BunchedLevel: a vehicle whose report puts it level with the one
// ahead, or past it, is 0 s behind it, not less.
func TestAlerts_BunchedLevel(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	level := []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250, fix: 90 * time.Second}}
	b.look(b.report(0, level...), 0)

	got := b.look(b.report(3*time.Minute, level...), 3*time.Minute)

	require.Len(t, got, 1)
	assert.Equal(t, &alertAhead{VehicleID: "bus-L", VehicleLabel: "Bus L", SpacingS: 0, ScheduledGapS: 600}, got[0].Ahead)
}

// TestAlerts_BunchedCountsOnlyReportedTime: the vehicles may have spread out
// in the silence after their latest reports, so a spell lasts to the reports
// it was found from and not to now.
func TestAlerts_BunchedCountsOnlyReportedTime(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	b.look(b.report(0, bunchedPair()...), 0)
	trips := b.report(2*time.Minute, bunchedPair()...)
	b.look(trips, 2*time.Minute)

	silentUntil := 2*time.Minute + bunchedSettings.quietAfter
	assert.Empty(t, b.look(trips, silentUntil), "five minutes on, but two of them reported")

	trips = b.report(3*time.Minute, bunchedPair()...)
	got := b.look(trips, silentUntil)
	require.Len(t, got, 1)
	assert.Equal(t, alertBunched, got[0].Kind)
	assert.Equal(t, int64(silentUntil/time.Second), got[0].DurationSeconds)
}

// TestAlerts_BunchedStartsAgain: a look that does not find the vehicle
// bunched ends its spell, and the next one found starts a new one.
func TestAlerts_BunchedStartsAgain(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	b.look(b.report(0, bunchedPair()...), 0)
	spreadOut := []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 900}}
	b.look(b.report(time.Minute, spreadOut...), time.Minute)
	assert.Empty(t, b.w.bunched, "the spell ends")

	b.look(b.report(2*time.Minute, bunchedPair()...), 2*time.Minute)
	assert.Empty(t, b.look(b.report(4*time.Minute, bunchedPair()...), 4*time.Minute), "two minutes into the new spell")

	got := b.look(b.report(5*time.Minute, bunchedPair()...), 5*time.Minute)
	require.Len(t, got, 1)
	assert.Equal(t, bunchClock.Add(2*time.Minute), got[0].Since)
}

// TestAlerts_BunchedBehindAnotherVehicleKeepsItsSpell: the vehicle ahead can
// change, when it ends its trip or another overtakes, and the vehicle is
// still bunched: one alert, naming the vehicle ahead now.
func TestAlerts_BunchedBehindAnotherVehicleKeepsItsSpell(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	b.look(b.report(0, bunchedPair()...), 0)
	behindM := []liveCar{{name: "F", trip: "GN", along: 125}, {name: "M", trip: "GN", along: 225}}
	b.look(b.report(time.Minute, behindM...), time.Minute)

	got := b.look(b.report(3*time.Minute, behindM...), 3*time.Minute)
	require.Len(t, got, 1)
	assert.Equal(t, bunchClock, got[0].Since)
	assert.Equal(t, &alertAhead{VehicleID: "bus-M", VehicleLabel: "Bus M", SpacingS: 60, ScheduledGapS: 600}, got[0].Ahead)
}

// TestAlerts_BunchedOnANewTripStartsAgain: a spell belongs to the trip it was
// found on.
func TestAlerts_BunchedOnANewTripStartsAgain(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	b.look(b.report(0, bunchedPair()...), 0)
	trips := b.report(2*time.Minute, bunchedPair()...)
	trips[0].ID = 9
	b.look(trips, 2*time.Minute)

	trips = b.report(4*time.Minute, bunchedPair()...)
	trips[0].ID = 9
	assert.Empty(t, b.look(trips, 4*time.Minute))

	trips = b.report(5*time.Minute, bunchedPair()...)
	trips[0].ID = 9
	got := b.look(trips, 5*time.Minute)
	require.Len(t, got, 1)
	assert.Equal(t, bunchClock.Add(2*time.Minute), got[0].Since)
}

// TestAlerts_BunchedClearsWhenTheTripEnds: a vehicle with no active trip has
// no alert, and nothing kept for it.
func TestAlerts_BunchedClearsWhenTheTripEnds(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	b.look(b.report(0, bunchedPair()...), 0)
	trips := b.report(3*time.Minute, bunchedPair()...)
	require.Len(t, b.look(trips, 3*time.Minute), 1)

	assert.Empty(t, b.look(trips[1:], 3*time.Minute))
	assert.Empty(t, b.w.bunched)
}

// TestAlerts_NotMovingComesBeforeBunched: two vehicles stuck together in
// traffic are not moving, which is the one to see to first.
func TestAlerts_NotMovingComesBeforeBunched(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	for after := time.Duration(0); after <= bunchedSettings.stoppedAfter; after += 30 * time.Second {
		b.look(b.report(after, bunchedPair()...), after)
	}
	got := b.look(b.report(bunchedSettings.stoppedAfter, bunchedPair()...), bunchedSettings.stoppedAfter)

	require.Len(t, got, 2)
	for _, a := range got {
		assert.Equal(t, alertNotMoving, a.Kind, a.Trip.VehicleID)
		assert.Nil(t, a.Ahead)
	}
}

// TestAlerts_OffRouteComesBeforeBunched: a vehicle off its route keeps its
// place from before it left the route, and is bunched behind no one.
func TestAlerts_OffRouteComesBeforeBunched(t *testing.T) {
	b := newBunchTest(t, bunchClock)
	b.look(b.report(0, bunchedPair()...), 0)
	trips := b.report(3*time.Minute, bunchedPair()...)
	b.w.routes["bus-F"] = routeState{tripStart: trips[0].StartTime, offSince: b.heard, offLast: b.heard.Add(3 * time.Minute)}
	b.w.settings.offRouteAfter = time.Minute

	got := b.look(trips, 3*time.Minute)
	require.Len(t, got, 1)
	assert.Equal(t, alertOffRoute, got[0].Kind)
}

// TestAlerts_BunchedNeedsTheTrackerAndTheSchedule: until main wires both in,
// no vehicle is bunched.
func TestAlerts_BunchedNeedsTheTrackerAndTheSchedule(t *testing.T) {
	for _, tc := range []struct {
		name               string
		vehicles, schedule bool
	}{
		{"neither", false, false},
		{"no tracker", false, true},
		{"no schedule", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBunchTest(t, bunchClock)
			if !tc.vehicles {
				b.w.vehicles = nil
			}
			if !tc.schedule {
				b.w.schedule = nil
			}
			b.look(b.report(0, bunchedPair()...), 0)
			assert.Empty(t, b.look(b.report(5*time.Minute, bunchedPair()...), 5*time.Minute))
		})
	}
}

func TestAlertSettingsFromEnv_BunchedAfter(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", defaultAlertBunchedAfter},
		{"5m", 5 * time.Minute},
		{"90s", 90 * time.Second},
		{"0s", defaultAlertBunchedAfter},
		{"-1m", defaultAlertBunchedAfter},
		{"soon", defaultAlertBunchedAfter},
	}
	for _, tc := range tests {
		t.Run("ALERT_BUNCHED_AFTER="+tc.value, func(t *testing.T) {
			t.Setenv("ALERT_BUNCHED_AFTER", tc.value)
			assert.Equal(t, tc.want, alertSettingsFromEnv(60).bunchedAfter)
		})
	}
}

// trackerBunchTest is the gap fixture's route 5 run through a real tracker
// and its trip locator, as main wires them: bus-F and bus-L report on GN,
// placed from their positions.
type trackerBunchTest struct {
	tracker *Tracker
	ix      *rider.Index
	w       *alertWatcher
	trips   []TripSummary
}

func newTrackerBunchTest(t *testing.T, settings alertSettings) *trackerBunchTest {
	ix := gapFixtureIndex(t)
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	tracker.SetTripLocator(newTripLocator(func() *rider.Index { return ix }, rider.DefaultThresholds().MaxShapeDistance))
	w := newAlertWatcher(&fakeTripLister{}, settings)
	w.watchingSince = time.Now().Add(-time.Hour)
	w.watchRoutes(func() *rider.Index { return ix })
	w.watchBunching(tracker.ActiveVehicles)
	start := time.Now().Add(-10 * time.Minute)
	return &trackerBunchTest{tracker: tracker, ix: ix, w: w, trips: []TripSummary{
		{ID: 1, VehicleID: "bus-F", VehicleLabel: "Bus F", RouteID: "R5", GtfsTripID: "GN", StartTime: start, Status: "active"},
		{ID: 2, VehicleID: "bus-L", VehicleLabel: "Bus L", RouteID: "R5", GtfsTripID: "GN", StartTime: start, Status: "active"},
	}}
}

// report has bus-F report from 120 m up S1 and bus-L from 250 m, both fixed
// at bunchClock, through the tracker and the watcher.
func (b *trackerBunchTest) report() {
	gn, _ := b.ix.Trip("GN")
	for _, fix := range []struct {
		vehicle string
		along   float64
	}{{"bus-F", 120}, {"bus-L", 250}} {
		pos := gn.Shape.PointAt(fix.along)
		loc := &LocationReport{VehicleID: fix.vehicle, TripID: "GN", RouteID: "R5", StartDate: "20260902",
			Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: bunchClock.Unix(), TripStartedAt: b.trips[0].StartTime}
		b.tracker.UpdateAuthorized(loc, time.Time{})
		b.w.report(loc, time.Now())
	}
}

// TestAlerts_BunchedFromTheTracker runs the alert over the tracker's own
// places: what tripLocator names and ActiveVehicles copies is what the check
// reads.
func TestAlerts_BunchedFromTheTracker(t *testing.T) {
	settings := bunchedSettings
	settings.bunchedAfter = time.Nanosecond
	b := newTrackerBunchTest(t, settings)

	assert.Eventually(t, func() bool {
		b.report()
		got := b.w.alerts(b.trips, time.Now())
		return len(got) == 1 && got[0].Kind == alertBunched
	}, 5*time.Second, time.Millisecond)

	b.report()
	got := b.w.alerts(b.trips, time.Now())
	require.Len(t, got, 1)
	assert.Equal(t, "bus-F", got[0].Trip.VehicleID)
	require.NotNil(t, got[0].Ahead)
	assert.Equal(t, "bus-L", got[0].Ahead.VehicleID)
	assert.InDelta(t, 78, got[0].Ahead.SpacingS, 1, "130 m at 0.6 s a metre")
	assert.Equal(t, 600, got[0].Ahead.ScheduledGapS)
}

// TestAlerts_BunchedWhileReportsArrive works the alerts out while reports
// move the tracker's vehicles and reach the watcher, for go test -race: each
// look reads the tracker's copies under the watcher's lock.
func TestAlerts_BunchedWhileReportsArrive(t *testing.T) {
	b := newTrackerBunchTest(t, bunchedSettings)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			b.report()
		}
	})
	wg.Go(func() {
		for range 200 {
			b.w.alerts(b.trips, time.Now())
		}
	})
	wg.Wait()
	b.report()
	assert.Empty(t, b.w.alerts(b.trips, time.Now()), "found bunched, not yet for three minutes")
	assert.Contains(t, b.w.bunched, "bus-F")
}

// TestHandleListAlerts_Bunched: the API names the vehicle ahead under
// "ahead", which the live map's card reads.
func TestHandleListAlerts_Bunched(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	b := newBunchTest(t, now.Add(-4*time.Minute))
	lister := &fakeTripLister{trips: b.report(0, bunchedPair()...)}
	b.w.trips = lister
	b.look(lister.trips, 0)
	lister.trips = b.report(4*time.Minute, bunchedPair()...)

	rec := httptest.NewRecorder()
	handleListAlerts(b.w)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/alerts", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Alerts []map[string]json.RawMessage `json:"alerts"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Alerts, 1)
	assert.JSONEq(t, `"bunched"`, string(resp.Alerts[0]["kind"]))
	assert.JSONEq(t, `{"vehicle_id":"bus-L","vehicle_label":"Bus L","spacing_s":75,"scheduled_gap_s":600}`, string(resp.Alerts[0]["ahead"]))
	_, hasNext := resp.Alerts[0]["next_trip"]
	assert.False(t, hasNext)
}

// TestHandleListAlerts_OthersNameNoVehicleAhead: only a bunched alert has
// "ahead".
func TestHandleListAlerts_OthersNameNoVehicleAhead(t *testing.T) {
	body, err := json.Marshal(webhookAlert(alertQuiet))
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"ahead"`)
}

// TestAdminMapCardReadsTheVehicleAhead pins the live map to the names the
// alerts API writes: the bunched kind, and the vehicle ahead with the
// spacing and the scheduled gap the card's Gap row says. Each expected
// expression is built from the structs' JSON tags, as
// TestAdminMapCardReadsTheStopItIsGiven does for the stop.
func TestAdminMapCardReadsTheVehicleAhead(t *testing.T) {
	h := newTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "/static/js/map.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	script := w.Body.String()

	tag := func(of any, field string) string {
		t.Helper()
		f, ok := reflect.TypeOf(of).FieldByName(field)
		require.True(t, ok, "%T has no field %s", of, field)
		return strings.Split(f.Tag.Get("json"), ",")[0]
	}
	entry, ahead := alertEntry{}, alertAhead{}
	for _, want := range []string{
		alertBunched + `: "Bunched"`,
		"al && al." + tag(entry, "Ahead"),
		"ageText(a." + tag(ahead, "SpacingS") + `) + " behind "`,
		"(a." + tag(ahead, "VehicleLabel") + " || a." + tag(ahead, "VehicleID") + `) + ", every "`,
		"ageText(a." + tag(ahead, "ScheduledGapS") + ")",
	} {
		assert.True(t, strings.Contains(script, want), "map.js is missing %s", want)
	}
}

// listedBunch is a bunchTest whose watcher lists its trips from lister, as
// the recorder, the webhook and the pages read it, with bus-F found bunched
// at the start and the trips of the latest report listed.
func listedBunch(t *testing.T, heard time.Time) (*bunchTest, *fakeTripLister) {
	b := newBunchTest(t, heard)
	lister := &fakeTripLister{trips: b.report(0, bunchedPair()...)}
	b.w.trips = lister
	b.look(lister.trips, 0)
	return b, lister
}

func TestAlertsPage_Bunched(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	b, lister := listedBunch(t, now.Add(-4*time.Minute))
	lister.trips = b.report(4*time.Minute, bunchedPair()...)
	mux := newAlertsMux(t, b.w)

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Contains(t, body, `<article class="card alert-row" data-kind="bunched">`)
	assert.Contains(t, body, `/static/icons.svg#layers`)
	assert.Contains(t, body, `<span class="pill pill-warn">Bunched</span>`)
	assert.Contains(t, body, "1 min behind Bus L, every 10 min, open since "+now.Add(-4*time.Minute).UTC().Format("15:04")+"</span>")
	assert.Contains(t, body, `data-filter="bunched" aria-pressed="false">Bunched <span class="n">1</span>`)
	assert.Contains(t, body, `<a class="chip" href="/admin/alerts?kind=bunched#cleared">Bunched</a>`)
	assert.Contains(t, body, "or have run under 25% of the scheduled gap behind the vehicle ahead for 3 min on frequent service")
	assert.Contains(t, body, `<a class="btn btn-sm btn-tonal" href="/admin/trips/1">`, "the vehicle behind's trip")
}

func TestAlertsPage_ClearedBunched(t *testing.T) {
	cleared := &fakeClearedAlerts{alerts: []clearedAlert{
		clearedAt(alertBunched, "bus-12", time.Now().UTC().Add(-time.Hour), 6*time.Minute),
	}}
	mux := newClearedAlertsMux(t, cleared)

	body := getAdminPage(t, mux, "/admin/alerts?kind=bunched")

	assert.Equal(t, alertBunched, cleared.kind)
	assert.Equal(t, [][2]string{{"bus-12", "Bunched"}}, clearedRows(body))
	assert.Contains(t, body, `<a class="chip" href="/admin/alerts?kind=bunched#cleared" aria-current="true">Bunched</a>`)
}

// TestOverview_NamesTheVehicleAhead: the Overview's list of the alerts open
// longest says it as the Alerts page does.
func TestOverview_NamesTheVehicleAhead(t *testing.T) {
	a := webhookAlert(alertBunched)
	a.Ahead = &alertAhead{VehicleID: "bus-12", VehicleLabel: "Bus 12", SpacingS: 75, ScheduledGapS: 600}

	got := overviewAttentionList([]alertEntry{a}, bunchedSettings, nil, time.UTC)

	require.Len(t, got, 1)
	assert.Equal(t, overviewAttention{
		Icon:   "layers",
		Title:  "Bus 7: bunched",
		Detail: "Route R1, 1 min behind Bus 12, every 10 min",
		URL:    "/admin/trips/7",
	}, got[0])
}

// TestAlertRecorder_Bunched records the watcher's own bunched alert: one row
// on the vehicle behind's trip, raised at the start of its spell, which a
// change of the vehicle ahead keeps and spreading out clears.
func TestAlertRecorder_Bunched(t *testing.T) {
	b, lister := listedBunch(t, bunchClock)
	store := &fakeAlertHistory{}
	r := newAlertRecorder(store, b.w, bunchClock, time.Hour, time.Hour)
	t.Cleanup(r.Stop)

	lister.trips = b.report(3*time.Minute, bunchedPair()...)
	r.check(bunchClock.Add(3 * time.Minute))
	behindM := []liveCar{{name: "F", trip: "GN", along: 125}, {name: "M", trip: "GN", along: 225}}
	lister.trips = b.report(4*time.Minute, behindM...)
	r.check(bunchClock.Add(4 * time.Minute))

	require.Len(t, store.raised, 1, "one alert while the vehicle ahead changes")
	assert.Empty(t, store.cleared)
	raised := store.raised[0]
	assert.Equal(t, alertBunched, raised.Kind)
	assert.Equal(t, bunchClock, raised.Since)
	assert.Equal(t, int64(1), raised.Trip.ID)
	assert.Equal(t, "bus-F", raised.Trip.VehicleID)

	spreadOut := []liveCar{{name: "F", trip: "GN", along: 125}, {name: "M", trip: "GN", along: 900}}
	lister.trips = b.report(5*time.Minute, spreadOut...)
	r.check(bunchClock.Add(5 * time.Minute))
	assert.Equal(t, []historyClear{{1, bunchClock.Add(5 * time.Minute)}}, store.cleared)
}

// TestAlertWebhook_Bunched posts the watcher's own bunched alert, naming the
// vehicle ahead, and its clear.
func TestAlertWebhook_Bunched(t *testing.T) {
	b, lister := listedBunch(t, bunchClock)
	receiver := newWebhookReceiver(t, http.StatusOK)
	h := startWebhook(t, configFor(receiver), b.w, b.ix, idleWebhookTiming())

	lister.trips = b.report(3*time.Minute, bunchedPair()...)
	h.check(bunchClock.Add(3 * time.Minute))
	lister.trips = b.report(4*time.Minute, bunchedPair()[:1]...)
	h.check(bunchClock.Add(4 * time.Minute))
	receiver.waitFor(t, 2)

	posts := receiver.payloads(t)
	assert.Equal(t, alertRaised, posts[0]["event"])
	assert.Equal(t, alertBunched, posts[0]["kind"])
	assert.Equal(t, "bus-F", posts[0]["vehicle_id"])
	assert.Equal(t, "Bus F on route 5 is bunched: 1 min behind Bus L, every 10 min, since 07:20.", posts[0]["text"])
	assert.Equal(t, alertCleared, posts[1]["event"])
	assert.Equal(t, "Cleared: Bus F on route 5, bunched since 07:20.", posts[1]["text"])
}
