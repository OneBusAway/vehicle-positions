package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// fakeUserFetcher is a minimal UserFetcher backed by an in-memory map, used by
// both the login-flow tests here and anywhere else a UserFetcher double is
// needed (merge target for any future Task 2-style fake of the same shape).
type fakeUserFetcher struct {
	users map[string]*User
}

func (f *fakeUserFetcher) GetUserByEmail(_ context.Context, email string) (*User, error) {
	u, ok := f.users[email]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}

func newTestAdminUI(t *testing.T) *adminUI {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ui, err := newAdminUI(&noopStore{}, tracker, testSecret, NewLoginRateLimiter(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute})
	require.NoError(t, err)
	t.Cleanup(ui.loginLimiter.Stop)
	return ui
}

// fakeAdminStats is a configurable adminStatsStore double for dashboard tests
// that need specific counts, independent of the tracker's own vehicle count.
type fakeAdminStats struct {
	vehicles int
	drivers  int
	trips    int
}

func (f *fakeAdminStats) CountActiveVehicles(_ context.Context) (int, error) { return f.vehicles, nil }
func (f *fakeAdminStats) CountActiveUsersByRole(_ context.Context, _ string) (int, error) {
	return f.drivers, nil
}
func (f *fakeAdminStats) CountActiveTrips(_ context.Context) (int, error) { return f.trips, nil }

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
	t.Run("unknown email re-renders 401 identically", func(t *testing.T) {
		w := post("ghost@test.com", "nope")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid email or password")
	})
	t.Run("deactivated user re-renders 401 identically", func(t *testing.T) {
		inactive := &User{ID: 3, Email: "inactive@test.com", PasswordHash: string(hash), Role: "admin", Active: false}
		ui.users = &fakeUserFetcher{users: map[string]*User{admin.Email: admin, driver.Email: driver, inactive.Email: inactive}}
		w := post(inactive.Email, "password123")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid email or password")
	})
	t.Run("driver role gets 403 admin-required", func(t *testing.T) {
		w := post(driver.Email, "password123")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "Admin access required")
	})
	t.Run("missing fields get 422", func(t *testing.T) {
		w := post("", "")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})
	t.Run("rate limited after repeated attempts", func(t *testing.T) {
		var last *httptest.ResponseRecorder
		for i := 0; i < 12; i++ {
			last = post("x@test.com", "nope")
		}
		assert.Equal(t, http.StatusTooManyRequests, last.Code)
		assert.Contains(t, last.Body.String(), "Too many attempts, try again shortly.")
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

// TestAdminPagesRenderWithSession exercises the protected pages with a valid
// admin session cookie, verifying the mock data carried over from the old
// package-level handlers still renders through the new adminUI methods.
func TestAdminPagesRenderWithSession(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	cases := []struct {
		name string
		path string
		want string
	}{
		{"dashboard", "/admin/dashboard", "Active Trips"},
		{"vehicles", "/admin/vehicles", "New vehicle"},
		{"users", "/admin/users", "Chaitanya K"},
		{"trips", "/admin/trips", "Route A"},
		{"map", "/admin/map", "Live Map"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.AddCookie(cookieFor(t, "admin"))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

// TestMapPageLiveMode verifies the live-map view (no trip_id) tags the map
// container with the live-feed data attribute and omits the trail attribute.
func TestMapPageLiveMode(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/map", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `id="main-map"`)
	assert.Contains(t, body, `data-live-url="/api/v1/admin/vehicles/live"`)
	assert.NotContains(t, body, "data-trip-url")
}

// TestMapPageTrailMode verifies a numeric trip_id query param tags the map
// container with the trail-locations data attribute.
func TestMapPageTrailMode(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/map?trip_id=42", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `data-trip-url="/api/v1/admin/trips/42/locations"`)
}

// TestMapPageInvalidTripIDReturns404 verifies a non-numeric trip_id is
// rejected as 404 rather than silently falling back to live mode.
func TestMapPageInvalidTripIDReturns404(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/map?trip_id=not-a-number", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAdminRootRedirect covers both branches of rootRedirect: an
// authenticated visitor goes straight to the dashboard, an unauthenticated
// one goes to the login page.
func TestAdminRootRedirect(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	t.Run("authenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/dashboard", w.Header().Get("Location"))
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/login", w.Header().Get("Location"))
	})
}

// TestAdminLoginPageRedirectsWhenAlreadyAuthenticated ensures a signed-in
// admin hitting the login page is bounced to the dashboard instead of seeing
// the form again.
func TestAdminLoginPageRedirectsWhenAlreadyAuthenticated(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/dashboard", w.Header().Get("Location"))
}

// TestDashboardRendersRealCounts verifies the dashboard renders live stats
// (from a fake stats store), the tracker's active-vehicle count/recent
// activity, and that the old mock data is gone.
func TestDashboardRendersRealCounts(t *testing.T) {
	ui := newTestAdminUI(t)
	ui.stats = &fakeAdminStats{vehicles: 7, drivers: 5, trips: 3}
	ui.tracker.Update(&LocationReport{VehicleID: "bus-1", TripID: "g1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, ">7<")   // total vehicles stat
	assert.Contains(t, body, ">5<")   // drivers stat
	assert.Contains(t, body, ">3<")   // active trips stat
	assert.Contains(t, body, "bus-1") // recent activity row (label falls back to id)
	assert.NotContains(t, body, "Bus 001", "mock data must be gone")
}

// TestDashboardRecentActivityEmptyState covers the empty-state row when the
// tracker has no active vehicles.
func TestDashboardRecentActivityEmptyState(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No vehicles have reported recently")
}

// TestDashboardStoreErrorReturns500 verifies a stats-store failure produces a
// 500 rather than a partially-rendered page.
func TestDashboardStoreErrorReturns500(t *testing.T) {
	ui := newTestAdminUI(t)
	ui.stats = &erroringAdminStats{}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

type erroringAdminStats struct{}

func (erroringAdminStats) CountActiveVehicles(_ context.Context) (int, error) {
	return 0, errors.New("boom")
}
func (erroringAdminStats) CountActiveUsersByRole(_ context.Context, _ string) (int, error) {
	return 0, errors.New("boom")
}
func (erroringAdminStats) CountActiveTrips(_ context.Context) (int, error) {
	return 0, errors.New("boom")
}

// fakeVehicleStore is an in-memory double implementing VehicleManager,
// vehicleEditor (UpdateVehicleInfo/SetVehicleActive), and VehicleChecker
// (VehicleExists), covering everything the vehicle pages need without a
// database.
type fakeVehicleStore struct {
	vehicles map[string]*VehicleResponse
}

func newFakeVehicleStore(vehicles ...VehicleResponse) *fakeVehicleStore {
	m := make(map[string]*VehicleResponse, len(vehicles))
	for i := range vehicles {
		v := vehicles[i]
		m[v.ID] = &v
	}
	return &fakeVehicleStore{vehicles: m}
}

func (f *fakeVehicleStore) ListVehicles(_ context.Context) ([]VehicleResponse, error) {
	out := make([]VehicleResponse, 0, len(f.vehicles))
	for _, v := range f.vehicles {
		out = append(out, *v)
	}
	return out, nil
}

func (f *fakeVehicleStore) GetVehicle(_ context.Context, id string) (*VehicleResponse, error) {
	v, ok := f.vehicles[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *v
	return &cp, nil
}

func (f *fakeVehicleStore) UpsertVehicle(_ context.Context, id, label, agencyTag string) (*VehicleResponse, error) {
	v := &VehicleResponse{ID: id, Label: label, AgencyTag: agencyTag, Active: true}
	f.vehicles[id] = v
	cp := *v
	return &cp, nil
}

func (f *fakeVehicleStore) DeactivateVehicle(_ context.Context, id string) error {
	v, ok := f.vehicles[id]
	if !ok {
		return pgx.ErrNoRows
	}
	v.Active = false
	return nil
}

func (f *fakeVehicleStore) UpdateVehicleInfo(_ context.Context, id, label, agencyTag string) error {
	v, ok := f.vehicles[id]
	if !ok {
		return pgx.ErrNoRows
	}
	v.Label = label
	v.AgencyTag = agencyTag
	return nil
}

func (f *fakeVehicleStore) SetVehicleActive(_ context.Context, id string, active bool) error {
	v, ok := f.vehicles[id]
	if !ok {
		return pgx.ErrNoRows
	}
	v.Active = active
	return nil
}

func (f *fakeVehicleStore) VehicleExists(_ context.Context, id string) (bool, error) {
	_, ok := f.vehicles[id]
	return ok, nil
}

// wireFakeVehicleStore points every vehicle-related adminUI field at the
// same fake, mirroring how newAdminUI wires them all from a single appStore.
func wireFakeVehicleStore(ui *adminUI, f *fakeVehicleStore) {
	ui.vehicles = f
	ui.vehicleEditor = f
	ui.vehicleChecker = f
}

// TestVehiclesPageListsRealVehicles verifies the list page renders a seeded
// vehicle's label and a CSV export link for it, and that the old mock data
// is gone.
func TestVehiclesPageListsRealVehicles(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Bus One", AgencyTag: "metro", Active: true}))
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	req := httptest.NewRequest(http.MethodGet, "/admin/vehicles", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Bus One")
	assert.Contains(t, body, "/api/v1/admin/vehicles/bus-1/locations?format=csv")
	assert.NotContains(t, body, "Bus 001", "mock data must be gone")
}

// TestVehiclesPageInactiveFilter verifies the list hides inactive vehicles
// by default and shows them with ?include_inactive=1.
func TestVehiclesPageInactiveFilter(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore(
		VehicleResponse{ID: "active-1", Label: "Active Bus", Active: true},
		VehicleResponse{ID: "inactive-1", Label: "Retired Bus", Active: false},
	))
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	get := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	t.Run("default hides inactive", func(t *testing.T) {
		body := get("/admin/vehicles")
		assert.Contains(t, body, "Active Bus")
		assert.NotContains(t, body, "Retired Bus")
	})

	t.Run("include_inactive shows all", func(t *testing.T) {
		body := get("/admin/vehicles?include_inactive=1")
		assert.Contains(t, body, "Active Bus")
		assert.Contains(t, body, "Retired Bus")
	})
}

// TestVehicleCreate covers the create form's happy path, validation-error
// re-render, and duplicate-id rejection.
func TestVehicleCreate(t *testing.T) {
	post := func(ui *adminUI, mux *http.ServeMux, id, label, agencyTag string) *httptest.ResponseRecorder {
		form := url.Values{"id": {id}, "label": {label}, "agency_tag": {agencyTag}}
		req := httptest.NewRequest(http.MethodPost, "/admin/vehicles", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("success redirects with flash", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(ui, mux, "bus-42", "Bus 42", "metro")
		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/vehicles", w.Header().Get("Location"))
		require.NotEmpty(t, w.Result().Cookies())
		found := false
		for _, c := range w.Result().Cookies() {
			if c.Name == flashCookieName {
				assert.Equal(t, "vehicle_created", c.Value)
				found = true
			}
		}
		assert.True(t, found, "expected vehicle_created flash cookie")
	})

	t.Run("invalid id re-renders 422 with API error text", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(ui, mux, "bad id!", "Bad Bus", "")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores")
		assert.Contains(t, w.Body.String(), `value="bad id!"`)
	})

	t.Run("duplicate id re-renders 422", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeVehicleStore(ui, newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Existing", Active: true}))
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(ui, mux, "bus-1", "New Label", "")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "vehicle id already exists")
	})
}

// TestVehicleEditPage covers rendering the edit form for a known vehicle and
// 404ing for an unknown one.
func TestVehicleEditPage(t *testing.T) {
	t.Run("known id renders form with values", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeVehicleStore(ui, newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Bus One", AgencyTag: "metro", Active: true}))
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		req := httptest.NewRequest(http.MethodGet, "/admin/vehicles/bus-1/edit", nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Bus One")
	})

	t.Run("unknown id 404s", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		req := httptest.NewRequest(http.MethodGet, "/admin/vehicles/ghost/edit", nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestVehicleUpdate verifies the edit POST updates label/agency_tag and
// redirects with a flash.
func TestVehicleUpdate(t *testing.T) {
	ui := newTestAdminUI(t)
	fake := newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Old Label", AgencyTag: "old-tag", Active: true})
	wireFakeVehicleStore(ui, fake)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	form := url.Values{"label": {"New Label"}, "agency_tag": {"new-tag"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/vehicles/bus-1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/vehicles", w.Header().Get("Location"))
	assert.Equal(t, "New Label", fake.vehicles["bus-1"].Label)
	assert.Equal(t, "new-tag", fake.vehicles["bus-1"].AgencyTag)
}

// TestVehicleDeactivateActivate verifies both POST endpoints redirect with
// the correct flash and flip the active flag.
func TestVehicleDeactivateActivate(t *testing.T) {
	ui := newTestAdminUI(t)
	fake := newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Bus One", Active: true})
	wireFakeVehicleStore(ui, fake)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	postTo := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	w := postTo("/admin/vehicles/bus-1/deactivate")
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/vehicles", w.Header().Get("Location"))
	assert.False(t, fake.vehicles["bus-1"].Active)
	var flashed bool
	for _, c := range w.Result().Cookies() {
		if c.Name == flashCookieName && c.Value == "vehicle_deactivated" {
			flashed = true
		}
	}
	assert.True(t, flashed, "expected vehicle_deactivated flash")

	w = postTo("/admin/vehicles/bus-1/activate")
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.True(t, fake.vehicles["bus-1"].Active)
	flashed = false
	for _, c := range w.Result().Cookies() {
		if c.Name == flashCookieName && c.Value == "vehicle_activated" {
			flashed = true
		}
	}
	assert.True(t, flashed, "expected vehicle_activated flash")
}
