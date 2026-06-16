import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { Network, Users, Copy, Search, Coins } from 'lucide-react';
import MetricChart from '../components/MetricChart';
import {
  usePChainStats,
  useSubnetTimeline,
  usePChainTxTypes,
  useChains,
  usePChainBlocks,
  usePChainTxs,
} from '../lib/hooks';

interface L1Subnet {
  subnet_id: string;
  subnet_type: string;
  chain_id: string;
  conversion_block: number;
  conversion_time: string;
  created_block: number;
  created_time: string;
  validator_count: number;
  name?: string;
  logo_url?: string;
  total_fees_paid?: number;
}

const SUBNET_TYPE_RANK: Record<string, number> = {
  primary: 0,
  l1: 1,
  elastic: 2,
  legacy: 3,
};

function PChainOverview() {
  const navigate = useNavigate();
  const [searchTerm, setSearchTerm] = useState('');
  const [txBlockSearch, setTxBlockSearch] = useState('');
  const [showAllChains, setShowAllChains] = useState(false);
  const [typeFilter, setTypeFilter] = useState<'all' | 'l1' | 'legacy'>('all');

  // Global Statistics
  const { data: stats, isLoading: loadingStats } = usePChainStats();

  // Subnet Creation Timeline (API returns per-month counts oldest-first;
  // compute the running cumulative total the chart expects).
  const { data: timelineRaw, isLoading: loadingTimeline } = useSubnetTimeline();
  let cumulative = 0;
  const timeline = timelineRaw?.map((item) => {
    cumulative += item.value;
    return { period: item.period, value: cumulative };
  });

  // Recent Platform Activity (last 30 days, top 10 tx types)
  const { data: platformActivityRaw, isLoading: loadingActivity } = usePChainTxTypes(30);
  const platformActivity = platformActivityRaw?.slice(0, 10);

  // All Chains Table
  const { data: chains, isLoading: loadingSubnets } = useChains();
  const subnets: L1Subnet[] | undefined = chains?.map((c) => {
    // For L1 subnets the chain-creation fields are often 0/epoch; the
    // meaningful "created" point is the L1 conversion. Fall back to it.
    const hasCreated = c.created_block > 0;
    return {
      subnet_id: c.subnet_id,
      subnet_type: c.chain_type,
      chain_id: c.chain_id,
      conversion_block: c.converted_block ?? 0,
      conversion_time: c.converted_time ?? '',
      created_block: hasCreated ? c.created_block : (c.converted_block ?? 0),
      created_time: hasCreated ? c.created_time : (c.converted_time ?? c.created_time),
      validator_count: c.validator_count ?? 0,
      name: c.name,
      logo_url: c.logo_url,
      total_fees_paid: c.total_fees_paid,
    };
  });

  // Recent P-Chain Blocks
  const { data: recentBlocks, isLoading: loadingBlocks } = usePChainBlocks({ limit: 8 });

  // Recent P-Chain Transactions
  const {
    data: recentTransactions,
    isLoading: loadingTransactions,
    error: txError,
  } = usePChainTxs({ limit: 8 });

  const formatTimestamp = (timestamp: string) => {
    const date = new Date(timestamp);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffSec = Math.floor(diffMs / 1000);
    const diffMin = Math.floor(diffSec / 60);
    const diffHour = Math.floor(diffMin / 60);
    const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

    if (diffSec < 60) return `${diffSec}s ago`;
    if (diffMin < 60) return `${diffMin}m ago`;
    if (diffHour < 24) return `${diffHour}h ago`;
    if (diffDays === 1) return 'Yesterday';
    if (diffDays < 7) return `${diffDays}d ago`;
    if (diffDays < 30) return `${Math.floor(diffDays / 7)}w ago`;
    return date.toLocaleDateString();
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
  };

  const truncateHash = (hash: string, length = 8) => {
    if (!hash) return '';
    return `${hash.slice(0, length)}...${hash.slice(-4)}`;
  };

  const formatSubnetType = (type: string) => {
    switch (type) {
      case 'l1': return 'L1';
      case 'legacy': return 'Legacy Subnet';
      case 'elastic': return 'Elastic';
      case 'primary': return 'Primary';
      default: return type;
    }
  };

  // Format nAVAX to AVAX with appropriate decimal places
  const formatAVAX = (nanoAVAX: number, decimals = 2) => {
    const avax = nanoAVAX / 1e9; // 1 AVAX = 1e9 nAVAX
    if (avax >= 1000000) {
      return `${(avax / 1000000).toFixed(decimals)}M`;
    } else if (avax >= 1000) {
      return `${(avax / 1000).toFixed(decimals)}K`;
    } else if (avax >= 1) {
      return avax.toFixed(decimals);
    } else {
      return avax.toFixed(4);
    }
  };

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedSearch = txBlockSearch.trim();

    if (!trimmedSearch) return;

    // Check if it's a number (block number)
    if (/^\d+$/.test(trimmedSearch)) {
      navigate(`/p-chain/block/${trimmedSearch}`);
    } else {
      // Otherwise treat as transaction ID (CB58 encoded string)
      navigate(`/p-chain/tx/${trimmedSearch}`);
    }
  };

  const allFilteredSubnets = subnets
    ?.filter(subnet => {
      // Type filter
      if (typeFilter === 'l1' && subnet.subnet_type !== 'l1') return false;
      if (typeFilter === 'legacy' && subnet.subnet_type !== 'legacy') return false;

      // Search filter
      return (
        subnet.name?.toLowerCase().includes(searchTerm.toLowerCase()) ||
        subnet.subnet_id.toLowerCase().includes(searchTerm.toLowerCase()) ||
        subnet.chain_id?.toLowerCase().includes(searchTerm.toLowerCase())
      );
    })
    .sort((a, b) => {
      // Rank by subnet type first (primary, l1, elastic, legacy, then others),
      // then by validator count descending.
      const rankA = SUBNET_TYPE_RANK[a.subnet_type] ?? 4;
      const rankB = SUBNET_TYPE_RANK[b.subnet_type] ?? 4;
      if (rankA !== rankB) return rankA - rankB;
      return b.validator_count - a.validator_count;
    });

  const filteredSubnets = showAllChains ? allFilteredSubnets : allFilteredSubnets?.slice(0, 50);
  const totalChains = allFilteredSubnets?.length || 0;
  const displayedChains = filteredSubnets?.length || 0;

  return (
    <PageTransition>
      <div className="p-8 space-y-6">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-gray-900">P-Chain Overview (v2)</h1>
          <p className="text-gray-600 mt-2">
            Platform chain for L1 subnet creation and validator management
          </p>
        </div>

        {/* Global Stats Cards */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-4">
          {/* Total Active Chains */}
          <div className="bg-white rounded-lg shadow p-6 border-l-4 border-indigo-500">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                  Total Active Chains
                </p>
                <p className="text-3xl font-bold text-gray-900 mt-2">
                  {loadingStats ? '...' : stats?.active_chains || 0}
                </p>
              </div>
              <div className="p-3 bg-indigo-100 rounded-full">
                <Network size={24} className="text-indigo-600" />
              </div>
            </div>
          </div>

          {/* Active L1s */}
          <div className="bg-white rounded-lg shadow p-6 border-l-4 border-blue-500">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                  Active L1s
                </p>
                <p className="text-3xl font-bold text-gray-900 mt-2">
                  {loadingStats ? '...' : stats?.active_l1_subnets || 0}
                </p>
              </div>
              <div className="p-3 bg-blue-100 rounded-full">
                <Network size={24} className="text-blue-600" />
              </div>
            </div>
          </div>

          {/* Active Legacy Subnets */}
          <div className="bg-white rounded-lg shadow p-6 border-l-4 border-gray-400">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                  Active Legacy Subnets
                </p>
                <p className="text-3xl font-bold text-gray-900 mt-2">
                  {loadingStats ? '...' : stats?.active_legacy_subnets || 0}
                </p>
              </div>
              <div className="p-3 bg-gray-100 rounded-full">
                <Network size={24} className="text-gray-600" />
              </div>
            </div>
          </div>

          {/* Active Validators */}
          <div className="bg-white rounded-lg shadow p-6 border-l-4 border-green-500">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                  Active Validators
                </p>
                <p className="text-3xl font-bold text-gray-900 mt-2">
                  {loadingStats ? '...' : stats?.active_validators || 0}
                </p>
              </div>
              <div className="p-3 bg-green-100 rounded-full">
                <Users size={24} className="text-green-600" />
              </div>
            </div>
          </div>

          {/* Total L1 Validation Fees */}
          <div className="bg-white rounded-lg shadow p-6 border-l-4 border-orange-500">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-semibold text-gray-600 uppercase tracking-wider">
                  L1 Validation Fees
                </p>
                <p className="text-3xl font-bold text-gray-900 mt-2">
                  {loadingStats ? '...' : `${((stats?.total_l1_fees_paid || 0) / 1e9).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })} AVAX`}
                </p>
              </div>
              <div className="p-3 bg-orange-100 rounded-full">
                <Coins size={24} className="text-orange-600" />
              </div>
            </div>
          </div>
        </div>

        {/* Two Column Layout */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Subnet Creation Timeline Chart */}
          <div className="bg-white rounded-lg shadow overflow-hidden">
            {loadingTimeline ? (
              <div className="h-[350px] flex items-center justify-center">
                <p className="text-gray-500">Loading timeline...</p>
              </div>
            ) : (
              <MetricChart
                metricName="Cumulative L1 Subnets"
                data={timeline?.map(t => ({
                  chain_id: 0,
                  metric_name: 'total_l1_subnets',
                  granularity: 'month',
                  period: t.period,
                  value: t.value,
                  computed_at: new Date().toISOString()
                })) || []}
                granularity="month"
              />
            )}
          </div>

          {/* Platform Activity */}
          <div className="bg-white rounded-lg shadow p-6">
            <h2 className="text-xl font-bold text-gray-900 mb-2">Platform Activity</h2>
            <div className="flex items-center justify-between py-3 mb-4 bg-orange-50 rounded-lg px-4 border border-orange-100">
              <span className="text-sm font-semibold text-orange-800">Transactions (7d)</span>
              <span className="text-xl font-bold text-orange-600">{loadingStats ? '...' : (stats?.recent_transactions || 0).toLocaleString()}</span>
            </div>
            <p className="text-sm text-gray-600 mb-4">Transaction breakdown (30d)</p>
            {loadingActivity ? (
              <div className="h-64 flex items-center justify-center">
                <p className="text-gray-500">Loading activity...</p>
              </div>
            ) : platformActivity && platformActivity.length > 0 ? (
              <div className="space-y-1">
                {platformActivity.map((item, idx) => (
                  <div key={idx} className="flex items-center justify-between py-2 border-b border-gray-100 last:border-0">
                    <span className="text-sm font-medium text-gray-700">{item.tx_type}</span>
                    <span className="text-sm font-bold text-gray-900">{item.count.toLocaleString()}</span>
                  </div>
                ))}
              </div>
            ) : (
              <div className="h-64 flex items-center justify-center">
                <p className="text-gray-500">No platform activity data available</p>
              </div>
            )}
          </div>
        </div>

        {/* Latest Blocks and Transactions */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Latest Blocks */}
          <div className="bg-gradient-to-br from-indigo-900 via-indigo-800 to-indigo-900 rounded-lg shadow-xl overflow-hidden">
            <div className="px-6 py-4 border-b border-indigo-700">
              <h2 className="text-xl font-bold text-white">Latest Blocks</h2>
            </div>

            {loadingBlocks ? (
              <div className="p-8 text-center">
                <p className="text-indigo-300">Loading blocks...</p>
              </div>
            ) : recentBlocks && recentBlocks.length > 0 ? (
              <div className="divide-y divide-indigo-700">
                {recentBlocks.map((block) => (
                  <Link
                    key={block.block_number}
                    to={`/p-chain/block/${block.block_number}`}
                    className="block px-6 py-4 hover:bg-indigo-800/50 transition-colors"
                  >
                    <div className="flex items-center gap-4">
                      <div className="flex-shrink-0">
                        <div className="w-10 h-10 rounded-full bg-blue-500 flex items-center justify-center">
                          <span className="text-white font-bold text-sm">P</span>
                        </div>
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center justify-between mb-1">
                          <span className="text-white font-semibold">{block.block_number.toLocaleString()}</span>
                          <span className="text-indigo-300 text-sm">{formatTimestamp(block.block_time)}</span>
                        </div>
                        <div className="flex items-center justify-between text-sm">
                          <span className="text-indigo-300 font-mono truncate">Hash {block.block_hash}...{block.block_hash?.slice(-6)}</span>
                          <span className="text-indigo-300">{block.tx_count} {block.tx_count === 1 ? 'Tx' : 'Txs'}</span>
                        </div>
                      </div>
                    </div>
                  </Link>
                ))}
              </div>
            ) : (
              <div className="p-8 text-center">
                <p className="text-indigo-300">No recent blocks found</p>
              </div>
            )}

            <div className="border-t border-indigo-700">
              <Link
                to="/p-chain/overview"
                className="block px-6 py-3 text-center text-white hover:bg-indigo-800/50 transition-colors font-medium"
              >
                View all Blocks
              </Link>
            </div>
          </div>

          {/* Latest Transactions */}
          <div className="bg-gradient-to-br from-indigo-900 via-indigo-800 to-indigo-900 rounded-lg shadow-xl overflow-hidden">
            <div className="px-6 py-4 border-b border-indigo-700">
              <h2 className="text-xl font-bold text-white mb-4">Latest Transactions</h2>

              {/* Search Bar */}
              <form onSubmit={handleSearch} className="flex gap-3">
                <div className="flex-1 relative">
                  <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 text-indigo-300" size={20} />
                  <input
                    type="text"
                    value={txBlockSearch}
                    onChange={(e) => setTxBlockSearch(e.target.value)}
                    placeholder="Search by Transaction ID or Block Number..."
                    className="w-full pl-10 pr-4 py-2 bg-indigo-800/50 border border-indigo-600 text-white placeholder-indigo-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent outline-none"
                  />
                </div>
                <button
                  type="submit"
                  className="px-6 py-2 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg transition-colors"
                >
                  Search
                </button>
              </form>
              <p className="text-xs text-indigo-300 mt-2">
                Enter a transaction ID (e.g., 22FdhK...) or block number (e.g., 23759061)
              </p>
            </div>

            {loadingTransactions ? (
              <div className="p-8 text-center">
                <p className="text-indigo-300">Loading transactions...</p>
              </div>
            ) : txError ? (
              <div className="p-8 text-center">
                <p className="text-red-300">Error: {String(txError)}</p>
              </div>
            ) : recentTransactions && recentTransactions.length > 0 ? (
              <div className="divide-y divide-indigo-700">
                {recentTransactions.map((tx) => (
                  <Link
                    key={tx.tx_id}
                    to={`/p-chain/tx/${tx.tx_id}`}
                    className="block px-6 py-4 hover:bg-indigo-800/50 transition-colors"
                  >
                    <div className="flex items-center gap-4">
                      <div className="flex-shrink-0">
                        <div className="w-10 h-10 rounded-full bg-blue-500 flex items-center justify-center">
                          <span className="text-white font-bold text-sm">P</span>
                        </div>
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center justify-between mb-1">
                          <span className="text-white font-mono text-sm truncate">{tx.tx_id.substring(0, 12)}...{tx.tx_id.slice(-8)}</span>
                          <span className="text-indigo-300 text-sm">{formatTimestamp(tx.block_time)}</span>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="inline-flex items-center px-2.5 py-0.5 rounded text-xs font-medium bg-blue-500/20 text-blue-300 border border-blue-400/30">
                            {tx.tx_type}
                          </span>
                        </div>
                      </div>
                    </div>
                  </Link>
                ))}
              </div>
            ) : (
              <div className="p-8 text-center">
                <p className="text-indigo-300">No recent transactions found</p>
              </div>
            )}

            <div className="border-t border-indigo-700">
              <button
                className="block w-full px-6 py-3 text-center text-white hover:bg-indigo-800/50 transition-colors font-medium"
              >
                View all Transactions
              </button>
            </div>
          </div>
        </div>

        {/* All Chains Table */}
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-200">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
              <div>
                <h2 className="text-xl font-bold text-gray-900">All Chains</h2>
                <p className="text-sm text-gray-600 mt-1">
                  Showing {displayedChains} of {totalChains} chains (sorted by validators)
                </p>
              </div>
              <div className="flex items-center gap-3">
                {/* Type Filter */}
                <div className="flex rounded-lg border border-gray-300 overflow-hidden">
                  <button
                    onClick={() => setTypeFilter('all')}
                    className={`px-3 py-2 text-sm font-medium transition-colors ${
                      typeFilter === 'all'
                        ? 'bg-blue-600 text-white'
                        : 'bg-white text-gray-700 hover:bg-gray-50'
                    }`}
                  >
                    All
                  </button>
                  <button
                    onClick={() => setTypeFilter('l1')}
                    className={`px-3 py-2 text-sm font-medium border-l border-gray-300 transition-colors ${
                      typeFilter === 'l1'
                        ? 'bg-blue-600 text-white'
                        : 'bg-white text-gray-700 hover:bg-gray-50'
                    }`}
                  >
                    L1s
                  </button>
                  <button
                    onClick={() => setTypeFilter('legacy')}
                    className={`px-3 py-2 text-sm font-medium border-l border-gray-300 transition-colors ${
                      typeFilter === 'legacy'
                        ? 'bg-blue-600 text-white'
                        : 'bg-white text-gray-700 hover:bg-gray-50'
                    }`}
                  >
                    Legacy
                  </button>
                </div>
                {/* Search */}
                <div className="relative">
                  <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" size={18} />
                  <input
                    type="text"
                    placeholder="Search chains..."
                    value={searchTerm}
                    onChange={(e) => setSearchTerm(e.target.value)}
                    className="pl-10 pr-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500 w-full sm:w-64"
                  />
                </div>
              </div>
            </div>
          </div>

          {loadingSubnets ? (
            <div className="p-8 text-center">
              <p className="text-gray-500">Loading chains...</p>
            </div>
          ) : filteredSubnets && filteredSubnets.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Type
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Name
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Subnet ID
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Created Block
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Created
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Validators
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">
                      Fees Paid
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {filteredSubnets.map((subnet, idx) => (
                    <tr key={subnet.subnet_id} className={idx % 2 === 0 ? 'bg-white' : 'bg-gray-50'}>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                          subnet.subnet_type === 'primary' ? 'bg-yellow-100 text-yellow-800 border border-yellow-300' :
                          subnet.subnet_type === 'l1' ? 'bg-blue-100 text-blue-800' :
                          subnet.subnet_type === 'elastic' ? 'bg-purple-100 text-purple-800' :
                          'bg-gray-100 text-gray-800'
                        }`}>
                          {formatSubnetType(subnet.subnet_type)}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <Link
                          to={`/p-chain/subnet/${subnet.subnet_id}`}
                          className="flex items-center gap-2 hover:opacity-80 transition-opacity"
                        >
                          {subnet.logo_url && (
                            <img
                              src={subnet.logo_url}
                              alt={subnet.name || 'Subnet logo'}
                              className="w-6 h-6 rounded-full object-cover"
                              onError={(e) => { e.currentTarget.style.display = 'none'; }}
                            />
                          )}
                          <span className={`text-sm ${(subnet.name || subnet.subnet_type === 'primary') ? 'font-medium text-blue-600 hover:text-blue-800' : 'font-mono text-blue-600 hover:text-blue-800'}`}>
                            {subnet.name || (subnet.subnet_type === 'primary' ? 'Primary Network' : truncateHash(subnet.subnet_id, 10))}
                          </span>
                        </Link>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="flex items-center gap-2">
                          <code className="text-xs font-mono text-gray-900">
                            {truncateHash(subnet.subnet_id, 10)}
                          </code>
                          <button
                            onClick={() => copyToClipboard(subnet.subnet_id)}
                            className="text-gray-400 hover:text-gray-600 transition-colors"
                            title="Copy full subnet ID"
                          >
                            <Copy size={14} />
                          </button>
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <span className="text-sm text-gray-900">
                          {subnet.created_block.toLocaleString()}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <span className="text-sm text-gray-600">
                          {formatTimestamp(subnet.created_time)}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <span className="text-sm font-semibold text-gray-900">
                          {subnet.validator_count}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        {subnet.subnet_type === 'l1' && subnet.total_fees_paid && subnet.total_fees_paid > 0 ? (
                          <span className="text-sm font-semibold text-orange-600">
                            {formatAVAX(subnet.total_fees_paid)} AVAX
                          </span>
                        ) : (
                          <span className="text-sm text-gray-400">-</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="p-8 text-center">
              <p className="text-gray-500">
                {searchTerm ? 'No chains found matching your search.' : 'No chains found'}
              </p>
            </div>
          )}

          {/* Show All / Show Less Button */}
          {totalChains > 50 && filteredSubnets && filteredSubnets.length > 0 && (
            <div className="px-6 py-4 border-t border-gray-200 text-center">
              <button
                onClick={() => setShowAllChains(!showAllChains)}
                className="text-blue-600 hover:text-blue-800 text-sm font-medium transition-colors"
              >
                {showAllChains ? `Show Less` : `Show All ${totalChains} Chains`}
              </button>
            </div>
          )}
        </div>
      </div>
    </PageTransition>
  );
}

export default PChainOverview;
