# GTFS Catalog for Drivers (Phase A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an authenticated driver list GTFS routes, list a route's trips on a service date, and fetch one trip's shape, stops and absolute stop times, with the schedule loaded whenever `GTFS_STATIC_URL` is set rather than only in rider mode.

**Architecture:** The existing `rider.Index` (built from a GTFS zip by `rider.BuildIndex`) gains route metadata, headsigns, stop names and a per-route trip listing. A new `gtfsRuntime` in `package main` owns loading and refreshing the index; rider mode borrows its refresher instead of loading its own. A new `gtfsCatalog` serves three read-only JSON endpoints behind the existing driver auth middleware, registered only when an index exists.

**Tech Stack:** Go 1.25, `net/http` `ServeMux` patterns, `github.com/OneBusAway/go-gtfs` v1.1.1, testify. Tests run with `go test ./...` from the repo root; `go vet ./...` must stay clean.

**Spec:** `docs/superpowers/specs/2026-09-14-ios-driver-app-carplay-design.md` §4 (Phase A), §1.1 (decisions), §3 (non-goals).

## Global Constraints

- Go 1.25; no new module dependencies.
- Package `rider/` keeps its name; the index lives there and is shared (spec §3).
- Endpoints require a driver or admin token via `requireAuth` (rider tokens get `403`). Errors use `{"error": "..."}`. Unknown route/trip `404`; malformed `date` `400`; trip not active on the date `422 {"error":"trip not active on date"}`; no index `503 {"error":"schedule data unavailable"}`.
- Absolute times are RFC 3339 in the agency timezone, derived from `rider.ServiceDayStart(date, tz)` plus the stop-time offsets, so after-midnight trips render on the following calendar day with the agency offset.
- `direction_id` is `0`, `1`, or `null` (absent in the feed).
- Every file in `package main` follows the one-concern-per-file convention: `gtfs_wiring.go`, `gtfs_handlers.go`, each with a `_test.go`.
- Commit after each task with the message given in the task.

---

### Task 1: Index extensions — routes, headsigns, stop names, trips per route

**Files:**
- Modify: `rider/index.go` (types at lines 38-74, `BuildIndex` at 88-148, accessors after 156)
- Modify: `rider/fixture_test.go:123-125` (routes.txt) and `:149-153` (trips.txt); add a helper after `fixtureIndexEdited` (line 76)
- Modify: `rider/index_test.go` (append tests)
- Regenerate: `rider/testdata/fixture.zip`

**Interfaces:**
- Consumes: `gtfs.Static.Routes []gtfs.Route{Id, ShortName, LongName, Color, TextColor, Type RouteType(int32), SortOrder *int32}`; `gtfs.ScheduledTrip{Headsign string, DirectionId gtfs.DirectionID}` where `DirectionID_False` (raw "0") = 2, `DirectionID_True` (raw "1") = 1, `DirectionID_Unspecified` = 0; `gtfs.Stop.Name`.
- Produces:
  - `type RouteInfo struct { ID, ShortName, LongName, Color, TextColor string; Type int; SortOrder *int32 }`
  - `TripInfo.Headsign string`, `TripInfo.DirectionID int` (0, 1, or -1 when absent)
  - `StopTimeInfo.StopName string`
  - `IndexStats.Routes int`
  - `func (ix *Index) Routes() []RouteInfo` (sorted: `SortOrder` ascending with nil last, then natural-order `ShortName`, then `LongName`; only routes with at least one indexed trip)
  - `func (ix *Index) Route(id string) (RouteInfo, bool)`
  - `func (ix *Index) TripsOnRoute(routeID, serviceDate string) []*TripInfo` (active on the date, sorted by first stop departure then ID; a fresh slice each call)

- [ ] **Step 1: Extend the fixture feed**

In `rider/fixture_test.go`, replace the `routes.txt` member (lines 123-125) with:

```go
		{"routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type,route_sort_order,route_color,route_text_color\n" +
			"R1,A,1,Straight,3,2,0077C0,FFFFFF\n" +
			"R2,A,2,Loop,3,1,,\n"},
```

and the `trips.txt` member (lines 149-153) with:

```go
		{"trips.txt", "route_id,service_id,trip_id,shape_id,trip_headsign,direction_id\n" +
			"R1,WEEKDAY,T1,S1,North,0\n" +
			"R1,SAT,T2,S1,North,\n" +
			"R2,WEEKDAY,T3,S2,Loop,1\n" +
			"R1,WEEKDAY,T4,,North,0\n"},
```

Update the doc comment above `fixtureFiles` (line 108-113) to add: "R2 has route_sort_order 1 and R1 has 2, so R2 lists first; T2 has no direction_id."

Add this helper after `fixtureIndexEdited` (after line 76):

```go
// fixtureIndexEditedFiles is fixtureIndexEdited for more than one member.
func fixtureIndexEditedFiles(t *testing.T, edits map[string]string) *Index {
	t.Helper()
	files := fixtureFiles(fixtureTimezone, 1)
	for name, body := range edits {
		replaced := false
		for i := range files {
			if files[i].name == name {
				files[i].body = body
				replaced = true
			}
		}
		require.True(t, replaced, "no fixture member named %q", name)
	}
	static, err := gtfs.ParseStatic(zipFixtureFiles(t, files), gtfs.ParseStaticOptions{})
	require.NoError(t, err)
	ix, err := BuildIndex(static, "fixture", fixtureLoadedAt)
	require.NoError(t, err)
	return ix
}
```

- [ ] **Step 2: Regenerate the committed fixture and confirm existing tests still pass**

Run: `WRITE_FIXTURE=1 go test ./rider/ -run TestWriteFixture && go test ./rider/`
Expected: PASS (the fixture zip is rewritten; every existing index test still passes because column additions do not change shapes or stop times).

- [ ] **Step 3: Write the failing tests**

Append to `rider/index_test.go`:

```go
func TestBuildIndex_Routes(t *testing.T) {
	ix := fixtureIndex(t)
	routes := ix.Routes()
	require.Len(t, routes, 2)
	assert.Equal(t, "R2", routes[0].ID, "route_sort_order 1 lists before 2")
	assert.Equal(t, RouteInfo{ID: "R1", ShortName: "1", LongName: "Straight", Color: "0077C0", TextColor: "FFFFFF", Type: 3, SortOrder: ptr(int32(2))}, routes[1])
	assert.Equal(t, 2, ix.Stats().Routes)

	r, ok := ix.Route("R2")
	require.True(t, ok)
	assert.Equal(t, "Loop", r.LongName)
	assert.Nil(t, r.SortOrder)
	_, ok = ix.Route("R9")
	assert.False(t, ok)
}

func TestRoutes_NaturalOrderWithoutSortOrder(t *testing.T) {
	ix := fixtureIndexEdited(t, "routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type\n"+
		"R1,A,10,Ten,3\n"+
		"R2,A,7,Seven,3\n")
	var ids []string
	for _, r := range ix.Routes() {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"R2", "R1"}, ids, "7 sorts before 10")
}

func TestRoutes_ExcludesRoutesWithoutIndexedTrips(t *testing.T) {
	ix := fixtureIndexEdited(t, "routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type\n"+
		"R1,A,1,Straight,3\n"+
		"R2,A,2,Loop,3\n"+
		"R3,A,3,Ghost,3\n")
	assert.Len(t, ix.Routes(), 2)
	_, ok := ix.Route("R3")
	assert.False(t, ok)
}

func TestBuildIndex_HeadsignDirectionAndStopNames(t *testing.T) {
	ix := fixtureIndex(t)
	t1, _ := ix.Trip("T1")
	assert.Equal(t, "North", t1.Headsign)
	assert.Equal(t, 0, t1.DirectionID)
	assert.Equal(t, "Stop ST2", t1.StopTimes[1].StopName)
	t2, _ := ix.Trip("T2")
	assert.Equal(t, -1, t2.DirectionID, "absent direction_id")
	t3, _ := ix.Trip("T3")
	assert.Equal(t, 1, t3.DirectionID)
}

func TestTripsOnRoute(t *testing.T) {
	ix := fixtureIndex(t)
	weekday := ix.TripsOnRoute("R1", "20260902") // Wednesday
	require.Len(t, weekday, 1)
	assert.Equal(t, "T1", weekday[0].ID)
	sat := ix.TripsOnRoute("R1", "20260905")
	require.Len(t, sat, 1)
	assert.Equal(t, "T2", sat[0].ID)
	assert.Empty(t, ix.TripsOnRoute("R9", "20260902"))
	assert.Empty(t, ix.TripsOnRoute("R1", "garbage"))
}

func TestTripsOnRoute_SortedByFirstDeparture(t *testing.T) {
	// T2 moved to weekday service and scheduled before T1, with an id that
	// sorts after it: departure order must win over id order.
	ix := fixtureIndexEditedFiles(t, map[string]string{
		"trips.txt": "route_id,service_id,trip_id,shape_id,trip_headsign,direction_id\n" +
			"R1,WEEKDAY,T1,S1,North,0\n" +
			"R1,WEEKDAY,T2,S1,North,\n" +
			"R2,WEEKDAY,T3,S2,Loop,1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled\n" +
			stopTimeRow("T1", "08:00:00", "ST1", 1, "0") +
			stopTimeRow("T1", "08:10:00", "ST3", 3, "1001") +
			stopTimeRow("T2", "07:00:00", "ST1", 1, "0") +
			stopTimeRow("T2", "07:10:00", "ST3", 3, "1001") +
			stopTimeRow("T3", "25:00:00", "LP1", 1, "") +
			stopTimeRow("T3", "25:20:00", "LP1", 4, ""),
	})
	trips := ix.TripsOnRoute("R1", "20260902")
	require.Len(t, trips, 2)
	assert.Equal(t, "T2", trips[0].ID)
	assert.Equal(t, "T1", trips[1].ID)
	// TripsOnRoute hands out a fresh slice: mutating it must not touch the index.
	trips[0] = nil
	assert.Equal(t, "T2", ix.TripsOnRoute("R1", "20260902")[0].ID)
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./rider/ -run 'TestBuildIndex_Routes|TestRoutes_|TestBuildIndex_HeadsignDirectionAndStopNames|TestTripsOnRoute'`
Expected: compile errors — `RouteInfo`, `Routes`, `Route`, `TripsOnRoute`, `Headsign`, `DirectionID`, `StopName`, `Stats().Routes` undefined.

- [ ] **Step 5: Implement the index extensions**

In `rider/index.go`:

Add `"strconv"` to the imports.

Replace the `StopTimeInfo` and `TripInfo` types (lines 38-55) with:

```go
// StopTimeInfo is one scheduled stop of a trip, positioned along its shape.
type StopTimeInfo struct {
	StopID     string
	StopName   string
	Sequence   int
	AlongShape float64       // metres from the start of the shape
	Arrival    time.Duration // since service-day midnight (may exceed 24h)
	Departure  time.Duration
	Pos        LatLon
}

// TripInfo is one scheduled trip with its shape geometry and stop times.
type TripInfo struct {
	ID          string
	RouteID     string
	ServiceID   string
	Headsign    string
	DirectionID int // 0 or 1 as in trips.txt; -1 when the feed gives none
	Shape       *ShapeGeom
	StopTimes   []StopTimeInfo // sorted by Sequence
}

// RouteInfo is one GTFS route as the driver catalog lists it. Only routes
// with at least one indexed trip are kept.
type RouteInfo struct {
	ID        string
	ShortName string
	LongName  string
	Color     string // hex without '#', as in routes.txt; may be empty
	TextColor string
	Type      int    // GTFS route_type
	SortOrder *int32 // route_sort_order, nil when the feed gives none
}
```

Replace `IndexStats` (lines 57-63) with:

```go
// IndexStats summarises a loaded index.
type IndexStats struct {
	Routes   int
	Trips    int
	Shapes   int
	LoadedAt time.Time
	Source   string
}
```

Replace the `Index` struct (lines 68-74) with:

```go
// Index is an immutable snapshot of the schedule data the rider engine and
// the driver catalog need. It is safe for concurrent use; nothing in it is
// mutated after BuildIndex returns.
type Index struct {
	trips        map[string]*TripInfo
	tripIDs      []string
	routes       map[string]RouteInfo
	routeList    []RouteInfo            // Routes() order
	tripsByRoute map[string][]*TripInfo // first-departure order
	services     map[string]serviceCalendar
	tz           *time.Location
	stats        IndexStats
}
```

