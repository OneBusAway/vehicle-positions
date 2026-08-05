# Android Driver App — Manual E2E Smoke Test

This is a manual, step-by-step procedure for exercising the Android driver app
against a real local server and a real (or emulated) Android device end to
end. It complements the automated test suites (`make test` for the server,
`./gradlew :app:testDebugUnitTest` for the app) — those check units of
behavior in isolation; this checks that the whole system actually works
together: login, permissions, GPS capture, network loss, task removal, and
trip lifecycle, as seen through both the app UI and the GTFS-RT feed.

Run this before cutting an APK for a pilot deployment, and after any change
that touches auth, the location-tracking service, permissions, or the trip
lifecycle.

All server-side commands below were verified live against the actual server
(Postgres + the Go binary in this repo) while writing this doc.

## Prerequisites

### Tools

- Docker + Docker Compose (to run Postgres + the server)
- `curl`
- `psql` (or `docker compose exec db psql`, used below — no local Postgres
  client install required)
- Android Studio with an emulator image (API 26+; API 35 was used for the
  reference run), **or** a physical Android 8.0+ device on the same network
  as the server
- `adb` on your `PATH`

### 1. Start the server with a real `JWT_SECRET`

The server hard-fails at startup if `JWT_SECRET` is unset or shorter than 32
bytes (`main.go`), and **`docker-compose.yml` does not set one** — so `make
up` alone will start a server that refuses to boot. Supply the secret with an
untracked Compose override (this is what was used to verify every command in
this doc; delete the override file when you're done so it never gets
committed):

```bash
cat > docker-compose.override.yml <<'EOF'
services:
  server:
    environment:
      JWT_SECRET: "local-dev-only-jwt-secret-please-change-32bytes"
EOF

make up
```

