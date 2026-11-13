package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// validateFeedCompliance checks a FeedMessage against applicable
// MobilityData GTFS-RT validator rules for VehiclePosition feeds.
// Returns a slice of rule violation strings. Empty = compliant.
//
// Callers typically build a feed via buildFeed() and pass the result here.
func validateFeedCompliance(t *testing.T, feed *gtfsrt.FeedMessage) []string {
	t.Helper()
	var violations []string

	// E038: valid GTFS-RT version
	if v := feed.Header.GetGtfsRealtimeVersion(); v != "1.0" && v != "2.0" {
		violations = append(violations, "E038: invalid gtfs_realtime_version: "+v)
	}

	// E048: header timestamp required for v2.0
	if feed.Header.Timestamp == nil || *feed.Header.Timestamp == 0 {
		violations = append(violations, "E048: header timestamp missing or zero")
	}

	// E049: incrementality required for v2.0
	if feed.Header.Incrementality == nil {
		violations = append(violations, "E049: incrementality missing")
	}

	headerTs := feed.Header.GetTimestamp()
	now := uint64(time.Now().Unix())
	seenIDs := make(map[string]struct{})
	seenVehicleIDs := make(map[string]string)

	for _, entity := range feed.Entity {
		id := entity.GetId()

		// Unique entity ids (GTFS-RT spec requirement; no numbered rule).
		if _, exists := seenIDs[id]; exists {
			violations = append(violations, fmt.Sprintf("duplicate entity id %q", id))
		}
		seenIDs[id] = struct{}{}

		vp := entity.GetVehicle()
		if vp == nil {
			continue
		}

		// W002: vehicle.id populated
		vehicleID := vp.GetVehicle().GetId()
		if vehicleID == "" {
			violations = append(violations, fmt.Sprintf("W002: vehicle.id empty for entity %q", id))
		} else {
			// E052: vehicle.id must be unique across the feed — two entities
			// describing the same vehicle at once is the error MobilityData
			// names. Driver ids cannot contain ":" (vehicleIDPattern), and
			// rider ids carry the trip and the start date, so the two halves
			// of the feed cannot collide.
			if prev, exists := seenVehicleIDs[vehicleID]; exists {
				violations = append(violations, fmt.Sprintf("E052: vehicle.id %q not unique (entities %q and %q)", vehicleID, prev, id))
			}
			seenVehicleIDs[vehicleID] = id
		}

		// W001: timestamp populated
		entityTs := vp.GetTimestamp()
		if entityTs == 0 {
			violations = append(violations, fmt.Sprintf("W001: timestamp zero for entity %q", id))
		}

		// E001: POSIX seconds (not milliseconds)
		if entityTs > 10_000_000_000 {
			violations = append(violations, fmt.Sprintf("E001: timestamp looks like ms for entity %q", id))
		}

		// E012: header timestamp >= entity timestamp
		if entityTs > headerTs {
			violations = append(violations, fmt.Sprintf("E012: entity %q ts %d > header ts %d", id, entityTs, headerTs))
		}

		// E050: not >60s in future
		if entityTs > now+60 {
			violations = append(violations, fmt.Sprintf("E050: entity %q timestamp >60s in future", id))
		}

		pos := vp.GetPosition()
		if pos != nil {
			// E026: valid WGS84 coordinates
			if lat := pos.GetLatitude(); lat < -90 || lat > 90 {
				violations = append(violations, fmt.Sprintf("E026: latitude %f out of range for %q", lat, id))
			}
			if lon := pos.GetLongitude(); lon < -180 || lon > 180 {
				violations = append(violations, fmt.Sprintf("E026: longitude %f out of range for %q", lon, id))
			}

			// E027: bearing 0..360 (inclusive per MobilityData E027 rule).
			// Bearing is optional; buildFeed() leaves pos.Bearing nil when the
			// source vehicle has no bearing, so the nil guard is required.
			if pos.Bearing != nil {
				if b := pos.GetBearing(); b < 0 || b > 360 {
					violations = append(violations, fmt.Sprintf("E027: bearing %f out of range for %q", b, id))
				}
			}

			// Speed non-negative (no formal validator rule number).
			if pos.Speed != nil && pos.GetSpeed() < 0 {
				violations = append(violations, fmt.Sprintf("speed: must be non-negative for entity %q", id))
			}
		}

		// E039: no is_deleted in FULL_DATASET
		if entity.IsDeleted != nil && *entity.IsDeleted {
			violations = append(violations, fmt.Sprintf("E039: is_deleted set for %q", id))
		}

		// Project rule: a TripDescriptor must identify something. A descriptor
		// with neither trip_id nor route_id is unmatchable by any consumer.
		if trip := vp.GetTrip(); trip != nil {
			if trip.GetTripId() == "" && trip.GetRouteId() == "" {
				violations = append(violations, fmt.Sprintf("entity %s: TripDescriptor has neither trip_id nor route_id", id))
			}
			if sd := trip.GetStartDate(); sd != "" && !serviceDatePattern.MatchString(sd) {
				violations = append(violations, fmt.Sprintf("entity %s: start_date %q is not YYYYMMDD", id, sd))
			}
		}

		// Project rules: current_stop_sequence indexes the trip's stop_times,
		// so without a trip_id no consumer can tell which stop it names, and
		// gtfs-realtime.proto ignores current_status without it.
		if vp.CurrentStopSequence != nil && vp.GetTrip().GetTripId() == "" {
			violations = append(violations, fmt.Sprintf("entity %s: current_stop_sequence without a trip_id", id))
		}
		if vp.CurrentStatus != nil && vp.CurrentStopSequence == nil {
			violations = append(violations, fmt.Sprintf("entity %s: current_status without current_stop_sequence", id))
		}
	}

	// E050: header timestamp not >60s in future
	if headerTs > now+60 {
		violations = append(violations, "E050: header timestamp >60s in future")
	}

	return violations
}

