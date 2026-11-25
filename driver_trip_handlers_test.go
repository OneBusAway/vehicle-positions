package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// listMyTrips asks handler for the trips of the account sub names.
func listMyTrips(handler http.HandlerFunc, sub, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/trips"+query, nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": sub, "role": roleDriver}))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func decodeMyTrips(t *testing.T, w *httptest.ResponseRecorder) myTripListResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got myTripListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

func TestHandleListMyTrips(t *testing.T) {
	start := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	straight := &catalogTripRoute{ShortName: "1", LongName: "Straight", Color: "0077C0", TextColor: "FFFFFF"}
	summary := TripSummary{ID: 7, VehicleID: "bus-1", VehicleLabel: "Bus One", UserID: 42, DriverName: "Asha", StartTime: start, EndTime: &end, Status: "completed"}
	with := func(routeID, tripID string) TripSummary {
		s := summary
		s.RouteID, s.GtfsTripID = routeID, tripID
		return s
	}
	tests := []struct {
		name     string
		trip     TripSummary
		catalog  *gtfsCatalog
		route    *catalogTripRoute
		headsign string
	}{
		{"a scheduled trip", with("R1", "T1"), testCatalog(t), straight, "North"},
		{"a trip started with its GTFS trip alone", with("", "T1"), testCatalog(t), straight, "North"},
		{"a trip started with a route alone", with("R1", ""), testCatalog(t), straight, ""},
		{"a trip the schedule has since put on another route", with("R2", "T1"), testCatalog(t), &catalogTripRoute{ShortName: "2", LongName: "Loop"}, "North"},
		{"a trip the schedule no longer has", with("R1", "GONE"), testCatalog(t), straight, ""},
		{"a route the schedule no longer has", with("GONE", "GONE"), testCatalog(t), nil, ""},
		{"a trip with neither", with("", ""), testCatalog(t), nil, ""},
		{"no schedule yet", with("R1", "T1"), newGTFSCatalog(func() *rider.Index { return nil }, rider.DefaultThresholds()), nil, ""},
		{"no catalog", with("R1", "T1"), nil, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeTripLister{trips: []TripSummary{tt.trip}}
			got := decodeMyTrips(t, listMyTrips(handleListMyTrips(store, tt.catalog), "42", ""))

			assert.Equal(t, TripFilter{UserID: 42, Limit: defaultTripListLimit + 1}, store.captured, "only the caller's own")
			assert.Equal(t, myTripListResponse{Count: 1, Trips: []myTrip{{
				ID: 7, VehicleID: "bus-1", VehicleLabel: "Bus One", RouteID: tt.trip.RouteID, Route: tt.route,
				GtfsTripID: tt.trip.GtfsTripID, Headsign: tt.headsign, StartTime: start, EndTime: &end, Status: "completed",
			}}}, got)
		})
	}
}

func TestHandleListMyTrips_Pages(t *testing.T) {
	trips := make([]TripSummary, 3)
	for i := range trips {
		trips[i] = TripSummary{ID: int64(i + 1), Status: "completed"}
	}
	store := &fakeTripLister{trips: trips}
	got := decodeMyTrips(t, listMyTrips(handleListMyTrips(store, nil), "42", "?limit=2&offset=4"))
	assert.Equal(t, TripFilter{UserID: 42, Limit: 3, Offset: 4}, store.captured)
	assert.Equal(t, 2, got.Count)
	assert.True(t, got.HasMore)
	require.Len(t, got.Trips, 2)
	assert.Equal(t, int64(2), got.Trips[1].ID)

	store.trips = trips[:2]
	got = decodeMyTrips(t, listMyTrips(handleListMyTrips(store, nil), "42", "?limit=2"))
	assert.False(t, got.HasMore)
	assert.Equal(t, 2, got.Count)
}

