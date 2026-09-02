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
	// failClosed refuses new keys once the map is full instead of letting
	// them through untracked. The driver limiter fails open — vehicle ids come
	// from authenticated staff, and a full map means a busy fleet — but a key
	// an anonymous client can mint for itself (a rider id) must not be able
	// to fill the map and switch the limit off for everyone after it.
	failClosed bool
	stop       chan struct{}
	once       sync.Once
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewVehicleRateLimiter() *VehicleRateLimiter {
	return NewKeyedRateLimiter(rateInterval, 1, false)
}

// NewKeyedRateLimiter builds a per-key token-bucket limiter allowing one event
// every interval with the given burst, failing closed or open at capacity (see
// VehicleRateLimiter.failClosed). It is the general form of
// NewVehicleRateLimiter, which is NewKeyedRateLimiter(rateInterval, 1, false).
func NewKeyedRateLimiter(interval time.Duration, burst int, failClosed bool) *VehicleRateLimiter {
	vrl := &VehicleRateLimiter{
		limiters:   make(map[string]*rateLimiterEntry),
		interval:   interval,
		burst:      burst,
		failClosed: failClosed,
		stop:       make(chan struct{}),
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
			if vrl.failClosed {
				slog.Warn("rate limiter at capacity, failing closed", "capacity", maxTrackedRates, "key", key)
				return false
			}
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
