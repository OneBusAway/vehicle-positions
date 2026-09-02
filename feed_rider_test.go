package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/OneBusAway/vehicle-positions/rider"
)

type staticEstimates []rider.TripEstimate

func (s staticEstimates) Estimates(_ time.Time) []rider.TripEstimate { return s }

func sampleEstimate() rider.TripEstimate {
	speed := 8.5
	return rider.TripEstimate{
		Key: rider.TripKey{TripID: "T1", StartDate: "20260902"}, RouteID: "R1",
		Pos: rider.LatLon{Lat: 47.6045, Lon: -122.33}, Bearing: 12, Speed: &speed,
		Timestamp: time.Now().Add(-5 * time.Second), StopID: "ST2", StopSequence: 2, Riders: 2,
	}
}

func TestBuildFeed_RiderEntities(t *testing.T) {
	driver := &VehicleState{VehicleID: "bus-1", TripID: "T9", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	feed := buildFeed([]*VehicleState{driver}, []rider.TripEstimate{sampleEstimate()})
	require.Len(t, feed.Entity, 2)
	e := feed.Entity[1]
	assert.Equal(t, "rider:T1:20260902", e.GetId())
	vp := e.GetVehicle()
	assert.Equal(t, "rider:T1:20260902", vp.GetVehicle().GetId(), "vehicle.id carries the start date so two service dates are two vehicles")
	assert.Equal(t, "Rider-reported", vp.GetVehicle().GetLabel())
	assert.Equal(t, "T1", vp.GetTrip().GetTripId())
	assert.Equal(t, "R1", vp.GetTrip().GetRouteId())
	assert.Equal(t, "20260902", vp.GetTrip().GetStartDate())
	assert.InDelta(t, 47.6045, vp.GetPosition().GetLatitude(), 0.0001)
	assert.InDelta(t, 12, vp.GetPosition().GetBearing(), 0.01)
	assert.InDelta(t, 8.5, vp.GetPosition().GetSpeed(), 0.01)
	assert.Equal(t, uint32(2), vp.GetCurrentStopSequence())
	assert.Equal(t, "ST2", vp.GetStopId())
	assert.Equal(t, gtfsrt.VehiclePosition_IN_TRANSIT_TO, vp.GetCurrentStatus())
	assert.Empty(t, validateFeedCompliance(t, feed))
}

func TestBuildFeed_RiderEntity_NoSpeedNoStop(t *testing.T) {
	est := sampleEstimate()
	est.Speed, est.StopID, est.StopSequence, est.RouteID = nil, "", 0, ""
	feed := buildFeed(nil, []rider.TripEstimate{est})
	vp := feed.Entity[0].GetVehicle()
	assert.Nil(t, vp.GetPosition().Speed)
	assert.Nil(t, vp.StopId)
	assert.Nil(t, vp.CurrentStopSequence)
	assert.Nil(t, vp.CurrentStatus)
	assert.Nil(t, vp.GetTrip().RouteId)
	assert.Empty(t, validateFeedCompliance(t, feed))
}

func TestHandleGetFeed_SourceFilter(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})
	h := handleGetFeed(tracker, staticEstimates{sampleEstimate()})

	count := func(source string) (int, int) {
		url := "/gtfs-rt/vehicle-positions?format=json"
		if source != "" {
			url += "&source=" + source
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != http.StatusOK {
			return -1, w.Code
		}
		var feed gtfsrt.FeedMessage
		require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &feed))
		return len(feed.Entity), w.Code
	}
	n, _ := count("")
	assert.Equal(t, 2, n)
	n, _ = count("all")
	assert.Equal(t, 2, n)
	n, _ = count("driver")
	assert.Equal(t, 1, n)
	n, _ = count("rider")
	assert.Equal(t, 1, n)
	_, code := count("bogus")
	assert.Equal(t, http.StatusBadRequest, code)

	// nil estimate source behaves like the old feed.
	w := httptest.NewRecorder()
	handleGetFeed(tracker, nil).ServeHTTP(w, httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions?source=rider&format=json", nil))
	var feed gtfsrt.FeedMessage
	require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &feed))
	assert.Empty(t, feed.Entity)
}

func TestBuildFeed_HeaderTimestampCoversRiderEntities(t *testing.T) {
	est := sampleEstimate()
	est.Timestamp = time.Now().Add(30 * time.Second) // aggregator clamps to now; buildFeed must still satisfy E012
	feed := buildFeed(nil, []rider.TripEstimate{est})
	assert.GreaterOrEqual(t, feed.Header.GetTimestamp(), feed.Entity[0].GetVehicle().GetTimestamp())
}
