package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/OneBusAway/vehicle-positions/internal/api"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port for the server")
	flag.Parse()

	mux := http.NewServeMux()

	// Initialize API handlers
	server := api.NewServer()
	server.RegisterRoutes(mux)

	log.Printf("Starting Vehicle Tracker server on port %d...", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
		os.Exit(1)
	}
}
