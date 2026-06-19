-- Seed stablecoin registry for Avalanche C-Chain (chain_id 43114).
-- Idempotent: rerunning produces identical rows because added_at is fixed,
-- and ReplacingMergeTree dedupes on (chain_id, token).
INSERT INTO stablecoins (chain_id, token, symbol, name, decimals, peg, issuer, bridged, added_at, doublecounted) VALUES
    (43114, unhex('b97ef9ef8734c71904d8002f8b6bc66dd9c48a6e'), 'USDC',   'USD Coin',                  6, 'USD', 'Circle',   false, '2026-01-01 00:00:00', false),
    (43114, unhex('9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7'), 'USDT',   'Tether USD',                6, 'USD', 'Tether',   false, '2026-01-01 00:00:00', false),
    (43114, unhex('a7d7079b0fead91f3e65f86e8915cb59c1a4c664'), 'USDC.e', 'USD Coin (Bridged)',        6, 'USD', 'Circle',   true,  '2026-01-01 00:00:00', false),
    (43114, unhex('c7198437980c041c805a1edcba50c1ce5db95118'), 'USDT.e', 'Tether USD (Bridged)',      6, 'USD', 'Tether',   true,  '2026-01-01 00:00:00', false),
    (43114, unhex('d586e7f844cea2f87f50152665bcbc2c279d8d70'), 'DAI.e',  'Dai Stablecoin (Bridged)',     18, 'USD', 'MakerDAO',    true,  '2026-01-01 00:00:00', false),
    -- Long-tail stablecoins discovered from token_metadata, verified against DeFiLlama supply figures.
    (43114, unhex('00000000efe302beaa2b3e6e1b18d08d69a9012a'), 'AUSD',  'Agora Dollar',                  6, 'USD', 'Agora',       false, '2026-01-01 00:00:00', false),
    (43114, unhex('24de8771bc5ddb3362db529fc3358f2df3a0e346'), 'avUSD', 'Avant USD',                    18, 'USD', 'Avant',       false, '2026-01-01 00:00:00', true),
    (43114, unhex('9c9e5fd8bbc25984b178fdce6117defa39d2db39'), 'BUSD',  'Binance USD',                  18, 'USD', 'Binance',     false, '2026-01-01 00:00:00', false),
    -- BUIDL: Securitize-issued, 6 decimals. Live circulating contract on C-Chain (~$386M).
    -- Replaced 2026-05-31: old seed used 0xd33176… (decimals 18), a dead/old contract with junk balances.
    (43114, unhex('53fc82f14f009009b440a706e31c9021e1196a2f'), 'BUIDL', 'BlackRock USD Institutional Digital Liquidity Fund', 6, 'USD', 'BlackRock', false, '2026-01-01 00:00:00', false),
    (43114, unhex('c891eb4cbdeff6e073e859e987815ed1505c2acd'), 'EURC',  'Euro Coin',                     6, 'EUR', 'Circle',      false, '2026-01-01 00:00:00', false),
    (43114, unhex('e7c3d8c9a439fede00d2600032d5db0be71c3c29'), 'JPYC',  'JPY Coin',                     18, 'JPY', 'JPYC Inc.',   false, '2026-01-01 00:00:00', false),
    (43114, unhex('130966628846bfd36ff31a822705796e8cb8c18d'), 'MIM',   'Magic Internet Money',         18, 'USD', 'Abracadabra', false, '2026-01-01 00:00:00', false),
    (43114, unhex('f14f4ce569cb3679e99d5059909e23b07bd2f387'), 'NXUSD', 'NXUSD',                        18, 'USD', '',            false, '2026-01-01 00:00:00', false),
    (43114, unhex('180af87b47bf272b2df59dccf2d76a6eafa625bf'), 'reUSD', 'Re Protocol reUSD',            18, 'USD', 'Re Protocol', false, '2026-01-01 00:00:00', true),
    (43114, unhex('1c20e891bab6b1727d14da358fae2984ed9b59eb'), 'TUSD',  'TrueUSD',                      18, 'USD', 'TrustToken',  false, '2026-01-01 00:00:00', false),
    (43114, unhex('ab05b04743e0aeaf9d2ca81e5d3b8385e4bf961e'), 'USDS',  'Spice USD',                    18, 'USD', '',            false, '2026-01-01 00:00:00', false),
    (43114, unhex('dbc5192a6b6ffee7451301bb4ec312f844f02b4a'), 'UTY',   'XSY UTY',                      18, 'USD', 'XSY',         false, '2026-01-01 00:00:00', true),
    (43114, unhex('b2f85b7ab3c2b6f62df06de6ae7d09c010a5096e'), 'XSGD',  'XSGD',                          6, 'SGD', 'StraitsX',    false, '2026-01-01 00:00:00', false),
    (43114, unhex('111111111111ed1d73f860f57b2798b683f2d325'), 'YUSD',  'YUSD Stablecoin',              18, 'USD', 'Yeti',        false, '2026-01-01 00:00:00', false),
    -- Batch added 2026-05-31: resolved from token_metadata via supply-match against DeFiLlama Avalanche
    -- figures (spoof contracts sharing these tickers were filtered out by holder count + supply).
    (43114, unhex('9ee1963f05553ef838604dd39403be21cef26aa4'), 'USDp',   'Parallel USD',                 18, 'USD', 'Parallel',          false, '2026-01-01 00:00:00', false),
    (43114, unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'), 'FRAX',   'Frax',                         18, 'USD', 'Frax',              false, '2026-01-01 00:00:00', false),
    (43114, unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'), 'aUSD',   'Stable Jack aUSD',             18, 'USD', 'Stable Jack',       false, '2026-01-01 00:00:00', false),
    (43114, unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'), 'FUSD',   'FinChain Dollar',              18, 'USD', 'FinChain',          false, '2026-01-01 00:00:00', false),
    (43114, unhex('80eede496655fb9047dd39d9f418d5483ed600df'), 'FRXUSD', 'Frax USD',                     18, 'USD', 'Frax',              false, '2026-01-01 00:00:00', true),
    (43114, unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'), 'USDe',   'Ethena USDe',                  18, 'USD', 'Ethena',            false, '2026-01-01 00:00:00', false),
    (43114, unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'), 'NUSD',   'Nexus USD',                    18, 'USD', 'Nexus',             false, '2026-01-01 00:00:00', false),
    (43114, unhex('025ab35ff6abcca56d57475249baaeae08419039'), 'ARUSD',  'arUSD',                        18, 'USD', '',                  false, '2026-01-01 00:00:00', false),
    (43114, unhex('221743dc9e954be4f86844649bf19b43d6f8366d'), 'DOLA',   'Dola',                         18, 'USD', 'Inverse Finance',   false, '2026-01-01 00:00:00', false),
    (43114, unhex('323665443cef804a3b5206103304bd4872ea4253'), 'USDV',   'Verified USD',                  6, 'USD', 'Verified',          false, '2026-01-01 00:00:00', false),
    (43114, unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'), 'USD+',   'USD+',                          6, 'USD', 'Overnight',         false, '2026-01-01 00:00:00', false),
    (43114, unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'), 'SBC',    'Stable Coin',                  18, 'USD', '',                  false, '2026-01-01 00:00:00', false),
    (43114, unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'), 'PYUSD',  'PayPal USD',                    6, 'USD', 'PayPal',            false, '2026-01-01 00:00:00', false),
    (43114, unhex('03569cc076654f82679c4ba2124d64774781b01d'), 'BOLD',   'Liquity BOLD',                 18, 'USD', 'Liquity',           false, '2026-01-01 00:00:00', false),
    (43114, unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'), 'BNUSD',  'Balanced Dollars',             18, 'USD', 'Balanced',          false, '2026-01-01 00:00:00', false),
    (43114, unhex('cc18b41a0f63c67f17f23388c848aec67b583422'), 'xpUSD',  'Gaming XP USD',                 6, 'USD', '',                  false, '2026-01-01 00:00:00', false),
    (43114, unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'), 'ZeUSD',  'Zoth ZeUSD',                    6, 'USD', 'Zoth',              false, '2026-01-01 00:00:00', false),
    (43114, unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'), 'LUSD',   'Liquity USD',                  18, 'USD', 'Liquity',           false, '2026-01-01 00:00:00', false),
    (43114, unhex('0000206329b97db379d5e1bf586bbdb969c63274'), 'USDA',   'USDA',                         18, 'USD', 'Angle',             false, '2026-01-01 00:00:00', false),
    (43114, unhex('e08b4c1005603427420e64252a8b120cace4d122'), 'BENJI',  'Franklin OnChain U.S. Government Money Fund', 18, 'USD', 'Franklin Templeton', false, '2026-01-01 00:00:00', false),
    (43114, unhex('564a341df6c126f90cf3ecb92120fd7190acb401'), 'TRYB',   'BiLira',                        6, 'TRY', 'BiLira',            false, '2026-01-01 00:00:00', false),
    (43114, unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'), 'VCHF',   'VNX Swiss Franc',              18, 'CHF', 'VNX',               false, '2026-01-01 00:00:00', false),
    (43114, unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'), 'EUROP',  'Schuman EUROP',                 6, 'EUR', 'Schuman',           false, '2026-01-01 00:00:00', false),
    (43114, unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'), 'VEUR',   'VNX Euro',                     18, 'EUR', 'VNX',               false, '2026-01-01 00:00:00', false),
    (43114, unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'), 'BRZ',    'Brazilian Digital',            18, 'BRL', 'Transfero',         false, '2026-01-01 00:00:00', false),
    (43114, unhex('27f6c8289550fce67f6b50bed1f519966afe5287'), 'tGBP',   'Tokenised GBP',                18, 'GBP', '',                  false, '2026-01-01 00:00:00', false),
    (43114, unhex('820802fa8a99901f52e39acd21177b0be6ee2974'), 'EUROe',  'EUROe Stablecoin',              6, 'EUR', 'Membrane',          false, '2026-01-01 00:00:00', false);

-- Known issuer/treasury holders to exclude from circulating supply.
-- Idempotent via fixed added_at + ReplacingMergeTree on (chain_id, token, holder).
INSERT INTO stablecoin_excluded_holders (chain_id, token, holder, reason, added_at) VALUES
    -- Tether treasury / authorized-but-not-issued reserve on C-Chain.
    -- Verified empirically: this single holder accounts for the entire delta between
    -- our totalSupply and DeFiLlama's circulating-supply figure.
    (43114, unhex('9702230a8ea53601f5cd2dc00fdbc13d4df4a8c7'), unhex('5754284f345afc66a98fbb0a0afe71e0f007b949'), 'treasury', '2026-01-01 00:00:00');
