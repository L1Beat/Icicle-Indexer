package api

import (
	"context"
	"math/big"
	"net/http"
	"sort"
	"time"
)

// RestakingProtocol is a restaking protocol whose vaults can secure L1s.
type RestakingProtocol struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// RestakingCollateral is a vault's underlying token.
type RestakingCollateral struct {
	Address  string `json:"address"` // underlying token, C-Chain
	Wrapper  string `json:"wrapper"` // vault collateral() token, equal to address when unwrapped
	Symbol   string `json:"symbol"`
	Decimals uint8  `json:"decimals"`
}

// RestakingVault is one vault securing an L1. Amounts are token base units.
type RestakingVault struct {
	Address    string              `json:"address"`
	Collateral RestakingCollateral `json:"collateral"`
	Deposited  string              `json:"deposited"` // activeStake()
	Allocated  string              `json:"allocated"` // delegated to the L1's operators
	Idle       string              `json:"idle"`      // deposited - allocated
	Limit      string              `json:"limit"`     // l1Limit, cap on allocation to this L1
}

// RestakingL1 is one L1 secured by a restaking protocol.
type RestakingL1 struct {
	SubnetID     string  `json:"subnet_id"`
	BlockchainID *string `json:"blockchain_id"`
	ProtocolID   string  `json:"protocol_id"`
	Balancer     string  `json:"balancer"`
	Middleware   string  `json:"middleware"`
	// IsActive and ActiveValidators use the /data/chains definition: L1
	// validators with a P-Chain balance.
	IsActive         bool   `json:"is_active"`
	ActiveValidators uint32 `json:"active_validators"`
	// RestakedWeight is the P-Chain weight of active validators registered by
	// the middleware, TotalWeight that of all active validators.
	RestakedWeight      uint64           `json:"restaked_weight"`
	RestakedValidators  uint32           `json:"restaked_validators"`
	TotalWeight         uint64           `json:"total_pchain_weight"`
	ShareOfWeightPct    *float64         `json:"share_of_weight_percent"` // null when no active weight
	ModuleWeight        uint64           `json:"module_weight"`           // middleware's weight in the balancer
	OperatorCount       uint32           `json:"operator_count"`
	Vaults              []RestakingVault `json:"vaults"`
	SampledAt           time.Time        `json:"sampled_at"`
	validationIDs       []string
	latestSampledAtUnix int64
}

// RestakingOverview is the /metrics/restaking payload.
type RestakingOverview struct {
	Protocols []RestakingProtocol `json:"protocols"`
	L1s       []RestakingL1       `json:"l1s"`
}

var restakingProtocols = []RestakingProtocol{
	{ID: "suzaku", Name: "Suzaku", URL: "https://suzaku.network"},
}

var restakingCache = newTTLCache(5 * time.Minute)

// handleRestaking returns L1s secured by restaking vaults
// @Summary Restaked collateral per L1
// @Description L1s whose validators are secured by restaking vaults on the C-Chain (Suzaku). Each L1 lists its vaults with deposited, allocated (delegated to the L1's operators) and idle collateral in token base units, and the share of the L1's active P-Chain validator weight held by validators the protocol registered. Every L1 in the Suzaku L1Registry with at least one vault is included, active or not. Sampled by the indexer every C-Chain snapshot cycle (5 minutes by default, `cchainSnapshotInterval`) and cached for 5 minutes.
// @Tags Metrics - Staking
// @Produce json
// @Param subnet_id query string false "Only this L1 subnet ID"
// @Param chain_id query string false "Only the L1 with this blockchain ID"
// @Success 200 {object} Response{data=RestakingOverview}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/restaking [get]
func (s *Server) handleRestaking(w http.ResponseWriter, r *http.Request) {
	v, err := restakingCache.get(r.Context(), "overview", s.loadRestaking)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: filterRestaking(v.(RestakingOverview), r.URL.Query().Get("subnet_id"), r.URL.Query().Get("chain_id"))})
}

func filterRestaking(o RestakingOverview, subnetID, chainID string) RestakingOverview {
	if subnetID == "" && chainID == "" {
		return o
	}
	out := RestakingOverview{Protocols: o.Protocols, L1s: []RestakingL1{}}
	for _, l1 := range o.L1s {
		if subnetID != "" && l1.SubnetID != subnetID {
			continue
		}
		if chainID != "" && (l1.BlockchainID == nil || *l1.BlockchainID != chainID) {
			continue
		}
		out.L1s = append(out.L1s, l1)
	}
	return out
}

