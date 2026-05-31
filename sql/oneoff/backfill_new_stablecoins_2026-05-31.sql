-- One-time historical backfill for stablecoins added/fixed on 2026-05-31:
-- the 30 newly-added tokens + BUIDL (0x53fc82...), which previously only had
-- forward data. Restricted to these 31 tokens so the existing 18 tokens'
-- already-correct series are never touched.
--
-- Backfills supply_change / volume / transfers at day, week and month
-- granularity, computed historically from raw_logs (same logic as
-- sql/evm_metrics/stablecoin_supply.sql and stablecoin_volume.sql).
-- 'holders' is intentionally NOT backfilled: it is a forward-only point-in-time
-- snapshot and cannot be reconstructed from current balances.
--
-- Idempotent: stablecoin_metrics is ReplacingMergeTree on
-- (chain_id, token, metric_name, granularity, period); re-running dedupes.
-- Run with: docker exec -i icicle-clickhouse clickhouse-client --password "$CH_PW" -n < this_file

-- ============================ SUPPLY (day) ============================
INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'day', period, toString(sum(delta))
FROM (
    SELECT address AS token, toStartOfDay(block_time) AS period,
           toInt256(reinterpretAsUInt256(reverse(data))) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32 AND topic2 IS NOT NULL
      AND substring(topic2, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic2, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
    UNION ALL
    SELECT address AS token, toStartOfDay(block_time) AS period,
           -toInt256(reinterpretAsUInt256(reverse(data))) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32 AND topic1 IS NOT NULL
      AND substring(topic1, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic1, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
)
GROUP BY token, period;

-- ============================ SUPPLY (week) ============================
INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'week', period, toString(sum(delta))
FROM (
    SELECT address AS token, toStartOfWeek(block_time) AS period,
           toInt256(reinterpretAsUInt256(reverse(data))) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32 AND topic2 IS NOT NULL
      AND substring(topic2, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic2, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
    UNION ALL
    SELECT address AS token, toStartOfWeek(block_time) AS period,
           -toInt256(reinterpretAsUInt256(reverse(data))) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32 AND topic1 IS NOT NULL
      AND substring(topic1, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic1, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
)
GROUP BY token, period;

-- ============================ SUPPLY (month) ============================
INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, token, 'supply_change', 'month', period, toString(sum(delta))
FROM (
    SELECT address AS token, toStartOfMonth(block_time) AS period,
           toInt256(reinterpretAsUInt256(reverse(data))) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32 AND topic2 IS NOT NULL
      AND substring(topic2, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic2, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
    UNION ALL
    SELECT address AS token, toStartOfMonth(block_time) AS period,
           -toInt256(reinterpretAsUInt256(reverse(data))) AS delta
    FROM raw_logs
    WHERE chain_id = 43114
      AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32 AND topic1 IS NOT NULL
      AND substring(topic1, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic1, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = 43114)
)
GROUP BY token, period;

-- ============================ VOLUME + TRANSFERS (day) ============================
INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address, 'volume', 'day', toStartOfDay(block_time), toString(sum(reinterpretAsUInt256(reverse(data))))
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
GROUP BY address, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address, 'transfers', 'day', toStartOfDay(block_time), toString(count())
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
GROUP BY address, period;

-- ============================ VOLUME + TRANSFERS (week) ============================
INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address, 'volume', 'week', toStartOfWeek(block_time), toString(sum(reinterpretAsUInt256(reverse(data))))
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
GROUP BY address, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address, 'transfers', 'week', toStartOfWeek(block_time), toString(count())
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
GROUP BY address, period;

-- ============================ VOLUME + TRANSFERS (month) ============================
INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address, 'volume', 'month', toStartOfMonth(block_time), toString(sum(reinterpretAsUInt256(reverse(data))))
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
GROUP BY address, period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT 43114, address, 'transfers', 'month', toStartOfMonth(block_time), toString(count())
FROM raw_logs
WHERE chain_id = 43114
  AND address IN (unhex('9ee1963f05553ef838604dd39403be21cef26aa4'),unhex('d24c2ad096400b6fbcd2ad8b24e7acbc21a1da64'),unhex('abe7a9dfda35230ff60d1590a929ae0644c47dc1'),unhex('9f6714c302ffe3c3bafaf2ccb44201ff64f6371c'),unhex('80eede496655fb9047dd39d9f418d5483ed600df'),unhex('5d3a1ff2b6bab83b63cd9ad0787074081a52ef34'),unhex('cfc37a6ab183dd4aed08c204d1c2773c0b1bdf46'),unhex('025ab35ff6abcca56d57475249baaeae08419039'),unhex('221743dc9e954be4f86844649bf19b43d6f8366d'),unhex('323665443cef804a3b5206103304bd4872ea4253'),unhex('853ea32391aaa14c112c645fd20ba389ab25c5e0'),unhex('e80772eaf6e2e18b651f160bc9158b2a5cafca65'),unhex('0f577433bf59560ef2a79c124e9ff99fca258948'),unhex('f9fb20b8e097904f0ab7d12e9dbee88f2dcd0f16'),unhex('09056fc62d9e1cff4bb5ceac4d7be6f420450647'),unhex('03569cc076654f82679c4ba2124d64774781b01d'),unhex('dbdd50997361522495ecfe57ebb6850da0e4c699'),unhex('cc18b41a0f63c67f17f23388c848aec67b583422'),unhex('7dc9748da8e762e569f9269f48f69a1a9f8ea761'),unhex('da0019e7e50ee4990440b1aa5dffcac6e27ee27b'),unhex('0000206329b97db379d5e1bf586bbdb969c63274'),unhex('e08b4c1005603427420e64252a8b120cace4d122'),unhex('b57b25851fe2311cc3fe511c8f10e868932e0680'),unhex('564a341df6c126f90cf3ecb92120fd7190acb401'),unhex('228a48df6819ccc2eca01e2192ebafffdad56c19'),unhex('8835a2f66a7aaccb297cb985831a616b75e2e16c'),unhex('7678e162f38ec9ef2bfd1d0aaf9fd93355e5fa0b'),unhex('05539f021b66fd01d1fb1ff8e167cdd09bf7c2d0'),unhex('27f6c8289550fce67f6b50bed1f519966afe5287'),unhex('820802fa8a99901f52e39acd21177b0be6ee2974'),unhex('53fc82f14f009009b440a706e31c9021e1196a2f'))
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
GROUP BY address, period;
