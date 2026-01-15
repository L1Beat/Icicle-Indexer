package api

import (
	"encoding/hex"
	"math/big"
	"net/http"
	"time"
)

// Transaction represents an EVM transaction
type Transaction struct {
	ChainID          uint32    `json:"chain_id" example:"43114"`
	Hash             string    `json:"hash" example:"0x1234..."`
	BlockNumber      uint32    `json:"block_number" example:"12345678"`
	BlockTime        time.Time `json:"block_time"`
	TransactionIndex uint16    `json:"transaction_index" example:"0"`
	From             string    `json:"from" example:"0x742d35Cc6634C0532925a3b844Bc9e7595f..."`
	To               *string   `json:"to" example:"0x123..."` // null for contract creation
	Value            string    `json:"value" example:"1000000000000000000"`
	GasLimit         uint32    `json:"gas_limit" example:"21000"`
	GasPrice         uint64    `json:"gas_price" example:"25000000000"`
	GasUsed          uint32    `json:"gas_used" example:"21000"`
	Success          bool      `json:"success" example:"true"`
	Type             uint8     `json:"type" example:"2"`
}

// handleListTxs returns a paginated list of transactions
// @Summary List transactions
// @Description Get a paginated list of transactions for a specific chain
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]Transaction,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/txs [get]
func (s *Server) handleListTxs(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
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
		writeInternalError(w, err.Error())
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
			writeInternalError(w, err.Error())
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

// handleGetTx returns a single transaction by hash
// @Summary Get transaction by hash
// @Description Get details for a specific transaction
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param hash path string true "Transaction hash (0x-prefixed)"
// @Success 200 {object} Response{data=Transaction}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/txs/{hash} [get]
func (s *Server) handleGetTx(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	hash := normalizeHash(r.PathValue("hash"))

	// Convert hex hash to bytes for query
	hashHex := hash[2:] // Remove 0x prefix
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil || len(hashBytes) != 32 {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid transaction hash")
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
		writeNotFoundError(w, "Transaction")
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

// handleAddressTxs returns transactions for an address
// @Summary Get transactions by address
// @Description Get transactions sent from or to a specific address
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param address path string true "Wallet address (0x-prefixed)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]Transaction,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/address/{address}/txs [get]
func (s *Server) handleAddressTxs(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	address := normalizeHash(r.PathValue("address"))

	// Convert hex address to bytes
	addrHex := address[2:] // Remove 0x prefix
	addrBytes, err := hex.DecodeString(addrHex)
	if err != nil || len(addrBytes) != 20 {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
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
		writeInternalError(w, err.Error())
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
			writeInternalError(w, err.Error())
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
