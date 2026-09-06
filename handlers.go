package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/OneBusAway/vehicle-positions/rider"
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
	if r.Bearing != nil && (*r.Bearing < 0 || *r.Bearing > 360) {
		return fmt.Errorf("bearing must be between 0 and 360 (inclusive)")
	}
	if r.Speed != nil && *r.Speed < 0 {
		return fmt.Errorf("speed must be non-negative")
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

// estimateSource supplies the rider-reported trip estimates that the feed
// merges alongside driver-reported positions. A server with rider mode off
// supplies riderOff, which has none, so the feed never has to ask.
type estimateSource interface {
	Estimates(now time.Time) []rider.TripEstimate
}

func handleGetFeed(tracker *Tracker, estimates estimateSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			vehicles []*VehicleState
			ests     []rider.TripEstimate
		)
		// source selects which half of the feed to publish; an unrecognised
		// value matches neither half and is rejected rather than served empty.
		source := r.URL.Query().Get("source")
		wantDriver := source == "" || source == "all" || source == "driver"
		wantRider := source == "" || source == "all" || source == "rider"
		if !wantDriver && !wantRider {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid source"})
			return
		}
		if wantDriver {
			vehicles = tracker.ActiveVehicles()
		}
		if wantRider {
			ests = estimates.Estimates(time.Now())
		}
		feed := buildFeed(vehicles, ests)

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

func buildFeed(vehicles []*VehicleState, estimates []rider.TripEstimate) *gtfsrt.FeedMessage {
	now := uint64(time.Now().Unix())
	version := "2.0"
	inc := gtfsrt.FeedHeader_FULL_DATASET

	// E012 (gtfs-realtime-validator): header.timestamp must be >= all entity timestamps.
	headerTimestamp := now

	var entities []*gtfsrt.FeedEntity
	for _, v := range vehicles {
		if v.Timestamp <= 0 {
			slog.Warn("buildFeed: skipping vehicle with non-positive timestamp", "vehicle_id", v.VehicleID, "timestamp", v.Timestamp)
			continue
		}
		ts := uint64(v.Timestamp)
		if ts > headerTimestamp {
			headerTimestamp = ts
		}

		position := &gtfsrt.Position{
			Latitude:  proto.Float32(float32(v.Latitude)),
			Longitude: proto.Float32(float32(v.Longitude)),
		}
		if v.Bearing != nil {
			position.Bearing = proto.Float32(float32(*v.Bearing))
		}
		if v.Speed != nil {
			position.Speed = proto.Float32(float32(*v.Speed))
		}

		entity := &gtfsrt.FeedEntity{
			Id: proto.String(v.VehicleID),
			Vehicle: &gtfsrt.VehiclePosition{
				Vehicle: &gtfsrt.VehicleDescriptor{
					Id: proto.String(v.VehicleID),
				},
				Position:  position,
				Timestamp: proto.Uint64(ts),
			},
		}

		if v.TripID != "" {
			entity.Vehicle.Trip = &gtfsrt.TripDescriptor{
				TripId: proto.String(v.TripID),
			}
		}
		entities = append(entities, entity)
	}

	for _, est := range estimates {
		ts := est.Timestamp.Unix()
		if ts <= 0 {
			slog.Warn("buildFeed: skipping rider estimate with non-positive timestamp",
				"trip_id", est.Key.TripID, "start_date", est.Key.StartDate, "timestamp", ts)
			continue
		}
		if uint64(ts) > headerTimestamp {
			headerTimestamp = uint64(ts)
		}
		entities = append(entities, riderEntity(est))
	}

	return &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: &version,
			Incrementality:      &inc,
			Timestamp:           &headerTimestamp,
		},
		Entity: entities,
	}
}

// riderEntity renders one rider-consensus trip estimate as a FeedEntity. The
// "rider:" prefixes keep these ids from colliding with driver-reported
// vehicles, and the label marks the position as rider-reported for consumers.
// vehicle.id repeats the entity id (trip + start date) rather than the trip id
// alone: the same trip running on two service dates is two vehicles, and E052
// requires vehicle.id to be unique across the feed.
func riderEntity(est rider.TripEstimate) *gtfsrt.FeedEntity {
	position := &gtfsrt.Position{
		Latitude:  proto.Float32(float32(est.Pos.Lat)),
		Longitude: proto.Float32(float32(est.Pos.Lon)),
		Bearing:   proto.Float32(float32(est.Bearing)),
	}
	if est.Speed != nil {
		position.Speed = proto.Float32(float32(*est.Speed))
	}

	trip := &gtfsrt.TripDescriptor{
		TripId:    proto.String(est.Key.TripID),
		StartDate: proto.String(est.Key.StartDate),
	}
	if est.RouteID != "" {
		trip.RouteId = proto.String(est.RouteID)
	}

	id := riderEntityID(est.Key)
	vp := &gtfsrt.VehiclePosition{
		Vehicle: &gtfsrt.VehicleDescriptor{
			Id:    proto.String(id),
			Label: proto.String("Rider-reported"),
		},
		Trip:      trip,
		Position:  position,
		Timestamp: proto.Uint64(uint64(est.Timestamp.Unix())),
	}
	if est.StopID != "" {
		vp.StopId = proto.String(est.StopID)
		vp.CurrentStopSequence = proto.Uint32(uint32(est.StopSequence))
		vp.CurrentStatus = gtfsrt.VehiclePosition_IN_TRANSIT_TO.Enum()
	}

	return &gtfsrt.FeedEntity{
		Id:      proto.String(id),
		Vehicle: vp,
	}
}

// riderEntityID is the feed id of a rider-reported trip instance, used both as
// the FeedEntity id and as vehicle.id.
func riderEntityID(key rider.TripKey) string {
	return "rider:" + key.TripID + ":" + key.StartDate
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

const (
	// defaultListLimit and maxListLimit bound the limit query param on the
	// admin vehicle and user list endpoints, mirroring
	// defaultTripListLimit/maxTripListLimit (admin_live_handlers.go) so every
	// admin list endpoint bounds a page the same way.
	defaultListLimit = 50
	maxListLimit     = 200

	// maxListOffset is the largest offset those endpoints accept: the paged
	// queries take an int32 offset, and a larger value would wrap negative
	// and be rejected by Postgres as a 500 instead of a 400.
	maxListOffset = math.MaxInt32
)

// parseListPageParams reads the limit/offset paging params shared by the
// admin list endpoints. It writes the 400 response itself and reports
// ok=false, so callers return without repeating the error handling.
func parseListPageParams(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	q := r.URL.Query()

	limit, err := parseOptionalInt(q.Get("limit"), defaultListLimit)
	if err != nil || limit < 1 || limit > maxListLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("limit must be between 1 and %d", maxListLimit)})
		return 0, 0, false
	}

	offset, err = parseOptionalInt(q.Get("offset"), 0)
	if err != nil || offset < 0 || offset > maxListOffset {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("offset must be between 0 and %d", maxListOffset)})
		return 0, 0, false
	}

	return limit, offset, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
