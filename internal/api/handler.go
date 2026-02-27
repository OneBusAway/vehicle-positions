package api

import (
	"encoding/json"
	"net/http"
)

type Server struct {
	// Add dependencies like DB or in-memory state here
}

func NewServer() *Server {
	return &Server{}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/locations", s.handleLocationReport)
	mux.HandleFunc("GET /gtfs-rt/vehicle-positions", s.handleGTFSRTFeed)
	mux.HandleFunc("GET /healthz", s.handleHealth)
}

func (s *Server) handleLocationReport(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement ingestion logic
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
