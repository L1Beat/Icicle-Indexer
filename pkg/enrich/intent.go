package enrich

import (
	"strings"
	"unicode"
)

// Intent is a coarse, server-side reading of what a transaction did. The rules
// live in intentRules below and are documented in pkg/enrich/INTENT_RULES.md; keep
// the two in sync.
type Intent struct {
	Kind       string  `json:"kind" example:"swap"`
	Label      string  `json:"label" example:"Swapped"`
	Protocol   *string `json:"protocol" example:"LFJ"`
	Confidence float64 `json:"confidence" example:"0.9"`
}

// IntentInput is everything the rules may look at.
type IntentInput struct {
	Method      *Method // nil when no calldata or unknown selector
	HasCalldata bool
	ValueOut    bool // tx.value > 0 (native sent by the sender)
	NativeIn    bool // an internal tx moved native value back to the sender
	Sender      string
	To          string // "" for contract creation
	ToLabel     *Label

	TokensOut   int // ERC-20 transfers whose from == sender
	TokensIn    int // ERC-20 transfers whose to == sender
	Transfers   int // total ERC-20 transfers in the tx
	Approvals   int
	HasTransfer bool // transfer details were available (single-tx or include=transfers)
}

// Direction summary used by several rules.
func (in IntentInput) outbound() bool { return in.TokensOut > 0 || in.ValueOut }
func (in IntentInput) inbound() bool  { return in.TokensIn > 0 || in.NativeIn }

type rule struct {
	kind  string
	label string
	match func(in IntentInput, name string) (conf float64, ok bool)
}

func anyPrefix(name string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func anyExact(name string, names ...string) bool {
	for _, n := range names {
		if name == n {
			return true
		}
	}
	return false
}

func toKind(in IntentInput) string {
	if in.ToLabel == nil {
		return ""
	}
	return in.ToLabel.Kind
}

// intentRules are evaluated in order; the first match wins.
var intentRules = []rule{
	// 1. Plain native transfer: no calldata, value moved.
	{"send", "Sent AVAX", func(in IntentInput, name string) (float64, bool) {
		if !in.HasCalldata && in.ValueOut {
			return 0.95, true
		}
		return 0, false
	}},
	// 2. Approvals and permits.
	{"approve", "Approved token", func(in IntentInput, name string) (float64, bool) {
		if anyExact(name, "approve", "permit", "setApprovalForAll", "increaseAllowance", "decreaseAllowance") {
			return 0.95, true
		}
		return 0, false
	}},
	// 3. ERC-20 / NFT transfer calls aimed at a token contract.
	{"send", "Sent tokens", func(in IntentInput, name string) (float64, bool) {
		if anyExact(name, "transfer", "transferFrom", "safeTransferFrom", "safeBatchTransferFrom") {
			if toKind(in) == "token" {
				return 0.9, true
			}
			return 0.7, true
		}
		return 0, false
	}},
	// 4. Bridging: bridge-kind destination, or a known bridge method.
	{"bridge", "Bridged", func(in IntentInput, name string) (float64, bool) {
		if toKind(in) == "bridge" {
			return 0.85, true
		}
		if anyPrefix(name, "bridge", "depositForBurn", "sendFrom", "transferTokens", "wrapAndTransfer", "sendCrossChainMessage") {
			return 0.75, true
		}
		return 0, false
	}},
	// 5. Wrapped native: deposit()/withdraw() on a token contract.
	{"deposit", "Wrapped AVAX", func(in IntentInput, name string) (float64, bool) {
		if name == "deposit" && toKind(in) == "token" && in.ValueOut {
			return 0.9, true
		}
		return 0, false
	}},
	{"withdraw", "Unwrapped AVAX", func(in IntentInput, name string) (float64, bool) {
		if name == "withdraw" && toKind(in) == "token" && in.ToLabel != nil && strings.Contains(strings.ToLower(in.ToLabel.Name), "wrapped") {
			return 0.9, true
		}
		return 0, false
	}},
	// 6. Liquidity management.
	{"remove_liquidity", "Removed liquidity", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "removeLiquidity", "decreaseLiquidity") || (name == "burn" && in.Method != nil && strings.Count(in.Method.Signature, ",") >= 2) {
			return 0.9, true
		}
		if name == "collect" && anyExact(toKind(in), "protocol", "pool", "router") {
			return 0.7, true
		}
		return 0, false
	}},
	{"add_liquidity", "Added liquidity", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "addLiquidity", "increaseLiquidity") {
			return 0.9, true
		}
		if name == "mint" && anyExact(toKind(in), "protocol", "pool") && in.Method != nil && strings.Contains(in.Method.Signature, "(") && strings.Contains(in.Method.Signature, "int24") {
			return 0.85, true
		}
		return 0, false
	}},
	// 7. Swaps: swap-like method, or a router that moved tokens both ways.
	{"swap", "Swapped", func(in IntentInput, name string) (float64, bool) {
		swapName := anyPrefix(name, "swap", "exactInput", "exactOutput", "unoswap", "uniswapV3Swap", "sellToUniswap", "fillQuote")
		routerCall := toKind(in) == "router" && anyExact(name, "execute", "multicall", "aggregate", "aggregate3", "swapCompact")
		if swapName || routerCall {
			if in.HasTransfer && in.outbound() && in.inbound() {
				return 0.9, true
			}
			if swapName {
				return 0.75, true
			}
			return 0.6, true
		}
		if in.HasTransfer && toKind(in) == "router" && in.outbound() && in.inbound() {
			return 0.7, true
		}
		return 0, false
	}},
	// 8. Staking and rewards.
	{"claim", "Claimed rewards", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "claim", "getReward", "harvest", "compound") {
			return 0.85, true
		}
		return 0, false
	}},
	{"stake", "Staked", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "stake", "delegate", "submit", "lock") {
			return 0.85, true
		}
		return 0, false
	}},
	{"unstake", "Unstaked", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "unstake", "undelegate", "requestUnlock", "unlock", "exit") {
			return 0.85, true
		}
		return 0, false
	}},
	// 9. Lending / vaults: deposits and withdrawals.
	{"deposit", "Deposited", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "deposit", "supply") || (name == "mint" && anyExact(toKind(in), "token", "protocol")) {
			return 0.85, true
		}
		return 0, false
	}},
	{"withdraw", "Withdrew", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "withdraw", "redeem") {
			return 0.85, true
		}
		return 0, false
	}},
	// 10. Lending borrow/repay have no kind of their own: report as
	// contract_call with a descriptive label so the UI can still show them.
	{"contract_call", "Borrowed", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "borrow") {
			return 0.8, true
		}
		return 0, false
	}},
	{"contract_call", "Repaid", func(in IntentInput, name string) (float64, bool) {
		if anyPrefix(name, "repay") {
			return 0.8, true
		}
		return 0, false
	}},
	// 11. Unknown method but tokens moved both ways through a contract.
	{"swap", "Swapped", func(in IntentInput, name string) (float64, bool) {
		if name == "" && in.HasTransfer && in.outbound() && in.inbound() && in.Transfers >= 2 {
			return 0.55, true
		}
		return 0, false
	}},
}

