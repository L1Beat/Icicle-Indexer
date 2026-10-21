package api

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"icicle/pkg/enrich"
)

const (
	lfjRouter = "0x60ae616a2155ee3d9a68541ba4544862310933d4"
	usdcAddr  = "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"
	wallet    = "0x7400000000000000000000000000000000000000"
)

// seedEnrichment loads a small in-memory enrichment state: one curated router,
// one token known only from token_metadata, and the transfer/swap selectors.
func seedEnrichment(s *Server) {
	s.setEnrichmentForTest(
		map[[4]byte][]enrich.Candidate{
			enrich.SelectorOf("transfer(address,uint256)"): {
				{Signature: "many_msg_babbage(bytes1)", Priority: enrich.PriorityOpenChain},
				{Signature: "transfer(address,uint256)", Priority: enrich.PrioritySeed},
			},
			enrich.SelectorOf("swapExactTokensForTokens(uint256,uint256,address[],address,uint256)"): {
				{Signature: "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)", Priority: enrich.PrioritySeed},
			},
		},
		map[uint32]map[string][]enrich.Label{43114: {
			lfjRouter: {{Address: lfjRouter, Name: "LFJ Router (v1)", Kind: "router", Protocol: "LFJ", IsContract: true, Verified: true, Source: enrich.SourceCurated}},
		}},
		map[uint32]map[string]enrich.TokenInfo{43114: {
			usdcAddr: {Name: "USD Coin", Symbol: "USDC"},
		}},
	)
}

func TestHandleLabels_Success(t *testing.T) {
	server := NewTestServer(&MockConn{})
	seedEnrichment(server)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/labels?addresses=0x60aE616a2155Ee3d9A68541Ba4544862310933d4,"+usdcAddr+","+wallet)
	AssertJSONResponse(t, w, http.StatusOK)

	var resp struct {
		Data map[string]AddressLabel `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2, "unknown wallet is omitted")

	router := resp.Data[lfjRouter]
	assert.Equal(t, "LFJ Router (v1)", router.Name)
	assert.Equal(t, "router", router.Kind)
	require.NotNil(t, router.Protocol)
	assert.Equal(t, "LFJ", *router.Protocol)
	assert.True(t, router.Verified)
	assert.Nil(t, router.Implementation)

	token := resp.Data[usdcAddr]
	assert.Equal(t, "USD Coin (USDC)", token.Name)
	assert.Equal(t, "token", token.Kind)
	assert.Nil(t, token.Protocol)
	assert.False(t, token.Verified)
	assert.True(t, token.IsContract)
}

func TestHandleLabels_Validation(t *testing.T) {
	server := NewTestServer(&MockConn{})
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/labels")
	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)

	w = MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/labels?addresses=0x123")
	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)

	many := ""
	for i := 0; i < 101; i++ {
		if i > 0 {
			many += ","
		}
		many += wallet
	}
	w = MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/labels?addresses="+many)
	AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
}

// txRow builds one txSelectCols mock row.
func txRow(hash byte, from, to [20]byte, value int64, sig string, inputLen uint64) []interface{} {
	var selector []byte
	if sig != "" {
		s := enrich.SelectorOf(sig)
		selector = s[:]
	}
	return []interface{}{
		uint32(43114), [32]byte{hash}, uint32(12345678), time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), uint16(0),
		from, to[:], *big.NewInt(value), uint32(21000), uint64(25000000000), uint32(21000), true, uint8(2),
		selector, inputLen,
	}
}

var txColumns = []string{"chain_id", "hash", "block_number", "block_time", "transaction_index", "from", "to", "value", "gas_limit", "gas_price", "gas_used", "success", "type", "selector", "input_len"}

func TestHandleListTxs_MethodLabelsIntent(t *testing.T) {
	var from, router, usdcB [20]byte
	from[0] = 0x74
	copy(router[:], mustHex(lfjRouter))
	copy(usdcB[:], mustHex(usdcAddr))

	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(txColumns, [][]interface{}{
				txRow(0x01, from, router, 0, "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)", 4+5*32+3*32),
				txRow(0x02, from, usdcB, 0, "transfer(address,uint256)", 4+64),
				txRow(0x03, from, [20]byte{0x99}, 1_000_000, "", 0),
			}), nil
		},
	}
	server := NewTestServer(mock)
	seedEnrichment(server)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs")
	AssertJSONResponse(t, w, http.StatusOK)

	var resp struct {
		Data []Transaction `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 3)

	swap := resp.Data[0]
	require.NotNil(t, swap.Method)
	assert.Equal(t, "swapExactTokensForTokens", swap.Method.Name)
	assert.Equal(t, "0x38ed1739", swap.Method.Selector)
	require.NotNil(t, swap.ToLabel)
	assert.Equal(t, "LFJ Router (v1)", swap.ToLabel.Name)
	assert.Nil(t, swap.FromLabel)
	require.NotNil(t, swap.Intent)
	assert.Equal(t, "swap", swap.Intent.Kind)
	assert.Equal(t, "LFJ", *swap.Intent.Protocol)

	transfer := resp.Data[1]
	require.NotNil(t, transfer.Method)
	assert.Equal(t, "transfer(address,uint256)", transfer.Method.Signature, "seed beats colliding spam signature")
	require.NotNil(t, transfer.ToLabel)
	assert.Equal(t, "token", transfer.ToLabel.Kind)
	assert.Equal(t, "send", transfer.Intent.Kind)

	plain := resp.Data[2]
	assert.Nil(t, plain.Method, "no calldata -> method null")
	assert.Nil(t, plain.ToLabel)
	require.NotNil(t, plain.Intent)
	assert.Equal(t, "send", plain.Intent.Kind)
	assert.Equal(t, "Sent AVAX", plain.Intent.Label)
}

