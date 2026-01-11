# Icicle API Documentation

Base URL: `http://localhost:8080`

## Common Response Format

**Success:**
```json
{
  "data": { ... },
  "meta": {
    "limit": 20,
    "offset": 0
  }
}
```

**Error:**
```json
{
  "error": "error message"
}
```

## Common Query Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | int | 20 | Number of results (max 100) |
| `offset` | int | 0 | Pagination offset |

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

### GET /indexer/status

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
curl -s http://your-server:8080/indexer/status | jq '.healthy'

# Get blocks behind for C-Chain
curl -s http://your-server:8080/indexer/status | jq '.evm[] | select(.chain_id == 43114) | .blocks_behind'
```

---

## EVM Chain Data

All EVM endpoints are prefixed with `/evm/{chainId}/...`

Common chain IDs:
- `43114` - Avalanche C-Chain

### GET /evm/{chainId}/blocks

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
    "offset": 0
  }
}
```

---

### GET /evm/{chainId}/blocks/{number}

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

### GET /evm/{chainId}/txs

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
    "offset": 0
  }
}
```

**Notes:**
- `to` is `null` for contract creation transactions
- `value` is in wei (string to preserve precision)
- `type`: 0=legacy, 1=EIP-2930, 2=EIP-1559, 3=EIP-4844

---

### GET /evm/{chainId}/txs/{hash}

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

### GET /evm/{chainId}/address/{address}/txs

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
    "offset": 0
  }
}
```

---

### GET /evm/{chainId}/stats

Get statistics for a specific chain.

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

## P-Chain Transactions

### GET /pchain/txs

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
    "offset": 0
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

### GET /pchain/txs/{txId}

Get a specific P-Chain transaction by ID.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `txId` | string | Transaction ID (CB58) |

**Response:**
```json
{
  "data": {
    "tx_id": "22FdhKfCTTW...xxNeV",
    "tx_type": "RegisterL1ValidatorTx",
    "block_number": 12345678,
    "block_time": "2024-12-01T00:00:00Z",
    "tx_data": { ... }
  }
}
```

---

### GET /pchain/tx-types

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

## Subnets

### GET /subnets

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
    "offset": 0
  }
}
```

**Subnet Types:**
- `regular` - Standard subnet
- `elastic` - Elastic subnet
- `l1` - Avalanche L1

---

### GET /subnets/{subnetId}

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

## L1s

### GET /l1s

List all Avalanche L1s with registry metadata.

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
      "subnet_id": "2ABC...xyz",
      "created_block": 10000000,
      "created_time": "2024-01-01T00:00:00Z",
      "chain_id": "2DEF...uvw",
      "converted_block": 12000000,
      "converted_time": "2024-06-01T00:00:00Z",
      "name": "My L1",
      "description": "A description",
      "logo_url": "https://example.com/logo.png",
      "website_url": "https://example.com"
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0
  }
}
```

---

## Chains

### GET /chains

List all blockchains created within subnets.

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
      "chain_id": "2DEF...uvw",
      "subnet_id": "2ABC...xyz",
      "chain_name": "My Chain",
      "vm_id": "srEXiWaH...",
      "created_block": 10000001,
      "created_time": "2024-01-01T00:01:00Z"
    }
  ],
  "meta": {
    "limit": 20,
    "offset": 0
  }
}
```

---

## Validators

### GET /validators

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
    "offset": 0
  }
}
```

**Notes:**
- All balance/fee values in nAVAX (1 AVAX = 1,000,000,000 nAVAX)

---

### GET /validators/{id}

Get validator by validation ID or node ID.

**Path Parameters:**
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Validation ID or Node ID |

---

### GET /validators/{id}/deposits

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
    "offset": 0
  }
}
```

---

## Metrics

### GET /metrics/fees

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
    "offset": 0
  }
}
```

---

## Examples

```bash
# Health check
curl http://localhost:8080/health

# === EVM Data (C-Chain = 43114) ===

# Get latest blocks
curl "http://localhost:8080/evm/43114/blocks?limit=10"

# Get specific block
curl "http://localhost:8080/evm/43114/blocks/54000000"

# Get latest transactions
curl "http://localhost:8080/evm/43114/txs?limit=10"

# Get transaction by hash
curl "http://localhost:8080/evm/43114/txs/0x1234..."

# Get address transactions
curl "http://localhost:8080/evm/43114/address/0xabcd.../txs"

# Get chain stats
curl "http://localhost:8080/evm/43114/stats"

# === P-Chain Data ===

# List P-Chain transactions
curl "http://localhost:8080/pchain/txs?limit=20"

# Filter by type
curl "http://localhost:8080/pchain/txs?tx_type=RegisterL1ValidatorTx"

# Get transaction by ID
curl "http://localhost:8080/pchain/txs/22FdhKfCTTW...xxNeV"

# Get transaction type counts
curl "http://localhost:8080/pchain/tx-types"

# === Subnets & L1s ===

# List all subnets
curl "http://localhost:8080/subnets"

# List L1 subnets only
curl "http://localhost:8080/subnets?type=l1"

# Get subnet details
curl "http://localhost:8080/subnets/2ABC...xyz"

# List all L1s with metadata
curl "http://localhost:8080/l1s"

# List all chains
curl "http://localhost:8080/chains"

# === Validators ===

# List active validators
curl "http://localhost:8080/validators?active=true"

# Filter by subnet
curl "http://localhost:8080/validators?subnet_id=2ABC...xyz"

# Get validator details
curl "http://localhost:8080/validators/NodeID-ABC123..."

# Get validator deposits
curl "http://localhost:8080/validators/NodeID-ABC123.../deposits"

# === Metrics ===

# Get fee stats for all L1s
curl "http://localhost:8080/metrics/fees"

# Get fee stats for specific L1
curl "http://localhost:8080/metrics/fees?subnet_id=2ABC...xyz"
```
