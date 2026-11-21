package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// totalsOf adds up each of reps as the Analytics page does.
func totalsOf(reps ...*serviceReport) []dayTotals {
	out := make([]dayTotals, 0, len(reps))
	for _, rep := range reps {
		out = append(out, newDayTotals(rep, testReportSettings))
	}
	return out
}

func reportsView(t *testing.T, r *serviceReporter, query string) (map[string]interface{}, int) {
	t.Helper()
	q, err := url.ParseQuery(query)
	require.NoError(t, err)
	data, status, err := reportsPageView(context.Background(), r, &overviewReports{}, q)
	require.NoError(t, err)
	// The days are saved after the page has them; wait, so a test reads
	// the store as the page left it.
	r.saving.Wait()
	return data, status
}

// renderReportsContent renders the Analytics page's content block, without
// the layout around it.
func renderReportsContent(t *testing.T, data map[string]interface{}) string {
	t.Helper()
	tmpl, err := template.ParseFS(files, "web/templates/layout/parts.html", "web/templates/views/reports.html")
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&buf, "content", data))
	return buf.String()
}

func TestParseAnalyticsQuery(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  analyticsQuery
		url   string
	}{
		{"", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"date=2026-09-02", analyticsQuery{Date: "2026-09-02", Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports?date=2026-09-02"},
		{"date=2026-09-02&range=day", analyticsQuery{Date: "2026-09-02", Days: 1, Metric: metricOnTime, Compare: true}, "/admin/reports?date=2026-09-02&range=day"},
		{"range=7&metric=late&compare=off", analyticsQuery{Days: 7, Metric: metricLate, Compare: false}, "/admin/reports?compare=off&metric=late"},
		{"range=28&metric=speed&compare=no", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"date=2026-09-02&route_id=R1&range=7&metric=late", analyticsQuery{Date: "2026-09-02", RouteID: "R1", Days: 1, Metric: metricLate, Compare: true}, "/admin/reports?date=2026-09-02&route_id=R1"},
		{"days=28", analyticsQuery{Days: 28, Metric: metricOnTime, Compare: true}, "/admin/reports?days=28"},
		{"date=2026-09-02&days=28&metric=late&compare=off", analyticsQuery{Date: "2026-09-02", Days: 28, Metric: metricLate, Compare: false}, "/admin/reports?compare=off&date=2026-09-02&days=28&metric=late"},
		{"days=7", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"days=28&range=day", analyticsQuery{Days: 1, Metric: metricOnTime, Compare: true}, "/admin/reports?range=day"},
		{"days=28&route_id=R1", analyticsQuery{RouteID: "R1", Days: 1, Metric: metricOnTime, Compare: true}, "/admin/reports?route_id=R1"},
		// Any other days is the period the page opens on, as an unknown
		// metric is on time.
		{"days=14", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"days=1", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"days=-28", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"days=028", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
		{"days=month", analyticsQuery{Days: 7, Metric: metricOnTime, Compare: true}, "/admin/reports"},
	} {
		q, err := url.ParseQuery(tc.query)
		require.NoError(t, err)
		got := parseAnalyticsQuery(q)
		assert.Equal(t, tc.want, got, tc.query)
		assert.Equal(t, tc.url, got.URL(), tc.query)
	}
	assert.Equal(t, "/admin/reports?date=2026-09-02&range=day", reportsPageURL("2026-09-02", ""))
	assert.Equal(t, "/admin/reports?date=2026-09-02&route_id=R+1", reportsPageURL("2026-09-02", "R 1"))
}

func TestReportsPage_Day(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02&range=day")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Analytics", data["Title"])
	assert.Equal(t, "/api/v1/admin/reports/service?date=2026-09-02&format=csv", data["CSVURL"])
	assert.Equal(t, "/admin/reports?date=2026-09-01&range=day", data["PrevURL"])
	assert.Equal(t, "/admin/reports?date=2026-09-03&range=day", data["NextURL"])
	assert.Equal(t, []analyticsLink{
		{Label: "Day", URL: "/admin/reports?date=2026-09-02&range=day", Current: true},
		{Label: "7 days", URL: "/admin/reports?date=2026-09-02"},
		{Label: "28 days", URL: "/admin/reports?date=2026-09-02&days=28"},
	}, data["Range"])
	assert.Equal(t, "On time was 78% on Wednesday 2 September", data["Headline"])
	assert.Equal(t, "Straight is the route pulling it down: 21% of its stops were early, most of them on trips leaving between 08:00 and 10:00. Times are America/Los_Angeles time.", data["Lede"])
	assert.Equal(t, analyticsTiles{Split: onTimeSplit{Known: true, Percent: 78, Early: 13, Late: 9}, Ran: 7, Scheduled: 9, NotRun: 2, Early: 3, Late: 2}, data["Tiles"])
	assert.Nil(t, data["Metrics"], "a day has tiles, not a trend")
	assert.Nil(t, data["Compare"])

	rows := data["Routes"].([]reportRouteRow)
	require.Len(t, rows, 3)
	assert.Equal(t, reportRouteRow{
		RouteID: "R1", Name: "Straight",
		Badge:     tripRoute{ID: "R1", Name: "1", LongName: "Straight", Color: "#0077C0", TextColor: "#FFFFFF"},
		Scheduled: 7, Ran: 5,
		OnTime: "64.3%", EarlyShare: "21.4%", LateShare: "14.3%", Unknown: "6.7%",
		Early: 3, Late: 2, Split: onTimeSplit{Known: true, Percent: 64, Early: 21, Late: 14},
		TripsURL: "/admin/reports?date=2026-09-02&route_id=R1",
	}, rows[1])

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<h2 class="h1">On time was 78% on Wednesday 2 September</h2>`)
	assert.Contains(t, html, `href="/api/v1/admin/reports/service?date=2026-09-02&amp;format=csv"`)
	assert.Contains(t, html, `<a href="/admin/reports?date=2026-09-02&amp;range=day" aria-current="true">Day</a><a href="/admin/reports?date=2026-09-02">7 days</a><a href="/admin/reports?date=2026-09-02&amp;days=28">28 days</a>`)
	assert.Contains(t, html, `<input type="hidden" name="range" value="day">`)
	assert.Contains(t, html, `name="date" value="2026-09-02" aria-label="Service day"`)
	assert.Contains(t, html, `<a class="row-link an-route" href="/admin/reports?date=2026-09-02&amp;route_id=R1">`)
	assert.Contains(t, html, `<b class="num">64.3%</b>`)
	assert.Contains(t, html, `aria-label="21% early, 64% on time, 14% late"`)
	assert.Contains(t, html, "64.3% on time, 21.4% early, 14.3% late, 6.7% no report", "the shares written out for a phone")
	assert.Contains(t, html, `<td class="num right tone-early hide-phone">21.4%</td><td class="num right tone-late hide-phone">14.3%</td><td class="num right muted hide-phone">6.7%</td>`)
	assert.Contains(t, html, `<span class="count-up" data-count="7">7</span><span class="tile-unit">of 9</span></span><span class="tile-note">2 not run</span>`)
	assert.Contains(t, html, "3 stops more than 1 min ahead")
	assert.Contains(t, html, "2 stops more than 5 min behind")
	assert.Contains(t, html, "A stop is on time when the vehicle left it from 1 min early to 5 min late, or reached the last stop in that window.")
	assert.Contains(t, html, `data-tip="11:00 to 12:00|67% late, 0% early, 33% on time (1 trip)"`)
	assert.Contains(t, html, `data-tip="Up to 1 min early|5 trips"`)
	assert.Contains(t, html, "counted as on time")
	assert.Equal(t, 2, strings.Count(html, "Show these numbers as a table"), "no trend on a day, so a table behind each of two charts")
	assert.NotContains(t, html, "Compare with")
}

// TestReportsPage_Period: with no range the page covers seven days, each a
// report of its own. The fixture has one day of trips, a Wednesday, in a
// feed whose weekday trips are scheduled on the days around it too.
func TestReportsPage_Period(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "On time was 78% over the 7 days to Wednesday 2 September", data["Headline"])
	assert.Equal(t, "/admin/reports?date=2026-08-26", data["PrevURL"], "a period at a time")
	assert.Equal(t, "/admin/reports?date=2026-09-04", data["NextURL"], "and no further than today")
	assert.Nil(t, data["CSVURL"], "the API has a day's report, not a period's")
	assert.Equal(t, analyticsLink{Label: "Compare with the 7 days before", URL: "/admin/reports?compare=off&date=2026-09-02", Current: true}, data["Compare"])
	assert.Nil(t, data["Tiles"])

	metrics := data["Metrics"].([]analyticsMetric)
	require.Len(t, metrics, 5)
	assert.Equal(t, analyticsMetric{Label: "On time", Value: "78%", Note: "18 of 23 stops", URL: "/admin/reports?date=2026-09-02", Selected: true}, metrics[0])
	assert.Equal(t, analyticsMetric{Label: "Trips run", Value: "15%", Note: "1 of 7 a day", URL: "/admin/reports?date=2026-09-02&metric=trips_run"}, metrics[1])
	assert.Equal(t, analyticsMetric{Label: "Early", Value: "13%", Note: "3 stops", Tone: "early", URL: "/admin/reports?date=2026-09-02&metric=early"}, metrics[2])
	assert.Equal(t, analyticsMetric{Label: "Late", Value: "9%", Note: "2 stops", Tone: "late", URL: "/admin/reports?date=2026-09-02&metric=late"}, metrics[3])
	assert.Equal(t, analyticsMetric{Label: "Stops with no report", Value: "4%", Note: "1 of 24 stops", URL: "/admin/reports?date=2026-09-02&metric=no_report"}, metrics[4])

	trend := data["Trend"].(analyticsTrend)
	assert.Equal(t, "On time", trend.Label)
	assert.Equal(t, "ok", trend.Tone)
	assert.Equal(t, "27 Aug to 2 Sep", trend.Period)
	assert.Equal(t, "The 7 days before", trend.Before)
	require.Len(t, trend.Rows, 7)
	assert.Equal(t, analyticsTrendRow{Day: "Thu 27 Aug", Value: "no figure", Before: "no figure"}, trend.Rows[0])
	assert.Equal(t, analyticsTrendRow{Day: "Wed 2 Sep", Value: "78%", Before: "no figure"}, trend.Rows[6])
	assert.Equal(t, "On time by day, 27 Aug to 2 Sep", trend.Chart.Name)
	assert.Len(t, trend.Chart.Dots, 1, "the one day with a figure has no line to be on")

	rows := data["Routes"].([]reportRouteRow)
	require.Len(t, rows, 3)
	assert.Equal(t, 36, rows[1].Scheduled, "seven trips on each of five weekdays, and one more a Saturday has")
	assert.Equal(t, 5, rows[1].Ran)
	assert.Equal(t, "/admin/reports?date=2026-09-02&route_id=R1", rows[1].TripsURL, "a route opens on the last day shown")

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<a class="metric" href="/admin/reports?date=2026-09-02" aria-current="true"><span class="tile-label">On time</span><span class="tile-value">78%</span><span class="tile-note">18 of 23 stops</span></a>`)
	assert.Contains(t, html, `<span class="tile-value tone-late">9%</span>`)
	assert.Contains(t, html, `<div class="an-chart line-ok">`)
	assert.Contains(t, html, `role="img" aria-label="On time by day, 27 Aug to 2 Sep"`)
	assert.Contains(t, html, `data-tip="2 Sep|78% (before: no figure)"`)
	assert.Contains(t, html, `<a class="chip" href="/admin/reports?compare=off&amp;date=2026-09-02" aria-current="true">`)
	assert.Contains(t, html, `<th class="right">On time</th><th class="right">The 7 days before</th>`)
	assert.Contains(t, html, `<tr><td>Wed 2 Sep</td><td class="num right">78%</td><td class="num right muted">no figure</td></tr>`)
	assert.Contains(t, html, `name="date" value="2026-09-02" aria-label="Last day shown"`)
	assert.NotContains(t, html, `name="range"`)
	assert.NotContains(t, html, "Download CSV")
	assert.Equal(t, 3, strings.Count(html, "Show these numbers as a table"))

	// Another measure, with nothing drawn behind it.
	data, _ = reportsView(t, r, "date=2026-09-02&metric=late&compare=off")
	trend = data["Trend"].(analyticsTrend)
	assert.Equal(t, "Late", trend.Label)
	assert.Empty(t, trend.Before)
	assert.Empty(t, trend.Chart.PrevPath)
	assert.Equal(t, "10%", trend.Chart.Grid[2].Label, "9% late is drawn under a line at 10")
	assert.Equal(t, analyticsLink{Label: "Compare with the 7 days before", URL: "/admin/reports?date=2026-09-02&metric=late"}, data["Compare"])
	html = renderReportsContent(t, data)
	assert.Contains(t, html, `<div class="an-chart line-late">`)
	assert.Contains(t, html, `<input type="hidden" name="metric" value="late"><input type="hidden" name="compare" value="off">`)
	assert.Contains(t, html, `data-tip="2 Sep|9%"`)
	assert.NotContains(t, html, "The 7 days before")
}

