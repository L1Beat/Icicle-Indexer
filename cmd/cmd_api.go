package cmd

import (
	"icicle/pkg/api"
	"icicle/pkg/chwrapper"
	"log"
	"time"
)

// APIOptions holds configuration for the API server
type APIOptions struct {
	Port              int
	RateLimitPerMin   int
	RateLimitBurst    int
}

// DefaultAPIOptions returns sensible defaults
func DefaultAPIOptions() APIOptions {
	return APIOptions{
		Port:            8080,
		RateLimitPerMin: 60,
		RateLimitBurst:  10,
	}
}

func RunAPI(opts APIOptions) {
	log.Printf("Starting API server...")

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

	// Create and start API server
	server := api.NewServer(conn, cfg)
	if err := server.Start(opts.Port); err != nil {
		log.Fatalf("API server failed: %v", err)
	}
}
