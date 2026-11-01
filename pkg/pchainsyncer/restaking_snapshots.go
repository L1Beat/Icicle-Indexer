package pchainsyncer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/restaking"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// SnapshotRestaking writes the current restaking_snapshots rows for every
// Suzaku L1 that has a vault. nodeURL is the avalanchego base URL. An L1 that
// fails to read is skipped (its previous rows stay), and its error is returned.
func SnapshotRestaking(ctx context.Context, conn clickhouse.Conn, nodeURL string) error {
	base := strings.TrimSuffix(strings.TrimRight(nodeURL, "/"), "/ext/bc/P")
	caller := restaking.RPCCaller{URL: base + "/ext/bc/C/rpc", Client: liquidRPCClient}

	l1s, readErr := restaking.ReadSuzaku(ctx, caller)
	if len(l1s) == 0 {
		return readErr
	}

	// All rows of a cycle share sampled_at, so readers can keep an L1's newest
	// sample and drop vaults that are gone.
	sampledAt := time.Now().UTC()
	var tuples []string
	var args []interface{}
	for _, l1 := range l1s {
		vids := l1.ValidationIDs
		if vids == nil {
			vids = []string{}
		}
		for _, v := range l1.Vaults {
			tuples = append(tuples, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, toUInt256(?), toUInt256(?), toUInt256(?), ?)")
			args = append(args, "suzaku", l1.SubnetID, v.Address, l1.Balancer, l1.Middleware,
				l1.ModuleWeight, l1.OperatorCount, vids, v.Collateral, v.Token, v.Symbol, v.Decimals,
				v.Deposited.String(), v.Allocated.String(), v.Limit.String(), sampledAt)
		}
	}

	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	err := conn.Exec(wctx, `
		INSERT INTO restaking_snapshots (
			protocol_id, subnet_id, vault, balancer, middleware, module_weight,
			operator_count, validation_ids, collateral, token, token_symbol,
			token_decimals, deposited, allocated, l1_limit, sampled_at
		) VALUES `+strings.Join(tuples, ", "), args...)
	if err != nil {
		return fmt.Errorf("failed to insert restaking snapshots: %w", err)
	}
	return readErr
}
