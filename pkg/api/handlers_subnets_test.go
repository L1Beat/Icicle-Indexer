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

// chainColumns matches the SELECT columns in handleListChains
var chainColumns = []string{
	"chain_id", "chain_name", "vm_id", "created_block", "created_time",
	"subnet_id", "subnet_type", "converted_block", "converted_time", "validator_manager_address", "validator_manager_owner",
	"name", "description", "logo_url", "website_url",
	"evm_chain_id", "categories", "socials",
	"rpc_url", "explorer_url", "sybil_resistance_type",
	"network_token_name", "network_token_symbol", "network_token_decimals", "network_token_logo_uri",
	"network",
	"validator_count", "total_fees_paid",
	"active_validators", "total_staked", "active_weight", "nakamoto_33", "nakamoto_50",
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
						*dest[7].(*string) = "0x1234567890abcdef1234567890abcdef12345678"
						*dest[8].(*string) = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
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
			return NewMockRows(chainColumns, [][]interface{}{
				{
					"chain123", "My Chain", "subnetevm", uint64(12345678), time.Now(),
					"subnet123", "l1", uint64(12345700), time.Now(), "0x1234567890abcdef1234567890abcdef12345678", "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
					stringPtr("My L1"), stringPtr("Description"), stringPtr("https://logo.png"), stringPtr("https://website.com"),
					uint64Ptr(43114), []string{"DeFi", "Gaming"}, stringPtr(`[{"name":"twitter","url":"https://x.com/test"}]`),
					stringPtr("https://rpc.example.com"), stringPtr("https://explorer.example.com"), stringPtr("Proof of Stake"),
					stringPtr("AVAX"), stringPtr("Avalanche"), uint8Ptr(18), stringPtr("https://token-logo.png"),
					stringPtr("mainnet"),
					uint32Ptr(5), uint64Ptr(1000000),
					uint32Ptr(3), uint64Ptr(500000), uint64Ptr(500000), uint32Ptr(2), uint32Ptr(2),
				},
			}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains")

	AssertJSONResponse(t, w, http.StatusOK)
	AssertCORSHeaders(t, w)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)
	require.NotNil(t, resp.Meta)

	dataList, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, dataList, 1)

	chain, ok := dataList[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "My L1", chain["name"])
	assert.Equal(t, "l1", chain["chain_type"])
	assert.Equal(t, float64(43114), chain["evm_chain_id"])
	assert.Equal(t, "https://rpc.example.com", chain["rpc_url"])
	assert.Equal(t, "https://explorer.example.com", chain["explorer_url"])
	assert.Equal(t, true, chain["is_active"])
	assert.NotNil(t, chain["network_token"])
	assert.NotNil(t, chain["socials"])
	assert.NotNil(t, chain["categories"])
}

func TestHandleListChains_FilterBySubnetType(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "s.subnet_type = ?")
			assert.Equal(t, "l1", args[0])
			return NewMockRows(chainColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?chain_type=l1")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_FilterBySubnetTypeLegacy(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "s.subnet_type = ?")
			assert.Equal(t, "legacy", args[0])
			return NewMockRows(chainColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?chain_type=legacy")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_FilterBySubnetID(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, query, "c.subnet_id = ?")
			assert.Equal(t, "subnet123", args[0])
			return NewMockRows(chainColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?subnet_id=subnet123")

	AssertJSONResponse(t, w, http.StatusOK)
}

func TestHandleListChains_LegacyChainOmitsL1Fields(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(chainColumns, [][]interface{}{
				{
					"chain456", "Legacy Chain", "subnetevm", uint64(11111111), time.Now(),
					"subnet456", "legacy", uint64(0), time.Time{}, "", "",
					(*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil),
					(*uint64)(nil), []string(nil), (*string)(nil),
					(*string)(nil), (*string)(nil), (*string)(nil),
					(*string)(nil), (*string)(nil), (*uint8)(nil), (*string)(nil),
					(*string)(nil),
					(*uint32)(nil), (*uint64)(nil),
					(*uint32)(nil), (*uint64)(nil), (*uint64)(nil), (*uint32)(nil), (*uint32)(nil),
				},
			}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains")

	AssertJSONResponse(t, w, http.StatusOK)

	resp := ParseResponse[Response](t, w)
	require.NotNil(t, resp.Data)

	dataList, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, dataList, 1)

	chain, ok := dataList[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "legacy", chain["chain_type"])
	assert.Equal(t, false, chain["is_active"])
	assert.Empty(t, chain["name"])
	assert.Empty(t, chain["logo_url"])
	assert.Nil(t, chain["evm_chain_id"])
	assert.Nil(t, chain["validator_count"])
	assert.Nil(t, chain["active_validators"])
	assert.Nil(t, chain["total_staked"])
	assert.Nil(t, chain["total_fees_paid"])
	assert.Nil(t, chain["network_token"])
	assert.Nil(t, chain["socials"])
	assert.Nil(t, chain["categories"])
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
			assert.Equal(t, 26, args[len(args)-2]) // fetchLimit = limit+1
			assert.Equal(t, 50, args[len(args)-1]) // offset
			return NewMockRows(chainColumns, [][]interface{}{}), nil
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains?limit=25&offset=50")

	AssertJSONResponse(t, w, http.StatusOK)
	resp := ParseResponse[Response](t, w)
	assert.Equal(t, 25, resp.Meta.Limit)
	assert.Equal(t, 50, resp.Meta.Offset)
}

func TestHandleChainRisk_Success(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			require.Contains(t, query, "c.chain_id = ?")
			assert.Equal(t, "chain123", args[0])
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*string) = "chain123"
					*dest[1].(*string) = "0x1234567890abcdef1234567890abcdef12345678"
					*dest[2].(*string) = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
					*dest[3].(**uint32) = uint32Ptr(3)
					*dest[4].(**uint64) = uint64Ptr(1000000)
					*dest[5].(**uint32) = uint32Ptr(1)
					*dest[6].(**uint32) = uint32Ptr(2)
					*dest[7].(*[]uint64) = []uint64{500000, 300000, 200000}
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains/chain123/risk")

	AssertJSONResponse(t, w, http.StatusOK)

	resp := ParseResponse[Response](t, w)
	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "chain123", data["chain_id"])

	vm, ok := data["validator_manager"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "0x1234567890abcdef1234567890abcdef12345678", vm["address"])
	assert.Equal(t, "unknown", vm["type"])
	owner, ok := vm["owner"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd", owner["address"])
	assert.Equal(t, "unknown", owner["kind"])
	assert.Nil(t, vm["proxy"])
	assert.Nil(t, vm["churn"])

	dec, ok := data["decentralization"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(3), dec["active_validator_count"])
	assert.Equal(t, float64(1), dec["nakamoto_33"])
	assert.Equal(t, float64(2), dec["nakamoto_50"])
	assert.Equal(t, "1000000", dec["total_weight"])
	weights, ok := dec["weights"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"500000", "300000", "200000"}, weights)

	assert.Nil(t, data["economic"])
	assert.NotEmpty(t, data["updated_at"])
}

