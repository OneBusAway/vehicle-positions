package gtfs

type Stop struct {
	StopID string
	Name   string
	Lat    float64
	Lon    float64
}

type Route struct {
	RouteID   string
	ShortName string
	LongName  string
}

type Trip struct {
	TripID    string
	RouteID   string
	ServiceID string
}

type StopTime struct {
	TripID        string
	ArrivalTime   string
	DepartureTime string
	StopID        string
	StopSequence  int
}

type ImportResult struct {
	Stops     int `json:"stops_imported"`
	Routes    int `json:"routes_imported"`
	Trips     int `json:"trips_imported"`
	StopTimes int `json:"stop_times_imported"`
}
