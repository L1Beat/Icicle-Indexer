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

func word(n *big.Int) []byte {
	w := make([]byte, 32)
	n.FillBytes(w)
	return w
}

func usd(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), lending.WAD) }

func words(ns ...*big.Int) []byte {
	var out []byte
	for _, n := range ns {
		out = append(out, word(n)...)
	}
	return out
}

// addrArray ABI-encodes an address[] return value.
func addrArray(addrs ...string) []byte {
	out := words(big.NewInt(32), big.NewInt(int64(len(addrs))))
	return append(out, dataWords(addrs...)...)
}

// Modeled on a live mismatch (0x3b0f...dbcf): $5,000 of entered qiAVAX at a 0.74
// collateral factor, $10,783 of sAVAX supplied but never entered, $2,130 of USDC
// debt. Chain HF = 3,700 / 2,130 = 1.737. Counting the non-entered sAVAX gave 5.28.
func TestBuildProbe_OnlyEnteredMarketsAreCollateral(t *testing.T) {
	const (
		account = "0x3b0fd40dd4a6360bdb52f40f8e4c044cd6afdbcf"
		qiAVAX  = "0x5c0401e81bc07ca70fad469b451682c0d747ef1c"
		qiSAVAX = "0xf362fea9659cf036792c9cb02f8ff8198e21b4cb"
		qiUSDC  = "0xb715808a78f6041e46d61cb123c9b4a27056ae9c"
	)
	a := New("")
	a.addrs.Markets = []string{qiAVAX, qiSAVAX, qiUSDC}
	a.params[qiAVAX] = lending.AssetParam{LiquidationThresholdBps: 7400}
	a.params[qiSAVAX] = lending.AssetParam{LiquidationThresholdBps: 5000}
	a.params[qiUSDC] = lending.AssetParam{LiquidationThresholdBps: 8000}

	one := lending.WAD // price 1e18 and exchange rate 1e18: 1 token = $1
	zero := big.NewInt(0)
	snap := func(supply, borrow *big.Int) lending.CallResult {
		return lending.CallResult{Success: true, ReturnData: words(zero, supply, borrow, one)}
	}
	price := lending.CallResult{Success: true, ReturnData: words(one)}
	// Comptroller: liquidity = 5,000 * 0.74 - 2,130 = 1,570.
	liquidity := lending.CallResult{Success: true, ReturnData: words(zero, usd(1570), zero)}
	assetsIn := lending.CallResult{Success: true, ReturnData: addrArray(qiAVAX, qiUSDC)}

	results := []lending.CallResult{
		liquidity, assetsIn,
		snap(usd(5000), zero), price,
		snap(usd(10783), zero), price,
		snap(zero, usd(2130)), price,
	}

	probe := a.BuildProbe(account, nil)
	if len(probe.Calls) != len(results) {
		t.Fatalf("probe has %d calls, test supplies %d results", len(probe.Calls), len(results))
	}
	h := probe.Decode(results, 1)

	wantHF := new(big.Int).Div(new(big.Int).Mul(usd(3700), lending.WAD), usd(2130))
	if h.HealthFactor.Cmp(wantHF) != 0 {
		t.Errorf("HF = %s, want %s (1.737)", h.HealthFactor, wantHF)
	}
	if h.CollateralBase.Cmp(usd(5000)) != 0 {
		t.Errorf("collateral = %s, want 5000e18 (non-entered sAVAX excluded from collateral_base)", h.CollateralBase)
	}
	if h.DebtBase.Cmp(usd(2130)) != 0 {
		t.Errorf("debt = %s, want 2130e18", h.DebtBase)
	}
	// Non-entered supply stays as a collateral leg, flagged: it backs no borrow
	// but Benqi's seizeAllowed does not check membership, so it is seizable.
	notEntered := map[string]bool{}
	for _, leg := range h.Assets {
		if leg.Side == lending.SideCollateral {
			notEntered[leg.Asset] = leg.NotEntered
		}
	}
	if ne, ok := notEntered[qiSAVAX]; !ok || !ne {
		t.Errorf("sAVAX leg: present=%v NotEntered=%v, want present and NotEntered", ok, ne)
	}
	if ne, ok := notEntered[qiAVAX]; !ok || ne {
		t.Errorf("AVAX leg: present=%v NotEntered=%v, want present and entered", ok, ne)
	}

	// getAssetsIn reverts: collateral falls back to every market, but the health
	// factor still comes from the Comptroller and stays correct.
	results[1] = lending.CallResult{Success: false}
	h = probe.Decode(results, 1)
	if h.HealthFactor.Cmp(wantHF) != 0 {
		t.Errorf("fallback HF = %s, want %s", h.HealthFactor, wantHF)
	}
	if h.CollateralBase.Cmp(usd(15783)) != 0 {
		t.Errorf("fallback collateral = %s, want 15783e18", h.CollateralBase)
	}
}
