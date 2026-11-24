package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestAdminGateAllows(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, role := range []string{roleAdmin, roleViewer, roleDriver, roleRider, ""} {
		for _, method := range methods {
			want := role == roleAdmin || role == roleViewer && (method == http.MethodGet || method == http.MethodHead)
			assert.Equal(t, want, adminGateAllows(role, method), "%q %s", role, method)
		}
	}
}

// TestRequireAdmin_Viewer: a viewer's read reaches the handler with its
// claims and an actor on the context, and every other method is refused
// before the handler runs.
func TestRequireAdmin_Viewer(t *testing.T) {
	token, err := generateJWT(&User{ID: 7, Email: "viewer@test.com", Role: roleViewer}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			var reached *http.Request
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r; w.WriteHeader(http.StatusOK) })
			req := httptest.NewRequest(method, "/api/v1/admin/vehicles", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			requireAuth(testSecret, newFakeRevocations())(requireAdmin(false)(next)).ServeHTTP(w, req)

			if method == http.MethodGet || method == http.MethodHead {
				require.Equal(t, http.StatusOK, w.Code)
				require.NotNil(t, reached)
				claims, _ := reached.Context().Value(claimsKey).(jwt.MapClaims)
				assert.Equal(t, roleViewer, claims["role"])
				actor, ok := reached.Context().Value(auditActorKey).(auditActor)
				require.True(t, ok)
				assert.Equal(t, int64(7), actor.UserID)
				return
			}
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Equal(t, "admin access required", decodeError(t, w))
			assert.Nil(t, reached, "a viewer's change must not reach the handler")
		})
	}
}

// TestRequireDriverAPI_RefusesAViewer: the driver app's door admits drivers
// and admins, as requireAuth always has, and refuses a viewer and a rider.
func TestRequireDriverAPI_RefusesAViewer(t *testing.T) {
	riderTok, err := generateRiderJWT("rider-1", testSecret, time.Hour)
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"driver", tokenFor(t, roleDriver), http.StatusOK},
		{"admin", tokenFor(t, roleAdmin), http.StatusOK},
		{"viewer", tokenFor(t, roleViewer), http.StatusForbidden},
		{"rider", riderTok, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips/start", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			requireDriverAPI(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
			if tc.want == http.StatusForbidden {
				assert.Equal(t, "forbidden", decodeError(t, w))
			}
		})
	}
}

func tokenFor(t *testing.T, role string) string {
	t.Helper()
	tok, err := generateJWT(&User{ID: 9, Email: role + "@test.com", Role: role}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	return tok
}

// TestRequireAdminPage_Viewer: a viewer's session opens a page, and a form
// post from it is refused with a 403 before the handler runs. A driver's
// session is still sent to sign in.
func TestRequireAdminPage_Viewer(t *testing.T) {
	for _, tc := range []struct {
		role, method string
		want         int
	}{
		{roleViewer, http.MethodGet, http.StatusOK},
		{roleViewer, http.MethodHead, http.StatusOK},
		{roleViewer, http.MethodPost, http.StatusForbidden},
		{roleAdmin, http.MethodPost, http.StatusOK},
		{roleDriver, http.MethodGet, http.StatusSeeOther},
	} {
		t.Run(tc.role+" "+tc.method, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				claims, _ := r.Context().Value(claimsKey).(jwt.MapClaims)
				assert.Equal(t, tc.role, claims["role"])
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(tc.method, "/admin/vehicles", nil)
			req.AddCookie(cookieFor(t, tc.role))
			w := httptest.NewRecorder()
			requireAdminPage(testSecret, newFakeRevocations(), false)(next).ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
			assert.Equal(t, tc.want == http.StatusOK, reached)
		})
	}
}

