package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// routeRunsOf is the cards of a route's day with frequent service.
func routeRunsOf(t *testing.T, data map[string]interface{}) *reportRouteRuns {
	t.Helper()
	runs, ok := data["RouteRuns"].(*reportRouteRuns)
	require.True(t, ok, "the route's day lists no runs")
	return runs
}

// runLines are a direction's runs, a line each: when it left, its trip and
// vehicle, the gap before it and the stops it was bunched at.
func runLines(dir reportRunDirection) []string {
	lines := make([]string, 0, len(dir.Runs))
	for _, r := range dir.Runs {
		line := r.Left + " " + r.GtfsTripID + " " + r.Vehicle
		if r.Gap.Text != "" {
			line += ": " + r.Gap.Text
		}
		if r.Gap.Every != "" {
			line += ", " + r.Gap.Every
		}
		if r.Gap.Tone != "" {
			line += " (" + r.Gap.Tone + ")"
		}
		if r.BunchedAt != "" {
			line += "; bunched at " + r.BunchedAt
		}
		lines = append(lines, line)
	}
	return lines
}

// TestReportsPage_HeadwayRouteRuns: route 5's day, run from frequencies.txt
// both ways (gapDayRuns), lists each direction's runs in the order they left
// the first stop, each with the gap to the run before it there. C is bunched
// behind B at ST1 only, since D overtakes it before ST2, and D behind C at
// ST1 and behind B at ST2. H turned short before ST2, so it is judged at ST1
// alone, and F's phone was silent leaving ST2, so its gap there is not
// judged, nor is G's after it. W ran GX, a timetabled trip with no other
// within 12 minutes, so it is listed with the timetabled trips alone. South,
// N sent nothing, so when it left is not known, and O's gaps are unknown.
// The stops judged add up to the day's 13 and 4 gaps judged, and the
// stops bunched to its 3 and 2 (TestServiceReporter_HeadwayGaps).
func TestReportsPage_HeadwayRouteRuns(t *testing.T) {
	r, _, _ := newGapReporter(t)

	data, status := reportsView(t, r, "date=2026-09-02&route_id=R5")
	require.Equal(t, 200, status)
	assert.Equal(t, &gapTiles{
		Known: true, Bunched: 29, Long: 12,
		BunchedNote: "5 gaps under 25% of the scheduled gap",
		LongNote:    "2 gaps over 150% of the scheduled gap",
	}, data["Tiles"].(analyticsTiles).Gaps, "the route's gaps, both ways")
	trips := data["Trips"].([]reportTripRow)
	require.Len(t, trips, 1)
	assert.Equal(t, "GX", trips[0].GtfsTripID, "the timetabled trip alone")
	assert.Equal(t, true, data["HasFrequencies"])

	runs := routeRunsOf(t, data)
	assert.Equal(t, [3]string{"25%", "150%", "12 min"}, [3]string{runs.BunchedBelow, runs.LongAbove, runs.FrequentSpacing})
	require.Len(t, runs.Directions, 2)
	north, south := runs.Directions[0], runs.Directions[1]
	assert.Equal(t, "Runs to North", north.Title)
	assert.False(t, north.Last)
	assert.Equal(t, []reportHeadwayTrip{
		{GtfsTripID: "GN", Scheduled: "every 10 min, 07:00 to 08:00, then every 10 min, 19:00 to 20:00, then every 30 min, 23:30 to 00:30 next day", RunCount: "10 of 14 runs"},
		{GtfsTripID: "GT", Scheduled: "every 10 min, 19:05 to 20:05", RunCount: "0 of 6 runs"},
	}, north.Schedules)
	assert.Equal(t, []string{
		"07:00:06 GN Bus A: no gap",
		"07:10:06 GN Bus B: 10 min after Bus A, every 10 min; bunched at 0 of 2 stops",
		"07:12:06 GN Bus C: 2 min after Bus B, every 10 min (bunched); bunched at 1 of 2 stops",
		"07:14:06 GN Bus D: 2 min after Bus C, every 10 min (bunched); bunched at 2 of 2 stops",
		"07:30:06 GN Bus H: 16 min after Bus D, every 10 min (long); bunched at 0 of 1 stop",
		"07:40:06 GN Bus E: 10 min after Bus H, every 10 min; bunched at 0 of 2 stops",
		"07:50:06 GN Bus F: 10 min after Bus E, every 10 min; bunched at 0 of 1 stop",
		"08:05:06 GN Bus G: 15 min after Bus F, every 10 min; bunched at 0 of 1 stop",
		"23:58:06 GN Bus I: no gap",
		"00:31:06 next day GN Bus J: 33 min after Bus I, every 30 min; bunched at 0 of 2 stops",
	}, runLines(north))
	assert.Equal(t, reportRunGapRow{
		GtfsTripID: "GN", Left: "07:12:06", LeftKnown: true, Vehicle: "Bus C", Driver: "Driver C", TrailURL: "/admin/trips/202",
		Gap:       reportRunGap{Text: "2 min after Bus B", Every: "every 10 min", Tone: "bunched"},
		BunchedAt: "1 of 2 stops", Bunched: true,
	}, north.Runs[2])
	assert.False(t, north.Runs[1].Bunched, "judged at two stops, bunched at neither")

	assert.Equal(t, "Runs to South", south.Title)
	assert.True(t, south.Last)
	assert.Equal(t, []reportHeadwayTrip{{GtfsTripID: "GS", Scheduled: "every 15 min, 07:00 to 08:00", RunCount: "4 of 4 runs"}}, south.Schedules)
	assert.Equal(t, []string{
		"07:00:06 GS Bus K: no gap",
		"07:15:06 GS Bus L: 15 min after Bus K, every 15 min; bunched at 0 of 2 stops",
		"07:18:06 GS Bus M: 3 min after Bus L, every 15 min (bunched); bunched at 2 of 2 stops",
		"not known GS Bus N: unknown",
		"07:45:06 GS Bus O: unknown",
	}, runLines(south))
	assert.False(t, south.Runs[3].LeftKnown)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<div class="rise tiles an-tiles an-tiles-6">`)
	assert.Contains(t, html, `<h3 id="an-trips" style="flex:1">Scheduled trips</h3>`)
	assert.Contains(t, html, `<a class="row-link" href="/admin/trips/205">GX</a>`)
	assert.NotContains(t, html, "no timetable")
	assert.Contains(t, html, `<section class="card" aria-labelledby="an-runs-0">`)
	assert.Contains(t, html, `<h3 id="an-runs-0" style="flex:1">Runs to North</h3><span class="sub hide-phone">In the order they left the first stop</span>`)
	assert.Contains(t, html, `<h3 id="an-runs-1" style="flex:1">Runs to South</h3>`)
	assert.Contains(t, html, `<p class="card-note"><span style="display:block">GN runs every 10 min, 07:00 to 08:00, then every 10 min, 19:00 to 20:00, then every 30 min, 23:30 to 00:30 next day: 10 of 14 runs</span><span style="display:block">GT runs every 10 min, 19:05 to 20:05: 0 of 6 runs</span></p>`)
	assert.Contains(t, html, `<th>Left</th><th>Vehicle</th><th>Gap before</th><th class="right">Bunched at</th>`)
	assert.Contains(t, html, `<tr><td class="nowrap"><a class="row-link num" href="/admin/trips/202">07:12:06</a><span class="sub">GN</span></td><td class="nowrap" data-label="Vehicle">Bus C<span class="sub hide-phone">Driver C</span></td><td data-label="Gap before"><span class="pill pill-early">Bunched</span> 2 min after Bus B<span class="sub">every 10 min</span></td><td class="num right c-side nowrap" data-label="Bunched at"><span class="tone-early">1 of 2 stops</span></td><td class="right c-actions"><a class="btn btn-sm btn-plain" href="/admin/trips/202" aria-label="Replay Bus C, 07:12:06">`)
	assert.Contains(t, html, `<td data-label="Gap before"><span class="pill pill-late">Long</span> 16 min after Bus D<span class="sub">every 10 min</span></td><td class="num right c-side nowrap" data-label="Bunched at">0 of 1 stop</td>`)
	assert.Contains(t, html, `<td data-label="Gap before">10 min after Bus A<span class="sub">every 10 min</span></td>`)
	assert.Contains(t, html, `<td data-label="Gap before"><span class="muted">no gap</span></td><td class="num right c-side nowrap" data-label="Bunched at"><span class="muted">no gap judged</span></td>`)
	assert.Contains(t, html, `<a class="row-link" href="/admin/trips/214"><span class="muted">not known</span></a><span class="sub">GS</span>`)
	assert.Contains(t, html, `<td data-label="Gap before"><span class="muted">unknown</span></td>`)
	assert.Contains(t, html, `>00:31:06 next day</a>`)
	assert.Equal(t, 1, strings.Count(html, "A run's gap is the time since the run before it left the same stop"), "said once, under the last direction")
	foot := html[strings.Index(html, "A run's gap"):]
	foot = foot[:strings.Index(foot, "</div>")]
	assert.Equal(t, "A run's gap is the time since the run before it left the same stop, whichever trip that was. "+
		"It is bunched under 25% of the gap scheduled then and long over 150%, and unknown when a vehicle with no report within 2 min of the stop could have left between them. "+
		"The first run of a stretch of service, and a run outside one, has no gap. "+
		"Bunched at counts the stops where the run's gap was bunched, of those it was judged at. "+
		"A trip run from the GTFS frequencies is not judged on time; each run a driver started fills the scheduled run nearest it. "+
		"A timetabled trip is listed where it leaves a stop within 12 min of another, and Scheduled trips judges it on time.", foot)
	assert.Less(t, strings.Index(html, "Scheduled trips"), strings.Index(html, "Runs to North"), "the timetabled trips first, as On time leads the day")

	// The foot says the bands, the fix window and the spacing it is given.
	runs.BunchedBelow, runs.LongAbove, runs.FrequentSpacing, data["FixWindow"] = "20%", "200%", "15 min", "3 min"
	html = renderReportsContent(t, data)
	assert.Contains(t, html, "It is bunched under 20% of the gap scheduled then and long over 200%, and unknown when a vehicle with no report within 3 min of the stop")
	assert.Contains(t, html, "A timetabled trip is listed where it leaves a stop within 15 min of another")
}

// TestReportsPage_TimetabledRouteRuns: a route run by timetable lists the
// runs of its frequent service with their gaps (TestServiceReporter_
// TimetabledGaps), and keeps every trip in Scheduled trips. Route 6's 6A,
// half an hour before the rest, and 6G, 13 minutes after 6F, are not in its
// frequent service. Route 7's runs of 7F, from frequencies.txt, and its
// timetabled trips are in one order.
func TestReportsPage_TimetabledRouteRuns(t *testing.T) {
	r, _ := newTimetableReporter(t)

	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R6")
	assert.Len(t, data["Trips"], 7, "every trip is judged on time")
	assert.Nil(t, data["HasFrequencies"])
	assert.Equal(t, &gapTiles{
		Known: true, Bunched: 25, Long: 25,
		BunchedNote: "2 gaps under 25% of the scheduled gap",
		LongNote:    "2 gaps over 150% of the scheduled gap",
	}, data["Tiles"].(analyticsTiles).Gaps)
	runs := routeRunsOf(t, data)
	require.Len(t, runs.Directions, 1)
	assert.Equal(t, "Runs to North", runs.Directions[0].Title)
	assert.Empty(t, runs.Directions[0].Schedules, "no trip in frequencies.txt")
	assert.True(t, runs.Directions[0].Last)
	assert.Equal(t, []string{
		"07:00:06 6B Bus 6B: no gap",
		"07:12:06 6C Bus 6C: 12 min after Bus 6B, every 10 min; bunched at 0 of 2 stops",
		"07:14:06 6D Bus 6D: 2 min after Bus 6C, every 10 min (bunched); bunched at 2 of 2 stops",
		"07:30:06 6E Bus 6E: 16 min after Bus 6D, every 10 min (long); bunched at 0 of 2 stops",
		"07:43:06 6F Bus 6F: 13 min after Bus 6E, every 12 min; bunched at 0 of 2 stops",
	}, runLines(runs.Directions[0]))
	html := renderReportsContent(t, data)
	assert.NotContains(t, html, `<p class="card-note"><span style="display:block">`, "no schedule to say")
	assert.Contains(t, html, "A timetabled trip is listed where it leaves a stop within 12 min of another, and Scheduled trips judges it on time.")
	assert.NotContains(t, html, "A trip run from the GTFS frequencies")

	data, _ = reportsView(t, r, "date=2026-09-02&route_id=R7")
	trips := make([]string, 0)
	for _, trip := range data["Trips"].([]reportTripRow) {
		trips = append(trips, trip.GtfsTripID)
	}
	assert.Equal(t, []string{"7B", "7C", "7D"}, trips, "7F runs from frequencies.txt")
	runs = routeRunsOf(t, data)
	require.Len(t, runs.Directions, 1)
	assert.Equal(t, []reportHeadwayTrip{{GtfsTripID: "7F", Scheduled: "every 20 min, 08:00 to 09:00", RunCount: "3 of 3 runs"}}, runs.Directions[0].Schedules)
	assert.Equal(t, []string{
		"08:00:06 7F Bus F1: no gap",
		"08:05:06 7B Bus 7B: 5 min after Bus F1, every 6 min 40 s; bunched at 0 of 2 stops",
		"08:16:06 7C Bus 7C: 11 min after Bus 7B, every 6 min 40 s (long); bunched at 0 of 2 stops",
		"08:20:06 7F Bus F2: 4 min after Bus 7C, every 6 min 40 s; bunched at 0 of 2 stops",
		"08:25:06 7D Bus 7D: 5 min after Bus F2, every 6 min 40 s; bunched at 0 of 2 stops",
		"08:40:06 7F Bus F3: 15 min after Bus 7D, every 20 min; bunched at 0 of 2 stops",
	}, runLines(runs.Directions[0]))
	html = renderReportsContent(t, data)
	assert.Contains(t, html, "A trip run from the GTFS frequencies is not judged on time")
	assert.Contains(t, html, "A timetabled trip is listed where it leaves a stop within 12 min of another")
}

// TestReportsPage_RouteWithoutFrequentService: a route run by timetable less
// often than every 12 minutes has no runs to list and no gap tiles.
func TestReportsPage_RouteWithoutFrequentService(t *testing.T) {
	r, _ := newTimetableReporter(t)
	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R1")
	assert.Nil(t, data["RouteRuns"])
	assert.Nil(t, data["Tiles"].(analyticsTiles).Gaps)
	html := renderReportsContent(t, data)
	assert.NotContains(t, html, "Runs to")
	assert.NotContains(t, html, "an-tiles-6")
	assert.Contains(t, html, "Scheduled trips")
}

// routeRunEdgeRuns is a day north on route 5 with a run in each state its
// list shows: A first; X started at ST1 and ended there a minute later,
// without leaving; P and Q leaving ST1 and ST2 together, P 20 minutes after
// A; R ten minutes after them with its phone silent from 07:49:30 to 07:52,
// as it left ST1, so when it left is not known there but its gap at ST2 is
// judged; and S leaving ST1 at 07:51, while R could have, so its gap there is
// unknown, and ST2 a minute behind R. Nobody ran route 5 south.
var routeRunEdgeRuns = []gapRun{
	{name: "A", tripID: "GN", legs: north(hms(7, 20, 0), hms(7, 25, 0))},
	{name: "X", tripID: "GN", legs: func(*rider.TripInfo) []reportLeg {
		return []reportLeg{{hms(7, 28, 0), 0}, {hms(7, 30, 0), 0}}
	}},
	{name: "P", tripID: "GN", legs: north(hms(7, 40, 0), hms(7, 45, 0))},
	{name: "Q", tripID: "GN", legs: north(hms(7, 40, 0), hms(7, 45, 0))},
	{name: "R", tripID: "GN", legs: north(hms(7, 50, 0), hms(7, 55, 0)), silent: [2]time.Duration{hms(7, 49, 30), hms(7, 52, 0)}},
	{name: "S", tripID: "GN", legs: north(hms(7, 51, 0), hms(7, 56, 0))},
}

// TestReportsPage_RouteRunsInEveryState: X is in the order by when it
// started, and R by the earliest it could have left ST1, before S, which left
// before R's phone spoke again. Of P and Q, which left in the same second, P
// is first by its lower id: the store lists them the other way round.
func TestReportsPage_RouteRunsInEveryState(t *testing.T) {
	ix := gapFixtureIndex(t)
	trips, trails := gapTrips(t, ix, 1, routeRunEdgeRuns)
	slices.Reverse(trips)
	r := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }

	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R5")
	runs := routeRunsOf(t, data)
	require.Len(t, runs.Directions, 2)
	assert.Equal(t, []string{
		"07:20:06 GN Bus A: no gap",
		"did not leave GN Bus X",
		"07:40:06 GN Bus P: 20 min after Bus A, every 10 min (long); bunched at 0 of 2 stops",
		"07:40:06 GN Bus Q: 0 s after Bus P, every 10 min (bunched); bunched at 2 of 2 stops",
		"not known GN Bus R: unknown; bunched at 0 of 1 stop",
		"07:51:06 GN Bus S: unknown; bunched at 1 of 1 stop",
	}, runLines(runs.Directions[0]))
	assert.Equal(t, reportRunGapRow{GtfsTripID: "GN", Left: "did not leave", Vehicle: "Bus X", Driver: "Driver X", TrailURL: "/admin/trips/2"}, runs.Directions[0].Runs[1])
	assert.Empty(t, runs.Directions[1].Runs, "nobody ran south")
	assert.Equal(t, []reportHeadwayTrip{{GtfsTripID: "GS", Scheduled: "every 15 min, 07:00 to 08:00", RunCount: "0 of 4 runs"}}, runs.Directions[1].Schedules)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<a class="row-link" href="/admin/trips/2"><span class="muted">did not leave</span></a><span class="sub">GN</span></td><td class="nowrap" data-label="Vehicle">Bus X<span class="sub hide-phone">Driver X</span></td><td></td><td class="num right c-side nowrap" data-label="Bunched at"><span class="muted">no gap judged</span></td>`)
	assert.Contains(t, html, `<td class="num right c-side nowrap" data-label="Bunched at">0 of 1 stop</td>`)
	south := html[strings.Index(html, "Runs to South"):]
	assert.Contains(t, south, `<p class="card-note">No run in this direction.</p>`)
	assert.NotContains(t, south, "<table")
}

