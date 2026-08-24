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
		{"users", "/admin/users", "New user"},
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

// fakeUserStore is an in-memory double implementing userManager
// (UserLister/Getter/Creator/Updater/Activator/UserPasswordUpdater), covering
// everything the user pages need without a database.
type fakeUserStore struct {
	users           map[int64]*UserResponse
	nextID          int64
	passwordUpdates map[int64]string
}

func newFakeUserStore(users ...UserResponse) *fakeUserStore {
	m := make(map[int64]*UserResponse, len(users))
	var maxID int64
	for i := range users {
		u := users[i]
		m[u.ID] = &u
		if u.ID > maxID {
			maxID = u.ID
		}
	}
	return &fakeUserStore{users: m, nextID: maxID + 1, passwordUpdates: map[int64]string{}}
}

func (f *fakeUserStore) ListUsers(_ context.Context) ([]UserResponse, error) {
	out := make([]UserResponse, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeUserStore) GetUser(_ context.Context, id int64) (*UserResponse, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUserStore) CreateUser(_ context.Context, name, email, _, role string) (*UserResponse, error) {
	for _, u := range f.users {
		if u.Email == email {
			return nil, ErrDuplicateEmail
		}
	}
	id := f.nextID
	f.nextID++
	u := &UserResponse{ID: id, Name: name, Email: email, Role: role, Active: true}
	f.users[id] = u
	cp := *u
	return &cp, nil
}

func (f *fakeUserStore) UpdateUser(_ context.Context, id int64, name, email, role string) (*UserResponse, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	for otherID, other := range f.users {
		if otherID != id && other.Email == email {
			return nil, ErrDuplicateEmail
		}
	}
	u.Name = name
	u.Email = email
	u.Role = role
	cp := *u
	return &cp, nil
}

func (f *fakeUserStore) SetUserActive(_ context.Context, id int64, active bool) error {
	u, ok := f.users[id]
	if !ok {
		return ErrUserNotFound
	}
	u.Active = active
	return nil
}

func (f *fakeUserStore) UpdateUserPassword(_ context.Context, id int64, password string) error {
	if _, ok := f.users[id]; !ok {
		return ErrUserNotFound
	}
	f.passwordUpdates[id] = password
	return nil
}

// fakeAssignmentStore is an in-memory double implementing
// AssignmentCreator/Deleter/ListerByUser, keyed by user ID then vehicle ID.
type fakeAssignmentStore struct {
	byUser map[int64]map[string]bool
}

func newFakeAssignmentStore() *fakeAssignmentStore {
	return &fakeAssignmentStore{byUser: map[int64]map[string]bool{}}
}

func (f *fakeAssignmentStore) CreateAssignment(_ context.Context, userID int64, vehicleID string) (*AssignmentResponse, error) {
	if f.byUser[userID] == nil {
		f.byUser[userID] = map[string]bool{}
	}
	f.byUser[userID][vehicleID] = true
	return &AssignmentResponse{UserID: userID, VehicleID: vehicleID}, nil
}

func (f *fakeAssignmentStore) DeleteAssignment(_ context.Context, userID int64, vehicleID string) error {
	if f.byUser[userID] == nil || !f.byUser[userID][vehicleID] {
		return ErrAssignmentNotFound
	}
	delete(f.byUser[userID], vehicleID)
	return nil
}

func (f *fakeAssignmentStore) ListAssignmentsByUser(_ context.Context, userID int64) ([]AssignmentResponse, error) {
	out := make([]AssignmentResponse, 0)
	for vID := range f.byUser[userID] {
		out = append(out, AssignmentResponse{UserID: userID, VehicleID: vID})
	}
	return out, nil
}

// wireFakeUserStore points the adminUI's user-management and assignment
// fields at the given fakes, mirroring how newAdminUI wires them from a
// single appStore.
func wireFakeUserStore(ui *adminUI, u *fakeUserStore, a *fakeAssignmentStore) {
	ui.userManager = u
	ui.assignments = a
}

// TestUsersPageListsRealData verifies the list page renders each user's
// name, email, role badge, active/deactivated badge, and assigned-vehicle
// count, and that the old mock data is gone.
func TestUsersPageListsRealData(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(
		UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: "admin", Active: true},
		UserResponse{ID: 2, Name: "Dana Driver", Email: "dana@test.com", Role: "driver", Active: false},
	)
	assignments := newFakeAssignmentStore()
	_, err := assignments.CreateAssignment(context.Background(), 2, "bus-1")
	require.NoError(t, err)
	wireFakeUserStore(ui, users, assignments)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Ada Admin")
	assert.Contains(t, body, "ada@test.com")
	assert.Contains(t, body, "Admin")
	assert.Contains(t, body, "Dana Driver")
	assert.Contains(t, body, "Driver")
	assert.Contains(t, body, "Deactivated")
	assert.Contains(t, body, ">1<") // Dana's assigned-vehicle count
	assert.NotContains(t, body, "Chaitanya K", "mock data must be gone")
	assert.NotContains(t, body, "Last Seen", "mock Last Seen column must be dropped")
}

