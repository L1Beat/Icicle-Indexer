package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/stretchr/testify/require"
)

func TestHandleICM(t *testing.T) {
	icmCache = newTTLCache(5 * time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC)
	dexalot, gunz := ids.ID{0xaa}, ids.ID{0xbb}
	var gotArgs []interface{}
	server := NewTestServer(&MockConn{
		QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
			switch {
			case strings.Contains(q, "indexer_watermarks"):
				return NewMockRows([]string{"last_period"}, [][]interface{}{{through}}), nil
			case strings.Contains(q, "l1_registry"):
				return NewMockRows([]string{"blockchain_id", "name"}, [][]interface{}{{dexalot.String(), "Dexalot"}}), nil
			case strings.Contains(q, "FROM icm_apps"):
				return NewMockRows([]string{"a", "k", "t", "s", "d"}, nil), nil
			case strings.Contains(q, "GROUP BY day, app, direction"):
				return NewMockRows([]string{"day", "app", "direction", "n"}, [][]interface{}{
					{"2026-11-01", strings.Repeat("a1", 20), "sent", uint64(500)},
					{"2026-11-01", strings.Repeat("a2", 20), "received", uint64(400)},
					{"2026-11-01", zeroAddress, "received", uint64(3)},
				}), nil
			case strings.Contains(q, "icm_messages"):
				gotArgs = args
				return NewMockRows([]string{"day", "direction", "counterparty", "n"}, [][]interface{}{
					{"2026-11-01", "sent", string(dexalot[:]), uint64(500)},
					{"2026-11-01", "received", string(dexalot[:]), uint64(400)},
					{"2026-11-02", "sent", string(gunz[:]), uint64(7)},
				}), nil
			}
			t.Fatalf("unexpected query %s", q)
			return nil, nil
		},
	})

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/icm?days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data ICMOverview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	d := resp.Data
	require.Equal(t, "2026-11-02", *d.AsOf)
	require.Equal(t, "2026-10-04", *d.From)
	require.Equal(t, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), gotArgs[1])
	require.Equal(t, ICMCounts{Sent: 507, Received: 400, Total: 907}, d.Totals)
	require.Len(t, d.ByChain, 2)
	require.Equal(t, ICMChain{BlockchainID: dexalot.String(), Name: "Dexalot", ICMCounts: ICMCounts{Sent: 500, Received: 400, Total: 900}}, d.ByChain[0])
	require.Equal(t, "", d.ByChain[1].Name) // not in the registry

	// The full history starts the day the messenger was deployed.
	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm?days=3650")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "2024-03-05", *resp.Data.From)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm/timeseries?days=3")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ts struct {
		Data []ICMDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ts))
	require.Len(t, ts.Data, 3) // quiet days included
	require.Equal(t, "2026-10-31", ts.Data[0].Date)
	require.Equal(t, uint64(0), ts.Data[0].Total)
	require.Empty(t, ts.Data[0].ByChain)
	require.Equal(t, ICMCounts{Sent: 500, Received: 400, Total: 900}, ts.Data[1].ICMCounts)
	require.Equal(t, uint64(7), ts.Data[2].Sent)

	require.Nil(t, ts.Data[1].ByApp) // only with by_app=true
	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm/timeseries?days=3&by_app=true")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var tsApps struct {
		Data []ICMDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsApps))
	require.Empty(t, tsApps.Data[0].ByApp) // quiet day
	require.Len(t, tsApps.Data[1].ByApp, 2)
	require.Equal(t, ICMAppDay{Address: "0x" + strings.Repeat("a1", 20), ICMCounts: ICMCounts{Sent: 500, Total: 500}}, tsApps.Data[1].ByApp[0])

	for _, path := range []string{"/api/v1/metrics/icm?days=0", "/api/v1/metrics/icm/timeseries?days=3651"} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestHandleICM_BeforeFirstWindow(t *testing.T) {
	icmCache = newTTLCache(5 * time.Minute)
	server := NewTestServer(&MockConn{
		QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
			require.Contains(t, q, "indexer_watermarks")
			return NewMockRows([]string{"last_period"}, nil), nil
		},
	})
	w := MakeRequest(t, server, "GET", "/api/v1/metrics/icm")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"as_of":null`)
	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm/timeseries")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"data":[]`)
}

