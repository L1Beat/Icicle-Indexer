// Package rwaprices reads on-chain USD prices for the floating-price registry
// assets (tokenized funds, equities, commodities) on the C-Chain: a curated
// RedStone or Chainlink feed, an ERC-4626 vault share, or an ERC-7575 share
// token priced through its ERC-7540 vault. It only reads; pchainsyncer writes
// the tokenized_fund_prices rows.
package rwaprices

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"icicle/pkg/ethcall"
	"math/big"
	"strings"
	"time"
)

// Caller runs eth_call against a contract and returns the raw return data.
type Caller = ethcall.Caller

var (
	selAsset            = []byte{0x38, 0xd5, 0x2e, 0x0f} // asset()
	selConvertToAssets  = []byte{0x07, 0xa2, 0xd1, 0x3a} // convertToAssets(uint256)
	selVault            = []byte{0xf8, 0x15, 0xc0, 0x3d} // vault(address), ERC-7575 share token
	selShare            = []byte{0xa8, 0xd5, 0xfd, 0x65} // share(), ERC-7575 vault
	selPriceLastUpdated = []byte{0x52, 0xaa, 0xea, 0xd8} // priceLastUpdated(), Centrifuge vaults
	selDescription      = []byte{0x72, 0x84, 0xe4, 0x16} // description(), Chainlink-style feed
	selDecimals         = []byte{0x31, 0x3c, 0xe5, 0x67} // decimals()
	selLatestRoundData  = []byte{0xfe, 0xaf, 0x96, 0x8c} // latestRoundData()
)

// navFeed is a price feed (Chainlink AggregatorV3 interface) for an asset with
// no price in its own contracts. source is "redstone" or "chainlink". id names
// the feed: a RedStone description() must end in its data feed ID, a
// Chainlink description() must equal it, so a wrong or repointed address is
// rejected rather than read.
type navFeed struct {
	feed   string
	id     string
	source string
}

// priceFeeds maps an asset token (lowercase hex, no 0x) to its price feed
// on the C-Chain. RedStone addresses from its relayer manifest
// (redstone-oracles-monorepo, relayer-manifests-multi-feed/avalancheMultiFeed.json,
// adapter 0x3587a73AA02519335A8a6053a97657BECe0bC2Cc), checked on-chain 2026-11-19.
// Updated on a 0.01% move or every 24h.
//
// XAUt0 (one token per troy ounce of gold) reads Chainlink's XAU / USD proxy,
// from Chainlink's registry (smartcontractkit/hardhat-chainlink,
// src/registries/dataFeedsRegistry.ts, avalanche xau-usd), checked on-chain
// 2026-11-20. Gold only updates while markets are open, so weekend gaps are
// normal: the API's 7-day NAV age limit covers them.
var priceFeeds = map[string]navFeed{
	"f5fa39a16449b1ee07aa52ae79c6b00567c59f83": {"0xc9e11c60e24bef478cc999fa9fa2d89cc098a86e", "HINC_AVALANCHE_FUNDAMENTAL", "redstone"}, // HINC
	"7c64925002bfa705834b118a923e9911bee32875": {"0x4bad96dd1c7d541270a0c92e1d4e5f12eeea7a57", "ACRED_FUNDAMENTAL", "redstone"},          // ACRED
	"5954ff4099ac47c4d6d098a9216f3278bb8c9506": {"0x29d2fec890b037b2d34f061f9a50f76f85ddbcae", "SECZ_EOD", "redstone"},                   // SECZ, end-of-day equity price, updated on 0.1% or 12h
	"2775d5105276781b4b85ba6ea6a6653beed1dd32": {"0x1f41ef93dece881ad0b98082b2d44d3f6f0c515b", "XAU / USD", "chainlink"},                 // XAUt0
}

