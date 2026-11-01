package pchainsyncer

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/lendingmarkets"
	"icicle/pkg/restaking"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// SnapshotLendingMarkets writes today's lending_market_snapshots rows for every
// market of the C-Chain lending protocols. nodeURL is the avalanchego base URL.
// A protocol that fails to read is skipped (its earlier rows stay), and its
// error is returned.
func SnapshotLendingMarkets(ctx context.Context, conn clickhouse.Conn, nodeURL string) error {
	base := strings.TrimSuffix(strings.TrimRight(nodeURL, "/"), "/ext/bc/P")
	caller := restaking.RPCCaller{URL: base + "/ext/bc/C/rpc", Client: liquidRPCClient}

	markets, readErr := lendingmarkets.ReadAll(ctx, caller)
	if len(markets) == 0 {
		return readErr
	}
	// All rows of a cycle share sampled_at, so readers can keep a protocol's
	// newest sample and drop markets that were delisted.
	if err := insertLendingMarkets(ctx, conn, markets, time.Now().UTC()); err != nil {
		return err
	}
	return readErr
}

// Lending history backfill: each past UTC day is read at its last block on the
// archive node, with the same reader as the live snapshot. Days are filled
// newest first, a few per cycle, down to lendingHistoryDays back. Progress is
// kept in indexer_watermarks (last_period = first day covered).
const (
	lendingHistoryDays      = 365
	lendingHistoryPerCycle  = 15
	lendingHistoryWatermark = "lendingmarkets/daily_from"
)

// BackfillLendingHistory fills lending_market_snapshots for past days.
func BackfillLendingHistory(ctx context.Context, conn clickhouse.Conn, nodeURL string) error {
	var covered time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = 43114 AND indexer_name = ?`, lendingHistoryWatermark)
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := rows.Scan(&covered); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if covered.IsZero() {
		// Live snapshots began on the oldest snapshot_date. Fill before it.
		if err := conn.QueryRow(ctx, `SELECT min(snapshot_date) FROM lending_market_snapshots`).Scan(&covered); err != nil {
			return err
		}
		if covered.Year() < 2000 {
			return nil
		}
	}
	covered = covered.UTC().Truncate(24 * time.Hour)
	target := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -lendingHistoryDays)
	if !covered.After(target) {
		return nil
	}
	from := covered.AddDate(0, 0, -lendingHistoryPerCycle)
	if from.Before(target) {
		from = target
	}

	// Last block of each day in [from, covered).
	brows, err := conn.Query(ctx, `
		SELECT toDate(block_time) AS d, max(block_number), max(block_time)
		FROM raw_blocks
		WHERE chain_id = 43114 AND block_time >= ? AND block_time < ?
		GROUP BY d ORDER BY d DESC`, from, covered)
	if err != nil {
		return err
	}
	type dayBlock struct {
		day   time.Time
		block uint32
		at    time.Time
	}
	var days []dayBlock
	for brows.Next() {
		var d dayBlock
		if err := brows.Scan(&d.day, &d.block, &d.at); err != nil {
			brows.Close()
			return err
		}
		days = append(days, d)
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		return err
	}

	base := strings.TrimSuffix(strings.TrimRight(nodeURL, "/"), "/ext/bc/P")
	start := time.Now()
	for _, d := range days {
		caller := restaking.RPCCaller{URL: base + "/ext/bc/C/rpc", Client: liquidRPCClient, Block: fmt.Sprintf("0x%x", d.block)}
		// A protocol that did not exist yet (Aave V4) fails and is skipped.
		markets, readErr := lendingmarkets.ReadAll(ctx, caller)
		if len(markets) == 0 {
			return fmt.Errorf("lending history %s: %w", d.day.Format("2006-01-02"), readErr)
		}
		if err := insertLendingMarkets(ctx, conn, markets, d.at.UTC()); err != nil {
			return err
		}
		wctx, cancel := chwrapper.WriteContext(ctx)
		err := conn.Exec(wctx, `
			INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
			VALUES (43114, ?, 'day', ?, ?)`, lendingHistoryWatermark, d.day, uint64(d.block))
		cancel()
		if err != nil {
			return err
		}
	}
	// Days with no blocks in raw_blocks would never be visited; move past them.
	if len(days) == 0 || !days[len(days)-1].day.Equal(from) {
		wctx, cancel := chwrapper.WriteContext(ctx)
		err := conn.Exec(wctx, `
			INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
			VALUES (43114, ?, 'day', ?, 0)`, lendingHistoryWatermark, from)
		cancel()
		if err != nil {
			return err
		}
	}
	slog.Info("Lending history backfilled", "from", from.Format("2006-01-02"), "to", covered.Format("2006-01-02"), "days", len(days), "duration", time.Since(start))
	return nil
}

// insertLendingMarkets writes one snapshot of markets, all sharing sampledAt
// and the UTC day of sampledAt.
func insertLendingMarkets(ctx context.Context, conn clickhouse.Conn, markets []lendingmarkets.Market, sampledAt time.Time) error {
	day := sampledAt.Format("2006-01-02")
	var tuples []string
	var args []interface{}
	for _, m := range markets {
		tuples = append(tuples, "(?, ?, ?, ?, ?, ?, toUInt256(?), toUInt256(?), ?, ?, ?, ?, ?, ?, toUInt256OrNull(?), toUInt256OrNull(?), ?)")
		args = append(args, day, m.Protocol, m.Asset, m.Market, m.Symbol, m.Decimals,
			m.Supplied.String(), m.Borrowed.String(), m.SupplyAPR, m.BorrowAPR,
			nullable(m.CollateralEnabled), nullable(m.LTVBps), nullable(m.LiqThresholdBps), nullable(m.PriceUSD),
			bigOrEmpty(m.SupplyCap), bigOrEmpty(m.BorrowCap), sampledAt)
	}

	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	err := conn.Exec(wctx, `
		INSERT INTO lending_market_snapshots (
			snapshot_date, protocol_id, asset, market, symbol, decimals,
			supplied, borrowed, supply_apr, borrow_apr, collateral_enabled,
			ltv_bps, liquidation_threshold_bps, price_usd, supply_cap, borrow_cap, sampled_at
		) VALUES `+strings.Join(tuples, ", "), args...)
	if err != nil {
		return fmt.Errorf("failed to insert lending market snapshots: %w", err)
	}
	return nil
}

// nullable turns a nil pointer into a NULL query argument.
func nullable[T any](p *T) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

// bigOrEmpty formats a cap for toUInt256OrNull, where "" becomes NULL.
func bigOrEmpty(v *big.Int) string {
	if v == nil {
		return ""
	}
	return v.String()
}
