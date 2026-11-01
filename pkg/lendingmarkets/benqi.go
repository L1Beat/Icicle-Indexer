package lendingmarkets

import (
	"context"
	"math/big"
	"strings"
)

// ReadBenqi reads every listed market of the BENQI Core comptroller.
func ReadBenqi(ctx context.Context, c Caller) ([]Market, error) {
	r := reader{ctx, c}
	oracle, err := r.address(BenqiComptroller, "oracle()")
	if err != nil {
		return nil, err
	}
	b, err := r.call(BenqiComptroller, 2, "getAllMarkets()")
	if err != nil {
		return nil, err
	}
	var out []Market
	for _, q := range addressArray(b) {
		m, listed, err := readBenqiMarket(r, oracle, q)
		if err != nil {
			return nil, err
		}
		if listed {
			out = append(out, m)
		}
	}
	return out, nil
}

func readBenqiMarket(r reader, oracle, q string) (Market, bool, error) {
	// isListed, collateralFactorMantissa, isQied
	mk, err := r.call(BenqiComptroller, 2, "markets(address)", addrWord(q))
	if err != nil {
		return Market{}, false, err
	}
	if !wordBool(mk, 0) {
		return Market{}, false, nil
	}

	// qiAVAX holds native AVAX and has no underlying().
	asset, sym, dec := NativeAVAX, "AVAX", uint8(18)
	if u, err := r.address(q, "underlying()"); err == nil {
		asset = u
		if sym, dec, err = r.token(u); err != nil {
			return Market{}, false, err
		}
	}

	var v [5]*big.Int
	for i, sig := range []string{"getCash()", "totalBorrows()", "totalReserves()",
		"supplyRatePerTimestamp()", "borrowRatePerTimestamp()"} {
		if v[i], err = r.uint(q, sig); err != nil {
			return Market{}, false, err
		}
	}
	cash, borrows, reserves := v[0], v[1], v[2]
	supplied := new(big.Int).Sub(new(big.Int).Add(cash, borrows), reserves)
	if supplied.Sign() < 0 {
		supplied.SetInt64(0)
	}

	// Compound-style markets have one collateral factor: it caps borrowing and
	// is also the point where an account becomes liquidatable.
	cf := cfBps(wordUint(mk, 1))
	m := Market{
		Protocol:          ProtocolBenqi,
		Market:            strings.ToLower(q),
		Asset:             strings.ToLower(asset),
		Symbol:            sym,
		Decimals:          dec,
		Supplied:          supplied,
		Borrowed:          borrows,
		SupplyAPR:         rateAPR(v[3], 18),
		BorrowAPR:         rateAPR(v[4], 18),
		CollateralEnabled: ptr(cf > 0),
		LTVBps:            ptr(cf),
		LiqThresholdBps:   ptr(cf),
	}
	if bc, err := r.uint(BenqiComptroller, "borrowCaps(address)", addrWord(q)); err == nil && bc.Sign() > 0 {
		m.BorrowCap = bc
	}
	// The oracle scales prices to 36 - underlying decimals.
	if p, err := r.uint(oracle, "getUnderlyingPrice(address)", addrWord(q)); err == nil && p.Sign() > 0 {
		m.PriceUSD = ptr(ratioFloat(p, 36-int(dec)))
	}
	return m, true, nil
}

func cfBps(mantissa *big.Int) uint16 {
	return uint16(new(big.Int).Div(mantissa, big.NewInt(1e14)).Uint64())
}

// addressArray decodes a single address[] return.
func addressArray(b []byte) []string {
	off := int(wordUint(b, 0).Int64())
	if off+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[off : off+32]).Int64())
	var out []string
	for i := 0; i < n; i++ {
		s := off + 32 + i*32
		if s+32 > len(b) {
			break
		}
		out = append(out, wordAddr(b[s:s+32], 0))
	}
	return out
}
