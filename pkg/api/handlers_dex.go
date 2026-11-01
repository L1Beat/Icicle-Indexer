package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"icicle/pkg/dexvolume"
)

// DexWindow is the day range a DEX response covers: the last Days complete UTC
// days ending AsOf, both inclusive.
type DexWindow struct {
	AsOf *string `json:"as_of"` // last complete UTC day, null before the first rollup
	From *string `json:"from"`
	Days int     `json:"days"`
}

// DexStats is one DEX, or all of them, over the window.
type DexStats struct {
	ID              string  `json:"id,omitempty"`
	Name            string  `json:"name,omitempty"`
	Protocol        string  `json:"protocol,omitempty"`
	VolumeUSD       float64 `json:"volume_usd"`
	Swaps           uint64  `json:"swaps"`
	PricedShare     float64 `json:"priced_share"` // priced swaps / swaps, 0-1
	UniqueAddresses uint64  `json:"unique_addresses"`
	FlaggedSwaps    uint64  `json:"flagged_swaps"`
}

// DexOverview is the /metrics/dex payload.
type DexOverview struct {
	DexWindow
	FlagThresholdUSD float64    `json:"flag_threshold_usd"`
	Dexes            []DexStats `json:"dexes"`
	Totals           DexStats   `json:"totals"`
}

// DexDayDex is one DEX on one day.
type DexDayDex struct {
	ID        string  `json:"id"`
	VolumeUSD float64 `json:"volume_usd"`
	Swaps     uint64  `json:"swaps"`
}

// DexDay is one day across DEXes.
type DexDay struct {
	Date      string      `json:"date"`
	VolumeUSD float64     `json:"volume_usd"`
	Swaps     uint64      `json:"swaps"`
	Dexes     []DexDayDex `json:"dexes"`
}

// DexToken is one side of a pool.
type DexToken struct {
	Address string `json:"address"`
	Symbol  string `json:"symbol"`
}

// DexPool is one pool over the window.
type DexPool struct {
	Pool      string   `json:"pool"`
	Dex       string   `json:"dex"`
	Protocol  string   `json:"protocol"`
	Token0    DexToken `json:"token0"`
	Token1    DexToken `json:"token1"`
	VolumeUSD float64  `json:"volume_usd"`
	Swaps     uint64   `json:"swaps"`
}

var dexCache = newTTLCache(10 * time.Minute)

func dexByID() map[string]dexvolume.Dex {
	m := map[string]dexvolume.Dex{}
	for _, d := range dexvolume.Dexes {
		m[d.ID] = d
	}
	return m
}

