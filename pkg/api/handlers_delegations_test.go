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

func TestHandleValidatorDelegations(t *testing.T) {
	now := time.Now().Unix()
	queries := 0
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			queries++
			require.Equal(t, "NodeID-Test1", args[0])
			return NewMockRows([]string{"tx_id", "node_id", "owners", "stake", "start", "end"}, [][]interface{}{
				{"a", "NodeID-Test1", `["4hXFR9YmCfVr6PjcUaQN9MN4uKQHgpKdf"]`, uint64(100), uint64(now - 100), uint64(now + 100)},
				{"b", "NodeID-Test1", "", uint64(300), uint64(now - 200), uint64(now + 50)},
				{"c", "NodeID-Test1", `["4hXFR9YmCfVr6PjcUaQN9MN4uKQHgpKdf",null]`, uint64(200), uint64(now - 900), uint64(now - 10)},
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

func TestHandleDelegatorDelegations(t *testing.T) {
	now := time.Now().Unix()
	var pageQuery string
	var pageArgs []interface{}
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			require.Equal(t, `"4hXFR9YmCfVr6PjcUaQN9MN4uKQHgpKdf"`, args[1])
			if strings.Contains(query, "uniqExact(node)") {
				return &MockRow{scanFunc: func(dest ...interface{}) error {
					*dest[0].(*uint64), *dest[1].(*string), *dest[2].(*uint64) = 2, "400", 1
					return nil
				}}
			}
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				*dest[0].(*uint64), *dest[1].(*uint64) = 5, uint64(now-900)
				return nil
			}}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			pageQuery, pageArgs = query, args
			return NewMockRows([]string{"tx_id", "node", "stake", "start_ts", "end_ts"}, [][]interface{}{
				{"b", "NodeID-A", uint64(300), uint64(now - 200), uint64(now + 50)},
				{"a", "NodeID-A", uint64(100), uint64(now - 100), uint64(now + 100)},
			}), nil
		},
	}
	server := NewTestServer(mock)

	w := MakeRequest(t, server, "GET", "/api/v1/data/delegators/avax19zfygxaf59stehzedhxjesads0p5jdvfeedal0/delegations?active=true&sort=end_time&order=asc&limit=2")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, pageQuery, "ORDER BY end_ts ASC, tx_id")
	require.Contains(t, pageQuery, "end_ts") // active filter on end
	require.Equal(t, []interface{}{2, 0}, pageArgs[len(pageArgs)-2:])

	var resp DelegatorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	require.Equal(t, "NodeID-A", resp.Data[0].NodeID)
	require.Equal(t, "300", resp.Data[0].Stake)
	require.Equal(t, int64(2), resp.Meta.Total) // active filter -> active count
	require.False(t, resp.Meta.HasMore)
	sum := resp.Meta.Summary
	require.Equal(t, 2, sum.ActiveCount)
	require.Equal(t, "400", sum.ActiveStake)
	require.Equal(t, 1, sum.ValidatorCount)
	require.Equal(t, now-900, sum.FirstDelegationTime.Unix())

	// Ended rows: total is all-history minus active.
	w = MakeRequest(t, server, "GET", "/api/v1/data/delegators/P-avax19zfygxaf59stehzedhxjesads0p5jdvfeedal0/delegations?active=false&limit=2")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, int64(3), resp.Meta.Total)
	require.True(t, resp.Meta.HasMore)
	require.Contains(t, pageQuery, "ORDER BY stake DESC, tx_id")
}

func TestHandleDelegatorDelegations_Empty(t *testing.T) {
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				if s, ok := dest[1].(*string); ok {
					*s = "0"
				}
				return nil
			}}
		},
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows([]string{"tx_id"}, nil), nil
		},
	}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/delegators/P-avax1yuf4ywcqq8ddfsnghk3qml8a5lnz8q7ejt9s7f/delegations")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"data":[]`)
	require.Contains(t, w.Body.String(), `"first_delegation_time":null`)

	w = MakeRequest(t, server, "GET", "/api/v1/data/delegators/P-avax1notanaddress/delegations")
	require.Equal(t, http.StatusBadRequest, w.Code)
}
