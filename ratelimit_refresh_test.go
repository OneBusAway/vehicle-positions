package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshRateLimiter_AllowsUpToLimit(t *testing.T) {
	l := NewRefreshRateLimiter()
	defer l.Stop()

	for i := range refreshIPLimit {
		assert.True(t, l.Allow("1.2.3.4"), "attempt %d", i)
	}
	assert.False(t, l.Allow("1.2.3.4"), "the window must close one past refreshIPLimit")
}

func TestRefreshRateLimiter_IsolatesIPs(t *testing.T) {
	l := NewRefreshRateLimiter()
	defer l.Stop()

	for range refreshIPLimit {
		require.True(t, l.Allow("1.2.3.4"))
	}
	require.False(t, l.Allow("1.2.3.4"))

	assert.True(t, l.Allow("5.6.7.8"), "an exhausted IP must not spend another IP's budget")
}

// TestRefreshRateLimiter_CapacityFailsClosed pins that a refresh from an
// address the limiter has no room to track is refused, not waved through.
func TestRefreshRateLimiter_CapacityFailsClosed(t *testing.T) {
	l := NewRefreshRateLimiter()
	defer l.Stop()

	now := time.Now()
	l.mu.Lock()
	for i := range maxTrackedLogins {
		l.byIP[fmt.Sprintf("filler-%d", i)] = &loginWindowEntry{count: 1, windowStart: now}
	}
	l.mu.Unlock()

	assert.False(t, l.Allow("9.9.9.9"), "a new IP must be refused when byIP is at capacity")
}

func TestRefreshRateLimiter_StopIsIdempotent(t *testing.T) {
	l := NewRefreshRateLimiter()

	returned := make(chan struct{})
	go func() {
		l.Stop()
		l.Stop()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a second Stop must return, not block or panic")
	}
}
