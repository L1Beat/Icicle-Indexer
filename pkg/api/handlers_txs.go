package api

import (
	"encoding/hex"
	"math/big"
	"net/http"
	"time"
)

type Transaction struct {
	ChainID          uint32    `json:"chain_id"`
	Hash             string    `json:"hash"`
	BlockNumber      uint32    `json:"block_number"`
	BlockTime        time.Time `json:"block_time"`
	TransactionIndex uint16    `json:"transaction_index"`
	From             string    `json:"from"`
	To               *string   `json:"to"` // null for contract creation
	Value            string    `json:"value"`
	GasLimit         uint32    `json:"gas_limit"`
	GasPrice         uint64    `json:"gas_price"`
	GasUsed          uint32    `json:"gas_used"`
	Success          bool      `json:"success"`
	Type             uint8     `json:"type"`
}

func (s *Server) handleListTxs(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	rows, err := s.conn.Query(ctx, `
		SELECT
			chain_id, hash, block_number, block_time, transaction_index,
			from, to, value, gas_limit, gas_price, gas_used, success, type
		FROM raw_txs
		WHERE chain_id = ?
		ORDER BY block_number DESC, transaction_index DESC
		LIMIT ? OFFSET ?
	`, chainID, limit, offset)

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	txs := []Transaction{}
	for rows.Next() {
		var tx Transaction
		var hashBytes [32]byte
		var fromBytes [20]byte
		var toBytes []byte // Use slice for nullable FixedString
		var valueBig big.Int

		if err := rows.Scan(
			&tx.ChainID, &hashBytes, &tx.BlockNumber, &tx.BlockTime, &tx.TransactionIndex,
			&fromBytes, &toBytes, &valueBig, &tx.GasLimit, &tx.GasPrice, &tx.GasUsed, &tx.Success, &tx.Type,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		tx.Hash = "0x" + hex.EncodeToString(hashBytes[:])
		tx.From = "0x" + hex.EncodeToString(fromBytes[:])
		if len(toBytes) > 0 {
			to := "0x" + hex.EncodeToString(toBytes)
			tx.To = &to
		}
		tx.Value = valueBig.String()
		txs = append(txs, tx)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

func (s *Server) handleGetTx(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	hash := normalizeHash(r.PathValue("hash"))

	// Convert hex hash to bytes for query
	hashHex := hash[2:] // Remove 0x prefix
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil || len(hashBytes) != 32 {
		writeError(w, http.StatusBadRequest, "invalid transaction hash")
		return
	}

	var hashFixed [32]byte
	copy(hashFixed[:], hashBytes)

	var tx Transaction
	var dbHashBytes [32]byte
	var fromBytes [20]byte
	var toBytes []byte // Use slice for nullable FixedString
	var valueBig big.Int

	err = s.conn.QueryRow(ctx, `
		SELECT
			chain_id, hash, block_number, block_time, transaction_index,
			from, to, value, gas_limit, gas_price, gas_used, success, type
		FROM raw_txs
		WHERE chain_id = ? AND hash = ?
		LIMIT 1
	`, chainID, hashFixed).Scan(
		&tx.ChainID, &dbHashBytes, &tx.BlockNumber, &tx.BlockTime, &tx.TransactionIndex,
		&fromBytes, &toBytes, &valueBig, &tx.GasLimit, &tx.GasPrice, &tx.GasUsed, &tx.Success, &tx.Type,
	)

	if err != nil {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}

	tx.Hash = "0x" + hex.EncodeToString(dbHashBytes[:])
	tx.From = "0x" + hex.EncodeToString(fromBytes[:])
	if len(toBytes) > 0 {
		to := "0x" + hex.EncodeToString(toBytes)
		tx.To = &to
	}
	tx.Value = valueBig.String()

	writeJSON(w, http.StatusOK, Response{Data: tx})
}

func (s *Server) handleAddressTxs(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	address := normalizeHash(r.PathValue("address"))

	// Convert hex address to bytes
	addrHex := address[2:] // Remove 0x prefix
	addrBytes, err := hex.DecodeString(addrHex)
	if err != nil || len(addrBytes) != 20 {
		writeError(w, http.StatusBadRequest, "invalid address")
		return
	}

	var addrFixed [20]byte
	copy(addrFixed[:], addrBytes)

	rows, err := s.conn.Query(ctx, `
		SELECT
			chain_id, hash, block_number, block_time, transaction_index,
			from, to, value, gas_limit, gas_price, gas_used, success, type
		FROM raw_txs
		WHERE chain_id = ? AND (from = ? OR to = ?)
		ORDER BY block_number DESC, transaction_index DESC
		LIMIT ? OFFSET ?
	`, chainID, addrFixed, addrFixed, limit, offset)

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	txs := []Transaction{}
	for rows.Next() {
		var tx Transaction
		var hashBytes [32]byte
		var fromBytes [20]byte
		var toBytes []byte // Use slice for nullable FixedString
		var valueBig big.Int

		if err := rows.Scan(
			&tx.ChainID, &hashBytes, &tx.BlockNumber, &tx.BlockTime, &tx.TransactionIndex,
			&fromBytes, &toBytes, &valueBig, &tx.GasLimit, &tx.GasPrice, &tx.GasUsed, &tx.Success, &tx.Type,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		tx.Hash = "0x" + hex.EncodeToString(hashBytes[:])
		tx.From = "0x" + hex.EncodeToString(fromBytes[:])
		if len(toBytes) > 0 {
			to := "0x" + hex.EncodeToString(toBytes)
			tx.To = &to
		}
		tx.Value = valueBig.String()
		txs = append(txs, tx)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}
