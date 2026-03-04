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
	TxHash      string    `json:"tx_hash,omitempty" example:"0x1234..."`
	BlockNumber uint32    `json:"block_number,omitempty" example:"12345678"`
	BlockTime   time.Time `json:"block_time,omitempty"`
	TraceIndex  string    `json:"trace_index" example:"0,1,2"` // Path in call tree
	From        string    `json:"from" example:"0x742d35Cc..."`
	To          *string   `json:"to" example:"0x123..."` // null for CREATE
	Value       string    `json:"value" example:"1000000000000000000"`
	GasUsed     uint32    `json:"gas_used" example:"21000"`
	CallType    string    `json:"call_type" example:"CALL"`
	Success     bool      `json:"success" example:"true"`
}

// TokenTransfer represents an ERC-20 token transfer within a transaction
type TokenTransfer struct {
	Token    string  `json:"token" example:"0xb97ef9ef..."`
	Name     *string `json:"name,omitempty" example:"USD Coin"`
	Symbol   *string `json:"symbol,omitempty" example:"USDC"`
	Decimals *uint8  `json:"decimals,omitempty" example:"6"`
	From     string  `json:"from" example:"0x742d35Cc..."`
	To       string  `json:"to" example:"0x123..."`
	Value    string  `json:"value" example:"1000000"`
	LogIndex uint32  `json:"log_index" example:"5"`
}

// TokenApproval represents an ERC-20 approval event within a transaction
type TokenApproval struct {
	Token       string  `json:"token" example:"0xb97ef9ef..."`
	Name        *string `json:"name,omitempty" example:"USD Coin"`
	Symbol      *string `json:"symbol,omitempty" example:"USDC"`
	Decimals    *uint8  `json:"decimals,omitempty" example:"6"`
	Owner       string  `json:"owner" example:"0x742d35Cc..."`
	Spender     string  `json:"spender" example:"0x123..."`
	Amount      string  `json:"amount" example:"1000000"`
	IsUnlimited bool    `json:"is_unlimited" example:"false"`
	LogIndex    uint32  `json:"log_index" example:"5"`
}

