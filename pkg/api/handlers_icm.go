package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ava-labs/avalanchego/ids"

	"icicle/pkg/icm"
)

// ICMCounts is a message count split by direction, seen from the C-Chain.
type ICMCounts struct {
	Sent     uint64 `json:"sent"`     // sent from the C-Chain
	Received uint64 `json:"received"` // received on the C-Chain
	Total    uint64 `json:"total"`
}

// ICMChain is the C-Chain's traffic with one other chain.
type ICMChain struct {
	BlockchainID string `json:"blockchain_id"` // CB58
	Name         string `json:"name"`          // from the L1 registry, empty when unlisted
	ICMCounts
}

// ICMOverview is the /metrics/icm payload.
type ICMOverview struct {
	PerpWindow
	Totals  ICMCounts  `json:"totals"`
	ByChain []ICMChain `json:"by_chain"`
}

// ICMDay is one UTC day.
type ICMDay struct {
	Date string `json:"date"`
	ICMCounts
	ByChain []ICMChain  `json:"by_chain"`
	ByApp   []ICMAppDay `json:"by_app,omitempty"` // with by_app=true, left out on quiet days
}

var icmCache = newTTLCache(5 * time.Minute)

// icmWindow resolves the window ending at the last complete UTC day the ICM
// sync has passed, clamped to the day the TeleporterMessenger was deployed.
func (s *Server) icmWindow(ctx context.Context, days int) (PerpWindow, time.Time, error) {
	w := PerpWindow{Days: days}
	through, err := icm.Through(ctx, s.conn)
	if err != nil {
		return w, time.Time{}, err
	}
	asOf := through.Truncate(24*time.Hour).AddDate(0, 0, -1)
	if through.IsZero() || asOf.Before(icm.Start) {
		return w, time.Time{}, nil
	}
	from := asOf.AddDate(0, 0, 1-days)
	if from.Before(icm.Start) {
		from = icm.Start
		w.Days = int(asOf.Sub(icm.Start).Hours()/24) + 1
	}
	a, f := asOf.Format("2006-01-02"), from.Format("2006-01-02")
	w.AsOf, w.From = &a, &f
	return w, asOf, nil
}

