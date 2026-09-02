package rider

import "math"

const (
	// earthRadiusM is the mean Earth radius used by the haversine formula.
	earthRadiusM = 6_371_000.0
	// metresPerDegree is the local equirectangular scale factor.
	metresPerDegree = 111_320.0
)

// LatLon is a WGS84 coordinate in degrees.
type LatLon struct{ Lat, Lon float64 }

// Distance returns the great-circle distance between a and b in metres.
func Distance(a, b LatLon) float64 {
	lat1, lat2 := rad(a.Lat), rad(b.Lat)
	dLat := lat2 - lat1
	dLon := rad(b.Lon - a.Lon)

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// InitialBearing returns the initial great-circle bearing from a to b in
// degrees clockwise from north, in [0, 360).
func InitialBearing(a, b LatLon) float64 {
	lat1, lat2 := rad(a.Lat), rad(b.Lat)
	dLon := rad(b.Lon - a.Lon)

	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)
	deg := math.Atan2(y, x) * 180 / math.Pi
	return math.Mod(deg+360, 360)
}

// Projection is the result of projecting a point onto a shape.
type Projection struct {
	AlongShape      float64 // metres from the shape start
	DistanceToShape float64 // metres from the point to the closest point on the shape
	SegmentBearing  float64 // bearing of the segment the point projected onto
}

// ShapeGeom is a polyline with precomputed cumulative distances.
type ShapeGeom struct {
	Points     []LatLon
	Cumulative []float64 // Cumulative[i] = metres from Points[0] to Points[i]
	Length     float64

	origin LatLon  // local-projection origin, Points[0]
	cosLat float64 // cosine of the mean latitude
}

// NewShapeGeom builds a ShapeGeom from points. It panics on fewer than two points.
func NewShapeGeom(points []LatLon) *ShapeGeom {
	if len(points) < 2 {
		panic("rider: NewShapeGeom requires at least 2 points")
	}

	s := &ShapeGeom{
		Points:     append([]LatLon(nil), points...),
		Cumulative: make([]float64, len(points)),
		origin:     points[0],
	}

	sumLat := 0.0
	for _, p := range points {
		sumLat += p.Lat
	}
	s.cosLat = math.Cos(rad(sumLat / float64(len(points))))

	for i := 1; i < len(points); i++ {
		s.Cumulative[i] = s.Cumulative[i-1] + Distance(points[i-1], points[i])
	}
	s.Length = s.Cumulative[len(points)-1]
	return s
}

// Project returns the position of p along the shape. Distances are measured in
// a local equirectangular projection. When hint is nil the globally closest
// segment wins; otherwise the closest local minimum to the hinted distance
// along the shape wins, which keeps loops and out-and-backs from snapping to
// the wrong pass.
func (s *ShapeGeom) Project(p LatLon, hint *float64) Projection {
	segments := len(s.Points) - 1
	dists := make([]float64, segments)
	alongs := make([]float64, segments)

	px, py := s.local(p)
	best := 0
	for i := range segments {
		ax, ay := s.local(s.Points[i])
		bx, by := s.local(s.Points[i+1])
		dx, dy := bx-ax, by-ay

		t := 0.0
		if lenSq := dx*dx + dy*dy; lenSq > 0 {
			t = math.Min(1, math.Max(0, ((px-ax)*dx+(py-ay)*dy)/lenSq))
		}

		dists[i] = math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
		alongs[i] = s.Cumulative[i] + t*(s.Cumulative[i+1]-s.Cumulative[i])
		if dists[i] < dists[best] {
			best = i
		}
	}

	chosen := best
	if hint != nil {
		threshold := 2*dists[best] + 1
		closest := math.Inf(1)
		for i := range segments {
			if dists[i] > threshold || !isLocalMin(dists, i) {
				continue
			}
			if delta := math.Abs(alongs[i] - *hint); delta < closest {
				closest = delta
				chosen = i
			}
		}
	}

	return Projection{
		AlongShape:      alongs[chosen],
		DistanceToShape: dists[chosen],
		SegmentBearing:  InitialBearing(s.Points[chosen], s.Points[chosen+1]),
	}
}

// PointAt returns the coordinate `along` metres into the shape, clamped to
// [0, Length].
func (s *ShapeGeom) PointAt(along float64) LatLon {
	i, t := s.segmentAt(along)
	a, b := s.Points[i], s.Points[i+1]
	return LatLon{
		Lat: a.Lat + t*(b.Lat-a.Lat),
		Lon: a.Lon + t*(b.Lon-a.Lon),
	}
}

// BearingAt returns the bearing of the segment containing `along`.
func (s *ShapeGeom) BearingAt(along float64) float64 {
	i, _ := s.segmentAt(along)
	return InitialBearing(s.Points[i], s.Points[i+1])
}

// segmentAt returns the index of the segment containing `along` and the
// fraction into that segment, clamping `along` to [0, Length].
func (s *ShapeGeom) segmentAt(along float64) (int, float64) {
	last := len(s.Points) - 2
	if along >= s.Length {
		return last, 1
	}
	if along <= 0 {
		return 0, 0
	}
	for i := 0; i <= last; i++ {
		if along < s.Cumulative[i+1] {
			segLen := s.Cumulative[i+1] - s.Cumulative[i]
			if segLen <= 0 {
				return i, 0
			}
			return i, (along - s.Cumulative[i]) / segLen
		}
	}
	return last, 1
}

// local converts p to metres in the shape's equirectangular projection.
func (s *ShapeGeom) local(p LatLon) (x, y float64) {
	return (p.Lon - s.origin.Lon) * s.cosLat * metresPerDegree,
		(p.Lat - s.origin.Lat) * metresPerDegree
}

// isLocalMin reports whether dists[i] is no greater than its neighbours.
func isLocalMin(dists []float64, i int) bool {
	if i > 0 && dists[i] > dists[i-1] {
		return false
	}
	if i < len(dists)-1 && dists[i] > dists[i+1] {
		return false
	}
	return true
}

func rad(deg float64) float64 { return deg * math.Pi / 180 }
