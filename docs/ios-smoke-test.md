# iOS Driver App — Manual E2E Smoke Test

This is a manual, step-by-step procedure for exercising the iOS driver app
against a real local server and the iOS Simulator (or a physical iPhone) end
to end. It complements the automated suites (`make test` for the server,
`xcodebuild test` for the app) — those check units of behaviour in isolation;
this checks that the whole system works together: sign-in, the vehicle/route/
trip pickers, location permission, GPS capture, adherence on the map, network
loss, relaunch, and the trip lifecycle, as seen through both the app UI and
the GTFS-RT feed.

Run it before cutting a build for a pilot, and after any change that touches
auth, `TripSession`, the location reporter, or the trip lifecycle.

Every command below was run live against the server in this repo while
writing this doc.

## Prerequisites

- Docker + Docker Compose (Postgres)
- Go toolchain (the server runs from source with `go run .`)
- `curl` and `python3`
- Xcode 26+ with an iOS simulator, **or** a physical iPhone. This app is
  iPhone-only and targets iOS 26.4.
- If the `.xcodeproj` is missing, generate it: `cd ios/VehicleTracker &&
  xcodegen generate`.

Two environment notes for the commands below:

- **Xcode 27 RC:** export the toolchain before any `xcodebuild`/`simctl`, and
  always pass `-collect-test-diagnostics never` to `xcodebuild test` (without
  it the RC can hang for ten minutes collecting diagnostics at the end of a
  run).

  ```bash
  export DEVELOPER_DIR=/Applications/Xcode-27.0.0-release.candidate.app/Contents/Developer
  export UDID=$(xcrun simctl list devices available | grep -m1 iPhone | sed -E 's/.*\(([0-9A-F-]{36})\).*/\1/')
  ```

- **Ports.** The obvious defaults are often already taken on a development
  Mac: Postgres.app (or any other local Postgres) owns `127.0.0.1:5432`, so
  `docker compose up -d db` fails to publish its port, and some other
  container may hold `8080`. Check both before you start:

  ```bash
  lsof -nP -iTCP:5432 -sTCP:LISTEN
  lsof -nP -iTCP:8080 -sTCP:LISTEN
  ```

  Step 1 below therefore runs Postgres on host port **5433** and the server on
  **8081** — that is the combination the reference run used, and it works
  whether or not 5432/8080 are free. If they are free on your machine you can
  use `docker compose up -d db` with `…@127.0.0.1:5432/…` and `PORT=8080`
  instead; every later command goes through `$BASE`, so only step 1 changes.

  A third option is to skip the published port and dial the container's own
  address:
  `DBIP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(docker compose ps -q db))`
  — but under OrbStack that address was **not** routable from the host
  (`dial tcp 192.168.215.2:5432: connect: no route to host`), so the extra
  published port is the reliable choice.

### 1. Start the server with the GTFS fixture

The driver catalog (`/api/v1/gtfs/...`) is served whenever `GTFS_STATIC_URL`
points at a feed — rider mode does not have to be on. Use the repo's test
fixture, which contains route `R1` ("1 Straight") and trip `T1`: a straight
1 km run due north with stops `ST1`/`ST2`/`ST3` at 0 / 500 / 1001 m, scheduled
08:00 / 08:05 / 08:10 America/Los_Angeles.

