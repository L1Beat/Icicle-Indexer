package kmeasure

import (
	"context"
	"math/big"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/libevm/common"

	"icicle/pkg/prefilter"
)

// ChainlinkAvaxUsd is the Chainlink AVAX/USD feed on Avalanche (8 decimals).
var ChainlinkAvaxUsd = common.HexToAddress("0x0A77230d17318075983913bC2145DB16C7366156")

var mul1e10 = new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil)

// CostOpts carries the configurable and fallback cost inputs.
type CostOpts struct {
	GasUnits          uint64
	MinProfitUSD1e18  *big.Int
	FlashFeeBps       uint64   // fallback when the on-chain read fails
	GasPriceWeiFallbk *big.Int // fallback when eth_gasPrice fails
	NativeUSDFallback *big.Int // fallback AVAX price, 1e18
	AvaxUsdFeed       common.Address
}

// BuildCostModel populates prefilter.CostModel from live values: current gas price,
// the AVAX price from Chainlink, and Aave's current flash-loan premium. Each falls
// back to a configured value if its read fails.
func BuildCostModel(ctx context.Context, r EthReader, conn driver.Conn, chainID uint32, opts CostOpts) prefilter.CostModel {
	cm := prefilter.CostModel{
		GasUnits:         opts.GasUnits,
		MinProfitUSD1e18: opts.MinProfitUSD1e18,
		FlashFeeBps:      opts.FlashFeeBps,
		GasPriceWei:      opts.GasPriceWeiFallbk,
		NativeUSD1e18:    opts.NativeUSDFallback,
	}

	if gp, err := r.GasPrice(ctx); err == nil && gp.Sign() > 0 {
		cm.GasPriceWei = gp
	}

	feed := opts.AvaxUsdFeed
	if feed == (common.Address{}) {
		feed = ChainlinkAvaxUsd
	}
	if ans, err := callUint(ctx, r, feed, "latestAnswer()"); err == nil && ans.Sign() > 0 {
		cm.NativeUSD1e18 = new(big.Int).Mul(ans, mul1e10) // 1e8 -> 1e18
	}

	if pool, ok := aavePool(ctx, conn, chainID); ok {
		if fee, err := callUint(ctx, r, pool, "FLASHLOAN_PREMIUM_TOTAL()"); err == nil && fee.Sign() > 0 {
			cm.FlashFeeBps = fee.Uint64()
		}
	}

	return cm
}

// aavePool reads the resolved Aave Pool address from lending_protocol_addresses.
func aavePool(ctx context.Context, conn driver.Conn, chainID uint32) (common.Address, bool) {
	var b [20]byte
	row := conn.QueryRow(ctx, `
		SELECT address FROM (SELECT * FROM lending_protocol_addresses FINAL)
		WHERE chain_id = ? AND protocol = 'aave-v3' AND role = 'pool'
		LIMIT 1
	`, chainID)
	if err := row.Scan(&b); err != nil {
		return common.Address{}, false
	}
	addr := common.BytesToAddress(b[:])
	if addr == (common.Address{}) {
		return common.Address{}, false
	}
	return addr, true
}