func TestAdminLogin_Viewer(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcryptCost)
	require.NoError(t, err)
	viewer := &User{ID: 4, Email: "look@test.com", PasswordHash: string(hash), Role: roleViewer, Active: true}
	driver := &User{ID: 2, Email: "drv@test.com", PasswordHash: string(hash), Role: roleDriver, Active: true}
	ui := newTestAdminUI(t)
	ui.users = &fakeUserFetcher{users: map[string]*User{viewer.Email: viewer, driver.Email: driver}}
	ui.stats = &fakeAdminStats{vehicles: 3, drivers: 2}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	post := func(email string) *httptest.ResponseRecorder {
		form := url.Values{"email": {email}, "password": {"password123"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	w := post(viewer.Email)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Equal(t, "/admin/map", w.Header().Get("Location"))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	claims, err := parseSessionToken(cookies[0].Value, testSecret)
	require.NoError(t, err)
	assert.Equal(t, roleViewer, claims["role"], "the session carries the viewer role, not an admin's")

	// The session it was given opens the admin, and the sign-in page sends
	// it on rather than asking again.
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/map", w.Header().Get("Location"))

	w = post(driver.Email)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Admin access required")
	assert.Empty(t, w.Result().Cookies(), "a driver gets no session")
}

// viewerPagesStore holds one of everything a page draws a control for: an
// active and a deactivated vehicle, a driver with a vehicle and another to
// assign, a deactivated viewer, a running trip, an API key and an audit row.
type viewerPagesStore struct{ noopStore }

var viewerPagesVehicles = []VehicleResponse{
	{ID: "bus-1", Label: "Bus 1", Active: true},
	{ID: "bus-2", Label: "Bus 2", Active: false},
	{ID: "bus-3", Label: "Bus 3", Active: true},
}

func (s *viewerPagesStore) ListVehicles(_ context.Context) ([]VehicleResponse, error) {
	return viewerPagesVehicles, nil
}
func (s *viewerPagesStore) ListVehiclesPage(_ context.Context, _ VehicleFilter) ([]VehicleResponse, error) {
	return viewerPagesVehicles, nil
}
func (s *viewerPagesStore) GetVehicle(_ context.Context, id string) (*VehicleResponse, error) {
	return &VehicleResponse{ID: id, Label: "Bus 1", Active: true}, nil
}
func (s *viewerPagesStore) VehicleExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (s *viewerPagesStore) ListUsersPage(_ context.Context, _ UserFilter) ([]UserResponse, error) {
	return []UserResponse{
		{ID: 1, Name: "Ana Driver", Email: "ana@example.org", Role: roleDriver, Active: true},
		{ID: 2, Name: "Vic Viewer", Email: "vic@example.org", Role: roleViewer, Active: false},
	}, nil
}

// GetUser: account 2 is a viewer with no vehicles, account 4 a viewer still
// holding one, and any other id the driver Ana.
func (s *viewerPagesStore) GetUser(_ context.Context, id int64) (*UserResponse, error) {
	switch id {
	case 2:
		return &UserResponse{ID: id, Name: "Vic Viewer", Email: "vic@example.org", Role: roleViewer, Active: true}, nil
	case 4:
		return &UserResponse{ID: id, Name: "Val Viewer", Email: "val@example.org", Role: roleViewer, Active: true}, nil
	}
	return &UserResponse{ID: id, Name: "Ana Driver", Email: "ana@example.org", Role: roleDriver, Active: true}, nil
}
func (s *viewerPagesStore) ListAssignmentsByUser(_ context.Context, id int64) ([]AssignmentResponse, error) {
	if id == 2 {
		return make([]AssignmentResponse, 0), nil
	}
	return []AssignmentResponse{{UserID: id, VehicleID: "bus-1"}}, nil
}

var viewerPagesTrip = TripSummary{ID: 1, VehicleID: "bus-1", VehicleLabel: "Bus 1", UserID: 1, DriverName: "Ana Driver", RouteID: "R1", Status: "active"}

func (s *viewerPagesStore) ListTrips(_ context.Context, _ TripFilter) ([]TripSummary, error) {
	viewerPagesTrip.StartTime = time.Now().Add(-10 * time.Minute)
	return []TripSummary{viewerPagesTrip}, nil
}
func (s *viewerPagesStore) GetTripSummary(_ context.Context, _ int64) (*TripSummary, error) {
	trip := viewerPagesTrip
	trip.StartTime = time.Now().Add(-10 * time.Minute)
	return &trip, nil
}
func (s *viewerPagesStore) ListAPIKeysPage(_ context.Context, _, _ int) ([]APIKey, error) {
	return []APIKey{{ID: 1, Name: "OneBusAway", Active: true, CreatedAt: time.Now()}}, nil
}

// viewerPagesIP is the address in the audit row, which a viewer's page must
// not show.
const viewerPagesIP = "203.0.113.24"

func (s *viewerPagesStore) ListAuditEntries(_ context.Context, _ auditFilter) ([]auditEntry, error) {
	actor := int64(5)
	return []auditEntry{{
		ID: 1, At: time.Now(), ActorID: &actor, ActorEmail: "ops@example.org", ActorIP: viewerPagesIP,
		ObjectType: auditVehicle, ObjectID: "bus-1", Action: auditUpdate,
		Before: json.RawMessage(`{"label":"Bus One","agency_tag":"","active":true}`),
		After:  json.RawMessage(`{"label":"Bus 1","agency_tag":"","active":true}`),
	}}, nil
}

// newViewerTestHandler is the whole server, the JSON API and the admin
// pages, over viewerPagesStore, with the GTFS catalog registered.
func newViewerTestHandler(t *testing.T) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	ix, err := rider.LoadIndex(context.Background(), "rider/testdata/fixture.zip", nil, time.Now())
	require.NoError(t, err)
	catalog := newGTFSCatalog(func() *rider.Index { return ix }, rider.DefaultThresholds())
	catalog.now = func() time.Time { return catalogNow }
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	store := &viewerPagesStore{}
	h, err := newHandler(store, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, catalog, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	return h
}

// routeSamples are the values the route table puts in each wildcard. A
// route with a wildcard not named here fails the test until one is added.
var routeSamples = map[string]string{
	"{id}": "1", "{userID}": "1", "{vehicleID}": "bus-1", "{kind}": "vehicles",
	"{route_id}": "R1", "{trip_id}": "T1", "{$}": "",
}

var wildcard = regexp.MustCompile(`\{[^}]*\}`)

func samplePath(t *testing.T, pattern string) string {
	t.Helper()
	path := wildcard.ReplaceAllStringFunc(pattern, func(w string) string {
		v, ok := routeSamples[w]
		require.Truef(t, ok, "route %s has a wildcard %s with no sample value in routeSamples", pattern, w)
		return v
	})
	// A pattern ending in a slash is a subtree: ask for a page in it that
	// no other route serves.
	if strings.HasSuffix(path, "/") {
		path += "no-such-page"
	}
	return path
}

// publicPages are the admin routes outside requireAdminPage: signing in,
// signing out and the bare /admin redirect must work without a session.
var publicPages = map[string]struct{}{
	"GET /admin": {}, "GET /admin/{$}": {}, "GET /admin/login": {}, "POST /admin/login": {},
	"POST /admin/logout": {}, "GET /static/": {},
}

// viewerStaffRoutes are the routes behind requireAuth alone, which a viewer
// may use: signing out, and the GTFS catalog the admin's map and search read.
// A new route behind requireAuth alone fails TestViewer_EveryRoute until it
// is put behind requireDriverAPI or added here.
var viewerStaffRoutes = map[string]struct{}{
	"POST /api/v1/auth/logout":                 {},
	"GET /api/v1/gtfs/routes":                  {},
	"GET /api/v1/gtfs/routes/{route_id}/trips": {},
	"GET /api/v1/gtfs/trips/{trip_id}":         {},
}

// serveAs sends method path through h as role: the JSON API with a bearer
// token, a page with a session cookie.
func serveAs(t *testing.T, h http.Handler, role, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if strings.HasPrefix(path, "/admin") {
		req.AddCookie(cookieFor(t, role))
	} else {
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, role))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestViewer_EveryRoute walks every route the server's source registers and
// sends it through the real handler as a viewer, so a route added later is
// held to the rule without anyone adding it to a table:
//   - behind requireAdmin, a read answers the viewer exactly as it answers an
//     admin, and every other method is refused with requireAdmin's 403;
//   - behind requireDriverAPI, the viewer is refused at the door;
//   - behind requireAuth alone, only the routes in viewerStaffRoutes, each
//     answering the viewer as it answers an admin;
//   - an admin page answers a viewer's GET as it answers an admin's, and
//     refuses its form post with the page that says why.
func TestViewer_EveryRoute(t *testing.T) {
	h := newViewerTestHandler(t)
	checked := map[string]int{}

	for _, route := range extractRegisteredRoutes(t) {
		_, isUI := htmlUIRoutes[route.String()]
		_, isPublic := publicPages[route.String()]
		read := route.method == http.MethodGet || route.method == http.MethodHead

		var kind string
		switch {
		case route.admin:
			kind = "admin API"
		case route.driverAPI:
			kind = "driver API"
		case route.auth:
			kind = "staff API"
		case isUI && !isPublic:
			kind = "admin page"
		default:
			continue // no staff role reaches it: public, the feed's key, rider or metrics
		}
		path := samplePath(t, route.path)
		checked[kind]++

		t.Run(kind+" "+route.String(), func(t *testing.T) {
			if kind == "staff API" {
				_, ok := viewerStaffRoutes[route.String()]
				require.Truef(t, ok, "%s is behind requireAuth alone, so a viewer can use it: put it behind requireDriverAPI, or add it to viewerStaffRoutes", route)
			}
			viewer := serveAs(t, h, roleViewer, route.method, path)
			switch {
			case kind == "driver API":
				assert.Equal(t, http.StatusForbidden, viewer.Code)
				assert.Equal(t, "forbidden", decodeError(t, viewer))
			case kind == "admin page" && !read:
				assert.Equal(t, http.StatusForbidden, viewer.Code)
				assert.Contains(t, viewer.Body.String(), "Your account can look but not change")
			case kind == "admin API" && !read:
				assert.Equal(t, http.StatusForbidden, viewer.Code)
				assert.Equal(t, "admin access required", decodeError(t, viewer))
			default:
				// A read, or sign-out: the viewer gets what an admin gets.
				admin := serveAs(t, h, roleAdmin, route.method, path)
				assert.Equal(t, admin.Code, viewer.Code, "a viewer's %s must be answered as an admin's", route)
				assert.NotContains(t, []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusMethodNotAllowed}, viewer.Code)
				if kind == "admin page" {
					assert.NotEqual(t, http.StatusSeeOther, viewer.Code, "a viewer is not sent to sign in")
				}
			}
		})
	}

	// The walk found each kind of route, so an AST change that hid them
	// cannot pass this test by checking nothing.
	assert.GreaterOrEqual(t, checked["admin API"], 28)
	assert.GreaterOrEqual(t, checked["driver API"], 6)
	assert.Equal(t, len(viewerStaffRoutes), checked["staff API"])
	assert.GreaterOrEqual(t, checked["admin page"], 36)
}

var (
	postForm   = regexp.MustCompile(`<form\b[^>]*\bmethod="post"[^>]*>`)
	writeLinks = regexp.MustCompile(`href="(/admin/(?:vehicles|users)/new|/admin/import/(?:vehicles|drivers))"`)
)

// writeControls lists what a page offers to change something: each form it
// posts, except signing out, and each link to a page for adding or
// importing.
func writeControls(body string) []string {
	found := make([]string, 0)
	for _, form := range postForm.FindAllString(body, -1) {
		if !strings.Contains(form, `action="/admin/logout"`) {
			found = append(found, form)
		}
	}
	for _, link := range writeLinks.FindAllStringSubmatch(body, -1) {
		found = append(found, link[1])
	}
	return found
}

// TestViewer_PagesHaveNoWriteControls renders every admin page a GET
// reaches, as an admin and as a viewer. The viewer's has no form that
// changes anything and no link to add or import, and says it is a viewer's.
// The pages that have controls show them to the admin, so the fixture is
// one that draws them.
func TestViewer_PagesHaveNoWriteControls(t *testing.T) {
	h := newViewerTestHandler(t)

	paths := []string{"/admin/vehicles?include_inactive=1", "/admin/import/drivers", "/admin/audit?type=vehicle&id=bus-1"}
	for _, route := range extractRegisteredRoutes(t) {
		_, isUI := htmlUIRoutes[route.String()]
		_, isPublic := publicPages[route.String()]
		if isUI && !isPublic && route.method == http.MethodGet && !strings.HasSuffix(route.path, ".csv") && !strings.HasSuffix(route.path, "/") {
			paths = append(paths, samplePath(t, route.path))
		}
	}
	require.GreaterOrEqual(t, len(paths), 20)

	withControls := []string{
		"/admin/vehicles", "/admin/vehicles?include_inactive=1", "/admin/vehicles/new", "/admin/vehicles/1/edit",
		"/admin/users", "/admin/users/new", "/admin/users/1/edit", "/admin/import/vehicles", "/admin/import/drivers",
		"/admin/trips", "/admin/trips/1", "/admin/api-keys",
	}
	for _, p := range withControls {
		require.Contains(t, paths, p)
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			admin := serveAs(t, h, roleAdmin, http.MethodGet, path)
			viewer := serveAs(t, h, roleViewer, http.MethodGet, path)
			// Analytics answers 503 with no schedule loaded, to both alike.
			require.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, admin.Code, admin.Body.String())
			require.Equal(t, admin.Code, viewer.Code, viewer.Body.String())

			if slices.Contains(withControls, path) {
				assert.NotEmpty(t, writeControls(admin.Body.String()), "the admin's page has its controls")
			}
			body := viewer.Body.String()
			assert.Empty(t, writeControls(body), "a viewer's page offers no change")
			assert.Contains(t, body, `title="This account can look at every page but not change anything">Viewer</span>`)
			assert.Contains(t, body, "Viewer: can look at every page, can't change anything")
			assert.NotContains(t, admin.Body.String(), "This account can look at every page", "an admin's top bar says nothing of viewers")
		})
	}
}

