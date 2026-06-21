// Package aave implements the lending.Adapter for the Aave v3 Avalanche market.
package aave

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"icicle/pkg/lending"
)

// Canonical Avalanche addresses. The provider is the trust anchor: everything
// else is resolved from it on-chain and reconciled against the expected Pool.
const (
	DefaultProvider = "0xa97684ead0e402dC232d5A977953DF7ECBaB3CDb" // PoolAddressesProvider
	ExpectedPool    = "0x794a61358D6845594F94dc1DB02A252b5b4814aD"
)

// Event signatures on the Aave v3 Pool. Indexed parameters become topics in the
// order they are declared.
const (
	sigSupply        = "Supply(address,address,address,uint256,uint16)"
	sigWithdraw      = "Withdraw(address,address,address,uint256)"
	sigBorrow        = "Borrow(address,address,address,uint256,uint8,uint256,uint16)"
	sigRepay         = "Repay(address,address,address,uint256,bool)"
	sigLiquidation   = "LiquidationCall(address,address,address,uint256,uint256,address,bool)"
	sigCollatEnable  = "ReserveUsedAsCollateralEnabled(address,address)"
	sigCollatDisable = "ReserveUsedAsCollateralDisabled(address,address)"
)

// Function signatures.
const (
	fnGetPool             = "getPool()"
	fnGetPriceOracle      = "getPriceOracle()"
	fnGetPoolDataProvider = "getPoolDataProvider()"
	fnGetUserAccountData  = "getUserAccountData(address)"
	fnGetAssetPrice       = "getAssetPrice(address)"
	fnBaseCurrencyUnit    = "BASE_CURRENCY_UNIT()"
	fnGetAllReserves      = "getAllReservesTokens()"
	fnGetReserveConfig    = "getReserveConfigurationData(address)"
	fnGetUserReserveData  = "getUserReserveData(address,address)"
)

// Adapter implements lending.Adapter for Aave v3.
type Adapter struct {
	provider string
	addrs    lending.Addresses
	params   map[string]lending.AssetParam // keyed by lowercase asset address
	globals  lending.GlobalParams

	topicSupply        string
	topicWithdraw      string
	topicBorrow        string
	topicRepay         string
	topicLiquidation   string
	topicCollatEnable  string
	topicCollatDisable string
}

// New builds an Aave adapter. provider may be empty to use the canonical default.
func New(provider string) *Adapter {
	if provider == "" {
		provider = DefaultProvider
	}
	return &Adapter{
		provider:           lending.NormalizeAddr(provider),
		params:             map[string]lending.AssetParam{},
		topicSupply:        lending.EventTopic(sigSupply),
		topicWithdraw:      lending.EventTopic(sigWithdraw),
		topicBorrow:        lending.EventTopic(sigBorrow),
		topicRepay:         lending.EventTopic(sigRepay),
		topicLiquidation:   lending.EventTopic(sigLiquidation),
		topicCollatEnable:  lending.EventTopic(sigCollatEnable),
		topicCollatDisable: lending.EventTopic(sigCollatDisable),
	}
}

func (a *Adapter) Protocol() lending.Protocol { return lending.ProtocolAaveV3 }

