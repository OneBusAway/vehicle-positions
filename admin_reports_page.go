package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// reportRouteRow is one route's row on the Reports page.
type reportRouteRow struct {
	RouteID   string
	Name      string
	Scheduled int
	Ran       int
	// OnTime and Unknown are percentages, "" when there is nothing to take
	// a percentage of: no stop time known, or no trip run.
	OnTime   string
	Early    int
	Late     int
	Unknown  string
	TripsURL string
}

// reportTripRow is one scheduled trip in a route's drill-down.
type reportTripRow struct {
	GtfsTripID string
	Headsign   string
	Scheduled  string
	Runs       []reportRunRow
	Departure  reportDeviation
	Arrival    reportDeviation
	OnTime     string
	Unknown    string
}

// reportRunRow is a driver trip that ran a scheduled trip, with a link to
// its trail.
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
}

func reportsPageURL(date, routeID string) string {
	q := url.Values{"date": {date}}
	if routeID != "" {
		q.Set("route_id", routeID)
	}
	return "/admin/reports?" + q.Encode()
}

// reportsPageView builds the Reports page for its query: every route's row
// for the day, or with route_id that route's trips. A date the page cannot
// read, or no schedule to report against, is a message on the page under
// the status it is sent with; any other error is the caller's to report.
func reportsPageView(ctx context.Context, reports *serviceReporter, q url.Values) (map[string]interface{}, int, error) {
	data := map[string]interface{}{
		"Title":   "Reports",
		"Page":    "reports",
		"Date":    q.Get("date"),
		"RouteID": q.Get("route_id"),
	}

	rep, err := reports.report(ctx, q.Get("date"))
	switch {
	case errors.Is(err, errBadReportDate):
		data["Error"] = "Date must be YYYY-MM-DD."
		return data, http.StatusBadRequest, nil
	case errors.Is(err, errNoSchedule):
		data["Error"] = "The GTFS schedule is not loaded, so there is nothing to report against."
		return data, http.StatusServiceUnavailable, nil
	case err != nil:
		return nil, 0, err
	}

	day, err := time.Parse(reportDateLayout, rep.Date)
	if err != nil {
		return nil, 0, err
	}
	data["Date"] = rep.Date
	data["DayLabel"] = day.Format("Monday 2 January 2006")
	data["Timezone"] = rep.Timezone
	data["OnTimeEarly"] = humanizeDuration(time.Duration(rep.OnTimeEarlyS) * time.Second)
	data["OnTimeLate"] = humanizeDuration(time.Duration(rep.OnTimeLateS) * time.Second)
	data["FixWindow"] = humanizeDuration(reportFixWindow)
	data["PrevURL"] = reportsPageURL(day.AddDate(0, 0, -1).Format(reportDateLayout), q.Get("route_id"))
	data["NextURL"] = reportsPageURL(day.AddDate(0, 0, 1).Format(reportDateLayout), q.Get("route_id"))
	data["AllRoutesURL"] = reportsPageURL(rep.Date, "")
	data["CSVURL"] = "/api/v1/admin/reports/service?" + url.Values{"date": {rep.Date}, "format": {"csv"}}.Encode()

	routeID := q.Get("route_id")
	if routeID == "" {
		rows := make([]reportRouteRow, 0, len(rep.Routes))
		for _, route := range rep.Routes {
			rows = append(rows, newReportRouteRow(rep.Date, route))
		}
		data["Routes"] = rows
		return data, http.StatusOK, nil
	}

	for _, route := range rep.Routes {
		if route.RouteID != routeID {
			continue
		}
		data["Route"] = newReportRouteRow(rep.Date, route)
		trips := make([]reportTripRow, 0, len(route.Trips))
		for _, trip := range route.Trips {
			trips = append(trips, newReportTripRow(rep, trip, day))
		}
		data["Trips"] = trips
		return data, http.StatusOK, nil
	}
	data["NoRoute"] = true
	return data, http.StatusOK, nil
}

// reportsPage renders the Reports page: a day's routes, or one route's trips.
func (ui *adminUI) reportsPage(w http.ResponseWriter, r *http.Request) {
	data, status, err := reportsPageView(r.Context(), ui.reports, r.URL.Query())
	if err != nil {
		slog.Error("reports: work out a service report", "date", r.URL.Query().Get("date"), "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	ui.renderAdmin(w, r, status, "reports.html", data)
}

func newReportRouteRow(date string, route routeReport) reportRouteRow {
	return reportRouteRow{
		RouteID:   route.RouteID,
		Name:      reportRouteName(route),
		Scheduled: route.TripsScheduled,
		Ran:       route.TripsRun,
		OnTime:    formatPercent(route.OnTimePercent),
		Early:     route.StopsEarly,
		Late:      route.StopsLate,
		Unknown:   formatPercent(route.UnknownPercent),
		TripsURL:  reportsPageURL(date, route.RouteID),
	}
}

// reportRouteName is how the report names a route: its short name and long
// name, or whichever it has, or its id.
func reportRouteName(r routeReport) string {
	name := strings.TrimSpace(strings.Join([]string{r.ShortName, r.LongName}, " "))
	if name == "" {
		return r.RouteID
	}
	return name
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
		Runs:   make([]reportRunRow, 0, len(trip.Runs)),
		OnTime: formatPercent(trip.OnTimePercent),
	}
	// A trip's unknown share is worth a line only when there is one.
	if trip.StopsUnknown > 0 {
		row.Unknown = formatPercent(trip.UnknownPercent)
	}
	for _, run := range trip.Runs {
		row.Runs = append(row.Runs, reportRunRow{
			Vehicle:  run.VehicleLabel,
			Driver:   run.DriverName,
			TrailURL: "/admin/map?trip_id=" + strconv.FormatInt(run.ID, 10),
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
	switch {
	case d > 0:
		text += " late"
	case d < 0:
		text += " early"
	}
	return reportDeviation{Text: text, Tone: s.verdict(d).tone()}
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
