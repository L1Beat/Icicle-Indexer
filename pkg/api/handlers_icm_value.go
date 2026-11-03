package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ava-labs/avalanchego/ids"

	"icicle/pkg/bridges"
	"icicle/pkg/icm"
)

// ICMValueStats is ICTT value over a window, seen from the C-Chain: sent left
// the C-Chain, received arrived on it.
type ICMValueStats struct {
	Transfers       uint64  `json:"transfers"`
	PricedTransfers uint64  `json:"priced_transfers"`
	SentUSD         float64 `json:"sent_usd"`
	ReceivedUSD     float64 `json:"received_usd"`
	TotalUSD        float64 `json:"total_usd"`
}

// ICMTokenValue is one token's ICTT transfers. Amounts are C-Chain token base
// units as strings. USD is null when the token has no price.
type ICMTokenValue struct {
	Token          ICMToken `json:"token"`
	Priced         bool     `json:"priced"` // a stablecoin or a price-feed token, matched by address
	Transfers      uint64   `json:"transfers"`
	SentAmount     string   `json:"sent_amount"`
	ReceivedAmount string   `json:"received_amount"`
	SentUSD        *float64 `json:"sent_usd"`
	ReceivedUSD    *float64 `json:"received_usd"`
}

// ICMChainValue is ICTT value with one other chain.
type ICMChainValue struct {
	BlockchainID string  `json:"blockchain_id"`
	Name         string  `json:"name"`
	Transfers    uint64  `json:"transfers"`
	SentUSD      float64 `json:"sent_usd"`
	ReceivedUSD  float64 `json:"received_usd"`
	TotalUSD     float64 `json:"total_usd"`
}

// ICMValue is the /metrics/icm/value payload.
type ICMValue struct {
	PerpWindow
	Totals  ICMValueStats   `json:"totals"`
	ByToken []ICMTokenValue `json:"by_token"`
	ByChain []ICMChainValue `json:"by_chain"`
}

// ICMValueDayToken is one token on one day.
type ICMValueDayToken struct {
	Address     string   `json:"address"`
	Symbol      string   `json:"symbol"`
	Transfers   uint64   `json:"transfers"`
	SentUSD     *float64 `json:"sent_usd"`
	ReceivedUSD *float64 `json:"received_usd"`
}

// ICMValueDay is one UTC day.
type ICMValueDay struct {
	Date        string             `json:"date"`
	Transfers   uint64             `json:"transfers"`
	SentUSD     float64            `json:"sent_usd"`
	ReceivedUSD float64            `json:"received_usd"`
	TotalUSD    float64            `json:"total_usd"`
	ByToken     []ICMValueDayToken `json:"by_token"`
}

// ICMTransfer is one ICTT transfer.
type ICMTransfer struct {
	MessageID    string      `json:"message_id"`
	Direction    string      `json:"direction"`
	Type         string      `json:"type"`
	Counterparty ICMChainRef `json:"counterparty"`
	BlockTime    string      `json:"block_time"`
	TxHash       string      `json:"tx_hash"`
	App          string      `json:"app"`
	AppName      string      `json:"app_name"`
	Token        ICMToken    `json:"token"`
	Recipient    string      `json:"recipient"`
	Amount       *string     `json:"amount"` // C-Chain base units, null when the home's scaling is unknown
	USD          *float64    `json:"usd"`
}

