// Package aavev4 implements the lending.Adapter for one Aave V4 spoke on Avalanche.
//
// Aave V4 keeps one independent position per (spoke, user): each spoke lists its
// own reserves (spoke-local uint256 ids over the hub's assets), prices them through
// its own oracle, and computes health on its own. So the engine runs one adapter
// per spoke, each under the protocol value lending.AaveV4Protocol(spoke).
//
// Interface: github.com/aave/aave-v4 src/spoke/interfaces/ISpoke.sol (main @ a3ef63e).
package aavev4

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sync"

	"icicle/pkg/lending"
)

// Spoke events. In every position event the user is an indexed topic; the
// reserve is the spoke-local reserve id in topic1.
const (
	sigSupply        = "Supply(uint256,address,address,uint256,uint256)"
	sigWithdraw      = "Withdraw(uint256,address,address,uint256,uint256)"
	sigBorrow        = "Borrow(uint256,address,address,uint256,uint256)"
	sigRepay         = "Repay(uint256,address,address,uint256,uint256,(int256,int256,uint256))"
	sigLiquidation   = "LiquidationCall(uint256,uint256,address,address,bool,uint256,uint256,(int256,int256,uint256),uint256,uint256,uint256)"
	sigSetCollateral = "SetUsingAsCollateral(uint256,address,address,bool)"
	sigReportDeficit = "ReportDeficit(uint256,address,uint256,(int256,int256,uint256))"
)

// Spoke and oracle view functions.
const (
	fnOracle               = "ORACLE()"
	fnReserveCount         = "getReserveCount()"
	fnGetReserve           = "getReserve(uint256)"
	fnGetReserveConfig     = "getReserveConfig(uint256)"
	fnGetDynamicConfig     = "getDynamicReserveConfig(uint256,uint32)"
	fnGetUserAccountData   = "getUserAccountData(address)"
	fnGetUserSupplied      = "getUserSuppliedAssets(uint256,address)"
	fnGetUserTotalDebt     = "getUserTotalDebt(uint256,address)"
	fnGetUserReserveStatus = "getUserReserveStatus(uint256,address)"
	fnGetUserPosition      = "getUserPosition(uint256,address)"
	fnGetReservePrice      = "getReservePrice(uint256)"
	fnSymbol               = "symbol()"
)

// callsPerReserve is the number of probe calls read for each exposed reserve:
// supplied assets, total debt, reserve status, user position, price.
const callsPerReserve = 5

var (
	// Spoke values are USD with 26 decimals (8-decimal oracle price times WAD), so
	// collateral divides by 1e8 to land on the feed's 1e18 USD scale. The debt
	// total is additionally RAY-scaled (totalDebtValueRay), so it divides by 1e35.
	collateralDiv = new(big.Int).Exp(big.NewInt(10), big.NewInt(8), nil)
	debtRayDiv    = new(big.Int).Exp(big.NewInt(10), big.NewInt(35), nil)
	// Oracle prices have 8 decimals; amount*price*1e10/10^dec gives 1e18 USD.
	priceTo1e18 = new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil)
)

// reserve is one spoke reserve.
type reserve struct {
	id         *big.Int
	underlying string // 0x lowercase
	decimals   uint8
}

// Adapter implements lending.Adapter for one Aave V4 spoke.
type Adapter struct {
	spoke string
	proto lending.Protocol

	mu      sync.RWMutex
	addrs   lending.Addresses
	byID    map[string]reserve // reserve id (decimal) -> reserve
	byAsset map[string]reserve // underlying -> reserve

	topicSupply, topicWithdraw, topicBorrow, topicRepay string
	topicLiquidation, topicSetCollateral, topicDeficit  string
}

// New builds the adapter for one spoke.
func New(spoke string) *Adapter {
	spoke = lending.NormalizeAddr(spoke)
	return &Adapter{
		spoke:              spoke,
		proto:              lending.AaveV4Protocol(spoke),
		byID:               map[string]reserve{},
		byAsset:            map[string]reserve{},
		topicSupply:        lending.EventTopic(sigSupply),
		topicWithdraw:      lending.EventTopic(sigWithdraw),
		topicBorrow:        lending.EventTopic(sigBorrow),
		topicRepay:         lending.EventTopic(sigRepay),
		topicLiquidation:   lending.EventTopic(sigLiquidation),
		topicSetCollateral: lending.EventTopic(sigSetCollateral),
		topicDeficit:       lending.EventTopic(sigReportDeficit),
	}
}

func (a *Adapter) Protocol() lending.Protocol { return a.proto }

