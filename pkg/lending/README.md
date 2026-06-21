# Lending liquidation-risk engine

A real-time feed of lending-position health on Avalanche C-Chain (Aave v3 and
Benqi), built on top of the existing Icicle indexer. It discovers borrow
positions from event logs the indexer already stores in `raw_logs`, reads health
directly from the protocols through Multicall3 on a tiered schedule, and serves a
ranked liquidation-risk view over REST and WebSocket.

This phase builds the data feed only. Liquidation execution, flash loans,
collateral-swap routing, and transaction submission are out of scope.

## Architecture

- **Discovery** (`discovery.go`): reduces protocol event logs in `raw_logs` into
  the account universe (`lending_accounts`) and candidate asset exposures
  (`lending_exposure`). Idempotent and watermark-driven (`indexer_watermarks`,
  granularity `block`), reading only up to the raw `sync_watermark` so it never
  runs ahead of confirmed data. The backfill floor is the earliest `raw_logs`
  block for each resolved contract, derived at runtime.
- **Health engine** (`health.go`): tiered refresh (hot, warm, cold). Reads
  `getUserAccountData` (Aave) and `getAccountLiquidity` plus per-market snapshots
  (Benqi) through Multicall3 `aggregate3` with `allowFailure=true`. A price move
  recomputes every exposed position within a health band, across tiers
  (`RecomputeAsset`). Benqi liquidatable is authoritative from `shortfall > 0`;
  the derived health factor is for ranking and display only.
- **Price watch** (`pricewatch.go`): tails Chainlink `AnswerUpdated` logs for the
  aggregators behind Aave oracle sources, and polls oracle prices for composite
  sources and Benqi. A material move triggers a recompute.
- **Feed** (`../api/handlers_lending.go`, `../api/websocket_lending.go`): REST
  under `/api/v1/data/evm/{chainId}/lending/*` and a `lending_alert` WebSocket
  message on `/ws/lending/{chainId}`. Positions are read with `argMax` over
  `updated_at` so pre-merge duplicates never return stale health.

The adapter interface (`adapter.go`) is the extension point: adding a protocol is
one new package implementing `Adapter`. The core never branches on protocol.

## Address resolution

Addresses are resolved and verified on-chain at startup, never trusted blindly.

- **Aave v3**: from the `PoolAddressesProvider`
  (`0xa97684ead0e402dC232d5A977953DF7ECBaB3CDb`) it resolves `getPool`,
  `getPriceOracle`, and `getPoolDataProvider`, and logs a loud warning if the
  resolved Pool differs from the expected `0x794a61358D6845594F94dc1DB02A252b5b4814aD`.
- **Benqi**: from the Comptroller it resolves `getAllMarkets` and `oracle`, and
  verifies the first market reports the same Comptroller. Confirm the Comptroller
  address against Benqi's official documentation before production use, it is the
  trust anchor.

All resolved addresses and their verification status are persisted to
`lending_protocol_addresses`.

## Running

The engine is a standalone service that shares ClickHouse and the archive node.

```bash
# Required: ICICLE_ARCHIVE_RPC, plus ClickHouse env used by the rest of Icicle.
icicle lending \
  --chain 43114 \
  --archive-rpc "$ICICLE_ARCHIVE_RPC" \
  --discovery-batch 5000 \
  --params-refresh-hours 6 \
  --metrics-port 9092
```

Flags: `--fallback-rpc` (public RPC used only on archive failure, logged when
used), `--aave-provider` and `--benqi-comptroller` (override the canonical
anchors), `--metrics-port` (0 disables `/metrics`).

### Backfill

There is no separate backfill command. On first run, discovery starts from the
earliest `raw_logs` block for each protocol's contracts and walks forward in
`--discovery-batch` block steps until it reaches the sync watermark, then follows
the head. To force a full re-discovery, delete the discovery watermark rows:

```sql
ALTER TABLE indexer_watermarks DELETE
WHERE chain_id = 43114 AND indexer_name LIKE 'lending_discovery:%';
```

The health engine runs a cold sweep at startup to populate tiers, so the feed
fills in as discovery and the first sweep complete.

## API

- `GET /api/v1/data/evm/{chainId}/lending/positions` ranked by liquidation
  proximity. Filters: `protocol`, `asset`. Paginated with `limit` and `offset`.
- `GET /api/v1/data/evm/{chainId}/lending/positions/{account}`
- `GET /api/v1/data/evm/{chainId}/lending/stats` open and liquidatable counts.
- `GET /api/v1/data/evm/{chainId}/lending/alerts` recent crossing events.
- `GET /ws/lending/{chainId}` pushes `lending_alert` messages on crossings.

Each position carries the unified health factor, total and per-asset collateral
and debt in the oracle base currency, and a gross liquidation estimate
(close factor, liquidation bonus, seizable collateral, gross bonus). Slippage is
not modeled in this phase and is never silently subtracted: the estimate reports
`slippage_modeled: false`.

## Metrics

Exposed on `/metrics` (default port 9092):

- `icicle_lending_positions_tracked{protocol,tier}`
- `icicle_lending_liquidatable{protocol}`
- `icicle_lending_refresh_seconds{trigger}`
- `icicle_lending_recompute_total{trigger}`
- `icicle_lending_multicall_requests_total`

## Tests

```bash
go test ./pkg/lending/...
```

Unit tests cover the health and ranking math, the close-factor rule, the Benqi
shortfall-authoritative flag, the Multicall3 ABI round-trip, and adapter log
decoding. The integration test asserts pre-liquidation detection against a live
archive node and is gated on `LENDING_IT_ARCHIVE_RPC`, `LENDING_IT_ACCOUNT`, and
`LENDING_IT_LIQ_BLOCK`.