// TestViewer_FormsAreReadOnly: the pages that are mostly a form show its
// fields to a viewer read-only, with no password field and nothing to save.
func TestViewer_FormsAreReadOnly(t *testing.T) {
	h := newViewerTestHandler(t)

	vehicle := serveAs(t, h, roleViewer, http.MethodGet, "/admin/vehicles/bus-1/edit").Body.String()
	for _, field := range []string{`id="id"`, `id="label"`, `id="agency_tag"`} {
		assert.Regexp(t, `<input [^>]*`+field+`[^>]* readonly`, vehicle)
	}
	assert.NotContains(t, vehicle, "Save changes")
	assert.Contains(t, vehicle, "<title>Vehicle · Vehicle Tracker</title>", "a viewer is not told it is editing")
	assert.Contains(t, vehicle, "You are signed in as a viewer, so you can look but not change anything.")
	assert.Contains(t, vehicle, `/api/v1/admin/vehicles/bus-1/locations?format=csv`, "the location download is a read, and stays")

	user := serveAs(t, h, roleViewer, http.MethodGet, "/admin/users/1/edit").Body.String()
	assert.Regexp(t, `<input [^>]*id="name"[^>]* readonly`, user)
	assert.Regexp(t, `<input [^>]*id="email"[^>]* readonly`, user)
	assert.Regexp(t, `<input [^>]*id="role"[^>]* readonly value="Driver"`, user)
	assert.NotContains(t, user, `name="password"`)
	assert.NotContains(t, user, "<select")
	assert.NotContains(t, user, "Save changes")
	assert.Contains(t, user, "<title>Account · Vehicle Tracker</title>")
	assert.Contains(t, user, "Bus 1", "the vehicles the driver may pick are listed")
	assert.Contains(t, user, "Setup sheet", "the setup sheet is a read, and stays")

	admin := serveAs(t, h, roleAdmin, http.MethodGet, "/admin/users/1/edit").Body.String()
	assert.Contains(t, admin, `name="password"`)
	assert.Contains(t, admin, `<option value="viewer">Viewer</option>`, "an admin can make an account a viewer")
	assert.NotContains(t, admin, "You are signed in as a viewer")
	assert.Contains(t, admin, "<title>Edit user · Vehicle Tracker</title>")
}

