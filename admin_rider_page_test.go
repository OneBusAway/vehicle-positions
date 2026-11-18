package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// newRiderPageMux serves the admin UI with source as the rider service,
// rides as the rides store, and ix as the schedule.
func newRiderPageMux(t *testing.T, source riderPageSource, rides RideLister, ix *rider.Index) (*http.ServeMux, *adminUI) {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.rider = source
	ui.rides = rides
	ui.reports = newServiceReporter(&fakeReportStore{}, func() *rider.Index { return ix }, testReportSettings)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux, ui
}

// fakeRiderPageSource answers the page with a fixed status.
type fakeRiderPageSource struct {
	status riderStatusResponse
	err    error
}

func (f fakeRiderPageSource) RiderStatus(context.Context) (riderStatusResponse, error) {
	return f.status, f.err
}

func (f fakeRiderPageSource) TripNow(rider.TripKey) (bool, int) { return false, 0 }

// enabledRiderStatus is a status with rider mode on and the schedule loaded.
func enabledRiderStatus() riderStatusResponse {
	return riderStatusResponse{
		Enabled: true,
		GTFS:    &riderGTFSStatus{Source: "gtfs.zip", LoadedAt: "2026-09-02T07:00:00Z", Trips: 4, Shapes: 2, Timezone: "America/Los_Angeles"},
		Riders:  &riderTierCounts{},
		Rides:   &riderRideCounts{},
	}
}

// recordingRideLister is a RideLister that serves rides as the store does
// and remembers what it was asked for.
type recordingRideLister struct {
	rides []Ride
	err   error
	calls []string
}

