-- One-time backfill for the rewritten cumulative_addresses metric (chain_id 43114).
--
-- Run with the INDEXER STOPPED, piped into clickhouse-client:
--   docker exec -i icicle-clickhouse clickhouse-client --user default \
--     --password "$CH_PW" --multiquery < ~/icicle/scripts/backfill_cumulative_addresses.sql
--
-- It (1) creates the first-seen registry, (2) populates it from all raw_traces
-- history, (3) recomputes the full cumulative_addresses series for every
-- granularity (overwriting the old overcounted rows via ReplacingMergeTree), and
-- (4) parks the watermarks at the latest backfilled period so the indexer only
-- maintains forward when it restarts.

-- 1. Registry table (idempotent; matches indexer_tables.sql).
CREATE TABLE IF NOT EXISTS evm_address_first_seen (
    chain_id UInt32,
    address FixedString(20),
    first_seen SimpleAggregateFunction(min, DateTime64(3, 'UTC'))
) ENGINE = AggregatingMergeTree()
ORDER BY (chain_id, address);

-- 2. Global first appearance of every address ever seen as sender or recipient.
INSERT INTO evm_address_first_seen (chain_id, address, first_seen)
SELECT 43114 AS chain_id, address, min(block_time) AS first_seen
FROM (
    SELECT from AS address, block_time
    FROM raw_traces
    WHERE chain_id = 43114
      AND from != unhex('0000000000000000000000000000000000000000')

    UNION ALL

    SELECT assumeNotNull(to) AS address, block_time
    FROM raw_traces
    WHERE chain_id = 43114
      AND to IS NOT NULL
      AND to != unhex('0000000000000000000000000000000000000000')
) AS all_occurrences
GROUP BY address
SETTINGS max_memory_usage = 16000000000, max_bytes_before_external_group_by = 8000000000;

-- 3. Full cumulative series per granularity = running sum of new-addresses-per-period.
INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'cumulative_addresses', 'hour', period,
       sum(new_count) OVER (ORDER BY period ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
FROM (
    SELECT toStartOfHour(first_seen) AS period, count() AS new_count
    FROM (SELECT address, min(first_seen) AS first_seen FROM evm_address_first_seen WHERE chain_id = 43114 GROUP BY address)
    GROUP BY period
)
ORDER BY period;

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'cumulative_addresses', 'day', period,
       sum(new_count) OVER (ORDER BY period ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
FROM (
    SELECT toStartOfDay(first_seen) AS period, count() AS new_count
    FROM (SELECT address, min(first_seen) AS first_seen FROM evm_address_first_seen WHERE chain_id = 43114 GROUP BY address)
    GROUP BY period
)
ORDER BY period;

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'cumulative_addresses', 'week', period,
       sum(new_count) OVER (ORDER BY period ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
FROM (
    SELECT toStartOfWeek(first_seen) AS period, count() AS new_count
    FROM (SELECT address, min(first_seen) AS first_seen FROM evm_address_first_seen WHERE chain_id = 43114 GROUP BY address)
    GROUP BY period
)
ORDER BY period;

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'cumulative_addresses', 'month', period,
       sum(new_count) OVER (ORDER BY period ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
FROM (
    SELECT toStartOfMonth(first_seen) AS period, count() AS new_count
    FROM (SELECT address, min(first_seen) AS first_seen FROM evm_address_first_seen WHERE chain_id = 43114 GROUP BY address)
    GROUP BY period
)
ORDER BY period;

-- 4. Park watermarks at the latest backfilled period per granularity so the
--    restarted indexer maintains forward instead of re-walking history.
INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
SELECT 43114, 'evm_metrics/cumulative_addresses', granularity, max(period), 0
FROM metrics
WHERE chain_id = 43114 AND metric_name = 'cumulative_addresses'
GROUP BY granularity;
