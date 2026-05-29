-- Cumulative addresses metric - total unique addresses seen up to each period
-- Parameters: chain_id, first_period, last_period, granularity
--
-- Strategy (incremental, bounded memory):
--   1. Maintain evm_address_first_seen: insert every address active in this window
--      with its windowed min(block_time). The table is AggregatingMergeTree with
--      SimpleAggregateFunction(min), so re-inserting an address that already exists
--      merges down to the GLOBAL earliest block_time -- no anti-join needed.
--   2. Count addresses whose GLOBAL first_seen (read via FINAL) falls in each period
--      of the window -- these are the genuinely-new addresses -- then take the
--      running sum on top of the prior cumulative value already stored in `metrics`.
--
-- Memory notes (why this shape, not the obvious ones):
--   * NO `WHERE address NOT IN (SELECT address FROM evm_address_first_seen)`:
--     that materialises the ENTIRE ~92M-row registry into an in-memory set
--     (CreatingSetsTransform) every run -> ~14 GiB -> code 241 OOM. The min-merge
--     makes the anti-join unnecessary, so we drop it entirely.
--   * NO `countDistinct()` / `GROUP BY address` over the full registry: a hash of
--     92M keys also blows the server memory cap. Step 2 instead reads the registry
--     with FINAL, which is a streaming merge by sort key (chain_id, address) -- low,
--     bounded memory regardless of registry size.
--   * Prior cumulative is read O(1) from `metrics`, never recomputed from raw_traces.

-- Step 1: register/refresh first appearance of every address active in this window.
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
GROUP BY address
SETTINGS max_bytes_before_external_group_by = 2000000000;

-- Step 2: cumulative count = prior cumulative + running sum of new addresses.
INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
WITH
new_per_period AS (
    SELECT
        toStartOf{granularityCamelCase}(first_seen) AS period,
        count() AS new_count
    FROM (
        -- FINAL collapses each address to its global-min first_seen via a streaming
        -- merge (bounded memory), so we count genuinely-new addresses per period.
        SELECT first_seen
        FROM evm_address_first_seen FINAL
        WHERE chain_id = @chain_id
    ) AS registry
    WHERE first_seen >= @first_period
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