// TestViewer_AuditLogLeavesOutTheAddress: a viewer sees who made each change
// on the Audit log, but not the address it came from; an admin sees both.
func TestViewer_AuditLogLeavesOutTheAddress(t *testing.T) {
	h := newViewerTestHandler(t)
	admin := serveAs(t, h, roleAdmin, http.MethodGet, "/admin/audit").Body.String()
	viewer := serveAs(t, h, roleViewer, http.MethodGet, "/admin/audit").Body.String()

	assert.Contains(t, admin, viewerPagesIP)
	assert.Contains(t, viewer, "ops@example.org")
	assert.Contains(t, viewer, "Bus One")
	assert.NotContains(t, viewer, viewerPagesIP)
}

// TestViewer_WelcomeHasNoAddButton: a server with no vehicle or no driver
// opens on the welcome. A viewer sees how far along its steps are, but not
// the button for the next one, a change it can't make; an admin sees both.
// TestViewer_PagesHaveNoWriteControls misses the button, as its fixture has
// vehicles and drivers and the steps link to list pages.
func TestViewer_WelcomeHasNoAddButton(t *testing.T) {
	for _, tc := range []struct {
		vehicles, drivers int
		step, current     string
		action            string
	}{
		{0, 0, "Step 1 of 4", "/admin/vehicles", "Add vehicles"},
		{1, 0, "Step 2 of 4", "/admin/users", "Add drivers"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			_, mux := newOverviewMux(t, tc.vehicles, tc.drivers)
			current := `<a class="step current" href="` + tc.current + `">`

			admin := serveAs(t, mux, roleAdmin, http.MethodGet, "/admin/dashboard")
			require.Equal(t, http.StatusOK, admin.Code)
			assert.Contains(t, admin.Body.String(), current)
			assert.Contains(t, admin.Body.String(), `<span class="btn btn-primary">`+tc.action+`</span>`)
			assert.Contains(t, admin.Body.String(), "Four steps. A driver's phone does the rest")

			viewer := serveAs(t, mux, roleViewer, http.MethodGet, "/admin/dashboard")
			require.Equal(t, http.StatusOK, viewer.Code)
			body := viewer.Body.String()
			require.Contains(t, body, "Viewer: can look at every page, can't change anything")
			require.Contains(t, body, "Get your first vehicle on the map")
			assert.NotContains(t, body, ">"+tc.action+"<", "a viewer is not offered the change")
			assert.Contains(t, body, `aria-label="`+tc.step+`"`)
			assert.Contains(t, body, current, "the step to take next is still marked")
			assert.Contains(t, body, "An admin takes these four steps. A driver's phone does the rest")
			assert.NotContains(t, body, "Four steps.")
		})
	}
}

