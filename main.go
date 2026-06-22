package main

import (
	"context"
	"icicle/cmd"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

func main() {
	_ = godotenv.Load()

	// Configure slog as the default logger
	// Use JSON in production (LOG_FORMAT=json), text otherwise
	if os.Getenv("LOG_FORMAT") == "json" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	// Ignore SIGPIPE - common in network servers when clients disconnect
	signal.Ignore(syscall.SIGPIPE)

	// Create a cancellable root context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Catch fatal signals and cancel context for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		sig := <-sigChan
		slog.Info("Signal received, initiating graceful shutdown", "signal", sig)
		cancel()
	}()

	root := &cobra.Command{Use: "clickhouse-ingest"}

	wipeCmd := &cobra.Command{
		Use:   "wipe",
		Short: "Drop calculated tables (keeps raw_* and sync_watermark)",
		Run: func(command *cobra.Command, args []string) {
			all, _ := command.Flags().GetBool("all")
			chainID, _ := command.Flags().GetUint32("chain")
			pchain, _ := command.Flags().GetBool("pchain")
			cmd.RunWipe(all, chainID, pchain)
		},
	}
	wipeCmd.Flags().Bool("all", false, "Drop all tables including raw_* tables")
	wipeCmd.Flags().Uint32("chain", 0, "Wipe data for a specific chain ID only")
	wipeCmd.Flags().Bool("pchain", false, "Wipe P-chain calculated tables (validator history, fee stats, subnets)")

	ingestCmd := &cobra.Command{
		Use:   "ingest",
		Short: "Start the continuous ingestion process",
		Run: func(command *cobra.Command, args []string) {
			fast, _ := command.Flags().GetBool("fast")
			cmd.RunIngest(ctx, fast)
		},
	}
	ingestCmd.Flags().Bool("fast", false, "Skip all indexers (incremental and metrics)")

	apiCmd := &cobra.Command{
		Use:   "api",
		Short: "Start the HTTP API server",
		Run: func(command *cobra.Command, args []string) {
			opts := cmd.DefaultAPIOptions()
			opts.Port, _ = command.Flags().GetInt("port")
			opts.RateLimitPerMin, _ = command.Flags().GetInt("rate-limit")
			opts.RateLimitBurst, _ = command.Flags().GetInt("burst")
			trustedProxies, _ := command.Flags().GetString("trusted-proxies")
			opts.TrustedProxies = cmd.ParseCSVFlag(trustedProxies)
			opts.MetricsToken, _ = command.Flags().GetString("metrics-token")
			opts.WSMaxConnections, _ = command.Flags().GetInt("ws-max-connections")
			opts.WSMaxConnectionsPerIP, _ = command.Flags().GetInt("ws-max-connections-per-ip")
			opts.WSMaxConnectionsPerChain, _ = command.Flags().GetInt("ws-max-connections-per-chain")
			cmd.RunAPI(ctx, opts)
		},
	}
	apiCmd.Flags().Int("port", 8080, "Port to listen on")
	apiCmd.Flags().Int("rate-limit", 60, "Rate limit requests per minute per IP")
	apiCmd.Flags().Int("burst", 10, "Rate limit burst size")
	apiCmd.Flags().String("trusted-proxies", "", "Comma-separated trusted proxy IPs/CIDRs for X-Forwarded-For/X-Real-IP")
	apiCmd.Flags().String("metrics-token", os.Getenv("ICICLE_METRICS_TOKEN"), "Bearer token required to enable and access /metrics")
	apiCmd.Flags().Int("ws-max-connections", 1000, "Maximum concurrent WebSocket connections")
	apiCmd.Flags().Int("ws-max-connections-per-ip", 20, "Maximum concurrent WebSocket connections per client IP")
	apiCmd.Flags().Int("ws-max-connections-per-chain", 250, "Maximum concurrent WebSocket connections per chain")

	lendingCmd := &cobra.Command{
		Use:   "lending",
		Short: "Start the lending liquidation-risk engine (Aave v3 + Benqi)",
		Run: func(command *cobra.Command, args []string) {
			opts := cmd.DefaultLendingOptions()
			cid, _ := command.Flags().GetUint32("chain")
			opts.ChainID = cid
			if v, _ := command.Flags().GetString("archive-rpc"); v != "" {
				opts.ArchiveRPC = v
			}
			if v, _ := command.Flags().GetString("fallback-rpc"); v != "" {
				opts.FallbackRPC = v
			}
			opts.AaveProvider, _ = command.Flags().GetString("aave-provider")
			opts.BenqiComptroller, _ = command.Flags().GetString("benqi-comptroller")
			opts.DiscoveryBatch, _ = command.Flags().GetUint64("discovery-batch")
			opts.ParamsRefreshHours, _ = command.Flags().GetInt("params-refresh-hours")
			opts.MetricsPort, _ = command.Flags().GetInt("metrics-port")
			cmd.RunLending(ctx, opts)
		},
	}
	lendingCmd.Flags().Uint32("chain", 43114, "EVM chain ID to track")
	lendingCmd.Flags().String("archive-rpc", os.Getenv("ICICLE_ARCHIVE_RPC"), "Archive node RPC URL (required)")
	lendingCmd.Flags().String("fallback-rpc", os.Getenv("ICICLE_FALLBACK_RPC"), "Optional public RPC fallback")
	lendingCmd.Flags().String("aave-provider", "", "Aave v3 PoolAddressesProvider (default: canonical Avalanche)")
	lendingCmd.Flags().String("benqi-comptroller", "", "Benqi Comptroller (default: canonical Avalanche)")
	lendingCmd.Flags().Uint64("discovery-batch", 5000, "Blocks per discovery batch")
	lendingCmd.Flags().Int("params-refresh-hours", 6, "Hours between protocol parameter refreshes")
	lendingCmd.Flags().Int("metrics-port", 9092, "Port for the Prometheus /metrics endpoint (0 to disable)")

	kmeasureCmd := &cobra.Command{
		Use:   "kmeasure",
		Short: "Read-only Stage 1 K-measurement: profitable liquidations after costs",
		Run: func(command *cobra.Command, args []string) {
			opts := cmd.DefaultKMeasureOptions()
			cid, _ := command.Flags().GetUint32("chain")
			opts.ChainID = cid
			if v, _ := command.Flags().GetString("feed-url"); v != "" {
				opts.FeedBaseURL = v
			}
			if v, _ := command.Flags().GetString("archive-rpc"); v != "" {
				opts.ArchiveRPC = v
			}
			if v, _ := command.Flags().GetString("fallback-rpc"); v != "" {
				opts.FallbackRPC = v
			}
			opts.IntervalMinutes, _ = command.Flags().GetInt("interval-min")
			opts.GasUnits, _ = command.Flags().GetUint64("gas-units")
			opts.MinProfitUSD, _ = command.Flags().GetFloat64("min-profit-usd")
			opts.FlashFeeBps, _ = command.Flags().GetUint64("flash-fee-bps")
			opts.MinDebtBase, _ = command.Flags().GetString("min-debt-base")
			opts.Persist, _ = command.Flags().GetBool("persist")
			cmd.RunKMeasure(ctx, opts)
		},
	}
	kmeasureCmd.Flags().Uint32("chain", 43114, "EVM chain ID")
	kmeasureCmd.Flags().String("feed-url", "https://api.l1beat.io/api/v1/data/evm/43114/lending", "Lending feed base URL")
	kmeasureCmd.Flags().String("archive-rpc", os.Getenv("ICICLE_ARCHIVE_RPC"), "Archive node RPC URL (required)")
	kmeasureCmd.Flags().String("fallback-rpc", os.Getenv("ICICLE_FALLBACK_RPC"), "Optional public RPC fallback")
	kmeasureCmd.Flags().Int("interval-min", 0, "Repeat every N minutes (0 = one-shot)")
	kmeasureCmd.Flags().Uint64("gas-units", 700000, "Estimated full-bundle gas units")
	kmeasureCmd.Flags().Float64("min-profit-usd", 25, "Minimum net profit in USD to count as profitable")
	kmeasureCmd.Flags().Uint64("flash-fee-bps", 5, "Flash-loan fee in bps, fallback if on-chain read fails")
	kmeasureCmd.Flags().String("min-debt-base", "", "Optional feed-side dust pre-cut, 1e18 USD (default off)")
	kmeasureCmd.Flags().Bool("persist", true, "Persist each run summary to kmeasure_runs")

	root.AddCommand(
		ingestCmd,
		apiCmd,
		lendingCmd,
		kmeasureCmd,
		&cobra.Command{
			Use:   "cache",
			Short: "Fill RPC cache at max speed (no ClickHouse)",
			Run:   func(command *cobra.Command, args []string) { cmd.RunCache() },
		},
		&cobra.Command{
			Use:   "size",
			Short: "Show ClickHouse table sizes and disk usage",
			Run:   func(command *cobra.Command, args []string) { cmd.RunSize() },
		},
		&cobra.Command{
			Use:   "duplicates",
			Short: "Check for duplicate records in raw tables",
			Run:   func(command *cobra.Command, args []string) { cmd.RunDuplicates() },
		},
		wipeCmd,
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
