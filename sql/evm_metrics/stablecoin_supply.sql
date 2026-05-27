-- Stablecoin net supply change per token per period (mints - burns)
-- Parameters: chain_id, first_period, last_period, granularity
-- Cumulative supply is computed at query time via window function.

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT
    {chain_id} as chain_id,
    address as token,
    'supply_change' as metric_name,
    '{granularity}' as granularity,
    toStartOf{granularityCamelCase}(block_time) as period,
    toString(
        sum(CASE
            WHEN substring(topic1, 13, 20) = unhex('0000000000000000000000000000000000000000')
            THEN toInt256(reinterpretAsUInt256(reverse(data)))
            ELSE toInt256(0)
        END)
        -
        sum(CASE
            WHEN substring(topic2, 13, 20) = unhex('0000000000000000000000000000000000000000')
            THEN toInt256(reinterpretAsUInt256(reverse(data)))
            ELSE toInt256(0)
        END)
    ) as value
FROM raw_logs
WHERE chain_id = @chain_id
  AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = @chain_id)
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
  AND block_time >= @first_period
  AND block_time < @last_period
  AND (
      substring(topic1, 13, 20) = unhex('0000000000000000000000000000000000000000')
      OR substring(topic2, 13, 20) = unhex('0000000000000000000000000000000000000000')
  )
GROUP BY address, period
ORDER BY period;
