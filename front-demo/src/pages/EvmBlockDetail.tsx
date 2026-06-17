import { useParams, Link } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { useEvmBlock, useEvmTxs } from '../lib/hooks';
import type { EvmTx } from '../lib/api';
import {
  ArrowLeft,
  Box,
  Clock,
  Cpu,
  Fuel,
  CheckCircle2,
  XCircle,
} from 'lucide-react';
import {
  shortHash,
  shortAddr,
  formatTimestamp,
  timeAgo,
  formatGwei,
  formatAvax,
} from '../lib/format';

function DetailRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1 border-b border-gray-100 py-3 last:border-0 sm:flex-row sm:items-center sm:gap-4">
      <span className="w-48 flex-shrink-0 text-xs font-semibold uppercase tracking-wide text-gray-500">
        {label}
      </span>
      <div className="min-w-0 text-sm text-gray-900">{children}</div>
    </div>
  );
}

function EvmBlockDetail() {
  const params = useParams<{ chainId: string; number: string }>();
  const chainId = parseInt(params.chainId || '43114', 10) || 43114;
  const blockNumber = parseInt(params.number || '', 10);
  const validNumber = !Number.isNaN(blockNumber);

  const {
    data: block,
    isLoading: loadingBlock,
    error: blockError,
  } = useEvmBlock(chainId, validNumber ? blockNumber : undefined);

  const {
    data: txData,
    isLoading: loadingTxs,
    error: txError,
  } = useEvmTxs(chainId, { blockNumber }, validNumber && !!block);

  const transactions: EvmTx[] = txData ?? [];

  const overviewHref = `/evm/${chainId}`;

  if (!validNumber) {
    return (
      <PageTransition>
        <div className="p-8 text-center">
          <h2 className="text-2xl font-bold text-gray-900">Invalid Block Number</h2>
          <p className="mt-2 text-gray-600">"{params.number}" is not a valid block number.</p>
          <Link to={overviewHref} className="mt-4 inline-block text-blue-600 hover:text-blue-800">
            ← Back
          </Link>
        </div>
      </PageTransition>
    );
  }

  if (loadingBlock) {
    return (
      <PageTransition>
        <div className="flex min-h-[400px] items-center justify-center p-8">
          <p className="text-gray-500">Loading block details...</p>
        </div>
      </PageTransition>
    );
  }

  if (blockError) {
    return (
      <PageTransition>
        <div className="p-8 text-center">
          <h2 className="text-2xl font-bold text-gray-900">Error Loading Block</h2>
          <p className="mt-2 text-red-600">{String(blockError)}</p>
          <p className="mt-2 text-gray-600">Block #{blockNumber.toLocaleString()}</p>
          <Link to={overviewHref} className="mt-4 inline-block text-blue-600 hover:text-blue-800">
            ← Back
          </Link>
        </div>
      </PageTransition>
    );
  }

  if (!block) {
    return (
      <PageTransition>
        <div className="p-8 text-center">
          <h2 className="text-2xl font-bold text-gray-900">Block Not Found</h2>
          <p className="mt-2 text-gray-600">Block #{blockNumber.toLocaleString()}</p>
          <p className="mt-2 text-sm text-gray-500">
            This block may not be indexed yet, or the number may be incorrect.
          </p>
          <Link to={overviewHref} className="mt-4 inline-block text-blue-600 hover:text-blue-800">
            ← Back
          </Link>
        </div>
      </PageTransition>
    );
  }

  const gasPct =
    block.gas_limit > 0 ? (block.gas_used / block.gas_limit) * 100 : 0;

  return (
    <PageTransition>
      <div className="space-y-6 p-8">
        {/* Back link */}
        <Link
          to={overviewHref}
          className="inline-flex items-center gap-2 text-gray-600 transition-colors hover:text-gray-900"
        >
          <ArrowLeft size={20} />
          Back
        </Link>

        {/* Block detail card */}
        <div className="rounded-lg bg-white p-6 shadow">
          <h1 className="flex items-center gap-3 text-2xl font-bold text-gray-900">
            <Box size={28} className="text-gray-400" />
            Block #{block.block_number.toLocaleString()}
          </h1>

          <div className="mt-4">
            <DetailRow label="Hash">
              <span className="break-all font-mono" title={block.hash}>
                {shortHash(block.hash)}
              </span>
            </DetailRow>

            <DetailRow label="Parent Hash">
              <Link
                to={`/evm/${chainId}/block/${block.block_number - 1}`}
                className="break-all font-mono text-blue-600 hover:text-blue-800"
                title={block.parent_hash}
              >
                {shortHash(block.parent_hash)}
              </Link>
            </DetailRow>

            <DetailRow label="Timestamp">
              <span className="inline-flex flex-wrap items-center gap-2">
                <Clock size={16} className="text-gray-400" />
                {formatTimestamp(block.block_time)}
                <span className="text-gray-500">({timeAgo(block.block_time)})</span>
              </span>
            </DetailRow>

            <DetailRow label="Miner">
              <Link
                to={`/evm/${chainId}/address/${block.miner}`}
                className="font-mono text-blue-600 hover:text-blue-800"
                title={block.miner}
              >
                {shortAddr(block.miner)}
              </Link>
            </DetailRow>

            <DetailRow label="Gas Used / Limit">
              <span className="inline-flex flex-wrap items-center gap-2">
                <Cpu size={16} className="text-gray-400" />
                {block.gas_used.toLocaleString()} / {block.gas_limit.toLocaleString()}
                <span className="text-gray-500">({gasPct.toFixed(2)}%)</span>
              </span>
            </DetailRow>

            <DetailRow label="Base Fee">
              <span className="inline-flex items-center gap-2">
                <Fuel size={16} className="text-gray-400" />
                {formatGwei(block.base_fee_per_gas)}
              </span>
            </DetailRow>

            <DetailRow label="Size">
              {block.size.toLocaleString()} bytes
            </DetailRow>

            <DetailRow label="Transactions">
              {block.tx_count.toLocaleString()}
            </DetailRow>
          </div>
        </div>

        {/* Transactions in block */}
        <div className="overflow-hidden rounded-lg bg-white shadow">
          <div className="border-b border-gray-200 px-6 py-4">
            <h2 className="text-lg font-bold text-gray-900">Transactions</h2>
            <p className="mt-1 text-sm text-gray-600">
              {block.tx_count.toLocaleString()} transaction
              {block.tx_count === 1 ? '' : 's'} in this block
            </p>
          </div>

          {loadingTxs ? (
            <div className="p-12 text-center">
              <p className="text-gray-500">Loading transactions...</p>
            </div>
          ) : txError ? (
            <div className="p-12 text-center">
              <p className="text-red-600">Error loading transactions: {String(txError)}</p>
            </div>
          ) : transactions.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="border-b border-gray-200 bg-gray-50">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider text-gray-700">
                      Tx Hash
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider text-gray-700">
                      From
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider text-gray-700">
                      To
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-700">
                      Value
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider text-gray-700">
                      Status
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {transactions.map((tx) => (
                    <tr key={tx.hash} className="transition-colors hover:bg-gray-50">
                      <td className="px-6 py-4 whitespace-nowrap">
                        <Link
                          to={`/evm/${chainId}/tx/${tx.hash}`}
                          className="font-mono text-sm text-blue-600 hover:text-blue-800"
                          title={tx.hash}
                        >
                          {shortHash(tx.hash)}
                        </Link>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <Link
                          to={`/evm/${chainId}/address/${tx.from}`}
                          className="font-mono text-sm text-blue-600 hover:text-blue-800"
                          title={tx.from}
                        >
                          {shortAddr(tx.from)}
                        </Link>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        {tx.to ? (
                          <Link
                            to={`/evm/${chainId}/address/${tx.to}`}
                            className="font-mono text-sm text-blue-600 hover:text-blue-800"
                            title={tx.to}
                          >
                            {shortAddr(tx.to)}
                          </Link>
                        ) : (
                          <span className="text-sm italic text-gray-500">
                            Contract creation
                          </span>
                        )}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <span className="text-sm text-gray-900">
                          {formatAvax(tx.value)} AVAX
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        {tx.success ? (
                          <span className="inline-flex items-center gap-1 text-sm font-medium text-green-600">
                            <CheckCircle2 size={16} />
                            Success
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 text-sm font-medium text-red-600">
                            <XCircle size={16} />
                            Failed
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="p-12 text-center">
              <p className="text-gray-500">No transactions in this block.</p>
            </div>
          )}
        </div>
      </div>
    </PageTransition>
  );
}

export default EvmBlockDetail;
