package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// PChainTx represents a P-Chain transaction
type PChainTx struct {
	TxID        string                 `json:"tx_id" example:"2ZW6HUePB..."`
	TxType      string                 `json:"tx_type" example:"ConvertSubnetToL1"`
	BlockNumber uint64                 `json:"block_number" example:"12345678"`
	BlockTime   time.Time              `json:"block_time"`
	TxData      map[string]interface{} `json:"tx_data"`
}

// handleListPChainTxs returns a paginated list of P-Chain transactions
// @Summary List P-Chain transactions
// @Description Get a paginated list of P-Chain transactions with optional filtering
// @Tags Data - P-Chain
// @Produce json
// @Param tx_type query string false "Filter by transaction type"
// @Param subnet_id query string false "Filter by subnet ID"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]PChainTx,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/pchain/txs [get]
func (s *Server) handleListPChainTxs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)

	txType := r.URL.Query().Get("tx_type")
	subnetID := r.URL.Query().Get("subnet_id")

	fetchLimit := limit + 1

	// Build WHERE clauses
	whereParts := []string{}
	whereArgs := []interface{}{}

	if cursor != nil {
		whereParts = append(whereParts, "block_number < ?")
		whereArgs = append(whereArgs, cursor.BlockNumber)
	}

	if txType != "" {
		whereParts = append(whereParts, "tx_type = ?")
		whereArgs = append(whereArgs, txType)
	}

	// Optional exact-block filter (used to list a single block's transactions).
	if bn := r.URL.Query().Get("block_number"); bn != "" {
		if parsed, err := strconv.ParseUint(bn, 10, 64); err == nil {
			whereParts = append(whereParts, "block_number = ?")
			whereArgs = append(whereArgs, parsed)
		}
	}

	if subnetID != "" {
		// Match txs that belong to the subnet through any of:
		//  - a direct subnetID in the tx body (Convert/CreateChain/AddSubnetValidator/…)
		//  - a validationID that resolves to the subnet (IncreaseL1ValidatorBalance, DisableL1Validator)
		//  - the validator's registration tx (RegisterL1Validator == l1_validator_history.created_tx_id)
		// tx_data is a ClickHouse JSON column; sub-fields are Dynamic and must be CAST to
		// String to compare (a bare `tx_data.subnetID = ?` silently matches nothing). The
		// camelCase paths mirror what the P-Chain syncer uses (pchainsyncer/ingest.go).
		// SetL1ValidatorWeight carries its validationID only inside a Warp message, so the
		// indexer decodes it into l1_validator_weight_txs; we join through that here.
		whereParts = append(whereParts, `(
			CAST(tx_data.subnetID AS String) = ?
			OR CAST(tx_data.validationID AS String) IN (
				SELECT validation_id FROM l1_validator_history FINAL WHERE subnet_id = ?
			)
			OR tx_id IN (
				SELECT created_tx_id FROM l1_validator_history FINAL WHERE subnet_id = ? AND created_tx_id != ''
			)
			OR tx_id IN (
				SELECT tx_id FROM l1_validator_weight_txs FINAL
				WHERE validation_id IN (SELECT validation_id FROM l1_validator_history FINAL WHERE subnet_id = ?)
			)
		)`)
		whereArgs = append(whereArgs, subnetID, subnetID, subnetID, subnetID)
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + whereParts[0]
		for _, p := range whereParts[1:] {
			whereClause += " AND " + p
		}
	}

	// p_chain_txs is sorted by (p_chain_id, tx_id), so "ORDER BY block_number" is
	// a full sort. Materializing the tx_data JSON column for all rows during that
	// sort exceeds the query memory limit on a multi-million-row table. So first
	// pick the page's tx_ids with a cheap sort that never reads tx_data, then
	// fetch full rows (incl. tx_data) for just those ids.
	var query string
	var args []interface{}

	if cursor != nil {
		query = fmt.Sprintf(`
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			WHERE tx_id IN (
				SELECT tx_id FROM p_chain_txs FINAL
				%s
				ORDER BY block_number DESC
				LIMIT ?
			)
			ORDER BY block_number DESC
		`, whereClause)
		args = append(whereArgs, fetchLimit)
	} else {
		query = fmt.Sprintf(`
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			WHERE tx_id IN (
				SELECT tx_id FROM p_chain_txs FINAL
				%s
				ORDER BY block_number DESC
				LIMIT ? OFFSET ?
			)
			ORDER BY block_number DESC
		`, whereClause)
		args = append(whereArgs, fetchLimit, offset)
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	txs := []PChainTx{}
	for rows.Next() {
		var tx PChainTx
		if err := rows.Scan(&tx.TxID, &tx.TxType, &tx.BlockNumber, &tx.BlockTime, &tx.TxData); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		txs = append(txs, tx)
	}
	// Surface a mid-stream query failure instead of silently returning [].
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	txs, hasMore := trimResults(txs, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(txs) > 0 {
		meta.NextCursor = cursorBlock(txs[len(txs)-1].BlockNumber)
	}

	if wantCount {
		var countQuery string
		var countArgs []interface{}
		if cursor != nil {
			// Rebuild WHERE without cursor filter for total count
			countParts := []string{}
			if txType != "" {
				countParts = append(countParts, "tx_type = ?")
			}
			if subnetID != "" {
				countParts = append(countParts, `(
						CAST(tx_data.subnetID AS String) = ?
						OR CAST(tx_data.validationID AS String) IN (SELECT validation_id FROM l1_validator_history FINAL WHERE subnet_id = ?)
						OR tx_id IN (SELECT created_tx_id FROM l1_validator_history FINAL WHERE subnet_id = ? AND created_tx_id != '')
						OR tx_id IN (SELECT tx_id FROM l1_validator_weight_txs FINAL WHERE validation_id IN (SELECT validation_id FROM l1_validator_history FINAL WHERE subnet_id = ?))
					)`)
			}
			countWhere := ""
			if len(countParts) > 0 {
				countWhere = "WHERE " + countParts[0]
				for _, p := range countParts[1:] {
					countWhere += " AND " + p
				}
			}
			countQuery = fmt.Sprintf(`SELECT toInt64(count()) FROM p_chain_txs FINAL %s`, countWhere)
			countArgs = whereArgs[1:] // skip cursor arg
		} else {
			countQuery = fmt.Sprintf(`SELECT toInt64(count()) FROM p_chain_txs FINAL %s`, whereClause)
			countArgs = whereArgs
		}
		var total int64
		_ = s.conn.QueryRow(ctx, countQuery, countArgs...).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: meta,
	})
}

