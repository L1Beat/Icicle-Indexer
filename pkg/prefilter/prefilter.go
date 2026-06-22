// Package prefilter is the cheap, off-chain Stage 1 profitability gate for
// liquidation candidates coming out of the lending feed. It does NOT submit
// anything and does NOT simulate the full bundle. Its only job is to turn a
// raw "liquidatable" count into a real "profitable after costs" count (K), so
// you can see how many of the standing positions are actually worth acting on
// before any bot code exists.
//
// Pipeline position:
//   feed (liquidatable=true)  ->  [solvency gate] -> [this pre-filter] -> shortlist -> full sim (Stage 2)
//
// Cost model, in plain terms, all denominated in the debt asset then converted
// to USD for a single comparable threshold:
//
//   seizeValueUSD = repayValueUSD * (1 + bonus)      // capped by available collateral (solvency)
//   swapProceeds  = quoter(collateral -> debt, seizeAmount)   // EXECUTABLE, not oracle value
//   flashRepay    = repayValueUSD * (1 + flashFee)
//   netProfitUSD  = swapProceedsUSD - flashRepayUSD - gasUSD
//
// The only network call here is the quoter (one read per evaluated collateral
// leg). Everything else is arithmetic over data the feed already carries.
//
// Units convention (matches the feed after the 1e18 base normalization fix):
//   - BaseValue: USD, 1e18-scaled.
//   - Amount:    native token units (10^Decimals).
//   - bonus / closeFactor / flashFee: basis points (10000 = 100%).
//
// No em dashes anywhere, per house style.

package prefilter

import (
	"context"
	"math/big"

	"github.com/ava-labs/libevm/common"
)

// ---- 1e18 fixed-point helpers (USD base is 1e18-scaled) ----

var (
	one1e18 = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	bpsDen  = big.NewInt(10000)
)

func mulDiv(a, b, den *big.Int) *big.Int {
	r := new(big.Int).Mul(a, b)
	return r.Div(r, den)
}

// applyBps returns x * (bps/10000).
func applyBps(x *big.Int, bps uint64) *big.Int {
	return mulDiv(x, new(big.Int).SetUint64(bps), bpsDen)
}

// addBps returns x * (1 + bps/10000).
func addBps(x *big.Int, bps uint64) *big.Int {
	return new(big.Int).Add(x, applyBps(x, bps))
}

// ---- Input shapes (bind these to your feed; field names are the seam) ----

type Side int

const (
	SideCollateral Side = iota
	SideDebt
)

// AssetLeg is one row of lending_position_assets for an account.
type AssetLeg struct {
	Asset     common.Address
	Symbol    string
	Side      Side
	Amount    *big.Int // native units, 10^Decimals
	Decimals  uint8
	BaseValue *big.Int // USD, 1e18-scaled, for the whole Amount
}

// Position is one account from the feed with its per-asset legs.
type Position struct {
	Protocol     string // "aave-v3" or "benqi"
	Account      common.Address
	HealthFactor *big.Int // 1e18-scaled; used to decide Aave close factor
	Legs         []AssetLeg
}

func (p Position) debtLegs() []AssetLeg {
	out := make([]AssetLeg, 0, len(p.Legs))
	for _, l := range p.Legs {
		if l.Side == SideDebt && l.Amount.Sign() > 0 && l.BaseValue.Sign() > 0 {
			out = append(out, l)
		}
	}
	return out
}

func (p Position) collateralLegs() []AssetLeg {
	out := make([]AssetLeg, 0, len(p.Legs))
	for _, l := range p.Legs {
		if l.Side == SideCollateral && l.Amount.Sign() > 0 && l.BaseValue.Sign() > 0 {
			out = append(out, l)
		}
	}
	return out
}

// ---- Pluggable lookups (wire to lending_protocol_params + globals) ----

