package stealtime

import (
	"context"
	"math/big"
	"sync"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

var (
	mantissaPerBps = new(big.Int).Exp(big.NewInt(10), big.NewInt(14), nil) // 1e18 -> 10000
	hf095          = new(big.Int).Div(new(big.Int).Mul(big.NewInt(95), lending.WAD), big.NewInt(100))
)

// blockParams implements prefilter.ParamsProvider as of a fixed historical block,
// reading bonus and close factor on-chain at that block. The stored bonus is a
// multiplier in bps; prefilter wants the premium, so we subtract 10000.
type blockParams struct {
	ctx              context.Context
	rpc              *lending.Client
	block            uint64
	aaveDataProvider common.Address
	benqiComptroller common.Address

	mu          sync.Mutex
	aaveBonus   map[common.Address]uint64
	benqiLoaded bool
	benqiBonus  uint64
	benqiClose  uint64
}

func newBlockParams(ctx context.Context, rpc *lending.Client, block uint64, aaveDataProvider, benqiComptroller common.Address) *blockParams {
	return &blockParams{
		ctx: ctx, rpc: rpc, block: block,
		aaveDataProvider: aaveDataProvider, benqiComptroller: benqiComptroller,
		aaveBonus: map[common.Address]uint64{},
	}
}

func (p *blockParams) BonusBps(protocol string, collateral common.Address) (uint64, bool) {
	if protocol == "benqi" {
		p.loadBenqi()
		return p.benqiBonus, p.benqiBonus > 0
	}
	p.mu.Lock()
	if v, ok := p.aaveBonus[collateral]; ok {
		p.mu.Unlock()
		return v, v > 0
	}
	p.mu.Unlock()

	res, err := p.rpc.EthCall(p.ctx, p.aaveDataProvider.Hex(), lending.EncodeCall1Addr("getReserveConfigurationData(address)", collateral.Hex()), blockHex(p.block))
	v := uint64(0)
	if err == nil {
		v = premium(lending.Word(lending.DecodeHexBytes(res), 3).Uint64()) // word3 = liquidationBonus multiplier
	}
	p.mu.Lock()
	p.aaveBonus[collateral] = v
	p.mu.Unlock()
	return v, v > 0
}

func (p *blockParams) CloseFactorBps(protocol string, hf *big.Int) uint64 {
	if protocol == "benqi" {
		p.loadBenqi()
		if p.benqiClose > 0 {
			return p.benqiClose
		}
		return 5000
	}
	if hf != nil && hf.Sign() > 0 && hf.Cmp(hf095) < 0 {
		return 10000
	}
	return 5000
}

func (p *blockParams) loadBenqi() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.benqiLoaded {
		return
	}
	p.benqiLoaded = true
	bh := blockHex(p.block)
	if res, err := p.rpc.EthCall(p.ctx, p.benqiComptroller.Hex(), lending.EncodeCall0("liquidationIncentiveMantissa()"), bh); err == nil {
		p.benqiBonus = premium(toBps(lending.Word(lending.DecodeHexBytes(res), 0)))
	}
	if res, err := p.rpc.EthCall(p.ctx, p.benqiComptroller.Hex(), lending.EncodeCall0("closeFactorMantissa()"), bh); err == nil {
		p.benqiClose = toBps(lending.Word(lending.DecodeHexBytes(res), 0))
	}
}

func toBps(mantissa *big.Int) uint64 {
	if mantissa == nil {
		return 0
	}
	return new(big.Int).Div(mantissa, mantissaPerBps).Uint64()
}

func premium(multiplierBps uint64) uint64 {
	if multiplierBps <= 10000 {
		return 0
	}
	return multiplierBps - 10000
}
