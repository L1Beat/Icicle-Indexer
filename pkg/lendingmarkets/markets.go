// Package lendingmarkets reads market-level supply, borrow, rates and risk
// parameters of the C-Chain lending protocols (Aave V3, Aave V4, BENQI) from
// their contracts. It reads markets, not user positions: positions and
// liquidation risk live in pkg/lending.
package lendingmarkets

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Protocol IDs, shared with pkg/lending where they overlap.
const (
	ProtocolAaveV3 = "aave-v3"
	ProtocolAaveV4 = "aave-v4"
	ProtocolBenqi  = "benqi"
)

// Verified mainnet anchors (2026-11-01). Aave V3 resolves its data provider and
// oracle from the addresses provider; BENQI its markets and oracle from the
// comptroller; Aave V4 its assets from the hub and prices from the spokes.
const (
	AaveV3Provider   = "0xa97684ead0e402dC232d5A977953DF7ECBaB3CDb"
	BenqiComptroller = "0x486Af39519B4Dc9a7fCcd318217352830E8AD9b4"
	AaveV4CoreHub    = "0xd07369fAE4A5BB13c9Ce446B052c7867B1AbDf6e"
)

// AaveV4Spokes are the Core Hub's user-facing spokes (bgd-labs
// aave-address-book AaveV4Avalanche). Each lists a subset of the hub's assets
// and prices them through its own oracle.
var AaveV4Spokes = []string{
	"0x435272CefF93a1E657E8ABfdf0A13e95900A3a56", // main
	"0x3b517594277c67307CF2d7CBE6FE1D4399B68c41", // avax correlated
	"0xaf22446124e566e54845f65b722E4dCbf12b650A", // ethena ecosystem
	"0x6a37776B5E026dBdF043b4F933c323C84DD1B514", // forex
}

// NativeAVAX is the asset address used for markets holding native AVAX
// (BENQI qiAVAX), which have no underlying token.
const NativeAVAX = "0x0000000000000000000000000000000000000000"

// Well-known assets for the AVAX and sAVAX summaries.
const (
	WAVAX = "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"
	SAVAX = "0x2b2c81e08f1af8835a78bb2a90ae924ace0ea4be"
)

const secondsPerYear = 365 * 24 * 60 * 60

// Caller runs eth_call against a contract and returns the raw return data.
type Caller interface {
	Call(ctx context.Context, to string, data []byte) ([]byte, error)
}

// Market is one protocol x asset market.
type Market struct {
	Protocol string
	Market   string // Aave V3 Pool, BENQI qiToken, Aave V4 Hub
	Asset    string // underlying token, lowercase. NativeAVAX for native AVAX
	Symbol   string
	Decimals uint8
	Supplied *big.Int // token base units
	Borrowed *big.Int // token base units
	// SupplyAPR and BorrowAPR are simple annual rates as fractions (0.03 = 3%),
	// the per-second rate times seconds per year. Borrow is the variable rate.
	SupplyAPR float64
	BorrowAPR float64
	// Collateral parameters. Nil where the protocol does not set them per
	// market (Aave V4 sets them per spoke).
	CollateralEnabled *bool
	LTVBps            *uint16
	LiqThresholdBps   *uint16
	PriceUSD          *float64 // protocol oracle, nil when the read failed
	SupplyCap         *big.Int // token base units, nil when uncapped
	BorrowCap         *big.Int // token base units, nil when uncapped
}

// ReadAll reads every protocol. A protocol that fails is left out and its error
// joined into the returned error, so one broken protocol does not hide the rest.
func ReadAll(ctx context.Context, c Caller) ([]Market, error) {
	var out []Market
	var errs []error
	for _, p := range []struct {
		id   string
		read func(context.Context, Caller) ([]Market, error)
	}{
		{ProtocolAaveV3, ReadAaveV3},
		{ProtocolAaveV4, ReadAaveV4},
		{ProtocolBenqi, ReadBenqi},
	} {
		ms, err := p.read(ctx, c)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.id, err))
			continue
		}
		out = append(out, ms...)
	}
	return out, errors.Join(errs...)
}

// Minimal ABI codec. Arguments are static words; returns are sliced as words.

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

func uintWord(n uint64) []byte {
	return new(big.Int).SetUint64(n).FillBytes(make([]byte, 32))
}

func encode(sig string, args ...[]byte) []byte {
	out := selector(sig)
	for _, a := range args {
		out = append(out, a...)
	}
	return out
}

type reader struct {
	ctx context.Context
	c   Caller
}

// call runs one eth_call and requires at least n return words.
func (r reader) call(to string, n int, sig string, args ...[]byte) ([]byte, error) {
	b, err := r.c.Call(r.ctx, to, encode(sig, args...))
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", sig, to, err)
	}
	if len(b) < n*32 {
		return nil, fmt.Errorf("%s on %s: short return data (%d bytes)", sig, to, len(b))
	}
	return b, nil
}

func (r reader) uint(to, sig string, args ...[]byte) (*big.Int, error) {
	b, err := r.call(to, 1, sig, args...)
	if err != nil {
		return nil, err
	}
	return wordUint(b, 0), nil
}

func (r reader) address(to, sig string, args ...[]byte) (string, error) {
	b, err := r.call(to, 1, sig, args...)
	if err != nil {
		return "", err
	}
	return wordAddr(b, 0), nil
}

// token reads an ERC-20's symbol and decimals.
func (r reader) token(addr string) (string, uint8, error) {
	b, err := r.call(addr, 1, "symbol()")
	if err != nil {
		return "", 0, err
	}
	d, err := r.uint(addr, "decimals()")
	if err != nil {
		return "", 0, err
	}
	return abiString(b), uint8(d.Uint64()), nil
}

func wordUint(b []byte, i int) *big.Int {
	if len(b) < (i+1)*32 {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(b[i*32 : (i+1)*32])
}

func wordAddr(b []byte, i int) string {
	if len(b) < (i+1)*32 {
		return NativeAVAX
	}
	return "0x" + hex.EncodeToString(b[i*32+12:(i+1)*32])
}

func wordBool(b []byte, i int) bool { return wordUint(b, i).Sign() != 0 }

// abiString decodes a dynamic string return, falling back to a bytes32 one.
func abiString(b []byte) string {
	if len(b) >= 64 {
		off := wordUint(b, 0)
		if off.IsInt64() && off.Int64()+32 <= int64(len(b)) {
			o := int(off.Int64())
			n := new(big.Int).SetBytes(b[o : o+32])
			if n.IsInt64() && o+32+int(n.Int64()) <= len(b) {
				return string(b[o+32 : o+32+int(n.Int64())])
			}
		}
	}
	if len(b) >= 32 {
		return strings.TrimRight(string(b[:32]), "\x00")
	}
	return ""
}

// rateAPR converts a per-second rate scaled by 10^scale to an annual fraction.
func rateAPR(rate *big.Int, scale int) float64 {
	f, _ := new(big.Float).SetInt(rate).Float64()
	return f * secondsPerYear / pow10f(scale)
}

// ratioFloat returns num/10^scale as a float.
func ratioFloat(num *big.Int, scale int) float64 {
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(num), new(big.Float).SetFloat64(pow10f(scale))).Float64()
	return f
}

func pow10f(n int) float64 {
	f, _ := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)).Float64()
	return f
}

func pow10(n uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func ptr[T any](v T) *T { return &v }
