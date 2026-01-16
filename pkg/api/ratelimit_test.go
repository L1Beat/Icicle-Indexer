package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         10,
		CleanupInterval:   time.Hour, // Long interval for test
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	// Should allow up to burst size immediately
	for i := 0; i < 10; i++ {
		assert.True(t, rl.Allow("192.168.1.1"), "request %d should be allowed", i)
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         5,
		CleanupInterval:   time.Hour,
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	// Exhaust burst
	for i := 0; i < 5; i++ {
		rl.Allow("192.168.1.1")
	}

	// Next request should be blocked
	assert.False(t, rl.Allow("192.168.1.1"), "request over limit should be blocked")
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Hour,
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	// Exhaust IP1's limit
	rl.Allow("192.168.1.1")
	rl.Allow("192.168.1.1")
	assert.False(t, rl.Allow("192.168.1.1"))

	// IP2 should still have full limit
	assert.True(t, rl.Allow("192.168.1.2"))
	assert.True(t, rl.Allow("192.168.1.2"))
}

func TestRateLimiter_Middleware(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Hour,
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	// Simple handler that returns 200
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with rate limiter middleware
	wrapped := rl.Middleware(handler)

	// First two requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should succeed", i)
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
}

func TestGetClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	ip := getClientIP(req)
	assert.Equal(t, "192.168.1.1", ip)
}

func TestGetClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18, 150.172.238.178")

	ip := getClientIP(req)
	assert.Equal(t, "203.0.113.195", ip)
}

func TestGetClientIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "203.0.113.195")

	ip := getClientIP(req)
	assert.Equal(t, "203.0.113.195", ip)
}

func TestGetClientIP_XForwardedForPriority(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195")
	req.Header.Set("X-Real-IP", "70.41.3.18")

	// X-Forwarded-For should take priority
	ip := getClientIP(req)
	assert.Equal(t, "203.0.113.195", ip)
}

func TestRateLimiter_Returns429WithJSON(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         1,
		CleanupInterval:   time.Hour,
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := rl.Middleware(handler)

	// Exhaust limit
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Next request should return JSON error
	req = httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, ErrRateLimited, resp.Error.Code)
	assert.Equal(t, 1, resp.Error.RetryAfter)
}
