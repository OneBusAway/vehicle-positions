package main

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path"
)

//go:embed web/templates web/static
var files embed.FS

const adminTemplateKey = "_admin_template"

type embeddedTemplates struct {
	public *template.Template
	admin  map[string]*template.Template
}

var templates = mustLoadTemplates()

func mustLoadTemplates() *embeddedTemplates {
	adminViews := []string{
		"dashboard.html",
		"map.html",
		"trips.html",
		"users.html",
		"vehicles.html",
	}

	adminTemplates := make(map[string]*template.Template, len(adminViews))

	for _, view := range adminViews {
		adminTemplates[view] = template.Must(template.ParseFS(
			files,
			"web/templates/layout/*.html",
			path.Join("web/templates/views", view),
		))
	}

	return &embeddedTemplates{
		public: template.Must(template.ParseFS(files, "web/templates/views/login.html")),
		admin:  adminTemplates,
	}
}

func adminViewName(data any) (string, error) {
	templateData, ok := data.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("admin template data must be map[string]interface{}")
	}

	viewName, ok := templateData[adminTemplateKey].(string)
	if !ok || viewName == "" {
		return "", fmt.Errorf("admin template view is missing")
	}

	return viewName, nil
}

func withAdminTemplate(data map[string]interface{}, view string) map[string]interface{} {
	if data == nil {
		data = make(map[string]interface{}, 1)
	}

	renderData := make(map[string]interface{}, len(data)+1)
	for k, v := range data {
		renderData[k] = v
	}

	renderData[adminTemplateKey] = path.Base(view)
	return renderData
}

func (t *embeddedTemplates) ExecuteTemplate(w io.Writer, name string, data any) error {
	if name != "base.html" {
		return t.public.Execute(w, data)
	}

	viewName, err := adminViewName(data)
	if err != nil {
		return err
	}

	tmpl, ok := t.admin[viewName]
	if !ok {
		return fmt.Errorf("unknown admin template: %s", viewName)
	}

	return tmpl.ExecuteTemplate(w, name, data)
}

func renderPublic(w http.ResponseWriter, view string, data map[string]interface{}) {
	if err := templates.ExecuteTemplate(w, path.Base(view), data); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
	}
}

func renderAdmin(w http.ResponseWriter, view string, data map[string]interface{}) {
	if err := templates.ExecuteTemplate(w, "base.html", withAdminTemplate(data, view)); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
	}
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
