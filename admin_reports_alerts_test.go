package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// fakeAlertsRaised answers ListAlertsRaisedBetween with alerts, after
// noting the period it was asked for.
type fakeAlertsRaised struct {
	alerts   []raisedAlert
	err      error
	from, to time.Time
	calls    int
}

func (f *fakeAlertsRaised) ListAlertsRaisedBetween(_ context.Context, from, to time.Time) ([]raisedAlert, error) {
	f.calls++
	f.from, f.to = from, to
	return f.alerts, f.err
}

// laTime is a time on the report fixture's clock, America/Los_Angeles.
func laTime(t *testing.T, ix *rider.Index, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, month, day, hour, minute, 0, 0, ix.Timezone())
}

func raised(kind, routeID string, at time.Time, lasted time.Duration) raisedAlert {
	cleared := at.Add(lasted)
	return raisedAlert{Kind: kind, RouteID: routeID, RaisedAt: at, ClearedAt: &cleared}
}

func stillOpen(kind, routeID string, at time.Time) raisedAlert {
	return raisedAlert{Kind: kind, RouteID: routeID, RaisedAt: at}
}

// fixtureDays are the service dates 1 to 3 September 2026.
var fixtureDays = []time.Time{
	time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
}

func TestNewAnalyticsAlerts_CountsEachDayByKind(t *testing.T) {
	ix := reportFixtureIndex(t)
	alerts := []raisedAlert{
		raised(alertQuiet, "R1", laTime(t, ix, 9, 1, 10, 0), time.Minute),
		// Before 03:00, still the day before's service.
		raised(alertQuiet, "R1", laTime(t, ix, 9, 2, 2, 59), time.Minute),
		raised(alertOffRoute, "R2", laTime(t, ix, 9, 2, 3, 0), time.Minute),
		raised(alertNotMoving, "R1", laTime(t, ix, 9, 3, 8, 0), time.Minute),
		stillOpen(alertNotMoving, "R2", laTime(t, ix, 9, 3, 9, 0)),
	}

	got := newAnalyticsAlerts(alerts, ix, fixtureDays)

	assert.Equal(t, 5, got.Raised)
	require.Len(t, got.Days, 3)
	for i, want := range []struct {
		day    string
		counts []int
		raised int
	}{
		// In the order of alertKinds: quiet, not moving, off route, not started.
		{"Tue 1 Sep", []int{2, 0, 0, 0}, 2},
		{"Wed 2 Sep", []int{0, 0, 1, 0}, 1},
		{"Thu 3 Sep", []int{0, 2, 0, 0}, 2},
	} {
		assert.Equal(t, want.day, got.Days[i].Day)
		assert.Equal(t, want.counts, got.Days[i].Counts, want.day)
		assert.Equal(t, want.raised, got.Days[i].Raised, want.day)
	}

	require.Len(t, got.Kinds, len(alertKinds))
	for i, want := range []struct {
		title  string
		url    string
		raised int
		tips   []string
	}{
		{"Gone quiet", "/admin/alerts?kind=quiet#cleared", 2, []string{"1 Sep|2 raised", "2 Sep|0 raised", "3 Sep|0 raised"}},
		{"Not moving", "/admin/alerts?kind=not_moving#cleared", 2, []string{"1 Sep|0 raised", "2 Sep|0 raised", "3 Sep|2 raised"}},
		{"Off route", "/admin/alerts?kind=off_route#cleared", 1, []string{"1 Sep|0 raised", "2 Sep|1 raised", "3 Sep|0 raised"}},
		{"Not started", "/admin/alerts?kind=not_started#cleared", 0, []string{"1 Sep|0 raised", "2 Sep|0 raised", "3 Sep|0 raised"}},
	} {
		kind := got.Kinds[i]
		assert.Equal(t, want.title, kind.Title)
		assert.Equal(t, want.url, kind.URL)
		assert.Equal(t, want.raised, kind.Raised, want.title)
		require.NotNil(t, kind.Chart, want.title)
		tips := make([]string, 0, len(kind.Chart.Bars))
		for _, bar := range kind.Chart.Bars {
			tips = append(tips, bar.Tip)
		}
		assert.Equal(t, want.tips, tips, want.title)
	}
}

