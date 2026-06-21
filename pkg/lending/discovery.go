package lending

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Discovery reduces the protocol's event logs in raw_logs into the account
// universe and candidate asset exposures. It is idempotent and watermark-driven,
// reading only up to the raw sync watermark so it never runs ahead of confirmed
// data (the indexer's finality-based, no-reorg posture).
type Discovery struct {
	store   *Store
	adapter Adapter
	name    string
	batch   uint64
}

// NewDiscovery builds a discovery cursor for one protocol.
func NewDiscovery(store *Store, adapter Adapter, batch uint64) *Discovery {
	if batch == 0 {
		batch = 5000
	}
	return &Discovery{
		store:   store,
		adapter: adapter,
		name:    "lending_discovery:" + string(adapter.Protocol()),
		batch:   batch,
	}
}

// RunOnce processes one block batch and returns how many blocks were processed.
// Zero means there is nothing new to do right now.
func (d *Discovery) RunOnce(ctx context.Context) (uint64, error) {
	wm, err := d.store.GetWatermark(ctx, d.name)
	if err != nil {
		return 0, fmt.Errorf("get watermark: %w", err)
	}
	spec := d.adapter.Discovery()

	// First run: start one block before the earliest log for the protocol's
	// contracts, the self-correcting backfill floor (rule 8).
	if wm == 0 {
		floor, err := d.store.EarliestLogBlock(ctx, spec.Addresses)
		if err != nil {
			return 0, fmt.Errorf("earliest log block: %w", err)
		}
		if floor > 0 {
			wm = floor - 1
		}
	}

	safe, err := d.store.SafeBlock(ctx)
	if err != nil {
		return 0, fmt.Errorf("safe block: %w", err)
	}
	if safe == 0 || safe <= wm {
		return 0, nil
	}

	from := wm + 1
	to := from + d.batch - 1
	if to > safe {
		to = safe
	}

	logs, err := d.store.ReadLogs(ctx, spec.Addresses, spec.Topics, from, to)
	if err != nil {
		return 0, fmt.Errorf("read logs: %w", err)
	}

	accounts := map[string]uint32{}
	var exposures []Exposure
	for _, l := range logs {
		for _, e := range d.adapter.DecodeLog(l) {
			e.Account = NormalizeAddr(e.Account)
			if e.Account == ZeroAddress {
				continue
			}
			if b, ok := accounts[e.Account]; !ok || e.Block > b {
				accounts[e.Account] = e.Block
			}
			if e.Asset != "" && NormalizeAddr(e.Asset) != ZeroAddress {
				exposures = append(exposures, e)
			}
		}
	}

	proto := d.adapter.Protocol()
	if err := d.store.UpsertAccounts(ctx, proto, accounts); err != nil {
		return 0, fmt.Errorf("upsert accounts: %w", err)
	}
	if err := d.store.UpsertExposure(ctx, proto, exposures); err != nil {
		return 0, fmt.Errorf("upsert exposure: %w", err)
	}
	if err := d.store.SetWatermark(ctx, d.name, to); err != nil {
		return 0, fmt.Errorf("set watermark: %w", err)
	}

	if len(accounts) > 0 {
		slog.Info("lending: discovery batch", "protocol", proto, "from", from, "to", to, "new_accounts", len(accounts))
	}
	return to - from + 1, nil
}

// Loop runs discovery continuously, sleeping when caught up.
func (d *Discovery) Loop(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		n, err := d.RunOnce(ctx)
		if err != nil {
			slog.Warn("lending: discovery error, will retry", "protocol", d.adapter.Protocol(), "error", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if n == 0 {
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
		}
	}
}
