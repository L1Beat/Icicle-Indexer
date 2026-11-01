// Package restaking reads restaking protocols that secure Avalanche L1
// validators from C-Chain contracts. Only Suzaku is tracked.
package restaking

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/ava-labs/avalanchego/ids"
	"golang.org/x/crypto/sha3"
)

// SuzakuL1Registry is Suzaku's mainnet L1Registry. Each entry is an L1's
// BalancerValidatorManager (the "l1" key the vault delegators use) and the
// Suzaku middleware that is one of its security modules.
const SuzakuL1Registry = "0xaA59b19A7636bf6d821aA124A14eEE6C92746110"

// Caller runs eth_call against a contract and returns the raw return data.
type Caller interface {
	Call(ctx context.Context, to string, data []byte) ([]byte, error)
}

// Vault is one Suzaku vault registered with an L1's middleware.
type Vault struct {
	Address    string
	Collateral string // the vault's collateral() token, often a DefaultCollateral wrapper
	Token      string // underlying token: the wrapper's asset(), else Collateral
	Symbol     string
	Decimals   uint8
	Deposited  *big.Int // activeStake(), token base units
	Allocated  *big.Int // delegator stake() summed over the L1's operators
	Limit      *big.Int // delegator l1Limit() for this L1
}

// L1 is one Suzaku-secured L1 with at least one active vault.
type L1 struct {
	SubnetID      string // CB58
	Balancer      string
	Middleware    string
	ModuleWeight  uint64 // the middleware's current weight in the balancer
	OperatorCount uint32 // operators with allocated stake in any vault
	ValidationIDs []string
	Vaults        []Vault
}

// RPCCaller is a Caller over a JSON-RPC endpoint (C-Chain).
type RPCCaller struct {
	URL    string
	Client *http.Client
}

func (c RPCCaller) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "eth_call",
		"params": []interface{}{map[string]string{"to": to, "data": "0x" + hex.EncodeToString(data)}, "latest"},
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

// Minimal ABI codec. Arguments are static words (address or uint); returns are
// sliced as 32-byte words, with helpers for the few dynamic types used here.

func selector(sig string) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return h.Sum(nil)[:4]
}

func addrWord(addr string) []byte {
	w := make([]byte, 32)
	a, _ := hex.DecodeString(strings.TrimPrefix(addr, "0x"))
	if len(a) <= 32 {
		copy(w[32-len(a):], a)
	}
	return w
}

func uintWord(n *big.Int) []byte {
	return n.FillBytes(make([]byte, 32))
}

func word(b []byte, i int) []byte {
	if i < 0 || len(b) < (i+1)*32 {
		return nil
	}
	return b[i*32 : (i+1)*32]
}

func wordUint(b []byte, i int) (*big.Int, error) {
	w := word(b, i)
	if w == nil {
		return nil, errors.New("short return data")
	}
	return new(big.Int).SetBytes(w), nil
}

func wordAddr(b []byte, i int) (string, error) {
	w := word(b, i)
	if w == nil {
		return "", errors.New("short return data")
	}
	return hexAddr(w[12:]), nil
}

// hexAddr formats 20 address bytes as lowercase 0x hex.
func hexAddr(a []byte) string { return "0x" + hex.EncodeToString(a) }

const zeroAddress = "0x0000000000000000000000000000000000000000"

// wordInt reads word i as a small non-negative int (offset or length).
func wordInt(b []byte, i int) (int, error) {
	n, err := wordUint(b, i)
	if err != nil {
		return 0, err
	}
	if !n.IsInt64() || n.Int64() > int64(len(b)) {
		return 0, errors.New("bad offset or length")
	}
	return int(n.Int64()), nil
}

// words decodes a dynamic array of 32-byte words starting at byte offset off.
func words(b []byte, off int) ([][]byte, error) {
	if off%32 != 0 {
		return nil, errors.New("unaligned array")
	}
	n, err := wordInt(b, off/32)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, n)
	for i := range out {
		if out[i] = word(b, off/32+1+i); out[i] == nil {
			return nil, errors.New("short array")
		}
	}
	return out, nil
}

// abiString decodes a string whose head is at byte offset off within b.
func abiString(b []byte, off int) (string, error) {
	if off%32 != 0 {
		return "", errors.New("unaligned string")
	}
	n, err := wordInt(b, off/32)
	if err != nil {
		return "", err
	}
	if len(b) < off+32+n {
		return "", errors.New("short string")
	}
	return string(b[off+32 : off+32+n]), nil
}

type reader struct {
	ctx context.Context
	c   Caller
}

// call invokes sig on to with static word arguments.
func (r reader) call(to, sig string, args ...[]byte) ([]byte, error) {
	data := selector(sig)
	for _, a := range args {
		data = append(data, a...)
	}
	out, err := r.c.Call(r.ctx, to, data)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", sig, to, err)
	}
	return out, nil
}

func (r reader) uint(to, sig string, args ...[]byte) (*big.Int, error) {
	out, err := r.call(to, sig, args...)
	if err != nil {
		return nil, err
	}
	n, err := wordUint(out, 0)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", sig, to, err)
	}
	return n, nil
}

func (r reader) address(to, sig string, args ...[]byte) (string, error) {
	out, err := r.call(to, sig, args...)
	if err != nil {
		return "", err
	}
	a, err := wordAddr(out, 0)
	if err != nil {
		return "", fmt.Errorf("%s on %s: %w", sig, to, err)
	}
	return a, nil
}