// icmTransfersSQL is every ICTT transfer at a checked C-Chain app between two
// bound times: one row per message with its amount in C-Chain token units and
// its USD value. A TokenRemote's messages are already in its units. A
// TokenHome's are in the remote's, scaled back with the remote's registered
// multiplier (null when the remote is not registered). A multi-hop transfer
// routed through a C-Chain home counts once, as received; the send that
// forwards it is left out. A receive counts only once its message executed
// (MessageExecuted, maybe on a later retry): a failed one moved no tokens.
// Tokens are priced by address only, as on /metrics/bridges: USD stablecoins
// at $1, price-feed tokens at the hour's price, others not at all. Binds the
// window five times: from, to, from, from, to.
func icmTransfersSQL() string {
	feeds := bridges.TokenFeeds()
	tokens := make([]string, 0, len(feeds))
	for t := range feeds {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)
	pt, pf := make([]string, len(tokens)), make([]string, len(tokens))
	for i, t := range tokens {
		pt[i], pf[i] = "'"+t+"'", "'"+feeds[t]+"'"
	}
	return fmt.Sprintf(`
		WITH
			(SELECT groupArray(lower(hex(token))) FROM stablecoins FINAL WHERE chain_id = %[1]d AND peg = 'USD') AS st,
			arrayConcat(st, [%[2]s]) AS pt,
			arrayConcat(arrayMap(x -> 'USD', st), [%[3]s]) AS pf
		SELECT block_time, direction, message_id, transaction_hash, ictt_type, app, token, symbol, decimals,
			counterparty, recipient, amount, toBool(feed != '') AS priced,
			multiIf(isNull(amount) OR feed = '', NULL,
				feed = 'USD', toFloat64(amount) / pow(10, decimals),
				h.px > 0 AND h.px_hour >= bt - INTERVAL %[4]d DAY, toFloat64(amount) / pow(10, decimals) * h.px,
				NULL) AS usd
		FROM (
			SELECT m.block_time AS block_time, toDateTime(m.block_time) AS bt, toString(m.direction) AS direction,
				m.message_id AS message_id, m.transaction_hash AS transaction_hash, m.ictt_type AS ictt_type,
				m.local_address AS app, a.token AS token, a.symbol AS symbol, a.decimals AS decimals,
				m.counterparty AS counterparty, m.ictt_recipient AS recipient,
				multiIf(a.kind = '%[5]s', toNullable(m.ictt_amount),
					NOT r.registered OR r.multiplier = 0, NULL,
					r.multiply_on_remote, toNullable(intDiv(m.ictt_amount, r.multiplier)),
					toNullable(m.ictt_amount * r.multiplier)) AS amount,
				transform(lower(hex(a.token)), pt, pf, '') AS feed
			FROM (SELECT * FROM icm_messages FINAL WHERE block_time >= ? AND block_time < ? AND ictt_type BETWEEN %[6]d AND %[7]d
				AND (direction = 'sent' OR message_id IN (SELECT message_id FROM icm_executions WHERE block_time >= ?))) m
			INNER JOIN (SELECT address, kind, token, symbol, decimals FROM icm_apps FINAL WHERE kind != '') a
				ON m.local_address = a.address
			LEFT JOIN (SELECT home, remote_chain, remote_address, registered, multiplier, multiply_on_remote FROM icm_remotes FINAL) r
				ON m.local_address = r.home AND m.counterparty = r.remote_chain AND m.remote_address = r.remote_address
			WHERE NOT (m.direction = 'sent' AND (m.transaction_hash, m.local_address) IN (
				SELECT transaction_hash, local_address FROM icm_messages
				WHERE block_time >= ? AND block_time < ? AND direction = 'received' AND ictt_type IN (%[8]d, %[9]d)))
		) x
		ASOF LEFT JOIN (SELECT feed AS pfeed, hour AS px_hour, price AS px FROM price_hourly FINAL) h
			ON x.feed = h.pfeed AND x.bt >= h.px_hour`,
		icm.ChainID, strings.Join(pt, ", "), strings.Join(pf, ", "), bridges.StaleDays, icm.KindICTTRemote,
		icm.ICTTSingleHopSend, icm.ICTTMultiHopCall, icm.ICTTMultiHopSend, icm.ICTTMultiHopCall)
}

func icmBounds(asOf time.Time, days int) []interface{} {
	from, to := asOf.AddDate(0, 0, 1-days), asOf.AddDate(0, 0, 1)
	return []interface{}{from, to, from, from, to}
}

func usdPtr(v float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &v
}

