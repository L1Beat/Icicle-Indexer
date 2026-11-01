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

	"icicle/pkg/lendingmarkets"
)

func ptrOf[T any](v T) *T { return &v }

func TestHandleLendingMarkets(t *testing.T) {
	lendingCache = newTTLCache(2 * time.Minute)
	at := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	cols := []string{"protocol_id", "market", "asset", "symbol", "decimals", "supplied", "borrowed",
		"supply_apr", "borrow_apr", "collateral_enabled", "ltv_bps", "lt_bps", "price_usd", "supply_cap", "borrow_cap", "sampled_at"}
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			if strings.Contains(query, "lending_emode_snapshots") {
				return NewMockRows([]string{"p", "g", "l", "a", "ltv", "conf", "lt", "b", "c", "br", "prev"}, [][]interface{}{
					{"aave-v3", "2", "AVAX correlated", lendingmarkets.WAVAX, uint16(0), uint16(9300), uint16(9500), uint16(100), true, true, (*uint16)(nil)},
					{"aave-v3", "2", "AVAX correlated", lendingmarkets.SAVAX, uint16(9300), uint16(9300), uint16(9500), uint16(100), true, false, (*uint16)(nil)},
					{"aave-v4", "0xspoke", "Main", lendingmarkets.WAVAX, uint16(0), uint16(0), uint16(0), uint16(1000), false, true, ptrOf(uint16(7300))},
				}), nil
			}
			require.Contains(t, query, "lending_market_snapshots")
			return NewMockRows(cols, [][]interface{}{
				{"aave-v3", "0xpool", lendingmarkets.WAVAX, "WAVAX", uint8(18), "16000000000000000000000000", "4000000000000000000000000",
					0.01, 0.03, ptrOf(true), ptrOf(uint16(0)), ptrOf(uint16(7300)), ptrOf(10.0), ptrOf("14000000000000000000000000"), (*string)(nil), at},
				{"aave-v3", "0xpool", lendingmarkets.SAVAX, "sAVAX", uint8(18), "7000000000000000000000000", "0",
					0.0, 0.0, ptrOf(true), ptrOf(uint16(2500)), ptrOf(uint16(4000)), (*float64)(nil), (*string)(nil), (*string)(nil), at},
				{"aave-v4", "0xhub", lendingmarkets.WAVAX, "WAVAX", uint8(18), "2000000000000000000000000", "500000000000000000000000",
					0.008, 0.04, (*bool)(nil), (*uint16)(nil), (*uint16)(nil), ptrOf(10.0), (*string)(nil), (*string)(nil), at},
				{"benqi", "0xqiavax", lendingmarkets.NativeAVAX, "AVAX", uint8(18), "1000000000000000000000000", "0",
					0.0, 0.03, ptrOf(true), ptrOf(uint16(7400)), ptrOf(uint16(7400)), ptrOf(10.0), (*string)(nil), (*string)(nil), at},
				{"benqi", "0xqisavax", lendingmarkets.SAVAX, "sAVAX", uint8(18), "3000000000000000000000000", "1000000000000000000000",
					0.0, 0.02, ptrOf(true), ptrOf(uint16(5000)), ptrOf(uint16(5000)), ptrOf(12.0), (*string)(nil), ptrOf("10000000000000000000000"), at},
			}), nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			require.Contains(t, query, "benqi-savax")
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				*dest[0].(*string) = "50000000000000000000000000" // 50M sAVAX
				return nil
			}}
		},
	}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/lending")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data LendingOverview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	d := resp.Data
	require.Len(t, d.Protocols, 3)
	require.Len(t, d.Markets, 5)

	wavax := d.Markets[0]
	require.Equal(t, "aave-v3", wavax.ProtocolID)
	require.Equal(t, "WAVAX", wavax.Symbol) // sorted by supplied USD within a protocol
	require.InDelta(t, 25.0, *wavax.UtilizationPct, 1e-9)
	require.InDelta(t, 1.00500, wavax.SupplyAPYPct, 1e-4)
	require.InDelta(t, 3.04545, wavax.BorrowAPYPct, 1e-4)
	require.InDelta(t, 0.0, *wavax.LTVPct, 1e-9)
	require.InDelta(t, 73.0, *wavax.LiquidationThresholdPct, 1e-9)
	require.InDelta(t, 160e6, *wavax.SuppliedUSD, 1)
	require.Equal(t, "14000000000000000000000000", *wavax.SupplyCap)
	require.Nil(t, wavax.BorrowCap)

	savaxAave := d.Markets[1]
	require.Nil(t, savaxAave.PriceUSD)
	require.Nil(t, savaxAave.SuppliedUSD) // no oracle price: null, not 0

	v4 := d.Markets[2]
	require.Nil(t, v4.CollateralEnabled)
	require.Nil(t, v4.LTVPct)

	s := d.Summary
	require.Equal(t, "19000000000000000000000000", s.AVAX.Supplied) // 16M + 2M + 1M
	require.Equal(t, "4500000000000000000000000", s.AVAX.Borrowed)
	require.Len(t, s.AVAX.ByProtocol, 3)
	require.Equal(t, "10000000000000000000000000", s.SAVAX.Supplied)
	require.InDelta(t, 20.0, *s.SAVAX.ShareOfTokenSupplyPct, 1e-9)
	require.Len(t, s.SAVAX.ByProtocol, 2) // no sAVAX market on aave-v4 here
	require.Nil(t, s.AVAX.ShareOfTokenSupplyPct)
	require.InDelta(t, 160e6+20e6+10e6+36e6, s.Totals.SuppliedUSD, 1)
	require.Len(t, s.Totals.ByProtocol, 3)
	require.Equal(t, at, *d.SampledAt)
	require.False(t, strings.Contains(w.Body.String(), "share_of_savax_supply_percent\":null"))

	require.Len(t, d.EModeCategories, 2)
	v3 := d.EModeCategories[0]
	require.Equal(t, "AVAX correlated", v3.Label)
	require.InDelta(t, 93.0, *v3.LTVPct, 1e-9)
	require.Equal(t, []string{lendingmarkets.WAVAX, lendingmarkets.SAVAX}, v3.CollateralAssets)
	require.Equal(t, []string{lendingmarkets.WAVAX}, v3.BorrowableAssets)
	require.Equal(t, []string{lendingmarkets.WAVAX}, v3.LTVZeroAssets)
	require.Nil(t, d.EModeCategories[1].LTVPct) // V4 spokes set it per asset
	require.Len(t, wavax.EMode, 1)
	require.Equal(t, 0.0, wavax.EMode[0].LTVPct)
	require.Equal(t, 93.0, wavax.EMode[0].ConfiguredLTVPct)
	require.True(t, wavax.EMode[0].Borrowable)
	require.Len(t, v4.EMode, 1)
	require.InDelta(t, 73.0, *v4.EMode[0].PreviousLTVPct, 1e-9)
	require.InDelta(t, 10.0, v4.EMode[0].LiquidationBonusPct, 1e-9)
	require.NotNil(t, d.Markets[3].EMode) // BENQI: empty array, not null
	require.Len(t, d.Markets[3].EMode, 0)
}

