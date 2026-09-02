# Rider Mode v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a crowdsourced, low-trust "rider" ingestion mode to the vehicle-positions server (verify rider GPS against GTFS static + a trusted GTFS-RT feed, accrue reputation, publish shape-snapped supplements into the GTFS-RT feed) and ship an iOS 18+ SwiftPM SDK (`VehiclePositionsKit`) that rider apps use to report.

**Architecture:** A new importable Go package `rider/` holds the pure engine (GTFS index + shape projection, trusted-feed poller, per-point verifier, ride session state machine, reputation policy, aggregator). `package main` gains rider store/auth/handlers/wiring files following the existing one-concern-per-file convention, a merged feed builder, and admin status endpoints; rider mode is enabled by env config. The SDK is a standalone Swift 6 package under `ios/VehiclePositionsKit/` with protocol seams for transport, location, credentials and clock so the whole ride lifecycle is unit-tested with fakes on the simulator.

**Tech Stack:** Go 1.25 (stdlib `net/http`), pgx/sqlc/golang-migrate, testify, `github.com/OneBusAway/go-gtfs` v1.1.1, `github.com/google/uuid`, `gtfs-realtime-bindings`; Swift 6 / SwiftPM tools 6.0, Swift Testing, CoreLocation (`CLLocationUpdate.liveUpdates`, `CLBackgroundActivitySession`, `CLServiceSession`), Xcode 27 beta at `/Applications/Xcode-27.0.0-beta.6.app`.

**Spec:** `docs/superpowers/specs/2026-09-02-rider-mode-design.md` — read it before starting any task. Section numbers below (§4.x) refer to it.

## Global Constraints

