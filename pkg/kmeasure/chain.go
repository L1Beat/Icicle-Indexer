// Package kmeasure is the read-only Stage 1 K-measurement runner. It loads the
// standing liquidatable set from the lending feed API, drives pkg/prefilter
// against live on-chain quotes, and reports K: how many liquidatable positions
// are actually profitable after bonus, real swap slippage, the flash-loan fee,
// and gas.
//
// This tool holds no keys, signs nothing, submits no transactions, and deploys
// no contracts. It is a diagnostic. No em dashes anywhere, per house style.
package kmeasure

import (
	"context"
	"math/big"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// EthReader is the minimal on-chain read surface the runner needs. lending.Client
// satisfies it, and tests substitute a stub.
type EthReader interface {
	EthCall(ctx context.Context, to, data, block string) (string, error)
	BlockNumber(ctx context.Context) (uint64, error)
	GasPrice(ctx context.Context) (*big.Int, error)
}

// parseBig decodes a base-10 integer string. Empty or invalid yields 0, never an
// error, since the feed uses "0" and occasionally omits values.
func parseBig(s string) *big.Int {
	if s == "" {
		return big.NewInt(0)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}

// callAddr reads a no-argument function returning a single address.
func callAddr(ctx context.Context, r EthReader, to common.Address, sig string) (common.Address, error) {
	res, err := r.EthCall(ctx, to.Hex(), lending.EncodeCall0(sig), "latest")
	if err != nil {
		return common.Address{}, err
	}
	return common.HexToAddress(lending.Addr(lending.DecodeHexBytes(res), 0)), nil
}

// callUint reads a no-argument function returning a single uint.
func callUint(ctx context.Context, r EthReader, to common.Address, sig string) (*big.Int, error) {
	res, err := r.EthCall(ctx, to.Hex(), lending.EncodeCall0(sig), "latest")
	if err != nil {
		return nil, err
	}
	return lending.Word(lending.DecodeHexBytes(res), 0), nil
}
