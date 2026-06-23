package stealtime

import (
	"context"
	"encoding/hex"
	"math/big"
	"sync"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// Avalanche venue defaults. The real liquidity sits on concentrated-liquidity
// venues (LFJ Liquidity Book, Uniswap V3, Pharaoh CL); the V2 routers below hold
// little depth now and are kept only as an attributed fallback in the route
// survey. Every quote is the venue's own on-chain swap simulation at the historical
// block, sized to the real seize amount, so the fill reflects executable depth.
var (
	pangolinRouter  = common.HexToAddress("0xE54Ca86531e17Ef3616d22Ca28b0D458b6C89106")
	traderJoeRouter = common.HexToAddress("0x60aE616a2155Ee3d9A68541Ba4544862310933d4")
	wavax           = common.HexToAddress("0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7")

	// LFJ Liquidity Book quoters, verified on-chain (developers.lfj.gg). V2.2 and
	// V2.1 share the Quote struct shape; V2.0 differs and is omitted.
	lfjLBQuoters = []common.Address{
		common.HexToAddress("0x9A550a522BBaDFB69019b0432800Ed17855A51C3"), // V2.2
		common.HexToAddress("0xd76019A16606FDa4651f636D9751f500Ed776250"), // V2.1
	}
)

// blockQuoter implements prefilter.Quoter against live pool state at a fixed
// historical block, surveying a configurable venue set and keeping the best
// executable output. When attribute is set it also records which venue won each
// directed pair, so a venue's real contribution (e.g. whether V2 ever wins a large
// unwind) can be measured. A pair with no route returns zero, which the pre-filter
// classifies illiquid, never an error.
type blockQuoter struct {
	rpc       *lending.Client
	block     uint64
	routers   []common.Address
	lbQuoters []common.Address
	v3Venues  []v3Venue
	attribute bool

	mu       sync.Mutex
	calls    int
	failures int
	winLabel map[string]string
	winAmt   map[string]*big.Int
}

func newBlockQuoterBase(rpc *lending.Client, block uint64) *blockQuoter {
	return &blockQuoter{
		rpc: rpc, block: block,
		winLabel: map[string]string{},
		winAmt:   map[string]*big.Int{},
	}
}

// newBlockQuoter surveys the V2 routers and the LFJ Liquidity Book. Used by the
// steal-time backtest's profitability gate.
func newBlockQuoter(rpc *lending.Client, block uint64) *blockQuoter {
	q := newBlockQuoterBase(rpc, block)
	q.routers = []common.Address{pangolinRouter, traderJoeRouter}
	q.lbQuoters = lfjLBQuoters
	return q
}

// newBlockQuoterV2 surveys ONLY the V2-style routers, the venue a plain
// swapExactTokensForTokens can replicate. The conservative executability baseline.
func newBlockQuoterV2(rpc *lending.Client, block uint64) *blockQuoter {
	q := newBlockQuoterBase(rpc, block)
	q.routers = []common.Address{pangolinRouter, traderJoeRouter}
	return q
}

// newRealVenueQuoter surveys the venues that actually hold depth on Avalanche:
// the LFJ Liquidity Book and the V3 concentrated-liquidity venues (Uniswap V3 and
// Pharaoh CL), with the V2 routers retained only as an attributed fallback. It
// records which venue wins each route so V2's real share can be measured.
func newRealVenueQuoter(rpc *lending.Client, block uint64) *blockQuoter {
	q := newBlockQuoterBase(rpc, block)
	q.routers = []common.Address{pangolinRouter, traderJoeRouter}
	q.lbQuoters = lfjLBQuoters
	q.v3Venues = realV3Venues
	q.attribute = true
	return q
}

func (q *blockQuoter) QuoteOut(ctx context.Context, tokenIn common.Address, _ uint8, tokenOut common.Address, _ uint8, amountIn *big.Int) (*big.Int, error) {
	q.mu.Lock()
	q.calls++
	q.mu.Unlock()
	if amountIn == nil || amountIn.Sign() <= 0 || tokenIn == tokenOut {
		return big.NewInt(0), nil
	}

	paths := [][]common.Address{{tokenIn, tokenOut}}
	if tokenIn != wavax && tokenOut != wavax {
		paths = append(paths, []common.Address{tokenIn, wavax, tokenOut})
	}

	best := big.NewInt(0)
	bestLabel := "none"
	bh := blockHex(q.block)
	consider := func(out *big.Int, label string) {
		if out != nil && out.Cmp(best) > 0 {
			best = out
			bestLabel = label
		}
	}

	for _, router := range q.routers {
		for _, path := range paths {
			consider(q.getAmountsOut(ctx, router, amountIn, path, bh), "v2")
		}
	}
	for _, lb := range q.lbQuoters {
		for _, path := range paths {
			consider(q.lbQuote(ctx, lb, amountIn, path, bh), "lb")
		}
	}
	for _, v := range q.v3Venues {
		consider(q.v3Route(ctx, v, tokenIn, tokenOut, amountIn, bh), v.name)
	}

	if best.Sign() == 0 {
		q.mu.Lock()
		q.failures++
		q.mu.Unlock()
	} else if q.attribute {
		q.recordWinner(tokenIn, tokenOut, best, bestLabel)
	}
	return best, nil
}

func (q *blockQuoter) recordWinner(tokenIn, tokenOut common.Address, out *big.Int, label string) {
	key := tokenIn.Hex() + ":" + tokenOut.Hex()
	q.mu.Lock()
	defer q.mu.Unlock()
	if prev, ok := q.winAmt[key]; !ok || out.Cmp(prev) > 0 {
		q.winAmt[key] = out
		q.winLabel[key] = label
	}
}

// WinningVenue returns the venue label that produced the best output for a
// directed pair during this quoter's lifetime, or "none".
func (q *blockQuoter) WinningVenue(tokenIn, tokenOut common.Address) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if l, ok := q.winLabel[tokenIn.Hex()+":"+tokenOut.Hex()]; ok {
		return l
	}
	return "none"
}