- Server code lives in `package main` at the repo root, one concern per file, `foo.go` / `foo_test.go`. The engine lives in `rider/` (`package rider`) and must import neither `main` nor `db`; it may use `net/http` only as a client.
- Store-backed tests use testify `require`/`assert`, the `newTestStore(t)` helper, and skip without `DATABASE_URL`. Start Postgres with `docker compose up -d db`, then run `DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable' go test ./...`. Handler-only and engine tests must pass with plain `go test ./...`.
- sqlc: queries in `db/query.sql`, regenerate with `make generate` (`/opt/homebrew/bin/sqlc`). Never hand-edit `db/*.sql.go`. Fixed-shape queries go through sqlc; the only hand-written SQL allowed is `pgx.Batch` point inserts inside a transaction.
- Migration numbering: the next migration is `000011` (the sequence intentionally skips 000007; 000010 is the latest).
- Rider and ride ids are UUID v4 **strings** stored in `TEXT` columns (avoids `pgtype.UUID` plumbing; deviation from the spec's `UUID` column type is intentional and documented here). Generate with `github.com/google/uuid`.
- JSON errors use the existing `writeJSON(w, status, map[string]string{"error": ...})` shape. Rider request bodies: `Content-Type: application/json` (415 otherwise), 64 KiB cap, `DisallowUnknownFields`, exactly one object (same decode pattern as `handlePostLocation` in `handlers.go:80-107`).
- Every `/api/v1/admin/*` route: `authMiddleware(adminMiddleware(...))`. Every `/api/v1/rider/*` route except `/register`: `requireRider(jwtSecret)`.
- Do not change driver-API request/response shapes. The one behavioural change to existing middleware is the role check in `requireAuth` (§4.3).
- Thresholds live in `rider.Thresholds`; the engine never reads environment variables.
- Feed validation: after Task 11 `validateFeedCompliance` (in `feed_validation_test.go`) must pass against merged feeds.
- Swift package: `swift-tools-version: 6.0`, `platforms: [.iOS(.v18)]`, `swiftLanguageModes: [.v6]`, no third-party dependencies, all public types `Sendable`, strict-concurrency clean (zero warnings). iOS-only frameworks are wrapped in `#if os(iOS)` (CoreLocation source, Keychain store). Tests run on the simulator: `export DEVELOPER_DIR=/Applications/Xcode-27.0.0-beta.6.app/Contents/Developer` then `xcodebuild test -scheme VehiclePositionsKit -destination 'platform=iOS Simulator,name=iPhone 17 Pro' -workspace ios/VehiclePositionsKit/.swiftpm/xcode/package.xcworkspace` — or, simpler, run from inside `ios/VehiclePositionsKit`: `xcodebuild test -scheme VehiclePositionsKit -destination 'platform=iOS Simulator,name=iPhone 17 Pro'` (xcodebuild auto-generates the scheme from `Package.swift`). XcodeBuildMCP's `test_sim` with `projectPath` pointing at the package directory is equivalent.
- Commit after each task with a conventional-commit message. Run `go vet ./...` (Go tasks) before each commit. No attribution lines in commit messages.

---

### Task 1: `rider/shape.go` — geometry and shape projection

**Files:**
- Create: `rider/shape.go`, `rider/shape_test.go`, `rider/doc.go`

**Interfaces:**
- Produces:

```go
package rider

type LatLon struct{ Lat, Lon float64 }
func Distance(a, b LatLon) float64          // haversine, metres
func InitialBearing(a, b LatLon) float64    // degrees clockwise from north, [0, 360)

type Projection struct {
    AlongShape      float64 // metres from the shape start
    DistanceToShape float64 // metres from the point to the closest point on the shape
    SegmentBearing  float64 // bearing of the segment the point projected onto
}

type ShapeGeom struct {
    Points     []LatLon
    Cumulative []float64 // Cumulative[i] = metres from Points[0] to Points[i]
    Length     float64
}
func NewShapeGeom(points []LatLon) *ShapeGeom   // panics on < 2 points
func (s *ShapeGeom) Project(p LatLon, hint *float64) Projection
func (s *ShapeGeom) PointAt(along float64) LatLon     // clamps to [0, Length]
func (s *ShapeGeom) BearingAt(along float64) float64  // bearing of the segment containing `along`
```

`Project` semantics (§4.2 `shape.go`): use an equirectangular local projection (x = lon × cos(meanLat), y = lat, both in metres via 111_320 m/deg) to compute point-to-segment distance and the parameter `t ∈ [0,1]` for every segment. Collect **local minima**: segment `i` is a local minimum when its distance is ≤ both neighbours' distances. Let `best` be the global minimum distance. Candidates are local minima with distance ≤ `2 × best + 1` (the `+1` metre avoids degenerate zero cases). With `hint == nil` return the global minimum; with a hint return the candidate minimising `|along − *hint|`. `along = Cumulative[i] + t × segmentLength(i)`.

- [ ] **Step 1: Write the failing tests**

```go
package rider

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// straightShape runs due north for ~1 km from (47.6000, -122.3300).
func straightShape() *ShapeGeom {
	return NewShapeGeom([]LatLon{
		{47.6000, -122.3300},
		{47.6045, -122.3300},
		{47.6090, -122.3300},
	})
}

// loopShape is a square loop: north, east, south, back west to the start.
func loopShape() *ShapeGeom {
	return NewShapeGeom([]LatLon{
		{47.6000, -122.3300},
		{47.6045, -122.3300}, // north 500 m
		{47.6045, -122.3234}, // east ~500 m
		{47.6000, -122.3234}, // south 500 m
		{47.6000, -122.3300}, // west back to start
	})
}

func TestDistance_Haversine(t *testing.T) {
	d := Distance(LatLon{47.6000, -122.3300}, LatLon{47.6090, -122.3300})
	assert.InDelta(t, 1001, d, 5) // 0.009° latitude ≈ 1001 m
	assert.Equal(t, 0.0, Distance(LatLon{1, 1}, LatLon{1, 1}))
}

func TestInitialBearing(t *testing.T) {
	assert.InDelta(t, 0, InitialBearing(LatLon{47.6, -122.33}, LatLon{47.61, -122.33}), 0.5)
	assert.InDelta(t, 90, InitialBearing(LatLon{47.6, -122.33}, LatLon{47.6, -122.32}), 1)
	assert.InDelta(t, 180, InitialBearing(LatLon{47.61, -122.33}, LatLon{47.6, -122.33}), 0.5)
}

func TestNewShapeGeom_Cumulative(t *testing.T) {
	s := straightShape()
	require.Len(t, s.Cumulative, 3)
	assert.Equal(t, 0.0, s.Cumulative[0])
	assert.InDelta(t, 500, s.Cumulative[1], 3)
	assert.InDelta(t, 1001, s.Length, 5)
	assert.Panics(t, func() { NewShapeGeom([]LatLon{{1, 1}}) })
}

func TestProject_OnShape(t *testing.T) {
	s := straightShape()
	p := s.Project(LatLon{47.6045, -122.3300}, nil)
	assert.InDelta(t, 500, p.AlongShape, 3)
	assert.InDelta(t, 0, p.DistanceToShape, 0.5)
	assert.InDelta(t, 0, p.SegmentBearing, 1)
}

func TestProject_OffShape(t *testing.T) {
	s := straightShape()
	// 0.001° longitude at 47.6° ≈ 75 m east of the line, 250 m along.
	p := s.Project(LatLon{47.60225, -122.3290}, nil)
	assert.InDelta(t, 250, p.AlongShape, 5)
	assert.InDelta(t, 75, p.DistanceToShape, 3)
}

func TestProject_BeyondEnds_Clamps(t *testing.T) {
	s := straightShape()
	before := s.Project(LatLon{47.5990, -122.3300}, nil)
	assert.InDelta(t, 0, before.AlongShape, 0.01)
	assert.InDelta(t, 111, before.DistanceToShape, 3)
	after := s.Project(LatLon{47.6100, -122.3300}, nil)
	assert.InDelta(t, s.Length, after.AlongShape, 0.01)
}

func TestProject_LoopUsesHint(t *testing.T) {
	s := loopShape()
	// The start/end corner is equidistant from the first and last segments.
	nearStart := LatLon{47.6001, -122.3299}
	first := s.Project(nearStart, nil)
	assert.Less(t, first.AlongShape, 50.0, "without a hint the global/first minimum wins")

	hint := s.Length - 30
	last := s.Project(nearStart, &hint)
	assert.Greater(t, last.AlongShape, s.Length-60, "with a late hint the last segment wins")

	// A hint far from any local minimum still returns a valid local minimum.
	mid := 1000.0
	p := s.Project(nearStart, &mid)
	assert.True(t, p.AlongShape < 50 || p.AlongShape > s.Length-60)
}

func TestPointAt_And_BearingAt(t *testing.T) {
	s := loopShape()
	p := s.PointAt(250)
	assert.InDelta(t, 47.60225, p.Lat, 0.0001)
	assert.InDelta(t, -122.3300, p.Lon, 0.0001)
	assert.InDelta(t, 0, s.BearingAt(250), 1)
	assert.InDelta(t, 90, s.BearingAt(s.Cumulative[1]+100), 2)
	assert.InDelta(t, 180, s.BearingAt(s.Cumulative[2]+100), 1)

	// Clamping.
	assert.Equal(t, s.Points[0], s.PointAt(-5))
	assert.Equal(t, s.Points[len(s.Points)-1], s.PointAt(s.Length+5))

	// Round trip: PointAt(along) projects back to ~along.
	for _, along := range []float64{10, 400, 900, 1400} {
		q := s.Project(s.PointAt(along), &along)
		assert.InDelta(t, along, q.AlongShape, 1, "along=%v", along)
		assert.False(t, math.IsNaN(q.DistanceToShape))
	}
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./rider/ -run 'TestDistance|TestInitialBearing|TestNewShapeGeom|TestProject|TestPointAt' -v` fails to compile (package does not exist).

- [ ] **Step 3: Implement** `rider/doc.go` (`// Package rider implements the crowdsourced rider-position engine: GTFS index, verification, sessions, reputation and aggregation (spec §4.2).`) and `rider/shape.go` per the Interfaces block. Haversine with Earth radius 6_371_000 m. `InitialBearing` via `atan2(sin Δλ cos φ2, cos φ1 sin φ2 − sin φ1 cos φ2 cos Δλ)` normalised to `[0,360)`. Precompute `cosLat = cos(mean latitude)` in `NewShapeGeom`. Point-to-segment: project `p` onto segment `ab` in local metres, `t = clamp(dot(ap, ab)/|ab|², 0, 1)`, distance to `a + t·ab`. Zero-length segments get `t = 0`. `BearingAt` uses `InitialBearing` of the segment endpoints; `PointAt` interpolates linearly in lat/lon between the segment endpoints.

- [ ] **Step 4: Run → PASS.** `go test ./rider/ -v`; then `go vet ./...`.

- [ ] **Step 5: Commit** — `git add rider && git commit -m "feat(rider): shape geometry and hinted projection"`

---

### Task 2: `rider/index.go` + `rider/loader.go` — GTFS index, service dates, loader, refresher, fixture

**Files:**
- Create: `rider/index.go`, `rider/index_test.go`, `rider/loader.go`, `rider/loader_test.go`, `rider/fixture_test.go`, `rider/testdata/fixture.zip` (generated), `rider/testdata/README.md`
- Modify: `go.mod` / `go.sum` (`go get github.com/OneBusAway/go-gtfs@v1.1.1`)

**Interfaces:**
- Consumes: `ShapeGeom`, `LatLon` (Task 1); go-gtfs: `gtfs.ParseStatic(b []byte, gtfs.ParseStaticOptions{}) (*gtfs.Static, error)`, `Static.Agencies[i].Timezone`, `Static.Trips []gtfs.ScheduledTrip{ID, Route *Route{Id}, Service *Service{Id, Monday…Sunday bool, StartDate, EndDate time.Time, AddedDates, RemovedDates []time.Time}, StopTimes []ScheduledStopTime{Stop *Stop{Id, Latitude, Longitude *float64}, ArrivalTime, DepartureTime time.Duration, StopSequence int, ShapeDistanceTraveled *float64}, Shape *Shape{ID, Points []ShapePoint{Latitude, Longitude, Distance *float64}}}`.
- Produces:

```go
type StopTimeInfo struct {
    StopID     string
    Sequence   int
    AlongShape float64        // metres
    Arrival    time.Duration  // since service-day midnight (GTFS noon-minus-12h)
    Departure  time.Duration
    Pos        LatLon
}
type TripInfo struct {
    ID, RouteID, ServiceID string
    Shape     *ShapeGeom
    StopTimes []StopTimeInfo // sorted by Sequence
}
type IndexStats struct{ Trips, Shapes int; LoadedAt time.Time; Source string }

type Index struct{ /* unexported */ }
func BuildIndex(static *gtfs.Static, source string, loadedAt time.Time) (*Index, error) // error when no agency timezone parses
func (ix *Index) Trip(id string) (*TripInfo, bool)
func (ix *Index) ActiveOn(trip *TripInfo, serviceDate string) bool   // serviceDate "YYYYMMDD"
func (ix *Index) Timezone() *time.Location
func (ix *Index) ServiceDate(now time.Time) string                   // local date; before 03:00 local → previous day
func (ix *Index) Stats() IndexStats
func (ix *Index) TripIDs() []string                                  // sorted
func ServiceDayStart(serviceDate string, loc *time.Location) (time.Time, error) // 12:00 local on serviceDate minus 12h
func ScheduledOffsetAt(trip *TripInfo, along float64) time.Duration  // interpolated schedule time at `along`, spec §4.5 rule 4

func LoadStatic(ctx context.Context, source string, client *http.Client) (*gtfs.Static, error) // http(s):// or file path
func LoadIndex(ctx context.Context, source string, client *http.Client, now time.Time) (*Index, error)

type Refresher struct{ /* atomic.Pointer[Index] + loader */ }
func NewRefresher(initial *Index, load func(ctx context.Context) (*Index, error)) *Refresher
func (r *Refresher) Current() *Index
func (r *Refresher) Start(ctx context.Context, every time.Duration) // goroutine; returns when ctx is done
func (r *Refresher) RefreshNow(ctx context.Context) error           // swap on success, keep old on failure
```

- Fixture (`fixture_test.go`, exported to other test files in the package): `buildFixtureZip(t) []byte` and `fixtureIndex(t) *Index`. Contents:
  - `agency.txt`: `agency_id=A, agency_name=Test, agency_url=http://example.com, agency_timezone=America/Los_Angeles`.
  - `routes.txt`: `R1` (straight north line) and `R2` (loop), `route_type=3`.
  - `calendar.txt`: `WEEKDAY` (Mon–Fri, 20260101–20261231), `SAT` (Sat only, same range).
  - `calendar_dates.txt`: `WEEKDAY,20260907,2` (removed: Labor Day), `SAT,20260907,1` (added).
  - `shapes.txt`: `S1` = the three points of `straightShape()` from Task 1 **with** `shape_dist_traveled` in metres (0, 500, 1001); `S2` = the five points of `loopShape()` **without** `shape_dist_traveled`.
  - `stops.txt`: `ST1 (47.6000,-122.3300)`, `ST2 (47.6045,-122.3300)`, `ST3 (47.6090,-122.3300)`, `LP1 (47.6000,-122.3300)`, `LP2 (47.6045,-122.3234)`, `LP3 (47.6000,-122.3234)`.
  - `trips.txt`: `T1` (R1, WEEKDAY, S1), `T2` (R1, SAT, S1), `T3` (R2, WEEKDAY, S2), `T4` (R1, WEEKDAY, **no shape_id** — must be excluded).
  - `stop_times.txt`: T1: ST1 08:00:00/08:00:00 seq 1 dist 0, ST2 08:05:00 seq 2 dist 500, ST3 08:10:00 seq 3 dist 1001. T2: same stops at 09:00/09:05/09:10 with dists. T3: LP1 25:00:00 seq 1, LP2 25:10:00 seq 2, LP3 25:15:00 seq 3, LP1 25:20:00 seq 4 (no shape_dist_traveled — projection path; the final LP1 must land near `Length`, which requires the stop projection to pass a hint of the previous stop's along-shape). T4: ST1 10:00 seq 1, ST3 10:10 seq 2.
  - `TestWriteFixture` writes `rider/testdata/fixture.zip` only when `WRITE_FIXTURE=1`; otherwise it asserts the on-disk file equals the in-memory bytes (so the committed fixture never drifts).

- [ ] **Step 1: Write the fixture helper** (`rider/fixture_test.go`) exactly as described: build each CSV as a string, write them into an in-memory `archive/zip`, return bytes. Add `rider/testdata/README.md`: "Generated by `WRITE_FIXTURE=1 go test ./rider -run TestWriteFixture`. Do not hand-edit."

- [ ] **Step 2: Write the failing tests** (`rider/index_test.go`, `rider/loader_test.go`)

```go
package rider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildIndex_TripsAndShapes(t *testing.T) {
	ix := fixtureIndex(t)
	st := ix.Stats()
	assert.Equal(t, 3, st.Trips, "T4 has no shape and must be excluded")
	assert.Equal(t, 2, st.Shapes)
	assert.Equal(t, "fixture", st.Source)
	assert.Equal(t, []string{"T1", "T2", "T3"}, ix.TripIDs())
	_, ok := ix.Trip("T4")
	assert.False(t, ok)
	assert.Equal(t, "America/Los_Angeles", ix.Timezone().String())
}

func TestBuildIndex_StopTimes_WithShapeDist(t *testing.T) {
	ix := fixtureIndex(t)
	trip, ok := ix.Trip("T1")
	require.True(t, ok)
	assert.Equal(t, "R1", trip.RouteID)
	assert.Equal(t, "WEEKDAY", trip.ServiceID)
	require.Len(t, trip.StopTimes, 3)
	assert.Equal(t, "ST2", trip.StopTimes[1].StopID)
	assert.Equal(t, 2, trip.StopTimes[1].Sequence)
	assert.InDelta(t, 500, trip.StopTimes[1].AlongShape, 1)
	assert.Equal(t, 8*time.Hour+5*time.Minute, trip.StopTimes[1].Arrival)
	assert.InDelta(t, 1001, trip.StopTimes[2].AlongShape, 5)
}

func TestBuildIndex_StopTimes_ProjectedWhenNoShapeDist(t *testing.T) {
	ix := fixtureIndex(t)
	trip, ok := ix.Trip("T3")
	require.True(t, ok)
	require.Len(t, trip.StopTimes, 4)
	assert.InDelta(t, 0, trip.StopTimes[0].AlongShape, 2)
	assert.InDelta(t, trip.Shape.Cumulative[2], trip.StopTimes[1].AlongShape, 5)
	assert.InDelta(t, trip.Shape.Cumulative[3], trip.StopTimes[2].AlongShape, 5)
	assert.InDelta(t, trip.Shape.Length, trip.StopTimes[3].AlongShape, 5, "last LP1 must project to the END of the loop, not the start")
	assert.Equal(t, 25*time.Hour, trip.StopTimes[0].Arrival)
}

func TestBuildIndex_RescalesShapeDistUnits(t *testing.T) {
	// Same fixture but shape_dist_traveled in kilometres (0, 0.5, 1.001).
	ix := fixtureIndexWithShapeDistScale(t, 0.001)
	trip, _ := ix.Trip("T1")
	assert.InDelta(t, 500, trip.StopTimes[1].AlongShape, 2)
}

func TestActiveOn(t *testing.T) {
	ix := fixtureIndex(t)
	t1, _ := ix.Trip("T1") // WEEKDAY
	t2, _ := ix.Trip("T2") // SAT
	assert.True(t, ix.ActiveOn(t1, "20260902"))   // Wednesday
	assert.False(t, ix.ActiveOn(t1, "20260905"))  // Saturday
	assert.True(t, ix.ActiveOn(t2, "20260905"))
	assert.False(t, ix.ActiveOn(t1, "20260907"))  // Labor Day removed
	assert.True(t, ix.ActiveOn(t2, "20260907"))   // added
	assert.False(t, ix.ActiveOn(t1, "20270106"))  // outside range
	assert.False(t, ix.ActiveOn(t1, "garbage"))
}

func TestServiceDate_ThreeAMBoundary(t *testing.T) {
	ix := fixtureIndex(t)
	la := ix.Timezone()
	assert.Equal(t, "20260901", ix.ServiceDate(time.Date(2026, 9, 2, 2, 59, 0, 0, la)))
	assert.Equal(t, "20260902", ix.ServiceDate(time.Date(2026, 9, 2, 3, 0, 0, 0, la)))
	// A UTC instant is converted to agency local time first: 09:30Z = 02:30 PDT.
	assert.Equal(t, "20260901", ix.ServiceDate(time.Date(2026, 9, 2, 9, 30, 0, 0, time.UTC)))
}

func TestServiceDayStart(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	start, err := ServiceDayStart("20260902", la)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 2, 0, 0, 0, 0, la), start)
	_, err = ServiceDayStart("2026-09-02", la)
	assert.Error(t, err)
}

func TestScheduledOffsetAt(t *testing.T) {
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T1") // ST1 08:00 @0, ST2 08:05 @500, ST3 08:10 @1001
	assert.Equal(t, 8*time.Hour, ScheduledOffsetAt(trip, -10))
	assert.InDelta(t, float64(8*time.Hour+150*time.Second), float64(ScheduledOffsetAt(trip, 250)), float64(3*time.Second))
	assert.Equal(t, 8*time.Hour+10*time.Minute, ScheduledOffsetAt(trip, 5000))
}

func TestBuildIndex_ErrorsWithoutTimezone(t *testing.T) {
	_, err := BuildIndex(fixtureStatic(t, "Not/AZone"), "x", time.Now())
	assert.Error(t, err)
}

func TestLoadStatic_FileAndHTTP(t *testing.T) {
	zipBytes := buildFixtureZip(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "gtfs.zip")
	require.NoError(t, os.WriteFile(path, zipBytes, 0o644))

	static, err := LoadStatic(context.Background(), path, http.DefaultClient)
	require.NoError(t, err)
	assert.Len(t, static.Trips, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) }))
	defer srv.Close()
	static, err = LoadStatic(context.Background(), srv.URL+"/gtfs.zip", srv.Client())
	require.NoError(t, err)
	assert.Len(t, static.Trips, 4)

	_, err = LoadStatic(context.Background(), filepath.Join(dir, "missing.zip"), http.DefaultClient)
	assert.Error(t, err)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	_, err = LoadStatic(context.Background(), bad.URL, bad.Client())
	assert.Error(t, err)
}

func TestRefresher_SwapsOnSuccessKeepsOnFailure(t *testing.T) {
	first := fixtureIndex(t)
	var calls atomic.Int32
	loader := func(ctx context.Context) (*Index, error) {
		n := calls.Add(1)
		if n == 1 {
			return nil, errors.New("boom")
		}
		return BuildIndex(fixtureStatic(t, "America/Los_Angeles"), "second", time.Now()), nil
	}
	r := NewRefresher(first, func(ctx context.Context) (*Index, error) { ix, err := loader(ctx); return ix, err })
	assert.Same(t, first, r.Current())
	assert.Error(t, r.RefreshNow(context.Background()))
	assert.Same(t, first, r.Current(), "failed refresh keeps the old index")
	require.NoError(t, r.RefreshNow(context.Background()))
	assert.Equal(t, "second", r.Current().Stats().Source)
	assert.Same(t, first, first, "old TripInfo pointers stay valid for rides in progress")
}

func TestWriteFixture(t *testing.T) {
	b := buildFixtureZip(t)
	path := filepath.Join("testdata", "fixture.zip")
	if os.Getenv("WRITE_FIXTURE") == "1" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, b, 0o644))
		return
	}
	onDisk, err := os.ReadFile(path)
	require.NoError(t, err, "run WRITE_FIXTURE=1 go test ./rider -run TestWriteFixture")
	assert.Equal(t, b, onDisk, "committed fixture drifted; regenerate it")
}
```

`fixtureStatic(t, tz string) *gtfs.Static` and `fixtureIndexWithShapeDistScale(t, scale float64) *Index` are additional helpers in `fixture_test.go` (the zip builder takes the timezone and a shape-dist multiplier as parameters; the plain helpers pass `America/Los_Angeles` and `1`). Note: `BuildIndex` in `TestRefresher` is called with two return values; wrap accordingly (`ix, err := BuildIndex(...)`; `require.NoError`).

- [ ] **Step 3: Run → FAIL.** `go get github.com/OneBusAway/go-gtfs@v1.1.1 && go test ./rider/ -run 'TestBuildIndex|TestActiveOn|TestServiceDate|TestServiceDayStart|TestScheduledOffsetAt|TestLoadStatic|TestRefresher|TestWriteFixture'` fails to compile.

- [ ] **Step 4: Implement** `index.go` and `loader.go`.
  - `BuildIndex`: timezone from `static.Agencies[0].Timezone` (error if missing or `time.LoadLocation` fails). For each trip with a non-nil `Shape` having ≥ 2 points: build `ShapeGeom` once per shape id (cache map), then stop times: if **every** stop time has `ShapeDistanceTraveled` non-nil, use them with rescaling: `ratio = lastStopDist / shape.Length`; if `0.8 ≤ ratio ≤ 1.2` treat as metres, else multiply every value by `1/ratio` (so km → metres, feet → metres). Otherwise project each stop onto the shape using the previous stop's along-shape as hint (first stop: nil). Sort stop times by `Sequence`. Trips without a shape are skipped and counted in a `skipped` log line. `gtfs.Static` is not retained.
  - `ActiveOn`: parse `serviceDate` as `20060102` in the index timezone; false on parse error; check `StartDate ≤ d ≤ EndDate` (compare year/month/day), weekday bool, then `RemovedDates` (→ false) and `AddedDates` (→ true) override — compute: `active := inRange && weekday; if removed → false; if added → true`.
  - `ServiceDate`: `local := now.In(tz); if local.Hour() < 3 { local = local.AddDate(0,0,-1) }; return local.Format("20060102")`.
  - `ServiceDayStart`: `noon := time.Date(y, m, d, 12, 0, 0, 0, loc); return noon.Add(-12*time.Hour)`.
  - `ScheduledOffsetAt`: if `along ≤ StopTimes[0].AlongShape` return `StopTimes[0].Arrival`; if `≥ last.AlongShape` return `last.Arrival`; else find bracketing pair `(a, b)` and interpolate between `a.Departure` and `b.Arrival` by `(along − a.Along)/(b.Along − a.Along)` (guard zero span → `a.Departure`).
  - `LoadStatic`: if `source` has prefix `http://` or `https://` do a GET with the client (10-minute context timeout), require status 200, cap body at 512 MiB via `io.LimitReader`; otherwise `os.ReadFile`. Then `gtfs.ParseStatic`.
  - `Refresher`: `atomic.Pointer[Index]`; `Start` loops on a `time.Ticker`, calling `RefreshNow` and logging with `slog` on failure; returns on `ctx.Done()`.

- [ ] **Step 5: Generate the fixture and run → PASS.** `WRITE_FIXTURE=1 go test ./rider -run TestWriteFixture && go test ./rider/ -v && go vet ./...`

- [ ] **Step 6: Commit** — `git add go.mod go.sum rider && git commit -m "feat(rider): GTFS index, service dates, loader and refresher"`

---

### Task 3: `rider/verify.go` — thresholds and per-point verification

**Files:**
- Create: `rider/verify.go`, `rider/verify_test.go`

**Interfaces:**
- Consumes: `TripInfo`, `ShapeGeom.Project`, `ServiceDayStart`, `ScheduledOffsetAt` (Tasks 1–2).
- Produces:

```go
type Thresholds struct {
    MaxShapeDistance float64       // 60 m
    MaxSpeed         float64       // 35 m/s
    MaxAccuracy      float64       // 100 m
    PastWindow       time.Duration // 10 min
    FutureWindow     time.Duration // 30 s
    ScheduleEarly    time.Duration // 15 min
    ScheduleLate     time.Duration // 90 min
    PointMaxAge      time.Duration // 90 s (used by Session.Fresh / Aggregator)
    MaxRegression    float64       // 50 m
    CorroborationBase float64      // 150 m
    ContradictionExtra float64     // 500 m
}
func DefaultThresholds() Thresholds

type Point struct {
    Pos                      LatLon
    Accuracy, Speed, Bearing float64 // -1 when absent
    Timestamp                time.Time
}
type AcceptedPoint struct {
    Point
    AlongShape float64
}
type TrustedVehicle struct {
    VehicleID string
    Pos       LatLon
    Timestamp time.Time
}

type Outcome int
const ( Ignored Outcome = iota; OffRoute; Implausible; OffSchedule; Matched )
func (o Outcome) String() string // "ignored","off_route","implausible","off_schedule","matched"

type Corroboration int
const ( Unavailable Corroboration = iota; NoCorroboration; Corroborated; Contradicted )
func (c Corroboration) String() string // "unavailable","none","corroborated","contradicted"

type VerifyInput struct {
    Trip       *TripInfo
    Timezone   *time.Location
    StartDate  string // YYYYMMDD
    Prev       *AcceptedPoint
    Point      Point
    Trusted    *TrustedVehicle
    Thresholds Thresholds
    Now        time.Time
}
type Verdict struct {
    Outcome           Outcome
    Corroboration     Corroboration
    AlongShape        float64
    DistanceToShape   float64
    ScheduleDeviation time.Duration
    Reason            string
}
func Verify(in VerifyInput) Verdict
```

- [ ] **Step 1: Write the failing tests**

```go
package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t1Input returns a VerifyInput for fixture trip T1 (08:00–08:10 on 20260902,
// straight 1 km north) at 08:02:30 local, positioned 250 m along the shape,
// which is exactly on schedule.
func t1Input(t *testing.T) VerifyInput {
	t.Helper()
	ix := fixtureIndex(t)
	trip, ok := ix.Trip("T1")
	require.True(t, ok)
	now := time.Date(2026, 9, 2, 8, 2, 30, 0, ix.Timezone())
	return VerifyInput{
		Trip: trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Point:      Point{Pos: trip.Shape.PointAt(250), Accuracy: 8, Speed: 9, Bearing: 0, Timestamp: now},
		Thresholds: DefaultThresholds(), Now: now,
	}
}

func TestVerify_Matched(t *testing.T) {
	v := Verify(t1Input(t))
	assert.Equal(t, Matched, v.Outcome)
	assert.Equal(t, Unavailable, v.Corroboration)
	assert.InDelta(t, 250, v.AlongShape, 2)
	assert.InDelta(t, 0, v.DistanceToShape, 1)
	assert.InDelta(t, 0, float64(v.ScheduleDeviation), float64(5*time.Second))
}

func TestVerify_Ignored(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*VerifyInput)
	}{
		{"accuracy too coarse", func(in *VerifyInput) { in.Point.Accuracy = 150 }},
		{"too old", func(in *VerifyInput) { in.Point.Timestamp = in.Now.Add(-11 * time.Minute) }},
		{"too far in future", func(in *VerifyInput) { in.Point.Timestamp = in.Now.Add(31 * time.Second) }},
		{"null island", func(in *VerifyInput) { in.Point.Pos = LatLon{0, 0} }},
		{"latitude out of range", func(in *VerifyInput) { in.Point.Pos.Lat = 91 }},
		{"not newer than prev", func(in *VerifyInput) {
			in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Point.Timestamp}, AlongShape: 240}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := t1Input(t)
			tc.mut(&in)
			v := Verify(in)
			assert.Equal(t, Ignored, v.Outcome)
			assert.NotEmpty(t, v.Reason)
		})
	}
}

func TestVerify_OffRoute_AccuracyWidensTolerance(t *testing.T) {
	in := t1Input(t)
	// ~75 m east of the line.
	in.Point.Pos = LatLon{47.60225, -122.3290}
	in.Point.Accuracy = 5
	assert.Equal(t, OffRoute, Verify(in).Outcome, "75 m > 60 + 5")
	in.Point.Accuracy = 20
	assert.Equal(t, Matched, Verify(in).Outcome, "75 m <= 60 + 20")
	in.Point.Accuracy = -1
	assert.Equal(t, OffRoute, Verify(in).Outcome, "absent accuracy adds nothing")
}

func TestVerify_Implausible(t *testing.T) {
	in := t1Input(t)
	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-5 * time.Second)}, AlongShape: 20}
	assert.Equal(t, Implausible, Verify(in).Outcome, "230 m in 5 s = 46 m/s")

	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-30 * time.Second)}, AlongShape: 20}
	assert.Equal(t, Matched, Verify(in).Outcome, "230 m in 30 s = 7.7 m/s")

	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-30 * time.Second)}, AlongShape: 320}
	v := Verify(in)
	assert.Equal(t, Implausible, v.Outcome, "regressed 70 m > 50 m")

	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-30 * time.Second)}, AlongShape: 280}
	assert.Equal(t, Matched, Verify(in).Outcome, "30 m regression is GPS noise")
}

func TestVerify_ProjectionUsesPrevAsHint(t *testing.T) {
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T3") // loop, 25:00–25:20 on the service day
	now := time.Date(2026, 9, 3, 1, 19, 0, 0, ix.Timezone()) // 25:19 of service day 20260902
	in := VerifyInput{
		Trip: trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Prev:       &AcceptedPoint{Point: Point{Timestamp: now.Add(-10 * time.Second)}, AlongShape: trip.Shape.Length - 60},
		Point:      Point{Pos: LatLon{47.6001, -122.3299}, Accuracy: 5, Speed: 5, Bearing: 270, Timestamp: now},
		Thresholds: DefaultThresholds(), Now: now,
	}
	v := Verify(in)
	assert.Equal(t, Matched, v.Outcome)
	assert.Greater(t, v.AlongShape, trip.Shape.Length-100, "near the loop closure, the hint keeps us at the end")
}

func TestVerify_OffSchedule(t *testing.T) {
	in := t1Input(t)
	// 20 minutes early: 07:42:30 at 250 m along (scheduled 08:02:30).
	in.Now = in.Now.Add(-20 * time.Minute)
	in.Point.Timestamp = in.Now
	v := Verify(in)
	assert.Equal(t, OffSchedule, v.Outcome)
	assert.InDelta(t, float64(-20*time.Minute), float64(v.ScheduleDeviation), float64(5*time.Second))

	// 89 minutes late is still inside the window.
	in.Now = in.Now.Add(20*time.Minute + 89*time.Minute)
	in.Point.Timestamp = in.Now
	assert.Equal(t, Matched, Verify(in).Outcome)

	// 91 minutes late is not.
	in.Now = in.Now.Add(2 * time.Minute)
	in.Point.Timestamp = in.Now
	assert.Equal(t, OffSchedule, Verify(in).Outcome)
}

func TestVerify_OffSchedule_PostMidnightTrip(t *testing.T) {
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T3") // 25:00 = 01:00 next calendar day
	now := time.Date(2026, 9, 3, 1, 0, 30, 0, ix.Timezone())
	in := VerifyInput{
		Trip: trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Point:      Point{Pos: trip.Shape.PointAt(5), Accuracy: 5, Speed: 1, Bearing: 0, Timestamp: now},
		Thresholds: DefaultThresholds(), Now: now,
	}
	v := Verify(in)
	assert.Equal(t, Matched, v.Outcome)
	assert.InDelta(t, float64(30*time.Second), float64(v.ScheduleDeviation), float64(5*time.Second))
}

func TestVerify_Corroboration(t *testing.T) {
	base := t1Input(t)
	trip := base.Trip
	cases := []struct {
		name    string
		trusted TrustedVehicle
		want    Corroboration
	}{
		{"within allowance", TrustedVehicle{"v1", trip.Shape.PointAt(300), base.Now}, Corroborated},
		{"age widens allowance", TrustedVehicle{"v1", trip.Shape.PointAt(700), base.Now.Add(-10 * time.Second)}, Corroborated}, // 450 m ≤ 150 + 35*10
		{"grey zone", TrustedVehicle{"v1", trip.Shape.PointAt(650), base.Now}, NoCorroboration},                                 // 400 m: > 150, ≤ 650
		{"contradicted", TrustedVehicle{"v1", trip.Shape.PointAt(950), base.Now}, Contradicted},                                 // 700 m > 650
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tr := tc.trusted
			in.Trusted = &tr
			v := Verify(in)
			require.Equal(t, Matched, v.Outcome)
			assert.Equal(t, tc.want, v.Corroboration)
		})
	}

	t.Run("not computed for non-matched points", func(t *testing.T) {
		in := base
		in.Point.Accuracy = 500
		tr := TrustedVehicle{"v1", trip.Shape.PointAt(250), base.Now}
		in.Trusted = &tr
		v := Verify(in)
		assert.Equal(t, Ignored, v.Outcome)
		assert.Equal(t, Unavailable, v.Corroboration)
	})
}

func TestOutcomeAndCorroborationStrings(t *testing.T) {
	assert.Equal(t, "off_route", OffRoute.String())
	assert.Equal(t, "matched", Matched.String())
	assert.Equal(t, "none", NoCorroboration.String())
	assert.Equal(t, "contradicted", Contradicted.String())
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./rider/ -run 'TestVerify|TestOutcome'` fails to compile.

- [ ] **Step 3: Implement** `verify.go` following spec §4.5 exactly, in this order: (1) Ignored checks with reasons `"accuracy > 100 m"`, `"timestamp outside window"`, `"invalid coordinates"`, `"not newer than previous point"`; (2) projection with `hint = &Prev.AlongShape` when `Prev != nil`; OffRoute when `DistanceToShape > MaxShapeDistance + max(Accuracy, 0)`; (3) Implausible when `Prev != nil` and (`along − Prev.AlongShape < −MaxRegression` or `|Δalong|/Δt > MaxSpeed`); (4) schedule: `dayStart, err := ServiceDayStart(StartDate, Timezone)` (on error → `OffSchedule` with reason `"bad start_date"`), `scheduled := dayStart.Add(ScheduledOffsetAt(Trip, along))`, `dev := Point.Timestamp.Sub(scheduled)`; OffSchedule when `dev < −ScheduleEarly || dev > ScheduleLate`; (5) Matched. Corroboration only when Matched: `Unavailable` if `Trusted == nil`; else project `Trusted.Pos` (no hint), `gap`, `allowance = CorroborationBase + MaxSpeed × |Δt|.Seconds()`, then Corroborated / Contradicted (`gap > allowance + ContradictionExtra`) / NoCorroboration. Always fill `AlongShape`, `DistanceToShape`, and `ScheduleDeviation` when computed.

- [ ] **Step 4: Run → PASS.** `go test ./rider/ -v && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider && git commit -m "feat(rider): per-point verification against shape, schedule and trusted feed"`

---

### Task 4: `rider/session.go` + `rider/reputation.go` — ride state machine and scoring

**Files:**
- Create: `rider/session.go`, `rider/session_test.go`, `rider/reputation.go`, `rider/reputation_test.go`

**Interfaces:**
- Consumes: `Verdict`, `Point`, `AcceptedPoint`, `Outcome`, `Corroboration`, `TripInfo` (Tasks 2–3).
- Produces:

```go
type State int
const ( Pending State = iota; Verified; Rejected )
func (s State) String() string // "pending","verified","rejected"

type EndReason string
const (
    EndUserRequested EndReason = "user_requested"; EndArrived = "arrived"; EndStationary = "stationary"
    EndMaxDuration = "max_duration"; EndLocationUnavailable = "location_unavailable"
    EndAuthorizationDenied = "authorization_denied"; EndNetworkFailure = "network_failure"; EndAppTerminated = "app_terminated"
    EndOffRoute = "off_route"; EndContradicted = "contradicted"; EndImplausible = "implausible"; EndOffSchedule = "off_schedule"
    EndSuperseded = "superseded"; EndServerRestart = "server_restart"; EndIdle = "idle"
)
func ParseClientEndReason(s string) (EndReason, bool) // only the first eight (client-allowed) reasons

type Tier string
const ( TierNew Tier = "new"; TierTrusted Tier = "trusted"; TierBlocked Tier = "blocked" )
func ParseTier(s string) Tier // unknown → TierNew

type TripKey struct{ TripID, StartDate string }
type Counts struct{ Total, Matched, Corroborated, Contradicted int }
type Transition struct{ StateChanged, Ended bool; EndReason EndReason }

const ( verifyStreak = 3; rejectStreak = 5; contradictStreak = 3; corroborateCount = 12 )

type Session struct{ /* unexported */ }
func NewSession(id, riderID string, key TripKey, trip *TripInfo, tier Tier, startedAt time.Time) *Session
func (s *Session) ID() string; RiderID() string; Key() TripKey; Trip() *TripInfo; StartedAt() time.Time
func (s *Session) Tier() Tier; SetTier(Tier)
func (s *Session) Apply(v Verdict, p Point) Transition
func (s *Session) State() State
func (s *Session) Corroborated() bool
func (s *Session) Latest() *AcceptedPoint            // nil before the first accepted point
func (s *Session) LatestCorroboration() Corroboration // of the latest accepted point; Unavailable before any
func (s *Session) Counts() Counts
func (s *Session) OffRouteStreak() int
func (s *Session) LastAcceptedAt() time.Time         // zero before the first accepted point
func (s *Session) End(reason EndReason, at time.Time)
func (s *Session) Ended() bool; EndReason() EndReason; EndedAt() time.Time
func (s *Session) Fresh(now time.Time, maxAge time.Duration) bool // Latest != nil && now − Latest.Timestamp ≤ maxAge
func (s *Session) Publishable(now time.Time, maxAge time.Duration) bool // §4.7
func (s *Session) Summary() RideSummary

type RideSummary struct {
    State           State
    EndReason       EndReason
    Corroborated    bool
    MatchedDuration time.Duration // last matched timestamp − first matched timestamp
    Counts          Counts
    Duration        time.Duration // EndedAt − StartedAt (0 if not ended)
}
func ScoreDelta(s RideSummary) int
func TierFor(score int) Tier
func Clamp(score int) int
```

"Accepted point" = any non-`Ignored` verdict. `Contradicted` counts as a Matched point for the verification streak (§4.6). Rejection reason in a mixed streak = the most frequent outcome in the last `rejectStreak` non-matched outcomes (ties → the latest one).

- [ ] **Step 1: Write the failing tests**

```go
package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSession(t *testing.T) *Session {
	t.Helper()
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T1")
	return NewSession("ride-1", "rider-1", TripKey{"T1", "20260902"}, trip, TierNew, time.Unix(1_756_800_000, 0))
}

func verdict(o Outcome, c Corroboration, along float64) Verdict {
	return Verdict{Outcome: o, Corroboration: c, AlongShape: along}
}

func pointAt(sec int) Point { return Point{Timestamp: time.Unix(1_756_800_000+int64(sec), 0), Speed: 8} }

func TestSession_PendingToVerified(t *testing.T) {
	s := newTestSession(t)
	assert.Equal(t, Pending, s.State())
	assert.Nil(t, s.Latest())
	var tr Transition
	for i := 1; i <= 3; i++ {
		tr = s.Apply(verdict(Matched, Unavailable, float64(i*10)), pointAt(i*5))
	}
	assert.True(t, tr.StateChanged)
	assert.Equal(t, Verified, s.State())
	assert.Equal(t, 30.0, s.Latest().AlongShape)
	assert.Equal(t, Counts{Total: 3, Matched: 3}, s.Counts())
	assert.Equal(t, pointAt(15).Timestamp, s.LastAcceptedAt())
}

func TestSession_IgnoredTouchesNothing(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(5))
	s.Apply(verdict(Ignored, Unavailable, 0), pointAt(6))
	assert.Equal(t, Counts{Total: 1, Matched: 1}, s.Counts())
	assert.Equal(t, 10.0, s.Latest().AlongShape)
}

func TestSession_RejectAfterFiveNonMatched_MostFrequentReason(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(5))
	outs := []Outcome{OffRoute, Implausible, OffRoute, OffSchedule, OffRoute}
	var tr Transition
	for i, o := range outs {
		tr = s.Apply(verdict(o, Unavailable, 10), pointAt(10+i*5))
		if i < 4 {
			assert.False(t, tr.Ended)
			assert.Equal(t, i+1, s.OffRouteStreak())
		}
	}
	assert.True(t, tr.Ended)
	assert.Equal(t, EndOffRoute, tr.EndReason)
	assert.Equal(t, Rejected, s.State())
	assert.True(t, s.Ended())
}

func TestSession_MatchedResetsOffRouteStreak(t *testing.T) {
	s := newTestSession(t)
	for i := 0; i < 4; i++ {
		s.Apply(verdict(OffRoute, Unavailable, 0), pointAt(i*5))
	}
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(100))
	assert.Equal(t, 0, s.OffRouteStreak())
	tr := s.Apply(verdict(OffRoute, Unavailable, 0), pointAt(105))
	assert.False(t, tr.Ended)
}

func TestSession_ContradictedThreeTimesRejects(t *testing.T) {
	s := newTestSession(t)
	for i := 1; i <= 3; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(i)), pointAt(i*5))
	}
	require.Equal(t, Verified, s.State())
	s.Apply(verdict(Matched, Contradicted, 20), pointAt(20))
	s.Apply(verdict(Matched, Contradicted, 21), pointAt(25))
	s.Apply(verdict(Matched, Corroborated, 22), pointAt(30)) // resets
	s.Apply(verdict(Matched, Contradicted, 23), pointAt(35))
	s.Apply(verdict(Matched, Contradicted, 24), pointAt(40))
	tr := s.Apply(verdict(Matched, Contradicted, 25), pointAt(45))
	assert.True(t, tr.Ended)
	assert.Equal(t, EndContradicted, tr.EndReason)
	assert.Equal(t, Rejected, s.State())
	assert.Equal(t, 5, s.Counts().Contradicted)
	assert.Equal(t, 9, s.Counts().Matched, "contradicted points still count as matched geometry")
}

func TestSession_ContradictedCountsTowardVerification(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Contradicted, 1), pointAt(5))
	s.Apply(verdict(Matched, NoCorroboration, 2), pointAt(10))
	tr := s.Apply(verdict(Matched, Unavailable, 3), pointAt(15))
	assert.True(t, tr.StateChanged)
	assert.Equal(t, Verified, s.State())
}

func TestSession_CorroboratedAfterTwelve(t *testing.T) {
	s := newTestSession(t)
	for i := 1; i <= 11; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(i)), pointAt(i*5))
		assert.False(t, s.Corroborated())
	}
	s.Apply(verdict(Matched, Corroborated, 12), pointAt(60))
	assert.True(t, s.Corroborated())
	assert.Equal(t, Corroborated, s.LatestCorroboration())
	s.Apply(verdict(Matched, NoCorroboration, 13), pointAt(65))
	assert.True(t, s.Corroborated(), "sticky")
	assert.Equal(t, NoCorroboration, s.LatestCorroboration())
}

func TestSession_ApplyAfterEndIsNoop(t *testing.T) {
	s := newTestSession(t)
	s.End(EndUserRequested, pointAt(100).Timestamp)
	tr := s.Apply(verdict(Matched, Unavailable, 1), pointAt(101))
	assert.False(t, tr.StateChanged)
	assert.True(t, tr.Ended)
	assert.Equal(t, EndUserRequested, tr.EndReason)
	assert.Equal(t, Counts{}, s.Counts())
}

func TestSession_FreshAndPublishable(t *testing.T) {
	s := newTestSession(t)
	now := pointAt(200).Timestamp
	assert.False(t, s.Fresh(now, 90*time.Second))
	for i := 1; i <= 3; i++ {
		s.Apply(verdict(Matched, Unavailable, float64(i)), pointAt(150+i*5))
	}
	assert.True(t, s.Fresh(now, 90*time.Second))    // latest at 165, now 200
	assert.False(t, s.Fresh(now, 30*time.Second))
	assert.False(t, s.Publishable(now, 90*time.Second), "new rider, not corroborated")
	s.SetTier(TierTrusted)
	assert.True(t, s.Publishable(now, 90*time.Second))
	s.SetTier(TierBlocked)
	assert.False(t, s.Publishable(now, 90*time.Second))
	s.SetTier(TierNew)
	for i := 0; i < 12; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(100+i)), pointAt(170+i))
	}
	assert.True(t, s.Publishable(now, 90*time.Second), "corroborated new rider")
	s.End(EndArrived, now)
	assert.False(t, s.Publishable(now, 90*time.Second))
}

func TestSession_Summary(t *testing.T) {
	s := newTestSession(t)
	for i := 0; i < 12; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(i)), pointAt(i*30))
	}
	s.End(EndArrived, pointAt(400).Timestamp)
	sum := s.Summary()
	assert.Equal(t, Verified, sum.State)
	assert.Equal(t, EndArrived, sum.EndReason)
	assert.True(t, sum.Corroborated)
	assert.Equal(t, 330*time.Second, sum.MatchedDuration)
	assert.Equal(t, 400*time.Second, sum.Duration)
	assert.Equal(t, 12, sum.Counts.Corroborated)
}

func TestParseClientEndReason(t *testing.T) {
	r, ok := ParseClientEndReason("user_requested")
	assert.True(t, ok)
	assert.Equal(t, EndUserRequested, r)
	for _, bad := range []string{"off_route", "superseded", "idle", "", "nope"} {
		_, ok := ParseClientEndReason(bad)
		assert.False(t, ok, bad)
	}
}

func TestScoreDelta(t *testing.T) {
	cases := []struct {
		name string
		sum  RideSummary
		want int
	}{
		{"corroborated 5 min", RideSummary{State: Verified, EndReason: EndArrived, Corroborated: true, MatchedDuration: 5 * time.Minute}, 1},
		{"corroborated but short", RideSummary{State: Verified, EndReason: EndArrived, Corroborated: true, MatchedDuration: 4 * time.Minute}, 0},
		{"verified uncorroborated", RideSummary{State: Verified, EndReason: EndUserRequested, MatchedDuration: 20 * time.Minute}, 0},
		{"ended while pending", RideSummary{State: Pending, EndReason: EndSuperseded}, 0},
		{"rejected off route", RideSummary{State: Rejected, EndReason: EndOffRoute}, -1},
		{"rejected implausible", RideSummary{State: Rejected, EndReason: EndImplausible}, -1},
		{"rejected off schedule", RideSummary{State: Rejected, EndReason: EndOffSchedule}, -1},
		{"rejected contradicted", RideSummary{State: Rejected, EndReason: EndContradicted}, -3},
		{"corroborated then superseded", RideSummary{State: Verified, EndReason: EndSuperseded, Corroborated: true, MatchedDuration: 6 * time.Minute}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, ScoreDelta(tc.sum)) })
	}
}

func TestTierForAndClamp(t *testing.T) {
	assert.Equal(t, TierBlocked, TierFor(-3))
	assert.Equal(t, TierNew, TierFor(-2))
	assert.Equal(t, TierNew, TierFor(2))
	assert.Equal(t, TierTrusted, TierFor(3))
	assert.Equal(t, 10, Clamp(11))
	assert.Equal(t, -10, Clamp(-11))
	assert.Equal(t, 4, Clamp(4))
	assert.Equal(t, TierNew, ParseTier("weird"))
	assert.Equal(t, TierTrusted, ParseTier("trusted"))
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./rider/ -run 'TestSession|TestParseClientEndReason|TestScoreDelta|TestTierFor'` fails to compile.

- [ ] **Step 3: Implement** `session.go` and `reputation.go` per the Interfaces block and spec §4.6–4.7. Keep a small ring of the last 5 non-matched outcomes for the rejection reason. `Publishable` = `State()==Verified && !ended && Fresh(now,maxAge) && tier != TierBlocked && (tier == TierTrusted || corroborated)`. `Summary().MatchedDuration` = `lastMatchedAt − firstMatchedAt` (0 if < 2 matched points). `ScoreDelta`: contradicted → −3; other rejected reasons → −1; `Corroborated && MatchedDuration ≥ 5 min` → +1; else 0.

- [ ] **Step 4: Run → PASS.** `go test ./rider/ -v && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider && git commit -m "feat(rider): ride session state machine and reputation policy"`

---

### Task 5: `rider/trusted.go` — trusted GTFS-RT feed poller

**Files:**
- Create: `rider/trusted.go`, `rider/trusted_test.go`

**Interfaces:**
- Consumes: `TripKey`, `TrustedVehicle`, `LatLon` (Tasks 3–4); `github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs` (`gtfs.FeedMessage`, `proto.Unmarshal`).
- Produces:

```go
type FeedHealth struct {
    URL         string
    LastSuccess time.Time
    LastError   string
    Entities    int
}
type TrustedFeed struct{ /* unexported */ }
func NewTrustedFeed(urls []string, client *http.Client, maxAge time.Duration) *TrustedFeed
func (f *TrustedFeed) Configured() bool                       // len(urls) > 0
func (f *TrustedFeed) Poll(ctx context.Context)               // one round over all URLs; never returns an error, records per-feed health
func (f *TrustedFeed) Start(ctx context.Context, every time.Duration) // Poll immediately, then on a ticker until ctx is done
func (f *TrustedFeed) Lookup(key TripKey, now time.Time) (TrustedVehicle, bool) // exact key, then {TripID, ""}; age-filtered
func (f *TrustedFeed) Covers(key TripKey, now time.Time) bool
func (f *TrustedFeed) Health() []FeedHealth
```

Per-URL state: `etag`, `lastModified` strings sent back as `If-None-Match` / `If-Modified-Since`; a `304` keeps the previous entities for that URL and updates `LastSuccess`. Entities: skip when `vehicle.trip.trip_id` is empty, `position` is nil, or `timestamp` is 0. Store under `TripKey{trip_id, start_date}` (start_date may be empty). The map is rebuilt per URL on each successful (200) poll and merged across URLs (later URLs win on key collision). `Lookup` drops entities whose `now − Timestamp > maxAge`.

- [ ] **Step 1: Write the failing tests**

```go
package rider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gtfsrt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func feedBytes(t *testing.T, entities ...*gtfsrt.FeedEntity) []byte {
	t.Helper()
	v, inc := "2.0", gtfsrt.FeedHeader_FULL_DATASET
	b, err := proto.Marshal(&gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: &v, Incrementality: &inc, Timestamp: proto.Uint64(uint64(time.Now().Unix()))},
		Entity: entities,
	})
	require.NoError(t, err)
	return b
}

func vpEntity(id, tripID, startDate string, lat, lon float64, ts time.Time) *gtfsrt.FeedEntity {
	trip := &gtfsrt.TripDescriptor{TripId: proto.String(tripID)}
	if startDate != "" {
		trip.StartDate = proto.String(startDate)
	}
	return &gtfsrt.FeedEntity{
		Id: proto.String(id),
		Vehicle: &gtfsrt.VehiclePosition{
			Trip:      trip,
			Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh-" + id)},
			Position:  &gtfsrt.Position{Latitude: proto.Float32(float32(lat)), Longitude: proto.Float32(float32(lon))},
			Timestamp: proto.Uint64(uint64(ts.Unix())),
		},
	}
}

func TestTrustedFeed_LookupExactThenDateless(t *testing.T) {
	now := time.Now()
	body := feedBytes(t,
		vpEntity("1", "T1", "20260902", 47.60, -122.33, now),
		vpEntity("2", "T3", "", 47.61, -122.32, now),
		vpEntity("3", "T9", "20260902", 47.62, -122.31, now.Add(-10*time.Minute)), // stale
		&gtfsrt.FeedEntity{Id: proto.String("4"), Vehicle: &gtfsrt.VehiclePosition{Position: &gtfsrt.Position{Latitude: proto.Float32(1), Longitude: proto.Float32(1)}}}, // no trip
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()

	f := NewTrustedFeed([]string{srv.URL}, srv.Client(), 5*time.Minute)
	assert.True(t, f.Configured())
	f.Poll(context.Background())

	v, ok := f.Lookup(TripKey{"T1", "20260902"}, now)
	require.True(t, ok)
	assert.Equal(t, "veh-1", v.VehicleID)
	assert.InDelta(t, 47.60, v.Pos.Lat, 0.0001)

	_, ok = f.Lookup(TripKey{"T1", "20260903"}, now)
	assert.False(t, ok, "different start_date does not match a dated entity")

	v, ok = f.Lookup(TripKey{"T3", "20260902"}, now)
	require.True(t, ok, "dateless entity matches any start_date")
	assert.Equal(t, "veh-2", v.VehicleID)
	assert.True(t, f.Covers(TripKey{"T3", "20261231"}, now))

	_, ok = f.Lookup(TripKey{"T9", "20260902"}, now)
	assert.False(t, ok, "stale entity filtered")
	assert.False(t, f.Covers(TripKey{"T9", "20260902"}, now))

	h := f.Health()
	require.Len(t, h, 1)
	assert.Equal(t, 3, h[0].Entities, "entity without a trip is dropped")
	assert.Empty(t, h[0].LastError)
	assert.WithinDuration(t, now, h[0].LastSuccess, 5*time.Second)
}

func TestTrustedFeed_ETagAnd304KeepsEntities(t *testing.T) {
	now := time.Now()
	body := feedBytes(t, vpEntity("1", "T1", "20260902", 47.60, -122.33, now))
	var hits atomic.Int32
	var sawIfNoneMatch atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"abc"` {
			sawIfNoneMatch.Store(true)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	f := NewTrustedFeed([]string{srv.URL}, srv.Client(), 5*time.Minute)
	f.Poll(context.Background())
	f.Poll(context.Background())
	assert.Equal(t, int32(2), hits.Load())
	assert.True(t, sawIfNoneMatch.Load())
	_, ok := f.Lookup(TripKey{"T1", "20260902"}, now)
	assert.True(t, ok, "304 keeps the previous entities")
	assert.Equal(t, 1, f.Health()[0].Entities)
}

func TestTrustedFeed_ErrorFeedDoesNotPoisonHealthyOne(t *testing.T) {
	now := time.Now()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(feedBytes(t, vpEntity("1", "T1", "20260902", 47.60, -122.33, now)))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()

	f := NewTrustedFeed([]string{bad.URL, good.URL}, http.DefaultClient, 5*time.Minute)
	f.Poll(context.Background())
	_, ok := f.Lookup(TripKey{"T1", "20260902"}, now)
	assert.True(t, ok)
	h := f.Health()
	require.Len(t, h, 2)
	assert.Contains(t, h[0].LastError, "503")
	assert.True(t, h[0].LastSuccess.IsZero())
	assert.Empty(t, h[1].LastError)

	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not protobuf at all, definitely not")) }))
	defer garbage.Close()
	g := NewTrustedFeed([]string{garbage.URL}, http.DefaultClient, 5*time.Minute)
	g.Poll(context.Background())
	assert.NotEmpty(t, g.Health()[0].LastError)
}

func TestTrustedFeed_Unconfigured(t *testing.T) {
	f := NewTrustedFeed(nil, http.DefaultClient, time.Minute)
	assert.False(t, f.Configured())
	_, ok := f.Lookup(TripKey{"T1", "x"}, time.Now())
	assert.False(t, ok)
	assert.Empty(t, f.Health())
	f.Poll(context.Background()) // must not panic
}

func TestTrustedFeed_StartPollsUntilCancelled(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(feedBytes(t))
	}))
	defer srv.Close()
	f := NewTrustedFeed([]string{srv.URL}, srv.Client(), time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Start(ctx, 20*time.Millisecond); close(done) }()
	assert.Eventually(t, func() bool { return hits.Load() >= 3 }, 2*time.Second, 10*time.Millisecond)
	cancel()
	<-done
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./rider/ -run TestTrustedFeed` fails to compile.

- [ ] **Step 3: Implement** `trusted.go`: per-URL struct `{url, etag, lastModified string; entities map[TripKey]TrustedVehicle; health FeedHealth}` under one `sync.RWMutex`; `Poll` iterates URLs sequentially with a 15 s per-request `context.WithTimeout`, GET with conditional headers, handles 304 / 200 / other (error `fmt.Errorf("status %d", code)`), body cap 32 MiB, `proto.Unmarshal` into `gtfsrt.FeedMessage`, rebuilds the map; on any error sets `health.LastError = err.Error()` and keeps old entities. `Lookup` merges by scanning URLs in order, so later URLs override earlier ones for the same key; check exact key first across all URLs, then dateless key.

- [ ] **Step 4: Run → PASS.** `go test ./rider/ -v && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider && git commit -m "feat(rider): trusted GTFS-RT vehicle positions poller"`

---

### Task 6: `rider/aggregator.go` — session registry, batch application, estimates

**Files:**
- Create: `rider/aggregator.go`, `rider/aggregator_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces:

