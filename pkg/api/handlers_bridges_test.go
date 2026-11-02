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

	"icicle/pkg/bridges"
)

func bridgeMock(t *testing.T, through, first time.Time, query func(q string, args []interface{}) (driver.Rows, error)) *MockConn {
	return &MockConn{
		QueryRowFunc: func(ctx context.Context, q string, args ...interface{}) driver.Row {
			require.Contains(t, q, "min(block_time)")
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				*dest[0].(*time.Time) = first
				return nil
			}}
		},
		QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
			if strings.Contains(q, "indexer_watermarks") {
				if through.IsZero() {
					return NewMockRows([]string{"last_period"}, nil), nil
				}
				return NewMockRows([]string{"last_period"}, [][]interface{}{{through}}), nil
			}
			return query(q, args)
		},
	}
}

func TestHandleBridges(t *testing.T) {
	bridgeCache = newTTLCache(5 * time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC) // Nov 2 is the last complete day
	first := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	var gotArgs []interface{}
	server := NewTestServer(bridgeMock(t, through, first, func(q string, args []interface{}) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "(chain, bridge)"):
			return NewMockRows([]string{"chain", "bridge", "in", "out", "n", "priced", "flagged"}, [][]interface{}{
				{"dexalot", "", 1e5, 0.0, uint64(3), uint64(3), uint64(0)},
				{"dexalot", "layerzero", 1e5, 0.0, uint64(3), uint64(3), uint64(0)},
				{"ethereum", "", 2.5e8, 4e8, uint64(90), uint64(80), uint64(2)},
				{"ethereum", "avalanche-bridge", 1e7, 2e7, uint64(20), uint64(15), uint64(0)},
				{"ethereum", "cctp", 2.4e8, 3.8e8, uint64(70), uint64(65), uint64(2)},
			}), nil
		case strings.Contains(q, "GROUPING SETS"):
			gotArgs = args
			return NewMockRows([]string{"bridge", "in", "out", "n", "priced", "flagged", "pass"}, [][]interface{}{
				{"cctp", 3.8e8, 7.1e8, uint64(100), uint64(100), uint64(2), uint64(0)},
				{"layerzero", 8e7, 2e7, uint64(50), uint64(10), uint64(0), uint64(0)},
				{"", 4.6e8, 7.3e8, uint64(150), uint64(110), uint64(2), uint64(7)},
			}), nil
		case strings.Contains(q, "GROUP BY token"):
			return NewMockRows([]string{"token", "symbol", "decimals", "known", "priced", "ai", "ao", "ui", "uo", "n"}, [][]interface{}{
				{bridges.USDC, "USDC", uint8(6), true, true, "380000000000000", "710000000000000", 3.8e8, 7.1e8, uint64(100)},
				{"00000000000000000000000000000000000000e1", "", uint8(0), false, false, "5", "0", 0.0, 0.0, uint64(1)},
			}), nil
		}
		t.Fatalf("unexpected query %s", q)
		return nil, nil
	}))

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/bridges?days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data BridgeOverview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	d := resp.Data
	require.Equal(t, "2026-11-02", *d.AsOf)
	require.Equal(t, "2026-10-04", *d.From)
	require.Equal(t, time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), gotArgs[0])
	require.Equal(t, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), gotArgs[1])
	require.Equal(t, uint64(7), d.Totals.PassthroughCount)
	require.InDelta(t, -2.7e8, d.Totals.NetUSD, 1)
	require.Len(t, d.ByBridge, len(bridges.Bridges)) // every bridge, zero when idle
	require.Equal(t, "Circle CCTP", d.ByBridge[0].Name)
	require.InDelta(t, 0.2, d.ByBridge[1].PricedShare, 1e-9)
	require.Equal(t, uint64(0), d.ByBridge[2].Transfers)
	require.Equal(t, "Ethereum", d.ByChain[0].Name)
	require.True(t, d.ByChain[1].AvalancheL1)
	require.Len(t, d.ByChain[0].Bridges, 2)
	require.Equal(t, "cctp", d.ByChain[0].Bridges[0].ID) // largest first
	require.InDelta(t, 3.8e8, d.ByChain[0].Bridges[0].OutflowUSD, 1)
	require.Equal(t, "layerzero", d.ByChain[1].Bridges[0].ID)
	require.Equal(t, "0x"+bridges.USDC, d.ByToken[0].Token)
	require.Equal(t, uint8(6), *d.ByToken[0].Decimals)
	require.Nil(t, d.ByToken[1].Decimals)
	require.False(t, d.ByToken[1].Priced)

	// Clamped to the first tracked day.
	w = MakeRequest(t, server, "GET", "/api/v1/metrics/bridges?days=3650")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "2026-10-01", *resp.Data.From)
	require.Equal(t, 33, resp.Data.Days)

	for _, path := range []string{
		"/api/v1/metrics/bridges?days=0",
		"/api/v1/metrics/bridges/timeseries?days=x",
		"/api/v1/metrics/bridges/transfers/top?limit=101",
		"/api/v1/metrics/bridges/transfers/recent?limit=0",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestHandleBridges_BeforeFirstWindow(t *testing.T) {
	bridgeCache = newTTLCache(5 * time.Minute)
	server := NewTestServer(bridgeMock(t, time.Time{}, time.Time{}, func(q string, args []interface{}) (driver.Rows, error) {
		t.Fatal("no aggregate query before the first window")
		return nil, nil
	}))
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/bridges")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"as_of":null`)
}

func TestHandleBridgesTimeseriesAndTransfers(t *testing.T) {
	bridgeCache = newTTLCache(5 * time.Minute)
	bridgeRecentCache = newTTLCache(time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC)
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	usd := 1e7
	zero := strings.Repeat("0", 40)
	server := NewTestServer(bridgeMock(t, through, first, func(q string, args []interface{}) (driver.Rows, error) {
		if strings.Contains(q, "GROUP BY day, bridge") {
			return NewMockRows([]string{"day", "bridge", "in", "out", "n"}, [][]interface{}{
				{"2026-11-01", "cctp", 1e6, 3e6, uint64(10)},
				{"2026-11-01", "layerzero", 2e6, 0.0, uint64(4)},
				{"2026-11-02", "cctp", 5e5, 0.0, uint64(2)},
			}), nil
		}
		require.Contains(t, q, "LIMIT ?")
		require.Equal(t, 10, args[len(args)-2])
		return NewMockRows([]string{"tx", "t", "bridge", "dir", "chain", "token", "sym", "dec", "known", "amount", "usd", "from", "to", "via", "pass"}, [][]interface{}{
			{strings.Repeat("ab", 32), through, "cctp", "out", "base", bridges.USDC, "USDC", uint8(6), true, "10000000000000", &usd,
				"00000000000000000000000000000000000000f1", zero, "ccip", false},
		}), nil
	}))

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/bridges/timeseries?days=2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ts struct {
		Data []BridgeDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ts))
	require.Len(t, ts.Data, 2)
	require.InDelta(t, 3e6, ts.Data[0].InflowUSD, 1e-6)
	require.InDelta(t, 0, ts.Data[0].NetUSD, 1e-6)
	require.Len(t, ts.Data[0].Bridges, 2)

	for _, path := range []string{"/api/v1/metrics/bridges/transfers/top?days=7&limit=10", "/api/v1/metrics/bridges/transfers/recent?limit=10"} {
		w = MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var tr struct {
			Data []BridgeTransfer `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tr))
		require.Len(t, tr.Data, 1)
		x := tr.Data[0]
		require.Equal(t, "0x"+strings.Repeat("ab", 32), x.TxHash)
		require.Equal(t, "Base", x.ChainName)
		require.Equal(t, "0x00000000000000000000000000000000000000f1", *x.From)
		require.Nil(t, x.To)
		require.Equal(t, "ccip", x.Via)
		require.InDelta(t, 1e7, *x.USD, 1e-6)
	}
}
