package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Subnet represents an Avalanche subnet
type Subnet struct {
	SubnetID                string     `json:"subnet_id" example:"2XDnKyAEr..."`
	SubnetType              string     `json:"subnet_type" example:"l1"`
	CreatedBlock            uint64     `json:"created_block" example:"12345678"`
	CreatedTime             time.Time  `json:"created_time"`
	ChainID                 string     `json:"chain_id,omitempty" example:"2q9e4r6Mu..."`
	ConvertedBlock          uint64     `json:"converted_block,omitempty" example:"12345700"`
	ConvertedTime           *time.Time `json:"converted_time,omitempty"`
	ValidatorManagerAddress string     `json:"validator_manager_address,omitempty" example:"0x1234..."`
	ValidatorManagerOwner   string     `json:"validator_manager_owner,omitempty" example:"0x5678..."`
}

// SubnetChain represents a blockchain within a subnet
type SubnetChain struct {
	ChainID      string    `json:"chain_id" example:"2q9e4r6Mu..."`
	SubnetID     string    `json:"subnet_id" example:"2XDnKyAEr..."`
	ChainName    string    `json:"chain_name" example:"My Chain"`
	VMID         string    `json:"vm_id" example:"subnetevm"`
	CreatedBlock uint64    `json:"created_block" example:"12345678"`
	CreatedTime  time.Time `json:"created_time"`
}

// L1Registry represents metadata for an L1
type L1Registry struct {
	SubnetID    string `json:"subnet_id" example:"2XDnKyAEr..."`
	Name        string `json:"name" example:"My L1"`
	Description string `json:"description" example:"A high-performance L1"`
	LogoURL     string `json:"logo_url" example:"https://example.com/logo.png"`
	WebsiteURL  string `json:"website_url" example:"https://example.com"`
}

// SubnetDetail contains full subnet information
type SubnetDetail struct {
	Subnet   Subnet        `json:"subnet"`
	Chains   []SubnetChain `json:"chains"`
	Registry *L1Registry   `json:"registry,omitempty"`
}

// SocialLink represents a social media link in the API response
type SocialLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// NetworkToken represents the native token of a chain
type NetworkToken struct {
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Decimals uint8  `json:"decimals"`
	LogoURI  string `json:"logo_uri,omitempty"`
}

// ChainInfo is the enriched response type for the unified /chains endpoint
type ChainInfo struct {
	// Chain identity
	ChainID      string    `json:"chain_id"`
	ChainName    string    `json:"chain_name"`
	VMID         string    `json:"vm_id"`
	CreatedBlock uint64    `json:"created_block"`
	CreatedTime  time.Time `json:"created_time"`

	// Parent subnet
	SubnetID                string     `json:"subnet_id"`
	ChainType               string     `json:"chain_type"`
	ConvertedBlock          uint64     `json:"converted_block,omitempty"`
	ConvertedTime           *time.Time `json:"converted_time,omitempty"`
	ValidatorManagerAddress string     `json:"validator_manager_address,omitempty"`
	ValidatorManagerOwner   string     `json:"validator_manager_owner,omitempty"`

	// Registry metadata
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	LogoURL     string `json:"logo_url,omitempty"`
	WebsiteURL  string `json:"website_url,omitempty"`

	// Extended registry fields
	EvmChainID          *uint64       `json:"evm_chain_id,omitempty"`
	Categories          []string      `json:"categories,omitempty"`
	Socials             []SocialLink  `json:"socials,omitempty"`
	RpcURL              string        `json:"rpc_url,omitempty"`
	ExplorerURL         string        `json:"explorer_url,omitempty"`
	SybilResistanceType string        `json:"sybil_resistance_type,omitempty"`
	NetworkToken        *NetworkToken `json:"network_token,omitempty"`
	Network             string        `json:"network,omitempty"`

	// Status
	IsActive bool `json:"is_active"`

	// L1 stats (only for L1s)
	ValidatorCount   *uint32 `json:"validator_count,omitempty"`
	ActiveValidators *uint32 `json:"active_validators,omitempty"`
	TotalStaked      *uint64 `json:"total_staked,omitempty"`
	TotalFeesPaid    *uint64 `json:"total_fees_paid,omitempty"`

	// Decentralization summary (compact; only for chains with active L1 validators)
	Decentralization *Decentralization `json:"decentralization,omitempty"`
}

