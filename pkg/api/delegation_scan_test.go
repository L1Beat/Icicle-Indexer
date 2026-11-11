package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Distinct keys each miss the cache; the gate must cap how many loads run at
// once, and a request that cannot get a slot must fail fast as busy.
func TestScanSlotBoundsConcurrentLoads(t *testing.T) {
	var mu sync.Mutex
	running, peak := 0, 0
	release := make(chan struct{})
	load := scanSlot(func(ctx context.Context) (interface{}, error) {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return nil, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 3*cap(delegationScans); i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = load(context.Background()) }()
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if peak > cap(delegationScans) {
		t.Fatalf("%d loads ran at once, cap is %d", peak, cap(delegationScans))
	}
	mu.Unlock()
	close(release)
	wg.Wait()
}

func TestScanSlotGivesUpWhenFull(t *testing.T) {
	for i := 0; i < cap(delegationScans); i++ {
		delegationScans <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(delegationScans); i++ {
			<-delegationScans
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := scanSlot(func(context.Context) (interface{}, error) { return nil, nil })(ctx)
	if !errors.Is(err, errDelegationsBusy) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want busy or deadline", err)
	}

	rec := httptest.NewRecorder()
	writeDelegationError(rec, errDelegationsBusy)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("busy gate wrote %d, want 503", rec.Code)
	}
}
