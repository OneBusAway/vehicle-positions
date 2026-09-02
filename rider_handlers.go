package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// batch of riderMaxBatchSize positions, which is comfortably under this.
	riderMaxBodyBytes = 1 << 16
	// riderMaxFieldLen bounds the free-text identifiers a client may send, so
	// nothing absurd reaches the database.
	riderMaxFieldLen = 100
	// riderBatchInterval and riderBatchBurst rate-limit position reports per
	// rider: one report every riderReportIntervalSeconds, with enough slack for
	// a retry.
	riderBatchInterval = 2 * time.Second
	riderBatchBurst    = 2
)

// errRideNotActive is returned by finishRide when the ride is no longer one
// the server can end: the aggregator has let go of it, or the database says it
// already ended. Either way the client must start a new ride.
var errRideNotActive = errors.New("ride is not active")

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

// positionUpload is one location report from the device. Accuracy, Speed and
// Bearing are pointers because a device that has no reading for them must be
// able to say so rather than claim a zero.
type positionUpload struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Accuracy  *float64 `json:"accuracy"`
	Speed     *float64 `json:"speed"`
	Bearing   *float64 `json:"bearing"`
	Timestamp int64    `json:"timestamp"`
}

// positionsRequest is the body of POST /api/v1/rider/rides/{id}/positions.
type positionsRequest struct {
	Positions []positionUpload `json:"positions"`
}

type positionsResponse struct {
	State          string `json:"state"`
	Published      bool   `json:"published"`
	Corroboration  string `json:"corroboration"`
	Accepted       int    `json:"accepted"`
	Ignored        int    `json:"ignored"`
	OffRouteStreak int    `json:"off_route_streak"`
	Ended          bool   `json:"ended"`
	EndReason      string `json:"end_reason"`
}

type tripStatusResponse struct {
	TripID        string `json:"trip_id"`
	StartDate     string `json:"start_date"`
	Trusted       bool   `json:"trusted"`
	RiderReported bool   `json:"rider_reported"`
	Riders        int    `json:"riders"`
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
		// A ride that has already gone is nothing to supersede.
		if oldID, ok := s.agg.ActiveRideForRider(riderID); ok {
			if _, err := s.finishRide(r.Context(), oldID, rider.EndSuperseded); err != nil && !errors.Is(err, errRideNotActive) {
				slog.Error("failed to supersede the rider's previous ride", "ride_id", oldID, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
		}

		rd, err := s.store.GetRider(r.Context(), riderID)
		if err != nil {
			if errors.Is(err, ErrRiderNotFound) {
				slog.Warn("start ride: rider not found", "rider_id", riderID)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown rider"})
				return
			}
			slog.Error("failed to load rider", "rider_id", riderID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
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

// resolveRide finds the live ride a client is addressing, and reports whether
// the caller may go on. It writes the refusal itself: a ride the aggregator no
// longer holds — or holds only until its outcome is written — has ended, and a
// ride belonging to someone else is one this rider has no business knowing
// exists. Both the end and the positions endpoints start here.
func (s *riderService) resolveRide(w http.ResponseWriter, rideID, riderID string) (rider.RideSnapshot, bool) {
	snap, ok := s.agg.Snapshot(rideID)
	if !ok && uuid.Validate(rideID) != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ride not found"})
		return rider.RideSnapshot{}, false
	}
	if !ok || snap.Ended {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "ride ended"})
		return rider.RideSnapshot{}, false
	}
	if snap.RiderID != riderID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ride not found"})
		return rider.RideSnapshot{}, false
	}
	return snap, true
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

		snap, ok := s.resolveRide(w, r.PathValue("id"), riderID)
		if !ok {
			return
		}
		rideID := snap.ID

		if _, err := s.finishRide(r.Context(), rideID, reason); err != nil {
			if errors.Is(err, errRideNotActive) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "ride ended"})
				return
			}
			slog.Error("failed to end ride", "ride_id", rideID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// finishRide ends the session at s.now(), so this is the duration it
		// recorded. A store-assigned start ahead of the clock is reported as
		// nothing rather than as a negative ride.
		duration := max(s.now().Sub(snap.StartedAt), 0)
		writeJSON(w, http.StatusOK, endRideResponse{
			Status: "ride ended",
			Summary: rideSummaryJSON{
				Points:          snap.Counts.Total,
				Matched:         snap.Counts.Matched,
				Corroborated:    snap.Counts.Corroborated,
				DurationSeconds: int(duration.Seconds()),
			},
		})
	}
}

