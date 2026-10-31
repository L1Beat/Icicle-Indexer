package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

func TestHandleValidatorDelegations(t *testing.T) {
	now := time.Now().Unix()
	queries := 0
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			queries++
			require.Equal(t, "NodeID-Test1", args[0])
			return NewMockRows([]string{"tx_id", "owners", "stake", "start", "end"}, [][]interface{}{
				{"a", `["4hXFR9YmCfVr6PjcUaQN9MN4uKQHgpKdf"]`, uint64(100), uint64(now - 100), uint64(now + 100)},
				{"b", "", uint64(300), uint64(now - 200), uint64(now + 50)},
				{"c", `["4hXFR9YmCfVr6PjcUaQN9MN4uKQHgpKdf",null]`, uint64(200), uint64(now - 900), uint64(now - 10)},
			}), nil
		},
	}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "GET", "/api/v1/data/validators/NodeID-Test1/delegations")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []ValidatorDelegation `json:"data"`
		Meta Meta                  `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, int64(3), resp.Meta.Total)
	require.Equal(t, []string{"b", "c", "a"}, []string{resp.Data[0].TxID, resp.Data[1].TxID, resp.Data[2].TxID})
	require.Nil(t, resp.Data[0].RewardOwner)
	require.Equal(t, []string{"P-avax19zfygxaf59stehzedhxjesads0p5jdvfeedal0"}, resp.Data[1].RewardOwner)
	require.False(t, resp.Data[1].Active)
	require.Equal(t, "300", resp.Data[0].Stake)

	// The all-history result is cached, so filtering ended rows needs no query.
	w = MakeRequest(t, server, "GET", "/api/v1/data/validators/NodeID-Test1/delegations?active=false&sort=end_time&order=asc")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, "c", resp.Data[0].TxID)
	require.Equal(t, 1, queries)
}

func TestHandleValidatorDelegations_BadParams(t *testing.T) {
	server := NewTestServer(&MockConn{})
	for _, path := range []string{
		"/api/v1/data/validators/2ZW6HUePB/delegations",
		"/api/v1/data/validators/NodeID-X/delegations?active=maybe",
		"/api/v1/data/validators/NodeID-X/delegations?sort=fee",
		"/api/v1/data/validators/NodeID-X/delegations?order=up",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}
