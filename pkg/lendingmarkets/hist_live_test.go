package lendingmarkets

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"icicle/pkg/restaking"
)

func TestReadAll_HistoricalLive(t *testing.T) {
	url := os.Getenv("LENDING_LIVE_RPC")
	block := os.Getenv("LENDING_LIVE_BLOCK")
	if url == "" || block == "" {
		t.Skip("LENDING_LIVE_RPC / LENDING_LIVE_BLOCK not set")
	}
	c := restaking.RPCCaller{URL: url, Client: &http.Client{Timeout: 30 * time.Second}, Block: block}
	start := time.Now()
	ms, err := ReadAll(context.Background(), c)
	t.Logf("%d markets in %s, err=%v", len(ms), time.Since(start), err)
	n := map[string]int{}
	for _, m := range ms {
		n[m.Protocol]++
		if m.Symbol == "WAVAX" || m.Symbol == "AVAX" || m.Symbol == "sAVAX" {
			p := -1.0
			if m.PriceUSD != nil {
				p = *m.PriceUSD
			}
			t.Logf("%s %s sup=%s bor=%s bapr=%.4f price=%.3f", m.Protocol, m.Symbol, m.Supplied, m.Borrowed, m.BorrowAPR, p)
		}
	}
	t.Log(n)
}
