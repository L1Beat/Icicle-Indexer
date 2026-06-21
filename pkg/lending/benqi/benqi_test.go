package benqi

import (
	"math/big"
	"testing"

	"icicle/pkg/lending"
)

// mantissa returns num/den scaled to 1e18.
func mantissa(num, den int64) *big.Int {
	n := new(big.Int).Mul(big.NewInt(num), lending.WAD)
	return n.Div(n, big.NewInt(den))
}

// dataWords builds an ABI data payload of left-padded address words.
func dataWords(addrs ...string) []byte {
	var out []byte
	for _, a := range addrs {
		w := make([]byte, 32)
		copy(w[12:], lending.DecodeHexBytes(a))
		out = append(out, w...)
	}
	return out
}

func TestDecodeMint(t *testing.T) {
	a := New("")
	market := "0x5c0401e81bc07ca70fad469b451682c0d747ef1c"
	minter := "0x2222222222222222222222222222222222222222"

	exp := a.DecodeLog(lending.LogRow{
		Address: market,
		Topic0:  a.topicMint,
		Data:    dataWords(minter),
		Block:   10,
	})
	if len(exp) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(exp))
	}
	if exp[0].Account != lending.NormalizeAddr(minter) {
		t.Errorf("account: got %s want %s", exp[0].Account, minter)
	}
	if exp[0].Asset != lending.NormalizeAddr(market) {
		t.Errorf("asset: got %s want %s", exp[0].Asset, market)
	}
	if exp[0].Side != lending.SideCollateral {
		t.Errorf("side: got %s want collateral", exp[0].Side)
	}
}

func TestDecodeRepayBorrowUsesBorrower(t *testing.T) {
	a := New("")
	market := "0x5c0401e81bc07ca70fad469b451682c0d747ef1c"
	payer := "0x1111111111111111111111111111111111111111"
	borrower := "0x2222222222222222222222222222222222222222"

	// RepayBorrow(payer, borrower, ...): the tracked account is the borrower (word1).
	exp := a.DecodeLog(lending.LogRow{
		Address: market,
		Topic0:  a.topicRepay,
		Data:    dataWords(payer, borrower),
		Block:   11,
	})
	if len(exp) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(exp))
	}
	if exp[0].Account != lending.NormalizeAddr(borrower) {
		t.Errorf("account: got %s want borrower %s", exp[0].Account, borrower)
	}
	if exp[0].Side != lending.SideBorrow {
		t.Errorf("side: got %s want borrow", exp[0].Side)
	}
}

func TestDecodeMarketEntered(t *testing.T) {
	a := New("")
	cToken := "0x5c0401e81bc07ca70fad469b451682c0d747ef1c"
	account := "0x3333333333333333333333333333333333333333"

	// MarketEntered(cToken, account) is emitted by the Comptroller, so the asset is
	// the cToken from data word0, not the log address.
	exp := a.DecodeLog(lending.LogRow{
		Address: a.comptroller,
		Topic0:  a.topicMarketEntered,
		Data:    dataWords(cToken, account),
		Block:   12,
	})
	if len(exp) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(exp))
	}
	if exp[0].Account != lending.NormalizeAddr(account) {
		t.Errorf("account: got %s want %s", exp[0].Account, account)
	}
	if exp[0].Asset != lending.NormalizeAddr(cToken) {
		t.Errorf("asset: got %s want %s", exp[0].Asset, cToken)
	}
}

func TestToBps(t *testing.T) {
	// 0.5e18 close factor -> 5000 bps, 1.08e18 incentive -> 10800 bps.
	if got := toBps(mantissa(5, 10)); got != 5000 {
		t.Errorf("close factor: got %d want 5000", got)
	}
	if got := toBps(mantissa(108, 100)); got != 10800 {
		t.Errorf("incentive: got %d want 10800", got)
	}
}
