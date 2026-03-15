package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gtfslocal "github.com/OneBusAway/vehicle-positions/gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockGTFSStore struct {
	importGTFSErr error

	stops     []gtfslocal.Stop
	routes    []gtfslocal.Route
	trips     []gtfslocal.Trip
	stopTimes []gtfslocal.StopTime
}

func (m *mockGTFSStore) ImportGTFS(
	ctx context.Context,
	stops []gtfslocal.Stop,
	routes []gtfslocal.Route,
	trips []gtfslocal.Trip,
	stopTimes []gtfslocal.StopTime,
) error {
	if m.importGTFSErr != nil {
		return m.importGTFSErr
	}
	m.stops = stops
	m.routes = routes
	m.trips = trips
	m.stopTimes = stopTimes
	return nil
}

type gtfsFileSpec struct {
	name    string
	content string
}

func buildGTFSZip(t *testing.T, files []gtfsFileSpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.name)
		require.NoError(t, err)
		_, err = io.WriteString(w, f.content)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func minimalGTFS(t *testing.T) []byte {
	t.Helper()
	return buildGTFSZip(t, []gtfsFileSpec{
		{
			name:    "stops.txt",
			content: "stop_id,stop_name,stop_lat,stop_lon\nS1,Stop One,1.0,2.0\nS2,Stop Two,3.0,4.0\n",
		},
		{
			name:    "routes.txt",
			content: "route_id,route_short_name,route_long_name\nR1,1,Route One\n",
		},
		{
			name:    "trips.txt",
			content: "trip_id,route_id,service_id\nT1,R1,WD\n",
		},
		{
			name:    "stop_times.txt",
			content: "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,S1,1\nT1,08:10:00,08:10:00,S2,2\n",
		},
	})
}

func postGTFSUpload(t *testing.T, handler http.HandlerFunc, zipData []byte, filename string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write(zipData)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/gtfs/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleUploadGTFS_HappyPath(t *testing.T) {
	store := &mockGTFSStore{}
	handler := handleUploadGTFS(store)
	w := postGTFSUpload(t, handler, minimalGTFS(t), "gtfs.zip")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var result gtfslocal.ImportResult
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, 2, result.Stops)
	assert.Equal(t, 1, result.Routes)
	assert.Equal(t, 1, result.Trips)
	assert.Equal(t, 2, result.StopTimes)

	require.Len(t, store.stops, 2)
	assert.Equal(t, "S1", store.stops[0].StopID)
	assert.Equal(t, "Stop One", store.stops[0].Name)
	assert.InDelta(t, 1.0, store.stops[0].Lat, 0.001)
	assert.InDelta(t, 2.0, store.stops[0].Lon, 0.001)

	require.Len(t, store.routes, 1)
	assert.Equal(t, "R1", store.routes[0].RouteID)
	assert.Equal(t, "1", store.routes[0].ShortName)
	assert.Equal(t, "Route One", store.routes[0].LongName)

	require.Len(t, store.trips, 1)
	assert.Equal(t, "T1", store.trips[0].TripID)
	assert.Equal(t, "R1", store.trips[0].RouteID)
	assert.Equal(t, "WD", store.trips[0].ServiceID)

	require.Len(t, store.stopTimes, 2)
	assert.Equal(t, "T1", store.stopTimes[0].TripID)
	assert.Equal(t, "S1", store.stopTimes[0].StopID)
	assert.Equal(t, 1, store.stopTimes[0].StopSequence)
}

func TestHandleUploadGTFS_MissingFileField(t *testing.T) {
	handler := handleUploadGTFS(&mockGTFSStore{})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/gtfs/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "file")
}

func TestHandleUploadGTFS_WrongExtension(t *testing.T) {
	handler := handleUploadGTFS(&mockGTFSStore{})
	w := postGTFSUpload(t, handler, []byte("not a zip"), "gtfs.txt")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], ".zip")
}

func TestHandleUploadGTFS_InvalidZip(t *testing.T) {
	handler := handleUploadGTFS(&mockGTFSStore{})
	w := postGTFSUpload(t, handler, []byte("this is not zip content"), "gtfs.zip")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "unzip")
}

func TestHandleUploadGTFS_MissingStopsFile(t *testing.T) {
	handler := handleUploadGTFS(&mockGTFSStore{})
	zipData := buildGTFSZip(t, []gtfsFileSpec{
		{name: "routes.txt", content: "route_id,route_short_name,route_long_name\nR1,1,Route One\n"},
		{name: "trips.txt", content: "trip_id,route_id,service_id\nT1,R1,WD\n"},
		{name: "stop_times.txt", content: "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,S1,1\n"},
	})
	w := postGTFSUpload(t, handler, zipData, "gtfs.zip")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "stops.txt")
}

