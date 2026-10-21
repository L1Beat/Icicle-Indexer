package cmd

import (
	"context"
	"log"
	"log/slog"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/enrich"
)

// EnrichBackfillOptions configures the one-shot enrichment backfill.
type EnrichBackfillOptions struct {
	ChainID      uint32
	Days         int    // trailing window of history to cover
	MaxContracts int    // Routescan lookups to perform (most active contracts first)
	MaxSelectors int    // distinct selectors to resolve
	RPCURL       string // our node; "" skips eth_getCode / EIP-1967 reads
	SkipLabels   bool
	SkipSigs     bool
}

// RunEnrichBackfill seeds the bundled lists, resolves every selector used in
// the window via OpenChain, and labels the most active contracts via
// Routescan. Idempotent: rows are ReplacingMergeTree and already-known
// selectors/addresses are skipped, so it can be re-run or interrupted freely.
// Runs alongside the live indexer without stopping it.
func RunEnrichBackfill(ctx context.Context, opts EnrichBackfillOptions) {
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()
	if err := chwrapper.CreateTables(conn); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	e := enrich.NewEnricher(opts.ChainID, conn, opts.RPCURL)
	if err := e.SeedAll(ctx); err != nil {
		log.Fatalf("Seed failed: %v", err)
	}
	slog.Info("enrich-backfill: seeded bundled signatures and curated labels", "chain_id", opts.ChainID)

	latest, err := enrich.LatestBlock(ctx, conn, opts.ChainID)
	if err != nil || latest == 0 {
		log.Fatalf("No indexed blocks for chain %d (%v)", opts.ChainID, err)
	}
	from, err := enrich.BlockAtOrAfter(ctx, conn, opts.ChainID, time.Now().Add(-time.Duration(opts.Days)*24*time.Hour))
	if err != nil || from == 0 {
		log.Fatalf("Could not find start block for the last %d days (%v)", opts.Days, err)
	}
	slog.Info("enrich-backfill: window", "chain_id", opts.ChainID, "from_block", from, "to_block", latest, "days", opts.Days)

	if !opts.SkipSigs {
		start := time.Now()
		looked, resolved, err := e.ResolveSignatures(ctx, from, latest, opts.MaxSelectors)
		if err != nil {
			slog.Error("enrich-backfill: signature resolution stopped", "error", err, "looked_up", looked, "resolved", resolved)
		} else {
			slog.Info("enrich-backfill: signatures done", "looked_up", looked, "resolved", resolved, "took", time.Since(start).Round(time.Second))
		}
	}

	if !opts.SkipLabels {
		start := time.Now()
		slog.Info("enrich-backfill: labelling contracts (throttled to ~1.5 req/s on Routescan)", "max", opts.MaxContracts)
		processed, verified, err := e.FetchLabels(ctx, from, latest, opts.MaxContracts)
		if err != nil {
			slog.Error("enrich-backfill: label fetch stopped", "error", err, "processed", processed, "verified", verified)
		} else {
			slog.Info("enrich-backfill: labels done", "processed", processed, "verified", verified, "took", time.Since(start).Round(time.Second))
		}
	}
}
