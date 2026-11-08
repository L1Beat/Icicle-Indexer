package pchainsyncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/lendingmarkets"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// lendingLTVBudget caps one cycle's dating work. Values left undated are
// retried next cycle. The first run dates every market once; later cycles only
// touch values that changed.
const lendingLTVBudget = 2 * time.Minute

var lendingHistoryRPCClient = &http.Client{Timeout: 30 * time.Second}

// TrackLendingLTVChanges dates the current LTV of every Aave V3 and BENQI
// market and every Aave V4 spoke entry into lending_ltv_changes. A value whose
// stored row already has the same LTV is skipped. Markets are narrowed to the
// day of their last change with lending_market_snapshots, then bisected on the
// archive node. Spokes are bisected on their dynamic config keys.
func TrackLendingLTVChanges(ctx context.Context, conn clickhouse.Conn, nodeURL string) error {
	ctx, cancel := context.WithTimeout(ctx, lendingLTVBudget)
	defer cancel()
	base := strings.TrimSuffix(strings.TrimRight(nodeURL, "/"), "/ext/bc/P")
	h := lendingmarkets.RPCHistory{URL: base + "/ext/bc/C/rpc", Client: lendingHistoryRPCClient}

	known, err := knownLTVs(ctx, conn)
	if err != nil {
		return err
	}
	head, err := h.Head(ctx)
	if err != nil {
		return err
	}
	markets, err := currentMarketLTVs(ctx, conn)
	if err != nil {
		return err
	}

	var out []lendingmarkets.LTVChange
	var errs []error
	for _, m := range markets {
		if v, ok := known[ltvKey(m.protocol, "", m.asset)]; ok && v == m.ltv {
			continue
		}
		ch, err := dateMarketLTV(ctx, conn, h, head, m)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			errs = append(errs, fmt.Errorf("%s %s: %w", m.protocol, m.asset, err))
			continue
		}
		out = append(out, ch)
	}
	if ctx.Err() == nil {
		chs, err := lendingmarkets.DateAaveV4Spokes(ctx, h, head, func(spoke, asset string, cf uint16) bool {
			v, ok := known[ltvKey(lendingmarkets.ProtocolAaveV4, spoke, asset)]
			return ok && v == cf
		})
		out = append(out, chs...)
		if err != nil {
			errs = append(errs, fmt.Errorf("aave-v4: %w", err))
		}
	}
	if err := insertLTVChanges(context.WithoutCancel(ctx), conn, out); err != nil {
		return err
	}
	if len(out) > 0 {
		slog.Info("Lending LTV changes dated", "rows", len(out))
	}
	return errors.Join(errs...)
}

func ltvKey(protocol, group, asset string) string { return protocol + "|" + group + "|" + asset }

func knownLTVs(ctx context.Context, conn clickhouse.Conn) (map[string]uint16, error) {
	rows, err := conn.Query(ctx, `SELECT protocol_id, group_id, asset, ltv_bps FROM lending_ltv_changes FINAL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint16{}
	for rows.Next() {
		var p, g, a string
		var v uint16
		if err := rows.Scan(&p, &g, &a, &v); err != nil {
			return nil, err
		}
		out[ltvKey(p, g, a)] = v
	}
	return out, rows.Err()
}

type marketLTV struct {
	protocol, market, asset string
	ltv                     uint16
}

// currentMarketLTVs reads each Aave V3 and BENQI market's LTV from its
// protocol's newest snapshot cycle.
func currentMarketLTVs(ctx context.Context, conn clickhouse.Conn) ([]marketLTV, error) {
	rows, err := conn.Query(ctx, `
		SELECT protocol_id, market, asset, assumeNotNull(ltv_bps)
		FROM (SELECT * FROM lending_market_snapshots FINAL) t
		WHERE snapshot_date >= today() - 1 AND protocol_id IN ('aave-v3', 'benqi') AND ltv_bps IS NOT NULL
		  AND (protocol_id, sampled_at) IN (
			SELECT protocol_id, max(sampled_at) FROM lending_market_snapshots
			WHERE snapshot_date >= today() - 1 GROUP BY protocol_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []marketLTV
	for rows.Next() {
		var m marketLTV
		if err := rows.Scan(&m.protocol, &m.market, &m.asset, &m.ltv); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// dateMarketLTV finds the last day the market's daily snapshots show another
// LTV and bisects between that sample and the next. No other LTV in the
// history means unchanged since listing.
func dateMarketLTV(ctx context.Context, conn clickhouse.Conn, h lendingmarkets.RPCHistory, head uint64, m marketLTV) (lendingmarkets.LTVChange, error) {
	unchanged := lendingmarkets.LTVChange{Protocol: m.protocol, Asset: m.asset, LTVBps: m.ltv}
	rows, err := conn.Query(ctx, `
		SELECT ltv_bps, sampled_at FROM lending_market_snapshots FINAL
		WHERE protocol_id = ? AND asset = ? AND ltv_bps IS NOT NULL
		ORDER BY snapshot_date`, m.protocol, m.asset)
	if err != nil {
		return unchanged, err
	}
	type sample struct {
		ltv uint16
		at  time.Time
	}
	var hist []sample
	for rows.Next() {
		var ltv *uint16
		var s sample
		if err := rows.Scan(&ltv, &s.at); err != nil {
			rows.Close()
			return unchanged, err
		}
		s.ltv = *ltv
		hist = append(hist, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return unchanged, err
	}
	j := -1
	for i, s := range hist {
		if s.ltv != m.ltv {
			j = i
		}
	}
	if j < 0 {
		return unchanged, nil
	}
	if j == len(hist)-1 {
		return unchanged, fmt.Errorf("newest snapshot shows LTV %d, not %d", hist[j].ltv, m.ltv)
	}
	lo, err := lendingmarkets.BlockAtTime(ctx, h, hist[j].at, head)
	if err != nil {
		return unchanged, err
	}
	hi, err := lendingmarkets.BlockAtTime(ctx, h, hist[j+1].at, head)
	if err != nil {
		return unchanged, err
	}
	return lendingmarkets.DateMarketLTV(ctx, h, m.protocol, m.market, m.asset, m.ltv, lo, hi)
}

func insertLTVChanges(ctx context.Context, conn clickhouse.Conn, chs []lendingmarkets.LTVChange) error {
	if len(chs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	var tuples []string
	var args []interface{}
	for _, c := range chs {
		tuples = append(tuples, "(?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args, c.Protocol, c.GroupID, c.Asset, c.LTVBps, c.Block, nullable(c.At), c.Tx, now)
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	if err := conn.Exec(wctx, `
		INSERT INTO lending_ltv_changes (
			protocol_id, group_id, asset, ltv_bps, changed_block, changed_at, changed_tx, checked_at
		) VALUES `+strings.Join(tuples, ", "), args...); err != nil {
		return fmt.Errorf("failed to insert lending LTV changes: %w", err)
	}
	return nil
}
