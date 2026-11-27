package ethcall

import "testing"

func TestCChainURL(t *testing.T) {
	for _, in := range []string{
		"http://127.0.0.1:9650",
		"http://127.0.0.1:9650/",
		"http://127.0.0.1:9650/ext/bc/P",
		"http://127.0.0.1:9650/ext/bc/P/",
	} {
		if got := CChainURL(in); got != "http://127.0.0.1:9650/ext/bc/C/rpc" {
			t.Errorf("CChainURL(%q) = %q", in, got)
		}
	}
}
