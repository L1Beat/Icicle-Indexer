package pchainsyncer

import (
	"context"
	"fmt"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/pchainrpc"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// SnapshotStakingToday writes today's staking_snapshots rows from the current
// l1_validator_state: one per subnet with active validators. Runs every sync
// cycle, so today's row is overwritten until the day closes and the last
// cycle's numbers stick (ReplacingMergeTree on sampled_at).
//
// AVAX total supply comes from platform.getCurrentSupply. If that call fails
// the snapshot is still written with total_supply 0 rather than skipped - the
// stake numbers matter more and the next cycle fills supply back in.
func SnapshotStakingToday(ctx context.Context, conn clickhouse.Conn, fetcher *pchainrpc.Fetcher, pchainID uint32) error {
	supply, supplyErr := fetcher.GetCurrentSupply(ctx)

	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()

	// own_stake sums in UInt128 and clamps: some L1s use weights near the
	// UInt64 range and a plain sum() would silently wrap.
	err := conn.Exec(wctx, `
		INSERT INTO staking_snapshots (
			snapshot_date, subnet_id, validator_count, delegation_count,
			own_stake, delegated_stake, total_supply, p_chain_id
		)
		SELECT
			toDate(?) AS snapshot_date,
			subnet_id,
			toUInt32(count()) AS validator_count,
			toUInt32(sum(delegator_count)) AS delegation_count,
			toUInt64(least(sum(toUInt128(weight)), toUInt128(18446744073709551615))) AS own_stake,
			toUInt64(sum(delegated_stake)) AS delegated_stake,
			if(subnet_id = ?, toUInt64(?), toUInt64(0)) AS total_supply,
			toUInt32(?) AS p_chain_id
		FROM (SELECT * FROM l1_validator_state FINAL)
		WHERE p_chain_id = ? AND active = true
		GROUP BY subnet_id
	`, time.Now().UTC().Format("2006-01-02"), PrimaryNetworkSubnetID, supply, pchainID, pchainID)
	if err != nil {
		return fmt.Errorf("failed to insert staking snapshot: %w", err)
	}
	if supplyErr != nil {
		return fmt.Errorf("staking snapshot written without supply: %w", supplyErr)
	}
	return nil
}
