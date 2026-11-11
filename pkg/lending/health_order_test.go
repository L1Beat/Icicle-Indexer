package lending

import (
	"math/big"
	"testing"
	"time"
)

func testEngine() *HealthEngine {
	return &HealthEngine{
		cfg:   DefaultHealthConfig(),
		state: map[Protocol]map[string]*cacheState{ProtocolAaveV3: {}},
	}
}

func health(acc string, liquidatable bool, hf int64) Health {
	return Health{
		Account:      Account{Protocol: ProtocolAaveV3, Address: acc},
		OK:           true,
		Liquidatable: liquidatable,
		HealthFactor: new(big.Int).Mul(big.NewInt(hf), big.NewInt(1e16)), // hf in hundredths
		DebtBase:     big.NewInt(1),
	}
}

// A cold sweep reads an account, a price recompute then reads and writes it,
// and only afterwards does the sweep reach its write. The sweep's older read
// must not replace the newer one or emit a spurious "recovered" alert.
func TestApply_OlderReadDoesNotOverwriteNewer(t *testing.T) {
	e := testEngine()
	acc := "0x1111111111111111111111111111111111111111"
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	sweepSeq := e.beginRead() // sweep starts reading first
	priceSeq := e.beginRead() // price recompute starts after
	kept, _, alerts, priceStamp := e.apply(ProtocolAaveV3, []Health{health(acc, true, 95)}, priceSeq, now)
	if len(kept) != 1 || len(alerts) != 1 || alerts[0].Kind != "liquidatable" {
		t.Fatalf("price recompute: kept=%d alerts=%+v", len(kept), alerts)
	}

	kept, _, alerts, _ = e.apply(ProtocolAaveV3, []Health{health(acc, false, 120)}, sweepSeq, now)
	if len(kept) != 0 || len(alerts) != 0 {
		t.Fatalf("stale sweep read was accepted: kept=%d alerts=%+v", len(kept), alerts)
	}
	if st := e.state[ProtocolAaveV3][acc]; !st.liquidatable || st.readSeq != priceSeq {
		t.Fatalf("cache overwritten by the older read: %+v", st)
	}

	// A later read is accepted and gets a strictly newer version even when the
	// wall clock has not moved, so it wins the argMax(updated_at) readers.
	nextSeq := e.beginRead()
	kept, _, _, stamp := e.apply(ProtocolAaveV3, []Health{health(acc, false, 130)}, nextSeq, now)
	if len(kept) != 1 || !stamp.After(priceStamp) {
		t.Fatalf("newer read: kept=%d stamp=%v prev=%v", len(kept), stamp, priceStamp)
	}
}

// Other accounts in the stale batch are still written.
func TestApply_StaleFilterIsPerAccount(t *testing.T) {
	e := testEngine()
	a, b := "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"
	sweepSeq := e.beginRead()
	priceSeq := e.beginRead()
	e.apply(ProtocolAaveV3, []Health{health(a, true, 95)}, priceSeq, time.Now())
	kept, _, _, _ := e.apply(ProtocolAaveV3, []Health{health(a, false, 120), health(b, false, 300)}, sweepSeq, time.Now())
	if len(kept) != 1 || kept[0].Account.Address != b {
		t.Fatalf("want only account b from the sweep, got %+v", kept)
	}
}

// A per-asset read that keeps failing is carried only for MaxLegCarry, then the
// leg is let go; a successful read resets the clock.
func TestApply_BoundsLegCarry(t *testing.T) {
	e := testEngine()
	acc, asset := "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	failing := func() Health { h := health(acc, false, 200); h.KeepLegs = []string{asset}; return h }

	kept, _, _, _ := e.apply(ProtocolAaveV3, []Health{failing()}, e.beginRead(), t0)
	if len(kept[0].KeepLegs) != 1 {
		t.Fatal("first failure should be carried")
	}
	kept, _, _, _ = e.apply(ProtocolAaveV3, []Health{failing()}, e.beginRead(), t0.Add(59*time.Minute))
	if len(kept[0].KeepLegs) != 1 {
		t.Fatal("still inside MaxLegCarry, should be carried")
	}
	kept, _, _, _ = e.apply(ProtocolAaveV3, []Health{failing()}, e.beginRead(), t0.Add(61*time.Minute))
	if len(kept[0].KeepLegs) != 0 {
		t.Fatal("failing past MaxLegCarry, should no longer be carried")
	}

	// A good read clears the history; the next failure starts a fresh window.
	e.apply(ProtocolAaveV3, []Health{health(acc, false, 200)}, e.beginRead(), t0.Add(62*time.Minute))
	kept, _, _, _ = e.apply(ProtocolAaveV3, []Health{failing()}, e.beginRead(), t0.Add(63*time.Minute))
	if len(kept[0].KeepLegs) != 1 {
		t.Fatal("after a successful read the carry window should restart")
	}
}
