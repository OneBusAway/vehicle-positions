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

func TestRiderAdminStatus_Disabled(t *testing.T) {
	w := httptest.NewRecorder()
	handleRiderAdminStatus(nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"enabled":false}`, w.Body.String())
}

func TestRiderAdminStatus_Enabled(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	env.store.riders[riderID].Tier = "trusted"
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.upload(t, tok, ride.RideID, env.walkPoints(0, 3))
	env.trusted.health = []rider.FeedHealth{{URL: "http://feed", LastSuccess: time.Now(), Entities: 3}}

	w := httptest.NewRecorder()
	handleRiderAdminStatus(env.svc).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var st riderStatusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.True(t, st.Enabled)
	require.NotNil(t, st.GTFS)
	assert.Equal(t, 3, st.GTFS.Trips)
	assert.Equal(t, "America/Los_Angeles", st.GTFS.Timezone)
	require.Len(t, st.TrustedFeeds, 1)
	assert.Equal(t, 3, st.TrustedFeeds[0].Entities)
	assert.NotEmpty(t, st.TrustedFeeds[0].LastSuccess)
	require.NotNil(t, st.Riders)
	assert.Equal(t, 1, st.Riders.Total)
	assert.Equal(t, 1, st.Riders.Trusted)
	require.NotNil(t, st.Rides)
	assert.Equal(t, 1, st.Rides.Active)
	assert.Equal(t, 1, st.Rides.Publishable, "trusted rider, verified, fresh, not covered")
}

func TestRiderAdminRides(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	for i := 0; i < 3; i++ {
		r := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
		if i < 2 {
			env.do(t, "POST", "/api/v1/rider/rides/"+r.RideID+"/end", tok, map[string]any{"reason": "arrived"})
		}
	}
	h := handleRiderAdminRides(env.store)
	get := func(q string) adminRidesResponse {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/rides"+q, nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp adminRidesResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		return resp
	}
	active := get("")
	assert.Equal(t, 1, active.Count)
	assert.Equal(t, "active", active.Rides[0].Status)
	assert.Nil(t, active.Rides[0].EndedAt)
	ended := get("?status=ended&limit=1")
	assert.Equal(t, 1, ended.Count)
	assert.True(t, ended.HasMore)
	assert.NotNil(t, ended.Rides[0].EndedAt)
	assert.Equal(t, "arrived", ended.Rides[0].EndReason)
	page2 := get("?status=ended&limit=1&offset=1")
	assert.False(t, page2.HasMore)

	for _, q := range []string{"?status=weird", "?limit=0", "?limit=201", "?offset=-1"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/rides"+q, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, q)
	}
}
