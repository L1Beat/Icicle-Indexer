package pchainsyncer

import (
	"context"
	"fmt"
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
	sampledAt := time.Now().UTC()
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
	return readErr
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