// TestUsersPage_Viewers: the Drivers and users page counts viewers, can show
// only them, and names the role on a viewer's row.
func TestUsersPage_Viewers(t *testing.T) {
	ui := newTestAdminUI(t)
	ui.userPager = &viewerPagesStore{}
	ui.stats = &fakeAdminStats{drivers: 11, admins: 1, viewers: 2}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/users")
	assert.Contains(t, body, "11 drivers, 1 admin and 2 viewers")
	assert.Contains(t, body, `<a href="/admin/users?page=1&amp;role=viewer">Viewers</a>`)
	assert.Contains(t, body, `<span class="muted">Viewer</span>`)

	pager := &mockUserPager{}
	ui.userPager = pager
	getAdminPage(t, mux, "/admin/users?role=viewer")
	assert.Equal(t, roleViewer, pager.gotFilter.Role)
}

// TestUserHandlers_AcceptViewer: the admin API creates a viewer, makes an
// account one, and lists viewers alone.
func TestUserHandlers_AcceptViewer(t *testing.T) {
	created := newSampleUser()
	created.Role = roleViewer
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users",
		bytes.NewReader([]byte(`{"name":"Vic","email":"vic@example.com","password":"securepass","role":"viewer"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCreateUser(&mockUserCreator{user: created})(w, req)
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	updater := &mockUserUpdater{user: created, getUserResp: &UserResponse{ID: 1, Role: roleDriver, Active: true}}
	w = putUser(t, updater, 1, `{"name":"Vic","email":"vic@example.com","role":"viewer"}`)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, updater.updateUserCalls)

	pager := &mockUserPager{}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?role=viewer", nil)
	w = httptest.NewRecorder()
	handleListUsers(pager)(w, req)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, roleViewer, pager.gotFilter.Role)
}

// TestUserHandlers_LastAdminCannotBecomeAViewer: a viewer is not an admin, so
// the last active admin cannot be made one, through the API or the form.
func TestUserHandlers_LastAdminCannotBecomeAViewer(t *testing.T) {
	updater := &mockUserUpdater{user: newSampleUser(), getUserResp: &UserResponse{ID: 1, Role: roleAdmin, Active: true}, countAdmins: 1}
	w := putUser(t, updater, 1, `{"name":"Alice","email":"alice@example.com","role":"viewer"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "cannot demote the last active admin", decodeErrorResponse(t, w))
	assert.Zero(t, updater.updateUserCalls)
}

// TestViewerAccountPage_NoVehicles: a viewer can't drive, so its account page
// has no vehicles to pick and nothing to assign. One still holding a vehicle
// shows it, with Remove but no Assign, so it can be taken away.
func TestViewerAccountPage_NoVehicles(t *testing.T) {
	h := newViewerTestHandler(t)
	const section = "Vehicles this driver may pick"

	driver := serveAs(t, h, roleAdmin, http.MethodGet, "/admin/users/1/edit").Body.String()
	assert.Contains(t, driver, section)
	assert.Contains(t, driver, `action="/admin/users/1/vehicles"`)

	viewer := serveAs(t, h, roleAdmin, http.MethodGet, "/admin/users/2/edit")
	require.Equal(t, http.StatusOK, viewer.Code)
	assert.NotContains(t, viewer.Body.String(), section)
	assert.NotContains(t, viewer.Body.String(), `action="/admin/users/2/vehicles"`)
	assert.Contains(t, viewer.Body.String(), `action="/admin/users/2"`, "the account itself can still be edited")

	holding := serveAs(t, h, roleAdmin, http.MethodGet, "/admin/users/4/edit").Body.String()
	assert.Contains(t, holding, section)
	assert.Contains(t, holding, `action="/admin/users/4/vehicles/bus-1/remove"`)
	assert.NotContains(t, holding, `action="/admin/users/4/vehicles"`)
}

// viewerAssignments is the page tests' assignment store answering every
// assignment as the real one answers one for a viewer.
type viewerAssignments struct{ *fakeAssignmentStore }

func (viewerAssignments) CreateAssignment(context.Context, int64, string) (*AssignmentResponse, error) {
	return nil, ErrAssignToViewer
}

// viewerOnTripUsers is the page tests' user store answering a change as the
// real one answers a change to viewer while the account has a trip running.
type viewerOnTripUsers struct{ *fakeUserStore }

func (viewerOnTripUsers) UpdateUser(context.Context, int64, string, string, string) (*UserResponse, error) {
	return nil, ErrViewerWithTrip
}

// TestAssigningToAViewer_Refused: the API and the Assign form both refuse a
// viewer as the account a vehicle goes to, with a 409 saying why, as they
// refuse an inactive vehicle.
func TestAssigningToAViewer_Refused(t *testing.T) {
	body, err := json.Marshal(AssignmentRequest{UserID: 2, VehicleID: "bus-1"})
	require.NoError(t, err)
	w := postAssignment(handleCreateAssignment(&mockAssignmentCreator{err: ErrAssignToViewer}), body, "application/json")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "a viewer can't drive, so no vehicles can be assigned to one", decodeAssignmentError(t, w))

	ui := newTestAdminUI(t)
	users := newFakeUserStore(UserResponse{ID: 2, Name: "Vic Viewer", Email: "vic@test.com", Role: roleViewer, Active: true})
	assignments := newFakeAssignmentStore()
	wireFakeUserStore(ui, users, assignments)
	ui.assignments = viewerAssignments{assignments}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/2/vehicles", strings.NewReader("vehicle_id=bus-1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, roleAdmin))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "a viewer can't drive, so no vehicles can be assigned to one\n", w.Body.String())
	assert.Empty(t, assignments.byUser[2])
}