// dexDays parses ?days within [1, max], defaulting to def.
func dexDays(r *http.Request, def, max int) (int, error) {
	q := r.URL.Query().Get("days")
	if q == "" {
		return def, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > max {
		return 0, fmt.Errorf("days must be 1-%d", max)
	}
	return n, nil
}

// dexWindow resolves the window ending at the newest rolled-up day.
func (s *Server) dexWindow(ctx context.Context, days int) (DexWindow, time.Time, error) {
	w := DexWindow{Days: days}
	var asOf time.Time
	if err := s.conn.QueryRow(ctx, `SELECT max(day) FROM dex_daily`).Scan(&asOf); err != nil {
		return w, asOf, err
	}
	if asOf.Year() < 2000 {
		return w, asOf, nil
	}
	a, f := asOf.Format("2006-01-02"), asOf.AddDate(0, 0, 1-days).Format("2006-01-02")
	w.AsOf, w.From = &a, &f
	return w, asOf, nil
}

// handleDexOverview returns C-Chain DEX volume per DEX
// @Summary C-Chain DEX volume
// @Description Swap volume, swaps and unique addresses per DEX (Pharaoh V3, Pharaoh DLMM, Uniswap V3, Blackhole CLMM, LFJ V2.1+V2.2, Pangolin V3) over the last N complete UTC days ending as_of. days=1 is the last complete UTC day (matches DefiLlama's daily numbers), not a rolling 24h. Only pools created by the known factories count. Swaps are priced at a USD stablecoin side ($1) or a WAVAX side (hourly AVAX price from WAVAX/stablecoin swaps), one side each; others are counted but unpriced (see priced_share). unique_addresses counts tx.from, bots included; totals deduplicate across DEXes. Cached for 10 minutes.
// @Tags Metrics - DEX
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(1)
// @Success 200 {object} Response{data=DexOverview}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/dex [get]
func (s *Server) handleDexOverview(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 1, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := dexCache.get(r.Context(), fmt.Sprintf("overview:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadDexOverview(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadDexOverview(ctx context.Context, days int) (DexOverview, error) {
	out := DexOverview{FlagThresholdUSD: dexvolume.FlagUSD, Dexes: []DexStats{}}
	win, asOf, err := s.dexWindow(ctx, days)
	out.DexWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	// The () grouping set is the all-DEX total, with addresses deduplicated
	// across DEXes; its dex column is empty.
	rows, err := s.conn.Query(ctx, `
		SELECT dex, sum(swaps), sum(priced_swaps), sum(volume_usd), sum(flagged_swaps),
			toUInt64(uniqExactMerge(addresses))
		FROM dex_daily FINAL
		WHERE day >= ? AND day <= ?
		GROUP BY GROUPING SETS ((dex), ())
	`, asOf.AddDate(0, 0, 1-days), asOf)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byID := map[string]DexStats{}
	for rows.Next() {
		var id string
		var st DexStats
		var priced uint64
		if err := rows.Scan(&id, &st.Swaps, &priced, &st.VolumeUSD, &st.FlaggedSwaps, &st.UniqueAddresses); err != nil {
			return out, err
		}
		if st.Swaps > 0 {
			st.PricedShare = float64(priced) / float64(st.Swaps)
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
	for _, d := range dexvolume.Dexes {
		st := byID[d.ID]
		st.ID, st.Name, st.Protocol = d.ID, d.Name, d.Protocol
		out.Dexes = append(out.Dexes, st)
	}
	return out, nil
}

// handleDexTimeseries returns daily C-Chain DEX volume
// @Summary C-Chain DEX volume history
// @Description Daily volume and swaps, total and per DEX, for the last N complete UTC days ending at the newest rolled-up day. Oldest first. Cached for 10 minutes.
// @Tags Metrics - DEX
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Success 200 {object} Response{data=[]DexDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/dex/timeseries [get]
func (s *Server) handleDexTimeseries(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := dexCache.get(r.Context(), fmt.Sprintf("timeseries:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadDexTimeseries(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadDexTimeseries(ctx context.Context, days int) ([]DexDay, error) {
	out := []DexDay{}
	win, asOf, err := s.dexWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT toString(day), dex, sum(volume_usd), sum(swaps)
		FROM dex_daily FINAL
		WHERE day >= ? AND day <= ?
		GROUP BY day, dex
		ORDER BY day, dex
	`, asOf.AddDate(0, 0, 1-days), asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date string
		var d DexDayDex
		if err := rows.Scan(&date, &d.ID, &d.VolumeUSD, &d.Swaps); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Date != date {
			out = append(out, DexDay{Date: date, Dexes: []DexDayDex{}})
		}
		day := &out[len(out)-1]
		day.VolumeUSD += d.VolumeUSD
		day.Swaps += d.Swaps
		day.Dexes = append(day.Dexes, d)
	}
	return out, rows.Err()
}

// handleDexPools returns the top C-Chain DEX pools by volume
// @Summary Top C-Chain DEX pools
// @Description Pools with the most priced volume over the last N complete UTC days ending at the newest rolled-up day, with their tokens. Token symbols come from token_metadata and are empty when unknown. Cached for 10 minutes.
// @Tags Metrics - DEX
// @Produce json
// @Param days query int false "Complete UTC days, 1-30" default(1)
// @Param limit query int false "Pools, 1-100" default(20)
// @Success 200 {object} Response{data=[]DexPool}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/dex/pools [get]
func (s *Server) handleDexPools(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 1, 30)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 100 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "limit must be 1-100")
			return
		}
		limit = n
	}
	v, err := dexCache.get(r.Context(), fmt.Sprintf("pools:%d:%d", days, limit), func(ctx context.Context) (interface{}, error) {
		return s.loadDexPools(ctx, days, limit)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadDexPools(ctx context.Context, days, limit int) ([]DexPool, error) {
	out := []DexPool{}
	win, asOf, err := s.dexWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(x.pool)), x.dex, x.v, x.n,
			lower(hex(p.token0)), lower(hex(p.token1)), m0.symbol, m1.symbol
		FROM (
			SELECT pool, dex, sum(volume_usd) AS v, sum(swaps) AS n
			FROM dex_daily FINAL
			WHERE day >= ? AND day <= ?
			GROUP BY pool, dex
			ORDER BY v DESC
			LIMIT ?
		) x
		LEFT JOIN (SELECT pool, token0, token1 FROM dex_pools FINAL) p ON x.pool = p.pool
		LEFT JOIN (SELECT token, symbol FROM token_metadata FINAL WHERE chain_id = ?) m0 ON p.token0 = m0.token
		LEFT JOIN (SELECT token, symbol FROM token_metadata FINAL WHERE chain_id = ?) m1 ON p.token1 = m1.token
		ORDER BY x.v DESC
	`, asOf.AddDate(0, 0, 1-days), asOf, limit, uint32(dexvolume.ChainID), uint32(dexvolume.ChainID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dexes := dexByID()
	for rows.Next() {
		var p DexPool
		var t0, t1 string
		if err := rows.Scan(&p.Pool, &p.Dex, &p.VolumeUSD, &p.Swaps, &t0, &t1, &p.Token0.Symbol, &p.Token1.Symbol); err != nil {
			return nil, err
		}
		p.Pool = "0x" + p.Pool
		p.Token0.Address, p.Token1.Address = "0x"+t0, "0x"+t1
		p.Protocol = dexes[p.Dex].Protocol
		out = append(out, p)
	}
	return out, rows.Err()
}
