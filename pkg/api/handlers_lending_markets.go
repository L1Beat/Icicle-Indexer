package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"icicle/pkg/lendingmarkets"
)

// LendingProtocol is a C-Chain lending protocol.
type LendingProtocol struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// LendingMarket is one protocol x asset market. Amounts are token base units.
type LendingMarket struct {
	ProtocolID              string    `json:"protocol_id"`
	Market                  string    `json:"market"` // Aave V3 Pool, BENQI qiToken, Aave V4 Hub
	Asset                   string    `json:"asset"`  // underlying token, 0x00..00 for native AVAX
	Symbol                  string    `json:"symbol"`
	Decimals                uint8     `json:"decimals"`
	TotalSupplied           string    `json:"total_supplied"`
	TotalBorrowed           string    `json:"total_borrowed"`
	UtilizationPct          *float64  `json:"utilization_percent"` // null when nothing is supplied
	SupplyAPYPct            float64   `json:"supply_apy_percent"`
	BorrowAPYPct            float64   `json:"borrow_apy_percent"`
	CollateralEnabled       *bool     `json:"collateral_enabled"`
	LTVPct                  *float64  `json:"ltv_percent"`
	LiquidationThresholdPct *float64  `json:"liquidation_threshold_percent"`
	PriceUSD                *float64  `json:"price_usd"`
	SuppliedUSD             *float64  `json:"supplied_usd"`
	BorrowedUSD             *float64  `json:"borrowed_usd"`
	SupplyCap               *string   `json:"supply_cap"`
	BorrowCap               *string   `json:"borrow_cap"`
	SampledAt               time.Time `json:"sampled_at"`
	// LTVChangedAt is when ltv_percent took its current value, and LTVChangedTx
	// the config transaction. Null when unchanged since listing, when not dated
	// yet, and where the market has no LTV (Aave V4).
	LTVChangedAt *time.Time `json:"ltv_changed_at"`
	LTVChangedTx *string    `json:"ltv_changed_tx"`
	// EMode lists the Aave V3 E-Mode categories, or Aave V4 spokes, that
	// include the asset. Empty when none (always for BENQI).
	EMode                []LendingMarketEMode `json:"emode"`
	supplied, borrowed   *big.Int
	supplyAPR, borrowAPR float64
	ltvBps               *uint16
}

// LendingAssetFlow is supply and borrow of one asset, in its base units.
type LendingAssetFlow struct {
	ProtocolID string `json:"protocol_id,omitempty"`
	Supplied   string `json:"supplied"`
	Borrowed   string `json:"borrowed"`
}

// LendingAssetSummary sums one asset across protocols.
type LendingAssetSummary struct {
	Supplied              string             `json:"supplied"`
	Borrowed              string             `json:"borrowed"`
	ShareOfTokenSupplyPct *float64           `json:"share_of_savax_supply_percent,omitempty"`
	ByProtocol            []LendingAssetFlow `json:"by_protocol"`
}

// LendingUSDTotal is supply and borrow value in USD.
type LendingUSDTotal struct {
	ProtocolID  string  `json:"protocol_id,omitempty"`
	SuppliedUSD float64 `json:"supplied_usd"`
	BorrowedUSD float64 `json:"borrowed_usd"`
	// UnpricedMarkets counts markets with a supply but no oracle price, which
	// are left out of the USD sums.
	UnpricedMarkets int `json:"unpriced_markets"`
}

// LendingTotals holds USD totals per protocol and overall. Markets without an
// oracle price are left out.
type LendingTotals struct {
	SuppliedUSD float64           `json:"supplied_usd"`
	BorrowedUSD float64           `json:"borrowed_usd"`
	ByProtocol  []LendingUSDTotal `json:"by_protocol"`
}

// LendingSummary is the AVAX and sAVAX view across protocols.
type LendingSummary struct {
	AVAX   LendingAssetSummary `json:"avax"`
	SAVAX  LendingAssetSummary `json:"savax"`
	Totals LendingTotals       `json:"totals"`
}

