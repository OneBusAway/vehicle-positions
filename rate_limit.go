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
	defaultRateLimitTTL   = 10 * time.Minute
)

type rateLimitEntry struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	rate    float64
	burst   float64
	ttl     time.Duration
}

func newIPRateLimiter(rps, burst int) *ipRateLimiter {
	if rps <= 0 {
		rps = 1
	}
	if burst <= 0 {
		burst = 1
	}

	return &ipRateLimiter{
		entries: make(map[string]*rateLimitEntry),
		rate:    float64(rps),
		burst:   float64(burst),
		ttl:     defaultRateLimitTTL,
	}
}

func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupExpiredLocked(now)

	entry, ok := l.entries[ip]
	if !ok {
		l.entries[ip] = &rateLimitEntry{
			tokens:     l.burst - 1,
			lastRefill: now,
			lastSeen:   now,
		}
		return true
	}

	elapsed := now.Sub(entry.lastRefill).Seconds()
	if elapsed > 0 {
		entry.tokens += elapsed * l.rate
		if entry.tokens > l.burst {
			entry.tokens = l.burst
		}
		entry.lastRefill = now
	}

	if entry.tokens < 1 {
		entry.lastSeen = now
		return false
	}

	entry.tokens--
	entry.lastSeen = now
	return true
}

func (l *ipRateLimiter) cleanupExpiredLocked(now time.Time) {
	for ip, entry := range l.entries {
		if now.Sub(entry.lastSeen) > l.ttl {
			delete(l.entries, ip)
		}
	}
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
