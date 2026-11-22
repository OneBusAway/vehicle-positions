package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// liveVehicle is a vehicle on an active trip as the bunched alert sees it:
// placed on its trip's shape, past its first stop and not past its last.
type liveVehicle struct {
	trip  TripSummary
	state *VehicleState
	info  *rider.TripInfo
	// along is how far along the shape the vehicle was last placed, and next
	// the index in info.StopTimes of the stop it is at or heading to.
	along float64
	next  int
	keys  []stopKey
	// date and dayStart are the service day of the vehicle's run.
	date     string
	dayStart time.Time
}

// liveGroup is the vehicles whose places can be compared: one route, one
// direction and one shape, since a place is metres along a shape.
type liveGroup struct {
	routeID   string
	direction directionKey
	shape     *rider.ShapeGeom
}

// vehicleSpacing is a vehicle found running bunched behind the vehicle ahead
// of it: its trip, the vehicle ahead, how far behind it, and the scheduled
// gap at the stop the vehicle is at or heading to.
type vehicleSpacing struct {
	tripID    int64
	ahead     TripSummary
	spacing   time.Duration
	scheduled time.Duration
	// at is when the spacing was measured: the later of the two vehicles'
	// latest reports, on the server's clock as the other alerts count.
	at time.Time
}

// placeLive is trip's vehicle as the bunched alert sees it at now, from v, its
// latest report as the tracker keeps it. ok is false, the vehicle left out,
// when v is on another GTFS trip; it has gone quiet, so its last place says
// nothing about now; its latest report named no stop of the trip, being off
// its route, on a shape drawn through its stops or past its last stop, or
// the vehicle was seeded at startup and has not reported since; it has not
// left its first stop, where a vehicle waiting to leave is not bunched; and
// when its run has no service day the schedule runs the trip on.
func placeLive(ix *rider.Index, trip TripSummary, v *VehicleState, now time.Time, quietAfter time.Duration) (liveVehicle, bool) {
	if v.TripID != trip.GtfsTripID || now.Sub(v.UpdatedAt) > quietAfter {
		return liveVehicle{}, false
	}
	info, ok := ix.Trip(v.TripID)
	if !ok {
		return liveVehicle{}, false
	}
	next := slices.IndexFunc(info.StopTimes, func(s rider.StopTimeInfo) bool {
		return s.StopID == v.StopID && s.Sequence == v.StopSequence
	})
	if next <= 0 {
		return liveVehicle{}, false
	}
	// tripLocator names a stop only for a report it placed, so the place is
	// set.
	date, ok := runDate(ix, info, v.StartDate, trip.StartTime)
	if !ok {
		return liveVehicle{}, false
	}
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	if err != nil {
		return liveVehicle{}, false
	}
	return liveVehicle{
		trip: trip, state: v, info: info,
		along: *v.alongShape, next: next, keys: stopKeys(info.StopTimes),
		date: date, dayStart: dayStart,
	}, true
}

// inOrder reports whether v's run is in the order of vehicles at stop, as
// directionStops puts runs in it for Analytics: its trip serves the stop
// other than as its last, and is in frequencies.txt or due to leave the stop
// as part of the timetable's frequent service there.
func inOrder(v liveVehicle, stop stopKey, s *stopSchedule) bool {
	i := slices.Index(v.keys[:len(v.keys)-1], stop)
	return i >= 0 && (v.info.InFrequencies() || s.timetabledAt(v.dayStart.Add(v.info.StopTimes[i].Departure)))
}

// spacingBehind is how far behind ahead, the vehicle in front of it on the
// same shape, v is running: the time v's timetable gives from v's place to
// ahead's, plus how much later v reported than ahead, on the reports' own
// times as Analytics times a gap. ok is false when v's timetable cannot say:
// ahead is past v's last stop, beyond what v's stop times cover, or the
// timetable gives no running time between the two places, which says nothing
// rather than calling them bunched.
func spacingBehind(v, ahead liveVehicle) (time.Duration, bool) {
	stops := v.info.StopTimes
	if ahead.along > stops[len(stops)-1].AlongShape+stoppedAtDistance {
		return 0, false
	}
	running := rider.ScheduledOffsetAt(v.info, ahead.along) - rider.ScheduledOffsetAt(v.info, v.along)
	if running <= 0 {
		return 0, false
	}
	return running + time.Unix(v.state.Timestamp, 0).Sub(time.Unix(ahead.state.Timestamp, 0)), true
}

// stopSchedules keeps the frequent service at the stops the bunched alert has
// looked at, for one schedule, since laying a stop out is a pass over its
// route's trips. Only the service dates of the latest look are kept.
type stopSchedules struct {
	ix     *rider.Index
	byStop map[stopScheduleKey]*stopSchedule
}

