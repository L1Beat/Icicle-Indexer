-- Adds four more C-Chain tokenized funds to the registry (category
-- 'tokenized_fund'): WTGXX and VBILL ($1-per-share money funds, peg 'USD'),
-- JAAA and ACRED (floating NAV, peg 'NAV', so DEX/bridge/ICM pricing never
-- values them at $1). Rows match pkg/chwrapper/stablecoins_seed.sql. Idempotent.
-- Needs the category column (stablecoins_category_tokenized_funds_2026-11-19.sql).
-- The last statement prints the top holders. Then run backfill_tokenized_funds_2026-11-19.sql.
-- Run with: sed '/^ *--/d' this_file > /tmp/atf.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CH_PW" -n < /tmp/atf.sql

INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at, doublecounted, category) VALUES
    (43114, unhex('870fd36b3bf7f5abeeea2c8d4abdf1dc4e33109d'), 'WTGXX', 'WisdomTree Government Money Market Digital Fund', 18, 'USD', 'WisdomTree', false, '2026-11-19 00:00:00', false, 'tokenized_fund'),
    (43114, unhex('7f4546ef315efc65336187fe3765ea779ac90183'), 'VBILL', 'VanEck Treasury Fund', 6, 'USD', 'VanEck', false, '2026-11-19 00:00:00', false, 'tokenized_fund'),
    (43114, unhex('58f93d6b1ef2f44ec379cb975657c132cbed3b6b'), 'JAAA', 'Janus Henderson AAA CLO Fund', 6, 'NAV', 'Janus Henderson', false, '2026-11-19 00:00:00', false, 'tokenized_fund'),
    (43114, unhex('7c64925002bfa705834b118a923e9911bee32875'), 'ACRED', 'Apollo Diversified Credit Securitize Fund', 6, 'NAV', 'Apollo', false, '2026-11-19 00:00:00', false, 'tokenized_fund');

-- Check: top 3 holders per new fund, with their share of total balance. A single
-- issuer/escrow wallet holding most of a fund belongs in stablecoin_excluded_holders
-- before the backfill runs.
SELECT s.symbol, lower(hex(b.wallet)) AS wallet,
       round(b.balance / pow(10, s.decimals)) AS shares,
       round(100 * b.balance / sum(b.balance) OVER (PARTITION BY b.token), 1) AS pct
FROM erc20_balances AS b
INNER JOIN (SELECT token, symbol, decimals FROM stablecoins FINAL WHERE chain_id = 43114 AND added_at = '2026-11-19 00:00:00' AND symbol NOT IN ('BUIDL', 'BENJI')) AS s ON s.token = b.token
WHERE b.chain_id = 43114 AND b.balance > 0
ORDER BY s.symbol, b.balance DESC
LIMIT 3 BY s.symbol;
