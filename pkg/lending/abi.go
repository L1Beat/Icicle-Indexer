package lending

import (
	"encoding/hex"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Minimal ABI helpers. The repo decodes contract returns by slicing 32-byte
// words (see registrysyncer), and that style is kept here. Event topics and
// function selectors are derived with keccak256 at runtime so signatures are
// self-documenting and there is no 32-byte hash to transcribe by hand.

func keccak256(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	return h.Sum(nil)
}

// EventTopic returns the 0x-prefixed topic0 for an event signature, for example
// "Borrow(address,address,address,uint256,uint8,uint256,uint16)".
func EventTopic(sig string) string {
	return "0x" + hex.EncodeToString(keccak256([]byte(sig)))
}

// Selector returns the 0x-prefixed 4-byte function selector for a signature,
// for example "getUserAccountData(address)".
func Selector(sig string) string {
	return "0x" + hex.EncodeToString(keccak256([]byte(sig))[:4])
}

// decodeHex decodes a hex string with or without a 0x prefix. Invalid input
// yields a nil slice rather than an error, matching the best-effort decode style
// used across the contract-read paths.
func decodeHex(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// wordBig returns the i-th 32-byte word as a big.Int, or 0 if out of range.
func wordBig(b []byte, i int) *big.Int {
	start := i * 32
	if len(b) < start+32 {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(b[start : start+32])
}

// wordU64 returns the low 64 bits of the i-th word.
func wordU64(b []byte, i int) uint64 {
	return wordBig(b, i).Uint64()
}

// wordBool reports whether the i-th word is non-zero.
func wordBool(b []byte, i int) bool {
	return wordBig(b, i).Sign() != 0
}

// addrFromWord decodes the low 20 bytes of the i-th word as a 0x address.
func addrFromWord(b []byte, i int) string {
	start := i * 32
	if len(b) < start+32 {
		return ZeroAddress
	}
	return "0x" + hex.EncodeToString(b[start+12:start+32])
}

// leftPadAddress encodes a 0x address into a left-padded 32-byte word.
func leftPadAddress(addr string) []byte {
	a := decodeHex(addr)
	w := make([]byte, 32)
	if len(a) <= 20 {
		copy(w[32-len(a):], a)
	} else {
		copy(w, a[len(a)-32:])
	}
	return w
}

// u256Word encodes a non-negative integer into a 32-byte big-endian word.
func u256Word(n *big.Int) []byte {
	w := make([]byte, 32)
	if n != nil && n.Sign() > 0 {
		nb := n.Bytes()
		if len(nb) <= 32 {
			copy(w[32-len(nb):], nb)
		} else {
			copy(w, nb[len(nb)-32:])
		}
	}
	return w
}

// boolWord encodes a bool into a 32-byte word.
func boolWord(v bool) []byte {
	w := make([]byte, 32)
	if v {
		w[31] = 1
	}
	return w
}

// rightPad32 pads a byte slice up to a 32-byte boundary.
func rightPad32(b []byte) []byte {
	r := len(b) % 32
	if r == 0 {
		return b
	}
	return append(b, make([]byte, 32-r)...)
}

// normalizeAddress lowercases and 0x-prefixes a hex address, returning the zero
// address for empty input.
func normalizeAddress(addr string) string {
	a := strings.ToLower(strings.TrimSpace(addr))
	if a == "" {
		return ZeroAddress
	}
	if !strings.HasPrefix(a, "0x") {
		a = "0x" + a
	}
	return a
}

// encodeCallAddress builds calldata for a function taking a single address arg.
func encodeCallAddress(sig, addr string) string {
	return Selector(sig) + hex.EncodeToString(leftPadAddress(addr))
}

// encodeCall builds calldata for a function with no arguments.
func encodeCall(sig string) string {
	return Selector(sig)
}

// Exported ABI helpers for protocol adapter packages.

// Word returns the i-th 32-byte word of decoded return data as a big.Int.
func Word(b []byte, i int) *big.Int { return wordBig(b, i) }

// WordU64 returns the low 64 bits of the i-th word.
func WordU64(b []byte, i int) uint64 { return wordU64(b, i) }

// WordBool reports whether the i-th word is non-zero.
func WordBool(b []byte, i int) bool { return wordBool(b, i) }

// Addr decodes the i-th word as a 0x address.
func Addr(b []byte, i int) string { return addrFromWord(b, i) }

// DecodeHexBytes decodes a hex string (with or without 0x) to bytes.
func DecodeHexBytes(s string) []byte { return decodeHex(s) }

// NormalizeAddr lowercases and 0x-prefixes an address.
func NormalizeAddr(a string) string { return normalizeAddress(a) }

// AddrFromTopic decodes an indexed-address event topic into a 0x address.
func AddrFromTopic(topic string) string {
	b := decodeHex(topic)
	if len(b) < 20 {
		return ZeroAddress
	}
	return "0x" + hex.EncodeToString(b[len(b)-20:])
}

// EncodeCall0 builds calldata for a no-argument function.
func EncodeCall0(sig string) string { return encodeCall(sig) }

// EncodeCall1Addr builds calldata for a function taking one address argument.
func EncodeCall1Addr(sig, addr string) string { return encodeCallAddress(sig, addr) }

// EncodeCall2Addr builds calldata for a function taking two address arguments.
func EncodeCall2Addr(sig, a, b string) string {
	return Selector(sig) + hex.EncodeToString(leftPadAddress(a)) + hex.EncodeToString(leftPadAddress(b))
}

// EncodeCall1AddrArray builds calldata for a function taking one address[] arg.
func EncodeCall1AddrArray(sig string, addrs []string) string {
	var b []byte
	b = append(b, u256Word(big.NewInt(0x20))...)
	b = append(b, u256Word(big.NewInt(int64(len(addrs))))...)
	for _, a := range addrs {
		b = append(b, leftPadAddress(a)...)
	}
	return Selector(sig) + hex.EncodeToString(b)
}

// DecodeUintArray decodes an ABI uint256[] return value.
func DecodeUintArray(b []byte) []*big.Int {
	if len(b) < 64 {
		return nil
	}
	arrBase := int(wordBig(b, 0).Uint64())
	if arrBase+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[arrBase : arrBase+32]).Uint64())
	out := make([]*big.Int, 0, n)
	for i := 0; i < n; i++ {
		off := arrBase + 32 + i*32
		if off+32 > len(b) {
			break
		}
		out = append(out, new(big.Int).SetBytes(b[off:off+32]))
	}
	return out
}