In `BuildIndex`, initialise the new maps in the `ix := &Index{...}` literal:

```go
	ix := &Index{
		trips:        make(map[string]*TripInfo, len(static.Trips)),
		routes:       make(map[string]RouteInfo),
		tripsByRoute: make(map[string][]*TripInfo),
		services:     make(map[string]serviceCalendar, len(static.Services)),
		tz:           tz,
	}
```

Replace the `info := &TripInfo{...}` block through `ix.tripIDs = append(...)` (lines 120-132) with:

```go
		info := &TripInfo{
			ID:          trip.ID,
			Headsign:    trip.Headsign,
			DirectionID: directionID(trip.DirectionId),
			Shape:       shape,
			StopTimes:   stopTimesAlong(trip.StopTimes, shape),
		}
		if trip.Route != nil {
			info.RouteID = trip.Route.Id
			ix.tripsByRoute[info.RouteID] = append(ix.tripsByRoute[info.RouteID], info)
		}
		if trip.Service != nil {
			info.ServiceID = trip.Service.Id
		}
		ix.trips[info.ID] = info
		ix.tripIDs = append(ix.tripIDs, info.ID)
```

After `slices.Sort(ix.tripIDs)` add:

```go
	for _, trips := range ix.tripsByRoute {
		slices.SortStableFunc(trips, func(a, b *TripInfo) int {
			if c := cmp.Compare(a.StopTimes[0].Departure, b.StopTimes[0].Departure); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
	}
	for i := range static.Routes {
		r := &static.Routes[i]
		if _, used := ix.tripsByRoute[r.Id]; !used {
			continue
		}
		info := RouteInfo{
			ID: r.Id, ShortName: r.ShortName, LongName: r.LongName,
			Color: r.Color, TextColor: r.TextColor, Type: int(r.Type), SortOrder: r.SortOrder,
		}
		ix.routes[r.Id] = info
		ix.routeList = append(ix.routeList, info)
	}
	slices.SortStableFunc(ix.routeList, compareRoutes)
```

Set `Routes: len(ix.routeList),` in the `ix.stats = IndexStats{...}` literal.

Add after `TripIDs` (line 156):

```go
// Routes returns every route with an indexed trip, in display order:
// route_sort_order ascending with unsorted routes last, then short name in
// natural order ("7" before "10"), then long name.
func (ix *Index) Routes() []RouteInfo { return slices.Clone(ix.routeList) }

// Route returns the route with the given ID, if it has an indexed trip.
func (ix *Index) Route(id string) (RouteInfo, bool) {
	r, ok := ix.routes[id]
	return r, ok
}

// TripsOnRoute returns the route's trips active on the "YYYYMMDD" service
// date, ordered by first departure. The slice is the caller's to keep.
func (ix *Index) TripsOnRoute(routeID, serviceDate string) []*TripInfo {
	var out []*TripInfo
	for _, trip := range ix.tripsByRoute[routeID] {
		if ix.ActiveOn(trip, serviceDate) {
			out = append(out, trip)
		}
	}
	return out
}
```

Add these helpers at the end of the file:

```go
// directionID maps the parser's tri-state direction onto trips.txt's 0/1,
// with -1 standing for a direction the feed did not give.
func directionID(d gtfs.DirectionID) int {
	switch d {
	case gtfs.DirectionID_False:
		return 0
	case gtfs.DirectionID_True:
		return 1
	default:
		return -1
	}
}

// compareRoutes orders routes for display: route_sort_order first (routes
// without one after every route with one), then short name in natural
// order, then long name.
func compareRoutes(a, b RouteInfo) int {
	switch {
	case a.SortOrder != nil && b.SortOrder != nil:
		if c := cmp.Compare(*a.SortOrder, *b.SortOrder); c != 0 {
			return c
		}
	case a.SortOrder != nil:
		return -1
	case b.SortOrder != nil:
		return 1
	}
	if c := compareNatural(a.ShortName, b.ShortName); c != 0 {
		return c
	}
	return cmp.Compare(a.LongName, b.LongName)
}

// compareNatural compares two names so that a leading number sorts
// numerically: "7" < "10" < "10A" < "A".
func compareNatural(a, b string) int {
	an, arest, aok := leadingNumber(a)
	bn, brest, bok := leadingNumber(b)
	switch {
	case aok && bok:
		if c := cmp.Compare(an, bn); c != 0 {
			return c
		}
		return cmp.Compare(arest, brest)
	case aok:
		return -1
	case bok:
		return 1
	}
	return cmp.Compare(a, b)
}

// leadingNumber splits a leading run of ASCII digits off s.
func leadingNumber(s string) (n int, rest string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, s, false
	}
	return n, s[i:], true
}
```

In `stopTimesAlong`, set the stop name: change the `out = append(out, StopTimeInfo{` literal to include `StopName: stopName(st.Stop),` and add next to `stopID`:

```go
func stopName(stop *gtfs.Stop) string {
	if stop == nil {
		return ""
	}
	return stop.Name
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./rider/ && go vet ./rider/`
Expected: PASS, including `TestWriteFixture` (the zip was regenerated in Step 2 from the same builder).

- [ ] **Step 7: Commit**

```bash
git add rider/index.go rider/index_test.go rider/fixture_test.go rider/testdata/fixture.zip
git commit -m "feat(rider): index routes, headsigns, stop names and trips per route"
```

---

### Task 2: GTFS runtime, decoupled from rider mode

**Files:**
- Create: `gtfs_wiring.go`, `gtfs_wiring_test.go`
- Modify: `rider_wiring.go:165-213` (`newRiderRuntime`), `rider_wiring.go:56-92` (`riderConfigFromEnv`, unchanged behaviour but read it)
- Modify: `rider_wiring_test.go:87-107`
- Modify: `main.go:233-247`

