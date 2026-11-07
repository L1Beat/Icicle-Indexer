package aavev4

import (
	"math/big"
	"testing"

	"icicle/pkg/lending"
)

func word(n *big.Int) []byte {
	w := make([]byte, 32)
	n.FillBytes(w)
	return w
}

func words(vals ...string) []byte {
	var out []byte
	for _, v := range vals {
		n, ok := new(big.Int).SetString(v, 10)
		if !ok {
			panic(v)
		}
		out = append(out, word(n)...)
	}
	return out
}

func ok(b []byte) lending.CallResult { return lending.CallResult{Success: true, ReturnData: b} }

const (
	wavax = "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"
	usdc  = "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
	user  = "0x9674fc77a3f5773002877cae189a51ea89cc4316"
)

// TestDecodeHealth uses the values the Main spoke returned for 0x9674fc77 on
// 2026-11-07: 2,267.07 WAVAX supplied as collateral at $8.22618 and 12,883.67
// USDC borrowed at $0.99979, health factor 1.0569.
func TestDecodeHealth(t *testing.T) {
	reserves := []reserve{
		{id: big.NewInt(0), underlying: wavax, decimals: 18},
		{id: big.NewInt(2), underlying: usdc, decimals: 6},
	}
	results := []lending.CallResult{
		// riskPremium, avgCollateralFactor, healthFactor, totalCollateralValue, totalDebtValueRay, activeCollateralCount, borrowCount
		ok(words("0", "730000000000000000", "1056910094233198719", "1864935932077194588127179252000",
			"1288097481370037294295901660165837603504723035000000000000", "1", "1")),
		// WAVAX: supplied, total debt, status (collateral, borrowing), position (.., key 2), price
		ok(words("2267074073953640128086")), ok(words("0")), ok(words("1", "0")),
		ok(words("0", "0", "0", "2258568092812986878910", "2")), ok(words("822618000")),
		// USDC
		ok(words("0")), ok(words("12883665675")), ok(words("0", "1")),
		ok(words("12809120235", "0", "0", "0", "0")), ok(words("99979119")),
	}
	h := decodeHealth(lending.AaveV4Protocol("0x435272CefF93a1E657E8ABfdf0A13e95900A3a56"), user, reserves, results, 100)
	if !h.OK {
		t.Fatal("expected OK")
	}
	if got := h.Account.Protocol; got != "aave-v4:0x435272ceff93a1e657e8abfdf0a13e95900a3a56" {
		t.Fatalf("protocol %s", got)
	}
	if h.Liquidatable {
		t.Fatal("HF 1.0569 must not be liquidatable")
	}
	// $18,649.36 collateral and $12,880.97 debt on the 1e18 USD scale.
	assertUSD(t, "collateral_base", h.CollateralBase, 18649.36)
	assertUSD(t, "debt_base", h.DebtBase, 12880.97)
	if len(h.Assets) != 2 {
		t.Fatalf("want 2 legs, got %d", len(h.Assets))
	}
	c, d := h.Assets[0], h.Assets[1]
	if c.Side != lending.SideCollateral || c.Asset != wavax || c.NotEntered || c.ConfigKey != 2 {
		t.Fatalf("collateral leg %+v", c)
	}
	if d.Side != lending.SideDebt || d.Asset != usdc || d.Amount.String() != "12883665675" {
		t.Fatalf("debt leg %+v", d)
	}
	assertUSD(t, "collateral leg", c.BaseValue, 18649.36)
	assertUSD(t, "debt leg", d.BaseValue, 12880.98)
}

func TestDecodeHealthReverted(t *testing.T) {
	h := decodeHealth("aave-v4:0x1", user, nil, []lending.CallResult{{Success: false}}, 1)
	if h.OK {
		t.Fatal("a reverted summary must not be OK")
	}
}

