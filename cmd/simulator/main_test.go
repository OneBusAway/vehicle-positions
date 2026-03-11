package main

import (
	"math"
	"testing"

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

func TestBuildReport(t *testing.T) {
	from := Waypoint{Lat: -1.2864, Lon: 36.8172}
	to := Waypoint{Lat: -1.2833, Lon: 36.8158}

	pos := interpolate(from, to, 0.5)
	brng := bearing(from, to)
	spd := speed(haversineDistance(from, to), 10.0)

	report := locationReport{
		VehicleID: "sim-vehicle-001",
		Latitude:  pos.Lat,
		Longitude: pos.Lon,
		Bearing:   brng,
		Speed:     spd,
		Timestamp: 1752566400,
	}

	assert.Equal(t, "sim-vehicle-001", report.VehicleID)
	assert.InDelta(t, -1.28485, report.Latitude, 0.001)
	assert.InDelta(t, 36.8165, report.Longitude, 0.001)
	assert.Greater(t, report.Bearing, 0.0)
	assert.Less(t, report.Bearing, 360.0)
	assert.Greater(t, report.Speed, 0.0)
	assert.Equal(t, int64(1752566400), report.Timestamp)
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
