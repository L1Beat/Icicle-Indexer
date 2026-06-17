import { useParams, useNavigate } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { useEvmBlocks, useIndexerStatus } from '../lib/hooks';
import type { EvmBlock } from '../lib/api';
import { shortAddr, timeAgo, formatGwei, formatUnits } from '../lib/format';

/** Gas used as a percentage of the block's gas limit. */
function gasUsedPct(block: EvmBlock): number {
  if (!block.gas_limit) return 0;
  return (block.gas_used / block.gas_limit) * 100;
}

function EvmBlocks() {
  const { chainId } = useParams<{ chainId: string }>();
  const navigate = useNavigate();

  const selectedChainId = chainId ? parseInt(chainId, 10) : 43114;
  const resolvedChainId = Number.isNaN(selectedChainId) ? 43114 : selectedChainId;

  const { data: blocks, isLoading, error } = useEvmBlocks(resolvedChainId, 25);
  const { data: status } = useIndexerStatus();
  const chainName =
    status?.evm?.find((c) => c.chain_id === resolvedChainId)?.name || `Chain ${resolvedChainId}`;

  return (
    <PageTransition>
      <div className="p-8 space-y-6">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-gray-900">{chainName} Blocks</h1>
          <p className="text-gray-600 mt-2">
            Most recent EVM blocks for {chainName} (chain {resolvedChainId}), refreshed every 10 seconds.
          </p>
        </div>

        {/* Blocks Table */}
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-xl font-bold text-gray-900">Latest Blocks</h2>
          </div>

          {error ? (
            <div className="p-8 text-center">
              <p className="text-red-600">
                Error loading blocks: {error instanceof Error ? error.message : String(error)}
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Block
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Age
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Txs
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Gas Used
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Base Fee
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Miner
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {isLoading ? (
                    // Skeleton rows while the first fetch is in flight.
                    Array.from({ length: 8 }).map((_, idx) => (
                      <tr key={idx} className={idx % 2 === 0 ? 'bg-white' : 'bg-gray-50'}>
                        {Array.from({ length: 6 }).map((_, col) => (
                          <td key={col} className="px-6 py-4 whitespace-nowrap">
                            <div className="h-4 bg-gray-200 rounded animate-pulse" />
                          </td>
                        ))}
                      </tr>
                    ))
                  ) : blocks && blocks.length > 0 ? (
                    blocks.map((block, idx) => {
                      const pct = gasUsedPct(block);
                      return (
                        <tr
                          key={block.block_number}
                          onClick={() =>
                            navigate(`/evm/${resolvedChainId}/block/${block.block_number}`)
                          }
                          className={`cursor-pointer hover:bg-blue-50 transition-colors ${
                            idx % 2 === 0 ? 'bg-white' : 'bg-gray-50'
                          }`}
                        >
                          <td className="px-6 py-4 whitespace-nowrap">
                            <span className="text-sm font-mono font-medium text-blue-600">
                              {block.block_number.toLocaleString()}
                            </span>
                          </td>
                          <td className="px-6 py-4 whitespace-nowrap">
                            <span className="text-sm text-gray-600">{timeAgo(block.block_time)}</span>
                          </td>
                          <td className="px-6 py-4 whitespace-nowrap text-right">
                            <span className="text-sm font-semibold text-gray-900">
                              {block.tx_count.toLocaleString()}
                            </span>
                          </td>
                          <td className="px-6 py-4 whitespace-nowrap text-right">
                            <span className="text-sm text-gray-900">
                              {formatUnits(block.gas_used, 0)}
                            </span>
                            <span className="text-xs text-gray-500 ml-1">
                              ({pct.toFixed(1)}%)
                            </span>
                          </td>
                          <td className="px-6 py-4 whitespace-nowrap text-right">
                            <span className="text-sm text-gray-900">
                              {formatGwei(block.base_fee_per_gas)}
                            </span>
                          </td>
                          <td className="px-6 py-4 whitespace-nowrap">
                            <code className="text-xs font-mono text-gray-700">
                              {shortAddr(block.miner)}
                            </code>
                          </td>
                        </tr>
                      );
                    })
                  ) : (
                    <tr>
                      <td colSpan={6} className="px-6 py-12 text-center">
                        <p className="text-gray-500">No recent blocks found for this chain.</p>
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </PageTransition>
  );
}

export default EvmBlocks;
