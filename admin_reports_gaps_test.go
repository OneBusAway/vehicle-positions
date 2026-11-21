package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gapsOf is the "Gaps between vehicles" section of a page's data.
func gapsOf(t *testing.T, data map[string]interface{}) *analyticsGaps {
	t.Helper()
	gaps, ok := data["Gaps"].(*analyticsGaps)
	require.True(t, ok, "the page has no gaps between vehicles")
	return gaps
}

// TestReportsPage_GapsDay: a day with route 5 run by headway has the gaps
// between its vehicles in two more tiles, a row for each direction, and the
// two charts, with the numbers the day's report gives
// (TestServiceReporter_HeadwayGaps).
func TestReportsPage_GapsDay(t *testing.T) {
	r, _, _ := newGapReporter(t)

	data, status := reportsView(t, r, "date=2026-09-02&range=day")
	require.Equal(t, 200, status)
	assert.Equal(t, "On time was 77% on Wednesday 2 September", data["Headline"], "the timetabled trips keep the on-time headline")
	assert.NotContains(t, data["Lede"], "bunching")

	tiles := data["Tiles"].(analyticsTiles)
	assert.Equal(t, &gapTiles{
		Known: true, Bunched: 29, Long: 12,
		BunchedNote: "5 gaps under 25% of the scheduled gap",
		LongNote:    "2 gaps over 150% of the scheduled gap",
	}, tiles.Gaps, "5 of 17 gaps bunched and 2 long, both ways")

	gaps := gapsOf(t, data)
	assert.Equal(t, "25%", gaps.BunchedBelow)
	assert.Equal(t, "150%", gaps.LongAbove)
	require.Len(t, gaps.Rows, 2)
	north, south := gaps.Rows[0], gaps.Rows[1]
	assert.Equal(t, "Every ten", north.Route.Name)
	assert.Equal(t, "/admin/reports?date=2026-09-02&route_id=R5", north.Route.TripsURL)
	assert.Equal(t, [2]int{15, 25}, [2]int{north.Route.Ran, north.Route.Scheduled}, "the whole route's trips run")
	assert.Equal(t, reportGapRow{Route: north.Route, Direction: "to North", Judged: 13, Unknown: 1, Bunched: "23.1%", LongGaps: "15.4%", ExtraWait: "1.9"}, north,
		"extra wait 113 s")
	assert.Equal(t, reportGapRow{Route: south.Route, Direction: "to South", Judged: 4, Unknown: 2, Bunched: "50.0%", LongGaps: "0.0%", ExtraWait: "-1.0"}, south,
		"60 s less than scheduled, the gaps judged being shorter")

	// By the hour the later vehicle left: 14 at 07 (5 bunched, 2 long), one
	// at 08, and J's two after midnight, with the hours between empty.
	require.NotNil(t, gaps.Hours)
	assert.Equal(t, "Share of gaps bunched or long, by hour of day", gaps.Hours.Name)
	require.Len(t, gaps.Hours.Bars, 18, "07:00 to the hour after midnight")
	assert.Equal(t, "07:00 to 08:00|14% long, 36% bunched, 50% neither (14 gaps)", gaps.Hours.Bars[0].Tip)
	assert.Equal(t, "09:00 to 10:00|no gap judged", gaps.Hours.Bars[2].Tip)
	assert.Equal(t, "00:00 to 01:00|0% long, 0% bunched, 100% neither (2 gaps)", gaps.Hours.Bars[17].Tip)
	assert.Equal(t, hourChartRow{Hour: "07:00", Count: 14, Lower: "14%", Upper: "36%", Rest: "50%"}, gaps.Hours.Rows[0])

	// By tenths of the scheduled gap, as the kept totals keep them
	// (TestNewDayTotals_HeadwayGaps).
	require.NotNil(t, gaps.Ratios)
	counts := make(map[string]int)
	for _, row := range gaps.Ratios.Rows {
		if row.Gaps > 0 {
			counts[row.Share] = row.Gaps
		}
	}
	assert.Equal(t, map[string]int{"20% to 30%": 5, "70% to 80%": 1, "100% to 110%": 6, "110% to 120%": 2, "150% to 160%": 1, "160% to 170%": 1, "210% to 220%": 1}, counts)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<div class="rise tiles an-tiles an-tiles-6">`)
	assert.Contains(t, html, `<span class="tile-label">Bunched</span><span class="tile-value tone-early"><span class="count-up" data-count="29">29</span>%</span><span class="tile-note">5 gaps under 25% of the scheduled gap</span>`)
	assert.Contains(t, html, `<span class="tile-label">Long gaps</span><span class="tile-value tone-late"><span class="count-up" data-count="12">12</span>%</span><span class="tile-note">2 gaps over 150% of the scheduled gap</span>`)
	assert.Contains(t, html, `<h3 id="an-gaps" style="flex:1">Gaps between vehicles</h3>`)
	assert.Contains(t, html, `<a class="row-link an-route" href="/admin/reports?date=2026-09-02&amp;route_id=R5"><span class="route" title="Every ten">5</span><span>Every ten<span class="sub">to North</span></span></a>`)
	assert.Contains(t, html, `<td class="num right nowrap" data-label="Gaps judged">13<span class="sub">1 unknown</span></td><td class="num right tone-early" data-label="Bunched">23.1%</td><td class="num right tone-late" data-label="Long gaps">15.4%</td><td class="num right" data-label="Extra wait (min)">1.9</td><td class="num right nowrap" data-label="Trips run">15 <span class="muted">of 25</span></td>`)
	assert.Contains(t, html, `data-label="Extra wait (min)">-1.0</td>`)
	assert.Contains(t, html, "It is bunched under 25% of the scheduled gap and long over 150%, and unknown when a vehicle with no report within 2 min of the stop could have left between them.")
	assert.Contains(t, html, `role="img" aria-label="Share of gaps bunched or long, by hour of day"`)
	assert.Contains(t, html, `data-tip="07:00 to 08:00|14% long, 36% bunched, 50% neither (14 gaps)"`)
	assert.Contains(t, html, `<tr><td class="num">07:00</td><td class="num right">14</td><td class="num right">36%</td><td class="num right">14%</td></tr>`, "bunched, then long")
	assert.Contains(t, html, `<rect class="early-band" x="0" y="4" width="45.2" height="164.0" rx="4"/><text x="22.6" y="16" text-anchor="middle">bunched</text>`)
	assert.Contains(t, html, `<rect class="late-band" x="271.0" y="4" width="289.0" height="164.0" rx="4"/>`)
	assert.Contains(t, html, `data-tip="20% to 30% of the scheduled gap|5 gaps"`)
	assert.Contains(t, html, `<tr><td>300% or more</td><td class="num right">0</td></tr>`)
	assert.Equal(t, 4, strings.Count(html, "Show these numbers as a table"), "behind the two charts of stops and the two of gaps")
	// The trips' hour table keeps its columns: on time, early, late.
	assert.Contains(t, html, `<tr><td class="num">11:00</td><td class="num right">1</td><td class="num right">33%</td><td class="num right">0%</td><td class="num right">67%</td></tr>`)
}

// TestReportsPage_GapsPeriod: over seven days Bunched and Long gaps are two
// more figures, each drawn day by day when picked.
func TestReportsPage_GapsPeriod(t *testing.T) {
	r, _, _ := newGapReporter(t)

	data, _ := reportsView(t, r, "date=2026-09-02")
	metrics := data["Metrics"].([]analyticsMetric)
	require.Len(t, metrics, 7)
	assert.Equal(t, analyticsMetric{Label: "Bunched", Value: "29%", Note: "5 of 17 gaps", Tone: "early", URL: "/admin/reports?date=2026-09-02&metric=bunched"}, metrics[5],
		"the 7 days before have no gap, so the counts stay")
	assert.Equal(t, analyticsMetric{Label: "Long gaps", Value: "12%", Note: "2 of 17 gaps", Tone: "late", URL: "/admin/reports?date=2026-09-02&metric=long_gaps"}, metrics[6])
	gaps := gapsOf(t, data)
	require.Len(t, gaps.Rows, 2)
	assert.Equal(t, [2]int{15, 125}, [2]int{gaps.Rows[0].Route.Ran, gaps.Rows[0].Route.Scheduled}, "25 runs on each of five weekdays")
	assert.Equal(t, 13, gaps.Rows[0].Judged)
	assert.Nil(t, data["Tiles"])

	data, _ = reportsView(t, r, "date=2026-09-02&metric=bunched")
	trend := data["Trend"].(analyticsTrend)
	assert.Equal(t, "Bunched", trend.Label)
	assert.Equal(t, "early", trend.Tone)
	assert.Equal(t, "Bunched by day, 27 Aug to 2 Sep", trend.Chart.Name)
	assert.Equal(t, analyticsTrendRow{Day: "Wed 2 Sep", Value: "29%", Before: "no figure"}, trend.Rows[6])
	assert.Equal(t, analyticsTrendRow{Day: "Tue 1 Sep", Value: "no figure", Before: "no figure"}, trend.Rows[5], "a day with no gap judged has no figure")
	assert.Equal(t, "30%", trend.Chart.Grid[2].Label, "a share that is good when low, drawn to the ten over its highest day")
	assert.True(t, data["Metrics"].([]analyticsMetric)[5].Selected)
	assert.Equal(t, "/admin/reports?date=2026-09-02&days=28&metric=bunched", data["Range"].([]analyticsLink)[2].URL, "a period keeps the measure")
	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<a class="metric" href="/admin/reports?date=2026-09-02&amp;metric=bunched" aria-current="true"><span class="tile-label">Bunched</span><span class="tile-value tone-early">29%</span><span class="tile-note">5 of 17 gaps</span></a>`)
	assert.Contains(t, html, `<div class="an-chart line-early">`)
	assert.Contains(t, html, `<input type="hidden" name="metric" value="bunched">`)
	assert.Contains(t, html, `<th class="right">Bunched</th><th class="right">The 7 days before</th>`)
	assert.Equal(t, 5, strings.Count(html, "Show these numbers as a table"), "the trend's table too")

	data, _ = reportsView(t, r, "date=2026-09-02&metric=long_gaps&compare=off")
	trend = data["Trend"].(analyticsTrend)
	assert.Equal(t, [2]string{"Long gaps", "late"}, [2]string{trend.Label, trend.Tone})
	assert.Equal(t, "12%", trend.Rows[6].Value)
	assert.Contains(t, renderReportsContent(t, data), `<div class="an-chart line-late">`)
}