// TransactionDetail represents a full transaction with internal txs and token transfers
type TransactionDetail struct {
	Transaction
	InternalTxs    []InternalTransaction `json:"internal_txs"`
	TokenTransfers []TokenTransfer       `json:"token_transfers"`
	Approvals      []TokenApproval       `json:"approvals"`
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
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	fetchLimit := limit + 1

	var query string
	var args []interface{}
	if cursor != nil && cursor.HasTxIndex {
		query = `
			SELECT
				chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type
			FROM raw_txs
			WHERE chain_id = ? AND (block_number, transaction_index) < (?, ?)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ?
		`
		args = []interface{}{chainID, cursor.BlockNumber, cursor.TxIndex, fetchLimit}
	} else {
		query = `
			SELECT
				chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type
			FROM raw_txs
			WHERE chain_id = ?
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{chainID, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
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
		var toBytes []byte
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

	txs, hasMore := trimResults(txs, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(txs) > 0 {
		last := txs[len(txs)-1]
		meta.NextCursor = cursorBlockTx(last.BlockNumber, last.TransactionIndex)
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `SELECT count() FROM raw_txs WHERE chain_id = ?`, chainID).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: meta,
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
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	hashHex, err := validateTxHash(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid transaction hash")
		return
	}

	var tx Transaction
	var dbHashBytes [32]byte
	var fromBytes [20]byte
	var toBytes []byte // Use slice for nullable FixedString
	var valueBig big.Int

	// Use unhex() in SQL for proper FixedString comparison
	err = s.conn.QueryRow(ctx, `
		SELECT
			chain_id, hash, block_number, block_time, transaction_index,
			from, to, value, gas_limit, gas_price, gas_used, success, type
		FROM raw_txs
		WHERE chain_id = ? AND hash = unhex(?)
		LIMIT 1
	`, chainID, hashHex).Scan(
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

	// Build detailed response
	detail := TransactionDetail{
		Transaction:    tx,
		InternalTxs:    []InternalTransaction{},
		TokenTransfers: []TokenTransfer{},
		Approvals:      []TokenApproval{},
	}

	// Fetch internal transactions (traces with value > 0, excluding top-level)
	traceRows, err := s.conn.Query(ctx, `
		SELECT trace_address, from, to, value, gas_used, call_type, tx_success
		FROM raw_traces
		WHERE chain_id = ? AND tx_hash = unhex(?)
		  AND value > 0
		  AND length(trace_address) > 0
		ORDER BY trace_address
	`, chainID, hashHex)
	if err == nil {
		defer traceRows.Close()
		for traceRows.Next() {
			var itx InternalTransaction
			var fromBytes [20]byte
			var toBytes []byte
			var valueBig big.Int
			var traceAddr []uint16

			if err := traceRows.Scan(&traceAddr, &fromBytes, &toBytes, &valueBig, &itx.GasUsed, &itx.CallType, &itx.Success); err == nil {
				itx.From = "0x" + hex.EncodeToString(fromBytes[:])
				if len(toBytes) > 0 {
					to := "0x" + hex.EncodeToString(toBytes)
					itx.To = &to
				}
				itx.Value = valueBig.String()

				traceStrs := make([]string, len(traceAddr))
				for i, v := range traceAddr {
					traceStrs[i] = fmt.Sprintf("%d", v)
				}
				itx.TraceIndex = strings.Join(traceStrs, ",")

				detail.InternalTxs = append(detail.InternalTxs, itx)
			}
		}
	}

	// Fetch ERC-20 token transfers (Transfer events)
	// Transfer(address indexed from, address indexed to, uint256 value)
	// topic0 = keccak256("Transfer(address,address,uint256)")
	// topic1 = from address (padded to 32 bytes)
	// topic2 = to address (padded to 32 bytes)
	// data = value (32 bytes for standard ERC-20, but check >= 32 for safety)
	transferRows, err := s.conn.Query(ctx, `
		SELECT
			l.address,
			COALESCE(tm.name, '') as name,
			COALESCE(tm.symbol, '') as symbol,
			COALESCE(tm.decimals, 0) as decimals,
			substring(l.topic1, 13, 20) as from_addr,
			substring(l.topic2, 13, 20) as to_addr,
			reinterpretAsUInt256(reverse(substring(l.data, 1, 32))) as value,
			l.log_index
		FROM raw_logs l
		LEFT JOIN token_metadata tm FINAL ON l.chain_id = tm.chain_id AND l.address = tm.token
		WHERE l.chain_id = ?
		  AND l.transaction_hash = unhex(?)
		  AND l.topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
		  AND length(l.data) >= 32
		  AND l.topic1 IS NOT NULL
		  AND l.topic2 IS NOT NULL
		ORDER BY l.log_index
	`, chainID, hashHex)
	if err != nil {
		fmt.Printf("Error querying token transfers: %v\n", err)
	} else {
		defer transferRows.Close()
		for transferRows.Next() {
			var tt TokenTransfer
			var tokenBytes [20]byte
			var fromBytes []byte // substring returns String, use slice
			var toBytes []byte   // substring returns String, use slice
			var name, symbol string
			var decimals uint8
			var valueBig big.Int

			if err := transferRows.Scan(&tokenBytes, &name, &symbol, &decimals, &fromBytes, &toBytes, &valueBig, &tt.LogIndex); err != nil {
				fmt.Printf("Error scanning token transfer: %v\n", err)
				continue
			}
			tt.Token = "0x" + hex.EncodeToString(tokenBytes[:])
			tt.From = "0x" + hex.EncodeToString(fromBytes)
			tt.To = "0x" + hex.EncodeToString(toBytes)
			tt.Value = valueBig.String()

			if name != "" {
				tt.Name = &name
			}
			if symbol != "" {
				tt.Symbol = &symbol
			}
			if decimals > 0 || name != "" || symbol != "" {
				tt.Decimals = &decimals
			}

			detail.TokenTransfers = append(detail.TokenTransfers, tt)
		}
	}

	// Fetch ERC-20 token approvals (Approval events)
	// Approval(address indexed owner, address indexed spender, uint256 value)
	// topic0 = keccak256("Approval(address,address,uint256)")
	// topic1 = owner address (padded to 32 bytes)
	// topic2 = spender address (padded to 32 bytes)
	// data = value (32 bytes)
	approvalRows, err := s.conn.Query(ctx, `
		SELECT
			l.address,
			COALESCE(tm.name, '') as name,
			COALESCE(tm.symbol, '') as symbol,
			COALESCE(tm.decimals, 0) as decimals,
			substring(l.topic1, 13, 20) as owner_addr,
			substring(l.topic2, 13, 20) as spender_addr,
			reinterpretAsUInt256(reverse(substring(l.data, 1, 32))) as value,
			l.log_index
		FROM raw_logs l
		LEFT JOIN token_metadata tm FINAL ON l.chain_id = tm.chain_id AND l.address = tm.token
		WHERE l.chain_id = ?
		  AND l.transaction_hash = unhex(?)
		  AND l.topic0 = unhex('8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925')
		  AND length(l.data) >= 32
		  AND l.topic1 IS NOT NULL
		  AND l.topic2 IS NOT NULL
		ORDER BY l.log_index
	`, chainID, hashHex)
	if err != nil {
		fmt.Printf("Error querying token approvals: %v\n", err)
	} else {
		defer approvalRows.Close()

		// MAX_UINT256 = 2^256 - 1 (indicates unlimited approval)
		maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

		for approvalRows.Next() {
			var ta TokenApproval
			var tokenBytes [20]byte
			var ownerBytes []byte
			var spenderBytes []byte
			var name, symbol string
			var decimals uint8
			var valueBig big.Int

			if err := approvalRows.Scan(&tokenBytes, &name, &symbol, &decimals, &ownerBytes, &spenderBytes, &valueBig, &ta.LogIndex); err != nil {
				fmt.Printf("Error scanning token approval: %v\n", err)
				continue
			}
			ta.Token = "0x" + hex.EncodeToString(tokenBytes[:])
			ta.Owner = "0x" + hex.EncodeToString(ownerBytes)
			ta.Spender = "0x" + hex.EncodeToString(spenderBytes)
			ta.Amount = valueBig.String()
			ta.IsUnlimited = valueBig.Cmp(maxUint256) == 0

			if name != "" {
				ta.Name = &name
			}
			if symbol != "" {
				ta.Symbol = &symbol
			}
			if decimals > 0 || name != "" || symbol != "" {
				ta.Decimals = &decimals
			}

			detail.Approvals = append(detail.Approvals, ta)
		}
	}

	writeJSON(w, http.StatusOK, Response{Data: detail})
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
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	addrHex, err := validateAddress(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
		return
	}

	fetchLimit := limit + 1

	var query string
	var args []interface{}

	if cursor != nil && cursor.HasTxIndex {
		// Cursor-based: no offset needed, more efficient UNION ALL
		query = `
			SELECT chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type
			FROM (
				SELECT chain_id, hash, block_number, block_time, transaction_index,
					from, to, value, gas_limit, gas_price, gas_used, success, type
				FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT chain_id, hash, block_number, block_time, transaction_index,
					from, to, value, gas_limit, gas_price, gas_used, success, type
				FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ?
		`
		args = []interface{}{
			chainID, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			chainID, addrHex, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			fetchLimit,
		}
	} else {
		// Offset-based
		innerLimit := fetchLimit + offset
		query = `
			SELECT chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type
			FROM (
				SELECT chain_id, hash, block_number, block_time, transaction_index,
					from, to, value, gas_limit, gas_price, gas_used, success, type
				FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT chain_id, hash, block_number, block_time, transaction_index,
					from, to, value, gas_limit, gas_price, gas_used, success, type
				FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{chainID, addrHex, innerLimit, chainID, addrHex, addrHex, innerLimit, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
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
		var toBytes []byte
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

	txs, hasMore := trimResults(txs, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(txs) > 0 {
		last := txs[len(txs)-1]
		meta.NextCursor = cursorBlockTx(last.BlockNumber, last.TransactionIndex)
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `
			SELECT sum(cnt) FROM (
				SELECT count() as cnt FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
				UNION ALL
				SELECT count() as cnt FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
			)
		`, chainID, addrHex, chainID, addrHex, addrHex).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: txs,
		Meta: meta,
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
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	addrHex, err := validateAddress(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
		return
	}

	fetchLimit := limit + 1

	var query string
	var args []interface{}

	if cursor != nil && cursor.HasTxIndex {
		query = `
			SELECT tx_hash, block_number, block_time, trace_address,
				from, to, value, gas_used, call_type, tx_success
			FROM (
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND from = unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ?
		`
		args = []interface{}{
			chainID, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			chainID, addrHex, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			fetchLimit,
		}
	} else {
		innerLimit := fetchLimit + offset
		query = `
			SELECT tx_hash, block_number, block_time, trace_address,
				from, to, value, gas_used, call_type, tx_success
			FROM (
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND from = unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{chainID, addrHex, innerLimit, chainID, addrHex, addrHex, innerLimit, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
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

		traceStrs := make([]string, len(traceAddr))
		for i, v := range traceAddr {
			traceStrs[i] = fmt.Sprintf("%d", v)
		}
		itx.TraceIndex = strings.Join(traceStrs, ",")

		internalTxs = append(internalTxs, itx)
	}

	internalTxs, hasMore := trimResults(internalTxs, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(internalTxs) > 0 {
		last := internalTxs[len(internalTxs)-1]
		meta.NextCursor = cursorBlockTx(last.BlockNumber, 0) // traces don't expose tx_index in result
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `
			SELECT sum(cnt) FROM (
				SELECT count() as cnt FROM raw_traces WHERE chain_id = ? AND from = unhex(?) AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				UNION ALL
				SELECT count() as cnt FROM raw_traces WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?) AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
			)
		`, chainID, addrHex, chainID, addrHex, addrHex).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: internalTxs,
		Meta: meta,
	})
}
