package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// deviationServiceDate is a Wednesday, so the fixture's WEEKDAY trips run on
// it; deviationSaturday is a day they do not.
const (
	deviationServiceDate = "20260902"
	deviationSaturday    = "20260905"
)

// dwellFixtureIndex is the test feed with T7 added: T1's street, but held at
// ST2 from 12:05 to 12:07, so a vehicle standing there is on time for two
// minutes.
func dwellFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	appendLines := func(body []byte, lines ...string) []byte {
		return []byte(string(body) + strings.Join(lines, "\n") + "\n")
	}
	return fixtureIndexEdited(t, func(name string, body []byte) []byte {
		switch name {
		case "trips.txt":
			return appendLines(body, "R1,WEEKDAY,T7,S1,North,0")
		case "stop_times.txt":
			return appendLines(body,
				"T7,12:00:00,12:00:00,ST1,1,0.000000",
				"T7,12:05:00,12:07:00,ST2,2,500.000000",
				"T7,12:12:00,12:12:00,ST3,3,1001.000000",
			)
		}
		return body
	})
}

// deviationDayStart is when the fixture's deviationServiceDate starts.
func deviationDayStart(t *testing.T, ix *rider.Index) time.Time {
	t.Helper()
	dayStart, err := rider.ServiceDayStart(deviationServiceDate, ix.Timezone())
	require.NoError(t, err)
	return dayStart
}

// dur is d as the *time.Duration a deviation is held as.
func dur(d time.Duration) *time.Duration { return &d }

// clock is a time of the service day starting at dayStart, as GTFS writes it.
func clock(dayStart time.Time, hour, minute, second int) time.Time {
	return dayStart.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second)
}

func TestScheduleDeviation(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6, t4 := mustTrip(t, ix, "T6"), mustTrip(t, ix, "T4")
	require.True(t, t4.ShapeFromStops, "T4 has no shape_id, so its shape is drawn through its stops")
	t7 := mustTrip(t, dwellFixtureIndex(t), "T7")
	headway := mustTrip(t, frequencyFixtureIndex(t), "T1")
	require.True(t, headway.InFrequencies())
	untimed := untimedFixtureIndex(t)
	tb, te, tz := mustTrip(t, untimed, "TB"), mustTrip(t, untimed, "TE"), mustTrip(t, untimed, "TZ")
	day := deviationDayStart(t, ix)

	// T6 leaves ST2, 500 m along, at 12:05 and reaches ST3, 1001 m along, at
	// 12:10: halfway between them, 750.5 m, the timetable has it at 12:07:30.
	const between = 750.5
	tests := []struct {
		name  string
		trip  *rider.TripInfo
		along float64
		at    time.Time
		want  *time.Duration // nil: not known
	}{
		{"between stops, late", t6, between, clock(day, 12, 13, 30), dur(6 * time.Minute)},
		{"between stops, early", t6, between, clock(day, 12, 5, 30), dur(-2 * time.Minute)},
		{"between stops, on the timetable", t6, between, clock(day, 12, 7, 30), dur(time.Duration(0))},
		// A stop is a window from its arrival to its departure, from either
		// side of it within stoppedAtDistance.
		{"held at a stop until its departure", t7, 510, clock(day, 12, 6, 0), dur(time.Duration(0))},
		{"at a stop after its departure", t7, 490, clock(day, 12, 8, 0), dur(time.Minute)},
		{"at a stop before its arrival", t7, 510, clock(day, 12, 4, 0), dur(-time.Minute)},
		{"just left the first stop", t6, stoppedAtDistance + 1, clock(day, 12, 0, 0), dur(-time.Duration(float64(5*time.Minute) * (stoppedAtDistance + 1) / 500))},
		{"at the last stop, past its end", t6, t6.Shape.Length + stoppedAtDistance - 1, clock(day, 12, 21, 0), dur(time.Minute)},
		// Nothing is known before the vehicle leaves its first stop: a driver
		// waiting to leave is not early.
		{"not yet left the first stop", t6, stoppedAtDistance, clock(day, 11, 50, 0), nil},
		{"past the last stop", t6, t6.Shape.Length + stoppedAtDistance + 1, clock(day, 12, 21, 0), nil},
		{"a trip run by headway", headway, between, clock(day, 12, 13, 30), nil},
		{"a shape drawn through the stops", t4, 300, clock(day, 10, 5, 0), nil},
		// Further off than maxScheduleDeviation, either way, the vehicle is
		// on some other run.
		{"90 minutes late", t6, between, clock(day, 13, 37, 30), dur(90 * time.Minute)},
		{"over 90 minutes late", t6, between, clock(day, 13, 37, 31), nil},
		{"90 minutes early", t6, between, clock(day, 10, 37, 30), dur(-90 * time.Minute)},
		{"over 90 minutes early", t6, between, clock(day, 10, 37, 29), nil},
		{"at a stop, over 90 minutes late", t7, 510, clock(day, 13, 37, 1), nil},
		{"at a stop, over 90 minutes early", t7, 510, clock(day, 10, 34, 59), nil},
		// TB's ST2 is untimed: the parser puts it at 12:04:59.7, 500 m of the
		// 1001 from ST1 (12:00) to ST3 (12:10).
		{"heading to an untimed stop", tb, 250, clock(day, 12, 4, 0), dur(90*time.Second + 150*time.Millisecond)},
		{"at an untimed stop", tb, 500, clock(day, 12, 6, 0), dur(60*time.Second + 300*time.Millisecond)},
		// TE has no timed stop after ST1, so the parser leaves ST2 and ST3 at
		// 0. Measured against that, the vehicle would be 58 minutes late here
		// and 12 hours late between them.
		{"heading to a stop no time was found for", te, 40, clock(day, 12, 0, 30), nil},
		{"between stops no time was found for", te, between, clock(day, 12, 13, 30), nil},
		{"heading to a stop due the minute the trip leaves", tz, 100, clock(day, 12, 0, 30), dur(30 * time.Second)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := scheduleDeviation(tc.trip, day, tc.along, tc.at)
			if tc.want == nil {
				assert.False(t, ok, "got %v", got)
				return
			}
			require.True(t, ok)
			assert.InDelta(t, tc.want.Seconds(), got.Seconds(), 0.01)
		})
	}
}

