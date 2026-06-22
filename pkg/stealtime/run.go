package stealtime

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/libevm/common"

	"icicle/pkg/kmeasure"
	"icicle/pkg/lending"
	"icicle/pkg/lending/aave"
	"icicle/pkg/lending/benqi"
	"icicle/pkg/prefilter"
)

// Config configures the steal-time backtest.
type Config struct {
	ChainID           uint32
	ArchiveRPC        string
	FallbackRPC       string
	FromBlock         uint64
	ToBlock           uint64
	MaxLookbackBlocks uint64
	MinProfitUSD1e18  *big.Int
	GasUnits          uint64
	TopN              int
	Persist           bool
}

// Run executes the backtest over the configured block range.
func Run(ctx context.Context, conn driver.Conn, cfg Config) error {
	start := time.Now()
	rpc := lending.NewClient(cfg.ArchiveRPC, cfg.FallbackRPC)
	store, err := lending.NewStore(conn, cfg.ChainID)
	if err != nil {
		return err
	}
	resolver := kmeasure.NewChainResolver(rpc)

	aaveAd := aave.New("")
	benqiAd := benqi.New("")
	aaveAddrs, err := bootstrap(ctx, rpc, aaveAd)
	if err != nil {
		return fmt.Errorf("bootstrap aave: %w", err)
	}
	benqiAddrs, err := bootstrap(ctx, rpc, benqiAd)
	if err != nil {
		return fmt.Errorf("bootstrap benqi: %w", err)
	}

	aavePool := common.HexToAddress(aaveAddrs.Pool)
	aaveOracle := common.HexToAddress(aaveAddrs.Oracle)
	aaveDataProvider := common.HexToAddress(aaveAddrs.DataProvider)
	benqiComptroller := common.HexToAddress(benqiAddrs.Comptroller)

	if err := ensureSchema(ctx, conn); err != nil {
		return err
	}

	liqs, err := Enumerate(ctx, store, aaveAddrs.Pool, benqiAddrs.Markets, cfg.FromBlock, cfg.ToBlock)
	if err != nil {
		return fmt.Errorf("enumerate: %w", err)
	}
	slog.Info("stealtime: enumerated liquidations", "count", len(liqs), "from", cfg.FromBlock, "to", cfg.ToBlock)

	aggCache := map[common.Address]common.Address{}
	reasons := map[prefilter.Reason]int{}
	var obs []Observation
	var scanned, profitable, quoterCalls, quoterFails int

	for _, liq := range liqs {
		scanned++
		adapter := lending.Adapter(aaveAd)
		poolOrComp := aavePool
		if liq.Protocol == "benqi" {
			adapter = benqiAd
			poolOrComp = benqiComptroller
		}

		collInfo, _ := resolver.Resolve(ctx, liq.Protocol, liq.CollateralAsset)
		debtInfo, _ := resolver.Resolve(ctx, liq.Protocol, liq.DebtAsset)
		assets := []common.Address{collInfo.Underlying, debtInfo.Underlying}

		floor := uint64(0)
		if liq.TakenBlock > cfg.MaxLookbackBlocks {
			floor = liq.TakenBlock - cfg.MaxLookbackBlocks
		}

		cands, err := gatherCandidates(ctx, conn, rpc, cfg.ChainID, aaveOracle, aavePool, liq.Account, liq.Protocol, assets, floor, liq.TakenBlock, aggCache)
		if err != nil {
			slog.Warn("stealtime: gather candidates failed", "account", liq.Account.Hex(), "error", err)
			continue
		}

		crossing, err := FindCrossing(cands, liq.TakenBlock, floor, func(b uint64) (bool, error) {
			return liquidatableAtBlock(ctx, rpc, liq.Protocol, liq.Account, poolOrComp, b)
		})
		if err != nil {
			slog.Warn("stealtime: find crossing failed", "account", liq.Account.Hex(), "error", err)
			continue
		}

		pos, ok := assemblePosition(ctx, rpc, adapter, resolver, liq.Protocol, liq, crossing.CrossingBlock)
		if !ok {
			continue
		}
		params := newBlockParams(ctx, rpc, crossing.CrossingBlock, aaveDataProvider, benqiComptroller)
		quoter := newBlockQuoter(rpc, crossing.CrossingBlock)
		cost := buildBlockCost(ctx, rpc, crossing.CrossingBlock, aavePool, cfg.GasUnits, cfg.MinProfitUSD1e18)

		res, err := prefilter.EvaluatePosition(ctx, pos, params, quoter, cost)
		qc, qf := quoter.Stats()
		quoterCalls += qc
		quoterFails += qf
		if err != nil {
			slog.Warn("stealtime: evaluate failed", "account", liq.Account.Hex(), "error", err)
			continue
		}
		reasons[res.Reason]++

		steal := StealTime(liq.TakenBlock, crossing)
		if res.Profitable {
			profitable++
			obs = append(obs, Observation{
				Account: liq.Account, Liquidator: liq.Liquidator, Protocol: liq.Protocol,
				StealTime: steal, Censored: crossing.Censored, NetProfitUSD: res.NetProfitUSD,
				SizeBucket: SizeBucketFor(res.NetProfitUSD), TakenBlock: liq.TakenBlock,
			})
		}
		if cfg.Persist {
			if err := persistRow(ctx, conn, cfg.ChainID, liq, crossing, steal, res); err != nil {
				slog.Warn("stealtime: persist failed", "error", err)
			}
		}
	}

	dist := Aggregate(obs, cfg.TopN)
	report(dist, reasons, scanned, profitable, quoterCalls, quoterFails, time.Since(start))

	slog.Info("stealtime: run complete",
		"liquidations_scanned", scanned, "profitable", profitable,
		"quoter_calls", quoterCalls, "quoter_failures", quoterFails, "duration", time.Since(start))
	return nil
}

