package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// catalogNow is a Wednesday in the fixture's calendar, mid-morning Pacific.
var catalogNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func testCatalog(t *testing.T) *gtfsCatalog {
	t.Helper()
	ix, err := rider.LoadIndex(context.Background(), "rider/testdata/fixture.zip", nil, catalogNow)
	require.NoError(t, err)
	c := newGTFSCatalog(func() *rider.Index { return ix }, rider.DefaultThresholds())
	c.now = func() time.Time { return catalogNow }
	return c
}

func catalogGet(t *testing.T, h http.Handler, path string, pathValues map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return rec.Code, body
}

func TestCatalogRoutes(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleRoutes(), "/api/v1/gtfs/routes", nil)
	require.Equal(t, http.StatusOK, code)
	routes := body["routes"].([]any)
	require.Len(t, routes, 2)
	assert.Equal(t, map[string]any{"id": "R2", "short_name": "2", "long_name": "Loop", "color": "", "text_color": "", "type": float64(3)}, routes[0])
	assert.Equal(t, "R1", routes[1].(map[string]any)["id"])
	assert.Equal(t, "0077C0", routes[1].(map[string]any)["color"])
}

func TestCatalogRouteTrips_DefaultsToTodaysServiceDate(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips", map[string]string{"route_id": "R1"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "R1", body["route_id"])
	assert.Equal(t, "20260902", body["service_date"])
	assert.Equal(t, "America/Los_Angeles", body["timezone"])
	trips := body["trips"].([]any)
	require.Len(t, trips, 1)
	assert.Equal(t, map[string]any{
		"id": "T1", "headsign": "North", "direction_id": float64(0),
		"starts_at": "2026-09-02T08:00:00-07:00", "ends_at": "2026-09-02T08:10:00-07:00",
		"first_stop": "Stop ST1", "last_stop": "Stop ST3",
	}, trips[0])
}

func TestCatalogRouteTrips_ExplicitDateAndNullDirection(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=20260905", map[string]string{"route_id": "R1"})
	require.Equal(t, http.StatusOK, code)
	trips := body["trips"].([]any)
	require.Len(t, trips, 1)
	assert.Equal(t, "T2", trips[0].(map[string]any)["id"])
	assert.Nil(t, trips[0].(map[string]any)["direction_id"])
}

func TestCatalogRouteTrips_Errors(t *testing.T) {
	c := testCatalog(t)
	code, body := catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R9/trips", map[string]string{"route_id": "R9"})
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "unknown route", body["error"])

	code, body = catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=2026-09-02", map[string]string{"route_id": "R1"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "date must be YYYYMMDD", body["error"])

	code, body = catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=20261399", map[string]string{"route_id": "R1"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "date must be YYYYMMDD", body["error"])

	// A date with no service is an empty list, not an error.
	code, body = catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=20270106", map[string]string{"route_id": "R1"})
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, body["trips"])
}

func TestCatalogTrip_AfterMidnightLoop(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleTrip(), "/api/v1/gtfs/trips/T3?date=20260902", map[string]string{"trip_id": "T3"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "T3", body["id"])
	assert.Equal(t, "R2", body["route_id"])
	assert.Equal(t, "Loop", body["headsign"])
	assert.Equal(t, float64(1), body["direction_id"])
	assert.Equal(t, "20260902", body["service_date"])
	assert.Equal(t, "America/Los_Angeles", body["timezone"])
	assert.Equal(t, map[string]any{"short_name": "2", "long_name": "Loop", "color": "", "text_color": ""}, body["route"])

	shape := body["shape"].(map[string]any)
	assert.InDelta(t, 2000, shape["length_m"], 30)
	points := shape["points"].([]any)
	require.Len(t, points, 5)
	assert.Equal(t, []any{47.6, -122.33}, points[0])

	stops := body["stops"].([]any)
	require.Len(t, stops, 4)
	first := stops[0].(map[string]any)
	assert.Equal(t, "LP1", first["id"])
	assert.Equal(t, "Stop LP1", first["name"])
	assert.Equal(t, float64(1), first["sequence"])
	assert.Equal(t, "2026-09-03T01:00:00-07:00", first["arrival_at"], "25:00 on the 2nd is 01:00 on the 3rd")
	assert.Equal(t, "2026-09-03T01:00:00-07:00", first["departure_at"])
	assert.InDelta(t, 0, first["along_shape_m"], 2)
	last := stops[3].(map[string]any)
	assert.InDelta(t, shape["length_m"].(float64), last["along_shape_m"].(float64), 5)

	assert.Equal(t, map[string]any{"max_shape_distance_m": float64(60), "schedule_early_s": float64(900), "schedule_late_s": float64(5400)}, body["thresholds"])
}

func TestCatalogTrip_DefaultsToTheTripsServiceDate(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleTrip(), "/api/v1/gtfs/trips/T1", map[string]string{"trip_id": "T1"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "20260902", body["service_date"])
	stops := body["stops"].([]any)
	assert.Equal(t, "2026-09-02T08:00:00-07:00", stops[0].(map[string]any)["departure_at"])
}

func TestCatalogTrip_Errors(t *testing.T) {
	c := testCatalog(t)
	code, body := catalogGet(t, c.handleTrip(), "/api/v1/gtfs/trips/T9", map[string]string{"trip_id": "T9"})
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "unknown trip", body["error"])

	code, body = catalogGet(t, c.handleTrip(), "/api/v1/gtfs/trips/T1?date=20260905", map[string]string{"trip_id": "T1"})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, "trip not active on date", body["error"])

	code, body = catalogGet(t, c.handleTrip(), "/api/v1/gtfs/trips/T1?date=nope", map[string]string{"trip_id": "T1"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "date must be YYYYMMDD", body["error"])
}

func TestCatalog_NoIndexIs503(t *testing.T) {
	c := newGTFSCatalog(func() *rider.Index { return nil }, rider.DefaultThresholds())
	for _, h := range []http.Handler{c.handleRoutes(), c.handleRouteTrips(), c.handleTrip()} {
		code, body := catalogGet(t, h, "/x", map[string]string{"route_id": "R1", "trip_id": "T1"})
		assert.Equal(t, http.StatusServiceUnavailable, code)
		assert.Equal(t, "schedule data unavailable", body["error"])
	}
}