func TestHandleICMAppsAndMessages(t *testing.T) {
	icmCache = newTTLCache(5 * time.Minute)
	icmRecentCache = newTTLCache(time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC)
	omnicoin, henesys := ids.ID{0xaa}, ids.ID{0xbb}
	const xom, other = "af72f6caf76775daeabaf5c82ce05d97359c8fa1", "a8baad3115a133b101ef935cb2e198fd04f1c659"
	unhexed := func(h string) string { b, _ := hex.DecodeString(h); return string(b) }
	var msgQuery string
	var msgArgs []interface{}
	server := NewTestServer(&MockConn{
		QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
			switch {
			case strings.Contains(q, "indexer_watermarks"):
				return NewMockRows([]string{"last_period"}, [][]interface{}{{through}}), nil
			case strings.Contains(q, "l1_registry"):
				return NewMockRows([]string{"blockchain_id", "name"}, [][]interface{}{{omnicoin.String(), "Omnicoin"}}), nil
			case strings.Contains(q, "FROM icm_apps"):
				return NewMockRows([]string{"a", "k", "t", "s", "d"}, [][]interface{}{{xom, "ictt-remote", xom, "XOM", uint8(18)}}), nil
			case strings.Contains(q, "GROUP BY app, direction, counterparty"):
				return NewMockRows([]string{"app", "direction", "counterparty", "n"}, [][]interface{}{
					{xom, "received", string(omnicoin[:]), uint64(258000)},
					{other, "sent", string(henesys[:]), uint64(40)},
					{other, "received", string(omnicoin[:]), uint64(2)},
					{zeroAddress, "sent", string(henesys[:]), uint64(9)}, // undecoded: left out
				}), nil
			case strings.Contains(q, "min(block_time), max(block_time)"):
				require.Contains(t, q, "unhex('"+xom+"')")
				return NewMockRows([]string{"a", "f", "l"}, [][]interface{}{
					{xom, time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)},
				}), nil
			case strings.Contains(q, "ORDER BY block_number DESC"):
				msgQuery, msgArgs = q, args
				big0, amt := big.NewInt(0), new(big.Int)
				amt.SetString("10039886999999999996", 10)
				return NewMockRows([]string{"id", "dir", "cp", "t", "b", "tx", "li", "local", "remote", "gas", "ft", "fa", "del", "rr", "it", "ir", "ia"}, [][]interface{}{
					{strings.Repeat("01", 32), "received", string(omnicoin[:]), through, uint32(100), strings.Repeat("0f", 32), uint32(4), xom, strings.Repeat("c0", 20), uint64(250000), zeroAddress, *big0, strings.Repeat("92", 20), strings.Repeat("02", 20), int8(1), strings.Repeat("c5", 20), *amt},
					{strings.Repeat("02", 32), "sent", string(henesys[:]), through, uint32(100), strings.Repeat("0e", 32), uint32(2), other, strings.Repeat("59", 20), uint64(200000), strings.Repeat("aa", 20), *big.NewInt(7), zeroAddress, zeroAddress, int8(-1), zeroAddress, *big0},
					{strings.Repeat("03", 32), "sent", string(henesys[:]), through, uint32(99), strings.Repeat("0d", 32), uint32(0), other, strings.Repeat("59", 20), uint64(200000), zeroAddress, *big0, zeroAddress, zeroAddress, int8(-1), zeroAddress, *big0},
				}), nil
			}
			t.Fatalf("unexpected query %s", q)
			return nil, nil
		},
	})

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/icm/apps?days=90")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var apps struct {
		Data ICMApps `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &apps))
	require.Len(t, apps.Data.Apps, 2)
	a := apps.Data.Apps[0]
	require.Equal(t, "0x"+xom, a.Address)
	require.Equal(t, "XOM ICTT remote", a.Name)
	require.Equal(t, "ictt-remote", a.Kind)
	require.Equal(t, &ICMToken{Address: "0x" + xom, Symbol: "XOM", Decimals: 18}, a.Token)
	require.Equal(t, uint64(258000), a.Received)
	require.Equal(t, "2026-09-04T01:00:00Z", a.FirstSeen)
	require.Equal(t, "Omnicoin", a.TopChains[0].Name)
	for name, want := range map[string]bool{"ERC20TokenHome": true, "NativeTokenRemoteUpgradeable": true, "DestinationBridge": false, "ERC20TokenHomeV2": false} {
		require.Equal(t, want, genericICTTName.MatchString(name), name)
	}
	b := apps.Data.Apps[1]
	require.Equal(t, "other", b.Kind)
	require.Nil(t, b.Token)
	require.Equal(t, ICMCounts{Sent: 40, Received: 2, Total: 42}, b.ICMCounts)
	require.Len(t, b.TopChains, 2)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm/messages?limit=2&direction=received&chain="+omnicoin.String()+"&app=0x"+xom+"&before=200-0")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, msgQuery, "(block_number, log_index) < (?, ?) AND counterparty = ? AND direction = ? AND local_address = ?")
	require.Equal(t, []interface{}{uint32(200), uint32(0), string(omnicoin[:]), "received", unhexed(xom), 3}, msgArgs)
	var msgs struct {
		Data []ICMMessage `json:"data"`
		Meta Meta         `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs.Data, 2)
	require.True(t, msgs.Meta.HasMore)
	require.Equal(t, "100-2", msgs.Meta.NextCursor)
	m := msgs.Data[0]
	require.Equal(t, "0x"+strings.Repeat("01", 32), m.MessageID)
	require.Equal(t, ICMChainRef{BlockchainID: omnicoin.String(), Name: "Omnicoin"}, m.Counterparty)
	require.Equal(t, "0x"+xom, m.LocalAddress)
	require.Equal(t, "0x"+strings.Repeat("92", 20), *m.Deliverer)
	require.Nil(t, m.FeeToken)
	require.Equal(t, &ICMICTTValue{Type: "single_hop_send", Recipient: "0x" + strings.Repeat("c5", 20), AmountRaw: "10039886999999999996",
		Token: &ICMToken{Address: "0x" + xom, Symbol: "XOM", Decimals: 18}}, m.ICTT)
	require.Equal(t, "XOM ICTT remote", m.App.Name)
	m = msgs.Data[1]
	require.Equal(t, "7", *m.FeeAmount)
	require.Nil(t, m.Deliverer)
	require.Nil(t, m.ICTT)
	require.Equal(t, "other", m.App.Kind)

	for _, path := range []string{
		"/api/v1/metrics/icm/messages?before=12",
		"/api/v1/metrics/icm/messages?before=1-2x",
		"/api/v1/metrics/icm/messages?chain=nope",
		"/api/v1/metrics/icm/messages?direction=both",
		"/api/v1/metrics/icm/messages?app=0x12",
		"/api/v1/metrics/icm/messages?limit=201",
		"/api/v1/metrics/icm/apps?limit=0",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestHandleICMValue(t *testing.T) {
	icmCache = newTTLCache(5 * time.Minute)
	through := time.Date(2026, 11, 3, 0, 5, 0, 0, time.UTC)
	chainA := ids.ID{0xaa}
	const usdc, xom, home = "b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", "af72f6caf76775daeabaf5c82ce05d97359c8fa1", "4ef4752d1a4a6bb1fa7bc862ba646643c7f30ed3"
	var gotArgs []interface{}
	server := NewTestServer(&MockConn{
		QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
			switch {
			case strings.Contains(q, "indexer_watermarks"):
				return NewMockRows([]string{"last_period"}, [][]interface{}{{through}}), nil
			case strings.Contains(q, "l1_registry"):
				return NewMockRows([]string{"blockchain_id", "name"}, [][]interface{}{{chainA.String(), "Henesys"}}), nil
			case strings.Contains(q, "WHERE kind != ''") && !strings.Contains(q, "icm_messages"):
				return NewMockRows([]string{"a", "k", "t", "s", "d"}, [][]interface{}{{home, "ictt-home", usdc, "USDC", uint8(6)}}), nil
			case strings.Contains(q, "GROUPING SETS ((tok), (cp), ())"):
				gotArgs = args
				return NewMockRows(make([]string, 13), [][]interface{}{
					{xom, "XOM", uint8(18), false, "", uint64(258000), uint64(0), "0", "2590000000000000000000000", 0.0, 0.0, true, false},
					{usdc, "USDC", uint8(6), true, "", uint64(30), uint64(29), "12000000", "3000000", 12.0, 3.0, true, false},
					{"", "", uint8(0), false, string(chainA[:]), uint64(30), uint64(29), "0", "0", 12.0, 3.0, false, true},
					{"", "", uint8(0), false, "", uint64(258030), uint64(29), "0", "0", 12.0, 3.0, false, false},
				}), nil
			case strings.Contains(q, "GROUP BY day, tok"):
				return NewMockRows(make([]string, 7), [][]interface{}{
					{"2026-11-02", xom, "XOM", false, uint64(9), 0.0, 0.0},
					{"2026-11-02", usdc, "USDC", true, uint64(2), 12.0, 3.0},
				}), nil
			case strings.Contains(q, "ORDER BY usd DESC"):
				require.Equal(t, 5, args[4])
				return NewMockRows(make([]string, 13), [][]interface{}{
					{strings.Repeat("01", 32), "sent", int8(1), string(chainA[:]), through, strings.Repeat("0f", 32), home, usdc, "USDC", uint8(6), strings.Repeat("c5", 20), "12000000", 12.0},
				}), nil
			}
			t.Fatalf("unexpected query %s", q)
			return nil, nil
		},
	})

	w := MakeRequest(t, server, "GET", "/api/v1/metrics/icm/value?days=30")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, gotArgs, 4) // from, to, from, to
	var v struct {
		Data ICMValue `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	require.Equal(t, ICMValueStats{Transfers: 258030, PricedTransfers: 29, SentUSD: 12, ReceivedUSD: 3, TotalUSD: 15}, v.Data.Totals)
	require.Len(t, v.Data.ByToken, 2)
	require.Equal(t, "USDC", v.Data.ByToken[0].Token.Symbol) // priced first
	require.Equal(t, 12.0, *v.Data.ByToken[0].SentUSD)
	require.Nil(t, v.Data.ByToken[1].SentUSD) // unpriced XOM
	require.Equal(t, "2590000000000000000000000", v.Data.ByToken[1].ReceivedAmount)
	require.Equal(t, []ICMChainValue{{BlockchainID: chainA.String(), Name: "Henesys", Transfers: 30, SentUSD: 12, ReceivedUSD: 3, TotalUSD: 15}}, v.Data.ByChain)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm/value/timeseries?days=2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ts struct {
		Data []ICMValueDay `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ts))
	require.Len(t, ts.Data, 2)
	require.Empty(t, ts.Data[0].ByToken)
	require.Equal(t, uint64(11), ts.Data[1].Transfers)
	require.Equal(t, 15.0, ts.Data[1].TotalUSD)
	require.Equal(t, "USDC", ts.Data[1].ByToken[0].Symbol)

	w = MakeRequest(t, server, "GET", "/api/v1/metrics/icm/transfers/top?days=7&limit=5")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var top struct {
		Data []ICMTransfer `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &top))
	require.Len(t, top.Data, 1)
	require.Equal(t, "single_hop_send", top.Data[0].Type)
	require.Equal(t, "USDC ICTT home", top.Data[0].AppName)
	require.Equal(t, "12000000", *top.Data[0].Amount)
	require.Equal(t, "Henesys", top.Data[0].Counterparty.Name)

	for _, path := range []string{"/api/v1/metrics/icm/value?days=0", "/api/v1/metrics/icm/transfers/top?limit=500"} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}
