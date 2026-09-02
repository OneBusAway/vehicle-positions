package rider

import "math"

const (
	// earthRadiusM is the mean Earth radius used by the haversine formula.
	earthRadiusM = 6_371_000.0
	// metresPerDegree is the local equirectangular scale factor.
	metresPerDegree = 111_320.0
	// hintCandidateBand is the minimum width, in metres, of the band of local
	// minima a hint may choose between. It lets a hint choose between passes of
	// a loop that share a point, where the closest distance is near zero and a
	// purely proportional band would admit only the one pass.
	hintCandidateBand = 30.0
	// minCosLat floors the cosine of the latitude in OffsetMetres, so a point
	// at a pole does not divide by zero.
	minCosLat = 1e-6
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

// OffsetMetres displaces p by north metres of latitude and east metres of
// longitude, using the same local equirectangular scale the shape projection
// does. It is the inverse of the local plane: metres in, degrees out.
func OffsetMetres(p LatLon, north, east float64) LatLon {
	cosLat := math.Cos(rad(p.Lat))
	if cosLat < minCosLat {
		cosLat = minCosLat
	}
	return LatLon{
		Lat: p.Lat + north/metresPerDegree,
		Lon: p.Lon + east/(metresPerDegree*cosLat),
	}
}

// Projection is the result of projecting a point onto a shape.
type Projection struct {
	AlongShape      float64 // metres from the shape start
	DistanceToShape float64 // metres from the point to the closest point on the shape
}

// ShapeGeom is a polyline with precomputed cumulative distances.
type ShapeGeom struct {
	Points     []LatLon
	Cumulative []float64 // Cumulative[i] = metres from Points[0] to Points[i]
	Length     float64

	origin LatLon  // local-projection origin, Points[0]
	cosLat float64 // cosine of the mean latitude
	// localX and localY are Points projected onto the local plane once, at
	// construction: Project runs on every rider point, and re-deriving the
	// vertices from degrees on each call was the bulk of its work.
	localX []float64
	localY []float64
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

	s.localX = make([]float64, len(points))
	s.localY = make([]float64, len(points))
	for i, p := range s.Points {
		s.localX[i], s.localY[i] = s.local(p)
	}
	return s
}

// Project returns the position of p along the shape. Distances are measured in
// a local equirectangular projection. When hint is nil the globally closest
// segment wins, found in one pass with nothing allocated — this is the hot
// path, run for every point of every batch. With a hint the closest local
// minimum to the hinted distance along the shape wins instead, which keeps
// loops and out-and-backs from snapping to the wrong pass; that needs every
// segment's distance at once, so it keeps the two slices.
func (s *ShapeGeom) Project(p LatLon, hint *float64) Projection {
	segments := len(s.Points) - 1
	px, py := s.local(p)

	if hint == nil {
		bestDist, bestAlong := math.Inf(1), 0.0
		for i := range segments {
			if d, along := s.projectOnto(i, px, py); d < bestDist {
				bestDist, bestAlong = d, along
			}
		}
		return Projection{AlongShape: bestAlong, DistanceToShape: bestDist}
	}

	dists := make([]float64, segments)
	alongs := make([]float64, segments)
	best := 0
	for i := range segments {
		dists[i], alongs[i] = s.projectOnto(i, px, py)
		if dists[i] < dists[best] {
			best = i
		}
	}

	chosen := best
	threshold := math.Max(2*dists[best]+1, hintCandidateBand)
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
	return Projection{AlongShape: alongs[chosen], DistanceToShape: dists[chosen]}
}

// projectOnto returns the distance from the local-plane point (px, py) to
// segment i, and the along-shape distance of the closest point on it.
func (s *ShapeGeom) projectOnto(i int, px, py float64) (dist, along float64) {
	ax, ay := s.localX[i], s.localY[i]
	dx, dy := s.localX[i+1]-ax, s.localY[i+1]-ay

	t := 0.0
	if lenSq := dx*dx + dy*dy; lenSq > 0 {
		t = math.Min(1, math.Max(0, ((px-ax)*dx+(py-ay)*dy)/lenSq))
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy)), s.Cumulative[i] + t*(s.Cumulative[i+1]-s.Cumulative[i])
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
