package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
	"github.com/google/uuid"
)

const (
	// riderReportIntervalSeconds is how often the app is told to report.
	riderReportIntervalSeconds = 5
	// riderMaxBatchSize is the most positions one report may carry.
	riderMaxBatchSize = 12
	// riderMaxBodyBytes caps a rider request body. The largest of them is a
	// batch of riderMaxBatchSize positions, which is orders of magnitude
	// smaller than this.
	riderMaxBodyBytes = 1 << 20
	// riderMaxFieldLen bounds the free-text identifiers a client may send, so
	// nothing absurd reaches the database.
	riderMaxFieldLen = 100
	// riderBatchInterval and riderBatchBurst rate-limit position reports per
	// rider: one report every riderReportIntervalSeconds, with enough slack for
	// a retry.
	riderBatchInterval = 2 * time.Second
	riderBatchBurst    = 2
)

// serviceDatePattern is the only shape a client-supplied start_date may take.
var serviceDatePattern = regexp.MustCompile(`^[0-9]{8}$`)

// riderPlatforms are the platform values a registration may claim.
var riderPlatforms = map[string]bool{"ios": true, "android": true, "other": true}

// riderStore is everything the rider API needs from persistence.
type riderStore interface {
	RiderRegistrar
	RiderReader
	RideStarter
	RidePointRecorder
	RideFinisher
	RideLister
	RiderStatsReader
	RidePointPruner
}

// trustedLookup is the agency's own view of where a trip is, as far as the
// rider API is concerned. *rider.TrustedFeed satisfies it.
type trustedLookup interface {
	Configured() bool
	Lookup(key rider.TripKey, now time.Time) (rider.TrustedVehicle, bool)
	Covers(key rider.TripKey, now time.Time) bool
	Health() []rider.FeedHealth
}

// riderService is the rider API: the HTTP surface over the rider engine, its
// store and the trusted feed.
type riderService struct {
	store        riderStore
	agg          *rider.Aggregator
	index        func() *rider.Index // current index (Refresher.Current in production)
	trusted      trustedLookup
	regLimiter   *RegistrationRateLimiter
	batchLimiter *VehicleRateLimiter
	jwtSecret    []byte
	jwtTTL       time.Duration
	trustProxy   bool
	thresholds   rider.Thresholds
	now          func() time.Time

	reportIntervalSeconds int
	maxBatchSize          int
}

// newRiderService wires the rider API together. It starts both rate limiters,
// so every caller must Stop the service it gets back.
func newRiderService(store riderStore, agg *rider.Aggregator, index func() *rider.Index, trusted trustedLookup,
	jwtSecret []byte, jwtTTL time.Duration, trustProxy bool, th rider.Thresholds) *riderService {
	return &riderService{
		store:                 store,
		agg:                   agg,
		index:                 index,
		trusted:               trusted,
		regLimiter:            NewRegistrationRateLimiter(),
		batchLimiter:          NewKeyedRateLimiter(riderBatchInterval, riderBatchBurst),
		jwtSecret:             jwtSecret,
		jwtTTL:                jwtTTL,
		trustProxy:            trustProxy,
		thresholds:            th,
		now:                   time.Now,
		reportIntervalSeconds: riderReportIntervalSeconds,
		maxBatchSize:          riderMaxBatchSize,
	}
}

// Stop shuts down the service's rate limiters.
func (s *riderService) Stop() {
	s.regLimiter.Stop()
	s.batchLimiter.Stop()
}

// riderRegisterRequest is the body of POST /api/v1/rider/register.
type riderRegisterRequest struct {
	InstallationID string           `json:"installation_id"`
	Platform       string           `json:"platform"`
	AppID          string           `json:"app_id"`
	AppVersion     string           `json:"app_version"`
	Attestation    *json.RawMessage `json:"attestation"`
}

type riderRegisterResponse struct {
	RiderID               string `json:"rider_id"`
	Token                 string `json:"token"`
	ReportIntervalSeconds int    `json:"report_interval_seconds"`
	MaxBatchSize          int    `json:"max_batch_size"`
}

// startRideRequest is the body of POST /api/v1/rider/rides.
type startRideRequest struct {
	TripID            string `json:"trip_id"`
	StartDate         string `json:"start_date"`
	RouteID           string `json:"route_id"`
	VehicleID         string `json:"vehicle_id"`
	BoardingStopID    string `json:"boarding_stop_id"`
	DestinationStopID string `json:"destination_stop_id"`
}

