package api

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "icicle_api_requests_total",
			Help: "Total number of HTTP requests by endpoint and status code.",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "icicle_api_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

// MetricsMiddleware records request duration and status code for Prometheus.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip metrics endpoint and WebSocket connections
		// WebSocket connections are long-lived and would skew latency metrics
		if r.URL.Path == "/metrics" || len(r.URL.Path) > 4 && r.URL.Path[:4] == "/ws/" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		// Use the same loggingResponseWriter to capture status code
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(lrw.statusCode)

		// Normalize path to avoid high cardinality from path parameters
		path := normalizePath(r.URL.Path)

		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}

// knownExactPaths are static routes that should be recorded as-is.
var knownExactPaths = map[string]bool{
	"/health":                        true,
	"/api/docs/":                     true,
	"/api/v1/data/pchain/txs":        true,
	"/api/v1/data/pchain/tx-types":   true,
	"/api/v1/data/subnets":           true,
	"/api/v1/data/l1s":               true,
	"/api/v1/data/chains":            true,
	"/api/v1/data/validators":        true,
	"/api/v1/metrics/fees":           true,
	"/api/v1/metrics/indexer/status": true,
}

// normalizePath replaces dynamic path segments with placeholders
// to keep Prometheus label cardinality bounded.
// Unknown paths are bucketed into "/unknown" to prevent scanner bots
// from creating unbounded time series.
func normalizePath(path string) string {
	// Check exact matches first
	if knownExactPaths[path] {
		return path
	}

	parts := splitPath(path)
	if len(parts) < 2 {
		if path == "/" {
			return "/"
		}
		return "/unknown"
	}

	matched := false

	// /api/v1/data/evm/{chainId}/...
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "evm" {
		parts[4] = ":chainId"
		matched = true
		if len(parts) >= 7 && parts[5] == "blocks" {
			parts[6] = ":number"
		}
		if len(parts) >= 7 && parts[5] == "txs" {
			parts[6] = ":hash"
		}
		if len(parts) >= 6 && parts[5] == "address" {
			if len(parts) >= 7 {
				parts[6] = ":address"
			}
		}
	}

	// /api/v1/data/pchain/txs/{txId}
	if len(parts) >= 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "pchain" && parts[4] == "txs" {
		parts[5] = ":txId"
		matched = true
	}

	// /api/v1/data/subnets/{subnetId}
	if len(parts) >= 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "subnets" {
		parts[5] = ":subnetId"
		matched = true
	}

	// /api/v1/data/validators/{id}
	if len(parts) >= 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "validators" {
		parts[5] = ":id"
		matched = true
	}

	// /api/v1/metrics/evm/{chainId}/...
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "metrics" && parts[3] == "evm" {
		parts[4] = ":chainId"
		matched = true
		if len(parts) >= 7 && parts[5] == "timeseries" {
			parts[6] = ":metric"
		}
	}

	// /ws/blocks/{chainId}
	if len(parts) >= 3 && parts[0] == "ws" && parts[1] == "blocks" {
		parts[2] = ":chainId"
		matched = true
	}

	if !matched {
		return "/unknown"
	}

	return "/" + joinPath(parts)
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range splitString(path, '/') {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitString(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func joinPath(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "/"
		}
		result += p
	}
	return result
}

// MetricsAuthMiddleware protects Prometheus metrics with a bearer token.
func MetricsAuthMiddleware(token string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				writeNotFoundError(w, "Metrics")
				return
			}

			got := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(got) <= len(prefix) || got[:len(prefix)] != prefix {
				writeAPIError(w, http.StatusUnauthorized, ErrValidationFailed, "Unauthorized")
				return
			}
			got = got[len(prefix):]

			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeAPIError(w, http.StatusUnauthorized, ErrValidationFailed, "Unauthorized")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// PrometheusHandler returns the Prometheus metrics HTTP handler.
func PrometheusHandler() http.Handler {
	return promhttp.Handler()
}
