-- Stablecoin transfer volume per token per period
-- Parameters: chain_id, first_period, last_period, granularity

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT
    {chain_id} as chain_id,
    address as token,
    'volume' as metric_name,
    '{granularity}' as granularity,
    toStartOf{granularityCamelCase}(block_time) as period,
    toString(sum(reinterpretAsUInt256(reverse(data)))) as value
FROM raw_logs
WHERE chain_id = @chain_id
  AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = @chain_id)
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
  AND block_time >= @first_period
  AND block_time < @last_period
GROUP BY address, period
ORDER BY period;

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT
    {chain_id} as chain_id,
    address as token,
    'transfers' as metric_name,
    '{granularity}' as granularity,
    toStartOf{granularityCamelCase}(block_time) as period,
    toString(count()) as value
FROM raw_logs
WHERE chain_id = @chain_id
  AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = @chain_id)
  AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
  AND length(data) = 32
  AND block_time >= @first_period
  AND block_time < @last_period
GROUP BY address, period
ORDER BY period;
