package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gunzip is the body of a response sent with Content-Encoding: gzip.
func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(plain)
}

// pageOf is an HTML page of exactly n bytes.
func pageOf(n int) string {
	const open = "<!doctype html><html><body>"
	return open + strings.Repeat("a", n-len(open))
}

// answering is a handler that sets headers, then answers with status and body.
func answering(status int, body string, headers ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

func TestGzipped_CompressesAPageOrJSON(t *testing.T) {
	page := pageOf(4000)
	json := `{"trips":[` + strings.Repeat(`{"id":"T1"},`, 400) + `{"id":"T2"}]}`
	for _, tc := range []struct {
		name           string
		handler        http.Handler
		body, wantType string
	}{
		// A rendered page sets no Content-Type: the server works it out from
		// the first bytes, which it could not from compressed ones.
		{name: "a page with no type set", handler: answering(http.StatusOK, page), body: page, wantType: "text/html; charset=utf-8"},
		{name: "a page", handler: answering(http.StatusOK, page, "Content-Type", "text/html; charset=utf-8"), body: page, wantType: "text/html; charset=utf-8"},
		{name: "JSON", handler: answering(http.StatusOK, json, "Content-Type", "application/json"), body: json, wantType: "application/json"},
		{name: "a length that was the plain body's", handler: answering(http.StatusOK, page, "Content-Type", "text/html", "Content-Length", "4000"), body: page, wantType: "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(gzipped(tc.handler), http.MethodGet, "/admin/trips", "Accept-Encoding", "gzip, deflate, br")

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
			assert.Equal(t, "Accept-Encoding", w.Header().Get("Vary"))
			assert.Equal(t, tc.wantType, w.Header().Get("Content-Type"))
			assert.Empty(t, w.Header().Get("Content-Length"), "the plain body's length is not the compressed one's")
			assert.Less(t, w.Body.Len(), len(tc.body)/2)
			assert.Equal(t, tc.body, gunzip(t, w.Body.Bytes()))
		})
	}
}

func TestGzipped_LeavesTheRestAlone(t *testing.T) {
	page := pageOf(4000)
	for _, tc := range []struct {
		name           string
		method, accept string
		status         int
		body           string
		headers        []string
		wantVary       bool
	}{
		// A cache must still learn that the answer depends on what was asked for.
		{name: "a client that does not ask", method: http.MethodGet, accept: "", status: http.StatusOK, body: page, wantVary: true},
		{name: "a client that refuses gzip", method: http.MethodGet, accept: "gzip;q=0, identity", status: http.StatusOK, body: page, wantVary: true},
		{name: "one byte short of worth it", method: http.MethodGet, accept: "gzip", status: http.StatusOK, body: pageOf(gzipMinSize - 1)},
		{name: "marked no-store", method: http.MethodGet, accept: "gzip", status: http.StatusOK, body: page, headers: []string{"Cache-Control", "no-store"}},
		{name: "marked no-store among others", method: http.MethodGet, accept: "gzip", status: http.StatusOK, body: page, headers: []string{"Cache-Control", "private, No-Store"}},
		{name: "a CSV export", method: http.MethodGet, accept: "gzip", status: http.StatusOK, body: page, headers: []string{"Content-Type", "text/csv"}},
		{name: "plain text", method: http.MethodGet, accept: "gzip", status: http.StatusOK, body: strings.Repeat("a", 4000)},
		{name: "a type that does not parse", method: http.MethodGet, accept: "gzip", status: http.StatusOK, body: page, headers: []string{"Content-Type", "text/html; charset"}},
		{name: "an error page", method: http.MethodGet, accept: "gzip", status: http.StatusNotFound, body: page},
		{name: "part of a body", method: http.MethodGet, accept: "gzip", status: http.StatusPartialContent, body: page, headers: []string{"Content-Type", "application/json"}},
		{name: "a form's answer", method: http.MethodPost, accept: "gzip", status: http.StatusOK, body: page},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(gzipped(answering(tc.status, tc.body, tc.headers...)), tc.method, "/admin/trips", "Accept-Encoding", tc.accept)

			assert.Equal(t, tc.status, w.Code)
			assert.Empty(t, w.Header().Get("Content-Encoding"))
			assert.Equal(t, tc.body, w.Body.String())
			if tc.wantVary {
				assert.Equal(t, "Accept-Encoding", w.Header().Get("Vary"))
			} else {
				assert.Empty(t, w.Header().Get("Vary"))
			}
		})
	}
}

func TestGzipped_AtExactlyTheSmallestSizeWorthIt(t *testing.T) {
	body := pageOf(gzipMinSize)

	w := request(gzipped(answering(http.StatusOK, body)), http.MethodGet, "/admin/more", "Accept-Encoding", "gzip")

	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	assert.Equal(t, body, gunzip(t, w.Body.Bytes()))
}

