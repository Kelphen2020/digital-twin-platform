package api

import (
	"testing"
	"time"
)

func TestRateLimiterBurstAndRefill(t *testing.T) {
	limiter := NewRateLimiter(2, 2)
	if !limiter.Allow() || !limiter.Allow() {
		t.Fatal("burst requests should be allowed")
	}
	if limiter.Allow() {
		t.Fatal("third request should be limited")
	}
	time.Sleep(600 * time.Millisecond) // ~1.2 tokens at 2/s
	if !limiter.Allow() {
		t.Fatal("token should refill after waiting")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	if NewRateLimiter(0, 10) != nil {
		t.Fatal("zero rate should disable the limiter")
	}
	var limiter *RateLimiter
	if !limiter.Allow() {
		t.Fatal("nil limiter should always allow")
	}
}
