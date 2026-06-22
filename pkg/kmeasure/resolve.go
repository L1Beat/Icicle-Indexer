package kmeasure

import (
	"context"
	"sync"

	"github.com/ava-labs/libevm/common"
)

// WAVAX is the wrapped-AVAX ERC20 on Avalanche C-Chain, the swap token for the
// native qiAVAX market whose qiToken has no underlying().
var WAVAX = common.HexToAddress("0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7")

// TokenInfo is the swap-relevant identity of a leg asset: the ERC20 to quote and
// its decimals. For Aave this is the leg asset itself; for Benqi it is the
// qiToken's underlying.
type TokenInfo struct {
	Underlying common.Address
	Decimals   uint8
}

// Resolver maps a feed leg asset to the token a DEX quote must use.
type Resolver interface {
	Resolve(ctx context.Context, protocol string, asset common.Address) (TokenInfo, error)
}

// ChainResolver resolves on-chain and caches. Aave leg assets are already the
// underlying. Benqi leg assets are qiTokens, resolved via underlying(), with the
// native qiAVAX market (underlying() reverts) mapped to WAVAX.
type ChainResolver struct {
	r     EthReader
	wavax common.Address

	mu    sync.Mutex
	cache map[common.Address]TokenInfo
}

// NewChainResolver builds a resolver over the given reader.
func NewChainResolver(r EthReader) *ChainResolver {
	return &ChainResolver{r: r, wavax: WAVAX, cache: map[common.Address]TokenInfo{}}
}

func (c *ChainResolver) Resolve(ctx context.Context, protocol string, asset common.Address) (TokenInfo, error) {
	c.mu.Lock()
	if info, ok := c.cache[asset]; ok {
		c.mu.Unlock()
		return info, nil
	}
	c.mu.Unlock()

	underlying := asset
	if protocol == "benqi" {
		// underlying() reverts on the native market; fall back to WAVAX.
		if u, err := callAddr(ctx, c.r, asset, "underlying()"); err == nil && u != (common.Address{}) {
			underlying = u
		} else {
			underlying = c.wavax
		}
	}

	dec := uint8(18)
	if d, err := callUint(ctx, c.r, underlying, "decimals()"); err == nil && d.Sign() > 0 {
		dec = uint8(d.Uint64())
	}

	info := TokenInfo{Underlying: underlying, Decimals: dec}
	c.mu.Lock()
	c.cache[asset] = info
	c.mu.Unlock()
	return info, nil
}
