# Configuration reference

Every environment variable the server reads, what it defaults to, and what
happens when it is set wrong. This is the complete list, and two tests check it
against the source: `TestConfigDoc_AllVariablesDocumented` fails the build if a
variable is added to the code without a row here, and
`TestConfigDoc_NoStaleVariables` fails it if a row here names a variable
nothing reads any more.

## How values are read

The server is configured entirely through the process environment. There is no
configuration file and no command-line flags.

**Everything is read once, at startup.** Changing a variable requires a restart;
nothing here is re-read per request.

**An empty value means unset.** `PORT=""` is the same as not setting `PORT` at
all — the default applies.

**Durations** use Go's [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration)
syntax: `30s`, `5m`, `24h`, `2160h`. There is no day or week unit; 90 days is
`2160h`.

**Booleans** use Go's [`strconv.ParseBool`](https://pkg.go.dev/strconv#ParseBool),
which accepts `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`,
`false`, `False` — **and nothing else**. `yes`, `no`, `on` and `off` are *not*
booleans here: they fail to parse, and a value that fails to parse falls back to
the default. `ADMIN_UI_ENABLED=no` therefore leaves the admin UI **on**. Use
`false`.

A bad value fails in one of three ways, noted per variable below:

| Failure mode | What happens |
|---|---|
| Warn and default | The value is ignored, the default is used, and a `WARN` line naming the key and the value is logged. Most variables behave this way. |
| Refuse to start | The server logs an `ERROR` and exits with status 1. |
| Panic | The process crashes with a stack trace. Only `STALENESS_THRESHOLD` does this. |

### Under Docker Compose

[`docker-compose.yml`](../docker-compose.yml) passes only the variables listed
in its `environment:` block into the container — today `PORT`, `DATABASE_URL`,
`STALENESS_THRESHOLD`, `JWT_SECRET`, `GTFS_STATIC_URL` and `RIDER_MODE_ENABLED`.
Exporting any other variable in your shell has **no effect** on a Compose-run
server; add it to that block (or an `env_file`) instead. Compose itself refuses
every command without `JWT_SECRET` and `GTFS_STATIC_URL` — even ones that only
touch the database, like `docker compose up -d db` — so keep both in a `.env`
file next to `docker-compose.yml`. The 32-byte minimum is the server's own
check. The image carries no GTFS file, so under Compose `GTFS_STATIC_URL` must
be a URL the container can download.

## Core server

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `PORT` | `8080` | Optional | TCP port to listen on. The server binds `:$PORT`. |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable` | Recommended in production | PostgreSQL connection string, used for both the connection pool and the migrations run at startup. A database that cannot be reached, or a migration that fails, **refuses to start**. |
| `JWT_SECRET` | — | **Required** | HMAC-SHA256 signing key for driver, admin and rider tokens. The server **refuses to start** when it is unset or shorter than 32 bytes. |
| `ACCESS_TOKEN_TTL` | `15m` | Optional | How long an API access token stays valid. Clients renew with `POST /api/v1/auth/refresh` before it elapses; the value is returned as `expires_in` on login and refresh. Both driver apps refresh, and so does the simulator. App builds from before [#205](https://github.com/OneBusAway/vehicle-positions/pull/205) (Android) and [#210](https://github.com/OneBusAway/vehicle-positions/pull/210) (iOS) cannot: they stop reporting 15 minutes after the driver signs in, until the driver signs in again. Update the apps first, or set it to `24h` until they are. Unparseable: warn and default. **A non-positive value refuses to start** — it would issue tokens that are already expired. Does not affect the admin UI's browser session, which is fixed at 24 hours. |
| `REFRESH_TOKEN_TTL` | `168h` | Optional | How long a refresh token stays valid (7 days). Each refresh consumes the token presented and issues a replacement with a fresh window. Unparseable: warn and default. **A value not longer than `ACCESS_TOKEN_TTL` refuses to start** — a refresh token that expires first has nothing to renew. |
| `REFRESH_TOKEN_PRUNE_INTERVAL` | `1h` | Optional | How often expired rows are deleted from `refresh_tokens`; the first pass runs one interval after startup. Unlike `LOCATION_RETENTION_PERIOD` this is on by default: a token past its `expires_at` is already rejected by `POST /api/v1/auth/refresh`, so deleting it removes nothing a client or operator can use. Consumed tokens are kept until they expire, so a replayed one is still logged as reuse. There is no separate retention setting — `REFRESH_TOKEN_TTL` already decides when each row expires. Unparseable: warn and default. A value that parses to zero or a negative duration logs an `ERROR` and leaves pruning **off**; the server still starts. |
| `STALENESS_THRESHOLD` | `5m` | Optional | How long a vehicle stays in the GTFS-RT feed after its last report. Also the window used to seed the tracker from the database at startup, and what the admin UI calls a stale vehicle. Unparseable: warn and default. **A value that parses to zero or a negative duration panics** — the tracker rejects a non-positive window rather than dropping every vehicle. |
| `READ_TIMEOUT` | `15s` | Optional | `http.Server.ReadTimeout` — the deadline for reading an entire request, headers and body. Warn and default. |
| `WRITE_TIMEOUT` | `15s` | Optional | `http.Server.WriteTimeout` — the deadline for writing the response. Warn and default. |
| `IDLE_TIMEOUT` | `60s` | Optional | `http.Server.IdleTimeout` — how long a keep-alive connection may sit idle. Warn and default. |

`STALENESS_THRESHOLD` is not the same as the ±5 minute skew check applied to the
`timestamp` in a location report; that one is request validation and is not
configurable.

## GTFS schedule

Required. Drivers pick their route and run from the schedule, through the
driver catalog (`/api/v1/gtfs/...`), so the server does not start without one.
Rider mode, when on, verifies riders against the same schedule.

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `GTFS_STATIC_URL` | — | **Required** | GTFS static zip — an `http(s)://` URL or a local file path. The server **refuses to start** when it is unset, empty or only whitespace; that check runs before the database is opened, so it fails immediately. A feed that is set but cannot be downloaded or parsed at startup also **refuses to start**, after migrations have run. |
| `GTFS_STATIC_REFRESH` | `24h` | Optional | How often to re-download the zip and rebuild the index. A failed refresh keeps the previous index and logs. Must be positive; zero, negative or unparseable warns and defaults. |

