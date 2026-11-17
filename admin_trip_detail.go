package main

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// defaultTrailColor is the trail's colour when the schedule gives the route
// none: the teal the live map draws a trail in.
const defaultTrailColor = "#0f766e"

// gtfsColorPattern is a GTFS route_color or route_text_color: six hex digits
// and no '#'.
var gtfsColorPattern = regexp.MustCompile(`^[0-9A-Fa-f]{6}$`)

// tripDetail is the page for one driver trip.
type tripDetail struct {
	ID           int64
	VehicleID    string
	VehicleLabel string
	DriverName   string
	Route        tripRoute
	GtfsTripID   string
	Headsign     string
	BlockID      string
	Start        string
	End          string
	Status       string
	Active       bool
	Duration     string
	Reports      int
	FirstReport  string
	LastReport   string
	// Timezone is the clock every time on the page is on: the agency's when
	// the schedule is loaded, else UTC.
	Timezone string
	// Timetable is set when the stops have scheduled times to compare with,
	// and Summary is then the trip's row as the Reports page shows it.
	Timetable bool
	Summary   reportTripRow
	// Note says why the page has no stops, or no timetable for them.
	Note  string
	Stops []tripStopRow
	Gaps  []tripGap
	CSV   []tripCSVPart
	Map   tripMap

	GapAfter    string
	OnTimeEarly string
	OnTimeLate  string
	FixWindow   string
}

// tripRoute is the route badge: the route's short name, or its long name, or
// its id, in the route's colours when the schedule gives them.
type tripRoute struct {
	ID        string
	Name      string
	LongName  string
	Color     string
	TextColor string
}

// tripStopRow is one scheduled stop of the trip.
type tripStopRow struct {
	Sequence int
	Name     string
	// Scheduled is the time the stop is judged against, departure but for
	// the last stop's arrival; "" when there is no timetable.
	Scheduled string
	// Passed is when the vehicle left the stop, or reached the last one; ""
	// when that is not known.
	Passed    string
	Deviation reportDeviation
	// Ahead is a stop a running trip has not got to yet.
	Ahead bool
	// Timed is whether the Reports page counts the stop: a timepoint, or any
	// stop when the schedule marks none.
	Timed bool
}

// tripGap is a stretch longer than the staleness threshold with no report.
type tripGap struct {
	From   string
	To     string
	Length string
	// Index is the report before the gap, for the slider to go to.
	Index int
}

// tripCSVPart is a link to download some of the trip's reports.
type tripCSVPart struct {
	URL   string
	Label string
}

// tripMap is what the page's script draws the map from. The trail itself
// comes from TrailURL, so an active trip's page can fetch it again.
type tripMap struct {
	TrailURL   string        `json:"trail_url"`
	Color      string        `json:"color"`
	GapSeconds float64       `json:"gap_seconds"`
	Timezone   string        `json:"timezone"`
	Active     bool          `json:"active"`
	Shape      [][2]float64  `json:"shape"`
	Stops      []tripMapStop `json:"stops"`
}