func TestHandleUploadGTFS_MissingRequiredColumn(t *testing.T) {
	handler := handleUploadGTFS(&mockGTFSStore{})
	zipData := buildGTFSZip(t, []gtfsFileSpec{
		{name: "stops.txt", content: "stop_id,stop_name,stop_lat\nS1,Stop One,1.0\n"},
		{name: "routes.txt", content: "route_id,route_short_name,route_long_name\nR1,1,Route One\n"},
		{name: "trips.txt", content: "trip_id,route_id,service_id\nT1,R1,WD\n"},
		{name: "stop_times.txt", content: "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,S1,1\n"},
	})
	w := postGTFSUpload(t, handler, zipData, "gtfs.zip")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "stop_lon")
}

func TestHandleUploadGTFS_StoreError(t *testing.T) {
	store := &mockGTFSStore{importGTFSErr: errors.New("db unavailable")}
	handler := handleUploadGTFS(store)
	w := postGTFSUpload(t, handler, minimalGTFS(t), "gtfs.zip")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "import GTFS data")
}

func TestHandleUploadGTFS_EmptyFiles(t *testing.T) {
	store := &mockGTFSStore{}
	handler := handleUploadGTFS(store)
	zipData := buildGTFSZip(t, []gtfsFileSpec{
		{name: "stops.txt", content: "stop_id,stop_name,stop_lat,stop_lon\n"},
		{name: "routes.txt", content: "route_id,route_short_name,route_long_name\n"},
		{name: "trips.txt", content: "trip_id,route_id,service_id\n"},
		{name: "stop_times.txt", content: "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n"},
	})
	w := postGTFSUpload(t, handler, zipData, "gtfs.zip")

	require.Equal(t, http.StatusOK, w.Code)
	var result gtfslocal.ImportResult
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, 0, result.Stops)
	assert.Equal(t, 0, result.Routes)
	assert.Equal(t, 0, result.Trips)
	assert.Equal(t, 0, result.StopTimes)
}

func TestHandleUploadGTFS_FilenameCaseInsensitiveExtension(t *testing.T) {
	store := &mockGTFSStore{}
	handler := handleUploadGTFS(store)
	w := postGTFSUpload(t, handler, minimalGTFS(t), "gtfs.ZIP")

	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandleUploadGTFS_ResponseJSON(t *testing.T) {
	store := &mockGTFSStore{}
	handler := handleUploadGTFS(store)
	w := postGTFSUpload(t, handler, minimalGTFS(t), "gtfs.zip")

	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&raw))
	assert.Contains(t, raw, "stops_imported", "response must include stops_imported")
	assert.Contains(t, raw, "routes_imported", "response must include routes_imported")
	assert.Contains(t, raw, "trips_imported", "response must include trips_imported")
	assert.Contains(t, raw, "stop_times_imported", "response must include stop_times_imported")
}

func TestHandleUploadGTFS_LargeDataset(t *testing.T) {
	var stops, stopTimes strings.Builder
	stops.WriteString("stop_id,stop_name,stop_lat,stop_lon\n")
	stopTimes.WriteString("trip_id,arrival_time,departure_time,stop_id,stop_sequence\n")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&stops, "S%d,Stop %d,%f,%f\n", i, i, float64(i%90), float64(i%180))
		fmt.Fprintf(&stopTimes, "T1,08:00:00,08:00:00,S%d,%d\n", i, i+1)
	}
	zipData := buildGTFSZip(t, []gtfsFileSpec{
		{name: "stops.txt", content: stops.String()},
		{name: "routes.txt", content: "route_id,route_short_name,route_long_name\nR1,1,Route One\n"},
		{name: "trips.txt", content: "trip_id,route_id,service_id\nT1,R1,WD\n"},
		{name: "stop_times.txt", content: stopTimes.String()},
	})

	store := &mockGTFSStore{}
	handler := handleUploadGTFS(store)
	w := postGTFSUpload(t, handler, zipData, "gtfs.zip")

	require.Equal(t, http.StatusOK, w.Code)
	var result gtfslocal.ImportResult
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, 1000, result.Stops)
	assert.Equal(t, 1000, result.StopTimes)
}