// handleICMValue returns value moved through ICTT on the C-Chain
// @Summary C-Chain ICTT value
// @Description Token value moved by ICTT token bridges (TokenHome / TokenRemote) on the C-Chain over the last N complete UTC days ending as_of: sent (left the C-Chain) and received (arrived on it, once executed), total, per token and per other chain. Amounts are C-Chain token base units: a TokenHome's messages are scaled back from the remote's denomination with the remote's registered multiplier. Priced by token address like /metrics/bridges (USD stablecoins $1, WAVAX/BTC.b/WETH.e/LINK.e at their Chainlink feed); other tokens have no USD. A multi-hop transfer through a C-Chain home counts once. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Success 200 {object} Response{data=ICMValue}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/value [get]
func (s *Server) handleICMValue(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := icmCache.get(r.Context(), fmt.Sprintf("value:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadICMValue(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICMValue(ctx context.Context, days int) (ICMValue, error) {
	out := ICMValue{ByToken: []ICMTokenValue{}, ByChain: []ICMChainValue{}}
	win, asOf, err := s.icmWindow(ctx, days)
	out.PerpWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	// The (token) and (counterparty) grouping sets give the per-token and
	// per-chain splits from one pass; the other key is empty in each.
	rows, err := s.conn.Query(ctx, icmValueSQL(), icmBounds(asOf, win.Days)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var tok, symbol, cp, sentAmt, recvAmt string
		var decimals uint8
		var priced, byToken, byChain bool
		var n, pricedN uint64
		var sentUSD, recvUSD float64
		if err := rows.Scan(&tok, &symbol, &decimals, &priced, &cp, &n, &pricedN, &sentAmt, &recvAmt,
			&sentUSD, &recvUSD, &byToken, &byChain); err != nil {
			return out, err
		}
		switch {
		case byToken:
			out.ByToken = append(out.ByToken, ICMTokenValue{
				Token:  ICMToken{Address: "0x" + tok, Symbol: symbol, Decimals: decimals},
				Priced: priced, Transfers: n, SentAmount: sentAmt, ReceivedAmount: recvAmt,
				SentUSD: usdPtr(sentUSD, priced), ReceivedUSD: usdPtr(recvUSD, priced),
			})
		case byChain:
			var id ids.ID
			copy(id[:], cp)
			out.ByChain = append(out.ByChain, ICMChainValue{BlockchainID: id.String(), Name: names[id.String()],
				Transfers: n, SentUSD: sentUSD, ReceivedUSD: recvUSD, TotalUSD: sentUSD + recvUSD})
		default:
			out.Totals = ICMValueStats{Transfers: n, PricedTransfers: pricedN, SentUSD: sentUSD, ReceivedUSD: recvUSD, TotalUSD: sentUSD + recvUSD}
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	usd := func(t ICMTokenValue) float64 {
		var v float64
		if t.SentUSD != nil {
			v += *t.SentUSD + *t.ReceivedUSD
		}
		return v
	}
	sort.Slice(out.ByToken, func(i, j int) bool {
		a, b := out.ByToken[i], out.ByToken[j]
		if usd(a) != usd(b) {
			return usd(a) > usd(b)
		}
		if a.Transfers != b.Transfers {
			return a.Transfers > b.Transfers
		}
		return a.Token.Address < b.Token.Address
	})
	sort.Slice(out.ByChain, func(i, j int) bool {
		a, b := out.ByChain[i], out.ByChain[j]
		if a.TotalUSD != b.TotalUSD {
			return a.TotalUSD > b.TotalUSD
		}
		if a.Transfers != b.Transfers {
			return a.Transfers > b.Transfers
		}
		return a.BlockchainID < b.BlockchainID
	})
	return out, nil
}

// handleICMValueTimeseries returns daily ICTT value on the C-Chain
// @Summary C-Chain ICTT value per day
// @Description Daily USD sent and received by ICTT token bridges on the C-Chain, with transfers and a per-token split, for the last N complete UTC days ending as_of. Oldest first, every day included. Same scaling and pricing as /metrics/icm/value. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Success 200 {object} Response{data=[]ICMValueDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/value/timeseries [get]
func (s *Server) handleICMValueTimeseries(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := icmCache.get(r.Context(), fmt.Sprintf("value-timeseries:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadICMValueTimeseries(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICMValueTimeseries(ctx context.Context, days int) ([]ICMValueDay, error) {
	out := []ICMValueDay{}
	win, asOf, err := s.icmWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, icmValueDaysSQL(), icmBounds(asOf, win.Days)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string][]ICMValueDayToken{}
	for rows.Next() {
		var day, tok, symbol string
		var priced bool
		var n uint64
		var sent, recv float64
		if err := rows.Scan(&day, &tok, &symbol, &priced, &n, &sent, &recv); err != nil {
			return nil, err
		}
		byDay[day] = append(byDay[day], ICMValueDayToken{Address: "0x" + tok, Symbol: symbol, Transfers: n,
			SentUSD: usdPtr(sent, priced), ReceivedUSD: usdPtr(recv, priced)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for d := asOf.AddDate(0, 0, 1-win.Days); !d.After(asOf); d = d.AddDate(0, 0, 1) {
		day := ICMValueDay{Date: d.Format("2006-01-02"), ByToken: byDay[d.Format("2006-01-02")]}
		if day.ByToken == nil {
			day.ByToken = []ICMValueDayToken{}
		}
		for _, t := range day.ByToken {
			day.Transfers += t.Transfers
			if t.SentUSD != nil {
				day.SentUSD += *t.SentUSD
				day.ReceivedUSD += *t.ReceivedUSD
			}
		}
		day.TotalUSD = day.SentUSD + day.ReceivedUSD
		sort.Slice(day.ByToken, func(i, j int) bool {
			a, b := day.ByToken[i], day.ByToken[j]
			av, bv := 0.0, 0.0
			if a.SentUSD != nil {
				av = *a.SentUSD + *a.ReceivedUSD
			}
			if b.SentUSD != nil {
				bv = *b.SentUSD + *b.ReceivedUSD
			}
			if av != bv {
				return av > bv
			}
			if a.Transfers != b.Transfers {
				return a.Transfers > b.Transfers
			}
			return a.Address < b.Address
		})
		out = append(out, day)
	}
	return out, nil
}

// handleICMTopTransfers returns the largest ICTT transfers on the C-Chain
// @Summary Largest C-Chain ICTT transfers
// @Description The largest priced ICTT transfers on the C-Chain by USD over the last N complete UTC days ending as_of. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Param limit query int false "Transfers, 1-200" default(50)
// @Success 200 {object} Response{data=[]ICMTransfer}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/transfers/top [get]
func (s *Server) handleICMTopTransfers(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	limit, err := icmLimit(r, 50)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := icmCache.get(r.Context(), fmt.Sprintf("top:%d:%d", days, limit), func(ctx context.Context) (interface{}, error) {
		return s.loadICMTopTransfers(ctx, days, limit)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICMTopTransfers(ctx context.Context, days, limit int) ([]ICMTransfer, error) {
	out := []ICMTransfer{}
	win, asOf, err := s.icmWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, icmTopSQL(), append(icmBounds(asOf, win.Days), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		t        ICMTransfer
		typ      int8
		cp, app  string
		tok, sym string
		dec      uint8
		amt      string
		usd      float64
		at       time.Time
	}
	var got []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.t.MessageID, &x.t.Direction, &x.typ, &x.cp, &x.at, &x.t.TxHash, &x.app,
			&x.tok, &x.sym, &x.dec, &x.t.Recipient, &x.amt, &x.usd); err != nil {
			return nil, err
		}
		got = append(got, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return out, nil
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return nil, err
	}
	infos, err := s.icmAppInfos(ctx)
	if err != nil {
		return nil, err
	}
	for _, x := range got {
		t := x.t
		var id ids.ID
		copy(id[:], x.cp)
		t.MessageID, t.TxHash = "0x"+t.MessageID, "0x"+t.TxHash
		t.Type = icm.ICTTTypeNames[x.typ]
		t.Counterparty = ICMChainRef{BlockchainID: id.String(), Name: names[id.String()]}
		t.BlockTime = x.at.UTC().Format(time.RFC3339Nano)
		t.App = "0x" + x.app
		info, ok := infos[x.app]
		t.AppName, _, _ = s.icmAppName(x.app, info, ok)
		t.Token = ICMToken{Address: "0x" + x.tok, Symbol: x.sym, Decimals: x.dec}
		t.Recipient = "0x" + t.Recipient
		amt, usd := x.amt, x.usd
		t.Amount, t.USD = &amt, &usd
		out = append(out, t)
	}
	return out, nil
}

// icmValueSQL aggregates icmTransfersSQL per token, per other chain and in total.
func icmValueSQL() string {
	return `
		SELECT lower(hex(token)) AS tok, any(symbol), any(decimals), any(priced), counterparty AS cp,
			count(), countIf(isNotNull(usd)),
			toString(sumIf(ifNull(amount, 0), direction = 'sent')), toString(sumIf(ifNull(amount, 0), direction = 'received')),
			sumIf(ifNull(usd, 0), direction = 'sent'), sumIf(ifNull(usd, 0), direction = 'received'),
			toBool(grouping(tok) = 0), toBool(grouping(cp) = 0)
		FROM (` + icmTransfersSQL() + `)
		GROUP BY GROUPING SETS ((tok), (cp), ())
	`
}

// icmValueDaysSQL aggregates icmTransfersSQL per day and token.
func icmValueDaysSQL() string {
	return `
		SELECT toString(toDate(block_time)) AS day, lower(hex(token)) AS tok, any(symbol), any(priced), count(),
			sumIf(ifNull(usd, 0), direction = 'sent'), sumIf(ifNull(usd, 0), direction = 'received')
		FROM (` + icmTransfersSQL() + `)
		GROUP BY day, tok
	`
}

// icmTopSQL is the largest priced transfers of icmTransfersSQL. Binds a limit last.
func icmTopSQL() string {
	return `
		SELECT lower(hex(message_id)), direction, ictt_type, counterparty, block_time, lower(hex(transaction_hash)),
			lower(hex(app)), lower(hex(token)), symbol, decimals, lower(hex(recipient)), toString(assumeNotNull(amount)), assumeNotNull(usd)
		FROM (` + icmTransfersSQL() + `)
		WHERE isNotNull(usd)
		ORDER BY usd DESC, block_time DESC
		LIMIT ?
	`
}
