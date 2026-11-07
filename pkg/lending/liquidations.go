package lending

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"time"
)

// Executed-liquidation ingest. Scans raw_logs for Aave LiquidationCall and Benqi
// LiquidateBorrow events, values the repaid debt in USD at the event block, and
// persists them to lending_liquidations. Self-correcting via a watermark (rule 8)
// and idempotent via ReplacingMergeTree keyed by (block, tx, log_index), so a
// re-scan replaces rather than duplicates.

var (
	topicAaveLiquidation  = EventTopic("LiquidationCall(address,address,address,uint256,uint256,address,bool)")
	topicBenqiLiquidation = EventTopic("LiquidateBorrow(address,address,uint256,address,uint256)")
	topicV4Liquidation    = EventTopic("LiquidationCall(uint256,uint256,address,address,bool,uint256,uint256,(int256,int256,uint256),uint256,uint256,uint256)")
	mul1e10liq            = new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil)
	ray                   = new(big.Int).Exp(big.NewInt(10), big.NewInt(27), nil)
)

const (
	// liqWatermark names the scan cursor. Bumped to _v2 when seized_usd and Aave
	// V4 were added, so history is rescanned and existing rows are replaced.
	liqWatermark  = "lending_liquidations_v2"
	liqRangeBatch = 1_000_000 // blocks scanned per cycle while backfilling
)

// liqSource holds one protocol's liquidation event source and valuation inputs.
type liqSource struct {
	protocol  string
	logAddrs  []string         // contracts that emit the event (Aave pool / Benqi markets)
	oracle    string           // head oracle, fallback when block-pinned resolution fails
	anchor    string           // stable contract to resolve the oracle at a block
	oracleSig string           // getPriceOracle() (Aave) / oracle() (Benqi)
	decimals  map[string]uint8 // underlying -> decimals (Aave); nil for Benqi
	isBenqi   bool
	isV4      bool // Aave V4 spoke: assets are reserve IDs, priced by the spoke oracle
}

// Aave V4 spokes on the Core Hub (bgd-labs aave-address-book AaveV4Avalanche).
// Each emits its own LiquidationCall, keyed by its reserve IDs.
var aaveV4Spokes = []string{
	"0x435272ceff93a1e657e8abfdf0a13e95900a3a56", // main
	"0x3b517594277c67307cf2d7cbe6fe1d4399b68c41", // avax correlated
	"0xaf22446124e566e54845f65b722e4dcbf12b650a", // ethena ecosystem
	"0x6a37776b5e026dbdf043b4f933c323c84dd1b514", // forex
}

// AaveV4Spokes returns the Core Hub spoke addresses, lowercase.
func AaveV4Spokes() []string {
	return append([]string(nil), aaveV4Spokes...)
}

// AaveV4LiqSources returns one liquidation source per Aave V4 spoke. They need
// no adapter: reserves and oracle are resolved lazily from the spoke.
func AaveV4LiqSources() []liqSource {
	out := make([]liqSource, 0, len(aaveV4Spokes))
	for _, sp := range aaveV4Spokes {
		out = append(out, liqSource{protocol: "aave-v4", logAddrs: []string{sp}, isV4: true})
	}
	return out
}

// v4Reserve is a spoke reserve's underlying token.
type v4Reserve struct {
	underlying [20]byte
	decimals   uint8
}

// LiquidationRow is one decoded, valued liquidation ready to persist.
type LiquidationRow struct {
	Protocol                               string
	Block                                  uint32
	BlockTime                              time.Time
	TxHash                                 [32]byte
	LogIndex                               uint32
	Liquidator, Borrower, Collateral, Debt [20]byte
	RepayAmount, SeizeAmount, RepaidUSD    *big.Int
	SeizedUnderlying, SeizedUSD            *big.Int // seized collateral in underlying units, and in 1e18 USD
	Market                                 [20]byte // Aave V4 spoke that emitted the event, zero for other protocols
}