// Decentralization summarizes how concentrated an L1's validator set is.
// Computed from active validators in l1_validator_state. The Nakamoto coefficient
// is the smallest number of validators whose combined weight exceeds the threshold
// fraction of total active weight. Raw weights are strings; counts are numbers.
type Decentralization struct {
	ActiveValidatorCount uint32   `json:"active_validator_count"`
	Nakamoto33           *uint32  `json:"nakamoto_33"`       // min validators controlling >33% of active weight; null if unknown
	Nakamoto50           *uint32  `json:"nakamoto_50"`       // min validators controlling >50% of active weight; null if unknown
	TotalWeight          string   `json:"total_weight"`      // sum of active validator weights, raw
	Weights              []string `json:"weights,omitempty"` // active validator weights sorted desc, raw (full detail only)
}

// RiskResponse is the per-chain risk/decentralization envelope returned by
// GET /api/v1/data/chains/{chainId}/risk. validator_manager and economic are
// populated by later tiers; unknown fields are null, never fabricated.
type RiskResponse struct {
	ChainID          string                `json:"chain_id"`
	ValidatorManager *ValidatorManagerRisk `json:"validator_manager"`
	Decentralization *Decentralization     `json:"decentralization"`
	Economic         *EconomicSecurity     `json:"economic"`
	UpdatedAt        string                `json:"updated_at"`
}

// ValidatorManagerRisk describes who controls the ValidatorManager and whether it
// is upgradeable. Tier 2 populates only the known on-chain addresses (the manager
// address and its owner); type/owner-kind/proxy/churn are filled by the Tier 1
// contract-reading syncer and remain "unknown"/null until then.
type ValidatorManagerRisk struct {
	Address string `json:"address"`
	Type    string `json:"type"` // "PoA" | "PoS-native" | "PoS-erc20" | "unknown"
	// DeployedOn is where the manager contract has code: "c-chain" means validators can
	// still be managed if the L1 halts; "self" means the manager lives on the L1 itself
	// and is unreachable if the chain is stuck; "unknown" means no contract was found.
	DeployedOn string     `json:"deployed_on"`
	Owner      *OwnerInfo `json:"owner"`
	Proxy      *ProxyInfo `json:"proxy"`
	Churn      *ChurnInfo `json:"churn"`
}

// OwnerInfo identifies the ValidatorManager owner and (Tier 1) classifies it.
type OwnerInfo struct {
	Address  string        `json:"address"`
	Kind     string        `json:"kind"` // "eoa" | "multisig" | "timelock" | "dao" | "contract" | "unknown"
	Multisig *MultisigInfo `json:"multisig"`
}

// MultisigInfo is populated when the owner is detected as a Gnosis Safe (Tier 1).
type MultisigInfo struct {
	Threshold int `json:"threshold"`
	Owners    int `json:"owners"`
}

// ProxyInfo describes EIP-1967 proxy state of the ValidatorManager (Tier 1).
type ProxyInfo struct {
	IsProxy             bool   `json:"is_proxy"`
	Implementation      string `json:"implementation"`
	ProxyAdmin          string `json:"proxy_admin"`
	ProxyAdminOwner     string `json:"proxy_admin_owner"`
	UpgradeDelaySeconds uint64 `json:"upgrade_delay_seconds"`
}

// ChurnInfo describes the validator-set churn limits (Tier 1).
type ChurnInfo struct {
	PeriodSeconds      uint64 `json:"period_seconds"`
	MaxChurnPercentage uint32 `json:"max_churn_percentage"`
}