func TestHandleChainRisk_ValidatorManagerPopulated(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			require.Contains(t, query, "chain_risk")
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					*dest[0].(*string) = "chain123"
					*dest[1].(*string) = "0xvm0000000000000000000000000000000000000a"
					*dest[2].(*string) = "0xsubnetsowner000000000000000000000000000b"
					// no decentralization
					// chain_risk (Tier 1)
					*dest[8].(*string) = "PoS-erc20"
					*dest[9].(*string) = "c-chain"
					*dest[10].(**string) = stringPtr("0xstakingmanager00000000000000000000000c")
					*dest[11].(*string) = "multisig"
					*dest[12].(**uint16) = uint16Ptr(2)
					*dest[13].(**uint16) = uint16Ptr(5)
					*dest[14].(*bool) = true
					*dest[15].(**string) = stringPtr("0ximpl000000000000000000000000000000000d")
					*dest[16].(**string) = stringPtr("0xproxyadmin0000000000000000000000000000e")
					*dest[17].(**string) = stringPtr("0xpadminowner000000000000000000000000000f")
					*dest[18].(**uint64) = uint64Ptr(172800)
					*dest[19].(**uint64) = uint64Ptr(3600)
					*dest[20].(**uint8) = uint8Ptr(20)
					return nil
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains/chain123/risk")
	AssertJSONResponse(t, w, http.StatusOK)

	resp := ParseResponse[Response](t, w)
	data := resp.Data.(map[string]interface{})
	vm := data["validator_manager"].(map[string]interface{})
	assert.Equal(t, "0xvm0000000000000000000000000000000000000a", vm["address"])
	assert.Equal(t, "PoS-erc20", vm["type"])
	assert.Equal(t, "c-chain", vm["deployed_on"])

	owner := vm["owner"].(map[string]interface{})
	assert.Equal(t, "0xstakingmanager00000000000000000000000c", owner["address"])
	assert.Equal(t, "multisig", owner["kind"])
	ms := owner["multisig"].(map[string]interface{})
	assert.Equal(t, float64(2), ms["threshold"])
	assert.Equal(t, float64(5), ms["owners"])

	proxy := vm["proxy"].(map[string]interface{})
	assert.Equal(t, true, proxy["is_proxy"])
	assert.Equal(t, "0ximpl000000000000000000000000000000000d", proxy["implementation"])
	assert.Equal(t, "0xpadminowner000000000000000000000000000f", proxy["proxy_admin_owner"])
	assert.Equal(t, float64(172800), proxy["upgrade_delay_seconds"])

	churn := vm["churn"].(map[string]interface{})
	assert.Equal(t, float64(3600), churn["period_seconds"])
	assert.Equal(t, float64(20), churn["max_churn_percentage"])
}

func TestHandleChainRisk_NotFound(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{
				scanFunc: func(dest ...interface{}) error {
					return ErrMockDB
				},
			}
		},
	}

	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/chains/missing/risk")

	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
}

// Helper functions for nullable types in mock rows
func stringPtr(s string) *string {
	return &s
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}

func uint8Ptr(v uint8) *uint8 {
	return &v
}

func uint16Ptr(v uint16) *uint16 {
	return &v
}
