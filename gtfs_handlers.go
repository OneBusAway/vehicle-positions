package main

import (
	"net/http"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gtfsCatalog serves the schedule to drivers (spec §4.3): the routes, a
// route's trips on a service date, and one trip's geometry and stop times
// with absolute times. It reads the index through a function so a refresh
// swapped in since the server started is what it serves.
type gtfsCatalog struct {
	index      func() *rider.Index
	thresholds rider.Thresholds
	now        func() time.Time
}

func newGTFSCatalog(index func() *rider.Index, thresholds rider.Thresholds) *gtfsCatalog {
	return &gtfsCatalog{index: index, thresholds: thresholds, now: time.Now}
}

// registerGTFSRoutes mounts the catalog behind the driver auth middleware.
func registerGTFSRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler, c *gtfsCatalog) {
	mux.Handle("GET /api/v1/gtfs/routes", auth(c.handleRoutes()))
	mux.Handle("GET /api/v1/gtfs/routes/{route_id}/trips", auth(c.handleRouteTrips()))
	mux.Handle("GET /api/v1/gtfs/trips/{trip_id}", auth(c.handleTrip()))
}

type catalogRoute struct {
	ID        string `json:"id"`
	ShortName string `json:"short_name"`
	LongName  string `json:"long_name"`
	Color     string `json:"color"`
	TextColor string `json:"text_color"`
	Type      int    `json:"type"`
}

type catalogRoutesResponse struct {
	Routes []catalogRoute `json:"routes"`
}

type catalogTripSummary struct {
	ID          string    `json:"id"`
	Headsign    string    `json:"headsign"`
	DirectionID *int      `json:"direction_id"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	FirstStop   string    `json:"first_stop"`
	LastStop    string    `json:"last_stop"`
}

type catalogRouteTripsResponse struct {
	RouteID     string               `json:"route_id"`
	ServiceDate string               `json:"service_date"`
	Timezone    string               `json:"timezone"`
	Trips       []catalogTripSummary `json:"trips"`
}

type catalogTripRoute struct {
	ShortName string `json:"short_name"`
	LongName  string `json:"long_name"`
	Color     string `json:"color"`
	TextColor string `json:"text_color"`
}

type catalogShape struct {
	LengthM float64      `json:"length_m"`
	Points  [][2]float64 `json:"points"` // [lat, lon]
}

type catalogStop struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Sequence    int       `json:"sequence"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	AlongShapeM float64   `json:"along_shape_m"`
	ArrivalAt   time.Time `json:"arrival_at"`
	DepartureAt time.Time `json:"departure_at"`
}

type catalogThresholds struct {
	MaxShapeDistanceM float64 `json:"max_shape_distance_m"`
	ScheduleEarlyS    int     `json:"schedule_early_s"`
	ScheduleLateS     int     `json:"schedule_late_s"`
}

type catalogTripResponse struct {
	ID          string            `json:"id"`
	RouteID     string            `json:"route_id"`
	Headsign    string            `json:"headsign"`
	DirectionID *int              `json:"direction_id"`
	ServiceDate string            `json:"service_date"`
	Timezone    string            `json:"timezone"`
	Route       catalogTripRoute  `json:"route"`
	Shape       catalogShape      `json:"shape"`
	Stops       []catalogStop     `json:"stops"`
	Thresholds  catalogThresholds `json:"thresholds"`
}

