package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Subnet represents an Avalanche subnet
type Subnet struct {
	SubnetID       string    `json:"subnet_id" example:"2XDnKyAEr..."`
	SubnetType     string    `json:"subnet_type" example:"l1"`
	CreatedBlock   uint64    `json:"created_block" example:"12345678"`
	CreatedTime    time.Time `json:"created_time"`
	ChainID        string    `json:"chain_id,omitempty" example:"2q9e4r6Mu..."`
	ConvertedBlock uint64     `json:"converted_block,omitempty" example:"12345700"`
	ConvertedTime  *time.Time `json:"converted_time,omitempty"`
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
	SubnetID       string     `json:"subnet_id"`
	ChainType      string     `json:"chain_type"`
	ConvertedBlock uint64     `json:"converted_block,omitempty"`
	ConvertedTime  *time.Time `json:"converted_time,omitempty"`

	// Registry metadata
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	LogoURL     string `json:"logo_url,omitempty"`
	WebsiteURL  string `json:"website_url,omitempty"`

	// Extended registry fields
	EvmChainID          *uint64      `json:"evm_chain_id,omitempty"`
	Categories          []string     `json:"categories,omitempty"`
	Socials             []SocialLink `json:"socials,omitempty"`
	RpcURL              string       `json:"rpc_url,omitempty"`
	ExplorerURL         string       `json:"explorer_url,omitempty"`
	SybilResistanceType string       `json:"sybil_resistance_type,omitempty"`
	NetworkToken        *NetworkToken `json:"network_token,omitempty"`
	Network             string       `json:"network,omitempty"`

	// L1 stats (only for L1s)
	ValidatorCount   *uint32 `json:"validator_count,omitempty"`
	ActiveValidators *uint32 `json:"active_validators,omitempty"`
	TotalStaked      *uint64 `json:"total_staked,omitempty"`
	TotalFeesPaid    *uint64 `json:"total_fees_paid,omitempty"`
}

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
			chain_id, converted_block, converted_time
		FROM subnets FINAL
		WHERE subnet_id = ?
		LIMIT 1
	`, subnetID).Scan(
		&sub.SubnetID, &sub.SubnetType, &sub.CreatedBlock, &sub.CreatedTime,
		&sub.ChainID, &sub.ConvertedBlock, &convertedTime,
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
		conditions = append(conditions, "has(r.categories, ?)")
		args = append(args, category)
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
			s.subnet_id, s.subnet_type, s.converted_block, s.converted_time,
			r.name, r.description, r.logo_url, r.website_url,
			r.evm_chain_id, r.categories, r.socials,
			r.rpc_url, r.explorer_url, r.sybil_resistance_type,
			r.network_token_name, r.network_token_symbol, r.network_token_decimals, r.network_token_logo_uri,
			r.network,
			f.validator_count, f.total_fees_paid,
			v.active_validators, v.total_staked
		FROM (SELECT * FROM subnet_chains FINAL) c
		INNER JOIN (SELECT * FROM subnets FINAL) s ON c.subnet_id = s.subnet_id
		LEFT JOIN (SELECT * FROM l1_registry FINAL) r ON c.subnet_id = r.subnet_id
		LEFT JOIN (SELECT * FROM l1_fee_stats FINAL) f ON c.subnet_id = f.subnet_id
		LEFT JOIN (
			SELECT subnet_id,
				toUInt32(countIf(active = true)) AS active_validators,
				sum(weight) AS total_staked
			FROM l1_validator_state FINAL
			GROUP BY subnet_id
		) v ON c.subnet_id = v.subnet_id
		%s
		ORDER BY c.created_block DESC
		LIMIT ?
	`, whereClause)

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
		var validatorCount, activeValidators *uint32
		var totalFeesPaid, totalStaked *uint64

		if err := rows.Scan(
			&ci.ChainID, &ci.ChainName, &ci.VMID, &ci.CreatedBlock, &ci.CreatedTime,
			&ci.SubnetID, &ci.ChainType, &ci.ConvertedBlock, &convertedTime,
			&name, &description, &logoURL, &websiteURL,
			&evmChainID, &categories, &socialsJSON,
			&rpcURL, &explorerURL, &sybilType,
			&tokenName, &tokenSymbol, &tokenDecimals, &tokenLogoURI,
			&network,
			&validatorCount, &totalFeesPaid,
			&activeValidators, &totalStaked,
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
				Name:    *tokenName,
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

		// Validator stats
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

		chains = append(chains, ci)
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
			countQuery = `SELECT toInt64(count()) FROM (SELECT * FROM subnet_chains FINAL) c INNER JOIN (SELECT * FROM subnets FINAL) s ON c.subnet_id = s.subnet_id LEFT JOIN (SELECT * FROM l1_registry FINAL) r ON c.subnet_id = r.subnet_id`
			countConditions = append(countConditions, "has(r.categories, ?)")
			countArgs = append(countArgs, category)
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
