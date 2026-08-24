package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

// adminUI owns the parsed templates and dependencies for all admin pages.
type adminUI struct {
	tmpl           *embeddedTemplates
	users          UserFetcher
	tracker        *Tracker
	stats          adminStatsStore
	activeTrips    ActiveTripLister
	vehicles       VehicleManager
	vehicleEditor  vehicleEditor
	vehicleChecker VehicleChecker
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
		vehicles:       store,
		vehicleEditor:  store,
		vehicleChecker: store,
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
	mux.Handle("GET /admin/trips", protect(http.HandlerFunc(ui.tripsPage)))
	// Remaining CRUD form routes (users) are added by later tasks.
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

// renderLogin sets the response status before rendering (renderInto never
// calls WriteHeader on the success path), so non-200 statuses land correctly.
func (ui *adminUI) renderLogin(w http.ResponseWriter, status int, errMsg, email string) {
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	renderInto(w, ui.tmpl.public, "login.html", "login.html", map[string]interface{}{
		"Title": "Sign In", "Error": errMsg, "Email": email,
	})
}

func (ui *adminUI) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// renderAdmin renders an admin page through the shared base.html layout,
// which pulls in the view's {{define "content"}} block, and threads through
// any pending flash message.
func (ui *adminUI) renderAdmin(w http.ResponseWriter, r *http.Request, view string, data map[string]interface{}) {
	data["Flash"] = takeFlash(w, r)
	renderInto(w, ui.tmpl.admin, view, "base.html", data)
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
	ui.renderAdmin(w, r, "map.html", map[string]interface{}{
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

	totalDrivers, err := ui.stats.CountActiveUsersByRole(ctx, "driver")
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

	ui.renderAdmin(w, r, "dashboard.html", map[string]interface{}{
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

// vehiclesPage renders the vehicle list: real vehicles from the store,
// joined with the tracker's live last-seen data and the current driver (if
// any) from the active-trips map. Inactive vehicles are hidden unless
// ?include_inactive=1 is set — the store itself always returns everything;
// filtering happens here so the store's ListVehicles stays a plain listing.
func (ui *adminUI) vehiclesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	includeInactive := r.URL.Query().Get("include_inactive") == "1"

	all, err := ui.vehicles.ListVehicles(ctx)
	if err != nil {
		slog.Error("vehicles: list vehicles", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	lastSeen := make(map[string]string, len(all))
	for _, v := range ui.tracker.ActiveVehicles() {
		lastSeen[v.VehicleID] = humanizeAge(v.UpdatedAt)
	}

	tripsByVehicle, err := ui.activeTrips.ListActiveTripsByVehicle(ctx)
	if err != nil {
		slog.Error("vehicles: list active trips by vehicle", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rows := make([]vehicleRow, 0, len(all))
	for _, v := range all {
		if !v.Active && !includeInactive {
			continue
		}
		row := vehicleRow{ID: v.ID, Label: v.Label, AgencyTag: v.AgencyTag, Active: v.Active}
		row.LastSeen = lastSeen[v.ID]
		if trip, ok := tripsByVehicle[v.ID]; ok {
			row.Driver = trip.DriverName
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	ui.renderAdmin(w, r, "vehicles.html", map[string]interface{}{
		"Title":           "Vehicles",
		"Page":            "vehicles",
		"Vehicles":        rows,
		"IncludeInactive": includeInactive,
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
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	title := "New Vehicle"
	if data.IsEdit {
		title = "Edit Vehicle"
	}
	ui.renderAdmin(w, r, "vehicle_form.html", map[string]interface{}{
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

	exists, err := ui.vehicleChecker.VehicleExists(r.Context(), id)
	if err != nil {
		slog.Error("vehicle create: check existence", "vehicle_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if exists {
		ui.renderVehicleForm(w, r, http.StatusUnprocessableEntity, vehicleFormData{ID: id, Label: label, AgencyTag: agencyTag, Error: "vehicle id already exists"})
		return
	}

	if _, err := ui.vehicles.UpsertVehicle(r.Context(), id, label, agencyTag); err != nil {
		slog.Error("vehicle create: upsert vehicle", "vehicle_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
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

func (ui *adminUI) usersPage(w http.ResponseWriter, r *http.Request) {
	ui.renderAdmin(w, r, "users.html", map[string]interface{}{
		"Title": "Users",
		"Page":  "users",
		"Users": []map[string]string{
			{"Name": "Chaitanya K", "Email": "kbc@transit.co.ke", "Role": "driver", "LastSeen": "Today"},
			{"Name": "To Holland", "Email": "tom@transit.co.ke", "Role": "driver", "LastSeen": "Today"},
			{"Name": "Open transit", "Email": "brian@transit.co.ke", "Role": "driver", "LastSeen": "Yesterday"},
		},
	})
}

func (ui *adminUI) tripsPage(w http.ResponseWriter, r *http.Request) {
	ui.renderAdmin(w, r, "trips.html", map[string]interface{}{
		"Title": "Trips",
		"Page":  "trips",
		"Trips": []map[string]string{
			{"ID": "T001", "Vehicle": "Bus 001", "Driver": "Tom Hiddlestone", "Route": "Route A", "Start": "07:00", "End": "08:45", "Status": "completed"},
			{"ID": "T002", "Vehicle": "Bus 002", "Driver": "Chris Hensworth", "Route": "Route B", "Start": "07:15", "End": "—", "Status": "active"},
			{"ID": "T003", "Vehicle": "Bus 003", "Driver": "Bruce Wayne", "Route": "Route C", "Start": "06:45", "End": "08:30", "Status": "completed"},
		},
	})
}