// TestReportsPage_28Days: days=28 covers the 28 days ending on the date, a
// report each, and Compare with the 28 before them. The 56 days are worked
// out once and kept in the store, so a restarted server reads none again.
func TestReportsPage_28Days(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02&days=28")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "On time was 78% over the 28 days to Wednesday 2 September", data["Headline"])
	assert.Equal(t, []analyticsLink{
		{Label: "Day", URL: "/admin/reports?date=2026-09-02&range=day"},
		{Label: "7 days", URL: "/admin/reports?date=2026-09-02"},
		{Label: "28 days", URL: "/admin/reports?date=2026-09-02&days=28", Current: true},
	}, data["Range"])
	assert.Equal(t, "/admin/reports?date=2026-08-05&days=28", data["PrevURL"], "a period at a time")
	assert.Equal(t, "/admin/reports?date=2026-09-04&days=28", data["NextURL"], "and no further than today")
	assert.Equal(t, analyticsLink{Label: "Compare with the 28 days before", URL: "/admin/reports?compare=off&date=2026-09-02&days=28", Current: true}, data["Compare"])
	assert.Equal(t, 56, store.reads, "the 28 days and the 28 before them")
	assert.Equal(t, 56, store.saves, "every one of them older than yesterday, so kept")

	trend := data["Trend"].(analyticsTrend)
	require.Len(t, trend.Rows, 28)
	assert.Equal(t, "Thu 6 Aug", trend.Rows[0].Day)
	assert.Equal(t, analyticsTrendRow{Day: "Wed 2 Sep", Value: "78%", Before: "no figure"}, trend.Rows[27])
	assert.Equal(t, "6 Aug to 2 Sep", trend.Period)
	assert.Equal(t, "The 28 days before", trend.Before)

	metrics := data["Metrics"].([]analyticsMetric)
	require.Len(t, metrics, 5)
	assert.Equal(t, "/admin/reports?date=2026-09-02&days=28&metric=late", metrics[3].URL, "a measure keeps the period")
	rows := data["Routes"].([]reportRouteRow)
	require.Len(t, rows, 3)
	assert.Equal(t, 144, rows[1].Scheduled, "the 36 trips of each of four weeks")
	assert.Equal(t, "/admin/reports?date=2026-09-02&route_id=R1", rows[1].TripsURL)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<a href="/admin/reports?date=2026-09-02&amp;days=28" aria-current="true">28 days</a>`)
	assert.Contains(t, html, `<input type="hidden" name="days" value="28">`, "Show keeps the period")
	assert.Contains(t, html, `role="img" aria-label="On time by day, 6 Aug to 2 Sep"`)
	assert.Contains(t, html, `<a class="chip" href="/admin/reports?compare=off&amp;date=2026-09-02&amp;days=28" aria-current="true">`)

	restarted := newServiceReporter(store, r.index, testReportSettings)
	restarted.now = r.now
	again, _ := reportsView(t, restarted, "date=2026-09-02&days=28")
	assert.Equal(t, 56, store.reads, "a restarted server reads the kept days, not the trips again")
	assert.Equal(t, data["Headline"], again["Headline"])

	// Compare turned off over 7 days stays off there after a look at 28.
	data, _ = reportsView(t, r, "date=2026-09-02&days=28&compare=off")
	assert.Equal(t, "/admin/reports?compare=off&date=2026-09-02", data["Range"].([]analyticsLink)[1].URL)
	assert.Contains(t, renderReportsContent(t, data), `<input type="hidden" name="days" value="28"><input type="hidden" name="compare" value="off">`)
}

// TestReportsPage_BadDays: a days the page has no period for is the 7 days it
// opens on, as an unknown metric or range is.
func TestReportsPage_BadDays(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	for _, bad := range []string{"14", "1", "0", "-28", "month", "28days"} {
		data, status := reportsView(t, r, "date=2026-09-02&days="+bad)
		require.Equal(t, http.StatusOK, status, bad)
		assert.Equal(t, "On time was 78% over the 7 days to Wednesday 2 September", data["Headline"], bad)
		assert.True(t, data["Range"].([]analyticsLink)[1].Current, bad)
		assert.NotContains(t, renderReportsContent(t, data), `name="days"`, bad)
	}
}

// TestReportsPage_Default: with no date the page ends on yesterday, as the
// API's report does, so every day it reads is one the reporter keeps.
func TestReportsPage_Default(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "2026-09-03", data["Date"])
	assert.Equal(t, "On time was 78% over the last 7 days", data["Headline"])
	assert.Equal(t, "/admin/reports?date=2026-09-04", data["NextURL"])
	assert.Equal(t, 1, data["HistogramLeftOut"], "the trip run on the 3rd has no trail")
	assert.Contains(t, renderReportsContent(t, data), "1 trip that ran had no report at its last stop and is left out.")

	data, _ = reportsView(t, r, "date=2026-09-04")
	assert.Nil(t, data["NextURL"], "nothing has run after today")
	assert.Contains(t, renderReportsContent(t, data), `<span class="icon-btn off" aria-hidden="true">`)

	reads := store.reads
	reportsView(t, r, "")
	assert.Equal(t, reads, store.reads, "fourteen settled days, none read again")
}

// TestReportsPage_TodayIsKeptForAMinute: today's report is not one the
// reporter keeps, so the page reads it through the Overview's, and the two
// pages share one read of the day's trails a minute.
func TestReportsPage_TodayIsKeptForAMinute(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportDayStart.Add(12*time.Hour))
	recent := &overviewReports{}
	view := func() map[string]interface{} {
		data, _, err := reportsPageView(context.Background(), r, recent, url.Values{"date": {"2026-09-02"}, "range": {"day"}})
		require.NoError(t, err)
		return data
	}

	data := view()
	assert.Equal(t, true, data["IsToday"])
	assert.Contains(t, data["Headline"], "so far today")
	assert.Nil(t, data["NextURL"])
	reads := store.reads
	require.NotZero(t, reads)
	view()
	assert.Equal(t, reads, store.reads)
	assert.Contains(t, renderReportsContent(t, data), "not run yet")
}

func TestReportsPage_RouteTrips(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02&route_id=R1")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Straight", data["Route"].(reportRouteRow).Name)
	assert.Equal(t, "Straight on Wednesday 2 September", data["Headline"])
	assert.Equal(t, "64% of stops were on time. 5 of 7 scheduled trips ran. Times are America/Los_Angeles time.", data["Lede"])
	assert.Equal(t, "/admin/reports?date=2026-09-01&route_id=R1", data["PrevURL"], "the day picker stays on the route")
	assert.Equal(t, "/admin/reports?date=2026-09-02&range=day", data["AllRoutesURL"])
	assert.Equal(t, analyticsTiles{Split: onTimeSplit{Known: true, Percent: 64, Early: 21, Late: 14}, Ran: 5, Scheduled: 7, NotRun: 2, Early: 3, Late: 2}, data["Tiles"])

	trips := data["Trips"].([]reportTripRow)
	require.Len(t, trips, 7)
	byID := make(map[string]reportTripRow, len(trips))
	for _, tr := range trips {
		byID[tr.GtfsTripID] = tr
	}

	t1 := byID["T1"]
	assert.Equal(t, "08:00 to 08:10", t1.Scheduled)
	assert.Equal(t, []reportRunRow{{Vehicle: "Bus T1", Driver: "Driver T1", TrailURL: "/admin/trips/1"}}, t1.Runs)
	assert.Equal(t, reportDeviation{Text: "0:06 late", Tone: "on-time", Signed: "+0:06"}, t1.Departure)
	assert.Equal(t, reportDeviation{Text: "0:16 early", Tone: "on-time", Signed: "-0:16"}, t1.Arrival)
	assert.Equal(t, "100.0%", t1.OnTime)
	assert.Zero(t, t1.NoReport)

	assert.Equal(t, reportDeviation{Text: "2:54 early", Tone: "early", Signed: "-2:54"}, byID["RE"].Departure)
	assert.Equal(t, reportDeviation{Text: "7:06 late", Tone: "late", Signed: "+7:06"}, byID["RL"].Departure)
	assert.Equal(t, reportDeviation{Text: "3:34 late", Tone: "on-time", Signed: "+3:34"}, byID["RL"].Arrival)
	assert.Equal(t, 1, byID["RG"].NoReport)
	assert.Equal(t, "00:10 next day to 00:20 next day", byID["RM"].Scheduled)

	rn := byID["RN"]
	assert.Empty(t, rn.Runs)
	assert.Equal(t, reportDeviation{}, rn.Departure)
	assert.Equal(t, "", rn.OnTime)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<a class="back" href="/admin/reports?date=2026-09-02&amp;range=day">`)
	assert.Contains(t, html, "All routes</a>")
	assert.Contains(t, html, `<span class="route route-lg" style="background-color: #0077C0; color: #FFFFFF" title="Straight">1</span>Straight on Wednesday 2 September</h2>`)
	assert.Contains(t, html, `<a class="row-link" href="/admin/trips/1">T1</a><span class="sub">to North</span>`)
	assert.Contains(t, html, `<a class="btn btn-sm btn-plain" href="/admin/trips/1" aria-label="Replay Bus T1 on Straight">`)
	assert.Contains(t, html, `<span class="strong">RN</span>`)
	assert.Contains(t, html, `<span class="pill pill-warn">Not run</span>`)
	assert.Contains(t, html, `<span class="pill pill-early">2:54 early</span>`)
	assert.Contains(t, html, `<span class="pill pill-late">7:06 late</span>`)
	assert.Contains(t, html, `<span class="pill pill-ok">On time</span> <span class="muted">&#43;3:34</span>`, "late inside the window is on time, with the difference beside it")
	assert.Contains(t, html, "1 with no report")
	assert.Contains(t, html, `name="route_id" value="R1"`)
	assert.Contains(t, html, "A stop with no report within 2 min either side of the vehicle passing it is left out.")
	assert.NotContains(t, html, "By route")
}

