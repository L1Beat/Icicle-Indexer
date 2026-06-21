// Package benqi implements the lending.Adapter for Benqi Core Markets, a
// Compound v2 fork. Unlike Aave, Compound events do not index their address
// parameters, so the account is decoded from the log data, and the liquidatable
// signal is authoritative from the Comptroller shortfall (rule 3).
package benqi

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"

	"icicle/pkg/lending"
)

// DefaultComptroller is the Benqi Unitroller on Avalanche. It is the trust
// anchor: the market list and oracle are resolved from it on-chain. Confirm this
// address against Benqi's official documentation before production use.
const DefaultComptroller = "0x486Af39519B4Dc9a7fCcd318217352830E8AD9b4"

// mantissaPerBps converts a 1e18 mantissa to basis points (1e18 -> 10000).
var mantissaPerBps = new(big.Int).Exp(big.NewInt(10), big.NewInt(14), nil)

// Compound v2 event signatures. No parameters are indexed, so addresses live in
// the data payload.
const (
	sigMint          = "Mint(address,uint256,uint256)"
	sigRedeem        = "Redeem(address,uint256,uint256)"
	sigBorrow        = "Borrow(address,uint256,uint256,uint256)"
	sigRepayBorrow   = "RepayBorrow(address,address,uint256,uint256,uint256)"
	sigLiquidate     = "LiquidateBorrow(address,address,uint256,address,uint256)"
	sigMarketEntered = "MarketEntered(address,address)"
	sigMarketExited  = "MarketExited(address,address)"
)

// Function signatures.
const (
	fnGetAllMarkets        = "getAllMarkets()"
	fnOracle               = "oracle()"
	fnCloseFactor          = "closeFactorMantissa()"
	fnLiquidationIncentive = "liquidationIncentiveMantissa()"
	fnMarkets              = "markets(address)"
	fnGetAccountLiquidity  = "getAccountLiquidity(address)"
	fnGetAccountSnapshot   = "getAccountSnapshot(address)"
	fnGetUnderlyingPrice   = "getUnderlyingPrice(address)"
	fnUnderlying           = "underlying()"
	fnSymbol               = "symbol()"
	fnComptroller          = "comptroller()"
)

// Adapter implements lending.Adapter for Benqi.
type Adapter struct {
	comptroller string
	addrs       lending.Addresses
	params      map[string]lending.AssetParam // keyed by lowercase market (qiToken) address
	globals     lending.GlobalParams

	topicMint          string
	topicRedeem        string
	topicBorrow        string
	topicRepay         string
	topicLiquidate     string
	topicMarketEntered string
	topicMarketExited  string
}

// New builds a Benqi adapter. comptroller may be empty to use the default anchor.
func New(comptroller string) *Adapter {
	if comptroller == "" {
		comptroller = DefaultComptroller
	}
	return &Adapter{
		comptroller:        lending.NormalizeAddr(comptroller),
		params:             map[string]lending.AssetParam{},
		topicMint:          lending.EventTopic(sigMint),
		topicRedeem:        lending.EventTopic(sigRedeem),
		topicBorrow:        lending.EventTopic(sigBorrow),
		topicRepay:         lending.EventTopic(sigRepayBorrow),
		topicLiquidate:     lending.EventTopic(sigLiquidate),
		topicMarketEntered: lending.EventTopic(sigMarketEntered),
		topicMarketExited:  lending.EventTopic(sigMarketExited),
	}
}

func (a *Adapter) Protocol() lending.Protocol { return lending.ProtocolBenqi }