// array decodes a single address[] or bytes32[] return value.
func (r reader) array(to, sig string, args ...[]byte) ([][]byte, error) {
	out, err := r.call(to, sig, args...)
	if err != nil {
		return nil, err
	}
	off, err := wordInt(out, 0)
	if err == nil {
		var ws [][]byte
		if ws, err = words(out, off); err == nil {
			return ws, nil
		}
	}
	return nil, fmt.Errorf("%s on %s: %w", sig, to, err)
}

// ReadSuzaku walks the L1Registry and returns every L1 with at least one
// active vault. An L1 that fails to read is skipped and its error returned
// alongside the others, so one broken entry never hides the rest.
func ReadSuzaku(ctx context.Context, c Caller) ([]L1, error) {
	r := reader{ctx, c}
	out, err := r.call(SuzakuL1Registry, "getAllL1s()")
	if err != nil {
		return nil, err
	}
	// Returns (address[] l1s, address[] middlewares, string[] metadataURLs).
	var lists [2][][]byte
	for i := range lists {
		off, err := wordInt(out, i)
		if err == nil {
			lists[i], err = words(out, off)
		}
		if err != nil {
			return nil, fmt.Errorf("getAllL1s: %w", err)
		}
	}

	var l1s []L1
	var errs []error
	for i := range lists[0] {
		if i >= len(lists[1]) {
			break
		}
		l1, err := r.readL1(hexAddr(lists[0][i][12:]), hexAddr(lists[1][i][12:]))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(l1.Vaults) > 0 {
			l1s = append(l1s, l1)
		}
	}
	return l1s, errors.Join(errs...)
}

func (r reader) readL1(balancer, middleware string) (L1, error) {
	l1 := L1{Balancer: balancer, Middleware: middleware}

	out, err := r.call(balancer, "subnetID()")
	if err != nil {
		return l1, err
	}
	w := word(out, 0)
	if w == nil {
		return l1, fmt.Errorf("subnetID on %s: short return data", balancer)
	}
	l1.SubnetID = ids.ID(w).String()

	// Returns (uint64 current, uint64 max).
	weight, err := r.uint(balancer, "getSecurityModuleWeights(address)", addrWord(middleware))
	if err != nil {
		return l1, err
	}
	l1.ModuleWeight = weight.Uint64()

	vm, err := r.address(middleware, "getVaultManager()")
	if err != nil {
		return l1, err
	}
	opWords, err := r.array(middleware, "getAllOperators()")
	if err != nil {
		return l1, err
	}
	operators := make([]string, len(opWords))
	for i, w := range opWords {
		operators[i] = hexAddr(w[12:])
		vids, err := r.array(middleware, "getOperatorValidationIDs(address)", addrWord(operators[i]))
		if err != nil {
			return l1, err
		}
		for _, id := range vids {
			l1.ValidationIDs = append(l1.ValidationIDs, ids.ID(id).String())
		}
	}

	n, err := r.uint(vm, "getVaultCount()")
	if err != nil {
		return l1, err
	}
	allocating := map[string]bool{}
	for i := int64(0); i < n.Int64(); i++ {
		// Returns (address vault, uint48 enabledTime, uint48 disabledTime).
		out, err := r.call(vm, "getVaultAtWithTimes(uint256)", uintWord(big.NewInt(i)))
		if err != nil {
			return l1, err
		}
		vault, err := wordAddr(out, 0)
		if err != nil {
			return l1, err
		}
		disabled, err := wordUint(out, 2)
		if err != nil {
			return l1, err
		}
		if disabled.Sign() != 0 {
			continue
		}
		v, err := r.readVault(vm, balancer, vault, operators, allocating)
		if err != nil {
			return l1, err
		}
		l1.Vaults = append(l1.Vaults, v)
	}
	l1.OperatorCount = uint32(len(allocating))
	return l1, nil
}

func (r reader) readVault(vm, balancer, addr string, operators []string, allocating map[string]bool) (Vault, error) {
	vt := Vault{Address: addr, Allocated: new(big.Int)}

	class, err := r.uint(vm, "getVaultCollateralClass(address)", addrWord(addr))
	if err != nil {
		return vt, err
	}
	if vt.Collateral, err = r.address(addr, "collateral()"); err != nil {
		return vt, err
	}
	if vt.Deposited, err = r.uint(addr, "activeStake()"); err != nil {
		return vt, err
	}
	del, err := r.address(addr, "delegator()")
	if err != nil {
		return vt, err
	}
	if vt.Limit, err = r.uint(del, "l1Limit(address,uint96)", addrWord(balancer), uintWord(class)); err != nil {
		return vt, err
	}
	for _, op := range operators {
		s, err := r.uint(del, "stake(address,uint96,address)", addrWord(balancer), uintWord(class), addrWord(op))
		if err != nil {
			return vt, err
		}
		if s.Sign() > 0 {
			vt.Allocated.Add(vt.Allocated, s)
			allocating[op] = true
		}
	}

	// A DefaultCollateral wrapper is 1:1 with its asset(); a plain ERC20
	// collateral reverts on asset() and is its own token.
	vt.Token = vt.Collateral
	if a, err := r.address(vt.Collateral, "asset()"); err == nil && a != zeroAddress {
		vt.Token = a
	}
	out, err := r.call(vt.Token, "symbol()")
	if err != nil {
		return vt, err
	}
	off, err := wordInt(out, 0)
	if err == nil {
		vt.Symbol, err = abiString(out, off)
	}
	if err != nil {
		return vt, fmt.Errorf("symbol on %s: %w", vt.Token, err)
	}
	dec, err := r.uint(vt.Token, "decimals()")
	if err != nil {
		return vt, err
	}
	vt.Decimals = uint8(dec.Uint64())
	return vt, nil
}
