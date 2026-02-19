package api

import (
	"net/http"
	"time"
)

// Subnet represents an Avalanche subnet
type Subnet struct {
	SubnetID       string    `json:"subnet_id" example:"2XDnKyAEr..."`
	SubnetType     string    `json:"subnet_type" example:"l1"`
	CreatedBlock   uint64    `json:"created_block" example:"12345678"`
	CreatedTime    time.Time `json:"created_time"`
	ChainID        string    `json:"chain_id,omitempty" example:"2q9e4r6Mu..."`
	ConvertedBlock uint64    `json:"converted_block,omitempty" example:"12345700"`
	ConvertedTime  time.Time `json:"converted_time,omitempty"`
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

// handleListSubnets returns a paginated list of subnets
// @Summary List subnets
// @Description Get a paginated list of subnets with optional type filtering
// @Tags Data - Subnets
// @Produce json
// @Param type query string false "Filter by subnet type (regular, elastic, l1)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]Subnet,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/subnets [get]
func (s *Server) handleListSubnets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)

	subnetType := r.URL.Query().Get("type") // regular, elastic, l1

	var query string
	var args []interface{}

	if subnetType != "" {
		query = `
			SELECT subnet_id, subnet_type, created_block, created_time,
				chain_id, converted_block, converted_time
			FROM subnets FINAL
			WHERE subnet_type = ?
			ORDER BY created_block DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{subnetType, limit, offset}
	} else {
		query = `
			SELECT subnet_id, subnet_type, created_block, created_time,
				chain_id, converted_block, converted_time
			FROM subnets FINAL
			ORDER BY created_block DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	subnets := []Subnet{}
	for rows.Next() {
		var sub Subnet
		if err := rows.Scan(
			&sub.SubnetID, &sub.SubnetType, &sub.CreatedBlock, &sub.CreatedTime,
			&sub.ChainID, &sub.ConvertedBlock, &sub.ConvertedTime,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		subnets = append(subnets, sub)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: subnets,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
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
	err := s.conn.QueryRow(ctx, `
		SELECT subnet_id, subnet_type, created_block, created_time,
			chain_id, converted_block, converted_time
		FROM subnets FINAL
		WHERE subnet_id = ?
		LIMIT 1
	`, subnetID).Scan(
		&sub.SubnetID, &sub.SubnetType, &sub.CreatedBlock, &sub.CreatedTime,
		&sub.ChainID, &sub.ConvertedBlock, &sub.ConvertedTime,
	)

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

// handleListL1s returns a list of L1s with registry metadata
// @Summary List L1s
// @Description Get a paginated list of L1 subnets with registry metadata
// @Tags Data - Subnets
// @Produce json
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]object,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/l1s [get]
func (s *Server) handleListL1s(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)

	// Join subnets with registry for L1-specific endpoint
	// Use subqueries for FINAL since ClickHouse doesn't support FINAL on joined tables
	rows, err := s.conn.Query(ctx, `
		SELECT
			s.subnet_id,
			s.created_block,
			s.created_time,
			s.chain_id,
			s.converted_block,
			s.converted_time,
			r.name,
			r.description,
			r.logo_url,
			r.website_url
		FROM (SELECT * FROM subnets FINAL WHERE subnet_type = 'l1') s
		LEFT JOIN (SELECT * FROM l1_registry FINAL) r ON s.subnet_id = r.subnet_id
		ORDER BY s.converted_block DESC
		LIMIT ? OFFSET ?
	`, limit, offset)

	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	type L1Info struct {
		SubnetID       string    `json:"subnet_id"`
		CreatedBlock   uint64    `json:"created_block"`
		CreatedTime    time.Time `json:"created_time"`
		ChainID        string    `json:"chain_id"`
		ConvertedBlock uint64    `json:"converted_block"`
		ConvertedTime  time.Time `json:"converted_time"`
		Name           string    `json:"name,omitempty"`
		Description    string    `json:"description,omitempty"`
		LogoURL        string    `json:"logo_url,omitempty"`
		WebsiteURL     string    `json:"website_url,omitempty"`
	}

	l1s := []L1Info{}
	for rows.Next() {
		var l1 L1Info
		var name, description, logoURL, websiteURL *string
		if err := rows.Scan(
			&l1.SubnetID, &l1.CreatedBlock, &l1.CreatedTime,
			&l1.ChainID, &l1.ConvertedBlock, &l1.ConvertedTime,
			&name, &description, &logoURL, &websiteURL,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		if name != nil {
			l1.Name = *name
		}
		if description != nil {
			l1.Description = *description
		}
		if logoURL != nil {
			l1.LogoURL = *logoURL
		}
		if websiteURL != nil {
			l1.WebsiteURL = *websiteURL
		}
		l1s = append(l1s, l1)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: l1s,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

// handleListChains returns a list of blockchains
// @Summary List chains
// @Description Get a paginated list of blockchains across all subnets
// @Tags Data - Subnets
// @Produce json
// @Param subnet_id query string false "Filter by subnet ID"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]SubnetChain,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/chains [get]
func (s *Server) handleListChains(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)

	subnetID := r.URL.Query().Get("subnet_id")

	var query string
	var args []interface{}

	if subnetID != "" {
		query = `
			SELECT chain_id, subnet_id, chain_name, vm_id, created_block, created_time
			FROM subnet_chains FINAL
			WHERE subnet_id = ?
			ORDER BY created_block DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{subnetID, limit, offset}
	} else {
		query = `
			SELECT chain_id, subnet_id, chain_name, vm_id, created_block, created_time
			FROM subnet_chains FINAL
			ORDER BY created_block DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
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

	writeJSON(w, http.StatusOK, Response{
		Data: chains,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}
