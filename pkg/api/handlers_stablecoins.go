package api

import (
	"context"
	"encoding/hex"
	"log/slog"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Stablecoin represents a curated stablecoin with current supply / holder / 24h volume stats.
//
// Supply is "circulating": balances held by addresses listed in stablecoin_excluded_holders
// (e.g. issuer treasuries) are subtracted, so the number matches DeFiLlama-style methodology
// rather than raw on-chain totalSupply.
type Stablecoin struct {
	Token    string `json:"token" example:"0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"`
	Symbol   string `json:"symbol" example:"USDC"`
	Name     string `json:"name" example:"USD Coin"`
	Decimals uint8  `json:"decimals" example:"6"`
	// Peg is the currency a unit tracks. On /tokenized-funds it is "USD" for
	// $1-per-share money-market funds and "NAV" for floating-NAV funds (credit or
	// CLO funds such as ACRED and JAAA), whose share price the API does not carry.
	Peg     string `json:"peg" example:"USD"`
	Issuer  string `json:"issuer,omitempty" example:"Circle"`
	Bridged bool   `json:"bridged" example:"false"`
	// Doublecounted marks stablecoins whose reserves are themselves other counted
	// stablecoins (e.g. avUSD, UTY, reUSD, FRXUSD). Sum all rows for the GROSS total;
	// exclude doublecounted rows for the NET total (the DeFiLlama-style headline).
	Doublecounted bool `json:"doublecounted" example:"false"`
	// Category is "stablecoin" on /stablecoins, "tokenized_fund" on
	// /tokenized-funds (fund shares such as BUIDL and JAAA), and
	// "tokenized_fund", "equity" (company shares such as SECZ) or "commodity"
	// (such as gold token XAUt0) on /rwa.
	Category     string `json:"category" example:"stablecoin" enums:"stablecoin,tokenized_fund,equity,commodity"`
	Supply       string `json:"supply" example:"459300000000000"` // token base units, circulating
	Holders      uint64 `json:"holders" example:"128453"`
	Volume24h    string `json:"volume_24h" example:"32100000000000"` // token base units
	Transfers24h uint64 `json:"transfers_24h" example:"58210"`

	// On-chain share price of a floating-NAV tokenized fund, from
	// tokenized_fund_prices. Served only on /tokenized-funds.
	navPrice    *float64
	navSource   string
	navPriceAt  time.Time // when the NAV was set
	navSampleAt time.Time // when it was last read
}

// TokenizedFund is a /tokenized-funds row: the /stablecoins fields plus a
// share price and USD value. A $1-per-share fund (peg USD) is priced at 1 from
// its peg. A floating-NAV fund (peg NAV) is priced only when its contract
// reports a price on-chain (an ERC-4626 vault over a USD stablecoin); otherwise
// the price fields are null.
type TokenizedFund struct {
	Stablecoin
	PriceUSD    *float64   `json:"price_usd" example:"1.065417"`                                                  // USD per whole share, null when unknown
	ValueUSD    *float64   `json:"value_usd" example:"26483900.5"`                                                // supply in whole shares times price_usd, null when unknown
	PriceSource *string    `json:"price_source" example:"erc4626" enums:"peg,erc4626,erc7540,redstone,chainlink"` // peg: $1 per share. erc4626: the fund vault's convertToAssets. erc7540: convertToAssets of the fund's ERC-7540 vault (Centrifuge, Nest). redstone: the asset's RedStone NAV or price feed. chainlink: the asset's Chainlink price feed (XAU / USD for XAUt0)
	PriceAt     *time.Time `json:"price_at"`                                                                      // when the NAV was set (Centrifuge priceLastUpdated, RedStone or Chainlink updatedAt), else when it was read. Null for peg prices
}

// An on-chain fund price is dropped when it was last read more than
// fundPriceMaxReadAge ago (the snapshotter reads every few minutes, so the
// reader is broken) or the NAV itself is older than fundNAVMaxAge (issuers
// update daily or on business days, so the fund stopped updating).
const (
	fundPriceMaxReadAge = 24 * time.Hour
	fundNAVMaxAge       = 7 * 24 * time.Hour
)

// Registry categories. Tokenized money-market funds (BUIDL, BENJI) share the
// stablecoin pipeline but are not stablecoins, so they are served apart and
// never counted in stablecoin totals.
const (
	categoryStablecoin    = "stablecoin"
	categoryTokenizedFund = "tokenized_fund"
	categoryEquity        = "equity"
	categoryCommodity     = "commodity"
)

// rwaCategories are the registry categories served by /rwa: every one but
// stablecoins.
var rwaCategories = []string{categoryTokenizedFund, categoryEquity, categoryCommodity}

// handleListStablecoins returns curated stablecoins for a chain with circulating supply,
// holder count, and last-24h transfer volume / count. Volume leaves out issuer
// operations: mints, burns, and transfers to or from an excluded issuer treasury.
// Tokenized funds are left out: they are listed on /tokenized-funds.
//
// @Summary List stablecoins for a chain
// @Description Returns curated stablecoins with circulating supply (excludes addresses listed in stablecoin_excluded_holders, e.g. issuer treasuries), holder count, and 24h transfer stats. Tokenized money-market funds (BUIDL, BENJI) are not stablecoins and are listed on /tokenized-funds instead.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Success 200 {object} Response{data=[]Stablecoin}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/stablecoins [get]
func (s *Server) handleListStablecoins(w http.ResponseWriter, r *http.Request) {
	all, ok := s.cachedRegistry(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: filterCategory(all, []string{categoryStablecoin})})
}

