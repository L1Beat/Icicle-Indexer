-- Stablecoin CIRCULATING-supply change per token per period.
--
-- Circulating supply = balances of all real, non-excluded holders (i.e. excludes
-- issuer treasuries listed in stablecoin_excluded_holders). This matches the
-- /stablecoins list endpoint methodology exactly, so the cumulative series'
-- latest value reconciles with the live list `supply` for every token.
--
-- We store the net per-period CHANGE in circulating supply; the API computes the
-- cumulative running total at query time via a window function.
--
-- Net circulating change in a period =
--     (amount RECEIVED by non-excluded holders)   -- topic2 = `to`
--   - (amount SENT by non-excluded holders)        -- topic1 = `from`
--
-- This correctly handles every supply movement, not just zero-address mint/burn:
--   * mint   (from 0x0  -> wallet)      => +amount  (received by a real wallet)
--   * burn   (wallet    -> 0x0)         => -amount  (sent by a real wallet)
--   * wallet -> excluded treasury       => -amount  (leaves circulation)
--   * excluded treasury -> wallet       => +amount  (enters circulation)
--   * wallet -> wallet (both included)  =>  0
-- The old mint/burn-only method was blind to treasury movements, which is why
-- e.g. USDT froze at total-minted ($1.85B) instead of circulating ($414M).
--
-- Parameters: chain_id, first_period, last_period, granularity

INSERT INTO stablecoin_metrics (chain_id, token, metric_name, granularity, period, value)
SELECT
    {chain_id} as chain_id,
    token,
    'supply_change' as metric_name,
    '{granularity}' as granularity,
    period,
    toString(sum(delta)) as value
FROM (
    -- Incoming to non-excluded holders: +amount
    SELECT
        address as token,
        toStartOf{granularityCamelCase}(block_time) as period,
        toInt256(reinterpretAsUInt256(reverse(data))) as delta
    FROM raw_logs
    WHERE chain_id = @chain_id
      AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = @chain_id)
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32
      AND block_time >= @first_period
      AND block_time < @last_period
      AND topic2 IS NOT NULL
      AND substring(topic2, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic2, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
      )

    UNION ALL

    -- Outgoing from non-excluded holders: -amount
    SELECT
        address as token,
        toStartOf{granularityCamelCase}(block_time) as period,
        -toInt256(reinterpretAsUInt256(reverse(data))) as delta
    FROM raw_logs
    WHERE chain_id = @chain_id
      AND address IN (SELECT token FROM stablecoins FINAL WHERE chain_id = @chain_id)
      AND topic0 = unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef')
      AND length(data) = 32
      AND block_time >= @first_period
      AND block_time < @last_period
      AND topic1 IS NOT NULL
      AND substring(topic1, 13, 20) != unhex('0000000000000000000000000000000000000000')
      AND (address, toFixedString(substring(topic1, 13, 20), 20)) NOT IN (
          SELECT token, holder FROM stablecoin_excluded_holders FINAL WHERE chain_id = @chain_id
      )
)
GROUP BY token, period
ORDER BY period;
