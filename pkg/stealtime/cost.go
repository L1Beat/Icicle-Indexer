package stealtime

import (
	"context"
	"math/big"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/kmeasure"
	"icicle/pkg/lending"
	"icicle/pkg/prefilter"
)

var mul1e10 = new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil)

// buildBlockCost builds a CostModel pinned to the block: base fee from the block
// header, AVAX price from Chainlink at the block, and Aave's flash premium at the
// block. Each falls back to a conservative default if its read fails.
func buildBlockCost(ctx context.Context, rpc *lending.Client, block uint64, aavePool common.Address, gasUnits uint64, minProfit *big.Int) prefilter.CostModel {
	cm := prefilter.CostModel{
		GasUnits:         gasUnits,
		MinProfitUSD1e18: minProfit,
		FlashFeeBps:      5,
		GasPriceWei:      big.NewInt(25_000_000_000),
		NativeUSD1e18:    new(big.Int).Mul(big.NewInt(20), lending.WAD),
	}
	if bf, err := rpc.BlockBaseFee(ctx, block); err == nil && bf.Sign() > 0 {
		cm.GasPriceWei = bf
	}
	if a := callUintAt(ctx, rpc, kmeasure.ChainlinkAvaxUsd, "latestAnswer()", block); a != nil && a.Sign() > 0 {
		cm.NativeUSD1e18 = new(big.Int).Mul(a, mul1e10) // 1e8 -> 1e18
	}
	if aavePool != (common.Address{}) {
		if f := callUintAt(ctx, rpc, aavePool, "FLASHLOAN_PREMIUM_TOTAL()", block); f != nil && f.Sign() > 0 {
			cm.FlashFeeBps = f.Uint64()
		}
	}
	return cm
}

func callUintAt(ctx context.Context, rpc *lending.Client, to common.Address, sig string, block uint64) *big.Int {
	res, err := rpc.EthCall(ctx, to.Hex(), lending.EncodeCall0(sig), blockHex(block))
	if err != nil {
		return nil
	}
	return lending.Word(lending.DecodeHexBytes(res), 0)
}
