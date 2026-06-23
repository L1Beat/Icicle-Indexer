package stealtime

import (
	"context"
	"fmt"
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

// FixturesConfig configures the per-venue fork-test fixture extraction.
type FixturesConfig struct {
	ChainID     uint32
	ArchiveRPC  string
	FallbackRPC string
	Label       string // replay_results label to pick from (e.g. real_oct)
	Protocol    string // optional: also emit a protocol-scoped fixture (e.g. benqi)
	MinSteal    uint64 // protocol fixture: minimum steal_time so the case is fork-replayable
}

type fixtureTarget struct {
	prefix       string
	venue        string
	unprofitable bool
}

// One fixture per venue plus an unprofitable one for the revert test. The three
// venue fixtures are chosen by the winning route recorded in replay_results, so
// each venue is genuinely exercised rather than all routing the same place.
var fixtureTargets = []fixtureTarget{
	{"FIX_LB", "lb", false},
	{"FIX_UNIV3", "univ3", false},
	{"FIX_PHARAOH", "pharaoh", false},
	{"FIX_UNPROFIT", "", true},
}

type fixtureRow struct {
	protocol         string
	account          common.Address
	collateral, debt common.Address
	crossing, taken  uint64
	stealTime        uint64
	sizeBucket       string
	netReal          *big.Int
	repaidUSD        *big.Int
	storedVenue      string
}

// Fixtures extracts one fork-test fixture per venue from replay_results, pulling
// the native repay and seized amounts and the source tx hash from the raw
// liquidation event, and re-confirming the winning venue live by re-quoting at the
// fork block. Read-only: no keys, no writes, no submission.
func Fixtures(ctx context.Context, conn driver.Conn, cfg FixturesConfig) error {
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
	minProfit := new(big.Int).Mul(big.NewInt(5), lending.WAD)

	var out strings.Builder
	fmt.Fprintf(&out, "\n=== fork-test fixtures (label=%q, read-only) ===\n\n", cfg.Label)

	for _, t := range fixtureTargets {
		row, ok, err := selectFixture(ctx, conn, cfg, t)
		if err != nil {
			return fmt.Errorf("select %s: %w", t.prefix, err)
		}
		if !ok {
			if t.venue == "pharaoh" {
				fmt.Fprintf(&out, "# %s: no profitable liquidation routes Pharaoh in this window. Pharaoh fork test DEFERRED.\n\n", t.prefix)
			} else {
				fmt.Fprintf(&out, "# %s: no candidate found for venue %q in label %q.\n\n", t.prefix, t.venue, cfg.Label)
			}
			continue
		}

		txHash, repay, seized, ok := fetchLiquidationEvent(ctx, conn, cfg.ChainID, aavePool, row)
		if !ok {
			fmt.Fprintf(&out, "# %s: raw liquidation event not found for %s @ block %d (skipped).\n\n", t.prefix, row.account.Hex(), row.taken)
			continue
		}

		// Re-confirm the winning venue live at the fork block.
		confirmed, _, _ := confirmVenue(ctx, rpc, resolver, addrRes, row,
			aaveAd, benqiAd, aaveParams, aaveGlobals, benqiParams, benqiGlobals,
			benqiComptroller, aavePool, minProfit)

		venueLabel := t.venue
		if t.unprofitable {
			venueLabel = confirmed // record the route the unprofitable swap would take
		}

		fmt.Fprintf(&out, "# %s  protocol=%s  size=%s  net_real=$%.2f  stored_venue=%s  confirmed_venue=%s\n",
			t.prefix, row.protocol, row.sizeBucket, usdFloat(row.netReal), row.storedVenue, confirmed)
		if !t.unprofitable && confirmed != t.venue {
			fmt.Fprintf(&out, "#   WARNING: live re-quote winner (%s) != target venue (%s); pick another candidate.\n", confirmed, t.venue)
		}
		fmt.Fprintf(&out, "%s_FORK_BLOCK=%d\n", t.prefix, row.crossing)
		fmt.Fprintf(&out, "%s_ACCOUNT=%s\n", t.prefix, row.account.Hex())
		fmt.Fprintf(&out, "%s_PROTOCOL=%s\n", t.prefix, row.protocol)
		fmt.Fprintf(&out, "%s_DEBT_ASSET=%s\n", t.prefix, row.debt.Hex())
		fmt.Fprintf(&out, "%s_COLLATERAL_ASSET=%s\n", t.prefix, row.collateral.Hex())
		fmt.Fprintf(&out, "%s_REPAY_AMOUNT=%s\n", t.prefix, repay.String())
		fmt.Fprintf(&out, "%s_SEIZED_AMOUNT=%s\n", t.prefix, seized.String())
		fmt.Fprintf(&out, "%s_WIN_VENUE=%s\n", t.prefix, venueLabel)
		fmt.Fprintf(&out, "%s_TX_HASH=0x%s\n", t.prefix, txHash)
		fmt.Fprintf(&out, "# verify: https://snowtrace.io/tx/0x%s\n\n", txHash)
	}

	// Optional protocol-scoped fixture (e.g. Benqi), to exercise the full path the
	// venue-chosen fixtures miss: liquidateBorrow + seizeTokens + redeem + CL swap +
	// positive profit. A candidate qualifies only if it is fork-replayable (crossing
	// strictly before taken AND shortfall > 0 at the crossing block on-chain) AND its
	// collateral unwind routes a real CL venue AND is profitable after gas at that
	// block. Candidates are ranked by repaid size; the qiAVAX/qiUSDC redeem shape is
	// tried first, then widened to any qiToken collateral if none qualifies.
	if cfg.Protocol != "" {
		prefix := "FIX_" + strings.ToUpper(cfg.Protocol)

		pick := func(cands []fixtureRow) (fixtureRow, string, *big.Int, *big.Int, bool) {
			for _, row := range cands {
				if row.crossing >= row.taken {
					continue // crossing must precede the liquidation tx
				}
				shortfall := benqiShortfallAt(ctx, rpc, benqiComptroller, row.account, row.crossing)
				if shortfall == nil || shortfall.Sign() <= 0 {
					continue // borrower not in shortfall at the fork block
				}
				venue, net, profitable := confirmVenue(ctx, rpc, resolver, addrRes, row,
					aaveAd, benqiAd, aaveParams, aaveGlobals, benqiParams, benqiGlobals,
					benqiComptroller, aavePool, minProfit)
				if !isCLVenue(venue) || !profitable {
					continue // must route a real CL venue and clear gas
				}
				return row, venue, net, shortfall, true
			}
			return fixtureRow{}, "", nil, nil, false
		}

		shaped, err := selectProtocolCandidates(ctx, conn, cfg, true)
		if err != nil {
			return fmt.Errorf("select %s: %w", prefix, err)
		}
		row, venue, net, shortfall, ok := pick(shaped)
		widened := false
		if !ok {
			any, err := selectProtocolCandidates(ctx, conn, cfg, false)
			if err != nil {
				return fmt.Errorf("select %s (widened): %w", prefix, err)
			}
			row, venue, net, shortfall, ok = pick(any)
			widened = true
		}

		if !ok {
			fmt.Fprintf(&out, "# %s: no Benqi liquidation is fork-replayable AND CL-routable AND profitable at size (steal_time >= %d). Tried qiAVAX/qiUSDC then any qiToken collateral.\n\n", prefix, cfg.MinSteal)
		} else if txHash, repay, seized, found := fetchLiquidationEvent(ctx, conn, cfg.ChainID, aavePool, row); !found {
			fmt.Fprintf(&out, "# %s: raw liquidation event not found for %s @ block %d.\n\n", prefix, row.account.Hex(), row.taken)
		} else {
			collNative := row.collateral.Hex()
			if info, rerr := resolver.Resolve(ctx, row.protocol, row.collateral); rerr == nil {
				collNative = info.Underlying.Hex()
			}
			if widened {
				fmt.Fprintf(&out, "# %s: NOTE: no qiAVAX/qiUSDC case qualified at size; WIDENED to any qiToken collateral. Redeem-into-profitable-CL-swap is exercised, but not the exact qiAVAX shape.\n", prefix)
			}
			fmt.Fprintf(&out, "# %s  protocol=%s  size=%s  repaid=$%.2f  steal_time=%d blocks  shortfall_at_fork=%s (>0, replayable)  route=%s  net=$%.2f (profitable after gas)\n",
				prefix, row.protocol, row.sizeBucket, usdFloat(row.repaidUSD), row.stealTime, shortfall.String(), venue, usdFloat(net))
			fmt.Fprintf(&out, "# fork block %d is strictly before taken block %d. DEBT_MARKET/COLL_MARKET are qiToken markets; COLL_NATIVE is the resolved underlying. SEIZE is qiToken units (seizeTokens), DEBT_TO_COVER is underlying debt units.\n", row.crossing, row.taken)
			fmt.Fprintf(&out, "%s_FORK_BLOCK=%d\n", prefix, row.crossing)
			fmt.Fprintf(&out, "%s_ACCOUNT=%s\n", prefix, row.account.Hex())
			fmt.Fprintf(&out, "%s_PROTOCOL=%s\n", prefix, row.protocol)
			fmt.Fprintf(&out, "%s_DEBT_MARKET=%s\n", prefix, row.debt.Hex())
			fmt.Fprintf(&out, "%s_COLL_MARKET=%s\n", prefix, row.collateral.Hex())
			fmt.Fprintf(&out, "%s_COLL_NATIVE=%s\n", prefix, collNative)
			fmt.Fprintf(&out, "%s_DEBT_TO_COVER=%s\n", prefix, repay.String())
			fmt.Fprintf(&out, "%s_SEIZE=%s\n", prefix, seized.String())
			fmt.Fprintf(&out, "%s_WIN_VENUE=%s\n", prefix, venue)
			fmt.Fprintf(&out, "%s_TX_HASH=0x%s\n", prefix, txHash)
			fmt.Fprintf(&out, "# verify: https://snowtrace.io/tx/0x%s\n\n", txHash)
		}
	}

	// Pharaoh quoter/router, verified live on-chain via getCode.
	fmt.Fprintf(&out, "# Pharaoh CL venue addresses (verified on-chain below)\n")
	fmt.Fprintf(&out, "FIX_PHARAOH_QUOTER=%s\n", pharaohV3.quoter.Hex())
	fmt.Fprintf(&out, "FIX_PHARAOH_ROUTER=%s\n", pharaohRouter.Hex())
	fmt.Fprintf(&out, "# on-chain code: quoter=%s  router=%s\n", codeStatus(ctx, rpc, pharaohV3.quoter), codeStatus(ctx, rpc, pharaohRouter))

	fmt.Print(out.String())
	return nil
}

// selectFixture picks the strongest candidate for a target. For a venue it takes
// the highest real-venue net liquidation whose recorded route is that venue; for
// the unprofitable target it takes the largest-repaid liquidation that is not
// profitable at execution yet still has a real route (so the swap runs and the
// bundle reverts on the cost check).
func selectFixture(ctx context.Context, conn driver.Conn, cfg FixturesConfig, t fixtureTarget) (fixtureRow, bool, error) {
	var q string
	var args []any
	if t.unprofitable {
		q = `
			SELECT r.protocol, r.account, r.collateral_asset, r.debt_asset,
				r.crossing_block, r.taken_block, r.size_bucket, r.net_real, r.win_venue
			FROM replay_results r
			INNER JOIN (
				SELECT account, taken_block, max(repaid_usd) AS repaid
				FROM stealtime_results WHERE chain_id = ? GROUP BY account, taken_block
			) s ON s.account = r.account AND s.taken_block = r.taken_block
			WHERE r.label = ? AND r.chain_id = ? AND r.profitable_real = 0 AND r.win_venue != 'none'
			ORDER BY s.repaid DESC LIMIT 1`
		args = []any{cfg.ChainID, cfg.Label, cfg.ChainID}
	} else {
		q = `
			SELECT protocol, account, collateral_asset, debt_asset,
				crossing_block, taken_block, size_bucket, net_real, win_venue
			FROM replay_results
			WHERE label = ? AND chain_id = ? AND profitable_real AND win_venue = ?
			ORDER BY net_real DESC LIMIT 1`
		args = []any{cfg.Label, cfg.ChainID, t.venue}
	}

	var r fixtureRow
	var acc, coll, debt [20]byte
	row := conn.QueryRow(ctx, q, args...)
	err := row.Scan(&r.protocol, &acc, &coll, &debt, &r.crossing, &r.taken, &r.sizeBucket, &r.netReal, &r.storedVenue)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return fixtureRow{}, false, nil
		}
		return fixtureRow{}, false, err
	}
	r.account = common.BytesToAddress(acc[:])
	r.collateral = common.BytesToAddress(coll[:])
	r.debt = common.BytesToAddress(debt[:])
	return r, true, nil
}

