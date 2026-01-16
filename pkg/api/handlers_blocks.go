package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// Block represents an EVM block
type Block struct {
	ChainID     uint32    `json:"chain_id" example:"43114"`
	BlockNumber uint32    `json:"block_number" example:"12345678"`
	Hash        string    `json:"hash" example:"0x1234..."`
	ParentHash  string    `json:"parent_hash" example:"0xabcd..."`
	BlockTime   time.Time `json:"block_time"`
	Miner       string    `json:"miner" example:"0x742d35Cc6634C0532925a3b844Bc9e7595f..."`
	Size        uint32    `json:"size" example:"1024"`
	GasLimit    uint32    `json:"gas_limit" example:"8000000"`
	GasUsed     uint32    `json:"gas_used" example:"500000"`
	BaseFee     uint64    `json:"base_fee_per_gas" example:"25000000000"`
	TxCount     uint32    `json:"tx_count,omitempty" example:"150"`
}

// handleListBlocks returns a paginated list of blocks
// @Summary List blocks
// @Description Get a paginated list of blocks for a specific chain
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g., 43114 for C-Chain)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]Block,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/blocks [get]
func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	rows, err := s.conn.Query(ctx, `
		SELECT
			chain_id, block_number, hash, parent_hash, block_time,
			miner, size, gas_limit, gas_used, base_fee_per_gas
		FROM raw_blocks
		WHERE chain_id = ?
		ORDER BY block_number DESC
		LIMIT ? OFFSET ?
	`, chainID, limit, offset)

	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	var blocks []Block
	for rows.Next() {
		var b Block
		var hashBytes, parentHashBytes [32]byte
		var minerAddr [20]byte

		if err := rows.Scan(
			&b.ChainID, &b.BlockNumber, &hashBytes, &parentHashBytes, &b.BlockTime,
			&minerAddr, &b.Size, &b.GasLimit, &b.GasUsed, &b.BaseFee,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		b.Hash = "0x" + hex.EncodeToString(hashBytes[:])
		b.ParentHash = "0x" + hex.EncodeToString(parentHashBytes[:])
		b.Miner = "0x" + hex.EncodeToString(minerAddr[:])
		blocks = append(blocks, b)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: blocks,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

// handleGetBlock returns a single block by number
// @Summary Get block by number
// @Description Get details for a specific block including transaction count
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param number path int true "Block number"
// @Success 200 {object} Response{data=Block}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/blocks/{number} [get]
func (s *Server) handleGetBlock(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	numberStr := r.PathValue("number")
	blockNumber, err := strconv.ParseUint(numberStr, 10, 32)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid block number")
		return
	}

	var b Block
	var hashBytes, parentHashBytes [32]byte
	var minerAddr [20]byte

	err = s.conn.QueryRow(ctx, `
		SELECT
			chain_id, block_number, hash, parent_hash, block_time,
			miner, size, gas_limit, gas_used, base_fee_per_gas
		FROM raw_blocks
		WHERE chain_id = ? AND block_number = ?
	`, chainID, blockNumber).Scan(
		&b.ChainID, &b.BlockNumber, &hashBytes, &parentHashBytes, &b.BlockTime,
		&minerAddr, &b.Size, &b.GasLimit, &b.GasUsed, &b.BaseFee,
	)

	if err != nil {
		writeNotFoundError(w, "Block")
		return
	}

	b.Hash = "0x" + hex.EncodeToString(hashBytes[:])
	b.ParentHash = "0x" + hex.EncodeToString(parentHashBytes[:])
	b.Miner = "0x" + hex.EncodeToString(minerAddr[:])

	// Get transaction count
	var txCount uint64
	s.conn.QueryRow(ctx, `
		SELECT count() FROM raw_txs WHERE chain_id = ? AND block_number = ?
	`, chainID, blockNumber).Scan(&txCount)
	b.TxCount = uint32(txCount)

	writeJSON(w, http.StatusOK, Response{Data: b})
}
