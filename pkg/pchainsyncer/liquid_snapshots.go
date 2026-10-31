package pchainsyncer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/liquidstaking"
	"icicle/pkg/pchainrpc"

	"github.com/ClickHouse/clickhouse-go/v2"
)

var liquidRPCClient = &http.Client{Timeout: 15 * time.Second}

// SnapshotLiquidStakingToday writes today's liquid_staking_snapshots row for
// each live protocol. nodeURL is the avalanchego base URL, whose C-Chain RPC
// serves the token reads. A protocol whose contract read fails is skipped, so
// a bad cycle never overwrites today's row with zeros.
func SnapshotLiquidStakingToday(ctx context.Context, conn clickhouse.Conn, nodeURL string) error {
	base := strings.TrimSuffix(strings.TrimRight(nodeURL, "/"), "/ext/bc/P")
	rpcURL := base + "/ext/bc/C/rpc"
	today := time.Now().UTC().Format("2006-01-02")

	var errs []error
	for _, p := range liquidstaking.Protocols {
		if p.Status != "live" || len(p.PChainAddresses) == 0 {
			continue
		}
		if err := snapshotLiquidProtocol(ctx, conn, rpcURL, today, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func snapshotLiquidProtocol(ctx context.Context, conn clickhouse.Conn, rpcURL, today string, p liquidstaking.Protocol) error {
	st, err := liquidstaking.ReadTokenState(ctx, liquidRPCClient, rpcURL, p)
	if err != nil {
		return err
	}

	// A delegation belongs to the protocol when any of its addresses is in the
	// rewards owner. The quoted CB58 match can't hit part of another address.
	var conds []string
	args := []interface{}{today, p.ID, st.PooledAvax.String(), st.TokenSupply.String(), PrimaryNetworkSubnetID}
	for _, a := range p.PChainAddresses {
		short, err := pchainrpc.ConvertPChainAddressToCB58(a)
		if err != nil {
			return fmt.Errorf("%s: %w", p.ID, err)
		}
		conds = append(conds, `position(ifNull(toJSONString(tx_data.rewardsOwner.addresses), ''), ?) > 0`)
		args = append(args, `"`+short+`"`)
	}
	args = append(args, time.Now().Unix())

	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()

	// Running delegations started within the last year, so the block_time bound
	// keeps the scan to recent txs. GROUP BY tx_id dedupes re-inserted txs.
	err = conn.Exec(wctx, `
		INSERT INTO liquid_staking_snapshots (
			snapshot_date, protocol_id, pooled_avax, token_supply,
			active_delegated, active_delegations, validator_count
		)
		SELECT
			toDate(?), ?, toUInt256(?), toUInt256(?),
			toUInt64(sum(stake)), toUInt32(count()), toUInt32(uniqExact(node))
		FROM (
			SELECT tx_id,
				any(toString(tx_data.validator.nodeID)) AS node,
				any(toUInt64OrZero(toString(tx_data.validator.weight))) AS stake
			FROM p_chain_txs
			WHERE p_chain_id = 0
			  AND tx_type IN ('AddDelegator', 'AddPermissionlessDelegator')
			  AND toString(tx_data.subnetID) IN ('', ?)
			  AND (`+strings.Join(conds, " OR ")+`)
			  AND block_time >= now() - INTERVAL 400 DAY
			  AND toUInt64OrZero(toString(tx_data.validator.end)) > ?
			GROUP BY tx_id
		)
	`, args...)
	if err != nil {
		return fmt.Errorf("failed to insert %s liquid staking snapshot: %w", p.ID, err)
	}
	return nil
}
