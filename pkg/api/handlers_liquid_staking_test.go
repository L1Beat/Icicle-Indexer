package api

import (
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"icicle/pkg/liquidstaking"
)

func parseBig(s string) *big.Int {
	n, _ := new(big.Int).SetString(s, 10)
	return n
}

func TestExchangeRate(t *testing.T) {
	// Live sAVAX values from 2026-11-01.
	require.Equal(t, "1.292953453671", exchangeRate(parseBig("24088038797328708645585787"), parseBig("18630244367219040899253899"), 18))
	require.Equal(t, "2.000000000000", exchangeRate(parseBig("2000000000000000000"), big.NewInt(1_000_000), 6))
	require.Equal(t, "0", exchangeRate(big.NewInt(1), new(big.Int), 18))
}

func TestBuildLiquidOverview(t *testing.T) {
	protocols := []liquidstaking.Protocol{
		{ID: "a", Name: "A", TokenSymbol: "aAVAX", TokenDecimals: 18, Status: "live", Attribution: "unconfirmed", PChainAddresses: []string{"P-avax1x"}},
		{ID: "b", Name: "B", TokenDecimals: 18, Status: "live", Attribution: "confirmed"},
		{ID: "c", Name: "C", TokenDecimals: 18, Status: "ended"},
	}
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	snaps := map[string]liquidSnap{
		"a": {pooledWei: parseBig("3000000000000000000000"), supply: parseBig("2000000000000000000000"), delegated: 2_500_000_000_000, delegations: 10, validators: 4, sampledAt: at},
		"c": {pooledWei: parseBig("1"), supply: parseBig("1"), delegated: 1}, // ended: ignored
	}
	out := buildLiquidOverview(protocols, snaps, big.NewInt(10_000_000_000_000))

	require.Len(t, out.Protocols, 3)
	a := out.Protocols[0]
	require.Equal(t, "3000000000000", *a.PooledAvax) // 3000 AVAX in nAVAX
	require.Equal(t, "1.500000000000", *a.ExchangeRate)
	require.Equal(t, "2500000000000", *a.ActiveDelegated)
	require.InDelta(t, 25.0, *a.ShareOfDelegatedPercent, 1e-9)
	require.Equal(t, uint32(4), *a.ValidatorCount)
	require.Equal(t, at, *a.AsOf)
	require.Nil(t, out.Protocols[1].PooledAvax) // no snapshot yet
	require.Nil(t, out.Protocols[2].PooledAvax) // ended
	require.Equal(t, []string{}, out.Protocols[1].PChainAddresses)

	require.Equal(t, "3000000000000", out.Totals.PooledAvax)
	require.Equal(t, "2500000000000", out.Totals.ActiveDelegated)
	require.Equal(t, uint64(10), out.Totals.ActiveDelegations)
	require.InDelta(t, 25.0, out.Totals.ShareOfDelegatedPercent, 1e-9)
	require.Equal(t, "10000000000000", out.NetworkDelegatedStake)
}

func TestHandleLiquidStakingTimeseries_BadParams(t *testing.T) {
	server := NewTestServer(&MockConn{})
	for _, q := range []string{"protocol=nope", "days=0", "days=x"} {
		w := MakeRequest(t, server, "GET", "/api/v1/metrics/staking/liquid/timeseries?"+q)
		require.Equal(t, http.StatusBadRequest, w.Code, q)
	}
}
