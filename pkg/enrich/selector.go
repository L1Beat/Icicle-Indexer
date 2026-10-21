// Package enrich turns raw transaction bytes into human-readable context:
// 4-byte selectors into function signatures, addresses into labels, and the
// combination into a coarse "intent". Everything here is pure or backed by
// ClickHouse tables that the indexer fills in the background; the API never
// calls a third-party service on the request path.
package enrich

import (
	"encoding/hex"
	"sort"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Signature source priorities. Lower wins when several signatures collide on
// the same selector (collisions are common and some are deliberate spam that
// mimics transfer/approve).
const (
	PrioritySeed              uint8 = 0 // bundled seed list, hand-checked
	PriorityOpenChainVerified uint8 = 1 // OpenChain, seen in a verified contract
	PriorityOpenChain         uint8 = 2 // OpenChain, not filtered as junk
)

// Method is the resolved function behind a transaction's calldata.
type Method struct {
	Selector  string `json:"selector" example:"0xa9059cbb"`
	Name      string `json:"name" example:"transfer"`
	Signature string `json:"signature" example:"transfer(address,uint256)"`
}

// Candidate is one known signature for a selector.
type Candidate struct {
	Signature string
	Priority  uint8
}

// Keccak256 returns the keccak-256 hash of b.
func Keccak256(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	return h.Sum(nil)
}

// SelectorOf returns the 4-byte selector for a canonical signature such as
// "transfer(address,uint256)".
func SelectorOf(signature string) [4]byte {
	var sel [4]byte
	copy(sel[:], Keccak256([]byte(signature))[:4])
	return sel
}

// SelectorHex formats a selector as 0x-prefixed lowercase hex.
func SelectorHex(sel [4]byte) string {
	return "0x" + hex.EncodeToString(sel[:])
}

// FunctionName returns the part of a signature before the first '('.
func FunctionName(signature string) string {
	if i := strings.IndexByte(signature, '('); i >= 0 {
		return signature[:i]
	}
	return signature
}

// ChooseSignature picks the best signature for a selector given the full
// calldata length (selector included). Candidates whose ABI head layout is
// consistent with the calldata length are preferred, then lower priority,
// then shorter signature. ok is false only when there are no candidates.
func ChooseSignature(cands []Candidate, calldataLen int) (string, bool) {
	if len(cands) == 0 {
		return "", false
	}
	type scored struct {
		c         Candidate
		plausible bool
	}
	scoredCands := make([]scored, 0, len(cands))
	for _, c := range cands {
		if c.Signature == "" {
			continue
		}
		scoredCands = append(scoredCands, scored{c: c, plausible: Plausible(c.Signature, calldataLen)})
	}
	if len(scoredCands) == 0 {
		return "", false
	}
	sort.SliceStable(scoredCands, func(i, j int) bool {
		a, b := scoredCands[i], scoredCands[j]
		if a.plausible != b.plausible {
			return a.plausible
		}
		if a.c.Priority != b.c.Priority {
			return a.c.Priority < b.c.Priority
		}
		if len(a.c.Signature) != len(b.c.Signature) {
			return len(a.c.Signature) < len(b.c.Signature)
		}
		return a.c.Signature < b.c.Signature
	})
	return scoredCands[0].c.Signature, true
}

// Plausible reports whether calldata of the given total length (including the
// 4-byte selector) could be an ABI encoding of the signature's parameters.
// Static-only parameter lists must match the head size exactly; lists with
// dynamic types need at least the head. Unparseable signatures are plausible
// so an odd-looking but genuine entry is never discarded.
func Plausible(signature string, calldataLen int) bool {
	params, ok := SplitParams(signature)
	if !ok {
		return true
	}
	argBytes := calldataLen - 4
	if argBytes < 0 {
		return false
	}
	head := 0
	dynamic := false
	for _, p := range params {
		w, dyn, ok := headWords(p)
		if !ok {
			return true
		}
		head += w
		dynamic = dynamic || dyn
	}
	if !dynamic {
		return argBytes == head*32
	}
	return argBytes >= head*32
}

// SplitParams returns the top-level parameter types of a signature, handling
// nested tuples. ok is false when the signature is not of the form name(...).
func SplitParams(signature string) ([]string, bool) {
	open := strings.IndexByte(signature, '(')
	if open < 0 || !strings.HasSuffix(signature, ")") {
		return nil, false
	}
	return splitTopLevel(signature[open+1 : len(signature)-1]), true
}

func splitTopLevel(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	return out
}

// headWords returns how many 32-byte head words a type occupies and whether
// it is dynamic (dynamic types occupy one offset word in the head).
func headWords(typ string) (words int, dynamic bool, ok bool) {
	// Arrays: strip the trailing [..] and recurse on the element type.
	if strings.HasSuffix(typ, "]") {
		lb := strings.LastIndexByte(typ, '[')
		if lb < 0 {
			return 0, false, false
		}
		inner, n := typ[:lb], typ[lb+1:len(typ)-1]
		if n == "" {
			return 1, true, true // T[] is dynamic
		}
		count := 0
		for _, ch := range n {
			if ch < '0' || ch > '9' {
				return 0, false, false
			}
			count = count*10 + int(ch-'0')
		}
		w, dyn, ok := headWords(inner)
		if !ok {
			return 0, false, false
		}
		if dyn {
			return 1, true, true // T[n] with dynamic T is dynamic
		}
		return w * count, false, true
	}
	if strings.HasPrefix(typ, "(") {
		if !strings.HasSuffix(typ, ")") {
			return 0, false, false
		}
		total := 0
		for _, c := range splitTopLevel(typ[1 : len(typ)-1]) {
			w, dyn, ok := headWords(c)
			if !ok {
				return 0, false, false
			}
			if dyn {
				return 1, true, true
			}
			total += w
		}
		return total, false, true
	}
	switch {
	case typ == "string" || typ == "bytes":
		return 1, true, true
	case typ == "address" || typ == "bool" || typ == "function":
		return 1, false, true
	case strings.HasPrefix(typ, "uint") || strings.HasPrefix(typ, "int") ||
		strings.HasPrefix(typ, "bytes") || strings.HasPrefix(typ, "fixed") || strings.HasPrefix(typ, "ufixed"):
		return 1, false, true
	}
	return 0, false, false
}
