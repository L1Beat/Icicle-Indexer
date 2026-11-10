package bridges

import (
	"context"
	"testing"
)

// fixedCaller returns the same eth_call result for every call.
type fixedCaller []byte

func (f fixedCaller) Call(context.Context, string, []byte) ([]byte, error) { return f, nil }

func word(v uint64) []byte {
	w := make([]byte, 32)
	for i := 0; i < 8; i++ {
		w[31-i] = byte(v >> (8 * i))
	}
	return w
}

// callString decodes symbol() from contracts that emit Avalanche Bridge
// topics, which any address can do. A length of 2^64-1 makes
// o+32+n wrap to a small uint64 that passes the bounds check, then
// int(n) is -1 and the slice b[o+32:o+31] panics.
func TestCallStringHostileLengthDoesNotPanic(t *testing.T) {
	cases := map[string][]byte{
		"length MaxUint64":    append(word(32), word(1<<64-1)...),
		"length wraps to fit": append(append(word(32), word(1<<64-32)...), make([]byte, 32)...),
		"offset MaxUint64":    append(word(1<<64-1), make([]byte, 32)...),
	}
	for name, ret := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("callString panicked on hostile return data: %v", r)
				}
			}()
			_, _ = callString(context.Background(), fixedCaller(ret), "00", "symbol()")
		})
	}
}

func TestCallStringValid(t *testing.T) {
	ret := append(append(word(32), word(5)...), []byte("BTC.b")...)
	ret = append(ret, make([]byte, 27)...)
	got, err := callString(context.Background(), fixedCaller(ret), "00", "symbol()")
	if err != nil {
		t.Fatal(err)
	}
	if got != "BTC.b" {
		t.Fatalf("got %q, want BTC.b", got)
	}
}