// Resolve reads the market list and oracle from the Comptroller and verifies the
// first market reports back the same Comptroller.
func (a *Adapter) Resolve(ctx context.Context, rpc *lending.Client) (lending.Addresses, []lending.VerifyNote, error) {
	var notes []lending.VerifyNote

	res, err := rpc.EthCall(ctx, a.comptroller, lending.EncodeCall0(fnGetAllMarkets), "latest")
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("getAllMarkets: %w", err)
	}
	markets := decodeAddressArray(lending.DecodeHexBytes(res))
	if len(markets) == 0 {
		return lending.Addresses{}, notes, fmt.Errorf("benqi: comptroller %s returned no markets, confirm the address", a.comptroller)
	}

	oracle, err := a.callAddr(ctx, rpc, a.comptroller, fnOracle)
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("oracle: %w", err)
	}

	// Verify the first market points back at this Comptroller.
	back, err := a.callAddr(ctx, rpc, markets[0], fnComptroller)
	marketOK := err == nil && lending.NormalizeAddr(back) == a.comptroller
	if !marketOK {
		slog.Warn("benqi: first market does not report the expected comptroller",
			"market", markets[0], "reported", back, "expected", a.comptroller)
	}
	notes = append(notes,
		lending.VerifyNote{Role: lending.RoleComptroller, Resolved: a.comptroller, OK: true},
		lending.VerifyNote{Role: lending.RoleOracle, Resolved: oracle, OK: oracle != lending.ZeroAddress},
		lending.VerifyNote{Role: lending.RoleMarket, Resolved: markets[0], OK: marketOK, Detail: fmt.Sprintf("%d markets", len(markets))},
	)

	return lending.Addresses{
		Comptroller: a.comptroller,
		Oracle:      oracle,
		Markets:     markets,
	}, notes, nil
}

func (a *Adapter) Configure(addrs lending.Addresses, params []lending.AssetParam, globals lending.GlobalParams) {
	a.addrs = addrs
	a.globals = globals
	a.params = make(map[string]lending.AssetParam, len(params))
	for _, p := range params {
		a.params[lending.NormalizeAddr(p.Market)] = p
	}
}

func (a *Adapter) Discovery() lending.DiscoverySpec {
	addrs := append([]string{a.comptroller}, a.addrs.Markets...)
	return lending.DiscoverySpec{
		Addresses: addrs,
		Topics: []string{
			a.topicMint, a.topicRedeem, a.topicBorrow, a.topicRepay,
			a.topicLiquidate, a.topicMarketEntered, a.topicMarketExited,
		},
	}
}

// DecodeLog reads the account from the unindexed event data. The exposure asset
// is the qiToken market, which is what per-market reads key on.
func (a *Adapter) DecodeLog(l lending.LogRow) []lending.Exposure {
	switch l.Topic0 {
	case a.topicMint, a.topicRedeem:
		return one(lending.Addr(l.Data, 0), l.Address, lending.SideCollateral, l.Block)
	case a.topicBorrow:
		return one(lending.Addr(l.Data, 0), l.Address, lending.SideBorrow, l.Block)
	case a.topicRepay:
		// payer=word0, borrower=word1.
		return one(lending.Addr(l.Data, 1), l.Address, lending.SideBorrow, l.Block)
	case a.topicLiquidate:
		// liquidator=word0, borrower=word1, repayAmount=word2, qiTokenCollateral=word3.
		borrower := lending.Addr(l.Data, 1)
		collateralMkt := lending.Addr(l.Data, 3)
		return []lending.Exposure{
			{Account: borrower, Asset: l.Address, Side: lending.SideBorrow, Block: l.Block},
			{Account: borrower, Asset: collateralMkt, Side: lending.SideCollateral, Block: l.Block},
		}
	case a.topicMarketEntered, a.topicMarketExited:
		// Emitted by the Comptroller: cToken=word0, account=word1.
		return one(lending.Addr(l.Data, 1), lending.Addr(l.Data, 0), lending.SideCollateral, l.Block)
	}
	return nil
}