// preferredUSD are the USD stablecoins tried first, in order, when asking an
// ERC-7575 share token for its vault: USDC, USDT, USDC.e, USDT.e. Fund vaults
// settle in these, so the rest of the registry is not tried.
var preferredUSD = []string{
	"b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e",
	"9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7",
	"a7d7079b0fead91f3e65f86e8915cb59c1a4c664",
	"c7198437980c041c805a1edcba50c1ce5db95118",
}

// Token is a stablecoin-registry token with its decimals.
type Token struct {
	Address  []byte // 20 bytes
	Decimals uint8
}

// Price is one on-chain share price.
type Price struct {
	USD     float64
	Source  string    // erc4626, erc7540, redstone or chainlink
	PriceAt time.Time // when the NAV was set, if the vault says, else the read time
}

func isRevert(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "revert")
}

// word returns the address in a 32-byte ABI word, or nil.
func word(out []byte) []byte {
	if len(out) != 32 {
		return nil
	}
	return out[12:32]
}

func addrArg(a []byte) []byte { return append(make([]byte, 12), a...) }

// Read returns the USD price of one whole share of fund f. ok is
// false, with no error, when the fund has no on-chain price in a USD
// stablecoin of usd (token bytes -> decimals).
func Read(ctx context.Context, c Caller, f Token, usd map[string]uint8, now time.Time) (Price, bool, error) {
	fund := "0x" + hex.EncodeToString(f.Address)

	// A curated feed wins: it is the issuer's published NAV or the market price.
	if nf, ok := priceFeeds[hex.EncodeToString(f.Address)]; ok {
		p, err := readNAVFeed(ctx, c, nf, now)
		if err != nil {
			return Price{}, false, err
		}
		return p, true, nil
	}

	// ERC-4626: the fund token is the vault.
	out, err := c.Call(ctx, fund, selAsset)
	if err != nil && !isRevert(err) {
		return Price{}, false, err
	}
	if a := word(out); err == nil && a != nil {
		if dec, isUSD := usd[string(a)]; isUSD {
			v, err := convertShare(ctx, c, fund, f.Decimals, dec)
			if err != nil {
				return Price{}, false, err
			}
			return Price{USD: v, Source: "erc4626", PriceAt: now}, true, nil
		}
		return Price{}, false, nil
	}

	// ERC-7575: the fund token is a share token with one ERC-7540 vault per
	// settlement asset.
	for _, h := range preferredUSD {
		stable, _ := hex.DecodeString(h)
		dec, inRegistry := usd[string(stable)]
		if !inRegistry {
			continue
		}
		out, err := c.Call(ctx, fund, append(append([]byte{}, selVault...), addrArg(stable)...))
		if isRevert(err) {
			return Price{}, false, nil // not an ERC-7575 share token
		}
		if err != nil {
			return Price{}, false, err
		}
		vaultAddr := word(out)
		if vaultAddr == nil || bytes.Equal(vaultAddr, make([]byte, 20)) {
			continue
		}
		vault := "0x" + hex.EncodeToString(vaultAddr)
		// The vault must point back at this fund and this stablecoin.
		share, err := c.Call(ctx, vault, selShare)
		if err != nil || !bytes.Equal(word(share), f.Address) {
			return Price{}, false, fmt.Errorf("vault %s does not hold this share", vault)
		}
		asset, err := c.Call(ctx, vault, selAsset)
		if err != nil || !bytes.Equal(word(asset), stable) {
			return Price{}, false, fmt.Errorf("vault %s does not settle in 0x%s", vault, h)
		}
		v, err := convertShare(ctx, c, vault, f.Decimals, dec)
		if err != nil {
			return Price{}, false, err
		}
		p := Price{USD: v, Source: "erc7540", PriceAt: now}
		// Centrifuge vaults say when the NAV was last set.
		if out, err := c.Call(ctx, vault, selPriceLastUpdated); err == nil && len(out) == 32 {
			if ts := new(big.Int).SetBytes(out); ts.Sign() > 0 && ts.IsInt64() && ts.Int64() <= now.Unix() {
				p.PriceAt = time.Unix(ts.Int64(), 0).UTC()
			}
		}
		return p, true, nil
	}
	return Price{}, false, nil
}