// TestNewAnalyticsAlerts_OneDayHasNoCharts: a day is one bar a kind, which
// says nothing the count does not.
func TestNewAnalyticsAlerts_OneDayHasNoCharts(t *testing.T) {
	ix := reportFixtureIndex(t)
	got := newAnalyticsAlerts([]raisedAlert{raised(alertQuiet, "R1", laTime(t, ix, 9, 2, 10, 0), time.Minute)}, ix, fixtureDays[1:2])
	assert.Equal(t, 1, got.Raised)
	assert.Nil(t, got.Days, "no table of one day")
	for _, kind := range got.Kinds {
		assert.Nil(t, kind.Chart, kind.Title)
	}
	assert.Equal(t, 1, got.Kinds[0].Raised)
}

func TestNewAnalyticsAlerts_TimeToClear(t *testing.T) {
	ix := reportFixtureIndex(t)
	at := laTime(t, ix, 9, 2, 10, 0)
	for _, tc := range []struct {
		name            string
		alerts          []raisedAlert
		median, longest string
		cleared, open   int
	}{
		{
			name: "an even count takes the middle two's mean, and an open alert is left out",
			alerts: []raisedAlert{
				raised(alertQuiet, "R1", at, 5*time.Minute),
				raised(alertQuiet, "R1", at, 30*time.Minute),
				stillOpen(alertQuiet, "R1", at),
				raised(alertOffRoute, "R1", at, 10*time.Minute),
				raised(alertNotStarted, "R2", at, 2*time.Hour+5*time.Minute),
			},
			median: "20 min", longest: "2 h 5 min", cleared: 4, open: 1,
		},
		{
			name: "an odd count takes the middle one",
			alerts: []raisedAlert{
				raised(alertQuiet, "R1", at, 7*time.Minute),
				raised(alertQuiet, "R1", at, 3*time.Minute),
				raised(alertQuiet, "R1", at, 50*time.Minute),
			},
			median: "7 min", longest: "50 min", cleared: 3,
		},
		{
			name:   "under a minute is said so, not rounded to none",
			alerts: []raisedAlert{raised(alertQuiet, "R1", at, 20*time.Second)},
			median: "under 1 min", longest: "under 1 min", cleared: 1,
		},
		{
			name:   "none cleared has no times",
			alerts: []raisedAlert{stillOpen(alertQuiet, "R1", at)},
			open:   1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newAnalyticsAlerts(tc.alerts, ix, fixtureDays)
			assert.Equal(t, tc.median, got.MedianClear)
			assert.Equal(t, tc.longest, got.LongestClear)
			assert.Equal(t, tc.cleared, got.Cleared)
			assert.Equal(t, tc.open, got.Open)
		})
	}
}

func TestNewAnalyticsAlerts_TopRoutes(t *testing.T) {
	ix := reportFixtureIndex(t)
	at := laTime(t, ix, 9, 2, 10, 0)
	var alerts []raisedAlert
	add := func(routeID, kind string, n int) {
		for range n {
			alerts = append(alerts, stillOpen(kind, routeID, at))
		}
	}
	add("R2", alertQuiet, 1)
	add("R1", alertQuiet, 2)
	add("R1", alertOffRoute, 1)
	add("R3", alertNotStarted, 3)
	add("R9", alertNotMoving, 2)
	add("", alertQuiet, 1)
	add("R8", alertQuiet, 1)

	got := newAnalyticsAlerts(alerts, ix, fixtureDays)

	require.Len(t, got.Routes, analyticsAlertRoutes)
	type row struct {
		badge, name string
		raised      int
		kinds       string
	}
	rows := make([]row, 0, len(got.Routes))
	for _, r := range got.Routes {
		rows = append(rows, row{r.Badge.Name, r.Name, r.Raised, r.Kinds})
	}
	assert.Equal(t, []row{
		// Most first; a tie goes by route ID.
		{"1", "Straight", 3, "2 gone quiet, 1 off route"},
		{"3", "Out and back", 3, "3 not started"},
		{"R9", "Route R9", 2, "2 not moving"},
		{"", "No route", 1, "1 gone quiet"},
		{"2", "Loop", 1, "1 gone quiet"},
	}, rows, "the five with the most; R8 is left out")
	assert.Equal(t, "0077C0", got.Routes[0].Badge.Color[1:], "the schedule's badge")
}

