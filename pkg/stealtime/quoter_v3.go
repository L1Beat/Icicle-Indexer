package stealtime

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// Concentrated-liquidity venues on Avalanche. These hold the real depth today;
// the V2 constant-product routers (Pangolin, Trader Joe V1) are kept only as an
// attributed fallback. Each venue is quoted through its OWN quoter, which walks
// live tick liquidity at the historical block, so the fill reflects executable
// depth near the current tick, not headline pool TVL.
//
// Uniswap V3 keys pools by a uint24 fee tier. Pharaoh CL is a Ramses-V3 fork and
// keys pools by an int24 tickSpacing instead, which changes the struct field type
// and therefore the function selector. Both are surveyed across their full key
// set; a pool that does not exist for a (pair, key) reverts and is skipped. Run
// VenueProbe first to confirm each quoter address and encoding is live on-chain
// before trusting a historical capture run.
type v3Kind int

const (
	v3Fee         v3Kind = iota // Uniswap V3 style: uint24 fee
	v3TickSpacing               // Ramses/Pharaoh style: int24 tickSpacing
)

func (k v3Kind) typeString() string {
	if k == v3TickSpacing {
		return "quoteExactInputSingle((address,address,uint256,int24,uint160))"
	}
	return "quoteExactInputSingle((address,address,uint256,uint24,uint160))"
}

type v3Venue struct {
	name     string
	quoter   common.Address
	kind     v3Kind
	poolKeys []int64 // fee tiers or tick spacings to survey
}

var (
	// Uniswap V3 on Avalanche (official Uniswap deployments page). QuoterV2.
	uniswapV3 = v3Venue{
		name:     "univ3",
		quoter:   common.HexToAddress("0xbe0F5544EC67e9B3b2D979aaA43f18Fd87E6257F"),
		kind:     v3Fee,
		poolKeys: []int64{100, 500, 3000, 10000},
	}
	// Pharaoh CL (Ramses-V3 fork) on Avalanche (docs.phar.gg contract addresses).
	// RamsesV3Factory confirms the fork, so pools are keyed by int24 tickSpacing.
	pharaohV3 = v3Venue{
		name:     "pharaoh",
		quoter:   common.HexToAddress("0xB7297301b7CC659BB96D51754643A0Df6eEA2138"),
		kind:     v3TickSpacing,
		poolKeys: []int64{1, 5, 10, 50, 100, 200, 2000},
	}

	realV3Venues = []v3Venue{uniswapV3, pharaohV3}

	// Canonical liquid token for the venue self-test (native USDC, 6 decimals).
	usdcAvax = common.HexToAddress("0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E")
)

// v3Route returns the best executable output for tokenIn->tokenOut on one V3
// venue, surveying every pool key direct and a via-WAVAX two-hop (the first hop's
// output feeds the second, exactly as a router chains them).
func (q *blockQuoter) v3Route(ctx context.Context, v v3Venue, tokenIn, tokenOut common.Address, amountIn *big.Int, bh string) *big.Int {
	best := q.v3DirectBest(ctx, v, tokenIn, tokenOut, amountIn, bh)
	if tokenIn != wavax && tokenOut != wavax {
		if mid := q.v3DirectBest(ctx, v, tokenIn, wavax, amountIn, bh); mid != nil && mid.Sign() > 0 {
			if o2 := q.v3DirectBest(ctx, v, wavax, tokenOut, mid, bh); o2 != nil && o2.Cmp(best) > 0 {
				best = o2
			}
		}
	}
	return best
}

func (q *blockQuoter) v3DirectBest(ctx context.Context, v v3Venue, tokenIn, tokenOut common.Address, amountIn *big.Int, bh string) *big.Int {
	best := big.NewInt(0)
	for _, key := range v.poolKeys {
		if out := q.quoteV3Single(ctx, v, key, tokenIn, tokenOut, amountIn, bh); out != nil && out.Cmp(best) > 0 {
			best = out
		}
	}
	return best
}

