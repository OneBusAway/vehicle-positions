package gtfs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OneBusAway/vehicle-positions/gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestParseStops_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stops.txt",
		"stop_id,stop_name,stop_lat,stop_lon\n"+
			"S1,Stop One,1.5,2.5\n"+
			"S2,Stop Two,-33.8688,151.2093\n")

	stops, err := gtfs.ParseStops(filepath.Join(dir, "stops.txt"))
	require.NoError(t, err)
	require.Len(t, stops, 2)

	assert.Equal(t, "S1", stops[0].StopID)
	assert.Equal(t, "Stop One", stops[0].Name)
	assert.InDelta(t, 1.5, stops[0].Lat, 0.0001)
	assert.InDelta(t, 2.5, stops[0].Lon, 0.0001)

	assert.Equal(t, "S2", stops[1].StopID)
	assert.InDelta(t, -33.8688, stops[1].Lat, 0.0001)
}

func TestParseStops_ExtraColumns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stops.txt",
		"stop_id,stop_code,stop_name,stop_lat,stop_lon,zone_id\n"+
			"S1,01,Stop One,1.0,2.0,Z1\n")

	stops, err := gtfs.ParseStops(filepath.Join(dir, "stops.txt"))
	require.NoError(t, err)
	require.Len(t, stops, 1)
	assert.Equal(t, "S1", stops[0].StopID)
}

func TestParseStops_MissingRequiredColumn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stops.txt",
		"stop_id,stop_name,stop_lat\n"+
			"S1,Stop One,1.0\n")

	_, err := gtfs.ParseStops(filepath.Join(dir, "stops.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop_lon")
}

func TestParseStops_InvalidLatitude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stops.txt",
		"stop_id,stop_name,stop_lat,stop_lon\n"+
			"S1,Stop One,not_a_number,2.0\n")

	_, err := gtfs.ParseStops(filepath.Join(dir, "stops.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop_lat")
}

func TestParseStops_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stops.txt", "stop_id,stop_name,stop_lat,stop_lon\n")

	stops, err := gtfs.ParseStops(filepath.Join(dir, "stops.txt"))
	require.NoError(t, err)
	assert.Empty(t, stops)
}

func TestParseStops_FileNotFound(t *testing.T) {
	_, err := gtfs.ParseStops("/nonexistent/path/stops.txt")
	require.Error(t, err)
}

func TestParseRoutes_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.txt",
		"route_id,route_short_name,route_long_name\n"+
			"R1,1,Route One\n"+
			"R2,2,Route Two\n")

	routes, err := gtfs.ParseRoutes(filepath.Join(dir, "routes.txt"))
	require.NoError(t, err)
	require.Len(t, routes, 2)

	assert.Equal(t, "R1", routes[0].RouteID)
	assert.Equal(t, "1", routes[0].ShortName)
	assert.Equal(t, "Route One", routes[0].LongName)
}

func TestParseRoutes_MissingRequiredColumn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.txt",
		"route_id,route_short_name\n"+
			"R1,1\n")

	_, err := gtfs.ParseRoutes(filepath.Join(dir, "routes.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "route_long_name")
}

func TestParseRoutes_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.txt", "route_id,route_short_name,route_long_name\n")

	routes, err := gtfs.ParseRoutes(filepath.Join(dir, "routes.txt"))
	require.NoError(t, err)
	assert.Empty(t, routes)
}

func TestParseTrips_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "trips.txt",
		"trip_id,route_id,service_id\n"+
			"T1,R1,WD\n"+
			"T2,R2,WE\n")

	trips, err := gtfs.ParseTrips(filepath.Join(dir, "trips.txt"))
	require.NoError(t, err)
	require.Len(t, trips, 2)

	assert.Equal(t, "T1", trips[0].TripID)
	assert.Equal(t, "R1", trips[0].RouteID)
	assert.Equal(t, "WD", trips[0].ServiceID)
}

func TestParseTrips_MissingRequiredColumn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "trips.txt",
		"trip_id,route_id\n"+
			"T1,R1\n")

	_, err := gtfs.ParseTrips(filepath.Join(dir, "trips.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service_id")
}

func TestParseStopTimes_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id,stop_sequence\n"+
			"T1,08:00:00,08:00:00,S1,1\n"+
			"T1,08:10:00,08:11:00,S2,2\n")

	sts, err := gtfs.ParseStopTimes(filepath.Join(dir, "stop_times.txt"))
	require.NoError(t, err)
	require.Len(t, sts, 2)

	assert.Equal(t, "T1", sts[0].TripID)
	assert.Equal(t, "08:00:00", sts[0].ArrivalTime)
	assert.Equal(t, "08:00:00", sts[0].DepartureTime)
	assert.Equal(t, "S1", sts[0].StopID)
	assert.Equal(t, 1, sts[0].StopSequence)

	assert.Equal(t, 2, sts[1].StopSequence)
}

func TestParseStopTimes_InvalidSequence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id,stop_sequence\n"+
			"T1,08:00:00,08:00:00,S1,not_a_number\n")

	_, err := gtfs.ParseStopTimes(filepath.Join(dir, "stop_times.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop_sequence")
}

func TestParseStopTimes_MissingRequiredColumn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id\n"+
			"T1,08:00:00,08:00:00,S1\n")

	_, err := gtfs.ParseStopTimes(filepath.Join(dir, "stop_times.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop_sequence")
}

func TestParseStopTimes_ExtraColumns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id,stop_sequence,pickup_type,drop_off_type\n"+
			"T1,08:00:00,08:00:00,S1,1,0,0\n")

	sts, err := gtfs.ParseStopTimes(filepath.Join(dir, "stop_times.txt"))
	require.NoError(t, err)
	require.Len(t, sts, 1)
	assert.Equal(t, 1, sts[0].StopSequence)
}
