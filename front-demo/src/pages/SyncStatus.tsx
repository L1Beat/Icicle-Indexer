import PageTransition from '../components/PageTransition';
import { RefreshCw } from 'lucide-react';
import { useMemo } from 'react';
import { useIndexerStatus, useStorageStats } from '../lib/hooks';

/** Per-chain sync row, derived from the indexer status API. */
interface ChainSyncData {
  chain_id: number;
  name: string;
  /** RFC3339 timestamp of the last sync. */
  last_updated: string;
  last_block_on_chain: number;
  watermark_block: number | null;
  syncPercentage: number;
  blocksBehind: number | null;
}

function SyncStatus() {
  const {
    data: indexerStatus,
    isLoading,
    error,
    refetch,
    isFetching,
  } = useIndexerStatus();

  const {
    data: tableSizes,
    isLoading: isLoadingTables,
    error: tableSizesError,
    refetch: refetchStorage,
  } = useStorageStats();

  // Derive the per-chain sync rows from the EVM chain statuses (plus the
  // optional P-Chain status). The API does not give the P-Chain a chain_id or
  // name, so we synthesize them for display purposes only.
  const chains = useMemo<ChainSyncData[] | undefined>(() => {
    if (!indexerStatus) return undefined;

    const evmChains: ChainSyncData[] = indexerStatus.evm.map((chain) => ({
      chain_id: chain.chain_id,
      name: chain.name,
      last_updated: chain.last_sync,
      last_block_on_chain: chain.latest_block,
      watermark_block: chain.current_block,
      syncPercentage:
        chain.latest_block > 0
          ? (chain.current_block / chain.latest_block) * 100
          : 100,
      blocksBehind: chain.blocks_behind,
    }));

    if (indexerStatus.pchain) {
      const p = indexerStatus.pchain;
      evmChains.push({
        chain_id: 0,
        name: 'P-Chain',
        last_updated: p.last_sync,
        last_block_on_chain: p.latest_block,
        watermark_block: p.current_block,
        syncPercentage:
          p.latest_block > 0 ? (p.current_block / p.latest_block) * 100 : 100,
        blocksBehind: p.blocks_behind,
      });
    }

    return evmChains;
  }, [indexerStatus]);

  const getBlocksBehindHealth = (blocksBehind: number | null) => {
    if (blocksBehind === null) return 'gray';
    if (blocksBehind < 10) return 'green';
    if (blocksBehind < 1000) return 'yellow';
    return 'red';
  };

  const getLastUpdatedHealth = (lastSync: string) => {
    const diffSec = (Date.now() - new Date(lastSync).getTime()) / 1000;

    if (diffSec < 60) return 'green';  // < 1 minute
    if (diffSec < 3600) return 'yellow';  // < 1 hour
    return 'red';  // > 1 hour
  };

  const formatTimestamp = (lastSync: string) => {
    const date = new Date(lastSync);
    const diffSec = Math.floor((Date.now() - date.getTime()) / 1000);
    const diffMin = Math.floor(diffSec / 60);
    const diffHour = Math.floor(diffMin / 60);

    if (diffSec < 60) return `${diffSec}s ago`;
    if (diffMin < 60) return `${diffMin}m ago`;
    if (diffHour < 24) return `${diffHour}h ago`;
    return date.toLocaleString();
  };

  const getHealthDot = (health: string) => {
    const colors = {
      green: 'bg-green-500',
      yellow: 'bg-yellow-500',
      red: 'bg-red-500',
      gray: 'bg-gray-400',
    };
    return colors[health as keyof typeof colors] || colors.gray;
  };

  const formatBytes = (bytes: number): string => {
    const gb = bytes / (1024 * 1024 * 1024);
    return `${gb.toFixed(3)} GB`;
  };

  const formatBytesPerRow = (bytes: number): string => {
    if (bytes < 1024) return `${bytes.toFixed(0)} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(2)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  };

  const totalRows = tableSizes?.reduce((sum, table) => sum + table.rows, 0) || 0;
  const totalBytes = tableSizes?.reduce((sum, table) => sum + table.size_bytes, 0) || 0;

  const rawTxsTable = tableSizes?.find(table => table.table === 'raw_txs');
  const rawTxsCount = rawTxsTable?.rows || 0;

  // Calculate GB per 1B transactions
  const gbPer1BTxs = rawTxsCount > 0
    ? (totalBytes / rawTxsCount) * 1_000_000_000 / (1024 * 1024 * 1024)
    : 0;

  // P-Chain transaction count, derived from the storage stats.
  const pChainTxCount = tableSizes?.find(table => table.table === 'p_chain_txs')?.rows || 0;

  const handleRefresh = () => {
    refetch();
    refetchStorage();
  };

  return (
    <PageTransition>
      <div className="p-8 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-gray-900">Sync Status</h1>
            {chains && (
              <p className="text-sm text-gray-600 mt-1">
                Monitoring {chains.length} chain{chains.length !== 1 ? 's' : ''}
              </p>
            )}
          </div>
          <button
            onClick={handleRefresh}
            disabled={isFetching}
            className="p-2 text-gray-500 hover:text-gray-700 hover:bg-gray-100 rounded-lg transition-colors disabled:opacity-50 cursor-pointer"
            title="Refresh"
          >
            <RefreshCw size={20} className={isFetching ? 'animate-spin' : ''} />
          </button>
        </div>

        {/* Storage Efficiency Metric */}
        {tableSizes && rawTxsCount > 0 && (
          <div className="bg-gradient-to-r from-blue-50 to-indigo-50 border border-blue-200 rounded-lg p-6">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="text-sm font-semibold text-gray-700 uppercase tracking-wider mb-1">
                  Storage Efficiency
                </h3>
                <p className="text-3xl font-bold text-gray-900">
                  {gbPer1BTxs.toFixed(2)} GB
                </p>
                <p className="text-sm text-gray-600 mt-1">
                  per 1 billion transactions
                </p>
              </div>
              <div className="text-right">
                <p className="text-sm text-gray-600">
                  Total DB: {formatBytes(totalBytes)}
                </p>
                <p className="text-sm text-gray-600">
                  Raw TXs: {rawTxsCount.toLocaleString()}
                </p>
              </div>
            </div>
          </div>
        )}

        {/* Loading State */}
        {isLoading && (
          <div className="bg-white rounded-lg shadow p-8 text-center">
            <p className="text-gray-600">Loading sync status...</p>
          </div>
        )}

        {/* Error State */}
        {error && (
          <div className="bg-red-50 border border-red-200 rounded-lg p-6">
            <h3 className="text-sm font-semibold text-red-900 mb-1">Error Loading Sync Status</h3>
            <p className="text-sm text-red-700">{error.message}</p>
          </div>
        )}

        {/* Status Table */}
        {chains && chains.length > 0 && (
          <div className="bg-white rounded-lg shadow overflow-hidden">
            <table className="w-full">
              <thead className="bg-gray-50 border-b border-gray-200">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Chain
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Chain ID
                  </th>
                  <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Transactions
                  </th>
                  <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Synced Block
                  </th>
                  <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Latest Block
                  </th>
                  <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Blocks Behind
                  </th>
                  <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Sync %
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                    Last Updated
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200">
                {chains.map((chain, idx) => {
                  const blocksHealth = getBlocksBehindHealth(chain.blocksBehind);
                  const updatedHealth = getLastUpdatedHealth(chain.last_updated);
                  // P-Chain tx count comes from the storage list; EVM per-chain
                  // counts are not provided by this endpoint.
                  const txCount = chain.chain_id === 0 ? pChainTxCount : null;

                  return (
                    <tr
                      key={chain.chain_id}
                      className={idx % 2 === 0 ? 'bg-white' : 'bg-gray-50'}
                    >
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="text-sm font-medium text-gray-900">{chain.name}</div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="text-sm text-gray-600">{chain.chain_id}</div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="text-sm font-medium text-gray-900">
                          {txCount !== null ? txCount.toLocaleString() : '—'}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="text-sm font-medium text-gray-900">
                          {chain.watermark_block !== null ? chain.watermark_block.toLocaleString() : '—'}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="text-sm font-medium text-gray-900">
                          {chain.last_block_on_chain.toLocaleString()}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="flex items-center justify-end gap-2">
                          <div className={`w-2 h-2 rounded-full ${getHealthDot(blocksHealth)}`} />
                          <span className="text-sm font-semibold text-gray-900">
                            {chain.blocksBehind !== null ? chain.blocksBehind.toLocaleString() : '—'}
                          </span>
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="text-sm text-gray-600">
                          {chain.syncPercentage > 0 ? `${chain.syncPercentage.toFixed(2)}%` : '—'}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="flex items-center gap-2">
                          <div className={`w-2 h-2 rounded-full ${getHealthDot(updatedHealth)}`} />
                          <span className="text-sm text-gray-600">
                            {formatTimestamp(chain.last_updated)}
                          </span>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {/* Empty State */}
        {!isLoading && !error && chains && chains.length === 0 && (
          <div className="bg-white rounded-lg shadow p-8 text-center">
            <p className="text-gray-600">No chains found in the database.</p>
          </div>
        )}

        {/* Table Sizes */}
        <div className="mt-8">
          <h2 className="text-2xl font-bold text-gray-900 mb-4">Database Table Sizes</h2>
          {isLoadingTables && (
            <div className="bg-white rounded-lg shadow p-8 text-center">
              <p className="text-gray-600">Loading table sizes...</p>
            </div>
          )}

          {tableSizesError && (
            <div className="bg-red-50 border border-red-200 rounded-lg p-6">
              <h3 className="text-sm font-semibold text-red-900 mb-1">Error Loading Table Sizes</h3>
              <p className="text-sm text-red-700">{tableSizesError.message}</p>
            </div>
          )}

          {tableSizes && tableSizes.length > 0 && (
            <div className="bg-white rounded-lg shadow overflow-hidden">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Table Name
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Rows
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Size
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Bytes/Row
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {tableSizes.map((table, idx) => {
                    const bytesPerRow = table.rows > 0 ? table.size_bytes / table.rows : 0;
                    return (
                      <tr
                        key={table.table}
                        className={idx % 2 === 0 ? 'bg-white' : 'bg-gray-50'}
                      >
                        <td className="px-6 py-4 whitespace-nowrap">
                          <div className="text-sm font-medium text-gray-900">{table.table}</div>
                        </td>
                        <td className="px-6 py-4 whitespace-nowrap text-right">
                          <div className="text-sm text-gray-900">
                            {table.rows.toLocaleString()}
                          </div>
                        </td>
                        <td className="px-6 py-4 whitespace-nowrap text-right">
                          <div className="text-sm font-medium text-gray-900">
                            {formatBytes(table.size_bytes)}
                          </div>
                        </td>
                        <td className="px-6 py-4 whitespace-nowrap text-right">
                          <div className="text-sm text-gray-600">
                            {formatBytesPerRow(bytesPerRow)}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                  <tr className="bg-gray-100 border-t-2 border-gray-300">
                    <td className="px-6 py-4 whitespace-nowrap">
                      <div className="text-sm font-bold text-gray-900">TOTAL</div>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap text-right">
                      <div className="text-sm font-bold text-gray-900">
                        {totalRows.toLocaleString()}
                      </div>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap text-right">
                      <div className="text-sm font-bold text-gray-900">
                        {formatBytes(totalBytes)}
                      </div>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap text-right">
                      <div className="text-sm font-bold text-gray-600">
                        {formatBytesPerRow(totalRows > 0 ? totalBytes / totalRows : 0)}
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </PageTransition>
  );
}

export default SyncStatus;