type tripMapStop struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// tripDetailPage renders one driver trip: who drove it and when, its trail
// over the trip's shape, when it passed each stop against the timetable, the
// gaps in its reports, and links to download them.
func (ui *adminUI) tripDetailPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	trip, err := ui.tripTrails.GetTripSummary(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrTripNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("trip page: get trip", "trip_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	points, err := ui.tripTrails.ListTripLocations(r.Context(), id)
	if err != nil {
		slog.Error("trip page: list trip locations", "trip_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	detail := newTripDetail(*trip, points, ui.reports.index(), ui.reports.settings, ui.cfg.stalenessThreshold, time.Now())
	ui.renderAdmin(w, r, http.StatusOK, "trip_detail.html", map[string]interface{}{
		"Title": "Trip " + strconv.FormatInt(id, 10),
		"Page":  "trips",
		"Trip":  detail,
	})
}

// newTripDetail is the page for trip, whose reports are points in the order
// driven, judged against ix as the Reports page judges it with s. ix is nil
// when no schedule is loaded. A gap is a stretch of more than gapAfter with no
// report.
func newTripDetail(trip TripSummary, points []LocationPoint, ix *rider.Index, s reportSettings, gapAfter time.Duration, now time.Time) tripDetail {
	loc := time.UTC
	if ix != nil {
		loc = ix.Timezone()
	}
	d := tripDetail{
		ID:           trip.ID,
		VehicleID:    trip.VehicleID,
		VehicleLabel: trip.VehicleLabel,
		DriverName:   trip.DriverName,
		Route:        tripRoute{ID: trip.RouteID, Name: trip.RouteID},
		GtfsTripID:   trip.GtfsTripID,
		Start:        formatTripTime(trip.StartTime, loc),
		End:          "—",
		Status:       trip.Status,
		Active:       trip.Status == "active",
		Reports:      len(points),
		Timezone:     loc.String(),
		Stops:        make([]tripStopRow, 0),
		Gaps:         tripGaps(points, gapAfter, loc),
		CSV:          tripCSVParts(trip.VehicleID, points, loc),
		Map: tripMap{
			TrailURL:   "/api/v1/admin/trips/" + strconv.FormatInt(trip.ID, 10) + "/locations",
			Color:      defaultTrailColor,
			GapSeconds: gapAfter.Seconds(),
			Timezone:   loc.String(),
			Active:     trip.Status == "active",
			Shape:      make([][2]float64, 0),
			Stops:      make([]tripMapStop, 0),
		},
		GapAfter:    humanizeDuration(gapAfter),
		OnTimeEarly: humanizeDuration(s.onTimeEarly),
		OnTimeLate:  humanizeDuration(s.onTimeLate),
		FixWindow:   humanizeDuration(reportFixWindow),
	}
	if trip.EndTime != nil {
		d.End = formatTripTime(*trip.EndTime, loc)
		d.Duration = formatTripDuration(trip.StartTime, trip.EndTime)
	} else {
		d.Duration = formatTripDuration(trip.StartTime, &now) + " so far"
	}
	if len(points) > 0 {
		d.FirstReport = reportClock(points[0], loc)
		d.LastReport = reportClock(points[len(points)-1], loc)
	}

	if ix == nil {
		if trip.GtfsTripID != "" {
			d.Note = "The GTFS schedule is not loaded, so the trip's stops can't be shown."
		}
		return d
	}
	if route, ok := ix.Route(trip.RouteID); ok {
		d.Route = newTripRoute(route)
		if d.Route.Color != "" {
			d.Map.Color = d.Route.Color
		}
	}
	if trip.GtfsTripID == "" {
		return d
	}
	info, ok := ix.Trip(trip.GtfsTripID)
	if !ok {
		d.Note = fmt.Sprintf("Trip %s is not in the GTFS schedule now loaded, so its stops can't be shown.", trip.GtfsTripID)
		return d
	}
	d.Headsign, d.BlockID = info.Headsign, info.BlockID
	for _, stop := range info.StopTimes {
		d.Map.Stops = append(d.Map.Stops, tripMapStop{Name: stop.StopName, Lat: stop.Pos.Lat, Lon: stop.Pos.Lon})
	}
	// A shape drawn through the stops cuts every corner, so it is left off
	// the map rather than drawn as the way the trip goes.
	var fixes []placedFix
	reached := math.Inf(1)
	if !info.ShapeFromStops {
		for _, p := range info.Shape.Points {
			d.Map.Shape = append(d.Map.Shape, [2]float64{p.Lat, p.Lon})
		}
		fixes = placeFixes(info, points, s.maxShapeDistance)
		// A running trip has not got past the furthest it has been placed,
		// so a stop beyond that is still ahead of it, not unknown.
		if d.Active {
			reached = 0
			for _, f := range fixes {
				reached = math.Max(reached, f.along)
			}
		}
	}

	y, m, dd := trip.StartTime.In(loc).Date()
	startDay := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
	if startTime, headway := info.Run(ix.RunServiceDate(info, trip.StartTime), loc, trip.StartTime); headway || startTime != "" {
		d.Note = fmt.Sprintf("Trip %s runs from the GTFS frequencies, so it has no timetable to compare with: the stops show when the vehicle passed them.", trip.GtfsTripID)
		d.Stops = tripStopRows(info, fixes, reached, nil, loc, startDay)
		return d
	}
	date, ok := runServiceDate(ix, info, trip.StartTime)
	if !ok {
		d.Note = fmt.Sprintf("No run of trip %s is scheduled within %s of this trip's start, so it has no timetable to compare with: the stops show when the vehicle passed them.", trip.GtfsTripID, humanizeDuration(maxRunGap))
		d.Stops = tripStopRows(info, fixes, reached, nil, loc, startDay)
		return d
	}
	// Both come from a date the index runs the trip on, so neither can fail.
	dayStart, _ := rider.ServiceDayStart(date, loc)
	day, _ := time.Parse(gtfsDateLayout, date)

	tr := newTripReport(info, dayStart, []TripSummary{trip})
	tr.setEvaluation(evaluateRuns(info, dayStart, [][]LocationPoint{points}, s))
	rep := &serviceReport{
		OnTimeEarlyS: int(s.onTimeEarly / time.Second),
		OnTimeLateS:  int(s.onTimeLate / time.Second),
		location:     loc,
	}
	d.Timetable = true
	d.Summary = newReportTripRow(rep, tr, day)
	d.Stops = tripStopRows(info, fixes, reached, &tripTimetable{dayStart: dayStart, settings: s}, loc, day)
	if info.ShapeFromStops {
		d.Note = fmt.Sprintf("Trip %s has no shape in the GTFS schedule, so the vehicle can't be placed along it and every stop is unknown, as on the Reports page.", trip.GtfsTripID)
	}
	return d
}

// tripTimetable is the run of a trip the stops are judged against: its
// service day's start, and the on-time window.
type tripTimetable struct {
	dayStart time.Time
	settings reportSettings
}

// tripStopRows is a row for each of trip's stops: when the vehicle placed at
// fixes passed it and, with a timetable, how that compares with the time it
// was due. A stop is passed when the vehicle leaves it and the last stop when
// it reaches it, the moments evaluateRuns judges them at, so each row agrees
// with the Reports page. A stop the vehicle has not been placed as far as,
// reached metres along the shape, is still ahead of it. day is the calendar
// day a time is "next day" after.
func tripStopRows(trip *rider.TripInfo, fixes []placedFix, reached float64, tt *tripTimetable, loc *time.Location, day time.Time) []tripStopRow {
	timed := make(map[int]struct{}, len(trip.StopTimes))
	for _, i := range judgedStops(trip.StopTimes) {
		timed[i] = struct{}{}
	}

	last := len(trip.StopTimes) - 1
	rows := make([]tripStopRow, 0, len(trip.StopTimes))
	for i, stop := range trip.StopTimes {
		target := math.Min(stop.AlongShape+stoppedAtDistance, trip.Shape.Length)
		due := stop.Departure
		if i == last {
			target = math.Max(stop.AlongShape-stoppedAtDistance, 0)
			due = stop.Arrival
		}
		_, isTimed := timed[i]
		row := tripStopRow{Sequence: stop.Sequence, Name: stop.StopName, Timed: isTimed}
		at, passed := passedAt(fixes, target)
		if passed {
			row.Passed = formatPassedTime(at, loc, day)
		}
		if tt != nil {
			row.Scheduled = formatScheduledTime(tt.dayStart.Add(due), loc, day)
		}
		if !passed && target > reached {
			row.Ahead = true
			rows = append(rows, row)
			continue
		}
		if tt != nil {
			scheduled := tt.dayStart.Add(due)
			var seconds *int
			if passed {
				s := int(at.Sub(scheduled) / time.Second)
				seconds = &s
			}
			row.Deviation = formatDeviation(seconds, tt.settings)
		}
		rows = append(rows, row)
	}
	return rows
}

// formatPassedTime is formatScheduledTime to the second.
func formatPassedTime(t time.Time, loc *time.Location, day time.Time) string {
	local := t.In(loc)
	text := local.Format("15:04:05")
	if y, m, d := local.Date(); time.Date(y, m, d, 0, 0, 0, 0, time.UTC).After(day) {
		text += " next day"
	}
	return text
}

// formatTripTime is a trip's start or end on the page's clock, with the zone.
func formatTripTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02 15:04 MST")
}

// reportClock is when p was recorded, on the page's clock.
func reportClock(p LocationPoint, loc *time.Location) string {
	return time.Unix(p.Timestamp, 0).In(loc).Format("15:04:05")
}

// newTripRoute is route's badge. GTFS colours a route with no route_color
// white and its text black; a route with no colour here keeps the page's own
// badge colours instead, as a white badge would vanish into the page.
func newTripRoute(route rider.RouteInfo) tripRoute {
	r := tripRoute{ID: route.ID, Name: route.ShortName, LongName: route.LongName}
	if r.Name == "" {
		r.Name, r.LongName = route.LongName, ""
	}
	if r.Name == "" {
		r.Name = route.ID
	}
	if gtfsColorPattern.MatchString(route.Color) {
		r.Color = "#" + route.Color
		r.TextColor = "#000000"
		if gtfsColorPattern.MatchString(route.TextColor) {
			r.TextColor = "#" + route.TextColor
		}
	}
	return r
}

// tripGaps returns each stretch of more than after between one report and the
// next, points being in the order driven.
func tripGaps(points []LocationPoint, after time.Duration, loc *time.Location) []tripGap {
	gaps := make([]tripGap, 0)
	for i := 1; i < len(points); i++ {
		silent := time.Duration(points[i].Timestamp-points[i-1].Timestamp) * time.Second
		if silent <= after {
			continue
		}
		gaps = append(gaps, tripGap{
			From:   reportClock(points[i-1], loc),
			To:     reportClock(points[i], loc),
			Length: humanizeDuration(silent),
			Index:  i - 1,
		})
	}
	return gaps
}

// tripCSVParts links the trip's reports, points in the order driven, to the
// vehicle location export. One download holds at most maxHistoryLimit
// reports, the newest of its window, so a longer trip is split into windows of
// at most that many, each ending the second before the next begins. Reports
// sent in the same second are never split between two windows, as a window
// is whole seconds: a part ends before such a second, or, when the second
// alone holds more than one download, after it, and that download is short
// of the second's oldest reports. The export pages by nothing finer.
func tripCSVParts(vehicleID string, points []LocationPoint, loc *time.Location) []tripCSVPart {
	parts := make([]tripCSVPart, 0)
	for start := 0; start < len(points); {
		end := min(start+maxHistoryLimit, len(points))
		cut := end
		for cut > start && cut < len(points) && points[cut].Timestamp == points[cut-1].Timestamp {
			cut--
		}
		if cut > start {
			end = cut
		} else {
			for end < len(points) && points[end].Timestamp == points[end-1].Timestamp {
				end++
			}
		}
		from, to := points[start].Timestamp, points[end-1].Timestamp
		if end < len(points) {
			to = points[end].Timestamp - 1
		}
		q := url.Values{
			"format": {"csv"},
			"from":   {strconv.FormatInt(from, 10)},
			"to":     {strconv.FormatInt(to, 10)},
			"limit":  {strconv.Itoa(maxHistoryLimit)},
		}
		parts = append(parts, tripCSVPart{
			URL:   "/api/v1/admin/vehicles/" + url.PathEscape(vehicleID) + "/locations?" + q.Encode(),
			Label: reportClock(points[start], loc) + " to " + reportClock(points[end-1], loc),
		})
		start = end
	}
	return parts
}
