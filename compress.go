package main

import (
	"compress/gzip"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// compressAdminSite sends what the admin website loads in fewer bytes: its
// pages, the admin API its scripts read, and the timetable routes its map
// reads. The driver apps read the timetable routes as well and get the same.
func compressAdminSite(next http.Handler) http.Handler {
	compressed := gzipped(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasPrefix(p, "/admin/"), strings.HasPrefix(p, "/api/v1/admin/"), strings.HasPrefix(p, "/api/v1/gtfs/"):
			compressed.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// gzipMinSize is the smallest body worth compressing. A shorter one fits in
// one packet as it is.
const gzipMinSize = 1024

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// gzipped compresses the answer to a GET for a client that accepts gzip, when
// the answer is a page or JSON of at least gzipMinSize bytes sent with a 200.
// The size is that of the handler's first write, which for a rendered page
// and for writeJSON is the whole body.
//
// A response marked no-store is left alone. The pages marked so show a
// secret once, a new API key or a new driver's password, and the size of a
// compressed page that holds a secret beside text a visitor chose can give
// the secret away (the BREACH attack).
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w, accepted: acceptsGzip(r)}
		next.ServeHTTP(gw, r)
		if err := gw.finish(); err != nil {
			// The client went away, or the connection failed, part way.
			slog.Warn("compressed response was not written to its end", "path", r.URL.Path, "error", err)
		}
	})
}

// gzipResponseWriter is the ResponseWriter gzipped hands a handler.
type gzipResponseWriter struct {
	http.ResponseWriter
	// accepted is whether the client takes gzip.
	accepted bool
	// status is the handler's, held back until the first bytes of the body
	// show whether the response will be compressed: its headers differ.
	status  int
	started bool
	// gz is set while the body is being compressed.
	gz *gzip.Writer
}

// Unwrap lets http.ResponseController reach the server's own writer, to move
// a deadline.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.start(b)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// FlushError is what http.ResponseController calls to flush. Without it the
// controller would reach past this writer through Unwrap and send the
// server's headers before the status held here.
func (w *gzipResponseWriter) FlushError() error {
	if !w.started {
		w.start(nil)
	}
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// start decides, from the headers the handler set and the first bytes of the
// body, whether the response is compressed, then sends the status.
func (w *gzipResponseWriter) start(first []byte) {
	w.started = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	h := w.Header()
	if w.status == http.StatusOK && len(first) >= gzipMinSize && h.Get("Content-Encoding") == "" && !marksNoStore(h) {
		contentType := h.Get("Content-Type")
		if contentType == "" {
			// What the server would work out from these bytes itself, which
			// it cannot once they are compressed.
			contentType = http.DetectContentType(first)
		}
		if gzipContentType(contentType) {
			h.Add("Vary", "Accept-Encoding")
			if w.accepted {
				h.Set("Content-Type", contentType)
				h.Set("Content-Encoding", "gzip")
				h.Del("Content-Length")
				w.gz = gzipWriters.Get().(*gzip.Writer)
				w.gz.Reset(w.ResponseWriter)
			}
		}
	}
	w.ResponseWriter.WriteHeader(w.status)
}

// finish ends the compressed body, or sends the status of a response that
// had no body.
func (w *gzipResponseWriter) finish() error {
	if w.gz != nil {
		err := w.gz.Close()
		gzipWriters.Put(w.gz)
		w.gz = nil
		return err
	}
	if !w.started && w.status != 0 {
		w.ResponseWriter.WriteHeader(w.status)
	}
	return nil
}

// gzipContentType reports whether a Content-Type is one gzipped compresses:
// a page or JSON.
func gzipContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mediaType == "text/html" || mediaType == "application/json")
}

func marksNoStore(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Cache-Control")), "no-store")
}

// acceptsGzip reports whether the request's Accept-Encoding names gzip with
// a quality above zero.
func acceptsGzip(r *http.Request) bool {
	for _, value := range r.Header.Values("Accept-Encoding") {
		for _, part := range strings.Split(value, ",") {
			coding, params, _ := strings.Cut(part, ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
				continue
			}
			quality, weighted := strings.CutPrefix(strings.ToLower(strings.TrimSpace(params)), "q=")
			if !weighted {
				return true
			}
			q, err := strconv.ParseFloat(quality, 64)
			return err == nil && q > 0
		}
	}
	return false
}