## Driver app map

Optional. The PMTiles file the driver app downloads for its offline street
map, hosted wherever the agency likes; the server never downloads it.
[Deployment §12.6](deployment.md#126-offline-street-map) covers making and
hosting one. Unset, the app draws the route on a plain background.

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `MAP_PMTILES_URL` | unset | Optional | `http` or `https` URL of the PMTiles map file, handed as is to drivers at `GET /api/v1/map`. When it is unset, empty or only whitespace, `pmtiles_url` is `null`. A value that is not an `http` or `https` URL with a host, or that carries a username or password (the URL goes to every driver's phone), **refuses to start**; the check runs before the database is opened. Plain `http` starts with a `WARN`: Android blocks cleartext downloads in release builds, so use `https` in production. |

## Admin UI and first-admin bootstrap

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `ADMIN_UI_ENABLED` | `true` | Optional | Serve the admin web UI at `/admin`. Set to `false` to unregister those routes entirely — they then return 404. Unparseable: warn and default, so the UI stays on. |
| `ADMIN_BOOTSTRAP_EMAIL` | — | Recommended on a new deployment | Email address for the initial admin account. |
| `ADMIN_BOOTSTRAP_PASSWORD` | — | Recommended on a new deployment | Password for that account; minimum 8 characters. A shorter one **refuses to start**. |

Bootstrap runs only when **both** variables are set and non-empty, and only when
the `users` table holds zero admins — it is a no-op on every later boot, and
changing the values afterwards does not change an account that already exists.
Once an admin exists, both variables can be removed.

## Dispatcher alerts

The thresholds behind the admin UI's **Alerts** page and
`GET /api/v1/admin/alerts`. All apply only to vehicles on an active trip.

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `ALERT_QUIET_AFTER` | `3m` | Optional | How long a vehicle may go without a report before it shows as gone quiet. The default sits below `STALENESS_THRESHOLD`, so the alert comes before the vehicle drops out of the feed and off the live map. Must be positive; zero, negative or unparseable warns and defaults. |
| `ALERT_STOPPED_AFTER` | `10m` | Optional | How long a vehicle that is still reporting may stay within 50 m of where it is before it shows as not moving. Must be positive; zero, negative or unparseable warns and defaults. |
| `ALERT_OFF_ROUTE_DISTANCE` | `RIDER_MAX_SHAPE_DISTANCE` (`60`) | Optional | Metres from the trip's shape, beyond the fix's reported accuracy, at which a report counts as off route. The default is the distance at which the driver apps already show the driver as off route. A fix whose accuracy reaches across this distance counts neither way, so one poor fix neither raises the alert nor clears it. Trips with no shape in the GTFS feed, or a shape drawn through their stops, are never judged. Must be positive; zero, negative, `NaN`, `Inf` or unparseable warns and defaults. |
| `ALERT_OFF_ROUTE_AFTER` | `5m` | Optional | How long a vehicle's reports may stay off route, counted to its latest report, before it shows as off route. Going round a blocked street takes a minute or two. Reports count only once the vehicle has been near its route on that trip, so a trip started at a depot or a layover spot away from the route is not off route on the way there. Must be positive; zero, negative or unparseable warns and defaults. |
| `ALERT_NOT_STARTED_AFTER` | `5m` | Optional | How long after it was due to leave a block's next trip may go unstarted before the vehicle that ran the block's latest trip shows as not started. The default matches `REPORT_ON_TIME_LATE`: a trip that leaves later than that is late. Only a block a vehicle has run a trip on today counts, and only in a feed whose `trips.txt` gives a `block_id`; headway trips never do. Must be positive; zero, negative or unparseable warns and defaults. |

The server keeps what the alerts need in memory, from the reports it accepts.
After a restart it counts a quiet vehicle from the restart, and a vehicle that
is not moving from its first report since. A vehicle that was off route when the
server restarted shows as off route again only after it has come back near its
route and left it. The not-started alert reads the trips started in the last
day from the database instead, so a restart does not reset it.

### Alert webhook

Posts each alert when it is raised and again when it clears, to a chat service
or an endpoint of your own, so a dispatcher can follow the alerts on a phone
instead of keeping the **Alerts** page open.

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `ALERT_WEBHOOK_URL` | unset | Optional | Where to post: a Slack or Google Chat incoming webhook URL, a Microsoft Teams Workflows webhook URL, or your own endpoint. Unset, empty or only whitespace leaves the webhook off. Anything but an `http` or `https` URL with a host **refuses to start**. A chat service's webhook URL works as its password, so the server logs only its host. |
| `ALERT_WEBHOOK_SECRET` | unset | Optional | Signs each post, for an endpoint of your own to check (see below). Unset sends posts unsigned. Never logged. |
| `PUBLIC_URL` | unset | Optional | The address dispatchers open the admin site at, such as `https://tracker.example.org`. With it set, each alert post links to the trip's page, `/admin/trips/{id}` under this address. Anything but an `http` or `https` URL with a host and no username, password, query or fragment **refuses to start**, whether or not the webhook is on. |

The server works the alerts out every 30 seconds, as often as the **Alerts**
page reloads, and posts one JSON body for each alert raised and each cleared
since the last time. An alert that stays is not posted again, and one that
comes and goes between two checks is not posted at all. Each body is the one
alert:

```json
{"event":"raised","kind":"quiet","vehicle_id":"bus-7","vehicle_label":"Bus 7","route_id":"R1","route_short_name":"1","gtfs_trip_id":"T1","trip_id":42,"since":"2026-11-14T09:02:00Z","link":"https://tracker.example.org/admin/trips/42","text":"Bus 7 on route 1 has gone quiet: no report since 09:02. https://tracker.example.org/admin/trips/42"}
```

`event` is `raised` or `cleared`, and `kind` is one of the alert kinds of
`GET /api/v1/admin/alerts`. For a `not_started` alert, the route and GTFS trip
are the trip not started, and `trip_id` and `link` are the vehicle's last trip.
`route_short_name` is empty when the GTFS feed gives the route none, and `link`
is left out without `PUBLIC_URL`. `text` is one sentence, with the time in the
GTFS feed's timezone, for a chat service to show as it is, as Slack does.
Google Chat refuses any other field, so to a
`chat.googleapis.com` URL only `text` is sent. Microsoft Teams takes webhooks
through a Workflows flow, which has to be set to post the body's `text`. No
post names the driver or carries a position.

With `ALERT_WEBHOOK_SECRET` set, each post carries `X-Alert-Timestamp`, the
Unix time in seconds, and `X-Alert-Signature`, `sha256=` followed by the hex
HMAC-SHA256, keyed with the secret, of the timestamp, a `.` and the raw body.
Check it with a constant-time comparison, and refuse a timestamp more than a
few minutes old so a captured post cannot be replayed.

Each attempt times out after 5 seconds. One that cannot connect, times out, or
is answered `5xx` or `429` is tried again after 1, 2 and 4 seconds; any other
answer is final, and a redirect is not followed. Posts go out one at a time, in
order, without holding up the alerts or any request: up to 100 wait in a queue,
and when it is full the oldest is dropped with a `WARN`. A post that still fails
logs one `ERROR` per alert, naming the alert, the host and the status code or
why there was no answer; never the rest of the URL or what the receiver sent
back. A bad setting is refused by name, without its value. Posts still queued
when the server stops are lost, and after a restart every alert still on is
posted again as `raised`.

## Service reports

The on-time window behind the admin UI's **Analytics** page and
`GET /api/v1/admin/reports/service`. A vehicle that passes a stop from
`REPORT_ON_TIME_EARLY` early to `REPORT_ON_TIME_LATE` late is on time. The
defaults are the cut-offs the Android driver app shows its driver.

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `REPORT_ON_TIME_EARLY` | `1m` | Optional | How early a vehicle may pass a stop and still be on time. `0` is accepted and allows no early running at all. Negative or unparseable warns and defaults. |
| `REPORT_ON_TIME_LATE` | `5m` | Optional | How late a vehicle may pass a stop and still be on time. `0` is accepted. Negative or unparseable warns and defaults. |

A report places each fix on its trip's shape the way the feed places a driver
vehicle, so a fix further from the route than `RIDER_MAX_SHAPE_DISTANCE` plus
its own accuracy is left out of reports too.

## Security and proxying

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `TRUST_PROXY_HEADERS` | `false` | Required behind a reverse proxy | Trust `X-Forwarded-For` and `X-Forwarded-Proto`. Controls which address the per-IP rate limiters bucket on, whether the admin session cookie is marked `Secure`, and the client IP recorded for rider requests. Unparseable: warn and default. |
| `FEED_AUTH_ENABLED` | `false` | Optional | Require an `X-API-Key` header on `GET /gtfs-rt/vehicle-positions`. Unlike every other boolean here, an unparseable value **refuses to start** rather than warning and defaulting: `strconv.ParseBool` rejects `yes` and `on`, and falling back to `false` would leave the feed public while the operator believes it is locked. Checked before the database is opened, so a typo fails immediately. |

## Location retention

Off by default: location history is kept forever until an agency opts in. The
operational notes — that deletion is permanent, that retention is measured from
server receipt time, and that the first pass runs one interval after startup —
are under *Data Retention & Privacy* in the [README](../README.md).

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `LOCATION_RETENTION_PERIOD` | `0` | Optional | How long to keep rows in `location_points`. **`0` means keep forever**, not "keep nothing" — it disables pruning. Any other value enables it. A negative duration logs an `ERROR` and leaves retention **off**; the server still starts. |
| `LOCATION_PRUNE_INTERVAL` | `1h` | Optional | How often the pruner sweeps. Read **only when `LOCATION_RETENTION_PERIOD` is non-zero.** A value that parses to zero or a negative duration disables retention entirely rather than falling back to `1h`. |
| `LOCATION_PRUNE_BATCH_SIZE` | `10000` | Optional | Maximum rows deleted per statement, each batch in its own transaction. Read **only when `LOCATION_RETENTION_PERIOD` is non-zero.** Must be a positive 32-bit integer; anything else warns and defaults. |

An interval longer than the retention period is allowed — points then outlive
the period by up to one interval, and the server logs a `WARN` saying so.

## Rider mode

Off by default. The narrative introduction — what rider mode does, the API, and
how rider tokens differ from driver and admin tokens — is in the
[Rider mode section of the README](../README.md#rider-mode-crowdsourced-positions),
which carries the same defaults in the context they are read. Riders are
verified against the schedule configured under [GTFS schedule](#gtfs-schedule).

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `RIDER_MODE_ENABLED` | `false` | Optional | Register the rider routes, start the engine, and merge rider positions into the feed. When off, rider routes are not registered at all. Unparseable: warn and default. |
| `TRUSTED_GTFS_RT_URLS` | empty | Optional | Comma-separated external GTFS-RT VehiclePositions feeds used to corroborate rider reports. Surrounding spaces and empty entries are dropped, so a trailing comma is not a feed. URLs are not validated at startup: an unreachable one fails per poll, is recorded in the feed health, and leaves that feed's last-known entities in place. The server's own driver-reported positions are always trusted; with no external feed, a trip no driver reports has corroboration `unavailable`. |
| `TRUSTED_FEED_POLL` | `30s` | Optional | How often each trusted feed is polled. Must be positive; zero, negative or unparseable warns and defaults. |
| `TRUSTED_FEED_MAX_AGE` | `5m` | Optional | Trusted entities older than this are dropped from the snapshot. Must be positive; zero, negative or unparseable warns and defaults. |
| `RIDER_JWT_TTL` | `8760h` | Optional | Rider token lifetime (one year by default). Must be positive; zero, negative or unparseable warns and defaults. |
| `RIDER_POINT_RETENTION` | `168h` | Optional | `ride_points` rows older than this are deleted. The sweep runs hourly and that interval is not configurable. Must be positive; zero, negative or unparseable warns and defaults. |
| `RIDER_MAX_SHAPE_DISTANCE` | `60` | Optional | Metres from the trip shape — plus the reported accuracy — within which a point still matches. Must be positive; zero, negative, `NaN`, `Inf` or unparseable warns and defaults. |
| `RIDER_MAX_SPEED` | `35` | Optional | Metres per second; an implied along-shape speed above this is treated as implausible. Must be positive; zero, negative, `NaN`, `Inf` or unparseable warns and defaults. |
| `RIDER_SCHEDULE_EARLY` | `15m` | Optional | How far ahead of schedule a trip may run and still be matched. Unparseable warns and defaults; unlike the durations above, zero and negative values are accepted, since this is a tolerance window rather than an interval. |
| `RIDER_SCHEDULE_LATE` | `90m` | Optional | How far behind schedule a trip may run and still be matched. Same handling as `RIDER_SCHEDULE_EARLY`. |
| `RIDER_POINT_MAX_AGE` | `90s` | Optional | A ride whose latest matched point is older than this stops contributing to the feed. Must be positive; zero, negative or unparseable warns and defaults. |

Rider mode shares `JWT_SECRET` with the rest of the API. Rider tokens carry
`role: "rider"` and are rejected on the driver and admin APIs, and vice versa.

## Settings that change the security posture

The defaults are chosen for a first local run, not for every deployment. These
five are worth a deliberate decision before going to production.

**`JWT_SECRET` is the whole authentication system.** Anyone holding it can mint
a token for any driver, rider or admin. Generate it with `openssl rand -hex 32`,
keep it out of version control, and note that rotating it invalidates every
issued token at once — every driver app and rider app has to sign in again.

**`ADMIN_UI_ENABLED` defaults to `true`.** An operator upgrading from a version
that predates the admin UI newly serves `/admin/login` on the same port as the
API, without opting in. If that surface should not be reachable, set it to
`false` before deploying — and remember that `no` is not a boolean, so it would
leave the UI on.

**`TRUST_PROXY_HEADERS` has no safe universal default.** Both directions are
wrong somewhere:

- Left `false` behind a reverse proxy, every request appears to come from the
  proxy's address, so the per-IP login rate limiter buckets the whole internet
  as one client — a handful of failed logins locks out every real operator, and
  the session cookie is not marked `Secure` because the server cannot see that
  the original request was HTTPS.
- Set `true` when the server is directly reachable, any client can send its own
  `X-Forwarded-For` and choose which bucket its login attempts land in, which
  defeats the rate limiter entirely.

Set it to `true` if and only if the server sits behind a proxy that overwrites
those headers itself.

**`ADMIN_BOOTSTRAP_PASSWORD` is a password in the process environment.** It
persists in shell history and is readable from the process listing and from
`docker inspect`. Prefer generating it (`openssl rand -hex 16`), and unset both
bootstrap variables once the account exists — they have no further effect, and
the server does not need them again.

**`LOCATION_RETENTION_PERIOD` defaults to keeping driver GPS traces forever.**
`location_points` is a per-driver movement history, and the default of `0` never
deletes any of it. Agencies should pick a period consistent with local law and
their own driver-privacy policy rather than inheriting the default.

## Related documentation

- [`docs/development.md`](development.md) — local setup, the variables a
  development run needs, and troubleshooting
- [README — Rider mode](../README.md#rider-mode-crowdsourced-positions) — what
  rider mode does, with the same defaults in context
- [README](../README.md), under *Data Retention & Privacy* — the operator
  notes behind the retention settings