// icmChainNames maps CB58 blockchain IDs to their L1 registry names.
func (s *Server) icmChainNames(ctx context.Context) (map[string]string, error) {
	rows, err := s.conn.Query(ctx, `SELECT blockchain_id, name FROM l1_registry FINAL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// icmRow is the message count for one day, direction and other chain.
type icmRow struct {
	day, direction string
	chain          string // CB58
	n              uint64
}

func (s *Server) icmRows(ctx context.Context, asOf time.Time, days int) ([]icmRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(block_time)) AS day, toString(direction), counterparty, count()
		FROM (SELECT * FROM icm_messages FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY day, direction, counterparty
		ORDER BY day
	`, asOf.AddDate(0, 0, 1-days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []icmRow
	for rows.Next() {
		var r icmRow
		var raw string
		if err := rows.Scan(&r.day, &r.direction, &raw, &r.n); err != nil {
			return nil, err
		}
		var id ids.ID
		copy(id[:], raw)
		r.chain = id.String()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *ICMCounts) add(direction string, n uint64) {
	if direction == "sent" {
		c.Sent += n
	} else {
		c.Received += n
	}
	c.Total += n
}

// icmChains turns per-chain counts into a list, busiest first.
func icmChains(m map[string]*ICMCounts, names map[string]string) []ICMChain {
	out := make([]ICMChain, 0, len(m))
	for id, c := range m {
		out = append(out, ICMChain{BlockchainID: id, Name: names[id], ICMCounts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].BlockchainID < out[j].BlockchainID
	})
	return out
}

// handleICM returns C-Chain ICM message counts
// @Summary C-Chain ICM (Teleporter) messages
// @Description ICM messages sent from and received on the C-Chain over the last N complete UTC days ending as_of, total and per other chain (destination for sends, source for receives). Counted from the official TeleporterMessenger's SendCrossChainMessage and ReceiveCrossChainMessage events, one per messageID in each direction, from 2024-03-05. Messages between two L1s that never touch the C-Chain are not included. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Success 200 {object} Response{data=ICMOverview}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm [get]
func (s *Server) handleICM(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := icmCache.get(r.Context(), fmt.Sprintf("overview:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadICM(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICM(ctx context.Context, days int) (ICMOverview, error) {
	out := ICMOverview{ByChain: []ICMChain{}}
	win, asOf, err := s.icmWindow(ctx, days)
	out.PerpWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.icmRows(ctx, asOf, win.Days)
	if err != nil {
		return out, err
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return out, err
	}
	chains := map[string]*ICMCounts{}
	for _, r := range rows {
		out.Totals.add(r.direction, r.n)
		if chains[r.chain] == nil {
			chains[r.chain] = &ICMCounts{}
		}
		chains[r.chain].add(r.direction, r.n)
	}
	out.ByChain = icmChains(chains, names)
	return out, nil
}

// handleICMTimeseries returns daily C-Chain ICM message counts
// @Summary C-Chain ICM (Teleporter) messages per day
// @Description Messages sent from and received on the C-Chain per UTC day, total and per other chain, for the last N complete days ending as_of. With by_app=true, also per C-Chain contract (see /metrics/icm/apps). Oldest first, every day included (zero when quiet), from 2024-03-05. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Param by_app query bool false "Add the per-app split"
// @Success 200 {object} Response{data=[]ICMDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/timeseries [get]
func (s *Server) handleICMTimeseries(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	byApp := r.URL.Query().Get("by_app") == "true"
	v, err := icmCache.get(r.Context(), fmt.Sprintf("timeseries:%d:%v", days, byApp), func(ctx context.Context) (interface{}, error) {
		return s.loadICMTimeseries(ctx, days, byApp)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICMTimeseries(ctx context.Context, days int, byApp bool) ([]ICMDay, error) {
	out := []ICMDay{}
	win, asOf, err := s.icmWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.icmRows(ctx, asOf, win.Days)
	if err != nil {
		return nil, err
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return nil, err
	}
	byDay := map[string]map[string]*ICMCounts{}
	for _, r := range rows {
		if byDay[r.day] == nil {
			byDay[r.day] = map[string]*ICMCounts{}
		}
		if byDay[r.day][r.chain] == nil {
			byDay[r.day][r.chain] = &ICMCounts{}
		}
		byDay[r.day][r.chain].add(r.direction, r.n)
	}
	var apps map[string][]ICMAppDay
	if byApp {
		if apps, err = s.icmAppDays(ctx, asOf, win.Days); err != nil {
			return nil, err
		}
	}
	for d := asOf.AddDate(0, 0, 1-win.Days); !d.After(asOf); d = d.AddDate(0, 0, 1) {
		day := ICMDay{Date: d.Format("2006-01-02")}
		day.ByChain = icmChains(byDay[day.Date], names)
		for _, c := range day.ByChain {
			day.Sent += c.Sent
			day.Received += c.Received
			day.Total += c.Total
		}
		if byApp {
			day.ByApp = apps[day.Date]
		}
		out = append(out, day)
	}
	return out, nil
}

// ICMToken is the token an ICTT app moves on the C-Chain. Symbol and decimals
// are what the token contract reports.
type ICMToken struct {
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Decimals uint8  `json:"decimals"`
}

// ICMApp is one C-Chain contract that sends or receives ICM messages.
type ICMApp struct {
	Address string        `json:"address"`
	Name    string        `json:"name"`  // label name, else e.g. "XOM ICTT remote", else empty
	Kind    string        `json:"kind"`  // ictt-home, ictt-remote or other
	Token   *ICMToken     `json:"token"` // ICTT apps only
	Label   *AddressLabel `json:"label"`
	ICMCounts
	FirstSeen string     `json:"first_seen"` // all time, not just the window
	LastSeen  string     `json:"last_seen"`
	TopChains []ICMChain `json:"top_chains"` // busiest other chains in the window, up to 5
}

// ICMApps is the /metrics/icm/apps payload.
type ICMApps struct {
	PerpWindow
	Apps []ICMApp `json:"apps"`
}

// ICMAppDay is one app on one day.
type ICMAppDay struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	ICMCounts
}

// icmAppTopChains is how many other chains an app lists.
const icmAppTopChains = 5

// zeroAddress is a message whose details did not decode.
var zeroAddress = strings.Repeat("0", 40)

type icmAppInfo struct {
	kind, token, symbol string
	decimals            uint8
}

// icmAppInfos maps checked C-Chain contracts (lowercase hex, no 0x) to their
// ICTT kind and token.
func (s *Server) icmAppInfos(ctx context.Context) (map[string]icmAppInfo, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(address)), kind, lower(hex(token)), symbol, decimals FROM icm_apps FINAL WHERE kind != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]icmAppInfo{}
	for rows.Next() {
		var a string
		var i icmAppInfo
		if err := rows.Scan(&a, &i.kind, &i.token, &i.symbol, &i.decimals); err != nil {
			return nil, err
		}
		out[a] = i
	}
	return out, rows.Err()
}

// genericICTTName matches the verified-source names of the stock ICTT
// contracts, which many apps share.
var genericICTTName = regexp.MustCompile(`^(ERC20|Native)Token(Home|Remote)(Upgradeable)?$`)

// icmAppName is an app's label name, else its ICTT token and side. An ICTT
// app labelled only with a stock contract name is named by its token too.
func (s *Server) icmAppName(addr string, info icmAppInfo, ok bool) (string, string, *AddressLabel) {
	label := s.labelFor(icm.ChainID, "0x"+addr)
	kind := "other"
	if ok {
		kind = info.kind
	}
	name := ""
	if label != nil {
		name = label.Name
	}
	if ok && info.symbol != "" && (name == "" || genericICTTName.MatchString(name)) {
		name = info.symbol + " ICTT " + strings.TrimPrefix(info.kind, "ictt-")
	}
	return name, kind, label
}

// handleICMApps returns the C-Chain contracts using ICM
// @Summary C-Chain ICM apps
// @Description C-Chain contracts sending or receiving ICM messages over the last N complete UTC days ending as_of: sends from them (originSenderAddress), receives to them (destinationAddress), their busiest other chains, and first and last message all time. ICTT token bridges are marked ictt-home or ictt-remote after an on-chain check, with their token. Names come from address labels, else the ICTT token. Busiest first. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Param limit query int false "Apps, 1-200" default(50)
// @Success 200 {object} Response{data=ICMApps}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/apps [get]
func (s *Server) handleICMApps(w http.ResponseWriter, r *http.Request) {
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
	v, err := icmCache.get(r.Context(), fmt.Sprintf("apps:%d:%d", days, limit), func(ctx context.Context) (interface{}, error) {
		return s.loadICMApps(ctx, days, limit)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func icmLimit(r *http.Request, def int) (int, error) {
	q := r.URL.Query().Get("limit")
	if q == "" {
		return def, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > 200 {
		return 0, fmt.Errorf("limit must be 1-200")
	}
	return n, nil
}

func (s *Server) loadICMApps(ctx context.Context, days, limit int) (ICMApps, error) {
	out := ICMApps{Apps: []ICMApp{}}
	win, asOf, err := s.icmWindow(ctx, days)
	out.PerpWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(local_address)) AS app, toString(direction), counterparty, count()
		FROM (SELECT * FROM icm_messages FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY app, direction, counterparty
	`, asOf.AddDate(0, 0, 1-win.Days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return out, err
	}
	totals := map[string]*ICMCounts{}
	chains := map[string]map[string]*ICMCounts{}
	for rows.Next() {
		var app, direction, raw string
		var n uint64
		if err := rows.Scan(&app, &direction, &raw, &n); err != nil {
			rows.Close()
			return out, err
		}
		if app == zeroAddress {
			continue
		}
		var id ids.ID
		copy(id[:], raw)
		if totals[app] == nil {
			totals[app] = &ICMCounts{}
			chains[app] = map[string]*ICMCounts{}
		}
		totals[app].add(direction, n)
		if chains[app][id.String()] == nil {
			chains[app][id.String()] = &ICMCounts{}
		}
		chains[app][id.String()].add(direction, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	addrs := make([]string, 0, len(totals))
	for a := range totals {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool {
		if totals[addrs[i]].Total != totals[addrs[j]].Total {
			return totals[addrs[i]].Total > totals[addrs[j]].Total
		}
		return addrs[i] < addrs[j]
	})
	if len(addrs) > limit {
		addrs = addrs[:limit]
	}
	if len(addrs) == 0 {
		return out, nil
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return out, err
	}
	infos, err := s.icmAppInfos(ctx)
	if err != nil {
		return out, err
	}
	// Addresses come from hex() above, so they are safe to inline.
	lits := make([]string, len(addrs))
	for i, a := range addrs {
		lits[i] = "unhex('" + a + "')"
	}
	seen := map[string][2]time.Time{}
	srows, err := s.conn.Query(ctx, `
		SELECT lower(hex(local_address)), min(block_time), max(block_time)
		FROM icm_messages
		WHERE local_address IN (`+strings.Join(lits, ", ")+`)
		GROUP BY local_address`)
	if err != nil {
		return out, err
	}
	for srows.Next() {
		var a string
		var first, last time.Time
		if err := srows.Scan(&a, &first, &last); err != nil {
			srows.Close()
			return out, err
		}
		seen[a] = [2]time.Time{first, last}
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return out, err
	}
	for _, a := range addrs {
		info, ok := infos[a]
		app := ICMApp{Address: "0x" + a, ICMCounts: *totals[a]}
		app.Name, app.Kind, app.Label = s.icmAppName(a, info, ok)
		if ok {
			app.Token = &ICMToken{Address: "0x" + info.token, Symbol: info.symbol, Decimals: info.decimals}
		}
		app.FirstSeen = seen[a][0].UTC().Format(time.RFC3339)
		app.LastSeen = seen[a][1].UTC().Format(time.RFC3339)
		app.TopChains = icmChains(chains[a], names)
		if len(app.TopChains) > icmAppTopChains {
			app.TopChains = app.TopChains[:icmAppTopChains]
		}
		out.Apps = append(out.Apps, app)
	}
	return out, nil
}

// ICMMessage is one message sent from or received on the C-Chain.
type ICMMessage struct {
	MessageID        string        `json:"message_id"`
	Direction        string        `json:"direction"` // sent (from the C-Chain) or received (on it)
	Counterparty     ICMChainRef   `json:"counterparty"`
	BlockTime        string        `json:"block_time"`
	BlockNumber      uint32        `json:"block_number"`
	TxHash           string        `json:"tx_hash"`
	LocalAddress     string        `json:"local_address"`  // C-Chain contract: originSenderAddress for sends, destinationAddress for receives
	RemoteAddress    string        `json:"remote_address"` // L1 address: destinationAddress for sends, originSenderAddress for receives
	App              *ICMAppRef    `json:"app"`
	RequiredGasLimit uint64        `json:"required_gas_limit"`
	FeeToken         *string       `json:"fee_token"`  // sends only
	FeeAmount        *string       `json:"fee_amount"` // sends only, in fee token units
	Deliverer        *string       `json:"deliverer"`  // receives only: the relayer
	RewardRedeemer   *string       `json:"reward_redeemer"`
	Executed         *bool         `json:"executed"` // receives only: MessageExecuted seen, on receipt or a retry
	ICTT             *ICMICTTValue `json:"ictt"`     // ICTT apps only
}

// ICMChainRef names another chain.
type ICMChainRef struct {
	BlockchainID string `json:"blockchain_id"`
	Name         string `json:"name"`
}

// ICMAppRef names the C-Chain contract of a message.
type ICMAppRef struct {
	Name  string        `json:"name"`
	Kind  string        `json:"kind"`
	Label *AddressLabel `json:"label"`
}

// ICMICTTValue is an ICTT transfer's payload.
type ICMICTTValue struct {
	Type      string    `json:"type"` // single_hop_send, single_hop_call, multi_hop_send, multi_hop_call, register_remote
	Recipient string    `json:"recipient"`
	AmountRaw string    `json:"amount_raw"` // as encoded in the message, before any decimals scaling between home and remote
	Token     *ICMToken `json:"token"`
}

// handleICMMessages lists recent C-Chain ICM messages
// @Summary Recent C-Chain ICM messages
// @Description ICM messages sent from or received on the C-Chain, newest first, with the C-Chain contract and L1 address, relayer fee (sends), deliverer and reward redeemer (receives), and the ICTT payload for ICTT apps. Page with before=meta.next_cursor. Cached for 1 minute.
// @Tags Metrics - ICM
// @Produce json
// @Param limit query int false "Messages, 1-200" default(50)
// @Param before query string false "Cursor from meta.next_cursor"
// @Param chain query string false "Other chain's blockchain ID (CB58)"
// @Param direction query string false "sent or received"
// @Param app query string false "C-Chain contract address"
// @Success 200 {object} Response{data=[]ICMMessage,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/messages [get]
func (s *Server) handleICMMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := icmLimit(r, 50)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	var where []string
	var args []interface{}
	if c := q.Get("before"); c != "" {
		var block, logIndex uint32
		if _, err := fmt.Sscanf(c, "%d-%d", &block, &logIndex); err != nil || fmt.Sprintf("%d-%d", block, logIndex) != c {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "invalid before cursor")
			return
		}
		where = append(where, "(block_number, log_index) < (?, ?)")
		args = append(args, block, logIndex)
	}
	if c := q.Get("chain"); c != "" {
		id, err := ids.FromString(c)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "chain must be a CB58 blockchain ID")
			return
		}
		where = append(where, "counterparty = ?")
		args = append(args, string(id[:]))
	}
	if d := q.Get("direction"); d != "" {
		if d != "sent" && d != "received" {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "direction must be sent or received")
			return
		}
		where = append(where, "direction = ?")
		args = append(args, d)
	}
	if a := q.Get("app"); a != "" {
		b, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(a), "0x"))
		if err != nil || len(b) != 20 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "app must be a 0x address")
			return
		}
		where = append(where, "local_address = ?")
		args = append(args, string(b))
	}
	key := fmt.Sprintf("messages:%d:%s", limit, r.URL.Query().Encode())
	v, err := icmRecentCache.get(r.Context(), key, func(ctx context.Context) (interface{}, error) {
		return s.loadICMMessages(ctx, where, args, limit)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	res := v.(icmMessagePage)
	writeJSON(w, http.StatusOK, Response{Data: res.messages, Meta: &Meta{Limit: limit, HasMore: res.next != "", NextCursor: res.next}})
}

var icmRecentCache = newTTLCache(time.Minute)

type icmMessagePage struct {
	messages []ICMMessage
	next     string
}

func (s *Server) loadICMMessages(ctx context.Context, where []string, args []interface{}, limit int) (icmMessagePage, error) {
	page := icmMessagePage{messages: []ICMMessage{}}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.conn.Query(ctx, `
		SELECT lower(hex(message_id)), toString(direction), counterparty, block_time, block_number,
			lower(hex(transaction_hash)), log_index, lower(hex(local_address)), lower(hex(remote_address)),
			required_gas_limit, lower(hex(fee_token)), fee_amount, lower(hex(deliverer)), lower(hex(reward_redeemer)),
			ictt_type, lower(hex(ictt_recipient)), ictt_amount
		FROM (SELECT * FROM icm_messages FINAL) t
		`+cond+`
		ORDER BY block_number DESC, log_index DESC
		LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	type row struct {
		m          ICMMessage
		logIndex   uint32
		local      string
		raw        string
		fee        string
		feeAmt     big.Int
		deliverer  string
		redeemer   string
		icttType   int8
		recipient  string
		icttAmount big.Int
	}
	var got []row
	for rows.Next() {
		var x row
		var t time.Time
		if err := rows.Scan(&x.m.MessageID, &x.m.Direction, &x.raw, &t, &x.m.BlockNumber, &x.m.TxHash, &x.logIndex,
			&x.local, &x.m.RemoteAddress, &x.m.RequiredGasLimit, &x.fee, &x.feeAmt, &x.deliverer, &x.redeemer,
			&x.icttType, &x.recipient, &x.icttAmount); err != nil {
			return page, err
		}
		x.m.BlockTime = t.UTC().Format(time.RFC3339Nano)
		got = append(got, x)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(got) > limit {
		last := got[limit-1]
		page.next = fmt.Sprintf("%d-%d", last.m.BlockNumber, last.logIndex)
		got = got[:limit]
	}
	if len(got) == 0 {
		return page, nil
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return page, err
	}
	infos, err := s.icmAppInfos(ctx)
	if err != nil {
		return page, err
	}
	// Message IDs come from hex() above, so they are safe to inline.
	var received []string
	for _, x := range got {
		if x.m.Direction == "received" {
			received = append(received, "unhex('"+x.m.MessageID+"')")
		}
	}
	executed := map[string]bool{}
	if len(received) > 0 {
		erows, err := s.conn.Query(ctx, `SELECT DISTINCT lower(hex(message_id)) FROM icm_executions WHERE message_id IN (`+strings.Join(received, ", ")+`)`)
		if err != nil {
			return page, err
		}
		for erows.Next() {
			var id string
			if err := erows.Scan(&id); err != nil {
				erows.Close()
				return page, err
			}
			executed[id] = true
		}
		erows.Close()
		if err := erows.Err(); err != nil {
			return page, err
		}
	}
	for _, x := range got {
		m := x.m
		ran := executed[m.MessageID]
		m.MessageID = "0x" + m.MessageID
		m.TxHash = "0x" + m.TxHash
		var id ids.ID
		copy(id[:], x.raw)
		m.Counterparty = ICMChainRef{BlockchainID: id.String(), Name: names[id.String()]}
		m.LocalAddress = "0x" + x.local
		m.RemoteAddress = "0x" + m.RemoteAddress
		info, ok := infos[x.local]
		if x.local != zeroAddress {
			ref := ICMAppRef{}
			ref.Name, ref.Kind, ref.Label = s.icmAppName(x.local, info, ok)
			m.App = &ref
		}
		if m.Direction == "sent" {
			fee, amt := "0x"+x.fee, x.feeAmt.String()
			m.FeeToken, m.FeeAmount = &fee, &amt
		} else {
			d, rr := "0x"+x.deliverer, "0x"+x.redeemer
			m.Deliverer, m.RewardRedeemer, m.Executed = &d, &rr, &ran
		}
		if ok && x.icttType >= 0 {
			m.ICTT = &ICMICTTValue{
				Type:      icm.ICTTTypeNames[x.icttType],
				Recipient: "0x" + x.recipient,
				AmountRaw: x.icttAmount.String(),
				Token:     &ICMToken{Address: "0x" + info.token, Symbol: info.symbol, Decimals: info.decimals},
			}
		}
		page.messages = append(page.messages, m)
	}
	return page, nil
}

// icmAppDays is the per-app split of each day, busiest first.
func (s *Server) icmAppDays(ctx context.Context, asOf time.Time, days int) (map[string][]ICMAppDay, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(block_time)) AS day, lower(hex(local_address)) AS app, toString(direction), count()
		FROM (SELECT * FROM icm_messages FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY day, app, direction
	`, asOf.AddDate(0, 0, 1-days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	counts := map[string]map[string]*ICMCounts{}
	for rows.Next() {
		var day, app, direction string
		var n uint64
		if err := rows.Scan(&day, &app, &direction, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if app == zeroAddress {
			continue
		}
		if counts[day] == nil {
			counts[day] = map[string]*ICMCounts{}
		}
		if counts[day][app] == nil {
			counts[day][app] = &ICMCounts{}
		}
		counts[day][app].add(direction, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	infos, err := s.icmAppInfos(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]ICMAppDay{}
	for day, m := range counts {
		list := make([]ICMAppDay, 0, len(m))
		for a, c := range m {
			info, ok := infos[a]
			name, _, _ := s.icmAppName(a, info, ok)
			list = append(list, ICMAppDay{Address: "0x" + a, Name: name, ICMCounts: *c})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Total != list[j].Total {
				return list[i].Total > list[j].Total
			}
			return list[i].Address < list[j].Address
		})
		out[day] = list
	}
	return out, nil
}