func (f *recordingRideLister) ListRides(_ context.Context, status string, limit, offset int) ([]Ride, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s limit=%d offset=%d", status, limit, offset))
	if f.err != nil {
		return nil, f.err
	}
	var out []Ride
	for _, r := range f.rides {
		if r.Status == status {
			out = append(out, r)
		}
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	return out[:min(limit, len(out))], nil
}

// riderRideRows is the page's ride rows, each up to its closing tag.
func riderRideRows(t *testing.T, body string) []string {
	t.Helper()
	var rows []string
	for _, part := range strings.Split(body, `<tr class="ride-row`)[1:] {
		end := strings.Index(part, "</tr>")
		require.GreaterOrEqual(t, end, 0)
		rows = append(rows, part[:end])
	}
	return rows
}

// findRideRow is the one ride row that contains needle.
func findRideRow(t *testing.T, rows []string, needle string) string {
	t.Helper()
	var found []string
	for _, row := range rows {
		if strings.Contains(row, needle) {
			found = append(found, row)
		}
	}
	require.Len(t, found, 1, "one row with %q", needle)
	return found[0]
}

// riderPageSection is the page from the element with id up to the next
// element whose id starts "rider-".
func riderPageSection(t *testing.T, body, id string) string {
	t.Helper()
	marker := `id="` + id + `"`
	start := strings.Index(body, marker)
	require.GreaterOrEqual(t, start, 0, "the page has #%s", id)
	section := body[start:]
	if end := strings.Index(section[len(marker):], `id="rider-`); end >= 0 {
		section = section[:len(marker)+end]
	}
	return section
}

func TestRiderPage_RequiresAdminSession(t *testing.T) {
	mux, _ := newRiderPageMux(t, riderOff{}, &recordingRideLister{}, nil)

	for _, role := range []string{"", "driver"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/rider", nil)
		if role != "" {
			req.AddCookie(cookieFor(t, role))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusSeeOther, w.Code, "role %q", role)
		// Only the path: the login page may carry where to return to.
		to, err := url.Parse(w.Header().Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "/admin/login", to.Path, "role %q", role)
	}

	assert.Contains(t, getAdminPage(t, mux, "/admin/rider"), "Rider mode is off", "an admin gets the page")
}

func TestRiderPage_Off(t *testing.T) {
	rides := &recordingRideLister{rides: []Ride{{ID: "r1", TripID: "T1", Status: "active"}}}
	mux, _ := newRiderPageMux(t, riderOff{}, rides, nil)

	body := getAdminPage(t, mux, "/admin/rider")
	off := riderPageSection(t, body, "rider-off")
	assert.Contains(t, off, "Rider mode is off")
	assert.Contains(t, off, "RIDER_MODE_ENABLED=true")
	assert.Contains(t, off, "TRUSTED_GTFS_RT_URLS")
	assert.Contains(t, off, `href="https://github.com/OneBusAway/vehicle-positions/blob/main/docs/configuration.md#rider-mode"`)
	assert.NotContains(t, body, `id="rider-rides"`)
	assert.NotContains(t, body, `id="rider-schedule"`)
	assert.Empty(t, rides.calls, "rides are not listed while rider mode is off")
}

func TestRiderPage_OnWithNoRides(t *testing.T) {
	env := newRiderTestEnv(t)
	mux, _ := newRiderPageMux(t, env.svc, env.store, env.index)

	body := getAdminPage(t, mux, "/admin/rider")
	assert.NotContains(t, body, `id="rider-off"`)
	assert.Contains(t, body, "Rider mode is on")

	schedule := riderPageSection(t, body, "rider-schedule")
	assert.Contains(t, schedule, "rider/testdata/fixture.zip")
	zone := time.Now().In(env.index.Timezone()).Format("MST")
	assert.Contains(t, schedule, zone+", just now", "loaded, on the agency's clock")
	assert.Contains(t, schedule, `<dd class="mt-1 tabular-nums text-slate-700">4</dd>`, "the schedule's trips")
	assert.Contains(t, schedule, "America/Los_Angeles")

	assert.Contains(t, riderPageSection(t, body, "rider-trusted-feeds"), "No trusted feed is set")
	assert.Contains(t, body, `id="rider-total" class="display-font mt-2 text-4xl font-bold text-slate-900">0<`)
	assert.Contains(t, body, `id="rider-active" class="display-font mt-2 text-4xl font-bold text-sky-700">0<`)
	assert.Contains(t, body, "No rides in progress.")
	assert.Empty(t, riderRideRows(t, body))
	assert.Contains(t, body, "Times in America/Los_Angeles.")

	assert.Contains(t, getAdminPage(t, mux, "/admin/rider?status=ended"), "No ended rides.")
}

func TestRiderPage_ScheduleNotLoaded(t *testing.T) {
	status := enabledRiderStatus()
	status.GTFS = nil
	mux, _ := newRiderPageMux(t, fakeRiderPageSource{status: status}, &recordingRideLister{}, nil)

	body := getAdminPage(t, mux, "/admin/rider")
	assert.Contains(t, riderPageSection(t, body, "rider-schedule"), "The schedule has not loaded yet")
	assert.Contains(t, body, "Times in UTC.", "no schedule, no agency clock")
}

func TestRiderPage_OnWithRides(t *testing.T) {
	env := newRiderTestEnv(t)
	trustedID, trustedTok := env.register(t)
	env.store.riders[trustedID].Tier = "trusted"
	_, pendingTok := env.register(t)
	_, endedTok := env.register(t)

	verified := env.startRide(t, trustedTok, map[string]any{"trip_id": "T1"})
	_, code := env.upload(t, trustedTok, verified.RideID, env.walkPoints(0, 5))
	require.Equal(t, http.StatusOK, code)
	env.startRide(t, pendingTok, map[string]any{"trip_id": "T1"})
	arrived := env.startRide(t, endedTok, map[string]any{"trip_id": "T3"})
	require.Equal(t, http.StatusOK, env.do(t, "POST", "/api/v1/rider/rides/"+arrived.RideID+"/end", endedTok, map[string]any{"reason": "arrived"}).Code)

	ended := time.Date(2026, 9, 2, 16, 30, 0, 0, time.UTC)
	env.store.riders["r-rejected"] = &Rider{ID: "r-rejected", Tier: "blocked"}
	env.store.rides["rejected"] = &Ride{ID: "rejected", RiderID: "r-rejected", TripID: "T4", StartDate: "20260902", RouteID: "R1",
		Status: "ended", State: "rejected", EndReason: "off_route", PointsTotal: 9, PointsMatched: 2,
		StartedAt: ended.Add(-20 * time.Minute), EndedAt: &ended}

	body := getAdminPage(t, riderEnvMux(t, env), "/admin/rider")
	assert.Contains(t, body, `id="rider-total" class="display-font mt-2 text-4xl font-bold text-slate-900">4<`)
	assert.Contains(t, body, "1 trusted, 1 blocked")
	assert.Contains(t, body, `id="rider-active" class="display-font mt-2 text-4xl font-bold text-sky-700">2<`)
	assert.Contains(t, body, `id="rider-publishable" class="display-font mt-2 text-4xl font-bold text-emerald-700">1<`)
	assert.Contains(t, body, "Riders on trip")
	assert.NotContains(t, body, ">Ended</th>")

	rows := riderRideRows(t, body)
	require.Len(t, rows, 2, "the two rides in progress")
	for _, row := range rows {
		assert.Contains(t, row, ">T1</span>")
		assert.Contains(t, row, "20260902")
		assert.Contains(t, row, "background-color: #0077C0", "route R1's colour")
		assert.Contains(t, row, `color: #FFFFFF">1</span>`, "route R1's short name")
		assert.Contains(t, row, `<span class="tabular-nums text-slate-700">1</span>`, "one rider counts towards T1: the verified one")
		assert.Contains(t, row, "In the feed")
		assert.NotContains(t, row, "/admin/trips/", "no driver is on T1")
	}
	verifiedRow := findRideRow(t, rows, "Verified")
	assert.Contains(t, verifiedRow, "5 of 5")
	assert.Contains(t, verifiedRow, ">No</span>", "no trusted feed confirmed it")
	pendingRow := findRideRow(t, rows, "Pending")
	assert.Contains(t, pendingRow, "0 of 0")

	body = getAdminPage(t, riderEnvMux(t, env), "/admin/rider?status=ended")
	assert.Contains(t, body, ">Ended</th>")
	assert.NotContains(t, body, "Riders on trip")
	rows = riderRideRows(t, body)
	require.Len(t, rows, 2, "the two ended rides")
	arrivedRow := findRideRow(t, rows, ">T3</span>")
	assert.Contains(t, arrivedRow, "Pending")
	assert.Contains(t, arrivedRow, ">arrived</p>")
	assert.Contains(t, arrivedRow, `bg-slate-100 px-2.5 py-1 text-xs font-semibold text-slate-600">2</span>`, "route R2 has no colour, so the page's own")
	rejectedRow := findRideRow(t, rows, ">T4</span>")
	assert.Contains(t, rejectedRow, "Rejected")
	assert.Contains(t, rejectedRow, ">off route</p>")
	assert.Contains(t, rejectedRow, "2 of 9")
	assert.Contains(t, rejectedRow, "2026-09-02 09:10 PDT", "started, on the agency's clock")
	assert.Contains(t, rejectedRow, "2026-09-02 09:30 PDT", "ended, on the agency's clock")
	assert.NotContains(t, rejectedRow, "In the feed")
}

// riderEnvMux serves env's rider service and store on the admin UI.
func riderEnvMux(t *testing.T, env *riderTestEnv) *http.ServeMux {
	t.Helper()
	m, _ := newRiderPageMux(t, env.svc, env.store, env.index)
	return m
}

func TestRiderPage_ConfirmedByTrustedFeed(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	trip, _ := env.index.Trip("T1")
	pts := env.walkPoints(0, 12)
	key := rider.TripKey{TripID: "T1", StartDate: "20260902"}
	env.trusted.set(key, rider.TrustedVehicle{VehicleID: "agency-7", Pos: trip.Shape.PointAt(60), Timestamp: env.now})
	_, code := env.upload(t, tok, ride.RideID, pts)
	require.Equal(t, http.StatusOK, code)

	row := findRideRow(t, riderRideRows(t, getAdminPage(t, riderEnvMux(t, env), "/admin/rider")), ">T1</span>")
	assert.Contains(t, row, "Confirmed")
	assert.NotContains(t, row, "In the feed", "the trusted feed speaks for T1, so the riders' position is left out")
	assert.Contains(t, row, `<span class="tabular-nums text-slate-700">1</span>`, "the rider still counts")
}

func TestRiderPage_TrustedFeeds(t *testing.T) {
	env := newRiderTestEnv(t)
	env.trusted.health = []rider.FeedHealth{
		{URL: "https://agency.example/working", LastSuccess: time.Now().Add(-90 * time.Second), Entities: 12},
		{URL: "https://agency.example/failing", LastSuccess: time.Now().Add(-time.Hour), LastError: "status 503", Entities: 4},
		{URL: "https://agency.example/new"},
	}

	feeds := riderPageSection(t, getAdminPage(t, riderEnvMux(t, env), "/admin/rider"), "rider-trusted-feeds")
	assert.Contains(t, feeds, "Your own drivers' positions always count as trusted.")
	rows := strings.Split(feeds, "<tr>")[1:]
	require.Len(t, rows, 3)
	row := func(url string) string {
		t.Helper()
		for _, r := range rows {
			if strings.Contains(r, url) {
				return r
			}
		}
		t.Fatalf("no row for %s", url)
		return ""
	}
	working := row("https://agency.example/working")
	assert.Contains(t, working, "Working")
	assert.Contains(t, working, "1 min ago")
	assert.Contains(t, working, ">12</td>")
	failing := row("https://agency.example/failing")
	assert.Contains(t, failing, "Failing")
	assert.Contains(t, failing, "status 503")
	assert.Contains(t, failing, "1 h ago", "when it last worked")
	waiting := row("https://agency.example/new")
	assert.Contains(t, waiting, "Not polled yet")
	assert.Contains(t, waiting, "Never")
}

func TestRiderPage_Paging(t *testing.T) {
	var rides []Ride
	start := time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)
	for i := 0; i < adminPageSize+1; i++ {
		end := start.Add(time.Duration(i) * time.Minute)
		rides = append(rides, Ride{ID: fmt.Sprintf("r%d", i), TripID: fmt.Sprintf("trip-%03d", i), Status: "ended", State: "verified", StartedAt: end.Add(-time.Minute), EndedAt: &end})
	}
	store := &recordingRideLister{rides: rides}
	mux, _ := newRiderPageMux(t, fakeRiderPageSource{status: enabledRiderStatus()}, store, nil)

	body := getAdminPage(t, mux, "/admin/rider?status=ended")
	assert.Len(t, riderRideRows(t, body), adminPageSize)
	assert.Contains(t, body, `href="/admin/rider?page=2&amp;status=ended"`, "next keeps the filter")
	assert.NotContains(t, body, "Previous")
	assert.NotContains(t, body, "trip-050")

	body = getAdminPage(t, mux, "/admin/rider?status=ended&page=2")
	page2 := riderRideRows(t, body)
	require.Len(t, page2, 1)
	assert.Contains(t, page2[0], "trip-050")
	assert.Contains(t, body, `href="/admin/rider?page=1&amp;status=ended"`)
	assert.NotContains(t, body, ">Next<")

	assert.Equal(t, []string{
		fmt.Sprintf("ended limit=%d offset=0", adminPageSize+1),
		fmt.Sprintf("ended limit=%d offset=%d", adminPageSize+1, adminPageSize),
	}, store.calls)
}