// selectProtocolCandidates returns fork-replayable candidates for one protocol,
// ranked by repaid size (largest first) so the caller can pick the biggest case
// that also routes a CL venue and clears gas. When shapeOnly is set it restricts to
// the qiAVAX-collateral / qiUSDC-debt redeem shape (the native-AVAX path worth
// testing, since qiAVAX has no underlying() view); otherwise it spans any
// collateral for that protocol. Sourced from stealtime_results (the full evaluated
// set, which carries steal_time) for a large pool. The caller verifies shortfall>0
// at the crossing block on-chain, so only genuinely replayable cases survive.
func selectProtocolCandidates(ctx context.Context, conn driver.Conn, cfg FixturesConfig, shapeOnly bool) ([]fixtureRow, error) {
	const (
		qiAVAX = "5c0401e81bc07ca70fad469b451682c0d747ef1c"
		qiUSDC = "b715808a78f6041e46d61cb123c9b4a27056ae9c"
	)
	shapeClause := ""
	if shapeOnly {
		shapeClause = fmt.Sprintf(" AND collateral_asset = unhex('%s') AND debt_asset = unhex('%s')", qiAVAX, qiUSDC)
	}
	q := fmt.Sprintf(`
		SELECT protocol, account, collateral_asset, debt_asset,
			crossing_block, taken_block, steal_time, size_bucket, net_profit_usd, repaid_usd
		FROM stealtime_results
		WHERE chain_id = ? AND protocol = ? AND evaluated AND steal_time >= ?%s
		ORDER BY repaid_usd DESC, steal_time DESC
		LIMIT 40`, shapeClause)

	rows, err := conn.Query(ctx, q, cfg.ChainID, cfg.Protocol, cfg.MinSteal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []fixtureRow
	for rows.Next() {
		var r fixtureRow
		var acc, coll, debt [20]byte
		if err := rows.Scan(&r.protocol, &acc, &coll, &debt, &r.crossing, &r.taken, &r.stealTime, &r.sizeBucket, &r.netReal, &r.repaidUSD); err != nil {
			return nil, err
		}
		r.account = common.BytesToAddress(acc[:])
		r.collateral = common.BytesToAddress(coll[:])
		r.debt = common.BytesToAddress(debt[:])
		out = append(out, r)
	}
	return out, rows.Err()
}

func isCLVenue(v string) bool {
	return v == "lb" || v == "univ3" || v == "pharaoh"
}

// benqiShortfallAt reads Comptroller.getAccountLiquidity(account) at a historical
// block and returns the shortfall (return word 2). A positive value means the
// borrower is liquidatable at that block, i.e. the fixture is fork-replayable.
func benqiShortfallAt(ctx context.Context, rpc *lending.Client, comptroller, account common.Address, block uint64) *big.Int {
	res, err := rpc.EthCall(ctx, comptroller.Hex(), lending.EncodeCall1Addr("getAccountLiquidity(address)", account.Hex()), blockHex(block))
	if err != nil {
		return nil
	}
	return lending.Word(lending.DecodeHexBytes(res), 2)
}

// fetchLiquidationEvent reads the raw liquidation event for a fixture and returns
// its tx hash and the NATIVE repay and seized amounts (Aave debtToCover /
// liquidatedCollateralAmount; Benqi repayAmount / seizeTokens), straight from the
// event data words.
func fetchLiquidationEvent(ctx context.Context, conn driver.Conn, chainID uint32, aavePool common.Address, r fixtureRow) (txHash string, repay, seized *big.Int, ok bool) {
	if r.protocol == "benqi" {
		// Benqi LiquidateBorrow is unindexed; the debt market is the emitting log
		// address. Match borrower and collateral market in the data words.
		q := `SELECT lower(hex(transaction_hash)), data FROM raw_logs
			WHERE chain_id = ? AND block_number = ? AND topic0 = unhex(?) AND address = unhex(?)`
		rows, err := conn.Query(ctx, q, chainID, r.taken, strip0x(topicBenqiLiquidation), hexNo0x(r.debt))
		if err != nil {
			return "", nil, nil, false
		}
		defer rows.Close()
		for rows.Next() {
			var tx string
			var data []byte
			if err := rows.Scan(&tx, &data); err != nil {
				return "", nil, nil, false
			}
			borrower := common.HexToAddress(lending.Addr(data, 1))
			coll := common.HexToAddress(lending.Addr(data, 3))
			if borrower == r.account && coll == r.collateral {
				return tx, lending.Word(data, 2), lending.Word(data, 4), true
			}
		}
		return "", nil, nil, false
	}

	// Aave LiquidationCall: indexed collateral (t1), debt (t2), user (t3).
	q := `SELECT lower(hex(transaction_hash)), data FROM raw_logs
		WHERE chain_id = ? AND block_number = ? AND topic0 = unhex(?) AND address = unhex(?)
			AND topic1 = unhex(?) AND topic2 = unhex(?) AND topic3 = unhex(?) LIMIT 1`
	var tx string
	var data []byte
	row := conn.QueryRow(ctx, q, chainID, r.taken, strip0x(topicAaveLiquidation), hexNo0x(aavePool),
		padTopic(r.collateral), padTopic(r.debt), padTopic(r.account))
	if err := row.Scan(&tx, &data); err != nil {
		return "", nil, nil, false
	}
	return tx, lending.Word(data, 0), lending.Word(data, 1), true
}

// confirmVenue re-assembles the position at the fork block and re-quotes it on the
// real venue set, returning the venue that wins the chosen route, its net profit
// (USD 1e18), and whether it clears the profit threshold after gas. This reproduces
// the recorded result from live state, so each fixture is verified, not trusted.
func confirmVenue(ctx context.Context, rpc *lending.Client, resolver kmeasure.Resolver, addrRes *addrResolver, r fixtureRow,
	aaveAd *aave.Adapter, benqiAd *benqi.Adapter, aaveParams []lending.AssetParam, aaveGlobals lending.GlobalParams,
	benqiParams []lending.AssetParam, benqiGlobals lending.GlobalParams,
	benqiComptroller, aavePool common.Address, minProfit *big.Int) (string, *big.Int, bool) {

	liq := Liquidation{Protocol: r.protocol, Account: r.account, CollateralAsset: r.collateral, DebtAsset: r.debt, TakenBlock: r.taken}
	blockAddrs := addrRes.at(ctx, r.protocol, r.crossing)
	aaveDataProvider := common.HexToAddress(blockAddrs.DataProvider)
	adapter := lending.Adapter(aaveAd)
	if r.protocol == "benqi" {
		adapter = benqiAd
		benqiAd.Configure(blockAddrs, benqiParams, benqiGlobals)
	} else {
		aaveAd.Configure(blockAddrs, aaveParams, aaveGlobals)
	}

	pos, ok := assemblePosition(ctx, rpc, adapter, resolver, r.protocol, liq, r.crossing)
	if !ok {
		return "none", big.NewInt(0), false
	}
	params := newBlockParams(ctx, rpc, r.crossing, aaveDataProvider, benqiComptroller)
	cost := buildBlockCost(ctx, rpc, r.crossing, aavePool, 700000, minProfit)
	realQ := newRealVenueQuoter(rpc, r.crossing)
	res, err := prefilter.EvaluatePosition(ctx, pos, params, realQ, cost)
	if err != nil {
		return "none", big.NewInt(0), false
	}
	return realQ.WinningVenue(res.CollateralAsset, res.DebtAsset), res.NetProfitUSD, res.Profitable
}

func codeStatus(ctx context.Context, rpc *lending.Client, addr common.Address) string {
	code, err := rpc.GetCode(ctx, addr.Hex(), "")
	if err != nil {
		return "read failed"
	}
	n := len(strings.TrimPrefix(code, "0x"))
	if n <= 0 {
		return "NO CODE (not a contract)"
	}
	return fmt.Sprintf("%d bytes (live)", n/2)
}

func strip0x(s string) string { return strings.TrimPrefix(strings.ToLower(s), "0x") }

func hexNo0x(a common.Address) string { return strip0x(a.Hex()) }

// padTopic left-pads a 20-byte address to a 32-byte topic in lowercase hex.
func padTopic(a common.Address) string {
	return "000000000000000000000000" + hexNo0x(a)
}
