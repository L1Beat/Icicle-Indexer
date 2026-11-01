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

func liqMock(t *testing.T, events func(q string, args []interface{}) (driver.Rows, error)) *MockConn {
	return &MockConn{QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
		if strings.Contains(q, "FROM lending_market_snapshots") && strings.Contains(q, "argMax(symbol") {
			return NewMockRows([]string{"p", "m", "a", "s", "d"}, [][]interface{}{
				{"benqi", "0xf362fea9659cf036792c9cb02f8ff8198e21b4cb", "0x2b2c81e08f1af8835a78bb2a90ae924ace0ea4be", "sAVAX", uint8(18)},
				{"benqi", "0x5c0401e81bc07ca70fad469b451682c0d747ef1c", "0x0000000000000000000000000000000000000000", "AVAX", uint8(18)},
				{"aave-v3", "0xpool", "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7", "WAVAX", uint8(18)},
			}), nil
		}
		return events(q, args)
	}}
}

func TestHandleLendingLiqDaily(t *testing.T) {
	liqCache = newTTLCache(5 * time.Minute)
	server := NewTestServer(liqMock(t, func(q string, args []interface{}) (driver.Rows, error) {
		require.Equal(t, 6, args[0]) // days=7 -> today and the 6 days before
		return NewMockRows([]string{"d", "p", "c", "n", "u", "r", "s"}, [][]interface{}{
			{"2026-10-31", "benqi", "f362fea9659cf036792c9cb02f8ff8198e21b4cb", uint64(2), uint64(0), 100.0, 110.0},
			{"2026-10-31", "aave-v3", "2b2c81e08f1af8835a78bb2a90ae924ace0ea4be", uint64(1), uint64(0), 50.0, 52.0},
			{"2026-10-31", "aave-v3", "b31f66aa3c1e785363f0875a1b74e27b85fd66c7", uint64(1), uint64(1), 0.0, 0.0},
			{"2026-11-01", "benqi", "5c0401e81bc07ca70fad469b451682c0d747ef1c", uint64(1), uint64(0), 10.0, 11.0},
		}), nil
	}))
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/lending/liquidations?days=7")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data []LiqDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	d := resp.Data[0]
	require.Equal(t, uint64(4), d.Count)
	require.Equal(t, uint64(1), d.Unpriced)
	require.InDelta(t, 162.0, d.SeizedUSD, 1e-9)
	require.Len(t, d.ByProtocol, 2)
	// sAVAX seized on BENQI (qisAVAX market) and Aave (sAVAX) merge into one collateral row.
	require.Equal(t, "sAVAX", d.ByCollateral[0].Symbol)
	require.Equal(t, uint64(3), d.ByCollateral[0].Count)
	require.InDelta(t, 162.0, d.ByCollateral[0].SeizedUSD, 1e-9)
	require.Equal(t, "AVAX", resp.Data[1].ByCollateral[0].Symbol)
}

func TestHandleLendingLiqRecentAndTop(t *testing.T) {
	liqCache = newTTLCache(5 * time.Minute)
	at := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	var lastQuery string
	var lastArgs []interface{}
	server := NewTestServer(liqMock(t, func(q string, args []interface{}) (driver.Rows, error) {
		lastQuery, lastArgs = q, args
		return NewMockRows([]string{"p", "tx", "b", "t", "bor", "liq", "d", "ra", "ru", "c", "sa", "su"}, [][]interface{}{
			{"benqi", strings.Repeat("aa", 32), uint32(100), at, strings.Repeat("22", 20), strings.Repeat("11", 20),
				"5c0401e81bc07ca70fad469b451682c0d747ef1c", "1000", 3376.58, "f362fea9659cf036792c9cb02f8ff8198e21b4cb", "432983712400627209946", 3714.24},
			{"aave-v3", strings.Repeat("bb", 32), uint32(99), at, strings.Repeat("44", 20), strings.Repeat("11", 20),
				"9999999999999999999999999999999999999999", "2000", 0.0, "b31f66aa3c1e785363f0875a1b74e27b85fd66c7", "99", 0.0},
		}), nil
	}))

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/lending/liquidations/recent?limit=10&min_usd=100")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, lastQuery, "block_number DESC")
	require.Equal(t, 100.0, lastArgs[0])
	require.Equal(t, 10, lastArgs[1])
	var resp struct {
		Data []LiqEvent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	e := resp.Data[0]
	require.Equal(t, "0x"+strings.Repeat("aa", 32), e.TxHash)
	require.Equal(t, "AVAX", e.Debt.Symbol)
	require.Equal(t, "0x0000000000000000000000000000000000000000", e.Debt.Address)
	require.Equal(t, "sAVAX", e.Collateral.Symbol)
	require.Equal(t, "432983712400627209946", e.Collateral.Amount)
	require.InDelta(t, 3714.24, *e.Collateral.USD, 1e-9)
	unpriced := resp.Data[1]
	require.Nil(t, unpriced.Debt.USD)
	require.Nil(t, unpriced.Collateral.USD)
	require.Nil(t, unpriced.Debt.Decimals) // unknown asset
	require.Equal(t, "", unpriced.Debt.Symbol)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/lending/liquidations/top?days=30&limit=5")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, lastQuery, "seized_usd DESC")
	require.Equal(t, 29, lastArgs[0])

	for _, path := range []string{
		"/api/v1/metrics/lending/liquidations?days=0",
		"/api/v1/metrics/lending/liquidations/recent?limit=501",
		"/api/v1/metrics/lending/liquidations/recent?min_usd=-1",
		"/api/v1/metrics/lending/liquidations/top?limit=101",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestLendingMarketSeriesAndTotals(t *testing.T) {
	lendingCache = newTTLCache(2 * time.Minute)
	server := NewTestServer(&MockConn{QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
		if strings.Contains(q, "sumIf") {
			return NewMockRows([]string{"d", "p", "s", "b"}, [][]interface{}{
				{"2026-10-31", "aave-v3", 400e6, 150e6},
				{"2026-10-31", "benqi", 130e6, 35e6},
			}), nil
		}
		require.Equal(t, "aave-v3", args[0])
		require.Equal(t, "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7", args[1])
		return NewMockRows([]string{"d", "dec", "s", "b", "sa", "ba", "p"}, [][]interface{}{
			{"2026-10-31", uint8(18), "2000000000000000000000", "500000000000000000000", 0.01, 0.03, ptrOf(8.6)},
		}), nil
	}})

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/lending/timeseries?market=aave-v3:0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7&days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var series struct {
		Data []LendingMarketPoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &series))
	require.Len(t, series.Data, 1)
	p := series.Data[0]
	require.InDelta(t, 17200, *p.SuppliedUSD, 1e-6)
	require.InDelta(t, 25, *p.UtilizationPct, 1e-9)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/lending/timeseries/totals?days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var totals struct {
		Data []LendingTotalsPoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &totals))
	require.Len(t, totals.Data, 1)
	require.InDelta(t, 530e6, totals.Data[0].SuppliedUSD, 1e-6)
	require.Len(t, totals.Data[0].ByProtocol, 2)

	for _, path := range []string{
		"/api/v1/metrics/lending/timeseries?market=aave-v3",
		"/api/v1/metrics/lending/timeseries?market=compound:0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7",
		"/api/v1/metrics/lending/timeseries?market=aave-v3:0x123",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}
