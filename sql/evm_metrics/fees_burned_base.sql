-- Base-fee portion of the transaction fee burned per period
-- gas_used * base_fee_per_gas; the rest (gas_price - base_fee) is the priority tip
-- Stored in nAVAX (1 nAVAX = 1e9 wei) to stay inside UInt64; wei = value * 1e9
-- UInt256 sum + intDiv avoids overflow and floating-point rounding
-- Parameters: chain_id, first_period, last_period, granularity
INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT
    {chain_id} as chain_id,
    'fees_burned_base' as metric_name,
    '{granularity}' as granularity,
    toStartOf{granularityCamelCase}(block_time) as period,
    toUInt64(intDiv(sum(toUInt256(gas_used) * toUInt256(base_fee_per_gas)), 1000000000)) as value
FROM raw_txs
WHERE chain_id = @chain_id
  AND block_time >= @first_period
  AND block_time < @last_period
GROUP BY period
ORDER BY period;
