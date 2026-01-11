package api

import (
	"net/http"
	"time"
)

// MetricDataPoint represents a single metric value at a point in time
type MetricDataPoint struct {
	Period time.Time `json:"period"`
	Value  uint64    `json:"value"`
}

// MetricSeries represents a time series for a specific metric
type MetricSeries struct {
	ChainID     uint32            `json:"chain_id"`
	MetricName  string            `json:"metric_name"`
	Granularity string            `json:"granularity"`
	Data        []MetricDataPoint `json:"data"`
}

// AvailableMetric represents a metric that has data
type AvailableMetric struct {
	MetricName    string    `json:"metric_name"`
	Granularities []string  `json:"granularities"`
	LatestPeriod  time.Time `json:"latest_period"`
	DataPoints    uint64    `json:"data_points"`
}

type FeeStats struct {
	SubnetID        string `json:"subnet_id"`
	TotalDeposited  uint64 `json:"total_deposited"`
	InitialDeposits uint64 `json:"initial_deposits"`
	TopUpDeposits   uint64 `json:"top_up_deposits"`
	TotalRefunded   uint64 `json:"total_refunded"`
	CurrentBalance  uint64 `json:"current_balance"`
	TotalFeesPaid   uint64 `json:"total_fees_paid"`
	DepositTxCount  uint32 `json:"deposit_tx_count"`
	ValidatorCount  uint32 `json:"validator_count"`
}

type ChainMetrics struct {
	ChainID        uint32    `json:"chain_id"`
	ChainName      string    `json:"chain_name"`
	LatestBlock    uint64    `json:"latest_block"`
	TotalBlocks    uint64    `json:"total_blocks"`
	TotalTxs       uint64    `json:"total_txs"`
	LastBlockTime  time.Time `json:"last_block_time"`
	AvgBlockTime   float64   `json:"avg_block_time_seconds"`
	AvgGasUsed     float64   `json:"avg_gas_used"`
	TotalGasUsed   uint64    `json:"total_gas_used"`
}

