package main

import (
	"sync"
	"time"
)

// refreshIPLimit is the number of token refreshes allowed per IP per
// loginWindow. Drivers who log in at the same pull-out get access tokens that
// expire together, so they refresh together, and a depot's drivers usually
// share one public IP. 60 covers a 50-driver depot's burst with room left for
// retries; a bigger depot behind one address would still be throttled.
const refreshIPLimit = 60

// RefreshRateLimiter guards token refresh with a per-IP fixed window. Like
// LoginRateLimiter it FAILS CLOSED at capacity, since refresh mints
// credentials too.
//
// It does not share LoginRateLimiter's per-IP budget. Login's budget is tight
// because a password can be guessed; a refresh token is 32 random bytes and
// cannot be, so a separate allowance gives an attacker nothing against login.
// What this limiter guards against is flooding, and that needs a cap generous
// enough for a depot's synchronised refreshes not to lock its drivers out of
// logging in.
type RefreshRateLimiter struct {
	mu    sync.Mutex
	byIP  map[string]*loginWindowEntry
	limit int
	stop  chan struct{}
	once  sync.Once
}

func NewRefreshRateLimiter() *RefreshRateLimiter {
	l := &RefreshRateLimiter{
		byIP:  make(map[string]*loginWindowEntry),
		limit: refreshIPLimit,
		stop:  make(chan struct{}),
	}
	go l.cleanup()
	return l
}

func (l *RefreshRateLimiter) Stop() { l.once.Do(func() { close(l.stop) }) }

// Allow reports whether another refresh from ip fits inside the current
// window.
func (l *RefreshRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return allowInWindow(l.byIP, ip, l.limit, time.Now(), "refresh")
}

func (l *RefreshRateLimiter) cleanup() {
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
