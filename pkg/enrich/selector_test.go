package enrich

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectorOf_KnownSelectors(t *testing.T) {
	assert.Equal(t, "0xa9059cbb", SelectorHex(SelectorOf("transfer(address,uint256)")))
	assert.Equal(t, "0x095ea7b3", SelectorHex(SelectorOf("approve(address,uint256)")))
	// From the frontend team's example.
	assert.Equal(t, "0xa214fa6b", SelectorHex(SelectorOf("burn(uint256,uint128,address,uint256,uint256,bool)")))
	// Tuple parameters hash on their canonical text.
	assert.Equal(t, "0x82ad56cb", SelectorHex(SelectorOf("aggregate3((address,bool,bytes)[])")))
}

func TestFunctionName(t *testing.T) {
	assert.Equal(t, "burn", FunctionName("burn(uint256,uint128,address,uint256,uint256,bool)"))
	assert.Equal(t, "deposit", FunctionName("deposit()"))
	assert.Equal(t, "weird", FunctionName("weird"))
}

func TestPlausible_StaticExact(t *testing.T) {
	sig := "transfer(address,uint256)"
	assert.True(t, Plausible(sig, 4+64))
	assert.False(t, Plausible(sig, 4+32), "too short for two static words")
	assert.False(t, Plausible(sig, 4+96), "static-only signatures must match exactly")
	assert.True(t, Plausible("deposit()", 4))
	assert.False(t, Plausible("deposit()", 4+32))
}

func TestPlausible_Dynamic(t *testing.T) {
	sig := "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)"
	assert.False(t, Plausible(sig, 4+4*32), "needs five head words")
	assert.True(t, Plausible(sig, 4+5*32+2*32+32))
	// Static tuple counts its components; dynamic array of tuples is one word.
	assert.True(t, Plausible("f((uint256,address))", 4+64))
	assert.False(t, Plausible("f((uint256,address))", 4+32))
	assert.True(t, Plausible("aggregate3((address,bool,bytes)[])", 4+32+32+32+96))
	assert.True(t, Plausible("g(uint256[3])", 4+96))
	assert.False(t, Plausible("g(uint256[3])", 4+32))
}

func TestPlausible_UnparseableIsPlausible(t *testing.T) {
	assert.True(t, Plausible("notASignature", 4+32))
	assert.True(t, Plausible("f(mysterytype)", 4+32))
}

func TestChooseSignature_PrefersPlausibleThenPriority(t *testing.T) {
	// 0xa9059cbb collides with spam entries that OpenChain also returns.
	cands := []Candidate{
		{Signature: "many_msg_babbage(bytes1)", Priority: PriorityOpenChain},
		{Signature: "transfer(bytes4[9],bytes5[6],int48[11])", Priority: PriorityOpenChain},
		{Signature: "transfer(address,uint256)", Priority: PriorityOpenChainVerified},
	}
	sig, ok := ChooseSignature(cands, 4+64)
	require.True(t, ok)
	assert.Equal(t, "transfer(address,uint256)", sig)

	// With calldata that fits only the odd one, plausibility wins over priority.
	sig, ok = ChooseSignature(cands, 4+32)
	require.True(t, ok)
	assert.Equal(t, "many_msg_babbage(bytes1)", sig)

	// Nothing plausible: fall back to best priority.
	sig, ok = ChooseSignature(cands, 4+5*32)
	require.True(t, ok)
	assert.Equal(t, "transfer(address,uint256)", sig)
}

func TestChooseSignature_SeedBeatsOpenChain(t *testing.T) {
	cands := []Candidate{
		{Signature: "zzz(address,uint256)", Priority: PriorityOpenChainVerified},
		{Signature: "transfer(address,uint256)", Priority: PrioritySeed},
	}
	sig, ok := ChooseSignature(cands, 4+64)
	require.True(t, ok)
	assert.Equal(t, "transfer(address,uint256)", sig)
}

func TestChooseSignature_TieBreakShorter(t *testing.T) {
	cands := []Candidate{
		{Signature: "bbbbbbbb(address,uint256)", Priority: PriorityOpenChain},
		{Signature: "aaa(address,uint256)", Priority: PriorityOpenChain},
	}
	sig, _ := ChooseSignature(cands, 4+64)
	assert.Equal(t, "aaa(address,uint256)", sig)
}

func TestChooseSignature_Empty(t *testing.T) {
	_, ok := ChooseSignature(nil, 68)
	assert.False(t, ok)
	_, ok = ChooseSignature([]Candidate{{Signature: ""}}, 68)
	assert.False(t, ok)
}

func TestSeedSignatures_ValidAndUnique(t *testing.T) {
	rows := SeedSignatures()
	require.Greater(t, len(rows), 50)
	seen := map[string]bool{}
	for _, r := range rows {
		assert.False(t, seen[r.Signature], "duplicate seed entry %s", r.Signature)
		seen[r.Signature] = true
		params, ok := SplitParams(r.Signature)
		require.True(t, ok, "seed entry %q is not name(...)", r.Signature)
		for _, p := range params {
			_, _, ok := headWords(p)
			assert.True(t, ok, "seed entry %q has unparseable param %q", r.Signature, p)
		}
		assert.Equal(t, PrioritySeed, r.Priority)
		assert.Equal(t, FunctionName(r.Signature), r.Name)
	}
}

func TestSplitParams_Nested(t *testing.T) {
	params, ok := SplitParams("f(uint256,(address,bytes)[],address)")
	require.True(t, ok)
	assert.Equal(t, []string{"uint256", "(address,bytes)[]", "address"}, params)
	params, ok = SplitParams("deposit()")
	require.True(t, ok)
	assert.Empty(t, params)
}