func TestRiderPage_DefaultsToRidesInProgress(t *testing.T) {
	store := &recordingRideLister{}
	mux, _ := newRiderPageMux(t, fakeRiderPageSource{status: enabledRiderStatus()}, store, nil)

	body := getAdminPage(t, mux, "/admin/rider")
	assert.Contains(t, body, `<option value="active" selected>`)
	assert.Equal(t, []string{fmt.Sprintf("active limit=%d offset=0", adminPageSize+1)}, store.calls)
}

func TestRiderPage_DriverTripLink(t *testing.T) {
	now := time.Now()
	store := &recordingRideLister{rides: []Ride{
		{ID: "a", TripID: "T1", StartDate: "20260902", Status: "active", State: "verified", StartedAt: now},
		{ID: "b", TripID: "T3", StartDate: "20260902", Status: "active", State: "verified", StartedAt: now},
		{ID: "c", TripID: "T4", StartDate: "20260902", Status: "active", State: "pending", StartedAt: now},
		{ID: "d", TripID: "T1", StartDate: "20260901", Status: "ended", State: "verified", StartedAt: now, EndedAt: &now},
	}}
	mux, ui := newRiderPageMux(t, fakeRiderPageSource{status: enabledRiderStatus()}, store, nil)
	ui.activeTrips = &fakeActiveTrips{m: map[string]ActiveTripInfo{
		"veh-1": {TripID: 42, GtfsTripID: "T1"},
		"veh-2": {TripID: 43, GtfsTripID: "T3"},
		"veh-3": {TripID: 44, GtfsTripID: "T3"},
	}}

	rows := riderRideRows(t, getAdminPage(t, mux, "/admin/rider"))
	require.Len(t, rows, 3)
	assert.Contains(t, findRideRow(t, rows, ">T1</span>"), `<a href="/admin/trips/42"`, "one driver runs T1")
	assert.NotContains(t, findRideRow(t, rows, ">T3</span>"), "/admin/trips/", "two drivers run T3, so the rider could be on either")
	assert.NotContains(t, findRideRow(t, rows, ">T4</span>"), "/admin/trips/", "no driver runs T4")

	ended := riderRideRows(t, getAdminPage(t, mux, "/admin/rider?status=ended"))
	require.Len(t, ended, 1)
	assert.NotContains(t, ended[0], "/admin/trips/", "an ended ride's run is not the one on the road now")
}

