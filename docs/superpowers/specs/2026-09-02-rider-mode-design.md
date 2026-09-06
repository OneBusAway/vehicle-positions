# Rider Mode v1 — Design Spec

**Date:** 2026-09-02 (revised after Fable design review, same day)
**Status:** Approved for implementation (autonomous run; user pre-approved the pipeline and asked for no further questions)
**Revision:** revised 2026-09-02 after final review
**Scope:** A crowdsourced, low-trust vehicle-position ingestion mode for the `vehicle-positions` server, plus an iOS 18+ SwiftPM SDK (`VehiclePositionsKit`) that rider-facing apps (OneBusAway iOS first) use to report their position while aboard a transit vehicle.

---

## 1. Overview

Today the server (commit `81e7433`) ingests positions from **trusted, authenticated drivers** who are assigned to known vehicles, and republishes them as a GTFS-Realtime VehiclePositions feed. Every report is believed; the only defenses are JSON validation and a per-driver rate limit.

This spec adds a second ingestion path: **riders**. A rider is an anonymous phone in the OneBusAway app (or any other "vendor" app built on the SDK) that says "I am on trip T right now" and streams GPS fixes for the duration of the ride. Rider data is untrusted by construction: the phone could be on the wrong bus, in a car on a parallel street, spoofing GPS, or lying about the trip. The server therefore treats every rider report the way Transit's GO treats its riders: **don't trust, verify, then trust.**

Verification has three legs, each independent and each cheap:

1. **Static plausibility** — the fix must lie on the trip's GTFS shape, move monotonically along it at a bus-like speed, and sit inside a schedule-adherence window for that trip on that service date.
2. **Corroboration** — a trusted GTFS-RT VehiclePositions feed (the agency's own AVL) is polled continuously. When it has a vehicle on the same trip instance, the rider's along-shape position is compared to it. Agreement raises trust; disagreement ends the ride and penalizes the rider.
3. **Reputation** — riders accrue a score across rides. Corroborated rides raise it, contradicted rides lower it. Trusted riders' verified rides publish immediately; new riders' rides publish only once corroborated during the ride, or when two independent riders agree.

The output is a **supplement**: rider-derived positions are published only for trip instances the trusted feed is *not* currently reporting. The trusted feed always wins when present. Consumers see the same `GET /gtfs-rt/vehicle-positions` feed they see today, with rider entities namespaced and labelled, or can filter to one source. This matters because OneBusAway's Java server does last-write-wins across feeds with no arbitration, so a republished feed must never carry a second entity for a vehicle the agency feed already reports.

The inspiration is Transit's GO ([help article](https://help.transitapp.com/article/549-how-to-use-go)): the rider taps GO on a trip they are about to board, the phone shares "the position of the vehicle" (never the rider's identity) "only while GO is active", and sharing "ends automatically when you've reached your destination." Transit reports roughly 5% battery for a 20-minute trip and under 100 KB of data; those are our budgets too.

### 1.1 Separate binary or a mode? — Decision: a mode, with the engine in its own package

Three options were weighed:

- **A. Separate binary (`cmd/rider`)** — cleanest deployment story, but every server file lives in `package main` at the repo root and is not importable. A second binary would first require extracting store, auth, tracker, feed building, proxy handling and the admin UI into packages: a large, unrelated refactor with its own risk.
- **B. Same binary, new files in `package main`** — matches the repo's "one concern per file in main" convention but would add roughly a dozen files of pure geometry/verification logic to an already 60-file root, and would tangle testable pure logic with HTTP and DB concerns.
- **C. Same binary, additive mode, engine in a dedicated importable package `rider/`** (recommended, chosen). HTTP handlers, store methods, auth middleware and wiring stay in `package main` following existing conventions. Everything that needs no database and no HTTP — GTFS index, shape projection, trusted-feed snapshot, per-point verifier, ride state machine, reputation policy, aggregator — lives in `rider/` with table-driven unit tests. The mode is enabled by configuration; a deployment can run driver mode only, rider mode only, or both in one process. If rider mode later needs to scale independently, `rider/` is already the seam for a `cmd/rider` extraction.

Shared by both modes: PostgreSQL, migrations, JWT secret and `parseSessionToken`, rate-limiter patterns, `buildFeed`, request logging, CSRF wrapper, admin auth, Docker image. `main` imports `rider`; `rider` never imports `main` or `db`, so there is no cycle.

---

## 2. Goals

1. Riders can register anonymously, start a ride on a GTFS trip instance, stream batched positions, and end the ride; all over JSON with a long-lived rider JWT.
2. Every rider point is verified against GTFS static data (shape, schedule, calendar) and, when available, a trusted GTFS-RT VehiclePositions feed.
3. A ride, and the rider behind it, moves through explicit trust states; only publishable rides reach the feed.
4. The GTFS-RT feed gains rider-derived entities for trip instances absent from the trusted feed, snapped to the shape, with correct trip descriptors and a visible source label. Driver-reported entities and the existing feed-validation guarantees (E001, E012, E026, E027, E050, E052, W001, W002) are unchanged.
5. The iOS SDK does the phone side end to end: registration, background location capture with `CLLocationUpdate.liveUpdates` and `CLBackgroundActivitySession`, batching and retrying uploads, reacting to server verdicts, and ending rides automatically.
6. Operators can see rider-mode health (GTFS load, trusted-feed freshness, active/publishable rides, rider tiers) from an admin JSON endpoint.
7. The whole loop can be exercised locally with a simulator that replays GTFS shapes as fake riders.

## 3. Non-Goals

