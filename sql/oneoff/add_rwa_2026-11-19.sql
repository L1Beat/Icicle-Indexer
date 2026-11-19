-- Adds the first non-fund /rwa assets to the registry: SECZ (Securitize Corp.
-- shares, category 'equity', peg 'NAV', priced by the indexer from RedStone
-- SECZ_EOD) and XAUt0 (Tether gold, category 'commodity', peg 'XAU', unpriced).
-- Rows match pkg/chwrapper/stablecoins_seed.sql. Idempotent.
-- The last statement prints each asset's top holders: an issuer, transfer-agent or
-- bridge wallet holding undistributed tokens belongs in stablecoin_excluded_holders
-- before backfill_rwa_2026-11-19.sql runs.
-- Run with: sed '/^ *--/d' this_file > /tmp/rwa.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CH_PW" -n < /tmp/rwa.sql

INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at, doublecounted, category) VALUES
    (43114, unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), 'SECZ', 'Securitize Corp.', 6, 'NAV', 'Securitize', false, '2026-11-19 21:00:00', false, 'equity'),
    (43114, unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'), 'XAUt0', 'XAUt0', 6, 'XAU', 'Tether', false, '2026-11-19 21:00:00', false, 'commodity');

SELECT s.symbol, lower(hex(b.wallet)) AS wallet,
       round(b.balance / pow(10, s.decimals), 2) AS units,
       round(100 * b.balance / sum(b.balance) OVER (PARTITION BY b.token), 1) AS pct
FROM erc20_balances AS b
INNER JOIN (SELECT token, symbol, decimals FROM stablecoins FINAL WHERE chain_id = 43114 AND token IN (unhex('5954ff4099ac47c4d6d098a9216f3278bb8c9506'), unhex('2775d5105276781b4b85ba6ea6a6653beed1dd32'))) AS s ON s.token = b.token
WHERE b.chain_id = 43114 AND b.balance > 0
ORDER BY s.symbol, b.balance DESC
LIMIT 5 BY s.symbol;
