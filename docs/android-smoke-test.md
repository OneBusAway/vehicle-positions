# Android Driver App — Manual E2E Smoke Test

This is a manual, step-by-step procedure for exercising the Android driver app
against a real local server and a real (or emulated) Android device end to
end. It complements the automated test suites (`make test` for the server,
`./gradlew :app:testDebugUnitTest` for the app) — those check units of
behavior in isolation; this checks that the whole system actually works
together: login, the vehicle/route/run pickers, permissions, GPS capture,
network loss, task removal, an interrupted shift, and trip lifecycle, as seen
through both the app UI
and the GTFS-RT feed.

Run this before cutting an APK for a pilot deployment, and after any change
that touches auth, the pickers, the location-tracking service, permissions, or
the trip lifecycle.

All server-side commands below were verified live against the actual server
(Postgres + the Go binary in this repo) while writing this doc.

## Prerequisites

### Tools

- Docker (for Postgres) and the Go toolchain (the server runs from source,
  so that it can read the repo's GTFS fixture)
- `curl` and `python3`
- Android Studio with an emulator image (API 26+; API 35 was used for the
  reference run), **or** a physical Android 8.0+ device on the same network
  as the server
- `adb` on your `PATH`

### 1. Start the server with a GTFS fixture

The route and run pickers read the driver catalog (`GET /api/v1/gtfs/routes`
and `.../routes/{route_id}/trips`), which the server registers only when
`GTFS_STATIC_URL` points at a feed. Without one there is nothing to pick and
the app says so (Check 6).

The repo's own fixture is the easiest feed to use, but the Docker image does
not carry it — the Dockerfile's final stage copies only the binary — so run the
server from source and keep Postgres in Docker. (`make up` still works for
everything except the catalog, or with `GTFS_STATIC_URL` pointing at a real
GTFS zip URL.)

The obvious ports are often already taken on a development machine, so this
uses **5433** for Postgres and **8081** for the server, as
[`docs/ios-smoke-test.md`](ios-smoke-test.md) does. Check first if you would
rather use the defaults: `lsof -nP -iTCP:5432 -sTCP:LISTEN` and the same for
8080. Everything below goes through `$BASE`, so only this step changes.

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
export BASE=http://localhost:$PORT

go run . > /tmp/vt-server.log 2>&1 &
curl -s $BASE/health
# {"status":"ok"}
```

Migrations run at startup. The log should show `gtfs: loaded schedule ...
routes=2 trips=3` and `bootstrapped initial admin user`.

What the fixture offers the picker, all in `America/Los_Angeles`:

- **`R1` — "1 Straight"**, one run today, `T1`, 08:00 to 08:10.
- **`R2` — "2 Loop"**, one run, `T3`, scheduled `25:00` — GTFS's way of writing
  01:00 the morning after the service date.

So which run carries the **Now** badge depends on when you run this. Outside
those windows no run is badged, which is correct and not a failure; the badge
itself is covered by `HighlightedRunTest` and `ViewModelsTest`.

### 2. The admin user

`ADMIN_BOOTSTRAP_EMAIL` / `ADMIN_BOOTSTRAP_PASSWORD` from step 1 create the
admin on the first boot of an empty database — every admin endpoint requires an
admin-role JWT (`requireAdmin` in `auth.go`, wired in `main.go`), and account
creation is itself admin-only, so that is the bootstrap. Log in and keep the
token:

```bash
ADMIN_TOKEN=$(curl -s -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@test.com","password":"password123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
echo "$ADMIN_TOKEN"
```

(`docker exec -i vt-smoke-db psql -U postgres -d vehicle_positions <
seed_dev.sql` seeds `admin@test.com` / `password` instead, if you would rather
not use the bootstrap variables. See `docs/development.md` for the full admin-UI
setup, including the server-rendered UI at `/admin` itself.)

### 3. Seed a driver, a vehicle, and the assignment between them

Everything from here on uses the admin API (`user_handlers.go`,
`handlers_vehicles.go`, `assignment_handlers.go`), all mounted under
`/api/v1/admin/...` and requiring the admin bearer token from step 2.

Create the driver (`driver@example.com` / `driverpass123`):

```bash
curl -s -i -X POST $BASE/api/v1/admin/users \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Test Driver","email":"driver@example.com","password":"driverpass123","role":"driver"}'
# 201 Created — note the returned "id", you'll need it for the assignment below
```

Create the vehicle (`bus-1`):

```bash
curl -s -i -X POST $BASE/api/v1/admin/vehicles \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":"bus-1","label":"Bus 1","agency_tag":"demo-agency"}'
# 200 OK
```

Assign the vehicle to the driver (replace `6` with the `id` from the
create-driver response above):

```bash
curl -s -i -X POST $BASE/api/v1/admin/assignments \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"user_id":6,"vehicle_id":"bus-1"}'
# 201 Created
```

Sanity-check as the driver:

```bash
DRIVER_TOKEN=$(curl -s -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"driver@example.com","password":"driverpass123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

curl -s $BASE/api/v1/vehicles    -H "Authorization: Bearer $DRIVER_TOKEN"
# [{"id":"bus-1","label":"Bus 1","agency_tag":"demo-agency","active":true,...}]
curl -s $BASE/api/v1/gtfs/routes -H "Authorization: Bearer $DRIVER_TOKEN"
# {"routes":[{"id":"R2",...,"long_name":"Loop",...},{"id":"R1",...,"long_name":"Straight",...}]}
```

Both must come back: the vehicle is what the driver picks first, and the routes
are what the picker offers next. A `404` with a `text/plain` body from the
routes call means `GTFS_STATIC_URL` never reached the server — that is Check 6's
setup, not this one's.

If you'd rather use the driver seeded by `seed_dev.sql` (`driver@test.com` /
`password`) instead of creating a new one, apply it and skip straight to
creating/assigning the vehicle:

```bash
docker exec -i vt-smoke-db psql -U postgres -d vehicle_positions < seed_dev.sql
```

### 4. Build and install the app

```bash
cd android
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

The app talks to the server at whatever URL you enter on the login screen.
From an emulator, the host machine's `localhost:8081` is reachable at
**`http://10.0.2.2:8081`**. From a physical device on the same LAN, use the
host machine's LAN IP instead. Debug builds ship a network-security config
that permits cleartext HTTP to `10.0.2.2` / `localhost` / `127.0.0.1` only
(`android/app/src/debug/`) — login over plain HTTP to any other host, or from
a release build, will be blocked by Android's default cleartext policy.

### 5. GPS playback via the emulator console

To simulate driving, feed the emulator a sequence of GPS fixes:

```bash
adb emu geo fix <longitude> <latitude>
```

**Note the argument order is longitude first, then latitude** — the reverse
of how coordinates are usually spoken/written. Example, walking a route
southwest in small steps:

```bash
adb emu geo fix -122.1050 37.4275
adb emu geo fix -122.1055 37.4272
adb emu geo fix -122.1060 37.4269
```

(Equivalently, use the emulator's Extended Controls → Location panel to load
a route or set points interactively.)

## The checks

Run checks 1-5 in order against a single trip; check 5 ends it. Check 6
restarts the server without a schedule and needs no trip at all. Check 2b needs
a run scheduled around now, so it has its own setup and its own trips: run it on
its own, before or after the rest.

### Check 1 — Login → vehicle → route → run → permissions → tracking starts

1. Launch the app, enter the server URL (`http://10.0.2.2:8081` on an
   emulator), and log in as `driver@example.com` / `driverpass123`.
2. With exactly one assigned vehicle, the app should skip straight past the
   vehicle list (auto-select). With more than one, pick `bus-1`.
3. **Select a Route** lists the catalog: `1 Straight` and `2 Loop`, each with
   its short name on its GTFS colour. Route 2's feed gives no `route_color`, so
   its badge falls back to the app's own colour — that is correct, not a
   missing style. Type `1` in the search box: route 1 stays and route 2 goes
   (the number is matched as a prefix). Type `loop` instead: route 2 comes back
   by long name. Clear the box again and tap **1 Straight**.
4. **Select a Run** lists that route's runs for the agency's service date, one
   row per run: the start time and the headsign over the first and last stop.
   The clock times are the agency's, not the phone's — with the fixture's
   `America/Los_Angeles` schedule, `8:00 AM → North` reads the same on a phone
   set to any timezone. Tap it.
5. Work through the permission sequence as it appears: fine+coarse location
   (grant precise), background location explanation → OS settings redirect
   (choose "Allow all the time"), notifications (allow), battery-optimization
   exemption (continue or not-now — either is fine).

**Expected outcome:** after the permission sequence completes and device
location services are confirmed on, the app navigates to the Tracking screen
showing a green "Tracking – Connected" status, the adherence panel above
`Route R1` — "Waiting for GPS…" until the first fix arrives, then its
judgement of that fix (Check 2b) — and a persistent foreground-service
notification in the status bar.

There is no route-id or trip-id text box anywhere in this flow any more. The
ids now come from the catalog, which is the point of Check 2.

### Check 2 — GPS playback appears in the feed

1. With the app on the Tracking screen, feed the emulator a GPS fix (see
   "GPS playback" above).
2. Wait up to ~10 seconds (the location-report interval) and query the feed:

   ```bash
   curl -s '$BASE/gtfs-rt/vehicle-positions?format=json'
   ```

**Expected outcome:** the feed's `entity[].vehicle.position.latitude` /
`.longitude` match the fix you injected (within GPS precision), and the
`timestamp` advances on repeated calls as new fixes are sent. The app's
"fixes sent" counter should also be climbing.

The vehicle's entity must show **both** ids, and both must be the catalog's:

```json
"trip": {"tripId": "T1", "routeId": "R1", "startDate": "<today, YYYYMMDD>"}
```

`T1` and `R1` are the fixture's own ids. This is the check the picker exists
for: `POST /api/v1/trips/start` never consults the catalog, so before the
picker a driver could type anything here — including a route id in the trip-id
box — and the feed would carry it.

(`startDate` is the *device's* calendar date, which can differ from the service
date the run list showed if the phone and the agency are on different sides of
midnight. That is `ServiceDate.kt`'s existing behaviour for the report and is
not something the picker changes.)

### Check 2b — The adherence panel follows the schedule and the route

The panel under the status banner judges every fix against the run's shape and
schedule on the phone, by the server's own thresholds, and mirrors iOS Check 2.
Against the stock fixture it is only interesting at 08:00 Pacific: at any other
time `T1` is hours off its schedule and the panel correctly reads
`Off schedule · N min late` in blue (or `… early` in red), because the server's
window is 900 s early and 5400 s late.

To see **On time**, serve a copy of the fixture with `T1` moved to start 15
minutes from now. Do this between 03:00 and 23:30 Pacific: the service day rolls
over at 03:00, and the times below do not wrap past midnight.

```bash
mkdir -p /tmp/vt-fixture && (cd /tmp/vt-fixture && unzip -o -q "$OLDPWD/rider/testdata/fixture.zip")
read ST1 ST2 ST3 < <(python3 -c 'from datetime import datetime, timedelta
from zoneinfo import ZoneInfo
t = datetime.now(ZoneInfo("America/Los_Angeles")).replace(second=0) + timedelta(minutes=15)
print(*[(t + timedelta(minutes=m)).strftime("%H:%M:00") for m in (0, 5, 10)])')
sed -i.bak -e "s/^T1,08:00:00,08:00:00,/T1,$ST1,$ST1,/" \
           -e "s/^T1,08:05:00,08:05:00,/T1,$ST2,$ST2,/" \
           -e "s/^T1,08:10:00,08:10:00,/T1,$ST3,$ST3,/" /tmp/vt-fixture/stop_times.txt
(cd /tmp/vt-fixture && rm -f ../vt-fixture-now.zip && zip -q ../vt-fixture-now.zip *.txt)
echo "T1 now runs $ST1 to $ST3 Pacific"
```

Restart the server from step 1 with `GTFS_STATIC_URL=/tmp/vt-fixture-now.zip`,
then start `T1` as in Check 1 (it is badged **Next** until `$ST1`).

1. Park the emulator on the first stop, and wait for a fix to land:

   ```bash
   adb emu geo fix -122.3300 47.6000
   ```

2. At `$ST1`, drive the route at its scheduled pace — ST1 to ST3 in ten
   minutes, a fix every 2 s — with 50 s spent 150 m east of the line as a
   deliberate detour:

   ```bash
   for i in $(seq 0 300); do
     lat=$(awk -v i=$i 'BEGIN { printf "%.6f", 47.6 + 0.009 * i / 300 }')
     lon=-122.3300; [ $i -ge 60 ] && [ $i -lt 85 ] && lon=-122.3280
     adb emu geo fix $lon $lat
     sleep 2
   done
   ```

   `ios/VehicleTracker/gpx/fixture-t1.gpx` holds the same three stops, if you
   would rather load them in the emulator's Extended Controls → Location.

3. While the loop runs (it has passed ST2 by then), force-stop the app, stop the
   server, and relaunch the app:

   ```bash
   adb shell am force-stop org.onebusaway.vehicletracker
   kill $(lsof -nP -iTCP:$PORT -sTCP:LISTEN -t)
   adb shell am start -n org.onebusaway.vehicletracker/.MainActivity
   ```

   Then start the server again as in step 1.

4. With the loop still running, delete the stored geometry to stand in for a
   trip started by an older build, and relaunch:

   ```bash
   adb shell run-as org.onebusaway.vehicletracker rm files/active_trip_geometry.json
   adb shell am force-stop org.onebusaway.vehicletracker
   adb shell am start -n org.onebusaway.vehicletracker/.MainActivity
   ```

5. End the trip, start `T1` again, and end that one too, listing the app's
   files while the second trip runs and after it ends:

   ```bash
   adb shell run-as org.onebusaway.vehicletracker ls files
   ```

**Expected outcome:**

- **Parked at ST1 before `$ST1`:** the panel reads `N min early` in red — or
  `Off schedule · 15 min early`, if the fix lands more than 900 s ahead, past
  the server's window. Under it, `Next: Stop ST2` with ST2's time on the
  agency's clock (`$ST2`, whatever zone the phone is set to) and the distance
  to it.
- **On the route:** `On time` in green. The distance to ST2 counts down every
  location update (about 10 s).
- **The detour:** within a location update, `Off route · 150 m from the route`
  in grey. It flips back to `On time` once the fixes are back on the line.
- **Past ST2:** the next stop becomes `Stop ST3` with `$ST3`.
- **Relaunched with the server down:** the app reopens on the Tracking screen
  with a red "No connection" banner. The panel still judges every fix, and the
  distance to ST3 keeps counting down — the shape and schedule are on the phone,
  not refetched. Once the server is back the banner turns green again. (The
  "Location updates sent" counter starts again from 0 with the new process.)
- **Relaunched without the stored geometry:** the panel reads `Schedule
  unavailable`, and the "Location updates sent" counter keeps climbing:
  reporting does not depend on it.
- **Ending a trip:** `active_trip_geometry.json` is listed while the second
  trip runs, and gone once it has ended.

### Check 3 — Network loss flips the status red, recovery flips it back green

1. Disable networking on the device/emulator (physical airplane mode, or on
   an emulator: `adb shell svc wifi disable && adb shell svc data disable`
   — real `AIRPLANE_MODE` broadcasts are blocked by emulator shell
   permissions, but this achieves the same `ConnectivityManager` callback).
2. Watch the Tracking screen.
3. Re-enable networking (`adb shell svc wifi enable && adb shell svc data
   enable`, or toggle airplane mode off on a physical device).

**Expected outcome:** within ~10 seconds of the network dropping, the status
banner flips to red "No connection". Within ~10 seconds of the network
returning, it flips back to green "Tracking – Connected". GPS fixes captured
while offline are dropped, not queued (v1 behavior) — the counter should
resume climbing from wherever it left off, not "catch up".

### Check 4 — Swiping the app away doesn't stop tracking

1. Remove the app from Recents (swipe away). On some emulator builds the
   fling gesture is unreliable; an equivalent is:

   ```bash
   adb shell dumpsys activity activities | grep -i taskId   # find the app's task id
   adb shell am stack remove <taskId>
   ```

2. Confirm the service and notification are still present:

   ```bash
   adb shell dumpsys activity services LocationTrackingService   # should show the service, not empty
   adb shell cmd notification list | grep vehicletracker         # should show one entry
   ```

3. Query the feed twice, ~10-15 seconds apart, and confirm the timestamp
   advances between calls.

**Expected outcome:** the foreground service and its notification survive
task removal (this is the point of running as a foreground service), and
location fixes keep arriving at the server the whole time. Relaunching the
app should rehydrate directly to the Tracking screen (active trip state is
persisted), with **no** Resume Shift prompt: the service never stopped, so there
is nothing to resume. A prompt here is a bug — it would interrupt every healthy
shift whose driver reopens the app.

### Check 4b — A force-stopped trip asks before resuming

Unlike swiping the app away, a force-stop kills the service too, and Android
does not restart a force-stopped app's services. The trip is still stored on
the phone and still open on the server, but nothing is reporting it, so the app
asks the driver instead of carrying on as if it were.

1. Force-stop the app and confirm the service is gone:

   ```bash
   adb shell am force-stop org.onebusaway.vehicletracker
   adb shell dumpsys activity services LocationTrackingService   # should be empty
   ```

2. Relaunch the app from the launcher.

**Expected outcome:** a spinner for about two seconds — the app gives a service
that the system is restarting that long to announce itself — then **Resume
Shift?**: "An incomplete shift with Vehicle bus-1 was detected. The app was
closed while tracking was active.", the route, how long ago the trip started,
and a blue **Resume** and a red **End Shift** button. The service stays stopped
while the prompt is up.

3. Tap **Resume**.

**Expected outcome:** the Tracking screen, with the trip duration still counted
from the original start and the adherence panel back. Location reports reach the
server again, and there is **no** second `POST /api/v1/trips/start`: the trip
was never ended on the server, so resuming does not start it again (a second
start would be refused with a 409). Only the start from Check 1 is listed:

```bash
grep -o '"path":"/api/v1/trips/[a-z]*"' /tmp/vt-server.log
# "path":"/api/v1/trips/start"
```

**End Shift** on the same prompt is the other way out. It takes the same path as
**End Trip** in Check 5, so Check 5 covers it: to run it from here instead,
force-stop and relaunch again and tap **End Shift**.

### Check 5 — Ending the trip stops everything and the vehicle drops from the feed

1. Reopen the app (if not already open) and tap **End Trip**.
2. Confirm in the dialog ("End this trip? This will stop location tracking
   and mark the trip as complete.").

**Expected outcome, immediately** (the same from **End Shift** on the resume
prompt):
- `POST /api/v1/trips/end` in the server log, answered `200`.
- The app navigates back to the start of the picker (the session token is
  still fresh, so there's no need to log in again) — the route list, since a
  driver with one assigned vehicle skips the vehicle list.
- The notification is gone: `adb shell cmd notification list | grep
  vehicletracker` returns nothing.
- The service is stopped: `adb shell dumpsys activity services
  LocationTrackingService` returns nothing.
- The trip's geometry is gone: `adb shell run-as
  org.onebusaway.vehicletracker ls files` no longer lists
  `active_trip_geometry.json`.

**Expected outcome, after the staleness window:** the server's
`STALENESS_THRESHOLD` (default 5 minutes; `docker-compose.yml` sets it
explicitly) excludes points older than the threshold from the feed. Poll the
feed every 20-30 seconds after the trip's last report:

```bash
curl -s '$BASE/gtfs-rt/vehicle-positions?format=json'
```

The vehicle's entity should disappear from `entity[]` once its last report
ages past the threshold (around 5 minutes with the default configuration).

### Check 6 — A server with no schedule offers no routes

The catalog endpoints are registered only when `GTFS_STATIC_URL` is set, so a
server without one answers the routes call with `net/http`'s own 404 — a
`text/plain` "404 page not found", not JSON. The app has to read that as "there
is no schedule here" rather than as a failed request.

1. Stop the server from step 1 and start it again with everything the same
   **except** the fixture:

   ```bash
   kill $(lsof -nP -iTCP:$PORT -sTCP:LISTEN -t)
   unset GTFS_STATIC_URL
   go run . > /tmp/vt-server-noschedule.log 2>&1 &
   curl -s -i $BASE/api/v1/gtfs/routes -H "Authorization: Bearer $DRIVER_TOKEN" | head -2
   # HTTP/1.1 404 Not Found
   # Content-Type: text/plain; charset=utf-8
   ```

2. Force-stop and relaunch the app, and let it reach the route picker.

**Expected outcome:** **Select a Route** shows "This server has no schedule
loaded, so there are no routes to start a trip on." — no route rows, no search
box, and **no Retry button**, because retrying cannot conjure a schedule. There
is no manual route/trip entry to fall back to: a trip needs a real GTFS trip id,
as on iOS.

A genuine failure looks different on purpose. Kill the server outright and
relaunch the app: the same screen reads "Could not load the routes." *with* a
Retry button, because that one is worth retrying.

## Cleanup

```bash
kill $(lsof -nP -iTCP:$PORT -sTCP:LISTEN -t)
docker rm -f vt-smoke-db
```

The database lives only inside that container, so removing it takes the smoke
test's users, vehicles and trips with it.
