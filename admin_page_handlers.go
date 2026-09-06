package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// adminUIConfig holds the runtime knobs for the admin UI that vary by
// deployment (whether it's served at all, whether proxy headers are trusted
// for client-IP/HTTPS detection, and the feed staleness threshold shown on
// the dashboard's feed-health strip — mirrors main's STALENESS_THRESHOLD).
type adminUIConfig struct {
	enabled            bool
	trustProxy         bool
	stalenessThreshold time.Duration
}

// adminStatsStore provides the aggregate counts shown on the admin
// dashboard.
type adminStatsStore interface {
	CountActiveVehicles(ctx context.Context) (int, error)
	CountActiveUsersByRole(ctx context.Context, role string) (int, error)
	CountActiveTrips(ctx context.Context) (int, error)
}

// vehicleEditor is the narrow interface the vehicle edit/deactivate/activate
// pages need: label/agency-tag updates and active-flag toggling. It's kept
// separate from VehicleManager because UpsertVehicle (used by the create
// page) force-reactivates a vehicle, which the edit/deactivate/activate
// flows must not do.
type vehicleEditor interface {
	VehicleInfoUpdater
	VehicleActivator
}

// userManager is the narrow interface the user CRUD pages need. It's kept
// separate from UserFetcher (used only by the login flow) so the login path
// doesn't depend on write methods it never calls.
type userManager interface {
	UserLister
	UserGetter
	UserCreator
	UserUpdater
	UserActivator
	UserPasswordUpdater
}

// assignmentManager is the narrow interface the user edit page's
// vehicle-assignments section needs.
type assignmentManager interface {
	AssignmentCreator
	AssignmentDeleter
	AssignmentListerByUser
}

// adminUI owns the parsed templates and dependencies for all admin pages.
type adminUI struct {
	tmpl           *embeddedTemplates
	users          UserFetcher
	tracker        *Tracker
	stats          adminStatsStore
	activeTrips    ActiveTripLister
	trips          TripLister
	vehicles       VehicleManager
	vehiclePager   VehiclePager
	userPager      UserPager
	vehicleEditor  vehicleEditor
	vehicleCreator VehicleCreator
	userManager    userManager
	assignments    assignmentManager
	jwtSecret      []byte
	loginLimiter   *LoginRateLimiter
	cfg            adminUIConfig
}

// newAdminUI loads the embedded templates and wires the admin UI's
// dependencies. It returns an error rather than panicking so callers can log
// it with context and exit cleanly. store supplies the user, stats, active
// trips, and vehicle dependencies (it implements appStore, a superset of all
// of them).
func newAdminUI(store appStore, tracker *Tracker, jwtSecret []byte, limiter *LoginRateLimiter, cfg adminUIConfig) (*adminUI, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &adminUI{
		tmpl:           tmpl,
		users:          store,
		tracker:        tracker,
		stats:          store,
		activeTrips:    store,
		trips:          store,
		vehicles:       store,
		vehiclePager:   store,
		userPager:      store,
		vehicleEditor:  store,
		vehicleCreator: store,
		userManager:    store,
		assignments:    store,
		jwtSecret:      jwtSecret,
		loginLimiter:   limiter,
		cfg:            cfg,
	}, nil
}

// registerAdminUI registers the admin routes on mux. It does not mount
// static assets — that's the caller's responsibility (main's handler
// construction), keeping this function focused on admin routes only.
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
	mux.Handle("GET /admin/vehicles/new", protect(http.HandlerFunc(ui.vehicleNewPage)))
	mux.Handle("POST /admin/vehicles", protect(http.HandlerFunc(ui.vehicleCreate)))
	mux.Handle("GET /admin/vehicles/{id}/edit", protect(http.HandlerFunc(ui.vehicleEditPage)))
	mux.Handle("POST /admin/vehicles/{id}", protect(http.HandlerFunc(ui.vehicleUpdate)))
	mux.Handle("POST /admin/vehicles/{id}/deactivate", protect(http.HandlerFunc(ui.vehicleDeactivate)))
	mux.Handle("POST /admin/vehicles/{id}/activate", protect(http.HandlerFunc(ui.vehicleActivate)))
	mux.Handle("GET /admin/users", protect(http.HandlerFunc(ui.usersPage)))
	mux.Handle("GET /admin/users/new", protect(http.HandlerFunc(ui.userNewPage)))
	mux.Handle("POST /admin/users", protect(http.HandlerFunc(ui.userCreate)))
	mux.Handle("GET /admin/users/{id}/edit", protect(http.HandlerFunc(ui.userEditPage)))
	mux.Handle("POST /admin/users/{id}", protect(http.HandlerFunc(ui.userUpdate)))
	mux.Handle("POST /admin/users/{id}/deactivate", protect(http.HandlerFunc(ui.userDeactivate)))
	mux.Handle("POST /admin/users/{id}/activate", protect(http.HandlerFunc(ui.userActivate)))
	mux.Handle("POST /admin/users/{id}/vehicles", protect(http.HandlerFunc(ui.userAssignVehicle)))
	mux.Handle("POST /admin/users/{id}/vehicles/{vehicleID}/remove", protect(http.HandlerFunc(ui.userUnassignVehicle)))
	mux.Handle("GET /admin/trips", protect(http.HandlerFunc(ui.tripsPage)))
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