func TestHandleListTxs_UnknownSelectorIsNull(t *testing.T) {
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(txColumns, [][]interface{}{
				txRow(0x01, [20]byte{0x74}, [20]byte{0x75}, 0, "mystery(uint256)", 36),
			}), nil
		},
	}
	server := NewTestServer(mock)
	seedEnrichment(server)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs")
	var resp struct {
		Data []Transaction `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Nil(t, resp.Data[0].Method)
	assert.Equal(t, "contract_call", resp.Data[0].Intent.Kind)
	assert.Equal(t, "Contract call", resp.Data[0].Intent.Label)
}

func TestHandleListTxs_IncludeTransfers(t *testing.T) {
	var from, router, usdcB [20]byte
	from[0] = 0x74
	copy(router[:], mustHex(lfjRouter))
	copy(usdcB[:], mustHex(usdcAddr))
	var transferTopic, approvalTopic [32]byte
	copy(transferTopic[:], mustHex("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"))
	copy(approvalTopic[:], mustHex("0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925"))

	calls := 0
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			calls++
			switch calls {
			case 1: // page of txs
				return NewMockRows(txColumns, [][]interface{}{
					txRow(0x01, from, router, 0, "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)", 4+8*32),
				}), nil
			case 2: // internal txs: one native refund back to the sender
				return NewMockRows([]string{"tx_hash", "trace_address", "from", "to", "value", "gas_used", "call_type", "tx_success"}, [][]interface{}{
					{[32]byte{0x01}, []uint16{0, 1}, router, from[:], *big.NewInt(5), uint32(100), "CALL", true},
				}), nil
			case 3: // token events: USDC out, approval
				return NewMockRows([]string{"transaction_hash", "topic0", "address", "name", "symbol", "decimals", "addr1", "addr2", "value", "log_index"}, [][]interface{}{
					{[32]byte{0x01}, transferTopic, usdcB, "USD Coin", "USDC", uint8(6), from[:], router[:], *big.NewInt(1000000), uint32(3)},
					{[32]byte{0x01}, approvalTopic, usdcB, "USD Coin", "USDC", uint8(6), from[:], router[:], *big.NewInt(1000000), uint32(2)},
				}), nil
			}
			return nil, ErrMockNotImpl
		},
	}
	server := NewTestServer(mock)
	seedEnrichment(server)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs?include=transfers&limit=100")
	AssertJSONResponse(t, w, http.StatusOK)
	assert.Equal(t, 3, calls, "one page query plus two batched detail queries, regardless of page size")

	var resp struct {
		Data []TransactionDetail `json:"data"`
		Meta Meta                `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, includeTransfersMaxLimit, resp.Meta.Limit, "limit clamped with include=transfers")
	require.Len(t, resp.Data, 1)
	d := resp.Data[0]
	require.Len(t, d.InternalTxs, 1)
	assert.Equal(t, "0,1", d.InternalTxs[0].TraceIndex)
	require.Len(t, d.TokenTransfers, 1)
	assert.Equal(t, usdcAddr, d.TokenTransfers[0].Token)
	require.NotNil(t, d.TokenTransfers[0].TokenLabel)
	assert.Equal(t, "USD Coin (USDC)", d.TokenTransfers[0].TokenLabel.Name)
	require.Len(t, d.Approvals, 1)
	assert.False(t, d.Approvals[0].IsUnlimited)
	require.NotNil(t, d.Intent)
	assert.Equal(t, "swap", d.Intent.Kind)
	assert.Equal(t, 0.9, d.Intent.Confidence, "tokens out + native in through a router")
}

func TestHandleListTxs_WithoutIncludeHasNoDetailQueries(t *testing.T) {
	calls := 0
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			calls++
			return NewMockRows(txColumns, [][]interface{}{txRow(0x01, [20]byte{1}, [20]byte{2}, 1, "", 0)}), nil
		},
	}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/txs")
	AssertJSONResponse(t, w, http.StatusOK)
	assert.Equal(t, 1, calls)
	var resp struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	_, hasTransfers := resp.Data[0]["token_transfers"]
	assert.False(t, hasTransfers, "default rows are unchanged apart from the new fields")
	assert.Contains(t, resp.Data[0], "method")
	assert.Contains(t, resp.Data[0], "from_label")
	assert.Contains(t, resp.Data[0], "to_label")
	assert.Contains(t, resp.Data[0], "intent")
}

func TestHandleAddressTxs_ReceiveIntent(t *testing.T) {
	var viewer [20]byte
	copy(viewer[:], mustHex(wallet))
	mock := &MockConn{
		QueryFunc: func(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
			return NewMockRows(txColumns, [][]interface{}{
				txRow(0x01, [20]byte{0x11}, viewer, 10, "", 0), // someone sent AVAX to the viewer
				txRow(0x02, viewer, [20]byte{0x22}, 10, "", 0), // viewer sent AVAX
			}), nil
		},
	}
	server := NewTestServer(mock)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/address/"+wallet+"/txs")
	AssertJSONResponse(t, w, http.StatusOK)
	var resp struct {
		Data []Transaction `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	assert.Equal(t, "receive", resp.Data[0].Intent.Kind)
	assert.Equal(t, "Received AVAX", resp.Data[0].Intent.Label)
	assert.Equal(t, "send", resp.Data[1].Intent.Kind)
}

func mustHex(s string) []byte {
	if len(s) >= 2 && s[:2] == "0x" {
		s = s[2:]
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var b byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				b = b*16 + (c - '0')
			case c >= 'a' && c <= 'f':
				b = b*16 + (c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				b = b*16 + (c - 'A' + 10)
			}
		}
		out[i] = b
	}
	return out
}
