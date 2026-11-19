package pchainsyncer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/rwaprices"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const cChainID = 43114

// SnapshotFundPrices writes tokenized_fund_prices rows for the floating-price
// registry assets (tokenized funds, equity and commodities: every peg but USD)
// with an on-chain USD price per share, as read by rwaprices.Read. Assets with
// none get no row, and an asset whose read fails keeps its previous row.
func SnapshotFundPrices(ctx context.Context, conn clickhouse.Conn, caller rwaprices.Caller) error {
	funds, err := queryRegistry(ctx, conn, "category != 'stablecoin' AND peg != 'USD'")
	if err != nil {
		return err
	}
	if len(funds) == 0 {
		return nil
	}
	usdList, err := queryRegistry(ctx, conn, "category = 'stablecoin' AND peg = 'USD'")
	if err != nil {
		return err
	}
	usd := make(map[string]uint8, len(usdList))
	for _, t := range usdList {
		usd[string(t.Address)] = t.Decimals
	}

	var tuples []string
	var args []interface{}
	var errs []error
	now := time.Now().UTC()
	for _, f := range funds {
		p, ok, err := rwaprices.Read(ctx, caller, f, usd, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("fund 0x%x: %w", f.Address, err))
			continue
		}
		if !ok {
			continue
		}
		tuples = append(tuples, "(?, ?, ?, ?, ?, ?)")
		args = append(args, uint32(cChainID), string(f.Address), p.USD, p.Source, p.PriceAt, now)
	}
	if len(tuples) > 0 {
		wctx, cancel := chwrapper.WriteContext(ctx)
		defer cancel()
		if err := conn.Exec(wctx, `
			INSERT INTO tokenized_fund_prices (chain_id, token, price_usd, source, price_at, sampled_at)
			VALUES `+strings.Join(tuples, ", "), args...); err != nil {
			return fmt.Errorf("failed to insert fund prices: %w", err)
		}
	}
	return errors.Join(errs...)
}

func queryRegistry(ctx context.Context, conn clickhouse.Conn, where string) ([]rwaprices.Token, error) {
	rows, err := conn.Query(ctx, `SELECT token, decimals FROM stablecoins FINAL WHERE chain_id = ? AND `+where, uint32(cChainID))
	if err != nil {
		return nil, fmt.Errorf("failed to read stablecoin registry: %w", err)
	}
	defer rows.Close()
	var out []rwaprices.Token
	for rows.Next() {
		var t rwaprices.Token
		var token string
		if err := rows.Scan(&token, &t.Decimals); err != nil {
			return nil, err
		}
		t.Address = []byte(token)
		out = append(out, t)
	}
	return out, rows.Err()
}
