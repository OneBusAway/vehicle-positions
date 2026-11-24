package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/OneBusAway/vehicle-positions/rider"
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

// apiKeyPageStore is what the API keys page needs: the key operations the
// JSON API uses, and the count of active keys for the page's headline.
type apiKeyPageStore interface {
	APIKeyManager
	APIKeyCounter
}

// adminUI owns the parsed templates and dependencies for all admin pages.
type adminUI struct {
	tmpl           *embeddedTemplates
	users          UserFetcher
	signIns        SignInRecorder
	failedSignIns  FailedSignInNoter
	tracker        *Tracker
	alerts         *alertWatcher
	clearedAlerts  ClearedAlertLister
	alertsRaised   AlertsRaisedLister
	reports        *serviceReporter
	health         *feedHealth
	overview       overviewReports
	stats          adminStatsStore
	activeTrips    ActiveTripLister
	trips          TripLister
	tripEnder      TripByIDEnder
	tripTrails     TripTrailStore
	vehicles       VehicleManager
	vehiclePager   VehiclePager
	userPager      UserPager
	vehicleEditor  vehicleEditor
	vehicleCreator VehicleCreator
	userManager    userManager
	assignments    assignmentManager
	importer       CSVImporter
	apiKeys        apiKeyPageStore
	auditLog       AuditLogLister
	rider          riderPageSource
	rides          RideLister
	rideDays       RideDayCounter
	tokenChecker   TokenChecker
	tokenRevoker   TokenRevoker
	jwtSecret      []byte
	loginLimiter   *LoginRateLimiter
	cfg            adminUIConfig
}

// newAdminUI loads the embedded templates and wires the admin UI's
// dependencies. It returns an error rather than panicking so callers can log
// it with context and exit cleanly. store supplies the user, stats, active
// trips, and vehicle dependencies (it implements appStore, a superset of all
// of them).
func newAdminUI(store appStore, tracker *Tracker, alerts *alertWatcher, reports *serviceReporter, health *feedHealth, jwtSecret []byte, limiter *LoginRateLimiter, cfg adminUIConfig) (*adminUI, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &adminUI{
		tmpl:           tmpl,
		users:          store,
		signIns:        store,
		failedSignIns:  store,
		tracker:        tracker,
		alerts:         alerts,
		clearedAlerts:  store,
		alertsRaised:   store,
		reports:        reports,
		health:         health,
		stats:          store,
		activeTrips:    store,
		trips:          store,
		tripEnder:      store,
		tripTrails:     store,
		vehicles:       store,
		vehiclePager:   store,
		userPager:      store,
		vehicleEditor:  store,
		vehicleCreator: store,
		userManager:    store,
		assignments:    store,
		importer:       store,
		apiKeys:        store,
		auditLog:       store,
		rider:          riderOff{},
		rides:          store,
		rideDays:       store,
		tokenChecker:   store,
		tokenRevoker:   store,
		jwtSecret:      jwtSecret,
		loginLimiter:   limiter,
		cfg:            cfg,
	}, nil
}

// registerAdminUI registers the admin routes on mux. It does not mount
// static assets — that's the caller's responsibility (main's handler
// construction), keeping this function focused on admin routes only.
func registerAdminUI(mux *http.ServeMux, ui *adminUI) {
	// Every page behind the guard answers a bare error with the error page,
	// and so does the guard when it refuses a viewer's form post.
	guard := requireAdminPage(ui.jwtSecret, ui.tokenChecker, ui.cfg.trustProxy)
	protect := func(next http.Handler) http.Handler { return ui.errorPages(guard(next)) }

	mux.HandleFunc("GET /admin/login", ui.loginPage)
	mux.HandleFunc("POST /admin/login", ui.loginSubmit)
	mux.HandleFunc("POST /admin/logout", ui.logout)
	mux.HandleFunc("GET /admin", ui.rootRedirect)
	mux.HandleFunc("GET /admin/{$}", ui.rootRedirect)
	mux.Handle("GET /admin/more", protect(http.HandlerFunc(ui.morePage)))
	mux.Handle("GET /admin/dashboard", protect(http.HandlerFunc(ui.dashboardPage)))
	mux.Handle("GET /admin/map", protect(http.HandlerFunc(ui.mapPage)))
	mux.Handle("GET /admin/alerts", protect(http.HandlerFunc(ui.alertsPage)))
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
	mux.Handle("GET /admin/driver-setup", protect(http.HandlerFunc(ui.driverSetupPage)))
	mux.Handle("GET /admin/import/{kind}", protect(http.HandlerFunc(ui.importPage)))
	mux.Handle("GET /admin/import/{kind}/template.csv", protect(http.HandlerFunc(ui.importTemplate)))
	mux.Handle("POST /admin/import/{kind}", protect(http.HandlerFunc(ui.importUpload)))
	mux.Handle("POST /admin/import/{kind}/confirm", protect(http.HandlerFunc(ui.importConfirm)))
	mux.Handle("GET /admin/trips", protect(http.HandlerFunc(ui.tripsPage)))
	mux.Handle("GET /admin/trips/{id}", protect(http.HandlerFunc(ui.tripDetailPage)))
	mux.Handle("GET /admin/reports", protect(http.HandlerFunc(ui.reportsPage)))
	mux.Handle("GET /admin/health", protect(http.HandlerFunc(ui.healthPage)))
	mux.Handle("GET /admin/rider", protect(http.HandlerFunc(ui.riderPage)))
	mux.Handle("POST /admin/trips/{id}/end", protect(http.HandlerFunc(ui.tripEnd)))
	mux.Handle("GET /admin/api-keys", protect(http.HandlerFunc(ui.apiKeysPage)))
	mux.Handle("POST /admin/api-keys", protect(http.HandlerFunc(ui.apiKeyCreate)))
	mux.Handle("POST /admin/api-keys/{id}/deactivate", protect(http.HandlerFunc(ui.apiKeyDeactivate)))
	mux.Handle("GET /admin/audit", protect(http.HandlerFunc(ui.auditPage)))
	// An address under /admin/ that is no page: signed in, the error page
	// says so; signed out, the guard sends the visitor to sign in like any
	// other, which tells them nothing about which pages exist.
	mux.Handle("GET /admin/", protect(http.NotFoundHandler()))
}

