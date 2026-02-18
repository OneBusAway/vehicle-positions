# vehicle-positions

# Vehicle Tracker: Realtime Vehicle Positioning for Developing Countries

**Google Summer of Code 2026 — Open Transit Software Foundation**

|                        |                                                              |
|------------------------|--------------------------------------------------------------|
|**Difficulty**          |Advanced                                                      |
|**Size**                |Large (350 hours / 12 weeks standard)                         |
|**Primary Technologies**|Go (server), Kotlin/Android (mobile)                          |
|**Mentor**              |Aaron Brethorst, Executive Director, OTSF|

-----

## 1. Problem Statement

The OneBusAway server relies on specialized software and hardware — Automatic Vehicle Location (AVL) systems, SIRI feeds, proprietary APIs — to generate the realtime vehicle position data that powers its rider-facing apps. This works well for transit agencies in developed countries that have already invested in this infrastructure, but it creates a significant barrier for transit systems in developing countries that are building out fixed-route transit for the first time.

In many cities across Africa, South Asia, and Latin America, transit agencies are beginning to formalize minibus, matatu, tro-tro, and bus rapid transit routes. They often have GTFS static feeds (route and schedule data) but no way to generate GTFS-Realtime feeds because they lack AVL hardware entirely. Drivers carry Android smartphones, connectivity is intermittent (ranging from strong 4G in city centers to nothing in outlying areas), and budgets for specialized hardware are near zero.

This project fills that gap by creating a lightweight, open-source vehicle tracking system purpose-built for these conditions: a Go server that receives location reports from drivers’ Android phones and produces standard GTFS-RT Vehicle Positions protobuf feeds, plus a companion Android app that drivers use to report their location. Together, these components allow any transit agency with a GTFS static feed and a fleet of Android phones to immediately begin offering realtime vehicle tracking to their riders through OneBusAway or any other GTFS-RT-compatible consumer.

## 2. Goals and Non-Goals

### Goals

- Build a production-ready Go server that ingests vehicle location reports and serves GTFS-RT Vehicle Positions feeds via HTTP
- Build a companion Android app that transit vehicle drivers use to continuously report their location while on a trip
- Implement secure authentication between the Android app and server (driver login, vehicle assignment, API keys)
- Build basic administrative tools for transit operators to manage vehicles, drivers, routes, and active trips
- Produce comprehensive deployment documentation so a transit agency IT team (or a technically inclined operations manager) can set up the system independently

### Non-Goals

- Offline data queuing and sync (v2 — see Future Work; v1 requires an active network connection to report locations)
- Arrival predictions / trip updates (this project produces Vehicle Positions only; arrival estimation is a separate, significantly more complex problem that can build on this data later)
- iOS driver app (the target user base — transit drivers in developing countries — overwhelmingly uses Android)
- Rider-facing features (riders consume the GTFS-RT feed through existing apps like OneBusAway; this project focuses on the data production side)
- Replacing existing AVL systems for agencies that already have them
- Building a general-purpose fleet management platform (the scope is deliberately narrow: location tracking → GTFS-RT feed)

## 3. Technical Approach

The system has two major components — the server and the Android app — plus an administrative interface. They communicate via a simple REST API.

### 3.1 Server (Go)

The server is a standalone Go application, independent from the existing Maglev API server. It is designed to be simple to deploy (single binary + database) and easy to operate for agencies with limited technical resources.

**Core Responsibilities:**

1. **Ingest location reports** from driver apps via a lightweight REST API
1. **Maintain current vehicle state** in memory (latest position, speed, bearing, trip assignment for each active vehicle)
1. **Serve GTFS-RT Vehicle Positions feeds** as protobuf over HTTP, refreshed on each request from the in-memory state
1. **Persist location history** to a database for operational analytics and debugging
1. **Authenticate drivers and API consumers** via token-based auth
1. **Provide an admin API** for managing vehicles, drivers, and route/trip assignments

**GTFS-RT Feed Generation:**