func TestDecodeHealthLiquidatableAndNotEntered(t *testing.T) {
	reserves := []reserve{{id: big.NewInt(0), underlying: wavax, decimals: 18}}
	results := []lending.CallResult{
		ok(words("0", "0", "990000000000000000", "100000000000000000000000000", "50000000000000000000000000000000000000000000000000000", "0", "1")),
		ok(words("1000000000000000000")), ok(words("0")), ok(words("0", "0")),
		ok(words("0", "0", "0", "1", "7")), ok(words("800000000")),
	}
	h := decodeHealth("aave-v4:0x1", user, reserves, results, 1)
	if !h.Liquidatable {
		t.Fatal("HF 0.99 with debt must be liquidatable")
	}
	assertUSD(t, "collateral_base", h.CollateralBase, 1)
	assertUSD(t, "debt_base", h.DebtBase, 0.5)
	if len(h.Assets) != 1 || !h.Assets[0].NotEntered || h.Assets[0].ConfigKey != 7 {
		t.Fatalf("leg %+v", h.Assets)
	}
}

func TestDecodeLog(t *testing.T) {
	a := New("0x435272CefF93a1E657E8ABfdf0A13e95900A3a56")
	a.byID = map[string]reserve{"0": {id: big.NewInt(0), underlying: wavax}, "2": {id: big.NewInt(2), underlying: usdc}}
	topic := func(hexAddrOrID string) string {
		n, _ := new(big.Int).SetString(hexAddrOrID, 0)
		return "0x" + new(big.Int).Set(n).Text(16)
	}
	pad := func(s string) string { // 32-byte topic
		h := s[2:]
		for len(h) < 64 {
			h = "0" + h
		}
		return "0x" + h
	}
	caller := pad("0x1111111111111111111111111111111111111111")
	u := pad(user)

	got := a.DecodeLog(lending.LogRow{Topic0: a.topicBorrow, Topic1: pad(topic("2")), Topic2: caller, Topic3: u, Block: 5})
	if len(got) != 1 || got[0].Account != user || got[0].Asset != usdc || got[0].Side != lending.SideBorrow {
		t.Fatalf("borrow: %+v", got)
	}
	got = a.DecodeLog(lending.LogRow{Topic0: a.topicSetCollateral, Topic1: pad(topic("0")), Topic2: caller, Topic3: u})
	if len(got) != 1 || got[0].Asset != wavax || got[0].Side != lending.SideCollateral {
		t.Fatalf("set collateral: %+v", got)
	}
	got = a.DecodeLog(lending.LogRow{Topic0: a.topicLiquidation, Topic1: pad(topic("0")), Topic2: pad(topic("2")), Topic3: u})
	if len(got) != 2 || got[0].Asset != wavax || got[1].Asset != usdc || got[1].Side != lending.SideBorrow {
		t.Fatalf("liquidation: %+v", got)
	}
	got = a.DecodeLog(lending.LogRow{Topic0: a.topicDeficit, Topic1: pad(topic("2")), Topic2: u})
	if len(got) != 1 || got[0].Account != user || got[0].Asset != usdc {
		t.Fatalf("deficit: %+v", got)
	}
	// Unknown reserve id: the account is still tracked, with no asset.
	got = a.DecodeLog(lending.LogRow{Topic0: a.topicSupply, Topic1: pad(topic("9")), Topic2: caller, Topic3: u})
	if len(got) != 1 || got[0].Account != user || got[0].Asset != "" {
		t.Fatalf("unknown reserve: %+v", got)
	}
}

// TestEventTopics pins the event signatures to the topic0 hashes, so a typo in a
// signature cannot silently stop discovery. The LiquidationCall hash matches the
// one the liquidation ingester already decodes in production.
func TestEventTopics(t *testing.T) {
	a := New("0x0000000000000000000000000000000000000001")
	if a.topicLiquidation != lending.EventTopic("LiquidationCall(uint256,uint256,address,address,bool,uint256,uint256,(int256,int256,uint256),uint256,uint256,uint256)") {
		t.Fatal("liquidation topic")
	}
	seen := map[string]bool{}
	for _, tp := range a.Discovery().Topics {
		if seen[tp] {
			t.Fatalf("duplicate topic %s", tp)
		}
		seen[tp] = true
	}
	if len(seen) != 7 {
		t.Fatalf("want 7 topics, got %d", len(seen))
	}
}

func assertUSD(t *testing.T, name string, v *big.Int, want float64) {
	t.Helper()
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(v), big.NewFloat(1e18)).Float64()
	if diff := f - want; diff > 0.02 || diff < -0.02 {
		t.Fatalf("%s = $%.4f, want $%.2f", name, f, want)
	}
}
