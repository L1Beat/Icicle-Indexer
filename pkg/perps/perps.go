// Package perps records C-Chain perpetuals trading from raw_logs: GMX V2
// (Synthetics) and the history of GMX V1. Each window of raw_logs is read once
// for the two contracts, decoded in Go (GMX V2's EventEmitter packs its fields
// in a key/value struct SQL cannot unpack) and written to perp_events, one row
// per position change, liquidation or fee. Open interest is rolled up per day
// into perp_oi_daily.
//
// Volume is the USD size change of every increase, decrease and liquidation,
// both legs, as GMX itself reports it. Collateral-only changes (size 0) are
// not trades and are not stored. Fees are position, borrowing and liquidation
// fees; funding, which traders pay each other, is not a fee.
package perps

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"golang.org/x/crypto/sha3"
)

// ChainID is the C-Chain. Only it is tracked.
const ChainID = 43114

// Venue is one perpetuals exchange as served by the API.
type Venue struct {
	ID   string
	Name string
}

// Venues in display order.
var Venues = []Venue{
	{ID: "gmx-v2", Name: "GMX V2"},
	{ID: "gmx-v1", Name: "GMX V1"},
}

// Contracts, verified on-chain 2026-11-03 (contract names on Routescan).
const (
	gmxV1Vault        = "9ab2de34a33fb459b538c43f251eb825645e8595" // Vault
	gmxV2EventEmitter = "db17b211c34240b014ab6d61d4a31fa0c0e20c26" // EventEmitter
)

// GMX V1 Vault events. Sizes, collateral and fees are USD with 30 decimals.
var (
	topicV1Increase   = topicOf("IncreasePosition(bytes32,address,address,address,uint256,uint256,bool,uint256,uint256)")
	topicV1Decrease   = topicOf("DecreasePosition(bytes32,address,address,address,uint256,uint256,bool,uint256,uint256)")
	topicV1Liquidate  = topicOf("LiquidatePosition(bytes32,address,address,address,bool,uint256,uint256,uint256,int256,uint256)")
	topicV1MarginFees = topicOf("CollectMarginFees(address,uint256,uint256)")
)

// GMX V2 EventEmitter events. topic1 is the hash of the event name, which is
// how the window read picks the names below.
var (
	topicV2EventLog  = topicOf("EventLog(address,string,string," + eventLogData + ")")
	topicV2EventLog1 = topicOf("EventLog1(address,string,string,bytes32," + eventLogData + ")")
	topicV2EventLog2 = topicOf("EventLog2(address,string,string,bytes32,bytes32," + eventLogData + ")")

	v2Names = []string{"PositionIncrease", "PositionDecrease", "PositionFeesCollected", "OpenInterestUpdated", "MarketCreated"}
)

// eventLogData is the ABI type of GMX V2's EventLogData: seven groups of
// (key/value items, key/array items) for address, uint, int, bool, bytes32,
// bytes and string values.
const eventLogData = "(((string,address)[],(string,address[])[]),((string,uint256)[],(string,uint256[])[])," +
	"((string,int256)[],(string,int256[])[]),((string,bool)[],(string,bool[])[])," +
	"((string,bytes32)[],(string,bytes32[])[]),((string,bytes)[],(string,bytes[])[])," +
	"((string,string)[],(string,string[])[]))"

// orderTypeLiquidation is GMX V2's Order.OrderType.Liquidation.
const orderTypeLiquidation = 7

// Start is where the first run begins, before GMX V1's first trade on the
// C-Chain (2021-12-22). V1 took its last new position on 2025-07-09, the day
// of the GMX V1 exploit, and has only seen closes since.
var Start = time.Date(2021, 12, 20, 0, 0, 0, 0, time.UTC)

// windowDays bounds one raw_logs read.
const windowDays = 7

// budget bounds the work per Sync call.
const budget = 2 * time.Minute

