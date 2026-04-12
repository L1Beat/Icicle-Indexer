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

	root.AddCommand(
		ingestCmd,
		apiCmd,
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
