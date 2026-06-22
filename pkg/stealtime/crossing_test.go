package stealtime

import "testing"

func TestFindCrossingHealthyThenCrossingThenTaken(t *testing.T) {
	taken := uint64(1000)
	floor := uint64(900)
	// Liquidatable at 999 and 998, healthy at 997. Crossing is 998.
	liqSet := map[uint64]bool{999: true, 998: true, 997: false, 950: false}
	liq := func(b uint64) (bool, error) { return liqSet[b], nil }

	got, err := FindCrossing([]uint64{999, 998, 997, 950}, taken, floor, liq)
	if err != nil {
		t.Fatalf("FindCrossing: %v", err)
	}
	if got.Censored {
		t.Fatalf("should not be censored")
	}
	if got.CrossingBlock != 998 {
		t.Fatalf("crossing: got %d want 998", got.CrossingBlock)
	}
	if s := StealTime(taken, got); s != 2 {
		t.Fatalf("steal_time: got %d want 2", s)
	}
}

func TestFindCrossingCensored(t *testing.T) {
	taken := uint64(1000)
	floor := uint64(900)
	// Every candidate down to the floor is still liquidatable: crossing is older
	// than the cap, so the observation is right-censored at the floor.
	liq := func(uint64) (bool, error) { return true, nil }

	got, err := FindCrossing([]uint64{999, 950, 900}, taken, floor, liq)
	if err != nil {
		t.Fatalf("FindCrossing: %v", err)
	}
	if !got.Censored {
		t.Fatalf("expected censored")
	}
	if got.CrossingBlock != floor {
		t.Fatalf("crossing: got %d want floor %d", got.CrossingBlock, floor)
	}
	if s := StealTime(taken, got); s != 100 {
		t.Fatalf("steal_time: got %d want 100 (the cap)", s)
	}
}

func TestFindCrossingNoCandidates(t *testing.T) {
	taken := uint64(1000)
	got, err := FindCrossing(nil, taken, 900, func(uint64) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("FindCrossing: %v", err)
	}
	if got.Censored || got.CrossingBlock != taken {
		t.Fatalf("no candidates should pin crossing to taken, got %+v", got)
	}
	if s := StealTime(taken, got); s != 0 {
		t.Fatalf("steal_time: got %d want 0", s)
	}
}
