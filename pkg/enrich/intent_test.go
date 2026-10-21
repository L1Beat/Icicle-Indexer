package enrich

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func method(sig string) *Method {
	return &Method{Selector: SelectorHex(SelectorOf(sig)), Name: FunctionName(sig), Signature: sig}
}

func TestDeriveIntent_PlainSend(t *testing.T) {
	got := DeriveIntent(IntentInput{HasCalldata: false, ValueOut: true, To: "0xabc"})
	require.NotNil(t, got)
	assert.Equal(t, "send", got.Kind)
	assert.Nil(t, got.Protocol)
	assert.Nil(t, DeriveIntent(IntentInput{HasCalldata: false, ValueOut: false, To: "0xabc"}), "nothing happened")
}

func TestDeriveIntent_ApproveAndTokenSend(t *testing.T) {
	got := DeriveIntent(IntentInput{HasCalldata: true, Method: method("approve(address,uint256)")})
	require.NotNil(t, got)
	assert.Equal(t, "approve", got.Kind)

	got = DeriveIntent(IntentInput{HasCalldata: true, Method: method("transfer(address,uint256)"), ToLabel: &Label{Kind: "token", Name: "USDC", Protocol: "Circle"}})
	require.NotNil(t, got)
	assert.Equal(t, "send", got.Kind)
	assert.Equal(t, 0.9, got.Confidence)
	require.NotNil(t, got.Protocol)
	assert.Equal(t, "Circle", *got.Protocol)
}

func TestDeriveIntent_RemoveLiquidityFromExample(t *testing.T) {
	// The frontend's example: burn(uint256,uint128,address,uint256,uint256,bool) on Pharaoh.
	got := DeriveIntent(IntentInput{
		HasCalldata: true,
		Method:      method("burn(uint256,uint128,address,uint256,uint256,bool)"),
		ToLabel:     &Label{Kind: "protocol", Name: "Pharaoh CL Positions NFT", Protocol: "Pharaoh"},
	})
	require.NotNil(t, got)
	assert.Equal(t, "remove_liquidity", got.Kind)
	assert.Equal(t, "Removed liquidity", got.Label)
	assert.Equal(t, "Pharaoh", *got.Protocol)
	assert.Equal(t, 0.9, got.Confidence)

	// Plain ERC-20 burn(uint256) is not liquidity removal.
	got = DeriveIntent(IntentInput{HasCalldata: true, Method: method("burn(uint256)"), ToLabel: &Label{Kind: "token"}})
	assert.Equal(t, "contract_call", got.Kind)
}

func TestDeriveIntent_SwapConfidenceUsesTransfers(t *testing.T) {
	base := IntentInput{HasCalldata: true, Method: method("swapExactTokensForTokens(uint256,uint256,address[],address,uint256)"), ToLabel: &Label{Kind: "router", Protocol: "LFJ"}}
	got := DeriveIntent(base)
	assert.Equal(t, "swap", got.Kind)
	assert.Equal(t, 0.75, got.Confidence, "method only")

	withTransfers := base
	withTransfers.HasTransfer, withTransfers.TokensOut, withTransfers.TokensIn, withTransfers.Transfers = true, 1, 1, 2
	got = DeriveIntent(withTransfers)
	assert.Equal(t, "swap", got.Kind)
	assert.Equal(t, 0.9, got.Confidence, "tokens moved both ways")

	// Universal Router execute() with tokens both ways is a swap; alone it is a weak guess.
	exec := IntentInput{HasCalldata: true, Method: method("execute(bytes,bytes[],uint256)"), ToLabel: &Label{Kind: "router", Protocol: "Uniswap"}}
	assert.Equal(t, 0.6, DeriveIntent(exec).Confidence)
	exec.HasTransfer, exec.ValueOut, exec.TokensIn = true, true, 1
	assert.Equal(t, 0.9, DeriveIntent(exec).Confidence)
}

