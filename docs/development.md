# Development Guide

This guide is for contributors who want to run, verify, and iterate on the current Go server in this repository.

## Scope

The current implementation focuses on:

- ingesting location reports (`POST /api/v1/locations`)
- serving GTFS-RT vehicle positions (`GET /gtfs-rt/vehicle-positions`)
- exposing basic server status (`GET /api/v1/admin/status`)

## Prerequisites

- Go (matching `go.mod` toolchain)
- Docker + Docker Compose
- `curl`

## Quick Start (Docker)

From the repository root:

1. Start the stack. Compose refuses to start without a `JWT_SECRET` of 32+
   bytes, so export one first (or put it in a `.env` file next to
   `docker-compose.yml`):

   ```bash
   export JWT_SECRET=$(openssl rand -hex 32)
   make up
   ```

2. Verify server health:

   ```bash
   curl http://localhost:8080/health
   ```

3. Run a smoke test (posts one location, then fetches status + feed JSON):

   ```bash
   make smoke
   ```

4. Stop the stack when done:

   ```bash
   make down
   ```

## Local Server Run (without Docker server container)

You can run Postgres in Docker and run the Go server directly:

1. Start only database:

   ```bash
   docker compose up -d db
   ```

2. Export environment variables:

   ```bash
   export PORT=8080
   export DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable'
   export JWT_SECRET=$(openssl rand -hex 32)   # required; the server exits without 32+ bytes
   export STALENESS_THRESHOLD=5m
   ```

   Location retention is optional and off unless you set it:

   ```bash
   export LOCATION_RETENTION_PERIOD=720h   # keep 30 days; 0 or unset keeps forever
   export LOCATION_PRUNE_INTERVAL=1h       # how often the pruner sweeps
   export LOCATION_PRUNE_BATCH_SIZE=10000  # rows deleted per statement
   ```

3. Run server:

   ```bash
   make run
   ```

Migrations are applied automatically on server startup.

## Admin Web UI

The server also serves a session-authenticated admin UI at `/admin`
(dashboard, live map + trails, vehicle/user CRUD, assignments, trip history).
It's on by default; set `ADMIN_UI_ENABLED=false` if you want to run the
server with just the JSON API.

To sign in locally, seed the dev admin (`admin@test.com` / `password`) the
same way you'd seed the dev driver:

```bash
docker compose exec -T db psql -U postgres -d vehicle_positions < seed_dev.sql
```

Then visit `http://localhost:8080/admin/login`. For a from-scratch admin
instead of the seed one, set `ADMIN_BOOTSTRAP_EMAIL` /
`ADMIN_BOOTSTRAP_PASSWORD` before the server's first boot — it only creates
an admin when none exist yet, so it's safe to leave set across restarts.

Deactivating a user blocks new logins immediately, but existing sessions and
tokens for that user remain valid until they expire (up to 24 hours) — this
isn't instant revocation.

If you're changing anything under `web/templates` or `web/styles/input.css`,
rebuild the compiled Tailwind CSS before checking your changes in the
browser:

```bash
make css
```

This compiles `web/styles/input.css` to `web/static/css/admin.css` (which is
what the server actually embeds and serves — the browser never sees
`input.css`) using a pinned Tailwind CLI binary (currently `v4.2.0`, see
`TAILWIND_VERSION` in the `Makefile`) that `make css` downloads to `.tools/`
on first use. CI checks in `web/static/css/admin.css` against the same
pinned version, so if you bump `TAILWIND_VERSION` in the `Makefile`, also
bump the version CI downloads in `.github/workflows/ci.yml` and re-run `make
css` to regenerate the checked-in output.

Running behind a reverse proxy locally (rare, but if you're testing that
path)? Set `TRUST_PROXY_HEADERS=true` so client-IP-based rate limiting and
the session cookie's `Secure` flag look at `X-Forwarded-For` /
`X-Forwarded-Proto` instead of the raw connection.

## Running Tests

Run all tests:

```bash
make test
```

Notes:

- most tests are unit tests and run without external services
- DB integration tests in `store_test.go` require `DATABASE_URL` and are skipped when it is not set

## Simulating Vehicle Traffic

Use the built-in simulator to generate multiple moving vehicles:

```bash
make simulate
```

Custom example:

```bash
go run ./cmd/simulator -url http://localhost:8080 -vehicles 20 -interval 2s -duration 2m
```

## Watching Retention Prune Locally

Retention deletes location points older than `LOCATION_RETENTION_PERIOD`, measured from
`received_at` (when the server stored the point), not the device-reported `timestamp`.
The first sweep runs one full `LOCATION_PRUNE_INTERVAL` after startup.

To watch it work without waiting hours, run the server with a very short retention:

```bash
export LOCATION_RETENTION_PERIOD=2m
export LOCATION_PRUNE_INTERVAL=30s
make run
```

Then generate some points and wait for the sweep:

```bash
make simulate
```

