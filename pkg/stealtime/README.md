# Steal-time backtest

An offline, block-pinned, read-only backtest that answers what a live snapshot
cannot: when a profitable liquidation appeared historically, how many blocks did
it sit available before an incumbent took it. That distribution, steal-time, tells
us both whether profitable liquidations exist on Avalanche and whether there is
room to win them against the searchers already operating here.

A calm live snapshot shows K=0 by construction, because anything profitable was
already taken. This backtest reconstructs the opportunities from history, so it is
the measurement that actually decides whether a Stage 2 bot is worth building.

Offline and read-only: no keys, no submission, no contract deployment. Every
historical read is pinned to its block.

## Method

For each liquidation that actually happened in `raw_logs`:

1. **Enumerate**: decode Aave `LiquidationCall` and Benqi `LiquidateBorrow` over
   the block window, keeping account, debt and collateral assets, the liquidator,
   and the block it landed (taken_block).
2. **Crossing**: step backward from taken_block by a coarse stride to bracket the
   boundary between a healthy and a liquidatable block, then binary-search the
   bracket for the exact first liquidatable block, checking the authoritative
   on-chain flag at each probe (Aave healthFactor < 1e18, Benqi shortfall > 0).
   This is block-precise, oracle-event agnostic (Avalanche feeds are OCR
   NewTransmission, not the legacy AnswerUpdated, so there are no AnswerUpdated logs
   to key on), and bounded to about lookback/stride coarse probes plus log2(stride)
   refinement probes. If the position was already liquidatable at the lookback
   floor, the observation is right-censored at the cap.
3. **Profitability**: at crossing_block, assemble the position's per-asset legs by
   reusing the live lending adapter's probe executed block-pinned, then run
   `pkg/prefilter.EvaluatePosition` with a block-pinned ParamsProvider (bonus and
   close factor as of the block), a block-pinned Quoter (Pangolin, TraderJoe, and
   LFJ Liquidity Book at the block, quoting the real seize size on the resolved
   underlying), and a CostModel whose gas is the block's base fee, AVAX price the
   oracle at the block, and flash fee Aave's premium at the block. Keep only
   opportunities profitable at crossing_block.
4. **Aggregate**: steal_time histogram (0, 1, 2, 3 to 5, 6 to 10, 11 to 20, 21+,
   censored), median and p90, the fraction taken within 0 to 2 blocks versus beyond
   10, split by protocol and size bucket, total realized profit, and incumbent
   concentration (top-N liquidator share).

Everything is pinned to the block: position state, oracle prices, params, DEX pool
liquidity, and gas. A backtest using today's prices, liquidity, or gas would be
meaningless. Multicall3 is used where it exists; for blocks before its deployment
the executor falls back to individual calls.

## How to read it

- **Profitable liquidations consistently taken within 0 to 2 blocks, dominated by
  a few liquidators**: incumbents are fast, reactive entry is a latency war you
  likely lose. Verdict leans feed, not bot.
- **A fat tail of profitable liquidations sitting unclaimed for many blocks**:
  incumbents are thin, that tail is the exploitable room, and its width is your
  latency budget. Verdict leans bot worth scoping.
- Split matters: incumbents may be fast on big Aave liquidations and slow on small
  Benqi ones, and that asymmetry is exactly where room would be. Read the
  by-protocol and by-size histograms, not just the headline.

## Running

Requires `ICICLE_ARCHIVE_RPC` and the standard ClickHouse env. Run over windows
that include known AVAX volatility spikes, since profitable flow concentrates
there; a calm window will show little and that is expected.

```bash
icicle stealtime --from-block 80000000 --to-block 80500000 --min-profit-usd 25
```

Flags: `--chain` (default 43114), `--max-lookback-blocks` (default 43200, about a
day), `--gas-units` (default 700000), `--top-n` (default 10), `--persist` (write
per-liquidation rows to `stealtime_results`, default on). Persisted rows let you
re-aggregate or compare windows without re-running the archive reads.

## Inherent limitations

- It measures liquidations that **actually happened**. It cannot see opportunities
  that appeared and vanished (a position crossed, then price recovered before
  anyone liquidated), so it is a lower bound on opportunity count.
- It reflects **past competition**, which predicts but does not guarantee future
  competition. A field that is thin today can thicken once a new opportunity is
  demonstrated.
- The LFJ Liquidity Book quoter address must be verified on-chain. A wrong
  `LBQuoter` address makes LB quotes fail and fall back to the V2 routers, which
  would understate profitability for LB-only pairs.
- The crossing scan assumes health crosses once and stays liquidatable through
  taken (the common case). A position that oscillated healthy and liquidatable
  within the bracket gets the binary-search boundary, which is block-precise but
  may not be the earliest of several liquidatable runs. The stride trades probe
  count against how wide a bracket the binary search must resolve, both bounded.