func TestNewAnalyticsAlerts_EmptyPeriod(t *testing.T) {
	ix := reportFixtureIndex(t)
	got := newAnalyticsAlerts(nil, ix, fixtureDays)
	assert.Zero(t, got.Raised)
	assert.Zero(t, got.Open)
	assert.Empty(t, got.MedianClear)
	assert.Empty(t, got.LongestClear)
	assert.NotNil(t, got.Routes)
	assert.Empty(t, got.Routes)
	require.Len(t, got.Days, 3)
	assert.Equal(t, []int{0, 0, 0, 0}, got.Days[0].Counts)
	require.Len(t, got.Kinds, len(alertKinds))
	for _, kind := range got.Kinds {
		assert.Zero(t, kind.Raised)
	}
}

// TestAddAnalyticsAlerts_ReadsThePeriod: the store is asked for the alerts
// raised from 03:00 on the period's first day to 03:00 after its last, the
// instants ServiceDate turns the day at, and for no others.
func TestAddAnalyticsAlerts_ReadsThePeriod(t *testing.T) {
	ix := reportFixtureIndex(t)
	for _, tc := range []struct {
		name     string
		query    analyticsQuery
		from, to time.Time
	}{
		{"7 days", analyticsQuery{Date: "2026-09-02", Days: 7}, laTime(t, ix, 8, 27, 3, 0), laTime(t, ix, 9, 3, 3, 0)},
		{"28 days", analyticsQuery{Date: "2026-09-02", Days: 28}, laTime(t, ix, 8, 6, 3, 0), laTime(t, ix, 9, 3, 3, 0)},
		{"a day", analyticsQuery{Date: "2026-09-02", Days: 1}, laTime(t, ix, 9, 2, 3, 0), laTime(t, ix, 9, 3, 3, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAlertsRaised{alerts: []raisedAlert{stillOpen(alertQuiet, "R1", laTime(t, ix, 9, 2, 10, 0))}}
			data := map[string]interface{}{}
			require.NoError(t, addAnalyticsAlerts(context.Background(), store, ix, tc.query, data))
			assert.Equal(t, 1, store.calls)
			assert.True(t, tc.from.Equal(store.from), "from %v, want %v", store.from, tc.from)
			assert.True(t, tc.to.Equal(store.to), "to %v, want %v", store.to, tc.to)
			section, ok := data["AlertsRaised"].(analyticsAlerts)
			require.True(t, ok)
			assert.Equal(t, 1, section.Raised)
			if tc.query.Days > 1 {
				assert.Len(t, section.Days, tc.query.Days)
			}
		})
	}
}

func TestAddAnalyticsAlerts_NotForARoute(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := &fakeAlertsRaised{}
	data := map[string]interface{}{}
	require.NoError(t, addAnalyticsAlerts(context.Background(), store, ix, analyticsQuery{Date: "2026-09-02", Days: 1, RouteID: "R1"}, data))
	assert.Zero(t, store.calls, "a route's day lists its trips, not alerts")
	assert.NotContains(t, data, "AlertsRaised")
}

// TestNewAnalyticsAlerts_LeavesOutAnUnknownKind: a kind this server does not
// know, from a newer one sharing the database, is counted nowhere.
func TestNewAnalyticsAlerts_LeavesOutAnUnknownKind(t *testing.T) {
	ix := reportFixtureIndex(t)
	got := newAnalyticsAlerts([]raisedAlert{
		stillOpen("speeding", "R1", laTime(t, ix, 9, 2, 10, 0)),
		stillOpen(alertQuiet, "R1", laTime(t, ix, 9, 2, 10, 0)),
	}, ix, fixtureDays)
	assert.Equal(t, 1, got.Raised)
	assert.Equal(t, 1, got.Open)
	require.Len(t, got.Routes, 1)
	assert.Equal(t, 1, got.Routes[0].Raised)
	assert.Equal(t, 1, got.Days[1].Raised)
}