// BuildProbe reads getAccountLiquidity for the authoritative shortfall, plus
// per-market snapshots and prices when exposure is provided.
func (a *Adapter) BuildProbe(account string, exposure []lending.Exposure) lending.HealthProbe {
	account = lending.NormalizeAddr(account)
	calls := []lending.Call{
		{Target: a.comptroller, AllowFailure: true, Data: lending.EncodeCall1Addr(fnGetAccountLiquidity, account)},
	}

	markets := distinctMarkets(exposure)
	for _, m := range markets {
		calls = append(calls,
			lending.Call{Target: m, AllowFailure: true, Data: lending.EncodeCall1Addr(fnGetAccountSnapshot, account)},
			lending.Call{Target: a.addrs.Oracle, AllowFailure: true, Data: lending.EncodeCall1Addr(fnGetUnderlyingPrice, m)},
		)
	}

	params := a.params
	return lending.HealthProbe{
		Account: account,
		Calls:   calls,
		Decode: func(results []lending.CallResult, block uint64) lending.Health {
			h := lending.Health{
				Account:       lending.Account{Protocol: lending.ProtocolBenqi, Address: account},
				BlockNumber:   block,
				HealthFactor:  big.NewInt(0),
				ShortfallBase: big.NewInt(0),
			}
			if len(results) == 0 || !results[0].Success {
				return h // liquidity read reverted, skip (rule 5)
			}
			// getAccountLiquidity returns (error, liquidity, shortfall), 1e18 USD.
			shortfall := lending.Word(results[0].ReturnData, 2)
			h.ShortfallBase = shortfall
			h.Liquidatable = shortfall.Sign() > 0 // authoritative (rule 3)
			h.OK = true

			weightedCollateral := big.NewInt(0)
			collateral := big.NewInt(0)
			debt := big.NewInt(0)

			idx := 1
			for _, m := range markets {
				if idx+1 >= len(results) {
					break
				}
				snap := results[idx]
				priceRes := results[idx+1]
				idx += 2
				if !snap.Success || !priceRes.Success {
					continue
				}
				cTokenBalance := lending.Word(snap.ReturnData, 1)
				borrowBalance := lending.Word(snap.ReturnData, 2)
				exchangeRate := lending.Word(snap.ReturnData, 3)
				price := lending.Word(priceRes.ReturnData, 0)

				// underlyingRaw = cTokenBalance * exchangeRate / 1e18
				underlyingRaw := div1e18(new(big.Int).Mul(cTokenBalance, exchangeRate))
				// USD (1e18) = price * amountRaw / 1e18, decimals cancel via Compound price scaling.
				collUSD := div1e18(new(big.Int).Mul(price, underlyingRaw))
				borrowUSD := div1e18(new(big.Int).Mul(price, borrowBalance))

				cfBps := params[lending.NormalizeAddr(m)].LiquidationThresholdBps
				weighted := new(big.Int).Div(new(big.Int).Mul(collUSD, big.NewInt(int64(cfBps))), big.NewInt(10000))

				collateral.Add(collateral, collUSD)
				weightedCollateral.Add(weightedCollateral, weighted)
				debt.Add(debt, borrowUSD)

				if underlyingRaw.Sign() > 0 {
					h.Assets = append(h.Assets, lending.AssetPosition{
						Asset: lending.NormalizeAddr(m), Side: lending.SideCollateral,
						Amount: underlyingRaw, BaseValue: collUSD,
					})
				}
				if borrowBalance.Sign() > 0 {
					h.Assets = append(h.Assets, lending.AssetPosition{
						Asset: lending.NormalizeAddr(m), Side: lending.SideDebt,
						Amount: borrowBalance, BaseValue: borrowUSD,
					})
				}
			}

			h.CollateralBase = collateral
			h.DebtBase = debt
			h.HealthFactor = lending.DeriveHealthFactor(weightedCollateral, debt)
			return h
		},
	}
}

// PriceSources exposes the oracle and the markets the price watcher should poll
// for Benqi.
func (a *Adapter) PriceSources() (oracle string, assets []string, markets []string) {
	return a.addrs.Oracle, nil, append([]string(nil), a.addrs.Markets...)
}

