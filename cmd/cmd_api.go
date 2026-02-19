package cmd

import (
	"context"
	"fmt"
	"icicle/pkg/api"
	"icicle/pkg/chwrapper"
	"log"
	"log/slog"
	"net/http"
	"time"
)

// APIOptions holds configuration for the API server
type APIOptions struct {
	Port            int
	RateLimitPerMin int
	RateLimitBurst  int
}

// DefaultAPIOptions returns sensible defaults
func DefaultAPIOptions() APIOptions {
	return APIOptions{
		Port:            8080,
		RateLimitPerMin: 60,
		RateLimitBurst:  10,
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
		},
	}

	// Create API server
	server := api.NewServer(conn, cfg)
	defer server.Stop()

	addr := fmt.Sprintf(":%d", opts.Port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: server,
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
