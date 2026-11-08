package api

import (
	"sync"
	"time"
)

// RateLimiter is a small token bucket used by the ingest endpoint.
type RateLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// NewRateLimiter builds a limiter allowing ratePerSecond requests with the given
// burst. A non-positive rate disables limiting and returns nil.
func NewRateLimiter(ratePerSecond, burst int) *RateLimiter {
	if ratePerSecond <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = ratePerSecond
	}
	return &RateLimiter{
		rate:   float64(ratePerSecond),
		burst:  float64(burst),
		tokens: float64(burst),
		last:   time.Now(),
	}
}

// Allow consumes one token, refilling from elapsed wall time.
func (l *RateLimiter) Allow() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(l.last).Seconds()
	if elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens -= 1
	return true
}
