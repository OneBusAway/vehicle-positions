package rider

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	// shapeDistMetresLo and shapeDistMetresHi bound the ratio of a trip's last
	// shape_dist_traveled to its shape length for which the values are taken to
	// be metres. Outside the band the values are rescaled onto the shape.
	shapeDistMetresLo = 0.8
	shapeDistMetresHi = 1.2
)

// StopTimeInfo is one scheduled stop of a trip, positioned along its shape.
type StopTimeInfo struct {
	StopID     string
	Sequence   int
	AlongShape float64       // metres from the start of the shape
	Arrival    time.Duration // since service-day midnight (may exceed 24h)
	Departure  time.Duration
	Pos        LatLon
}

// TripInfo is one scheduled trip with its shape geometry and stop times.
type TripInfo struct {
	ID        string
	RouteID   string
	ServiceID string
	Shape     *ShapeGeom
	StopTimes []StopTimeInfo // sorted by Sequence
}

// IndexStats summarises a loaded index.
type IndexStats struct {
	Trips    int
	Shapes   int
	LoadedAt time.Time
	Source   string
}

// Index is an immutable snapshot of the schedule data the rider engine needs.
// It is safe for concurrent use; nothing in it is mutated after BuildIndex
// returns.
type Index struct {
	trips    map[string]*TripInfo
	tripIDs  []string
	services map[string]serviceCalendar
	tz       *time.Location
	stats    IndexStats
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
// shape are skipped. The static feed is not retained.
func BuildIndex(static *gtfs.Static, source string, loadedAt time.Time) (*Index, error) {
	tz, err := feedTimezone(static)
	if err != nil {
		return nil, err
	}

	ix := &Index{
		trips:    make(map[string]*TripInfo, len(static.Trips)),
		services: make(map[string]serviceCalendar, len(static.Services)),
		tz:       tz,
	}
	for i := range static.Services {
		svc := &static.Services[i]
		ix.services[svc.Id] = newServiceCalendar(svc)
	}

	shapes := make(map[string]*ShapeGeom)
	skipped := 0
	for i := range static.Trips {
		trip := &static.Trips[i]
		shape := shapeFor(shapes, trip.Shape)
		if shape == nil {
			skipped++
			continue
		}
		info := &TripInfo{
			ID:        trip.ID,
			Shape:     shape,
			StopTimes: stopTimesAlong(trip.StopTimes, shape),
		}
		if trip.Route != nil {
			info.RouteID = trip.Route.Id
		}
		if trip.Service != nil {
			info.ServiceID = trip.Service.Id
		}
		ix.trips[info.ID] = info
		ix.tripIDs = append(ix.tripIDs, info.ID)
	}
	slices.Sort(ix.tripIDs)

	ix.stats = IndexStats{
		Trips:    len(ix.trips),
		Shapes:   len(shapes),
		LoadedAt: loadedAt,
		Source:   source,
	}
	if skipped > 0 {
		slog.Info("rider: skipped GTFS trips without a usable shape", "source", source, "skipped", skipped, "indexed", ix.stats.Trips)
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

// Timezone returns the agency timezone of the feed.
func (ix *Index) Timezone() *time.Location { return ix.tz }

// Stats returns a summary of the index.
func (ix *Index) Stats() IndexStats { return ix.stats }

// ActiveOn reports whether the trip runs on the given "YYYYMMDD" service date.
// Calendar exceptions override the weekly pattern.
func (ix *Index) ActiveOn(trip *TripInfo, serviceDate string) bool {
	if trip == nil {
		return false
	}
	day, err := time.ParseInLocation(serviceDateLayout, serviceDate, ix.tz)
	if err != nil {
		return false
	}
	svc, ok := ix.services[trip.ServiceID]
	if !ok {
		return false
	}

	key := dateKey(day)
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
	if last <= 0 || shapeLength <= 0 {
		return 1, true
	}
	ratio := last / shapeLength
	if ratio < shapeDistMetresLo || ratio > shapeDistMetresHi {
		return 1 / ratio, true
	}
	return 1, true
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
