package evmindexer

import (
	"encoding/hex"
	"strings"
	"testing"
)

// abiWord returns a 32-byte ABI word whose low 8 bytes hold v.
func abiWord(v uint64) string {
	w := make([]byte, 32)
	for i := 0; i < 8; i++ {
		w[31-i] = byte(v >> (8 * i))
	}
	return hex.EncodeToString(w)
}

// decodeString reads name()/symbol() output from any deployed contract, so a
// crafted length word must not panic the indexer. A length near MaxInt64
// makes 64+length overflow to a negative int, which skips the bounds check
// and slices data[64:negative].
func TestDecodeStringHostileLengthDoesNotPanic(t *testing.T) {
	cases := map[string]string{
		"length near MaxInt64": "0x" + abiWord(32) + abiWord(1<<63-10) + strings.Repeat("41", 32),
		"length MaxUint64":     "0x" + abiWord(32) + abiWord(1<<64-1) + strings.Repeat("41", 32),
		"length wider than 64 bits": "0x" + abiWord(32) +
			strings.Repeat("ff", 32) + strings.Repeat("41", 32),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("decodeString panicked on hostile ABI string: %v", r)
				}
			}()
			_, _ = decodeString(in)
		})
	}
}

// An oversized length is truncated to the bytes present, as before.
func TestDecodeStringTruncatesOversizedLength(t *testing.T) {
	in := "0x" + abiWord(32) + abiWord(1<<63-10) + hex.EncodeToString([]byte("AB"))
	got, err := decodeString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "AB" {
		t.Fatalf("got %q, want AB", got)
	}
}

func TestDecodeStringValid(t *testing.T) {
	in := "0x" + abiWord(32) + abiWord(4) + hex.EncodeToString([]byte("USDC")) + strings.Repeat("00", 28)
	got, err := decodeString(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "USDC" {
		t.Fatalf("got %q, want USDC", got)
	}
}
