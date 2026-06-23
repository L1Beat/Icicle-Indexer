-- Mid-band characterization: is the 3-10 block band real room or incumbent
-- indifference? Read-only over stealtime_results (one clean run after a DROP).
-- Bands by steal_time: fast [0,2], mid [3,10], slow [11+]. Run with:
--   docker exec -i icicle-clickhouse clickhouse-client --user default \
--     --password "$CLICKHOUSE_PASSWORD" --multiquery < scripts/stealtime_midband.sql

-- Profit-ranked top-10 incumbents (the fast actors that win profitable flow).
DROP TABLE IF EXISTS st_top10;
CREATE TABLE st_top10 ENGINE = Memory AS
  SELECT liquidator FROM stealtime_results
  WHERE chain_id = 43114 AND profitable
  GROUP BY liquidator ORDER BY count() DESC LIMIT 10;

SELECT '=== context: top10 by count-over-all (incl dust) vs by profit ===';
SELECT 'count_all' AS rank, hex(liquidator) AS liquidator, count() AS n
FROM stealtime_results WHERE chain_id = 43114 GROUP BY liquidator ORDER BY n DESC LIMIT 10;
SELECT 'profit' AS rank, hex(liquidator) AS liquidator, count() AS profitable_n
FROM stealtime_results WHERE chain_id = 43114 AND profitable GROUP BY liquidator ORDER BY profitable_n DESC LIMIT 10;

SELECT '=== M1: mid-band taker composition (top10 vs outsider) ===';
SELECT
  countIf(liquidator IN (SELECT liquidator FROM st_top10)) AS by_top10,
  countIf(liquidator NOT IN (SELECT liquidator FROM st_top10)) AS by_outsider,
  round(countIf(liquidator IN (SELECT liquidator FROM st_top10)) / count(), 3) AS top10_share
FROM stealtime_results
WHERE chain_id = 43114 AND profitable AND steal_time BETWEEN 3 AND 10;

SELECT '=== M2: profit distribution by band ===';
SELECT
  multiIf(steal_time <= 2, 'fast', steal_time <= 10, 'mid', 'slow') AS band,
  count() AS n,
  round(sum(net_profit_usd) / 1e18) AS sum_usd,
  round(quantile(0.5)(net_profit_usd) / 1e18, 2) AS median_usd,
  round(quantile(0.9)(net_profit_usd) / 1e18, 2) AS p90_usd
FROM stealtime_results WHERE chain_id = 43114 AND profitable
GROUP BY band ORDER BY band;

SELECT '=== M3: profit by taker within mid band (the decisive cut) ===';
SELECT
  liquidator IN (SELECT liquidator FROM st_top10) AS is_top10,
  count() AS n,
  round(sum(net_profit_usd) / 1e18) AS sum_usd,
  round(quantile(0.5)(net_profit_usd) / 1e18, 2) AS median_usd,
  round(quantile(0.9)(net_profit_usd) / 1e18, 2) AS p90_usd
FROM stealtime_results
WHERE chain_id = 43114 AND profitable AND steal_time BETWEEN 3 AND 10
GROUP BY is_top10;

SELECT '=== M4: was a top10 incumbent active in the open window [crossing, taken]? ===';
SELECT round(avg(present), 3) AS frac_top10_present_in_window
FROM (
  SELECT m.account, m.taken_block,
    if(countIf(s.account != m.account
               AND s.taken_block >= m.crossing_block
               AND s.taken_block <= m.taken_block
               AND s.liquidator IN (SELECT liquidator FROM st_top10)) > 0, 1, 0) AS present
  FROM (SELECT account, crossing_block, taken_block FROM stealtime_results
        WHERE chain_id = 43114 AND profitable AND steal_time BETWEEN 3 AND 10) m
  CROSS JOIN (SELECT account, taken_block, liquidator FROM stealtime_results WHERE chain_id = 43114) s
  GROUP BY m.account, m.crossing_block, m.taken_block
);

SELECT '=== M5: in-window market activity (any taker, any position) ===';
SELECT
  round(avg(others), 2) AS avg_other_liqs_in_window,
  quantile(0.5)(others) AS median_others,
  countIf(others = 0) AS windows_with_no_other_activity,
  count() AS mid_windows
FROM (
  SELECT m.account, m.taken_block,
    countIf(s.account != m.account
            AND s.taken_block >= m.crossing_block
            AND s.taken_block <= m.taken_block) AS others
  FROM (SELECT account, crossing_block, taken_block FROM stealtime_results
        WHERE chain_id = 43114 AND profitable AND steal_time BETWEEN 3 AND 10) m
  CROSS JOIN (SELECT account, taken_block FROM stealtime_results WHERE chain_id = 43114) s
  GROUP BY m.account, m.crossing_block, m.taken_block
);

SELECT '=== M6: mid-band collateral and size composition ===';
SELECT hex(collateral_asset) AS collateral, size_bucket, count() AS n,
  round(sum(net_profit_usd) / 1e18) AS profit_usd
FROM stealtime_results
WHERE chain_id = 43114 AND profitable AND steal_time BETWEEN 3 AND 10
GROUP BY collateral_asset, size_bucket ORDER BY profit_usd DESC LIMIT 20;

SELECT '=== M7: taker concentration by band ===';
SELECT
  multiIf(steal_time <= 2, 'fast', steal_time <= 10, 'mid', 'slow') AS band,
  count() AS n,
  uniqExact(liquidator) AS distinct_takers,
  round(countIf(liquidator IN (SELECT liquidator FROM st_top10)) / count(), 3) AS top10_share
FROM stealtime_results WHERE chain_id = 43114 AND profitable
GROUP BY band ORDER BY band;

DROP TABLE st_top10;
