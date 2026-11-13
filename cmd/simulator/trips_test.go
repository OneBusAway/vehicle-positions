package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var noon = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// tripAt is a trip whose times are given in minutes after noon.
func tripAt(id string, starts, ends int, first, last string) scheduledTrip {
	return scheduledTrip{
		ID:        id,
		StartsAt:  noon.Add(time.Duration(starts) * time.Minute),
		EndsAt:    noon.Add(time.Duration(ends) * time.Minute),
		FirstStop: first,
		LastStop:  last,
	}
}

func TestPickTrip(t *testing.T) {
	// A route between Centre and Terminal, ordered by departure as the server
	// lists it.
	trips := []scheduledTrip{
		tripAt("out-1", -60, -30, "Centre", "Terminal"),
		tripAt("out-2", -15, 15, "Centre", "Terminal"),
		tripAt("back-1", -10, 20, "Terminal", "Centre"),
		tripAt("back-2", 5, 35, "Terminal", "Centre"),
		tripAt("out-3", 30, 60, "Centre", "Terminal"),
	}

	tests := []struct {
		name      string
		trips     []scheduledTrip
		now       time.Time
		from      string
		wantID    string
		wantShift time.Duration
	}{
		{
			name:  "a first trip joins the one that left most recently",
			trips: trips, now: noon, wantID: "back-1",
		},
		{
			name:  "a vehicle at a terminal waits for the next trip that leaves from it",
			trips: trips, now: noon, from: "Terminal", wantID: "back-2",
		},
		{
			name:  "a trip leaving exactly at the layover limit is still waited for",
			trips: trips, now: noon.Add(15 * time.Minute), from: "Centre", wantID: "out-3",
		},
		{
			name:  "a trip leaving after the layover limit is not waited for",
			trips: trips, now: noon.Add(15*time.Minute - time.Second), from: "Centre", wantID: "back-2",
		},
		{
			name:  "a trip that leaves at this instant counts as running",
			trips: trips, now: noon.Add(5 * time.Minute), wantID: "back-2",
		},
		{
			name:  "a trip is over at the instant it ends",
			trips: trips[:1], now: noon.Add(-30 * time.Minute), wantID: "out-1", wantShift: 30 * time.Minute,
		},
		{
			name:  "with nothing running the next trip to leave is driven now",
			trips: trips, now: noon.Add(-2 * time.Hour), wantID: "out-1", wantShift: -time.Hour,
		},
		{
			name:  "with the day's service over its first trip is driven now",
			trips: trips, now: noon.Add(3 * time.Hour), wantID: "out-1", wantShift: 4 * time.Hour,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, shift := pickTrip(tc.trips, tc.now, tc.from)
			assert.Equal(t, tc.wantID, got.ID)
			assert.Equal(t, tc.wantShift, shift)
		})
	}
}

func TestTripClaimsKeepVehiclesOnDifferentTrips(t *testing.T) {
	trips := []scheduledTrip{
		tripAt("early", -20, 10, "Centre", "Terminal"),
		tripAt("late", -5, 25, "Centre", "Terminal"),
	}
	claims := newTripClaims()

	first, _ := claims.claim(trips, noon, "")
	second, _ := claims.claim(trips, noon, "")
	assert.Equal(t, "late", first.ID)
	assert.Equal(t, "early", second.ID, "the second vehicle must not join the trip the first is driving")

	// More vehicles than trips: the third shares one rather than having none.
	third, _ := claims.claim(trips, noon, "")
	assert.Equal(t, "late", third.ID)

	// "late" is still driven by one vehicle after the other leaves it.
	claims.release("late")
	fourth, _ := claims.claim(trips[1:], noon, "")
	assert.Equal(t, "late", fourth.ID)
	claims.release("late")
	claims.release("late")
	claims.release("early")
	assert.Empty(t, claims.taken)

	free, _ := claims.claim(trips, noon, "")
	assert.Equal(t, "late", free.ID, "a released trip can be claimed again")
}

// straightTrip is the detail of a trip up a 1000 m line due north: it leaves at
// departs, dwells at a stop 600 m along from 60 s to 80 s, and arrives at 120 s.
func straightTrip(departs time.Time) tripDetail {
	var d tripDetail
	d.Shape.Points = [][2]float64{{0, 36.8}, {0.0089932, 36.8}}
	stop := func(along float64, arrive, leave time.Duration) {
		d.Stops = append(d.Stops, struct {
			AlongShapeM float64   `json:"along_shape_m"`
			ArrivalAt   time.Time `json:"arrival_at"`
			DepartureAt time.Time `json:"departure_at"`
		}{along, departs.Add(arrive), departs.Add(leave)})
	}
	stop(0, 0, 0)
	stop(600, 60*time.Second, 80*time.Second)
	stop(1000, 120*time.Second, 120*time.Second)
	return d
}

