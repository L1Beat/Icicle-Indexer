package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// MetricDataPoint represents a single metric value at a point in time
type MetricDataPoint struct {
	Period time.Time `json:"period"`
	Value  uint64    `json:"value" example:"1234567"`
}

// MetricSeries represents a time series for a specific metric
type MetricSeries struct {
	ChainID     uint32            `json:"chain_id" example:"43114"`
	MetricName  string            `json:"metric_name" example:"tx_count"`
	Granularity string            `json:"granularity" example:"day"`
	Data        []MetricDataPoint `json:"data"`
}

// AvailableMetric represents a metric that has data
type AvailableMetric struct {
	MetricName    string    `json:"metric_name" example:"tx_count"`
	Granularities []string  `json:"granularities" example:"hour,day,week"`
	LatestPeriod  time.Time `json:"latest_period"`
	DataPoints    uint64    `json:"data_points" example:"365"`
}

// FeeStats represents L1 validation fee statistics
type FeeStats struct {
	SubnetID        string `json:"subnet_id" example:"2XDnKyAEr..."`
	TotalDeposited  uint64 `json:"total_deposited" example:"100000000000"`
	InitialDeposits uint64 `json:"initial_deposits" example:"80000000000"`
	TopUpDeposits   uint64 `json:"top_up_deposits" example:"20000000000"`
	TotalRefunded   uint64 `json:"total_refunded" example:"10000000000"`
	CurrentBalance  uint64 `json:"current_balance" example:"85000000000"`
	TotalFeesPaid   uint64 `json:"total_fees_paid" example:"5000000000"`
	DepositTxCount  uint32 `json:"deposit_tx_count" example:"15"`
	ValidatorCount  uint32 `json:"validator_count" example:"5"`
}

// ChainMetrics represents aggregate statistics for a chain
type ChainMetrics struct {
	ChainID       uint32    `json:"chain_id" example:"43114"`
	ChainName     string    `json:"chain_name" example:"C-Chain"`
	LatestBlock   uint64    `json:"latest_block" example:"12345678"`
	TotalBlocks   uint64    `json:"total_blocks" example:"12345678"`
	TotalTxs      uint64    `json:"total_txs" example:"100000000"`
	LastBlockTime time.Time `json:"last_block_time"`
	AvgBlockTime  float64   `json:"avg_block_time_seconds" example:"2.0"`
	AvgGasUsed    float64   `json:"avg_gas_used" example:"500000"`
	TotalGasUsed  uint64    `json:"total_gas_used" example:"50000000000000"`
}

// handleFeeMetrics returns L1 validation fee statistics
// @Summary Get L1 fee statistics
// @Description Get aggregated fee statistics for L1 validators
// @Tags Metrics - Fees
// @Produce json
// @Param subnet_id query string false "Filter by subnet ID"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]FeeStats,meta=Meta}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/fees [get]
func (s *Server) handleFeeMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	wantCount := getCountParam(r)

	subnetID := r.URL.Query().Get("subnet_id")

	fetchLimit := limit + 1

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
		args = []interface{}{fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
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
			writeInternalError(w, err.Error())
			return
		}
		stats = append(stats, f)
	}

	stats, hasMore := trimResults(stats, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}

	if wantCount && subnetID == "" {
		var total int64
		_ = s.conn.QueryRow(ctx, `SELECT toInt64(count()) FROM l1_fee_stats FINAL`).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: stats,
		Meta: meta,
	})
}

