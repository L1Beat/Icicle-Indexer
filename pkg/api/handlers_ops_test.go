package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

func TestHandleStorageStats_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "system.parts")
			return NewMockRows(
				[]string{"table", "size_bytes", "rows"},
				[][]interface{}{
					{"raw_txs", uint64(123456789), uint64(1000000000)},
					{"p_chain_txs", uint64(2048), uint64(50000)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeAuthedRequest(t, server, "GET", "/api/v1/metrics/storage")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
}

func TestHandleStorageStats_RequiresToken(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			t.Fatal("handler should not run without a valid bearer token")
			return nil, nil
		},
	}
	server := NewTestServer(mock)
	// No Authorization header -> rejected before the handler runs.
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/storage")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleStorageStats_DBError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}
	server := NewTestServer(mock)
	w := MakeAuthedRequest(t, server, "GET", "/api/v1/metrics/storage")
	require.Equal(t, http.StatusInternalServerError, w.Code)
}