From the repo root (see [`docs/development.md`](development.md#local-server-run-without-docker-server-container)
for the full "Local Server Run" recipe):

```bash
docker run -d --name vt-smoke-db -p 5433:5432 \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=vehicle_positions postgres:17-alpine

export JWT_SECRET=$(openssl rand -hex 32)   # the server exits below 32 bytes
export PORT=8081
export DATABASE_URL="postgres://postgres:postgres@127.0.0.1:5433/vehicle_positions?sslmode=disable"
export GTFS_STATIC_URL=rider/testdata/fixture.zip
export STALENESS_THRESHOLD=5m
export ADMIN_BOOTSTRAP_EMAIL=admin@test.com
export ADMIN_BOOTSTRAP_PASSWORD=password123

go run . > /tmp/vt-server.log 2>&1 &
sleep 20
export BASE=http://localhost:$PORT
curl -s $BASE/health
# {"status":"ok"}
```

Migrations run at startup. The log should show
`gtfs: loaded schedule ... routes=2 trips=3` and
`bootstrapped initial admin user`.

### 2. The admin user

`ADMIN_BOOTSTRAP_EMAIL` / `ADMIN_BOOTSTRAP_PASSWORD` create the admin on the
first boot of an empty database. Log in and keep the token:

```bash
ADMIN_TOKEN=$(curl -s -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@test.com","password":"password123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
```

(`docker compose exec -T db psql -U postgres -d vehicle_positions <
seed_dev.sql` seeds `admin@test.com` / `password` instead, if you would
rather not use the bootstrap variables.)

### 3. Seed a driver, a vehicle, and the assignment

```bash
USER_ID=$(curl -s -X POST $BASE/api/v1/admin/users \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Test Driver","email":"driver@example.com","password":"driverpass123","role":"driver"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')

curl -s -X POST $BASE/api/v1/admin/vehicles \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"id":"bus-1","label":"Bus 1","agency_tag":"demo-agency"}'

curl -s -X POST $BASE/api/v1/admin/assignments \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"user_id\":$USER_ID,\"vehicle_id\":\"bus-1\"}"
```

Sanity-check as the driver — the vehicle and both routes must come back:

```bash
DRIVER_TOKEN=$(curl -s -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"driver@example.com","password":"driverpass123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

curl -s $BASE/api/v1/vehicles     -H "Authorization: Bearer $DRIVER_TOKEN"
# [{"id":"bus-1","label":"Bus 1",...}]
curl -s $BASE/api/v1/gtfs/routes  -H "Authorization: Bearer $DRIVER_TOKEN"
# {"routes":[{"id":"R2",...,"long_name":"Loop",...},{"id":"R1",...,"long_name":"Straight",...}]}
```

### 4. Build and install the app

```bash
cd ios/VehicleTracker
xcodebuild build -project VehicleTracker.xcodeproj -scheme VehicleTracker \
  -destination "id=$UDID" -derivedDataPath build 2>&1 | tail -3
xcrun simctl install "$UDID" build/Build/Products/Debug-iphonesimulator/VehicleTracker.app
```

Grant location **before** launching, so the When-In-Use prompt never appears:

```bash
xcrun simctl privacy "$UDID" grant location org.onebusaway.vehicletracker
```

Then launch. Either drive the UI by hand (sign in at `$BASE` —
`http://localhost:8081` for the ports above — with `driver@example.com` /
`driverpass123`, pick `bus-1`, route `1 Straight`, run `T1`) —

```bash
xcrun simctl launch "$UDID" org.onebusaway.vehicletracker
```

— or take the **debug launch-argument shortcut**, which signs in and starts the
trip without a single tap. It is compiled into DEBUG builds only
(`App/DebugAutoStart.swift`); a release build ignores the arguments:

```bash
xcrun simctl launch "$UDID" org.onebusaway.vehicletracker \
  -autoServer "$BASE" \
  -autoEmail driver@example.com \
  -autoPassword driverpass123 \
  -autoVehicle bus-1 \
  -autoTrip T1
```

The auto-start only runs from a signed-out or idle session: if a stored trip
makes the app open **Paused**, tap Resume (or end the trip) rather than
expecting the arguments to start a second one.

Plain HTTP is refused except to `localhost`, `127.0.0.1` or a `.local` host
(`Model/ServerURLPolicy.swift`). On a physical iPhone, put the Mac on the same
network and use `https://` or a `.local` hostname.

App-side logging, when something does not add up:

```bash
xcrun simctl spawn "$UDID" log stream --predicate 'subsystem == "org.onebusaway.vehicletracker"'
```

### 5. GPS playback

Drive the fixture's straight route north at 8 m/s — about two minutes from
ST1 to ST3:

```bash
xcrun simctl location "$UDID" start --speed=8 --interval=1 \
  47.6000,-122.3300 47.6090,-122.3300
```

Stop it again with `xcrun simctl location "$UDID" clear`. When the route runs
out of waypoints the simulator stops delivering fixes and the banner correctly
turns red "No GPS".

For a run from Xcode instead, `ios/VehicleTracker/gpx/fixture-t1.gpx` holds the
same three waypoints for **Debug → Simulate Location → Add GPX File to
Project…**. It lives outside `Resources/`, which XcodeGen treats as a resource
folder — a GPX left in there would be copied into the app bundle.

## The checks

Run these in order against a single trip; check 5 ends it.

### Check 1 — Sign in → vehicle → route → run → tracking

1. Launch the app and sign in (or use the auto-start arguments).
2. With exactly one assigned vehicle the picker shows `Bus 1`; choose it, then
   route **1 Straight**, then run **T1**.

**Expected outcome:** the Tracking screen appears with the route badge `1`,
headsign "North" and "Bus 1" in the header; the map shows the R1 shape drawn
in the route colour (`0077C0`) with its three stops; and — before the first
fix — a red **No GPS** banner over "Waiting for GPS…".

### Check 2 — GPS playback drives the screen and the feed

1. Start the simulated route (step 5 above).
2. Watch the Tracking screen, then query the feed:

   ```bash
   curl -s "$BASE/gtfs-rt/vehicle-positions?format=json" | python3 -m json.tool
   ```

**Expected outcome:** the banner turns green **Reporting**; the "n sent"
counter in the footer climbs (it stays visible whatever the banner says); the map follows the vehicle (camera heading north, marker low
on the screen, look-ahead centring) and the **next stop** is drawn larger and
labelled, advancing from `Stop ST2` to `Stop ST3` as the vehicle passes ST2;
and the adherence line shows the deviation against the schedule, with the next
stop's scheduled time and the distance to it.

The fixture's `T1` is scheduled at 08:00 Pacific, so unless you run this
around 08:00 the deviation is large and the line reads
`Off schedule · N min late` (blue) or `… early` (red) — that is correct: the
`Off schedule` cut-off comes from the server's own thresholds (±900 s early,
±5400 s late).

