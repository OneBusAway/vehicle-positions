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

// TestFeed_FrequencyRunOnADayItDoesNotRunGetsNoStartDate: a trip started on a
// day it does not run, here a weekday trip on a Saturday, has no service date
// to count a run from, so a report that leaves start_date out gets neither
// start_date nor start_time, as a rider could not start a ride on it. A
// start_date the report sends is kept, as it always was.
func TestFeed_FrequencyRunOnADayItDoesNotRunGetsNoStartDate(t *testing.T) {
	ix := frequencyFixtureIndex(t)
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	saturday, err := rider.ServiceDayStart("20260905", ix.Timezone())
	require.NoError(t, err)
	started := saturday.Add(8*time.Hour + 3*time.Minute)
	store := &tripStartStore{started: map[string]time.Time{"bus-1": started, "bus-2": started}}
	post := handlePostLocation(store, tracker, rl)

	for vehicleID, startDate := range map[string]string{"bus-1": "", "bus-2": "20260905"} {
		pos := mustTrip(t, ix, "T1").Shape.PointAt(250)
		w := postLocationWithClaims(post, LocationReport{VehicleID: vehicleID, TripID: "T1", StartDate: startDate,
			Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}, jwt.MapClaims{"sub": "driver-" + vehicleID})
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	trips := feedTrips(t, tracker)
	require.Len(t, trips, 2)
	assert.Nil(t, trips["bus-1"].StartDate)
	assert.Nil(t, trips["bus-1"].StartTime)
	assert.Equal(t, "20260905", trips["bus-2"].GetStartDate())
	assert.Equal(t, "08:03:00", trips["bus-2"].GetStartTime())
}

// TestFeed_OvernightFrequencyRunWithoutStartDate: a headway window that runs
// past midnight, 06:00 to 26:00, makes a run started at 00:30 one of the day
// before, though the trip's pattern of stop times ends at 08:10.
func TestFeed_OvernightFrequencyRunWithoutStartDate(t *testing.T) {
	keep := func(_ string, body []byte) []byte { return body }
	ix := fixtureIndexEdited(t, keep, fixtureMember{"frequencies.txt",
		"trip_id,start_time,end_time,headway_secs,exact_times\nT1,06:00:00,26:00:00,600,0\n"})
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	wednesday, err := rider.ServiceDayStart("20260902", ix.Timezone())
	require.NoError(t, err)
	store := &tripStartStore{started: map[string]time.Time{"bus-1": wednesday.Add(24*time.Hour + 30*time.Minute)}}
	post := handlePostLocation(store, tracker, rl)

	pos := mustTrip(t, ix, "T1").Shape.PointAt(250)
	w := postLocationWithClaims(post, LocationReport{VehicleID: "bus-1", TripID: "T1",
		Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}, jwt.MapClaims{"sub": "driver-bus-1"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	trip := feedTrips(t, tracker)["bus-1"]
	assert.Equal(t, "20260902", trip.GetStartDate())
	assert.Equal(t, "24:30:00", trip.GetStartTime())
}
