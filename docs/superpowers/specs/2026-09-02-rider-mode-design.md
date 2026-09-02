# Rider Mode v1 — Design Spec

**Date:** 2026-09-02
**Status:** Approved for implementation (autonomous run; user pre-approved the pipeline and asked for no further questions)
**Scope:** A crowdsourced, low-trust vehicle-position ingestion mode for the `vehicle-positions` server, plus an iOS 18+ SwiftPM SDK (`VehiclePositionsKit`) that rider-facing apps (OneBusAway iOS first) use to report their position while aboard a transit vehicle.

---

## 1. Overview

Today the server (commit `81e7433`) ingests positions from **trusted, authenticated drivers** who are assigned to known vehicles, and republishes them as a GTFS-Realtime VehiclePositions feed. Every report is believed; the only defenses are JSON validation and a per-driver rate limit.

This spec adds a second ingestion path: **riders**. A rider is an anonymous phone in the OneBusAway app (or any other "vendor" app built on the SDK) that says "I am on trip T right now" and streams GPS fixes for the duration of the ride. Rider data is untrusted by construction: the phone could be on the wrong bus, in a car on a parallel street, spoofing GPS, or lying about the trip. The server therefore treats every rider report the way Transit's GO treats its riders: **don't trust, verify, then trust.**

Verification has three legs, each independent and each cheap:

1. **Static plausibility** — the fix must lie on the trip's GTFS shape, move monotonically along it at a bus-like speed, and sit inside a schedule-adherence window for that trip on that service date.
2. **Corroboration** — a trusted GTFS-RT VehiclePositions feed (the agency's own AVL) is polled continuously. When it has a vehicle on the same trip instance, the rider's along-shape position is compared to it. Agreement raises trust; disagreement ends the ride and penalizes the rider.
3. **Reputation** — riders accrue a score across rides. Corroborated rides raise it, contradicted rides lower it. Trusted riders' verified rides publish immediately; new riders' rides publish only once corroborated during the ride, or when two independent riders agree.

The output is a **supplement**: rider-derived positions are published only for trip instances the trusted feed is *not* currently reporting. The trusted feed always wins when present. Consumers see the same `GET /gtfs-rt/vehicle-positions` feed they see today, with rider entities namespaced and labelled, or can filter to one source.

The inspiration is Transit's GO ([help article](https://help.transitapp.com/article/549-how-to-use-go)): the rider taps GO on a trip they are about to board, the phone shares "the position of the vehicle" (never the rider's identity) "only while GO is active", and sharing "ends automatically when you've reached your destination." Transit reports roughly 5% battery for a 20-minute trip and under 100 KB of data; those are our budgets too.

### 1.1 Separate binary or a mode? — Decision: a mode, with the engine in its own package

Three options were weighed:

- **A. Separate binary (`cmd/rider`)** — cleanest deployment story, but every server file lives in `package main` at the repo root and is not importable. A second binary would first require extracting store, auth, tracker, feed building, proxy handling and the admin UI into packages: a large, unrelated refactor with its own risk.
- **B. Same binary, new files in `package main`** — matches the repo's "one concern per file in main" convention but would add roughly a dozen files of pure geometry/verification logic to an already 60-file root, and would tangle testable pure logic with HTTP and DB concerns.
- **C. Same binary, additive mode, engine in a dedicated importable package `rider/`** (recommended, chosen). HTTP handlers, store methods, auth middleware and wiring stay in `package main` following existing conventions. Everything that needs no database and no HTTP — GTFS index, shape projection, trusted-feed snapshot, per-point verifier, ride state machine, reputation policy, aggregator — lives in `rider/` with table-driven unit tests. The mode is enabled by configuration; a deployment can run driver mode only, rider mode only, or both in one process. If rider mode later needs to scale independently, `rider/` is already the seam for a `cmd/rider` extraction.

Shared by both modes: PostgreSQL, migrations, JWT secret and `parseSessionToken`, rate-limiter patterns, `buildFeed`, request logging, CSRF wrapper, admin auth, Docker image.

---

## 2. Goals