// bootstrap resolves and configures an adapter at head. Addresses are injected
// before RefreshParams, which reads per-asset config from them.
func bootstrap(ctx context.Context, rpc *lending.Client, a lending.Adapter) (lending.Addresses, error) {
	addrs, _, err := a.Resolve(ctx, rpc)
	if err != nil {
		return lending.Addresses{}, err
	}
	a.Configure(addrs, nil, lending.GlobalParams{})
	params, globals, err := a.RefreshParams(ctx, rpc)
	if err != nil {
		return lending.Addresses{}, err
	}
	a.Configure(addrs, params, globals)
	return addrs, nil
}

func ensureSchema(ctx context.Context, conn driver.Conn) error {
	return conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS stealtime_results (
		run_at DateTime64(3, 'UTC') DEFAULT now64(3),
		chain_id UInt32,
		protocol LowCardinality(String),
		account FixedString(20),
		liquidator FixedString(20),
		taken_block UInt64,
		crossing_block UInt64,
		steal_time UInt64,
		censored Bool,
		profitable Bool,
		reason LowCardinality(String),
		net_profit_usd UInt256,
		size_bucket LowCardinality(String)
	) ENGINE = MergeTree ORDER BY (chain_id, taken_block)`)
}

func persistRow(ctx context.Context, conn driver.Conn, chainID uint32, liq Liquidation, c CrossingResult, steal uint64, res prefilter.Result) error {
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO stealtime_results (
		run_at, chain_id, protocol, account, liquidator, taken_block, crossing_block,
		steal_time, censored, profitable, reason, net_profit_usd, size_bucket
	)`)
	if err != nil {
		return err
	}
	net := res.NetProfitUSD
	if net == nil {
		net = big.NewInt(0)
	}
	if err := batch.Append(
		time.Now().UTC(), chainID, liq.Protocol, liq.Account.Bytes(), liq.Liquidator.Bytes(),
		liq.TakenBlock, c.CrossingBlock, steal, c.Censored, res.Profitable, string(res.Reason),
		net, SizeBucketFor(net),
	); err != nil {
		return err
	}
	return batch.Send()
}

func report(d Distribution, reasons map[prefilter.Reason]int, scanned, profitable, qCalls, qFails int, dur time.Duration) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== steal-time backtest ===\n")
	fmt.Fprintf(&b, "scanned=%d profitable=%d quoter_calls=%d quoter_failures=%d duration=%s\n",
		scanned, profitable, qCalls, qFails, dur.Round(time.Millisecond))
	fmt.Fprintf(&b, "reasons: profitable=%d dust=%d illiquid=%d bad_debt=%d no_pair=%d unprofitable=%d\n",
		reasons[prefilter.ReasonProfitable], reasons[prefilter.ReasonDust], reasons[prefilter.ReasonIlliquid],
		reasons[prefilter.ReasonBadDebt], reasons[prefilter.ReasonNoPair], reasons[prefilter.ReasonUnprofit])

	fmt.Fprintf(&b, "\nprofitable liquidations: %d (censored: %d)\n", d.Total, d.Censored)
	fmt.Fprintf(&b, "steal_time blocks: median=%d p90=%d  within_0to2=%.0f%%  beyond_10=%.0f%%\n",
		d.MedianBlocks, d.P90Blocks, d.WithinTwo*100, d.BeyondTen*100)
	fmt.Fprintf(&b, "histogram: 0=%d 1=%d 2=%d 3to5=%d 6to10=%d 11to20=%d 21plus=%d censored=%d\n",
		d.Overall.B0, d.Overall.B1, d.Overall.B2, d.Overall.B3to5, d.Overall.B6to10, d.Overall.B11to20, d.Overall.B21plus, d.Overall.Censored)

	for proto, h := range d.ByProtocol {
		fmt.Fprintf(&b, "  %-8s 0=%d 1=%d 2=%d 3to5=%d 6to10=%d 11to20=%d 21plus=%d censored=%d\n",
			proto, h.B0, h.B1, h.B2, h.B3to5, h.B6to10, h.B11to20, h.B21plus, h.Censored)
	}
	for size, h := range d.BySize {
		fmt.Fprintf(&b, "  size=%-6s 0=%d 1=%d 2=%d 3to5=%d 6to10=%d 11to20=%d 21plus=%d censored=%d\n",
			size, h.B0, h.B1, h.B2, h.B3to5, h.B6to10, h.B11to20, h.B21plus, h.Censored)
	}

	fmt.Fprintf(&b, "total realized profit: $%.2f\n", usdFloat(d.TotalProfit))
	fmt.Fprintf(&b, "incumbent concentration: top-%d share=%.0f%%\n", len(d.TopLiquidators), d.TopNShare*100)
	for _, it := range d.TopLiquidators {
		fmt.Fprintf(&b, "  %s  %d liquidations\n", it.Liquidator.Hex(), it.Count)
	}
	fmt.Print(b.String())
}

func usdFloat(n *big.Int) float64 {
	if n == nil {
		return 0
	}
	f := new(big.Float).Quo(new(big.Float).SetInt(n), new(big.Float).SetInt(wad))
	v, _ := f.Float64()
	return v
}
