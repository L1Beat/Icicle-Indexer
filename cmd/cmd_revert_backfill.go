package cmd

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"icicle/pkg/chwrapper"
	"icicle/pkg/evmrpc"
	"icicle/pkg/evmsyncer"
)

// RevertBackfillOptions configures the one-time correction of native balances
// for history ingested before raw_traces.reverted existed.
type RevertBackfillOptions struct {
	ChainID        uint32
	From, To       uint32 // block range [From, To)
	RPC            string
	TraceChunk     uint32 // blocks re-traced per fetch and insert
	Concurrency    int    // parallel RPC requests, kept low for the shared node
	DebugBatch     int    // trace calls per HTTP request
	Recompute      bool   // phase 2: rebuild native_balance_changes from the recorded reverts
	RecomputeChunk uint32 // blocks per recompute step
	SQLDir         string // where sql/evm_incremental/native_balances.sql lives
}

// RunRevertBackfill runs one of two resumable phases.
//
// Phase 1 (default) re-traces [From, To) from the archive node and records every
// value-moving frame that was rolled back in raw_trace_reverts.
//
// Phase 2 (--recompute, only after phase 1 finished) rebuilds
// native_balance_changes for the same range with the indexer's own SQL plus an
// anti-join on raw_trace_reverts. It walks the range in steps aligned to the
// existing rows' block ranges and, per step, deletes the old rows and inserts
// the corrected ones, so a step is briefly missing rather than double counted.
//
// Both phases save progress after each chunk and run alongside the live indexer,
// which only writes blocks above To.
func RunRevertBackfill(ctx context.Context, opts RevertBackfillOptions) {
	if opts.To <= opts.From {
		log.Fatalf("revert-backfill: --to (%d) must be above --from (%d)", opts.To, opts.From)
	}
	if os.Getenv("CH_READ_TIMEOUT_SEC") == "" {
		os.Setenv("CH_READ_TIMEOUT_SEC", "3600")
	}
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()
	if err := chwrapper.CreateTables(conn); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	if opts.Recompute {
		if err := recomputeNativeBalances(ctx, conn, opts); err != nil {
			log.Fatalf("revert-backfill recompute: %v", err)
		}
		return
	}
	if opts.RPC == "" {
		log.Fatalf("revert-backfill: no RPC (set --rpc or add the chain to config.yaml)")
	}
	if err := traceReverts(ctx, conn, opts); err != nil {
		log.Fatalf("revert-backfill trace: %v", err)
	}
}

func revertStateKey(phase string, opts RevertBackfillOptions) string {
	return fmt.Sprintf("revert-%s:%d:%d-%d", phase, opts.ChainID, opts.From, opts.To)
}

func traceReverts(ctx context.Context, conn driver.Conn, opts RevertBackfillOptions) error {
	key := revertStateKey("trace", opts)
	_, next, found, err := loadBackfillState(ctx, conn, key)
	if err != nil {
		return err
	}
	if !found || next < opts.From {
		next = opts.From
	}
	if next >= opts.To {
		slog.Info("revert-backfill: trace phase already complete", "from", opts.From, "to", opts.To)
		return nil
	}

	fetcher := evmrpc.NewFetcher(evmrpc.FetcherOptions{
		RpcURL:         opts.RPC,
		ChainID:        opts.ChainID,
		ChainName:      "revert-backfill",
		MaxConcurrency: opts.Concurrency,
		MaxRetries:     20,
		RetryDelay:     500 * time.Millisecond,
		BatchSize:      100,
		DebugBatchSize: opts.DebugBatch,
	})
	defer fetcher.Close()

	slog.Info("revert-backfill: tracing", "chain_id", opts.ChainID, "from", next, "to", opts.To)
	start, startNext := time.Now(), next
	var total int
	for next < opts.To {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := next + opts.TraceChunk
		if end > opts.To || end < next {
			end = opts.To
		}
		blocks, err := fetcher.FetchBlockRange(int64(next), int64(end)-1)
		if err != nil {
			return fmt.Errorf("fetch %d-%d: %w", next, end-1, err)
		}
		var frames []evmsyncer.RevertFrame
		for _, b := range blocks {
			if b == nil {
				return fmt.Errorf("missing block in %d-%d", next, end-1)
			}
			f, err := evmsyncer.RevertedValueFrames(b)
			if err != nil {
				return err
			}
			frames = append(frames, f...)
		}
		if err := insertReverts(ctx, conn, opts.ChainID, frames); err != nil {
			return fmt.Errorf("insert %d-%d: %w", next, end-1, err)
		}
		if err := saveBackfillState(ctx, conn, key, opts.To, end); err != nil {
			return err
		}
		total += len(frames)
		done := float64(end-startNext) / float64(opts.To-startNext)
		eta := time.Duration(float64(time.Since(start)) * (1 - done) / done)
		slog.Info("revert-backfill: traced", "blocks", fmt.Sprintf("%d-%d", next, end-1), "reverted_value_frames", len(frames),
			"total", total, "progress", fmt.Sprintf("%.2f%%", 100*done), "eta", eta.Round(time.Minute))
		next = end
	}
	slog.Info("revert-backfill: trace phase complete", "frames", total, "took", time.Since(start).Round(time.Second))
	return nil
}

