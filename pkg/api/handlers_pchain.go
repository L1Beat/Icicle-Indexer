package api

import (
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

	txType := r.URL.Query().Get("tx_type")
	subnetID := r.URL.Query().Get("subnet_id")

	var query string
	var args []interface{}

	if txType != "" && subnetID != "" {
		query = `
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			WHERE tx_type = ? AND (
				tx_data.Subnet = ? OR
				tx_data.SubnetID = ? OR
				tx_data.SubnetValidator.Subnet = ?
			)
			ORDER BY block_number DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{txType, subnetID, subnetID, subnetID, limit, offset}
	} else if txType != "" {
		query = `
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			WHERE tx_type = ?
			ORDER BY block_number DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{txType, limit, offset}
	} else if subnetID != "" {
		query = `
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			WHERE tx_data.Subnet = ? OR
				tx_data.SubnetID = ? OR
				tx_data.SubnetValidator.Subnet = ?
			ORDER BY block_number DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{subnetID, subnetID, subnetID, limit, offset}
	} else {
		query = `
			SELECT tx_id, tx_type, block_number, block_time, tx_data
			FROM p_chain_txs FINAL
			ORDER BY block_number DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
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

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: &Meta{Limit: limit, Offset: offset},
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
