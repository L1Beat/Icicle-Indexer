# K-measurement runner

A read-only diagnostic that measures K: how many currently liquidatable positions
on the lending feed are actually profitable to liquidate after the liquidation
bonus, real swap slippage, the flash-loan fee, and gas. It tells us whether a
liquidation bot is worth building or whether the feed itself is the product.

This tool holds no keys, signs nothing, submits no transactions, and deploys no
contracts. Quoting is read-only. It drives `pkg/prefilter` (the Stage 1
profitability gate) against live data and reports the result.

## How it works

```
feed /positions (liquidatable=true)  ->  resolve underlyings  ->  prefilter.ComputeK  ->  K + rejection breakdown
                                                                         |
                                                            live DEX quote per pair (executable, sized to the seize amount)
```

- **Position loader** pages `GET /positions`, which is ordered liquidatable first,
  and collects the full liquidatable set. Money fields are 1e18-scaled USD
  strings, amounts are native units, decoded with big.Int.
- **Token resolution** maps each Benqi leg's qiToken to its underlying via
  `underlying()` (native qiAVAX maps to WAVAX), and reads `decimals()`, cached.
  Aave legs are already underlyings. DEX quotes use the underlying, since seized
  Benqi collateral is redeemed to the underlying before swapping.
- **ParamsProvider** reads `lending_protocol_params` and `lending_protocol_globals`
  from ClickHouse. The tables store the liquidation bonus as a multiplier in bps
  (Aave per-reserve, Benqi global incentive); the pre-filter wants the premium, so
  the provider subtracts 10000 (10500 becomes 500, 11000 becomes 1000). Close
  factor is the Benqi global value, or for Aave 10000 when the health factor is
  below 0.95 and 5000 otherwise.
- **Quoter** quotes the executable output for the actual seize amount against
  UniswapV2-style routers (Pangolin, TraderJoe) at live chain head, trying a direct
  path and a WAVAX-bridged path and keeping the best. It returns the route
  alongside the amount, so Stage 2 can replicate it, adapted to `prefilter.Quoter`
  for the run. A pair with no route surfaces as a zero output (illiquid or
  no_pair), never a crash.
- **Cost model** is built from live values: gas price from the node, AVAX price
  from Chainlink, and the flash-loan premium from Aave's `FLASHLOAN_PREMIUM_TOTAL`.
  Each falls back to a configured value if its read fails. `MinProfitUSD` is above
  zero on purpose, to cover the variance of losing races and worse-than-quoted fills.

## Running

Requires `ICICLE_ARCHIVE_RPC` and the standard ClickHouse env.

```bash
# one-shot
icicle kmeasure --min-profit-usd 25

# repeat every 5 minutes, to capture quiet and volatile stretches
icicle kmeasure --interval-min 5 --min-profit-usd 25
```

Flags: `--chain` (default 43114), `--feed-url`, `--archive-rpc`, `--fallback-rpc`,
`--gas-units` (default 700000), `--flash-fee-bps` (fallback, default 5),
`--min-debt-base` (optional feed-side dust pre-cut in 1e18 USD, off by default so
the pre-filter classifies dust itself), `--persist` (write each run to
`kmeasure_runs`, default on).

Output is a summary: total evaluated, K, the counts by reason (profitable, dust,
illiquid, bad_debt, no_pair, unprofitable), and the profitable shortlist with
account, protocol, chosen debt and collateral assets, and net USD. The large
`no_pair` count is the zero-collateral bad debt and is shown explicitly, not
hidden. Each run is persisted with a timestamp, and Prometheus metrics cover
evaluated, K, quoter latency, and quoter failure rate.

## Two caveats

1. K is only meaningful once the lending backfill is complete. An incomplete
   standing set undercounts the denominator. The runner logs the discovery
   watermark against chain head and warns loudly when it is behind.
2. The Quoter must quote the real seize size against live state, on the resolved
   underlying token, and return a route a Stage 2 contract could replicate. A
   quote from a venue or path the contract cannot reproduce is one we cannot act
   on. The default routers are UniswapV2-style precisely because their
   `getAmountsOut` is exactly replicable on-chain; adding a deeper-liquidity venue
   means adding one that is equally replicable.
