-- Cumulative addresses metric - total unique addresses seen up to each period
-- Parameters: chain_id, first_period, last_period, granularity
--
-- Strategy (incremental, bounded memory):
--   1. Maintain evm_address_first_seen: insert the global first appearance of any
--      address that is NEW in this window (anti-join against the registry). Because
--      we process strictly forward, the first time an address is inserted is its
--      true global first_seen.
--   2. Count addresses whose first_seen falls in each period of the window (these
--      are the genuinely-new addresses), take the running sum on top of the prior
--      cumulative value already stored in `metrics`.
--
-- This avoids the previous approach's two problems:
--   * OOM: the old baseline did countDistinct() over all of raw_traces every cycle,
--     building a multi-GB exact hash set. We now read the prior cumulative from
--     `metrics` (O(1)) and only scan the small first_seen registry.
--   * Overcounting: the old query counted an address as "new" if its first
--     appearance WITHIN the window fell in a period, double-counting returning
--     addresses on top of the baseline. We now key off the GLOBAL first_seen.

-- Step 1: register first appearance of addresses new in this window.
INSERT INTO evm_address_first_seen (chain_id, address, first_seen)
SELECT
    @chain_id AS chain_id,
    address,
    min(block_time) AS first_seen
FROM (
    SELECT from AS address, block_time
    FROM raw_traces
    WHERE chain_id = @chain_id
      AND block_time >= @first_period
      AND block_time < @last_period
      AND from != unhex('0000000000000000000000000000000000000000')

    UNION ALL

    SELECT assumeNotNull(to) AS address, block_time
    FROM raw_traces
    WHERE chain_id = @chain_id
      AND block_time >= @first_period
      AND block_time < @last_period
      AND to IS NOT NULL
      AND to != unhex('0000000000000000000000000000000000000000')
) AS window_occurrences
WHERE address NOT IN (
    SELECT address FROM evm_address_first_seen WHERE chain_id = @chain_id
)
GROUP BY address
SETTINGS max_memory_usage = 8000000000;

-- Step 2: cumulative count = prior cumulative + running sum of new addresses.
INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
WITH
new_per_period AS (
    SELECT
        toStartOf{granularityCamelCase}(first_seen) AS period,
        count() AS new_count
    FROM evm_address_first_seen
    WHERE chain_id = @chain_id
      AND first_seen >= @first_period
      AND first_seen < @last_period
    GROUP BY period
),
baseline AS (
    SELECT toUInt64(0) AS prev_cumulative
    UNION ALL
    (
        SELECT value AS prev_cumulative
        FROM metrics FINAL
        WHERE chain_id = @chain_id
          AND metric_name = 'cumulative_addresses'
          AND granularity = '{granularity}'
          AND period < @first_period
        ORDER BY period DESC
        LIMIT 1
    )
)
SELECT
    {chain_id} AS chain_id,
    'cumulative_addresses' AS metric_name,
    '{granularity}' AS granularity,
    period,
    (SELECT max(prev_cumulative) FROM baseline)
        + sum(new_count) OVER (ORDER BY period ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS value
FROM new_per_period
ORDER BY period;