1. Riders can register anonymously, start a ride on a GTFS trip instance, stream batched positions, and end the ride; all over JSON with a long-lived rider JWT.
2. Every rider point is verified against GTFS static data (shape, schedule, calendar) and, when available, a trusted GTFS-RT VehiclePositions feed.
3. A ride, and the rider behind it, moves through explicit trust states; only publishable rides reach the feed.
4. The GTFS-RT feed gains rider-derived entities for trip instances absent from the trusted feed, snapped to the shape, with correct trip descriptors and a visible source label. Driver-reported entities are unchanged.
5. The iOS SDK does the phone side end to end: registration, background location capture with `CLLocationUpdate.liveUpdates` and `CLBackgroundActivitySession`, batching and retrying uploads, reacting to server verdicts, and ending rides automatically.
6. Operators can see rider-mode health (GTFS load, trusted-feed freshness, active/publishable rides, rider tiers) from an admin JSON endpoint.
7. The whole loop can be exercised locally with a simulator that replays GTFS shapes as fake riders.

## 3. Non-Goals

- **App Attest / device integrity.** The registration payload reserves an `attestation` field and the `riders` table has an `attested` column, but v1 does not verify Apple attestation objects (CBOR + X.509 chain validation is a project of its own). Reputation and corroboration carry the trust load in v1.
- **Changing the OneBusAway iOS app.** The SDK is delivered as a SwiftPM package in this repo; integration into `../ios` (My Trip screen, GO-style notifications, consent UI) is a follow-up in that repo.
- **Rider-facing predictions or notifications** ("your stop is next"). The SDK reports; the host app owns UX.
- **Trip inference** ("guess which bus you're on"). The host app already has `NearbyTripMatcher`; the SDK takes a trip descriptor as input.
- **Overriding the trusted feed.** Rider positions never replace an official position for the same trip instance, even when fresher.
- **Multi-agency tenancy.** One GTFS static feed and one set of trusted feed URLs per server process, matching the existing single-agency assumption.
- **Admin HTML pages for rides.** JSON status/list endpoints only; a page is a follow-up.
- **Fixing the existing driver simulator's missing auth header.** Noted; out of scope.

---

## 4. Architecture

### 4.1 Configuration and enablement

Rider mode is off unless `RIDER_MODE_ENABLED=true`. When on, `GTFS_STATIC_URL` is required (an `http(s)://` URL or a local file path to a GTFS zip). All other settings have defaults.

| Variable | Default | Purpose |
|---|---|---|
| `RIDER_MODE_ENABLED` | `false` | Enable rider routes, engine and feed merge. |
| `GTFS_STATIC_URL` | — (required when enabled) | GTFS zip URL or path. |
| `GTFS_STATIC_REFRESH` | `24h` | Re-download and rebuild the index. Failure keeps the old index and logs. |
| `TRUSTED_GTFS_RT_URLS` | empty | Comma-separated VehiclePositions feed URLs. Empty means "no corroboration available". |
| `TRUSTED_FEED_POLL` | `30s` | Poll interval; uses `If-None-Match` / `If-Modified-Since`. |
| `TRUSTED_FEED_MAX_AGE` | `5m` | Trusted entities older than this are dropped from the snapshot. |
| `RIDER_JWT_TTL` | `8760h` | Rider token lifetime. |
| `RIDER_MAX_SHAPE_DISTANCE` | `60` | Metres from shape (plus reported accuracy) for a point to match. |
| `RIDER_MAX_SPEED` | `35` | Metres per second; implied speed above this is implausible. |
| `RIDER_SCHEDULE_EARLY` / `RIDER_SCHEDULE_LATE` | `15m` / `90m` | Schedule-adherence window. |
| `RIDER_POINT_MAX_AGE` | `90s` | A ride whose latest verified point is older than this stops contributing to the feed. |
| `RIDER_POINT_RETENTION` | `168h` | `ride_points` rows older than this are deleted hourly. |

Startup order when enabled: load GTFS (blocking; startup fails if the first load fails), start the trusted-feed poller, construct the engine, wire routes. `main.go` grows by one `if riderCfg.enabled { ... }` block; the rest lives in `rider_wiring.go`.

### 4.2 Package `rider/` — the engine

Importable package `github.com/OneBusAway/vehicle-positions/rider`. No `net/http` handlers, no `pgx`. Dependencies: `github.com/OneBusAway/go-gtfs` (static parsing; MIT, org-maintained, already used by maglev), `github.com/golang/geo/s2` (spherical polyline projection), the existing `gtfs-realtime-bindings`.

Files and responsibilities:

