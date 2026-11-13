package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// locatorFixtureIndex is the test feed with two trips added: T1S, a short turn
// on T1's shape that ends at ST2 halfway along it, and T5, a second run of
// T3's loop.
func locatorFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	appendLines := func(body []byte, lines ...string) []byte {
		return []byte(string(body) + strings.Join(lines, "\n") + "\n")
	}
	return fixtureIndexEdited(t, func(name string, body []byte) []byte {
		switch name {
		case "trips.txt":
			return appendLines(body, "R1,WEEKDAY,T1S,S1,North,0", "R2,WEEKDAY,T5,S2,Loop,1")
		case "stop_times.txt":
			return appendLines(body,
				"T1S,11:00:00,11:00:00,ST1,1,0.000000",
				"T1S,11:05:00,11:05:00,ST2,2,500.000000",
				"T5,26:00:00,26:00:00,LP1,1,",
				"T5,26:10:00,26:10:00,LP2,2,",
				"T5,26:15:00,26:15:00,LP3,3,",
				"T5,26:20:00,26:20:00,LP1,4,",
			)
		}
		return body
	})
}

func mustTrip(t *testing.T, ix *rider.Index, id string) *rider.TripInfo {
	t.Helper()
	trip, ok := ix.Trip(id)
	require.True(t, ok, "fixture trip %s", id)
	return trip
}

// loopClosingCorner is a fix at the terminal of T3's loop, 4 m north and 1 m
// east of it: nearer the opening pass (1 m) than the closing one (4 m), as
// GPS puts a bus pulling in at the end of the loop about half the time.
func loopClosingCorner(t3 *rider.TripInfo) rider.LatLon {
	return rider.OffsetMetres(t3.Shape.Points[0], 4, 1)
}

