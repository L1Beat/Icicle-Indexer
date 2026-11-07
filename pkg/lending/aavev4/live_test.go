package aavev4

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"icicle/pkg/lending"
)

// TestLiveProbe runs the adapter's real read path against a live node: resolve,
// params refresh, then the exact Multicall3 probe the engine sends. It checks that
// the per-leg values add up to the spoke's own totals, and that the health factor
// rebuilt from each collateral leg's collateral factor at its config key matches
// the spoke's healthFactor.
//
//	LENDING_IT_ARCHIVE_RPC   node RPC URL
//	LENDING_IT_V4_ACCOUNT    a borrower with open debt on the spoke
//	LENDING_IT_V4_SPOKE      optional, defaults to the Main spoke
//
//	Run: LENDING_IT_ARCHIVE_RPC=https://rpc.l1beat.io/ext/bc/C/rpc LENDING_IT_V4_ACCOUNT=0x... \
//		go test ./pkg/lending/aavev4/ -run TestLiveProbe -v
func TestLiveProbe(t *testing.T) {
	rpcURL, account := os.Getenv("LENDING_IT_ARCHIVE_RPC"), os.Getenv("LENDING_IT_V4_ACCOUNT")
	if rpcURL == "" || account == "" {
		t.Skip("set LENDING_IT_ARCHIVE_RPC and LENDING_IT_V4_ACCOUNT to run")
	}
	spoke := os.Getenv("LENDING_IT_V4_SPOKE")
	if spoke == "" {
		spoke = "0x435272CefF93a1E657E8ABfdf0A13e95900A3a56"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rpc := lending.NewClient(rpcURL, "")

	a := New(spoke)
	addrs, notes, err := a.Resolve(ctx, rpc)
	if err != nil {
		t.Fatalf("resolve: %v (%+v)", err, notes)
	}
	a.Configure(addrs, nil, lending.GlobalParams{})
	params, globals, err := a.RefreshParams(ctx, rpc)
	if err != nil {
		t.Fatalf("refresh params: %v", err)
	}
	a.Configure(addrs, params, globals)
	for _, p := range params {
		t.Logf("reserve %s %s dec=%d cf=%d max_bonus=%d borrowable=%v", p.Symbol, p.Asset, p.Decimals, p.LtvBps, p.LiquidationBonusBps, p.CanBorrow)
	}

	// Detail probe across every reserve, as if discovery had seen them all.
	var exposure []lending.Exposure
	for _, p := range params {
		exposure = append(exposure, lending.Exposure{Account: account, Asset: p.Asset})
	}
	probe := a.BuildProbe(account, exposure)
	res, err := rpc.EthCall(ctx, lending.Multicall3Address, lending.EncodeAggregate3(probe.Calls), "latest")
	if err != nil {
		t.Fatalf("aggregate3: %v", err)
	}
	results, err := lending.DecodeAggregate3(lending.DecodeHexBytes(res))
	if err != nil {
		t.Fatalf("decode aggregate3: %v", err)
	}
	h := probe.Decode(results, 0)
	if !h.OK {
		t.Fatal("probe not OK")
	}
	if h.DebtBase.Sign() == 0 {
		t.Fatalf("account %s has no debt on spoke %s, pick another fixture", account, spoke)
	}

	collSum, debtSum, weighted := new(big.Int), new(big.Int), new(big.Int)
	for _, leg := range h.Assets {
		t.Logf("leg %s %s amount=%s base=%s entered=%v config_key=%d", leg.Side, leg.Asset, leg.Amount, leg.BaseValue, !leg.NotEntered, leg.ConfigKey)
		if leg.Side == lending.SideDebt {
			debtSum.Add(debtSum, leg.BaseValue)
			continue
		}
		if leg.NotEntered {
			continue
		}
		collSum.Add(collSum, leg.BaseValue)
		r := a.byAsset[leg.Asset]
		dyn, err := rpc.EthCall(ctx, spoke, lending.EncodeCall2Uint(fnGetDynamicConfig, r.id, big.NewInt(int64(leg.ConfigKey))), "latest")
		if err != nil {
			t.Fatalf("dynamic config: %v", err)
		}
		cf := lending.Word(lending.DecodeHexBytes(dyn), 0)
		weighted.Add(weighted, new(big.Int).Div(new(big.Int).Mul(leg.BaseValue, cf), big.NewInt(10000)))
	}
	t.Logf("spoke: hf=%s collateral_base=%s debt_base=%s", h.HealthFactor, h.CollateralBase, h.DebtBase)
	t.Logf("legs:  collateral=%s debt=%s", collSum, debtSum)

	within(t, "collateral legs vs totalCollateralValue", collSum, h.CollateralBase, 10)
	within(t, "debt legs vs totalDebtValueRay", debtSum, h.DebtBase, 10)
	rebuilt := new(big.Int).Div(new(big.Int).Mul(weighted, lending.WAD), debtSum)
	t.Logf("rebuilt hf from legs and config keys = %s", rebuilt)
	within(t, "rebuilt HF vs spoke healthFactor", rebuilt, h.HealthFactor, 10)
}

// within fails unless a and b differ by at most bps basis points.
func within(t *testing.T, name string, a, b *big.Int, bps int64) {
	t.Helper()
	if b.Sign() == 0 {
		t.Fatalf("%s: zero reference", name)
	}
	diff := new(big.Int).Abs(new(big.Int).Sub(a, b))
	if new(big.Int).Mul(diff, big.NewInt(10000)).Cmp(new(big.Int).Mul(new(big.Int).Abs(b), big.NewInt(bps))) > 0 {
		t.Fatalf("%s: %s vs %s differ by more than %d bps", name, a, b, bps)
	}
}
