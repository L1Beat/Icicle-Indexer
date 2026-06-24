-- Lending liquidation-risk engine tables.
-- Conventions match pkg/chwrapper/raw_tables.sql: FixedString(20) addresses,
-- UInt256 amounts, DateTime64(3, 'UTC') timestamps. Do not put a semicolon inside
-- an inline comment: the SQL splitter breaks statements on semicolons.

-- Resolved and on-chain-verified protocol addresses. One row per role.
CREATE TABLE IF NOT EXISTS lending_protocol_addresses (
    chain_id UInt32,
    protocol LowCardinality(String),       -- aave-v3 | benqi
    role LowCardinality(String),           -- pool | oracle | provider | data_provider | comptroller | market | multicall
    address FixedString(20),
    verified Bool,                         -- on-chain check passed
    note String,                           -- mismatch warning detail, empty when clean
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, protocol, role, address);

-- Protocol-global parameters. Benqi close factor and liquidation incentive are
-- Comptroller-global, so they live here rather than duplicated per asset (rule 6).
CREATE TABLE IF NOT EXISTS lending_protocol_globals (
    chain_id UInt32,
    protocol LowCardinality(String),
    close_factor_bps UInt16,               -- Benqi global close factor (5000 = 50%). 0 for Aave (rule-based)
    liquidation_incentive_bps UInt16,      -- Benqi global incentive multiplier (10800 = collateral worth 108%). 0 for Aave
    small_position_base UInt256,           -- Aave small-position close-factor threshold in base currency. 0 disables
    base_currency_unit UInt256,            -- oracle base unit (Aave 1e8 USD base)
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, protocol);

-- Per-asset risk parameters. Aave per-reserve, Benqi per-market.
CREATE TABLE IF NOT EXISTS lending_protocol_params (
    chain_id UInt32,
    protocol LowCardinality(String),
    asset FixedString(20),                 -- underlying reserve token
    market FixedString(20),                -- Benqi qiToken or Aave aToken, zero when not applicable
    symbol String,
    decimals UInt8,
    liquidation_threshold_bps UInt16,      -- Aave reserve LT, Benqi collateral factor mapped here
    liquidation_bonus_bps UInt16,          -- Aave per-reserve multiplier (10500 = 105%). 0 for Benqi (global)
    ltv_bps UInt16,
    can_collateral Bool,
    can_borrow Bool,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, protocol, asset);