**Interfaces:**
- Consumes: `rider.LoadIndex(ctx, source, client, now) (*rider.Index, error)`, `rider.NewRefresher(initial, load) *rider.Refresher`, `(*rider.Refresher).Start(ctx, every)`, `(*rider.Refresher).Current() *rider.Index`.
- Produces:
  - `type gtfsRuntime struct` with `Index() *rider.Index`, `Refresher() *rider.Refresher`, `Stop()`
  - `func newGTFSRuntime(ctx context.Context, source string, refresh time.Duration) (*gtfsRuntime, error)`
  - `newRiderRuntime(ctx context.Context, cfg riderConfig, refresher *rider.Refresher, store riderStore, jwtSecret []byte, trustProxy bool, tracker *Tracker) (*riderRuntime, error)` — the refresher is passed in; the runtime no longer loads GTFS or runs the refresh loop.

- [ ] **Step 1: Write the failing tests**

Create `gtfs_wiring_test.go`:

```go
package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGTFSRuntime_LoadsIndex(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := newGTFSRuntime(ctx, "rider/testdata/fixture.zip", time.Hour)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, 3, rt.Index().Stats().Trips)
	assert.Equal(t, 2, rt.Index().Stats().Routes)
	assert.Same(t, rt.Index(), rt.Refresher().Current())
}

func TestNewGTFSRuntime_FailsWhenTheFeedIsMissing(t *testing.T) {
	_, err := newGTFSRuntime(context.Background(), "does/not/exist.zip", time.Hour)
	assert.Error(t, err)
}

func TestNewGTFSRuntime_StopReturns(t *testing.T) {
	rt, err := newGTFSRuntime(context.Background(), "rider/testdata/fixture.zip", time.Hour)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { rt.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}
```

In `rider_wiring_test.go`, replace `TestNewRiderRuntime_LoadsIndexAndEndsStaleRides` (lines 87-107) with:

```go
func TestNewRiderRuntime_EndsStaleRidesAndSharesTheIndex(t *testing.T) {
	store := newFakeRiderStore()
	r, _, _ := store.RegisterRider(context.Background(), "inst", "ios", "x", "1")
	require.NoError(t, store.StartRide(context.Background(), &Ride{ID: "stale", RiderID: r.ID, TripID: "T1", StartDate: "20260902"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gt, err := newGTFSRuntime(ctx, "rider/testdata/fixture.zip", time.Hour)
	require.NoError(t, err)
	defer gt.Stop()

	cfg := riderConfig{Enabled: true, GTFSSource: "rider/testdata/fixture.zip", GTFSRefresh: time.Hour, TrustedPoll: time.Hour,
		TrustedMaxAge: 5 * time.Minute, JWTTTL: time.Hour, PointRetention: time.Hour, Thresholds: rider.DefaultThresholds()}
	rt, err := newRiderRuntime(ctx, cfg, gt.Refresher(), store, testSecret, false, nil)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, "ended", store.rides["stale"].Status)
	assert.Equal(t, "server_restart", store.rides["stale"].EndReason)
	assert.Same(t, gt.Index(), rt.refresher.Current(), "rider mode serves the shared index")
	assert.False(t, rt.trusted.Configured())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test . -run 'TestNewGTFSRuntime|TestNewRiderRuntime'`
Expected: compile errors — `newGTFSRuntime` undefined and `newRiderRuntime` has the old signature.

- [ ] **Step 3: Create the GTFS runtime and rewire rider mode**

Create `gtfs_wiring.go`:

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gtfsRuntime owns the schedule: the index loaded at startup and the
// background refresh that replaces it. It stands on its own because two
// consumers read the schedule — rider mode and the driver catalog — and
// neither should have to own it for the other. The returned runtime must be
// Stopped.
type gtfsRuntime struct {
	refresher *rider.Refresher
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// newGTFSRuntime loads the feed at source (a failure aborts startup: nothing
// downstream can run without a schedule) and starts reloading it every
// `refresh`. A failed reload keeps the previous index and logs.
func newGTFSRuntime(ctx context.Context, source string, refresh time.Duration) (*gtfsRuntime, error) {
	// http.DefaultClient carries no timeout of its own, which is what the
	// download wants: it applies its own, and a static feed is far too large
	// for a short one.
	client := http.DefaultClient

	index, err := rider.LoadIndex(ctx, source, client, time.Now())
	if err != nil {
		return nil, err
	}
	stats := index.Stats()
	slog.Info("gtfs: loaded schedule", "source", source, "routes", stats.Routes, "trips", stats.Trips,
		"shapes", stats.Shapes, "timezone", index.Timezone().String())

	refresher := rider.NewRefresher(index, func(ctx context.Context) (*rider.Index, error) {
		return rider.LoadIndex(ctx, source, client, time.Now())
	})

	runCtx, cancel := context.WithCancel(ctx)
	rt := &gtfsRuntime{refresher: refresher, cancel: cancel}
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		refresher.Start(runCtx, refresh)
	}()
	return rt, nil
}

// Index returns the schedule in force right now.
func (rt *gtfsRuntime) Index() *rider.Index { return rt.refresher.Current() }

// Refresher hands the schedule to a consumer that must always read the
// current index rather than hold one.
func (rt *gtfsRuntime) Refresher() *rider.Refresher { return rt.refresher }

