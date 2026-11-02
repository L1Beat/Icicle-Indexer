-- One-time recompute of NXUSD and MIM 'supply_change' history after their
-- lending vaults (DegenBox/BentoBox) were added to stablecoin_excluded_holders
-- on 2026-11-02. Same rule as sql/evm_metrics/stablecoin_supply.sql: received
-- minus sent by holders that are neither 0x0 nor excluded.
--
-- Step 1 writes the hour series. Every hour with any Transfer of the two tokens
-- gets a row, so hours whose movements were all vault-side are overwritten.
-- Steps 2-4 derive day, week and month by summing the hours.
--
-- Run AFTER the indexer has restarted with the new seed (it inserts the
-- excluded holders at startup). Idempotent.
-- Run with: sed '/^ *--/d' this_file > /tmp/ssv.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --password "$CH_PW" -n < /tmp/ssv.sql

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
      AND address IN (unhex('f14f4ce569cb3679e99d5059909e23b07bd2f387'), unhex('130966628846bfd36ff31a822705796e8cb8c18d'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32
      AND block_time < toStartOfHour(now())
)
GROUP BY token, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'day', toStartOfDay(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('f14f4ce569cb3679e99d5059909e23b07bd2f387'), unhex('130966628846bfd36ff31a822705796e8cb8c18d'))
  AND period < toStartOfDay(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'week', toStartOfWeek(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('f14f4ce569cb3679e99d5059909e23b07bd2f387'), unhex('130966628846bfd36ff31a822705796e8cb8c18d'))
  AND period < toStartOfWeek(now())
GROUP BY token, p;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'month', toStartOfMonth(period) AS p, toString(sum(toInt256(value)))
FROM stablecoin_metrics FINAL
WHERE chain_id = 43114 AND granularity = 'hour' AND metric_name = 'supply_change'
  AND token IN (unhex('f14f4ce569cb3679e99d5059909e23b07bd2f387'), unhex('130966628846bfd36ff31a822705796e8cb8c18d'))
  AND period < toStartOfMonth(now())
GROUP BY token, p;
