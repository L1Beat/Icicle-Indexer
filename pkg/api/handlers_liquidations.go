package api

import (
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Executed-liquidation endpoints, served from lending_liquidations (valued at the
// event block by the lending engine). Deduped with FINAL since the table is a
// ReplacingMergeTree keyed by the event identity.

type liquidationEvent struct {
	Protocol         string    `json:"protocol"`
	BlockNumber      uint32    `json:"block_number"`
	BlockTime        time.Time `json:"block_time"`
	TxHash           string    `json:"tx_hash"`
	Liquidator       string    `json:"liquidator"`
	Borrower         string    `json:"borrower"`
	CollateralAsset  string    `json:"collateral_asset"`
	CollateralSymbol string    `json:"collateral_symbol,omitempty"`
	DebtAsset        string    `json:"debt_asset"`
	DebtSymbol       string    `json:"debt_symbol,omitempty"`
	RepayAmount      string    `json:"repay_amount"`
	SeizeAmount      string    `json:"seize_amount"`
	RepaidUSD        string    `json:"repaid_usd"`
}

// handleLendingLiquidations returns executed liquidations newest-first, paginated,
// filterable by protocol, account (borrower or liquidator), or asset (collateral
// or debt).
func (s *Server) handleLendingLiquidations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	limit, offset := getPagination(r)
	protocol := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	account := normalizeHexAddr(r.URL.Query().Get("account"))
	asset := normalizeHexAddr(r.URL.Query().Get("asset"))

	conds := []string{"chain_id = ?"}
	args := []interface{}{chainID}
	if protocol != "" {
		conds = append(conds, "protocol = ?")
		args = append(args, protocol)
	}
	if account != "" {
		conds = append(conds, fmt.Sprintf("(liquidator = unhex('%s') OR borrower = unhex('%s'))", account, account))
	}
	if asset != "" {
		conds = append(conds, fmt.Sprintf("(collateral_asset = unhex('%s') OR debt_asset = unhex('%s'))", asset, asset))
	}

	query := fmt.Sprintf(`
		SELECT protocol, block_number, block_time, lower(hex(tx_hash)),
			lower(hex(liquidator)), lower(hex(borrower)),
			lower(hex(collateral_asset)), lower(hex(debt_asset)),
			repay_amount, seize_amount, repaid_usd
		FROM (SELECT * FROM lending_liquidations FINAL)
		WHERE %s
		ORDER BY block_number DESC, log_index DESC
		LIMIT ? OFFSET ?
	`, strings.Join(conds, " AND "))
	args = append(args, limit+1, offset)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	symbols := s.loadAssetSymbols(ctx, chainID)
	var events []liquidationEvent
	for rows.Next() {
		var e liquidationEvent
		var tx, liq, borrower, coll, debt string
		var repay, seize, usd *big.Int
		if err := rows.Scan(&e.Protocol, &e.BlockNumber, &e.BlockTime, &tx, &liq, &borrower, &coll, &debt, &repay, &seize, &usd); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		e.TxHash = "0x" + tx
		e.Liquidator = "0x" + liq
		e.Borrower = "0x" + borrower
		e.CollateralAsset = "0x" + coll
		e.DebtAsset = "0x" + debt
		e.CollateralSymbol = symbols[e.Protocol+"|0x"+coll]
		e.DebtSymbol = symbols[e.Protocol+"|0x"+debt]
		e.RepayAmount = bigStr(repay)
		e.SeizeAmount = bigStr(seize)
		e.RepaidUSD = bigStr(usd)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	writeJSON(w, http.StatusOK, Response{Data: events, Meta: &Meta{Limit: limit, Offset: offset, HasMore: hasMore}})
}

type liquidationDay struct {
	Day            string `json:"day"`
	Protocol       string `json:"protocol"`
	Count          uint64 `json:"count"`
	Over25         uint64 `json:"over_25"`
	Over100        uint64 `json:"over_100"`
	Over1000       uint64 `json:"over_1000"`
	TotalRepaidUSD string `json:"total_repaid_usd"`
}

// handleLendingLiquidationsDaily returns the daily liquidation series per protocol:
// count, sized buckets (> $25 / $100 / $1000), and total repaid USD, over a
// trailing window so crash-day spikes are visible.
func (s *Server) handleLendingLiquidationsDaily(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))

	// Trailing window in days (default 90, capped at 3650 to bound the scan).
	days := 90
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			days = n
		}
	}
	if days > 3650 {
		days = 3650
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	conds := []string{"chain_id = ?", "block_time >= ?"}
	args := []interface{}{chainID, cutoff}
	if protocol != "" {
		conds = append(conds, "protocol = ?")
		args = append(args, protocol)
	}

	query := fmt.Sprintf(`
		SELECT toString(toDate(block_time)) AS day, protocol,
			count() AS cnt,
			countIf(repaid_usd > toUInt256('25000000000000000000')) AS o25,
			countIf(repaid_usd > toUInt256('100000000000000000000')) AS o100,
			countIf(repaid_usd > toUInt256('1000000000000000000000')) AS o1000,
			sum(repaid_usd) AS total
		FROM (SELECT * FROM lending_liquidations FINAL)
		WHERE %s
		GROUP BY day, protocol
		ORDER BY day DESC, protocol
	`, strings.Join(conds, " AND "))

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	var out []liquidationDay
	for rows.Next() {
		var d liquidationDay
		var total *big.Int
		if err := rows.Scan(&d.Day, &d.Protocol, &d.Count, &d.Over25, &d.Over100, &d.Over1000, &total); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		d.TotalRepaidUSD = bigStr(total)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}