// Stop ends the refresh loop and waits for it. It must be called exactly once.
func (rt *gtfsRuntime) Stop() {
	rt.cancel()
	rt.wg.Wait()
}
```

In `rider_wiring.go`, change `newRiderRuntime` (lines 165-213):

- Signature becomes `func newRiderRuntime(ctx context.Context, cfg riderConfig, refresher *rider.Refresher, store riderStore, jwtSecret []byte, trustProxy bool, tracker *Tracker) (*riderRuntime, error)`.
- Delete the `client := http.DefaultClient` line, the `index, err := rider.LoadIndex(...)` block and its `slog.Info("rider: loaded GTFS", ...)`, and the `refresher := rider.NewRefresher(...)` block. Keep the `EndAllActiveRides` block.
- `trusted := rider.NewTrustedFeed(cfg.TrustedURLs, http.DefaultClient, cfg.TrustedMaxAge)` (it still needs a client).
- `agg := rider.NewAggregator(cfg.Thresholds, refresher.Current().Timezone())`.
- Delete `rt.goroutine(func() { refresher.Start(runCtx, cfg.GTFSRefresh) })` — the GTFS runtime runs the refresh now.
- Update the doc comment: "newRiderRuntime brings rider mode up (spec §4.1) on a schedule the GTFS runtime already loaded: end every ride left active by the previous process, then wire the engine, the service and the background tickers."
- Remove any import that becomes unused (`net/http` stays, for `NewTrustedFeed`).

In `main.go`, replace lines 233-247 with:

```go
	riderCfg, err := riderConfigFromEnv()
	if err != nil {
		slog.Error("invalid rider mode configuration", "error", err)
		os.Exit(1)
	}
	// The schedule loads whenever there is one to load: the driver catalog
	// serves it on its own, and rider mode verifies against it when enabled.
	var gtfs *gtfsRuntime
	if riderCfg.GTFSSource != "" {
		gtfs, err = newGTFSRuntime(ctx, riderCfg.GTFSSource, riderCfg.GTFSRefresh)
		if err != nil {
			slog.Error("failed to load GTFS", "source", riderCfg.GTFSSource, "error", err)
			os.Exit(1)
		}
		defer gtfs.Stop()
	}
	var riderSvc *riderService
	if riderCfg.Enabled {
		rt, err := newRiderRuntime(ctx, riderCfg, gtfs.Refresher(), store, jwtSecret, trustProxyHeaders(), tracker)
		if err != nil {
			slog.Error("failed to start rider mode", "error", err)
			os.Exit(1)
		}
		defer rt.Stop()
		riderSvc = rt.svc
	}
```

(`riderConfigFromEnv` already refuses `Enabled` without `GTFSSource`, so `gtfs` is non-nil inside the `if riderCfg.Enabled` block.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go vet ./... && go test .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gtfs_wiring.go gtfs_wiring_test.go rider_wiring.go rider_wiring_test.go main.go
git commit -m "feat: load GTFS whenever GTFS_STATIC_URL is set, shared by rider mode"
```

---

### Task 3: Catalog endpoints

**Files:**
- Create: `gtfs_handlers.go`, `gtfs_handlers_test.go`
- Modify: `main.go:69-121` (`newMux`), `main.go:127-131` (`newHandler`), `main.go` (the `newHandler(...)` call after the rider block)
- Modify: call sites of `newMux`/`newHandler`: `route_wiring_test.go:171,227,286,370`, `rider_wiring_test.go:110,128`, `handler_composition_test.go:21`
- Modify: `route_wiring_test.go` (append a test)

**Interfaces:**
- Consumes: `rider.Index` methods from Task 1; `rider.ServiceDayStart(date string, loc *time.Location) (time.Time, error)`; `(*rider.Index).ServiceDate(now)`, `.ServiceDateFor(trip, now)`, `.ActiveOn(trip, date)`, `.Timezone()`; `serviceDatePattern` (`rider_handlers.go:56`); `writeJSON`; `requireAuth`.
- Produces:
  - `type gtfsCatalog struct { index func() *rider.Index; thresholds rider.Thresholds; now func() time.Time }`
  - `func newGTFSCatalog(index func() *rider.Index, thresholds rider.Thresholds) *gtfsCatalog`
  - `func registerGTFSRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler, c *gtfsCatalog)`
  - `newMux(..., riderSvc *riderService, catalog *gtfsCatalog)` and `newHandler(..., riderSvc *riderService, catalog *gtfsCatalog)`; a nil catalog registers nothing.
  - JSON shapes exactly as spec §4.3.

- [ ] **Step 1: Write the failing handler tests**

