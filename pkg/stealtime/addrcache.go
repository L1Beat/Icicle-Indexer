package stealtime

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
	"icicle/pkg/lending/aave"
)

// addrResolver resolves protocol addresses as of a historical block via the stable
// anchors (Aave PoolAddressesProvider, Benqi Comptroller). The oracle and market
// list change over time, so resolving them at head and reading state at an old
// block makes price reads revert and drops every leg. Results are cached per
// coarse block bucket since these addresses change rarely.
type addrResolver struct {
	rpc              *lending.Client
	aaveProvider     common.Address
	benqiComptroller common.Address
	stride           uint64

	mu    sync.Mutex
	cache map[string]lending.Addresses
}

func newAddrResolver(rpc *lending.Client, benqiComptroller common.Address, stride uint64) *addrResolver {
	if stride == 0 {
		stride = 43200
	}
	return &addrResolver{
		rpc:              rpc,
		aaveProvider:     common.HexToAddress(aave.DefaultProvider),
		benqiComptroller: benqiComptroller,
		stride:           stride,
		cache:            map[string]lending.Addresses{},
	}
}

func (r *addrResolver) at(ctx context.Context, protocol string, block uint64) lending.Addresses {
	key := fmt.Sprintf("%s|%d", protocol, block/r.stride)
	r.mu.Lock()
	if a, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return a
	}
	r.mu.Unlock()

	var a lending.Addresses
	if protocol == "benqi" {
		a.Comptroller = r.benqiComptroller.Hex()
		a.Oracle = callAddrAt(ctx, r.rpc, r.benqiComptroller, "oracle()", block).Hex()
		a.Markets = marketsAt(ctx, r.rpc, r.benqiComptroller, block)
	} else {
		a.Provider = r.aaveProvider.Hex()
		a.Pool = callAddrAt(ctx, r.rpc, r.aaveProvider, "getPool()", block).Hex()
		a.Oracle = callAddrAt(ctx, r.rpc, r.aaveProvider, "getPriceOracle()", block).Hex()
		a.DataProvider = callAddrAt(ctx, r.rpc, r.aaveProvider, "getPoolDataProvider()", block).Hex()
	}

	r.mu.Lock()
	r.cache[key] = a
	r.mu.Unlock()
	return a
}

func callAddrAt(ctx context.Context, rpc *lending.Client, to common.Address, sig string, block uint64) common.Address {
	res, err := rpc.EthCall(ctx, to.Hex(), lending.EncodeCall0(sig), blockHex(block))
	if err != nil {
		return common.Address{}
	}
	return common.HexToAddress(lending.Addr(lending.DecodeHexBytes(res), 0))
}

// marketsAt decodes Comptroller.getAllMarkets() (address[]) as of a block.
func marketsAt(ctx context.Context, rpc *lending.Client, comptroller common.Address, block uint64) []string {
	res, err := rpc.EthCall(ctx, comptroller.Hex(), lending.EncodeCall0("getAllMarkets()"), blockHex(block))
	if err != nil {
		return nil
	}
	b := lending.DecodeHexBytes(res)
	if len(b) < 64 {
		return nil
	}
	arrBase := int(lending.Word(b, 0).Uint64())
	if arrBase+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[arrBase : arrBase+32]).Uint64())
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		off := arrBase + 32 + i*32
		if off+32 > len(b) {
			break
		}
		out = append(out, lending.Addr(b[off:off+32], 0))
	}
	return out
}