// A response another layer already encoded is not encoded a second time.
func TestGzipped_LeavesAnEncodedResponseAsItIs(t *testing.T) {
	body := pageOf(4000)

	w := request(gzipped(answering(http.StatusOK, body, "Content-Type", "text/html", "Content-Encoding", "br")), http.MethodGet, "/admin/more", "Accept-Encoding", "gzip, br")

	assert.Equal(t, "br", w.Header().Get("Content-Encoding"))
	assert.Equal(t, body, w.Body.String())
}

// A HEAD is answered with the headers its GET would have.
func TestGzipped_Head(t *testing.T) {
	w := request(gzipped(answering(http.StatusOK, pageOf(4000))), http.MethodHead, "/admin/more", "Accept-Encoding", "gzip")

	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", w.Header().Get("Vary"))
}

// The status is held back until the body starts, and must still arrive when
// there is no body, or when the handler never names one.
func TestGzipped_KeepsTheStatus(t *testing.T) {
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	})
	w := request(gzipped(redirect), http.MethodGet, "/admin/map", "Accept-Encoding", "gzip")
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/login", w.Header().Get("Location"))
	assert.Empty(t, w.Header().Get("Content-Encoding"))

	noBody := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	w = request(gzipped(noBody), http.MethodGet, "/admin/map", "Accept-Encoding", "gzip")
	assert.Equal(t, http.StatusNoContent, w.Code)

	twice := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, pageOf(4000))
	})
	w = request(gzipped(twice), http.MethodGet, "/admin/map", "Accept-Encoding", "gzip")
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "the first status a handler writes is the one sent")
	assert.Empty(t, w.Header().Get("Content-Encoding"))

	unnamed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, pageOf(4000)) })
	w = request(gzipped(unnamed), http.MethodGet, "/admin/map", "Accept-Encoding", "gzip")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
}

// statusCounter counts the status lines a handler's response is given.
type statusCounter struct {
	*httptest.ResponseRecorder
	statuses int
}

func (c *statusCounter) WriteHeader(status int) {
	c.statuses++
	c.ResponseRecorder.WriteHeader(status)
}

// The status is sent once, with the body's first bytes or, when there is no
// body, at the end: the server logs a second one as a mistake.
func TestGzipped_SendsTheStatusOnce(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"compressed":     answering(http.StatusOK, pageOf(4000)),
		"not compressed": answering(http.StatusNotFound, pageOf(4000)),
		"no body":        http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	} {
		w := &statusCounter{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(http.MethodGet, "/admin/trips", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		gzipped(h).ServeHTTP(w, req)

		assert.Equal(t, 1, w.statuses, name)
	}
}

// A compressor goes back to the pool after each response and is reused by the
// next, which must start a stream of its own.
func TestGzipped_OneResponseAfterAnother(t *testing.T) {
	n := 0
	h := gzipped(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		_, _ = fmt.Fprintf(w, "<!doctype html><html><body>page %d %s", n, strings.Repeat("b", 3000))
		_, _ = io.WriteString(w, " and its end")
	}))
	for i := 1; i <= 3; i++ {
		w := request(h, http.MethodGet, "/admin/more", "Accept-Encoding", "gzip")
		require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
		assert.Equal(t, fmt.Sprintf("<!doctype html><html><body>page %d %s and its end", i, strings.Repeat("b", 3000)), gunzip(t, w.Body.Bytes()))
	}
}

// Requests in flight together each get a compressor to themselves, and each
// its own page back.
func TestGzipped_ManyAtOnce(t *testing.T) {
	h := gzipped(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<!doctype html><html><body>"+strings.Repeat(r.URL.Query().Get("n")+" ", 800))
	}))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				n := strconv.Itoa(i*100 + j)
				w := request(h, http.MethodGet, "/admin/trips?n="+n, "Accept-Encoding", "gzip")
				if !assert.Equal(t, "gzip", w.Header().Get("Content-Encoding")) {
					return
				}
				assert.Equal(t, "<!doctype html><html><body>"+strings.Repeat(n+" ", 800), gunzip(t, w.Body.Bytes()))
			}
		}()
	}
	wg.Wait()
}

// A handler behind gzipped can still move its write deadline, as one behind
// requestLogger and errorPages can.
func TestGzipped_KeepsTheWriteDeadline(t *testing.T) {
	want := time.Now().Add(time.Minute)
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}

	gzipped(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, http.NewResponseController(w).SetWriteDeadline(want))
	})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/trips", nil))

	assert.Equal(t, want, w.deadline, "the deadline reached the writer underneath")
}