func (c *gtfsCatalog) handleRoutes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ix, ok := c.currentIndex(w)
		if !ok {
			return
		}
		routes := ix.Routes()
		out := catalogRoutesResponse{Routes: make([]catalogRoute, 0, len(routes))}
		for _, rt := range routes {
			out.Routes = append(out.Routes, catalogRoute{
				ID: rt.ID, ShortName: rt.ShortName, LongName: rt.LongName,
				Color: rt.Color, TextColor: rt.TextColor, Type: rt.Type,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (c *gtfsCatalog) handleRouteTrips() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ix, ok := c.currentIndex(w)
		if !ok {
			return
		}
		routeID := r.PathValue("route_id")
		if _, ok := ix.Route(routeID); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown route"})
			return
		}
		date := r.URL.Query().Get("date")
		if date == "" {
			date = ix.ServiceDate(c.now())
		}
		dayStart, ok := c.serviceDay(w, ix, date)
		if !ok {
			return
		}

		trips := ix.TripsOnRoute(routeID, date)
		out := catalogRouteTripsResponse{RouteID: routeID, ServiceDate: date, Timezone: ix.Timezone().String(),
			Trips: make([]catalogTripSummary, 0, len(trips))}
		for _, trip := range trips {
			first, last := trip.StopTimes[0], trip.StopTimes[len(trip.StopTimes)-1]
			out.Trips = append(out.Trips, catalogTripSummary{
				ID: trip.ID, Headsign: trip.Headsign, DirectionID: directionJSON(trip.DirectionID),
				StartsAt: dayStart.Add(first.Departure), EndsAt: dayStart.Add(last.Arrival),
				FirstStop: first.StopName, LastStop: last.StopName,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (c *gtfsCatalog) handleTrip() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ix, ok := c.currentIndex(w)
		if !ok {
			return
		}
		trip, ok := ix.Trip(r.PathValue("trip_id"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown trip"})
			return
		}
		// No date named means the one this trip actually runs on, which for
		// an after-midnight departure is not always the one the 03:00 cutoff
		// derives (the same rule rider start applies).
		date := r.URL.Query().Get("date")
		if date == "" {
			date = ix.ServiceDateFor(trip, c.now())
		}
		dayStart, ok := c.serviceDay(w, ix, date)
		if !ok {
			return
		}
		if !ix.ActiveOn(trip, date) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "trip not active on date"})
			return
		}
		route, _ := ix.Route(trip.RouteID)

		points := make([][2]float64, len(trip.Shape.Points))
		for i, p := range trip.Shape.Points {
			points[i] = [2]float64{p.Lat, p.Lon}
		}
		stops := make([]catalogStop, 0, len(trip.StopTimes))
		for _, st := range trip.StopTimes {
			stops = append(stops, catalogStop{
				ID: st.StopID, Name: st.StopName, Sequence: st.Sequence,
				Lat: st.Pos.Lat, Lon: st.Pos.Lon, AlongShapeM: st.AlongShape,
				ArrivalAt: dayStart.Add(st.Arrival), DepartureAt: dayStart.Add(st.Departure),
			})
		}
		writeJSON(w, http.StatusOK, catalogTripResponse{
			ID: trip.ID, RouteID: trip.RouteID, Headsign: trip.Headsign, DirectionID: directionJSON(trip.DirectionID),
			ServiceDate: date, Timezone: ix.Timezone().String(),
			Route: catalogTripRoute{ShortName: route.ShortName, LongName: route.LongName, Color: route.Color, TextColor: route.TextColor},
			Shape: catalogShape{LengthM: trip.Shape.Length, Points: points},
			Stops: stops,
			Thresholds: catalogThresholds{
				MaxShapeDistanceM: c.thresholds.MaxShapeDistance,
				ScheduleEarlyS:    int(c.thresholds.ScheduleEarly.Seconds()),
				ScheduleLateS:     int(c.thresholds.ScheduleLate.Seconds()),
			},
		})
	}
}

// currentIndex answers 503 when there is no schedule to serve, which can only
// happen between a failed load and the process exiting; it is here so no
// handler dereferences a nil index.
func (c *gtfsCatalog) currentIndex(w http.ResponseWriter) (*rider.Index, bool) {
	ix := c.index()
	if ix == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedule data unavailable"})
		return nil, false
	}
	return ix, true
}

// serviceDay validates a "YYYYMMDD" date and returns the instant its service
// day starts, answering 400 for anything else. A date that matches the
// pattern but names no real day ("20261399") is malformed too.
func (c *gtfsCatalog) serviceDay(w http.ResponseWriter, ix *rider.Index, date string) (time.Time, bool) {
	if !serviceDatePattern.MatchString(date) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be YYYYMMDD"})
		return time.Time{}, false
	}
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be YYYYMMDD"})
		return time.Time{}, false
	}
	return dayStart, true
}

// directionJSON renders the index's -1 sentinel as JSON null.
func directionJSON(d int) *int {
	if d < 0 {
		return nil
	}
	return &d
}
