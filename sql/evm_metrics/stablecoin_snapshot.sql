-- Stablecoin daily holder count snapshot (point-in-time)
-- Parameters: chain_id, first_period, last_period, granularity
-- Only runs for daily granularity (enforced by _snapshot suffix convention)

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT
    @chain_id as chain_id,
    token,
    'holders' as metric_name,
    '{granularity}' as granularity,
    toStartOf{granularityCamelCase}(@last_period - INTERVAL 1 DAY) as period,
    toString(holder_count) as value
FROM (
    SELECT
        token,
        countIf(balance > toInt256(0)) as holder_count
    FROM erc20_balances
    WHERE chain_id = @chain_id
      AND token IN (SELECT token FROM stablecoins FINAL WHERE chain_id = @chain_id)
      AND (token, wallet) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
      )
    GROUP BY token
)
SETTINGS max_memory_usage = 8000000000;
