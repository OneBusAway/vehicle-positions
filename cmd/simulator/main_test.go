package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBearing(t *testing.T) {
	tests := []struct {
		name     string
		from     Waypoint
		to       Waypoint
		expected float64
		delta    float64
	}{
		{
			name:     "due north",
			from:     Waypoint{Lat: 0, Lon: 0},
			to:       Waypoint{Lat: 1, Lon: 0},
			expected: 0,
			delta:    0.5,
		},
		{
			name:     "due east",
			from:     Waypoint{Lat: 0, Lon: 0},
			to:       Waypoint{Lat: 0, Lon: 1},
			expected: 90,
			delta:    0.5,
		},
		{
			name:     "due south",
			from:     Waypoint{Lat: 1, Lon: 0},
			to:       Waypoint{Lat: 0, Lon: 0},
			expected: 180,
			delta:    0.5,
		},
		{
			name:     "due west",
			from:     Waypoint{Lat: 0, Lon: 0},
			to:       Waypoint{Lat: 0, Lon: -1},
			expected: 270,
			delta:    0.5,
		},
		{
			name:     "nairobi CBD to Westlands (roughly northwest)",
			from:     Waypoint{Lat: -1.2864, Lon: 36.8172},
			to:       Waypoint{Lat: -1.2638, Lon: 36.8028},
			expected: 327,
			delta:    5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bearing(tt.from, tt.to)
			assert.InDelta(t, tt.expected, got, tt.delta, "bearing from %v to %v", tt.from, tt.to)
		})
	}
}

func TestHaversineDistance(t *testing.T) {
	tests := []struct {
		name    string
		from    Waypoint
		to      Waypoint
		minDist float64
		maxDist float64
	}{
		{
			name:    "same point",
			from:    Waypoint{Lat: -1.2864, Lon: 36.8172},
			to:      Waypoint{Lat: -1.2864, Lon: 36.8172},
			minDist: 0,
			maxDist: 0,
		},
		{
			name:    "nairobi CBD to Westlands (~3km)",
			from:    Waypoint{Lat: -1.2864, Lon: 36.8172},
			to:      Waypoint{Lat: -1.2638, Lon: 36.8028},
			minDist: 2500,
			maxDist: 3500,
		},
		{
			name:    "one degree latitude at equator (~111km)",
			from:    Waypoint{Lat: 0, Lon: 0},
			to:      Waypoint{Lat: 1, Lon: 0},
			minDist: 110000,
			maxDist: 112000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := haversineDistance(tt.from, tt.to)
			assert.GreaterOrEqual(t, got, tt.minDist)
			assert.LessOrEqual(t, got, tt.maxDist)
		})
	}
}

func TestSpeed(t *testing.T) {
	dist := haversineDistance(
		Waypoint{Lat: -1.2864, Lon: 36.8172},
		Waypoint{Lat: -1.2638, Lon: 36.8028},
	)
	require.Greater(t, dist, 0.0)

	s := speed(dist, 10.0)
	assert.Greater(t, s, 0.0)
	assert.InDelta(t, dist/10.0, s, 0.001)

	assert.Equal(t, 0.0, speed(100, 0))
	assert.Equal(t, 0.0, speed(100, -1))
}

func TestInterpolate(t *testing.T) {
	a := Waypoint{Lat: 0, Lon: 0}
	b := Waypoint{Lat: 10, Lon: 20}

	tests := []struct {
		name string
		t    float64
		want Waypoint
	}{
		{"start", 0.0, a},
		{"end", 1.0, b},
		{"midpoint", 0.5, Waypoint{Lat: 5, Lon: 10}},
		{"quarter", 0.25, Waypoint{Lat: 2.5, Lon: 5}},
		{"clamp below zero", -0.5, a},
		{"clamp above one", 1.5, b},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpolate(a, b, tt.t)
			assert.InDelta(t, tt.want.Lat, got.Lat, 0.0001)
			assert.InDelta(t, tt.want.Lon, got.Lon, 0.0001)
		})
	}
}

