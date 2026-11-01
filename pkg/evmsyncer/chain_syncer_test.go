package evmsyncer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"icicle/pkg/evmrpc"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// failingConn stands in for a wedged ClickHouse: every batch insert fails, so
// the writer's flush never succeeds and its retry buffer is never cleared. The
// embedded nil interface is deliberate -- writeBlocks only reaches PrepareBatch,
// and any other call should fail loudly rather than silently pass.
type failingConn struct{ driver.Conn }

func (failingConn) PrepareBatch(context.Context, string, ...driver.PrepareBatchOption) (driver.Batch, error) {
	return nil, errors.New("simulated clickhouse failure")
}

func blockBatch(startNum uint32, n int) []*evmrpc.NormalizedBlock {
	batch := make([]*evmrpc.NormalizedBlock, n)
	for i := range batch {
		b := &evmrpc.NormalizedBlock{}
		// Above the syncer's zero-valued maxBlock* watermarks so the blocks
		// survive InsertBlocks' filter and reach PrepareBatch.
		b.Block.Number = fmt.Sprintf("0x%x", startNum+uint32(i)+1)
		batch[i] = b
	}
	return batch
}

// TestWriterLoopAppliesBackpressureWhenWritesFail is a regression test for the
// 2026-08-05 stall: while every flush failed, the writer kept draining blockChan
// into its retry buffer, so the fetcher never blocked and memory grew until the
// process held 138k blocks / 66 GB. The writer must stop receiving once the
// buffer hits maxPending, leaving blocks in the channel so the fetcher blocks.
func TestWriterLoopAppliesBackpressureWhenWritesFail(t *testing.T) {
	const (
		batchSize  = 5
		maxPending = 10
		chanCap    = 8
		sent       = 6
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &ChainSyncer{
		chainId:       43114,
		conn:          failingConn{},
		blockChan:     make(chan []*evmrpc.NormalizedBlock, chanCap),
		maxPending:    maxPending,
		flushInterval: 5 * time.Millisecond,
		ctx:           ctx,
		cancel:        cancel,
		lastPrintTime: time.Now(),
		startTime:     time.Now(),
	}

	for i := 0; i < sent; i++ {
		select {
		case cs.blockChan <- blockBatch(uint32(i*batchSize), batchSize):
		default:
			t.Fatalf("channel unexpectedly full while seeding at batch %d", i)
		}
	}

	cs.wg.Add(1)
	go cs.writerLoop()

	// The writer should take exactly enough batches to reach maxPending
	// (2 batches x 5 blocks) and then stop, leaving the rest queued.
	wantRemaining := sent - (maxPending / batchSize)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(cs.blockChan) == wantRemaining {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer drained channel to %d, want it parked at %d remaining; "+
				"backpressure is not engaging and the retry buffer is unbounded",
				len(cs.blockChan), wantRemaining)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// It must stay parked: repeated failing flushes must not resume draining.
	time.Sleep(100 * time.Millisecond)
	if got := len(cs.blockChan); got != wantRemaining {
		t.Fatalf("writer resumed draining while flushes still fail: %d remaining, want %d", got, wantRemaining)
	}

	// And it must shut down cleanly from the parked state. Stop() no longer
	// closes blockChan, so this must not panic or hang.
	done := make(chan struct{})
	go func() {
		cancel()
		cs.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writerLoop did not exit after cancel; shutdown deadlocked")
	}
}

// TestStopDoesNotPanicWithBlockedFetcher covers the close-on-blocked-sender race
// that removing close(blockChan) fixes. A sender parked on a full channel must
// survive shutdown; closing under it would let select pick the send and panic.
func TestStopDoesNotPanicWithBlockedFetcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	cs := &ChainSyncer{
		chainId:       43114,
		blockChan:     make(chan []*evmrpc.NormalizedBlock, 1),
		maxPending:    1,
		flushInterval: time.Millisecond,
		ctx:           ctx,
		cancel:        cancel,
		lastPrintTime: time.Now(),
		startTime:     time.Now(),
	}

	cs.blockChan <- blockBatch(0, 1) // fill it

	var wg sync.WaitGroup
	wg.Add(1)
	sendReturned := make(chan struct{})
	go func() {
		defer wg.Done()
		// Mirrors the fetcher's send in fetchLoop.
		select {
		case cs.blockChan <- blockBatch(1, 1):
		case <-cs.ctx.Done():
		}
		close(sendReturned)
	}()

	time.Sleep(20 * time.Millisecond) // let the sender park
	cs.Stop()                         // cancel only; must not close the channel

	select {
	case <-sendReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked sender never returned after Stop")
	}
	wg.Wait()
}