// --- Individual rule tests ---

func TestFeedValidation_E001_TimestampsInPOSIXSeconds(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)

	entityTs := feed.Entity[0].Vehicle.GetTimestamp()
	assert.Less(t, entityTs, uint64(10_000_000_000), "E001: timestamp should be in seconds, not ms")

	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E012_HeaderTimestampGEEntityTimestamps(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now - 30},
		{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: now - 10},
	}
	feed := buildFeed(vehicles, nil)

	headerTs := feed.Header.GetTimestamp()
	for _, e := range feed.Entity {
		assert.GreaterOrEqual(t, headerTs, e.Vehicle.GetTimestamp(),
			"E012: header must be >= entity %s", e.GetId())
	}
}

func TestFeedValidation_E012_FutureEntityTimestamp(t *testing.T) {
	// Use now+30 — within E050's 60s window — so the full compliance
	// checker can run without triggering E050 on the header.
	futureTs := time.Now().Unix() + 30
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: futureTs},
	}
	feed := buildFeed(vehicles, nil)

	headerTs := feed.Header.GetTimestamp()
	entityTs := feed.Entity[0].Vehicle.GetTimestamp()

	assert.GreaterOrEqual(t, headerTs, entityTs,
		"E012: header timestamp must be >= entity timestamp")

	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E012_AllPastEntities(t *testing.T) {
	// All vehicles have past timestamps.
	// Header should be approximately time.Now(), not dragged down.
	pastTs := time.Now().Unix() - 60
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: pastTs},
	}
	feed := buildFeed(vehicles, nil)

	headerTs := feed.Header.GetTimestamp()
	now := uint64(time.Now().Unix())

	// Header should be close to now, not the past entity timestamp.
	// Delta of 5 seconds accounts for CI environments under load.
	assert.InDelta(t, float64(now), float64(headerTs), 5, "header should be close to now for past entities")
	assert.GreaterOrEqual(t, headerTs, uint64(pastTs), "E012: header >= entity")
}

func TestFeedValidation_E026_ValidCoordinates(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "nairobi", Latitude: -1.2921, Longitude: 36.8219, Timestamp: now},
		{VehicleID: "nyc", Latitude: 40.7128, Longitude: -74.0060, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E026_BoundaryCoordinates(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "north-pole", Latitude: 90, Longitude: 0.001, Timestamp: now},
		{VehicleID: "south-pole", Latitude: -90, Longitude: 0.001, Timestamp: now},
		{VehicleID: "dateline-east", Latitude: 0.001, Longitude: 180, Timestamp: now},
		{VehicleID: "dateline-west", Latitude: 0.001, Longitude: -180, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "boundary coordinates should be valid")
}

