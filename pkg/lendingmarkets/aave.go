package lendingmarkets

import (
	"context"
	"fmt"
	"math/big"
	"strings"
)

// ReadAaveV3 reads every active reserve of the Aave V3 Avalanche market.
func ReadAaveV3(ctx context.Context, c Caller) ([]Market, error) {
	r := reader{ctx, c}
	pool, err := r.address(AaveV3Provider, "getPool()")
	if err != nil {
		return nil, err
	}
	dp, err := r.address(AaveV3Provider, "getPoolDataProvider()")
	if err != nil {
		return nil, err
	}
	oracle, err := r.address(AaveV3Provider, "getPriceOracle()")
	if err != nil {
		return nil, err
	}
	unit, err := r.uint(oracle, "BASE_CURRENCY_UNIT()")
	if err != nil || unit.Sign() == 0 {
		unit = big.NewInt(1e8)
	}

	b, err := r.call(dp, 2, "getAllReservesTokens()")
	if err != nil {
		return nil, err
	}
	reserves := decodeTokenData(b)
	if len(reserves) == 0 {
		return nil, fmt.Errorf("no reserves from data provider %s", dp)
	}

	var out []Market
	for _, t := range reserves {
		m, active, err := readAaveV3Reserve(r, pool, dp, oracle, unit, t)
		if err != nil {
			return nil, err
		}
		if active {
			out = append(out, m)
		}
	}
	return out, nil
}

func readAaveV3Reserve(r reader, pool, dp, oracle string, unit *big.Int, t tokenData) (Market, bool, error) {
	// decimals, ltv, liquidationThreshold, liquidationBonus, reserveFactor,
	// usageAsCollateralEnabled, borrowingEnabled, stableBorrowRateEnabled,
	// isActive, isFrozen
	cfg, err := r.call(dp, 10, "getReserveConfigurationData(address)", addrWord(t.address))
	if err != nil {
		return Market{}, false, err
	}
	if !wordBool(cfg, 8) {
		return Market{}, false, nil
	}
	// unbacked, accruedToTreasuryScaled, totalAToken, totalStableDebt,
	// totalVariableDebt, liquidityRate, variableBorrowRate, ...
	data, err := r.call(dp, 7, "getReserveData(address)", addrWord(t.address))
	if err != nil {
		return Market{}, false, err
	}
	caps, err := r.call(dp, 2, "getReserveCaps(address)", addrWord(t.address))
	if err != nil {
		return Market{}, false, err
	}

	dec := uint8(wordUint(cfg, 0).Uint64())
	ltv := uint16(wordUint(cfg, 1).Uint64())
	lt := uint16(wordUint(cfg, 2).Uint64())
	m := Market{
		Protocol:          ProtocolAaveV3,
		Market:            pool,
		Asset:             t.address,
		Symbol:            t.symbol,
		Decimals:          dec,
		Supplied:          wordUint(data, 2),
		Borrowed:          new(big.Int).Add(wordUint(data, 3), wordUint(data, 4)),
		SupplyAPR:         ratioFloat(wordUint(data, 5), 27), // ray, already annual
		BorrowAPR:         ratioFloat(wordUint(data, 6), 27),
		CollateralEnabled: ptr(wordBool(cfg, 5)),
		LTVBps:            ptr(ltv),
		LiqThresholdBps:   ptr(lt),
		BorrowCap:         wholeTokenCap(wordUint(caps, 0), dec),
		SupplyCap:         wholeTokenCap(wordUint(caps, 1), dec),
	}
	if p, err := r.uint(oracle, "getAssetPrice(address)", addrWord(t.address)); err == nil && p.Sign() > 0 {
		m.PriceUSD = ptr(ratioFloat(p, len(unit.String())-1))
	}
	return m, true, nil
}

// wholeTokenCap converts an Aave cap in whole tokens to base units. 0 = none.
func wholeTokenCap(c *big.Int, dec uint8) *big.Int {
	if c.Sign() == 0 {
		return nil
	}
	return new(big.Int).Mul(c, pow10(dec))
}

type tokenData struct {
	symbol  string
	address string
}