Create `gtfs_handlers_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// catalogNow is a Wednesday in the fixture's calendar, mid-morning Pacific.
var catalogNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func testCatalog(t *testing.T) *gtfsCatalog {
	t.Helper()
	ix, err := rider.LoadIndex(context.Background(), "rider/testdata/fixture.zip", nil, catalogNow)
	require.NoError(t, err)
	c := newGTFSCatalog(func() *rider.Index { return ix }, rider.DefaultThresholds())
	c.now = func() time.Time { return catalogNow }
	return c
}

func catalogGet(t *testing.T, h http.Handler, path string, pathValues map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return rec.Code, body
}

func TestCatalogRoutes(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleRoutes(), "/api/v1/gtfs/routes", nil)
	require.Equal(t, http.StatusOK, code)
	routes := body["routes"].([]any)
	require.Len(t, routes, 2)
	assert.Equal(t, map[string]any{"id": "R2", "short_name": "2", "long_name": "Loop", "color": "", "text_color": "", "type": float64(3)}, routes[0])
	assert.Equal(t, "R1", routes[1].(map[string]any)["id"])
	assert.Equal(t, "0077C0", routes[1].(map[string]any)["color"])
}

func TestCatalogRouteTrips_DefaultsToTodaysServiceDate(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips", map[string]string{"route_id": "R1"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "R1", body["route_id"])
	assert.Equal(t, "20260902", body["service_date"])
	assert.Equal(t, "America/Los_Angeles", body["timezone"])
	trips := body["trips"].([]any)
	require.Len(t, trips, 1)
	assert.Equal(t, map[string]any{
		"id": "T1", "headsign": "North", "direction_id": float64(0),
		"starts_at": "2026-09-02T08:00:00-07:00", "ends_at": "2026-09-02T08:10:00-07:00",
		"first_stop": "Stop ST1", "last_stop": "Stop ST3",
	}, trips[0])
}

func TestCatalogRouteTrips_ExplicitDateAndNullDirection(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=20260905", map[string]string{"route_id": "R1"})
	require.Equal(t, http.StatusOK, code)
	trips := body["trips"].([]any)
	require.Len(t, trips, 1)
	assert.Equal(t, "T2", trips[0].(map[string]any)["id"])
	assert.Nil(t, trips[0].(map[string]any)["direction_id"])
}

func TestCatalogRouteTrips_Errors(t *testing.T) {
	c := testCatalog(t)
	code, body := catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R9/trips", map[string]string{"route_id": "R9"})
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "unknown route", body["error"])

	code, body = catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=2026-09-02", map[string]string{"route_id": "R1"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "date must be YYYYMMDD", body["error"])

	code, body = catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=20261399", map[string]string{"route_id": "R1"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "date must be YYYYMMDD", body["error"])

	// A date with no service is an empty list, not an error.
	code, body = catalogGet(t, c.handleRouteTrips(), "/api/v1/gtfs/routes/R1/trips?date=20270106", map[string]string{"route_id": "R1"})
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, body["trips"])
}

func TestCatalogTrip_AfterMidnightLoop(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleTrip(), "/api/v1/gtfs/trips/T3?date=20260902", map[string]string{"trip_id": "T3"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "T3", body["id"])
	assert.Equal(t, "R2", body["route_id"])
	assert.Equal(t, "Loop", body["headsign"])
	assert.Equal(t, float64(1), body["direction_id"])
	assert.Equal(t, "20260902", body["service_date"])
	assert.Equal(t, "America/Los_Angeles", body["timezone"])
	assert.Equal(t, map[string]any{"short_name": "2", "long_name": "Loop", "color": "", "text_color": ""}, body["route"])

	shape := body["shape"].(map[string]any)
	assert.InDelta(t, 2000, shape["length_m"], 30)
	points := shape["points"].([]any)
	require.Len(t, points, 5)
	assert.Equal(t, []any{47.6, -122.33}, points[0])

	stops := body["stops"].([]any)
	require.Len(t, stops, 4)
	first := stops[0].(map[string]any)
	assert.Equal(t, "LP1", first["id"])
	assert.Equal(t, "Stop LP1", first["name"])
	assert.Equal(t, float64(1), first["sequence"])
	assert.Equal(t, "2026-09-03T01:00:00-07:00", first["arrival_at"], "25:00 on the 2nd is 01:00 on the 3rd")
	assert.Equal(t, "2026-09-03T01:00:00-07:00", first["departure_at"])
	assert.InDelta(t, 0, first["along_shape_m"], 2)
	last := stops[3].(map[string]any)
	assert.InDelta(t, shape["length_m"].(float64), last["along_shape_m"].(float64), 5)

	assert.Equal(t, map[string]any{"max_shape_distance_m": float64(60), "schedule_early_s": float64(900), "schedule_late_s": float64(5400)}, body["thresholds"])
}

func TestCatalogTrip_DefaultsToTheTripsServiceDate(t *testing.T) {
	code, body := catalogGet(t, testCatalog(t).handleTrip(), "/api/v1/gtfs/trips/T1", map[string]string{"trip_id": "T1"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "20260902", body["service_date"])
	stops := body["stops"].([]any)
	assert.Equal(t, "2026-09-02T08:00:00-07:00", stops[0].(map[string]any)["departure_at"])
}

func TestCatalogTrip_Errors(t *testing.T) {
	c := testCatalog(t)
	code, body := catalogGet(t, c.handleTrip(), "/api/v1/gtfs/trips/T9", map[string]string{"trip_id": "T9"})
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "unknown trip", body["error"])

	code, body = catalogGet(t, c.handleTrip(), "/api/v1/gtfs/trips/T1?date=20260905", map[string]string{"trip_id": "T1"})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, "trip not active on date", body["error"])

	code, body = catalogGet(t, c.handleTrip(), "/api/v1/gtfs/trips/T1?date=nope", map[string]string{"trip_id": "T1"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "date must be YYYYMMDD", body["error"])
}

func TestCatalog_NoIndexIs503(t *testing.T) {
	c := newGTFSCatalog(func() *rider.Index { return nil }, rider.DefaultThresholds())
	for _, h := range []http.Handler{c.handleRoutes(), c.handleRouteTrips(), c.handleTrip()} {
		code, body := catalogGet(t, h, "/x", map[string]string{"route_id": "R1", "trip_id": "T1"})
		assert.Equal(t, http.StatusServiceUnavailable, code)
		assert.Equal(t, "schedule data unavailable", body["error"])
	}
}
```

Append to `route_wiring_test.go`:

```go
func TestGTFSRoutes_RegisteredOnlyWithACatalog(t *testing.T) {
	without := newMux(&noopStore{}, nil, nil, testSecret, time.Time{}, nil, false, nil, nil)
	rec := httptest.NewRecorder()
	without.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/gtfs/routes", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	ix, err := rider.LoadIndex(context.Background(), "rider/testdata/fixture.zip", nil, time.Now())
	require.NoError(t, err)
	catalog := newGTFSCatalog(func() *rider.Index { return ix }, rider.DefaultThresholds())
	with := newMux(&noopStore{}, nil, nil, testSecret, time.Time{}, nil, false, nil, catalog)

	driverTok, _ := generateJWT(&User{ID: 1, Email: "d@test.com", Role: "driver"}, testSecret)
	riderTok, _ := generateRiderJWT("rider-1", testSecret, time.Hour)
	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"driver", driverTok, http.StatusOK},
		{"rider token is refused", riderTok, http.StatusForbidden},
		{"no token", "", http.StatusUnauthorized},
	}
	for _, path := range []string{"/api/v1/gtfs/routes", "/api/v1/gtfs/routes/R1/trips", "/api/v1/gtfs/trips/T1"} {
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			with.ServeHTTP(rec, req)
			assert.Equal(t, tc.want, rec.Code, "%s %s", tc.name, path)
		}
	}
}
```

