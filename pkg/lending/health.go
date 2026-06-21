package lending

import (
	"context"
	"log/slog"
	"math/big"
	"sync"
	"time"
)

// HealthConfig tunes the tiered refresh policy. Edges are health factors in WAD.
type HealthConfig struct {
	HotEdge       *big.Int // below this HF a position is hot
	WarmEdge      *big.Int // below this HF a position is warm
	NearEdge      *big.Int // below this HF a position is near-liquidatable (alert threshold)
	PriceBandEdge *big.Int // price moves recompute exposed positions below this HF, across tiers (rule 1)

	HotInterval  time.Duration
	WarmInterval time.Duration
	ColdInterval time.Duration

	BatchSize    int // Multicall sub-calls per aggregate3 request
	SweepWorkers int // concurrency for sweep and burst reads
}

// DefaultHealthConfig returns sane defaults sized to the read-budget analysis.
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		HotEdge:       wadRatio(110, 100),
		WarmEdge:      wadRatio(150, 100),
		NearEdge:      wadRatio(105, 100),
		PriceBandEdge: wadRatio(150, 100),
		HotInterval:   4 * time.Second,
		WarmInterval:  60 * time.Second,
		ColdInterval:  20 * time.Minute,
		BatchSize:     400,
		SweepWorkers:  8,
	}
}

func wadRatio(num, den int64) *big.Int {
	return new(big.Int).Div(new(big.Int).Mul(big.NewInt(num), WAD), big.NewInt(den))
}

type cacheState struct {
	tier         Tier
	liquidatable bool
	near         bool
	hf           *big.Int
}

// HealthEngine reads position health on a tiered schedule and writes snapshots,
// per-asset legs, and crossing alerts.
type HealthEngine struct {
	store    *Store
	rpc      *Client
	adapters map[Protocol]Adapter
	cfg      HealthConfig

	mu    sync.Mutex
	state map[Protocol]map[string]*cacheState
}

// NewHealthEngine builds the engine over the configured adapters.
func NewHealthEngine(store *Store, rpc *Client, adapters []Adapter, cfg HealthConfig) *HealthEngine {
	m := map[Protocol]Adapter{}
	state := map[Protocol]map[string]*cacheState{}
	for _, a := range adapters {
		m[a.Protocol()] = a
		state[a.Protocol()] = map[string]*cacheState{}
	}
	return &HealthEngine{store: store, rpc: rpc, adapters: m, cfg: cfg, state: state}
}

// SeedState loads the last persisted liquidatable/near flags so the first sweep
// does not re-alert on positions that were already in that state.
func (e *HealthEngine) SeedState(ctx context.Context) {
	for proto := range e.adapters {
		prior, err := e.store.ReadPriorStates(ctx, proto, e.cfg.NearEdge)
		if err != nil {
			slog.Warn("lending: seed prior states failed", "protocol", proto, "error", err)
			continue
		}
		e.mu.Lock()
		for acc, ps := range prior {
			e.state[proto][acc] = &cacheState{liquidatable: ps.Liquidatable, near: ps.Near}
		}
		e.mu.Unlock()
	}
}

// Run drives the three tier loops until the context is cancelled. The cold sweep
// also reclassifies tiers, so it runs first to populate them.
func (e *HealthEngine) Run(ctx context.Context) {
	e.SeedState(ctx)
	e.sweep(ctx)

	hot := time.NewTicker(e.cfg.HotInterval)
	warm := time.NewTicker(e.cfg.WarmInterval)
	cold := time.NewTicker(e.cfg.ColdInterval)
	defer hot.Stop()
	defer warm.Stop()
	defer cold.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-hot.C:
			e.refreshTier(ctx, TierHot)
		case <-warm.C:
			e.refreshTier(ctx, TierWarm)
		case <-cold.C:
			e.sweep(ctx)
		}
	}
}