// Resolve reads Pool, oracle, and data provider from the addresses provider and
// reconciles the Pool against the expected canonical value.
func (a *Adapter) Resolve(ctx context.Context, rpc *lending.Client) (lending.Addresses, []lending.VerifyNote, error) {
	var notes []lending.VerifyNote

	pool, err := a.callAddr(ctx, rpc, a.provider, fnGetPool)
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("getPool: %w", err)
	}
	oracle, err := a.callAddr(ctx, rpc, a.provider, fnGetPriceOracle)
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("getPriceOracle: %w", err)
	}
	dataProvider, err := a.callAddr(ctx, rpc, a.provider, fnGetPoolDataProvider)
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("getPoolDataProvider: %w", err)
	}

	poolOK := strings.EqualFold(pool, ExpectedPool)
	notes = append(notes, lending.VerifyNote{
		Role: lending.RolePool, Expected: lending.NormalizeAddr(ExpectedPool), Resolved: pool, OK: poolOK,
	})
	if !poolOK {
		slog.Warn("aave: resolved Pool differs from expected canonical address",
			"resolved", pool, "expected", ExpectedPool)
	}
	notes = append(notes,
		lending.VerifyNote{Role: lending.RoleOracle, Resolved: oracle, OK: oracle != lending.ZeroAddress},
		lending.VerifyNote{Role: lending.RoleDataProvider, Resolved: dataProvider, OK: dataProvider != lending.ZeroAddress},
	)

	addrs := lending.Addresses{
		Pool:         pool,
		Oracle:       oracle,
		Provider:     a.provider,
		DataProvider: dataProvider,
	}
	if oracle == lending.ZeroAddress || dataProvider == lending.ZeroAddress {
		return addrs, notes, fmt.Errorf("aave: resolved a zero address (oracle=%s dataProvider=%s)", oracle, dataProvider)
	}
	return addrs, notes, nil
}

func (a *Adapter) Configure(addrs lending.Addresses, params []lending.AssetParam, globals lending.GlobalParams) {
	a.addrs = addrs
	a.globals = globals
	a.params = make(map[string]lending.AssetParam, len(params))
	for _, p := range params {
		a.params[lending.NormalizeAddr(p.Asset)] = p
	}
}

func (a *Adapter) Discovery() lending.DiscoverySpec {
	return lending.DiscoverySpec{
		Addresses: []string{a.addrs.Pool},
		Topics: []string{
			a.topicSupply, a.topicWithdraw, a.topicBorrow, a.topicRepay,
			a.topicLiquidation, a.topicCollatEnable, a.topicCollatDisable,
		},
	}
}

// DecodeLog maps a Pool event to account exposures. The borrower or supplier is
// always the indexed onBehalfOf/user in topic2.
func (a *Adapter) DecodeLog(l lending.LogRow) []lending.Exposure {
	switch l.Topic0 {
	case a.topicSupply, a.topicCollatEnable, a.topicCollatDisable:
		return a.exposure(l, lending.SideCollateral)
	case a.topicWithdraw:
		return a.exposure(l, lending.SideCollateral)
	case a.topicBorrow, a.topicRepay:
		return a.exposure(l, lending.SideBorrow)
	case a.topicLiquidation:
		// collateralAsset=topic1, debtAsset=topic2, user=topic3. Record both legs
		// so the liquidated account is rechecked.
		user := lending.AddrFromTopic(l.Topic3)
		return []lending.Exposure{
			{Account: user, Asset: lending.AddrFromTopic(l.Topic1), Side: lending.SideCollateral, Block: l.Block},
			{Account: user, Asset: lending.AddrFromTopic(l.Topic2), Side: lending.SideBorrow, Block: l.Block},
		}
	}
	return nil
}

// exposure extracts (reserve=topic1, account=topic2) for the standard Pool events.
func (a *Adapter) exposure(l lending.LogRow, side lending.Side) []lending.Exposure {
	account := lending.AddrFromTopic(l.Topic2)
	asset := lending.AddrFromTopic(l.Topic1)
	if account == lending.ZeroAddress {
		return nil
	}
	return []lending.Exposure{{Account: account, Asset: asset, Side: side, Block: l.Block}}
}

