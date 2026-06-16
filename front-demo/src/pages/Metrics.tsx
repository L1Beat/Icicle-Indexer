import { useParams, useNavigate } from 'react-router-dom';
import { useMemo } from 'react';
import PageTransition from '../components/PageTransition';
import MetricChart from '../components/MetricChart';
import {
  useIndexerStatus,
  useAvailableMetrics,
  useMetricSeries,
} from '../lib/hooks';
import type { Granularity } from '../lib/api';

type TimePeriod = '24h' | '7d' | '30d' | '90d' | '1y';

interface TimePeriodConfig {
  value: TimePeriod;
  label: string;
  granularity: Granularity;
  /** Window length in milliseconds, used to derive the `from` query param. */
  windowMs: number;
}

const DAY = 24 * 60 * 60 * 1000;

const TIME_PERIODS: TimePeriodConfig[] = [
  { value: '24h', label: '24 Hours', granularity: 'hour', windowMs: DAY },
  { value: '7d', label: '7 Days', granularity: 'hour', windowMs: 7 * DAY },
  { value: '30d', label: '30 Days', granularity: 'day', windowMs: 30 * DAY },
  { value: '90d', label: '90 Days', granularity: 'day', windowMs: 90 * DAY },
  { value: '1y', label: '1 Year', granularity: 'day', windowMs: 365 * DAY },
];

function Metrics() {
  const { chainId, timePeriod } = useParams<{ chainId: string; timePeriod: string }>();
  const navigate = useNavigate();

  const selectedChainId = chainId ? parseInt(chainId) : 43114;
  const selectedTimePeriod = (timePeriod as TimePeriod) || '7d';
  const periodConfig =
    TIME_PERIODS.find((p) => p.value === selectedTimePeriod) || TIME_PERIODS[1];

  // Chain list + per-chain sync status come from the indexer status endpoint.
  const { data: status, isLoading: chainsLoading, error: chainsError } = useIndexerStatus();
  const chains = useMemo(
    () => (status?.evm ?? []).filter((c) => c.chain_id !== 0),
    [status],
  );

  // Available metrics for the selected chain, filtered to this granularity.
  const { data: allMetrics, isLoading: metricsLoading } = useAvailableMetrics(selectedChainId);
  const metricNames = useMemo(
    () =>
      (allMetrics ?? [])
        .filter((m) => m.granularities.includes(periodConfig.granularity))
        .map((m) => m.metric_name),
    [allMetrics, periodConfig.granularity],
  );

  // `from` is computed once per render window; the API accepts RFC3339.
  const from = useMemo(
    () => new Date(Date.now() - periodConfig.windowMs).toISOString(),
    [periodConfig.windowMs],
  );

  return (
    <PageTransition>
      <div className="p-8 space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-3xl font-bold text-gray-900">EVM Metrics</h1>
        </div>

        {/* Compact filters */}
        <div className="bg-white rounded-lg shadow p-4">
          <div className="flex flex-col lg:flex-row gap-4 items-start lg:items-center">
            {/* Chain Selector */}
            <div className="flex items-center gap-3 min-w-0 flex-shrink-0">
              <label className="text-sm font-semibold text-gray-700 whitespace-nowrap">Chain:</label>
              {chainsLoading && <p className="text-sm text-gray-500">Loading...</p>}
              {chainsError && <p className="text-sm text-red-600">Error loading chains</p>}
              {chains.length > 0 && (
                <select
                  value={selectedChainId}
                  onChange={(e) => navigate(`/evm-metrics/${e.target.value}/${selectedTimePeriod}`)}
                  className="px-3 py-2 border border-gray-300 rounded-lg text-sm font-medium text-gray-900 bg-white hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500 cursor-pointer"
                >
                  {chains.map((chain) => {
                    const syncPct =
                      chain.latest_block > 0
                        ? (chain.current_block / chain.latest_block) * 100
                        : 100;
                    const needsWarning = syncPct < 99.9;
                    return (
                      <option key={chain.chain_id} value={chain.chain_id}>
                        {chain.name} (ID: {chain.chain_id})
                        {needsWarning ? ` - ${syncPct.toFixed(1)}% synced` : ''}
                      </option>
                    );
                  })}
                </select>
              )}
            </div>

            {/* Time Period Pills */}
            <div className="flex items-center gap-3 flex-wrap flex-1">
              <label className="text-sm font-semibold text-gray-700 whitespace-nowrap">Period:</label>
              <div className="flex gap-2 flex-wrap">
                {TIME_PERIODS.map((period) => {
                  const isSelected = selectedTimePeriod === period.value;
                  return (
                    <button
                      key={period.value}
                      onClick={() => navigate(`/evm-metrics/${selectedChainId}/${period.value}`)}
                      className={`px-4 py-1.5 rounded-full text-sm font-medium transition-all cursor-pointer ${isSelected
                        ? 'bg-blue-500 text-white shadow-sm'
                        : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                        }`}
                    >
                      {period.label}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        </div>

        {/* Metrics Charts */}
        <div className="space-y-6">
          {metricsLoading && metricNames.length === 0 && (
            <div className="bg-white rounded-lg shadow p-6">
              <p className="text-gray-500">Loading metrics...</p>
            </div>
          )}

          {!metricsLoading && metricNames.length === 0 && (
            <div className="bg-white rounded-lg shadow p-6">
              <p className="text-gray-500">No metrics data available for this chain and granularity.</p>
            </div>
          )}

          {metricNames.map((metricName) => (
            <MetricChartLoader
              key={`${selectedChainId}-${periodConfig.value}-${metricName}`}
              metricName={metricName}
              chainId={selectedChainId}
              granularity={periodConfig.granularity}
              from={from}
            />
          ))}
        </div>
      </div>
    </PageTransition>
  );
}

// Loads a single metric series independently so each chart streams in on its own.
function MetricChartLoader({
  metricName,
  chainId,
  granularity,
  from,
}: {
  metricName: string;
  chainId: number;
  granularity: Granularity;
  from: string;
}) {
  const { data, isLoading, error } = useMetricSeries(chainId, metricName, granularity, from);

  if (isLoading) {
    return (
      <div className="bg-white rounded-lg shadow p-6">
        <div className="animate-pulse">
          <div className="h-4 bg-gray-200 rounded w-1/4 mb-4"></div>
          <div className="h-64 bg-gray-100 rounded"></div>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-white rounded-lg shadow p-6">
        <p className="text-red-600">Error loading {metricName}: {error.message}</p>
      </div>
    );
  }

  if (!data || data.data.length === 0) {
    return null;
  }

  // MetricChart only reads period + value, but its prop type requires the full
  // metric shape — build it from the API points.
  const chartData = data.data.map((p) => ({
    chain_id: chainId,
    metric_name: metricName,
    granularity,
    period: p.period,
    value: p.value,
    computed_at: p.period,
  }));

  return (
    <div className="bg-white rounded-lg shadow overflow-hidden">
      <MetricChart metricName={metricName} data={chartData} granularity={granularity} />
    </div>
  );
}

export default Metrics;