(Check the imports at the top of `route_wiring_test.go`; add `"context"` and `"github.com/OneBusAway/vehicle-positions/rider"` if missing. `generateRiderJWT` lives in `rider_auth.go`; confirm its signature with `grep -n 'func generateRiderJWT' rider_auth.go` and adjust the call if it differs.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test . -run 'TestCatalog|TestGTFSRoutes'`
Expected: compile errors — `newGTFSCatalog`, handler methods and the new `newMux` arity undefined.

- [ ] **Step 3: Implement the catalog**

Create `gtfs_handlers.go`:

```go
package main

import (
	"net/http"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gtfsCatalog serves the schedule to drivers (spec §4.3): the routes, a
// route's trips on a service date, and one trip's geometry and stop times
// with absolute times. It reads the index through a function so a refresh
// swapped in since the server started is what it serves.
type gtfsCatalog struct {
	index      func() *rider.Index
	thresholds rider.Thresholds
	now        func() time.Time
}

func newGTFSCatalog(index func() *rider.Index, thresholds rider.Thresholds) *gtfsCatalog {
	return &gtfsCatalog{index: index, thresholds: thresholds, now: time.Now}
}

// registerGTFSRoutes mounts the catalog behind the driver auth middleware.
func registerGTFSRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler, c *gtfsCatalog) {
	mux.Handle("GET /api/v1/gtfs/routes", auth(c.handleRoutes()))
	mux.Handle("GET /api/v1/gtfs/routes/{route_id}/trips", auth(c.handleRouteTrips()))
	mux.Handle("GET /api/v1/gtfs/trips/{trip_id}", auth(c.handleTrip()))
}

type catalogRoute struct {
	ID        string `json:"id"`
	ShortName string `json:"short_name"`
	LongName  string `json:"long_name"`
	Color     string `json:"color"`
	TextColor string `json:"text_color"`
	Type      int    `json:"type"`
}

type catalogRoutesResponse struct {
	Routes []catalogRoute `json:"routes"`
}

type catalogTripSummary struct {
	ID          string    `json:"id"`
	Headsign    string    `json:"headsign"`
	DirectionID *int      `json:"direction_id"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	FirstStop   string    `json:"first_stop"`
	LastStop    string    `json:"last_stop"`
}

type catalogRouteTripsResponse struct {
	RouteID     string               `json:"route_id"`
	ServiceDate string               `json:"service_date"`
	Timezone    string               `json:"timezone"`
	Trips       []catalogTripSummary `json:"trips"`
}

type catalogTripRoute struct {
	ShortName string `json:"short_name"`
	LongName  string `json:"long_name"`
	Color     string `json:"color"`
	TextColor string `json:"text_color"`
}

type catalogShape struct {
	LengthM float64      `json:"length_m"`
	Points  [][2]float64 `json:"points"` // [lat, lon]
}

type catalogStop struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Sequence    int       `json:"sequence"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	AlongShapeM float64   `json:"along_shape_m"`
	ArrivalAt   time.Time `json:"arrival_at"`
	DepartureAt time.Time `json:"departure_at"`
}

type catalogThresholds struct {
	MaxShapeDistanceM float64 `json:"max_shape_distance_m"`
	ScheduleEarlyS    int     `json:"schedule_early_s"`
	ScheduleLateS     int     `json:"schedule_late_s"`
}

type catalogTripResponse struct {
	ID          string            `json:"id"`
	RouteID     string            `json:"route_id"`
	Headsign    string            `json:"headsign"`
	DirectionID *int              `json:"direction_id"`
	ServiceDate string            `json:"service_date"`
	Timezone    string            `json:"timezone"`
	Route       catalogTripRoute  `json:"route"`
	Shape       catalogShape      `json:"shape"`
	Stops       []catalogStop     `json:"stops"`
	Thresholds  catalogThresholds `json:"thresholds"`
}

func (c *gtfsCatalog) handleRoutes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ix, ok := c.currentIndex(w)
		if !ok {
			return
		}
		routes := ix.Routes()
		out := catalogRoutesResponse{Routes: make([]catalogRoute, 0, len(routes))}
		for _, rt := range routes {
			out.Routes = append(out.Routes, catalogRoute{
				ID: rt.ID, ShortName: rt.ShortName, LongName: rt.LongName,
				Color: rt.Color, TextColor: rt.TextColor, Type: rt.Type,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (c *gtfsCatalog) handleRouteTrips() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ix, ok := c.currentIndex(w)
		if !ok {
			return
		}
		routeID := r.PathValue("route_id")
		if _, ok := ix.Route(routeID); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown route"})
			return
		}
		date := r.URL.Query().Get("date")
		if date == "" {
			date = ix.ServiceDate(c.now())
		}
		dayStart, ok := c.serviceDay(w, ix, date)
		if !ok {
			return
		}

		trips := ix.TripsOnRoute(routeID, date)
		out := catalogRouteTripsResponse{RouteID: routeID, ServiceDate: date, Timezone: ix.Timezone().String(),
			Trips: make([]catalogTripSummary, 0, len(trips))}
		for _, trip := range trips {
			first, last := trip.StopTimes[0], trip.StopTimes[len(trip.StopTimes)-1]
			out.Trips = append(out.Trips, catalogTripSummary{
				ID: trip.ID, Headsign: trip.Headsign, DirectionID: directionJSON(trip.DirectionID),
				StartsAt: dayStart.Add(first.Departure), EndsAt: dayStart.Add(last.Arrival),
				FirstStop: first.StopName, LastStop: last.StopName,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (c *gtfsCatalog) handleTrip() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ix, ok := c.currentIndex(w)
		if !ok {
			return
		}
		trip, ok := ix.Trip(r.PathValue("trip_id"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown trip"})
			return
		}
		// No date named means the one this trip actually runs on, which for
		// an after-midnight departure is not always the one the 03:00 cutoff
		// derives (the same rule rider start applies).
		date := r.URL.Query().Get("date")
		if date == "" {
			date = ix.ServiceDateFor(trip, c.now())
		}
		dayStart, ok := c.serviceDay(w, ix, date)
		if !ok {
			return
		}
		if !ix.ActiveOn(trip, date) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "trip not active on date"})
			return
		}
		route, _ := ix.Route(trip.RouteID)

		points := make([][2]float64, len(trip.Shape.Points))
		for i, p := range trip.Shape.Points {
			points[i] = [2]float64{p.Lat, p.Lon}
		}
		stops := make([]catalogStop, 0, len(trip.StopTimes))
		for _, st := range trip.StopTimes {
			stops = append(stops, catalogStop{
				ID: st.StopID, Name: st.StopName, Sequence: st.Sequence,
				Lat: st.Pos.Lat, Lon: st.Pos.Lon, AlongShapeM: st.AlongShape,
				ArrivalAt: dayStart.Add(st.Arrival), DepartureAt: dayStart.Add(st.Departure),
			})
		}
		writeJSON(w, http.StatusOK, catalogTripResponse{
			ID: trip.ID, RouteID: trip.RouteID, Headsign: trip.Headsign, DirectionID: directionJSON(trip.DirectionID),
			ServiceDate: date, Timezone: ix.Timezone().String(),
			Route: catalogTripRoute{ShortName: route.ShortName, LongName: route.LongName, Color: route.Color, TextColor: route.TextColor},
			Shape: catalogShape{LengthM: trip.Shape.Length, Points: points},
			Stops: stops,
			Thresholds: catalogThresholds{
				MaxShapeDistanceM: c.thresholds.MaxShapeDistance,
				ScheduleEarlyS:    int(c.thresholds.ScheduleEarly.Seconds()),
				ScheduleLateS:     int(c.thresholds.ScheduleLate.Seconds()),
			},
		})
	}
}

// currentIndex answers 503 when there is no schedule to serve, which can only
// happen between a failed load and the process exiting; it is here so no
// handler dereferences a nil index.
func (c *gtfsCatalog) currentIndex(w http.ResponseWriter) (*rider.Index, bool) {
	ix := c.index()
	if ix == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "schedule data unavailable"})
		return nil, false
	}
	return ix, true
}

