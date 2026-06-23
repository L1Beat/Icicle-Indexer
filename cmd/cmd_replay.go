package cmd

import (
	"context"
	"log"
	"log/slog"
	"math/big"
	"os"

	"icicle/pkg/chwrapper"
	"icicle/pkg/stealtime"
)

// ReplayOptions configures the crash-day capture replay.
type ReplayOptions struct {
	ChainID      uint32
	ArchiveRPC   string
	FallbackRPC  string
	TopDays      int
	MinSizeUSD   float64
	GasUnits     uint64
	MinProfitUSD float64
	Label        string
	ProbeVenues  bool
	ProbeBlock   uint64
}

// DefaultReplayOptions returns replay defaults.
func DefaultReplayOptions() ReplayOptions {
	return ReplayOptions{
		ChainID:      43114,
		ArchiveRPC:   os.Getenv("ICICLE_ARCHIVE_RPC"),
		FallbackRPC:  os.Getenv("ICICLE_FALLBACK_RPC"),
		TopDays:      12,
		MinSizeUSD:   1000,
		GasUnits:     700000,
		MinProfitUSD: 5,
	}
}

// RunReplay runs the crash-day capture replay over stealtime_results. Read-only.
func RunReplay(ctx context.Context, opts ReplayOptions) {
	slog.Info("Starting crash-day capture replay", "chain_id", opts.ChainID, "top_days", opts.TopDays)

	if opts.ArchiveRPC == "" {
		log.Fatalf("replay: archive RPC is required (set --archive-rpc or ICICLE_ARCHIVE_RPC)")
	}

	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	cfg := stealtime.ReplayConfig{
		ChainID:          opts.ChainID,
		ArchiveRPC:       opts.ArchiveRPC,
		FallbackRPC:      opts.FallbackRPC,
		TopDays:          opts.TopDays,
		MinSizeUSD1e18:   usdToWei(opts.MinSizeUSD),
		GasUnits:         opts.GasUnits,
		MinProfitUSD1e18: usdToWei(opts.MinProfitUSD),
		Label:            opts.Label,
		ProbeVenues:      opts.ProbeVenues,
		ProbeBlock:       opts.ProbeBlock,
	}

	if err := stealtime.Replay(ctx, conn, cfg); err != nil {
		log.Fatalf("replay: failed: %v", err)
	}
}

func usdToWei(usd float64) *big.Int {
	out := new(big.Int)
	new(big.Float).Mul(big.NewFloat(usd), new(big.Float).SetInt(bigPow10(18))).Int(out)
	return out
}
