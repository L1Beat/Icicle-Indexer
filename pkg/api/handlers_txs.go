package api

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
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

// InternalTransaction represents an internal call trace
type InternalTransaction struct {
	TxHash      string    `json:"tx_hash" example:"0x1234..."`
	BlockNumber uint32    `json:"block_number" example:"12345678"`
	BlockTime   time.Time `json:"block_time"`
	TraceIndex  string    `json:"trace_index" example:"0,1,2"` // Path in call tree
	From        string    `json:"from" example:"0x742d35Cc..."`
	To          *string   `json:"to" example:"0x123..."` // null for CREATE
	Value       string    `json:"value" example:"1000000000000000000"`
	GasUsed     uint32    `json:"gas_used" example:"21000"`
	CallType    string    `json:"call_type" example:"CALL"`
	Success     bool      `json:"success" example:"true"`
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
	if _, err := hex.DecodeString(addrHex); err != nil || len(addrHex) != 40 {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
		return
	}

	// Use unhex() in SQL for proper FixedString comparison
	rows, err := s.conn.Query(ctx, `
		SELECT
			chain_id, hash, block_number, block_time, transaction_index,
			from, to, value, gas_limit, gas_price, gas_used, success, type
		FROM raw_txs
		WHERE chain_id = ? AND (from = unhex(?) OR to = unhex(?))
		ORDER BY block_number DESC, transaction_index DESC
		LIMIT ? OFFSET ?
	`, chainID, addrHex, addrHex, limit, offset)

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

// handleAddressInternalTxs returns internal transactions for an address
// @Summary Get internal transactions by address
// @Description Get internal transactions (traces) involving a specific address
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param address path string true "Wallet address (0x-prefixed)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]InternalTransaction,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/address/{address}/internal-txs [get]
func (s *Server) handleAddressInternalTxs(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	address := normalizeHash(r.PathValue("address"))
	addrHex := address[2:] // Remove 0x prefix

	if _, err := hex.DecodeString(addrHex); err != nil || len(addrHex) != 40 {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
		return
	}

	// Query internal transactions where address is sender or receiver
	// Only include traces with value > 0 or CREATE types for meaningful results
	rows, err := s.conn.Query(ctx, `
		SELECT
			tx_hash, block_number, block_time, trace_address,
			from, to, value, gas_used, call_type, tx_success
		FROM raw_traces
		WHERE chain_id = ?
		  AND (from = unhex(?) OR to = unhex(?))
		  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
		ORDER BY block_number DESC, transaction_index DESC
		LIMIT ? OFFSET ?
	`, chainID, addrHex, addrHex, limit, offset)

	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	internalTxs := []InternalTransaction{}
	for rows.Next() {
		var itx InternalTransaction
		var txHashBytes [32]byte
		var fromBytes [20]byte
		var toBytes []byte
		var valueBig big.Int
		var traceAddr []uint16

		if err := rows.Scan(
			&txHashBytes, &itx.BlockNumber, &itx.BlockTime, &traceAddr,
			&fromBytes, &toBytes, &valueBig, &itx.GasUsed, &itx.CallType, &itx.Success,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		itx.TxHash = "0x" + hex.EncodeToString(txHashBytes[:])
		itx.From = "0x" + hex.EncodeToString(fromBytes[:])
		if len(toBytes) > 0 {
			to := "0x" + hex.EncodeToString(toBytes)
			itx.To = &to
		}
		itx.Value = valueBig.String()

		// Convert trace address array to string like "0,1,2"
		traceStrs := make([]string, len(traceAddr))
		for i, v := range traceAddr {
			traceStrs[i] = fmt.Sprintf("%d", v)
		}
		itx.TraceIndex = strings.Join(traceStrs, ",")

		internalTxs = append(internalTxs, itx)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: internalTxs,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}