// handleChainMetrics returns aggregate statistics for a chain
// @Summary Get chain statistics
// @Description Get aggregate statistics (block count, tx count, gas usage) for a chain
// @Tags Metrics - Chain
// @Produce json
// @Param chainId path int true "Chain ID"
// @Success 200 {object} Response{data=ChainMetrics}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/metrics/evm/{chainId}/stats [get]
func (s *Server) handleChainMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
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
			toUInt64(max(block_number)) as latest_block,
			toUInt64(count()) as total_blocks,
			max(block_time) as last_block_time,
			ifNotFinite(avg(gas_used), 0) as avg_gas_used,
			toUInt64(sum(gas_used)) as total_gas_used
		FROM raw_blocks
		WHERE chain_id = ?
	`, chainID).Scan(&m.LatestBlock, &m.TotalBlocks, &m.LastBlockTime, &m.AvgGasUsed, &m.TotalGasUsed)

	if err != nil {
		slog.Error("Chain stats query failed", "chain_id", chainID, "error", err)
		writeNotFoundError(w, "Chain")
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
			toFloat64(dateDiff('millisecond', min(block_time), max(block_time))) / 1000.0 / count() as avg_block_time
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
// @Summary List available metrics
// @Description Get a list of available time series metrics for a chain
// @Tags Metrics - Chain
// @Produce json
// @Param chainId path int true "Chain ID"
// @Success 200 {object} Response{data=[]AvailableMetric}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/evm/{chainId}/timeseries [get]
func (s *Server) handleListMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
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
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	metrics := []AvailableMetric{}
	for rows.Next() {
		var m AvailableMetric
		if err := rows.Scan(&m.MetricName, &m.Granularities, &m.LatestPeriod, &m.DataPoints); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		metrics = append(metrics, m)
	}

	writeJSON(w, http.StatusOK, Response{Data: metrics})
}

// handleGetMetric returns time series data for a specific metric
// @Summary Get metric time series
// @Description Get time series data for a specific metric with configurable granularity
// @Tags Metrics - Chain
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param metric path string true "Metric name (e.g., tx_count, gas_used)"
// @Param granularity query string false "Time granularity (hour, day, week, month)" default(day)
// @Param from query string false "Start time (date, RFC3339, or unix timestamp)"
// @Param to query string false "End time (date, RFC3339, or unix timestamp)"
// @Param limit query int false "Number of data points (max 1000)" default(100)
// @Success 200 {object} Response{data=MetricSeries}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/evm/{chainId}/timeseries/{metric} [get]
func (s *Server) handleGetMetric(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	metricName := r.PathValue("metric")
	if metricName == "" {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Metric name required")
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
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid granularity (use: hour, day, week, month)")
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
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	data := []MetricDataPoint{}
	for rows.Next() {
		var dp MetricDataPoint
		if err := rows.Scan(&dp.Period, &dp.Value); err != nil {
			writeInternalError(w, err.Error())
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

// DailyFeeBurn represents the total fee burn for a single day
type DailyFeeBurn struct {
	Date             string                  `json:"date" example:"2025-01-15"`
	TotalFeesBurned  uint64                  `json:"total_fees_burned" example:"44236800"`
	ActiveValidators uint32                  `json:"active_validators" example:"5"`
	Validators       []ValidatorDailyFeeBurn `json:"validators,omitempty"`
}

// ValidatorDailyFeeBurn represents a single validator's fee burn for a day
type ValidatorDailyFeeBurn struct {
	ValidationID string `json:"validation_id" example:"2ZW6HUePB..."`
	NodeID       string `json:"node_id" example:"NodeID-P7oB2McjBGgW..."`
	FeesBurned   uint64 `json:"fees_burned" example:"44236800"`
	ActiveSecs   uint64 `json:"active_seconds" example:"86400"`
}

// handleDailyFeeBurn returns daily fee burn data for an L1 subnet
// @Summary Get daily fee burn
// @Description Get daily L1 validation fee burn computed from validator active periods (512 nAVAX/sec per validator)
// @Tags Metrics - Fees
// @Produce json
// @Param subnet_id query string true "Subnet ID"
// @Param days query int false "Number of days (max 365)" default(90)
// @Param validators query string false "Include per-validator breakdown (true/false)"
// @Success 200 {object} Response{data=[]DailyFeeBurn}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/fees/daily [get]
func (s *Server) handleDailyFeeBurn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	subnetID := r.URL.Query().Get("subnet_id")
	if subnetID == "" {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "subnet_id is required")
		return
	}

	days := 90
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}
	if days > 365 {
		days = 365
	}

	includeValidators := r.URL.Query().Get("validators") == "true"

	// Query: for each day, for each validator, compute active seconds on that day.
	// A validator is active from start_time until it gets disabled (refund block_time) or until now if still active.
	// Fee burn = active_seconds * 512 nAVAX/sec
	query := `
		WITH
		dates AS (
			SELECT toDate(now()) - number AS day
			FROM numbers(?)
		),
		validators AS (
			SELECT
				v.validation_id,
				v.node_id,
				v.start_time,
				if(v.active = true,
					now(),
					coalesce(r.disable_time, v.start_time)
				) AS end_time
			FROM l1_validator_state FINAL v
			LEFT JOIN (
				SELECT validation_id, subnet_id, max(block_time) AS disable_time
				FROM l1_validator_refunds FINAL
				GROUP BY validation_id, subnet_id
			) r ON r.validation_id = v.validation_id AND r.subnet_id = v.subnet_id
			WHERE v.subnet_id = ?
		)
		SELECT
			d.day,
			v.validation_id,
			v.node_id,
			toUInt64(greatest(0,
				dateDiff('second',
					greatest(v.start_time, toDateTime64(d.day, 3, 'UTC')),
					least(v.end_time, toDateTime64(d.day + 1, 3, 'UTC'))
				)
			)) AS active_seconds
		FROM dates d
		CROSS JOIN validators v
		WHERE v.start_time < toDateTime64(d.day + 1, 3, 'UTC')
		  AND v.end_time > toDateTime64(d.day, 3, 'UTC')
		ORDER BY d.day ASC, active_seconds DESC
	`

	rows, err := s.conn.Query(ctx, query, days, subnetID)
	if err != nil {
		slog.Error("Daily fee burn query failed", "subnet_id", subnetID, "error", err)
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	// Aggregate results by day
	type dayKey = string
	dayMap := make(map[dayKey]*DailyFeeBurn)
	dayOrder := []dayKey{}

	for rows.Next() {
		var day time.Time
		var validationID, nodeID string
		var activeSecs uint64

		if err := rows.Scan(&day, &validationID, &nodeID, &activeSecs); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		dateStr := day.Format("2006-01-02")
		feesBurned := activeSecs * 512 // 512 nAVAX/sec

		entry, ok := dayMap[dateStr]
		if !ok {
			entry = &DailyFeeBurn{Date: dateStr}
			dayMap[dateStr] = entry
			dayOrder = append(dayOrder, dateStr)
		}

		entry.TotalFeesBurned += feesBurned
		entry.ActiveValidators++

		if includeValidators {
			entry.Validators = append(entry.Validators, ValidatorDailyFeeBurn{
				ValidationID: validationID,
				NodeID:       nodeID,
				FeesBurned:   feesBurned,
				ActiveSecs:   activeSecs,
			})
		}
	}

	// Build ordered result
	result := make([]DailyFeeBurn, 0, len(dayOrder))
	for _, d := range dayOrder {
		result = append(result, *dayMap[d])
	}

	writeJSON(w, http.StatusOK, Response{Data: result})
}
