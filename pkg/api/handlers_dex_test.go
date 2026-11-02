package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

func dexMock(t *testing.T, asOf time.Time, query func(q string, args []interface{}) (driver.Rows, error)) *MockConn {
	return &MockConn{
		QueryRowFunc: func(ctx context.Context, q string, args ...interface{}) driver.Row {
			require.Contains(t, q, "max(day)")
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				*dest[0].(*time.Time) = asOf
				if !asOf.IsZero() {
					*dest[1].(*time.Time) = asOf.AddDate(0, 0, -99) // 100 days rolled up
				}
				return nil
			}}
		},
		QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
			return query(q, args)
		},
	}
}

func TestHandleDexOverview(t *testing.T) {
	dexCache = newTTLCache(10 * time.Minute)
	asOf := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var gotArgs []interface{}
	server := NewTestServer(dexMock(t, asOf, func(q string, args []interface{}) (driver.Rows, error) {
		require.Contains(t, q, "GROUPING SETS")
		gotArgs = args
		return NewMockRows([]string{"dex", "swaps", "priced", "volume", "flagged", "addresses"}, [][]interface{}{
			{"pharaoh-v3", uint64(1000), uint64(990), 4.5e7, uint64(0), uint64(300)},
			{"lfj", uint64(100), uint64(90), 1e6, uint64(1), uint64(50)},
			{"", uint64(1100), uint64(1080), 4.6e7, uint64(1), uint64(320)},
		}), nil
	}))

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/dex?days=7")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data DexOverview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	d := resp.Data
	require.Equal(t, "2026-11-01", *d.AsOf)
	require.Equal(t, "2026-10-26", *d.From) // 7 complete days, inclusive
	require.Equal(t, 7, d.Days)
	require.Equal(t, asOf.AddDate(0, 0, -6), gotArgs[0])
	require.Equal(t, asOf, gotArgs[1])
	require.Len(t, d.Dexes, 6) // every DEX, zero when idle
	require.Equal(t, "pharaoh-v3", d.Dexes[0].ID)
	require.Equal(t, "pharaoh", d.Dexes[0].Protocol)
	require.Equal(t, "pharaoh", d.Dexes[1].Protocol)
	require.InDelta(t, 0.99, d.Dexes[0].PricedShare, 1e-9)
	require.Equal(t, uint64(0), d.Dexes[1].Swaps)
	require.Equal(t, "LFJ", d.Dexes[4].Name)
	require.Equal(t, uint64(1), d.Dexes[4].FlaggedSwaps)
	require.Equal(t, uint64(320), d.Totals.UniqueAddresses)
	require.Equal(t, uint64(1100), d.Totals.Swaps)
	require.Equal(t, float64(1_000_000), d.FlagThresholdUSD)
	require.True(t, strings.Contains(w.Body.String(), `"unique_addresses":320`))

	// Clamped: 3650 days asked, 100 rolled up.
	w = MakeRequest(t, server, "GET", "/api/v1/metrics/dex?days=3650")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 100, resp.Data.Days)
	require.Equal(t, "2026-07-25", *resp.Data.From)
	require.Equal(t, asOf.AddDate(0, 0, -99), gotArgs[0])
}

func TestHandleDexOverview_Empty(t *testing.T) {
	dexCache = newTTLCache(10 * time.Minute)
	server := NewTestServer(dexMock(t, time.Time{}, func(q string, args []interface{}) (driver.Rows, error) {
		t.Fatal("no rollup query before the first rollup")
		return nil, nil
	}))
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/dex")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"as_of":null`)
}

func TestHandleDexTimeseriesAndPools(t *testing.T) {
	dexCache = newTTLCache(10 * time.Minute)
	asOf := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	server := NewTestServer(dexMock(t, asOf, func(q string, args []interface{}) (driver.Rows, error) {
		if strings.Contains(q, "dex_pools") {
			require.Equal(t, 5, args[2])
			return NewMockRows([]string{"pool", "dex", "v", "n", "t0", "t1", "s0", "s1"}, [][]interface{}{
				{"00000000000000000000000000000000000000a1", "pharaoh-dlmm", 1.2e7, uint64(900),
					"b31f66aa3c1e785363f0875a1b74e27b85fd66c7", "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", "WAVAX", "USDC"},
			}), nil
		}
		return NewMockRows([]string{"day", "dex", "v", "n"}, [][]interface{}{
			{"2026-10-31", "pharaoh-v3", 1.9e7, uint64(10)},
			{"2026-10-31", "uniswap-v3", 6e6, uint64(5)},
			{"2026-11-01", "pharaoh-v3", 4.5e7, uint64(20)},
		}), nil
	}))

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/dex/timeseries?days=2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ts struct {
		Data []DexDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ts))
	require.Len(t, ts.Data, 2)
	require.InDelta(t, 2.5e7, ts.Data[0].VolumeUSD, 1e-6)
	require.Equal(t, uint64(15), ts.Data[0].Swaps)
	require.Len(t, ts.Data[0].Dexes, 2)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/dex/pools?days=7&limit=5")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var pools struct {
		Data []DexPool `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &pools))
	require.Len(t, pools.Data, 1)
	p := pools.Data[0]
	require.Equal(t, "0x00000000000000000000000000000000000000a1", p.Pool)
	require.Equal(t, "pharaoh", p.Protocol)
	require.Equal(t, "WAVAX", p.Token0.Symbol)
	require.Equal(t, "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", p.Token1.Address)

	// A window past the first rolled-up day is clamped and reported as such.
	w = MakeRequest(t, server, "GET", "/api/v1/metrics/dex/timeseries?days=3650")
	require.Equal(t, http.StatusOK, w.Code)

	for _, path := range []string{
		"/api/v1/metrics/dex?days=0",
		"/api/v1/metrics/dex?days=3651",
		"/api/v1/metrics/dex/timeseries?days=x",
		"/api/v1/metrics/dex/pools?days=31",
		"/api/v1/metrics/dex/pools?limit=101",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}