func TestFeedValidation_E027_ValidBearing(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Bearing: float64ptr(180), Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E027_BearingBoundaries(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "north", Latitude: 1, Longitude: 2, Bearing: float64ptr(0), Timestamp: now},
		{VehicleID: "east", Latitude: 3, Longitude: 4, Bearing: float64ptr(90), Timestamp: now},
		{VehicleID: "south", Latitude: 5, Longitude: 6, Bearing: float64ptr(180), Timestamp: now},
		{VehicleID: "wrap", Latitude: 7, Longitude: 8, Bearing: float64ptr(360), Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)

	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "all bearings 0-360 should be valid")
}

func TestFeedValidation_E038_ValidVersion(t *testing.T) {
	feed := buildFeed(nil, nil)
	assert.Equal(t, "2.0", feed.Header.GetGtfsRealtimeVersion())
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E039_NoIsDeletedInFullDataset(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)

	for _, e := range feed.Entity {
		assert.Nil(t, e.IsDeleted, "E039: is_deleted must not be set in FULL_DATASET")
	}
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E048_HeaderTimestampPopulated(t *testing.T) {
	feed := buildFeed(nil, nil)
	assert.NotNil(t, feed.Header.Timestamp, "E048: header timestamp required for v2.0")
	assert.NotZero(t, feed.Header.GetTimestamp())
}

func TestFeedValidation_E049_IncrementalityPopulated(t *testing.T) {
	feed := buildFeed(nil, nil)
	assert.NotNil(t, feed.Header.Incrementality, "E049: incrementality required for v2.0")
	assert.Equal(t, gtfsrt.FeedHeader_FULL_DATASET, feed.Header.GetIncrementality())
}

func TestFeedValidation_E050_TimestampsNotFarFuture(t *testing.T) {
	// Use current timestamps — these should never trigger E050.
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
		{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: now - 30},
	}
	feed := buildFeed(vehicles, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "current/past timestamps should not trigger E050")
}

func TestFeedValidation_E050_RejectsEntityFarInFuture(t *testing.T) {
	now := time.Now().Unix()
	feed := buildFeed([]*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}, nil)
	// buildFeed caps entity timestamps at now+60, so set this one by hand.
	feed.Entity[0].Vehicle.Timestamp = proto.Uint64(uint64(now + 120))

	violations := validateFeedCompliance(t, feed)
	assert.Contains(t, violations, `E050: entity "bus-1" timestamp >60s in future`)
}

func TestFeedValidation_E050_IngestWindowGap(t *testing.T) {
	// Ingest accepts a report up to now+300s, but E050 rejects anything past
	// now+60s, so buildFeed caps the entity and the header follows it.
	now := time.Now().Unix()
	fast := &LocationReport{VehicleID: "fast-clock", Latitude: 1, Longitude: 2, Timestamp: now + 120}
	require.NoError(t, fast.validate(), "ingest must accept a report at now+120")
	onTime := &LocationReport{VehicleID: "on-time", Latitude: 3, Longitude: 4, Timestamp: now - 10}
	require.NoError(t, onTime.validate())

	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	tracker.Update(fast)
	tracker.Update(onTime)

	before := uint64(time.Now().Unix())
	feed := buildFeed(tracker.ActiveVehicles(), nil)
	after := uint64(time.Now().Unix())

	assert.Empty(t, validateFeedCompliance(t, feed))
	require.Len(t, feed.Entity, 2)
	for _, e := range feed.Entity {
		switch e.GetId() {
		case "fast-clock":
			ts := e.GetVehicle().GetTimestamp()
			assert.GreaterOrEqual(t, ts, before+60, "capped at now+60, not pulled back further")
			assert.LessOrEqual(t, ts, after+60)
		case "on-time":
			assert.Equal(t, uint64(now-10), e.GetVehicle().GetTimestamp(), "a timestamp under the cap is left alone")
		default:
			t.Errorf("unexpected entity %q", e.GetId())
		}
	}
}

func TestFeedValidation_E052_UniqueVehicleIDs(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
		{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: now},
		{VehicleID: "bus-3", Latitude: 5, Longitude: 6, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "unique IDs should not trigger E052")
}