type stopScheduleKey struct {
	routeID   string
	direction directionKey
	stop      stopKey
	date      string
}

// at is the frequent service at stop of the route's trips in direction on
// date, whose service day starts at dayStart, laid out as Analytics lays out
// the stops it measures (scheduleStops).
func (c *stopSchedules) at(ix *rider.Index, group liveGroup, stop stopKey, date string, dayStart time.Time) *stopSchedule {
	if c.ix != ix {
		c.ix, c.byStop = ix, make(map[stopScheduleKey]*stopSchedule)
	}
	key := stopScheduleKey{group.routeID, group.direction, stop, date}
	if s, ok := c.byStop[key]; ok {
		return s
	}
	var trips []*rider.TripInfo
	var keys [][]stopKey
	for _, trip := range ix.TripsOnRoute(group.routeID, date) {
		if directionOf(trip) == group.direction {
			trips = append(trips, trip)
			keys = append(keys, stopKeys(trip.StopTimes))
		}
	}
	s := &stopSchedule{}
	scheduleStops(map[stopKey]*stopSchedule{stop: s}, trips, keys, dayStart)
	c.byStop[key] = s
	return s
}

// keepDates drops the stops laid out for any service date not in dates.
func (c *stopSchedules) keepDates(dates map[string]struct{}) {
	for key := range c.byStop {
		if _, ok := dates[key.date]; !ok {
			delete(c.byStop, key)
		}
	}
}

// bunched is each vehicle on active found running bunched behind the vehicle
// ahead of it at now, by vehicle_id, from tracked, the tracker's vehicles,
// and the schedule ix.
//
// The vehicles placeLive keeps are grouped by route, direction and shape,
// and each group put in order of place along the shape. A vehicle is judged
// at the stop it is at or heading to, other than its trip's last, and only
// where Analytics would put its run in that stop's order: a trip in
// frequencies.txt, or a timetabled trip due there as part of a timetable
// every frequentSpacing or more often. Its spacing is taken to the nearest
// vehicle ahead of it whose run is also in that order (spacingBehind), and it
// is bunched when that is under bunchedBelow of the stop's scheduled gap at
// its latest report, the gap Analytics judges by. Vehicles ahead whose runs
// are not in the order, such as one that does not stop there, are passed
// over, as Analytics passes over their passings.
func (c *stopSchedules) bunched(ix *rider.Index, active []TripSummary, tracked []*VehicleState, now time.Time, quietAfter time.Duration) map[string]vehicleSpacing {
	found := make(map[string]vehicleSpacing)
	if ix == nil {
		return found
	}
	byVehicle := make(map[string]*VehicleState, len(tracked))
	for _, v := range tracked {
		byVehicle[v.VehicleID] = v
	}
	groups := make(map[liveGroup][]liveVehicle)
	dates := make(map[string]struct{})
	for _, trip := range active {
		state, ok := byVehicle[trip.VehicleID]
		if !ok {
			continue
		}
		v, ok := placeLive(ix, trip, state, now, quietAfter)
		if !ok {
			continue
		}
		group := liveGroup{v.info.RouteID, directionOf(v.info), v.info.Shape}
		groups[group] = append(groups[group], v)
		dates[v.date] = struct{}{}
	}
	for group, vehicles := range groups {
		// Furthest along first; the vehicle_id breaks a tie, so the order
		// does not depend on the order the trips were listed in.
		slices.SortFunc(vehicles, func(a, b liveVehicle) int {
			return cmp.Or(cmp.Compare(b.along, a.along), cmp.Compare(a.trip.VehicleID, b.trip.VehicleID))
		})
		for k, v := range vehicles {
			// inOrder leaves out a vehicle heading to its trip's last stop.
			stop := v.keys[v.next]
			s := c.at(ix, group, stop, v.date, v.dayStart)
			if !inOrder(v, stop, s) {
				continue
			}
			scheduled := s.gapAt(time.Unix(v.state.Timestamp, 0))
			if scheduled == 0 {
				continue
			}
			for j := k - 1; j >= 0; j-- {
				ahead := vehicles[j]
				if !inOrder(ahead, stop, s) {
					continue
				}
				spacing, ok := spacingBehind(v, ahead)
				if ok && isBunched(int64(spacing/time.Second), int64(scheduled/time.Second)) {
					found[v.trip.VehicleID] = vehicleSpacing{
						tripID:    v.trip.ID,
						ahead:     ahead.trip,
						spacing:   spacing,
						scheduled: scheduled,
						at:        later(v.state.UpdatedAt, ahead.state.UpdatedAt),
					}
				}
				break
			}
		}
	}
	c.keepDates(dates)
	return found
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
