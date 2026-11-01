package restaking

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/stretchr/testify/require"
)

// fakeChain answers eth_call by (contract, calldata).
type fakeChain map[string][]byte

func (f fakeChain) Call(_ context.Context, to string, data []byte) ([]byte, error) {
	if out, ok := f[to+":"+hex.EncodeToString(data)]; ok {
		return out, nil
	}
	return nil, errors.New("execution reverted")
}

func (f fakeChain) on(to, sig string, out []byte, args ...[]byte) {
	data := selector(sig)
	for _, a := range args {
		data = append(data, a...)
	}
	f[to+":"+hex.EncodeToString(data)] = out
}

func u(n int64) []byte { return uintWord(big.NewInt(n)) }

func cat(ws ...[]byte) []byte {
	var b []byte
	for _, w := range ws {
		b = append(b, w...)
	}
	return b
}

// dynArray encodes a single dynamic array of words as a return value.
func dynArray(ws ...[]byte) []byte {
	return cat(u(32), u(int64(len(ws))), cat(ws...))
}

func abiStr(s string) []byte {
	b := []byte(s)
	pad := make([]byte, (32-len(b)%32)%32)
	return cat(u(32), u(int64(len(b))), b, pad)
}

const (
	balA = "0x00000000000000000000000000000000000000a1"
	mwA  = "0x00000000000000000000000000000000000000a2"
	vmA  = "0x00000000000000000000000000000000000000a3"
	balB = "0x00000000000000000000000000000000000000b1"
	mwB  = "0x00000000000000000000000000000000000000b2"
	vmB  = "0x00000000000000000000000000000000000000b3"
	op1  = "0x00000000000000000000000000000000000000c1"
	op2  = "0x00000000000000000000000000000000000000c2"
	vlt  = "0x00000000000000000000000000000000000000d1"
	off  = "0x00000000000000000000000000000000000000d2"
	dlg  = "0x00000000000000000000000000000000000000d3"
	dc   = "0x00000000000000000000000000000000000000e1"
	tok  = "0x00000000000000000000000000000000000000e2"
)

func TestReadSuzaku(t *testing.T) {
	f := fakeChain{}
	reg := "0xaA59b19A7636bf6d821aA124A14eEE6C92746110"
	// getAllL1s: (address[] l1s, address[] middlewares, string[] urls). The
	// url array is never decoded, so an empty one suffices.
	f.on(reg, "getAllL1s()", cat(u(96), u(192), u(288),
		u(2), addrWord(balA), addrWord(balB),
		u(2), addrWord(mwA), addrWord(mwB),
		u(0)))

	subnetA := ids.ID{1, 2, 3}
	f.on(balA, "subnetID()", subnetA[:])
	f.on(balA, "getSecurityModuleWeights(address)", cat(u(5_000_000), u(5_000_000)), addrWord(mwA))
	f.on(mwA, "getVaultManager()", addrWord(vmA))
	f.on(mwA, "getAllOperators()", dynArray(addrWord(op1), addrWord(op2)))
	vid := ids.ID{9}
	f.on(mwA, "getOperatorValidationIDs(address)", dynArray(vid[:]), addrWord(op1))
	f.on(mwA, "getOperatorValidationIDs(address)", dynArray(), addrWord(op2))
	f.on(vmA, "getVaultCount()", u(2))
	f.on(vmA, "getVaultAtWithTimes(uint256)", cat(addrWord(vlt), u(100), u(0)), u(0))
	f.on(vmA, "getVaultAtWithTimes(uint256)", cat(addrWord(off), u(100), u(200)), u(1)) // disabled
	f.on(vmA, "getVaultCollateralClass(address)", u(1), addrWord(vlt))
	f.on(vlt, "collateral()", addrWord(dc))
	f.on(vlt, "activeStake()", u(13_000))
	f.on(vlt, "delegator()", addrWord(dlg))
	f.on(dlg, "l1Limit(address,uint96)", u(5_000), addrWord(balA), u(1))
	f.on(dlg, "stake(address,uint96,address)", u(5_000), addrWord(balA), u(1), addrWord(op1))
	f.on(dlg, "stake(address,uint96,address)", u(0), addrWord(balA), u(1), addrWord(op2))
	f.on(dc, "asset()", addrWord(tok))
	f.on(tok, "symbol()", abiStr("ALOT"))
	f.on(tok, "decimals()", u(18))

	// L1 B has no vaults and is left out.
	f.on(balB, "subnetID()", make([]byte, 32))
	f.on(balB, "getSecurityModuleWeights(address)", cat(u(0), u(1)), addrWord(mwB))
	f.on(mwB, "getVaultManager()", addrWord(vmB))
	f.on(mwB, "getAllOperators()", dynArray())
	f.on(vmB, "getVaultCount()", u(0))

	l1s, err := ReadSuzaku(context.Background(), f)
	require.NoError(t, err)
	require.Len(t, l1s, 1)
	l1 := l1s[0]
	require.Equal(t, subnetA.String(), l1.SubnetID)
	require.Equal(t, balA, l1.Balancer)
	require.Equal(t, uint64(5_000_000), l1.ModuleWeight)
	require.Equal(t, uint32(1), l1.OperatorCount)
	require.Equal(t, []string{vid.String()}, l1.ValidationIDs)
	require.Len(t, l1.Vaults, 1)
	v := l1.Vaults[0]
	require.Equal(t, vlt, v.Address)
	require.Equal(t, dc, v.Collateral)
	require.Equal(t, tok, v.Token)
	require.Equal(t, "ALOT", v.Symbol)
	require.Equal(t, uint8(18), v.Decimals)
	require.Equal(t, int64(13_000), v.Deposited.Int64())
	require.Equal(t, int64(5_000), v.Allocated.Int64())
	require.Equal(t, int64(5_000), v.Limit.Int64())
}

func TestReadSuzaku_BrokenL1Skipped(t *testing.T) {
	f := fakeChain{}
	f.on(SuzakuL1Registry, "getAllL1s()", cat(u(96), u(160), u(224), u(1), addrWord(balA), u(1), addrWord(mwA), u(0)))
	// balA.subnetID() is missing, so the read reverts.
	l1s, err := ReadSuzaku(context.Background(), f)
	require.Error(t, err)
	require.Empty(t, l1s)
}

func TestUnwrappedCollateral(t *testing.T) {
	f := fakeChain{}
	f.on(vmA, "getVaultCollateralClass(address)", u(1), addrWord(vlt))
	f.on(vlt, "collateral()", addrWord(tok))
	f.on(vlt, "activeStake()", u(7))
	f.on(vlt, "delegator()", addrWord(dlg))
	f.on(dlg, "l1Limit(address,uint96)", u(0), addrWord(balA), u(1))
	f.on(tok, "symbol()", abiStr("A-VERY-LONG-TOKEN-SYMBOL-OVER-32-BYTES"))
	f.on(tok, "decimals()", u(6))
	v, err := reader{context.Background(), f}.readVault(vmA, balA, vlt, nil, map[string]bool{})
	require.NoError(t, err)
	require.Equal(t, tok, v.Token) // asset() reverts: the collateral is the token
	require.Equal(t, "A-VERY-LONG-TOKEN-SYMBOL-OVER-32-BYTES", v.Symbol)
	require.Equal(t, int64(0), v.Allocated.Int64())
}