// BuildProbe reads getUserAccountData for the health summary, plus per-asset
// reserve data and prices when exposure is provided.
func (a *Adapter) BuildProbe(account string, exposure []lending.Exposure) lending.HealthProbe {
	account = lending.NormalizeAddr(account)
	calls := []lending.Call{
		{Target: a.addrs.Pool, AllowFailure: true, Data: lending.EncodeCall1Addr(fnGetUserAccountData, account)},
	}

	// Distinct exposed assets, so we read each reserve once.
	assets := distinctAssets(exposure)
	for _, asset := range assets {
		calls = append(calls,
			lending.Call{Target: a.addrs.DataProvider, AllowFailure: true, Data: lending.EncodeCall2Addr(fnGetUserReserveData, asset, account)},
			lending.Call{Target: a.addrs.Oracle, AllowFailure: true, Data: lending.EncodeCall1Addr(fnGetAssetPrice, asset)},
		)
	}

	params := a.params
	// Aave reports base-currency amounts in BASE_CURRENCY_UNIT (1e8 for USD on
	// Avalanche). Normalize to 1e18 so collateral and debt values are comparable
	// with Benqi's 1e18-scaled values in the unified feed and ranking. The health
	// factor is already a 1e18 dimensionless ratio and is left untouched.
	baseScale := a.baseScale()
	return lending.HealthProbe{
		Account: account,
		Calls:   calls,
		Decode: func(results []lending.CallResult, block uint64) lending.Health {
			h := lending.Health{
				Account:      lending.Account{Protocol: lending.ProtocolAaveV3, Address: account},
				BlockNumber:  block,
				HealthFactor: big.NewInt(0),
			}
			if len(results) == 0 || !results[0].Success {
				return h // summary read reverted, skip this account (rule 5)
			}
			sum := results[0].ReturnData
			h.CollateralBase = new(big.Int).Mul(lending.Word(sum, 0), baseScale)
			h.DebtBase = new(big.Int).Mul(lending.Word(sum, 1), baseScale)
			h.HealthFactor = lending.Word(sum, 5)
			h.OK = true
			h.Liquidatable = h.DebtBase.Sign() > 0 && h.HealthFactor.Cmp(lending.WAD) < 0

			// Per-asset detail, two results per asset: reserve data then price.
			idx := 1
			for _, asset := range assets {
				if idx+1 >= len(results) {
					break
				}
				rd := results[idx]
				pr := results[idx+1]
				idx += 2
				if !rd.Success || !pr.Success {
					continue
				}
				p := params[asset]
				price := lending.Word(pr.ReturnData, 0)
				aTokenBal := lending.Word(rd.ReturnData, 0)
				debt := new(big.Int).Add(lending.Word(rd.ReturnData, 1), lending.Word(rd.ReturnData, 2))
				if aTokenBal.Sign() > 0 {
					h.Assets = append(h.Assets, lending.AssetPosition{
						Asset: asset, Side: lending.SideCollateral,
						Amount: aTokenBal, BaseValue: new(big.Int).Mul(baseValue(aTokenBal, price, p.Decimals), baseScale),
					})
				}
				if debt.Sign() > 0 {
					h.Assets = append(h.Assets, lending.AssetPosition{
						Asset: asset, Side: lending.SideDebt,
						Amount: debt, BaseValue: new(big.Int).Mul(baseValue(debt, price, p.Decimals), baseScale),
					})
				}
			}
			return h
		},
	}
}

// baseScale returns the multiplier that converts Aave base-currency amounts
// (BASE_CURRENCY_UNIT, typically 1e8) to the canonical 1e18 scale. Returns 1 when
// the unit is unknown or already at or above 1e18.
func (a *Adapter) baseScale() *big.Int {
	unit := a.globals.BaseCurrencyUnit
	if unit == nil || unit.Sign() == 0 {
		return big.NewInt(1)
	}
	scale := new(big.Int).Div(lending.WAD, unit)
	if scale.Sign() == 0 {
		return big.NewInt(1)
	}
	return scale
}

// PriceSources exposes the oracle and the reserve assets the price watcher should
// follow for Aave.
func (a *Adapter) PriceSources() (oracle string, assets []string, markets []string) {
	for asset := range a.params {
		assets = append(assets, asset)
	}
	return a.addrs.Oracle, assets, nil
}

