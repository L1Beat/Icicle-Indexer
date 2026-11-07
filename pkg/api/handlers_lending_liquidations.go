package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LiqAsset is one side of a liquidation. Amount is in the asset's base units.
type LiqAsset struct {
	Address  string   `json:"address"` // underlying token, 0x00..00 for native AVAX
	Symbol   string   `json:"symbol"`
	Decimals *uint8   `json:"decimals"`
	Amount   *string  `json:"amount"` // null when not yet known (BENQI seized before conversion)
	USD      *float64 `json:"usd"`    // oracle value at the block, null when a non-zero amount is unpriced (0 for a zero amount)
}

// LiqEvent is one executed liquidation.
type LiqEvent struct {
	ProtocolID  string    `json:"protocol_id"`
	Market      string    `json:"market,omitempty"` // Aave V4 spoke that emitted the liquidation
	TxHash      string    `json:"tx_hash"`
	BlockNumber uint32    `json:"block_number"`
	BlockTime   time.Time `json:"block_time"`
	Borrower    string    `json:"borrower"`
	Liquidator  string    `json:"liquidator"`
	Debt        LiqAsset  `json:"debt"`       // repaid
	Collateral  LiqAsset  `json:"collateral"` // seized
}

// LiqTotals is a count and USD sums over priced liquidations.
type LiqTotals struct {
	Count      uint64  `json:"count"`
	Unpriced   uint64  `json:"unpriced"` // liquidations with a non-zero repaid or seized amount but no USD value
	RepaidUSD  float64 `json:"repaid_usd"`
	SeizedUSD  float64 `json:"seized_usd"`
	ProtocolID string  `json:"protocol_id,omitempty"`
	Asset      string  `json:"asset,omitempty"`
	Symbol     string  `json:"symbol,omitempty"`
}

// LiqDay is one UTC day of liquidations.
type LiqDay struct {
	Date string `json:"date"`
	LiqTotals
	ByProtocol   []LiqTotals `json:"by_protocol"`
	ByCollateral []LiqTotals `json:"by_collateral"`
}

// liqAssetInfo resolves a liquidation's asset column to the underlying token.
// BENQI rows hold qiToken markets, Aave rows the underlying itself.
type liqAssetInfo struct {
	asset    string
	symbol   string
	decimals uint8
}

var liqCache = newTTLCache(5 * time.Minute)

