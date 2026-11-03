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

	"icicle/pkg/perps"
)

func perpMock(t *testing.T, through, first time.Time, query func(q string, args []interface{}) (driver.Rows, error)) *MockConn {
	return &MockConn{
		QueryRowFunc: func(ctx context.Context, q string, args ...interface{}) driver.Row {
			require.Contains(t, q, "min(block_time) FROM perp_events")
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
			if strings.Contains(q, "FROM perp_markets") {
				return NewMockRows([]string{"m", "i", "l", "s"}, [][]interface{}{
					{"913c1f46b48b3ed35e7dc3cf754d4ae8499f31cf", "b31f66aa3c1e785363f0875a1b74e27b85fd66c7",
						"b31f66aa3c1e785363f0875a1b74e27b85fd66c7", "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"},
				}), nil
			}
			return query(q, args)
		},
	}
}

func TestHandlePerps(t *testing.T) {
	perpCache = newTTLCache(5 * time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC)
	first := time.Date(2021, 12, 22, 17, 45, 0, 0, time.UTC)
	var gotArgs []interface{}
	server := NewTestServer(perpMock(t, through, first, func(q string, args []interface{}) (driver.Rows, error) {
		require.Contains(t, q, "GROUPING SETS")
		gotArgs = args
		return NewMockRows([]string{"venue", "v", "lv", "sv", "n", "u", "f", "l", "lu"}, [][]interface{}{
			{"gmx-v2", 9.72e7, 6e7, 3.72e7, uint64(7607), uint64(157), 7.7e4, uint64(113), 1.26e6},
			{"", 9.72e7, 6e7, 3.72e7, uint64(7607), uint64(157), 7.7e4, uint64(113), 1.26e6},
		}), nil
	}))
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/perps?days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data PerpOverview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	d := resp.Data
	require.Equal(t, "2026-11-02", *d.AsOf)
	require.Equal(t, "2026-10-04", *d.From)
	require.Equal(t, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), gotArgs[1])
	require.Len(t, d.ByVenue, len(perps.Venues)) // every venue, zero when idle
	require.Equal(t, "GMX V2", d.ByVenue[0].Name)
	require.Equal(t, uint64(157), d.ByVenue[0].Traders)
	require.Equal(t, uint64(0), d.ByVenue[1].Trades)
	require.InDelta(t, 1.26e6, d.Totals.LiquidatedUSD, 1)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/perps?days=3650")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "2021-12-22", *resp.Data.From)

	for _, path := range []string{
		"/api/v1/metrics/perps?days=0",
		"/api/v1/metrics/perps/timeseries?days=3651",
		"/api/v1/metrics/perps/open-interest?days=x",
		"/api/v1/metrics/perps/liquidations/top?limit=101",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestHandlePerps_BeforeFirstWindow(t *testing.T) {
	perpCache = newTTLCache(5 * time.Minute)
	server := NewTestServer(perpMock(t, time.Time{}, time.Time{}, func(q string, args []interface{}) (driver.Rows, error) {
		t.Fatal("no aggregate query before the first window")
		return nil, nil
	}))
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/perps")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"as_of":null`)
}

func TestHandlePerpsSeriesOIAndLiquidations(t *testing.T) {
	perpCache = newTTLCache(5 * time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC)
	first := time.Date(2021, 12, 22, 0, 0, 0, 0, time.UTC)
	server := NewTestServer(perpMock(t, through, first, func(q string, args []interface{}) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "GROUP BY day, venue") && !strings.Contains(q, "perp_oi_daily"):
			return NewMockRows([]string{"day", "venue", "v", "n", "f", "l"}, [][]interface{}{
				{"2026-11-01", "gmx-v2", 3e6, uint64(250), 2500.0, 1e4},
				{"2026-11-02", "gmx-v2", 4e6, uint64(260), 2600.0, 0.0},
			}), nil
		case strings.Contains(q, "perp_oi_daily"):
			return NewMockRows([]string{"day", "venue", "market", "l", "s"}, [][]interface{}{
				{"2026-11-02", "gmx-v2", "913c1f46b48b3ed35e7dc3cf754d4ae8499f31cf", 2e6, 1e6},
				{"2026-11-02", "gmx-v1", "b31f66aa3c1e785363f0875a1b74e27b85fd66c7", 5.0, 0.0},
			}), nil
		case strings.Contains(q, "kind = 'liquidation'"):
			require.Equal(t, 10, args[2])
			return NewMockRows([]string{"tx", "t", "venue", "market", "account", "long", "size", "coll"}, [][]interface{}{
				{strings.Repeat("ab", 32), through, "gmx-v2", "913c1f46b48b3ed35e7dc3cf754d4ae8499f31cf",
					"00000000000000000000000000000000000000f1", true, 2.5e5, 1.2e4},
			}), nil
		}
		t.Fatalf("unexpected query %s", q)
		return nil, nil
	}))

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/perps/timeseries?days=2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ts struct {
		Data []PerpDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ts))
	require.Len(t, ts.Data, 2)
	require.InDelta(t, 1e4, ts.Data[0].LiquidatedUSD, 1e-6)
	require.Len(t, ts.Data[1].Venues, 1)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/perps/open-interest?days=1")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var oi struct {
		Data []PerpOIDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &oi))
	require.Len(t, oi.Data, 1)
	require.InDelta(t, 2e6+5, oi.Data[0].LongUSD, 1e-6)
	require.Equal(t, "AVAX/USD", oi.Data[0].ByMarket[0].Name)
	require.Equal(t, "AVAX-USDC", oi.Data[0].ByMarket[0].Pool)
	require.Equal(t, "AVAX/USD", oi.Data[0].ByMarket[1].Name) // V1: named by its index token
	require.Equal(t, "", oi.Data[0].ByMarket[1].Pool)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/perps/liquidations/top?days=7&limit=10")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var liq struct {
		Data []PerpLiquidation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &liq))
	require.Len(t, liq.Data, 1)
	require.Equal(t, "AVAX/USD", liq.Data[0].MarketName)
	require.Equal(t, "0x00000000000000000000000000000000000000f1", liq.Data[0].Account)
	require.True(t, liq.Data[0].IsLong)
}
