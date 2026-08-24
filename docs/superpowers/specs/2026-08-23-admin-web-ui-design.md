# Admin Web UI v1 — Design Spec

**Date:** 2026-08-23 (revised 2026-08-24 after design review)
**Status:** Approved for implementation
**Milestone:** 4 (Admin Interface + End-to-End Polish) from the project README

## 1. Overview

The repo contains a proof-of-concept admin UI (commit `e49ea27`) behind the
`ADMIN_UI_ENABLED` flag: five server-rendered pages (dashboard, live map,
vehicles, users, trips) styled with Tailwind, populated entirely with
hardcoded mock data, and served with **no authentication**. This project
replaces the mockup with a functional, authenticated admin interface backed
by real data, fulfilling the Milestone 4 admin UI deliverable:

- Dashboard: active vehicle count, feed health, last update times
- Vehicle map: Leaflet/OSM showing current vehicle positions
- Vehicle management: CRUD
- User management: CRUD + vehicle assignment
- Trip history: searchable list with location trail visualization
- CSV download of location data

## 2. Goals

1. Session-authenticated admin UI (admin-role users only).
2. Every page shows real data from the store and the in-memory tracker.
3. Vehicle and user CRUD, user–vehicle assignment management, trip history
   with filters and free-text search, per-trip location trails on the map,
   and CSV download of per-vehicle location history.
4. Live map that polls current vehicle positions.
5. Admin UI works without internet access to CDNs (assets vendored/compiled
   into the binary via `go:embed`) — target agencies have intermittent
   connectivity.
6. New JSON endpoints added for the UI are proper additions to the admin
   REST API (trips listing was promised in Milestone 2 and never built).

## 3. Non-Goals

- API-key auth for the GTFS-RT feed (separate work item).
- Dark mode, localization of the admin UI.
- Password self-service / reset emails.
- Signup flow — the mockup's signup page is removed; admins create users.
  (`/api/v1/auth/signup` never existed on the server.)
- Audit logging, multi-agency tenancy.
- Charts/analytics beyond the dashboard counters. Feed **error rates**
  (README §3.3) are explicitly deferred: the server does not yet count
  ingest errors, and adding metrics plumbing is out of scope for v1.
- Server-side token revocation on logout (clearing the cookie is enough for
  v1; tokens expire in ≤ 24 h).
- A `next`/return-to parameter on the login redirect (v1 always lands on
  the dashboard after login).

## 4. Architecture

Server-rendered Go templates (existing `web/templates` structure) with a
session cookie for authentication. Pages pull data from the store and
tracker directly in their handlers — no internal HTTP hop. A small amount
of vanilla JS handles the Leaflet map (live polling, trip trails).

### 4.1 Handler composition: `newHandler`

`newMux` currently returns `*http.ServeMux` and the admin UI is bolted on
separately in `main()`. A new constructor becomes the single composition
point used by both `main()` and tests:

```go
func newHandler(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter,
    loginLimiter *LoginRateLimiter, jwtSecret []byte, startTime time.Time,
    cfg adminUIConfig) (http.Handler, error)
```

It builds the API mux via `newMux`, registers the admin UI routes when
enabled (passing store/tracker/secret/templates into the page handlers),
and wraps the whole thing in `http.CrossOriginProtection` (Go 1.25) and
the request logger. `newMux` remains for existing JSON-only tests; new
wiring tests target `newHandler`. `appStore` gains the new store
interfaces, and the `noopStore` in `route_wiring_test.go` is extended to
match.

### 4.2 Authentication: session cookie carrying the existing JWT

- `POST /admin/login` — HTML form (email + password). Validates credentials
  with the same logic as the API login (bcrypt compare, timing-safe on
  unknown email), requires `role == "admin"`, then sets a cookie:
  - Name `vp_session`, value = the same HS256 JWT `generateJWT` produces.
  - `HttpOnly`, `SameSite=Lax`, `Path=/`, `Max-Age` matching the 24 h token
    TTL. `Secure` is set when the request arrived over TLS, or when
    `TRUST_PROXY_HEADERS=true` and `X-Forwarded-Proto: https` (see §4.10).
  - Non-admin users get the login page back with "admin access required"
    (403). Deactivated users are treated as invalid credentials.
- `POST /admin/logout` — clears the cookie, redirects to `/admin/login`.
- `GET /admin/login` — renders the login page; if already authenticated as
  admin, redirects to `/admin/dashboard`. `GET /admin` and `GET /admin/`
  redirect to the dashboard (or login when unauthenticated).
