package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// reportRouteRow is one route's row on the Analytics page, over the days the
// page covers.
type reportRouteRow struct {
	RouteID   string
	Name      string
	Badge     tripRoute
	Scheduled int
	Ran       int
	// OnTime, EarlyShare and LateShare are percentages of the stops judged,
	// and Unknown of every stop; each is "" when there is nothing to take a
	// percentage of: no stop time known, or no trip run.
	OnTime     string
	EarlyShare string
	LateShare  string
	Unknown    string
	// Early and Late are the stops counted, and Split the three shares in
	// whole numbers, which the row's bar is drawn from.
	Early    int
	Late     int
	Split    onTimeSplit
	TripsURL string
}

// reportTripRow is one timetabled trip in a route's day.
type reportTripRow struct {
	GtfsTripID string
	Headsign   string
	// Scheduled is when the trip leaves its first stop and reaches its last.
	Scheduled string
	Runs      []reportRunRow
	Departure reportDeviation
	Arrival   reportDeviation
	OnTime    string
	// NoReport is how many of the trip's stops had no report near them.
	NoReport int
}

// reportRunRow is a driver trip that ran a scheduled trip, with a link to
// its page.
type reportRunRow struct {
	Vehicle  string
	Driver   string
	TrailURL string
}

// reportDeviation is a deviation from schedule as the page shows it: the
// text, and Tone, one of "on-time", "early", "late" or "unknown", which the
// template colours it by.
type reportDeviation struct {
	Text string
	Tone string
	// Signed is the deviation as a signed time, "+0:20" late or "-0:40"
	// early, which is shown beside "On time": a vehicle inside the window is
	// never called late in green.
	Signed string
}

// The measures of the Analytics page, as ?metric= names them.
const (
	metricOnTime   = "on_time"
	metricTripsRun = "trips_run"
	metricEarly    = "early"
	metricLate     = "late"
	metricNoReport = "no_report"
	metricBunched  = "bunched"
	metricLongGaps = "long_gaps"
)

// analyticsMetrics are the measures in the order of their tabs. Those of the
// gaps between vehicles, marked Gaps, are shown only for a period with
// headway service.
var analyticsMetrics = []struct {
	Key, Label, Tone string
	Gaps             bool
}{
	{metricOnTime, "On time", "ok", false},
	{metricTripsRun, "Trips run", "", false},
	{metricEarly, "Early", "early", false},
	{metricLate, "Late", "late", false},
	{metricNoReport, "Stops with no report", "", false},
	{metricBunched, "Bunched", "early", true},
	{metricLongGaps, "Long gaps", "late", true},
}

// gapMetric reports whether metric is one of the gaps between vehicles.
func gapMetric(metric string) bool {
	for _, m := range analyticsMetrics {
		if m.Key == metric {
			return m.Gaps
		}
	}
	return false
}

// analyticsPeriods are the periods, in days, the page covers besides a
// single day; it opens on the first. Days older than yesterday are read from
// the totals the reporter keeps, so a period and the one before it cost two
// reads of the database and the work of at most yesterday and today.
var analyticsPeriods = []int{7, 28}

// analyticsQuery is what the Analytics page was asked for. All of it is in
// the address, so a bookmark is a saved report.
type analyticsQuery struct {
	// Date is the last day shown, YYYY-MM-DD; "" is yesterday.
	Date    string
	RouteID string
	// Days is 1 or one of analyticsPeriods.
	Days   int
	Metric string
	// Compare draws the period before behind a trend.
	Compare bool
}

func parseAnalyticsQuery(q url.Values) analyticsQuery {
	a := analyticsQuery{
		Date:    q.Get("date"),
		RouteID: q.Get("route_id"),
		Days:    analyticsPeriods[0],
		Metric:  metricOnTime,
		Compare: q.Get("compare") != "off",
	}
	// Any other days is the period the page opens on, as an unknown metric
	// is on time.
	for _, days := range analyticsPeriods {
		if q.Get("days") == strconv.Itoa(days) {
			a.Days = days
		}
	}
	// A route is shown a day at a time.
	if q.Get("range") == "day" || a.RouteID != "" {
		a.Days = 1
	}
	for _, m := range analyticsMetrics {
		if m.Key == q.Get("metric") {
			a.Metric = m.Key
		}
	}
	return a
}

// URL is the page's address for q, with what is the default left out.
func (q analyticsQuery) URL() string {
	v := url.Values{}
	if q.Date != "" {
		v.Set("date", q.Date)
	}
	switch {
	case q.RouteID != "":
		v.Set("route_id", q.RouteID)
	case q.Days == 1:
		v.Set("range", "day")
	default:
		if d := q.daysParam(); d != "" {
			v.Set("days", d)
		}
		if q.Metric != metricOnTime {
			v.Set("metric", q.Metric)
		}
		if !q.Compare {
			v.Set("compare", "off")
		}
	}
	if len(v) == 0 {
		return "/admin/reports"
	}
	return "/admin/reports?" + v.Encode()
}

// daysParam is the days the address names for q: none for a day, which range
// names, or for the period the page opens on.
func (q analyticsQuery) daysParam() string {
	if q.RouteID != "" || q.Days == 1 || q.Days == analyticsPeriods[0] {
		return ""
	}
	return strconv.Itoa(q.Days)
}

// reportsPageURL is the address of a day's report: every route's row, or with
// routeID that route's trips.
func reportsPageURL(date, routeID string) string {
	return analyticsQuery{Date: date, RouteID: routeID, Days: 1}.URL()
}

// analyticsLink is a choice in the page's range bar.
type analyticsLink struct {
	Label   string
	URL     string
	Current bool
}

// analyticsMetric is one tab over the trend: a measure over the whole period,
// and Note, how it moved against the period before or the counts behind it.
type analyticsMetric struct {
	Label    string
	Value    string
	Note     string
	Tone     string
	URL      string
	Selected bool
}

// analyticsTrend is the chart under the tabs with its legend and the table
// that holds the same numbers.
type analyticsTrend struct {
	Label  string
	Tone   string
	Chart  lineChartView
	Period string
	// Before names the period drawn dashed; "" when it is not shown.
	Before string
	Rows   []analyticsTrendRow
}

type analyticsTrendRow struct {
	Day, Value, Before string
}

// analyticsTiles are the four tiles of a single day, and two more for its
// gaps between vehicles.
type analyticsTiles struct {
	Split     onTimeSplit
	Ran       int
	Scheduled int
	NotRun    int
	Early     int
	Late      int
	// Gaps is nil when the day has no headway service.
	Gaps *gapTiles
}

// gapTiles are Bunched and Long gaps as whole percentages of the gaps
// judged, which say nothing when Known is false, with the counts behind them
// in words.
type gapTiles struct {
	Known       bool
	Bunched     int
	Long        int
	BunchedNote string
	LongNote    string
}

