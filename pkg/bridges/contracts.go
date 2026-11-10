package bridges

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"icicle/pkg/chwrapper"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"golang.org/x/crypto/sha3"
)

// Caller runs eth_call against a contract and returns the raw return data.
// Errors that start with "rpc error:" are the node's answer (a revert, or no
// such function) and other errors are transport failures.
type Caller interface {
	Call(ctx context.Context, to string, data []byte) ([]byte, error)
}

// Contract is one emitter of a generic bridge event, as checked on-chain.
type Contract struct {
	Address string // lowercase hex, no 0x
	Bridge  string
	Token   string // lowercase hex, no 0x
	Valid   bool
	Note    string
}

// kinds of generic events, by topic.
var eventKinds = map[string]string{
	topicOFTSent:              "lz",
	topicOFTReceived:          "lz",
	topicLZ1Send:              "lz",
	topicLZ1SendBytes:         "lz",
	topicLZ1Receive:           "lz",
	topicLZ1ReceiveBytes:      "lz",
	topicCCIPLockedOrBurned:   "ccip",
	topicCCIPReleasedOrMinted: "ccip",
	topicABMint:               "ab",
	topicABUnwrap:             "ab",
}

// discover checks the window's new emitters of generic bridge events and
// records them in bridge_contracts. A transport failure aborts, so the window
// is not decoded with contracts missing.
func discover(ctx context.Context, conn driver.Conn, caller Caller, from, to time.Time) error {
	var topics []string
	for t := range eventKinds {
		topics = append(topics, t)
	}
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT lower(hex(address)), lower(hex(topic0))
		FROM bridge_logs FINAL
		WHERE block_time >= %s AND block_time < %s AND topic0 IN (%s)
		  AND address NOT IN (SELECT address FROM bridge_contracts FINAL)
		  AND address != unhex('%s')
	`, timeLit(from), timeLit(to), unhexList(topics...), BTCb))
	if err != nil {
		return err
	}
	kinds := map[string]string{}
	for rows.Next() {
		var addr, topic string
		if err := rows.Scan(&addr, &topic); err != nil {
			rows.Close()
			return err
		}
		kinds[addr] = eventKinds[topic]
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(kinds) == 0 {
		return nil
	}

	var found []Contract
	for addr, kind := range kinds {
		var c Contract
		var err error
		switch kind {
		case "lz":
			c, err = checkLayerZero(ctx, caller, addr)
		case "ccip":
			c, err = checkCCIPPool(ctx, caller, addr)
		case "ab":
			c, err = checkBridgeToken(ctx, caller, addr)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", addr, err)
		}
		found = append(found, c)
	}

	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO bridge_contracts (address, bridge, token, valid, note)`)
	if err != nil {
		return err
	}
	valid := 0
	for _, c := range found {
		a, _ := hex.DecodeString(c.Address)
		t, _ := hex.DecodeString(c.Token)
		if err := batch.Append(string(a), c.Bridge, string(t), c.Valid, c.Note); err != nil {
			return err
		}
		if c.Valid {
			valid++
		}
	}
	if err := batch.Send(); err != nil {
		return err
	}
	slog.Info("Bridge contracts checked", "new", len(found), "valid", valid)
	return nil
}

// checkLayerZero accepts an OFT whose endpoint is LayerZero's: endpoint() for
// V2 OApps, lzEndpoint() for V1. The token is token(), else innerToken() (V1
// ProxyOFTV2), else the contract itself.
func checkLayerZero(ctx context.Context, caller Caller, addr string) (Contract, error) {
	c := Contract{Address: addr, Bridge: "layerzero", Token: addr}
	if stargatePools[addr] {
		c.Bridge = "stargate"
	}
	ep, err := callAddress(ctx, caller, addr, "endpoint()")
	if err != nil {
		return c, err
	}
	switch ep {
	case lzEndpointV2:
		c.Valid, c.Note = true, "endpoint() is EndpointV2"
	default:
		ep1, err := callAddress(ctx, caller, addr, "lzEndpoint()")
		if err != nil {
			return c, err
		}
		if ep1 == lzEndpointV1 {
			c.Valid, c.Note = true, "lzEndpoint() is Endpoint V1"
		} else {
			c.Note = "endpoint() and lzEndpoint() are not LayerZero's"
		}
	}
	for _, sig := range []string{"token()", "innerToken()"} {
		t, err := callAddress(ctx, caller, addr, sig)
		if err != nil {
			return c, err
		}
		if t != "" && t != zeroHex20 {
			c.Token = t
			break
		}
	}
	return c, nil
}

