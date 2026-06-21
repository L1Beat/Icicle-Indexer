package lending

import (
	"math/big"
	"sort"
)

// WAD is the 1e18 fixed-point scale used by Aave health factors and Benqi
// mantissas.
var WAD = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

// hf095 is 0.95 in WAD, the Aave threshold below which the close factor is 100%.
var hf095 = new(big.Int).Div(new(big.Int).Mul(big.NewInt(95), WAD), big.NewInt(100))

// hfInfinite is the sentinel health factor for a position with no debt.
var hfInfinite = new(big.Int).Mul(WAD, big.NewInt(1_000_000_000))

// DeriveHealthFactor computes a 1e18-scaled health factor from weighted
// collateral and debt, both in the oracle base currency. This is used for Benqi
// so its positions rank alongside Aave, and for display. It is never the source
// of the Benqi liquidatable flag, which comes from on-chain shortfall (rule 3).
func DeriveHealthFactor(weightedCollateralBase, debtBase *big.Int) *big.Int {
	if debtBase == nil || debtBase.Sign() == 0 {
		return new(big.Int).Set(hfInfinite)
	}
	if weightedCollateralBase == nil {
		weightedCollateralBase = big.NewInt(0)
	}
	return new(big.Int).Div(new(big.Int).Mul(weightedCollateralBase, WAD), debtBase)
}

// AaveCloseFactorBps returns the close factor in basis points. Aave v3 allows up
// to 100% when the health factor is below 0.95 or the position is small, and 50%
// otherwise.
func AaveCloseFactorBps(hf, totalDebtBase, smallThresholdBase *big.Int) uint16 {
	if hf != nil && hf.Cmp(hf095) < 0 {
		return 10000
	}
	if smallThresholdBase != nil && smallThresholdBase.Sign() > 0 &&
		totalDebtBase != nil && totalDebtBase.Cmp(smallThresholdBase) <= 0 {
		return 10000
	}
	return 5000
}

// ClassifyTier assigns a refresh tier from health. Liquidatable and near-line
// positions are hot, moderately healthy are warm, deeply healthy are cold. The
// band edges are caller-provided in WAD so they stay configurable.
func ClassifyTier(hf *big.Int, liquidatable bool, hotEdge, warmEdge *big.Int) Tier {
	if liquidatable {
		return TierHot
	}
	if hf == nil {
		return TierCold
	}
	if hf.Cmp(hotEdge) < 0 {
		return TierHot
	}
	if hf.Cmp(warmEdge) < 0 {
		return TierWarm
	}
	return TierCold
}

// LiquidationEstimate is the informational sizing a searcher needs. v1 reports
// gross numbers only: there is no DEX liquidity wired in this phase, so slippage
// is not modeled and is never silently subtracted (rule 4).
type LiquidationEstimate struct {
	CollateralAsset      string
	DebtAsset            string
	CloseFactorBps       uint16
	LiquidationBonusBps  uint16
	MaxDebtRepaidBase    *big.Int // base-currency value of debt a liquidator may repay
	SeizedCollateralBase *big.Int // base-currency value of collateral seized, including bonus
	GrossBonusBase       *big.Int // seized minus repaid, the gross incentive before costs
	SlippageModeled      bool     // always false in v1
}

// EstimateLiquidation sizes a single collateral/debt pair. bonusBps is the
// collateral multiplier in basis points (10500 = 105%). The seizable amount is
// capped by available collateral, and the repaid value is scaled down to match
// when the cap binds.
func EstimateLiquidation(collateralAsset, debtAsset string, debtBase, collateralBase *big.Int, closeFactorBps, bonusBps uint16) LiquidationEstimate {
	if debtBase == nil {
		debtBase = big.NewInt(0)
	}
	if collateralBase == nil {
		collateralBase = big.NewInt(0)
	}
	if bonusBps == 0 {
		bonusBps = 10000
	}

	maxRepaid := mulBps(debtBase, closeFactorBps)
	seized := mulBps(maxRepaid, bonusBps)
	if seized.Cmp(collateralBase) > 0 {
		// Not enough collateral to honor the full repay plus bonus. Cap the seizure
		// and back out the repay value it actually supports.
		seized = new(big.Int).Set(collateralBase)
		maxRepaid = new(big.Int).Div(new(big.Int).Mul(seized, big.NewInt(10000)), big.NewInt(int64(bonusBps)))
	}
	gross := new(big.Int).Sub(seized, maxRepaid)
	if gross.Sign() < 0 {
		gross = big.NewInt(0)
	}

	return LiquidationEstimate{
		CollateralAsset:      collateralAsset,
		DebtAsset:            debtAsset,
		CloseFactorBps:       closeFactorBps,
		LiquidationBonusBps:  bonusBps,
		MaxDebtRepaidBase:    maxRepaid,
		SeizedCollateralBase: seized,
		GrossBonusBase:       gross,
		SlippageModeled:      false,
	}
}

// mulBps returns x * bps / 10000.
func mulBps(x *big.Int, bps uint16) *big.Int {
	if x == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Div(new(big.Int).Mul(x, big.NewInt(int64(bps))), big.NewInt(10000))
}

// Ranked is a position prepared for the unified feed ordering.
type Ranked struct {
	Account        Account
	HealthFactor   *big.Int
	CollateralBase *big.Int
	DebtBase       *big.Int
	Liquidatable   bool
}

// RankByProximity sorts positions by liquidation proximity: liquidatable first,
// then ascending health factor, then descending debt size as a tie-breaker so
// the biggest opportunities surface first within an equal-health band.
func RankByProximity(positions []Ranked) {
	sort.SliceStable(positions, func(i, j int) bool {
		a, b := positions[i], positions[j]
		if a.Liquidatable != b.Liquidatable {
			return a.Liquidatable
		}
		if c := cmpBig(a.HealthFactor, b.HealthFactor); c != 0 {
			return c < 0
		}
		return cmpBig(a.DebtBase, b.DebtBase) > 0
	})
}

func cmpBig(a, b *big.Int) int {
	if a == nil {
		a = big.NewInt(0)
	}
	if b == nil {
		b = big.NewInt(0)
	}
	return a.Cmp(b)
}
