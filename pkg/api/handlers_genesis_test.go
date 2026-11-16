package api

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	avagenesis "github.com/ava-labs/avalanchego/genesis"
	"github.com/stretchr/testify/require"
)

// The derived IDs must equal the live mainnet values. The P-Chain genesis block
// ID is block 1's parentID on the public P-Chain API.
func TestGenesisIDsMatchMainnet(t *testing.T) {
	g, err := mainnetGenesis()
	require.NoError(t, err)
	o := g.overview

	require.Equal(t, uint32(1), o.NetworkID)
	require.Equal(t, "From Snowflake to Avalanche. Per consensum ad astra.", o.Message)
	require.Equal(t, "2020-09-10T00:00:00Z", o.StartTime.Format("2006-01-02T15:04:05Z07:00"))
	require.Equal(t, "2FUFPVPxbTpKNn39moGSzsmGroYES4NZRdw3mJgNvMkMiMHJ9e", o.PChainGenesisBlockID)
	require.Equal(t, "2oYMBNV4eNHyqk2fjjV5nVQLDbtmNJzq5s3qs3Lo6ftnC6FByM", o.XChainID)
	require.Equal(t, "2q9e4r6Mu3U68nU1fYjgbR6JvwrRx36CohpAX5UQxse55x1Q5", o.CChainID)
	require.Equal(t, "FvwEAhmxKfeiG8SnEvq42hc6whRyY3EFYAvebMqDNDGCgxN5Z", o.AVAXAssetID)
	require.Equal(t, uint64(100_000_000), o.CChainGenesisGasLimit)
	require.Len(t, o.InitialValidators, 75)
	require.Equal(t, 20.0, o.InitialValidators[0].DelegationFeePercent)
	require.True(t, json.Valid(o.CChainGenesis))
}

// Totals are checked against avalanchego's own InitialSupply and against the
// allocation list, so nothing in the summary is hand-typed.
func TestGenesisSupplyAddsUp(t *testing.T) {
	g, err := mainnetGenesis()
	require.NoError(t, err)
	s := g.overview.Supply

	want, err := avagenesis.MainnetConfig.InitialSupply()
	require.NoError(t, err)
	require.Equal(t, parseNAVAX(t, s.Total).Uint64(), want)

	parts := new(big.Int).Add(parseNAVAX(t, s.XChainUnlocked), parseNAVAX(t, s.PChainLocked))
	parts.Add(parts, parseNAVAX(t, s.InitiallyStaked))
	require.Equal(t, s.Total, parts.String())

	sumAllocs := new(big.Int)
	for _, a := range g.allocations {
		sumAllocs.Add(sumAllocs, parseNAVAX(t, a.Total))
	}
	require.Equal(t, s.Total, sumAllocs.String())
	require.Len(t, g.allocations, s.AllocationCount)

	locked := new(big.Int).Add(parseNAVAX(t, s.PChainLocked), parseNAVAX(t, s.InitiallyStaked))
	sumBuckets := new(big.Int)
	for i, b := range g.overview.UnlockSchedule {
		sumBuckets.Add(sumBuckets, parseNAVAX(t, b.Amount))
		if i > 0 {
			require.True(t, g.overview.UnlockSchedule[i-1].UnlockTime.Before(b.UnlockTime))
		}
	}
	require.Equal(t, locked.String(), sumBuckets.String())
}

func TestGenesisAllocationsEndpoint(t *testing.T) {
	g, err := mainnetGenesis()
	require.NoError(t, err)
	first := g.allocations[0]
	server := NewTestServer(&MockConn{})

	w := MakeRequest(t, server, "GET", "/api/v1/data/genesis/allocations?limit=2&offset=1")
	var page struct {
		Data []GenesisAllocation `json:"data"`
		Meta Meta                `json:"meta"`
	}
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Data, 2)
	require.Equal(t, 1, page.Data[0].Index)
	require.Equal(t, int64(len(g.allocations)), page.Meta.Total)
	require.True(t, page.Meta.HasMore)

	// The same allocation is found by its bech32 address with any prefix, or by ETH address.
	for _, q := range []string{first.Address, "X-" + first.Address, "P-" + first.Address, first.ETHAddress} {
		w = MakeRequest(t, server, "GET", "/api/v1/data/genesis/allocations?address="+q)
		require.Equal(t, http.StatusOK, w.Code, q)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		require.NotEmpty(t, page.Data, q)
		require.Equal(t, first.Address, page.Data[0].Address, q)
	}

	w = MakeRequest(t, server, "GET", "/api/v1/data/genesis/allocations?limit=1001")
	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

func TestGenesisEndpoint(t *testing.T) {
	server := NewTestServer(&MockConn{})
	w := MakeRequest(t, server, "GET", "/api/v1/data/genesis")
	AssertJSONResponse(t, w, http.StatusOK)
	require.Contains(t, w.Body.String(), "Per consensum ad astra")
}

func TestPChainGenesisBlock(t *testing.T) {
	server := NewTestServer(&MockConn{})
	w := MakeRequest(t, server, "GET", "/api/v1/data/pchain/blocks/0")
	AssertJSONResponse(t, w, http.StatusOK)
	body := w.Body.String()
	require.Contains(t, body, `"block_hash":"2FUFPVPxbTpKNn39moGSzsmGroYES4NZRdw3mJgNvMkMiMHJ9e"`)
	require.Contains(t, body, `"block_time":"2020-09-10T00:00:00Z"`)
}

// C-Chain block 0 takes its hash from block 1's parent hash and has a null time.
func TestCChainGenesisBlock(t *testing.T) {
	genesisHash := [32]byte{0x31, 0xce}
	mock := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			return &MockRow{scanFunc: func(dest ...interface{}) error {
				*dest[0].(*[32]byte) = genesisHash
				return nil
			}}
		},
	}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/blocks/0")
	AssertJSONResponse(t, w, http.StatusOK)
	body := w.Body.String()
	require.Contains(t, body, `"hash":"0x31ce`)
	require.Contains(t, body, `"block_time":null`)
	require.Contains(t, body, `"gas_limit":100000000`)
	require.Contains(t, body, `"genesis":"/api/v1/data/genesis"`)

	// Other EVM chains have no genesis to serve.
	w = MakeRequest(t, NewTestServer(&MockConn{}), "GET", "/api/v1/data/evm/99999/blocks/0")
	AssertErrorResponse(t, w, http.StatusNotFound, ErrNotFound)
	require.Contains(t, w.Body.String(), "genesis")
}

func parseNAVAX(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	require.True(t, ok, s)
	return n
}
