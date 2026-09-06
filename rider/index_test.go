package rider

import (
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func TestBuildIndex_TripsAndShapes(t *testing.T) {
	ix := fixtureIndex(t)
	st := ix.Stats()
	assert.Equal(t, 3, st.Trips, "T4 has no shape and must be excluded")
	assert.Equal(t, 2, st.Shapes)
	assert.Equal(t, "fixture", st.Source)
	assert.Equal(t, fixtureLoadedAt, st.LoadedAt)
	assert.Equal(t, []string{"T1", "T2", "T3"}, ix.TripIDs())
	_, ok := ix.Trip("T4")
	assert.False(t, ok)
	assert.Equal(t, "America/Los_Angeles", ix.Timezone().String())
}

func TestBuildIndex_StopTimes_WithShapeDist(t *testing.T) {
	ix := fixtureIndex(t)
	trip, ok := ix.Trip("T1")
	require.True(t, ok)
	assert.Equal(t, "R1", trip.RouteID)
	assert.Equal(t, "WEEKDAY", trip.ServiceID)
	require.Len(t, trip.StopTimes, 3)
	assert.Equal(t, "ST2", trip.StopTimes[1].StopID)
	assert.Equal(t, 2, trip.StopTimes[1].Sequence)
	assert.InDelta(t, 500, trip.StopTimes[1].AlongShape, 1)
	assert.Equal(t, 8*time.Hour+5*time.Minute, trip.StopTimes[1].Arrival)
	assert.Equal(t, LatLon{47.6045, -122.3300}, trip.StopTimes[1].Pos)
	assert.InDelta(t, 1001, trip.StopTimes[2].AlongShape, 5)
}

func TestBuildIndex_StopTimes_ProjectedWhenNoShapeDist(t *testing.T) {
	ix := fixtureIndex(t)
	trip, ok := ix.Trip("T3")
	require.True(t, ok)
	require.Len(t, trip.StopTimes, 4)
	assert.InDelta(t, 0, trip.StopTimes[0].AlongShape, 2)
	assert.InDelta(t, trip.Shape.Cumulative[2], trip.StopTimes[1].AlongShape, 5)
	assert.InDelta(t, trip.Shape.Cumulative[3], trip.StopTimes[2].AlongShape, 5)
	assert.InDelta(t, trip.Shape.Length, trip.StopTimes[3].AlongShape, 5, "last LP1 must project to the END of the loop, not the start")
	assert.Equal(t, 25*time.Hour, trip.StopTimes[0].Arrival)
}

func TestBuildIndex_RescalesShapeDistUnits(t *testing.T) {
	// Same fixture but shape_dist_traveled in kilometres (0, 0.5, 1.001).
	ix := fixtureIndexWithShapeDistScale(t, 0.001)
	trip, _ := ix.Trip("T1")
	assert.InDelta(t, 500, trip.StopTimes[1].AlongShape, 2)
}

func TestBuildIndex_ProjectsWhenShapeDistIsAllZero(t *testing.T) {
	// Every stop time carries shape_dist_traveled, but the column never grows:
	// the values say nothing, so the stops must be projected instead of being
	// scaled to zero.
	ix := fixtureIndexEdited(t, "stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled\n"+
			stopTimeRow("T1", "08:00:00", "ST1", 1, "0.000000")+
			stopTimeRow("T1", "08:05:00", "ST2", 2, "0.000000")+
			stopTimeRow("T1", "08:10:00", "ST3", 3, "0.000000"))

	trip, ok := ix.Trip("T1")
	require.True(t, ok)
	require.Len(t, trip.StopTimes, 3)
	assert.InDelta(t, 0, trip.StopTimes[0].AlongShape, 2)
	assert.InDelta(t, 500, trip.StopTimes[1].AlongShape, 5, "projected, not scaled to zero")
	assert.InDelta(t, trip.Shape.Length, trip.StopTimes[2].AlongShape, 5)
}

func TestBuildIndex_SkipsTripsWithoutStopTimes(t *testing.T) {
	// T5 has a perfectly good shape but no stop_times rows: nothing can be
	// interpolated for it, so it is skipped alongside the shapeless T4.
	ix := fixtureIndexEdited(t, "trips.txt", "route_id,service_id,trip_id,shape_id\n"+
		"R1,WEEKDAY,T1,S1\n"+
		"R1,SAT,T2,S1\n"+
		"R2,WEEKDAY,T3,S2\n"+
		"R1,WEEKDAY,T4,\n"+
		"R1,WEEKDAY,T5,S1\n")

	_, ok := ix.Trip("T5")
	assert.False(t, ok, "a trip with no stop times is not indexed")
	assert.Equal(t, []string{"T1", "T2", "T3"}, ix.TripIDs())
	assert.Equal(t, 3, ix.Stats().Trips)
}

func TestActiveOn(t *testing.T) {
	ix := fixtureIndex(t)
	t1, _ := ix.Trip("T1")                       // WEEKDAY
	t2, _ := ix.Trip("T2")                       // SAT
	assert.True(t, ix.ActiveOn(t1, "20260902"))  // Wednesday
	assert.False(t, ix.ActiveOn(t1, "20260905")) // Saturday
	assert.True(t, ix.ActiveOn(t2, "20260905"))
	assert.False(t, ix.ActiveOn(t1, "20260907")) // Labor Day removed
	assert.True(t, ix.ActiveOn(t2, "20260907"))  // added
	assert.False(t, ix.ActiveOn(t1, "20270106")) // outside range
	assert.False(t, ix.ActiveOn(t1, "garbage"))
}

func TestServiceDate_ThreeAMBoundary(t *testing.T) {
	ix := fixtureIndex(t)
	la := ix.Timezone()
	assert.Equal(t, "20260901", ix.ServiceDate(time.Date(2026, 9, 2, 2, 59, 0, 0, la)))
	assert.Equal(t, "20260902", ix.ServiceDate(time.Date(2026, 9, 2, 3, 0, 0, 0, la)))
	// A UTC instant is converted to agency local time first: 09:30Z = 02:30 PDT.
	assert.Equal(t, "20260901", ix.ServiceDate(time.Date(2026, 9, 2, 9, 30, 0, 0, time.UTC)))
}

func TestServiceDayStart(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	start, err := ServiceDayStart("20260902", la)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 2, 0, 0, 0, 0, la), start)
	_, err = ServiceDayStart("2026-09-02", la)
	assert.Error(t, err)
}