- `index.go` — `Index` built from `gtfs.Static`: `Trip(tripID) (*TripInfo, bool)`, `ActiveOn(tripID, serviceDate) bool`. `TripInfo{ID, RouteID, ShapeID, Shape *ShapeGeom, StopTimes []StopTimeInfo, Timezone}`. `StopTimeInfo{StopID, Sequence, AlongShape float64, Arrival, Departure time.Duration}`. Stop along-shape distances come from `shape_dist_traveled` when present (rescaled to metres if the feed uses other units, detected by comparing the last value with the computed shape length), otherwise from projecting the stop onto the shape at load time.
- `shape.go` — `ShapeGeom{Polyline *s2.Polyline, Cumulative []float64 /* metres */, Length float64}`; `Project(lat, lon) Projection{AlongShape, DistanceToShape, SegmentBearing}`; `PointAt(alongShape) (lat, lon)`. Uses `s2.Polyline.Project` for the closest point and segment index, then haversine for metres.
- `loader.go` — `LoadIndex(ctx, source string) (*Index, error)` for URL or path; `Refresher` goroutine with atomic swap.
- `trusted.go` — `TrustedFeed` poller for N URLs; `Snapshot()` returns `map[TripKey]TrustedVehicle{VehicleID, Lat, Lon, Timestamp, FeedURL}` filtered by `TRUSTED_FEED_MAX_AGE`, plus per-feed `Health{URL, LastSuccess, LastError, Entities}`. `TripKey{TripID, StartDate}`; entities without `start_date` are keyed on the service date inferred from the entity timestamp in the agency timezone.
- `verify.go` — pure function `Verify(in VerifyInput) Verdict`. Input: the trip info, previous accepted point (if any), the new point, the trusted vehicle for that trip key (if any), thresholds, and `now`. Verdict fields: `Outcome` (`Matched`, `OffRoute`, `Implausible`, `OffSchedule`, `Ignored`), `Corroboration` (`None`, `Corroborated`, `Contradicted`, `Unavailable`), `AlongShape`, `DistanceToShape`, `ScheduleDeviation`, and `Reason` string. Rules in §4.5.
- `ride.go` — `RideSession` state machine: consumes verdicts, tracks consecutive counters, exposes `State()` (`Pending`, `Verified`, `Rejected`), `Corroborated() bool`, `LatestPoint()`, `EndReason`. Rules in §4.6.
- `reputation.go` — `ScoreDelta(summary RideSummary) int` and `TierFor(score int, ridesTotal int) Tier` (`New`, `Trusted`, `Blocked`). Rules in §4.7.
- `aggregator.go` — `Aggregator` holds active `RideSession`s keyed by ride id; `Publishable(now) []TripEstimate` groups publishable rides by `TripKey`, computes the along-shape median, snaps to the shape, derives bearing and next stop. Rules in §4.8.

Every file has a table-driven `_test.go`. `testdata/` contains a small synthetic GTFS built in code by `testutil_test.go` (two routes, one loop, four trips, calendar, stop_times with and without `shape_dist_traveled`) and zipped in memory so the loader and index tests never touch the network.

### 4.3 Rider identity and authentication

Riders are anonymous. The SDK generates a UUID (`installation_id`) once per install and keeps it in the Keychain. Registration is idempotent.

