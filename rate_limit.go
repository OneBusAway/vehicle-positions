package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	defaultRateLimitRPS   = 5
	defaultRateLimitBurst = 10
)

type rateLimitEntry struct {
	windowStart time.Time
	count       int
}

type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	limit   int
}

func newIPRateLimiter(rps, burst int) *ipRateLimiter {
	limit := rps + burst
	if limit <= 0 {
		limit = 1
	}

	return &ipRateLimiter{
		entries: make(map[string]*rateLimitEntry),
		limit:   limit,
	}
}

func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[ip]
	if !ok {
		l.entries[ip] = &rateLimitEntry{windowStart: now, count: 1}
		return true
	}

	if now.Sub(entry.windowStart) >= time.Second {
		entry.windowStart = now
		entry.count = 1
		return true
	}

	if entry.count >= l.limit {
		return false
	}
	entry.count++
	return true
}

func rateLimitMiddleware(limiter *ipRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r), time.Now()) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return r.RemoteAddr
	}
	return host
}