// deviationReport is bus-1's report at along metres on T6, taken at at.
func deviationReport(t6 *rider.TripInfo, vehicleID, startDate string, along float64, at time.Time) *LocationReport {
	pos := t6.Shape.PointAt(along)
	return &LocationReport{VehicleID: vehicleID, TripID: t6.ID, StartDate: startDate,
		Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: at.Unix()}
}

// heldDeviation is what tracker holds for vehicleID as its schedule
// deviation, nil when none.
func heldDeviation(t *testing.T, tracker *Tracker, vehicleID string) *time.Duration {
	t.Helper()
	return heldVehicle(t, tracker, vehicleID).ScheduleDeviation
}

// heldVehicle is what tracker holds for vehicleID, as ActiveVehicles hands it
// out.
func heldVehicle(t *testing.T, tracker *Tracker, vehicleID string) *VehicleState {
	t.Helper()
	for _, v := range tracker.ActiveVehicles() {
		if v.VehicleID == vehicleID {
			return v
		}
	}
	require.FailNow(t, "vehicle not in the tracker", vehicleID)
	return nil
}

// TestTracker_ScheduleDeviationMovesWithEachReport: each accepted report sets
// the vehicle's deviation, and when its run is due at the stop it names, from
// where it put the vehicle, before anything asks for them, so neither the live
// answer nor the trip updates feed has to read the trail; and a report that
// says nothing clears both rather than leaving the last ones standing.
func TestTracker_ScheduleDeviationMovesWithEachReport(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)
	report := func(loc *LocationReport) *VehicleState {
		t.Helper()
		require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
		return heldVehicle(t, tracker, loc.VehicleID)
	}

	got := report(deviationReport(t6, "bus-1", deviationServiceDate, 750.5, clock(day, 12, 13, 30)))
	require.NotNil(t, got.ScheduleDeviation)
	assert.InDelta(t, (6 * time.Minute).Seconds(), got.ScheduleDeviation.Seconds(), 0.5)
	require.NotNil(t, got.ScheduledArrival)
	assert.Equal(t, clock(day, 12, 10, 0), *got.ScheduledArrival, "T6 is due at ST3 at 12:10")
	// ActiveVehicles hands out a copy: changing it changes nothing held.
	*got.ScheduleDeviation, *got.ScheduledArrival = 0, time.Time{}
	held := heldVehicle(t, tracker, "bus-1")
	assert.InDelta(t, (6 * time.Minute).Seconds(), held.ScheduleDeviation.Seconds(), 0.5)
	assert.Equal(t, clock(day, 12, 10, 0), *held.ScheduledArrival)

	// Three quarters of the way from ST2 (12:05) to ST3 (12:10) is 12:08:45.
	got = report(deviationReport(t6, "bus-1", deviationServiceDate, 875.75, clock(day, 12, 14, 15)))
	require.NotNil(t, got.ScheduleDeviation)
	assert.InDelta(t, (5*time.Minute + 30*time.Second).Seconds(), got.ScheduleDeviation.Seconds(), 0.5)

	offRoute := deviationReport(t6, "bus-1", deviationServiceDate, 1300, clock(day, 12, 17, 0))
	pos := rider.OffsetMetres(t6.Shape.PointAt(1300), 0, 500)
	offRoute.Latitude, offRoute.Longitude = pos.Lat, pos.Lon
	got = report(offRoute)
	assert.Nil(t, got.ScheduleDeviation, "a vehicle off its route is not early or late on it")
	assert.Nil(t, got.ScheduledArrival)
}

