package api

import (
	"encoding/hex"
	"net/http"
	"strings"
)

// TokenBalance represents an ERC-20 token balance for a wallet
type TokenBalance struct {
	Token            string `json:"token" example:"0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"`
	Balance          string `json:"balance" example:"1000000"`
	TotalIn          string `json:"total_in" example:"2000000"`
	TotalOut         string `json:"total_out" example:"1000000"`
	LastUpdatedBlock uint64 `json:"last_updated_block" example:"77048918"`
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
	query := `
		SELECT
			token,
			toString(balance) as balance_str,
			toString(total_in) as total_in,
			toString(total_out) as total_out,
			last_updated_block
		FROM erc20_balances FINAL
		WHERE chain_id = ?
		  AND wallet = unhex(?)
		  AND balance > toInt256(0)
		ORDER BY balance DESC
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
		var balance, totalIn, totalOut string
		var lastBlock uint32

		if err := rows.Scan(&tokenBytes, &balance, &totalIn, &totalOut, &lastBlock); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		balances = append(balances, TokenBalance{
			Token:            "0x" + hex.EncodeToString(tokenBytes),
			Balance:          balance,
			TotalIn:          totalIn,
			TotalOut:         totalOut,
			LastUpdatedBlock: uint64(lastBlock),
		})
	}

	writeJSON(w, http.StatusOK, Response{
		Data: balances,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}