// TestChangeToViewerWithATrip_Refused: the API and the account form both
// refuse to make an account a viewer while it has a trip running, with a 409
// saying to end the trip.
func TestChangeToViewerWithATrip_Refused(t *testing.T) {
	updater := &mockUserUpdater{err: ErrViewerWithTrip, getUserResp: &UserResponse{ID: 1, Role: roleDriver, Active: true}}
	w := putUser(t, updater, 1, `{"name":"Ana","email":"ana@example.com","role":"viewer"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "end this account's running trip before making it a viewer", decodeErrorResponse(t, w))

	ui := newTestAdminUI(t)
	users := newFakeUserStore(UserResponse{ID: 1, Name: "Ana Driver", Email: "ana@test.com", Role: roleDriver, Active: true})
	wireFakeUserStore(ui, users, newFakeAssignmentStore())
	ui.userManager = viewerOnTripUsers{users}
	wireFakeVehicleStore(ui, newFakeVehicleStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	form := url.Values{"name": {"Ana Driver"}, "email": {"ana@test.com"}, "role": {roleViewer}}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, roleAdmin))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "end this account&#39;s running trip before making it a viewer")
	assert.Contains(t, w.Body.String(), `id="role-err"`, "the error is on the role field")
	assert.Equal(t, roleDriver, users.users[1].Role)
}
