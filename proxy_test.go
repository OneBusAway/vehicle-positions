package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 192.0.2.7")

	assert.Equal(t, "203.0.113.9", clientIP(req, false), "untrusted: RemoteAddr host wins")
	assert.Equal(t, "192.0.2.7", clientIP(req, true), "trusted: rightmost XFF hop")

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.9:4567"
	assert.Equal(t, "203.0.113.9", clientIP(req2, true), "trusted but no header: RemoteAddr")
}

// TestClientIP_ProxyHopOnItsOwnLine is a proxy that adds its hop as a second
// X-Forwarded-For line instead of appending to the client's, as HAProxy does.
// The client's line is whatever the client sent, so reading it would let one
// address pick a new rate-limit bucket for every request.
func TestClientIP_ProxyHopOnItsOwnLine(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:4567"
	req.Header.Add("X-Forwarded-For", "198.51.100.66")
	req.Header.Add("X-Forwarded-For", "192.0.2.7")

	assert.Equal(t, "192.0.2.7", clientIP(req, true))

	req.Header.Set("X-Forwarded-For", "198.51.100.66, 198.51.100.1")
	req.Header.Add("X-Forwarded-For", "10.0.0.5, 192.0.2.7")
	assert.Equal(t, "192.0.2.7", clientIP(req, true), "the last hop of the last line")
}

// TestClientIP_HopWithPort is a proxy that sends its hop as IP:port, as Azure
// Application Gateway does by default. The port is the client's source port,
// so keeping it would give every connection a fresh rate-limit bucket.
func TestClientIP_HopWithPort(t *testing.T) {
	tests := []struct {
		name string
		xff  string
		want string
	}{
		{"IPv4 with port", "192.0.2.7:51234", "192.0.2.7"},
		{"IPv4 with port after other hops", "198.51.100.1, 192.0.2.7:51234", "192.0.2.7"},
		{"IPv6 with port", "[2001:db8::7]:51234", "2001:db8::7"},
		{"bare IPv6", "2001:db8::7", "2001:db8::7"},
		{"bare IPv4", "192.0.2.7", "192.0.2.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "203.0.113.9:4567"
			req.Header.Set("X-Forwarded-For", tt.xff)

			assert.Equal(t, tt.want, clientIP(req, true))
		})
	}
}

func TestRequestIsSecure(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, requestIsSecure(req, false))
	req.Header.Set("X-Forwarded-Proto", "https")
	assert.False(t, requestIsSecure(req, false), "untrusted header ignored")
	assert.True(t, requestIsSecure(req, true))
	reqTLS := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	assert.True(t, requestIsSecure(reqTLS, false), "real TLS always secure")
}