// quoteV3Single calls a single-pool quoteExactInputSingle and returns amountOut
// (return word 0). The five struct fields are all static, so the tuple is encoded
// inline as five consecutive words with no offset. Returns nil on revert (no pool
// for this key) or a malformed return.
func (q *blockQuoter) quoteV3Single(ctx context.Context, v v3Venue, key int64, tokenIn, tokenOut common.Address, amountIn *big.Int, bh string) *big.Int {
	res, err := q.rpc.EthCall(ctx, v.quoter.Hex(), encodeQuoteExactInputSingle(v.kind, tokenIn, tokenOut, amountIn, key), bh)
	if err != nil {
		return nil
	}
	out := lending.Word(lending.DecodeHexBytes(res), 0)
	if out == nil || out.Sign() == 0 {
		return nil
	}
	return out
}

// encodeQuoteExactInputSingle builds calldata for a V3-style QuoterV2. The five
// struct fields are all static, so the tuple is encoded inline as five consecutive
// words: tokenIn, tokenOut, amountIn, key (uint24 fee or int24 tickSpacing,
// right-aligned), sqrtPriceLimitX96=0. The selector differs between the fee and
// tickSpacing variants because the struct field type differs.
func encodeQuoteExactInputSingle(kind v3Kind, tokenIn, tokenOut common.Address, amountIn *big.Int, key int64) string {
	var b []byte
	b = append(b, addrWord(tokenIn)...)
	b = append(b, addrWord(tokenOut)...)
	b = append(b, wordOf(amountIn)...)
	b = append(b, wordOf(big.NewInt(key))...)
	b = append(b, wordOf(big.NewInt(0))...)
	return lending.Selector(kind.typeString()) + hex.EncodeToString(b)
}

// VenueProbe quotes 1 WAVAX -> USDC (and the reverse) against every venue at a
// recent block and returns a human-readable report, so each venue's address and
// quoter encoding is confirmed live on-chain BEFORE trusting a historical run. A
// venue that prints all zeros has a wrong address or encoding and must be fixed
// rather than silently falling back to another venue. Read-only.
func VenueProbe(ctx context.Context, rpc *lending.Client, block uint64) string {
	q := newRealVenueQuoter(rpc, block)
	bh := blockHex(block)
	oneWavax := new(big.Int).Set(wad)            // 1e18 = 1 WAVAX
	tenKUsdc := new(big.Int).SetUint64(10_000e6) // 10,000 USDC (6 decimals)

	var b strings.Builder
	fmt.Fprintf(&b, "\n=== venue probe @ block %d (read-only) ===\n", block)
	fmt.Fprintf(&b, "WAVAX=%s  USDC=%s\n", wavax.Hex(), usdcAvax.Hex())
	fmt.Fprintf(&b, "amounts: in=1 WAVAX (1e18) and 10,000 USDC (1e10); out shown raw (USDC 6dp, WAVAX 18dp)\n\n")

	probe := func(label string, tokenIn, tokenOut common.Address, amountIn *big.Int) {
		fmt.Fprintf(&b, "%s  %s -> %s  in=%s\n", label, short(tokenIn), short(tokenOut), amountIn.String())
		for _, r := range q.routers {
			out := q.getAmountsOut(ctx, r, amountIn, []common.Address{tokenIn, tokenOut}, bh)
			fmt.Fprintf(&b, "    v2 %s: %s\n", short(r), rawStr(out))
		}
		for _, lb := range q.lbQuoters {
			out := q.lbQuote(ctx, lb, amountIn, []common.Address{tokenIn, tokenOut}, bh)
			fmt.Fprintf(&b, "    lb %s: %s\n", short(lb), rawStr(out))
		}
		for _, v := range q.v3Venues {
			for _, key := range v.poolKeys {
				out := q.quoteV3Single(ctx, v, key, tokenIn, tokenOut, amountIn, bh)
				if out != nil && out.Sign() > 0 {
					fmt.Fprintf(&b, "    %s key=%d: %s\n", v.name, key, rawStr(out))
				}
			}
			fmt.Fprintf(&b, "    %s best: %s\n", v.name, rawStr(q.v3DirectBest(ctx, v, tokenIn, tokenOut, amountIn, bh)))
		}
	}

	probe("WAVAX->USDC", wavax, usdcAvax, oneWavax)
	fmt.Fprintln(&b)
	probe("USDC->WAVAX", usdcAvax, wavax, tenKUsdc)
	return b.String()
}

func short(a common.Address) string {
	h := a.Hex()
	if len(h) < 10 {
		return h
	}
	return h[:6] + ".." + h[len(h)-4:]
}

func rawStr(n *big.Int) string {
	if n == nil || n.Sign() == 0 {
		return "(none)"
	}
	return n.String()
}
