package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"icicle/pkg/bridges"
)

// BridgeWindow is the day range a bridge response covers: the last Days
// complete UTC days ending AsOf, both inclusive.
type BridgeWindow struct {
	AsOf *string `json:"as_of"` // last complete UTC day, null before the first one
	From *string `json:"from"`
	Days int     `json:"days"`
}

// BridgeFlow is USD in and out of the C-Chain. Unpriced transfers are counted
// in Transfers but add no USD (see PricedShare).
type BridgeFlow struct {
	InflowUSD        float64 `json:"inflow_usd"`
	OutflowUSD       float64 `json:"outflow_usd"`
	NetUSD           float64 `json:"net_usd"` // inflow - outflow
	Transfers        uint64  `json:"transfers"`
	PricedShare      float64 `json:"priced_share"` // priced transfers / transfers, 0-1
	FlaggedTransfers uint64  `json:"flagged_transfers"`
}

// BridgeTotals is all bridges together.
type BridgeTotals struct {
	BridgeFlow
	// PassthroughCount is the transactions that bridged in and out again in
	// one go (e.g. a CCTP mint forwarded straight to another chain). Their legs
	// are left out of every figure.
	PassthroughCount uint64 `json:"passthrough_count"`
}

// BridgeStats is one bridge.
type BridgeStats struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	BridgeFlow
}

// BridgeChain is one counterparty chain, across bridges.
type BridgeChain struct {
	Chain       string `json:"chain"` // key, e.g. ethereum, base, bitcoin
	Name        string `json:"name"`
	AvalancheL1 bool   `json:"avalanche_l1"`
	BridgeFlow
}

// BridgeToken is one token, across bridges. Amounts are token base units.
type BridgeToken struct {
	Token         string  `json:"token"`
	Symbol        string  `json:"symbol"`
	Decimals      *uint8  `json:"decimals"` // null when unknown
	Priced        bool    `json:"priced"`
	InflowAmount  string  `json:"inflow_amount"`
	OutflowAmount string  `json:"outflow_amount"`
	InflowUSD     float64 `json:"inflow_usd"`
	OutflowUSD    float64 `json:"outflow_usd"`
	NetUSD        float64 `json:"net_usd"`
	Transfers     uint64  `json:"transfers"`
}

// BridgeOverview is the /metrics/bridges payload.
type BridgeOverview struct {
	BridgeWindow
	FlagThresholdUSD float64       `json:"flag_threshold_usd"`
	Totals           BridgeTotals  `json:"totals"`
	ByBridge         []BridgeStats `json:"by_bridge"`
	ByChain          []BridgeChain `json:"by_chain"`
	ByToken          []BridgeToken `json:"by_token"`
}

// BridgeDayBridge is one bridge on one day.
type BridgeDayBridge struct {
	ID         string  `json:"id"`
	InflowUSD  float64 `json:"inflow_usd"`
	OutflowUSD float64 `json:"outflow_usd"`
	Transfers  uint64  `json:"transfers"`
}

// BridgeDay is one day across bridges.
type BridgeDay struct {
	Date       string            `json:"date"`
	InflowUSD  float64           `json:"inflow_usd"`
	OutflowUSD float64           `json:"outflow_usd"`
	NetUSD     float64           `json:"net_usd"`
	Transfers  uint64            `json:"transfers"`
	Bridges    []BridgeDayBridge `json:"bridges"`
}

// BridgeTransfer is one leg.
type BridgeTransfer struct {
	TxHash      string   `json:"tx_hash"`
	Time        string   `json:"time"`
	Bridge      string   `json:"bridge"`
	Direction   string   `json:"direction"` // in, out
	Chain       string   `json:"chain"`
	ChainName   string   `json:"chain_name"`
	Token       string   `json:"token"`
	Symbol      string   `json:"symbol"`
	Decimals    *uint8   `json:"decimals"`
	Amount      string   `json:"amount"`
	USD         *float64 `json:"usd"`
	From        *string  `json:"from"` // C-Chain sender (out), null when unknown
	To          *string  `json:"to"`   // C-Chain recipient (in), null when unknown
	Via         string   `json:"via,omitempty"`
	Passthrough bool     `json:"passthrough"`
}

var bridgeCache = newTTLCache(5 * time.Minute)

