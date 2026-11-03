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