// handleListTokenizedFunds returns the tokenized money-market funds in the
// stablecoin registry, with the same fields and methodology as /stablecoins.
//
// @Summary List tokenized funds for a chain
// @Description Returns tokenized money-market funds (e.g. BlackRock BUIDL, Franklin Templeton BENJI) with circulating supply (excludes addresses listed in stablecoin_excluded_holders), holder count, and 24h transfer stats. Same shape as /stablecoins. These are fund shares, not stablecoins, so they are not part of the /stablecoins list. supply is in shares. price_usd and value_usd: peg USD funds are priced at $1 per share (price_source peg). Peg NAV funds have a floating share price, priced only when it is reported on-chain in a USD stablecoin, read every few minutes: the fund is an ERC-4626 vault (price_source erc4626), or its share token has an ERC-7540 vault such as Centrifuge's (price_source erc7540), or it has a RedStone NAV feed (price_source redstone). Otherwise the price fields are null. A price whose NAV is more than 7 days old is null. Never value a NAV fund at $1.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Success 200 {object} Response{data=[]TokenizedFund}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/tokenized-funds [get]
func (s *Server) handleListTokenizedFunds(w http.ResponseWriter, r *http.Request) {
	s.listPriced(w, r, []string{categoryTokenizedFund}, false)
}

// handleListRWA returns every non-stablecoin asset in the registry: tokenized
// funds, equity and commodities, priced like /tokenized-funds.
//
// @Summary List tokenized real-world assets for a chain
// @Description Returns every tokenized real-world asset in the registry that is not a stablecoin: fund shares (category tokenized_fund, the /tokenized-funds rows), company shares (category equity, e.g. Securitize SECZ) and commodities (category commodity, e.g. Tether gold XAUt0, peg XAU, one token per troy ounce). Same row shape and price fields as /tokenized-funds. supply is in token units. An equity price comes from its RedStone end-of-day price feed (price_source redstone). XAUt0 is priced per troy ounce from Chainlink's XAU / USD feed (price_source chainlink), which does not update while gold markets are closed. Rows are sorted by value_usd, highest first, unpriced rows last.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Success 200 {object} Response{data=[]TokenizedFund}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/rwa [get]
func (s *Server) handleListRWA(w http.ResponseWriter, r *http.Request) {
	s.listPriced(w, r, rwaCategories, true)
}

// listPriced serves the registry rows of the given categories with their
// share price and USD value. byValue sorts them highest value first; otherwise
// they keep the registry list's order (by raw supply).
func (s *Server) listPriced(w http.ResponseWriter, r *http.Request, categories []string, byValue bool) {
	all, ok := s.cachedRegistry(w, r)
	if !ok {
		return
	}
	rows := filterCategory(all, categories)
	out := make([]TokenizedFund, 0, len(rows))
	now := time.Now()
	for _, f := range rows {
		out = append(out, priceFund(f, now))
	}
	if byValue {
		sortByValue(out)
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}

// sortByValue orders rows by value_usd, highest first. Unpriced rows go last,
// in their existing order.
func sortByValue(rows []TokenizedFund) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].ValueUSD, rows[j].ValueUSD
		if a == nil || b == nil {
			return a != nil && b == nil
		}
		return *a > *b
	})
}

