-- Seed stablecoin registry for Avalanche C-Chain (chain_id 43114).
-- Idempotent: rerunning produces identical rows because added_at is fixed,
-- and ReplacingMergeTree dedupes on (chain_id, token).
INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at) VALUES
    (43114, unhex('b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e'), 'USDC',   'USD Coin',                  6, 'USD', 'Circle',   false, '2026-01-01 00:00:00'),
    (43114, unhex('9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7'), 'USDT',   'Tether USD',                6, 'USD', 'Tether',   false, '2026-01-01 00:00:00'),
    (43114, unhex('a7d7079b0fead91f3e65f86e8915cb59c1a4c664'), 'USDC.e', 'USD Coin (Bridged)',        6, 'USD', 'Circle',   true,  '2026-01-01 00:00:00'),
    (43114, unhex('c7198437980c041c805a1edcba50c1ce5db95118'), 'USDT.e', 'Tether USD (Bridged)',      6, 'USD', 'Tether',   true,  '2026-01-01 00:00:00'),
    (43114, unhex('d586e7f844cea2f87f50152665bcbc2c279d8d70'), 'DAI.e',  'Dai Stablecoin (Bridged)', 18, 'USD', 'MakerDAO', true,  '2026-01-01 00:00:00');

-- Known issuer/treasury holders to exclude from circulating supply.
-- Idempotent via fixed added_at + ReplacingMergeTree on (chain_id, token, holder).
INSERT INTO stablecoin_excluded_holders (chain_id, token, holder, reason, added_at) VALUES
    -- Tether treasury / authorized-but-not-issued reserve on C-Chain.
    -- Verified empirically: this single holder accounts for the entire delta between
    -- our totalSupply and DeFiLlama's circulating-supply figure.
    (43114, unhex('9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7'), unhex('5754284f345afc66a98fbb0a0afe71e0f007b949'), 'treasury', '2026-01-01 00:00:00');
