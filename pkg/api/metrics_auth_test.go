package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetricsEndpoint_DisabledWithoutToken(t *testing.T) {
	server := NewServer(&MockConn{}, Config{
		RateLimit: RateLimitConfig{
			RequestsPerMinute: 1000,
			BurstSize:         100,
			CleanupInterval:   time.Hour,
		},
	})
	defer server.Stop()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMetricsEndpoint_RequiresBearerToken(t *testing.T) {
	server := NewServer(&MockConn{}, Config{
		RateLimit: RateLimitConfig{
			RequestsPerMinute: 1000,
			BurstSize:         100,
			CleanupInterval:   time.Hour,
		},
		Metrics: MetricsConfig{Token: "secret"},
	})
	defer server.Stop()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMetricsEndpoint_AllowsValidBearerToken(t *testing.T) {
	server := NewServer(&MockConn{}, Config{
		RateLimit: RateLimitConfig{
			RequestsPerMinute: 1000,
			BurstSize:         100,
			CleanupInterval:   time.Hour,
		},
		Metrics: MetricsConfig{Token: "secret"},
	})
	defer server.Stop()

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