// convertShare reads convertToAssets(one whole share) from vault and scales it
// by the asset's decimals.
func convertShare(ctx context.Context, c Caller, vault string, shareDec, assetDec uint8) (float64, error) {
	oneShare := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(shareDec)), nil)
	arg := make([]byte, 32)
	oneShare.FillBytes(arg)
	out, err := c.Call(ctx, vault, append(append([]byte{}, selConvertToAssets...), arg...))
	if err != nil {
		return 0, err
	}
	if len(out) != 32 {
		return 0, fmt.Errorf("convertToAssets returned %d bytes", len(out))
	}
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(assetDec)), nil)
	price, _ := new(big.Float).Quo(new(big.Float).SetInt(new(big.Int).SetBytes(out)), new(big.Float).SetInt(unit)).Float64()
	// A share worth nothing or a million dollars is a broken read, not a NAV.
	if price <= 0 || price > 1e6 {
		return 0, fmt.Errorf("implausible share price %v", price)
	}
	return price, nil
}

// readNAVFeed reads a RedStone or Chainlink feed: USD per token from
// latestRoundData, stamped with its updatedAt.
func readNAVFeed(ctx context.Context, c Caller, nf navFeed, now time.Time) (Price, error) {
	out, err := c.Call(ctx, nf.feed, selDescription)
	if err != nil {
		return Price{}, err
	}
	desc := abiString(out)
	if nf.source == "redstone" && !strings.HasSuffix(desc, " "+nf.id) || nf.source != "redstone" && desc != nf.id {
		return Price{}, fmt.Errorf("feed %s describes itself as %q, want %s", nf.feed, desc, nf.id)
	}
	out, err = c.Call(ctx, nf.feed, selDecimals)
	if err != nil {
		return Price{}, err
	}
	if len(out) != 32 || new(big.Int).SetBytes(out).Cmp(big.NewInt(36)) > 0 {
		return Price{}, fmt.Errorf("feed %s: bad decimals", nf.feed)
	}
	dec := new(big.Int).SetBytes(out).Int64()
	out, err = c.Call(ctx, nf.feed, selLatestRoundData)
	if err != nil {
		return Price{}, err
	}
	if len(out) != 5*32 {
		return Price{}, fmt.Errorf("feed %s: latestRoundData returned %d bytes", nf.feed, len(out))
	}
	answer := new(big.Int).SetBytes(out[32:64]) // int256, a negative NAV is caught below
	if out[32]&0x80 != 0 {
		return Price{}, fmt.Errorf("feed %s: negative answer", nf.feed)
	}
	updated := new(big.Int).SetBytes(out[96:128])
	if updated.Sign() <= 0 || !updated.IsInt64() || updated.Int64() > now.Unix() {
		return Price{}, fmt.Errorf("feed %s: bad updatedAt %s", nf.feed, updated)
	}
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(dec), nil)
	price, _ := new(big.Float).Quo(new(big.Float).SetInt(answer), new(big.Float).SetInt(unit)).Float64()
	if price <= 0 || price > 1e6 {
		return Price{}, fmt.Errorf("feed %s: implausible share price %v", nf.feed, price)
	}
	return Price{USD: price, Source: nf.source, PriceAt: time.Unix(updated.Int64(), 0).UTC()}, nil
}

// abiString decodes an ABI-encoded string return value, or "".
func abiString(out []byte) string {
	if len(out) < 64 {
		return ""
	}
	n := new(big.Int).SetBytes(out[32:64])
	if !n.IsInt64() || 64+n.Int64() > int64(len(out)) {
		return ""
	}
	return string(out[64 : 64+n.Int64()])
}
