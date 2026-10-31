package liquidstaking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icicle/pkg/pchainrpc"
)

func TestProtocolsConfig(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Protocols {
		if seen[p.ID] {
			t.Fatalf("duplicate id %s", p.ID)
		}
		seen[p.ID] = true
		if p.Status != "live" && p.Status != "ended" {
			t.Fatalf("%s: bad status %q", p.ID, p.Status)
		}
		if p.Attribution != "confirmed" && p.Attribution != "unconfirmed" {
			t.Fatalf("%s: bad attribution %q", p.ID, p.Attribution)
		}
		for _, a := range p.PChainAddresses {
			if _, err := pchainrpc.ConvertPChainAddressToCB58(a); err != nil {
				t.Fatalf("%s: %v", p.ID, err)
			}
		}
	}
}

func TestReadTokenState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		word := "0000000000000000000000000000000000000000000000000000000000000002" // supply
		if strings.Contains(string(req.Params[0]), "0x629e8056") {
			word = "0000000000000000000000000000000000000000000000000000000000000003" // pooled
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x` + word + `"}`))
	}))
	defer srv.Close()

	st, err := ReadTokenState(context.Background(), srv.Client(), srv.URL, Protocols[0])
	if err != nil {
		t.Fatal(err)
	}
	if st.PooledAvax.Int64() != 3 || st.TokenSupply.Int64() != 2 {
		t.Fatalf("got %v %v", st.PooledAvax, st.TokenSupply)
	}
}
