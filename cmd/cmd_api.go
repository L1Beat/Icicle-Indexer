package cmd

import (
	"context"
	"fmt"
	"icicle/pkg/api"
	"icicle/pkg/chwrapper"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// APIOptions holds configuration for the API server
type APIOptions struct {
	Port                     int
	RateLimitPerMin          int
	RateLimitBurst           int
	TrustedProxies           []string
	MetricsToken             string
	WSMaxConnections         int
	WSMaxConnectionsPerIP    int
	WSMaxConnectionsPerChain int
}

// DefaultAPIOptions returns sensible defaults
func DefaultAPIOptions() APIOptions {
	return APIOptions{
		Port:                     8080,
		RateLimitPerMin:          60,
		RateLimitBurst:           10,
		MetricsToken:             os.Getenv("ICICLE_METRICS_TOKEN"),
		WSMaxConnections:         1000,
		WSMaxConnectionsPerIP:    20,
		WSMaxConnectionsPerChain: 250,
	}
}

func RunAPI(ctx context.Context, opts APIOptions) {
	slog.Info("Starting API server")

	// Connect to ClickHouse
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	// Build API config
	cfg := api.Config{
		RateLimit: api.RateLimitConfig{
			RequestsPerMinute: opts.RateLimitPerMin,
			BurstSize:         opts.RateLimitBurst,
			CleanupInterval:   5 * time.Minute,
			TrustedProxies:    opts.TrustedProxies,
		},
		Metrics: api.MetricsConfig{
			Token: opts.MetricsToken,
		},
		WebSocket: api.WebSocketConfig{
			MaxConnections:         opts.WSMaxConnections,
			MaxConnectionsPerIP:    opts.WSMaxConnectionsPerIP,
			MaxConnectionsPerChain: opts.WSMaxConnectionsPerChain,
		},
	}

	// Create API server
	server := api.NewServer(conn, cfg)
	defer server.Stop()

	addr := fmt.Sprintf(":%d", opts.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		slog.Info("API server listening", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("API server failed: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	slog.Info("Shutting down API server")

	// Give in-flight requests 10 seconds to complete
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("API server forced shutdown", "error", err)
	} else {
		slog.Info("API server stopped gracefully")
	}
}

func ParseCSVFlag(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