// handleGetPChainTx returns a single P-Chain transaction
// @Summary Get P-Chain transaction by ID
// @Description Get details for a specific P-Chain transaction
// @Tags Data - P-Chain
// @Produce json
// @Param txId path string true "Transaction ID"
// @Success 200 {object} Response{data=PChainTx}
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/pchain/txs/{txId} [get]
func (s *Server) handleGetPChainTx(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txID := r.PathValue("txId")

	var tx PChainTx
	err := s.conn.QueryRow(ctx, `
		SELECT tx_id, tx_type, block_number, block_time, tx_data
		FROM p_chain_txs FINAL
		WHERE tx_id = ?
		LIMIT 1
	`, txID).Scan(&tx.TxID, &tx.TxType, &tx.BlockNumber, &tx.BlockTime, &tx.TxData)

	if err != nil {
		writeNotFoundError(w, "Transaction")
		return
	}

	writeJSON(w, http.StatusOK, Response{Data: tx})
}

// handlePChainTxTypes returns transaction type counts
// @Summary List P-Chain transaction types
// @Description Get a list of P-Chain transaction types with counts. Pass ?days=N to restrict to the last N days.
// @Tags Data - P-Chain
// @Produce json
// @Param days query int false "Restrict to the last N days (default: all time)"
// @Success 200 {object} Response{data=[]object}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/pchain/tx-types [get]
func (s *Server) handlePChainTxTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Optional time window. Default (0) means all-time.
	query := `
		SELECT tx_type, count() as count
		FROM p_chain_txs FINAL
		GROUP BY tx_type
		ORDER BY count DESC
	`
	var args []interface{}
	if d := r.URL.Query().Get("days"); d != "" {
		if days, err := strconv.Atoi(d); err == nil && days > 0 {
			query = `
				SELECT tx_type, count() as count
				FROM p_chain_txs FINAL
				WHERE block_time >= now() - toIntervalDay(?)
				GROUP BY tx_type
				ORDER BY count DESC
			`
			args = append(args, days)
		}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	type TxTypeCount struct {
		TxType string `json:"tx_type"`
		Count  uint64 `json:"count"`
	}

	types := []TxTypeCount{}
	for rows.Next() {
		var t TxTypeCount
		if err := rows.Scan(&t.TxType, &t.Count); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		types = append(types, t)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, Response{Data: types})
}
