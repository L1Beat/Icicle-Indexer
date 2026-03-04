package api

import (
	"context"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleListTxs_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "hash", "block_number", "block_time", "transaction_index", "from", "to", "value", "gas_limit", "gas_price", "gas_used", "success", "type"},
				[][]interface{}{
					{uint32(43114), [32]byte{0x12}, uint32(12345678), time.Now(), uint16(0), [20]byte{0x74}, []byte{0x75}, *big.NewInt(1000000), uint32(21000), uint64(25000000000), uint32(21000), true, uint8(2)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleListTxs_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/invalid/txs")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleListTxs_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListTxs_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Len(t, args, 3)
			assert.Equal(t, uint32(43114), args[0])
			assert.Equal(t, 26, args[1]) // fetchLimit = limit+1
			assert.Equal(t, 50, args[2])
			return NewMockRows([]string{"chain_id", "hash", "block_number", "block_time", "transaction_index", "from", "to", "value", "gas_limit", "gas_price", "gas_used", "success", "type"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs?limit=25&offset=50")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 25, resp.Meta.Limit)
	assert.Equal(t, 50, resp.Meta.Offset)
}

func TestHandleGetTx_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*uint32) = 43114
					*dest[1].(*[32]byte) = [32]byte{0x12, 0x34}
					*dest[2].(*uint32) = 12345678
					*dest[3].(*time.Time) = time.Now()
					*dest[4].(*uint16) = 0
					*dest[5].(*[20]byte) = [20]byte{0x74}
					*dest[6].(*[]byte) = []byte{0x75}
					*dest[7].(*big.Int) = *big.NewInt(1000000000000000000)
					*dest[8].(*uint32) = 21000
					*dest[9].(*uint64) = 25000000000
					*dest[10].(*uint32) = 21000
					*dest[11].(*bool) = true
					*dest[12].(*uint8) = 2
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs/0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleGetTx_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs/0x0000000000000000000000000000000000000000000000000000000000000000")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

func TestHandleGetTx_InvalidHash(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs/invalidhash")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleGetTx_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/invalid/txs/0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleAddressTxs_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "hash", "block_number", "block_time", "transaction_index", "from", "to", "value", "gas_limit", "gas_price", "gas_used", "success", "type"},
				[][]interface{}{
					{uint32(43114), [32]byte{0x12}, uint32(12345678), time.Now(), uint16(0), [20]byte{0x74}, []byte{0x75}, *big.NewInt(1000000), uint32(21000), uint64(25000000000), uint32(21000), true, uint8(2)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/address/0x742d35Cc6634C0532925a3b844Bc9e7595f0A521/txs")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleAddressTxs_InvalidAddress(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/address/invalidaddress/txs")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleAddressTxs_InvalidChainID(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/invalid/address/0x742d35Cc6634C0532925a3b844Bc9e7595f0A521/txs")

	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleAddressTxs_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/address/0x742d35Cc6634C0532925a3b844Bc9e7595f0A521/txs")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleAddressTxs_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// UNION ALL args: chainID, addrHex, innerLimit, chainID, addrHex, addrHex, innerLimit, fetchLimit, offset
			require.Len(t, args, 9)
			assert.Equal(t, 11, args[7]) // fetchLimit = limit+1
			assert.Equal(t, 20, args[8])
			return NewMockRows([]string{"chain_id", "hash", "block_number", "block_time", "transaction_index", "from", "to", "value", "gas_limit", "gas_price", "gas_used", "success", "type"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/address/0x742d35Cc6634C0532925a3b844Bc9e7595f0A521/txs?limit=10&offset=20")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 10, resp.Meta.Limit)
	assert.Equal(t, 20, resp.Meta.Offset)
}

func TestHandleListTxs_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/data/evm/43114/txs")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