// RefreshParams reads the reserve list and each reserve's risk configuration, plus
// the oracle base-currency unit.
func (a *Adapter) RefreshParams(ctx context.Context, rpc *lending.Client) ([]lending.AssetParam, lending.GlobalParams, error) {
	globals := lending.GlobalParams{
		BaseCurrencyUnit: big.NewInt(1e8),
	}
	if unit, err := a.callUint(ctx, rpc, a.addrs.Oracle, fnBaseCurrencyUnit); err == nil && unit.Sign() > 0 {
		globals.BaseCurrencyUnit = unit
	}

	res, err := rpc.EthCall(ctx, a.addrs.DataProvider, lending.EncodeCall0(fnGetAllReserves), "latest")
	if err != nil {
		return nil, globals, fmt.Errorf("getAllReservesTokens: %w", err)
	}
	tokens := decodeReserveTokens(lending.DecodeHexBytes(res))

	var params []lending.AssetParam
	for _, t := range tokens {
		cfg, err := rpc.EthCall(ctx, a.addrs.DataProvider, lending.EncodeCall1Addr(fnGetReserveConfig, t.address), "latest")
		if err != nil {
			slog.Warn("aave: getReserveConfigurationData failed", "asset", t.address, "error", err)
			continue
		}
		b := lending.DecodeHexBytes(cfg)
		params = append(params, lending.AssetParam{
			Asset:                   t.address,
			Symbol:                  t.symbol,
			Decimals:                uint8(lending.WordU64(b, 0)),
			LtvBps:                  uint16(lending.WordU64(b, 1)),
			LiquidationThresholdBps: uint16(lending.WordU64(b, 2)),
			LiquidationBonusBps:     uint16(lending.WordU64(b, 3)),
			CanCollateral:           lending.WordBool(b, 5),
			CanBorrow:               lending.WordBool(b, 6),
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

func distinctAssets(exposure []lending.Exposure) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range exposure {
		asset := lending.NormalizeAddr(e.Asset)
		if asset == lending.ZeroAddress || seen[asset] {
			continue
		}
		seen[asset] = true
		out = append(out, asset)
	}
	return out
}

// baseValue returns amount * price / 10^decimals in the oracle base currency.
func baseValue(amount, price *big.Int, decimals uint8) *big.Int {
	if amount == nil || price == nil {
		return big.NewInt(0)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	v := new(big.Int).Mul(amount, price)
	return v.Div(v, scale)
}

type reserveToken struct {
	symbol  string
	address string
}

// decodeReserveTokens decodes TokenData[] { string symbol, address tokenAddress }.
func decodeReserveTokens(b []byte) []reserveToken {
	if len(b) < 64 {
		return nil
	}
	arrBase := int(lending.Word(b, 0).Uint64())
	if arrBase+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[arrBase : arrBase+32]).Uint64())
	headBase := arrBase + 32
	var out []reserveToken
	for i := 0; i < n; i++ {
		hp := headBase + i*32
		if hp+32 > len(b) {
			break
		}
		elemOff := int(new(big.Int).SetBytes(b[hp : hp+32]).Uint64())
		elemStart := headBase + elemOff
		if elemStart+64 > len(b) {
			break
		}
		// tuple: word0 = offset to string symbol (relative to tuple start), word1 = address
		strOff := int(new(big.Int).SetBytes(b[elemStart : elemStart+32]).Uint64())
		addr := lending.Addr(b[elemStart+32:elemStart+64], 0)
		symStart := elemStart + strOff
		symbol := ""
		if symStart+32 <= len(b) {
			slen := int(new(big.Int).SetBytes(b[symStart : symStart+32]).Uint64())
			if symStart+32+slen <= len(b) {
				symbol = string(b[symStart+32 : symStart+32+slen])
			}
		}
		out = append(out, reserveToken{symbol: symbol, address: lending.NormalizeAddr(addr)})
	}
	return out
}
