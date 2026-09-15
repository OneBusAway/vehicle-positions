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

func TestBuildIndex_Routes(t *testing.T) {
	ix := fixtureIndex(t)
	routes := ix.Routes()
	require.Len(t, routes, 2)
	assert.Equal(t, "R2", routes[0].ID, "route_sort_order 1 lists before 2")
	assert.Equal(t, RouteInfo{ID: "R1", ShortName: "1", LongName: "Straight", Color: "0077C0", TextColor: "FFFFFF", Type: 3, SortOrder: ptr(int32(2))}, routes[1])
	assert.Equal(t, 2, ix.Stats().Routes)

	r, ok := ix.Route("R2")
	require.True(t, ok)
	assert.Equal(t, "Loop", r.LongName)
	assert.Equal(t, ptr(int32(1)), r.SortOrder, "R2's route_sort_order is 1, not absent")
	_, ok = ix.Route("R9")
	assert.False(t, ok)
}

func TestRoutes_NaturalOrderWithoutSortOrder(t *testing.T) {
	ix := fixtureIndexEdited(t, "routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type\n"+
		"R1,A,10,Ten,3\n"+
		"R2,A,7,Seven,3\n")
	var ids []string
	for _, r := range ix.Routes() {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"R2", "R1"}, ids, "7 sorts before 10")
}

func TestRoutes_ExcludesRoutesWithoutIndexedTrips(t *testing.T) {
	ix := fixtureIndexEdited(t, "routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type\n"+
		"R1,A,1,Straight,3\n"+
		"R2,A,2,Loop,3\n"+
		"R3,A,3,Ghost,3\n")
	assert.Len(t, ix.Routes(), 2)
	_, ok := ix.Route("R3")
	assert.False(t, ok)
}

func TestBuildIndex_HeadsignDirectionAndStopNames(t *testing.T) {
	ix := fixtureIndex(t)
	t1, _ := ix.Trip("T1")
	assert.Equal(t, "North", t1.Headsign)
	assert.Equal(t, 0, t1.DirectionID)
	assert.Equal(t, "Stop ST2", t1.StopTimes[1].StopName)
	t2, _ := ix.Trip("T2")
	assert.Equal(t, -1, t2.DirectionID, "absent direction_id")
	t3, _ := ix.Trip("T3")
	assert.Equal(t, 1, t3.DirectionID)
}

func TestTripsOnRoute(t *testing.T) {
	ix := fixtureIndex(t)
	weekday := ix.TripsOnRoute("R1", "20260902") // Wednesday
	require.Len(t, weekday, 1)
	assert.Equal(t, "T1", weekday[0].ID)
	sat := ix.TripsOnRoute("R1", "20260905")
	require.Len(t, sat, 1)
	assert.Equal(t, "T2", sat[0].ID)
	assert.Empty(t, ix.TripsOnRoute("R9", "20260902"))
	assert.Empty(t, ix.TripsOnRoute("R1", "garbage"))
}

func TestTripsOnRoute_SortedByFirstDeparture(t *testing.T) {
	// T2 moved to weekday service and scheduled before T1, with an id that
	// sorts after it: departure order must win over id order.
	ix := fixtureIndexEditedFiles(t, map[string]string{
		"trips.txt": "route_id,service_id,trip_id,shape_id,trip_headsign,direction_id\n" +
			"R1,WEEKDAY,T1,S1,North,0\n" +
			"R1,WEEKDAY,T2,S1,North,\n" +
			"R2,WEEKDAY,T3,S2,Loop,1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled\n" +
			stopTimeRow("T1", "08:00:00", "ST1", 1, "0") +
			stopTimeRow("T1", "08:10:00", "ST3", 3, "1001") +
			stopTimeRow("T2", "07:00:00", "ST1", 1, "0") +
			stopTimeRow("T2", "07:10:00", "ST3", 3, "1001") +
			stopTimeRow("T3", "25:00:00", "LP1", 1, "") +
			stopTimeRow("T3", "25:20:00", "LP1", 4, ""),
	})
	trips := ix.TripsOnRoute("R1", "20260902")
	require.Len(t, trips, 2)
	assert.Equal(t, "T2", trips[0].ID)
	assert.Equal(t, "T1", trips[1].ID)
	// TripsOnRoute hands out a fresh slice: mutating it must not touch the index.
	trips[0] = nil
	assert.Equal(t, "T2", ix.TripsOnRoute("R1", "20260902")[0].ID)
}
