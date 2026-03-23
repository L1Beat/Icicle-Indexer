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

func TestHandleListChains_Success(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{
					"chain_id", "chain_name", "vm_id", "created_block", "created_time",
					"subnet_id", "subnet_type", "converted_block", "converted_time",
					"name", "description", "logo_url", "website_url",
					"validator_count", "total_fees_paid",
					"active_validators", "total_staked",
				},
				[][]interface{}{
					{
						"chain123", "My Chain", "subnetevm", uint64(12345678), time.Now(),
						"subnet123", "l1", uint64(12345700), time.Now(),
						stringPtr("My L1"), stringPtr("Description"), stringPtr("https://logo.png"), stringPtr("https://website.com"),
						uint32Ptr(5), uint64Ptr(1000000),
						uint32Ptr(3), uint64Ptr(500000),
					},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)
}

func TestHandleListChains_FilterBySubnetType(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "s.subnet_type = ?")
			assert.Equal(t, "l1", args[0])
			return NewMockRows(
				[]string{
					"chain_id", "chain_name", "vm_id", "created_block", "created_time",
					"subnet_id", "subnet_type", "converted_block", "converted_time",
					"name", "description", "logo_url", "website_url",
					"validator_count", "total_fees_paid",
					"active_validators", "total_staked",
				},
				[][]interface{}{},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?subnet_type=l1")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_FilterBySubnetTypeLegacy(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "s.subnet_type = ?")
			assert.Equal(t, "legacy", args[0])
			return NewMockRows(
				[]string{
					"chain_id", "chain_name", "vm_id", "created_block", "created_time",
					"subnet_id", "subnet_type", "converted_block", "converted_time",
					"name", "description", "logo_url", "website_url",
					"validator_count", "total_fees_paid",
					"active_validators", "total_staked",
				},
				[][]interface{}{},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?subnet_type=legacy")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_FilterBySubnetID(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "c.subnet_id = ?")
			assert.Equal(t, "subnet123", args[0])
			return NewMockRows(
				[]string{
					"chain_id", "chain_name", "vm_id", "created_block", "created_time",
					"subnet_id", "subnet_type", "converted_block", "converted_time",
					"name", "description", "logo_url", "website_url",
					"validator_count", "total_fees_paid",
					"active_validators", "total_staked",
				},
				[][]interface{}{},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_LegacyChainOmitsL1Fields(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(
				[]string{
					"chain_id", "chain_name", "vm_id", "created_block", "created_time",
					"subnet_id", "subnet_type", "converted_block", "converted_time",
					"name", "description", "logo_url", "website_url",
					"validator_count", "total_fees_paid",
					"active_validators", "total_staked",
				},
				[][]interface{}{
					{
						"chain456", "Legacy Chain", "subnetevm", uint64(11111111), time.Now(),
						"subnet456", "legacy", uint64(0), time.Time{},
						(*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil),
						(*uint32)(nil), (*uint64)(nil),
						(*uint32)(nil), (*uint64)(nil),
					},
				},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains")

	AssertJSONResponse(t, w, http.StatusOK)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)

	// Verify the response doesn't contain L1-specific fields for legacy chains
	dataList, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, dataList, 1)

	chain, ok := dataList[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "legacy", chain["subnet_type"])
	assert.Empty(t, chain["name"])
	assert.Empty(t, chain["logo_url"])
	assert.Nil(t, chain["validator_count"])
	assert.Nil(t, chain["active_validators"])
	assert.Nil(t, chain["total_staked"])
	assert.Nil(t, chain["total_fees_paid"])
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

func TestHandleListChains_Pagination(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			// Last two args should be fetchLimit and offset
			assert.Equal(t, 26, args[len(args)-2]) // fetchLimit = limit+1
			assert.Equal(t, 50, args[len(args)-1]) // offset
			return NewMockRows(
				[]string{
					"chain_id", "chain_name", "vm_id", "created_block", "created_time",
					"subnet_id", "subnet_type", "converted_block", "converted_time",
					"name", "description", "logo_url", "website_url",
					"validator_count", "total_fees_paid",
					"active_validators", "total_staked",
				},
				[][]interface{}{},
			), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?limit=25&offset=50")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 25, resp.Meta.Limit)
	assert.Equal(t, 50, resp.Meta.Offset)
}

// Helper functions for nullable types in mock rows
func stringPtr(s string) *string {
	return &s
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}
