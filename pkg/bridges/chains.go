package bridges

import (
	"strings"
)

// Chain keys follow LayerZero's chainKey names (lzChains), so a chain reached
// through several bridges groups under one key. Bridges without a LayerZero
// counterpart map onto the same names here.

// cctpChains maps Circle CCTP domains to chain keys (developers.circle.com,
// "CCTP supported blockchains", 2026-11-03). Domain 1 is Avalanche itself.
var cctpChains = map[uint32]string{
	0:  "ethereum",
	2:  "optimism",
	3:  "arbitrum",
	4:  "noble",
	5:  "solana",
	6:  "base",
	7:  "polygon",
	8:  "sui",
	9:  "aptos",
	10: "unichain",
	11: "linea",
	12: "codex",
	13: "sonic",
	14: "worldchain",
	15: "monad",
	16: "sei",
	17: "bsc",
	18: "xdc",
	19: "hyperliquid",
	21: "ink",
	22: "plume",
	25: "starknet",
	26: "arc",
	27: "stellar",
	28: "edge",
	29: "injective",
	30: "morph",
	31: "pharos",
	32: "cronosevm",
	33: "plasma",
	37: "xlayer",
}

// chainNames are display names, ahead of LayerZero's own (lzChainNames).
var chainNames = map[string]string{
	"abstract":    "Abstract",
	"aptos":       "Aptos",
	"arbitrum":    "Arbitrum",
	"arc":         "Arc",
	"base":        "Base",
	"beam":        "Beam",
	"bera":        "Berachain",
	"bitcoin":     "Bitcoin",
	"blast":       "Blast",
	"bsc":         "BNB Chain",
	"codex":       "Codex",
	"cronosevm":   "Cronos",
	"dexalot":     "Dexalot",
	"dfk":         "DFK Chain",
	"edge":        "EDGE",
	"ethereum":    "Ethereum",
	"fantom":      "Fantom",
	"gnosis":      "Gnosis",
	"gunz":        "GUNZ",
	"hyperliquid": "HyperEVM",
	"injective":   "Injective",
	"ink":         "Ink",
	"linea":       "Linea",
	"mantle":      "Mantle",
	"monad":       "Monad",
	"morph":       "Morph",
	"noble":       "Noble",
	"optimism":    "OP Mainnet",
	"pharos":      "Pharos",
	"plasma":      "Plasma",
	"plume":       "Plume",
	"polygon":     "Polygon",
	"scroll":      "Scroll",
	"sei":         "Sei",
	"shrapnel":    "Shrapnel",
	"solana":      "Solana",
	"sonic":       "Sonic",
	"starknet":    "Starknet",
	"stellar":     "Stellar",
	"sui":         "Sui",
	"tempo":       "Tempo",
	"unichain":    "Unichain",
	"worldchain":  "World Chain",
	"xdc":         "XDC",
	"xlayer":      "X Layer",
	"zksync":      "ZKsync",
}

// avalancheL1s are counterparties that are themselves Avalanche L1s, so a page
// can tell flows to other ecosystems from flows inside Avalanche.
var avalancheL1s = map[string]bool{
	"beam":     true,
	"dexalot":  true,
	"dfk":      true,
	"gunz":     true,
	"shrapnel": true,
}

// ChainName is a chain key's display name. Unknown keys, such as an unmapped
// "lz:30999", are returned as they are.
func ChainName(key string) string {
	if n, ok := chainNames[key]; ok {
		return n
	}
	if n, ok := lzChainNames[key]; ok {
		return n
	}
	if key == "" || strings.Contains(key, ":") {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

// IsAvalancheL1 reports whether the chain key is an Avalanche L1.
func IsAvalancheL1(key string) bool { return avalancheL1s[key] }
