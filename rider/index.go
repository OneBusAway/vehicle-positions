package rider

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

const (
	// serviceDateLayout is the GTFS calendar date format.
	serviceDateLayout = "20060102"
	// serviceDayCutoffHour is the local hour before which "now" still belongs
	// to the previous service day, so after-midnight trips keep yesterday's
	// service date.
	serviceDayCutoffHour = 3
	// shapeDistMaxFraction is the furthest along its shape a trip's last stop
	// may sit for a unit to be believable. Slightly over 1 because rounding in
	// the feed can put the final stop a hair past the end of the shape.
	shapeDistMaxFraction = 1.05
)

// shapeDistUnits are the units feeds actually publish shape_dist_traveled in,
// as metres per unit. Recovering the unit means choosing from this list, never
// inventing a multiplier: GTFS only says the values increase along the shape,
// so any other factor would be reading meaning into an arbitrary ratio.
var shapeDistUnits = []float64{
	1,        // metres
	0.3048,   // feet
	1000,     // kilometres
	1609.344, // miles
}

// StopTimeInfo is one scheduled stop of a trip, positioned along its shape.
type StopTimeInfo struct {
	StopID     string
	StopName   string
	Sequence   int
	AlongShape float64       // metres from the start of the shape
	Arrival    time.Duration // since service-day midnight (may exceed 24h)
	Departure  time.Duration
	Pos        LatLon
}

// TripInfo is one scheduled trip with its shape geometry and stop times.
type TripInfo struct {
	ID          string
	RouteID     string
	ServiceID   string
	Headsign    string
	DirectionID int // 0 or 1 as in trips.txt; -1 when the feed gives none
	Shape       *ShapeGeom
	StopTimes   []StopTimeInfo // sorted by Sequence
}

// RouteInfo is one GTFS route as the driver catalog lists it. Only routes
// with at least one indexed trip are kept.
type RouteInfo struct {
	ID        string
	ShortName string
	LongName  string
	Color     string // hex without '#', as in routes.txt; may be empty
	TextColor string
	Type      int    // GTFS route_type
	SortOrder *int32 // route_sort_order, nil when the feed gives none
}

// IndexStats summarises a loaded index.
type IndexStats struct {
	Routes   int
	Trips    int
	Shapes   int
	LoadedAt time.Time
	Source   string
}

// Index is an immutable snapshot of the schedule data the rider engine and
// the driver catalog need. It is safe for concurrent use; nothing in it is
// mutated after BuildIndex returns.
type Index struct {
	trips        map[string]*TripInfo
	tripIDs      []string
	routes       map[string]RouteInfo
	routeList    []RouteInfo            // Routes() order
	tripsByRoute map[string][]*TripInfo // first-departure order
	services     map[string]serviceCalendar
	tz           *time.Location
	stats        IndexStats
}

// serviceCalendar is the calendar of one GTFS service, with dates reduced to
// comparable YYYYMMDD keys.
type serviceCalendar struct {
	days       [7]bool // indexed by time.Weekday
	start, end int
	added      map[int]bool
	removed    map[int]bool
}

