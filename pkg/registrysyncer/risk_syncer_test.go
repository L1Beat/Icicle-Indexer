package registrysyncer

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// fakeEVM is a JSON-RPC server that answers eth_call by (to, data) and
// eth_getCode by address; anything else reverts or has no code.
func fakeEVM(t *testing.T, calls map[string]string, code map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(v interface{}) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": v})
		}
		switch req.Method {
		case "eth_getCode":
			var addr string
			_ = json.Unmarshal(req.Params[0], &addr)
			if code[addr] {
				reply("0x6080")
			} else {
				reply("0x")
			}
		case "eth_call":
			var msg struct{ To, Data string }
			_ = json.Unmarshal(req.Params[0], &msg)
			if out, ok := calls[msg.To+":"+msg.Data]; ok {
				reply(out)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "error": map[string]interface{}{"code": 3, "message": "execution reverted"}})
		default:
			reply("0x")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func word(hexVal string) string {
	return strings.Repeat("0", 64-len(hexVal)) + hexVal
}

func TestReadBalancer(t *testing.T) {
	const (
		bal   = "0x00000000000000000000000000000000000000b1"
		poa   = "00000000000000000000000000000000000000a1"
		mw    = "00000000000000000000000000000000000000a2"
		owner = "00000000000000000000000000000000000000e1"
	)
	calls := map[string]string{
		bal + ":" + getSecurityModulesSelector:                   "0x" + word("20") + word("2") + word(poa) + word(mw),
		bal + ":" + ownerSelector:                                "0x" + word(owner),
		bal + ":" + getSecurityModuleWeightsSelector + word(poa): "0x" + word("0") + word("989680"),
		bal + ":" + getSecurityModuleWeightsSelector + word(mw):  "0x" + word("4c4b40") + word("4c4b40"),
		"0x" + poa + ":" + balancerValidatorManagerSelector:      "0x" + word(bal[2:]),
		"0x" + poa + ":" + ownerSelector:                         "0x" + word(owner),
		"0x" + mw + ":" + getVaultManagerSelector:                "0x" + word("c1"),
		"0x" + mw + ":" + balancerValidatorManagerSelector:       "0x" + word(bal[2:]),
	}
	srv := fakeEVM(t, calls, nil)

	b, ok := readBalancer(srv.Client(), srv.URL, bal)
	if !ok {
		t.Fatal("expected a balancer")
	}
	if b.Owner == nil || b.Owner.Address != "0x"+owner || b.Owner.Kind != "eoa" {
		t.Fatalf("owner: %+v", b.Owner)
	}
	if len(b.Modules) != 2 {
		t.Fatalf("modules: %+v", b.Modules)
	}
	p, m := b.Modules[0], b.Modules[1]
	if p.Address != "0x"+poa || p.Kind != "poa" || p.Weight != 0 || p.MaxWeight != 10_000_000 || p.Owner == nil {
		t.Errorf("poa module: %+v", p)
	}
	if m.Address != "0x"+mw || m.Kind != "suzaku_middleware" || m.Weight != 5_000_000 || m.Owner != nil {
		t.Errorf("middleware module: %+v", m)
	}

	// A contract that doesn't answer getSecurityModules() is not a balancer.
	if _, ok := readBalancer(srv.Client(), srv.URL, "0x00000000000000000000000000000000000000ff"); ok {
		t.Error("non-balancer detected as balancer")
	}
}

func TestBalancerJSON(t *testing.T) {
	if balancerJSON(nil) != "" {
		t.Error("nil balancer should encode as empty")
	}
	got := balancerJSON(&balancerInfo{Modules: []securityModule{{Address: "0xa", Kind: "poa", MaxWeight: 1}}})
	want := `{"owner":null,"security_modules":[{"address":"0xa","kind":"poa","weight":0,"max_weight":1,"owner":null}]}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestFetchOwner_NoContract(t *testing.T) {
	const (
		empty = "0x00000000000000000000000000000000000000aa"
		owned = "0x00000000000000000000000000000000000000bb"
	)
	srv := fakeEVM(t, map[string]string{
		empty + ":" + ownerSelector: "0x",
		owned + ":" + ownerSelector: "0x" + word("e1"),
	}, nil)

	if _, err := fetchOwner(srv.Client(), srv.URL, empty); !errors.Is(err, errNoContract) {
		t.Errorf("empty result: got %v, want errNoContract", err)
	}
	if owner, err := fetchOwner(srv.Client(), srv.URL, owned); err != nil || owner != "0x00000000000000000000000000000000000000e1" {
		t.Errorf("owned: got %q, %v", owner, err)
	}
	// A revert is a real error, not errNoContract.
	if _, err := fetchOwner(srv.Client(), srv.URL, "0x00000000000000000000000000000000000000cc"); err == nil || errors.Is(err, errNoContract) {
		t.Errorf("revert: got %v", err)
	}
}