func (s *Server) loadRestaking(ctx context.Context) (interface{}, error) {
	// Rows from the last hour of samples. An L1 that left the registry stops
	// being sampled and ages out.
	rows, err := s.conn.Query(ctx, `
		SELECT protocol_id, subnet_id, vault, balancer, middleware, module_weight,
			operator_count, validation_ids, collateral, token, token_symbol, token_decimals,
			toString(deposited), toString(allocated), toString(l1_limit), sampled_at
		FROM (SELECT * FROM restaking_snapshots FINAL) t
		WHERE sampled_at >= (SELECT max(sampled_at) FROM restaking_snapshots) - INTERVAL 1 HOUR
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byKey := map[string]*RestakingL1{}
	for rows.Next() {
		var l1 RestakingL1
		var v RestakingVault
		var deposited, allocated string
		if err := rows.Scan(&l1.ProtocolID, &l1.SubnetID, &v.Address, &l1.Balancer, &l1.Middleware, &l1.ModuleWeight,
			&l1.OperatorCount, &l1.validationIDs, &v.Collateral.Wrapper, &v.Collateral.Address, &v.Collateral.Symbol, &v.Collateral.Decimals,
			&deposited, &allocated, &v.Limit, &l1.SampledAt); err != nil {
			return nil, err
		}
		v.Deposited, v.Allocated, v.Idle = deposited, allocated, idleAmount(deposited, allocated)
		addRestakingRow(byKey, l1, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	l1s := make([]RestakingL1, 0, len(byKey))
	subnets := []string{}
	var vids []string
	for _, l1 := range byKey {
		l1s = append(l1s, *l1)
		subnets = append(subnets, l1.SubnetID)
		vids = append(vids, l1.validationIDs...)
	}
	if vids == nil {
		vids = []string{}
	}
	if len(l1s) > 0 {
		if err := s.addRestakingWeights(ctx, l1s, subnets, vids); err != nil {
			return nil, err
		}
		if err := s.addRestakingBlockchains(ctx, l1s, subnets); err != nil {
			return nil, err
		}
	}
	sort.Slice(l1s, func(i, j int) bool {
		if l1s[i].RestakedWeight != l1s[j].RestakedWeight {
			return l1s[i].RestakedWeight > l1s[j].RestakedWeight
		}
		return l1s[i].SubnetID < l1s[j].SubnetID
	})
	return RestakingOverview{Protocols: restakingProtocols, L1s: l1s}, nil
}

// addRestakingRow folds one vault row into its L1. Only rows from an L1's
// newest sample are kept, which drops vaults no longer in the middleware.
func addRestakingRow(byKey map[string]*RestakingL1, row RestakingL1, v RestakingVault) {
	key := row.ProtocolID + "/" + row.SubnetID
	ts := row.SampledAt.UnixMilli()
	cur, ok := byKey[key]
	if !ok || ts > cur.latestSampledAtUnix {
		row.latestSampledAtUnix = ts
		row.SampledAt = row.SampledAt.UTC()
		row.Vaults = []RestakingVault{v}
		byKey[key] = &row
		return
	}
	if ts == cur.latestSampledAtUnix {
		cur.Vaults = append(cur.Vaults, v)
	}
}

func idleAmount(deposited, allocated string) string {
	d, ok1 := new(big.Int).SetString(deposited, 10)
	a, ok2 := new(big.Int).SetString(allocated, 10)
	if !ok1 || !ok2 || d.Cmp(a) <= 0 {
		return "0"
	}
	return d.Sub(d, a).String()
}

// addRestakingWeights fills validator weights from l1_validator_state, the same
// table and active flag behind /data/chains.
func (s *Server) addRestakingWeights(ctx context.Context, l1s []RestakingL1, subnets, vids []string) error {
	rows, err := s.conn.Query(ctx, `
		SELECT subnet_id,
			toUInt32(countIf(active)),
			toUInt64(sumIf(weight, active)),
			toUInt32(countIf(active AND has(?, validation_id))),
			toUInt64(sumIf(weight, active AND has(?, validation_id)))
		FROM l1_validator_state FINAL
		WHERE has(?, subnet_id)
		GROUP BY subnet_id
	`, vids, vids, subnets)
	if err != nil {
		return err
	}
	defer rows.Close()
	type w struct {
		active, restakedN uint32
		total, restaked   uint64
	}
	ws := map[string]w{}
	for rows.Next() {
		var id string
		var x w
		if err := rows.Scan(&id, &x.active, &x.total, &x.restakedN, &x.restaked); err != nil {
			return err
		}
		ws[id] = x
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range l1s {
		x := ws[l1s[i].SubnetID]
		l1s[i].IsActive = x.active > 0
		l1s[i].ActiveValidators = x.active
		l1s[i].TotalWeight = x.total
		l1s[i].RestakedWeight = x.restaked
		l1s[i].RestakedValidators = x.restakedN
		if x.total > 0 {
			pct := 100 * float64(x.restaked) / float64(x.total)
			l1s[i].ShareOfWeightPct = &pct
		}
	}
	return nil
}

// addRestakingBlockchains sets each L1's blockchain ID, preferring the chain
// the L1 registry lists when a subnet has several.
func (s *Server) addRestakingBlockchains(ctx context.Context, l1s []RestakingL1, subnets []string) error {
	rows, err := s.conn.Query(ctx, `
		SELECT c.subnet_id, c.chain_id
		FROM (SELECT * FROM subnet_chains FINAL) c
		LEFT JOIN (SELECT blockchain_id FROM l1_registry FINAL) r ON c.chain_id = r.blockchain_id
		WHERE has(?, c.subnet_id)
		ORDER BY (r.blockchain_id != '') DESC, c.created_block ASC
	`, subnets)
	if err != nil {
		return err
	}
	defer rows.Close()
	chains := map[string]string{}
	for rows.Next() {
		var subnet, chain string
		if err := rows.Scan(&subnet, &chain); err != nil {
			return err
		}
		if _, ok := chains[subnet]; !ok {
			chains[subnet] = chain
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range l1s {
		if c, ok := chains[l1s[i].SubnetID]; ok {
			l1s[i].BlockchainID = &c
		}
	}
	return nil
}
