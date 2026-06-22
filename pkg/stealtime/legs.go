package stealtime

import (
	"context"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/kmeasure"
	"icicle/pkg/lending"
	"icicle/pkg/prefilter"
)

// assemblePosition reconstructs a position's per-asset legs at a historical block
// by reusing the live lending adapter's probe (BuildProbe) executed block-pinned,
// then mapping legs to prefilter form with underlying and decimals resolved. The
// exposure passed is the liquidation's own collateral and debt assets.
func assemblePosition(ctx context.Context, rpc *lending.Client, adapter lending.Adapter, resolver kmeasure.Resolver, protocol string, liq Liquidation, block uint64) (prefilter.Position, bool) {
	exposure := []lending.Exposure{
		{Account: liq.Account.Hex(), Asset: liq.CollateralAsset.Hex(), Side: lending.SideCollateral, Block: uint32(block)},
		{Account: liq.Account.Hex(), Asset: liq.DebtAsset.Hex(), Side: lending.SideBorrow, Block: uint32(block)},
	}
	probe := adapter.BuildProbe(liq.Account.Hex(), exposure)
	health := executeProbeAt(ctx, rpc, probe, block)
	if !health.OK {
		return prefilter.Position{}, false
	}

	pos := prefilter.Position{Protocol: protocol, Account: liq.Account, HealthFactor: health.HealthFactor}
	for _, a := range health.Assets {
		info, err := resolver.Resolve(ctx, protocol, common.HexToAddress(a.Asset))
		if err != nil {
			continue
		}
		side := prefilter.SideCollateral
		if a.Side == lending.SideDebt {
			side = prefilter.SideDebt
		}
		pos.Legs = append(pos.Legs, prefilter.AssetLeg{
			Asset:     info.Underlying,
			Side:      side,
			Amount:    a.Amount,
			Decimals:  info.Decimals,
			BaseValue: a.BaseValue,
		})
	}
	return pos, true
}
