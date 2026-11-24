package rider

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// captureLogs points the default logger at a buffer for the rest of the test.
// Not safe for t.Parallel(); uses global logger.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return &logs
}

// withCredentials is address with "secret" everywhere a credential can go.
func withCredentials(address string) string {
	return strings.Replace(address, "://", "://secret:secret@", 1) + "?api_key=secret#secret"
}

// failingFeed is a feed that can't be fetched: its address, credentials and
// all, the address as it is shown, and what went wrong.
type failingFeed struct{ name, address, shown, reason string }

// failingFeeds is every way a fetch fails that can repeat the feed's address or
// one it redirects to.
func failingFeeds(t *testing.T) []failingFeed {
	t.Helper()
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()

	mux := http.NewServeMux()
	redirect := func(path, to string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", to)
			w.WriteHeader(http.StatusFound)
		})
	}
	redirect("/to-down", withCredentials(down.URL+"/feed"))
	redirect("/to-ftp", "ftp://secret:secret@feed.example/feed?api_key=secret")
	redirect("/to-no-escape", "/%zz?api_key=secret")
	redirect("/to-bad-port", "https://agency:secret/word@feed.example/feed")
	mux.HandleFunc("/cut", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("PK"))
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if assert.NoError(t, err) {
			defer conn.Close()
			_, err = fmt.Fprintf(conn, "%s %s\r\n\r\n", r.Method, r.RequestURI)
			assert.NoError(t, err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const invalid = "not a valid URL"
	at := func(path, reason string) failingFeed {
		return failingFeed{path[1:], withCredentials(srv.URL + path), srv.URL + path, reason}
	}
	return []failingFeed{
		{"down", withCredentials(down.URL + "/feed"), down.URL + "/feed", "connection refused"},
		at("/missing", "status 404"),
		at("/cut", "unexpected EOF"),
		at("/echo", "malformed HTTP status code"),
		at("/to-down", "connection refused"),
		at("/to-ftp", "unsupported protocol scheme"),
		at("/to-no-escape", "redirected to an address that is not a valid URL"),
		at("/to-bad-port", "redirected to an address that is not a valid URL"),
		{"no escape", "https://feed.example/%zz?api_key=secret", invalid, invalid},
		// The password's slash ends the host, so it parses as a bad port.
		{"bad port", "https://agency:secret/word@feed.example/feed", invalid, invalid},
	}
}

func TestShownFeedURL(t *testing.T) {
	for _, tc := range []struct{ name, url, want string }{
		{"nothing to leave out", "https://feed.example/vehicles.pb", "https://feed.example/vehicles.pb"},
		{"a key in the query", "https://feed.example/vehicles.pb?api_key=k", "https://feed.example/vehicles.pb"},
		{"an empty query", "https://feed.example/vehicles.pb?", "https://feed.example/vehicles.pb"},
		{"a user name and password", "https://u:p@feed.example/vehicles.pb", "https://feed.example/vehicles.pb"},
		{"a key as the user name", "https://k@feed.example/vehicles.pb", "https://feed.example/vehicles.pb"},
		{"a fragment", "https://feed.example/vehicles.pb#k", "https://feed.example/vehicles.pb"},
		{"upper case scheme", "HTTPS://feed.example/vehicles.pb?api_key=k", "https://feed.example/vehicles.pb"},
		{"no scheme", "k@feed.example/vehicles.pb", "not a valid URL"},
		{"no slashes after the scheme", "https:k@feed.example/vehicles.pb", "not a valid URL"},
		{"not a URL", "https://feed.example/%zz?api_key=k", "not a valid URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ShownFeedURL(tc.url))
		})
	}
}

func TestShownGTFSSource(t *testing.T) {
	assert.Equal(t, "https://gtfs.example/feed.zip", ShownGTFSSource("https://u:p@gtfs.example/feed.zip?key=k"))
	assert.Equal(t, "http://gtfs.example/feed.zip", ShownGTFSSource("http://gtfs.example/feed.zip?key=k"))
	// A local path is shown as it is, even one that would not parse as a URL.
	assert.Equal(t, "/srv/gtfs/100% feed?.zip", ShownGTFSSource("/srv/gtfs/100% feed?.zip"))
	// Nor is an address that is read as a path only because it is mistyped.
	assert.Equal(t, "https://gtfs.example/feed.zip", ShownGTFSSource("HTTPS://u:p@gtfs.example/feed.zip?key=k"))
	assert.Equal(t, "htps://gtfs.example/feed.zip", ShownGTFSSource("htps://u:p@gtfs.example/feed.zip?key=k"))
	assert.Equal(t, "not a valid URL", ShownGTFSSource(" https://u:p@gtfs.example/feed.zip?key=k"))
}
