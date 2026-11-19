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
			{Symbol: "SECZ", Category: categoryEquity},
			{Symbol: "XAUt0", Peg: "XAU", Category: categoryCommodity},
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
	// /rwa is every non-stablecoin: funds, equity and commodities, never stablecoins.
	assert.Equal(t, []string{"BUIDL", "BENJI", "SECZ", "XAUt0"}, symbols("/api/v1/data/evm/43114/rwa"))
}

// A category with no tokens answers [], not null.
func TestFilterCategoryEmptyIsNotNil(t *testing.T) {
	got := filterCategory([]Stablecoin{{Category: categoryStablecoin}}, []string{categoryTokenizedFund})
	require.NotNil(t, got)
	assert.Empty(t, got)
}

// Both timeseries endpoints scope stablecoin_metrics to their category's
// registry tokens, for every metric branch.
func TestRegistryTimeseriesScopedToCategory(t *testing.T) {
	for _, tc := range []struct {
		path       string
		categories []string
	}{
		{"/api/v1/data/evm/43114/stablecoins/timeseries?metric=supply", []string{categoryStablecoin}},
		{"/api/v1/data/evm/43114/stablecoins/timeseries?metric=holders", []string{categoryStablecoin}},
		{"/api/v1/data/evm/43114/tokenized-funds/timeseries?metric=supply", []string{categoryTokenizedFund}},
		{"/api/v1/data/evm/43114/tokenized-funds/timeseries?metric=volume", []string{categoryTokenizedFund}},
		{"/api/v1/data/evm/43114/rwa/timeseries?metric=supply", rwaCategories},
		{"/api/v1/data/evm/43114/rwa/timeseries?metric=transfers", rwaCategories},
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
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(tc.categories)), ", ")
		assert.Contains(t, gotQuery, "FROM stablecoins FINAL WHERE chain_id = ? AND category IN ("+marks+"))", tc.path)
		for _, c := range tc.categories {
			assert.Contains(t, gotArgs, c, tc.path)
		}
		assert.NotContains(t, gotArgs, func() string {
			if tc.categories[0] == categoryStablecoin {
				return categoryTokenizedFund
			}
			return categoryStablecoin
		}(), tc.path)
	}
}

func TestPriceFund(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	fresh := 1.065417

	// $1-per-share fund: priced from its peg. 2.5 shares at 6 decimals.
	got := priceFund(Stablecoin{Peg: "USD", Decimals: 6, Supply: "2500000"}, now)
	require.NotNil(t, got.PriceUSD)
	assert.Equal(t, 1.0, *got.PriceUSD)
	assert.InDelta(t, 2.5, *got.ValueUSD, 1e-9)
	assert.Equal(t, "peg", *got.PriceSource)
	assert.Nil(t, got.PriceAt)

	// Floating NAV with a fresh on-chain price.
	f := Stablecoin{Peg: "NAV", Decimals: 6, Supply: "10000000", navPrice: &fresh, navSource: "erc4626", navPriceAt: now.Add(-5 * time.Minute), navSampleAt: now.Add(-5 * time.Minute)}
	got = priceFund(f, now)
	require.NotNil(t, got.PriceUSD)
	assert.Equal(t, fresh, *got.PriceUSD)
	assert.InDelta(t, 10.65417, *got.ValueUSD, 1e-9)
	assert.Equal(t, "erc4626", *got.PriceSource)
	require.NotNil(t, got.PriceAt)

	// A Centrifuge NAV set a day ago, read just now, is still current, and
	// price_at is the NAV time.
	f.navPriceAt = now.Add(-31 * time.Hour)
	got = priceFund(f, now)
	require.NotNil(t, got.PriceUSD)
	assert.Equal(t, f.navPriceAt, *got.PriceAt)

	// A NAV older than 7 days is dropped: the fund stopped updating.
	f.navPriceAt = now.Add(-8 * 24 * time.Hour)
	got = priceFund(f, now)
	assert.Nil(t, got.PriceUSD)

	// A price not read for over 24h is dropped: the reader is broken.
	f.navPriceAt, f.navSampleAt = now.Add(-25*time.Hour), now.Add(-25*time.Hour)
	got = priceFund(f, now)
	assert.Nil(t, got.PriceUSD)
	assert.Nil(t, got.ValueUSD)
	assert.Nil(t, got.PriceSource)

	// Floating NAV with no on-chain price is never valued at $1.
	got = priceFund(Stablecoin{Peg: "NAV", Decimals: 6, Supply: "10000000"}, now)
	assert.Nil(t, got.PriceUSD)
	assert.Nil(t, got.ValueUSD)
}