In the feed, exactly one entity, with `vehicle.id` `bus-1`, `trip.trip_id`
`T1`, and a latitude tracking the simulated position:

```json
"entity": [{"id": "bus-1", "vehicle": {
  "trip": {"tripId": "T1"}, "vehicle": {"id": "bus-1"},
  "position": {"latitude": 47.605793, "longitude": -122.33, "bearing": 0, "speed": 8},
  "timestamp": "1789461576"}}]
```

### Check 3 — Losing the server flips the banner red, recovery flips it back

1. Stop the server (`kill %1`, or kill the `go run` and its child binary).
2. Watch the Tracking screen for ~15 s.
3. Start the server again with the same environment and wait ~15 s.

**Expected outcome:** the banner turns red **No connection** within about a
reporting interval, and returns to green **Reporting** once the server is
back. The footer keeps showing the "n sent" counter throughout — the banner
alone carries the status. Fixes captured while offline are dropped, not queued
(v1 behaviour), so the counter resumes climbing from where it left off rather
than catching up.

### Check 4 — Relaunching rehydrates the trip as Paused

1. Terminate the app (`xcrun simctl terminate "$UDID"
   org.onebusaway.vehicletracker`) and launch it again with no arguments.

**Expected outcome:** the app opens straight back on the Tracking screen for
the same trip, with an **orange "Paused — not reporting"** banner (the banner
reads the phase, not just the send status, so a rehydrated trip is never
painted green), **Paused** / "Tap Resume to keep reporting." in the middle, the
map redrawn, and **both** a blue **Resume** button and a red **End Trip**
button beneath it — a paused trip must still be endable. Tapping Resume
restarts location delivery without touching the server, and the banner goes
back to green. (iOS only permits a (re)start of the location session from the
foreground, which is why a relaunch pauses rather than resuming by itself.)

The same pair of buttons appears when the location stream dies mid-trip and
the banner reads "Location stopped — tap Resume".

### Check 4b — An expired sign-in can be put off

Only reachable with a token older than 24 h, so it is usually checked by hand
rather than in a timed run: when `reporting` becomes "Signed out — sign in
again" the re-login sheet appears over the tracking screen. **Later** dismisses
it without re-authenticating — the trip keeps running and the banner keeps
saying signed out — and **tapping the banner** brings the sheet back.

### Check 5 — Ending the trip drops the vehicle from the feed

1. Tap **End Trip** and confirm in the dialog.

**Expected outcome, immediately:** the app returns to the vehicle picker (the
token is still fresh, so no second sign-in), and the stored trip is gone —
a further relaunch goes to the picker, not to Paused.

**Expected outcome, after the staleness window:** the feed serves only points
newer than `STALENESS_THRESHOLD` (5 m above), so the `bus-1` entity keeps
appearing until its last report ages past the threshold. Poll every 20-30 s:

```bash
curl -s "$BASE/gtfs-rt/vehicle-positions?format=json" | python3 -m json.tool
```

`entity` should become an empty list (the key is omitted) about five minutes
after the last report.

## CarPlay

Added by the CarPlay phase.

## Cleanup

```bash
xcrun simctl location "$UDID" clear
kill %1                      # the go run started in step 1
docker rm -f vt-smoke-db     # or `docker compose down`, if you used compose
```
