package stealtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// Candidate-event topics. A position's liquidatable status changes only at a price
// update or its own worsening position change, so these blocks are the exact
// candidates for the crossing, giving block-precise steal_time.
var (
	topicAnswerUpdated = lending.EventTopic("AnswerUpdated(int256,uint256,uint256)")
	topicAaveBorrow    = lending.EventTopic("Borrow(address,address,address,uint256,uint8,uint256,uint16)")
	topicAaveWithdraw  = lending.EventTopic("Withdraw(address,address,address,uint256)")
	topicAaveCollatOff = lending.EventTopic("ReserveUsedAsCollateralDisabled(address,address)")
)

// gatherCandidates returns block-precise candidate crossing blocks: AnswerUpdated
// logs on the aggregators backing the position's assets (resolved via the Aave
// oracle, which prices the underlyings both protocols use), plus the account's own
// worsening Aave position events. Benqi position-change candidates are omitted;
// price candidates cover the common, price-driven crossing.
func gatherCandidates(ctx context.Context, conn driver.Conn, rpc *lending.Client, chainID uint32, oracle, aavePool, account common.Address, protocol string, assets []common.Address, floor, taken uint64, aggCache map[common.Address]common.Address) ([]uint64, error) {
	var blocks []uint64

	for _, asset := range assets {
		if asset == (common.Address{}) {
			continue
		}
		agg := resolveAggregator(ctx, rpc, oracle, asset, aggCache)
		if agg == (common.Address{}) {
			continue
		}
		where := fmt.Sprintf("address IN (%s) AND topic0 = unhex('%s')", unhexAddr(agg), trim0x(topicAnswerUpdated))
		bs, err := queryBlocks(ctx, conn, where, chainID, floor, taken)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, bs...)
	}

	if protocol == "aave-v3" && aavePool != (common.Address{}) {
		topics := topicInList([]string{topicAaveBorrow, topicAaveWithdraw, topicAaveCollatOff})
		where := fmt.Sprintf("address IN (%s) AND topic0 IN (%s) AND topic2 = unhex('%s')",
			unhexAddr(aavePool), topics, topicForAddr(account))
		bs, err := queryBlocks(ctx, conn, where, chainID, floor, taken)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, bs...)
	}

	return blocks, nil
}

func queryBlocks(ctx context.Context, conn driver.Conn, where string, chainID uint32, floor, taken uint64) ([]uint64, error) {
	q := fmt.Sprintf(`SELECT DISTINCT block_number FROM raw_logs
		WHERE chain_id = ? AND block_number >= ? AND block_number <= ? AND %s`, where)
	rows, err := conn.Query(ctx, q, chainID, floor, taken)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var b uint32
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, uint64(b))
	}
	return out, rows.Err()
}

// resolveAggregator maps an underlying asset to its Chainlink aggregator via the
// Aave oracle (getSourceOfAsset then the proxy's aggregator()), cached per asset.
func resolveAggregator(ctx context.Context, rpc *lending.Client, oracle, asset common.Address, cache map[common.Address]common.Address) common.Address {
	if a, ok := cache[asset]; ok {
		return a
	}
	agg := common.Address{}
	src := callAddrLatest(ctx, rpc, oracle, lending.EncodeCall1Addr("getSourceOfAsset(address)", asset.Hex()))
	if src != (common.Address{}) {
		agg = callAddrLatest(ctx, rpc, src, lending.EncodeCall0("aggregator()"))
	}
	cache[asset] = agg
	return agg
}

func callAddrLatest(ctx context.Context, rpc *lending.Client, to common.Address, data string) common.Address {
	res, err := rpc.EthCall(ctx, to.Hex(), data, "latest")
	if err != nil {
		return common.Address{}
	}
	return common.HexToAddress(lending.Addr(lending.DecodeHexBytes(res), 0))
}

// --- SQL literal helpers (inputs are validated hex, injection-safe) ---

func trim0x(s string) string { return strings.TrimPrefix(strings.ToLower(s), "0x") }

func unhexAddr(a common.Address) string {
	return "unhex('" + trim0x(a.Hex()) + "')"
}

func topicForAddr(a common.Address) string {
	return strings.Repeat("0", 24) + trim0x(a.Hex())
}

func topicInList(topics []string) string {
	parts := make([]string, 0, len(topics))
	for _, t := range topics {
		parts = append(parts, "unhex('"+trim0x(t)+"')")
	}
	return strings.Join(parts, ", ")
}
