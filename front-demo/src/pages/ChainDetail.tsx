import { useParams, Link } from 'react-router-dom';
import { Boxes, BarChart3, ArrowRight } from 'lucide-react';
import PageTransition from '../components/PageTransition';
import { useChainStats, useIndexerStatus, useEvmBlocks } from '../lib/hooks';
import { shortHash, shortAddr, timeAgo, formatTimestamp, formatGwei } from '../lib/format';

function formatInt(n: number | undefined): string {
  return (n ?? 0).toLocaleString();
}

function StatCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="bg-white rounded-lg shadow p-4 border-l-4 border-l-blue-500">
      <div className="text-xs font-semibold uppercase tracking-wide text-gray-500">{label}</div>
      <div className="mt-1 text-2xl font-bold text-gray-900">{value}</div>
      {sub && <div className="text-xs text-gray-400 mt-0.5">{sub}</div>}
    </div>
  );
}

function ChainDetail() {
  const { chainId } = useParams<{ chainId: string }>();
  const id = chainId ? parseInt(chainId, 10) : 43114;

  const { data: stats, isLoading: statsLoading, error: statsError } = useChainStats(id);
  const { data: status } = useIndexerStatus();
  const { data: blocks } = useEvmBlocks(id, 10);

  const sync = status?.evm?.find((c) => c.chain_id === id);
  const name = stats?.chain_name || sync?.name || `Chain ${id}`;

  return (
    <PageTransition>
      <div className="space-y-6">
        <div className="flex items-center justify-between flex-wrap gap-3">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">{name}</h1>
            <p className="text-sm text-gray-500">Chain ID {id}</p>
          </div>
          <div className="flex gap-2">
            <Link
              to={`/evm/${id}/blocks`}
              className="flex items-center gap-2 px-4 py-2 rounded-lg bg-gray-900 text-white text-sm font-medium hover:bg-gray-800"
            >
              <Boxes size={16} /> Explore blocks
            </Link>
            <Link
              to={`/evm-metrics/${id}/7d`}
              className="flex items-center gap-2 px-4 py-2 rounded-lg bg-white border border-gray-300 text-gray-700 text-sm font-medium hover:bg-gray-50"
            >
              <BarChart3 size={16} /> Metrics
            </Link>
          </div>
        </div>

        {statsError && (
          <div className="bg-white rounded-lg shadow p-6 text-red-600">
            Failed to load chain stats: {statsError.message}
          </div>
        )}

        {/* Sync status */}
        {sync && (
          <div className="bg-white rounded-lg shadow p-4 flex flex-wrap items-center gap-x-8 gap-y-2">
            <div className="flex items-center gap-2">
              <span
                className={`inline-block w-2.5 h-2.5 rounded-full ${sync.is_synced ? 'bg-green-500' : 'bg-red-500'}`}
              />
              <span className="font-medium text-gray-900">{sync.is_synced ? 'Synced' : 'Behind / stale'}</span>
            </div>
            <div className="text-sm text-gray-500">
              Indexed <span className="font-semibold text-gray-900 tabular-nums">{formatInt(sync.current_block)}</span>
              {' '}/ tip <span className="tabular-nums">{formatInt(sync.latest_block)}</span>
            </div>
            <div className="text-sm text-gray-500">
              Behind{' '}
              <span className={`font-semibold tabular-nums ${sync.blocks_behind > 100 ? 'text-red-600' : sync.blocks_behind > 0 ? 'text-amber-600' : 'text-gray-900'}`}>
                {formatInt(sync.blocks_behind)}
              </span>
            </div>
            <div className="text-sm text-gray-500">Last sync {timeAgo(sync.last_sync)}</div>
          </div>
        )}

        {/* What's indexed */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <StatCard label="Total blocks" value={statsLoading ? '…' : formatInt(stats?.total_blocks)} />
          <StatCard label="Total transactions" value={statsLoading ? '…' : formatInt(stats?.total_txs)} />
          <StatCard
            label="Avg block time"
            value={statsLoading ? '…' : `${(stats?.avg_block_time_seconds ?? 0).toFixed(2)}s`}
            sub="last 1000 blocks"
          />
          <StatCard
            label="Avg gas / block"
            value={statsLoading ? '…' : formatInt(Math.round(stats?.avg_gas_used ?? 0))}
            sub={stats?.last_block_time ? `tip ${timeAgo(stats.last_block_time)}` : undefined}
          />
        </div>

        {/* Recent blocks */}
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-100 flex items-center justify-between">
            <h2 className="font-semibold text-gray-900">Recent blocks</h2>
            <Link to={`/evm/${id}/blocks`} className="text-sm text-blue-600 hover:underline flex items-center gap-1">
              View all <ArrowRight size={14} />
            </Link>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-gray-500">
                <tr>
                  <th className="text-left font-medium px-6 py-2">Block</th>
                  <th className="text-left font-medium px-6 py-2">Hash</th>
                  <th className="text-right font-medium px-6 py-2">Txs</th>
                  <th className="text-right font-medium px-6 py-2">Base fee</th>
                  <th className="text-left font-medium px-6 py-2">Miner</th>
                  <th className="text-right font-medium px-6 py-2">Age</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {(blocks ?? []).map((b) => (
                  <tr key={b.block_number} className="hover:bg-gray-50">
                    <td className="px-6 py-3">
                      <Link to={`/evm/${id}/block/${b.block_number}`} className="text-blue-600 font-mono hover:underline">
                        {formatInt(b.block_number)}
                      </Link>
                    </td>
                    <td className="px-6 py-3 font-mono text-gray-500" title={b.hash}>{shortHash(b.hash)}</td>
                    <td className="px-6 py-3 text-right tabular-nums">{b.tx_count}</td>
                    <td className="px-6 py-3 text-right tabular-nums text-gray-500">{formatGwei(b.base_fee_per_gas)}</td>
                    <td className="px-6 py-3 font-mono text-gray-500">
                      <Link to={`/evm/${id}/address/${b.miner}`} className="hover:underline">{shortAddr(b.miner)}</Link>
                    </td>
                    <td className="px-6 py-3 text-right text-gray-500">{timeAgo(b.block_time)}</td>
                  </tr>
                ))}
                {(!blocks || blocks.length === 0) && (
                  <tr>
                    <td colSpan={6} className="px-6 py-6 text-center text-gray-400">No recent blocks.</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>

        <p className="text-xs text-gray-400">{formatTimestamp(stats?.last_block_time)} · last indexed block time</p>
      </div>
    </PageTransition>
  );
}

export default ChainDetail;
