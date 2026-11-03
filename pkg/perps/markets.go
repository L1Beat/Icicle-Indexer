package perps

import (
	"encoding/hex"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// tokenSymbols are display symbols for the tokens GMX markets use on the
// C-Chain: bridged and wrapped tokens under their asset's name.
var tokenSymbols = map[string]string{
	"b31f66aa3c1e785363f0875a1b74e27b85fd66c7": "AVAX", // WAVAX
	"152b9d0fdc40c096757f570a51e494bd4b943e50": "BTC",  // BTC.b
	"50b7545627a5162f82a992c33b87adc75187b218": "BTC",  // WBTC.e
	"49d5c2bdffac6ce2bfdb6640f4f80f226bc10bab": "ETH",  // WETH.e
	"fe6b19286885a4f7f55adad09c3cd1f906d2478f": "SOL",
	"2775d5105276781b4b85ba6ea6a6653beed1dd32": "XAUt", // XAUt0
	"b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e": "USDC",
	"a7d7079b0fead91f3e65f86e8915cb59c1a4c664": "USDC.e",
	"9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7": "USDT",
	"c7198437980c041c805a1edcba50c1ce5db95118": "USDT.e",
	"d586e7f844cea2f87f50152665bcbc2c279d8d70": "DAI.e",
	"5947bb275c521040051d82396192181b413227a3": "LINK", // LINK.e
}

// syntheticSymbols are GMX V2 synthetic index tokens (no contract) on the
// C-Chain. GMX derives their address as the last 20 bytes of
// keccak256(abi.encode(chainId, symbol)), see getSyntheticTokenAddress in
// gmx-synthetics utils/token.ts.
var syntheticSymbols = []string{"DOGE", "LTC", "XRP", "TRUMP", "MELANIA", "PUMP", "WLFI"}

func init() {
	for _, s := range syntheticSymbols {
		tokenSymbols[syntheticAddress(ChainID, s)] = s
	}
}

func syntheticAddress(chainID int64, symbol string) string {
	word := func(b []byte) []byte { w := make([]byte, 32); copy(w[32-len(b):], b); return w }
	enc := word(big.NewInt(chainID).Bytes())
	enc = append(enc, word(big.NewInt(64).Bytes())...)
	enc = append(enc, word(big.NewInt(int64(len(symbol))).Bytes())...)
	pad := make([]byte, (len(symbol)+31)/32*32)
	copy(pad, symbol)
	enc = append(enc, pad...)
	h := sha3.NewLegacyKeccak256()
	h.Write(enc)
	return hex.EncodeToString(h.Sum(nil)[12:])
}

// TokenSymbol is a token's display symbol, or its short address when unknown.
func TokenSymbol(addr string) string {
	addr = strings.TrimPrefix(strings.ToLower(addr), "0x")
	if s, ok := tokenSymbols[addr]; ok {
		return s
	}
	if len(addr) >= 8 {
		return "0x" + addr[:8]
	}
	return addr
}

// MarketName is a market's display name: "AVAX/USD" for its index token, and
// the pool's long and short tokens, e.g. "AVAX-USDC" (GMX V2 has several pools
// per index). GMX V1 markets are index tokens and have no pool.
func MarketName(index, long, short string) (name, pool string) {
	if strings.Trim(index, "0") == "" {
		name = "Swap only"
	} else {
		name = TokenSymbol(index) + "/USD"
	}
	if long != "" || short != "" {
		pool = TokenSymbol(long) + "-" + TokenSymbol(short)
	}
	return name, pool
}
