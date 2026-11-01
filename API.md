# Icicle API Documentation

Production URL: `https://api.l1beat.io`
Local URL: `http://localhost:8080`

**Interactive Documentation**: Open `/api/docs/` in browser for Swagger UI

## API Structure

- **Data API** (`/api/v1/data/*`): Blocks, transactions, chains, validators, P-chain data
- **Metrics API** (`/api/v1/metrics/*`): Fee statistics, daily fee burn, chain metrics, time series data
- **WebSocket** (`/ws/*`): Real-time block streaming
- **System** (`/health`): Health check (no versioning)

## Common Response Format

**Success (list):**
```json
{
  "data": [ ... ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true,
    "next_cursor": "54000000",
    "total": 1234567
  }
}
```

**Success (single resource):**
```json
{
  "data": { ... }
}
```

- `has_more` — always present on list endpoints, indicates whether additional results exist beyond this page
- `next_cursor` — present when `has_more` is true on cursor-eligible endpoints; pass as `?cursor=` for the next page
- `total` — only present when `?count=true` is passed; the total number of matching records

**Error:**
```json
{
  "error": {
    "code": "INVALID_PARAMETER",
    "message": "Description of the error",
    "details": "Optional extra details",
    "retry_after": 5
  }
}
```

**Error Codes:**
- `INVALID_PARAMETER` — Invalid request parameter (HTTP 400)
- `VALIDATION_FAILED` — Request validation failed (HTTP 400)
- `NOT_FOUND` — Resource not found (HTTP 404)
- `RATE_LIMITED` — Too many requests (HTTP 429, includes `retry_after` seconds)
- `INTERNAL_ERROR` — Server error (HTTP 500)
- `DATABASE_ERROR` — Database error (HTTP 500)

## CORS

All origins are allowed (`Access-Control-Allow-Origin: *`). No authentication is required.

## Rate Limiting

All endpoints are rate limited to 180 requests/minute per IP (burst of 30) by default.

Every response carries the current quota (per-IP token bucket):
- `X-RateLimit-Limit` — burst capacity (30 by default)
- `X-RateLimit-Remaining` — tokens left right now
- `X-RateLimit-Reset` — epoch seconds at which the bucket refills to full

When rate limited, you'll receive:
- HTTP 429 status
- `Retry-After` header with seconds to wait
- Error body with `retry_after` field

These headers (and `Retry-After`) are listed in `Access-Control-Expose-Headers`, so cross-origin browser clients can read them.

## Prometheus Metrics

`/metrics` is a Prometheus endpoint for operators. It is disabled unless the API is started with `--metrics-token` or `ICICLE_METRICS_TOKEN`. When enabled, scrape it with:

```bash
curl -H "Authorization: Bearer $ICICLE_METRICS_TOKEN" https://api.l1beat.io/metrics
```

## Common Query Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | int | 20 | Number of results (max 100) |
| `offset` | int | 0 | Pagination offset |
| `cursor` | string | - | Cursor for keyset pagination (use `next_cursor` from previous response) |
| `count` | string | - | Set to `true` to include total count in response |

### Pagination

All list endpoints support **offset-based** pagination (`?limit=20&offset=40`).

Most endpoints also support **cursor-based** pagination, which is more efficient for deep pagination. When a response has `"has_more": true`, the `next_cursor` field contains the value to pass as `?cursor=` for the next page. When using cursor, offset is ignored.

**Cursor-eligible endpoints:** blocks, transactions, address transactions, address internal transactions, P-Chain transactions, chains, validator deposits.

Treat `next_cursor` as opaque and pass it back unchanged. P-Chain transactions, validator deposits and chains return `<block>:<id>`, so a page that ends partway through a block resumes after its last row. A bare block number is still accepted on those endpoints, but it resumes at the next block and skips the rest of the current one. Address internal transactions return `<block>:<tx_index>:<trace position>` for the same reason.

**Offset-only endpoints** (sorted by non-monotonic fields): validators, fee metrics, token balances.

---

## Health

### GET /health

Check API and database connectivity.

**Response:**
```json
{
  "status": "healthy",
  "database": "connected"
}
```

---

## Indexer Status

### GET /api/v1/metrics/indexer/status

Get indexer sync status for all chains. Useful for monitoring and alerting.

**Response:**
```json
{
  "healthy": true,
  "evm": [
    {
      "chain_id": 43114,
      "name": "C-Chain",
      "current_block": 75510573,
      "latest_block": 75538000,
      "blocks_behind": 27427,
      "last_sync": "2025-01-11T08:49:15Z",
      "is_synced": false
    }
  ],
  "pchain": {
    "current_block": 24160141,
    "latest_block": 24160200,
    "blocks_behind": 59,
    "last_sync": "2025-01-11T16:10:16Z",
    "is_synced": true
  },
  "last_update": "2025-01-11T16:15:00Z"
}
```

**Fields:**
- `healthy` — `false` if any chain is >100 blocks behind
- `is_synced` — `true` if chain is <10 blocks behind
- `blocks_behind` — Number of blocks behind the chain tip

---

## Data API - EVM

All EVM data endpoints are prefixed with `/api/v1/data/evm/{chainId}/...`

Common chain IDs:
- `43114` — Avalanche C-Chain

### GET /api/v1/data/evm/{chainId}/blocks

List recent blocks for a chain.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID (e.g., 43114) |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "chain_id": 43114,
      "block_number": 54000000,
      "hash": "0x1234...abcd",
      "parent_hash": "0xabcd...1234",
      "block_time": "2025-01-08T12:00:00Z",
      "miner": "0x0100000000000000000000000000000000000000",
      "size": 1234,
      "gas_limit": 15000000,
      "gas_used": 8000000,
      "base_fee_per_gas": 25000000000
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true,
    "next_cursor": "53999980"
  }
}
```

---

### GET /api/v1/data/evm/{chainId}/blocks/{number}

Get a specific block by number.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `number` | int | Block number |

**Response:**
```json
{
  "data": {
    "chain_id": 43114,
    "block_number": 54000000,
    "hash": "0x1234...abcd",
    "parent_hash": "0xabcd...1234",
    "block_time": "2025-01-08T12:00:00Z",
    "miner": "0x0100000000000000000000000000000000000000",
    "size": 1234,
    "gas_limit": 15000000,
    "gas_used": 8000000,
    "base_fee_per_gas": 25000000000,
    "tx_count": 150
  }
}
```

---

### GET /api/v1/data/evm/{chainId}/txs

List recent transactions for a chain.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "chain_id": 43114,
      "hash": "0x1234...abcd",
      "block_number": 54000000,
      "block_time": "2025-01-08T12:00:00Z",
      "transaction_index": 0,
      "from": "0xabcd...1234",
      "to": "0x5678...efgh",
      "value": "1000000000000000000",
      "gas_limit": 21000,
      "gas_price": 25000000000,
      "gas_used": 21000,
      "success": true,
      "type": 2
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true,
    "next_cursor": "54000000:0"
  }
}
```

**Notes:**
- `to` is `null` for contract creation transactions
- `value` is in wei (string to preserve precision)
- `type`: 0=legacy, 1=EIP-2930, 2=EIP-1559, 3=EIP-4844
- Cursor format for transactions: `block_number:transaction_index`

---

### GET /api/v1/data/evm/{chainId}/txs/{hash}

Get a specific transaction by hash, including internal transactions, token transfers, and approvals.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `hash` | string | Transaction hash (with or without 0x prefix) |

**Response:**
```json
{
  "data": {
    "chain_id": 43114,
    "hash": "0x1234...abcd",
    "block_number": 54000000,
    "block_time": "2025-01-08T12:00:00Z",
    "transaction_index": 0,
    "from": "0xabcd...1234",
    "to": "0x5678...efgh",
    "value": "1000000000000000000",
    "gas_limit": 21000,
    "gas_price": 25000000000,
    "gas_used": 21000,
    "success": true,
    "type": 2,
    "internal_txs": [
      {
        "trace_index": "0,1",
        "from": "0x5678...efgh",
        "to": "0x9abc...def0",
        "value": "500000000000000000",
        "gas_used": 10000,
        "call_type": "CALL",
        "success": true
      }
    ],
    "token_transfers": [
      {
        "token": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e",
        "name": "USD Coin",
        "symbol": "USDC",
        "decimals": 6,
        "from": "0xabcd...1234",
        "to": "0x5678...efgh",
        "value": "1000000",
        "log_index": 5
      }
    ],
    "approvals": [
      {
        "token": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e",
        "name": "USD Coin",
        "symbol": "USDC",
        "decimals": 6,
        "owner": "0xabcd...1234",
        "spender": "0x5678...efgh",
        "amount": "1000000",
        "is_unlimited": false,
        "log_index": 3
      }
    ]
  }
}
```

**Notes:**
- `internal_txs` — internal call traces within the transaction (empty array if none)
- `token_transfers` — ERC-20 Transfer events (empty array if none)
- `approvals` — ERC-20 Approval events (empty array if none)
- `name`, `symbol`, `decimals` on token transfers/approvals are optional (only if token metadata known)

---

### GET /api/v1/data/evm/{chainId}/address/{address}/txs

Get transactions for a specific address (as sender or receiver).

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `address` | string | Ethereum address (with or without 0x prefix) |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:** Same format as `GET /txs` (array of Transaction objects).

---

### GET /api/v1/data/evm/{chainId}/labels

Resolve up to 100 addresses to labels. Sources are merged per address in the
order curated > verified-source (Routescan) > token-metadata; unknown
addresses are omitted from the map. Served from memory (refreshed every 5
minutes from `address_labels` / `token_metadata`); nothing on the request path
calls a third party.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `addresses` | string | Comma-separated 0x addresses, any case, max 100 |

**Response:**
```json
{
  "data": {
    "0x60ae616a2155ee3d9a68541ba4544862310933d4": {
      "name": "LFJ Router (v1)",
      "kind": "router",
      "protocol": "LFJ",
      "is_contract": true,
      "verified": true,
      "implementation": null
    }
  }
}
```

`kind` is one of `protocol`, `token`, `pool`, `router`, `bridge`, `exchange`,
`wallet`, `contract`. `protocol` and `implementation` are `null` when unknown.
Keys are lowercase.

---

### Transaction enrichment fields (all tx endpoints)

Every transaction object (list, address and single-tx endpoints) carries four
additive fields. Existing fields are unchanged.

```json
{
  "method": {
    "selector": "0xa214fa6b",
    "name": "burn",
    "signature": "burn(uint256,uint128,address,uint256,uint256,bool)"
  },
  "from_label": null,
  "to_label": { "name": "Pharaoh CL Positions NFT", "kind": "protocol", "protocol": "Pharaoh", "is_contract": true, "verified": true, "implementation": "0x9f10..." },
  "intent": { "kind": "remove_liquidity", "label": "Removed liquidity", "protocol": "Pharaoh", "confidence": 0.9 }
}
```

- `method` is `null` when the transaction has no calldata or the selector is
  unknown. Selectors are resolved from `function_signatures` (bundled seed +
  OpenChain lookups done by the indexer, cached, negative results retried
  weekly). Colliding signatures are disambiguated by calldata length, then
  source priority.