```go
var ErrUnknownRide = errors.New("unknown or ended ride")

type AppliedPoint struct {
    Point
    Verdict Verdict
}
type BatchResult struct {
    State          State
    Published      bool          // Session.Publishable at the end of the batch
    Corroboration  Corroboration // Session.LatestCorroboration
    Accepted       int           // non-ignored points applied
    Ignored        int
    OffRouteStreak int
    Ended          bool
    EndReason      EndReason
    Points         []AppliedPoint // non-ignored points, in application order (for persistence)
    Counts         Counts
    Corroborated   bool
}
type TripEstimate struct {
    Key          TripKey
    RouteID      string
    Pos          LatLon
    Bearing      float64
    Speed        *float64
    Timestamp    time.Time
    StopID       string // "" past the last stop
    StopSequence int    // 0 when StopID == ""
    Riders       int
}

const ( idleTimeout = 15 * time.Minute; maxRideDuration = 3 * time.Hour; consensusDistance = 100.0 )

type Aggregator struct{ /* mutex, sessions map[rideID]*Session, byRider map[riderID]rideID */ }
func NewAggregator(th Thresholds, tz *time.Location) *Aggregator
func (a *Aggregator) Add(s *Session)                                   // replaces any session for the same ride id
func (a *Aggregator) Owner(rideID string) (riderID string, ok bool)   // ok=false for unknown/ended
func (a *Aggregator) ActiveRideForRider(riderID string) (rideID string, ok bool)
func (a *Aggregator) ApplyBatch(rideID string, points []Point, lookup func(TripKey) (TrustedVehicle, bool), now time.Time) (BatchResult, error)
func (a *Aggregator) SetTier(riderID string, tier Tier)               // updates the rider's active session, if any
func (a *Aggregator) Remove(rideID string) *Session                   // nil if absent
func (a *Aggregator) Reap(now time.Time) []*Session                   // ends + removes idle/expired; returned for persistence
func (a *Aggregator) Estimates(now time.Time, covered func(TripKey) bool) []TripEstimate // sorted by TripID then StartDate
func (a *Aggregator) ActiveCount() int
func (a *Aggregator) PublishableCount(now time.Time, covered func(TripKey) bool) int   // number of estimates
func (a *Aggregator) TripStatus(key TripKey, now time.Time, covered func(TripKey) bool) (riderReported bool, riders int)
```

`ApplyBatch`: sort points by timestamp; for each point build `VerifyInput{Trip: s.Trip(), Timezone: tz, StartDate: s.Key().StartDate, Prev: s.Latest(), Point: p, Trusted: lookupPtr, Thresholds: th, Now: now}`, call `Verify`, then `s.Apply`; stop applying once the session ends (later points are ignored and counted in `Ignored`). `lookup` may be `nil` (→ `Trusted == nil`). `Reap`: ends sessions with `LastAcceptedAt` (or `StartedAt` if never accepted) older than `idleTimeout` with `EndIdle`, and sessions with `now − StartedAt > maxRideDuration` with `EndMaxDuration`. `Estimates` per §4.8: contributing = `Verified && !Ended && Fresh && Tier != Blocked`; group by key; skip covered keys; publishable when any member `Publishable` or ≥ 2 distinct rider ids with latest along-shape within `consensusDistance` of each other; median along-shape; drop members > 100 m from the median and recompute once (only if ≥ 1 remains); `Speed` = median of members' `Speed ≥ 0` (nil if none); `Timestamp` = newest member timestamp clamped to `now`; next stop = first `StopTimeInfo` with `AlongShape ≥ median`.

- [ ] **Step 1: Write the failing tests**

```go
package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aggFixture returns an aggregator plus helpers to build on-schedule points
// for T1 on 20260902 starting at 08:00 local.
type aggFixture struct {
	ix   *Index
	agg  *Aggregator
	t1   *TripInfo
	base time.Time // 08:00 local
}

func newAggFixture(t *testing.T) *aggFixture {
	t.Helper()
	ix := fixtureIndex(t)
	t1, _ := ix.Trip("T1")
	return &aggFixture{
		ix: ix, agg: NewAggregator(DefaultThresholds(), ix.Timezone()), t1: t1,
		base: time.Date(2026, 9, 2, 8, 0, 0, 0, ix.Timezone()),
	}
}

// onSchedulePoint is at `along` metres at the scheduled time for that spot
// (T1 runs 1001 m in 10 minutes ≈ 1.67 m/s) plus `offset`.
func (f *aggFixture) onSchedulePoint(along float64, offset time.Duration) Point {
	ts := f.base.Add(time.Duration(along/1001*600) * time.Second).Add(offset)
	return Point{Pos: f.t1.Shape.PointAt(along), Accuracy: 5, Speed: 1.7, Bearing: 0, Timestamp: ts}
}

func (f *aggFixture) addSession(id, riderID string, tier Tier) *Session {
	s := NewSession(id, riderID, TripKey{"T1", "20260902"}, f.t1, tier, f.base)
	f.agg.Add(s)
	return s
}

func (f *aggFixture) walk(t *testing.T, rideID string, from, to, step float64, lookup func(TripKey) (TrustedVehicle, bool)) BatchResult {
	t.Helper()
	var pts []Point
	for a := from; a <= to; a += step {
		pts = append(pts, f.onSchedulePoint(a, 0))
	}
	now := pts[len(pts)-1].Timestamp.Add(2 * time.Second)
	res, err := f.agg.ApplyBatch(rideID, pts, lookup, now)
	require.NoError(t, err)
	return res
}

func noCover(TripKey) bool { return false }

func TestAggregator_ApplyBatch_VerifiesAndReportsResult(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	res := f.walk(t, "r1", 0, 60, 10, nil) // 7 points, 6 s apart... all matched
	assert.Equal(t, Verified, res.State)
	assert.Equal(t, 7, res.Accepted)
	assert.Equal(t, 0, res.Ignored)
	assert.False(t, res.Published, "new rider without corroboration")
	assert.Equal(t, Unavailable, res.Corroboration)
	assert.Len(t, res.Points, 7)
	assert.Equal(t, Matched, res.Points[0].Verdict.Outcome)
	assert.False(t, res.Ended)

	owner, ok := f.agg.Owner("r1")
	assert.True(t, ok)
	assert.Equal(t, "rider-a", owner)
	id, ok := f.agg.ActiveRideForRider("rider-a")
	assert.True(t, ok)
	assert.Equal(t, "r1", id)
	assert.Equal(t, 1, f.agg.ActiveCount())
}

func TestAggregator_ApplyBatch_UnknownRide(t *testing.T) {
	f := newAggFixture(t)
	_, err := f.agg.ApplyBatch("nope", []Point{f.onSchedulePoint(0, 0)}, nil, f.base)
	assert.ErrorIs(t, err, ErrUnknownRide)
}

func TestAggregator_ApplyBatch_SortsAndStopsAtEnd(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	// Reversed order + 6 off-route points: 5 reject, the 6th and the trailing on-route point are ignored.
	off := LatLon{47.6045, -122.3200} // ~750 m east of the line
	pts := []Point{
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(50 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(40 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(30 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(20 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(10 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(60 * time.Second)},
		f.onSchedulePoint(100, 0),
	}
	res, err := f.agg.ApplyBatch("r1", pts, nil, f.base.Add(70*time.Second))
	require.NoError(t, err)
	assert.True(t, res.Ended)
	assert.Equal(t, EndOffRoute, res.EndReason)
	assert.Equal(t, Rejected, res.State)
	assert.Equal(t, 5, res.Accepted)
	assert.Equal(t, 2, res.Ignored)
	_, ok := f.agg.Owner("r1")
	assert.False(t, ok, "ended sessions are not owners")
	_, err = f.agg.ApplyBatch("r1", pts[:1], nil, f.base)
	assert.ErrorIs(t, err, ErrUnknownRide)
	assert.NotNil(t, f.agg.Remove("r1"))
	assert.Nil(t, f.agg.Remove("r1"))
}

func TestAggregator_TrustedRiderPublishes(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	res := f.walk(t, "r1", 0, 30, 10, nil)
	assert.True(t, res.Published)
	now := f.onSchedulePoint(30, 5*time.Second).Timestamp
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, TripKey{"T1", "20260902"}, est[0].Key)
	assert.Equal(t, "R1", est[0].RouteID)
	assert.Equal(t, 1, est[0].Riders)
	assert.InDelta(t, 30, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 2)
	assert.InDelta(t, 0, est[0].Bearing, 1)
	assert.Equal(t, "ST2", est[0].StopID)
	assert.Equal(t, 2, est[0].StopSequence)
	require.NotNil(t, est[0].Speed)
	assert.InDelta(t, 1.7, *est[0].Speed, 0.01)
	assert.False(t, est[0].Timestamp.After(now))
	assert.Equal(t, 1, f.agg.PublishableCount(now, noCover))
}

func TestAggregator_CorroboratedNewRiderPublishes(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	lookup := func(k TripKey) (TrustedVehicle, bool) {
		return TrustedVehicle{VehicleID: "v", Pos: f.t1.Shape.PointAt(80), Timestamp: f.base.Add(50 * time.Second)}, true
	}
	res := f.walk(t, "r1", 0, 110, 10, lookup) // 12 points, each within 150 m + age allowance
	assert.True(t, res.Corroborated)
	assert.True(t, res.Published)
	assert.Equal(t, Corroborated, res.Corroboration)
}

func TestAggregator_ConsensusPublishes(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	f.addSession("r2", "rider-b", TierNew)
	f.walk(t, "r1", 0, 30, 10, nil)
	f.walk(t, "r2", 20, 50, 10, nil)
	now := f.onSchedulePoint(50, 5*time.Second).Timestamp
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1, "two new riders within 100 m reach consensus")
	assert.Equal(t, 2, est[0].Riders)
	assert.InDelta(t, 40, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 3, "median of 30 and 50")

	reported, riders := f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.True(t, reported)
	assert.Equal(t, 2, riders)
}

func TestAggregator_NoConsensusWhenSameRiderOrFarApart(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	f.addSession("r2", "rider-a", TierNew) // same rider id → r1 is replaced as active ride but both sessions exist
	f.walk(t, "r1", 0, 30, 10, nil)
	f.walk(t, "r2", 20, 50, 10, nil)
	now := f.onSchedulePoint(50, 5*time.Second).Timestamp
	assert.Empty(t, f.agg.Estimates(now, noCover), "same rider twice is not consensus")

	g := newAggFixture(t)
	g.addSession("r1", "rider-a", TierNew)
	g.addSession("r2", "rider-b", TierNew)
	g.walk(t, "r1", 0, 30, 10, nil)
	g.walk(t, "r2", 300, 330, 10, nil)
	now = g.onSchedulePoint(330, 5*time.Second).Timestamp
	assert.Empty(t, g.Estimates(now, noCover), "300 m apart is not consensus")
	reported, riders := g.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.False(t, reported)
	assert.Equal(t, 2, riders)
}

func TestAggregator_OutlierExcludedFromEstimate(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	f.addSession("r2", "rider-b", TierTrusted)
	f.addSession("r3", "rider-c", TierTrusted)
	f.walk(t, "r1", 0, 100, 10, nil)
	f.walk(t, "r2", 10, 110, 10, nil)
	// r3 is 400 m ahead but reports at the same wall-clock times as r1, so it
	// stays fresh (3 min early is inside the schedule window).
	var pts []Point
	for i, a := 0, 400.0; a <= 500; a, i = a+10, i+1 {
		pts = append(pts, Point{Pos: f.t1.Shape.PointAt(a), Accuracy: 5, Speed: 1.7, Timestamp: f.base.Add(time.Duration(i*6) * time.Second)})
	}
	_, err := f.agg.ApplyBatch("r3", pts, nil, f.base.Add(70*time.Second))
	require.NoError(t, err)
	now := f.base.Add(70 * time.Second)
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, 2, est[0].Riders)
	assert.InDelta(t, 105, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 3)
}

func TestAggregator_CoveredAndStaleAndBlocked(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	f.walk(t, "r1", 0, 30, 10, nil)
	now := f.onSchedulePoint(30, 5*time.Second).Timestamp
	assert.Empty(t, f.agg.Estimates(now, func(TripKey) bool { return true }), "trusted feed covers the trip")
	assert.Empty(t, f.agg.Estimates(now.Add(2*time.Minute), noCover), "stale after 90 s")
	f.agg.SetTier("rider-a", TierBlocked)
	assert.Empty(t, f.agg.Estimates(now, noCover))
	f.agg.SetTier("rider-a", TierTrusted)
	assert.Len(t, f.agg.Estimates(now, noCover), 1)
}

func TestAggregator_Reap(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("idle", "rider-a", TierNew)
	f.addSession("old", "rider-b", TierNew)
	f.addSession("live", "rider-c", TierNew)
	f.walk(t, "live", 0, 10, 10, nil)
	_, err := f.agg.ApplyBatch("idle", []Point{f.onSchedulePoint(0, 0)}, nil, f.base.Add(5*time.Second))
	require.NoError(t, err)

	// At 08:16 every session's last accepted point (or start, for "old") is
	// more than 15 minutes old, so all three are reaped as idle.
	reaped := f.agg.Reap(f.base.Add(16 * time.Minute))
	require.Len(t, reaped, 3)
	for _, s := range reaped {
		assert.Equal(t, EndIdle, s.EndReason(), s.ID())
		assert.True(t, s.Ended())
	}
	assert.Equal(t, 0, f.agg.ActiveCount())

	g := newAggFixture(t)
	g.addSession("old", "rider-b", TierNew)
	_, err = g.agg.ApplyBatch("old", []Point{g.onSchedulePoint(0, 0)}, nil, g.base.Add(5*time.Second))
	require.NoError(t, err)
	// Keep it non-idle but past 3 h: the max-duration rule wins.
	reaped = g.agg.Reap(g.base.Add(3*time.Hour + time.Second))
	require.Len(t, reaped, 1)
	assert.Equal(t, EndMaxDuration, reaped[0].EndReason())
	assert.Equal(t, 0, g.agg.ActiveCount())
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./rider/ -run TestAggregator` fails to compile.

- [ ] **Step 3: Implement** `aggregator.go` per the Interfaces block. `Add` sets `byRider[riderID] = rideID` (an earlier active ride for the same rider stays in `sessions` until removed; `ActiveRideForRider` returns the newest). `Owner` returns false when the session is ended. `Estimates` implementation detail: median of a sorted slice (average of middle two for even counts).

- [ ] **Step 4: Run → PASS.** `go test ./rider/ -v -race && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider && git commit -m "feat(rider): aggregator with batch application, consensus and estimates"`

---

### Task 7: migration 000011 + sqlc queries + `rider_store.go`

**Files:**
- Create: `migrations/000011_riders.up.sql`, `migrations/000011_riders.down.sql`, `rider_store.go`, `rider_store_test.go`
- Modify: `db/query.sql` (append rider queries), regenerate `db/*.go` with `make generate`; `go.mod` (`go get github.com/google/uuid@latest`)

**Interfaces:**
- Consumes: `newTestStore(t)` (store_test.go), `db.Queries`, `pgx.Batch`.
- Produces (all in `rider_store.go`):

```go
var ErrRiderNotFound = errors.New("rider not found")
var ErrRideNotFound = errors.New("ride not found")

type Rider struct {
    ID, InstallationID, Platform, AppID, AppVersion string
    Attested bool
    Score int
    Tier string // "new" | "trusted" | "blocked"
    RidesTotal, RidesCorroborated, RidesRejected int
    CreatedAt, LastSeenAt time.Time
}
type Ride struct {
    ID, RiderID, TripID, StartDate, RouteID, VehicleID, BoardingStopID, DestinationStopID string
    Status string // "active" | "ended"
    State  string // "pending" | "verified" | "rejected"
    Corroborated bool
    EndReason string
    PointsTotal, PointsMatched, PointsCorroborated, PointsContradicted int
    StartedAt time.Time
    EndedAt   *time.Time
}
type RideProgress struct {
    State string; Corroborated bool
    PointsTotal, PointsMatched, PointsCorroborated, PointsContradicted int
}
type RidePointRecord struct {
    Latitude, Longitude float64
    Accuracy, Speed, Bearing *float64
    Timestamp int64
    Outcome, Corroboration string
    AlongShape, DistanceToShape float64
    ScheduleDeviationSeconds int
}
type RideOutcome struct {
    EndReason string
    Progress  RideProgress // final state/corroborated/counters
    ScoreDelta int
    Rejected  bool // increments rides_rejected
    Corroborated bool // increments rides_corroborated
}

type RiderRegistrar interface { RegisterRider(ctx context.Context, installationID, platform, appID, appVersion string) (rider *Rider, created bool, err error) }
type RiderReader interface { GetRider(ctx context.Context, id string) (*Rider, error) } // ErrRiderNotFound
type RideStarter interface { StartRide(ctx context.Context, ride *Ride) error }       // caller sets ID; StartedAt returned via RETURNING
type RidePointRecorder interface { RecordRidePoints(ctx context.Context, rideID, riderID string, points []RidePointRecord, progress RideProgress) error }
type RideFinisher interface {
    FinishRide(ctx context.Context, rideID string, outcome RideOutcome) (*Rider, error) // ErrRideNotFound; returns the rider after the score/tier update
    EndAllActiveRides(ctx context.Context, reason string) (int64, error)
}
type RideLister interface { ListRides(ctx context.Context, status string, limit, offset int) ([]Ride, error) }
type RiderStatsReader interface { CountRidersByTier(ctx context.Context) (map[string]int, error) }
type RidePointPruner interface { DeleteRidePointsBefore(ctx context.Context, cutoff time.Time) (int64, error) }
```

`FinishRide` runs one transaction: `EndRide` (`WHERE id = $1 AND status = 'active'`, `:execrows`; 0 rows → `ErrRideNotFound`), then `ApplyRideOutcome` on the rider: `score = LEAST(10, GREATEST(-10, score + $2))`, `tier = CASE WHEN new_score <= -3 THEN 'blocked' WHEN new_score >= 3 THEN 'trusted' ELSE 'new' END` (compute via a CTE or two statements: update score then update tier from score), `rides_total = rides_total + 1`, `rides_corroborated += $3::int`, `rides_rejected += $4::int`, `RETURNING *`.

- [ ] **Step 1: Write the migration**

`migrations/000011_riders.up.sql`: the three tables and five indexes from spec §4.9 **with `id TEXT PRIMARY KEY` and `rider_id TEXT`/`ride_id TEXT` instead of `UUID`** (Global Constraints), plus `CHECK (id <> '')` on `riders.id` and `rides.id`. `migrations/000011_riders.down.sql`: `DROP TABLE IF EXISTS ride_points; DROP TABLE IF EXISTS rides; DROP TABLE IF EXISTS riders;`.

- [ ] **Step 2: Add sqlc queries** to `db/query.sql`:

```sql
-- name: UpsertRider :one
INSERT INTO riders (id, installation_id, platform, app_id, app_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (installation_id) DO UPDATE
  SET last_seen_at = NOW(), app_version = EXCLUDED.app_version, platform = EXCLUDED.platform, app_id = EXCLUDED.app_id
RETURNING *, (xmax = 0) AS created;

-- name: GetRider :one
SELECT * FROM riders WHERE id = $1;

-- name: TouchRider :exec
UPDATE riders SET last_seen_at = NOW() WHERE id = $1;

-- name: ApplyRideOutcome :one
UPDATE riders SET
  score = LEAST(10, GREATEST(-10, score + $2)),
  rides_total = rides_total + 1,
  rides_corroborated = rides_corroborated + $3,
  rides_rejected = rides_rejected + $4,
  last_seen_at = NOW()
WHERE id = $1
RETURNING *;

-- name: SetRiderTier :exec
UPDATE riders SET tier = $2 WHERE id = $1;

-- name: CountRidersByTier :many
SELECT tier, COUNT(*) AS count FROM riders GROUP BY tier;

-- name: InsertRide :one
INSERT INTO rides (id, rider_id, trip_id, start_date, route_id, vehicle_id, boarding_stop_id, destination_stop_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetRide :one
SELECT * FROM rides WHERE id = $1;

-- name: UpdateRideProgress :exec
UPDATE rides SET state = $2, corroborated = $3, points_total = $4, points_matched = $5,
  points_corroborated = $6, points_contradicted = $7, updated_at = NOW()
WHERE id = $1;

-- name: EndRide :execrows
UPDATE rides SET status = 'ended', ended_at = NOW(), end_reason = $2, state = $3, corroborated = $4,
  points_total = $5, points_matched = $6, points_corroborated = $7, points_contradicted = $8, updated_at = NOW()
WHERE id = $1 AND status = 'active';

-- name: EndAllActiveRides :execrows
UPDATE rides SET status = 'ended', ended_at = NOW(), end_reason = $1, updated_at = NOW() WHERE status = 'active';

-- name: ListRides :many
SELECT * FROM rides WHERE status = $1 ORDER BY started_at DESC LIMIT $2 OFFSET $3;

-- name: InsertRidePoint :exec
INSERT INTO ride_points (ride_id, latitude, longitude, accuracy, speed, bearing, timestamp, outcome, corroboration,
  along_shape, distance_to_shape, schedule_deviation_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: CountRidePointsForRide :one
SELECT COUNT(*) FROM ride_points WHERE ride_id = $1;

-- name: DeleteRidePointsBefore :execrows
DELETE FROM ride_points WHERE received_at < $1;
```

Run `make generate`. (`CountRidePointsForRide` exists for tests only.) In `FinishRide`, after `ApplyRideOutcome` returns the new score, call `SetRiderTier(id, tierFor(score))` in the same transaction where `tierFor` mirrors `rider.TierFor` — import `rider` and call `string(rider.TierFor(int(row.Score)))`.

- [ ] **Step 3: Write the failing store tests** (`rider_store_test.go`; every test starts with `store := newTestStore(t)` which skips without `DATABASE_URL`)

```go
package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerTestRider(t *testing.T, store *Store) *Rider {
	t.Helper()
	r, created, err := store.RegisterRider(context.Background(), uuid.NewString(), "ios", "org.test", "1.0")
	require.NoError(t, err)
	require.True(t, created)
	return r
}

func startTestRide(t *testing.T, store *Store, riderID string) *Ride {
	t.Helper()
	ride := &Ride{ID: uuid.NewString(), RiderID: riderID, TripID: "T1", StartDate: "20260902", RouteID: "R1"}
	require.NoError(t, store.StartRide(context.Background(), ride))
	return ride
}

func TestRiderStore_RegisterIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	inst := uuid.NewString()
	r1, created, err := store.RegisterRider(context.Background(), inst, "ios", "org.test", "1.0")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "new", r1.Tier)
	assert.Equal(t, 0, r1.Score)

	r2, created, err := store.RegisterRider(context.Background(), inst, "ios", "org.test", "1.1")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, r1.ID, r2.ID)
	assert.Equal(t, "1.1", r2.AppVersion)

	got, err := store.GetRider(context.Background(), r1.ID)
	require.NoError(t, err)
	assert.Equal(t, inst, got.InstallationID)
	_, err = store.GetRider(context.Background(), uuid.NewString())
	assert.ErrorIs(t, err, ErrRiderNotFound)
}

func TestRiderStore_StartRideAndRecordPoints(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	ride := startTestRide(t, store, r.ID)
	assert.False(t, ride.StartedAt.IsZero())
	assert.Equal(t, "active", ride.Status)
	assert.Equal(t, "pending", ride.State)

	acc := 5.0
	pts := []RidePointRecord{
		{Latitude: 47.6, Longitude: -122.33, Accuracy: &acc, Timestamp: 1756800000, Outcome: "matched", Corroboration: "unavailable", AlongShape: 10, DistanceToShape: 1},
		{Latitude: 47.601, Longitude: -122.33, Timestamp: 1756800005, Outcome: "matched", Corroboration: "none", AlongShape: 20, DistanceToShape: 2, ScheduleDeviationSeconds: 30},
	}
	prog := RideProgress{State: "verified", Corroborated: false, PointsTotal: 2, PointsMatched: 2}
	require.NoError(t, store.RecordRidePoints(context.Background(), ride.ID, r.ID, pts, prog))

	n, err := store.queries.CountRidePointsForRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	got, err := store.queries.GetRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.Equal(t, "verified", got.State)
	assert.EqualValues(t, 2, got.PointsMatched)

	rider, err := store.GetRider(context.Background(), r.ID)
	require.NoError(t, err)
	assert.True(t, rider.LastSeenAt.After(r.LastSeenAt.Add(-time.Second)))
}

func TestRiderStore_FinishRide_UpdatesScoreAndTier(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	for i := 0; i < 3; i++ {
		ride := startTestRide(t, store, r.ID)
		updated, err := store.FinishRide(context.Background(), ride.ID, RideOutcome{
			EndReason: "arrived", ScoreDelta: 1, Corroborated: true,
			Progress: RideProgress{State: "verified", Corroborated: true, PointsTotal: 50, PointsMatched: 50, PointsCorroborated: 40},
		})
		require.NoError(t, err)
		assert.Equal(t, i+1, updated.Score)
		assert.Equal(t, i+1, updated.RidesTotal)
	}
	got, _ := store.GetRider(context.Background(), r.ID)
	assert.Equal(t, "trusted", got.Tier)
	assert.Equal(t, 3, got.RidesCorroborated)

	ride := startTestRide(t, store, r.ID)
	updated, err := store.FinishRide(context.Background(), ride.ID, RideOutcome{EndReason: "contradicted", ScoreDelta: -3, Rejected: true, Progress: RideProgress{State: "rejected"}})
	require.NoError(t, err)
	assert.Equal(t, 0, updated.Score)
	assert.Equal(t, "new", updated.Tier)
	assert.Equal(t, 1, updated.RidesRejected)

	rides, err := store.ListRides(context.Background(), "ended", 10, 0)
	require.NoError(t, err)
	require.Len(t, rides, 4)
	assert.Equal(t, "contradicted", rides[0].EndReason, "newest first")
	assert.NotNil(t, rides[0].EndedAt)

	_, err = store.FinishRide(context.Background(), ride.ID, RideOutcome{EndReason: "arrived"})
	assert.ErrorIs(t, err, ErrRideNotFound, "already ended")
}

func TestRiderStore_ScoreClamps(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	var last *Rider
	for i := 0; i < 5; i++ {
		ride := startTestRide(t, store, r.ID)
		var err error
		last, err = store.FinishRide(context.Background(), ride.ID, RideOutcome{EndReason: "contradicted", ScoreDelta: -3, Rejected: true})
		require.NoError(t, err)
	}
	assert.Equal(t, -10, last.Score)
	assert.Equal(t, "blocked", last.Tier)
}

func TestRiderStore_EndAllActiveRides_AndCascade(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	a := startTestRide(t, store, r.ID)
	b := startTestRide(t, store, r.ID)
	n, err := store.EndAllActiveRides(context.Background(), "server_restart")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(2))
	ga, _ := store.queries.GetRide(context.Background(), a.ID)
	gb, _ := store.queries.GetRide(context.Background(), b.ID)
	assert.Equal(t, "server_restart", ga.EndReason)
	assert.Equal(t, "ended", gb.Status)
	active, err := store.ListRides(context.Background(), "active", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, active)

	tiers, err := store.CountRidersByTier(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, tiers["new"], 1)

	// Deleting the rider cascades to rides and points.
	require.NoError(t, store.RecordRidePoints(context.Background(), a.ID, r.ID,
		[]RidePointRecord{{Latitude: 1, Longitude: 1, Timestamp: 1, Outcome: "matched", Corroboration: "none"}}, RideProgress{State: "pending", PointsTotal: 1, PointsMatched: 1}))
	_, err = store.pool.Exec(context.Background(), "DELETE FROM riders WHERE id = $1", r.ID)
	require.NoError(t, err)
	_, err = store.queries.GetRide(context.Background(), a.ID)
	assert.Error(t, err)
}

func TestRiderStore_DeleteRidePointsBefore(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	ride := startTestRide(t, store, r.ID)
	require.NoError(t, store.RecordRidePoints(context.Background(), ride.ID, r.ID,
		[]RidePointRecord{{Latitude: 1, Longitude: 1, Timestamp: 1, Outcome: "matched", Corroboration: "none"}}, RideProgress{State: "pending", PointsTotal: 1}))
	n, err := store.DeleteRidePointsBefore(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	n, err = store.DeleteRidePointsBefore(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
}
```

- [ ] **Step 4: Run → FAIL.** `docker compose up -d db` then `DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable' go test ./ -run TestRiderStore -v` fails to compile.

- [ ] **Step 5: Implement** `rider_store.go`: mapping helpers `riderFromRow(db.Rider) *Rider` and `rideFromRow(db.Ride) *Ride` (`pgtype.Timestamptz` → `*time.Time` for `EndedAt`; `int32` counters → `int`), `nullableFloat` reuse for point columns is not needed (points are write-only). `RegisterRider` uses `UpsertRider` with `uuid.NewString()` as the candidate id; the `created` column tells whether the insert happened. `RecordRidePoints`: `tx`, `q := s.queries.WithTx(tx)`, `pgx.Batch` with one `InsertRidePoint` queued per point using the generated `InsertRidePointParams` and `q.InsertRidePoint` — simplest: loop `q.InsertRidePoint(ctx, params)` inside the tx (batching is an optimisation; a loop inside one transaction is acceptable and simpler — do the loop), then `UpdateRideProgress`, `TouchRider`, commit. `FinishRide`: tx → `EndRide` execrows (0 → `ErrRideNotFound`) → `ApplyRideOutcome` → `SetRiderTier` → commit → return rider with the tier set.

- [ ] **Step 6: Run → PASS.** `DATABASE_URL=... go test ./ -run TestRiderStore -v && go test ./... && go vet ./...`

- [ ] **Step 7: Commit** — `git add migrations db rider_store.go rider_store_test.go go.mod go.sum && git commit -m "feat: riders/rides/ride_points schema and store"`

---

### Task 8: `rider_auth.go` — rider JWTs, `requireRider`, role check in `requireAuth`, keyed + registration rate limiters

**Files:**
- Create: `rider_auth.go`, `rider_auth_test.go`
- Modify: `auth.go:201-234` (`requireAuth` role check), `ratelimit.go` (add `NewKeyedRateLimiter`), `auth_test.go` (add one test), `ratelimit_test.go` if present (add one test)

**Interfaces:**
- Consumes: `parseSessionToken`, `contextWithClaims`, `claimsKey`, `writeJSON`, `allowInWindow`, `pruneStaleWindows`, `loginWindow`, `maxTrackedLogins`.
- Produces:

```go
// rider_auth.go
const riderRole = "rider"
func generateRiderJWT(riderID string, secret []byte, ttl time.Duration) (string, error) // claims: sub, role=rider, iat, exp, iss
func requireRider(secret []byte) func(http.Handler) http.Handler // Bearer only; 401 missing/invalid; 403 when role != rider
func riderIDFromContext(ctx context.Context) (string, bool)      // claims["sub"] when role == rider

const registrationIPLimit = 5
type RegistrationRateLimiter struct{ /* mu, byIP map[string]*loginWindowEntry, stop, once */ }
func NewRegistrationRateLimiter() *RegistrationRateLimiter
func (l *RegistrationRateLimiter) Allow(ip string) bool // allowInWindow(byIP, ip, registrationIPLimit, now); fails closed
func (l *RegistrationRateLimiter) Stop()

// ratelimit.go
func NewKeyedRateLimiter(interval time.Duration, burst int) *VehicleRateLimiter // NewVehicleRateLimiter() == NewKeyedRateLimiter(5s, 1)
```

`requireAuth` change: after `parseSessionToken`, read `claims["role"]`; if it is not `"driver"` or `"admin"`, respond `403 {"error":"forbidden"}` and log at Warn. Existing tests that mint tokens with other roles must be checked (`grep -n 'Role: "' *_test.go`); driver/admin remain valid.

- [ ] **Step 1: Write the failing tests** (`rider_auth_test.go`, plus one test appended to `auth_test.go`)

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRiderJWT_ClaimsAndTTL(t *testing.T) {
	tok, err := generateRiderJWT("rider-123", testSecret, 2*time.Hour)
	require.NoError(t, err)
	claims, err := parseSessionToken(tok, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "rider-123", claims["sub"])
	assert.Equal(t, "rider", claims["role"])
	exp, _ := claims["exp"].(float64)
	assert.InDelta(t, time.Now().Add(2*time.Hour).Unix(), int64(exp), 5)
}

func TestRequireRider(t *testing.T) {
	riderTok, _ := generateRiderJWT("rider-1", testSecret, time.Hour)
	driverTok, _ := generateJWT(&User{ID: 1, Email: "d@test.com", Role: "driver"}, testSecret)
	adminTok, _ := generateJWT(&User{ID: 2, Email: "a@test.com", Role: "admin"}, testSecret)

	var gotID string
	h := requireRider(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := riderIDFromContext(r.Context())
		require.True(t, ok)
		gotID = id
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name   string
		header string
		cookie bool
		want   int
	}{
		{"rider token", "Bearer " + riderTok, false, http.StatusNoContent},
		{"driver token", "Bearer " + driverTok, false, http.StatusForbidden},
		{"admin token", "Bearer " + adminTok, false, http.StatusForbidden},
		{"missing", "", false, http.StatusUnauthorized},
		{"garbage", "Bearer nope", false, http.StatusUnauthorized},
		{"cookie only is never accepted", "", true, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: riderTok})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
	assert.Equal(t, "rider-1", gotID)
}

func TestRequireAuth_RejectsRiderRole(t *testing.T) {
	riderTok, _ := generateRiderJWT("rider-1", testSecret, time.Hour)
	called := false
	h := requireAuth(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+riderTok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, called)
	assert.Equal(t, "forbidden", decodeError(t, w))
}

func TestRegistrationRateLimiter(t *testing.T) {
	l := NewRegistrationRateLimiter()
	defer l.Stop()
	for i := 0; i < registrationIPLimit; i++ {
		assert.True(t, l.Allow("1.2.3.4"), "attempt %d", i)
	}
	assert.False(t, l.Allow("1.2.3.4"))
	assert.True(t, l.Allow("5.6.7.8"), "other IPs unaffected")
}

func TestNewKeyedRateLimiter_Burst(t *testing.T) {
	l := NewKeyedRateLimiter(2*time.Second, 2)
	defer l.Stop()
	assert.True(t, l.Allow("k"))
	assert.True(t, l.Allow("k"))
	assert.False(t, l.Allow("k"))
	assert.True(t, l.Allow("other"))
}
```

`decodeError` already exists in `trip_handlers_test.go` (same package).

- [ ] **Step 2: Run → FAIL.** `go test ./ -run 'TestGenerateRiderJWT|TestRequireRider|TestRequireAuth_RejectsRiderRole|TestRegistrationRateLimiter|TestNewKeyedRateLimiter'` fails to compile.

- [ ] **Step 3: Implement** per the Interfaces block. In `ratelimit.go`, add an `interval time.Duration` and `burst int` field to `VehicleRateLimiter`, used in `Allow` when creating a new `rate.NewLimiter(rate.Every(interval), burst)`; `NewVehicleRateLimiter` delegates to `NewKeyedRateLimiter(rateInterval, 1)`. Add the role check to `requireAuth`. `RegistrationRateLimiter` copies the `LoginRateLimiter` cleanup pattern (ticker every minute, `pruneStaleWindows(byIP, now-2*loginWindow)`).

- [ ] **Step 4: Run → PASS.** `go test ./... && go vet ./...` (all existing auth/route tests must still pass).

- [ ] **Step 5: Commit** — `git add auth.go ratelimit.go rider_auth.go rider_auth_test.go auth_test.go && git commit -m "feat: rider JWTs, requireRider middleware, role check in requireAuth, keyed rate limiters"`

---

### Task 9: `rider_handlers.go` part 1 — service struct, JSON decoding, register, start ride, end ride, `finishRide`

**Files:**
- Create: `rider_handlers.go`, `rider_handlers_test.go` (with `fakeRiderStore` used by Tasks 9, 10, 12, 13)

**Interfaces:**
- Consumes: Tasks 2–8. `clientIP(r, trustProxy)` (proxy.go).
- Produces:

```go
type riderStore interface {
    RiderRegistrar; RiderReader; RideStarter; RidePointRecorder; RideFinisher; RideLister; RiderStatsReader; RidePointPruner
}
type trustedLookup interface {
    Configured() bool
    Lookup(key rider.TripKey, now time.Time) (rider.TrustedVehicle, bool)
    Covers(key rider.TripKey, now time.Time) bool
    Health() []rider.FeedHealth
}
type riderService struct {
    store        riderStore
    agg          *rider.Aggregator
    index        func() *rider.Index      // current index (Refresher.Current in production)
    trusted      trustedLookup
    regLimiter   *RegistrationRateLimiter
    batchLimiter *VehicleRateLimiter      // NewKeyedRateLimiter(2*time.Second, 2)
    jwtSecret    []byte
    jwtTTL       time.Duration
    trustProxy   bool
    thresholds   rider.Thresholds
    now          func() time.Time
    reportIntervalSeconds int // 5
    maxBatchSize          int // 12
}
func newRiderService(store riderStore, agg *rider.Aggregator, index func() *rider.Index, trusted trustedLookup,
    jwtSecret []byte, jwtTTL time.Duration, trustProxy bool, th rider.Thresholds) *riderService // creates both limiters, now=time.Now
func (s *riderService) Stop() // stops limiters

func decodeRiderJSON(w http.ResponseWriter, r *http.Request, dst any) bool // 415/400 handling; returns false after writing the error

func (s *riderService) handleRegister() http.HandlerFunc
func (s *riderService) handleStartRide() http.HandlerFunc
func (s *riderService) handleEndRide() http.HandlerFunc
func (s *riderService) handlePositions() http.HandlerFunc   // Task 10
func (s *riderService) handleTripStatus() http.HandlerFunc  // Task 10
func (s *riderService) finishRide(ctx context.Context, sess *rider.Session, reason rider.EndReason) (*Rider, error)
func (s *riderService) Estimates(now time.Time) []rider.TripEstimate // Task 11 (estimateSource)
func registerRiderRoutes(mux *http.ServeMux, s *riderService) // all five routes; requireRider on four of them

