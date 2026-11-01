package lendingmarkets

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

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
	f[to+":"+hex.EncodeToString(encode(sig, args...))] = out
}

func u(n int64) []byte { return uintWord(uint64(n)) }

func ub(s string) []byte {
	n, _ := new(big.Int).SetString(s, 10)
	return n.FillBytes(make([]byte, 32))
}

func cat(ws ...[]byte) []byte {
	var b []byte
	for _, w := range ws {
		b = append(b, w...)
	}
	return b
}

func abiStr(s string) []byte {
	b := []byte(s)
	pad := make([]byte, (32-len(b)%32)%32)
	return cat(u(32), u(int64(len(b))), b, pad)
}

func (f fakeChain) erc20(addr, symbol string, decimals int64) {
	f.on(addr, "symbol()", abiStr(symbol))
	f.on(addr, "decimals()", u(decimals))
}

const (
	pool     = "0x00000000000000000000000000000000000000a1"
	dp       = "0x00000000000000000000000000000000000000a2"
	aOracle  = "0x00000000000000000000000000000000000000a3"
	qiAVAX   = "0x00000000000000000000000000000000000000b1"
	qiUSDC   = "0x00000000000000000000000000000000000000b2"
	usdc     = "0x00000000000000000000000000000000000000b3"
	bOracle  = "0x00000000000000000000000000000000000000b4"
	v4Oracle = "0x00000000000000000000000000000000000000c1"
)

func TestReadAaveV3(t *testing.T) {
	f := fakeChain{}
	f.on(AaveV3Provider, "getPool()", addrWord(pool))
	f.on(AaveV3Provider, "getPoolDataProvider()", addrWord(dp))
	f.on(AaveV3Provider, "getPriceOracle()", addrWord(aOracle))
	f.on(aOracle, "BASE_CURRENCY_UNIT()", u(1e8))
	// TokenData[] with one (symbol "WAVAX", address WAVAX) element.
	f.on(dp, "getAllReservesTokens()", cat(u(32), u(1), u(32), u(64), addrWord(WAVAX), abiStr("WAVAX")[32:]))
	// decimals, ltv, lt, bonus, reserveFactor, collateral, borrowing, stable, active, frozen
	f.on(dp, "getReserveConfigurationData(address)", cat(u(18), u(0), u(7300), u(10900), u(2000), u(1), u(1), u(0), u(1), u(0)), addrWord(WAVAX))
	f.on(dp, "getReserveData(address)", cat(u(0), u(0), ub("16000000000000000000000000"), u(0), ub("6000000000000000000000000"),
		ub("10000000000000000000000000"), ub("30000000000000000000000000"), u(0), u(0), u(0), u(0), u(0)), addrWord(WAVAX))
	f.on(dp, "getReserveCaps(address)", cat(u(6000000), u(0)), addrWord(WAVAX))
	f.on(aOracle, "getAssetPrice(address)", u(866882000), addrWord(WAVAX))

	ms, err := ReadAaveV3(context.Background(), f)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	m := ms[0]
	require.Equal(t, "WAVAX", m.Symbol)
	require.Equal(t, WAVAX, m.Asset)
	require.Equal(t, pool, m.Market)
	require.Equal(t, "6000000000000000000000000", m.Borrowed.String())
	require.InDelta(t, 0.01, m.SupplyAPR, 1e-12)
	require.InDelta(t, 0.03, m.BorrowAPR, 1e-12)
	require.Equal(t, uint16(0), *m.LTVBps)
	require.Equal(t, uint16(7300), *m.LiqThresholdBps)
	require.True(t, *m.CollateralEnabled)
	require.InDelta(t, 8.66882, *m.PriceUSD, 1e-9)
	require.Equal(t, "6000000000000000000000000", m.BorrowCap.String())
	require.Nil(t, m.SupplyCap)
}