- `from_label` / `to_label` have the labels-endpoint shape or are `null`.
- `intent` kinds: `remove_liquidity`, `add_liquidity`, `swap`, `send`,
  `receive`, `deposit`, `withdraw`, `stake`, `unstake`, `claim`, `approve`,
  `bridge`, `contract_call`. Rules are documented in `pkg/enrich/INTENT_RULES.md`.
  `receive` is only emitted on `/address/{address}/txs`.
- Each `token_transfers[]` and `approvals[]` entry carries `token_label`
  (same shape as `to_label`, or `null`).

**`include=transfers`** on `GET /txs` and `GET /address/{address}/txs` embeds
`internal_txs`, `token_transfers` and `approvals` on every row, exactly as the
single-transaction endpoint does, loaded with three batched queries per page.
With the flag the page size is capped at 50 and per transaction at most 100
internal txs, 200 token transfers and 50 approvals are returned. Without the
flag the rows are unchanged apart from the four fields above.

Backfill / ops: `./icicle enrich-backfill --chain 43114 --days 30` seeds the
bundled lists, resolves selectors seen in the window and labels the most
active contracts (Routescan, throttled). Idempotent, runs alongside the
indexer. The indexer keeps both tables current incrementally;
`ICICLE_ENRICH_DISABLE=1` turns that off and `ICICLE_ROUTESCAN_API_KEY` is
optional.

---

### GET /api/v1/data/evm/{chainId}/address/{address}/internal-txs

Get internal transactions (traces) for a specific address.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `address` | string | Ethereum address (with or without 0x prefix) |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "tx_hash": "0x1234...abcd",
      "block_number": 54000000,
      "block_time": "2025-01-08T12:00:00Z",
      "trace_index": "0,1",
      "from": "0xabcd...1234",
      "to": "0x5678...efgh",
      "value": "1000000000000000000",
      "gas_used": 21000,
      "call_type": "CALL",
      "success": true
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  }
}
```

**Notes:**
- Only includes traces with value > 0 or CREATE/CREATE2 types
- `trace_index` shows the path in the call tree (e.g., "0,1" = first call's second subcall)
- `to` is `null` for CREATE/CREATE2
- `next_cursor` is `<block>:<tx_index>:<trace position>` (e.g. `54000000:12:0.1`, with an empty position for a tx's top-level call), so a page that ends inside a transaction resumes at its next trace. The older `<block>:<tx_index>` form is still accepted but resumes before that whole transaction.

---

### GET /api/v1/data/evm/{chainId}/address/{address}/balances

Get ERC-20 token balances for an address.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `address` | string | Wallet address (with or without 0x prefix) |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "token": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e",
      "name": "USD Coin",
      "symbol": "USDC",
      "decimals": 6,
      "balance": "1000000",
      "total_in": "2000000",
      "total_out": "1000000",
      "last_updated_block": 77048918
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  }
}
```

**Notes:**
- Only returns tokens with balance > 0
- `name`, `symbol`, `decimals` are optional (only included if token metadata is available)
- Balance values are in token's smallest unit (e.g., 6 decimals for USDC)
- Offset-only pagination (no cursor support)

---

### GET /api/v1/data/evm/{chainId}/address/{address}/native

Get native token balance (AVAX, ETH, etc.) for an address.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `address` | string | Wallet address (with or without 0x prefix) |

**Response:**
```json
{
  "data": {
    "total_in": "1000000000000000000",
    "total_out": "500000000000000000",
    "total_gas": "21000000000000",
    "balance": "499979000000000000",
    "last_updated_block": 77048918,
    "tx_count": 788432,
    "first_tx_time": "2020-09-23T11:02:19Z",
    "last_tx_time": "2026-02-02T10:15:33Z"
  }
}
```

**Notes:**
- All values in wei (string to preserve precision)
- `first_tx_time` and `last_tx_time` only included when `tx_count` > 0
- Returns zeros if address has no history

---

### GET /api/v1/data/evm/{chainId}/stablecoins

Curated stablecoins on a chain with **circulating** supply (excludes issuer-treasury addresses), holder count, and 24h transfer stats.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID (e.g. 43114) |

**Response:**
```json
{
  "data": [
    {
      "token": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e",
      "symbol": "USDC",
      "name": "USD Coin",
      "decimals": 6,
      "peg": "USD",
      "issuer": "Circle",
      "bridged": false,
      "doublecounted": false,
      "supply": "459300000000000",
      "holders": 128453,
      "volume_24h": "32100000000000",
      "transfers_24h": 58210
    }
  ]
}
```

**Notes:**
- Amount fields (`supply`, `volume_24h`) are strings in the token's smallest unit; scale by `decimals`.
- `supply` is circulating (issuer treasuries in `stablecoin_excluded_holders` are subtracted), DeFiLlama-style.
- `doublecounted` marks stablecoins whose reserves are themselves other counted stablecoins (e.g. `avUSD`, `UTY`, `reUSD`, `FRXUSD`). Summing every row gives the **gross** total; excluding `doublecounted` rows gives the **net** total (the DeFiLlama-style headline, which avoids counting the same dollars twice). Show all rows in a table, but compute the headline from the net set.

---

### GET /api/v1/data/evm/{chainId}/stablecoins/timeseries

Time series for a stablecoin metric.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `metric` | string | — | **Required.** One of `supply`, `volume`, `transfers`, `holders` |
| `granularity` | string | `day` | `hour`, `day`, `week`, or `month` |
| `token` | string | — | Token address (0x-hex). Omit for all stablecoins on the chain |
| `from` | string | — | Start time (date, RFC3339, or unix) |
| `to` | string | — | End time |
| `limit` | int | 365 | Data points per token (max 5000) |

**Response:**
```json
{
  "data": [
    {
      "chain_id": 43114,
      "token": "0xb97ef9ef...",
      "metric_name": "supply",
      "granularity": "day",
      "data": [
        { "period": "2025-06-01T00:00:00Z", "value": "459300000000000" }
      ]
    }
  ]
}
```

---

## Data API - Lending

Liquidation-risk feed for Aave v3 and Benqi on C-Chain (`chainId` 43114), written by the `icicle-lending` service.

### Units

| Field | Scale |
|-------|-------|
| `health_factor`, `hf_max` | Dimensionless ratio × 1e18. `1000000000000000000` = 1.0; below 1.0 is liquidatable |
| `*_base` (`collateral_base`, `debt_base`, `base_value`, `min_debt_base`, ...) | USD × 1e18 for **both** protocols. Aave's 1e8 base currency is normalized to 1e18 |
| `repaid_usd`, `total_repaid_usd` | USD × 1e18, priced by the protocol oracle at the liquidation block. Same scale as `*_base` |
| `amount`, `repay_amount`, `seize_amount` | Raw token units. Scale by the leg's `decimals` |

All of these are decimal strings.

### Collateral legs and `entered`

Each collateral leg has `entered`. It is always `true` for Aave. For Benqi, `false` means the account supplied the asset but never entered that market as collateral (`enterMarkets`). That supply backs no borrow, so it is excluded from `collateral_base`, `health_factor`, and the per-asset `/risk` totals. It is still seizable in a liquidation, because Benqi's `seizeAllowed` only checks that both markets are listed. The `liquidation` estimate therefore considers it when picking the largest collateral leg.

Benqi `health_factor` is computed from the Comptroller's own `getAccountLiquidity`: `(debt_base + liquidity_base - shortfall_base) / debt_base`.

### `emode_category`

Each position has `emode_category`, the account's Aave V3 E-Mode category from the Pool's `getUserEMode`, read with the health factor on every refresh. `0` means not in E-Mode, and it is always `0` for Benqi. In E-Mode, an asset in the category's collateral bitmap uses the category's liquidation threshold instead of the reserve's. The categories, with their thresholds and assets, are under `emode_categories` in [`/metrics/lending`](#get-apiv1metricslending). Rows written before this field existed read `0` until their next refresh (at most 20 minutes, see Freshness).

### Freshness

`health_factor`, `liquidatable`, `tier`, the `*_base` totals, and the per-asset legs are a snapshot read from chain at the row's `block_number` (written at `updated_at`). They are **not** live. How often a position is re-read depends on its tier:

| Tier | Health factor | Re-read every | Re-read on oracle price move ≥ 0.5% |
|------|---------------|---------------|-------------------------------------|
| hot | < 1.1 | 4 s | yes |
| warm | 1.1 to 1.5 | 60 s | yes |
| cold | ≥ 1.5 | 20 min | no |

A cold position can therefore drift from the chain for up to 20 minutes. Before acting on a position, re-read it on chain.

### Filters

Address filters (`account`, `asset`) take a 40-hex-character address, with or without `0x`, case-insensitive. A malformed value returns `400 INVALID_PARAMETER`. It is never ignored. Empty results return `"data": []`.

### GET /api/v1/data/evm/{chainId}/lending/positions

Open positions ranked by liquidation proximity.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `protocol` | string | — | `aave-v3` or `benqi` |
| `asset` | string | — | Only positions with a collateral or debt leg in this asset (Benqi: qiToken market) |
| `min_debt_base` | string | `0` | Dust floor: only positions with `debt_base` above this (USD × 1e18). Must be a non-negative integer |
| `limit`, `offset` | int | | Pagination |

### GET /api/v1/data/evm/{chainId}/lending/positions/{account}

All positions for one account, across protocols.

### GET /api/v1/data/evm/{chainId}/lending/stats

Open and liquidatable position counts per protocol.

### GET /api/v1/data/evm/{chainId}/lending/risk

Aggregate risk per protocol and per asset: health factor histogram, near-liquidation count, bad debt, value at risk. Takes `protocol` and `min_debt_base`. The applied floor is echoed back as `min_debt_base`.

### GET /api/v1/data/evm/{chainId}/lending/risk/timeseries

Periodic risk snapshots (every 15 min). `days` defaults to 30, max 365.

### GET /api/v1/data/evm/{chainId}/lending/alerts

Health factor crossing alerts (became liquidatable, entered near-liquidation), newest first. Paginated.

### GET /api/v1/data/evm/{chainId}/lending/liquidations

Executed liquidations, newest first.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `protocol` | string | — | `aave-v3` or `benqi` |
| `account` | string | — | Matches the **borrower or the liquidator** |
| `asset` | string | — | Matches the collateral or debt asset |
| `limit`, `offset` | int | | Pagination |

**Response:**
```json
{
  "data": [
    {
      "protocol": "aave-v3",
      "block_number": 98432377,
      "tx_hash": "0x...",
      "liquidator": "0x777e0fc6...",
      "borrower": "0x9674fc77...",
      "collateral_asset": "0x...",
      "debt_asset": "0x...",
      "repay_amount": "10254000000",
      "seize_amount": "...",
      "repaid_usd": "10255549220530094080000"
    }
  ],
  "meta": { "limit": 5, "offset": 0, "has_more": false }
}
```
`repaid_usd` above is $10,255.55.

### GET /api/v1/data/evm/{chainId}/lending/liquidations/daily

Daily liquidation counts per protocol, with size buckets (`over_25`, `over_100`, `over_1000` USD) and `total_repaid_usd`. `days` defaults to 30, max 3650.

---

## Data API - P-Chain

### GET /api/v1/data/pchain/txs

