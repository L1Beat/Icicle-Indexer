package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// eip1967ImplSlot is keccak256("eip1967.proxy.implementation") - 1.
const eip1967ImplSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"

// rpcCall performs a JSON-RPC POST against our own node and returns the raw
// "result" string. Used for eth_getCode and the EIP-1967 slot read; both are
// cheap and local, unlike the third-party lookups.
func rpcCall(ctx context.Context, client *http.Client, rpcURL, method string, params ...interface{}) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var parsed struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("%s: decode: %w", method, err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("%s: %s", method, parsed.Error.Message)
	}
	return parsed.Result, nil
}

// hasCode reports whether eth_getCode returned bytecode.
func hasCode(ctx context.Context, client *http.Client, rpcURL, addr string) (bool, error) {
	res, err := rpcCall(ctx, client, rpcURL, "eth_getCode", addr, "latest")
	if err != nil {
		return false, err
	}
	return res != "" && res != "0x", nil
}

// eip1967Implementation reads the implementation slot; "" when zero.
func eip1967Implementation(ctx context.Context, client *http.Client, rpcURL, addr string) (string, error) {
	res, err := rpcCall(ctx, client, rpcURL, "eth_getStorageAt", addr, eip1967ImplSlot, "latest")
	if err != nil {
		return "", err
	}
	h := strings.TrimPrefix(res, "0x")
	if len(h) < 40 {
		return "", nil
	}
	impl := "0x" + h[len(h)-40:]
	if strings.Trim(impl[2:], "0") == "" {
		return "", nil
	}
	return impl, nil
}
