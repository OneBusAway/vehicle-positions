# Software Requirements Specification

# Vehicle Tracker: Realtime Vehicle Positioning for Developing Countries

**Project:** OneBusAway Vehicle Positions  
**Program:** Google Summer of Code 2026 — Open Transit Software Foundation  
**Repository:** [OneBusAway/vehicle-positions](https://github.com/OneBusAway/vehicle-positions)  
**Document Version:** 1.0  
**Status:** Active Development  

---

## Table of Contents

1. [System Context](#1-system-context)
2. [Stakeholders](#2-stakeholders)
3. [Functional Requirements](#3-functional-requirements)
4. [Non-Functional Requirements](#4-non-functional-requirements)
5. [System Constraints](#5-system-constraints)
6. [Assumptions](#6-assumptions)
7. [Out of Scope](#7-out-of-scope)

---

## 1. System Context

### 1.1 The Problem

The [OneBusAway](https://onebusaway.org) platform provides real-time transit information to riders through a standard set of GTFS-Realtime (GTFS-RT) data feeds. In developed countries, transit agencies supply these feeds through Automatic Vehicle Location (AVL) hardware — specialised onboard units that broadcast GPS position data into agency back-office systems. These systems are expensive to procure, install, and maintain, and they require operational maturity that many emerging transit networks do not yet possess.

Transit agencies across **Africa, South Asia, and Latin America** are actively formalising fixed-route services — minibus networks, matatu routes, tro-tros, and bus rapid transit corridors. Many have already published GTFS static feeds describing their routes and schedules. However, the absence of AVL infrastructure means they cannot generate the GTFS-RT vehicle position feeds that would enable OneBusAway deployment. Without real-time vehicle location data, these agencies cannot offer the rider experience that makes OneBusAway valuable.

The operational reality on the ground makes this problem tractable: **drivers in these fleets universally carry Android smartphones.** These devices include GPS receivers capable of producing position fixes at sufficient frequency for real-time tracking. The critical unsolved problem is capturing those fixes reliably, transmitting them to a server, and producing a GTFS-RT feed — under conditions of intermittent mobile connectivity and with zero investment in specialised hardware.

### 1.2 The Solution

This project delivers a **lightweight, open-source vehicle tracking system** designed for transit agencies operating in environments with limited
infrastructure and intermittent connectivity. The system consists of two
primary components:

**Go Backend Server (this repository):** A standalone Go application that exposes a versioned REST API. It receives location reports from driver apps, maintains a thread-safe in-memory store of current vehicle positions, persists full location history to a relational database, and continuously generates standards-compliant GTFS-RT Vehicle Positions feeds. The server is deployable as a single binary or Docker container with no dependency on proprietary cloud infrastructure.

**Android Driver Application:** A Kotlin application that transit vehicle drivers use during their shifts. The app authenticates the driver, tracks the vehicle's GPS position via Android's `FusedLocationProviderClient`, and transmits location fixes to the server. Critically, the app implements an **offline-first strategy**: every GPS fix is durably written to a local Room database before any transmission is attempted. When network connectivity is unavailable, fixes accumulate in the local buffer and are batch-synchronised to the server the moment connectivity is restored — with zero data loss.

Together, these two components allow any transit agency that possesses a GTFS static feed and a fleet of Android devices to begin generating real-time vehicle position data — and by extension, to deploy OneBusAway for their riders — without purchasing a single piece of specialised hardware.

### 1.3 Integration with the OneBusAway Ecosystem

The GTFS-RT Vehicle Positions feed produced by this server conforms to the [GTFS-Realtime specification](https://gtfs.org/documentation/realtime/proto/) and is consumable by any compliant application without modification. An agency that deploys Vehicle Tracker need only point their existing OBA server at the feed URL. No changes to OBA are required. Vehicle Tracker functions as a **GTFS-RT data source**, not as a replacement for any part of the OBA platform itself.

```
Android Driver App  ──►  Go Backend Server  ──►  GTFS-RT Feed
     (GPS fixes)           (this repo)          ──►  OneBusAway Server
                                                 ──►  Any GTFS-RT Consumer
```

### 1.4 Deployment Context

The system is designed for deployment in environments characterised by:

- **Intermittent mobile connectivity:** 2G/3G/4G coverage that varies by geography, time of day, and network congestion. Signal may disappear entirely on portions of a route and recover minutes later.
- **Low-cost server infrastructure:** A single virtual private server or on-premises Linux machine with 1–2 GB RAM, operated by an agency IT team with limited experience in production software operations.
- **Low-end Android devices:** Driver smartphones running Android 8.0 or later with limited RAM, potentially throttled by aggressive OEM battery optimisation.
- **No proprietary dependencies:** The system must run entirely on open-source software with no requirements for paid API keys, cloud-provider-specific services, or commercial databases.

---

## 2. Stakeholders

| ID | Stakeholder | Role in the System | Primary Concerns |
|----|-------------|-------------------|-----------------|
| **SH-01** | Transit Vehicle Driver | Operates the Android app during their shift. Starts and ends trips, receives tracking status feedback. | Simplicity of use, battery drain, clarity of connectivity status, resilience to signal loss |
| **SH-02** | Transit Operator / Dispatcher | Uses the admin web interface to manage vehicles, drivers, routes, and monitor the live fleet. | Visibility of fleet status, ease of onboarding drivers, reliability of the data feed |
| **SH-03** | Transit Agency IT Administrator | Deploys and operates the backend server. Manages database, TLS, upgrades, and API keys. | Ease of deployment, operational simplicity, structured logs, health monitoring |
| **SH-04** | OneBusAway Server | Machine consumer of the GTFS-RT feed. Polls the feed endpoint to obtain current vehicle positions for rider-facing apps. | Feed validity, spec compliance, low latency, endpoint availability |
| **SH-05** | Third-Party Transit App | Any GTFS-RT-compliant application (e.g., Transit App, Google Maps) that ingests the feed to display vehicle locations. | Feed compliance, consistent availability, standard content-type headers |
| **SH-06** |  Developer | Implements the system under mentor guidance. Future contributors extend and maintain it. | Code clarity, test coverage, architecture documentation, contribution guidelines |
| **SH-07** | Open Transit Software Foundation | Project steward. Responsible for long-term maintenance, licensing, and governance of the OBA ecosystem. | Open-source licence compliance, code quality, community adoption potential |

---

## 3. Functional Requirements

Requirements are grouped by functional domain. Each requirement is assigned a unique identifier in the form `FR-XX`. Requirements use RFC 2119 keywords: **SHALL** (mandatory), **SHOULD** (recommended), **MAY** (optional).

---

### 3.1 Driver Authentication

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-01 | The system SHALL allow drivers to authenticate using their registered credentials. | Must |
| FR-02 | Upon successful authentication, the system SHALL issue a signed access token that must be included in subsequent API requests. | Must |
| FR-03 | Protected API endpoints SHALL require a valid authentication token. Requests without valid credentials SHALL be rejected with HTTP 401. | Must |
| FR-04 | The system SHALL securely store user credentials using a cryptographic password hashing algorithm. | Must |
| FR-05 | Authentication endpoints SHALL protect against user enumeration attacks. | Should |
| FR-06 | The system SHALL support role-based access control to differentiate between driver and administrator privileges. | Must |
| FR-07 | Administrative APIs SHALL only be accessible to authenticated users with administrative privileges. | Must |
| FR-08 | GTFS-RT feed endpoints MAY optionally require an API key depending on deployment configuration. | Should |

---

### 3.2 Trip Lifecycle Management

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-08** | The server SHALL expose a `POST /api/v1/trips/start` endpoint that creates a trip record associating a driver, a vehicle, a route identifier, a GTFS trip identifier, a start time, and a start date. | Must Have |
| **FR-09** | The server SHALL expose a `POST /api/v1/trips/end` endpoint that marks a trip as completed, records the end time, and removes the vehicle from the active GTFS-RT feed. | Must Have |
| **FR-10** | A vehicle SHALL appear in the GTFS-RT feed only when it has an active trip. Vehicles without an active trip SHALL be excluded from feed output regardless of any previously received location data. | Must Have |
| **FR-11** | The server SHALL persist full trip records — including start time, end time, status, route, driver, and vehicle identifiers — to the database for historical analysis. | Must Have |
| **FR-12** | The server SHALL enforce that only one active trip exists per vehicle at any time. Attempting to start a second concurrent trip for the same vehicle SHALL return HTTP `409 Conflict`. | Must Have |

---

### 3.3 Location Reporting — Single Point

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-13** | The server SHALL expose a `POST /api/v1/locations` endpoint that accepts a single GPS location report from an authenticated driver app and returns HTTP `200 OK` on success. | Must Have |
| **FR-14** | Each location report payload SHALL include: `vehicle_id`, `trip_id`, `latitude` (WGS84), `longitude` (WGS84), and `timestamp` (Unix epoch, seconds, UTC). The field `accuracy` (metres) is optional. | Must Have |
| **FR-15** | The server SHALL validate all incoming location payloads. Requests with missing required fields, out-of-range coordinates, or timestamps that are unreasonably far in the past or future SHALL be rejected with HTTP `400 Bad Request` and a descriptive error body. | Must Have |
| **FR-16** | Upon receipt of a valid location report, the server SHALL update the in-memory vehicle state store and persist the location point to the database within the same request handling cycle. | Must Have |

---

### 3.4 Offline Data Buffering and Batch Sync

> **Rationale:** Mobile network coverage in target operating environments is intermittent. The system treats offline operation as a first-class requirement, not a fallback. Every GPS fix captured by the Android app must be durably preserved and eventually delivered to the server — even if the driver's route passes through areas with no signal for extended periods.

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-17** | The Android app SHALL write every GPS fix to a local Room database (Write-Ahead Buffer) before any network transmission is attempted. A GPS fix SHALL NOT be discarded due to a network error or unavailability. | Must Have |
| **FR-18** | The Android app SHALL monitor network connectivity using Android's `ConnectivityManager`. When connectivity is restored after an outage, the app SHALL initiate a batch sync of all unsynced points in the Write-Ahead Buffer. | Must Have |
| **FR-19** | The server SHALL expose a `POST /api/v1/locations/batch` endpoint that accepts an array of timestamped location points from an authenticated driver app and processes them as a single atomic operation. | Must Have |
| **FR-20** | The batch ingestion endpoint SHALL accept a payload of the form `{ "points": [ { ...LocationPoint fields... }, ... ] }` where each element conforms to the single-point schema defined in FR-14. | Must Have |
| **FR-21** | The server SHALL process batch submissions idempotently. Duplicate points, identified by the composite key `(vehicle_id, timestamp)`, SHALL be silently discarded without error. The same batch payload MAY be submitted multiple times and SHALL produce exactly one persisted record per unique `(vehicle_id, timestamp)` pair. | Must Have |
| **FR-22** | The database schema SHALL enforce the idempotency constraint at the storage level via a `UNIQUE` index on `(vehicle_id, timestamp)` in the `location_points` table. The Location Repository SHALL use `INSERT OR IGNORE` (SQLite) or `INSERT ... ON CONFLICT DO NOTHING` (PostgreSQL) when persisting points. | Must Have |
| **FR-23** | When updating the in-memory vehicle state from a batch submission, the server SHALL apply a **timestamp guard**: the in-memory `VehicleState` for a given vehicle SHALL be updated only if the incoming point's timestamp is strictly greater than the vehicle's current `lastSeen` value. Backfilled historical points SHALL enrich the database record without overwriting a fresher live position. | Must Have |
| **FR-24** | The Android app SHALL mark points in the Write-Ahead Buffer as synchronised only upon receiving a confirmed HTTP `200 OK` from the server. On any network failure or non-2xx response, the app SHALL retain the points and retry the batch sync using WorkManager with exponential backoff. | Must Have |
| **FR-25** | The Android app's Write-Ahead Buffer SHALL survive application process termination and device reboot. Unsynced points SHALL remain available for sync after the app is restarted. | Must Have |

---

### 3.5 GTFS-Realtime Feed Generation

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-26** | The server SHALL expose a `GET /gtfs-rt/vehicle-positions` endpoint that returns a valid GTFS-RT `FeedMessage` containing one `VehiclePosition` entity per currently active, non-stale vehicle. | Must Have |
| **FR-27** | The GTFS-RT feed SHALL be encoded as Protocol Buffer binary by default (`Content-Type: application/x-protobuf`). The endpoint SHALL also support JSON output when the query parameter `?format=json` is supplied, for developer debugging purposes. | Must Have |
| **FR-28** | Every `FeedMessage` SHALL include a `FeedHeader` with `gtfs_realtime_version: "2.0"`, `incrementality: FULL_DATASET`, and `timestamp` set to the current Unix epoch at the moment of feed generation. | Must Have |
| **FR-29** | Each `VehiclePosition` entity in the feed SHALL include: `trip` (with `trip_id`, `route_id`, `start_time`, `start_date`, `schedule_relationship: SCHEDULED`), `position` (with `latitude`, `longitude`, `bearing`, `speed`), `timestamp` (last known position timestamp), and `vehicle` (with `id` and `label`). | Must Have |
| **FR-30** | The server SHALL exclude from the feed any vehicle whose `lastSeen` timestamp is older than a configurable **staleness threshold**. The default threshold SHALL be 300 seconds (5 minutes). The threshold SHALL be configurable via an environment variable. | Must Have |
| **FR-31** | Feed generation SHALL read exclusively from the in-memory vehicle state store and SHALL NOT perform any database I/O. This ensures that feed endpoint latency is constant and independent of database load. | Must Have |
| **FR-32** | The GTFS-RT feed produced by the server SHALL pass the [MobilityData GTFS-RT Validator](https://github.com/MobilityData/gtfs-realtime-validator) with zero errors and zero warnings. | Must Have |

---

### 3.6 Vehicle and Driver Management

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-33** | The server SHALL expose admin REST endpoints to create, read, update, and deactivate **vehicle** records. Each vehicle record SHALL include at minimum: `id`, `label`, `agency_id`, and `active` status. | Must Have |
| **FR-34** | The server SHALL expose admin REST endpoints to create, read, update, and deactivate **driver** records. Each driver record SHALL include at minimum: `id`, `name`, `phone` (unique), `password_hash`, `vehicle_id` (assigned vehicle), and `active` status. | Must Have |
| **FR-35** | The server SHALL expose admin REST endpoints to list active and historical **trips**, and to retrieve the location trail for any completed trip. | Must Have |
| **FR-36** | Deactivating a vehicle or driver record SHALL be a soft-delete operation. The record SHALL be marked `active: false` and retained in the database. Hard deletion is not supported through the API. | Should Have |
| **FR-37** | The server SHALL expose admin REST endpoints for managing **API keys** used by GTFS-RT feed consumers: create, list, and revoke. API keys SHALL be stored as bcrypt hashes; the plaintext key SHALL be returned only at creation time and SHALL NOT be retrievable thereafter. | Must Have |

---

### 3.7 Admin Interface

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-38** | The server SHALL serve a web-based admin interface accessible via a browser. The interface SHALL be bundled with and served directly by the Go server binary, with no separate deployment step required. | Must Have |
| **FR-39** | The admin interface SHALL display a live map of all active vehicles, updated at a configurable polling interval. The map SHALL use Leaflet with OpenStreetMap tiles and SHALL require no proprietary map API key. | Must Have |
| **FR-40** | The admin interface SHALL provide management screens for vehicles, drivers, and API keys, corresponding to the admin REST endpoints defined in FR-33 through FR-37. | Must Have |
| **FR-41** | The admin interface SHALL display a system dashboard showing: number of active vehicles, GTFS-RT feed health (last generation timestamp), database connectivity status, and server uptime. | Must Have |
| **FR-42** | The admin interface SHALL allow operators to view the historical location trail of any completed trip, rendered as a polyline on the map. | Should Have |
| **FR-43** | The admin interface SHALL allow operators to export the location data for any trip as a CSV file for offline analysis. | Should Have |

---

### 3.8 System Health Monitoring

| ID | Requirement | Priority |
|----|-------------|----------|
| **FR-44** | The server SHALL expose a `GET /api/v1/admin/status` endpoint that returns a machine-readable JSON document describing: server uptime, active vehicle count, database connectivity, time since last location report received, and time since last GTFS-RT feed generation. | Must Have |
| **FR-45** | The health check endpoint SHALL return HTTP `200 OK` when the system is operational and `503 Service Unavailable` when a critical dependency (e.g. the database) is unreachable. This enables integration with standard container health check mechanisms and load balancer probes. | Must Have |
| **FR-46** | The server SHALL emit structured logs in JSON format for all significant events: incoming requests (method, path, status, latency), authentication failures, location ingestion errors, feed generation, and application startup/shutdown. | Must Have |

---

## 4. Non-Functional Requirements

| ID | Category | Requirement | Rationale |
|----|----------|-------------|-----------|
| **NFR-01** | **Performance** | The server SHALL sustain ingestion from at least 50 simultaneously active vehicles, each reporting at a 10-second interval, without feed generation latency exceeding 500 ms. | Target fleet size for initial deployments; ensures feed consumers receive fresh data. |
| **NFR-02** | **Performance** | A location report received via `POST /api/v1/locations` SHALL be reflected in the next GTFS-RT feed response within **5 seconds** of server receipt under normal load. | Rider-facing apps expect near-real-time position updates. |
| **NFR-03** | **Performance** | The `GET /gtfs-rt/vehicle-positions` endpoint SHALL respond in under **200 ms** at p99 for fleets of up to 100 active vehicles. Feed generation reads exclusively from the in-memory store; no database I/O is permitted in the feed generation path. | Feed consumers poll frequently; high feed endpoint latency compounds across all downstream apps. |
| **NFR-04** | **Resilience** | The Android app SHALL buffer GPS fixes in a local Room database during network outages of **any duration**. No location fix SHALL be discarded due to network unavailability. On connectivity restoration, all buffered fixes SHALL be delivered to the server without driver intervention. | Intermittent connectivity is the expected operating condition in target regions. |
| **NFR-05** | **Resilience** | The server SHALL handle duplicate batch submissions idempotently. Submitting the same batch payload multiple times SHALL produce exactly one persisted record per `(vehicle_id, timestamp)` pair, with no error returned to the client. | Network retries and WorkManager re-execution may result in the same batch being submitted more than once. |
| **NFR-06** | **Resilience** | The Android app's foreground tracking service SHALL recover automatically if the OS terminates the app process due to memory pressure. Location tracking SHALL resume within 30 seconds of process restart without requiring driver interaction. | Low-end devices with aggressive memory management may kill background processes. |
| **NFR-07** | **Scalability** | The server's database layer SHALL be fully swappable between SQLite and PostgreSQL via a single environment variable change (`DATABASE_URL`). No application code changes SHALL be required to switch database engines. | Small agencies begin on SQLite; growth to PostgreSQL must not require code changes or re-deployment from source. |
| **NFR-08** | **Scalability** | The server architecture SHALL support horizontal scaling: multiple server instances MAY be deployed behind a load balancer sharing a common PostgreSQL database. In-memory state per instance SHALL converge as live location reports arrive, with no inter-instance coordination required. | Larger agencies may require higher availability or throughput than a single instance provides. |
| **NFR-09** | **Security** | All communication between the Android app and the server SHALL be conducted over HTTPS/TLS. The server SHALL not accept location data over plaintext HTTP in production deployments. | Location data and authentication credentials must be protected in transit. |
| **NFR-10** | **Security** | Driver PINs and API keys SHALL never be stored or logged in plaintext. All credentials SHALL be stored as bcrypt hashes. Log output SHALL be audited to ensure credential data is never emitted. | Credential exposure via log aggregation pipelines is a common vulnerability in production systems. |
| **NFR-11** | **Security** | The server SHALL enforce HTTP request rate limiting on all public-facing endpoints to mitigate denial-of-service and credential-stuffing attacks. Rate limit thresholds SHALL be configurable via environment variables. | A publicly accessible server in a low-operational-maturity environment requires baseline DoS protection. |
| **NFR-12** | **Low Bandwidth** | The single-point location report payload (`POST /api/v1/locations`) SHALL be designed for minimum wire size. Required fields SHALL be limited to those necessary for GTFS-RT output. The expected payload size per report SHALL not exceed **512 bytes**. | Target environments may operate on metered 2G/3G data plans with significant cost-per-megabyte. |
| **NFR-13** | **Low Bandwidth** | The Android app SHALL implement batch submission for offline-sync payloads, grouping multiple queued fixes into a single HTTP request, reducing per-fix TLS and TCP connection overhead. | Connection establishment overhead is disproportionately expensive on high-latency mobile networks. |
| **NFR-14** | **Battery Efficiency** | The Android tracking service SHALL use Android's `FusedLocationProviderClient` for GPS acquisition. The default location update interval SHALL be **10 seconds**, configurable by the operator. The service SHALL request battery optimisation exemption via a foreground notification. | GPS radio usage is the primary source of battery drain; `FusedLocationProvider` minimises this relative to direct GPS access. |
| **NFR-15** | **Battery Efficiency** | The Android tracking foreground service SHALL display a persistent notification that clearly communicates tracking status (active / offline-buffering / GPS unavailable) to the driver. This notification is the mechanism by which Android's Doze mode restrictions are lifted for the duration of a shift. | Without a foreground notification, Android may suspend the location service, causing tracking gaps. |
| **NFR-16** | **Observability** | The server SHALL emit structured JSON logs for all requests (method, path, HTTP status, response latency), authentication events (success and failure), ingestion events (point count, deduplication count), and feed generation events (vehicle count, generation latency). Logs SHALL be written to stdout. | Structured logs on stdout are consumable by any log aggregation pipeline (ELK, Loki, CloudWatch, etc.) without server configuration changes. |
| **NFR-17** | **Deployment Simplicity** | The server SHALL be distributable and runnable as a **single self-contained binary** requiring no separate installation of language runtimes, library dependencies, or build tools on the target machine. | IT staff at transit agencies in target regions may have limited Linux administration experience. |
| **NFR-18** | **Deployment Simplicity** | The server SHALL be fully deployable using a provided `docker-compose.yml` with a single command (`docker compose up`). All runtime configuration SHALL be supplied via environment variables with documented defaults. | A one-command deployment path is essential for adoption by agencies without dedicated DevOps staff. |
| **NFR-19** | **Deployment Simplicity** | All runtime configuration — port, database connection string, JWT secret, staleness threshold, rate limits — SHALL be supplied exclusively via environment variables. No configuration file SHALL be required. The server SHALL start with sensible defaults suitable for development with no environment variables set. | Twelve-Factor App methodology; simplifies deployment across environments without file management. |
| **NFR-20** | **GTFS-RT Compliance** | The GTFS-RT feed produced by the server SHALL conform to the GTFS-Realtime specification version 2.0 and SHALL pass the MobilityData GTFS-RT Validator with **zero errors and zero warnings**. This validation SHALL be executed as a mandatory step in the CI pipeline on every pull request. | Spec non-compliance causes silent failures or incorrect data in downstream consumer applications including OBA. |

---

## 5. System Constraints

These constraints are non-negotiable technical boundaries on the implementation. They are derived from the project mandate, the target operating environment, and the OBA ecosystem requirements.

| ID | Constraint | Justification |
|----|-----------|---------------|
| **SC-01** | The backend server SHALL be implemented exclusively in **Go**. No other server-side language or runtime is permitted. | Aligns with the OneBusAway ecosystem's server-side direction (Maglev is Go); ensures long-term maintainability by OTSF contributors. |
| **SC-02** | The Android driver application SHALL be implemented in **Kotlin** targeting Android API level 26 (Android 8.0) and above. | Android 8.0+ covers the large majority of devices in service in target regions; Kotlin is the OTSF-standard Android language. |
| **SC-03** | The GTFS-RT feed SHALL be encoded using the **official Protocol Buffer definition** from the GTFS-RT specification (`gtfs-realtime.proto`). The Go implementation SHALL use `google.golang.org/protobuf` and the [MobilityData gtfs-realtime-bindings](https://github.com/MobilityData/gtfs-realtime-bindings) for Go. | Protocol Buffer encoding is mandated by the GTFS-RT specification and required for compatibility with OBA and all other GTFS-RT consumers. |
| **SC-04** | All server-client communication SHALL use a **versioned REST API** (`/api/v1/...`). No other API paradigm (GraphQL, gRPC, WebSocket for data ingestion) is permitted for the primary data path. | REST minimises client implementation complexity; versioning prefix allows future breaking changes without disrupting existing deployments. |
| **SC-05** | The server SHALL support **SQLite** as a fully functional deployment target with no feature degradation relative to PostgreSQL. SQLite is not a "development-only" mode; it is the recommended database for agencies with fleets under approximately 50 vehicles. | Many target agencies lack the operational capacity to run and maintain a PostgreSQL server. |
| **SC-06** | The server SHALL support **PostgreSQL** as the database for production deployments with larger fleets or high-availability requirements. The database engine SHALL be selected at deployment time via the `DATABASE_URL` environment variable, with no code changes required. | Larger agencies or those with existing PostgreSQL infrastructure require a production-grade RDBMS. |
| **SC-07** | The system SHALL have **no dependency on proprietary or paid external services.** This includes map tile APIs (the admin map SHALL use OpenStreetMap/Leaflet), cloud storage, managed databases, or any service requiring a paid API key. | Target agencies operate under severe budget constraints; any proprietary dependency creates a barrier to adoption and a long-term cost obligation. |
| **SC-08** | The server SHALL be distributable as a **Docker image** published to a public container registry (GitHub Container Registry). A `Dockerfile` and `docker-compose.yml` SHALL be maintained in the repository. | Docker is the lowest-barrier-to-entry deployment mechanism for Linux servers in the target environment. |
| **SC-09** | The Android app SHALL function as a **foreground service** with a persistent notification during active tracking. Background-only location access is not a permitted implementation approach. | Android 8.0+ restricts background location access; only foreground services with persistent notifications have reliable access to continuous GPS updates. |
| **SC-10** | The CI pipeline SHALL be implemented using **GitHub Actions** and SHALL run on every pull request and every push to the main branch. All CI checks must pass before a pull request may be merged. | GitHub Actions is the OTSF-standard CI platform; gating on CI ensures code quality gates are enforced before merge. |

---

## 6. Assumptions

These are conditions assumed to be true during design and development. If any assumption is violated, the corresponding requirements or architecture may need to be revisited.

| ID | Assumption |
|----|-----------|
| **AS-01** | Every transit vehicle driver in the target deployment has personal possession of an Android smartphone (Android 8.0 or later) and carries it during their shift. The agency does not need to procure devices. |
| **AS-02** | The transit agency already has a **GTFS static feed** describing their routes, stops, trips, and schedules. The Vehicle Tracker system augments this static data with real-time positions; it does not replace or generate static GTFS data. |
| **AS-03** | Mobile network connectivity (2G/3G/4G) is **intermittently available** on driver routes. Connectivity will be absent at times but will be restored periodically during a shift — sufficient for batch sync to occur. Connectivity is not assumed to be completely absent for the entire duration of a shift. |
| **AS-04** | The transit agency has access to a Linux server (physical or virtual) with internet connectivity sufficient to serve GTFS-RT consumers. The agency has at least one person with basic Linux administration skills to perform the initial deployment. |
| **AS-05** | Driver-facing interactions with the Android app are limited to: login at shift start, vehicle/route selection, monitoring the tracking status indicator, and ending the trip. Drivers are not expected to configure the app or troubleshoot connectivity issues. |
| **AS-06** | The transit agency consuming the GTFS-RT feed operates an OBA server or another GTFS-RT-compliant application that polls the feed endpoint at a configurable interval (typically every 15–30 seconds). |
| **AS-07** | The server is deployed behind a TLS-terminating reverse proxy (nginx or equivalent). TLS termination at the application binary itself is not required but may be added. The `docker-compose.yml` will document a reference nginx configuration. |
| **AS-08** | The Android app is distributed to drivers via direct APK installation (sideloading) or a private app distribution mechanism. Distribution through the Google Play Store is not assumed, as some target agencies may not have access to a Google Play Developer account. |
| **AS-09** | A vehicle operates exactly one active trip at a time. A driver is assigned to exactly one vehicle per shift. The system does not need to model shared vehicles or co-driver scenarios in the initial version. |
| **AS-10** | GPS signal is available to the Android device during the majority of a driver's route. Brief GPS signal loss (tunnels, dense urban canyons) is handled by `FusedLocationProviderClient`'s sensor fusion; the system does not need to implement additional dead-reckoning. |

---

## 7. Out of Scope

The following capabilities are explicitly excluded from the current project. They are documented here to prevent scope creep, to inform future roadmap planning, and to allow the architecture to be designed in a way that does not preclude their later addition.

| ID | Capability | Reason for Exclusion |
|----|-----------|----------------------|
| **OS-01** | **Arrival Prediction / GTFS-RT TripUpdates** | Estimating arrival times at stops requires significant additional complexity: stop sequence matching, schedule deviation computation, historical travel-time modelling, and a TripUpdate feed generator. This is a distinct problem that can be built on top of the Vehicle Positions infrastructure once it is stable. The database schema is designed to accumulate the historical data that arrival prediction would require. |
| **OS-02** | **iOS Driver Application** | An iOS companion app requires Apple Developer Program membership, Xcode tooling, and Swift expertise. Drivers in target regions — Africa, South Asia, Latin America — use Android devices with very high market share. iOS is a future consideration for regions with different device demographics. |
| **OS-03** | **Rider-Facing Passenger App** | The Vehicle Tracker system is a data production pipeline. Rider-facing features (journey planning, arrival notifications, trip history for passengers) are delivered by OneBusAway and other GTFS-RT consumer applications. This project does not build or modify rider apps. |
| **OS-04** | **Fleet Management Beyond Real-Time Tracking** | Features such as maintenance scheduling, driver performance analytics, route optimisation, fuel tracking, and incident reporting are general fleet management capabilities. They are out of scope; this system tracks vehicle positions and nothing else. |
| **OS-05** | **Passenger Counting** | Ridership estimation via cameras, tap counters, or weight sensors is a separate hardware and software problem. The system collects location data only. |
| **OS-06** | **GTFS-RT Service Alerts** | Service alert feeds (disruptions, detours, stop closures) require a content management workflow and are authored by operators, not derived from vehicle telemetry. They are not generated by this system. |
| **OS-07** | **Multi-Tenancy (Multiple Agencies on One Instance)** | The current architecture serves a single transit agency per server instance. Running multiple agencies on a shared instance requires tenant isolation (row-level security, scoped API keys, per-agency feeds), which adds significant complexity. This is identified as a future enhancement in the architecture documentation. |
| **OS-08** | **Replacing Existing AVL Systems** | Transit agencies that already have functioning AVL infrastructure and GTFS-RT feeds have no need for this system. Vehicle Tracker is explicitly designed for agencies with **no** existing AVL capability. |
| **OS-09** | **Native Windows or macOS Server Deployment** | The server binary is cross-compilable and may function on Windows or macOS, but deployment documentation, CI, and the Docker image target Linux only. Windows/macOS server operation is unsupported. |
| **OS-10** | **Driver Incentivisation, Gamification, or Performance Scoring** | Features such as on-time performance scoring, route adherence leagues, or driver dashboards are policy concerns for the transit agency, not infrastructure concerns for this system. They may be built by third parties consuming the data this system produces. |
| **OS-11** | **Multilingual User Interface Support** | The backend server primarily exposes machine-readable APIs and GTFS-RT feeds, which are language-neutral. Localization of user-facing interfaces (e.g., Android driver app or admin UI) is handled at the client application layer. While the system is designed so that future clients can support multiple languages, multilingual UI support itself is outside the scope of this backend project. |

---

## Appendix A: Requirement Traceability Summary

| Domain | FR Count | Key Endpoints |
|--------|----------|--------------|
| Driver Authentication | FR-01 – FR-07 | `POST /api/v1/auth/login` |
| Trip Lifecycle | FR-08 – FR-12 | `POST /api/v1/trips/start`, `POST /api/v1/trips/end` |
| Single Location Reporting | FR-13 – FR-16 | `POST /api/v1/locations` |
| Offline Buffering & Batch Sync | FR-17 – FR-25 | `POST /api/v1/locations/batch` (Android Room WAB + WorkManager) |
| GTFS-RT Feed Generation | FR-26 – FR-32 | `GET /gtfs-rt/vehicle-positions` |
| Vehicle & Driver Management | FR-33 – FR-37 | `GET/POST/PUT/DELETE /api/v1/admin/vehicles`, `/drivers`, `/api-keys` |
| Admin Interface | FR-38 – FR-43 | Served by Go binary; Leaflet/OpenStreetMap |
| System Health | FR-44 – FR-46 | `GET /api/v1/admin/status` |

---

## Appendix B: Glossary

| Term | Definition |
|------|-----------|
| **AVL** | Automatic Vehicle Location — specialised onboard hardware that transmits vehicle position to an agency back-office system. Vehicle Tracker replaces the need for AVL in agencies that lack it. |
| **GTFS** | General Transit Feed Specification — the standard format for publishing static transit schedule data (routes, stops, trips, fares). |
| **GTFS-RT** | GTFS-Realtime — a specification for publishing real-time transit data (vehicle positions, trip updates, service alerts) as Protocol Buffer feeds. |
| **FeedMessage** | The top-level Protocol Buffer message in a GTFS-RT feed. Contains a `FeedHeader` and an array of `FeedEntity` records. |
| **VehiclePosition** | A GTFS-RT message type describing the current geographic position of a transit vehicle on an active trip. |
| **Write-Ahead Buffer (WAB)** | A local Room database on the Android device that durably stores GPS fixes before network transmission. Ensures zero data loss during connectivity outages. |
| **Idempotent Ingestion** | A property of the batch endpoint: submitting the same payload multiple times produces exactly one persisted database record per unique `(vehicle_id, timestamp)` pair. |
| **Staleness Threshold** | The maximum age of a vehicle's last-known position before it is excluded from the GTFS-RT feed. Configurable; default 5 minutes. |
| **JWT** | JSON Web Token — a compact, signed token used for stateless driver authentication. |
| **FusedLocationProviderClient** | Android's sensor-fusion location API, combining GPS, Wi-Fi positioning, and cell network data for improved accuracy and reduced battery consumption relative to raw GPS access. |
| **OBA** | OneBusAway — the open-source transit rider information platform maintained by OTSF. |
| **OTSF** | Open Transit Software Foundation — the non-profit organisation that stewards OneBusAway and related open-source transit software projects. |
| **WorkManager** | Android Jetpack library for scheduling deferrable, guaranteed background work. Used for batch sync retry with exponential backoff. |

---

*This document is part of the OneBusAway Vehicle Positions project maintained by the Open Transit Software Foundation.*

*For system architecture and design details, see [ARCHITECTURE.md](./ARCHITECTURE.md).*