// priceFund adds the share price and USD value to a fund row.
func priceFund(f Stablecoin, now time.Time) TokenizedFund {
	out := TokenizedFund{Stablecoin: f}
	var price float64
	switch {
	case f.Peg == "USD":
		price = 1
		src := "peg"
		out.PriceSource = &src
	case f.navPrice != nil && now.Sub(f.navSampleAt) <= fundPriceMaxReadAge && now.Sub(f.navPriceAt) <= fundNAVMaxAge:
		price = *f.navPrice
		src, at := f.navSource, f.navPriceAt
		out.PriceSource, out.PriceAt = &src, &at
	default:
		return out
	}
	out.PriceUSD = &price
	if supply, ok := new(big.Float).SetString(f.Supply); ok {
		unit := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(f.Decimals)), nil))
		v, _ := new(big.Float).Mul(new(big.Float).Quo(supply, unit), big.NewFloat(price)).Float64()
		out.ValueUSD = &v
	}
	return out
}

// cachedRegistry returns the cached registry list for the path's chain, or
// writes the error response. The cache holds every registry token, so both
// list endpoints share one ~8s load.
func (s *Server) cachedRegistry(w http.ResponseWriter, r *http.Request) ([]Stablecoin, bool) {
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return nil, false
	}
	all, err := s.stablecoins.get(r.Context(), chainID, s.loadStablecoins)
	if err != nil {
		writeInternalError(w, err.Error())
		return nil, false
	}
	return all, true
}

