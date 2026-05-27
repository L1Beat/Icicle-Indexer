package api

import (
	"encoding/hex"
	"net/http"
)

// Stablecoin represents a curated stablecoin with current supply / holder / 24h volume stats.
//
// Supply is "circulating": balances held by addresses listed in stablecoin_excluded_holders
// (e.g. issuer treasuries) are subtracted, so the number matches DeFiLlama-style methodology
// rather than raw on-chain totalSupply.
type Stablecoin struct {
	Token        string `json:"token" example:"0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"`
	Symbol       string `json:"symbol" example:"USDC"`
	Name         string `json:"name" example:"USD Coin"`
	Decimals     uint8  `json:"decimals" example:"6"`
	Peg          string `json:"peg" example:"USD"`
	Issuer       string `json:"issuer,omitempty" example:"Circle"`
	Bridged      bool   `json:"bridged" example:"false"`
	Supply       string `json:"supply" example:"459300000000000"`
	Holders      uint64 `json:"holders" example:"128453"`
	Volume24h    string `json:"volume_24h" example:"32100000000000"`
	Transfers24h uint64 `json:"transfers_24h" example:"58210"`
}

// handleListStablecoins returns curated stablecoins for a chain with circulating supply,
// holder count, and last-24h transfer volume / count.
//
// @Summary List stablecoins for a chain
// @Description Returns curated stablecoins with circulating supply (excludes addresses listed in stablecoin_excluded_holders, e.g. issuer treasuries), holder count, and 24h transfer stats.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID (e.g. 43114 for Avalanche C-Chain)"
// @Success 200 {object} Response{data=[]Stablecoin}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/stablecoins [get]
func (s *Server) handleListStablecoins(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	query := `
		WITH stables AS (
			SELECT chain_id, token, symbol, name, decimals, peg, issuer, bridged
			FROM stablecoins FINAL
			WHERE chain_id = ?
		)
		SELECT
			s.token,
			s.symbol,
			s.name,
			s.decimals,
			s.peg,
			s.issuer,
			s.bridged,
			toString(b.supply_total) AS supply,
			b.holders AS holders,
			toString(v.volume_amount) AS volume_24h,
			v.transfers_count AS transfers_24h
		FROM stables s
		LEFT JOIN (
			SELECT
				token,
				sum(balance) AS supply_total,
				countIf(balance > toInt256(0)) AS holders
			FROM erc20_balances
			WHERE chain_id = ?
			  AND token IN (SELECT token FROM stablecoins FINAL WHERE chain_id = ?)
			  AND (token, wallet) NOT IN (
			      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = ?
			  )
			GROUP BY token
		) b ON b.token = s.token
		LEFT JOIN (
			SELECT
				address AS token,
				sum(reinterpretAsUInt256(reverse(data))) AS volume_amount,
				count() AS transfers_count
			FROM raw_logs
			WHERE chain_id = ?
			  AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = ?)
			  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
			  AND length(data) = 32
			  AND block_time >= now() - INTERVAL 24 HOUR
		    GROUP BY address
		) v ON v.token = s.token
		ORDER BY b.supply_total DESC
	`

	rows, err := s.conn.Query(ctx, query, chainID, chainID, chainID, chainID, chainID, chainID)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	out := []Stablecoin{}
	for rows.Next() {
		var (
			tokenBytes                []byte
			symbol, name, peg, issuer string
			decimals                  uint8
			bridged                   bool
			supply, volume24h         string
			holders, transfers24h     uint64
		)
		if err := rows.Scan(&tokenBytes, &symbol, &name, &decimals, &peg, &issuer, &bridged,
			&supply, &holders, &volume24h, &transfers24h); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		out = append(out, Stablecoin{
			Token:        "0x" + hex.EncodeToString(tokenBytes),
			Symbol:       symbol,
			Name:         name,
			Decimals:     decimals,
			Peg:          peg,
			Issuer:       issuer,
			Bridged:      bridged,
			Supply:       supply,
			Holders:      holders,
			Volume24h:    volume24h,
			Transfers24h: transfers24h,
		})
	}

	writeJSON(w, http.StatusOK, Response{Data: out})
}