func TestReportsPage_RouteWithNoServiceThatDay(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02&route_id=R9")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, data["NoRoute"])
	assert.Equal(t, "Route R9 on Wednesday 2 September", data["Headline"])
	html := renderReportsContent(t, data)
	assert.Contains(t, html, "Route R9 has no trips scheduled on this day.")
	assert.Contains(t, html, `<a class="btn" href="/admin/reports?date=2026-09-02&amp;range=day">All routes</a>`)
}

func TestReportsPage_EmptyDay(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow.AddDate(0, 0, 10))

	data, status := reportsView(t, r, "date=2026-09-06&range=day")
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, data["Routes"])
	assert.Equal(t, "No trips were scheduled on Sunday 6 September", data["Headline"])
	html := renderReportsContent(t, data)
	assert.Contains(t, html, "No trips scheduled on this day.")
	assert.Contains(t, html, "No stop was judged, so there is nothing to draw.")
	assert.Contains(t, html, "No trip had a report at its last stop, so there is nothing to draw.")
	assert.NotContains(t, html, "<svg class=\"chart\"")
}

func TestReportsPage_Errors(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2-9-2026&metric=late&compare=off")
	assert.Equal(t, http.StatusBadRequest, status)
	html := renderReportsContent(t, data)
	assert.Contains(t, html, `<div class="banner banner-error" role="alert" id="date-error">`)
	assert.Contains(t, html, "Date must be YYYY-MM-DD.")
	assert.Contains(t, html, `value="2-9-2026"`, "the date picker keeps what was asked for")
	assert.Contains(t, html, `aria-invalid="true" aria-describedby="date-error"`)
	assert.Contains(t, html, `<input type="hidden" name="metric" value="late"><input type="hidden" name="compare" value="off">`, "and so does the rest of the address")
	assert.NotContains(t, html, "Download CSV")

	noSchedule := newServiceReporter(store, func() *rider.Index { return nil }, testReportSettings)
	data, status = reportsView(t, noSchedule, "")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	html = renderReportsContent(t, data)
	assert.Contains(t, html, `<h2 class="h1">The schedule is not loaded</h2>`)
	assert.Contains(t, html, "The GTFS schedule has not loaded")
	assert.NotContains(t, html, `name="date"`)

	store.err = assert.AnError
	_, _, err := reportsPageView(context.Background(), r, &overviewReports{}, url.Values{"date": {"2026-09-02"}})
	assert.ErrorIs(t, err, assert.AnError)
}