func TestRunPosition(t *testing.T) {
	tests := []struct {
		name      string
		after     time.Duration // since the scheduled departure
		wantAlong float64
		wantSpeed float64
		wantDone  bool
	}{
		{name: "waiting to depart", after: -time.Minute, wantAlong: 0, wantSpeed: 0},
		{name: "at departure", after: 0, wantAlong: 0, wantSpeed: 10},
		{name: "on the first leg", after: 30 * time.Second, wantAlong: 300, wantSpeed: 10},
		{name: "arriving at the stop", after: 60 * time.Second, wantAlong: 600, wantSpeed: 0},
		{name: "dwelling at the stop", after: 70 * time.Second, wantAlong: 600, wantSpeed: 0},
		{name: "on the second leg", after: 100 * time.Second, wantAlong: 800, wantSpeed: 10},
		{name: "a second before arriving", after: 119 * time.Second, wantAlong: 990, wantSpeed: 10},
		{name: "arrived", after: 120 * time.Second, wantAlong: 1000, wantSpeed: 0, wantDone: true},
		{name: "long after arriving", after: time.Hour, wantAlong: 1000, wantSpeed: 0, wantDone: true},
	}
	for _, shift := range []time.Duration{0, 5 * time.Hour, -26 * time.Hour} {
		r, err := newRun(scheduledTrip{ID: "T"}, "20261113", shift, straightTrip(noon))
		require.NoError(t, err)
		assert.InDelta(t, 1000, r.shape.Length, 1)

		for _, tc := range tests {
			t.Run(tc.name+" shifted "+shift.String(), func(t *testing.T) {
				now := noon.Add(shift + tc.after)
				along, speed := r.position(now)
				assert.InDelta(t, tc.wantAlong, along, 1e-6)
				assert.InDelta(t, tc.wantSpeed, speed, 1e-6)
				assert.Equal(t, tc.wantDone, r.finished(now))
			})
		}
	}
}

func TestNewRunRejectsATripWithNothingToDriveAlong(t *testing.T) {
	onePoint := straightTrip(noon)
	onePoint.Shape.Points = onePoint.Shape.Points[:1]
	_, err := newRun(scheduledTrip{ID: "T9"}, "20261113", 0, onePoint)
	assert.ErrorContains(t, err, "trip T9 has 1 shape points")

	noStops := straightTrip(noon)
	noStops.Stops = nil
	_, err = newRun(scheduledTrip{ID: "T9"}, "20261113", 0, noStops)
	assert.ErrorContains(t, err, "0 stops")
}

// TestFetchRouteTripsLooksAheadForService pins why the lookahead exists: a
// route with no service today would otherwise leave its vehicle with no shape.
func TestFetchRouteTripsLooksAheadForService(t *testing.T) {
	var mu sync.Mutex
	var dates []string
	serviceFrom := "20261117"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/gtfs/routes/R 1/trips" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"unknown route"}`))
			return
		}
		date := r.URL.Query().Get("date")
		mu.Lock()
		dates = append(dates, date)
		mu.Unlock()
		if date == "" {
			date = "20261115"
		}
		out := routeTrips{ServiceDate: date, Trips: []scheduledTrip{}}
		if date >= serviceFrom {
			out.Trips = append(out.Trips, tripAt("T-"+date, 0, 10, "A", "B"))
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	got, err := fetchRouteTrips(t.Context(), srv.Client(), srv.URL, "R 1")
	require.NoError(t, err)
	assert.Equal(t, "20261117", got.ServiceDate)
	require.Len(t, got.Trips, 1)
	assert.Equal(t, "T-20261117", got.Trips[0].ID)
	assert.True(t, got.Trips[0].StartsAt.Equal(noon))
	assert.Equal(t, []string{"", "20261116", "20261117"}, dates)

	// Today has service: nothing else is asked for.
	dates, serviceFrom = nil, "20261115"
	got, err = fetchRouteTrips(t.Context(), srv.Client(), srv.URL, "R 1")
	require.NoError(t, err)
	assert.Equal(t, "20261115", got.ServiceDate)
	assert.Equal(t, []string{""}, dates)

	// No service in the week: today and the six days after it were tried.
	dates, serviceFrom = nil, "20261122"
	_, err = fetchRouteTrips(t.Context(), srv.Client(), srv.URL, "R 1")
	assert.ErrorContains(t, err, "route R 1 has no trips from 20261115")
	assert.Equal(t, []string{"", "20261116", "20261117", "20261118", "20261119", "20261120", "20261121"}, dates)

	_, err = fetchRouteTrips(t.Context(), srv.Client(), srv.URL, "R2")
	assert.ErrorContains(t, err, "returned 404")
	assert.ErrorContains(t, err, "unknown route")
}

