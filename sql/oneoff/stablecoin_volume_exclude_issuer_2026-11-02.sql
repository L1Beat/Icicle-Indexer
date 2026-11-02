-- One-time recompute of stablecoin 'volume' and 'transfers' history after
-- sql/evm_metrics/stablecoin_volume.sql stopped counting issuer operations
-- (mints, burns, and transfers to or from an excluded issuer treasury).
--
-- Step 1 scans raw_logs once and writes the hour series. Every hour that had
-- any Transfer gets a row, so hours whose only transfers were issuer
-- operations are overwritten with 0 instead of keeping their old value.
-- Steps 2-4 derive day, week and month by summing the hours (both metrics are
-- additive), with the same period starts the indexer uses.
--
-- Run AFTER deploying the new stablecoin_volume.sql, so the indexer writes new
-- periods with the same rule. Idempotent: stablecoin_metrics is a
-- ReplacingMergeTree on (chain_id, token, metric_name, granularity, period),
-- newest computed_at wins.
-- Run with: sed '/^ *--/d' this_file > /tmp/scv.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --password "$CH_PW" -n < /tmp/scv.sql

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, m.1, 'hour', period, m.2
FROM (
    SELECT address AS token, toStartOfHour(block_time) AS period,
        sumIf(reinterpretAsUInt256(reverse(data)), keep) AS v,
        countIf(keep) AS c
    FROM (
        SELECT address, block_time, data,
            substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
            AND substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
            AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
                SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
            AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
                SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114) AS keep
        FROM raw_logs
        WHERE chain_id = 43114
          AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = 43114)
          AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
          AND length(data) = 32
          AND block_time < toStartOfHour(now())
    )
    GROUP BY token, period
)
ARRAY JOIN [('volume', toString(v)), ('transfers', toString(c))] AS m;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, metric_name, 'day', toStartOfDay(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name IN ('volume', 'transfers')
  AND period < toStartOfDay(now())
GROUP BY token, metric_name, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, metric_name, 'week', toStartOfWeek(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name IN ('volume', 'transfers')
  AND period < toStartOfWeek(now())
GROUP BY token, metric_name, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, metric_name, 'month', toStartOfMonth(period) AS p, toString(sum(toUInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name IN ('volume', 'transfers')
  AND period < toStartOfMonth(now())
GROUP BY token, metric_name, p;
