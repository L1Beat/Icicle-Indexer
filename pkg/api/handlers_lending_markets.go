package api

import (
	"context"
	"math"
	"math/big"
	"net/http"
	"slices"
	"sort"
	"strconv"
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
	supplied, borrowed      *big.Int
	supplyAPR, borrowAPR    float64
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

// LendingOverview is the /metrics/lending payload.
type LendingOverview struct {
	Protocols []LendingProtocol `json:"protocols"`
	Markets   []LendingMarket   `json:"markets"`
	Summary   LendingSummary    `json:"summary"`
	SampledAt *time.Time        `json:"sampled_at"`
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
// @Description Supply, borrow, rates and risk parameters of every market on Aave V3, Aave V4 (Core Hub) and BENQI Lending, read from the contracts by the indexer every validator sync cycle (about 5 minutes) and cached for 2 minutes. Amounts are token base units. APYs compound the per-second rate. USD values use each protocol's own oracle and are null when its read failed. The summary sums AVAX (WAVAX on Aave, native AVAX on BENQI) and sAVAX across protocols.
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
		fillLendingMarket(&m, supplied, borrowed)
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

	out := LendingOverview{Protocols: lendingProtocols, Markets: markets, Summary: summarizeLending(markets, savaxSupply)}
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
		if !ok || m.SuppliedUSD == nil {
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
// @Description Daily supplied, borrowed and rates of AVAX (WAVAX on Aave, native AVAX on BENQI) or sAVAX across Aave V3, Aave V4 and BENQI, from the last sample of each UTC day. Amounts are wei strings (18 decimals). History starts 2026-11-01. Oldest first.
// @Tags Metrics - Staking
// @Produce json
// @Param asset query string true "avax or savax"
// @Param days query int false "Days back, 1-3650" default(365)
// @Success 200 {object} Response{data=[]LendingPoint}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/lending/timeseries [get]
func (s *Server) handleLendingTimeseries(w http.ResponseWriter, r *http.Request) {
	assets, ok := lendingAssetClasses[r.URL.Query().Get("asset")]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "asset must be avax or savax")
		return
	}
	days := 365
	if q := r.URL.Query().Get("days"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 3650 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "days must be 1-3650")
			return
		}
		days = n
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
