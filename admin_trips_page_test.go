package main

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestFormatMinutes(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0 min"},
		{29 * time.Second, "0 min"},
		{31 * time.Second, "1 min"},
		{45 * time.Minute, "45 min"},
		{time.Hour, "1 h"},
		{105 * time.Minute, "1 h 45 min"},
		{26*time.Hour + 5*time.Minute, "26 h 5 min"},
	} {
		assert.Equal(t, tc.want, formatMinutes(tc.d), tc.d.String())
	}
}

func TestFormatDay(t *testing.T) {
	nairobi := time.FixedZone("EAT", 3*60*60)
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

	assert.Equal(t, "17 Nov", formatDay(time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), time.UTC, now))
	assert.Equal(t, "18 Nov", formatDay(time.Date(2026, 11, 17, 21, 30, 0, 0, time.UTC), nairobi, now), "the day is the page's clock's, not UTC's")
	assert.Equal(t, "31 Dec 2025", formatDay(time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC), time.UTC, now), "another year is named")
}

func TestTripsHeadline(t *testing.T) {
	assert.Equal(t, "No trips are running", tripsHeadline(0))
	assert.Equal(t, "1 trip is running", tripsHeadline(1))
	assert.Equal(t, "9 trips are running", tripsHeadline(9))
}

func TestReadableOn(t *testing.T) {
	for hex, want := range map[string]string{
		"FFFFFF": "#000000",
		"000000": "#FFFFFF",
		"0077C0": "#FFFFFF",
		"FDD663": "#000000",
		"e65100": "#000000",
		"1F1F1F": "#FFFFFF",
		"zzzzzz": "#000000",
	} {
		assert.Equal(t, want, readableOn(hex), hex)
	}
}

func TestRouteBadge(t *testing.T) {
	ix := reportFixtureIndex(t)

	assert.Equal(t, tripRoute{ID: "R1", Name: "1", LongName: "Straight", Color: "#0077C0", TextColor: "#FFFFFF"}, routeBadge(ix, "R1"))
	assert.Equal(t, tripRoute{ID: "R9", Name: "R9"}, routeBadge(ix, "R9"), "a route the schedule does not have goes by its id, on the page's grey")
	assert.Equal(t, tripRoute{ID: "R1", Name: "R1"}, routeBadge(nil, "R1"), "no schedule loaded")
}

// TestNewTripRoute_TextReadsOnTheRouteColour: the feed's own text colour is
// not trusted to be readable, and a colour that is not six hex digits never
// reaches the page's style.
func TestNewTripRoute_TextReadsOnTheRouteColour(t *testing.T) {
	pale := newTripRoute(rider.RouteInfo{ID: "R", ShortName: "7", Color: "FDD663", TextColor: "FFFFFF"})
	assert.Equal(t, "#FDD663", pale.Color)
	assert.Equal(t, "#000000", pale.TextColor)

	bad := newTripRoute(rider.RouteInfo{ID: "R", ShortName: "7", Color: "red;background:url(x)"})
	assert.Empty(t, bad.Color)
	assert.Empty(t, bad.TextColor)

	unnamed := newTripRoute(rider.RouteInfo{ID: "R7", LongName: "Thika Road"})
	assert.Equal(t, "Thika Road", unnamed.Name)
	assert.Empty(t, unnamed.LongName)
}

func TestFormatDeviation_Signed(t *testing.T) {
	seconds := func(n int) *int { return &n }

	assert.Equal(t, reportDeviation{Text: "0:20 late", Tone: "on-time", Signed: "+0:20"}, formatDeviation(seconds(20), testReportSettings))
	assert.Equal(t, reportDeviation{Text: "0:40 early", Tone: "on-time", Signed: "-0:40"}, formatDeviation(seconds(-40), testReportSettings))
	assert.Equal(t, reportDeviation{Text: "0:00", Tone: "on-time", Signed: "0:00"}, formatDeviation(seconds(0), testReportSettings))
	assert.Equal(t, reportDeviation{Text: "unknown", Tone: "unknown"}, formatDeviation(nil, testReportSettings))
}

