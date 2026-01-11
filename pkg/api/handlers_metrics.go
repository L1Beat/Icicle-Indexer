package api

import (
	"net/http"
	"time"
)

type FeeStats struct {
	SubnetID        string `json:"subnet_id"`
	TotalDeposited  uint64 `json:"total_deposited"`
	InitialDeposits uint64 `json:"initial_deposits"`
	TopUpDeposits   uint64 `json:"top_up_deposits"`
	TotalRefunded   uint64 `json:"total_refunded"`
	CurrentBalance  uint64 `json:"current_balance"`
	TotalFeesPaid   uint64 `json:"total_fees_paid"`
	DepositTxCount  uint32 `json:"deposit_tx_count"`
	ValidatorCount  uint32 `json:"validator_count"`
}

type ChainMetrics struct {
	ChainID        uint32    `json:"chain_id"`
	ChainName      string    `json:"chain_name"`
	LatestBlock    uint64    `json:"latest_block"`
	TotalBlocks    uint64    `json:"total_blocks"`
	TotalTxs       uint64    `json:"total_txs"`
	LastBlockTime  time.Time `json:"last_block_time"`
	AvgBlockTime   float64   `json:"avg_block_time_seconds"`
	AvgGasUsed     uint64    `json:"avg_gas_used"`
	TotalGasUsed   uint64    `json:"total_gas_used"`
}

func (s *Server) handleFeeMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	subnetID := r.URL.Query().Get("subnet_id")

	var query string
	var args []interface{}

	if subnetID != "" {
		query = `
			SELECT
				subnet_id, total_deposited, initial_deposits, top_up_deposits,
				total_refunded, current_balance, total_fees_paid,
				deposit_tx_count, validator_count
			FROM l1_fee_stats FINAL
			WHERE subnet_id = ?
		`
		args = []interface{}{subnetID}
	} else {
		query = `
			SELECT
				subnet_id, total_deposited, initial_deposits, top_up_deposits,
				total_refunded, current_balance, total_fees_paid,
				deposit_tx_count, validator_count
			FROM l1_fee_stats FINAL
			ORDER BY total_fees_paid DESC
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

	stats := []FeeStats{}
	for rows.Next() {
		var f FeeStats
		if err := rows.Scan(
			&f.SubnetID, &f.TotalDeposited, &f.InitialDeposits, &f.TopUpDeposits,
			&f.TotalRefunded, &f.CurrentBalance, &f.TotalFeesPaid,
			&f.DepositTxCount, &f.ValidatorCount,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		stats = append(stats, f)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: stats,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

func (s *Server) handleChainMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	var m ChainMetrics
	m.ChainID = chainID

	// Get chain name from chain_status
	s.conn.QueryRow(ctx, `
		SELECT name FROM chain_status FINAL WHERE chain_id = ?
	`, chainID).Scan(&m.ChainName)

	// Get block stats
	err = s.conn.QueryRow(ctx, `
		SELECT
			max(block_number) as latest_block,
			count() as total_blocks,
			max(block_time) as last_block_time,
			avg(gas_used) as avg_gas_used,
			sum(gas_used) as total_gas_used
		FROM raw_blocks
		WHERE chain_id = ?
	`, chainID).Scan(&m.LatestBlock, &m.TotalBlocks, &m.LastBlockTime, &m.AvgGasUsed, &m.TotalGasUsed)

	if err != nil {
		writeError(w, http.StatusNotFound, "chain not found")
		return
	}

	// Get transaction count
	s.conn.QueryRow(ctx, `
		SELECT count() FROM raw_txs WHERE chain_id = ?
	`, chainID).Scan(&m.TotalTxs)

	// Calculate average block time (last 1000 blocks)
	var avgBlockTimeSec float64
	s.conn.QueryRow(ctx, `
		SELECT
			(max(block_time) - min(block_time)) / count() as avg_block_time
		FROM (
			SELECT block_time
			FROM raw_blocks
			WHERE chain_id = ?
			ORDER BY block_number DESC
			LIMIT 1000
		)
	`, chainID).Scan(&avgBlockTimeSec)
	m.AvgBlockTime = avgBlockTimeSec

	writeJSON(w, http.StatusOK, Response{Data: m})
}
