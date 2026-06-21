package lending

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// parseAggregate3Input decodes the calldata produced by EncodeAggregate3 back
// into its tuples, so the encoder can be verified without a chain.
func parseAggregate3Input(t *testing.T, calldata string) []Call {
	t.Helper()
	raw := decodeHex(calldata)
	if len(raw) < 4 {
		t.Fatal("calldata too short")
	}
	b := raw[4:] // strip selector

	arrBase := int(new(big.Int).SetBytes(b[0:32]).Uint64())
	n := int(new(big.Int).SetBytes(b[arrBase : arrBase+32]).Uint64())
	headBase := arrBase + 32

	var out []Call
	for i := 0; i < n; i++ {
		elemOff := int(new(big.Int).SetBytes(b[headBase+i*32 : headBase+i*32+32]).Uint64())
		elemStart := headBase + elemOff
		target := "0x" + hex.EncodeToString(b[elemStart+12:elemStart+32])
		allow := new(big.Int).SetBytes(b[elemStart+32 : elemStart+64]).Sign() != 0
		bytesOff := int(new(big.Int).SetBytes(b[elemStart+64 : elemStart+96]).Uint64())
		bytesStart := elemStart + bytesOff
		blen := int(new(big.Int).SetBytes(b[bytesStart : bytesStart+32]).Uint64())
		data := b[bytesStart+32 : bytesStart+32+blen]
		out = append(out, Call{Target: target, AllowFailure: allow, Data: "0x" + hex.EncodeToString(data)})
	}
	return out
}

func TestEncodeAggregate3RoundTrip(t *testing.T) {
	calls := []Call{
		{Target: "0x794a61358d6845594f94dc1db02a252b5b4814ad", AllowFailure: true, Data: EncodeCall1Addr("getUserAccountData(address)", "0x00000000000000000000000000000000000000aa")},
		{Target: "0xca11bde05977b3631167028862be2a173976ca11", AllowFailure: false, Data: "0x"},
		{Target: "0x486af39519b4dc9a7fccd318217352830e8ad9b4", AllowFailure: true, Data: EncodeCall2Addr("getUserReserveData(address,address)", "0x00000000000000000000000000000000000000bb", "0x00000000000000000000000000000000000000cc")},
	}

	encoded := EncodeAggregate3(calls)
	if !strings.HasPrefix(encoded, aggregate3Selector) {
		t.Fatalf("missing aggregate3 selector")
	}

	got := parseAggregate3Input(t, encoded)
	if len(got) != len(calls) {
		t.Fatalf("round-trip count: got %d want %d", len(got), len(calls))
	}
	for i := range calls {
		if !strings.EqualFold(got[i].Target, calls[i].Target) {
			t.Errorf("call %d target: got %s want %s", i, got[i].Target, calls[i].Target)
		}
		if got[i].AllowFailure != calls[i].AllowFailure {
			t.Errorf("call %d allowFailure: got %v want %v", i, got[i].AllowFailure, calls[i].AllowFailure)
		}
		wantData := strings.ToLower(ensure0x(calls[i].Data))
		if calls[i].Data == "0x" {
			wantData = "0x"
		}
		if !strings.EqualFold(got[i].Data, wantData) {
			t.Errorf("call %d data: got %s want %s", i, got[i].Data, wantData)
		}
	}
}

// buildAggregate3Return constructs a valid aggregate3 return buffer for the given
// (success, data) results, mirroring how Multicall3 encodes Result[].
func buildAggregate3Return(results []CallResult) []byte {
	// Each element tail: success(32) + bytesOffset(0x40)(32) + len(32) + padded data.
	elems := make([][]byte, len(results))
	for i, r := range results {
		var e []byte
		e = append(e, boolWord(r.Success)...)
		e = append(e, u256Word(big.NewInt(0x40))...)
		e = append(e, u256Word(big.NewInt(int64(len(r.ReturnData))))...)
		e = append(e, rightPad32(append([]byte(nil), r.ReturnData...))...)
		elems[i] = e
	}
	n := len(results)
	headLen := 32 * n
	var heads, tails []byte
	off := headLen
	for _, e := range elems {
		heads = append(heads, u256Word(big.NewInt(int64(off)))...)
		tails = append(tails, e...)
		off += len(e)
	}
	var arr []byte
	arr = append(arr, u256Word(big.NewInt(int64(n)))...)
	arr = append(arr, heads...)
	arr = append(arr, tails...)
	var out []byte
	out = append(out, u256Word(big.NewInt(0x20))...)
	out = append(out, arr...)
	return out
}

func TestDecodeAggregate3(t *testing.T) {
	want := []CallResult{
		{Success: true, ReturnData: u256Word(big.NewInt(123456))},
		{Success: false, ReturnData: nil},
		{Success: true, ReturnData: append(u256Word(big.NewInt(7)), u256Word(big.NewInt(8))...)},
	}
	buf := buildAggregate3Return(want)

	got, err := DecodeAggregate3(buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("count: got %d want %d", len(got), len(want))
	}
	if !got[0].Success || Word(got[0].ReturnData, 0).Int64() != 123456 {
		t.Errorf("result 0 wrong: %+v", got[0])
	}
	if got[1].Success || len(got[1].ReturnData) != 0 {
		t.Errorf("result 1 should be failed/empty: %+v", got[1])
	}
	if !got[2].Success || Word(got[2].ReturnData, 1).Int64() != 8 {
		t.Errorf("result 2 wrong: %+v", got[2])
	}
}