// loginSubmit authenticates the form POST. Failure responses are
// intentionally identical (401 "Invalid email or password.") for a wrong
// password, an unknown email, and a deactivated user, and the bcrypt compare
// against dummyHash on unknown email keeps the timing side-channel closed —
// this mirrors the JSON login handler in auth.go exactly, including checking
// Active only after the password compare succeeds.
func (ui *adminUI) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ui.renderLogin(w, http.StatusBadRequest, "Invalid form submission.", "")
		return
	}
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	if email == "" || password == "" {
		ui.renderLogin(w, http.StatusUnprocessableEntity, "Email and password are required.", email)
		return
	}
	if !ui.loginLimiter.Allow(clientIP(r, ui.cfg.trustProxy), email) {
		ui.renderLogin(w, http.StatusTooManyRequests, "Too many attempts, try again shortly.", email)
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
	// Successful authentication: clear the per-email rate-limit window so
	// legitimate repeat logins aren't counted toward the brute-force budget
	// (mirrors handleLogin in auth.go).
	ui.loginLimiter.ResetEmail(email)
	if user.Role != roleAdmin {
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

// renderLogin renders the login form with the given status (renderInto
// writes the status line after a successful render).
func (ui *adminUI) renderLogin(w http.ResponseWriter, status int, errMsg, email string) {
	renderInto(w, status, ui.tmpl.public, "login.html", "login.html", map[string]interface{}{
		"Title": "Sign In", "Error": errMsg, "Email": email,
	})
}

func (ui *adminUI) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// renderAdmin renders an admin page through the shared base.html layout,
// which pulls in the view's {{define "content"}} block, and threads through
// any pending flash message. takeFlash's clearing Set-Cookie must land before
// the status line is committed, which renderInto guarantees by calling
// WriteHeader(status) only after the template has rendered successfully.
func (ui *adminUI) renderAdmin(w http.ResponseWriter, r *http.Request, status int, view string, data map[string]interface{}) {
	data["Flash"] = takeFlash(w, r)
	renderInto(w, status, ui.tmpl.admin, view, "base.html", data)
}

// mapPage renders the live fleet map, or (with a ?trip_id= query param) a
// single trip's trail. trip_id must be a valid int64 when present; a
// non-numeric value produces 404 rather than silently falling back to live
// mode, since it can't identify any real trip.
func (ui *adminUI) mapPage(w http.ResponseWriter, r *http.Request) {
	tripID := ""
	if raw := r.URL.Query().Get("trip_id"); raw != "" {
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			http.NotFound(w, r)
			return
		}
		tripID = raw
	}
	ui.renderAdmin(w, r, http.StatusOK, "map.html", map[string]interface{}{
		"Title":  "Live Map",
		"Page":   "map",
		"TripID": tripID,
	})
}

// dashboardRow is a single row in the dashboard's recent-activity table: a
// vehicle's label, its current route (if it has an active trip), and how
// long ago it last reported.
type dashboardRow struct {
	Label    string
	RouteID  string
	LastSeen string
}

// recentActivityLimit caps the dashboard's recent-activity table so it stays
// scannable regardless of fleet size.
const recentActivityLimit = 10

// dashboardPage renders the admin dashboard: aggregate stats from the store,
// the tracker's live feed status, and the most recently reported vehicles
// joined with their labels and (if any) active trip's route.
func (ui *adminUI) dashboardPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	totalVehicles, err := ui.stats.CountActiveVehicles(ctx)
	if err != nil {
		slog.Error("dashboard: count active vehicles", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	totalDrivers, err := ui.stats.CountActiveUsersByRole(ctx, roleDriver)
	if err != nil {
		slog.Error("dashboard: count active drivers", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	activeTripCount, err := ui.stats.CountActiveTrips(ctx)
	if err != nil {
		slog.Error("dashboard: count active trips", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	vehicleList, err := ui.vehicles.ListVehicles(ctx)
	if err != nil {
		slog.Error("dashboard: list vehicles", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	labels := make(map[string]string, len(vehicleList))
	for _, v := range vehicleList {
		labels[v.ID] = v.Label
	}

	tripsByVehicle, err := ui.activeTrips.ListActiveTripsByVehicle(ctx)
	if err != nil {
		slog.Error("dashboard: list active trips by vehicle", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	status := ui.tracker.Status()

	active := ui.tracker.ActiveVehicles()
	sort.Slice(active, func(i, j int) bool { return active[i].UpdatedAt.After(active[j].UpdatedAt) })
	if len(active) > recentActivityLimit {
		active = active[:recentActivityLimit]
	}
	recent := make([]dashboardRow, 0, len(active))
	for _, v := range active {
		label := v.VehicleID
		if l, ok := labels[v.VehicleID]; ok {
			label = l
		}
		var routeID string
		if trip, ok := tripsByVehicle[v.VehicleID]; ok {
			routeID = trip.RouteID
		}
		recent = append(recent, dashboardRow{
			Label:    label,
			RouteID:  routeID,
			LastSeen: humanizeAge(v.UpdatedAt),
		})
	}

	lastUpdate := "never"
	if status.LastUpdate != nil {
		lastUpdate = humanizeAge(*status.LastUpdate)
	}

	ui.renderAdmin(w, r, http.StatusOK, "dashboard.html", map[string]interface{}{
		"Title":              "Dashboard",
		"Page":               "dashboard",
		"TotalVehicles":      totalVehicles,
		"ActiveVehicles":     status.ActiveVehicles,
		"TotalDrivers":       totalDrivers,
		"ActiveTrips":        activeTripCount,
		"LastUpdate":         lastUpdate,
		"StalenessThreshold": humanizeDuration(ui.cfg.stalenessThreshold),
		"RecentVehicles":     recent,
	})
}

// humanizeAge renders how long ago t was, in a compact human form: "just
// now" for anything under a minute, then whole minutes, then whole hours.
func humanizeAge(t time.Time) string {
	age := time.Since(t)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%d min ago", int(age.Minutes()))
	default:
		return fmt.Sprintf("%d h ago", int(age.Hours()))
	}
}

// humanizeDuration renders a duration in the same compact style as
// humanizeAge, without the "ago" suffix — used for the feed-health strip's
// staleness threshold.
func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
}

// adminPageSize is the number of rows shown per page on the admin list
// pages. Each list is queried with adminPageSize+1 so an extra row past the
// page boundary reveals whether there's a next page (HasMore), without a
// separate COUNT query.
const adminPageSize = 50

// maxAdminPage bounds the ?page= query param so the offset arithmetic can
// never overflow into a negative OFFSET; values past it fall back to page 1.
const maxAdminPage = 1_000_000

// adminPageNumber reads the ?page= param for the admin list pages. A
// missing, non-numeric, or out-of-range value falls back to page 1 rather
// than erroring, since it's a bookmarkable/shareable URL param that's easy
// to hand-edit into something invalid.
func adminPageNumber(q url.Values) int {
	if raw := q.Get("page"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= maxAdminPage {
			return n
		}
	}
	return 1
}

// adminPageOffset converts a page number into the SQL offset for a page of
// adminPageSize rows. maxAdminPage keeps the result well inside int32, the
// offset type the paged store queries take.
func adminPageOffset(page int) int {
	return (page - 1) * adminPageSize
}

// vehicleRow is a single row in the vehicle list table: the vehicle's
// stored fields plus whatever live state we can join in (last-seen from the
// tracker, current driver from the active-trips map).
type vehicleRow struct {
	ID        string
	Label     string
	AgencyTag string
	Active    bool
	LastSeen  string
	Driver    string
}

// vehiclesPageURL builds an /admin/vehicles link preserving the inactive
// filter with page set to the given page number, for the prev/next
// pagination links.
func vehiclesPageURL(includeInactive bool, page int) string {
	v := url.Values{}
	if includeInactive {
		v.Set("include_inactive", "1")
	}
	v.Set("page", strconv.Itoa(page))
	return "/admin/vehicles?" + v.Encode()
}

// vehiclesPage renders the vehicle list: one page of vehicles from the
// store, joined with the tracker's live last-seen data and the current
// driver (if any) from the active-trips map. Rows are ordered newest-first
// by the store. Inactive vehicles are hidden unless ?include_inactive=1 is
// set; that filter runs in SQL so a page holds a full page of rows.
func (ui *adminUI) vehiclesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()
	includeInactive := query.Get("include_inactive") == "1"
	page := adminPageNumber(query)

	vehicles, err := ui.vehiclePager.ListVehiclesPage(ctx, includeInactive, adminPageSize+1, int32(adminPageOffset(page)))
	if err != nil {
		slog.Error("vehicles: list vehicles page", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	hasMore := len(vehicles) > adminPageSize
	if hasMore {
		vehicles = vehicles[:adminPageSize]
	}

	reporting := ui.tracker.ActiveVehicles()
	lastSeen := make(map[string]string, len(reporting))
	for _, v := range reporting {
		lastSeen[v.VehicleID] = humanizeAge(v.UpdatedAt)
	}

	tripsByVehicle, err := ui.activeTrips.ListActiveTripsByVehicle(ctx)
	if err != nil {
		slog.Error("vehicles: list active trips by vehicle", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rows := make([]vehicleRow, 0, len(vehicles))
	for _, v := range vehicles {
		row := vehicleRow{ID: v.ID, Label: v.Label, AgencyTag: v.AgencyTag, Active: v.Active}
		row.LastSeen = lastSeen[v.ID]
		if trip, ok := tripsByVehicle[v.ID]; ok {
			row.Driver = trip.DriverName
		}
		rows = append(rows, row)
	}

	ui.renderAdmin(w, r, http.StatusOK, "vehicles.html", map[string]interface{}{
		"Title":           "Vehicles",
		"Page":            "vehicles",
		"Vehicles":        rows,
		"IncludeInactive": includeInactive,
		"PageNum":         page,
		"HasMore":         hasMore,
		"PrevURL":         vehiclesPageURL(includeInactive, page-1),
		"NextURL":         vehiclesPageURL(includeInactive, page+1),
	})
}

// vehicleFormData carries the vehicle_form.html template's fields for both
// the create and edit flows (distinguished by IsEdit), including any
// submitted values and validation error to re-render on failure.
type vehicleFormData struct {
	IsEdit    bool
	ID        string
	Label     string
	AgencyTag string
	Error     string
}

func (ui *adminUI) renderVehicleForm(w http.ResponseWriter, r *http.Request, status int, data vehicleFormData) {
	title := "New Vehicle"
	if data.IsEdit {
		title = "Edit Vehicle"
	}
	ui.renderAdmin(w, r, status, "vehicle_form.html", map[string]interface{}{
		"Title":     title,
		"Page":      "vehicles",
		"IsEdit":    data.IsEdit,
		"ID":        data.ID,
		"Label":     data.Label,
		"AgencyTag": data.AgencyTag,
		"Error":     data.Error,
	})
}

// vehicleNewPage renders the blank create-vehicle form.
func (ui *adminUI) vehicleNewPage(w http.ResponseWriter, r *http.Request) {
	ui.renderVehicleForm(w, r, http.StatusOK, vehicleFormData{})
}

// vehicleCreate validates and saves a new vehicle. It reuses
// validateVehicleID — the same helper the JSON API uses — so form and API
// validation stay in lockstep, and reports the exact same error text on
// failure. A 422 re-renders the form with the submitted values so the admin
// doesn't have to retype everything.
//
// The insert is a single conditional CreateVehicle (ON CONFLICT DO NOTHING)
// rather than a VehicleExists check followed by UpsertVehicle: the
// check-then-act version had a race window where two concurrent creates with
// the same id both passed the check and the loser silently overwrote (and
// force-reactivated) the winner's vehicle.
func (ui *adminUI) vehicleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ui.renderVehicleForm(w, r, http.StatusBadRequest, vehicleFormData{Error: "Invalid form submission."})
		return
	}
	id := r.PostFormValue("id")
	label := r.PostFormValue("label")
	agencyTag := r.PostFormValue("agency_tag")

	if err := validateVehicleID(id); err != nil {
		ui.renderVehicleForm(w, r, http.StatusUnprocessableEntity, vehicleFormData{ID: id, Label: label, AgencyTag: agencyTag, Error: err.Error()})
		return
	}

	created, err := ui.vehicleCreator.CreateVehicle(r.Context(), id, label, agencyTag)
	if err != nil {
		slog.Error("vehicle create: create vehicle", "vehicle_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !created {
		ui.renderVehicleForm(w, r, http.StatusUnprocessableEntity, vehicleFormData{ID: id, Label: label, AgencyTag: agencyTag, Error: "vehicle id already exists"})
		return
	}
	setFlash(w, "vehicle_created")
	http.Redirect(w, r, "/admin/vehicles", http.StatusSeeOther)
}

// vehicleEditPage renders the edit form pre-filled with the vehicle's
// current label/agency tag. An unknown id 404s rather than showing a blank
// or error-banner form.
func (ui *adminUI) vehicleEditPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, err := ui.vehicles.GetVehicle(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		slog.Error("vehicle edit: get vehicle", "vehicle_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	ui.renderVehicleForm(w, r, http.StatusOK, vehicleFormData{IsEdit: true, ID: v.ID, Label: v.Label, AgencyTag: v.AgencyTag})
}

// vehicleUpdate saves label/agency_tag edits for an existing vehicle. The id
// is read-only in the form (it's part of the URL, not submitted), and this
// uses UpdateVehicleInfo rather than UpsertVehicle so it never touches the
// active flag.
func (ui *adminUI) vehicleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		ui.renderVehicleForm(w, r, http.StatusBadRequest, vehicleFormData{IsEdit: true, ID: id, Error: "Invalid form submission."})
		return
	}
	label := r.PostFormValue("label")
	agencyTag := r.PostFormValue("agency_tag")

	if err := ui.vehicleEditor.UpdateVehicleInfo(r.Context(), id, label, agencyTag); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		slog.Error("vehicle update: update info", "vehicle_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setFlash(w, "vehicle_updated")
	http.Redirect(w, r, "/admin/vehicles", http.StatusSeeOther)
}

// vehicleDeactivate and vehicleActivate toggle a vehicle's active flag via
// setVehicleActive, sharing the same 404/error/flash/redirect handling.
func (ui *adminUI) vehicleDeactivate(w http.ResponseWriter, r *http.Request) {
	ui.setVehicleActive(w, r, false, "vehicle_deactivated")
}

func (ui *adminUI) vehicleActivate(w http.ResponseWriter, r *http.Request) {
	ui.setVehicleActive(w, r, true, "vehicle_activated")
}

func (ui *adminUI) setVehicleActive(w http.ResponseWriter, r *http.Request, active bool, flashCode string) {
	id := r.PathValue("id")
	if err := ui.vehicleEditor.SetVehicleActive(r.Context(), id, active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		slog.Error("vehicle set active", "vehicle_id", id, "active", active, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setFlash(w, flashCode)
	http.Redirect(w, r, "/admin/vehicles", http.StatusSeeOther)
}

// minPasswordLength is the minimum length required for a user password. It is
// enforced everywhere passwords are set — the users API, the admin UI forms,
// and the bootstrap admin path — via validatePassword.
const minPasswordLength = 8

// validatePassword enforces the shared minimum-password-length policy.
func validatePassword(password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	return nil
}

// userRow is a single row in the user list table: the user's stored fields
// plus how many vehicles are currently assigned to them.
type userRow struct {
	ID           int64
	Name         string
	Email        string
	Role         string
	Active       bool
	VehicleCount int
}

// usersPageURL builds an /admin/users link with page set to the given page
// number, for the prev/next pagination links.
func usersPageURL(page int) string {
	v := url.Values{}
	v.Set("page", strconv.Itoa(page))
	return "/admin/users?" + v.Encode()
}

// usersPage renders the user list: one page of users from the store, each
// joined with its assigned-vehicle count via a per-user
// ListAssignmentsByUser call. This is an N+1 query pattern, but it's fine at
// admin scale (dozens of users, not thousands) and keeps the assignment
// store's query surface simple (no bulk "counts by user" query needed just
// for this list). Paging the list bounds it further: at most adminPageSize
// lookups per request, whatever the table holds.
func (ui *adminUI) usersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := adminPageNumber(r.URL.Query())

	users, err := ui.userPager.ListUsersPage(ctx, adminPageSize+1, int32(adminPageOffset(page)))
	if err != nil {
		slog.Error("users: list users page", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	hasMore := len(users) > adminPageSize
	if hasMore {
		users = users[:adminPageSize]
	}

	rows := make([]userRow, 0, len(users))
	for _, u := range users {
		assignments, err := ui.assignments.ListAssignmentsByUser(ctx, u.ID)
		if err != nil {
			slog.Error("users: list assignments", "user_id", u.ID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		rows = append(rows, userRow{
			ID:           u.ID,
			Name:         u.Name,
			Email:        u.Email,
			Role:         u.Role,
			Active:       u.Active,
			VehicleCount: len(assignments),
		})
	}

	ui.renderAdmin(w, r, http.StatusOK, "users.html", map[string]interface{}{
		"Title":   "Users",
		"Page":    "users",
		"Users":   rows,
		"PageNum": page,
		"HasMore": hasMore,
		"PrevURL": usersPageURL(page - 1),
		"NextURL": usersPageURL(page + 1),
	})
}

// assignmentRow is a single currently-assigned vehicle shown in the user
// edit page's assignments section, with the vehicle's label joined in for
// display (assignments themselves only carry the vehicle id).
type assignmentRow struct {
	VehicleID string
	Label     string
}

// userFormData carries the user_form.html template's fields for both the
// create and edit flows (distinguished by IsEdit), including any submitted
// values and validation error to re-render on failure. Assignments and
// AvailableVehicles are only populated (and only rendered) in edit mode.
type userFormData struct {
	IsEdit            bool
	ID                string
	Name              string
	Email             string
	Role              string
	Error             string
	Assignments       []assignmentRow
	AvailableVehicles []VehicleResponse
}

func (ui *adminUI) renderUserForm(w http.ResponseWriter, r *http.Request, status int, data userFormData) {
	title := "New User"
	if data.IsEdit {
		title = "Edit User"
	}
	ui.renderAdmin(w, r, status, "user_form.html", map[string]interface{}{
		"Title":             title,
		"Page":              "users",
		"IsEdit":            data.IsEdit,
		"ID":                data.ID,
		"Name":              data.Name,
		"Email":             data.Email,
		"Role":              data.Role,
		"Error":             data.Error,
		"Assignments":       data.Assignments,
		"AvailableVehicles": data.AvailableVehicles,
	})
}

// validTripStatus reports whether s is a valid trips status filter value,
// shared by the trips JSON endpoint and the trips admin page.
func validTripStatus(s string) bool {
	return s == "" || s == "active" || s == "completed"
}

// userNewPage renders the blank create-user form.
func (ui *adminUI) userNewPage(w http.ResponseWriter, r *http.Request) {
	ui.renderUserForm(w, r, http.StatusOK, userFormData{Role: roleDriver})
}

// userCreate validates and saves a new user. A 422 re-renders the form with
// the submitted name/email/role (but never the password) so the admin
// doesn't have to retype everything.
func (ui *adminUI) userCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ui.renderUserForm(w, r, http.StatusBadRequest, userFormData{Error: "Invalid form submission."})
		return
	}
	name := r.PostFormValue("name")
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	role := r.PostFormValue("role")

	// The form marks name/email required client-side, but form submissions
	// aren't trustworthy — enforce it server-side with the same error text as
	// the JSON API (handleCreateUser).
	if name == "" {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "name is required"})
		return
	}
	if email == "" {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "email is required"})
		return
	}
	if err := validatePassword(password); err != nil {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: err.Error()})
		return
	}
	if !validUserRole(role) {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "role must be driver or admin"})
		return
	}

	if _, err := ui.userManager.CreateUser(r.Context(), name, email, password, role); err != nil {
		if errors.Is(err, ErrDuplicateEmail) {
			ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "email already exists"})
			return
		}
		slog.Error("user create: create user", "email", email, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setFlash(w, "user_created")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// buildUserFormData assembles the edit form's assignments section: the
// user's current assignments joined with vehicle labels, and the active
// vehicles not already assigned to them (candidates for the assign
// dropdown).
func (ui *adminUI) buildUserFormData(ctx context.Context, id int64, name, email, role string) (userFormData, error) {
	assignments, err := ui.assignments.ListAssignmentsByUser(ctx, id)
	if err != nil {
		return userFormData{}, fmt.Errorf("list assignments: %w", err)
	}
	allVehicles, err := ui.vehicles.ListVehicles(ctx)
	if err != nil {
		return userFormData{}, fmt.Errorf("list vehicles: %w", err)
	}

	vehicleLabels := make(map[string]string, len(allVehicles))
	for _, v := range allVehicles {
		vehicleLabels[v.ID] = v.Label
	}

	assigned := make(map[string]bool, len(assignments))
	rows := make([]assignmentRow, 0, len(assignments))
	for _, a := range assignments {
		assigned[a.VehicleID] = true
		rows = append(rows, assignmentRow{VehicleID: a.VehicleID, Label: vehicleLabels[a.VehicleID]})
	}

	available := make([]VehicleResponse, 0, len(allVehicles))
	for _, v := range allVehicles {
		if v.Active && !assigned[v.ID] {
			available = append(available, v)
		}
	}

	return userFormData{
		IsEdit:            true,
		ID:                strconv.FormatInt(id, 10),
		Name:              name,
		Email:             email,
		Role:              role,
		Assignments:       rows,
		AvailableVehicles: available,
	}, nil
}

// userEditPage renders the edit form pre-filled with the user's current
// name/email/role and the assignments section. An unknown id 404s rather
// than showing a blank or error-banner form.
func (ui *adminUI) userEditPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := ui.userManager.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("user edit: get user", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data, err := ui.buildUserFormData(r.Context(), u.ID, u.Name, u.Email, u.Role)
	if err != nil {
		slog.Error("user edit: build form data", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	ui.renderUserForm(w, r, http.StatusOK, data)
}

// renderUserEditError re-renders the edit form for id with the submitted
// name/email/role values and an error message, refetching the current
// assignments/available-vehicles for the assignments section so it isn't
// blank on a validation failure.
func (ui *adminUI) renderUserEditError(w http.ResponseWriter, r *http.Request, status int, id int64, name, email, role, errMsg string) {
	data, err := ui.buildUserFormData(r.Context(), id, name, email, role)
	if err != nil {
		slog.Error("user edit: rebuild form data", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data.Error = errMsg
	ui.renderUserForm(w, r, status, data)
}

// userUpdate saves name/email/role edits for an existing user. If the
// password field is non-empty, it's validated the same way as the create
// form and, on success, also written via UpdateUserPassword — leaving it
// blank keeps the current password untouched.
func (ui *adminUI) userUpdate(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		ui.renderUserForm(w, r, http.StatusBadRequest, userFormData{IsEdit: true, ID: idStr, Error: "Invalid form submission."})
		return
	}
	name := r.PostFormValue("name")
	email := r.PostFormValue("email")
	role := r.PostFormValue("role")
	password := r.PostFormValue("password")

	// Server-side required checks mirroring the JSON API (handleUpdateUser);
	// the form's client-side `required` attributes aren't trustworthy.
	if name == "" {
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "name is required")
		return
	}
	if email == "" {
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "email is required")
		return
	}
	if !validUserRole(role) {
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "role must be driver or admin")
		return
	}
	if password != "" {
		if err := validatePassword(password); err != nil {
			ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, err.Error())
			return
		}
	}

	// Refuse to demote the last active admin: with no active admin left, no
	// one can sign in to the admin UI (and ADMIN_BOOTSTRAP doesn't recover,
	// since bootstrapAdmin counts existing admins regardless of active flag).
	// The check-then-act window here is acceptable for an admin UI guard.
	current, err := ui.userManager.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("user update: get user", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if current.Role == roleAdmin && current.Active && role != roleAdmin {
		admins, err := ui.stats.CountActiveUsersByRole(r.Context(), roleAdmin)
		if err != nil {
			slog.Error("user update: count active admins", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if admins <= 1 {
			ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "cannot demote the last active admin")
			return
		}
	}

	if _, err := ui.userManager.UpdateUser(r.Context(), id, name, email, role); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, ErrDuplicateEmail) {
			ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "email already exists")
			return
		}
		slog.Error("user update: update user", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if password != "" {
		if err := ui.userManager.UpdateUserPassword(r.Context(), id, password); err != nil {
			if errors.Is(err, ErrUserNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("user update: update password", "user_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	setFlash(w, "user_updated")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// userDeactivate and userActivate toggle a user's active flag via
// setUserActive, sharing the same 404/error/flash/redirect handling.
func (ui *adminUI) userDeactivate(w http.ResponseWriter, r *http.Request) {
	ui.setUserActive(w, r, false, "user_deactivated")
}

func (ui *adminUI) userActivate(w http.ResponseWriter, r *http.Request) {
	ui.setUserActive(w, r, true, "user_activated")
}

func (ui *adminUI) setUserActive(w http.ResponseWriter, r *http.Request, active bool, flashCode string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Refuse to deactivate the last active admin: with no active admin left,
	// no one can sign in to the admin UI, and restarting with ADMIN_BOOTSTRAP
	// doesn't recover (bootstrapAdmin counts existing admins regardless of
	// active flag), so the lockout would require manual SQL to undo. The
	// check-then-act window here is acceptable for an admin UI guard.
	if !active {
		target, err := ui.userManager.GetUser(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("user set active: get user", "user_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if target.Role == roleAdmin && target.Active {
			admins, err := ui.stats.CountActiveUsersByRole(r.Context(), roleAdmin)
			if err != nil {
				slog.Error("user set active: count active admins", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if admins <= 1 {
				http.Error(w, "cannot deactivate the last active admin", http.StatusUnprocessableEntity)
				return
			}
		}
	}
	if err := ui.userManager.SetUserActive(r.Context(), id, active); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("user set active", "user_id", id, "active", active, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setFlash(w, flashCode)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// userAssignVehicle assigns a vehicle (form field vehicle_id) to the user
// and redirects back to the edit page, where the newly-assigned vehicle now
// shows up in the current-assignments list. An empty vehicle_id is a no-op
// (no flash) rather than attempting an assignment with an empty vehicle id.
//
// CreateAssignment's sentinel errors are mapped the same way the JSON API
// does in handleCreateAssignment (assignment_handlers.go): ErrAssignmentExists
// (a double-submit or a race with another admin) is treated as success — the
// end state is what the admin wanted, so it just redirects without a flash —
// and ErrVehicleNotFoundFK 404s rather than surfacing a raw 500.
func (ui *adminUI) userAssignVehicle(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	vehicleID := r.PostFormValue("vehicle_id")
	if vehicleID != "" {
		if _, err := ui.assignments.CreateAssignment(r.Context(), id, vehicleID); err != nil {
			if errors.Is(err, ErrVehicleNotFoundFK) {
				http.NotFound(w, r)
				return
			}
			if !errors.Is(err, ErrAssignmentExists) {
				slog.Error("user assign vehicle", "user_id", id, "vehicle_id", vehicleID, "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			// ErrAssignmentExists: fall through to the redirect below without
			// a flash — the assignment already exists, which is the state
			// the admin wanted.
		} else {
			setFlash(w, "vehicle_assigned")
		}
	}
	http.Redirect(w, r, "/admin/users/"+idStr+"/edit", http.StatusSeeOther)
}

// userUnassignVehicle removes a vehicle assignment (vehicleID from the URL)
// and redirects back to the edit page. An assignment that's already gone
// (e.g. a double-submitted remove) 404s rather than silently succeeding.
func (ui *adminUI) userUnassignVehicle(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	vehicleID := r.PathValue("vehicleID")
	if err := ui.assignments.DeleteAssignment(r.Context(), id, vehicleID); err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("user unassign vehicle", "user_id", id, "vehicle_id", vehicleID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setFlash(w, "vehicle_unassigned")
	http.Redirect(w, r, "/admin/users/"+idStr+"/edit", http.StatusSeeOther)
}

// tripRow is a single row in the trips table: a trip's joined display fields
// plus pre-formatted start/end times and duration, ready for the template.
type tripRow struct {
	ID           int64
	VehicleLabel string
	DriverName   string
	RouteID      string
	GtfsTripID   string
	Start        string
	End          string
	Status       string
	Duration     string
}

// formatTripTimestamp renders a trip's start/end time in the admin UI's
// fixed UTC display format, regardless of the server's local timezone.
func formatTripTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// formatTripDuration renders how long a completed trip took, rounded to the
// nearest minute. Active trips (nil end) have no duration yet.
func formatTripDuration(start time.Time, end *time.Time) string {
	if end == nil {
		return "—"
	}
	d := end.Sub(start).Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// tripsPageURL builds a /admin/trips link preserving the current filter
// values with page set to the given page number, for the prev/next
// pagination links.
func tripsPageURL(status, vehicleID, q string, page int) string {
	v := url.Values{}
	if status != "" {
		v.Set("status", status)
	}
	if vehicleID != "" {
		v.Set("vehicle_id", vehicleID)
	}
	if q != "" {
		v.Set("q", q)
	}
	v.Set("page", strconv.Itoa(page))
	return "/admin/trips?" + v.Encode()
}

// tripsPage renders the trip history list: real trips from the store,
// filtered by status/vehicle/free-text query, adminPageSize per page.
// status must be ""/active/completed (else 400); an invalid or missing page
// falls back to page 1, as on the other admin list pages.
func (ui *adminUI) tripsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	status := query.Get("status")
	if !validTripStatus(status) {
		http.Error(w, "status must be active or completed", http.StatusBadRequest)
		return
	}
	vehicleID := query.Get("vehicle_id")
	q := query.Get("q")

	page := adminPageNumber(query)

	filter := TripFilter{
		Status:    status,
		VehicleID: vehicleID,
		Q:         q,
		Limit:     adminPageSize + 1,
		Offset:    adminPageOffset(page),
	}

	trips, err := ui.trips.ListTrips(ctx, filter)
	if err != nil {
		slog.Error("trips: list trips", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	hasMore := len(trips) > adminPageSize
	if hasMore {
		trips = trips[:adminPageSize]
	}

	allVehicles, err := ui.vehicles.ListVehicles(ctx)
	if err != nil {
		slog.Error("trips: list vehicles", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	activeVehicles := make([]VehicleResponse, 0, len(allVehicles))
	for _, v := range allVehicles {
		if v.Active {
			activeVehicles = append(activeVehicles, v)
		}
	}
	sort.Slice(activeVehicles, func(i, j int) bool { return activeVehicles[i].ID < activeVehicles[j].ID })

	rows := make([]tripRow, 0, len(trips))
	for _, t := range trips {
		row := tripRow{
			ID:           t.ID,
			VehicleLabel: t.VehicleLabel,
			DriverName:   t.DriverName,
			RouteID:      t.RouteID,
			GtfsTripID:   t.GtfsTripID,
			Start:        formatTripTimestamp(t.StartTime),
			End:          "—",
			Status:       t.Status,
			Duration:     formatTripDuration(t.StartTime, t.EndTime),
		}
		if t.EndTime != nil {
			row.End = formatTripTimestamp(*t.EndTime)
		}
		rows = append(rows, row)
	}

	ui.renderAdmin(w, r, http.StatusOK, "trips.html", map[string]interface{}{
		"Title":     "Trips",
		"Page":      "trips",
		"Trips":     rows,
		"Vehicles":  activeVehicles,
		"Status":    status,
		"VehicleID": vehicleID,
		"Q":         q,
		"PageNum":   page,
		"HasMore":   hasMore,
		"PrevURL":   tripsPageURL(status, vehicleID, q, page-1),
		"NextURL":   tripsPageURL(status, vehicleID, q, page+1),
	})
}