// TestReportsPage_NoGapsWithoutHeadwayService: an agency run only by
// timetable sees the page as before, and a measure of gaps is on time there.
func TestReportsPage_NoGapsWithoutHeadwayService(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, _ := reportsView(t, r, "date=2026-09-02&range=day")
	assert.Nil(t, data["Gaps"])
	assert.Nil(t, data["Tiles"].(analyticsTiles).Gaps)
	html := renderReportsContent(t, data)
	assert.NotContains(t, html, "Gaps between vehicles")
	assert.NotContains(t, html, "an-tiles-6")
	assert.NotContains(t, html, "Bunched")

	data, _ = reportsView(t, r, "date=2026-09-02&metric=bunched")
	assert.Equal(t, metricOnTime, data["Metric"])
	assert.Len(t, data["Metrics"], 5)
	assert.True(t, data["Metrics"].([]analyticsMetric)[0].Selected)
	assert.Equal(t, "On time", data["Trend"].(analyticsTrend).Label)
	assert.Equal(t, "/admin/reports?date=2026-09-02&days=28", data["Range"].([]analyticsLink)[2].URL)
	assert.Equal(t, "/admin/reports?date=2026-08-26", data["PrevURL"])
	html = renderReportsContent(t, data)
	assert.NotContains(t, html, `name="metric"`)
	assert.NotContains(t, html, "Long gaps")
}

