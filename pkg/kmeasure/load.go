package kmeasure

import (
	"context"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/prefilter"
)

// BuildPositions turns raw feed positions into prefilter.Position values, resolving
// each leg's swap token and decimals. A position with no collateral legs (the
// zero-collateral bad-debt case where the feed sends collateral: null) yields a
// position with only debt legs, which the pre-filter correctly classifies no_pair.
func BuildPositions(ctx context.Context, raws []rawPosition, res Resolver) ([]prefilter.Position, error) {
	out := make([]prefilter.Position, 0, len(raws))
	for _, rp := range raws {
		pos := prefilter.Position{
			Protocol:     rp.Protocol,
			Account:      common.HexToAddress(rp.Account),
			HealthFactor: parseBig(rp.HealthFactor),
		}
		for _, l := range rp.Collateral {
			leg, err := buildLeg(ctx, res, rp.Protocol, l, prefilter.SideCollateral)
			if err != nil {
				return nil, err
			}
			pos.Legs = append(pos.Legs, leg)
		}
		for _, l := range rp.Debt {
			leg, err := buildLeg(ctx, res, rp.Protocol, l, prefilter.SideDebt)
			if err != nil {
				return nil, err
			}
			pos.Legs = append(pos.Legs, leg)
		}
		out = append(out, pos)
	}
	return out, nil
}

func buildLeg(ctx context.Context, res Resolver, protocol string, l rawLeg, side prefilter.Side) (prefilter.AssetLeg, error) {
	asset := common.HexToAddress(l.Asset)
	info, err := res.Resolve(ctx, protocol, asset)
	if err != nil {
		return prefilter.AssetLeg{}, err
	}
	return prefilter.AssetLeg{
		Asset:     info.Underlying,
		Symbol:    l.Symbol,
		Side:      side,
		Amount:    parseBig(l.Amount),
		Decimals:  info.Decimals,
		BaseValue: parseBig(l.BaseValue),
	}, nil
}
