package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	RequestsPerMinute int
	BurstSize         int
	CleanupInterval   time.Duration
	TrustedProxies    []string
	// ExemptLoopback skips rate limiting for genuine same-box direct callers: a
	// loopback peer with no forwarding header. External traffic always arrives via
	// the reverse proxy (which adds X-Forwarded-For) and cannot reach the API port
	// directly (firewall), so this cannot be spoofed from outside. Lets trusted
	// same-box consumers burst freely.
	ExemptLoopback bool
}

// DefaultRateLimitConfig returns sensible defaults
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         10,
		CleanupInterval:   5 * time.Minute,
		ExemptLoopback:    true,
	}
}

// RateLimiter implements per-IP rate limiting using token bucket algorithm
type RateLimiter struct {
	limiters map[string]*rateLimiterEntry
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
	cleanup  time.Duration
	stopCh   chan struct{}
	proxies  []*net.IPNet
	exemptLB bool
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new rate limiter with the given config
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		rate:     rate.Limit(float64(cfg.RequestsPerMinute) / 60.0),
		burst:    cfg.BurstSize,
		cleanup:  cfg.CleanupInterval,
		stopCh:   make(chan struct{}),
		proxies:  parseTrustedProxies(cfg.TrustedProxies),
		exemptLB: cfg.ExemptLoopback,
	}
	go rl.cleanupLoop()
	return rl
}

// getLimiter returns the rate limiter for a given IP, creating one if needed
func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if entry, exists := rl.limiters[ip]; exists {
		entry.lastSeen = time.Now()
		return entry.limiter
	}

	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters[ip] = &rateLimiterEntry{
		limiter:  limiter,
		lastSeen: time.Now(),
	}
	return limiter
}

// Allow checks if a request from the given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	return rl.getLimiter(ip).Allow()
}

// cleanupLoop removes stale entries periodically
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup_stale()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *RateLimiter) cleanup_stale() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	threshold := time.Now().Add(-rl.cleanup)
	for ip, entry := range rl.limiters {
		if entry.lastSeen.Before(threshold) {
			delete(rl.limiters, ip)
		}
	}
}

// Stop stops the cleanup goroutine
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// Middleware returns an HTTP middleware that applies rate limiting and emits
// X-RateLimit-* headers so clients can see their remaining quota. This is a
// per-IP token bucket, so Limit is the burst capacity, Remaining is the tokens
// currently left, and Reset is the epoch second at which the bucket refills to
// full (sustained refill rate is RequestsPerMinute/min).
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Same-box direct callers on 127.0.0.1 bypass the limit: a loopback peer with
		// no forwarding header is necessarily local and trusted, and cannot be
		// impersonated from outside (external traffic comes through the proxy with
		// X-Forwarded-For and cannot reach this port directly).
		if rl.exemptLB && isLocalDirect(r) {
			w.Header().Set("X-RateLimit-Bypass", "loopback")
			next.ServeHTTP(w, r)
			return
		}

		ip := rl.ClientIP(r)
		limiter := rl.getLimiter(ip)
		allowed := limiter.Allow()

		tokens := limiter.Tokens()
		if tokens < 0 {
			tokens = 0
		}
		var secsToFull float64
		if rl.rate > 0 {
			secsToFull = (float64(rl.burst) - tokens) / float64(rl.rate)
		}
		reset := time.Now().Add(time.Duration(secsToFull * float64(time.Second)))

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.burst))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(tokens)))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))

		if !allowed {
			w.Header().Set("Retry-After", "1")
			writeRateLimitError(w, 1)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the client IP from a request. Forwarded headers are only
// trusted when the immediate peer is a configured trusted proxy.
func (rl *RateLimiter) ClientIP(r *http.Request) string {
	remoteIP := remoteAddrIP(r)
	if rl.isTrustedProxy(remoteIP) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if idx := strings.Index(xff, ","); idx != -1 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}

		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}

	return remoteIP
}

// getClientIP extracts the client IP without trusting forwarded headers.
func getClientIP(r *http.Request) string {
	return remoteAddrIP(r)
}

// isLocalDirect reports whether a request is a genuine same-box direct call: the
// immediate peer is loopback and no forwarding header is present. The reverse proxy
// always sets X-Forwarded-For, so a forwarded (external) request is never treated
// as local even though its peer is also loopback.
func isLocalDirect(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	ip := net.ParseIP(remoteAddrIP(r))
	return ip != nil && ip.IsLoopback()
}

func remoteAddrIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func (rl *RateLimiter) isTrustedProxy(remoteIP string) bool {
	if remoteIP == "" || len(rl.proxies) == 0 {
		return false
	}
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}
	for _, proxy := range rl.proxies {
		if proxy.Contains(ip) {
			return true
		}
	}
	return false
}

func parseTrustedProxies(values []string) []*net.IPNet {
	proxies := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.Contains(value, "/") {
			if _, cidr, err := net.ParseCIDR(value); err == nil {
				proxies = append(proxies, cidr)
			}
			continue
		}
		ip := net.ParseIP(value)
		if ip == nil {
			continue
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		proxies = append(proxies, &net.IPNet{
			IP:   ip,
			Mask: net.CIDRMask(bits, bits),
		})
	}
	return proxies
}
