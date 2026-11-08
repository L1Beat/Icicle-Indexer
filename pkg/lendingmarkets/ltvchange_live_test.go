package lendingmarkets

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestDateLTV_Live dates known LTV changes against an archive node. Set
// LENDING_LIVE_RPC to a C-Chain archive RPC URL to run it.
func TestDateLTV_Live(t *testing.T) {
	url := os.Getenv("LENDING_LIVE_RPC")
	if url == "" {
		t.Skip("LENDING_LIVE_RPC not set")
	}
	ctx := context.Background()
	h := RPCHistory{URL: url, Client: &http.Client{Timeout: 30 * time.Second}}
	head, err := h.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Aave V3 USDC went 7500 -> 0 in block 98902157 (2026-10-29), and
	// WAVAX 0 -> 6800 in block 99231889 (2026-11-02). Windows are the day's
	// snapshots, as the syncer passes them.
	day := func(s string) time.Time { tm, _ := time.Parse(time.RFC3339, s); return tm }
	for _, c := range []struct {
		asset  string
		cur    uint16
		lo, hi time.Time
		block  uint64
		tx     string
	}{
		{"0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", 0, day("2026-10-28T23:59:59Z"), day("2026-10-29T23:59:59Z"), 98902157,
			"0xa9b3c3f499a77de4577b6e66aebd99aa0b7b90ffe050d6d71249d26661e62c48"},
		{WAVAX, 6800, day("2026-11-01T23:59:59Z"), day("2026-11-02T23:59:59Z"), 99231889,
			"0x2bb136df1f295eeced29f02614654244507dd5410783d83f6d6e581c7724de90"},
		// Older zero-LTV listings: DAI.e, EURC, AUSD.
		{"0xd586e7f844cea2f87f50152665bcbc2c279d8d70", 0, day("2025-12-22T23:59:59Z"), day("2025-12-23T23:59:59Z"), 74274427,
			"0x7384103761ba90af41eb072e9664de674c4fd6d49d8a21cf58b46f6f80235178"},
		{"0xc891eb4cbdeff6e073e859e987815ed1505c2acd", 0, day("2026-03-09T23:59:59Z"), day("2026-03-10T23:59:59Z"), 80038260,
			"0x788ad3ba604a8a06e9c9a7331142880712843c16ecf2eadc4d0e6dcd65d60e35"},
		{"0x00000000efe302beaa2b3e6e1b18d08d69a9012a", 0, day("2026-07-21T23:59:59Z"), day("2026-07-22T23:59:59Z"), 90971174,
			"0x69808315c20b313f9d5798ce99ed88ffbab4371ed370aef0a28150409a508119"},
	} {
		lo, err := BlockAtTime(ctx, h, c.lo, head)
		if err != nil {
			t.Fatal(err)
		}
		hi, err := BlockAtTime(ctx, h, c.hi, head)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := DateMarketLTV(ctx, h, ProtocolAaveV3, "", c.asset, c.cur, lo, hi)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("aave-v3 %s -> %d at block %d %s tx %s", c.asset, c.cur, ch.Block, ch.At, ch.Tx)
		if ch.Block != c.block || ch.Tx != c.tx {
			t.Errorf("%s: got block %d tx %s, want %d %s", c.asset, ch.Block, ch.Tx, c.block, c.tx)
		}
	}

	chs, err := DateAaveV4Spokes(ctx, h, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{
		"0x435272ceff93a1e657e8abfdf0a13e95900a3a56|0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e": 98951438, // USDC Main
		"0x6a37776b5e026dbdf043b4f933c323c84dd1b514|0x9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7": 98951438, // USDt Forex
		"0x435272ceff93a1e657e8abfdf0a13e95900a3a56|" + WAVAX:                                   99233827, // WAVAX Main
		"0x3b517594277c67307cf2d7cbe6fe1d4399b68c41|" + SAVAX:                                   99233827, // sAVAX AVAX Correlated
		"0x435272ceff93a1e657e8abfdf0a13e95900a3a56|0x152b9d0fdc40c096757f570a51e494bd4b943e50": 0,        // BTC.b Main, never changed
	}
	for _, ch := range chs {
		t.Logf("aave-v4 %s %s cf %d block %d at %v tx %s", ch.GroupID, ch.Asset, ch.LTVBps, ch.Block, ch.At, ch.Tx)
		if b, ok := want[ch.GroupID+"|"+ch.Asset]; ok {
			if ch.Block != b {
				t.Errorf("%s %s: block %d, want %d", ch.GroupID, ch.Asset, ch.Block, b)
			}
			if b != 0 && ch.Tx == "" {
				t.Errorf("%s %s: no tx", ch.GroupID, ch.Asset)
			}
			delete(want, ch.GroupID+"|"+ch.Asset)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing V4 entries: %v", want)
	}
}
