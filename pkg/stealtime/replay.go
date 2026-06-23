package stealtime

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/libevm/common"

	"icicle/pkg/kmeasure"
	"icicle/pkg/lending"
	"icicle/pkg/lending/aave"
	"icicle/pkg/lending/benqi"
	"icicle/pkg/prefilter"
)

// ReplayConfig configures the crash-day capture replay.
type ReplayConfig struct {
	ChainID          uint32
	ArchiveRPC       string
	FallbackRPC      string
	TopDays          int      // crash days to take, by sized-liquidation count
	MinSizeUSD1e18   *big.Int // sized threshold (repaid debt), default $1000
	GasUnits         uint64
	MinProfitUSD1e18 *big.Int
}

type replayLiq struct {
	liq       Liquidation
	crossing  uint64
	stealTime uint64
	day       string
}

type replayResult struct {
	day          string
	protocol     string
	liquidator   common.Address
	stealTime    uint64
	profitableV2 bool
	netV2        *big.Int
	netFull      *big.Int // V2 plus Liquidity Book, for the gap only
	sizeBucket   string
}

var reactionBudgets = []uint64{1, 2, 3, 5}

// Replay re-evaluates crash-day sized liquidations on the executable V2 venue with
// the actual crash-day base fee, and models would-be capture under a reaction
// budget. It reads the liquidation set from stealtime_results (no re-enumeration),
// but re-quotes and re-costs everything block-pinned and fresh, since the stored
// net may have been computed on a different venue or gas basis.
func Replay(ctx context.Context, conn driver.Conn, cfg ReplayConfig) error {
	rpc := lending.NewClient(cfg.ArchiveRPC, cfg.FallbackRPC)
	resolver := kmeasure.NewChainResolver(rpc)

	aaveAd := aave.New("")
	benqiAd := benqi.New("")
	aaveAddrs, aaveParams, aaveGlobals, err := bootstrap(ctx, rpc, aaveAd)
	if err != nil {
		return fmt.Errorf("bootstrap aave: %w", err)
	}
	benqiAddrs, benqiParams, benqiGlobals, err := bootstrap(ctx, rpc, benqiAd)
	if err != nil {
		return fmt.Errorf("bootstrap benqi: %w", err)
	}
	aavePool := common.HexToAddress(aaveAddrs.Pool)
	benqiComptroller := common.HexToAddress(benqiAddrs.Comptroller)
	addrRes := newAddrResolver(rpc, benqiComptroller, 0)

	liqs, windowDays, err := loadCrashDayLiquidations(ctx, conn, cfg)
	if err != nil {
		return fmt.Errorf("load crash-day liquidations: %w", err)
	}
	slog.Info("stealtime replay: loaded crash-day sized liquidations", "count", len(liqs), "window_days", int(windowDays))

	var results []replayResult
	for i, rl := range liqs {
		if i%200 == 0 && i > 0 {
			slog.Info("stealtime replay: progress", "done", i, "of", len(liqs))
		}

		blockAddrs := addrRes.at(ctx, rl.liq.Protocol, rl.crossing)
		aaveDataProvider := common.HexToAddress(blockAddrs.DataProvider)
		adapter := lending.Adapter(aaveAd)
		if rl.liq.Protocol == "benqi" {
			adapter = benqiAd
			benqiAd.Configure(blockAddrs, benqiParams, benqiGlobals)
		} else {
			aaveAd.Configure(blockAddrs, aaveParams, aaveGlobals)
		}

		pos, ok := assemblePosition(ctx, rpc, adapter, resolver, rl.liq.Protocol, rl.liq, rl.crossing)
		if !ok {
			continue
		}
		params := newBlockParams(ctx, rpc, rl.crossing, aaveDataProvider, benqiComptroller)
		// Crash-day gas: base fee at the opportunity block (cost.go reads BlockBaseFee).
		cost := buildBlockCost(ctx, rpc, rl.crossing, aavePool, cfg.GasUnits, cfg.MinProfitUSD1e18)

		resV2, err := prefilter.EvaluatePosition(ctx, pos, params, newBlockQuoterV2(rpc, rl.crossing), cost)
		if err != nil {
			continue
		}
		resFull, err := prefilter.EvaluatePosition(ctx, pos, params, newBlockQuoter(rpc, rl.crossing), cost)
		if err != nil {
			resFull = resV2
		}

		results = append(results, replayResult{
			day: rl.day, protocol: rl.liq.Protocol, liquidator: rl.liq.Liquidator, stealTime: rl.stealTime,
			profitableV2: resV2.Profitable, netV2: orZero(resV2.NetProfitUSD), netFull: orZero(resFull.NetProfitUSD),
			sizeBucket: SizeBucketFor(resV2.NetProfitUSD),
		})
	}

	replayReport(results, windowDays, cfg)
	return nil
}

