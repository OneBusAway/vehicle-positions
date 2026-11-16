package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// TestFeed_FrequencyRunWithoutStartDateGetsOne: GTFS-RT needs a frequency
// run's start_date beside its start_time. A report that leaves start_date out
// is published with the service date its start_time counts from, the one the
// schedule runs the trip on nearest the trip's start. A trip run by timetable
// is still published with none.
func TestFeed_FrequencyRunWithoutStartDateGetsOne(t *testing.T) {
	ix := frequencyFixtureIndex(t)
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	dayStart, err := rider.ServiceDayStart("20260902", ix.Timezone())
	require.NoError(t, err)
	store := &tripStartStore{started: map[string]time.Time{
		"bus-1": dayStart.Add(8*time.Hour + 3*time.Minute + 17*time.Second),
		"bus-2": dayStart.Add(8 * time.Hour),
	}}
	post := handlePostLocation(store, tracker, rl)

	for vehicleID, tripID := range map[string]string{"bus-1": "T1", "bus-2": "T3"} {
		pos := mustTrip(t, ix, tripID).Shape.PointAt(250)
		w := postLocationWithClaims(post, LocationReport{VehicleID: vehicleID, TripID: tripID,
			Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}, jwt.MapClaims{"sub": "driver-" + vehicleID})
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	trips := feedTrips(t, tracker)
	require.Len(t, trips, 2)
	assert.Equal(t, "20260902", trips["bus-1"].GetStartDate())
	assert.Equal(t, "08:03:17", trips["bus-1"].GetStartTime())
	assert.Nil(t, trips["bus-2"].StartDate, "a trip run by timetable gets no start_date the report did not send")
	assert.Nil(t, trips["bus-2"].StartTime)
}