// TestReportsPage_GapsWithNoGapJudged: a day route 5 was scheduled but not
// run has its rows, and nothing to draw.
func TestReportsPage_GapsWithNoGapJudged(t *testing.T) {
	r, _, _ := newGapReporter(t)

	data, _ := reportsView(t, r, "date=2026-09-01&range=day")
	assert.Equal(t, "No scheduled trip ran on Tuesday 1 September", data["Headline"])
	assert.Equal(t, &gapTiles{BunchedNote: "0 gaps under 25% of the scheduled gap", LongNote: "0 gaps over 150% of the scheduled gap"}, data["Tiles"].(analyticsTiles).Gaps)
	gaps := gapsOf(t, data)
	assert.Nil(t, gaps.Hours)
	assert.Nil(t, gaps.Ratios)
	require.Len(t, gaps.Rows, 2)
	assert.Equal(t, reportGapRow{Route: gaps.Rows[0].Route, Direction: "to North"}, gaps.Rows[0])

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<span class="tile-label">Bunched</span><span class="tile-value muted">None</span><span class="tile-note">0 gaps under 25% of the scheduled gap</span>`)
	assert.Contains(t, html, `<td class="num right nowrap" data-label="Gaps judged">0</td>`)
	assert.Equal(t, 2, strings.Count(html, "No gap was judged, so there is nothing to draw."))
}

// newHeadwayOnlyReporter is route 5's day with nothing else run, not even
// its timetabled trip: no stop is judged against a timetable.
func newHeadwayOnlyReporter(t *testing.T) *serviceReporter {
	t.Helper()
	ix := gapFixtureIndex(t)
	runs := slices.DeleteFunc(slices.Clone(gapDayRuns), func(run gapRun) bool { return run.tripID == "GX" })
	trips, trails := gapTrips(t, ix, 200, runs)
	r := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }
	return r
}

// TestReportsPage_HeadwayOnlyHeadline: a period run only by headway leads
// with its gaps.
func TestReportsPage_HeadwayOnlyHeadline(t *testing.T) {
	r := newHeadwayOnlyReporter(t)

	data, _ := reportsView(t, r, "date=2026-09-02&range=day")
	assert.Equal(t, "29% of gaps were bunched and 12% were long on Wednesday 2 September", data["Headline"])
	assert.Equal(t, "Times are America/Los_Angeles time.", data["Lede"], "one route is the whole of it")
	assert.False(t, data["Tiles"].(analyticsTiles).Split.Known)

	data, _ = reportsView(t, r, "date=2026-09-02")
	assert.Equal(t, "29% of gaps were bunched and 12% were long over the 7 days to Wednesday 2 September", data["Headline"])
	assert.Contains(t, renderReportsContent(t, data), `<h2 class="h1">29% of gaps were bunched and 12% were long over the 7 days to Wednesday 2 September</h2>`)
}

// headwayRoute is a route run only by headway, in one direction, with its
// gaps judged in one hour.
func headwayRoute(id string, gaps, bunched, long int) routeDayTotals {
	direction := 0
	return routeDayTotals{RouteID: id, LongName: "Road " + id, Hours: make([]hourTotals, 0), Gaps: []directionGapTotals{{
		DirectionID: &direction, Headsign: "Town",
		Hours: []gapHourTotals{{Hour: 7, gapCounts: gapCounts{Gaps: gaps, Bunched: bunched, LongGaps: long}}},
	}}}
}

// keptDayReporter is a reporter over the report fixture's schedule whose
// store keeps day's totals, so the page reads them as they are; it works out
// the period's other days from no trips at all.
func keptDayReporter(t *testing.T, day dayTotals) *serviceReporter {
	t.Helper()
	ix := reportFixtureIndex(t)
	date, err := time.Parse(reportDateLayout, day.Date)
	require.NoError(t, err)
	day.Version = dayTotalsVersion
	store := &fakeReportStore{days: map[string]storedServiceDay{day.Date: {
		Date: date, OnTimeEarly: testReportSettings.onTimeEarly, OnTimeLate: testReportSettings.onTimeLate, Totals: day,
	}}}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportFixtureNow }
	return r
}

// TestReportsPage_MostBunchedRoute: under a headline of gaps, the route most
// often bunched is named when another route has gaps too; under the on-time
// headline it is not.
func TestReportsPage_MostBunchedRoute(t *testing.T) {
	headwayOnly := keptDayReporter(t, dayTotals{Date: "2026-09-02", Routes: []routeDayTotals{
		headwayRoute("A", 10, 1, 2), headwayRoute("B", 10, 4, 0),
	}})
	data, _ := reportsView(t, headwayOnly, "date=2026-09-02&compare=off")
	assert.Equal(t, "25% of gaps were bunched and 10% were long over the 7 days to Wednesday 2 September", data["Headline"])
	assert.Equal(t, "Road B is the route with the most bunching: 40% of its gaps were bunched. Times are America/Los_Angeles time.", data["Lede"])

	timetabled := headwayRoute("C", 0, 0, 0)
	timetabled.Gaps, timetabled.Hours = make([]directionGapTotals, 0), []hourTotals{{Hour: 8, Scheduled: 1, Run: 1, OnTime: 3, Late: 1}}
	mixed := keptDayReporter(t, dayTotals{Date: "2026-09-02", Routes: []routeDayTotals{
		headwayRoute("A", 10, 1, 2), headwayRoute("B", 10, 4, 0), timetabled,
	}})
	data, _ = reportsView(t, mixed, "date=2026-09-02&compare=off")
	assert.Equal(t, "On time was 75% over the 7 days to Wednesday 2 September", data["Headline"])
	assert.NotContains(t, data["Lede"], "bunching")
	assert.Len(t, gapsOf(t, data).Rows, 2, "a row for each route run by headway, none for the timetabled one")
}

func TestAnalyticsHeadline_Gaps(t *testing.T) {
	gaps := gapCounts{Gaps: 17, Bunched: 5, LongGaps: 2}
	for _, tc := range []struct {
		name    string
		total   reportCounts
		isToday bool
		want    string
	}{
		{"gaps", reportCounts{Scheduled: 25, Run: 15, Gaps: gaps}, false, "29% of gaps were bunched and 12% were long yesterday"},
		{"gaps today", reportCounts{Scheduled: 25, Run: 15, Gaps: gaps}, true, "29% of gaps are bunched and 12% long so far today"},
		{"a stop judged keeps on time", reportCounts{Scheduled: 25, Run: 15, OnTime: 1, Gaps: gaps}, false, "On time was 100% yesterday"},
		{"a stop judged today keeps on time", reportCounts{Scheduled: 25, Run: 15, Late: 1, Gaps: gaps}, true, "On time is 0% so far today"},
		{"only gaps unknown", reportCounts{Scheduled: 25, Run: 15, Gaps: gapCounts{Unknown: 3}}, false, "No stop was judged yesterday"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, analyticsHeadline(tc.total, "yesterday", tc.isToday))
		})
	}
}

func TestMostBunchedSentence(t *testing.T) {
	sentence := func(routes ...routeDayTotals) string {
		got, total := routeTotals([]dayTotals{{Routes: routes}})
		return mostBunchedSentence(got, total)
	}
	assert.Equal(t, "Road B is the route with the most bunching: 40% of its gaps were bunched.", sentence(headwayRoute("A", 10, 1, 5), headwayRoute("B", 10, 4, 0)))
	assert.Equal(t, "Road B is the route with the most bunching: 40% of its gaps were bunched.", sentence(headwayRoute("B", 10, 4, 0), headwayRoute("A", 10, 1, 5)), "either order")
	// Two routes as often bunched: the first, in the newest day's order.
	assert.Equal(t, "Road A is the route with the most bunching: 40% of its gaps were bunched.", sentence(headwayRoute("A", 10, 4, 0), headwayRoute("B", 5, 2, 0), headwayRoute("C", 10, 0, 0)))
	assert.Equal(t, "Road B is the route with the most bunching: 40% of its gaps were bunched.", sentence(headwayRoute("B", 5, 2, 0), headwayRoute("A", 10, 4, 0), headwayRoute("C", 10, 0, 0)))

	assert.Empty(t, sentence(headwayRoute("A", 10, 4, 0)), "one route is the whole of it")
	assert.Empty(t, sentence(headwayRoute("A", 10, 4, 0), headwayRoute("B", 0, 0, 0)), "a route with no gap judged is not one to set it against")
	assert.Empty(t, sentence(headwayRoute("A", 10, 3, 0), headwayRoute("B", 20, 6, 0)), "no route is behind the rest")
	assert.Empty(t, sentence(headwayRoute("A", 10, 0, 3), headwayRoute("B", 10, 0, 0)), "no route bunched")
	assert.Empty(t, sentence(headwayRoute("A", 500, 16, 0), headwayRoute("B", 681, 14, 0)), "3.2% against 2.5% is 3% against 3%, as the page shows them")
}

func TestGapTotals(t *testing.T) {
	zero, one := 0, 1
	direction := func(id *int, headsign string, c gapCounts) directionGapTotals {
		return directionGapTotals{DirectionID: id, Headsign: headsign, Hours: []gapHourTotals{{Hour: 7, gapCounts: c}}}
	}
	older := dayTotals{Routes: []routeDayTotals{
		{RouteID: "R5", Gaps: []directionGapTotals{
			direction(&zero, "Northbound", gapCounts{Gaps: 2, Bunched: 1, SumGapS: 600, SumGapSq: 360000, SumScheduledS: 600, SumScheduledSq: 360000}),
			direction(&one, "South", gapCounts{Gaps: 1, LongGaps: 1, Unknown: 2}),
		}},
		{RouteID: "GONE", Gaps: []directionGapTotals{
			direction(nil, "Zoo", gapCounts{Gaps: 3}),
			direction(nil, "Airport", gapCounts{Gaps: 1}),
		}},
	}}
	newer := dayTotals{Routes: []routeDayTotals{
		{RouteID: "R5", Gaps: []directionGapTotals{
			direction(&one, "South", gapCounts{Gaps: 4, Bunched: 2}),
			direction(&zero, "North", gapCounts{Gaps: 5, Unknown: 1, SumGapS: 1200, SumGapSq: 1440000, SumScheduledS: 600, SumScheduledSq: 360000}),
		}},
		{RouteID: "R6", Hours: []hourTotals{{Hour: 7, Scheduled: 1}}, Gaps: make([]directionGapTotals, 0)},
	}}

	totals := gapTotals([]dayTotals{older, newer})

	require.Len(t, totals, 2, "no entry for a route run by timetable")
	assert.Equal(t, []directionTotal{
		{DirectionID: &zero, Headsign: "North", gapCounts: gapCounts{Gaps: 7, Bunched: 1, Unknown: 1, SumGapS: 1800, SumGapSq: 1800000, SumScheduledS: 1200, SumScheduledSq: 720000}},
		{DirectionID: &one, Headsign: "South", gapCounts: gapCounts{Gaps: 5, Bunched: 2, LongGaps: 1, Unknown: 2}},
	}, totals["R5"], "by direction_id whatever the order, named by the newest day")
	assert.Equal(t, []string{"Airport", "Zoo"}, []string{totals["GONE"][0].Headsign, totals["GONE"][1].Headsign}, "with no direction_id, by headsign")
	assert.Empty(t, gapTotals([]dayTotals{{Routes: []routeDayTotals{{RouteID: "R6"}}}}))

	// Extra wait over both days is worked out from their sums, not as the
	// average of each day's: a 20-minute gap and a 10-minute one, every 10.
	row := newReportGapRow(reportRouteRow{}, totals["R5"][0])
	assert.Equal(t, "3.3", row.ExtraWait, "1800000 / 3600 - 720000 / 2400 = 200 s")
}

func TestNewReportGapRow(t *testing.T) {
	route := reportRouteRow{RouteID: "R5", Name: "Every ten", Ran: 3, Scheduled: 4}
	id := 1
	assert.Equal(t, reportGapRow{Route: route, Direction: "to North", Judged: 8, Unknown: 2, Bunched: "25.0%", LongGaps: "12.5%", ExtraWait: "0.5"},
		newReportGapRow(route, directionTotal{DirectionID: &id, Headsign: "North", gapCounts: gapCounts{Gaps: 8, Bunched: 2, LongGaps: 1, Unknown: 2, SumGapS: 60, SumGapSq: 7200, SumScheduledS: 60, SumScheduledSq: 3600}}))
	assert.Equal(t, "direction 1", newReportGapRow(route, directionTotal{DirectionID: &id}).Direction, "no headsign")
	assert.Empty(t, newReportGapRow(route, directionTotal{}).Direction)
	assert.Equal(t, reportGapRow{Route: route, Unknown: 3}, newReportGapRow(route, directionTotal{gapCounts: gapCounts{Unknown: 3}}), "nothing judged, nothing to share")
}

func TestFormatExtraWait(t *testing.T) {
	seconds := func(s int) *int { return &s }
	assert.Empty(t, formatExtraWait(nil))
	assert.Equal(t, "1.9", formatExtraWait(seconds(113)))
	assert.Equal(t, "-1.0", formatExtraWait(seconds(-60)))
	assert.Equal(t, "0.0", formatExtraWait(seconds(0)))
	assert.Equal(t, "0.0", formatExtraWait(seconds(-2)), "never -0.0")
	assert.Equal(t, "0.1", formatExtraWait(seconds(3)), "to the nearest tenth of a minute")
	assert.Equal(t, "-0.1", formatExtraWait(seconds(-4)))
	assert.Equal(t, "10.0", formatExtraWait(seconds(600)))
}

func TestGapHoursOfDay(t *testing.T) {
	direction := func(hours ...gapHourTotals) directionGapTotals { return directionGapTotals{Hours: hours} }
	hour := func(h int, c gapCounts) gapHourTotals { return gapHourTotals{Hour: h, gapCounts: c} }
	day := dayTotals{Routes: []routeDayTotals{
		{Gaps: []directionGapTotals{
			direction(hour(6, gapCounts{Unknown: 3}), hour(7, gapCounts{Gaps: 10, Bunched: 3, LongGaps: 2})),
			direction(hour(7, gapCounts{Gaps: 4, Bunched: 2})),
		}},
		{Gaps: []directionGapTotals{direction(hour(24, gapCounts{Gaps: 2, LongGaps: 1}))}},
		{Hours: []hourTotals{{Hour: 5, Scheduled: 4}}},
	}}

	hours := gapHoursOfDay([]dayTotals{day, day})

	require.Len(t, hours, 19, "from 06:00, an hour with gaps none judged, to the hour after midnight")
	assert.Equal(t, hourSplit{Hour: 6}, hours[0])
	assert.Equal(t, hourSplit{Hour: 7, Count: 28, Lower: 4, Upper: 10, Rest: 14}, hours[1], "both days' gaps: long, bunched and neither")
	assert.Equal(t, hourSplit{Hour: 8}, hours[2])
	assert.Equal(t, hourSplit{Hour: 24, Count: 4, Lower: 2, Rest: 2}, hours[18])
	assert.Empty(t, gapHoursOfDay([]dayTotals{{Routes: []routeDayTotals{{Hours: []hourTotals{{Hour: 5, Scheduled: 4}}}}}}), "trips but no gaps")
}

func TestGapRatios(t *testing.T) {
	tenths := gapRatios([]dayTotals{
		{GapRatios: []gapRatioCount{{Tenth: 2, Gaps: 5}, {Tenth: 30, Gaps: 1}}},
		{GapRatios: []gapRatioCount{{Tenth: 2, Gaps: 1}, {Tenth: 0, Gaps: 2}, {Tenth: 31, Gaps: 4}, {Tenth: -1, Gaps: 7}}},
	})
	require.Len(t, tenths, gapRatioTop+1)
	want := make([]int, gapRatioTop+1)
	want[0], want[2], want[30] = 2, 6, 1
	assert.Equal(t, want, tenths, "a tenth off the chart is left out")
}

func TestNewAnalyticsTiles_Gaps(t *testing.T) {
	c := reportCounts{Scheduled: 4, Run: 3, Gaps: gapCounts{Gaps: 3, Bunched: 1, LongGaps: 2}}
	assert.Nil(t, newAnalyticsTiles(c, false).Gaps)
	assert.Equal(t, &gapTiles{Known: true, Bunched: 33, Long: 67, BunchedNote: "1 gap under 25% of the scheduled gap", LongNote: "2 gaps over 150% of the scheduled gap"},
		newAnalyticsTiles(c, true).Gaps)
	assert.Equal(t, &gapTiles{BunchedNote: "0 gaps under 25% of the scheduled gap", LongNote: "0 gaps over 150% of the scheduled gap"},
		newAnalyticsTiles(reportCounts{Gaps: gapCounts{Unknown: 2}}, true).Gaps, "no gap judged")
}

func TestNewAnalyticsMetrics_Gaps(t *testing.T) {
	query := analyticsQuery{Date: "2026-11-17", Days: 7, Metric: metricLongGaps, Compare: true}
	total := reportCounts{Gaps: gapCounts{Gaps: 200, Bunched: 12, LongGaps: 40}}
	before := reportCounts{Gaps: gapCounts{Gaps: 100, Bunched: 9, LongGaps: 20}}

	metrics := newAnalyticsMetrics(query, total, &before, true)

	require.Len(t, metrics, 7)
	assert.Equal(t, analyticsMetric{Label: "Bunched", Value: "6%", Note: "3 points down", Tone: "early", URL: "/admin/reports?date=2026-11-17&metric=bunched"}, metrics[5])
	assert.Equal(t, analyticsMetric{Label: "Long gaps", Value: "20%", Note: "unchanged", Tone: "late", URL: "/admin/reports?date=2026-11-17&metric=long_gaps", Selected: true}, metrics[6])
	assert.Empty(t, metrics[0].Tone, "on time's green is its line's")

	alone := newAnalyticsMetrics(query, total, nil, true)
	assert.Equal(t, "12 of 200 gaps", alone[5].Note)
	assert.Equal(t, "40 of 200 gaps", alone[6].Note)
	none := newAnalyticsMetrics(query, reportCounts{}, nil, true)
	assert.Equal(t, [2]string{"None", "0 of 0 gaps"}, [2]string{none[5].Value, none[5].Note})
	assert.Len(t, newAnalyticsMetrics(query, total, nil, false), 5)
}

func TestReportCounts_Gaps(t *testing.T) {
	c := reportCounts{OnTime: 4, Gaps: gapCounts{Gaps: 20, Bunched: 2, LongGaps: 5}}
	assert.InDelta(t, 10, *c.measure(metricBunched), 1e-9)
	assert.InDelta(t, 25, *c.measure(metricLongGaps), 1e-9)
	assert.Nil(t, reportCounts{OnTime: 4}.measure(metricBunched), "of no gap judged")
	assert.Nil(t, reportCounts{OnTime: 4}.measure(metricLongGaps))

	one := gapCounts{Gaps: 1, Bunched: 2, LongGaps: 3, Unknown: 4, SumGapS: 5, SumGapSq: 6, SumScheduledS: 7, SumScheduledSq: 8}
	var sum reportCounts
	sum.add(reportCounts{Gaps: one})
	sum.add(reportCounts{Scheduled: 1, Gaps: one})
	assert.Equal(t, reportCounts{Scheduled: 1, Gaps: gapCounts{Gaps: 2, Bunched: 4, LongGaps: 6, Unknown: 8, SumGapS: 10, SumGapSq: 12, SumScheduledS: 14, SumScheduledSq: 16}}, sum)

	assert.True(t, gapMetric(metricBunched))
	assert.True(t, gapMetric(metricLongGaps))
	assert.False(t, gapMetric(metricLate))
	assert.False(t, gapMetric("speed"))
}

// TestReportsPage_EscapesGapRows: a route's name and a direction's headsign
// come from the agency's feed; neither reaches the page as markup.
func TestReportsPage_EscapesGapRows(t *testing.T) {
	r, _, _ := newGapReporter(t)
	data, _ := reportsView(t, r, "date=2026-09-02&range=day")
	gaps := gapsOf(t, data)
	gaps.Rows[0].Route.Name, gaps.Rows[0].Direction = `<img src=x>`, `to "><script>`
	html := renderReportsContent(t, data)
	assert.NotContains(t, html, "<img")
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, `&lt;img src=x&gt;<span class="sub">to &#34;&gt;&lt;script&gt;</span>`)
}