// handlePositions verifies a batch of positions against the ride's trip. The
// batch is judged whatever the rider's reputation says: a blocked rider gets
// honest verdicts back and simply has nothing recorded or published, so a
// client cannot tell it is being shadowed and stop reporting.
func (s *riderService) handlePositions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		riderID, ok := riderIDFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		// Reporting is the hot path of the whole API: the limit is checked
		// before the body is read, and before the ride is even looked up.
		if !s.batchLimiter.Allow(riderID) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many position reports, slow down"})
			return
		}

		snap, ok := s.resolveRide(w, r.PathValue("id"), riderID)
		if !ok {
			return
		}
		rideID := snap.ID

		var req positionsRequest
		if !decodeRiderJSON(w, r, &req) {
			return
		}
		if len(req.Positions) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "positions must contain at least one position"})
			return
		}
		if len(req.Positions) > s.maxBatchSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many positions in one report"})
			return
		}
		points := make([]rider.Point, 0, len(req.Positions))
		for _, p := range req.Positions {
			if msg, ok := p.validate(); !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
				return
			}
			points = append(points, p.point())
		}

		now := s.now()
		// A trip the agency's own feed can speak for is judged against it; with
		// no feed the engine has only the schedule and the shape to go on.
		var lookup func(rider.TripKey) (rider.TrustedVehicle, bool)
		if s.trusted != nil && s.trusted.Configured() {
			lookup = func(k rider.TripKey) (rider.TrustedVehicle, bool) { return s.trusted.Lookup(k, now) }
		}
		res, err := s.agg.ApplyBatch(rideID, points, lookup, now)
		if err != nil {
			// The ride ended, or was superseded onto another trip, while the
			// batch was in flight.
			if errors.Is(err, rider.ErrUnknownRide) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "ride ended"})
				return
			}
			slog.Error("failed to apply rider positions", "ride_id", rideID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Nothing a blocked rider reports is stored: their points are neither
		// evidence nor a position, so keeping them would only cost storage. A
		// batch that produced no points has nothing to say either: the counters
		// are exactly as the store already has them.
		var storeErr error
		if snap.Tier != rider.TierBlocked && len(res.Points) > 0 {
			progress := progressOf(res.State, res.Corroborated, res.Counts)
			// The session has already advanced, so this batch cannot be
			// replayed; the counters are absolute, so the next successful write
			// heals the record.
			if storeErr = s.store.RecordRidePoints(r.Context(), rideID, riderID, ridePointRecords(res.Points), progress); storeErr != nil {
				slog.Error("failed to record ride points", "ride_id", rideID, "error", storeErr)
			}
		}

		// The engine ended the ride itself: it was rejected, or contradicted.
		// This happens whether or not the batch could be stored — a ride the
		// engine has finished with must be filed and its reputation effect
		// applied, or it lingers in the aggregator until the reaper notices.
		if res.Ended {
			if _, err := s.finishRide(r.Context(), rideID, res.EndReason); err != nil && !errors.Is(err, errRideNotActive) {
				slog.Error("failed to finish a ride the engine ended", "ride_id", rideID, "error", err)
			}
		}

		if storeErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, positionsResponse{
			State:          res.State.String(),
			Published:      res.Published,
			Corroboration:  res.Corroboration.String(),
			Accepted:       res.Accepted,
			Ignored:        res.Ignored,
			OffRouteStreak: res.OffRouteStreak,
			Ended:          res.Ended,
			EndReason:      string(res.EndReason),
		})
	}
}

