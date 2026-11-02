-- Stablecoin transfer volume and transfer count per token per period, without
-- issuer operations (see the filter below), so a treasury burn or mint does not
-- show up as trading activity.
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
  -- Issuer operations are not volume: mints, burns, and moves to or from an
  -- excluded issuer treasury (stablecoin_excluded_holders).
  AND substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
  )
  AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
  )
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
  -- Issuer operations are not volume: mints, burns, and moves to or from an
  -- excluded issuer treasury (stablecoin_excluded_holders).
  AND substring(assumeNotNull(topic1), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND substring(assumeNotNull(topic2), 13, 20) != unhex('0000000000000000000000000000000000000000')
  AND (address, toFixedString(substring(assumeNotNull(topic1), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
  )
  AND (address, toFixedString(substring(assumeNotNull(topic2), 13, 20), 20)) NOT IN (
      SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
  )
GROUP BY address, period
ORDER BY period;