// BuildIndex indexes the trips of a parsed GTFS feed. Trips without a usable
// shape, and trips with no stop times, are skipped: neither can be verified
// against a schedule. The static feed is not retained.
func BuildIndex(static *gtfs.Static, source string, loadedAt time.Time) (*Index, error) {
	tz, err := feedTimezone(static)
	if err != nil {
		return nil, err
	}

	ix := &Index{
		trips:        make(map[string]*TripInfo, len(static.Trips)),
		routes:       make(map[string]RouteInfo),
		tripsByRoute: make(map[string][]*TripInfo),
		services:     make(map[string]serviceCalendar, len(static.Services)),
		tz:           tz,
	}
	for i := range static.Services {
		svc := &static.Services[i]
		ix.services[svc.Id] = newServiceCalendar(svc)
	}

	shapes := make(map[string]*ShapeGeom)
	skippedShapeless, skippedNoStopTimes := 0, 0
	for i := range static.Trips {
		trip := &static.Trips[i]
		shape := shapeFor(shapes, trip.Shape)
		if shape == nil {
			skippedShapeless++
			continue
		}
		if len(trip.StopTimes) == 0 {
			// Verify interpolates a scheduled time at the point's position, and
			// the aggregator reports the next stop; neither has an answer for a
			// trip with no stop times.
			skippedNoStopTimes++
			continue
		}
		info := &TripInfo{
			ID:          trip.ID,
			Headsign:    trip.Headsign,
			DirectionID: directionID(trip.DirectionId),
			Shape:       shape,
			StopTimes:   stopTimesAlong(trip.StopTimes, shape),
		}
		if trip.Route != nil {
			info.RouteID = trip.Route.Id
			ix.tripsByRoute[info.RouteID] = append(ix.tripsByRoute[info.RouteID], info)
		}
		if trip.Service != nil {
			info.ServiceID = trip.Service.Id
		}
		ix.trips[info.ID] = info
		ix.tripIDs = append(ix.tripIDs, info.ID)
	}
	slices.Sort(ix.tripIDs)

	for _, trips := range ix.tripsByRoute {
		slices.SortStableFunc(trips, func(a, b *TripInfo) int {
			if c := cmp.Compare(a.StopTimes[0].Departure, b.StopTimes[0].Departure); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
	}
	for i := range static.Routes {
		r := &static.Routes[i]
		if _, used := ix.tripsByRoute[r.Id]; !used {
			continue
		}
		info := RouteInfo{
			ID: r.Id, ShortName: r.ShortName, LongName: r.LongName,
			Color: r.Color, TextColor: r.TextColor, Type: int(r.Type), SortOrder: cloneInt32(r.SortOrder),
		}
		ix.routes[r.Id] = info
		ix.routeList = append(ix.routeList, info)
	}
	slices.SortStableFunc(ix.routeList, compareRoutes)

	ix.stats = IndexStats{
		Routes:   len(ix.routeList),
		Trips:    len(ix.trips),
		Shapes:   len(shapes),
		LoadedAt: loadedAt,
		Source:   source,
	}
	if skippedShapeless > 0 || skippedNoStopTimes > 0 {
		slog.Info("rider: skipped unusable GTFS trips", "source", source,
			"no_shape", skippedShapeless, "no_stop_times", skippedNoStopTimes, "indexed", ix.stats.Trips)
	}
	return ix, nil
}

// Trip returns the indexed trip with the given ID.
func (ix *Index) Trip(id string) (*TripInfo, bool) {
	trip, ok := ix.trips[id]
	return trip, ok
}

// TripIDs returns the IDs of every indexed trip, sorted.
func (ix *Index) TripIDs() []string { return slices.Clone(ix.tripIDs) }

// Routes returns every route with an indexed trip, in display order:
// route_sort_order ascending with unsorted routes last, then short name in
// natural order ("7" before "10"), then long name.
func (ix *Index) Routes() []RouteInfo { return slices.Clone(ix.routeList) }

// Route returns the route with the given ID, if it has an indexed trip.
func (ix *Index) Route(id string) (RouteInfo, bool) {
	r, ok := ix.routes[id]
	return r, ok
}

// TripsOnRoute returns the route's trips active on the "YYYYMMDD" service
// date, ordered by first departure. The slice is the caller's to keep.
func (ix *Index) TripsOnRoute(routeID, serviceDate string) []*TripInfo {
	day, key, ok := ix.serviceDay(serviceDate)
	if !ok {
		return nil
	}
	var out []*TripInfo
	for _, trip := range ix.tripsByRoute[routeID] {
		if ix.activeOn(trip, day, key) {
			out = append(out, trip)
		}
	}
	return out
}

// Timezone returns the agency timezone of the feed.
func (ix *Index) Timezone() *time.Location { return ix.tz }

// Stats returns a summary of the index.
func (ix *Index) Stats() IndexStats { return ix.stats }

// ActiveOn reports whether the trip runs on the given "YYYYMMDD" service date.
// Calendar exceptions override the weekly pattern.
func (ix *Index) ActiveOn(trip *TripInfo, serviceDate string) bool {
	day, key, ok := ix.serviceDay(serviceDate)
	if !ok {
		return false
	}
	return ix.activeOn(trip, day, key)
}

// serviceDay parses a "YYYYMMDD" service date into the day it names in the
// feed's timezone and that day's calendar key; ok is false for anything else.
func (ix *Index) serviceDay(serviceDate string) (day time.Time, key int, ok bool) {
	day, err := time.ParseInLocation(serviceDateLayout, serviceDate, ix.tz)
	if err != nil {
		return time.Time{}, 0, false
	}
	return day, dateKey(day), true
}

// activeOn is ActiveOn with the service date already parsed into the day it
// names and its calendar key. TripsOnRoute asks about one date for every trip
// on the route, and parsing it once is the whole difference.
func (ix *Index) activeOn(trip *TripInfo, day time.Time, key int) bool {
	if trip == nil {
		return false
	}
	svc, ok := ix.services[trip.ServiceID]
	if !ok {
		return false
	}
	if svc.removed[key] {
		return false
	}
	if svc.added[key] {
		return true
	}
	return key >= svc.start && key <= svc.end && svc.days[day.Weekday()]
}

// ServiceDate returns the "YYYYMMDD" service date that `now` belongs to. Times
// before 03:00 in the agency timezone belong to the previous service day.
func (ix *Index) ServiceDate(now time.Time) string {
	local := now.In(ix.tz)
	if local.Hour() < serviceDayCutoffHour {
		local = local.AddDate(0, 0, -1)
	}
	return local.Format(serviceDateLayout)
}

// ServiceDateFor is ServiceDate narrowed to one trip. The 03:00 cutoff assumes
// a trip running after midnight belongs to the previous service day, which is
// right for one whose stop times run past 24:00 and wrong for one genuinely
// scheduled at, say, 00:30 on a calendar the previous day does not include. So
// when the trip does not run on the derived date, the calendar day is offered
// instead; when neither runs it, the derived date is returned and the caller
// refuses the start as it would have anyway.
func (ix *Index) ServiceDateFor(trip *TripInfo, now time.Time) string {
	date := ix.ServiceDate(now)
	if ix.ActiveOn(trip, date) {
		return date
	}
	if calendarDay := now.In(ix.tz).Format(serviceDateLayout); ix.ActiveOn(trip, calendarDay) {
		return calendarDay
	}
	return date
}

// ServiceDayStart returns the instant a "YYYYMMDD" service day starts: noon
// local time minus twelve hours, which is midnight except on DST boundaries.
func ServiceDayStart(serviceDate string, loc *time.Location) (time.Time, error) {
	day, err := time.ParseInLocation(serviceDateLayout, serviceDate, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("rider: invalid service date %q: %w", serviceDate, err)
	}
	noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, loc)
	return noon.Add(-12 * time.Hour), nil
}

// ScheduledOffsetAt returns the scheduled offset from the start of the service
// day at which the trip is `along` metres into its shape, interpolated between
// the bracketing stop times and clamped to the first and last stop.
func ScheduledOffsetAt(trip *TripInfo, along float64) time.Duration {
	if trip == nil || len(trip.StopTimes) == 0 {
		return 0
	}
	stops := trip.StopTimes
	first, last := stops[0], stops[len(stops)-1]
	if along <= first.AlongShape {
		return first.Arrival
	}
	if along >= last.AlongShape {
		return last.Arrival
	}

	for i := 1; i < len(stops); i++ {
		a, b := stops[i-1], stops[i]
		if along > b.AlongShape {
			continue
		}
		span := b.AlongShape - a.AlongShape
		if span <= 0 {
			return a.Departure
		}
		fraction := (along - a.AlongShape) / span
		return a.Departure + time.Duration(fraction*float64(b.Arrival-a.Departure))
	}
	return last.Arrival
}

// feedTimezone returns the location named by the feed's first agency.
func feedTimezone(static *gtfs.Static) (*time.Location, error) {
	if len(static.Agencies) == 0 {
		return nil, errors.New("rider: GTFS feed has no agency")
	}
	name := static.Agencies[0].Timezone
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("rider: agency timezone %q: %w", name, err)
	}
	return loc, nil
}

