package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var validatorColumns = []string{"subnet_id", "validation_id", "node_id", "balance", "weight", "start_time", "end_time", "uptime_percentage", "active", "initial_deposit", "total_topups", "refund_amount", "fees_paid", "created_tx_id", "created_tx_type", "created_block", "created_time", "bls_public_key", "remaining_balance_owner", "primary_stake", "primary_uptime"}

func TestHandleListValidators_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			// Subnet type check returns empty (not legacy)
			return &MockRow{err: ErrMockDB}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				validatorColumns,
				[][]interface{}{
					{"2XDnKyAEr123", "2ZW6HUePB456", "NodeID-P7oB2McjBGgW", uint64(100000000000), uint64(2000), time.Now(), time.Now().Add(24 * time.Hour), float64(99.5), true, uint64(100000000000), uint64(50000000000), uint64(0), uint64(5000000000), "tx123", "RegisterL1ValidatorTx", uint64(12345678), time.Now(), "0x8ea73dd040", "avax1abc123", uint64(0), float64(0)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleListValidators_FilterBySubnetID(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			// Subnet type check
			if strings.Contains(query, "subnet_type") {
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "l1"
						return nil
					},
				}
			}
			return &MockRow{err: ErrMockDB}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "subnet_id = ?")
			assert.Equal(t, "subnet123", args[0])
			return NewMockRows(validatorColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators?subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListValidators_FilterByActive(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "active = true")
			return NewMockRows(validatorColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators?active=true")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListValidators_FilterBoth(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "subnet_id = ?")
			require.Contains(t, query, "active = true")
			return NewMockRows(validatorColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators?subnet_id=subnet123&active=true")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListValidators_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListValidators_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// Last two args should be fetchLimit and offset
			assert.Equal(t, 31, args[len(args)-2]) // fetchLimit = limit+1
			assert.Equal(t, 60, args[len(args)-1])
			return NewMockRows(validatorColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators?limit=30&offset=60")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 30, resp.Meta.Limit)
	assert.Equal(t, 60, resp.Meta.Offset)
}

func TestHandleGetValidator_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			if strings.Contains(query, "v.validation_id") {
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "2XDnKyAEr123"
						*dest[1].(*string) = "2ZW6HUePB456"
						*dest[2].(*string) = "NodeID-P7oB2McjBGgW"
						*dest[3].(*uint64) = 100000000000
						*dest[4].(*uint64) = 2000
						*dest[5].(*time.Time) = time.Now()
						*dest[6].(*time.Time) = time.Now().Add(24 * time.Hour)
						*dest[7].(*float64) = 99.5
						*dest[8].(*bool) = true
						*dest[9].(*uint64) = 100000000000
						*dest[10].(*uint64) = 50000000000
						*dest[11].(*uint64) = 0
						*dest[12].(*uint64) = 5000000000
						*dest[13].(*string) = "tx123"
						*dest[14].(*string) = "RegisterL1ValidatorTx"
						*dest[15].(*uint64) = 12345678
						*dest[16].(*time.Time) = time.Now()
						*dest[17].(*string) = "0x8ea73dd040"
						*dest[18].(*string) = "avax1abc123"
						return nil
					},
				}
			}
			if strings.Contains(query, "subnet_type") {
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "l1"
						return nil
					},
				}
			}
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/2ZW6HUePB456")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleGetValidator_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/nonexistent")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

func TestHandleGetValidator_CanSearchByNodeID(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			if strings.Contains(query, "v.validation_id = ? OR v.node_id = ?") {
				assert.Equal(t, "NodeID-P7oB2McjBGgW", args[0])
				assert.Equal(t, "NodeID-P7oB2McjBGgW", args[1])
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "2XDnKyAEr123"
						*dest[1].(*string) = "2ZW6HUePB456"
						*dest[2].(*string) = "NodeID-P7oB2McjBGgW"
						*dest[3].(*uint64) = 100000000000
						*dest[4].(*uint64) = 2000
						*dest[5].(*time.Time) = time.Now()
						*dest[6].(*time.Time) = time.Now().Add(24 * time.Hour)
						*dest[7].(*float64) = 99.5
						*dest[8].(*bool) = true
						*dest[9].(*uint64) = 100000000000
						*dest[10].(*uint64) = 50000000000
						*dest[11].(*uint64) = 0
						*dest[12].(*uint64) = 5000000000
						*dest[13].(*string) = "tx123"
						*dest[14].(*string) = "RegisterL1ValidatorTx"
						*dest[15].(*uint64) = 12345678
						*dest[16].(*time.Time) = time.Now()
						*dest[17].(*string) = "0x8ea73dd040"
						*dest[18].(*string) = "avax1abc123"
						return nil
					},
				}
			}
			if strings.Contains(query, "subnet_type") {
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "l1"
						return nil
					},
				}
			}
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/NodeID-P7oB2McjBGgW")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleGetValidator_LegacySubnetEnriched(t *testing.T) {
	callCount := 0
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if strings.Contains(query, "v.validation_id = ? OR v.node_id = ?") {
				// Return a legacy subnet validator
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "2MbQjnTg3yxE"
						*dest[1].(*string) = "val123"
						*dest[2].(*string) = "NodeID-Test"
						*dest[3].(*uint64) = 0
						*dest[4].(*uint64) = 20
						*dest[5].(*time.Time) = time.Now()
						*dest[6].(*time.Time) = time.Now().Add(24 * time.Hour)
						*dest[7].(*float64) = 0
						*dest[8].(*bool) = true
						*dest[9].(*uint64) = 0
						*dest[10].(*uint64) = 0
						*dest[11].(*uint64) = 0
						*dest[12].(*uint64) = 0
						*dest[13].(*string) = ""
						*dest[14].(*string) = ""
						*dest[15].(*uint64) = 0
						*dest[16].(*time.Time) = time.Time{}
						*dest[17].(*string) = ""
						*dest[18].(*string) = ""
						return nil
					},
				}
			}
			if strings.Contains(query, "subnet_type") {
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "legacy"
						return nil
					},
				}
			}
			if strings.Contains(query, "weight, uptime_percentage") {
				// Primary Network data
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*uint64) = 2000000000000
						*dest[1].(*float64) = 99.99
						return nil
					},
				}
			}
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/NodeID-Test")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleValidatorDeposits_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"tx_id", "tx_type", "block_number", "block_time", "amount"},
				[][]interface{}{
					{"tx123", "IncreaseL1ValidatorBalanceTx", uint64(12345678), time.Now(), uint64(10000000000)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/2ZW6HUePB456/deposits")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleValidatorDeposits_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/2ZW6HUePB456/deposits")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleValidatorDeposits_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// args: id, id, fetchLimit, offset
			require.Len(t, args, 4)
			assert.Equal(t, 16, args[2]) // fetchLimit = limit+1
			assert.Equal(t, 30, args[3])
			return NewMockRows([]string{"tx_id", "tx_type", "block_number", "block_time", "amount"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/2ZW6HUePB456/deposits?limit=15&offset=30")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 15, resp.Meta.Limit)
	assert.Equal(t, 30, resp.Meta.Offset)
}

func TestHandleListValidators_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/data/validators")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandleGetValidator_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "DELETE", "/api/v1/data/validators/123")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
