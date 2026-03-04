# Icicle API Documentation

Base URL: `http://localhost:8080`

**Interactive Documentation**: Open `/api/docs/` in browser for Swagger UI

## API Structure

- **Data API** (`/api/v1/data/*`): Blocks, transactions, subnets, validators, P-chain data
- **Metrics API** (`/api/v1/metrics/*`): Fee statistics, chain metrics, time series data
- **WebSocket** (`/ws/*`): Real-time block streaming
- **System** (`/health`): Health check (no versioning)

## Common Response Format

**Success:**
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

- `has_more` — always present, indicates whether additional results exist beyond this page
- `next_cursor` — present when `has_more` is true on cursor-eligible endpoints; pass as `?cursor=` for the next page
- `total` — only present when `?count=true` is passed; the total number of matching records

**Error:**
```json
{
  "error": {
    "code": "INVALID_PARAMETER",
    "message": "Description of the error"
  }
}
```

**Error Codes:**
- `INVALID_PARAMETER` - Invalid request parameter
- `NOT_FOUND` - Resource not found
- `INTERNAL_ERROR` - Server error
- `RATE_LIMITED` - Too many requests

## Rate Limiting

All endpoints are rate limited to 100 requests/second per IP (burst of 100) by default. Configurable via `--rate-limit` and `--burst` CLI flags.

When rate limited, you'll receive:
- HTTP 429 status
- `Retry-After` header with seconds to wait

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

**Cursor-eligible endpoints:** blocks, transactions, address transactions, address internal transactions, P-Chain transactions, subnets, L1s, chains, validator deposits.

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
- `healthy` - `false` if any chain is >100 blocks behind
- `is_synced` - `true` if chain is <10 blocks behind
- `blocks_behind` - Number of blocks behind the chain tip

**Use for Telegram alerts:**
```bash
# Check if healthy
curl -s http://your-server:8080/api/v1/metrics/indexer/status | jq '.healthy'

# Get blocks behind for C-Chain
curl -s http://your-server:8080/api/v1/metrics/indexer/status | jq '.evm[] | select(.chain_id == 43114) | .blocks_behind'
```

---

## Data API - EVM

All EVM data endpoints are prefixed with `/api/v1/data/evm/{chainId}/...`

Common chain IDs:
- `43114` - Avalanche C-Chain

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
    "has_more": true
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
    "has_more": true
  }
}
```

**Notes:**
- `to` is `null` for contract creation transactions
- `value` is in wei (string to preserve precision)
- `type`: 0=legacy, 1=EIP-2930, 2=EIP-1559, 3=EIP-4844

---

### GET /api/v1/data/evm/{chainId}/txs/{hash}

Get a specific transaction by hash.

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
    "type": 2
  }
}
```

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

