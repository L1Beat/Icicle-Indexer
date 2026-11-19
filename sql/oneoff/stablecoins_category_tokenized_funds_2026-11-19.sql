-- One-time split of tokenized money-market funds (BUIDL, BENJI) out of the
-- stablecoin list. Adds stablecoins.category and re-inserts the two fund rows
-- with category 'tokenized_fund' and a later added_at, so ReplacingMergeTree
-- keeps them over the old 2026-01-01 seed rows. The rows match the new
-- pkg/chwrapper/stablecoins_seed.sql exactly, so the indexer's startup seed
-- is a no-op afterwards. Their stablecoin_metrics history stays as is and is
-- served by /tokenized-funds/timeseries. Idempotent.
--
-- Run BEFORE restarting icicle-api: the new API selects the category column.
-- Run with: sed '/^ *--/d' this_file > /tmp/scf.sql, then
-- docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CH_PW" -n < /tmp/scf.sql

ALTER TABLE stablecoins ADD COLUMN IF NOT EXISTS category LowCardinality(String) DEFAULT 'stablecoin' AFTER doublecounted;

INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at, doublecounted, category) VALUES
    (43114, unhex('53fc82f14f009009b440a706e31c9021e1196a2f'), 'BUIDL', 'BlackRock USD Institutional Digital Liquidity Fund', 6, 'USD', 'BlackRock', false, '2026-11-19 00:00:00', false, 'tokenized_fund'),
    (43114, unhex('e08b4c1005603427420e64252a8b120cace4d122'), 'BENJI',  'Franklin OnChain U.S. Government Money Fund', 18, 'USD', 'Franklin Templeton', false, '2026-11-19 00:00:00', false, 'tokenized_fund');
