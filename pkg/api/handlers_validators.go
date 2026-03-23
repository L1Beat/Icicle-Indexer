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

	// Primary Network data (for legacy subnet validators)
	PrimaryStake  *uint64  `json:"primary_stake,omitempty" example:"2000000000000"`
	PrimaryUptime *float64 `json:"primary_uptime,omitempty" example:"99.99"`
}

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
				p.weight AS primary_stake,
				p.uptime_percentage AS primary_uptime
			FROM (SELECT * FROM l1_validator_state FINAL) v
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
				toUInt64(0) AS primary_stake,
				toFloat64(0) AS primary_uptime
			FROM l1_validator_state FINAL v
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
		var primaryStake uint64
		var primaryUptime float64
		if err := rows.Scan(
			&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
			&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
			&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
			&primaryStake, &primaryUptime,
		); err != nil {
			writeInternalError(w, err.Error())
			return
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
		countQuery := fmt.Sprintf(`SELECT toInt64(count()) FROM l1_validator_state FINAL v %s`, countWhereClause)
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
				subnet_id, validation_id, node_id, balance, weight,
				start_time, end_time, uptime_percentage, active,
				initial_deposit, total_topups, refund_amount, fees_paid
			FROM l1_validator_state FINAL
			WHERE (validation_id = ? OR node_id = ?) AND subnet_id = ?
			LIMIT 1
		`
		args = []interface{}{id, id, subnetID}
	} else {
		query = `
			SELECT
				subnet_id, validation_id, node_id, balance, weight,
				start_time, end_time, uptime_percentage, active,
				initial_deposit, total_topups, refund_amount, fees_paid
			FROM l1_validator_state FINAL
			WHERE validation_id = ? OR node_id = ?
			LIMIT 1
		`
		args = []interface{}{id, id}
	}

	err := s.conn.QueryRow(ctx, query, args...).Scan(
		&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
		&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
		&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
	)

	if err != nil {
		writeNotFoundError(w, "Validator")
		return
	}

	// If this is a legacy subnet validator, enrich with Primary Network data
	if v.SubnetID != primaryNetworkSubnetID {
		var subnetType string
		_ = s.conn.QueryRow(ctx, `
			SELECT subnet_type FROM subnets FINAL WHERE subnet_id = ? LIMIT 1
		`, v.SubnetID).Scan(&subnetType)

		if subnetType == "legacy" {
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
