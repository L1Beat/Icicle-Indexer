package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"icicle/pkg/perps"
)

// PerpWindow is the day range a perps response covers: the last Days complete
// UTC days ending AsOf, both inclusive.
type PerpWindow struct {
	AsOf *string `json:"as_of"` // last complete UTC day, null before the first one
	From *string `json:"from"`
	Days int     `json:"days"`
}

// PerpStats is one venue, or all of them, over the window.
type PerpStats struct {
	ID             string  `json:"id,omitempty"`
	Name           string  `json:"name,omitempty"`
	VolumeUSD      float64 `json:"volume_usd"`
	LongVolumeUSD  float64 `json:"long_volume_usd"`
	ShortVolumeUSD float64 `json:"short_volume_usd"`
	Trades         uint64  `json:"trades"`
	Traders        uint64  `json:"traders"`
	FeesUSD        float64 `json:"fees_usd"`
	Liquidations   uint64  `json:"liquidations"`
	LiquidatedUSD  float64 `json:"liquidated_usd"`
}

// PerpOverview is the /metrics/perps payload.
type PerpOverview struct {
	PerpWindow
	Totals  PerpStats   `json:"totals"`
	ByVenue []PerpStats `json:"by_venue"`
}

// PerpDayVenue is one venue on one day.
type PerpDayVenue struct {
	ID        string  `json:"id"`
	VolumeUSD float64 `json:"volume_usd"`
	Trades    uint64  `json:"trades"`
}

// PerpDay is one day across venues.
type PerpDay struct {
	Date          string         `json:"date"`
	VolumeUSD     float64        `json:"volume_usd"`
	Trades        uint64         `json:"trades"`
	FeesUSD       float64        `json:"fees_usd"`
	LiquidatedUSD float64        `json:"liquidated_usd"`
	Venues        []PerpDayVenue `json:"venues"`
}

// PerpMarketOI is one market's open interest on a day.
type PerpMarketOI struct {
	Venue    string  `json:"venue"`
	Market   string  `json:"market"`
	Name     string  `json:"name"` // e.g. AVAX/USD
	Pool     string  `json:"pool,omitempty"`
	LongUSD  float64 `json:"long_usd"`
	ShortUSD float64 `json:"short_usd"`
}

// PerpOIDay is open interest at the end of one day.
type PerpOIDay struct {
	Date     string         `json:"date"`
	LongUSD  float64        `json:"long_usd"`
	ShortUSD float64        `json:"short_usd"`
	ByMarket []PerpMarketOI `json:"by_market"`
}

// PerpLiquidation is one liquidation.
type PerpLiquidation struct {
	TxHash        string  `json:"tx_hash"`
	Time          string  `json:"time"`
	Venue         string  `json:"venue"`
	Market        string  `json:"market"`
	MarketName    string  `json:"market_name"`
	Pool          string  `json:"pool,omitempty"`
	Account       string  `json:"account"`
	IsLong        bool    `json:"is_long"`
	SizeUSD       float64 `json:"size_usd"`
	CollateralUSD float64 `json:"collateral_usd"`
}

var perpCache = newTTLCache(5 * time.Minute)

// perpWindow resolves the window ending at the last complete UTC day the perps
// sync has passed, clamped to the first day with a trade.
func (s *Server) perpWindow(ctx context.Context, days int) (PerpWindow, time.Time, error) {
	w := PerpWindow{Days: days}
	through, err := perps.Through(ctx, s.conn)
	if err != nil {
		return w, time.Time{}, err
	}
	var first time.Time
	if err := s.conn.QueryRow(ctx, `SELECT min(block_time) FROM perp_events`).Scan(&first); err != nil {
		return w, time.Time{}, err
	}
	asOf := through.Truncate(24*time.Hour).AddDate(0, 0, -1)
	first = first.UTC().Truncate(24 * time.Hour)
	if through.IsZero() || first.Year() < 2000 || asOf.Before(first) {
		return w, time.Time{}, nil
	}
	from := asOf.AddDate(0, 0, 1-days)
	if from.Before(first) {
		from = first
		w.Days = int(asOf.Sub(first).Hours()/24) + 1
	}
	a, f := asOf.Format("2006-01-02"), from.Format("2006-01-02")
	w.AsOf, w.From = &a, &f
	return w, asOf, nil
}

// perpMarketNames maps GMX V2 market tokens to their names and pools. GMX V1
// markets are index tokens and are named by MarketName directly.
func (s *Server) perpMarketNames(ctx context.Context) (map[string][2]string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(market)), lower(hex(index_token)), lower(hex(long_token)), lower(hex(short_token))
		FROM perp_markets FINAL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var m, idx, l, sh string
		if err := rows.Scan(&m, &idx, &l, &sh); err != nil {
			return nil, err
		}
		name, pool := perps.MarketName(idx, l, sh)
		out[m] = [2]string{name, pool}
	}
	return out, rows.Err()
}