// liqAssets maps protocol|address (BENQI market, Aave underlying) to the
// underlying token, from the lending market snapshots.
func (s *Server) liqAssets(ctx context.Context) (map[string]liqAssetInfo, error) {
	v, err := liqCache.get(ctx, "assets", func(ctx context.Context) (interface{}, error) {
		rows, err := s.conn.Query(ctx, `
			SELECT protocol_id, market, asset, argMax(symbol, sampled_at), argMax(decimals, sampled_at)
			FROM lending_market_snapshots
			GROUP BY protocol_id, market, asset`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		m := map[string]liqAssetInfo{}
		for rows.Next() {
			var protocol, market string
			var a liqAssetInfo
			if err := rows.Scan(&protocol, &market, &a.asset, &a.symbol, &a.decimals); err != nil {
				return nil, err
			}
			key := a.asset
			if protocol == "benqi" {
				key = market
			}
			m[protocol+"|"+key] = a
		}
		return m, rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]liqAssetInfo), nil
}

func resolveLiqAsset(assets map[string]liqAssetInfo, protocol, addr string) (string, string, *uint8) {
	if a, ok := assets[protocol+"|"+addr]; ok {
		d := a.decimals
		return a.asset, a.symbol, &d
	}
	return addr, "", nil
}

// usdOrNil returns v, or nil when a non-zero (or unknown) amount has no USD
// value. Dust liquidations repay exactly 0 and are worth a real 0.
func usdOrNil(v float64, amount *string) *float64 {
	if v <= 0 && (amount == nil || *amount != "0") {
		return nil
	}
	return &v
}

// seizedAmount returns the seized collateral in underlying units. Aave seizes
// underlying, so seize_amount is it. BENQI seizes qiTokens: the underlying
// amount is only known once the ingester has converted it (seized_underlying),
// so nil until then rather than a qiToken count under underlying decimals.
func seizedAmount(protocol, underlying, raw string) *string {
	if underlying != "0" && underlying != "" {
		return &underlying
	}
	if protocol == "benqi" && raw != "0" {
		return nil
	}
	return &raw
}

// handleLendingLiqDaily returns daily liquidation totals
// @Summary Lending liquidations per day
// @Description Executed liquidations on Aave V3, Aave V4 and BENQI per UTC day: count, repaid and seized USD (oracle value at the event block), split by protocol and by collateral asset. USD sums cover priced liquidations only; unpriced counts those missing a price. Today's partial day is included. Oldest first. Cached for 5 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Param days query int false "Days back, 1-3650" default(30)
// @Success 200 {object} Response{data=[]LiqDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending/liquidations [get]
func (s *Server) handleLendingLiqDaily(w http.ResponseWriter, r *http.Request) {
	days, ok := intParam(w, r, "days", 30, 1, 3650)
	if !ok {
		return
	}
	v, err := liqCache.get(r.Context(), fmt.Sprintf("daily:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadLiqDaily(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadLiqDaily(ctx context.Context, days int) ([]LiqDay, error) {
	assets, err := s.liqAssets(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(block_time)) AS d, protocol, lower(hex(collateral_asset)),
			count(), countIf((repay_amount > 0 AND repaid_usd = 0)
				OR (if(seized_underlying > 0, seized_underlying, seize_amount) > 0 AND seized_usd = 0)),
			sum(toFloat64(repaid_usd)) / 1e18, sum(toFloat64(seized_usd)) / 1e18
		FROM lending_liquidations FINAL
		WHERE chain_id = 43114 AND block_time >= today() - ?
		GROUP BY d, protocol, collateral_asset
		ORDER BY d
	`, days-1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiqDay{}
	var byProto, byColl map[string]*LiqTotals
	flush := func() {
		if len(out) == 0 {
			return
		}
		d := &out[len(out)-1]
		for _, t := range byProto {
			d.ByProtocol = append(d.ByProtocol, *t)
		}
		for _, t := range byColl {
			d.ByCollateral = append(d.ByCollateral, *t)
		}
		sort.Slice(d.ByProtocol, func(i, j int) bool { return d.ByProtocol[i].ProtocolID < d.ByProtocol[j].ProtocolID })
		sort.Slice(d.ByCollateral, func(i, j int) bool { return d.ByCollateral[i].SeizedUSD > d.ByCollateral[j].SeizedUSD })
	}
	for rows.Next() {
		var date, protocol, coll string
		var t LiqTotals
		if err := rows.Scan(&date, &protocol, &coll, &t.Count, &t.Unpriced, &t.RepaidUSD, &t.SeizedUSD); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Date != date {
			flush()
			out = append(out, LiqDay{Date: date, ByProtocol: []LiqTotals{}, ByCollateral: []LiqTotals{}})
			byProto, byColl = map[string]*LiqTotals{}, map[string]*LiqTotals{}
		}
		d := &out[len(out)-1]
		add := func(dst *LiqTotals) {
			dst.Count += t.Count
			dst.Unpriced += t.Unpriced
			dst.RepaidUSD += t.RepaidUSD
			dst.SeizedUSD += t.SeizedUSD
		}
		add(&d.LiqTotals)
		p, ok := byProto[protocol]
		if !ok {
			p = &LiqTotals{ProtocolID: protocol}
			byProto[protocol] = p
		}
		add(p)
		asset, symbol, _ := resolveLiqAsset(assets, protocol, "0x"+coll)
		c, ok := byColl[asset]
		if !ok {
			c = &LiqTotals{Asset: asset, Symbol: symbol}
			byColl[asset] = c
		}
		add(c)
	}
	flush()
	return out, rows.Err()
}

// handleLendingLiqRecent returns the latest liquidations
// @Summary Recent lending liquidations
// @Description The latest executed liquidations on Aave V3, Aave V4 and BENQI, newest first. min_usd keeps those whose repaid or seized value is at least that much. Cached for 5 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Param limit query int false "Rows, 1-500" default(50)
// @Param min_usd query number false "Minimum repaid or seized USD"
// @Success 200 {object} Response{data=[]LiqEvent}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending/liquidations/recent [get]
func (s *Server) handleLendingLiqRecent(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(w, r, "limit", 50, 1, 500)
	if !ok {
		return
	}
	minUSD := 0.0
	if q := r.URL.Query().Get("min_usd"); q != "" {
		f, err := strconv.ParseFloat(q, 64)
		if err != nil || f < 0 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "min_usd must be a non-negative number")
			return
		}
		minUSD = f
	}
	key := fmt.Sprintf("recent:%d:%g", limit, minUSD)
	v, err := liqCache.get(r.Context(), key, func(ctx context.Context) (interface{}, error) {
		return s.loadLiqEvents(ctx, `toFloat64(greatest(repaid_usd, seized_usd)) / 1e18 >= ?`, "block_number DESC, log_index DESC", limit, minUSD)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

// handleLendingLiqTop returns the largest liquidations
// @Summary Largest lending liquidations
// @Description The largest executed liquidations by seized USD over the last N days (today included), on Aave V3, Aave V4 and BENQI. Cached for 5 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Param days query int false "Days back, 1-3650" default(30)
// @Param limit query int false "Rows, 1-100" default(20)
// @Success 200 {object} Response{data=[]LiqEvent}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending/liquidations/top [get]
func (s *Server) handleLendingLiqTop(w http.ResponseWriter, r *http.Request) {
	days, ok := intParam(w, r, "days", 30, 1, 3650)
	if !ok {
		return
	}
	limit, ok := intParam(w, r, "limit", 20, 1, 100)
	if !ok {
		return
	}
	v, err := liqCache.get(r.Context(), fmt.Sprintf("top:%d:%d", days, limit), func(ctx context.Context) (interface{}, error) {
		return s.loadLiqEvents(ctx, `block_time >= today() - ?`, "seized_usd DESC, block_number DESC", limit, days-1)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

// liqMarket returns the 0x market of a liquidation row, or "" when it is the zero
// address (Aave V3 and BENQI rows carry no market).
func liqMarket(hexAddr string) string {
	if hexAddr == "" || strings.Trim(hexAddr, "0") == "" {
		return ""
	}
	return "0x" + hexAddr
}

// loadLiqEvents reads liquidations matching where (one ? bound to arg).
func (s *Server) loadLiqEvents(ctx context.Context, where, order string, limit int, arg interface{}) ([]LiqEvent, error) {
	assets, err := s.liqAssets(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT protocol, lower(hex(tx_hash)), block_number, block_time, lower(hex(borrower)), lower(hex(liquidator)),
			lower(hex(debt_asset)), toString(repay_amount), toFloat64(repaid_usd) / 1e18,
			lower(hex(collateral_asset)), toString(seized_underlying), toString(seize_amount),
			toFloat64(seized_usd) / 1e18, lower(hex(market))
		FROM lending_liquidations FINAL
		WHERE chain_id = 43114 AND `+where+`
		ORDER BY `+order+`
		LIMIT ?
	`, arg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiqEvent{}
	for rows.Next() {
		var e LiqEvent
		var debt, coll, repaid, seizedUnderlying, seized, market string
		var repaidUSD, seizedUSD float64
		if err := rows.Scan(&e.ProtocolID, &e.TxHash, &e.BlockNumber, &e.BlockTime, &e.Borrower, &e.Liquidator,
			&debt, &repaid, &repaidUSD, &coll, &seizedUnderlying, &seized, &seizedUSD, &market); err != nil {
			return nil, err
		}
		e.Market = liqMarket(market)
		e.Debt.Amount = &repaid
		e.Collateral.Amount = seizedAmount(e.ProtocolID, seizedUnderlying, seized)
		e.TxHash, e.Borrower, e.Liquidator = "0x"+e.TxHash, "0x"+e.Borrower, "0x"+e.Liquidator
		e.BlockTime = e.BlockTime.UTC()
		e.Debt.Address, e.Debt.Symbol, e.Debt.Decimals = resolveLiqAsset(assets, e.ProtocolID, "0x"+debt)
		e.Collateral.Address, e.Collateral.Symbol, e.Collateral.Decimals = resolveLiqAsset(assets, e.ProtocolID, "0x"+coll)
		e.Debt.USD, e.Collateral.USD = usdOrNil(repaidUSD, e.Debt.Amount), usdOrNil(seizedUSD, e.Collateral.Amount)
		out = append(out, e)
	}
	return out, rows.Err()
}

// intParam parses an integer query parameter within [min, max].
func intParam(w http.ResponseWriter, r *http.Request, name string, def, min, max int) (int, bool) {
	q := r.URL.Query().Get(name)
	if q == "" {
		return def, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(q))
	if err != nil || n < min || n > max {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, fmt.Sprintf("%s must be %d-%d", name, min, max))
		return 0, false
	}
	return n, true
}