func TestScheduledOffsetAt(t *testing.T) {
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T1") // ST1 08:00 @0, ST2 08:05 @500, ST3 08:10 @1001
	assert.Equal(t, 8*time.Hour, ScheduledOffsetAt(trip, -10))
	assert.InDelta(t, float64(8*time.Hour+150*time.Second), float64(ScheduledOffsetAt(trip, 250)), float64(3*time.Second))
	assert.Equal(t, 8*time.Hour+10*time.Minute, ScheduledOffsetAt(trip, 5000))
}

func TestBuildIndex_ErrorsWithoutTimezone(t *testing.T) {
	_, err := BuildIndex(fixtureStatic(t, "Not/AZone"), "x", time.Now())
	assert.Error(t, err)
}

// shapeDistScale recovers the unit of shape_dist_traveled from its last value.
// The case that matters is a feed already in metres whose last stop falls short
// of the end of the shape: GTFS permits that, and reading the shortfall as a
// unit conversion would move every scheduled stop.
func TestShapeDistScale_RecoversUnit(t *testing.T) {
	stopsEndingAt := func(last float64) []gtfs.ScheduledStopTime {
		return []gtfs.ScheduledStopTime{
			{ShapeDistanceTraveled: ptr(0.0)},
			{ShapeDistanceTraveled: ptr(last / 2)},
			{ShapeDistanceTraveled: ptr(last)},
		}
	}
	const shape = 10000.0 // metres

	for _, tc := range []struct {
		name  string
		last  float64
		scale float64
		ok    bool
	}{
		{"metres to the end of the shape", 9800, 1, true},
		{"metres, last stop halfway along", 5000, 1, true},
		{"metres, last stop at a twentieth", 500, 1, true},
		{"kilometres", 9.8, 1000, true},
		{"feet", 9800 / 0.3048, 0.3048, true},
		{"miles", 9800 / 1609.344, 1609.344, true},
		{"past the end of the shape in every unit", 40000, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scale, ok := shapeDistScale(stopsEndingAt(tc.last), shape)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.InDelta(t, tc.scale, scale, 1e-9)
			}
		})
	}
}

func TestShapeDistScale_RequiresEveryStopTime(t *testing.T) {
	stops := []gtfs.ScheduledStopTime{{ShapeDistanceTraveled: ptr(0.0)}, {}}
	_, ok := shapeDistScale(stops, 1000)
	assert.False(t, ok, "a missing value makes the whole column unusable")

	_, ok = shapeDistScale([]gtfs.ScheduledStopTime{{ShapeDistanceTraveled: ptr(0.0)}}, 1000)
	assert.False(t, ok, "all-zero values say nothing about the unit")
}