// fakeServer is the part of the server a vehicle talks to: the schedule of one
// route, and the trip and location endpoints, which it records.
type fakeServer struct {
	*httptest.Server
	trips   []scheduledTrip
	details map[string]tripDetail

	mu          sync.Mutex
	startStatus int
	started     []map[string]string
	ended       []int64
	reports     []locationReport
}

func newFakeServer(t *testing.T, trips []scheduledTrip, details map[string]tripDetail) *fakeServer {
	t.Helper()
	f := &fakeServer{trips: trips, details: details, startStatus: http.StatusCreated}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer driver-token" {
			t.Errorf("%s Authorization = %q, want the driver's token", r.URL.Path, got)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/gtfs/routes/R1/trips":
			json.NewEncoder(w).Encode(routeTrips{ServiceDate: "20261113", Trips: f.trips})
		case strings.HasPrefix(r.URL.Path, "/api/v1/gtfs/trips/"):
			assert.Equal(t, "20261113", r.URL.Query().Get("date"), "the trip is asked for on the date it was listed under")
			json.NewEncoder(w).Encode(f.details[strings.TrimPrefix(r.URL.Path, "/api/v1/gtfs/trips/")])
		case r.URL.Path == "/api/v1/trips/start":
			var body map[string]string
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if f.startStatus != http.StatusCreated {
				w.WriteHeader(f.startStatus)
				w.Write([]byte(`{"error":"driver already has an active trip"}`))
				return
			}
			f.started = append(f.started, body)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"id": len(f.started)})
		case r.URL.Path == "/api/v1/locations":
			var report locationReport
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&report))
			f.reports = append(f.reports, report)
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/api/v1/trips/end":
			var body map[string]int64
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			f.ended = append(f.ended, body["trip_id"])
			w.Write([]byte(`{"status":"trip ended"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) vehicle(interval time.Duration) *vehicle {
	return &vehicle{
		id: "sim-test-001", routeID: "R1", baseURL: f.URL, token: "driver-token",
		client:   &http.Client{Transport: bearerTransport{token: "driver-token", base: http.DefaultTransport}},
		interval: interval, claims: newTripClaims(), stats: &stats{},
	}
}

// reportsOn returns the recorded reports that name a trip.
func (f *fakeServer) reportsOn(tripID string) []locationReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []locationReport
	for _, r := range f.reports {
		if r.TripID == tripID {
			out = append(out, r)
		}
	}
	return out
}

// TestVehicleDrivesOneTripAfterAnother runs a vehicle through a trip that is
// already under way and on to the trip back, against a server that records what
// a driver app would send.
func TestVehicleDrivesOneTripAfterAnother(t *testing.T) {
	// "out" left a second ago and arrives 600 ms from now; "back" leaves the
	// terminal 300 ms after that.
	start := time.Now()
	out := scheduledTrip{ID: "out", StartsAt: start.Add(-time.Second), EndsAt: start.Add(600 * time.Millisecond), FirstStop: "Centre", LastStop: "Terminal"}
	back := scheduledTrip{ID: "back", StartsAt: start.Add(900 * time.Millisecond), EndsAt: start.Add(1500 * time.Millisecond), FirstStop: "Terminal", LastStop: "Centre"}
	line := func(trip scheduledTrip, from, to float64) tripDetail {
		var d tripDetail
		d.Shape.Points = [][2]float64{{from, 36.8}, {to, 36.8}}
		d.Stops = straightTrip(trip.StartsAt).Stops[:2]
		d.Stops[1].AlongShapeM = 1000
		d.Stops[1].ArrivalAt, d.Stops[1].DepartureAt = trip.EndsAt, trip.EndsAt
		return d
	}
	const terminalLat = 0.0089932
	f := newFakeServer(t, []scheduledTrip{out, back}, map[string]tripDetail{
		"out":  line(out, 0, terminalLat),
		"back": line(back, terminalLat, 0),
	})
	v := f.vehicle(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- v.drive(ctx) }()

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.started) == 2 && len(f.reports) > 0 && f.reports[len(f.reports)-1].TripID == "back"
	}, 5*time.Second, 10*time.Millisecond, "the vehicle should finish the first trip and report on the trip back")
	cancel()
	require.NoError(t, <-done)

	f.mu.Lock()
	started, ended := f.started, f.ended
	f.mu.Unlock()
	assert.Equal(t, []map[string]string{
		{"vehicle_id": "sim-test-001", "route_id": "R1", "gtfs_trip_id": "out"},
		{"vehicle_id": "sim-test-001", "route_id": "R1", "gtfs_trip_id": "back"},
	}, started)
	assert.Equal(t, []int64{1, 2}, ended, "each trip is ended, the last one when the run is cancelled")
	assert.Empty(t, v.claims.taken)

	outReports := f.reportsOn("out")
	require.NotEmpty(t, outReports)
	// The trip was 1 s into its 1.6 s when the vehicle joined it, so the first
	// report is already most of the way up the line, not at its start.
	assert.Greater(t, outReports[0].Latitude, terminalLat/2)
	previous := 0.0
	for _, r := range outReports {
		assert.Equal(t, "sim-test-001", r.VehicleID)
		assert.Equal(t, "R1", r.RouteID)
		assert.Equal(t, "20261113", r.StartDate)
		assert.InDelta(t, 36.8, r.Longitude, 1e-9)
		assert.InDelta(t, 0, r.Bearing, 1e-6, "driving north")
		assert.GreaterOrEqual(t, r.Latitude, previous, "the vehicle only moves forward along the shape")
		assert.LessOrEqual(t, r.Latitude, terminalLat+1e-9)
		previous = r.Latitude
	}
	assert.InDelta(t, terminalLat, previous, 1e-9, "the last report of a trip is at its last stop")

	// Until "back" departs, the vehicle waits at the terminal it arrived at.
	first := f.reportsOn("back")[0]
	assert.InDelta(t, terminalLat, first.Latitude, 1e-9)
	assert.Zero(t, first.Speed)
	assert.InDelta(t, 180, first.Bearing, 1e-6, "facing back south")
}

func TestVehicleStopsWhenTheServerRefusesTheTrip(t *testing.T) {
	trip := scheduledTrip{ID: "out", StartsAt: time.Now().Add(-time.Second), EndsAt: time.Now().Add(time.Minute), FirstStop: "Centre", LastStop: "Terminal"}
	f := newFakeServer(t, []scheduledTrip{trip}, map[string]tripDetail{"out": straightTrip(trip.StartsAt)})
	f.startStatus = http.StatusConflict
	v := f.vehicle(50 * time.Millisecond)

	err := v.drive(t.Context())
	assert.ErrorContains(t, err, "starting trip out")
	assert.ErrorContains(t, err, "driver already has an active trip")
	assert.Empty(t, v.claims.taken, "a trip that could not be started is free for another vehicle")
	assert.Empty(t, f.ended, "there is no trip to end")
	assert.Empty(t, f.reports)
}

func TestVehicleStopsWhenItsTripHasNoShape(t *testing.T) {
	trip := scheduledTrip{ID: "out", StartsAt: time.Now().Add(-time.Second), EndsAt: time.Now().Add(time.Minute), FirstStop: "Centre", LastStop: "Terminal"}
	f := newFakeServer(t, []scheduledTrip{trip}, map[string]tripDetail{"out": {}})
	v := f.vehicle(50 * time.Millisecond)

	err := v.drive(t.Context())
	assert.ErrorContains(t, err, "nothing to drive along")
	assert.Empty(t, v.claims.taken)
	assert.Empty(t, f.started)
}

// TestVehicleDriveEndsQuietlyWhenCancelled: Ctrl+C in the middle of a request
// fails that request, and that is a clean stop, not a vehicle that failed.
func TestVehicleDriveEndsQuietlyWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	requests := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		cancel()
		<-r.Context().Done()
	}))
	defer srv.Close()
	v := &vehicle{
		id: "sim-test-001", routeID: "R1", baseURL: srv.URL, client: srv.Client(),
		interval: 50 * time.Millisecond, claims: newTripClaims(), stats: &stats{},
	}

	assert.NoError(t, v.drive(ctx))
	assert.Equal(t, "/api/v1/gtfs/routes/R1/trips", <-requests, "the run was cancelled while this request was in flight")
}
