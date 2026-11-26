package lending

import (
	"encoding/hex"
	"math/big"

	"github.com/ava-labs/libevm/common"
)

// Liquidation-sizing helpers shared by the kmeasure and stealtime replays.

// LiquidationPremium converts a liquidation bonus multiplier in bps (10500) to
// the premium above 100% (500).
func LiquidationPremium(multiplierBps uint64) uint64 {
	if multiplierBps <= 10000 {
		return 0
	}
	return multiplierBps - 10000
}

// CloseFactorBps returns the close factor in bps. Benqi uses its global value
// (benqiCloseFactor, 50% when unknown); Aave allows 100% when the health
// factor is below 0.95, otherwise 50%.
func CloseFactorBps(protocol string, hf *big.Int, benqiCloseFactor uint64) uint64 {
	if protocol == "benqi" {
		if benqiCloseFactor > 0 {
			return benqiCloseFactor
		}
		return 5000
	}
	if hf != nil && hf.Sign() > 0 && hf.Cmp(hf095) < 0 {
		return 10000
	}
	return 5000
}

// EncodeGetAmountsOut builds calldata for a UniswapV2-style router's
// getAmountsOut(uint256 amountIn, address[] path).
func EncodeGetAmountsOut(amountIn *big.Int, path []common.Address) string {
	var b []byte
	b = append(b, u256Word(amountIn)...)
	b = append(b, u256Word(big.NewInt(0x40))...) // offset to the path array
	b = append(b, u256Word(big.NewInt(int64(len(path))))...)
	for _, a := range path {
		w := make([]byte, 32)
		copy(w[12:], a.Bytes())
		b = append(b, w...)
	}
	return Selector("getAmountsOut(uint256,address[])") + hex.EncodeToString(b)
}