// sweep refreshes every account with per-asset detail, reclassifies tiers, and
// promotes anything that became liquidatable or near. Detail is needed so totals
// land for both protocols: Aave's summary read carries collateral and debt, but
// Benqi's getAccountLiquidity returns only net shortfall, so a healthy Benqi
// position would otherwise never get its debt and collateral populated.
func (e *HealthEngine) sweep(ctx context.Context) {
	head, err := e.rpc.BlockNumber(ctx)
	if err != nil {
		slog.Warn("lending: sweep head read failed", "error", err)
		return
	}
	for proto, adapter := range e.adapters {
		accounts, err := e.store.ReadAccounts(ctx, proto)
		if err != nil {
			slog.Warn("lending: sweep read accounts failed", "protocol", proto, "error", err)
			continue
		}
		exposure, err := e.store.ReadExposure(ctx, proto)
		if err != nil {
			slog.Warn("lending: sweep read exposure failed", "protocol", proto, "error", err)
			continue
		}
		start := time.Now()
		e.refresh(ctx, adapter, accounts, exposure, head, "sweep")
		metricRefreshSeconds.WithLabelValues("sweep").Observe(time.Since(start).Seconds())
		slog.Info("lending: cold sweep complete", "protocol", proto, "accounts", len(accounts), "elapsed", time.Since(start))
	}
}

// refreshTier refreshes accounts currently assigned to a tier, with per-asset
// detail so the feed and crossing detection stay accurate near the line.
func (e *HealthEngine) refreshTier(ctx context.Context, tier Tier) {
	head, err := e.rpc.BlockNumber(ctx)
	if err != nil {
		slog.Warn("lending: tier head read failed", "tier", tier, "error", err)
		return
	}
	for proto, adapter := range e.adapters {
		accounts := e.accountsInTier(proto, tier)
		if len(accounts) == 0 {
			continue
		}
		exposure, err := e.store.ReadExposure(ctx, proto)
		if err != nil {
			slog.Warn("lending: tier read exposure failed", "protocol", proto, "error", err)
			continue
		}
		start := time.Now()
		e.refresh(ctx, adapter, accounts, exposure, head, string(tier))
		metricRefreshSeconds.WithLabelValues(string(tier)).Observe(time.Since(start).Seconds())
		slog.Debug("lending: tier refresh complete", "protocol", proto, "tier", tier, "accounts", len(accounts), "elapsed", time.Since(start))
	}
}

// RecomputeAsset refreshes, with detail, every account exposed to a moved asset
// whose last-known health sits within the price band, regardless of timer tier
// (rule 1). Accounts with no cached health are included to be safe.
func (e *HealthEngine) RecomputeAsset(ctx context.Context, proto Protocol, asset string) {
	adapter, ok := e.adapters[proto]
	if !ok {
		return
	}
	exposed, err := e.store.ReadAccountsExposedTo(ctx, proto, asset)
	if err != nil {
		slog.Warn("lending: price recompute read exposed failed", "protocol", proto, "asset", asset, "error", err)
		return
	}
	if len(exposed) == 0 {
		return
	}

	band := e.cfg.PriceBandEdge
	e.mu.Lock()
	inBand := exposed[:0]
	for _, acc := range exposed {
		st := e.state[proto][acc]
		if st == nil || st.hf == nil || st.hf.Cmp(band) < 0 {
			inBand = append(inBand, acc)
		}
	}
	picked := append([]string(nil), inBand...)
	e.mu.Unlock()

	head, err := e.rpc.BlockNumber(ctx)
	if err != nil {
		slog.Warn("lending: price recompute head read failed", "error", err)
		return
	}
	exposure, err := e.store.ReadExposure(ctx, proto)
	if err != nil {
		slog.Warn("lending: price recompute read exposure failed", "protocol", proto, "error", err)
		return
	}
	slog.Info("lending: price-triggered recompute", "protocol", proto, "asset", asset, "accounts", len(picked))
	start := time.Now()
	e.refresh(ctx, adapter, picked, exposure, head, "price")
	metricRefreshSeconds.WithLabelValues("price").Observe(time.Since(start).Seconds())
}

