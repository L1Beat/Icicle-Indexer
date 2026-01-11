package api

import (
	"net/http"
	"time"
)

type EVMChainStatus struct {
	ChainID      uint32    `json:"chain_id"`
	Name         string    `json:"name"`
	CurrentBlock uint64    `json:"current_block"`
	LatestBlock  uint64    `json:"latest_block"`
	BlocksBehind int64     `json:"blocks_behind"`
	LastSync     time.Time `json:"last_sync"`
	IsSynced     bool      `json:"is_synced"`
}

type PChainStatus struct {
	CurrentBlock uint64    `json:"current_block"`
	LatestBlock  uint64    `json:"latest_block"`
	BlocksBehind int64     `json:"blocks_behind"`
	LastSync     time.Time `json:"last_sync"`
	IsSynced     bool      `json:"is_synced"`
}

type IndexerStatus struct {
	Healthy    bool             `json:"healthy"`
	EVM        []EVMChainStatus `json:"evm"`
	PChain     *PChainStatus    `json:"pchain,omitempty"`
	LastUpdate time.Time        `json:"last_update"`
}

func (s *Server) handleIndexerStatus(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	status := IndexerStatus{
		Healthy:    true,
		EVM:        []EVMChainStatus{},
		LastUpdate: time.Now().UTC(),
	}

	// Use same query as frontend - JOIN chain_status with sync_watermark
	evmQuery := `
		SELECT
			cs.chain_id,
			cs.name,
			cs.last_block_on_chain,
			cs.last_updated,
			sw.block_number as watermark_block
		FROM chain_status cs FINAL
		LEFT JOIN sync_watermark sw ON cs.chain_id = sw.chain_id
		WHERE cs.chain_id > 0
		ORDER BY cs.chain_id
	`
	evmRows, err := s.conn.Query(ctx, evmQuery)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query chain status")
		return
	}
	defer evmRows.Close()

	for evmRows.Next() {
		var cs EVMChainStatus
		var lastUpdated time.Time
		var watermarkBlock *uint32
		if err := evmRows.Scan(&cs.ChainID, &cs.Name, &cs.LatestBlock, &lastUpdated, &watermarkBlock); err != nil {
			continue
		}
		cs.LastSync = lastUpdated
		if watermarkBlock != nil {
			cs.CurrentBlock = uint64(*watermarkBlock)
		}

		// Calculate blocks behind
		cs.BlocksBehind = int64(cs.LatestBlock) - int64(cs.CurrentBlock)
		if cs.BlocksBehind < 0 {
			cs.BlocksBehind = 0
		}
		cs.IsSynced = cs.BlocksBehind < 10

		status.EVM = append(status.EVM, cs)
		// If any chain is significantly behind, mark as unhealthy
		if cs.BlocksBehind > 100 {
			status.Healthy = false
		}
	}

	// Get P-Chain status
	pchainQuery := `
		SELECT
			MAX(block_number) as current_block,
			MAX(block_time) as last_sync
		FROM p_chain_txs FINAL
		WHERE p_chain_id = 0
	`
	var pCurrentBlock uint64
	var pLastSync time.Time
	row := s.conn.QueryRow(ctx, pchainQuery)
	if err := row.Scan(&pCurrentBlock, &pLastSync); err == nil && pCurrentBlock > 0 {
		// Get latest P-Chain block from chain_status if available
		var pLatestBlock uint64
		pStatusQuery := `SELECT last_block_on_chain FROM chain_status FINAL WHERE chain_id = 0`
		pRow := s.conn.QueryRow(ctx, pStatusQuery)
		pRow.Scan(&pLatestBlock)

		pchain := &PChainStatus{
			CurrentBlock: pCurrentBlock,
			LatestBlock:  pLatestBlock,
			BlocksBehind: int64(pLatestBlock) - int64(pCurrentBlock),
			LastSync:     pLastSync,
			IsSynced:     true,
		}
		if pchain.BlocksBehind < 0 {
			pchain.BlocksBehind = 0
		}
		if pchain.BlocksBehind > 100 {
			pchain.IsSynced = false
			status.Healthy = false
		}
		status.PChain = pchain
	}

	writeJSON(w, http.StatusOK, status)
}
