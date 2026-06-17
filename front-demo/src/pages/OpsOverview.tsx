import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { getIndexerStatus, type IndexerStatus, type EVMChainStatus } from '../lib/api';
import { useStorageStats, useEvmBlocks } from '../lib/hooks';
import { timeAgo } from '../lib/format';

const C_CHAIN = 43114;

// Human-readable byte size.
function formatBytes(n: number): string {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 2)} ${units[i]}`;
}

function formatInt(n: number | undefined): string {
  return (n ?? 0).toLocaleString();
}

interface ChainRow {
  name: string;
  indexed: number;
  tip: number;
  behind: number;
  lastSync: string;
  synced: boolean;
  to: string;
}

function toRows(status: IndexerStatus | undefined): ChainRow[] {
  if (!status) return [];
  const rows: ChainRow[] = (status.evm ?? []).map((c: EVMChainStatus) => ({
    name: c.name || `chain ${c.chain_id}`,
    indexed: c.current_block,
    tip: c.latest_block,
    behind: c.blocks_behind,
    lastSync: c.last_sync,
    synced: c.is_synced,
    to: `/chain/${c.chain_id}`,
  }));
  if (status.pchain) {
    rows.push({
      name: 'P-Chain',
      indexed: status.pchain.current_block,
      tip: status.pchain.latest_block,
      behind: status.pchain.blocks_behind,
      lastSync: status.pchain.last_sync,
      synced: status.pchain.is_synced,
      to: '/p-chain/overview',
    });
  }
  return rows;
}

function Sparkline({ values }: { values: number[] }) {
  if (values.length < 2) {
    return <div className="text-sm text-gray-400">loading…</div>;
  }
  const max = Math.max(...values, 1);
  return (
    <div className="flex items-end gap-0.5 h-16">
      {values.map((v, i) => (
        <div
          key={i}
          className="flex-1 bg-blue-500/70 rounded-sm"
          style={{ height: `${Math.max(4, (v / max) * 100)}%` }}
          title={`${v} tx`}
        />
      ))}
    </div>
  );
}

function StatCard({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: 'ok' | 'warn' | 'bad' }) {
  const border =
    tone === 'bad' ? 'border-l-red-500' : tone === 'warn' ? 'border-l-amber-500' : 'border-l-blue-500';
  return (
    <div className={`bg-white rounded-lg shadow p-4 border-l-4 ${border}`}>
      <div className="text-xs font-semibold uppercase tracking-wide text-gray-500">{label}</div>
      <div className="mt-1 text-2xl font-bold text-gray-900">{value}</div>
      {sub && <div className="text-xs text-gray-400 mt-0.5">{sub}</div>}
    </div>
  );
}

function OpsOverview() {
  const navigate = useNavigate();
  // Poll indexer status for live health + freshness. Kept at 10s (not faster)
  // so the dashboard stays well under the API's 60 req/min-per-IP limit — an
  // aggressive poll here makes the dashboard rate-limit itself (429s).
  const { data: status } = useQuery<IndexerStatus>({
    queryKey: ['indexer-status-live'],
    queryFn: ({ signal }) => getIndexerStatus(signal),
    refetchInterval: 10000,
    staleTime: 8000,
  });
  const { data: storage } = useStorageStats();
  // C-Chain is the high-volume chain; its recent blocks give live rates.
  const { data: cchainBlocks } = useEvmBlocks(C_CHAIN, 50);

  const rows = toRows(status);
  const total = rows.length;
  const okCount = rows.filter((r) => r.synced).length;
  const problems = rows.filter((r) => !r.synced || r.behind > 10);
  const totalBehind = rows.reduce((s, r) => s + Math.max(0, r.behind), 0);
  const totalBytes = (storage ?? []).reduce((s, t) => s + t.size_bytes, 0);
  const maxBytes = storage && storage.length ? storage[0].size_bytes : 0;

  // Coverage — raw_txs / raw_blocks are multi-chain, so their row counts ARE
  // the cross-chain totals (free, from the storage stats we already loaded).
  const tableRows = (name: string) => storage?.find((t) => t.table === name)?.rows ?? 0;
  const txsIndexed = tableRows('raw_txs');
  const pchainTxs = tableRows('p_chain_txs');
  const blocksIndexed = tableRows('raw_blocks');

  // Live block & tx rates, both derived from the same C-Chain block feed so
  // they're mutually consistent: rate = (count over the window) / (time the
  // window spans). This avoids the watermark-height-diff artifact that made a
  // single catch-up jump inflate the number.
  const { blockRate, txRate, perBlock } = useMemo(() => {
    const b = cchainBlocks;
    if (!b || b.length < 2) return { blockRate: null as number | null, txRate: null as number | null, perBlock: [] as number[] };
    const newest = new Date(b[0].block_time).getTime();
    const oldest = new Date(b[b.length - 1].block_time).getTime();
    const span = (newest - oldest) / 1000;
    if (span <= 0) return { blockRate: null, txRate: null, perBlock: [] };
    const txs = b.reduce((s, x) => s + x.tx_count, 0);
    return {
      blockRate: (b.length - 1) / span,
      txRate: txs / span,
      perBlock: [...b].reverse().map((x) => x.tx_count),
    };
  }, [cchainBlocks]);

  return (
    <PageTransition>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Indexer Overview</h1>
          <p className="text-sm text-gray-500">Sync health, ingestion rate, and storage at a glance.</p>
        </div>

        {/* Attention banner — only when something is wrong */}
        {problems.length > 0 && (
          <div className="bg-red-50 border border-red-200 rounded-lg p-4">
            <div className="flex items-center gap-2 text-red-700 font-semibold mb-2">
              <span className="inline-block w-2.5 h-2.5 rounded-full bg-red-500" />
              {problems.length} chain{problems.length > 1 ? 's' : ''} need attention
            </div>
            <div className="space-y-1">
              {problems.map((p) => (
                <button
                  key={p.name}
                  onClick={() => navigate(p.to)}
                  className="block w-full text-left text-sm text-red-800 hover:underline"
                >
                  <span className="font-medium">{p.name}</span> —{' '}
                  {p.behind > 0 ? `${formatInt(p.behind)} blocks behind` : 'stale'} · last sync {timeAgo(p.lastSync)}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Live ops KPIs */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <StatCard
            label="Chains synced"
            value={`${okCount} / ${total || '—'}`}
            tone={total > 0 && okCount === total ? 'ok' : 'warn'}
          />
          <StatCard
            label="Blocks behind"
            value={formatInt(totalBehind)}
            tone={totalBehind === 0 ? 'ok' : totalBehind > 100 ? 'bad' : 'warn'}
          />
          <StatCard
            label="Block rate"
            value={blockRate === null ? '…' : `${blockRate.toFixed(2)} blk/s`}
            sub="C-Chain, live"
          />
          <StatCard
            label="Tx throughput"
            value={txRate === null ? '…' : `${txRate.toFixed(1)} tx/s`}
            sub="C-Chain, live"
          />
        </div>

        {/* Coverage — what the indexer holds */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <StatCard
            label="Transactions indexed"
            value={formatInt(txsIndexed)}
            sub={pchainTxs ? `+ ${formatInt(pchainTxs)} P-Chain` : 'EVM, all chains'}
          />
          <StatCard label="Blocks indexed" value={formatInt(blocksIndexed)} sub="EVM, all chains" />
          <StatCard label="Chains indexed" value={formatInt(total)} sub="incl. P-Chain" />
          <StatCard label="Storage" value={formatBytes(totalBytes)} sub={`${formatInt(blocksIndexed + txsIndexed)}+ rows`} />
        </div>

        {/* Per-chain health */}
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-100">
            <h2 className="font-semibold text-gray-900">Per-chain health</h2>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-gray-500">
                <tr>
                  <th className="text-left font-medium px-6 py-2">Chain</th>
                  <th className="text-right font-medium px-6 py-2">Indexed</th>
                  <th className="text-right font-medium px-6 py-2">Tip</th>
                  <th className="text-right font-medium px-6 py-2">Behind</th>
                  <th className="text-right font-medium px-6 py-2">Last sync</th>
                  <th className="text-center font-medium px-6 py-2">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {rows.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-6 py-6 text-center text-gray-400">
                      Loading chain status…
                    </td>
                  </tr>
                )}
                {rows.map((r) => (
                  <tr
                    key={r.name}
                    onClick={() => navigate(r.to)}
                    className="hover:bg-blue-50 cursor-pointer"
                  >
                    <td className="px-6 py-3 font-medium text-gray-900">{r.name}</td>
                    <td className="px-6 py-3 text-right tabular-nums">{formatInt(r.indexed)}</td>
                    <td className="px-6 py-3 text-right tabular-nums text-gray-500">{formatInt(r.tip)}</td>
                    <td
                      className={`px-6 py-3 text-right tabular-nums ${r.behind > 100 ? 'text-red-600 font-semibold' : r.behind > 0 ? 'text-amber-600' : 'text-gray-500'}`}
                    >
                      {formatInt(r.behind)}
                    </td>
                    <td className="px-6 py-3 text-right text-gray-500">{timeAgo(r.lastSync)}</td>
                    <td className="px-6 py-3 text-center">
                      <span
                        className={`inline-block w-2.5 h-2.5 rounded-full ${r.synced ? 'bg-green-500' : 'bg-red-500'}`}
                        title={r.synced ? 'synced' : 'behind / stale'}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        {/* Block activity + storage */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div className="bg-white rounded-lg shadow p-6">
            <h2 className="font-semibold text-gray-900 mb-1">Recent block activity</h2>
            <p className="text-xs text-gray-400 mb-3">
              {blockRate === null
                ? 'loading…'
                : `${blockRate.toFixed(2)} blk/s · ${txRate?.toFixed(1)} tx/s · tx per block`}
            </p>
            <Sparkline values={perBlock} />
          </div>

          <div className="bg-white rounded-lg shadow p-6">
            <h2 className="font-semibold text-gray-900 mb-3">Storage by table</h2>
            <div className="space-y-2">
              {(storage ?? []).slice(0, 8).map((t) => (
                <div key={t.table}>
                  <div className="flex justify-between text-xs text-gray-600 mb-0.5">
                    <span className="font-mono">{t.table}</span>
                    <span>
                      {formatBytes(t.size_bytes)} · {formatInt(t.rows)} rows
                    </span>
                  </div>
                  <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
                    <div
                      className="h-full bg-indigo-500/70"
                      style={{ width: `${maxBytes ? (t.size_bytes / maxBytes) * 100 : 0}%` }}
                    />
                  </div>
                </div>
              ))}
              {(!storage || storage.length === 0) && (
                <div className="text-sm text-gray-400">Loading storage…</div>
              )}
            </div>
          </div>
        </div>
      </div>
    </PageTransition>
  );
}

export default OpsOverview;
