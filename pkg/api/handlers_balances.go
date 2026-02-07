package api

import (
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// NativeBalance represents the native token balance for a wallet
type NativeBalance struct {
	TotalIn          string     `json:"total_in" example:"1000000000000000000"`
	TotalOut         string     `json:"total_out" example:"500000000000000000"`
	TotalGas         string     `json:"total_gas" example:"21000000000000"`
	Balance          string     `json:"balance" example:"499979000000000000"`
	LastUpdatedBlock uint64     `json:"last_updated_block" example:"77048918"`
	TxCount          uint64     `json:"tx_count" example:"788432"`
	FirstTxTime      *time.Time `json:"first_tx_time,omitempty"`
	LastTxTime       *time.Time `json:"last_tx_time,omitempty"`
}

// TokenBalance represents an ERC-20 token balance for a wallet
type TokenBalance struct {
	Token            string  `json:"token" example:"0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"`
	Name             *string `json:"name,omitempty" example:"USD Coin"`
	Symbol           *string `json:"symbol,omitempty" example:"USDC"`
	Decimals         *uint8  `json:"decimals,omitempty" example:"6"`
	Balance          string  `json:"balance" example:"1000000"`
	TotalIn          string  `json:"total_in" example:"2000000"`
	TotalOut         string  `json:"total_out" example:"1000000"`
	LastUpdatedBlock uint64  `json:"last_updated_block" example:"77048918"`
}

// handleAddressBalances returns ERC-20 token balances for an address
// @Summary Get ERC-20 token balances
// @Description Get all ERC-20 token balances for an address
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param address path string true "Wallet address (with or without 0x prefix)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]TokenBalance,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/address/{address}/balances [get]
func (s *Server) handleAddressBalances(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	// Get address from path and normalize
	address := r.PathValue("address")
	address = strings.TrimPrefix(strings.ToLower(address), "0x")

	// Validate address (should be 40 hex chars = 20 bytes)
	if len(address) != 40 {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address format")
		return
	}

	// Validate it's valid hex
	if _, err := hex.DecodeString(address); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address hex")
		return
	}

	limit, offset := getPagination(r)

	// Use unhex() in SQL to convert hex string to FixedString(20)
	// Cast Int256 results to String for Go compatibility
	// Left join with token_metadata to get name, symbol, decimals
	query := `
		SELECT
			b.token,
			tm.name,
			tm.symbol,
			tm.decimals,
			toString(b.balance) as balance_str,
			toString(b.total_in) as total_in,
			toString(b.total_out) as total_out,
			b.last_updated_block
		FROM erc20_balances b FINAL
		LEFT JOIN token_metadata tm FINAL ON b.chain_id = tm.chain_id AND b.token = tm.token
		WHERE b.chain_id = ?
		  AND b.wallet = unhex(?)
		  AND b.balance > toInt256(0)
		ORDER BY b.balance DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.conn.Query(ctx, query, chainID, address, limit, offset)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	balances := []TokenBalance{}
	for rows.Next() {
		var tokenBytes []byte
		var name, symbol string
		var decimals uint8
		var balance, totalIn, totalOut string
		var lastBlock uint32

		if err := rows.Scan(&tokenBytes, &name, &symbol, &decimals, &balance, &totalIn, &totalOut, &lastBlock); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		tb := TokenBalance{
			Token:            "0x" + hex.EncodeToString(tokenBytes),
			Balance:          balance,
			TotalIn:          totalIn,
			TotalOut:         totalOut,
			LastUpdatedBlock: uint64(lastBlock),
		}

		// Only include metadata if we have it
		if name != "" {
			tb.Name = &name
		}
		if symbol != "" {
			tb.Symbol = &symbol
		}
		if decimals > 0 || name != "" || symbol != "" {
			tb.Decimals = &decimals
		}

		balances = append(balances, tb)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: balances,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

// handleAddressNativeBalance returns native token balance for an address
// @Summary Get native token balance
// @Description Get native token balance (AVAX, ETH, etc.) for an address
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param address path string true "Wallet address (with or without 0x prefix)"
// @Success 200 {object} Response{data=NativeBalance}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/address/{address}/native [get]
func (s *Server) handleAddressNativeBalance(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	// Get address from path and normalize
	address := r.PathValue("address")
	address = strings.TrimPrefix(strings.ToLower(address), "0x")

	// Validate address (should be 40 hex chars = 20 bytes)
	if len(address) != 40 {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address format")
		return
	}

	// Validate it's valid hex
	if _, err := hex.DecodeString(address); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address hex")
		return
	}

	balanceQuery := `
		SELECT
			toString(total_in) as total_in,
			toString(total_out) as total_out,
			toString(total_gas) as total_gas,
			toString(balance) as balance,
			last_updated_block
		FROM native_balances FINAL
		WHERE chain_id = ?
		  AND wallet = unhex(?)
	`

	var totalIn, totalOut, totalGas, balance string
	var lastBlock uint32

	err = s.conn.QueryRow(ctx, balanceQuery, chainID, address).Scan(&totalIn, &totalOut, &totalGas, &balance, &lastBlock)
	if err != nil {
		totalIn = "0"
		totalOut = "0"
		totalGas = "0"
		balance = "0"
		lastBlock = 0
	}

	// Use UNION ALL instead of OR to allow ClickHouse to use bloom filter indexes on each column
	txStatsQuery := `
		SELECT
			sum(cnt) as tx_count,
			min(first_tx_time) as first_tx_time,
			max(last_tx_time) as last_tx_time
		FROM (
			SELECT count() as cnt,
				min(block_time) as first_tx_time,
				max(block_time) as last_tx_time
			FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
			UNION ALL
			SELECT count() as cnt,
				min(block_time) as first_tx_time,
				max(block_time) as last_tx_time
			FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
		)
	`

	var txCount uint64
	var firstTxTime, lastTxTime time.Time

	err = s.conn.QueryRow(ctx, txStatsQuery, chainID, address, chainID, address, address).Scan(&txCount, &firstTxTime, &lastTxTime)
	if err != nil {
		txCount = 0
	}

	result := NativeBalance{
		TotalIn:          totalIn,
		TotalOut:         totalOut,
		TotalGas:         totalGas,
		Balance:          balance,
		LastUpdatedBlock: uint64(lastBlock),
		TxCount:          txCount,
	}

	// Only set timestamps if there are transactions
	if txCount > 0 {
		result.FirstTxTime = &firstTxTime
		result.LastTxTime = &lastTxTime
	}

	writeJSON(w, http.StatusOK, Response{
		Data: result,
	})
}