-- Universe of accounts seen on each protocol. AggregatingMergeTree keeps the min
-- first-seen and max last-event automatically, so discovery just appends.
CREATE TABLE IF NOT EXISTS lending_accounts (
    chain_id UInt32,
    protocol LowCardinality(String),
    account FixedString(20),
    first_seen_block SimpleAggregateFunction(min, UInt32),
    last_event_block SimpleAggregateFunction(max, UInt32),
    updated_at SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree
ORDER BY (chain_id, protocol, account);

-- Candidate asset exposure per account, from lending events. Over-inclusive on
-- purpose: it bounds health reads and drives the price-trigger asset fan-out
-- (rule 1). The authoritative current per-asset state is lending_position_assets.
CREATE TABLE IF NOT EXISTS lending_exposure (
    chain_id UInt32,
    protocol LowCardinality(String),
    account FixedString(20),
    asset FixedString(20),
    side LowCardinality(String),           -- collateral | borrow
    last_block SimpleAggregateFunction(max, UInt32),
    updated_at SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree
ORDER BY (chain_id, protocol, account, asset, side);

-- Current health snapshot per (account, protocol). Serve with FINAL or argMax,
-- never a plain SELECT, since pre-merge duplicates can return stale health (rule 2).
CREATE TABLE IF NOT EXISTS lending_positions (
    chain_id UInt32,
    protocol LowCardinality(String),
    account FixedString(20),
    health_factor UInt256,                 -- 1e18-scaled. Benqi derived, for ranking and display only (rule 3)
    collateral_base UInt256,               -- weighted collateral in oracle base currency
    debt_base UInt256,
    shortfall_base UInt256,                -- Benqi shortfall, 0 for Aave
    liquidity_base UInt256,                -- Benqi positive account liquidity (distance to liquidation), 0 for Aave
    liquidatable Bool,                     -- Aave HF<1e18, Benqi shortfall>0 (rule 3)
    tier LowCardinality(String),           -- hot | warm | cold
    block_number UInt32,                   -- chain head at read time
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, protocol, account);

-- Backfill the liquidity_base column onto an already-created table (idempotent).
ALTER TABLE lending_positions ADD COLUMN IF NOT EXISTS liquidity_base UInt256 AFTER shortfall_base;

-- Per-asset breakdown for the feed. Assets that drop to zero are written with
-- amount 0 on the next refresh so stale legs do not linger.
CREATE TABLE IF NOT EXISTS lending_position_assets (
    chain_id UInt32,
    protocol LowCardinality(String),
    account FixedString(20),
    asset FixedString(20),
    side LowCardinality(String),           -- collateral | debt
    amount UInt256,                        -- token units
    base_value UInt256,                    -- oracle base-currency value
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, protocol, account, asset, side);

-- Executed on-chain liquidations (Aave LiquidationCall, Benqi LiquidateBorrow),
-- valued in USD at the event block. ReplacingMergeTree keyed by the event identity
-- so a re-scan replaces rather than duplicates. Serve with FINAL.
CREATE TABLE IF NOT EXISTS lending_liquidations (
    chain_id UInt32,
    protocol LowCardinality(String),       -- aave-v3 | benqi
    block_number UInt32,
    block_time DateTime64(3, 'UTC'),
    tx_hash FixedString(32),
    log_index UInt32,
    liquidator FixedString(20),
    borrower FixedString(20),
    collateral_asset FixedString(20),      -- Aave underlying / Benqi qiToken market
    debt_asset FixedString(20),            -- Aave underlying / Benqi qiToken market
    repay_amount UInt256,                  -- native debt units (Aave debtToCover, Benqi repayAmount)
    seize_amount UInt256,                  -- native units (Aave liquidatedCollateralAmount, Benqi seizeTokens)
    repaid_usd UInt256,                    -- 1e18 USD of repaid debt at the block, 0 when unpriced
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, block_number, tx_hash, log_index);

-- Periodic aggregate-risk snapshots per protocol, for the dashboard's risk-trend
-- timeseries. Append-only; one row per (protocol, sample time) at a fixed debt
-- floor so the series is comparable across time.
CREATE TABLE IF NOT EXISTS lending_risk_snapshots (
    chain_id UInt32,
    protocol LowCardinality(String),
    snapshot_at DateTime64(3, 'UTC'),
    min_debt_base UInt256,                 -- debt floor the snapshot was taken at
    open_positions UInt32,
    liquidatable UInt32,                   -- dust-free (debt > floor)
    bad_debt_count UInt32,                 -- collateral < debt
    bad_debt_total UInt256,                -- sum(debt - collateral), 1e18 USD
    near_count UInt32,                     -- not liquidatable, HF within the near band
    var_collateral UInt256,                -- collateral USD across at-risk positions
    var_debt UInt256                       -- debt USD across at-risk positions
) ENGINE = MergeTree
ORDER BY (chain_id, protocol, snapshot_at);

-- Append-only crossing events for the WebSocket tail.
CREATE TABLE IF NOT EXISTS lending_alerts (
    chain_id UInt32,
    protocol LowCardinality(String),
    account FixedString(20),
    kind LowCardinality(String),           -- liquidatable | near_liquidatable | recovered
    health_factor UInt256,
    collateral_base UInt256,
    debt_base UInt256,
    block_number UInt32,
    created_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = MergeTree()
ORDER BY (chain_id, created_at);