func TestRiderPage_EscapesRiderText(t *testing.T) {
	now := time.Now()
	status := enabledRiderStatus()
	status.GTFS.Source = `<script>alert("source")</script>`
	status.TrustedFeeds = []riderFeedStatus{{URL: `https://feed.example/?q=<script>alert("url")</script>`, LastError: `<img src=x onerror=alert("error")>`}}
	store := &recordingRideLister{rides: []Ride{
		{ID: "a", TripID: `<script>alert("trip")</script>`, StartDate: `<b>date</b>`, RouteID: `"><img src=x onerror=alert("route")>`, Status: "ended", State: `<i>state</i>`, EndReason: `<b>reason</b>`, StartedAt: now, EndedAt: &now},
	}}
	mux, _ := newRiderPageMux(t, fakeRiderPageSource{status: status}, store, nil)

	body := getAdminPage(t, mux, "/admin/rider?status=ended")
	for _, raw := range []string{`<script>alert(`, `<img src=x`, `<b>date</b>`, `<b>reason</b>`, `<i>state</i>`} {
		assert.NotContains(t, body, raw)
	}
	for _, escaped := range []string{
		`&lt;script&gt;alert(&#34;trip&#34;)&lt;/script&gt;`,
		`&lt;script&gt;alert(&#34;source&#34;)&lt;/script&gt;`,
		`&lt;script&gt;alert(&#34;url&#34;)&lt;/script&gt;`,
		`&lt;img src=x onerror=alert(&#34;error&#34;)&gt;`,
		`&#34;&gt;&lt;img src=x onerror=alert(&#34;route&#34;)&gt;`,
		`&lt;b&gt;date&lt;/b&gt;`,
		`&lt;b&gt;reason&lt;/b&gt;`,
		`&lt;i&gt;state&lt;/i&gt;`,
	} {
		assert.Contains(t, body, escaped)
	}
}