// ParamsProvider returns protocol/asset risk params. Bonus is the liquidation
// bonus in bps for the COLLATERAL asset (Aave per-reserve; Benqi the global
// incentive expressed as a bonus, e.g. incentive 1.08 -> 800 bps). CloseFactor
// is in bps and may depend on protocol and health factor.
type ParamsProvider interface {
	BonusBps(protocol string, collateral common.Address) (uint64, bool)
	CloseFactorBps(protocol string, hf *big.Int) uint64
}

// Quoter returns the EXECUTABLE output amount (native units of tokenOut) for
// selling amountIn of tokenIn right now, sized to the real seize amount.
// Back this with the deepest-liquidity venue (LFJ LB router, Pangolin, a
// Uniswap deployment, or an aggregator). This is the term that separates dust
// and illiquid collateral from real opportunities, so the quote MUST be for the
// actual size, not a unit price.
type Quoter interface {
	QuoteOut(
		ctx context.Context,
		tokenIn common.Address, tokenInDec uint8,
		tokenOut common.Address, tokenOutDec uint8,
		amountIn *big.Int,
	) (amountOut *big.Int, err error)
}

// CostModel carries the non-position inputs. Set these from live values before a run.
type CostModel struct {
	FlashFeeBps      uint64   // Aave flashLoanSimple premium, ~5 (0.05%)
	GasUnits         uint64   // estimated full bundle, e.g. 700000
	GasPriceWei      *big.Int // current effective gas price
	NativeUSD1e18    *big.Int // AVAX price, USD 1e18-scaled
	MinProfitUSD1e18 *big.Int // threshold; keep ABOVE zero to cover variance
}

func (c CostModel) gasUSD1e18() *big.Int {
	// (GasUnits * GasPriceWei) is native wei; / 1e18 -> native; * NativeUSD1e18 -> USD 1e18.
	wei := new(big.Int).Mul(new(big.Int).SetUint64(c.GasUnits), c.GasPriceWei)
	return mulDiv(wei, c.NativeUSD1e18, one1e18)
}

// ---- Result ----

type Reason string

const (
	ReasonProfitable Reason = "profitable"
	ReasonDust       Reason = "dust"     // gross positive but gas eats it
	ReasonIlliquid   Reason = "illiquid" // swap output well below seize value
	ReasonBadDebt    Reason = "bad_debt" // collateral cannot cover repay+bonus
	ReasonNoPair     Reason = "no_pair"  // missing usable debt or collateral leg
	ReasonUnprofit   Reason = "unprofitable"
)

type Result struct {
	Account         common.Address
	Protocol        string
	Profitable      bool
	Reason          Reason
	NetProfitUSD    *big.Int // 1e18-scaled, best pair
	RepayUSD        *big.Int // 1e18-scaled, at the chosen size
	DebtAsset       common.Address
	CollateralAsset common.Address
}

// ---- Core evaluation: best (debt, collateral) pair for one position ----