// request/response types (json tags exactly as in spec §4.3–4.4)
type riderRegisterRequest struct { InstallationID string `json:"installation_id"`; Platform string `json:"platform"`; AppID string `json:"app_id"`; AppVersion string `json:"app_version"`; Attestation *json.RawMessage `json:"attestation"` }
type riderRegisterResponse struct { RiderID string `json:"rider_id"`; Token string `json:"token"`; ReportIntervalSeconds int `json:"report_interval_seconds"`; MaxBatchSize int `json:"max_batch_size"` }
type startRideRequest struct { TripID string `json:"trip_id"`; StartDate string `json:"start_date"`; RouteID string `json:"route_id"`; VehicleID string `json:"vehicle_id"`; BoardingStopID string `json:"boarding_stop_id"`; DestinationStopID string `json:"destination_stop_id"` }
type rideDestination struct { StopID string `json:"stop_id"`; Latitude float64 `json:"latitude"`; Longitude float64 `json:"longitude"` }
type startRideResponse struct { RideID string `json:"ride_id"`; State string `json:"state"`; ReportIntervalSeconds int `json:"report_interval_seconds"`; MaxBatchSize int `json:"max_batch_size"`; Destination *rideDestination `json:"destination,omitempty"` }
type endRideRequest struct { Reason string `json:"reason"` }
type rideSummaryJSON struct { Points int `json:"points"`; Matched int `json:"matched"`; Corroborated int `json:"corroborated"`; DurationSeconds int `json:"duration_seconds"` }
type endRideResponse struct { Status string `json:"status"`; Summary rideSummaryJSON `json:"summary"` }
```

`finishRide`: `s.agg.Remove(sess.ID())` happens **after** a successful `FinishRide` (spec §4.9: reaping retries). Sequence: `sess.End(reason, now)` if not already ended → build `RideOutcome{EndReason: string(reason), ScoreDelta: rider.ScoreDelta(sum), Rejected: sum.State == rider.Rejected, Corroborated: sum.Corroborated, Progress: progressFrom(sess)}` → `store.FinishRide` → on success `agg.Remove` and `agg.SetTier(riderID, rider.ParseTier(updated.Tier))` → return. `progressFrom(sess)` maps `Counts`/`State`/`Corroborated` to `RideProgress`.

Handler rules (spec §4.3–4.4): start-ride `start_date`, when present, must match `^[0-9]{8}$` (`400` otherwise) before the active-on check (`422`). Registration validates the UUID with `uuid.Parse`, platform ∈ {ios, android, other}, lengths ≤ 100, `attestation` must be null/absent (`400 {"error":"attestation not supported"}` otherwise); rate limit first (`429`). Start ride: `404 unknown trip`, `422 trip not active on start_date`, `422 route_id mismatch`, supersede via `finishRide(..., rider.EndSuperseded)` when `agg.ActiveRideForRider` finds one, build `Ride`, `store.StartRide`, `rider.NewSession(ride.ID, riderID, key, trip, rider.ParseTier(r.Tier), ride.StartedAt)` where the rider is loaded with `GetRider` (needed for the tier), `agg.Add`. Destination: find `destination_stop_id` in `trip.StopTimes` → `rideDestination` from `StopTimeInfo.Pos`. End ride: parse reason with `rider.ParseClientEndReason` (`400` otherwise), `404` when `agg.Owner(id)` is missing or not this rider… but an ended ride that is *not in the aggregator* must return `409 {"error":"ride ended"}`: check `store.GetRider`-independent path: if `agg.Owner` fails, call `store.queries`? No — keep it simple: the aggregator is the source of truth for active rides; unknown id → `409 {"error":"ride ended"}` only when the id parses as a UUID, else `404`. Ownership mismatch → `404`.

- [ ] **Step 1: Write the fake store and the failing tests**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// fakeRiderStore is an in-memory riderStore for handler tests.
type fakeRiderStore struct {
	mu       sync.Mutex
	riders   map[string]*Rider // by id
	byInst   map[string]string // installation → id
	rides    map[string]*Ride
	points   map[string][]RidePointRecord
	failNext error // returned once by the next mutating call
}

func newFakeRiderStore() *fakeRiderStore {
	return &fakeRiderStore{riders: map[string]*Rider{}, byInst: map[string]string{}, rides: map[string]*Ride{}, points: map[string][]RidePointRecord{}}
}

func (f *fakeRiderStore) takeErr() error { e := f.failNext; f.failNext = nil; return e }

func (f *fakeRiderStore) RegisterRider(_ context.Context, inst, platform, appID, appVersion string) (*Rider, bool, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	if err := f.takeErr(); err != nil { return nil, false, err }
	if id, ok := f.byInst[inst]; ok {
		r := f.riders[id]; r.AppVersion = appVersion; r.LastSeenAt = time.Now()
		return r, false, nil
	}
	r := &Rider{ID: uuid.NewString(), InstallationID: inst, Platform: platform, AppID: appID, AppVersion: appVersion, Tier: "new", CreatedAt: time.Now(), LastSeenAt: time.Now()}
	f.riders[r.ID] = r; f.byInst[inst] = r.ID
	return r, true, nil
}
func (f *fakeRiderStore) GetRider(_ context.Context, id string) (*Rider, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	if r, ok := f.riders[id]; ok { return r, nil }
	return nil, ErrRiderNotFound
}
func (f *fakeRiderStore) StartRide(_ context.Context, ride *Ride) error {
	f.mu.Lock(); defer f.mu.Unlock()
	if err := f.takeErr(); err != nil { return err }
	ride.Status, ride.State, ride.StartedAt = "active", "pending", time.Now()
	cp := *ride; f.rides[ride.ID] = &cp
	return nil
}
func (f *fakeRiderStore) RecordRidePoints(_ context.Context, rideID, riderID string, pts []RidePointRecord, p RideProgress) error {
	f.mu.Lock(); defer f.mu.Unlock()
	if err := f.takeErr(); err != nil { return err }
	r, ok := f.rides[rideID]; if !ok { return ErrRideNotFound }
	f.points[rideID] = append(f.points[rideID], pts...)
	r.State, r.Corroborated, r.PointsTotal, r.PointsMatched, r.PointsCorroborated, r.PointsContradicted = p.State, p.Corroborated, p.PointsTotal, p.PointsMatched, p.PointsCorroborated, p.PointsContradicted
	return nil
}
func (f *fakeRiderStore) FinishRide(_ context.Context, rideID string, o RideOutcome) (*Rider, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	if err := f.takeErr(); err != nil { return nil, err }
	r, ok := f.rides[rideID]; if !ok || r.Status != "active" { return nil, ErrRideNotFound }
	now := time.Now()
	r.Status, r.EndedAt, r.EndReason = "ended", &now, o.EndReason
	r.State, r.Corroborated = o.Progress.State, o.Progress.Corroborated
	r.PointsTotal, r.PointsMatched, r.PointsCorroborated, r.PointsContradicted = o.Progress.PointsTotal, o.Progress.PointsMatched, o.Progress.PointsCorroborated, o.Progress.PointsContradicted
	rd := f.riders[r.RiderID]
	rd.Score = rider.Clamp(rd.Score + o.ScoreDelta); rd.Tier = string(rider.TierFor(rd.Score)); rd.RidesTotal++
	if o.Rejected { rd.RidesRejected++ }
	if o.Corroborated { rd.RidesCorroborated++ }
	return rd, nil
}
func (f *fakeRiderStore) EndAllActiveRides(_ context.Context, reason string) (int64, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	var n int64
	for _, r := range f.rides { if r.Status == "active" { r.Status, r.EndReason = "ended", reason; n++ } }
	return n, nil
}
func (f *fakeRiderStore) ListRides(_ context.Context, status string, limit, offset int) ([]Ride, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	var out []Ride
	for _, r := range f.rides { if r.Status == status { out = append(out, *r) } }
	if offset > len(out) { return nil, nil }
	out = out[offset:]
	if len(out) > limit { out = out[:limit] }
	return out, nil
}
func (f *fakeRiderStore) CountRidersByTier(_ context.Context) (map[string]int, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	m := map[string]int{}
	for _, r := range f.riders { m[r.Tier]++ }
	return m, nil
}
func (f *fakeRiderStore) DeleteRidePointsBefore(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

// fakeTrusted is a scriptable trustedLookup.
type fakeTrusted struct {
	mu       sync.Mutex
	vehicles map[rider.TripKey]rider.TrustedVehicle
	health   []rider.FeedHealth
}
func newFakeTrusted() *fakeTrusted { return &fakeTrusted{vehicles: map[rider.TripKey]rider.TrustedVehicle{}} }
func (f *fakeTrusted) Configured() bool { return true }
func (f *fakeTrusted) Lookup(k rider.TripKey, _ time.Time) (rider.TrustedVehicle, bool) { f.mu.Lock(); defer f.mu.Unlock(); v, ok := f.vehicles[k]; return v, ok }
func (f *fakeTrusted) Covers(k rider.TripKey, now time.Time) bool { _, ok := f.Lookup(k, now); return ok }
func (f *fakeTrusted) Health() []rider.FeedHealth { return f.health }
func (f *fakeTrusted) set(k rider.TripKey, v rider.TrustedVehicle) { f.mu.Lock(); f.vehicles[k] = v; f.mu.Unlock() }
func (f *fakeTrusted) clear(k rider.TripKey) { f.mu.Lock(); delete(f.vehicles, k); f.mu.Unlock() }

// riderTestEnv wires a riderService against the fixture GTFS, a fake store,
// and a fake trusted feed, with a controllable clock.
type riderTestEnv struct {
	svc     *riderService
	store   *fakeRiderStore
	trusted *fakeTrusted
	index   *rider.Index
	mux     *http.ServeMux
	now     time.Time
}

func loadFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	b, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	static, err := rider.ParseStaticBytes(b)
	require.NoError(t, err)
	ix, err := rider.BuildIndex(static, "fixture", time.Now())
	require.NoError(t, err)
	return ix
}

func newRiderTestEnv(t *testing.T) *riderTestEnv {
	t.Helper()
	ix := loadFixtureIndex(t)
	env := &riderTestEnv{store: newFakeRiderStore(), trusted: newFakeTrusted(), index: ix}
	env.now = time.Date(2026, 9, 2, 8, 0, 0, 0, ix.Timezone())
	agg := rider.NewAggregator(rider.DefaultThresholds(), ix.Timezone())
	env.svc = newRiderService(env.store, agg, func() *rider.Index { return ix }, env.trusted, testSecret, time.Hour, false, rider.DefaultThresholds())
	env.svc.now = func() time.Time { return env.now }
	// Permissive limiters by default so unrelated tests never trip them; the
	// rate-limit tests install strict ones explicitly.
	env.svc.regLimiter.Stop()
	env.svc.batchLimiter.Stop()
	env.svc.regLimiter = newPermissiveRegistrationLimiter()
	env.svc.batchLimiter = NewKeyedRateLimiter(time.Millisecond, 1_000)
	t.Cleanup(env.svc.Stop)
	env.mux = http.NewServeMux()
	registerRiderRoutes(env.mux, env.svc)
	return env
}

func (e *riderTestEnv) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func (e *riderTestEnv) register(t *testing.T) (riderID, token string) {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/rider/register", "", map[string]any{"installation_id": uuid.NewString(), "platform": "ios", "app_id": "org.test", "app_version": "1"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp riderRegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp.RiderID, resp.Token
}

func (e *riderTestEnv) startRide(t *testing.T, token string, body map[string]any) startRideResponse {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/rider/rides", token, body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp startRideResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

func TestRiderRegister(t *testing.T) {
	env := newRiderTestEnv(t)
	inst := uuid.NewString()
	body := map[string]any{"installation_id": inst, "platform": "ios", "app_id": "org.test", "app_version": "1.0", "attestation": nil}
	w := env.do(t, "POST", "/api/v1/rider/register", "", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var first riderRegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&first))
	assert.NotEmpty(t, first.RiderID)
	assert.Equal(t, 5, first.ReportIntervalSeconds)
	assert.Equal(t, 12, first.MaxBatchSize)
	claims, err := parseSessionToken(first.Token, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "rider", claims["role"])
	assert.Equal(t, first.RiderID, claims["sub"])

	w = env.do(t, "POST", "/api/v1/rider/register", "", body)
	assert.Equal(t, http.StatusOK, w.Code, "re-registration returns 200")
	var second riderRegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&second))
	assert.Equal(t, first.RiderID, second.RiderID)

	bad := []map[string]any{
		{"installation_id": "not-a-uuid", "platform": "ios"},
		{"installation_id": uuid.NewString(), "platform": "windows"},
		{"installation_id": uuid.NewString(), "platform": "ios", "attestation": map[string]any{"x": 1}},
		{"installation_id": uuid.NewString(), "platform": "ios", "unknown": 1},
	}
	for i, b := range bad {
		w := env.do(t, "POST", "/api/v1/rider/register", "", b)
		assert.Equal(t, http.StatusBadRequest, w.Code, "case %d: %s", i, w.Body.String())
	}

	req := httptest.NewRequest("POST", "/api/v1/rider/register", bytes.NewBufferString("{}"))
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestRiderRegister_RateLimited(t *testing.T) {
	env := newRiderTestEnv(t)
	env.svc.regLimiter = NewRegistrationRateLimiter()
	for i := 0; i < registrationIPLimit; i++ {
		env.register(t)
	}
	w := env.do(t, "POST", "/api/v1/rider/register", "", map[string]any{"installation_id": uuid.NewString(), "platform": "ios"})
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestStartRide(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)

	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1", "start_date": "20260902", "route_id": "R1", "destination_stop_id": "ST3"})
	assert.NotEmpty(t, resp.RideID)
	assert.Equal(t, "pending", resp.State)
	assert.Equal(t, 5, resp.ReportIntervalSeconds)
	require.NotNil(t, resp.Destination)
	assert.Equal(t, "ST3", resp.Destination.StopID)
	assert.InDelta(t, 47.609, resp.Destination.Latitude, 0.001)
	assert.Equal(t, 1, env.svc.agg.ActiveCount())
	stored := env.store.rides[resp.RideID]
	require.NotNil(t, stored)
	assert.Equal(t, "R1", stored.RouteID)
	assert.Equal(t, "ST3", stored.DestinationStopID)

	// Default start_date is the service date; no destination when unknown stop.
	resp2 := env.startRide(t, tok, map[string]any{"trip_id": "T1", "destination_stop_id": "NOPE"})
	assert.Nil(t, resp2.Destination)
	assert.Equal(t, "20260902", env.store.rides[resp2.RideID].StartDate)
	assert.Equal(t, "ended", env.store.rides[resp.RideID].Status, "first ride superseded")
	assert.Equal(t, "superseded", env.store.rides[resp.RideID].EndReason)
	assert.Equal(t, 1, env.svc.agg.ActiveCount())

	cases := []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"trip_id": "NOPE"}, http.StatusNotFound},
		{map[string]any{"trip_id": "T1", "start_date": "20260905"}, http.StatusUnprocessableEntity}, // Saturday
		{map[string]any{"trip_id": "T1", "route_id": "R2"}, http.StatusUnprocessableEntity},
		{map[string]any{"trip_id": ""}, http.StatusBadRequest},
		{map[string]any{"trip_id": "T1", "start_date": "2026-09-02"}, http.StatusBadRequest},
	}
	for i, tc := range cases {
		w := env.do(t, "POST", "/api/v1/rider/rides", tok, tc.body)
		assert.Equal(t, tc.want, w.Code, "case %d: %s", i, w.Body.String())
	}

	w := env.do(t, "POST", "/api/v1/rider/rides", "", map[string]any{"trip_id": "T1"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestEndRide(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	_, otherTok := env.register(t)
	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	w := env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", otherTok, map[string]any{"reason": "user_requested"})
	assert.Equal(t, http.StatusNotFound, w.Code, "another rider's ride looks nonexistent")

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "off_route"})
	assert.Equal(t, http.StatusBadRequest, w.Code, "server-only reason")

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var er endRideResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&er))
	assert.Equal(t, "ride ended", er.Status)
	assert.Equal(t, 0, er.Summary.Points)
	assert.Equal(t, "ended", env.store.rides[resp.RideID].Status)
	assert.Equal(t, "arrived", env.store.rides[resp.RideID].EndReason)
	assert.Equal(t, 0, env.svc.agg.ActiveCount())
	assert.Equal(t, 1, env.store.riders[riderID].RidesTotal)

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusConflict, w.Code)
	w = env.do(t, "POST", "/api/v1/rider/rides/not-a-uuid/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestFinishRide_StoreFailureKeepsSession(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.store.failNext = assert.AnError
	w := env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, 1, env.svc.agg.ActiveCount(), "session stays so reaping can retry")
	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, env.svc.agg.ActiveCount())
}
```

`newPermissiveRegistrationLimiter()` is a test helper in `rider_handlers_test.go` returning a `*RegistrationRateLimiter` whose limit never trips (add an unexported `limit int` field to `RegistrationRateLimiter`, defaulting to `registrationIPLimit`, and set it to `1 << 30` in the helper).

`rider.ParseStaticBytes(b []byte) (*gtfs.Static, error)` is a one-line exported wrapper around `gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})` — add it to `rider/loader.go` in this task so `package main` tests never import go-gtfs directly.

- [ ] **Step 2: Run → FAIL.** `go test ./ -run 'TestRiderRegister|TestStartRide|TestEndRide|TestFinishRide'` fails to compile.

- [ ] **Step 3: Implement** `rider_handlers.go` per the Interfaces block and handler rules above. `registerRiderRoutes` registers: `POST /api/v1/rider/register` (no middleware), and with `requireRider(s.jwtSecret)`: `POST /api/v1/rider/rides`, `POST /api/v1/rider/rides/{id}/positions`, `POST /api/v1/rider/rides/{id}/end`, `GET /api/v1/rider/trips/{trip_id}/status`. For Task 9, `handlePositions` and `handleTripStatus` may return `501` placeholders **only within this task**; Task 10 replaces them.

- [ ] **Step 4: Run → PASS.** `go test ./... && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider_handlers.go rider_handlers_test.go rider/loader.go && git commit -m "feat: rider registration, ride start/end handlers and finishRide"`

---

### Task 10: `rider_handlers.go` part 2 — positions batch and trip status

**Files:**
- Modify: `rider_handlers.go`, `rider_handlers_test.go`

**Interfaces:**
- Produces:

```go
type positionUpload struct {
    Latitude  float64  `json:"latitude"`
    Longitude float64  `json:"longitude"`
    Accuracy  *float64 `json:"accuracy"`
    Speed     *float64 `json:"speed"`
    Bearing   *float64 `json:"bearing"`
    Timestamp int64    `json:"timestamp"`
}
type positionsRequest struct { Positions []positionUpload `json:"positions"` }
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
```

`handlePositions` flow (spec §4.9): `riderIDFromContext` → `batchLimiter.Allow(riderID)` (`429`) → parse `{id}` (non-UUID → `404`) → `agg.Owner(id)` (missing → `409 ride ended`; other rider → `404`) → decode body (`400` for empty or `> maxBatchSize` positions; `latitude`/`longitude`/`timestamp` required — a zero timestamp → `400`) → convert to `[]rider.Point` (`-1` for absent optionals) → `agg.ApplyBatch(id, pts, lookup, now)` where `lookup` is `s.trusted.Lookup` bound to `now` when `s.trusted.Configured()`, else `nil` → load the rider tier from the session (blocked riders: skip persistence) → `store.RecordRidePoints(id, riderID, records, progress)` (`500` on error; session already advanced) → if `res.Ended`: `finishRide(ctx, sess, res.EndReason)` (log on error) → respond `200`.

`handleTripStatus`: `{trip_id}` must exist in the index (`404`); `start_date` query optional (default `index.ServiceDate(now)`; malformed → `400`); `trusted := s.trusted.Covers(key, now)`; `riderReported, riders := agg.TripStatus(key, now, coveredFn)`.

- [ ] **Step 1: Write the failing tests** (append to `rider_handlers_test.go`)

```go
// walkPoints returns n on-schedule T1 uploads starting `along` metres in,
// 10 m and 6 s apart, in wire form. It advances env.now past the last point.
func (e *riderTestEnv) walkPoints(along float64, n int) []map[string]any {
	trip, _ := e.index.Trip("T1")
	base := time.Date(2026, 9, 2, 8, 0, 0, 0, e.index.Timezone())
	var out []map[string]any
	var last time.Time
	for i := 0; i < n; i++ {
		a := along + float64(i*10)
		ts := base.Add(time.Duration(a/1001*600) * time.Second)
		p := trip.Shape.PointAt(a)
		out = append(out, map[string]any{"latitude": p.Lat, "longitude": p.Lon, "accuracy": 5, "speed": 1.7, "bearing": 0, "timestamp": ts.Unix()})
		last = ts
	}
	e.now = last.Add(2 * time.Second)
	return out
}

func (e *riderTestEnv) upload(t *testing.T, token, rideID string, pts []map[string]any) (positionsResponse, int) {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/rider/rides/"+rideID+"/positions", token, map[string]any{"positions": pts})
	var resp positionsResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return resp, w.Code
}

func TestPositions_VerifiesAndPersists(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	resp, code := env.upload(t, tok, ride.RideID, env.walkPoints(0, 5))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "verified", resp.State)
	assert.Equal(t, 5, resp.Accepted)
	assert.False(t, resp.Published)
	assert.Equal(t, "unavailable", resp.Corroboration)
	assert.False(t, resp.Ended)
	assert.Len(t, env.store.points[ride.RideID], 5)
	assert.Equal(t, "verified", env.store.rides[ride.RideID].State)
	assert.Equal(t, 5, env.store.rides[ride.RideID].PointsMatched)
}

func TestPositions_CorroborationAndPublish(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	trip, _ := env.index.Trip("T1")
	pts := env.walkPoints(0, 12)
	env.trusted.set(rider.TripKey{"T1", "20260902"}, rider.TrustedVehicle{VehicleID: "bus", Pos: trip.Shape.PointAt(60), Timestamp: env.now})
	resp, code := env.upload(t, tok, ride.RideID, pts)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "corroborated", resp.Corroboration)
	assert.True(t, resp.Published)
	assert.True(t, env.store.rides[ride.RideID].Corroborated)
	assert.Equal(t, 12, env.store.rides[ride.RideID].PointsCorroborated)

	// Trusted feed covers the trip → not in estimates; drop it → estimate appears.
	assert.Empty(t, env.svc.Estimates(env.now))
	env.trusted.clear(rider.TripKey{"T1", "20260902"})
	est := env.svc.Estimates(env.now)
	require.Len(t, est, 1)
	assert.Equal(t, "T1", est[0].Key.TripID)
}

func TestPositions_RejectionEndsRideAndPenalises(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	pts := env.walkPoints(0, 6)
	for i := range pts {
		pts[i]["longitude"] = -122.3200 // ~750 m east of the shape
	}
	resp, code := env.upload(t, tok, ride.RideID, pts)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ended)
	assert.Equal(t, "off_route", resp.EndReason)
	assert.Equal(t, "rejected", resp.State)
	assert.Equal(t, 5, resp.Accepted)
	assert.Equal(t, 1, resp.Ignored)
	assert.Equal(t, "ended", env.store.rides[ride.RideID].Status)
	assert.Equal(t, -1, env.store.riders[riderID].Score)

	_, code = env.upload(t, tok, ride.RideID, env.walkPoints(0, 1))
	assert.Equal(t, http.StatusConflict, code)
}

func TestPositions_Validation(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	_, otherTok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	_, code := env.upload(t, tok, ride.RideID, nil)
	assert.Equal(t, http.StatusBadRequest, code, "empty batch")
	_, code = env.upload(t, tok, ride.RideID, env.walkPoints(0, 13))
	assert.Equal(t, http.StatusBadRequest, code, "batch too large")
	_, code = env.upload(t, tok, ride.RideID, []map[string]any{{"latitude": 1, "longitude": 1}})
	assert.Equal(t, http.StatusBadRequest, code, "missing timestamp")
	_, code = env.upload(t, otherTok, ride.RideID, env.walkPoints(0, 1))
	assert.Equal(t, http.StatusNotFound, code, "someone else's ride")
	_, code = env.upload(t, tok, "not-a-uuid", env.walkPoints(0, 1))
	assert.Equal(t, http.StatusNotFound, code)
	_, code = env.upload(t, tok, uuid.NewString(), env.walkPoints(0, 1))
	assert.Equal(t, http.StatusConflict, code, "unknown ride is treated as ended")

	// Ignored points are not persisted.
	resp, code := env.upload(t, tok, ride.RideID, []map[string]any{{"latitude": 47.6, "longitude": -122.33, "accuracy": 500, "timestamp": env.now.Unix()}})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, resp.Ignored)
	assert.Empty(t, env.store.points[ride.RideID])
}

func TestPositions_RateLimitedPerRider(t *testing.T) {
	env := newRiderTestEnv(t)
	env.svc.batchLimiter = NewKeyedRateLimiter(2*time.Second, 2)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	_, c1 := env.upload(t, tok, ride.RideID, env.walkPoints(0, 1))
	_, c2 := env.upload(t, tok, ride.RideID, env.walkPoints(10, 1))
	_, c3 := env.upload(t, tok, ride.RideID, env.walkPoints(20, 1))
	assert.Equal(t, http.StatusOK, c1)
	assert.Equal(t, http.StatusOK, c2)
	assert.Equal(t, http.StatusTooManyRequests, c3, "burst of 2 per 2 s")
}

func TestPositions_BlockedRiderShadowed(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	env.store.riders[riderID].Tier = "blocked"
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	resp, code := env.upload(t, tok, ride.RideID, env.walkPoints(0, 5))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "verified", resp.State, "verdicts look normal")
	assert.False(t, resp.Published)
	assert.Empty(t, env.store.points[ride.RideID], "nothing persisted for blocked riders")
}

func TestPositions_StoreFailureReturns500(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.store.failNext = assert.AnError
	_, code := env.upload(t, tok, ride.RideID, env.walkPoints(0, 3))
	assert.Equal(t, http.StatusInternalServerError, code)
	resp, code := env.upload(t, tok, ride.RideID, env.walkPoints(30, 1))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "verified", resp.State, "in-memory session advanced despite the failed commit")
	assert.Equal(t, 4, env.store.rides[ride.RideID].PointsTotal, "absolute counters heal the record")
}

func TestTripStatus(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	w := env.do(t, "GET", "/api/v1/rider/trips/T1/status?start_date=20260902", tok, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var st tripStatusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.Equal(t, tripStatusResponse{TripID: "T1", StartDate: "20260902"}, st)

	env.trusted.set(rider.TripKey{"T1", "20260902"}, rider.TrustedVehicle{VehicleID: "bus", Timestamp: env.now})
	w = env.do(t, "GET", "/api/v1/rider/trips/T1/status", tok, nil)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.True(t, st.Trusted)
	assert.Equal(t, "20260902", st.StartDate, "defaults to the service date")

	assert.Equal(t, http.StatusNotFound, env.do(t, "GET", "/api/v1/rider/trips/NOPE/status", tok, nil).Code)
	assert.Equal(t, http.StatusBadRequest, env.do(t, "GET", "/api/v1/rider/trips/T1/status?start_date=x", tok, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, env.do(t, "GET", "/api/v1/rider/trips/T1/status", "", nil).Code)
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./ -run 'TestPositions|TestTripStatus'` — the `501` placeholders fail the assertions.

- [ ] **Step 3: Implement** `handlePositions`, `handleTripStatus`, and `(*riderService).Estimates(now)` (`agg.Estimates(now, func(k) bool { return s.trusted.Configured() && s.trusted.Covers(k, now) })`). Convert `rider.AppliedPoint` → `RidePointRecord` (`Outcome.String()`, `Corroboration.String()`, `int(ScheduleDeviation.Seconds())`, `-1` optionals → nil pointers).

- [ ] **Step 4: Run → PASS.** `go test ./... -race && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider_handlers.go rider_handlers_test.go && git commit -m "feat: rider positions batch verification and trip status endpoints"`

---

### Task 11: feed merge — `buildFeed` with rider estimates and the `source` filter

**Files:**
- Modify: `handlers.go:141-230` (`handleGetFeed`, `buildFeed`), `handlers_test.go`, `handlers_vehicles_test.go` / `feed_validation_test.go` (call-site updates), `main.go:58-99` (`newMux` passes `nil` for now; Task 13 wires the real source)
- Create: `feed_rider_test.go`

**Interfaces:**
- Produces:

```go
type estimateSource interface { Estimates(now time.Time) []rider.TripEstimate }
func buildFeed(vehicles []*VehicleState, estimates []rider.TripEstimate) *gtfs.FeedMessage
func handleGetFeed(tracker *Tracker, estimates estimateSource) http.HandlerFunc // estimates may be nil
```

Rider entity (spec §4.8): `id = "rider:" + TripID + ":" + StartDate`, `vehicle.vehicle.id = "rider:" + TripID`, `vehicle.vehicle.label = "Rider-reported"`, `trip = {trip_id, route_id (omitted when empty), start_date}`, `position = {lat, lon, bearing, speed?}`, `timestamp = Timestamp.Unix()`, `current_stop_sequence` + `stop_id` + `current_status = IN_TRANSIT_TO` when `StopID != ""`. Header timestamp = `max(now, entity timestamps)`.

`source` query: `""`/`all` → both; `driver` → tracker only; `rider` → estimates only; anything else → `400 {"error":"invalid source"}`.

- [ ] **Step 1: Update every existing `buildFeed(` call** to `buildFeed(vehicles, nil)` and every `handleGetFeed(tracker)` to `handleGetFeed(tracker, nil)` (`grep -n 'buildFeed(\|handleGetFeed(' *.go`). Run `go build ./...` to confirm compilation after the signature change (tests will be added next).

- [ ] **Step 2: Write the failing tests** (`feed_rider_test.go`)

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/OneBusAway/vehicle-positions/rider"
)

type staticEstimates []rider.TripEstimate

func (s staticEstimates) Estimates(_ time.Time) []rider.TripEstimate { return s }

func sampleEstimate() rider.TripEstimate {
	speed := 8.5
	return rider.TripEstimate{
		Key: rider.TripKey{TripID: "T1", StartDate: "20260902"}, RouteID: "R1",
		Pos: rider.LatLon{Lat: 47.6045, Lon: -122.33}, Bearing: 12, Speed: &speed,
		Timestamp: time.Now().Add(-5 * time.Second), StopID: "ST2", StopSequence: 2, Riders: 2,
	}
}

func TestBuildFeed_RiderEntities(t *testing.T) {
	driver := &VehicleState{VehicleID: "bus-1", TripID: "T9", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	feed := buildFeed([]*VehicleState{driver}, []rider.TripEstimate{sampleEstimate()})
	require.Len(t, feed.Entity, 2)
	e := feed.Entity[1]
	assert.Equal(t, "rider:T1:20260902", e.GetId())
	vp := e.GetVehicle()
	assert.Equal(t, "rider:T1", vp.GetVehicle().GetId())
	assert.Equal(t, "Rider-reported", vp.GetVehicle().GetLabel())
	assert.Equal(t, "T1", vp.GetTrip().GetTripId())
	assert.Equal(t, "R1", vp.GetTrip().GetRouteId())
	assert.Equal(t, "20260902", vp.GetTrip().GetStartDate())
	assert.InDelta(t, 47.6045, vp.GetPosition().GetLatitude(), 0.0001)
	assert.InDelta(t, 12, vp.GetPosition().GetBearing(), 0.01)
	assert.InDelta(t, 8.5, vp.GetPosition().GetSpeed(), 0.01)
	assert.Equal(t, uint32(2), vp.GetCurrentStopSequence())
	assert.Equal(t, "ST2", vp.GetStopId())
	assert.Equal(t, gtfs.VehiclePosition_IN_TRANSIT_TO, vp.GetCurrentStatus())
	assert.Empty(t, validateFeedCompliance(t, feed))
}

func TestBuildFeed_RiderEntity_NoSpeedNoStop(t *testing.T) {
	est := sampleEstimate()
	est.Speed, est.StopID, est.StopSequence, est.RouteID = nil, "", 0, ""
	feed := buildFeed(nil, []rider.TripEstimate{est})
	vp := feed.Entity[0].GetVehicle()
	assert.Nil(t, vp.GetPosition().Speed)
	assert.Nil(t, vp.StopId)
	assert.Nil(t, vp.CurrentStopSequence)
	assert.Nil(t, vp.CurrentStatus)
	assert.Nil(t, vp.GetTrip().RouteId)
	assert.Empty(t, validateFeedCompliance(t, feed))
}

func TestHandleGetFeed_SourceFilter(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})
	h := handleGetFeed(tracker, staticEstimates{sampleEstimate()})

	count := func(source string) (int, int) {
		url := "/gtfs-rt/vehicle-positions?format=json"
		if source != "" {
			url += "&source=" + source
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != http.StatusOK {
			return -1, w.Code
		}
		var feed gtfs.FeedMessage
		require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &feed))
		return len(feed.Entity), w.Code
	}
	n, _ := count("")
	assert.Equal(t, 2, n)
	n, _ = count("all")
	assert.Equal(t, 2, n)
	n, _ = count("driver")
	assert.Equal(t, 1, n)
	n, _ = count("rider")
	assert.Equal(t, 1, n)
	_, code := count("bogus")
	assert.Equal(t, http.StatusBadRequest, code)

	// nil estimate source behaves like the old feed.
	w := httptest.NewRecorder()
	handleGetFeed(tracker, nil).ServeHTTP(w, httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions?source=rider&format=json", nil))
	var feed gtfs.FeedMessage
	require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &feed))
	assert.Empty(t, feed.Entity)
}

