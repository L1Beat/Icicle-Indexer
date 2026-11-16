package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// handleGenesis returns the mainnet genesis
// @Summary Network genesis
// @Description The Avalanche mainnet genesis, from the config every node boots from: the genesis message, start time, the P-Chain genesis block ID, the X-Chain and C-Chain IDs, the AVAX asset ID, the initial supply split by where it was created, when the locked supply unlocked, the 75 initial validators and the C-Chain genesis. Amounts are nAVAX strings. Individual allocations are at /api/v1/data/genesis/allocations.
// @Tags Data - Genesis
// @Produce json
// @Success 200 {object} Response{data=GenesisOverview}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/genesis [get]
func (s *Server) handleGenesis(w http.ResponseWriter, r *http.Request) {
	g, err := mainnetGenesis()
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: g.overview})
}

// handleGenesisAllocations returns the genesis allocations
// @Summary Genesis allocations
// @Description The 5,390 mainnet genesis allocations in genesis order. x_chain_amount was spendable on the X-Chain at genesis; unlock_schedule tranches were locked on the P-Chain until unlock_time, or staked by the genesis validators when initially_staked is true. Amounts are nAVAX strings.
// @Tags Data - Genesis
// @Produce json
// @Param address query string false "AVAX address (avax1..., X-avax1... or P-avax1...) or 0x ETH address"
// @Param limit query int false "Number of results (max 1000)" default(100)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]GenesisAllocation,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/genesis/allocations [get]
func (s *Server) handleGenesisAllocations(w http.ResponseWriter, r *http.Request) {
	g, err := mainnetGenesis()
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	q := r.URL.Query()

	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "limit must be 1-1000")
			return
		}
		limit = n
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "offset must be 0 or more")
			return
		}
		offset = n
	}

	all := g.allocations
	if addr := q.Get("address"); addr != "" {
		var hits []GenesisAllocation
		for _, a := range all {
			if a.matches(addr) {
				hits = append(hits, a)
			}
		}
		all = hits
	}

	page := []GenesisAllocation{}
	if offset < len(all) {
		end := min(offset+limit, len(all))
		page = all[offset:end]
	}
	writeJSON(w, http.StatusOK, Response{
		Data: page,
		Meta: &Meta{Total: int64(len(all)), Limit: limit, Offset: offset, HasMore: offset+limit < len(all)},
	})
}

// GenesisEVMBlock is C-Chain block 0. Its header timestamp is 0, so
// block_time is null rather than 1970.
type GenesisEVMBlock struct {
	ChainID     uint32     `json:"chain_id"`
	BlockNumber uint32     `json:"block_number"`
	Hash        string     `json:"hash"`
	ParentHash  string     `json:"parent_hash"`
	BlockTime   *time.Time `json:"block_time"`
	Miner       string     `json:"miner"`
	GasLimit    uint64     `json:"gas_limit"`
	GasUsed     uint32     `json:"gas_used"`
	BaseFee     uint64     `json:"base_fee_per_gas"`
	TxCount     uint32     `json:"tx_count"`
	Genesis     string     `json:"genesis"` // where the full genesis is served
}

const (
	genesisPath   = "/api/v1/data/genesis"
	zeroHash32Hex = "0x0000000000000000000000000000000000000000000000000000000000000000"
	zeroAddr20Hex = "0x0000000000000000000000000000000000000000"
)

// writeCChainGenesisBlock serves C-Chain block 0. The genesis hash is not in
// the genesis config (it needs the state root), so it is read from block 1's
// parent hash.
func (s *Server) writeCChainGenesisBlock(w http.ResponseWriter, r *http.Request) {
	g, err := mainnetGenesis()
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	var parent [32]byte
	if err := s.conn.QueryRow(r.Context(), `
		SELECT parent_hash FROM raw_blocks WHERE chain_id = ? AND block_number = 1
	`, cChainID).Scan(&parent); err != nil {
		writeNotFoundError(w, "Block")
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: GenesisEVMBlock{
		ChainID:    cChainID,
		Hash:       "0x" + hex.EncodeToString(parent[:]),
		ParentHash: zeroHash32Hex,
		Miner:      zeroAddr20Hex,
		GasLimit:   g.overview.CChainGenesisGasLimit,
		Genesis:    genesisPath,
	}})
}

// pChainGenesisBlock is P-Chain block 0, built from the genesis config.
func pChainGenesisBlock() (PChainBlock, error) {
	g, err := mainnetGenesis()
	if err != nil {
		return PChainBlock{}, err
	}
	return PChainBlock{
		BlockNumber: 0,
		BlockTime:   g.overview.StartTime,
		BlockHash:   g.overview.PChainGenesisBlockID,
		BlockType:   "Genesis",
	}, nil
}
