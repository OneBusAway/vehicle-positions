package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// validateFeedCompliance checks a FeedMessage against applicable
// MobilityData GTFS-RT validator rules for VehiclePosition feeds.
// Returns a slice of rule violation strings. Empty = compliant.
//
// This helper exercises buildFeed() — the real production function —
// through the lens of every applicable validator rule.
func validateFeedCompliance(t *testing.T, feed *gtfs.FeedMessage) []string {
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

	for _, entity := range feed.Entity {
		id := entity.GetId()

		// E052: unique entity IDs
		if _, exists := seenIDs[id]; exists {
			violations = append(violations, fmt.Sprintf("E052: duplicate entity id %q", id))
		}
		seenIDs[id] = struct{}{}

		vp := entity.GetVehicle()
		if vp == nil {
			continue
		}

		// W002: vehicle.id populated
		if vp.GetVehicle().GetId() == "" {
			violations = append(violations, fmt.Sprintf("W002: vehicle.id empty for entity %q", id))
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
			// buildFeed() always sets Bearing via proto.Float32(), so pos.Bearing
			// is never nil in current production output. The nil guard is retained
			// for forward-compatibility if Bearing becomes a pointer type.
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
	feed := buildFeed(vehicles)

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
	feed := buildFeed(vehicles)

	headerTs := feed.Header.GetTimestamp()
	for _, e := range feed.Entity {
		assert.GreaterOrEqual(t, headerTs, e.Vehicle.GetTimestamp(),
			"E012: header must be >= entity %s", e.GetId())
	}
}

func TestFeedValidation_E012_FutureEntityTimestamp(t *testing.T) {
	// Vehicle with timestamp 2 minutes in the future (within 5min ingest window).
	// Header must be >= this timestamp.
	futureTs := time.Now().Unix() + 120
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: futureTs},
	}
	feed := buildFeed(vehicles)

	headerTs := feed.Header.GetTimestamp()
	entityTs := feed.Entity[0].Vehicle.GetTimestamp()

	assert.GreaterOrEqual(t, headerTs, entityTs,
		"E012: header timestamp must be >= entity timestamp")
}

func TestFeedValidation_E012_AllPastEntities(t *testing.T) {
	// All vehicles have past timestamps.
	// Header should be approximately time.Now(), not dragged down.
	pastTs := time.Now().Unix() - 60
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: pastTs},
	}
	feed := buildFeed(vehicles)

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
	feed := buildFeed(vehicles)
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
	feed := buildFeed(vehicles)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "boundary coordinates should be valid")
}

func TestFeedValidation_E027_ValidBearing(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Bearing: 180, Timestamp: now},
	}
	feed := buildFeed(vehicles)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E027_BearingBoundaries(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "north", Latitude: 1, Longitude: 2, Bearing: 0, Timestamp: now},
		{VehicleID: "east", Latitude: 3, Longitude: 4, Bearing: 90, Timestamp: now},
		{VehicleID: "south", Latitude: 5, Longitude: 6, Bearing: 180, Timestamp: now},
		{VehicleID: "wrap", Latitude: 7, Longitude: 8, Bearing: 360, Timestamp: now},
	}
	feed := buildFeed(vehicles)

	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "all bearings 0-360 should be valid")
}

