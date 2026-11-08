package pchainsyncer

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"icicle/pkg/bridges"
	"icicle/pkg/dexvolume"
	"icicle/pkg/icm"
	"icicle/pkg/perps"
	"icicle/pkg/restaking"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// CChainSnapshotter refreshes the C-Chain contract-read and rollup tables on
// its own interval, apart from the validator sync, which is heavy and usually
// runs much less often. Each step is independent: one failing does not skip
// the others.
type CChainSnapshotter struct {
	conn     clickhouse.Conn
	nodeURL  string // avalanchego base URL (the P-Chain fetcher's), C-Chain RPC is derived
	interval time.Duration
}

// NewCChainSnapshotter builds a snapshotter. interval defaults to 5 minutes.
func NewCChainSnapshotter(conn clickhouse.Conn, nodeURL string, interval time.Duration) *CChainSnapshotter {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &CChainSnapshotter{conn: conn, nodeURL: nodeURL, interval: interval}
}

// Start runs one cycle now and then every interval until ctx is cancelled. A
// cycle that overruns delays the next one rather than overlapping it.
func (s *CChainSnapshotter) Start(ctx context.Context) {
	slog.Info("Starting C-Chain snapshotter", "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.runOnce(ctx)
		select {
		case <-ctx.Done():
			slog.Info("Stopping C-Chain snapshotter")
			return
		case <-ticker.C:
		}
	}
}

func (s *CChainSnapshotter) runOnce(ctx context.Context) {
	start := time.Now()
	// Restaking collateral (Suzaku vaults securing L1s).
	if err := SnapshotRestaking(ctx, s.conn, s.nodeURL); err != nil {
		slog.Warn("Failed to snapshot restaking", "error", err)
	}
	// Lending markets (Aave V3, Aave V4, BENQI) and their E-Mode/spoke configs.
	if err := SnapshotLendingMarkets(ctx, s.conn, s.nodeURL); err != nil {
		slog.Warn("Failed to snapshot lending markets", "error", err)
	}
	// When each market's and V4 spoke entry's LTV took its current value.
	if err := TrackLendingLTVChanges(ctx, s.conn, s.nodeURL); err != nil {
		slog.Warn("Failed to date lending LTV changes", "error", err)
	}
	if err := BackfillLendingHistory(ctx, s.conn, s.nodeURL); err != nil {
		slog.Warn("Failed to backfill lending history", "error", err)
	}
	// DEX pools and complete UTC days of swaps.
	if err := dexvolume.Sync(ctx, s.conn); err != nil {
		slog.Warn("Failed to roll up DEX volume", "error", err)
	}
	// Bridge transfers into and out of the C-Chain.
	base := strings.TrimSuffix(strings.TrimRight(s.nodeURL, "/"), "/ext/bc/P")
	caller := restaking.RPCCaller{URL: base + "/ext/bc/C/rpc", Client: liquidRPCClient}
	if err := bridges.Sync(ctx, s.conn, caller); err != nil {
		slog.Warn("Failed to sync bridge transfers", "error", err)
	}
	// Perpetuals (GMX V2, GMX V1 history).
	if err := perps.Sync(ctx, s.conn); err != nil {
		slog.Warn("Failed to sync perps", "error", err)
	}
	// ICM (Teleporter) messages sent from and received on the C-Chain.
	if err := icm.Sync(ctx, s.conn, caller); err != nil {
		slog.Warn("Failed to sync ICM messages", "error", err)
	}
	slog.Info("C-Chain snapshot cycle completed", "duration", time.Since(start))
}