// checkCCIPPool accepts a pool the CCIP TokenAdminRegistry lists for its
// token, directly or as one of the pools behind the registered USDC pool
// proxy (getPools()).
func checkCCIPPool(ctx context.Context, caller Caller, addr string) (Contract, error) {
	c := Contract{Address: addr, Bridge: "ccip", Token: zeroHex20}
	tok, err := callAddress(ctx, caller, addr, "getToken()")
	if err != nil {
		return c, err
	}
	if tok == "" || tok == zeroHex20 {
		c.Note = "no getToken()"
		return c, nil
	}
	c.Token = tok
	reg, err := callAddress(ctx, caller, ccipTokenAdminRegistry, "getPool(address)", tok)
	if err != nil {
		return c, err
	}
	if reg == addr {
		c.Valid, c.Note = true, "TokenAdminRegistry pool for its token"
		return c, nil
	}
	if reg != "" && reg != zeroHex20 {
		b, err := call(ctx, caller, reg, "getPools()")
		if err != nil {
			return c, err
		}
		for i := 0; i+32 <= len(b) && i < 32*8; i += 32 {
			if hex.EncodeToString(b[i+12:i+32]) == addr {
				c.Valid, c.Note = true, "behind the registry's pool proxy for its token"
				return c, nil
			}
		}
	}
	c.Note = "not the registry's pool for its token"
	return c, nil
}

// checkBridgeToken accepts an Avalanche Bridge BridgeToken: a ".e" symbol and
// chainIds(0) set, which its constructor does.
func checkBridgeToken(ctx context.Context, caller Caller, addr string) (Contract, error) {
	c := Contract{Address: addr, Bridge: "avalanche-bridge", Token: addr}
	sym, err := callString(ctx, caller, addr, "symbol()")
	if err != nil {
		return c, err
	}
	b, err := call(ctx, caller, addr, "chainIds(uint256)", zeroHex20)
	if err != nil {
		return c, err
	}
	home := len(b) >= 32 && b[31] == 1
	if strings.HasSuffix(sym, ".e") && home {
		c.Valid, c.Note = true, "BridgeToken "+sym
	} else {
		c.Note = fmt.Sprintf("not a BridgeToken (symbol %q, chainIds(0) %v)", sym, home)
	}
	return c, nil
}

var zeroHex20 = strings.Repeat("00", 20)

// call runs fn on to with static address or uint arguments given as hex. A
// revert or empty return is nil data, not an error.
func call(ctx context.Context, caller Caller, to, fn string, args ...string) ([]byte, error) {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(fn))
	data := h.Sum(nil)[:4]
	for _, a := range args {
		w := make([]byte, 32)
		b, _ := hex.DecodeString(strings.TrimPrefix(a, "0x"))
		copy(w[32-len(b):], b)
		data = append(data, w...)
	}
	b, err := caller.Call(ctx, "0x"+to, data)
	if err != nil {
		if strings.HasPrefix(err.Error(), "rpc error:") {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// callAddress returns fn's address result as lowercase hex, empty when the
// call reverts or returns nothing.
func callAddress(ctx context.Context, caller Caller, to, fn string, args ...string) (string, error) {
	b, err := call(ctx, caller, to, fn, args...)
	if err != nil || len(b) < 32 {
		return "", err
	}
	return hex.EncodeToString(b[12:32]), nil
}

// callString returns fn's string result, empty when there is none.
func callString(ctx context.Context, caller Caller, to, fn string) (string, error) {
	b, err := call(ctx, caller, to, fn)
	if err != nil || len(b) < 64 {
		return "", err
	}
	off := new(big.Int).SetBytes(b[:32])
	if !off.IsUint64() || off.Uint64() > uint64(len(b)-32) {
		return "", nil
	}
	o := int(off.Uint64())
	n := new(big.Int).SetBytes(b[o : o+32])
	// Compare against the bytes left after the length word; adding n to the
	// offset first can wrap uint64 and pass the check with a huge n.
	if !n.IsUint64() || n.Uint64() > uint64(len(b)-o-32) {
		return "", nil
	}
	return string(b[o+32 : o+32+int(n.Uint64())]), nil
}
