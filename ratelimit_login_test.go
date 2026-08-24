package main

import (
	"fmt"
	"testing"

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
