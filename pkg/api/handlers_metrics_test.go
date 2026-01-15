package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleFeeMetrics_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"subnet_id", "total_deposited", "initial_deposits", "top_up_deposits", "total_refunded", "current_balance", "total_fees_paid", "deposit_tx_count", "validator_count"},
				[][]interface{}{
					{"subnet123", uint64(100000000000), uint64(80000000000), uint64(20000000000), uint64(10000000000), uint64(85000000000), uint64(5000000000), uint32(15), uint32(5)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/fees")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
}

func TestHandleFeeMetrics_FilterBySubnetID(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "subnet_id = ?")
			assert.Equal(t, "subnet123", args[0])
			return NewMockRows([]string{"subnet_id", "total_deposited", "initial_deposits", "top_up_deposits", "total_refunded", "current_balance", "total_fees_paid", "deposit_tx_count", "validator_count"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/fees?subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleFeeMetrics_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/fees")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleChainMetrics_Success(t *testing.T) {
	queryCount := 0
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			queryCount++
			switch queryCount {
			case 1: // Chain name query
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "C-Chain"
						return nil
					},
				}
			case 2: // Block stats query
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*uint64) = 12345678
						*dest[1].(*uint64) = 12345678
						*dest[2].(*time.Time) = time.Now()
						*dest[3].(*float64) = 500000.0
						*dest[4].(*uint64) = 50000000000000
						return nil
					},
				}
			case 3: // Tx count query
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*uint64) = 100000000
						return nil
					},
				}
			default: // Avg block time query
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*float64) = 2.0
						return nil
					},
				}
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/stats")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleChainMetrics_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/invalid/stats")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleChainMetrics_NotFound(t *testing.T) {
	queryCount := 0
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			queryCount++
			if queryCount == 1 {
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						return nil // No chain name
					},
				}
			}
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/99999/stats")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

func TestHandleListMetrics_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"metric_name", "granularities", "latest_period", "data_points"},
				[][]interface{}{
					{"tx_count", []string{"hour", "day", "week"}, time.Now(), uint64(365)},
					{"gas_used", []string{"day", "week"}, time.Now(), uint64(100)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleListMetrics_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/invalid/timeseries")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleListMetrics_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleGetMetric_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"period", "value"},
				[][]interface{}{
					{time.Now().Add(-24 * time.Hour), uint64(1000)},
					{time.Now(), uint64(1200)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries/tx_count")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleGetMetric_WithGranularity(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			assert.Equal(t, "hour", args[2])
			return NewMockRows([]string{"period", "value"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries/tx_count?granularity=hour")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleGetMetric_InvalidGranularity(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries/tx_count?granularity=invalid")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleGetMetric_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/invalid/timeseries/tx_count")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleGetMetric_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries/tx_count")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleGetMetric_WithTimeRange(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "period >= ?")
			require.Contains(t, query, "period <= ?")
			return NewMockRows([]string{"period", "value"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries/tx_count?from=2025-01-01&to=2025-01-31")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleGetMetric_LimitCapped(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// Verify limit is capped at 1000 when requesting more than 1000
			limit := args[len(args)-1].(int)
			assert.LessOrEqual(t, limit, 1000, "limit should be capped at 1000")
			return NewMockRows([]string{"period", "value"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/evm/43114/timeseries/tx_count?limit=5000")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleFeeMetrics_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/metrics/fees")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
