package lendingmarkets

import (
	"context"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"icicle/pkg/restaking"
)

// TestReadAll_Live reads the real markets. Set LENDING_LIVE_RPC to a C-Chain
// RPC URL to run it.
func TestReadAll_Live(t *testing.T) {
	url := os.Getenv("LENDING_LIVE_RPC")
	if url == "" {
		t.Skip("LENDING_LIVE_RPC not set")
	}
	c := restaking.RPCCaller{URL: url, Client: &http.Client{Timeout: 15 * time.Second}}
	start := time.Now()
	ms, err := ReadAll(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d markets in %s", len(ms), time.Since(start))
	seen := map[string]int{}
	usd := map[string][2]float64{}
	for _, m := range ms {
		seen[m.Protocol]++
		if m.PriceUSD != nil {
			scale := pow10f(int(m.Decimals))
			s, _ := new(big.Float).SetInt(m.Supplied).Float64()
			b, _ := new(big.Float).SetInt(m.Borrowed).Float64()
			t := usd[m.Protocol]
			usd[m.Protocol] = [2]float64{t[0] + s/scale**m.PriceUSD, t[1] + b/scale**m.PriceUSD}
		}
		price := "nil"
		if m.PriceUSD != nil {
			price = strconv.FormatFloat(*m.PriceUSD, 'f', 4, 64)
		}
		t.Logf("%-8s %-8s %s sup=%s bor=%s sapr=%.4f bapr=%.4f price=%s cap=%v/%v",
			m.Protocol, m.Symbol, m.Asset, m.Supplied, m.Borrowed, m.SupplyAPR, m.BorrowAPR, price, m.SupplyCap, m.BorrowCap)
	}
	for _, p := range []string{ProtocolAaveV3, ProtocolAaveV4, ProtocolBenqi} {
		t.Logf("%s supplied_usd=%.0f borrowed_usd=%.0f", p, usd[p][0], usd[p][1])
		if seen[p] == 0 {
			t.Errorf("no markets for %s", p)
		}
	}
}

func TestEMode_Live(t *testing.T) {
	url := os.Getenv("LENDING_LIVE_RPC")
	if url == "" {
		t.Skip("LENDING_LIVE_RPC not set")
	}
	c := restaking.RPCCaller{URL: url, Client: &http.Client{Timeout: 15 * time.Second}}
	for _, read := range []func(context.Context, Caller) ([]EModeEntry, error){ReadAaveV3EMode, ReadAaveV4Spokes} {
		es, err := read(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			prev := "-"
			if e.PrevLTVBps != nil {
				prev = strconv.Itoa(int(*e.PrevLTVBps))
			}
			t.Logf("%s %s %q %s ltv=%d conf=%d lt=%d bonus=%d coll=%v borr=%v prev=%s", e.Protocol, e.GroupID[:min(8, len(e.GroupID))], e.GroupLabel, e.Asset[:10], e.LTVBps, e.ConfLTVBps, e.LTBps, e.BonusBps, e.Collateral, e.Borrowable, prev)
		}
	}
}
