import { useParams, Link } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { useEvmTx } from '../lib/hooks';
import type {
  EvmInternalTx,
  EvmTokenTransfer,
  EvmTokenApproval,
} from '../lib/api';
import {
  shortAddr,
  formatTimestamp,
  timeAgo,
  formatGwei,
  formatAvax,
  formatUnits,
} from '../lib/format';

/** Map an EVM tx `type` byte to a human label. */
function txTypeLabel(type: number): string {
  switch (type) {
    case 0:
      return 'Legacy (0)';
    case 1:
      return 'Access List (1)';
    case 2:
      return 'EIP-1559 (2)';
    default:
      return `Type ${type}`;
  }
}

/** A small monospace link to an address page. */
function AddressLink({
  chainId,
  address,
  short = true,
}: {
  chainId: number;
  address: string | null | undefined;
  short?: boolean;
}) {
  if (!address) {
    return <span className="text-gray-500">—</span>;
  }
  return (
    <Link
      to={`/evm/${chainId}/address/${address}`}
      className="font-mono text-blue-600 hover:text-blue-800 break-all"
    >
      {short ? shortAddr(address) : address}
    </Link>
  );
}

/** A key / value row inside the overview card. */
function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 py-3 sm:flex-row sm:items-start sm:gap-4 border-b border-gray-100 last:border-0">
      <p className="text-xs uppercase tracking-wide text-gray-500 sm:w-40 sm:flex-shrink-0 sm:pt-0.5">
        {label}
      </p>
      <div className="min-w-0 flex-1 text-sm text-gray-900">{children}</div>
    </div>
  );
}