// A flush sends on what was written so far in a form the client can already
// read, and the status that was being held with it.
func TestGzipped_Flush(t *testing.T) {
	first := pageOf(4000)
	var flushed string
	rec := httptest.NewRecorder()
	h := gzipped(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, first)
		require.NoError(t, http.NewResponseController(w).Flush())
		zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		require.NoError(t, err)
		got := make([]byte, len(first))
		_, err = io.ReadFull(zr, got)
		require.NoError(t, err, "everything written before the flush can be read after it")
		flushed = string(got)
		_, _ = io.WriteString(w, " and more")
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/trips", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	assert.Equal(t, first, flushed)
	assert.True(t, rec.Flushed)
	assert.Equal(t, first+" and more", gunzip(t, rec.Body.Bytes()))

	// Before any body: the status goes out as the handler wrote it, and
	// what follows is not compressed, its size never having been seen.
	rec = httptest.NewRecorder()
	h = gzipped(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		require.NoError(t, http.NewResponseController(w).Flush())
		_, _ = io.WriteString(w, first)
	}))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.True(t, rec.Flushed)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, first, rec.Body.String())
}

// brokenWriter is a client that has gone away.
type brokenWriter struct{ *httptest.ResponseRecorder }

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("connection reset") }

// Not safe for t.Parallel(); uses global logger.
func TestGzipped_LogsABodyCutShort(t *testing.T) {
	logs := captureLogs(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/trips", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	gzipped(answering(http.StatusOK, pageOf(4000))).ServeHTTP(brokenWriter{httptest.NewRecorder()}, req)

	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "compressed response was not written to its end")
	assert.Contains(t, logs.String(), "path=/admin/trips")
	assert.Contains(t, logs.String(), "connection reset")

	// A flush into the same broken connection reports it to the handler.
	var flushErr error
	gzipped(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, pageOf(4000))
		flushErr = http.NewResponseController(w).Flush()
	})).ServeHTTP(brokenWriter{httptest.NewRecorder()}, req)
	assert.ErrorContains(t, flushErr, "connection reset")
}

func TestAcceptsGzip(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"gzip", true},
		{"gzip, deflate, br, zstd", true},
		{"br, GZIP", true},
		{"deflate,gzip;q=0.5", true},
		{"gzip; q=1", true},
		{"gzip;Q=0.001", true},
		{"", false},
		{"identity", false},
		{"br, deflate", false},
		{"gzip;q=0", false},
		{"gzip; q=0.000", false},
		{"gzip;Q=0", false},
		{"gzip;q=none", false},
		{"gzip;q=1e999", false},
		{"gzipped", false},
		{"x-gzip", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", tc.header)
		assert.Equal(t, tc.want, acceptsGzip(r), "Accept-Encoding: %s", tc.header)
	}

	// A client may send the header more than once.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add("Accept-Encoding", "br")
	r.Header.Add("Accept-Encoding", "gzip")
	assert.True(t, acceptsGzip(r))
}

// compressAdminSite's addresses, with a handler that answers every one the
// same way: what differs is only what was put around it.
func TestCompressAdminSite_ByAddress(t *testing.T) {
	body := `{"trips":[` + strings.Repeat(`{"id":"T1"},`, 400) + `{"id":"T2"}]}`
	h := compressAdminSite(answering(http.StatusOK, body, "Content-Type", "application/json"))

	for _, tc := range []struct {
		path       string
		compressed bool
	}{
		{"/admin/trips", true},
		{"/admin/login", true},
		{"/api/v1/admin/vehicles/live", true},
		{"/api/v1/admin/trips/7/locations", true},
		{"/api/v1/gtfs/routes", true},
		{"/api/v1/gtfs/routes/R1/trips", true},
		{"/api/v1/gtfs/trips/T1", true},
		{"/api/v1/trips", false},
		{"/api/v1/vehicles", false},
		{"/api/v1/administrators", false},
		{"/api/v1/gtfsx", false},
		{"/administrators", false},
		{"/gtfs-rt/vehicle-positions", false},
		{"/health", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := request(h, http.MethodGet, tc.path, "Accept-Encoding", "gzip")

			require.Equal(t, http.StatusOK, w.Code)
			if tc.compressed {
				require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
				assert.Equal(t, body, gunzip(t, w.Body.Bytes()))
			} else {
				assert.Empty(t, w.Header().Get("Content-Encoding"))
				assert.Equal(t, body, w.Body.String())
			}
		})
	}
}

// The production handler is the one wrapped: a page and the admin API reach
// a browser compressed.
func TestNewHandler_SendsTheAdminSiteCompressed(t *testing.T) {
	h := newSiteHandler(t)
	get := func(path string) *httptest.ResponseRecorder {
		return getPage(t, h, path, "Accept-Encoding", "gzip")
	}

	for _, path := range []string{"/admin/login", "/admin/more", "/admin/map"} {
		w := get(path)
		require.Equal(t, http.StatusOK, w.Code, path)
		require.Equal(t, "gzip", w.Header().Get("Content-Encoding"), path)
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"), path)
		assert.Contains(t, gunzip(t, w.Body.Bytes()), "</html>", path)
	}

	health := get("/api/v1/admin/health")
	require.Equal(t, http.StatusOK, health.Code)
	require.Equal(t, "gzip", health.Header().Get("Content-Encoding"))
	assert.Contains(t, gunzip(t, health.Body.Bytes()), `"reports"`)
}
