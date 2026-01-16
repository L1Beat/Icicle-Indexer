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

func TestHandleListBlocks_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"},
				[][]interface{}{
					{uint32(43114), uint32(12345678), [32]byte{0x12, 0x34}, [32]byte{0xab, 0xcd}, time.Now(), [20]byte{0x74}, uint32(1024), uint32(8000000), uint32(500000), uint64(25000000000), uint32(150)},
					{uint32(43114), uint32(12345677), [32]byte{0x56, 0x78}, [32]byte{0xef, 0x01}, time.Now(), [20]byte{0x75}, uint32(2048), uint32(8000000), uint32(600000), uint64(26000000000), uint32(200)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	var resp Response
	resp = ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
	assert.Equal(t, 20, resp.Meta.Limit)
	assert.Equal(t, 0, resp.Meta.Offset)
}

func TestHandleListBlocks_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/invalid/blocks")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleListBlocks_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListBlocks_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// Verify pagination params are passed correctly
			require.Len(t, args, 3) // chainID, limit, offset
			assert.Equal(t, uint32(43114), args[0])
			assert.Equal(t, 50, args[1])
			assert.Equal(t, 100, args[2])
			return NewMockRows([]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?limit=50&offset=100")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 50, resp.Meta.Limit)
	assert.Equal(t, 100, resp.Meta.Offset)
}

func TestHandleListBlocks_LimitCapped(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// Verify limit is capped at 100 when requesting more than 100
			limit := args[1].(int)
			assert.LessOrEqual(t, limit, 100, "limit should be capped at 100")
			return NewMockRows([]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?limit=500")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.LessOrEqual(t, resp.Meta.Limit, 100, "limit in response should be capped at 100")
}

func TestHandleGetBlock_Success(t *testing.T) {
	blockTime := time.Now()
	queryCount := 0
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			queryCount++
			if queryCount == 1 {
				// Block data query
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*uint32) = 43114
						*dest[1].(*uint32) = 12345678
						*dest[2].(*[32]byte) = [32]byte{0x12, 0x34}
						*dest[3].(*[32]byte) = [32]byte{0xab, 0xcd}
						*dest[4].(*time.Time) = blockTime
						*dest[5].(*[20]byte) = [20]byte{0x74}
						*dest[6].(*uint32) = 1024
						*dest[7].(*uint32) = 8000000
						*dest[8].(*uint32) = 500000
						*dest[9].(*uint64) = 25000000000
						return nil
					},
				}
			}
			// Tx count query
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*uint64) = 150
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks/12345678")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleGetBlock_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks/99999999")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

func TestHandleGetBlock_InvalidBlockNumber(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks/invalid")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleGetBlock_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/invalid/blocks/12345678")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleListBlocks_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/data/evm/43114/blocks")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandleGetBlock_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "DELETE", "/api/v1/data/evm/43114/blocks/12345678")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