// decodeTokenData decodes TokenData[] { string symbol, address tokenAddress }.
func decodeTokenData(b []byte) []tokenData {
	arr := int(wordUint(b, 0).Int64())
	if arr+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[arr : arr+32]).Int64())
	head := arr + 32
	var out []tokenData
	for i := 0; i < n; i++ {
		hp := head + i*32
		if hp+32 > len(b) {
			break
		}
		el := head + int(new(big.Int).SetBytes(b[hp:hp+32]).Int64())
		if el+64 > len(b) {
			break
		}
		addr := strings.ToLower(wordAddr(b[el:el+64], 1))
		s := el + int(new(big.Int).SetBytes(b[el:el+32]).Int64())
		sym := ""
		if s+32 <= len(b) {
			l := int(new(big.Int).SetBytes(b[s : s+32]).Int64())
			if s+32+l <= len(b) {
				sym = string(b[s+32 : s+32+l])
			}
		}
		out = append(out, tokenData{symbol: sym, address: addr})
	}
	return out
}

// ReadAaveV4 reads every asset of the Aave V4 Core Hub. Liquidity is pooled at
// the hub, so one row per hub asset; collateral parameters and caps are set
// per spoke and are left nil. Prices come from the first spoke that lists the
// asset.
func ReadAaveV4(ctx context.Context, c Caller) ([]Market, error) {
	r := reader{ctx, c}
	n, err := r.uint(AaveV4CoreHub, "getAssetCount()")
	if err != nil {
		return nil, err
	}
	oracles := map[string]string{}
	for _, s := range AaveV4Spokes {
		if o, err := r.address(s, "ORACLE()"); err == nil {
			oracles[s] = o
		}
	}

	var out []Market
	for i := uint64(0); i < n.Uint64(); i++ {
		id := uintWord(i)
		ud, err := r.call(AaveV4CoreHub, 2, "getAssetUnderlyingAndDecimals(uint256)", id)
		if err != nil {
			return nil, err
		}
		asset := wordAddr(ud, 0)
		added, err := r.uint(AaveV4CoreHub, "getAddedAssets(uint256)", id)
		if err != nil {
			return nil, err
		}
		owed, err := r.uint(AaveV4CoreHub, "getAssetTotalOwed(uint256)", id)
		if err != nil {
			return nil, err
		}
		rate, err := r.uint(AaveV4CoreHub, "getAssetDrawnRate(uint256)", id)
		if err != nil {
			return nil, err
		}
		// feeReceiver, liquidityFee, irStrategy, reinvestmentController
		cfg, err := r.call(AaveV4CoreHub, 2, "getAssetConfig(uint256)", id)
		if err != nil {
			return nil, err
		}
		sym, _, err := r.token(asset)
		if err != nil {
			return nil, err
		}

		borrowAPR := ratioFloat(rate, 27)
		m := Market{
			Protocol:  ProtocolAaveV4,
			Market:    strings.ToLower(AaveV4CoreHub),
			Asset:     asset,
			Symbol:    sym,
			Decimals:  uint8(wordUint(ud, 1).Uint64()),
			Supplied:  added,
			Borrowed:  owed,
			BorrowAPR: borrowAPR,
			SupplyAPR: v4SupplyAPR(borrowAPR, owed, added, wordUint(cfg, 1).Uint64()),
		}
		m.PriceUSD = v4Price(r, oracles, i)
		out = append(out, m)
	}
	return out, nil
}

// v4SupplyAPR estimates what suppliers earn: interest on everything owed at the
// drawn rate, less the hub's liquidity fee, spread over everything added.
// Borrowers' risk premiums add on top, so this slightly understates it.
func v4SupplyAPR(borrowAPR float64, owed, added *big.Int, feeBps uint64) float64 {
	if added.Sign() == 0 || feeBps >= 10000 {
		return 0
	}
	u, _ := new(big.Float).Quo(new(big.Float).SetInt(owed), new(big.Float).SetInt(added)).Float64()
	return borrowAPR * u * float64(10000-feeBps) / 10000
}

// v4Price reads the asset price from the first spoke that lists it. Spoke
// oracles report USD with 8 decimals.
func v4Price(r reader, oracles map[string]string, assetID uint64) *float64 {
	for _, s := range AaveV4Spokes {
		o, ok := oracles[s]
		if !ok {
			continue
		}
		rid, err := r.uint(s, "getReserveId(address,uint256)", addrWord(AaveV4CoreHub), uintWord(assetID))
		if err != nil {
			continue // not listed on this spoke
		}
		p, err := r.uint(o, "getReservePrice(uint256)", uintWord(rid.Uint64()))
		if err != nil || p.Sign() == 0 {
			continue
		}
		return ptr(ratioFloat(p, 8))
	}
	return nil
}