function EvmTxDetailPage() {
  const params = useParams<{ chainId?: string; hash?: string }>();
  const chainId = Number(params.chainId ?? 43114) || 43114;
  const hash = params.hash;

  const { data: tx, isLoading, error } = useEvmTx(chainId, hash);

  if (isLoading) {
    return (
      <div className="p-8 flex items-center justify-center min-h-[400px]">
        <p className="text-gray-500">Loading transaction details...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-8 text-center">
        <h2 className="text-2xl font-bold text-gray-900">Error Loading Transaction</h2>
        <p className="text-red-600 mt-2">{String(error)}</p>
        <p className="text-gray-600 mt-2 font-mono break-all">{hash}</p>
        <Link to="/evm-metrics" className="text-blue-600 hover:text-blue-800 mt-4 inline-block">
          ← Back
        </Link>
      </div>
    );
  }

  if (!tx) {
    return (
      <div className="p-8 text-center">
        <h2 className="text-2xl font-bold text-gray-900">Transaction Not Found</h2>
        <p className="text-gray-600 mt-2 font-mono break-all">{hash}</p>
        <p className="text-sm text-gray-500 mt-2">
          This transaction does not exist in the database.
        </p>
        <Link to="/evm-metrics" className="text-blue-600 hover:text-blue-800 mt-4 inline-block">
          ← Back
        </Link>
      </div>
    );
  }

  const transfers: EvmTokenTransfer[] = tx.token_transfers ?? [];
  const approvals: EvmTokenApproval[] = tx.approvals ?? [];
  const internalTxs: EvmInternalTx[] = tx.internal_txs ?? [];

  return (
    <PageTransition>
      <div className="p-8 space-y-6">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-gray-900">Transaction</h1>
          <p className="text-gray-600 mt-2 font-mono break-all">{tx.hash}</p>
        </div>

        {/* Overview card */}
        <div className="bg-white rounded-lg shadow-sm border border-gray-200 p-6">
          <h2 className="text-xl font-bold text-gray-900 mb-2">Overview</h2>
          <div className="divide-gray-100">
            <Row label="Hash">
              <span className="font-mono break-all">{tx.hash}</span>
            </Row>

            <Row label="Status">
              {tx.success ? (
                <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-800">
                  Success
                </span>
              ) : (
                <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-800">
                  Failed
                </span>
              )}
            </Row>

            <Row label="Block">
              <Link
                to={`/evm/${chainId}/block/${tx.block_number}`}
                className="text-blue-600 hover:text-blue-800 font-medium"
              >
                #{tx.block_number.toLocaleString()}
              </Link>
              <span className="text-gray-500 ml-2">(index {tx.transaction_index})</span>
            </Row>

            <Row label="Timestamp">
              <span>{formatTimestamp(tx.block_time)}</span>
              <span className="text-gray-500 ml-2">({timeAgo(tx.block_time)})</span>
            </Row>

            <Row label="From">
              <AddressLink chainId={chainId} address={tx.from} short={false} />
            </Row>

            <Row label="To">
              {tx.to === null ? (
                <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-purple-100 text-purple-800">
                  Contract creation
                </span>
              ) : (
                <AddressLink chainId={chainId} address={tx.to} />
              )}
            </Row>

            <Row label="Value">
              <span className="font-medium">{formatAvax(tx.value)} AVAX</span>
            </Row>

            <Row label="Tx Type">
              <span>{txTypeLabel(tx.type)}</span>
            </Row>

            <Row label="Gas Used">
              <span>{tx.gas_used.toLocaleString()}</span>
            </Row>

            <Row label="Gas Price">
              <span>{formatGwei(tx.gas_price)}</span>
            </Row>

            <Row label="Gas Limit">
              <span>{tx.gas_limit.toLocaleString()}</span>
            </Row>
          </div>
        </div>

        {/* Token Transfers */}
        {transfers.length > 0 && (
          <div className="bg-white rounded-lg shadow-sm border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-xl font-bold text-gray-900">
                Token Transfers ({transfers.length})
              </h2>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      From
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      To
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Amount
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Token
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {transfers.map((t) => (
                    <tr key={`${t.token}-${t.log_index}`}>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <AddressLink chainId={chainId} address={t.from} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <AddressLink chainId={chainId} address={t.to} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right text-sm font-medium text-gray-900">
                        {formatUnits(t.value, t.decimals ?? 18)}{' '}
                        <span className="text-gray-500">{t.symbol ?? shortAddr(t.token)}</span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <Link
                          to={`/evm/${chainId}/address/${t.token}`}
                          className="text-blue-600 hover:text-blue-800"
                        >
                          {t.symbol ?? t.name ?? shortAddr(t.token)}
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* Approvals */}
        {approvals.length > 0 && (
          <div className="bg-white rounded-lg shadow-sm border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-xl font-bold text-gray-900">Approvals ({approvals.length})</h2>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Owner
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Spender
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Token
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Amount
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {approvals.map((a) => (
                    <tr key={`${a.token}-${a.log_index}`}>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <AddressLink chainId={chainId} address={a.owner} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <AddressLink chainId={chainId} address={a.spender} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <Link
                          to={`/evm/${chainId}/address/${a.token}`}
                          className="text-blue-600 hover:text-blue-800"
                        >
                          {a.symbol ?? a.name ?? shortAddr(a.token)}
                        </Link>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right text-sm font-medium text-gray-900">
                        {a.is_unlimited ? (
                          <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-800">
                            Unlimited
                          </span>
                        ) : (
                          formatUnits(a.amount, a.decimals ?? 18)
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* Internal Transactions */}
        {internalTxs.length > 0 && (
          <div className="bg-white rounded-lg shadow-sm border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-xl font-bold text-gray-900">
                Internal Transactions ({internalTxs.length})
              </h2>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Trace
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Type
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      From
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      To
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Value
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Gas Used
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Status
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {internalTxs.map((itx) => (
                    <tr key={itx.trace_index}>
                      <td className="px-6 py-4 whitespace-nowrap text-sm font-mono text-gray-700">
                        {itx.trace_index}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <span className="inline-flex items-center px-2.5 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-800">
                          {itx.call_type || 'call'}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <AddressLink chainId={chainId} address={itx.from} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        <AddressLink chainId={chainId} address={itx.to} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right text-sm text-gray-900">
                        {formatAvax(itx.value)} AVAX
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right text-sm text-gray-900">
                        {itx.gas_used.toLocaleString()}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm">
                        {itx.success ? (
                          <span className="text-green-700">Success</span>
                        ) : (
                          <span className="text-red-700">Failed</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    </PageTransition>
  );
}

export default EvmTxDetailPage;