// LendingMarketEMode is the asset's setting inside one collateral group: an
// Aave V3 E-Mode category or an Aave V4 spoke.
type LendingMarketEMode struct {
	ID                      string   `json:"id"` // V3 category id, V4 spoke address
	Label                   string   `json:"label"`
	LTVPct                  float64  `json:"ltv_percent"`            // effective, 0 when LTV-zeroed
	ConfiguredLTVPct        float64  `json:"configured_ltv_percent"` // the group's LTV before any LTV-zero
	LiquidationThresholdPct float64  `json:"liquidation_threshold_percent"`
	LiquidationBonusPct     float64  `json:"liquidation_bonus_percent"` // above 100%, 1 = 1%
	AsCollateral            bool     `json:"as_collateral"`
	Borrowable              bool     `json:"borrowable"`
	PreviousLTVPct          *float64 `json:"previous_ltv_percent"` // V4: config before the current one, kept by older positions
	// LTVChangedAt is when a V4 spoke entry's LTV took its current value (the
	// dynamic config that set it), LTVChangedTx its transaction. Null for V3
	// categories, when unchanged since listing, and when not dated yet.
	LTVChangedAt *time.Time `json:"ltv_changed_at"`
	LTVChangedTx *string    `json:"ltv_changed_tx"`
	ltvBps       uint16
	protocol     string
}

// LendingEModeCategory is one collateral group. V3 categories share one LTV,
// threshold and bonus. V4 spokes set them per asset, so those are null here and
// given on each market's emode entry.
type LendingEModeCategory struct {
	ProtocolID              string   `json:"protocol_id"`
	ID                      string   `json:"id"`
	Label                   string   `json:"label"`
	LTVPct                  *float64 `json:"ltv_percent"`
	LiquidationThresholdPct *float64 `json:"liquidation_threshold_percent"`
	LiquidationBonusPct     *float64 `json:"liquidation_bonus_percent"`
	CollateralAssets        []string `json:"collateral_assets"`
	BorrowableAssets        []string `json:"borrowable_assets"`
	LTVZeroAssets           []string `json:"ltv_zero_assets"` // collateral assets with effective LTV 0 in the group
}

// LendingOverview is the /metrics/lending payload.
type LendingOverview struct {
	Protocols       []LendingProtocol      `json:"protocols"`
	EModeCategories []LendingEModeCategory `json:"emode_categories"`
	Markets         []LendingMarket        `json:"markets"`
	Summary         LendingSummary         `json:"summary"`
	SampledAt       *time.Time             `json:"sampled_at"`
}

var lendingProtocols = []LendingProtocol{
	{ID: lendingmarkets.ProtocolAaveV3, Name: "Aave V3", URL: "https://aave.com"},
	{ID: lendingmarkets.ProtocolAaveV4, Name: "Aave V4", URL: "https://aave.com"},
	{ID: lendingmarkets.ProtocolBenqi, Name: "BENQI Lending", URL: "https://benqi.fi"},
}

// lendingAssetClasses maps the summary assets to their market assets: AVAX is
// WAVAX on Aave and native AVAX on BENQI (qiAVAX).
var lendingAssetClasses = map[string][]string{
	"avax":  {lendingmarkets.WAVAX, lendingmarkets.NativeAVAX},
	"savax": {lendingmarkets.SAVAX},
}

var lendingCache = newTTLCache(2 * time.Minute)

