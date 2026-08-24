# Admin Web UI v1 — Design Spec

**Date:** 2026-08-23
**Status:** Approved for implementation
**Milestone:** 4 (Admin Interface + End-to-End Polish) from the project README

## 1. Overview

The repo contains a proof-of-concept admin UI (PR: `e49ea27`) behind the
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

## 2. Goals

1. Session-authenticated admin UI (admin-role users only).
2. Every page shows real data from the store and the in-memory tracker.
3. Vehicle and user CRUD, user–vehicle assignment management, trip history
   with filters, and per-trip location trails on the map.
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
- Charts/analytics beyond the dashboard counters.

## 4. Architecture

Server-rendered Go templates (existing `web/templates` structure) with a
session cookie for authentication. Pages pull data from the store and
tracker directly in their handlers — no internal HTTP hop. A small amount
of vanilla JS handles the Leaflet map (live polling, trip trails).

### 4.1 Authentication: session cookie carrying the existing JWT

- `POST /admin/login` — HTML form (email + password). Validates credentials
  with the same logic as the API login (bcrypt compare, timing-safe on
  unknown email), requires `role == "admin"`, then sets a cookie:
  - Name `vp_session`, value = the same HS256 JWT `generateJWT` produces.
  - `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when the request is TLS
    (or `X-Forwarded-Proto: https`), `Max-Age` matching the 24h token TTL.
  - Non-admin users get the login page back with "admin access required".
- `POST /admin/logout` — clears the cookie, redirects to `/admin/login`.
- `GET /admin/login` — renders the login page; if already authenticated as
  admin, redirects to `/admin/dashboard`.
- New middleware `requireAdminPage(jwtSecret)` guards every `/admin/*` page
  except login: parses/validates the cookie JWT, requires `role == "admin"`,
  and on failure redirects (`303 See Other`) to `/admin/login` instead of
  returning JSON.
- **Cookie fallback in `requireAuth`:** the existing API middleware checks
  the `Authorization: Bearer` header first and, when absent, falls back to
  the `vp_session` cookie. This lets the browser session use the existing
  admin JSON endpoints (e.g. the CSV location-history download) without a
  second auth system. Bearer, when present, wins; an invalid Bearer is
  rejected without falling back to the cookie.

### 4.2 CSRF protection

All state-changing routes (admin forms and the now cookie-reachable API
mutations) are protected by Go 1.25's `http.CrossOriginProtection`, wrapped
around the whole mux in `main.go` (and in `newMux` tests). It rejects
browser-originated cross-origin non-safe requests using `Sec-Fetch-Site` /
`Origin`; non-browser clients (the Android app's Retrofit, curl) send
neither header and are unaffected. All admin forms use `POST`; no
state-changing `GET` routes exist.

### 4.3 Route map

Pages (server-rendered, cookie-authed unless noted):

| Route | Purpose |
|---|---|
| `GET /admin/login` | Login form (public) |
| `POST /admin/login` | Authenticate, set cookie (public, rate limited) |
| `POST /admin/logout` | Clear session |
| `GET /admin/dashboard` | Real stats + recent activity |
| `GET /admin/map` | Live map (also renders a single trip trail via `?trip_id=N`) |
| `GET /admin/vehicles` | Vehicle list |
| `GET /admin/vehicles/new`, `POST /admin/vehicles` | Create vehicle |
| `GET /admin/vehicles/{id}/edit`, `POST /admin/vehicles/{id}` | Edit vehicle (label, agency tag, active) |
| `POST /admin/vehicles/{id}/deactivate` | Deactivate (confirm dialog in UI) |
| `GET /admin/users` | User list |
| `GET /admin/users/new`, `POST /admin/users` | Create user (name, email, password, role) |
| `GET /admin/users/{id}/edit`, `POST /admin/users/{id}` | Edit user (name, email, role, optional new password) + manage vehicle assignments |
| `POST /admin/users/{id}/deactivate` | Deactivate user |
| `POST /admin/users/{id}/vehicles` | Assign a vehicle |
| `POST /admin/users/{id}/vehicles/{vehicleID}/remove` | Unassign a vehicle |
| `GET /admin/trips` | Trip history with filters |

The mockup's `GET /admin/signup` route and signup template mode are deleted.
`GET /admin` redirects to `/admin/dashboard`.

New/changed JSON API endpoints (Bearer or cookie, admin role):

| Route | Purpose |
|---|---|
| `GET /api/v1/admin/vehicles/live` | Current tracker state joined with vehicle labels and active-trip info; feeds the live map |
| `GET /api/v1/admin/trips` | List trips: filters `status` (active/completed), `vehicle_id`, `limit`/`offset` (default 50, max 200), newest first |
| `GET /api/v1/admin/trips/{id}/locations` | Ordered location points for one trip; feeds the trail view |

`/api/v1/admin/vehicles/live` response entries: `vehicle_id`, `label`,
`latitude`, `longitude`, `bearing`, `speed` (nullable), `trip_id`,
`route_id`, `driver_name` (nullable when no active trip matches), and
`recorded_at`. Only vehicles currently in the tracker (within the staleness
threshold) appear.

### 4.4 Forms and error handling

- Classic POST → redirect (PRG). Success redirects carry a flash message via
  a short-lived (60s) non-HttpOnly `vp_flash` cookie rendered once by the
  layout and cleared.
- Validation failures re-render the form with a page-level error message and
  the submitted values (except passwords), status 422.
- Store errors render a friendly 500 page section; details go to `slog`.
- Unknown IDs → 404 page.

### 4.5 Pages: data contracts

**Dashboard** — stat cards: total vehicles (store count, active flag true),
active now (tracker), registered drivers (store count of role `driver`,
active), active trips (store count `status = 'active'`). Feed health strip:
tracker last-update time and staleness threshold. Recent activity table:
tracker's active vehicles joined with labels/route, newest first, capped at
10, with humanized "last update" times. Empty state when nothing is active.

**Live map** — JS fetches `/api/v1/admin/vehicles/live` every 10 s (visible
tab only), renders the existing marker/popup design with real fields, fits
bounds on first load, and shows an empty-state banner when no vehicles are
active. Mock corridor polylines are deleted. The fleet sidebar lists the
same live vehicles. With `?trip_id=N` the page instead fetches the trail
endpoint once and draws a polyline with start/end markers plus trip
metadata.

**Vehicles** — table: id, label, agency tag, active badge, live "last seen"
(from tracker when present), current driver (from active trip when
present). Row actions: edit, deactivate (POST form with JS confirm).
"Include deactivated" toggle via `?include_inactive=1` (the existing
`ListVehicles` store method already supports this).

**Users** — table: name, email, role badge, active badge, assigned-vehicle
count. Create form (name, email, password ≥ 8 chars, role select). Edit
form adds optional password change and an assignments section: current
vehicles with remove buttons and a select of active unassigned vehicles to
add. Deactivate with confirm. The mockup's fake "Last Seen" column is
dropped.

**Trips** — table: trip id, vehicle label, driver name, route id, GTFS trip
id, start/end times (local server TZ), status badge, duration. Filter bar:
status select, vehicle select, applied via GET query params. Pagination
with next/prev links (50/page). Row action: "View trail" → `/admin/map?trip_id=N`.

### 4.6 Store additions

New methods on `*Store` (each behind a narrow interface consumed by its
handler, matching existing convention):

- `CountVehicles(ctx, activeOnly bool) (int, error)`
- `CountUsersByRole(ctx, role string, activeOnly bool) (int, error)`
- `CountActiveTrips(ctx) (int, error)`
- `ListTrips(ctx, TripFilter) ([]TripSummary, error)` — joins users
  (driver name) and vehicles (label); filter: status, vehicleID,
  limit/offset; ordered by `start_time DESC`
- `GetTripSummary(ctx, id int64) (*TripSummary, error)`
- `ListLocationsByTrip(ctx, tripID int64) ([]LocationPoint, error)` —
  ordered by `recorded_at ASC`, capped at 10,000 points
- `ListActiveTripsByVehicle(ctx) (map[string]ActiveTripInfo, error)` — one
  query powering the live endpoint's driver/route join

No schema migrations are required; all data exists in current tables. An
index on `location_points (trip_id, recorded_at)` is added in a new
migration to make trail queries cheap.

### 4.7 Static assets: no CDNs

- Vendor Leaflet 1.9.4 (`leaflet.js`, `leaflet.css`, marker images) into
  `web/static/vendor/leaflet/`.
- Replace the `cdn.tailwindcss.com` runtime script with a Tailwind CSS file
  compiled once via the standalone Tailwind CLI from the templates and
  committed at `web/static/css/admin.css`. A `Makefile` target (`make css`)
  documents regeneration. If the CLI proves unavailable during
  implementation, fall back to a hand-written CSS file reproducing the
  used utility classes — the visual design is preserved either way.
- Drop Google Fonts; use a system font stack (`system-ui, -apple-system,
  Segoe UI, Roboto, ...`). The `display-font` class keeps a distinct weight
  treatment instead of a webfont.
- CARTO basemap tiles remain a runtime network dependency of the map page
  only (unavoidable for map imagery; documented).

### 4.8 Flag and rollout

`ADMIN_UI_ENABLED` now defaults to **true** (the UI is authenticated);
setting it to a false value disables registration of all `/admin` routes.
The startup warning about an unauthenticated demo UI is removed. The
README/development docs gain a short "Admin UI" section (URL, default flag,
how to create the first admin user via `seed_dev.sql` or the users API).

### 4.9 Login rate limiting

A small fixed-window per-IP limiter (10 attempts/minute) guards
`POST /admin/login` and `POST /api/v1/auth/login`, returning 429 (page:
"too many attempts, try again shortly"). Implementation mirrors the
existing `VehicleRateLimiter` style; client IP is taken from the direct
connection (`r.RemoteAddr`), not spoofable proxy headers, and documented
as such.

## 5. Component boundaries

- `admin_session.go` — cookie issue/clear/parse, `requireAdminPage`
  middleware, flash helpers. No knowledge of specific pages.
- `admin_page_handlers.go` (replaces the mock handlers in
  `admin_handlers.go`) — page rendering, form processing. Depends on store
  interfaces + tracker + templates.
- `admin_live_handlers.go` — `/api/v1/admin/vehicles/live`, trips list,
  trip locations JSON handlers.
- `store_admin_stats.go`, additions to `store_trips.go` — new queries.
- `web/templates/**`, `web/static/**` — templates and vendored assets.
- `auth.go` — gains the cookie fallback in `requireAuth` only.

Each unit is testable in isolation (httptest + fake stores for handlers,
`newTestStore` + `DATABASE_URL` for store methods, following existing
conventions).

## 6. Testing

- **Store tests** (Postgres-backed, skip without `DATABASE_URL`): each new
  method — counts, trip listing filters/pagination/order, trail ordering,
  active-trip join.
- **Handler tests** (httptest, fake stores): login form success/failure/
  non-admin/ratelimit; cookie attributes; logout; redirect-to-login on
  every protected page when unauthenticated/expired/tampered cookie;
  Bearer-wins-over-cookie and invalid-Bearer-does-not-fall-back in
  `requireAuth`; CSRF rejection of cross-origin form POST; each page
  renders real data (golden substrings, not full-page snapshots); form
  validation errors re-render with 422; PRG redirects; 404s.
- **Route wiring test** (`route_wiring_test.go` pattern): every new route
  present with correct middleware; `/admin/*` pages redirect when
  unauthenticated; JSON endpoints return 401 JSON.
- **End-to-end smoke** (manual, documented): docker-compose up → seed →
  log in → create vehicle/user → assign → simulate locations → vehicle on
  map → trip in history → trail renders → CSV downloads.

## 7. Risks

- **Tailwind compile step**: one-time; fallback to handwritten CSS is
  planned and acceptable.
- **Cookie fallback on the API** widens the browser-reachable surface;
  mitigated by `CrossOriginProtection`, `SameSite=Lax`, HttpOnly, and
  admin-role checks already on every admin endpoint.
- **Tracker/DB label mismatch** (a tracked vehicle deleted from the DB):
  live endpoint returns the vehicle with a `label` equal to its id rather
  than erroring.
