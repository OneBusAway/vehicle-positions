# iOS Driver App with CarPlay — Design Spec

**Date:** 2026-09-14
**Status:** Implemented 2026-09-15 (plans: 2026-09-14-gtfs-catalog-server.md, 2026-09-14-ios-driver-app.md, 2026-09-14-carplay.md)
**Scope:** An iOS driver app for the `vehicle-positions` server, delivered in this repo under `ios/VehicleTracker`, whose in-cab surface is a CarPlay navigation scene showing a live map of the driver's assigned trip, their position on it, and how well they are keeping to the route and the schedule. Three phases: (A) a GTFS catalog API on the server, (B) the iOS app proper, (C) the CarPlay scene.

---

## 1. Overview

Today the server has two ingestion paths. Drivers use the Android app in `android/`: log in, pick an assigned vehicle, type a route id, and stream one location report every ten seconds to `POST /api/v1/locations`, which the server republishes verbatim. Riders use the `VehiclePositionsKit` SDK in `ios/`; their reports are verified against a GTFS static feed (shape, schedule, calendar) by the engine in `rider/`.

This spec adds an iOS driver app whose main screen is the car's own display. A driver signs in on the phone, picks their vehicle, picks the trip they are about to run (route first, then the runs active now), and starts. From then on CarPlay shows the route shape, the stops, the vehicle, the next stop with its scheduled time, and a running schedule deviation ("3 min late"), coloured the way OneBusAway riders already read it. The phone keeps reporting positions to the server exactly as the Android app does.

Route adherence is computed **on the phone** from a trip's shape and stop times, which the server serves once at trip start. The projection and schedule-interpolation rules are a port of the Go engine's `rider/shape.go` and `rider.ScheduledOffsetAt`, and the thresholds come from the server in the same payload, so the phone's judgement and the server's stay aligned by construction. The server's driver path is otherwise unchanged: it does not verify driver reports and does not need to.

### 1.1 Decisions already taken

| Question | Decision |
|---|---|
| Which app | A new iOS driver app in this repo, the iOS counterpart of `android/`. Not the OneBusAway rider app, and not a dogfood shell around the rider SDK. |
| README non-goal | The project README lists an iOS driver app as a GSoC non-goal. The project owner is overriding that here; the README's non-goals section gains a pointer to this spec. |
| A real map on CarPlay | Yes. `CPMapTemplate` needs the `com.apple.developer.carplay-maps` entitlement, which Apple grants to navigation apps after review. Xcode's simulator only requires the key in the entitlements file, so development and dogfooding proceed now; running in a real car waits on Apple. The entitlement request is the owner's task, outside this spec. |
| Where adherence is computed | On the phone, from server-served trip geometry. Smooth updates at GPS rate; survives connectivity gaps; costs one Swift port of ~150 lines of geometry that is table-tested against the same cases as the Go original. |
| Trip selection | Route, then trips active on today's service date, from a GTFS catalog the server exposes. Manual trip-id entry is gone; adherence needs a real GTFS trip. |
| Process | One spec, three phases, one plan; implementation runs without further questions unless blocked. |

### 1.2 Sources consulted

Apple documentation via sosumi: CarPlay overview, *Requesting CarPlay Entitlements*, *Displaying Content in CarPlay*, *Using the CarPlay Simulator*, `CPMapTemplate`, `CPNavigationSession`, `CPTravelEstimates`, `CPMapPanel`, and the CarPlay Human Interface Guidelines. context7 has no entry for Apple's CarPlay framework itself, only third-party wrappers, so it was not used for the CarPlay API; it remains the reference for any library that enters later.

---

## 2. Goals