func TestFeedValidation_E052_RejectsDuplicateIDs(t *testing.T) {
	now := time.Now().Unix()
	feed := buildFeed([]*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}, nil)
	// The Tracker dedups by VehicleID upstream (tracker.go), so buildFeed
	// never emits duplicate IDs in practice — inject a raw entity to exercise E052.
	dup := &gtfsrt.FeedEntity{
		Id: proto.String("bus-1"),
		Vehicle: &gtfsrt.VehiclePosition{
			Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("bus-1")},
			Position:  &gtfsrt.Position{Latitude: proto.Float32(1), Longitude: proto.Float32(2)},
			Timestamp: proto.Uint64(uint64(now)),
		},
	}
	feed.Entity = append(feed.Entity, dup)

	violations := validateFeedCompliance(t, feed)
	require.NotEmpty(t, violations)
	joined := strings.Join(violations, "|")
	assert.Contains(t, joined, "E052:")
	assert.Contains(t, joined, "bus-1")
}

func TestFeedValidation_E052_RiderVehicleIDsUniqueAcrossStartDates(t *testing.T) {
	// The same trip running on two service dates is two vehicles. When
	// vehicle.id was just "rider:<trip_id>" both entities claimed one vehicle.
	yesterday := sampleEstimate()
	yesterday.Key.StartDate = "20260901"
	feed := buildFeed(nil, []rider.TripEstimate{sampleEstimate(), yesterday})
	require.Len(t, feed.Entity, 2)
	assert.Equal(t, "rider:T1:20260902", feed.Entity[0].GetVehicle().GetVehicle().GetId())
	assert.Equal(t, "rider:T1:20260901", feed.Entity[1].GetVehicle().GetVehicle().GetId())
	assert.Empty(t, validateFeedCompliance(t, feed))
}

func TestFeedValidation_E052_RejectsDuplicateVehicleIDs(t *testing.T) {
	now := time.Now().Unix()
	feed := buildFeed([]*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}, nil)
	// Distinct entity id, same vehicle.id: exactly what E052 forbids.
	feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
		Id: proto.String("bus-1-second-entity"),
		Vehicle: &gtfsrt.VehiclePosition{
			Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("bus-1")},
			Position:  &gtfsrt.Position{Latitude: proto.Float32(1), Longitude: proto.Float32(2)},
			Timestamp: proto.Uint64(uint64(now)),
		},
	})

	violations := validateFeedCompliance(t, feed)
	require.NotEmpty(t, violations)
	assert.Contains(t, strings.Join(violations, "|"), `E052: vehicle.id "bus-1" not unique`)
}

func TestFeedValidation_W001_TimestampsPopulated(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)

	for _, e := range feed.Entity {
		assert.NotZero(t, e.Vehicle.GetTimestamp(), "W001: entity timestamp must be populated")
	}
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_W002_VehicleIDPopulated(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)

	for _, e := range feed.Entity {
		assert.NotEmpty(t, e.Vehicle.GetVehicle().GetId(), "W002: vehicle.id must be populated")
	}
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_SpeedNonNegative(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "stationary", Latitude: 1, Longitude: 2, Speed: float64ptr(0), Timestamp: now},
		{VehicleID: "moving", Latitude: 3, Longitude: 4, Speed: float64ptr(15.5), Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "zero and positive speeds should be valid")
}

