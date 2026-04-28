-- Active senders metric (unique transaction senders per period)
-- Parameters: chain_id, first_period, last_period, granularity

INSERT INTO metrics (chain_id, metric_name, granularity, period, value)
SELECT
    {chain_id} as chain_id,
    'active_senders' as metric_name,
    '{granularity}' as granularity,
    toStartOf{granularityCamelCase}(block_time) as period,
    uniq(`from`) as value
FROM raw_txs
WHERE chain_id = @chain_id
  AND block_time >= @first_period
  AND block_time < @last_period
GROUP BY period
ORDER BY period;
