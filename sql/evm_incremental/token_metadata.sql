-- Token Metadata Table
-- Stores ERC-20 token name, symbol, and decimals
-- Populated by Go code making RPC calls to token contracts

CREATE TABLE IF NOT EXISTS token_metadata (
    chain_id UInt32,
    token FixedString(20),
    name String,
    symbol String,
    decimals UInt8,
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (chain_id, token);