- New middleware `requireAdminPage(jwtSecret)` guards every `/admin/*` page
  except login: parses/validates the cookie JWT, requires `role == "admin"`,
  and on failure redirects (`303 See Other`) to `/admin/login` instead of
  returning JSON.
- **Cookie fallback in `requireAuth`:** the existing API middleware falls
  back to the `vp_session` cookie **only when the `Authorization` header is
  entirely absent**. A present-but-malformed or invalid `Authorization`
  header is rejected with 401 without consulting the cookie. This lets the
  browser session use the existing admin JSON endpoints (notably the CSV
  location-history download) without a second auth system.
- **Login gate for deactivated users:** both login paths (`/admin/login`
  and `/api/v1/auth/login`) reject users whose `active` flag is false with
  the same "invalid email or password" response used for wrong passwords.

### 4.3 CSRF protection

All state-changing routes (admin forms and the now cookie-reachable API
mutations) are protected by Go 1.25's `http.CrossOriginProtection`, applied
in `newHandler` around the composed handler. It rejects browser-originated
cross-origin non-safe requests using `Sec-Fetch-Site` / `Origin`-vs-`Host`;
non-browser clients (the Android app's Retrofit, curl) send neither header
and are unaffected. All admin forms use `POST`; no state-changing `GET`
routes exist.

### 4.4 Route map

Pages (server-rendered, cookie-authed unless noted):

| Route | Purpose |
|---|---|
| `GET /admin/login` | Login form (public) |
| `POST /admin/login` | Authenticate, set cookie (public, rate limited) |
| `POST /admin/logout` | Clear session |
| `GET /admin` | Redirect to dashboard |
| `GET /admin/dashboard` | Real stats + recent activity |
| `GET /admin/map` | Live map (also renders a single trip trail via `?trip_id=N`) |
| `GET /admin/vehicles` | Vehicle list |
| `GET /admin/vehicles/new`, `POST /admin/vehicles` | Create vehicle |
| `GET /admin/vehicles/{id}/edit`, `POST /admin/vehicles/{id}` | Edit vehicle (label, agency tag) |
| `POST /admin/vehicles/{id}/deactivate` | Deactivate (confirm dialog in UI) |
| `POST /admin/vehicles/{id}/activate` | Reactivate |
| `GET /admin/users` | User list |
| `GET /admin/users/new`, `POST /admin/users` | Create user (name, email, password, role) |
| `GET /admin/users/{id}/edit`, `POST /admin/users/{id}` | Edit user (name, email, role, optional new password) + manage vehicle assignments |
| `POST /admin/users/{id}/deactivate` | Deactivate user |
| `POST /admin/users/{id}/activate` | Reactivate user |
| `POST /admin/users/{id}/vehicles` | Assign a vehicle |
| `POST /admin/users/{id}/vehicles/{vehicleID}/remove` | Unassign a vehicle |
| `GET /admin/trips` | Trip history with filters |

The mockup's `GET /admin/signup` route and signup template mode are
deleted.

New JSON API endpoints (Bearer or cookie, admin role):

| Route | Purpose |
|---|---|
| `GET /api/v1/admin/vehicles/live` | Current tracker state joined with vehicle labels and active-trip info; feeds the live map |
| `GET /api/v1/admin/trips` | List trips (API-only addition fulfilling the Milestone 2 promise; the trips *page* queries the store directly). Filters: `status` (active/completed), `vehicle_id`, `q`, `limit`/`offset` (default 50, max 200), newest first |
| `GET /api/v1/admin/trips/{id}/locations` | Trip metadata + ordered location points for one trip; feeds the trail view (single fetch for the map page) |

`/api/v1/admin/vehicles/live` response entries:

```json
{
  "vehicle_id": "bus-1",
  "label": "Bus 1",
  "latitude": -1.29, "longitude": 36.82,
  "bearing": 180.0,            // nullable
  "speed": 8.5,                // nullable
  "gtfs_trip_id": "route_5_0830",  // string from the tracker (client-reported)
  "trip_db_id": 42,            // nullable int64: trips.id of the vehicle's active trip
  "route_id": "5",             // nullable, from the active-trip join
  "driver_name": "Asha",       // nullable, from the active-trip join
  "reported_at": 1752566400,   // device-reported unix epoch
  "updated_at": "2026-08-24T04:00:00Z"  // server receipt time (staleness basis)
}
```

Only vehicles currently in the tracker (within the staleness threshold)
appear. A tracked vehicle missing from the `vehicles` table (edge case)
is returned with `label` equal to its id rather than erroring.

`/api/v1/admin/trips/{id}/locations` response: `{"trip": {…TripSummary…},
"points": [{latitude, longitude, bearing, speed, accuracy, reported_at,
received_at}, …]}` ordered by `received_at ASC`, capped at 10,000 points.

### 4.5 Trip trail derivation (no ingest change)

`location_points.trip_id` is a client-supplied GTFS/route **string**, not a
`trips.id` reference, so trails are derived instead from columns the server
controls: points where `vehicle_id = trips.vehicle_id` AND `driver_id =
trips.user_id::text` (`driver_id` is set server-side from the JWT `sub`)
AND `received_at` between `trips.start_time` and
`COALESCE(trips.end_time, NOW())`. The partial unique index
`idx_trips_one_active_per_user` makes this window exact per driver. The
existing `idx_location_points_vehicle_received_at (vehicle_id,
received_at DESC)` index covers the query; **no new index or ingest-path
change is required**.

### 4.6 Forms and error handling

- Classic POST → redirect (PRG). Success redirects carry a flash via a
  short-lived (60 s) **HttpOnly** `vp_flash` cookie whose value is an
  opaque code (e.g. `vehicle_created`); the layout maps known codes to
  fixed message strings server-side and clears the cookie. Unknown codes
  render nothing. Flash values are never rendered as raw markup.
- Validation failures re-render the form with a page-level error message
  and the submitted values (except passwords), status 422.
- Create-vehicle collisions (id already exists) re-render at 422 with a
  "vehicle id already exists" error — no silent overwrite.
- Store errors render a friendly 500 page section; details go to `slog`.
- Unknown IDs → 404 page.

### 4.7 Pages: data contracts

**Dashboard** — stat cards: total active vehicles (store count), active
now + feed last-update time (both from `Tracker.Status()`, the same source
as `/api/v1/admin/status` — no re-derived "active" definition), registered
active drivers (store count, role `driver`), active trips (store count).
Feed health strip: `Status().LastUpdate` and the staleness threshold.
Recent activity table: `Tracker.ActiveVehicles()` joined with labels and
active-trip route, newest first, capped at 10, humanized "last update"
ages. Empty state when nothing is active.

**Live map** — JS fetches `/api/v1/admin/vehicles/live` every 10 s
(visible tab only), fits bounds on first load, and shows an empty-state
banner when no vehicles are active. Mock corridor polylines and the
active/idle marker distinction are deleted: every tracked vehicle is by
definition fresh, so there is a single marker style. Popups show label,
vehicle id, route id, driver name, speed (when present), and last-update
age. The fleet sidebar lists the same live vehicles; the mockup's
"route count" stat becomes the count of distinct non-empty `route_id`
values in the live data. With `?trip_id=N` the page instead makes a
single fetch of the trail endpoint and draws a polyline with start/end
markers plus the returned trip metadata.

**Vehicles** — table: id, label, agency tag, active badge, live "last
seen" (from tracker when present), current driver (from active-trip join
when present). Row actions: edit, deactivate/reactivate (POST forms with
JS confirm), and **Download CSV** linking to
`GET /api/v1/admin/vehicles/{id}/locations?format=csv` (existing endpoint,
reachable via the session cookie; default range is the endpoint's default
last 24 h). The page shows active vehicles by default; `?include_inactive=1`
shows all. Filtering happens in the page handler over the existing
`ListVehicles` result (fleet sizes are small); the store method is not
changed. The create form's `id` field enforces the same rules as the API
(`^[a-zA-Z0-9._-]+$`, ≤ 50 chars) with the same error text.