1. A driver with an assigned vehicle can, from the phone or from the car screen, pick a route and a run active now, start the trip, and have positions reported to `POST /api/v1/locations` for the duration.
2. While a trip is active, CarPlay shows a map with the trip shape, its stops, the vehicle, and the projected position on the shape; the next stop with its scheduled time; distance and time to it; and the schedule deviation, coloured on time / early / late / off route.
3. The same adherence state is visible on the phone's tracking screen, so the app is useful without CarPlay.
4. The server serves a GTFS catalog to authenticated drivers: routes, a route's trips on a service date, and one trip's geometry and schedule with absolute times. Loading GTFS no longer requires rider mode.
5. Every rule the phone applies to decide on-route and schedule deviation matches the server's rider engine, and is tested against the same cases.
6. The whole loop runs in Xcode's CarPlay simulator against a local server, with a documented smoke test like `docs/android-smoke-test.md`.

## 3. Non-Goals

- **Getting the entitlement from Apple**, the App Store listing, and TestFlight. The owner submits the request; the code is ready when it lands.
- **CarPlay Dashboard scene, instrument cluster, Siri, voice control.** Navigation-app extras that add nothing to adherence.
- **Turn-by-turn road directions.** This is not a routing app. The "maneuvers" CarPlay shows are the trip's stops.
- **Offline queueing of location reports.** Same as Android v1: a failed send is dropped. Adherence still works offline because the geometry is on the phone.
- **Token refresh, stored passwords, biometric unlock.** The JWT lives 24 h; on 401 the driver signs in again on the phone.
- **Server-side verification of driver reports.** Drivers are trusted, as today. Nothing in `rider.Verify` is applied to them.
- **Frequency-based trips (`frequencies.txt`).** Trips with frequencies are listed but their scheduled times are the ones in `stop_times.txt`; the engine already treats them that way.
- **Renaming the `rider/` package.** The GTFS index it holds now serves drivers too. The package keeps its name; extraction is a follow-up.
- **Rejoining location delivery after iOS terminates the app.** Same limitation as the rider SDK: the app relaunches to the tracking screen in a paused state and the driver taps Resume from the foreground.
- **Localization beyond a String Catalog** with English as the base. Layouts use leading/trailing so RTL is not blocked.
- **Android parity for the trip picker.** The Android app keeps manual route entry; giving it the catalog is a separate change.

---

## 4. Phase A — Server: GTFS catalog for drivers

### 4.1 Configuration

`GTFS_STATIC_URL` alone now loads the schedule. Rider mode still requires it, as before.

| Setting | Before | After |
|---|---|---|
| `GTFS_STATIC_URL` set, `RIDER_MODE_ENABLED` unset | Ignored | Index loads at startup (failure is fatal), refreshes every `GTFS_STATIC_REFRESH`, catalog routes registered. |
| Both set | Rider mode loads the index | Same index, loaded once, shared by rider mode and the catalog. |
| Neither | Nothing | Nothing; catalog routes are not registered (`404`). |

Wiring: `newGTFSRuntime(ctx, source, refresh) (*gtfsRuntime, error)` in a new `gtfs_wiring.go` owns `rider.LoadIndex` and the `rider.Refresher`; `newRiderRuntime` takes the `*rider.Refresher` instead of loading its own. `main.go` grows one block: build the GTFS runtime when `GTFS_STATIC_URL` is set, pass its refresher to rider mode and to `newHandler`. `riderConfigFromEnv` keeps its "required when enabled" check.

### 4.2 Index extensions (`rider/index.go`)

The index keeps what the picker and the map need and nothing else:

- `RouteInfo{ID, ShortName, LongName, Color, TextColor string; Type int; SortOrder *int32}` from `routes.txt`. Only routes with at least one indexed trip are kept.
- `TripInfo` gains `Headsign string`, `DirectionID int` (0, 1, or -1 when absent; rendered as `null` in JSON), and each `StopTimeInfo` gains `StopName string`.
- `(*Index).Routes() []RouteInfo` sorted by `SortOrder` (nil last), then `ShortName` (natural order: "7" before "10"), then `LongName`.
- `(*Index).Route(id string) (RouteInfo, bool)`.
- `(*Index).TripsOnRoute(routeID, serviceDate string) []*TripInfo` — trips on that route active on that date, sorted by first departure then id. Built from a `map[routeID][]*TripInfo` filled at index time.

