package kmeasure

import (
	"context"
	"math/big"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// hf095 is 0.95 in 1e18, the Aave threshold below which the close factor is 100%.
var hf095 = new(big.Int).Div(new(big.Int).Mul(big.NewInt(95), lending.WAD), big.NewInt(100))

// Params implements prefilter.ParamsProvider from the lending tables. The tables
// store the liquidation bonus as a multiplier in bps (Aave per-reserve e.g. 10500,
// Benqi global incentive e.g. 11000); prefilter wants the premium, so we subtract
// 10000 (10500 -> 500, 11000 -> 1000).
type Params struct {
	aaveBonusPremium  map[common.Address]uint64
	benqiBonusPremium uint64
	benqiCloseFactor  uint64
}

// LoadParams reads Aave per-reserve bonuses and the Benqi global incentive and
// close factor from ClickHouse.
func LoadParams(ctx context.Context, conn driver.Conn, chainID uint32) (*Params, error) {
	p := &Params{aaveBonusPremium: map[common.Address]uint64{}}

	rows, err := conn.Query(ctx, `
		SELECT asset, liquidation_bonus_bps
		FROM (SELECT * FROM lending_protocol_params FINAL)
		WHERE chain_id = ? AND protocol = 'aave-v3'
	`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var asset [20]byte
		var bonus uint16
		if err := rows.Scan(&asset, &bonus); err != nil {
			return nil, err
		}
		p.aaveBonusPremium[common.BytesToAddress(asset[:])] = premium(uint64(bonus))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var inc, cf uint16
	row := conn.QueryRow(ctx, `
		SELECT liquidation_incentive_bps, close_factor_bps
		FROM (SELECT * FROM lending_protocol_globals FINAL)
		WHERE chain_id = ? AND protocol = 'benqi'
	`, chainID)
	if err := row.Scan(&inc, &cf); err == nil {
		p.benqiBonusPremium = premium(uint64(inc))
		p.benqiCloseFactor = uint64(cf)
	}

	return p, nil
}

// premium converts a bonus multiplier in bps to the premium above 100%.
func premium(multiplierBps uint64) uint64 {
	if multiplierBps <= 10000 {
		return 0
	}
	return multiplierBps - 10000
}

// BonusBps returns the liquidation bonus premium in bps for the collateral asset.
func (p *Params) BonusBps(protocol string, collateral common.Address) (uint64, bool) {
	if protocol == "benqi" {
		return p.benqiBonusPremium, p.benqiBonusPremium > 0
	}
	v, ok := p.aaveBonusPremium[collateral]
	return v, ok && v > 0
}

// CloseFactorBps returns the close factor in bps. Benqi uses its global value;
// Aave allows 100% when the health factor is below 0.95, otherwise 50%.
func (p *Params) CloseFactorBps(protocol string, hf *big.Int) uint64 {
	if protocol == "benqi" {
		if p.benqiCloseFactor > 0 {
			return p.benqiCloseFactor
		}
		return 5000
	}
	if hf != nil && hf.Sign() > 0 && hf.Cmp(hf095) < 0 {
		return 10000
	}
	return 5000
}
