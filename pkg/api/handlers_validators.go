package api

import (
	"net/http"
	"time"
)

type Validator struct {
	SubnetID         string    `json:"subnet_id"`
	ValidationID     string    `json:"validation_id"`
	NodeID           string    `json:"node_id"`
	Balance          uint64    `json:"balance"`
	Weight           uint64    `json:"weight"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	UptimePercentage float64   `json:"uptime_percentage"`
	Active           bool      `json:"active"`
	InitialDeposit   uint64    `json:"initial_deposit"`
	TotalTopups      uint64    `json:"total_topups"`
	RefundAmount     uint64    `json:"refund_amount"`
	FeesPaid         uint64    `json:"fees_paid"`
}

type ValidatorDeposit struct {
	TxID        string    `json:"tx_id"`
	TxType      string    `json:"tx_type"`
	BlockNumber uint64    `json:"block_number"`
	BlockTime   time.Time `json:"block_time"`
	Amount      uint64    `json:"amount"`
}

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
		writeError(w, http.StatusInternalServerError, err.Error())
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
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		validators = append(validators, v)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: validators,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

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
		writeError(w, http.StatusNotFound, "validator not found")
		return
	}

	writeJSON(w, http.StatusOK, Response{Data: v})
}

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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	deposits := []ValidatorDeposit{}
	for rows.Next() {
		var d ValidatorDeposit
		if err := rows.Scan(&d.TxID, &d.TxType, &d.BlockNumber, &d.BlockTime, &d.Amount); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		deposits = append(deposits, d)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: deposits,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}