// handleLendingMarkets returns C-Chain lending markets
// @Summary C-Chain lending markets
// @Description Supply, borrow, rates and risk parameters of every market on Aave V3, Aave V4 (Core Hub) and BENQI Lending, read from the contracts by the indexer every C-Chain snapshot cycle (5 minutes by default, `cchainSnapshotInterval`) and cached for 2 minutes. Amounts are token base units. APYs compound the per-second rate. USD values use each protocol's own oracle and are null when its read failed. The summary sums AVAX (WAVAX on Aave, native AVAX on BENQI) and sAVAX across protocols.
// @Tags Metrics - Staking
// @Produce json
// @Success 200 {object} Response{data=LendingOverview}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending [get]
func (s *Server) handleLendingMarkets(w http.ResponseWriter, r *http.Request) {
	v, err := lendingCache.get(r.Context(), "overview", s.loadLendingMarkets)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadLendingMarkets(ctx context.Context) (interface{}, error) {
	// Each protocol's newest cycle. A protocol whose read failed keeps its last
	// good cycle, and markets missing from it (delisted) drop out.
	rows, err := s.conn.Query(ctx, `
		SELECT protocol_id, market, asset, symbol, decimals, toString(supplied), toString(borrowed),
			supply_apr, borrow_apr, collateral_enabled, ltv_bps, liquidation_threshold_bps,
			price_usd, toString(supply_cap), toString(borrow_cap), sampled_at
		FROM (SELECT * FROM lending_market_snapshots FINAL) t
		WHERE snapshot_date >= today() - 1
		  AND (protocol_id, sampled_at) IN (
			SELECT protocol_id, max(sampled_at) FROM lending_market_snapshots
			WHERE snapshot_date >= today() - 1 GROUP BY protocol_id)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	markets := []LendingMarket{}
	for rows.Next() {
		var m LendingMarket
		var supplied, borrowed string
		var supplyCap, borrowCap *string
		var ltv, lt *uint16
		if err := rows.Scan(&m.ProtocolID, &m.Market, &m.Asset, &m.Symbol, &m.Decimals, &supplied, &borrowed,
			&m.supplyAPR, &m.borrowAPR, &m.CollateralEnabled, &ltv, &lt,
			&m.PriceUSD, &supplyCap, &borrowCap, &m.SampledAt); err != nil {
			return nil, err
		}
		m.SupplyCap, m.BorrowCap = supplyCap, borrowCap
		m.LTVPct, m.LiquidationThresholdPct = bpsPct(ltv), bpsPct(lt)
		m.ltvBps = ltv
		fillLendingMarket(&m, supplied, borrowed)
		m.EMode = []LendingMarketEMode{}
		markets = append(markets, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(markets, func(i, j int) bool {
		a, b := markets[i], markets[j]
		if a.ProtocolID != b.ProtocolID {
			return a.ProtocolID < b.ProtocolID
		}
		return usdOrZero(a.SuppliedUSD) > usdOrZero(b.SuppliedUSD)
	})

	var savaxSupply *big.Int
	var sup string
	if err := s.conn.QueryRow(ctx, `
		SELECT toString(argMax(token_supply, sampled_at)) FROM liquid_staking_snapshots
		WHERE protocol_id = 'benqi-savax'
	`).Scan(&sup); err == nil {
		savaxSupply, _ = new(big.Int).SetString(sup, 10)
	}

	cats, err := s.loadLendingEMode(ctx, markets)
	if err != nil {
		return nil, err
	}
	s.fillLTVChanges(ctx, markets)
	out := LendingOverview{Protocols: lendingProtocols, EModeCategories: cats, Markets: markets, Summary: summarizeLending(markets, savaxSupply)}
	for i := range markets {
		if out.SampledAt == nil || markets[i].SampledAt.After(*out.SampledAt) {
			t := markets[i].SampledAt
			out.SampledAt = &t
		}
	}
	return out, nil
}

// fillLendingMarket derives the amounts, utilization, APYs and USD values.
func fillLendingMarket(m *LendingMarket, supplied, borrowed string) {
	m.SampledAt = m.SampledAt.UTC()
	m.supplied, _ = new(big.Int).SetString(supplied, 10)
	m.borrowed, _ = new(big.Int).SetString(borrowed, 10)
	if m.supplied == nil {
		m.supplied = new(big.Int)
	}
	if m.borrowed == nil {
		m.borrowed = new(big.Int)
	}
	m.TotalSupplied, m.TotalBorrowed = m.supplied.String(), m.borrowed.String()
	if m.supplied.Sign() > 0 {
		u := 100 * bigRatio(m.borrowed, m.supplied)
		m.UtilizationPct = &u
	}
	m.SupplyAPYPct, m.BorrowAPYPct = aprToAPYPct(m.supplyAPR), aprToAPYPct(m.borrowAPR)
	if m.PriceUSD != nil {
		sv := tokenAmount(m.supplied, m.Decimals) * *m.PriceUSD
		bv := tokenAmount(m.borrowed, m.Decimals) * *m.PriceUSD
		m.SuppliedUSD, m.BorrowedUSD = &sv, &bv
	}
}

// aprToAPYPct compounds a simple annual rate per second, the way the protocols
// accrue it: APY = (1 + APR/secondsPerYear)^secondsPerYear - 1.
func aprToAPYPct(apr float64) float64 {
	const n = 365 * 24 * 60 * 60
	if apr <= 0 {
		return 0
	}
	return 100 * (math.Pow(1+apr/n, n) - 1)
}

func summarizeLending(markets []LendingMarket, savaxSupply *big.Int) LendingSummary {
	sum := LendingSummary{
		AVAX:  assetSummary(markets, lendingAssetClasses["avax"]),
		SAVAX: assetSummary(markets, lendingAssetClasses["savax"]),
	}
	if savaxSupply != nil && savaxSupply.Sign() > 0 {
		supplied, _ := new(big.Int).SetString(sum.SAVAX.Supplied, 10)
		pct := 100 * bigRatio(supplied, savaxSupply)
		sum.SAVAX.ShareOfTokenSupplyPct = &pct
	}
	byProto := map[string]*LendingUSDTotal{}
	sum.Totals.ByProtocol = []LendingUSDTotal{}
	for _, p := range lendingProtocols {
		byProto[p.ID] = &LendingUSDTotal{ProtocolID: p.ID}
	}
	for _, m := range markets {
		t, ok := byProto[m.ProtocolID]
		if !ok {
			continue
		}
		if m.SuppliedUSD == nil {
			if m.supplied != nil && m.supplied.Sign() > 0 {
				t.UnpricedMarkets++
			}
			continue
		}
		t.SuppliedUSD += *m.SuppliedUSD
		t.BorrowedUSD += *m.BorrowedUSD
	}
	for _, p := range lendingProtocols {
		t := byProto[p.ID]
		sum.Totals.SuppliedUSD += t.SuppliedUSD
		sum.Totals.BorrowedUSD += t.BorrowedUSD
		sum.Totals.ByProtocol = append(sum.Totals.ByProtocol, *t)
	}
	return sum
}

func assetSummary(markets []LendingMarket, assets []string) LendingAssetSummary {
	supplied, borrowed := new(big.Int), new(big.Int)
	out := LendingAssetSummary{ByProtocol: []LendingAssetFlow{}}
	for _, p := range lendingProtocols {
		ps, pb := new(big.Int), new(big.Int)
		found := false
		for _, m := range markets {
			if m.ProtocolID == p.ID && slices.Contains(assets, m.Asset) {
				ps.Add(ps, m.supplied)
				pb.Add(pb, m.borrowed)
				found = true
			}
		}
		if !found {
			continue
		}
		supplied.Add(supplied, ps)
		borrowed.Add(borrowed, pb)
		out.ByProtocol = append(out.ByProtocol, LendingAssetFlow{ProtocolID: p.ID, Supplied: ps.String(), Borrowed: pb.String()})
	}
	out.Supplied, out.Borrowed = supplied.String(), borrowed.String()
	return out
}

// LendingPoint is one day of an asset's lending across protocols.
type LendingPoint struct {
	Date       string                 `json:"date"`
	Supplied   string                 `json:"supplied"`
	Borrowed   string                 `json:"borrowed"`
	ByProtocol []LendingProtocolPoint `json:"by_protocol"`
}

// LendingProtocolPoint is one protocol's market for the asset on a day.
type LendingProtocolPoint struct {
	ProtocolID   string  `json:"protocol_id"`
	Supplied     string  `json:"supplied"`
	Borrowed     string  `json:"borrowed"`
	SupplyAPYPct float64 `json:"supply_apy_percent"`
	BorrowAPYPct float64 `json:"borrow_apy_percent"`
}

// handleLendingTimeseries returns daily lending history for AVAX or sAVAX
// @Summary Lending history for AVAX or sAVAX
// @Description Daily supplied, borrowed and rates of AVAX (WAVAX on Aave, native AVAX on BENQI) or sAVAX across Aave V3, Aave V4 and BENQI, from the last sample of each UTC day. Amounts are wei strings (18 decimals). With market=<protocol_id>:<asset>, returns that one market's daily history instead. Past days are read at the day's last block on the archive node, about a year back. Oldest first.
// @Tags Metrics - Staking
// @Produce json
// @Param asset query string false "avax or savax (when market is not set)"
// @Param market query string false "One market as <protocol_id>:<asset address>, e.g. aave-v3:0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7. Returns []LendingMarketPoint instead"
// @Param days query int false "Days back, 1-3650" default(365)
// @Success 200 {object} Response{data=[]LendingPoint}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending/timeseries [get]
func (s *Server) handleLendingTimeseries(w http.ResponseWriter, r *http.Request) {
	days, ok := lendingDays(w, r)
	if !ok {
		return
	}
	if m := r.URL.Query().Get("market"); m != "" {
		s.lendingMarketSeries(w, r, m, days)
		return
	}
	assets, ok := lendingAssetClasses[r.URL.Query().Get("asset")]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "asset must be avax or savax, or pass market=<protocol_id>:<asset>")
		return
	}

	rows, err := s.conn.Query(r.Context(), `
		SELECT toString(snapshot_date), protocol_id, toString(supplied), toString(borrowed), supply_apr, borrow_apr
		FROM lending_market_snapshots FINAL
		WHERE has(?, asset) AND snapshot_date >= today() - ?
		ORDER BY snapshot_date, protocol_id
	`, assets, days)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	out := []LendingPoint{}
	var supplied, borrowed *big.Int
	for rows.Next() {
		var date, s1, b1 string
		var p LendingProtocolPoint
		var sapr, bapr float64
		if err := rows.Scan(&date, &p.ProtocolID, &s1, &b1, &sapr, &bapr); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		ps, ok1 := new(big.Int).SetString(s1, 10)
		pb, ok2 := new(big.Int).SetString(b1, 10)
		if !ok1 || !ok2 {
			continue
		}
		if len(out) == 0 || out[len(out)-1].Date != date {
			out = append(out, LendingPoint{Date: date, ByProtocol: []LendingProtocolPoint{}})
			supplied, borrowed = new(big.Int), new(big.Int)
		}
		pt := &out[len(out)-1]
		supplied.Add(supplied, ps)
		borrowed.Add(borrowed, pb)
		pt.Supplied, pt.Borrowed = supplied.String(), borrowed.String()
		p.Supplied, p.Borrowed = ps.String(), pb.String()
		p.SupplyAPYPct, p.BorrowAPYPct = aprToAPYPct(sapr), aprToAPYPct(bapr)
		pt.ByProtocol = append(pt.ByProtocol, p)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}

func bpsPct(v *uint16) *float64 {
	if v == nil {
		return nil
	}
	p := float64(*v) / 100
	return &p
}

func tokenAmount(v *big.Int, decimals uint8) float64 {
	f, _ := new(big.Rat).SetFrac(v, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)).Float64()
	return f
}

func usdOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func lendingDays(w http.ResponseWriter, r *http.Request) (int, bool) {
	days := 365
	if q := r.URL.Query().Get("days"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 3650 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "days must be 1-3650")
			return 0, false
		}
		days = n
	}
	return days, true
}

// LendingMarketPoint is one market on one day. Amounts are token base units.
type LendingMarketPoint struct {
	Date           string   `json:"date"`
	TotalSupplied  string   `json:"total_supplied"`
	TotalBorrowed  string   `json:"total_borrowed"`
	SuppliedUSD    *float64 `json:"supplied_usd"`
	BorrowedUSD    *float64 `json:"borrowed_usd"`
	UtilizationPct *float64 `json:"utilization_percent"`
	SupplyAPYPct   float64  `json:"supply_apy_percent"`
	BorrowAPYPct   float64  `json:"borrow_apy_percent"`
	PriceUSD       *float64 `json:"price_usd"`
}

func (s *Server) lendingMarketSeries(w http.ResponseWriter, r *http.Request, market string, days int) {
	protocol, asset, ok := strings.Cut(market, ":")
	asset = strings.ToLower(asset)
	if !ok || !isLendingProtocol(protocol) || !isHexAddress(asset) {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "market must be <protocol_id>:<asset address>")
		return
	}
	rows, err := s.conn.Query(r.Context(), `
		SELECT toString(snapshot_date), decimals, toString(supplied), toString(borrowed), supply_apr, borrow_apr, price_usd
		FROM lending_market_snapshots FINAL
		WHERE protocol_id = ? AND asset = ? AND snapshot_date >= today() - ?
		ORDER BY snapshot_date
	`, protocol, asset, days)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()
	out := []LendingMarketPoint{}
	for rows.Next() {
		var m LendingMarket
		var date, supplied, borrowed string
		if err := rows.Scan(&date, &m.Decimals, &supplied, &borrowed, &m.supplyAPR, &m.borrowAPR, &m.PriceUSD); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		fillLendingMarket(&m, supplied, borrowed)
		out = append(out, LendingMarketPoint{
			Date: date, TotalSupplied: m.TotalSupplied, TotalBorrowed: m.TotalBorrowed,
			SuppliedUSD: m.SuppliedUSD, BorrowedUSD: m.BorrowedUSD, UtilizationPct: m.UtilizationPct,
			SupplyAPYPct: m.SupplyAPYPct, BorrowAPYPct: m.BorrowAPYPct, PriceUSD: m.PriceUSD,
		})
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}

// LendingTotalsPoint is one day's USD supply and borrow, overall and per
// protocol. Markets without an oracle price that day are left out.
type LendingTotalsPoint struct {
	Date            string            `json:"date"`
	SuppliedUSD     float64           `json:"supplied_usd"`
	BorrowedUSD     float64           `json:"borrowed_usd"`
	UnpricedMarkets int               `json:"unpriced_markets"` // across protocols, see LendingUSDTotal
	ByProtocol      []LendingUSDTotal `json:"by_protocol"`
}

// handleLendingTotals returns daily USD supply and borrow per protocol
// @Summary Lending USD totals history
// @Description Daily supplied and borrowed USD per protocol (Aave V3, Aave V4, BENQI) and overall, from each market's daily sample and its oracle price that day. Markets without a price are left out. Oldest first. Cached for 10 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Param days query int false "Days back, 1-3650" default(365)
// @Success 200 {object} Response{data=[]LendingTotalsPoint}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending/timeseries/totals [get]
func (s *Server) handleLendingTotals(w http.ResponseWriter, r *http.Request) {
	days, ok := lendingDays(w, r)
	if !ok {
		return
	}
	v, err := lendingCache.get(r.Context(), fmt.Sprintf("totals:%d", days), func(ctx context.Context) (interface{}, error) {
		rows, err := s.conn.Query(ctx, `
			SELECT toString(snapshot_date), protocol_id,
				sumIf(toFloat64(supplied) / pow(10, decimals) * assumeNotNull(price_usd), price_usd IS NOT NULL),
				sumIf(toFloat64(borrowed) / pow(10, decimals) * assumeNotNull(price_usd), price_usd IS NOT NULL),
				toInt64(countIf(price_usd IS NULL AND supplied > 0))
			FROM lending_market_snapshots FINAL
			WHERE snapshot_date >= today() - ?
			GROUP BY snapshot_date, protocol_id
			ORDER BY snapshot_date, protocol_id
		`, days)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []LendingTotalsPoint{}
		for rows.Next() {
			var date string
			var t LendingUSDTotal
			var unpriced int64
			if err := rows.Scan(&date, &t.ProtocolID, &t.SuppliedUSD, &t.BorrowedUSD, &unpriced); err != nil {
				return nil, err
			}
			if len(out) == 0 || out[len(out)-1].Date != date {
				out = append(out, LendingTotalsPoint{Date: date, ByProtocol: []LendingUSDTotal{}})
			}
			t.UnpricedMarkets = int(unpriced)
			p := &out[len(out)-1]
			p.UnpricedMarkets += t.UnpricedMarkets
			p.SuppliedUSD += t.SuppliedUSD
			p.BorrowedUSD += t.BorrowedUSD
			p.ByProtocol = append(p.ByProtocol, t)
		}
		return out, rows.Err()
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func isLendingProtocol(id string) bool {
	for _, p := range lendingProtocols {
		if p.ID == id {
			return true
		}
	}
	return false
}

func isHexAddress(s string) bool {
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}

// loadLendingEMode reads each protocol's newest collateral-group sample, fills
// the markets' emode entries and returns the groups.
func (s *Server) loadLendingEMode(ctx context.Context, markets []LendingMarket) ([]LendingEModeCategory, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT protocol_id, group_id, group_label, asset, ltv_bps, configured_ltv_bps,
			liquidation_threshold_bps, liquidation_bonus_bps, as_collateral, borrowable, previous_ltv_bps
		FROM (SELECT * FROM lending_emode_snapshots FINAL) t
		WHERE (protocol_id, sampled_at) IN (
			SELECT protocol_id, max(sampled_at) FROM lending_emode_snapshots GROUP BY protocol_id)
		ORDER BY protocol_id, length(group_id), group_id, asset
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byMarket := map[string]int{}
	for i, m := range markets {
		byMarket[m.ProtocolID+"|"+m.Asset] = i
	}
	cats := []LendingEModeCategory{}
	idx := map[string]int{}
	for rows.Next() {
		var protocol, asset string
		var e LendingMarketEMode
		var ltv, conf, lt, bonus uint16
		var prev *uint16
		if err := rows.Scan(&protocol, &e.ID, &e.Label, &asset, &ltv, &conf, &lt, &bonus, &e.AsCollateral, &e.Borrowable, &prev); err != nil {
			return nil, err
		}
		e.LTVPct, e.ConfiguredLTVPct = float64(ltv)/100, float64(conf)/100
		e.LiquidationThresholdPct, e.LiquidationBonusPct = float64(lt)/100, float64(bonus)/100
		e.PreviousLTVPct = bpsPct(prev)
		e.ltvBps, e.protocol = conf, protocol

		key := protocol + "|" + e.ID
		ci, ok := idx[key]
		if !ok {
			c := LendingEModeCategory{ProtocolID: protocol, ID: e.ID, Label: e.Label,
				CollateralAssets: []string{}, BorrowableAssets: []string{}, LTVZeroAssets: []string{}}
			if protocol == lendingmarkets.ProtocolAaveV3 {
				ltvPct, ltPct, bonusPct := e.ConfiguredLTVPct, e.LiquidationThresholdPct, e.LiquidationBonusPct
				c.LTVPct, c.LiquidationThresholdPct, c.LiquidationBonusPct = &ltvPct, &ltPct, &bonusPct
			}
			cats = append(cats, c)
			ci = len(cats) - 1
			idx[key] = ci
		}
		c := &cats[ci]
		if e.AsCollateral {
			c.CollateralAssets = append(c.CollateralAssets, asset)
			if ltv == 0 {
				c.LTVZeroAssets = append(c.LTVZeroAssets, asset)
			}
		}
		if e.Borrowable {
			c.BorrowableAssets = append(c.BorrowableAssets, asset)
		}
		if i, ok := byMarket[protocol+"|"+asset]; ok {
			markets[i].EMode = append(markets[i].EMode, e)
		}
	}
	return cats, rows.Err()
}

// fillLTVChanges dates each market's LTV and each Aave V4 spoke entry's from
// lending_ltv_changes. A stored row counts only while its LTV is still the
// current one. A failed read leaves the dates null rather than failing the
// overview.
func (s *Server) fillLTVChanges(ctx context.Context, markets []LendingMarket) {
	rows, err := s.conn.Query(ctx, `
		SELECT protocol_id, group_id, asset, ltv_bps, changed_at, changed_tx
		FROM lending_ltv_changes FINAL
		WHERE changed_at IS NOT NULL`)
	if err != nil {
		slog.Warn("Failed to read lending LTV changes", "error", err)
		return
	}
	defer rows.Close()
	type change struct {
		ltv uint16
		at  time.Time
		tx  string
	}
	changes := map[string]change{}
	for rows.Next() {
		var protocol, group, asset string
		var c change
		var at *time.Time
		if err := rows.Scan(&protocol, &group, &asset, &c.ltv, &at, &c.tx); err != nil {
			slog.Warn("Failed to read lending LTV changes", "error", err)
			return
		}
		if at != nil {
			c.at = at.UTC()
			changes[protocol+"|"+group+"|"+asset] = c
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("Failed to read lending LTV changes", "error", err)
		return
	}
	set := func(key string, ltv uint16, at **time.Time, tx **string) {
		c, ok := changes[key]
		if !ok || c.ltv != ltv {
			return
		}
		t := c.at
		*at = &t
		if c.tx != "" {
			h := c.tx
			*tx = &h
		}
	}
	for i := range markets {
		m := &markets[i]
		if m.ltvBps != nil {
			set(m.ProtocolID+"||"+m.Asset, *m.ltvBps, &m.LTVChangedAt, &m.LTVChangedTx)
		}
		for j := range m.EMode {
			e := &m.EMode[j]
			if e.protocol == lendingmarkets.ProtocolAaveV4 {
				set(e.protocol+"|"+e.ID+"|"+m.Asset, e.ltvBps, &e.LTVChangedAt, &e.LTVChangedTx)
			}
		}
	}
}
