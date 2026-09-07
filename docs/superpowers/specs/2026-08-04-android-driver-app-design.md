# Android Driver App — v1 Design

**Date:** 2026-08-04
**Status:** Approved design, pre-implementation
**Scope:** README Milestone 3 (core tracking) plus cheap-now polish: localization readiness, RTL-safe layouts, dark mode, battery-optimization prompt. Onboarding screens and device-matrix testing are deferred.

## Context

The vehicle-positions Go server (this repo) ingests driver location reports and serves a GTFS-RT Vehicle Positions feed. This project adds the companion Android app drivers use to report their location, per the README project spec. Target users are transit drivers in developing countries: low-end Android devices, intermittent connectivity, minimal smartphone experience.

## Decisions

| Decision | Choice |
|---|---|
| Min SDK | 26 (Android 8.0), targetSdk 36 |
| Location | Same repo, `android/` directory (Gradle root) |
| Architecture | Single-module MVVM + Repository (Approach A) |
| Stack | Kotlin, Jetpack Compose (Material 3), Hilt, Retrofit + OkHttp + kotlinx.serialization, Play Services FusedLocationProviderClient, Jetpack DataStore |
| Persistence | DataStore only — **no Room** (v1 has no offline queue; Room arrives with v2) |
| Server URL | Editable field on login screen, persisted; one generic APK for all agencies |
| Auth expiry | Daily login; re-auth prompt on 401 (server JWT lives 24h, no refresh endpoint) |
| API gaps | Add one server endpoint `GET /api/v1/vehicles` (driver's assigned vehicles); route/trip is manual entry with recent-route chips (no server GTFS catalog in v1) |

## Server-Side Addition

`GET /api/v1/vehicles` — authenticated (non-admin) — returns the calling driver's assigned, **active** vehicles. Implementation mirrors `handleListUserVehicles` but derives the user ID from JWT claims instead of a path parameter. Reuses the existing assignment store. Includes route-wiring and handler tests in the existing Go test style.

*Server-side note (resolved 2026-09-06):* the location report now carries `route_id` and `start_date`, and the app sends `trip_id` only when the driver entered a GTFS trip id. The feed's `TripDescriptor` therefore always identifies the route, and never carries a route id in `trip_id`.

## Project Layout

```
android/                          # Gradle root (open this in Android Studio)
  app/
    src/main/kotlin/org/onebusaway/vehicletracker/
      ui/          # Compose screens + ViewModels (login, vehicles, trip, tracking)
      data/        # Repositories, Retrofit API, DTOs, DataStore prefs
      service/     # LocationTrackingService + notification
      di/          # Hilt modules
```

- Gradle Kotlin DSL, version catalog (`libs.versions.toml`).
- Target 36 UI requirements: edge-to-edge is mandatory (`enableEdgeToEdge()` + Scaffold inset handling); predictive back is on by default (safe — no custom back interception planned).
- Dependencies (complete list): Compose BOM, Compose Navigation, Hilt, Retrofit, OkHttp, kotlinx.serialization, Play Services Location, DataStore, Lifecycle. Tests: JUnit, Turbine, MockWebServer, Compose UI test.
- CI: GitHub Actions workflow (assembleDebug + unit tests) alongside the existing Go workflow.

## Screens & Flow

Single activity, Compose Navigation, four screens:

1. **Login** — server URL field (pre-filled from last use), email, password → `POST /api/v1/auth/login`; JWT + URL stored in DataStore. Skipped on launch if a stored token is under 24h old.
2. **Vehicle Select** — list from `GET /api/v1/vehicles`; large touch targets; auto-skip when exactly one vehicle is assigned.
3. **Trip Setup** — manual `route_id` text field plus chips for recently used routes (stored locally in DataStore); optional `gtfs_trip_id` field → `POST /api/v1/trips/start`. Server errors surfaced inline: 403 "not assigned to this vehicle", 409 "you already have an active trip".
4. **Tracking** — giant status indicator (green "Tracking – Connected"; red "No connection" / "GPS unavailable"), trip duration, fixes-sent counter, large End Trip button with confirmation dialog → `POST /api/v1/trips/end`, then stop the service.

**Driver-usability principles (from README):** touch targets ≥48dp (64dp+ for primary actions), high contrast, minimal text, status always glanceable. All strings in `strings.xml` (localization-ready); layouts use start/end (RTL-safe); light/dark Material 3 color schemes.

## Tracking Service & Data Flow

`LocationTrackingService`: foreground service, `foregroundServiceType="location"`, started at trip start, stopped at trip end.

- Fused location updates every 10 seconds (single named constant; configurability deferred).
- Each fix → `POST /api/v1/locations`: `vehicle_id`, `trip_id` (GTFS trip ID string when provided, else route ID — matching what the server publishes into GTFS-RT), `latitude`, `longitude`, `bearing`, `speed`, `accuracy`, `timestamp`. Fire-and-forget: a failed send logs, updates status, and is dropped (v1 spec — no retry, no queue).
- Payload conforms exactly to the server's strict contract: `Content-Type: application/json`, no unknown fields, single JSON object, vehicle ID pattern `[A-Za-z0-9._-]{1,50}`.
- Status published as a `TrackingStatus` StateFlow (`Connected`, `NoNetwork`, `NoGps`, `AuthExpired` + counters) via a singleton `TrackingRepository`; the Tracking screen collects it. Network state from `ConnectivityManager` callbacks; GPS availability from `LocationAvailability` callbacks.
- Persistent notification ("OBA Tracker is active") mirrors the status.
- **Process death:** `START_STICKY`; active trip state persisted in DataStore — both the numeric `trip_id` returned by `trips/start` (needed for `trips/end`) and the string trip identifier sent with each location report, plus vehicle ID and route. A relaunched app rehydrates directly to the Tracking screen. **Resuming location delivery after an OS kill requires `ACCESS_BACKGROUND_LOCATION`** ("Allow all the time"): on Android 14+, a location-type FGS restarted from the background gets no location access with only while-in-use permission. We request it (see Permissions); if the driver declines, a sticky restart shows a "tap to resume tracking" notification and reporting resumes when the app returns to the foreground — documented as the degraded path.
- **Permissions flow** (in order, each with a one-line explanation, all before first trip start):
  1. `ACCESS_FINE_LOCATION` + `ACCESS_COARSE_LOCATION` requested **together** (required on Android 12+; system may grant approximate-only → warn and offer to re-request precise; tracking still runs degraded).
  2. `ACCESS_BACKGROUND_LOCATION` as a separate, settings-directed step after fine location is granted (platform requires the two-step sequence). Decline is non-blocking (degraded restart path above). Play policy is not a concern — distribution is sideloaded APK.
  3. `POST_NOTIFICATIONS` (API 33+). Denial is non-blocking: service runs without a visible notification; show an in-app warning.
  4. Battery-optimization exemption via `ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` the first time tracking starts. Denial non-blocking (warn only).
- **Pre-start check:** device location services must be ON before starting the FGS (a platform prerequisite for the `location` service type) — checked via Play Services `SettingsClient` with its resolution dialog at trip start.
- **Manifest checklist:** `INTERNET`, `ACCESS_FINE_LOCATION`, `ACCESS_COARSE_LOCATION`, `ACCESS_BACKGROUND_LOCATION`, `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_LOCATION` (API 34+ — SecurityException without it), `POST_NOTIFICATIONS`, `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`; service declared with `foregroundServiceType="location"`.
- **Notification details:** dedicated `NotificationChannel` (minSdk 26 requirement). On Android 14+ users can swipe the FGS notification away — tracking continues; the in-app status remains the source of truth.
- **Timestamps:** each report's `timestamp` is the GPS fix time in Unix seconds. The server rejects timestamps outside ±5 minutes of its clock — common on low-end devices with wrong clocks — so repeated 400s are surfaced as a distinct "check device clock" status rather than a generic send failure.
- The send loop and status machine live in a plain class (`TripReporter`) injected into the service; the `Service` subclass stays a thin shell.

## Error Handling

| Failure | Behavior |
|---|---|
| Location POST fails (network) | Status → red `NoNetwork`; keep capturing fixes; drop them; auto-recover to green on next successful send |
| 401 mid-trip | Status → `AuthExpired`; notification + UI prompt to re-login; service keeps running; after re-login, sending resumes on the same trip |
| 403/409 on trip start | Inline human-readable error on Trip Setup |
| GPS lost | Status → red `NoGps`; keep requesting fixes |
| Repeated 400s (clock skew) | Distinct "check device clock" status — server rejects timestamps outside ±5 min of its clock |
| OS kill without background-location grant | Sticky restart posts "tap to resume tracking" notification; reporting resumes when app foregrounds |
| App swiped away / OS kill | Foreground service continues (or restarts sticky); trip state rehydrated from DataStore |
| End-trip POST fails | Dialog with retry; "end locally anyway" option (server trip left open — known v1 limitation, closed by ops) |

## Testing

- **Unit:** ViewModels via Turbine; repositories against MockWebServer (auth interceptor, error mapping, strict-JSON contract); location payload builder validated against the server's exact field names and rules.
- **Service logic:** `TripReporter` tested with fake clock/location/network sources.
- **UI:** Compose tests for each screen's primary flow (login validation, vehicle pick, end-trip confirmation).
- **Go:** handler + route-wiring tests for `GET /api/v1/vehicles`.
- **Manual/E2E:** documented smoke script — emulator GPS playback against a local Go server; verify fixes appear in `/gtfs-rt/vehicle-positions?format=json`.

## Out of Scope (v1)

- Offline queuing/batch sync (v2 — the `TripReporter`/repository seam is where Room + WorkManager slot in)
- Token refresh endpoint; stored credentials
- Server-side GTFS static import / trips catalog
- Onboarding/permission-education screens; low-end device test matrix
- Configurable reporting interval UI
