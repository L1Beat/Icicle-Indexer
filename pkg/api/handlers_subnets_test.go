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

func TestHandleListSubnets_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"subnet_id", "subnet_type", "created_block", "created_time", "chain_id", "converted_block", "converted_time"},
				[][]interface{}{
					{"2XDnKyAEr123", "l1", uint64(12345678), time.Now(), "chain123", uint64(12345700), time.Now()},
					{"2XDnKyAEr456", "regular", uint64(12345670), time.Now(), "", uint64(0), time.Time{}},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/subnets")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleListSubnets_FilterByType(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "subnet_type = ?")
			assert.Equal(t, "l1", args[0])
			return NewMockRows([]string{"subnet_id", "subnet_type", "created_block", "created_time", "chain_id", "converted_block", "converted_time"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/subnets?type=l1")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListSubnets_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/subnets")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListSubnets_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			assert.Equal(t, 25, args[len(args)-2])
			assert.Equal(t, 50, args[len(args)-1])
			return NewMockRows([]string{"subnet_id", "subnet_type", "created_block", "created_time", "chain_id", "converted_block", "converted_time"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/subnets?limit=25&offset=50")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 25, resp.Meta.Limit)
	assert.Equal(t, 50, resp.Meta.Offset)
}

func TestHandleGetSubnet_Success(t *testing.T) {
	queryCount := 0
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			if queryCount == 0 {
				queryCount++
				return &MockRow{
					scanFunc: func(dest ...interface{}) error {
						*dest[0].(*string) = "2XDnKyAEr123"
						*dest[1].(*string) = "l1"
						*dest[2].(*uint64) = 12345678
						*dest[3].(*time.Time) = time.Now()
						*dest[4].(*string) = "chain123"
						*dest[5].(*uint64) = 12345700
						*dest[6].(*time.Time) = time.Now()
						return nil
					},
				}
			}
			// Registry query
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*string) = "2XDnKyAEr123"
					*dest[1].(*string) = "My L1"
					*dest[2].(*string) = "A description"
					*dest[3].(*string) = "https://logo.png"
					*dest[4].(*string) = "https://website.com"
					return nil
				},
			}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "subnet_id", "chain_name", "vm_id", "created_block", "created_time"},
				[][]interface{}{
					{"chain123", "2XDnKyAEr123", "My Chain", "subnetevm", uint64(12345678), time.Now()},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/subnets/2XDnKyAEr123")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleGetSubnet_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{err: ErrMockDB}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/subnets/nonexistent")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

func TestHandleListL1s_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "subnet_type = 'l1'")
			return NewMockRows(
				[]string{"subnet_id", "created_block", "created_time", "chain_id", "converted_block", "converted_time", "name", "description", "logo_url", "website_url"},
				[][]interface{}{
					{"subnet123", uint64(12345678), time.Now(), "chain123", uint64(12345700), time.Now(), stringPtr("My L1"), stringPtr("Description"), stringPtr("https://logo.png"), stringPtr("https://website.com")},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/l1s")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleListL1s_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/l1s")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListChains_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{"chain_id", "subnet_id", "chain_name", "vm_id", "created_block", "created_time"},
				[][]interface{}{
					{"chain123", "subnet123", "My Chain", "subnetevm", uint64(12345678), time.Now()},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)
}

func TestHandleListChains_FilterBySubnetID(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "subnet_id = ?")
			assert.Equal(t, "subnet123", args[0])
			return NewMockRows([]string{"chain_id", "subnet_id", "chain_name", "vm_id", "created_block", "created_time"}, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_DatabaseError(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return nil, ErrMockDB
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains")

	AssertErrorResponse(t, w, http.StatusInternalServerError, ErrInternalError)
}

func TestHandleListSubnets_MethodNotAllowed(t *testing.T) {
	mock := &MockConn{}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "POST", "/api/v1/data/subnets")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// Helper function for nullable strings in mock rows
func stringPtr(s string) *string {
	return &s
}