// handleTripStatus reports what is known about one trip: whether the agency's
// own feed covers it, and what its riders add up to.
func (s *riderService) handleTripStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		index := s.index()
		if index == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedule data unavailable"})
			return
		}

		now := s.now()
		startDate := r.URL.Query().Get("start_date")
		if startDate == "" {
			startDate = index.ServiceDate(now)
		} else if !serviceDatePattern.MatchString(startDate) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start_date must be YYYYMMDD"})
			return
		}

		tripID := r.PathValue("trip_id")
		if _, ok := index.Trip(tripID); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown trip"})
			return
		}

		key := rider.TripKey{TripID: tripID, StartDate: startDate}
		covered := s.coveredFunc(now)
		riderReported, riders := s.agg.TripStatus(key, now, covered)
		writeJSON(w, http.StatusOK, tripStatusResponse{
			TripID:        tripID,
			StartDate:     startDate,
			Trusted:       covered != nil && covered(key),
			RiderReported: riderReported,
			Riders:        riders,
		})
	}
}

// Estimates is the rider-derived vehicle position of every trip the riders can
// vouch for and the agency's own feed does not already cover.
func (s *riderService) Estimates(now time.Time) []rider.TripEstimate {
	return s.agg.Estimates(now, s.coveredFunc(now))
}

// RiderStatus is the rider subsystem's account of itself for the admin status
// endpoint: the schedule it is matching against, the trusted feeds it is
// checked against, its riders and the rides it is holding. now is the moment
// the ride counts are judged at, since a session only counts while its points
// are fresh.
func (s *riderService) RiderStatus(ctx context.Context, now time.Time) (riderStatusResponse, error) {
	status := riderStatusResponse{Enabled: true}

	// The index is nil until the first refresh has landed; the rest of the
	// status is still worth reporting while that is true.
	if index := s.index(); index != nil {
		stats := index.Stats()
		status.GTFS = &riderGTFSStatus{
			Source:   stats.Source,
			LoadedAt: adminRiderTime(stats.LoadedAt),
			Trips:    stats.Trips,
			Shapes:   stats.Shapes,
			Timezone: index.Timezone().String(),
		}
	}

	if s.trusted != nil && s.trusted.Configured() {
		status.TrustedFeeds = riderFeedStatuses(s.trusted.Health())
	}

	byTier, err := s.store.CountRidersByTier(ctx)
	if err != nil {
		return riderStatusResponse{}, err
	}
	counts := riderTierCounts{
		Trusted: byTier[string(rider.TierTrusted)],
		Blocked: byTier[string(rider.TierBlocked)],
	}
	// Every tier counts towards the total, including any the store knows about
	// and this build does not.
	for _, n := range byTier {
		counts.Total += n
	}
	status.Riders = &counts

	status.Rides = &riderRideCounts{
		Active:      s.agg.ActiveCount(),
		Publishable: s.agg.PublishableCount(now, s.coveredFunc(now)),
	}
	return status, nil
}

// coveredFunc reports, for a trip, whether the trusted feed already speaks for
// it at now. It returns nil when there is no trusted feed to ask, which is what
// the aggregator expects for "nothing is covered".
func (s *riderService) coveredFunc(now time.Time) func(rider.TripKey) bool {
	if s.trusted == nil || !s.trusted.Configured() {
		return nil
	}
	return func(k rider.TripKey) bool { return s.trusted.Covers(k, now) }
}

// validate reports whether one uploaded position is usable, and why not. The
// engine judges everything else; this only rejects what could not have come
// from a working device.
func (p positionUpload) validate() (string, bool) {
	if p.Timestamp <= 0 {
		return "each position needs a timestamp", false
	}
	// Coordinates off the globe cannot be judged at all. A zeroed fix is left
	// to the engine, which ignores null island along with every other point it
	// cannot use.
	if p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 {
		return "latitude and longitude must be valid coordinates", false
	}
	return "", true
}

