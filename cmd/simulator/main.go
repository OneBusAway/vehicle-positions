package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type locationReport struct {
	VehicleID string  `json:"vehicle_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Bearing   float64 `json:"bearing"`
	Speed     float64 `json:"speed"`
	Accuracy  float64 `json:"accuracy"`
	Timestamp int64   `json:"timestamp"`
}

type stats struct {
	succeeded atomic.Int64
	failed    atomic.Int64
	totalMS   atomic.Int64
}

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Server base URL")
	numVehicles := flag.Int("vehicles", 10, "Number of simulated vehicles")
	interval := flag.Duration("interval", 10*time.Second, "Time between location reports per vehicle")
	duration := flag.Duration("duration", 5*time.Minute, "Total simulation duration (0 = run until Ctrl+C)")
	flag.Parse()

	if *numVehicles <= 0 {
		log.Fatal("vehicles must be positive")
	}
	if *interval <= 0 {
		log.Fatal("interval must be positive")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	client := &http.Client{Timeout: 10 * time.Second}
	s := &stats{}

	log.Printf("starting simulator: %d vehicles, interval=%s, duration=%s", *numVehicles, *interval, *duration)

	var wg sync.WaitGroup
	for i := 0; i < *numVehicles; i++ {
		wg.Add(1)
		vehicleID := fmt.Sprintf("sim-vehicle-%03d", i+1)
		route := routes[i%len(routes)]
		go func() {
			defer wg.Done()
			simulateVehicle(ctx, client, *baseURL, vehicleID, route, *interval, s)
		}()
	}
	wg.Wait()

	ok := s.succeeded.Load()
	fail := s.failed.Load()
	avgMS := int64(0)
	if ok > 0 {
		avgMS = s.totalMS.Load() / ok
	}
	log.Printf("simulation complete: %d requests, %d ok, %d failed, avg=%dms", ok+fail, ok, fail, avgMS)
}

func simulateVehicle(ctx context.Context, client *http.Client, baseURL, vehicleID string, route []Waypoint, interval time.Duration, s *stats) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	waypointIdx := 0
	segmentStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			from := route[waypointIdx]
			to := route[(waypointIdx+1)%len(route)]

			segmentDist := haversineDistance(from, to)
			segmentDuration := segmentDist / 8.0 // assume ~8 m/s (~29 km/h, realistic urban bus)
			if segmentDuration <= 0 {
				segmentDuration = 1
			}

			elapsed := now.Sub(segmentStart).Seconds()
			t := elapsed / segmentDuration
			if t >= 1.0 {
				waypointIdx = (waypointIdx + 1) % len(route)
				segmentStart = now
				t = 0
				from = route[waypointIdx]
				to = route[(waypointIdx+1)%len(route)]
				segmentDist = haversineDistance(from, to)
				segmentDuration = segmentDist / 8.0
				if segmentDuration <= 0 {
					segmentDuration = 1
				}
			}

			pos := interpolate(from, to, t)
			brng := bearing(from, to)
			spd := speed(segmentDist, segmentDuration)

			report := locationReport{
				VehicleID: vehicleID,
				Latitude:  pos.Lat,
				Longitude: pos.Lon,
				Bearing:   brng,
				Speed:     spd,
				Accuracy:  5.0, // assume ~5m GPS accuracy for simulated reports
				Timestamp: now.Unix(),
			}

			sendReport(ctx, client, baseURL, vehicleID, &report, s)
		}
	}
}

func sendReport(ctx context.Context, client *http.Client, baseURL, vehicleID string, report *locationReport, s *stats) {
	body, err := json.Marshal(report)
	if err != nil {
		log.Printf("%s: marshal error: %v", vehicleID, err)
		s.failed.Add(1)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/locations", bytes.NewReader(body))
	if err != nil {
		log.Printf("%s: request error: %v", vehicleID, err)
		s.failed.Add(1)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return // clean shutdown, not a real failure
		}
		log.Printf("%s: POST failed: %v", vehicleID, err)
		s.failed.Add(1)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		io.Copy(io.Discard, resp.Body)
		s.succeeded.Add(1)
		s.totalMS.Add(latency.Milliseconds())
		log.Printf("%s: POST %d (%dms)", vehicleID, resp.StatusCode, latency.Milliseconds())
	} else {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		s.failed.Add(1)
		log.Printf("%s: POST %d (%dms): %s", vehicleID, resp.StatusCode, latency.Milliseconds(), string(bodyBytes))
	}
}
