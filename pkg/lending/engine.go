package lending

import (
	"context"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Config configures the lending engine.
type Config struct {
	ChainID               uint32
	ArchiveRPC            string
	FallbackRPC           string // optional public RPC, used only on archive failure
	DiscoveryBatch        uint64
	ParamsRefreshInterval time.Duration
	RiskSnapshotInterval  time.Duration
	Health                HealthConfig
}

// DefaultConfig returns engine defaults for Avalanche C-Chain.
func DefaultConfig() Config {
	return Config{
		ChainID:               43114,
		DiscoveryBatch:        5000,
		ParamsRefreshInterval: 6 * time.Hour,
		RiskSnapshotInterval:  15 * time.Minute,
		Health:                DefaultHealthConfig(),
	}
}

// Engine wires discovery, the health engine, and price watching over a set of
// protocol adapters.
type Engine struct {
	cfg    Config
	store  *Store
	rpc    *Client
	health *HealthEngine
	price  *PriceWatch

	active      []Adapter
	discoveries []*Discovery
	liq         *LiquidationIngester
	liqSources  []liqSource
	risk        *RiskSampler
}

// NewEngine constructs the engine. Adapters are brought up in Bootstrap.
func NewEngine(conn driver.Conn, adapters []Adapter, cfg Config) (*Engine, error) {
	store, err := NewStore(conn, cfg.ChainID)
	if err != nil {
		return nil, err
	}
	return &Engine{
		cfg:   cfg,
		store: store,
		rpc:   NewClient(cfg.ArchiveRPC, cfg.FallbackRPC),
	}, nil
}

// Bootstrap resolves and verifies each adapter's addresses, loads parameters, and
// wires the runtime components. An adapter that fails to resolve is skipped with
// a loud warning rather than taking down the whole engine.
func (e *Engine) Bootstrap(ctx context.Context, adapters []Adapter) error {
	for _, a := range adapters {
		addrs, notes, err := a.Resolve(ctx, e.rpc)
		if err != nil {
			slog.Error("lending: adapter resolve failed, skipping protocol", "protocol", a.Protocol(), "error", err)
			continue
		}
		if err := e.store.WriteAddresses(ctx, a.Protocol(), notes); err != nil {
			slog.Warn("lending: write addresses failed", "protocol", a.Protocol(), "error", err)
		}

		// Inject resolved addresses before reading params: RefreshParams reads
		// per-asset config from the data provider / market list, which the adapter
		// only knows once configured.
		a.Configure(addrs, nil, GlobalParams{})

		params, globals, err := a.RefreshParams(ctx, e.rpc)
		if err != nil {
			slog.Error("lending: initial params refresh failed, skipping protocol", "protocol", a.Protocol(), "error", err)
			continue
		}
		a.Configure(addrs, params, globals)
		if err := e.store.WriteParams(ctx, a.Protocol(), params, globals); err != nil {
			slog.Warn("lending: write params failed", "protocol", a.Protocol(), "error", err)
		}

		e.active = append(e.active, a)
		e.discoveries = append(e.discoveries, NewDiscovery(e.store, a, e.cfg.DiscoveryBatch))
		e.liqSources = append(e.liqSources, buildLiqSource(a.Protocol(), addrs, params))
		slog.Info("lending: protocol ready", "protocol", a.Protocol(), "assets", len(params),
			"close_factor_bps", globals.CloseFactorBps, "incentive_bps", globals.LiquidationIncentiveBps)
	}

	if len(e.active) == 0 {
		return errNoProtocols
	}

	e.liq = NewLiquidationIngester(e.store, e.rpc, e.cfg.ChainID, e.liqSources)
	e.risk = NewRiskSampler(e.store, e.cfg.ChainID, e.cfg.RiskSnapshotInterval, nil, nil)
	e.health = NewHealthEngine(e.store, e.rpc, e.active, e.cfg.Health)
	e.price = NewPriceWatch(e.store, e.rpc, e.health)
	e.resolvePriceSources(ctx)
	return nil
}

// Run starts all loops and blocks until the context is cancelled.
func (e *Engine) Run(ctx context.Context) {
	for _, d := range e.discoveries {
		go d.Loop(ctx)
	}
	go e.health.Run(ctx)
	go e.price.Run(ctx)
	go e.paramsRefreshLoop(ctx)
	if e.liq != nil {
		go e.liq.Loop(ctx)
	}
	if e.risk != nil {
		go e.risk.Loop(ctx)
	}

	slog.Info("lending: engine running", "protocols", len(e.active), "chain_id", e.cfg.ChainID)
	<-ctx.Done()
	slog.Info("lending: engine stopped")
}

// paramsRefreshLoop periodically reloads governance-tunable parameters and
// re-resolves price sources so aggregator rotation is picked up.
func (e *Engine) paramsRefreshLoop(ctx context.Context) {
	interval := e.cfg.ParamsRefreshInterval
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, a := range e.active {
				params, globals, err := a.RefreshParams(ctx, e.rpc)
				if err != nil {
					slog.Warn("lending: params refresh failed", "protocol", a.Protocol(), "error", err)
					continue
				}
				// Re-resolve addresses to pick up any rotation. On failure keep the
				// existing resolved addresses rather than zeroing them.
				if addrs, _, rerr := a.Resolve(ctx, e.rpc); rerr == nil {
					a.Configure(addrs, params, globals)
				} else {
					slog.Warn("lending: re-resolve failed, keeping existing addresses", "protocol", a.Protocol(), "error", rerr)
				}
				if err := e.store.WriteParams(ctx, a.Protocol(), params, globals); err != nil {
					slog.Warn("lending: write params failed", "protocol", a.Protocol(), "error", err)
				}
			}
			e.resolvePriceSources(ctx)
		}
	}
}

// resolvePriceSources feeds the price watcher the current Aave aggregators and
// Benqi markets.
func (e *Engine) resolvePriceSources(ctx context.Context) {
	var aaveOracle, benqiOracle string
	var aaveAssets, benqiMarkets []string
	for _, a := range e.active {
		switch ad := a.(type) {
		case priceSource:
			oracle, assets, markets := ad.PriceSources()
			switch a.Protocol() {
			case ProtocolAaveV3:
				aaveOracle, aaveAssets = oracle, assets
			case ProtocolBenqi:
				benqiOracle, benqiMarkets = oracle, markets
			}
		}
	}
	if e.price != nil {
		e.price.ResolveSources(ctx, aaveOracle, aaveAssets, benqiOracle, benqiMarkets)
	}
}

// priceSource is optionally implemented by adapters to expose their oracle and
// the assets or markets the price watcher should follow.
type priceSource interface {
	PriceSources() (oracle string, assets []string, markets []string)
}

type engineError string

func (e engineError) Error() string { return string(e) }

const errNoProtocols engineError = "lending: no protocols could be brought up"
