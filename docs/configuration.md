# Configuration reference

Every environment variable the server reads, what it defaults to, and what
happens when it is set wrong. This is the complete list — it is checked against
the source by `TestConfigDoc_AllVariablesDocumented`, which fails the build if a
variable is added to the code without a row here, or if a row here names a
variable nothing reads any more.

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
`STALENESS_THRESHOLD`, `JWT_SECRET` and `RIDER_MODE_ENABLED`. Exporting any
other variable in your shell has **no effect** on a Compose-run server; add it
to that block (or an `env_file`) instead. Compose itself refuses to start
without `JWT_SECRET`; the 32-byte minimum is the server's own check.

## Core server

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `PORT` | `8080` | Optional | TCP port to listen on. The server binds `:$PORT`. |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable` | Recommended in production | PostgreSQL connection string, used for both the connection pool and the migrations run at startup. A database that cannot be reached, or a migration that fails, **refuses to start**. |
| `JWT_SECRET` | — | **Required** | HMAC-SHA256 signing key for driver, admin and rider tokens. The server **refuses to start** when it is unset or shorter than 32 bytes. |
| `STALENESS_THRESHOLD` | `5m` | Optional | How long a vehicle stays in the GTFS-RT feed after its last report. Also the window used to seed the tracker from the database at startup, and what the admin UI calls a stale vehicle. Unparseable: warn and default. **A value that parses to zero or a negative duration panics** — the tracker rejects a non-positive window rather than dropping every vehicle. |
| `READ_TIMEOUT` | `15s` | Optional | `http.Server.ReadTimeout` — the deadline for reading an entire request, headers and body. Warn and default. |
| `WRITE_TIMEOUT` | `15s` | Optional | `http.Server.WriteTimeout` — the deadline for writing the response. Warn and default. |
| `IDLE_TIMEOUT` | `60s` | Optional | `http.Server.IdleTimeout` — how long a keep-alive connection may sit idle. Warn and default. |

`STALENESS_THRESHOLD` is not the same as the ±5 minute skew check applied to the
`timestamp` in a location report; that one is request validation and is not
configurable.

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

## Security and proxying

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `TRUST_PROXY_HEADERS` | `false` | Required behind a reverse proxy | Trust `X-Forwarded-For` and `X-Forwarded-Proto`. Controls which address the per-IP rate limiters bucket on, whether the admin session cookie is marked `Secure`, and the client IP recorded for rider requests. Unparseable: warn and default. |

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
which carries the same defaults in the context they are read.

| Variable | Default | Status | Purpose |
|---|---|---|---|
| `RIDER_MODE_ENABLED` | `false` | Optional | Register the rider routes, start the engine, and merge rider positions into the feed. When off, rider routes are not registered at all. Unparseable: warn and default. |
| `GTFS_STATIC_URL` | — | **Required when rider mode is on** | GTFS static zip to verify rider reports against — an `http(s)://` URL or a local file path. The server **refuses to start** if rider mode is on and this is missing, or if the feed cannot be downloaded or parsed at startup. Ignored when rider mode is off. |
| `GTFS_STATIC_REFRESH` | `24h` | Optional | How often to re-download the zip and rebuild the index. A failed refresh keeps the previous index and logs. Must be positive; zero, negative or unparseable warns and defaults. |
| `TRUSTED_GTFS_RT_URLS` | empty | Optional | Comma-separated external GTFS-RT VehiclePositions feeds used to corroborate rider reports. Surrounding spaces and empty entries are dropped, so a trailing comma is not a feed. URLs are not validated at startup: an unreachable one fails per poll, is recorded in the feed health, and leaves that feed's last-known entities in place. The server's own driver-reported positions are always trusted; with no external feed, a trip no driver reports has corroboration `unavailable`. |
| `TRUSTED_FEED_POLL` | `30s` | Optional | How often each trusted feed is polled. Must be positive; zero, negative or unparseable warns and defaults. |
| `TRUSTED_FEED_MAX_AGE` | `5m` | Optional | Trusted entities older than this are dropped from the snapshot. Must be positive; zero, negative or unparseable warns and defaults. |
| `RIDER_JWT_TTL` | `8760h` | Optional | Rider token lifetime (one year by default). Must be positive; zero, negative or unparseable warns and defaults. |
| `RIDER_POINT_RETENTION` | `168h` | Optional | `ride_points` rows older than this are deleted. The sweep runs hourly and that interval is not configurable. Must be positive; zero, negative or unparseable warns and defaults. |
| `RIDER_MAX_SHAPE_DISTANCE` | `60` | Optional | Metres from the trip shape — plus the reported accuracy — within which a point still matches. Must be positive; zero, negative, `NaN`, `Inf` or unparseable warns and defaults. |
| `RIDER_MAX_SPEED` | `35` | Optional | Metres per second; an implied along-shape speed above this is treated as implausible. Must be positive; zero, negative, `NaN`, `Inf` or unparseable warns and defaults. |
| `RIDER_SCHEDULE_EARLY` | `15m` | Optional | How far ahead of schedule a trip may run and still be matched. Unparseable warns and defaults; unlike the durations above, zero and negative values are accepted, since this is a tolerance window rather than an interval. |
| `RIDER_SCHEDULE_LATE` | `90m` | Optional | How far behind schedule a trip may run and still be matched. Same handling as `RIDER_SCHEDULE_EARLY`. |
| `RIDER_POINT_MAX_AGE` | `90s` | Optional | A ride whose latest accepted point is older than this stops contributing to the feed. Must be positive; zero, negative or unparseable warns and defaults. |

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
