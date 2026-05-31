-- Seed stablecoin registry for Avalanche C-Chain (chain_id 43114).
-- Idempotent: rerunning produces identical rows because added_at is fixed,
-- and ReplacingMergeTree dedupes on (chain_id, token).
INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at) VALUES
    (43114, unhex('b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e'), 'USDC',   'USD Coin',                  6, 'USD', 'Circle',   false, '2026-01-01 00:00:00'),
    (43114, unhex('9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7'), 'USDT',   'Tether USD',                6, 'USD', 'Tether',   false, '2026-01-01 00:00:00'),
    (43114, unhex('a7d7079b0fead91f3e65f86e8915cb59c1a4c664'), 'USDC.e', 'USD Coin (Bridged)',        6, 'USD', 'Circle',   true,  '2026-01-01 00:00:00'),
    (43114, unhex('c7198437980c041c805a1edcba50c1ce5db95118'), 'USDT.e', 'Tether USD (Bridged)',      6, 'USD', 'Tether',   true,  '2026-01-01 00:00:00'),
    (43114, unhex('d586e7f844cea2f87f50152665bcbc2c279d8d70'), 'DAI.e',  'Dai Stablecoin (Bridged)',     18, 'USD', 'MakerDAO',    true,  '2026-01-01 00:00:00'),
    -- Long-tail stablecoins discovered from token_metadata, verified against DeFiLlama supply figures.
    (43114, unhex('00000000efe302beaa2b3e6e1b18d08d69a9012a'), 'AUSD',  'Agora Dollar',                  6, 'USD', 'Agora',       false, '2026-01-01 00:00:00'),
    (43114, unhex('24de8771bc5ddb3362db529fc3358f2df3a0e346'), 'avUSD', 'Avant USD',                    18, 'USD', 'Avant',       false, '2026-01-01 00:00:00'),
    (43114, unhex('9c9e5fd8bbc25984b178fdce6117defa39d2db39'), 'BUSD',  'Binance USD',                  18, 'USD', 'Binance',     false, '2026-01-01 00:00:00'),
    -- BUIDL: Securitize-issued, 6 decimals. Live circulating contract on C-Chain (~$386M).
    -- Replaced 2026-05-31: old seed used 0xd33176… (decimals 18), a dead/old contract with junk balances.
    (43114, unhex('53fc82f14f009009b440a706e31c9021e1196a2f'), 'BUIDL', 'BlackRock USD Institutional Digital Liquidity Fund', 6, 'USD', 'BlackRock', false, '2026-01-01 00:00:00'),
    (43114, unhex('c891eb4cbdeff6e073e859e987815ed1505c2acd'), 'EURC',  'Euro Coin',                     6, 'EUR', 'Circle',      false, '2026-01-01 00:00:00'),
    (43114, unhex('e7c3d8c9a439fede00d2600032d5db0be71c3c29'), 'JPYC',  'JPY Coin',                     18, 'JPY', 'JPYC Inc.',   false, '2026-01-01 00:00:00'),
    (43114, unhex('130966628846bfd36ff31a822705796e8cb8c18d'), 'MIM',   'Magic Internet Money',         18, 'USD', 'Abracadabra', false, '2026-01-01 00:00:00'),
    (43114, unhex('f14f4ce569cb3679e99d5059909e23b07bd2f387'), 'NXUSD', 'NXUSD',                        18, 'USD', '',            false, '2026-01-01 00:00:00'),
    (43114, unhex('180af87b47bf272b2df59dccf2d76a6eafa625bf'), 'reUSD', 'Re Protocol reUSD',            18, 'USD', 'Re Protocol', false, '2026-01-01 00:00:00'),
    (43114, unhex('1c20e891bab6b1727d14da358fae2984ed9b59eb'), 'TUSD',  'TrueUSD',                      18, 'USD', 'TrustToken',  false, '2026-01-01 00:00:00'),
    (43114, unhex('ab05b04743e0aeaf9d2ca81e5d3b8385e4bf961e'), 'USDS',  'Spice USD',                    18, 'USD', '',            false, '2026-01-01 00:00:00'),
    (43114, unhex('dbc5192a6b6ffee7451301bb4ec312f844f02b4a'), 'UTY',   'XSY UTY',                      18, 'USD', 'XSY',         false, '2026-01-01 00:00:00'),
    (43114, unhex('b2f85b7ab3c2b6f62df06de6ae7d09c010a5096e'), 'XSGD',  'XSGD',                          6, 'SGD', 'StraitsX',    false, '2026-01-01 00:00:00'),
    (43114, unhex('111111111111ed1d73f860f57b2798b683f2d325'), 'YUSD',  'YUSD Stablecoin',              18, 'USD', 'Yeti',        false, '2026-01-01 00:00:00');

-- Known issuer/treasury holders to exclude from circulating supply.
-- Idempotent via fixed added_at + ReplacingMergeTree on (chain_id, token, holder).
INSERT INTO stablecoin_excluded_holders (chain_id, token, holder, reason, added_at) VALUES
    -- Tether treasury / authorized-but-not-issued reserve on C-Chain.
    -- Verified empirically: this single holder accounts for the entire delta between
    -- our totalSupply and DeFiLlama's circulating-supply figure.
    (43114, unhex('9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7'), unhex('5754284f345afc66a98fbb0a0afe71e0f007b949'), 'treasury', '2026-01-01 00:00:00');