func TestTripLocator_Locate(t *testing.T) {
	ix := locatorFixtureIndex(t)
	locator := newTripLocator(func() *rider.Index { return ix }, rider.DefaultThresholds().MaxShapeDistance)

	t1, t1s, t3, t4 := mustTrip(t, ix, "T1"), mustTrip(t, ix, "T1S"), mustTrip(t, ix, "T3"), mustTrip(t, ix, "T4")
	require.True(t, t4.ShapeFromStops, "T4 has no shape_id, so its shape is drawn through its stops")
	st2 := t1.StopTimes[1]
	require.Equal(t, "ST2", st2.StopID)
	loopEnd := t3.Shape.Length

	report := func(tripID string, pos rider.LatLon, accuracy *float64) *LocationReport {
		return &LocationReport{VehicleID: "bus-1", TripID: tripID, Latitude: pos.Lat, Longitude: pos.Lon, Accuracy: accuracy}
	}
	east := func(trip *rider.TripInfo, along, metres float64) rider.LatLon {
		return rider.OffsetMetres(trip.Shape.PointAt(along), 0, metres)
	}

	type want struct {
		stopID    string
		sequence  int
		stoppedAt bool
		along     float64
	}
	cases := []struct {
		name   string
		report *LocationReport
		hint   float64
		want   want
	}{
		{"mid-trip names the next stop", report("T1", t1.Shape.PointAt(250), nil), 0,
			want{"ST2", 2, false, 250}},
		{"at the first stop", report("T1", t1.Shape.PointAt(0), nil), 0,
			want{"ST1", 1, true, 0}},
		{"just inside the radius before a stop", report("T1", t1.Shape.PointAt(st2.AlongShape-stoppedAtDistance+1), nil), 0,
			want{"ST2", 2, true, st2.AlongShape - stoppedAtDistance + 1}},
		{"just outside the radius before a stop", report("T1", t1.Shape.PointAt(st2.AlongShape-stoppedAtDistance-1), nil), 0,
			want{"ST2", 2, false, st2.AlongShape - stoppedAtDistance - 1}},
		{"just inside the radius past a stop", report("T1", t1.Shape.PointAt(st2.AlongShape+stoppedAtDistance-1), nil), 0,
			want{"ST2", 2, true, st2.AlongShape + stoppedAtDistance - 1}},
		{"just outside the radius past a stop", report("T1", t1.Shape.PointAt(st2.AlongShape+stoppedAtDistance+1), nil), 0,
			want{"ST3", 3, false, st2.AlongShape + stoppedAtDistance + 1}},
		{"just inside the radius past the last stop", report("T1S", t1s.Shape.PointAt(st2.AlongShape+stoppedAtDistance-1), nil), 0,
			want{"ST2", 2, true, st2.AlongShape + stoppedAtDistance - 1}},
		// Still on the route, so the vehicle is placed and its place is the
		// next hint, but there is no stop left to name.
		{"past the last stop", report("T1S", t1s.Shape.PointAt(st2.AlongShape+stoppedAtDistance+1), nil), 0,
			want{"", 0, false, st2.AlongShape + stoppedAtDistance + 1}},
		{"well past the last stop", report("T1S", t1s.Shape.PointAt(800), nil), 0,
			want{"", 0, false, 800}},
		{"inside the off-route distance", report("T1", east(t1, 250, 59), nil), 0,
			want{"ST2", 2, false, 250}},
		{"off route keeps the hint", report("T1", east(t1, 250, 61), nil), 120,
			want{"", 0, false, 120}},
		{"the fix's accuracy widens the off-route distance", report("T1", east(t1, 250, 61), float64ptr(5)), 0,
			want{"ST2", 2, false, 250}},
		{"a negative accuracy does not narrow it", report("T1", east(t1, 250, 59), float64ptr(-50)), 0,
			want{"ST2", 2, false, 250}},
		{"a trip not in the schedule", report("NOPE", t1.Shape.PointAt(250), nil), 120,
			want{"", 0, false, 120}},
		{"a report with only a route", report("", t1.Shape.PointAt(250), nil), 120,
			want{"", 0, false, 120}},
		// Right on the line drawn through T4's stops, and still not placed.
		{"a shape drawn through the stops", report("T4", t4.Shape.PointAt(250), nil), 120,
			want{"", 0, false, 120}},
		{"a loop's closing pass, from a hint near its end", report("T3", loopClosingCorner(t3), nil), loopEnd - 30,
			want{"LP1", 4, true, loopEnd - 1}},
		{"a loop's opening pass, on a trip just begun", report("T3", loopClosingCorner(t3), nil), 0,
			want{"LP1", 1, true, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := locator.locate(tc.report, tc.hint)
			assert.Equal(t, tc.want.stopID, got.stopID)
			assert.Equal(t, tc.want.sequence, got.stopSequence)
			assert.Equal(t, tc.want.stoppedAt, got.stoppedAt)
			assert.InDelta(t, tc.want.along, got.alongShape, 0.5)
		})
	}
}

func TestTripLocator_NoScheduleLoaded(t *testing.T) {
	ix := loadFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	locator := newTripLocator(func() *rider.Index { return nil }, rider.DefaultThresholds().MaxShapeDistance)

	pos := t1.Shape.PointAt(250)
	got := locator.locate(&LocationReport{VehicleID: "bus-1", TripID: "T1", Latitude: pos.Lat, Longitude: pos.Lon}, 120)
	assert.Equal(t, tripPlace{alongShape: 120}, got, "no stop, and the hint carries forward")
}

func TestTripLocator_OffRouteDistanceIsConfigured(t *testing.T) {
	ix := loadFixtureIndex(t)
	t1 := mustTrip(t, ix, "T1")
	pos := rider.OffsetMetres(t1.Shape.PointAt(250), 0, 90)
	loc := &LocationReport{VehicleID: "bus-1", TripID: "T1", Latitude: pos.Lat, Longitude: pos.Lon}

	assert.Empty(t, newTripLocator(func() *rider.Index { return ix }, 60).locate(loc, 0).stopID)
	assert.Equal(t, "ST2", newTripLocator(func() *rider.Index { return ix }, 100).locate(loc, 0).stopID,
		"RIDER_MAX_SHAPE_DISTANCE moves the line for drivers too")
}
