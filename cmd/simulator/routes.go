package main

import "math"

// Waypoint represents a GPS coordinate on a route.
type Waypoint struct {
	Lat float64
	Lon float64
}

// Predefined routes using real Nairobi coordinates.
var routes = [][]Waypoint{
	// Route 1: Nairobi CBD → Westlands (along Uhuru Highway / Waiyaki Way)
	{
		{Lat: -1.2864, Lon: 36.8172}, // Kencom / CBD
		{Lat: -1.2833, Lon: 36.8158}, // Uhuru Highway
		{Lat: -1.2762, Lon: 36.8098}, // Museum Hill
		{Lat: -1.2690, Lon: 36.8065}, // Chiromo
		{Lat: -1.2638, Lon: 36.8028}, // Westlands
	},
	// Route 2: CBD → Eastlands (along Jogoo Road)
	{
		{Lat: -1.2864, Lon: 36.8172}, // Kencom / CBD
		{Lat: -1.2878, Lon: 36.8250}, // Muthurwa
		{Lat: -1.2900, Lon: 36.8350}, // Shauri Moyo
		{Lat: -1.2920, Lon: 36.8450}, // Makadara
		{Lat: -1.2945, Lon: 36.8550}, // Jogoo Road / Eastlands
	},
	// Route 3: CBD → Karen (along Ngong Road)
	{
		{Lat: -1.2864, Lon: 36.8172}, // Kencom / CBD
		{Lat: -1.2950, Lon: 36.8100}, // Upper Hill
		{Lat: -1.3020, Lon: 36.7950}, // Ngong Road
		{Lat: -1.3100, Lon: 36.7800}, // Prestige / Junction
		{Lat: -1.3180, Lon: 36.7650}, // Karen
	},
}

const earthRadiusMeters = 6371000

func toRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

func toDegrees(rad float64) float64 {
	return rad * 180 / math.Pi
}

// bearing calculates the forward azimuth (in degrees, 0-360) from point a to point b.
func bearing(a, b Waypoint) float64 {
	lat1 := toRadians(a.Lat)
	lat2 := toRadians(b.Lat)
	dLon := toRadians(b.Lon - a.Lon)

	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)

	brng := toDegrees(math.Atan2(y, x))
	return math.Mod(brng+360, 360)
}

// haversineDistance returns the distance in meters between two waypoints.
func haversineDistance(a, b Waypoint) float64 {
	lat1 := toRadians(a.Lat)
	lat2 := toRadians(b.Lat)
	dLat := toRadians(b.Lat - a.Lat)
	dLon := toRadians(b.Lon - a.Lon)

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))

	return earthRadiusMeters * c
}

// speed calculates speed in meters/second given distance and interval duration.
func speed(distMeters float64, intervalSeconds float64) float64 {
	if intervalSeconds <= 0 {
		return 0
	}
	return distMeters / intervalSeconds
}

// interpolate returns a waypoint that is fraction t (0.0–1.0) of the way from a to b.
func interpolate(a, b Waypoint, t float64) Waypoint {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	return Waypoint{
		Lat: a.Lat + (b.Lat-a.Lat)*t,
		Lon: a.Lon + (b.Lon-a.Lon)*t,
	}
}
