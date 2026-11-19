package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// liveStopTracker holds four vehicles placed as accepted reports place them:
// at ST2 on T6, heading to it 250 m back, with no trip and so no stop, and at
// a first stop whose GTFS stop_sequence is 0, which the fixture has none of
// and so is recorded as the locator would place it.
func liveStopTracker(t *testing.T) *Tracker {
	t.Helper()
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	tracker := newPlacingTracker(t, ix)
	report := func(id, tripID string, pos rider.LatLon) {
		startDate := ""
		if tripID != "" {
			startDate = "20260902"
		}
		loc := &LocationReport{VehicleID: id, TripID: tripID, StartDate: startDate,
			Latitude: pos.Lat, Longitude: pos.Lon, Timestamp: time.Now().Unix()}
		require.True(t, tracker.UpdateAuthorized(loc, time.Now()))
	}
	report("at-stop", "T6", t6.Shape.PointAt(500))
	report("in-transit", "T6", t6.Shape.PointAt(250))
	report("no-stop", "", rider.LatLon{Lat: 47.7, Lon: -122.33})

	tracker.mu.Lock()
	tracker.update(&LocationReport{VehicleID: "first-stop", TripID: "T6", StartDate: "20260902",
		Latitude: 47.6, Longitude: -122.33, Timestamp: time.Now().Unix()},
		tripPlace{stopID: "ST1", stopSequence: 0, stoppedAt: true})
	tracker.mu.Unlock()
	return tracker
}

// liveEntries serves the admin live view from tracker and returns each
// vehicle's entry as raw JSON fields, by vehicle id.
func liveEntries(t *testing.T, tracker *Tracker) map[string]map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, &fakeVehicleLister{}, &fakeActiveTrips{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Vehicles []map[string]json.RawMessage `json:"vehicles"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	out := make(map[string]map[string]json.RawMessage, len(resp.Vehicles))
	for _, v := range resp.Vehicles {
		var id string
		require.NoError(t, json.Unmarshal(v["vehicle_id"], &id))
		out[id] = v
	}
	return out
}

func TestHandleLiveVehicles_Stop(t *testing.T) {
	entries := liveEntries(t, liveStopTracker(t))
	require.Len(t, entries, 4)

	tests := []struct {
		vehicle                  string
		stopID, sequence, status string
	}{
		{"at-stop", `"ST2"`, `2`, `"STOPPED_AT"`},
		{"in-transit", `"ST2"`, `2`, `"IN_TRANSIT_TO"`},
		{"first-stop", `"ST1"`, `0`, `"STOPPED_AT"`},
		// No stop is null, not "" and 0: a vehicle with no stop is not at
		// stop_sequence 0.
		{"no-stop", `null`, `null`, `null`},
	}
	for _, tc := range tests {
		t.Run(tc.vehicle, func(t *testing.T) {
			e := entries[tc.vehicle]
			for field, want := range map[string]string{"stop_id": tc.stopID, "stop_sequence": tc.sequence, "current_status": tc.status} {
				got, ok := e[field]
				if assert.True(t, ok, "%s is always present", field) {
					assert.JSONEq(t, want, string(got), field)
				}
			}
		})
	}
}

// The admin live view and the GTFS-RT feed name the same stop, sequence and
// status for each vehicle, read from the same tracker state.
func TestHandleLiveVehicles_StopAgreesWithFeed(t *testing.T) {
	tracker := liveStopTracker(t)
	entries := liveEntries(t, tracker)
	feed := buildFeed(tracker.ActiveVehicles(), nil)
	require.Len(t, feed.Entity, len(entries))

	for _, e := range feed.Entity {
		vp := e.GetVehicle()
		entry, ok := entries[e.GetId()]
		require.True(t, ok, e.GetId())

		var stopID *string
		var sequence *uint32
		var status *string
		require.NoError(t, json.Unmarshal(entry["stop_id"], &stopID))
		require.NoError(t, json.Unmarshal(entry["stop_sequence"], &sequence))
		require.NoError(t, json.Unmarshal(entry["current_status"], &status))
		if vp.StopId == nil {
			assert.Nil(t, stopID, e.GetId())
			assert.Nil(t, sequence, e.GetId())
			assert.Nil(t, status, e.GetId())
			continue
		}
		if assert.NotNil(t, stopID, e.GetId()) && assert.NotNil(t, sequence, e.GetId()) && assert.NotNil(t, status, e.GetId()) {
			assert.Equal(t, vp.GetStopId(), *stopID, e.GetId())
			assert.Equal(t, vp.GetCurrentStopSequence(), *sequence, e.GetId())
			assert.Equal(t, vp.GetCurrentStatus().String(), *status, e.GetId())
		}
	}
}
