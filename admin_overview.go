package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// overviewReportTTL is how long the Overview keeps today's service report.
// The reporter keeps only days whose service is over, so today's is worked
// out from every trail of the day on each request; a page that refreshes
// itself every ten seconds would otherwise be the heaviest on the site.
const overviewReportTTL = time.Minute

// overviewReports are the two service reports the Overview reads, each kept
// for overviewReportTTL.
type overviewReports struct {
	today     reportCache
	yesterday reportCache
}

// reportCache keeps one day's service report for overviewReportTTL. The zero
// value is ready to use.
type reportCache struct {
	mu   sync.Mutex
	date string
	load scheduleLoad
	at   time.Time
	rep  *serviceReport
}

// get returns the report for date against ix, working it out when the one
// kept is for another day or another load of the schedule, or is older than
// overviewReportTTL. The lock is held while it is worked out, so requests that
// arrive together share one read of the day's trails.
func (c *reportCache) get(ctx context.Context, reports *serviceReporter, ix *rider.Index, date string) (*serviceReport, error) {
	load := loadOf(ix)
	now := reports.now()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rep != nil && c.date == date && c.load.same(load) && now.Sub(c.at) < overviewReportTTL {
		return c.rep, nil
	}
	rep, err := reports.report(ctx, date)
	if err != nil {
		return nil, err
	}
	c.date, c.load, c.at, c.rep = date, load, now, rep
	return rep, nil
}

// overviewView is the Overview: one sentence on the fleet, four tiles, each
// route with its vehicles along it, what needs attention, the last hour's
// reports and the state of the feed, the schedule and rider mode.
type overviewView struct {
	Headline string
	Lede     string
	// Now is when the page was drawn, on the clock Timezone names.
	Now      string
	Timezone string
	// Problem is the one thing that needs a look now; nil when nothing does.
	Problem *overviewProblem

	OnTrip   int
	Vehicles int
	// Units is a mark for each vehicle on a trip, reporting ones first:
	// "seg-ok" or "seg-warn". It is empty when there are too many to draw
	// one each, and the two counts are drawn in proportion instead.
	Units     []string
	Reporting int
	Quiet     int

	OnTime overviewOnTime
	// ReportsURL opens Analytics on today.
	ReportsURL string

	TripsRun       int
	TripsScheduled int
	// HasSchedule is false when no schedule is loaded, and the trips tile
	// then has no scheduled count to show.
	HasSchedule bool
	NotStarted  int

	AlertCount  int
	LongestOpen string

	Routes    []overviewRoute
	Attention []overviewAttention
	Chart     barChartView
	Accepted  uint64
	Rejected  uint64
	System    []overviewSystemRow
}

// overviewProblem is the problem line: what is wrong and where to see it.
type overviewProblem struct {
	Text   string
	URL    string
	Action string
}

// overviewOnTime is how the stops passed today stood against the timetable,
// as whole percentages of the stops judged. Known is false when none has
// been judged, and the shares then say nothing.
type overviewOnTime struct {
	Known   bool
	Percent int
	Early   int
	Late    int
}

// overviewRoute is one route with a trip running: its badge, how many of its
// vehicles are reporting, and the Line, with the stops of one of its trips
// and each vehicle where it is along the route.
type overviewRoute struct {
	Route     tripRoute
	Name      string
	URL       string
	OnTrip    int
	Reporting int
	// OnTime is the route's on-time share today, "88%"; empty when none of
	// its stops has been judged.
	OnTime   string
	Stops    []string
	Vehicles []overviewVehicle
}

// overviewVehicle is a vehicle on a route's Line. Left is how far along the
// line it is drawn, a percentage; Back marks one running the route's other
// direction, which is drawn right to left so both share one line.
type overviewVehicle struct {
	ID    string
	Label string
	Left  string
	Back  bool
	Quiet bool
}

// overviewAttention is one of the alerts the Overview lists.
type overviewAttention struct {
	Icon   string
	Title  string
	Detail string
	URL    string
}

