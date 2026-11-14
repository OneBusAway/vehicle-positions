package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// reportDay is the service day the report fixture runs: a Wednesday in the
// fixture's calendar. reportDayStart is its start in the agency's timezone,
// America/Los_Angeles.
var (
	reportDay      = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	reportDayStart = time.Date(2026, 9, 2, 7, 0, 0, 0, time.UTC)
)

// reportFixtureIndex is the test feed with the trips a day's report needs.
// Route 1 runs straight north along S1 through ST1, ST2 and ST3, at 0, 500
// and 1001 m; its weekday trips are T1 at 08:00 and T4 at 10:00 from the
// feed, plus RE at 09:00, RL at 11:00, RN at 12:00, RG at 13:00 and RM at
// 24:10, after midnight. Route 2 is the feed's loop T3 at 25:00. Route 3 is
// OB at 14:00, out and back along S3, whose two legs run 15 m apart: OB2 on
// the way out and OB4 on the way back stand across the road from each other,
// as OB1 and OB5 do. OB is the one trip whose first and last stops have
// arrival and departure times apart, so it tells the two apart.
func reportFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	appendLines := func(body []byte, lines ...string) []byte {
		return []byte(string(body) + strings.Join(lines, "\n") + "\n")
	}
	// onS1 is a trip along S1 through ST1, ST2 and ST3 at these times.
	onS1 := func(trip, st1, st2, st3 string) []string {
		return []string{
			trip + "," + st1 + "," + st1 + ",ST1,1,0.000000",
			trip + "," + st2 + "," + st2 + ",ST2,2,500.000000",
			trip + "," + st3 + "," + st3 + ",ST3,3,1001.000000",
		}
	}
	return fixtureIndexEdited(t, func(name string, body []byte) []byte {
		switch name {
		case "routes.txt":
			return appendLines(body, "R3,A,3,Out and back,3,3,,")
		case "shapes.txt":
			return appendLines(body,
				"S3,47.620000,-122.330000,1,",
				"S3,47.629000,-122.330000,2,",
				"S3,47.629000,-122.329800,3,",
				"S3,47.620000,-122.329800,4,",
			)
		case "stops.txt":
			return appendLines(body,
				"OB1,Stop OB1,47.620000,-122.330000",
				"OB2,Stop OB2,47.624500,-122.330000",
				"OB3,Stop OB3,47.629000,-122.330000",
				"OB4,Stop OB4,47.624500,-122.329800",
				"OB5,Stop OB5,47.620000,-122.329800",
			)
		case "trips.txt":
			return appendLines(body,
				"R1,WEEKDAY,RE,S1,North,0",
				"R1,WEEKDAY,RL,S1,North,0",
				"R1,WEEKDAY,RN,S1,North,0",
				"R1,WEEKDAY,RG,S1,North,0",
				"R1,WEEKDAY,RM,S1,North,0",
				"R3,WEEKDAY,OB,S3,Out and back,0",
			)
		case "stop_times.txt":
			lines := []string{
				"OB,13:58:00,14:00:00,OB1,1,",
				"OB,14:05:00,14:05:00,OB2,2,",
				"OB,14:10:00,14:10:00,OB3,3,",
				"OB,14:15:00,14:15:00,OB4,4,",
				"OB,14:20:00,14:21:00,OB5,5,",
			}
			lines = append(lines, onS1("RE", "09:00:00", "09:05:00", "09:10:00")...)
			lines = append(lines, onS1("RL", "11:00:00", "11:05:00", "11:10:00")...)
			lines = append(lines, onS1("RN", "12:00:00", "12:05:00", "12:10:00")...)
			lines = append(lines, onS1("RG", "13:00:00", "13:05:00", "13:10:00")...)
			lines = append(lines, onS1("RM", "24:10:00", "24:15:00", "24:20:00")...)
			return appendLines(body, lines...)
		}
		return body
	})
}

