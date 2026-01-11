package api

import (
	"net/http"
	"time"
)

type PChainTx struct {
	TxID        string                 `json:"tx_id"`
	TxType      string                 `json:"tx_type"`
	BlockNumber uint64                 `json:"block_number"`
	BlockTime   time.Time              `json:"block_time"`
	TxData      map[string]interface{} `json:"tx_data"`
}

func (s *Server) handleListPChainTxs(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	txs := []PChainTx{}
	for rows.Next() {
		var tx PChainTx
		if err := rows.Scan(&tx.TxID, &tx.TxType, &tx.BlockNumber, &tx.BlockTime, &tx.TxData); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		txs = append(txs, tx)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

func (s *Server) handleGetPChainTx(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	txID := r.PathValue("txId")

	var tx PChainTx
	err := s.conn.QueryRow(ctx, `
		SELECT tx_id, tx_type, block_number, block_time, tx_data
		FROM p_chain_txs FINAL
		WHERE tx_id = ?
		LIMIT 1
	`, txID).Scan(&tx.TxID, &tx.TxType, &tx.BlockNumber, &tx.BlockTime, &tx.TxData)

	if err != nil {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}

	writeJSON(w, http.StatusOK, Response{Data: tx})
}

func (s *Server) handlePChainTxTypes(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	rows, err := s.conn.Query(ctx, `
		SELECT tx_type, count() as count
		FROM p_chain_txs FINAL
		GROUP BY tx_type
		ORDER BY count DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		types = append(types, t)
	}

	writeJSON(w, http.StatusOK, Response{Data: types})
}
