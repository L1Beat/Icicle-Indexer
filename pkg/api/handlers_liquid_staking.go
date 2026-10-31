package api

import (
	"context"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"icicle/pkg/liquidstaking"
)

// LiquidStakingProtocol is one liquid staking token with its latest snapshot.
// Live figures are null until the indexer has sampled the protocol.
type LiquidStakingProtocol struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	TokenSymbol     string   `json:"token_symbol"`
	TokenContract   string   `json:"token_contract"` // C-Chain
	TokenDecimals   int      `json:"token_decimals"`
	Status          string   `json:"status"`      // live | ended
	Attribution     string   `json:"attribution"` // confirmed | unconfirmed
	PChainAddresses []string `json:"p_chain_addresses"`

	PooledAvax              *string    `json:"pooled_avax"`      // nAVAX
	TokenSupply             *string    `json:"token_supply"`     // token base units
	ExchangeRate            *string    `json:"exchange_rate"`    // AVAX per token
	ActiveDelegated         *string    `json:"active_delegated"` // nAVAX
	ActiveDelegations       *uint32    `json:"active_delegations"`
	ValidatorCount          *uint32    `json:"validator_count"`
	ShareOfDelegatedPercent *float64   `json:"share_of_delegated_percent"`
	AsOf                    *time.Time `json:"as_of"`
}

// LiquidStakingTotals sums the live protocols.
type LiquidStakingTotals struct {
	PooledAvax              string  `json:"pooled_avax"`      // nAVAX
	ActiveDelegated         string  `json:"active_delegated"` // nAVAX
	ActiveDelegations       uint64  `json:"active_delegations"`
	ShareOfDelegatedPercent float64 `json:"share_of_delegated_percent"`
}

// LiquidStakingOverview is the /metrics/staking/liquid payload.
type LiquidStakingOverview struct {
	Protocols             []LiquidStakingProtocol `json:"protocols"`
	Totals                LiquidStakingTotals     `json:"totals"`
	NetworkDelegatedStake string                  `json:"network_delegated_stake"` // nAVAX, all Primary Network delegations
}

// LiquidStakingPoint is one protocol's sample for one UTC day.
type LiquidStakingPoint struct {
	Date              string `json:"date"`
	ProtocolID        string `json:"protocol_id"`
	PooledAvax        string `json:"pooled_avax"` // nAVAX
	ExchangeRate      string `json:"exchange_rate"`
	ActiveDelegated   string `json:"active_delegated"` // nAVAX
	ActiveDelegations uint32 `json:"active_delegations"`
	ValidatorCount    uint32 `json:"validator_count"`
}

// liquidSnap is a liquid_staking_snapshots row, token figures in wei.
type liquidSnap struct {
	pooledWei, supply       *big.Int
	delegated               uint64
	delegations, validators uint32
	sampledAt               time.Time
}

var liquidStakingCache = newTTLCache(5 * time.Minute)

var weiPerNAVAX = big.NewInt(1_000_000_000)

