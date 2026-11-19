-- Adds JTRSY (Janus Henderson Treasury Fund, Centrifuge share token, 6 decimals,
-- peg 'NAV') to the tokenized funds and backfills its history. It holds 1 share on
-- the C-Chain as of 2026-11-19. Its price comes from its Centrifuge ERC-7540 vault
-- (price_source erc7540) once the indexer runs the new seed or this insert.
-- Step 1 matches pkg/chwrapper/stablecoins_seed.sql. The rest follow the same rules
-- as sql/evm_metrics/stablecoin_supply.sql and stablecoin_volume.sql: hour series
-- from raw_logs, then day, week and month summed from the hours, closed periods
-- only. holders is forward-only and not backfilled. Idempotent.
-- Run with: sed '/^ *--/d' this_file > /tmp/jtrsy.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CH_PW" -n < /tmp/jtrsy.sql

INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at, doublecounted, category) VALUES
    (43114, unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'), 'JTRSY', 'Janus Henderson Treasury Fund', 6, 'NAV', 'Janus Henderson', false, '2026-11-19 20:00:00', false, 'tokenized_fund');

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
      AND address IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32
      AND block_time < toStartOfHour(now())
)
GROUP BY token, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address AS token, 'volume', 'hour', toStartOfHour(block_time) AS period, toString(sum(reinterpretAsUInt256(reverse(data))))
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
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
  AND address IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
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
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'week', toStartOfWeek(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'month', toStartOfMonth(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'volume', 'day', toStartOfDay(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'volume'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'volume', 'week', toStartOfWeek(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'volume'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'volume', 'month', toStartOfMonth(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'volume'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'transfers', 'day', toStartOfDay(period) AS p, toString(sum(toUInt64(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'transfers'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'transfers', 'week', toStartOfWeek(period) AS p, toString(sum(toUInt64(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'transfers'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'transfers', 'month', toStartOfMonth(period) AS p, toString(sum(toUInt64(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'transfers'
  AND token IN (unhex('a5d465251fbcc907f5dd6bb2145488dfc6a2627b'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;
