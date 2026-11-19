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

// The registry list is cached once with every category, and each endpoint
// serves only its own: tokenized funds never reach the stablecoin list.
func TestStablecoinListsSplitByCategory(t *testing.T) {
	server := NewTestServer(&MockConn{})
	server.stablecoins.entries[43114] = &stablecoinEntry{
		fetched: time.Now(),
		data: []Stablecoin{
			{Symbol: "USDC", Category: categoryStablecoin},
			{Symbol: "BUIDL", Category: categoryTokenizedFund},
			{Symbol: "USDT", Category: categoryStablecoin},
			{Symbol: "BENJI", Category: categoryTokenizedFund},
		},
	}

	symbols := func(path string) []string {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := ParseResponse[struct {
			Data []Stablecoin `json:"data"`
		}](t, w)
		var out []string
		for _, c := range resp.Data {
			out = append(out, c.Symbol)
		}
		return out
	}

	assert.Equal(t, []string{"USDC", "USDT"}, symbols("/api/v1/data/evm/43114/stablecoins"))
	assert.Equal(t, []string{"BUIDL", "BENJI"}, symbols("/api/v1/data/evm/43114/tokenized-funds"))
}

// A category with no tokens answers [], not null.
func TestFilterCategoryEmptyIsNotNil(t *testing.T) {
	got := filterCategory([]Stablecoin{{Category: categoryStablecoin}}, categoryTokenizedFund)
	require.NotNil(t, got)
	assert.Empty(t, got)
}

// Both timeseries endpoints scope stablecoin_metrics to their category's
// registry tokens, for every metric branch.
func TestRegistryTimeseriesScopedToCategory(t *testing.T) {
	for _, tc := range []struct {
		path     string
		category string
	}{
		{"/api/v1/data/evm/43114/stablecoins/timeseries?metric=supply", categoryStablecoin},
		{"/api/v1/data/evm/43114/stablecoins/timeseries?metric=holders", categoryStablecoin},
		{"/api/v1/data/evm/43114/tokenized-funds/timeseries?metric=supply", categoryTokenizedFund},
		{"/api/v1/data/evm/43114/tokenized-funds/timeseries?metric=volume", categoryTokenizedFund},
	} {
		var gotQuery string
		var gotArgs []interface{}
		server := NewTestServer(&MockConn{
			QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
				gotQuery, gotArgs = query, args
				return NewMockRows(nil, nil), nil
			},
		})
		w := MakeRequest(t, server, "GET", tc.path)
		require.Equal(t, http.StatusOK, w.Code, tc.path)
		assert.True(t, strings.Contains(gotQuery, "FROM stablecoins FINAL WHERE chain_id = ? AND category = ?"), tc.path)
		assert.Contains(t, gotArgs, tc.category, tc.path)
	}
}
