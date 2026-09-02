package main

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func fixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	ix, err := rider.LoadIndex(t.Context(), "../../rider/testdata/fixture.zip", nil, time.Now())
	require.NoError(t, err)
	return ix
}

func TestJitterAndOffset(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	base := rider.LatLon{Lat: 47.6, Lon: -122.33}
	var sum float64
	for i := 0; i < 1000; i++ {
		d := rider.Distance(base, jitter(base, 8, rng))
		assert.Less(t, d, 40.0)
		sum += d
	}
	assert.Less(t, sum/1000, 12.0)
	assert.InDelta(t, 300, rider.Distance(base, offsetEast(base, 300)), 1)
}

func TestPickTrips(t *testing.T) {
	ix := fixtureIndex(t)
	got, err := pickTrips(ix, []string{"T1"}, 0, "20260902")
	require.NoError(t, err)
	assert.Equal(t, []string{"T1"}, got)
	_, err = pickTrips(ix, []string{"T2"}, 0, "20260902")
	assert.Error(t, err, "Saturday-only trip on a Wednesday")
	_, err = pickTrips(ix, []string{"NOPE"}, 0, "20260902")
	assert.Error(t, err)
	got, err = pickTrips(ix, nil, 2, "20260902")
	require.NoError(t, err)
	assert.Len(t, got, 2)
	_, err = pickTrips(ix, nil, 1, "20271231")
	assert.Error(t, err, "outside calendar range: nothing active")
}
