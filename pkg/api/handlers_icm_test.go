package api

import (
	"context"
	"encoding/json"
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