func insertReverts(ctx context.Context, conn driver.Conn, chainID uint32, frames []evmsyncer.RevertFrame) error {
	if len(frames) == 0 {
		return nil
	}
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO raw_trace_reverts (chain_id, block_number, tx_hash, trace_address, from, to, value)`)
	if err != nil {
		return err
	}
	defer func() { _ = batch.Abort() }()
	for _, f := range frames {
		if err := batch.Append(chainID, f.BlockNumber, f.TxHash, f.TraceAddress, f.From, f.To, f.Value); err != nil {
			return err
		}
	}
	return chwrapper.RetryableBatchSend(batch)
}

// revertAntiJoin is appended after every "AND NOT reverted" in the indexer's
// native_balances INSERT so recorded reverted frames are left out as well.
const revertAntiJoin = `
      AND (tx_hash, trace_address) NOT IN (
        SELECT tx_hash, trace_address FROM raw_trace_reverts
        WHERE chain_id = @chain_id AND block_number >= @from_block AND block_number <= @to_block)`

// recomputeInsertSQL returns the indexer's native_balance_changes INSERT with
// the anti-join added to both trace branches, so the correction cannot drift
// from the live job's logic.
func recomputeInsertSQL(fileSQL string) (string, error) {
	var lines []string
	for _, l := range strings.Split(fileSQL, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		lines = append(lines, l)
	}
	clean := strings.Join(lines, "\n")
	i := strings.Index(clean, "INSERT INTO native_balance_changes")
	if i < 0 {
		return "", fmt.Errorf("native_balances.sql has no INSERT INTO native_balance_changes")
	}
	stmt := clean[i:]
	if j := strings.Index(stmt, ";"); j >= 0 {
		stmt = stmt[:j]
	}
	if n := strings.Count(stmt, "AND NOT reverted"); n != 2 {
		return "", fmt.Errorf("expected 2 trace branches with AND NOT reverted, found %d", n)
	}
	return strings.ReplaceAll(stmt, "AND NOT reverted", "AND NOT reverted"+revertAntiJoin), nil
}

func recomputeNativeBalances(ctx context.Context, conn driver.Conn, opts RevertBackfillOptions) error {
	_, traced, found, err := loadBackfillState(ctx, conn, revertStateKey("trace", opts))
	if err != nil {
		return err
	}
	if !found || traced < opts.To {
		return fmt.Errorf("trace phase for %d-%d is not complete (at %d), run it first", opts.From, opts.To, traced)
	}
	fileSQL, err := os.ReadFile(filepath.Join(opts.SQLDir, "evm_incremental", "native_balances.sql"))
	if err != nil {
		return err
	}
	insertSQL, err := recomputeInsertSQL(string(fileSQL))
	if err != nil {
		return err
	}

	// State: next is the first block not yet recomputed; pending (stored in the
	// cutoff field) is the end of a step whose rows may already be deleted, or 0.
	// Saving pending before the delete keeps a crashed step re-runnable: its end
	// cannot be re-derived from rows that are gone.
	key := revertStateKey("recompute", opts)
	pending, next, found, err := loadBackfillState(ctx, conn, key)
	if err != nil {
		return err
	}
	if !found || next < opts.From {
		// Start at the first existing row range at or after From, so no row
		// straddles the start.
		var first uint32
		var has uint8
		if err := conn.QueryRow(ctx, `SELECT min(from_block), toUInt8(count() > 0) FROM native_balance_changes
			WHERE chain_id = ? AND from_block >= ? AND to_block < ?`, opts.ChainID, opts.From, opts.To).Scan(&first, &has); err != nil {
			return err
		}
		if has == 0 {
			return fmt.Errorf("no native_balance_changes rows in %d-%d", opts.From, opts.To)
		}
		next = first
	}

	slog.Info("revert-backfill: recomputing native_balance_changes", "chain_id", opts.ChainID, "from", next, "to", opts.To)
	start := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var stepEnd uint32
		if pending >= next && pending < opts.To {
			stepEnd = pending // resume an interrupted step with the same bounds
		} else {
			// Step end: the last existing row range that ends inside
			// [next, next+chunk], so no row straddles the step boundary.
			var has uint8
			limit := next + opts.RecomputeChunk
			if limit >= opts.To || limit < next {
				limit = opts.To - 1
			}
			if err := conn.QueryRow(ctx, `SELECT max(to_block), toUInt8(count() > 0) FROM native_balance_changes
				WHERE chain_id = ? AND from_block >= ? AND to_block <= ?`, opts.ChainID, next, limit).Scan(&stepEnd, &has); err != nil {
				return err
			}
			if has == 0 {
				break // nothing left inside the range
			}
			if err := saveBackfillState(ctx, conn, key, stepEnd, next); err != nil {
				return err
			}
		}
		pending = 0
		if err := conn.Exec(ctx, `DELETE FROM native_balance_changes WHERE chain_id = ? AND from_block >= ? AND to_block <= ?`,
			opts.ChainID, next, stepEnd); err != nil {
			return fmt.Errorf("delete %d-%d: %w", next, stepEnd, err)
		}
		if err := conn.Exec(ctx, insertSQL,
			clickhouse.Named("chain_id", opts.ChainID),
			clickhouse.Named("from_block", next),
			clickhouse.Named("to_block", stepEnd)); err != nil {
			return fmt.Errorf("insert %d-%d (rows deleted, re-run to retry): %w", next, stepEnd, err)
		}
		if err := saveBackfillState(ctx, conn, key, 0, stepEnd+1); err != nil {
			return err
		}
		slog.Info("revert-backfill: recomputed", "blocks", fmt.Sprintf("%d-%d", next, stepEnd),
			"progress", fmt.Sprintf("%.2f%%", 100*float64(stepEnd+1-opts.From)/float64(opts.To-opts.From)))
		next = stepEnd + 1
		if next >= opts.To {
			break
		}
	}
	slog.Info("revert-backfill: recompute complete", "took", time.Since(start).Round(time.Second))
	return nil
}