func TestHandleListMyTrips_EmptyIsJSONArray(t *testing.T) {
	w := listMyTrips(handleListMyTrips(&fakeTripLister{}, nil), "42", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"count":0,"has_more":false,"trips":[]}`, w.Body.String())
}

func TestHandleListMyTrips_Refuses(t *testing.T) {
	tests := []struct {
		name  string
		sub   string
		query string
		store *fakeTripLister
		code  int
		want  string
	}{
		{"a subject that is no number", "rider-uuid", "", &fakeTripLister{}, http.StatusUnauthorized, "invalid token: invalid subject"},
		// TripFilter reads 0 as every driver, so it must not reach the store.
		{"a subject that is no account", "0", "", &fakeTripLister{}, http.StatusUnauthorized, "invalid token: invalid subject"},
		{"a negative subject", "-1", "", &fakeTripLister{}, http.StatusUnauthorized, "invalid token: invalid subject"},
		{"a limit of none", "42", "?limit=0", &fakeTripLister{}, http.StatusBadRequest, "limit must be between 1 and 200"},
		{"a limit past the cap", "42", "?limit=201", &fakeTripLister{}, http.StatusBadRequest, "limit must be between 1 and 200"},
		{"a negative offset", "42", "?offset=-1", &fakeTripLister{}, http.StatusBadRequest, "offset must be a non-negative integer"},
		{"a store that fails", "42", "", &fakeTripLister{err: errors.New("boom")}, http.StatusInternalServerError, "internal server error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := listMyTrips(handleListMyTrips(tt.store, nil), tt.sub, tt.query)
			assert.Equal(t, tt.code, w.Code)
			assert.Equal(t, tt.want, decodeError(t, w))
			if tt.code != http.StatusInternalServerError {
				assert.Zero(t, tt.store.captured, "the store must not be asked")
			}
		})
	}
}

// TestDriverTripsRoute_Wiring: a driver lists their own trips, and another
// account's user_id in the query changes nothing. A viewer, as everywhere in
// the driver API, is refused.
func TestDriverTripsRoute_Wiring(t *testing.T) {
	token := func(role string) string {
		signed, err := generateJWT(&User{ID: 42, Email: "driver@test.com", Role: role}, testSecret, defaultAccessTokenTTL)
		require.NoError(t, err)
		return "Bearer " + signed
	}
	store := &ownTripsStore{}
	mux := newMux(store, nil, nil, testSecret, testTTLs, time.Time{}, nil, nil, false, false, "", nil, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), nil, newFeedHealth(time.Now))
	tests := []struct {
		name string
		auth string
		want int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"a driver", token(roleDriver), http.StatusOK},
		{"an admin", token(roleAdmin), http.StatusOK},
		{"a viewer", token(roleViewer), http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store.captured = TripFilter{}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/trips?user_id=7&vehicle_id=bus-9&q=x", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			assert.Equal(t, tt.want, w.Code, w.Body.String())
			if tt.want == http.StatusOK {
				assert.Equal(t, TripFilter{UserID: 42, Limit: defaultTripListLimit + 1}, store.captured)
			} else {
				assert.Zero(t, store.captured)
			}
		})
	}
}

// ownTripsStore is a noopStore that records what ListTrips is asked for.
type ownTripsStore struct {
	noopStore
	captured TripFilter
}

func (s *ownTripsStore) ListTrips(_ context.Context, f TripFilter) ([]TripSummary, error) {
	s.captured = f
	return nil, nil
}

// TestStore_ListTrips_OneDriversOwn: with two drivers' trips in the database, a
// driver's list has only theirs, newest first, the running one included.
func TestStore_ListTrips_OneDriversOwn(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()
	ana := setupTripUser(t, store, "Ana Driver", "ana-own@test.com", "bus-list-1", "Bus List 1")
	bo := setupTripUser(t, store, "Bo Driver", "bo-own@test.com", "bus-list-2", "Bus List 2")
	first, err := store.StartTrip(ctx, ana, "bus-list-1", "R1", "T1")
	require.NoError(t, err)
	mustEndTrip(t, store, ctx, first.ID, ana)
	_, err = store.StartTrip(ctx, bo, "bus-list-2", "R2", "T3")
	require.NoError(t, err)
	running, err := store.StartTrip(ctx, ana, "bus-list-1", "R1", "T4")
	require.NoError(t, err)

	got := decodeMyTrips(t, listMyTrips(handleListMyTrips(store, nil), strconv.FormatInt(ana, 10), ""))
	require.Equal(t, 2, got.Count)
	assert.Equal(t, running.ID, got.Trips[0].ID)
	assert.Equal(t, "active", got.Trips[0].Status)
	assert.Nil(t, got.Trips[0].EndTime)
	assert.Equal(t, first.ID, got.Trips[1].ID)
	assert.Equal(t, "completed", got.Trips[1].Status)
	assert.NotNil(t, got.Trips[1].EndTime)
	assert.Equal(t, "Bus List 1", got.Trips[1].VehicleLabel)
}
