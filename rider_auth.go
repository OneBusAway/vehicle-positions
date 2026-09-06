package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateRiderJWT signs a rider session token. The subject is the rider's
// store id; there is no email claim because riders are anonymous.
func generateRiderJWT(riderID string, secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"sub":  riderID,
		"role": roleRider,
		"exp":  now.Add(ttl).Unix(),
		"iat":  now.Unix(),
		"iss":  "vehicle-positions-api",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// requireRider is middleware that validates the Bearer rider JWT on the rider
// API. Unlike requireAuth there is no cookie fallback — rider tokens belong to
// a mobile client, never to a browser session, so a cookie is never accepted.
func requireRider(secret []byte) func(http.Handler) http.Handler {
	return requireRoles(secret, false, roleRider)
}

// riderIDFromContext returns the rider id from claims stored by requireRider.
// It reports false unless the claims carry the rider role, so a driver or
// admin token can never be mistaken for a rider.
func riderIDFromContext(ctx context.Context) (string, bool) {
	claims, ok := ctx.Value(claimsKey).(jwt.MapClaims)
	if !ok {
		return "", false
	}
	if role, _ := claims["role"].(string); role != roleRider {
		return "", false
	}
	id, ok := claims["sub"].(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// registrationIPLimit is the number of rider registrations allowed per IP per
// loginWindow.
const registrationIPLimit = 5

// RegistrationRateLimiter guards rider registration with a per-IP fixed
// window. Like LoginRateLimiter it FAILS CLOSED at capacity — registration
// mints credentials, so it must not become unlimited under memory pressure.
type RegistrationRateLimiter struct {
	mu    sync.Mutex
	byIP  map[string]*loginWindowEntry
	limit int
	stop  chan struct{}
	once  sync.Once
}

func NewRegistrationRateLimiter() *RegistrationRateLimiter {
	l := &RegistrationRateLimiter{
		byIP:  make(map[string]*loginWindowEntry),
		limit: registrationIPLimit,
		stop:  make(chan struct{}),
	}
	go l.cleanup()
	return l
}

func (l *RegistrationRateLimiter) Stop() { l.once.Do(func() { close(l.stop) }) }

// Allow reports whether another registration from ip fits inside the current
// window.
func (l *RegistrationRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return allowInWindow(l.byIP, ip, l.limit, time.Now(), "rider registration")
}

func (l *RegistrationRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-2 * loginWindow)
			l.mu.Lock()
			pruneStaleWindows(l.byIP, cutoff)
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}
