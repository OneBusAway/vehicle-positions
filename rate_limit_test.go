package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_AllowsUnderLimit(t *testing.T) {
	limiter := newIPRateLimiter(1, 1) // 2 req/s
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
	limiter := newIPRateLimiter(1, 0) // 1 req/s
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
