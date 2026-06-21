package cmd

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"icicle/pkg/chwrapper"
	"icicle/pkg/lending"
	"icicle/pkg/lending/aave"
	"icicle/pkg/lending/benqi"
)

// LendingOptions configures the liquidation-risk engine service.
type LendingOptions struct {
	ChainID            uint32
	ArchiveRPC         string
	FallbackRPC        string
	AaveProvider       string
	BenqiComptroller   string
	DiscoveryBatch     uint64
	ParamsRefreshHours int
	MetricsPort        int
}

// DefaultLendingOptions returns service defaults for Avalanche C-Chain.
func DefaultLendingOptions() LendingOptions {
	return LendingOptions{
		ChainID:            43114,
		ArchiveRPC:         os.Getenv("ICICLE_ARCHIVE_RPC"),
		FallbackRPC:        os.Getenv("ICICLE_FALLBACK_RPC"),
		DiscoveryBatch:     5000,
		ParamsRefreshHours: 6,
		MetricsPort:        9092,
	}
}

// RunLending starts the lending liquidation-risk engine and blocks until the
// context is cancelled.
func RunLending(ctx context.Context, opts LendingOptions) {
	slog.Info("Starting lending engine", "chain_id", opts.ChainID)

	if opts.ArchiveRPC == "" {
		log.Fatalf("lending: archive RPC is required (set --archive-rpc or ICICLE_ARCHIVE_RPC)")
	}

	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	cfg := lending.DefaultConfig()
	cfg.ChainID = opts.ChainID
	cfg.ArchiveRPC = opts.ArchiveRPC
	cfg.FallbackRPC = opts.FallbackRPC
	cfg.DiscoveryBatch = opts.DiscoveryBatch
	cfg.ParamsRefreshInterval = time.Duration(opts.ParamsRefreshHours) * time.Hour

	adapters := []lending.Adapter{
		aave.New(opts.AaveProvider),
		benqi.New(opts.BenqiComptroller),
	}

	engine, err := lending.NewEngine(conn, adapters, cfg)
	if err != nil {
		log.Fatalf("lending: failed to create engine: %v", err)
	}

	if err := engine.Bootstrap(ctx, adapters); err != nil {
		log.Fatalf("lending: bootstrap failed: %v", err)
	}

	if opts.MetricsPort > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		metricsServer := &http.Server{
			Addr:              fmt.Sprintf(":%d", opts.MetricsPort),
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			slog.Info("lending: metrics listening", "addr", metricsServer.Addr)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("lending: metrics server failed", "error", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsServer.Shutdown(shutdownCtx)
		}()
	}

	engine.Run(ctx)
}
