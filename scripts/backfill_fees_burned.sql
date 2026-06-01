-- One-time backfill for the fees_burned_total / fees_burned_base metrics (chain_id 43114).
--
-- Run with the INDEXER STOPPED, piped into clickhouse-client:
--   sudo systemctl stop icicle-indexer
--   docker exec -i icicle-clickhouse clickhouse-client --user default \
--     --password "$CH_PW" --multiquery < ~/icicle/scripts/backfill_fees_burned.sql
--   sudo systemctl start icicle-indexer
--
-- It computes the full burn history for every granularity in one pass each
-- (instead of letting the indexer crawl 6 periods/cycle from epoch, which would
-- re-scan C-Chain raw_txs thousands of times), then parks the watermarks at the
-- latest backfilled period so the restarted indexer only maintains forward.
--
-- Values are nAVAX (1 nAVAX = 1e9 wei); UInt256 sum + intDiv avoids UInt64
-- overflow and floating-point rounding. ReplacingMergeTree dedups on re-run.

-- 1. Total burned = sum(gas_used * gas_price), per granularity.
INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_total', 'hour', toStartOfHour(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(gas_price)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfHour(block_time);

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_total', 'day', toStartOfDay(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(gas_price)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfDay(block_time);

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_total', 'week', toStartOfWeek(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(gas_price)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfWeek(block_time);

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_total', 'month', toStartOfMonth(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(gas_price)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfMonth(block_time);

-- 2. Base-fee portion = sum(gas_used * base_fee_per_gas), per granularity.
INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_base', 'hour', toStartOfHour(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(base_fee_per_gas)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfHour(block_time);

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_base', 'day', toStartOfDay(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(base_fee_per_gas)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfDay(block_time);

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_base', 'week', toStartOfWeek(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(base_fee_per_gas)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfWeek(block_time);

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT 43114, 'fees_burned_base', 'month', toStartOfMonth(block_time),
       toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(base_fee_per_gas)), 1000000000))
FROM raw_txs WHERE chain_id = 43114
GROUP BY toStartOfMonth(block_time);

-- 3. Park watermarks at the latest backfilled period per metric+granularity so
--    the restarted indexer maintains forward instead of re-walking from epoch.
INSERT INTO indexer_watermarks (chain_id, indexer_name, granularity, last_period, last_block_num)
SELECT 43114, concat('evm_metrics/', metric_name), granularity, max(period), 0
FROM metrics
WHERE chain_id = 43114 AND metric_name IN ('fees_burned_total', 'fees_burned_base')
GROUP BY metric_name, granularity;