// refresh reads health for a set of accounts, classifies tiers, detects crossings,
// and persists everything. When exposure is nil the probes are summary-only.
func (e *HealthEngine) refresh(ctx context.Context, adapter Adapter, accounts []string, exposure map[string][]Exposure, head uint64, trigger string) {
	if len(accounts) == 0 {
		return
	}
	proto := adapter.Protocol()
	metricRecomputeTotal.WithLabelValues(trigger).Add(float64(len(accounts)))

	probes := make([]HealthProbe, 0, len(accounts))
	for _, acc := range accounts {
		var exp []Exposure
		if exposure != nil {
			exp = exposure[acc]
		}
		probes = append(probes, adapter.BuildProbe(acc, exp))
	}

	healths := e.executeProbes(ctx, probes, head)
	if len(healths) == 0 {
		return
	}

	tiers := make(map[string]Tier, len(healths))
	var alerts []Alert
	e.mu.Lock()
	for _, h := range healths {
		if !h.OK {
			continue
		}
		tier := ClassifyTier(h.HealthFactor, h.Liquidatable, e.cfg.HotEdge, e.cfg.WarmEdge)
		tiers[h.Account.Address] = tier
		near := !h.Liquidatable && h.HealthFactor != nil && h.HealthFactor.Cmp(e.cfg.NearEdge) < 0

		if a, ok := e.crossing(proto, h, near); ok {
			alerts = append(alerts, a)
		}
		e.state[proto][h.Account.Address] = &cacheState{tier: tier, liquidatable: h.Liquidatable, near: near, hf: h.HealthFactor}
	}
	e.mu.Unlock()

	if err := e.store.WriteHealth(ctx, healths, tiers, alerts); err != nil {
		slog.Warn("lending: write health failed", "protocol", proto, "error", err)
	}
	e.updateGauges()
}

// crossing compares new state to the cached prior state and returns an alert when
// a position crosses into liquidatable, into near-liquidatable, or recovers.
// Caller holds e.mu.
func (e *HealthEngine) crossing(proto Protocol, h Health, near bool) (Alert, bool) {
	prev := e.state[proto][h.Account.Address]
	var kind string
	switch {
	case h.Liquidatable && (prev == nil || !prev.liquidatable):
		kind = "liquidatable"
	case !h.Liquidatable && near && (prev == nil || (!prev.near && !prev.liquidatable)):
		kind = "near_liquidatable"
	case !h.Liquidatable && !near && prev != nil && (prev.liquidatable || prev.near):
		kind = "recovered"
	default:
		return Alert{}, false
	}
	return Alert{
		Protocol: proto, Account: h.Account.Address, Kind: kind,
		HealthFactor: h.HealthFactor, CollateralBase: h.CollateralBase, DebtBase: h.DebtBase,
		Block: uint32(h.BlockNumber),
	}, true
}

// executeProbes batches probe calls into aggregate3 requests, keeping each
// probe's calls within a single batch, and decodes results back per probe. Chunks
// run concurrently bounded by SweepWorkers so large read sets stay fast without
// overwhelming the archive node.
func (e *HealthEngine) executeProbes(ctx context.Context, probes []HealthProbe, head uint64) []Health {
	batchSize := e.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 400
	}
	workers := e.cfg.SweepWorkers
	if workers <= 0 {
		workers = 8
	}

	// Pack probes into chunks, never splitting a single probe across chunks.
	var chunks [][]HealthProbe
	i := 0
	for i < len(probes) {
		var chunk []HealthProbe
		calls := 0
		for i < len(probes) {
			n := len(probes[i].Calls)
			if calls > 0 && calls+n > batchSize {
				break
			}
			chunk = append(chunk, probes[i])
			calls += n
			i++
		}
		if len(chunk) == 0 {
			i++
			continue
		}
		chunks = append(chunks, chunk)
	}

	var mu sync.Mutex
	out := make([]Health, 0, len(probes))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, chunk := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(chunk []HealthProbe) {
			defer wg.Done()
			defer func() { <-sem }()

			var calls []Call
			for _, p := range chunk {
				calls = append(calls, p.Calls...)
			}

			metricMulticallTotal.Inc()
			res, err := e.rpc.EthCall(ctx, Multicall3Address, EncodeAggregate3(calls), "latest")
			if err != nil {
				slog.Warn("lending: multicall failed", "calls", len(calls), "error", err)
				return
			}
			results, err := DecodeAggregate3(decodeHex(res))
			if err != nil || len(results) != len(calls) {
				slog.Warn("lending: multicall decode mismatch", "want", len(calls), "got", len(results), "error", err)
				return
			}

			local := make([]Health, 0, len(chunk))
			off := 0
			for _, p := range chunk {
				n := len(p.Calls)
				local = append(local, p.Decode(results[off:off+n], head))
				off += n
			}
			mu.Lock()
			out = append(out, local...)
			mu.Unlock()
		}(chunk)
	}
	wg.Wait()
	return out
}

func (e *HealthEngine) accountsInTier(proto Protocol, tier Tier) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for acc, st := range e.state[proto] {
		if st.tier == tier {
			out = append(out, acc)
		}
	}
	return out
}