List P-Chain transactions.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `tx_type` | string | Filter by transaction type |
| `subnet_id` | string | Filter by subnet ID (CB58) |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "tx_id": "22FdhKfCTTW...xxNeV",
      "tx_type": "RegisterL1Validator",
      "block_number": 12345678,
      "block_time": "2024-12-01T00:00:00Z",
      "tx_data": {
        "Balance": 2000000000000,
        "Signer": { "...": "..." },
        "ValidationID": "3DEF...uvw"
      }
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true,
    "next_cursor": "12345678:2ZW6HUePB..."
  }
}
```

**Common Transaction Types** (filter values for `?tx_type=`):
- `CreateSubnet` — Create a new subnet
- `CreateChain` — Create a blockchain in a subnet
- `AddValidator` — Add validator to primary network
- `AddDelegator` — Add delegator to primary network
- `ConvertSubnetToL1` — Convert subnet to L1
- `RegisterL1Validator` — Register L1 validator
- `IncreaseL1ValidatorBalance` — Top up validator balance
- `DisableL1Validator` — Disable L1 validator
- `SetL1ValidatorWeight` — Change validator weight

> **Note:** `tx_type` values have **no `Tx` suffix** (use `CreateSubnet`, not `CreateSubnetTx`). Filtering with a `…Tx` value returns `200` with `0` results. The authoritative, always-current list of types is `GET /api/v1/data/pchain/tx-types`.

---

### GET /api/v1/data/pchain/txs/{txId}

Get a specific P-Chain transaction by ID.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `txId` | string | Transaction ID (CB58) |

**Response:** Same format as single item in `GET /pchain/txs` response, wrapped in `{"data": {...}}`.

---

### GET /api/v1/data/pchain/tx-types

Get P-Chain transaction types with counts.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Restrict the count to the last N days (omit for all-time) |

**Response:**
```json
{
  "data": [
    { "tx_type": "AddDelegator", "count": 150000 },
    { "tx_type": "AddValidator", "count": 50000 },
    { "tx_type": "RegisterL1Validator", "count": 1200 }
  ]
}
```

---

### GET /api/v1/data/pchain/stats

Headline P-Chain counters for an overview page.

**Response:**
```json
{
  "data": {
    "active_l1_subnets": 42,
    "active_legacy_subnets": 15,
    "active_chains": 58,
    "active_validators": 1200,
    "recent_transactions": 34000,
    "total_l1_fees_paid": 5000000000
  }
}
```

**Notes:**
- `recent_transactions` is the last 7 days; `total_l1_fees_paid` is all-time in nAVAX.

---

### GET /api/v1/data/pchain/subnet-timeline

Monthly count of subnets converted to L1, oldest first (render a cumulative line from these).

**Response:**
```json
{
  "data": [
    { "period": "2024-01-01T00:00:00Z", "value": 3 },
    { "period": "2024-02-01T00:00:00Z", "value": 5 }
  ]
}
```

---

### GET /api/v1/data/pchain/blocks

Recent P-Chain blocks (summarized from transactions), newest first. Bounded to a recent window for performance.

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `days` | int | 7 | Look-back window in days |
| `limit` | int | 20 | Results per page (max 100) |
| `offset` | int | 0 | Pagination offset |

**Response:**
```json
{
  "data": [
    { "block_number": 24160141, "tx_count": 3, "block_time": "2025-06-01T12:00:00Z", "block_hash": "2ZW6HUePB..." }
  ],
  "meta": { "limit": 20, "offset": 0, "has_more": true }
}
```

---

### GET /api/v1/data/pchain/blocks/{number}

A single P-Chain block by number, including proposer/parent metadata.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `number` | int | Block number |

**Response:**
```json
{
  "data": {
    "block_number": 24160141,
    "tx_count": 3,
    "block_time": "2025-06-01T12:00:00Z",
    "block_hash": "2ZW6HUePB...",
    "parent_id": "2ABC...",
    "proposer_id": "...",
    "proposer_node_id": "NodeID-...",
    "block_type": "AdvanceTime"
  }
}
```

**Notes:**
- `parent_id`, `proposer_id`, `proposer_node_id`, `block_type` are only populated on this single-block endpoint. Returns 404 if the block isn't found.

---

## Data API - Subnets

### GET /api/v1/data/subnets/{subnetId}

Get subnet details with chains and registry metadata.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnetId` | string | Subnet ID (CB58) |

**Response:**
```json
{
  "data": {
    "subnet": {
      "subnet_id": "2ABC...xyz",
      "subnet_type": "l1",
      "created_block": 10000000,
      "created_time": "2024-01-01T00:00:00Z",
      "chain_id": "2DEF...uvw",
      "converted_block": 12000000,
      "converted_time": "2024-06-01T00:00:00Z"
    },
    "chains": [
      {
        "chain_id": "2DEF...uvw",
        "subnet_id": "2ABC...xyz",
        "chain_name": "My Chain",
        "vm_id": "srEXiWaH...",
        "created_block": 10000001,
        "created_time": "2024-01-01T00:01:00Z"
      }
    ],
    "registry": {
      "subnet_id": "2ABC...xyz",
      "name": "My L1",
      "description": "A description",
      "logo_url": "https://example.com/logo.png",
      "website_url": "https://example.com"
    }
  }
}
```

**Notes:**
- `chain_id`, `converted_block`, `converted_time` are omitted if the subnet hasn't been converted to L1
- `registry` is `null` if no L1 registry metadata exists
- **Subnet Types:** `regular`, `elastic`, `l1`

---

## Data API - Chains

### GET /api/v1/data/chains

List all blockchains with enriched subnet, L1 registry, and validator data. This is the primary endpoint for listing L1s and chains.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chain_type` | string | Filter by type: `l1` or `legacy` |
| `subnet_id` | string | Filter by subnet ID |
| `category` | string | Filter by category (e.g., `DeFi`, `Gaming`) — case-insensitive |
| `active` | string | Set to `true` to only show chains with active validators |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "chain_id": "2q9e4r6Mu...",
      "chain_name": "My Chain",
      "vm_id": "srEXiWaH...",
      "created_block": 10000001,
      "created_time": "2024-01-01T00:01:00Z",
      "subnet_id": "2ABC...xyz",
      "chain_type": "l1",
      "converted_block": 12000000,
      "converted_time": "2024-06-01T00:00:00Z",
      "name": "My L1",
      "description": "A high-performance L1",
      "logo_url": "https://example.com/logo.png",
      "website_url": "https://example.com",
      "evm_chain_id": 12345,
      "categories": ["DeFi", "Gaming"],
      "socials": [
        { "name": "twitter", "url": "https://twitter.com/mychain" }
      ],
      "rpc_url": "https://rpc.mychain.com",
      "explorer_url": "https://explorer.mychain.com",
      "sybil_resistance_type": "Proof of Authority",
      "network_token": {
        "name": "My Token",
        "symbol": "MTK",
        "decimals": 18,
        "logo_uri": "https://example.com/token.png"
      },
      "network": "mainnet",
      "validator_count": 10,
      "active_validators": 8,
      "total_staked": 5000000,
      "total_staked_tokens": "5000",
      "total_fees_paid": 1500000000000,
      "staking_model": "pos",
      "staked_token": "MTK"
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true,
    "next_cursor": "10000001:2q9e4r6Mu..."
  }
}
```

**Notes:**
- All registry fields (`name`, `description`, `logo_url`, `website_url`, `evm_chain_id`, `categories`, `socials`, `rpc_url`, `explorer_url`, `sybil_resistance_type`, `network_token`, `network`) are optional — only present if registry metadata exists
- `converted_block`, `converted_time` only present for L1-converted subnets
- `validator_count`, `active_validators`, `total_staked`, `total_fees_paid` only present for L1 chains
- `staking_model` — always present: `pos`, `poa`, `none` or `unknown`. `none` means the conversion named a placeholder manager address (empty, zero or a precompile such as `0x...0001`), so the validator set can't change. Otherwise taken from the on-chain ValidatorManager type, else the registry's `sybil_resistance_type`, else `pos` when a staking factor is configured. Primary Network chains are `pos`. `staked_token` is the staked token's symbol, present only for `pos`.
- `total_staked` is the raw sum of **active** validator weights (meaningless for `poa`, hide it there); `total_staked_tokens` is that sum converted to **whole tokens** (decimal string), present only for Proof-of-Stake chains with a configured staking factor. Pair it with `network_token.symbol` for display.
- `socials` is an array of `{"name": string, "url": string}` objects
- `network_token` includes native token info when available

---

### GET /api/v1/data/chains/{chainId}/risk

Per-chain decentralization and ValidatorManager risk profile (L2Beat-style). Keyed by the same `chain_id` (CB58) as `/api/v1/data/chains`.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | string | Blockchain ID (CB58) |

**Response:**
```json
{
  "data": {
    "chain_id": "2q9e4r6Mu...",
    "validator_manager": {
      "address": "0x1234...",
      "type": "PoA",
      "deployed_on": "c-chain",
      "owner": {
        "address": "0x5678...",
        "kind": "multisig",
        "multisig": { "threshold": 3, "owners": 5 }
      },
      "proxy": {
        "is_proxy": true,
        "implementation": "0xabcd...",
        "proxy_admin": "0xdef0...",
        "proxy_admin_owner": "0x9999...",
        "upgrade_delay_seconds": 0
      },
      "churn": { "period_seconds": 3600, "max_churn_percentage": 20 },
      "balancer_owner": null,
      "security_modules": null
    },
    "decentralization": {
      "active_validator_count": 8,
      "nakamoto_33": 2,
      "nakamoto_50": 3,
      "total_weight": "5000000",
      "weights": ["2000000", "1500000", "..."]
    },
    "economic": null,
    "updated_at": "2026-06-17T12:00:00Z"
  }
}
```

