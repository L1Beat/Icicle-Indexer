# Transaction intent rules

`intent` on every transaction is a coarse, server-side reading derived from
three inputs: the resolved method name (`method.name`), the label of the `to`
address (`to_label.kind` / `to_label.protocol`) and, when transfers are loaded
(single-tx endpoint or `include=transfers`), the direction of token and native
value moves relative to the sender. Rules live in `pkg/enrich/intent.go`
(`intentRules`); the first matching rule wins. Confidence values are
hand-assigned per rule, not computed.

Shape: `{"kind", "label", "protocol" (to_label.protocol or null), "confidence"}`.
`intent` is `null` only for a transaction with neither calldata nor value.

| # | kind | label | condition | confidence |
|---|------|-------|-----------|------------|
| 1 | send | Sent AVAX | no calldata, value > 0 | 0.95 |
| 2 | approve | Approved token | method in approve, permit, setApprovalForAll, increaseAllowance, decreaseAllowance | 0.95 |
| 3 | send | Sent tokens | method in transfer, transferFrom, safeTransferFrom, safeBatchTransferFrom | 0.9 if to is a token, else 0.7 |
| 4 | bridge | Bridged | to_label.kind = bridge, or method starts with bridge, depositForBurn, sendFrom, transferTokens, wrapAndTransfer, sendCrossChainMessage | 0.85 / 0.75 |
| 5 | deposit | Wrapped AVAX | `deposit` on a token with value > 0 | 0.9 |
| 5 | withdraw | Unwrapped AVAX | `withdraw` on a token whose name contains "wrapped" | 0.9 |
| 6 | remove_liquidity | Removed liquidity | method starts with removeLiquidity or decreaseLiquidity; `burn` with 3+ params; `collect` on protocol/pool/router | 0.9 / 0.7 |
| 6 | add_liquidity | Added liquidity | method starts with addLiquidity or increaseLiquidity; `mint` with a tuple containing int24 on protocol/pool | 0.9 / 0.85 |
| 7 | swap | Swapped | method starts with swap, exactInput, exactOutput, unoswap, uniswapV3Swap, sellToUniswap, fillQuote; or execute/multicall/aggregate/aggregate3/swapCompact on a router | 0.9 when tokens moved both ways, else 0.75 (swap name) / 0.6 (router call) |
| 7 | swap | Swapped | any method on a router with tokens both out and in | 0.7 |
| 8 | claim | Claimed rewards | method starts with claim, getReward, harvest, compound | 0.85 |
| 8 | stake | Staked | method starts with stake, delegate, submit, lock | 0.85 |
| 8 | unstake | Unstaked | method starts with unstake, undelegate, requestUnlock, unlock, exit | 0.85 |
| 9 | deposit | Deposited | method starts with deposit or supply; `mint` on token/protocol | 0.85 |
| 9 | withdraw | Withdrew | method starts with withdraw or redeem | 0.85 |
| 10 | contract_call | Borrowed / Repaid | method starts with borrow / repay (no dedicated kind in the agreed vocabulary) | 0.8 |
| 11 | swap | Swapped | unknown method, 2+ transfers with tokens both out and in | 0.55 |
| – | contract_call | Deployed contract | `to` is null | 0.9 |
| – | contract_call | Called <humanized method> | known method, nothing above matched | 0.5 |
| – | contract_call | Contract call | unknown method | 0.3 |

Direction: "out" = a Transfer whose `from` is the sender or tx.value > 0;
"in" = a Transfer whose `to` is the sender or an internal tx paying the sender.

`receive`: a transaction has no viewpoint, so the API only emits `receive` on
`/address/{address}/txs`, where a `send` whose `to` is the page's address (and
whose `from` is not) is flipped to `receive` / "Received ...".