// LiquidationIngester scans, values, and persists executed liquidations.
type LiquidationIngester struct {
	store       *Store
	rpc         *Client
	chainID     uint32
	addrSet     []string
	byAddr      map[string]liqSource // log address -> its source (Benqi markets all map to the Benqi source)
	oracleCache map[string]string    // protocol|block-bucket -> oracle resolved at that block
	v4Reserves  map[string]v4Reserve // spoke|reserveId -> underlying
	v4Oracles   map[string]string    // spoke -> oracle
}

// oracleStride buckets oracle resolution by ~1 day; oracles rotate rarely, so this
// bounds the resolution reads during backfill.
const oracleStride = 43200

// buildLiqSource derives a protocol's liquidation source from its resolved
// addresses and params: Aave events come from the pool with per-underlying
// decimals for valuation; Benqi events come from each qiToken market (the Compound
// price convention folds in decimals, so none are needed).
func buildLiqSource(protocol Protocol, addrs Addresses, params []AssetParam) liqSource {
	s := liqSource{protocol: string(protocol), oracle: NormalizeAddr(addrs.Oracle)}
	if protocol == ProtocolBenqi {
		s.isBenqi = true
		s.logAddrs = append([]string(nil), addrs.Markets...)
		s.anchor = NormalizeAddr(addrs.Comptroller)
		s.oracleSig = "oracle()"
		return s
	}
	s.logAddrs = []string{addrs.Pool}
	s.anchor = NormalizeAddr(addrs.Provider)
	s.oracleSig = "getPriceOracle()"
	s.decimals = map[string]uint8{}
	for _, p := range params {
		s.decimals[NormalizeAddr(p.Asset)] = p.Decimals
	}
	return s
}

// NewLiquidationIngester builds an ingester from the active protocols' sources.
func NewLiquidationIngester(store *Store, rpc *Client, chainID uint32, sources []liqSource) *LiquidationIngester {
	li := &LiquidationIngester{store: store, rpc: rpc, chainID: chainID, byAddr: map[string]liqSource{}, oracleCache: map[string]string{},
		v4Reserves: map[string]v4Reserve{}, v4Oracles: map[string]string{}}
	for _, s := range sources {
		for _, a := range s.logAddrs {
			la := NormalizeAddr(a)
			if la == ZeroAddress {
				continue
			}
			li.addrSet = append(li.addrSet, la)
			li.byAddr[la] = s
		}
	}
	return li
}

