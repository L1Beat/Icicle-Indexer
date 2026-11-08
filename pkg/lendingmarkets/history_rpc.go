package lendingmarkets

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RPCHistory is a History over a C-Chain JSON-RPC endpoint on an archive node.
type RPCHistory struct {
	URL    string
	Client *http.Client
}

func (h RPCHistory) rpc(ctx context.Context, method string, params []interface{}, out interface{}) error {
	body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: rpc error: %s", method, r.Error.Message)
	}
	return json.Unmarshal(r.Result, out)
}

func blockTag(n uint64) string { return "0x" + strconv.FormatUint(n, 16) }

// Head returns the latest block number.
func (h RPCHistory) Head(ctx context.Context) (uint64, error) {
	var s string
	if err := h.rpc(ctx, "eth_blockNumber", nil, &s); err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}

func (h RPCHistory) CallAt(ctx context.Context, block uint64, to string, data []byte) ([]byte, error) {
	var s string
	err := h.rpc(ctx, "eth_call", []interface{}{
		map[string]string{"to": to, "data": "0x" + hex.EncodeToString(data)}, blockTag(block)}, &s)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

func (h RPCHistory) BlockTime(ctx context.Context, block uint64) (time.Time, error) {
	var b *struct {
		Timestamp string `json:"timestamp"`
	}
	if err := h.rpc(ctx, "eth_getBlockByNumber", []interface{}{blockTag(block), false}, &b); err != nil {
		return time.Time{}, err
	}
	if b == nil {
		return time.Time{}, fmt.Errorf("block %d not found", block)
	}
	ts, err := strconv.ParseInt(strings.TrimPrefix(b.Timestamp, "0x"), 16, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(ts, 0).UTC(), nil
}

func (h RPCHistory) LogTx(ctx context.Context, block uint64, address string, topics [][]string) (string, error) {
	ts := make([]interface{}, len(topics))
	for i, alts := range topics {
		if alts != nil {
			ts[i] = alts
		}
	}
	var logs []struct {
		TxHash string `json:"transactionHash"`
	}
	err := h.rpc(ctx, "eth_getLogs", []interface{}{map[string]interface{}{
		"fromBlock": blockTag(block), "toBlock": blockTag(block), "address": address, "topics": ts,
	}}, &logs)
	if err != nil {
		return "", err
	}
	if len(logs) == 0 {
		return "", nil
	}
	return strings.ToLower(logs[0].TxHash), nil
}
