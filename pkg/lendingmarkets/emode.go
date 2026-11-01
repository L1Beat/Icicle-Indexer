package lendingmarkets

import (
	"context"
	"fmt"
	"strings"
)

// EModeEntry is one asset's collateral setting inside a group: an Aave V3
// E-Mode category, or an Aave V4 spoke (V4 sets collateral per spoke reserve).
type EModeEntry struct {
	Protocol   string
	GroupID    string // V3 category id, V4 spoke address
	GroupLabel string
	Asset      string // underlying, lowercase
	LTVBps     uint16 // effective: 0 when the asset is in the category's ltvzero bitmap
	ConfLTVBps uint16 // the group's configured LTV
	LTBps      uint16
	BonusBps   uint16 // liquidation bonus above 100%, 100 = 1%
	Collateral bool   // counts as collateral in this group
	Borrowable bool   // can be borrowed in this group
	// PrevLTVBps is the V4 config just before the current one, which positions
	// opened earlier keep using. Nil for V3 and for reserves on their first config.
	PrevLTVBps *uint16
}

// AaveV4SpokeLabels names the spokes (aave-address-book AaveV4Avalanche).
var AaveV4SpokeLabels = map[string]string{
	"0x435272ceff93a1e657e8abfdf0a13e95900a3a56": "Main",
	"0x3b517594277c67307cf2d7cbe6fe1d4399b68c41": "AVAX Correlated",
	"0xaf22446124e566e54845f65b722e4dcbf12b650a": "Ethena Ecosystem",
	"0x6a37776b5e026dbdf043b4f933c323c84dd1b514": "Forex",
}

// maxEModeCategory bounds the V3 category scan. Ids are uint8, assigned from 1.
const maxEModeCategory = 64

// ReadAaveV3EMode reads every configured Aave V3 E-Mode category (v3.2+
// getters) and returns one entry per asset in its collateral or borrowable
// bitmap. Bit i is getReservesList()[i]. In E-Mode the borrowable bitmap is
// enforced instead of the reserve's borrowing flag, so an empty bitmap means
// nothing can be borrowed in that category. An asset in the ltvzero bitmap has
// LTV 0 in the category (it backs no new borrows but keeps the liquidation
// threshold), as in the Pool's getUserReserveLtv.
func ReadAaveV3EMode(ctx context.Context, c Caller) ([]EModeEntry, error) {
	r := reader{ctx, c}
	pool, err := r.address(AaveV3Provider, "getPool()")
	if err != nil {
		return nil, err
	}
	b, err := r.call(pool, 2, "getReservesList()")
	if err != nil {
		return nil, err
	}
	reserves := addressArray(b)

	var out []EModeEntry
	empty := 0
	for id := uint64(1); id <= maxEModeCategory && empty < 3; id++ {
		cfg, err := r.call(pool, 3, "getEModeCategoryCollateralConfig(uint8)", uintWord(id))
		if err != nil {
			return nil, err
		}
		ltv, lt, bonus := wordUint(cfg, 0).Uint64(), wordUint(cfg, 1).Uint64(), wordUint(cfg, 2).Uint64()
		if lt == 0 {
			empty++
			continue
		}
		empty = 0
		lb, err := r.call(pool, 1, "getEModeCategoryLabel(uint8)", uintWord(id))
		if err != nil {
			return nil, err
		}
		coll, err := r.uint(pool, "getEModeCategoryCollateralBitmap(uint8)", uintWord(id))
		if err != nil {
			return nil, err
		}
		borr, err := r.uint(pool, "getEModeCategoryBorrowableBitmap(uint8)", uintWord(id))
		if err != nil {
			return nil, err
		}
		zero, err := r.uint(pool, "getEModeCategoryLtvzeroBitmap(uint8)", uintWord(id))
		if err != nil {
			return nil, err
		}
		label := abiString(lb)
		bonusExtra := uint16(0)
		if bonus > 10000 {
			bonusExtra = uint16(bonus - 10000)
		}
		for i, a := range reserves {
			isColl, isBorr := coll.Bit(i) == 1, borr.Bit(i) == 1
			if !isColl && !isBorr {
				continue
			}
			eff := uint16(ltv)
			if zero.Bit(i) == 1 {
				eff = 0
			}
			out = append(out, EModeEntry{
				Protocol: ProtocolAaveV3, GroupID: fmt.Sprint(id), GroupLabel: label, Asset: strings.ToLower(a),
				LTVBps: eff, ConfLTVBps: uint16(ltv), LTBps: uint16(lt), BonusBps: bonusExtra, Collateral: isColl, Borrowable: isBorr,
			})
		}
	}
	return out, nil
}

// ReadAaveV4Spokes reads each Aave V4 spoke's reserves: the collateral factor
// of the current dynamic config (V4 uses one factor for borrowing power and
// liquidation), the liquidation bonus cap, and whether the reserve can be
// borrowed (borrowable, not paused or frozen).
func ReadAaveV4Spokes(ctx context.Context, c Caller) ([]EModeEntry, error) {
	r := reader{ctx, c}
	var out []EModeEntry
	for _, spoke := range AaveV4Spokes {
		key := strings.ToLower(spoke)
		n, err := r.uint(spoke, "getReserveCount()")
		if err != nil {
			return nil, err
		}
		for i := uint64(0); i < n.Uint64(); i++ {
			// underlying, hub, assetId, decimals, collateralRisk, flags, dynamicConfigKey
			res, err := r.call(spoke, 7, "getReserve(uint256)", uintWord(i))
			if err != nil {
				return nil, err
			}
			// collateralRisk, paused, frozen, borrowable, receiveSharesEnabled
			cfg, err := r.call(spoke, 5, "getReserveConfig(uint256)", uintWord(i))
			if err != nil {
				return nil, err
			}
			dk := wordUint(res, 6).Uint64()
			// collateralFactor, maxLiquidationBonus, liquidationFee
			dyn, err := r.call(spoke, 3, "getDynamicReserveConfig(uint256,uint32)", uintWord(i), uintWord(dk))
			if err != nil {
				return nil, err
			}
			cf := uint16(wordUint(dyn, 0).Uint64())
			bonus := wordUint(dyn, 1).Uint64()
			e := EModeEntry{
				Protocol: ProtocolAaveV4, GroupID: key, GroupLabel: AaveV4SpokeLabels[key], Asset: wordAddr(res, 0),
				LTVBps: cf, ConfLTVBps: cf, LTBps: cf, Collateral: cf > 0,
				Borrowable: wordBool(cfg, 3) && !wordBool(cfg, 1) && !wordBool(cfg, 2),
			}
			if bonus > 10000 {
				e.BonusBps = uint16(bonus - 10000)
			}
			if dk > 0 {
				if prev, err := r.call(spoke, 1, "getDynamicReserveConfig(uint256,uint32)", uintWord(i), uintWord(dk-1)); err == nil {
					p := uint16(wordUint(prev, 0).Uint64())
					e.PrevLTVBps = &p
				}
			}
			out = append(out, e)
		}
	}
	return out, nil
}