// TestReportsPage_UnlabelledVehicles: a vehicle with no label, which
// vehicles.label allows, is named by its id on the route's day, as the rest
// of the admin pages name it: in the run cards, in the gap after it, and in
// Scheduled trips.
func TestReportsPage_UnlabelledVehicles(t *testing.T) {
	ix := gapFixtureIndex(t)
	trips, trails := gapTrips(t, ix, 1, []gapRun{
		{name: "A", tripID: "GN", legs: north(hms(7, 0, 0), hms(7, 5, 0))},
		{name: "B", tripID: "GN", legs: north(hms(7, 10, 0), hms(7, 15, 0))},
		{name: "W", tripID: "GX", legs: north(hms(7, 35, 0), hms(7, 40, 0))},
	})
	for i := range trips {
		trips[i].VehicleLabel = ""
	}
	r := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }

	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R5")
	runs := routeRunsOf(t, data)
	require.NotEmpty(t, runs.Directions)
	assert.Equal(t, []string{
		"07:00:06 GN bus-A: no gap",
		"07:10:06 GN bus-B: 10 min after bus-A, every 10 min; bunched at 0 of 2 stops",
	}, runLines(runs.Directions[0]))
	scheduled := data["Trips"].([]reportTripRow)
	require.Len(t, scheduled, 1)
	require.Len(t, scheduled[0].Runs, 1)
	assert.Equal(t, "bus-W", scheduled[0].Runs[0].Vehicle)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `aria-label="Replay bus-B, 07:10:06"`)
	assert.Contains(t, html, `aria-label="Replay bus-W on `)
}