// handleLiquidStaking returns liquid staking protocols with their latest figures
// @Summary Liquid staking protocols
// @Description AVAX liquid staking tokens with pooled AVAX, token supply and exchange rate from the C-Chain contract, and running delegations whose rewards go to the protocol's P-Chain addresses. Sampled by the indexer every validator sync cycle. attribution says whether the P-Chain addresses are confirmed by the protocol. Amounts are nAVAX strings. Cached for 5 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Success 200 {object} Response{data=LiquidStakingOverview}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/staking/liquid [get]
func (s *Server) handleLiquidStaking(w http.ResponseWriter, r *http.Request) {
	v, err := liquidStakingCache.get(r.Context(), "overview", s.loadLiquidStaking)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadLiquidStaking(ctx context.Context) (interface{}, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT protocol_id,
			argMax(toString(pooled_avax), sampled_at),
			argMax(toString(token_supply), sampled_at),
			argMax(active_delegated, sampled_at),
			argMax(active_delegations, sampled_at),
			argMax(validator_count, sampled_at),
			max(sampled_at)
		FROM liquid_staking_snapshots
		WHERE snapshot_date >= today() - 7
		GROUP BY protocol_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snaps := map[string]liquidSnap{}
	for rows.Next() {
		var id, pooled, supply string
		var sn liquidSnap
		if err := rows.Scan(&id, &pooled, &supply, &sn.delegated, &sn.delegations, &sn.validators, &sn.sampledAt); err != nil {
			return nil, err
		}
		sn.pooledWei, _ = new(big.Int).SetString(pooled, 10)
		sn.supply, _ = new(big.Int).SetString(supply, 10)
		snaps[id] = sn
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sum, err := stakingSummaryCache.get(ctx, "primary", s.loadStakingSummary)
	if err != nil {
		return nil, err
	}
	networkDelegated, _ := new(big.Int).SetString(sum.(StakingSummary).DelegatedStake, 10)
	return buildLiquidOverview(liquidstaking.Protocols, snaps, networkDelegated), nil
}

func buildLiquidOverview(protocols []liquidstaking.Protocol, snaps map[string]liquidSnap, networkDelegated *big.Int) LiquidStakingOverview {
	if networkDelegated == nil {
		networkDelegated = new(big.Int)
	}
	out := LiquidStakingOverview{Protocols: []LiquidStakingProtocol{}, NetworkDelegatedStake: networkDelegated.String()}
	pooledTotal, delegatedTotal := new(big.Int), new(big.Int)
	for _, p := range protocols {
		lp := LiquidStakingProtocol{
			ID: p.ID, Name: p.Name, TokenSymbol: p.TokenSymbol, TokenContract: p.TokenContract,
			TokenDecimals: p.TokenDecimals, Status: p.Status, Attribution: p.Attribution,
			PChainAddresses: append([]string{}, p.PChainAddresses...),
		}
		if sn, ok := snaps[p.ID]; ok && p.Status == "live" && sn.pooledWei != nil && sn.supply != nil {
			pooled := new(big.Int).Quo(sn.pooledWei, weiPerNAVAX)
			pooledStr, supplyStr := pooled.String(), sn.supply.String()
			rate := exchangeRate(sn.pooledWei, sn.supply, p.TokenDecimals)
			delegated := new(big.Int).SetUint64(sn.delegated)
			delegatedStr := delegated.String()
			share := 100 * bigRatio(delegated, networkDelegated)
			dc, vc, asOf := sn.delegations, sn.validators, sn.sampledAt.UTC()
			lp.PooledAvax, lp.TokenSupply, lp.ExchangeRate = &pooledStr, &supplyStr, &rate
			lp.ActiveDelegated, lp.ActiveDelegations, lp.ValidatorCount = &delegatedStr, &dc, &vc
			lp.ShareOfDelegatedPercent, lp.AsOf = &share, &asOf

			pooledTotal.Add(pooledTotal, pooled)
			delegatedTotal.Add(delegatedTotal, delegated)
			out.Totals.ActiveDelegations += uint64(dc)
		}
		out.Protocols = append(out.Protocols, lp)
	}
	out.Totals.PooledAvax = pooledTotal.String()
	out.Totals.ActiveDelegated = delegatedTotal.String()
	out.Totals.ShareOfDelegatedPercent = 100 * bigRatio(delegatedTotal, networkDelegated)
	return out
}

// exchangeRate is AVAX per whole token: (pooled / 1e18) / (supply / 10^decimals),
// as a 12-decimal string. "0" when there is no supply.
func exchangeRate(pooledWei, supply *big.Int, decimals int) string {
	if supply.Sign() == 0 {
		return "0"
	}
	num := new(big.Int).Mul(pooledWei, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	den := new(big.Int).Mul(supply, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	return new(big.Rat).SetFrac(num, den).FloatString(12)
}

// handleLiquidStakingTimeseries returns daily liquid staking samples
// @Summary Liquid staking history
// @Description Daily pooled AVAX, exchange rate and running delegations per liquid staking protocol (last sample of each UTC day). History starts 2026-11-01. Amounts are nAVAX strings. Oldest first.
// @Tags Metrics - Staking
// @Produce json
// @Param protocol query string false "Protocol id, e.g. benqi-savax (default: all)"
// @Param days query int false "Days back, 1-3650" default(365)
// @Success 200 {object} Response{data=[]LiquidStakingPoint}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/staking/liquid/timeseries [get]
func (s *Server) handleLiquidStakingTimeseries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	protocol := r.URL.Query().Get("protocol")
	decimals := map[string]int{}
	for _, p := range liquidstaking.Protocols {
		decimals[p.ID] = p.TokenDecimals
	}
	if _, ok := decimals[protocol]; protocol != "" && !ok {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "unknown protocol")
		return
	}
	days := 365
	if q := r.URL.Query().Get("days"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 3650 {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "days must be 1-3650")
			return
		}
		days = n
	}

	rows, err := s.conn.Query(ctx, `
		SELECT toString(snapshot_date), protocol_id, toString(pooled_avax), toString(token_supply),
			active_delegated, active_delegations, validator_count
		FROM liquid_staking_snapshots FINAL
		WHERE (? = '' OR protocol_id = ?) AND snapshot_date >= today() - ?
		ORDER BY snapshot_date, protocol_id
	`, protocol, protocol, days)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	out := []LiquidStakingPoint{}
	for rows.Next() {
		var p LiquidStakingPoint
		var pooled, supply string
		var delegated uint64
		if err := rows.Scan(&p.Date, &p.ProtocolID, &pooled, &supply, &delegated, &p.ActiveDelegations, &p.ValidatorCount); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		pw, ok1 := new(big.Int).SetString(pooled, 10)
		sp, ok2 := new(big.Int).SetString(supply, 10)
		if !ok1 || !ok2 {
			continue
		}
		dec, ok := decimals[p.ProtocolID]
		if !ok {
			dec = 18
		}
		p.PooledAvax = new(big.Int).Quo(pw, weiPerNAVAX).String()
		p.ExchangeRate = exchangeRate(pw, sp, dec)
		p.ActiveDelegated = strconv.FormatUint(delegated, 10)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}
