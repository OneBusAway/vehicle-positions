package rider

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// straightShape runs due north for ~1 km from (47.6000, -122.3300).
func straightShape() *ShapeGeom {
	return NewShapeGeom([]LatLon{
		{47.6000, -122.3300},
		{47.6045, -122.3300},
		{47.6090, -122.3300},
	})
}

// loopShape is a square loop: north, east, south, back west to the start.
func loopShape() *ShapeGeom {
	return NewShapeGeom([]LatLon{
		{47.6000, -122.3300},
		{47.6045, -122.3300}, // north 500 m
		{47.6045, -122.3234}, // east ~500 m
		{47.6000, -122.3234}, // south 500 m
		{47.6000, -122.3300}, // west back to start
	})
}

func TestDistance_Haversine(t *testing.T) {
	d := Distance(LatLon{47.6000, -122.3300}, LatLon{47.6090, -122.3300})
	assert.InDelta(t, 1001, d, 5) // 0.009° latitude ≈ 1001 m
	assert.Equal(t, 0.0, Distance(LatLon{1, 1}, LatLon{1, 1}))
}

func TestInitialBearing(t *testing.T) {
	assert.InDelta(t, 0, InitialBearing(LatLon{47.6, -122.33}, LatLon{47.61, -122.33}), 0.5)
	assert.InDelta(t, 90, InitialBearing(LatLon{47.6, -122.33}, LatLon{47.6, -122.32}), 1)
	assert.InDelta(t, 180, InitialBearing(LatLon{47.61, -122.33}, LatLon{47.6, -122.33}), 0.5)
}

func TestNewShapeGeom_Cumulative(t *testing.T) {
	s := straightShape()
	require.Len(t, s.Cumulative, 3)
	assert.Equal(t, 0.0, s.Cumulative[0])
	assert.InDelta(t, 500, s.Cumulative[1], 3)
	assert.InDelta(t, 1001, s.Length, 5)
	assert.Panics(t, func() { NewShapeGeom([]LatLon{{1, 1}}) })
}

func TestProject_OnShape(t *testing.T) {
	s := straightShape()
	p := s.Project(LatLon{47.6045, -122.3300}, nil)
	assert.InDelta(t, 500, p.AlongShape, 3)
	assert.InDelta(t, 0, p.DistanceToShape, 0.5)
	assert.InDelta(t, 0, s.BearingAt(p.AlongShape), 1)
}

func TestProject_OffShape(t *testing.T) {
	s := straightShape()
	// 0.001° longitude at 47.6° ≈ 75 m east of the line, 250 m along.
	p := s.Project(LatLon{47.60225, -122.3290}, nil)
	assert.InDelta(t, 250, p.AlongShape, 5)
	assert.InDelta(t, 75, p.DistanceToShape, 3)
}

func TestProject_BeyondEnds_Clamps(t *testing.T) {
	s := straightShape()
	before := s.Project(LatLon{47.5990, -122.3300}, nil)
	assert.InDelta(t, 0, before.AlongShape, 0.01)
	assert.InDelta(t, 111, before.DistanceToShape, 3)
	after := s.Project(LatLon{47.6100, -122.3300}, nil)
	assert.InDelta(t, s.Length, after.AlongShape, 0.01)
}

func TestProject_LoopUsesHint(t *testing.T) {
	s := loopShape()
	// The start/end corner is equidistant from the first and last segments.
	nearStart := LatLon{47.6001, -122.3299}
	first := s.Project(nearStart, nil)
	assert.Less(t, first.AlongShape, 50.0, "without a hint the global/first minimum wins")

	hint := s.Length - 30
	last := s.Project(nearStart, &hint)
	assert.Greater(t, last.AlongShape, s.Length-60, "with a late hint the last segment wins")

	// A hint far from any local minimum still returns a valid local minimum.
	mid := 1000.0
	p := s.Project(nearStart, &mid)
	assert.True(t, p.AlongShape < 50 || p.AlongShape > s.Length-60)
}

func TestPointAt_And_BearingAt(t *testing.T) {
	s := loopShape()
	p := s.PointAt(250)
	assert.InDelta(t, 47.60225, p.Lat, 0.0001)
	assert.InDelta(t, -122.3300, p.Lon, 0.0001)
	assert.InDelta(t, 0, s.BearingAt(250), 1)
	assert.InDelta(t, 90, s.BearingAt(s.Cumulative[1]+100), 2)
	assert.InDelta(t, 180, s.BearingAt(s.Cumulative[2]+100), 1)

	// Clamping.
	assert.Equal(t, s.Points[0], s.PointAt(-5))
	assert.Equal(t, s.Points[len(s.Points)-1], s.PointAt(s.Length+5))

	// Round trip: PointAt(along) projects back to ~along.
	for _, along := range []float64{10, 400, 900, 1400} {
		q := s.Project(s.PointAt(along), &along)
		assert.InDelta(t, along, q.AlongShape, 1, "along=%v", along)
		assert.False(t, math.IsNaN(q.DistanceToShape))
	}
}

func TestProject_SharedLoopPoint_HintPicksLastPass(t *testing.T) {
	s := loopShape()
	// The loop starts and ends at this exact point, so both passes are 0 m away
	// and only the hint can tell them apart.
	shared := LatLon{47.6000, -122.3300}

	unhinted := s.Project(shared, nil)
	assert.Less(t, unhinted.AlongShape, 1.0, "without a hint the first pass wins")

	hint := s.Length - 30
	hinted := s.Project(shared, &hint)
	assert.Greater(t, hinted.AlongShape, s.Length-60, "with a late hint the last pass wins")

	// Near, but not on, the shared corner: ~5 m from the first pass and ~15 m
	// from the last. A purely proportional band (2*5+1 = 11 m) would exclude the
	// last pass outright; hintCandidateBand keeps it in play for the hint.
	nearCorner := LatLon{47.6001347, -122.3299334}
	assert.Less(t, s.Project(nearCorner, nil).AlongShape, 50.0)
	offset := s.Project(nearCorner, &hint)
	assert.Greater(t, offset.AlongShape, s.Length-60, "the band must be wide enough to reach the last pass")
	assert.InDelta(t, 15, offset.DistanceToShape, 2)
}