// gapFixtureIndexSouthUnmeasured is the gap fixture with GS's only timepoint
// ST1, its last stop, so no stop south is measured.
func gapFixtureIndexSouthUnmeasured(t *testing.T) *rider.Index {
	t.Helper()
	return gapFixtureIndexEdited(t, func(name string, body []byte) []byte {
		if name != "stop_times.txt" {
			return body
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		for i, line := range lines {
			switch {
			case i == 0:
				lines[i] = line + ",timepoint"
			case strings.HasPrefix(line, "GS,") && !strings.Contains(line, ",ST1,"):
				lines[i] = line + ",0"
			default:
				lines[i] = line + ",1"
			}
		}
		return []byte(strings.Join(lines, "\n") + "\n")
	})
}

// TestReportsPage_RouteRunsWithNoStopMeasured: route 5's day (gapDayRuns)
// with no stop south measured (gapFixtureIndexSouthUnmeasured). The South
// card still says GS's schedule, and lists K, L, M, N and O by when they
// started, when each left not known, each with its Replay link. North is as
// before.
func TestReportsPage_RouteRunsWithNoStopMeasured(t *testing.T) {
	ix := gapFixtureIndexSouthUnmeasured(t)
	trips, trails := gapFixtureTrips(t, ix)
	r := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }

	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R5")
	runs := routeRunsOf(t, data)
	require.Len(t, runs.Directions, 2)
	assert.Len(t, runs.Directions[0].Runs, 10, "north as before")
	south := runs.Directions[1]
	assert.Equal(t, "Runs to South", south.Title)
	assert.Equal(t, []reportHeadwayTrip{{GtfsTripID: "GS", Scheduled: "every 15 min, 07:00 to 08:00", RunCount: "4 of 4 runs"}}, south.Schedules)
	assert.Equal(t, []string{
		"not known GS Bus K: unknown",
		"not known GS Bus L: unknown",
		"not known GS Bus M: unknown",
		"not known GS Bus N: unknown",
		"not known GS Bus O: unknown",
	}, runLines(south))

	html := renderReportsContent(t, data)
	southHTML := html[strings.Index(html, "Runs to South"):]
	assert.Contains(t, southHTML, `GS runs every 15 min, 07:00 to 08:00: 4 of 4 runs`)
	assert.NotContains(t, southHTML, "No run in this direction.")
	assert.Contains(t, southHTML, `<a class="row-link" href="/admin/trips/211"><span class="muted">not known</span></a><span class="sub">GS</span></td><td class="nowrap" data-label="Vehicle">Bus K<span class="sub hide-phone">Driver K</span></td>`)
	assert.Contains(t, southHTML, `<a class="btn btn-sm btn-plain" href="/admin/trips/211" aria-label="Replay Bus K, not known">`)
	assert.Equal(t, 5, strings.Count(southHTML, `aria-label="Replay Bus `), "a Replay link for each run")
}

