package main

import (
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// maxScheduleDeviation is the furthest from its timetable, late or early, a
// driver vehicle is taken to be on the run it is measured against. Further off
// than that it is on another run: most often the phone sent a start_date from
// an earlier day, which the trip also runs on, and the deviation comes out in
// days. It is the 90 minutes the rest of the server lets a run be late
// (RIDER_SCHEDULE_LATE's default, rider's runningLateGrace, passingLateGrace),
// held on the early side too, where a start_date from a later day lands.
const maxScheduleDeviation = 90 * time.Minute

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
// it, so a driver waiting to leave is not counted early; once it is past its
// last stop, where tripLocator names no stop either; when the stop it is at or
// heading to has no time, not even one filled in between timed stops; and when
// it is further than maxScheduleDeviation from the timetable.
func scheduleDeviation(trip *rider.TripInfo, dayStart time.Time, along float64, at time.Time) (time.Duration, bool) {
	if trip.InFrequencies() || trip.ShapeFromStops || len(trip.StopTimes) == 0 {
		return 0, false
	}
	if along <= trip.StopTimes[0].AlongShape+stoppedAtDistance {
		return 0, false
	}
	stop, ok := rider.NextStop(trip, along-stoppedAtDistance)
	// The GTFS parser fills a stop's blank times in between the timed stops
	// either side. With no timed stop after it, where stop_times.txt leaves
	// even the last stop blank, it leaves them at 0, before the trip's first
	// departure.
	if !ok || stop.Arrival < trip.StopTimes[0].Departure {
		return 0, false
	}
	var deviation time.Duration
	if stop.AlongShape-along <= stoppedAtDistance {
		arrival, departure := dayStart.Add(stop.Arrival), dayStart.Add(stop.Departure)
		switch {
		case at.Before(arrival):
			deviation = at.Sub(arrival)
		case at.After(departure):
			deviation = at.Sub(departure)
		}
	} else {
		deviation = at.Sub(dayStart.Add(rider.ScheduledOffsetAt(trip, along)))
	}
	if deviation.Abs() > maxScheduleDeviation {
		return 0, false
	}
	return deviation, true
}

// runDayStart is when the service day of the run loc's vehicle is on began:
// the report's start_date, which the GTFS-RT feed names the run by, or when it
// gave none, the day runServiceDate matches the trip's start to, as the trip
// page and the Trips list judge the trip by. ok is false when neither is
// known, and when the trip does not run on the start_date given: there is no
// timetable to be early or late against.
func runDayStart(ix *rider.Index, trip *rider.TripInfo, loc *LocationReport) (time.Time, bool) {
	date, ok := runDate(ix, trip, loc.StartDate, loc.TripStartedAt)
	if !ok {
		return time.Time{}, false
	}
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	return dayStart, err == nil
}

// runDate is the service date of a run of trip that started at startedAt:
// startDate, the start_date its reports name it by, or when they gave none,
// the day runServiceDate matches the start to. ok is false when neither is
// known, and when the trip does not run on startDate.
func runDate(ix *rider.Index, trip *rider.TripInfo, startDate string, startedAt time.Time) (string, bool) {
	if startDate == "" {
		// A trip start that is not known, the zero time, matches no run.
		return runServiceDate(ix, trip, startedAt)
	}
	return startDate, ix.ActiveOn(trip, startDate)
}
