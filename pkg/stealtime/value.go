package stealtime

import (
	"context"
	"math/big"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// valueRepaidUSD values a liquidation's repaid debt in USD (1e18) using the oracle
// at the liquidation block. It is a cheap pre-filter (one price read) to skip the
// expensive crossing and quote work on dust, the sub-cent bad-debt that dominates
// Avalanche liquidation counts. Returns 0 when it cannot value.
func valueRepaidUSD(ctx context.Context, rpc *lending.Client, oracle common.Address, protocol string, liq Liquidation, debtDecimals uint8, block uint64) *big.Int {
	if liq.RepayAmount == nil || liq.RepayAmount.Sign() == 0 || oracle == (common.Address{}) {
		return big.NewInt(0)
	}

	if protocol == "benqi" {
		// getUnderlyingPrice(market) is scaled 1e(36-dec); price * amount / 1e18 = USD 1e18.
		price := callUint1AddrAt(ctx, rpc, oracle, "getUnderlyingPrice(address)", liq.DebtAsset, block)
		if price == nil || price.Sign() == 0 {
			return big.NewInt(0)
		}
		return new(big.Int).Div(new(big.Int).Mul(price, liq.RepayAmount), lending.WAD)
	}

	// Aave getAssetPrice(asset) is 1e8 USD. value = amount * price / 10^dec, then 1e8 -> 1e18.
	price := callUint1AddrAt(ctx, rpc, oracle, "getAssetPrice(address)", liq.DebtAsset, block)
	if price == nil || price.Sign() == 0 {
		return big.NewInt(0)
	}
	v := new(big.Int).Mul(liq.RepayAmount, price)
	v.Mul(v, mul1e10)
	return v.Div(v, pow10(debtDecimals))
}

func callUint1AddrAt(ctx context.Context, rpc *lending.Client, to common.Address, sig string, arg common.Address, block uint64) *big.Int {
	res, err := rpc.EthCall(ctx, to.Hex(), lending.EncodeCall1Addr(sig, arg.Hex()), blockHex(block))
	if err != nil {
		return nil
	}
	return lending.Word(lending.DecodeHexBytes(res), 0)
}

func pow10(d uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)
}