Memory: stop names are one string per stop time; for a 10 000-trip feed that is a few megabytes. Acceptable. `Stats()` gains `Routes int`.

### 4.3 Endpoints

All three require a driver or admin token (`requireAuth`, which already rejects rider tokens). Responses are JSON; errors use the existing `{"error": "..."}` shape. Path ids are URL-decoded (`r.PathValue`). Registered in `newHandler` only when a GTFS index exists.

**`GET /api/v1/gtfs/routes`**

```json
{"routes":[{"id":"1_100223","short_name":"8","long_name":"Seattle Center - Rainier Beach","color":"0077C0","text_color":"FFFFFF","type":3}]}
```

**`GET /api/v1/gtfs/routes/{route_id}/trips?date=YYYYMMDD`**

`date` defaults to `Index.ServiceDate(now)` (agency-local; before 03:00 belongs to the previous service day, as rider mode already does). A malformed date is `400`; an unknown route is `404`.

```json
{"route_id":"1_100223","service_date":"20260914","timezone":"America/Los_Angeles",
 "trips":[{"id":"1_604321","headsign":"Rainier Beach","direction_id":0,
           "starts_at":"2026-09-14T07:02:00-07:00","ends_at":"2026-09-14T08:11:00-07:00",
           "first_stop":"Seattle Center","last_stop":"Rainier Beach Station"}]}
```

`starts_at` is the first stop's departure and `ends_at` the last stop's arrival, as RFC 3339 in the agency zone, from `rider.ServiceDayStart` plus the `stop_times.txt` offsets (which may exceed 24 h). Sorted by `starts_at`.

**`GET /api/v1/gtfs/trips/{trip_id}?date=YYYYMMDD`**

Same `date` default. Unknown trip is `404`; a trip not active on the date is `422 {"error":"trip not active on date"}`, mirroring rider start.

```json
{"id":"1_604321","route_id":"1_100223","headsign":"Rainier Beach","direction_id":0,
 "service_date":"20260914","timezone":"America/Los_Angeles",
 "route":{"short_name":"8","long_name":"…","color":"0077C0","text_color":"FFFFFF"},
 "shape":{"length_m":18342.5,"points":[[47.6205,-122.3493],[47.6207,-122.3490]]},
 "stops":[{"id":"1_75403","name":"Seattle Center","sequence":1,"lat":47.6205,"lon":-122.3493,
           "along_shape_m":0,"arrival_at":"2026-09-14T07:02:00-07:00","departure_at":"2026-09-14T07:02:00-07:00"}],
 "thresholds":{"max_shape_distance_m":60,"schedule_early_s":900,"schedule_late_s":5400}}
```

`points` are `[lat, lon]` pairs in shape order; a long urban shape is a few thousand points, well under 100 KB. `along_shape_m` is the index's `AlongShape`, so stop positions along the route are the same numbers the server uses. `thresholds` are the server's `rider.Thresholds` values for shape distance and the schedule window: the phone applies exactly these, so an operator tuning `RIDER_MAX_SHAPE_DISTANCE` tunes the driver's display too.

### 4.4 Tests

- `rider/index_test.go` gains cases for `Routes` ordering, `TripsOnRoute` on an active and an inactive date, headsign and stop name capture, using the committed `rider/testdata/fixture.zip` (regenerated under `WRITE_FIXTURE=1` after `fixture_test.go` gains `trip_headsign` and `route_sort_order`; it already has route short and long names and stop names).
- `gtfs_handlers_test.go`: each endpoint's happy path, `400`/`404`/`422`, absolute-time rendering across a trip that crosses midnight, and the `403` for a rider token.
- `route_wiring_test.go`: routes present with an index, absent without.
- `gtfs_wiring_test.go`: `GTFS_STATIC_URL` without rider mode loads; rider mode without it still fails startup.

