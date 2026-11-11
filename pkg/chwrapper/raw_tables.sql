-- Blocks table - main block headers
CREATE TABLE IF NOT EXISTS raw_blocks (
    chain_id UInt32,  -- Multiple chains in same tables
    block_number UInt32,
    hash FixedString(32),  -- 32 bytes
    parent_hash FixedString(32),
    block_time DateTime64(3, 'UTC'),  -- Millisecond precision, UTC timezone
    miner FixedString(20),  -- 20 bytes address
    difficulty UInt8,  -- Always 1 on PoS chains
    total_difficulty UInt64,  -- On PoS chains, equals block number, but store for compatibility
    size UInt32,
    gas_limit UInt32,
    gas_used UInt32,
    base_fee_per_gas UInt64,
    block_gas_cost UInt64,
    state_root FixedString(32),
    transactions_root FixedString(32),
    receipts_root FixedString(32),
    extra_data String,
    block_extra_data String,
    ext_data_hash FixedString(32),
    ext_data_gas_used UInt32,
    mix_hash FixedString(32),
    nonce LowCardinality(FixedString(8)),  -- 8 bytes, always 0x00...00 on PoS
    sha3_uncles FixedString(32),
    uncles Array(FixedString(32)),
    blob_gas_used UInt32,  -- Always 0 if no blob txs
    excess_blob_gas UInt64,  -- Always 0 if no blob txs
    parent_beacon_block_root LowCardinality(FixedString(32)),  -- Often all zeros
    min_delay_excess UInt64,
    tx_count UInt32  -- Denormalized transaction count for fast list queries
) ENGINE = MergeTree()
ORDER BY (chain_id, block_number);

-- Transactions table - merged with receipts for analytics performance
CREATE TABLE IF NOT EXISTS raw_txs (
    chain_id UInt32,  -- Multiple chains in same tables
    hash FixedString(32),
    block_number UInt32,
    block_hash FixedString(32),
    block_time DateTime64(3, 'UTC'),  -- Millisecond precision, UTC timezone
    transaction_index UInt16,
    nonce UInt64,
    from FixedString(20),
    to Nullable(FixedString(20)),  -- NULL for contract creation
    value UInt256,
    gas_limit UInt32,  -- Renamed from 'gas' for clarity
    gas_price UInt64,
    gas_used UInt32,  -- From receipt
    success Bool,  -- From receipt status
    input String CODEC(ZSTD(3)),  -- Calldata
    type UInt8,  -- 0,1,2,3 (legacy, EIP-2930, EIP-1559, EIP-4844)
    max_fee_per_gas Nullable(UInt64),  -- Only for EIP-1559
    max_priority_fee_per_gas Nullable(UInt64),  -- Only for EIP-1559
    priority_fee_per_gas Nullable(UInt64),  -- Computed: min(gas_price - base_fee, max_priority_fee)
    base_fee_per_gas UInt64,  -- Denormalized from blocks for easier queries
    contract_address Nullable(FixedString(20)),  -- From receipt if contract creation
    access_list Array(Tuple(
        address FixedString(20),
        storage_keys Array(FixedString(32))
    )),  -- Properly structured, not JSON
    INDEX idx_hash hash TYPE bloom_filter GRANULARITY 1,  -- Fast tx lookup by hash
    INDEX idx_from `from` TYPE bloom_filter GRANULARITY 1,  -- Fast address lookup
    INDEX idx_to `to` TYPE bloom_filter GRANULARITY 1  -- Fast address lookup
) ENGINE = MergeTree()
ORDER BY (chain_id, block_number);

-- Traces table - flattened trace calls
CREATE TABLE IF NOT EXISTS raw_traces (
    chain_id UInt32,  -- Multiple chains in same tables
    tx_hash FixedString(32),
    block_number UInt32,
    block_time DateTime64(3, 'UTC'),  -- Millisecond precision, UTC timezone
    transaction_index UInt16,
    trace_address Array(UInt16),  -- Path in call tree, e.g. [0,2,1] = first call -> third subcall -> second subcall
    from FixedString(20),
    to Nullable(FixedString(20)),  -- NULL for certain call types
    gas UInt32,
    gas_used UInt32,
    value UInt256,
    input String CODEC(ZSTD(3)),
    output String,
    call_type LowCardinality(String),  -- CALL, DELEGATECALL, STATICCALL, CREATE, CREATE2, etc.
    tx_success Bool,  -- Transaction success status (denormalized from raw_txs)
    tx_from FixedString(20),  -- Original transaction sender (denormalized)
    tx_to Nullable(FixedString(20)),  -- Original transaction target (denormalized)
    INDEX idx_tx_hash tx_hash TYPE bloom_filter GRANULARITY 1,  -- Fast trace lookup by tx hash
    INDEX idx_from `from` TYPE bloom_filter GRANULARITY 1,  -- Fast address lookup
    INDEX idx_to `to` TYPE bloom_filter GRANULARITY 1  -- Fast address lookup
) ENGINE = MergeTree()
ORDER BY (chain_id, block_number);

-- reverted: this frame or an ancestor frame failed, so its effects were rolled back
-- even when tx_success is true. Rows ingested before this column existed read false.
ALTER TABLE raw_traces ADD COLUMN IF NOT EXISTS reverted Bool DEFAULT false;

-- Logs table - event logs emitted by smart contracts
CREATE TABLE IF NOT EXISTS raw_logs (
    chain_id UInt32,  -- Multiple chains in same tables
    address FixedString(20),
    block_number UInt32,
    block_hash FixedString(32),  -- Needed for reorg detection and data integrity
    block_time DateTime64(3, 'UTC'),  -- Millisecond precision, UTC timezone
    transaction_hash FixedString(32),
    transaction_index UInt16,
    log_index UInt32,
    tx_from FixedString(20),  -- Denormalized from transactions for faster queries
    tx_to Nullable(FixedString(20)),  -- Denormalized from transactions
    topic0 FixedString(32),  -- Event signature hash (empty for rare anonymous events)
    topic1 Nullable(FixedString(32)),
    topic2 Nullable(FixedString(32)),
    topic3 Nullable(FixedString(32)),
    data String CODEC(ZSTD(3)),  -- Non-indexed event data
    removed Bool,  -- TODO: check if ever happen to be true
    INDEX idx_block_number block_number TYPE minmax GRANULARITY 1,  -- Speeds up block range queries
    INDEX idx_transaction_hash transaction_hash TYPE bloom_filter GRANULARITY 1  -- Fast log lookup by tx hash
) ENGINE = MergeTree()
ORDER BY (chain_id, block_time, address, topic0);

