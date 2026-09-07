# Production Deployment Guide

## 1. Who this is for, and what you get

This guide is for the person who will run the Vehicle Tracker for a transit
agency: an IT administrator or a technically inclined operations manager. It
assumes you can use a Linux shell and `sudo`, and that you have never seen this
codebase before. For running the project on a laptop while developing it, read
[`development.md`](development.md) instead.

What you are deploying is one Go binary plus one PostgreSQL database. The
binary ingests location reports from the Android driver app, keeps the current
position of every active vehicle in memory, and publishes a standard GTFS
Realtime **Vehicle Positions** feed at `GET /gtfs-rt/vehicle-positions`. The
same binary serves a browser admin UI at `/admin` (sign-in, dashboard, live
fleet map, vehicle and user management, trip history) and a JSON API under
`/api/v1/`. Database migrations and the admin UI's templates and CSS are
compiled into the binary, so there is nothing else to install or copy to the
server.

Run **one** instance of the server against a database. Current vehicle
positions, rider-mode sessions and the rate limiters all live in the process's
memory, so a second instance would answer with a different view of the fleet.
On startup the server reloads recent positions from the database
(`GetRecentLocations`), so a restart is not a data loss event — it just costs a
few seconds of feed continuity.

## 2. Sizing and prerequisites

