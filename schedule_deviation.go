package main

import (
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// scheduleDeviation is how far from its timetable a driver vehicle was at `at`,
// when a report placed it along metres into trip's shape, on the service day
// that starts at dayStart: positive late, as GTFS-RT counts a delay. It is the
// measurement proposal #491 gives a TripUpdate's delay. Between stops, the
// vehicle's place is held against the timetable's time for that point,
// interpolated between the stops either side (rider.ScheduledOffsetAt), which
// is what the Java OneBusAway server works out with scheduleAdherenceFromLocation
// on. At a stop, the vehicle is on time from the stop's arrival to its
// departure, so a driver holding at a timepoint until it is due to leave is not
// counted late while waiting there.
//
// along is where tripLocator looks for the vehicle's stop
// (stopSearchPosition), and the stop it is at is the one tripLocator names.
// ok is false, nothing being known, for a trip in frequencies.txt, whose stop
// times are only the pattern of its runs; for a trip whose shape was drawn
// through its stops, where no place is good enough to judge; before the
// vehicle has left its first stop, the moment evaluateRuns counts as departing
// it, so a driver waiting to leave is not counted early; and once it is past
// its last stop, where tripLocator names no stop either.
func scheduleDeviation(trip *rider.TripInfo, dayStart time.Time, along float64, at time.Time) (time.Duration, bool) {
	if trip.InFrequencies() || trip.ShapeFromStops || len(trip.StopTimes) == 0 {
		return 0, false
	}
	if along <= trip.StopTimes[0].AlongShape+stoppedAtDistance {
		return 0, false
	}
	stop, ok := rider.NextStop(trip, along-stoppedAtDistance)
	if !ok {
		return 0, false
	}
	if stop.AlongShape-along <= stoppedAtDistance {
		arrival, departure := dayStart.Add(stop.Arrival), dayStart.Add(stop.Departure)
		switch {
		case at.Before(arrival):
			return at.Sub(arrival), true
		case at.After(departure):
			return at.Sub(departure), true
		}
		return 0, true
	}
	return at.Sub(dayStart.Add(rider.ScheduledOffsetAt(trip, along))), true
}

// runDayStart is when the service day of the run loc's vehicle is on began:
// the report's start_date, which the GTFS-RT feed names the run by, or when it
// gave none, the day runServiceDate matches the trip's start to, as the trip
// page and the Trips list judge the trip by. ok is false when neither is
// known, and when the trip does not run on the start_date given: there is no
// timetable to be early or late against.
func runDayStart(ix *rider.Index, trip *rider.TripInfo, loc *LocationReport) (time.Time, bool) {
	date := loc.StartDate
	if date == "" {
		// A trip start that is not known, the zero time, matches no run.
		var ok bool
		if date, ok = runServiceDate(ix, trip, loc.TripStartedAt); !ok {
			return time.Time{}, false
		}
	} else if !ix.ActiveOn(trip, date) {
		return time.Time{}, false
	}
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	return dayStart, err == nil
}
