package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoginRateLimiterPerIP(t *testing.T) {
	l := NewLoginRateLimiter()
	defer l.Stop()
	for i := 0; i < 10; i++ {
		assert.True(t, l.Allow("1.2.3.4", fmt.Sprintf("u%d@test.com", i)), "attempt %d", i)
	}
	assert.False(t, l.Allow("1.2.3.4", "another@test.com"), "11th attempt from same IP blocked")
	assert.True(t, l.Allow("5.6.7.8", "fresh@test.com"), "other IP unaffected")
}

func TestLoginRateLimiterPerEmail(t *testing.T) {
	l := NewLoginRateLimiter()
	defer l.Stop()
	for i := 0; i < 5; i++ {
		assert.True(t, l.Allow(fmt.Sprintf("10.0.0.%d", i), "target@test.com"))
	}
	assert.False(t, l.Allow("10.0.0.99", "target@test.com"), "6th attempt on same email blocked across IPs")
}

// TestLoginRateLimiterIPBlockDoesNotGrowEmailMap guards against a
// map-filling DoS: once an IP is blocked, further Allow calls from that IP
// with brand-new emails must not insert into byEmail at all. A single IP
// spraying distinct emails must not be able to grow byEmail toward
// maxTrackedLogins while it is itself blocked.
func TestLoginRateLimiterIPBlockDoesNotGrowEmailMap(t *testing.T) {
	l := NewLoginRateLimiter()
	defer l.Stop()

	ip := "1.2.3.4"
	for i := 0; i < loginIPLimit; i++ {
		assert.True(t, l.Allow(ip, fmt.Sprintf("u%d@test.com", i)), "attempt %d within IP limit", i)
	}

	l.mu.Lock()
	emailCountAtIPLimit := len(l.byEmail)
	l.mu.Unlock()
	assert.Equal(t, loginIPLimit, emailCountAtIPLimit)

	// The IP is now blocked. Further attempts with brand-new emails must be
	// denied without inserting into byEmail.
	for i := loginIPLimit; i < loginIPLimit+20; i++ {
		assert.False(t, l.Allow(ip, fmt.Sprintf("u%d@test.com", i)), "IP-blocked attempt %d", i)
	}

	l.mu.Lock()
	emailCountAfter := len(l.byEmail)
	l.mu.Unlock()
	assert.Equal(t, emailCountAtIPLimit, emailCountAfter, "byEmail must not grow while IP is blocked")

	// One of the emails that was never inserted, tried from a fresh IP,
	// should still get its full per-email attempt budget.
	freshIP := "9.9.9.9"
	neverInserted := fmt.Sprintf("u%d@test.com", loginIPLimit)
	for i := 0; i < loginEmailLimit; i++ {
		assert.True(t, l.Allow(freshIP, neverInserted), "fresh IP + never-inserted email attempt %d", i)
	}
	assert.False(t, l.Allow(freshIP, neverInserted), "email budget exhausted after loginEmailLimit attempts")
}

// TestLoginRateLimiterEmailMapCapacityFailsClosed exercises the real
// maxTrackedLogins constant: once byEmail is at capacity, a brand-new email
// (from an IP that is not itself blocked) must be denied rather than
// silently allowed.
func TestLoginRateLimiterEmailMapCapacityFailsClosed(t *testing.T) {
	l := NewLoginRateLimiter()
	defer l.Stop()

	now := time.Now()
	l.mu.Lock()
	for i := 0; i < maxTrackedLogins; i++ {
		l.byEmail[fmt.Sprintf("filler%d@test.com", i)] = &loginWindowEntry{count: 1, windowStart: now}
	}
	l.mu.Unlock()

	assert.False(t, l.Allow("9.9.9.9", "newcomer@test.com"), "new email denied when byEmail is at capacity")
}
