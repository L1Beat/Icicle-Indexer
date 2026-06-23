package stealtime

import (
	"math/big"
	"testing"
)

// buildLBQuoteReturn encodes a Quote struct return (offset word + 7 dynamic array
// members) with only the amounts array (member index 4) populated.
func buildLBQuoteReturn(amounts []int64) []byte {
	word := func(n int64) []byte {
		b := make([]byte, 32)
		big.NewInt(n).FillBytes(b)
		return b
	}
	empty := word(0) // array with length 0
	amountsArr := word(int64(len(amounts)))
	for _, a := range amounts {
		amountsArr = append(amountsArr, word(a)...)
	}
	tails := [][]byte{empty, empty, empty, empty, amountsArr, empty, empty}

	var head, tailBytes []byte
	off := 7 * 32
	for _, t := range tails {
		head = append(head, word(int64(off))...)
		tailBytes = append(tailBytes, t...)
		off += len(t)
	}
	structEnc := append(head, tailBytes...)
	return append(word(0x20), structEnc...) // leading offset to the struct
}

func TestDecodeLBAmountsLast(t *testing.T) {
	got := decodeLBAmountsLast(buildLBQuoteReturn([]int64{100, 250, 990}))
	if got == nil || got.Int64() != 990 {
		t.Fatalf("amounts last: got %v want 990", got)
	}

	// Empty amounts decodes to nil, not a panic or zero.
	if decodeLBAmountsLast(buildLBQuoteReturn(nil)) != nil {
		t.Fatalf("empty amounts should decode nil")
	}

	// Garbage / too-short input is rejected.
	if decodeLBAmountsLast([]byte{1, 2, 3}) != nil {
		t.Fatalf("short input should decode nil")
	}
}
