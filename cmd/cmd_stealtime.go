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

// StealtimeOptions configures the offline steal-time backtest.
type StealtimeOptions struct {
	ChainID           uint32
	ArchiveRPC        string
	FallbackRPC       string
	FromBlock         uint64
	ToBlock           uint64
	MaxLookbackBlocks uint64
	SampleStride      uint64
	MinDebtUSD        float64
	MinProfitUSD      float64
	GasUnits          uint64
	TopN              int
	Persist           bool
	Debug             bool
}

// DefaultStealtimeOptions returns backtest defaults for Avalanche C-Chain.
func DefaultStealtimeOptions() StealtimeOptions {
	return StealtimeOptions{
		ChainID:           43114,
		ArchiveRPC:        os.Getenv("ICICLE_ARCHIVE_RPC"),
		FallbackRPC:       os.Getenv("ICICLE_FALLBACK_RPC"),
		MaxLookbackBlocks: 43200, // about one day at 2s blocks
		SampleStride:      600,   // coarse backward step before binary search
		MinDebtUSD:        50,    // skip sub-threshold dust liquidations cheaply
		MinProfitUSD:      25,
		GasUnits:          700000,
		TopN:              10,
		Persist:           true,
	}
}

// RunStealtime runs the offline, read-only backtest. No keys, no submission.
func RunStealtime(ctx context.Context, opts StealtimeOptions) {
	slog.Info("Starting steal-time backtest", "chain_id", opts.ChainID, "from", opts.FromBlock, "to", opts.ToBlock)

	if opts.ArchiveRPC == "" {
		log.Fatalf("stealtime: archive RPC is required (set --archive-rpc or ICICLE_ARCHIVE_RPC)")
	}
	if opts.ToBlock == 0 || opts.ToBlock < opts.FromBlock {
		log.Fatalf("stealtime: set --from-block and --to-block (to >= from)")
	}

	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	minProfit := new(big.Int)
	new(big.Float).Mul(big.NewFloat(opts.MinProfitUSD), new(big.Float).SetInt(bigPow10(18))).Int(minProfit)
	minDebt := new(big.Int)
	new(big.Float).Mul(big.NewFloat(opts.MinDebtUSD), new(big.Float).SetInt(bigPow10(18))).Int(minDebt)

	cfg := stealtime.Config{
		ChainID:           opts.ChainID,
		ArchiveRPC:        opts.ArchiveRPC,
		FallbackRPC:       opts.FallbackRPC,
		FromBlock:         opts.FromBlock,
		ToBlock:           opts.ToBlock,
		MaxLookbackBlocks: opts.MaxLookbackBlocks,
		SampleStride:      opts.SampleStride,
		MinDebtUSD1e18:    minDebt,
		MinProfitUSD1e18:  minProfit,
		GasUnits:          opts.GasUnits,
		TopN:              opts.TopN,
		Persist:           opts.Persist,
		Debug:             opts.Debug,
		DebugN:            25,
	}

	if err := stealtime.Run(ctx, conn, cfg); err != nil {
		log.Fatalf("stealtime: run failed: %v", err)
	}
}