**Notes:**
- `validator_manager.type` — `PoA`, `PoS-native`, `PoS-erc20`, `balancer`, `placeholder`, or `unknown`. `balancer` means the owner is a Suzaku BalancerValidatorManager. `placeholder` means the address is empty, zero or an EVM precompile (`0x01`-`0x09`), which can never sign a validator-set change. `unknown` usually means the manager lives on the L1 itself and we have no RPC for that chain
- `deployed_on` — `c-chain` (manager reachable even if the L1 halts), `self` (lives on the L1, unreachable if stuck), or `unknown`
- `owner.kind` — `eoa`, `multisig`, `timelock`, `dao`, `contract`, or `unknown`; `multisig` populated only for detected Gnosis Safes
- `proxy.upgrade_delay_seconds` — `0` means upgrades are instant; a timelock delay otherwise
- `balancer_owner` and `security_modules` — null unless `type` is `balancer`. For a balancer, `owner` is the balancer contract, `balancer_owner` is whoever controls it (it sets module weights), and `security_modules` lists each module: `{address, kind, weight, max_weight, owner}`. `kind` is `suzaku_middleware` (validators admitted through Suzaku restaking), `poa` (a module that only points back at its balancer, Suzaku's PoA module), or `unknown`. `weight` is the validator weight the module holds now, `max_weight` its cap. Example (Dexalot): a `poa` module at weight 0 and a `suzaku_middleware` at 5000000, both owned by the same EOA as the balancer.
- `decentralization` — present only when the chain has active L1 validators; `nakamoto_33`/`nakamoto_50` are the min validators controlling >33%/>50% of active weight; `weights` are raw, sorted descending
- `economic` — reserved for a later tier; currently always `null`
- Returns 404 if the chain ID is unknown

---

## Data API - Validators

### GET /api/v1/data/validators

List validators (L1 and legacy subnet validators).

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Filter by subnet ID (CB58) |
| `active` | string | Set to `true` for active only |
| `node_id` | string | Filter by node ID (returns all of the node's validations) |
| `sort` | string | `weight` (default), `total_stake`, `total_delegated`, `delegator_count`, `delegation_fee_percent`, `end_time` |
| `order` | string | `desc` (default) or `asc` |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "subnet_id": "2ABC...xyz",
      "validation_id": "3DEF...uvw",
      "node_id": "NodeID-ABC123...",
      "weight": 100000,
      "staked_amount": "100",
      "staked_token": "MTK",
      "start_time": "2024-12-01T00:00:00Z",
      "active": true,
      "end_time": "2025-12-01T00:00:00Z",
      "uptime_percentage": 99.5,
      "balance": 1000000000000,
      "initial_deposit": 2000000000000,
      "total_topups": 500000000000,
      "refund_amount": 0,
      "fees_paid": 1500000000000
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  }
}
```

**Notes:**
- All balance/fee values in nAVAX (1 AVAX = 1,000,000,000 nAVAX)
- `staked_amount` / `staked_token` — the validator's stake in **whole tokens** (decimal string, full precision) and the token's symbol. Present only for Proof-of-Stake chains (incl. the Primary Network, in AVAX); omitted for Proof-of-Authority chains, where `weight` is an abstract value with no token conversion. Derive your UI from the presence of `staked_amount`.
- `end_time` — omitted for L1 validators with no expiry
- `uptime_percentage` — omitted for L1 validators (not meaningful)
- `balance`, `initial_deposit`, `total_topups`, `refund_amount`, `fees_paid` — only present for L1 validators
- Offset-only pagination (no cursor support)
- Legacy subnet validators may include `primary_stake` and `primary_uptime` from Primary Network
- Active Primary Network validators also carry `delegation_fee_percent`, `delegator_count`, `total_delegated` and `total_stake` (own + delegated, nAVAX), refreshed from `platform.getCurrentValidators` every 5 minutes. `delegator_count` counts active delegations, not unique delegator addresses.
- `sort`/`order` apply to non-legacy listings. Legacy subnet listings sort by Primary Network stake.

---

### GET /api/v1/data/validators/{id}

Get detailed validator info by validation ID or node ID. A node ID can match several validations over time (re-registrations, renewed stakes); it resolves to the node's active validation, else its most recent one. Use `/validators?node_id=` to list them all.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Validation ID or Node ID (e.g., `NodeID-ABC123...`) |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Disambiguate when a node validates multiple subnets |

**Response:**
```json
{
  "data": {
    "subnet_id": "2ABC...xyz",
    "validation_id": "3DEF...uvw",
    "node_id": "NodeID-ABC123...",
    "weight": 100000,
    "start_time": "2024-12-01T00:00:00Z",
    "active": true,
    "balance": 1000000000000,
    "initial_deposit": 2000000000000,
    "total_topups": 500000000000,
    "refund_amount": 0,
    "fees_paid": 1500000000000,
    "tx_hash": "22FdhK...xxNeV",
    "tx_type": "RegisterL1Validator",
    "created_block": 12345678,
    "created_time": "2024-12-01T00:00:00Z",
    "bls_public_key": "0x85abcd...",
    "remaining_balance_owner": "P-avax1abc...",
    "total_deposited": 2500000000000,
    "days_remaining": 45.2,
    "estimated_days_left": 22.6,
    "daily_fee_burn": 44236800,
    "network_share_percent": 12.5,
    "delegation_fee_percent": 2.0,
    "delegator_count": 150,
    "total_delegated": 50000000000000,
    "total_stake": 52000000000000
  }
}
```

**Detail-only fields (not in list endpoint):**
- `tx_hash`, `tx_type` — Registration transaction info
- `created_block`, `created_time` — When the validator was registered
- `bls_public_key` — BLS public key
- `remaining_balance_owner` — P-Chain address (bech32 format, e.g., `P-avax1...`)
- `total_deposited` — Total amount ever deposited (initial + topups)
- `days_remaining` — Days until balance runs out at 512 nAVAX/sec burn rate
- `estimated_days_left` — Estimated days remaining based on current balance
- `daily_fee_burn` — Daily fee burn in nAVAX (512 nAVAX/sec = 44,236,800 nAVAX/day per validator)
- `network_share_percent` — This validator's total stake (own + delegated) as % of the Primary Network's active total (active Primary Network validators only)
- `delegation_fee_percent`, `delegator_count`, `total_delegated`, `total_stake` — also in the list endpoint (active Primary Network validators)

---

### GET /api/v1/data/validators/{id}/delegations

List the delegations to a Primary Network validator (AddDelegator and AddPermissionlessDelegator transactions). Cached for 5 minutes.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Node ID (`NodeID-...`) |

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `active` | bool | - | `true` = running, `false` = ended, omitted = both |
| `sort` | string | `stake` | `stake`, `end_time` or `start_time` |
| `order` | string | `desc` | `desc` or `asc` |
| `limit` | int | 20 | Page size, max 500 |
| `offset` | int | 0 | Offset |

**Response:**
```json
{
  "data": [
    {
      "tx_id": "28E9TkTq8tynDrj8MTwypdvwTAN6TCxS36uuceqk7MVBk42JHW",
      "reward_owner": ["P-avax1..."],
      "stake": "398132993099",
      "start_time": "2026-10-31T20:03:40Z",
      "end_time": "2026-11-17T20:00:08Z",
      "active": true
    }
  ],
  "meta": { "total": 312, "limit": 20, "offset": 0, "has_more": true }
}
```

- `stake` is nAVAX as a string.
- `reward_owner` is null when the tx names no reward address.
- `active` is `end_time` in the future.

---

### GET /api/v1/data/delegators/{address}/delegations

List every Primary Network delegation whose reward owner includes this address, across all validators. Cached for 5 minutes.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `address` | string | P-Chain address, `P-avax1...` or `avax1...` |

**Query Parameters:** same as `/validators/{id}/delegations` (`active`, `sort`, `order`, `limit` max 500, `offset`).

**Response:**
```json
{
  "data": [
    {
      "tx_id": "26Qm4E54ge2Qs8nT6d9RAkCj7FUojQhmnm7ray61C5y4Vcos2A",
      "node_id": "NodeID-EmyEA3mdxUZibCy8heBSV1hj52zphK3J6",
      "stake": "227146366379315",
      "start_time": "2026-10-25T16:11:04Z",
      "end_time": "2026-11-09T11:12:22Z",
      "active": true
    }
  ],
  "meta": {
    "total": 1204, "limit": 20, "offset": 0, "has_more": true,
    "summary": {
      "active_count": 410,
      "active_stake": "1250000000000000",
      "validator_count": 37,
      "first_delegation_time": "2024-02-11T09:30:00Z"
    }
  }
}
```

- `summary` covers all of the address's delegations, whatever the `active` filter. `validator_count` counts distinct nodes among active delegations. `first_delegation_time` is null when there are none.
- A malformed or non-mainnet address returns 400. An address with no delegations returns 200 with an empty list.

---

### GET /api/v1/data/validators/{id}/deposits

Get deposit history for a validator.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Validation ID or Node ID |

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `cursor` | string | Cursor for keyset pagination |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "tx_id": "22FdhK...xxNeV",
      "tx_type": "RegisterL1Validator",
      "block_number": 12345678,
      "block_time": "2024-12-01T00:00:00Z",
      "amount": 2000000000000
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  }
}
```

**Notes:**
- `amount` in nAVAX
- `tx_type` values: `RegisterL1Validator` (initial deposit), `IncreaseL1ValidatorBalance` (top-up)

---

## Metrics API - Fees

### GET /api/v1/metrics/fees

Get L1 validation fee statistics per subnet, plus an aggregated summary.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Filter by subnet ID |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |
| `count` | string | Set to `true` to include total count |

**Response:**
```json
{
  "data": [
    {
      "subnet_id": "2ABC...xyz",
      "total_deposited": 100000000000000,
      "initial_deposits": 80000000000000,
      "top_up_deposits": 20000000000000,
      "total_refunded": 5000000000000,
      "current_balance": 15000000000000,
      "total_fees_paid": 80000000000000,
      "deposit_tx_count": 150,
      "validator_count": 50
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  },
  "summary": {
    "total_deposited": 500000000000000,
    "total_refunded": 25000000000000,
    "total_fees_paid": 400000000000000,
    "current_balance": 75000000000000,
    "subnet_count": 10,
    "validator_count": 200
  }
}
```

**Notes:**
- `summary` is **always** included — aggregated totals across ALL L1 subnets (not just the current page)
- All values in nAVAX
- Offset-only pagination (no cursor support)

---

### GET /api/v1/metrics/fees/daily

Get daily fee burn data for an L1 subnet, computed from validator active periods (512 nAVAX/sec per validator). Omit `subnet_id` for network-wide totals: each day is summed across every L1 on the primary P-Chain, and `active_validators` counts validators across all of them.

**Query Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `subnet_id` | string | No | Subnet ID (CB58). Omit for all L1s |
| `days` | int | No | Number of days (default: all days since first validator) |
| `validators` | string | No | Set to `true` to include per-validator breakdown (requires `subnet_id`) |

**Response:**
```json
{
  "data": [
    {
      "date": "2025-01-15",
      "total_fees_burned": 221184000,
      "active_validators": 5,
      "validators": [
        {
          "validation_id": "2ZW6HUePB...",
          "node_id": "NodeID-P7oB2McjBGgW...",
          "fees_burned": 44236800,
          "active_seconds": 86400
        }
      ]
    }
  ]
}
```

**Notes:**
- `validators` array only included when `?validators=true` is passed
- `date` format is `YYYY-MM-DD`
- Fee burn = `active_seconds * 512` nAVAX
- Full day = 86400 seconds = 44,236,800 nAVAX per validator
- Returns empty array if no validators exist for the subnet

---

### GET /api/v1/metrics/evm/{chainId}/fees/burned

Transaction-fee burn for an EVM chain, split into total / base-fee / priority-tip, per period plus all-time cumulative. On the C-Chain 100% of the fee is burned.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID (e.g. 43114) |

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `granularity` | string | `day` | `hour`, `day`, `week`, or `month` |
| `from` | string | — | Start time (date, RFC3339, or unix) |
| `to` | string | — | End time |
| `limit` | int | 100 | Number of periods (max 1000) |

**Response:**
```json
{
  "data": {
    "chain_id": 43114,
    "granularity": "day",
    "unit": "nAVAX",
    "cumulative": {
      "total_burned": "5013452000000000",
      "base_fee_burned": "4900000000000000",
      "priority_fee_burned": "113452000000000"
    },
    "series": [
      {
        "period": "2025-06-01T00:00:00Z",
        "total_burned": "12000000000000",
        "base_fee_burned": "11500000000000",
        "priority_fee_burned": "500000000000"
      }
    ]
  }
}
```

**Notes:**
- All amounts are nAVAX strings (1 nAVAX = 1e9 wei). `series` is chronological (oldest first).

---

### GET /api/v1/metrics/burned/total

Network-wide all-time AVAX burned, split by chain (P / X / C). Only the C-Chain is indexed today; the others are `null` until computed.

