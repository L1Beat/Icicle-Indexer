package kmeasure

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// Default UniswapV2-style routers on Avalanche. Both expose getAmountsOut, which
// returns the executable output (including price impact) for the given amountIn,
// and a Stage 2 contract can replicate the same route via swapExactTokensForTokens.
// Configurable so deeper-liquidity venues can be added without code changes.
var (
	PangolinRouter  = common.HexToAddress("0xE54Ca86531e17Ef3616d22Ca28b0D458b6C89106")
	TraderJoeRouter = common.HexToAddress("0x60aE616a2155Ee3d9A68541Ba4544862310933d4")
)

// Route is the venue and token path that produced a quote, carried alongside the
// output so Stage 2 can replicate it. The plumbing exists here so prefilter stays
// untouched.
type Route struct {
	Router common.Address
	Path   []common.Address
}

// RichQuoter returns the executable output and the route that produced it.
type RichQuoter interface {
	QuoteRoute(ctx context.Context, tokenIn common.Address, tokenInDec uint8, tokenOut common.Address, tokenOutDec uint8, amountIn *big.Int) (*big.Int, Route, error)
}

// DexQuoter quotes against UniswapV2-style routers at live chain head, trying a
// direct path and a WAVAX-bridged path per router and keeping the best output.
type DexQuoter struct {
	r       EthReader
	routers []common.Address
	wavax   common.Address
}

// NewDexQuoter builds a quoter. If routers is empty, the Avalanche defaults are used.
func NewDexQuoter(r EthReader, routers []common.Address) *DexQuoter {
	if len(routers) == 0 {
		routers = []common.Address{PangolinRouter, TraderJoeRouter}
	}
	return &DexQuoter{r: r, routers: routers, wavax: WAVAX}
}

func (q *DexQuoter) QuoteRoute(ctx context.Context, tokenIn common.Address, _ uint8, tokenOut common.Address, _ uint8, amountIn *big.Int) (*big.Int, Route, error) {
	if amountIn == nil || amountIn.Sign() <= 0 || tokenIn == tokenOut {
		return big.NewInt(0), Route{}, nil
	}

	paths := [][]common.Address{{tokenIn, tokenOut}}
	if tokenIn != q.wavax && tokenOut != q.wavax {
		paths = append(paths, []common.Address{tokenIn, q.wavax, tokenOut})
	}

	best := big.NewInt(0)
	var bestRoute Route
	for _, router := range q.routers {
		for _, path := range paths {
			out := q.getAmountsOut(ctx, router, amountIn, path)
			if out != nil && out.Cmp(best) > 0 {
				best = out
				bestRoute = Route{Router: router, Path: path}
			}
		}
	}
	// No usable route is a normal outcome, not an error: the adapter maps a zero
	// output to the pre-filter's illiquid or no-pair classification.
	return best, bestRoute, nil
}

// getAmountsOut returns the final output amount for one router and path, or nil if
// the call reverts (no pair on that route).
func (q *DexQuoter) getAmountsOut(ctx context.Context, router common.Address, amountIn *big.Int, path []common.Address) *big.Int {
	res, err := q.r.EthCall(ctx, router.Hex(), encodeGetAmountsOut(amountIn, path), "latest")
	if err != nil {
		return nil
	}
	amounts := lending.DecodeUintArray(lending.DecodeHexBytes(res))
	if len(amounts) == 0 {
		return nil
	}
	return amounts[len(amounts)-1]
}

// QuoterAdapter wraps a RichQuoter as a prefilter.Quoter, discarding the route for
// the K run while recording call latency and the failure rate. A quote failure or
// missing route surfaces as a zero output (illiquid or no-pair), never an error,
// so a single bad pair does not abort the run.
type QuoterAdapter struct {
	rq RichQuoter

	mu        sync.Mutex
	calls     int
	failures  int
	totalTime time.Duration
}

// NewQuoterAdapter adapts a RichQuoter to prefilter.Quoter.
func NewQuoterAdapter(rq RichQuoter) *QuoterAdapter {
	return &QuoterAdapter{rq: rq}
}

func (a *QuoterAdapter) QuoteOut(ctx context.Context, tokenIn common.Address, tokenInDec uint8, tokenOut common.Address, tokenOutDec uint8, amountIn *big.Int) (*big.Int, error) {
	start := time.Now()
	out, _, err := a.rq.QuoteRoute(ctx, tokenIn, tokenInDec, tokenOut, tokenOutDec, amountIn)
	elapsed := time.Since(start)

	a.mu.Lock()
	a.calls++
	a.totalTime += elapsed
	failed := err != nil || out == nil || out.Sign() == 0
	if failed {
		a.failures++
	}
	a.mu.Unlock()

	observeQuoterLatency(elapsed)
	if failed {
		return big.NewInt(0), nil
	}
	return out, nil
}

// Stats returns the call count, failure count, and average latency.
func (a *QuoterAdapter) Stats() (calls, failures int, avg time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calls > 0 {
		avg = a.totalTime / time.Duration(a.calls)
	}
	return a.calls, a.failures, avg
}
