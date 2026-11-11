package aave

import (
	"testing"

	"icicle/pkg/lending"
)

// A reserve missing from the params map (listed since the last refresh, or
// whose config read failed) must not be valued with decimals=0, which would
// inflate base_value by 10^decimals.
func TestBuildProbe_SkipsLegWithUnknownDecimals(t *testing.T) {
	a := New("")
	known := "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"
	fresh := "0x9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7"
	a.Configure(lending.Addresses{Pool: "0x794a61358d6845594f94dc1db02a252b5b4814ad", Oracle: "0x00000000000000000000000000000000000000aa", DataProvider: "0x00000000000000000000000000000000000000bb"},
		[]lending.AssetParam{{Asset: known, Decimals: 18}}, lending.GlobalParams{})
	acct := "0x2222222222222222222222222222222222222222"

	p := a.BuildProbe(acct, []lending.Exposure{
		{Account: acct, Asset: known, Side: lending.SideCollateral},
		{Account: acct, Asset: fresh, Side: lending.SideCollateral},
	})
	summary := append(append(append(append(append(word(100), word(50)...), word(0)...), word(0)...), word(0)...), word(2e18)...)
	reserve := append(append(word(1e18), word(0)...), word(0)...)
	h := p.Decode([]lending.CallResult{
		{Success: true, ReturnData: summary},
		{Success: true, ReturnData: reserve}, {Success: true, ReturnData: word(1e8)},
		{Success: true, ReturnData: reserve}, {Success: true, ReturnData: word(1e8)},
		{Success: true, ReturnData: word(0)},
	}, 1)
	if !h.OK {
		t.Fatal("summary read should still mark the account OK")
	}
	if len(h.Assets) != 1 || h.Assets[0].Asset != known {
		t.Fatalf("want only the known-decimals leg, got %+v", h.Assets)
	}
	// 1 token at price 1e8 with 18 decimals -> 1e8 in base units.
	if got := h.Assets[0].BaseValue.Int64(); got != 1e8 {
		t.Fatalf("known leg base_value = %d, want 1e8", got)
	}
}

// A refresh whose config read for a reserve failed returns params without it.
// Configure must keep the last known decimals instead of dropping them.
func TestConfigure_KeepsParamsForReservesMissingFromRefresh(t *testing.T) {
	a := New("")
	asset := "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"
	other := "0x9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7"
	a.Configure(lending.Addresses{}, []lending.AssetParam{{Asset: asset, Decimals: 18}, {Asset: other, Decimals: 6}}, lending.GlobalParams{})
	a.Configure(lending.Addresses{}, []lending.AssetParam{{Asset: other, Decimals: 6}}, lending.GlobalParams{})

	if p, ok := a.params[asset]; !ok || p.Decimals != 18 {
		t.Fatalf("decimals for a reserve missing from the refresh were dropped: %+v ok=%v", p, ok)
	}
}

// A failed per-asset read keeps the account OK but lists the asset so its
// previous legs are carried forward instead of disappearing.
func TestBuildProbe_FailedAssetReadIsKept(t *testing.T) {
	a := New("")
	asset := "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"
	a.Configure(lending.Addresses{Pool: "0x794a61358d6845594f94dc1db02a252b5b4814ad", Oracle: "0x00000000000000000000000000000000000000aa", DataProvider: "0x00000000000000000000000000000000000000bb"},
		[]lending.AssetParam{{Asset: asset, Decimals: 18}}, lending.GlobalParams{})
	acct := "0x2222222222222222222222222222222222222222"
	p := a.BuildProbe(acct, []lending.Exposure{{Account: acct, Asset: asset, Side: lending.SideDebt}})
	summary := append(append(append(append(append(word(100), word(50)...), word(0)...), word(0)...), word(0)...), word(2e18)...)
	h := p.Decode([]lending.CallResult{
		{Success: true, ReturnData: summary},
		{Success: true, ReturnData: word(0)}, {Success: false},
		{Success: true, ReturnData: word(0)},
	}, 1)
	if !h.OK || len(h.Assets) != 0 || len(h.KeepLegs) != 1 || h.KeepLegs[0] != asset {
		t.Fatalf("ok=%v assets=%v keep=%v", h.OK, h.Assets, h.KeepLegs)
	}
}