func perpName(names map[string][2]string, venue, market string) (string, string) {
	if venue == "gmx-v2" {
		if n, ok := names[market]; ok {
			return n[0], n[1]
		}
		return "0x" + market, ""
	}
	return perps.MarketName(market, "", "")
}

// handlePerps returns C-Chain perpetuals volume per venue
// @Summary C-Chain perpetuals
// @Description Volume, long/short split, trades, traders, fees and liquidations per venue (GMX V2, GMX V1) over the last N complete UTC days ending as_of. Volume is the USD size change of every increase, decrease and liquidation, as GMX reports it. Fees are position, borrowing and liquidation fees (funding is not a fee). traders counts distinct accounts that changed a position size. Cached for 5 minutes.
// @Tags Metrics - Perps
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Success 200 {object} Response{data=PerpOverview}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/perps [get]
func (s *Server) handlePerps(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := perpCache.get(r.Context(), fmt.Sprintf("overview:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadPerps(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

// perpAggregates is the shared SELECT list over perp_events.
const perpAggregates = `
	sumIf(size_usd, kind != 'fee'),
	sumIf(size_usd, kind != 'fee' AND is_long),
	sumIf(size_usd, kind != 'fee' AND NOT is_long),
	countIf(kind != 'fee'),
	uniqExactIf(account, kind != 'fee'),
	sumIf(fee_usd, kind = 'fee'),
	countIf(kind = 'liquidation'),
	sumIf(size_usd, kind = 'liquidation')`

func perpScan(st *PerpStats) []interface{} {
	return []interface{}{&st.VolumeUSD, &st.LongVolumeUSD, &st.ShortVolumeUSD, &st.Trades, &st.Traders, &st.FeesUSD, &st.Liquidations, &st.LiquidatedUSD}
}

func (s *Server) loadPerps(ctx context.Context, days int) (PerpOverview, error) {
	out := PerpOverview{ByVenue: []PerpStats{}}
	win, asOf, err := s.perpWindow(ctx, days)
	out.PerpWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	// The () grouping set is the all-venue total, traders deduplicated across
	// venues; its venue column is empty.
	rows, err := s.conn.Query(ctx, `
		SELECT venue, `+perpAggregates+`
		FROM (SELECT * FROM perp_events FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY GROUPING SETS ((venue), ())
	`, asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byID := map[string]PerpStats{}
	for rows.Next() {
		var id string
		var st PerpStats
		if err := rows.Scan(append([]interface{}{&id}, perpScan(&st)...)...); err != nil {
			return out, err
		}
		if id == "" {
			out.Totals = st
			continue
		}
		byID[id] = st
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, v := range perps.Venues {
		st := byID[v.ID]
		st.ID, st.Name = v.ID, v.Name
		out.ByVenue = append(out.ByVenue, st)
	}
	return out, nil
}

// handlePerpsTimeseries returns daily C-Chain perpetuals volume
// @Summary C-Chain perpetuals history
// @Description Daily volume, trades, fees and liquidated size, total and per venue, for the last N complete UTC days ending as_of. Oldest first, days without trades left out. Cached for 5 minutes.
// @Tags Metrics - Perps
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Success 200 {object} Response{data=[]PerpDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/perps/timeseries [get]
func (s *Server) handlePerpsTimeseries(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := perpCache.get(r.Context(), fmt.Sprintf("timeseries:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadPerpsTimeseries(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadPerpsTimeseries(ctx context.Context, days int) ([]PerpDay, error) {
	out := []PerpDay{}
	win, asOf, err := s.perpWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(block_time)) AS day, venue,
			sumIf(size_usd, kind != 'fee'), countIf(kind != 'fee'), sumIf(fee_usd, kind = 'fee'), sumIf(size_usd, kind = 'liquidation')
		FROM (SELECT * FROM perp_events FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY day, venue
		ORDER BY day, venue
	`, asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date string
		var v PerpDayVenue
		var fees, liq float64
		if err := rows.Scan(&date, &v.ID, &v.VolumeUSD, &v.Trades, &fees, &liq); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Date != date {
			out = append(out, PerpDay{Date: date, Venues: []PerpDayVenue{}})
		}
		d := &out[len(out)-1]
		d.VolumeUSD += v.VolumeUSD
		d.Trades += v.Trades
		d.FeesUSD += fees
		d.LiquidatedUSD += liq
		if v.Trades > 0 {
			d.Venues = append(d.Venues, v)
		}
	}
	return out, rows.Err()
}

// handlePerpsOpenInterest returns daily C-Chain perpetuals open interest
// @Summary C-Chain perpetuals open interest
// @Description Long and short open interest in USD at the end of each complete UTC day, total and per market, for the last N days ending as_of. GMX V2 from its OpenInterestUpdated events, GMX V1 from the sum of its position size changes. Markets with no open interest are left out. Cached for 5 minutes.
// @Tags Metrics - Perps
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Success 200 {object} Response{data=[]PerpOIDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/perps/open-interest [get]
func (s *Server) handlePerpsOpenInterest(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := perpCache.get(r.Context(), fmt.Sprintf("oi:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadPerpsOpenInterest(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadPerpsOpenInterest(ctx context.Context, days int) ([]PerpOIDay, error) {
	out := []PerpOIDay{}
	win, asOf, err := s.perpWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	names, err := s.perpMarketNames(ctx)
	if err != nil {
		return nil, err
	}
	// Rounding in the carried-forward sums can leave a closed side a little
	// below zero, so each side is clamped at zero.
	rows, err := s.conn.Query(ctx, `
		SELECT toString(day), venue, lower(hex(market)),
			greatest(sumIf(oi_usd, is_long), 0), greatest(sumIf(oi_usd, NOT is_long), 0)
		FROM (SELECT * FROM perp_oi_daily FINAL) t
		WHERE day >= ? AND day <= ?
		GROUP BY day, venue, market
		HAVING greatest(sumIf(oi_usd, is_long), 0) + greatest(sumIf(oi_usd, NOT is_long), 0) >= 1
		ORDER BY day
	`, asOf.AddDate(0, 0, 1-win.Days), asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date string
		var m PerpMarketOI
		if err := rows.Scan(&date, &m.Venue, &m.Market, &m.LongUSD, &m.ShortUSD); err != nil {
			return nil, err
		}
		m.Name, m.Pool = perpName(names, m.Venue, m.Market)
		m.Market = "0x" + m.Market
		if len(out) == 0 || out[len(out)-1].Date != date {
			out = append(out, PerpOIDay{Date: date, ByMarket: []PerpMarketOI{}})
		}
		d := &out[len(out)-1]
		d.LongUSD += m.LongUSD
		d.ShortUSD += m.ShortUSD
		d.ByMarket = append(d.ByMarket, m)
	}
	for i := range out {
		bm := out[i].ByMarket
		sort.Slice(bm, func(a, b int) bool { return bm[a].LongUSD+bm[a].ShortUSD > bm[b].LongUSD+bm[b].ShortUSD })
	}
	return out, rows.Err()
}

// handlePerpsTopLiquidations returns the largest C-Chain perpetuals liquidations
// @Summary Largest C-Chain perpetuals liquidations
// @Description Liquidations with the largest position size over the last N complete UTC days ending as_of. collateral_usd is the collateral the position held. Cached for 5 minutes.
// @Tags Metrics - Perps
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Param limit query int false "Liquidations, 1-100" default(25)
// @Success 200 {object} Response{data=[]PerpLiquidation}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/perps/liquidations/top [get]
func (s *Server) handlePerpsTopLiquidations(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	limit, err := bridgeLimit(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := perpCache.get(r.Context(), fmt.Sprintf("liq:%d:%d", days, limit), func(ctx context.Context) (interface{}, error) {
		return s.loadPerpsTopLiquidations(ctx, days, limit)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadPerpsTopLiquidations(ctx context.Context, days, limit int) ([]PerpLiquidation, error) {
	out := []PerpLiquidation{}
	win, asOf, err := s.perpWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	names, err := s.perpMarketNames(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(transaction_hash)), block_time, venue, lower(hex(market)), lower(hex(account)), is_long, size_usd, collateral_usd
		FROM (SELECT * FROM perp_events FINAL) t
		WHERE block_time >= ? AND block_time < ? AND kind = 'liquidation'
		ORDER BY size_usd DESC
		LIMIT ?
	`, asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l PerpLiquidation
		var at time.Time
		if err := rows.Scan(&l.TxHash, &at, &l.Venue, &l.Market, &l.Account, &l.IsLong, &l.SizeUSD, &l.CollateralUSD); err != nil {
			return nil, err
		}
		l.MarketName, l.Pool = perpName(names, l.Venue, l.Market)
		l.TxHash, l.Market, l.Account = "0x"+l.TxHash, "0x"+l.Market, "0x"+l.Account
		l.Time = at.UTC().Format(time.RFC3339)
		out = append(out, l)
	}
	return out, rows.Err()
}