// newServiceCalendar copies a GTFS service into a comparable calendar.
func newServiceCalendar(svc *gtfs.Service) serviceCalendar {
	cal := serviceCalendar{
		start:   dateKey(svc.StartDate),
		end:     dateKey(svc.EndDate),
		added:   make(map[int]bool, len(svc.AddedDates)),
		removed: make(map[int]bool, len(svc.RemovedDates)),
	}
	cal.days[time.Monday] = svc.Monday
	cal.days[time.Tuesday] = svc.Tuesday
	cal.days[time.Wednesday] = svc.Wednesday
	cal.days[time.Thursday] = svc.Thursday
	cal.days[time.Friday] = svc.Friday
	cal.days[time.Saturday] = svc.Saturday
	cal.days[time.Sunday] = svc.Sunday
	for _, d := range svc.AddedDates {
		cal.added[dateKey(d)] = true
	}
	for _, d := range svc.RemovedDates {
		cal.removed[dateKey(d)] = true
	}
	return cal
}

// cloneInt32 copies the pointed-to value so the index does not retain the
// parser's pointer, keeping the parsed feed out of Index and Routes()'s
// result safe from a caller mutating it.
func cloneInt32(p *int32) *int32 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// dateKey reduces an instant to a comparable YYYYMMDD integer in its own
// location, so dates parsed in different zones still compare by calendar day.
func dateKey(t time.Time) int {
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}

