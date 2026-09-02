package main

import (
	"log/slog"
	"sync"
	"time"
)

const (
	loginIPLimit     = 10
	loginEmailLimit  = 5
	loginWindow      = time.Minute
	maxTrackedLogins = 10_000
)

type loginWindowEntry struct {
	count       int
	windowStart time.Time
}

// LoginRateLimiter guards login endpoints with per-IP and per-email fixed
// windows. Unlike VehicleRateLimiter it FAILS CLOSED at capacity — an auth
// endpoint must not become unlimited under memory pressure (spec §4.11).
type LoginRateLimiter struct {
	mu      sync.Mutex
	byIP    map[string]*loginWindowEntry
	byEmail map[string]*loginWindowEntry
	stop    chan struct{}
	once    sync.Once
}

func NewLoginRateLimiter() *LoginRateLimiter {
	l := &LoginRateLimiter{
		byIP:    make(map[string]*loginWindowEntry),
		byEmail: make(map[string]*loginWindowEntry),
		stop:    make(chan struct{}),
	}
	go l.cleanup()
	return l
}

func (l *LoginRateLimiter) Stop() { l.once.Do(func() { close(l.stop) }) }

// Allow checks the IP dimension first and returns false immediately, without
// touching byEmail, when the IP is already blocked. This prevents a single
// IP from spraying distinct emails to fill byEmail up to maxTrackedLogins
// while it is itself IP-blocked, which would otherwise fail every new key
// closed once the map hit capacity (a map-filling DoS from one IP). When the
// IP is not blocked, a single Allow call still consumes budget from both
// dimensions, matching prior behavior.
func (l *LoginRateLimiter) Allow(ip, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if !allowInWindow(l.byIP, ip, loginIPLimit, now, "login") {
		return false
	}
	return allowInWindow(l.byEmail, email, loginEmailLimit, now, "login")
}

// ResetEmail clears the per-email window after a successful authentication,
// so an account legitimately signing in several times a minute (a shared
// account, or the driver app plus the admin form) isn't 429'd despite zero
// failed attempts. Only the email dimension is reset — an attacker can't
// trigger it without valid credentials, and the per-IP budget (the
// map-filling-DoS defense) is never relaxed.
func (l *LoginRateLimiter) ResetEmail(email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byEmail, email)
}

// allowInWindow is the fixed-window admission shared by the login and rider
// registration limiters; name says which one is speaking when it fails closed.
func allowInWindow(m map[string]*loginWindowEntry, key string, limit int, now time.Time, name string) bool {
	e, ok := m[key]
	if !ok {
		if len(m) >= maxTrackedLogins {
			slog.Warn("rate limiter at capacity, failing closed", "limiter", name, "capacity", maxTrackedLogins)
			return false
		}
		m[key] = &loginWindowEntry{count: 1, windowStart: now}
		return true
	}
	if now.Sub(e.windowStart) >= loginWindow {
		e.count = 1
		e.windowStart = now
		return true
	}
	e.count++
	return e.count <= limit
}

// pruneStaleWindows deletes entries whose window started before cutoff.
// Caller must hold the limiter's mutex.
func pruneStaleWindows(m map[string]*loginWindowEntry, cutoff time.Time) {
	for k, e := range m {
		if e.windowStart.Before(cutoff) {
			delete(m, k)
		}
	}
}

func (l *LoginRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-2 * loginWindow)
			l.mu.Lock()
			pruneStaleWindows(l.byIP, cutoff)
			pruneStaleWindows(l.byEmail, cutoff)
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}
