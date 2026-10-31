package registrysyncer

import (
	"net/http"
	"testing"
)

func TestIsPlaceholderManager(t *testing.T) {
	for addr, want := range map[string]bool{
		"":   true,
		"0x": true,
		"0x0000000000000000000000000000000000000000": true,
		"0x0000000000000000000000000000000000000001": true,
		"0x0000000000000000000000000000000000000009": true,
		"0x000000000000000000000000000000000000000a": false,
		"0x0100000000000000000000000000000000000000": false,
		"0x0feedc0de0000000000000000000000000000000": false,
		"0xnothex": false,
	} {
		if got := isPlaceholderManager(addr); got != want {
			t.Errorf("isPlaceholderManager(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestResolveChainRisk_Placeholder(t *testing.T) {
	// Returns before any RPC call, so a nil-transport client is never used.
	row := resolveChainRisk(&http.Client{}, riskInput{ChainID: "c", VMAddress: "0x0000000000000000000000000000000000000001"})
	if row.ManagerType != "placeholder" || row.ManagerLocation != "unknown" || row.OwnerAddress != nil {
		t.Fatalf("unexpected row: %+v", row)
	}
}