func (ui *adminUI) rootRedirect(w http.ResponseWriter, r *http.Request) {
	if _, ok := adminClaimsFromCookie(r, ui.jwtSecret, ui.tokenChecker); ok {
		http.Redirect(w, r, ui.home(r.Context()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// home is where a signed-in admin starts: the live map, or the welcome on
// the Overview while the server has no vehicle or no driver to show on it.
// A count that fails is logged and the admin goes to the map, which does
// not need it.
func (ui *adminUI) home(ctx context.Context) string {
	vehicles, err := ui.stats.CountActiveVehicles(ctx)
	if err != nil {
		slog.Error("admin home: count active vehicles", "error", err)
		return "/admin/map"
	}
	drivers, err := ui.stats.CountActiveUsersByRole(ctx, roleDriver)
	if err != nil {
		slog.Error("admin home: count active drivers", "error", err)
		return "/admin/map"
	}
	if needsWelcome(vehicles, drivers) {
		return "/admin/dashboard"
	}
	return "/admin/map"
}

func (ui *adminUI) loginPage(w http.ResponseWriter, r *http.Request) {
	next := loginNext(r.URL.Query().Get("next"))
	if _, ok := adminClaimsFromCookie(r, ui.jwtSecret, ui.tokenChecker); ok {
		if next == "" {
			next = ui.home(r.Context())
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	ui.renderLogin(w, http.StatusOK, "", "", next)
}

// loginSubmit authenticates the form POST. Failure responses are
// intentionally identical (401 "Invalid email or password.") for a wrong
// password, an unknown email, and a deactivated user, and the bcrypt compare
// against dummyHash on unknown email keeps the timing side-channel closed —
// this mirrors the JSON login handler in auth.go exactly, including checking
// Active only after the password compare succeeds. A failure is noted for the
// audit log without waiting for its row, to keep it closed too.
func (ui *adminUI) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ui.renderLogin(w, http.StatusBadRequest, "Invalid form submission.", "", "")
		return
	}
	next := loginNext(r.PostFormValue("next"))
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	if email == "" || password == "" {
		ui.renderLogin(w, http.StatusUnprocessableEntity, "Email and password are required.", email, next)
		return
	}
	if !ui.loginLimiter.Allow(clientIP(r, ui.cfg.trustProxy), email) {
		ui.renderLogin(w, http.StatusTooManyRequests, "Too many attempts, try again shortly.", email, next)
		return
	}
	user, err := ui.users.GetUserByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			ui.renderLogin(w, http.StatusUnauthorized, "Invalid email or password.", email, next)
			return
		}
		slog.Error("admin login: database error", "error", err)
		ui.renderLogin(w, http.StatusInternalServerError, "Something went wrong. Try again.", email, next)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			ui.failedSignIns.NoteFailedSignIn(user, clientIP(r, ui.cfg.trustProxy), auditViaAdmin)
		}
		ui.renderLogin(w, http.StatusUnauthorized, "Invalid email or password.", email, next)
		return
	}
	if !user.Active {
		ui.failedSignIns.NoteFailedSignIn(user, clientIP(r, ui.cfg.trustProxy), auditViaAdmin)
		ui.renderLogin(w, http.StatusUnauthorized, "Invalid email or password.", email, next)
		return
	}
	// Successful authentication: legitimate logins aren't counted toward the
	// brute-force budget (mirrors handleLogin in auth.go).
	ui.loginLimiter.RecordSuccess(clientIP(r, ui.cfg.trustProxy), email)
	if !adminUIRole(user.Role) {
		ui.renderLogin(w, http.StatusForbidden, "Admin access required.", email, next)
		return
	}
	token, err := generateJWT(user, ui.jwtSecret, sessionLifetime)
	if err != nil {
		slog.Error("admin login: token generation failed", "error", err)
		ui.renderLogin(w, http.StatusInternalServerError, "Something went wrong. Try again.", email, next)
		return
	}
	// Before the cookie, so a session is never handed out without its row in
	// the audit log.
	if err := ui.signIns.RecordSignIn(withSessionActor(r, user, auditViaAdmin, ui.cfg.trustProxy)); err != nil {
		slog.Error("admin login: failed to record the sign-in in the audit log", "sub", user.ID, "error", err)
		ui.renderLogin(w, http.StatusInternalServerError, "Something went wrong. Try again.", email, next)
		return
	}
	setSessionCookie(w, r, token, ui.cfg.trustProxy)
	recordLastSeen(r.Context(), ui.users, user.ID, "admin login")
	if next == "" {
		next = ui.home(r.Context())
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// renderLogin renders the login form with the given status (renderInto
// writes the status line after a successful render).
func (ui *adminUI) renderLogin(w http.ResponseWriter, status int, errMsg, email, next string) {
	renderInto(w, status, ui.tmpl.public, "login.html", "login.html", map[string]interface{}{
		"Title": "Sign in", "Error": errMsg, "Email": email, "Next": next,
	})
}

// logout revokes the session's JWT server-side and clears the cookie. The
// route is deliberately unauthenticated (an expired session must still be
// able to log out), so a cookie that no longer validates is simply cleared.
// If the revocation can't be recorded the token is still live, so the cookie
// is kept and the admin is told, matching POST /api/v1/auth/logout's 500:
// clearing it would only hide a session that someone holding a copy of the
// cookie could still use.
func (ui *adminUI) logout(w http.ResponseWriter, r *http.Request) {
	if err := revokeSessionCookie(r, ui.jwtSecret, ui.tokenRevoker, ui.cfg.trustProxy); err != nil {
		slog.Error("admin logout: kept the session, revocation failed", "error", err)
		setFlash(w, "logout_failed")
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// renderAdmin renders an admin page through the shared base.html layout,
// which pulls in the view's {{define "content"}} block, and threads through
// any pending flash message and the shell's data. takeFlash's clearing
// Set-Cookie must land before the status line is committed, which renderInto
// guarantees by calling WriteHeader(status) only after the template has
// rendered successfully.
func (ui *adminUI) renderAdmin(w http.ResponseWriter, r *http.Request, status int, view string, data map[string]interface{}) {
	ui.renderAdminWithAlerts(w, r, status, view, data, ui.listAlertsForShell(r))
}

// renderAdminWithAlerts is renderAdmin for a page that has listed the open
// alerts itself, so the shell counts the same list the page shows.
func (ui *adminUI) renderAdminWithAlerts(w http.ResponseWriter, r *http.Request, status int, view string, data map[string]interface{}, alerts []alertEntry) {
	data["Flash"] = takeFlash(w, r)
	data["Shell"] = ui.shell(r, alerts)
	renderInto(w, status, ui.tmpl.admin, view, "base.html", data)
}

// mapPage renders the live map. A trip's trail was once drawn here with
// ?trip_id=N; it has its own page now, so an old link is sent there. A
// trip_id that is not a positive number can't name a trip and is a 404.
func (ui *adminUI) mapPage(w http.ResponseWriter, r *http.Request) {
	if raw := r.URL.Query().Get("trip_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin/trips/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	ui.renderAdmin(w, r, http.StatusOK, "map.html", map[string]interface{}{
		"Title":      "Live map",
		"Page":       "map",
		"QuietAfter": int(ui.alerts.settings.quietAfter / time.Second),
		// The on-time window, so the card calls a vehicle early, late or on
		// time as the trip page and the Trips list do (reportSettings.verdict).
		// The page names them data-window-*: html/template reads a data-on*
		// attribute as an event handler and writes its value as script.
		"OnTimeEarly": int(ui.reports.settings.onTimeEarly / time.Second),
		"OnTimeLate":  int(ui.reports.settings.onTimeLate / time.Second),
	})
}

// humanizeAge renders how long ago t was, the one way every admin page writes
// an age: "just now", then whole seconds, minutes and hours.
func humanizeAge(t time.Time) string {
	return ageText(time.Since(t))
}

// ageText is humanizeAge for an age already worked out.
func ageText(age time.Duration) string {
	switch {
	case age < 2*time.Second:
		return "just now"
	case age < time.Minute:
		return fmt.Sprintf("%d s ago", int(age.Seconds()))
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

// pagerCount is how many rows a list page shows, for the pager under it.
// The lists know only whether there is a next page, never a total, so the
// pager says "50 vehicles shown" and not "of 213".
type pagerCount struct {
	N    int
	Text string
}

func newPagerCount(n int, one, many string) pagerCount {
	if n == 0 {
		return pagerCount{Text: "No " + many}
	}
	return pagerCount{N: n, Text: countOf(n, one, many)}
}

// adminPageOffset converts a page number into the SQL offset for a page of
// adminPageSize rows. maxAdminPage keeps the result well inside int32, the
// offset type the paged store queries take.
func adminPageOffset(page int) int {
	return (page - 1) * adminPageSize
}

// vehicleRow is a single row in the vehicle list table: the vehicle's
// stored fields plus whatever live state we can join in (its last report
// from the tracker and the open alerts, its driver from the active trips).
type vehicleRow struct {
	ID        string
	Label     string
	AgencyTag string
	Active    bool
	Report    vehicleReport
	Driver    string
}

// Name is what the list calls the vehicle: its label, or its id when it has
// none.
func (v vehicleRow) Name() string { return cmp.Or(v.Label, v.ID) }

// vehicleReport is what the list says about a vehicle's last report. State
// is "live" (heard from within the staleness threshold), "quiet" (an open
// alert says it has gone quiet), "waiting" (on a trip, no report yet) or
// "off" (not on a trip).
type vehicleReport struct {
	State string
	Text  string
}

// vehiclesPageURL builds an /admin/vehicles link preserving the current
// filter values with page set to the given page number, for the prev/next
// pagination links and the show/hide-deactivated toggle. Every filter has to
// be threaded through, or page 2 of a search silently shows unfiltered rows.
func vehiclesPageURL(f VehicleFilter, page int) string {
	v := url.Values{}
	if f.IncludeInactive {
		v.Set("include_inactive", "1")
	}
	if f.AgencyTag != "" {
		v.Set("agency_tag", f.AgencyTag)
	}
	if f.Q != "" {
		v.Set("q", f.Q)
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

	q := query.Get("q")
	if err := validateListQuery(q); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page := adminPageNumber(query)

	filter := VehicleFilter{
		IncludeInactive: query.Get("include_inactive") == "1",
		AgencyTag:       query.Get("agency_tag"),
		Q:               q,
		Limit:           adminPageSize + 1,
		Offset:          int32(adminPageOffset(page)),
	}

	vehicles, err := ui.vehiclePager.ListVehiclesPage(ctx, filter)
	if err != nil {
		slog.Error("vehicles: list vehicles page", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	hasMore := len(vehicles) > adminPageSize
	if hasMore {
		vehicles = vehicles[:adminPageSize]
	}

	total, err := ui.stats.CountActiveVehicles(ctx)
	if err != nil {
		slog.Error("vehicles: count active vehicles", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	tripsByVehicle, err := ui.activeTrips.ListActiveTripsByVehicle(ctx)
	if err != nil {
		slog.Error("vehicles: list active trips by vehicle", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	alerts := ui.listAlertsForShell(r)
	quietFor := make(map[string]string)
	for id, a := range quietVehicles(alerts) {
		quietFor[id] = humanizeDuration(time.Duration(a.DurationSeconds) * time.Second)
	}
	lastSeen := make(map[string]string)
	for _, v := range ui.tracker.ActiveVehicles() {
		lastSeen[v.VehicleID] = humanizeAge(v.UpdatedAt)
	}

	rows := make([]vehicleRow, 0, len(vehicles))
	for _, v := range vehicles {
		row := vehicleRow{ID: v.ID, Label: v.Label, AgencyTag: v.AgencyTag, Active: v.Active}
		trip, onTrip := tripsByVehicle[v.ID]
		if onTrip {
			row.Driver = trip.DriverName
		}
		switch {
		case quietFor[v.ID] != "":
			row.Report = vehicleReport{State: "quiet", Text: "Quiet " + quietFor[v.ID]}
		case lastSeen[v.ID] != "":
			row.Report = vehicleReport{State: "live", Text: lastSeen[v.ID]}
		case onTrip:
			row.Report = vehicleReport{State: "waiting", Text: "No report yet"}
		default:
			row.Report = vehicleReport{State: "off", Text: "Not on a trip"}
		}
		rows = append(rows, row)
	}

	// The show/hide-deactivated link flips only that one filter, so a search
	// in progress survives the toggle.
	toggled := filter
	toggled.IncludeInactive = !filter.IncludeInactive

	ui.renderAdminWithAlerts(w, r, http.StatusOK, "vehicles.html", map[string]interface{}{
		"Title":             "Vehicles",
		"Page":              "vehicles",
		"Headline":          vehiclesHeadline(total, ui.fleetNow(alerts).Reporting),
		"Total":             total,
		"Filtered":          filter.Q != "" || filter.AgencyTag != "",
		"Vehicles":          rows,
		"IncludeInactive":   filter.IncludeInactive,
		"AgencyTag":         filter.AgencyTag,
		"Q":                 filter.Q,
		"Shown":             newPagerCount(len(rows), "vehicle", "vehicles"),
		"PageNum":           page,
		"HasMore":           hasMore,
		"PrevURL":           vehiclesPageURL(filter, page-1),
		"NextURL":           vehiclesPageURL(filter, page+1),
		"ToggleInactiveURL": vehiclesPageURL(toggled, 1),
	}, alerts)
}

// vehiclesHeadline is the Vehicles page's headline: how many vehicles are
// active and how many of them are reporting now.
func vehiclesHeadline(total, reporting int) string {
	if total == 0 {
		return "No vehicles yet"
	}
	now := "none reporting now"
	if reporting > 0 {
		now = strconv.Itoa(reporting) + " reporting now"
	}
	return countOf(total, "vehicle", "vehicles") + ", " + now
}

// countOf writes n with the singular or plural noun: "1 vehicle", "14
// vehicles".
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
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
	// ErrorField names the field Error is about ("id"), so the form can mark
	// it; empty when the error is about the submission as a whole.
	ErrorField string
	// Active is whether the vehicle being edited is active, which decides
	// the Deactivate or Activate button. It is nil when that was not read (a
	// new vehicle, or a submission that was refused), and the button is then
	// left off.
	Active *bool
}

// Name is the vehicle's label, or its id when it has none.
func (d vehicleFormData) Name() string { return cmp.Or(d.Label, d.ID) }

// IsActive reports whether the vehicle being edited is known to be active.
func (d vehicleFormData) IsActive() bool { return d.Active != nil && *d.Active }

func (ui *adminUI) renderVehicleForm(w http.ResponseWriter, r *http.Request, status int, data vehicleFormData) {
	title := "New vehicle"
	switch {
	case data.IsEdit && isViewer(r):
		title = "Vehicle"
	case data.IsEdit:
		title = "Edit vehicle"
	}
	ui.renderAdmin(w, r, status, "vehicle_form.html", map[string]interface{}{
		"Title":       title,
		"Page":        "vehicles",
		"Form":        data,
		"MaxIDLength": maxVehicleIDLength,
	})
}

// vehicleNewPage renders the blank create-vehicle form.
func (ui *adminUI) vehicleNewPage(w http.ResponseWriter, r *http.Request) {
	ui.renderVehicleForm(w, r, http.StatusOK, vehicleFormData{})
}

// vehicleCreate validates and saves a new vehicle. It reuses
// validateVehicleID and validateVehicleText, the JSON API's own checks, so form
// and API validation stay in lockstep, and reports the exact same error text on
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
		ui.renderVehicleForm(w, r, http.StatusUnprocessableEntity, vehicleFormData{ID: id, Label: label, AgencyTag: agencyTag, Error: err.Error(), ErrorField: "id"})
		return
	}
	if err := validateVehicleText(label, agencyTag); err != nil {
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
		ui.renderVehicleForm(w, r, http.StatusUnprocessableEntity, vehicleFormData{ID: id, Label: label, AgencyTag: agencyTag, Error: "vehicle id already exists", ErrorField: "id"})
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
	ui.renderVehicleForm(w, r, http.StatusOK, vehicleFormData{IsEdit: true, ID: v.ID, Label: v.Label, AgencyTag: v.AgencyTag, Active: &v.Active})
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

	if err := validateVehicleText(label, agencyTag); err != nil {
		ui.renderVehicleForm(w, r, http.StatusUnprocessableEntity, vehicleFormData{IsEdit: true, ID: id, Label: label, AgencyTag: agencyTag, Error: err.Error()})
		return
	}

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
	if !active {
		// Leave the feed now, not when the last report goes stale.
		ui.tracker.Remove(id)
	}
	setFlash(w, flashCode)
	http.Redirect(w, r, "/admin/vehicles", http.StatusSeeOther)
}

// minPasswordLength is the minimum number of characters in a password, and
// maxPasswordBytes is bcrypt's input limit: bcrypt.GenerateFromPassword
// rejects anything longer. Both are enforced everywhere passwords are set —
// the users API, the admin UI forms, and the bootstrap admin path — via
// validatePassword. The maximum has to be checked here, before any write:
// handleUpdateUser saves the profile before it hashes the new password, so a
// password bcrypt rejects later would leave the profile changed and the
// password not.
const (
	minPasswordLength = 8
	maxPasswordBytes  = 72
)

// validatePassword enforces the shared password-length policy.
func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("password must be at most %d bytes", maxPasswordBytes)
	}
	return nil
}

// userRow is a single row in the user list table: the user's stored fields
// plus how many vehicles are currently assigned to them.
type userRow struct {
	ID           int64
	Name         string
	Email        string
	Initials     string
	Role         string
	Active       bool
	VehicleCount int
	LastSeen     string // "never" when the user has not signed in since it was first recorded
}

// MayDrive is how many vehicles a driver is assigned: "1 vehicle", "3
// vehicles".
func (u userRow) MayDrive() string { return countOf(u.VehicleCount, "vehicle", "vehicles") }

// roleLink is one choice of the Drivers and users page's role filter.
type roleLink struct {
	Label   string
	URL     string
	Current bool
}

// usersHeadline is the Drivers and users page's headline, from the counts of
// active accounts. Viewers are named only when there are some, since most
// servers have none.
func usersHeadline(drivers, admins, viewers int) string {
	if drivers == 0 {
		return "No drivers yet"
	}
	if viewers == 0 {
		return countOf(drivers, "driver", "drivers") + " and " + countOf(admins, "admin", "admins")
	}
	return countOf(drivers, "driver", "drivers") + ", " + countOf(admins, "admin", "admins") + " and " + countOf(viewers, "viewer", "viewers")
}

// usersPageURL builds an /admin/users link preserving the current filter
// values with page set to the given page number, for the prev/next
// pagination links and the active-only toggle. Every filter has to be
// threaded through, or page 2 of a search silently shows unfiltered rows.
func usersPageURL(f UserFilter, page int) string {
	v := url.Values{}
	if f.Role != "" {
		v.Set("role", f.Role)
	}
	if f.Q != "" {
		v.Set("q", f.Q)
	}
	if f.ActiveOnly {
		v.Set("active", "1")
	}
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
	query := r.URL.Query()

	q := query.Get("q")
	if err := validateListQuery(q); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	role := query.Get("role")
	if !validUserRoleFilter(role) {
		http.Error(w, "role must be driver, admin or viewer", http.StatusBadRequest)
		return
	}
	page := adminPageNumber(query)

	filter := UserFilter{
		Role:       role,
		Q:          q,
		ActiveOnly: query.Get("active") == "1",
		Limit:      adminPageSize + 1,
		Offset:     int32(adminPageOffset(page)),
	}

	users, err := ui.userPager.ListUsersPage(ctx, filter)
	if err != nil {
		slog.Error("users: list users page", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	drivers, err := ui.stats.CountActiveUsersByRole(ctx, roleDriver)
	if err != nil {
		slog.Error("users: count active drivers", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	admins, err := ui.stats.CountActiveUsersByRole(ctx, roleAdmin)
	if err != nil {
		slog.Error("users: count active admins", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	viewers, err := ui.stats.CountActiveUsersByRole(ctx, roleViewer)
	if err != nil {
		slog.Error("users: count active viewers", "error", err)
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
		row := userRow{
			ID:           u.ID,
			Name:         u.Name,
			Email:        u.Email,
			Initials:     initials(u.Name, u.Email),
			Role:         u.Role,
			Active:       u.Active,
			VehicleCount: len(assignments),
			LastSeen:     "never",
		}
		if u.LastSeenAt != nil {
			row.LastSeen = formatTripTimestamp(*u.LastSeenAt)
		}
		rows = append(rows, row)
	}

	// Each choice of role flips only the role, so a search in progress
	// survives it, and starts over at page 1.
	roles := make([]roleLink, 0, 4)
	for _, choice := range []struct{ role, label string }{{"", "Everyone"}, {roleDriver, "Drivers"}, {roleAdmin, "Admins"}, {roleViewer, "Viewers"}} {
		f := filter
		f.Role = choice.role
		roles = append(roles, roleLink{Label: choice.label, URL: usersPageURL(f, 1), Current: filter.Role == choice.role})
	}

	// The active-only link flips only that one filter, so a search in
	// progress survives the toggle.
	toggled := filter
	toggled.ActiveOnly = !filter.ActiveOnly

	ui.renderAdmin(w, r, http.StatusOK, "users.html", map[string]interface{}{
		"Title":           "Drivers and users",
		"Page":            "users",
		"Headline":        usersHeadline(drivers, admins, viewers),
		"Users":           rows,
		"Role":            filter.Role,
		"Roles":           roles,
		"Q":               filter.Q,
		"ActiveOnly":      filter.ActiveOnly,
		"Filtered":        filter.Role != "" || filter.Q != "" || filter.ActiveOnly,
		"Shown":           newPagerCount(len(rows), "account", "accounts"),
		"PageNum":         page,
		"HasMore":         hasMore,
		"PrevURL":         usersPageURL(filter, page-1),
		"NextURL":         usersPageURL(filter, page+1),
		"ToggleActiveURL": usersPageURL(toggled, 1),
	})
}

// assignmentRow is a single currently-assigned vehicle shown in the user
// edit page's assignments section, with the vehicle's label joined in for
// display (assignments themselves only carry the vehicle id).
type assignmentRow struct {
	VehicleID string
	Label     string
}

// Name is the vehicle's label, or its id when it has none.
func (a assignmentRow) Name() string { return cmp.Or(a.Label, a.VehicleID) }

// userFormData carries the user_form.html template's fields for both the
// create and edit flows (distinguished by IsEdit), including any submitted
// values and validation error to re-render on failure. Assignments and
// AvailableVehicles are only populated (and only rendered) in edit mode.
type userFormData struct {
	IsEdit bool
	ID     string
	Name   string
	Email  string
	Role   string
	Error  string
	// ErrorField names the field Error is about ("name", "email", "password"
	// or "role"), so the form can mark it; empty when the error is about the
	// submission as a whole.
	ErrorField string
	// Active is whether the account being edited is active, which decides
	// the Deactivate or Activate button and the setup sheet link. It is nil
	// when that was not read (a new user, or a form re-rendered with an
	// error), and those are then left off.
	Active            *bool
	Assignments       []assignmentRow
	AvailableVehicles []VehicleResponse
}

// IsActive reports whether the account being edited is known to be active.
func (d userFormData) IsActive() bool { return d.Active != nil && *d.Active }

func (ui *adminUI) renderUserForm(w http.ResponseWriter, r *http.Request, status int, data userFormData) {
	title := "New user"
	switch {
	case data.IsEdit && isViewer(r):
		title = "Account"
	case data.IsEdit:
		title = "Edit user"
	}
	ui.renderAdmin(w, r, status, "user_form.html", map[string]interface{}{
		"Title":             title,
		"Page":              "users",
		"Form":              data,
		"MinPasswordLength": minPasswordLength,
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
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "name is required", ErrorField: "name"})
		return
	}
	if email == "" {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "email is required", ErrorField: "email"})
		return
	}
	if !validEmail(email) {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "email is not valid", ErrorField: "email"})
		return
	}
	if err := validatePassword(password); err != nil {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: err.Error(), ErrorField: "password"})
		return
	}
	if !validUserRole(role) {
		ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "role must be driver, admin or viewer", ErrorField: "role"})
		return
	}

	if _, err := ui.userManager.CreateUser(r.Context(), name, email, password, role); err != nil {
		if errors.Is(err, ErrDuplicateEmail) {
			ui.renderUserForm(w, r, http.StatusUnprocessableEntity, userFormData{Name: name, Email: email, Role: role, Error: "email already exists", ErrorField: "email"})
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
	data.Active = &u.Active
	ui.renderUserForm(w, r, http.StatusOK, data)
}

// renderUserEditError re-renders the edit form for id with the submitted
// name/email/role values, an error message and the field it is about,
// refetching the current assignments/available-vehicles for the assignments
// section so it isn't blank on a validation failure.
func (ui *adminUI) renderUserEditError(w http.ResponseWriter, r *http.Request, status int, id int64, name, email, role, field, errMsg string) {
	data, err := ui.buildUserFormData(r.Context(), id, name, email, role)
	if err != nil {
		slog.Error("user edit: rebuild form data", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data.Error = errMsg
	data.ErrorField = field
	ui.renderUserForm(w, r, status, data)
}

// userUpdate saves name/email/role edits for an existing user. If the
// password field is non-empty, it's validated the same way as the create
// form and, on success, also written via SetUserPasswordAndRevokeSessions.
// Leaving it blank keeps the current password untouched.
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
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "name", "name is required")
		return
	}
	if email == "" {
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "email", "email is required")
		return
	}
	if !validEmail(email) {
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "email", "email is not valid")
		return
	}
	if !validUserRole(role) {
		ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "role", "role must be driver, admin or viewer")
		return
	}
	if password != "" {
		if err := validatePassword(password); err != nil {
			ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "password", err.Error())
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
			ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "role", "cannot demote the last active admin")
			return
		}
	}

	if _, err := ui.userManager.UpdateUser(r.Context(), id, name, email, role); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, ErrDuplicateEmail) {
			ui.renderUserEditError(w, r, http.StatusUnprocessableEntity, id, name, email, role, "email", "email already exists")
			return
		}
		if errors.Is(err, ErrViewerWithTrip) {
			ui.renderUserEditError(w, r, http.StatusConflict, id, name, email, role, "role", ErrViewerWithTrip.Error())
			return
		}
		slog.Error("user update: update user", "user_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if password != "" {
		// Mirrors handleUpdateUser: a password reset must end the sessions
		// the old password left behind, or a stolen refresh token outlives it.
		if err := ui.userManager.SetUserPasswordAndRevokeSessions(r.Context(), id, password); err != nil {
			if errors.Is(err, ErrUserNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("user update: change password and revoke refresh tokens", "user_id", id, "error", err)
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
	if err := ui.userManager.SetUserActiveAndRevokeSessions(r.Context(), id, active); err != nil {
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
// ErrVehicleNotFoundFK 404s rather than surfacing a raw 500, and
// ErrVehicleInactive (the form only offers active vehicles, so a vehicle
// deactivated since the page loaded) is a 409.
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
			if errors.Is(err, ErrVehicleInactive) {
				http.Error(w, "vehicle is inactive", http.StatusConflict)
				return
			}
			if errors.Is(err, ErrAssignToViewer) {
				http.Error(w, ErrAssignToViewer.Error(), http.StatusConflict)
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
// (e.g. a double-submitted remove) 404s rather than silently succeeding, and
// one the driver is running a trip on is kept, with a flash saying to end the
// trip first.
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
		if errors.Is(err, ErrAssignmentInUse) {
			setFlash(w, "unassign_on_trip")
			http.Redirect(w, r, "/admin/users/"+idStr+"/edit", http.StatusSeeOther)
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
// with its route's badge and headsign from the schedule, and its times on the
// page's clock, ready for the template.
type tripRow struct {
	ID           int64
	VehicleLabel string
	DriverName   string
	Route        tripRoute
	Headsign     string
	GtfsTripID   string
	StartClock   string
	StartDay     string
	// EndClock is empty while the trip is running.
	EndClock string
	Duration string
	Status   string
	Active   bool
}

// formatTripTimestamp renders a time in the admin UI's fixed UTC display
// format, regardless of the server's local timezone.
func formatTripTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// pageLocation is the clock an admin page writes a trip's times on: the
// agency's when a schedule is loaded, else UTC.
func pageLocation(ix *rider.Index) *time.Location {
	if ix == nil {
		return time.UTC
	}
	return ix.Timezone()
}

// formatDay is the day t falls on, on the page's clock: "17 Nov", with the
// year when it is not now's.
func formatDay(t time.Time, loc *time.Location, now time.Time) string {
	local := t.In(loc)
	if local.Year() != now.In(loc).Year() {
		return local.Format("2 Jan 2006")
	}
	return local.Format("2 Jan")
}

// formatMinutes renders how long something took, rounded to the nearest
// minute: "45 min", "1 h 45 min".
func formatMinutes(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d h %d min", h, m)
	}
}

// tripsHeadline is the Trips page's headline, from the count of running
// trips.
func tripsHeadline(running int) string {
	switch running {
	case 0:
		return "No trips are running"
	case 1:
		return "1 trip is running"
	default:
		return strconv.Itoa(running) + " trips are running"
	}
}

// statusLink is one choice of the Trips page's status filter.
type statusLink struct {
	Label   string
	URL     string
	Current bool
	// Count is shown beside the label when it is not zero.
	Count int
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

	running, err := ui.stats.CountActiveTrips(ctx)
	if err != nil {
		slog.Error("trips: count active trips", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
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

	ix := ui.reports.index()
	loc := pageLocation(ix)
	now := time.Now()
	lateness, err := ui.tripsLateness(ctx, trips, ix, now)
	if err != nil {
		slog.Error("trips: work out lateness", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	rows := make([]tripRow, 0, len(trips))
	for _, t := range trips {
		row := tripRow{
			ID:           t.ID,
			VehicleLabel: t.VehicleLabel,
			DriverName:   t.DriverName,
			Route:        routeBadge(ix, t.RouteID),
			GtfsTripID:   t.GtfsTripID,
			StartClock:   t.StartTime.In(loc).Format("15:04"),
			StartDay:     formatDay(t.StartTime, loc, now),
			Status:       t.Status,
			Active:       t.Status == "active",
		}
		if ix != nil {
			if info, ok := ix.Trip(t.GtfsTripID); ok {
				row.Headsign = info.Headsign
			}
		}
		if t.EndTime != nil {
			row.EndClock = t.EndTime.In(loc).Format("15:04")
			row.Duration = formatMinutes(t.EndTime.Sub(t.StartTime))
		} else {
			row.Duration = formatMinutes(now.Sub(t.StartTime)) + " so far"
		}
		rows = append(rows, row)
	}

	// Each choice of status flips only the status, so a search in progress
	// survives it, and starts over at page 1.
	statuses := []statusLink{
		{Label: "All", URL: tripsPageURL("", vehicleID, q, 1), Current: status == ""},
		{Label: "Running", URL: tripsPageURL("active", vehicleID, q, 1), Current: status == "active", Count: running},
		{Label: "Completed", URL: tripsPageURL("completed", vehicleID, q, 1), Current: status == "completed"},
	}

	ui.renderAdmin(w, r, http.StatusOK, "trips.html", map[string]interface{}{
		"Title":     "Trips",
		"Page":      "trips",
		"Headline":  tripsHeadline(running),
		"Timezone":  loc.String(),
		"Trips":     rows,
		"Lateness":  lateness,
		"Vehicles":  activeVehicles,
		"Status":    status,
		"Statuses":  statuses,
		"VehicleID": vehicleID,
		"Q":         q,
		"Filtered":  status != "" || vehicleID != "" || q != "",
		"Shown":     newPagerCount(len(rows), "trip", "trips"),
		"PageNum":   page,
		"HasMore":   hasMore,
		"PrevURL":   tripsPageURL(status, vehicleID, q, page-1),
		"NextURL":   tripsPageURL(status, vehicleID, q, page+1),
	})
}

// tripEnd ends a driver's active trip from the Trips page, for a driver
// locked out by a trip their phone no longer knows about; it mirrors
// handleAdminEndTrip. A trip that had already ended (a stale page, or the
// driver ended it first) is not an error: it is ended, which is what the
// admin wanted, so it gets its own flash and the same redirect, as
// ErrAssignmentExists does in userAssignVehicle.
func (ui *adminUI) tripEnd(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	sub := adminSub(r)
	trip, err := ui.tripEnder.EndTripByID(r.Context(), id)
	switch {
	case err == nil:
		ui.tracker.Remove(trip.VehicleID)
		slog.Info("admin ended trip", "trip_id", trip.ID, "driver_id", trip.UserID, "vehicle_id", trip.VehicleID, "admin_sub", sub)
		setFlash(w, "trip_ended")
	case errors.Is(err, ErrTripNotActive):
		slog.Warn("admin end trip: trip already ended", "trip_id", id, "admin_sub", sub)
		setFlash(w, "trip_already_ended")
	case errors.Is(err, ErrTripNotFound):
		slog.Warn("admin end trip: no trip with this id", "trip_id", id, "admin_sub", sub)
		http.NotFound(w, r)
		return
	default:
		slog.Error("failed to end trip by id", "trip_id", id, "admin_sub", sub, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/trips", http.StatusSeeOther)
}