// TestReportsPage_RouteRunsWithNoStopMeasuredThatDidNotLeave: no stop south
// measured (gapFixtureIndexSouthUnmeasured). X waited at ST3, GS's first
// stop, and ended there a minute later, so it is listed by when it started,
// after K, as did not leave, with no gap: as X on a measured trip is
// (TestReportsPage_RouteRunsInEveryState).
func TestReportsPage_RouteRunsWithNoStopMeasuredThatDidNotLeave(t *testing.T) {
	ix := gapFixtureIndexSouthUnmeasured(t)
	trips, trails := gapTrips(t, ix, 1, []gapRun{
		{name: "K", tripID: "GS", legs: south(hms(7, 0, 0), hms(7, 5, 0))},
		{name: "X", tripID: "GS", legs: func(*rider.TripInfo) []reportLeg {
			return []reportLeg{{hms(7, 13, 0), 0}, {hms(7, 15, 0), 0}}
		}},
	})
	r := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }

	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R5")
	runs := routeRunsOf(t, data)
	require.Len(t, runs.Directions, 2)
	dir := runs.Directions[1]
	assert.Equal(t, "Runs to South", dir.Title)
	assert.Equal(t, []string{
		"not known GS Bus K: unknown",
		"did not leave GS Bus X",
	}, runLines(dir))
	assert.Equal(t, reportRunGapRow{GtfsTripID: "GS", Left: "did not leave", Vehicle: "Bus X", Driver: "Driver X", TrailURL: "/admin/trips/2"}, dir.Runs[1])

	html := renderReportsContent(t, data)
	southHTML := html[strings.Index(html, "Runs to South"):]
	assert.Contains(t, southHTML, `<a class="row-link" href="/admin/trips/2"><span class="muted">did not leave</span></a><span class="sub">GS</span></td><td class="nowrap" data-label="Vehicle">Bus X<span class="sub hide-phone">Driver X</span></td><td></td><td class="num right c-side nowrap" data-label="Bunched at"><span class="muted">no gap judged</span></td>`)
	assert.Contains(t, southHTML, `<a class="btn btn-sm btn-plain" href="/admin/trips/2" aria-label="Replay Bus X, did not leave">`)
}