---

## 5. Phase B — iOS driver app

### 5.1 Project

```
ios/
  VehiclePositionsKit/          # existing rider SDK, unchanged
  VehicleTracker/
    project.yml                 # XcodeGen; VehicleTracker.xcodeproj is generated and git-ignored
    VehicleTracker.entitlements
    App/                        # @main App, AppDelegate, DI container
    Model/                      # API DTOs, TrackerAPI client, KeychainTokenStore, persisted state
    Engine/                     # ShapeGeometry, ScheduleInterpolator, AdherenceEvaluator, TripSession
    UI/                         # SwiftUI screens: Login, Vehicles, Routes, Trips, Tracking
    Map/                        # RouteMapViewController (MKMapView) shared by phone and CarPlay
    CarPlay/                    # CarPlaySceneDelegate, template builders
    Resources/                  # Assets, Localizable.xcstrings, gpx/ simulated routes
  VehicleTrackerTests/          # unit tests hosted by the app
```

- **XcodeGen**, like the OneBusAway iOS app. `xcodegen generate` is documented in `docs/development.md`; CI runs it. Bundle id `org.onebusaway.vehicletracker`, display name "OBA Vehicle Tracker".
- **Deployment target iOS 26.4**, Swift 6 language mode, strict concurrency, `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor` for the app target (engine types are `nonisolated` and `Sendable`). 26.4 rather than 26.0 because `CPTrip`'s `MKMapItem` initializer is deprecated and its replacement, `init(originWaypoint:destinationWaypoint:routeChoices:)` with `CPNavigationWaypoint`, is iOS 26.4+. iOS 27-only CarPlay API (`CPMapPanel`) is not used.
- **Dependency on `../VehiclePositionsKit`** (local package) for `CoreLocationSource`, `LocationFix`, `LocationSample` and `LocationDiagnostic`. The source is constructed with `.automotiveNavigation`, the profile for the vehicle's own driver. Nothing else from the SDK is used; `RiderCredentials` is the wrong shape for a driver, so the app has its own 40-line Keychain item for the JWT.
- **No third-party dependencies.**

### 5.2 Screens (SwiftUI, phone)

Mirrors the Android flow with the trip picker replaced:

1. **Login** — server URL (pre-filled from last use), email, password → `POST /api/v1/auth/login`. Token in the Keychain, URL and email in `UserDefaults`. Skipped when a stored token is under 24 h old. Plain `http://` is refused unless the host is `localhost`, `127.0.0.1` or `*.local`, matching the SDK's rule for development servers.
2. **Vehicles** — `GET /api/v1/vehicles`; auto-advance when exactly one.
3. **Routes** — `GET /api/v1/gtfs/routes`, searchable, recent routes pinned on top (`UserDefaults`, last five).
4. **Trips** — `GET /api/v1/gtfs/routes/{id}/trips`; rows "07:02 → Rainier Beach"; the run whose window contains now (or the next to start) is scrolled into view and marked. Tapping starts: fetch `GET /api/v1/gtfs/trips/{id}`, then `POST /api/v1/trips/start` with `vehicle_id`, `route_id`, `gtfs_trip_id`. Server `403`/`409` shown inline.
5. **Tracking** — big status (green "Reporting", red "No connection" / "No GPS" / "Signed out"), route badge and headsign, schedule deviation in OneBusAway colours, next stop and its scheduled time, fixes sent, elapsed time, a map (the shared `RouteMapViewController` in a `UIViewControllerRepresentable`), and a large End Trip button with confirmation → `POST /api/v1/trips/end`.

Touch targets ≥ 44 pt, primary actions ≥ 64 pt, high contrast, light and dark.

### 5.3 TripSession — the one source of truth

`TripSession` is a `@MainActor @Observable` object owned by the app container and observed by both the phone UI and the CarPlay scene. It holds:

