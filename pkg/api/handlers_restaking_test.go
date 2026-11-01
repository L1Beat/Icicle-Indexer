package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIdleAmount(t *testing.T) {
	require.Equal(t, "8684218504272505054021750", idleAmount("13684218504272505054021750", "5000000000000000000000000"))
	require.Equal(t, "0", idleAmount("5", "5"))
	require.Equal(t, "0", idleAmount("5", "7"))
	require.Equal(t, "0", idleAmount("x", "1"))
}

func TestAddRestakingRow_KeepsNewestSample(t *testing.T) {
	old := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cur := old.Add(5 * time.Minute)
	byKey := map[string]*RestakingL1{}
	row := func(at time.Time) RestakingL1 {
		return RestakingL1{ProtocolID: "suzaku", SubnetID: "s1", SampledAt: at}
	}
	addRestakingRow(byKey, row(old), RestakingVault{Address: "gone"})
	addRestakingRow(byKey, row(cur), RestakingVault{Address: "v1"})
	addRestakingRow(byKey, row(cur), RestakingVault{Address: "v2"})
	addRestakingRow(byKey, row(old), RestakingVault{Address: "gone2"})
	addRestakingRow(byKey, RestakingL1{ProtocolID: "suzaku", SubnetID: "s2", SampledAt: old}, RestakingVault{Address: "w"})

	require.Len(t, byKey, 2)
	l1 := byKey["suzaku/s1"]
	require.Equal(t, cur, l1.SampledAt)
	require.Len(t, l1.Vaults, 2)
	require.Equal(t, "v1", l1.Vaults[0].Address)
	require.Equal(t, "v2", l1.Vaults[1].Address)
}

func TestFilterRestaking(t *testing.T) {
	chain := "c1"
	o := RestakingOverview{Protocols: restakingProtocols, L1s: []RestakingL1{
		{SubnetID: "s1", BlockchainID: &chain},
		{SubnetID: "s2"},
	}}
	require.Len(t, filterRestaking(o, "", "").L1s, 2)
	require.Len(t, filterRestaking(o, "s2", "").L1s, 1)
	got := filterRestaking(o, "", "c1")
	require.Len(t, got.L1s, 1)
	require.Equal(t, "s1", got.L1s[0].SubnetID)
	require.Equal(t, []RestakingL1{}, filterRestaking(o, "nope", "").L1s)
	require.Equal(t, restakingProtocols, filterRestaking(o, "nope", "").Protocols)
}
