package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
)

// errorPage is what an admin sees when a page answers with an error: what
// happened and a way out, in place of a line of plain text.
type errorPage struct {
	Status int
	Title  string
	Text   string
	// Lost draws the page's marker as a vehicle with no heading: there is
	// nowhere to point.
	Lost bool
	// Retry is the address to try again at; "" when a link cannot repeat
	// the request, as it cannot a form's.
	Retry string
	// Back offers the way back to the page the request was made from.
	Back bool
}

// newErrorPage is the page for status, or false when the status has none and
// the response is left as it is. Only what an admin page answers today has
// one: a page or record that is not there, and a failure of the server.
func newErrorPage(status int, r *http.Request) (errorPage, bool) {
	// A GET changes nothing and can be asked again with a link.
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch status {
	case http.StatusNotFound:
		return errorPage{
			Status: status,
			Title:  "This page isn't here",
			Text:   "The address may be old, or the trip, vehicle or user it pointed to was removed.",
			Lost:   true,
		}, true
	case http.StatusInternalServerError:
		page := errorPage{
			Status: status,
			Title:  "Something went wrong on the server",
			Text:   "Go back and check whether it went through before trying again. If it keeps happening, the server log has the details.",
			Back:   true,
		}
		if read {
			page.Text = "Nothing was changed. Try again in a moment. If it keeps happening, the server log has the details."
			page.Retry, page.Back = r.URL.RequestURI(), false
		}
		return page, true
	}
	return errorPage{}, false
}

// errorPages answers a bare error from next with the error page. A bare
// error is the plain text http.Error and http.NotFound write, which is how
// every admin page reports one; a page a handler rendered itself is left
// alone whatever its status. Handlers need no change to get the page, and a
// new one gets it too.
func (ui *adminUI) errorPages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&errorPageWriter{ResponseWriter: w, ui: ui, request: r}, r)
	})
}

// errorPageWriter is the ResponseWriter errorPages hands a page's handler.
type errorPageWriter struct {
	http.ResponseWriter
	ui      *adminUI
	request *http.Request
	// wroteHeader is set once the status is sent, and replaced when the
	// error page was sent with it, after which the handler's own body is
	// dropped.
	wroteHeader bool
	replaced    bool
}

// Unwrap lets http.ResponseController reach the server's own writer, which
// the import page sets its write deadline on.
func (w *errorPageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *errorPageWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	page, ok := newErrorPage(status, w.request)
	if !ok || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	var buf bytes.Buffer
	if err := w.ui.tmpl.public["error.html"].ExecuteTemplate(&buf, "error.html", page); err != nil {
		// The plain text the handler is about to write is still an answer.
		slog.Error("error page render failed", "status", status, "error", err)
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.replaced = true
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(status)
	if _, err := buf.WriteTo(w.ResponseWriter); err != nil {
		slog.Error("error page write failed", "status", status, "error", err)
	}
}

func (w *errorPageWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.replaced {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
