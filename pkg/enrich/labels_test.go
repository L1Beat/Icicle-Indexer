package enrich

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const usdc = "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e"

func TestMergeLabels_CuratedBeatsVerifiedBeatsToken(t *testing.T) {
	labels := []Label{
		{Address: usdc, Name: "USD Coin (USDC)", Kind: "token", Source: SourceTokenMetadata, IsContract: true},
		{Address: usdc, Name: "FiatTokenV2_2", Kind: "token", Source: SourceVerifiedSource, IsContract: true, Verified: true, Implementation: "0xa3fa3d254bf6af295b5b22cc6730b04144314890"},
		{Address: usdc, Name: "USD Coin", Kind: "token", Protocol: "Circle", Source: SourceCurated, IsContract: true, Verified: true},
	}
	got := MergeLabels(labels)
	require.NotNil(t, got)
	assert.Equal(t, "USD Coin", got.Name, "curated name wins")
	assert.Equal(t, "Circle", got.Protocol)
	assert.Equal(t, "0xa3fa3d254bf6af295b5b22cc6730b04144314890", got.Implementation, "implementation inherited from verified-source")
	assert.True(t, got.Verified)
	assert.Equal(t, SourceCurated, got.Source)

	// Without curated, verified-source wins over token metadata.
	got = MergeLabels(labels[:2])
	require.NotNil(t, got)
	assert.Equal(t, "FiatTokenV2_2", got.Name)
	assert.Equal(t, SourceVerifiedSource, got.Source)

	// Token metadata alone.
	got = MergeLabels(labels[:1])
	require.NotNil(t, got)
	assert.Equal(t, "USD Coin (USDC)", got.Name)
	assert.Equal(t, "token", got.Kind)
	assert.False(t, got.Verified)
}

func TestMergeLabels_OrderIndependent(t *testing.T) {
	a := Label{Name: "curated", Source: SourceCurated, Kind: "router"}
	b := Label{Name: "verified", Source: SourceVerifiedSource, Kind: "contract"}
	c := Label{Name: "token", Source: SourceTokenMetadata, Kind: "token"}
	for _, order := range [][]Label{{a, b, c}, {c, b, a}, {b, a, c}, {c, a, b}} {
		got := MergeLabels(order)
		require.NotNil(t, got)
		assert.Equal(t, "curated", got.Name)
		assert.Equal(t, "router", got.Kind, "winner keeps its own kind")
	}
}

func TestMergeLabels_FillsMissingFieldsFromLowerSources(t *testing.T) {
	got := MergeLabels([]Label{
		{Name: "My Router", Source: SourceCurated},
		{Name: "RouterV2", Kind: "router", Protocol: "Pharaoh", Source: SourceVerifiedSource, Verified: true, Implementation: "0x1111111111111111111111111111111111111111"},
	})
	require.NotNil(t, got)
	assert.Equal(t, "My Router", got.Name)
	assert.Equal(t, "router", got.Kind)
	assert.Equal(t, "Pharaoh", got.Protocol)
	assert.True(t, got.Verified)
	assert.True(t, got.IsContract)
	assert.Equal(t, "0x1111111111111111111111111111111111111111", got.Implementation)
}

func TestMergeLabels_VerifiedGenericContractPromotedToToken(t *testing.T) {
	got := MergeLabels([]Label{
		{Name: "BridgeToken", Kind: "contract", Source: SourceVerifiedSource, Verified: true},
		{Name: "Wrapped Ether (WETH.e)", Kind: "token", Source: SourceTokenMetadata},
	})
	require.NotNil(t, got)
	assert.Equal(t, "BridgeToken", got.Name)
	assert.Equal(t, "token", got.Kind)
}

func TestMergeLabels_IgnoresNegativeCacheAndEmpty(t *testing.T) {
	assert.Nil(t, MergeLabels(nil))
	assert.Nil(t, MergeLabels([]Label{{Source: SourceUnverified, IsContract: true}}))
	assert.Nil(t, MergeLabels([]Label{{Name: "", Source: SourceCurated}}))
	assert.Nil(t, MergeLabels([]Label{{Name: "x", Source: SourceUnverified}}), "unverified rows never surface even if named")
}

func TestKindFromContractName(t *testing.T) {
	assert.Equal(t, "router", KindFromContractName("JoeRouter02"))
	assert.Equal(t, "router", KindFromContractName("AugustusSwapper"))
	assert.Equal(t, "bridge", KindFromContractName("TokenMessenger"))
	assert.Equal(t, "pool", KindFromContractName("LBPair"))
	assert.Equal(t, "token", KindFromContractName("FiatTokenV2_2"))
	assert.Equal(t, "contract", KindFromContractName("Multicall3"))
}

func TestIsProxyName(t *testing.T) {
	assert.True(t, IsProxyName("TransparentUpgradeableProxy"))
	assert.True(t, IsProxyName("Unitroller"))
	assert.True(t, IsProxyName("QiErc20Delegator"))
	assert.False(t, IsProxyName("JoeRouter02"))
}

func TestCuratedLabels_LoadsAndValidates(t *testing.T) {
	all, err := CuratedLabels()
	require.NoError(t, err)
	avax := all[43114]
	require.NotEmpty(t, avax)
	seen := map[string]bool{}
	for _, l := range avax {
		assert.False(t, seen[l.Address], "duplicate curated address %s", l.Address)
		seen[l.Address] = true
		assert.Equal(t, SourceCurated, l.Source)
		assert.True(t, l.IsContract)
		assert.Regexp(t, `^0x[0-9a-f]{40}$`, l.Address)
	}
	assert.True(t, seen[usdc], "USDC should be curated")
}

func TestTokenLabel(t *testing.T) {
	assert.Equal(t, "USD Coin (USDC)", TokenLabel(usdc, TokenInfo{Name: "USD Coin", Symbol: "USDC"}).Name)
	assert.Equal(t, "USDC", TokenLabel(usdc, TokenInfo{Symbol: "USDC"}).Name)
	assert.Equal(t, "WAVAX", TokenLabel(usdc, TokenInfo{Name: "WAVAX", Symbol: "WAVAX"}).Name)
}

func TestNormalizeAddress(t *testing.T) {
	got, ok := NormalizeAddress(" 0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E ")
	require.True(t, ok)
	assert.Equal(t, usdc, got)
	_, ok = NormalizeAddress("0x123")
	assert.False(t, ok)
	_, ok = NormalizeAddress("0xZZ7ef9ef8734c71904d8002f8b6bc66dd9c48a6e")
	assert.False(t, ok)
}