func (s *Server) handleFeeMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()
	limit, offset := getPagination(r)

	subnetID := r.URL.Query().Get("subnet_id")

	var query string
	var args []interface{}

	if subnetID != "" {
		query = `
			SELECT
				subnet_id, total_deposited, initial_deposits, top_up_deposits,
				total_refunded, current_balance, total_fees_paid,
				deposit_tx_count, validator_count
			FROM l1_fee_stats FINAL
			WHERE subnet_id = ?
		`
		args = []interface{}{subnetID}
	} else {
		query = `
			SELECT
				subnet_id, total_deposited, initial_deposits, top_up_deposits,
				total_refunded, current_balance, total_fees_paid,
				deposit_tx_count, validator_count
			FROM l1_fee_stats FINAL
			ORDER BY total_fees_paid DESC
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

	stats := []FeeStats{}
	for rows.Next() {
		var f FeeStats
		if err := rows.Scan(
			&f.SubnetID, &f.TotalDeposited, &f.InitialDeposits, &f.TopUpDeposits,
			&f.TotalRefunded, &f.CurrentBalance, &f.TotalFeesPaid,
			&f.DepositTxCount, &f.ValidatorCount,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		stats = append(stats, f)
	}

	writeJSON(w, http.StatusOK, Response{
		Data: stats,
		Meta: &Meta{Limit: limit, Offset: offset},
	})
}

func (s *Server) handleChainMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	var m ChainMetrics
	m.ChainID = chainID

	// Get chain name from chain_status
	s.conn.QueryRow(ctx, `
		SELECT name FROM chain_status FINAL WHERE chain_id = ?
	`, chainID).Scan(&m.ChainName)

	// Get block stats
	err = s.conn.QueryRow(ctx, `
		SELECT
			max(block_number) as latest_block,
			count() as total_blocks,
			max(block_time) as last_block_time,
			avg(gas_used) as avg_gas_used,
			sum(gas_used) as total_gas_used
		FROM raw_blocks
		WHERE chain_id = ?
	`, chainID).Scan(&m.LatestBlock, &m.TotalBlocks, &m.LastBlockTime, &m.AvgGasUsed, &m.TotalGasUsed)

	if err != nil {
		writeError(w, http.StatusNotFound, "chain not found")
		return
	}

	// Get transaction count
	s.conn.QueryRow(ctx, `
		SELECT count() FROM raw_txs WHERE chain_id = ?
	`, chainID).Scan(&m.TotalTxs)

	// Calculate average block time (last 1000 blocks)
	var avgBlockTimeSec float64
	s.conn.QueryRow(ctx, `
		SELECT
			(max(block_time) - min(block_time)) / count() as avg_block_time
		FROM (
			SELECT block_time
			FROM raw_blocks
			WHERE chain_id = ?
			ORDER BY block_number DESC
			LIMIT 1000
		)
	`, chainID).Scan(&avgBlockTimeSec)
	m.AvgBlockTime = avgBlockTimeSec

	writeJSON(w, http.StatusOK, Response{Data: m})
}

// handleListMetrics lists available metrics for a chain
func (s *Server) handleListMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	query := `
		SELECT
			metric_name,
			groupArray(DISTINCT granularity) as granularities,
			max(period) as latest_period,
			count() as data_points
		FROM metrics FINAL
		WHERE chain_id = ?
		GROUP BY metric_name
		ORDER BY metric_name
	`

	rows, err := s.conn.Query(ctx, query, chainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	metrics := []AvailableMetric{}
	for rows.Next() {
		var m AvailableMetric
		if err := rows.Scan(&m.MetricName, &m.Granularities, &m.LatestPeriod, &m.DataPoints); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		metrics = append(metrics, m)
	}

	writeJSON(w, http.StatusOK, Response{Data: metrics})
}

// handleGetMetric returns time series data for a specific metric
func (s *Server) handleGetMetric(w http.ResponseWriter, r *http.Request) {
	ctx := s.queryContext()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}

	metricName := r.PathValue("metric")
	if metricName == "" {
		writeError(w, http.StatusBadRequest, "metric name required")
		return
	}

	// Get granularity from query params (default: day)
	granularity := r.URL.Query().Get("granularity")
	if granularity == "" {
		granularity = "day"
	}

	// Validate granularity
	validGranularities := map[string]bool{"hour": true, "day": true, "week": true, "month": true}
	if !validGranularities[granularity] {
		writeError(w, http.StatusBadRequest, "invalid granularity (use: hour, day, week, month)")
		return
	}

	// Optional time range filters (accepts: 2025-01-01, 2025-01-01T00:00:00Z, or unix timestamp)
	var fromTime, toTime time.Time
	if from := r.URL.Query().Get("from"); from != "" {
		fromTime = parseFlexibleTime(from)
	}
	if to := r.URL.Query().Get("to"); to != "" {
		toTime = parseFlexibleTime(to)
	}

	limit, _ := getPagination(r)
	if limit > 1000 {
		limit = 1000
	}

	// Build query
	query := `
		SELECT period, value
		FROM metrics FINAL
		WHERE chain_id = ?
		  AND metric_name = ?
		  AND granularity = ?
	`
	args := []interface{}{chainID, metricName, granularity}

	if !fromTime.IsZero() {
		query += " AND period >= ?"
		args = append(args, fromTime)
	}
	if !toTime.IsZero() {
		query += " AND period <= ?"
		args = append(args, toTime)
	}

	query += " ORDER BY period DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	data := []MetricDataPoint{}
	for rows.Next() {
		var dp MetricDataPoint
		if err := rows.Scan(&dp.Period, &dp.Value); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data = append(data, dp)
	}

	// Reverse to get chronological order
	for i, j := 0, len(data)-1; i < j; i, j = i+1, j-1 {
		data[i], data[j] = data[j], data[i]
	}

	result := MetricSeries{
		ChainID:     chainID,
		MetricName:  metricName,
		Granularity: granularity,
		Data:        data,
	}

	writeJSON(w, http.StatusOK, Response{Data: result})
}
