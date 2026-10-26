package registrysyncer

import "testing"

func TestStaleCleanupAllowed(t *testing.T) {
	cases := []struct {
		total, stale uint64
		want         bool
	}{
		{525, 0, true},
		{525, 2, true},   // a redeploy
		{525, 10, true},  // at the row cap
		{525, 11, false}, // more than the cap
		{30, 7, false},   // more than a fifth of the table
		{0, 0, true},
	}
	for _, c := range cases {
		if got := staleCleanupAllowed(c.total, c.stale); got != c.want {
			t.Errorf("staleCleanupAllowed(%d, %d) = %v, want %v", c.total, c.stale, got, c.want)
		}
	}
}

func TestRegistryBlockchainIDs_SkipsEmpty(t *testing.T) {
	regs := []ChainRegistry{
		{SubnetID: "s1", Chains: []ChainEntry{{BlockchainID: "a"}, {BlockchainID: ""}}},
		{SubnetID: "s2", Chains: []ChainEntry{{BlockchainID: "b"}}},
	}
	got := registryBlockchainIDs(regs)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}
