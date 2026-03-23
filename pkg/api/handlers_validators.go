package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Validator represents a validator (L1 or legacy subnet)
type Validator struct {
	SubnetID         string    `json:"subnet_id" example:"2XDnKyAEr..."`
	ValidationID     string    `json:"validation_id" example:"2ZW6HUePB..."`
	NodeID           string    `json:"node_id" example:"NodeID-P7oB2McjBGgW..."`
	Balance          uint64    `json:"balance" example:"100000000000"`
	Weight           uint64    `json:"weight" example:"2000"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	UptimePercentage float64   `json:"uptime_percentage" example:"99.5"`
	Active           bool      `json:"active" example:"true"`
	InitialDeposit   uint64    `json:"initial_deposit" example:"100000000000"`
	TotalTopups      uint64    `json:"total_topups" example:"50000000000"`
	RefundAmount     uint64    `json:"refund_amount" example:"0"`
	FeesPaid         uint64    `json:"fees_paid" example:"5000000000"`

	// Registration info (from l1_validator_history)
	TxHash                string     `json:"tx_hash,omitempty" example:"2aCDtYusy..."`
	TxType                string     `json:"tx_type,omitempty" example:"RegisterL1ValidatorTx"`
	CreatedBlock          *uint64    `json:"created_block,omitempty" example:"12345678"`
	CreatedTime           *time.Time `json:"created_time,omitempty"`
	BLSPublicKey          string     `json:"bls_public_key,omitempty" example:"0x8ea73dd040..."`
	RemainingBalanceOwner string     `json:"remaining_balance_owner,omitempty" example:"avax1pjht9ute9..."`

	// Computed fields (detail endpoint only)
	TotalDeposited       *uint64  `json:"total_deposited,omitempty" example:"150000000000"`
	DaysRemaining        *float64 `json:"days_remaining,omitempty" example:"45.5"`
	EstimatedDaysLeft    *float64 `json:"estimated_days_left,omitempty" example:"30.2"`
	DailyFeeBurn         *uint64  `json:"daily_fee_burn,omitempty" example:"44236800"`
	NetworkSharePercent  *float64 `json:"network_share_percent,omitempty" example:"0.0027"`

	// Delegation data (Primary Network validators, detail endpoint only)
	DelegationFeePercent *float64 `json:"delegation_fee_percent,omitempty" example:"2.0"`
	DelegatorCount       *uint64  `json:"delegator_count,omitempty" example:"6"`
	TotalDelegated       *uint64  `json:"total_delegated,omitempty" example:"3897926468660"`
	TotalStake           *uint64  `json:"total_stake,omitempty" example:"6347926468660"`

	// Primary Network data (for legacy subnet validators)
	PrimaryStake  *uint64  `json:"primary_stake,omitempty" example:"2000000000000"`
	PrimaryUptime *float64 `json:"primary_uptime,omitempty" example:"99.99"`
}

const (
	// L1 validators burn 512 nAVAX per second
	burnRatePerSecond = 512
	burnRatePerDay    = burnRatePerSecond * 86400 // 44,236,800 nAVAX/day
)

// ValidatorDeposit represents a balance transaction for a validator
type ValidatorDeposit struct {
	TxID        string    `json:"tx_id" example:"2ZW6HUePB..."`
	TxType      string    `json:"tx_type" example:"IncreaseL1ValidatorBalanceTx"`
	BlockNumber uint64    `json:"block_number" example:"12345678"`
	BlockTime   time.Time `json:"block_time"`
	Amount      uint64    `json:"amount" example:"10000000000"`
}

const primaryNetworkSubnetID = "11111111111111111111111111111111LpoYY"

// handleListValidators returns a paginated list of validators
// @Summary List validators
// @Description Get a paginated list of validators with optional filtering. Legacy subnet validators are enriched with Primary Network stake and uptime.
// @Tags Data - Validators
// @Produce json
// @Param subnet_id query string false "Filter by subnet ID"
// @Param active query boolean false "Filter by active status (true/false)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]Validator,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/validators [get]
func (s *Server) handleListValidators(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	wantCount := getCountParam(r)

	subnetID := r.URL.Query().Get("subnet_id")
	activeOnly := r.URL.Query().Get("active") == "true"

	fetchLimit := limit + 1

	// Check if we're querying a legacy subnet (needs Primary Network enrichment)
	isLegacySubnet := false
	if subnetID != "" && subnetID != primaryNetworkSubnetID {
		var subnetType string
		_ = s.conn.QueryRow(ctx, `
			SELECT subnet_type FROM subnets FINAL WHERE subnet_id = ? LIMIT 1
		`, subnetID).Scan(&subnetType)
		isLegacySubnet = subnetType == "legacy"
	}

	// Build WHERE clause
	var whereParts []string
	var whereArgs []interface{}
	if subnetID != "" {
		whereParts = append(whereParts, "v.subnet_id = ?")
		whereArgs = append(whereArgs, subnetID)
	}
	if activeOnly {
		whereParts = append(whereParts, "v.active = true")
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	var query string
	if isLegacySubnet {
		// JOIN with Primary Network entries to get real stake and uptime
		query = fmt.Sprintf(`
			SELECT
				v.subnet_id, v.validation_id, v.node_id, v.balance, v.weight,
				v.start_time, v.end_time, v.uptime_percentage, v.active,
				v.initial_deposit, v.total_topups, v.refund_amount, v.fees_paid,
				h.created_tx_id, h.created_tx_type, h.created_block, h.created_time,
				h.bls_public_key, h.remaining_balance_owner,
				p.weight AS primary_stake,
				p.uptime_percentage AS primary_uptime
			FROM (SELECT * FROM l1_validator_state FINAL) v
			LEFT JOIN (SELECT * FROM l1_validator_history FINAL) h
				ON v.validation_id = h.validation_id AND v.subnet_id = h.subnet_id
			LEFT JOIN (
				SELECT node_id, weight, uptime_percentage
				FROM l1_validator_state FINAL
				WHERE subnet_id = '%s'
			) p ON v.node_id = p.node_id
			%s
			ORDER BY p.weight DESC, v.weight DESC
			LIMIT ? OFFSET ?
		`, primaryNetworkSubnetID, whereClause)
	} else {
		query = fmt.Sprintf(`
			SELECT
				v.subnet_id, v.validation_id, v.node_id, v.balance, v.weight,
				v.start_time, v.end_time, v.uptime_percentage, v.active,
				v.initial_deposit, v.total_topups, v.refund_amount, v.fees_paid,
				h.created_tx_id, h.created_tx_type, h.created_block, h.created_time,
				h.bls_public_key, h.remaining_balance_owner,
				toUInt64(0) AS primary_stake,
				toFloat64(0) AS primary_uptime
			FROM (SELECT * FROM l1_validator_state FINAL) v
			LEFT JOIN (SELECT * FROM l1_validator_history FINAL) h
				ON v.validation_id = h.validation_id AND v.subnet_id = h.subnet_id
			%s
			ORDER BY v.weight DESC
			LIMIT ? OFFSET ?
		`, whereClause)
	}
	args := append(whereArgs, fetchLimit, offset)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	validators := []Validator{}
	for rows.Next() {
		var v Validator
		var createdBlock uint64
		var createdTime time.Time
		var primaryStake uint64
		var primaryUptime float64
		if err := rows.Scan(
			&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
			&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
			&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
			&v.TxHash, &v.TxType, &createdBlock, &createdTime,
			&v.BLSPublicKey, &v.RemainingBalanceOwner,
			&primaryStake, &primaryUptime,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		if createdBlock > 0 {
			v.CreatedBlock = &createdBlock
			v.CreatedTime = &createdTime
		}
		if isLegacySubnet && primaryStake > 0 {
			v.PrimaryStake = &primaryStake
			v.PrimaryUptime = &primaryUptime
		}
		validators = append(validators, v)
	}

	validators, hasMore := trimResults(validators, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}

	if wantCount {
		countWhereClause := ""
		if len(whereParts) > 0 {
			countWhereClause = "WHERE " + strings.Join(whereParts, " AND ")
		}
		countQuery := fmt.Sprintf(`SELECT toInt64(count()) FROM (SELECT * FROM l1_validator_state FINAL) v %s`, countWhereClause)
		var total int64
		_ = s.conn.QueryRow(ctx, countQuery, whereArgs...).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: validators,
		Meta: meta,
	})
}

// handleGetValidator returns a single validator
// @Summary Get validator by ID
// @Description Get details for a specific validator by validation ID or node ID. Legacy subnet validators include Primary Network stake and uptime.
// @Tags Data - Validators
// @Produce json
// @Param id path string true "Validation ID or Node ID"
// @Param subnet_id query string false "Subnet ID (to get subnet-specific entry for a node)"
// @Success 200 {object} Response{data=Validator}
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/validators/{id} [get]
func (s *Server) handleGetValidator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	subnetID := r.URL.Query().Get("subnet_id")

	var v Validator

	// If subnet_id is provided, get that specific entry
	var query string
	var args []interface{}
	if subnetID != "" {
		query = `
			SELECT
				v.subnet_id, v.validation_id, v.node_id, v.balance, v.weight,
				v.start_time, v.end_time, v.uptime_percentage, v.active,
				v.initial_deposit, v.total_topups, v.refund_amount, v.fees_paid,
				h.created_tx_id, h.created_tx_type, h.created_block, h.created_time,
				h.bls_public_key, h.remaining_balance_owner
			FROM (SELECT * FROM l1_validator_state FINAL) v
			LEFT JOIN (SELECT * FROM l1_validator_history FINAL) h
				ON v.validation_id = h.validation_id AND v.subnet_id = h.subnet_id
			WHERE (v.validation_id = ? OR v.node_id = ?) AND v.subnet_id = ?
			LIMIT 1
		`
		args = []interface{}{id, id, subnetID}
	} else {
		query = `
			SELECT
				v.subnet_id, v.validation_id, v.node_id, v.balance, v.weight,
				v.start_time, v.end_time, v.uptime_percentage, v.active,
				v.initial_deposit, v.total_topups, v.refund_amount, v.fees_paid,
				h.created_tx_id, h.created_tx_type, h.created_block, h.created_time,
				h.bls_public_key, h.remaining_balance_owner
			FROM (SELECT * FROM l1_validator_state FINAL) v
			LEFT JOIN (SELECT * FROM l1_validator_history FINAL) h
				ON v.validation_id = h.validation_id AND v.subnet_id = h.subnet_id
			WHERE v.validation_id = ? OR v.node_id = ?
			LIMIT 1
		`
		args = []interface{}{id, id}
	}

	var createdBlock uint64
	var createdTime time.Time
	err := s.conn.QueryRow(ctx, query, args...).Scan(
		&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
		&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
		&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
		&v.TxHash, &v.TxType, &createdBlock, &createdTime,
		&v.BLSPublicKey, &v.RemainingBalanceOwner,
	)

	if err != nil {
		writeNotFoundError(w, "Validator")
		return
	}
	if createdBlock > 0 {
		v.CreatedBlock = &createdBlock
		v.CreatedTime = &createdTime
	}

	// Computed fields
	totalDeposited := v.InitialDeposit + v.TotalTopups
	v.TotalDeposited = &totalDeposited

	now := time.Now()
	if v.Active && v.EndTime.After(now) {
		daysRemaining := v.EndTime.Sub(now).Hours() / 24
		v.DaysRemaining = &daysRemaining
	}

	// L1 validators have fee burn; Primary Network and legacy don't
	isL1Validator := false
	if v.SubnetID != primaryNetworkSubnetID {
		var subnetType string
		_ = s.conn.QueryRow(ctx, `
			SELECT subnet_type FROM subnets FINAL WHERE subnet_id = ? LIMIT 1
		`, v.SubnetID).Scan(&subnetType)

		if subnetType == "legacy" {
			// Legacy subnet validator — enrich with Primary Network data
			var primaryStake uint64
			var primaryUptime float64
			err := s.conn.QueryRow(ctx, `
				SELECT weight, uptime_percentage
				FROM l1_validator_state FINAL
				WHERE node_id = ? AND subnet_id = ?
				LIMIT 1
			`, v.NodeID, primaryNetworkSubnetID).Scan(&primaryStake, &primaryUptime)
			if err == nil && primaryStake > 0 {
				v.PrimaryStake = &primaryStake
				v.PrimaryUptime = &primaryUptime
			}
		} else {
			isL1Validator = true
		}
	}

	if isL1Validator {
		// L1 fee burn stats
		dailyBurn := uint64(burnRatePerDay)
		v.DailyFeeBurn = &dailyBurn
		if v.Active && v.Balance > 0 {
			daysLeft := float64(v.Balance) / float64(burnRatePerDay)
			v.EstimatedDaysLeft = &daysLeft
		}
	}

	// Delegation data — query from p_chain_txs for Primary Network validators
	if v.SubnetID == primaryNetworkSubnetID {
		// Get delegation fee % from the validator's registration tx
		var shares uint32
		err := s.conn.QueryRow(ctx, `
			SELECT toUInt32OrZero(toString(tx_data.shares))
			FROM p_chain_txs
			WHERE tx_type IN ('AddValidator', 'AddPermissionlessValidator')
			AND toString(tx_data.validator.nodeID) = ?
			ORDER BY block_number DESC
			LIMIT 1
		`, v.NodeID).Scan(&shares)
		if err == nil && shares > 0 {
			feePercent := float64(shares) / 10000.0 // shares is in basis points (10000 = 1%)
			v.DelegationFeePercent = &feePercent
		}

		// Get delegator count and total delegated stake
		var delegatorCount uint64
		var totalDelegated uint64
		err = s.conn.QueryRow(ctx, `
			SELECT
				toUInt64(count()) as cnt,
				toUInt64(sum(toUInt64OrZero(toString(tx_data.validator.wght)))) as total
			FROM p_chain_txs
			WHERE tx_type IN ('AddDelegator', 'AddPermissionlessDelegator')
			AND toString(tx_data.validator.nodeID) = ?
		`, v.NodeID).Scan(&delegatorCount, &totalDelegated)
		if err == nil && delegatorCount > 0 {
			v.DelegatorCount = &delegatorCount
			v.TotalDelegated = &totalDelegated
			totalStake := v.Weight + totalDelegated
			v.TotalStake = &totalStake
		}

		// Network share
		var totalNetworkStake uint64
		_ = s.conn.QueryRow(ctx, `
			SELECT toUInt64(sum(weight))
			FROM (SELECT * FROM l1_validator_state FINAL)
			WHERE subnet_id = ?
		`, primaryNetworkSubnetID).Scan(&totalNetworkStake)
		if totalNetworkStake > 0 {
			share := (float64(v.Weight) / float64(totalNetworkStake)) * 100
			v.NetworkSharePercent = &share
		}
	}

	writeJSON(w, http.StatusOK, Response{Data: v})
}

// handleValidatorDeposits returns deposit transactions for a validator
// @Summary Get validator deposits
// @Description Get balance transactions (deposits, topups, refunds) for a validator
// @Tags Data - Validators
// @Produce json
// @Param id path string true "Validation ID or Node ID"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]ValidatorDeposit,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/validators/{id}/deposits [get]
func (s *Server) handleValidatorDeposits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)
	id := r.PathValue("id")

	fetchLimit := limit + 1

	var query string
	var args []interface{}

	if cursor != nil {
		query = `
			SELECT tx_id, tx_type, block_number, block_time, amount
			FROM l1_validator_balance_txs FINAL
			WHERE (validation_id = ? OR node_id = ?) AND block_number < ?
			ORDER BY block_number DESC
			LIMIT ?
		`
		args = []interface{}{id, id, cursor.BlockNumber, fetchLimit}
	} else {
		query = `
			SELECT tx_id, tx_type, block_number, block_time, amount
			FROM l1_validator_balance_txs FINAL
			WHERE validation_id = ? OR node_id = ?
			ORDER BY block_number DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{id, id, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	deposits := []ValidatorDeposit{}
	for rows.Next() {
		var d ValidatorDeposit
		if err := rows.Scan(&d.TxID, &d.TxType, &d.BlockNumber, &d.BlockTime, &d.Amount); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		deposits = append(deposits, d)
	}

	deposits, hasMore := trimResults(deposits, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(deposits) > 0 {
		meta.NextCursor = cursorBlock(deposits[len(deposits)-1].BlockNumber)
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `
			SELECT toInt64(count()) FROM l1_validator_balance_txs FINAL
			WHERE validation_id = ? OR node_id = ?
		`, id, id).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: deposits,
		Meta: meta,
	})
}