func TestRiderPage_Errors(t *testing.T) {
	now := time.Now()
	active := []Ride{{ID: "a", TripID: "T1", Status: "active", State: "pending", StartedAt: now}}
	tests := []struct {
		name       string
		source     riderPageSource
		rides      RideLister
		trips      ActiveTripLister
		path       string
		wantStatus int
		wantBody   string
	}{
		{"status fails", fakeRiderPageSource{err: assert.AnError}, &recordingRideLister{}, &fakeActiveTrips{}, "/admin/rider", http.StatusInternalServerError, "internal server error"},
		{"unknown filter", fakeRiderPageSource{status: enabledRiderStatus()}, &recordingRideLister{}, &fakeActiveTrips{}, "/admin/rider?status=pending", http.StatusBadRequest, "status must be active or ended"},
		{"rides fail", fakeRiderPageSource{status: enabledRiderStatus()}, &recordingRideLister{err: assert.AnError}, &fakeActiveTrips{}, "/admin/rider", http.StatusInternalServerError, "internal server error"},
		{"driver trips fail", fakeRiderPageSource{status: enabledRiderStatus()}, &recordingRideLister{rides: active}, &errActiveTrips{}, "/admin/rider", http.StatusInternalServerError, "internal server error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux, ui := newRiderPageMux(t, tc.source, tc.rides, nil)
			ui.activeTrips = tc.trips
			w := requestAdminPage(t, mux, tc.path)
			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Contains(t, w.Body.String(), tc.wantBody)
		})
	}
}

func TestRiderPage_SidebarEntry(t *testing.T) {
	mux, _ := newRiderPageMux(t, riderOff{}, &recordingRideLister{}, nil)

	assert.Equal(t, railLinked, railLink(getAdminPage(t, mux, "/admin/health"), "/admin/rider"), "every admin page links to Rider mode, on or off")
	assert.Equal(t, railOpen, railLink(getAdminPage(t, mux, "/admin/rider"), "/admin/rider"), "the entry is marked on its own page")
}

// TestRiderPage_ServedFromTheRiderService drives the page through newHandler,
// so it reads the rider service main hands the router, and riderOff without
// one.
func TestRiderPage_ServedFromTheRiderService(t *testing.T) {
	env := newRiderTestEnv(t)
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return env.index }, testReportSettings)
	build := func(svc *riderService) http.Handler {
		h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, svc, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), reports, newFeedHealth(time.Now))
		require.NoError(t, err)
		return h
	}
	get := func(h http.Handler) string {
		req := httptest.NewRequest(http.MethodGet, "/admin/rider", nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	assert.Contains(t, get(build(env.svc)), "Rider mode is on")
	assert.Contains(t, get(build(nil)), "Rider mode is off")
}
