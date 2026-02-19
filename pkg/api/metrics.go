package api

import (
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
		// Skip metrics endpoint itself to avoid recursion
		if r.URL.Path == "/metrics" {
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

// normalizePath replaces dynamic path segments with placeholders
// to keep Prometheus label cardinality bounded.
func normalizePath(path string) string {
	// Map known route patterns to normalized forms
	// This is simpler and safer than regex-based normalization
	parts := splitPath(path)
	if len(parts) < 2 {
		return path
	}

	// /api/v1/data/evm/{chainId}/...
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "evm" {
		parts[4] = ":chainId"
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
	}

	// /api/v1/data/subnets/{subnetId}
	if len(parts) >= 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "subnets" {
		parts[5] = ":subnetId"
	}

	// /api/v1/data/validators/{id}
	if len(parts) >= 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "data" && parts[3] == "validators" {
		parts[5] = ":id"
	}

	// /api/v1/metrics/evm/{chainId}/...
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "metrics" && parts[3] == "evm" {
		parts[4] = ":chainId"
		if len(parts) >= 7 && parts[5] == "timeseries" {
			parts[6] = ":metric"
		}
	}

	// /ws/blocks/{chainId}
	if len(parts) >= 3 && parts[0] == "ws" && parts[1] == "blocks" {
		parts[2] = ":chainId"
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

// PrometheusHandler returns the Prometheus metrics HTTP handler.
func PrometheusHandler() http.Handler {
	return promhttp.Handler()
}
