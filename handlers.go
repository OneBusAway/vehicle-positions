package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxVehicleIDLength = 64
	maxTripIDLength    = 128
)

// LocationReport is the JSON payload for incoming location data.
type LocationReport struct {
	VehicleID string  `json:"vehicle_id"`
	TripID    string  `json:"trip_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Bearing   float64 `json:"bearing"`
	Speed     float64 `json:"speed"`
	Accuracy  float64 `json:"accuracy"`
	Timestamp int64   `json:"timestamp"`
}

func (r *LocationReport) validate() error {
	if r.VehicleID == "" {
		return fmt.Errorf("vehicle_id is required")
	}
	if len(r.VehicleID) > maxVehicleIDLength {
		return fmt.Errorf("vehicle_id exceeds maximum length of %d", maxVehicleIDLength)
	}
	if len(r.TripID) > maxTripIDLength {
		return fmt.Errorf("trip_id exceeds maximum length of %d", maxTripIDLength)
	}
	if r.Latitude == 0 && r.Longitude == 0 {
		return fmt.Errorf("latitude and longitude cannot both be zero (likely GPS error)")
	}
	if r.Latitude < -90 || r.Latitude > 90 {
		return fmt.Errorf("latitude must be between -90 and 90")
	}
	if r.Longitude < -180 || r.Longitude > 180 {
		return fmt.Errorf("longitude must be between -180 and 180")
	}
	if r.Timestamp <= 0 {
		return fmt.Errorf("timestamp must be positive")
	}
	return nil
}

type LocationSaver interface {
	SaveLocation(ctx context.Context, loc *LocationReport) error
}

func handlePostLocation(store LocationSaver, tracker *Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB

		var loc LocationReport
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&loc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		if err := loc.validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		if err := store.SaveLocation(r.Context(), &loc); err != nil {
			log.Printf("failed to save location for vehicle %s: %v", loc.VehicleID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save location"})
			return
		}

		tracker.Update(&loc)

		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	}
}

func handleGetFeed(tracker *Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vehicles := tracker.ActiveVehicles()
		feed := buildFeed(vehicles)

		if r.URL.Query().Get("format") == "json" {
			data, err := protojson.Marshal(feed)
			if err != nil {
				log.Printf("failed to marshal feed as JSON: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to marshal feed"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(data); err != nil {
				log.Printf("failed to write JSON response: %v", err)
			}
			return
		}

		data, err := proto.Marshal(feed)
		if err != nil {
			log.Printf("failed to marshal feed as protobuf: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to marshal feed"})
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		if _, err := w.Write(data); err != nil {
			log.Printf("failed to write protobuf response: %v", err)
		}
	}
}

func buildFeed(vehicles []*VehicleState) *gtfs.FeedMessage {
	now := uint64(time.Now().Unix())
	version := "2.0"
	inc := gtfs.FeedHeader_FULL_DATASET

	feed := &gtfs.FeedMessage{
		Header: &gtfs.FeedHeader{
			GtfsRealtimeVersion: &version,
			Incrementality:      &inc,
			Timestamp:           &now,
		},
	}

	for _, v := range vehicles {
		entity := &gtfs.FeedEntity{
			Id: proto.String(v.VehicleID),
			Vehicle: &gtfs.VehiclePosition{
				Vehicle: &gtfs.VehicleDescriptor{
					Id: proto.String(v.VehicleID),
				},
				Position: &gtfs.Position{
					Latitude:  proto.Float32(float32(v.Latitude)),
					Longitude: proto.Float32(float32(v.Longitude)),
					Bearing:   proto.Float32(float32(v.Bearing)),
					Speed:     proto.Float32(float32(v.Speed)),
				},
				Timestamp: proto.Uint64(uint64(v.Timestamp)),
			},
		}
		if v.TripID != "" {
			entity.Vehicle.Trip = &gtfs.TripDescriptor{
				TripId: proto.String(v.TripID),
			}
		}
		feed.Entity = append(feed.Entity, entity)
	}

	return feed
}

type adminStatusResponse struct {
	Status               string     `json:"status"`
	UptimeSeconds        int64      `json:"uptime_seconds"`
	ActiveVehicles       int        `json:"active_vehicles"`
	TotalVehiclesTracked int        `json:"total_vehicles_tracked"`
	LastUpdate           *time.Time `json:"last_update,omitempty"`
}

func handleAdminStatus(tracker *Tracker, startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ts := tracker.Status()
		writeJSON(w, http.StatusOK, adminStatusResponse{
			Status:               "ok",
			UptimeSeconds:        int64(time.Since(startTime).Seconds()),
			ActiveVehicles:       ts.ActiveVehicles,
			TotalVehiclesTracked: ts.TotalVehiclesTracked,
			LastUpdate:           ts.LastUpdate,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to write JSON response: %v", err)
	}
}
