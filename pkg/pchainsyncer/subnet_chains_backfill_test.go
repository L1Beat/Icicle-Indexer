package pchainsyncer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"icicle/pkg/pchainrpc"
)

// The RPC backfill has no creation block or time, so it must only add chains
// the CreateChainTx scan missed. Re-inserting known chains or the Primary
// Network chains used to zero their created_block/created_time every cycle.
func TestMissingRPCChains(t *testing.T) {
	all := []pchainrpc.BlockchainInfo{
		{ID: "2q9e4r6Mu3U68nU1fYjgbR6JvwrRx36CohpAX5UQxse55x1Q5", SubnetID: primaryNetworkSubnetID, Name: "C-Chain"},
		{ID: "2oYMBNV4eNHyqk2fjjV5nVQLDbtmNJzq5s3qs3Lo6ftnC6FByM", SubnetID: primaryNetworkSubnetID, Name: "X-Chain"},
		{ID: "known", SubnetID: "subnetA", Name: "Found from CreateChainTx"},
		{ID: "rpcOnly", SubnetID: "subnetB", Name: "Only in getBlockchains"},
	}

	got := missingRPCChains(all, map[string]bool{"known": true})

	require.Len(t, got, 1)
	require.Equal(t, "rpcOnly", got[0].ID)
	require.Empty(t, missingRPCChains(all[:3], map[string]bool{"known": true}))
}
