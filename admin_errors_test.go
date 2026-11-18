package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serverErrorTitle is the headline of the page an admin sees when a page's
// handler answers with a bare 500.
const serverErrorTitle = "Something went wrong on the server"

// errorPagesFor serves next behind errorPages alone, with no session guard.
func errorPagesFor(t *testing.T, next http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	ui := newTestAdminUI(t)
	w := httptest.NewRecorder()
	ui.errorPages(next).ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func TestErrorPages_NotFound(t *testing.T) {
	w := errorPagesFor(t, http.NotFound, http.MethodGet, "/admin/trips/999")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.Contains(t, body, "<title>This page isn&#39;t here · Vehicle Tracker</title>")
	assert.Contains(t, body, `<div class="error-code">Error 404</div>`)
	assert.Contains(t, body, `<h1 class="h1">This page isn&#39;t here</h1>`)
	assert.Contains(t, body, "The address may be old, or the trip, vehicle or user it pointed to was removed.")
	assert.Contains(t, body, `<a class="error-home" href="/admin/map">`)
	assert.Contains(t, body, `<span class="veh-marker no-bearing" aria-hidden="true">`)
	assert.Contains(t, body, `<div class="error-ways"><a class="btn btn-primary" href="/admin/map">Go to the live map</a></div>`, "one way out")
	assert.NotContains(t, body, "404 page not found", "the plain text is not sent after the page")
}

func TestErrorPages_ServerError(t *testing.T) {
	failing := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}

	t.Run("a page that was asked for", func(t *testing.T) {
		w := errorPagesFor(t, failing, http.MethodGet, "/admin/trips?status=active&q=%22%3E%3Cscript%3E")

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `<div class="error-code">Error 500</div>`)
		assert.Contains(t, body, `<h1 class="h1">`+serverErrorTitle+`</h1>`)
		assert.Contains(t, body, "Nothing was changed. Try again in a moment.")
		assert.Contains(t, body, `<span class="veh-marker quiet" aria-hidden="true">`)
		assert.Contains(t, body, `<a class="btn btn-primary" href="/admin/trips?status=active&amp;q=%22%3E%3Cscript%3E">Try again</a><a class="btn" href="/admin/map">Go to the live map</a>`, "the same address again, escaped")
		assert.NotContains(t, body, `"><script>`)
		assert.NotContains(t, body, "internal server error")
		assert.NotContains(t, body, "history.back()")
	})
	t.Run("a form that was sent", func(t *testing.T) {
		w := errorPagesFor(t, failing, http.MethodPost, "/admin/trips/7/end")

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Go back and check whether it went through before trying again.")
		assert.NotContains(t, body, "Nothing was changed", "the page cannot know that of a form")
		assert.Contains(t, body, `<button class="btn btn-primary js-only" type="button" onclick="history.back()">Go back</button><a class="btn" href="/admin/map">Go to the live map</a>`)
		assert.NotContains(t, body, "Try again</a>", "a link cannot send the form again")
	})
}

// TestErrorPages_LeavesOtherAnswersAlone: only a bare 404 or 500 is replaced.
func TestErrorPages_LeavesOtherAnswersAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		body    string
	}{
		{"a page", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<p>hello</p>")) }, http.StatusOK, "<p>hello</p>"},
		{"a refused form", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "invalid form submission", http.StatusBadRequest)
		}, http.StatusBadRequest, "invalid form submission\n"},
		{"a redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/trips", http.StatusSeeOther)
		}, http.StatusSeeOther, ""},
		{"a page rendered under an error status", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<p>the handler's own page</p>"))
		}, http.StatusInternalServerError, "<p>the handler's own page</p>"},
		{"a download that is not there", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/csv")
			w.WriteHeader(http.StatusNotFound)
		}, http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := errorPagesFor(t, tc.handler, http.MethodPost, "/admin/import/users")
			assert.Equal(t, tc.status, w.Code)
			assert.Equal(t, tc.body, w.Body.String())
		})
	}
}

// TestErrorPages_AFailedRenderIsTheErrorPage: a template that fails halfway
// leaks none of what it had written, and the admin gets the error page.
func TestErrorPages_AFailedRenderIsTheErrorPage(t *testing.T) {
	set := map[string]*template.Template{
		"boom.html": template.Must(template.New("base.html").Parse(`PARTIAL-OUTPUT{{index .Items 99}}`)),
	}
	w := errorPagesFor(t, func(w http.ResponseWriter, _ *http.Request) {
		renderInto(w, http.StatusOK, set, "boom.html", "base.html", map[string]interface{}{"Items": []int{}})
	}, http.MethodGet, "/admin/trips")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), serverErrorTitle)
	assert.NotContains(t, w.Body.String(), "PARTIAL-OUTPUT")
}

// TestErrorPages_WithoutItsTemplate: if the error page itself cannot be
// drawn, the handler's plain text is the answer.
func TestErrorPages_WithoutItsTemplate(t *testing.T) {
	ui := newTestAdminUI(t)
	ui.tmpl.public["error.html"] = template.Must(template.New("error.html").Parse(`{{index .Missing 1}}`))
	w := httptest.NewRecorder()

	ui.errorPages(http.HandlerFunc(http.NotFound)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/nope", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "404 page not found\n", w.Body.String())
}

// deadlineRecorder is a response writer that takes a write deadline, as the
// server's own does, and remembers the one it was given.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

// TestErrorPages_KeepsTheWriteDeadline: the import page moves its write
// deadline through http.ResponseController, which has to reach the server's
// own writer through the one errorPages puts in front of it.
func TestErrorPages_KeepsTheWriteDeadline(t *testing.T) {
	ui := newTestAdminUI(t)
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	want := time.Now().Add(time.Minute)

	ui.errorPages(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, http.NewResponseController(w).SetWriteDeadline(want))
	})).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/import/users/confirm", nil))

	assert.Equal(t, want, w.deadline, "the deadline reached the writer underneath")
}

// TestAdminPages_NoSuchPage: an address under /admin/ that is no page is the
// error page for an admin, and the way to sign in for anyone else.
func TestAdminPages_NoSuchPage(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	w := requestAdminPage(t, mux, "/admin/no-such-page")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `<div class="error-code">Error 404</div>`)

	for _, role := range []string{"", "driver"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/no-such-page", nil)
		if role != "" {
			req.AddCookie(cookieFor(t, role))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusSeeOther, w.Code, "role %q", role)
		assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/admin/login"), "role %q", role)
	}

	// The catch-all is for a page asked for. A form sent to an address that
	// is no page is refused by the router, in plain text, as a form sent to a
	// page that takes none is.
	req := httptest.NewRequest(http.MethodPost, "/admin/no-such-page", nil)
	req.AddCookie(cookieFor(t, "admin"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.NotContains(t, rec.Body.String(), "<html")

	// A page asked for a record that cannot be is the same page.
	w = requestAdminPage(t, mux, "/admin/driver-setup?user=not-a-number")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "The address may be old, or the trip, vehicle or user it pointed to was removed.")
}