// shapeFor returns the cached geometry of a GTFS shape, building it on first
// use. It returns nil for a missing or degenerate shape.
func shapeFor(cache map[string]*ShapeGeom, shape *gtfs.Shape) *ShapeGeom {
	if shape == nil || len(shape.Points) < 2 {
		return nil
	}
	if geom, ok := cache[shape.ID]; ok {
		return geom
	}
	points := make([]LatLon, len(shape.Points))
	for i, p := range shape.Points {
		points[i] = LatLon{Lat: p.Latitude, Lon: p.Longitude}
	}
	geom := NewShapeGeom(points)
	cache[shape.ID] = geom
	return geom
}

// stopTimesAlong positions a trip's stop times along its shape, in stop
// sequence order.
func stopTimesAlong(stopTimes []gtfs.ScheduledStopTime, shape *ShapeGeom) []StopTimeInfo {
	sorted := slices.Clone(stopTimes)
	slices.SortStableFunc(sorted, func(a, b gtfs.ScheduledStopTime) int {
		return cmp.Compare(a.StopSequence, b.StopSequence)
	})

	scale, useDist := shapeDistScale(sorted, shape.Length)
	out := make([]StopTimeInfo, 0, len(sorted))
	for i, st := range sorted {
		pos, located := stopPos(st.Stop)
		var along float64
		switch {
		case useDist:
			along = *st.ShapeDistanceTraveled * scale
		case located:
			// Hinting with the previous stop keeps repeated stops on a loop
			// from snapping back to the shape's first pass.
			var hint *float64
			if i > 0 {
				previous := out[i-1].AlongShape
				hint = &previous
			}
			along = shape.Project(pos, hint).AlongShape
		case i > 0:
			along = out[i-1].AlongShape
		}
		if !located {
			pos = shape.PointAt(along)
		}

		out = append(out, StopTimeInfo{
			StopID:     stopID(st.Stop),
			StopName:   stopName(st.Stop),
			Sequence:   st.StopSequence,
			AlongShape: along,
			Arrival:    st.ArrivalTime,
			Departure:  st.DepartureTime,
			Pos:        pos,
		})
	}
	return out
}

