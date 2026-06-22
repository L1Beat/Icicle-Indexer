package stealtime

import "sort"

// CrossingResult is the reconstructed crossing block for one liquidation. When the
// position was already liquidatable at the lookback floor (the crossing is older
// than the cap), the result is right-censored and CrossingBlock is pinned to the
// floor, so steal_time = taken - floor >= cap.
type CrossingResult struct {
	CrossingBlock uint64
	Censored      bool
}

// LiquidatableAt reports whether the position was liquidatable at a block. It is
// the authoritative on-chain check (Aave healthFactor < 1e18, Benqi shortfall > 0)
// in production, and a stub in tests.
type LiquidatableAt func(block uint64) (bool, error)

// FindCrossing walks candidate blocks backward from takenBlock and returns the
// earliest block at which the position was liquidatable and stayed liquidatable
// through taken. Health only changes at candidate blocks (price updates and the
// account's own position changes), so checking candidates is sufficient and keeps
// archive calls bounded. floor is takenBlock minus the lookback cap.
func FindCrossing(candidates []uint64, takenBlock, floor uint64, liq LiquidatableAt) (CrossingResult, error) {
	// Candidates strictly below taken and at or above the floor, newest first.
	cand := make([]uint64, 0, len(candidates))
	seen := map[uint64]bool{}
	for _, b := range candidates {
		if b < takenBlock && b >= floor && !seen[b] {
			seen[b] = true
			cand = append(cand, b)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i] > cand[j] })

	crossing := takenBlock // default: no earlier evidence, crossed at taken (steal_time 0)
	for _, b := range cand {
		ok, err := liq(b)
		if err != nil {
			return CrossingResult{}, err
		}
		if ok {
			crossing = b
			continue
		}
		// First healthy block going back: the crossing is the next block forward,
		// which is the candidate we last marked liquidatable (already in crossing).
		return CrossingResult{CrossingBlock: crossing, Censored: false}, nil
	}

	// Every candidate down to the floor was liquidatable: the true crossing is at or
	// before the floor, so the observation is right-censored at the cap.
	if len(cand) > 0 {
		return CrossingResult{CrossingBlock: floor, Censored: true}, nil
	}
	return CrossingResult{CrossingBlock: crossing, Censored: false}, nil
}

// FindCrossingByScan locates the crossing without relying on oracle events (which
// on Avalanche are OCR NewTransmission, not the legacy AnswerUpdated). It steps
// backward from takenBlock by stride to bracket the boundary between a healthy and
// a liquidatable block, then binary-searches the bracket for the exact first
// liquidatable block. Block-precise and bounded to about lookback/stride coarse
// probes plus log2(stride) refinement probes. Right-censors when the position was
// already liquidatable at the floor.
func FindCrossingByScan(takenBlock, floor, stride uint64, liq LiquidatableAt) (CrossingResult, error) {
	if stride == 0 {
		stride = 600
	}
	if floor >= takenBlock {
		return CrossingResult{CrossingBlock: takenBlock, Censored: false}, nil
	}

	hi := takenBlock // known liquidatable: it was liquidated at takenBlock
	lo := uint64(0)
	b := takenBlock
	for {
		if b <= floor+stride {
			ok, err := liq(floor)
			if err != nil {
				return CrossingResult{}, err
			}
			if ok {
				return CrossingResult{CrossingBlock: floor, Censored: true}, nil
			}
			lo = floor
			break
		}
		b -= stride
		ok, err := liq(b)
		if err != nil {
			return CrossingResult{}, err
		}
		if !ok {
			lo = b
			break
		}
		hi = b
	}

	// lo is healthy, hi is liquidatable: binary-search the first liquidatable block.
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		ok, err := liq(mid)
		if err != nil {
			return CrossingResult{}, err
		}
		if ok {
			hi = mid
		} else {
			lo = mid
		}
	}
	return CrossingResult{CrossingBlock: hi, Censored: false}, nil
}

// StealTime returns taken - crossing in blocks.
func StealTime(takenBlock uint64, c CrossingResult) uint64 {
	if c.CrossingBlock > takenBlock {
		return 0
	}
	return takenBlock - c.CrossingBlock
}