// Resolve reads the spoke's oracle and checks that the spoke lists reserves.
func (a *Adapter) Resolve(ctx context.Context, rpc *lending.Client) (lending.Addresses, []lending.VerifyNote, error) {
	var notes []lending.VerifyNote
	oracle, err := callAddr(ctx, rpc, a.spoke, lending.EncodeCall0(fnOracle))
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("ORACLE: %w", err)
	}
	count, err := callUint(ctx, rpc, a.spoke, lending.EncodeCall0(fnReserveCount))
	if err != nil {
		return lending.Addresses{}, notes, fmt.Errorf("getReserveCount: %w", err)
	}
	notes = append(notes,
		lending.VerifyNote{Role: lending.RoleSpoke, Expected: a.spoke, Resolved: a.spoke, OK: count.Sign() > 0,
			Detail: fmt.Sprintf("%s reserves", count)},
		lending.VerifyNote{Role: lending.RoleOracle, Resolved: oracle, OK: oracle != lending.ZeroAddress},
	)
	addrs := lending.Addresses{Pool: a.spoke, Oracle: oracle}
	if oracle == lending.ZeroAddress || count.Sign() == 0 {
		return addrs, notes, fmt.Errorf("aave v4 spoke %s: oracle=%s reserves=%s", a.spoke, oracle, count)
	}
	return addrs, notes, nil
}

// Configure stores the resolved addresses. The reserve list is owned by
// RefreshParams, so a Configure without params never wipes it.
func (a *Adapter) Configure(addrs lending.Addresses, _ []lending.AssetParam, _ lending.GlobalParams) {
	a.mu.Lock()
	a.addrs = addrs
	a.mu.Unlock()
}

func (a *Adapter) Discovery() lending.DiscoverySpec {
	return lending.DiscoverySpec{
		Addresses: []string{a.spoke},
		Topics: []string{
			a.topicSupply, a.topicWithdraw, a.topicBorrow, a.topicRepay,
			a.topicLiquidation, a.topicSetCollateral, a.topicDeficit,
		},
	}
}

// DecodeLog maps a spoke event to exposures. A reserve id the adapter does not
// know yet (listed after the last params refresh) still tracks the account, with
// an empty asset, so its health summary is read; its legs appear once the next
// event after a params refresh records the asset.
func (a *Adapter) DecodeLog(l lending.LogRow) []lending.Exposure {
	switch l.Topic0 {
	case a.topicSupply, a.topicWithdraw, a.topicSetCollateral:
		// reserveId=topic1, caller=topic2, user=topic3
		return a.exposure(lending.AddrFromTopic(l.Topic3), l.Topic1, lending.SideCollateral, l.Block)
	case a.topicBorrow, a.topicRepay:
		return a.exposure(lending.AddrFromTopic(l.Topic3), l.Topic1, lending.SideBorrow, l.Block)
	case a.topicDeficit:
		// reserveId=topic1, user=topic2
		return a.exposure(lending.AddrFromTopic(l.Topic2), l.Topic1, lending.SideBorrow, l.Block)
	case a.topicLiquidation:
		// collateralReserveId=topic1, debtReserveId=topic2, user=topic3
		user := lending.AddrFromTopic(l.Topic3)
		out := a.exposure(user, l.Topic1, lending.SideCollateral, l.Block)
		return append(out, a.exposure(user, l.Topic2, lending.SideBorrow, l.Block)...)
	}
	return nil
}

func (a *Adapter) exposure(user, reserveTopic string, side lending.Side, block uint32) []lending.Exposure {
	if user == lending.ZeroAddress {
		return nil
	}
	asset := ""
	// The store reports an all-zero topic as "" (absent). Every event decoded here
	// always carries its reserve id topic, so "" is reserve id 0, which is a real
	// reserve (WAVAX on Main, sAVAX on AVAX Correlated, USDe on Ethena, EURC on Forex).
	if reserveTopic == "" {
		reserveTopic = "0x0"
	}
	if id, ok := new(big.Int).SetString(trim0x(reserveTopic), 16); ok {
		a.mu.RLock()
		if r, found := a.byID[id.String()]; found {
			asset = r.underlying
		}
		a.mu.RUnlock()
	}
	return []lending.Exposure{{Account: user, Asset: asset, Side: side, Block: block}}
}

