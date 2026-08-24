# Admin Web UI v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the unauthenticated, mock-data admin UI proof-of-concept with a session-authenticated admin interface backed by real store/tracker data, plus the new admin JSON endpoints it needs.

**Architecture:** Server-rendered Go templates with an HttpOnly session cookie carrying the existing HS256 JWT. Pages query the store/tracker directly in their handlers. A `newHandler` constructor composes the API mux, admin UI routes, `http.CrossOriginProtection` (CSRF), and request logging for both `main()` and tests. Vanilla JS drives the Leaflet live map and trip trails against two new admin JSON endpoints.

**Tech Stack:** Go 1.25 (stdlib `net/http`, `html/template`), pgx/sqlc/golang-migrate, Leaflet 1.9.4 (vendored), Tailwind CSS v4 standalone CLI (build-time only), testify.

**Spec:** `docs/superpowers/specs/2026-08-23-admin-web-ui-design.md` — read it before starting any task.

## Global Constraints

- All server code lives in package `main` at the repo root, one concern per file. Follow existing file/test naming (`foo.go` / `foo_test.go`).
- Store-backed tests: `require`/`assert` from testify, `newTestStore(t)` helper, and they skip without `DATABASE_URL`. Start the DB with `docker compose up -d db`, then run tests with `DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable' go test ./...`. Handler-only tests must pass with plain `go test ./...`.
- sqlc: queries in `db/query.sql`, regenerate with `make generate` (sqlc is at `/opt/homebrew/bin/sqlc`). Never hand-edit `db/*.sql.go`.
- Fixed-shape queries go through sqlc; dynamically-filtered queries are hand-written pgx in the store file (spec §4.8).
- Session cookie name: `vp_session`. Flash cookie name: `vp_flash`. Both HttpOnly, SameSite=Lax, Path=/.
- Every new `/api/v1/admin/*` route: `authMiddleware(adminMiddleware(...))`. Every `/admin/*` page except login: `requireAdminPage`.
- JSON errors use the existing `writeJSON(w, status, map[string]string{"error": ...})` shape.
- Migration numbering: the next migration is `000010` (the sequence intentionally skips 000007).
- Do not change driver-API behavior (`/api/v1/locations`, `/api/v1/trips/*`, `/api/v1/vehicles`, `/api/v1/auth/login` request/response shapes). The only login change is rejecting deactivated users (indistinguishable from wrong password) and rate limiting.
- Commit after each task with a conventional-commit message. Run `go vet ./...` before each commit.

---

### Task 1: users.active migration + store plumbing

**Files:**
- Create: `migrations/000010_add_user_active.up.sql`, `migrations/000010_add_user_active.down.sql`
- Modify: `db/query.sql` (add `active` to user queries; add `SetUserActive`, `CountUsersByRole`, `CountActiveUsersByRole`), `user.go`, `user_store.go`, `store_users.go`
- Test: `store_users_test.go`, `user_store_test.go` (whichever holds store user tests — check both; add where `newTestStore` user tests already live)

**Interfaces:**
- Produces: `User.Active bool`, `UserResponse.Active bool` (`json:"active"`), `(*Store) SetUserActive(ctx context.Context, id int64, active bool) error` (returns `ErrUserNotFound` when no row), `(*Store) CountUsersByRole(ctx context.Context, role string) (int, error)`, `(*Store) CountActiveUsersByRole(ctx context.Context, role string) (int, error)`.

- [ ] **Step 1: Write the migration**

`migrations/000010_add_user_active.up.sql`:
```sql
ALTER TABLE users ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;
```
`migrations/000010_add_user_active.down.sql`:
```sql
ALTER TABLE users DROP COLUMN active;
```

- [ ] **Step 2: Update sqlc queries**

In `db/query.sql`, add `active` to the SELECT/RETURNING column lists of `ListUsers`, `GetUserByID`, `CreateUser`, `UpdateUser`, and append:

```sql
-- name: SetUserActive :execrows
UPDATE users SET active = $2 WHERE id = $1;

-- name: CountUsersByRole :one
SELECT COUNT(*) FROM users WHERE role = $1;

-- name: CountActiveUsersByRole :one
SELECT COUNT(*) FROM users WHERE role = $1 AND active = true;
```

Run `make generate`.

- [ ] **Step 3: Write failing store tests**

In the file holding existing user store tests, add (adapting helper names to what exists there):

```go
func TestSetUserActive(t *testing.T) {
	store := newTestStore(t)
	u, err := store.CreateUser(context.Background(), "Deact Me", uniqueEmail(t), "password123", "driver")
	require.NoError(t, err)
	require.True(t, u.Active)

	require.NoError(t, store.SetUserActive(context.Background(), u.ID, false))
	got, err := store.GetUser(context.Background(), u.ID)
	require.NoError(t, err)
	assert.False(t, got.Active)

	require.NoError(t, store.SetUserActive(context.Background(), u.ID, true))
	got, err = store.GetUser(context.Background(), u.ID)
	require.NoError(t, err)
	assert.True(t, got.Active)

	assert.ErrorIs(t, store.SetUserActive(context.Background(), 999999999, false), ErrUserNotFound)
}

func TestGetUserByEmailIncludesActive(t *testing.T) {
	store := newTestStore(t)
	email := uniqueEmail(t)
	u, err := store.CreateUser(context.Background(), "Flag Check", email, "password123", "driver")
	require.NoError(t, err)
	require.NoError(t, store.SetUserActive(context.Background(), u.ID, false))

	fetched, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.False(t, fetched.Active)
}

func TestCountUsersByRole(t *testing.T) {
	store := newTestStore(t)
	u, err := store.CreateUser(context.Background(), "Count Me", uniqueEmail(t), "password123", "driver")
	require.NoError(t, err)

	total, err := store.CountUsersByRole(context.Background(), "driver")
	require.NoError(t, err)
	active, err := store.CountActiveUsersByRole(context.Background(), "driver")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.GreaterOrEqual(t, active, 1)

	require.NoError(t, store.SetUserActive(context.Background(), u.ID, false))
	active2, err := store.CountActiveUsersByRole(context.Background(), "driver")
	require.NoError(t, err)
	assert.Equal(t, active-1, active2)
}
```

If no `uniqueEmail(t)` helper exists, add one: `func uniqueEmail(t *testing.T) string { return fmt.Sprintf("u-%d-%s@test.com", time.Now().UnixNano(), t.Name()) }` (lowercase/sanitize `t.Name()` if it contains `/`). Reuse an existing pattern if the test files already generate unique emails another way.

- [ ] **Step 4: Run tests to verify they fail** — `DATABASE_URL=... go test ./... -run 'TestSetUserActive|TestGetUserByEmailIncludesActive|TestCountUsersByRole' -v` → compile errors / FAIL.

- [ ] **Step 5: Implement**

`user.go`: add `Active bool` to `User`. `user_store.go`: add `Active bool \`json:"active"\`` to `UserResponse`; populate `Active: row.Active` in `ListUsers`, `GetUser`, `CreateUser`, `UpdateUser`; add:

```go
// SetUserActive flips a user's active flag. Deactivated users cannot log in.
func (s *Store) SetUserActive(ctx context.Context, id int64, active bool) error {
	rows, err := s.queries.SetUserActive(ctx, db.SetUserActiveParams{ID: id, Active: active})
	if err != nil {
		return fmt.Errorf("set user active: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *Store) CountUsersByRole(ctx context.Context, role string) (int, error) {
	n, err := s.queries.CountUsersByRole(ctx, role)
	if err != nil {
		return 0, fmt.Errorf("count users by role: %w", err)
	}
	return int(n), nil
}

func (s *Store) CountActiveUsersByRole(ctx context.Context, role string) (int, error) {
	n, err := s.queries.CountActiveUsersByRole(ctx, role)
	if err != nil {
		return 0, fmt.Errorf("count active users by role: %w", err)
	}
	return int(n), nil
}
```

`store_users.go` (`GetUserByEmail`): add `active` to the SELECT list and `&u.Active` to the Scan, keeping column order aligned.

- [ ] **Step 6: Run the new tests → PASS; run full suite** `DATABASE_URL=... go test ./...` → PASS.

- [ ] **Step 7: Commit** — `git add -A && git commit -m "feat: add users.active flag with soft-deactivate store support"`

---

### Task 2: reject deactivated users at login

**Files:**
- Modify: `auth.go` (handleLogin)
- Test: `auth_test.go`

**Interfaces:**
- Consumes: `User.Active` from Task 1.
- Produces: `handleLogin` returns 401 `{"error": "invalid email or password"}` for inactive users.

- [ ] **Step 1: Write failing test** in `auth_test.go`, following its existing fake-`UserFetcher` pattern (there is one for the login tests — reuse it):

```go
func TestLoginRejectsDeactivatedUser(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcryptCost)
	require.NoError(t, err)
	fetcher := &fakeUserFetcher{user: &User{ID: 7, Email: "gone@test.com", PasswordHash: string(hash), Role: "driver", Active: false}}

	body := strings.NewReader(`{"email":"gone@test.com","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	handleLogin(fetcher, testSecret).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "invalid email or password")
}
```

Adapt `fakeUserFetcher` to whatever stub `auth_test.go` already defines (add an `Active: true` to existing fixtures so current tests keep passing).

- [ ] **Step 2: Run → FAIL** (currently 200).

- [ ] **Step 3: Implement** — in `handleLogin`, after the bcrypt compare succeeds, add:

```go
	if !user.Active {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		return
	}
```

(After bcrypt, not before, to keep timing identical to the wrong-password path.)

- [ ] **Step 4: Run auth tests → PASS.** `go test ./... -run TestLogin -v`
- [ ] **Step 5: Commit** — `git commit -am "fix: reject deactivated users at login"`

---

### Task 3: vehicle + count store additions

**Files:**
- Modify: `db/query.sql`, `store_vehicles.go`, `store_trips.go` (or a new `store_admin_stats.go` for counts)
- Test: `store_vehicles_test.go`, `store_trips_test.go`

**Interfaces:**
- Produces: `(*Store) UpdateVehicleInfo(ctx, id, label, agencyTag string) error` (ErrNoRows-wrapped when missing; never touches `active`), `(*Store) SetVehicleActive(ctx, id string, active bool) error`, `(*Store) CountActiveVehicles(ctx) (int, error)`, `(*Store) CountActiveTrips(ctx) (int, error)`.

- [ ] **Step 1: sqlc queries** — append to `db/query.sql`:

```sql
-- name: UpdateVehicleInfo :execrows
UPDATE vehicles SET label = $2, agency_tag = $3, updated_at = NOW() WHERE id = $1;

-- name: SetVehicleActive :execrows
UPDATE vehicles SET active = $2, updated_at = NOW() WHERE id = $1;

-- name: CountActiveVehicles :one
SELECT COUNT(*) FROM vehicles WHERE active = true;