func EvaluatePosition(
	ctx context.Context,
	p Position,
	params ParamsProvider,
	q Quoter,
	cost CostModel,
) (Result, error) {

	debts := p.debtLegs()
	colls := p.collateralLegs()
	if len(debts) == 0 || len(colls) == 0 {
		return Result{Account: p.Account, Protocol: p.Protocol, Reason: ReasonNoPair, NetProfitUSD: big.NewInt(0)}, nil
	}

	cfBps := params.CloseFactorBps(p.Protocol, p.HealthFactor)
	gasUSD := cost.gasUSD1e18()

	best := Result{
		Account: p.Account, Protocol: p.Protocol,
		Reason: ReasonUnprofit, NetProfitUSD: new(big.Int).Neg(one1e18),
	}
	sawBadDebt, sawIlliquid, sawDust := false, false, false

	for _, d := range debts {
		// Max repay value allowed by the close factor, in USD.
		maxRepayUSD := applyBps(d.BaseValue, cfBps)
		if maxRepayUSD.Sign() == 0 {
			continue
		}

		for _, c := range colls {
			bonusBps, ok := params.BonusBps(p.Protocol, c.Asset)
			if !ok {
				continue
			}

			// Solvency / collateral cap: seizeValue = repay*(1+bonus) must fit
			// inside available collateral value. Solve for the repay this allows,
			// then take the smaller of that and the close-factor cap.
			collateralCapRepayUSD := mulDiv(c.BaseValue, bpsDen, new(big.Int).Add(bpsDen, new(big.Int).SetUint64(bonusBps)))
			repayUSD := maxRepayUSD
			if collateralCapRepayUSD.Cmp(repayUSD) < 0 {
				repayUSD = collateralCapRepayUSD
			}
			if repayUSD.Sign() == 0 {
				sawBadDebt = true
				continue
			}

			seizeValueUSD := addBps(repayUSD, bonusBps)

			// Convert USD slices to native token amounts proportionally from the leg.
			// amount = legAmount * (sliceValue / legValue). No price lookup needed.
			seizeAmountC := mulDiv(c.Amount, seizeValueUSD, c.BaseValue)
			debtToCoverD := mulDiv(d.Amount, repayUSD, d.BaseValue)
			if seizeAmountC.Sign() == 0 || debtToCoverD.Sign() == 0 {
				sawBadDebt = true
				continue
			}

			// The one network call: real executable swap output, sized to seizeAmountC.
			outD, err := q.QuoteOut(ctx, c.Asset, c.Decimals, d.Asset, d.Decimals, seizeAmountC)
			if err != nil {
				return Result{}, err
			}

			// Value the swap proceeds in USD using the debt leg's own price (BaseValue/Amount).
			swapProceedsUSD := mulDiv(d.BaseValue, outD, d.Amount)

			flashRepayUSD := addBps(repayUSD, cost.FlashFeeBps)

			net := new(big.Int).Sub(swapProceedsUSD, flashRepayUSD)
			net.Sub(net, gasUSD)

			// Classify this attempt for the rejection breakdown.
			switch {
			case swapProceedsUSD.Cmp(flashRepayUSD) < 0:
				// Even before gas, the swap does not return the borrowed amount:
				// illiquid collateral or effectively bad debt at executable prices.
				sawIlliquid = true
			case net.Sign() <= 0:
				sawDust = true
			}

			if net.Cmp(best.NetProfitUSD) > 0 {
				best.NetProfitUSD = net
				best.RepayUSD = repayUSD
				best.DebtAsset = d.Asset
				best.CollateralAsset = c.Asset
			}
		}
	}

	if best.NetProfitUSD.Cmp(cost.MinProfitUSD1e18) > 0 {
		best.Profitable = true
		best.Reason = ReasonProfitable
		return best, nil
	}

	// Not profitable: pick the most informative reason for the K breakdown.
	switch {
	case best.RepayUSD == nil && sawBadDebt:
		best.Reason = ReasonBadDebt
	case sawIlliquid:
		best.Reason = ReasonIlliquid
	case sawDust:
		best.Reason = ReasonDust
	default:
		best.Reason = ReasonUnprofit
	}
	if best.NetProfitUSD.Sign() < 0 && best.RepayUSD == nil {
		best.NetProfitUSD = big.NewInt(0)
	}
	return best, nil
}

// ---- Batch: compute K and the rejection breakdown over the feed ----

type Summary struct {
	Total      int
	Profitable int // this is K
	ByReason   map[Reason]int
	Results    []Result
}

// ComputeK runs the pre-filter across a batch of liquidatable positions. Run it
// once over the current standing set to see how many of the raw count survive.
func ComputeK(
	ctx context.Context,
	positions []Position,
	params ParamsProvider,
	q Quoter,
	cost CostModel,
) (Summary, error) {
	s := Summary{ByReason: map[Reason]int{}, Results: make([]Result, 0, len(positions))}
	for _, p := range positions {
		r, err := EvaluatePosition(ctx, p, params, q, cost)
		if err != nil {
			return s, err
		}
		s.Total++
		s.ByReason[r.Reason]++
		if r.Profitable {
			s.Profitable++
		}
		s.Results = append(s.Results, r)
	}
	return s, nil
}
