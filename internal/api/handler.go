package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/OneBusAway/vehicle-positions/internal/db"
)

type Server struct {
	db      *sql.DB
	queries db.Querier
}

func NewServer(database *sql.DB) *Server {
	return &Server{
		db:      database,
		queries: db.New(database),
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/locations", s.handleLocationReport)
	mux.HandleFunc("GET /gtfs-rt/vehicle-positions", s.handleGTFSRTFeed)
	mux.HandleFunc("GET /healthz", s.handleHealth)
}

func (s *Server) handleLocationReport(w http.ResponseWriter, r *http.Request) {
	var report struct {
		VehicleID string  `json:"vehicle_id"`
		TripID    string  `json:"trip_id"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Bearing   float32 `json:"bearing"`
		Speed     float32 `json:"speed"`
		Accuracy  float32 `json:"accuracy"`
		Timestamp int64   `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	params := db.InsertLocationParams{
		VehicleID: report.VehicleID,
		TripID:    report.TripID,
		Latitude:  report.Latitude,
		Longitude: report.Longitude,
		Bearing:   float64(report.Bearing),
		Speed:     float64(report.Speed),
		Accuracy:  float64(report.Accuracy),
		Timestamp: report.Timestamp,
	}

	if err := s.queries.InsertLocation(r.Context(), params); err != nil {
		http.Error(w, "failed to save location", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

func (s *Server) handleGTFSRTFeed(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement GTFS-RT serialization
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