// serviceDay validates a "YYYYMMDD" date and returns the instant its service
// day starts, answering 400 for anything else. A date that matches the
// pattern but names no real day ("20261399") is malformed too.
func (c *gtfsCatalog) serviceDay(w http.ResponseWriter, ix *rider.Index, date string) (time.Time, bool) {
	if !serviceDatePattern.MatchString(date) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be YYYYMMDD"})
		return time.Time{}, false
	}
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be YYYYMMDD"})
		return time.Time{}, false
	}
	return dayStart, true
}

// directionJSON renders the index's -1 sentinel as JSON null.
func directionJSON(d int) *int {
	if d < 0 {
		return nil
	}
	return &d
}
```

Note: `time.ParseInLocation("20060102", "20261399", loc)` returns an error for month 13, which is what makes the `20261399` case a `400`.

In `main.go`:

- `newMux` signature gains a trailing `catalog *gtfsCatalog`; after the rider block (line 118) add:

```go
	// The GTFS catalog exists whenever a schedule is loaded, rider mode or
	// not; without one its routes are not registered (404).
	if catalog != nil {
		registerGTFSRoutes(mux, authMiddleware, catalog)
	}
```

- `newHandler` signature gains a trailing `catalog *gtfsCatalog` and passes it to `newMux`.
- In `main()`, after the rider block, build the catalog and pass it:

```go
	var catalog *gtfsCatalog
	if gtfs != nil {
		catalog = newGTFSCatalog(gtfs.Index, riderCfg.Thresholds)
	}
```

and add `, catalog` to the `newHandler(...)` call.

Update every call site to the new arity: `route_wiring_test.go:171,227,286,370` and `rider_wiring_test.go:110,128` append `, nil` to `newMux(...)`; `handler_composition_test.go:21` appends `, nil` to `newHandler(...)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gtfs_handlers.go gtfs_handlers_test.go main.go route_wiring_test.go rider_wiring_test.go handler_composition_test.go
git commit -m "feat: serve a GTFS catalog to drivers: routes, trips on a date, trip geometry"
```

---

### Task 4: Documentation

**Files:**
- Modify: `README.md` (the "Rider mode" section: `GTFS_STATIC_URL` row and the paragraph "Turn it on by setting…"; the "Non-Goals" list item "iOS driver app")
- Modify: `ARCHITECTURE.md` (§8 Rider Mode: add a subsection)
- Modify: `docs/development.md` ("API Sanity Checks" section)

- [ ] **Step 1: README**

In the paragraph beginning "Turn it on by setting `RIDER_MODE_ENABLED=true`", append this paragraph after it:

```markdown
`GTFS_STATIC_URL` on its own, without rider mode, also loads the schedule and
turns on the **driver GTFS catalog**: three read-only endpoints the driver apps
use to pick a trip and draw its route. They take a driver or admin token and
are not registered (`404`) when no schedule is configured.

| Method + path | Purpose |
|---|---|
| `GET /api/v1/gtfs/routes` | Routes with at least one trip, in display order. |
| `GET /api/v1/gtfs/routes/{route_id}/trips?date=YYYYMMDD` | The route's trips active on a service date (default: today's), with absolute start and end times. |
| `GET /api/v1/gtfs/trips/{trip_id}?date=YYYYMMDD` | One trip's shape, stops with absolute times, and the adherence thresholds the server applies. |
```

In the configuration table, change the `GTFS_STATIC_URL` row's Purpose to: `GTFS zip URL or path. Required when rider mode is enabled; on its own it enables the driver GTFS catalog.`

In "Non-Goals", change the "iOS driver app" bullet to: `- iOS driver app for the GSoC deliverable (the target user base overwhelmingly uses Android). An iOS driver app with CarPlay was added later by the project owner; see docs/superpowers/specs/2026-09-14-ios-driver-app-carplay-design.md`

- [ ] **Step 2: ARCHITECTURE.md**

At the end of §8 add:

```markdown
### 8.x GTFS catalog for drivers

The schedule index (`rider.Index`) is loaded by `gtfs_wiring.go` whenever
`GTFS_STATIC_URL` is set, independently of rider mode, and refreshed on
`GTFS_STATIC_REFRESH`. Rider mode borrows the refresher. `gtfs_handlers.go`
serves the index to drivers as `GET /api/v1/gtfs/routes`,
`GET /api/v1/gtfs/routes/{route_id}/trips` and `GET /api/v1/gtfs/trips/{trip_id}`
behind `requireAuth`. Absolute stop times are `rider.ServiceDayStart(date, tz)`
plus the `stop_times.txt` offsets, so after-midnight trips land on the next
calendar day. The trip payload carries the server's `RIDER_MAX_SHAPE_DISTANCE`
and schedule window so a client computing adherence locally applies the same
numbers.
```

- [ ] **Step 3: development.md**

Under "API Sanity Checks" add:

```markdown
### Browse the GTFS catalog

With `GTFS_STATIC_URL=rider/testdata/fixture.zip` and a driver token in `$TOKEN`:

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/gtfs/routes | jq
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/api/v1/gtfs/routes/R1/trips' | jq
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/api/v1/gtfs/trips/T1' | jq '.stops[0], .thresholds'
```
```

- [ ] **Step 4: Commit**

```bash
git add README.md ARCHITECTURE.md docs/development.md
git commit -m "docs: describe the driver GTFS catalog and GTFS_STATIC_URL on its own"
```