-- Watermark table - tracks guaranteed sync progress per chain
CREATE TABLE IF NOT EXISTS sync_watermark (
    chain_id UInt32,
    block_number UInt32
) ENGINE = EmbeddedRocksDB
PRIMARY KEY chain_id;

-- Chain status table - tracks chain metadata and RPC connectivity
CREATE TABLE IF NOT EXISTS chain_status (
    chain_id UInt32,
    name String,
    last_updated DateTime64(3, 'UTC'),
    last_block_on_chain UInt64
) ENGINE = ReplacingMergeTree(last_updated)
PRIMARY KEY chain_id;

-- P-chain transactions table - simplified schema using ClickHouse JSON type
CREATE TABLE IF NOT EXISTS p_chain_txs (
    -- Core indexed columns for efficient queries
    tx_id String,  -- CB58-encoded transaction ID (e.g., "22FdhKfCTTWTfgBWiibGo8x2pEaCeWLdwHwwDCvK9M7eyxxNeV")
    tx_type LowCardinality(String),
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    p_chain_id UInt32,  -- Identifies which P-chain instance (e.g., mainnet vs testnet)
    
    -- Main JSON column storing the complete transaction data
    -- Type hints optimize storage and query performance for frequently accessed fields
    tx_data JSON(
        max_dynamic_paths=512,
        max_dynamic_types=32,
        
        -- Common validator/subnet fields with type hints
        -- Validator.NodeID String,
        -- Validator.Start UInt64,
        -- Validator.End UInt64,
        -- Validator.Wght UInt64,
        -- Subnet String,
        -- SubnetID String,
        -- ChainID String,
        
        -- L1 validator fields
        -- ValidationID String,
        -- Balance UInt64,
        
        -- SubnetValidator fields
        -- SubnetValidator.Subnet String,
        -- SubnetValidator.NodeID String,
        
        -- Other commonly queried fields
        -- TxID String,
        -- AssetID String,
        -- Owner String,
        -- Address String,
        -- ChainName String,
        -- VMID String,
        -- SourceChain String,
        -- DestinationChain String
    )
) ENGINE = ReplacingMergeTree(block_time)
ORDER BY (p_chain_id, tx_id);
-- Note: Using ReplacingMergeTree to deduplicate transactions that may be inserted multiple times
-- during syncer restarts. ORDER BY tx_id ensures uniqueness per transaction.
-- IMPORTANT: For existing tables, use FINAL or DISTINCT in queries to get deduplicated results.
-- Migration note: If migrating from MergeTree, recreate table and re-sync data.

-- L1 Validator State table - tracks current state of L1 validators
CREATE TABLE IF NOT EXISTS l1_validator_state (
    -- Identifiers
    subnet_id String,  -- The L1 subnet ID (CB58)
    validation_id String,  -- Unique validator ID for L1 validators (CB58)
    node_id String,  -- Node ID in format "NodeID-xxx"

    -- Validator state
    balance UInt64,  -- Remaining balance for this validator
    weight UInt64,  -- Current validator weight/stake
    start_time DateTime64(3, 'UTC'),  -- When validator started
    end_time DateTime64(3, 'UTC'),  -- When validator ends
    uptime_percentage Float64,  -- Uptime percentage (0-100)

    -- Status
    active Bool,  -- Whether validator is currently active

    -- Fee tracking (computed from balance transactions)
    initial_deposit UInt64 DEFAULT 0,  -- Initial balance at creation (in nAVAX)
    total_topups UInt64 DEFAULT 0,  -- Sum of all top-up transactions (in nAVAX)
    refund_amount UInt64 DEFAULT 0,  -- Refund when disabled (in nAVAX)
    fees_paid UInt64 DEFAULT 0,  -- Total fees consumed (deposited - refunded - balance)

    -- Delegation (Primary Network only, from getCurrentValidators, 0 elsewhere)
    delegator_count UInt32 DEFAULT 0,  -- Active delegations to this validator
    delegated_stake UInt64 DEFAULT 0,  -- Sum of active delegated stake (nAVAX)
    delegation_fee_percent Float64 DEFAULT 0,  -- Fee charged to delegators, percent (2 = 2%)

    -- Metadata
    last_updated DateTime64(3, 'UTC'),  -- When this state was last updated
    p_chain_id UInt32  -- Which P-chain instance (mainnet vs testnet)
) ENGINE = ReplacingMergeTree(last_updated)
ORDER BY (p_chain_id, subnet_id, validation_id);

-- Migrate existing l1_validator_state deployments to add delegation fields (idempotent, no-op on fresh installs)
ALTER TABLE l1_validator_state ADD COLUMN IF NOT EXISTS delegator_count UInt32 DEFAULT 0 AFTER fees_paid;
ALTER TABLE l1_validator_state ADD COLUMN IF NOT EXISTS delegated_stake UInt64 DEFAULT 0 AFTER delegator_count;
ALTER TABLE l1_validator_state ADD COLUMN IF NOT EXISTS delegation_fee_percent Float64 DEFAULT 0 AFTER delegated_stake;

-- L1 Subnets table - tracks which subnets are L1 and should be monitored
CREATE TABLE IF NOT EXISTS l1_subnets (
    subnet_id String,  -- The L1 subnet ID (CB58)
    chain_id String,  -- The associated chain ID (CB58)
    conversion_block UInt64,  -- Block number when subnet was converted to L1
    conversion_time DateTime64(3, 'UTC'),  -- When subnet was converted to L1
    validator_manager_address String DEFAULT '',  -- EVM address of the ValidatorManager contract (0x-prefixed hex)
    p_chain_id UInt32,  -- Which P-chain instance
    last_synced DateTime64(3, 'UTC')  -- Last time validators were synced for this subnet
) ENGINE = ReplacingMergeTree(last_synced)
PRIMARY KEY (p_chain_id, subnet_id);

