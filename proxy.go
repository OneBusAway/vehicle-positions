package main

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// clientIP extracts the caller's IP. With trustProxy (TRUST_PROXY_HEADERS=true)
// the rightmost X-Forwarded-For hop is used — the value appended by our own
// reverse proxy. Without it, only the direct connection is trusted (spec §4.10).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
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
func trustProxyHeaders() bool {
	trust, _ := strconv.ParseBool(os.Getenv("TRUST_PROXY_HEADERS"))
	return trust
}
