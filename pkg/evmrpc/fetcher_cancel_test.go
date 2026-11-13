package evmrpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// failingTraceServer rejects every request, so tracing falls back to
// per-transaction retries, the path that used to ignore shutdown.
func failingTraceServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testTxInfos() []txInfo {
	return []txInfo{
		{hash: "0xa", blockNum: 1, txIdx: 0},
		{hash: "0xb", blockNum: 1, txIdx: 1},
		{hash: "0xc", blockNum: 1, txIdx: 2},
	}
}

func TestTraceRetriesStopOnCancel(t *testing.T) {
	srv := failingTraceServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	f := NewFetcher(FetcherOptions{
		RpcURL:         srv.URL,
		MaxConcurrency: 4,
		DebugBatchSize: 1,
		MaxRetries:     20, // as the backfill runs: minutes of retries if cancel is ignored
		RetryDelay:     200 * time.Millisecond,
		Ctx:            ctx,
	})
	defer f.Close()

	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := f.fetchTracesBatch(1, 1, testTxInfos())
	took := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if took > 2*time.Second {
		t.Fatalf("took %v to stop after cancel, want under 2s", took)
	}
}

func TestRequestAbortsWhenCancelled(t *testing.T) {
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // a node that never answers
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) }) // runs first, so Close does not wait on the handler
	ctx, cancel := context.WithCancel(context.Background())
	f := NewFetcher(FetcherOptions{RpcURL: srv.URL, MaxRetries: 20, Ctx: ctx})
	defer f.Close()

	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := f.batchRpcCallDebug([]jsonRpcRequest{{Jsonrpc: "2.0", Method: "debug_traceBlockByNumber", ID: 0}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("took %v to abort a hanging request, want under 2s", took)
	}
}

// Without a context the fetcher behaves as before: block traces come back.
func TestTraceWithoutCtxUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqs []jsonRpcRequest
		if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		var resps []map[string]any
		for _, rq := range reqs {
			var traces []map[string]any
			for _, h := range []string{"0xa", "0xb", "0xc"} {
				traces = append(traces, map[string]any{"txHash": h, "result": map[string]any{
					"from": "0x1", "gas": "0x0", "gasUsed": "0x0", "to": "0x2", "input": "0x", "type": "CALL"}})
			}
			resps = append(resps, map[string]any{"jsonrpc": "2.0", "id": rq.ID, "result": traces})
		}
		_ = json.NewEncoder(w).Encode(resps)
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(FetcherOptions{RpcURL: srv.URL})
	defer f.Close()

	got, err := f.fetchTracesBatch(1, 1, testTxInfos())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"0xa", "0xb", "0xc"} {
		if got[h] == nil || got[h].Result == nil || got[h].Result.Type != "CALL" {
			t.Fatalf("missing trace for %s: %+v", h, got[h])
		}
	}
}