// Watermarks in indexer_watermarks: the time up to which raw_logs have been
// read (exclusive), and the first day open interest is not rolled up for.
const (
	watermark   = "perps/through"
	oiWatermark = "perps/oi_day"
)

var e30 = new(big.Float).SetFloat64(1e30)

func topicOf(sig string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return hex.EncodeToString(h.Sum(nil))
}

// usd30 converts a 30-decimal USD amount to a float.
func usd30(x *big.Int) float64 {
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(x), e30).Float64()
	return f
}

// Sync reads raw_logs from the watermark up to the C-Chain sync watermark, a
// week at a time within budget, then rolls up open interest for every day the
// read has passed.
func Sync(ctx context.Context, conn driver.Conn) error {
	var synced time.Time
	if err := conn.QueryRow(ctx, `
		SELECT block_time FROM raw_blocks
		WHERE chain_id = ? AND block_number = (SELECT block_number FROM sync_watermark WHERE chain_id = ?)
	`, uint32(ChainID), uint32(ChainID)).Scan(&synced); err != nil {
		return fmt.Errorf("sync watermark time: %w", err)
	}
	synced = synced.UTC()
	from, err := readWatermark(ctx, conn, watermark)
	if err != nil {
		return err
	}
	if from.IsZero() {
		from = Start
	}
	deadline := time.Now().Add(budget)
	for from.Before(synced) && time.Now().Before(deadline) {
		to := from.AddDate(0, 0, windowDays)
		if to.After(synced) {
			to = synced
		}
		start := time.Now()
		n, err := syncWindow(ctx, conn, from, to)
		if err != nil {
			return fmt.Errorf("window %s..%s: %w", from.Format(time.RFC3339), to.Format(time.RFC3339), err)
		}
		if err := writeWatermark(ctx, conn, watermark, to); err != nil {
			return err
		}
		if to.Sub(from) > time.Hour {
			slog.Info("Perps synced", "from", from.Format("2006-01-02"), "to", to.Format("2006-01-02"), "rows", n, "duration", time.Since(start))
		}
		from = to
	}
	return rollupOI(ctx, conn, from, deadline)
}

// Through is the time up to which perps events are complete, zero before the
// first window.
func Through(ctx context.Context, conn driver.Conn) (time.Time, error) {
	return readWatermark(ctx, conn, watermark)
}

func readWatermark(ctx context.Context, conn driver.Conn, name string) (time.Time, error) {
	var t time.Time
	rows, err := conn.Query(ctx, `
		SELECT last_period FROM indexer_watermarks FINAL
		WHERE chain_id = ? AND indexer_name = ?`, uint32(ChainID), name)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&t); err != nil {
			return t, err
		}
	}
	return t.UTC(), rows.Err()
}

func writeWatermark(ctx context.Context, conn driver.Conn, name string, t time.Time) error {
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	return conn.Exec(wctx, `
		INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
		VALUES (?, ?, 'time', ?, 0)`, uint32(ChainID), name, t)
}

// event is one perp_events row.
type event struct {
	Venue      string
	Time       time.Time
	Tx         string
	LogIndex   uint32
	Kind       string // increase, decrease, liquidation, fee
	Market     string // V2 market token, V1 index token, zeros for V1 fees
	Account    string
	IsLong     bool
	SizeUSD    float64
	Collateral float64
	FeeUSD     float64
}

// oiUpdate is one perp_oi_events row: a GMX V2 OpenInterestUpdated.
type oiUpdate struct {
	Time       time.Time
	Tx         string
	LogIndex   uint32
	Market     string
	Collateral string
	IsLong     bool
	OIUSD      float64
}

// market is one perp_markets row: a GMX V2 MarketCreated.
type market struct {
	Time                    time.Time
	Market, Index, Long, Sh string
}

type rawLog struct {
	Time     time.Time
	Tx       []byte
	LogIndex uint32
	Address  []byte
	Topic0   []byte
	Topic1   []byte
	Data     []byte
}

