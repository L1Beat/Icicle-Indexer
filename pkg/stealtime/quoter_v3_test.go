package stealtime

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// TestEncodeQuoteExactInputSingle locks the QuoterV2 calldata shape: a 4-byte
// selector plus exactly five inline static words, with the fee/tickSpacing variant
// producing a different selector (the field type differs).
func TestEncodeQuoteExactInputSingle(t *testing.T) {
	tokenIn := common.HexToAddress("0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7")  // WAVAX
	tokenOut := common.HexToAddress("0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E") // USDC
	amountIn := new(big.Int).SetUint64(1_000_000_000_000_000_000)                 // 1e18

	feeData := encodeQuoteExactInputSingle(v3Fee, tokenIn, tokenOut, amountIn, 3000)
	tickData := encodeQuoteExactInputSingle(v3TickSpacing, tokenIn, tokenOut, amountIn, 100)

	// 4-byte selector (8 hex) + 5 words (5*64 hex) = 8 + 320 = 328 hex chars.
	if got := len(strings.TrimPrefix(feeData, "0x")); got != 8+5*64 {
		t.Fatalf("fee calldata length = %d hex chars, want %d", got, 8+5*64)
	}

	feeSel := lending.Selector("quoteExactInputSingle((address,address,uint256,uint24,uint160))")
	tickSel := lending.Selector("quoteExactInputSingle((address,address,uint256,int24,uint160))")
	if !strings.HasPrefix(feeData, feeSel) {
		t.Errorf("fee variant selector = %s..., want prefix %s", feeData[:10], feeSel)
	}
	if !strings.HasPrefix(tickData, tickSel) {
		t.Errorf("tickSpacing variant selector = %s..., want prefix %s", tickData[:10], tickSel)
	}
	if feeSel == tickSel {
		t.Errorf("fee and tickSpacing selectors must differ (uint24 vs int24): both %s", feeSel)
	}

	// Word 3 (the key) of the fee variant must hold 3000, right-aligned.
	body := lending.DecodeHexBytes(strings.TrimPrefix(feeData, feeSel))
	if got := lending.Word(body, 3).Uint64(); got != 3000 {
		t.Errorf("fee word = %d, want 3000", got)
	}
	// Word 0 is the tokenIn address, right-aligned in the low 20 bytes.
	if got := lending.Addr(body, 0); !strings.EqualFold(got, tokenIn.Hex()) {
		t.Errorf("tokenIn word = %s, want %s", got, tokenIn.Hex())
	}
}