func TestBuildFeed_HeaderTimestampCoversRiderEntities(t *testing.T) {
	est := sampleEstimate()
	est.Timestamp = time.Now().Add(30 * time.Second) // aggregator clamps to now; buildFeed must still satisfy E012
	feed := buildFeed(nil, []rider.TripEstimate{est})
	assert.GreaterOrEqual(t, feed.Header.GetTimestamp(), feed.Entity[0].GetVehicle().GetTimestamp())
}
```

- [ ] **Step 3: Run → FAIL.** `go test ./ -run 'TestBuildFeed_Rider|TestHandleGetFeed_SourceFilter|TestBuildFeed_HeaderTimestamp'`.

- [ ] **Step 4: Implement** the new `buildFeed` and `handleGetFeed`. Guard against a typed-nil interface: in `handleGetFeed`, `if estimates != nil { ests = estimates.Estimates(time.Now()) }`.

- [ ] **Step 5: Run → PASS.** `go test ./... && go vet ./...`

- [ ] **Step 6: Commit** — `git add handlers.go feed_rider_test.go handlers_test.go feed_validation_test.go handlers_vehicles_test.go main.go && git commit -m "feat: merge rider estimates into the GTFS-RT feed with a source filter"`

---

### Task 12: admin endpoints — rider status and rides list

**Files:**
- Create: `rider_admin_handlers.go`, `rider_admin_handlers_test.go`

**Interfaces:**
- Consumes: `riderService` (may be nil), `RideLister`, `RiderStatsReader`, `rider.IndexStats`, `rider.FeedHealth`.
- Produces:

```go
type riderStatusProvider interface {
    RiderStatus(ctx context.Context, now time.Time) (riderStatusResponse, error) // implemented by *riderService
}
type riderStatusResponse struct {
    Enabled      bool                 `json:"enabled"`
    GTFS         *riderGTFSStatus     `json:"gtfs,omitempty"`
    TrustedFeeds []riderFeedStatus    `json:"trusted_feeds,omitempty"`
    Riders       *riderTierCounts     `json:"riders,omitempty"`
    Rides        *riderRideCounts     `json:"rides,omitempty"`
}
type riderGTFSStatus struct { Source string `json:"source"`; LoadedAt string `json:"loaded_at"`; Trips int `json:"trips"`; Shapes int `json:"shapes"`; Timezone string `json:"timezone"` }
type riderFeedStatus struct { URL string `json:"url"`; LastSuccess string `json:"last_success"`; LastError string `json:"last_error"`; Entities int `json:"entities"` }
type riderTierCounts struct { Total int `json:"total"`; Trusted int `json:"trusted"`; Blocked int `json:"blocked"` }
type riderRideCounts struct { Active int `json:"active"`; Publishable int `json:"publishable"` }
type adminRideEntry struct { ID string `json:"id"`; RiderID string `json:"rider_id"`; TripID string `json:"trip_id"`; StartDate string `json:"start_date"`; RouteID string `json:"route_id"`; State string `json:"state"`; Corroborated bool `json:"corroborated"`; Status string `json:"status"`; EndReason string `json:"end_reason"`; PointsTotal int `json:"points_total"`; PointsMatched int `json:"points_matched"`; PointsCorroborated int `json:"points_corroborated"`; StartedAt string `json:"started_at"`; EndedAt *string `json:"ended_at"` }
type adminRidesResponse struct { Count int `json:"count"`; HasMore bool `json:"has_more"`; Rides []adminRideEntry `json:"rides"` }

func handleRiderAdminStatus(p riderStatusProvider) http.HandlerFunc  // p == nil → {"enabled":false}
func handleRiderAdminRides(store RideLister) http.HandlerFunc          // status default active; limit 1–200 default 50; offset ≥ 0; fetch limit+1 for has_more
```

Timestamps are RFC3339 UTC strings; zero `LastSuccess` → `""`.

- [ ] **Step 1: Write the failing tests**

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestRiderAdminStatus_Disabled(t *testing.T) {
	w := httptest.NewRecorder()
	handleRiderAdminStatus(nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"enabled":false}`, w.Body.String())
}

func TestRiderAdminStatus_Enabled(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	env.store.riders[riderID].Tier = "trusted"
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.upload(t, tok, ride.RideID, env.walkPoints(0, 3))
	env.trusted.health = []rider.FeedHealth{{URL: "http://feed", LastSuccess: time.Now(), Entities: 3}}

	w := httptest.NewRecorder()
	handleRiderAdminStatus(env.svc).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var st riderStatusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.True(t, st.Enabled)
	require.NotNil(t, st.GTFS)
	assert.Equal(t, 3, st.GTFS.Trips)
	assert.Equal(t, "America/Los_Angeles", st.GTFS.Timezone)
	require.Len(t, st.TrustedFeeds, 1)
	assert.Equal(t, 3, st.TrustedFeeds[0].Entities)
	assert.NotEmpty(t, st.TrustedFeeds[0].LastSuccess)
	require.NotNil(t, st.Riders)
	assert.Equal(t, 1, st.Riders.Total)
	assert.Equal(t, 1, st.Riders.Trusted)
	require.NotNil(t, st.Rides)
	assert.Equal(t, 1, st.Rides.Active)
	assert.Equal(t, 1, st.Rides.Publishable, "trusted rider, verified, fresh, not covered")
}