Alternative without Docker for the server process (see
[`docs/development.md`](development.md#local-server-run-without-docker-server-container)
for the full "Local Server Run" recipe): run Postgres via `docker compose up
-d db`, then `export JWT_SECRET=<32+ byte secret>` alongside `PORT`,
`DATABASE_URL`, and `STALENESS_THRESHOLD`, and `make run`.

Confirm the server is up:

```bash
curl -s http://localhost:8080/health
# {"status":"ok"}
```

### 2. Create an admin user (no bootstrap path exists)

Neither `seed_dev.sql` nor the migrations create an admin user — only a
driver (see step 3). Every admin endpoint requires an admin-role JWT
(`requireAdmin` in `auth.go`, wired in `main.go`), and account creation
itself is an admin-only endpoint, so the very first admin has to be inserted
directly into the database. Insert one via `psql`, reusing the bcrypt hash
already checked into `seed_dev.sql` (it hashes the password `password`, cost
10, matching the server's `bcrypt.DefaultCost`):

```bash
docker compose exec -T db psql -U postgres -d vehicle_positions -c "
INSERT INTO users (name, email, password_hash, role)
VALUES ('Admin', 'admin@test.com', '\$2a\$10\$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi', 'admin')
ON CONFLICT (email) DO NOTHING;
"
```

Log in to confirm and capture the admin token for the next step:

```bash
ADMIN_TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@test.com","password":"password"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
echo "$ADMIN_TOKEN"
```

### 3. Seed a driver, a vehicle, and the assignment between them

Everything from here on uses the admin API (`user_handlers.go`,
`handlers_vehicles.go`, `assignment_handlers.go`), all mounted under
`/api/v1/admin/...` and requiring the admin bearer token from step 2.

Create the driver (`driver@example.com` / `driverpass123`):

```bash
curl -s -i -X POST http://localhost:8080/api/v1/admin/users \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Test Driver","email":"driver@example.com","password":"driverpass123","role":"driver"}'
# 201 Created — note the returned "id", you'll need it for the assignment below
```

Create the vehicle (`bus-1`):

```bash
curl -s -i -X POST http://localhost:8080/api/v1/admin/vehicles \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":"bus-1","label":"Bus 1","agency_tag":"demo-agency"}'
# 200 OK
```

Assign the vehicle to the driver (replace `6` with the `id` from the
create-driver response above):

```bash
curl -s -i -X POST http://localhost:8080/api/v1/admin/assignments \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"user_id":6,"vehicle_id":"bus-1"}'
# 201 Created
```

Sanity-check as the driver:

```bash
DRIVER_TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"driver@example.com","password":"driverpass123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

curl -s http://localhost:8080/api/v1/vehicles -H "Authorization: Bearer $DRIVER_TOKEN"
# [{"id":"bus-1","label":"Bus 1","agency_tag":"demo-agency","active":true,...}]
```

If you'd rather use the driver seeded by `seed_dev.sql` (`driver@test.com` /
`password`) instead of creating a new one, apply it and skip straight to
creating/assigning the vehicle:

```bash
docker compose exec -T db psql -U postgres -d vehicle_positions < seed_dev.sql
```

### 4. Build and install the app

```bash
cd android
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

The app talks to the server at whatever URL you enter on the login screen.
From an emulator, the host machine's `localhost:8080` is reachable at
**`http://10.0.2.2:8080`**. From a physical device on the same LAN, use the
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

## The 5 checks

Run these in order against a single trip; check 5 ends it.

### Check 1 — Login → vehicle → route → permissions → tracking starts

1. Launch the app, enter the server URL (`http://10.0.2.2:8080` on an
   emulator), and log in as `driver@example.com` / `driverpass123`.
2. With exactly one assigned vehicle, the app should skip straight to Trip
   Setup (auto-select). With more than one, pick `bus-1` from the vehicle
   list.
3. Enter a route ID (e.g. `5`) and tap **Start Trip**.
4. Work through the permission sequence as it appears: fine+coarse location
   (grant precise), background location explanation → OS settings redirect
   (choose "Allow all the time"), notifications (allow), battery-optimization
   exemption (continue or not-now — either is fine).

**Expected outcome:** after the permission sequence completes and device
location services are confirmed on, the app navigates to the Tracking screen
showing a green "Tracking – Connected" status, and a persistent foreground-
service notification appears in the status bar.

### Check 2 — GPS playback appears in the feed

1. With the app on the Tracking screen, feed the emulator a GPS fix (see
   "GPS playback" above).
2. Wait up to ~10 seconds (the location-report interval) and query the feed:

   ```bash
   curl -s 'http://localhost:8080/gtfs-rt/vehicle-positions?format=json'
   ```

**Expected outcome:** the feed's `entity[].vehicle.position.latitude` /
`.longitude` match the fix you injected (within GPS precision), and the
`timestamp` advances on repeated calls as new fixes are sent. The app's
"fixes sent" counter should also be climbing.

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
persisted).

### Check 5 — Ending the trip stops everything and the vehicle drops from the feed

1. Reopen the app (if not already open) and tap **End Trip**.
2. Confirm in the dialog ("End this trip? This will stop location tracking
   and mark the trip as complete.").

**Expected outcome, immediately:**
- The app navigates back to the Login screen.
- The notification is gone: `adb shell cmd notification list | grep
  vehicletracker` returns nothing.
- The service is stopped: `adb shell dumpsys activity services
  LocationTrackingService` returns nothing.

**Expected outcome, after the staleness window:** the server's
`STALENESS_THRESHOLD` (default 5 minutes; `docker-compose.yml` sets it
explicitly) excludes points older than the threshold from the feed. Poll the
feed every 20-30 seconds after the trip's last report:

```bash
curl -s 'http://localhost:8080/gtfs-rt/vehicle-positions?format=json'
```

The vehicle's entity should disappear from `entity[]` once its last report
ages past the threshold (around 5 minutes with the default configuration).

## Cleanup

```bash
docker compose down
rm -f docker-compose.override.yml   # if you created one for JWT_SECRET
```

`make down` also works and is equivalent to `docker compose down` here.