// Stats returns calls and failures for metrics.
func (q *blockQuoter) Stats() (calls, failures int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls, q.failures
}

func (q *blockQuoter) getAmountsOut(ctx context.Context, router common.Address, amountIn *big.Int, path []common.Address, bh string) *big.Int {
	res, err := q.rpc.EthCall(ctx, router.Hex(), encodeGetAmountsOut(amountIn, path), bh)
	if err != nil {
		return nil
	}
	amounts := lending.DecodeUintArray(lending.DecodeHexBytes(res))
	if len(amounts) == 0 {
		return nil
	}
	return amounts[len(amounts)-1]
}

// lbQuote calls an LFJ LBQuoter.findBestPathFromAmountIn(route, amountIn) and reads
// the last entry of the returned Quote.amounts array.
func (q *blockQuoter) lbQuote(ctx context.Context, lbQuoter common.Address, amountIn *big.Int, path []common.Address, bh string) *big.Int {
	res, err := q.rpc.EthCall(ctx, lbQuoter.Hex(), encodeFindBestPath(path, amountIn), bh)
	if err != nil {
		return nil
	}
	return decodeLBAmountsLast(lending.DecodeHexBytes(res))
}

// --- ABI encoding ---

func wordOf(n *big.Int) []byte {
	w := make([]byte, 32)
	if n != nil && n.Sign() > 0 {
		nb := n.Bytes()
		if len(nb) <= 32 {
			copy(w[32-len(nb):], nb)
		}
	}
	return w
}

func addrWord(a common.Address) []byte {
	w := make([]byte, 32)
	copy(w[12:], a.Bytes())
	return w
}

func encodeGetAmountsOut(amountIn *big.Int, path []common.Address) string {
	var b []byte
	b = append(b, wordOf(amountIn)...)
	b = append(b, wordOf(big.NewInt(0x40))...)
	b = append(b, wordOf(big.NewInt(int64(len(path))))...)
	for _, a := range path {
		b = append(b, addrWord(a)...)
	}
	return lending.Selector("getAmountsOut(uint256,address[])") + hex.EncodeToString(b)
}

// encodeFindBestPath encodes findBestPathFromAmountIn(address[] route, uint128 amountIn).
func encodeFindBestPath(route []common.Address, amountIn *big.Int) string {
	var b []byte
	b = append(b, wordOf(big.NewInt(0x40))...) // offset to route array
	b = append(b, wordOf(amountIn)...)         // uint128 amountIn (right-padded in 32)
	b = append(b, wordOf(big.NewInt(int64(len(route))))...)
	for _, a := range route {
		b = append(b, addrWord(a)...)
	}
	return lending.Selector("findBestPathFromAmountIn(address[],uint128)") + hex.EncodeToString(b)
}

// decodeLBAmountsLast reads the last element of the amounts array (member index 4)
// from a returned LFJ Quote struct. The return is a single dynamic struct, so word
// 0 is the offset to the tuple and every member offset is relative to that base.
// Returns nil on any decode issue.
func decodeLBAmountsLast(b []byte) *big.Int {
	const amountsMemberIndex = 4
	if len(b) < 64 {
		return nil
	}
	base := int(lending.Word(b, 0).Uint64())
	headWord := base + amountsMemberIndex*32
	if base < 0 || headWord+32 > len(b) {
		return nil
	}
	amountsOff := base + int(new(big.Int).SetBytes(b[headWord:headWord+32]).Uint64())
	if amountsOff < 0 || amountsOff+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[amountsOff : amountsOff+32]).Uint64())
	if n == 0 {
		return nil
	}
	last := amountsOff + 32 + (n-1)*32
	if last+32 > len(b) {
		return nil
	}
	out := new(big.Int).SetBytes(b[last : last+32])
	// amounts are uint128; reject anything wider as a decode-misalignment guard.
	if out.BitLen() > 128 {
		return nil
	}
	return out
}
