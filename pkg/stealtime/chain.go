package stealtime

import (
	"context"
	"strconv"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

func blockHex(b uint64) string { return "0x" + strconv.FormatUint(b, 16) }

// executeProbeAt runs a lending health probe pinned to a historical block. It
// tries Multicall3 aggregate3, and falls back to individual eth_calls when
// Multicall3 has no code at that block or the batch reverts. This reuses the live
// adapter's per-asset assembler (BuildProbe + Decode) without changing it.
func executeProbeAt(ctx context.Context, rpc *lending.Client, probe lending.HealthProbe, block uint64) lending.Health {
	bh := blockHex(block)

	data := lending.EncodeAggregate3(probe.Calls)
	if res, err := rpc.EthCall(ctx, lending.Multicall3Address, data, bh); err == nil {
		if results, derr := lending.DecodeAggregate3(lending.DecodeHexBytes(res)); derr == nil && len(results) == len(probe.Calls) {
			return probe.Decode(results, block)
		}
	}

	results := make([]lending.CallResult, len(probe.Calls))
	for i, c := range probe.Calls {
		r, err := rpc.EthCall(ctx, c.Target, c.Data, bh)
		if err != nil {
			results[i] = lending.CallResult{Success: false}
			continue
		}
		results[i] = lending.CallResult{Success: true, ReturnData: lending.DecodeHexBytes(r)}
	}
	return probe.Decode(results, block)
}

// liquidatableAtBlock is the authoritative on-chain liquidatable check pinned to a
// block: Aave healthFactor < 1e18 with debt present, Benqi shortfall > 0.
func liquidatableAtBlock(ctx context.Context, rpc *lending.Client, protocol string, account, poolOrComptroller common.Address, block uint64) (bool, error) {
	bh := blockHex(block)
	if protocol == "benqi" {
		res, err := rpc.EthCall(ctx, poolOrComptroller.Hex(), lending.EncodeCall1Addr("getAccountLiquidity(address)", account.Hex()), bh)
		if err != nil {
			return false, err
		}
		b := lending.DecodeHexBytes(res)
		return lending.Word(b, 2).Sign() > 0, nil // shortfall
	}

	res, err := rpc.EthCall(ctx, poolOrComptroller.Hex(), lending.EncodeCall1Addr("getUserAccountData(address)", account.Hex()), bh)
	if err != nil {
		return false, err
	}
	b := lending.DecodeHexBytes(res)
	debt := lending.Word(b, 1)
	hf := lending.Word(b, 5)
	return debt.Sign() > 0 && hf.Cmp(lending.WAD) < 0, nil
}