// DeriveIntent applies intentRules and falls back to contract_call. It returns
// nil only for a transaction with neither calldata nor value (nothing to say).
func DeriveIntent(in IntentInput) *Intent {
	if !in.HasCalldata && !in.ValueOut {
		if in.To == "" {
			return &Intent{Kind: "contract_call", Label: "Deployed contract", Protocol: protocolOf(in), Confidence: 0.9}
		}
		return nil
	}
	name := ""
	if in.Method != nil {
		name = in.Method.Name
	}
	for _, r := range intentRules {
		if conf, ok := r.match(in, name); ok {
			return &Intent{Kind: r.kind, Label: r.label, Protocol: protocolOf(in), Confidence: conf}
		}
	}
	if in.To == "" {
		return &Intent{Kind: "contract_call", Label: "Deployed contract", Protocol: protocolOf(in), Confidence: 0.9}
	}
	label := "Contract call"
	conf := 0.3
	if name != "" {
		label = "Called " + Humanize(name)
		conf = 0.5
	}
	return &Intent{Kind: "contract_call", Label: label, Protocol: protocolOf(in), Confidence: conf}
}

func protocolOf(in IntentInput) *string {
	if in.ToLabel == nil || in.ToLabel.Protocol == "" {
		return nil
	}
	p := in.ToLabel.Protocol
	return &p
}

// Humanize turns a camelCase method name into words, keeping acronyms:
// "removeLiquidityAVAX" becomes "remove liquidity AVAX".
func Humanize(name string) string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		if r == '_' {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	for i, w := range words {
		if len(w) > 1 && strings.ToUpper(w) == w {
			continue // acronym such as AVAX, ETH, NFT
		}
		words[i] = strings.ToLower(w)
	}
	return strings.Join(words, " ")
}