func TestDeriveIntent_WrapUnwrap(t *testing.T) {
	wavax := &Label{Kind: "token", Name: "Wrapped AVAX", Protocol: "Avalanche"}
	got := DeriveIntent(IntentInput{HasCalldata: true, ValueOut: true, Method: method("deposit()"), ToLabel: wavax})
	assert.Equal(t, "deposit", got.Kind)
	assert.Equal(t, "Wrapped AVAX", got.Label)
	got = DeriveIntent(IntentInput{HasCalldata: true, Method: method("withdraw(uint256)"), ToLabel: wavax})
	assert.Equal(t, "withdraw", got.Kind)
	assert.Equal(t, "Unwrapped AVAX", got.Label)
}

func TestDeriveIntent_LendingAndStaking(t *testing.T) {
	aave := &Label{Kind: "protocol", Protocol: "Aave"}
	assert.Equal(t, "deposit", DeriveIntent(IntentInput{HasCalldata: true, Method: method("supply(address,uint256,address,uint16)"), ToLabel: aave}).Kind)
	assert.Equal(t, "withdraw", DeriveIntent(IntentInput{HasCalldata: true, Method: method("withdraw(address,uint256,address)"), ToLabel: aave}).Kind)
	borrow := DeriveIntent(IntentInput{HasCalldata: true, Method: method("borrow(address,uint256,uint256,uint16,address)"), ToLabel: aave})
	assert.Equal(t, "contract_call", borrow.Kind)
	assert.Equal(t, "Borrowed", borrow.Label)
	assert.Equal(t, "stake", DeriveIntent(IntentInput{HasCalldata: true, Method: method("stake(uint256)")}).Kind)
	assert.Equal(t, "unstake", DeriveIntent(IntentInput{HasCalldata: true, Method: method("unstake(uint256)")}).Kind)
	assert.Equal(t, "claim", DeriveIntent(IntentInput{HasCalldata: true, Method: method("getReward()")}).Kind)
	assert.Equal(t, "claim", DeriveIntent(IntentInput{HasCalldata: true, Method: method("claimReward(uint8,address)")}).Kind)
}

func TestDeriveIntent_Bridge(t *testing.T) {
	got := DeriveIntent(IntentInput{HasCalldata: true, Method: method("multicall(bytes[])"), ToLabel: &Label{Kind: "bridge", Protocol: "Stargate"}})
	assert.Equal(t, "bridge", got.Kind)
	got = DeriveIntent(IntentInput{HasCalldata: true, Method: method("depositForBurn(uint256,uint32,bytes32,address)")})
	assert.Equal(t, "bridge", got.Kind)
}

func TestDeriveIntent_Fallbacks(t *testing.T) {
	got := DeriveIntent(IntentInput{HasCalldata: true, Method: method("rebalancePortfolio(uint256)"), To: "0xabc"})
	assert.Equal(t, "contract_call", got.Kind)
	assert.Equal(t, "Called rebalance portfolio", got.Label)
	assert.Equal(t, 0.5, got.Confidence)

	got = DeriveIntent(IntentInput{HasCalldata: true, To: "0xabc"})
	assert.Equal(t, "Contract call", got.Label)
	assert.Equal(t, 0.3, got.Confidence)

	got = DeriveIntent(IntentInput{HasCalldata: true, To: ""})
	assert.Equal(t, "Deployed contract", got.Label)

	// Unknown selector but tokens moved both ways through a contract.
	got = DeriveIntent(IntentInput{HasCalldata: true, To: "0xabc", HasTransfer: true, TokensOut: 1, TokensIn: 1, Transfers: 2})
	assert.Equal(t, "swap", got.Kind)
	assert.Equal(t, 0.55, got.Confidence)
}

func TestHumanize(t *testing.T) {
	assert.Equal(t, "remove liquidity AVAX", Humanize("removeLiquidityAVAX"))
	assert.Equal(t, "swap exact tokens for tokens", Humanize("swapExactTokensForTokens"))
	assert.Equal(t, "exact input single", Humanize("exactInputSingle"))
	assert.Equal(t, "claim reward", Humanize("claim_reward"))
}