func loadCrashDayLiquidations(ctx context.Context, conn driver.Conn, cfg ReplayConfig) ([]replayLiq, float64, error) {
	var minB, maxB uint64
	if err := conn.QueryRow(ctx, `SELECT min(taken_block), max(taken_block) FROM stealtime_results WHERE chain_id = ?`, cfg.ChainID).Scan(&minB, &maxB); err != nil {
		return nil, 0, err
	}
	windowDays := float64(maxB-minB) * 2 / 86400

	size := cfg.MinSizeUSD1e18.String()
	q := fmt.Sprintf(`
		WITH crash AS (
			SELECT toDate(b.block_time) AS day
			FROM stealtime_results s
			INNER JOIN raw_blocks b ON b.chain_id = s.chain_id AND b.block_number = toUInt32(s.taken_block)
			WHERE s.chain_id = ? AND s.repaid_usd > %s
			GROUP BY day ORDER BY count() DESC LIMIT %d
		)
		SELECT s.protocol, s.account, s.liquidator, s.collateral_asset, s.debt_asset,
			s.taken_block, s.crossing_block, s.steal_time, toString(toDate(b.block_time))
		FROM stealtime_results s
		INNER JOIN raw_blocks b ON b.chain_id = s.chain_id AND b.block_number = toUInt32(s.taken_block)
		WHERE s.chain_id = ? AND s.evaluated AND s.repaid_usd > %s
			AND toDate(b.block_time) IN (SELECT day FROM crash)
	`, size, cfg.TopDays, size)

	rows, err := conn.Query(ctx, q, cfg.ChainID, cfg.ChainID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []replayLiq
	for rows.Next() {
		var protocol, day string
		var account, liquidator, collateral, debt [20]byte
		var taken, crossing, steal uint64
		if err := rows.Scan(&protocol, &account, &liquidator, &collateral, &debt, &taken, &crossing, &steal, &day); err != nil {
			return nil, 0, err
		}
		out = append(out, replayLiq{
			liq: Liquidation{
				Protocol: protocol, Account: common.BytesToAddress(account[:]), Liquidator: common.BytesToAddress(liquidator[:]),
				CollateralAsset: common.BytesToAddress(collateral[:]), DebtAsset: common.BytesToAddress(debt[:]), TakenBlock: taken,
			},
			crossing: crossing, stealTime: steal, day: day,
		})
	}
	return out, windowDays, rows.Err()
}

func replayReport(results []replayResult, windowDays float64, cfg ReplayConfig) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== crash-day capture replay (V2-executable venue, crash-day gas) ===\n")
	fmt.Fprintf(&b, "evaluated=%d  window_days=%d  min_profit=$%.0f  gas_units=%d\n",
		len(results), int(windowDays), usdFloat(cfg.MinProfitUSD1e18), cfg.GasUnits)

	profV2 := 0
	for _, r := range results {
		if r.profitableV2 {
			profV2++
		}
	}
	fmt.Fprintf(&b, "profitable on V2 (sized, crash-day gas)=%d of %d\n\n", profV2, len(results))

	fmt.Fprintf(&b, "Step 3: would-be capture by reaction budget R (UPPER BOUND: assumes ready-first wins)\n")
	for _, R := range reactionBudgets {
		win, sum := 0, big.NewInt(0)
		byProto := map[string]int{}
		bySize := map[string]int{}
		for _, r := range results {
			if r.profitableV2 && r.stealTime > R {
				win++
				sum = new(big.Int).Add(sum, r.netV2)
				byProto[r.protocol]++
				bySize[r.sizeBucket]++
			}
		}
		rate := 0.0
		if profV2 > 0 {
			rate = float64(win) / float64(profV2)
		}
		fmt.Fprintf(&b, "  R=%d: winnable=%d  capture=$%.0f  rate=%.1f%%  (aave=%d benqi=%d | small=%d medium=%d large=%d)\n",
			R, win, usdFloat(sum), rate*100, byProto["aave-v3"], byProto["benqi"], bySize["small"], bySize["medium"], bySize["large"])
	}

	// Step 6: V2 vs LB gap on the R=2 winnable set.
	winV2, winFull := big.NewInt(0), big.NewInt(0)
	for _, r := range results {
		if r.profitableV2 && r.stealTime > 2 {
			winV2 = new(big.Int).Add(winV2, r.netV2)
			winFull = new(big.Int).Add(winFull, r.netFull)
		}
	}
	gap := new(big.Int).Sub(winFull, winV2)
	fmt.Fprintf(&b, "\nStep 6: venue gap on R=2 winnable set: V2=$%.0f  V2+LB=$%.0f  LB_adds=$%.0f (%.0f%%)\n",
		usdFloat(winV2), usdFloat(winFull), usdFloat(gap), pct(gap, winV2))

	// Annualize R=2 V2 capture with explicit haircuts.
	annual := 0.0
	if windowDays > 0 {
		annual = usdFloat(winV2) * 365 / windowDays
	}
	fmt.Fprintf(&b, "\nAnnualized V2 capture at R=2 (window=%dd): upper_bound=$%.0f/yr\n", int(windowDays), annual)
	fmt.Fprintf(&b, "  after win-fraction haircut: 50%% -> $%.0f/yr   25%% -> $%.0f/yr   10%% -> $%.0f/yr\n",
		annual*0.5, annual*0.25, annual*0.1)
	fmt.Print(b.String())
}

func orZero(n *big.Int) *big.Int {
	if n == nil {
		return big.NewInt(0)
	}
	return n
}

func pct(part, whole *big.Int) float64 {
	if whole == nil || whole.Sign() == 0 {
		return 0
	}
	p := new(big.Float).Quo(new(big.Float).SetInt(part), new(big.Float).SetInt(whole))
	v, _ := p.Float64()
	return v * 100
}