// TestReportsPage_EscapesNames: a route's name and a trip's ids come from the
// agency's feed and a vehicle's from an admin; none reaches the page as
// markup.
func TestReportsPage_EscapesNames(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, _ := reportsView(t, r, "date=2026-09-02&route_id=R1")
	route := data["Route"].(reportRouteRow)
	route.Name = `<img src=x>`
	data["Route"] = route
	data["Headline"] = route.Name + " today"
	trips := data["Trips"].([]reportTripRow)
	trips[0].GtfsTripID, trips[0].Headsign = `<b>T</b>`, `"><script>`
	trips[0].Runs[0].Vehicle = `Bus "<1>"`
	html := renderReportsContent(t, data)
	assert.NotContains(t, html, "<img")
	assert.NotContains(t, html, "<b>T</b>")
	assert.NotContains(t, html, "<script>")
	assert.Contains(t, html, `aria-label="Replay Bus &#34;&lt;1&gt;&#34; on &lt;img src=x&gt;"`)

	data, _ = reportsView(t, r, "date=2026-09-02&range=day")
	rows := data["Routes"].([]reportRouteRow)
	rows[0].Name = `<img src=x>`
	html = renderReportsContent(t, data)
	assert.NotContains(t, html, "<img")
	assert.Contains(t, html, `aria-label="Trips on &lt;img src=x&gt;"`)
}

func TestPeriodPhrase(t *testing.T) {
	today := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	day := func(back int) time.Time { return today.AddDate(0, 0, -back) }
	assert.Equal(t, "today", periodPhrase(today, today, 1))
	assert.Equal(t, "yesterday", periodPhrase(day(1), today, 1))
	assert.Equal(t, "on Monday 16 November", periodPhrase(day(2), today, 1))
	assert.Equal(t, "on Tuesday 18 November 2025", periodPhrase(today.AddDate(-1, 0, 0), today, 1), "the year, when it is not this one")
	assert.Equal(t, "over the last 7 days", periodPhrase(today, today, 7))
	assert.Equal(t, "over the last 7 days", periodPhrase(day(1), today, 7))
	assert.Equal(t, "over the 7 days to Monday 16 November", periodPhrase(day(2), today, 7))
	assert.Equal(t, "over the 7 days to Thursday 19 November", periodPhrase(day(-1), today, 7), "a day yet to come is not the last 7")
}

func TestAnalyticsHeadline(t *testing.T) {
	judged := reportCounts{Scheduled: 9, Run: 7, OnTime: 18, Early: 3, Late: 2}
	for _, tc := range []struct {
		name    string
		total   reportCounts
		isToday bool
		want    string
	}{
		{"a share", judged, false, "On time was 78% yesterday"},
		{"a share today", judged, true, "On time is 78% so far today"},
		{"nothing scheduled", reportCounts{}, false, "No trips were scheduled yesterday"},
		{"nothing scheduled today", reportCounts{}, true, "No trips are scheduled today"},
		{"nothing ran", reportCounts{Scheduled: 9}, false, "No scheduled trip ran yesterday"},
		{"nothing has run", reportCounts{Scheduled: 9}, true, "No scheduled trip has run yet today"},
		{"nothing judged", reportCounts{Scheduled: 9, Run: 1, Unknown: 3}, false, "No stop was judged yesterday"},
		{"nothing judged yet", reportCounts{Scheduled: 9, Run: 1, Unknown: 3}, true, "No stop has been judged yet today"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, analyticsHeadline(tc.total, "yesterday", tc.isToday))
		})
	}
}

func TestComparedSentence(t *testing.T) {
	share := func(onTime int) reportCounts { return reportCounts{OnTime: onTime, Late: 100 - onTime} }
	assert.Equal(t, "That is 3 points higher than the 7 days before.", comparedSentence(share(86), share(83), 7))
	assert.Equal(t, "That is 1 point higher than the 7 days before.", comparedSentence(share(86), share(85), 7))
	assert.Equal(t, "That is 4 points lower than the 7 days before.", comparedSentence(share(86), share(90), 7))
	assert.Equal(t, "That is the same as the 7 days before.", comparedSentence(share(86), share(86), 7))
	assert.Empty(t, comparedSentence(share(86), reportCounts{}, 7), "nothing to set it against")
	assert.Empty(t, comparedSentence(reportCounts{}, share(86), 7))
}

func TestRouteDaySentences(t *testing.T) {
	assert.Equal(t, []string{"79% of stops were on time.", "80 of 88 scheduled trips ran."}, routeDaySentences(reportCounts{Scheduled: 88, Run: 80, OnTime: 79, Early: 3, Late: 18}, false))
	assert.Equal(t, []string{"0 of 1 scheduled trip ran."}, routeDaySentences(reportCounts{Scheduled: 1}, false))
	assert.Equal(t, []string{"3 of 88 scheduled trips have run so far."}, routeDaySentences(reportCounts{Scheduled: 88, Run: 3}, true))
	assert.Equal(t, []string{"1 of 1 scheduled trip has run so far."}, routeDaySentences(reportCounts{Scheduled: 1, Run: 1}, true))
}

func TestRouteTitle(t *testing.T) {
	assert.Equal(t, "Thika Road", routeTitle("R3", "3", " Thika Road "))
	assert.Equal(t, "Route 3", routeTitle("R3", "3", ""))
	assert.Equal(t, "Route R3", routeTitle("R3", " ", ""))
}