// TestReportsPage_EscapesRouteRuns: a headsign, a trip id, a vehicle's label
// and a driver's name come from the agency's feed and its admins; none
// reaches the page as markup.
func TestReportsPage_EscapesRouteRuns(t *testing.T) {
	r, _, _ := newGapReporter(t)
	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R5")
	dir := &routeRunsOf(t, data).Directions[0]
	dir.Title = `Runs to <img src=x>`
	dir.Schedules[0].GtfsTripID = `<b>GN</b>`
	run := &dir.Runs[2]
	run.GtfsTripID, run.Vehicle, run.Driver = `"><script>`, `<i>Bus C</i>`, `<u>Driver</u>`
	run.Gap.Text = `2 min after <s>Bus B</s>`

	html := renderReportsContent(t, data)
	for _, raw := range []string{"<img", "<b>GN", "<script>", "<i>Bus", "<u>Driver", "<s>Bus"} {
		assert.NotContains(t, html, raw)
	}
	assert.Contains(t, html, `Runs to &lt;img src=x&gt;</h3>`)
	assert.Contains(t, html, `&lt;b&gt;GN&lt;/b&gt; runs every 10 min`)
	assert.Contains(t, html, `<span class="sub">&#34;&gt;&lt;script&gt;</span>`)
	assert.Contains(t, html, `aria-label="Replay &lt;i&gt;Bus C&lt;/i&gt;, 07:12:06"`)
	assert.Contains(t, html, `&lt;u&gt;Driver&lt;/u&gt;`)
	assert.Contains(t, html, `<span class="pill pill-early">Bunched</span> 2 min after &lt;s&gt;Bus B&lt;/s&gt;`)
}