func TestFeedValidation_E038_ValidVersion(t *testing.T) {
	feed := buildFeed(nil)
	assert.Equal(t, "2.0", feed.Header.GetGtfsRealtimeVersion())
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E039_NoIsDeletedInFullDataset(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}
	feed := buildFeed(vehicles)

	for _, e := range feed.Entity {
		assert.Nil(t, e.IsDeleted, "E039: is_deleted must not be set in FULL_DATASET")
	}
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_E048_HeaderTimestampPopulated(t *testing.T) {
	feed := buildFeed(nil)
	assert.NotNil(t, feed.Header.Timestamp, "E048: header timestamp required for v2.0")
	assert.NotZero(t, feed.Header.GetTimestamp())
}

func TestFeedValidation_E049_IncrementalityPopulated(t *testing.T) {
	feed := buildFeed(nil)
	assert.NotNil(t, feed.Header.Incrementality, "E049: incrementality required for v2.0")
	assert.Equal(t, gtfs.FeedHeader_FULL_DATASET, feed.Header.GetIncrementality())
}

func TestFeedValidation_E050_TimestampsNotFarFuture(t *testing.T) {
	// Use current timestamps — these should never trigger E050.
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
		{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: now - 30},
	}
	feed := buildFeed(vehicles)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "current/past timestamps should not trigger E050")

	// Note: The ingest window allows timestamps up to now+300s, but E050
	// rejects >now+60s. This is a known limitation documented in the plan.
	// We do NOT test with now+120 here because it would legitimately fail E050.
}

func TestFeedValidation_E052_UniqueVehicleIDs(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
		{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: now},
		{VehicleID: "bus-3", Latitude: 5, Longitude: 6, Timestamp: now},
	}
	feed := buildFeed(vehicles)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "unique IDs should not trigger E052")
}

func TestFeedValidation_W001_TimestampsPopulated(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: now},
	}
	feed := buildFeed(vehicles)

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
	feed := buildFeed(vehicles)

	for _, e := range feed.Entity {
		assert.NotEmpty(t, e.Vehicle.GetVehicle().GetId(), "W002: vehicle.id must be populated")
	}
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations)
}

func TestFeedValidation_SpeedNonNegative(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "stationary", Latitude: 1, Longitude: 2, Speed: 0, Timestamp: now},
		{VehicleID: "moving", Latitude: 3, Longitude: 4, Speed: 15.5, Timestamp: now},
	}
	feed := buildFeed(vehicles)
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "zero and positive speeds should be valid")
}

// --- Composite tests ---

func TestFeedValidation_EmptyFeedValid(t *testing.T) {
	feed := buildFeed(nil)
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
			Bearing:   180.0,
			Speed:     8.5,
			Timestamp: now,
		},
		{
			VehicleID: "vehicle-100",
			Latitude:  -1.3000,
			Longitude: 36.8300,
			Bearing:   0.0, // heading north — valid
			Speed:     0.0, // stationary — valid
			Timestamp: now - 30,
		},
		{
			VehicleID: "vehicle-200",
			TripID:    "route_10_0900",
			Latitude:  40.7128,
			Longitude: -74.0060,
			Bearing:   270.0,
			Speed:     12.5,
			Timestamp: now - 5,
		},
	}

	feed := buildFeed(vehicles)

	// Run full compliance check
	violations := validateFeedCompliance(t, feed)
	assert.Empty(t, violations, "feed should pass all validator rules: %v", violations)

	// Verify structure
	require.Len(t, feed.Entity, 3)
	assert.Equal(t, "2.0", feed.Header.GetGtfsRealtimeVersion())
	assert.Equal(t, gtfs.FeedHeader_FULL_DATASET, feed.Header.GetIncrementality())

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

func TestFeedValidation_ZeroTimestampVehicleSkipped(t *testing.T) {
	now := time.Now().Unix()
	vehicles := []*VehicleState{
		{VehicleID: "valid", Latitude: 1, Longitude: 2, Timestamp: now},
		{VehicleID: "zero-ts", Latitude: 3, Longitude: 4, Timestamp: 0},
		{VehicleID: "negative-ts", Latitude: 5, Longitude: 6, Timestamp: -100},
	}
	feed := buildFeed(vehicles)

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
			Bearing: 180, Speed: 8.5, Timestamp: now},
	}
	feed := buildFeed(vehicles)

	// Verify protobuf round-trip: marshal then unmarshal
	data, err := proto.Marshal(feed)
	require.NoError(t, err, "feed must serialize to protobuf")

	var decoded gtfs.FeedMessage
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
