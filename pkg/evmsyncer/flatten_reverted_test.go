package evmsyncer

import (
	"testing"
	"time"

	"icicle/pkg/evmrpc"
)

// Shape of the WAVAX case: frame 0 reverts, its grandchild moves value with no
// error of its own. The grandchild must be marked reverted; the sibling must not.
func TestFlattenTrace_RevertedIsInherited(t *testing.T) {
	addr := func(b byte) string {
		s := "0x"
		for i := 0; i < 20; i++ {
			s += "0" + string("0123456789abcdef"[b&0xf])
		}
		return s
	}
	root := &evmrpc.CallTrace{Type: "CALL", From: addr(1), To: addr(2), Calls: []evmrpc.CallTrace{
		{Type: "CALL", From: addr(2), To: addr(3), Error: "execution reverted", Calls: []evmrpc.CallTrace{
			{Type: "CALL", From: addr(3), To: addr(4), Calls: []evmrpc.CallTrace{
				{Type: "CALL", From: addr(4), To: addr(5), Value: "0xa3c1e8c2b9b1a3f3c000"},
			}},
		}},
		{Type: "CALL", From: addr(2), To: addr(6), Value: "0x1"},
	}}

	rows, err := flattenTrace(root, "0x"+repeat("ab", 32), 1, time.Unix(0, 0), 0, []uint16{}, true, make([]byte, 20), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"": false, "0": true, "0.0": true, "0.0.0": true, "1": false}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		p := ""
		for i, a := range r.TraceAddress {
			if i > 0 {
				p += "."
			}
			p += string(rune('0' + a))
		}
		if r.Reverted != want[p] {
			t.Errorf("trace %q: reverted=%v, want %v", p, r.Reverted, want[p])
		}
		if !r.TxSuccess {
			t.Errorf("trace %q: tx_success should stay true", p)
		}
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
