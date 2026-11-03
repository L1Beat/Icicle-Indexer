package icm

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

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

// App kinds.
const (
	KindICTTHome   = "ictt-home"   // TokenHome: locks the token on the C-Chain
	KindICTTRemote = "ictt-remote" // TokenRemote: the token on the C-Chain is its remote
)

// cChainBlockchainID is the C-Chain's blockchain ID
// (2q9e4r6Mu3U68nU1fYjgbR6JvwrRx36CohpAX5UQxse55x1Q5).
var cChainBlockchainID, _ = hex.DecodeString("0427d4b22a2a78bcddd456742caf91b56badbff985ee19aef14573e7343fd652")

// App is a C-Chain contract behind ICTT-shaped messages, as checked on-chain.
type App struct {
	Address  string // lowercase hex, no 0x
	Kind     string // KindICTTHome, KindICTTRemote, or "" when not ICTT
	Token    string // lowercase hex, no 0x: the remote itself, or the home's token address
	Symbol   string
	Decimals uint8
	Note     string
}

// discover checks the C-Chain contracts behind the window's new ICTT-shaped
// messages and records them in icm_apps. A transport failure aborts, so the
// window is retried with the checks complete.
func discover(ctx context.Context, conn driver.Conn, caller Caller, from, to time.Time) error {
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT lower(hex(local_address))
		FROM icm_messages
		WHERE block_time >= %s AND block_time < %s AND ictt_type >= 0
		  AND local_address NOT IN (SELECT address FROM icm_apps FINAL)
	`, timeLit(from), timeLit(to)))
	if err != nil {
		return err
	}
	var addrs []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return err
		}
		addrs = append(addrs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(addrs) == 0 {
		return err
	}
	var apps []App
	for _, a := range addrs {
		app, err := checkApp(ctx, caller, a)
		if err != nil {
			return fmt.Errorf("check %s: %w", a, err)
		}
		apps = append(apps, app)
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO icm_apps (address, kind, token, symbol, decimals, note)`)
	if err != nil {
		return err
	}
	defer func() { _ = batch.Abort() }()
	for _, app := range apps {
		addr, _ := hex.DecodeString(app.Address)
		token, _ := hex.DecodeString(app.Token)
		if len(token) != 20 {
			token = make([]byte, 20)
		}
		if err := batch.Append(string(addr), app.Kind, string(token), app.Symbol, app.Decimals, app.Note); err != nil {
			return err
		}
		slog.Info("ICM app checked", "address", "0x"+app.Address, "kind", app.Kind, "symbol", app.Symbol, "note", app.Note)
	}
	return batch.Send()
}

// checkApp tells an ICTT TokenHome or TokenRemote on the C-Chain from any other
// contract by its views: both report the C-Chain as their blockchain ID, a
// remote names its home, and a home names its token. ICTT v1 exposes these as
// public variables (blockchainID(), tokenHomeAddress(), tokenAddress()), later
// versions as getters (getBlockchainID(), ...), so both are tried.
func checkApp(ctx context.Context, caller Caller, addr string) (App, error) {
	app := App{Address: addr}
	id, fn, err := callEither(ctx, caller, addr, "getBlockchainID()", "blockchainID()")
	if err != nil {
		return app, err
	}
	if len(id) < 32 || !bytes.Equal(id[:32], cChainBlockchainID) {
		app.Note = "blockchain ID is not the C-Chain"
		return app, nil
	}
	home, homeFn, err := callEither(ctx, caller, addr, "getTokenHomeAddress()", "tokenHomeAddress()")
	if err != nil {
		return app, err
	}
	if len(home) >= 32 && new(big.Int).SetBytes(home[:32]).Sign() != 0 {
		app.Kind, app.Token = KindICTTRemote, addr
		app.Note = fn + ", " + homeFn + " = 0x" + hex.EncodeToString(home[12:32])
	} else {
		token, tokenFn, err := callEither(ctx, caller, addr, "getTokenAddress()", "tokenAddress()")
		if err != nil {
			return app, err
		}
		if len(token) < 32 || new(big.Int).SetBytes(token[:32]).Sign() == 0 {
			app.Note = fn + ", but no token home or token address"
			return app, nil
		}
		app.Kind, app.Token = KindICTTHome, hex.EncodeToString(token[12:32])
		app.Note = fn + ", " + tokenFn
	}
	if app.Symbol, err = callSymbol(ctx, caller, app.Token); err != nil {
		return app, err
	}
	dec, err := call(ctx, caller, app.Token, "decimals()")
	if err != nil {
		return app, err
	}
	if len(dec) >= 32 && new(big.Int).SetBytes(dec[:32]).Cmp(big.NewInt(255)) <= 0 {
		app.Decimals = uint8(dec[31])
	}
	return app, nil
}

// callEither returns the first of fns that answers, and which one did.
func callEither(ctx context.Context, caller Caller, to string, fns ...string) ([]byte, string, error) {
	for _, fn := range fns {
		b, err := call(ctx, caller, to, fn)
		if err != nil {
			return nil, "", err
		}
		if len(b) >= 32 {
			return b, fn, nil
		}
	}
	return nil, "", nil
}