**Response:**
```json
{
  "data": [
    {
      "tx_hash": "0x1234...abcd",
      "block_number": 54000000,
      "block_time": "2025-01-08T12:00:00Z",
      "trace_address": "0,1",
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
- `trace_address` shows the path in the call tree (e.g., "0,1" = first call's second subcall)

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

**Response:**
```json
{
  "data": [
    {
      "tx_id": "22FdhKfCTTW...xxNeV",
      "tx_type": "RegisterL1ValidatorTx",
      "block_number": 12345678,
      "block_time": "2024-12-01T00:00:00Z",
      "tx_data": {
        "Balance": 2000000000000,
        "Signer": { ... },
        "ValidationID": "3DEF...uvw"
      }
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  }
}
```

**Common Transaction Types:**
- `CreateSubnetTx` - Create a new subnet
- `CreateChainTx` - Create a blockchain in a subnet
- `AddValidatorTx` - Add validator to primary network
- `AddDelegatorTx` - Add delegator to primary network
- `ConvertSubnetToL1Tx` - Convert subnet to L1
- `RegisterL1ValidatorTx` - Register L1 validator
- `IncreaseL1ValidatorBalanceTx` - Top up validator balance
- `DisableL1ValidatorTx` - Disable L1 validator
- `SetL1ValidatorWeightTx` - Change validator weight

---

### GET /api/v1/data/pchain/txs/{txId}

Get a specific P-Chain transaction by ID.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `txId` | string | Transaction ID (CB58) |

---

### GET /api/v1/data/pchain/tx-types

Get all P-Chain transaction types with counts.

**Response:**
```json
{
  "data": [
    { "tx_type": "AddDelegatorTx", "count": 150000 },
    { "tx_type": "AddValidatorTx", "count": 50000 },
    { "tx_type": "RegisterL1ValidatorTx", "count": 1200 }
  ]
}
```

---

## Data API - Subnets

### GET /api/v1/data/subnets

List all subnets.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Filter by type: "regular", "elastic", or "l1" |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |

**Response:**
```json
{
  "data": [
    {
      "subnet_id": "2ABC...xyz",
      "subnet_type": "l1",
      "created_block": 10000000,
      "created_time": "2024-01-01T00:00:00Z",
      "chain_id": "2DEF...uvw",
      "converted_block": 12000000,
      "converted_time": "2024-06-01T00:00:00Z"
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0,
    "has_more": true
  }
}
```

**Subnet Types:**
- `regular` - Standard subnet
- `elastic` - Elastic subnet
- `l1` - Avalanche L1

---

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

---

## Data API - L1s

### GET /api/v1/data/l1s

List all Avalanche L1s with registry metadata.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |

---

## Data API - Chains

### GET /api/v1/data/chains

List all blockchains created within subnets.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Filter by subnet ID |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |

---

## Data API - Validators

### GET /api/v1/data/validators

List L1 validators.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Filter by subnet ID (CB58) |
| `active` | string | Set to "true" for active only |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |

**Response:**
```json
{
  "data": [
    {
      "subnet_id": "2ABC...xyz",
      "validation_id": "3DEF...uvw",
      "node_id": "NodeID-ABC123...",
      "balance": 1000000000000,
      "weight": 100000,
      "start_time": "2024-12-01T00:00:00Z",
      "end_time": "2025-12-01T00:00:00Z",
      "uptime_percentage": 99.5,
      "active": true,
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

---

### GET /api/v1/data/validators/{id}

Get validator by validation ID or node ID.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Validation ID or Node ID |

---

### GET /api/v1/data/validators/{id}/deposits

Get deposit history for a validator.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Validation ID or Node ID |

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

---

## Metrics API - Fees

### GET /api/v1/metrics/fees

Get L1 validation fee statistics.

**Query Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `subnet_id` | string | Filter by subnet ID |
| `limit` | int | Results per page |
| `offset` | int | Pagination offset |

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
  }
}
```

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
| `metric` | string | Metric name |

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `granularity` | string | day | Time granularity: hour, day, week, month |
| `from` | string | - | Start time (date, RFC3339, or unix timestamp) |
| `to` | string | - | End time (date, RFC3339, or unix timestamp) |
| `limit` | int | 100 | Number of data points (max 1000) |

**Available Metrics:**
- `tx_count` - Transaction count
- `active_addresses` - Unique active addresses
- `active_senders` - Unique senders
- `fees_paid` - Total fees in wei
- `gas_used` - Total gas used
- `contracts` - New contracts deployed
- `deployers` - Unique contract deployers
- `avg_tps` - Average transactions per second
- `max_tps` - Maximum transactions per second
- `avg_gps` - Average gas per second
- `max_gps` - Maximum gas per second
- `avg_gas_price` - Average gas price
- `max_gas_price` - Maximum gas price
- `icm_total` - Total ICM messages
- `icm_sent` - ICM messages sent
- `icm_received` - ICM messages received
- `usdc_volume` - USDC transfer volume
- `cumulative_tx_count` - Cumulative transaction count
- `cumulative_addresses` - Cumulative unique addresses
- `cumulative_contracts` - Cumulative contracts deployed
- `cumulative_deployers` - Cumulative unique deployers

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

1. **initial** - Sent on connection with last 10 blocks:
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

2. **new_block** - Sent when a new block is indexed:
```json
{
  "type": "new_block",
  "data": {
    "chain_id": 43114,
    "block_number": 54000001,
    "hash": "0x5678...efgh",
    ...
  }
}
```

3. **ping** - Keepalive sent every 30 seconds:
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

## Examples

```bash
# Health check
curl http://localhost:8080/health

# Indexer status (for monitoring)
curl http://localhost:8080/api/v1/metrics/indexer/status

# === EVM Data (C-Chain = 43114) ===

# Get latest blocks
curl "http://localhost:8080/api/v1/data/evm/43114/blocks?limit=10"

# Get specific block
curl "http://localhost:8080/api/v1/data/evm/43114/blocks/54000000"

# Get latest transactions
curl "http://localhost:8080/api/v1/data/evm/43114/txs?limit=10"

# Get transaction by hash
curl "http://localhost:8080/api/v1/data/evm/43114/txs/0x1234..."

# Get address transactions
curl "http://localhost:8080/api/v1/data/evm/43114/address/0xabcd.../txs"

# Get address internal transactions
curl "http://localhost:8080/api/v1/data/evm/43114/address/0xabcd.../internal-txs"

# Get address ERC-20 balances
curl "http://localhost:8080/api/v1/data/evm/43114/address/0xabcd.../balances"

# Get address native balance
curl "http://localhost:8080/api/v1/data/evm/43114/address/0xabcd.../native"

# === EVM Metrics ===

# Get chain stats
curl "http://localhost:8080/api/v1/metrics/evm/43114/stats"

# List available metrics
curl "http://localhost:8080/api/v1/metrics/evm/43114/timeseries"

# Get daily transaction count (last 30 days)
curl "http://localhost:8080/api/v1/metrics/evm/43114/timeseries/tx_count?granularity=day&limit=30"

# Get hourly active addresses with time range
curl "http://localhost:8080/api/v1/metrics/evm/43114/timeseries/active_addresses?granularity=hour&from=2025-01-01&to=2025-01-07"

# === P-Chain Data ===

# List P-Chain transactions
curl "http://localhost:8080/api/v1/data/pchain/txs?limit=20"

# Filter by type
curl "http://localhost:8080/api/v1/data/pchain/txs?tx_type=RegisterL1ValidatorTx"

# Get transaction by ID
curl "http://localhost:8080/api/v1/data/pchain/txs/22FdhKfCTTW...xxNeV"

# Get transaction type counts
curl "http://localhost:8080/api/v1/data/pchain/tx-types"

# === Subnets & L1s ===

# List all subnets
curl "http://localhost:8080/api/v1/data/subnets"

# List L1 subnets only
curl "http://localhost:8080/api/v1/data/subnets?type=l1"

# Get subnet details
curl "http://localhost:8080/api/v1/data/subnets/2ABC...xyz"

# List all L1s with metadata
curl "http://localhost:8080/api/v1/data/l1s"

# List all chains
curl "http://localhost:8080/api/v1/data/chains"

# === Validators ===

# List active validators
curl "http://localhost:8080/api/v1/data/validators?active=true"

# Filter by subnet
curl "http://localhost:8080/api/v1/data/validators?subnet_id=2ABC...xyz"

# Get validator details
curl "http://localhost:8080/api/v1/data/validators/NodeID-ABC123..."

# Get validator deposits
curl "http://localhost:8080/api/v1/data/validators/NodeID-ABC123.../deposits"

# === Fee Metrics ===

# Get fee stats for all L1s
curl "http://localhost:8080/api/v1/metrics/fees"

# Get fee stats for specific L1
curl "http://localhost:8080/api/v1/metrics/fees?subnet_id=2ABC...xyz"

# === WebSocket ===

# Connect to block stream (use wscat or browser)
wscat -c "ws://localhost:8080/ws/blocks/43114"
```