| Requirement | What you need |
|---|---|
| Host | A Linux server with a public IP. 1 vCPU and 1 GB RAM comfortably serves a fleet of tens of vehicles; the process holds one small record per active vehicle in memory. |
| Disk | Sized for `location_points`, the only table that grows quickly. Fifty vehicles reporting every 10 seconds over a 16-hour service day is roughly 105 million rows a year (see the README's [Data Retention & Privacy](../README.md#31-server-go) notes). Turn on retention (section 6) and this stops mattering. |
| PostgreSQL | 15 or newer. The project's own stack runs `postgres:17-alpine` (`docker-compose.yml`), so 17 is the best-tested choice for a new install. It may run on the same host or a managed service. |
| DNS + TLS | A hostname (`tracker.example.org` throughout this guide) pointing at the host, and a certificate. Section 7 uses Let's Encrypt. |
| Build tooling | Either Docker (option A) **or** Go 1.25 or newer (option B, `go.mod` declares `go 1.25.0`). There is no published container image — agencies build from this repository. |
| Ports | 80 and 443 open to the internet. Port 8080 (the app) and 5432 (PostgreSQL) must **not** be reachable from the internet. |

The GTFS-RT feed endpoint is unauthenticated: anyone who can reach the server
can read the current positions of your fleet. That is normally what you want (a
public realtime feed), but it means the reverse proxy is the only thing between
the world and everything else the binary serves, so do not skip section 7.

## 3. Configuration reference

Every setting is an environment variable. There is no configuration file.

Rules that apply to the whole table:

- Durations use Go's `time.ParseDuration` syntax: `30s`, `5m`, `24h`, `2160h`.
  There is no "days" unit.
- An unset or empty variable takes the default. A value that cannot be parsed
  is **ignored**, logged as a warning, and the default is used instead — so
  check the logs after a change rather than assuming it took effect.
- Booleans accept anything Go's `strconv.ParseBool` accepts: `true`, `false`,
  `1`, `0`, `t`, `f`, `TRUE`, `FALSE`.

### Core

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | TCP port the HTTP server listens on. It binds every interface; there is no loopback-only mode, so keep the port off the internet with a firewall or a Docker port binding (sections 4, 5 and 7). |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable` | PostgreSQL connection string. The default is a development convenience — always set it explicitly in production. |
| `JWT_SECRET` | — (required) | HMAC-SHA256 key that signs every driver, admin and rider token. The server logs an error and exits 1 if it is unset or shorter than 32 bytes. Generate with `openssl rand -hex 32`. |
| `STALENESS_THRESHOLD` | `5m` | How long a vehicle stays in the feed after its last report, and the window used to reseed the tracker at startup. Must be positive — a value of `0s` crashes the server at startup. |
| `READ_TIMEOUT` | `15s` | HTTP server read timeout. |
| `WRITE_TIMEOUT` | `15s` | HTTP server write timeout. |
| `IDLE_TIMEOUT` | `60s` | HTTP keep-alive idle timeout. |

### Admin UI and proxying

| Variable | Default | Purpose |
|---|---|---|
| `ADMIN_UI_ENABLED` | `true` | Serves the browser admin UI at `/admin`. Set to `false` to run the JSON API only; the `/admin` routes then return 404. **This is on by default**, so decide before you expose the host. |
| `ADMIN_BOOTSTRAP_EMAIL` | unset | Email of the first admin account. Takes effect only when `ADMIN_BOOTSTRAP_PASSWORD` is also set. |
| `ADMIN_BOOTSTRAP_PASSWORD` | unset | Password for that account, 8 characters minimum. The account is created only when the `users` table holds zero admins, so the pair is a no-op on every later boot. On that first boot, a password shorter than 8 characters aborts startup rather than creating a weak account. |
| `TRUST_PROXY_HEADERS` | `false` | Read the client IP from the last `X-Forwarded-For` hop and the scheme from `X-Forwarded-Proto`. Set to `true` **only** behind a reverse proxy you control (section 7). |

See the README's [Admin web UI](../README.md#admin-web-ui) section for the
sign-in behaviour, including the one limitation worth knowing up front:
deactivating a user or changing their password blocks new logins immediately
but does not revoke tokens already issued — those stay valid for up to 24
hours.

### Location retention (driver data)

| Variable | Default | Purpose |
|---|---|---|
| `LOCATION_RETENTION_PERIOD` | `0` | How long to keep rows in `location_points`, measured from server receipt time. `0` (the default) keeps them forever. |
| `LOCATION_PRUNE_INTERVAL` | `1h` | How often the pruner sweeps. The first sweep runs one full interval after startup, not at boot. |
| `LOCATION_PRUNE_BATCH_SIZE` | `10000` | Rows deleted per statement, each batch in its own transaction, so a large backlog does not stall location ingest. |

Deletion is permanent — there is no archive step. The README's
[retention notes](../README.md#31-server-go) cover the privacy reasoning and
the operator caveats in full.

### Rider mode (crowdsourced positions)

Rider mode is off by default and adds nothing to the driver-reported feed when
disabled. Turn it on only if you intend to accept positions from riders' phones.

| Variable | Default | Purpose |
|---|---|---|
| `RIDER_MODE_ENABLED` | `false` | Enable the rider routes, verification engine and feed merge. |
| `GTFS_STATIC_URL` | — (required when rider mode is on) | GTFS static zip, as an `http(s)://` URL or a local file path. The server exits 1 if rider mode is enabled without one. |
| `GTFS_STATIC_REFRESH` | `24h` | How often the schedule is re-downloaded. A failed refresh keeps the previous index and logs. |
| `TRUSTED_GTFS_RT_URLS` | empty | Comma-separated external VehiclePositions feeds used to corroborate riders. This server's own driver-reported positions are always trusted, so this is optional. |
| `TRUSTED_FEED_POLL` | `30s` | Poll interval for those feeds. |
| `TRUSTED_FEED_MAX_AGE` | `5m` | Trusted entities older than this are dropped. |
| `RIDER_JWT_TTL` | `8760h` | Lifetime of an anonymous rider token (one year). |
| `RIDER_MAX_SHAPE_DISTANCE` | `60` | Metres from the route shape (plus the fix's reported accuracy) for a point to match. |
| `RIDER_MAX_SPEED` | `35` | Metres per second; a higher implied along-shape speed is treated as implausible. |
| `RIDER_SCHEDULE_EARLY` | `15m` | How early a trip may be running and still match. |
| `RIDER_SCHEDULE_LATE` | `90m` | How late a trip may be running and still match. |
| `RIDER_POINT_MAX_AGE` | `90s` | A ride whose latest accepted point is older than this stops contributing to the feed. |
| `RIDER_POINT_RETENTION` | `168h` | `ride_points` rows older than this are deleted hourly. |

Every rider-mode value above except `RIDER_SCHEDULE_EARLY` and
`RIDER_SCHEDULE_LATE` must be positive: a zero or negative one is rejected with
a warning and the default is used instead. The two schedule windows are taken as
given, so setting either to `0s` narrows the adherence window rather than
falling back. The README's
[Rider mode](../README.md#rider-mode-crowdsourced-positions) section documents
the API and the verification model.

## 4. Option A — Docker Compose on one host

The `docker-compose.yml` in the repository root is the **development** stack: it
publishes PostgreSQL on port 5432 and uses the password `postgres`. Do not
deploy it as-is. Build an image from the repository and run the file below
instead.

### 4.1 Build a pinned image

```bash
git clone https://github.com/OneBusAway/vehicle-positions.git
cd vehicle-positions
docker build -t vehicle-positions:$(git rev-parse --short HEAD) .
```

The `Dockerfile` is multi-stage (`golang:1.25-alpine` builds a static binary,
`alpine:3.21` runs it), exposes port 8080, and has `vehicle-positions` as its
entrypoint. Tagging with the commit SHA rather than `latest` is what makes a
rollback possible: you can always start the previous tag again.

The repository's development `docker-compose.yml` builds this same image through
its `build: .` line, which is why the production file below refers to a tag you
built yourself instead: an explicit tag is what lets you roll back to the
previous build, and it keeps the image pinned when you edit the compose file.

### 4.2 The environment file

```bash
mkdir -p /srv/vehicle-positions
cd /srv/vehicle-positions
umask 077
cat > .env <<EOF
JWT_SECRET=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 24)
ADMIN_BOOTSTRAP_EMAIL=ops@example.org
ADMIN_BOOTSTRAP_PASSWORD=$(openssl rand -hex 12)
EOF
chmod 600 .env
cat .env   # copy the bootstrap password somewhere safe, you need it once
```

`chmod 600` matters: `JWT_SECRET` is the key that signs admin tokens, so anyone
who can read this file can mint an administrator session for your server.

### 4.3 `compose.yaml`

Save this next to the `.env` file as `/srv/vehicle-positions/compose.yaml`,
replacing `<SHA>` with the tag you built.

```yaml
services:
  db:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: vehicle_positions
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: vehicle_positions
    # No `ports:` on purpose. The database is reachable from the server
    # container over the compose network and from nowhere else.
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U vehicle_positions"]
      interval: 5s
      timeout: 5s
      retries: 5

  server:
    image: vehicle-positions:<SHA>
    restart: unless-stopped
    # Bound to loopback: nginx on the host reaches it, the internet does not.
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      PORT: "8080"
      DATABASE_URL: "postgres://vehicle_positions:${POSTGRES_PASSWORD}@db:5432/vehicle_positions?sslmode=disable"
      JWT_SECRET: "${JWT_SECRET:?JWT_SECRET must be set; the server requires 32+ bytes}"
      STALENESS_THRESHOLD: "5m"
      TRUST_PROXY_HEADERS: "true"
      ADMIN_UI_ENABLED: "true"
      LOCATION_RETENTION_PERIOD: "2160h"
      LOCATION_PRUNE_INTERVAL: "1h"
      # First boot only. Delete these two lines and re-run `docker compose up -d`
      # once you have signed in and confirmed the account works.
      ADMIN_BOOTSTRAP_EMAIL: "${ADMIN_BOOTSTRAP_EMAIL}"
      ADMIN_BOOTSTRAP_PASSWORD: "${ADMIN_BOOTSTRAP_PASSWORD}"
    depends_on:
      db:
        condition: service_healthy

volumes:
  pgdata:
```

Start it:

```bash
docker compose up -d
docker compose logs -f server
```

The server creates its schema on the first boot, so there is no migration step
to run. `TRUST_PROXY_HEADERS: "true"` is correct here only because section 7
puts nginx in front; if you have not done that yet, leave it `"false"`.

### 4.4 After the first sign-in

Remove `ADMIN_BOOTSTRAP_EMAIL` and `ADMIN_BOOTSTRAP_PASSWORD` from
`compose.yaml` and the matching lines from `.env`, then `docker compose up -d`
to apply. They are harmless if left (the bootstrap is skipped once an admin
exists) but they keep a working admin password in plain text on disk for no
further benefit.

## 5. Option B — systemd and a native binary

Use this when you already run PostgreSQL on the host and would rather not add
Docker.

### 5.1 Build and install the binary

```bash
git clone https://github.com/OneBusAway/vehicle-positions.git
cd vehicle-positions
CGO_ENABLED=0 go build -o vehicle-positions .
sudo install -o root -g root -m 0755 vehicle-positions /usr/local/bin/vehicle-positions
/usr/local/bin/vehicle-positions --help 2>/dev/null; echo "installed"
```

The binary embeds the migrations, HTML templates and CSS, so this single file is
the entire application. You can build it on a build host and copy it to the
server, as long as both are the same OS and architecture.

### 5.2 Create the service account

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin vehicle-positions
```

### 5.3 The environment file

```bash
sudo touch /etc/vehicle-positions.env
sudo chown root:vehicle-positions /etc/vehicle-positions.env
sudo chmod 0640 /etc/vehicle-positions.env
sudo tee /etc/vehicle-positions.env >/dev/null <<'EOF'
PORT=8080
DATABASE_URL=postgres://vehicle_positions:CHANGE_ME@127.0.0.1:5432/vehicle_positions?sslmode=disable
JWT_SECRET=CHANGE_ME
STALENESS_THRESHOLD=5m
TRUST_PROXY_HEADERS=true
ADMIN_UI_ENABLED=true
LOCATION_RETENTION_PERIOD=2160h
LOCATION_PRUNE_INTERVAL=1h
# First boot only — delete both lines after you have signed in.
ADMIN_BOOTSTRAP_EMAIL=ops@example.org
ADMIN_BOOTSTRAP_PASSWORD=CHANGE_ME
EOF
```

Then replace each `CHANGE_ME` (`openssl rand -hex 32` for `JWT_SECRET`). Two
things to know about this file:

- systemd's `EnvironmentFile` is **not** a shell script. Write
  `JWT_SECRET=abc123`, not `export JWT_SECRET=...`, and do not expect
  `$(openssl rand -hex 32)` to be evaluated — paste the generated value in.
- `0640 root:vehicle-positions` keeps it readable by the service and root only.
  It holds both the database password and the token-signing key.

### 5.4 The unit file

```ini
# /etc/systemd/system/vehicle-positions.service
[Unit]
Description=OneBusAway Vehicle Tracker
Documentation=https://github.com/OneBusAway/vehicle-positions
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=vehicle-positions
Group=vehicle-positions
EnvironmentFile=/etc/vehicle-positions.env
ExecStart=/usr/local/bin/vehicle-positions
Restart=on-failure
RestartSec=5s
KillSignal=SIGTERM
# The server drains in-flight requests for up to 10s on SIGTERM.
TimeoutStopSec=30s

NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LockPersonality=yes

[Install]
WantedBy=multi-user.target
```

`ProtectSystem=strict` makes the whole filesystem read-only for this service.
That is fine because the server writes nothing to disk — it logs to stdout and
stores everything else in PostgreSQL. If you enable rider mode with a *local*
GTFS zip, give `GTFS_STATIC_URL` an absolute path to a file the service can
read; a URL needs no filesystem access at all.

`After=postgresql.service` only orders startup when PostgreSQL runs on this
same host. If the database is remote, drop it — the server exits 1 when it
cannot reach the database, and `Restart=on-failure` will retry every 5 seconds
until it can.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now vehicle-positions
sudo systemctl status vehicle-positions
sudo journalctl -u vehicle-positions -f
```

### 5.5 Keep port 8080 private

The server binds every interface. With no reverse proxy in front of it, that
means the admin UI is on the internet over plain HTTP. Block the port at the
firewall and let nginx reach it over loopback:

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw deny 8080/tcp
sudo ufw enable
```

## 6. Database

### 6.1 Create the role and database

For option B (option A's compose file does this for you):

```bash
sudo -u postgres createuser --pwprompt vehicle_positions
sudo -u postgres createdb --owner vehicle_positions vehicle_positions
```

Put the password you chose into `DATABASE_URL`. Use `sslmode=disable` only for
a database on the same host reached over loopback; for a database on another
machine use `sslmode=require`, or `sslmode=verify-full` with a CA you trust.

### 6.2 Migrations

There is no separate migration command and no `migrate` subcommand. The server
runs every pending migration itself at startup, from a copy of `migrations/`
compiled into the binary, before it starts listening. A failed migration is a
startup failure: the process logs `could not run migrations` and exits 1
without serving traffic. This means a deploy is just "restart the new binary",
and it also means you should take a backup before upgrading (section 10).

### 6.3 Backups

Take a compressed custom-format dump; it restores selectively and compresses
well.

```bash
# pg_dump runs as the postgres user, so the directory must be writable by it.
sudo mkdir -p /var/backups/vehicle-positions
sudo chown postgres:postgres /var/backups/vehicle-positions
sudo chmod 0700 /var/backups/vehicle-positions

sudo -u postgres pg_dump -Fc vehicle_positions \
  -f /var/backups/vehicle-positions/vehicle_positions-$(date +%F).dump
```

For the Docker Compose deployment, run `pg_dump` inside the database container
and pipe the result out. The dump goes to stdout here, and the `>` of a plain
redirect would be opened by your own shell before `sudo` ever runs — so write
the file through `sudo tee` (or back it up somewhere your user can already
write):

```bash
sudo mkdir -p /var/backups/vehicle-positions
docker compose exec -T db pg_dump -U vehicle_positions -Fc vehicle_positions \
  | sudo tee /var/backups/vehicle-positions/vehicle_positions-$(date +%F).dump >/dev/null
```

Automate it with a systemd timer. Two files:

```ini
# /etc/systemd/system/vehicle-positions-backup.service
[Unit]
Description=Back up the vehicle-positions database
After=postgresql.service

[Service]
Type=oneshot
User=postgres
ExecStart=/bin/sh -c 'pg_dump -Fc vehicle_positions -f /var/backups/vehicle-positions/vehicle_positions-$(date +%%F).dump'
ExecStartPost=/usr/bin/find /var/backups/vehicle-positions -name "vehicle_positions-*.dump" -mtime +30 -delete
```

```ini
# /etc/systemd/system/vehicle-positions-backup.timer
[Unit]
Description=Nightly vehicle-positions database backup

[Timer]
OnCalendar=*-*-* 02:30:00
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
# The unit runs as postgres, into the directory you chowned above.
sudo systemctl daemon-reload
sudo systemctl enable --now vehicle-positions-backup.timer
sudo systemctl start vehicle-positions-backup.service   # prove it works now
```

`%%F` is doubled because systemd itself uses `%` for specifiers. Copy the dumps
off the host — a backup on the same disk as the database is not a backup.

### 6.4 Restore

Stop the server first so nothing writes while the schema is being replaced.

```bash
sudo systemctl stop vehicle-positions          # or: docker compose stop server
sudo -u postgres pg_restore --clean --if-exists \
  -d vehicle_positions /var/backups/vehicle-positions/vehicle_positions-2026-09-01.dump
sudo systemctl start vehicle-positions         # or: docker compose start server
```

### 6.5 Retention

`location_points` is a per-driver GPS trace and the fastest-growing table in the
system, so decide how long you intend to keep it before you go live. A good
starting point is 90 days, swept hourly:

```
LOCATION_RETENTION_PERIOD=2160h
LOCATION_PRUNE_INTERVAL=1h
```

Retention is off by default, deletion is permanent, and the period is measured
from when the server received the point rather than the timestamp the phone
reported — the README's [retention notes](../README.md#31-server-go) explain
each of those and the privacy reasoning behind them. Pick a period consistent
with local law and your agency's driver-privacy policy, and export anything you
want to keep before you turn it on.

## 7. Reverse proxy and TLS

Terminate TLS in nginx and forward to the server on loopback.

### 7.1 Get a certificate

```bash
sudo apt install nginx certbot
sudo mkdir -p /var/www/html
sudo certbot certonly --webroot -w /var/www/html -d tracker.example.org
```

Certbot installs its own renewal timer; confirm it with
`systemctl list-timers | grep certbot` and test with
`sudo certbot renew --dry-run`.

### 7.2 The nginx site

```nginx
# /etc/nginx/sites-available/vehicle-positions
server {
    listen 80;
    listen [::]:80;
    server_name tracker.example.org;

    # Leave this reachable over HTTP so certificate renewals keep working.
    location /.well-known/acme-challenge/ {
        root /var/www/html;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;                 # nginx 1.25.1+; older builds: `listen 443 ssl http2;`
    server_name tracker.example.org;

    ssl_certificate     /etc/letsencrypt/live/tracker.example.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/tracker.example.org/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    # Location reports are a few hundred bytes; the location endpoint itself
    # caps bodies at 1 MiB. This stops oversized uploads at the proxy first.
    client_max_body_size 2m;

    access_log /var/log/nginx/vehicle-positions.access.log;
    error_log  /var/log/nginx/vehicle-positions.error.log;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}
```

```bash
sudo ln -s /etc/nginx/sites-available/vehicle-positions /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 7.3 Then, and only then, set `TRUST_PROXY_HEADERS=true`

With the proxy in place, set `TRUST_PROXY_HEADERS=true` and restart the server.
It changes two things: the admin login rate limiter buckets by the real client
IP instead of nginx's, and the admin session cookie is marked `Secure` because
the server can now tell the request arrived over HTTPS.

Leave it `false` whenever the server can be reached directly. A client can send
any `X-Forwarded-For` it likes, so trusting the header without a proxy in front
lets an attacker spoof their IP and escape the login rate limiter. The server
reads the **rightmost** hop of `X-Forwarded-For`, which is exactly the value
`$proxy_add_x_forwarded_for` appends — nginx's own observation of who connected
— so a client-supplied header cannot displace it. The README's
[admin UI section](../README.md#admin-web-ui) states the same rule.

## 8. First boot checklist

```bash
# 1. Liveness: the process is up and serving.
curl https://tracker.example.org/health
# {"status":"ok"}

# 2. Readiness: it can reach the database. 503 with {"status":"degraded"} if not.
curl -i https://tracker.example.org/ready
# HTTP/2 200 ... {"status":"ok"}

# 3. The feed answers, with no vehicles yet.
curl 'https://tracker.example.org/gtfs-rt/vehicle-positions?format=json'
```

4. Open `https://tracker.example.org/admin/login` and sign in with
   `ADMIN_BOOTSTRAP_EMAIL` and `ADMIN_BOOTSTRAP_PASSWORD`. Change that password
   from the admin UI once you are in.

5. Remove `ADMIN_BOOTSTRAP_EMAIL` and `ADMIN_BOOTSTRAP_PASSWORD` from the
   environment file and restart. They are only read when the `users` table has
   no admin, so keeping them buys nothing and leaves a valid password on disk.

6. In the admin UI, create your first vehicle (**Vehicles → New**), then a
   driver user (**Users → New**), then assign the vehicle to that driver from
   the user's page. The assignment is what makes the vehicle appear in the
   driver app's vehicle picker (`GET /api/v1/vehicles` lists a driver's active
   assigned vehicles).

7. Hand that driver's email and password to a phone running the Android app,
   with `https://tracker.example.org` as the server URL, and watch the vehicle
   appear on **Map**.

## 9. Monitoring

| Check | What it tells you |
|---|---|
| `GET /health` | The process is alive and serving HTTP. Always `{"status":"ok"}`; it touches nothing else. Use it as the liveness probe. |
| `GET /ready` | The database answered a ping within 2 seconds. `200 {"status":"ok"}` or `503 {"status":"degraded"}`. Use it as the readiness probe and alert on it. |
| `GET /api/v1/admin/status` | Requires an admin bearer token. Returns `status`, `uptime_seconds`, `active_vehicles`, `total_vehicles_tracked` and `last_update`. This is the one that tells you whether data is actually flowing. |
| `/admin/dashboard` and `/admin/map` | The human version of the same picture: active vehicles, recent trips, live positions. |

Fetching the status endpoint from a script:

```bash
TOKEN=$(curl -s -X POST https://tracker.example.org/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"ops@example.org","password":"..."}' | jq -r .token)

curl -s https://tracker.example.org/api/v1/admin/status \
  -H "Authorization: Bearer $TOKEN" | jq .
# {"status":"ok","uptime_seconds":3600,"active_vehicles":12,...}
```

### Logs

The server writes structured JSON to stdout, one object per line — under
systemd that goes to the journal, under Compose to the container log.

```bash
sudo journalctl -u vehicle-positions -f            # option B
docker compose logs -f server                      # option A
```

Every request logs `{"msg":"request","method":...,"path":...,"status":...,"duration_ms":...}`.
Startup logs `starting server` with the port, `seeded tracker` with the count of
vehicles restored from the database, and `location retention enabled` when
retention is on. Because the format is JSON, this is a usable triage command:

```bash
sudo journalctl -u vehicle-positions -o cat \
  | jq 'select(.level=="ERROR" or .level=="WARN")'
```

Watch both levels, not just `ERROR`. Several failures that matter are logged at
`WARN`: `readiness check failed` (the database did not answer),
`failed to seed tracker from database` (the feed started empty after a restart),
`rider: GTFS refresh failed, keeping the previous index` and
`rider: trusted feed poll failed, keeping the previous vehicles` (rider mode is
running on stale data). `location retention prune failed` is logged at `ERROR`.

### What to alert on

- `/ready` returning 503 for more than a minute — the database is unreachable,
  and location reports are being lost, not queued.
- `active_vehicles` at 0 during service hours while buses are running. This is
  the failure that no process-level check catches: the server is healthy, the
  feed is just empty because no phone is reporting.
- `last_update` older than your `STALENESS_THRESHOLD` during service hours.
- Log lines at `ERROR` or `WARN` — `location retention prune failed` is an
  `ERROR`, while `readiness check failed` is a `WARN`. If you would rather not
  alert on log levels at all, key the database alert off the `/ready` probe
  above; it covers the same failure and is easier to reason about.
- Certificate expiry, if you are not relying on certbot's own renewal timer.

## 10. Upgrading

Take a backup first (section 6.3); migrations run automatically and are not
reversed automatically.

Docker Compose:

```bash
cd /path/to/checkout && git pull
docker build -t vehicle-positions:$(git rev-parse --short HEAD) .
# edit the image tag in /srv/vehicle-positions/compose.yaml
cd /srv/vehicle-positions && docker compose up -d
docker compose logs -f server
```

systemd:

```bash
cd /path/to/checkout && git pull
CGO_ENABLED=0 go build -o vehicle-positions .
sudo install -o root -g root -m 0755 vehicle-positions /usr/local/bin/vehicle-positions
sudo systemctl restart vehicle-positions
sudo journalctl -u vehicle-positions -n 50
```

What a restart costs: pending migrations apply before the server listens, so a
schema change shows up as a slightly longer startup. The in-memory tracker is
reseeded from the database using `STALENESS_THRESHOLD`, so recently active
vehicles reappear in the feed immediately. Rider-mode rides are held in memory
only and do not survive a restart — every active ride is ended and filed as the
new process starts.

Read the release notes for new default-on surfaces before you upgrade. The
admin UI is the example the README calls out: it did not exist in earlier
versions and it is **on by default**, so an upgrade exposes `/admin` unless you
set `ADMIN_UI_ENABLED=false` first.

To roll back, start the previous image tag (Compose) or reinstall the previous
binary (systemd). If the newer version applied a migration, restore the pre-
upgrade dump as well.

## 11. Connecting the feed to OneBusAway

The feed is a standard GTFS Realtime `FeedMessage` containing
`VehiclePosition` entities, served as protobuf:

```
https://tracker.example.org/gtfs-rt/vehicle-positions
```

Point your OneBusAway instance's vehicle-positions realtime source at that URL.
OneBusAway consumes GTFS-RT Vehicle Positions natively — no changes to OBA are
required, and any other GTFS-RT-compliant consumer works the same way (see the
README's [How This Connects to OneBusAway](../README.md#8-how-this-connects-to-onebusaway)).

Two query parameters help when you are checking the feed by hand:

```bash
# Human-readable JSON instead of protobuf — for eyeballing, not for OBA.
curl 'https://tracker.example.org/gtfs-rt/vehicle-positions?format=json' | jq .

# Pick one half of the feed: driver, rider, or all (the default).
curl 'https://tracker.example.org/gtfs-rt/vehicle-positions?format=json&source=driver' | jq '.entity | length'
```

`source` accepts exactly `driver`, `rider` and `all`; anything else returns
`400 {"error":"invalid source"}`. With rider mode off, `driver` and `all` are
identical. Rider-derived entities are always distinguishable: their id is
`rider:<trip_id>:<start_date>` and their vehicle label is `Rider-reported`, so
you can feed OBA `?source=driver` if you want agency-reported positions only.

A driver-reported entity's id and `vehicle.id` are the `vehicle_id` the app
reported — the vehicle the driver picked, which you created in the admin UI —
and its `trip.trip_id` and `trip.route_id` are whatever the app sent with the
fix. For OBA to match them to your schedule, those must be the ids from the
same GTFS static feed OBA is using.

## 12. Distributing the Android app

The driver app lives in [`android/`](../android/) and takes the server URL on
its **login screen**, stored per device. One APK therefore works for every
agency and every server — there is nothing to rebuild per deployment.

### 12.1 Build a release APK

```bash
cd android
./gradlew :app:assembleRelease
ls app/build/outputs/apk/release/app-release-unsigned.apk
```

JDK 17 is what CI uses (`.github/workflows/android.yml`), and the Gradle build
targets Java 17, so build with a JDK 17 toolchain. The output is
**unsigned**: `android/app/build/outputs/apk/release/app-release-unsigned.apk`.
`android/app/build.gradle.kts` enables minification for the release build type
but defines no `signingConfig`, so signing is the agency's job. Android will not
install an unsigned APK.

### 12.2 Create a signing key

Do this once, and keep the keystore forever: an update can only install over an
existing app if it is signed with the *same* key. Lose it and every driver has
to uninstall and reinstall.

```bash
keytool -genkeypair -v \
  -keystore ~/vehicle-tracker-release.jks \
  -alias vehicle-tracker \
  -keyalg RSA -keysize 4096 -validity 10000
```

Back the `.jks` file up somewhere separate from the build machine, and treat the
passwords like the server's `JWT_SECRET`.

### 12.3 Sign the APK

`zipalign` and `apksigner` ship with the Android SDK build-tools (adjust the
version to match what you have installed under `$ANDROID_HOME/build-tools/`):

```bash
export BT="$ANDROID_HOME/build-tools/36.0.0"

"$BT/zipalign" -p -f 4 \
  app/build/outputs/apk/release/app-release-unsigned.apk \
  app-release-aligned.apk

"$BT/apksigner" sign \
  --ks ~/vehicle-tracker-release.jks \
  --ks-key-alias vehicle-tracker \
  --out app-release.apk \
  app-release-aligned.apk

"$BT/apksigner" verify --print-certs app-release.apk
```

Align before signing, not after: rewriting the archive afterwards invalidates
the signature.

### 12.4 Or let Gradle sign it

If you build releases often, add a signing config that is used only when the
keystore is available, so the repository's CI (which has no keystore) keeps
building as it does today. In `android/app/build.gradle.kts`:

```kotlin
android {
    val keystorePath = System.getenv("ANDROID_KEYSTORE")

    signingConfigs {
        if (keystorePath != null) {
            create("release") {
                storeFile = file(keystorePath)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.findByName("release")
        }
    }
}
```

With the four `ANDROID_*` variables exported, `./gradlew :app:assembleRelease`
writes a signed `app-release.apk` to the same directory; without them the build
still produces `app-release-unsigned.apk`. Never commit the keystore or its
passwords — pass them through the environment or a secrets store. Verify the
result with `apksigner verify --print-certs` before you hand the APK out.

### 12.5 Get it onto driver phones

Most agencies deploying this have no Play Store presence, so sideload:

- **Over USB**, for phones you control:

  ```bash
  adb install -r app-release.apk
  ```

  `-r` reinstalls over an existing copy, keeping the driver's stored login.

- **From a download link**, for phones you do not: host the APK on your own
  HTTPS site (`https://tracker.example.org` is fine if you can serve a static
  file from nginx) and send drivers the link. On Android 8 and newer, the phone
  will prompt to allow the *downloading app* — Chrome, or the file manager — to
  install unknown apps; the driver grants it once under
  **Settings → Apps → Special app access → Install unknown apps**.

Bump `versionCode` in `android/app/build.gradle.kts` for every build you hand
out (it is `1` today). Android refuses to install an APK whose `versionCode` is
lower than the installed one, and identical codes make it impossible to tell
which build a driver is running when you are debugging a report from the field.

Then tell drivers three things: the server URL, their email, and their password.
The app's [smoke-test walkthrough](android-smoke-test.md) is a useful script for
verifying a new build end to end before you distribute it.
