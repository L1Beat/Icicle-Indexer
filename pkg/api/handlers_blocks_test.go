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
				[]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas"},
				[][]interface{}{
					{uint32(43114), uint32(12345678), [32]byte{0x12, 0x34}, [32]byte{0xab, 0xcd}, time.Now(), [20]byte{0x74}, uint32(1024), uint32(8000000), uint32(500000), uint64(25000000000)},
					{uint32(43114), uint32(12345677), [32]byte{0x56, 0x78}, [32]byte{0xef, 0x01}, time.Now(), [20]byte{0x75}, uint32(2048), uint32(8000000), uint32(600000), uint64(26000000000)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
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
			// Verify pagination params are passed correctly (fetchLimit = limit+1)
			require.Len(t, args, 3) // chainID, fetchLimit, offset
			assert.Equal(t, uint32(43114), args[0])
			assert.Equal(t, 51, args[1]) // limit+1
			assert.Equal(t, 100, args[2])
			return NewMockRows([]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?limit=50&offset=100")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 50, resp.Meta.Limit)
	assert.Equal(t, 100, resp.Meta.Offset)
	assert.False(t, resp.Meta.HasMore)
}

func TestHandleListBlocks_LimitCapped(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// Verify fetchLimit is capped at 101 (100+1) when requesting more than 100
			fetchLimit := args[1].(int)
			assert.LessOrEqual(t, fetchLimit, 101, "fetchLimit should be capped at 101 (100+1)")
			return NewMockRows([]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas"}, [][]interface{}{}), nil
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

func TestHandleListBlocks_HasMore(t *testing.T) {
	// Return 3 rows when limit=2 → has_more=true, only 2 results returned
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"},
				[][]interface{}{
					{uint32(43114), uint32(100), [32]byte{0x01}, [32]byte{0x02}, time.Now(), [20]byte{0x03}, uint32(1024), uint32(8000000), uint32(500000), uint64(25000000000), uint32(10)},
					{uint32(43114), uint32(99), [32]byte{0x04}, [32]byte{0x05}, time.Now(), [20]byte{0x06}, uint32(1024), uint32(8000000), uint32(500000), uint64(25000000000), uint32(10)},
					{uint32(43114), uint32(98), [32]byte{0x07}, [32]byte{0x08}, time.Now(), [20]byte{0x09}, uint32(1024), uint32(8000000), uint32(500000), uint64(25000000000), uint32(10)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?limit=2")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.True(t, resp.Meta.HasMore)
	assert.Equal(t, "99", resp.Meta.NextCursor)
}

func TestHandleListBlocks_NoMore(t *testing.T) {
	// Return 1 row when limit=2 → has_more=false
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"},
				[][]interface{}{
					{uint32(43114), uint32(100), [32]byte{0x01}, [32]byte{0x02}, time.Now(), [20]byte{0x03}, uint32(1024), uint32(8000000), uint32(500000), uint64(25000000000), uint32(10)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?limit=2")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.False(t, resp.Meta.HasMore)
	assert.Empty(t, resp.Meta.NextCursor)
}

func TestHandleListBlocks_Cursor(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// With cursor, args should be: chainID, cursorBlock, fetchLimit
			require.Len(t, args, 3)
			assert.Equal(t, uint32(43114), args[0])
			assert.Equal(t, uint64(99), args[1]) // cursor block
			assert.Equal(t, 21, args[2])          // fetchLimit = 20+1
			return NewMockRows([]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?cursor=99")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListBlocks_Count(t *testing.T) {
	queryCount := 0
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows([]string{"chain_id", "block_number", "hash", "parent_hash", "block_time", "miner", "size", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count"}, [][]interface{}{}), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			queryCount++
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*int64) = 12345
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks?count=true")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, int64(12345), resp.Meta.Total)
}
