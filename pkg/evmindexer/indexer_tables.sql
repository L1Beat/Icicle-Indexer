-- Single unified metrics table for all metric types and granularities
CREATE TABLE IF NOT EXISTS metrics (
    chain_id UInt32,
    metric_name LowCardinality(String),  -- e.g., "active_addresses", "cumulative_addresses", "contracts", "cumulative_contracts", "icm_sent"
    granularity LowCardinality(String),  -- e.g., "hour", "day", "week", "month"
    period DateTime64(3, 'UTC'),         -- Period start time
    value UInt64,
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (chain_id, metric_name, granularity, period)
PARTITION BY (chain_id, toYYYYMM(period));

-- indexer_watermarks is created by chwrapper.CreateIndexerWatermarks.

-- First-seen address registry: one logical row per address holding the earliest
-- block_time it was ever observed (as sender or recipient) in raw_traces.
-- Used by the cumulative_addresses metric to count genuinely-new addresses per
-- period in O(distinct addresses) instead of re-scanning all of raw_traces.
-- SimpleAggregateFunction(min) merges duplicate inserts down to the global min.
CREATE TABLE IF NOT EXISTS evm_address_first_seen (
    chain_id UInt32,
    address FixedString(20),
    first_seen SimpleAggregateFunction(min, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree()
ORDER BY (chain_id, address);