// BuildProbe reads getUserAccountData for the health summary and, when exposure
// is given, the user's supplied assets, total debt, collateral flag, dynamic
// config key and the oracle price of each exposed reserve.
func (a *Adapter) BuildProbe(account string, exposure []lending.Exposure) lending.HealthProbe {
	account = lending.NormalizeAddr(account)

	a.mu.RLock()
	spoke, oracle := a.spoke, a.addrs.Oracle
	var reserves []reserve
	seen := map[string]bool{}
	for _, e := range exposure {
		asset := lending.NormalizeAddr(e.Asset)
		if seen[asset] {
			continue
		}
		seen[asset] = true
		if r, ok := a.byAsset[asset]; ok {
			reserves = append(reserves, r)
		}
	}
	a.mu.RUnlock()

	calls := []lending.Call{
		{Target: spoke, AllowFailure: true, Data: lending.EncodeCall1Addr(fnGetUserAccountData, account)},
	}
	for _, r := range reserves {
		calls = append(calls,
			lending.Call{Target: spoke, AllowFailure: true, Data: lending.EncodeCallUintAddr(fnGetUserSupplied, r.id, account)},
			lending.Call{Target: spoke, AllowFailure: true, Data: lending.EncodeCallUintAddr(fnGetUserTotalDebt, r.id, account)},
			lending.Call{Target: spoke, AllowFailure: true, Data: lending.EncodeCallUintAddr(fnGetUserReserveStatus, r.id, account)},
			lending.Call{Target: spoke, AllowFailure: true, Data: lending.EncodeCallUintAddr(fnGetUserPosition, r.id, account)},
			lending.Call{Target: oracle, AllowFailure: true, Data: lending.EncodeCall1Uint(fnGetReservePrice, r.id)},
		)
	}

	proto := a.proto
	return lending.HealthProbe{
		Account: account,
		Calls:   calls,
		Decode: func(results []lending.CallResult, block uint64) lending.Health {
			return decodeHealth(proto, account, reserves, results, block)
		},
	}
}

// decodeHealth turns probe results into a Health. results[0] is the account data,
// then callsPerReserve results per reserve in BuildProbe order.
func decodeHealth(proto lending.Protocol, account string, reserves []reserve, results []lending.CallResult, block uint64) lending.Health {
	h := lending.Health{
		Account:      lending.Account{Protocol: proto, Address: account},
		BlockNumber:  block,
		HealthFactor: big.NewInt(0),
	}
	if len(results) == 0 || !results[0].Success || len(results[0].ReturnData) < 7*32 {
		return h // summary read reverted, skip this account (rule 5)
	}
	// UserAccountData: riskPremium(0), avgCollateralFactor(1), healthFactor(2),
	// totalCollateralValue(3), totalDebtValueRay(4), activeCollateralCount(5), borrowCount(6).
	sum := results[0].ReturnData
	debtRay := lending.Word(sum, 4)
	h.HealthFactor = lending.Word(sum, 2)
	h.CollateralBase = new(big.Int).Div(lending.Word(sum, 3), collateralDiv)
	h.DebtBase = new(big.Int).Div(debtRay, debtRayDiv)
	h.Liquidatable = debtRay.Sign() > 0 && h.HealthFactor.Cmp(lending.WAD) < 0
	h.OK = true

	for i, r := range reserves {
		idx := 1 + i*callsPerReserve
		if idx+callsPerReserve > len(results) {
			break
		}
		supplied, debt, status, pos, price := results[idx], results[idx+1], results[idx+2], results[idx+3], results[idx+4]
		if !supplied.Success || !debt.Success || !price.Success {
			continue
		}
		p := lending.Word(price.ReturnData, 0)
		if amt := lending.Word(supplied.ReturnData, 0); amt.Sign() > 0 {
			leg := lending.AssetPosition{
				Asset: r.underlying, Side: lending.SideCollateral,
				Amount: amt, BaseValue: value(amt, p, r.decimals),
				// getUserReserveStatus -> (usingAsCollateral, borrowing). Supply not
				// used as collateral backs no borrow but is still on the spoke.
				NotEntered: !(status.Success && lending.WordBool(status.ReturnData, 0)),
			}
			if pos.Success && len(pos.ReturnData) >= 5*32 {
				// UserPosition: drawnShares, premiumShares, premiumOffsetRay, suppliedShares, dynamicConfigKey.
				leg.ConfigKey = uint32(lending.WordU64(pos.ReturnData, 4))
			}
			h.Assets = append(h.Assets, leg)
		}
		if amt := lending.Word(debt.ReturnData, 0); amt.Sign() > 0 {
			h.Assets = append(h.Assets, lending.AssetPosition{
				Asset: r.underlying, Side: lending.SideDebt,
				Amount: amt, BaseValue: value(amt, p, r.decimals),
			})
		}
	}
	return h
}

