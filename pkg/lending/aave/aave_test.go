package aave

import (
	"strings"
	"testing"

	"icicle/pkg/lending"
)

func topicFromAddr(addr string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(addr), "0x")
}

func TestDecodeBorrow(t *testing.T) {
	a := New("")
	reserve := "0x1111111111111111111111111111111111111111"
	user := "0x2222222222222222222222222222222222222222"

	exp := a.DecodeLog(lending.LogRow{
		Topic0: a.topicBorrow,
		Topic1: topicFromAddr(reserve),
		Topic2: topicFromAddr(user),
		Block:  100,
	})
	if len(exp) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(exp))
	}
	if exp[0].Account != lending.NormalizeAddr(user) {
		t.Errorf("account: got %s want %s", exp[0].Account, user)
	}
	if exp[0].Asset != lending.NormalizeAddr(reserve) {
		t.Errorf("asset: got %s want %s", exp[0].Asset, reserve)
	}
	if exp[0].Side != lending.SideBorrow {
		t.Errorf("side: got %s want borrow", exp[0].Side)
	}
}

func TestDecodeLiquidationCall(t *testing.T) {
	a := New("")
	collateral := "0x1111111111111111111111111111111111111111"
	debt := "0x2222222222222222222222222222222222222222"
	user := "0x3333333333333333333333333333333333333333"

	exp := a.DecodeLog(lending.LogRow{
		Topic0: a.topicLiquidation,
		Topic1: topicFromAddr(collateral),
		Topic2: topicFromAddr(debt),
		Topic3: topicFromAddr(user),
		Block:  200,
	})
	if len(exp) != 2 {
		t.Fatalf("expected 2 exposures, got %d", len(exp))
	}
	for _, e := range exp {
		if e.Account != lending.NormalizeAddr(user) {
			t.Errorf("account: got %s want %s", e.Account, user)
		}
	}
}

func TestDecodeReserveTokens(t *testing.T) {
	// One TokenData { string "USDC", address 0xabc... }. Built by hand to exercise
	// the dynamic tuple-array decoder used by RefreshParams.
	addr := "0x00000000000000000000000000000000000000ab"
	tokens := decodeReserveTokens(buildTokenDataArray(t, "USDC", addr))
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if tokens[0].symbol != "USDC" {
		t.Errorf("symbol: got %q want USDC", tokens[0].symbol)
	}
	if tokens[0].address != lending.NormalizeAddr(addr) {
		t.Errorf("address: got %s want %s", tokens[0].address, addr)
	}
}

// buildTokenDataArray encodes TokenData[]{ {symbol, address} } with one element.
func buildTokenDataArray(t *testing.T, symbol, addr string) []byte {
	t.Helper()
	word := func(n int) []byte {
		b := make([]byte, 32)
		b[31] = byte(n)
		return b
	}
	addrWord := func(a string) []byte {
		b := make([]byte, 32)
		copy(b[12:], lending.DecodeHexBytes(a))
		return b
	}
	strBytes := func(s string) []byte {
		b := append(word(len(s)), []byte(s)...)
		if r := len(b) % 32; r != 0 {
			b = append(b, make([]byte, 32-r)...)
		}
		return b
	}

	// tuple: word0 = offset to symbol string (0x40), word1 = address, then string.
	tuple := append(word(0x40), addrWord(addr)...)
	tuple = append(tuple, strBytes(symbol)...)

	// array: count=1, one head offset (0x20), then the tuple.
	arr := append(word(1), word(0x20)...)
	arr = append(arr, tuple...)

	// outer: offset 0x20 to the array.
	return append(word(0x20), arr...)
}

func word(n int64) []byte {
	b := make([]byte, 32)
	for i := 31; i >= 0 && n > 0; i-- {
		b[i] = byte(n & 0xff)
		n >>= 8
	}
	return b
}

// The E-Mode call goes last, so per-asset pairs keep their positions, and its
// result lands in Health.EModeCategory.
func TestBuildProbe_EModeCategory(t *testing.T) {
	a := New("")
	pool := "0x794a61358d6845594f94dc1db02a252b5b4814ad"
	asset := "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"
	a.Configure(lending.Addresses{Pool: pool, Oracle: "0x00000000000000000000000000000000000000aa", DataProvider: "0x00000000000000000000000000000000000000bb"},
		[]lending.AssetParam{{Asset: asset, Decimals: 18}}, lending.GlobalParams{})
	acct := "0x2222222222222222222222222222222222222222"

	p := a.BuildProbe(acct, []lending.Exposure{{Account: acct, Asset: asset, Side: lending.SideCollateral}})
	if len(p.Calls) != 4 {
		t.Fatalf("calls: got %d want 4 (summary, reserve, price, emode)", len(p.Calls))
	}
	if p.Calls[3].Target != pool || !strings.HasPrefix(p.Calls[3].Data, "0xeddf1b79") {
		t.Fatalf("last call should be getUserEMode on the pool, got %s %s", p.Calls[3].Target, p.Calls[3].Data[:10])
	}

	summary := append(append(append(append(append(word(100), word(50)...), word(0)...), word(0)...), word(0)...), word(2e18)...)
	reserve := append(append(word(7e18), word(0)...), word(0)...)
	h := p.Decode([]lending.CallResult{
		{Success: true, ReturnData: summary},
		{Success: true, ReturnData: reserve},
		{Success: true, ReturnData: word(9e8)},
		{Success: true, ReturnData: word(2)},
	}, 123)
	if !h.OK || h.EModeCategory != 2 {
		t.Fatalf("emode: got ok=%v category=%d", h.OK, h.EModeCategory)
	}
	if len(h.Assets) != 1 || h.Assets[0].Amount.Int64() != 7e18 {
		t.Fatalf("asset leg misread: %+v", h.Assets)
	}

	// Summary-only probe (cold sweep): the E-Mode call is the second call.
	p = a.BuildProbe(acct, nil)
	h = p.Decode([]lending.CallResult{{Success: true, ReturnData: summary}, {Success: false}}, 1)
	if len(p.Calls) != 2 || h.EModeCategory != 0 || !h.OK {
		t.Fatalf("summary-only: calls=%d category=%d ok=%v", len(p.Calls), h.EModeCategory, h.OK)
	}
}
