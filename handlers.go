package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// LocationReport is the JSON payload for incoming location data.
type LocationReport struct {
	VehicleID string   `json:"vehicle_id"`
	TripID    string   `json:"trip_id"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Bearing   *float64 `json:"bearing,omitempty"`
	Speed     *float64 `json:"speed,omitempty"`
	Accuracy  *float64 `json:"accuracy,omitempty"`
	Timestamp int64    `json:"timestamp"`
	// Set server-side from JWT; never decoded from JSON.
	DriverID string `json:"-"`
}

const maxVehicleIDLength = 50

var vehicleIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

const maxTimestampSkew = 5 * time.Minute

func (r *LocationReport) validate() error {
	if r.VehicleID == "" {
		return fmt.Errorf("vehicle_id is required")
	}
	if len(r.VehicleID) > maxVehicleIDLength {
		return fmt.Errorf("vehicle_id must be at most %d characters", maxVehicleIDLength)
	}
	if !vehicleIDPattern.MatchString(r.VehicleID) {
		return fmt.Errorf("vehicle_id must contain only alphanumeric characters, dots, hyphens, and underscores")
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
	now := time.Now().Unix()
	if r.Timestamp < now-int64(maxTimestampSkew.Seconds()) || r.Timestamp > now+int64(maxTimestampSkew.Seconds()) {
		return fmt.Errorf("timestamp must be within %d minutes of server time", int(maxTimestampSkew.Minutes()))
	}
	return nil
}

type LocationSaver interface {
	SaveLocation(ctx context.Context, loc *LocationReport) error
}

func handlePostLocation(store LocationSaver, tracker *Tracker, rl *VehicleRateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

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

		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		if !ok {
			slog.Warn("handlePostLocation: JWT claims missing from context")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		sub, ok := claims["sub"].(string)
		if !ok || sub == "" {
			slog.Warn("handlePostLocation: JWT sub claim missing or not a string")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token: missing subject"})
			return
		}
		loc.DriverID = sub
		if !rl.Allow(loc.DriverID) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded: at most one location report per 5 seconds per driver"})
			return
		}

		if err := store.SaveLocation(r.Context(), &loc); err != nil {
			slog.Error("failed to save location", "vehicle_id", loc.VehicleID, "error", err)
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
				slog.Error("failed to marshal feed", "format", "json", "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to marshal feed"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(data); err != nil {
				slog.Error("failed to write response", "format", "json", "error", err)
			}
			return
		}

		data, err := proto.Marshal(feed)
		if err != nil {
			slog.Error("failed to marshal feed", "format", "protobuf", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to marshal feed"})
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		if _, err := w.Write(data); err != nil {
			slog.Error("failed to write response", "format", "protobuf", "error", err)
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
		position := &gtfs.Position{
			Latitude:  proto.Float32(float32(v.Latitude)),
			Longitude: proto.Float32(float32(v.Longitude)),
		}
		if v.Bearing != nil {
			position.Bearing = proto.Float32(float32(*v.Bearing))
		}
		if v.Speed != nil {
			position.Speed = proto.Float32(float32(*v.Speed))
		}

		entity := &gtfs.FeedEntity{
			Id: proto.String(v.VehicleID),
			Vehicle: &gtfs.VehiclePosition{
				Vehicle: &gtfs.VehicleDescriptor{
					Id: proto.String(v.VehicleID),
				},
				Position:  position,
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

type HealthChecker interface {
	Ping(ctx context.Context) error
}

type readinessResponse struct {
	Status string `json:"status"`
}

func handleReadiness(checker HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := checker.Ping(ctx); err != nil {
			slog.Warn("readiness check failed", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, readinessResponse{Status: "degraded"})
			return
		}

		writeJSON(w, http.StatusOK, readinessResponse{Status: "ok"})
	}
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
		slog.Error("failed to write JSON response", "error", err)
	}
}

// Audit log for delete user action
// deleteUser handles the DELETE /api/v1/admin/users/{id} endpoint.
func deleteUser(store *store.Store, auditStore *store.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r http.Request) {
		// Gets user ID frmo URL
		vars := mux.Vars(r)
		userIDStr := vars["id"]
		userID, err := parseUserID(userIDStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
			return
		}

		// Fetch the user from the database
		user, err := store.GetUserByID(context.Background(), userID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Delete the user
		if err := store.DeleteUser(context.Background(), userID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete user"})
			return
		}

		// Log the audit event
		ipAddress := r.RemoteAddr
		details := fmt.Sprintf("User ID: %d, Email: %s", user.ID, user.Email)
		auditLog := audit.AuditLog{
			UserID:    user.ID,
			Action:    "user_deleted",
			IPAddress: ipAddress,
			Timestamp: time.Now(),
			Details:   details,
		}

		if err := auditStore.LogAudit(context.Background(), auditLog); err != nil {
			log.Printf("failed to log audit event: %v", err)
		}

		writeJSON(w, http.StatusNoContent, nil)

	}
}

// createUser handles the POST /api/v1/admin/users endpoint.
func createUser(store *store.Store, auditStore *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse the request body into a new user
		var user store.User
		if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Create the user in the database
		if err := store.CreateUser(context.Background(), &user); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
			return
		}

		// Log the audit event
		ipAddress := r.RemoteAddr
		details := fmt.Sprintf("User ID: %d, Email: %s", user.ID, user.Email)
		auditLog := audit.AuditLog{
			UserID:    user.ID,
			Action:    "user_created",
			IPAddress: ipAddress,
			Timestamp: time.Now(),
			Details:   details,
		}

		if err := auditStore.LogAudit(context.Background(), auditLog); err != nil {
			log.Printf("failed to log audit event: %v", err)
		}

		writeJSON(w, http.StatusCreated, map[string]string{"message": "user created successfully"})
	}
}

// updateUser handles the PUT /api/v1/admin/users/{id} endpoint.
func updateUser(store *store.Store, auditStore *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the user ID from the URL
		vars := mux.Vars(r)
		userIDStr := vars["id"]
		userID, err := parseUserID(userIDStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
			return
		}

		// Parse the request body into an updated user
		var user store.User
		if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		// Update the user in the database
		user.ID = userID
		if err := store.UpdateUser(context.Background(), &user); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update user"})
			return
		}

		// Log the audit event
		ipAddress := r.RemoteAddr
		details := fmt.Sprintf("User ID: %d, Email: %s", user.ID, user.Email)
		auditLog := audit.AuditLog{
			UserID:    user.ID,
			Action:    "user_updated",
			IPAddress: ipAddress,
			Timestamp: time.Now(),
			Details:   details,
		}

		if err := auditStore.LogAudit(context.Background(), auditLog); err != nil {
			log.Printf("failed to log audit event: %v", err)
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "user updated successfully"})
	}
}

// deleteVehicle handles the DELETE /api/v1/admin/vehicles/{id} endpoint.
func deleteVehicle(store *store.Store, auditStore *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the vehicle ID from the URL
		vars := mux.Vars(r)
		vehicleIDStr := vars["id"]
		vehicleID, err := parseVehicleID(vehicleIDStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid vehicle ID"})
			return
		}

		// Fetch the vehicle from the database
		vehicle, err := store.GetVehicleByID(context.Background(), vehicleID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Delete the vehicle
		if err := store.DeleteVehicle(context.Background(), vehicleID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete vehicle"})
			return
		}

		// Log the audit event
		ipAddress := r.RemoteAddr
		details := fmt.Sprintf("Vehicle ID: %d, License Plate: %s", vehicle.ID, vehicle.LicensePlate)
		auditLog := audit.AuditLog{
			UserID:    vehicle.UserID, // assuming the vehicle is associated with a user
			Action:    "vehicle_deleted",
			IPAddress: ipAddress,
			Timestamp: time.Now(),
			Details:   details,
		}

		if err := auditStore.LogAudit(context.Background(), auditLog); err != nil {
			log.Printf("failed to log audit event: %v", err)
		}

		writeJSON(w, http.StatusNoContent, nil)
	}
}