// RefreshParams reads the global close factor and incentive, then each market's
// collateral factor and underlying metadata.
func (a *Adapter) RefreshParams(ctx context.Context, rpc *lending.Client) ([]lending.AssetParam, lending.GlobalParams, error) {
	globals := lending.GlobalParams{
		BaseCurrencyUnit: lending.WAD, // Benqi prices and liquidity are 1e18 USD
	}
	if cf, err := a.callUint(ctx, rpc, a.comptroller, fnCloseFactor); err == nil {
		globals.CloseFactorBps = toBps(cf)
	}
	if inc, err := a.callUint(ctx, rpc, a.comptroller, fnLiquidationIncentive); err == nil {
		globals.LiquidationIncentiveBps = toBps(inc)
	}

	var params []lending.AssetParam
	for _, m := range a.addrs.Markets {
		cf, err := rpc.EthCall(ctx, a.comptroller, lending.EncodeCall1Addr(fnMarkets, m), "latest")
		if err != nil {
			slog.Warn("benqi: markets() failed", "market", m, "error", err)
			continue
		}
		cfBps := toBps(lending.Word(lending.DecodeHexBytes(cf), 1))

		// underlying() reverts on the native market (qiAVAX). Fall back to the
		// market address so the leg is still tracked.
		asset := m
		if u, err := a.callAddr(ctx, rpc, m, fnUnderlying); err == nil && u != lending.ZeroAddress {
			asset = u
		}
		symbol := ""
		if s, err := rpc.EthCall(ctx, m, lending.EncodeCall0(fnSymbol), "latest"); err == nil {
			symbol = decodeString(lending.DecodeHexBytes(s))
		}

		params = append(params, lending.AssetParam{
			Asset:                   lending.NormalizeAddr(asset),
			Market:                  lending.NormalizeAddr(m),
			Symbol:                  symbol,
			LiquidationThresholdBps: cfBps, // Compound uses the collateral factor for the liquidation limit
			LtvBps:                  cfBps,
			LiquidationBonusBps:     0, // global on Benqi (rule 6)
			CanCollateral:           cfBps > 0,
			CanBorrow:               true,
		})
	}
	return params, globals, nil
}

// --- helpers ---

func (a *Adapter) callAddr(ctx context.Context, rpc *lending.Client, to, sig string) (string, error) {
	res, err := rpc.EthCall(ctx, to, lending.EncodeCall0(sig), "latest")
	if err != nil {
		return "", err
	}
	return lending.Addr(lending.DecodeHexBytes(res), 0), nil
}

func (a *Adapter) callUint(ctx context.Context, rpc *lending.Client, to, sig string) (*big.Int, error) {
	res, err := rpc.EthCall(ctx, to, lending.EncodeCall0(sig), "latest")
	if err != nil {
		return nil, err
	}
	return lending.Word(lending.DecodeHexBytes(res), 0), nil
}

func one(account, asset string, side lending.Side, block uint32) []lending.Exposure {
	account = lending.NormalizeAddr(account)
	if account == lending.ZeroAddress {
		return nil
	}
	return []lending.Exposure{{Account: account, Asset: lending.NormalizeAddr(asset), Side: side, Block: block}}
}

func distinctMarkets(exposure []lending.Exposure) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range exposure {
		m := lending.NormalizeAddr(e.Asset)
		if m == lending.ZeroAddress || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func div1e18(x *big.Int) *big.Int { return new(big.Int).Div(x, lending.WAD) }

func toBps(mantissa *big.Int) uint16 {
	if mantissa == nil {
		return 0
	}
	return uint16(new(big.Int).Div(mantissa, mantissaPerBps).Uint64())
}

// decodeAddressArray decodes an ABI address[] return value.
func decodeAddressArray(b []byte) []string {
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
		out = append(out, lending.NormalizeAddr(lending.Addr(b[off:off+32], 0)))
	}
	return out
}

// decodeString decodes an ABI string return value.
func decodeString(b []byte) string {
	if len(b) < 64 {
		return ""
	}
	off := int(lending.Word(b, 0).Uint64())
	if off+32 > len(b) {
		return ""
	}
	slen := int(new(big.Int).SetBytes(b[off : off+32]).Uint64())
	if off+32+slen > len(b) {
		return ""
	}
	return string(b[off+32 : off+32+slen])
}
