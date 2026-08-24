package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
)

// adminUIEnabled reports whether the admin UI should be served, controlled by
// the ADMIN_UI_ENABLED environment variable (default true). Any value
// strconv.ParseBool accepts as false (0, f, F, FALSE, false, ...) turns it
// off; unset or unparseable values leave it on.
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

type embeddedTemplates struct {
	public map[string]*template.Template
	admin  map[string]*template.Template
}

// loadTemplates parses the embedded admin UI templates once at startup. It
// returns an error rather than panicking so callers can log it with context
// and exit cleanly, consistent with the rest of the server's startup error
// handling.
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

// renderInto looks up the parsed template set by view name and executes
// rootName into a buffer first, so a mid-render failure yields a clean 500
// instead of a half-written 200 with a corrupted body. An unknown view is a
// programmer error (the route registered it) but is still reported rather
// than silently ignored.
//
// renderInto never calls WriteHeader itself on the success path: callers that
// need a non-200 status (e.g. a failed login re-rendering the form) must call
// w.WriteHeader before invoking renderInto. On template failure it falls back
// to http.Error, which is a no-op on the status line if one was already
// written but still logs and emits an error body.
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
		// The header is already committed, so we can't convert this to a
		// 500 — log it so a truncated response is at least visible server-side.
		slog.Error("template response write failed", "view", view, "error", err)
	}
}