// point is the upload as the engine takes it, with -1 standing in for each
// optional the device did not report.
func (p positionUpload) point() rider.Point {
	return rider.Point{
		Pos:       rider.LatLon{Lat: p.Latitude, Lon: p.Longitude},
		Accuracy:  orMissing(p.Accuracy),
		Speed:     orMissing(p.Speed),
		Bearing:   orMissing(p.Bearing),
		Timestamp: time.Unix(p.Timestamp, 0),
	}
}

// orMissing is the engine's sentinel for an optional reading the device did not
// supply.
func orMissing(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}

// reported turns the engine's -1 sentinel back into an absent value, so a
// reading the device never made is stored as NULL rather than as a number.
// Only the sentinel itself is absent: a genuinely negative reading is the
// device's, and stays.
func reported(v float64) *float64 {
	if v == -1 {
		return nil
	}
	return &v
}

// ridePointRecords is a batch of verified points as persistence takes them.
func ridePointRecords(points []rider.AppliedPoint) []RidePointRecord {
	out := make([]RidePointRecord, 0, len(points))
	for _, p := range points {
		out = append(out, RidePointRecord{
			Latitude:                 p.Pos.Lat,
			Longitude:                p.Pos.Lon,
			Accuracy:                 reported(p.Accuracy),
			Speed:                    reported(p.Speed),
			Bearing:                  reported(p.Bearing),
			Timestamp:                p.Timestamp.Unix(),
			Outcome:                  p.Verdict.Outcome.String(),
			Corroboration:            p.Verdict.Corroboration.String(),
			AlongShape:               p.Verdict.AlongShape,
			DistanceToShape:          p.Verdict.DistanceToShape,
			ScheduleDeviationSeconds: int(p.Verdict.ScheduleDeviation.Seconds()),
		})
	}
	return out
}

// finishRide persists what a ride amounted to and retires its session. It
// works from a snapshot rather than the session itself: a registered session is
// only ever read under the aggregator's lock, because points may still be being
// applied to it. The session is ended and unregistered only once the store has
// accepted the outcome — a failed write leaves the ride live so the caller, or
// the reaper, can try again rather than losing the ride's points and its
// reputation effect. A session that ended itself (rejected, or reaped) keeps
// its own end reason; the first end wins.
func (s *riderService) finishRide(ctx context.Context, rideID string, reason rider.EndReason) (*Rider, error) {
	snap, ok := s.agg.Snapshot(rideID)
	if !ok {
		return nil, errRideNotActive
	}
	if snap.Ended {
		reason = snap.EndReason
	}

	summary := snap.Summary
	summary.EndReason = reason
	outcome := RideOutcome{
		EndReason:    string(reason),
		Progress:     progressFrom(snap),
		ScoreDelta:   rider.ScoreDelta(summary),
		Rejected:     snap.State == rider.Rejected,
		Corroborated: snap.Corroborated,
	}

	updated, err := s.store.FinishRide(ctx, rideID, outcome)
	if err != nil {
		// The row has already ended: this session is stale, and retrying it
		// forever would only keep it registered.
		if errors.Is(err, ErrRideNotFound) {
			s.agg.Remove(rideID)
			return nil, errRideNotActive
		}
		return nil, err
	}

	// Unregistering first is what makes ending the session safe: once the
	// aggregator has let go of it, nothing else holds it.
	sess := s.agg.Remove(rideID)
	if sess != nil && !snap.Ended {
		sess.End(reason, s.now())
	}
	// A score change lands on the ride the rider has in flight, if the ride
	// that just ended was not it.
	s.agg.SetTier(updated.ID, rider.ParseTier(updated.Tier))
	return updated, nil
}

// progressFrom is the ride progress a snapshot has accumulated.
func progressFrom(snap rider.RideSnapshot) RideProgress {
	return progressOf(snap.State, snap.Corroborated, snap.Counts)
}

// progressOf is the ride progress a state, corroboration flag and set of counts
// amount to. The counts are absolute, so writing them heals a record whose
// earlier write failed.
func progressOf(state rider.State, corroborated bool, counts rider.Counts) RideProgress {
	return RideProgress{
		State:              state.String(),
		Corroborated:       corroborated,
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