**Users** — table: name, email, role badge, active badge, assigned-vehicle
count. Create form (name, email, password ≥ 8 chars, role select). Edit
form adds optional password change and an assignments section: current
vehicles with remove buttons and a select of active unassigned vehicles to
add. Deactivate/reactivate with confirm. The mockup's fake "Last Seen"
column is dropped. The existing API hard-delete
(`DELETE /api/v1/admin/users/{id}`) is left unchanged; the UI only
soft-deactivates.

**Trips** — table: trip id, vehicle label, driver name, route id, GTFS
trip id, start/end times (rendered in UTC with an explicit "UTC" label),
status badge, duration. Filter bar: status select, vehicle select, and a
free-text `q` input matching driver name, `route_id`, or `gtfs_trip_id`
(ILIKE substring), applied via GET query params — this satisfies the
README's "searchable list". Pagination with next/prev links (50/page),
using the existing `limit+1 → hasMore` idiom from
`handleGetLocationHistory`. Row action: "View trail" →
`/admin/map?trip_id={trips.id}`.

### 4.8 Store additions

One schema migration is required: **`000010_add_user_active`** —
`ALTER TABLE users ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;`
(down: drop column). Note the migration sequence skips `000007`; the next
number is `000010`.

New methods on `*Store` (each behind a narrow interface consumed by its
handler, matching existing convention). Fixed-shape queries go through
sqlc (`db/query.sql`, regenerate with `make generate`); dynamically
filtered ones are hand-written pgx in the store file:

- `CountActiveVehicles(ctx) (int, error)` — sqlc
- `CountActiveUsersByRole(ctx, role string) (int, error)` — sqlc
- `CountActiveTrips(ctx) (int, error)` — sqlc
- `SetVehicleActive(ctx, id string, active bool) error` — sqlc; powers
  deactivate/reactivate. The edit form updates only label/agency tag via a
  new `UpdateVehicleInfo(ctx, id, label, agencyTag string) error` (sqlc)
  that never touches `active` — the existing upsert's `active = true`
  behavior made editing a deactivated vehicle silently reactivate it.
- `SetUserActive(ctx, id int64, active bool) error` — sqlc.
  `GetUserByEmail` gains an `active` column in its result; login handlers
  check it. `UserResponse` gains an `Active` field.
- `ListTrips(ctx, TripFilter) ([]TripSummary, error)` — hand-written pgx
  (dynamic filters: status, vehicleID, q, limit/offset), joins users
  (driver name) and vehicles (label), ordered `start_time DESC`, called
  with `limit+1` for `hasMore`
- `GetTripSummary(ctx, id int64) (*TripSummary, error)` — sqlc; caller is
  the trail endpoint (its `trip` object)
- `ListTripLocations(ctx, tripID int64) ([]LocationPoint, error)` —
  implements §4.5's windowed query, `received_at ASC`, cap 10,000
- `ListActiveTripsByVehicle(ctx) (map[string]ActiveTripInfo, error)` — one
  `DISTINCT ON (vehicle_id) … ORDER BY vehicle_id, start_time DESC` query
  powering the live endpoint's and vehicle page's driver/route join. The
  schema only guarantees one active trip per *user*, so the newest active
  trip per vehicle is the defined tiebreak (no new unique index; changing
  `StartTrip` semantics is out of scope).

### 4.9 Static assets: no CDNs

- Vendor Leaflet 1.9.4 (`leaflet.js`, `leaflet.css`, marker images) into
  `web/static/vendor/leaflet/`.
- Replace the `cdn.tailwindcss.com` runtime script with a committed,
  pre-compiled stylesheet at `web/static/css/admin.css`, generated by the
  **standalone Tailwind CLI (v4.x, hard prerequisite — already installed
  in the dev environment)** scanning `web/templates/**`. Any inline
  `tailwind.config` blocks in the mockup templates (custom colors, fonts,
  shadows, arbitrary values) are ported to a CSS-first `@theme` block in
  the Tailwind input file. `make css` regenerates; CI runs `make css` and
  fails if the committed output is stale. There is no hand-written-CSS
  fallback — the CLI is required tooling, same as `sqlc`.
- Drop Google Fonts; use a system font stack (`system-ui, -apple-system,
  Segoe UI, Roboto, ...`). The `display-font` class keeps a distinct
  weight treatment instead of a webfont.
- CARTO basemap tiles remain a runtime network dependency of the map page
  only (unavoidable for map imagery; documented).

### 4.10 Proxy awareness

A single `TRUST_PROXY_HEADERS` env var (default `false`) governs all proxy
header trust consistently:

- **Client IP** (rate limiting): `X-Forwarded-For` rightmost hop when
  trusted, else `r.RemoteAddr` host.
- **Secure cookie flag**: `X-Forwarded-Proto: https` when trusted, else
  `r.TLS != nil`.

The production deployment guide documents setting it to `true` behind the
reverse proxy.

### 4.11 Login rate limiting

