package main

import (
	"net"
	"net/http"
	"strings"
)

// clientIP extracts the caller's IP. With trustProxy (TRUST_PROXY_HEADERS=true)
// the rightmost X-Forwarded-For hop is used — the value appended by our own
// reverse proxy. Without it, only the direct connection is trusted (spec §4.10).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		// Several X-Forwarded-For lines are one list (RFC 9110 section 5.3),
		// and a proxy may append its hop as a line of its own after the
		// client's (HAProxy does), so the rightmost hop ends the last line.
		if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				// Some proxies send the hop as IP:port (Azure Application
				// Gateway does by default). The port changes with every
				// connection, so keeping it would give each one its own
				// rate-limit bucket. A bare IPv6 address has no port and
				// fails SplitHostPort, so it is kept as it is.
				if host, _, err := net.SplitHostPort(ip); err == nil {
					return host
				}
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestIsSecure reports whether the request arrived over HTTPS, honoring
// X-Forwarded-Proto only when proxy headers are trusted.
func requestIsSecure(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	return trustProxy && r.Header.Get("X-Forwarded-Proto") == "https"
}

// trustProxyHeaders reports whether X-Forwarded-For/-Proto should be trusted
// for client-IP and HTTPS detection, controlled by TRUST_PROXY_HEADERS
// (default false — only set this behind a reverse proxy that overwrites
// those headers itself).
func trustProxyHeaders() bool { return envBoolOrDefault("TRUST_PROXY_HEADERS", false) }