// sentenceDay is a day's report in UTC with one trip an hour on each route:
// trips maps a route's id to its trips' stop counts by the hour they leave.
func sentenceDay(date string, trips map[string]map[int]reportCounts) *serviceReport {
	day, _ := time.Parse(reportDateLayout, date)
	rep := &serviceReport{Date: date, location: time.UTC}
	ids := make([]string, 0, len(trips))
	for id := range trips {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		route := routeReport{RouteID: id, LongName: "Road " + id}
		for hour, c := range trips[id] {
			route.TripsScheduled++
			route.StopsOnTime += c.OnTime
			route.StopsEarly += c.Early
			route.StopsLate += c.Late
			route.Trips = append(route.Trips, tripReport{
				ScheduledDeparture: day.Add(time.Duration(hour) * time.Hour),
				StopsOnTime:        c.OnTime, StopsEarly: c.Early, StopsLate: c.Late,
			})
		}
		rep.Routes = append(rep.Routes, route)
	}
	return rep
}

func TestWorstRouteSentence(t *testing.T) {
	sentence := func(days ...*serviceReport) string {
		routes, total := routeTotals(totalsOf(days...))
		return worstRouteSentence(totalsOf(days...), routes, total)
	}
	good := map[int]reportCounts{7: {OnTime: 10}}

	late := sentenceDay("2026-11-17", map[string]map[int]reportCounts{
		"A": good,
		"B": {7: {OnTime: 6, Late: 4}, 8: {OnTime: 7, Late: 3}, 12: {OnTime: 9, Late: 1}, 17: {OnTime: 8, Late: 2}},
	})
	assert.Equal(t, "Road B is the route pulling it down: 25% of its stops were late, most of them on trips leaving between 07:00 and 09:00.", sentence(late))

	spread := sentenceDay("2026-11-17", map[string]map[int]reportCounts{
		"A": good,
		"B": {7: {OnTime: 8, Late: 2}, 10: {OnTime: 8, Late: 2}, 13: {OnTime: 8, Late: 2}, 16: {OnTime: 8, Late: 2}},
	})
	assert.Equal(t, "Road B is the route pulling it down: 20% of its stops were late.", sentence(spread), "no two hours hold over half of them")

	early := sentenceDay("2026-11-17", map[string]map[int]reportCounts{
		"A": good,
		"B": {23: {OnTime: 5, Early: 5}, 24: {OnTime: 5, Early: 4, Late: 1}},
	})
	assert.Equal(t, "Road B is the route pulling it down: 45% of its stops were early, most of them on trips leaving between 23:00 and 01:00.", sentence(early), "past midnight on the service day's clock")

	// Over two days the misses add up before the hours are weighed: 9 of the
	// 18 left between 07:00 and 09:00, and half is not most.
	assert.Equal(t, "Road B is the route pulling it down: 23% of its stops were late.", sentence(late, spread))

	assert.Empty(t, sentence(sentenceDay("2026-11-17", map[string]map[int]reportCounts{"A": good, "B": good})), "no route is behind the rest")
	assert.Empty(t, sentence(sentenceDay("2026-11-17", map[string]map[int]reportCounts{"B": {7: {OnTime: 6, Late: 4}}})), "one route is the whole of it")
	assert.Empty(t, sentence(sentenceDay("2026-11-17", map[string]map[int]reportCounts{"A": {7: {}}, "B": {7: {OnTime: 6, Late: 4}}})), "a route with nothing judged is not one to set it against")
}

func TestRouteTotals(t *testing.T) {
	older := dayTotals{Routes: []routeDayTotals{
		{RouteID: "B", ShortName: "old", Hours: []hourTotals{{Hour: 7, Scheduled: 2, Run: 2, OnTime: 4}, {Hour: 9, Scheduled: 1, Unknown: 1}}},
		{RouteID: "GONE", Hours: []hourTotals{{Hour: 7, Scheduled: 1}}},
	}}
	newer := dayTotals{Routes: []routeDayTotals{
		{RouteID: "A", Hours: []hourTotals{{Hour: 6, Scheduled: 5, Run: 5, OnTime: 9, Early: 1}}},
		{RouteID: "B", ShortName: "new", Hours: []hourTotals{{Hour: 7, Scheduled: 3, Run: 3, OnTime: 5, Late: 1}}},
	}}

	routes, total := routeTotals([]dayTotals{older, newer})

	require.Len(t, routes, 3)
	assert.Equal(t, routeTotal{ID: "A", reportCounts: reportCounts{Scheduled: 5, Run: 5, OnTime: 9, Early: 1}}, routes[0], "the newest day's order")
	assert.Equal(t, routeTotal{ID: "B", ShortName: "new", reportCounts: reportCounts{Scheduled: 6, Run: 5, OnTime: 9, Late: 1, Unknown: 1}}, routes[1], "and its names")
	assert.Equal(t, "GONE", routes[2].ID)
	assert.Equal(t, reportCounts{Scheduled: 12, Run: 10, OnTime: 18, Early: 1, Late: 1, Unknown: 1}, total)
}

func TestReportCounts_Measure(t *testing.T) {
	c := reportCounts{Scheduled: 8, Run: 6, OnTime: 6, Early: 1, Late: 3, Unknown: 10}
	assert.InDelta(t, 60, *c.measure(metricOnTime), 1e-9)
	assert.InDelta(t, 75, *c.measure(metricTripsRun), 1e-9)
	assert.InDelta(t, 10, *c.measure(metricEarly), 1e-9)
	assert.InDelta(t, 30, *c.measure(metricLate), 1e-9)
	assert.InDelta(t, 50, *c.measure(metricNoReport), 1e-9, "of every stop, judged or not")
	for _, m := range analyticsMetrics {
		assert.Nil(t, reportCounts{}.measure(m.Key), m.Key)
	}
	assert.Equal(t, onTimeSplit{Known: true, Percent: 60, Early: 10, Late: 30}, c.split())
	assert.Equal(t, onTimeSplit{}, reportCounts{Unknown: 4}.split())
}

func TestHoursOfDay(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	// The fixture day starts at 07:00 UTC, midnight in Los Angeles.
	at := func(d time.Duration) time.Time { return reportDayStart.Add(d) }
	rep := &serviceReport{Date: "2026-09-02", location: la, Routes: []routeReport{
		{Trips: []tripReport{
			{ScheduledDeparture: at(hms(6, 59, 0)), StopsOnTime: 2, StopsLate: 1, StopsUnknown: 4},
			{ScheduledDeparture: at(hms(6, 0, 0)), StopsEarly: 1},
		}},
		{Trips: []tripReport{
			{ScheduledDeparture: at(hms(9, 30, 0))},
			{ScheduledDeparture: at(hms(24, 10, 0)), StopsOnTime: 3},
		}},
	}}

	hours := hoursOfDay(totalsOf(rep, rep))

	require.Len(t, hours, 19, "06:00 to the hour after midnight, with none left out")
	assert.Equal(t, hourShares{Hour: 6, Trips: 4, reportCounts: reportCounts{OnTime: 4, Early: 2, Late: 2}}, hours[0], "both days' trips, and no stop without a verdict")
	assert.Equal(t, hourShares{Hour: 7}, hours[1])
	assert.Equal(t, hourShares{Hour: 9, Trips: 2}, hours[3])
	assert.Equal(t, hourShares{Hour: 24, Trips: 2, reportCounts: reportCounts{OnTime: 6}}, hours[18], "after midnight sorts after the evening")
	assert.Empty(t, hoursOfDay(totalsOf(&serviceReport{Date: "2026-09-02", location: la})))
}

