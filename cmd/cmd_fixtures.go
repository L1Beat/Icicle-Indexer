package cmd

import (
	"context"
	"log"
	"log/slog"
	"os"

	"icicle/pkg/chwrapper"
	"icicle/pkg/stealtime"
)

// FixturesOptions configures the per-venue fork-test fixture extraction.
type FixturesOptions struct {
	ChainID     uint32
	ArchiveRPC  string
	FallbackRPC string
	Label       string
	Protocol    string
}

// DefaultFixturesOptions returns fixture-extraction defaults.
func DefaultFixturesOptions() FixturesOptions {
	return FixturesOptions{
		ChainID:     43114,
		ArchiveRPC:  os.Getenv("ICICLE_ARCHIVE_RPC"),
		FallbackRPC: os.Getenv("ICICLE_FALLBACK_RPC"),
		Label:       "real_oct",
	}
}

// RunFixtures extracts one fork-test fixture per venue from replay_results. Read-only.
func RunFixtures(ctx context.Context, opts FixturesOptions) {
	slog.Info("Extracting fork-test fixtures", "chain_id", opts.ChainID, "label", opts.Label)

	if opts.ArchiveRPC == "" {
		log.Fatalf("fixtures: archive RPC is required (set --archive-rpc or ICICLE_ARCHIVE_RPC)")
	}

	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	cfg := stealtime.FixturesConfig{
		ChainID:     opts.ChainID,
		ArchiveRPC:  opts.ArchiveRPC,
		FallbackRPC: opts.FallbackRPC,
		Label:       opts.Label,
		Protocol:    opts.Protocol,
	}

	if err := stealtime.Fixtures(ctx, conn, cfg); err != nil {
		log.Fatalf("fixtures: failed: %v", err)
	}
}
