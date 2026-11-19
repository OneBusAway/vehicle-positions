package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// reportTripRow is one scheduled trip in a route's day.
type reportTripRow struct {
	GtfsTripID string
	Headsign   string
	Scheduled  string
	Runs       []reportRunRow
	Departure  reportDeviation
	Arrival    reportDeviation
	OnTime     string
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
)

// analyticsMetrics are the measures in the order of their tabs.
var analyticsMetrics = []struct{ Key, Label, Tone string }{
	{metricOnTime, "On time", "ok"},
	{metricTripsRun, "Trips run", ""},
	{metricEarly, "Early", "early"},
	{metricLate, "Late", "late"},
	{metricNoReport, "Stops with no report", ""},
}

// analyticsRangeDays is the longest period the page covers. Each day is a
// service report of its own, and the reporter keeps only so many, so a longer
// period waits until its cost has been measured.
const analyticsRangeDays = 7

// analyticsQuery is what the Analytics page was asked for. All of it is in
// the address, so a bookmark is a saved report.
type analyticsQuery struct {
	// Date is the last day shown, YYYY-MM-DD; "" is yesterday.
	Date    string
	RouteID string
	// Days is 1 or analyticsRangeDays.
	Days   int
	Metric string
	// Compare draws the period before behind a trend.
	Compare bool
}