// TestReportsPage_GapsFromTheStore: the page reads the gaps of days kept in
// service_day_totals, with the trips they came from gone, as it worked them
// out from memory.
func TestReportsPage_GapsFromTheStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearKept := func() {
		_, err := store.pool.Exec(ctx, `DELETE FROM service_day_totals WHERE service_date BETWEEN $1 AND $2`, reportDay.AddDate(0, 0, -13), reportDay)
		require.NoError(t, err)
	}
	clearTripTestData(t, store)
	clearKept()
	t.Cleanup(func() {
		clearTripTestData(t, store)
		clearKept()
	})

	ix := gapFixtureIndex(t)
	trips, trails := gapFixtureTrips(t, ix)
	storeReportDay(t, store, trips, trails)
	index := func() *rider.Index { return ix }
	clock := func() time.Time { return reportFixtureNow }

	memory := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, index, testReportSettings)
	memory.now = clock
	want, _ := reportsView(t, memory, "date=2026-09-02")

	first := newServiceReporter(store, index, testReportSettings)
	first.now = clock
	reportsView(t, first, "date=2026-09-02")
	clearTripTestData(t, store)

	kept := newServiceReporter(store, index, testReportSettings)
	kept.now = clock
	got, _ := reportsView(t, kept, "date=2026-09-02")

	require.Len(t, gapsOf(t, got).Rows, 2)
	assert.Equal(t, 13, gapsOf(t, got).Rows[0].Judged, "read from the kept day, its trips deleted")
	assert.Equal(t, gapsOf(t, want), gapsOf(t, got))
	assert.Equal(t, want["Metrics"], got["Metrics"])
	assert.Equal(t, want["Headline"], got["Headline"])
}