The server produces a standard `FeedMessage` containing `VehiclePosition` entities per the [GTFS-RT specification](https://gtfs.org/documentation/realtime/proto/). Each entity includes:

```protobuf
vehicle {
  trip {
    trip_id: "route_5_0830"
    route_id: "5"
    start_time: "08:30:00"
    start_date: "20260715"
    schedule_relationship: SCHEDULED
  }
  position {
    latitude: -1.2921
    longitude: 36.8219
    bearing: 180.0
    speed: 8.5
  }
  timestamp: 1752566400
  vehicle {
    id: "vehicle-042"
    label: "Bus 42"
  }
}
```

The feed is served at a configurable HTTP endpoint (e.g., `GET /gtfs-rt/vehicle-positions`) and returns the protobuf binary by default, with an optional `?format=json` parameter for debugging. The feed uses `incrementality: FULL_DATASET` and includes all currently-active vehicles.

**API Design:**

|Endpoint                        |Method|Purpose                                             |
|--------------------------------|------|----------------------------------------------------|
|`POST /api/v1/auth/login`       |POST  |Driver login → returns JWT                          |
|`POST /api/v1/locations`        |POST  |Single location report from driver app              |
|`GET /gtfs-rt/vehicle-positions`|GET   |GTFS-RT feed (protobuf or JSON)                     |
|`GET /api/v1/admin/vehicles`    |GET   |List vehicles                                       |
|`POST /api/v1/admin/vehicles`   |POST  |Create/update vehicle                               |
|`GET /api/v1/admin/drivers`     |GET   |List drivers                                        |
|`POST /api/v1/admin/drivers`    |POST  |Create/update driver                                |
|`POST /api/v1/trips/start`      |POST  |Driver starts a trip (assigns vehicle to route/trip)|
|`POST /api/v1/trips/end`        |POST  |Driver ends a trip                                  |
|`GET /api/v1/admin/status`      |GET   |System health, active vehicles, feed stats          |

**Location Report Payload:**

Each location report is a single point sent directly from the Android app as it receives GPS fixes. The payload is deliberately minimal to reduce bandwidth consumption.

```json
{
  "vehicle_id": "vehicle-042",
  "trip_id": "route_5_0830",
  "latitude": -1.2921,
  "longitude": 36.8219,
  "bearing": 180.0,
  "speed": 8.5,
  "accuracy": 12.0,
  "timestamp": 1752566400
}
```

The server updates its in-memory state with the latest position and persists the point to the database. Points older than a configurable staleness threshold (default 5 minutes) are excluded from the GTFS-RT feed.

**Technology Stack:**

- **Language:** Go (aligns with Maglev and OTSF’s server-side direction)
- **HTTP framework:** Chi or stdlib `net/http` with middleware
- **Database:** SQLite for small deployments, PostgreSQL for larger ones (abstracted behind a repository interface)
- **Protobuf:** `google.golang.org/protobuf` with the official `gtfs-realtime.proto`
- **Authentication:** JWT tokens for driver auth, API keys for feed consumers
- **Deployment:** Single binary, Dockerfile, docker-compose for the full stack

### 3.2 Android Driver App (Kotlin)

The Android app is the primary data collection tool. It must be dead simple for drivers to use (many of whom may have limited smartphone experience) and reliable in sending location data to the server while the driver is on a trip.

In v1, the app sends location reports directly to the server as they are captured. If the network is temporarily unavailable, the app shows a clear warning to the driver but does not queue data locally — location points captured during a network outage are dropped. This is an acceptable tradeoff for v1: the GTFS-RT feed only needs to reflect the *current* position of each vehicle, so historical points lost during a brief outage do not create gaps in the rider experience. Offline queuing and backfill is planned for v2 (see Future Work).

**Core User Flow:**

```
[Login screen] → Enter phone number + PIN
        ↓
[Select vehicle] → Pick from assigned vehicle list
        ↓
[Select route/trip] → Pick from today's scheduled trips
        ↓
[Tracking active] → Large, clear status indicator
  - Green: "Tracking - Connected"
  - Red: "No connection" or "GPS unavailable"
        ↓
[End trip] → Large button, confirmation dialog
```

**Location Reporting:**

- **Location capture:** Android `FusedLocationProviderClient` running as a foreground service with a persistent notification (“OBA Tracker is active”). Location updates every 10 seconds (configurable).
- **Direct send:** Each location fix is immediately POSTed to the server via Retrofit/OkHttp. Failed requests are logged but not retried (v1 simplification).
- **Connection status:** The app monitors network availability and displays a clear visual indicator to the driver. When the network is unavailable, the app continues capturing GPS fixes (in case connectivity returns quickly) but does not attempt to send them.
- **Battery optimization:** The app must request exemption from battery optimization (Doze mode) during active tracking. The foreground service notification ensures Android doesn’t kill the process. GPS polling interval is configurable to balance accuracy vs. battery drain.

**UI Design Principles (for driver usability):**

- Large touch targets (minimum 48dp, prefer 64dp+ for primary actions)
- High-contrast colors, minimal text
- Status always visible at a glance (green = connected and sending; red = problem)
- Trip start/stop are the only two primary actions
- No complex navigation or settings during active tracking
- Supports right-to-left (RTL) layouts for Arabic, Urdu, etc.
- Localization-ready from day one (English + string extraction for future translations)

**Technology Stack:**

- **Language:** Kotlin
- **UI:** Jetpack Compose (Material 3)
- **Location:** Google Play Services `FusedLocationProviderClient`
- **Networking:** Retrofit + OkHttp (with interceptor for auth token injection)
- **DI:** Hilt
- **Architecture:** MVVM with Repository pattern

### 3.3 Admin Interface

A lightweight web-based admin panel for transit operators to manage the system. This can be a simple server-rendered UI (Go templates) or a minimal React SPA served by the Go server — the contributor should propose an approach during the community bonding period.

**Admin Capabilities:**

- View active vehicles on a map (Leaflet/OpenStreetMap — no Google Maps API key required)
- Create/edit/deactivate vehicles and driver accounts
- Assign drivers to vehicles
- View trip history and location trails
- Monitor feed health (last update time, number of active vehicles, error rates)
- Download location data as CSV for analysis

## 4. Technical Prerequisites

This project requires a contributor comfortable working across both a Go backend and an Android app. Given the GSoC timeline and the project’s advanced rating, the ideal candidate is an undergraduate with prior experience in at least one of these domains and willingness to learn the other quickly.

**Required:**

- Proficiency in at least one of: Go or Kotlin/Android
- Working familiarity with the other (willing to ramp up quickly during community bonding)
- Understanding of REST API design and HTTP
- Experience with relational databases (SQL)
- Git and collaborative open-source development workflows

**Strongly Preferred:**

- Experience with Android foreground services and background processing
- Familiarity with Protocol Buffers
- Understanding of GPS/location services on mobile devices
- Experience with authentication systems (JWT, API keys)

**Nice to Have:**

- Familiarity with GTFS or GTFS-RT data formats
- Experience deploying server applications (Docker, Linux)
- Interest in transit, urban mobility, or international development

## 5. Milestones and Timeline

This timeline follows the GSoC 2026 standard coding period (May 25 – August 24, 2026) at approximately 27 hours/week. The contributor may request an extended timeline (up to 22 weeks) if needed, with mentor approval.

### Community Bonding Period (May 1 – May 24)

- Set up development environments: Go toolchain, Android Studio, protobuf compiler
- Read and understand the GTFS-RT specification, particularly the `VehiclePosition` message
- Review the Maglev codebase (Go) to understand OTSF coding conventions
- Review the OBA Android codebase to understand existing patterns
- Research transit operations in 1–2 target regions (understand how drivers currently operate, what phone models are common, what network conditions are like)
- Propose and agree on: database schema, API contract, admin UI approach, Android architecture
- Set up CI (GitHub Actions) for both the server and Android projects

### Milestone 1: Server Foundation + GTFS-RT Feed (~60 hours, Weeks 1–3)

**Deliverable:** A running Go server that accepts location reports via REST API and serves a valid GTFS-RT Vehicle Positions protobuf feed.

- Initialize Go project with module structure, CI, and linting
- Define and compile `gtfs-realtime.proto` into Go code
- Implement the database schema and repository layer:
  - Vehicles table (id, label, agency_id, active)
  - Drivers table (id, name, phone, pin_hash, vehicle_id)
  - Trips table (id, vehicle_id, route_id, trip_id, start_time, end_time, status)
  - Location points table (id, vehicle_id, trip_id, lat, lon, bearing, speed, accuracy, timestamp, received_at)
- Implement `POST /api/v1/locations` — accept a location report, validate, persist, update in-memory state
- Implement `GET /gtfs-rt/vehicle-positions` — build a `FeedMessage` from in-memory state and serialize to protobuf
- Add JSON output option for debugging (`?format=json`)
- Write integration tests: submit locations, fetch feed, verify protobuf contents
- Validate output against a GTFS-RT validator tool

**Exit Criteria:** The server accepts location POSTs and produces a valid GTFS-RT Vehicle Positions feed. The feed can be consumed by the [GTFS-RT Validator](https://github.com/MobilityData/gtfs-realtime-validator) without errors.

### Milestone 2: Authentication + Admin API (~50 hours, Weeks 3–5)

**Deliverable:** The server has secure authentication for drivers and API consumers, plus admin endpoints for managing vehicles and drivers.

- Implement driver authentication:
  - `POST /api/v1/auth/login` — phone + PIN → JWT token
  - JWT middleware for all driver-facing endpoints
  - Token refresh flow
- Implement API key authentication for feed consumers (separate from driver auth)
- Implement admin CRUD endpoints:
  - Vehicles: create, read, update, deactivate
  - Drivers: create, read, update, deactivate, assign to vehicle
  - Trips: start, end, list active, list historical
- Implement basic authorization (admin vs. driver roles)
- Implement system status endpoint (`GET /api/v1/admin/status`)
- Write tests for auth flows and admin operations

**Exit Criteria:** Drivers can log in and submit locations with a valid token. Unauthorized requests are rejected. Admins can manage vehicles and drivers via the API.

-----

> **⏰ Midterm Evaluation (July 6–10)**
> 
> At this point, the contributor should have a functional server that: (1) authenticates drivers, (2) ingests location reports, (3) serves a valid GTFS-RT feed, and (4) has admin management endpoints. The server should be deployable via Docker. The mentor evaluates progress, adjusts scope if needed, and confirms direction for the Android app work.

-----

### Milestone 3: Android App — Core Tracking (~80 hours, Weeks 5–8)

**Deliverable:** A working Android app that captures GPS locations continuously via a foreground service and sends them directly to the server.

- Set up Android project (Kotlin, Jetpack Compose, Hilt)
- Implement driver login screen (phone + PIN → JWT from server)
- Implement vehicle selection and trip start/end flow
- Implement location tracking foreground service:
  - `FusedLocationProviderClient` with configurable interval (default 10s)
  - Persistent notification showing tracking status
  - Direct POST of each location fix to the server
- Implement connection status monitoring:
  - Visual indicator: green (connected, sending) / red (no connection or GPS unavailable)
  - Graceful handling of failed sends (log and skip, do not crash)
- Implement the main tracking UI:
  - Connection status indicator
  - Trip duration and distance counters
  - Large “End Trip” button
- Handle edge cases:
  - App killed by OS → foreground service restarts, resumes tracking
  - GPS unavailable → show warning, continue attempting to acquire fix
  - Token expired → prompt re-authentication
  - Network unavailable → clear visual warning, continue capturing GPS (send resumes when connection returns)

**Exit Criteria:** The app can track a driver’s location for an extended period (30+ minutes), sending each fix to the server in near-realtime. Location points appear in the GTFS-RT feed within seconds of being sent. The foreground service survives the app being backgrounded.

### Milestone 4: Admin Interface + End-to-End Polish (~80 hours, Weeks 8–10)

**Deliverable:** A functional admin web interface and polished end-to-end user experience.

- Build admin web UI:
  - Dashboard: active vehicles count, feed health, last update times
  - Vehicle map: Leaflet/OSM showing current vehicle positions
  - Vehicle management: CRUD interface
  - Driver management: CRUD interface, vehicle assignment
  - Trip history: searchable list with location trail visualization
- Polish the Android app:
  - Handle all permission request flows (location, notification, battery optimization)
  - Add onboarding screens explaining permissions
  - Test on low-end Android devices (Android 8.0+, 2GB RAM)
  - Localization setup (English + string extraction for future translations)
  - Dark mode support
- Polish the server:
  - Request rate limiting
  - Configurable staleness threshold for GTFS-RT feed
  - Health check endpoint for monitoring
  - Structured logging (JSON) for production debugging

**Exit Criteria:** Admin can manage the system through a web browser. The Android app handles all permission and lifecycle edge cases gracefully. The system works end-to-end: driver opens app → starts trip → drives → locations appear in GTFS-RT feed → admin sees vehicle on map.

### Milestone 5: Documentation, Testing & Deployment (~80 hours, Weeks 10–12)

**Deliverable:** Production-ready documentation and comprehensive testing.

- Write deployment documentation:
  - Quick-start guide (docker-compose up → working system)
  - Production deployment guide (PostgreSQL, reverse proxy, TLS, systemd)
  - Android APK distribution guide (sideloading for agencies without Play Store access)
  - Operator manual: how to onboard drivers, manage vehicles, monitor feed health
- Write architecture documentation:
  - System architecture diagram
  - API reference (OpenAPI/Swagger spec)
  - Data retention and privacy considerations
- Comprehensive testing:
  - Server: unit tests, integration tests, GTFS-RT feed validation
  - Android: unit tests for location service, UI tests for critical flows
  - End-to-end: simulated multi-vehicle scenario
  - Stress testing: 50+ simultaneous vehicles reporting
- Create a demo environment:
  - Pre-loaded sample GTFS data for a fictional agency
  - Script to simulate multiple vehicles sending location data
  - Connect demo feed to a local OBA server instance to show the full loop

**Exit Criteria:** A transit agency IT person can follow the documentation to deploy the system from scratch. All tests pass. The demo environment works and can be shown to prospective agency partners.

-----

> **📦 Final Submission (August 24)**
>
> **Final Evaluation (August 24 – August 31)**

-----

## 6. Deliverables Summary

|#|Deliverable                                |Format                              |
|-|-------------------------------------------|------------------------------------|
|1|Go server with GTFS-RT feed generation     |Open-source repository, Docker image|
|2|Android driver tracking app                |Open-source repository, signed APK  |
|3|Admin web interface                        |Bundled with server                 |
|4|API documentation (OpenAPI spec)           |Markdown + generated docs           |
|5|Deployment guide (quick-start + production)|Markdown                            |
|6|Operator manual                            |Markdown                            |
|7|Architecture documentation                 |Markdown + diagrams                 |
|8|Demo environment with simulator            |Docker-compose + scripts            |
|9|Blog post / project report                 |Published on OTSF website           |

## 7. Risks and Mitigations

|Risk                                                                                 |Likelihood|Impact|Mitigation                                                                                                                                                                                                                         |
|-------------------------------------------------------------------------------------|----------|------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|Contributor unfamiliar with Go or Android (one of the two)                           |High      |Medium|Community bonding period includes intensive ramp-up on the weaker skill; project is structured so server (Milestones 1–2) and Android (Milestones 3–4) are sequential, allowing focused learning                                   |
|Android background location tracking is killed by aggressive OEM battery optimization|Medium    |High  |Use foreground service with persistent notification; document per-manufacturer workarounds (Xiaomi, Samsung, Huawei are known offenders); test on [dontkillmyapp.com](https://dontkillmyapp.com) devices                           |
|Scope is too large for 12 weeks                                                      |Medium    |Medium|Milestones are ordered by priority — Milestones 1–3 represent the core MVP. Admin UI (Milestone 4) and docs (Milestone 5) can be simplified if time is tight. The contributor can also request a timeline extension up to 22 weeks.|
|GTFS-RT protobuf generation has subtle spec compliance issues                        |Low       |Medium|Validate feed output against the MobilityData GTFS-RT Validator early and often; study existing feeds from agencies like MBTA and King County Metro as reference implementations                                                   |
|No real transit agency available for testing                                         |Low       |Low   |Build a vehicle simulator that generates realistic GPS traces along GTFS routes; use this for all testing and demos. Real-world testing with a partner agency is a stretch goal.                                                   |
|Network conditions are worse than expected in target regions                         |Medium    |Medium|v1 accepts this limitation — locations are only reported when connected. The architecture is designed so v2 offline queuing can be added without restructuring. Clear UX communicates connection status to the driver.             |

## 8. How This Connects to OneBusAway

The output of this project — a GTFS-RT Vehicle Positions feed — is a standard that OBA already consumes natively. Once a transit agency deploys the Vehicle Tracker server and equips its drivers with the Android app, the generated feed can be pointed at any OBA server instance as a realtime data source. No changes to OBA are required.

This makes the Vehicle Tracker a force multiplier for OBA adoption: it removes the single biggest prerequisite (an AVL system) that has historically prevented transit agencies in developing countries from using OneBusAway. A city that has a GTFS static feed and a fleet of Android phones can go from zero realtime data to a fully functional OBA deployment in a day.

The Vehicle Tracker server is intentionally built as a standalone service (not integrated into Maglev) so that it can be deployed independently by agencies that may not run their own OBA server, and so that the GTFS-RT feed can be consumed by any compliant application, not just OneBusAway.

## 9. Future Work

### v2: Offline Queuing and Sync

The most important enhancement after v1 ships. In areas with intermittent connectivity, v1 drops location points when the network is unavailable. v2 adds a full offline-first architecture to the Android app:

- **Local storage:** Room database as a write-ahead buffer. Every GPS fix is persisted locally before any network request is attempted.
- **Batch sync:** When connectivity is available, a WorkManager periodic task batches unsynced points and POSTs them to a new server endpoint (`POST /api/v1/locations/batch`) that accepts arrays of timestamped points.
- **Server-side deduplication:** Idempotent ingestion keyed on `vehicle_id + timestamp` to handle duplicate submissions from retries.
- **Connectivity-aware sync triggers:** `ConnectivityManager` callbacks to initiate immediate sync when the network becomes available after an outage.
- **Staleness handling:** The server accepts backfilled historical points for the database (operational analytics) but only uses the most recent point for the GTFS-RT feed.
- **Status UX:** Yellow “offline — X points queued” indicator in addition to green/red.

This is a significant engineering effort (estimated 60–80 hours) involving careful protocol design, idempotency guarantees, and extensive testing of edge cases (app killed while offline, phone rebooted with pending queue, token expiry during offline period). It was intentionally excluded from v1 to keep the GSoC scope achievable.

### Other Future Work

- **Arrival predictions:** Use the historical location data to estimate arrival times at stops, generating GTFS-RT TripUpdate feeds in addition to Vehicle Positions
- **Driver incentives and gamification:** Track on-time performance, route adherence, and other metrics to help agencies improve service quality
- **Passenger counting:** Integrate with Android’s camera or simple tap-counter UI to estimate ridership
- **Multi-agency support:** Add tenant isolation so a single server instance can serve multiple transit agencies
- **Push notifications for operators:** Alert when a vehicle goes off-route, a driver hasn’t started their scheduled trip, or a vehicle has been stationary too long
- **iOS driver app:** For agencies where drivers use iPhones (uncommon in target markets but possible)
- **Integration with The Transit Clock:** Connect the vehicle positions to The Transit Clock for more sophisticated arrival prediction
- **OBACloud hosted offering:** Offer Vehicle Tracker as a managed service through OBACloud so agencies don’t need to run their own server

## 10. References

- [GTFS-RT Specification](https://gtfs.org/documentation/realtime/proto/)
- [GTFS-RT Vehicle Positions — Google reference](https://developers.google.com/transit/gtfs-realtime)
- [GTFS-RT Validator (MobilityData)](https://github.com/MobilityData/gtfs-realtime-validator)
- [gtfs-realtime-bindings for Go](https://github.com/MobilityData/gtfs-realtime-bindings)
- [Maglev — OneBusAway next-generation server (Go)](https://github.com/OneBusAway/maglev)
- [OneBusAway Android app](https://github.com/OneBusAway/onebusaway-android)
- [OneBusAway REST API documentation](https://developer.onebusaway.org/api/where)
- [Android Foreground Services documentation](https://developer.android.com/develop/background-work/services/foreground-services)
- [Android FusedLocationProviderClient](https://developers.google.com/android/reference/com/google/android/gms/location/FusedLocationProviderClient)
- [Don’t Kill My App — OEM battery optimization reference](https://dontkillmyapp.com)
- [GSoC 2026 Timeline](https://developers.google.com/open-source/gsoc/timeline)
- [GSoC Contributor Time Management Guide](https://google.github.io/gsocguides/student/time-management-for-students)