func TestAddAnalyticsAlerts_NoSchedule(t *testing.T) {
	store := &fakeAlertsRaised{}
	data := map[string]interface{}{}
	require.NoError(t, addAnalyticsAlerts(context.Background(), store, nil, analyticsQuery{Date: "2026-09-02", Days: 7}, data))
	assert.Zero(t, store.calls, "with no schedule there are no service days to read")
	assert.NotContains(t, data, "AlertsRaised")
}

func TestAddAnalyticsAlerts_StoreError(t *testing.T) {
	ix := reportFixtureIndex(t)
	boom := errors.New("boom")
	err := addAnalyticsAlerts(context.Background(), &fakeAlertsRaised{err: boom}, ix, analyticsQuery{Date: "2026-09-02", Days: 7}, map[string]interface{}{})
	assert.ErrorIs(t, err, boom)
}

func TestReportsContent_AlertsSection(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	data, status := reportsView(t, r, "date=2026-09-02")
	require.Equal(t, http.StatusOK, status)
	store := &fakeAlertsRaised{alerts: []raisedAlert{
		raised(alertQuiet, "R1", laTime(t, ix, 9, 1, 10, 0), 12*time.Minute),
		raised(alertOffRoute, "R1", laTime(t, ix, 9, 2, 10, 0), 40*time.Minute),
		stillOpen(alertNotStarted, "R2", laTime(t, ix, 9, 2, 11, 0)),
	}}
	require.NoError(t, addAnalyticsAlerts(context.Background(), store, ix, analyticsQuery{Date: "2026-09-02", Days: 7}, data))

	body := renderReportsContent(t, data)
	assert.Contains(t, body, `<h3 id="an-alerts" style="flex:1">Alerts</h3>`)
	assert.Contains(t, body, `href="/admin/alerts#cleared"`, "the section links to the Alerts page")
	assert.Contains(t, body, `href="/admin/alerts?kind=off_route#cleared"`, "and each kind to its cleared alerts")
	assert.Contains(t, body, "26 min", "the median time to clear")
	assert.Contains(t, body, "40 min", "the longest")
	assert.Contains(t, body, "1 still open")
	assert.Contains(t, body, "Straight")
	assert.Contains(t, body, "1 gone quiet, 1 off route")
	assert.Contains(t, body, `data-tip="1 Sep|1 raised"`, "a kind's chart has a bar a day")
	assert.Contains(t, body, "Show these numbers as a table")

	empty := map[string]interface{}{}
	for k, v := range data {
		empty[k] = v
	}
	require.NoError(t, addAnalyticsAlerts(context.Background(), &fakeAlertsRaised{}, ix, analyticsQuery{Date: "2026-09-02", Days: 7}, empty))
	body = renderReportsContent(t, empty)
	assert.Contains(t, body, "No alert was raised on these days.")
	assert.NotContains(t, body, "still open")
}

// TestReportsPage_ShowsAlerts: the page reads the alerts through the admin
// UI's store, and a failed read is the page's error.
func TestReportsPage_ShowsAlerts(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	ui := newTestAdminUI(t)
	ui.reports = r
	store := &fakeAlertsRaised{alerts: []raisedAlert{raised(alertQuiet, "R1", laTime(t, ix, 9, 2, 10, 0), 12*time.Minute)}}
	ui.alertsRaised = store
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	admin := cookieFor(t, "admin")

	rec := serve(mux, "/admin/reports?date=2026-09-02", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, store.calls)
	assert.Contains(t, rec.Body.String(), `id="an-alerts"`)

	rec = serve(mux, "/admin/reports?date=2026-09-02&route_id=R1", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, store.calls, "not read for a route's day")
	assert.NotContains(t, rec.Body.String(), `id="an-alerts"`)

	store.err = errors.New("boom")
	rec = serve(mux, "/admin/reports?date=2026-09-02", admin)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