func TestDeviationBuckets(t *testing.T) {
	dev := func(seconds int) tripReport {
		return tripReport{Runs: []tripRun{{ID: 1}}, ArrivalDeviationS: &seconds}
	}
	rep := &serviceReport{Date: "2026-09-02", location: time.UTC, Routes: []routeReport{{Trips: []tripReport{
		dev(0), dev(59), dev(-1), dev(-60), dev(-61), dev(300), dev(301), dev(-3600), dev(3600),
		{Runs: []tripRun{{ID: 2}}},
		{},
	}}}}

	buckets, leftOut := deviationBuckets(totalsOf(rep), testReportSettings)

	assert.Equal(t, 1, leftOut, "ran, with no report at the last stop; a trip not run is neither")
	require.Len(t, buckets, 17)
	assert.Equal(t, -4, buckets[0].Minute)
	assert.Equal(t, 12, buckets[16].Minute)
	byMinute := make(map[int]deviationBucket)
	for _, b := range buckets {
		if b.trips() > 0 {
			byMinute[b.Minute] = b
		}
	}
	assert.Equal(t, map[int]deviationBucket{
		-4: {Minute: -4, Early: 1},
		-2: {Minute: -2, Early: 1},
		-1: {Minute: -1, OnTime: 2},
		0:  {Minute: 0, OnTime: 2},
		5:  {Minute: 5, OnTime: 1, Late: 1},
		12: {Minute: 12, Late: 1},
	}, byMinute, "a minute the window ends inside has both verdicts; the ends hold everything past them")

	buckets, leftOut = deviationBuckets(totalsOf(&serviceReport{Date: "2026-09-02", location: time.UTC, Routes: []routeReport{{Trips: []tripReport{{Runs: []tripRun{{ID: 2}}}}}}}), testReportSettings)
	assert.Nil(t, buckets)
	assert.Equal(t, 1, leftOut)
}

func TestNewAnalyticsMetrics(t *testing.T) {
	query := analyticsQuery{Date: "2026-11-17", Days: 7, Metric: metricLate, Compare: true}
	total := reportCounts{Scheduled: 1848, Run: 1764, OnTime: 860, Early: 30, Late: 110, Unknown: 64}
	before := reportCounts{Scheduled: 1848, Run: 1700, OnTime: 830, Early: 30, Late: 140, Unknown: 75}

	metrics := newAnalyticsMetrics(query, total, &before)

	require.Len(t, metrics, 5)
	notes := make([]string, 0, 5)
	for _, m := range metrics {
		notes = append(notes, m.Value+" "+m.Note)
	}
	assert.Equal(t, []string{"86% 3 points up", "95% 252 of 264 a day", "3% unchanged", "11% 3 points down", "6% 1 point down"}, notes)
	assert.True(t, metrics[3].Selected)
	assert.False(t, metrics[0].Selected)
	assert.Equal(t, "/admin/reports?date=2026-11-17&metric=trips_run", metrics[1].URL)

	alone := newAnalyticsMetrics(query, total, nil)
	assert.Equal(t, "860 of 1000 stops", alone[0].Note)
	assert.Equal(t, "30 stops", alone[2].Note)
	assert.Equal(t, "64 of 1064 stops", alone[4].Note)

	// A period with nothing to set a share against keeps its counts.
	nothingBefore := newAnalyticsMetrics(query, total, &reportCounts{})
	assert.Equal(t, "860 of 1000 stops", nothingBefore[0].Note)

	empty := newAnalyticsMetrics(query, reportCounts{}, nil)
	assert.Equal(t, "None", empty[0].Value)
	assert.Equal(t, "0 of 0 stops", empty[0].Note)
}

func TestNewAnalyticsTrend_Bounds(t *testing.T) {
	day := func(date string, c reportCounts) dayTotals {
		return dayTotals{Date: date, Routes: []routeDayTotals{{Hours: []hourTotals{{Scheduled: c.Scheduled, Run: c.Run, OnTime: c.OnTime, Early: c.Early, Late: c.Late}}}}}
	}
	days := []dayTotals{
		day("2026-11-16", reportCounts{OnTime: 64, Late: 36}),
		day("2026-11-17", reportCounts{OnTime: 90, Late: 10}),
	}
	before := []dayTotals{
		day("2026-11-14", reportCounts{OnTime: 95, Late: 5}),
		day("2026-11-15", reportCounts{}),
	}
	grid := func(metric string, before []dayTotals) []string {
		trend := newAnalyticsTrend(analyticsQuery{Days: 2, Metric: metric}, days, before)
		labels := make([]string, 0, 3)
		for _, g := range trend.Chart.Grid {
			labels = append(labels, g.Label)
		}
		return labels
	}

	assert.Equal(t, []string{"40%", "70%", "100%"}, grid(metricOnTime, before), "from the twenty under the lowest day")
	assert.Equal(t, []string{"0%", "20%", "40%"}, grid(metricLate, nil), "to the ten over the highest day")
	assert.Equal(t, []string{"0%", "5%", "10%"}, grid(metricEarly, nil), "never a flat chart")

	trend := newAnalyticsTrend(analyticsQuery{Days: 2, Metric: metricOnTime}, days, before)
	assert.Equal(t, "16 Nov to 17 Nov", trend.Period)
	assert.Equal(t, "The 2 days before", trend.Before)
	assert.Equal(t, []analyticsTrendRow{{Day: "Mon 16 Nov", Value: "64%", Before: "95%"}, {Day: "Tue 17 Nov", Value: "90%", Before: "no figure"}}, trend.Rows)

	high := []dayTotals{day("2026-11-17", reportCounts{OnTime: 99, Late: 1})}
	trend = newAnalyticsTrend(analyticsQuery{Days: 1, Metric: metricOnTime}, high, nil)
	assert.Equal(t, "60%", trend.Chart.Grid[0].Label, "the floor is never over 60")
}

// TestReportsPage_MatchesJSONAndCSV: the page, the JSON and the CSV are one
// report, so every route's numbers are the same in all three.
func TestReportsPage_MatchesJSONAndCSV(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	var rep serviceReport
	jsonRec := getServiceReport(t, r, "?date=2026-09-02")
	require.Equal(t, http.StatusOK, jsonRec.Code)
	require.NoError(t, json.Unmarshal(jsonRec.Body.Bytes(), &rep))
	csvRec := getServiceReport(t, r, "?date=2026-09-02&format=csv")
	records, err := csv.NewReader(csvRec.Body).ReadAll()
	require.NoError(t, err)
	data, _ := reportsView(t, r, "date=2026-09-02&range=day")
	rows := data["Routes"].([]reportRouteRow)
	html := renderReportsContent(t, data)

	require.Len(t, rows, len(rep.Routes))
	require.Len(t, records, len(rep.Routes)+1)
	pct := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', 1, 64) + "%"
	}
	withPercentSign := func(cell string) string {
		if cell == "" {
			return ""
		}
		return cell + "%"
	}
	for i, route := range rep.Routes {
		row, record := rows[i], records[i+1]
		assert.Equal(t, route.RouteID, row.RouteID)
		assert.Equal(t, route.RouteID, record[1])
		assert.Equal(t, route.TripsScheduled, row.Scheduled)
		assert.Equal(t, strconv.Itoa(route.TripsScheduled), record[4])
		assert.Equal(t, route.TripsRun, row.Ran)
		assert.Equal(t, strconv.Itoa(route.TripsRun), record[5])
		assert.Equal(t, pct(route.OnTimePercent), row.OnTime)
		assert.Equal(t, row.OnTime, withPercentSign(record[10]))
		assert.Equal(t, pct(route.UnknownPercent), row.Unknown)
		assert.Equal(t, row.Unknown, withPercentSign(record[11]))
		assert.Contains(t, html, row.OnTime)
		assert.Contains(t, html, row.Unknown)
	}
}

// newReportsHandler is the server's whole handler, admin UI on, with r as its
// reporter.
func newReportsHandler(t *testing.T, r *serviceReporter) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(&noopStore{}, testAlertSettings), r, newFeedHealth(time.Now))
	require.NoError(t, err)
	return h
}