func TestTracker_ScheduleDeviationRun(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	at := clock(day, 12, 13, 30)

	tests := []struct {
		name string
		loc  func() *LocationReport
		want *time.Duration
	}{
		{"the report's start_date", func() *LocationReport {
			return deviationReport(t6, "bus-1", deviationServiceDate, 750.5, at)
		}, dur(6 * time.Minute)},
		{"no start_date: the run the trip's start is matched to", func() *LocationReport {
			loc := deviationReport(t6, "bus-1", "", 750.5, at)
			loc.TripStartedAt = clock(day, 12, 1, 0)
			return loc
		}, dur(6 * time.Minute)},
		{"no start_date and no known start", func() *LocationReport {
			return deviationReport(t6, "bus-1", "", 750.5, at)
		}, nil},
		{"a start_date the trip does not run on", func() *LocationReport {
			return deviationReport(t6, "bus-1", deviationSaturday, 750.5, at)
		}, nil},
		{"past the last stop", func() *LocationReport {
			// 50 m on beyond T6's end, the way its last stretch heads: the
			// locator puts it past the end, not clamped onto the last stop.
			loc := deviationReport(t6, "bus-1", deviationServiceDate, t6.Shape.Length, at)
			pos := rider.OffsetMetres(t6.Shape.PointAt(t6.Shape.Length), -50, 0)
			loc.Latitude, loc.Longitude = pos.Lat, pos.Lon
			return loc
		}, nil},
		{"no trip", func() *LocationReport {
			loc := deviationReport(t6, "bus-1", "", 750.5, at)
			loc.TripID = ""
			return loc
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newPlacingTracker(t, ix)
			require.True(t, tracker.UpdateAuthorized(tc.loc(), time.Now()))
			got := heldDeviation(t, tracker, "bus-1")
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.InDelta(t, tc.want.Seconds(), got.Seconds(), 0.5)
		})
	}

	t.Run("restored at startup", func(t *testing.T) {
		tracker := newPlacingTracker(t, ix)
		tracker.Update(deviationReport(t6, "bus-1", deviationServiceDate, 750.5, at))
		assert.Nil(t, heldDeviation(t, tracker, "bus-1"), "a vehicle restored at startup is placed by its next report")
	})
}

// TestHandleLiveVehicles_ScheduleDeviation: the live answer carries each
// vehicle's deviation in whole seconds, late positive, and null, not 0, when
// it is not known.
func TestHandleLiveVehicles_ScheduleDeviation(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	day := deviationDayStart(t, ix)
	tracker := newPlacingTracker(t, ix)
	for _, loc := range []*LocationReport{
		deviationReport(t6, "late", deviationServiceDate, 750.5, clock(day, 12, 13, 30)),
		deviationReport(t6, "early", deviationServiceDate, 750.5, clock(day, 12, 5, 30)),
		deviationReport(t6, "on-time", deviationServiceDate, 750.5, clock(day, 12, 7, 30)),
		deviationReport(t6, "at-first-stop", deviationServiceDate, 0, clock(day, 12, 2, 0)),
	} {
		require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
	}

	entries := liveEntries(t, tracker)
	for vehicle, want := range map[string]string{"late": "360", "early": "-120", "on-time": "0", "at-first-stop": "null"} {
		assert.Equal(t, want, string(entries[vehicle]["schedule_deviation_s"]), vehicle)
	}
}
