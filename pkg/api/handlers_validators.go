package api

import (
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
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	subnetID := r.URL.Query().Get("subnet_id")
	activeOnly := r.URL.Query().Get("active") == "true"

	var query string
	var args []interface{}

	if subnetID != "" && activeOnly {
		query = `
			SELECT
				subnet_id, validation_id, node_id, balance, weight,
				start_time, end_time, uptime_percentage, active,
				initial_deposit, total_topups, refund_amount, fees_paid
			FROM l1_validator_state FINAL
			WHERE subnet_id = ? AND active = true
			ORDER BY weight DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{subnetID, limit, offset}
	} else if subnetID != "" {
		query = `
			SELECT
				subnet_id, validation_id, node_id, balance, weight,
				start_time, end_time, uptime_percentage, active,
				initial_deposit, total_topups, refund_amount, fees_paid
			FROM l1_validator_state FINAL
			WHERE subnet_id = ?
			ORDER BY weight DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{subnetID, limit, offset}
	} else if activeOnly {
		query = `
			SELECT
				subnet_id, validation_id, node_id, balance, weight,
				start_time, end_time, uptime_percentage, active,
				initial_deposit, total_topups, refund_amount, fees_paid
			FROM l1_validator_state FINAL
			WHERE active = true
			ORDER BY weight DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
	} else {
		query = `
			SELECT
				subnet_id, validation_id, node_id, balance, weight,
				start_time, end_time, uptime_percentage, active,
				initial_deposit, total_topups, refund_amount, fees_paid
			FROM l1_validator_state FINAL
			ORDER BY weight DESC
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

	validators := []Validator{}
	for rows.Next() {
		var v Validator
		if err := rows.Scan(
			&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
			&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
			&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		validators = append(validators, v)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: validators,
		Meta: &Meta{Limit: limit, Offset: offset},
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
	ctx := s.queryContext()
	id := r.PathValue("id")

	var v Validator
	err := s.conn.QueryRow(ctx, `
		SELECT
			subnet_id, validation_id, node_id, balance, weight,
			start_time, end_time, uptime_percentage, active,
			initial_deposit, total_topups, refund_amount, fees_paid
		FROM l1_validator_state FINAL
		WHERE validation_id = ? OR node_id = ?
		LIMIT 1
	`, id, id).Scan(
		&v.SubnetID, &v.ValidationID, &v.NodeID, &v.Balance, &v.Weight,
		&v.StartTime, &v.EndTime, &v.UptimePercentage, &v.Active,
		&v.InitialDeposit, &v.TotalTopups, &v.RefundAmount, &v.FeesPaid,
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
	ctx := s.queryContext()
	limit, offset := getPagination(r)
	id := r.PathValue("id")

	rows, err := s.conn.Query(ctx, `
		SELECT tx_id, tx_type, block_number, block_time, amount
		FROM l1_validator_balance_txs FINAL
		WHERE validation_id = ? OR node_id = ?
		ORDER BY block_number DESC
		LIMIT ? OFFSET ?
	`, id, id, limit, offset)

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

	writeJSON(w, http.StatusOK, Response{
		Data: deposits,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}