// buildFeed cannot produce any of these, so each case breaks one field of a
// compliant feed to prove the helper would catch that regression.
func TestFeedValidation_RejectsBrokenFeed(t *testing.T) {
	cases := []struct {
		name      string
		breakFeed func(*gtfsrt.FeedMessage)
		want      string
	}{
		{
			name:      "E038 unknown version",
			breakFeed: func(f *gtfsrt.FeedMessage) { f.Header.GtfsRealtimeVersion = proto.String("3.0") },
			want:      "E038: invalid gtfs_realtime_version: 3.0",
		},
		{
			name:      "E048 header timestamp missing",
			breakFeed: func(f *gtfsrt.FeedMessage) { f.Header.Timestamp = nil },
			want:      "E048: header timestamp missing or zero",
		},
		{
			name:      "E048 header timestamp zero",
			breakFeed: func(f *gtfsrt.FeedMessage) { f.Header.Timestamp = proto.Uint64(0) },
			want:      "E048: header timestamp missing or zero",
		},
		{
			name:      "E049 incrementality missing",
			breakFeed: func(f *gtfsrt.FeedMessage) { f.Header.Incrementality = nil },
			want:      "E049: incrementality missing",
		},
		{
			name:      "E039 is_deleted in a full dataset",
			breakFeed: func(f *gtfsrt.FeedMessage) { f.Entity[0].IsDeleted = proto.Bool(true) },
			want:      `E039: is_deleted set for "bus-1"`,
		},
		{
			name: "E050 header far in future",
			breakFeed: func(f *gtfsrt.FeedMessage) {
				f.Header.Timestamp = proto.Uint64(uint64(time.Now().Unix() + 120))
			},
			want: "E050: header timestamp >60s in future",
		},
		{
			name:      "stop sequence without a trip",
			breakFeed: func(f *gtfsrt.FeedMessage) { f.Entity[0].Vehicle.CurrentStopSequence = proto.Uint32(2) },
			want:      "entity bus-1: current_stop_sequence without a trip_id",
		},
		{
			name: "stop status without a stop sequence",
			breakFeed: func(f *gtfsrt.FeedMessage) {
				f.Entity[0].Vehicle.CurrentStatus = gtfsrt.VehiclePosition_STOPPED_AT.Enum()
			},
			want: "entity bus-1: current_status without current_stop_sequence",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			feed := buildFeed([]*VehicleState{
				{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()},
			}, nil)
			require.Empty(t, validateFeedCompliance(t, feed), "the feed must be compliant before it is broken")

			tc.breakFeed(feed)
			assert.Contains(t, validateFeedCompliance(t, feed), tc.want)
		})
	}
}

// --- Composite tests ---

func TestFeedValidation_EmptyFeedValid(t *testing.T) {
	feed := buildFeed(nil, nil)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "empty feed should be valid: %v", violations)

	assert.Equal(t, "2.0", feed.Header.GetGtfsRealtimeVersion())
	assert.NotNil(t, feed.Header.Timestamp)
	assert.NotZero(t, feed.Header.GetTimestamp())
	assert.Empty(t, feed.Entity)
}

func TestFeedValidation_FullFeedCompliance(t *testing.T) {
	// 3 realistic vehicles — mix of with/without trips, varying fields.
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{
			VehicleID: "vehicle-042",
			TripID:    "route_5_0830",
			Latitude:  -1.2921,
			Longitude: 36.8219,
			Bearing:   float64ptr(180.0),
			Speed:     float64ptr(8.5),
			Timestamp: now,
		},
		{
			VehicleID: "vehicle-100",
			Latitude:  -1.3000,
			Longitude: 36.8300,
			Bearing:   float64ptr(0.0), // heading north — valid
			Speed:     float64ptr(0.0), // stationary — valid
			Timestamp: now - 30,
		},
		{
			VehicleID: "vehicle-200",
			TripID:    "route_10_0900",
			Latitude:  40.7128,
			Longitude: -74.0060,
			Bearing:   float64ptr(270.0),
			Speed:     float64ptr(12.5),
			Timestamp: now - 5,
		},
	}

	feed := buildFeed(vehicles, nil)

	// Run full compliance check
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "feed should pass all validator rules: %v", violations)

	// Verify structure
	require.Len(t, feed.Entity, 3)
	assert.Equal(t, "2.0", feed.Header.GetGtfsRealtimeVersion())
	assert.Equal(t, gtfsrt.FeedHeader_FULL_DATASET, feed.Header.GetIncrementality())

	// Verify trip assignment
	var withTrip, withoutTrip int
	for _, e := range feed.Entity {
		if e.Vehicle.Trip != nil {
			withTrip++
		} else {
			withoutTrip++
		}
	}
	assert.Equal(t, 2, withTrip, "two vehicles should have trips")
	assert.Equal(t, 1, withoutTrip, "one vehicle should have no trip")
}