A dedicated `LoginRateLimiter` guards `POST /admin/login` and
`POST /api/v1/auth/login`, with two dimensions: per-client-IP (10
attempts/min, §4.10 IP extraction) and per-submitted-email (5
attempts/min) so a shared-IP bucket can't lock out all admins and a
single-target attack is still bounded. Implementation mirrors
`VehicleRateLimiter`'s fixed-window style but **fails closed** at capacity
(the existing limiter's fail-open default is wrong for an auth endpoint).
Limited requests get 429 (page: "too many attempts, try again shortly").

### 4.12 Flag, bootstrap, and rollout

- `ADMIN_UI_ENABLED` now defaults to **true** (the UI is authenticated);
  a false value disables registration of all `/admin` routes. The startup
  warning about an unauthenticated demo UI is removed. The change is
  called out in the README (upgrading operators will newly serve
  `/admin/login`; it is session-gated).
- **First-admin bootstrap** (both): `seed_dev.sql` gains an admin user for
  dev, and the server honors one-shot `ADMIN_BOOTSTRAP_EMAIL` /
  `ADMIN_BOOTSTRAP_PASSWORD` env vars at startup — creating that admin
  only when the users table contains **zero admin users**, logging what it
  did. Without either, a fresh production install cannot mint an admin
  (the users API requires an admin token).
- Docs: README and `docs/development.md` gain a short "Admin UI" section
  (URL, flag, bootstrap, `TRUST_PROXY_HEADERS`).

## 5. Component boundaries

- `admin_session.go` — cookie issue/clear/parse, `requireAdminPage`
  middleware, flash read/write helpers, login/logout handlers. No
  knowledge of specific pages.
- `admin_page_handlers.go` (replaces the mock handlers in
  `admin_handlers.go`) — page rendering and form processing. Handlers are
  built by a constructor that receives the parsed templates, store
  interfaces, and tracker — `loadTemplates`' result is passed in
  explicitly; the package-level `templates` global is removed.
- `admin_live_handlers.go` — `/api/v1/admin/vehicles/live`, trips list,
  trip locations JSON handlers.
- `store_admin_stats.go`, additions to `store_trips.go`,
  `store_vehicles.go`, `store_users.go` — new queries per §4.8.
- `ratelimit_login.go` — `LoginRateLimiter`.
- `auth.go` — cookie fallback in `requireAuth`; active-user check in
  login.
- `web/templates/**`, `web/static/**` — templates and vendored assets.

Each unit is testable in isolation (httptest + fake stores for handlers,
`newTestStore` + `DATABASE_URL` for store methods, following existing
conventions).

## 6. Testing

- **Store tests** (Postgres-backed, skip without `DATABASE_URL`): each new
  method — counts, trip listing filters/search/pagination/order, trail
  windowing (points inside/outside the trip window, other drivers'
  points on the same vehicle excluded), active-trip-per-vehicle tiebreak,
  user/vehicle activate/deactivate round-trips, migration 000010.
- **Handler tests** (httptest, fake stores): login form success/failure/
  non-admin/deactivated-user/rate-limit (both dimensions, fail-closed);
  cookie attributes incl. Secure under `TRUST_PROXY_HEADERS`; logout;
  redirect-to-login on every protected page when unauthenticated/expired/
  tampered cookie; Bearer-wins and absent-vs-malformed-header fallback
  rules in `requireAuth`; CSRF rejection of cross-origin form POST through
  `newHandler`; each page renders real data (golden substrings, not
  full-page snapshots); form validation errors re-render with 422;
  create-vehicle collision 422; PRG redirects + flash codes; 404s; live
  endpoint JSON shape incl. nullable fields and label fallback.
- **Route wiring test** (`route_wiring_test.go` pattern, extended
  `noopStore`, targeting `newHandler`): every new route present with
  correct middleware; `/admin/*` pages redirect when unauthenticated; JSON
  endpoints return 401 JSON; admin UI disabled ⇒ `/admin/*` 404s.
- **End-to-end smoke** (manual, documented): docker-compose up → bootstrap
  admin → log in → create vehicle/user → assign → simulate locations →
  vehicle on map → trip in history → trail renders → CSV downloads.

## 7. Risks

- **Tailwind CLI availability**: pinned-version standalone CLI is a build
  prerequisite; CI verifies the committed CSS so runtime never depends on
  it.
- **Cookie fallback on the API** widens the browser-reachable surface;
  mitigated by `CrossOriginProtection`, `SameSite=Lax`, HttpOnly, and
  admin-role checks already on every admin endpoint.
- **Trail accuracy** depends on the §4.5 window derivation; points sent by
  a driver outside any trip window are simply not part of any trail
  (acceptable — the same is true of the GTFS-RT feed's trip association).
- **Two drivers, one vehicle**: the live endpoint shows the newest active
  trip's driver (defined tiebreak in §4.8); the underlying double-active
  possibility is unchanged driver-API behavior.
