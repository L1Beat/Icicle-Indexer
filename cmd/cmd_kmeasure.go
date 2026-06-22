package cmd

import (
	"context"
	"log"
	"log/slog"
	"math/big"
	"os"

	"icicle/pkg/chwrapper"
	"icicle/pkg/kmeasure"
)

// KMeasureOptions configures the read-only K-measurement runner.
type KMeasureOptions struct {
	ChainID         uint32
	FeedBaseURL     string
	ArchiveRPC      string
	FallbackRPC     string
	IntervalMinutes int
	GasUnits        uint64
	MinProfitUSD    float64 // dollars, converted to 1e18
	FlashFeeBps     uint64
	MinDebtBase     string
	Persist         bool
}

// DefaultKMeasureOptions returns runner defaults for Avalanche C-Chain.
func DefaultKMeasureOptions() KMeasureOptions {
	return KMeasureOptions{
		ChainID:         43114,
		FeedBaseURL:     "https://api.l1beat.io/api/v1/data/evm/43114/lending",
		ArchiveRPC:      os.Getenv("ICICLE_ARCHIVE_RPC"),
		FallbackRPC:     os.Getenv("ICICLE_FALLBACK_RPC"),
		IntervalMinutes: 0,
		GasUnits:        700000,
		MinProfitUSD:    25,
		FlashFeeBps:     5,
		MinDebtBase:     "",
		Persist:         true,
	}
}

// RunKMeasure wires and runs the K-measurement diagnostic. Read-only: no keys, no
// submission.
func RunKMeasure(ctx context.Context, opts KMeasureOptions) {
	slog.Info("Starting K-measurement runner", "chain_id", opts.ChainID, "interval_min", opts.IntervalMinutes)

	if opts.ArchiveRPC == "" {
		log.Fatalf("kmeasure: archive RPC is required (set --archive-rpc or ICICLE_ARCHIVE_RPC)")
	}

	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	// Convert the dollar threshold to 1e18, the feed's USD scale.
	minProfit := new(big.Int)
	new(big.Float).Mul(big.NewFloat(opts.MinProfitUSD), new(big.Float).SetInt(bigPow10(18))).Int(minProfit)

	cfg := kmeasure.Config{
		ChainID:          opts.ChainID,
		FeedBaseURL:      opts.FeedBaseURL,
		ArchiveRPC:       opts.ArchiveRPC,
		FallbackRPC:      opts.FallbackRPC,
		IntervalMinutes:  opts.IntervalMinutes,
		GasUnits:         opts.GasUnits,
		MinProfitUSD1e18: minProfit,
		FlashFeeBps:      opts.FlashFeeBps,
		MinDebtBase:      opts.MinDebtBase,
		Persist:          opts.Persist,
	}

	runner, err := kmeasure.NewRunner(ctx, conn, cfg)
	if err != nil {
		log.Fatalf("kmeasure: setup failed: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		log.Fatalf("kmeasure: run failed: %v", err)
	}
}

func bigPow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}
