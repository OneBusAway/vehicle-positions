package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	serviceDateLayout = "20060102"

	// scheduleLookahead is how many days past today a route is searched for
	// trips, so a route with no service today (a weekday-only feed on a
	// Sunday) still gives its vehicle a shape to drive.
	scheduleLookahead = 6

	// maxLayover is the longest a vehicle waits at a terminal for a trip that
	// leaves from it. Past that it takes a trip already on the road instead of
	// sitting still.
	maxLayover = 15 * time.Minute

	endTripTimeout = 10 * time.Second
)

// scheduledTrip is one entry of GET /api/v1/gtfs/routes/{route_id}/trips.
type scheduledTrip struct {
	ID        string    `json:"id"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	FirstStop string    `json:"first_stop"`
	LastStop  string    `json:"last_stop"`
}

// routeTrips is a route's trips on one service date, ordered by departure.
type routeTrips struct {
	ServiceDate string          `json:"service_date"`
	Trips       []scheduledTrip `json:"trips"`
}

// tripDetail is the part of GET /api/v1/gtfs/trips/{trip_id} a vehicle needs
// to drive the trip: the shape and when the schedule puts it at each stop.
type tripDetail struct {
	Shape struct {
		Points [][2]float64 `json:"points"` // [lat, lon]
	} `json:"shape"`
	Stops []struct {
		AlongShapeM float64   `json:"along_shape_m"`
		ArrivalAt   time.Time `json:"arrival_at"`
		DepartureAt time.Time `json:"departure_at"`
	} `json:"stops"`
}

// timepoint is where along its shape the schedule has a trip at one instant.
type timepoint struct {
	at    time.Time
	along float64
}

// run is one GTFS trip a vehicle is driving.
type run struct {
	trip        scheduledTrip
	serviceDate string
	shape       *rider.ShapeGeom
	timetable   []timepoint
	// shift is how much later than scheduled the trip is driven. It is zero
	// for a trip the schedule has on the road now, and moves a trip from some
	// other hour or day so that it leaves its first stop as the run begins.
	shift time.Duration
}

func newRun(trip scheduledTrip, serviceDate string, shift time.Duration, detail tripDetail) (*run, error) {
	if len(detail.Shape.Points) < 2 || len(detail.Stops) == 0 {
		return nil, fmt.Errorf("trip %s has %d shape points and %d stops, so there is nothing to drive along",
			trip.ID, len(detail.Shape.Points), len(detail.Stops))
	}
	points := make([]rider.LatLon, len(detail.Shape.Points))
	for i, p := range detail.Shape.Points {
		points[i] = rider.LatLon{Lat: p[0], Lon: p[1]}
	}
	timetable := make([]timepoint, 0, 2*len(detail.Stops))
	for _, s := range detail.Stops {
		timetable = append(timetable, timepoint{s.ArrivalAt, s.AlongShapeM}, timepoint{s.DepartureAt, s.AlongShapeM})
	}
	return &run{trip: trip, serviceDate: serviceDate, shape: rider.NewShapeGeom(points), timetable: timetable, shift: shift}, nil
}

// position returns how far along the shape the vehicle is at now, and its
// speed in metres per second: the pace of the leg it is on, and zero while it
// waits to depart, dwells at a stop, or has arrived.
func (r *run) position(now time.Time) (along, speed float64) {
	t := now.Add(-r.shift)
	for i, next := range r.timetable {
		if !t.Before(next.at) {
			continue
		}
		if i == 0 {
			return next.along, 0
		}
		prev := r.timetable[i-1]
		speed = (next.along - prev.along) / next.at.Sub(prev.at).Seconds()
		return prev.along + speed*t.Sub(prev.at).Seconds(), speed
	}
	return r.timetable[len(r.timetable)-1].along, 0
}

// finished reports whether the vehicle has reached the trip's last stop.
func (r *run) finished(now time.Time) bool {
	return !now.Add(-r.shift).Before(r.timetable[len(r.timetable)-1].at)
}

// pickTrip chooses the trip a vehicle drives next from a route's trips, which
// must be non-empty and ordered by departure, and returns it with the run's
// shift. In order of preference:
//
//  1. the next trip to leave the stop the vehicle just arrived at (from, empty
//     for a vehicle's first trip), within maxLayover: the trip back, as a
//     driver would take it;
//  2. the trip the schedule has on the road now that left most recently, joined
//     where the schedule has it;
//  3. the next trip to leave, or failing that the day's first, driven from its
//     first stop starting now, because the schedule has nothing running.
func pickTrip(trips []scheduledTrip, now time.Time, from string) (scheduledTrip, time.Duration) {
	if from != "" {
		for _, t := range trips {
			if t.FirstStop == from && t.StartsAt.After(now) && t.StartsAt.Sub(now) <= maxLayover {
				return t, 0
			}
		}
	}
	for i := len(trips) - 1; i >= 0; i-- {
		if t := trips[i]; !t.StartsAt.After(now) && now.Before(t.EndsAt) {
			return t, 0
		}
	}
	for _, t := range trips {
		if t.StartsAt.After(now) {
			return t, now.Sub(t.StartsAt)
		}
	}
	return trips[0], now.Sub(trips[0].StartsAt)
}

// tripClaims keeps the vehicles of a run on different trips, so they spread
// out along a route instead of all driving the same one on top of each other.
type tripClaims struct {
	mu    sync.Mutex
	taken map[string]int // trip id -> vehicles driving it
}

func newTripClaims() *tripClaims {
	return &tripClaims{taken: make(map[string]int)}
}

// claim picks a trip no other vehicle is driving. When every trip is taken,
// which a route with fewer trips than vehicles comes to, it shares one.
func (c *tripClaims) claim(trips []scheduledTrip, now time.Time, from string) (scheduledTrip, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	free := make([]scheduledTrip, 0, len(trips))
	for _, t := range trips {
		if c.taken[t.ID] == 0 {
			free = append(free, t)
		}
	}
	if len(free) == 0 {
		free = trips
	}
	trip, shift := pickTrip(free, now, from)
	c.taken[trip.ID]++
	return trip, shift
}

func (c *tripClaims) release(tripID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.taken[tripID]--; c.taken[tripID] <= 0 {
		delete(c.taken, tripID)
	}
}

// vehicle is one simulated vehicle and the driver signed in on it.
type vehicle struct {
	id       string
	routeID  string
	baseURL  string
	token    string
	client   *http.Client // carries the driver's token
	interval time.Duration
	claims   *tripClaims
	stats    *stats
}

// drive works the vehicle's route until ctx is done, one GTFS trip after
// another. It returns an error when the vehicle cannot go on: the route has no
// trip to drive, or the server refuses to start one.
func (v *vehicle) drive(ctx context.Context) error {
	from := ""
	for ctx.Err() == nil {
		r, err := v.nextRun(ctx, from)
		if err == nil {
			err = v.driveRun(ctx, r)
			v.claims.release(r.trip.ID)
			from = r.trip.LastStop
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
	}
	return nil
}

// nextRun claims the vehicle's next trip and loads its shape and timetable.
func (v *vehicle) nextRun(ctx context.Context, from string) (*run, error) {
	schedule, err := fetchRouteTrips(ctx, v.client, v.baseURL, v.routeID)
	if err != nil {
		return nil, err
	}
	trip, shift := v.claims.claim(schedule.Trips, time.Now(), from)

	var detail tripDetail
	err = getJSON(ctx, v.client, v.baseURL+"/api/v1/gtfs/trips/"+url.PathEscape(trip.ID)+"?date="+schedule.ServiceDate, &detail)
	if err != nil {
		v.claims.release(trip.ID)
		return nil, err
	}
	r, err := newRun(trip, schedule.ServiceDate, shift, detail)
	if err != nil {
		v.claims.release(trip.ID)
		return nil, err
	}
	return r, nil
}

// driveRun starts the trip on the server the way a driver app does, reports the
// vehicle's position along the shape every interval until it reaches the last
// stop or ctx is done, and ends the trip. Without the end every run would
// leave an active trip behind and repeated runs would pile them up.
func (v *vehicle) driveRun(ctx context.Context, r *run) error {
	var started struct {
		ID int64 `json:"id"`
	}
	err := postJSON(ctx, v.client, v.baseURL+"/api/v1/trips/start", v.token,
		map[string]string{"vehicle_id": v.id, "route_id": v.routeID, "gtfs_trip_id": r.trip.ID}, &started)
	if err != nil {
		return fmt.Errorf("starting trip %s: %w", r.trip.ID, err)
	}
	defer v.endTrip(started.ID)
	log.Printf("%s: driving trip %s on route %s (%.0f m)", v.id, r.trip.ID, v.routeID, r.shape.Length)

	ticker := time.NewTicker(v.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			along, speed := r.position(now)
			pos := r.shape.PointAt(along)
			sendReport(ctx, v.client, v.baseURL, v.id, &locationReport{
				VehicleID: v.id,
				TripID:    r.trip.ID,
				RouteID:   v.routeID,
				StartDate: r.serviceDate,
				Latitude:  pos.Lat,
				Longitude: pos.Lon,
				Bearing:   r.shape.BearingAt(along),
				Speed:     speed,
				Accuracy:  5.0, // assume ~5m GPS accuracy for simulated reports
				Timestamp: now.Unix(),
			}, v.stats)
			if r.finished(now) {
				return nil
			}
		}
	}
}

// endTrip has its own deadline, since the run's context is usually done by the
// time the last trip ends (the -duration ran out, or Ctrl+C).
func (v *vehicle) endTrip(tripID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), endTripTimeout)
	defer cancel()
	if err := postJSON(ctx, v.client, v.baseURL+"/api/v1/trips/end", v.token, map[string]int64{"trip_id": tripID}, nil); err != nil {
		log.Printf("%s: ending trip %d: %v", v.id, tripID, err)
	}
}

// fetchRouteTrips lists a route's trips on the server's current service date,
// or on the first of the following scheduleLookahead days that has any.
func fetchRouteTrips(ctx context.Context, client *http.Client, baseURL, routeID string) (routeTrips, error) {
	endpoint := baseURL + "/api/v1/gtfs/routes/" + url.PathEscape(routeID) + "/trips"
	var today routeTrips
	if err := getJSON(ctx, client, endpoint, &today); err != nil {
		return routeTrips{}, err
	}
	if len(today.Trips) > 0 {
		return today, nil
	}
	day, err := time.Parse(serviceDateLayout, today.ServiceDate)
	if err != nil {
		return routeTrips{}, fmt.Errorf("route %s: service_date %q is not YYYYMMDD", routeID, today.ServiceDate)
	}
	for ahead := 1; ahead <= scheduleLookahead; ahead++ {
		var later routeTrips
		if err := getJSON(ctx, client, endpoint+"?date="+day.AddDate(0, 0, ahead).Format(serviceDateLayout), &later); err != nil {
			return routeTrips{}, err
		}
		if len(later.Trips) > 0 {
			return later, nil
		}
	}
	return routeTrips{}, fmt.Errorf("route %s has no trips from %s to %d days later", routeID, today.ServiceDate, scheduleLookahead)
}

// getJSON GETs endpoint and decodes the response into out. Any status other than
// 200 is an error carrying the server's message.
func getJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("GET %s returned %d: %s", req.URL.Path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("decoding GET %s response: %w", req.URL.Path, err)
	}
	return nil
}