func serve(h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReportsPage_SignedOut(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	h := newReportsHandler(t, r)

	page := serve(h, "/admin/reports?date=2026-09-02", nil)
	assert.Equal(t, http.StatusSeeOther, page.Code)
	assert.Equal(t, loginLocation(http.MethodGet, "/admin/reports?date=2026-09-02"), page.Header().Get("Location"))

	api := serve(h, "/api/v1/admin/reports/service?date=2026-09-02&format=csv", nil)
	assert.Equal(t, http.StatusUnauthorized, api.Code)
	assert.Zero(t, store.reads, "nothing is worked out for a visitor who is not signed in")
}

func TestReportsPage_SignedIn(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)
	h := newReportsHandler(t, r)
	admin := cookieFor(t, "admin")

	rec := serve(h, "/admin/reports?date=2026-09-02", admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<title>Analytics · Vehicle Tracker</title>")
	assert.Contains(t, body, `<h1 class="topbar-title">Analytics</h1>`)
	assert.Equal(t, railOpen, railLink(body, "/admin/reports"), "the menu marks Analytics as the page open")
	assert.Contains(t, body, "On time was 78% over the 7 days to Wednesday 2 September")
	assert.Contains(t, body, "Straight</a>")
	assert.Contains(t, body, "64.3%")
	assert.Contains(t, body, `<div class="tip" id="tip" aria-hidden="true"></div>`)

	rec = serve(h, "/admin/reports?date=2026-09-02&route_id=R1", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `href="/admin/trips/1"`)

	rec = serve(h, "/admin/reports?date=13-11-2026", admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "Date must be YYYY-MM-DD.")

	// The page's Download CSV link goes to the API, which takes the same
	// session cookie.
	rec = serve(h, "/api/v1/admin/reports/service?date=2026-09-02&format=csv", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/csv", rec.Header().Get("Content-Type"))
}

func TestReportsPage_DriverSession(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	h := newReportsHandler(t, r)

	page := serve(h, "/admin/reports", cookieFor(t, "driver"))
	assert.Equal(t, http.StatusSeeOther, page.Code)
	api := serve(h, "/api/v1/admin/reports/service", cookieFor(t, "driver"))
	assert.Equal(t, http.StatusForbidden, api.Code)
	assert.Zero(t, store.reads)
}

// heldReportStore holds the read of each day's trips until the test lets that
// day go, so a test can see which days are worked out at the same time. The
// day whose read fails, if any, fails at once.
type heldReportStore struct {
	loc  *time.Location
	fail string
	err  error

	mu    sync.Mutex
	gates map[string]chan struct{}
	// held counts the reads of each day waiting now, so two reads of one
	// day count as two.
	held     map[string]int
	mostHeld int
	read     []string
}

func newHeldReportStore(ix *rider.Index) *heldReportStore {
	return &heldReportStore{loc: ix.Timezone(), gates: make(map[string]chan struct{}), held: make(map[string]int)}
}

// gate is the channel that lets date's read go, made on first use.
func (s *heldReportStore) gate(date string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gates[date]
	if !ok {
		g = make(chan struct{})
		s.gates[date] = g
	}
	return g
}

// release lets date's reads go, and any later read of it at once.
func (s *heldReportStore) release(date string) {
	gate := s.gate(date)
	select {
	case <-gate:
	default:
		close(gate)
	}
}

// ListTripsStartedBetween reads from the start of the day before the one
// reported, which in the fixture's late summer is that day's local midnight.
func (s *heldReportStore) ListTripsStartedBetween(ctx context.Context, from, _ time.Time) ([]TripSummary, error) {
	date := from.In(s.loc).AddDate(0, 0, 1).Format(reportDateLayout)
	s.mu.Lock()
	s.read = append(s.read, date)
	s.mu.Unlock()
	if date == s.fail {
		return nil, s.err
	}
	gate := s.gate(date)
	s.mu.Lock()
	s.held[date]++
	s.mostHeld = max(s.mostHeld, s.holding())
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.held[date]--; s.held[date] == 0 {
			delete(s.held, date)
		}
		s.mu.Unlock()
	}()
	select {
	case <-gate:
		return make([]TripSummary, 0), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *heldReportStore) ListTrails(context.Context, []int64) (map[int64][]LocationPoint, error) {
	return make(map[int64][]LocationPoint), nil
}

// The held store keeps no days, so every day is worked out.
func (s *heldReportStore) ListServiceDays(context.Context, time.Time, time.Time) ([]storedServiceDay, error) {
	return nil, nil
}

func (s *heldReportStore) SaveServiceDay(context.Context, storedServiceDay) error { return nil }

// holding is how many reads are waiting now. The caller holds s.mu.
func (s *heldReportStore) holding() int {
	n := 0
	for _, count := range s.held {
		n += count
	}
	return n
}

// heldDays are the days whose reads are waiting now, oldest first, a day read
// twice at once listed twice.
func (s *heldReportStore) heldDays() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	days := make([]string, 0, len(s.held))
	for d, count := range s.held {
		for range count {
			days = append(days, d)
		}
	}
	slices.Sort(days)
	return days
}

func (s *heldReportStore) readDays() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.read)
}

type analyticsDaysResult struct {
	reps []*serviceReport
	err  error
}

// heldWeek is the week analyticsDays is asked for in these tests, oldest
// first, a month before today so no day goes through the Overview's cache.
var heldWeek = []string{"2026-08-27", "2026-08-28", "2026-08-29", "2026-08-30", "2026-08-31", "2026-09-01", "2026-09-02"}

// weekBefore is the week before heldWeek, oldest first.
var weekBefore = []string{"2026-08-20", "2026-08-21", "2026-08-22", "2026-08-23", "2026-08-24", "2026-08-25", "2026-08-26"}

// newHeldReporter is a reporter over store a month after heldWeek, which is
// settled by then and kept once read.
func newHeldReporter(store serviceReportStore, ix *rider.Index) *serviceReporter {
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return reportDayStart.AddDate(0, 0, 30) }
	return r
}

// startAnalyticsDays works out heldWeek against store in the background.
func startAnalyticsDays(ctx context.Context, store serviceReportStore, ix *rider.Index) <-chan analyticsDaysResult {
	return startAnalyticsWeek(ctx, newHeldReporter(store, ix), ix, reportDay)
}

// startAnalyticsWeek works out the seven days ending on last with r in the
// background, as one request for the Analytics page does.
func startAnalyticsWeek(ctx context.Context, r *serviceReporter, ix *rider.Index, last time.Time) <-chan analyticsDaysResult {
	today := reportDay.AddDate(0, 0, 30)
	done := make(chan analyticsDaysResult, 1)
	go func() {
		days := make([]time.Time, len(heldWeek))
		for i := range days {
			days[i] = last.AddDate(0, 0, i+1-len(heldWeek))
		}
		reps, err := analyticsDays(ctx, r, &overviewReports{}, ix, today, days)
		done <- analyticsDaysResult{reps, err}
	}()
	return done
}

func awaitAnalyticsDays(t *testing.T, done <-chan analyticsDaysResult) analyticsDaysResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		require.FailNow(t, "analyticsDays did not return")
		return analyticsDaysResult{}
	}
}

func TestAnalyticsDays_WorksOutAFewAtOnceOldestFirst(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	done := startAnalyticsDays(context.Background(), store, ix)

	for left := heldWeek; len(left) > 0; {
		batch := left[:min(reportReadsAtOnce, len(left))]
		require.Eventually(t, func() bool { return slices.Equal(batch, store.heldDays()) }, 5*time.Second, time.Millisecond,
			"the oldest days not yet read are read at the same time")
		assert.Never(t, func() bool { return len(store.heldDays()) > reportReadsAtOnce }, 100*time.Millisecond, time.Millisecond,
			"no more days start while those are read")

		// Let them go newest first, so they finish out of order.
		for i := len(batch) - 1; i >= 0; i-- {
			store.release(batch[i])
		}
		left = left[len(batch):]
	}

	res := awaitAnalyticsDays(t, done)
	require.NoError(t, res.err)
	dates := make([]string, 0, len(res.reps))
	for _, rep := range res.reps {
		dates = append(dates, rep.Date)
	}
	assert.Equal(t, heldWeek, dates, "oldest first, as before")
	assert.Equal(t, reportReadsAtOnce, store.mostHeld)
}

