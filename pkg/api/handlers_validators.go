package api

import (
	"fmt"
	"net/http"
	"time"
)

// Validator represents an L1 validator
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
	CreatedTxType    string    `json:"created_tx_type,omitempty" example:"RegisterL1Validator"`
	CreatedTime      time.Time `json:"created_time,omitempty"`
}

// ValidatorDeposit represents a balance transaction for a validator
type ValidatorDeposit struct {
	TxID        string    `json:"tx_id" example:"2ZW6HUePB..."`
	TxType      string    `json:"tx_type" example:"IncreaseL1ValidatorBalanceTx"`
	BlockNumber uint64    `json:"block_number" example:"12345678"`
	BlockTime   time.Time `json:"block_time"`
	Amount      uint64    `json:"amount" example:"10000000000"`
}

// handleListValidators returns a paginated list of validators
// @Summary List validators
// @Description Get a paginated list of L1 validators with optional filtering
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

	// Build WHERE clause (using h. prefix for history table, s. for state table)
	whereParts := []string{}
	whereArgs := []interface{}{}
	if subnetID != "" {
		whereParts = append(whereParts, "h.subnet_id = ?")
		whereArgs = append(whereArgs, subnetID)
	}
	if activeOnly {
		whereParts = append(whereParts, "s.active = true")
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + whereParts[0]
		for _, p := range whereParts[1:] {
			whereClause += " AND " + p
		}
	}

	// Merge l1_validator_state (live RPC data) with l1_validator_history (tx-discovered validators)
	// so validators that were never seen by the RPC syncer are still returned.
	query := fmt.Sprintf(`
		SELECT
			h.subnet_id,
			COALESCE(NULLIF(s.validation_id, ''), h.validation_id) AS validation_id,
			h.node_id,
			COALESCE(s.balance, 0) AS balance,
			IF(s.weight > 0, s.weight, h.initial_weight) AS weight,
			COALESCE(s.start_time, h.created_time) AS start_time,
			COALESCE(s.end_time, toDateTime64(0, 3, 'UTC')) AS end_time,
			COALESCE(s.uptime_percentage, 0) AS uptime_percentage,
			COALESCE(s.active, false) AS active,
			IF(s.initial_deposit > 0, s.initial_deposit, h.initial_balance) AS initial_deposit,
			COALESCE(s.total_topups, 0) AS total_topups,
			COALESCE(s.refund_amount, 0) AS refund_amount,
			COALESCE(s.fees_paid, 0) AS fees_paid,
			h.created_tx_type,
			h.created_time
		FROM (SELECT * FROM l1_validator_history FINAL) h
		LEFT JOIN (SELECT * FROM l1_validator_state FINAL) s
			ON h.subnet_id = s.subnet_id AND h.node_id = s.node_id
		%s
		ORDER BY weight DESC
		LIMIT ? OFFSET ?
	`, whereClause)
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
		if err := rows.Scan(
			&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
			&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
			&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
			&v.CreatedTxType, &v.CreatedTime,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		validators = append(validators, v)
	}

	validators, hasMore := trimResults(validators, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}

	if wantCount {
		// Count query uses the same join to respect active filter
		countQuery := fmt.Sprintf(`
			SELECT toInt64(count())
			FROM (SELECT * FROM l1_validator_history FINAL) h
			LEFT JOIN (SELECT * FROM l1_validator_state FINAL) s
				ON h.subnet_id = s.subnet_id AND h.node_id = s.node_id
			%s
		`, whereClause)
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
// @Description Get details for a specific validator by validation ID or node ID
// @Tags Data - Validators
// @Produce json
// @Param id path string true "Validation ID or Node ID"
// @Success 200 {object} Response{data=Validator}
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/validators/{id} [get]
func (s *Server) handleGetValidator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	var v Validator
	err := s.conn.QueryRow(ctx, `
		SELECT
			h.subnet_id,
			COALESCE(NULLIF(s.validation_id, ''), h.validation_id) AS validation_id,
			h.node_id,
			COALESCE(s.balance, 0) AS balance,
			IF(s.weight > 0, s.weight, h.initial_weight) AS weight,
			COALESCE(s.start_time, h.created_time) AS start_time,
			COALESCE(s.end_time, toDateTime64(0, 3, 'UTC')) AS end_time,
			COALESCE(s.uptime_percentage, 0) AS uptime_percentage,
			COALESCE(s.active, false) AS active,
			IF(s.initial_deposit > 0, s.initial_deposit, h.initial_balance) AS initial_deposit,
			COALESCE(s.total_topups, 0) AS total_topups,
			COALESCE(s.refund_amount, 0) AS refund_amount,
			COALESCE(s.fees_paid, 0) AS fees_paid,
			h.created_tx_type,
			h.created_time
		FROM (SELECT * FROM l1_validator_history FINAL) h
		LEFT JOIN (SELECT * FROM l1_validator_state FINAL) s
			ON h.subnet_id = s.subnet_id AND h.node_id = s.node_id
		WHERE h.validation_id = ? OR h.node_id = ?
		LIMIT 1
	`, id, id).Scan(
		&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
		&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
		&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
		&v.CreatedTxType, &v.CreatedTime,
	)

	if err != nil {
		writeNotFoundError(w, "Validator")
		return
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