func TestFormatGap(t *testing.T) {
	for _, tc := range []struct {
		gap  time.Duration
		want string
	}{
		{0, "0 s"},
		{40 * time.Second, "40 s"},
		{time.Minute, "1 min"},
		{130 * time.Second, "2 min 10 s"},
		{400 * time.Second, "6 min 40 s"},
		{10 * time.Minute, "10 min"},
		{104 * time.Minute, "104 min"},
	} {
		assert.Equal(t, tc.want, formatGap(tc.gap), "%v", tc.gap)
	}
}

func TestRunsTitle(t *testing.T) {
	assert.Equal(t, "Runs to North", runsTitle(directionGaps{DirectionID: ptr(0), Headsign: "North"}))
	assert.Equal(t, "Runs to Zoo", runsTitle(directionGaps{Headsign: "Zoo"}))
	assert.Equal(t, "Runs in direction 1", runsTitle(directionGaps{DirectionID: ptr(1)}))
	assert.Equal(t, "Runs", runsTitle(directionGaps{}))
}

// TestNewReportRunGap: a gap's verdict is the one the day's counts give it
// (TestGapCounts_Add), at the same bounds.
func TestNewReportRunGap(t *testing.T) {
	runs := map[int64]routeRun{7: {gtfsTripID: "GN", tripRun: tripRun{ID: 7, VehicleLabel: "Bus 7"}}}
	judged := func(gap, scheduled time.Duration) *stopGap {
		return &stopGap{gap: gap, scheduled: scheduled, run: 8, ahead: 7}
	}
	for _, tc := range []struct {
		name string
		gap  *stopGap
		want reportRunGap
	}{
		{"none", nil, reportRunGap{Text: "no gap"}},
		{"unknown", &stopGap{unknown: true, run: 8, ahead: 7}, reportRunGap{Text: "unknown"}},
		{"under a quarter is bunched", judged(149*time.Second, 10*time.Minute), reportRunGap{Text: "2 min 29 s after Bus 7", Every: "every 10 min", Tone: "bunched"}},
		{"exactly a quarter is not", judged(150*time.Second, 10*time.Minute), reportRunGap{Text: "2 min 30 s after Bus 7", Every: "every 10 min"}},
		{"exactly half again is not long", judged(15*time.Minute, 10*time.Minute), reportRunGap{Text: "15 min after Bus 7", Every: "every 10 min"}},
		{"a second more is long", judged(15*time.Minute+time.Second, 10*time.Minute), reportRunGap{Text: "15 min 1 s after Bus 7", Every: "every 10 min", Tone: "long"}},
		{"in the same second", judged(0, 400*time.Second), reportRunGap{Text: "0 s after Bus 7", Every: "every 6 min 40 s", Tone: "bunched"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, newReportRunGap(tc.gap, runs))
		})
	}
}