// overviewSystemRow is one line of the Overview's system card. Tone is the
// pill's: "ok", "warn" or "" for a plain one.
type overviewSystemRow struct {
	Icon   string
	Name   string
	Detail string
	State  string
	Tone   string
	URL    string
}

// overviewMaxUnits is the most vehicles the first tile draws a mark each for.
const overviewMaxUnits = 30

// overviewMaxStops is the most stops a route's Line draws. A long route's
// stops are thinned to this many, its first and last always kept.
const overviewMaxStops = 24

// overviewAttentionLimit is how many alerts the Overview lists before
// pointing at the Alerts page.
const overviewAttentionLimit = 3

// dashboardPage renders the Overview, or the welcome while the server has no
// vehicle or no driver to show anything of.
func (ui *adminUI) dashboardPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vehicles, err := ui.stats.CountActiveVehicles(ctx)
	if err != nil {
		slog.Error("overview: count active vehicles", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	drivers, err := ui.stats.CountActiveUsersByRole(ctx, roleDriver)
	if err != nil {
		slog.Error("overview: count active drivers", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if vehicles == 0 || drivers == 0 {
		ui.renderAdmin(w, r, http.StatusOK, "dashboard.html", map[string]interface{}{
			"Title":   "Welcome",
			"Page":    "dashboard",
			"Welcome": newWelcomeView(vehicles, drivers),
		})
		return
	}

	tripsByVehicle, err := ui.activeTrips.ListActiveTripsByVehicle(ctx)
	if err != nil {
		slog.Error("overview: list active trips by vehicle", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	now := ui.reports.now()
	// The Overview's numbers are the alerts, so it does not draw itself
	// without them as the shell does.
	alerts, err := ui.alerts.list(ctx, now)
	if err != nil {
		slog.Error("overview: list alerts", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	ix := ui.reports.index()
	var today, yesterday *serviceReport
	if ix != nil {
		// ServiceDate writes the date in the layout it is parsed with here.
		day, _ := time.Parse(gtfsDateLayout, ix.ServiceDate(now))
		if today, err = ui.overview.today.get(ctx, ui.reports, ix, day.Format(reportDateLayout)); err != nil {
			slog.Error("overview: today's report", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		// The reporter keeps the day before once its service is over; until
		// then it is worked out afresh like today's, so it is kept here too.
		if yesterday, err = ui.overview.yesterday.get(ctx, ui.reports, ix, day.AddDate(0, 0, -1).Format(reportDateLayout)); err != nil {
			slog.Error("overview: yesterday's report", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	riderStatus, err := ui.rider.RiderStatus(ctx)
	if err != nil {
		slog.Error("overview: rider status", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	view := newOverviewView(overviewInput{
		now:       now,
		ix:        ix,
		vehicles:  vehicles,
		trips:     tripsByVehicle,
		tracked:   ui.tracker.ActiveVehicles(),
		alerts:    alerts,
		settings:  ui.alerts.settings,
		today:     today,
		yesterday: yesterday,
		health:    ui.health.snapshot(),
		rider:     riderStatus,
	})
	ui.renderAdminWithAlerts(w, r, http.StatusOK, "dashboard.html", map[string]interface{}{
		"Title":    "Overview",
		"Page":     "dashboard",
		"Overview": view,
	}, alerts)
}

// overviewInput is everything the Overview is worked out from, read by
// dashboardPage. ix, today and yesterday are nil when no schedule is loaded.
type overviewInput struct {
	now       time.Time
	ix        *rider.Index
	vehicles  int
	trips     map[string]ActiveTripInfo
	tracked   []*VehicleState
	alerts    []alertEntry
	settings  alertSettings
	today     *serviceReport
	yesterday *serviceReport
	health    feedHealthResponse
	rider     riderStatusResponse
}

func newOverviewView(in overviewInput) overviewView {
	loc := pageLocation(in.ix)
	tracked := make(map[string]*VehicleState, len(in.tracked))
	for _, v := range in.tracked {
		tracked[v.VehicleID] = v
	}
	quiet := make(map[string]struct{})
	notStarted := 0
	var longest int64
	for _, a := range in.alerts {
		switch a.Kind {
		case alertQuiet:
			quiet[a.Trip.VehicleID] = struct{}{}
		case alertNotStarted:
			notStarted++
		}
		longest = max(longest, a.DurationSeconds)
	}
	// A vehicle on a trip is reporting when the tracker has heard from it
	// within the staleness threshold and no alert calls it quiet.
	reporting := 0
	for id := range in.trips {
		if _, isQuiet := quiet[id]; isQuiet {
			continue
		}
		if _, ok := tracked[id]; ok {
			reporting++
		}
	}

	v := overviewView{
		Now:         in.now.In(loc).Format("Monday 2 January, 15:04"),
		Timezone:    loc.String(),
		OnTrip:      len(in.trips),
		Vehicles:    in.vehicles,
		Reporting:   reporting,
		Quiet:       len(quiet),
		ReportsURL:  "/admin/reports",
		HasSchedule: in.ix != nil,
		NotStarted:  notStarted,
		AlertCount:  len(in.alerts),
		Routes:      overviewRoutes(in.ix, in.trips, tracked, quiet, in.today),
		Attention:   overviewAttentionList(in.alerts, in.settings, in.ix, loc),
		Accepted:    in.health.LastHour.Reports.Accepted,
		Rejected:    in.health.LastHour.Reports.Rejected(),
		Chart:       reportsPerMinuteChart(in.health, loc),
		System:      overviewSystem(in, loc),
	}
	if len(in.trips) <= overviewMaxUnits {
		for i := 0; i < len(in.trips); i++ {
			if i < len(in.trips)-len(quiet) {
				v.Units = append(v.Units, "seg-ok")
			} else {
				v.Units = append(v.Units, "seg-warn")
			}
		}
	}
	if longest > 0 {
		v.LongestOpen = humanizeDuration(time.Duration(longest) * time.Second)
	}
	if in.today != nil {
		v.ReportsURL = reportsPageURL(in.today.Date, "")
		v.OnTime = onTimeShares(in.today)
		for _, route := range in.today.Routes {
			v.TripsRun += route.TripsRun
			for _, trip := range route.Trips {
				if !trip.ScheduledDeparture.After(in.now) {
					v.TripsScheduled++
				}
			}
		}
	}
	v.Headline = overviewHeadline(reporting, len(in.trips))
	v.Lede = overviewLede(in.ix != nil, v.OnTime, in.yesterday)
	switch {
	case len(quiet) > 0:
		text := strconv.Itoa(len(quiet)) + " vehicles have gone quiet"
		if len(quiet) == 1 {
			text = "1 vehicle has gone quiet"
		}
		v.Problem = &overviewProblem{Text: text, URL: "/admin/map", Action: "Show on map"}
	case len(in.alerts) > 0:
		v.Problem = &overviewProblem{Text: alertsHeadline(len(in.alerts)), URL: "/admin/alerts", Action: "See alerts"}
	}
	return v
}

// overviewHeadline says how many of the vehicles on a trip are reporting.
func overviewHeadline(reporting, onTrip int) string {
	switch onTrip {
	case 0:
		return "No vehicles are on a trip"
	case 1:
		return fmt.Sprintf("%d of 1 vehicle is reporting", reporting)
	default:
		return fmt.Sprintf("%d of %d vehicles are reporting", reporting, onTrip)
	}
}

// overviewLede says how today's stops stand against the timetable, and
// against yesterday's when yesterday has stops to compare with.
func overviewLede(hasSchedule bool, today overviewOnTime, yesterday *serviceReport) string {
	switch {
	case !hasSchedule:
		return "No schedule is loaded, so nothing is judged against a timetable."
	case !today.Known:
		return "No stop has been passed yet today."
	}
	lede := fmt.Sprintf("%d%% of the stops passed today were on time", today.Percent)
	if yesterday != nil {
		if before := onTimeShares(yesterday); before.Known {
			switch diff := today.Percent - before.Percent; {
			case diff == 1:
				lede += ", 1 point up on yesterday"
			case diff > 1:
				lede += fmt.Sprintf(", %d points up on yesterday", diff)
			case diff == -1:
				lede += ", 1 point down on yesterday"
			case diff < -1:
				lede += fmt.Sprintf(", %d points down on yesterday", -diff)
			default:
				lede += ", the same as yesterday"
			}
		}
	}
	return lede + "."
}

// onTimeShares adds up a report's stops over its routes, as whole
// percentages of the stops judged. Early and late are rounded and on time
// takes what is left, so the three add up to 100.
func onTimeShares(rep *serviceReport) overviewOnTime {
	var onTime, early, late int
	for _, route := range rep.Routes {
		onTime += route.StopsOnTime
		early += route.StopsEarly
		late += route.StopsLate
	}
	judged := onTime + early + late
	if judged == 0 {
		return overviewOnTime{}
	}
	share := func(n int) int { return int(math.Round(float64(n) * 100 / float64(judged))) }
	s := overviewOnTime{Known: true, Early: share(early), Late: share(late)}
	s.Percent = 100 - s.Early - s.Late
	return s
}

// overviewRoutes is a row for each route with a trip running, in the
// schedule's order of routes, then routes the schedule does not have by id.
// A vehicle is placed where the tracker last put it along its trip's shape; a
// vehicle not yet placed, or gone from the tracker, is counted on its route
// and not drawn.
func overviewRoutes(ix *rider.Index, trips map[string]ActiveTripInfo, tracked map[string]*VehicleState, quiet map[string]struct{}, today *serviceReport) []overviewRoute {
	byRoute := make(map[string][]string)
	for vehicleID, trip := range trips {
		byRoute[trip.RouteID] = append(byRoute[trip.RouteID], vehicleID)
	}
	order := make([]string, 0, len(byRoute))
	if ix != nil {
		for _, route := range ix.Routes() {
			if _, ok := byRoute[route.ID]; ok {
				order = append(order, route.ID)
			}
		}
	}
	rest := make([]string, 0, len(byRoute))
	for id := range byRoute {
		if !slices.Contains(order, id) {
			rest = append(rest, id)
		}
	}
	slices.Sort(rest)
	order = append(order, rest...)

	onTime := make(map[string]string)
	date := ""
	if today != nil {
		date = today.Date
		for _, route := range today.Routes {
			if route.OnTimePercent != nil {
				onTime[route.RouteID] = strconv.Itoa(int(math.Round(*route.OnTimePercent))) + "%"
			}
		}
	}

	rows := make([]overviewRoute, 0, len(order))
	for _, routeID := range order {
		vehicleIDs := byRoute[routeID]
		slices.Sort(vehicleIDs)
		row := overviewRoute{
			Route:    routeBadge(ix, routeID),
			URL:      tripsPageURL("active", "", "", 1),
			OnTrip:   len(vehicleIDs),
			OnTime:   onTime[routeID],
			Stops:    make([]string, 0),
			Vehicles: make([]overviewVehicle, 0, len(vehicleIDs)),
		}
		row.Name = cmp.Or(row.Route.LongName, row.Route.Name, "No route")
		if date != "" {
			row.URL = reportsPageURL(date, routeID)
		}
		var drawn *rider.TripInfo
		for _, vehicleID := range vehicleIDs {
			state, isTracked := tracked[vehicleID]
			_, isQuiet := quiet[vehicleID]
			if isTracked && !isQuiet {
				row.Reporting++
			}
			if ix == nil {
				continue
			}
			info, ok := ix.Trip(trips[vehicleID].GtfsTripID)
			if !ok || info.ShapeFromStops || info.Shape.Length <= 0 {
				continue
			}
			// The route is drawn once, by a trip in its first direction when
			// one is running.
			if drawn == nil || (drawn.DirectionID == 1 && info.DirectionID != 1) {
				drawn = info
			}
			if !isTracked || state.alongShape == nil || state.TripID != info.ID {
				continue
			}
			along := min(max(*state.alongShape/info.Shape.Length, 0), 1)
			back := info.DirectionID == 1
			if back {
				along = 1 - along
			}
			row.Vehicles = append(row.Vehicles, overviewVehicle{
				ID:    vehicleID,
				Label: cmp.Or(state.VehicleLabel, vehicleID),
				Left:  percentAlong(along),
				Back:  back,
				Quiet: isQuiet,
			})
		}
		if drawn != nil {
			row.Stops = lineStops(drawn)
		}
		rows = append(rows, row)
	}
	return rows
}

// percentAlong writes a share of a line's length, 0 to 1, as the percentage
// a style takes.
func percentAlong(share float64) string {
	return strconv.FormatFloat(share*100, 'f', 1, 64)
}

// lineStops is where trip's stops fall along its shape, as percentages,
// thinned to overviewMaxStops with the first and last kept. A trip running
// the route's second direction is mirrored, as its vehicles are.
func lineStops(trip *rider.TripInfo) []string {
	n := len(trip.StopTimes)
	step := 1
	if n > overviewMaxStops {
		step = int(math.Ceil(float64(n-1) / float64(overviewMaxStops-1)))
	}
	stops := make([]string, 0, min(n, overviewMaxStops))
	for i, stop := range trip.StopTimes {
		if i%step != 0 && i != n-1 {
			continue
		}
		along := min(max(stop.AlongShape/trip.Shape.Length, 0), 1)
		if trip.DirectionID == 1 {
			along = 1 - along
		}
		stops = append(stops, percentAlong(along))
	}
	return stops
}

// overviewAttentionList is the alerts open longest, as the Overview names
// them: the vehicle and what is wrong, or the trip a block has not started.
func overviewAttentionList(alerts []alertEntry, settings alertSettings, ix *rider.Index, loc *time.Location) []overviewAttention {
	list := make([]overviewAttention, 0, overviewAttentionLimit)
	for _, a := range alerts {
		if len(list) == overviewAttentionLimit {
			break
		}
		row := newAlertRow(a, settings, ix, loc)
		item := overviewAttention{
			Icon:   row.Icon,
			Title:  cmp.Or(row.VehicleLabel, row.VehicleID) + ": " + strings.ToLower(row.Title),
			Detail: row.Detail,
			URL:    row.MapURL,
		}
		if a.Kind == alertNotStarted {
			item.Title = "Trip " + row.GtfsTripID + " has not started"
			item.URL = row.TripURL
		}
		// Every detail newAlertRow writes starts with an ASCII capital.
		if row.Route.Name != "" {
			item.Detail = "Route " + row.Route.Name + ", " + strings.ToLower(row.Detail[:1]) + row.Detail[1:]
		}
		list = append(list, item)
	}
	return list
}

// reportsPerMinuteChart is the last hour's location reports, a bar a minute
// with the rejected ones stacked on the accepted.
func reportsPerMinuteChart(health feedHealthResponse, loc *time.Location) barChartView {
	bars := make([]chartBar, 0, len(health.Minutes))
	for _, m := range health.Minutes {
		bars = append(bars, chartBar{
			Label: m.Minute.In(loc).Format("15:04"),
			Value: m.Reports.Accepted,
			Warn:  m.Reports.Rejected(),
		})
	}
	return newBarChart(bars, barChartOptions{
		Name:     "Location reports a minute over the last hour",
		Height:   84,
		Unit:     "accepted",
		WarnUnit: "rejected",
		Peak:     "Most in a minute",
	})
}

// overviewSystem is the system card: the feed's fetches, the schedule and
// rider mode, each with a pill for its state.
func overviewSystem(in overviewInput, loc *time.Location) []overviewSystemRow {
	feed := overviewSystemRow{Icon: "sensors", Name: "Feed", Detail: "No fetches in the last hour", State: "Idle", URL: "/admin/health"}
	if hour := in.health.LastHour.Feed; hour.Total() > 0 {
		feed.Detail = countOf(int(hour.Served), "fetch", "fetches") + " served in the last hour"
		for i := len(in.health.Minutes) - 1; i >= 0; i-- {
			if m := in.health.Minutes[i]; m.Feed.Total() > 0 {
				feed.Detail += ", last at " + m.Minute.In(loc).Format("15:04")
				break
			}
		}
		feed.State, feed.Tone = "OK", "ok"
		if hour.ServerError > 0 {
			feed.State, feed.Tone = "Errors", "warn"
		}
	}

	schedule := overviewSystemRow{Icon: "route", Name: "Schedule", Detail: "No GTFS schedule is loaded", State: "Not loaded", Tone: "warn", URL: "/admin/reports"}
	if in.ix != nil {
		stats := in.ix.Stats()
		schedule.Detail = fmt.Sprintf("%s, %s, loaded %s", countOf(stats.Routes, "route", "routes"), countOf(stats.Trips, "trip", "trips"), stats.LoadedAt.In(loc).Format("15:04"))
		if day := formatDay(stats.LoadedAt, loc, in.now); day != formatDay(in.now, loc, in.now) {
			schedule.Detail += " on " + day
		}
		schedule.State, schedule.Tone = "OK", "ok"
	}

	riders := overviewSystemRow{Icon: "hail", Name: "Rider mode", Detail: "Riders cannot share where they are", State: "Off", URL: "/admin/rider"}
	if in.rider.Enabled {
		riders.State, riders.Tone = "On", "ok"
		riders.Detail = "No rider has signed up yet"
		if in.rider.Riders != nil && in.rider.Rides != nil && in.rider.Riders.Total > 0 {
			riders.Detail = fmt.Sprintf("%s, %s in progress", countOf(in.rider.Riders.Total, "rider", "riders"), countOf(in.rider.Rides.Active, "ride", "rides"))
		}
	}
	return []overviewSystemRow{feed, schedule, riders}
}

// welcomeStep is one of the welcome's four steps.
type welcomeStep struct {
	Icon  string
	Title string
	Text  string
	URL   string
	// Action is the step's button, on the step to do next only.
	Action string
	Done   bool
}

// welcomeView is the page a server with no vehicle or no driver opens on:
// what to do to get the first vehicle on the map, and how far along that is.
type welcomeView struct {
	Line  stepLine
	Steps []welcomeStep
}

func newWelcomeView(vehicles, drivers int) welcomeView {
	steps := []welcomeStep{
		{Icon: "directions_bus", Title: "Add your vehicles", Text: "Add them one by one, or import a CSV file", URL: "/admin/vehicles", Done: vehicles > 0},
		{Icon: "group", Title: "Add your drivers", Text: "Import a CSV file, or add them one by one", URL: "/admin/users", Done: drivers > 0},
		{Icon: "qr_code_2", Title: "Hand out the setup sheet", Text: "Each driver scans a code and signs in", URL: "/admin/driver-setup"},
		{Icon: "sensors", Title: "The first report arrives", Text: "This page becomes your Overview", URL: "/admin/map"},
	}
	if vehicles > 0 {
		steps[0].Text = countOf(vehicles, "vehicle", "vehicles") + " added"
	}
	if drivers > 0 {
		steps[1].Text = countOf(drivers, "driver", "drivers") + " added"
	}
	next := 0
	if steps[0].Done {
		next = 1
	}
	steps[next].Action = [...]string{"Add vehicles", "Add drivers"}[next]
	return welcomeView{Line: stepLine{N: next + 1, Of: len(steps)}, Steps: steps}
}