func TestReadBenqi(t *testing.T) {
	f := fakeChain{}
	f.on(BenqiComptroller, "oracle()", addrWord(bOracle))
	f.on(BenqiComptroller, "getAllMarkets()", cat(u(32), u(2), addrWord(qiAVAX), addrWord(qiUSDC)))
	f.on(BenqiComptroller, "markets(address)", cat(u(1), ub("740000000000000000"), u(0)), addrWord(qiAVAX))
	f.on(BenqiComptroller, "markets(address)", cat(u(1), u(0), u(0)), addrWord(qiUSDC))
	f.on(BenqiComptroller, "borrowCaps(address)", ub("350000000000000000000000"), addrWord(qiAVAX))
	f.on(BenqiComptroller, "borrowCaps(address)", u(0), addrWord(qiUSDC))
	// qiAVAX has no underlying(), so it reverts.
	f.on(qiUSDC, "underlying()", addrWord(usdc))
	f.erc20(usdc, "USDC", 6)
	for _, q := range []string{qiAVAX, qiUSDC} {
		f.on(q, "getCash()", u(500))
		f.on(q, "totalBorrows()", u(350))
		f.on(q, "totalReserves()", u(50))
		f.on(q, "supplyRatePerTimestamp()", u(352857338))
		f.on(q, "borrowRatePerTimestamp()", u(1020773569))
	}
	f.on(bOracle, "getUnderlyingPrice(address)", ub("8668820000000000000"), addrWord(qiAVAX))
	// qiUSDC price read fails: price stays nil.

	ms, err := ReadBenqi(context.Background(), f)
	require.NoError(t, err)
	require.Len(t, ms, 2)

	avax := ms[0]
	require.Equal(t, NativeAVAX, avax.Asset)
	require.Equal(t, "AVAX", avax.Symbol)
	require.Equal(t, qiAVAX, avax.Market)
	require.Equal(t, "800", avax.Supplied.String()) // cash + borrows - reserves
	require.Equal(t, uint16(7400), *avax.LTVBps)
	require.Equal(t, uint16(7400), *avax.LiqThresholdBps)
	require.True(t, *avax.CollateralEnabled)
	require.InDelta(t, 0.011128, avax.SupplyAPR, 1e-6)
	require.InDelta(t, 8.66882, *avax.PriceUSD, 1e-9)
	require.Equal(t, "350000000000000000000000", avax.BorrowCap.String())

	usd := ms[1]
	require.Equal(t, usdc, usd.Asset)
	require.Equal(t, uint8(6), usd.Decimals)
	require.False(t, *usd.CollateralEnabled)
	require.Nil(t, usd.PriceUSD)
	require.Nil(t, usd.BorrowCap)
	require.Nil(t, usd.SupplyCap)
}

func TestReadAaveV4(t *testing.T) {
	f := fakeChain{}
	hub := AaveV4CoreHub
	f.on(hub, "getAssetCount()", u(1))
	main := AaveV4Spokes[0]
	f.on(main, "ORACLE()", addrWord(v4Oracle))
	id := u(0)
	f.on(hub, "getAssetUnderlyingAndDecimals(uint256)", cat(addrWord(WAVAX), u(18)), id)
	f.on(hub, "getAddedAssets(uint256)", ub("2000000000000000000000000"), id)
	f.on(hub, "getAssetTotalOwed(uint256)", ub("500000000000000000000000"), id)
	f.on(hub, "getAssetDrawnRate(uint256)", ub("40000000000000000000000000"), id)
	f.on(hub, "getAssetConfig(uint256)", cat(addrWord(pool), u(2000), addrWord(pool), addrWord(pool)), id)
	f.erc20(WAVAX, "WAVAX", 18)
	f.on(main, "getReserveId(address,uint256)", u(3), addrWord(hub), id)
	f.on(v4Oracle, "getReservePrice(uint256)", u(866658000), u(3))

	ms, err := ReadAaveV4(context.Background(), f)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	m := ms[0]
	require.Equal(t, "WAVAX", m.Symbol)
	require.InDelta(t, 0.04, m.BorrowAPR, 1e-12)
	require.InDelta(t, 0.04*0.25*0.8, m.SupplyAPR, 1e-12) // utilization 25%, 20% fee
	require.Nil(t, m.CollateralEnabled)
	require.Nil(t, m.LTVBps)
	require.InDelta(t, 8.66658, *m.PriceUSD, 1e-9)
}

// A protocol that fails is left out and reported, the others still return.
func TestReadAll_PartialFailure(t *testing.T) {
	f := fakeChain{}
	f.on(BenqiComptroller, "oracle()", addrWord(bOracle))
	f.on(BenqiComptroller, "getAllMarkets()", cat(u(32), u(0)))
	f.on(AaveV4CoreHub, "getAssetCount()", u(0))
	ms, err := ReadAll(context.Background(), f)
	require.Error(t, err)
	require.Contains(t, err.Error(), ProtocolAaveV3)
	require.Empty(t, ms)
}
