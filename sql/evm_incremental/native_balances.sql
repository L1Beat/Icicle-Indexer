-- Native Token Balance Tracking (AVAX, ETH, etc.)
-- Stage 1: native_balance_changes table (stores diffs per block range)
-- Stage 2: native_balances view (aggregates all diffs)

-- ========================================================================
-- STAGE 1: CREATE BALANCE CHANGES TABLE
-- ========================================================================

CREATE TABLE IF NOT EXISTS native_balance_changes (
    chain_id UInt32,
    wallet FixedString(20),
    from_block UInt32,
    to_block UInt32,
    deposits UInt256,      -- incoming value from traces
    withdrawals UInt256,   -- outgoing value from traces
    gas_spent UInt256,     -- gas fees paid (only for tx senders)
    computed_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(computed_at)
ORDER BY (chain_id, wallet, from_block, to_block);

-- ========================================================================
-- STAGE 2: CREATE BALANCE VIEW
-- ========================================================================

CREATE OR REPLACE VIEW native_balances AS
SELECT
    chain_id,
    wallet,
    sum(deposits) as total_in,
    sum(withdrawals) as total_out,
    sum(gas_spent) as total_gas,
    toInt256(sum(deposits)) - toInt256(sum(withdrawals)) - toInt256(sum(gas_spent)) as balance,
    max(to_block) as last_updated_block
FROM native_balance_changes FINAL
GROUP BY chain_id, wallet;

-- ========================================================================
-- INSERT: Process balance changes for block range
-- ========================================================================

INSERT INTO native_balance_changes (chain_id, wallet, from_block, to_block, deposits, withdrawals, gas_spent)
SELECT
    @chain_id as chain_id,
    wallet,
    @from_block as from_block,
    @to_block as to_block,
    sum(deposit_amount) as deposits,
    sum(withdrawal_amount) as withdrawals,
    sum(gas_amount) as gas_spent
FROM (
    -- ========================================================================
    -- INCOMING VALUE (DEPOSITS) from traces
    -- ========================================================================
    -- Value received via CALL or as contract creation
    SELECT
        to as wallet,
        value as deposit_amount,
        toUInt256(0) as withdrawal_amount,
        toUInt256(0) as gas_amount
    FROM raw_traces
    WHERE chain_id = @chain_id
      AND block_number >= @from_block
      AND block_number <= @to_block
      AND to IS NOT NULL
      AND value > 0
      AND tx_success = true
      AND call_type IN ('CALL', 'CREATE', 'CREATE2')

    UNION ALL

    -- ========================================================================
    -- OUTGOING VALUE (WITHDRAWALS) from traces
    -- ========================================================================
    -- Value sent via CALL or contract creation
    SELECT
        from as wallet,
        toUInt256(0) as deposit_amount,
        value as withdrawal_amount,
        toUInt256(0) as gas_amount
    FROM raw_traces
    WHERE chain_id = @chain_id
      AND block_number >= @from_block
      AND block_number <= @to_block
      AND value > 0
      AND tx_success = true
      AND call_type IN ('CALL', 'CREATE', 'CREATE2')

    UNION ALL

    -- ========================================================================
    -- GAS COSTS from transactions
    -- ========================================================================
    -- Gas is always paid by tx sender, regardless of success
    SELECT
        from as wallet,
        toUInt256(0) as deposit_amount,
        toUInt256(0) as withdrawal_amount,
        toUInt256(gas_used) * toUInt256(gas_price) as gas_amount
    FROM raw_txs
    WHERE chain_id = @chain_id
      AND block_number >= @from_block
      AND block_number <= @to_block
) transfers
WHERE wallet != unhex('0000000000000000000000000000000000000000')
GROUP BY wallet
HAVING deposits > 0 OR withdrawals > 0 OR gas_spent > 0;
