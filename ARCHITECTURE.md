# Architecture

This document describes the architecture of **vehicle-positions**, a service that ingests real-time vehicle location reports and generates a GTFS-Realtime Vehicle Positions feed.

It outlines the system's core components, module structure, data flow, and how the service processes location reports to produce the live feed consumed by transit applications.

---

## Table of Contents

1. [Design Goals](#1-design-goals)
2. [Core Concepts](#2-core-concepts)
3. [System Overview](#3-system-overview)
4. [Component Architecture](#4-component-architecture)
5. [Data Flow](#5-data-flow)
6. [Module Overview](#6-module-overview)
7. [Extending the System](#7-extending-the-system)
8. [Rider Mode](#8-rider-mode)

---

## 1. Design Goals

### 1.1 Problem Statement

Transit agencies and GTFS-Realtime consumers require a reliable, low-latency source of vehicle location data. Driver applications periodically submit GPS location reports via an HTTP API, and the system must validate and persist these reports while making the latest vehicle positions available to feed consumers with minimal latency.

### 1.2 Scope

This document describes the system as it exists today. The service is intentionally minimal: it accepts location reports, stores them in PostgreSQL, maintains an in-memory state of current positions, and exposes a GTFS-Realtime feed endpoint plus simple status and liveness endpoints. Nothing beyond this is implemented.

### 1.3 High-Level Requirements

The service must fulfil the following capabilities:

- Accept real-time vehicle location reports from driver applications over HTTP POST.
- Validate each report (required fields, coordinate bounds, positive timestamp, null-island rejection).
- Persist every valid report to PostgreSQL within a transactional write.
- Maintain an in-memory tracker of the latest position for each active vehicle.
- Serve a GTFS-Realtime 2.0 compliant `FeedMessage` (protobuf or JSON) to consumers.
- Exclude stale vehicles (older than `STALENESS_THRESHOLD`) from the live feed.
- Expose an admin status endpoint reporting active vehicle counts, total tracked vehicles, last update time, and uptime.
- Expose a `/health` liveness probe.
- Seed the in-memory tracker from the database on startup using recently received points.
- Shut down gracefully on `SIGINT` / `SIGTERM` with a 10-second drain window.

---

## 2. Core Concepts

### 2.1 Architectural Style

The service follows a single-binary, layered architecture with no external web framework dependencies beyond the Go standard library. All components run in one process. The design separates concerns across three thin layers:

- **HTTP Handlers** (`handlers.go`) — parse requests, validate input, coordinate store and tracker, and serialize responses.
- **Store** (`store.go`) — wraps PostgreSQL via `pgxpool`; runs embedded migrations and sqlc-generated queries inside transactions.
- **Tracker** (`tracker.go`) — an in-memory map protected by `sync.RWMutex`, cleaned up on a background ticker.

There is no service layer, repository pattern, or middleware chain. Handlers call the store and tracker directly.

### 2.2 Key Architectural Principles

- **Feed generation from memory, not the database.** The GTFS-RT feed is built entirely from the in-memory Tracker on every request. This keeps feed latency low and independent of database load.
- **Server-side freshness clock.** `VehicleState.UpdatedAt` records server time (`time.Now()`) when the report is stored in memory, not the vehicle-reported epoch timestamp. Feed filtering and active_vehicles calculations use this server-side freshness clock, and last_update reports the most recent in-memory update time.
- **Startup seeding for continuity.** On startup, the tracker is seeded from PostgreSQL using GetRecentLocations() with a recent cutoff so recently active vehicle state is preserved across server restarts. If seeding fails, startup continues and a warning is logged.
- **Staleness enforced at two points.** `ActiveVehicles()` filters stale vehicles at read time, and a background goroutine also evicts stale entries on a ticker interval, preventing unbounded memory growth.

---

## 3. System Overview

### 3.1 Technology Stack

| Attribute | Value |
|-----------|-------|
| Language | Go 1.24.7 |
| HTTP Server | Go stdlib `net/http` with `ServeMux` |
| Database | PostgreSQL 17, accessed via `github.com/jackc/pgx/v5` and `pgxpool` |
| Query Layer | sqlc-generated typed query package (`db/`) |
| Migrations | `github.com/golang-migrate/migrate/v4` with embedded SQL migration files |
| Feed Format | GTFS-Realtime 2.0 protobuf, with optional JSON serialization for debugging |
| GTFS-Realtime Bindings | `github.com/OneBusAway/go-gtfs/proto` v1.1.1 (the same generated bindings, re-exported by the GTFS library the rider engine already uses; MobilityData's `gtfs-realtime-bindings` registers the same protobuf file names, so linking both panics the protobuf registry at init) |
| Containerization | Multi-stage Docker build (`golang:1.24-alpine` -> `alpine:3.21`) plus Docker Compose with `postgres:17-alpine` |

### 3.2 System Actors

| Actor | Interaction |
|-------|-------------|
| Driver Application | Sends periodic `POST /api/v1/locations` requests containing JSON vehicle location reports. |
| GTFS-RT Consumer | Polls `GET /gtfs-rt/vehicle-positions` to retrieve the current vehicle positions feed as protobuf or JSON. |
| Operator / Monitor | Queries `GET /api/v1/admin/status` to inspect uptime, active vehicle count, total tracked vehicles, and last update time. |
| Infra / Orchestrator | Polls `GET /health` as a liveness check for container platforms, load balancers, or process supervisors. |

### 3.3 Database Schema

The core schema consists of two tables. Each `location_points` row references a `vehicles` row through `vehicle_id`, giving a one-to-many relationship from vehicles to recorded points. The schema enforces a non-empty vehicle id, latitude and longitude bounds, and a positive epoch `timestamp`. It also defines defaults for `label`, `trip_id`, `created_at`, `updated_at`, and `received_at`, and adds indexes on `location_points.vehicle_id` and `location_points.timestamp` (see `migrations/000001_init_schema.up.sql`).

```mermaid
erDiagram
  vehicles {
    TEXT id PK
    TEXT label
    TIMESTAMPTZ created_at
    TIMESTAMPTZ updated_at
  }
  location_points {
    BIGSERIAL id PK
    TEXT vehicle_id FK
    TEXT trip_id
    FLOAT8 latitude
    FLOAT8 longitude
    FLOAT8 bearing
    FLOAT8 speed
    FLOAT8 accuracy
    BIGINT timestamp
    TIMESTAMPTZ received_at
  }
  vehicles ||--o{ location_points : records
```

The ER diagram shows the structural relationship only. Nullability, defaults, and `CHECK` constraints are enforced in the SQL schema rather than encoded directly in the diagram.

Rider mode (§8) adds three more tables in `migrations/000011_riders.up.sql`.
The migration always runs, so the tables exist whether or not
`RIDER_MODE_ENABLED` is set; they simply stay empty when it is not. A `rides`
row belongs to one `riders` row and owns the `ride_points` behind it, both with
`ON DELETE CASCADE`, so forgetting a rider forgets everything reported under
that installation.

```mermaid
erDiagram
  riders {
    TEXT id PK
    TEXT installation_id UK
    TEXT platform
    TEXT app_id
    TEXT app_version
    BOOLEAN attested
    INTEGER score
    TEXT tier
    INTEGER rides_total
    INTEGER rides_corroborated
    INTEGER rides_rejected
    TIMESTAMPTZ created_at
    TIMESTAMPTZ last_seen_at
  }
  rides {
    TEXT id PK
    TEXT rider_id FK
    TEXT trip_id
    TEXT start_date
    TEXT route_id
    TEXT vehicle_id
    TEXT boarding_stop_id
    TEXT destination_stop_id
    TEXT status
    TEXT state
    BOOLEAN corroborated
    TEXT end_reason
    INTEGER points_total
    INTEGER points_matched
    INTEGER points_corroborated
    INTEGER points_contradicted
    TIMESTAMPTZ started_at
    TIMESTAMPTZ ended_at
    TIMESTAMPTZ updated_at
  }
  ride_points {
    BIGSERIAL id PK
    TEXT ride_id FK
    FLOAT8 latitude
    FLOAT8 longitude
    FLOAT8 accuracy
    FLOAT8 speed
    FLOAT8 bearing
    BIGINT timestamp
    TEXT outcome
    TEXT corroboration
    FLOAT8 along_shape
    FLOAT8 distance_to_shape
    INTEGER schedule_deviation_seconds
    TIMESTAMPTZ received_at
  }
  riders ||--o{ rides : reports
  rides ||--o{ ride_points : records
```

`riders` holds no directly identifying data — no name, account or contact — but
it is pseudonymous rather than anonymous: the installation id generated on the
device is stable for as long as the app keeps its keychain item, and every
`rides` row is linked to it. So the rows are a per-device travel history, and
should be treated as personal data even though no field names a person.

Only `ride_points`, where the raw rider fixes live, expires: those rows are
deleted after `RIDER_POINT_RETENTION` (default seven days) by an hourly sweep.
`riders` and `rides` are kept indefinitely — `rides` keeps the trip, route and
boarding stop of every ride that device has reported. Both are readable through
the admin API, so an operator holding admin credentials can read any device's
ride history; deleting a rider's data means deleting those rows directly.

---

## 4. Component Architecture

### 4.1 Component Architecture Diagram

```mermaid
graph TD
  DA[Driver Application] -->|POST /api/v1/locations| H[HTTP Handlers]
  GC[GTFS-RT Consumer] -->|GET /gtfs-rt/vehicle-positions| H
  OM[Operator / Monitor] -->|GET /api/v1/admin/status| H
  IO[Infra / Orchestrator] -->|GET /health| H

  H -->|SaveLocation tx| S[(PostgreSQL Store)]
  H -->|tracker.Update| T[In-Memory Tracker]

  H -->|tracker.ActiveVehicles| T
  T -->|active vehicles| H
  H -->|build and serialize feed| GC

  H -->|tracker.Status| T
  T -->|status snapshot| H
  H -->|admin status JSON| OM

  H -->|health JSON| IO

  S -.->|GetRecentLocations on startup| T
```

> **Note:** The Tracker is seeded from the database on startup to preserve active vehicle state across server restarts. If seeding fails, startup continues and a warning is logged.

### 4.2 Core System Components

#### 4.2.1 `main.go` — Entry Point & Wiring

Reads configuration from environment variables (`PORT`, `DATABASE_URL`, `STALENESS_THRESHOLD`, `READ_TIMEOUT`, `WRITE_TIMEOUT`, `IDLE_TIMEOUT`). Initialises the Store, runs migrations, creates the Tracker, seeds the tracker from the database using `GetRecentLocations` with `time.Now().Add(-maxAge)`, reinserts each seeded row through `tracker.Update()`, registers HTTP routes on stdlib's `ServeMux`, and starts the server. Handles graceful shutdown via `signal.NotifyContext` for `SIGINT` and `SIGTERM`, then calls `srv.Shutdown()` with a 10-second timeout.

#### 4.2.2 `handlers.go` — HTTP Handlers

Contains the three handler factories (`handlePostLocation`, `handleGetFeed`, `handleAdminStatus`) plus an internal `buildFeed()` helper used by the feed endpoint. The `/health` liveness endpoint is registered inline in `main.go`. Handlers are plain functions that close over their dependencies (`store`, `tracker`, `startTime`) — no global HTTP state.

#### 4.2.3 `store.go` — PostgreSQL Persistence

Wraps a `pgxpool` connection pool. `Migrate()` runs embedded SQL migrations through `golang-migrate`'s `iofs` source and treats `migrate.ErrNoChange` as success. `SaveLocation()` executes an UPSERT on `vehicles` and an INSERT on `location_points` inside a single transaction with deferred rollback on failure. `GetRecentLocations()` queries the latest position per vehicle since a given cutoff timestamp using `DISTINCT ON (vehicle_id)` ordered by `received_at DESC`.

#### 4.2.4 `tracker.go` — In-Memory Vehicle State

A `map[string]*VehicleState` guarded by `sync.RWMutex`. A background goroutine ticks at `maxAge` intervals to evict entries older than the staleness threshold, preventing unbounded memory growth. `Stop()` uses `sync.Once` so it is safe to call multiple times.

#### 4.2.5 `db/` — sqlc-Generated Layer

The directory contains two source files and three generated files. `query.sql` defines the named SQL queries (`UpsertVehicle`, `InsertLocationPoint`, `GetRecentLocations`), and `sqlc.yml` configures code generation (engine: PostgreSQL, sql package: `pgx/v5`, output package: `db`). Running `sqlc generate` from this directory regenerates the three output files: `db.go` (DBTX interface + `Queries` type), `models.go` (`LocationPoint` and `Vehicle` structs), and `query.sql.go` (typed Go wrappers for each query). The generated files are marked `DO NOT EDIT` — modify `query.sql` or `sqlc.yml` instead and regenerate.

---

## 5. Data Flow

### 5.1 Location Ingestion

When a driver application reports a vehicle position, the following sequence occurs:

1. Driver app sends `POST /api/v1/locations` with JSON body.
2. Handler validates `Content-Type` by parsing the media type and requiring `application/json` (so `application/json; charset=utf-8` is accepted).
3. Handler wraps the body with `http.MaxBytesReader(..., 1<<20)` to enforce a 1 MiB request limit.
4. JSON decoding uses `DisallowUnknownFields()`, rejects malformed JSON, and performs a second decode to ensure there is exactly one JSON object and no trailing data other than whitespace.
5. `LocationReport.validate()` checks: non-empty `vehicle_id`, coordinates not both zero, latitude in `[-90, 90]`, longitude in `[-180, 180]`, positive timestamp.
6. `store.SaveLocation()` opens a PostgreSQL transaction: `UpsertVehicle` (`INSERT ... ON CONFLICT DO UPDATE SET updated_at = NOW()`) then `InsertLocationPoint`.
7. On successful commit, `tracker.Update()` writes the new state to the in-memory map and records a fresh server-side `UpdatedAt` timestamp.
8. The handler responds with `201 Created` and the JSON body `{"status":"ok"}`. If the database write fails, the in-memory tracker is not updated and the handler returns `500 Internal Server Error`.

### 5.2 GTFS Feed Generation

1. Consumer sends `GET /gtfs-rt/vehicle-positions` (optionally `?format=json`).
2. Handler calls `tracker.ActiveVehicles()`, which reads the in-memory map and returns only vehicles whose `UpdatedAt` is newer than `time.Now().Add(-maxAge)`.
3. `buildFeed()` constructs a `gtfs.FeedMessage` with a `FULL_DATASET` header and one `VehiclePosition` entity per active vehicle. `TripDescriptor` carries `trip_id`, `route_id` and `start_date` as sent, and is omitted when both `trip_id` and `route_id` are empty.
4. Response is serialized with `proto.Marshal` (protobuf, default) or `protojson.Marshal` (`?format=json`) and written with the appropriate `Content-Type`.

### 5.3 Sequence Diagrams

#### 5.3.1 Location Ingestion

```mermaid
sequenceDiagram
  participant DA as Driver App
  participant H  as HTTP Handler
  participant S  as Store (PostgreSQL)
  participant T  as Tracker (in-memory)

  DA->>H: POST /api/v1/locations (JSON)
  H->>H: Validate Content-Type
  H->>H: Decode JSON (DisallowUnknownFields)
  H->>H: Ensure single JSON object (no trailing data)
  H->>H: LocationReport.validate()

  alt request is invalid
    H-->>DA: 4xx JSON error response
  else request is valid
    H->>S: SaveLocation(ctx, &loc)
    S->>S: BEGIN TRANSACTION
    S->>S: UpsertVehicle(vehicle_id)
    S->>S: InsertLocationPoint(...)
    S->>S: COMMIT

    alt DB write fails
      H-->>DA: 500 Internal Server Error
    else DB write succeeds
      H->>T: tracker.Update(&loc)
      H-->>DA: 201 Created {"status":"ok"}
    end
  end
```

#### 5.3.2 GTFS-RT Feed Request

```mermaid
sequenceDiagram
  participant C  as Consumer
  participant H  as Handler
  participant T  as Tracker (in-memory)

  C->>H: GET /gtfs-rt/vehicle-positions[?format=json]
  H->>T: tracker.ActiveVehicles()
  T-->>H: []*VehicleState (non-stale only)
  H->>H: buildFeed(vehicles)
  alt format=json
    H->>H: protojson.Marshal(feed)
    H-->>C: 200 OK JSON feed
  else default
    H->>H: proto.Marshal(feed)
    H-->>C: 200 OK protobuf feed
  end
```

---

## 6. Interfaces & Modules

### 6.1 API Endpoints

| Method | Path | Response | Purpose |
|--------|------|----------|---------|
| `POST` | `/api/v1/locations` | `201` / `4xx` / `5xx` JSON | Ingest a vehicle location report. |
| `GET` | `/gtfs-rt/vehicle-positions` | `200` protobuf or JSON | Return GTFS-RT feed of active vehicles. |
| `GET` | `/api/v1/admin/status` | `200` JSON | Return server uptime, active vehicle count, total tracked vehicle count, and last update time. |
| `GET` | `/health` | `200` JSON | Liveness probe returning `{"status":"ok"}`. |

Authentication is per endpoint, not global:

| Surface | Requirement |
|---------|-------------|
| `/health`, `/ready`, `GET /gtfs-rt/vehicle-positions` | None. The feed is public by design. |
| `POST /api/v1/auth/login` | None, but rate limited per client. |
| `POST /api/v1/locations`, `/api/v1/trips/*`, `GET /api/v1/vehicles` | A driver's JWT (`authMiddleware`). |
| `/api/v1/admin/*` — including `/api/v1/admin/rider/status` | A JWT whose user is an admin (`authMiddleware` + `adminMiddleware`). |
| `/api/v1/rider/*` | A rider JWT, issued by `POST /api/v1/rider/register`; see §8. |

### 6.2 Location Report Payload

`POST /api/v1/locations` requires a `Content-Type` whose parsed media type is `application/json`. Unknown fields and trailing data are rejected, and the request body is capped at 1 MiB.

```json
{
  "vehicle_id":  "bus-42",
  "trip_id":     "t_5_0830",
  "route_id":    "5",
  "start_date":  "20260715",
  "latitude":    -1.2921,
  "longitude":   36.8219,
  "bearing":     90.0,
  "speed":       12.5,
  "accuracy":    5.0,
  "timestamp":   1752566400
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `vehicle_id` | ✅ | Non-empty string identifier for the vehicle. |
| `trip_id` | ❌ | GTFS `trip_id`. Empty when the driver only knows the route; never a route id. Max 100 characters. |
| `route_id` | ❌ | GTFS `route_id`. Max 100 characters. |
| `start_date` | ❌ | Service date `YYYYMMDD`; accepted only with `trip_id` or `route_id`. |
| `latitude` | ✅ | Decimal degrees, range `-90` to `90`. Cannot be `0` when `longitude` is also `0`. |
| `longitude` | ✅ | Decimal degrees, range `-180` to `180`. |
| `bearing` | ❌ | Direction of travel in degrees. |
| `speed` | ❌ | Speed in metres per second. |
| `accuracy` | ❌ | GPS accuracy in metres. Stored in PostgreSQL, but not emitted in the GTFS-RT feed. |
| `timestamp` | ✅ | Positive Unix epoch timestamp in seconds. |

**Validation rules:**

- `vehicle_id` must be a non-empty string. Error: `vehicle_id is required`.
- `latitude` and `longitude` cannot both be zero. Error: `latitude and longitude cannot both be zero (likely GPS error)`.
- `latitude` must be in the range `-90` to `90`. Error: `latitude must be between -90 and 90`.
- `longitude` must be in the range `-180` to `180`. Error: `longitude must be between -180 and 180`.
- `timestamp` must be positive. Error: `timestamp must be positive`.
- `trip_id` is capped at 100 characters. Error: `trip_id must be at most 100 characters`.
- `route_id` is capped at 100 characters. Error: `route_id must be at most 100 characters`.
- `start_date` must match `YYYYMMDD`. Error: `start_date must be YYYYMMDD`.
- `start_date` must be a real calendar date. Error: `start_date must be a valid YYYYMMDD date`.
- `start_date` requires `trip_id` or `route_id` to be non-empty. Error: `start_date requires trip_id or route_id`.

**Decoding behavior:**

- Invalid or malformed JSON returns `400` with `{"error":"invalid JSON: ..."}`.
- Unknown JSON properties are rejected because the decoder uses `DisallowUnknownFields()`.
- A second JSON object or other non-whitespace trailing data is rejected with `invalid JSON: request body must contain a single JSON object and no trailing data`.
- Because `bearing`, `speed`, and `accuracy` are plain `float64` fields, omitted values decode as `0` and are persisted as non-NULL zeroes.

### 6.3 GTFS-Realtime Feed Generation

The feed is generated on every request entirely from the in-memory Tracker — the database is **not** read during feed generation. This keeps feed latency low and independent of database load.

`buildFeed()` uses `github.com/OneBusAway/go-gtfs/proto` types together with `google.golang.org/protobuf/proto` and `protojson` to construct a `FeedMessage` with:

(Those are the same generated GTFS-RT bindings as MobilityData's `gtfs-realtime-bindings`; only one of the two may be linked into the binary, because both register the same protobuf file names and the second registration panics at init. The rider engine already depends on `go-gtfs` for static GTFS, so that copy is the one the server uses.)

- **Header:** GTFS-Realtime version `2.0`, incrementality `FULL_DATASET`, current Unix timestamp.
- One `FeedEntity` per active vehicle (`id = vehicle_id`).
- Each entity carries a `VehiclePosition` with `Position` (latitude, longitude, bearing, speed), a `VehicleDescriptor` (`id` only), and an epoch `Timestamp` copied from the incoming report.
- A `TripDescriptor` with `trip_id`, `route_id` and `start_date` as reported, only when `trip_id` or `route_id` is non-empty.

Consumers can request JSON encoding by appending `?format=json` to the URL. The default response is binary protobuf (`application/x-protobuf`). Any other `format` value falls back to protobuf because only the exact string `json` is checked.

### 6.4 Staleness & In-Memory Lifecycle

The Tracker is initialised with a configurable `maxAge` (`STALENESS_THRESHOLD`, default `5m`). A background goroutine ticks at that same interval and evicts any `VehicleState` whose `UpdatedAt` is older than the threshold. `ActiveVehicles()` also filters on the same threshold at read time, so stale vehicles are excluded from the feed immediately — even before the next cleanup tick runs.

On startup, the tracker is seeded from PostgreSQL using `GetRecentLocations()` with a cutoff of `time.Now().Add(-maxAge)`. The SQL query filters by `received_at > cutoff` and returns the most recent row per vehicle using `DISTINCT ON`, so seeding is based on when the server stored the point, not the device-reported epoch timestamp. Each seeded row is then inserted into the tracker via `tracker.Update()`, which gives it a fresh in-memory `UpdatedAt` at startup time.

The admin status endpoint uses the same tracker state and returns:

- `active_vehicles`: number of non-stale vehicles at read time.
- `total_vehicles_tracked`: current map size, which may temporarily include stale entries until cleanup runs.
- `last_update`: latest in-memory `UpdatedAt`, omitted when no vehicles have ever been tracked.
- `uptime_seconds`: whole seconds since `startTime` was recorded during startup.

### 6.5 Implementation Notes

A few implementation details matter:

- `vehicles.label` exists in the schema but is not populated by current application code; inserts rely on the column default `''`.
- Recent-location seeding is based on database `received_at`, not the client-supplied `timestamp`.
- `bearing`, `speed`, and `accuracy` are always written as non-NULL values because `LocationReport` uses plain `float64` fields.

### 6.6 Deployment & Configuration

The repository includes a two-stage `Dockerfile` configured to build with `CGO_ENABLED=0` in a `golang:1.24-alpine` stage and copy the resulting binary into a final `alpine:3.21` image with `ca-certificates` installed. The final image exposes port `8080` and uses `ENTRYPOINT ["vehicle-positions"]`.

Docker Compose provides a local development environment:

- **`db` service:** `postgres:17-alpine`, healthcheck via `pg_isready -U postgres`, data persisted in a named volume.
- **`server` service:** builds from the repository root, maps `8080:8080`, sets `PORT`, `DATABASE_URL`, and `STALENESS_THRESHOLD`, and waits for the database healthcheck before starting.

**Environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port. |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable` | PostgreSQL connection string. |
| `STALENESS_THRESHOLD` | `5m` | Max age for a vehicle to be considered active. |
| `READ_TIMEOUT` | `15s` | HTTP server read timeout. |
| `WRITE_TIMEOUT` | `15s` | HTTP server write timeout. |
| `IDLE_TIMEOUT` | `60s` | HTTP server idle connection timeout. |

All duration environment variables are parsed with `time.ParseDuration`; invalid values are logged and the built-in defaults are used.

`Makefile` provides convenience targets for local development and maintenance:

- `make generate` → run `sqlc generate` in `db/`
- `make fmt` → run `go fmt ./...`
- `make vet` → run `go vet ./...`
- `make test` → run `go test ./...` (skips PostgreSQL integration tests when `DATABASE_URL` is unset)

### 6.7 Testing Strategy

The repository contains three test files covering the main components:

#### `handlers_test.go` — Handler Unit Tests

All handler tests use `httptest.NewRequest` / `httptest.NewRecorder`; no real database or port is needed. A `mockStore` implements the `LocationSaver` interface so `SaveLocation` errors can be injected. Tests cover:

- `buildFeed()` correctness (empty feed, multi-vehicle feed, conditional `TripDescriptor`).
- `GET /gtfs-rt/vehicle-positions`: protobuf and JSON serialization, empty feed behavior, stale vehicle exclusion.
- `POST /api/v1/locations`: all validation error paths, happy path, DB failure isolation, `Content-Type` enforcement, `application/json; charset=utf-8` acceptance, unknown field rejection, malformed JSON rejection, and trailing data handling.
- `GET /api/v1/admin/status`: empty and populated tracker behavior, uptime calculation, and zero-count JSON fields.

#### `tracker_test.go` — Tracker Unit Tests

Tests cover: `Update` and overwrite semantics, multi-vehicle tracking, staleness filtering, concurrent read/write safety (100-goroutine stress), concurrent cleanup interaction, constructor panics for non-positive `maxAge`, background cleanup, `Stop` idempotency, and `Status` aggregation.

#### `store_test.go` — PostgreSQL Integration Tests

Skipped automatically when `DATABASE_URL` is not set. Tests require a live PostgreSQL instance and cover: store creation and migration, `SaveLocation` happy path with verification, vehicle upsert idempotency, `GetRecentLocations()` with `DISTINCT ON` and cutoff filtering, DB constraint violations with rollback verification, numeric field round-trips, and migration idempotency.

At the time this document was written, `go test ./...` passes locally without a database by skipping the PostgreSQL integration tests when `DATABASE_URL` is unset.

---

## 7. Extending the System

| Area | Possible Enhancement |
|------|---------------------|
| Input Model | Change `bearing`, `speed`, `accuracy` to `*float64` on `LocationReport` to distinguish "not provided" from zero. |
| Authentication | Add API key or JWT validation for the `POST /api/v1/locations` endpoint. |
| Feed Consumers | Serve incremental GTFS-RT feed (`DIFFERENTIAL` incrementality) to reduce consumer bandwidth. |
| Admin API | Add CRUD endpoints to manage vehicles (label, metadata). |
| Observability | Expose Prometheus metrics (request count, latency, active vehicles, DB query duration). |
| Rate Limiting | Per-vehicle or per-IP rate limiting on the ingestion endpoint. |
| CI/CD | Expand the existing GitHub Actions workflow (currently `go vet ./...` and `go test ./...` on every push and pull request to `main`) to add PostgreSQL-backed integration tests and Docker image publishing. |
| Router | Adopt a third-party router (e.g. Chi) if middleware or path-parameter needs grow. |

---

## 8. Rider Mode

Rider mode is an optional second source of vehicle positions: riders' phones,
reporting from aboard a scheduled trip, for trips no driver app covers. It is
off unless `RIDER_MODE_ENABLED=true`, and when off nothing below is
constructed, no rider route is registered, and the feed carries exactly the
driver entities it always did — `handlers.go` takes the same path it took
before rider mode existed. The tests assert that on the decoded feed rather
than on its bytes: protobuf does not promise a stable encoding across library
versions, and this tree has since moved to a different binding. The design
spec is
`docs/superpowers/specs/2026-09-02-rider-mode-design.md`.

### 8.1 Components

The verification engine lives in its own package, `rider/`, which imports no
HTTP handler and no database driver: it is a pure function of a GTFS schedule
and a stream of points. `package main` owns everything that touches the network
or the database.

| Piece | Where | Responsibility |
|---|---|---|
| `rider.Index` | `rider/index.go` | Immutable snapshot of a GTFS static feed: trips, stop times, shapes, timezone, service-day calendar. |
| `rider.ShapeGeom` | `rider/shape.go` | Projects a fix onto a route shape (local-minimum aware, so loops behave), and interpolates position and bearing back off it. |
| `rider.Verify` | `rider/verify.go` | Pure verdict for one point: `Ignored`, `OffRoute`, `Implausible`, `OffSchedule` or `Matched`, plus its corroboration against a trusted feed. |
| `rider.Session` | `rider/session.go` | One ride's state machine (`Pending → Verified`, or `Rejected`) and its running counts. |
| `rider.Aggregator` | `rider/aggregator.go` | The registry of live rides. Owns every session, serialises access to them, and turns the live ones into one estimate per trip. |
| `rider.Refresher` / `rider.TrustedFeed` | `rider/loader.go`, `rider/trusted.go` | Reload the schedule on a timer; poll the agency's own GTFS-RT VehiclePositions feeds conditionally. |
| `riderService` | `rider_handlers.go` | The HTTP surface: registration, ride lifecycle, position batches, trip status. |
| `riderRuntime` | `rider_wiring.go` | Configuration, startup and the background tickers. |
| `Store` rider methods | `rider_store.go` | `riders`, `rides` and `ride_points` persistence, including the reputation update. |

`main.go` grows by one block for all of it: read `riderConfigFromEnv()`, and if
enabled build a `riderRuntime`, defer its `Stop`, and hand its `*riderService`
to `newHandler`. A nil service is rider mode being off, and both places that
consume it — the feed's `estimateSource` and the admin status provider — go
through helpers (`estimatesOrNil`, `statusOrNil`) that return a true nil
interface rather than an interface holding a nil pointer.

### 8.2 Startup and background work

Startup, in order: load and index the GTFS feed (a failure here aborts the
process — nothing can be verified without a schedule); end every ride the
database still calls `active` with reason `server_restart`, because sessions
live in memory only and none survived the restart; construct the trusted-feed
poller and the aggregator; register the routes. Four goroutines then run until
shutdown, all cancelled and waited for by `riderRuntime.Stop()`:

- the schedule refresher, every `GTFS_STATIC_REFRESH` (a failed reload keeps the previous index);
- the trusted-feed poller, every `TRUSTED_FEED_POLL` (only when URLs are configured);
- the reaper, every 30 s, which ends rides idle for 15 minutes or running longer than 3 hours and files what they amounted to;
- the retention sweep, hourly, deleting `ride_points` older than `RIDER_POINT_RETENTION`.

### 8.3 Data flow

```text
device ──register──▶ riders row + rider JWT
       ──start ride─▶ rides row + rider.Session in the Aggregator
       ──positions──▶ Aggregator.ApplyBatch ──▶ rider.Verify per point
                                            └─▶ Session state + counts
                                            └─▶ ride_points rows + rides progress
       ──end────────▶ FinishRide: outcome, score delta, tier ──▶ Aggregator.SetTier

GET /gtfs-rt/vehicle-positions ──▶ Tracker.ActiveVehicles()  (driver entities)
                               └─▶ Aggregator.Estimates()    (rider entities)
```

A rider's raw fixes never reach the feed. The published position is the
along-shape median of the trip's contributing riders' latest matched points,
snapped back onto the route with `ShapeGeom.PointAt`, so the entity says where
the vehicle is on its route rather than where any phone was.

`GET /api/v1/admin/rider/status` reports both ends of that pipeline:
`rides.active` counts live sessions, one per ride, while `rides.publishable`
counts the estimates the feed would carry right now — one per *trip*, so three
riders on one bus are one publishable trip, not three.

### 8.4 Trust states

Two independent judgements decide whether rider data is published.

Per ride, the session state: a ride is `Pending` until three consecutive points
match the shape and schedule, then `Verified`; five consecutive bad points, or
three consecutive points contradicted by the agency's own feed, make it
`Rejected` and end it. Per rider, the reputation tier: each finished ride
yields a score delta (+1 for a corroborated ride of five minutes or more, −1
for a rejection, −3 for a contradicted one), the score is clamped to [−10, 10],
and the tier follows — `Blocked` at ≤ −3, `Trusted` at ≥ 3, `New` in between. A
blocked rider still receives normal responses, so an abuser learns nothing from
them, but their points are never persisted or published.

A ride contributes to the feed only while it is `Verified`, not ended, fresh
(its latest *accepted* point — of whatever outcome — is younger than
`RIDER_POINT_MAX_AGE`, so a rider whose phone is still reporting is judged on
that, not on the last point that happened to match) and its rider is not
blocked — and then only if that rider is `Trusted`, or the ride has been
corroborated by the trusted feed, or a second rider on the same trip
independently agrees within 100 m.

### 8.5 Feed merge

`handleGetFeed` asks the tracker for driver entities and the aggregator for
rider entities, and `buildFeed` emits both into one `FeedMessage`. Rider
entities take the id `rider:<trip_id>:<start_date>` — repeated as `vehicle.id`,
so the same trip on two service dates is two vehicles (E052) — and the vehicle
label `Rider-reported`; driver vehicle ids cannot contain a colon, so entity and
vehicle ids stay unique across both halves by construction. The position
published for a ride comes from its latest *matched* point, snapped to the
shape: an off-route or implausible fix keeps the ride fresh but never moves the
published estimate. The header timestamp is still the newest of now and
every entity's timestamp.

The trusted feed always wins: any trip the agency's own GTFS-RT feed currently
reports is skipped by `Aggregator.Estimates`, so rider data only ever appears
where there was nothing. The `source` query parameter (`driver`, `rider`, or
the default `all`) lets a consumer take one half, which is also how the merge
is exercised end to end in the smoke test.

---

*This document is part of the OneBusAway Vehicle Positions project maintained by the Open Transit Software Foundation.*