// TestUserCreate covers the create form's validation (short password, bad
// role, duplicate email all 422 and re-render submitted values) and the
// happy path (303 + flash).
func TestUserCreate(t *testing.T) {
	post := func(mux *http.ServeMux, name, email, password, role string) *httptest.ResponseRecorder {
		form := url.Values{"name": {name}, "email": {email}, "password": {password}, "role": {role}}
		req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("short password 422s and re-renders values", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(mux, "New Guy", "newguy@test.com", "short", "driver")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "password must be at least 8 characters")
		assert.Contains(t, w.Body.String(), `value="newguy@test.com"`)
	})

	t.Run("bad role 422s", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(mux, "New Guy", "newguy2@test.com", "longenoughpassword", "superadmin")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("duplicate email 422s", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeUserStore(ui, newFakeUserStore(UserResponse{ID: 1, Name: "Existing", Email: "dupe@test.com", Role: "driver", Active: true}), newFakeAssignmentStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(mux, "New Guy", "dupe@test.com", "longenoughpassword", "driver")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "email already exists")
	})

	t.Run("success redirects with flash", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		w := post(mux, "New Guy", "newguy3@test.com", "longenoughpassword", "driver")
		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/users", w.Header().Get("Location"))
		found := false
		for _, c := range w.Result().Cookies() {
			if c.Name == flashCookieName {
				assert.Equal(t, "user_created", c.Value)
				found = true
			}
		}
		assert.True(t, found, "expected user_created flash cookie")
	})
}