-- L1 Registry table - metadata from external registry (l1beat-l1-registry npm package)
-- Keyed by chain_id (blockchainId from registry) so each chain gets its own metadata
CREATE TABLE IF NOT EXISTS l1_registry (
    blockchain_id String,                    -- blockchain ID (CB58) from registry chains[].blockchainId
    subnet_id String,
    name String,
    description String,
    logo_url String,
    website_url String,

    -- Extended fields from l1beat-l1-registry
    network String DEFAULT 'mainnet',       -- "mainnet" or "fuji"
    is_l1 Bool DEFAULT true,                -- L1 vs legacy subnet
    categories Array(String),               -- e.g. ["DeFi", "Gaming"]
    socials String DEFAULT '[]',            -- JSON array of {name, url}

    -- Per-chain fields
    evm_chain_id UInt64 DEFAULT 0,          -- EVM chain ID (e.g. 43114)
    rpc_url String DEFAULT '',              -- Primary RPC URL
    explorer_url String DEFAULT '',         -- Block explorer URL
    sybil_resistance_type String DEFAULT '',-- "Proof of Stake" or "Proof of Authority"

    -- Native token
    network_token_name String DEFAULT '',
    network_token_symbol String DEFAULT '',
    network_token_decimals UInt8 DEFAULT 18,
    network_token_logo_uri String DEFAULT '',

    -- Staking (PoS only): converts P-Chain validator weight to a whole-token
    -- staked amount as staked = weight / staking_weight_factor. Curated in the
    -- registry because the on-chain weightToValueFactor doesn't reconcile with
    -- token decimals for several L1s. 0 = not staked / unset (PoA). The staked
    -- token may differ from the gas token, so its symbol is stored explicitly.
    staking_weight_factor UInt64 DEFAULT 0,
    staking_token_symbol String DEFAULT '',

    last_updated DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(last_updated)
PRIMARY KEY blockchain_id;

-- Migrate existing l1_registry deployments to add staking fields (idempotent, no-op on fresh installs)
ALTER TABLE l1_registry ADD COLUMN IF NOT EXISTS staking_weight_factor UInt64 DEFAULT 0;
ALTER TABLE l1_registry ADD COLUMN IF NOT EXISTS staking_token_symbol String DEFAULT '';

-- Unified Subnets table - tracks all subnets with their lifecycle status
CREATE TABLE IF NOT EXISTS subnets (
    subnet_id String,  -- The subnet ID (CB58)

    -- Creation info (from CreateSubnetTx)
    created_block UInt64,  -- Block number when subnet was created
    created_time DateTime64(3, 'UTC'),  -- When subnet was created

    -- Subnet type/status
    subnet_type LowCardinality(String),  -- 'legacy', 'primary', 'l1'

    -- L1/Elastic conversion info (nullable, populated when converted)
    chain_id String,  -- Associated chain ID (empty for non-L1 subnets)
    converted_block UInt64,  -- Block number when converted (0 if not converted)
    converted_time DateTime64(3, 'UTC'),  -- When converted (epoch 0 if not converted)
    validator_manager_address String DEFAULT '',  -- EVM address of the ValidatorManager contract (0x-prefixed hex)
    validator_manager_owner String DEFAULT '',  -- Owner of the ValidatorManager contract (0x-prefixed hex)

    -- Metadata
    p_chain_id UInt32,  -- Which P-chain instance
    last_updated DateTime64(3, 'UTC')  -- Last time this record was updated
) ENGINE = ReplacingMergeTree(last_updated)
PRIMARY KEY (p_chain_id, subnet_id);

-- Subnet Chains table - tracks blockchains created within subnets
CREATE TABLE IF NOT EXISTS subnet_chains (
    chain_id String,  -- The blockchain ID (CB58)
    subnet_id String,  -- Parent subnet ID (CB58)
    chain_name String,  -- Chain name
    vm_id String,  -- VM ID (CB58)
    created_block UInt64,  -- Block number when chain was created
    created_time DateTime64(3, 'UTC'),  -- When chain was created
    p_chain_id UInt32,  -- Which P-chain instance
    last_updated DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(last_updated)
PRIMARY KEY (p_chain_id, chain_id);

-- L1 Fee Stats table - tracks total validation fees paid per L1
CREATE TABLE IF NOT EXISTS l1_fee_stats (
    subnet_id String,  -- The L1 subnet ID (CB58)

    -- Deposit tracking (all values in nAVAX)
    total_deposited UInt64,  -- Total AVAX deposited across all validators
    initial_deposits UInt64,  -- From ConvertSubnetToL1Tx + RegisterL1ValidatorTx
    top_up_deposits UInt64,  -- From IncreaseL1ValidatorBalanceTx
    total_refunded UInt64 DEFAULT 0,  -- Total refunds from DisableL1Validator

    -- Current state
    current_balance UInt64,  -- Sum of current remaining balances

    -- Calculated fee (deposited - refunded - current balance)
    total_fees_paid UInt64,  -- Total fees consumed by validators

    -- Counts
    deposit_tx_count UInt32,  -- Number of deposit transactions
    validator_count UInt32,  -- Number of validators (active + inactive)

    -- Metadata
    p_chain_id UInt32,
    last_updated DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(last_updated)
PRIMARY KEY (p_chain_id, subnet_id);

-- L1 Validator History table - tracks all L1 validators from creation
CREATE TABLE IF NOT EXISTS l1_validator_history (
    -- Identifiers
    subnet_id String,  -- The L1 subnet ID (CB58)
    node_id String,  -- Node ID (CB58 format, e.g., "NodeID-xxx...")
    validation_id String,  -- Computed validation ID (CB58)

    -- Creation info
    created_tx_id String,  -- Transaction that created this validator
    created_tx_type LowCardinality(String),  -- 'ConvertSubnetToL1' or 'RegisterL1Validator'
    created_block UInt64,  -- Block when validator was created
    created_time DateTime64(3, 'UTC'),  -- When validator was created

    -- Initial values at creation
    initial_balance UInt64,  -- Balance at creation (in nAVAX)
    initial_weight UInt64,  -- Weight at creation

    -- BLS key info (from creation tx)
    bls_public_key String,  -- BLS public key (hex)

    -- Owner info (for refunds when disabled)
    remaining_balance_owner String,  -- P-Chain address to receive remaining balance (CB58)

    -- Metadata
    p_chain_id UInt32,
    last_updated DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(last_updated)
ORDER BY (p_chain_id, subnet_id, node_id, created_block);

-- L1 Validator Balance Transactions table - tracks all balance-affecting transactions
-- Indexed by validation_id and node_id for fast lookups from frontend
CREATE TABLE IF NOT EXISTS l1_validator_balance_txs (
    -- Identifiers
    validation_id String,  -- The validation ID (CB58), may be empty for disabled validators
    tx_id String,  -- Transaction ID
    tx_type LowCardinality(String),  -- 'ConvertSubnetToL1', 'RegisterL1Validator', or 'IncreaseL1ValidatorBalance'

    -- Transaction details
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),
    amount UInt64,  -- Amount added to balance (in nAVAX)

    -- Additional context
    subnet_id String,  -- For filtering by subnet
    node_id String,  -- For correlation with validator

    -- Metadata
    p_chain_id UInt32,
    inserted_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(inserted_at)
ORDER BY (p_chain_id, node_id, tx_id);

-- Stablecoins registry - curated list of stablecoins by chain.
-- Populated via the embedded seed (stablecoins_seed.sql) on startup, and extensible
-- by manual INSERTs as new stablecoins launch. ReplacingMergeTree dedupes by (chain_id, token).
CREATE TABLE IF NOT EXISTS stablecoins (
    chain_id UInt32,
    token FixedString(20),
    symbol String,
    name String,
    decimals UInt8,
    peg LowCardinality(String) DEFAULT 'USD',
    issuer String DEFAULT '',
    bridged Bool DEFAULT false,
    -- doublecounted: reserves are themselves other counted stablecoins (e.g. avUSD,
    -- UTY, reUSD, FRXUSD), so they inflate a naive sum. Excluded from the NET total;
    -- still listed (GROSS). Frontend sources the exclusion set from this flag.
    doublecounted Bool DEFAULT false,
    added_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(added_at)
ORDER BY (chain_id, token);
ALTER TABLE stablecoins ADD COLUMN IF NOT EXISTS doublecounted Bool DEFAULT false;

-- Stablecoin excluded holders - wallets to subtract when computing circulating supply.
-- Used for treasury / issuance / bridge contracts that hold tokens which haven't been
-- distributed to end users (e.g. Tether treasury). Seeded with known addresses on startup;
-- extensible by manual INSERTs.
CREATE TABLE IF NOT EXISTS stablecoin_excluded_holders (
    chain_id UInt32,
    token FixedString(20),
    holder FixedString(20),
    reason String DEFAULT '',
    added_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(added_at)
ORDER BY (chain_id, token, holder);

-- Stablecoin time-series metrics (supply, volume, transfers, holders per token per period)
CREATE TABLE IF NOT EXISTS stablecoin_metrics (
    chain_id UInt32,
    token FixedString(20),
    metric_name LowCardinality(String),
    granularity LowCardinality(String),
    period DateTime64(3, 'UTC'),
    value String,
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (chain_id, token, metric_name, granularity, period)
PARTITION BY (chain_id, toYYYYMM(period));

-- L1 Validator Refunds table - tracks refunds when validators are disabled
CREATE TABLE IF NOT EXISTS l1_validator_refunds (
    -- Identifiers
    tx_id String,  -- DisableL1Validator transaction ID
    validation_id String,  -- The validation ID (CB58)
    subnet_id String,  -- The L1 subnet ID (CB58)

    -- Refund details
    refund_amount UInt64,  -- Actual refund amount (in nAVAX)
    refund_address String,  -- Address that received the refund (remainingBalanceOwner)

    -- Transaction details
    block_number UInt64,
    block_time DateTime64(3, 'UTC'),

    -- Metadata
    p_chain_id UInt32
) ENGINE = ReplacingMergeTree(block_time)
ORDER BY (p_chain_id, validation_id, tx_id);

-- L1 Validator Weight-change txs - maps each SetL1ValidatorWeight tx to the validationID
-- decoded from its signed Warp message (tx_data.message, an L1ValidatorWeightMessage),
-- which isn't a top-level JSON field. Lets these txs be resolved to a subnet (via
-- l1_validator_history) and gives a weight-change timeline per validator.
CREATE TABLE IF NOT EXISTS l1_validator_weight_txs (
    tx_id String,  -- SetL1ValidatorWeight transaction ID
    validation_id String,  -- validationID from the decoded Warp message (CB58)
    nonce UInt64,  -- message nonce (monotonic per validator)
    weight UInt64,  -- new weight (0 = removal)

    block_number UInt64,
    block_time DateTime64(3, 'UTC'),

    p_chain_id UInt32,
    inserted_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(inserted_at)
ORDER BY (p_chain_id, validation_id, tx_id);

-- Chain Risk table - ValidatorManager control & upgradeability, read from on-chain
-- contract state (eth_call / eth_getCode / eth_getStorageAt) by the risk syncer.
-- Keyed by chain_id (the blockchain ID used everywhere in the API). Nullable columns
-- hold "unknown" (we never fabricate); the /risk endpoint maps them to JSON null.
CREATE TABLE IF NOT EXISTS chain_risk (
    chain_id String,  -- Blockchain ID (CB58), same key as subnet_chains.chain_id
    validator_manager_address String,  -- The ValidatorManager contract (0x-prefixed hex)

    -- Manager type & owner
    manager_type LowCardinality(String) DEFAULT 'unknown',  -- PoA | PoS-native | PoS-erc20 | balancer | placeholder | unknown
    manager_location LowCardinality(String) DEFAULT 'unknown',  -- where the manager contract has code: c-chain | self | unknown
    owner_address Nullable(String),  -- ValidatorManager owner() as 0x-prefixed hex, null if unresolved
    owner_kind LowCardinality(String) DEFAULT 'unknown',  -- eoa | multisig | timelock | dao | contract | unknown
    multisig_threshold Nullable(UInt16),  -- Gnosis Safe threshold (null if owner not a multisig)
    multisig_owners Nullable(UInt16),  -- Gnosis Safe owner count

    -- EIP-1967 proxy / upgradeability
    is_proxy Bool DEFAULT false,  -- true if the EIP-1967 implementation slot is non-zero
    proxy_implementation Nullable(String),  -- implementation address from slot 0x360894…bbc
    proxy_admin Nullable(String),  -- admin address from slot 0xb53127…103 (usually a ProxyAdmin)
    proxy_admin_owner Nullable(String),  -- ProxyAdmin.owner() — the real upgrade controller
    upgrade_delay_seconds Nullable(UInt64),  -- timelock delay on upgrades, 0 means instant, null if unknown

    -- Churn limits (read from the ValidatorManager)
    churn_period_seconds Nullable(UInt64),  -- getChurnPeriodSeconds()
    max_churn_percentage Nullable(UInt8),  -- maximumChurnPercentage from getChurnTracker()

    -- Metadata
    last_updated DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(last_updated)
ORDER BY chain_id;

-- Migrate existing chain_risk deployments to add manager_location (idempotent, no-op on fresh installs)
ALTER TABLE chain_risk ADD COLUMN IF NOT EXISTS manager_location LowCardinality(String) DEFAULT 'unknown';
-- Suzaku BalancerValidatorManager detail as JSON (owner and security modules), empty otherwise
ALTER TABLE chain_risk ADD COLUMN IF NOT EXISTS balancer String DEFAULT '';

-- Validator count snapshots - exact active validator counts per subnet at a
-- historical P-Chain height, sampled via platform.getValidatorsAt (the node's
-- authoritative validator diffs, not a reconstruction). Backfilled once via the
-- validator-backfill command and kept current by a daily sample from the
-- validator syncer. ReplacingMergeTree keyed by (subnet, date) so re-running a
-- backfill replaces rather than duplicates - fully idempotent.
CREATE TABLE IF NOT EXISTS validator_count_snapshots (
    snapshot_date Date,  -- The sample date (height resolved as of end of this day UTC)
    subnet_id String,  -- Primary Network = 11111111111111111111111111111111LpoYY, else the L1 subnet ID (CB58)
    p_chain_height UInt64,  -- P-Chain height the sample was taken at
    validator_count UInt32,  -- Exact active validator count at that height
    p_chain_id UInt32,  -- Which P-chain instance (mainnet vs testnet)
    sampled_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(sampled_at)
ORDER BY (p_chain_id, subnet_id, snapshot_date);

-- Staking snapshots - one row per (subnet, day) of active stake, written by
-- every validator sync cycle (the last cycle of a UTC day wins via
-- ReplacingMergeTree), so history starts on deploy day. Primary Network rows
-- carry delegation and AVAX total supply. L1 rows carry own_stake as the raw
-- validator weight sum (delegation and supply stay 0), converted to tokens at
-- read time with the registry staking factor.
CREATE TABLE IF NOT EXISTS staking_snapshots (
    snapshot_date Date,  -- UTC day
    subnet_id String,  -- Primary Network = 11111111111111111111111111111111LpoYY, else the L1 subnet ID (CB58)
    validator_count UInt32,  -- Active validators
    delegation_count UInt32 DEFAULT 0,  -- Active delegations (Primary Network only)
    own_stake UInt64,  -- Sum of active validator weight (nAVAX for the Primary Network)
    delegated_stake UInt64 DEFAULT 0,  -- Sum of active delegated stake, nAVAX (Primary Network only)
    total_supply UInt64 DEFAULT 0,  -- AVAX total supply from platform.getCurrentSupply, nAVAX (Primary Network only)
    p_chain_id UInt32,  -- Which P-chain instance (mainnet vs testnet)
    sampled_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(sampled_at)
ORDER BY (p_chain_id, subnet_id, snapshot_date);

-- Liquid staking snapshots - one row per (protocol, UTC day), rewritten every
-- validator sync cycle (last sample wins). Protocols are listed in
-- pkg/liquidstaking. Token figures come from the C-Chain contract, delegation
-- figures from p_chain_txs rows whose rewards owner is a protocol address.
CREATE TABLE IF NOT EXISTS liquid_staking_snapshots (
    snapshot_date Date,  -- UTC day
    protocol_id LowCardinality(String),  -- e.g. benqi-savax
    pooled_avax UInt256,  -- AVAX backing the token, wei (18 decimals)
    token_supply UInt256,  -- Token totalSupply(), base units
    active_delegated UInt64,  -- Running delegated stake, nAVAX
    active_delegations UInt32,  -- Running delegations
    validator_count UInt32,  -- Distinct nodes among running delegations
    sampled_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(sampled_at)
ORDER BY (protocol_id, snapshot_date);

-- Restaking collateral securing L1 validators, one row per (protocol, L1,
-- vault), refreshed by the indexer every validator sync cycle. Each cycle
-- writes all rows of an L1 with the same sampled_at, so readers keep only an
-- L1's newest sampled_at to drop vaults that left the middleware.
CREATE TABLE IF NOT EXISTS restaking_snapshots (
    protocol_id LowCardinality(String),  -- e.g. suzaku
    subnet_id String,  -- L1 subnet ID (CB58)
    vault String,  -- C-Chain vault address
    balancer String,  -- BalancerValidatorManager, the L1 key in the protocol
    middleware String,  -- Protocol middleware, a security module of the balancer
    module_weight UInt64,  -- Middleware's current weight in the balancer
    operator_count UInt32,  -- Operators with allocated stake in any vault of the L1
    validation_ids Array(String),  -- L1 validations registered by the middleware (CB58)
    collateral String,  -- Vault collateral() token, often a DefaultCollateral wrapper
    token String,  -- Underlying token (wrapper asset(), else collateral)
    token_symbol String,
    token_decimals UInt8,
    deposited UInt256,  -- Vault activeStake(), token base units
    allocated UInt256,  -- Delegator stake summed over operators, token base units
    l1_limit UInt256,  -- Delegator l1Limit for this L1, token base units
    sampled_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(sampled_at)
ORDER BY (protocol_id, subnet_id, vault);

-- C-Chain lending markets (Aave V3, Aave V4, BENQI), read from the contracts by
-- the validator syncer every cycle. The last sample of each UTC day is kept.
CREATE TABLE IF NOT EXISTS lending_market_snapshots (
    snapshot_date Date,  -- UTC day
    protocol_id LowCardinality(String),  -- aave-v3, aave-v4, benqi
    asset String,  -- Underlying token, lowercase. 0x00..00 for native AVAX (BENQI qiAVAX)
    market String,  -- Aave V3 Pool, BENQI qiToken, Aave V4 Hub
    symbol String,
    decimals UInt8,
    supplied UInt256,  -- Token base units
    borrowed UInt256,  -- Token base units
    supply_apr Float64,  -- Simple annual rate as a fraction
    borrow_apr Float64,  -- Variable borrow rate, simple annual fraction
    collateral_enabled Nullable(Bool),  -- NULL where set per spoke (Aave V4)
    ltv_bps Nullable(UInt16),
    liquidation_threshold_bps Nullable(UInt16),
    price_usd Nullable(Float64),  -- Protocol oracle, NULL when the read failed
    supply_cap Nullable(UInt256),  -- Token base units, NULL when uncapped
    borrow_cap Nullable(UInt256),  -- Token base units, NULL when uncapped
    sampled_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(sampled_at)
ORDER BY (protocol_id, asset, snapshot_date);

-- Collateral groups per asset, latest sample: Aave V3 E-Mode categories and
-- Aave V4 spokes (V4 sets collateral per spoke reserve). Read with the market
-- snapshot each cycle.
CREATE TABLE IF NOT EXISTS lending_emode_snapshots (
    protocol_id LowCardinality(String),  -- aave-v3, aave-v4
    group_id String,  -- V3 E-Mode category id, V4 spoke address
    group_label String,
    asset String,  -- Underlying token, lowercase
    ltv_bps UInt16,  -- Effective LTV, 0 when the asset is LTV-zeroed in the group
    configured_ltv_bps UInt16,  -- The group's configured LTV
    liquidation_threshold_bps UInt16,
    liquidation_bonus_bps UInt16,  -- Bonus above 100 percent
    as_collateral Bool,
    borrowable Bool,
    previous_ltv_bps Nullable(UInt16),  -- V4 config before the current one, kept by older positions
    sampled_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(sampled_at)
ORDER BY (protocol_id, group_id, asset);

-- When each lending LTV took its current value: Aave V3 and BENQI markets
-- (group_id empty) and Aave V4 spoke entries (group_id = spoke). Dated by
-- bisecting archive reads, written by the C-Chain snapshot cycle when a value
-- is new or differs from the stored one.
CREATE TABLE IF NOT EXISTS lending_ltv_changes (
    protocol_id LowCardinality(String),  -- aave-v3, aave-v4, benqi
    group_id String,  -- Empty for a market, V4 spoke address
    asset String,  -- Underlying token, lowercase
    ltv_bps UInt16,  -- The value being dated (V4 collateral factor)
    changed_block UInt64,  -- 0 when unchanged since listing
    changed_at Nullable(DateTime64(3, 'UTC')),  -- NULL when unchanged since listing
    changed_tx String,  -- Config transaction, empty when unchanged or not found
    checked_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(checked_at)
ORDER BY (protocol_id, group_id, asset);

-- C-Chain DEX pools, registered from their factory's creation event (pkg/dexvolume).
CREATE TABLE IF NOT EXISTS dex_pools (
    pool FixedString(20),
    dex LowCardinality(String),  -- a dexvolume.Dexes id, e.g. pharaoh-v3, lfj, lfj-v1, pangolin-v2
    factory FixedString(20),  -- Emitter of the creation event
    token0 FixedString(20),  -- Liquidity Book tokenX
    token1 FixedString(20),  -- Liquidity Book tokenY
    created_time DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree
ORDER BY pool;

-- C-Chain DEX swaps per UTC day and pool. Complete days only, rewritten in
-- place when a day is rolled up again.
CREATE TABLE IF NOT EXISTS dex_daily (
    day Date,
    dex LowCardinality(String),
    pool FixedString(20),
    swaps UInt64,
    priced_swaps UInt64,  -- Swaps with a USD stablecoin or WAVAX side
    volume_usd Float64,  -- Priced swaps only, one side each
    flagged_swaps UInt64,  -- Priced swaps above dexvolume.FlagUSD, still in volume_usd
    addresses AggregateFunction(uniqExact, FixedString(20)),  -- tx.from of the swaps
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (day, dex, pool);

-- C-Chain perpetuals (pkg/perps): one row per position change, liquidation or
-- fee on GMX V2 and GMX V1. USD amounts are as the venue reports them.
CREATE TABLE IF NOT EXISTS perp_events (
    venue LowCardinality(String),  -- a perps.Venues id, gmx-v2 or gmx-v1
    block_time DateTime64(3, 'UTC'),
    transaction_hash FixedString(32),
    log_index UInt32,
    kind Enum8('increase' = 1, 'decrease' = 2, 'liquidation' = 3, 'fee' = 4),
    market FixedString(20),  -- GMX V2 market token, GMX V1 index token, zeros for V1 fees
    account FixedString(20),  -- Trader, zeros for V1 fees
    is_long Bool,
    size_usd Float64,  -- Size change, 0 on fee rows
    collateral_usd Float64,  -- Collateral added or removed
    fee_usd Float64,  -- Fee rows only: position, borrowing and liquidation fees
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (block_time, transaction_hash, log_index);

-- GMX V2 OpenInterestUpdated: open interest per market, collateral token and
-- side after the update.
CREATE TABLE IF NOT EXISTS perp_oi_events (
    block_time DateTime64(3, 'UTC'),
    transaction_hash FixedString(32),
    log_index UInt32,
    market FixedString(20),
    collateral_token FixedString(20),
    is_long Bool,
    oi_usd Float64
) ENGINE = ReplacingMergeTree
ORDER BY (block_time, transaction_hash, log_index);

-- GMX V2 markets, from MarketCreated.
CREATE TABLE IF NOT EXISTS perp_markets (
    market FixedString(20),
    index_token FixedString(20),  -- Zeros for swap-only markets
    long_token FixedString(20),
    short_token FixedString(20),
    created_time DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree
ORDER BY market;

-- Open interest at the end of each complete UTC day, carried forward.
CREATE TABLE IF NOT EXISTS perp_oi_daily (
    day Date,
    venue LowCardinality(String),
    market FixedString(20),
    collateral_token FixedString(20),  -- Zeros for GMX V1
    is_long Bool,
    oi_usd Float64,
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (day, venue, market, collateral_token, is_long);

-- C-Chain ICM (Teleporter) messages (pkg/icm), one row per messageID and
-- direction: sent from the C-Chain or received on it. Replaying a window
-- replaces its rows.
CREATE TABLE IF NOT EXISTS icm_messages (
    direction Enum8('sent' = 1, 'received' = 2),
    message_id FixedString(32),
    counterparty FixedString(32),  -- destination blockchain ID for sends, source for receives
    block_time DateTime64(3, 'UTC'),
    block_number UInt32,
    transaction_hash FixedString(32),
    log_index UInt32,
    local_address FixedString(20),  -- C-Chain side: originSenderAddress for sends, destinationAddress for receives
    remote_address FixedString(20),  -- L1 side: destinationAddress for sends, originSenderAddress for receives
    required_gas_limit UInt64,
    fee_token FixedString(20),  -- Relayer fee token, sends only
    fee_amount UInt256,
    deliverer FixedString(20),  -- Relayer that delivered it, receives only
    reward_redeemer FixedString(20),  -- Address that redeems the relayer reward, receives only
    ictt_type Int8 DEFAULT -1,  -- ICTT TransferrerMessageType, -1 when the payload is not ICTT-shaped
    ictt_recipient FixedString(20),  -- ICTT recipient, or recipientContract for calls
    ictt_amount UInt256  -- ICTT amount as encoded in the message
) ENGINE = ReplacingMergeTree()
ORDER BY (direction, message_id);

ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS local_address FixedString(20) AFTER log_index;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS remote_address FixedString(20) AFTER local_address;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS required_gas_limit UInt64 AFTER remote_address;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS fee_token FixedString(20) AFTER required_gas_limit;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS fee_amount UInt256 AFTER fee_token;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS deliverer FixedString(20) AFTER fee_amount;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS reward_redeemer FixedString(20) AFTER deliverer;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS ictt_type Int8 DEFAULT -1 AFTER reward_redeemer;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS ictt_recipient FixedString(20) AFTER ictt_type;
ALTER TABLE icm_messages ADD COLUMN IF NOT EXISTS ictt_amount UInt256 AFTER ictt_recipient;

-- C-Chain contracts behind ICTT-shaped ICM messages (pkg/icm), checked
-- on-chain: a TokenHome or TokenRemote reports the C-Chain as its blockchain
-- ID and names its token or home. kind is empty for other contracts.
CREATE TABLE IF NOT EXISTS icm_apps (
    address FixedString(20),
    kind LowCardinality(String),  -- ictt-home, ictt-remote, or empty
    token FixedString(20),  -- The remote itself, or the home's token address
    symbol String,
    decimals UInt8,
    note String,  -- Why it is (not) ICTT
    checked_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(checked_at)
ORDER BY address;

-- Received ICM messages that ran on the C-Chain (pkg/icm): MessageExecuted,
-- on receipt or on a later retry. A receive without one failed and moved no
-- tokens.
CREATE TABLE IF NOT EXISTS icm_executions (
    message_id FixedString(32),
    source FixedString(32),
    block_time DateTime64(3, 'UTC'),
    transaction_hash FixedString(32)
) ENGINE = ReplacingMergeTree()
ORDER BY message_id;

-- TokenRemotes registered with C-Chain ICTT TokenHomes (pkg/icm), as the home
-- reports them. ICTT messages carry amounts in the remote's denomination: the
-- home amount is the message amount divided by multiplier when
-- multiply_on_remote, else multiplied by it.
CREATE TABLE IF NOT EXISTS icm_remotes (
    home FixedString(20),
    remote_chain FixedString(32),
    remote_address FixedString(20),
    registered Bool,
    multiplier UInt256,
    multiply_on_remote Bool,
    checked_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(checked_at)
ORDER BY (home, remote_chain, remote_address);

-- C-Chain bridge logs (pkg/bridges): the raw_logs rows bridge transfers are
-- decoded from, copied in one scan per window so decoding can be re-run
-- without scanning raw_logs again. Also holds the Chainlink price updates.
CREATE TABLE IF NOT EXISTS bridge_logs (
    block_time DateTime64(3, 'UTC'),
    block_number UInt32,
    transaction_hash FixedString(32),
    log_index UInt32,
    address FixedString(20),
    tx_from FixedString(20),
    topic0 FixedString(32),
    topic1 Nullable(FixedString(32)),
    topic2 Nullable(FixedString(32)),
    topic3 Nullable(FixedString(32)),
    data String CODEC(ZSTD(3))
) ENGINE = ReplacingMergeTree
ORDER BY (block_time, transaction_hash, log_index);

-- Contracts that emit generic bridge events (LayerZero OFTs, CCIP token pools,
-- Avalanche Bridge tokens), checked on-chain once. Only valid ones count.
CREATE TABLE IF NOT EXISTS bridge_contracts (
    address FixedString(20),
    bridge LowCardinality(String),  -- layerzero, stargate, ccip, avalanche-bridge
    token FixedString(20),  -- Token moved: the contract itself, or an adapter's token()
    valid Bool,  -- Wired to the bridge, e.g. an OFT whose endpoint() is LayerZero's
    note String,  -- Why it is (in)valid
    checked_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(checked_at)
ORDER BY address;

-- Hourly USD prices from Chainlink AnswerUpdated logs (pkg/bridges): the last
-- answer in the hour.
CREATE TABLE IF NOT EXISTS price_hourly (
    feed LowCardinality(String),  -- ETH, BTC, AVAX, LINK
    hour DateTime('UTC'),
    price Float64,
    answered_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(answered_at)
ORDER BY (feed, hour);

-- C-Chain bridge transfers, one row per leg, each transfer once (pkg/bridges).
CREATE TABLE IF NOT EXISTS bridge_transfers (
    block_time DateTime64(3, 'UTC'),
    transaction_hash FixedString(32),
    log_index UInt32,  -- Of the event the leg is read from
    bridge LowCardinality(String),  -- a bridges.Bridges id, e.g. cctp, layerzero
    direction Enum8('in' = 1, 'out' = 2),  -- Into or out of the C-Chain
    chain LowCardinality(String),  -- Counterparty chain key, e.g. ethereum, base, bitcoin
    remote_id String,  -- Bridge-native chain id, e.g. cctp:0, lz:30101, ccip:5009297550715157269
    token FixedString(20),
    amount UInt256,  -- Token base units
    usd Nullable(Float64),  -- NULL when the token has no price
    from_address FixedString(20),  -- Sender on the C-Chain (out), zeros when unknown
    to_address FixedString(20),  -- Recipient on the C-Chain (in), zeros when unknown
    via LowCardinality(String),  -- Wrapping bridge for a leg counted under another, e.g. ccip on a cctp leg
    passthrough Bool,  -- The tx bridges in and out again, left out of totals
    derived_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(derived_at)
ORDER BY (block_time, transaction_hash, log_index);

-- Function signatures - 4-byte selector -> canonical signature, filled by the
-- bundled seed list and demand-driven OpenChain lookups (pkg/enrich). Several
-- rows per selector are expected (collisions); the API picks one per tx using
-- priority + calldata-length plausibility. signature = '' is a negative-cache
-- row (looked up, unknown) retried weekly. Never written on the request path.
CREATE TABLE IF NOT EXISTS function_signatures (
    selector FixedString(4),
    signature String,  -- canonical text such as transfer(address,uint256), empty string = unknown
    name String,  -- function name, the part before the parameter list
    source LowCardinality(String),  -- seed | openchain
    priority UInt8,  -- 0 seed, 1 openchain+verified contract, 2 openchain
    resolved_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(resolved_at)
ORDER BY (selector, signature);

-- Address labels - one row per (address, source). Sources are merged at read
-- time: curated beats verified-source beats token-metadata. source =
-- 'unverified' rows are negative cache (EOA or unverified contract) so the
-- Routescan fetcher does not re-query; they carry no name and never surface.
CREATE TABLE IF NOT EXISTS address_labels (
    chain_id UInt32,
    address FixedString(20),
    name String,
    kind LowCardinality(String),  -- protocol | token | pool | router | bridge | exchange | wallet | contract
    protocol String,  -- optional group name, e.g. Pharaoh
    is_contract Bool,
    verified Bool,  -- has verified source on Routescan (or curated says so)
    implementation Nullable(FixedString(20)),  -- proxy implementation if any
    source LowCardinality(String),  -- curated | verified-source | token-metadata | unverified
    updated_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, address, source);

-- Address lookup tables for the explorer's address pages. raw_txs and
-- raw_traces are sorted by block, so finding one address meant scanning their
-- bloom filters: about 2.5% of the table even for a small wallet (the filter's
-- false-positive floor), and the whole table for a busy contract (30s
-- timeouts). These keep one row per (address, tx) or (address, trace), sorted
-- by address, so a page is one short read from the newest end.
-- Filled by the materialized views below on every insert. History before the
-- views existed is copied once with `icicle address-backfill`.
-- Readers must ORDER BY the full sorting key (chain_id DESC, address DESC, ...)
-- because unhex(?) is not folded to a constant, and without the fixed prefix
-- in ORDER BY ClickHouse falls back to a full sort of the address's rows.
CREATE TABLE IF NOT EXISTS address_txs (
    chain_id UInt32,
    address FixedString(20),  -- the from or the to of the tx
    block_number UInt32,
    transaction_index UInt16,
    block_time DateTime64(3, 'UTC'),
    hash FixedString(32),
    from FixedString(20),
    to Nullable(FixedString(20)),
    value UInt256,
    gas_limit UInt32,
    gas_price UInt64,
    gas_used UInt32,
    success Bool,
    type UInt8,
    selector String,  -- first 4 bytes of input, for method resolution
    input_len UInt32
) ENGINE = ReplacingMergeTree
ORDER BY (chain_id, address, block_number, transaction_index);

-- Only traces the internal-txs endpoint shows: value transfers and contract
-- creations (about 5.5% of all traces).
CREATE TABLE IF NOT EXISTS address_traces (
    chain_id UInt32,
    address FixedString(20),  -- the from or the to of the trace
    block_number UInt32,
    transaction_index UInt16,
    trace_address Array(UInt16),
    block_time DateTime64(3, 'UTC'),
    tx_hash FixedString(32),
    from FixedString(20),
    to Nullable(FixedString(20)),
    value UInt256,
    gas_used UInt32,
    call_type LowCardinality(String),
    tx_success Bool
) ENGINE = ReplacingMergeTree
ORDER BY (chain_id, address, block_number, transaction_index, trace_address);

-- Progress of `icicle address-backfill`, one row per <table>:<chain_id>.
CREATE TABLE IF NOT EXISTS address_backfill_state (
    key String,
    cutoff UInt32,  -- first block the materialized view captured, the backfill copies below it
    next_block UInt32  -- blocks below this are copied
) ENGINE = EmbeddedRocksDB
PRIMARY KEY key;

-- arrayDistinct gives a self-transfer one row, matching the old from/to query.
CREATE MATERIALIZED VIEW IF NOT EXISTS address_txs_mv TO address_txs AS
SELECT
    chain_id, address, block_number, transaction_index, block_time, hash,
    from, to, value, gas_limit, gas_price, gas_used, success, type,
    substring(input, 1, 4) AS selector, toUInt32(length(input)) AS input_len
FROM raw_txs
ARRAY JOIN arrayDistinct(if(isNull(to), [from], [from, assumeNotNull(to)])) AS address;

CREATE MATERIALIZED VIEW IF NOT EXISTS address_traces_mv TO address_traces AS
SELECT
    chain_id, address, block_number, transaction_index, trace_address, block_time,
    tx_hash, from, to, value, gas_used, call_type, tx_success
FROM raw_traces
ARRAY JOIN arrayDistinct(if(isNull(to), [from], [from, assumeNotNull(to)])) AS address
WHERE (value > 0 OR call_type IN ('CREATE', 'CREATE2'));
