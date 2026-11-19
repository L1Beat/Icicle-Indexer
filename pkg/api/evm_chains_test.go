package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func onlyCChain(ctx context.Context) (map[uint32]bool, error) {
	return map[uint32]bool{43114: true}, nil
}

// Every EVM route answers an unindexed chain with the same 404, before any
// query: the mock has no query functions, so a query would fail the request.
func TestUnindexedChainIs404(t *testing.T) {
	server := NewTestServer(&MockConn{})
	server.evmChains = newIndexedChains(onlyCChain)

	for _, path := range []string{
		"/api/v1/data/evm/36463/blocks",
		"/api/v1/data/evm/36463/blocks/0",
		"/api/v1/data/evm/36463/blocks/1",
		"/api/v1/data/evm/36463/txs",
		"/api/v1/data/evm/36463/txs/0xabc",
		"/api/v1/data/evm/36463/address/0x0000000000000000000000000000000000000001/txs",
		"/api/v1/data/evm/36463/address/0x0000000000000000000000000000000000000001/internal-txs",
		"/api/v1/data/evm/36463/address/0x0000000000000000000000000000000000000001/balances",
		"/api/v1/data/evm/36463/address/0x0000000000000000000000000000000000000001/native",
		"/api/v1/data/evm/36463/stablecoins",
		"/api/v1/data/evm/36463/stablecoins/timeseries",
		"/api/v1/data/evm/36463/tokenized-funds",
		"/api/v1/data/evm/36463/tokenized-funds/timeseries",
		"/api/v1/data/evm/36463/rwa",
		"/api/v1/data/evm/36463/rwa/timeseries",
		"/api/v1/metrics/evm/36463/stats",
		"/api/v1/metrics/evm/36463/timeseries",
		"/api/v1/metrics/evm/36463/timeseries/tx_count",
		"/api/v1/metrics/evm/36463/fees/burned",
		"/ws/blocks/36463",
	} {
		w := MakeRequest(t, server, "GET", path)
		require.Equal(t, http.StatusNotFound, w.Code, path)
		require.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"Chain not indexed"}}`, w.Body.String(), path)
	}
}

func TestIndexedChainPassesThrough(t *testing.T) {
	server := NewTestServer(&MockConn{})
	server.evmChains = newIndexedChains(onlyCChain)

	// An invalid chain ID still gets the handler's own 400.
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/abc/blocks")
	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)

	// The indexed chain reaches the handler (here: its not-found path).
	w = MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks/5")
	require.NotContains(t, w.Body.String(), "Chain not indexed")
}

// If the chain list cannot be loaded, requests go through rather than 404.
func TestIndexedChainsFailOpen(t *testing.T) {
	c := newIndexedChains(func(ctx context.Context) (map[uint32]bool, error) {
		return nil, errors.New("clickhouse down")
	})
	_, ok := c.indexed(context.Background(), 36463)
	require.False(t, ok)

	calls := 0
	c = newIndexedChains(func(ctx context.Context) (map[uint32]bool, error) {
		calls++
		return map[uint32]bool{43114: true}, nil
	})
	for i := 0; i < 3; i++ {
		indexed, ok := c.indexed(context.Background(), 43114)
		require.True(t, indexed && ok)
	}
	require.Equal(t, 1, calls, "the chain list is cached")
}
