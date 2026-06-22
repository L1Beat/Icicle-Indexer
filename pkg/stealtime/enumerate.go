// Package stealtime is an offline, block-pinned, read-only backtest. For every
// liquidation that actually happened in our raw_logs history, it reconstructs the
// block at which the position first became liquidatable and stayed so (crossing),
// and the block where the liquidation landed (taken). steal_time = taken -
// crossing, measured only over opportunities that were profitable at the crossing
// block via pkg/prefilter. The distribution tells us whether profitable
// liquidations exist on Avalanche and whether incumbents leave room to win them.
//
// Offline and read-only: no keys, no submission, no contract deployment. Every
// historical read is pinned to its block. No em dashes anywhere, per house style.
package stealtime

import (
	"context"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// Event topic0 hashes for the liquidation events.
var (
	topicAaveLiquidation  = lending.EventTopic("LiquidationCall(address,address,address,uint256,uint256,address,bool)")
	topicBenqiLiquidation = lending.EventTopic("LiquidateBorrow(address,address,uint256,address,uint256)")
)

// Liquidation is one realized liquidation decoded from raw_logs. For Aave the
// asset fields are underlyings; for Benqi they are qiToken markets, resolved to
// underlyings later. The liquidator is kept for incumbent-concentration analysis.
type Liquidation struct {
	Protocol        string // aave-v3 | benqi
	Account         common.Address
	Liquidator      common.Address
	DebtAsset       common.Address
	CollateralAsset common.Address
	TakenBlock      uint64
}

// decodeAave decodes an Aave LiquidationCall. Indexed: collateralAsset (t1),
// debtAsset (t2), user (t3). Data: debtToCover, liquidatedCollateralAmount,
// liquidator, receiveAToken.
func decodeAave(l lending.LogRow) (Liquidation, bool) {
	if l.Topic0 != topicAaveLiquidation {
		return Liquidation{}, false
	}
	user := common.HexToAddress(lending.AddrFromTopic(l.Topic3))
	if user == (common.Address{}) {
		return Liquidation{}, false
	}
	return Liquidation{
		Protocol:        "aave-v3",
		Account:         user,
		CollateralAsset: common.HexToAddress(lending.AddrFromTopic(l.Topic1)),
		DebtAsset:       common.HexToAddress(lending.AddrFromTopic(l.Topic2)),
		Liquidator:      common.HexToAddress(lending.Addr(l.Data, 2)),
		TakenBlock:      uint64(l.Block),
	}, true
}

// decodeBenqi decodes a Benqi LiquidateBorrow. Unindexed data: liquidator,
// borrower, repayAmount, cTokenCollateral, seizeTokens. The borrowed market is the
// emitting qiToken (the log address).
func decodeBenqi(l lending.LogRow) (Liquidation, bool) {
	if l.Topic0 != topicBenqiLiquidation {
		return Liquidation{}, false
	}
	borrower := common.HexToAddress(lending.Addr(l.Data, 1))
	if borrower == (common.Address{}) {
		return Liquidation{}, false
	}
	return Liquidation{
		Protocol:        "benqi",
		Account:         borrower,
		Liquidator:      common.HexToAddress(lending.Addr(l.Data, 0)),
		DebtAsset:       common.HexToAddress(l.Address),
		CollateralAsset: common.HexToAddress(lending.Addr(l.Data, 3)),
		TakenBlock:      uint64(l.Block),
	}, true
}

// Enumerate scans raw_logs for Aave and Benqi liquidations in [fromBlock, toBlock]
// using the lending store's log reader. aavePool is the Aave Pool address;
// benqiMarkets are the qiToken markets.
func Enumerate(ctx context.Context, store *lending.Store, aavePool string, benqiMarkets []string, fromBlock, toBlock uint64) ([]Liquidation, error) {
	var out []Liquidation

	if aavePool != "" {
		logs, err := store.ReadLogs(ctx, []string{aavePool}, []string{topicAaveLiquidation}, fromBlock, toBlock)
		if err != nil {
			return nil, err
		}
		for _, l := range logs {
			if liq, ok := decodeAave(l); ok {
				out = append(out, liq)
			}
		}
	}

	if len(benqiMarkets) > 0 {
		logs, err := store.ReadLogs(ctx, benqiMarkets, []string{topicBenqiLiquidation}, fromBlock, toBlock)
		if err != nil {
			return nil, err
		}
		for _, l := range logs {
			if liq, ok := decodeBenqi(l); ok {
				out = append(out, liq)
			}
		}
	}

	return out, nil
}
