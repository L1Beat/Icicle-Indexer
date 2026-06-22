package prefilter

import (
	"context"
	"math/big"
	"testing"

	"github.com/ava-labs/libevm/common"
)

// Token fixtures: address, decimals, USD price per whole token (1e18-scaled).
var (
	usdc  = common.HexToAddress("0x0000000000000000000000000000000000000001")
	wavax = common.HexToAddress("0x0000000000000000000000000000000000000002")
	illiq = common.HexToAddress("0x0000000000000000000000000000000000000003")
)

func pow10(d uint8) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil) }

func usd(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), one1e18) }

// baseValue derives the leg USD value (1e18) from a native amount and a price.
func baseValue(amount, price1e18 *big.Int, dec uint8) *big.Int {
	return mulDiv(amount, price1e18, pow10(dec))
}

// stubParams returns fixed risk parameters.
type stubParams struct{}

func (stubParams) BonusBps(string, common.Address) (uint64, bool) { return 800, true } // 8%
func (stubParams) CloseFactorBps(string, *big.Int) uint64         { return 5000 }       // 50%

// stubQuoter prices swaps off a table, with a per-input liquidity factor so we can
// model an illiquid collateral whose executable output is far below oracle value.
type stubQuoter struct {
	price map[common.Address]*big.Int
	liq   map[common.Address]uint64 // output kept, bps; default 9900 (1% slippage)
}

func (q stubQuoter) QuoteOut(_ context.Context, in common.Address, inDec uint8, out common.Address, outDec uint8, amountIn *big.Int) (*big.Int, error) {
	pin, pout := q.price[in], q.price[out]
	if pin == nil || pout == nil || pout.Sign() == 0 {
		return big.NewInt(0), nil
	}
	inUSD := mulDiv(amountIn, pin, pow10(inDec))
	outIdeal := mulDiv(inUSD, pow10(outDec), pout)
	keep := q.liq[in]
	if keep == 0 {
		keep = 9900
	}
	return applyBps(outIdeal, keep), nil
}

func debtLeg(asset common.Address, amount *big.Int, price *big.Int, dec uint8) AssetLeg {
	return AssetLeg{Asset: asset, Side: SideDebt, Amount: amount, Decimals: dec, BaseValue: baseValue(amount, price, dec)}
}

func collLeg(asset common.Address, amount *big.Int, price *big.Int, dec uint8) AssetLeg {
	return AssetLeg{Asset: asset, Side: SideCollateral, Amount: amount, Decimals: dec, BaseValue: baseValue(amount, price, dec)}
}

func TestComputeK(t *testing.T) {
	ctx := context.Background()
	p1, p25 := usd(1), usd(25) // $1 stable, $25 AVAX

	q := stubQuoter{
		price: map[common.Address]*big.Int{usdc: p1, wavax: p25, illiq: p1},
		liq:   map[common.Address]uint64{illiq: 4000}, // illiquid: only 40% of value out
	}

	usdcAmt := func(d int64) *big.Int { return new(big.Int).Mul(big.NewInt(d), pow10(6)) }
	avaxAmt := func(milli int64) *big.Int { return new(big.Int).Div(new(big.Int).Mul(big.NewInt(milli), pow10(18)), big.NewInt(1000)) }

	positions := []Position{
		{ // profitable: $10k USDC debt, $15k WAVAX collateral, deep liquidity
			Protocol: "aave-v3", Account: common.HexToAddress("0xaa"), HealthFactor: usd(1),
			Legs: []AssetLeg{debtLeg(usdc, usdcAmt(10000), p1, 6), collLeg(wavax, avaxAmt(600_000), p25, 18)},
		},
		{ // illiquid: collateral oracle value fine, executable output far below repay
			Protocol: "benqi", Account: common.HexToAddress("0xbb"), HealthFactor: usd(1),
			Legs: []AssetLeg{debtLeg(usdc, usdcAmt(10000), p1, 6), collLeg(illiq, new(big.Int).Mul(big.NewInt(15000), pow10(18)), p1, 18)},
		},
		{ // dust: tiny position, gas exceeds the gross bonus
			Protocol: "aave-v3", Account: common.HexToAddress("0xcc"), HealthFactor: usd(1),
			Legs: []AssetLeg{debtLeg(usdc, usdcAmt(5), p1, 6), collLeg(wavax, avaxAmt(300), p25, 18)},
		},
		{ // no pair: collateral only, no debt leg
			Protocol: "benqi", Account: common.HexToAddress("0xdd"), HealthFactor: usd(1),
			Legs: []AssetLeg{collLeg(wavax, avaxAmt(400_000), p25, 18)},
		},
	}

	cost := CostModel{
		FlashFeeBps:      5,
		GasUnits:         700000,
		GasPriceWei:      big.NewInt(25_000_000_000), // 25 gwei
		NativeUSD1e18:    usd(25),
		MinProfitUSD1e18: usd(10),
	}

	s, err := ComputeK(ctx, positions, stubParams{}, q, cost)
	if err != nil {
		t.Fatalf("ComputeK: %v", err)
	}

	t.Logf("Total=%d  K(profitable)=%d  byReason=%v", s.Total, s.Profitable, s.ByReason)
	for _, r := range s.Results {
		net := new(big.Float).Quo(new(big.Float).SetInt(r.NetProfitUSD), new(big.Float).SetInt(one1e18))
		t.Logf("  %-8s %s  reason=%-12s net=$%.2f", r.Protocol, r.Account.Hex()[:6], r.Reason, net)
	}

	if s.Total != 4 {
		t.Fatalf("Total: got %d want 4", s.Total)
	}
	if s.Profitable != 1 {
		t.Fatalf("K: got %d want 1", s.Profitable)
	}
	for reason, want := range map[Reason]int{ReasonProfitable: 1, ReasonIlliquid: 1, ReasonDust: 1, ReasonNoPair: 1} {
		if s.ByReason[reason] != want {
			t.Errorf("reason %s: got %d want %d", reason, s.ByReason[reason], want)
		}
	}
}