// rideDestination is where the rider said they are getting off, positioned.
type rideDestination struct {
	StopID    string  `json:"stop_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type startRideResponse struct {
	RideID                string           `json:"ride_id"`
	State                 string           `json:"state"`
	ReportIntervalSeconds int              `json:"report_interval_seconds"`
	MaxBatchSize          int              `json:"max_batch_size"`
	Destination           *rideDestination `json:"destination,omitempty"`
}

// endRideRequest is the body of POST /api/v1/rider/rides/{id}/end.
type endRideRequest struct {
	Reason string `json:"reason"`
}

type rideSummaryJSON struct {
	Points          int `json:"points"`
	Matched         int `json:"matched"`
	Corroborated    int `json:"corroborated"`
	DurationSeconds int `json:"duration_seconds"`
}

type endRideResponse struct {
	Status  string          `json:"status"`
	Summary rideSummaryJSON `json:"summary"`
}

// decodeRiderJSON reads one JSON object from the request into dst, rejecting a
// wrong content type, unknown fields and trailing data. It reports whether
// decoding succeeded, having already written the error response if it did not.
func decodeRiderJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, riderMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
		return false
	}
	return true
}

// handleRegister creates (or refreshes) the anonymous rider behind an
// installation id and hands back a session token. A first registration answers
// 201; a returning installation answers 200 with the same rider id.
func (s *riderService) handleRegister() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The limit is checked before the body is read: registration mints
		// credentials, so a flood must cost as little as possible.
		if !s.regLimiter.Allow(clientIP(r, s.trustProxy)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many registrations, try again later"})
			return
		}

		var req riderRegisterRequest
		if !decodeRiderJSON(w, r, &req) {
			return
		}
		if msg, ok := req.validate(); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}

		rd, created, err := s.store.RegisterRider(r.Context(), req.InstallationID, req.Platform, req.AppID, req.AppVersion)
		if err != nil {
			slog.Error("failed to register rider", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		token, err := generateRiderJWT(rd.ID, s.jwtSecret, s.jwtTTL)
		if err != nil {
			slog.Error("failed to sign rider token", "rider_id", rd.ID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, riderRegisterResponse{
			RiderID:               rd.ID,
			Token:                 token,
			ReportIntervalSeconds: s.reportIntervalSeconds,
			MaxBatchSize:          s.maxBatchSize,
		})
	}
}

// validate reports whether the registration is acceptable, and why not.
func (req *riderRegisterRequest) validate() (string, bool) {
	if _, err := uuid.Parse(req.InstallationID); err != nil {
		return "installation_id must be a UUID", false
	}
	if !riderPlatforms[req.Platform] {
		return "platform must be one of ios, android, other", false
	}
	if tooLong(req.AppID) || tooLong(req.AppVersion) {
		return "app_id and app_version must be at most 100 characters", false
	}
	// Attestation is accepted by the schema so clients can be built against
	// the final shape, but nothing here verifies one yet; silently ignoring it
	// would let a client believe it had attested.
	if req.Attestation != nil && !bytes.Equal(bytes.TrimSpace(*req.Attestation), []byte("null")) {
		return "attestation not supported", false
	}
	return "", true
}

// handleStartRide opens a ride on a scheduled trip, superseding whatever ride
// the same rider had open.
func (s *riderService) handleStartRide() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		riderID, ok := riderIDFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		var req startRideRequest
		if !decodeRiderJSON(w, r, &req) {
			return
		}
		if req.TripID == "" || tooLong(req.TripID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id is required"})
			return
		}
		if tooLong(req.RouteID) || tooLong(req.VehicleID) || tooLong(req.BoardingStopID) || tooLong(req.DestinationStopID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "identifiers must be at most 100 characters"})
			return
		}
		if req.StartDate != "" && !serviceDatePattern.MatchString(req.StartDate) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start_date must be YYYYMMDD"})
			return
		}

		index := s.index()
		if index == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedule data unavailable"})
			return
		}
		startDate := req.StartDate
		if startDate == "" {
			startDate = index.ServiceDate(s.now())
		}

		trip, ok := index.Trip(req.TripID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown trip"})
			return
		}
		if !index.ActiveOn(trip, startDate) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "trip not active on start_date"})
			return
		}
		if req.RouteID != "" && req.RouteID != trip.RouteID {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "route_id does not match the trip"})
			return
		}

		// One rider rides one vehicle. The old ride goes through finishRide so
		// its reputation outcome is applied and its session stops publishing.
		if oldID, ok := s.agg.ActiveRideForRider(riderID); ok {
			if old, ok := s.agg.Session(oldID); ok {
				if _, err := s.finishRide(r.Context(), old, rider.EndSuperseded); err != nil {
					slog.Error("failed to supersede the rider's previous ride", "ride_id", oldID, "error", err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
					return
				}
			}
		}

		rd, err := s.store.GetRider(r.Context(), riderID)
		if err != nil {
			slog.Warn("start ride: rider not found", "rider_id", riderID, "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown rider"})
			return
		}

		routeID := req.RouteID
		if routeID == "" {
			routeID = trip.RouteID
		}
		ride := &Ride{
			ID:                uuid.NewString(),
			RiderID:           riderID,
			TripID:            req.TripID,
			StartDate:         startDate,
			RouteID:           routeID,
			VehicleID:         req.VehicleID,
			BoardingStopID:    req.BoardingStopID,
			DestinationStopID: req.DestinationStopID,
		}
		if err := s.store.StartRide(r.Context(), ride); err != nil {
			slog.Error("failed to start ride", "rider_id", riderID, "trip_id", req.TripID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		key := rider.TripKey{TripID: ride.TripID, StartDate: ride.StartDate}
		s.agg.Add(rider.NewSession(ride.ID, riderID, key, trip, rider.ParseTier(rd.Tier), ride.StartedAt))

		writeJSON(w, http.StatusCreated, startRideResponse{
			RideID:                ride.ID,
			State:                 ride.State,
			ReportIntervalSeconds: s.reportIntervalSeconds,
			MaxBatchSize:          s.maxBatchSize,
			Destination:           destinationOf(trip, ride.DestinationStopID),
		})
	}
}

// handleEndRide closes a ride at the client's request.
func (s *riderService) handleEndRide() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		riderID, ok := riderIDFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		var req endRideRequest
		if !decodeRiderJSON(w, r, &req) {
			return
		}
		reason, ok := rider.ParseClientEndReason(req.Reason)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown end reason"})
			return
		}

		rideID := r.PathValue("id")
		owner, live := s.agg.Owner(rideID)
		if !live {
			// A well-formed id we no longer hold is a ride that has ended;
			// anything else never existed.
			if uuid.Validate(rideID) != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "ride not found"})
				return
			}
			writeJSON(w, http.StatusConflict, map[string]string{"error": "ride ended"})
			return
		}
		// Another rider's ride is not theirs to know about.
		if owner != riderID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "ride not found"})
			return
		}
		sess, ok := s.agg.Session(rideID)
		if !ok {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "ride ended"})
			return
		}

		if _, err := s.finishRide(r.Context(), sess, reason); err != nil {
			slog.Error("failed to end ride", "ride_id", rideID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		sum := sess.Summary()
		writeJSON(w, http.StatusOK, endRideResponse{
			Status: "ride ended",
			Summary: rideSummaryJSON{
				Points:          sum.Counts.Total,
				Matched:         sum.Counts.Matched,
				Corroborated:    sum.Counts.Corroborated,
				DurationSeconds: int(sum.Duration.Seconds()),
			},
		})
	}
}

// handlePositions is implemented in Task 10.
func (s *riderService) handlePositions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "not implemented"})
	}
}

// handleTripStatus is implemented in Task 10.
func (s *riderService) handleTripStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "not implemented"})
	}
}

// finishRide persists what a ride amounted to and retires its session. The
// session is only ended and unregistered once the store has accepted the
// outcome: a failed write leaves the ride live, so the caller — or the reaper —
// can try again rather than losing the ride's points and its reputation
// effect. A session that has already ended (rejected, or reaped) keeps its own
// end reason; the first end wins.
func (s *riderService) finishRide(ctx context.Context, sess *rider.Session, reason rider.EndReason) (*Rider, error) {
	if sess.Ended() {
		reason = sess.EndReason()
	}

	summary := sess.Summary()
	summary.EndReason = reason
	outcome := RideOutcome{
		EndReason:    string(reason),
		Progress:     progressFrom(sess),
		ScoreDelta:   rider.ScoreDelta(summary),
		Rejected:     summary.State == rider.Rejected,
		Corroborated: summary.Corroborated,
	}

	updated, err := s.store.FinishRide(ctx, sess.ID(), outcome)
	if err != nil {
		return nil, err
	}

	// Unregister before ending: once the aggregator has let go of the session
	// nothing else can be applying points to it.
	s.agg.Remove(sess.ID())
	sess.End(reason, s.now())
	// A score change lands on the ride the rider has in flight, if the ride
	// that just ended was not it.
	s.agg.SetTier(updated.ID, rider.ParseTier(updated.Tier))
	return updated, nil
}

// progressFrom is the ride progress a session has accumulated.
func progressFrom(sess *rider.Session) RideProgress {
	counts := sess.Counts()
	return RideProgress{
		State:              sess.State().String(),
		Corroborated:       sess.Corroborated(),
		PointsTotal:        counts.Total,
		PointsMatched:      counts.Matched,
		PointsCorroborated: counts.Corroborated,
		PointsContradicted: counts.Contradicted,
	}
}

// destinationOf positions the rider's destination stop on the trip, or returns
// nil when they named a stop the trip does not call at.
func destinationOf(trip *rider.TripInfo, stopID string) *rideDestination {
	if stopID == "" {
		return nil
	}
	for _, st := range trip.StopTimes {
		if st.StopID == stopID {
			return &rideDestination{StopID: st.StopID, Latitude: st.Pos.Lat, Longitude: st.Pos.Lon}
		}
	}
	return nil
}

// tooLong reports whether a client-supplied identifier exceeds what is worth
// storing.
func tooLong(v string) bool { return len(v) > riderMaxFieldLen }

// registerRiderRoutes mounts the rider API. Registration is the only route
// open to an unauthenticated caller.
func registerRiderRoutes(mux *http.ServeMux, s *riderService) {
	auth := requireRider(s.jwtSecret)
	mux.Handle("POST /api/v1/rider/register", s.handleRegister())
	mux.Handle("POST /api/v1/rider/rides", auth(s.handleStartRide()))
	mux.Handle("POST /api/v1/rider/rides/{id}/positions", auth(s.handlePositions()))
	mux.Handle("POST /api/v1/rider/rides/{id}/end", auth(s.handleEndRide()))
	mux.Handle("GET /api/v1/rider/trips/{trip_id}/status", auth(s.handleTripStatus()))
}