- `phase`: `.signedOut`, `.idle(vehicles)`, `.starting`, `.active(ActiveTrip)`, `.paused(ActiveTrip)` (after relaunch), `.ending`.
- `ActiveTrip`: server trip id (numeric, for `/trips/end`), `TripGeometry` (the catalog payload, decoded), vehicle, started-at.
- `latest: Adherence?` — updated on every fix.
- `reporting: ReportingStatus` — `.connected(fixesSent)`, `.noNetwork`, `.noGPS`, `.authExpired`, `.clockSkew`.

On `start`:
1. `POST /trips/start` (fails inline on 403/409).
2. Persist `ActiveTrip` to the app's Application Support directory as JSON (so relaunch can show `.paused`).
3. Begin `CoreLocationSource.updates()` and `beginBackgroundActivity()`. Core Location requires a new background activity session to be created while the app is in the foreground; the phone screen qualifies. Whether an active CarPlay scene alone counts is not documented by Apple and is verified on the first CarPlay start. If Core Location reports `insufficientlyInUse`, the status shows "Open the app on iPhone" and the session stays active.
4. For each fix: `AdherenceEvaluator.evaluate(fix, previous:)` → `latest`; then the reporter (below).

On `end`: stop the stream, invalidate the background activity, `POST /trips/end` (retry dialog with "end locally anyway", as Android), clear persisted state.

### 5.4 Reporter

One `POST /api/v1/locations` per accepted fix, with `vehicle_id`, `trip_id` = the GTFS trip id, `latitude`, `longitude`, `bearing` (only when in 0…360), `speed` (clamped ≥ 0), `accuracy`, `timestamp` (fix time, Unix seconds). The server allows one report per five seconds per driver, so the reporter sends a fix only when at least five seconds have passed since the last *sent* one and drops the rest; the map still updates from every fix. Error mapping copies Android's: `401` → `.authExpired` and a re-login prompt that keeps the trip; `429` → drop silently; `400` mentioning `timestamp` three times running → `.clockSkew`; other errors → log and drop; transport error → `.noNetwork` until the next success. `Content-Type: application/json`, no unknown fields, HTTPS-only outside the development hosts, no redirects off the server (the same two rules the SDK's transport applies).

### 5.5 Engine

Pure, `nonisolated`, `Sendable`, unit-tested:

- **`ShapeGeometry`** — port of `rider/shape.go`. Equirectangular local projection scaled by the cosine of the shape's mean latitude; cumulative metres per vertex; `project(_:hint:)` computes point-to-segment distance for every segment, collects local minima within 2× the best distance, and returns the one nearest the hint when a hint is given; `point(at:)`, `bearing(at:)`, `length`. Distances by haversine, as the Go code.
- **`ScheduleInterpolator`** — port of `rider.ScheduledOffsetAt`: for an along-shape distance, the scheduled time interpolated between the surrounding stops' departure and arrival; clamped to the first stop's arrival before the first stop and the last stop's arrival after the last, exactly as the Go function does.
- **`AdherenceEvaluator.evaluate(fix, previous) -> Adherence`**:
  - `projection` (along-shape, distance-to-shape, bearing)
  - `isOnRoute` = distance ≤ `thresholds.maxShapeDistance + max(accuracy, 0)` — the server's rule verbatim
  - `scheduleDeviation` = fix time − scheduled time at the projected position (positive is late)
  - `nextStop` = first stop with `along_shape_m` > projected along-shape (or the last stop), `distanceToNextStop`, `timeToNextStop` (distance ÷ max(speed, 3 m/s), capped at the scheduled gap)
  - `status`: `.offRoute` when not on route; else `.early` when deviation < −60 s, `.late` when > +300 s, `.onTime` otherwise; `.offSchedule` when outside the server's window. Colours follow OneBusAway: green on time, red early, blue late, grey off route. The ±60 s / +300 s display thresholds are named constants; the server's window is the hard one.
  - The hint for the next projection is the previous along-shape when the previous fix was on route; off-route fixes do not advance the hint, so a detour rejoining a loop snaps back to the right pass.

