// Package ethcall runs eth_call against EVM contracts over JSON-RPC. It is the
// one contract-read client the C-Chain snapshot packages (restaking,
// lendingmarkets, bridges, icm, rwaprices) share.
package ethcall

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Caller runs eth_call against a contract and returns the raw return data.
type Caller interface {
	Call(ctx context.Context, to string, data []byte) ([]byte, error)
}

// HTTPClient is the shared HTTP client for C-Chain contract reads.
var HTTPClient = &http.Client{Timeout: 15 * time.Second}

// CChainURL returns the C-Chain JSON-RPC URL of the avalanchego node at
// nodeURL, which may be the node's base URL or its P-Chain endpoint.
func CChainURL(nodeURL string) string {
	return strings.TrimSuffix(strings.TrimRight(nodeURL, "/"), "/ext/bc/P") + "/ext/bc/C/rpc"
}

// RPCCaller is a Caller over a JSON-RPC endpoint (C-Chain).
type RPCCaller struct {
	URL    string
	Client *http.Client
	Block  string // block tag or 0x height for historical reads, empty for latest
}

func (c RPCCaller) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	block := c.Block
	if block == "" {
		block = "latest"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "eth_call",
		"params": []interface{}{map[string]string{"to": to, "data": "0x" + hex.EncodeToString(data)}, block},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("rpc error: %s", out.Error.Message)
	}
	return hex.DecodeString(strings.TrimPrefix(out.Result, "0x"))
}