// Loop runs Sync until the context is cancelled, cycling fast while backfilling
// and idling once caught up.
func (li *LiquidationIngester) Loop(ctx context.Context) {
	if len(li.addrSet) == 0 {
		return
	}
	if err := li.backfillV4Market(ctx); err != nil {
		slog.Warn("lending: aave v4 liquidation market backfill failed, will retry on next start", "error", err)
	}
	for {
		behind, err := li.Sync(ctx)
		if err != nil {
			slog.Warn("lending: liquidation sync failed", "error", err)
		}
		wait := 60 * time.Second
		if behind {
			wait = 1 * time.Second // catch up quickly on first backfill
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// v4MarketBackfill marks the one-time fill of the market column on Aave V4
// liquidation rows written before the column existed.
const (
	v4MarketBackfill = "lending_liquidations_v4_market"
	aaveV4StartBlock = 89_721_383 // Core Hub spokes created
)

// backfillV4Market fills market (the emitting spoke) on Aave V4 liquidation rows
// stored before the column existed, by joining them back to raw_logs. It rewrites
// those rows once with a newer updated_at, so ReplacingMergeTree keeps the filled
// copy, then records a watermark so it never runs again.
func (li *LiquidationIngester) backfillV4Market(ctx context.Context) error {
	done, err := li.store.GetWatermark(ctx, v4MarketBackfill)
	if err != nil || done > 0 {
		return err
	}
	q := fmt.Sprintf(`
		INSERT INTO lending_liquidations
			(chain_id, protocol, market, block_number, block_time, tx_hash, log_index, liquidator, borrower,
			 collateral_asset, debt_asset, repay_amount, seize_amount, repaid_usd, seized_underlying, seized_usd, updated_at)
		SELECT l.chain_id, l.protocol, r.address, l.block_number, l.block_time, l.tx_hash, l.log_index, l.liquidator, l.borrower,
			l.collateral_asset, l.debt_asset, l.repay_amount, l.seize_amount, l.repaid_usd, l.seized_underlying, l.seized_usd, now64(3)
		FROM (SELECT * FROM lending_liquidations FINAL WHERE chain_id = %[1]d AND protocol = '%[2]s') AS l
		INNER JOIN (
			SELECT address, transaction_hash, log_index FROM raw_logs
			WHERE chain_id = %[1]d AND block_number >= %[3]d AND address IN (%[4]s) AND topic0 IN (%[5]s)
		) AS r ON l.tx_hash = r.transaction_hash AND l.log_index = r.log_index
	`, li.chainID, ProtocolAaveV4, aaveV4StartBlock, addrInList(aaveV4Spokes), topicInList([]string{topicV4Liquidation}))
	if err := li.store.conn.Exec(ctx, q); err != nil {
		return err
	}
	slog.Info("lending: aave v4 liquidation market backfill done")
	return li.store.SetWatermark(ctx, v4MarketBackfill, 1)
}

// Sync processes one block range from the watermark forward and returns whether
// more remains (so the caller can cycle without waiting).
func (li *LiquidationIngester) Sync(ctx context.Context) (bool, error) {
	wm, err := li.store.GetWatermark(ctx, liqWatermark)
	if err != nil {
		return false, err
	}
	safe, err := li.store.SafeBlock(ctx)
	if err != nil || safe == 0 {
		return false, err
	}
	from := wm + 1
	if wm == 0 {
		if e, eerr := li.store.EarliestLogBlock(ctx, li.addrSet); eerr == nil && e > 0 {
			from = e
		}
	}
	if from > safe {
		return false, nil
	}
	to := from + liqRangeBatch
	if to > safe {
		to = safe
	}

	rows, err := li.scan(ctx, from, to)
	if err != nil {
		return false, err
	}
	if err := li.persist(ctx, rows); err != nil {
		return false, err
	}
	if err := li.store.SetWatermark(ctx, liqWatermark, to); err != nil {
		return false, err
	}
	if len(rows) > 0 {
		slog.Info("lending: liquidations ingested", "count", len(rows), "from", from, "to", to)
	}
	return to < safe, nil
}

// scan reads liquidation events in [from, to], decodes them, and values the repaid
// debt block-pinned.
func (li *LiquidationIngester) scan(ctx context.Context, from, to uint64) ([]LiquidationRow, error) {
	q := fmt.Sprintf(`
		SELECT address, block_number, block_time, transaction_hash, log_index, topic0,
			ifNull(topic1, toFixedString('', 32)) AS t1,
			ifNull(topic2, toFixedString('', 32)) AS t2,
			ifNull(topic3, toFixedString('', 32)) AS t3,
			data
		FROM raw_logs
		WHERE chain_id = ? AND block_number >= ? AND block_number <= ?
			AND address IN (%s) AND topic0 IN (%s)
		ORDER BY block_number, log_index
	`, addrInList(li.addrSet), topicInList([]string{topicAaveLiquidation, topicBenqiLiquidation, topicV4Liquidation}))

	rows, err := li.store.conn.Query(ctx, q, li.chainID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiquidationRow
	for rows.Next() {
		var addr [20]byte
		var blk uint32
		var bt time.Time
		var tx, t0, t1, t2, t3 [32]byte
		var logIdx uint32
		var data []byte
		if err := rows.Scan(&addr, &blk, &bt, &tx, &logIdx, &t0, &t1, &t2, &t3, &data); err != nil {
			return nil, err
		}
		la := "0x" + hex.EncodeToString(addr[:])
		src, ok := li.byAddr[la]
		if !ok {
			continue
		}
		r := LiquidationRow{Block: blk, BlockTime: bt, TxHash: tx, LogIndex: logIdx}
		switch "0x" + hex.EncodeToString(t0[:]) {
		case topicAaveLiquidation:
			// indexed: collateral (t1), debt (t2), user (t3); data: debtToCover(0),
			// liquidatedCollateralAmount(1), liquidator(2), receiveAToken(3).
			r.Protocol = string(ProtocolAaveV3)
			r.Collateral = last20(t1)
			r.Debt = last20(t2)
			r.Borrower = last20(t3)
			r.Liquidator = dataAddr(data, 2)
			r.RepayAmount = Word(data, 0)
			r.SeizeAmount = Word(data, 1)
		case topicBenqiLiquidation:
			// unindexed: liquidator(0), borrower(1), repayAmount(2), qiTokenCollateral(3),
			// seizeTokens(4). The borrowed market is the emitting log address.
			r.Protocol = string(ProtocolBenqi)
			r.Liquidator = dataAddr(data, 0)
			r.Borrower = dataAddr(data, 1)
			r.RepayAmount = Word(data, 2)
			r.Collateral = dataAddr(data, 3)
			r.SeizeAmount = Word(data, 4)
			r.Debt = addr // emitting qiToken market
		case topicV4Liquidation:
			// indexed: collateralReserveId (t1), debtReserveId (t2), user (t3); data:
			// liquidator(0), receiveShares(1), debtAmountRestored(2),
			// drawnSharesLiquidated(3), premiumDelta(4-6, restoredPremiumRay at 6),
			// collateralAmountRemoved(7), collateralSharesLiquidated(8),
			// collateralSharesToLiquidator(9). Amounts are in asset units.
			if !src.isV4 {
				continue
			}
			coll, ok1 := li.v4ReserveAt(ctx, la, new(big.Int).SetBytes(t1[:]))
			debt, ok2 := li.v4ReserveAt(ctx, la, new(big.Int).SetBytes(t2[:]))
			if !ok1 || !ok2 {
				slog.Warn("lending: aave v4 reserve unresolved, skipping liquidation", "spoke", la, "tx", hex.EncodeToString(tx[:]))
				continue
			}
			r.Protocol = string(ProtocolAaveV4)
			r.Market = addr
			r.Collateral = coll.underlying
			r.Debt = debt.underlying
			r.Borrower = last20(t3)
			r.Liquidator = dataAddr(data, 0)
			r.RepayAmount = new(big.Int).Add(Word(data, 2), new(big.Int).Div(Word(data, 6), ray))
			r.SeizeAmount = Word(data, 7)
			r.SeizedUnderlying = r.SeizeAmount
			usdRepaid, usdSeized := li.valueV4(ctx, la, new(big.Int).SetBytes(t2[:]), new(big.Int).SetBytes(t1[:]), debt, coll, r)
			r.RepaidUSD, r.SeizedUSD = usdRepaid, usdSeized
			out = append(out, r)
			continue
		default:
			continue
		}
		r.RepaidUSD = li.valueRepaid(ctx, src, r)
		r.SeizedUnderlying, r.SeizedUSD = li.valueSeized(ctx, src, r)
		out = append(out, r)
	}
	return out, rows.Err()
}

// valueRepaid prices the repaid debt in USD (1e18) using the oracle at the event
// block. Returns 0 when the price cannot be read (kept as an unpriced row rather
// than dropped, so counts stay complete).
func (li *LiquidationIngester) valueRepaid(ctx context.Context, src liqSource, r LiquidationRow) *big.Int {
	if r.RepayAmount == nil || r.RepayAmount.Sign() == 0 {
		return big.NewInt(0)
	}
	// Resolve the oracle as of the event block: Benqi rotates its oracle, so the
	// head address has no code / reverts at historical blocks. Aave's is stable but
	// block-pinned is correct for both.
	oracle := li.oracleAt(ctx, src, uint64(r.Block))
	if oracle == "" {
		return big.NewInt(0)
	}
	bh := "0x" + strconv.FormatUint(uint64(r.Block), 16)
	debt := "0x" + hex.EncodeToString(r.Debt[:])

	if src.isBenqi {
		// getUnderlyingPrice(market) is scaled 1e(36-dec); price * amount / 1e18 = USD 1e18.
		price := li.callPrice(ctx, oracle, "getUnderlyingPrice(address)", debt, bh)
		if price == nil || price.Sign() == 0 {
			return big.NewInt(0)
		}
		return new(big.Int).Div(new(big.Int).Mul(price, r.RepayAmount), WAD)
	}

	// Aave getAssetPrice(asset) is 1e8 USD. value = amount * price / 10^dec, then 1e8 -> 1e18.
	price := li.callPrice(ctx, oracle, "getAssetPrice(address)", debt, bh)
	if price == nil || price.Sign() == 0 {
		return big.NewInt(0)
	}
	dec := uint8(18)
	if d, ok := src.decimals[debt]; ok && d > 0 {
		dec = d
	}
	v := new(big.Int).Mul(r.RepayAmount, price)
	v.Mul(v, mul1e10liq)
	return v.Div(v, pow10liq(dec))
}

// oracleAt resolves the price oracle as of a block via the protocol's stable anchor
// (Aave PoolAddressesProvider.getPriceOracle / Benqi Comptroller.oracle), cached by
// a coarse block bucket. Falls back to the head oracle if the read fails.
func (li *LiquidationIngester) oracleAt(ctx context.Context, src liqSource, block uint64) string {
	key := src.protocol + "|" + strconv.FormatUint(block/oracleStride, 10)
	if v, ok := li.oracleCache[key]; ok {
		return v
	}
	o := src.oracle
	if src.anchor != "" && src.oracleSig != "" {
		bh := "0x" + strconv.FormatUint(block, 16)
		if res, err := li.rpc.EthCall(ctx, src.anchor, EncodeCall0(src.oracleSig), bh); err == nil {
			if a := Addr(DecodeHexBytes(res), 0); a != ZeroAddress {
				o = a
			}
		}
	}
	li.oracleCache[key] = o
	return o
}

func (li *LiquidationIngester) callPrice(ctx context.Context, oracle, sig, arg, blockHex string) *big.Int {
	res, err := li.rpc.EthCall(ctx, oracle, EncodeCall1Addr(sig, arg), blockHex)
	if err != nil {
		return nil
	}
	return Word(DecodeHexBytes(res), 0)
}

func (li *LiquidationIngester) persist(ctx context.Context, rows []LiquidationRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := li.store.conn.PrepareBatch(ctx, `INSERT INTO lending_liquidations (
		chain_id, protocol, block_number, block_time, tx_hash, log_index,
		liquidator, borrower, collateral_asset, debt_asset,
		repay_amount, seize_amount, repaid_usd, seized_underlying, seized_usd, market
	)`)
	if err != nil {
		return err
	}
	defer func() { _ = batch.Abort() }()
	for _, r := range rows {
		if err := batch.Append(
			li.chainID, r.Protocol, r.Block, r.BlockTime, r.TxHash[:], r.LogIndex,
			r.Liquidator[:], r.Borrower[:], r.Collateral[:], r.Debt[:],
			orZero(r.RepayAmount), orZero(r.SeizeAmount), orZero(r.RepaidUSD),
			orZero(r.SeizedUnderlying), orZero(r.SeizedUSD), r.Market[:],
		); err != nil {
			return err
		}
	}
	return batch.Send()
}

// valueSeized converts the seized collateral to underlying units and prices it
// in USD (1e18) with the oracle at the event block. Aave seizes underlying
// directly. Benqi seizes qiTokens, converted with the collateral market's
// exchangeRateStored at the block. Zero when a read fails.
func (li *LiquidationIngester) valueSeized(ctx context.Context, src liqSource, r LiquidationRow) (*big.Int, *big.Int) {
	if r.SeizeAmount == nil || r.SeizeAmount.Sign() == 0 {
		return big.NewInt(0), big.NewInt(0)
	}
	bh := "0x" + strconv.FormatUint(uint64(r.Block), 16)
	coll := "0x" + hex.EncodeToString(r.Collateral[:])
	underlying := r.SeizeAmount
	if src.isBenqi {
		res, err := li.rpc.EthCall(ctx, coll, EncodeCall0("exchangeRateStored()"), bh)
		if err != nil {
			return big.NewInt(0), big.NewInt(0)
		}
		rate := Word(DecodeHexBytes(res), 0)
		underlying = new(big.Int).Div(new(big.Int).Mul(r.SeizeAmount, rate), WAD)
	}
	oracle := li.oracleAt(ctx, src, uint64(r.Block))
	if oracle == "" {
		return underlying, big.NewInt(0)
	}
	if src.isBenqi {
		price := li.callPrice(ctx, oracle, "getUnderlyingPrice(address)", coll, bh)
		if price == nil || price.Sign() == 0 {
			return underlying, big.NewInt(0)
		}
		return underlying, new(big.Int).Div(new(big.Int).Mul(price, underlying), WAD)
	}
	price := li.callPrice(ctx, oracle, "getAssetPrice(address)", coll, bh)
	if price == nil || price.Sign() == 0 {
		return underlying, big.NewInt(0)
	}
	dec := uint8(18)
	if d, ok := src.decimals[coll]; ok && d > 0 {
		dec = d
	}
	v := new(big.Int).Mul(underlying, price)
	v.Mul(v, mul1e10liq)
	return underlying, v.Div(v, pow10liq(dec))
}

// v4ReserveAt resolves a spoke reserve's underlying and decimals, cached. The
// mapping is fixed once a reserve is added, so the head state is used.
func (li *LiquidationIngester) v4ReserveAt(ctx context.Context, spoke string, id *big.Int) (v4Reserve, bool) {
	key := spoke + "|" + id.String()
	if r, ok := li.v4Reserves[key]; ok {
		return r, true
	}
	// getReserve(uint256) returns (underlying, hub, assetId, decimals, ...).
	data := Selector("getReserve(uint256)") + hex.EncodeToString(id.FillBytes(make([]byte, 32)))
	res, err := li.rpc.EthCall(ctx, spoke, data, "latest")
	if err != nil {
		return v4Reserve{}, false
	}
	b := DecodeHexBytes(res)
	if len(b) < 4*32 {
		return v4Reserve{}, false
	}
	var r v4Reserve
	copy(r.underlying[:], b[12:32])
	r.decimals = uint8(WordU64(b, 3))
	li.v4Reserves[key] = r
	return r, true
}

// valueV4 prices repaid debt and seized collateral with the spoke's oracle at
// the event block. getReservePrice is USD with 8 decimals.
func (li *LiquidationIngester) valueV4(ctx context.Context, spoke string, debtID, collID *big.Int, debt, coll v4Reserve, r LiquidationRow) (*big.Int, *big.Int) {
	oracle, ok := li.v4Oracles[spoke]
	if !ok {
		res, err := li.rpc.EthCall(ctx, spoke, EncodeCall0("ORACLE()"), "latest")
		if err != nil {
			return big.NewInt(0), big.NewInt(0)
		}
		oracle = Addr(DecodeHexBytes(res), 0)
		li.v4Oracles[spoke] = oracle
	}
	bh := "0x" + strconv.FormatUint(uint64(r.Block), 16)
	value := func(id *big.Int, amount *big.Int, dec uint8) *big.Int {
		if amount == nil || amount.Sign() == 0 {
			return big.NewInt(0)
		}
		data := Selector("getReservePrice(uint256)") + hex.EncodeToString(id.FillBytes(make([]byte, 32)))
		res, err := li.rpc.EthCall(ctx, oracle, data, bh)
		if err != nil {
			return big.NewInt(0)
		}
		price := Word(DecodeHexBytes(res), 0)
		v := new(big.Int).Mul(amount, price)
		v.Mul(v, mul1e10liq)
		return v.Div(v, pow10liq(dec))
	}
	return value(debtID, r.RepayAmount, debt.decimals), value(collID, r.SeizeAmount, coll.decimals)
}

// --- helpers ---

func last20(b [32]byte) [20]byte {
	var a [20]byte
	copy(a[:], b[12:])
	return a
}

func dataAddr(data []byte, i int) [20]byte {
	var a [20]byte
	off := i * 32
	if off+32 <= len(data) {
		copy(a[:], data[off+12:off+32])
	}
	return a
}

func pow10liq(dec uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
}