**Response:**
```json
{
  "data": {
    "unit": "nAVAX",
    "total_burned": "5013452000000000",
    "by_chain": {
      "p_chain": null,
      "x_chain": null,
      "c_chain": "5013452000000000"
    },
    "updated_at": "2026-06-17T12:00:00Z"
  }
}
```

---

### GET /api/v1/metrics/storage

> **Operator-only.** Exposes internal table names / sizes / row counts, so it's gated behind the same bearer token as `/metrics`. Send `Authorization: Bearer <ICICLE_METRICS_TOKEN>`. Without it: `401` (or `404` when no token is configured). Not part of the public consumer API.

Per-table on-disk size (bytes) and row counts for the indexer's ClickHouse store, largest first. Operational metadata only — table names and sizes, no row contents.

**Response:**
```json
{
  "data": [
    { "table": "raw_traces", "size_bytes": 123456789012, "rows": 10000000000 },
    { "table": "raw_txs", "size_bytes": 12345678901, "rows": 1000000000 }
  ]
}
```

---

## Metrics API - Staking

Primary Network staking. Validator and delegation numbers are refreshed from `platform.getCurrentValidators` every 5 minutes. All amounts are **nAVAX as decimal strings** (they exceed JS safe integers).

### GET /api/v1/metrics/staking

Staking overview in one request. Cached for 1 minute.

**Response:**
```json
{
  "data": {
    "as_of": "2026-10-30T12:00:00Z",
    "validator_count": 568,
    "delegation_count": 34912,
    "own_stake": "160695147139964970",
    "delegated_stake": "42287944243437480",
    "total_stake": "202983091383402450",
    "total_supply": "475774638238261125",
    "staked_supply_percent": 42.66,
    "delegation_fee": {
      "median_percent": 4.74,
      "histogram": [
        { "label": "2%", "min_percent": 2, "max_percent": 2, "validator_count": 262, "total_stake": "..." },
        { "label": "2-5%", "min_percent": 2, "max_percent": 5, "validator_count": 74, "total_stake": "..." },
        { "label": "5-10%", "min_percent": 5, "max_percent": 10, "validator_count": 10, "total_stake": "..." },
        { "label": "10-20%", "min_percent": 10, "max_percent": 20, "validator_count": 82, "total_stake": "..." },
        { "label": "20%+", "min_percent": 20, "max_percent": null, "validator_count": 140, "total_stake": "..." }
      ]
    },
    "concentration": {
      "top10_percent": 14.74,
      "top50_percent": 54.9,
      "top100_percent": 77.17,
      "nakamoto_33": 25,
      "nakamoto_50": 44
    }
  }
}
```

**Notes:**
- `delegation_count` counts active delegations, not unique delegator addresses
- `total_supply` is AVAX **total** supply from `platform.getCurrentSupply` (not circulating). It and `staked_supply_percent` are null until the first daily snapshot exists
- Histogram buckets cover `(min_percent, max_percent]`; the first is exactly the 2% protocol minimum, the last is open-ended
- Concentration and Nakamoto use total stake (own + delegated); Nakamoto is the fewest validators holding **more than** 33% / 50%

### GET /api/v1/metrics/staking/unlocks

Stake whose lock ends in each of the next N weeks (weeks start Monday 00:00 UTC; the first is the current week). Cached for 5 minutes.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `weeks` | int | Weeks ahead, 1-53 (default 12) |

**Response:**
```json
{
  "data": [
    {
      "week_start": "2026-10-26T00:00:00Z",
      "validator_stake": "1200000000000000",
      "delegated_stake": "350000000000000",
      "validator_count": 9,
      "delegation_count": 412
    }
  ]
}
```

Delegations come from still-running `AddDelegator` / `AddPermissionlessDelegator` transactions.

### GET /api/v1/metrics/staking/timeseries

Daily staking snapshots (last sample of each UTC day), oldest first. History starts 2026-10-30.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Subnet ID (default: Primary Network) |
| `days` | int | Days of history, 1-3650 (default 365) |

**Response:**
```json
{
  "data": [
    {
      "date": "2026-10-30",
      "validator_count": 568,
      "delegation_count": 34912,
      "own_stake": "160695147139964970",
      "delegated_stake": "42287944243437480",
      "total_stake": "202983091383402450",
      "total_supply": "475774638238261125",
      "staked_tokens": "202983091.38340245",
      "staked_token": "AVAX"
    }
  ]
}
```

**Notes:**
- For L1s, `own_stake` is the raw validator weight sum. `delegation_count`, `delegated_stake` are 0 and `total_supply` is omitted
- `staked_tokens` / `staked_token` are present only for PoS chains with a configured staking factor (and the Primary Network)

---

### GET /api/v1/metrics/staking/liquid

AVAX liquid staking protocols with their latest figures. The indexer samples each live protocol every validator sync cycle (about 5 minutes):
- Pooled AVAX and token supply come from the C-Chain token contract.
- Delegation figures cover running Primary Network delegations whose rewards owner includes one of the protocol's P-Chain addresses.

Cached for 5 minutes.

**Response:**
```json
{
  "data": {
    "protocols": [
      {
        "id": "benqi-savax",
        "name": "BENQI",
        "token_symbol": "sAVAX",
        "token_contract": "0x2b2C81e08f1Af8835a78Bb2A90AE924ACE0eA4bE",
        "token_decimals": 18,
        "status": "live",
        "attribution": "unconfirmed",
        "p_chain_addresses": ["P-avax19zfygxaf59stehzedhxjesads0p5jdvfeedal0"],
        "pooled_avax": "24088038797328708",
        "token_supply": "18630244367219040899253899",
        "exchange_rate": "1.292953453671",
        "active_delegated": "22565009736807640",
        "active_delegations": 27504,
        "validator_count": 107,
        "share_of_delegated_percent": 53.37,
        "as_of": "2026-11-01T10:05:12.345Z"
      }
    ],
    "totals": {
      "pooled_avax": "24088038797328708",
      "active_delegated": "22565009736807640",
      "active_delegations": 27504,
      "share_of_delegated_percent": 53.37
    },
    "network_delegated_stake": "42277000000000000"
  }
}
```

**Notes:**
- Amounts: `pooled_avax`, `active_delegated` and `network_delegated_stake` are nAVAX. `token_supply` is in token base units (`token_decimals`). `exchange_rate` is AVAX per whole token.
- `attribution` is `unconfirmed` until the protocol confirms its P-Chain addresses.
- Live figures are null until the protocol has been sampled. Protocols with status `ended` never have live figures.
- `share_of_delegated_percent` is `active_delegated` divided by all running Primary Network delegated stake. Totals sum the live protocols.

---

### GET /api/v1/metrics/staking/liquid/timeseries

Daily liquid staking samples (last sample of each UTC day), oldest first. History starts 2026-10-31.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `protocol` | string | Protocol id, e.g. `benqi-savax` (default: all) |
| `days` | int | Days of history, 1-3650 (default 365) |

**Response:**
```json
{
  "data": [
    {
      "date": "2026-11-01",
      "protocol_id": "benqi-savax",
      "pooled_avax": "24088038797328708",
      "exchange_rate": "1.292953453671",
      "active_delegated": "22565009736807640",
      "active_delegations": 27504,
      "validator_count": 107
    }
  ]
}
```

---

### GET /api/v1/metrics/restaking

L1s whose validators are secured by restaking vaults on the C-Chain. Only Suzaku is tracked. Every L1 in Suzaku's L1Registry (`0xaA59b19A7636bf6d821aA124A14eEE6C92746110`) with at least one active vault is listed, active or not, so new L1s appear on their own. The indexer reads the contracts every validator sync cycle (about 5 minutes). Cached for 5 minutes.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Only this L1 subnet ID |
| `chain_id` | string | Only the L1 with this blockchain ID |

**Response:**
```json
{
  "data": {
    "protocols": [
      { "id": "suzaku", "name": "Suzaku", "url": "https://suzaku.network" }
    ],
    "l1s": [
      {
        "subnet_id": "wenKDikJWAYQs3f2v9JhV86fC6kHZwkFZsuUBftnmgZ4QXPnu",
        "blockchain_id": "21Ths5Afqi5r4PaoV8r8cruGZWhN11y5rxvy89K8px7pKy3P8E",
        "protocol_id": "suzaku",
        "balancer": "0xcff0fc701ef47d6217fdf9def903990b7afa8ac7",
        "middleware": "0x9411307279456450abf9b5181aa7a02271f0dc34",
        "is_active": true,
        "active_validators": 10,
        "restaked_weight": 5000000,
        "restaked_validators": 10,
        "total_pchain_weight": 5000000,
        "share_of_weight_percent": 100,
        "module_weight": 5000000,
        "operator_count": 1,
        "vaults": [
          {
            "address": "0xc9a25f0a8436de76e999787bd509edba0d2471a2",
            "collateral": {
              "address": "0x093783055f9047c2bff99c4e414501f8a147bc69",
              "wrapper": "0x51111e1f63d3341573882c0ed844e0876efd634d",
              "symbol": "ALOT",
              "decimals": 18
            },
            "deposited": "13684218504272505054021750",
            "allocated": "5000000000000000000000000",
            "idle": "8684218504272505054021750",
            "limit": "5000000000000000000000000"
          }
        ],
        "sampled_at": "2026-11-01T14:05:12.345Z"
      }
    ]
  }
}
```