func TestHandleLendingTimeseries(t *testing.T) {
	var gotArgs []interface{}
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			gotArgs = args
			return NewMockRows([]string{"date", "protocol_id", "supplied", "borrowed", "supply_apr", "borrow_apr"}, [][]interface{}{
				{"2026-11-01", "aave-v3", "100", "40", 0.01, 0.03},
				{"2026-11-01", "benqi", "50", "10", 0.0, 0.02},
				{"2026-11-02", "aave-v3", "110", "45", 0.01, 0.03},
			}), nil
		},
	}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/lending/timeseries?asset=avax&days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data []LendingPoint `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	require.Equal(t, "150", resp.Data[0].Supplied)
	require.Equal(t, "50", resp.Data[0].Borrowed)
	require.Len(t, resp.Data[0].ByProtocol, 2)
	require.Equal(t, "110", resp.Data[1].Supplied)
	require.Equal(t, []string{lendingmarkets.WAVAX, lendingmarkets.NativeAVAX}, gotArgs[0])
	require.Equal(t, 30, gotArgs[1])

	for _, path := range []string{
		"/api/v1/metrics/lending/timeseries",
		"/api/v1/metrics/lending/timeseries?asset=btc",
		"/api/v1/metrics/lending/timeseries?asset=savax&days=0",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestAprToAPYPct(t *testing.T) {
	require.Equal(t, 0.0, aprToAPYPct(0))
	require.InDelta(t, 5.12711, aprToAPYPct(0.05), 1e-4) // e^0.05 - 1
}
