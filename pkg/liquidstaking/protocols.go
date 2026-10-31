// Package liquidstaking lists the Avalanche liquid staking protocols tracked on
// the staking page and reads their C-Chain token contracts.
package liquidstaking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Protocol is one liquid staking token backed by Primary Network delegations.
type Protocol struct {
	ID            string
	Name          string
	TokenSymbol   string
	TokenContract string // C-Chain
	TokenDecimals int
	// PooledSelector returns the AVAX backing the token, in wei.
	PooledSelector string
	Status         string // live | ended
	// Attribution says whether the protocol has confirmed PChainAddresses.
	Attribution string // confirmed | unconfirmed
	// PChainAddresses are reward-owner addresses (P-avax1...). A delegation
	// belongs to the protocol when any of them is in its rewards owner.
	PChainAddresses []string
}

// Protocols is the tracked list. Yield Yak yyAVAX and Ankr ankrAVAX are left out
// until their P-Chain addresses can be verified. GoGoPool/Hypha shut down in
// August 2026.
var Protocols = []Protocol{
	{
		ID:             "benqi-savax",
		Name:           "BENQI",
		TokenSymbol:    "sAVAX",
		TokenContract:  "0x2b2C81e08f1Af8835a78Bb2A90AE924ACE0eA4bE",
		TokenDecimals:  18,
		PooledSelector: "0x629e8056", // totalPooledAvax()
		Status:         "live",
		// Unconfirmed until BENQI confirms. On-chain evidence: the EOA that calls
		// sAVAX withdraw()/deposit() (0x565f0fe9...) has the same key as P-Chain
		// address P-avax1tnuesf6cqwnjw7fxjyk7lhch0vhf0v95wj5jvy, which funds the
		// delegations whose rewards go to the address below.
		Attribution:     "unconfirmed",
		PChainAddresses: []string{"P-avax19zfygxaf59stehzedhxjesads0p5jdvfeedal0"},
	},
}

const totalSupplySelector = "0x18160ddd" // ERC20 totalSupply()

// TokenState is a protocol's on-chain token figures, both in wei.
type TokenState struct {
	PooledAvax  *big.Int
	TokenSupply *big.Int
}

// ReadTokenState reads pooled AVAX and token supply from the C-Chain contract.
func ReadTokenState(ctx context.Context, client *http.Client, rpcURL string, p Protocol) (TokenState, error) {
	pooled, err := callUint(ctx, client, rpcURL, p.TokenContract, p.PooledSelector)
	if err != nil {
		return TokenState{}, fmt.Errorf("%s pooled: %w", p.ID, err)
	}
	supply, err := callUint(ctx, client, rpcURL, p.TokenContract, totalSupplySelector)
	if err != nil {
		return TokenState{}, fmt.Errorf("%s totalSupply: %w", p.ID, err)
	}
	return TokenState{PooledAvax: pooled, TokenSupply: supply}, nil
}

// callUint runs eth_call and decodes the first 32-byte word as an unsigned int.
func callUint(ctx context.Context, client *http.Client, rpcURL, to, data string) (*big.Int, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "eth_call",
		"params": []interface{}{map[string]string{"to": to, "data": data}, "latest"},
	})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
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
	h := strings.TrimPrefix(out.Result, "0x")
	if len(h) < 64 {
		return nil, fmt.Errorf("short eth_call result %q", out.Result)
	}
	n, ok := new(big.Int).SetString(h[:64], 16)
	if !ok {
		return nil, fmt.Errorf("bad eth_call result %q", out.Result)
	}
	return n, nil
}
