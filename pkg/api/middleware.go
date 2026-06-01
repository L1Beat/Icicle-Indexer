package api

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// Middleware is a function that wraps an http.Handler
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in order (first middleware is outermost)
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// CORSMiddleware handles CORS preflight and headers
func CORSMiddleware(allowedOrigins string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// LoggingMiddleware logs request method, path, status, and duration
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		slog.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration", duration,
			"remote", r.RemoteAddr,
			"xff", r.Header.Get("X-Forwarded-For"),
		)
	})
}

// loggingResponseWriter wraps http.ResponseWriter to capture status code
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker for WebSocket support
func (lrw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// RecoveryMiddleware catches panics in handlers, logs the stack trace, and returns 500
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("PANIC RECOVERED", "error", err, "stack", string(debug.Stack()))
				http.Error(w, `{"error":"INTERNAL_ERROR"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cacheControlValue is sent on cacheable GET responses. The indexer updates
// these metrics on the order of minutes, so a short max-age plus a longer
// stale-while-revalidate window lets a CDN serve cold loads from the edge and
// refresh in the background without ever blocking a user.
const cacheControlValue = "public, max-age=60, stale-while-revalidate=300"

// isCacheablePath reports whether a request path serves public, edge-cacheable
// data. Live/operational endpoints (indexer status, health, websockets) are
// excluded so they always reflect current state.
func isCacheablePath(path string) bool {
	if strings.HasPrefix(path, "/api/v1/data/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/metrics/") && !strings.HasPrefix(path, "/api/v1/metrics/indexer/") {
		return true
	}
	return false
}

// CacheControlMiddleware sets Cache-Control on successful (2xx) GET responses to
// public data endpoints so CDNs and browsers can cache them. Errors and
// non-cacheable paths are left untouched.
func CacheControlMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheable := (r.Method == http.MethodGet || r.Method == http.MethodHead) && isCacheablePath(r.URL.Path)
		if !cacheable {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&cacheResponseWriter{ResponseWriter: w}, r)
	})
}

// cacheResponseWriter adds Cache-Control just before the status line is written,
// but only for 2xx responses, so error responses are never cached.
type cacheResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (c *cacheResponseWriter) WriteHeader(code int) {
	if !c.wroteHeader {
		c.wroteHeader = true
		if code >= 200 && code < 300 {
			c.ResponseWriter.Header().Set("Cache-Control", cacheControlValue)
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *cacheResponseWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

// TimeoutMiddleware adds a server-side timeout to each request's context
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
