# Product Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the driver-reported GTFS-RT feed carry a correct `TripDescriptor` (route id, optional GTFS trip id, service date) end to end from the Android app through the server, then close the remaining pre-launch gaps: password changes over the JSON admin API, and the production deployment guide and operator manual promised in the README's Milestone 5.

**Architecture:** The location report (`POST /api/v1/locations`) gains two optional fields, `route_id` and `start_date`, alongside the existing `trip_id`. The in-memory `Tracker` carries them and `buildFeed` emits a `TripDescriptor` whenever a trip id or route id is present. The Android app stops copying the route id into `trip_id`; it sends the GTFS trip id only when the driver entered one, and always sends `route_id` and the device-local service date. The rider-mode entities already do this correctly and are untouched. iOS needs no change: `VehiclePositionsKit` is the rider SDK, never posts to `/api/v1/locations`, and its `TripDescriptor` already carries `route_id` and `start_date`.

**Tech Stack:** Go 1.25 stdlib `net/http`, sqlc + pgx, testify; `github.com/OneBusAway/go-gtfs/proto` GTFS-RT bindings. Android: Kotlin, Jetpack Compose, Hilt, kotlinx.serialization, Retrofit, MockWebServer, minSdk 26 (java.time available).

**Spec:** No single spec. Authorities, in order: the GTFS-Realtime `TripDescriptor` semantics (https://gtfs.org/documentation/realtime/reference/#message-tripdescriptor); the README §3.1 feed example (trip_id + route_id + start_date); the Android design spec note at `docs/superpowers/specs/2026-08-04-android-driver-app-design.md:28`, which flagged this bug; the README §5 Milestone 5 / §6 deliverables list for the documentation tasks.

## Global Constraints

- Every server change must keep `go vet ./... && go test ./...` green. DB-backed store tests are skipped without `DATABASE_URL`; when a task touches SQL, also run with `DATABASE_URL='postgres://postgres:postgres@192.168.97.2:5432/vehicle_positions?sslmode=disable'` (the compose container's address on this machine; `localhost:5432` reaches a different, host-native Postgres and must not be used).
- The JSON decoders use `DisallowUnknownFields()`. New request fields are additive and optional so existing clients keep working; nothing existing is renamed or removed from the wire.
- Error strings on the location endpoint follow the existing shape: lower-case, field name first, e.g. `start_date must be YYYYMMDD` (the same wording the rider API uses).
- Feed validation: every entity's `TripDescriptor`, when present, must carry `trip_id` or `route_id`; `start_date`, when present, is 8 digits `YYYYMMDD`. Rider entities (`rider:<trip_id>:<start_date>`) are unchanged.
- Android: `./gradlew :app:testDebugUnitTest` and `./gradlew :app:compileDebugAndroidTestKotlin` must pass from `android/`. Keep the strict server contract: no unknown JSON fields, nulls omitted (`explicitNulls = false` is already set in `ApiFactory`).
- Commit messages follow the repo's history (`feat:`, `fix:`, `docs:`, `test:` prefixes, imperative mood). No attribution lines or co-author trailers.
- Documentation must describe the code as it is. Every claim about a flag, route, default, or behaviour must be checked against the source before it is written.
- Stacked branches, one per PR, each based on the previous: `feed-trip-descriptor-server` (from `main`) → `feed-trip-descriptor-android` → `admin-api-user-password` → `deployment-docs`. Tasks say which branch they run on; the controller creates branches, implementers only commit.

---

## PR 1 — branch `feed-trip-descriptor-server` (Tasks 1–2)

### Task 1: Server accepts `route_id` / `start_date` and publishes a full `TripDescriptor`

**Files:**
- Modify: `handlers.go:24-80` (`LocationReport`, `validate`), `handlers.go:241-245` (`buildFeed` trip descriptor)
- Modify: `tracker.go:8-19` (`VehicleState`), `tracker.go:70-105` (`Update`)
- Test: `handlers_test.go` (`TestBuildFeed_WithVehicles`, `TestHandlePostLocation_Validation`, new `TestBuildFeed_TripDescriptorFields`, new `TestHandlePostLocation_TripFieldsReachTracker`)
- Test: `tracker_test.go` (new `TestTracker_UpdateStoresTripFields`)
- Test: `feed_validation_test.go` (`validateFeedCompliance` gains a TripDescriptor rule; new `TestFeedValidation_TripDescriptorNeedsTripOrRoute`)

**Interfaces:**
- Consumes: `serviceDatePattern` (`rider_handlers.go:56`, `^[0-9]{8}$`), already in package `main`.
- Produces: `LocationReport.RouteID string` (`json:"route_id"`), `LocationReport.StartDate string` (`json:"start_date"`); `VehicleState.RouteID`, `VehicleState.StartDate`; `const maxTripFieldLength = 100`. Task 2 documents these; Task 3 (Android) sends them.

- [ ] **Step 1: Write the failing tests**

In `handlers_test.go`, extend `TestBuildFeed_WithVehicles` so `bus-1` has the three fields and add a third vehicle with only a route. Replace the existing `bus-1` literal's `TripID: "route-5"` with the block below and add `bus-3`:

```go
		{
			VehicleID: "bus-1",
			TripID:    "trip-0830",
			RouteID:   "5",
			StartDate: "20260906",
			Latitude:  -1.29,
			Longitude: 36.82,
			Bearing:   ptrFloat(180),
			Speed:     ptrFloat(8.5),
			Timestamp: bus1Ts,
		},
		// bus-2 unchanged: no trip fields at all.
		{
			VehicleID: "bus-3",
			RouteID:   "7",
			Latitude:  -1.31,
			Longitude: 36.84,
			Timestamp: bus2Ts,
		},
```

(Use whatever helper the file already uses for `*float64` literals — check the existing `bus-1` literal and keep its style.) Change `require.Len(t, feed.Entity, 2)` to `3`, collect `bus3` in the switch, and replace the `bus-1` trip assertion with:

```go
	require.NotNil(t, bus1.Vehicle.Trip)
	assert.Equal(t, "trip-0830", bus1.Vehicle.Trip.GetTripId())
	assert.Equal(t, "5", bus1.Vehicle.Trip.GetRouteId())
	assert.Equal(t, "20260906", bus1.Vehicle.Trip.GetStartDate())

	require.NotNil(t, bus2)
	assert.Nil(t, bus2.Vehicle.Trip, "bus-2 has no trip, Trip should be nil")

	require.NotNil(t, bus3)
	require.NotNil(t, bus3.Vehicle.Trip, "a route-only report still gets a TripDescriptor")
	assert.Nil(t, bus3.Vehicle.Trip.TripId, "route-only report must not invent a trip_id")
	assert.Equal(t, "7", bus3.Vehicle.Trip.GetRouteId())
	assert.Nil(t, bus3.Vehicle.Trip.StartDate)
```

Add a focused test after `TestBuildFeed_WithVehicles`:

```go
func TestBuildFeed_TripDescriptorFields(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name      string
		state     *VehicleState
		wantTrip  bool
		wantTrip_ string
		wantRoute string
		wantDate  string
	}{
		{"trip only", &VehicleState{VehicleID: "a", TripID: "T1", Latitude: 1, Longitude: 1, Timestamp: now}, true, "T1", "", ""},
		{"route only", &VehicleState{VehicleID: "b", RouteID: "R1", Latitude: 1, Longitude: 1, Timestamp: now}, true, "", "R1", ""},
		{"route and date", &VehicleState{VehicleID: "c", RouteID: "R1", StartDate: "20260906", Latitude: 1, Longitude: 1, Timestamp: now}, true, "", "R1", "20260906"},
		{"all three", &VehicleState{VehicleID: "d", TripID: "T1", RouteID: "R1", StartDate: "20260906", Latitude: 1, Longitude: 1, Timestamp: now}, true, "T1", "R1", "20260906"},
		{"nothing", &VehicleState{VehicleID: "e", Latitude: 1, Longitude: 1, Timestamp: now}, false, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			feed := buildFeed([]*VehicleState{tc.state}, nil)
			require.Len(t, feed.Entity, 1)
			trip := feed.Entity[0].Vehicle.Trip
			if !tc.wantTrip {
				assert.Nil(t, trip)
				return
			}
			require.NotNil(t, trip)
			assert.Equal(t, tc.wantTrip_, trip.GetTripId())
			assert.Equal(t, tc.wantRoute, trip.GetRouteId())
			assert.Equal(t, tc.wantDate, trip.GetStartDate())
			// Absent fields must be absent, not empty strings.
			assert.Equal(t, tc.wantTrip_ == "", trip.TripId == nil)
			assert.Equal(t, tc.wantRoute == "", trip.RouteId == nil)
			assert.Equal(t, tc.wantDate == "", trip.StartDate == nil)
		})
	}
}
```

Extend `TestHandlePostLocation_Validation` (`handlers_test.go:180`) — it is a table test over request bodies; add these rows in the file's existing row format (look at the neighbouring rows for the exact struct field names; the JSON is what matters):

| name | body | want status | want error substring |
|---|---|---|---|
| `route_id too long` | valid base body + `"route_id":"<101 × 'r'>"` | 400 | `route_id must be at most 100 characters` |
| `trip_id too long` | valid base body + `"trip_id":"<101 × 't'>"` | 400 | `trip_id must be at most 100 characters` |
| `start_date wrong shape` | valid base body + `"route_id":"5","start_date":"2026-09-06"` | 400 | `start_date must be YYYYMMDD` |
| `start_date not a date` | valid base body + `"route_id":"5","start_date":"20261399"` | 400 | `start_date must be a valid YYYYMMDD date` |
| `start_date without trip or route` | valid base body + `"start_date":"20260906"` | 400 | `start_date requires trip_id or route_id` |
| `route_id and start_date accepted` | valid base body + `"route_id":"5","start_date":"20260906"` | 201 | (none) |

Add a handler test proving the fields reach the tracker:

```go
func TestHandlePostLocation_TripFieldsReachTracker(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	body := fmt.Sprintf(`{"vehicle_id":"bus-1","trip_id":"trip-0830","route_id":"5","start_date":"20260906","latitude":-1.29,"longitude":36.82,"timestamp":%d}`, time.Now().Unix())
	// Build the request the same way TestHandlePostLocation_HappyPath does
	// (JSON content type, driver claims on the context), using this body.
	// ... rr := ... ; require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Equal(t, "trip-0830", active[0].TripID)
	assert.Equal(t, "5", active[0].RouteID)
	assert.Equal(t, "20260906", active[0].StartDate)
}
```

Fill in the request-building lines by copying `TestHandlePostLocation_HappyPath` (`handlers_test.go:425`) — same store fake, same claims helper — so the test differs only in the body and the assertions.

In `tracker_test.go` add:

```go
func TestTracker_UpdateStoresTripFields(t *testing.T) {
	tr := NewTracker(time.Minute)
	defer tr.Stop()
	tr.Update(&LocationReport{VehicleID: "bus-1", TripID: "trip-0830", RouteID: "5", StartDate: "20260906", Latitude: 1, Longitude: 1, Timestamp: time.Now().Unix()})
	v := tr.ActiveVehicles()
	require.Len(t, v, 1)
	assert.Equal(t, "trip-0830", v[0].TripID)
	assert.Equal(t, "5", v[0].RouteID)
	assert.Equal(t, "20260906", v[0].StartDate)
}
```

In `feed_validation_test.go`, inside `validateFeedCompliance`'s per-entity loop (near the W002/E052 checks around line 60), add a project rule:

```go
		// Project rule: a TripDescriptor must identify something. A descriptor
		// with neither trip_id nor route_id is unmatchable by any consumer.
		if trip := e.GetVehicle().GetTrip(); trip != nil {
			if trip.GetTripId() == "" && trip.GetRouteId() == "" {
				violations = append(violations, fmt.Sprintf("entity %s: TripDescriptor has neither trip_id nor route_id", e.GetId()))
			}
			if sd := trip.GetStartDate(); sd != "" && !serviceDatePattern.MatchString(sd) {
				violations = append(violations, fmt.Sprintf("entity %s: start_date %q is not YYYYMMDD", e.GetId(), sd))
			}
		}
```

(Match the file's existing violation-appending style; `violations` and `fmt` may already be in scope under other names — adapt.) Then add:

```go
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
```

- [ ] **Step 2: Run → FAIL.** `go test ./ -run 'TestBuildFeed|TestHandlePostLocation_Validation|TestHandlePostLocation_TripFieldsReachTracker|TestTracker_UpdateStoresTripFields|TestFeedValidation_TripDescriptor' -v` fails to compile (`RouteID`/`StartDate` unknown fields).

- [ ] **Step 3: Implement**

`handlers.go` — the struct and validation:

```go
type LocationReport struct {
	VehicleID string `json:"vehicle_id"`
	// TripID is the GTFS trip_id when the driver knows it. Empty when the
	// driver only knows the route; it must never carry a route id.
	TripID string `json:"trip_id"`
	// RouteID is the GTFS route_id. Optional, but the only thing a consumer
	// can match on when TripID is empty.
	RouteID string `json:"route_id"`
	// StartDate is the trip's service date, YYYYMMDD in the agency's local
	// time. Optional; meaningful only alongside TripID or RouteID.
	StartDate string   `json:"start_date"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Bearing   *float64 `json:"bearing,omitempty"`
	Speed     *float64 `json:"speed,omitempty"`
	Accuracy  *float64 `json:"accuracy,omitempty"`
	Timestamp int64    `json:"timestamp"`
	// Set server-side from JWT; never decoded from JSON.
	DriverID string `json:"-"`
}

// maxTripFieldLength matches the trips endpoint's cap on route_id and
// gtfs_trip_id (trip_handlers.go).
const maxTripFieldLength = 100
```

Append to `validate()` after the vehicle-id checks:

```go
	if len(r.TripID) > maxTripFieldLength {
		return fmt.Errorf("trip_id must be at most %d characters", maxTripFieldLength)
	}
	if len(r.RouteID) > maxTripFieldLength {
		return fmt.Errorf("route_id must be at most %d characters", maxTripFieldLength)
	}
	if r.StartDate != "" {
		if !serviceDatePattern.MatchString(r.StartDate) {
			return fmt.Errorf("start_date must be YYYYMMDD")
		}
		if _, err := time.Parse("20060102", r.StartDate); err != nil {
			return fmt.Errorf("start_date must be a valid YYYYMMDD date")
		}
		if r.TripID == "" && r.RouteID == "" {
			return fmt.Errorf("start_date requires trip_id or route_id")
		}
	}
```

`buildFeed` — replace the `if v.TripID != ""` block with:

```go
		if v.TripID != "" || v.RouteID != "" {
			trip := &gtfsrt.TripDescriptor{}
			if v.TripID != "" {
				trip.TripId = proto.String(v.TripID)
			}
			if v.RouteID != "" {
				trip.RouteId = proto.String(v.RouteID)
			}
			if v.StartDate != "" {
				trip.StartDate = proto.String(v.StartDate)
			}
			entity.Vehicle.Trip = trip
		}
```

`tracker.go` — add `RouteID string` and `StartDate string` to `VehicleState` directly under `TripID`, and in `Update` set `RouteID: loc.RouteID, StartDate: loc.StartDate,` next to `TripID`. `ActiveVehicles`/`VehicleOnTrip` copy the struct by value, so nothing else changes. `SaveLocation` is untouched: `location_points` keeps `trip_id` only (the trips table already records the route per trip; trail lookup joins on vehicle + driver + time, not on this column).

- [ ] **Step 4: Run → PASS.** `go vet ./... && go test ./...`

- [ ] **Step 5: Commit**

```bash
git add handlers.go tracker.go handlers_test.go tracker_test.go feed_validation_test.go
git commit -m "feat: publish route_id and start_date in the driver-reported TripDescriptor"
```

---

### Task 2: Document the new fields and make the simulator send a route

**Files:**
- Modify: `README.md:240-283` (payload example, validation bullets, curl example)
- Modify: `ARCHITECTURE.md:275`, `ARCHITECTURE.md:357-398` (§6.2 payload table + validation rules), `ARCHITECTURE.md:411` (§6.3 bullet)
- Modify: `docs/development.md:361-380` (API sanity check body)
- Modify: `cmd/simulator/main.go:19-27` (`locationReport`), `cmd/simulator/main.go:60-70` (route selection), `cmd/simulator/main.go:83` (`simulateVehicle` signature), `cmd/simulator/main.go:124-132` (report literal)
- Test: `cmd/simulator/main_test.go:151` (`TestLocationReportJSONRoundTrip`)

**Interfaces:**
- Consumes: `LocationReport.RouteID`, `LocationReport.StartDate`, `maxTripFieldLength` from Task 1.

- [ ] **Step 1: Write the failing simulator test**

In `cmd/simulator/main_test.go`, `TestLocationReportJSONRoundTrip` builds a `locationReport` literal and round-trips it. Add `RouteID: "sim-route-1"` to the literal and assert after the round trip:

```go
	assert.Equal(t, "sim-route-1", decoded.RouteID)
	assert.Contains(t, string(body), `"route_id":"sim-route-1"`)
```

(Adapt the variable names to the ones the test already uses for the marshalled bytes and the decoded struct.)

- [ ] **Step 2: Run → FAIL.** `go test ./cmd/simulator/ -run TestLocationReportJSONRoundTrip` fails to compile (`RouteID` unknown).

- [ ] **Step 3: Implement the simulator change**

```go
type locationReport struct {
	VehicleID string  `json:"vehicle_id"`
	RouteID   string  `json:"route_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Bearing   float64 `json:"bearing"`
	Speed     float64 `json:"speed"`
	Accuracy  float64 `json:"accuracy"`
	Timestamp int64   `json:"timestamp"`
}
```

Give `simulateVehicle` a `routeID string` parameter after `route []Waypoint`; at the call site compute `routeID := fmt.Sprintf("sim-route-%d", i%len(routes)+1)` and pass it; set `RouteID: routeID` in the report literal. No `trip_id` or `start_date`: the simulator has no schedule and must not invent one.

- [ ] **Step 4: Run → PASS.** `go test ./cmd/simulator/`

- [ ] **Step 5: Update the docs**

`README.md` payload example (line ~246) becomes:

```json
{
  "vehicle_id": "vehicle-042",
  "trip_id": "route_5_0830",
  "route_id": "5",
  "start_date": "20260715",
  "latitude": -1.2921,
  "longitude": 36.8219,
  "bearing": 180.0,
  "speed": 8.5,
  "accuracy": 12.0,
  "timestamp": 1752566400
}
```

Directly after the example paragraph ("The server updates its in-memory state…"), add:

> `trip_id`, `route_id` and `start_date` are all optional. `trip_id` is the GTFS `trip_id` and must be left empty when the driver only knows the route — never send a route id in `trip_id`. `route_id` is the GTFS `route_id`; when `trip_id` is empty it is the only thing a consumer can match on. `start_date` is the service date, `YYYYMMDD`, and is accepted only alongside `trip_id` or `route_id`. The feed's `TripDescriptor` carries exactly the fields that were sent, and is omitted entirely when both `trip_id` and `route_id` are empty.

In the validation bullets add: "- `trip_id` and `route_id` are capped at 100 characters; `start_date` must be a real `YYYYMMDD` date." Change the valid curl example body (line ~281) to `{"vehicle_id":"bus-1","route_id":"5","latitude":-1.29,"longitude":36.82,"timestamp":1752566400}`.

`ARCHITECTURE.md`: in the §6.2 JSON add `"route_id": "5",` and `"start_date": "20260715",` after `trip_id`; in the field table replace the `trip_id` row and add two rows:

```
| `trip_id` | ❌ | GTFS `trip_id`. Empty when the driver only knows the route; never a route id. Max 100 characters. |
| `route_id` | ❌ | GTFS `route_id`. Max 100 characters. |
| `start_date` | ❌ | Service date `YYYYMMDD`; accepted only with `trip_id` or `route_id`. |
```

Add validation-rule bullets for the three new error strings (`trip_id must be at most 100 characters`, `route_id must be at most 100 characters`, `start_date must be YYYYMMDD`, `start_date must be a valid YYYYMMDD date`, `start_date requires trip_id or route_id`). Line 275 becomes: "`TripDescriptor` carries `trip_id`, `route_id` and `start_date` as sent, and is omitted when both `trip_id` and `route_id` are empty." Line 411 becomes: "- A `TripDescriptor` with `trip_id`, `route_id` and `start_date` as reported, only when `trip_id` or `route_id` is non-empty."

`docs/development.md` line ~370: add `"route_id": "5",` to the sample body.

- [ ] **Step 6: Verify and commit**

`go vet ./... && go test ./...`, then:

```bash
git add README.md ARCHITECTURE.md docs/development.md cmd/simulator/main.go cmd/simulator/main_test.go
git commit -m "docs: describe route_id/start_date on location reports; simulator sends a route"
```

---

## PR 2 — branch `feed-trip-descriptor-android` (Task 3)

### Task 3: Android sends `route_id` and `start_date`, and never a route id as `trip_id`

**Files:**
- Modify: `android/app/src/main/kotlin/org/onebusaway/vehicletracker/data/TripStateStore.kt` (`ActiveTrip`, keys, read/write)
- Modify: `android/app/src/main/kotlin/org/onebusaway/vehicletracker/data/TripRepository.kt`
- Create: `android/app/src/main/kotlin/org/onebusaway/vehicletracker/data/ServiceDate.kt`
- Modify: `android/app/src/main/kotlin/org/onebusaway/vehicletracker/data/api/ApiModels.kt` (`LocationReportDto`)
- Modify: `android/app/src/main/kotlin/org/onebusaway/vehicletracker/service/TripReporter.kt:39-49`
- Modify: `android/app/src/main/kotlin/org/onebusaway/vehicletracker/di/AppModule.kt` (provide `ZoneId`)
- Test: `android/app/src/test/kotlin/org/onebusaway/vehicletracker/data/RepositoriesTest.kt:53-80`
- Test: `android/app/src/test/kotlin/org/onebusaway/vehicletracker/data/api/TrackerApiTest.kt:59-72`
- Test: `android/app/src/test/kotlin/org/onebusaway/vehicletracker/service/TripReporterTest.kt:16` and new tests
- Test: any other test constructing `ActiveTrip(` or `LocationReportDto(` (grep `android/app/src`)
- Modify: `docs/android-smoke-test.md` (verification step after "Start Trip"), `docs/superpowers/specs/2026-08-04-android-driver-app-design.md:28`

**Interfaces:**
- Consumes: server fields `route_id`, `start_date` from Task 1 (the app must talk to a server that includes PR 1; an older server rejects unknown fields with 400).
- Produces: `ActiveTrip(tripDbId, gtfsTripId, vehicleId, routeId, startDate, startedAtEpochSec)`; `fun serviceDate(epochSec: Long, zone: ZoneId): String`; `LocationReportDto.tripId: String?`, `.routeId`, `.startDate`.

- [ ] **Step 1: Write the failing tests**

`RepositoriesTest.kt` — replace the two trip-start tests at lines 53–80 with:

```kotlin
    @Test fun `trip start saves gtfs trip id, route id and device-local service date`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"trip-0830","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val store = FakeTripStateStore()
        // 2026-08-04T23:30:00Z is already 2026-08-05 in Nairobi (UTC+3).
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, store, clock = { 1_785_886_200L }, zone = ZoneId.of("Africa/Nairobi"))

        val result = repo.start("bus-1", "5", "trip-0830")

        assertTrue(result.isSuccess)
        val trip = store.activeTrip.first()!!
        assertEquals(7L, trip.tripDbId)
        assertEquals("trip-0830", trip.gtfsTripId)
        assertEquals("5", trip.routeId)
        assertEquals("20260805", trip.startDate)
        assertEquals(listOf("5"), store.recentRoutes.first())
        server.shutdown()
    }

    @Test fun `trip start keeps gtfs trip id blank instead of copying the route id`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":8,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, store, clock = { 500L }, zone = ZoneOffset.UTC)

        repo.start("bus-1", "5", "  ")

        val trip = store.activeTrip.first()!!
        assertEquals("", trip.gtfsTripId)
        assertEquals("5", trip.routeId)
        assertEquals("19700101", trip.startDate)
        server.shutdown()
    }

    @Test fun `serviceDate formats the local calendar date as YYYYMMDD`() {
        assertEquals("19700101", serviceDate(0L, ZoneOffset.UTC))
        assertEquals("19691231", serviceDate(0L, ZoneId.of("America/Los_Angeles")))
        assertEquals("20260805", serviceDate(1_785_886_200L, ZoneId.of("Africa/Nairobi")))
    }
```

Check the epoch: `1_785_886_200` = 2026-08-04T23:30:00Z (verified with `date -u -r 1785886200` before relying on it; if it is off, pick an epoch that is 23:30 UTC on 2026-08-04 and keep the Nairobi assertion). Update the other `TripRepository(` constructions in this file to pass `zone = ZoneOffset.UTC`. Add imports `java.time.ZoneId`, `java.time.ZoneOffset`, and `org.onebusaway.vehicletracker.data.serviceDate` if the test file is in a different package (it is in `...data`, so no import needed for `serviceDate`).

`TrackerApiTest.kt` — replace the location-report test at line 59 with two:

```kotlin
    @Test fun `location report omits null optionals and uses exact field names`() = runTest {
        token = "jwt"
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        api.postLocation(LocationReportDto(
            vehicleId = "bus-1", tripId = "trip-0830", routeId = "5", startDate = "20260804",
            latitude = -1.2921, longitude = 36.8219,
            bearing = null, speed = 8.5, accuracy = null, timestamp = 1752566400,
        ))
        val recorded = server.takeRequest()
        assertEquals("application/json", recorded.getHeader("Content-Type")?.substringBefore(";"))
        val sent = Json.parseToJsonElement(recorded.body.readUtf8()).jsonObject
        assertEquals(setOf("vehicle_id", "trip_id", "route_id", "start_date", "latitude", "longitude", "speed", "timestamp"), sent.keys)
        assertFalse(sent.containsKey("bearing"))
    }

    @Test fun `location report without a gtfs trip id omits trip_id entirely`() = runTest {
        token = "jwt"
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        api.postLocation(LocationReportDto(
            vehicleId = "bus-1", tripId = null, routeId = "5", startDate = "20260804",
            latitude = -1.2921, longitude = 36.8219, timestamp = 1752566400,
        ))
        val sent = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertFalse(sent.containsKey("trip_id"))
        assertEquals("5", sent["route_id"]!!.jsonPrimitive.content)
        assertEquals("20260804", sent["start_date"]!!.jsonPrimitive.content)
    }
```

(Import `kotlinx.serialization.json.jsonPrimitive`.)

`TripReporterTest.kt` — change the fixture to `ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L)` and add:

```kotlin
    @Test fun `report sends route_id and start_date, and trip_id only when a gtfs id exists`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        val (reporter, _) = reporterWith(server)

        reporter.report(trip, fix())
        val withTrip = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals("trip-0830", withTrip["trip_id"]!!.jsonPrimitive.content)
        assertEquals("5", withTrip["route_id"]!!.jsonPrimitive.content)
        assertEquals("20260804", withTrip["start_date"]!!.jsonPrimitive.content)

        reporter.report(trip.copy(gtfsTripId = ""), fix())
        val routeOnly = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals(false, routeOnly.containsKey("trip_id"))
        assertEquals("5", routeOnly["route_id"]!!.jsonPrimitive.content)
        server.shutdown()
    }
```

(Imports: `kotlinx.serialization.json.Json`, `kotlinx.serialization.json.jsonPrimitive`.) Grep `android/app/src` for every other `ActiveTrip(` and `LocationReportDto(` construction (e.g. `ViewModelsTest.kt`, `Fakes.kt`, `ScreenFlowTest.kt`) and update them to the new shapes.

- [ ] **Step 2: Run → FAIL.** From `android/`: `./gradlew :app:testDebugUnitTest` fails to compile.

- [ ] **Step 3: Implement**

`ServiceDate.kt`:

```kotlin
package org.onebusaway.vehicletracker.data

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * GTFS-RT `start_date` for a trip that began at [epochSec]: the calendar date in
 * [zone], formatted YYYYMMDD. The phone's zone stands in for the agency's — the
 * driver is physically in the agency's region. Trips that start after midnight
 * on an overnight schedule are dated by clock time, a known v1 limitation.
 */
fun serviceDate(epochSec: Long, zone: ZoneId): String =
    Instant.ofEpochSecond(epochSec).atZone(zone).toLocalDate().format(DateTimeFormatter.BASIC_ISO_DATE)
```

`TripStateStore.kt`:

```kotlin
data class ActiveTrip(
    val tripDbId: Long,
    /** GTFS trip_id the driver entered, or "" when they only know the route. Never a route id. */
    val gtfsTripId: String,
    val vehicleId: String,
    val routeId: String,
    /** Service date, YYYYMMDD, fixed when the trip started. */
    val startDate: String,
    val startedAtEpochSec: Long,
)
```

Keys: replace `TRIP_LOCATION_ID` with `val TRIP_GTFS_TRIP_ID = stringPreferencesKey("trip_gtfs_trip_id")` and add `val TRIP_START_DATE = stringPreferencesKey("trip_start_date")`. Reading:

```kotlin
    override val activeTrip: Flow<ActiveTrip?> = context.tripStateDataStore.data.map { prefs ->
        val tripDbId = prefs[Keys.TRIP_DB_ID]
        val vehicleId = prefs[Keys.TRIP_VEHICLE_ID]
        val routeId = prefs[Keys.TRIP_ROUTE_ID]
        val startedAt = prefs[Keys.TRIP_STARTED_AT]
        if (tripDbId != null && vehicleId != null && routeId != null && startedAt != null) {
            ActiveTrip(
                tripDbId = tripDbId,
                // A trip persisted by a build before these keys existed has neither; treat it as
                // route-only and date it from when it started.
                gtfsTripId = prefs[Keys.TRIP_GTFS_TRIP_ID] ?: "",
                vehicleId = vehicleId,
                routeId = routeId,
                startDate = prefs[Keys.TRIP_START_DATE] ?: serviceDate(startedAt, ZoneId.systemDefault()),
                startedAtEpochSec = startedAt,
            )
        } else {
            null
        }
    }
```

`saveActiveTrip` writes `TRIP_GTFS_TRIP_ID` and `TRIP_START_DATE`; `clearActiveTrip` removes both, and also removes the legacy `stringPreferencesKey("trip_location_id")` so old installs are cleaned up.

`TripRepository.kt`: add `private val zone: ZoneId` as the last constructor parameter (Hilt-injected; see `AppModule` below) and change `start`:

```kotlin
    suspend fun start(vehicleId: String, routeId: String, gtfsTripId: String): Result<ActiveTrip> = try {
        val cleanedTripId = gtfsTripId.trim()
        val trip = apiProvider.get().startTrip(StartTripRequest(vehicleId, routeId, cleanedTripId))
        val startedAt = clock()
        val activeTrip = ActiveTrip(
            tripDbId = trip.id,
            gtfsTripId = cleanedTripId,
            vehicleId = vehicleId,
            routeId = routeId,
            startDate = serviceDate(startedAt, zone),
            startedAtEpochSec = startedAt,
        )
        ...
```

`AppModule.kt`: next to `provideClock` add

```kotlin
    @Provides
    fun provideZoneId(): ZoneId = ZoneId.systemDefault()
```

(import `java.time.ZoneId`). If `TripRepository` is constructed anywhere else in main code (grep), pass `ZoneId.systemDefault()`.

`ApiModels.kt`:

```kotlin
@Serializable data class LocationReportDto(
    @SerialName("vehicle_id") val vehicleId: String,
    /** GTFS trip_id; null (omitted on the wire) when the driver only knows the route. */
    @SerialName("trip_id") val tripId: String? = null,
    @SerialName("route_id") val routeId: String,
    @SerialName("start_date") val startDate: String,
    val latitude: Double,
    val longitude: Double,
    val bearing: Double? = null,
    val speed: Double? = null,
    val accuracy: Double? = null,
    val timestamp: Long,
)
```

`TripReporter.report`:

```kotlin
        val dto = LocationReportDto(
            vehicleId = trip.vehicleId,
            tripId = trip.gtfsTripId.ifBlank { null },
            routeId = trip.routeId,
            startDate = trip.startDate,
            ...
```

- [ ] **Step 4: Run → PASS.** From `android/`: `./gradlew :app:testDebugUnitTest && ./gradlew :app:compileDebugAndroidTestKotlin`.

- [ ] **Step 5: Docs**

`docs/android-smoke-test.md`: after step 3 ("Enter a route ID (e.g. `5`) and tap **Start Trip**"), add a verification step:

> 4. Fetch `http://localhost:8080/gtfs-rt/vehicle-positions?format=json`. The vehicle's entity must show `"trip": {"routeId": "5", "startDate": "<today, YYYYMMDD>"}` and **no** `tripId` — the app never sends the route id as a trip id. Start a second trip with a GTFS trip id filled in and confirm `tripId` appears alongside `routeId`.

Renumber following steps. In the Android design spec, replace the "Server-side note" paragraph at line 28 with:

> *Server-side note (resolved 2026-09-06):* the location report now carries `route_id` and `start_date`, and the app sends `trip_id` only when the driver entered a GTFS trip id. The feed's `TripDescriptor` therefore always identifies the route, and never carries a route id in `trip_id`.

- [ ] **Step 6: Commit**

```bash
git add android/app/src docs/android-smoke-test.md docs/superpowers/specs/2026-08-04-android-driver-app-design.md
git commit -m "fix(android): send route_id and start_date; never report the route as trip_id"
```

---

## PR 3 — branch `admin-api-user-password` (Task 4)

### Task 4: `PUT /api/v1/admin/users/{id}` accepts an optional new password

The admin web UI already resets passwords through its edit form (`admin_page_handlers.go` → `UpdateUserPassword`). The JSON API cannot, and `user_store.go:173` carries a stale TODO saying so. Bring the API to parity.

**Files:**
- Modify: `user_handlers.go:21-25` (`UpdateUserRequest`), `user_handlers.go:130-191` (`handleUpdateUser`)
- Modify: `user_store.go:49-51` (`UserUpdater` interface), `user_store.go:172-173` (delete the TODO)
- Test: `user_handlers_test.go` (new `TestHandleUpdateUser_PasswordOptional`; extend the file's update-user fake)
- Modify: `README.md` (the "Deactivating a user…" paragraph near line 47) and `ARCHITECTURE.md` wherever the admin users API is described (grep `admin/users`)

**Interfaces:**
- Consumes: `validatePassword(string) error` and `minPasswordLength` (`admin_page_handlers.go:650`), `Store.UpdateUserPassword(ctx, id int64, password string) error` (`user_store.go:229`), `ErrUserNotFound`.
- Produces: `UserUpdater` interface now requires `UpdateUserPassword`; `UpdateUserRequest.Password string` (`json:"password"`).

- [ ] **Step 1: Write the failing tests**

Find the fake that `handleUpdateUser` tests use in `user_handlers_test.go` (grep `UpdateUser(ctx`). Give it a `passwordUpdates map[int64]string` field (initialised where the fake is constructed) and the method:

```go
func (m *<fakeType>) UpdateUserPassword(_ context.Context, id int64, password string) error {
	if m.err != nil { // or whatever error-injection field the fake already has
		return m.err
	}
	if m.passwordUpdates == nil {
		m.passwordUpdates = map[int64]string{}
	}
	m.passwordUpdates[id] = password
	return nil
}
```

Then add:

```go
func TestHandleUpdateUser_PasswordOptional(t *testing.T) {
	t.Run("blank password leaves the hash alone", func(t *testing.T) {
		store := <new fake with user id 1>
		rr := putUser(t, store, 1, `{"name":"N","email":"n@test.com","role":"driver"}`)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		assert.Empty(t, store.passwordUpdates)
	})
	t.Run("new password is applied after the profile update", func(t *testing.T) {
		store := <new fake with user id 1>
		rr := putUser(t, store, 1, `{"name":"N","email":"n@test.com","role":"driver","password":"newlongpassword"}`)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		assert.Equal(t, "newlongpassword", store.passwordUpdates[1])
		assert.NotContains(t, rr.Body.String(), "newlongpassword")
	})
	t.Run("short password is rejected before any write", func(t *testing.T) {
		store := <new fake with user id 1>
		rr := putUser(t, store, 1, `{"name":"N","email":"n@test.com","role":"driver","password":"short"}`)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "password must be at least 8 characters")
		assert.Empty(t, store.passwordUpdates)
		// and the profile was not updated either — check however the fake records UpdateUser calls
	})
}
```

Write `putUser(t, store, id, body)` as a small helper in the test file that builds a `PUT /api/v1/admin/users/{id}` request with `Content-Type: application/json`, sets the path value the way the file's other update-user tests do, and invokes `handleUpdateUser(store)`. Reuse an existing helper if one exists.

- [ ] **Step 2: Run → FAIL.** `go test ./ -run TestHandleUpdateUser -v` fails to compile.

- [ ] **Step 3: Implement**

```go
type UpdateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// Password, when non-empty, replaces the user's password. Blank keeps
	// the current one — the same contract as the admin UI's edit form.
	Password string `json:"password"`
}
```

```go
type UserUpdater interface {
	UpdateUser(ctx context.Context, id int64, name, email, role string) (*UserResponse, error)
	UpdateUserPassword(ctx context.Context, id int64, password string) error
}
```

In `handleUpdateUser`, after the role check and before `store.UpdateUser`:

```go
		if req.Password != "" {
			if err := validatePassword(req.Password); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
```

After the successful `UpdateUser` and before `writeJSON(w, http.StatusOK, user)`:

```go
		if req.Password != "" {
			if err := store.UpdateUserPassword(r.Context(), id, req.Password); err != nil {
				if errors.Is(err, ErrUserNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
					return
				}
				slog.Error("failed to update user password", "id", id, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
		}
```

Delete the TODO line at `user_store.go:173`. If any other fake implements `UserUpdater` (grep `UpdateUser(ctx context.Context, id int64` in `*_test.go`), add `UpdateUserPassword` to it.

- [ ] **Step 4: Run → PASS.** `go vet ./... && go test ./...`

- [ ] **Step 5: Docs**

README, the paragraph starting "Deactivating a user blocks new logins immediately": append a sentence — "Changing a user's password (from the admin UI's edit form, or by sending `password` in `PUT /api/v1/admin/users/{id}`) has the same limit: tokens already issued stay valid until they expire." If ARCHITECTURE.md describes the users API fields, add `password` as optional there; if it does not list fields, leave it.

- [ ] **Step 6: Commit**

```bash
git add user_handlers.go user_store.go user_handlers_test.go README.md ARCHITECTURE.md
git commit -m "feat: allow password changes through PUT /api/v1/admin/users/{id}"
```

---

## PR 4 — branch `deployment-docs` (Tasks 5–6)

### Task 5: Production deployment guide

**Files:**
- Create: `docs/deployment.md`
- Modify: `README.md:16-23` (Getting Started → link the new guide)

**Interfaces:**
- Consumes (verify each in source before writing): env vars read in `main.go`, `rider_wiring.go`, `bootstrap.go`, `admin_session.go`, `proxy.go`, `retention.go`; `GET /health` and `GET /ready` (`main.go:88-92`); migrations run at boot via `store.Migrate` (`main.go:183`); `Dockerfile` (multi-stage, `alpine:3.21`, port 8080, no published image); `docker-compose.yml`; `TRUST_PROXY_HEADERS`, `ADMIN_BOOTSTRAP_EMAIL/PASSWORD`, `JWT_SECRET` 32-byte minimum; retention vars; rider-mode vars; `android/app/build.gradle.kts` (release build type has no `signingConfig`, so a release APK must be signed by the agency); `.github/workflows/android.yml`.

- [ ] **Step 1: Collect the facts.** Run `grep -n 'envOrDefault\|envBoolOrDefault\|envDurationOrDefault\|envInt32OrDefault\|envPositive\|os.Getenv' *.go | grep -v _test` and read each default. Read `main.go` end to end for startup order (config → DB → migrate → bootstrap admin → routes → listen), `Dockerfile`, `docker-compose.yml`, `docs/development.md`, the README's admin-UI, rider-mode and retention sections, and `android/app/build.gradle.kts`.

- [ ] **Step 2: Write `docs/deployment.md`** with these sections, in this order. Each must be concrete: real commands, real file contents, no "configure as appropriate".

1. **Who this is for / what you get** — one paragraph: single Go binary + PostgreSQL; produces a GTFS-RT Vehicle Positions feed at `/gtfs-rt/vehicle-positions`; admin UI at `/admin`.
2. **Sizing and prerequisites** — a Linux host (1 vCPU / 1 GB is enough for tens of vehicles), PostgreSQL 15+ (compose ships 17), a DNS name and TLS certificate, Docker *or* Go 1.25 to build the binary.
3. **Configuration reference** — one table of *every* environment variable with default and purpose, grouped: core (`PORT`, `DATABASE_URL`, `JWT_SECRET`, `STALENESS_THRESHOLD`, `READ_TIMEOUT`, `WRITE_TIMEOUT`, `IDLE_TIMEOUT`), admin (`ADMIN_UI_ENABLED`, `ADMIN_BOOTSTRAP_EMAIL`, `ADMIN_BOOTSTRAP_PASSWORD`, `TRUST_PROXY_HEADERS`), retention (`LOCATION_RETENTION_PERIOD`, `LOCATION_PRUNE_INTERVAL`, `LOCATION_PRUNE_BATCH_SIZE`), rider mode (all `RIDER_*`, `GTFS_STATIC_*`, `TRUSTED_FEED_*`, `TRUSTED_GTFS_RT_URLS`). Values come from the source, not from memory.
4. **Option A: Docker Compose on one host** — production `docker-compose.yml` variant: pinned image built from the repo (`docker build -t vehicle-positions:$(git rev-parse --short HEAD) .`), `.env` with `JWT_SECRET=$(openssl rand -hex 32)`, a real Postgres password, `restart: unless-stopped`, DB port *not* published, bootstrap admin vars set for first boot then removed.
5. **Option B: systemd + native binary** — `CGO_ENABLED=0 go build -o /usr/local/bin/vehicle-positions .`, a `vehicle-positions` system user, `/etc/vehicle-positions.env` (mode 0600), and a complete unit file (`After=network-online.target postgresql.service`, `EnvironmentFile=`, `Restart=on-failure`, `NoNewPrivileges=yes`, `ProtectSystem=strict`).
6. **Database** — create role and database; note migrations run automatically at startup so no separate step; backups with `pg_dump -Fc` on a cron/systemd timer and a restore command; the retention section pointing at the README's retention table with a recommended starting value (90 days) and the privacy reasoning already in the README.
7. **Reverse proxy and TLS** — complete nginx server block: HTTP→HTTPS redirect, `proxy_pass http://127.0.0.1:8080`, `X-Forwarded-For` and `X-Forwarded-Proto` set, `client_max_body_size 2m`, Let's Encrypt via certbot; *then* set `TRUST_PROXY_HEADERS=true` and explain why it must stay `false` when the server is reached directly (from the README).
8. **First boot checklist** — `curl https://…/health` → `{"status":"ok"}`, `curl https://…/ready`, sign in at `/admin` with the bootstrap admin, remove the bootstrap vars, create the first vehicle.
9. **Monitoring** — `/health` (liveness), `/ready` (DB reachable), `GET /api/v1/admin/status` (admin token), admin dashboard; structured JSON logs on stdout (`slog.NewJSONHandler`); what to alert on (feed empty during service hours, `/ready` failing).
10. **Upgrading** — pull, rebuild, restart; migrations apply on boot; note any default-on surfaces called out in the README (the admin UI note).
11. **Connecting the feed to OneBusAway** — the feed URL, `?format=json` for eyeballing, `source=driver|rider|all`, and that OBA consumes GTFS-RT Vehicle Positions natively.
12. **Distributing the Android app** — `cd android && ./gradlew :app:assembleRelease` produces an unsigned release APK (`build.gradle.kts` defines no `signingConfig`); how to generate a keystore and sign with `apksigner`, or add a `signingConfig` fed from environment variables; sideloading via `adb install` or a download link + "Install unknown apps"; the app takes the server URL on its login screen so one APK serves every agency.

- [ ] **Step 3: Link it.** In README "Getting Started → Server (Go)" add: "For production — Postgres, reverse proxy and TLS, systemd, backups, monitoring and APK distribution — see [`docs/deployment.md`](docs/deployment.md)."

- [ ] **Step 4: Verify.** Re-open every file you cited and confirm each flag name, default, route and command in the guide matches. Run `go test ./...` (nothing should change, but the tree must stay green).

- [ ] **Step 5: Commit**

```bash
git add docs/deployment.md README.md
git commit -m "docs: production deployment guide"
```

---

### Task 6: Operator manual

**Files:**
- Create: `docs/operator-manual.md`
- Modify: `README.md:24-55` (Admin web UI section → link the manual)

**Interfaces:**
- Consumes (verify in source): admin UI routes in `admin_page_handlers.go:100-160`; templates in `web/templates/views/*.html` (dashboard, map, vehicles, vehicle_form, users, user_form, trips); CSV export route for vehicle location history; `GET /api/v1/vehicles` (driver's assigned vehicles); the Android login/vehicle/trip flow from `docs/android-smoke-test.md` and `docs/superpowers/specs/2026-08-04-android-driver-app-design.md`; the rider-mode admin endpoints from the README; `docs/deployment.md` from Task 5.

- [ ] **Step 1: Collect the facts.** Read `admin_page_handlers.go` route registrations and each template to learn exactly what each screen shows and which buttons exist; read `docs/android-smoke-test.md` for the driver-side flow; read the README's admin UI, rider mode and retention sections.

- [ ] **Step 2: Write `docs/operator-manual.md`** for a transit-agency operations manager, not a developer. Sections:

1. **What the system does** — one paragraph and a three-box diagram in a fenced text block: driver phone → server → GTFS-RT feed → OneBusAway.
2. **Signing in** — `/admin`, the bootstrap admin from deployment, changing your own password (edit your user).
3. **Dashboard** — what each number means (active vehicles, staleness threshold, last update), using the real labels from `dashboard.html`.
4. **Vehicles** — add (id rules: `[A-Za-z0-9._-]`, max 50, from `handlers.go`), edit label, deactivate vs delete (whichever the UI offers), export location history CSV.
5. **Drivers and admins** — create user, roles, reset a password (edit form's "New password" field), deactivate (blocks new logins; existing sessions live up to 24 h — from README), the last-admin protection.
6. **Assigning vehicles to drivers** — from the user edit page; a driver can only start trips on assigned vehicles (`403` otherwise).
7. **Onboarding a driver** — step list: install the APK, open the app, enter the server URL, sign in, grant location "Allow all the time", pick a vehicle, enter the route id and (optionally) the GTFS trip id, Start Trip; what the LIVE indicator and problem states mean (network, auth expired, clock skew, GPS) — names from `TrackingProblem` in the Android source.
8. **Watching the fleet** — live map with trails, trip history and trail view, the feed URL to hand to OneBusAway or any GTFS-RT consumer, checking `?format=json`.
9. **Daily operations checklist** — before service: feed has vehicles; during: map; after: trips completed, stuck "active" trips.
10. **Rider mode (optional)** — what it adds, how to tell it is on (`GET /api/v1/admin/rider/status`), what `Rider-reported` entities look like in the feed.
11. **Data retention and privacy** — what is stored, the retention setting, who can see location history.
12. **Troubleshooting** — a table: symptom → likely cause → fix (driver sees "not assigned"; vehicle missing from feed; feed empty; login fails after deactivation; time skew rejections; OBA not showing vehicles).

- [ ] **Step 3: Link it.** In README "Admin web UI" add: "Day-to-day instructions for operators — onboarding drivers, managing vehicles, watching the feed — are in [`docs/operator-manual.md`](docs/operator-manual.md)."

- [ ] **Step 4: Verify.** Every screen, button and label named in the manual must exist in the templates or app source. Re-check each. `go test ./...` stays green.

- [ ] **Step 5: Commit**

```bash
git add docs/operator-manual.md README.md
git commit -m "docs: operator manual"
```
