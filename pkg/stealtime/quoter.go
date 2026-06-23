package stealtime

import (
	"context"
	"encoding/hex"
	"math/big"
	"sync"

	"github.com/ava-labs/libevm/common"

	"icicle/pkg/lending"
)

// Avalanche venue defaults. The LFJ Liquidity Book quoter is the dominant venue
// today; omitting it produces false illiquid results. Its address is configurable
// and must be verified on-chain (LBQuoter), since a wrong address makes LB quotes
// fail and fall back to the V2 routers.
var (
	pangolinRouter  = common.HexToAddress("0xE54Ca86531e17Ef3616d22Ca28b0D458b6C89106")
	traderJoeRouter = common.HexToAddress("0x60aE616a2155Ee3d9A68541Ba4544862310933d4")
	wavax           = common.HexToAddress("0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7")

	// LFJ Liquidity Book quoters, verified on-chain (developers.lfj.gg). V2.2 and
	// V2.1 share the Quote struct shape (a versions field, so amounts is member
	// index 4); V2.0 differs and is omitted. Querying both covers historical and
	// recent blocks; a quoter that did not exist yet at a block reverts and is
	// skipped.
	lfjLBQuoters = []common.Address{
		common.HexToAddress("0x9A550a522BBaDFB69019b0432800Ed17855A51C3"), // V2.2
		common.HexToAddress("0xd76019A16606FDa4651f636D9751f500Ed776250"), // V2.1
	}
)

// blockQuoter implements prefilter.Quoter against live pool state at a fixed
// historical block, surveying UniswapV2-style routers and the LFJ Liquidity Book
// and keeping the best executable output. A pair with no route returns zero, which
// the pre-filter classifies illiquid, never an error.
type blockQuoter struct {
	rpc       *lending.Client
	block     uint64
	routers   []common.Address
	lbQuoters []common.Address

	mu       sync.Mutex
	calls    int
	failures int
}

func newBlockQuoter(rpc *lending.Client, block uint64) *blockQuoter {
	return &blockQuoter{
		rpc:       rpc,
		block:     block,
		routers:   []common.Address{pangolinRouter, traderJoeRouter},
		lbQuoters: lfjLBQuoters,
	}
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
	bh := blockHex(q.block)

	for _, router := range q.routers {
		for _, path := range paths {
			if out := q.getAmountsOut(ctx, router, amountIn, path, bh); out != nil && out.Cmp(best) > 0 {
				best = out
			}
		}
	}
	for _, lb := range q.lbQuoters {
		for _, path := range paths {
			if out := q.lbQuote(ctx, lb, amountIn, path, bh); out != nil && out.Cmp(best) > 0 {
				best = out
			}
		}
	}

	if best.Sign() == 0 {
		q.mu.Lock()
		q.failures++
		q.mu.Unlock()
	}
	return best, nil
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
// from a returned LFJ Quote tuple. Returns nil on any decode issue.
func decodeLBAmountsLast(b []byte) *big.Int {
	const amountsMemberIndex = 4
	if len(b) < (amountsMemberIndex+1)*32 {
		return nil
	}
	off := int(lending.Word(b, amountsMemberIndex).Uint64())
	if off < 0 || off+32 > len(b) {
		return nil
	}
	n := int(new(big.Int).SetBytes(b[off : off+32]).Uint64())
	if n == 0 {
		return nil
	}
	last := off + 32 + (n-1)*32
	if last+32 > len(b) {
		return nil
	}
	return new(big.Int).SetBytes(b[last : last+32])
}