-- name: CountActiveTrips :one
SELECT COUNT(*) FROM trips WHERE status = 'active';
```

Run `make generate`.

- [ ] **Step 2: Failing store tests** (in `store_vehicles_test.go`, reusing its existing unique-vehicle-id helpers/patterns):

```go
func TestUpdateVehicleInfoDoesNotReactivate(t *testing.T) {
	store := newTestStore(t)
	id := uniqueVehicleID(t) // reuse/create helper matching existing tests
	_, err := store.UpsertVehicle(context.Background(), id, "Old", "tag")
	require.NoError(t, err)
	require.NoError(t, store.DeactivateVehicle(context.Background(), id))

	require.NoError(t, store.UpdateVehicleInfo(context.Background(), id, "New Label", "newtag"))
	v, err := store.GetVehicle(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "New Label", v.Label)
	assert.False(t, v.Active, "editing must not reactivate a deactivated vehicle")

	err = store.UpdateVehicleInfo(context.Background(), "no-such-vehicle-xyz", "x", "y")
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestSetVehicleActive(t *testing.T) {
	store := newTestStore(t)
	id := uniqueVehicleID(t)
	_, err := store.UpsertVehicle(context.Background(), id, "Bus", "")
	require.NoError(t, err)

	require.NoError(t, store.SetVehicleActive(context.Background(), id, false))
	v, _ := store.GetVehicle(context.Background(), id)
	assert.False(t, v.Active)
	require.NoError(t, store.SetVehicleActive(context.Background(), id, true))
	v, _ = store.GetVehicle(context.Background(), id)
	assert.True(t, v.Active)
	assert.ErrorIs(t, store.SetVehicleActive(context.Background(), "no-such-vehicle-xyz", true), pgx.ErrNoRows)
}

func TestCountActiveVehiclesAndTrips(t *testing.T) {
	store := newTestStore(t)
	before, err := store.CountActiveVehicles(context.Background())
	require.NoError(t, err)
	id := uniqueVehicleID(t)
	_, err = store.UpsertVehicle(context.Background(), id, "Bus", "")
	require.NoError(t, err)
	after, err := store.CountActiveVehicles(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before+1, after)

	_, err = store.CountActiveTrips(context.Background())
	require.NoError(t, err) // exact value covered by trip tests; here just exercises the query
}
```

- [ ] **Step 3: Run → FAIL (compile).**
- [ ] **Step 4: Implement** in `store_vehicles.go` (counts may live in a new `store_admin_stats.go` together with Task 1's counts if you prefer one stats file — pick one and be consistent):

```go
// UpdateVehicleInfo updates label/agency tag WITHOUT touching the active flag,
// unlike UpsertVehicle which force-reactivates.
func (s *Store) UpdateVehicleInfo(ctx context.Context, id, label, agencyTag string) error {
	rows, err := s.queries.UpdateVehicleInfo(ctx, db.UpdateVehicleInfoParams{ID: id, Label: label, AgencyTag: agencyTag})
	if err != nil {
		return fmt.Errorf("update vehicle info: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("update vehicle info: %w", pgx.ErrNoRows)
	}
	return nil
}

// SetVehicleActive flips a vehicle's active flag (deactivate/reactivate).
func (s *Store) SetVehicleActive(ctx context.Context, id string, active bool) error {
	rows, err := s.queries.SetVehicleActive(ctx, db.SetVehicleActiveParams{ID: id, Active: active})
	if err != nil {
		return fmt.Errorf("set vehicle active: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("set vehicle active: %w", pgx.ErrNoRows)
	}
	return nil
}

func (s *Store) CountActiveVehicles(ctx context.Context) (int, error) {
	n, err := s.queries.CountActiveVehicles(ctx)
	if err != nil {
		return 0, fmt.Errorf("count active vehicles: %w", err)
	}
	return int(n), nil
}

func (s *Store) CountActiveTrips(ctx context.Context) (int, error) {
	n, err := s.queries.CountActiveTrips(ctx)
	if err != nil {
		return 0, fmt.Errorf("count active trips: %w", err)
	}
	return int(n), nil
}
```

- [ ] **Step 5: Run → PASS, full suite PASS.**
- [ ] **Step 6: Commit** — `git commit -am "feat: add vehicle activate/edit-without-reactivate and admin count queries"`

---

### Task 4: trips store — summaries, trails, active-trip join

**Files:**
- Modify: `db/query.sql`, `store_trips.go`
- Test: `store_trips_test.go`

**Interfaces:**
- Produces:

```go
type TripSummary struct {
	ID           int64      `json:"id"`
	VehicleID    string     `json:"vehicle_id"`
	VehicleLabel string     `json:"vehicle_label"`
	UserID       int64      `json:"user_id"`
	DriverName   string     `json:"driver_name"`
	RouteID      string     `json:"route_id"`
	GtfsTripID   string     `json:"gtfs_trip_id"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      *time.Time `json:"end_time,omitempty"`
	Status       string     `json:"status"`
}

type TripFilter struct {
	Status    string // "", "active", "completed"
	VehicleID string // "" = all
	Q         string // ILIKE substring on driver name, route_id, gtfs_trip_id
	Limit     int    // callers pass limit+1 to detect hasMore
	Offset    int
}

type ActiveTripInfo struct {
	TripID     int64
	RouteID    string
	GtfsTripID string
	UserID     int64
	DriverName string
}

func (s *Store) ListTrips(ctx context.Context, f TripFilter) ([]TripSummary, error)
func (s *Store) GetTripSummary(ctx context.Context, id int64) (*TripSummary, error) // ErrTripNotFound when missing
func (s *Store) ListTripLocations(ctx context.Context, tripID int64) ([]LocationPoint, error)
func (s *Store) ListActiveTripsByVehicle(ctx context.Context) (map[string]ActiveTripInfo, error)
var ErrTripNotFound = errors.New("trip not found")
```

- [ ] **Step 1: sqlc queries** — append to `db/query.sql`:

```sql
-- name: GetTripSummary :one
SELECT t.id, t.vehicle_id, v.label AS vehicle_label, t.user_id, u.name AS driver_name,
       t.route_id, t.gtfs_trip_id, t.start_time, t.end_time, t.status
FROM trips t
JOIN users u ON u.id = t.user_id
JOIN vehicles v ON v.id = t.vehicle_id
WHERE t.id = $1;

-- name: ListTripLocations :many
-- Trail derivation per spec §4.5: location_points.trip_id is a client string,
-- not trips.id, so trail points are matched by vehicle + driver + time window.
SELECT lp.latitude, lp.longitude, lp.bearing, lp.speed, lp.accuracy,
       lp.timestamp, lp.trip_id, lp.received_at
FROM location_points lp
JOIN trips t ON t.id = $1
WHERE lp.vehicle_id = t.vehicle_id
  AND lp.driver_id = t.user_id::text
  AND lp.received_at >= t.start_time
  AND lp.received_at <= COALESCE(t.end_time, NOW())
ORDER BY lp.received_at ASC
LIMIT 10000;

-- name: ListActiveTripsByVehicle :many
-- Schema guarantees one active trip per USER, not per vehicle; newest active
-- trip per vehicle is the defined tiebreak (spec §4.8).
SELECT DISTINCT ON (t.vehicle_id)
       t.vehicle_id, t.id, t.route_id, t.gtfs_trip_id, t.user_id, u.name AS driver_name
FROM trips t
JOIN users u ON u.id = t.user_id
WHERE t.status = 'active'
ORDER BY t.vehicle_id, t.start_time DESC;
```

Run `make generate`.

- [ ] **Step 2: Failing store tests** in `store_trips_test.go`. Use its existing helpers for creating users/vehicles/assignments (it has them for StartTrip tests — reuse). Cover:

```go
func TestListTripsFiltersAndOrder(t *testing.T) {
	store := newTestStore(t)
	// create driver+vehicle+assignment, StartTrip, EndTrip → one completed trip
	// create second driver+vehicle+assignment, StartTrip → one active trip
	// (follow the existing setup pattern in this file)
	...
	all, err := store.ListTrips(context.Background(), TripFilter{Limit: 200})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 2)
	// newest first
	for i := 1; i < len(all); i++ {
		assert.True(t, !all[i-1].StartTime.Before(all[i].StartTime))
	}

	active, err := store.ListTrips(context.Background(), TripFilter{Status: "active", Limit: 200})
	require.NoError(t, err)
	for _, tr := range active {
		assert.Equal(t, "active", tr.Status)
	}

	byVehicle, err := store.ListTrips(context.Background(), TripFilter{VehicleID: vehicleID1, Limit: 200})
	require.NoError(t, err)
	for _, tr := range byVehicle {
		assert.Equal(t, vehicleID1, tr.VehicleID)
	}

	byQ, err := store.ListTrips(context.Background(), TripFilter{Q: driver1NameFragment, Limit: 200})
	require.NoError(t, err)
	assert.NotEmpty(t, byQ)

	page1, err := store.ListTrips(context.Background(), TripFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, page1, 1)
	page2, err := store.ListTrips(context.Background(), TripFilter{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.NotEqual(t, page1[0].ID, page2[0].ID)
}

func TestGetTripSummary(t *testing.T) {
	// start a trip; GetTripSummary returns matching labels/names;
	// unknown id → ErrTripNotFound
}

func TestListTripLocationsWindow(t *testing.T) {
	store := newTestStore(t)
	// driver A + vehicle V assigned; StartTrip → trip
	// SaveLocation with DriverID = strconv.FormatInt(driverA.ID, 10), VehicleID = V  → IN window
	// SaveLocation with DriverID = other user's id, VehicleID = V                    → excluded
	// EndTrip; SaveLocation for driver A after end                                   → excluded
	pts, err := store.ListTripLocations(context.Background(), tripID)
	require.NoError(t, err)
	require.Len(t, pts, 1)
}

func TestListActiveTripsByVehicleTiebreak(t *testing.T) {
	store := newTestStore(t)
	// two drivers assigned to the SAME vehicle; both StartTrip (allowed by schema)
	m, err := store.ListActiveTripsByVehicle(context.Background())
	require.NoError(t, err)
	info, ok := m[sharedVehicleID]
	require.True(t, ok)
	assert.Equal(t, secondTrip.ID, info.TripID, "newest active trip wins")
}
```

Write these fully (no `...` in the committed test) using the concrete setup helpers present in `store_trips_test.go`.

- [ ] **Step 3: Run → FAIL.**
- [ ] **Step 4: Implement** in `store_trips.go`. `ListTrips` is hand-written pgx (dynamic filter):

```go
var ErrTripNotFound = errors.New("trip not found")

// ListTrips returns trip summaries newest-first with optional filters.
// Dynamic WHERE clauses make this a hand-written query rather than sqlc.
func (s *Store) ListTrips(ctx context.Context, f TripFilter) ([]TripSummary, error) {
	query := `
		SELECT t.id, t.vehicle_id, v.label, t.user_id, u.name,
		       t.route_id, t.gtfs_trip_id, t.start_time, t.end_time, t.status
		FROM trips t
		JOIN users u ON u.id = t.user_id
		JOIN vehicles v ON v.id = t.vehicle_id`
	var conds []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if f.Status != "" {
		conds = append(conds, "t.status = "+arg(f.Status))
	}
	if f.VehicleID != "" {
		conds = append(conds, "t.vehicle_id = "+arg(f.VehicleID))
	}
	if f.Q != "" {
		p := arg("%" + f.Q + "%")
		conds = append(conds, fmt.Sprintf("(u.name ILIKE %s OR t.route_id ILIKE %s OR t.gtfs_trip_id ILIKE %s)", p, p, p))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY t.start_time DESC LIMIT " + arg(f.Limit) + " OFFSET " + arg(f.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trips: %w", err)
	}
	defer rows.Close()

	var trips []TripSummary
	for rows.Next() {
		var tr TripSummary
		var endTime pgtype.Timestamptz
		if err := rows.Scan(&tr.ID, &tr.VehicleID, &tr.VehicleLabel, &tr.UserID, &tr.DriverName,
			&tr.RouteID, &tr.GtfsTripID, &tr.StartTime, &endTime, &tr.Status); err != nil {
			return nil, fmt.Errorf("scan trip: %w", err)
		}
		if endTime.Valid {
			t := endTime.Time
			tr.EndTime = &t
		}
		trips = append(trips, tr)
	}
	return trips, rows.Err()
}
```

`GetTripSummary`, `ListTripLocations`, `ListActiveTripsByVehicle` wrap the sqlc-generated calls, mapping row types to `TripSummary`/`LocationPoint`/`map[string]ActiveTripInfo` the same way existing methods do (`pgtype.Float8` → `*float64` etc.; see `GetLocationHistory` in `location_history_store.go` for the pattern). `GetTripSummary` maps `pgx.ErrNoRows` to `ErrTripNotFound`.

- [ ] **Step 5: Run → PASS, full suite PASS.**
- [ ] **Step 6: Commit** — `git commit -am "feat: add trip summaries, trail query, and active-trip-by-vehicle store methods"`

---

### Task 5: proxy helpers + login rate limiter

**Files:**
- Create: `proxy.go`, `ratelimit_login.go`
- Test: `proxy_test.go`, `ratelimit_login_test.go`

**Interfaces:**
- Produces:

```go
func clientIP(r *http.Request, trustProxy bool) string       // proxy.go
func requestIsSecure(r *http.Request, trustProxy bool) bool  // proxy.go
type LoginRateLimiter struct{ ... }
func NewLoginRateLimiter() *LoginRateLimiter
func (l *LoginRateLimiter) Allow(ip, email string) bool // false when either dimension exceeded OR at capacity (fail closed)
func (l *LoginRateLimiter) Stop()
```

- [ ] **Step 1: Failing tests**

`proxy_test.go`:
```go
func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 192.0.2.7")

	assert.Equal(t, "203.0.113.9", clientIP(req, false), "untrusted: RemoteAddr host wins")
	assert.Equal(t, "192.0.2.7", clientIP(req, true), "trusted: rightmost XFF hop")

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.9:4567"
	assert.Equal(t, "203.0.113.9", clientIP(req2, true), "trusted but no header: RemoteAddr")
}

func TestRequestIsSecure(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, requestIsSecure(req, false))
	req.Header.Set("X-Forwarded-Proto", "https")
	assert.False(t, requestIsSecure(req, false), "untrusted header ignored")
	assert.True(t, requestIsSecure(req, true))
	reqTLS := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	assert.True(t, requestIsSecure(reqTLS, false), "real TLS always secure")
}
```

`ratelimit_login_test.go`:
```go
func TestLoginRateLimiterPerIP(t *testing.T) {
	l := NewLoginRateLimiter()
	defer l.Stop()
	for i := 0; i < 10; i++ {
		assert.True(t, l.Allow("1.2.3.4", fmt.Sprintf("u%d@test.com", i)), "attempt %d", i)
	}
	assert.False(t, l.Allow("1.2.3.4", "another@test.com"), "11th attempt from same IP blocked")
	assert.True(t, l.Allow("5.6.7.8", "fresh@test.com"), "other IP unaffected")
}

func TestLoginRateLimiterPerEmail(t *testing.T) {
	l := NewLoginRateLimiter()
	defer l.Stop()
	for i := 0; i < 5; i++ {
		assert.True(t, l.Allow(fmt.Sprintf("10.0.0.%d", i), "target@test.com"))
	}
	assert.False(t, l.Allow("10.0.0.99", "target@test.com"), "6th attempt on same email blocked across IPs")
}
```

- [ ] **Step 2: Run → FAIL (compile).**
- [ ] **Step 3: Implement**

`proxy.go`:
```go
package main

import (
	"net"
	"net/http"
	"strings"
)

// clientIP extracts the caller's IP. With trustProxy (TRUST_PROXY_HEADERS=true)
// the rightmost X-Forwarded-For hop is used — the value appended by our own
// reverse proxy. Without it, only the direct connection is trusted (spec §4.10).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestIsSecure reports whether the request arrived over HTTPS, honoring
// X-Forwarded-Proto only when proxy headers are trusted.
func requestIsSecure(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	return trustProxy && r.Header.Get("X-Forwarded-Proto") == "https"
}
```

`ratelimit_login.go` — fixed-window, dual-dimension, fail-closed:
```go
package main

import (
	"log/slog"
	"sync"
	"time"
)

const (
	loginIPLimit      = 10
	loginEmailLimit   = 5
	loginWindow       = time.Minute
	maxTrackedLogins  = 10_000
)

type loginWindowEntry struct {
	count       int
	windowStart time.Time
}

// LoginRateLimiter guards login endpoints with per-IP and per-email fixed
// windows. Unlike VehicleRateLimiter it FAILS CLOSED at capacity — an auth
// endpoint must not become unlimited under memory pressure (spec §4.11).
type LoginRateLimiter struct {
	mu      sync.Mutex
	byIP    map[string]*loginWindowEntry
	byEmail map[string]*loginWindowEntry
	stop    chan struct{}
	once    sync.Once
}

func NewLoginRateLimiter() *LoginRateLimiter {
	l := &LoginRateLimiter{
		byIP:    make(map[string]*loginWindowEntry),
		byEmail: make(map[string]*loginWindowEntry),
		stop:    make(chan struct{}),
	}
	go l.cleanup()
	return l
}

func (l *LoginRateLimiter) Stop() { l.once.Do(func() { close(l.stop) }) }

func (l *LoginRateLimiter) Allow(ip, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	okIP := allowInWindow(l.byIP, ip, loginIPLimit, now)
	okEmail := allowInWindow(l.byEmail, email, loginEmailLimit, now)
	return okIP && okEmail
}

func allowInWindow(m map[string]*loginWindowEntry, key string, limit int, now time.Time) bool {
	e, ok := m[key]
	if !ok {
		if len(m) >= maxTrackedLogins {
			slog.Warn("login rate limiter at capacity, failing closed", "capacity", maxTrackedLogins)
			return false
		}
		m[key] = &loginWindowEntry{count: 1, windowStart: now}
		return true
	}
	if now.Sub(e.windowStart) >= loginWindow {
		e.count = 1
		e.windowStart = now
		return true
	}
	e.count++
	return e.count <= limit
}

func (l *LoginRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-2 * loginWindow)
			l.mu.Lock()
			for k, e := range l.byIP {
				if e.windowStart.Before(cutoff) {
					delete(l.byIP, k)
				}
			}
			for k, e := range l.byEmail {
				if e.windowStart.Before(cutoff) {
					delete(l.byEmail, k)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}
```

Note: counting both dimensions on every call means a blocked attempt still consumes window budget; that is intended (attempts, not successes, are limited).

- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -am "feat: add proxy-aware client IP/secure helpers and fail-closed login rate limiter"`

---

### Task 6: requireAuth cookie fallback

**Files:**
- Modify: `auth.go`
- Test: `auth_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `requireAuth` accepts the JWT from cookie `vp_session` **only when the `Authorization` header is entirely absent**. Constant `sessionCookieName = "vp_session"` defined in `auth.go` (Task 7's session helpers reuse it).

- [ ] **Step 1: Failing tests** in `auth_test.go` (reuse `testSecret` and a protected probe handler like existing requireAuth tests do):

```go
func TestRequireAuthCookieFallback(t *testing.T) {
	token, err := generateJWT(&User{ID: 3, Email: "admin@test.com", Role: "admin", Active: true}, testSecret)
	require.NoError(t, err)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireAuth(testSecret)(next)

	t.Run("cookie only → 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
	t.Run("invalid bearer + valid cookie → 401, no fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer garbage")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("malformed header + valid cookie → 401, no fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic abc")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("bad cookie only → 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "garbage"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
```

- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** — in `auth.go` add `const sessionCookieName = "vp_session"`. In `requireAuth`, replace the header-only extraction:

```go
			authHeader := r.Header.Get("Authorization")
			var tokenString string
			switch {
			case authHeader == "":
				// Cookie fallback for the admin UI's browser session
				// (spec §4.2). Applies ONLY when the header is entirely
				// absent — a present-but-bad header never falls back.
				c, err := r.Cookie(sessionCookieName)
				if err != nil || c.Value == "" {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
					return
				}
				tokenString = c.Value
			case strings.HasPrefix(authHeader, "Bearer "):
				tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			default:
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
				return
			}
```

The rest of the parse/validation is unchanged.

- [ ] **Step 4: Run auth tests + full suite → PASS.**
- [ ] **Step 5: Commit** — `git commit -am "feat: accept session cookie in requireAuth when Authorization header is absent"`

---

### Task 7: session layer — cookies, flash, requireAdminPage

**Files:**
- Create: `admin_session.go`
- Test: `admin_session_test.go`

**Interfaces:**
- Consumes: `sessionCookieName`, `generateJWT`, `clientIP`/`requestIsSecure` (Task 5).
- Produces:

```go
const flashCookieName = "vp_flash"
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, trustProxy bool) // 24h Max-Age, HttpOnly, Lax, Secure per requestIsSecure
func clearSessionCookie(w http.ResponseWriter)
func adminClaimsFromCookie(r *http.Request, secret []byte) (jwt.MapClaims, bool) // valid cookie JWT with role==admin
func requireAdminPage(secret []byte) func(http.Handler) http.Handler            // 303 → /admin/login on failure
func setFlash(w http.ResponseWriter, code string)   // HttpOnly, 60s Max-Age
func takeFlash(w http.ResponseWriter, r *http.Request) string // returns mapped message ("" if none/unknown) and clears cookie
var flashMessages = map[string]string{ ... }
```

- [ ] **Step 1: Failing tests** — `admin_session_test.go`:

```go
func TestSetSessionCookieAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	setSessionCookie(w, req, "tok123", false)
	res := w.Result()
	require.Len(t, res.Cookies(), 1)
	c := res.Cookies()[0]
	assert.Equal(t, sessionCookieName, c.Name)
	assert.Equal(t, "tok123", c.Value)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.Equal(t, 24*60*60, c.MaxAge)
	assert.False(t, c.Secure, "plain HTTP without trusted proxy")

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	req2.Header.Set("X-Forwarded-Proto", "https")
	setSessionCookie(w2, req2, "tok123", true)
	assert.True(t, w2.Result().Cookies()[0].Secure, "trusted proxy + https → Secure")
}

func TestRequireAdminPage(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireAdminPage(testSecret)(next)

	cases := []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{
		{"no cookie", nil, http.StatusSeeOther},
		{"garbage cookie", &http.Cookie{Name: sessionCookieName, Value: "garbage"}, http.StatusSeeOther},
		{"driver role", cookieFor(t, "driver"), http.StatusSeeOther},
		{"admin role", cookieFor(t, "admin"), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
			if tc.want == http.StatusSeeOther {
				assert.Equal(t, "/admin/login", w.Header().Get("Location"))
			}
		})
	}
}

func cookieFor(t *testing.T, role string) *http.Cookie {
	t.Helper()
	tok, err := generateJWT(&User{ID: 9, Email: role + "@test.com", Role: role, Active: true}, testSecret)
	require.NoError(t, err)
	return &http.Cookie{Name: sessionCookieName, Value: tok}
}

func TestFlashRoundTrip(t *testing.T) {
	w := httptest.NewRecorder()
	setFlash(w, "vehicle_created")
	c := w.Result().Cookies()[0]
	assert.Equal(t, flashCookieName, c.Name)
	assert.True(t, c.HttpOnly)

	req := httptest.NewRequest(http.MethodGet, "/admin/vehicles", nil)
	req.AddCookie(c)
	w2 := httptest.NewRecorder()
	msg := takeFlash(w2, req)
	assert.Equal(t, "Vehicle created.", msg)
	// clearing set-cookie present
	require.NotEmpty(t, w2.Result().Cookies())
	assert.Equal(t, -1, w2.Result().Cookies()[0].MaxAge)

	// unknown code renders nothing
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.AddCookie(&http.Cookie{Name: flashCookieName, Value: "<script>x</script>"})
	assert.Equal(t, "", takeFlash(httptest.NewRecorder(), req3))
}
```

- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `admin_session.go`:

```go
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const flashCookieName = "vp_flash"

// flashMessages maps opaque flash codes to the fixed strings the layout
// renders. Cookie values are attacker-writable, so free text is never
// rendered — unknown codes yield nothing (spec §4.6).
var flashMessages = map[string]string{
	"vehicle_created":     "Vehicle created.",
	"vehicle_updated":     "Vehicle updated.",
	"vehicle_deactivated": "Vehicle deactivated.",
	"vehicle_activated":   "Vehicle reactivated.",
	"user_created":        "User created.",
	"user_updated":        "User updated.",
	"user_deactivated":    "User deactivated.",
	"user_activated":      "User reactivated.",
	"vehicle_assigned":    "Vehicle assigned.",
	"vehicle_unassigned":  "Vehicle unassigned.",
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, trustProxy bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsSecure(r, trustProxy),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// adminClaimsFromCookie validates the session cookie's JWT and requires the
// admin role. It mirrors requireAuth's validation exactly (HS256, issuer).
func adminClaimsFromCookie(r *http.Request, secret []byte) (jwt.MapClaims, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	token, err := jwt.Parse(c.Value, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("vehicle-positions-api"))
	if err != nil || !token.Valid {
		return nil, false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, false
	}
	if role, _ := claims["role"].(string); role != "admin" {
		return nil, false
	}
	return claims, true
}

// requireAdminPage guards HTML admin pages: unauthenticated or non-admin
// visitors are redirected to the login page (303) rather than given JSON.
func requireAdminPage(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := adminClaimsFromCookie(r, secret)
			if !ok {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			ctx := contextWithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func setFlash(w http.ResponseWriter, code string) {
	http.SetCookie(w, &http.Cookie{
		Name: flashCookieName, Value: code, Path: "/", MaxAge: 60,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// takeFlash reads, clears, and resolves the flash cookie to its message.
func takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: flashCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	msg, ok := flashMessages[c.Value]
	if !ok {
		slog.Debug("unknown flash code ignored", "code", c.Value)
		return ""
	}
	return msg
}
```

Add to `auth.go` a tiny helper so both middlewares share claim wiring:
```go
func contextWithClaims(ctx context.Context, claims jwt.MapClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}
```
and use it in `requireAuth` too (replacing the inline `context.WithValue`).

- [ ] **Step 4: Run → PASS, full suite PASS.**
- [ ] **Step 5: Commit** — `git commit -am "feat: add admin session cookie layer, flash messages, and requireAdminPage middleware"`

---

### Task 8: admin UI restructure — injected deps, login/logout, signup removal

**Files:**
- Create: `admin_page_handlers.go`
- Modify: `admin_handlers.go` (shrinks to template loading + `adminUIEnabled`), `web/templates/views/login.html` (rewrite as form-POST login only), `web/templates/layout/header.html` (add logout button + flash slot), `web/templates/layout/base.html` (flash render)
- Delete: signup route/mode remnants
- Test: `admin_handlers_test.go` (rework), new tests in `admin_page_handlers_test.go`

**Interfaces:**
- Consumes: Tasks 5–7 helpers; `UserFetcher`; `LoginRateLimiter`.
- Produces:

```go
type adminUIConfig struct {
	enabled    bool
	trustProxy bool
}

// adminUI owns the parsed templates and dependencies for all admin pages.
type adminUI struct {
	tmpl         *embeddedTemplates
	users        UserFetcher
	jwtSecret    []byte
	loginLimiter *LoginRateLimiter
	cfg          adminUIConfig
	// page-data deps grow in later tasks (tracker, stats, trips, vehicles...)
}

func registerAdminUI(mux *http.ServeMux, ui *adminUI) // registers /admin routes with requireAdminPage
func newAdminUI(...) (*adminUI, error)                 // loads templates, wires deps
```

The package-level `templates` global is deleted; `render`/`renderAdmin`/`renderPublic` become methods on `adminUI` (same bodies, `ui.tmpl` instead of the global).

- [ ] **Step 1: Failing tests** — in `admin_page_handlers_test.go`:

```go
func newTestAdminUI(t *testing.T) *adminUI {
	t.Helper()
	ui, err := newAdminUI(&noopStore{}, testSecret, NewLoginRateLimiter(), adminUIConfig{enabled: true})
	require.NoError(t, err)
	t.Cleanup(ui.loginLimiter.Stop)
	return ui
}

func TestAdminLoginPageRenders(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `method="post"`)
	assert.Contains(t, w.Body.String(), `action="/admin/login"`)
	assert.NotContains(t, w.Body.String(), "signup")
}

func TestAdminLoginFlow(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcryptCost)
	admin := &User{ID: 1, Email: "boss@test.com", PasswordHash: string(hash), Role: "admin", Active: true}
	driver := &User{ID: 2, Email: "drv@test.com", PasswordHash: string(hash), Role: "driver", Active: true}
	ui := newTestAdminUI(t)
	ui.users = &fakeUserFetcher{users: map[string]*User{admin.Email: admin, driver.Email: driver}}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	post := func(email, pw string) *httptest.ResponseRecorder {
		form := url.Values{"email": {email}, "password": {pw}}
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("success sets cookie and redirects", func(t *testing.T) {
		w := post(admin.Email, "password123")
		assert.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/dashboard", w.Header().Get("Location"))
		require.NotEmpty(t, w.Result().Cookies())
		assert.Equal(t, sessionCookieName, w.Result().Cookies()[0].Name)
	})
	t.Run("wrong password re-renders 401", func(t *testing.T) {
		w := post(admin.Email, "nope")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid email or password")
	})
	t.Run("driver role gets 403 admin-required", func(t *testing.T) {
		w := post(driver.Email, "password123")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "Admin access required")
	})
	t.Run("rate limited after repeated attempts", func(t *testing.T) {
		var last *httptest.ResponseRecorder
		for i := 0; i < 12; i++ {
			last = post("x@test.com", "nope")
		}
		assert.Equal(t, http.StatusTooManyRequests, last.Code)
	})
}

func TestAdminLogout(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/login", w.Header().Get("Location"))
	require.NotEmpty(t, w.Result().Cookies())
	assert.Equal(t, -1, w.Result().Cookies()[0].MaxAge)
}

func TestAdminPagesRedirectWithoutSession(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	for _, path := range []string{"/admin", "/admin/dashboard", "/admin/map", "/admin/vehicles", "/admin/users", "/admin/trips"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusSeeOther, w.Code, path)
		assert.Equal(t, "/admin/login", w.Header().Get("Location"), path)
	}
}
```

`fakeUserFetcher` here: `type fakeUserFetcher struct{ users map[string]*User }` with `GetUserByEmail` returning `ErrUserNotFound` when missing (merge with the Task 2 fake if names collide — one fake, both shapes).

- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement.**

`admin_handlers.go` keeps: `adminUIEnabled()` (unchanged for now; Task 9 flips the default), `embeddedTemplates`, `loadTemplates()` (drop the signup entry: the public map still holds only `login.html`), `render` (unchanged logic but taking the template set as a parameter — no global).

`admin_page_handlers.go` — the structure plus login/logout/redirect handlers:

```go
package main

import (
	"log/slog"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

type adminUIConfig struct {
	enabled    bool
	trustProxy bool
}

type adminUI struct {
	tmpl         *embeddedTemplates
	users        UserFetcher
	jwtSecret    []byte
	loginLimiter *LoginRateLimiter
	cfg          adminUIConfig
}

func newAdminUI(users UserFetcher, jwtSecret []byte, limiter *LoginRateLimiter, cfg adminUIConfig) (*adminUI, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &adminUI{tmpl: tmpl, users: users, jwtSecret: jwtSecret, loginLimiter: limiter, cfg: cfg}, nil
}

func registerAdminUI(mux *http.ServeMux, ui *adminUI) {
	protect := requireAdminPage(ui.jwtSecret)

	mux.HandleFunc("GET /admin/login", ui.loginPage)
	mux.HandleFunc("POST /admin/login", ui.loginSubmit)
	mux.HandleFunc("POST /admin/logout", ui.logout)
	mux.HandleFunc("GET /admin", ui.rootRedirect)
	mux.HandleFunc("GET /admin/{$}", ui.rootRedirect)
	mux.Handle("GET /admin/dashboard", protect(http.HandlerFunc(ui.dashboardPage)))
	mux.Handle("GET /admin/map", protect(http.HandlerFunc(ui.mapPage)))
	mux.Handle("GET /admin/vehicles", protect(http.HandlerFunc(ui.vehiclesPage)))
	mux.Handle("GET /admin/users", protect(http.HandlerFunc(ui.usersPage)))
	mux.Handle("GET /admin/trips", protect(http.HandlerFunc(ui.tripsPage)))
	// CRUD form routes are added by later tasks.
}

func (ui *adminUI) rootRedirect(w http.ResponseWriter, r *http.Request) {
	if _, ok := adminClaimsFromCookie(r, ui.jwtSecret); ok {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (ui *adminUI) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := adminClaimsFromCookie(r, ui.jwtSecret); ok {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}
	ui.renderLogin(w, http.StatusOK, "", "")
}

func (ui *adminUI) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ui.renderLogin(w, http.StatusBadRequest, "Invalid form submission.", "")
		return
	}
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	if !ui.loginLimiter.Allow(clientIP(r, ui.cfg.trustProxy), email) {
		ui.renderLogin(w, http.StatusTooManyRequests, "Too many attempts, try again shortly.", email)
		return
	}
	if email == "" || password == "" {
		ui.renderLogin(w, http.StatusUnprocessableEntity, "Email and password are required.", email)
		return
	}
	user, err := ui.users.GetUserByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			ui.renderLogin(w, http.StatusUnauthorized, "Invalid email or password.", email)
			return
		}
		slog.Error("admin login: database error", "error", err)
		ui.renderLogin(w, http.StatusInternalServerError, "Something went wrong. Try again.", email)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		ui.renderLogin(w, http.StatusUnauthorized, "Invalid email or password.", email)
		return
	}
	if !user.Active {
		ui.renderLogin(w, http.StatusUnauthorized, "Invalid email or password.", email)
		return
	}
	if user.Role != "admin" {
		ui.renderLogin(w, http.StatusForbidden, "Admin access required.", email)
		return
	}
	token, err := generateJWT(user, ui.jwtSecret)
	if err != nil {
		slog.Error("admin login: token generation failed", "error", err)
		ui.renderLogin(w, http.StatusInternalServerError, "Something went wrong. Try again.", email)
		return
	}
	setSessionCookie(w, r, token, ui.cfg.trustProxy)
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

func (ui *adminUI) renderLogin(w http.ResponseWriter, status int, errMsg, email string) {
	w.WriteHeader(status)
	// render() writes the body; status must be set first for non-200s.
	renderInto(w, ui.tmpl.public, "login.html", "login.html", map[string]interface{}{
		"Title": "Sign In", "Error": errMsg, "Email": email,
	})
}

func (ui *adminUI) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}
```

Note on rendering with non-200 status: the existing `render` calls `http.Error` on failure after possibly not writing a header. Refactor `render` into `renderInto(w, set, view, root, data)` that buffers, then writes the body WITHOUT calling `WriteHeader` itself (caller sets status first; default 200 applies otherwise), and on template failure logs + writes a plain 500 only if the header isn't committed — keep it simple: buffer first, and only `WriteHeader(500)+error body` when the buffer fails AND status wasn't already set. Simplest correct shape:

```go
func renderInto(w http.ResponseWriter, set map[string]*template.Template, view, rootName string, data map[string]interface{}) {
	tmpl, ok := set[path.Base(view)]
	if !ok {
		slog.Error("template render failed", "view", view, "error", "no such template")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, rootName, data); err != nil {
		slog.Error("template render failed", "view", view, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := buf.WriteTo(w); err != nil {
		slog.Error("template response write failed", "view", view, "error", err)
	}
}
```

(When the caller pre-sets a non-200 status and the template then fails, `http.Error`'s WriteHeader is a no-op with a log line — acceptable.) For error-status page renders, call `w.WriteHeader(status)` before `renderInto` only when status != 200. The five page methods (`dashboardPage`, `mapPage`, `vehiclesPage`, `usersPage`, `tripsPage`) keep this task's scope: same mock data bodies as today, but as `adminUI` methods calling `ui.renderAdmin(...)`, where:

```go
func (ui *adminUI) renderAdmin(w http.ResponseWriter, r *http.Request, view string, data map[string]interface{}) {
	data["Flash"] = takeFlash(w, r)
	renderInto(w, ui.tmpl.admin, view, "base.html", data)
}
```

`web/templates/views/login.html` — replace entirely with a standalone form page (keep the existing visual style/classes from the old file where convenient):

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}} — Transit Tracker</title>
<link rel="stylesheet" href="/static/css/admin.css">
</head>
<body class="login-body">
<main class="login-card">
  <h1>Transit Tracker</h1>
  <p class="login-sub">Fleet operations sign in</p>
  {{if .Error}}<div class="alert alert-error" role="alert">{{.Error}}</div>{{end}}
  <form method="post" action="/admin/login">
    <label for="email">Email</label>
    <input id="email" name="email" type="email" required autocomplete="username" value="{{.Email}}">
    <label for="password">Password</label>
    <input id="password" name="password" type="password" required autocomplete="current-password">
    <button type="submit">Sign in</button>
  </form>
</main>
</body>
</html>
```

(Styling: reuse the old login.html's Tailwind classes for these elements if they translate cleanly; the semantic structure above is the contract. Until Task 10 lands `admin.css`, the CDN link may remain in this file temporarily — Task 10 removes every CDN reference.)

`web/templates/layout/base.html`: inside `<main>` before `{{template "content" .}}` add:
```html
{{if .Flash}}<div class="glass-panel mx-5 mt-4 rounded-xl border border-emerald-200 bg-emerald-50/80 px-4 py-3 text-sm font-semibold text-emerald-800" role="status">{{.Flash}}</div>{{end}}
```

`web/templates/layout/header.html`: replace the static "Admin" pill with:
```html
<form method="post" action="/admin/logout">
  <button type="submit" class="inline-flex items-center gap-2 rounded-full border border-slate-200 bg-white/75 px-3 py-1.5 text-xs font-semibold text-slate-700 shadow-sm hover:bg-slate-50">Sign out</button>
</form>
```

Rework `admin_handlers_test.go`: its existing tests exercised unauthenticated page renders; update them to construct an `adminUI` via `newTestAdminUI(t)` and include an admin session cookie (use `cookieFor(t, "admin")` from Task 7's test file) where they hit protected pages. Delete signup-related tests.

- [ ] **Step 4: Run → PASS, full suite PASS.**
- [ ] **Step 5: Commit** — `git commit -am "feat: session-authenticated admin UI shell with form login, logout, and flash"`

---

### Task 9: newHandler composition, CSRF, bootstrap, flag default

**Files:**
- Modify: `main.go`, `admin_handlers.go` (`adminUIEnabled` default true), `seed_dev.sql`, `route_wiring_test.go` (noopStore additions), `handlers.go` (only if `newMux` signature grows — it should not)
- Create: `bootstrap.go`, `bootstrap_test.go`, `handler_composition_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:

```go
func newHandler(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter,
	loginLimiter *LoginRateLimiter, jwtSecret []byte, startTime time.Time,
	cfg adminUIConfig) (http.Handler, error)
// bootstrap.go:
func bootstrapAdmin(ctx context.Context, store adminBootstrapStore, email, password string) error
type adminBootstrapStore interface {
	CountUsersByRole(ctx context.Context, role string) (int, error)
	CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error)
}
```

`appStore` gains: `SetUserActive`, `CountUsersByRole`, `CountActiveUsersByRole`, `UpdateVehicleInfo`, `SetVehicleActive`, `CountActiveVehicles`, `CountActiveTrips`, `ListTrips`, `GetTripSummary`, `ListTripLocations`, `ListActiveTripsByVehicle` — declare matching narrow interfaces next to each store method group and embed them in `appStore`; extend `noopStore` with zero-value stubs for all of them.

- [ ] **Step 1: Failing tests** — `handler_composition_test.go`:

```go
func newTestHandler(t *testing.T, enabled bool) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, testSecret, time.Now(), adminUIConfig{enabled: enabled})
	require.NoError(t, err)
	return h
}

func TestNewHandlerServesAPIAndAdmin(t *testing.T) {
	h := newTestHandler(t, true)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code, "admin page redirects to login when unauthenticated")
}

func TestNewHandlerAdminDisabled(t *testing.T) {
	h := newTestHandler(t, false)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNewHandlerCSRFRejectsCrossOriginPost(t *testing.T) {
	h := newTestHandler(t, true)
	form := url.Values{"email": {"a@b.c"}, "password": {"x"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code, "cross-site browser POST must be rejected")
}

func TestNewHandlerCSRFAllowsHeaderlessClients(t *testing.T) {
	h := newTestHandler(t, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.c","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusForbidden, w.Code, "non-browser clients (no Sec-Fetch-Site/Origin) pass CSRF")
}
```

`bootstrap_test.go` (store-backed):

```go
func TestBootstrapAdmin(t *testing.T) {
	store := newTestStore(t)
	email := uniqueEmail(t)
	require.NoError(t, bootstrapAdmin(context.Background(), store, email, "supersecret123"))
	u, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.Equal(t, "admin", u.Role)

	// second call: an admin now exists → no-op, no duplicate
	err = bootstrapAdmin(context.Background(), store, uniqueEmail(t), "supersecret123")
	require.NoError(t, err)
}
```

(Precondition: the shared dev DB may already contain admins, making the first assertion unreliable — instead structure the test with a fake `adminBootstrapStore` for the "creates when zero" and "skips when nonzero" branches, plus one real-store smoke call. Write the fake-based version as primary.)

- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement.**

`newHandler` (place in `main.go` next to `newMux`):

```go
func newHandler(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter,
	loginLimiter *LoginRateLimiter, jwtSecret []byte, startTime time.Time,
	cfg adminUIConfig) (http.Handler, error) {

	mux := newMux(store, tracker, rateLimiter, jwtSecret, startTime)

	if cfg.enabled {
		ui, err := newAdminUI(store, jwtSecret, loginLimiter, cfg)
		if err != nil {
			return nil, fmt.Errorf("init admin UI: %w", err)
		}
		staticFiles, err := fs.Sub(files, "web/static")
		if err != nil {
			return nil, fmt.Errorf("prepare static files: %w", err)
		}
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
		registerAdminUI(mux, ui)
	}

	// CSRF: rejects browser cross-origin non-safe requests; clients without
	// Sec-Fetch-Site/Origin headers (Retrofit, curl) are unaffected (spec §4.3).
	csrf := http.NewCrossOriginProtection()
	return csrf.Handler(mux), nil
}
```

(Move the static-file mounting out of `registerAdminUI` into here, or keep it in `registerAdminUI` — one place only; update Task 8's code accordingly. Check the actual Go 1.25 constructor name with `go doc net/http.NewCrossOriginProtection` — if it is `http.CrossOriginProtection{}` zero-value usable, use that form.)

`bootstrap.go`:
```go
package main

import (
	"context"
	"fmt"
	"log/slog"
)

type adminBootstrapStore interface {
	CountUsersByRole(ctx context.Context, role string) (int, error)
	CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error)
}

// bootstrapAdmin creates the first admin account from ADMIN_BOOTSTRAP_* env
// vars, but only when the users table holds zero admins (spec §4.12).
func bootstrapAdmin(ctx context.Context, store adminBootstrapStore, email, password string) error {
	n, err := store.CountUsersByRole(ctx, "admin")
	if err != nil {
		return fmt.Errorf("bootstrap admin: count: %w", err)
	}
	if n > 0 {
		slog.Info("admin bootstrap skipped: admin users already exist", "count", n)
		return nil
	}
	if len(password) < 8 {
		return fmt.Errorf("bootstrap admin: password must be at least 8 characters")
	}
	if _, err := store.CreateUser(ctx, "Administrator", email, password, "admin"); err != nil {
		return fmt.Errorf("bootstrap admin: create: %w", err)
	}
	slog.Info("bootstrapped initial admin user", "email", email)
	return nil
}
```

`main()` changes:
- After migrations: `if be, bp := os.Getenv("ADMIN_BOOTSTRAP_EMAIL"), os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"); be != "" && bp != "" { if err := bootstrapAdmin(ctx, store, be, bp); err != nil { slog.Error(...); os.Exit(1) } }`
- Build `loginLimiter := NewLoginRateLimiter(); defer loginLimiter.Stop()`.
- Replace the `newMux` + `registerAdminUI` block with `handler, err := newHandler(store, tracker, rateLimiter, loginLimiter, jwtSecret, startTime, adminUIConfig{enabled: adminUIEnabled(), trustProxy: trustProxyHeaders()})`, exit on error; `srv.Handler = requestLogger(handler)`. Remove the old warning log line.
- Add `func trustProxyHeaders() bool { v, _ := strconv.ParseBool(os.Getenv("TRUST_PROXY_HEADERS")); return v }` (in `proxy.go`).
- Wire the API login's rate limiting: in `newMux`, change the login registration to `mux.Handle("POST /api/v1/auth/login", handleLogin(store, jwtSecret))` → the limiter check goes INSIDE `handleLogin` via a new parameter: `handleLogin(store, jwtSecret, loginLimiter, trustProxy)` returning 429 JSON `{"error":"too many attempts"}` before touching the store; `newMux` therefore gains `loginLimiter *LoginRateLimiter` and `trustProxy bool` params (update all `newMux` callers in tests — pass `nil`-safe: guard `if limiter != nil` inside handleLogin so existing tests passing nil still work).

`adminUIEnabled()` in `admin_handlers.go` becomes default-true:
```go
func adminUIEnabled() bool {
	v := os.Getenv("ADMIN_UI_ENABLED")
	if v == "" {
		return true
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return enabled
}
```

`seed_dev.sql` — append:
```sql
-- Seed a test admin for local development
-- Email: admin@test.com  |  Password: password
INSERT INTO users (name, email, password_hash, role)
VALUES (
    'Test Admin',
    'admin@test.com',
    '$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi',
    'admin'
)
ON CONFLICT (email) DO NOTHING;
```

Extend `noopStore` in `route_wiring_test.go` with stubs for every new interface method (zero values, same style as existing stubs).

- [ ] **Step 4: Run new tests + full suite → PASS.**
- [ ] **Step 5: Commit** — `git commit -am "feat: compose handler with CSRF protection, admin bootstrap, and default-on admin UI"`

---

### Task 10: static assets — vendor Leaflet, compile Tailwind, drop CDNs

**Files:**
- Create: `web/static/vendor/leaflet/leaflet.js`, `web/static/vendor/leaflet/leaflet.css`, `web/static/vendor/leaflet/images/*` (marker-icon.png, marker-icon-2x.png, marker-shadow.png, layers.png, layers-2x.png), `web/static/css/admin.css` (generated, committed), `web/styles/input.css` (Tailwind source)
- Modify: `web/templates/layout/base.html`, `web/templates/views/login.html`, `Makefile`, CI workflow (`.github/workflows/*` — the Go one)

**Interfaces:**
- Produces: `make css` target; templates reference only `/static/...` URLs (Google Fonts, cdn.tailwindcss.com, unpkg removed).

- [ ] **Step 1: Vendor Leaflet 1.9.4**

```bash
mkdir -p web/static/vendor/leaflet/images
curl -fsSL https://unpkg.com/leaflet@1.9.4/dist/leaflet.js -o web/static/vendor/leaflet/leaflet.js
curl -fsSL https://unpkg.com/leaflet@1.9.4/dist/leaflet.css -o web/static/vendor/leaflet/leaflet.css
for f in marker-icon.png marker-icon-2x.png marker-shadow.png layers.png layers-2x.png; do
  curl -fsSL "https://unpkg.com/leaflet@1.9.4/dist/images/$f" -o "web/static/vendor/leaflet/images/$f"
done
```

Verify sizes are plausible (`leaflet.js` ≈ 140 KB).

- [ ] **Step 2: Tailwind source + build**

`web/styles/input.css` (Tailwind v4 CSS-first config; port the custom look):
```css
@import "tailwindcss";
@source "../templates/**/*.html";
@source "../static/js/**/*.js";

@theme {
  --font-sans: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  --font-display: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
}
```

Then move every rule currently inside base.html's `<style>` block into `input.css` below the `@theme` (the `:root` variables, `.glass-panel`, `.page-shell`, `.sidebar-shell`, `.sidebar-link`, `.table-card`, `.stat-card`, leaflet overrides, `.map-popup*`, `.bus-marker*`, keyframes, plus new `.login-body`/`.login-card`/`.alert` styles for Task 8's login page — write simple card/alert CSS consistent with the existing look). If any template contains an inline `<script>tailwind.config = ...</script>` block (check `login.html`'s old version and all views), port its custom tokens into `@theme` and delete the block.

`Makefile`:
```make
css:
	tailwindcss -i web/styles/input.css -o web/static/css/admin.css --minify
```
(add `css` to `.PHONY` and help text). Run `make css`; commit the generated file.

- [ ] **Step 3: Update templates** — in `base.html`: delete the Google Fonts `<link>`s, the unpkg leaflet css/js tags, the `cdn.tailwindcss.com` script, and the whole inline `<style>` block; add:
```html
<link rel="stylesheet" href="/static/css/admin.css">
<link rel="stylesheet" href="/static/vendor/leaflet/leaflet.css"/>
```
and before `</body>`:
```html
<script src="/static/vendor/leaflet/leaflet.js"></script>
<script src="/static/js/admin.js"></script>
```
In `login.html`, point at `/static/css/admin.css` only. `display-font` keeps `font-weight: 700; letter-spacing: -0.01em` via a rule in `input.css`.

- [ ] **Step 4: CI staleness check** — in the Go CI workflow add a step after checkout (guarded to Linux runner):
```yaml
- name: Verify committed Tailwind CSS is current
  run: |
    curl -fsSL https://github.com/tailwindlabs/tailwindcss/releases/download/v4.1.16/tailwindcss-linux-x64 -o /tmp/tailwindcss
    chmod +x /tmp/tailwindcss
    /tmp/tailwindcss -i web/styles/input.css -o /tmp/admin.css --minify
    diff -q /tmp/admin.css web/static/css/admin.css || { echo "web/static/css/admin.css is stale — run 'make css' and commit"; exit 1; }
```
Pin the Tailwind version in both Makefile comment and CI to the same release. If the local `tailwindcss` version differs from the pinned CI one, regenerate with the pinned binary so outputs match byte-for-byte (download it the same way locally).

- [ ] **Step 5: Verify** — `go test ./...` (templates still parse), then `go run .` briefly with `ADMIN_UI_ENABLED=true JWT_SECRET=$(head -c48 /dev/zero | tr '\0' 'x') PORT=8090` + a running db, `curl -s http://localhost:8090/admin/login | grep -c cdn` → 0. Also `curl -sI http://localhost:8090/static/css/admin.css` → 200.
- [ ] **Step 6: Commit** — `git commit -am "feat: vendor Leaflet and compiled Tailwind CSS; remove CDN dependencies"`

---

### Task 11: live vehicles endpoint

**Files:**
- Create: `admin_live_handlers.go`, `admin_live_handlers_test.go`
- Modify: `handlers.go` (`newMux` route), `route_wiring_test.go` (route tables + noopStore already extended)

**Interfaces:**
- Consumes: `Tracker.ActiveVehicles()`, `VehicleManager.ListVehicles`, `ListActiveTripsByVehicle` (Task 4).
- Produces: `GET /api/v1/admin/vehicles/live` →

```go
type liveVehicleEntry struct {
	VehicleID  string   `json:"vehicle_id"`
	Label      string   `json:"label"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Bearing    *float64 `json:"bearing"`
	Speed      *float64 `json:"speed"`
	GtfsTripID string   `json:"gtfs_trip_id"`
	TripDBID   *int64   `json:"trip_db_id"`
	RouteID    *string  `json:"route_id"`
	DriverName *string  `json:"driver_name"`
	ReportedAt int64    `json:"reported_at"`
	UpdatedAt  string   `json:"updated_at"` // RFC3339 UTC
}
// response: {"count": N, "vehicles": [...]}
func handleLiveVehicles(tracker *Tracker, vehicles VehicleManager, trips ActiveTripLister) http.HandlerFunc
type ActiveTripLister interface {
	ListActiveTripsByVehicle(ctx context.Context) (map[string]ActiveTripInfo, error)
}
```

- [ ] **Step 1: Failing tests** — drive the handler directly with a real `Tracker` and fakes:

```go
type fakeVehicleLister struct{ vehicles []VehicleResponse }
func (f *fakeVehicleLister) ListVehicles(_ context.Context) ([]VehicleResponse, error) { return f.vehicles, nil }
// ...stub the other VehicleManager methods with panics or zero returns

type fakeActiveTrips struct{ m map[string]ActiveTripInfo }
func (f *fakeActiveTrips) ListActiveTripsByVehicle(_ context.Context) (map[string]ActiveTripInfo, error) { return f.m, nil }

func TestHandleLiveVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	speed := 8.5
	tracker.Update(&LocationReport{VehicleID: "bus-1", TripID: "gtfs-77", Latitude: -1.29, Longitude: 36.82, Speed: &speed, Timestamp: 1752566400})
	tracker.Update(&LocationReport{VehicleID: "ghost-9", TripID: "", Latitude: 0.1, Longitude: 0.2, Timestamp: 1752566400})

	vehicles := &fakeVehicleLister{vehicles: []VehicleResponse{{ID: "bus-1", Label: "Bus One", Active: true}}}
	trips := &fakeActiveTrips{m: map[string]ActiveTripInfo{
		"bus-1": {TripID: 42, RouteID: "5", GtfsTripID: "gtfs-77", UserID: 3, DriverName: "Asha"},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, vehicles, trips).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count    int                `json:"count"`
		Vehicles []liveVehicleEntry `json:"vehicles"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 2, resp.Count)

	byID := map[string]liveVehicleEntry{}
	for _, v := range resp.Vehicles {
		byID[v.VehicleID] = v
	}
	b1 := byID["bus-1"]
	assert.Equal(t, "Bus One", b1.Label)
	require.NotNil(t, b1.TripDBID)
	assert.EqualValues(t, 42, *b1.TripDBID)
	assert.Equal(t, "Asha", *b1.DriverName)
	assert.Equal(t, "5", *b1.RouteID)
	assert.Nil(t, b1.Bearing)
	assert.Equal(t, 8.5, *b1.Speed)
	assert.EqualValues(t, 1752566400, b1.ReportedAt)

	g := byID["ghost-9"]
	assert.Equal(t, "ghost-9", g.Label, "label falls back to id when vehicle unknown")
	assert.Nil(t, g.TripDBID)
	assert.Nil(t, g.DriverName)
}
```

- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** — build a `map[id]label` from `ListVehicles`, iterate `tracker.ActiveVehicles()`, join `ListActiveTripsByVehicle()`; entries sorted by `vehicle_id` for stable output; errors from either store call → 500 JSON + slog. `UpdatedAt` = `state.UpdatedAt.UTC().Format(time.RFC3339)`.
- [ ] **Step 4: Wire route** in `newMux`: `mux.Handle("GET /api/v1/admin/vehicles/live", authMiddleware(adminMiddleware(handleLiveVehicles(tracker, store, store))))`. **Order matters**: register it BEFORE the `{id}` pattern conflicts cannot arise (Go 1.22 mux prefers the more specific literal segment "live" over `{id}` automatically, but `GET /api/v1/admin/vehicles/{id}` also matches "live" — the literal pattern wins; add a wiring test asserting `/api/v1/admin/vehicles/live` with admin token does NOT hit `handleGetVehicle`). Add the route to both tables in `route_wiring_test.go`.
- [ ] **Step 5: Run → PASS, full suite PASS.**
- [ ] **Step 6: Commit** — `git commit -am "feat: add live vehicles admin endpoint joining tracker, labels, and active trips"`

---

### Task 12: trips list + trail JSON endpoints

**Files:**
- Modify: `admin_live_handlers.go`, `admin_live_handlers_test.go`, `handlers.go` (routes), `route_wiring_test.go`

**Interfaces:**
- Consumes: `ListTrips`, `GetTripSummary`, `ListTripLocations` (Task 4).
- Produces:
  - `GET /api/v1/admin/trips?status=&vehicle_id=&q=&limit=&offset=` → `{"count": n, "has_more": bool, "trips": [TripSummary...]}` (limit default 50, max 200; invalid → 400)
  - `GET /api/v1/admin/trips/{id}/locations` → `{"trip": TripSummary, "points": [{"latitude":..,"longitude":..,"bearing":..,"speed":..,"accuracy":..,"reported_at":<unix>,"received_at":"RFC3339"}...]}`; unknown id → 404.
  - `func handleListTrips(store TripLister) http.HandlerFunc`, `func handleTripLocations(store TripTrailStore) http.HandlerFunc` with `type TripLister interface { ListTrips(ctx context.Context, f TripFilter) ([]TripSummary, error) }` and `type TripTrailStore interface { GetTripSummary(ctx context.Context, id int64) (*TripSummary, error); ListTripLocations(ctx context.Context, tripID int64) ([]LocationPoint, error) }`.

- [ ] **Step 1: Failing handler tests** with fakes: status filter passthrough, `limit+1` hasMore behavior (fake returns limit+1 rows → `has_more: true`, `trips` trimmed to limit), bad `limit` → 400, bad `status` (not active/completed/"") → 400, trail unknown id → 404 (fake returns `ErrTripNotFound`), trail happy path point mapping (`Timestamp` → `reported_at`).
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** both handlers (validate query params exactly like `handleGetLocationHistory` does — reuse `parseOptionalInt`).
- [ ] **Step 4: Wire routes** in `newMux` (`GET /api/v1/admin/trips`, `GET /api/v1/admin/trips/{id}/locations`), both `authMiddleware(adminMiddleware(...))`; extend both route-wiring tables.
- [ ] **Step 5: Run → PASS, full suite. Commit** — `git commit -am "feat: add admin trips list and trip trail endpoints"`

---

### Task 13: dashboard page with real data

**Files:**
- Modify: `admin_page_handlers.go` (`dashboardPage`), `web/templates/views/dashboard.html`, `admin_page_handlers_test.go`
- The `adminUI` struct gains fields: `tracker *Tracker`, `stats adminStatsStore`, `activeTrips ActiveTripLister`, `vehicles VehicleManager` — with `type adminStatsStore interface { CountActiveVehicles(ctx context.Context) (int, error); CountActiveUsersByRole(ctx context.Context, role string) (int, error); CountActiveTrips(ctx context.Context) (int, error) }`. Update `newAdminUI` signature to accept the full `appStore` and tracker: `newAdminUI(store appStore, tracker *Tracker, jwtSecret []byte, limiter *LoginRateLimiter, cfg adminUIConfig)` — update `newHandler` and every test constructor.

**Interfaces:**
- Produces: dashboard template data keys: `TotalVehicles`, `ActiveVehicles`, `TotalDrivers`, `ActiveTrips` (ints), `LastUpdate` (string, humanized or "never"), `StalenessThreshold` (string), `RecentVehicles` (`[]dashboardRow{Label, RouteID, LastSeen string}`), `Flash`.

- [ ] **Step 1: Failing test** — with an admin cookie, a tracker containing one fresh vehicle, and a fake store returning counts (extend `noopStore`-style fake or use dedicated fakes on `adminUI` fields):

```go
func TestDashboardRendersRealCounts(t *testing.T) {
	ui := newTestAdminUI(t) // now takes fakes: stats fake returns 7 vehicles, 5 drivers, 3 trips
	ui.tracker.Update(&LocationReport{VehicleID: "bus-1", TripID: "g1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, ">7<")      // total vehicles stat
	assert.Contains(t, body, ">5<")      // drivers stat
	assert.Contains(t, body, ">3<")      // active trips stat
	assert.Contains(t, body, "bus-1")    // recent activity row (label falls back to id)
	assert.NotContains(t, body, "Bus 001", "mock data must be gone")
}
```

- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `dashboardPage`: call the three counts, `ui.tracker.Status()` for `ActiveVehicles`/`LastUpdate`, `ui.tracker.ActiveVehicles()` sorted by `UpdatedAt` desc capped at 10 joined with vehicle labels (`ListVehicles` map) and active-trip route ids; humanize ages with a small helper `func humanizeAge(t time.Time) string` ("just now", "N min ago", "N h ago"). Any store error → 500 via `http.Error` + slog. Update `dashboard.html`: keep the stat-card markup, bind the new keys, add the feed-health strip (`LastUpdate`, `StalenessThreshold`) below the cards, replace the recent-activity `range` fields (`.Label`, `.RouteID`, `.LastSeen`), add `{{if not .RecentVehicles}}` empty-state row.
- [ ] **Step 4: Run → PASS, full suite. Commit** — `git commit -am "feat: dashboard renders live counts, feed health, and recent activity"`

---

### Task 14: live map front-end + trail mode

**Files:**
- Modify: `web/static/js/admin.js` (full rewrite), `web/templates/views/map.html`, `admin_page_handlers.go` (`mapPage` passes trail context), `admin_page_handlers_test.go`

**Interfaces:**
- Consumes: `/api/v1/admin/vehicles/live` (Task 11), `/api/v1/admin/trips/{id}/locations` (Task 12) — both reachable with the session cookie.
- Produces: `mapPage` template data: `Title`, `Page: "map"`, `TripID` (string, "" for live mode), `Flash`.

- [ ] **Step 1: Failing render test** — `GET /admin/map` with admin cookie contains `id="main-map"` and `data-live-url="/api/v1/admin/vehicles/live"`; `GET /admin/map?trip_id=42` contains `data-trip-url="/api/v1/admin/trips/42/locations"`; non-numeric `trip_id` → 404.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement.**

`mapPage`: parse optional `trip_id` (int64; invalid → `http.NotFound`), pass through. `map.html`: keep the map container + stats overlay skeleton, delete the hardcoded fleet sidebar entries and route summary, replace with empty containers filled by JS (`id="fleet-list"`, `id="empty-banner"` hidden by default, stats `id="stat-active"`, `id="stat-routes"`); tag the map div:
```html
<div id="main-map" class="absolute inset-0"
     data-live-url="/api/v1/admin/vehicles/live"
     {{if .TripID}}data-trip-url="/api/v1/admin/trips/{{.TripID}}/locations"{{end}}></div>
```

`admin.js` — complete rewrite (no mock data). Structure:

```js
(function () {
  const el = document.getElementById("main-map");
  if (!el || typeof L === "undefined") return;

  const map = L.map("main-map", { zoomControl: false }).setView([0, 0], 2);
  L.tileLayer("https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png", {
    attribution: "&copy; OpenStreetMap contributors &copy; CARTO", maxZoom: 19,
  }).addTo(map);
  L.control.zoom({ position: "bottomright" }).addTo(map);

  const tripUrl = el.dataset.tripUrl;
  if (tripUrl) { renderTrail(tripUrl); } else { startLive(el.dataset.liveUrl); }

  function busIcon() { /* existing divIcon markup, single active style */ }

  function popupHtml(v) {
    // label, id, route, driver, speed, last update age — all via textContent-safe
    // construction: build DOM nodes, never innerHTML with server strings.
  }

  async function fetchJSON(url) {
    const res = await fetch(url, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    return res.json();
  }

  // --- live mode ---
  let markers = new Map(); let fitted = false; let timer = null;
  async function refresh(url) {
    try {
      const data = await fetchJSON(url);
      drawVehicles(data.vehicles || []);
      updateSidebar(data.vehicles || []);
    } catch (e) { console.error("live refresh failed", e); }
  }
  function startLive(url) {
    refresh(url);
    timer = setInterval(() => { if (!document.hidden) refresh(url); }, 10000);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(url); });
  }
  function drawVehicles(vehicles) {
    const seen = new Set();
    vehicles.forEach(v => {
      seen.add(v.vehicle_id);
      const ll = [v.latitude, v.longitude];
      if (markers.has(v.vehicle_id)) { markers.get(v.vehicle_id).setLatLng(ll).setPopupContent(popupHtml(v)); }
      else { markers.set(v.vehicle_id, L.marker(ll, { icon: busIcon() }).addTo(map).bindPopup(popupHtml(v))); }
    });
    for (const [id, m] of markers) if (!seen.has(id)) { map.removeLayer(m); markers.delete(id); }
    document.getElementById("empty-banner")?.classList.toggle("hidden", vehicles.length > 0);
    if (!fitted && vehicles.length) { map.fitBounds(vehicles.map(v => [v.latitude, v.longitude]), { padding: [40, 40], maxZoom: 15 }); fitted = true; }
    const routes = new Set(vehicles.map(v => v.route_id).filter(Boolean));
    setText("stat-active", vehicles.length); setText("stat-routes", routes.size);
  }
  function updateSidebar(vehicles) { /* rebuild #fleet-list rows with DOM APIs */ }
  function setText(id, v) { const n = document.getElementById(id); if (n) n.textContent = String(v); }

  // --- trail mode ---
  async function renderTrail(url) {
    try {
      const data = await fetchJSON(url);
      const pts = (data.points || []).map(p => [p.latitude, p.longitude]);
      if (!pts.length) { document.getElementById("empty-banner")?.classList.remove("hidden"); return; }
      L.polyline(pts, { color: "#0f766e", weight: 5, opacity: 0.85 }).addTo(map);
      L.marker(pts[0], { icon: busIcon() }).addTo(map).bindPopup(trailPopup("Start", data.trip));
      L.marker(pts[pts.length - 1], { icon: busIcon() }).addTo(map).bindPopup(trailPopup("End", data.trip));
      map.fitBounds(pts, { padding: [40, 40] });
      // header strip with trip metadata via DOM construction
    } catch (e) { console.error("trail load failed", e); }
  }
  function trailPopup(kind, trip) { /* DOM-built: kind, vehicle_label, driver_name, route_id */ }
})();
```

Write the elided function bodies fully in the actual file — every popup/sidebar builder uses `document.createElement` + `textContent` (no string interpolation of server data into HTML).

- [ ] **Step 4: Run render tests → PASS.** Manual check deferred to Task 18's smoke.
- [ ] **Step 5: Commit** — `git commit -am "feat: live map polls real vehicle data; trip trail mode"`

---

### Task 15: vehicles pages

**Files:**
- Modify: `admin_page_handlers.go`, `web/templates/views/vehicles.html`, `admin_page_handlers_test.go`
- Create: `web/templates/views/vehicle_form.html` (add to `loadTemplates` adminViews list)

**Interfaces:**
- Consumes: `VehicleManager`, `UpdateVehicleInfo`, `SetVehicleActive`, `VehicleExists` (existing `VehicleChecker`), tracker, `ListActiveTripsByVehicle`.
- Produces routes (all behind `requireAdminPage`, registered in `registerAdminUI`): `GET /admin/vehicles`, `GET /admin/vehicles/new`, `POST /admin/vehicles`, `GET /admin/vehicles/{id}/edit`, `POST /admin/vehicles/{id}`, `POST /admin/vehicles/{id}/deactivate`, `POST /admin/vehicles/{id}/activate`.

- [ ] **Step 1: Failing tests** — list page shows a seeded fake vehicle's label + CSV link `href` containing `/api/v1/admin/vehicles/<id>/locations?format=csv`; default hides inactive, `?include_inactive=1` shows them; create happy path → 303 to `/admin/vehicles` + flash cookie `vehicle_created`; create with bad id (`"bad id!"`) → 422 re-render with the exact API error text (`vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores`); create with existing id → 422 "vehicle id already exists"; edit POST updates label; deactivate POST → 303 + flash; activate POST → 303 + flash; unknown id on edit page → 404.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement.**

Handlers: `vehiclesPage` (list: `ListVehicles`, filter `Active` unless `include_inactive=1`; join tracker `ActiveVehicles()` for LastSeen and `ListActiveTripsByVehicle` for driver), `vehicleNewPage`, `vehicleCreate` (validate id via the same `validateVehicleID` helper `handlers_vehicles.go` uses — export/reuse it, don't duplicate the regexp; `VehicleExists` → 422 on collision; then `UpsertVehicle`), `vehicleEditPage` (`GetVehicle`, 404 on ErrNoRows), `vehicleUpdate` (`UpdateVehicleInfo`), `vehicleDeactivate`/`vehicleActivate` (`SetVehicleActive` + flash + 303). All POSTs finish with `setFlash(w, code); http.Redirect(w, r, "/admin/vehicles", http.StatusSeeOther)`.

`vehicles.html`: real columns (ID, Label, Agency tag, Status badge incl. Deactivated, Last seen, Driver), row actions (Edit link, CSV link, deactivate/activate POST forms with `onsubmit="return confirm('...')"`) plus a "New vehicle" button and the include-inactive toggle link. `vehicle_form.html`: one template serving create and edit (`{{if .IsEdit}}`), fields id (readonly on edit), label, agency_tag, error banner, submit. Register in `loadTemplates`.

- [ ] **Step 4: Run → PASS, full suite. Commit** — `git commit -am "feat: vehicle management pages with create, edit, deactivate, CSV export"`

---

### Task 16: users pages + assignments

**Files:**
- Modify: `admin_page_handlers.go`, `web/templates/views/users.html`, `admin_page_handlers_test.go`
- Create: `web/templates/views/user_form.html` (register in `loadTemplates`)

**Interfaces:**
- Consumes: `UserLister/Getter/Creator/Updater`, `SetUserActive`, `AssignmentCreator/Deleter/ListerByUser`, `VehicleManager`, and a password-update path: add `(*Store) UpdateUserPassword(ctx context.Context, id int64, password string) error` (bcrypt-hash inside; sqlc query `UpdateUserPassword :execrows — UPDATE users SET password_hash = $2 WHERE id = $1`) with narrow interface `UserPasswordUpdater`; add to `appStore` + `noopStore` + a store test (round-trip: update password, `GetUserByEmail`, bcrypt compare succeeds with new password).
- Produces routes: `GET /admin/users`, `GET /admin/users/new`, `POST /admin/users`, `GET /admin/users/{id}/edit`, `POST /admin/users/{id}`, `POST /admin/users/{id}/deactivate`, `POST /admin/users/{id}/activate`, `POST /admin/users/{id}/vehicles`, `POST /admin/users/{id}/vehicles/{vehicleID}/remove`.

- [ ] **Step 1: Failing tests** — list renders names/roles/active badges and assigned-vehicle counts; create validates (password < 8 chars → 422 "password must be at least 8 characters"; duplicate email → 422 "email already exists"); create success → 303 + `user_created` flash; edit updates name/email/role, optional non-empty password triggers `UpdateUserPassword`; deactivate/activate → 303 + flash; assign vehicle POST (`vehicle_id` form value) → 303 + `vehicle_assigned`; unassign → 303 + `vehicle_unassigned`; edit page for unknown id → 404. Plus the `UpdateUserPassword` store test.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** handlers + templates. `usersPage`: `ListUsers` + per-user `ListAssignmentsByUser` count (N+1 is fine at admin scale; note it). `user_form.html` edit mode shows the assignments block: current assignments (each with a remove POST form) and a `<select name="vehicle_id">` of active vehicles not already assigned + assign button. Role `<select>` offers `driver`/`admin` only; reject other values server-side (422). Password field labeled "New password (leave blank to keep current)".
- [ ] **Step 4: Run → PASS, full suite. Commit** — `git commit -am "feat: user management pages with CRUD, password change, and vehicle assignments"`

---

### Task 17: trips page

**Files:**
- Modify: `admin_page_handlers.go`, `web/templates/views/trips.html`, `admin_page_handlers_test.go`

**Interfaces:**
- Consumes: `ListTrips` (`TripFilter`), `VehicleManager.ListVehicles` (filter dropdown).
- Produces: `GET /admin/trips?status=&vehicle_id=&q=&page=N` — 50/page, `Filter` echo back into form fields, `HasMore`/`Page` for pagination links.

- [ ] **Step 1: Failing tests** — renders fake trips (vehicle label, driver name, "UTC" suffix on times, duration, View-trail link `href="/admin/map?trip_id=42"`); filter params passed through to the store fake (`assert` captured `TripFilter{Status:"active", VehicleID:"bus-1", Q:"asha", Limit:51, Offset:50}` for `?status=active&vehicle_id=bus-1&q=asha&page=2`); invalid `page` → page 1; empty state row when no trips.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `tripsPage`: parse filters (`status` must be ""/active/completed else 400), `page` ≥ 1, call `ListTrips` with `Limit: 51, Offset: (page-1)*50`, trim + `HasMore`; format times `t.UTC().Format("2006-01-02 15:04") + " UTC"`, duration `EndTime.Sub(StartTime)` rounded to minutes ("—" while active). Template: filter bar `<form method="get">` (status select, vehicle select from `ListVehicles`, text input `q`, Apply button), table per spec §4.7, prev/next links preserving query params, View-trail row action.
- [ ] **Step 4: Run → PASS, full suite. Commit** — `git commit -am "feat: trip history page with filters, search, pagination, and trail links"`

---

### Task 18: wiring tests, docs, end-to-end smoke

**Files:**
- Modify: `route_wiring_test.go` (admin-page table), `README.md`, `docs/development.md`, `docs/android-smoke-test.md` (only if it references ADMIN_UI_ENABLED)
- Create: none

- [ ] **Step 1: Admin-page wiring test** — extend `handler_composition_test.go` (or `route_wiring_test.go`) with a table over EVERY `/admin/*` page and POST route (from Tasks 8, 15, 16, 17): unauthenticated → 303 to `/admin/login`; driver-role cookie → 303; and every new `/api/v1/admin/*` JSON route (live, trips, trip locations) present in both existing route-wiring tables (driver → 403 JSON, admin → not 401/403). Run → fix any wiring gap it finds.
- [ ] **Step 2: Docs.**
  - `README.md`: under "Getting Started", add an "Admin web UI" subsection: served at `/admin` (default on; `ADMIN_UI_ENABLED=false` disables), first admin via `ADMIN_BOOTSTRAP_EMAIL`/`ADMIN_BOOTSTRAP_PASSWORD` or `seed_dev.sql` (admin@test.com / password), `TRUST_PROXY_HEADERS=true` behind a reverse proxy. Note the default flip for upgrading operators.
  - `docs/development.md`: same info in the dev-workflow voice + `make css` for template styling changes + the pinned Tailwind CLI version.
- [ ] **Step 3: Full-stack smoke** (documented commands, run them):

```bash
docker compose up -d db
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable'
psql "$DATABASE_URL" -f seed_dev.sql   # or: docker compose exec -T db psql -U postgres -d vehicle_positions < seed_dev.sql
JWT_SECRET='dev-secret-dev-secret-dev-secret-12' PORT=8090 go run . &
sleep 2
# login page live
curl -s -o /dev/null -w '%{http_code}' http://localhost:8090/admin/login   # expect 200
# unauthenticated dashboard redirects
curl -s -o /dev/null -w '%{http_code}' http://localhost:8090/admin/dashboard   # expect 303
# form login sets cookie
curl -s -i -c /tmp/vp-cookies.txt -d 'email=admin@test.com&password=password' http://localhost:8090/admin/login | head -5   # expect 303 + Set-Cookie vp_session
# authenticated dashboard renders
curl -s -b /tmp/vp-cookies.txt http://localhost:8090/admin/dashboard | grep -o 'Dashboard' | head -1
# live endpoint via cookie
curl -s -b /tmp/vp-cookies.txt http://localhost:8090/api/v1/admin/vehicles/live
# simulate some vehicles, then re-check live + map + trips pages
go run ./cmd/simulator -url http://localhost:8090 -vehicles 3 -interval 2s -duration 10s || true
curl -s -b /tmp/vp-cookies.txt http://localhost:8090/api/v1/admin/vehicles/live | head -c 400
kill %1
```

(Port 8090 avoids the machine's occupied 8080. The simulator posts unauthenticated locations — if it needs a driver JWT, log in as driver@test.com via the API first and pass the token per the simulator's flags; check `cmd/simulator` usage.) Fix anything the smoke exposes.

- [ ] **Step 4: Full suite + vet** — `DATABASE_URL=... go test ./... && go vet ./...` → all green.
- [ ] **Step 5: Commit** — `git commit -am "test: admin route wiring coverage; docs: admin UI setup and operations"`

---

## Self-Review Notes (already applied)

- Spec §4.2 cookie fallback ↔ Task 6; §4.3 CSRF ↔ Task 9; §4.4 routes ↔ Tasks 8/15/16/17; §4.5 trail ↔ Task 4; §4.6 flash/PRG ↔ Tasks 7/15/16; §4.7 pages ↔ Tasks 13–17; §4.8 store ↔ Tasks 1/3/4 (+`UpdateUserPassword` added in Task 16 — spec's "optional password change" implied it); §4.9 assets ↔ Task 10; §4.10 proxy ↔ Task 5; §4.11 limiter ↔ Tasks 5/8/9; §4.12 flag/bootstrap ↔ Task 9; §6 testing ↔ every task + Task 18.
- Types cross-checked: `TripSummary`/`TripFilter`/`ActiveTripInfo` (Task 4) consumed by Tasks 11/12/17; `adminUIConfig`/`adminUI` (Task 8) consumed by 9/13–17; `sessionCookieName` defined once (Task 6).
- Verify `http.CrossOriginProtection` construction form with `go doc` in Task 9 (constructor vs zero value) — noted inline.
