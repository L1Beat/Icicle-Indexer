-- Crash-day go/no-go, Steps 1, 4, 5 (the decisive, venue-independent part).
-- Pure SQL over stealtime_results, no archive reads. Step 4 is the gate: if
-- crash-day steal-times collapse to 0-2 blocks, it is a NO-GO and the V2 replay
-- (Steps 2/3/6) is not worth running. Run with:
--   docker exec -i icicle-clickhouse clickhouse-client --user default \
--     --password "$CLICKHOUSE_PASSWORD" --multiquery < scripts/stealtime_crashday.sql

-- Crash days: the 12 days with the most sized (>$1000 repaid) liquidations.
DROP TABLE IF EXISTS st_crashdays;
CREATE TABLE st_crashdays ENGINE = Memory AS
  SELECT toDate(b.block_time) AS day
  FROM stealtime_results s
  INNER JOIN raw_blocks b ON b.chain_id = s.chain_id AND b.block_number = toUInt32(s.taken_block)
  WHERE s.chain_id = 43114 AND s.repaid_usd > 1000000000000000000000
  GROUP BY day ORDER BY count() DESC LIMIT 12;

-- Profit-ranked top-10 incumbents (for Step 5).
DROP TABLE IF EXISTS st_top10;
CREATE TABLE st_top10 ENGINE = Memory AS
  SELECT liquidator FROM stealtime_results
  WHERE chain_id = 43114 AND profitable
  GROUP BY liquidator ORDER BY count() DESC LIMIT 10;

SELECT '=== Step 1: crash days (top 12 by >$1000 liquidation count) ===';
SELECT toDate(b.block_time) AS day,
  count() AS sized_liqs,
  round(sum(toFloat64(s.repaid_usd) / 1e18)) AS repaid_usd_total
FROM stealtime_results s
INNER JOIN raw_blocks b ON b.chain_id = s.chain_id AND b.block_number = toUInt32(s.taken_block)
WHERE s.chain_id = 43114 AND s.repaid_usd > 1000000000000000000000
  AND toDate(b.block_time) IN (SELECT day FROM st_crashdays)
GROUP BY day ORDER BY sized_liqs DESC;

SELECT '=== Step 4: steal-time distribution, crash vs normal days (sized, evaluated) ===';
SELECT
  multiIf(toDate(b.block_time) IN (SELECT day FROM st_crashdays), 'crash', 'normal') AS daytype,
  count() AS n,
  countIf(steal_time <= 2) AS b_0_2,
  countIf(steal_time BETWEEN 3 AND 5) AS b_3_5,
  countIf(steal_time BETWEEN 6 AND 10) AS b_6_10,
  countIf(steal_time BETWEEN 11 AND 20) AS b_11_20,
  countIf(steal_time > 20) AS b_21plus,
  round(quantile(0.5)(steal_time), 1) AS median,
  round(quantile(0.9)(steal_time), 1) AS p90,
  round(countIf(steal_time >= 3) / count(), 3) AS frac_3plus
FROM stealtime_results s
INNER JOIN raw_blocks b ON b.chain_id = s.chain_id AND b.block_number = toUInt32(s.taken_block)
WHERE s.chain_id = 43114 AND s.evaluated AND s.repaid_usd > 1000000000000000000000
GROUP BY daytype ORDER BY daytype;

SELECT '=== Step 5: taker concentration, crash vs normal days (sized) ===';
SELECT
  multiIf(toDate(b.block_time) IN (SELECT day FROM st_crashdays), 'crash', 'normal') AS daytype,
  count() AS n,
  uniqExact(s.liquidator) AS distinct_takers,
  round(countIf(s.liquidator IN (SELECT liquidator FROM st_top10)) / count(), 3) AS top10_share
FROM stealtime_results s
INNER JOIN raw_blocks b ON b.chain_id = s.chain_id AND b.block_number = toUInt32(s.taken_block)
WHERE s.chain_id = 43114 AND s.evaluated AND s.repaid_usd > 1000000000000000000000
GROUP BY daytype ORDER BY daytype;

DROP TABLE st_crashdays;
DROP TABLE st_top10;
