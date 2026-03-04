package api

import (
	"fmt"
	"net/http"
	"time"
)

// PChainTx represents a P-Chain transaction
type PChainTx struct {
	TxID        string                 `json:"tx_id" example:"2ZW6HUePB..."`
	TxType      string                 `json:"tx_type" example:"ConvertSubnetToL1Tx"`
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

	if subnetID != "" {
		whereParts = append(whereParts, "(tx_data.Subnet = ? OR tx_data.SubnetID = ? OR tx_data.SubnetValidator.Subnet = ?)")
		whereArgs = append(whereArgs, subnetID, subnetID, subnetID)
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + whereParts[0]
		for _, p := range whereParts[1:] {
			whereClause += " AND " + p
		}
	}

	var query string
	var args []interface{}

	if cursor != nil {
		query = fmt.Sprintf(`
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			%s
			ORDER BY block_number DESC
			LIMIT ?
		`, whereClause)
		args = append(whereArgs, fetchLimit)
	} else {
		query = fmt.Sprintf(`
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			%s
			ORDER BY block_number DESC
			LIMIT ? OFFSET ?
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
				countParts = append(countParts, "(tx_data.Subnet = ? OR tx_data.SubnetID = ? OR tx_data.SubnetValidator.Subnet = ?)")
			}
			countWhere := ""
			if len(countParts) > 0 {
				countWhere = "WHERE " + countParts[0]
				for _, p := range countParts[1:] {
					countWhere += " AND " + p
				}
			}
			countQuery = fmt.Sprintf(`SELECT count() FROM p_chain_txs FINAL %s`, countWhere)
			countArgs = whereArgs[1:] // skip cursor arg
		} else {
			countQuery = fmt.Sprintf(`SELECT count() FROM p_chain_txs FINAL %s`, whereClause)
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
// @Description Get a list of P-Chain transaction types with counts
// @Tags Data - P-Chain
// @Produce json
// @Success 200 {object} Response{data=[]object}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/pchain/tx-types [get]
func (s *Server) handlePChainTxTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := s.conn.Query(ctx, `
		SELECT tx_type, count() as count
		FROM p_chain_txs FINAL
		GROUP BY tx_type
		ORDER BY count DESC
	`)
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

	writeJSON(w, http.StatusOK, Response{Data: types})
}