// TestRouteGaps_RunsAddUpToTheDirection: each gap judged counts once, for the
// run behind, so a direction's runs add up to its gaps judged and bunched.
func TestRouteGaps_RunsAddUpToTheDirection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		route string
		r     func(t *testing.T) *serviceReporter
	}{
		{"frequencies.txt", "R5", func(t *testing.T) *serviceReporter { r, _, _ := newGapReporter(t); return r }},
		{"timetable", "R6", func(t *testing.T) *serviceReporter { r, _ := newTimetableReporter(t); return r }},
		{"both", "R7", func(t *testing.T) *serviceReporter { r, _ := newTimetableReporter(t); return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := tc.r(t).report(t.Context(), "2026-09-02")
			require.NoError(t, err)
			route := reportRoute(t, rep, tc.route)
			require.NotEmpty(t, route.Gaps)
			for _, d := range route.Gaps {
				judged, bunched := 0, 0
				for _, run := range d.runs {
					judged += run.judged
					bunched += run.bunched
				}
				assert.Equal(t, d.GapsJudged, judged, d.Headsign)
				assert.Equal(t, d.Bunched, bunched, d.Headsign)
			}
		})
	}
}

// untimedST1 is the gap fixture's trip id with ST1 no timepoint.
func untimedST1(t *testing.T, ix *rider.Index, id string) *rider.TripInfo {
	t.Helper()
	trip := *mustTrip(t, ix, id)
	trip.StopTimes = slices.Clone(trip.StopTimes)
	trip.StopTimes[0].Timepoint = false
	return &trip
}

