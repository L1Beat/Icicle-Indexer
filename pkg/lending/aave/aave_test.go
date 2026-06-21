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