`POST /api/v1/rider/register` (no auth; rate-limited 5/min per IP using a `RegistrationRateLimiter` built on the login limiter's fixed-window code):

```json
{"installation_id":"7d1d…","platform":"ios","app_id":"org.onebusaway.iphone","app_version":"26.4.0","attestation":null}
```

Response `201` (or `200` when the installation already exists):

```json
{"rider_id":"a3f0…","token":"<jwt>","report_interval_seconds":5,"max_batch_size":12}
```

The JWT is HS256 with the shared `JWT_SECRET`, issuer `vehicle-positions-api`, `role: "rider"`, `sub: <rider uuid>`, TTL `RIDER_JWT_TTL`. `requireRider(secret)` middleware parses it through `parseSessionToken`, rejects any role other than `rider`, and never falls back to the session cookie. Existing `requireAuth` continues to admit only `driver`/`admin` claims; `requireAdmin` is unchanged. A `blocked` rider is still authenticated and still gets `200`s (shadow treatment) so that abusers learn nothing from responses.

### 4.4 Rider API

All routes need `Authorization: Bearer <rider jwt>` except registration. All bodies are JSON, 64 KiB cap, `DisallowUnknownFields`, single object. Errors use the existing `{"error": "..."}` shape.

| Method + path | Purpose |
|---|---|
| `POST /api/v1/rider/register` | Issue or re-issue a rider token (§4.3). |
| `POST /api/v1/rider/rides` | Start a ride. |
| `POST /api/v1/rider/rides/{id}/positions` | Upload a batch of positions; returns the verdict summary. |
| `POST /api/v1/rider/rides/{id}/end` | End a ride with a reason. |
| `GET /api/v1/rider/trips/{trip_id}/status?start_date=YYYYMMDD` | Whether this trip instance currently has trusted or rider-derived coverage. |

**Start ride** request:

```json
{"trip_id":"1_604321","start_date":"20260902","route_id":"1_100223","vehicle_id":"1_7042",
 "boarding_stop_id":"1_75403","destination_stop_id":"1_75414"}
```

`trip_id` required and must exist in the index; `start_date` optional (defaults to today in the agency timezone) and the trip must be active on it (else `422 {"error":"trip not active on start_date"}`); `route_id` optional and, when present, must match the index; the rest optional and stored. One active ride per rider: starting a new one ends the previous with reason `superseded`. Response `201`:

```json
{"ride_id":"c9e2…","state":"pending","report_interval_seconds":5,"max_batch_size":12,
 "destination":{"stop_id":"1_75414","latitude":47.61,"longitude":-122.33}}
```

The destination coordinate is echoed from GTFS so the SDK can geofence it without the host app supplying coordinates.

**Positions** request (1–`max_batch_size` points, timestamps within `[now-10m, now+1m]`):

```json
{"positions":[{"latitude":47.60,"longitude":-122.33,"accuracy":8.0,"speed":9.2,"bearing":184.0,"timestamp":1756800000}]}
```

Response `200`:

```json
{"state":"verified","published":true,"corroboration":"corroborated","accepted":3,"ignored":0,
 "off_route_streak":0,"ended":false,"end_reason":null,"next_interval_seconds":5}
```

When the batch ends the ride (rejection, contradiction) the response still returns `200` with `"ended":true` and an `end_reason` of `off_route`, `contradicted`, or `implausible`. Uploading to an ended ride returns `409 {"error":"ride ended"}`. Per-rider rate limit: one batch per 2 s, burst 2; keyed on rider id; 429 on excess.

**End ride** request `{"reason":"user_requested"}`; allowed reasons: `user_requested`, `arrived`, `stationary`, `max_duration`, `location_unavailable`, `app_terminated`. Response `200 {"status":"ride ended","summary":{"points":142,"matched":139,"corroborated":88,"duration_seconds":1260}}`.

**Trip status** response: `{"trip_id":"…","start_date":"…","trusted":true,"rider_reported":false,"riders":0}`. Lets a host app show "shared by another rider" the way Transit does.

### 4.5 Point verification rules (`rider.Verify`)

Applied in order; the first failing rule sets the outcome.

1. **Ignored** (not counted toward any streak): accuracy > 100 m, or timestamp outside the batch window, or coordinates invalid/null island.
2. **OffRoute**: `DistanceToShape > RIDER_MAX_SHAPE_DISTANCE + accuracy`.
3. **Implausible**: with a previous accepted point, implied speed `|Δalong| / Δt > RIDER_MAX_SPEED`, or along-shape regression `Δalong < -50 m` (loops are handled because projection prefers the candidate nearest the previous along-shape distance: `Project` takes an optional `hint` and, among local minima within 2× the shape threshold, chooses the one closest to the hint).
4. **OffSchedule**: scheduled time at `AlongShape` is interpolated between the bracketing stop times (go-gtfs already interpolates missing stop times); deviation must be within `[-RIDER_SCHEDULE_EARLY, +RIDER_SCHEDULE_LATE]`. Before the trip's first stop time or after its last, the window is measured from that stop.
5. **Matched** otherwise.

Corroboration is computed independently for matched points:
- `Unavailable` when no trusted feed is configured or no trusted vehicle exists for the trip key.
- Project the trusted vehicle onto the shape. `gap = |ridersAlong − trustedAlong|`, `allowance = 150 m + RIDER_MAX_SPEED × |Δtimestamp|`.
- `Corroborated` if `gap ≤ allowance`; `Contradicted` if `gap > allowance + 500 m`; `None` in between.

### 4.6 Ride state machine (`rider.RideSession`)

```
Pending ──3 consecutive Matched──▶ Verified
Pending/Verified ──5 consecutive OffRoute|Implausible|OffSchedule──▶ Rejected(off_route|implausible|off_schedule)
Pending/Verified ──3 consecutive Contradicted──▶ Rejected(contradicted)
Verified ──12 Corroborated points (~1 min)──▶ Verified + Corroborated=true (sticky until ride end)
any ──end request | 15 min without accepted points | 3 h total──▶ Ended
```

`Matched` points reset the off-route streak; `Corroborated` resets the contradiction streak; `Ignored` points touch nothing. A ride contributes to the feed only while `Verified`, not ended, and its latest matched point is younger than `RIDER_POINT_MAX_AGE`.

Idle rides are reaped by the aggregator's ticker (every 30 s); reaping persists the end with reason `idle`.

### 4.7 Reputation (`rider.ScoreDelta`, `rider.TierFor`)

At ride end the ride summary yields a score delta:

| Ride outcome | Delta |
|---|---|
| Corroborated and ≥ 5 min of matched points | +1 |
| Verified, uncorroborated (feed unavailable or no vehicle) | 0 |
| Rejected: off_route / implausible / off_schedule | −1 |
| Rejected: contradicted | −3 |

Tiers: `Blocked` when score ≤ −3; `Trusted` when score ≥ 3; else `New`. Score is clamped to `[-10, 10]` so a long-good rider can be demoted in a few bad rides and a demoted rider can recover.

Publishability of a ride = `Verified && !ended && fresh && (rider.Tier == Trusted || ride.Corroborated || consensus)` and `rider.Tier != Blocked`. Consensus is decided by the aggregator (§4.8).

### 4.8 Aggregation and feed merge

`Aggregator.Publishable(now)` groups contributing rides by `TripKey`:

- A ride contributes if it is `Verified`, fresh, and its rider is not `Blocked`.
- A group is **publishable** if any contributing ride is individually publishable (trusted rider or corroborated), or if ≥ 2 contributing rides from distinct riders have latest along-shape positions within 100 m of each other (consensus). Riders that disagree with the group median by more than 100 m are excluded from the estimate but keep contributing (they may be right and the others wrong; the server does not adjudicate beyond the median).
- Estimate: along-shape **median** of contributing latest points; position = `Shape.PointAt(median)`; bearing = segment bearing at that point; speed = median reported speed; timestamp = newest contributing timestamp; `current_stop_sequence` / `stop_id` = first stop whose along-shape distance ≥ median, `current_status = IN_TRANSIT_TO`; `riders` count for admin.

Feed merge (`buildFeed` gains a second input):

- Rider estimates are **suppressed** for any trip key present in the trusted snapshot (`Snapshot()` already drops entities older than `TRUSTED_FEED_MAX_AGE`).
- Entity id `rider:<trip_id>:<start_date>`; `vehicle.id = "rider:" + trip_id`; `vehicle.label = "Rider-reported"`; `trip.trip_id`, `trip.route_id`, `trip.start_date`; `position` snapped; `timestamp` newest contributing.
- `GET /gtfs-rt/vehicle-positions?source=driver|rider|all` (default `all`); any other value → `400`. Existing driver entities and header behaviour are untouched; the feed-validation test suite runs against merged output too.

Snapping to the shape also serves privacy: the published position is the vehicle's estimated position on the route, never a raw rider fix.

### 4.9 Store additions

Migrations `000011_riders.up/down.sql`:

```sql
CREATE TABLE riders (
  id UUID PRIMARY KEY,
  installation_id TEXT NOT NULL UNIQUE CHECK (installation_id <> ''),
  platform TEXT NOT NULL DEFAULT '',
  app_id TEXT NOT NULL DEFAULT '',
  app_version TEXT NOT NULL DEFAULT '',
  attested BOOLEAN NOT NULL DEFAULT false,
  score INTEGER NOT NULL DEFAULT 0,
  tier TEXT NOT NULL DEFAULT 'new' CHECK (tier IN ('new','trusted','blocked')),
  rides_total INTEGER NOT NULL DEFAULT 0,
  rides_corroborated INTEGER NOT NULL DEFAULT 0,
  rides_rejected INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE rides (
  id UUID PRIMARY KEY,
  rider_id UUID NOT NULL REFERENCES riders(id) ON DELETE CASCADE,
  trip_id TEXT NOT NULL, start_date TEXT NOT NULL, route_id TEXT NOT NULL DEFAULT '',
  vehicle_id TEXT NOT NULL DEFAULT '', boarding_stop_id TEXT NOT NULL DEFAULT '',
  destination_stop_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','ended')),
  state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','verified','rejected')),
  corroborated BOOLEAN NOT NULL DEFAULT false,
  end_reason TEXT NOT NULL DEFAULT '',
  points_total INTEGER NOT NULL DEFAULT 0, points_matched INTEGER NOT NULL DEFAULT 0,
  points_corroborated INTEGER NOT NULL DEFAULT 0, points_contradicted INTEGER NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), ended_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rides_rider_status ON rides(rider_id, status);
CREATE INDEX idx_rides_trip ON rides(trip_id, start_date);
CREATE TABLE ride_points (
  id BIGSERIAL PRIMARY KEY,
  ride_id UUID NOT NULL REFERENCES rides(id) ON DELETE CASCADE,
  latitude DOUBLE PRECISION NOT NULL, longitude DOUBLE PRECISION NOT NULL,
  accuracy DOUBLE PRECISION, speed DOUBLE PRECISION, bearing DOUBLE PRECISION,
  timestamp BIGINT NOT NULL CHECK (timestamp > 0),
  outcome TEXT NOT NULL, corroboration TEXT NOT NULL,
  along_shape DOUBLE PRECISION, distance_to_shape DOUBLE PRECISION, schedule_deviation_seconds INTEGER,
  received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ride_points_ride_ts ON ride_points(ride_id, timestamp DESC);
CREATE INDEX idx_ride_points_received ON ride_points(received_at);
```

Fixed-shape queries go through sqlc in `db/query.sql`: `UpsertRider`, `GetRiderByID`, `UpdateRiderAfterRide`, `TouchRider`, `InsertRide`, `GetRide`, `EndActiveRidesForRider`, `EndRide`, `UpdateRideStats`, `InsertRidePoint` (batched via `pgx.Batch` in the store), `DeleteRidePointsBefore`, `CountRidersByTier`, `ListRides` (fixed filter: status + limit/offset), `EndAllActiveRides` (startup). Store file `rider_store.go` exposes narrow interfaces (`RiderRegistrar`, `RideStarter`, `RidePointRecorder`, `RideEnder`, `RideLister`, `RiderStatsReader`) which are added to `appStore` and to `route_wiring_test.go`'s `noopStore`.

Ingest path per batch: verify all points in memory (engine), then one transaction: insert points (batch), update ride stats/state, touch rider. On DB failure the in-memory session is **not** advanced and the client gets `500`, mirroring the driver path. On startup all `active` rides are ended with reason `server_restart` (rides are short; re-seeding sessions is not worth the complexity).

### 4.10 Admin endpoints

Both `authMiddleware(adminMiddleware(...))`.

- `GET /api/v1/admin/rider/status` → `{"enabled":true,"gtfs":{"source":"…","loaded_at":"…","trips":1234,"shapes":210,"agency_timezone":"America/Los_Angeles"},"trusted_feeds":[{"url":"…","last_success":"…","last_error":"","entities":87}],"riders":{"total":40,"trusted":6,"blocked":1},"rides":{"active":3,"publishable":2},"published_trips":2}`. When disabled: `{"enabled":false}`.
- `GET /api/v1/admin/rider/rides?status=active|ended&limit=50&offset=0` → `{"count":n,"has_more":b,"rides":[{id, rider_id, trip_id, start_date, route_id, state, corroborated, status, end_reason, points_total, points_matched, points_corroborated, started_at, ended_at, publishable (active only)}]}`.

### 4.11 Rider simulator (`cmd/ridersim`)

Flags: `-url`, `-gtfs` (zip path or URL), `-trip` (repeatable; or `-random N` to pick N trips active today), `-interval 5s`, `-speed 10` (m/s), `-noise 8` (metres of Gaussian jitter), `-offroute` (walk 300 m off the shape after 60 s, to exercise rejection), `-riders-per-trip 1`. Each rider registers, starts the ride, walks the shape at the configured speed, uploads batches, prints the state transitions and ends on the last stop. Used by the end-to-end smoke in the plan and by `/go`.

### 4.12 iOS SDK — `VehiclePositionsKit`

SwiftPM package at `ios/VehiclePositionsKit/`, `swift-tools-version: 6.0`, `platforms: [.iOS(.v18)]`, `swiftLanguageModes: [.v6]`, default isolation `nonisolated` (matching OBAKitCore; the host app is `MainActor` by default and wraps what it needs). One library product `VehiclePositionsKit`, one test target using Swift Testing. All public types are `Sendable`.

**Public surface**

```swift
public struct RideReporterConfiguration: Sendable {
    public var serverURL: URL
    public var appID: String
    public var appVersion: String
    public var reportInterval: Duration = .seconds(5)      // server may override per ride
    public var maxBatchSize: Int = 12
    public var maxRideDuration: Duration = .seconds(3 * 3600)
    public var stationaryTimeout: Duration = .seconds(10 * 60)
    public var arrivalRadius: CLLocationDistance = 75
    public var locationConfiguration: CLLocationUpdate.LiveConfiguration = .otherNavigation
}

public struct TripDescriptor: Sendable, Codable {
    public var tripID: String
    public var startDate: String?            // YYYYMMDD in agency time; nil = today
    public var routeID: String?
    public var vehicleID: String?
    public var boardingStopID: String?
    public var destinationStopID: String?
}

public enum RideState: String, Sendable, Codable { case pending, verified, rejected }
public enum RideEndReason: String, Sendable, Codable {
    case userRequested, arrived, stationary, maxDuration, locationUnavailable,
         authorizationDenied, offRoute, contradicted, implausible, offSchedule, superseded, serverRestart, idle, networkFailure
}
public struct RideProgress: Sendable { state, published: Bool, corroboration, pointsAccepted, offRouteStreak }
public enum RideEvent: Sendable {
    case registered(riderID: String)
    case started(rideID: String)
    case progress(RideProgress)
    case ended(RideEndReason, summary: RideSummary?)
    case warning(RideWarning)   // uploadRetrying(attempt), locationAccuracyLimited, backgroundSessionLost
}

public actor RideReporter {
    public init(configuration: RideReporterConfiguration,
                transport: any RideTransport = URLSessionRideTransport(),
                locationSource: any LocationSource = CoreLocationSource(),
                credentialStore: any CredentialStore = KeychainCredentialStore())
    public var currentState: RideState? { get }
    public func start(_ trip: TripDescriptor) async throws -> AsyncStream<RideEvent>
    public func end(reason: RideEndReason = .userRequested) async
    public func tripStatus(tripID: String, startDate: String?) async throws -> TripStatus
}
```

`RideTransport` (`send(_ request: RiderRequest) async throws -> RiderResponse`) and `LocationSource` (`func updates() -> AsyncThrowingStream<LocationSample, Error>` plus `func beginBackgroundActivity() -> any BackgroundActivityToken`) are the injection seams; production implementations wrap `URLSession` and `CLLocationUpdate.liveUpdates` + `CLBackgroundActivitySession` + `CLServiceSession(authorization: .whenInUse)`. `CredentialStore` persists `installation_id`, `rider_id`, `token`; the default uses the Keychain, tests use an in-memory store. A `Clock`-style `RideClock` protocol is injected so timer-based behaviour (stationary timeout, max duration, upload cadence) is tested with a manual clock.

**Behaviour**

1. `start` registers if no token is stored (or re-registers on `401`), calls start-ride, holds a background activity session and a service session for the ride's lifetime, and begins consuming location updates.
2. Samples are buffered; every `reportInterval` or when `maxBatchSize` is reached, a batch is uploaded on a regular `URLSession` (the location background mode keeps the process alive; background sessions are wrong for a 5 s cadence). Failed uploads retry with exponential backoff (1 s, 2 s, 4 s … 30 s cap) and the buffer keeps at most 10 minutes of samples, dropping the oldest. After 5 minutes of continuous failure the ride ends with `networkFailure` (locally only; the server reaps it as `idle`).
3. Each response updates `currentState` and emits `.progress`; a response with `ended: true` ends the ride locally with the server's reason.
4. The reporter ends the ride itself on: the destination coordinate (from the start-ride response) coming within `arrivalRadius` after the ride has moved ≥ 200 m along the trip (so starting at the destination does not end immediately); `stationary == true` persisting for `stationaryTimeout`; `maxRideDuration`; `authorizationDenied` / `locationUnavailable` diagnostics; the host calling `end`.
5. Ending always attempts one final flush and the end-ride call (best effort, 10 s timeout), then invalidates the background session.
6. Registration and ride lifecycle never include any identifier other than the random installation UUID; the SDK exposes no way to attach user identity.

**Host integration notes** (documented in the package README, not implemented here): the host app must add `location` to `UIBackgroundModes` and already declares `NSLocationWhenInUseUsageDescription`; When-In-Use authorization is sufficient because `CLBackgroundActivitySession` keeps the app "in use" with the blue indicator, matching Transit's visible-while-active behaviour. The host recreates the reporter when its region (server URL) changes, exactly as it does for `RESTAPIService`.

**Testing**: Swift Testing suites for the request/response codec, the batching/retry scheduler (manual clock), the end-condition evaluator (arrival, stationary, max duration), the state mirror, and a full scripted ride against fake transport and location source. Tests run on the iOS simulator through XcodeBuildMCP / `xcodebuild test -scheme VehiclePositionsKit -destination 'platform=iOS Simulator,…'`; `swift test` on macOS host is not a target because the package is iOS-only.

---

## 5. Component boundaries

| Unit | Does | Depends on | Consumed by |
|---|---|---|---|
| `rider.Index` / `ShapeGeom` | GTFS lookups, projection, schedule interpolation | go-gtfs, s2 | Verify, Aggregator, handlers (start-ride validation) |
| `rider.TrustedFeed` | Poll + snapshot trusted VehiclePositions | gtfs-realtime-bindings, net/http client | Verify (via handler), feed merge, admin status |
| `rider.Verify` | Pure per-point verdict | Index, thresholds | Ride handler |
| `rider.RideSession` | Per-ride streaks and state | Verify verdicts | Aggregator, handler |
| `rider.Aggregator` | Active sessions, publishable estimates, idle reaping | RideSession, Index | `buildFeed`, admin status, wiring |
| `rider_store.go` | Persistence of riders/rides/points, reputation update | pgx, sqlc | handlers |
| `rider_handlers.go` / `rider_auth.go` | HTTP surface, JWT for riders, rate limits | store, engine, auth helpers | `newMux` |
| `rider_wiring.go` | Config parsing, engine construction, refresh/poll goroutines, startup cleanup | all above | `main.go`, tests |
| `VehiclePositionsKit` | Phone-side capture, batching, lifecycle | CoreLocation, Foundation, Security | Host apps |

---

## 6. Testing

- **Engine**: table-driven unit tests per file, synthetic in-memory GTFS, no network. Projection tests include a loop route and a point equidistant from two segments with a hint. Verify tests cover each outcome and each corroboration result. Session tests walk every transition in §4.6. Reputation tests cover every row of §4.7 and clamping. Aggregator tests cover single trusted rider, two-rider consensus, outlier exclusion, suppression by the trusted snapshot, and freshness expiry.
- **Trusted feed**: `httptest.Server` serving protobuf with ETag / 304 and error cases; snapshot age filtering.
- **Handlers**: `httptest` with a fake store (extend `noopStore`); registration idempotency and rate limiting; rider JWT role isolation (driver token → 403 on rider routes, rider token → 401/403 on driver and admin routes); start-ride validation; positions batch verdicts and ride ending; 409 on ended rides; feed `source` filter; admin status/list.
- **Store**: `newTestStore(t)` integration tests behind `DATABASE_URL` for every query, cascade deletes, retention delete, startup end-all.
- **Feed compliance**: the existing `validateFeedCompliance` runs on a merged feed containing rider entities.
- **Simulator + smoke**: `cmd/ridersim` against a local server with a small GTFS fixture and a trusted feed served by the server's own driver mode (a driver simulator on the same trip acts as the "official" feed), asserting that a rider ride becomes corroborated, that the rider entity is suppressed while the driver reports, and that it appears once the driver stops.
- **SDK**: Swift Testing on simulator as in §4.12; strict concurrency clean.

---

## 7. Risks

- **GTFS memory**: whole-feed parse with go-gtfs. Large agencies (King County Metro ~300 MB unzipped) take seconds and hundreds of MB. Acceptable for v1; the index keeps only trips, stop times and shapes and drops the rest after build. If it becomes a problem, transitland-lib's streaming reader is the fallback (GPL).
- **Trusted feed without `start_date`**: inferring the service date from the entity timestamp is wrong for post-midnight trips; those trips may be double-published for a few minutes. Mitigation: key inference uses the agency timezone and a 3 a.m. service-day boundary.
- **Colluding devices** can reach consensus without corroboration. Mitigation in v1: consensus requires distinct rider ids, both non-blocked, and the estimate is still suppressed whenever the trusted feed reports the trip. App Attest is the v2 mitigation.
- **Loops and overlapping routes**: projection with a hint handles loops; overlapping routes are a host-app trip-choice problem, and the schedule window catches most wrong-direction choices.
- **Battery**: `liveUpdates` has no accuracy knobs; `.otherNavigation` and automatic stationary pausing are the only levers. The SDK measures nothing itself; the host app can compare against Transit's 5%/20 min benchmark.
- **JWT secret sharing**: rider tokens use the same secret as driver/admin tokens, so role checks are the only separation. The `requireRider` middleware and the negative tests in §6 are the guard.