// TestUserEditPage covers rendering the edit form for a known user
// (including the assignments section) and 404ing for an unknown one.
func TestUserEditPage(t *testing.T) {
	t.Run("known id renders form with values and assignments", func(t *testing.T) {
		ui := newTestAdminUI(t)
		users := newFakeUserStore(UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: "admin", Active: true})
		assignments := newFakeAssignmentStore()
		_, err := assignments.CreateAssignment(context.Background(), 1, "bus-1")
		require.NoError(t, err)
		wireFakeUserStore(ui, users, assignments)
		wireFakeVehicleStore(ui, newFakeVehicleStore(
			VehicleResponse{ID: "bus-1", Label: "Bus One", Active: true},
			VehicleResponse{ID: "bus-2", Label: "Bus Two", Active: true},
			VehicleResponse{ID: "bus-3", Label: "Retired Bus", Active: false},
		))
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		req := httptest.NewRequest(http.MethodGet, "/admin/users/1/edit", nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Ada Admin")
		assert.Contains(t, body, "Bus One", "currently-assigned vehicle should be listed")
		assert.Contains(t, body, "Bus Two", "unassigned active vehicle should be selectable")
		assert.NotContains(t, body, "Retired Bus", "inactive vehicles must not be offered for assignment")
		assert.Contains(t, body, "leave blank to keep current")
	})

	t.Run("unknown id 404s", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		req := httptest.NewRequest(http.MethodGet, "/admin/users/999/edit", nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestUserUpdate covers the edit POST's name/email/role update, the optional
// password path calling UpdateUserPassword, and validation.
func TestUserUpdate(t *testing.T) {
	postTo := func(mux *http.ServeMux, path string, values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("updates name/email/role without touching password", func(t *testing.T) {
		ui := newTestAdminUI(t)
		users := newFakeUserStore(UserResponse{ID: 1, Name: "Old Name", Email: "old@test.com", Role: "driver", Active: true})
		wireFakeUserStore(ui, users, newFakeAssignmentStore())
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		form := url.Values{"name": {"New Name"}, "email": {"new@test.com"}, "role": {"admin"}, "password": {""}}
		w := postTo(mux, "/admin/users/1", form)
		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "/admin/users", w.Header().Get("Location"))
		assert.Equal(t, "New Name", users.users[1].Name)
		assert.Equal(t, "new@test.com", users.users[1].Email)
		assert.Equal(t, "admin", users.users[1].Role)
		assert.Empty(t, users.passwordUpdates, "password must not be touched when the field is blank")
	})

	t.Run("non-empty password also calls UpdateUserPassword", func(t *testing.T) {
		ui := newTestAdminUI(t)
		users := newFakeUserStore(UserResponse{ID: 1, Name: "Old Name", Email: "old@test.com", Role: "driver", Active: true})
		wireFakeUserStore(ui, users, newFakeAssignmentStore())
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		form := url.Values{"name": {"Old Name"}, "email": {"old@test.com"}, "role": {"driver"}, "password": {"newlongpassword"}}
		w := postTo(mux, "/admin/users/1", form)
		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, "newlongpassword", users.passwordUpdates[1])
	})

	t.Run("short password 422s without updating", func(t *testing.T) {
		ui := newTestAdminUI(t)
		users := newFakeUserStore(UserResponse{ID: 1, Name: "Old Name", Email: "old@test.com", Role: "driver", Active: true})
		wireFakeUserStore(ui, users, newFakeAssignmentStore())
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		form := url.Values{"name": {"Old Name"}, "email": {"old@test.com"}, "role": {"driver"}, "password": {"short"}}
		w := postTo(mux, "/admin/users/1", form)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "password must be at least 8 characters")
		assert.Empty(t, users.passwordUpdates)
	})

	t.Run("unknown id 404s", func(t *testing.T) {
		ui := newTestAdminUI(t)
		wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())
		wireFakeVehicleStore(ui, newFakeVehicleStore())
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)

		form := url.Values{"name": {"X"}, "email": {"x@test.com"}, "role": {"driver"}}
		w := postTo(mux, "/admin/users/999", form)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestUserDeactivateActivate verifies both POST endpoints redirect with the
// correct flash and flip the active flag.
func TestUserDeactivateActivate(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: "admin", Active: true})
	wireFakeUserStore(ui, users, newFakeAssignmentStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	postTo := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	w := postTo("/admin/users/1/deactivate")
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/users", w.Header().Get("Location"))
	assert.False(t, users.users[1].Active)
	var flashed bool
	for _, c := range w.Result().Cookies() {
		if c.Name == flashCookieName && c.Value == "user_deactivated" {
			flashed = true
		}
	}
	assert.True(t, flashed, "expected user_deactivated flash")

	w = postTo("/admin/users/1/activate")
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.True(t, users.users[1].Active)
	flashed = false
	for _, c := range w.Result().Cookies() {
		if c.Name == flashCookieName && c.Value == "user_activated" {
			flashed = true
		}
	}
	assert.True(t, flashed, "expected user_activated flash")

	t.Run("unknown id 404s", func(t *testing.T) {
		w := postTo("/admin/users/999/deactivate")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestUserAssignUnassignVehicle covers the assign/unassign POST endpoints:
// both redirect back to the edit page with the appropriate flash.
func TestUserAssignUnassignVehicle(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: "admin", Active: true})
	assignments := newFakeAssignmentStore()
	wireFakeUserStore(ui, users, assignments)
	wireFakeVehicleStore(ui, newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Bus One", Active: true}))
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	form := url.Values{"vehicle_id": {"bus-1"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/vehicles", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/users/1/edit", w.Header().Get("Location"))
	assert.True(t, assignments.byUser[1]["bus-1"])
	var flashed bool
	for _, c := range w.Result().Cookies() {
		if c.Name == flashCookieName && c.Value == "vehicle_assigned" {
			flashed = true
		}
	}
	assert.True(t, flashed, "expected vehicle_assigned flash")

	req = httptest.NewRequest(http.MethodPost, "/admin/users/1/vehicles/bus-1/remove", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/users/1/edit", w.Header().Get("Location"))
	assert.False(t, assignments.byUser[1]["bus-1"])
	flashed = false
	for _, c := range w.Result().Cookies() {
		if c.Name == flashCookieName && c.Value == "vehicle_unassigned" {
			flashed = true
		}
	}
	assert.True(t, flashed, "expected vehicle_unassigned flash")
}
