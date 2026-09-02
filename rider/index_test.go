package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