- **App Attest / device integrity.** The registration payload reserves an `attestation` field (must be `null` or absent in v1) and the `riders` table has an `attested` column, but v1 does not verify Apple attestation objects (CBOR + X.509 chain validation is a project of its own, and `DCAppAttestService` is unavailable on the simulator anyway). Reputation and corroboration carry the trust load in v1.
- **Changing the OneBusAway iOS app.** The SDK is delivered as a SwiftPM package in this repo; integration into `../ios` (My Trip screen, GO-style notifications, consent UI, `location` background mode) is a follow-up in that repo.
- **Rider-facing predictions or notifications** ("your stop is next"). The SDK reports; the host app owns UX.
- **Trip inference** ("guess which bus you're on"). The host app already has `NearbyTripMatcher`; the SDK takes a trip descriptor as input.
- **Overriding the trusted feed.** Rider positions never replace an official position for the same trip instance, even when fresher.
- **Multi-agency tenancy.** One GTFS static feed and one set of trusted feed URLs per server process, matching the existing single-agency assumption. Multi-agency GTFS zips are accepted; the first agency's timezone is used.
- **Rejoining a ride after the app is terminated.** If iOS kills the host app, the ride is lost on the phone and reaped as `idle` on the server; the host can start a new ride. (`CLBackgroundActivitySession` can be rejoined after relaunch, but v1 does not implement it.)
- **Admin HTML pages for rides.** JSON status/list endpoints only.
- **Fixing the existing driver simulator's missing auth header.** Noted; out of scope.
- **Heading checks.** GPS heading is unreliable at bus speeds; TransitClock disables its heading check by default. Not used.

---

## 4. Architecture

### 4.1 Configuration and enablement

Rider mode is off unless `RIDER_MODE_ENABLED=true`. When on, `GTFS_STATIC_URL` is required (an `http(s)://` URL or a local file path to a GTFS zip). All other settings have defaults. Durations use `time.ParseDuration`; invalid values log and fall back to the default, like the existing `envDurationOrDefault`.

| Variable | Default | Purpose |
|---|---|---|
| `RIDER_MODE_ENABLED` | `false` | Enable rider routes, engine and feed merge. |
| `GTFS_STATIC_URL` | — (required when enabled; exit 1 if missing) | GTFS zip URL or path. |
| `GTFS_STATIC_REFRESH` | `24h` | Re-download and rebuild the index. Failure keeps the old index and logs. |
| `TRUSTED_GTFS_RT_URLS` | empty | Comma-separated VehiclePositions feed URLs. Empty means corroboration is `unavailable`. |
| `TRUSTED_FEED_POLL` | `30s` | Poll interval; sends `If-None-Match` / `If-Modified-Since` when the server gave `ETag` / `Last-Modified`. |
| `TRUSTED_FEED_MAX_AGE` | `5m` | Trusted entities older than this are dropped from the snapshot. |
| `RIDER_JWT_TTL` | `8760h` | Rider token lifetime. |
| `RIDER_MAX_SHAPE_DISTANCE` | `60` | Metres from shape (plus reported accuracy) for a point to match. |
| `RIDER_MAX_SPEED` | `35` | Metres per second; implied along-shape speed above this is implausible. |
| `RIDER_SCHEDULE_EARLY` / `RIDER_SCHEDULE_LATE` | `15m` / `90m` | Schedule-adherence window. |
| `RIDER_POINT_MAX_AGE` | `90s` | A ride whose latest matched point is older than this stops contributing to the feed. |
| `RIDER_POINT_RETENTION` | `168h` | `ride_points` rows older than this are deleted hourly. |

Thresholds live in one struct, `rider.Thresholds`, with `rider.DefaultThresholds()`; the engine never reads the environment.

Startup order when enabled: load GTFS (blocking; startup fails if the first load fails), end all `active` rides in the DB with reason `server_restart`, start the trusted-feed poller, construct the aggregator, wire routes, start the refresh, reap and retention tickers. `main.go` grows by one `if riderCfg.Enabled { ... }` block; the rest lives in `rider_wiring.go` (`type riderConfig`, `riderConfigFromEnv()`, `newRiderRuntime(ctx, cfg, store) (*riderRuntime, error)`, `(*riderRuntime).Stop()`).

### 4.2 Package `rider/` — the engine

Importable package `github.com/OneBusAway/vehicle-positions/rider`. No `net/http` handlers (the trusted-feed *client* is allowed), no `pgx`, no `database/sql`. Dependencies: `github.com/OneBusAway/go-gtfs` v1.1.1 (static parsing; MIT, org-maintained, used by maglev) and the existing `gtfs-realtime-bindings`. No geometry library: projection is ~100 lines and is written here (§4.2 `shape.go`) so that loops get local-minimum handling, which `s2.Polyline.Project` and `orb` do not offer.

go-gtfs facts the implementation relies on: `gtfs.ParseStatic(zipBytes, gtfs.ParseStaticOptions{}) (*gtfs.Static, error)`; `Static.Trips []ScheduledTrip{ID, Route *Route, Service *Service, StopTimes []ScheduledStopTime, Shape *Shape}`; `ScheduledStopTime{Stop *Stop, ArrivalTime, DepartureTime time.Duration, StopSequence int, ShapeDistanceTraveled *float64}` (missing times are already interpolated by the parser); `Shape{ID, Points []ShapePoint{Latitude, Longitude, Distance *float64}}`; `Service{Monday…Sunday bool, StartDate, EndDate time.Time, AddedDates, RemovedDates []time.Time}` (no `ActiveOn` helper; the index implements it); `Static.Agencies[0].Timezone string`.

Files and responsibilities:

- `index.go` — `type Index struct` built by `BuildIndex(static *gtfs.Static) (*Index, error)`. Methods: `Trip(tripID string) (*TripInfo, bool)`, `ActiveOn(trip *TripInfo, serviceDate string) bool` (weekday bools, start/end date, added/removed dates), `Timezone() *time.Location`, `ServiceDate(now time.Time) string` (local date in the agency timezone; local hours before 03:00 belong to the previous service day), `Stats() IndexStats{Trips, Shapes int, LoadedAt time.Time, Source string}`, `TripIDs() []string` (for the simulator's `-random`). `TripInfo{ID, RouteID, ServiceID string, Shape *ShapeGeom, StopTimes []StopTimeInfo}` is immutable after build; active rides hold their own `*TripInfo`, so an index swap never affects a ride in progress. `StopTimeInfo{StopID string, Sequence int, AlongShape float64, Arrival, Departure time.Duration, Lat, Lon float64}`; `AlongShape` comes from `shape_dist_traveled` when the feed provides it (rescaled to metres by comparing the last stop's value to the computed shape length; ratio within 0.8–1.2 means metres, otherwise the ratio is applied), else from projecting the stop onto the shape at build time. Trips without a shape are excluded from the index (rider mode needs geometry). The index keeps only what it needs; `gtfs.Static` is dropped after build.
- `shape.go` — `type ShapeGeom struct{ Points []LatLon; Cumulative []float64; Length float64 }` (metres). `NewShapeGeom(points []LatLon) *ShapeGeom`. `Project(p LatLon, hint *float64) Projection` uses an equirectangular local projection (longitude scaled by cos of the shape's mean latitude) to compute point-to-segment distance for every segment, collects local minima whose distance is within `2×` the best distance, and returns the best; with a non-nil `hint` (previous along-shape distance) it returns the local minimum with the smallest `|along − hint|` among those within 2× the best distance. `Projection{AlongShape, DistanceToShape, SegmentBearing float64}`. `PointAt(along float64) LatLon` and `BearingAt(along float64) float64` interpolate along `Cumulative`. Haversine lives here as `Distance(a, b LatLon) float64`.
- `loader.go` — `LoadStatic(ctx context.Context, source string) (*gtfs.Static, error)` for `http(s)://` URLs (via an injected `*http.Client`) or file paths; `type Refresher` with `Start(ctx, every time.Duration)` swapping an `atomic.Pointer[Index]`; `Current() *Index`.
- `trusted.go` — `type TrustedFeed struct` polling N URLs on one ticker. `Lookup(key TripKey) (TrustedVehicle, bool)`: exact `{TripID, StartDate}` first, then `{TripID, ""}` (entities whose `trip.start_date` is absent are stored under the empty date; no service-day inference). `Covers(key TripKey) bool` = same lookup. `Snapshot()` returns the age-filtered map (entities with `timestamp` older than `MaxAge`, or with no timestamp, are dropped). `Health() []FeedHealth{URL string, LastSuccess time.Time, LastError string, Entities int}`. `TrustedVehicle{VehicleID string, Pos LatLon, Timestamp time.Time}`. Entities whose `vehicle.position` or `trip.trip_id` is missing are ignored.
- `verify.go` — `func Verify(in VerifyInput) Verdict`, pure. `VerifyInput{Trip *TripInfo; Prev *AcceptedPoint; Point Point; Trusted *TrustedVehicle; Thresholds Thresholds; Now time.Time}`; `Point{Lat, Lon, Accuracy, Speed, Bearing float64; Timestamp time.Time}` (`Accuracy`, `Speed`, `Bearing` are `-1` when absent). `Verdict{Outcome Outcome; Corroboration Corroboration; AlongShape, DistanceToShape float64; ScheduleDeviation time.Duration; Reason string}`. `Outcome` ∈ `Ignored, OffRoute, Implausible, OffSchedule, Matched`; `Corroboration` ∈ `Unavailable, None, Corroborated, Contradicted`. Rules in §4.5.
- `session.go` — `type Session struct` (per ride; not safe for concurrent use, guarded by the aggregator). `NewSession(id, riderID string, key TripKey, trip *TripInfo, startedAt time.Time) *Session`; `Apply(v Verdict, p Point, now time.Time) Transition` (returns whether state changed and any end reason); `State() State` (`Pending, Verified, Rejected`); `Corroborated() bool`; `Latest() *AcceptedPoint` (`AcceptedPoint{Point; AlongShape float64}`); `Counts() Counts{Total, Matched, Corroborated, Contradicted int}`; `LastAcceptedAt() time.Time`; `End(reason EndReason, at time.Time)`. Rules in §4.6.
- `reputation.go` — `func ScoreDelta(s RideSummary) int`; `func TierFor(score int) Tier` (`New, Trusted, Blocked`); `func Clamp(score int) int`. `RideSummary{EndReason EndReason; Corroborated bool; MatchedDuration time.Duration}`. Rules in §4.7.
- `aggregator.go` — `type Aggregator struct` with one `sync.Mutex` around `map[string]*Session`. `Add(s *Session, tier Tier)`; `ApplyBatch(rideID string, points []Point, lookup func(TripKey) (TrustedVehicle, bool), now time.Time) (BatchResult, error)` (verifies and applies every point under the lock, in timestamp order; returns `ErrUnknownRide` for ended/unknown rides); `SetTier(riderID string, tier Tier)`; `Remove(rideID string) *Session`; `Reap(now time.Time) []*Session` (ends and removes sessions idle > 15 min or older than 3 h; returns them so the caller can persist); `Estimates(now time.Time, covered func(TripKey) bool) []TripEstimate`; `ActiveCount() int`; `PublishableCount(now, covered) int`. Rules in §4.8.

Every file has a table-driven `_test.go`. `fixture_test.go` builds a small synthetic GTFS in code (two routes, one of them a loop, four trips, `calendar.txt` plus one `calendar_dates.txt` exception, `stop_times.txt` with and without `shape_dist_traveled`), zips it in memory, and exposes it to the loader, index and simulator tests so nothing touches the network. `rider/testdata/fixture.zip` is committed, so a clean checkout has it without running anything. `rider/fixture_test.go` rewrites it only under `WRITE_FIXTURE=1` and otherwise checks the committed file still matches what the builder produces, which is what keeps the two from drifting.

### 4.3 Rider identity and authentication

Riders are pseudonymous rather than anonymous: the SDK generates a UUID (`installation_id`) once per install and keeps it in the Keychain, so it is stable across launches and every ride links back to it. Registration is idempotent.

`POST /api/v1/rider/register` (no auth; rate-limited 5/min per IP using `clientIP(r, trustProxy)` and a `RegistrationRateLimiter` built on the login limiter's `allowInWindow` fixed-window helper, failing closed at 10 000 tracked IPs):

```json
{"installation_id":"7d1d…","platform":"ios","app_id":"org.onebusaway.iphone","app_version":"26.4.0","attestation":null}
```

`installation_id` must parse as a UUID; `platform` ∈ `ios`, `android`, `other`; `app_id` and `app_version` ≤ 100 chars. Response `201` on first registration, `200` when the installation already exists (same `rider_id`, fresh token):

```json
{"rider_id":"a3f0…","token":"<jwt>","report_interval_seconds":5,"max_batch_size":12}
```

The JWT is HS256 with the shared `JWT_SECRET`, issuer `vehicle-positions-api`, claims `role: "rider"`, `sub: <rider uuid>`, `iat`, `exp` (TTL `RIDER_JWT_TTL`), issued by `generateRiderJWT(riderID string, secret []byte, ttl time.Duration)` in `rider_auth.go`.

**Role separation (change to existing middleware).** Today `requireAuth` admits any valid token and only `requireAdmin` inspects the role, so a rider token would pass every driver route. `requireAuth` therefore gains a role check: after parsing, `claims["role"]` must be `driver` or `admin`, otherwise `403 {"error":"forbidden"}`. The new `requireRider(secret []byte)` middleware accepts only `Authorization: Bearer …` (no cookie fallback), parses through `parseSessionToken`, and requires `role == "rider"` (`403` otherwise); it stores claims under the same `claimsKey`. Handlers read the rider id with `riderIDFromContext(ctx) (string, bool)`. `adminClaimsFromCookie` already requires `admin`, so the admin UI is unaffected.

A `blocked` rider is still authenticated and still receives normal `200` responses (shadow treatment) so that abusers learn nothing from responses; their points are verified in memory but never persisted or published (§4.9).

### 4.4 Rider API

All routes below except registration require `requireRider`. Bodies are JSON with `Content-Type: application/json` (415 otherwise), 64 KiB cap, `DisallowUnknownFields`, exactly one object. Errors use the existing `{"error": "..."}` shape. Ride ids are UUIDs; `{id}` that is not a UUID → `404`. A ride belonging to a different rider → `404` (not `403`, to avoid confirming ids). When rider mode is disabled the routes are not registered (`404`).

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

`trip_id` required, ≤ 100 chars, must exist in the current index (`404 {"error":"unknown trip"}`); `start_date` optional `YYYYMMDD` (defaults to `Index.ServiceDate(now)`), and the trip must be active on it (`422 {"error":"trip not active on start_date"}`); `route_id` optional and, when present, must equal the index's route for the trip (`422`); the rest optional, ≤ 100 chars, stored verbatim. One active ride per rider: starting a new one first finishes the previous one with reason `superseded` (same path as any other end, including reputation). Response `201`:

```json
{"ride_id":"c9e2…","state":"pending","report_interval_seconds":5,"max_batch_size":12,
 "destination":{"stop_id":"1_75414","latitude":47.61,"longitude":-122.33}}
```

`destination` is present only when `destination_stop_id` was supplied and appears in the trip's stop times; its coordinate comes from GTFS so the SDK can geofence it without the host app supplying coordinates.

**Positions** request (1–`max_batch_size` points; a batch larger than `max_batch_size` → `400`):

```json
{"positions":[{"latitude":47.60,"longitude":-122.33,"accuracy":8.0,"speed":9.2,"bearing":184.0,"timestamp":1756800000}]}
```

`latitude`/`longitude`/`timestamp` required; `accuracy`, `speed`, `bearing` optional (`*float64`). Points are sorted by timestamp before verification. Response `200`:

```json
{"state":"verified","published":true,"corroboration":"corroborated","accepted":3,"ignored":0,
 "off_route_streak":0,"ended":false,"end_reason":""}
```

`published` is the ride's individual publishability at the time of the response (§4.7; consensus is not reflected). `corroboration` is the latest accepted point's value. When the batch ends the ride (rejection) the response is still `200` with `"ended":true` and `end_reason` ∈ `off_route`, `contradicted`, `implausible`, `off_schedule`. Uploading to an ended or unknown ride → `409 {"error":"ride ended"}`. Per-rider rate limit: one batch per 2 s, burst 2, keyed on rider id, `429` on excess, using `NewKeyedRateLimiter(interval time.Duration, burst int)` — a generalisation of `VehicleRateLimiter` whose existing zero-arg constructor keeps its 5 s / 1 behaviour.

**End ride** request `{"reason":"user_requested"}`; allowed reasons: `user_requested`, `arrived`, `stationary`, `max_duration`, `location_unavailable`, `authorization_denied`, `network_failure`, `app_terminated`. Response `200 {"status":"ride ended","summary":{"points":142,"matched":139,"corroborated":88,"duration_seconds":1260}}`. Ending an already-ended ride → `409`.

**Trip status** (`start_date` optional, defaults as above) response: `{"trip_id":"…","start_date":"…","trusted":true,"rider_reported":false,"riders":0}`. `rider_reported` is true when a publishable estimate exists; `riders` counts contributing sessions. Lets a host app show "shared by another rider" the way Transit does. Unknown trip → `404`.

### 4.5 Point verification rules (`rider.Verify`)

Applied in order; the first failing rule sets the outcome.

1. **Ignored** (not counted toward any streak, not persisted): latitude/longitude out of range or both zero; `accuracy > 100` m; timestamp outside `[now − 10 min, now + 30 s]`; timestamp not newer than `Prev` (duplicates and reordered batches).
2. **OffRoute**: `DistanceToShape > Thresholds.MaxShapeDistance + max(accuracy, 0)`. Projection uses `Prev.AlongShape` as the hint when `Prev` exists.
3. **Implausible**: with `Prev`, `along − Prev.AlongShape < −50 m` (regression beyond noise), or `|along − Prev.AlongShape| / Δt > Thresholds.MaxSpeed`.
4. **OffSchedule**: scheduled time at `along` is interpolated linearly between the bracketing `StopTimeInfo` entries (using `Departure` of the earlier and `Arrival` of the later); before the first stop or after the last, the nearest stop's time is used. `ScheduleDeviation = pointTime − (serviceDateMidnight + scheduledOffset)` in the agency timezone, where `serviceDateMidnight` is 12:00 local of the day before `start_date` plus 12 h (the GTFS "noon minus 12 h" rule). Must be within `[−Thresholds.ScheduleEarly, +Thresholds.ScheduleLate]`.
5. **Matched** otherwise.

Corroboration is computed only for `Matched` points:
- `Unavailable` when `in.Trusted == nil` (no feed configured, or no vehicle for the trip key).
- Otherwise project `Trusted.Pos` onto the shape (no hint). `gap = |along − trustedAlong|`, `allowance = 150 m + Thresholds.MaxSpeed × |pointTime − Trusted.Timestamp|`.
- `Corroborated` if `gap ≤ allowance`; `Contradicted` if `gap > allowance + 500 m`; `None` in between.

### 4.6 Ride state machine (`rider.Session`)

```
Pending ──3 consecutive Matched──▶ Verified
Pending|Verified ──5 consecutive OffRoute|Implausible|OffSchedule──▶ Rejected(off_route|implausible|off_schedule, whichever was most frequent in the streak)
Pending|Verified ──3 consecutive Contradicted──▶ Rejected(contradicted)
Verified ──12 Corroborated points in total──▶ Verified, Corroborated=true (sticky until end)
any ──End(reason)──▶ ended (state preserved for the summary)
```

`Matched` resets the off-route streak; `Corroborated` resets the contradiction streak; `Ignored` points touch nothing; `Contradicted` also counts as a matched point for the verification streak (the geometry matched; the feed disagreed). A ride is **fresh** while `now − Latest().Timestamp ≤ Thresholds.PointMaxAge`. The aggregator's `Reap` ends sessions with no accepted point for 15 min (`idle`) or started more than 3 h ago (`max_duration`).

`Session.Apply` is called with `Prev` = the session's own latest accepted point, which lives in memory only; because startup ends every active ride, a session never needs to reload its history from the database.

### 4.7 Reputation (`rider.ScoreDelta`, `rider.TierFor`)

At ride end the summary yields a score delta:

| Ride outcome | Delta |
|---|---|
| Corroborated and ≥ 5 min between first and last matched point | +1 |
| Verified, not corroborated (feed unavailable or no vehicle) | 0 |
| Ended while still `Pending` | 0 |
| Rejected: `off_route` / `implausible` / `off_schedule` | −1 |
| Rejected: `contradicted` | −3 |

The delta is applied for every end reason, including `superseded` and `idle`; the bulk `server_restart` update at startup applies no delta. Score is clamped to `[−10, 10]`. Tiers: `Blocked` when score ≤ −3; `Trusted` when score ≥ 3; else `New`. The rider's tier is recomputed and written with the score in the same statement, and pushed to the aggregator with `SetTier` so a tier change affects the rider's next ride immediately.

Individual publishability of a ride = `State == Verified && !ended && fresh && tier != Blocked && (tier == Trusted || Corroborated)`.

### 4.8 Aggregation and feed merge

`Aggregator.Estimates(now, covered)` groups **contributing** sessions (`Verified`, fresh, tier ≠ `Blocked`) by `TripKey`:

- A group is **publishable** if any member is individually publishable, or if ≥ 2 members from distinct rider ids have latest along-shape positions within 100 m of each other (consensus).
- Groups whose key is `covered` (trusted feed reports the trip, §4.2 `Covers`) are skipped entirely: the trusted feed wins.
- Estimate: along-shape **median** of members' latest points; members farther than 100 m from that median are excluded and the median recomputed once; position = `Shape.PointAt(median)`; bearing = `Shape.BearingAt(median)`; speed = median of members' reported speeds (omitted when none reported); timestamp = newest member timestamp, clamped to `now` (E050); `current_stop_sequence` / `stop_id` = the first `StopTimeInfo` with `AlongShape ≥ median` (omitted past the last stop), `current_status = IN_TRANSIT_TO`.
- `TripEstimate{Key TripKey; RouteID string; Lat, Lon, Bearing float64; Speed *float64; Timestamp time.Time; StopID string; StopSequence int; Riders int}`.

Feed merge, in `package main`:

- `buildFeed(vehicles []*VehicleState, estimates []rider.TripEstimate) *gtfs.FeedMessage` — driver entities are built exactly as today; rider entities are appended with `id = "rider:" + trip_id + ":" + start_date`, `vehicle.id = "rider:" + trip_id + ":" + start_date` (the same string as the entity id: the same trip on two service dates is two vehicles, and E052 requires vehicle.id to be unique), `vehicle.label = "Rider-reported"`, `trip.trip_id`, `trip.route_id`, `trip.start_date`, snapped `position` (lat, lon, bearing, optional speed), `timestamp`, `current_stop_sequence`, `stop_id`, `current_status`. Header timestamp remains `max(now, all entity timestamps)` (E012). Driver vehicle ids match `^[a-zA-Z0-9._-]+$` and can never contain `:`, so E052 uniqueness holds by construction.
- `handleGetFeed(tracker *Tracker, estimates estimateSource)` where `type estimateSource interface{ Estimates(now time.Time) []rider.TripEstimate }`; `nil` (mode disabled) means no rider entities. `riderRuntime` implements it by calling the aggregator with `trusted.Covers`.
- Query `source=driver|rider|all` (default `all`); any other value → `400 {"error":"invalid source"}`. `format` is parsed independently as today.

Snapping to the shape also serves privacy: the published position is the vehicle's estimated position on the route, never a raw rider fix.

### 4.9 Store additions

Migration `000011_riders.up.sql` / `.down.sql` (the sequence intentionally skips 000007; 000010 is the latest):

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
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), ended_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rides_rider_status ON rides(rider_id, status);
CREATE INDEX idx_rides_trip ON rides(trip_id, start_date);
CREATE INDEX idx_rides_started ON rides(started_at DESC);
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

Fixed-shape queries go through sqlc in `db/query.sql`: `UpsertRider` (`ON CONFLICT (installation_id) DO UPDATE SET last_seen_at = NOW(), app_version = EXCLUDED.app_version RETURNING *, (xmax = 0) AS created`), `GetRider`, `TouchRider`, `ApplyRideOutcome` (score/tier/counters in one `UPDATE … RETURNING`), `CountRidersByTier`, `InsertRide`, `GetRide`, `EndRide` (`status='ended', ended_at, end_reason, state, corroborated, points_*`), `UpdateRideProgress` (state, corroborated, points_*), `EndAllActiveRides`, `ListRides` (`status`, `limit`, `offset`), `CountRides`, `InsertRidePoint`, `DeleteRidePointsBefore`. `rider_store.go` exposes narrow interfaces `RiderRegistrar`, `RiderReader`, `RideStarter`, `RidePointRecorder`, `RideFinisher`, `RideLister`, `RiderStatsReader`, all added to `appStore` and to `route_wiring_test.go`'s `noopStore`. Point inserts use `pgx.Batch` inside the transaction.

Ingest path per batch (`handleRiderPositions`): rate limit → load ride/rider from the aggregator (rider tier is cached on the session; the DB is not read per batch) → `ApplyBatch` under the session lock → one transaction: insert non-ignored points (skipped entirely for `Blocked` riders), `UpdateRideProgress`, `TouchRider` → respond. If the transaction fails the handler returns `500`; the in-memory session has already advanced, which is acceptable because the next batch re-persists counters (`UpdateRideProgress` writes absolute values) and the missing points only affect history, not the feed. If the batch ended the ride, `finishRide` (below) runs after the commit.

`finishRide(ctx, session, reason)` in `rider_handlers.go` is the single end path used by the end endpoint, batch rejection, `superseded`, and reaping: `Aggregator.Remove` → `EndRide` + `ApplyRideOutcome` in one transaction → `SetTier`. Failures are logged; reaping retries on the next tick because the session is removed only after a successful commit.

Retention: an hourly ticker runs `DeleteRidePointsBefore(now − RIDER_POINT_RETENTION)`. Ride and rider rows are kept.

### 4.10 Admin endpoints

Both `authMiddleware(adminMiddleware(...))`, registered even when rider mode is disabled (status then reports `{"enabled":false}` and the list returns an empty page).

- `GET /api/v1/admin/rider/status` → `{"enabled":true,"gtfs":{"source":"…","loaded_at":"…","trips":1234,"shapes":210,"timezone":"America/Los_Angeles"},"trusted_feeds":[{"url":"…","last_success":"…","last_error":"","entities":87}],"riders":{"total":40,"trusted":6,"blocked":1},"rides":{"active":3,"publishable":2}}`.
- `GET /api/v1/admin/rider/rides?status=active|ended&limit=50&offset=0` (limit 1–200, default 50; `status` default `active`) → `{"count":n,"has_more":b,"rides":[{"id","rider_id","trip_id","start_date","route_id","state","corroborated","status","end_reason","points_total","points_matched","points_corroborated","started_at","ended_at"}]}`.

### 4.11 Rider simulator (`cmd/ridersim`)

Separate `package main`. Flags: `-url` (default `http://localhost:8080`), `-gtfs` (zip path or URL; default `rider/testdata/fixture.zip`), `-trip` (repeatable), `-random N` (pick N trips active today), `-start-date` (default today), `-interval 5s`, `-speed 10` (m/s along the shape), `-noise 8` (metres of Gaussian jitter), `-offroute-after 0s` (when > 0, leave the shape by 300 m after that long, to exercise rejection), `-riders-per-trip 1`, `-duration 0` (0 = until the trip's shape ends). Each rider registers, starts the ride, walks the shape at the configured speed, uploads batches of the reported size at the reported interval, prints every state transition and the final summary, and ends on the last shape point with reason `arrived`. Reuses `rider.ShapeGeom` and `rider.LoadStatic`. Exit code is non-zero if any ride ends with a reason other than the expected one (`-expect-end arrived|off_route`).

### 4.12 iOS SDK — `VehiclePositionsKit`

SwiftPM package at `ios/VehiclePositionsKit/`, `swift-tools-version: 6.0`, `platforms: [.iOS(.v18)]`, `swiftLanguageModes: [.v6]`, package default isolation left at `nonisolated` (matching OBAKitCore; the host app is `MainActor`-by-default and calls into the actor with `await`). One library product `VehiclePositionsKit`, targets `VehiclePositionsKit` and `VehiclePositionsKitTests` (Swift Testing). All public types are `Sendable`. Production CoreLocation/Keychain implementations are wrapped in `#if os(iOS)` so `swift build` on the macOS host still compiles the protocol layer; tests run on the simulator via XcodeBuildMCP or `xcodebuild test -scheme VehiclePositionsKit -destination 'platform=iOS Simulator,name=<sim>'` (the scheme is auto-generated from the package name).

**Public surface**

```swift
public struct RideReporterConfiguration: Sendable {
    public var serverURL: URL
    public var appID: String
    public var appVersion: String
    public var maxRideDuration: Duration = .seconds(3 * 3600)
    public var stationaryTimeout: Duration = .seconds(10 * 60)
    public var arrivalRadiusMeters: Double = 75
    public var minimumTravelBeforeArrivalMeters: Double = 200
    public var uploadFailureTimeout: Duration = .seconds(5 * 60)
    public init(serverURL: URL, appID: String, appVersion: String)
}
// reportInterval and maxBatchSize are not configurable: the server dictates them per ride.

public struct TripDescriptor: Sendable, Codable, Equatable {
    public var tripID: String
    public var startDate: String?            // YYYYMMDD in agency time; nil = server default
    public var routeID: String?
    public var vehicleID: String?
    public var boardingStopID: String?
    public var destinationStopID: String?
}

// Both enums carry `unknown` for forward compatibility: a tolerant `init(from:)`
// maps any raw value this build does not know to `.unknown` rather than throwing.
public enum RideState: String, Sendable, Codable { case pending, verified, rejected, unknown }
public enum Corroboration: String, Sendable, Codable { case unavailable, none, corroborated, contradicted, unknown }
public enum RideEndReason: String, Sendable, Codable {
    case userRequested = "user_requested", arrived, stationary, maxDuration = "max_duration",
         locationUnavailable = "location_unavailable", authorizationDenied = "authorization_denied",
         networkFailure = "network_failure", appTerminated = "app_terminated",
         offRoute = "off_route", contradicted, implausible, offSchedule = "off_schedule",
         superseded, serverRestart = "server_restart", idle
}
public struct RideProgress: Sendable, Equatable {
    public var state: RideState; public var published: Bool; public var corroboration: Corroboration
    public var pointsAccepted: Int; public var offRouteStreak: Int
}
public struct RideSummary: Sendable, Codable, Equatable { points, matched, corroborated: Int; durationSeconds: Int }
public struct TripStatus: Sendable, Codable, Equatable { tripID, startDate: String; trusted, riderReported: Bool; riders: Int }
public enum RideWarning: Sendable, Equatable { case uploadRetrying(attempt: Int), accuracyLimited, insufficientlyInUse, batchRejected(status: Int) }
public enum RideEvent: Sendable, Equatable {
    case registered(riderID: String)
    case started(rideID: String)
    case progress(RideProgress)
    case warning(RideWarning)
    case ended(RideEndReason, summary: RideSummary?)
}
public enum RideError: Error, Sendable, Equatable { case notAuthorized, server(status: Int, message: String), transport(String), alreadyEnded, decoding(String) }

public actor RideReporter {
    public init(configuration: RideReporterConfiguration,
                transport: any RideTransport,
                locationSource: any LocationSource,
                credentialStore: any CredentialStore,
                clock: any RideClock = ContinuousRideClock())
    #if os(iOS)
    public init(configuration: RideReporterConfiguration)   // URLSession + CoreLocation + Keychain
    #endif
    public var currentState: RideState? { get }
    public func start(_ trip: TripDescriptor) async throws -> AsyncStream<RideEvent>
    public func end(reason: RideEndReason = .userRequested) async
    public func tripStatus(tripID: String, startDate: String?) async throws -> TripStatus
}
```

Seams (all `Sendable` protocols):
- `RideTransport`: `func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse` where `RiderRequest{method, path, query: [String: String], body: Data?, bearerToken: String?}` and `RiderResponse{status: Int, body: Data}`. `path` is already percent-encoded (trip and ride ids are escaped with `.urlPathAllowed` minus `/`), so a transport must not escape it again. `RiderClient`, which builds these five requests, is **internal**: `RideReporter` is the public surface. `URLSessionRideTransport` uses a regular `URLSession` (10 s request timeout); background sessions are wrong for a 5 s cadence. Because every request carries a bearer token, the installation id, or a precise position, the transport rejects a non-`https` URL before building the request, and follows a redirect only when it stays on the same scheme, host and port — a cross-origin or downgrading redirect is refused rather than followed with the credentials stripped. `URLSessionRideTransport.insecureForDevelopment()` is the one way to reach a local `http://` server.
- `LocationSource`: `func updates() -> AsyncThrowingStream<LocationSample, Error>` and `func beginBackgroundActivity() -> any BackgroundActivityHandle` (`invalidate()`); `LocationSample{coordinate, horizontalAccuracy, speed, course, timestamp, isStationary, diagnostic: LocationDiagnostic?}` with `LocationDiagnostic` ∈ `authorizationDenied, locationUnavailable, accuracyLimited, insufficientlyInUse`. `CoreLocationSource` iterates `CLLocationUpdate.liveUpdates(.otherNavigation)`, maps `update.stationary` and the iOS 18 diagnostic Bools, creates a `CLServiceSession(authorization: .whenInUse)` and a `CLBackgroundActivitySession` (both retained for the ride, invalidated on end).
- `CredentialStore`: `load() -> RiderCredentials?`, `save(_:)`, `clear()` with `RiderCredentials{installationID, riderID, token}`; `KeychainCredentialStore` (service `org.onebusaway.vehiclepositionskit`) in production, `InMemoryCredentialStore` in tests.
- `RideClock`: `var now: Date` (wall clock — a jump may make a timer fire once early or late, which is accepted for a ride that lasts minutes) plus `sleep(for:)`; `ManualRideClock` in tests drives batching, stationary and max-duration timers deterministically.

**Behaviour**

1. `start` must be called while the app is in the foreground (a `CLBackgroundActivitySession` can only be created there; this is documented, not enforced). If a ride is active it is ended first with `.superseded`. The reporter loads credentials, registers when none exist (or re-registers once after a `401`), calls start-ride, stores `report_interval_seconds`, `max_batch_size` and `destination`, begins the background activity, and consumes `updates()`. Authorization prompting is CoreLocation's: iterating `liveUpdates` prompts if needed; hosts are advised to request When-In-Use up front for better UX. When-In-Use is sufficient; Always is not requested.
2. Samples are buffered; every `report_interval_seconds` or when `max_batch_size` samples are buffered, a batch is uploaded. Failed uploads retry with exponential backoff (1, 2, 4, … 30 s cap) and the buffer keeps the newest 10 minutes of samples. After `uploadFailureTimeout` of continuous failure the ride ends locally with `.networkFailure` (the server reaps it as `idle`).
3. Each positions response updates `currentState` and emits `.progress`; `"ended": true` ends the ride locally with the server's reason. A `409` ends the ride with `.serverRestart` (the server no longer knows it). A 4xx other than `401`, `409` and `429`, and a response this build cannot decode, **drop** the batch — it is not restored to the buffer and does not count against the retry budget — and emit `.warning(.batchRejected(status:))` (`0` for a decoding failure); `429`, 5xx and transport failures keep the retry-with-backoff path.
4. The reporter ends the ride itself on: the destination coordinate coming within `arrivalRadiusMeters` after the ride has travelled ≥ `minimumTravelBeforeArrivalMeters` straight-line from its first fix; `isStationary` persisting for `stationaryTimeout` (judged on the clock in the upload tick, not only on arriving samples: `liveUpdates` pauses while the device is still, so a second stationary sample may never come); `maxRideDuration`; an `authorizationDenied` or `locationUnavailable` diagnostic (`.authorizationDenied` / `.locationUnavailable`); the host calling `end`. `accuracyLimited` and `insufficientlyInUse` are surfaced as warnings only.
5. Ending always performs one final flush — at most two batches, so a device with ten minutes of buffered fixes cannot outrun the budget — and the end-ride call (best effort, 10 s total, raced against `clock.sleep`; a teardown that loses the race keeps running detached while `end` returns), emits `.ended`, finishes the stream, and invalidates the background activity.
6. The stream returned by `start` finishes exactly once, after `.ended`. Registration and ride lifecycle never include any identifier other than the random installation UUID; the SDK exposes no way to attach user identity.

**Host integration notes** (documented in the package README, not implemented here): add `location` to `UIBackgroundModes` (the OneBusAway app currently has only `remote-notification`); `NSLocationWhenInUseUsageDescription` is already declared; the blue location pill is shown for the duration of the ride, matching Transit's visible-while-active behaviour; recreate the reporter when the region (server URL) changes, exactly as the app does for `RESTAPIService`.

**Testing**: Swift Testing suites for the JSON codec (every request/response in §4.4), the batching/retry scheduler (manual clock), the end-condition evaluator (arrival with and without the travel guard, stationary, max duration, diagnostics), the state mirror, credential persistence and re-registration on `401`, and a full scripted ride against `FakeRideTransport` (scripted responses, recorded requests) and `FakeLocationSource` (an `AsyncStream` the test feeds). Strict-concurrency clean.

---

## 5. Component boundaries

| Unit | Does | Depends on | Consumed by |
|---|---|---|---|
| `rider.Index` / `ShapeGeom` | GTFS lookups, projection, schedule interpolation, service dates | go-gtfs | Verify, Aggregator, handlers (start-ride validation), ridersim |
| `rider.TrustedFeed` | Poll + snapshot trusted VehiclePositions, coverage lookup | gtfs-realtime-bindings, `net/http` client | handlers (lookup), feed merge (`Covers`), admin status |
| `rider.Verify` | Pure per-point verdict | `TripInfo`, `Thresholds` | Aggregator |
| `rider.Session` | Per-ride streaks and state | verdicts | Aggregator |
| `rider.Aggregator` | Session registry, batch application, estimates, reaping | Session, Verify, ShapeGeom | handlers, `estimateSource`, admin status |
| `rider_store.go` | Persistence of riders/rides/points, reputation write | pgx, sqlc | handlers, wiring |
| `rider_auth.go` | Rider JWT issue/verify, `requireRider`, role check in `requireAuth` | jwt | `newMux` |
| `rider_handlers.go` | HTTP surface, `finishRide`, rate limits | store, engine, auth helpers | `newMux` |
| `rider_wiring.go` | Config, runtime construction, tickers, startup cleanup, `estimateSource` | all above | `main.go`, tests |
| `cmd/ridersim` | Replays shapes as riders | `rider` (shape, loader), `net/http` | smoke tests, `/go` |
| `VehiclePositionsKit` | Phone-side capture, batching, lifecycle | CoreLocation, Foundation, Security | Host apps |

---

## 6. Testing

- **Engine**: table-driven unit tests per file, synthetic in-memory GTFS, no network. Projection tests include the loop route with a hint, a point equidistant from two segments, `PointAt`/`BearingAt` round trips, and `shape_dist_traveled` rescaling. Index tests cover `ActiveOn` (weekday, date range, added and removed dates), `ServiceDate` around 03:00, trips without shapes, and immutability across a refresh. Verify tests cover every outcome and every corroboration result, including the timestamp window and accuracy cut-off. Session tests walk every transition in §4.6. Reputation tests cover every row of §4.7 and clamping. Aggregator tests cover a single trusted rider, a single corroborated new rider, two-rider consensus, outlier exclusion, suppression by `covered`, freshness expiry, blocked riders, reaping and `ApplyBatch` ordering.
- **Trusted feed**: `httptest.Server` serving protobuf with `ETag` / `304`, entities with and without `start_date`, stale entities, and an erroring URL alongside a healthy one.
- **Handlers**: `httptest` with a fake store; registration idempotency and rate limiting; rider JWT role isolation (rider token → `403` on `/api/v1/locations` and admin routes; driver token → `403` on rider routes; cookie never accepted on rider routes); start-ride validation and `superseded`; positions batch verdicts, streak-driven ending, `409` after end, batch-size cap, rate limit; end-ride summary; trip status; feed `source` filter and rider entity shape; admin status and list, including the disabled case.
- **Store**: `newTestStore(t)` integration tests behind `DATABASE_URL` for every query, cascade deletes, retention delete, `EndAllActiveRides`, `ApplyRideOutcome` clamping.
- **Feed compliance**: `validateFeedCompliance` runs on a merged feed containing driver and rider entities.
- **Simulator + smoke**: `cmd/ridersim` against a local server loaded with `rider/testdata/fixture.zip`; the server's own driver half (fed by a driver JWT + `curl` on the same trip) needs no `TRUSTED_GTFS_RT_URLS`: `rider_wiring.go` passes the local `Tracker` to `trustedSources`, which consults it whenever no configured feed covers the trip. Asserts: a rider ride becomes corroborated while the driver reports, the rider entity is suppressed while the driver reports, and it appears within one poll interval after the driver stops; an `-offroute-after` rider is rejected.
- **SDK**: Swift Testing on the simulator as in §4.12; strict concurrency clean.

---

## 7. Risks

- **GTFS memory**: whole-feed parse with go-gtfs. Large agencies (King County Metro ~300 MB unzipped) take seconds and hundreds of MB transiently. The index keeps only trips, stop times and shapes and drops `gtfs.Static` after build. If this becomes a problem, transitland-lib's streaming reader is the fallback (GPL).
- **Trusted entities without `start_date`** are matched to any service date for that trip id. A post-midnight trip could be suppressed by yesterday's instance for up to `TRUSTED_FEED_MAX_AGE`. Accepted: suppression errs on the side of publishing less.
- **Colluding devices** can reach consensus without corroboration. Mitigations in v1: distinct rider ids, both non-blocked, estimates still suppressed whenever the trusted feed reports the trip, and reputation penalties when the feed later contradicts. App Attest is the v2 mitigation.
- **Overlapping routes and wrong-direction choices**: the host app chooses the trip; the schedule window and the along-shape monotonicity catch most wrong choices, and a rejected ride costs the rider one point.
- **Battery**: `liveUpdates` has no accuracy knobs; `.otherNavigation` and automatic stationary pausing are the only levers. Measured against Transit's 5% per 20 min benchmark by the host app, not the SDK.
- **Shared JWT secret**: rider tokens use the same secret as driver/admin tokens, so the role checks in `requireAuth` and `requireRider` are the only separation. The negative tests in §6 guard them.
- **In-memory session advances before the DB commit** (§4.9): a failed commit loses at most one batch of history; counters are re-persisted on the next batch.
