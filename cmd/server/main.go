package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/OneBusAway/vehicle-positions/internal/api"
	_ "modernc.org/sqlite"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port for the server")
	dbPath := flag.String("db", "vehicle_positions.db", "Path to SQLite database")
	flag.Parse()

	// Initialize SQLite database
	database, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	mux := http.NewServeMux()

	// Initialize API handlers
	server := api.NewServer(database)
	server.RegisterRoutes(mux)

	log.Printf("Starting Vehicle Tracker server on port %d using database %s...", *port, *dbPath)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
		os.Exit(1)
	}
}