// bridgeWindow resolves the window ending at the last complete UTC day the
// bridge sync has passed. A window reaching past the first day with a tracked
// transfer is clamped to it, and Days and From report the clamped range.
func (s *Server) bridgeWindow(ctx context.Context, days int) (BridgeWindow, time.Time, error) {
	w := BridgeWindow{Days: days}
	through, err := bridges.Through(ctx, s.conn)
	if err != nil {
		return w, time.Time{}, err
	}
	var first time.Time
	if err := s.conn.QueryRow(ctx, `SELECT min(block_time) FROM bridge_transfers`).Scan(&first); err != nil {
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

// bridgeAggregates is the shared SELECT list: USD in, USD out, transfers,
// priced transfers, flagged transfers. Passthrough legs are left out.
const bridgeAggregates = `
	sumIf(ifNull(usd, 0), direction = 'in' AND NOT passthrough),
	sumIf(ifNull(usd, 0), direction = 'out' AND NOT passthrough),
	countIf(NOT passthrough),
	countIf(usd IS NOT NULL AND NOT passthrough),
	countIf(usd > %d AND NOT passthrough)`

func scanFlow(f *BridgeFlow, priced *uint64) []interface{} {
	return []interface{}{&f.InflowUSD, &f.OutflowUSD, &f.Transfers, priced, &f.FlaggedTransfers}
}

func (f *BridgeFlow) finish(priced uint64) {
	f.NetUSD = f.InflowUSD - f.OutflowUSD
	if f.Transfers > 0 {
		f.PricedShare = float64(priced) / float64(f.Transfers)
	}
}

// handleBridges returns C-Chain bridge flows
// @Summary C-Chain bridge inflows and outflows
// @Description USD bridged into and out of the C-Chain over the last N complete UTC days ending as_of, in total and by bridge, counterparty chain and token. Tracked: Circle CCTP V1+V2, LayerZero OFTs V1+V2, Stargate V2 pools, the Avalanche Bridge (.e tokens to Ethereum, BTC.b to Bitcoin) and Chainlink CCIP token pools. Each transfer counts once: CCIP or other legs moving native USDC through CCTP count as CCTP. Transactions that bridge in and out again are left out (passthrough_count). Stablecoins are priced at $1, wrapped ETH, BTC, AVAX and LINK at the hourly Chainlink price, other tokens are counted but unpriced (priced_share). Cached for 5 minutes.
// @Tags Metrics - Bridges
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Success 200 {object} Response{data=BridgeOverview}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/bridges [get]
func (s *Server) handleBridges(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := bridgeCache.get(r.Context(), fmt.Sprintf("overview:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadBridges(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadBridges(ctx context.Context, days int) (BridgeOverview, error) {
	out := BridgeOverview{FlagThresholdUSD: bridges.FlagUSD, ByBridge: []BridgeStats{}, ByChain: []BridgeChain{}, ByToken: []BridgeToken{}}
	win, asOf, err := s.bridgeWindow(ctx, days)
	out.BridgeWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	from, to := asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1)

	// By bridge, and the () grouping set for the total, whose bridge is empty.
	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT bridge, `+bridgeAggregates+`, uniqExactIf(transaction_hash, passthrough)
		FROM (SELECT * FROM bridge_transfers FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY GROUPING SETS ((bridge), ())
	`, bridges.FlagUSD), from, to)
	if err != nil {
		return out, err
	}
	byID := map[string]BridgeFlow{}
	for rows.Next() {
		var id string
		var f BridgeFlow
		var priced, pass uint64
		if err := rows.Scan(append(append([]interface{}{&id}, scanFlow(&f, &priced)...), &pass)...); err != nil {
			rows.Close()
			return out, err
		}
		f.finish(priced)
		if id == "" {
			out.Totals = BridgeTotals{BridgeFlow: f, PassthroughCount: pass}
			continue
		}
		byID[id] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, b := range bridges.Bridges {
		out.ByBridge = append(out.ByBridge, BridgeStats{ID: b.ID, Name: b.Name, BridgeFlow: byID[b.ID]})
	}

	rows, err = s.conn.Query(ctx, fmt.Sprintf(`
		SELECT chain, `+bridgeAggregates+`
		FROM (SELECT * FROM bridge_transfers FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY chain
		HAVING countIf(NOT passthrough) > 0
		ORDER BY sumIf(ifNull(usd, 0), NOT passthrough) DESC, chain
	`, bridges.FlagUSD), from, to)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c BridgeChain
		var priced uint64
		if err := rows.Scan(append([]interface{}{&c.Chain}, scanFlow(&c.BridgeFlow, &priced)...)...); err != nil {
			rows.Close()
			return out, err
		}
		c.finish(priced)
		c.Name, c.AvalancheL1 = bridges.ChainName(c.Chain), bridges.IsAvalancheL1(c.Chain)
		out.ByChain = append(out.ByChain, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = s.conn.Query(ctx, `
		SELECT lower(hex(x.token)), m.symbol, m.decimals, m.known = 1, x.priced,
			toString(x.amt_in), toString(x.amt_out), x.usd_in, x.usd_out, x.n
		FROM (
			SELECT token,
				countIf(usd IS NOT NULL AND NOT passthrough) > 0 AS priced,
				sumIf(amount, direction = 'in' AND NOT passthrough) AS amt_in,
				sumIf(amount, direction = 'out' AND NOT passthrough) AS amt_out,
				sumIf(ifNull(usd, 0), direction = 'in' AND NOT passthrough) AS usd_in,
				sumIf(ifNull(usd, 0), direction = 'out' AND NOT passthrough) AS usd_out,
				countIf(NOT passthrough) AS n
			FROM (SELECT * FROM bridge_transfers FINAL) t
			WHERE block_time >= ? AND block_time < ?
			GROUP BY token
			HAVING n > 0
		) x
		LEFT JOIN (SELECT token, symbol, decimals, toUInt8(1) AS known FROM token_metadata FINAL WHERE chain_id = ?) m ON x.token = m.token
		ORDER BY x.usd_in + x.usd_out DESC, x.n DESC
	`, from, to, uint32(bridges.ChainID))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t BridgeToken
		var dec uint8
		var known bool
		if err := rows.Scan(&t.Token, &t.Symbol, &dec, &known, &t.Priced, &t.InflowAmount, &t.OutflowAmount,
			&t.InflowUSD, &t.OutflowUSD, &t.Transfers); err != nil {
			return out, err
		}
		t.Token = "0x" + t.Token
		if known {
			t.Decimals = &dec
		}
		t.NetUSD = t.InflowUSD - t.OutflowUSD
		out.ByToken = append(out.ByToken, t)
	}
	return out, rows.Err()
}

// handleBridgesTimeseries returns daily C-Chain bridge flows
// @Summary C-Chain bridge flows history
// @Description Daily USD in and out of the C-Chain, total and per bridge, for the last N complete UTC days ending as_of. Oldest first, days without transfers left out. Same counting as /metrics/bridges. Cached for 5 minutes.
// @Tags Metrics - Bridges
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Success 200 {object} Response{data=[]BridgeDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/bridges/timeseries [get]
func (s *Server) handleBridgesTimeseries(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := bridgeCache.get(r.Context(), fmt.Sprintf("timeseries:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadBridgesTimeseries(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadBridgesTimeseries(ctx context.Context, days int) ([]BridgeDay, error) {
	out := []BridgeDay{}
	win, asOf, err := s.bridgeWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(block_time)) AS day, bridge,
			sumIf(ifNull(usd, 0), direction = 'in'), sumIf(ifNull(usd, 0), direction = 'out'), count()
		FROM (SELECT * FROM bridge_transfers FINAL) t
		WHERE block_time >= ? AND block_time < ? AND NOT passthrough
		GROUP BY day, bridge
		ORDER BY day, bridge
	`, asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date string
		var b BridgeDayBridge
		if err := rows.Scan(&date, &b.ID, &b.InflowUSD, &b.OutflowUSD, &b.Transfers); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Date != date {
			out = append(out, BridgeDay{Date: date, Bridges: []BridgeDayBridge{}})
		}
		day := &out[len(out)-1]
		day.InflowUSD += b.InflowUSD
		day.OutflowUSD += b.OutflowUSD
		day.NetUSD = day.InflowUSD - day.OutflowUSD
		day.Transfers += b.Transfers
		day.Bridges = append(day.Bridges, b)
	}
	return out, rows.Err()
}

// handleBridgeTransfersTop returns the largest C-Chain bridge transfers
// @Summary Largest C-Chain bridge transfers
// @Description Priced bridge transfers with the highest USD value over the last N complete UTC days ending as_of. Passthrough legs are left out. Cached for 5 minutes.
// @Tags Metrics - Bridges
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Param limit query int false "Transfers, 1-100" default(25)
// @Success 200 {object} Response{data=[]BridgeTransfer}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/bridges/transfers/top [get]
func (s *Server) handleBridgeTransfersTop(w http.ResponseWriter, r *http.Request) {
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
	v, err := bridgeCache.get(r.Context(), fmt.Sprintf("top:%d:%d", days, limit), func(ctx context.Context) (interface{}, error) {
		win, asOf, err := s.bridgeWindow(ctx, days)
		if err != nil || win.AsOf == nil {
			return []BridgeTransfer{}, err
		}
		return s.loadBridgeTransfers(ctx, `block_time >= ? AND block_time < ? AND usd IS NOT NULL AND NOT passthrough`,
			`usd DESC`, limit, asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1))
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

var bridgeRecentCache = newTTLCache(time.Minute)

// handleBridgeTransfersRecent returns the newest C-Chain bridge transfers
// @Summary Recent C-Chain bridge transfers
// @Description The newest bridge transfers, priced or not, passthrough legs included and marked. Follows the chain within the bridge sync interval (5 minutes). Cached for 1 minute.
// @Tags Metrics - Bridges
// @Produce json
// @Param limit query int false "Transfers, 1-100" default(25)
// @Success 200 {object} Response{data=[]BridgeTransfer}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/bridges/transfers/recent [get]
func (s *Server) handleBridgeTransfersRecent(w http.ResponseWriter, r *http.Request) {
	limit, err := bridgeLimit(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := bridgeRecentCache.get(r.Context(), fmt.Sprintf("recent:%d", limit), func(ctx context.Context) (interface{}, error) {
		// The newest day bounds the read, so it stays a short scan.
		return s.loadBridgeTransfers(ctx,
			`block_time >= (SELECT max(block_time) FROM bridge_transfers) - INTERVAL 7 DAY`, `block_time DESC, log_index DESC`, limit)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func bridgeLimit(r *http.Request) (int, error) {
	q := r.URL.Query().Get("limit")
	if q == "" {
		return 25, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > 100 {
		return 0, fmt.Errorf("limit must be 1-100")
	}
	return n, nil
}

func (s *Server) loadBridgeTransfers(ctx context.Context, where, order string, limit int, args ...interface{}) ([]BridgeTransfer, error) {
	out := []BridgeTransfer{}
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(t.transaction_hash)), t.block_time, t.bridge, toString(t.direction), t.chain,
			lower(hex(t.token)), m.symbol, m.decimals, m.known = 1, toString(t.amount), t.usd,
			lower(hex(t.from_address)), lower(hex(t.to_address)), t.via, t.passthrough
		FROM (
			SELECT * FROM (SELECT * FROM bridge_transfers FINAL)
			WHERE `+where+`
			ORDER BY `+order+`
			LIMIT ?
		) t
		LEFT JOIN (SELECT token, symbol, decimals, toUInt8(1) AS known FROM token_metadata FINAL WHERE chain_id = ?) m ON t.token = m.token
		ORDER BY `+order, append(args, limit, uint32(bridges.ChainID))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	zero := hex.EncodeToString(make([]byte, 20))
	for rows.Next() {
		var t BridgeTransfer
		var at time.Time
		var dec uint8
		var known bool
		var from, to string
		if err := rows.Scan(&t.TxHash, &at, &t.Bridge, &t.Direction, &t.Chain, &t.Token, &t.Symbol, &dec, &known,
			&t.Amount, &t.USD, &from, &to, &t.Via, &t.Passthrough); err != nil {
			return nil, err
		}
		t.TxHash, t.Token = "0x"+t.TxHash, "0x"+t.Token
		t.Time = at.UTC().Format(time.RFC3339)
		t.ChainName = bridges.ChainName(t.Chain)
		if known {
			t.Decimals = &dec
		}
		if from != zero {
			f := "0x" + from
			t.From = &f
		}
		if to != zero {
			v := "0x" + to
			t.To = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