// TestTripsPage_WithSchedule draws the list over the report fixture's
// schedule: the route as a badge in its colours with the trip's headsign, and
// times on the agency's clock.
func TestTripsPage_WithSchedule(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)
	ui := newTestAdminUI(t)
	ui.reports = r
	ui.stats = &fakeAdminStats{trips: 1}
	// 14:57 UTC is 07:57 in Los Angeles.
	start := time.Date(2026, 9, 2, 14, 57, 0, 0, time.UTC)
	end := start.Add(31 * time.Minute)
	ui.trips = &fakeTripLister{trips: []TripSummary{
		{ID: 8, VehicleLabel: "Bus <b>8</b>", DriverName: "Achieng", RouteID: "R1", GtfsTripID: "T1", StartTime: start, Status: "active"},
		{ID: 7, VehicleLabel: "Bus 7", DriverName: "Baraka", RouteID: "R1", GtfsTripID: "T1", StartTime: start, EndTime: &end, Status: "completed"},
	}}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/trips")

	assert.Contains(t, body, `<h2 class="h1">1 trip is running</h2>`)
	assert.Contains(t, body, "Times are America/Los_Angeles time.")
	assert.Contains(t, body, `<span class="route-to"><span class="route" style="background-color: #0077C0; color: #FFFFFF" title="Straight">1</span><span>to North</span></span>`)
	assert.Contains(t, body, `data-label="Started">07:57<span class="sub hide-phone">2 Sep`)
	assert.Contains(t, body, `08:28<span class="sub">31 min</span>`)
	assert.Contains(t, body, `>Running <span class="muted">1</span></a>`, "the status control counts the running trips")
	assert.Contains(t, body, "Bus &lt;b&gt;8&lt;/b&gt;", "a label is escaped")
	assert.NotContains(t, body, "<b>8</b>")
}

func TestTripsPage_StatusLinksKeepTheSearch(t *testing.T) {
	ui := newTestAdminUI(t)
	ui.trips = &fakeTripLister{}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/trips?status=completed&vehicle_id=bus-1&q=route+9")

	assert.Contains(t, body, `<a href="/admin/trips?page=1&amp;q=route&#43;9&amp;vehicle_id=bus-1">All</a>`)
	assert.Contains(t, body, `<a href="/admin/trips?page=1&amp;q=route&#43;9&amp;status=completed&amp;vehicle_id=bus-1" aria-current="true">Completed</a>`)
	assert.Contains(t, body, `<input type="hidden" name="status" value="completed">`, "a new search keeps the status")
	assert.Contains(t, body, `<div class="empty-title">Nothing fits "route 9"</div>`)
	assert.Contains(t, body, `<a class="btn btn-tonal" href="/admin/trips">Clear the search</a>`)
	assert.NotContains(t, body, "No trips yet")
}

// TestTripDetailPage_Tones: early is red and late is blue, as in the driver
// apps, and a stop inside the on-time window is never called late.
func TestTripDetailPage_Tones(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	page := func(id int64) string {
		trip, points := fixtureTrip(t, ix, id)
		mux := newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r)
		return getAdminPage(t, mux, "/admin/trips/"+strconv.FormatInt(id, 10))
	}

	// RE leaves three minutes early and never waits: -2:54, -6:14, -9:46.
	early := page(2)
	assert.Contains(t, early, `<span class="pill pill-early">2:54 early</span>`)
	assert.Contains(t, early, `<span class="pill pill-early">9:46 early at the last stop</span>`)
	assert.Contains(t, early, "<dt>Stops on time</dt><dd>0 of 3 passed</dd>")

	// RL leaves seven minutes late and makes some up: +7:06, +7:06, +3:34.
	late := page(3)
	assert.Contains(t, late, `<span class="pill pill-late">7:06 late</span>`)
	assert.Contains(t, late, `<span class="pill pill-ok">On time at the last stop</span>`)
	assert.Contains(t, late, `<span class="pill pill-ok">On time</span> <span class="muted">&#43;3:34</span>`)
	assert.Contains(t, late, "On time at 1 of the 3 stops passed")

	// RG's phone is silent around ST2.
	silent := page(4)
	require.Contains(t, silent, "Stop ST2")
	assert.Contains(t, silent, `<td class="num"><span class="muted">no report</span></td>`)
	assert.Contains(t, silent, "On time at 2 of the 2 stops passed; 1 has no report")
}

// TestTripDetailPage_WorksWithoutTheScript: the header, the facts, the stops
// and the gaps are the server's; only the replay needs JavaScript, and says
// so by its class.
func TestTripDetailPage_WorksWithoutTheScript(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	trip, points := fixtureTrip(t, ix, 1)
	mux := newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r)

	body := getAdminPage(t, mux, "/admin/trips/1")

	assert.Contains(t, body, `<section class="card js-only" style="overflow:hidden" id="trip-replay" aria-label="Replay">`)
	for _, id := range []string{"trip-header", "trip-facts", "trip-stops", "trip-gaps"} {
		assert.Contains(t, body, `id="`+id+`"`, "the script replaces this section from a fresh copy of the page")
	}
	assert.Contains(t, body, `<input type="range" id="trip-slider" min="0" max="0" step="1" value="0" disabled aria-label="Report shown on the map">`)
	assert.Contains(t, body, `<script src="`+assetURL(t, "vendor/leaflet/leaflet.js")+`" defer></script>`)
}
