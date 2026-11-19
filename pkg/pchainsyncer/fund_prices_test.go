package pchainsyncer

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addr(h string) []byte { b, _ := hex.DecodeString(h); return b }

var (
	usdc     = addr("b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e")
	fundAddr = addr("58f93d6b1ef2f44ec379cb975657c132cbed3b6b")
	vaultAdr = addr("1121f4e21ed8b9bc1bb9a2952cdd8639ac897784")
)

// fakeChain answers eth_calls by (contract, selector). A missing entry reverts.
type fakeChain map[string][]byte

func key(to string, sel []byte) string { return to + ":" + hex.EncodeToString(sel[:4]) }

func (f fakeChain) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	if out, ok := f[key(to, data)]; ok {
		return out, nil
	}
	return nil, errors.New("execution reverted")
}

func uintWord(v int64) []byte {
	out := make([]byte, 32)
	big.NewInt(v).FillBytes(out)
	return out
}

func hx(b []byte) string { return "0x" + hex.EncodeToString(b) }

func TestReadFundPrice(t *testing.T) {
	now := time.Date(2026, 11, 19, 19, 0, 0, 0, time.UTC)
	usd := map[string]uint8{string(usdc): 6}
	fund := registryToken{token: fundAddr, decimals: 6}
	ctx := context.Background()

	// ERC-4626: the fund is a vault over USDC (XDFIS-DeFi-2, hyTB).
	p, ok, err := readFundPrice(ctx, fakeChain{
		key(hx(fundAddr), selAsset):           addrArg(usdc),
		key(hx(fundAddr), selConvertToAssets): uintWord(1065417),
	}, fund, usd, now)
	require.NoError(t, err)
	require.True(t, ok)
	assert.InDelta(t, 1.065417, p.usd, 1e-12)
	assert.Equal(t, "erc4626", p.source)
	assert.Equal(t, now, p.priceAt)

	// ERC-7575 share token with a Centrifuge ERC-7540 vault (JAAA): the NAV
	// time comes from priceLastUpdated.
	centrifuge := fakeChain{
		key(hx(fundAddr), selVault):            addrArg(vaultAdr),
		key(hx(vaultAdr), selShare):            addrArg(fundAddr),
		key(hx(vaultAdr), selAsset):            addrArg(usdc),
		key(hx(vaultAdr), selConvertToAssets):  uintWord(1057215),
		key(hx(vaultAdr), selPriceLastUpdated): uintWord(1795003200),
	}
	p, ok, err = readFundPrice(ctx, centrifuge, fund, usd, now)
	require.NoError(t, err)
	require.True(t, ok)
	assert.InDelta(t, 1.057215, p.usd, 1e-12)
	assert.Equal(t, "erc7540", p.source)
	assert.Equal(t, time.Unix(1795003200, 0).UTC(), p.priceAt)

	// A vault without priceLastUpdated (Nest nOPAL) is stamped with the read time.
	delete(centrifuge, key(hx(vaultAdr), selPriceLastUpdated))
	p, ok, err = readFundPrice(ctx, centrifuge, fund, usd, now)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, now, p.priceAt)

	// A vault that does not point back at the fund is an error, not a price.
	centrifuge[key(hx(vaultAdr), selShare)] = addrArg(addr("0000000000000000000000000000000000000001"))
	_, _, err = readFundPrice(ctx, centrifuge, fund, usd, now)
	assert.Error(t, err)

	// Neither a vault nor a share token (ACRED, HINC): no price, no error.
	_, ok, err = readFundPrice(ctx, fakeChain{}, fund, usd, now)
	require.NoError(t, err)
	assert.False(t, ok)

	// A zero share price is a broken read.
	_, _, err = readFundPrice(ctx, fakeChain{
		key(hx(fundAddr), selAsset):           addrArg(usdc),
		key(hx(fundAddr), selConvertToAssets): uintWord(0),
	}, fund, usd, now)
	assert.Error(t, err)
}

func abiStr(s string) []byte {
	out := append(uintWord(32), uintWord(int64(len(s)))...)
	pad := make([]byte, (len(s)+31)/32*32)
	copy(pad, s)
	return append(out, pad...)
}

func TestReadFundPriceRedStone(t *testing.T) {
	now := time.Date(2026, 11, 19, 19, 0, 0, 0, time.UTC)
	hinc := addr("f5fa39a16449b1ee07aa52ae79c6b00567c59f83")
	feed := redstoneFeeds[hex.EncodeToString(hinc)].feed
	round := append(append(append(append(uintWord(1), uintWord(97729722200)...), uintWord(1795107585)...), uintWord(1795107585)...), uintWord(1)...)
	chain := fakeChain{
		key(feed, selDescription):     abiStr("RedStone Price Feed for HINC_AVALANCHE_FUNDAMENTAL"),
		key(feed, selDecimals):        uintWord(8),
		key(feed, selLatestRoundData): round,
	}
	fund := registryToken{token: hinc, decimals: 6}

	p, ok, err := readFundPrice(context.Background(), chain, fund, nil, now)
	require.NoError(t, err)
	require.True(t, ok)
	assert.InDelta(t, 977.297222, p.usd, 1e-9)
	assert.Equal(t, "redstone", p.source)
	assert.Equal(t, time.Unix(1795107585, 0).UTC(), p.priceAt)

	// A feed that names another data feed is rejected, not read.
	chain[key(feed, selDescription)] = abiStr("RedStone Price Feed for ACRED_FUNDAMENTAL")
	_, _, err = readFundPrice(context.Background(), chain, fund, nil, now)
	assert.Error(t, err)
}