// hms is a time of day on the service day's clock, which runs past 24:00.
func hms(h, m, s int) time.Duration {
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

// reportLeg is a waypoint of a test vehicle: along metres along its trip's
// shape at a time on the service day's clock.
type reportLeg struct {
	at    time.Duration
	along float64
}

// driveTrip is a vehicle's fixes every 10 s from the first waypoint to the
// last, moving at a steady speed between each pair. nudge, when set, moves
// each fix off the shape by north and east metres.
func driveTrip(trip *rider.TripInfo, dayStart time.Time, legs []reportLeg, nudge func(along float64) (north, east float64)) []LocationPoint {
	var points []LocationPoint
	for at := legs[0].at; at <= legs[len(legs)-1].at; at += 10 * time.Second {
		along := legs[len(legs)-1].along
		for i := 1; i < len(legs); i++ {
			if at <= legs[i].at {
				a, b := legs[i-1], legs[i]
				along = a.along + (b.along-a.along)*float64(at-a.at)/float64(b.at-a.at)
				break
			}
		}
		pos := trip.Shape.PointAt(along)
		if nudge != nil {
			north, east := nudge(along)
			pos = rider.OffsetMetres(pos, north, east)
		}
		points = append(points, LocationPoint{Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: dayStart.Add(at).Unix()})
	}
	return points
}

// withoutFixes drops the fixes strictly between from and to, a phone that lost
// signal.
func withoutFixes(points []LocationPoint, dayStart time.Time, from, to time.Duration) []LocationPoint {
	kept := make([]LocationPoint, 0, len(points))
	for _, p := range points {
		at := time.Unix(p.Timestamp, 0).Sub(dayStart)
		if at > from && at < to {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// fixtureRun is one driver trip of the fixture day: the scheduled trip it ran
// and the vehicle's waypoints.
type fixtureRun struct {
	tripID string
	legs   func(trip *rider.TripInfo) []reportLeg
	gap    [2]time.Duration
	nudge  func(along float64) (north, east float64)
}

// straightLegs waits at ST1 until leave, drives to ST2 in 100 s, waits there
// until leaveST2, and drives to the end of S1 in 100 s. Moving at 5 m/s, it
// is past the 30 m around a stop 6 s after leaving it and inside the 30 m
// before ST3 6 s before reaching it.
func straightLegs(arrive, leave, leaveST2 time.Duration) func(trip *rider.TripInfo) []reportLeg {
	return func(trip *rider.TripInfo) []reportLeg {
		return []reportLeg{
			{arrive, 0}, {leave, 0},
			{leave + 100*time.Second, 500}, {leaveST2, 500},
			{leaveST2 + 100*time.Second, trip.Shape.Length},
		}
	}
}

// stopLegs waits at each stop of trip in turn, from when it arrives at the
// first until leaves[i], and then drives to the next stop in 100 s.
func stopLegs(arrive time.Duration, leaves ...time.Duration) func(trip *rider.TripInfo) []reportLeg {
	return func(trip *rider.TripInfo) []reportLeg {
		legs := []reportLeg{{arrive, trip.StopTimes[0].AlongShape}}
		for i, leave := range leaves {
			legs = append(legs,
				reportLeg{leave, trip.StopTimes[i].AlongShape},
				reportLeg{leave + 100*time.Second, trip.StopTimes[i+1].AlongShape})
		}
		return legs
	}
}

// reportFixtureRuns is the fixture day's driving, keyed by GTFS trip id. Each
// is commented with what the report should make of it.
var reportFixtureRuns = []fixtureRun{
	// Waits at ST1 from 07:57 and leaves on time: +0:06, +3:16, -0:16.
	{tripID: "T1", legs: straightLegs(hms(7, 57, 0), hms(8, 0, 0), hms(8, 8, 10))},
	// Leaves three minutes early and never waits: -2:54, -6:14, -9:46.
	{tripID: "RE", legs: straightLegs(hms(8, 55, 0), hms(8, 57, 0), hms(8, 58, 40))},
	// Leaves seven minutes late and makes some up: +7:06, +7:06, +3:34.
	{tripID: "RL", legs: straightLegs(hms(11, 0, 0), hms(11, 7, 0), hms(11, 12, 0))},
	// Drives as T1 does, but the phone is silent from 13:06:50 to 13:09:20, so
	// ST2 is unknown: +0:06, unknown, -0:16.
	{tripID: "RG", legs: straightLegs(hms(12, 57, 0), hms(13, 0, 0), hms(13, 8, 10)), gap: [2]time.Duration{hms(13, 6, 50), hms(13, 9, 20)}},
	// After midnight, on the service day before the calendar day: +0:06,
	// +3:16, -0:16.
	{tripID: "RM", legs: straightLegs(hms(24, 7, 0), hms(24, 10, 0), hms(24, 18, 10))},
	// The loop, its first and last stop the same. The first leg is twice as
	// long as the others, so the vehicle is past LP1's 30 m in 3 s: +0:03,
	// +0:06, +3:26, -0:06.
	{tripID: "T3", legs: stopLegs(hms(24, 58, 0), hms(25, 0, 0), hms(25, 10, 0), hms(25, 18, 20))},
	// Out and back: +0:06, +0:06, +0:06, +3:26, -0:06. Every fix on the way
	// back is 10 m off towards the outbound leg, nearer that leg than its own.
	{tripID: "OB", legs: stopLegs(hms(13, 58, 0), hms(14, 0, 0), hms(14, 5, 0), hms(14, 10, 0), hms(14, 18, 20)),
		nudge: func(along float64) (float64, float64) {
			if along > 1016 {
				return 0, -10
			}
			return 0, 0
		}},
}

// reportFixtureTrips is the fixture day as the store holds it: a driver trip
// for each fixture run, numbered from 1 in order, started at its first
// waypoint, and each run's points. RN is not run. Three more trips are not
// the day's: T1 run the next morning, a trip of a GTFS trip the schedule no
// longer has, and a trip of T2, which runs only on Saturdays.
func reportFixtureTrips(t *testing.T, ix *rider.Index) ([]TripSummary, map[int64][]LocationPoint) {
	t.Helper()
	trips := make([]TripSummary, 0, len(reportFixtureRuns)+3)
	trails := make(map[int64][]LocationPoint)
	for i, run := range reportFixtureRuns {
		trip := mustTrip(t, ix, run.tripID)
		legs := run.legs(trip)
		points := driveTrip(trip, reportDayStart, legs, run.nudge)
		if run.gap != [2]time.Duration{} {
			points = withoutFixes(points, reportDayStart, run.gap[0], run.gap[1])
		}
		id := int64(i + 1)
		trips = append(trips, TripSummary{
			ID: id, VehicleID: "bus-" + run.tripID, VehicleLabel: "Bus " + run.tripID,
			DriverName: "Driver " + run.tripID, RouteID: trip.RouteID, GtfsTripID: run.tripID,
			StartTime: reportDayStart.Add(legs[0].at), Status: "completed",
		})
		trails[id] = points
	}
	next := int64(len(trips))
	for _, other := range []struct {
		tripID string
		start  time.Duration
	}{
		{"T1", 24*time.Hour + hms(7, 58, 0)},
		{"GONE", hms(9, 0, 0)},
		{"T2", hms(9, 0, 0)},
	} {
		next++
		trips = append(trips, TripSummary{
			ID: next, VehicleID: "bus-x", VehicleLabel: "Bus X", DriverName: "Driver X",
			GtfsTripID: other.tripID, StartTime: reportDayStart.Add(other.start), Status: "completed",
		})
	}
	return trips, trails
}

// fakeReportStore serves the trips and trails a report reads from memory,
// counting how often the trips are read.
type fakeReportStore struct {
	trips  []TripSummary
	trails map[int64][]LocationPoint
	err    error
	reads  int
}

func (f *fakeReportStore) ListTripsStartedBetween(_ context.Context, from, to time.Time) ([]TripSummary, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]TripSummary, 0)
	for _, tr := range f.trips {
		if !tr.StartTime.Before(from) && tr.StartTime.Before(to) {
			out = append(out, tr)
		}
	}
	return out, nil
}

func (f *fakeReportStore) ListTripLocations(_ context.Context, tripID int64) ([]LocationPoint, error) {
	return f.trails[tripID], nil
}

var testReportSettings = reportSettings{
	onTimeEarly:      defaultReportOnTimeEarly,
	onTimeLate:       defaultReportOnTimeLate,
	maxShapeDistance: rider.DefaultThresholds().MaxShapeDistance,
}

// newFixtureReporter is a reporter over the fixture day, its clock at now.
func newFixtureReporter(t *testing.T, now time.Time) (*serviceReporter, *fakeReportStore, *rider.Index) {
	t.Helper()
	ix := reportFixtureIndex(t)
	trips, trails := reportFixtureTrips(t, ix)
	store := &fakeReportStore{trips: trips, trails: trails}
	r := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	r.now = func() time.Time { return now }
	return r, store, ix
}
