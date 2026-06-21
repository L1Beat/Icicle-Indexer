package lending

import (
	"context"
	"log/slog"
	"math/big"
	"sync"
	"time"
)

// PriceWatch triggers recomputation when collateral or debt prices move. For
// Aave assets whose oracle source resolves to a single Chainlink aggregator
// behind a proxy, it tails that aggregator's AnswerUpdated logs already stored in
// raw_logs. For composite or wrapped sources with no single AnswerUpdated, and
// for Benqi, it falls back to polling oracle prices at hot frequency (rule 9).
// Either path recomputes through the engine, which applies the asset-exposure
// band fan-out (rule 1).
type PriceWatch struct {
	store  *Store
	rpc    *Client
	engine *HealthEngine

	moveThresholdBps int64
	pollInterval     time.Duration
	answerUpdated    string

	aaveOracle  string
	benqiOracle string

	mu             sync.Mutex
	aggToAsset     map[string]assetRef // aggregator address -> asset it prices
	pollAaveAssets []string            // Aave assets without a single aggregator
	benqiMarkets   []string            // Benqi markets, always polled
	lastPrice      map[string]*big.Int // "proto|asset" -> last observed price
}

type assetRef struct {
	proto Protocol
	asset string
}

// NewPriceWatch builds a price watcher.
func NewPriceWatch(store *Store, rpc *Client, engine *HealthEngine) *PriceWatch {
	return &PriceWatch{
		store:            store,
		rpc:              rpc,
		engine:           engine,
		moveThresholdBps: 50, // 0.5 percent
		pollInterval:     6 * time.Second,
		answerUpdated:    EventTopic("AnswerUpdated(int256,uint256,uint256)"),
		aggToAsset:       map[string]assetRef{},
		lastPrice:        map[string]*big.Int{},
	}
}

// ResolveSources maps Aave assets to their Chainlink aggregators where possible
// and records the poll-fallback sets. Call this after each params refresh so
// aggregator rotation is picked up.
func (p *PriceWatch) ResolveSources(ctx context.Context, aaveOracle string, aaveAssets []string, benqiOracle string, benqiMarkets []string) {
	aggToAsset := map[string]assetRef{}
	var pollAave []string

	for _, asset := range aaveAssets {
		src, err := p.callAddr(ctx, aaveOracle, EncodeCall1Addr("getSourceOfAsset(address)", asset))
		if err != nil || src == ZeroAddress {
			pollAave = append(pollAave, asset)
			continue
		}
		agg, err := p.callAddr(ctx, src, EncodeCall0("aggregator()"))
		if err != nil || agg == ZeroAddress {
			// Composite or wrapped adapter with no single AnswerUpdated source.
			pollAave = append(pollAave, asset)
			continue
		}
		aggToAsset[NormalizeAddr(agg)] = assetRef{proto: ProtocolAaveV3, asset: NormalizeAddr(asset)}
	}

	p.mu.Lock()
	p.aaveOracle = aaveOracle
	p.benqiOracle = benqiOracle
	p.benqiMarkets = benqiMarkets
	p.aggToAsset = aggToAsset
	p.pollAaveAssets = pollAave
	p.mu.Unlock()

	slog.Info("lending: price sources resolved",
		"aave_aggregators", len(aggToAsset), "aave_polled", len(pollAave), "benqi_markets", len(benqiMarkets))
}

// Run drives the AnswerUpdated tail and the poll fallback until cancelled.
func (p *PriceWatch) Run(ctx context.Context) {
	go p.runTail(ctx)
	p.runPoll(ctx)
}

// runTail follows new AnswerUpdated logs for the mapped aggregators.
func (p *PriceWatch) runTail(ctx context.Context) {
	const cursor = "lending_pricewatch:answerupdated"
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		p.tailOnce(ctx, cursor)
		if !sleepCtx(ctx, 2*time.Second) {
			return
		}
	}
}

func (p *PriceWatch) tailOnce(ctx context.Context, cursor string) {
	p.mu.Lock()
	aggs := make([]string, 0, len(p.aggToAsset))
	for a := range p.aggToAsset {
		aggs = append(aggs, a)
	}
	p.mu.Unlock()
	if len(aggs) == 0 {
		return
	}

	wm, err := p.store.GetWatermark(ctx, cursor)
	if err != nil {
		return
	}
	safe, err := p.store.SafeBlock(ctx)
	if err != nil || safe <= wm {
		return
	}
	logs, err := p.store.ReadLogs(ctx, aggs, []string{p.answerUpdated}, wm+1, safe)
	if err != nil {
		slog.Warn("lending: pricewatch read logs failed", "error", err)
		return
	}

	triggered := map[string]assetRef{}
	p.mu.Lock()
	for _, l := range logs {
		if ref, ok := p.aggToAsset[NormalizeAddr(l.Address)]; ok {
			triggered[ref.asset] = ref
		}
	}
	p.mu.Unlock()

	for _, ref := range triggered {
		p.engine.RecomputeAsset(ctx, ref.proto, ref.asset)
	}
	if err := p.store.SetWatermark(ctx, cursor, safe); err != nil {
		slog.Warn("lending: pricewatch set watermark failed", "error", err)
	}
}

// runPoll polls oracle prices for the fallback assets and Benqi markets.
func (p *PriceWatch) runPoll(ctx context.Context) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *PriceWatch) pollOnce(ctx context.Context) {
	p.mu.Lock()
	aaveOracle := p.aaveOracle
	aaveAssets := append([]string(nil), p.pollAaveAssets...)
	benqiOracle := p.benqiOracle
	benqiMarkets := append([]string(nil), p.benqiMarkets...)
	p.mu.Unlock()

	if aaveOracle != "" && len(aaveAssets) > 0 {
		res, err := p.rpc.EthCall(ctx, aaveOracle, EncodeCall1AddrArray("getAssetsPrices(address[])", aaveAssets), "latest")
		if err == nil {
			prices := DecodeUintArray(decodeHex(res))
			for i, asset := range aaveAssets {
				if i < len(prices) {
					p.checkMove(ctx, ProtocolAaveV3, asset, prices[i])
				}
			}
		}
	}

	for _, m := range benqiMarkets {
		res, err := p.rpc.EthCall(ctx, benqiOracle, EncodeCall1Addr("getUnderlyingPrice(address)", m), "latest")
		if err != nil {
			continue
		}
		p.checkMove(ctx, ProtocolBenqi, m, Word(decodeHex(res), 0))
	}
}

// checkMove compares a price to the last observed value and recomputes the
// exposed positions when it moves more than the threshold.
func (p *PriceWatch) checkMove(ctx context.Context, proto Protocol, asset string, price *big.Int) {
	if price == nil || price.Sign() == 0 {
		return
	}
	key := string(proto) + "|" + NormalizeAddr(asset)
	p.mu.Lock()
	last := p.lastPrice[key]
	p.lastPrice[key] = price
	p.mu.Unlock()

	if last == nil || last.Sign() == 0 {
		return // first observation, nothing to compare
	}
	diff := new(big.Int).Sub(price, last)
	diff.Abs(diff)
	moveBps := new(big.Int).Div(new(big.Int).Mul(diff, big.NewInt(10000)), last)
	if moveBps.Int64() < p.moveThresholdBps {
		return
	}
	p.engine.RecomputeAsset(ctx, proto, NormalizeAddr(asset))
}

func (p *PriceWatch) callAddr(ctx context.Context, to, data string) (string, error) {
	res, err := p.rpc.EthCall(ctx, to, data, "latest")
	if err != nil {
		return "", err
	}
	return Addr(decodeHex(res), 0), nil
}