func TestFeedValidation_DriverStopFields(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "in-transit", TripID: "T1", RouteID: "R1", StartDate: "20260902", Latitude: 47.602, Longitude: -122.33,
			StopID: "ST2", StopSequence: 2, Timestamp: now},
		{VehicleID: "stopped", TripID: "T1", RouteID: "R1", StartDate: "20260902", Latitude: 47.6045, Longitude: -122.33,
			StopID: "ST2", StopSequence: 2, StoppedAt: true, Timestamp: now},
		// A GTFS stop_sequence may be 0; StopID, not the sequence, says
		// whether the vehicle was placed.
		{VehicleID: "first-stop", TripID: "T1", Latitude: 47.6, Longitude: -122.33,
			StopID: "ST1", StopSequence: 0, StoppedAt: true, Timestamp: now},
		{VehicleID: "unplaced", TripID: "T1", Latitude: 47.7, Longitude: -122.33, Timestamp: now},
	}
	feed := buildFeed(vehicles, []rider.TripEstimate{sampleEstimate()})
	assert.Empty(t, validateFeedCompliance(t, feed))

	byID := make(map[string]*gtfsrt.VehiclePosition)
	for _, e := range feed.Entity {
		byID[e.GetId()] = e.GetVehicle()
	}
	require.Len(t, byID, 5)
	assert.Equal(t, "ST2", byID["in-transit"].GetStopId())
	assert.Equal(t, uint32(2), byID["in-transit"].GetCurrentStopSequence())
	assert.Equal(t, gtfsrt.VehiclePosition_IN_TRANSIT_TO.Enum(), byID["in-transit"].CurrentStatus)
	assert.Equal(t, gtfsrt.VehiclePosition_STOPPED_AT.Enum(), byID["stopped"].CurrentStatus)
	assert.Equal(t, proto.Uint32(0), byID["first-stop"].CurrentStopSequence)
	assert.Equal(t, gtfsrt.VehiclePosition_STOPPED_AT.Enum(), byID["first-stop"].CurrentStatus)
	assert.Nil(t, byID["unplaced"].StopId)
	assert.Nil(t, byID["unplaced"].CurrentStopSequence)
	assert.Nil(t, byID["unplaced"].CurrentStatus)
}

func TestFeedValidation_ZeroTimestampVehicleSkipped(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "valid", Latitude: 1, Longitude: 2, Timestamp: now},
		{VehicleID: "zero-ts", Latitude: 3, Longitude: 4, Timestamp: 0},
		{VehicleID: "negative-ts", Latitude: 5, Longitude: 6, Timestamp: -100},
	}
	feed := buildFeed(vehicles, nil)

	// Only the valid vehicle should appear in the feed
	require.Len(t, feed.Entity, 1)
	assert.Equal(t, "valid", feed.Entity[0].GetId())

	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_FeedSerializesCleanly(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", TripID: "trip-1", Latitude: -1.29, Longitude: 36.82,
			Bearing: float64ptr(180), Speed: float64ptr(8.5), Timestamp: now},
	}
	feed := buildFeed(vehicles, nil)

	// Verify protobuf round-trip: marshal then unmarshal
	data, err := proto.Marshal(feed)
	require.NoError(t, err, "feed must serialize to protobuf")

	var decoded gtfsrt.FeedMessage
	err = proto.Unmarshal(data, &decoded)
	require.NoError(t, err, "feed must deserialize from protobuf")

	require.Len(t, decoded.Entity, 1)
	assert.Equal(t, "bus-1", decoded.Entity[0].GetId())
	assert.Equal(t, float32(-1.29), decoded.Entity[0].Vehicle.Position.GetLatitude())
	assert.Equal(t, "trip-1", decoded.Entity[0].Vehicle.Trip.GetTripId())

	// Verify the deserialized feed also passes compliance
	violations := validateFeedCompliance(t, &decoded)
	assert.Empty(t, violations, "round-tripped feed should be compliant: %v", violations)
}

func TestFeedValidation_TripDescriptorNeedsTripOrRoute(t *testing.T) {
	now := time.Now().Unix()
	good := buildFeed([]*VehicleState{{VehicleID: "a", RouteID: "R1", StartDate: "20260906", Latitude: 1, Longitude: 1, Timestamp: now}}, nil)
	assert.Empty(t, validateFeedCompliance(t, good))

	// Hand-build a bad descriptor: buildFeed can no longer produce one.
	bad := buildFeed([]*VehicleState{{VehicleID: "b", Latitude: 1, Longitude: 1, Timestamp: now}}, nil)
	bad.Entity[0].Vehicle.Trip = &gtfsrt.TripDescriptor{StartDate: proto.String("20260906")}
	v := validateFeedCompliance(t, bad)
	require.NotEmpty(t, v)
	assert.Contains(t, v[0], "neither trip_id nor route_id")
}
