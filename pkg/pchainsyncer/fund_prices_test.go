package pchainsyncer

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeVault struct {
	asset   []byte // returned by asset(), nil to revert
	perUnit *big.Int
}

func (f fakeVault) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	switch {
	case bytes.Equal(data, selAsset):
		if f.asset == nil {
			return nil, errors.New("execution reverted")
		}
		return append(make([]byte, 12), f.asset...), nil
	case bytes.HasPrefix(data, selConvertToAssets):
		out := make([]byte, 32)
		f.perUnit.FillBytes(out)
		return out, nil
	}
	return nil, errors.New("unexpected call")
}

func TestReadVaultPrice(t *testing.T) {
	usdc, _ := hex.DecodeString("b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e")
	other, _ := hex.DecodeString("0000000000000000000000000000000000000001")
	usd := map[string]uint8{string(usdc): 6}
	fund := registryToken{token: make([]byte, 20), decimals: 18}

	// XDFIS-style vault over USDC: one share converts to 1.065417 USDC.
	price, ok, err := readVaultPrice(context.Background(), fakeVault{asset: usdc, perUnit: big.NewInt(1065417)}, fund, usd)
	require.NoError(t, err)
	require.True(t, ok)
	assert.InDelta(t, 1.065417, price, 1e-12)

	// Not a vault: asset() reverts.
	_, ok, err = readVaultPrice(context.Background(), fakeVault{}, fund, usd)
	require.NoError(t, err)
	assert.False(t, ok)

	// A vault over a token that is not a registry USD stablecoin is not priced.
	_, ok, err = readVaultPrice(context.Background(), fakeVault{asset: other, perUnit: big.NewInt(1)}, fund, usd)
	require.NoError(t, err)
	assert.False(t, ok)

	// A zero share price is a broken read.
	_, _, err = readVaultPrice(context.Background(), fakeVault{asset: usdc, perUnit: big.NewInt(0)}, fund, usd)
	assert.Error(t, err)
}
