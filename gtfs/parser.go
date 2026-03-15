package gtfs

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
)

func header(row []string) map[string]int {
	m := make(map[string]int, len(row))
	for i, name := range row {
		m[name] = i
	}
	return m
}

func col(row []string, h map[string]int, name string) string {
	if i, ok := h[name]; ok && i < len(row) {
		return row[i]
	}
	return ""
}

func openCSV(path string, required []string) (*os.File, *csv.Reader, map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true

	headerRow, err := r.Read()
	if err == io.EOF {
		_ = f.Close()
		return nil, nil, nil, nil // empty file
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, nil, err
	}

	h := header(headerRow)
	for _, req := range required {
		if _, ok := h[req]; !ok {
			_ = f.Close()
			return nil, nil, nil, fmt.Errorf("missing required column %q", req)
		}
	}
	return f, r, h, nil
}

func ParseStops(path string) ([]Stop, error) {
	f, r, h, err := openCSV(path, []string{"stop_id", "stop_name", "stop_lat", "stop_lon"})
	if err != nil {
		return nil, fmt.Errorf("stops.txt: %w", err)
	}
	if f == nil {
		return nil, nil
	}
	defer f.Close()

	var stops []Stop
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stops.txt: %w", err)
		}

		latStr := col(row, h, "stop_lat")
		lonStr := col(row, h, "stop_lon")
		if latStr == "" || lonStr == "" {
			continue
		}

		lat, err := strconv.ParseFloat(latStr, 64)
		if err != nil {
			return nil, fmt.Errorf("stops.txt: invalid stop_lat %q: %w", latStr, err)
		}
		lon, err := strconv.ParseFloat(lonStr, 64)
		if err != nil {
			return nil, fmt.Errorf("stops.txt: invalid stop_lon %q: %w", lonStr, err)
		}

		stops = append(stops, Stop{
			StopID: col(row, h, "stop_id"),
			Name:   col(row, h, "stop_name"),
			Lat:    lat,
			Lon:    lon,
		})
	}
	return stops, nil
}

func ParseRoutes(path string) ([]Route, error) {
	f, r, h, err := openCSV(path, []string{"route_id", "route_short_name", "route_long_name"})
	if err != nil {
		return nil, fmt.Errorf("routes.txt: %w", err)
	}
	if f == nil {
		return nil, nil
	}
	defer f.Close()

	var routes []Route
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("routes.txt: %w", err)
		}
		routes = append(routes, Route{
			RouteID:   col(row, h, "route_id"),
			ShortName: col(row, h, "route_short_name"),
			LongName:  col(row, h, "route_long_name"),
		})
	}
	return routes, nil
}

func ParseTrips(path string) ([]Trip, error) {
	f, r, h, err := openCSV(path, []string{"trip_id", "route_id", "service_id"})
	if err != nil {
		return nil, fmt.Errorf("trips.txt: %w", err)
	}
	if f == nil {
		return nil, nil
	}
	defer f.Close()

	var trips []Trip
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("trips.txt: %w", err)
		}
		trips = append(trips, Trip{
			TripID:    col(row, h, "trip_id"),
			RouteID:   col(row, h, "route_id"),
			ServiceID: col(row, h, "service_id"),
		})
	}
	return trips, nil
}

func ParseStopTimes(path string) ([]StopTime, error) {
	f, r, h, err := openCSV(path, []string{"trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence"})
	if err != nil {
		return nil, fmt.Errorf("stop_times.txt: %w", err)
	}
	if f == nil {
		return nil, nil
	}
	defer f.Close()

	var stopTimes []StopTime
	lineNum := 2
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stop_times.txt: %w", err)
		}

		seqStr := col(row, h, "stop_sequence")
		seq, err := strconv.Atoi(seqStr)
		if err != nil {
			return nil, fmt.Errorf("stop_times.txt line %d: invalid stop_sequence %q: %w", lineNum, seqStr, err)
		}

		stopTimes = append(stopTimes, StopTime{
			TripID:        col(row, h, "trip_id"),
			ArrivalTime:   col(row, h, "arrival_time"),
			DepartureTime: col(row, h, "departure_time"),
			StopID:        col(row, h, "stop_id"),
			StopSequence:  seq,
		})
		lineNum++
	}
	return stopTimes, nil
}
