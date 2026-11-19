-- One-time history backfill for SECZ and XAUt0 (added 2026-11-19 for /rwa).
-- Same rules as sql/evm_metrics/stablecoin_supply.sql and stablecoin_volume.sql:
-- supply_change = received minus sent by holders that are neither 0x0 nor excluded,
-- volume/transfers leave out mints, burns and excluded holders. Hour series first
-- (supply_change gets a row for every hour with any Transfer, so a rerun after adding
-- excluded holders overwrites cleanly), then day, week and month summed from hours,
-- closed periods only. holders is forward-only and not backfilled. Idempotent.
-- Run AFTER add_rwa_2026-11-19.sql and the excluded-holder check.
-- Run with: sed '/^ *--/d' this_file > /tmp/brwa.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CH_PW" -n < /tmp/brwa.sql

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'hour', period, toString(sum(delta))
FROM (
    SELECT address AS token, toStartOfHour(block_time) AS period,
        if(substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
            AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
                SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114),
            toInt256(reinterpretAsUInt256(reverse(data))), toInt256(0))
        - if(substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
            AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
                SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114),
            toInt256(reinterpretAsUInt256(reverse(data))), toInt256(0)) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32
      AND block_time < toStartOfHour(now())
)
GROUP BY token, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address AS token, 'volume', 'hour', toStartOfHour(block_time) AS period, toString(sum(reinterpretAsUInt256(reverse(data))))
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
  AND block_time < toStartOfHour(now())
  AND substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
  AND substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
GROUP BY address, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address AS token, 'transfers', 'hour', toStartOfHour(block_time) AS period, toString(count())
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
  AND block_time < toStartOfHour(now())
  AND substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
  AND substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
GROUP BY address, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'day', toStartOfDay(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'week', toStartOfWeek(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'month', toStartOfMonth(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'volume', 'day', toStartOfDay(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'volume'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'volume', 'week', toStartOfWeek(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'volume'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'volume', 'month', toStartOfMonth(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'volume'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'transfers', 'day', toStartOfDay(period) AS p, toString(sum(toUInt64(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'transfers'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'transfers', 'week', toStartOfWeek(period) AS p, toString(sum(toUInt64(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'transfers'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'transfers', 'month', toStartOfMonth(period) AS p, toString(sum(toUInt64(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'transfers'
  AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;