// EconomicSecurity describes the cost to attack the validator set (Tier 3).
type EconomicSecurity struct {
	StakingToken    *NetworkToken `json:"staking_token"`
	TotalStakeRaw   string        `json:"total_stake_raw"`
	TotalStakeUSD   *float64      `json:"total_stake_usd"`
	CostToControl33 *float64      `json:"cost_to_control_33_usd"`
}

// decentralizationSubquery aggregates l1_validator_state into per-subnet
// decentralization metrics. Shared by the chains list and the /risk endpoint so
// the Nakamoto math stays in one place. Weights are sorted descending; the
// Nakamoto coefficient is the 1-based index at which the cumulative weight first
// exceeds the threshold fraction of total active weight.
const decentralizationSubquery = `(
		SELECT subnet_id,
			toUInt32(countIf(active = true)) AS active_validators,
			sum(weight) AS total_staked,
			arrayReverseSort(groupArrayIf(weight, active = true)) AS sorted_weights,
			arraySum(sorted_weights) AS active_weight,
			toUInt32(arrayFirstIndex(x -> x > active_weight * 0.33, arrayCumSum(sorted_weights))) AS nakamoto_33,
			toUInt32(arrayFirstIndex(x -> x > active_weight * 0.50, arrayCumSum(sorted_weights))) AS nakamoto_50
		FROM l1_validator_state FINAL
		GROUP BY subnet_id
	)`

