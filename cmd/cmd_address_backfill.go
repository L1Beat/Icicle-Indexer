package cmd

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"icicle/pkg/chwrapper"
)

// AddressBackfillOptions configures the one-time copy of history into the
// address_txs / address_traces lookup tables.
type AddressBackfillOptions struct {
	Tables     []string // "txs", "traces"
	ChainID    uint32   // 0 = every chain in raw_blocks
	ChunkSize  uint32   // blocks per INSERT ... SELECT
	MaxThreads int      // per-chunk ClickHouse threads, kept low so the explorer stays responsive
}

// addressBackfillSelects copy rows exactly as address_txs_mv / address_traces_mv
// do, restricted to one chain and block range. Keep them in sync with the
// views in raw_tables.sql (TestAddressBackfillMatchesViews checks this).
var addressBackfillSelects = map[string]struct{ target, sql string }{
	"txs": {"address_txs", `
SELECT
    chain_id, address, block_number, transaction_index, block_time, hash,
    from, to, value, gas_limit, gas_price, gas_used, success, type,
    substring(input, 1, 4) AS selector, toUInt32(length(input)) AS input_len
FROM raw_txs
ARRAY JOIN arrayDistinct(if(isNull(to), [from], [from, assumeNotNull(to)])) AS address`},
	"traces": {"address_traces", `
SELECT
    chain_id, address, block_number, transaction_index, trace_address, block_time,
    tx_hash, from, to, value, gas_used, call_type, tx_success
FROM raw_traces
ARRAY JOIN arrayDistinct(if(isNull(to), [from], [from, assumeNotNull(to)])) AS address
WHERE (value > 0 OR call_type IN ('CREATE', 'CREATE2'))`},
}

// RunAddressBackfill copies every block below the materialized view's first
// captured block into the lookup tables, chain by chain, in chunks. The view
// has captured every insert since it was created (one writer per chain,
// blocks in order), so below that block is exactly what it missed.
// Progress is saved after each chunk, so it can be stopped and re-run. A chunk
// interrupted midway is copied again; ReplacingMergeTree merges the duplicates.
// Runs alongside the live indexer.
func RunAddressBackfill(ctx context.Context, opts AddressBackfillOptions) {
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()
	if err := chwrapper.CreateTables(conn); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	chains := []uint32{opts.ChainID}
	if opts.ChainID == 0 {
		if chains, err = indexedChains(ctx, conn); err != nil {
			log.Fatalf("Failed to list chains: %v", err)
		}
	}

	for _, table := range opts.Tables {
		if _, ok := addressBackfillSelects[table]; !ok {
			log.Fatalf("Unknown table %q (use txs, traces)", table)
		}
		for _, chainID := range chains {
			if err := backfillAddressTable(ctx, conn, table, chainID, opts); err != nil {
				log.Fatalf("address-backfill %s chain %d: %v", table, chainID, err)
			}
		}
	}
	slog.Info("address-backfill: done", "tables", opts.Tables, "chains", len(chains))
}

func indexedChains(ctx context.Context, conn driver.Conn) ([]uint32, error) {
	rows, err := conn.Query(ctx, "SELECT DISTINCT chain_id FROM raw_blocks ORDER BY chain_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func backfillAddressTable(ctx context.Context, conn driver.Conn, table string, chainID uint32, opts AddressBackfillOptions) error {
	sel := addressBackfillSelects[table]
	source := "raw_" + table
	key := fmt.Sprintf("%s:%d", table, chainID)

	cutoff, next, found, err := loadBackfillState(ctx, conn, key)
	if err != nil {
		return err
	}
	if !found {
		// First run: nothing has been backfilled yet, so the lowest block in
		// the lookup table is the view's first capture. A chain with nothing
		// inserted since the view was created gets everything up to its head.
		var captured, head uint32
		var hasCaptured uint8
		if err := conn.QueryRow(ctx, fmt.Sprintf(
			"SELECT min(block_number), toUInt8(count() > 0) FROM %s WHERE chain_id = ?", sel.target),
			chainID).Scan(&captured, &hasCaptured); err != nil {
			return fmt.Errorf("read view start: %w", err)
		}
		if head, err = chwrapper.GetLatestBlockForChain(conn, source, chainID); err != nil {
			return err
		}
		cutoff = head + 1
		if hasCaptured == 1 {
			cutoff = captured
		}
		if err := saveBackfillState(ctx, conn, key, cutoff, 0); err != nil {
			return err
		}
	}
	if next >= cutoff {
		slog.Info("address-backfill: already complete", "table", table, "chain_id", chainID, "cutoff", cutoff)
		return nil
	}

	slog.Info("address-backfill: starting", "table", table, "chain_id", chainID, "from_block", next, "cutoff", cutoff)
	query := fmt.Sprintf(`INSERT INTO %s %s
		%s chain_id = ? AND block_number >= ? AND block_number < ?
		SETTINGS max_threads = %d, max_execution_time = 3600`,
		sel.target, sel.sql, whereOrAnd(table), opts.MaxThreads)

	start := time.Now()
	startNext := next
	for next < cutoff {
		if err := ctx.Err(); err != nil {
			return err
		}
		to := next + opts.ChunkSize
		if to > cutoff || to < next {
			to = cutoff
		}
		t := time.Now()
		if err := conn.Exec(ctx, query, chainID, next, to); err != nil {
			return fmt.Errorf("blocks %d-%d: %w", next, to, err)
		}
		if err := saveBackfillState(ctx, conn, key, cutoff, to); err != nil {
			return err
		}
		done := float64(to-startNext) / float64(cutoff-startNext)
		eta := time.Duration(float64(time.Since(start)) * (1 - done) / done)
		slog.Info("address-backfill: chunk", "table", table, "chain_id", chainID,
			"blocks", fmt.Sprintf("%d-%d", next, to), "took", time.Since(t).Round(time.Second),
			"progress", fmt.Sprintf("%.1f%%", 100*float64(to)/float64(cutoff)), "eta", eta.Round(time.Minute))
		next = to
	}
	slog.Info("address-backfill: complete", "table", table, "chain_id", chainID, "took", time.Since(start).Round(time.Second))
	return nil
}

// whereOrAnd returns the keyword that appends the chunk filter to the select:
// the traces select already has a WHERE.
func whereOrAnd(table string) string {
	if table == "traces" {
		return "AND"
	}
	return "WHERE"
}

func loadBackfillState(ctx context.Context, conn driver.Conn, key string) (cutoff, next uint32, found bool, err error) {
	rows, err := conn.Query(ctx, "SELECT cutoff, next_block FROM address_backfill_state WHERE key = ?", key)
	if err != nil {
		return 0, 0, false, fmt.Errorf("read state: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&cutoff, &next); err != nil {
			return 0, 0, false, err
		}
		found = true
	}
	return cutoff, next, found, rows.Err()
}

func saveBackfillState(ctx context.Context, conn driver.Conn, key string, cutoff, next uint32) error {
	if err := conn.Exec(ctx, "INSERT INTO address_backfill_state (key, cutoff, next_block) VALUES (?, ?, ?)", key, cutoff, next); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