// reportCounts are the counts the page takes its shares from: a route's, a
// day's, an hour's or a whole period's.
type reportCounts struct {
	Scheduled int
	Run       int
	OnTime    int
	Early     int
	Late      int
	Unknown   int
	// Gaps is the gaps between vehicles on headway trips.
	Gaps gapCounts
}

func (c *reportCounts) add(o reportCounts) {
	c.Scheduled += o.Scheduled
	c.Run += o.Run
	c.OnTime += o.OnTime
	c.Early += o.Early
	c.Late += o.Late
	c.Unknown += o.Unknown
	c.Gaps.addCounts(o.Gaps)
}

// addCounts adds o's gaps to c's.
func (c *gapCounts) addCounts(o gapCounts) {
	c.Gaps += o.Gaps
	c.Bunched += o.Bunched
	c.LongGaps += o.LongGaps
	c.Unknown += o.Unknown
	c.SumGapS += o.SumGapS
	c.SumGapSq += o.SumGapSq
	c.SumScheduledS += o.SumScheduledS
	c.SumScheduledSq += o.SumScheduledSq
}

// judged is the stops known to be on time, early or late.
func (c reportCounts) judged() int { return c.OnTime + c.Early + c.Late }

// measure is one of the page's measures as a percentage, nil when there is
// nothing to take it of.
func (c reportCounts) measure(metric string) *float64 {
	switch metric {
	case metricTripsRun:
		return share(c.Run, c.Scheduled)
	case metricEarly:
		return share(c.Early, c.judged())
	case metricLate:
		return share(c.Late, c.judged())
	case metricNoReport:
		return share(c.Unknown, c.judged()+c.Unknown)
	case metricBunched:
		return share(c.Gaps.Bunched, c.Gaps.Gaps)
	case metricLongGaps:
		return share(c.Gaps.LongGaps, c.Gaps.Gaps)
	}
	return share(c.OnTime, c.judged())
}

func share(n, of int) *float64 {
	if of == 0 {
		return nil
	}
	p := float64(n) * 100 / float64(of)
	return &p
}

// routeCounts is a route's counts on its day, with the gaps between vehicles
// in each of its directions.
func routeCounts(r routeReport) reportCounts {
	c := reportCounts{
		Scheduled: r.TripsScheduled, Run: r.TripsRun,
		OnTime: r.StopsOnTime, Early: r.StopsEarly, Late: r.StopsLate, Unknown: r.StopsUnknown,
	}
	for _, d := range r.Gaps {
		for _, h := range d.hours {
			c.Gaps.addCounts(h.gapCounts)
		}
	}
	return c
}

// dayCounts adds up a day's routes.
func dayCounts(rep *serviceReport) reportCounts {
	var c reportCounts
	for _, route := range rep.Routes {
		c.add(routeCounts(route))
	}
	return c
}

// onTimeSplit is how stops stood against the timetable, as whole percentages
// of the stops judged. Known is false when none was judged, and the shares
// then say nothing.
type onTimeSplit struct {
	Known   bool
	Percent int
	Early   int
	Late    int
}

// newOnTimeSplit rounds each share on its own, so each is the number the
// page shows for it elsewhere; the three can add up to 99 or 101.
func newOnTimeSplit(onTime, early, late int) onTimeSplit {
	judged := onTime + early + late
	if judged == 0 {
		return onTimeSplit{}
	}
	whole := func(n int) int { return int(math.Round(float64(n) * 100 / float64(judged))) }
	return onTimeSplit{Known: true, Percent: whole(onTime), Early: whole(early), Late: whole(late)}
}

func (c reportCounts) split() onTimeSplit { return newOnTimeSplit(c.OnTime, c.Early, c.Late) }

// analyticsTotals adds up each of the days that end on last, oldest first.
// The days the reporter keeps totals for are read in one go; the others,
// yesterday and today and any day not kept yet, are worked out.
func analyticsTotals(ctx context.Context, reports *serviceReporter, recent *overviewReports, ix *rider.Index, today, last time.Time, days int) ([]dayTotals, error) {
	first := last.AddDate(0, 0, 1-days)
	stored, err := reports.storedDays(ctx, first, last)
	if err != nil {
		return nil, err
	}
	out := make([]dayTotals, days)
	var missing []time.Time
	var at []int
	for i := range out {
		day := first.AddDate(0, 0, i)
		if totals, ok := stored[day.Format(reportDateLayout)]; ok {
			out[i] = totals
			continue
		}
		missing = append(missing, day)
		at = append(at, i)
	}
	reps, err := analyticsDays(ctx, reports, recent, ix, today, missing)
	if err != nil {
		return nil, err
	}
	for j, rep := range reps {
		out[at[j]] = newDayTotals(rep, reports.settings)
	}
	return out, nil
}

