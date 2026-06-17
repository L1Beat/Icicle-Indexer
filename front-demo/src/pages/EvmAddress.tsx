import { useParams, Link } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import {
  Wallet,
  ArrowDownLeft,
  ArrowUpRight,
  Activity,
  Coins,
  Hash,
  Flame,
  Clock,
  CheckCircle,
  XCircle,
} from 'lucide-react';
import {
  useAddressNativeBalance,
  useAddressBalances,
  useAddressTxs,
  useAddressInternalTxs,
} from '../lib/hooks';
import {
  shortHash,
  shortAddr,
  formatTimestamp,
  timeAgo,
  formatAvax,
  formatUnits,
} from '../lib/format';

function EvmAddress() {
  const params = useParams<{ chainId: string; address: string }>();
  const chainId = Number(params.chainId) || 43114;
  const address = params.address ?? '';
  const addrLower = address.toLowerCase();

  const { data: native, isLoading: loadingNative, error: nativeError } =
    useAddressNativeBalance(chainId, address);
  const { data: tokens, isLoading: loadingTokens } = useAddressBalances(chainId, address);
  const { data: txs, isLoading: loadingTxs } = useAddressTxs(chainId, address, 25);
  const { data: internalTxs, isLoading: loadingInternal } = useAddressInternalTxs(
    chainId,
    address,
    25,
  );

  const isSelf = (other: string | null | undefined) =>
    !!other && other.toLowerCase() === addrLower;

  if (!address) {
    return (
      <div className="p-8 text-center">
        <h2 className="text-2xl font-bold text-gray-900">No Address</h2>
        <p className="text-gray-500 mt-2">No address was provided in the URL.</p>
      </div>
    );
  }

  return (
    <PageTransition>
      <div className="p-8 space-y-6 max-w-6xl mx-auto">
        {/* Header */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
          <div className="flex items-start gap-4">
            <div className="p-3 rounded-xl bg-blue-100 flex-shrink-0">
              <Wallet size={28} className="text-blue-600" />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-semibold text-gray-500 uppercase tracking-wider">
                Address
              </p>
              <h1 className="text-xl md:text-2xl font-bold text-gray-900 font-mono break-all mt-1">
                {address}
              </h1>
              <p className="text-sm text-gray-500 mt-1">Chain ID: {chainId}</p>
            </div>
          </div>
        </div>

        {/* Native balance / activity stat cards */}
        {nativeError ? (
          <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-8 text-center">
            <p className="text-gray-500">Failed to load native balance.</p>
          </div>
        ) : loadingNative ? (
          <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-8 text-center">
            <p className="text-gray-500">Loading balance...</p>
          </div>
        ) : native ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <div className="bg-white rounded-lg shadow p-6 border-l-4 border-blue-500">
              <div className="flex items-center justify-between">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                    AVAX Balance
                  </p>
                  <p className="text-2xl font-bold text-gray-900 mt-2 break-all">
                    {formatAvax(native.balance)} AVAX
                  </p>
                </div>
                <div className="p-3 bg-blue-100 rounded-full flex-shrink-0">
                  <Wallet size={24} className="text-blue-600" />
                </div>
              </div>
            </div>

            <div className="bg-white rounded-lg shadow p-6 border-l-4 border-indigo-500">
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                    Tx Count
                  </p>
                  <p className="text-2xl font-bold text-gray-900 mt-2">
                    {native.tx_count.toLocaleString()}
                  </p>
                </div>
                <div className="p-3 bg-indigo-100 rounded-full flex-shrink-0">
                  <Activity size={24} className="text-indigo-600" />
                </div>
              </div>
            </div>

            <div className="bg-white rounded-lg shadow p-6 border-l-4 border-green-500">
              <div className="flex items-center justify-between">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                    Total In
                  </p>
                  <p className="text-2xl font-bold text-green-600 mt-2 break-all">
                    {formatAvax(native.total_in)} AVAX
                  </p>
                </div>
                <div className="p-3 bg-green-100 rounded-full flex-shrink-0">
                  <ArrowDownLeft size={24} className="text-green-600" />
                </div>
              </div>
            </div>

            <div className="bg-white rounded-lg shadow p-6 border-l-4 border-red-500">
              <div className="flex items-center justify-between">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                    Total Out
                  </p>
                  <p className="text-2xl font-bold text-red-600 mt-2 break-all">
                    {formatAvax(native.total_out)} AVAX
                  </p>
                </div>
                <div className="p-3 bg-red-100 rounded-full flex-shrink-0">
                  <ArrowUpRight size={24} className="text-red-600" />
                </div>
              </div>
            </div>

            <div className="bg-white rounded-lg shadow p-6 border-l-4 border-orange-500">
              <div className="flex items-center justify-between">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                    Total Gas Spent
                  </p>
                  <p className="text-2xl font-bold text-orange-600 mt-2 break-all">
                    {formatAvax(native.total_gas)} AVAX
                  </p>
                </div>
                <div className="p-3 bg-orange-100 rounded-full flex-shrink-0">
                  <Flame size={24} className="text-orange-600" />
                </div>
              </div>
            </div>

            <div className="bg-white rounded-lg shadow p-6 border-l-4 border-gray-400">
              <div className="flex items-center justify-between">
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                    Activity
                  </p>
                  <p className="text-sm font-semibold text-gray-900 mt-2">
                    First: {formatTimestamp(native.first_tx_time)}
                  </p>
                  <p className="text-sm font-semibold text-gray-900 mt-1">
                    Last: {formatTimestamp(native.last_tx_time)}
                  </p>
                </div>
                <div className="p-3 bg-gray-100 rounded-full flex-shrink-0">
                  <Clock size={24} className="text-gray-600" />
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-8 text-center">
            <p className="text-gray-500">No native balance data.</p>
          </div>
        )}

        {/* Token Balances */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-lg font-bold text-gray-900 flex items-center gap-2">
              <Coins size={20} className="text-gray-400" />
              Token Balances
            </h2>
          </div>
          {loadingTokens ? (
            <div className="p-8 text-center">
              <p className="text-gray-500">Loading token balances...</p>
            </div>
          ) : tokens && tokens.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Token
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Contract
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Balance
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {tokens.map((t) => (
                    <tr key={t.token} className="hover:bg-gray-50">
                      <td className="px-6 py-3">
                        <Link
                          to={`/evm/${chainId}/address/${t.token}`}
                          className="font-medium text-blue-600 hover:text-blue-800"
                        >
                          {t.symbol || t.name || shortAddr(t.token)}
                        </Link>
                      </td>
                      <td className="px-6 py-3 font-mono text-gray-600">
                        {shortAddr(t.token)}
                      </td>
                      <td className="px-6 py-3 text-right font-semibold text-gray-900">
                        {formatUnits(t.balance, t.decimals ?? 18)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="p-8 text-center">
              <p className="text-gray-500">No token balances.</p>
            </div>
          )}
        </div>

        {/* Transactions */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-lg font-bold text-gray-900 flex items-center gap-2">
              <Hash size={20} className="text-gray-400" />
              Transactions
            </h2>
          </div>
          {loadingTxs ? (
            <div className="p-8 text-center">
              <p className="text-gray-500">Loading transactions...</p>
            </div>
          ) : txs && txs.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Hash
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Block
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Age
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      From
                    </th>
                    <th className="px-6 py-3 text-center text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      {/* IN/OUT direction chip */}
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      To
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Value
                    </th>
                    <th className="px-6 py-3 text-center text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Status
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {txs.map((tx) => {
                    const out = isSelf(tx.from);
                    return (
                      <tr key={tx.hash} className="hover:bg-gray-50">
                        <td className="px-6 py-3">
                          <Link
                            to={`/evm/${chainId}/tx/${tx.hash}`}
                            className="font-mono text-blue-600 hover:text-blue-800"
                          >
                            {shortHash(tx.hash)}
                          </Link>
                        </td>
                        <td className="px-6 py-3">
                          <Link
                            to={`/evm/${chainId}/block/${tx.block_number}`}
                            className="text-blue-600 hover:text-blue-800"
                          >
                            {tx.block_number.toLocaleString()}
                          </Link>
                        </td>
                        <td className="px-6 py-3 text-gray-600 whitespace-nowrap">
                          {timeAgo(tx.block_time)}
                        </td>
                        <td className="px-6 py-3">
                          <Link
                            to={`/evm/${chainId}/address/${tx.from}`}
                            className="font-mono text-blue-600 hover:text-blue-800"
                          >
                            {shortAddr(tx.from)}
                          </Link>
                        </td>
                        <td className="px-6 py-3 text-center">
                          <span
                            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-semibold ${
                              out
                                ? 'bg-red-100 text-red-700'
                                : 'bg-green-100 text-green-700'
                            }`}
                          >
                            {out ? 'OUT' : 'IN'}
                          </span>
                        </td>
                        <td className="px-6 py-3">
                          {tx.to ? (
                            <Link
                              to={`/evm/${chainId}/address/${tx.to}`}
                              className="font-mono text-blue-600 hover:text-blue-800"
                            >
                              {shortAddr(tx.to)}
                            </Link>
                          ) : (
                            <span className="text-gray-500 italic">
                              Contract creation
                            </span>
                          )}
                        </td>
                        <td className="px-6 py-3 text-right font-semibold text-gray-900 whitespace-nowrap">
                          {formatAvax(tx.value)} AVAX
                        </td>
                        <td className="px-6 py-3 text-center">
                          {tx.success ? (
                            <span className="inline-flex items-center gap-1 text-green-600">
                              <CheckCircle size={16} />
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1 text-red-600">
                              <XCircle size={16} />
                            </span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="p-8 text-center">
              <p className="text-gray-500">No transactions found.</p>
            </div>
          )}
        </div>

        {/* Internal Transactions */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-lg font-bold text-gray-900 flex items-center gap-2">
              <Activity size={20} className="text-gray-400" />
              Internal Transactions
            </h2>
          </div>
          {loadingInternal ? (
            <div className="p-8 text-center">
              <p className="text-gray-500">Loading internal transactions...</p>
            </div>
          ) : internalTxs && internalTxs.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
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
                    <th className="px-6 py-3 text-center text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Status
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {internalTxs.map((itx, idx) => (
                    <tr
                      key={`${itx.tx_hash ?? 'tx'}-${itx.trace_index}-${idx}`}
                      className="hover:bg-gray-50"
                    >
                      <td className="px-6 py-3 font-mono text-gray-600">
                        {itx.tx_hash ? (
                          <Link
                            to={`/evm/${chainId}/tx/${itx.tx_hash}`}
                            className="text-blue-600 hover:text-blue-800"
                          >
                            {itx.trace_index}
                          </Link>
                        ) : (
                          itx.trace_index
                        )}
                      </td>
                      <td className="px-6 py-3">
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700">
                          {itx.call_type}
                        </span>
                      </td>
                      <td className="px-6 py-3">
                        <Link
                          to={`/evm/${chainId}/address/${itx.from}`}
                          className="font-mono text-blue-600 hover:text-blue-800"
                        >
                          {shortAddr(itx.from)}
                        </Link>
                      </td>
                      <td className="px-6 py-3">
                        {itx.to ? (
                          <Link
                            to={`/evm/${chainId}/address/${itx.to}`}
                            className="font-mono text-blue-600 hover:text-blue-800"
                          >
                            {shortAddr(itx.to)}
                          </Link>
                        ) : (
                          <span className="text-gray-500 italic">—</span>
                        )}
                      </td>
                      <td className="px-6 py-3 text-right font-semibold text-gray-900 whitespace-nowrap">
                        {formatAvax(itx.value)} AVAX
                      </td>
                      <td className="px-6 py-3 text-center">
                        {itx.success ? (
                          <span className="inline-flex items-center gap-1 text-green-600">
                            <CheckCircle size={16} />
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 text-red-600">
                            <XCircle size={16} />
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="p-8 text-center">
              <p className="text-gray-500">No internal transactions found.</p>
            </div>
          )}
        </div>
      </div>
    </PageTransition>
  );
}

export default EvmAddress;
