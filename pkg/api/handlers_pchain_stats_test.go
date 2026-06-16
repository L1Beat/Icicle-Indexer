package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

func TestHandlePChainStats_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				// Works for the 4-column stats query and the single-column
				// recent-tx / fees queries alike.
				for i := range dest {
					if p, ok := dest[i].(*uint64); ok {
						*p = uint64(i + 1)
					}
				}
				return nil
			}}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/stats")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
}

func TestHandleSubnetTimeline_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "toStartOfMonth")
			return NewMockRows(
				[]string{"period", "value"},
				[][]interface{}{
					{time.Now(), uint64(3)},
					{time.Now(), uint64(5)},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/subnet-timeline")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
}

func TestHandleListPChainBlocks_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "GROUP BY block_number")
			return NewMockRows(
				[]string{"block_number", "tx_count", "block_time", "block_hash"},
				[][]interface{}{
					{uint64(24160141), uint64(3), time.Now(), "blockhash123"},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/blocks")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleGetPChainBlock_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				for _, d := range dest {
					switch p := d.(type) {
					case *uint64:
						*p = 24160141
					case *time.Time:
						*p = time.Now()
					case *string:
						*p = "x"
					}
				}
				return nil
			}}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/blocks/24160141")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
}

func TestHandleGetPChainBlock_InvalidNumber(t *testing.T) {
	server := NewTestServer(&MockConn{})
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/blocks/notanumber")
	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestHandleGetPChainBlock_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/blocks/999")
	require.Equal(t, http.StatusNotFound, w.Code)
}