The server logs `location retention enabled` at startup, and each sweep that removes
anything logs `pruned expired location points` with the row count and cutoff. Confirm
against the database:

```bash
docker compose exec db psql -U postgres -d vehicle_positions \
  -c "SELECT count(*), min(received_at) FROM location_points;"
```

Deletion is permanent, so use a scratch database for this rather than one holding data
you care about.

## Rider Mode Smoke Test

Rider mode is off by default. Exercising it end to end means starting the
server with `RIDER_MODE_ENABLED=true` and a `GTFS_STATIC_URL`, driving riders
along a trip with the `cmd/ridersim` simulator, and reading the resulting
entities back out of `/gtfs-rt/vehicle-positions?source=rider`. See
[`README.md`](../README.md#rider-mode-crowdsourced-positions) for the full
configuration reference.

The run below uses the committed test feed, `rider/testdata/fixture.zip`,
whose trip `T1` is a 1001 m straight line scheduled 08:00-08:10 on weekdays.
Two settings make it runnable at any hour:

- `RIDER_SCHEDULE_EARLY=24h RIDER_SCHEDULE_LATE=24h` widens the
  schedule-adherence window so `T1` matches whatever the clock says. The
  simulator itself never fakes time — it walks the shape in real time, and
  `-speed` is what controls how fast it gets through it.
- `STALENESS_THRESHOLD=30s` makes the "agency" vehicle
  disappear 30 s after its last report instead of five minutes later, so you
  do not have to wait to see the rider take over.

### 1. Start the server

The server's own driver half is always a trusted source — a trip a driver is
reporting through this server suppresses and corroborates riders on it without
any `TRUSTED_GTFS_RT_URLS` — so the engine has something authoritative to check
riders against as soon as a driver reports. `TRUSTED_FEED_MAX_AGE` is what
bounds an external feed; the local driver half is bounded by
`STALENESS_THRESHOLD`.

The credentials below are generated per run rather than written down, because
the server listens on every interface: it has no loopback-only mode, so anyone
who can reach port 18080 can reach this admin account. Run the smoke test on a
machine that is not on an untrusted network. Step 2 needs the generated
password, so echo it once and copy it into that terminal.

```bash
docker compose up -d db
export PORT=18080
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable'
export JWT_SECRET=$(openssl rand -hex 32)
export ADMIN_BOOTSTRAP_EMAIL=admin@test.com ADMIN_BOOTSTRAP_PASSWORD=$(openssl rand -hex 16)
export RIDER_MODE_ENABLED=true GTFS_STATIC_URL=rider/testdata/fixture.zip
export RIDER_SCHEDULE_EARLY=24h RIDER_SCHEDULE_LATE=24h
export STALENESS_THRESHOLD=30s
go run .
```

### 2. Drive a trusted vehicle down T1

In a second terminal. `/api/v1/locations` needs a bearer token; the
bootstrapped admin's works. Set `ADMIN_PASSWORD` to the value the first
terminal generated (`echo $ADMIN_BOOTSTRAP_PASSWORD` there).

```bash
export ADMIN_PASSWORD=...   # from the first terminal
TOKEN=$(curl -s -X POST localhost:18080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg p "$ADMIN_PASSWORD" '{email:"admin@test.com",password:$p}')" | jq -r .token)

# bus-1 sits 300 m along T1's shape and reports every 3 s for a minute
( for i in $(seq 1 20); do
    curl -s -X POST localhost:18080/api/v1/locations \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d "{\"vehicle_id\":\"bus-1\",\"trip_id\":\"T1\",\"latitude\":47.6027,\"longitude\":-122.33,\"timestamp\":$(date +%s)}" >/dev/null
    sleep 3
  done ) &
```

### 3. Run a rider along the same trip

```bash
go run ./cmd/ridersim -url http://localhost:18080 \
  -gtfs rider/testdata/fixture.zip -trip T1 \
  -interval 1s -speed 10 -expect-end arrived
```

It registers a fresh anonymous rider, opens a ride, and walks the shape at
10 m/s, so the 1001 m trip takes about 100 s. Expect the state to go
`pending` -> `verified` within about 5 s, and `published` to flip to `true`
around 15 s in (a new rider needs twelve corroborated points before the server
will publish it):

```
T1/9d1b2e6f: ride started state=pending report=5s batch=12 shape=1001m
T1/9d1b2e6f: state=verified corroboration=corroborated published=false accepted=5 ...
T1/9d1b2e6f: state=verified corroboration=corroborated published=true  accepted=5 ...
T1/9d1b2e6f: ended arrived after 1001m along the shape (101 points sent, 0 dropped)
all 1 rides ended as expected
```

The process exits 1 if any ride ends with a reason other than `-expect-end`,
and 2 if the invocation itself is wrong (an unknown `-expect-end` reason, say).

Corroboration flaps while the driver is parked — you will see it move between
`corroborated`, `none` and `unavailable` as the rider walks past `bus-1` and
the odd jittered fix fails to match. That is expected; what matters is that
`published` stays `true` once it flips, since a ride stays corroborated for
good after twelve corroborated points.

### 4. Watch the rider take over from the agency feed

While `bus-1` is reporting, the rider is verified but suppressed — the agency
already covers `T1`:

```bash
curl -s 'localhost:18080/gtfs-rt/vehicle-positions?format=json&source=rider' | jq '.entity | length'   # 0
curl -s 'localhost:18080/gtfs-rt/vehicle-positions?format=json&source=driver' | jq '.entity | length'  # 1
```

About 30 s after the driver loop ends, `bus-1` goes stale, the driver half no
longer covers `T1`, and the rider's estimate appears instead:

```bash
curl -s 'localhost:18080/gtfs-rt/vehicle-positions?format=json&source=rider' | jq '.entity[0].vehicle.vehicle'
# {"id":"rider:T1:20260902","label":"Rider-reported"}
```

### 5. Check that an off-route rider is thrown out

`-offroute-after` steps 300 m east of the shape after the given delay. Five
consecutive non-matching points reject the ride, so this ends in about 10 s:

```bash
go run ./cmd/ridersim -url http://localhost:18080 \
  -gtfs rider/testdata/fixture.zip -trip T1 \
  -interval 1s -speed 10 -offroute-after 5s -expect-end off_route
# T1/c75ec0df: state=rejected ... off_route_streak=5
# T1/c75ec0df: server ended the ride: off_route
```

### 6. Check the admin view

```bash
curl -s localhost:18080/api/v1/admin/rider/status -H "Authorization: Bearer $TOKEN" | jq .
```

`gtfs` should show the loaded feed, and `trusted_feeds[0].last_error` should
be empty.

### Simulator flags

`make ridersim` runs the default one-rider case against `localhost:$(PORT)`
(8080 unless `PORT` is set, matching `make run`). The full set:

| Flag | Default | Meaning |
|---|---|---|
| `-url` | `http://localhost:8080` | Server base URL |
| `-gtfs` | `rider/testdata/fixture.zip` | GTFS static zip path or URL |
| `-trip` | — | Trip id to simulate; repeat for more than one |
| `-random` | `0` | Extra trips picked at random from those running that day |
| `-start-date` | today | Service date, `YYYYMMDD`, in the feed's timezone |
| `-interval` | `5s` | Time between simulated GPS fixes |
| `-speed` | `10` | Metres per second along the shape |
| `-noise` | `8` | Metres of Gaussian jitter added to each fix, and the accuracy each fix reports (floor 5 m) |
| `-offroute-after` | `0` | Leave the shape by 300 m after this long |
| `-riders-per-trip` | `1` | Riders on each trip |
| `-duration` | `0` | Stop each ride after this long (0 = when the shape ends) |
| `-expect-end` | — | Require every ride to end with this reason |

Two server limits bound how hard you can push it. Registration is capped at
five riders per minute per IP: the simulator registers riders one at a time,
250 ms apart, and a rider that is refused waits 12 s and tries again (ten
attempts), so a large `-random`/`-riders-per-trip` run ramps up over a couple
of minutes rather than failing. Each ride may also report only once every two
seconds, which is why `report_interval_seconds` from the server, not
`-interval`, decides how often a batch goes out.

Timestamps go over the wire as whole seconds and the server ignores a fix that
is not newer than the last one, so `-interval` below `1s` samples the shape
more finely but still uploads at most one fix per second.

## API Sanity Checks

### Submit one location

```bash
curl -X POST http://localhost:8080/api/v1/locations \
  -H 'Content-Type: application/json' \
  -d '{
    "vehicle_id": "demo-vehicle-42",
    "trip_id": "route-5-0830",
    "route_id": "5",
    "latitude": -1.2921,
    "longitude": 36.8219,
    "bearing": 180,
    "speed": 8.5,
    "accuracy": 12,
    "timestamp": '"$(date +%s)"'
  }'
```

### Get feed (JSON debug format)

```bash
curl 'http://localhost:8080/gtfs-rt/vehicle-positions?format=json'
```

### Get admin status

```bash
curl http://localhost:8080/api/v1/admin/status
```

## Troubleshooting

- `connection refused` when posting locations:
  - confirm server is running on `localhost:8080`
- DB connection/migration errors:
  - check `DATABASE_URL`
  - verify Postgres container is healthy (`docker compose ps`)
- `address already in use` for `0.0.0.0:5432` when running `make up`:
   - another local Postgres is using port `5432`
   - stop that service, or update [docker-compose.yml](docker-compose.yml) to map a different host port and adjust `DATABASE_URL` accordingly
- empty feed:
   - make sure timestamp is within 5 minutes of server time (this is request validation in `handlers.go`, independent of `STALENESS_THRESHOLD`)
  - ensure coordinates are valid and non-zero
