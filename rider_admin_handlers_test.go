package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestRiderAdminStatus_Disabled(t *testing.T) {
	w := httptest.NewRecorder()
	handleRiderAdminStatus(riderOff{}).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil))
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
	assert.Equal(t, 4, st.GTFS.Trips)
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

// TestRiderStatus_LeavesOutFeedCredentials drives the page and the API through
// newHandler: a viewer may open both, so neither may carry what the feeds'
// addresses sign in with. An admin gets the same addresses.
func TestRiderStatus_LeavesOutFeedCredentials(t *testing.T) {
	body, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	ix, err := rider.IndexFromBytes(body, "https://agency:gtfs-password@gtfs.example/static/feed.zip?api_key=gtfs-key", time.Now())
	require.NoError(t, err)
	env := newRiderTestEnv(t)
	env.svc.index = func() *rider.Index { return ix }
	env.trusted.health = []rider.FeedHealth{
		{URL: "https://token@feed.example:8443/vehicles.pb?agency=1&api_key=feed-key#feed-fragment", LastSuccess: time.Now(), Entities: 3},
		{URL: "https://feed.example/%zz?api_key=unparsed-key", LastError: "status 403"},
	}
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return ix }, testReportSettings)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", env.svc, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)

	for _, role := range []string{roleViewer, roleAdmin} {
		for _, path := range []string{"/admin/rider", "/api/v1/admin/rider/status"} {
			t.Run(role+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.AddCookie(cookieFor(t, role))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code)
				got := w.Body.String()

				assert.Contains(t, got, "https://gtfs.example/static/feed.zip")
				assert.Contains(t, got, "https://feed.example:8443/vehicles.pb")
				assert.Contains(t, got, "status 403")
				for _, secret := range []string{"agency:", "gtfs-password", "gtfs-key", "token@", "feed-key", "feed-fragment", "unparsed-key"} {
					assert.NotContains(t, got, secret)
				}
			})
		}
	}
}

func TestShownFeedURL(t *testing.T) {
	for _, tc := range []struct{ name, url, want string }{
		{"nothing to leave out", "https://feed.example/vehicles.pb", "https://feed.example/vehicles.pb"},
		{"a key in the query", "https://feed.example/vehicles.pb?api_key=k", "https://feed.example/vehicles.pb"},
		{"an empty query", "https://feed.example/vehicles.pb?", "https://feed.example/vehicles.pb"},
		{"a user name and password", "https://u:p@feed.example/vehicles.pb", "https://feed.example/vehicles.pb"},
		{"a key as the user name", "https://k@feed.example/vehicles.pb", "https://feed.example/vehicles.pb"},
		{"a fragment", "https://feed.example/vehicles.pb#k", "https://feed.example/vehicles.pb"},
		{"upper case scheme", "HTTPS://feed.example/vehicles.pb?api_key=k", "https://feed.example/vehicles.pb"},
		{"no scheme", "k@feed.example/vehicles.pb", "not a valid URL"},
		{"no slashes after the scheme", "https:k@feed.example/vehicles.pb", "not a valid URL"},
		{"not a URL", "https://feed.example/%zz?api_key=k", "not a valid URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shownFeedURL(tc.url))
		})
	}
}

func TestShownGTFSSource(t *testing.T) {
	assert.Equal(t, "https://gtfs.example/feed.zip", shownGTFSSource("https://u:p@gtfs.example/feed.zip?key=k"))
	assert.Equal(t, "http://gtfs.example/feed.zip", shownGTFSSource("http://gtfs.example/feed.zip?key=k"))
	// A local path is shown as it is, even one that would not parse as a URL.
	assert.Equal(t, "/srv/gtfs/100% feed?.zip", shownGTFSSource("/srv/gtfs/100% feed?.zip"))
}