func TestRiderAdminRides(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	for i := 0; i < 3; i++ {
		r := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
		if i < 2 {
			env.do(t, "POST", "/api/v1/rider/rides/"+r.RideID+"/end", tok, map[string]any{"reason": "arrived"})
		}
	}
	h := handleRiderAdminRides(env.store)
	get := func(q string) adminRidesResponse {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/rides"+q, nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp adminRidesResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		return resp
	}
	active := get("")
	assert.Equal(t, 1, active.Count)
	assert.Equal(t, "active", active.Rides[0].Status)
	assert.Nil(t, active.Rides[0].EndedAt)
	ended := get("?status=ended&limit=1")
	assert.Equal(t, 1, ended.Count)
	assert.True(t, ended.HasMore)
	assert.NotNil(t, ended.Rides[0].EndedAt)
	assert.Equal(t, "arrived", ended.Rides[0].EndReason)
	page2 := get("?status=ended&limit=1&offset=1")
	assert.False(t, page2.HasMore)

	for _, q := range []string{"?status=weird", "?limit=0", "?limit=201", "?offset=-1"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/rider/rides"+q, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, q)
	}
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./ -run TestRiderAdmin`.

- [ ] **Step 3: Implement** `rider_admin_handlers.go` and `(*riderService).RiderStatus` in `rider_handlers.go` (index stats from `s.index()`, `trusted.Health()`, `store.CountRidersByTier` → total/trusted/blocked, `agg.ActiveCount()`, `agg.PublishableCount(now, covered)`).

- [ ] **Step 4: Run → PASS.** `go test ./... && go vet ./...`

- [ ] **Step 5: Commit** — `git add rider_admin_handlers.go rider_admin_handlers_test.go rider_handlers.go && git commit -m "feat: admin rider status and rides endpoints"`

---

### Task 13: `rider_wiring.go` + `main.go` + route wiring + docs

**Files:**
- Create: `rider_wiring.go`, `rider_wiring_test.go`
- Modify: `main.go` (`appStore`, `newMux`, `newHandler`, `main`), `route_wiring_test.go` (`noopStore` + new tests), `handler_composition_test.go` and every other `newMux(`/`newHandler(` call site, `docker-compose.yml`, `README.md`, `ARCHITECTURE.md`, `docs/development.md`

**Interfaces:**
- Produces:

```go
type riderConfig struct {
    Enabled        bool
    GTFSSource     string
    GTFSRefresh    time.Duration
    TrustedURLs    []string
    TrustedPoll    time.Duration
    TrustedMaxAge  time.Duration
    JWTTTL         time.Duration
    PointRetention time.Duration
    Thresholds     rider.Thresholds
}
func riderConfigFromEnv() (riderConfig, error) // error only when Enabled && GTFSSource == ""; RIDER_MAX_SHAPE_DISTANCE / RIDER_MAX_SPEED parsed with strconv.ParseFloat (invalid → default + log), durations via envDurationOrDefault

type riderRuntime struct {
    cfg       riderConfig
    refresher *rider.Refresher
    trusted   *rider.TrustedFeed
    svc       *riderService
    cancel    context.CancelFunc
}
func newRiderRuntime(ctx context.Context, cfg riderConfig, store riderStore, jwtSecret []byte, trustProxy bool) (*riderRuntime, error)
// load index (LoadIndex; error aborts), EndAllActiveRides("server_restart"), NewTrustedFeed, NewAggregator(cfg.Thresholds, index.Timezone()),
// newRiderService, then Start(ctx): refresher.Start(every GTFSRefresh), trusted.Start(every TrustedPoll), reap ticker (30 s → agg.Reap + finishRide persistence),
// retention ticker (1 h → store.DeleteRidePointsBefore).
func (rt *riderRuntime) Stop()

// main.go
func newMux(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter, jwtSecret []byte, startTime time.Time,
    loginLimiter *LoginRateLimiter, trustProxy bool, riderSvc *riderService) *http.ServeMux
func newHandler(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter, loginLimiter *LoginRateLimiter,
    jwtSecret []byte, startTime time.Time, cfg adminUIConfig, riderSvc *riderService) (http.Handler, error)
```

In `newMux`: `handleGetFeed(tracker, estimatesOrNil(riderSvc))` where `estimatesOrNil` returns a nil `estimateSource` when `riderSvc == nil`; `if riderSvc != nil { registerRiderRoutes(mux, riderSvc) }`; admin routes always: `GET /api/v1/admin/rider/status` → `authMiddleware(adminMiddleware(handleRiderAdminStatus(statusOrNil(riderSvc))))`, `GET /api/v1/admin/rider/rides` → `authMiddleware(adminMiddleware(handleRiderAdminRides(store)))`. `appStore` gains `RiderRegistrar, RiderReader, RideStarter, RidePointRecorder, RideFinisher, RideLister, RiderStatsReader, RidePointPruner`; `noopStore` gains the matching no-op methods. `riderStore` (Task 9) must also embed `RidePointPruner` for the retention ticker — add it there.

In `main`: after `loginLimiter`, `riderCfg, err := riderConfigFromEnv()` (exit 1 on error); `var riderSvc *riderService; if riderCfg.Enabled { rt, err := newRiderRuntime(ctx, riderCfg, store, jwtSecret, trustProxyHeaders()); exit 1 on error; defer rt.Stop(); riderSvc = rt.svc }`.

- [ ] **Step 1: Write the failing tests** (`rider_wiring_test.go` + additions to `route_wiring_test.go`)

```go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestRiderConfigFromEnv(t *testing.T) {
	t.Setenv("RIDER_MODE_ENABLED", "false")
	cfg, err := riderConfigFromEnv()
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)

	t.Setenv("RIDER_MODE_ENABLED", "true")
	_, err = riderConfigFromEnv()
	assert.Error(t, err, "GTFS_STATIC_URL required when enabled")

	t.Setenv("GTFS_STATIC_URL", "rider/testdata/fixture.zip")
	t.Setenv("TRUSTED_GTFS_RT_URLS", "http://a/vp.pb, http://b/vp.pb")
	t.Setenv("RIDER_MAX_SHAPE_DISTANCE", "80")
	t.Setenv("RIDER_MAX_SPEED", "garbage")
	t.Setenv("RIDER_SCHEDULE_LATE", "2h")
	cfg, err = riderConfigFromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, []string{"http://a/vp.pb", "http://b/vp.pb"}, cfg.TrustedURLs)
	assert.Equal(t, 80.0, cfg.Thresholds.MaxShapeDistance)
	assert.Equal(t, 35.0, cfg.Thresholds.MaxSpeed, "invalid float falls back to the default")
	assert.Equal(t, 2*time.Hour, cfg.Thresholds.ScheduleLate)
	assert.Equal(t, 24*time.Hour, cfg.GTFSRefresh)
	assert.Equal(t, 30*time.Second, cfg.TrustedPoll)
	assert.Equal(t, 5*time.Minute, cfg.TrustedMaxAge)
	assert.Equal(t, 8760*time.Hour, cfg.JWTTTL)
	assert.Equal(t, 168*time.Hour, cfg.PointRetention)
}

func TestNewRiderRuntime_LoadsIndexAndEndsStaleRides(t *testing.T) {
	store := newFakeRiderStore()
	r, _, _ := store.RegisterRider(context.Background(), "inst", "ios", "x", "1")
	require.NoError(t, store.StartRide(context.Background(), &Ride{ID: "stale", RiderID: r.ID, TripID: "T1", StartDate: "20260902"}))

	cfg := riderConfig{Enabled: true, GTFSSource: "rider/testdata/fixture.zip", GTFSRefresh: time.Hour, TrustedPoll: time.Hour,
		TrustedMaxAge: 5 * time.Minute, JWTTTL: time.Hour, PointRetention: time.Hour, Thresholds: rider.DefaultThresholds()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := newRiderRuntime(ctx, cfg, store, testSecret, false)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, "ended", store.rides["stale"].Status)
	assert.Equal(t, "server_restart", store.rides["stale"].EndReason)
	assert.Equal(t, 3, rt.refresher.Current().Stats().Trips)
	assert.False(t, rt.trusted.Configured())

	cfg.GTFSSource = "does/not/exist.zip"
	_, err = newRiderRuntime(ctx, cfg, store, testSecret, false)
	assert.Error(t, err)
}

func TestRiderRoutes_NotRegisteredWhenDisabled(t *testing.T) {
	mux := newMux(&noopStore{}, nil, nil, testSecret, time.Time{}, nil, false, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/rider/register", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	adminTok, _ := generateJWT(&User{ID: 1, Email: "a@test.com", Role: "admin"}, testSecret)
	req := httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"enabled":false}`, w.Body.String())
}

func TestRiderRoutes_RoleIsolation(t *testing.T) {
	env := newRiderTestEnv(t)
	mux := newMux(&noopStore{}, NewTracker(time.Minute), nil, testSecret, time.Time{}, nil, false, env.svc)
	_, riderTok := env.register(t)
	driverTok, _ := generateJWT(&User{ID: 1, Email: "d@test.com", Role: "driver"}, testSecret)
	adminTok, _ := generateJWT(&User{ID: 2, Email: "a@test.com", Role: "admin"}, testSecret)

	call := func(method, path, tok string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}
	assert.Equal(t, http.StatusForbidden, call("POST", "/api/v1/locations", riderTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/admin/status", riderTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/admin/rider/status", riderTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/rider/trips/T1/status", driverTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/rider/trips/T1/status", adminTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/admin/rider/rides", driverTok))
	assert.Equal(t, http.StatusOK, call("GET", "/api/v1/admin/rider/rides", adminTok))
	assert.Equal(t, http.StatusOK, call("GET", "/api/v1/rider/trips/T1/status", riderTok))
}

func TestMain_GTFSFixtureExists(t *testing.T) {
	_, err := os.Stat("rider/testdata/fixture.zip")
	require.NoError(t, err)
}
```

Also add `{"GET", "/api/v1/admin/rider/status"}` and `{"GET", "/api/v1/admin/rider/rides"}` to the route tables in `TestAdminRoutes_DriverTokenRejected` and `TestAdminRoutes_AdminTokenAllowed` in `route_wiring_test.go`.

- [ ] **Step 2: Run → FAIL.** `go test ./ -run 'TestRiderConfigFromEnv|TestNewRiderRuntime|TestRiderRoutes|TestAdminRoutes'` fails to compile (signatures).

- [ ] **Step 3: Implement** `rider_wiring.go`, update `main.go` and every call site, extend `noopStore`. The reap ticker: `for _, s := range agg.Reap(now) { if _, err := svc.finishRide(ctx, s, s.EndReason()); err != nil { slog.Warn(...) } }` — note `finishRide` must tolerate an already-ended session (skip `End`, still persist, then `Remove`).

- [ ] **Step 4: Docs and compose.** Add to `docker-compose.yml` server environment: `JWT_SECRET: "change-me-change-me-change-me-32b"`, `RIDER_MODE_ENABLED: "false"`, `GTFS_STATIC_URL: ""`, `TRUSTED_GTFS_RT_URLS: ""` (commented examples). README: new `### Rider mode (crowdsourced positions)` under Getting Started explaining enablement, env vars table (copy from spec §4.1), the API summary table (spec §4.4), and a pointer to the SDK at `ios/VehiclePositionsKit`. ARCHITECTURE.md: new `## 8. Rider Mode` section (components, data flow, trust states, feed merge; ~60 lines) and add the three tables to §3.3 with a note. `docs/development.md`: a `## Rider Mode Smoke Test` section (the commands from Task 14 Step 5).

- [ ] **Step 5: Run → PASS.** `go test ./... -race && go vet ./...`

- [ ] **Step 6: Commit** — `git add -A && git commit -m "feat: wire rider mode runtime, routes, config and docs"`

---

### Task 14: `cmd/ridersim` — rider simulator, Makefile targets, end-to-end smoke

**Files:**
- Create: `cmd/ridersim/main.go`, `cmd/ridersim/main_test.go`
- Modify: `Makefile` (`ridersim` target, help line), `docs/development.md` (smoke section from Task 13 Step 4 references these commands)

**Interfaces:**
- Consumes: `rider.LoadStatic`, `rider.BuildIndex`, `rider.Index.TripIDs/Trip/ActiveOn/ServiceDate`, `rider.ShapeGeom.PointAt/BearingAt`; the rider HTTP API (Task 9–10 wire formats — the simulator defines its own request/response structs, it does **not** import `package main`).
- Produces: `go run ./cmd/ridersim -url http://localhost:8080 -gtfs rider/testdata/fixture.zip -trip T1 -start-date 20260902 -interval 1s -speed 20 -expect-end arrived`. Exit code 0 when every ride ended with the expected reason, 1 otherwise. Flags per spec §4.11 (`-url`, `-gtfs`, `-trip` repeatable via a `flag.Func` appending to a slice, `-random N`, `-start-date`, `-interval`, `-speed`, `-noise`, `-offroute-after`, `-riders-per-trip`, `-duration`, `-expect-end`).

Simulation loop per rider: register (fresh UUID) → start ride → every `interval`, advance `along += speed × interval`, sample `pos = shape.PointAt(along)` plus Gaussian noise of `noise` metres (`rand.NormFloat64()` scaled: `Δlat = n/111_320`, `Δlon = n/(111_320·cos lat)`); after `offroute-after` elapsed (if > 0) add a fixed 300 m eastward offset; buffer points and POST a batch when `max_batch_size` is reached or `interval × max_batch_size` … simpler: POST every `report_interval_seconds` from the start-ride response, at most `max_batch_size` points per POST, with the timestamps set to the simulated wall clock (real `time.Now()` — the simulator runs in real time so server skew checks pass; `-speed` therefore controls how fast the shape is consumed). Print each response's state/corroboration when it changes; stop when `along ≥ shape.Length` (POST end with reason `arrived`) or when the server reports `ended`. **Timing caveat for the fixture**: T1 is scheduled 08:00–08:10; the schedule window (−15 / +90 min) means the smoke test must run within that window or pass `-start-date` for a service date whose trip window includes the current local time. To make the smoke test runnable at any time, the simulator accepts `-schedule-offset` (duration, default 0) which is **not** sent to the server; instead the **server** is started with `RIDER_SCHEDULE_EARLY=24h RIDER_SCHEDULE_LATE=24h` during the smoke (documented in development.md). Keep the simulator free of clock tricks.

- [ ] **Step 1: Write the failing test** (`cmd/ridersim/main_test.go`) — a unit test for the pure helpers `jitter(pos rider.LatLon, metres float64, rng *rand.Rand) rider.LatLon` (result within `4×metres` of the input over 1000 samples, mean offset < `metres`) and `offsetEast(pos rider.LatLon, metres float64) rider.LatLon` (`rider.Distance(pos, offsetEast(pos, 300))` within 1 m of 300), plus `pickTrips(ix *rider.Index, requested []string, random int, serviceDate string) ([]string, error)` (explicit ids validated against the index and `ActiveOn`; `random` picks that many active trips deterministically-seeded; error when none active).

```go
package main

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func fixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	b, err := os.ReadFile("../../rider/testdata/fixture.zip")
	require.NoError(t, err)
	static, err := rider.ParseStaticBytes(b)
	require.NoError(t, err)
	ix, err := rider.BuildIndex(static, "fixture", time.Now())
	require.NoError(t, err)
	return ix
}

func TestJitterAndOffset(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	base := rider.LatLon{Lat: 47.6, Lon: -122.33}
	var sum float64
	for i := 0; i < 1000; i++ {
		d := rider.Distance(base, jitter(base, 8, rng))
		assert.Less(t, d, 40.0)
		sum += d
	}
	assert.Less(t, sum/1000, 12.0)
	assert.InDelta(t, 300, rider.Distance(base, offsetEast(base, 300)), 1)
}

func TestPickTrips(t *testing.T) {
	ix := fixtureIndex(t)
	got, err := pickTrips(ix, []string{"T1"}, 0, "20260902")
	require.NoError(t, err)
	assert.Equal(t, []string{"T1"}, got)
	_, err = pickTrips(ix, []string{"T2"}, 0, "20260902")
	assert.Error(t, err, "Saturday-only trip on a Wednesday")
	_, err = pickTrips(ix, []string{"NOPE"}, 0, "20260902")
	assert.Error(t, err)
	got, err = pickTrips(ix, nil, 2, "20260902")
	require.NoError(t, err)
	assert.Len(t, got, 2)
	_, err = pickTrips(ix, nil, 1, "20271231")
	assert.Error(t, err, "outside calendar range: nothing active")
}
```

- [ ] **Step 2: Run → FAIL.** `go test ./cmd/ridersim/`.

- [ ] **Step 3: Implement** `cmd/ridersim/main.go` (flags, helpers, `runRider(ctx, cfg, tripID) (endReason string, err error)`, `main` running riders concurrently with a `sync.WaitGroup`, summary and exit code). Add Makefile target:

```make
ridersim:
	go run ./cmd/ridersim -url http://localhost:8080 -gtfs rider/testdata/fixture.zip -trip T1 -interval 1s -speed 20 -expect-end arrived
```
and a `make help` line.

- [ ] **Step 4: Run → PASS.** `go test ./... && go vet ./...`

- [ ] **Step 5: End-to-end smoke** (record the exact output in the commit message body or a scratch note; this is what `/go` re-runs). In one terminal:

```bash
docker compose up -d db
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable'
export JWT_SECRET='change-me-change-me-change-me-32b'
export ADMIN_BOOTSTRAP_EMAIL=admin@test.com ADMIN_BOOTSTRAP_PASSWORD=password123
export RIDER_MODE_ENABLED=true GTFS_STATIC_URL=rider/testdata/fixture.zip
export RIDER_SCHEDULE_EARLY=24h RIDER_SCHEDULE_LATE=24h TRUSTED_FEED_POLL=2s
export TRUSTED_GTFS_RT_URLS='http://localhost:8080/gtfs-rt/vehicle-positions?source=driver'
go run . 
```
In another:
```bash
# 1. a "trusted" driver on T1 at 300 m along the shape (47.6027,-122.33), reporting every 3 s
TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' -d '{"email":"admin@test.com","password":"password123"}' | jq -r .token)
( for i in $(seq 1 20); do curl -s -X POST localhost:8080/api/v1/locations -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
   -d "{\"vehicle_id\":\"bus-1\",\"trip_id\":\"T1\",\"latitude\":47.6027,\"longitude\":-122.33,\"timestamp\":$(date +%s)}" >/dev/null; sleep 3; done ) &
# 2. a rider walking T1 slowly enough to stay near 300 m for the first ~30 s
go run ./cmd/ridersim -url http://localhost:8080 -gtfs rider/testdata/fixture.zip -trip T1 -interval 1s -speed 10 -expect-end arrived
# expect: state pending→verified, corroboration corroborated, published true within ~15 s
# 3. while the driver loop runs: rider entity suppressed
curl -s 'localhost:8080/gtfs-rt/vehicle-positions?format=json&source=rider' | jq '.entity | length'   # 0
# 4. after the driver loop ends (60 s) + TRUSTED_FEED_MAX_AGE... for the smoke set STALENESS_THRESHOLD=30s on the server so bus-1 drops out quickly, then:
curl -s 'localhost:8080/gtfs-rt/vehicle-positions?format=json&source=rider' | jq '.entity[0].vehicle.vehicle'   # {"id":"rider:T1","label":"Rider-reported"}
# 5. off-route rider is rejected
go run ./cmd/ridersim -url http://localhost:8080 -gtfs rider/testdata/fixture.zip -trip T1 -interval 1s -speed 10 -offroute-after 5s -expect-end off_route
# 6. admin status
curl -s localhost:8080/api/v1/admin/rider/status -H "Authorization: Bearer $TOKEN" | jq .
```
Note for step 4: `TRUSTED_FEED_MAX_AGE` defaults to 5 m but the trusted feed here is the server's own driver feed, which drops `bus-1` after `STALENESS_THRESHOLD`; set `STALENESS_THRESHOLD=30s` and `TRUSTED_FEED_MAX_AGE=30s` on the server for the smoke. Write these commands into `docs/development.md` (Task 13 Step 4 reserved the section).

- [ ] **Step 6: Commit** — `git add cmd/ridersim Makefile docs/development.md && git commit -m "feat: rider simulator and end-to-end smoke"`

---

### Task 15: `VehiclePositionsKit` package scaffold, public models, wire types and codec

**Files:**
- Create: `ios/VehiclePositionsKit/Package.swift`, `ios/VehiclePositionsKit/.gitignore` (`.build/`, `.swiftpm/xcode/xcuserdata/`, `DerivedData/`), `ios/VehiclePositionsKit/Sources/VehiclePositionsKit/Models/PublicTypes.swift`, `ios/VehiclePositionsKit/Sources/VehiclePositionsKit/API/WireTypes.swift`, `ios/VehiclePositionsKit/Tests/VehiclePositionsKitTests/WireTypesTests.swift`

**Interfaces:**
- Produces (`PublicTypes.swift`; everything `public`, `Sendable`):

```swift
public struct Coordinate: Sendable, Codable, Equatable, Hashable { public var latitude: Double; public var longitude: Double; public init(latitude:longitude:) }

public struct RideReporterConfiguration: Sendable {
    public var serverURL: URL
    public var appID: String
    public var appVersion: String
    public var maxRideDuration: Duration = .seconds(3 * 3600)
    public var stationaryTimeout: Duration = .seconds(10 * 60)
    public var arrivalRadiusMeters: Double = 75
    public var minimumTravelBeforeArrivalMeters: Double = 200
    public var uploadFailureTimeout: Duration = .seconds(5 * 60)
    public var sampleRetention: Duration = .seconds(10 * 60)
    public init(serverURL: URL, appID: String, appVersion: String)
}
public struct TripDescriptor: Sendable, Codable, Equatable {
    public var tripID: String; public var startDate: String?; public var routeID: String?
    public var vehicleID: String?; public var boardingStopID: String?; public var destinationStopID: String?
    public init(tripID: String, startDate: String? = nil, routeID: String? = nil, vehicleID: String? = nil, boardingStopID: String? = nil, destinationStopID: String? = nil)
}
public enum RideState: String, Sendable, Codable { case pending, verified, rejected }
public enum Corroboration: String, Sendable, Codable { case unavailable, none, corroborated, contradicted }
public enum RideEndReason: String, Sendable, Codable, CaseIterable {
    case userRequested = "user_requested", arrived, stationary, maxDuration = "max_duration"
    case locationUnavailable = "location_unavailable", authorizationDenied = "authorization_denied"
    case networkFailure = "network_failure", appTerminated = "app_terminated"
    case offRoute = "off_route", contradicted, implausible, offSchedule = "off_schedule"
    case superseded, serverRestart = "server_restart", idle
    /// Reasons the server accepts from a client (spec §4.4).
    public var isClientReportable: Bool
}
public struct RideProgress: Sendable, Equatable {
    public var state: RideState; public var published: Bool; public var corroboration: Corroboration
    public var pointsAccepted: Int; public var offRouteStreak: Int
}
public struct RideSummary: Sendable, Codable, Equatable { public var points: Int; public var matched: Int; public var corroborated: Int; public var durationSeconds: Int }
public struct TripStatus: Sendable, Codable, Equatable { public var tripID: String; public var startDate: String; public var trusted: Bool; public var riderReported: Bool; public var riders: Int }
public enum RideWarning: Sendable, Equatable { case uploadRetrying(attempt: Int), accuracyLimited, insufficientlyInUse }
public enum RideEvent: Sendable, Equatable {
    case registered(riderID: String)
    case started(rideID: String)
    case progress(RideProgress)
    case warning(RideWarning)
    case ended(RideEndReason, summary: RideSummary?)
}
public enum RideError: Error, Sendable, Equatable {
    case notAuthorized
    case server(status: Int, message: String)
    case transport(String)
    case alreadyEnded
    case decoding(String)
    case notActive
}
```

- Produces (`WireTypes.swift`; `public` so host apps can build their own clients, snake_case keys via explicit `CodingKeys`):

```swift
public struct RegisterRequest: Codable, Sendable, Equatable { installationID, platform, appID, appVersion: String; attestation: String? /* always nil in v1; encoded as null */ }
public struct RegisterResponse: Codable, Sendable, Equatable { riderID, token: String; reportIntervalSeconds, maxBatchSize: Int }
public struct StartRideRequest: Codable, Sendable, Equatable { tripID: String; startDate, routeID, vehicleID, boardingStopID, destinationStopID: String? /* nil keys omitted */ ; init(_ trip: TripDescriptor) }
public struct DestinationInfo: Codable, Sendable, Equatable { stopID: String; latitude, longitude: Double; var coordinate: Coordinate }
public struct StartRideResponse: Codable, Sendable, Equatable { rideID: String; state: RideState; reportIntervalSeconds, maxBatchSize: Int; destination: DestinationInfo? }
public struct PositionUpload: Codable, Sendable, Equatable { latitude, longitude: Double; accuracy, speed, bearing: Double? /* nil omitted */; timestamp: Int }
public struct PositionsRequest: Codable, Sendable, Equatable { positions: [PositionUpload] }
public struct PositionsResponse: Codable, Sendable, Equatable { state: RideState; published: Bool; corroboration: Corroboration; accepted, ignored, offRouteStreak: Int; ended: Bool; endReason: String /* "" when none */; var endReasonValue: RideEndReason? }
public struct EndRideRequest: Codable, Sendable, Equatable { reason: RideEndReason }
public struct EndRideResponse: Codable, Sendable, Equatable { status: String; summary: RideSummary }
public struct ServerErrorBody: Codable, Sendable { error: String }
public enum RiderAPICodec {
    public static func encode<T: Encodable>(_ value: T) throws -> Data   // sortedKeys for stable tests
    public static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T
}
```
`RideSummary`'s wire keys are `points`, `matched`, `corroborated`, `duration_seconds`; `TripStatus`'s are `trip_id`, `start_date`, `trusted`, `rider_reported`, `riders`.

- [ ] **Step 1: Package manifest**

```swift
// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "VehiclePositionsKit",
    platforms: [.iOS(.v18)],
    products: [.library(name: "VehiclePositionsKit", targets: ["VehiclePositionsKit"])],
    targets: [
        .target(name: "VehiclePositionsKit"),
        .testTarget(name: "VehiclePositionsKitTests", dependencies: ["VehiclePositionsKit"]),
    ],
    swiftLanguageModes: [.v6]
)
```

- [ ] **Step 2: Write the failing tests** (`WireTypesTests.swift`)

```swift
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct WireTypesTests {
    private func json(_ data: Data) throws -> [String: Any] {
        try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    @Test func registerRequestUsesSnakeCaseAndNullAttestation() throws {
        let req = RegisterRequest(installationID: "7d1d", platform: "ios", appID: "org.onebusaway.iphone", appVersion: "26.4.0", attestation: nil)
        let obj = try json(RiderAPICodec.encode(req))
        #expect(obj["installation_id"] as? String == "7d1d")
        #expect(obj["app_id"] as? String == "org.onebusaway.iphone")
        #expect(obj["app_version"] as? String == "26.4.0")
        #expect(obj["platform"] as? String == "ios")
        #expect(obj.keys.contains("attestation"), "attestation must be sent as explicit null")
        #expect(obj["attestation"] is NSNull)
    }

    @Test func registerResponseDecodes() throws {
        let data = Data(#"{"rider_id":"a3f0","token":"jwt","report_interval_seconds":5,"max_batch_size":12}"#.utf8)
        let resp = try RiderAPICodec.decode(RegisterResponse.self, from: data)
        #expect(resp == RegisterResponse(riderID: "a3f0", token: "jwt", reportIntervalSeconds: 5, maxBatchSize: 12))
    }

    @Test func startRideRequestOmitsNilKeys() throws {
        let trip = TripDescriptor(tripID: "1_604321", startDate: "20260902", destinationStopID: "1_75414")
        let obj = try json(RiderAPICodec.encode(StartRideRequest(trip)))
        #expect(obj["trip_id"] as? String == "1_604321")
        #expect(obj["start_date"] as? String == "20260902")
        #expect(obj["destination_stop_id"] as? String == "1_75414")
        #expect(obj["route_id"] == nil)
        #expect(obj["vehicle_id"] == nil)
        #expect(obj["boarding_stop_id"] == nil)
    }

    @Test func startRideResponseDecodesWithAndWithoutDestination() throws {
        let with = Data(#"{"ride_id":"c9e2","state":"pending","report_interval_seconds":5,"max_batch_size":12,"destination":{"stop_id":"1_75414","latitude":47.61,"longitude":-122.33}}"#.utf8)
        let r = try RiderAPICodec.decode(StartRideResponse.self, from: with)
        #expect(r.rideID == "c9e2")
        #expect(r.state == .pending)
        #expect(r.destination?.coordinate == Coordinate(latitude: 47.61, longitude: -122.33))
        let without = Data(#"{"ride_id":"c9e2","state":"pending","report_interval_seconds":5,"max_batch_size":12}"#.utf8)
        #expect(try RiderAPICodec.decode(StartRideResponse.self, from: without).destination == nil)
    }

    @Test func positionsRequestOmitsAbsentOptionals() throws {
        let req = PositionsRequest(positions: [
            PositionUpload(latitude: 47.60, longitude: -122.33, accuracy: 8, speed: nil, bearing: 184, timestamp: 1_756_800_000),
        ])
        let obj = try json(RiderAPICodec.encode(req))
        let positions = try #require(obj["positions"] as? [[String: Any]])
        #expect(positions.count == 1)
        #expect(positions[0]["accuracy"] as? Double == 8)
        #expect(positions[0]["speed"] == nil)
        #expect(positions[0]["bearing"] as? Double == 184)
        #expect(positions[0]["timestamp"] as? Int == 1_756_800_000)
    }

    @Test func positionsResponseDecodes() throws {
        let data = Data(#"{"state":"verified","published":true,"corroboration":"corroborated","accepted":3,"ignored":0,"off_route_streak":0,"ended":false,"end_reason":""}"#.utf8)
        let r = try RiderAPICodec.decode(PositionsResponse.self, from: data)
        #expect(r.state == .verified)
        #expect(r.published)
        #expect(r.corroboration == .corroborated)
        #expect(r.accepted == 3)
        #expect(r.endReasonValue == nil)
        let ended = Data(#"{"state":"rejected","published":false,"corroboration":"none","accepted":5,"ignored":1,"off_route_streak":5,"ended":true,"end_reason":"off_route"}"#.utf8)
        #expect(try RiderAPICodec.decode(PositionsResponse.self, from: ended).endReasonValue == .offRoute)
    }

    @Test func endRideRoundTrip() throws {
        let obj = try json(RiderAPICodec.encode(EndRideRequest(reason: .userRequested)))
        #expect(obj["reason"] as? String == "user_requested")
        let data = Data(#"{"status":"ride ended","summary":{"points":142,"matched":139,"corroborated":88,"duration_seconds":1260}}"#.utf8)
        let r = try RiderAPICodec.decode(EndRideResponse.self, from: data)
        #expect(r.summary == RideSummary(points: 142, matched: 139, corroborated: 88, durationSeconds: 1260))
    }

    @Test func tripStatusDecodes() throws {
        let data = Data(#"{"trip_id":"T1","start_date":"20260902","trusted":true,"rider_reported":false,"riders":0}"#.utf8)
        let s = try RiderAPICodec.decode(TripStatus.self, from: data)
        #expect(s == TripStatus(tripID: "T1", startDate: "20260902", trusted: true, riderReported: false, riders: 0))
    }

    @Test func endReasonWireValuesAndClientReportability() {
        #expect(RideEndReason.maxDuration.rawValue == "max_duration")
        #expect(RideEndReason.userRequested.isClientReportable)
        #expect(RideEndReason.appTerminated.isClientReportable)
        #expect(!RideEndReason.offRoute.isClientReportable)
        #expect(!RideEndReason.superseded.isClientReportable)
        #expect(RideEndReason.allCases.filter(\.isClientReportable).count == 8)
    }

    @Test func serverErrorBodyDecodes() throws {
        #expect(try RiderAPICodec.decode(ServerErrorBody.self, from: Data(#"{"error":"ride ended"}"#.utf8)).error == "ride ended")
    }
}
```

- [ ] **Step 3: Run → FAIL.** `cd ios/VehiclePositionsKit && export DEVELOPER_DIR=/Applications/Xcode-27.0.0-beta.6.app/Contents/Developer && xcodebuild test -scheme VehiclePositionsKit -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -30` — compile failure.

- [ ] **Step 4: Implement** `PublicTypes.swift` and `WireTypes.swift` per the Interfaces block. `RiderAPICodec.encode` uses a `JSONEncoder` with `.sortedKeys`; `attestation` is encoded with `encodeNil` when nil (custom `encode(to:)` on `RegisterRequest`). `PositionsResponse.endReasonValue` = `RideEndReason(rawValue: endReason)` when non-empty.

- [ ] **Step 5: Run → PASS** (`** TEST SUCCEEDED **`, zero warnings in the build log: `grep -c 'warning:' build.log` → 0).

- [ ] **Step 6: Commit** — `git add ios/VehiclePositionsKit && git commit -m "feat(ios): VehiclePositionsKit package scaffold, public models and wire types"`

---

### Task 16: seams — clock, credentials, location, transport, client; buffer and end-condition evaluator

**Files:**
- Create under `ios/VehiclePositionsKit/Sources/VehiclePositionsKit/`: `Clock/RideClock.swift`, `Credentials/CredentialStore.swift`, `Location/LocationSource.swift`, `Transport/RideTransport.swift`, `Transport/RiderClient.swift`, `Internal/SampleBuffer.swift`, `Internal/EndConditionEvaluator.swift`, `Internal/GeoDistance.swift`
- Create under `Tests/VehiclePositionsKitTests/`: `Fakes/FakeRideTransport.swift`, `Fakes/FakeLocationSource.swift`, `RiderClientTests.swift`, `ManualRideClockTests.swift`, `SampleBufferTests.swift`, `EndConditionEvaluatorTests.swift`

**Interfaces:**

```swift
// Clock/RideClock.swift
public protocol RideClock: Sendable {
    var now: Date { get }
    func sleep(for duration: Duration) async throws   // throws CancellationError when cancelled
}
public struct ContinuousRideClock: RideClock { public init() }           // Date() + Task.sleep(for:)
public final class ManualRideClock: RideClock, @unchecked Sendable {     // NSLock-guarded
    public init(now: Date = Date(timeIntervalSince1970: 1_756_800_000))
    public var now: Date { get }
    public func sleep(for duration: Duration) async throws               // parks until advance() passes the deadline
    public func advance(by duration: Duration)                           // resumes every sleeper whose deadline ≤ new now
    public var sleeperCount: Int { get }
    public func waitForSleepers(atLeast n: Int, timeout: Duration = .seconds(2)) async -> Bool // polls with Task.yield/Task.sleep(1 ms)
}

// Credentials/CredentialStore.swift
public struct RiderCredentials: Sendable, Codable, Equatable { public var installationID: String; public var riderID: String?; public var token: String? }
public protocol CredentialStore: Sendable { func load() throws -> RiderCredentials?; func save(_ c: RiderCredentials) throws; func clear() throws }
public final class InMemoryCredentialStore: CredentialStore, @unchecked Sendable { public init(_ initial: RiderCredentials? = nil) }

// Location/LocationSource.swift
public struct LocationFix: Sendable, Equatable {
    public var latitude, longitude: Double
    public var horizontalAccuracy: Double  // -1 when unknown
    public var speed: Double               // -1 when unknown
    public var course: Double              // -1 when unknown
    public var timestamp: Date
    public init(latitude:longitude:horizontalAccuracy:speed:course:timestamp:)
    public var coordinate: Coordinate { get }
}
public enum LocationDiagnostic: Sendable, Equatable { case authorizationDenied, locationUnavailable, accuracyLimited, insufficientlyInUse }
public struct LocationSample: Sendable, Equatable {
    public var fix: LocationFix?; public var isStationary: Bool; public var diagnostic: LocationDiagnostic?
    public init(fix: LocationFix?, isStationary: Bool = false, diagnostic: LocationDiagnostic? = nil)
}
public protocol BackgroundActivityHandle: Sendable { func invalidate() }
public protocol LocationSource: Sendable {
    func updates() -> AsyncThrowingStream<LocationSample, any Error>
    func beginBackgroundActivity() -> any BackgroundActivityHandle
}

// Transport/RideTransport.swift
public struct RiderRequest: Sendable, Equatable { public var method: String; public var path: String; public var query: [String: String]; public var body: Data?; public var bearerToken: String? }
public struct RiderResponse: Sendable, Equatable { public var status: Int; public var body: Data }
public protocol RideTransport: Sendable { func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse }
public struct URLSessionRideTransport: RideTransport {
    public init(session: URLSession = URLSessionRideTransport.makeSession()) // ephemeral config, 10 s request timeout, waitsForConnectivity=false
    public static func makeSession() -> URLSession
    // sets Content-Type: application/json when body != nil, Authorization: Bearer when token != nil, Accept: application/json
}

// Transport/RiderClient.swift
public struct RiderClient: Sendable {
    public init(serverURL: URL, transport: any RideTransport)
    public func register(installationID: String, appID: String, appVersion: String) async throws -> RegisterResponse   // 200 or 201
    public func startRide(token: String, trip: TripDescriptor) async throws -> StartRideResponse                  // 201
    public func uploadPositions(token: String, rideID: String, positions: [PositionUpload]) async throws -> PositionsResponse
    public func endRide(token: String, rideID: String, reason: RideEndReason) async throws -> EndRideResponse
    public func tripStatus(token: String, tripID: String, startDate: String?) async throws -> TripStatus
}
// Status mapping: transport throw → .transport(description); 401 → .notAuthorized; 409 → .alreadyEnded;
// other non-2xx → .server(status:message:) with message from ServerErrorBody or "" ; 2xx decode failure → .decoding(description).
// Paths: "api/v1/rider/register", "api/v1/rider/rides", "api/v1/rider/rides/{id}/positions", "api/v1/rider/rides/{id}/end", "api/v1/rider/trips/{tripID}/status" (+ query start_date when non-nil).
// platform is always "ios".

// Internal/SampleBuffer.swift
struct SampleBuffer: Sendable {
    let retention: Duration
    private(set) var fixes: [LocationFix]
    var count: Int
    mutating func append(_ fix: LocationFix, now: Date)      // then prune(older than now − retention)
    mutating func take(max: Int) -> [LocationFix]            // oldest first, removed
    mutating func restore(_ fixes: [LocationFix])            // prepend (upload failed)
}

// Internal/GeoDistance.swift
enum GeoDistance { static func meters(from a: Coordinate, to b: Coordinate) -> Double } // haversine

// Internal/EndConditionEvaluator.swift
struct EndConditionEvaluator: Sendable {
    init(configuration: RideReporterConfiguration, destination: Coordinate?)
    private(set) var firstFix: Coordinate?
    private(set) var stationarySince: Date?
    mutating func evaluate(_ sample: LocationSample, now: Date) -> RideEndReason?
    // diagnostics first: .authorizationDenied → .authorizationDenied, .locationUnavailable → .locationUnavailable (other diagnostics → nil)
    // stationary: isStationary sets/keeps stationarySince; elapsed ≥ stationaryTimeout → .stationary; a non-stationary sample resets
    // arrival: fix within arrivalRadiusMeters of destination AND meters(firstFix, fix) ≥ minimumTravelBeforeArrivalMeters → .arrived
}
```

Fakes (test target):

```swift
// Fakes/FakeRideTransport.swift
final class FakeRideTransport: RideTransport, @unchecked Sendable {
    struct Recorded: Sendable { let request: RiderRequest; let baseURL: URL }
    private let lock = NSLock()
    private(set) var recorded: [Recorded] = []
    private var scripted: [String: [Result<RiderResponse, Error>]] = [:]   // keyed by "METHOD path-suffix" e.g. "POST /positions"
    struct Unscripted: Error { let key: String }
    func script(_ key: String, _ results: Result<RiderResponse, Error>...)  // appends; consumed FIFO; last one repeats when exhausted; an unscripted key throws Unscripted
    func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse
    // key match: request.method + " " + last path component group: "register" | "rides" | "positions" | "end" | "status"
    func requests(matching key: String) -> [RiderRequest]
    static func ok(_ status: Int = 200, json: String) -> Result<RiderResponse, Error>
    static func error(_ status: Int, message: String) -> Result<RiderResponse, Error>
    static let transportFailure: Result<RiderResponse, Error> = .failure(URLError(.notConnectedToInternet))
}
// Fakes/FakeLocationSource.swift
final class FakeLocationSource: LocationSource, @unchecked Sendable {
    final class Handle: BackgroundActivityHandle, @unchecked Sendable { private(set) var invalidated = false; func invalidate() }
    private(set) var handles: [Handle] = []
    private var continuation: AsyncThrowingStream<LocationSample, any Error>.Continuation?
    func updates() -> AsyncThrowingStream<LocationSample, any Error>   // stores the continuation
    func beginBackgroundActivity() -> any BackgroundActivityHandle
    func emit(_ sample: LocationSample); func emitFix(lat: Double, lon: Double, at: Date, accuracy: Double = 5, speed: Double = 8, course: Double = 0, stationary: Bool = false)
    func finish(throwing: Error? = nil)
    var lastHandleInvalidated: Bool
}
```

- [ ] **Step 1: Write the failing tests**

```swift
// RiderClientTests.swift
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct RiderClientTests {
    let base = URL(string: "https://vp.example.org")!

    @Test func registerPostsAndDecodes() async throws {
        let t = FakeRideTransport()
        t.script("POST register", FakeRideTransport.ok(201, json: #"{"rider_id":"r1","token":"tok","report_interval_seconds":5,"max_batch_size":12}"#))
        let c = RiderClient(serverURL: base, transport: t)
        let r = try await c.register(installationID: "inst", appID: "org.test", appVersion: "1")
        #expect(r.riderID == "r1")
        let req = try #require(t.requests(matching: "POST register").first)
        #expect(req.path == "api/v1/rider/register")
        #expect(req.bearerToken == nil)
        let body = try #require(req.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
        #expect(obj["platform"] as? String == "ios")
        #expect(obj["installation_id"] as? String == "inst")
    }

    @Test func startRideSendsBearerAndPath() async throws {
        let t = FakeRideTransport()
        t.script("POST rides", FakeRideTransport.ok(201, json: #"{"ride_id":"ride1","state":"pending","report_interval_seconds":5,"max_batch_size":12}"#))
        let c = RiderClient(serverURL: base, transport: t)
        let r = try await c.startRide(token: "tok", trip: TripDescriptor(tripID: "T1"))
        #expect(r.rideID == "ride1")
        let req = try #require(t.requests(matching: "POST rides").first)
        #expect(req.bearerToken == "tok")
        #expect(req.path == "api/v1/rider/rides")
    }

    @Test func uploadAndEndPaths() async throws {
        let t = FakeRideTransport()
        t.script("POST positions", FakeRideTransport.ok(json: #"{"state":"pending","published":false,"corroboration":"unavailable","accepted":1,"ignored":0,"off_route_streak":0,"ended":false,"end_reason":""}"#))
        t.script("POST end", FakeRideTransport.ok(json: #"{"status":"ride ended","summary":{"points":1,"matched":1,"corroborated":0,"duration_seconds":10}}"#))
        let c = RiderClient(serverURL: base, transport: t)
        _ = try await c.uploadPositions(token: "tok", rideID: "ride1", positions: [PositionUpload(latitude: 1, longitude: 2, accuracy: nil, speed: nil, bearing: nil, timestamp: 3)])
        let end = try await c.endRide(token: "tok", rideID: "ride1", reason: .arrived)
        #expect(end.summary.points == 1)
        #expect(t.requests(matching: "POST positions").first?.path == "api/v1/rider/rides/ride1/positions")
        #expect(t.requests(matching: "POST end").first?.path == "api/v1/rider/rides/ride1/end")
    }

    @Test func tripStatusQuery() async throws {
        let t = FakeRideTransport()
        t.script("GET status", FakeRideTransport.ok(json: #"{"trip_id":"T1","start_date":"20260902","trusted":false,"rider_reported":true,"riders":2}"#))
        let c = RiderClient(serverURL: base, transport: t)
        let s = try await c.tripStatus(token: "tok", tripID: "T1", startDate: "20260902")
        #expect(s.riders == 2)
        let req = try #require(t.requests(matching: "GET status").first)
        #expect(req.path == "api/v1/rider/trips/T1/status")
        #expect(req.query == ["start_date": "20260902"])
    }

    @Test func errorMapping() async throws {
        let t = FakeRideTransport()
        t.script("POST positions",
                 FakeRideTransport.error(401, message: "invalid token"),
                 FakeRideTransport.error(409, message: "ride ended"),
                 FakeRideTransport.error(429, message: "rate limit exceeded"),
                 FakeRideTransport.transportFailure,
                 FakeRideTransport.ok(json: "not json"))
        let c = RiderClient(serverURL: base, transport: t)
        let pos = [PositionUpload(latitude: 1, longitude: 2, accuracy: nil, speed: nil, bearing: nil, timestamp: 3)]
        await #expect(throws: RideError.notAuthorized) { try await c.uploadPositions(token: "t", rideID: "r", positions: pos) }
        await #expect(throws: RideError.alreadyEnded) { try await c.uploadPositions(token: "t", rideID: "r", positions: pos) }
        await #expect(throws: RideError.server(status: 429, message: "rate limit exceeded")) { try await c.uploadPositions(token: "t", rideID: "r", positions: pos) }
        do { _ = try await c.uploadPositions(token: "t", rideID: "r", positions: pos); Issue.record("expected transport error") }
        catch let RideError.transport(msg) { #expect(!msg.isEmpty) }
        do { _ = try await c.uploadPositions(token: "t", rideID: "r", positions: pos); Issue.record("expected decoding error") }
        catch let RideError.decoding(msg) { #expect(!msg.isEmpty) }
    }
}

// ManualRideClockTests.swift
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct ManualRideClockTests {
    @Test func advanceResumesSleepersInOrder() async throws {
        let clock = ManualRideClock()
        let start = clock.now
        let task = Task { try await clock.sleep(for: .seconds(5)); return clock.now }
        #expect(await clock.waitForSleepers(atLeast: 1))
        clock.advance(by: .seconds(2))
        #expect(clock.sleeperCount == 1)
        clock.advance(by: .seconds(3))
        let woke = try await task.value
        #expect(woke == start.addingTimeInterval(5))
        #expect(clock.sleeperCount == 0)
    }

    @Test func cancellationThrows() async {
        let clock = ManualRideClock()
        let task = Task { try await clock.sleep(for: .seconds(60)) }
        _ = await clock.waitForSleepers(atLeast: 1)
        task.cancel()
        await #expect(throws: CancellationError.self) { try await task.value }
    }
}

// SampleBufferTests.swift
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct SampleBufferTests {
    func fix(_ t: TimeInterval) -> LocationFix { LocationFix(latitude: 1, longitude: 2, horizontalAccuracy: 5, speed: 1, course: 0, timestamp: Date(timeIntervalSince1970: t)) }

    @Test func takeReturnsOldestFirstAndRemoves() {
        var b = SampleBuffer(retention: .seconds(600))
        let now = Date(timeIntervalSince1970: 1000)
        b.append(fix(990), now: now); b.append(fix(995), now: now); b.append(fix(1000), now: now)
        let taken = b.take(max: 2)
        #expect(taken.map(\.timestamp.timeIntervalSince1970) == [990, 995])
        #expect(b.count == 1)
        b.restore(taken)
        #expect(b.take(max: 10).map(\.timestamp.timeIntervalSince1970) == [990, 995, 1000])
    }

    @Test func pruneDropsOldSamples() {
        var b = SampleBuffer(retention: .seconds(600))
        b.append(fix(100), now: Date(timeIntervalSince1970: 100))
        b.append(fix(800), now: Date(timeIntervalSince1970: 800))
        #expect(b.count == 1)
    }
}

// EndConditionEvaluatorTests.swift
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct EndConditionEvaluatorTests {
    let config = RideReporterConfiguration(serverURL: URL(string: "https://x")!, appID: "a", appVersion: "1")
    let dest = Coordinate(latitude: 47.6090, longitude: -122.3300) // 1 km north of the start
    func sample(lat: Double, lon: Double = -122.33, stationary: Bool = false, at t: TimeInterval = 0) -> LocationSample {
        LocationSample(fix: LocationFix(latitude: lat, longitude: lon, horizontalAccuracy: 5, speed: 8, course: 0, timestamp: Date(timeIntervalSince1970: t)), isStationary: stationary)
    }

    @Test func arrivalRequiresTravelGuard() {
        var e = EndConditionEvaluator(configuration: config, destination: dest)
        let now = Date()
        #expect(e.evaluate(sample(lat: 47.6089), now: now) == nil, "starting next to the destination does not end")
        #expect(e.evaluate(sample(lat: 47.6000), now: now) == nil)
        #expect(e.evaluate(sample(lat: 47.6089), now: now) == nil, "first fix was near the destination: travelled distance from the first fix is ~0")

        var f = EndConditionEvaluator(configuration: config, destination: dest)
        #expect(f.evaluate(sample(lat: 47.6000), now: now) == nil)
        #expect(f.evaluate(sample(lat: 47.6050), now: now) == nil, "500 m away")
        #expect(f.evaluate(sample(lat: 47.6086), now: now) == .arrived, "≈45 m from the destination after 950 m of travel")
    }

    @Test func noDestinationNeverArrives() {
        var e = EndConditionEvaluator(configuration: config, destination: nil)
        #expect(e.evaluate(sample(lat: 47.6000), now: Date()) == nil)
        #expect(e.evaluate(sample(lat: 47.6090), now: Date()) == nil)
    }

    @Test func stationaryTimeout() {
        var e = EndConditionEvaluator(configuration: config, destination: nil)
        let t0 = Date(timeIntervalSince1970: 0)
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0) == nil)
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0.addingTimeInterval(599)) == nil)
        #expect(e.evaluate(sample(lat: 47.6, stationary: false), now: t0.addingTimeInterval(600)) == nil, "movement resets")
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0.addingTimeInterval(700)) == nil)
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0.addingTimeInterval(1300)) == .stationary)
    }

    @Test func diagnostics() {
        var e = EndConditionEvaluator(configuration: config, destination: nil)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .accuracyLimited), now: Date()) == nil)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .insufficientlyInUse), now: Date()) == nil)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .locationUnavailable), now: Date()) == .locationUnavailable)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .authorizationDenied), now: Date()) == .authorizationDenied)
    }

    @Test func geoDistance() {
        let d = GeoDistance.meters(from: Coordinate(latitude: 47.6000, longitude: -122.33), to: dest)
        #expect(abs(d - 1001) < 5)
    }
}
```

- [ ] **Step 2: Run → FAIL** (compile).

- [ ] **Step 3: Implement** every file in the Interfaces block plus the two fakes. `ManualRideClock.sleep`: register `(deadline, CheckedContinuation)` under the lock, then `withTaskCancellationHandler` removing + resuming with `CancellationError` on cancel; `advance` collects due continuations under the lock and resumes them outside it. `URLSessionRideTransport.send`: builds `baseURL.appending(path:)` + query items, sets headers, `session.data(for:)`, returns status + body; non-HTTP response → throw `URLError(.badServerResponse)`.

- [ ] **Step 4: Run → PASS**, zero warnings.

- [ ] **Step 5: Commit** — `git add ios/VehiclePositionsKit && git commit -m "feat(ios): clock, credential, location and transport seams, rider client, buffer and end-condition evaluator"`

---

### Task 17: `RideReporter` actor — full ride lifecycle against fakes

**Files:**
- Create: `Sources/VehiclePositionsKit/RideReporter.swift`, `Tests/VehiclePositionsKitTests/RideReporterTests.swift`, `Tests/VehiclePositionsKitTests/Fakes/EventCollector.swift`

**Interfaces:**

```swift
public actor RideReporter {
    public init(configuration: RideReporterConfiguration, transport: any RideTransport, locationSource: any LocationSource,
                credentialStore: any CredentialStore, clock: any RideClock = ContinuousRideClock())
    public var currentState: RideState? { get }          // nil when no ride is active
    public var isActive: Bool { get }
    public func start(_ trip: TripDescriptor) async throws -> AsyncStream<RideEvent>
    public func end(reason: RideEndReason = .userRequested) async
    public func tripStatus(tripID: String, startDate: String?) async throws -> TripStatus   // registers if needed
}
```

Behaviour (spec §4.12, implement exactly):

1. `start`: if a ride is active → `await end(reason: .superseded)` first. Load credentials (`installationID` generated with `UUID().uuidString` and saved when none). If `token == nil` → `register` → save → emit `.registered(riderID:)` **after the stream is created** (buffer the event; the stream is created before any event is emitted, and `AsyncStream` buffers so emitting before the caller iterates is safe — create the stream first, then do the network work, so `.registered` and `.started` land in order). `startRide`; on `.notAuthorized`: clear token, register once more, retry once; any other error rethrows and leaves no active ride. Store `reportInterval = .seconds(resp.reportIntervalSeconds)`, `maxBatchSize`, `destination`. `handle = locationSource.beginBackgroundActivity()`. Spawn `locationTask` and `uploadTask`. Emit `.started(rideID:)`. `currentState = resp.state`.
2. `locationTask`: `for try await sample in locationSource.updates()`: `evaluator.evaluate(sample, now: clock.now)` → non-nil reason → `await end(reason:)` and return; diagnostics `.accuracyLimited` → emit `.warning(.accuracyLimited)` once per ride; `.insufficientlyInUse` → `.warning(.insufficientlyInUse)` once; `fix != nil` → `buffer.append`; `buffer.count >= maxBatchSize` → `await flush()`. Stream error/finish → `end(reason: .locationUnavailable)`.
3. `uploadTask` loop: `let delay = failureAttempts == 0 ? reportInterval : backoff(failureAttempts)` (`min(30, 2^(attempts-1))` seconds); `try await clock.sleep(for: delay)` (cancellation → return); if `clock.now − startedAt ≥ maxRideDuration` → `end(.maxDuration)`; else `await flush()`.
4. `flush()`: if buffer empty → return; `batch = buffer.take(max: maxBatchSize)`; convert to `PositionUpload` (`-1` → nil; `timestamp = Int(fix.timestamp.timeIntervalSince1970)`); `uploadPositions`; on success: `failureAttempts = 0; failingSince = nil; currentState = resp.state; emit .progress(RideProgress(...)); if resp.ended → end(reason: resp.endReasonValue ?? .idle)`; on `RideError.alreadyEnded` → `end(reason: .serverRestart)`; on `RideError.notAuthorized` → `end(reason: .authorizationDenied)`… no: a 401 mid-ride means the token expired; treat as `.serverRestart`? Decide: **clear stored token and end with `.networkFailure`** is wrong too. Rule: 401 mid-ride → clear token, `end(reason: .serverRestart)` (the next `start` re-registers). On any other error: `buffer.restore(batch)`, `failureAttempts += 1`, `failingSince = failingSince ?? clock.now`, emit `.warning(.uploadRetrying(attempt: failureAttempts))`; if `clock.now − failingSince ≥ uploadFailureTimeout` → `end(reason: .networkFailure)`.
5. `end(reason:)`: guard active (idempotent); mark `ending` so re-entrant calls return; cancel both tasks; best-effort final flush (only if reason is not a server-initiated one and the buffer is non-empty; ignore errors); best-effort `endRide` when `reason.isClientReportable` (server-initiated reasons and `.serverRestart`/`.superseded`/`.idle` skip the call; `.superseded` maps to `.userRequested` on the wire — actually the server ends the old ride itself on the next start, so skip); `summary` from the response when available; emit `.ended(reason, summary:)`; `continuation.finish()`; `handle.invalidate()`; clear state.
6. `tripStatus`: ensure credentials (register if needed) then `client.tripStatus`.

- [ ] **Step 1: Write the event collector and the failing tests**

```swift
// Fakes/EventCollector.swift
import Foundation
@testable import VehiclePositionsKit

actor EventCollector {
    private(set) var events: [RideEvent] = []
    private var task: Task<Void, Never>?
    init(_ stream: AsyncStream<RideEvent>) {
        task = Task { [weak self] in
            for await e in stream { await self?.append(e) }
        }
    }
    private func append(_ e: RideEvent) { events.append(e) }
    /// Polls until `predicate` is satisfied or `timeout` elapses (real time).
    func wait(timeout: Duration = .seconds(5), _ predicate: @Sendable ([RideEvent]) -> Bool) async -> Bool {
        let deadline = ContinuousClock.now + timeout
        while ContinuousClock.now < deadline {
            if predicate(events) { return true }
            try? await Task.sleep(for: .milliseconds(5))
        }
        return predicate(events)
    }
    func waitForEnd(timeout: Duration = .seconds(5)) async -> RideEndReason? {
        _ = await wait(timeout: timeout) { $0.contains { if case .ended = $0 { return true }; return false } }
        for e in events { if case let .ended(r, _) = e { return r } }
        return nil
    }
}

// RideReporterTests.swift
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite(.serialized) struct RideReporterTests {
    struct Env {
        let transport = FakeRideTransport()
        let location = FakeLocationSource()
        let credentials = InMemoryCredentialStore()
        let clock = ManualRideClock()
        let reporter: RideReporter
        init(config: RideReporterConfiguration? = nil) {
            let cfg = config ?? RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "org.test", appVersion: "1")
            reporter = RideReporter(configuration: cfg, transport: transport, locationSource: location, credentialStore: credentials, clock: clock)
        }
        static let registerJSON = #"{"rider_id":"r1","token":"tok","report_interval_seconds":5,"max_batch_size":3}"#
        static let startJSON = #"{"ride_id":"ride1","state":"pending","report_interval_seconds":5,"max_batch_size":3,"destination":{"stop_id":"S","latitude":47.6090,"longitude":-122.33}}"#
        static func positionsJSON(state: String = "verified", published: Bool = false, ended: Bool = false, reason: String = "") -> String {
            #"{"state":"\#(state)","published":\#(published),"corroboration":"unavailable","accepted":3,"ignored":0,"off_route_streak":0,"ended":\#(ended),"end_reason":"\#(reason)"}"#
        }
        static let endJSON = #"{"status":"ride ended","summary":{"points":3,"matched":3,"corroborated":0,"duration_seconds":30}}"#
        func scriptHappyPath() {
            transport.script("POST register", FakeRideTransport.ok(201, json: Self.registerJSON))
            transport.script("POST rides", FakeRideTransport.ok(201, json: Self.startJSON))
            transport.script("POST positions", FakeRideTransport.ok(json: Self.positionsJSON()))
            transport.script("POST end", FakeRideTransport.ok(json: Self.endJSON))
        }
        /// Emits `n` fixes moving north from 47.6000 in 10 m steps, timestamps from the manual clock.
        func emitFixes(_ n: Int, startLat: Double = 47.6000) {
            for i in 0..<n {
                location.emitFix(lat: startLat + Double(i) * 0.00009, lon: -122.33, at: clock.now.addingTimeInterval(Double(i)))
            }
        }
    }

    @Test func registersStartsUploadsAndEnds() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        #expect(await events.wait { $0.contains(.registered(riderID: "r1")) && $0.contains(.started(rideID: "ride1")) })
        #expect(await env.reporter.currentState == .pending)
        #expect(try env.credentials.load()?.token == "tok")
        #expect(env.location.handles.count == 1)

        env.emitFixes(2)
        #expect(await env.clock.waitForSleepers(atLeast: 1))
        env.clock.advance(by: .seconds(5))
        #expect(await events.wait { $0.contains { if case .progress(let p) = $0 { return p.state == .verified }; return false } })
        #expect(await env.reporter.currentState == .verified)
        let upload = try #require(env.transport.requests(matching: "POST positions").first)
        let body = try #require(JSONSerialization.jsonObject(with: upload.body!) as? [String: Any])
        #expect((body["positions"] as? [[String: Any]])?.count == 2)

        await env.reporter.end(reason: .userRequested)
        #expect(await events.waitForEnd() == .userRequested)
        let endReq = try #require(env.transport.requests(matching: "POST end").first)
        #expect(String(data: endReq.body!, encoding: .utf8)!.contains("user_requested"))
        #expect(env.location.lastHandleInvalidated)
        #expect(await env.reporter.isActive == false)
        #expect(await env.reporter.currentState == nil)
    }

    @Test func batchSizeTriggersImmediateUpload() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3) // max_batch_size = 3
        #expect(await events.wait { $0.contains { if case .progress = $0 { return true }; return false } })
        #expect(env.transport.requests(matching: "POST positions").count == 1)
        await env.reporter.end()
    }

    @Test func reusesStoredCredentialsAndReRegistersOn401() async throws {
        let env = Env()
        try env.credentials.save(RiderCredentials(installationID: "inst", riderID: "old", token: "expired"))
        env.transport.script("POST rides", FakeRideTransport.error(401, message: "invalid token"), FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST register", FakeRideTransport.ok(200, json: Env.registerJSON))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        #expect(await events.wait { $0.contains(.started(rideID: "ride1")) })
        #expect(env.transport.requests(matching: "POST register").count == 1)
        #expect(env.transport.requests(matching: "POST rides").count == 2)
        #expect(try env.credentials.load()?.token == "tok")
        #expect(try env.credentials.load()?.installationID == "inst", "installation id is stable across re-registration")
        await env.reporter.end()
    }

    @Test func serverEndsRideViaPositionsResponse() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.ok(json: Env.positionsJSON(state: "rejected", ended: true, reason: "off_route")))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3)
        #expect(await events.waitForEnd() == .offRoute)
        #expect(env.transport.requests(matching: "POST end").isEmpty, "server-initiated end sends no end request")
        #expect(await env.reporter.isActive == false)
    }

    @Test func conflictMeansServerRestart() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.error(409, message: "ride ended"))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3)
        #expect(await events.waitForEnd() == .serverRestart)
    }

    @Test func retriesWithBackoffThenGivesUp() async throws {
        var cfg = RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "a", appVersion: "1")
        cfg.uploadFailureTimeout = .seconds(60)
        let env = Env(config: cfg)
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.transportFailure)
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3) // immediate upload → fails → warning(attempt 1)
        #expect(await events.wait { $0.contains(.warning(.uploadRetrying(attempt: 1))) })
        // backoff 1 s, 2 s, 4 s, 8 s, 16 s, 32→30 s: after 60 s of failure the ride ends.
        for _ in 0..<8 {
            _ = await env.clock.waitForSleepers(atLeast: 1)
            env.clock.advance(by: .seconds(16))
        }
        #expect(await events.waitForEnd() == .networkFailure)
        #expect(env.transport.requests(matching: "POST positions").count >= 4)
    }

    @Test func arrivalEndsRide() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1", destinationStopID: "S"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.location.emitFix(lat: 47.6000, lon: -122.33, at: env.clock.now)
        env.location.emitFix(lat: 47.6050, lon: -122.33, at: env.clock.now)
        env.location.emitFix(lat: 47.6087, lon: -122.33, at: env.clock.now) // ~33 m from the destination
        #expect(await events.waitForEnd() == .arrived)
        let endReq = try #require(env.transport.requests(matching: "POST end").first)
        #expect(String(data: endReq.body!, encoding: .utf8)!.contains("arrived"))
    }

    @Test func stationaryAndMaxDurationEndRide() async throws {
        var cfg = RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "a", appVersion: "1")
        cfg.stationaryTimeout = .seconds(20)
        let env = Env(config: cfg); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.location.emitFix(lat: 47.6, lon: -122.33, at: env.clock.now, stationary: true)
        env.clock.advance(by: .seconds(25))
        env.location.emitFix(lat: 47.6, lon: -122.33, at: env.clock.now, stationary: true)
        #expect(await events.waitForEnd() == .stationary)

        var cfg2 = cfg
        cfg2.maxRideDuration = .seconds(12)
        let env2 = Env(config: cfg2); env2.scriptHappyPath()
        let stream2 = try await env2.reporter.start(TripDescriptor(tripID: "T1"))
        let events2 = EventCollector(stream2)
        _ = await events2.wait { $0.contains(.started(rideID: "ride1")) }
        for _ in 0..<3 {
            _ = await env2.clock.waitForSleepers(atLeast: 1)
            env2.clock.advance(by: .seconds(5))
        }
        #expect(await events2.waitForEnd() == .maxDuration)
    }

    @Test func diagnosticsEndOrWarn() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.location.emit(LocationSample(fix: nil, diagnostic: .accuracyLimited))
        #expect(await events.wait { $0.contains(.warning(.accuracyLimited)) })
        env.location.emit(LocationSample(fix: nil, diagnostic: .authorizationDenied))
        #expect(await events.waitForEnd() == .authorizationDenied)
    }

    @Test func startingAgainSupersedes() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON), FakeRideTransport.ok(201, json: Env.startJSON.replacingOccurrences(of: "ride1", with: "ride2")))
        env.transport.script("POST positions", FakeRideTransport.ok(json: Env.positionsJSON()))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let s1 = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let e1 = EventCollector(s1)
        _ = await e1.wait { $0.contains(.started(rideID: "ride1")) }
        let s2 = try await env.reporter.start(TripDescriptor(tripID: "T2"))
        let e2 = EventCollector(s2)
        #expect(await e1.waitForEnd() == .superseded)
        #expect(await e2.wait { $0.contains(.started(rideID: "ride2")) })
        #expect(env.location.handles.count == 2)
        #expect(env.location.handles[0].invalidated)
        await env.reporter.end()
    }

    @Test func startFailureLeavesNoActiveRide() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.error(404, message: "unknown trip"))
        await #expect(throws: RideError.server(status: 404, message: "unknown trip")) {
            _ = try await env.reporter.start(TripDescriptor(tripID: "NOPE"))
        }
        #expect(await env.reporter.isActive == false)
        #expect(env.location.handles.isEmpty)
    }

    @Test func tripStatusRegistersWhenNeeded() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("GET status", FakeRideTransport.ok(json: #"{"trip_id":"T1","start_date":"20260902","trusted":false,"rider_reported":true,"riders":1}"#))
        let s = try await env.reporter.tripStatus(tripID: "T1", startDate: nil)
        #expect(s.riderReported)
        #expect(env.transport.requests(matching: "GET status").first?.bearerToken == "tok")
    }
}
```

- [ ] **Step 2: Run → FAIL** (compile).

- [ ] **Step 3: Implement** `RideReporter.swift` per the behaviour list. Internal state struct `ActiveRide { rideID, trip, startedAt: Date, destination: Coordinate?, reportInterval: Duration, maxBatchSize: Int, continuation: AsyncStream<RideEvent>.Continuation, handle: any BackgroundActivityHandle, evaluator: EndConditionEvaluator, buffer: SampleBuffer, failureAttempts: Int, failingSince: Date?, warnedAccuracy: Bool, warnedInUse: Bool, ending: Bool }` plus `locationTask: Task<Void, Never>?`, `uploadTask: Task<Void, Never>?`. Tasks are created with `Task { [weak self] in await self?.runLocationLoop(rideID:) }` and check `rideID == active?.rideID` before mutating, so a superseded ride's late events cannot touch the new ride.

- [ ] **Step 4: Run → PASS**, zero warnings, run the suite twice to catch flakiness (`for i in 1 2; do xcodebuild test ... | tail -3; done`).

- [ ] **Step 5: Commit** — `git add ios/VehiclePositionsKit && git commit -m "feat(ios): RideReporter actor with batching, retry, end conditions and superseding"`

---

### Task 18: production adapters — CoreLocation source, Keychain store, convenience init, README

**Files:**
- Create: `Sources/VehiclePositionsKit/Location/CoreLocationSource.swift`, `Sources/VehiclePositionsKit/Credentials/KeychainCredentialStore.swift`, `Sources/VehiclePositionsKit/RideReporter+Defaults.swift`, `Tests/VehiclePositionsKitTests/KeychainCredentialStoreTests.swift`, `Tests/VehiclePositionsKitTests/CoreLocationSourceTests.swift`, `ios/VehiclePositionsKit/README.md`

**Interfaces:**

```swift
#if os(iOS)
import CoreLocation
public final class CoreLocationSource: LocationSource {
    public init(configuration: CLLocationUpdate.LiveConfiguration = .otherNavigation)
    public func updates() -> AsyncThrowingStream<LocationSample, any Error>
    // Task: holds `CLServiceSession(authorization: .whenInUse)` for the iteration's lifetime; iterates
    // CLLocationUpdate.liveUpdates(configuration); yields LocationSample(update); finish on completion/error;
    // onTermination cancels the task and invalidates the service session.
    public func beginBackgroundActivity() -> any BackgroundActivityHandle   // wraps CLBackgroundActivitySession
}
extension LocationSample { init(_ update: CLLocationUpdate) }
// mapping: fix from update.location (coordinate, horizontalAccuracy, speed, course, timestamp);
// isStationary = update.stationary;
// diagnostic priority: authorizationDenied || authorizationDeniedGlobally || authorizationRestricted → .authorizationDenied;
// locationUnavailable → .locationUnavailable; accuracyLimited → .accuracyLimited; insufficientlyInUse → .insufficientlyInUse; else nil.

public final class KeychainCredentialStore: CredentialStore {
    public init(service: String = "org.onebusaway.vehiclepositionskit", account: String = "rider-credentials")
    // kSecClassGenericPassword, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, JSON-encoded RiderCredentials;
    // save = delete-then-add; load returns nil on errSecItemNotFound; other OSStatus → KeychainError.status(OSStatus)
}
public enum KeychainError: Error, Equatable { case status(OSStatus) }

extension RideReporter {
    public init(configuration: RideReporterConfiguration)  // URLSessionRideTransport(), CoreLocationSource(), KeychainCredentialStore()
}
#endif
```

- [ ] **Step 1: Write the failing tests**

```swift
// KeychainCredentialStoreTests.swift
#if os(iOS)
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite(.serialized) struct KeychainCredentialStoreTests {
    @Test func roundTripAndClear() throws {
        let store = KeychainCredentialStore(service: "org.onebusaway.vehiclepositionskit.tests", account: UUID().uuidString)
        #expect(try store.load() == nil)
        let creds = RiderCredentials(installationID: "inst", riderID: "r1", token: "tok")
        try store.save(creds)
        #expect(try store.load() == creds)
        try store.save(RiderCredentials(installationID: "inst", riderID: "r1", token: "tok2"))
        #expect(try store.load()?.token == "tok2", "save overwrites")
        try store.clear()
        #expect(try store.load() == nil)
        try store.clear() // idempotent
    }
}
#endif

// CoreLocationSourceTests.swift
#if os(iOS)
import CoreLocation
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct CoreLocationSourceTests {
    @Test func backgroundActivityHandleCanBeInvalidated() {
        let source = CoreLocationSource()
        let handle = source.beginBackgroundActivity()
        handle.invalidate() // must not crash on the simulator without authorization
    }

    @Test func sampleMappingFromLocation() {
        // CLLocationUpdate cannot be constructed in tests; exercise the shared mapping helper instead.
        let loc = CLLocation(coordinate: CLLocationCoordinate2D(latitude: 47.6, longitude: -122.33), altitude: 0, horizontalAccuracy: 7, verticalAccuracy: 0, course: 90, speed: 8, timestamp: Date(timeIntervalSince1970: 100))
        let s = LocationSample(location: loc, stationary: true, diagnostic: nil)
        #expect(s.fix?.latitude == 47.6)
        #expect(s.fix?.horizontalAccuracy == 7)
        #expect(s.fix?.course == 90)
        #expect(s.fix?.speed == 8)
        #expect(s.isStationary)
        #expect(LocationSample(location: nil, stationary: false, diagnostic: .locationUnavailable).fix == nil)
    }
}
#endif
```

`LocationSample.init(location: CLLocation?, stationary: Bool, diagnostic: LocationDiagnostic?)` is the shared helper that `init(_ update: CLLocationUpdate)` calls after computing the diagnostic.

- [ ] **Step 2: Run → FAIL** (compile).

- [ ] **Step 3: Implement** the three source files. Verify the exact CoreLocation property names against the SDK headers before writing them: `xcrun --sdk iphoneos swift-ide-test` is unavailable, so instead open `/Applications/Xcode-27.0.0-beta.6.app/Contents/Developer/Platforms/iPhoneOS.platform/Developer/SDKs/iPhoneOS.sdk/System/Library/Frameworks/CoreLocation.framework/Modules/CoreLocation.swiftmodule/*.swiftinterface` and `grep -n "stationary\|authorizationDenied\|accuracyLimited\|insufficientlyInUse\|serviceSessionRequired\|locationUnavailable" | head` — use `update.stationary` if present (iOS 18 name), else `update.isStationary`.

- [ ] **Step 4: README** (`ios/VehiclePositionsKit/README.md`): what the SDK does (Transit-GO-style rider reporting, don't-trust-verify-then-trust on the server), requirements (iOS 18, Swift 6), installation (SwiftPM path or git URL of this repo with `path: ios/VehiclePositionsKit`; XcodeGen snippet for the OneBusAway app: `packages: VehiclePositionsKit: { path: ../vehicle-positions/ios/VehiclePositionsKit }` or url), host integration checklist (add `location` to `UIBackgroundModes`; `NSLocationWhenInUseUsageDescription`; call `start` in the foreground; recreate the reporter on region change; When-In-Use suffices; the blue indicator is expected), a 20-line usage example (`RideReporter(configuration:)`, `for await event in try await reporter.start(trip)`), privacy notes (random installation UUID only, positions snapped server-side), the event/end-reason reference, and testing instructions (`xcodebuild test` command from Global Constraints).

- [ ] **Step 5: Run → PASS.** Full simulator test run, zero warnings; also `swift build` from the package directory with `DEVELOPER_DIR` set (macOS host build must succeed thanks to the `#if os(iOS)` guards).

- [ ] **Step 6: Commit** — `git add ios/VehiclePositionsKit && git commit -m "feat(ios): CoreLocation source, Keychain credential store, default init and README"`

---

## End-to-end verification (after Task 18)

1. `go test ./... -race && go vet ./...` (with and without `DATABASE_URL`).
2. Smoke from Task 14 Step 5 — all six steps.
3. `cd ios/VehiclePositionsKit && xcodebuild test -scheme VehiclePositionsKit -destination 'platform=iOS Simulator,name=iPhone 17 Pro'` → `** TEST SUCCEEDED **`.
4. Optional, if time permits: a throwaway Swift script under the scratchpad that drives `RideReporter` with `URLSessionRideTransport` + `FakeLocationSource` against the local server (walking the fixture shape) to prove the wire formats agree between Go and Swift. Not committed.

## Self-Review Notes (already applied)

- **Spec coverage**: §4.1 → Task 13; §4.2 → Tasks 1–6; §4.3 → Tasks 8–9; §4.4 → Tasks 9–10; §4.5 → Task 3; §4.6–4.7 → Task 4; §4.8 → Tasks 6, 11; §4.9 → Task 7 (+ Task 10 ingest path, Task 13 retention/reap); §4.10 → Task 12; §4.11 → Task 14; §4.12 → Tasks 15–18; §6 smoke → Task 14 Step 5.
- **Deviations from the spec, deliberate**: UUID columns are `TEXT`; `VerifyInput` gains `Timezone` and `StartDate` (needed for schedule deviation); `Session` takes the tier in its constructor; `TrustedFeed.Lookup/Covers` take `now`; `RideTransport.send` takes `baseURL`; `RideReporterConfiguration` gains `sampleRetention`; the store's `FinishRide` wraps the spec's `EndRide` + `ApplyRideOutcome`; `RidePointPruner` is a separate narrow interface. The spec's `RideSummary` in Swift mirrors the server summary object (not the Go `rider.RideSummary`).
- **Type consistency checked**: `rider.Point.Timestamp` is `time.Time` everywhere; `PositionUpload.timestamp` is `Int` seconds; `Corroboration` string values (`unavailable/none/corroborated/contradicted`) match in Go (`String()`), JSON, and Swift raw values; `RideEndReason` raw values match `rider.EndReason` constants and the DB `end_reason` strings; `max_batch_size` = 12 on the server and `3` in the SDK tests (server-dictated, so tests choose a small value).
- **Known timing subtlety** (Task 14): fixture trips are scheduled at fixed clock times; the smoke widens the schedule window via env instead of faking clocks.
