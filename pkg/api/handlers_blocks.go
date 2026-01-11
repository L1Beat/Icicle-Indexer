package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

type Block struct {
	ChainID     uint32    `json:"chain_id"`
	BlockNumber uint32    `json:"block_number"`
	Hash        string    `json:"hash"`
	ParentHash  string    `json:"parent_hash"`
	BlockTime   time.Time `json:"block_time"`
	Miner       string    `json:"miner"`
	Size        uint32    `json:"size"`
	GasLimit    uint32    `json:"gas_limit"`
	GasUsed     uint32    `json:"gas_used"`
	BaseFee     uint64    `json:"base_fee_per_gas"`
	TxCount     uint32    `json:"tx_count,omitempty"`
}

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
			writeError(w, http.StatusInternalServerError, err.Error())
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

func (s *Server) handleGetBlock(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	numberStr := r.PathValue("number")
	blockNumber, err := strconv.ParseUint(numberStr, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid block number")
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
		writeError(w, http.StatusNotFound, "block not found")
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