// handleGetSubnet returns details for a specific subnet
// @Summary Get subnet by ID
// @Description Get full details for a subnet including chains and registry info
// @Tags Data - Subnets
// @Produce json
// @Param subnetId path string true "Subnet ID"
// @Success 200 {object} Response{data=SubnetDetail}
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/subnets/{subnetId} [get]
func (s *Server) handleGetSubnet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subnetID := r.PathValue("subnetId")

	// Get subnet info
	var sub Subnet
	var convertedTime time.Time
	err := s.conn.QueryRow(ctx, `
		SELECT subnet_id, subnet_type, created_block, created_time,
			chain_id, converted_block, converted_time, validator_manager_address, validator_manager_owner
		FROM subnets FINAL
		WHERE subnet_id = ?
		LIMIT 1
	`, subnetID).Scan(
		&sub.SubnetID, &sub.SubnetType, &sub.CreatedBlock, &sub.CreatedTime,
		&sub.ChainID, &sub.ConvertedBlock, &convertedTime, &sub.ValidatorManagerAddress, &sub.ValidatorManagerOwner,
	)
	if !convertedTime.IsZero() {
		sub.ConvertedTime = &convertedTime
	}

	if err != nil {
		writeNotFoundError(w, "Subnet")
		return
	}

	// Get chains in this subnet
	rows, err := s.conn.Query(ctx, `
		SELECT chain_id, subnet_id, chain_name, vm_id, created_block, created_time
		FROM subnet_chains FINAL
		WHERE subnet_id = ?
		ORDER BY created_block
	`, subnetID)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	chains := []SubnetChain{}
	for rows.Next() {
		var chain SubnetChain
		if err := rows.Scan(
			&chain.ChainID, &chain.SubnetID, &chain.ChainName,
			&chain.VMID, &chain.CreatedBlock, &chain.CreatedTime,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		chains = append(chains, chain)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	// Get registry info if L1
	var registry *L1Registry
	if sub.SubnetType == "l1" {
		var reg L1Registry
		err := s.conn.QueryRow(ctx, `
			SELECT subnet_id, name, description, logo_url, website_url
			FROM l1_registry FINAL
			WHERE subnet_id = ?
			LIMIT 1
		`, subnetID).Scan(&reg.SubnetID, &reg.Name, &reg.Description, &reg.LogoURL, &reg.WebsiteURL)
		if err == nil {
			registry = &reg
		}
	}

	writeJSON(w, http.StatusOK, Response{
		Data: SubnetDetail{
			Subnet:   sub,
			Chains:   chains,
			Registry: registry,
		},
	})
}

// handleListChains returns a unified list of chains with enriched subnet, registry, and validator data
// @Summary List chains
// @Description Get a paginated list of chains with subnet info, L1 registry metadata, and validator stats
// @Tags Data - Subnets
// @Produce json
// @Param chain_type query string false "Filter by chain type (l1, legacy)"
// @Param subnet_id query string false "Filter by subnet ID"
// @Param category query string false "Filter by category (e.g. DeFi, Gaming)"
// @Param active query string false "Filter by active validators (true)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Param cursor query string false "Cursor for keyset pagination"
// @Param count query string false "Include total count (true/false)"
// @Success 200 {object} Response{data=[]ChainInfo,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/chains [get]
func (s *Server) handleListChains(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)

	chainType := r.URL.Query().Get("chain_type")
	subnetID := r.URL.Query().Get("subnet_id")
	category := r.URL.Query().Get("category")
	active := r.URL.Query().Get("active")
	fetchLimit := limit + 1

	// Build WHERE clauses
	var conditions []string
	var args []interface{}

	if chainType != "" {
		conditions = append(conditions, "s.subnet_type = ?")
		args = append(args, chainType)
	}
	if subnetID != "" {
		conditions = append(conditions, "c.subnet_id = ?")
		args = append(args, subnetID)
	}
	if category != "" {
		conditions = append(conditions, "hasAny(arrayMap(x -> upper(x), r.categories), [upper(?)])")
		args = append(args, category)
	}
	if active == "true" {
		conditions = append(conditions, "v.active_validators > 0")
	}
	if cursor != nil {
		conditions = append(conditions, "c.created_block < ?")
		args = append(args, cursor.BlockNumber)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT
			c.chain_id, c.chain_name, c.vm_id, c.created_block, c.created_time,
			s.subnet_id, s.subnet_type, s.converted_block, s.converted_time, s.validator_manager_address, s.validator_manager_owner,
			r.name, r.description, r.logo_url, r.website_url,
			r.evm_chain_id, r.categories, r.socials,
			r.rpc_url, r.explorer_url, r.sybil_resistance_type,
			r.network_token_name, r.network_token_symbol, r.network_token_decimals, r.network_token_logo_uri,
			r.network,
			f.validator_count, f.total_fees_paid,
			v.active_validators, v.total_staked, v.active_weight, v.nakamoto_33, v.nakamoto_50
		FROM (SELECT * FROM subnet_chains FINAL) c
		INNER JOIN (SELECT * FROM subnets FINAL) s ON c.subnet_id = s.subnet_id
		LEFT JOIN (SELECT * FROM l1_registry FINAL) r ON c.chain_id = r.blockchain_id
		LEFT JOIN (SELECT * FROM l1_fee_stats FINAL) f ON c.subnet_id = f.subnet_id
		LEFT JOIN %s v ON c.subnet_id = v.subnet_id
		%s
		ORDER BY c.created_block DESC, c.chain_id ASC
		LIMIT ?
	`, decentralizationSubquery, whereClause)

	if cursor != nil {
		args = append(args, fetchLimit)
	} else {
		args = append(args, fetchLimit)
		query += " OFFSET ?"
		args = append(args, offset)
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	chains := []ChainInfo{}
	for rows.Next() {
		var ci ChainInfo
		var convertedTime time.Time
		var name, description, logoURL, websiteURL *string
		var evmChainID *uint64
		var categories []string
		var socialsJSON *string
		var rpcURL, explorerURL, sybilType *string
		var tokenName, tokenSymbol *string
		var tokenDecimals *uint8
		var tokenLogoURI *string
		var network *string
		var validatorCount, activeValidators, nakamoto33, nakamoto50 *uint32
		var totalFeesPaid, totalStaked, activeWeight *uint64

		if err := rows.Scan(
			&ci.ChainID, &ci.ChainName, &ci.VMID, &ci.CreatedBlock, &ci.CreatedTime,
			&ci.SubnetID, &ci.ChainType, &ci.ConvertedBlock, &convertedTime, &ci.ValidatorManagerAddress, &ci.ValidatorManagerOwner,
			&name, &description, &logoURL, &websiteURL,
			&evmChainID, &categories, &socialsJSON,
			&rpcURL, &explorerURL, &sybilType,
			&tokenName, &tokenSymbol, &tokenDecimals, &tokenLogoURI,
			&network,
			&validatorCount, &totalFeesPaid,
			&activeValidators, &totalStaked, &activeWeight, &nakamoto33, &nakamoto50,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		if !convertedTime.IsZero() && convertedTime.Unix() > 0 {
			ci.ConvertedTime = &convertedTime
		}

		// Registry metadata
		if name != nil && *name != "" {
			ci.Name = *name
		}
		if description != nil && *description != "" {
			ci.Description = *description
		}
		if logoURL != nil && *logoURL != "" {
			ci.LogoURL = *logoURL
		}
		if websiteURL != nil && *websiteURL != "" {
			ci.WebsiteURL = *websiteURL
		}

		// Extended registry fields
		if evmChainID != nil && *evmChainID > 0 {
			ci.EvmChainID = evmChainID
		}
		if len(categories) > 0 {
			ci.Categories = categories
		}
		if socialsJSON != nil && *socialsJSON != "" && *socialsJSON != "[]" {
			var socials []SocialLink
			if err := json.Unmarshal([]byte(*socialsJSON), &socials); err == nil && len(socials) > 0 {
				ci.Socials = socials
			}
		}
		if rpcURL != nil && *rpcURL != "" {
			ci.RpcURL = *rpcURL
		}
		if explorerURL != nil && *explorerURL != "" {
			ci.ExplorerURL = *explorerURL
		}
		if sybilType != nil && *sybilType != "" {
			ci.SybilResistanceType = *sybilType
		}
		if tokenName != nil && *tokenName != "" {
			ci.NetworkToken = &NetworkToken{
				Name:     *tokenName,
				Decimals: 18,
			}
			if tokenSymbol != nil {
				ci.NetworkToken.Symbol = *tokenSymbol
			}
			if tokenDecimals != nil && *tokenDecimals > 0 {
				ci.NetworkToken.Decimals = *tokenDecimals
			}
			if tokenLogoURI != nil {
				ci.NetworkToken.LogoURI = *tokenLogoURI
			}
		}
		if network != nil && *network != "" {
			ci.Network = *network
		}

		// Validator stats + active status
		ci.IsActive = activeValidators != nil && *activeValidators > 0
		if validatorCount != nil && *validatorCount > 0 {
			ci.ValidatorCount = validatorCount
		}
		if activeValidators != nil && *activeValidators > 0 {
			ci.ActiveValidators = activeValidators
		}
		if totalStaked != nil && *totalStaked > 0 {
			ci.TotalStaked = totalStaked
		}
		if totalFeesPaid != nil && *totalFeesPaid > 0 {
			ci.TotalFeesPaid = totalFeesPaid
		}

		// Decentralization summary: only meaningful when the chain has active L1 validators.
		if activeValidators != nil && *activeValidators > 0 {
			d := &Decentralization{ActiveValidatorCount: *activeValidators}
			if activeWeight != nil {
				d.TotalWeight = strconv.FormatUint(*activeWeight, 10)
			}
			if nakamoto33 != nil && *nakamoto33 > 0 {
				d.Nakamoto33 = nakamoto33
			}
			if nakamoto50 != nil && *nakamoto50 > 0 {
				d.Nakamoto50 = nakamoto50
			}
			ci.Decentralization = d
		}

		chains = append(chains, ci)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	chains, hasMore := trimResults(chains, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(chains) > 0 {
		meta.NextCursor = cursorBlock(chains[len(chains)-1].CreatedBlock)
	}

	if wantCount {
		countQuery := `SELECT toInt64(count()) FROM (SELECT * FROM subnet_chains FINAL) c INNER JOIN (SELECT * FROM subnets FINAL) s ON c.subnet_id = s.subnet_id`
		var countConditions []string
		var countArgs []interface{}
		if chainType != "" {
			countConditions = append(countConditions, "s.subnet_type = ?")
			countArgs = append(countArgs, chainType)
		}
		if subnetID != "" {
			countConditions = append(countConditions, "c.subnet_id = ?")
			countArgs = append(countArgs, subnetID)
		}
		if category != "" {
			countQuery += ` LEFT JOIN (SELECT * FROM l1_registry FINAL) r ON c.chain_id = r.blockchain_id`
			countConditions = append(countConditions, "hasAny(arrayMap(x -> upper(x), r.categories), [upper(?)])")
			countArgs = append(countArgs, category)
		}
		if active == "true" {
			countQuery += ` LEFT JOIN (SELECT subnet_id, toUInt32(countIf(active = true)) AS active_validators FROM l1_validator_state FINAL GROUP BY subnet_id) v ON c.subnet_id = v.subnet_id`
			countConditions = append(countConditions, "v.active_validators > 0")
		}
		if len(countConditions) > 0 {
			countQuery += " WHERE " + strings.Join(countConditions, " AND ")
		}
		var total int64
		_ = s.conn.QueryRow(ctx, countQuery, countArgs...).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: chains,
		Meta: meta,
	})
}

// handleChainRisk returns the per-chain risk/decentralization detail.
// @Summary Get chain risk profile
// @Description Validator-set decentralization (Nakamoto coefficient) plus, in later tiers, ValidatorManager control and economic security. Keyed by the same chain_id as /api/v1/data/chains.
// @Tags Data - Chains
// @Produce json
// @Param chainId path string true "Chain ID"
// @Success 200 {object} Response{data=RiskResponse}
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/chains/{chainId}/risk [get]
func (s *Server) handleChainRisk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chainID := r.PathValue("chainId")

	var (
		gotChainID       string
		vmAddress        string
		vmOwner          string
		activeValidators *uint32
		activeWeight     *uint64
		nakamoto33       *uint32
		nakamoto50       *uint32
		sortedWeights    []uint64

		// chain_risk (Tier 1); crManagerType == "" means no risk row resolved yet.
		crManagerType     string
		crManagerLocation string
		crOwnerAddress    *string
		crOwnerKind       string
		crMultisigThresh  *uint16
		crMultisigOwners  *uint16
		crIsProxy         bool
		crProxyImpl       *string
		crProxyAdmin      *string
		crProxyAdminOwner *string
		crUpgradeDelay    *uint64
		crChurnPeriod     *uint64
		crMaxChurn        *uint8
	)

	query := fmt.Sprintf(`
		SELECT
			c.chain_id,
			s.validator_manager_address,
			s.validator_manager_owner,
			v.active_validators, v.active_weight, v.nakamoto_33, v.nakamoto_50, v.sorted_weights,
			cr.manager_type, cr.manager_location, cr.owner_address, cr.owner_kind, cr.multisig_threshold, cr.multisig_owners,
			cr.is_proxy, cr.proxy_implementation, cr.proxy_admin, cr.proxy_admin_owner, cr.upgrade_delay_seconds,
			cr.churn_period_seconds, cr.max_churn_percentage
		FROM (SELECT * FROM subnet_chains FINAL) c
		INNER JOIN (SELECT * FROM subnets FINAL) s ON c.subnet_id = s.subnet_id
		LEFT JOIN %s v ON c.subnet_id = v.subnet_id
		LEFT JOIN (SELECT * FROM chain_risk FINAL) cr ON c.chain_id = cr.chain_id
		WHERE c.chain_id = ?
		LIMIT 1
	`, decentralizationSubquery)

	err := s.conn.QueryRow(ctx, query, chainID).Scan(
		&gotChainID,
		&vmAddress,
		&vmOwner,
		&activeValidators, &activeWeight, &nakamoto33, &nakamoto50, &sortedWeights,
		&crManagerType, &crManagerLocation, &crOwnerAddress, &crOwnerKind, &crMultisigThresh, &crMultisigOwners,
		&crIsProxy, &crProxyImpl, &crProxyAdmin, &crProxyAdminOwner, &crUpgradeDelay,
		&crChurnPeriod, &crMaxChurn,
	)
	if err != nil {
		writeNotFoundError(w, "Chain")
		return
	}

	resp := RiskResponse{
		ChainID:   gotChainID,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// ValidatorManager: populated from chain_risk (Tier 1) when the risk syncer has
	// resolved this chain. Until then, fall back to the known on-chain addresses only.
	if vmAddress != "" {
		vm := &ValidatorManagerRisk{Address: vmAddress, Type: "unknown", DeployedOn: "unknown"}
		if crManagerType != "" {
			// Tier 1 data present. Unknown fields stay null/"unknown" (never fabricated).
			vm.Type = crManagerType
			if crManagerLocation != "" {
				vm.DeployedOn = crManagerLocation
			}

			ownerAddr := vmOwner
			if crOwnerAddress != nil && *crOwnerAddress != "" {
				ownerAddr = *crOwnerAddress
			}
			if ownerAddr != "" {
				kind := "unknown"
				if crOwnerKind != "" {
					kind = crOwnerKind
				}
				o := &OwnerInfo{Address: ownerAddr, Kind: kind}
				if crMultisigThresh != nil && crMultisigOwners != nil {
					o.Multisig = &MultisigInfo{Threshold: int(*crMultisigThresh), Owners: int(*crMultisigOwners)}
				}
				vm.Owner = o
			}

			if crIsProxy {
				p := &ProxyInfo{IsProxy: true}
				if crProxyImpl != nil {
					p.Implementation = *crProxyImpl
				}
				if crProxyAdmin != nil {
					p.ProxyAdmin = *crProxyAdmin
				}
				if crProxyAdminOwner != nil {
					p.ProxyAdminOwner = *crProxyAdminOwner
				}
				if crUpgradeDelay != nil {
					p.UpgradeDelaySeconds = *crUpgradeDelay
				}
				vm.Proxy = p
			}

			if crChurnPeriod != nil || crMaxChurn != nil {
				c := &ChurnInfo{}
				if crChurnPeriod != nil {
					c.PeriodSeconds = *crChurnPeriod
				}
				if crMaxChurn != nil {
					c.MaxChurnPercentage = uint32(*crMaxChurn)
				}
				vm.Churn = c
			}
		} else if vmOwner != "" {
			// No Tier 1 row yet: known owner address only.
			vm.Owner = &OwnerInfo{Address: vmOwner, Kind: "unknown"}
		}
		resp.ValidatorManager = vm
	}

	// Decentralization (full, with sorted weights): only when the chain has active L1 validators.
	if activeValidators != nil && *activeValidators > 0 {
		d := &Decentralization{ActiveValidatorCount: *activeValidators}
		if activeWeight != nil {
			d.TotalWeight = strconv.FormatUint(*activeWeight, 10)
		}
		if nakamoto33 != nil && *nakamoto33 > 0 {
			d.Nakamoto33 = nakamoto33
		}
		if nakamoto50 != nil && *nakamoto50 > 0 {
			d.Nakamoto50 = nakamoto50
		}
		if len(sortedWeights) > 0 {
			ws := make([]string, len(sortedWeights))
			for i, wt := range sortedWeights {
				ws[i] = strconv.FormatUint(wt, 10)
			}
			d.Weights = ws
		}
		resp.Decentralization = d
	}

	writeJSON(w, http.StatusOK, Response{Data: resp})
}
