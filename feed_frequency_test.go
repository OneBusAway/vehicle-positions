package main

import (
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

// frequencyFixtureIndex is the fixture feed with T1 run every 10 minutes by
// headway (frequencies.txt exact_times=0) and every other trip by timetable.
func frequencyFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	keep := func(_ string, body []byte) []byte { return body }
	return fixtureIndexEdited(t, keep, fixtureMember{"frequencies.txt",
		"trip_id,start_time,end_time,headway_secs,exact_times\nT1,06:00:00,22:00:00,600,0\n"})
}

// feedScheduleRelationships is the schedule_relationship of each entity's
// trip in the feed tracker publishes, "unset" where it has none.
func feedScheduleRelationships(t *testing.T, tracker *Tracker) map[string]string {
	t.Helper()
	w := getFeed(handleGetFeed(tracker, riderOff{}), "")
	require.Equal(t, http.StatusOK, w.Code)
	var feed gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(w.Body.Bytes(), &feed))
	got := make(map[string]string)
	for _, e := range feed.Entity {
		rel := "unset"
		if r := e.GetVehicle().GetTrip().ScheduleRelationship; r != nil {
			rel = r.String()
		}
		got[e.GetId()] = rel
	}
	return got
}

// TestFeed_FrequencyBasedTripIsUnscheduled: the GTFS-RT spec makes a trip run
// by headway UNSCHEDULED, and the validator raises E013 when one is
// SCHEDULED. A timetabled trip stays SCHEDULED.
func TestFeed_FrequencyBasedTripIsUnscheduled(t *testing.T) {
	ix := frequencyFixtureIndex(t)
	tracker := newPlacingTracker(t, ix)
	rl := NewVehicleRateLimiter()
	t.Cleanup(rl.Stop)
	post := handlePostLocation(&mockStore{}, tracker, rl)

	for vehicleID, tripID := range map[string]string{"bus-1": "T1", "bus-2": "T3"} {
		pos := mustTrip(t, ix, tripID).Shape.PointAt(250)
		w := postLocationWithClaims(post, LocationReport{VehicleID: vehicleID, TripID: tripID, StartDate: "20260902",
			Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}, jwt.MapClaims{"sub": "driver-1"})
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	assert.Equal(t, map[string]string{"bus-1": "UNSCHEDULED", "bus-2": "SCHEDULED"}, feedScheduleRelationships(t, tracker))
}

// TestFeed_SeededFrequencyBasedTripIsUnscheduled: a vehicle seeded at startup
// is not placed on its trip until it reports again, but the feed still names
// its trip right in the meantime.
func TestFeed_SeededFrequencyBasedTripIsUnscheduled(t *testing.T) {
	ix := frequencyFixtureIndex(t)
	tracker := newPlacingTracker(t, ix)
	pos := mustTrip(t, ix, "T1").Shape.PointAt(250)
	tracker.Update(&LocationReport{VehicleID: "bus-1", TripID: "T1", StartDate: "20260902",
		Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()})

	assert.Equal(t, map[string]string{"bus-1": "UNSCHEDULED"}, feedScheduleRelationships(t, tracker))
}

func TestBuildFeed_FrequencyBasedRiderEstimateIsUnscheduled(t *testing.T) {
	est := sampleEstimate()
	est.FrequencyBased = true
	feed := buildFeed(nil, []rider.TripEstimate{est})
	require.Len(t, feed.Entity, 1)
	assert.Equal(t, gtfsrt.TripDescriptor_UNSCHEDULED.Enum(), feed.Entity[0].GetVehicle().GetTrip().ScheduleRelationship)
}
