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

func (l *LoginRateLimiter) Allow(ip, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	okIP := allowInWindow(l.byIP, ip, loginIPLimit, now)
	okEmail := allowInWindow(l.byEmail, email, loginEmailLimit, now)
	return okIP && okEmail
}

func allowInWindow(m map[string]*loginWindowEntry, key string, limit int, now time.Time) bool {
	e, ok := m[key]
	if !ok {
		if len(m) >= maxTrackedLogins {
			slog.Warn("login rate limiter at capacity, failing closed", "capacity", maxTrackedLogins)
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

func (l *LoginRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-2 * loginWindow)
			l.mu.Lock()
			for k, e := range l.byIP {
				if e.windowStart.Before(cutoff) {
					delete(l.byIP, k)
				}
			}
			for k, e := range l.byEmail {
				if e.windowStart.Before(cutoff) {
					delete(l.byEmail, k)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}