// filterCategory returns the entries of the given categories, keeping their
// order. It never returns nil, so an empty result encodes as [].
func filterCategory(all []Stablecoin, categories []string) []Stablecoin {
	out := make([]Stablecoin, 0, len(all))
	for _, c := range all {
		for _, want := range categories {
			if c.Category == want {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// registryEntry is a registry row being filled in by loadStablecoins.
type registryEntry struct {
	Stablecoin
	tokenBytes []byte
}

// loadStablecoins runs the registry, supply and 24h volume queries for every
// registry token, of every category. Together
// they take ~8s (measured 2026-10-26), so the handler serves them through
// stablecoinCache rather than per request.
func (s *Server) loadStablecoins(ctx context.Context, chainID uint32) ([]Stablecoin, error) {
	registryQuery := `
		SELECT token, symbol, name, decimals, peg, issuer, bridged, doublecounted, category
		FROM stablecoins FINAL
		WHERE chain_id = ?
	`
	rows, err := s.conn.Query(ctx, registryQuery, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byToken := map[string]*registryEntry{}
	var ordered []*registryEntry
	for rows.Next() {
		var e registryEntry
		if err := rows.Scan(&e.tokenBytes, &e.Symbol, &e.Name, &e.Decimals, &e.Peg, &e.Issuer, &e.Bridged, &e.Doublecounted, &e.Category); err != nil {
			return nil, err
		}
		e.Token = "0x" + hex.EncodeToString(e.tokenBytes)
		// Tokens with no balances or no counted transfers in 24h get no row
		// from the queries below and keep these zeros.
		e.Supply, e.Volume24h = "0", "0"
		byToken[e.Token] = &e
		ordered = append(ordered, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ordered) == 0 {
		return []Stablecoin{}, nil
	}

	supplyQuery := `
		SELECT
			token,
			toString(sum(balance)) AS supply_total,
			countIf(balance > toInt256(0)) AS holders
		FROM erc20_balances
		WHERE chain_id = ?
		  AND token IN (SELECT token FROM stablecoins FINAL WHERE chain_id = ?)
		  AND (token, wallet) NOT IN (
		      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = ?
		  )
		GROUP BY token
		SETTINGS max_memory_usage = 8000000000
	`
	supplyRows, err := s.conn.Query(ctx, supplyQuery, chainID, chainID, chainID)
	if err != nil {
		return nil, err
	}
	defer supplyRows.Close()

	for supplyRows.Next() {
		var (
			tokenBytes []byte
			supply     string
			holders    uint64
		)
		if err := supplyRows.Scan(&tokenBytes, &supply, &holders); err != nil {
			return nil, err
		}
		key := "0x" + hex.EncodeToString(tokenBytes)
		if e, ok := byToken[key]; ok {
			e.Supply = supply
			e.Holders = holders
		}
	}
	if err := supplyRows.Err(); err != nil {
		return nil, err
	}

	volumeQuery := `
		SELECT
			address AS token,
			toString(sum(reinterpretAsUInt256(reverse(data)))) AS volume_amount,
			count() AS transfers_count
		FROM raw_logs
		WHERE chain_id = ?
		  AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = ?)
		  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
		  AND length(data) = 32
		  AND block_time >= now() - INTERVAL 24 HOUR
		  AND substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
		  AND substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
		  AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
		      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = ?)
		  AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
		      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = ?)
		GROUP BY address
	`
	volRows, err := s.conn.Query(ctx, volumeQuery, chainID, chainID, chainID, chainID)
	if err != nil {
		return nil, err
	}
	defer volRows.Close()

	for volRows.Next() {
		var (
			tokenBytes   []byte
			volume24h    string
			transfers24h uint64
		)
		if err := volRows.Scan(&tokenBytes, &volume24h, &transfers24h); err != nil {
			return nil, err
		}
		key := "0x" + hex.EncodeToString(tokenBytes)
		if e, ok := byToken[key]; ok {
			e.Volume24h = volume24h
			e.Transfers24h = transfers24h
		}
	}
	if err := volRows.Err(); err != nil {
		return nil, err
	}

	// Fund share prices are optional: a failed read leaves funds unpriced and
	// never fails the list.
	if err := s.loadFundPrices(ctx, chainID, byToken); err != nil {
		slog.Warn("Tokenized fund price read failed", "chain_id", chainID, "error", err)
	}

	sort.Slice(ordered, func(i, j int) bool {
		a, _ := new(big.Int).SetString(ordered[i].Supply, 10)
		b, _ := new(big.Int).SetString(ordered[j].Supply, 10)
		if a == nil {
			a = new(big.Int)
		}
		if b == nil {
			b = new(big.Int)
		}
		return a.Cmp(b) > 0
	})

	out := make([]Stablecoin, 0, len(ordered))
	for _, e := range ordered {
		out = append(out, e.Stablecoin)
	}

	return out, nil
}

// loadFundPrices attaches the latest on-chain share price to each fund in
// byToken that has one.
func (s *Server) loadFundPrices(ctx context.Context, chainID uint32, byToken map[string]*registryEntry) error {
	rows, err := s.conn.Query(ctx, `
		SELECT token, price_usd, source, price_at, sampled_at
		FROM tokenized_fund_prices FINAL
		WHERE chain_id = ?
	`, chainID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			tokenBytes []byte
			price      float64
			source     string
			priceAt    time.Time
			sampledAt  time.Time
		)
		if err := rows.Scan(&tokenBytes, &price, &source, &priceAt, &sampledAt); err != nil {
			return err
		}
		if e, ok := byToken["0x"+hex.EncodeToString(tokenBytes)]; ok {
			p := price
			e.navPrice, e.navSource, e.navPriceAt, e.navSampleAt = &p, source, priceAt, sampledAt
		}
	}
	return rows.Err()
}

type StablecoinMetricDataPoint struct {
	Period time.Time `json:"period"`
	Value  string    `json:"value"` // token base units for supply and volume, a count for transfers and holders
}

type StablecoinMetricSeries struct {
	ChainID     uint32                      `json:"chain_id"`
	Token       string                      `json:"token"`
	MetricName  string                      `json:"metric_name"`
	Granularity string                      `json:"granularity"`
	Data        []StablecoinMetricDataPoint `json:"data"`
}

// handleStablecoinTimeseries returns time-series data for stablecoin metrics.
// Tokenized funds are left out: their series are on /tokenized-funds/timeseries.
//
// @Summary Get stablecoin metric time series
// @Description Returns time-series data for a stablecoin metric (supply, volume, transfers, holders). Tokenized funds (BUIDL, BENJI) are not included, also when asked for by token: use /tokenized-funds/timeseries.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Param metric query string true "Metric name (supply, volume, transfers, holders)"
// @Param granularity query string false "Time granularity (hour, day, week, month)" default(day)
// @Param token query string false "Token address (0x-prefixed hex). Omit for all stablecoins."
// @Param from query string false "Start time (date, RFC3339, or unix timestamp)"
// @Param to query string false "End time (date, RFC3339, or unix timestamp)"
// @Param limit query int false "Number of data points per token (max 5000)" default(365)
// @Success 200 {object} Response{data=[]StablecoinMetricSeries}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/stablecoins/timeseries [get]
func (s *Server) handleStablecoinTimeseries(w http.ResponseWriter, r *http.Request) {
	s.registryTimeseries(w, r, []string{categoryStablecoin})
}

// handleTokenizedFundTimeseries returns time-series data for tokenized funds.
//
// @Summary Get tokenized fund metric time series
// @Description Returns time-series data for a tokenized money-market fund metric (supply, volume, transfers, holders). Same parameters and shape as /stablecoins/timeseries.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Param metric query string true "Metric name (supply, volume, transfers, holders)"
// @Param granularity query string false "Time granularity (hour, day, week, month)" default(day)
// @Param token query string false "Token address (0x-prefixed hex). Omit for all tokenized funds."
// @Param from query string false "Start time (date, RFC3339, or unix timestamp)"
// @Param to query string false "End time (date, RFC3339, or unix timestamp)"
// @Param limit query int false "Number of data points per token (max 5000)" default(365)
// @Success 200 {object} Response{data=[]StablecoinMetricSeries}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/tokenized-funds/timeseries [get]
func (s *Server) handleTokenizedFundTimeseries(w http.ResponseWriter, r *http.Request) {
	s.registryTimeseries(w, r, []string{categoryTokenizedFund})
}

// handleRWATimeseries returns time-series data for tokenized real-world assets.
//
// @Summary Get tokenized real-world asset metric time series
// @Description Returns time-series data for a metric (supply, volume, transfers, holders) of every non-stablecoin registry asset: tokenized funds, equity and commodities. Same parameters and shape as /stablecoins/timeseries.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Param metric query string true "Metric name (supply, volume, transfers, holders)"
// @Param granularity query string false "Time granularity (hour, day, week, month)" default(day)
// @Param token query string false "Token address (0x-prefixed hex). Omit for all assets."
// @Param from query string false "Start time (date, RFC3339, or unix timestamp)"
// @Param to query string false "End time (date, RFC3339, or unix timestamp)"
// @Param limit query int false "Number of data points per token (max 5000)" default(365)
// @Success 200 {object} Response{data=[]StablecoinMetricSeries}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/rwa/timeseries [get]
func (s *Server) handleRWATimeseries(w http.ResponseWriter, r *http.Request) {
	s.registryTimeseries(w, r, rwaCategories)
}

// registryTimeseries serves stablecoin_metrics series for the registry tokens
// of the given categories.
func (s *Server) registryTimeseries(w http.ResponseWriter, r *http.Request, categories []string) {
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	metricName := r.URL.Query().Get("metric")
	validMetrics := map[string]bool{"supply": true, "volume": true, "transfers": true, "holders": true}
	if !validMetrics[metricName] {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid metric (use: supply, volume, transfers, holders)")
		return
	}

	granularity := r.URL.Query().Get("granularity")
	if granularity == "" {
		granularity = "day"
	}
	validGranularities := map[string]bool{"hour": true, "day": true, "week": true, "month": true}
	if !validGranularities[granularity] {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid granularity (use: hour, day, week, month)")
		return
	}

	var fromTime, toTime time.Time
	if from := r.URL.Query().Get("from"); from != "" {
		fromTime = parseFlexibleTime(from)
	}
	if to := r.URL.Query().Get("to"); to != "" {
		toTime = parseFlexibleTime(to)
	}

	// NOTE: parse limit locally instead of getPagination(), which hard-caps at 100.
	// Timeseries needs the full per-token history in one request (~1600+ daily points
	// back to 2021, and growing), so the shared paginator's 100 cap would silently
	// truncate daily series. 5000 ≈ 13.7 years of daily points — full history with
	// headroom, while still bounding the per-token row count.
	limit := 365
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 5000 {
		limit = 5000
	}

	// Supply is stored as per-period deltas (supply_change); compute cumulative at query time.
	dbMetric := metricName
	if metricName == "supply" {
		dbMetric = "supply_change"
	}

	var tokenFilter string
	var tokenArg []interface{}
	if tokenHex := r.URL.Query().Get("token"); tokenHex != "" {
		tokenHex = strings.TrimPrefix(tokenHex, "0x")
		tokenBytes, err := hex.DecodeString(tokenHex)
		if err != nil || len(tokenBytes) != 20 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid token address")
			return
		}
		tokenFilter = " AND sm.token = ?"
		tokenArg = append(tokenArg, string(tokenBytes))
	}
	// Scope to the category's registry tokens. stablecoin_metrics holds every
	// registry token, funds included.
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(categories)), ", ")
	categoryFilter := " AND sm.token IN (SELECT token FROM stablecoins FINAL WHERE chain_id = ? AND category IN (" + marks + "))"
	categoryArgs := []interface{}{chainID}
	for _, c := range categories {
		categoryArgs = append(categoryArgs, c)
	}

	var query string
	var args []interface{}

	if metricName == "supply" {
		// Running sum of supply_change deltas, then filter/limit the result
		inner := `
			SELECT sm.token, sm.period,
				toString(sum(toInt256(sm.value)) OVER (PARTITION BY sm.token ORDER BY sm.period)) AS value
			FROM (SELECT * FROM stablecoin_metrics FINAL) sm
			WHERE sm.chain_id = ?
			  AND sm.metric_name = 'supply_change'
			  AND sm.granularity = ?
		`
		args = append(args, chainID, granularity)
		inner += tokenFilter
		args = append(args, tokenArg...)
		inner += categoryFilter
		args = append(args, categoryArgs...)

		query = "SELECT token, period, value FROM (" + inner + ") WHERE 1=1"
		if !fromTime.IsZero() {
			query += " AND period >= ?"
			args = append(args, fromTime)
		}
		if !toTime.IsZero() {
			query += " AND period <= ?"
			args = append(args, toTime)
		}
		// LIMIT ? BY token returns the most-recent `limit` points for EACH token,
		// so omitting the token filter yields one series per stablecoin. A plain
		// global LIMIT would be consumed entirely by whichever token sorts first.
		query += " ORDER BY token, period DESC LIMIT ? BY token"
		args = append(args, limit)
	} else {
		query = `
			SELECT sm.token, sm.period, sm.value
			FROM (SELECT * FROM stablecoin_metrics FINAL) sm
			WHERE sm.chain_id = ?
			  AND sm.metric_name = ?
			  AND sm.granularity = ?
		`
		args = append(args, chainID, dbMetric, granularity)
		query += tokenFilter
		args = append(args, tokenArg...)
		query += categoryFilter
		args = append(args, categoryArgs...)

		if !fromTime.IsZero() {
			query += " AND sm.period >= ?"
			args = append(args, fromTime)
		}
		if !toTime.IsZero() {
			query += " AND sm.period <= ?"
			args = append(args, toTime)
		}
		// Per-token limit (see supply branch) so every stablecoin gets its own series.
		query += " ORDER BY sm.token, sm.period DESC LIMIT ? BY sm.token"
		args = append(args, limit)
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	seriesMap := map[string]*StablecoinMetricSeries{}
	var seriesOrder []string
	for rows.Next() {
		var (
			tokenBytes []byte
			period     time.Time
			value      string
		)
		if err := rows.Scan(&tokenBytes, &period, &value); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		key := "0x" + hex.EncodeToString(tokenBytes)
		series, ok := seriesMap[key]
		if !ok {
			series = &StablecoinMetricSeries{
				ChainID:     chainID,
				Token:       key,
				MetricName:  metricName,
				Granularity: granularity,
			}
			seriesMap[key] = series
			seriesOrder = append(seriesOrder, key)
		}
		if len(series.Data) < limit {
			series.Data = append(series.Data, StablecoinMetricDataPoint{
				Period: period,
				Value:  value,
			})
		}
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	for _, series := range seriesMap {
		for i, j := 0, len(series.Data)-1; i < j; i, j = i+1, j-1 {
			series.Data[i], series.Data[j] = series.Data[j], series.Data[i]
		}
	}

	out := make([]StablecoinMetricSeries, 0, len(seriesOrder))
	for _, key := range seriesOrder {
		out = append(out, *seriesMap[key])
	}

	writeJSON(w, http.StatusOK, Response{Data: out})
}