**Notes:**
- Vault amounts are strings in the collateral token's base units (`collateral.decimals`). `collateral.address` is the underlying token; `wrapper` is the vault's `collateral()` token (a 1:1 DefaultCollateral wrapper), equal to `address` when the vault holds the token directly.
- `deposited` is the vault's active stake. `allocated` is what the vault delegates to the L1's operators, capped by `limit` (the vault's limit for this L1). `idle` is `deposited - allocated` and secures nothing.
- `is_active` and `active_validators` use the `/data/chains` definition (L1 validators with a P-Chain balance).
- `restaked_weight` is the P-Chain weight of active validators the protocol's middleware registered; `total_pchain_weight` is that of all active validators. `share_of_weight_percent` is their ratio, null when the L1 has no active weight.
- `module_weight` is the middleware's current weight in the L1's BalancerValidatorManager, straight from the contract.
- `operator_count` counts operators with allocated stake in at least one vault.
- No USD values.

### GET /api/v1/metrics/lending

Market-level supply, borrow, rates and risk parameters of the C-Chain lending protocols: Aave V3, Aave V4 (Core Hub) and BENQI Lending. One row per protocol x asset, every market. The indexer reads the contracts every validator sync cycle (about 5 minutes). Cached for 2 minutes. Positions and liquidation risk are under [Data API - Lending](#data-api---lending).

Anchors, verified on-chain 2026-11-01: Aave V3 PoolAddressesProvider `0xa97684ead0e402dC232d5A977953DF7ECBaB3CDb` (Pool, data provider and oracle resolved from it), BENQI Comptroller `0x486Af39519B4Dc9a7fCcd318217352830E8AD9b4` (markets and oracle resolved from it), Aave V4 Core Hub `0xd07369fAE4A5BB13c9Ce446B052c7867B1AbDf6e` with its main, AVAX-correlated, Ethena and forex spokes (bgd-labs aave-address-book `AaveV4Avalanche`).

**Response:**
```json
{
  "data": {
    "protocols": [
      { "id": "aave-v3", "name": "Aave V3", "url": "https://aave.com" },
      { "id": "aave-v4", "name": "Aave V4", "url": "https://aave.com" },
      { "id": "benqi", "name": "BENQI Lending", "url": "https://benqi.fi" }
    ],
    "markets": [
      {
        "protocol_id": "aave-v3",
        "market": "0x794a61358d6845594f94dc1db02a252b5b4814ad",
        "asset": "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7",
        "symbol": "WAVAX",
        "decimals": 18,
        "total_supplied": "16234165023996328671909803",
        "total_borrowed": "5956815354478002969493270",
        "utilization_percent": 36.69,
        "supply_apy_percent": 0.96,
        "borrow_apy_percent": 3.31,
        "collateral_enabled": true,
        "ltv_percent": 0,
        "liquidation_threshold_percent": 73,
        "price_usd": 8.6738,
        "supplied_usd": 140811170.3,
        "borrowed_usd": 51668000.1,
        "supply_cap": "14000000000000000000000000",
        "borrow_cap": "6000000000000000000000000",
        "sampled_at": "2026-11-01T20:45:00.123Z"
      }
    ],
    "summary": {
      "avax": {
        "supplied": "18891060010680541993210619",
        "borrowed": "6972543286048035173078050",
        "by_protocol": [
          { "protocol_id": "aave-v3", "supplied": "16234165023996328671909803", "borrowed": "5956815354478002969493270" },
          { "protocol_id": "aave-v4", "supplied": "1846953770078008305536448", "borrowed": "665755970692981642713434" },
          { "protocol_id": "benqi", "supplied": "809941216606205025764368", "borrowed": "349971960877050560871346" }
        ]
      },
      "savax": {
        "supplied": "15108379190203700096218478",
        "borrowed": "4207175761426682893209",
        "share_of_savax_supply_percent": 81.07,
        "by_protocol": [ { "protocol_id": "aave-v3", "supplied": "6970033295528336495601629", "borrowed": "0" } ]
      },
      "totals": {
        "supplied_usd": 679271270.0,
        "borrowed_usd": 212842023.0,
        "by_protocol": [ { "protocol_id": "aave-v3", "supplied_usd": 431048588.0, "borrowed_usd": 149351152.0 } ]
      }
    },
    "sampled_at": "2026-11-01T20:45:00.123Z"
  }
}
```

**Notes:**
- Amounts are strings in the asset's base units (`decimals`). `asset` is the underlying token; BENQI's qiAVAX holds native AVAX and uses `0x0000000000000000000000000000000000000000` with symbol `AVAX`. `market` is the Aave V3 Pool, the BENQI qiToken, or the Aave V4 Hub.
- Supplied and borrowed: Aave V3 total aToken supply and stable plus variable debt. BENQI `getCash + totalBorrows - totalReserves` and `totalBorrows`. Aave V4 the hub's `getAddedAssets` and `getAssetTotalOwed` (drawn plus premium debt), summed over all spokes.
- Rates: Aave V3 reports annual rates in ray (1e27), so APR = rate / 1e27. BENQI reports per-second rates (`supplyRatePerTimestamp`, `borrowRatePerTimestamp`, 1e18-scaled), so APR = rate x 31,536,000 / 1e18. Aave V4 reports the hub's annual drawn rate in ray. All three accrue per second, so APY = (1 + APR / 31,536,000)^31,536,000 - 1 (the same convention as the Aave app). Borrow is the variable rate.
- Aave V4 specifics: `borrow_apy_percent` is the base drawn rate. Individual borrowers pay more through their risk premium. `supply_apy_percent` is estimated as drawn rate x (owed / added) x (1 - liquidity fee), so it slightly understates supplier yield. Collateral parameters and caps are set per spoke in V4, so `collateral_enabled`, `ltv_percent`, `liquidation_threshold_percent` and the caps are null on aave-v4 rows.
- BENQI has one collateral factor per market: it caps borrowing and is also the liquidation point, so `ltv_percent` equals `liquidation_threshold_percent`. BENQI has no supply caps (`supply_cap` null). A borrow cap of `1` (base unit) means borrowing is disabled, as on Aave a cap of one whole token marks a market being wound down.
- Caps are in base units, null when uncapped (Aave stores caps in whole tokens, converted here).
- `price_usd` comes from each protocol's own oracle (Aave V3 AaveOracle, BENQI PriceOracle, Aave V4 spoke oracles). When the read fails, `price_usd`, `supplied_usd` and `borrowed_usd` are null, not 0, and the market is left out of `summary.totals`.
- `summary.avax` sums WAVAX on Aave V3 and V4 and native AVAX on BENQI. `summary.savax` sums sAVAX. `share_of_savax_supply_percent` is supplied sAVAX over sAVAX `totalSupply()` from `/metrics/staking/liquid`.
- When a protocol's read fails, its previous sample is served. `sampled_at` per market says how fresh it is.

**E-Mode and V4 spoke collateral (`emode_categories`, `markets[].emode`):**
- Aave V3 E-Mode categories are read from the Pool each cycle (`getEModeCategoryCollateralConfig`, `getEModeCategoryLabel`, and the collateral, borrowable and ltvzero bitmaps, where bit i is `getReservesList()[i]`). Each category has one LTV, liquidation threshold and bonus.
- In E-Mode the borrowable bitmap replaces the reserve's own borrowing flag: an asset can be borrowed in the category only if its bit is set, and a user can enter the category only if all their debt is borrowable in it. An empty borrowable bitmap means nothing can be borrowed in that category.
- An asset in a category's ltvzero bitmap has LTV 0 there: it backs no new borrows but keeps the liquidation threshold. `ltv_percent` on an emode entry is this effective LTV, and `configured_ltv_percent` is the category's LTV. `ltv_zero_assets` lists the zeroed collateral per category.
- Aave V4 has no E-Mode. Collateral is set per spoke reserve, so each spoke is listed as a group (`id` = spoke address, `label` = spoke name). The group-level LTV, threshold and bonus are null, and each market's emode entry carries them. V4 uses one collateral factor for both borrowing power and liquidation, so `ltv_percent` equals `liquidation_threshold_percent`. `borrowable` means borrowable and neither paused nor frozen.
- V4 reserves can get a new config while older positions keep the earlier one. `previous_ltv_percent` is the collateral factor of the config just before the current one (null for a reserve on its first config).
- `liquidation_bonus_percent` is the bonus above 100% (1 = 1%). For V4 it is the maximum bonus.
- `emode` is an empty array for markets in no group (always for BENQI).

```json
"emode_categories": [
  {
    "protocol_id": "aave-v3",
    "id": "2",
    "label": "AVAX correlated",
    "ltv_percent": 93,
    "liquidation_threshold_percent": 95,
    "liquidation_bonus_percent": 1,
    "collateral_assets": ["0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7", "0x2b2c81e08f1af8835a78bb2a90ae924ace0ea4be"],
    "borrowable_assets": ["0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7"],
    "ltv_zero_assets": ["0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7", "0x2b2c81e08f1af8835a78bb2a90ae924ace0ea4be"]
  }
]
```

A market row's `emode` entry, for Aave V3 sAVAX:

```json
"emode": [
  { "id": "2", "label": "AVAX correlated", "ltv_percent": 0, "configured_ltv_percent": 93, "liquidation_threshold_percent": 95,
    "liquidation_bonus_percent": 1, "as_collateral": true, "borrowable": false, "previous_ltv_percent": null }
]
```

### GET /api/v1/metrics/lending/timeseries

Daily supplied, borrowed and rates of AVAX or sAVAX across the three protocols, from the last sample of each UTC day, about a year back (see `market=` below for how past days are read). Oldest first.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `asset` | string | `avax` (WAVAX on Aave, native AVAX on BENQI) or `savax`. Required unless `market` is set |
| `market` | string | One market, `<protocol_id>:<asset>`. See below |
| `days` | int | Days back, 1-3650 (default 365) |

**Response:**
```json
{
  "data": [
    {
      "date": "2026-11-01",
      "supplied": "18891060010680541993210619",
      "borrowed": "6972543286048035173078050",
      "by_protocol": [
        { "protocol_id": "aave-v3", "supplied": "16234165023996328671909803", "borrowed": "5956815354478002969493270", "supply_apy_percent": 0.96, "borrow_apy_percent": 3.31 }
      ]
    }
  ]
}
```

Amounts are wei strings (18 decimals, both assets).

### GET /api/v1/metrics/lending/timeseries?market=

With `market=<protocol_id>:<asset>` (asset as in `/metrics/lending`, e.g. `aave-v3:0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7`, or `benqi:0x0000000000000000000000000000000000000000` for qiAVAX), `/metrics/lending/timeseries` returns that market's daily history instead of the AVAX/sAVAX view. `days` is 1-3650 (default 365).

Past days are read at each UTC day's last block on our archive node, with the same contract reader as the live snapshot. That covers about a year back (filled by the indexer a couple of weeks per cycle after a deploy). Today's row is the latest live sample. A market appears from the first day it existed (Aave V4 from its launch).

```json
{
  "data": [
    {
      "date": "2025-11-01",
      "total_supplied": "18159594657983586977444819",
      "total_borrowed": "7019569841697407319450460",
      "supplied_usd": 339836000.1,
      "borrowed_usd": 131364000.2,
      "utilization_percent": 38.65,
      "supply_apy_percent": 1.17,
      "borrow_apy_percent": 3.44,
      "price_usd": 18.714
    }
  ]
}
```

USD fields are null on days the oracle read failed.

### GET /api/v1/metrics/lending/timeseries/totals

Daily supplied and borrowed USD per protocol and overall, from each market's daily sample and its oracle price that day. Markets without a price that day are left out. Cached for 10 minutes. Oldest first.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Days back, 1-3650 (default 365) |

```json
{
  "data": [
    {
      "date": "2026-10-31",
      "supplied_usd": 683158590.7,
      "borrowed_usd": 216620019.3,
      "by_protocol": [
        { "protocol_id": "aave-v3", "supplied_usd": 431147408.1, "borrowed_usd": 149365627.7 }
      ]
    }
  ]
}
```

### Liquidations

Executed liquidations on Aave V3 (Pool `LiquidationCall`), Aave V4 (each spoke's `LiquidationCall`) and BENQI (each qiToken's `LiquidateBorrow`), from `raw_logs`, ingested by the `icicle-lending` service into `lending_liquidations` over the protocols' full history. Values use each protocol's own oracle at the event block: Aave V3 AaveOracle, BENQI PriceOracle (resolved at the block, since it rotates), Aave V4 the spoke's oracle (`getReservePrice`).

- `debt` is what the liquidator repaid: Aave V3 `debtToCover`, BENQI `repayAmount`, Aave V4 `debtAmountRestored` plus restored premium.
- `collateral` is what was seized, in underlying units: Aave V3 `liquidatedCollateralAmount`, Aave V4 `collateralAmountRemoved`, BENQI `seizeTokens` converted with the collateral market's `exchangeRateStored` at the block (it includes the protocol's seize share).
- Assets are the underlying tokens (BENQI qiToken markets are mapped to theirs, native AVAX is `0x00..00`). Symbols come from the lending market snapshots and are empty for markets delisted before the snapshots began.
- USD values are null when the oracle read failed. Daily sums cover priced liquidations only, and `unpriced` counts the rest.

#### GET /api/v1/metrics/lending/liquidations

Per UTC day (today's partial day included): count, repaid and seized USD, by protocol and by collateral asset (the same token across protocols is one row). Cached for 5 minutes. Oldest first.

| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Days back including today, 1-3650 (default 30) |

```json
{
  "data": [
    {
      "date": "2026-10-31",
      "count": 4,
      "unpriced": 0,
      "repaid_usd": 12455.2,
      "seized_usd": 13510.9,
      "by_protocol": [ { "count": 2, "unpriced": 0, "repaid_usd": 6800.1, "seized_usd": 7480.3, "protocol_id": "benqi" } ],
      "by_collateral": [ { "count": 3, "unpriced": 0, "repaid_usd": 9800.0, "seized_usd": 10690.2, "asset": "0x2b2c81e08f1af8835a78bb2a90ae924ace0ea4be", "symbol": "sAVAX" } ]
    }
  ]
}
```

#### GET /api/v1/metrics/lending/liquidations/recent

The latest liquidations, newest first. Cached for 5 minutes.

Sample values are illustrative.

| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Rows, 1-500 (default 50) |
| `min_usd` | number | Keep liquidations whose repaid or seized value is at least this |

```json
{
  "data": [
    {
      "protocol_id": "aave-v4",
      "tx_hash": "0x51cac928d36cffc520519fd7cc66c9651ddadfc955ce00adec3a86e05676ca57",
      "block_number": 99100000,
      "block_time": "2026-11-01T12:00:00Z",
      "borrower": "0x...",
      "liquidator": "0x...",
      "debt": { "address": "0x9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7", "symbol": "USDt", "decimals": 6, "amount": "2487780258", "usd": 2484.80 },
      "collateral": { "address": "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7", "symbol": "WAVAX", "decimals": 18, "amount": "319987725290971256930", "usd": 2708.43 }
    }
  ]
}
```

#### GET /api/v1/metrics/lending/liquidations/top

The largest liquidations by seized USD over the last N days (today included), same shape as `/recent`. Cached for 5 minutes.

| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Days back including today, 1-3650 (default 30) |
| `limit` | int | Rows, 1-100 (default 20) |


---

## Metrics API - DEX

C-Chain DEX swap volume, rolled up per complete UTC day from the swap events in `raw_logs` (`pkg/dexvolume`). The indexer registers pools and rolls up new days every validator sync cycle (about 5 minutes). A day is rolled up once the C-Chain sync has passed its end. The endpoints read only the rollup and are cached for 10 minutes.

**History:** all time, from the first day a tracked factory created a pool. The first run rolls up the last 365 days. Older days are filled backward a few months per cycle, so the full history fills in within about an hour of a fresh deploy.

**What counts:**
- DEXes: Pharaoh V3 (`pharaoh-v3`), Pharaoh DLMM (`pharaoh-dlmm`), Uniswap V3 (`uniswap-v3`), Blackhole CLMM (`blackhole-clmm`), LFJ V2.1 + V2.2 (`lfj`), Pangolin V3 (`pangolin-v3`). Together that is about 94% of DefiLlama's Avalanche DEX volume. Uniswap V4, WOOFi, DODO, Metric and others are not tracked.
- Only pools whose creation event was emitted by one of these factories count, so a contract that merely emits a matching Swap event adds nothing:

| DEX | Factory | Creation event |
|-----|---------|----------------|
| pharaoh-v3 | `0xAE6E5c62328ade73ceefD42228528b70c8157D0d` (RamsesV3Factory), `0xAAA32926fcE6bE95ea2c51cB4Fcb60836D320C42` (legacy) | PoolCreated |
| pharaoh-dlmm | `0xEb480050b016f6c6d45203D2346B68bDDDa23D4D` | LBPairCreated |
| uniswap-v3 | `0x740b1c1de25031C31FF4fC9A62f554A55cdC1baD` | PoolCreated |
| blackhole-clmm | `0x512eb749541B7cf294be882D636218c84a5e9E5F` (AlgebraFactory) | CustomPool / Pool |
| lfj | `0xb43120c4745967fa9b93E79C149E66B0f2D6Fe0c` (V2.1), `0x8e42f2F4101563bF679975178e880FD87d3eFd4e` (V2.2) | LBPairCreated |
| pangolin-v3 | `0x1128F23D0bc0A8396E9FBC3c0c68f5EA228B8256` | PoolCreated |

- Swap events: the Uniswap V3 layout (Uniswap, Pharaoh V3, Pangolin V3, and Blackhole's Algebra pools emit the same event) and the Liquidity Book layout (Pharaoh DLMM, LFJ).

**Pricing (one side per swap, never both legs):**
1. If either token is a USD stablecoin from the stablecoin registry (`peg = 'USD'`), volume is that side's amount at $1.
2. Else if either token is WAVAX, volume is the AVAX amount times the hour's AVAX price. The price is the median AVAX/USD implied by that hour's WAVAX/stablecoin swaps of at least $10 in the same tracked pools (the day's median when an hour has none).
3. Else the swap is counted but unpriced. `priced_share` is priced swaps / swaps.

Priced swaps above $1,000,000 (`flag_threshold_usd`) are counted in `flagged_swaps`. They stay in the volume.

`unique_addresses` counts distinct `tx.from` of the swaps. That includes MEV and arbitrage bots, and an aggregator trade counts its signer. Totals and multi-day windows deduplicate (exact distinct count, not a sum of daily counts).

**Window:** `days=N` is the last N complete UTC days ending `as_of` (the newest rolled-up day), both inclusive. `days=1` is the last complete UTC day, not a rolling 24h, which matches DefiLlama's daily numbers. Before the first rollup, `as_of` and `from` are null and lists are empty.

**Accuracy check:** a reproduction of 2026-11-01 from our node came within 0.6-4.7% of DefiLlama per DEX (Pharaoh V3 -3.5%, Pharaoh DLMM -4.7%, Uniswap V3 -0.6%, Blackhole -2.9%, LFJ -2%), -3.4% overall.

### GET /api/v1/metrics/dex

Volume, swaps and unique addresses per DEX over the window.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Complete UTC days, 1-3650 (default 1) |

**Response:**
```json
{
  "data": {
    "as_of": "2026-11-01",
    "from": "2026-11-01",
    "days": 1,
    "flag_threshold_usd": 1000000,
    "dexes": [
      {
        "id": "pharaoh-v3",
        "name": "Pharaoh V3",
        "protocol": "pharaoh",
        "volume_usd": 45193155.2,
        "swaps": 19275,
        "priced_share": 0.9925,
        "unique_addresses": 1834,
        "flagged_swaps": 0
      }
    ],
    "totals": {
      "volume_usd": 67628000.5,
      "swaps": 63156,
      "priced_share": 0.989,
      "unique_addresses": 4120,
      "flagged_swaps": 0
    }
  }
}
```

Sample values are illustrative. Every tracked DEX is listed, in a fixed order, with zeros when it had no swaps. `protocol` groups Pharaoh V3 and Pharaoh DLMM under `pharaoh`.

### GET /api/v1/metrics/dex/timeseries

Daily volume and swaps, total and per DEX. Oldest first.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Complete UTC days, 1-3650 (default 90) |

**Response:**
```json
{
  "data": [
    {
      "date": "2026-11-01",
      "volume_usd": 67628000.5,
      "swaps": 63156,
      "dexes": [
        { "id": "pharaoh-v3", "volume_usd": 45193155.2, "swaps": 19275 }
      ]
    }
  ]
}
```

### GET /api/v1/metrics/dex/pools

Pools with the most priced volume over the window.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `days` | int | Complete UTC days, 1-30 (default 1) |
| `limit` | int | Pools, 1-100 (default 20) |

**Response:**
```json
{
  "data": [
    {
      "pool": "0x...",
      "dex": "pharaoh-v3",
      "protocol": "pharaoh",
      "token0": { "address": "0xb31f66aa3c1e785363f0875a1b74e27b85fd66c7", "symbol": "WAVAX" },
      "token1": { "address": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e", "symbol": "USDC" },
      "volume_usd": 21000000.0,
      "swaps": 5400
    }
  ]
}
```

`token0`/`token1` are the pool's tokens (Liquidity Book tokenX/tokenY). Symbols come from token metadata and are empty when unknown.

---

## Metrics API - Chain Stats

### GET /api/v1/metrics/evm/{chainId}/stats

Get aggregate statistics for a chain.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |

**Response:**
```json
{
  "data": {
    "chain_id": 43114,
    "chain_name": "C-Chain",
    "latest_block": 54000000,
    "total_blocks": 54000000,
    "total_txs": 250000000,
    "last_block_time": "2025-01-08T12:00:00Z",
    "avg_block_time_seconds": 2.0,
    "avg_gas_used": 8000000,
    "total_gas_used": 432000000000000
  }
}
```

---

## Metrics API - Time Series

### GET /api/v1/metrics/evm/{chainId}/timeseries

List available metrics for a chain.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |

**Response:**
```json
{
  "data": [
    {
      "metric_name": "tx_count",
      "granularities": ["hour", "day", "week"],
      "latest_period": "2025-01-15T00:00:00Z",
      "data_points": 365
    },
    {
      "metric_name": "active_addresses",
      "granularities": ["day", "week"],
      "latest_period": "2025-01-15T00:00:00Z",
      "data_points": 180
    }
  ]
}
```

---

### GET /api/v1/metrics/evm/{chainId}/timeseries/{metric}

Get time series data for a specific metric.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |
| `metric` | string | Metric name (see list below) |

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `granularity` | string | day | Time granularity: `hour`, `day`, `week`, `month` |
| `from` | string | - | Start time (date `YYYY-MM-DD`, RFC3339, or unix timestamp) |
| `to` | string | - | End time (date `YYYY-MM-DD`, RFC3339, or unix timestamp) |
| `limit` | int | 100 | Number of data points (max 1000) |

**Available Metrics:**
- `tx_count` — Transaction count
- `active_addresses` — Unique active addresses
- `active_senders` — Unique senders
- `fees_paid` — Total fees in wei
- `gas_used` — Total gas used
- `contracts` — New contracts deployed
- `deployers` — Unique contract deployers
- `avg_tps` — Average transactions per second
- `max_tps` — Maximum transactions per second
- `avg_gps` — Average gas per second
- `max_gps` — Maximum gas per second
- `avg_gas_price` — Average gas price
- `max_gas_price` — Maximum gas price
- `icm_total` — Total ICM messages
- `icm_sent` — ICM messages sent
- `icm_received` — ICM messages received
- `usdc_volume` — USDC transfer volume
- `cumulative_tx_count` — Cumulative transaction count
- `cumulative_addresses` — Cumulative unique addresses
- `cumulative_contracts` — Cumulative contracts deployed
- `cumulative_deployers` — Cumulative unique deployers

**Response:**
```json
{
  "data": {
    "chain_id": 43114,
    "metric_name": "tx_count",
    "granularity": "day",
    "data": [
      { "period": "2025-01-01T00:00:00Z", "value": 123456 },
      { "period": "2025-01-02T00:00:00Z", "value": 234567 }
    ]
  }
}
```

---

## WebSocket API

### WS /ws/blocks/{chainId}

Stream real-time blocks for a chain via WebSocket.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `chainId` | int | Chain ID |

**Connection:**
```javascript
const ws = new WebSocket('wss://api.l1beat.io/ws/blocks/43114');

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  console.log(msg.type, msg.data);
};
```

**Message Types:**

1. **initial** — Sent on connection with last 10 blocks:
```json
{
  "type": "initial",
  "data": [
    {
      "chain_id": 43114,
      "block_number": 54000000,
      "hash": "0x1234...abcd",
      "parent_hash": "0xabcd...1234",
      "block_time": "2025-01-08T12:00:00Z",
      "miner": "0x0100...",
      "size": 1234,
      "gas_limit": 15000000,
      "gas_used": 8000000,
      "base_fee_per_gas": 25000000000,
      "tx_count": 150
    }
  ]
}
```

2. **new_block** — Sent when a new block is indexed:
```json
{
  "type": "new_block",
  "data": {
    "chain_id": 43114,
    "block_number": 54000001,
    "hash": "0x5678...efgh",
    "parent_hash": "0x1234...abcd",
    "block_time": "2025-01-08T12:00:02Z",
    "miner": "0x0100...",
    "size": 2345,
    "gas_limit": 15000000,
    "gas_used": 9000000,
    "base_fee_per_gas": 26000000000,
    "tx_count": 200
  }
}
```

3. **ping** — Keepalive sent every 30 seconds:
```json
{
  "type": "ping"
}
```

**Notes:**
- Blocks are polled every 500ms
- Connection times out after 60s of inactivity (respond to pings)
- Use `wss://` for HTTPS servers, `ws://` for HTTP

---

## Complete Endpoint Reference

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/api/v1/metrics/indexer/status` | Indexer sync status |
| GET | `/api/v1/metrics/storage` | Per-table storage size & row counts (operator token) |
| GET | `/api/v1/data/evm/{chainId}/blocks` | List blocks |
| GET | `/api/v1/data/evm/{chainId}/blocks/{number}` | Get block by number |
| GET | `/api/v1/data/evm/{chainId}/txs` | List transactions |
| GET | `/api/v1/data/evm/{chainId}/txs/{hash}` | Get transaction detail |
| GET | `/api/v1/data/evm/{chainId}/address/{address}/txs` | Address transactions |
| GET | `/api/v1/data/evm/{chainId}/address/{address}/internal-txs` | Address internal txs |
| GET | `/api/v1/data/evm/{chainId}/labels` | Address labels (batch, max 100) |
| GET | `/api/v1/data/evm/{chainId}/address/{address}/balances` | ERC-20 token balances |
| GET | `/api/v1/data/evm/{chainId}/address/{address}/native` | Native token balance |
| GET | `/api/v1/data/evm/{chainId}/stablecoins` | List stablecoins (supply, holders, 24h) |
| GET | `/api/v1/data/evm/{chainId}/stablecoins/timeseries` | Stablecoin metric time series |
| GET | `/api/v1/data/evm/{chainId}/lending/positions` | Lending positions by liquidation proximity |
| GET | `/api/v1/data/evm/{chainId}/lending/positions/{account}` | Lending positions for one account |
| GET | `/api/v1/data/evm/{chainId}/lending/stats` | Lending open/liquidatable counts |
| GET | `/api/v1/data/evm/{chainId}/lending/risk` | Lending aggregate risk |
| GET | `/api/v1/data/evm/{chainId}/lending/risk/timeseries` | Lending risk snapshots |
| GET | `/api/v1/data/evm/{chainId}/lending/alerts` | Health factor crossing alerts |
| GET | `/api/v1/data/evm/{chainId}/lending/liquidations` | Executed liquidations |
| GET | `/api/v1/data/evm/{chainId}/lending/liquidations/daily` | Daily liquidation stats |
| GET | `/api/v1/data/pchain/txs` | List P-Chain transactions |
| GET | `/api/v1/data/pchain/txs/{txId}` | Get P-Chain transaction |
| GET | `/api/v1/data/pchain/tx-types` | P-Chain transaction type counts |
| GET | `/api/v1/data/pchain/stats` | P-Chain overview counters |
| GET | `/api/v1/data/pchain/subnet-timeline` | Monthly L1-conversion timeline |
| GET | `/api/v1/data/pchain/blocks` | List P-Chain blocks |
| GET | `/api/v1/data/pchain/blocks/{number}` | Get P-Chain block by number |
| GET | `/api/v1/data/subnets/{subnetId}` | Get subnet details |
| GET | `/api/v1/data/chains` | List chains (unified) |
| GET | `/api/v1/data/chains/{chainId}/risk` | Chain risk & decentralization |
| GET | `/api/v1/data/validators` | List validators |
| GET | `/api/v1/data/validators/{id}` | Get validator detail |
| GET | `/api/v1/data/validators/{id}/deposits` | Validator deposit history |
| GET | `/api/v1/data/validators/{id}/delegations` | Delegations to a Primary Network validator |
| GET | `/api/v1/data/delegators/{address}/delegations` | Delegations by reward address |
| GET | `/api/v1/metrics/fees` | L1 fee statistics |
| GET | `/api/v1/metrics/fees/daily` | Daily fee burn |
| GET | `/api/v1/metrics/evm/{chainId}/stats` | Chain aggregate stats |
| GET | `/api/v1/metrics/evm/{chainId}/timeseries` | List available metrics |
| GET | `/api/v1/metrics/evm/{chainId}/timeseries/{metric}` | Get time series data |
| GET | `/api/v1/metrics/evm/{chainId}/fees/burned` | Tx fee burn (total/base/tip) |
| GET | `/api/v1/metrics/burned/total` | Network-wide AVAX burned |
| GET | `/api/v1/metrics/staking` | Primary Network staking summary |
| GET | `/api/v1/metrics/staking/unlocks` | Weekly stake unlock schedule |
| GET | `/api/v1/metrics/staking/timeseries` | Daily staking snapshots |
| GET | `/api/v1/metrics/staking/liquid` | Liquid staking protocols (BENQI sAVAX) |
| GET | `/api/v1/metrics/staking/liquid/timeseries` | Daily liquid staking samples |
| GET | `/api/v1/metrics/restaking` | Restaked collateral per L1 (Suzaku vaults) |
| GET | `/api/v1/metrics/lending` | C-Chain lending markets (Aave V3, Aave V4, BENQI) |
| GET | `/api/v1/metrics/lending/timeseries` | Daily AVAX or sAVAX lending, or one market (`market=`) |
| GET | `/api/v1/metrics/lending/timeseries/totals` | Daily lending USD totals per protocol |
| GET | `/api/v1/metrics/lending/liquidations` | Daily liquidations by protocol and collateral |
| GET | `/api/v1/metrics/lending/liquidations/recent` | Latest liquidations |
| GET | `/api/v1/metrics/lending/liquidations/top` | Largest liquidations by seized USD |
| GET | `/api/v1/metrics/dex` | C-Chain DEX volume per DEX |
| GET | `/api/v1/metrics/dex/timeseries` | Daily C-Chain DEX volume |
| GET | `/api/v1/metrics/dex/pools` | Top C-Chain DEX pools by volume |
| WS | `/ws/blocks/{chainId}` | Real-time block stream |

---

## Examples

```bash
# Health check
curl https://api.l1beat.io/health

# Indexer status (for monitoring)
curl https://api.l1beat.io/api/v1/metrics/indexer/status

# === EVM Data (C-Chain = 43114) ===

# Get latest blocks
curl "https://api.l1beat.io/api/v1/data/evm/43114/blocks?limit=10"

# Get specific block
curl "https://api.l1beat.io/api/v1/data/evm/43114/blocks/54000000"

# Get latest transactions
curl "https://api.l1beat.io/api/v1/data/evm/43114/txs?limit=10"

# Get transaction by hash (includes internal txs, transfers, approvals)
curl "https://api.l1beat.io/api/v1/data/evm/43114/txs/0x1234..."

# Get address transactions
curl "https://api.l1beat.io/api/v1/data/evm/43114/address/0xabcd.../txs"

# Get address internal transactions
curl "https://api.l1beat.io/api/v1/data/evm/43114/address/0xabcd.../internal-txs"

# Get address ERC-20 balances
curl "https://api.l1beat.io/api/v1/data/evm/43114/address/0xabcd.../balances"

# Get address native balance
curl "https://api.l1beat.io/api/v1/data/evm/43114/address/0xabcd.../native"

# === EVM Metrics ===

# Get chain stats
curl "https://api.l1beat.io/api/v1/metrics/evm/43114/stats"

# List available metrics
curl "https://api.l1beat.io/api/v1/metrics/evm/43114/timeseries"

# Get daily transaction count (last 30 days)
curl "https://api.l1beat.io/api/v1/metrics/evm/43114/timeseries/tx_count?granularity=day&limit=30"

# Get hourly active addresses with time range
curl "https://api.l1beat.io/api/v1/metrics/evm/43114/timeseries/active_addresses?granularity=hour&from=2025-01-01&to=2025-01-07"

# === P-Chain Data ===

# List P-Chain transactions
curl "https://api.l1beat.io/api/v1/data/pchain/txs?limit=20"

# Filter by type
curl "https://api.l1beat.io/api/v1/data/pchain/txs?tx_type=RegisterL1Validator"

# Get transaction by ID
curl "https://api.l1beat.io/api/v1/data/pchain/txs/22FdhKfCTTW...xxNeV"

# Get transaction type counts
curl "https://api.l1beat.io/api/v1/data/pchain/tx-types"

# === Chains & Subnets ===

# List all chains (unified endpoint with registry + validator data)
curl "https://api.l1beat.io/api/v1/data/chains"

# List L1 chains only
curl "https://api.l1beat.io/api/v1/data/chains?chain_type=l1"

# List chains with active validators
curl "https://api.l1beat.io/api/v1/data/chains?active=true"

# Filter by category
curl "https://api.l1beat.io/api/v1/data/chains?category=DeFi"

# Get subnet details
curl "https://api.l1beat.io/api/v1/data/subnets/2ABC...xyz"

# === Validators ===

# List active validators
curl "https://api.l1beat.io/api/v1/data/validators?active=true"

# Filter by subnet
curl "https://api.l1beat.io/api/v1/data/validators?subnet_id=2ABC...xyz"

# Get validator details (by node ID or validation ID)
curl "https://api.l1beat.io/api/v1/data/validators/NodeID-ABC123..."

# Get validator deposits
curl "https://api.l1beat.io/api/v1/data/validators/NodeID-ABC123.../deposits"

# === Fee Metrics ===

# Get fee stats for all L1s (includes summary totals)
curl "https://api.l1beat.io/api/v1/metrics/fees"

# Get fee stats for specific L1
curl "https://api.l1beat.io/api/v1/metrics/fees?subnet_id=2ABC...xyz"

# Get daily fee burn for a subnet
curl "https://api.l1beat.io/api/v1/metrics/fees/daily?subnet_id=2ABC...xyz"

# Network-wide daily fee burn across all L1s
curl "https://api.l1beat.io/api/v1/metrics/fees/daily?days=30"

# Get daily fee burn with per-validator breakdown
curl "https://api.l1beat.io/api/v1/metrics/fees/daily?subnet_id=2ABC...xyz&validators=true&days=30"

# === WebSocket ===

# Connect to block stream (use wscat or browser)
wscat -c "wss://api.l1beat.io/ws/blocks/43114"
```