// analyticsDays works out the service report of each of days, in their
// order. Days are asked for reportReadsAtOnce at a time, so a cold cache costs
// a few days' reads rather than all of them in a row, the first days are read
// first, and two requests' days take turns at the reporter's slots. The first
// day that fails, or the request ending, stops the days not started yet and
// gives up those under way, whose reads stop unless another request waits for
// them.
func analyticsDays(ctx context.Context, reports *serviceReporter, recent *overviewReports, ix *rider.Index, today time.Time, days []time.Time) ([]*serviceReport, error) {
	out := make([]*serviceReport, len(days))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(reportReadsAtOnce)
	for i, day := range days {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			rep, err := recent.report(ctx, reports, ix, today, day)
			out[i] = rep
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// reportsPageView builds the Analytics page for its query: a period's trend
// and routes, a day's routes, or with route_id that route's trips on a day.
// A date the page cannot read, or no schedule to report against, is a
// message on the page under the status it is sent with; any other error is
// the caller's to report.
func reportsPageView(ctx context.Context, reports *serviceReporter, recent *overviewReports, q url.Values) (map[string]interface{}, int, error) {
	query := parseAnalyticsQuery(q)
	data := map[string]interface{}{
		"Title":   "Analytics",
		"Page":    "reports",
		"Date":    query.Date,
		"RouteID": query.RouteID,
		"OneDay":  query.Days == 1,
		"Metric":  query.Metric,
		// CompareOff and DaysParam are carried by the date form, which has
		// no chip to read.
		"CompareOff": query.Days > 1 && !query.Compare,
		"DaysParam":  query.daysParam(),
	}
	noSchedule := func() (map[string]interface{}, int, error) {
		data["NoSchedule"] = true
		data["Headline"] = "The schedule is not loaded"
		data["Lede"] = "On-time figures compare each trip with the timetable, so there is nothing to measure against yet."
		return data, http.StatusServiceUnavailable, nil
	}

	ix := reports.index()
	if ix == nil {
		return noSchedule()
	}
	day, err := reports.reportDay(ix, query.Date)
	if err != nil {
		data["Headline"] = "That date could not be read"
		data["Error"] = "Date must be YYYY-MM-DD."
		return data, http.StatusBadRequest, nil
	}
	// ServiceDate writes the date in the layout it is parsed with here.
	today, _ := time.Parse(gtfsDateLayout, ix.ServiceDate(reports.now()))

	// A route's trips are listed from the day's report; everything else adds
	// up the days' totals.
	var rep *serviceReport
	var days, before []dayTotals
	if query.RouteID != "" {
		rep, err = recent.report(ctx, reports, ix, today, day)
	} else {
		days, err = analyticsTotals(ctx, reports, recent, ix, today, day, query.Days)
		if err == nil && query.Days > 1 && query.Compare {
			before, err = analyticsTotals(ctx, reports, recent, ix, today, day.AddDate(0, 0, -query.Days), query.Days)
		}
	}
	switch {
	case errors.Is(err, errNoSchedule):
		return noSchedule()
	case err != nil:
		return nil, 0, err
	}
	// A period with no headway service has no gaps to draw, so a measure of
	// them is on time, as an unknown one is.
	directions := gapTotals(days)
	if len(directions) == 0 && gapMetric(query.Metric) {
		query.Metric = metricOnTime
		data["Metric"] = metricOnTime
	}

	date := day.Format(reportDateLayout)
	query.Date = date
	settings := reports.settings
	data["Date"] = date
	data["OnTimeEarly"] = humanizeDuration(settings.onTimeEarly)
	data["OnTimeLate"] = humanizeDuration(settings.onTimeLate)
	data["FixWindow"] = humanizeDuration(reportFixWindow)

	oneDay := query
	oneDay.RouteID, oneDay.Days = "", 1
	periods := []analyticsLink{{Label: "Day", URL: oneDay.URL(), Current: query.Days == 1}}
	for _, days := range analyticsPeriods {
		period := query
		period.RouteID, period.Days = "", days
		periods = append(periods, analyticsLink{Label: strconv.Itoa(days) + " days", URL: period.URL(), Current: query.Days == days})
	}
	data["Range"] = periods
	data["AllRoutesURL"] = oneDay.URL()
	earlier := query
	earlier.Date = day.AddDate(0, 0, -query.Days).Format(reportDateLayout)
	data["PrevURL"] = earlier.URL()
	// Nothing has run after today, so the page does not step past it.
	if day.Before(today) {
		later := query
		later.Date = minTime(day.AddDate(0, 0, query.Days), today).Format(reportDateLayout)
		data["NextURL"] = later.URL()
	}
	switch {
	case query.Days == 1:
		data["CSVURL"] = "/api/v1/admin/reports/service?" + url.Values{"date": {date}, "format": {"csv"}}.Encode()
	default:
		toggled := query
		toggled.Compare = !query.Compare
		data["Compare"] = analyticsLink{Label: fmt.Sprintf("Compare with the %d days before", query.Days), URL: toggled.URL(), Current: query.Compare}
	}
	tz := "Times are " + ix.Timezone().String() + " time."
	when := periodPhrase(day, today, query.Days)
	isToday := query.Days == 1 && day.Equal(today)
	data["IsToday"] = isToday

	if query.RouteID != "" {
		for _, route := range rep.Routes {
			if route.RouteID != query.RouteID {
				continue
			}
			counts := routeCounts(route)
			row := newReportRouteRow(ix, rep.Date, route.RouteID, route.ShortName, route.LongName, counts)
			data["Route"] = row
			data["Headline"] = row.Name + " " + when
			data["Lede"] = strings.Join(append(routeDaySentences(counts, isToday), tz), " ")
			data["Tiles"] = newAnalyticsTiles(counts, len(route.Gaps) > 0)
			// A trip in frequencies.txt has no timetable to list it by. Its
			// direction is one of route.Gaps, whose card lists its runs.
			trips := make([]reportTripRow, 0, len(route.Trips))
			for _, trip := range route.Trips {
				if trip.Frequencies != nil {
					data["HasFrequencies"] = true
					continue
				}
				trips = append(trips, newReportTripRow(rep, trip, day))
			}
			data["Trips"] = trips
			if len(route.Gaps) > 0 {
				data["RouteRuns"] = &reportRouteRuns{
					Directions:      newReportRunDirections(rep, route, day),
					BunchedBelow:    percentOf(bunchedBelow),
					LongAbove:       percentOf(longGapAbove),
					FrequentSpacing: humanizeDuration(frequentSpacing),
				}
			}
			return data, http.StatusOK, nil
		}
		data["NoRoute"] = true
		data["Headline"] = "Route " + query.RouteID + " " + when
		return data, http.StatusOK, nil
	}

	routes, total := routeTotals(days)
	headway := len(directions) > 0
	rows := make([]reportRouteRow, 0, len(routes))
	var gapRows []reportGapRow
	for _, r := range routes {
		row := newReportRouteRow(ix, date, r.ID, r.ShortName, r.LongName, r.reportCounts)
		rows = append(rows, row)
		for _, d := range directions[r.ID] {
			gapRows = append(gapRows, newReportGapRow(row, d))
		}
	}
	data["Routes"] = rows
	data["Headline"] = analyticsHeadline(total, when, isToday)

	var sentences []string
	var totalBefore *reportCounts
	if len(before) > 0 {
		_, was := routeTotals(before)
		totalBefore = &was
		if s := comparedSentence(total, was, query.Days); s != "" {
			sentences = append(sentences, s)
		}
	}
	if s := worstRouteSentence(days, routes, total); s != "" {
		sentences = append(sentences, s)
	}
	// Under a headline of gaps, which a period with no stop judged has.
	if !total.split().Known {
		if s := mostBunchedSentence(routes, total); s != "" {
			sentences = append(sentences, s)
		}
	}
	data["Lede"] = strings.Join(append(sentences, tz), " ")

	if query.Days == 1 {
		data["Tiles"] = newAnalyticsTiles(total, headway)
	} else {
		data["Metrics"] = newAnalyticsMetrics(query, total, totalBefore, headway)
		data["Trend"] = newAnalyticsTrend(query, days, before)
	}
	if total.judged() > 0 {
		data["Hours"] = newHourChart(hoursOfDay(days), tripHours)
	}
	buckets, leftOut := deviationBuckets(days, settings)
	if buckets != nil {
		data["Histogram"] = newHistChart(buckets, settings.onTimeEarly, settings.onTimeLate)
	}
	data["HistogramLeftOut"] = leftOut
	if headway {
		data["Gaps"] = newAnalyticsGaps(gapRows, days, total.Gaps)
	}
	return data, http.StatusOK, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// dayName is a day as the page names it. The year is said only when it is
// not this one.
func dayName(day, today time.Time) string {
	if day.Year() != today.Year() {
		return day.Format("Monday 2 January 2006")
	}
	return day.Format("Monday 2 January")
}

// periodPhrase says which days a page covers, as its headline ends: "over
// the last 7 days", "today", "on Tuesday 17 November".
func periodPhrase(day, today time.Time, days int) string {
	yesterday := today.AddDate(0, 0, -1)
	switch {
	case days > 1 && !day.Before(yesterday) && !day.After(today):
		return fmt.Sprintf("over the last %d days", days)
	case days > 1:
		return fmt.Sprintf("over the %d days to %s", days, dayName(day, today))
	case day.Equal(today):
		return "today"
	case day.Equal(yesterday):
		return "yesterday"
	}
	return "on " + dayName(day, today)
}

// analyticsHeadline is the page's one sentence: the on-time share of the
// stops judged; with none judged, the shares of the gaps between vehicles
// bunched and long, a period run only by headway; or why there is neither.
func analyticsHeadline(total reportCounts, when string, isToday bool) string {
	split := total.split()
	bunched, long := wholeShare(total.Gaps.Bunched, total.Gaps.Gaps), wholeShare(total.Gaps.LongGaps, total.Gaps.Gaps)
	switch {
	case split.Known && isToday:
		return fmt.Sprintf("On time is %d%% so far today", split.Percent)
	case split.Known:
		return fmt.Sprintf("On time was %d%% %s", split.Percent, when)
	case total.Gaps.Gaps > 0 && isToday:
		return fmt.Sprintf("%d%% of gaps are bunched and %d%% long so far today", bunched, long)
	case total.Gaps.Gaps > 0:
		return fmt.Sprintf("%d%% of gaps were bunched and %d%% were long %s", bunched, long, when)
	case total.Scheduled == 0 && isToday:
		return "No trips are scheduled today"
	case total.Scheduled == 0:
		return "No trips were scheduled " + when
	case total.Run == 0 && isToday:
		return "No scheduled trip has run yet today"
	case total.Run == 0:
		return "No scheduled trip ran " + when
	case isToday:
		return "No stop has been judged yet today"
	}
	return "No stop was judged " + when
}

// comparedSentence sets a period's on-time share against the period
// before's; "" when either has none.
func comparedSentence(total, before reportCounts, days int) string {
	now, was := total.split(), before.split()
	if !now.Known || !was.Known {
		return ""
	}
	switch diff := now.Percent - was.Percent; {
	case diff > 0:
		return fmt.Sprintf("That is %s higher than the %d days before.", countOf(diff, "point", "points"), days)
	case diff < 0:
		return fmt.Sprintf("That is %s lower than the %d days before.", countOf(-diff, "point", "points"), days)
	}
	return fmt.Sprintf("That is the same as the %d days before.", days)
}

// routeDaySentences are a route's day in words: its on-time share, when a
// stop was judged, and how many of its trips ran.
func routeDaySentences(c reportCounts, isToday bool) []string {
	var out []string
	if split := c.split(); split.Known {
		out = append(out, fmt.Sprintf("%d%% of stops were on time.", split.Percent))
	}
	trips := "scheduled trips"
	if c.Scheduled == 1 {
		trips = "scheduled trip"
	}
	ran := "ran"
	if isToday {
		ran = "have run so far"
		if c.Scheduled == 1 {
			ran = "has run so far"
		}
	}
	return append(out, fmt.Sprintf("%d of %d %s %s.", c.Run, c.Scheduled, trips, ran))
}

// routeTotal is a route over the days of a period.
type routeTotal struct {
	ID        string
	ShortName string
	LongName  string
	reportCounts
}

// allCounts is the route's counts as counts adds them up from its hours,
// with the gaps between vehicles in each of its directions.
func (r routeDayTotals) allCounts() reportCounts {
	c := r.counts()
	for _, d := range r.Gaps {
		for _, h := range d.Hours {
			c.Gaps.addCounts(h.gapCounts)
		}
	}
	return c
}

// allCounts adds up the day's routes with their gaps between vehicles.
func (d dayTotals) allCounts() reportCounts {
	var c reportCounts
	for _, r := range d.Routes {
		c.add(r.allCounts())
	}
	return c
}

// routeTotals adds each route up over days, and all of them together. The
// routes are in the newest day's order, then those only older days have.
func routeTotals(days []dayTotals) ([]routeTotal, reportCounts) {
	var total reportCounts
	at := make(map[string]int)
	routes := make([]routeTotal, 0)
	for i := len(days) - 1; i >= 0; i-- {
		for _, route := range days[i].Routes {
			n, ok := at[route.RouteID]
			if !ok {
				n = len(routes)
				at[route.RouteID] = n
				routes = append(routes, routeTotal{ID: route.RouteID, ShortName: route.ShortName, LongName: route.LongName})
			}
			c := route.allCounts()
			routes[n].add(c)
			total.add(c)
		}
	}
	return routes, total
}

// routeTitle is a route in a sentence or beside its badge: its long name, or
// "Route" and its short name or id.
func routeTitle(id, shortName, longName string) string {
	if name := strings.TrimSpace(longName); name != "" {
		return name
	}
	return "Route " + cmp.Or(strings.TrimSpace(shortName), id)
}

// worstRouteSentence names the route with the lowest on-time share, what
// most of its misses were and, when over half of them were on trips that
// left within two hours of each other, when. It is "" unless two routes
// have a share and they differ.
func worstRouteSentence(days []dayTotals, routes []routeTotal, total reportCounts) string {
	var worst *routeTotal
	judged := 0
	for i := range routes {
		r := &routes[i]
		if r.judged() == 0 {
			continue
		}
		judged++
		if worst == nil || *r.measure(metricOnTime) < *worst.measure(metricOnTime) {
			worst = r
		}
	}
	if judged < 2 || *worst.measure(metricOnTime) >= *total.measure(metricOnTime) {
		return ""
	}

	kind, missed := "late", worst.Late
	if worst.Early > worst.Late {
		kind, missed = "early", worst.Early
	}
	sentence := fmt.Sprintf("%s is the route pulling it down: %d%% of its stops were %s",
		routeTitle(worst.ID, worst.ShortName, worst.LongName), int(math.Round(float64(missed)*100/float64(worst.judged()))), kind)

	byHour := make(map[int]int)
	for _, d := range days {
		for _, route := range d.Routes {
			if route.RouteID != worst.ID {
				continue
			}
			for _, h := range route.Hours {
				n := h.Late
				if kind == "early" {
					n = h.Early
				}
				byHour[h.Hour] += n
			}
		}
	}
	from, most := 0, 0
	for hour := range byHour {
		for _, start := range []int{hour - 1, hour} {
			if n := byHour[start] + byHour[start+1]; n > most || n == most && start < from {
				from, most = start, n
			}
		}
	}
	if most*2 > missed {
		sentence += fmt.Sprintf(", most of them on trips leaving between %02d:00 and %02d:00", (from+24)%24, (from+2)%24)
	}
	return sentence + "."
}

// mostBunchedSentence names the route whose gaps between vehicles were most
// often bunched, the first in the newest day's order of those as often. It
// is "" unless its share, in the whole percent the page shows, is over the
// headline's, which a route alone never is.
func mostBunchedSentence(routes []routeTotal, total reportCounts) string {
	var most *routeTotal
	for i := range routes {
		r := &routes[i]
		if r.Gaps.Gaps > 0 && (most == nil || *r.measure(metricBunched) > *most.measure(metricBunched)) {
			most = r
		}
	}
	if most == nil {
		return ""
	}
	share := wholeShare(most.Gaps.Bunched, most.Gaps.Gaps)
	if share <= wholeShare(total.Gaps.Bunched, total.Gaps.Gaps) {
		return ""
	}
	return fmt.Sprintf("%s is the route with the most bunching: %d%% of its gaps were bunched.",
		routeTitle(most.ID, most.ShortName, most.LongName), share)
}

// directionTotal is one direction of a route's headway service over the days
// of a period.
type directionTotal struct {
	// DirectionID is nil when the feed gives the trips none; Headsign then
	// tells the directions apart.
	DirectionID *int
	Headsign    string
	gapCounts
}

// gapTotals adds up each direction of each route's headway service over
// days, by route id, in the order the day's report lists them: by
// direction_id, then by headsign for a feed with none. A direction is named
// by the newest day's headsign. It is empty when no day has headway service.
func gapTotals(days []dayTotals) map[string][]directionTotal {
	type key struct {
		id       int
		headsign string
	}
	keyOf := func(d directionGapTotals) key {
		if d.DirectionID != nil {
			return key{id: *d.DirectionID}
		}
		return key{id: -1, headsign: d.Headsign}
	}
	byKey := make(map[string]map[key]*directionTotal)
	for i := len(days) - 1; i >= 0; i-- {
		for _, route := range days[i].Routes {
			for _, d := range route.Gaps {
				if byKey[route.RouteID] == nil {
					byKey[route.RouteID] = make(map[key]*directionTotal)
				}
				t, ok := byKey[route.RouteID][keyOf(d)]
				if !ok {
					t = &directionTotal{DirectionID: d.DirectionID, Headsign: d.Headsign}
					byKey[route.RouteID][keyOf(d)] = t
				}
				for _, h := range d.Hours {
					t.addCounts(h.gapCounts)
				}
			}
		}
	}
	out := make(map[string][]directionTotal, len(byKey))
	for routeID, directions := range byKey {
		keys := slices.SortedFunc(maps.Keys(directions), func(a, b key) int {
			return cmp.Or(cmp.Compare(a.id, b.id), cmp.Compare(a.headsign, b.headsign))
		})
		for _, k := range keys {
			out[routeID] = append(out[routeID], *directions[k])
		}
	}
	return out
}

// reportGapRow is one direction of a route's headway service in "Gaps
// between vehicles", over the days the page covers, with the route's row:
// its trips run sit beside the gaps, since a vehicle without the app reads
// as a long gap.
type reportGapRow struct {
	Route reportRouteRow
	// Direction is "to" and the headsign, or the direction_id when there is
	// none.
	Direction string
	Judged    int
	Unknown   int
	// Bunched and LongGaps are percentages of the gaps judged, and ExtraWait
	// in minutes; each is "" when no gap was judged.
	Bunched   string
	LongGaps  string
	ExtraWait string
}

func newReportGapRow(route reportRouteRow, d directionTotal) reportGapRow {
	row := reportGapRow{
		Route:     route,
		Judged:    d.Gaps,
		Unknown:   d.Unknown,
		Bunched:   formatPercent(share(d.Bunched, d.Gaps)),
		LongGaps:  formatPercent(share(d.LongGaps, d.Gaps)),
		ExtraWait: formatExtraWait(d.extraWait()),
	}
	switch {
	case d.Headsign != "":
		row.Direction = "to " + d.Headsign
	case d.DirectionID != nil:
		row.Direction = "direction " + strconv.Itoa(*d.DirectionID)
	}
	return row
}

// formatExtraWait is extra wait in seconds as minutes to one decimal place,
// "1.9", or "" when there is none.
func formatExtraWait(seconds *int) string {
	if seconds == nil {
		return ""
	}
	minutes := math.Round(float64(*seconds)/6) / 10
	if minutes == 0 {
		// Not "-0.0" for a few seconds under.
		minutes = 0
	}
	return strconv.FormatFloat(minutes, 'f', 1, 64)
}

// analyticsGaps is the "Gaps between vehicles" section: a row for each
// direction of each route with headway service, and its two charts, which
// are nil when no gap was judged.
type analyticsGaps struct {
	Rows []reportGapRow
	// BunchedBelow and LongAbove are the bands a gap is judged by, as
	// percentages of the scheduled gap.
	BunchedBelow string
	LongAbove    string
	Hours        *hourChartView
	Ratios       *ratioChartView
}

func newAnalyticsGaps(rows []reportGapRow, days []dayTotals, total gapCounts) *analyticsGaps {
	gaps := &analyticsGaps{Rows: rows, BunchedBelow: percentOf(bunchedBelow), LongAbove: percentOf(longGapAbove)}
	if total.Gaps > 0 {
		hours := newHourChart(gapHoursOfDay(days), gapHours)
		ratios := newRatioChart(gapRatios(days))
		gaps.Hours, gaps.Ratios = &hours, &ratios
	}
	return gaps
}

// gapRatios adds up the gaps judged on days by the tenth of their scheduled
// gap each was in, from under 10% to gapRatioTop, 300% and over. A tenth
// outside those, which no kept day of this version has, is left out rather
// than drawn off the chart.
func gapRatios(days []dayTotals) []int {
	tenths := make([]int, gapRatioTop+1)
	for _, d := range days {
		for _, r := range d.GapRatios {
			if r.Tenth >= 0 && r.Tenth <= gapRatioTop {
				tenths[r.Tenth] += r.Gaps
			}
		}
	}
	return tenths
}

// serviceHour is the hour of rep's service day that t falls in, on the
// agency's clock: 24 and up after midnight, so the last trips of a day sort
// after its evening.
func serviceHour(rep *serviceReport, t time.Time) int {
	day, _ := time.Parse(reportDateLayout, rep.Date)
	local := t.In(rep.location)
	y, m, d := local.Date()
	return local.Hour() + 24*int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(day).Hours()/24)
}

// hoursOfDay groups the trips of days by the hour they were scheduled to
// leave in, from the first hour with a trip to the last with none left out,
// and splits their stops judged: late, early and on time.
func hoursOfDay(days []dayTotals) []hourSplit {
	byHour := make(map[int]hourSplit)
	for _, d := range days {
		for _, route := range d.Routes {
			for _, t := range route.Hours {
				h := byHour[t.Hour]
				h.Count += t.Scheduled
				h.Lower += t.Late
				h.Upper += t.Early
				h.Rest += t.OnTime
				byHour[t.Hour] = h
			}
		}
	}
	return everyHour(byHour)
}

// gapHoursOfDay groups the gaps judged between vehicles on days by the hour
// their later vehicle left in, from the first hour with a gap, judged or
// not, to the last with none left out, and splits them: long, bunched and
// neither.
func gapHoursOfDay(days []dayTotals) []hourSplit {
	byHour := make(map[int]hourSplit)
	for _, d := range days {
		for _, route := range d.Routes {
			for _, dir := range route.Gaps {
				for _, g := range dir.Hours {
					h := byHour[g.Hour]
					h.Count += g.Gaps
					h.Lower += g.LongGaps
					h.Upper += g.Bunched
					h.Rest += g.Gaps - g.Bunched - g.LongGaps
					byHour[g.Hour] = h
				}
			}
		}
	}
	return everyHour(byHour)
}

// everyHour lists the hours of byHour in order, with an empty hour for each
// between its first and its last that it does not have.
func everyHour(byHour map[int]hourSplit) []hourSplit {
	hours := make([]hourSplit, 0, len(byHour))
	if len(byHour) == 0 {
		return hours
	}
	keys := slices.Collect(maps.Keys(byHour))
	for hour := slices.Min(keys); hour <= slices.Max(keys); hour++ {
		h := byHour[hour]
		h.Hour = hour
		hours = append(hours, h)
	}
	return hours
}

// deviationBuckets counts the trips of days by the minute they reached
// their last stop against the timetable, from three minutes before the
// on-time window to eight after it. It also returns how many trips that ran
// had no report at the last stop and are in no bucket. The buckets are nil
// when no trip is in one.
func deviationBuckets(days []dayTotals, s reportSettings) ([]deviationBucket, int) {
	first := -int(math.Ceil(s.onTimeEarly.Minutes())) - 3
	last := int(math.Ceil(s.onTimeLate.Minutes())) + 7
	buckets := make([]deviationBucket, last-first+1)
	for i := range buckets {
		buckets[i].Minute = first + i
	}
	counted, leftOut := 0, 0
	for _, d := range days {
		leftOut += d.ArrivalsLeftOut
		for _, a := range d.Arrivals {
			counted += a.OnTime + a.Early + a.Late
			b := &buckets[min(last, max(first, a.Minute))-first]
			b.OnTime += a.OnTime
			b.Early += a.Early
			b.Late += a.Late
		}
	}
	if counted == 0 {
		return nil, leftOut
	}
	return buckets, leftOut
}

// newAnalyticsTiles is a day's tiles, with those of its gaps between
// vehicles when it has headway service.
func newAnalyticsTiles(c reportCounts, headway bool) analyticsTiles {
	tiles := analyticsTiles{
		Split:     c.split(),
		Ran:       c.Run,
		Scheduled: c.Scheduled,
		NotRun:    c.Scheduled - c.Run,
		Early:     c.Early,
		Late:      c.Late,
	}
	if headway {
		tiles.Gaps = &gapTiles{
			Known:       c.Gaps.Gaps > 0,
			Bunched:     wholeShare(c.Gaps.Bunched, c.Gaps.Gaps),
			Long:        wholeShare(c.Gaps.LongGaps, c.Gaps.Gaps),
			BunchedNote: countOf(c.Gaps.Bunched, "gap", "gaps") + " under " + percentOf(bunchedBelow) + " of the scheduled gap",
			LongNote:    countOf(c.Gaps.LongGaps, "gap", "gaps") + " over " + percentOf(longGapAbove) + " of the scheduled gap",
		}
	}
	return tiles
}

// wholeShare is n as a whole percentage of of, 0 when of is.
func wholeShare(n, of int) int {
	if p := share(n, of); p != nil {
		return int(math.Round(*p))
	}
	return 0
}

// percentOf is a fraction as a whole percentage, "25%".
func percentOf(f float64) string {
	return strconv.Itoa(int(math.Round(f*100))) + "%"
}

// newAnalyticsMetrics is the tabs over the trend, with those of the gaps
// between vehicles for a period with headway service. A tab's note is how
// the measure moved against the period before, in points, when there is one
// to set it against, and otherwise the counts it is a share of.
func newAnalyticsMetrics(query analyticsQuery, total reportCounts, before *reportCounts, headway bool) []analyticsMetric {
	stops := func(n int) string { return countOf(n, "stop", "stops") }
	counts := map[string]string{
		metricOnTime:   fmt.Sprintf("%d of %s", total.OnTime, stops(total.judged())),
		metricTripsRun: fmt.Sprintf("%d of %d a day", int(math.Round(float64(total.Run)/float64(query.Days))), int(math.Round(float64(total.Scheduled)/float64(query.Days)))),
		metricEarly:    stops(total.Early),
		metricLate:     stops(total.Late),
		metricNoReport: fmt.Sprintf("%d of %s", total.Unknown, stops(total.judged()+total.Unknown)),
		metricBunched:  fmt.Sprintf("%d of %s", total.Gaps.Bunched, countOf(total.Gaps.Gaps, "gap", "gaps")),
		metricLongGaps: fmt.Sprintf("%d of %s", total.Gaps.LongGaps, countOf(total.Gaps.Gaps, "gap", "gaps")),
	}
	out := make([]analyticsMetric, 0, len(analyticsMetrics))
	for _, m := range analyticsMetrics {
		if m.Gaps && !headway {
			continue
		}
		tab := query
		tab.Metric = m.Key
		metric := analyticsMetric{Label: m.Label, Value: "None", Note: counts[m.Key], URL: tab.URL(), Selected: m.Key == query.Metric}
		// On time's green is for its line; a figure is coloured only when it
		// counts misses.
		if m.Tone == "early" || m.Tone == "late" {
			metric.Tone = m.Tone
		}
		now := total.measure(m.Key)
		if now != nil {
			metric.Value = percentTip(now)
		}
		if before != nil && m.Key != metricTripsRun {
			if was := before.measure(m.Key); now != nil && was != nil {
				switch diff := int(math.Round(*now)) - int(math.Round(*was)); {
				case diff > 0:
					metric.Note = countOf(diff, "point", "points") + " up"
				case diff < 0:
					metric.Note = countOf(-diff, "point", "points") + " down"
				default:
					metric.Note = "unchanged"
				}
			}
		}
		out = append(out, metric)
	}
	return out
}

// newAnalyticsTrend is the chosen measure day by day, over the same days of
// the period before when there is one.
func newAnalyticsTrend(query analyticsQuery, days, before []dayTotals) analyticsTrend {
	trend := analyticsTrend{Rows: make([]analyticsTrendRow, 0, len(days))}
	for _, m := range analyticsMetrics {
		if m.Key == query.Metric {
			trend.Label, trend.Tone = m.Label, m.Tone
		}
	}
	date := func(d dayTotals, layout string) string {
		day, _ := time.Parse(reportDateLayout, d.Date)
		return day.Format(layout)
	}
	measures := func(days []dayTotals) []*float64 {
		out := make([]*float64, 0, len(days))
		for _, d := range days {
			out = append(out, d.allCounts().measure(query.Metric))
		}
		return out
	}
	series, prev := measures(days), measures(before)
	labels := make([]string, 0, len(days))
	for i, rep := range days {
		labels = append(labels, date(rep, "2 Jan"))
		row := analyticsTrendRow{Day: date(rep, "Mon 2 Jan"), Value: percentTip(series[i])}
		if i < len(prev) {
			row.Before = percentTip(prev[i])
		}
		trend.Rows = append(trend.Rows, row)
	}
	trend.Period = labels[0] + " to " + labels[len(labels)-1]
	if len(before) > 0 {
		trend.Before = fmt.Sprintf("The %d days before", len(before))
	}

	// A share that is good when high is drawn up to 100 from the twenty
	// under its lowest day, and no higher a floor than 60; one that is good
	// when low is drawn from zero to the ten over its highest.
	low, high := 100.0, 0.0
	for _, v := range append(slices.Clone(series), prev...) {
		if v != nil {
			low, high = min(low, *v), max(high, *v)
		}
	}
	o := lineChartOptions{Name: fmt.Sprintf("%s by day, %s", trend.Label, trend.Period), Min: 0, Max: max(10, int(math.Ceil(high/10))*10)}
	if query.Metric == metricOnTime || query.Metric == metricTripsRun {
		o.Min, o.Max = max(0, min(60, int(math.Floor((low-5)/20))*20)), 100
	}
	trend.Chart = newLineChart(labels, series, prev, o)
	return trend
}

// reportsPage renders the Analytics page.
func (ui *adminUI) reportsPage(w http.ResponseWriter, r *http.Request) {
	data, status, err := reportsPageView(r.Context(), ui.reports, &ui.overview, r.URL.Query())
	if err != nil {
		slog.Error("analytics: work out a service report", "date", r.URL.Query().Get("date"), "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if status == http.StatusOK {
		// The view settles on the day shown, which the query may leave out.
		query := parseAnalyticsQuery(r.URL.Query())
		query.Date, _ = data["Date"].(string)
		if err := addAnalyticsAlerts(r.Context(), ui.alertsRaised, ui.reports.index(), query, data); err != nil {
			slog.Error("analytics: list the alerts raised", "date", query.Date, "days", query.Days, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	ui.renderAdmin(w, r, status, "reports.html", data)
}

func newReportRouteRow(ix *rider.Index, date, routeID, shortName, longName string, c reportCounts) reportRouteRow {
	badge := routeBadge(ix, routeID)
	// A route the schedule no longer has keeps the short name the report
	// gave it.
	if _, ok := ix.Route(routeID); !ok && shortName != "" {
		badge.Name = shortName
	}
	return reportRouteRow{
		RouteID:    routeID,
		Name:       routeTitle(routeID, shortName, longName),
		Badge:      badge,
		Scheduled:  c.Scheduled,
		Ran:        c.Run,
		OnTime:     formatPercent(c.measure(metricOnTime)),
		EarlyShare: formatPercent(c.measure(metricEarly)),
		LateShare:  formatPercent(c.measure(metricLate)),
		Unknown:    formatPercent(c.measure(metricNoReport)),
		Early:      c.Early,
		Late:       c.Late,
		Split:      c.split(),
		TripsURL:   reportsPageURL(date, routeID),
	}
}

func newReportTripRow(rep *serviceReport, trip tripReport, day time.Time) reportTripRow {
	settings := reportSettings{
		onTimeEarly: time.Duration(rep.OnTimeEarlyS) * time.Second,
		onTimeLate:  time.Duration(rep.OnTimeLateS) * time.Second,
	}
	row := reportTripRow{
		GtfsTripID: trip.GtfsTripID,
		Headsign:   trip.Headsign,
		Scheduled: formatScheduledTime(trip.ScheduledDeparture, rep.location, day) + " to " +
			formatScheduledTime(trip.ScheduledArrival, rep.location, day),
		Runs:     make([]reportRunRow, 0, len(trip.Runs)),
		OnTime:   formatPercent(trip.OnTimePercent),
		NoReport: trip.StopsUnknown,
	}
	for _, run := range trip.Runs {
		row.Runs = append(row.Runs, reportRunRow{
			Vehicle:  cmp.Or(run.VehicleLabel, run.VehicleID),
			Driver:   run.DriverName,
			TrailURL: "/admin/trips/" + strconv.FormatInt(run.ID, 10),
		})
	}
	if len(trip.Runs) > 0 {
		row.Departure = formatDeviation(trip.DepartureDeviationS, settings)
		row.Arrival = formatDeviation(trip.ArrivalDeviationS, settings)
	}
	return row
}

// reportRouteRuns are the cards of a route's day with frequent service, one
// for each direction, and the bands their gaps are judged by: bunched under
// BunchedBelow of the scheduled gap and long over LongAbove, both
// percentages, with a timetable frequent where it runs every
// FrequentSpacing or more often.
type reportRouteRuns struct {
	Directions      []reportRunDirection
	BunchedBelow    string
	LongAbove       string
	FrequentSpacing string
}

// reportRunDirection is one direction of a route's frequent service on the
// route's day: how often its trips in frequencies.txt run, and its runs in
// the order they left the first stop each is measured at.
type reportRunDirection struct {
	// Title is "Runs to" the headsign, or "Runs in direction" the
	// direction_id when there is none.
	Title     string
	Schedules []reportHeadwayTrip
	Runs      []reportRunGapRow
	// Last is the route's last direction, whose card says how the gaps are
	// judged.
	Last bool
}

// reportHeadwayTrip is a trip in frequencies.txt in its direction's card:
// how often it runs and when, and how many of its runs ran, as
// frequencyWords says them.
type reportHeadwayTrip struct {
	GtfsTripID string
	Scheduled  string
	RunCount   string
}

// reportRunGapRow is one run in its direction's card.
type reportRunGapRow struct {
	GtfsTripID string
	// Left is when the run left the first stop it is measured at, to the
	// second, when LeftKnown; otherwise "not known", or "did not leave" for a
	// run that ended there.
	Left      string
	LeftKnown bool
	Vehicle   string
	Driver    string
	TrailURL  string
	Gap       reportRunGap
	// BunchedAt is how many of the stops its gap was judged at it was
	// bunched at, "1 of 2 stops", and Bunched whether any; "" when its gap
	// was judged at none.
	BunchedAt string
	Bunched   bool
}

// reportRunGap is the gap before a run where it left the first stop: Text,
// "2 min 10 s after Bus 12", and Every, the scheduled gap then, "every 10
// min", with Tone "bunched", "long" or "". When no gap was judged there,
// Text alone says why: "unknown" or "no gap". All are "" for a run that did
// not leave.
type reportRunGap struct {
	Text  string
	Every string
	Tone  string
}

// routeRun is a driver trip that ran one of a route's trips.
type routeRun struct {
	gtfsTripID string
	tripRun
}

// newReportRunDirections is a card for each direction of route's frequent
// service on its day, the last marked.
func newReportRunDirections(rep *serviceReport, route routeReport, day time.Time) []reportRunDirection {
	trips := make(map[string]tripReport, len(route.Trips))
	runs := make(map[int64]routeRun)
	for _, trip := range route.Trips {
		trips[trip.GtfsTripID] = trip
		for _, run := range trip.Runs {
			runs[run.ID] = routeRun{gtfsTripID: trip.GtfsTripID, tripRun: run}
		}
	}
	out := make([]reportRunDirection, 0, len(route.Gaps))
	for _, d := range route.Gaps {
		dir := reportRunDirection{Title: runsTitle(d), Runs: make([]reportRunGapRow, 0, len(d.runs))}
		for _, id := range d.trips {
			if f := trips[id].Frequencies; f != nil {
				scheduled, count := frequencyWords(f, rep.location, day)
				dir.Schedules = append(dir.Schedules, reportHeadwayTrip{GtfsTripID: id, Scheduled: scheduled, RunCount: count})
			}
		}
		for _, r := range d.runs {
			dir.Runs = append(dir.Runs, newReportRunGapRow(rep, r, runs, day))
		}
		out = append(out, dir)
	}
	if n := len(out); n > 0 {
		out[n-1].Last = true
	}
	return out
}

// runsTitle names a direction's card.
func runsTitle(d directionGaps) string {
	switch {
	case d.Headsign != "":
		return "Runs to " + d.Headsign
	case d.DirectionID != nil:
		return "Runs in direction " + strconv.Itoa(*d.DirectionID)
	}
	return "Runs"
}

// newReportRunGapRow is run r in its direction's card, the vehicles named
// from runs, the route's runs by id.
func newReportRunGapRow(rep *serviceReport, r runGaps, runs map[int64]routeRun, day time.Time) reportRunGapRow {
	run := runs[r.id]
	row := reportRunGapRow{
		GtfsTripID: run.gtfsTripID,
		Vehicle:    cmp.Or(run.VehicleLabel, run.VehicleID),
		Driver:     run.DriverName,
		TrailURL:   "/admin/trips/" + strconv.FormatInt(r.id, 10),
	}
	switch {
	case !r.served:
		row.Left = "did not leave"
	case r.left.IsZero():
		row.Left, row.Gap.Text = "not known", "unknown"
	default:
		row.Left, row.LeftKnown = formatPassedTime(r.left, rep.location, day), true
		row.Gap = newReportRunGap(r.gap, runs)
	}
	if r.judged > 0 {
		row.BunchedAt = fmt.Sprintf("%d of %s", r.bunched, countOf(r.judged, "stop", "stops"))
		row.Bunched = r.bunched > 0
	}
	return row
}

// newReportRunGap is the gap g before a run that left the first stop, nil
// when it was the first of a stretch of service there, the run ahead named
// from runs.
func newReportRunGap(g *stopGap, runs map[int64]routeRun) reportRunGap {
	switch {
	case g == nil:
		return reportRunGap{Text: "no gap"}
	case g.unknown:
		return reportRunGap{Text: "unknown"}
	}
	ahead := runs[g.ahead]
	gap := reportRunGap{
		Text:  formatGap(g.gap) + " after " + cmp.Or(ahead.VehicleLabel, ahead.VehicleID),
		Every: "every " + formatGap(g.scheduled),
	}
	switch {
	case g.bunched():
		gap.Tone = "bunched"
	case g.long():
		gap.Tone = "long"
	}
	return gap
}

// formatGap is a gap between vehicles in minutes and seconds, "2 min 10 s",
// the seconds left out when there are none and the minutes when there are
// none.
func formatGap(d time.Duration) string {
	m, sec := int(d/time.Minute), int(d%time.Minute/time.Second)
	switch {
	case m == 0:
		return fmt.Sprintf("%d s", sec)
	case sec == 0:
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d min %d s", m, sec)
}

// frequencyWords says how often a trip in frequencies.txt runs and when,
// "every 10 min, 06:00 to 22:00", a window at a time joined by "then", and
// how many of its runs ran, "41 of 48 runs". The headway is in formatGap's
// words, as each run's scheduled gap in the same card is.
func frequencyWords(f *tripFrequencies, loc *time.Location, day time.Time) (scheduled, runs string) {
	windows := make([]string, 0, len(f.Windows))
	for _, w := range f.Windows {
		from := formatScheduledTime(w.Start, loc, day)
		if w.HeadwayS <= 0 {
			windows = append(windows, "once at "+from)
			continue
		}
		windows = append(windows, fmt.Sprintf("every %s, %s to %s",
			formatGap(time.Duration(w.HeadwayS)*time.Second), from, formatScheduledTime(w.End, loc, day)))
	}
	return strings.Join(windows, ", then "), fmt.Sprintf("%d of %s", f.RunsRun, countOf(f.RunsScheduled, "run", "runs"))
}

// formatScheduledTime is a scheduled time on the agency's clock, marked when
// it falls after midnight on a later calendar day than the service day's.
func formatScheduledTime(t time.Time, loc *time.Location, day time.Time) string {
	local := t.In(loc)
	text := local.Format("15:04")
	if y, m, d := local.Date(); time.Date(y, m, d, 0, 0, 0, 0, time.UTC).After(day) {
		text += " next day"
	}
	return text
}

// formatDeviation is a deviation in seconds, positive late, as minutes and
// seconds early or late.
func formatDeviation(seconds *int, s reportSettings) reportDeviation {
	if seconds == nil {
		return reportDeviation{Text: "unknown", Tone: "unknown"}
	}
	d := time.Duration(*seconds) * time.Second
	abs := d.Abs()
	text := fmt.Sprintf("%d:%02d", int(abs.Minutes()), int(abs.Seconds())%60)
	signed := text
	switch {
	case d > 0:
		signed = "+" + text
		text += " late"
	case d < 0:
		signed = "-" + text
		text += " early"
	}
	return reportDeviation{Text: text, Tone: s.verdict(d).tone(), Signed: signed}
}

// tone names a verdict for the page to colour by.
func (v stopVerdict) tone() string {
	switch v {
	case stopOnTime:
		return "on-time"
	case stopEarly:
		return "early"
	case stopLate:
		return "late"
	}
	return "unknown"
}

func formatPercent(p *float64) string {
	if p == nil {
		return ""
	}
	return percentCell(p) + "%"
}
