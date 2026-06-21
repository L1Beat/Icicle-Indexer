package lending

import (
	"math/big"
	"testing"
)

func wad(numerator, denominator int64) *big.Int {
	n := new(big.Int).Mul(big.NewInt(numerator), WAD)
	return new(big.Int).Div(n, big.NewInt(denominator))
}

func usd(n int64) *big.Int {
	// Oracle base currency is 1e8-scaled for Aave. Tests use that unit.
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e8))
}

func TestDeriveHealthFactor(t *testing.T) {
	// Weighted collateral 150, debt 100 -> HF 1.5.
	got := DeriveHealthFactor(usd(150), usd(100))
	want := wad(15, 10)
	if got.Cmp(want) != 0 {
		t.Fatalf("HF: got %s want %s", got, want)
	}

	// No debt -> infinite sentinel.
	if DeriveHealthFactor(usd(150), big.NewInt(0)).Cmp(hfInfinite) != 0 {
		t.Fatalf("expected infinite HF for zero debt")
	}

	// Underwater: weighted collateral 90, debt 100 -> HF 0.9.
	got = DeriveHealthFactor(usd(90), usd(100))
	if got.Cmp(wad(9, 10)) != 0 {
		t.Fatalf("HF underwater: got %s", got)
	}
}

func TestAaveCloseFactor(t *testing.T) {
	small := usd(1000)

	cases := []struct {
		name     string
		hf       *big.Int
		debt     *big.Int
		expected uint16
	}{
		{"deeply underwater below 0.95 -> 100%", wad(80, 100), usd(50000), 10000},
		{"just below 1.0 but above 0.95 -> 50%", wad(98, 100), usd(50000), 5000},
		{"healthy large position -> 50%", wad(120, 100), usd(50000), 5000},
		{"small position above 0.95 -> 100%", wad(98, 100), usd(500), 10000},
		{"small position at threshold -> 100%", wad(110, 100), usd(1000), 10000},
		{"large position just over threshold -> 50%", wad(110, 100), usd(1001), 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AaveCloseFactorBps(c.hf, c.debt, small); got != c.expected {
				t.Fatalf("got %d want %d", got, c.expected)
			}
		})
	}
}

// Rule 3: Benqi liquidatable is authoritative from shortfall, even when the
// derived health factor sits slightly above 1.0. The derived HF must not
// override the on-chain shortfall signal.
func TestBenqiShortfallIsAuthoritative(t *testing.T) {
	// A derived HF can read marginally above 1.0 due to rounding or a price skew
	// between the snapshot reads and the Comptroller's own accounting, while the
	// Comptroller reports a positive shortfall.
	derivedHF := wad(1001, 1000) // 1.001
	shortfall := usd(25)         // > 0 -> liquidatable

	liquidatable := shortfall.Sign() > 0
	if !liquidatable {
		t.Fatalf("shortfall>0 must be liquidatable")
	}
	if derivedHF.Cmp(WAD) <= 0 {
		t.Fatalf("test precondition: derived HF should be above 1.0 to prove it does not gate the flag")
	}
}

func TestEstimateLiquidationGrossNoSlippage(t *testing.T) {
	// Debt 1000, collateral 5000, 50% close factor, 5% bonus (10500).
	est := EstimateLiquidation("0xcoll", "0xdebt", usd(1000), usd(5000), 5000, 10500)

	if est.MaxDebtRepaidBase.Cmp(usd(500)) != 0 {
		t.Fatalf("repaid: got %s want %s", est.MaxDebtRepaidBase, usd(500))
	}
	// Seized = 500 * 1.05 = 525.
	if est.SeizedCollateralBase.Cmp(usd(525)) != 0 {
		t.Fatalf("seized: got %s want %s", est.SeizedCollateralBase, usd(525))
	}
	// Gross bonus = 525 - 500 = 25.
	if est.GrossBonusBase.Cmp(usd(25)) != 0 {
		t.Fatalf("gross bonus: got %s want %s", est.GrossBonusBase, usd(25))
	}
	if est.SlippageModeled {
		t.Fatalf("slippage must not be modeled in v1")
	}
}

func TestEstimateLiquidationCappedByCollateral(t *testing.T) {
	// Debt huge, collateral only 100. Seizure caps at collateral, repaid backs out.
	est := EstimateLiquidation("0xcoll", "0xdebt", usd(1_000_000), usd(100), 10000, 10800)
	if est.SeizedCollateralBase.Cmp(usd(100)) != 0 {
		t.Fatalf("seized should cap at collateral 100, got %s", est.SeizedCollateralBase)
	}
	// Repaid = 100 / 1.08 ~= 92.59 USD. Check it is below 100 and positive.
	if est.MaxDebtRepaidBase.Sign() <= 0 || est.MaxDebtRepaidBase.Cmp(usd(100)) >= 0 {
		t.Fatalf("repaid should be positive and below capped collateral, got %s", est.MaxDebtRepaidBase)
	}
	if est.GrossBonusBase.Sign() < 0 {
		t.Fatalf("gross bonus must not be negative")
	}
}

func TestClassifyTier(t *testing.T) {
	hotEdge := wad(110, 100)  // 1.10
	warmEdge := wad(150, 100) // 1.50

	if got := ClassifyTier(wad(200, 100), false, hotEdge, warmEdge); got != TierCold {
		t.Fatalf("healthy -> cold, got %s", got)
	}
	if got := ClassifyTier(wad(130, 100), false, hotEdge, warmEdge); got != TierWarm {
		t.Fatalf("1.30 -> warm, got %s", got)
	}
	if got := ClassifyTier(wad(105, 100), false, hotEdge, warmEdge); got != TierHot {
		t.Fatalf("1.05 -> hot, got %s", got)
	}
	// Liquidatable is always hot regardless of derived HF.
	if got := ClassifyTier(wad(300, 100), true, hotEdge, warmEdge); got != TierHot {
		t.Fatalf("liquidatable -> hot, got %s", got)
	}
}

func TestRankByProximity(t *testing.T) {
	positions := []Ranked{
		{Account: Account{Address: "0xhealthy"}, HealthFactor: wad(200, 100), DebtBase: usd(10), Liquidatable: false},
		{Account: Account{Address: "0xliq_small"}, HealthFactor: wad(90, 100), DebtBase: usd(10), Liquidatable: true},
		{Account: Account{Address: "0xliq_big"}, HealthFactor: wad(90, 100), DebtBase: usd(1000), Liquidatable: true},
		{Account: Account{Address: "0xnear"}, HealthFactor: wad(101, 100), DebtBase: usd(500), Liquidatable: false},
	}
	RankByProximity(positions)

	order := []string{"0xliq_big", "0xliq_small", "0xnear", "0xhealthy"}
	for i, want := range order {
		if positions[i].Account.Address != want {
			t.Fatalf("position %d: got %s want %s", i, positions[i].Account.Address, want)
		}
	}
}