// RefreshParams reads every spoke reserve: underlying, decimals, borrowable flag,
// and the collateral factor and max liquidation bonus at the reserve's current
// dynamic config key. V4 has a single collateral factor (no LTV/LT split), so it
// fills both LtvBps and LiquidationThresholdBps.
func (a *Adapter) RefreshParams(ctx context.Context, rpc *lending.Client) ([]lending.AssetParam, lending.GlobalParams, error) {
	globals := lending.GlobalParams{BaseCurrencyUnit: big.NewInt(1e8)}
	count, err := callUint(ctx, rpc, a.spoke, lending.EncodeCall0(fnReserveCount))
	if err != nil {
		return nil, globals, fmt.Errorf("getReserveCount: %w", err)
	}
	n := count.Int64()

	a.mu.RLock()
	prevByID := make(map[string]reserve, len(a.byID))
	for k, v := range a.byID {
		prevByID[k] = v
	}
	a.mu.RUnlock()

	byID := map[string]reserve{}
	byAsset := map[string]reserve{}
	var params []lending.AssetParam
	for i := int64(0); i < n; i++ {
		id := big.NewInt(i)
		res, err := rpc.EthCall(ctx, a.spoke, lending.EncodeCall1Uint(fnGetReserve, id), "latest")
		b := lending.DecodeHexBytes(res)
		if err != nil || len(b) < 7*32 {
			// Keep the previous reading of this reserve rather than dropping it.
			if prev, ok := prevByID[id.String()]; ok {
				byID[id.String()] = prev
				if _, dup := byAsset[prev.underlying]; !dup {
					byAsset[prev.underlying] = prev
				}
			}
			slog.Warn("aave v4: getReserve failed", "spoke", a.spoke, "reserve", i, "error", err)
			continue
		}
		// Reserve: underlying(0), hub(1), assetId(2), decimals(3), collateralRisk(4), flags(5), dynamicConfigKey(6).
		r := reserve{id: id, underlying: lending.NormalizeAddr(lending.Addr(b, 0)), decimals: uint8(lending.WordU64(b, 3))}
		key := lending.Word(b, 6)
		byID[id.String()] = r
		if other, dup := byAsset[r.underlying]; dup {
			slog.Warn("aave v4: spoke lists an underlying twice, keeping the lower reserve id",
				"spoke", a.spoke, "underlying", r.underlying, "kept", other.id, "ignored", id)
		} else {
			byAsset[r.underlying] = r
		}

		p := lending.AssetParam{Asset: r.underlying, Decimals: r.decimals}
		if sym, err := rpc.EthCall(ctx, r.underlying, lending.EncodeCall0(fnSymbol), "latest"); err == nil {
			p.Symbol = lending.DecodeString(lending.DecodeHexBytes(sym))
		}
		// ReserveConfig: collateralRisk(0), paused(1), frozen(2), borrowable(3), receiveSharesEnabled(4).
		if cfg, err := rpc.EthCall(ctx, a.spoke, lending.EncodeCall1Uint(fnGetReserveConfig, id), "latest"); err == nil {
			p.CanBorrow = lending.WordBool(lending.DecodeHexBytes(cfg), 3)
		}
		// DynamicReserveConfig: collateralFactor(0), maxLiquidationBonus(1), liquidationFee(2).
		if dyn, err := rpc.EthCall(ctx, a.spoke, lending.EncodeCall2Uint(fnGetDynamicConfig, id, key), "latest"); err == nil {
			db := lending.DecodeHexBytes(dyn)
			cf := uint16(lending.WordU64(db, 0))
			p.LiquidationThresholdBps, p.LtvBps = cf, cf
			p.LiquidationBonusBps = clampU16(lending.WordU64(db, 1))
			p.CanCollateral = cf > 0
		}
		params = append(params, p)
	}

	a.mu.Lock()
	a.byID, a.byAsset = byID, byAsset
	a.mu.Unlock()
	return params, globals, nil
}

// --- helpers ---

// value returns amount * price / 10^decimals scaled from the 8-decimal oracle to 1e18 USD.
func value(amount, price *big.Int, decimals uint8) *big.Int {
	if amount == nil || price == nil {
		return big.NewInt(0)
	}
	v := new(big.Int).Mul(amount, price)
	v.Mul(v, priceTo1e18)
	return v.Div(v, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
}

func callAddr(ctx context.Context, rpc *lending.Client, to, data string) (string, error) {
	res, err := rpc.EthCall(ctx, to, data, "latest")
	if err != nil {
		return "", err
	}
	return lending.NormalizeAddr(lending.Addr(lending.DecodeHexBytes(res), 0)), nil
}

func callUint(ctx context.Context, rpc *lending.Client, to, data string) (*big.Int, error) {
	res, err := rpc.EthCall(ctx, to, data, "latest")
	if err != nil {
		return nil, err
	}
	return lending.Word(lending.DecodeHexBytes(res), 0), nil
}

func clampU16(v uint64) uint16 {
	if v > 0xffff {
		return 0xffff
	}
	return uint16(v)
}

func trim0x(s string) string {
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		return s[2:]
	}
	return s
}
