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

func TestHandleIndexerStatus_Success(t *testing.T) {
	queryCount := 0
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			queryCount++
			// EVM chain status query
			return NewMockRows(
				[]string{"chain_id", "name", "last_block_on_chain", "last_updated", "watermark_block"},
				[][]interface{}{
					{uint32(43114), "C-Chain", uint64(12345700), time.Now(), uint32Ptr(12345698)},
					{uint32(43113), "Fuji", uint64(1000000), time.Now(), uint32Ptr(999990)},
				},
			), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					if len(dest) == 2 {
						// P-Chain query
						*dest[0].(*uint64) = 24160141
						*dest[1].(*time.Time) = time.Now()
					} else if len(dest) == 1 {
						// P-Chain latest block query
						*dest[0].(*uint64) = 24160200
					}
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleIndexerStatus_Healthy(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "name", "last_block_on_chain", "last_updated", "watermark_block"},
				[][]interface{}{
					{uint32(43114), "C-Chain", uint64(12345700), time.Now(), uint32Ptr(12345700)}, // Fully synced
				},
			), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			// P-Chain query returns 0 (no P-Chain data)
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					if len(dest) == 2 {
						*dest[0].(*uint64) = 0
						*dest[1].(*time.Time) = time.Time{}
					}
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertJSONResponse(t, w, http.StatusOK)

	status := ParseResponse[IndexerStatus](t, w)
	assert.True(t, status.Healthy, "should be healthy when caught up")
}

func TestHandleIndexerStatus_Unhealthy_EVMBehind(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "name", "last_block_on_chain", "last_updated", "watermark_block"},
				[][]interface{}{
					{uint32(43114), "C-Chain", uint64(12345700), time.Now(), uint32Ptr(12345500)}, // 200 blocks behind
				},
			), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					return ErrMockDB // No P-Chain data
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertJSONResponse(t, w, http.StatusOK)

	status := ParseResponse[IndexerStatus](t, w)
	assert.False(t, status.Healthy, "should be unhealthy when >100 blocks behind")
}

func TestHandleIndexerStatus_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleIndexerStatus_EmptyEVMStatus(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "name", "last_block_on_chain", "last_updated", "watermark_block"},
				[][]interface{}{}, // Empty result
			), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertJSONResponse(t, w, http.StatusOK)

	status := ParseResponse[IndexerStatus](t, w)
	assert.Empty(t, status.EVM)
	assert.Nil(t, status.PChain)
}

func TestHandleIndexerStatus_NoPChainData(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "name", "last_block_on_chain", "last_updated", "watermark_block"},
				[][]interface{}{
					{uint32(43114), "C-Chain", uint64(12345700), time.Now(), uint32Ptr(12345698)},
				},
			), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					// Return 0 for P-Chain block (no data)
					if len(dest) == 2 {
						*dest[0].(*uint64) = 0
						*dest[1].(*time.Time) = time.Time{}
					}
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertJSONResponse(t, w, http.StatusOK)

	status := ParseResponse[IndexerStatus](t, w)
	assert.Nil(t, status.PChain, "should not include P-Chain status when no data")
}

func TestHandleIndexerStatus_BlocksBehindCalculation(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "name", "last_block_on_chain", "last_updated", "watermark_block"},
				[][]interface{}{
					{uint32(43114), "C-Chain", uint64(100), time.Now(), uint32Ptr(95)}, // 5 blocks behind
				},
			), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			// P-Chain query returns 0 (no P-Chain data)
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					if len(dest) == 2 {
						*dest[0].(*uint64) = 0
						*dest[1].(*time.Time) = time.Time{}
					}
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/indexer/status")

	AssertJSONResponse(t, w, http.StatusOK)

	status := ParseResponse[IndexerStatus](t, w)
	require.Len(t, status.EVM, 1)
	assert.Equal(t, int64(5), status.EVM[0].BlocksBehind)
	assert.True(t, status.EVM[0].IsSynced, "should be synced when <10 blocks behind")
}

func TestHandleIndexerStatus_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/metrics/indexer/status")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// Helper function for nullable uint32 in mock rows
func uint32Ptr(v uint32) *uint32 {
	return &v
}