func parseAnalyticsQuery(q url.Values) analyticsQuery {
	a := analyticsQuery{
		Date:    q.Get("date"),
		RouteID: q.Get("route_id"),
		Days:    analyticsRangeDays,
		Metric:  metricOnTime,
		Compare: q.Get("compare") != "off",
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

// analyticsTiles are the four tiles of a single day.
type analyticsTiles struct {
	Split     onTimeSplit
	Ran       int
	Scheduled int
	NotRun    int
	Early     int
	Late      int
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
}

func (c *reportCounts) add(o reportCounts) {
	c.Scheduled += o.Scheduled
	c.Run += o.Run
	c.OnTime += o.OnTime
	c.Early += o.Early
	c.Late += o.Late
	c.Unknown += o.Unknown
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

func routeCounts(r routeReport) reportCounts {
	return reportCounts{
		Scheduled: r.TripsScheduled, Run: r.TripsRun,
		OnTime: r.StopsOnTime, Early: r.StopsEarly, Late: r.StopsLate, Unknown: r.StopsUnknown,
	}
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

// analyticsDaysAtOnce is how many of a period's days are worked out at the
// same time. Each holds a database connection while it reads its trips and
// trails, and pgxpool keeps at least four.
const analyticsDaysAtOnce = 4

// analyticsDays works out the service report of each of the days that end on
// last, oldest first. Days are worked out analyticsDaysAtOnce at a time, so a
// cold cache costs a few days' reads rather than all of them in a row. The
// first day that fails, or the request ending, stops the days not started
// yet and cancels the reads under way.
func analyticsDays(ctx context.Context, reports *serviceReporter, recent *overviewReports, ix *rider.Index, today, last time.Time, days int) ([]*serviceReport, error) {
	out := make([]*serviceReport, days)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(analyticsDaysAtOnce)
	for i := range out {
		day := last.AddDate(0, 0, i-(days-1))
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
		// CompareOff is carried by the date form, which has no chip to read.
		"CompareOff": query.Days > 1 && !query.Compare,
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

	days, err := analyticsDays(ctx, reports, recent, ix, today, day, query.Days)
	var before []*serviceReport
	if err == nil && query.Days > 1 && query.Compare {
		before, err = analyticsDays(ctx, reports, recent, ix, today, day.AddDate(0, 0, -query.Days), query.Days)
	}
	switch {
	case errors.Is(err, errNoSchedule):
		return noSchedule()
	case err != nil:
		return nil, 0, err
	}

	rep := days[len(days)-1]
	query.Date = rep.Date
	settings := reportSettings{
		onTimeEarly: time.Duration(rep.OnTimeEarlyS) * time.Second,
		onTimeLate:  time.Duration(rep.OnTimeLateS) * time.Second,
	}
	data["Date"] = rep.Date
	data["OnTimeEarly"] = humanizeDuration(settings.onTimeEarly)
	data["OnTimeLate"] = humanizeDuration(settings.onTimeLate)
	data["FixWindow"] = humanizeDuration(reportFixWindow)

	oneDay, period := query, query
	oneDay.RouteID, oneDay.Days = "", 1
	period.RouteID, period.Days = "", analyticsRangeDays
	data["Range"] = []analyticsLink{
		{Label: "Day", URL: oneDay.URL(), Current: query.Days == 1},
		{Label: strconv.Itoa(analyticsRangeDays) + " days", URL: period.URL(), Current: query.Days > 1},
	}
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
	if query.Days == 1 {
		data["CSVURL"] = "/api/v1/admin/reports/service?" + url.Values{"date": {rep.Date}, "format": {"csv"}}.Encode()
	} else {
		toggled := query
		toggled.Compare = !query.Compare
		data["Compare"] = analyticsLink{Label: fmt.Sprintf("Compare with the %d days before", query.Days), URL: toggled.URL(), Current: query.Compare}
	}
	tz := "Times are " + rep.Timezone + " time."
	when := periodPhrase(day, today, query.Days)
	isToday := query.Days == 1 && day.Equal(today)
	data["IsToday"] = isToday

	if query.RouteID != "" {
		for _, route := range rep.Routes {
			if route.RouteID != query.RouteID {
				continue
			}
			row := newReportRouteRow(ix, rep.Date, route.RouteID, route.ShortName, route.LongName, routeCounts(route))
			counts := routeCounts(route)
			data["Route"] = row
			data["Headline"] = row.Name + " " + when
			data["Lede"] = strings.Join(append(routeDaySentences(counts, isToday), tz), " ")
			data["Tiles"] = newAnalyticsTiles(counts)
			trips := make([]reportTripRow, 0, len(route.Trips))
			for _, trip := range route.Trips {
				trips = append(trips, newReportTripRow(rep, trip, day))
			}
			data["Trips"] = trips
			return data, http.StatusOK, nil
		}
		data["NoRoute"] = true
		data["Headline"] = "Route " + query.RouteID + " " + when
		return data, http.StatusOK, nil
	}

	routes, total := routeTotals(days)
	rows := make([]reportRouteRow, 0, len(routes))
	for _, r := range routes {
		rows = append(rows, newReportRouteRow(ix, rep.Date, r.ID, r.ShortName, r.LongName, r.reportCounts))
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
	data["Lede"] = strings.Join(append(sentences, tz), " ")

	if query.Days == 1 {
		data["Tiles"] = newAnalyticsTiles(total)
	} else {
		data["Metrics"] = newAnalyticsMetrics(query, total, totalBefore)
		data["Trend"] = newAnalyticsTrend(query, days, before)
	}
	if total.judged() > 0 {
		data["Hours"] = newHourChart(hoursOfDay(days))
	}
	buckets, leftOut := deviationBuckets(days, settings)
	if buckets != nil {
		data["Histogram"] = newHistChart(buckets, settings.onTimeEarly, settings.onTimeLate)
	}
	data["HistogramLeftOut"] = leftOut
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
// stops judged, or why there is none.
func analyticsHeadline(total reportCounts, when string, isToday bool) string {
	split := total.split()
	switch {
	case split.Known && isToday:
		return fmt.Sprintf("On time is %d%% so far today", split.Percent)
	case split.Known:
		return fmt.Sprintf("On time was %d%% %s", split.Percent, when)
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

// routeTotals adds each route up over days, and all of them together. The
// routes are in the newest day's order, then those only older days have.
func routeTotals(days []*serviceReport) ([]routeTotal, reportCounts) {
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
			routes[n].add(routeCounts(route))
			total.add(routeCounts(route))
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
func worstRouteSentence(days []*serviceReport, routes []routeTotal, total reportCounts) string {
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
	for _, rep := range days {
		for _, route := range rep.Routes {
			if route.RouteID != worst.ID {
				continue
			}
			for _, trip := range route.Trips {
				n := trip.StopsLate
				if kind == "early" {
					n = trip.StopsEarly
				}
				byHour[serviceHour(rep, trip)] += n
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

// serviceHour is the hour of the service day trip was scheduled to leave in,
// on the agency's clock: 24 and up after midnight, so the last trips of a
// day sort after its evening.
func serviceHour(rep *serviceReport, trip tripReport) int {
	day, _ := time.Parse(reportDateLayout, rep.Date)
	local := trip.ScheduledDeparture.In(rep.location)
	y, m, d := local.Date()
	return local.Hour() + 24*int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(day).Hours()/24)
}

// hoursOfDay groups the trips of days by the hour they were scheduled to
// leave in, from the first hour with a trip to the last with none left out.
func hoursOfDay(days []*serviceReport) []hourShares {
	byHour := make(map[int]*hourShares)
	first, last := 0, -1
	for _, rep := range days {
		for _, route := range rep.Routes {
			for _, trip := range route.Trips {
				hour := serviceHour(rep, trip)
				h, ok := byHour[hour]
				if !ok {
					h = &hourShares{Hour: hour}
					byHour[hour] = h
					if last < first || hour < first {
						first = hour
					}
					last = max(last, hour)
				}
				h.Trips++
				h.add(reportCounts{OnTime: trip.StopsOnTime, Early: trip.StopsEarly, Late: trip.StopsLate})
			}
		}
	}
	hours := make([]hourShares, 0, last-first+1)
	for hour := first; hour <= last; hour++ {
		if h, ok := byHour[hour]; ok {
			hours = append(hours, *h)
		} else {
			hours = append(hours, hourShares{Hour: hour})
		}
	}
	return hours
}

// deviationBuckets counts the trips of days by the minute they reached
// their last stop against the timetable, from three minutes before the
// on-time window to eight after it. It also returns how many trips that ran
// had no report at the last stop and are in no bucket. The buckets are nil
// when no trip is in one.
func deviationBuckets(days []*serviceReport, s reportSettings) ([]deviationBucket, int) {
	first := -int(math.Ceil(s.onTimeEarly.Minutes())) - 3
	last := int(math.Ceil(s.onTimeLate.Minutes())) + 7
	buckets := make([]deviationBucket, last-first+1)
	for i := range buckets {
		buckets[i].Minute = first + i
	}
	counted, leftOut := 0, 0
	for _, rep := range days {
		for _, route := range rep.Routes {
			for _, trip := range route.Trips {
				if len(trip.Runs) == 0 {
					continue
				}
				if trip.ArrivalDeviationS == nil {
					leftOut++
					continue
				}
				counted++
				d := time.Duration(*trip.ArrivalDeviationS) * time.Second
				b := &buckets[min(last, max(first, int(math.Floor(d.Minutes()))))-first]
				switch s.verdict(d) {
				case stopEarly:
					b.Early++
				case stopLate:
					b.Late++
				default:
					b.OnTime++
				}
			}
		}
	}
	if counted == 0 {
		return nil, leftOut
	}
	return buckets, leftOut
}

func newAnalyticsTiles(c reportCounts) analyticsTiles {
	return analyticsTiles{
		Split:     c.split(),
		Ran:       c.Run,
		Scheduled: c.Scheduled,
		NotRun:    c.Scheduled - c.Run,
		Early:     c.Early,
		Late:      c.Late,
	}
}

// newAnalyticsMetrics is the tabs over the trend. A tab's note is how the
// measure moved against the period before, in points, when there is one to
// set it against, and otherwise the counts it is a share of.
func newAnalyticsMetrics(query analyticsQuery, total reportCounts, before *reportCounts) []analyticsMetric {
	stops := func(n int) string { return countOf(n, "stop", "stops") }
	counts := map[string]string{
		metricOnTime:   fmt.Sprintf("%d of %s", total.OnTime, stops(total.judged())),
		metricTripsRun: fmt.Sprintf("%d of %d a day", int(math.Round(float64(total.Run)/float64(query.Days))), int(math.Round(float64(total.Scheduled)/float64(query.Days)))),
		metricEarly:    stops(total.Early),
		metricLate:     stops(total.Late),
		metricNoReport: fmt.Sprintf("%d of %s", total.Unknown, stops(total.judged()+total.Unknown)),
	}
	out := make([]analyticsMetric, 0, len(analyticsMetrics))
	for _, m := range analyticsMetrics {
		tab := query
		tab.Metric = m.Key
		metric := analyticsMetric{Label: m.Label, Value: "None", Note: counts[m.Key], URL: tab.URL(), Selected: m.Key == query.Metric}
		if m.Key == metricEarly || m.Key == metricLate {
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
func newAnalyticsTrend(query analyticsQuery, days, before []*serviceReport) analyticsTrend {
	trend := analyticsTrend{Rows: make([]analyticsTrendRow, 0, len(days))}
	for _, m := range analyticsMetrics {
		if m.Key == query.Metric {
			trend.Label, trend.Tone = m.Label, m.Tone
		}
	}
	date := func(rep *serviceReport, layout string) string {
		day, _ := time.Parse(reportDateLayout, rep.Date)
		return day.Format(layout)
	}
	measures := func(reps []*serviceReport) []*float64 {
		out := make([]*float64, 0, len(reps))
		for _, rep := range reps {
			out = append(out, dayCounts(rep).measure(query.Metric))
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
			Vehicle:  run.VehicleLabel,
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
