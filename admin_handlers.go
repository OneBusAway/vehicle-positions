package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
)

// adminUIEnabled reports whether the admin UI should be served, controlled by
// the ADMIN_UI_ENABLED environment variable (default false). Any value
// strconv.ParseBool accepts as true (1, t, T, TRUE, true, ...) turns it on;
// unset or unparseable values leave it off.
func adminUIEnabled() bool {
	enabled, _ := strconv.ParseBool(os.Getenv("ADMIN_UI_ENABLED"))
	return enabled
}

// registerAdminUI loads the embedded templates, mounts the static file server,
// and registers the admin UI routes on mux. It returns an error if the
// templates or static assets cannot be prepared from the embedded filesystem.
func registerAdminUI(mux *http.ServeMux) error {
	tmpls, err := loadTemplates()
	if err != nil {
		return fmt.Errorf("load templates: %w", err)
	}
	templates = tmpls

	staticFiles, err := fs.Sub(files, "web/static")
	if err != nil {
		return fmt.Errorf("prepare static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	mux.HandleFunc("GET /admin/login", AdminLoginHandler)
	mux.HandleFunc("GET /admin/signup", AdminSignupHandler)
	mux.HandleFunc("GET /admin/map", AdminMapHandler)
	mux.HandleFunc("GET /admin/dashboard", AdminDashboardHandler)
	mux.HandleFunc("GET /admin/vehicles", AdminVehiclesHandler)
	mux.HandleFunc("GET /admin/users", AdminUsersHandler)
	mux.HandleFunc("GET /admin/trips", AdminTripsHandler)
	return nil
}

// templates holds the parsed admin UI templates. It stays nil until
// loadTemplates succeeds in main(); the admin routes that use it are only
// registered when the admin UI is enabled, so handlers never run against nil.
var templates *embeddedTemplates

type embeddedTemplates struct {
	public map[string]*template.Template
	admin  map[string]*template.Template
}

// loadTemplates parses the embedded admin UI templates once at startup. It
// returns an error rather than panicking so main() can log it with context and
// exit cleanly, consistent with the rest of the server's startup error handling.
func loadTemplates() (*embeddedTemplates, error) {
	adminViews := []string{
		"dashboard.html",
		"map.html",
		"trips.html",
		"users.html",
		"vehicles.html",
	}

	admin := make(map[string]*template.Template, len(adminViews))
	for _, view := range adminViews {
		tmpl, err := template.ParseFS(
			files,
			"web/templates/layout/*.html",
			path.Join("web/templates/views", view),
		)
		if err != nil {
			return nil, fmt.Errorf("parse admin view %q: %w", view, err)
		}
		admin[view] = tmpl
	}

	login, err := template.ParseFS(files, "web/templates/views/login.html")
	if err != nil {
		return nil, fmt.Errorf("parse public view %q: %w", "login.html", err)
	}

	return &embeddedTemplates{
		public: map[string]*template.Template{"login.html": login},
		admin:  admin,
	}, nil
}

// render looks up the parsed template set by view name and executes rootName
// into a buffer first, so a mid-render failure yields a clean 500 instead of a
// half-written 200 with a corrupted body. An unknown view is a programmer error
// (the route registered it) but is still reported rather than silently ignored.
func render(w http.ResponseWriter, set map[string]*template.Template, view, rootName string, data map[string]interface{}) {
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
		// The 200 header is already committed, so we can't convert this to a
		// 500 — log it so a truncated response is at least visible server-side.
		slog.Error("template response write failed", "view", view, "error", err)
	}
}

// renderPublic renders a standalone public page (e.g. login) by its own name.
func renderPublic(w http.ResponseWriter, view string, data map[string]interface{}) {
	render(w, templates.public, view, path.Base(view), data)
}

// renderAdmin renders an admin page through the shared base.html layout, which
// pulls in the view's {{define "content"}} block.
func renderAdmin(w http.ResponseWriter, view string, data map[string]interface{}) {
	render(w, templates.admin, view, "base.html", data)
}

func AdminMapHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, "web/templates/views/map.html", map[string]interface{}{
		"Title": "Live Map",
		"Page":  "map",
	})
}

func AdminLoginHandler(w http.ResponseWriter, r *http.Request) {
	renderPublic(w, "web/templates/views/login.html", map[string]interface{}{
		"Title":          "Welcome",
		"Mode":           "login",
		"LoginEndpoint":  "/api/v1/auth/login",
		"SignupEndpoint": "/api/v1/auth/signup",
	})
}

func AdminSignupHandler(w http.ResponseWriter, r *http.Request) {
	renderPublic(w, "web/templates/views/login.html", map[string]interface{}{
		"Title":          "Create Account",
		"Mode":           "signup",
		"LoginEndpoint":  "/api/v1/auth/login",
		"SignupEndpoint": "/api/v1/auth/signup",
	})
}

func AdminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, "web/templates/views/dashboard.html", map[string]interface{}{
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

func AdminVehiclesHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, "web/templates/views/vehicles.html", map[string]interface{}{
		"Title": "Vehicles",
		"Page":  "vehicles",
		"Vehicles": []map[string]string{
			{"ID": "V001", "Name": "Bus 001", "Route": "Route A", "Driver": "Chaitanya K", "Status": "active", "LastSeen": "2 min ago"},
			{"ID": "V002", "Name": "Bus 002", "Route": "Route B", "Driver": "Aron", "Status": "active", "LastSeen": "5 min ago"},
			{"ID": "V003", "Name": "Bus 003", "Route": "Route C", "Driver": "Brad Pitt", "Status": "idle", "LastSeen": "12 min ago"},
		},
	})
}

func AdminUsersHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, "web/templates/views/users.html", map[string]interface{}{
		"Title": "Users",
		"Page":  "users",
		"Users": []map[string]string{
			{"Name": "Chaitanya K", "Email": "kbc@transit.co.ke", "Role": "driver", "LastSeen": "Today"},
			{"Name": "To Holland", "Email": "tom@transit.co.ke", "Role": "driver", "LastSeen": "Today"},
			{"Name": "Open transit", "Email": "brian@transit.co.ke", "Role": "driver", "LastSeen": "Yesterday"},
		},
	})
}

func AdminTripsHandler(w http.ResponseWriter, r *http.Request) {
	renderAdmin(w, "web/templates/views/trips.html", map[string]interface{}{
		"Title": "Trips",
		"Page":  "trips",
		"Trips": []map[string]string{
			{"ID": "T001", "Vehicle": "Bus 001", "Driver": "Tom Hiddlestone", "Route": "Route A", "Start": "07:00", "End": "08:45", "Status": "completed"},
			{"ID": "T002", "Vehicle": "Bus 002", "Driver": "Chris Hensworth", "Route": "Route B", "Start": "07:15", "End": "\u2014", "Status": "active"},
			{"ID": "T003", "Vehicle": "Bus 003", "Driver": "Bruce Wayne", "Route": "Route C", "Start": "06:45", "End": "08:30", "Status": "completed"},
		},
	})
}
