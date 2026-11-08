package lendingmarkets

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LTV change dates. Each market's (and each Aave V4 spoke entry's) current LTV
// is dated by the block where it took that value, found by bisecting archive
// reads. The transaction is the protocol's config event in that block.

// History reads past chain state from an archive node.
type History interface {
	// CallAt runs eth_call at a block.
	CallAt(ctx context.Context, block uint64, to string, data []byte) ([]byte, error)
	// BlockTime returns a block's timestamp.
	BlockTime(ctx context.Context, block uint64) (time.Time, error)
	// LogTx returns the transaction of the first log in block from address
	// matching topics (each position a list of alternatives, nil for any),
	// or "" when there is none.
	LogTx(ctx context.Context, block uint64, address string, topics [][]string) (string, error)
}

// LTVChange dates a current LTV. At is nil, and Block 0, when the value has not
// changed since the market (or spoke reserve) was listed.
type LTVChange struct {
	Protocol string
	GroupID  string // "" for a market, the lowercase spoke address for an Aave V4 spoke entry
	Asset    string // lowercase underlying, NativeAVAX for BENQI qiAVAX
	LTVBps   uint16 // the value being dated
	Block    uint64
	At       *time.Time
	Tx       string
}

// Config events, used to find the transaction in the change block.
var (
	topicAaveV3CollateralConfig = topicHex("CollateralConfigurationChanged(address,uint256,uint256,uint256)")
	topicBenqiCollateralFactor  = topicHex("NewCollateralFactor(address,uint256,uint256)")
	topicAaveV4AddDynamic       = topicHex("AddDynamicReserveConfig(uint256,uint32,(uint16,uint32,uint16))")
	topicAaveV4UpdateDynamic    = topicHex("UpdateDynamicReserveConfig(uint256,uint32,(uint16,uint32,uint16))")
)

func topicHex(sig string) string {
	return "0x" + hex.EncodeToString(keccak([]byte(sig)))
}

func wordHex(w []byte) string { return "0x" + hex.EncodeToString(w) }

// errNotThere marks a read that failed because the contract or reserve did not
// exist yet at that block. Bisection treats it as "not yet".
var errNotThere = errors.New("not deployed or not listed at block")

// isRevert reports whether an RPC error is the contract reverting, as opposed
// to a transport failure.
func isRevert(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "revert") || strings.Contains(s, "invalid opcode")
}

// callAt runs one historical eth_call and requires at least n return words.
// Missing code or a revert returns errNotThere.
func callAt(ctx context.Context, h History, block uint64, to string, n int, sig string, args ...[]byte) ([]byte, error) {
	b, err := h.CallAt(ctx, block, to, encode(sig, args...))
	if err != nil {
		if isRevert(err) {
			return nil, errNotThere
		}
		return nil, fmt.Errorf("%s on %s at %d: %w", sig, to, block, err)
	}
	if len(b) < n*32 {
		return nil, errNotThere
	}
	return b, nil
}

