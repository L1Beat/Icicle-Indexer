-- Adds five more C-Chain tokenized funds (second batch 2026-11-19, from DefiLlama
-- RWA contracts, checked on-chain): XDFIS-DeFi-2 (OpenTrade), AoABTd (Asseto),
-- HINC (Neuberger Berman / Securitize), nOPAL (Nest Credit), hyTB (HiYield). All
-- floating NAV, so peg 'NAV'. Rows match pkg/chwrapper/stablecoins_seed.sql. Idempotent.
-- The last statement prints the top holders. Then run backfill_tokenized_funds_2026-11-19b.sql.
-- Run with: sed '/^ *--/d' this_file > /tmp/atf2.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CH_PW" -n < /tmp/atf2.sql

INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at, doublecounted, category) VALUES
    (43114, unhex('c0fd0afa73b35544cea520138cfdfa723bead848'), 'XDFIS-DeFi-2', 'Diversified Fixed Income Strategy Vault (DeFi) 2', 6, 'NAV', 'OpenTrade', false, '2026-11-19 12:00:00', false, 'tokenized_fund'),
    (43114, unhex('b2ea3e7b80317c4e20d1927034162176e25834e2'), 'AoABTd', 'AoABTd', 18, 'NAV', 'Asseto', false, '2026-11-19 12:00:00', false, 'tokenized_fund'),
    (43114, unhex('f5fa39a16449b1ee07aa52ae79c6b00567c59f83'), 'HINC', 'Neuberger Securitize High Income Tokenized Fund', 6, 'NAV', 'Neuberger Berman', false, '2026-11-19 12:00:00', false, 'tokenized_fund'),
    (43114, unhex('119dd7daff816f29d7ee47596ae5e4bdc4299165'), 'nOPAL', 'Nest BlackOpal LiquidStone II Vault', 6, 'NAV', 'Nest Credit', false, '2026-11-19 12:00:00', false, 'tokenized_fund'),
    (43114, unhex('8475509d391e6ee5a8b7133221ce17019d307b3e'), 'hyTB', 'HiYield Treasury Bill Vault', 18, 'NAV', 'HiYield', false, '2026-11-19 12:00:00', false, 'tokenized_fund');

SELECT s.symbol, lower(hex(b.wallet)) AS wallet,
       round(b.balance / pow(10, s.decimals)) AS shares,
       round(100 * b.balance / sum(b.balance) OVER (PARTITION BY b.token), 1) AS pct
FROM erc20_balances AS b
INNER JOIN (SELECT token, symbol, decimals FROM stablecoins FINAL WHERE chain_id = 43114 AND token IN (unhex('c0fd0afa73b35544cea520138cfdfa723bead848'), unhex('b2ea3e7b80317c4e20d1927034162176e25834e2'), unhex('f5fa39a16449b1ee07aa52ae79c6b00567c59f83'), unhex('119dd7daff816f29d7ee47596ae5e4bdc4299165'), unhex('8475509d391e6ee5a8b7133221ce17019d307b3e'))) AS s ON s.token = b.token
WHERE b.chain_id = 43114 AND b.balance > 0
ORDER BY s.symbol, b.balance DESC
LIMIT 3 BY s.symbol;
