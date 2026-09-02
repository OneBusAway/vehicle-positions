package main

import (
	"log/slog"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	rateInterval    = 5 * time.Second
	maxTrackedRates = 10_000
)

type VehicleRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
	interval time.Duration
	burst    int
	stop     chan struct{}
	once     sync.Once
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewVehicleRateLimiter() *VehicleRateLimiter {
	return NewKeyedRateLimiter(rateInterval, 1)
}

// NewKeyedRateLimiter builds a per-key token-bucket limiter allowing one event
// every interval with the given burst. It is the general form of
// NewVehicleRateLimiter, which is exactly NewKeyedRateLimiter(rateInterval, 1).
func NewKeyedRateLimiter(interval time.Duration, burst int) *VehicleRateLimiter {
	vrl := &VehicleRateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		interval: interval,
		burst:    burst,
		stop:     make(chan struct{}),
	}
	go vrl.cleanup()
	return vrl
}

// Stop shuts down the background cleanup goroutine.
func (vrl *VehicleRateLimiter) Stop() {
	vrl.once.Do(func() { close(vrl.stop) })
}

func (vrl *VehicleRateLimiter) Allow(key string) bool {
	vrl.mu.Lock()
	defer vrl.mu.Unlock()

	entry, ok := vrl.limiters[key]
	if !ok {
		if len(vrl.limiters) >= maxTrackedRates {
			slog.Warn("rate limiter at capacity, allowing untracked key", "capacity", maxTrackedRates, "key", key)
			return true
		}
		entry = &rateLimiterEntry{
			limiter: rate.NewLimiter(rate.Every(vrl.interval), vrl.burst),
		}
		vrl.limiters[key] = entry
	}

	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

func (vrl *VehicleRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-time.Minute)
			vrl.mu.Lock()
			for id, entry := range vrl.limiters {
				if entry.lastSeen.Before(cutoff) {
					delete(vrl.limiters, id)
				}
			}
			vrl.mu.Unlock()
		case <-vrl.stop:
			return
		}
	}
}