func TestLocationReportJSONRoundTrip(t *testing.T) {
	report := locationReport{
		VehicleID: "sim-vehicle-001",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Bearing:   327.5,
		Speed:     8.0,
		Timestamp: 1752566400,
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	// Verify JSON field names match server's expected format
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	expectedFields := []string{"vehicle_id", "latitude", "longitude", "bearing", "speed", "timestamp"}
	for _, field := range expectedFields {
		assert.Contains(t, raw, field, "missing JSON field %q", field)
	}
	assert.Len(t, raw, len(expectedFields), "unexpected extra fields in JSON")

	// Verify round-trip preserves values
	var decoded locationReport
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, report, decoded)
}

func TestRouteWraparound(t *testing.T) {
	route := []Waypoint{
		{Lat: 0, Lon: 0},
		{Lat: 1, Lon: 1},
		{Lat: 2, Lon: 2},
	}

	for i := 0; i < 10; i++ {
		idx := i % len(route)
		nextIdx := (i + 1) % len(route)
		from := route[idx]
		to := route[nextIdx]
		pos := interpolate(from, to, 0.5)
		assert.False(t, math.IsNaN(pos.Lat), "NaN at wraparound index %d", i)
		assert.False(t, math.IsNaN(pos.Lon), "NaN at wraparound index %d", i)
	}
}

func TestRoutesNotEmpty(t *testing.T) {
	require.NotEmpty(t, routes, "predefined routes must not be empty")
	for i, route := range routes {
		require.GreaterOrEqual(t, len(route), 2, "route %d must have at least 2 waypoints", i)
		for j, wp := range route {
			assert.GreaterOrEqual(t, wp.Lat, -90.0, "route %d waypoint %d lat", i, j)
			assert.LessOrEqual(t, wp.Lat, 90.0, "route %d waypoint %d lat", i, j)
			assert.GreaterOrEqual(t, wp.Lon, -180.0, "route %d waypoint %d lon", i, j)
			assert.LessOrEqual(t, wp.Lon, 180.0, "route %d waypoint %d lon", i, j)
		}
	}
}

func TestSendReport_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/locations", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s := &stats{}
	report := &locationReport{
		VehicleID: "test-vehicle",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Bearing:   90.0,
		Speed:     8.0,
		Timestamp: time.Now().Unix(),
	}

	sendReport(context.Background(), server.Client(), server.URL, "test-vehicle", report, s)

	assert.Equal(t, int64(1), s.succeeded.Load())
	assert.Equal(t, int64(0), s.failed.Load())
	assert.Greater(t, s.totalMS.Load(), int64(-1))
}

func TestSendReport_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid vehicle_id"}`))
	}))
	defer server.Close()

	s := &stats{}
	report := &locationReport{
		VehicleID: "test-vehicle",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Timestamp: time.Now().Unix(),
	}

	sendReport(context.Background(), server.Client(), server.URL, "test-vehicle", report, s)

	assert.Equal(t, int64(0), s.succeeded.Load())
	assert.Equal(t, int64(1), s.failed.Load())
}

func TestSendReport_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s := &stats{}
	report := &locationReport{
		VehicleID: "test-vehicle",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Timestamp: time.Now().Unix(),
	}

	sendReport(ctx, http.DefaultClient, "http://localhost:99999", "test-vehicle", report, s)

	// Cancelled context should not count as a failure
	assert.Equal(t, int64(0), s.succeeded.Load())
	assert.Equal(t, int64(0), s.failed.Load())
}

func TestSimulateVehicle(t *testing.T) {
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	route := []Waypoint{
		{Lat: -1.2864, Lon: 36.8172},
		{Lat: -1.2833, Lon: 36.8158},
	}

	s := &stats{}
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	simulateVehicle(ctx, server.Client(), server.URL, "test-sim", route, 100*time.Millisecond, s)

	assert.Eventually(t, func() bool {
		return s.succeeded.Load() >= 2
	}, time.Second, 10*time.Millisecond, "expected at least 2 successful requests")
	assert.Equal(t, int64(0), s.failed.Load())
	assert.Greater(t, received.Load(), int64(1))
}