func syncWindow(ctx context.Context, conn driver.Conn, from, to time.Time) (int, error) {
	names := make([]string, len(v2Names))
	for i, n := range v2Names {
		names[i] = fmt.Sprintf("unhex('%s')", topicOf(n))
	}
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT block_time, transaction_hash, log_index, address, topic0, ifNull(topic1, ''), data
		FROM raw_logs
		WHERE chain_id = %d AND block_time >= ? AND block_time < ?
		  AND ((address = unhex('%s') AND topic0 IN (unhex('%s'), unhex('%s'), unhex('%s'), unhex('%s')))
		    OR (address = unhex('%s') AND topic0 IN (unhex('%s'), unhex('%s'), unhex('%s')) AND topic1 IN (%s)))
	`, ChainID, gmxV1Vault, topicV1Increase, topicV1Decrease, topicV1Liquidate, topicV1MarginFees,
		gmxV2EventEmitter, topicV2EventLog, topicV2EventLog1, topicV2EventLog2, joinComma(names)), from, to)
	if err != nil {
		return 0, err
	}
	var events []event
	var ois []oiUpdate
	var markets []market
	for rows.Next() {
		var l rawLog
		var tx, addr, t0, t1 string
		if err := rows.Scan(&l.Time, &tx, &l.LogIndex, &addr, &t0, &t1, &l.Data); err != nil {
			rows.Close()
			return 0, err
		}
		l.Tx, l.Address, l.Topic0, l.Topic1 = []byte(tx), []byte(addr), []byte(t0), []byte(t1)
		if hex.EncodeToString(l.Address) == gmxV1Vault {
			if e, ok := decodeV1(l); ok {
				events = append(events, e)
			}
			continue
		}
		e, err := decodeEventLog(l.Data)
		if err != nil {
			slog.Debug("Undecodable GMX V2 event", "tx", hex.EncodeToString(l.Tx), "error", err)
			continue
		}
		switch e.Name {
		case "PositionIncrease", "PositionDecrease", "PositionFeesCollected":
			if ev, ok := v2Event(l, e); ok {
				events = append(events, ev)
			}
		case "OpenInterestUpdated":
			ois = append(ois, oiUpdate{Time: l.Time.UTC(), Tx: string(l.Tx), LogIndex: l.LogIndex, Market: e.Addr["market"],
				Collateral: e.Addr["collateralToken"], IsLong: e.Bool["isLong"], OIUSD: usd30(e.uint("nextValue"))})
		case "MarketCreated":
			markets = append(markets, market{Time: l.Time.UTC(), Market: e.Addr["marketToken"], Index: e.Addr["indexToken"],
				Long: e.Addr["longToken"], Sh: e.Addr["shortToken"]})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := insertEvents(ctx, conn, events); err != nil {
		return 0, err
	}
	if err := insertOI(ctx, conn, ois); err != nil {
		return 0, err
	}
	if err := insertMarkets(ctx, conn, markets); err != nil {
		return 0, err
	}
	return len(events) + len(ois) + len(markets), nil
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// decodeV1 reads a GMX V1 Vault event. All fields are non-indexed:
// Increase/DecreasePosition(key, account, collateralToken, indexToken,
// collateralDelta, sizeDelta, isLong, price, fee), LiquidatePosition(key,
// account, collateralToken, indexToken, isLong, size, collateral,
// reserveAmount, realisedPnl, markPrice), CollectMarginFees(token, feeUsd,
// feeTokens).
func decodeV1(l rawLog) (event, bool) {
	r := abiReader(l.Data)
	w := func(i int) []byte { b, _ := r.word(32 * i); return b }
	addr := func(i int) string { return hex.EncodeToString(w(i)[12:]) }
	u := func(i int) *big.Int { return new(big.Int).SetBytes(w(i)) }
	e := event{Venue: "gmx-v1", Time: l.Time.UTC(), Tx: string(l.Tx), LogIndex: l.LogIndex}
	switch hex.EncodeToString(l.Topic0) {
	case topicV1Increase, topicV1Decrease:
		if len(l.Data) < 32*9 {
			return e, false
		}
		e.Kind = "increase"
		if hex.EncodeToString(l.Topic0) == topicV1Decrease {
			e.Kind = "decrease"
		}
		e.Account, e.Market, e.IsLong = addr(1), addr(3), w(6)[31] != 0
		e.Collateral, e.SizeUSD = usd30(u(4)), usd30(u(5))
		return e, e.SizeUSD > 0
	case topicV1Liquidate:
		if len(l.Data) < 32*10 {
			return e, false
		}
		e.Kind = "liquidation"
		e.Account, e.Market, e.IsLong = addr(1), addr(3), w(4)[31] != 0
		e.SizeUSD, e.Collateral = usd30(u(5)), usd30(u(6))
		return e, true
	case topicV1MarginFees:
		if len(l.Data) < 32*3 {
			return e, false
		}
		e.Kind, e.Market, e.Account = "fee", zeroAddr, zeroAddr
		e.FeeUSD = usd30(u(1))
		return e, e.FeeUSD > 0
	}
	return e, false
}

var zeroAddr = hex.EncodeToString(make([]byte, 20))

// v2Event turns a decoded GMX V2 position event into a row. Token amounts are
// priced with the collateral price in the event: price * amount is USD with 30
// decimals.
func v2Event(l rawLog, e eventLog) (event, bool) {
	ev := event{Venue: "gmx-v2", Time: l.Time.UTC(), Tx: string(l.Tx), LogIndex: l.LogIndex, Market: e.Addr["market"],
		Account: e.Addr["account"], IsLong: e.Bool["isLong"]}
	px := e.uint("collateralTokenPrice.min")
	tokenUSD := func(amount *big.Int) float64 {
		return usd30(new(big.Int).Mul(new(big.Int).Abs(amount), px))
	}
	switch e.Name {
	case "PositionIncrease", "PositionDecrease":
		ev.Kind = "increase"
		if e.Name == "PositionDecrease" {
			ev.Kind = "decrease"
			if e.uint("orderType").Int64() == orderTypeLiquidation {
				ev.Kind = "liquidation"
			}
		}
		ev.SizeUSD = usd30(e.uint("sizeDeltaUsd"))
		cd, ok := e.Uint["collateralDeltaAmount"]
		if !ok {
			cd = e.Int["collateralDeltaAmount"] // an int item on increases
		}
		if cd != nil {
			ev.Collateral = tokenUSD(cd)
		}
		return ev, ev.SizeUSD > 0
	case "PositionFeesCollected":
		ev.Kind, ev.Account = "fee", e.Addr["trader"]
		fees := new(big.Int).Add(e.uint("positionFeeAmount"), e.uint("borrowingFeeAmount"))
		fees.Add(fees, e.uint("liquidationFeeAmount"))
		ev.FeeUSD = tokenUSD(fees)
		return ev, ev.FeeUSD > 0
	}
	return ev, false
}

func unhexOrZero(s string, n int) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != n {
		return string(make([]byte, n))
	}
	return string(b)
}

func insertEvents(ctx context.Context, conn driver.Conn, events []event) error {
	if len(events) == 0 {
		return nil
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	b, err := conn.PrepareBatch(wctx, `INSERT INTO perp_events (venue, block_time, transaction_hash, log_index, kind, market, account, is_long, size_usd, collateral_usd, fee_usd)`)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := b.Append(e.Venue, e.Time, e.Tx, e.LogIndex, e.Kind, unhexOrZero(e.Market, 20), unhexOrZero(e.Account, 20),
			e.IsLong, e.SizeUSD, e.Collateral, e.FeeUSD); err != nil {
			return err
		}
	}
	return b.Send()
}

func insertOI(ctx context.Context, conn driver.Conn, ois []oiUpdate) error {
	if len(ois) == 0 {
		return nil
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	b, err := conn.PrepareBatch(wctx, `INSERT INTO perp_oi_events (block_time, transaction_hash, log_index, market, collateral_token, is_long, oi_usd)`)
	if err != nil {
		return err
	}
	for _, o := range ois {
		if err := b.Append(o.Time, o.Tx, o.LogIndex, unhexOrZero(o.Market, 20), unhexOrZero(o.Collateral, 20), o.IsLong, o.OIUSD); err != nil {
			return err
		}
	}
	return b.Send()
}

func insertMarkets(ctx context.Context, conn driver.Conn, ms []market) error {
	if len(ms) == 0 {
		return nil
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	b, err := conn.PrepareBatch(wctx, `INSERT INTO perp_markets (market, index_token, long_token, short_token, created_time)`)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if err := b.Append(unhexOrZero(m.Market, 20), unhexOrZero(m.Index, 20), unhexOrZero(m.Long, 20), unhexOrZero(m.Sh, 20), m.Time); err != nil {
			return err
		}
	}
	return b.Send()
}

// rollupOI writes perp_oi_daily for every complete day the raw_logs read has
// passed, carrying each day forward from the one before:
//   - GMX V2: per (market, collateral token, side), the day's last
//     OpenInterestUpdated nextValue, else the previous day's value.
//   - GMX V1: per (index token, side), the previous day's value plus the
//     day's increases minus its decreases and liquidations.
func rollupOI(ctx context.Context, conn driver.Conn, through time.Time, deadline time.Time) error {
	day, err := readWatermark(ctx, conn, oiWatermark)
	if err != nil {
		return err
	}
	if day.IsZero() {
		day = Start
	}
	end := through.Truncate(24 * time.Hour) // first day not complete
	for day.Before(end) && time.Now().Before(deadline) {
		d := day.Format("2006-01-02")
		for _, q := range []string{oiV2SQL, oiV1SQL} {
			if err := run(ctx, conn, fmt.Sprintf(q, d)); err != nil {
				return fmt.Errorf("open interest %s: %w", d, err)
			}
		}
		day = day.AddDate(0, 0, 1)
		if err := writeWatermark(ctx, conn, oiWatermark, day); err != nil {
			return err
		}
	}
	return nil
}

func run(ctx context.Context, conn driver.Conn, q string) error {
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	return conn.Exec(wctx, q)
}

const oiV2SQL = `
	INSERT INTO perp_oi_daily (day, venue, market, collateral_token, is_long, oi_usd)
	SELECT toDate('%[1]s'), 'gmx-v2', market, collateral_token, is_long, argMax(v, ord)
	FROM (
		SELECT market, collateral_token, is_long, oi_usd AS v, toUInt64(0) AS ord
		FROM perp_oi_daily FINAL WHERE day = toDate('%[1]s') - 1 AND venue = 'gmx-v2'
		UNION ALL
		SELECT market, collateral_token, is_long, oi_usd AS v,
			toUInt64(toUnixTimestamp64Milli(block_time)) * 100000 + log_index + 1 AS ord
		FROM perp_oi_events FINAL WHERE toDate(block_time) = toDate('%[1]s')
	)
	GROUP BY market, collateral_token, is_long`

const oiV1SQL = `
	INSERT INTO perp_oi_daily (day, venue, market, collateral_token, is_long, oi_usd)
	SELECT toDate('%[1]s'), 'gmx-v1', market, collateral_token, is_long, sum(v)
	FROM (
		SELECT market, collateral_token, is_long, oi_usd AS v
		FROM perp_oi_daily FINAL WHERE day = toDate('%[1]s') - 1 AND venue = 'gmx-v1'
		UNION ALL
		SELECT market, toFixedString(unhex('0000000000000000000000000000000000000000'), 20) AS collateral_token, is_long,
			if(kind = 'increase', size_usd, -size_usd) AS v
		FROM perp_events FINAL
		WHERE venue = 'gmx-v1' AND kind IN ('increase', 'decrease', 'liquidation') AND toDate(block_time) = toDate('%[1]s')
	)
	GROUP BY market, collateral_token, is_long`