// firstTrue returns the first block in (lo, hi] where pred holds, given that it
// does not hold at lo and does at hi, and flips once in between.
func firstTrue(ctx context.Context, lo, hi uint64, pred func(uint64) (bool, error)) (uint64, error) {
	for hi-lo > 1 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		mid := lo + (hi-lo)/2
		ok, err := pred(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, nil
}

// BlockAtTime returns the last block at or before t, searching [1, head].
func BlockAtTime(ctx context.Context, h History, t time.Time, head uint64) (uint64, error) {
	if ht, err := h.BlockTime(ctx, head); err != nil {
		return 0, err
	} else if !ht.After(t) {
		return head, nil
	}
	// First block after t, minus one.
	b, err := firstTrue(ctx, 0, head, func(n uint64) (bool, error) {
		bt, err := h.BlockTime(ctx, n)
		if err != nil {
			return false, err
		}
		return bt.After(t), nil
	})
	if err != nil {
		return 0, err
	}
	return b - 1, nil
}

// marketLTVAt reads an Aave V3 or BENQI market's LTV at a block.
type marketLTVAt func(block uint64) (uint16, error)

// DateMarketLTV dates an Aave V3 or BENQI market's current LTV, cur, which it
// did not have at loBlock and has at hiBlock. market is the BENQI qiToken
// (ignored for Aave V3). The window should hold a single change, which a day
// of snapshots does in practice.
func DateMarketLTV(ctx context.Context, h History, protocol, market, asset string, cur uint16, loBlock, hiBlock uint64) (LTVChange, error) {
	ch := LTVChange{Protocol: protocol, Asset: asset, LTVBps: cur}
	var at marketLTVAt
	var logAddr string
	var topics [][]string
	switch protocol {
	case ProtocolAaveV3:
		pool, err := callAt(ctx, h, hiBlock, AaveV3Provider, 1, "getPool()")
		if err != nil {
			return ch, err
		}
		conf, err := callAt(ctx, h, hiBlock, AaveV3Provider, 1, "getPoolConfigurator()")
		if err != nil {
			return ch, err
		}
		poolAddr, confAddr := wordAddr(pool, 0), wordAddr(conf, 0)
		at = func(b uint64) (uint16, error) {
			c, err := callAt(ctx, h, b, poolAddr, 1, "getConfiguration(address)", addrWord(asset))
			if err != nil {
				return 0, err
			}
			// Bits 0-15 of the reserve configuration bitmap.
			return uint16(wordUint(c, 0).Uint64() & 0xFFFF), nil
		}
		logAddr, topics = confAddr, [][]string{{topicAaveV3CollateralConfig}, {wordHex(addrWord(asset))}}
	case ProtocolBenqi:
		at = func(b uint64) (uint16, error) {
			m, err := callAt(ctx, h, b, BenqiComptroller, 2, "markets(address)", addrWord(market))
			if err != nil {
				return 0, err
			}
			return cfBps(wordUint(m, 1)), nil
		}
		logAddr, topics = BenqiComptroller, [][]string{{topicBenqiCollateralFactor}}
	default:
		return ch, fmt.Errorf("no market LTV dating for %s", protocol)
	}

	pred := func(b uint64) (bool, error) {
		v, err := at(b)
		if errors.Is(err, errNotThere) {
			return false, nil
		}
		return v == cur, err
	}
	if ok, err := pred(loBlock); err != nil {
		return ch, err
	} else if ok {
		return ch, fmt.Errorf("%s %s already at LTV %d at block %d", protocol, asset, cur, loBlock)
	}
	if ok, err := pred(hiBlock); err != nil {
		return ch, err
	} else if !ok {
		return ch, fmt.Errorf("%s %s not at LTV %d at block %d", protocol, asset, cur, hiBlock)
	}
	b, err := firstTrue(ctx, loBlock, hiBlock, pred)
	if err != nil {
		return ch, err
	}
	return finishChange(ctx, h, ch, b, logAddr, topics)
}

func finishChange(ctx context.Context, h History, ch LTVChange, block uint64, logAddr string, topics [][]string) (LTVChange, error) {
	t, err := h.BlockTime(ctx, block)
	if err != nil {
		return ch, err
	}
	tx, err := h.LogTx(ctx, block, logAddr, topics)
	if err != nil {
		return ch, err
	}
	t = t.UTC()
	ch.Block, ch.At, ch.Tx = block, &t, tx
	return ch, nil
}

// DateAaveV4Spokes dates each Aave V4 spoke reserve's current collateral
// factor. A spoke reserve's dynamic config keys only go up, and each new key
// can carry a new factor. The current value took effect with the first key of
// the trailing run of keys that share it, so the change block is where the
// reserve's key reached that one and its factor equals the current value.
// Entries for which skip returns true are left out (already dated).
func DateAaveV4Spokes(ctx context.Context, h History, head uint64, skip func(spoke, asset string, cf uint16) bool) ([]LTVChange, error) {
	var out []LTVChange
	for _, spoke := range AaveV4Spokes {
		key := strings.ToLower(spoke)
		nb, err := callAt(ctx, h, head, spoke, 1, "getReserveCount()")
		if err != nil {
			return out, err
		}
		for i := uint64(0); i < wordUint(nb, 0).Uint64(); i++ {
			res, err := callAt(ctx, h, head, spoke, 7, "getReserve(uint256)", uintWord(i))
			if err != nil {
				return out, err
			}
			asset, dk := wordAddr(res, 0), wordUint(res, 6).Uint64()
			cfOf := func(b, k uint64) (uint16, error) {
				d, err := callAt(ctx, h, b, spoke, 1, "getDynamicReserveConfig(uint256,uint32)", uintWord(i), uintWord(k))
				if err != nil {
					return 0, err
				}
				return uint16(wordUint(d, 0).Uint64()), nil
			}
			cur, err := cfOf(head, dk)
			if err != nil {
				return out, err
			}
			if skip != nil && skip(key, asset, cur) {
				continue
			}
			ch := LTVChange{Protocol: ProtocolAaveV4, GroupID: key, Asset: asset, LTVBps: cur}
			start := dk
			for start > 0 {
				prev, err := cfOf(head, start-1)
				if err != nil {
					return out, err
				}
				if prev != cur {
					break
				}
				start--
			}
			if start == 0 {
				// Unchanged since the reserve's first config.
				out = append(out, ch)
				continue
			}
			pred := func(b uint64) (bool, error) {
				r, err := callAt(ctx, h, b, spoke, 7, "getReserve(uint256)", uintWord(i))
				if errors.Is(err, errNotThere) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				k := wordUint(r, 6).Uint64()
				if k < start {
					return false, nil
				}
				v, err := cfOf(b, k)
				if errors.Is(err, errNotThere) {
					return false, nil
				}
				return v == cur, err
			}
			b, err := firstTrue(ctx, 0, head, pred)
			if err != nil {
				return out, err
			}
			topics := [][]string{{topicAaveV4AddDynamic, topicAaveV4UpdateDynamic}, {wordHex(uintWord(i))}}
			if ch, err = finishChange(ctx, h, ch, b, spoke, topics); err != nil {
				return out, err
			}
			out = append(out, ch)
		}
	}
	return out, nil
}
