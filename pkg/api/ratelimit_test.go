package api

import (
	"encoding/json"
	"fmt"
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

	// First two requests should succeed and advertise the remaining quota.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should succeed", i)
		assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"), "limit = burst")
		assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
	}

	// Third request should be rate limited — headers still present on the 429.
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"))
}

func TestGetClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	ip := getClientIP(req)
	assert.Equal(t, "192.168.1.1", ip)
}

func TestGetClientIP_IgnoresXForwardedForByDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18, 150.172.238.178")

	ip := getClientIP(req)
	assert.Equal(t, "10.0.0.1", ip)
}

func TestGetClientIP_IgnoresXRealIPByDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "203.0.113.195")

	ip := getClientIP(req)
	assert.Equal(t, "10.0.0.1", ip)
}

func TestRateLimiter_ClientIP_UsesForwardedForFromTrustedProxy(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         10,
		CleanupInterval:   time.Hour,
		TrustedProxies:    []string{"10.0.0.1"},
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195")
	req.Header.Set("X-Real-IP", "70.41.3.18")

	ip := rl.ClientIP(req)
	assert.Equal(t, "203.0.113.195", ip)
}

func TestRateLimiter_ClientIP_IgnoresForwardedForFromUntrustedProxy(t *testing.T) {
	cfg := RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         10,
		CleanupInterval:   time.Hour,
		TrustedProxies:    []string{"10.0.0.2"},
	}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195")

	ip := rl.ClientIP(req)
	assert.Equal(t, "10.0.0.1", ip)
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

// A client can put anything in X-Forwarded-For; nginx appends the real peer on
// the right. The limiter must key on that, not on the client-chosen left end.
func TestRateLimiter_ClientIP_IgnoresClientSuppliedForwardedFor(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         10,
		CleanupInterval:   time.Hour,
		TrustedProxies:    []string{"127.0.0.1", "10.0.0.1"},
	})
	defer rl.Stop()

	cases := []struct {
		name   string
		xff    []string
		realIP string
		want   string
	}{
		{"spoofed left entry", []string{"1.2.3.4, 203.0.113.195"}, "", "203.0.113.195"},
		{"skips trusted proxies on the right", []string{"1.2.3.4, 203.0.113.195, 10.0.0.1"}, "", "203.0.113.195"},
		{"multiple header lines", []string{"1.2.3.4", "203.0.113.195"}, "", "203.0.113.195"},
		{"all trusted falls to leftmost", []string{"10.0.0.1, 127.0.0.1"}, "", "10.0.0.1"},
		{"garbage falls back to X-Real-IP", []string{"not-an-ip"}, "198.51.100.9", "198.51.100.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			assert.Equal(t, tc.want, rl.ClientIP(req))
		})
	}
}

// Rotating the spoofed left entry must not mint fresh token buckets.
func TestRateLimiter_SpoofedForwardedForSharesOneBucket(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         3,
		CleanupInterval:   time.Hour,
		TrustedProxies:    []string{"127.0.0.1"},
	})
	defer rl.Stop()
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	limited := false
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("1.2.3.%d, 203.0.113.195", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	assert.True(t, limited, "rotating the spoofed X-Forwarded-For entry bypassed the limit")
}
