package registrysyncer

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// TestResolveChainRisk_LiveDexalot exercises the full detection path against the live
// Dexalot ValidatorManager on C-Chain. Skipped by default (hits the network); run with:
//
//	RISK_LIVE_TEST=1 go test ./pkg/registrysyncer/ -run TestResolveChainRisk_LiveDexalot -v
func TestResolveChainRisk_LiveDexalot(t *testing.T) {
	if os.Getenv("RISK_LIVE_TEST") != "1" {
		t.Skip("set RISK_LIVE_TEST=1 to run live network validation")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	in := riskInput{
		ChainID:   "21Ths5Afqi5r4PaoV8r8cruGZWhN11y5rxvy89K8px7pKy3P8E",
		VMAddress: "0x05ad6d9cecf4b08e5ee0e636a245776d09810f4c",
		RpcURL:    "https://subnets.avax.network/dexalot/mainnet/rpc", // no code here -> exercises C-Chain fallback
	}

	row := resolveChainRisk(client, in)

	deref := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	t.Logf("type=%s owner=%s kind=%s isProxy=%v impl=%s admin=%s adminOwner=%s delay=%v churn=%v maxChurn=%v",
		row.ManagerType, deref(row.OwnerAddress), row.OwnerKind, row.IsProxy,
		deref(row.ProxyImplementation), deref(row.ProxyAdmin), deref(row.ProxyAdminOwner),
		row.UpgradeDelaySeconds, row.ChurnPeriodSeconds, row.MaxChurnPercentage)

	if row.OwnerAddress == nil || *row.OwnerAddress != "0xcff0fc701ef47d6217fdf9def903990b7afa8ac7" {
		t.Errorf("owner: got %s, want 0xcff0fc701ef47d6217fdf9def903990b7afa8ac7", deref(row.OwnerAddress))
	}
	if row.OwnerKind != "contract" {
		t.Errorf("owner kind: got %s, want contract", row.OwnerKind)
	}
	if !row.IsProxy {
		t.Error("expected is_proxy=true")
	}
	if row.ProxyImplementation == nil || *row.ProxyImplementation != "0x068cd30a749dcdf94669580b5e7cd244a408a8bc" {
		t.Errorf("impl: got %s", deref(row.ProxyImplementation))
	}
	if row.ProxyAdminOwner == nil || *row.ProxyAdminOwner != "0xbfd53904e0a0c02efb7e76aad7ffb1f476320038" {
		t.Errorf("proxy admin owner: got %s", deref(row.ProxyAdminOwner))
	}
	if row.UpgradeDelaySeconds == nil || *row.UpgradeDelaySeconds != 0 {
		t.Errorf("upgrade delay: want 0 (EOA-controlled), got %v", row.UpgradeDelaySeconds)
	}
	if row.ChurnPeriodSeconds == nil || *row.ChurnPeriodSeconds != 3600 {
		t.Errorf("churn period: want 3600, got %v", row.ChurnPeriodSeconds)
	}
	if row.MaxChurnPercentage == nil || *row.MaxChurnPercentage != 20 {
		t.Errorf("max churn: want 20, got %v", row.MaxChurnPercentage)
	}
}