// Price fields are explicit nulls in JSON, so clients can tell "unknown" apart.
func TestTokenizedFundNullPriceJSON(t *testing.T) {
	server := NewTestServer(&MockConn{})
	server.stablecoins.entries[43114] = &stablecoinEntry{
		fetched: time.Now(),
		data:    []Stablecoin{{Symbol: "ACRED", Peg: "NAV", Category: categoryTokenizedFund, Supply: "1"}},
	}
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/tokenized-funds")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"price_usd":null`)
	assert.Contains(t, w.Body.String(), `"value_usd":null`)
}

// Priced rows sort by value, highest first, and unpriced rows go last.
func TestSortByValue(t *testing.T) {
	v := func(f float64) *float64 { return &f }
	rows := []TokenizedFund{
		{Stablecoin: Stablecoin{Symbol: "AoABTd"}},
		{Stablecoin: Stablecoin{Symbol: "BENJI"}, ValueUSD: v(2.1e6)},
		{Stablecoin: Stablecoin{Symbol: "BUIDL"}, ValueUSD: v(356.7e6)},
		{Stablecoin: Stablecoin{Symbol: "X"}},
		{Stablecoin: Stablecoin{Symbol: "SECZ"}, ValueUSD: v(173.4e6)},
	}
	sortByValue(rows)
	var got []string
	for _, r := range rows {
		got = append(got, r.Symbol)
	}
	assert.Equal(t, []string{"BUIDL", "SECZ", "BENJI", "AoABTd", "X"}, got)
}

// /tokenized-funds keeps the registry order. Only /rwa sorts by value.
func TestTokenizedFundsOrderUnchanged(t *testing.T) {
	server := NewTestServer(&MockConn{})
	server.stablecoins.entries[43114] = &stablecoinEntry{
		fetched: time.Now(),
		data: []Stablecoin{
			{Symbol: "SMALL", Peg: "USD", Decimals: 0, Supply: "1", Category: categoryTokenizedFund},
			{Symbol: "BIG", Peg: "USD", Decimals: 0, Supply: "100", Category: categoryTokenizedFund},
			{Symbol: "XAUt0", Peg: "XAU", Decimals: 6, Supply: "400317069", Category: categoryCommodity},
		},
	}
	get := func(path string) []TokenizedFund {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusOK, w.Code)
		return ParseResponse[struct {
			Data []TokenizedFund `json:"data"`
		}](t, w).Data
	}
	funds := get("/api/v1/data/evm/43114/tokenized-funds")
	require.Len(t, funds, 2)
	assert.Equal(t, "SMALL", funds[0].Symbol)

	rwa := get("/api/v1/data/evm/43114/rwa")
	require.Len(t, rwa, 3)
	assert.Equal(t, []string{"BIG", "SMALL", "XAUt0"}, []string{rwa[0].Symbol, rwa[1].Symbol, rwa[2].Symbol})
	// A gold token with no on-chain price is never valued at $1.
	assert.Equal(t, categoryCommodity, rwa[2].Category)
	assert.Nil(t, rwa[2].PriceUSD)
	assert.Nil(t, rwa[2].ValueUSD)
}
