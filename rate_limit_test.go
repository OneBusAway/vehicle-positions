package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_AllowsUnderLimit(t *testing.T) {
	limiter := newIPRateLimiter(2, 2) // 2 tokens/s, burst capacity 2
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
	})
	handler := rateLimitMiddleware(limiter, next)

	req1 := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req1.RemoteAddr = "10.0.0.1:1111"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req2.RemoteAddr = "10.0.0.1:1111"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusCreated, w1.Code)
	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Equal(t, 2, called)
}

func TestRateLimitMiddleware_RejectsBurst(t *testing.T) {
	limiter := newIPRateLimiter(1, 1) // single immediate token, then block until refill
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	handler := rateLimitMiddleware(limiter, next)

	req1 := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req1.RemoteAddr = "10.0.0.2:2222"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req2.RemoteAddr = "10.0.0.2:2222"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusCreated, w1.Code)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestIPRateLimiter_CleansUpExpiredEntries(t *testing.T) {
	limiter := newIPRateLimiter(1, 1)
	limiter.ttl = time.Millisecond

	now := time.Now()
	limiter.entries["10.0.0.1"] = &rateLimitEntry{
		tokens:     0,
		lastRefill: now.Add(-time.Second),
		lastSeen:   now.Add(-time.Second),
	}

	allowed := limiter.allow("10.0.0.2", now)
	assert.True(t, allowed)
	_, exists := limiter.entries["10.0.0.1"]
	assert.False(t, exists, "expired limiter entry should be evicted")
}