// call runs fn on to with 32-byte word arguments. A node error (revert, no
// such function) is an empty result, a transport error is returned.
func call(ctx context.Context, caller Caller, to, fn string, args ...[]byte) ([]byte, error) {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(fn))
	data := h.Sum(nil)[:4]
	for _, a := range args {
		w := make([]byte, 32)
		copy(w[32-len(a):], a)
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

// callSymbol reads symbol() as an ABI string, or a bytes32 for old tokens.
func callSymbol(ctx context.Context, caller Caller, token string) (string, error) {
	b, err := call(ctx, caller, token, "symbol()")
	if err != nil || len(b) < 32 {
		return "", err
	}
	if len(b) >= 64 {
		if off, err := offset(b, 0); err == nil && off == 32 {
			if n, err := offset(b, 32); err == nil && 64+n <= uint64(len(b)) {
				if s := string(b[64 : 64+n]); utf8.ValidString(s) {
					return s, nil
				}
			}
		}
	}
	s := string(bytes.TrimRight(b[:32], "\x00"))
	if !utf8.ValidString(s) {
		return "", nil
	}
	return s, nil
}

// Remote is a TokenRemote registered with a C-Chain TokenHome, as the home
// reports it. ICTT messages carry amounts in the remote's denomination, so the
// home's amount is the message amount divided by Multiplier when
// MultiplyOnRemote, else multiplied by it (TokenScalingUtils.removeTokenScale).
type Remote struct {
	Home             string // lowercase hex, no 0x
	Chain            []byte // 32-byte blockchain ID
	Address          string // lowercase hex, no 0x
	Registered       bool
	Multiplier       *big.Int
	MultiplyOnRemote bool
}

// discoverRemotes reads the settings of the remotes the window's ICTT transfers
// at C-Chain homes went to or came from, and records them in icm_remotes.
func discoverRemotes(ctx context.Context, conn driver.Conn, caller Caller, from, to time.Time) error {
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT lower(hex(local_address)), counterparty, lower(hex(remote_address))
		FROM icm_messages
		WHERE block_time >= %s AND block_time < %s AND ictt_type BETWEEN %d AND %d
		  AND local_address IN (SELECT address FROM icm_apps FINAL WHERE kind = '%s')
		  AND (local_address, counterparty, remote_address) NOT IN
			(SELECT home, remote_chain, remote_address FROM icm_remotes FINAL)
	`, timeLit(from), timeLit(to), ICTTSingleHopSend, ICTTMultiHopCall, KindICTTHome))
	if err != nil {
		return err
	}
	var remotes []Remote
	for rows.Next() {
		var r Remote
		var chain string
		if err := rows.Scan(&r.Home, &chain, &r.Address); err != nil {
			rows.Close()
			return err
		}
		r.Chain = []byte(chain)
		remotes = append(remotes, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(remotes) == 0 {
		return err
	}
	for i := range remotes {
		if err := checkRemote(ctx, caller, &remotes[i]); err != nil {
			return fmt.Errorf("remote settings %s: %w", remotes[i].Home, err)
		}
	}
	wctx, cancel := chwrapper.WriteContext(ctx)
	defer cancel()
	batch, err := conn.PrepareBatch(wctx, `INSERT INTO icm_remotes (home, remote_chain, remote_address, registered, multiplier, multiply_on_remote)`)
	if err != nil {
		return err
	}
	defer func() { _ = batch.Abort() }()
	for _, r := range remotes {
		home, _ := hex.DecodeString(r.Home)
		addr, _ := hex.DecodeString(r.Address)
		if err := batch.Append(string(home), string(r.Chain), string(addr), r.Registered, r.Multiplier, r.MultiplyOnRemote); err != nil {
			return err
		}
	}
	slog.Info("ICM remotes checked", "count", len(remotes))
	return batch.Send()
}

// checkRemote reads a remote's RemoteTokenTransferrerSettings (registered,
// collateralNeeded, tokenMultiplier, multiplyOnRemote) from its home:
// getRemoteTokenTransferrerSettings on current ICTT, the public
// registeredRemotes mapping on ICTT v1. Both return the four words inline.
func checkRemote(ctx context.Context, caller Caller, r *Remote) error {
	r.Multiplier = new(big.Int)
	addr, _ := hex.DecodeString(r.Address)
	for _, fn := range []string{"getRemoteTokenTransferrerSettings(bytes32,address)", "registeredRemotes(bytes32,address)"} {
		b, err := call(ctx, caller, r.Home, fn, r.Chain, addr)
		if err != nil {
			return err
		}
		if len(b) >= 128 {
			r.Registered = b[31] == 1
			r.Multiplier.SetBytes(b[64:96])
			r.MultiplyOnRemote = b[127] == 1
			return nil
		}
	}
	return nil
}
