package main

import (
	"errors"
	"log/slog"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// adminUIConfig holds the runtime knobs for the admin UI that vary by
// deployment (whether it's served at all, and whether proxy headers are
// trusted for client-IP/HTTPS detection).
type adminUIConfig struct {
	enabled    bool
	trustProxy bool
}

// adminUI owns the parsed templates and dependencies for all admin pages.
// Page-data deps grow in later tasks (tracker, stats, trips, vehicles...).
type adminUI struct {
	tmpl         *embeddedTemplates
	users        UserFetcher
	jwtSecret    []byte
	loginLimiter *LoginRateLimiter
	cfg          adminUIConfig
}

// newAdminUI loads the embedded templates and wires the admin UI's
// dependencies. It returns an error rather than panicking so callers can log
// it with context and exit cleanly.
func newAdminUI(users UserFetcher, jwtSecret []byte, limiter *LoginRateLimiter, cfg adminUIConfig) (*adminUI, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &adminUI{tmpl: tmpl, users: users, jwtSecret: jwtSecret, loginLimiter: limiter, cfg: cfg}, nil
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

func (ui *adminUI) mapPage(w http.ResponseWriter, r *http.Request) {
	ui.renderAdmin(w, r, "map.html", map[string]interface{}{
		"Title": "Live Map",
		"Page":  "map",
	})
}

func (ui *adminUI) dashboardPage(w http.ResponseWriter, r *http.Request) {
	ui.renderAdmin(w, r, "dashboard.html", map[string]interface{}{
		"Title":          "Dashboard",
		"Page":           "dashboard",
		"TotalVehicles":  "24",
		"ActiveVehicles": "18",
		"TotalDrivers":   "32",
		"ActiveTrips":    "15",
		"RecentVehicles": []map[string]string{
			{"Name": "Bus 001", "Route": "Route A", "Status": "active", "LastSeen": "2 min ago"},
			{"Name": "Bus 002", "Route": "Route B", "Status": "active", "LastSeen": "5 min ago"},
			{"Name": "Bus 003", "Route": "Route C", "Status": "idle", "LastSeen": "12 min ago"},
			{"Name": "Bus 004", "Route": "Route A", "Status": "active", "LastSeen": "1 min ago"},
			{"Name": "Bus 005", "Route": "Route D", "Status": "active", "LastSeen": "3 min ago"},
		},
	})
}

func (ui *adminUI) vehiclesPage(w http.ResponseWriter, r *http.Request) {
	ui.renderAdmin(w, r, "vehicles.html", map[string]interface{}{
		"Title": "Vehicles",
		"Page":  "vehicles",
		"Vehicles": []map[string]string{
			{"ID": "V001", "Name": "Bus 001", "Route": "Route A", "Driver": "Chaitanya K", "Status": "active", "LastSeen": "2 min ago"},
			{"ID": "V002", "Name": "Bus 002", "Route": "Route B", "Driver": "Aron", "Status": "active", "LastSeen": "5 min ago"},
			{"ID": "V003", "Name": "Bus 003", "Route": "Route C", "Driver": "Brad Pitt", "Status": "idle", "LastSeen": "12 min ago"},
		},
	})
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
