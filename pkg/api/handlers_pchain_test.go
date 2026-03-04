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

func TestHandleListPChainTxs_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"tx_id", "tx_type", "block_number", "block_time", "tx_data"},
				[][]interface{}{
					{"tx123", "ConvertSubnetToL1Tx", uint64(12345678), time.Now(), map[string]interface{}{"SubnetID": "subnet123"}},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleListPChainTxs_FilterByTxType(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "tx_type = ?")
			assert.Equal(t, "ConvertSubnetToL1Tx", args[0])
			return NewMockRows([]string{"tx_id", "tx_type", "block_number", "block_time", "tx_data"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs?tx_type=ConvertSubnetToL1Tx")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListPChainTxs_FilterBySubnetID(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "tx_data.Subnet = ?")
			return NewMockRows([]string{"tx_id", "tx_type", "block_number", "block_time", "tx_data"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs?subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListPChainTxs_FilterBoth(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "tx_type = ?")
			require.Contains(t, query, "tx_data.Subnet = ?")
			return NewMockRows([]string{"tx_id", "tx_type", "block_number", "block_time", "tx_data"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs?tx_type=ConvertSubnetToL1Tx&subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListPChainTxs_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListPChainTxs_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			assert.Equal(t, 41, args[len(args)-2]) // fetchLimit = limit+1
			assert.Equal(t, 80, args[len(args)-1])
			return NewMockRows([]string{"tx_id", "tx_type", "block_number", "block_time", "tx_data"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs?limit=40&offset=80")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 40, resp.Meta.Limit)
	assert.Equal(t, 80, resp.Meta.Offset)
}

func TestHandleGetPChainTx_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			assert.Equal(t, "tx123", args[0])
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*string) = "tx123"
					*dest[1].(*string) = "ConvertSubnetToL1Tx"
					*dest[2].(*uint64) = 12345678
					*dest[3].(*time.Time) = time.Now()
					*dest[4].(*map[string]interface{}) = map[string]interface{}{"SubnetID": "subnet123"}
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs/tx123")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleGetPChainTx_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/txs/nonexistent")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

func TestHandlePChainTxTypes_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "GROUP BY tx_type")
			return NewMockRows(
				[]string{"tx_type", "count"},
				[][]interface{}{
					{"ConvertSubnetToL1Tx", uint64(100)},
					{"RegisterL1ValidatorTx", uint64(50)},
					{"IncreaseL1ValidatorBalanceTx", uint64(200)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/tx-types")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandlePChainTxTypes_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/tx-types")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListPChainTxs_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/data/pchain/txs")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandleGetPChainTx_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "DELETE", "/api/v1/data/pchain/txs/tx123")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
