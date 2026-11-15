package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// mixedFrequencyFixtureIndex is the fixture feed with T1 run every 5 minutes
// by headway until 09:00 and every 10 minutes to a timetable after it.
func mixedFrequencyFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	keep := func(_ string, body []byte) []byte { return body }
	return fixtureIndexEdited(t, keep, fixtureMember{"frequencies.txt",
		"trip_id,start_time,end_time,headway_secs,exact_times\n" +
			"T1,06:00:00,09:00:00,300,0\n" +
			"T1,09:00:00,22:00:00,600,1\n"})
}

// tripStartStore authorizes every report and sets the time its vehicle's trip
// started, as Store.AuthorizeLocation does from the trips row.
type tripStartStore struct {
	mockStore
	started map[string]time.Time // by vehicle id
}

func (s *tripStartStore) AuthorizeLocation(_ context.Context, loc *LocationReport) error {
	loc.TripStartedAt = s.started[loc.VehicleID]
	return nil
}

// feedTrips is the TripDescriptor of each entity in the feed tracker
// publishes.
func feedTrips(t *testing.T, tracker *Tracker) map[string]*gtfsrt.TripDescriptor {
	t.Helper()
	w := getFeed(handleGetFeed(tracker, riderOff{}), "")
	require.Equal(t, http.StatusOK, w.Code)
	var feed gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &feed))
	trips := make(map[string]*gtfsrt.TripDescriptor)
	for _, e := range feed.Entity {
		trips[e.GetId()] = e.GetVehicle().GetTrip()
	}
	return trips
}

// TestFeed_FrequencyRunCarriesStartTime: GTFS-RT needs start_time on every run
// of a trip in frequencies.txt to tell its runs apart, and the validator
// raises E006 without it. A headway run's is when its trip started, and a
// run to the timetable's is its scheduled departure; which one a run is
// depends on the window it started in, so T1 is UNSCHEDULED before 09:00 and
// SCHEDULED after. A trip run by timetable keeps no start_time.
func TestFeed_FrequencyRunCarriesStartTime(t *testing.T) {
	ix := mixedFrequencyFixtureIndex(t)
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	dayStart, err := rider.ServiceDayStart("20260902", ix.Timezone())
	require.NoError(t, err)
	store := &tripStartStore{started: map[string]time.Time{
		"bus-1": dayStart.Add(8*time.Hour + 30*time.Minute + 12*time.Second),
		"bus-2": dayStart.Add(10*time.Hour + 4*time.Minute),
		"bus-3": dayStart.Add(8 * time.Hour),
	}}
	post := handlePostLocation(store, tracker, rl)

	for vehicleID, tripID := range map[string]string{"bus-1": "T1", "bus-2": "T1", "bus-3": "T3"} {
		pos := mustTrip(t, ix, tripID).Shape.PointAt(250)
		w := postLocationWithClaims(post, LocationReport{VehicleID: vehicleID, TripID: tripID, StartDate: "20260902",
			Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}, jwt.MapClaims{"sub": "driver-" + vehicleID})
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	trips := feedTrips(t, tracker)
	require.Len(t, trips, 3)
	assert.Equal(t, "08:30:12", trips["bus-1"].GetStartTime())
	assert.Equal(t, gtfsrt.TripDescriptor_UNSCHEDULED, trips["bus-1"].GetScheduleRelationship())
	assert.Equal(t, "10:00:00", trips["bus-2"].GetStartTime())
	assert.Equal(t, gtfsrt.TripDescriptor_SCHEDULED, trips["bus-2"].GetScheduleRelationship())
	assert.Nil(t, trips["bus-3"].StartTime, "a trip run by timetable has no start_time")
	assert.Equal(t, gtfsrt.TripDescriptor_SCHEDULED, trips["bus-3"].GetScheduleRelationship())
}

// TestFeed_SeededFrequencyRunGetsStartTimeWithItsNextReport: the startup seed
// does not know when a vehicle's trip started. Until the vehicle reports
// again it is named by its trip, which runs by headway at some time of day,
// and has no start_time.
func TestFeed_SeededFrequencyRunGetsStartTimeWithItsNextReport(t *testing.T) {
	ix := mixedFrequencyFixtureIndex(t)
	tracker := newPlacingTracker(t, ix)
	dayStart, err := rider.ServiceDayStart("20260902", ix.Timezone())
	require.NoError(t, err)
	pos := mustTrip(t, ix, "T1").Shape.PointAt(250)
	loc := &LocationReport{VehicleID: "bus-1", TripID: "T1", StartDate: "20260902",
		Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}

	tracker.Update(loc)
	seeded := feedTrips(t, tracker)["bus-1"]
	assert.Nil(t, seeded.StartTime)
	assert.Equal(t, gtfsrt.TripDescriptor_UNSCHEDULED, seeded.GetScheduleRelationship())

	loc.TripStartedAt = dayStart.Add(10*time.Hour + 4*time.Minute)
	require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
	reported := feedTrips(t, tracker)["bus-1"]
	assert.Equal(t, "10:00:00", reported.GetStartTime())
	assert.Equal(t, gtfsrt.TripDescriptor_SCHEDULED, reported.GetScheduleRelationship())
}

func TestBuildFeed_RiderEstimateStartTime(t *testing.T) {
	headway := sampleEstimate()
	headway.StartTime, headway.FrequencyBased = "07:59:59", true
	timetable := sampleEstimate()
	timetable.Key.StartDate = "20260903"

	feed := buildFeed(nil, []rider.TripEstimate{headway, timetable})
	require.Len(t, feed.Entity, 2)
	assert.Equal(t, "07:59:59", feed.Entity[0].GetVehicle().GetTrip().GetStartTime())
	assert.Nil(t, feed.Entity[1].GetVehicle().GetTrip().StartTime, "a trip run by timetable has no start_time")
}