func TestAnalyticsDays_AFailedDayStopsTheRest(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	store.fail = heldWeek[0]
	store.err = errors.New("the trips could not be read")
	done := startAnalyticsDays(context.Background(), store, ix)

	// The days already being read are let go only by the failure.
	res := awaitAnalyticsDays(t, done)
	require.ErrorIs(t, res.err, store.err)
	assert.Nil(t, res.reps)
	assert.Subset(t, heldWeek[:reportReadsAtOnce], store.readDays(), "no day starts after the failure")
}

func TestAnalyticsDays_ACancelledRequestStopsTheRest(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	ctx, cancel := context.WithCancel(context.Background())
	done := startAnalyticsDays(ctx, store, ix)

	require.Eventually(t, func() bool { return len(store.heldDays()) == reportReadsAtOnce }, 5*time.Second, time.Millisecond)
	cancel()

	res := awaitAnalyticsDays(t, done)
	require.ErrorIs(t, res.err, context.Canceled)
	assert.Nil(t, res.reps)
	assert.ElementsMatch(t, heldWeek[:reportReadsAtOnce], store.readDays(), "no day starts after the request ends")
	assert.Eventually(t, func() bool { return len(store.heldDays()) == 0 }, 5*time.Second, time.Millisecond,
		"the reads nobody waits for any more are cancelled")
}

// releaseAsHeld lets each day go once its read is held, checking that no more
// than reportReadsAtOnce are held at a time, until n days have been let go.
func releaseAsHeld(t *testing.T, store *heldReportStore, n int) {
	t.Helper()
	for released := make(map[string]bool); len(released) < n; {
		want := min(reportReadsAtOnce, n-len(released))
		require.Eventually(t, func() bool { return len(store.heldDays()) == want }, 5*time.Second, time.Millisecond,
			"the slots are used while days are left; held %v", store.heldDays())
		assert.Never(t, func() bool { return len(store.heldDays()) > reportReadsAtOnce }, 50*time.Millisecond, time.Millisecond,
			"no more than %d reads at once", reportReadsAtOnce)
		for _, d := range store.heldDays() {
			released[d] = true
			store.release(d)
		}
	}
}

func reportDates(reps []*serviceReport) []string {
	dates := make([]string, 0, len(reps))
	for _, rep := range reps {
		dates = append(dates, rep.Date)
	}
	return dates
}

// waitingFor is how many requests wait for the read of date under way.
func waitingFor(r *serviceReporter, date string) int {
	day, _ := time.Parse(reportDateLayout, date)
	r.mu.Lock()
	defer r.mu.Unlock()
	if read, ok := r.reading[day.Format(gtfsDateLayout)]; ok {
		return read.waiting
	}
	return 0
}

// Two cold loads of the same week, say an admin refreshing a slow page, read
// each day once between them, two at a time.
func TestAnalyticsDays_TwoRequestsForTheSameWeekShareItsReads(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	r := newHeldReporter(store, ix)

	first := startAnalyticsWeek(context.Background(), r, ix, reportDay)
	require.Eventually(t, func() bool { return len(store.heldDays()) == reportReadsAtOnce }, 5*time.Second, time.Millisecond)
	second := startAnalyticsWeek(context.Background(), r, ix, reportDay)
	releaseAsHeld(t, store, len(heldWeek))

	for _, done := range []<-chan analyticsDaysResult{first, second} {
		res := awaitAnalyticsDays(t, done)
		require.NoError(t, res.err)
		assert.Equal(t, heldWeek, reportDates(res.reps))
	}
	assert.ElementsMatch(t, heldWeek, store.readDays(), "each day read once")
	assert.Equal(t, 2, store.mostHeld, "two reads at once, leaving two of the four connections a 1 vCPU host has")
}

// Two loads of different weeks, or a period and the one before it that
// Compare reads, share the two slots too.
func TestAnalyticsDays_TwoRequestsForDifferentWeeksShareTheSlots(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	r := newHeldReporter(store, ix)

	first := startAnalyticsWeek(context.Background(), r, ix, reportDay)
	require.Eventually(t, func() bool { return len(store.heldDays()) == reportReadsAtOnce }, 5*time.Second, time.Millisecond)
	second := startAnalyticsWeek(context.Background(), r, ix, reportDay.AddDate(0, 0, -len(heldWeek)))
	releaseAsHeld(t, store, len(heldWeek)+len(weekBefore))

	res := awaitAnalyticsDays(t, first)
	require.NoError(t, res.err)
	assert.Equal(t, heldWeek, reportDates(res.reps))
	res = awaitAnalyticsDays(t, second)
	require.NoError(t, res.err)
	assert.Equal(t, weekBefore, reportDates(res.reps))
	assert.ElementsMatch(t, slices.Concat(weekBefore, heldWeek), store.readDays())
	assert.Equal(t, 2, store.mostHeld, "two reads at once, leaving two of the four connections a 1 vCPU host has")
}

// A request that ends while its days wait for a slot stops waiting at once
// and reads none of them.
func TestAnalyticsDays_ACancelledRequestWaitingForASlotReadsNothing(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	r := newHeldReporter(store, ix)

	first := startAnalyticsWeek(context.Background(), r, ix, reportDay)
	require.Eventually(t, func() bool { return len(store.heldDays()) == reportReadsAtOnce }, 5*time.Second, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	second := startAnalyticsWeek(ctx, r, ix, reportDay.AddDate(0, 0, -len(heldWeek)))
	assert.Never(t, func() bool { return len(store.heldDays()) > reportReadsAtOnce }, 100*time.Millisecond, time.Millisecond)
	cancel()

	// The first request's reads are still held, so the second can only have
	// returned by giving up its wait.
	res := awaitAnalyticsDays(t, second)
	require.ErrorIs(t, res.err, context.Canceled)
	assert.Nil(t, res.reps)

	releaseAsHeld(t, store, len(heldWeek))
	res = awaitAnalyticsDays(t, first)
	require.NoError(t, res.err)
	assert.Equal(t, heldWeek, reportDates(res.reps))
	assert.ElementsMatch(t, heldWeek, store.readDays(), "none of the second request's days is read")
}

// A request that ends does not cancel the reads it started while another
// request waits for them: the other gets each day, read once.
func TestAnalyticsDays_ASharedReadOutlivesTheRequestThatStartedIt(t *testing.T) {
	ix := reportFixtureIndex(t)
	store := newHeldReportStore(ix)
	r := newHeldReporter(store, ix)

	ctx, cancel := context.WithCancel(context.Background())
	first := startAnalyticsWeek(ctx, r, ix, reportDay)
	require.Eventually(t, func() bool { return len(store.heldDays()) == reportReadsAtOnce }, 5*time.Second, time.Millisecond)
	second := startAnalyticsWeek(context.Background(), r, ix, reportDay)
	require.Eventually(t, func() bool { return waitingFor(r, heldWeek[0]) == 2 && waitingFor(r, heldWeek[1]) == 2 }, 5*time.Second, time.Millisecond,
		"the second request waits for the first one's reads")
	cancel()

	res := awaitAnalyticsDays(t, first)
	require.ErrorIs(t, res.err, context.Canceled)
	assert.Never(t, func() bool { return !slices.Equal(heldWeek[:reportReadsAtOnce], store.heldDays()) }, 100*time.Millisecond, time.Millisecond,
		"the reads go on for the second request")

	releaseAsHeld(t, store, len(heldWeek))
	res = awaitAnalyticsDays(t, second)
	require.NoError(t, res.err)
	assert.Equal(t, heldWeek, reportDates(res.reps))
	assert.ElementsMatch(t, heldWeek, store.readDays(), "each day read once")
}