// shapeDistScale reports whether every stop time carries shape_dist_traveled
// and, if so, the factor converting those values to metres along the shape.
// Feeds publish them in kilometres, feet or miles as often as in metres, so the
// last value is compared with the shape length to recover the unit.
func shapeDistScale(stopTimes []gtfs.ScheduledStopTime, shapeLength float64) (float64, bool) {
	if len(stopTimes) == 0 {
		return 0, false
	}
	last := 0.0
	for _, st := range stopTimes {
		if st.ShapeDistanceTraveled == nil {
			return 0, false
		}
		last = *st.ShapeDistanceTraveled
	}
	if last <= 0 {
		// Present but useless: every value is zero, or the column decreases to
		// nothing. Projecting the stops onto the shape is the better answer.
		return 0, false
	}
	if shapeLength <= 0 {
		return 1, true
	}
	// The last stop's distance over the shape length gives the unit only if the
	// stop is also the end of the shape, and GTFS does not require that: a trip
	// whose last stop sits at 500 m of a 1000 m shape is not a feed publishing
	// half-metres. So test each real unit against the ratio and keep the one
	// putting the last stop furthest along without running off the end — the
	// unit that explains the values with the least left over.
	ratio := last / shapeLength
	best, bestFraction := 0.0, 0.0
	for _, unit := range shapeDistUnits {
		fraction := ratio * unit
		if fraction > bestFraction && fraction <= shapeDistMaxFraction {
			best, bestFraction = unit, fraction
		}
	}
	if best == 0 {
		// No unit places the last stop on the shape at all, so the column says
		// nothing this index can use. Projecting the stops is the better answer.
		return 0, false
	}
	return best, true
}

// stopPos returns the coordinates of a stop, if it has any.
func stopPos(stop *gtfs.Stop) (LatLon, bool) {
	if stop == nil || stop.Latitude == nil || stop.Longitude == nil {
		return LatLon{}, false
	}
	return LatLon{Lat: *stop.Latitude, Lon: *stop.Longitude}, true
}

func stopID(stop *gtfs.Stop) string {
	if stop == nil {
		return ""
	}
	return stop.Id
}

// stopName returns the display name of a stop, if it has any.
func stopName(stop *gtfs.Stop) string {
	if stop == nil {
		return ""
	}
	return stop.Name
}

// directionID maps the parser's tri-state direction onto trips.txt's 0/1,
// with -1 standing for a direction the feed did not give.
func directionID(d gtfs.DirectionID) int {
	switch d {
	case gtfs.DirectionID_False:
		return 0
	case gtfs.DirectionID_True:
		return 1
	default:
		return -1
	}
}

// compareRoutes orders routes for display: route_sort_order first (routes
// without one after every route with one), then short name in natural
// order, then long name.
func compareRoutes(a, b RouteInfo) int {
	switch {
	case a.SortOrder != nil && b.SortOrder != nil:
		if c := cmp.Compare(*a.SortOrder, *b.SortOrder); c != 0 {
			return c
		}
	case a.SortOrder != nil:
		return -1
	case b.SortOrder != nil:
		return 1
	}
	if c := compareNatural(a.ShortName, b.ShortName); c != 0 {
		return c
	}
	return cmp.Compare(a.LongName, b.LongName)
}

// compareNatural compares two names so that a leading number sorts
// numerically: "7" < "10" < "10A" < "A".
func compareNatural(a, b string) int {
	an, arest, aok := leadingNumber(a)
	bn, brest, bok := leadingNumber(b)
	switch {
	case aok && bok:
		if c := cmp.Compare(an, bn); c != 0 {
			return c
		}
		return cmp.Compare(arest, brest)
	case aok:
		return -1
	case bok:
		return 1
	}
	return cmp.Compare(a, b)
}

// leadingNumber splits a leading run of ASCII digits off s.
func leadingNumber(s string) (n int, rest string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, s, false
	}
	return n, s[i:], true
}