// TestRouteGaps_RunsOfATripWithNoStopMeasured: route 5 north with ST1 no
// timepoint, so GN is measured at ST2 alone, and GT's only timepoint ST2, its
// last stop, so no stop of GT is measured. GT's runs Q and S are listed all
// the same, by when they started, between GN's P and R, which are listed by
// when they left ST2: when each left not known, with no gap and nothing
// judged. P and R keep their passings, and R's gap is the one judged.
func TestRouteGaps_RunsOfATripWithNoStopMeasured(t *testing.T) {
	ix := gapFixtureIndex(t)
	runs, trails := gapTrips(t, ix, 1, []gapRun{
		{name: "P", tripID: "GN", legs: north(hms(19, 0, 0), hms(19, 5, 0))},
		{name: "Q", tripID: "GT", legs: north(hms(19, 8, 0), hms(19, 13, 0))},
		{name: "R", tripID: "GN", legs: north(hms(19, 10, 0), hms(19, 15, 0))},
		{name: "S", tripID: "GT", legs: north(hms(19, 18, 0), hms(19, 23, 0))},
	})
	byTrip, trailsByTrip := byGTFSTrip(runs, trails)
	rep := &serviceReport{Date: "2026-09-02", location: ix.Timezone()}

	gaps := routeGaps(rep, []*rider.TripInfo{untimedST1(t, ix, "GN"), untimedST1(t, ix, "GT")}, reportDayStart, byTrip, trailsByTrip, testReportSettings)
	require.Len(t, gaps, 1)
	d := gaps[0]
	require.Len(t, d.runs, 4, "each run once")
	assert.Equal(t, int64(1), d.runs[0].id)
	assert.Equal(t, "19:05:06", serviceClock(d.runs[0].left.Sub(reportDayStart)), "P left ST2")
	assert.Equal(t, runGaps{id: 2, served: true}, d.runs[1], "Q started at 19:07")
	assert.Equal(t, int64(3), d.runs[2].id)
	assert.Equal(t, "19:15:06", serviceClock(d.runs[2].left.Sub(reportDayStart)), "R left ST2")
	assert.Equal(t, 1, d.runs[2].judged)
	assert.Equal(t, runGaps{id: 4, served: true}, d.runs[3], "S started at 19:17")
	assert.Equal(t, 1, d.GapsJudged)
}

// TestRouteGaps_RunsOfATripWithNoStopMeasuredThatEnded: GT measured at no
// stop, as in TestRouteGaps_RunsOfATripWithNoStopMeasured. X waited at ST1,
// GT's first stop, and ended there a minute after its last fix, so it did
// not serve it, as runPassing says of a run that ended before a stop. Y left
// ST1 and ended short of ST2, GT's last stop: it left, at a time not known.
// Z drove as X did on GT drawn through its stops, which is not placed, so
// whether it left is not known. Each is listed by when it started.
func TestRouteGaps_RunsOfATripWithNoStopMeasuredThatEnded(t *testing.T) {
	ix := gapFixtureIndex(t)
	waits := func(arrive, end time.Duration) func(*rider.TripInfo) []reportLeg {
		return func(*rider.TripInfo) []reportLeg { return []reportLeg{{arrive, 0}, {end, 0}} }
	}
	runs, trails := gapTrips(t, ix, 1, []gapRun{
		{name: "X", tripID: "GT", legs: waits(hms(19, 8, 0), hms(19, 10, 0))},
		{name: "Y", tripID: "GT", legs: func(*rider.TripInfo) []reportLeg {
			return []reportLeg{{hms(19, 18, 0), 0}, {hms(19, 19, 0), 0}, {hms(19, 20, 0), 300}}
		}},
		{name: "Z", tripID: "GT", legs: waits(hms(19, 28, 0), hms(19, 30, 0))},
	})
	drawn := untimedST1(t, ix, "GT")
	drawn.ID, drawn.ShapeFromStops = "GT-drawn", true
	runs[2].GtfsTripID = drawn.ID
	byTrip, trailsByTrip := byGTFSTrip(runs, trails)
	rep := &serviceReport{Date: "2026-09-02", location: ix.Timezone()}

	gaps := routeGaps(rep, []*rider.TripInfo{untimedST1(t, ix, "GT"), drawn}, reportDayStart, byTrip, trailsByTrip, testReportSettings)
	require.Len(t, gaps, 1)
	assert.Equal(t, []runGaps{{id: 1}, {id: 2, served: true}, {id: 3, served: true}}, gaps[0].runs)
}