### 5.6 Persistence and relaunch

`UserDefaults`: server URL, email, recent routes, last vehicle. Keychain: JWT (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`). Application Support: `active-trip.json`. On launch with an `active-trip.json`, the app shows Tracking in `.paused` with a Resume button; Resume restarts the stream (foreground required) without calling `/trips/start` again. Ending from `.paused` calls `/trips/end` as usual.

### 5.7 Info.plist and capabilities

`NSLocationWhenInUseUsageDescription`; `UIBackgroundModes: [location]`; `UIApplicationSceneManifest` with `UIApplicationSupportsMultipleScenes = true` and the CarPlay scene (§6.1), whose `UISceneDelegateClassName` is the runtime class name, `$(PRODUCT_MODULE_NAME).CarPlaySceneDelegate`; `CFBundleDisplayName`. The entitlements file carries `com.apple.developer.carplay-maps = true`. Signing is automatic for the simulator; the plan notes where a real provisioning profile plugs in.

---

## 6. Phase C — CarPlay scene

### 6.1 Scene

`CarPlaySceneDelegate: UIResponder, CPTemplateApplicationSceneDelegate`, declared in the scene manifest under `CPTemplateApplicationSceneSessionRoleApplication`. Because the app has the navigation entitlement it implements `templateApplicationScene(_:didConnect:to:)`, sets `window.rootViewController = RouteMapViewController()`, and sets a `CPMapTemplate` as root. It holds the interface controller and window for the session and observes `TripSession` with `withObservationTracking(_:onChange:)` to re-render templates on change; that call is one-shot and its `onChange` is not on the main actor, so the delegate re-arms it from `MainActor` inside every `onChange`. The SwiftUI `App` handles the phone's `UIWindowSceneSessionRoleApplication`; the CarPlay scene comes from the plist. If the two lifecycles do not cooperate in the simulator, the fallback is a `UIApplicationDelegateAdaptor` whose `application(_:configurationForConnecting:options:)` returns, for `session.role == .carTemplateApplication`, a `UISceneConfiguration` with `delegateClass = CarPlaySceneDelegate.self`; the plan verifies this on the first CarPlay launch.

### 6.2 States

| `TripSession.phase` | Map | Navigation bar | Buttons |
|---|---|---|---|
| `.signedOut` | Follows the phone's location | Title "Sign in on iPhone" | none |
| `.idle` | Follows location | Title "OBA Vehicle Tracker" | Trailing "Start trip" → §6.4 |
| `.starting` | — | Title "Starting…" | none |
| `.active` | §6.3 | Leading "End", trailing "Details"; while panning, trailing "Done" | Map buttons (three is the maximum): pan, zoom in, zoom out |
| `.paused` | Shape drawn, no vehicle | Title "Resume on iPhone" | Leading "End" |

Errors are shown as `CPAlertTemplate`s on the car screen, never by pointing at the phone, except the two setups that iOS itself requires on the phone (sign-in, and a paused session's foreground restart), which are named plainly.

### 6.3 Active trip on the map

- **Base view**: `RouteMapViewController` wraps an `MKMapView` (no user interaction; the template owns input). It draws the shape as an `MKPolyline` in the route colour (fallback: system blue) with a darker casing, stop annotations as small circles with the next stop enlarged and labelled, a vehicle annotation rotated to the fix's course, and a small dot for the projected position on the shape when the vehicle is off route. Off route: the polyline dims and the vehicle marker turns grey. The map view has scroll, zoom, rotate and pitch disabled; the template owns input. Appearance follows the scene's `contentStyleDidChange(_:)` via `overrideUserInterfaceStyle`, so light and dark both render. Camera follows the vehicle, heading-up, centred a third of the way up the screen, at a zoom that keeps roughly 800 m ahead in view. The pan map button calls `showPanningInterface(animated:)`, which hides the map buttons and suspends following; the app moves its own camera in the delegate's `mapTemplate(_:panWith:)` (knobs and buttons, by `PanDirection`) and `mapTemplate(_:didUpdatePanGestureWithTranslation:velocity:)` (touchscreens). CarPlay provides no dismiss control, so a trailing "Done" navigation bar button calls `dismissPanningInterface(animated:)`, and `mapTemplateDidDismissPanningInterface(_:)` restores following. The bar holds at most two leading and two trailing buttons, which "End", "Details" and "Done" fit.
- **Navigation session**: on `.active` the delegate builds a `CPTrip` from `CPNavigationWaypoint`s for the first stop (origin) and the last (destination) with one `CPRouteChoice` (summary: the headsign) and calls `startNavigationSession(for:)`. Each stop is a `CPManeuver` whose `instructionVariants` carry the stop name, scheduled time and deviation, longest first (`["Pike St & 3rd Ave · 7:14 · 3 min late", "Pike St & 3rd Ave · 3 min late", "Pike St & 3rd Ave"]`; the system shows the first that fits), symbol a bus-stop glyph. `upcomingManeuvers` is the next stop (and the one after it); `updateEstimates` on every adherence update with distance and time to the next stop. Trip-level estimates via `mapTemplate.update(_:for:with:)` carry distance and time to the last stop, and the `CPTimeRemainingColor` encodes adherence: green on time, orange late, red early, `.default` off route. That enum has no blue, so the estimates bar is the one place late is orange rather than OneBusAway blue; the map marker, the maneuver text and the details template use the OneBusAway colours from §5.5. `CPManeuver` has no secondary text, so the deviation lives in the instruction variants above and the maneuver is replaced when the deviation string changes. `finishTrip()` on end; `cancelTrip()` if the driver ends from the car's own navigation cancel.
- **Off-route alert**: a `CPNavigationAlert` ("Off route — 140 m from the shape", one primary action "OK") with `duration` 0 so it does not time out, shown once per off-route episode and dismissed with `dismissNavigationAlert(animated:completion:)` when back on route.
- **Details**: the trailing button pushes a `CPInformationTemplate` with rows Route, Trip (headsign, start time), Schedule ("3 min late"), Next stop (name, scheduled, distance), Reporting ("Connected · 214 sent" or the problem), GPS (accuracy). Six rows, under the template's cap of ten items, which it truncates silently. It re-renders while shown.
- **End**: the leading button presents a `CPAlertTemplate` "End trip?" with End / Cancel; End calls `TripSession.end()`.

### 6.4 Starting a trip from the car

Sign-in stays on the phone. Everything after it works on the car screen with list templates: "Start trip" → `CPListTemplate` of vehicles (skipped when one) → routes (recent first, then all) → trips ("07:02 → Rainier Beach", the current or next run marked) → `CPAlertTemplate` confirm → `TripSession.start`. Lists are capped at the class properties `CPListTemplate.maximumItemCount` and `maximumSectionCount`, read at runtime because they depend on the vehicle, with a trailing "Use iPhone to see more" row if a feed exceeds them. Starting from CarPlay relies on the app being foreground-active through the CarPlay scene, which is the normal case; if Core Location refuses the background session, the map shows "Open the app on iPhone to start" and the trip stays started server-side so the phone's Resume picks it up.

### 6.5 Simulator workflow

Simulator: I/O → External Displays → CarPlay (800 × 480 @2x); `defaults write com.apple.iphonesimulator CarPlayExtraOptions -bool YES` unlocks the other sizes. Location from `xcrun simctl location <udid> start --speed=<m/s> --interval=1 <lat,lon> <lat,lon> …` (at least two waypoints) or a GPX in `Resources/gpx/` derived from the fixture's shape; screenshots with `xcrun simctl io <udid> screenshot --display=external <file>`. Build, run and screenshots go through XcodeBuildMCP with session defaults set to the generated project, the `VehicleTracker` scheme and an iPhone simulator on the newest installed iOS runtime that meets the deployment target. On this Mac the iPhone 17 simulators exist only on iOS 26.3 and the iOS 27.0 runtimes have no devices, so the plan creates one with `xcrun simctl create`.

---

## 7. Data flow

```
Login ──▶ token ──▶ GET /vehicles ──▶ GET /gtfs/routes ──▶ GET /gtfs/routes/{r}/trips
                                                                  │ tap
                                              GET /gtfs/trips/{t} ─┴─▶ POST /trips/start ──▶ TripSession.active
CLLocationUpdate ──▶ LocationFix ──▶ AdherenceEvaluator ──▶ TripSession.latest ──┬─▶ phone Tracking screen
                                        │                                        └─▶ CarPlay map + nav session
                                        └─▶ Reporter (≥ 5 s apart) ──▶ POST /locations ──▶ tracker ──▶ GTFS-RT feed
End ──▶ POST /trips/end ──▶ TripSession.idle
```

## 8. Error handling

| Failure | Behaviour |
|---|---|
| Catalog `404`/`422` on start | Inline on the picker; from CarPlay a `CPAlertTemplate`. |
| `403`/`409` on `/trips/start` | Same, with the server's message ("driver already has an active trip"). |
| Location POST network failure | Status red "No connection"; fixes keep flowing to the map; dropped; recovers on next success. |
| `401` mid-trip | Status "Signed out"; phone prompts re-login; trip and location stream continue; sends resume after login. |
| GPS lost / accuracy limited | Status "No GPS"; adherence frozen at the last fix, marker greyed. |
| Off route | Map dims the shape, grey marker, one `CPNavigationAlert`; reporting continues (the server does not verify drivers). |
| Outside the server's schedule window | Status `.offSchedule` in the deviation colour with the window named; reporting continues. |
| App killed | Relaunch shows Tracking `.paused`; server trip stays open until Resume + End or the operator closes it. |
| End POST fails | Retry dialog; "End locally anyway" leaves the server trip open (known limitation, as Android). |
| CarPlay connects mid-trip | Scene renders the current `TripSession` state immediately; nothing is replayed. |

## 9. Testing

- **Go**: §4.4.
- **Swift unit tests** (`VehicleTrackerTests`, hosted by the app so the Keychain works): `ShapeGeometry` against the cases in `rider/shape_test.go` (straight line, loop with hint, out-and-back, off-shape tolerance, `point(at:)`/`bearing(at:)`); `ScheduleInterpolator` against `rider/index_test.go`'s `ScheduledOffsetAt` cases; `AdherenceEvaluator` status thresholds and hint behaviour; the reporter's throttle and error mapping with a fake transport and a manual clock; DTO decoding of the catalog payloads including a past-midnight trip; trip-list "current run" selection; template builders (a `CPListTemplate`'s rows and a `CPInformationTemplate`'s items are plain objects) for each `TripSession.phase`.
- **Manual**: `docs/ios-smoke-test.md` — local server with the fixture GTFS, seeded driver and vehicle, simulator location playback along the fixture shape, CarPlay window screenshots at on-time, late and off-route moments, and the feed check in `/gtfs-rt/vehicle-positions?format=json`.
- **CI**: a new `ios` job on a macOS runner runs `xcodegen generate` and `xcodebuild test` for `VehicleTracker`, and `xcodebuild test` for `VehiclePositionsKit`, which has had no CI until now. The job selects the newest installed Xcode ≥ 26.

## 10. Phasing

1. **A — server catalog** (Go only; deployable on its own; no client depends on it yet).
2. **B — iOS app** (project, login, vehicles, picker, session, reporter, engine, phone tracking screen with map).
3. **C — CarPlay** (scene, map template, navigation session, lists, details, alerts, smoke test doc, CI job).

Each phase ends with its tests green and a commit; B and C are exercised in the simulator against a local server before being called done.
