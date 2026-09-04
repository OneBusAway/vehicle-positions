package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRiderJWT_ClaimsAndTTL(t *testing.T) {
	tok, err := generateRiderJWT("rider-123", testSecret, 2*time.Hour)
	require.NoError(t, err)
	claims, err := parseSessionToken(tok, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "rider-123", claims["sub"])
	assert.Equal(t, "rider", claims["role"])
	exp, _ := claims["exp"].(float64)
	assert.InDelta(t, time.Now().Add(2*time.Hour).Unix(), int64(exp), 5)
}

func TestRequireRider(t *testing.T) {
	riderTok, _ := generateRiderJWT("rider-1", testSecret, time.Hour)
	driverTok, _ := generateJWT(&User{ID: 1, Email: "d@test.com", Role: "driver"}, testSecret)
	adminTok, _ := generateJWT(&User{ID: 2, Email: "a@test.com", Role: "admin"}, testSecret)

	var gotID string
	h := requireRider(testSecret, newFakeRevocations())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := riderIDFromContext(r.Context())
		require.True(t, ok)
		gotID = id
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name   string
		header string
		cookie bool
		want   int
	}{
		{"rider token", "Bearer " + riderTok, false, http.StatusNoContent},
		{"driver token", "Bearer " + driverTok, false, http.StatusForbidden},
		{"admin token", "Bearer " + adminTok, false, http.StatusForbidden},
		{"missing", "", false, http.StatusUnauthorized},
		{"garbage", "Bearer nope", false, http.StatusUnauthorized},
		{"cookie only is never accepted", "", true, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: riderTok})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
	assert.Equal(t, "rider-1", gotID)
}

func TestRegistrationRateLimiter(t *testing.T) {
	l := NewRegistrationRateLimiter()
	defer l.Stop()
	for i := 0; i < registrationIPLimit; i++ {
		assert.True(t, l.Allow("1.2.3.4"), "attempt %d", i)
	}
	assert.False(t, l.Allow("1.2.3.4"))
	assert.True(t, l.Allow("5.6.7.8"), "other IPs unaffected")
}

func TestNewKeyedRateLimiter_Burst(t *testing.T) {
	l := NewKeyedRateLimiter(2*time.Second, 2, true)
	defer l.Stop()
	assert.True(t, l.Allow("k"))
	assert.True(t, l.Allow("k"))
	assert.False(t, l.Allow("k"))
	assert.True(t, l.Allow("other"))
}

func TestNewKeyedRateLimiter_FailsClosedAtCapacityWhenAsked(t *testing.T) {
	open := NewKeyedRateLimiter(time.Hour, 1, false)
	defer open.Stop()
	closed := NewKeyedRateLimiter(time.Hour, 1, true)
	defer closed.Stop()
	for i := 0; i < maxTrackedRates; i++ {
		key := strconv.Itoa(i)
		open.Allow(key)
		closed.Allow(key)
	}
	assert.True(t, open.Allow("one-more"), "the driver limiter lets an untracked vehicle through")
	assert.False(t, closed.Allow("one-more"), "the rider limiter refuses a key it cannot track")
}
