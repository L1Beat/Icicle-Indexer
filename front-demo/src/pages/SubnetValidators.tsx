import { useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { useValidators, useChains } from '../lib/hooks';
import type { Validator, ChainInfo } from '../lib/api';
import {
  ArrowLeft,
  Search,
  Server,
  Clock,
  CheckCircle,
  XCircle,
  Activity,
  Wallet,
  Copy,
  ExternalLink,
  Globe,
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  ChevronRight,
} from 'lucide-react';

/**
 * Resolve the value shown in the sortable amount column ("Stake" for
 * Primary Network / legacy subnets, "Balance" for L1). All amounts are nAVAX.
 *
 * - L1 validators carry an explicit `balance`.
 * - Primary Network validators have no `balance` — their stake is `weight`.
 * - Legacy subnet validators carry the Primary Network stake in `primary_stake`.
 */
function amountForValidator(v: Validator, subnetType: string | undefined): number {
  if (subnetType === 'primary') return v.weight;
  if (subnetType === 'legacy') return v.primary_stake ?? 0;
  return v.balance ?? 0;
}

/**
 * Group a whole-token decimal string (e.g. "2999999.5") with thousands
 * separators, keeping at most two fractional digits. Done via string ops so
 * very large integer parts don't lose precision through Number().
 */
function formatTokenAmount(s: string | undefined): string {
  if (!s) return '0';
  const [intPart, fracPart = ''] = s.split('.');
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = fracPart.slice(0, 2).replace(/0+$/, '');
  return frac ? `${grouped}.${frac}` : grouped;
}

function SubnetValidators() {
  const { subnetId } = useParams<{ subnetId: string }>();
  const navigate = useNavigate();
  const [searchTerm, setSearchTerm] = useState('');
  const [showAll, setShowAll] = useState(false);
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc' | null>('desc');

  // Subnet header info. [0] is the requested subnet; the server enriches it
  // with registry metadata + validator stats.
  const { data: chains, isLoading: loadingDetails } = useChains(
    { subnetId },
    !!subnetId,
  );
  const subnetDetails: ChainInfo | undefined = chains?.[0];
  const subnetType = subnetDetails?.chain_type;

  // Validators list. One call returns the correct rows for any subnet type;
  // the server handles primary / legacy / l1 differences + Primary Network
  // enrichment, and the hook polls every 30s.
  const {
    data: validators,
    isLoading: loadingValidators,
    error: validatorsError,
  } = useValidators(subnetId, !!subnetDetails);

  const formatWeight = (weight: number) => {
    const num = weight;
    if (num >= 1e9) return `${(num / 1e9).toFixed(2)}B`;
    if (num >= 1e6) return `${(num / 1e6).toFixed(2)}M`;
    if (num >= 1e3) return `${(num / 1e3).toFixed(2)}K`;
    return num.toFixed(0);
  };

  const formatBalance = (balance: number) => {
    return (balance / 1e9).toLocaleString(undefined, { maximumFractionDigits: 2 }) + ' AVAX';
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

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
  };

  const allFilteredValidators = validators?.filter(v =>
    v.node_id.toLowerCase().includes(searchTerm.toLowerCase()) ||
    v.validation_id.toLowerCase().includes(searchTerm.toLowerCase())
  );

  // PoS chains return a real per-validator staked_amount (whole tokens). When
  // present we show "Staked" + token symbol; otherwise we fall back to the raw
  // weight / balance view used for PoA and legacy subnets.
  const isPoS = validators?.some((v) => v.staked_amount != null) ?? false;
  const stakeKey = (v: Validator) =>
    isPoS ? parseFloat(v.staked_amount ?? '0') : amountForValidator(v, subnetType);

  const sortedValidators = allFilteredValidators?.slice().sort((a, b) => {
    if (!sortOrder) return 0;
    const aBalance = stakeKey(a);
    const bBalance = stakeKey(b);
    return sortOrder === 'desc' ? bBalance - aBalance : aBalance - bBalance;
  });

  const filteredValidators = showAll ? sortedValidators : sortedValidators?.slice(0, 10);
  const totalValidators = allFilteredValidators?.length || 0;
  const displayedValidators = filteredValidators?.length || 0;

  const handleSortToggle = () => {
    setSortOrder(current => {
      if (current === 'desc') return 'asc';
      if (current === 'asc') return null;
      return 'desc';
    });
  };

  if (loadingDetails) {
    return (
      <div className="p-8 flex items-center justify-center min-h-[400px]">
        <p className="text-gray-500">Loading subnet details...</p>
      </div>
    );
  }

  if (!subnetDetails) {
    return (
      <div className="p-8 text-center">
        <h2 className="text-2xl font-bold text-gray-900">Subnet Not Found</h2>
        <Link to="/p-chain/overview" className="text-blue-600 hover:text-blue-800 mt-4 inline-block">
          ← Back to Overview
        </Link>
      </div>
    );
  }

  return (
    <PageTransition>
      <div className="p-8 space-y-6">
        {/* Header with Back Button */}
        <div>
          <Link
            to="/p-chain/overview"
            className="inline-flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4 transition-colors"
          >
            <ArrowLeft size={20} />
            Back to Overview
          </Link>

          <div className="bg-white rounded-lg shadow-sm border border-gray-200 p-6">
            <div className="flex items-start justify-between">
              <div className="flex-1">
                <div className="flex items-start gap-4">
                  {subnetDetails.logo_url && (
                    <img
                      src={subnetDetails.logo_url}
                      alt={subnetDetails.name || 'Subnet logo'}
                      className="w-16 h-16 rounded-lg object-cover border border-gray-200"
                      onError={(e) => { e.currentTarget.style.display = 'none'; }}
                    />
                  )}
                  <div className="flex-1">
                    <h1 className="text-2xl font-bold text-gray-900 flex items-center gap-3">
                      {subnetDetails.name || (subnetType === 'primary' ? 'Primary Network' : 'Subnet Details')}
                      <span className={`px-3 py-1 text-sm font-medium rounded-full ${
                        subnetType === 'primary' ? 'bg-yellow-100 text-yellow-800 border border-yellow-300' :
                        subnetType === 'l1' ? 'bg-blue-100 text-blue-800' :
                        subnetType === 'elastic' ? 'bg-purple-100 text-purple-800' :
                        'bg-gray-100 text-gray-800'
                      }`}>
                        {formatSubnetType(subnetType ?? '')}
                      </span>
                    </h1>
                    {subnetDetails.description && (
                      <p className="mt-2 text-sm text-gray-600 max-w-2xl">{subnetDetails.description}</p>
                    )}
                    <div className="mt-3 space-y-1">
                      <p className="text-sm text-gray-500 font-mono">Subnet ID: {subnetDetails.subnet_id}</p>
                      {subnetDetails.chain_id && (
                        <p className="text-sm text-gray-500 font-mono">Chain ID: {subnetDetails.chain_id}</p>
                      )}
                      {subnetDetails.website_url && (
                        <a
                          href={subnetDetails.website_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-sm text-blue-600 hover:text-blue-800 transition-colors"
                        >
                          <Globe size={14} />
                          {subnetDetails.website_url.replace(/^https?:\/\//, '')}
                          <ExternalLink size={12} />
                        </a>
                      )}
                    </div>
                  </div>
                </div>
              </div>

              <div className="flex gap-6 text-right ml-6">
                <div>
                  <p className="text-sm text-gray-500 uppercase tracking-wide font-semibold">Validators</p>
                  <p className="text-2xl font-bold text-gray-900">{subnetDetails.validator_count ?? 0}</p>
                </div>
                <div>
                  <p className="text-sm text-gray-500 uppercase tracking-wide font-semibold">
                    {subnetDetails.total_staked_tokens ? 'Total Staked' : 'Total Weight'}
                  </p>
                  <p className="text-2xl font-bold text-gray-900">
                    {subnetDetails.total_staked_tokens
                      ? `${formatTokenAmount(subnetDetails.total_staked_tokens)} ${subnetDetails.network_token?.symbol ?? ''}`.trim()
                      : formatWeight(subnetDetails.total_staked ?? 0)}
                  </p>
                </div>
              </div>
            </div>

            <div className="mt-6 grid grid-cols-1 md:grid-cols-3 gap-4 pt-6 border-t border-gray-100">
              {subnetDetails.converted_block !== undefined && (
                <div className="flex items-center gap-3 text-sm text-gray-600">
                  <Server size={18} className="text-gray-400" />
                  <span>Converted at block <strong>{subnetDetails.converted_block.toLocaleString()}</strong></span>
                </div>
              )}
              {subnetDetails.converted_time && (
                <div className="flex items-center gap-3 text-sm text-gray-600">
                  <Clock size={18} className="text-gray-400" />
                  <span>Converted on <strong>{new Date(subnetDetails.converted_time).toLocaleDateString()}</strong></span>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Validators List */}
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <div className="px-6 py-4 border-b border-gray-200 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
            <div>
              <h2 className="text-lg font-bold text-gray-900">Validators</h2>
              <p className="text-sm text-gray-600 mt-1">
                Showing {displayedValidators} of {totalValidators} validators
                <span className="text-gray-400 ml-2">• Click a row for details</span>
              </p>
            </div>
            <div className="relative">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" size={18} />
              <input
                type="text"
                placeholder="Search Node ID..."
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
                className="pl-10 pr-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500 w-full sm:w-64"
              />
            </div>
          </div>

          {validatorsError ? (
            <div className="p-12 text-center">
              <p className="text-red-500 font-semibold">Error loading validators:</p>
              <pre className="text-red-400 text-sm mt-2 whitespace-pre-wrap">{String(validatorsError)}</pre>
            </div>
          ) : loadingValidators ? (
            <div className="p-12 text-center">
              <p className="text-gray-500">Loading validators...</p>
            </div>
          ) : filteredValidators && filteredValidators.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider min-w-[350px]">Node ID</th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">Status</th>
                    <th className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider">Weight</th>
                    <th
                      className="px-6 py-3 text-right text-xs font-semibold text-gray-700 uppercase tracking-wider cursor-pointer hover:bg-gray-100 transition-colors select-none"
                      onClick={handleSortToggle}
                    >
                      <div className="flex items-center justify-end gap-1">
                        {isPoS ? 'Staked' : subnetType === 'primary' || subnetType === 'legacy' ? 'Stake' : 'Balance'}
                        {sortOrder === 'desc' && <ArrowDown size={14} />}
                        {sortOrder === 'asc' && <ArrowUp size={14} />}
                        {!sortOrder && <ArrowUpDown size={14} className="text-gray-400" />}
                      </div>
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-semibold text-gray-700 uppercase tracking-wider">Registered</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {filteredValidators.map((validator) => (
                    <tr
                      key={validator.node_id + validator.validation_id}
                      className={`hover:bg-blue-50 transition-colors cursor-pointer ${!validator.active ? 'opacity-60' : ''}`}
                      onClick={() => navigate(`/p-chain/subnet/${subnetId}/validator/${encodeURIComponent(validator.node_id)}`)}
                    >
                      <td className="px-6 py-4 whitespace-nowrap min-w-[350px]">
                        <div className="flex flex-col gap-1">
                          <div className="flex items-center gap-2">
                            <span className={`text-sm font-medium font-mono ${validator.active ? 'text-gray-900' : 'text-gray-500'}`}>
                              {validator.node_id || <span className="text-gray-400 italic">Unknown</span>}
                            </span>
                            {validator.node_id && (
                              <button
                                onClick={(e) => { e.stopPropagation(); copyToClipboard(validator.node_id); }}
                                className="text-gray-400 hover:text-gray-600 transition-colors"
                                title="Copy Node ID"
                              >
                                <Copy size={12} />
                              </button>
                            )}
                            <ChevronRight size={14} className="text-gray-300" />
                          </div>
                          {validator.validation_id && (
                            <div className="flex items-center gap-2">
                              <span className="text-xs text-gray-500 font-mono" title="Validation ID">
                                {validator.validation_id.substring(0, 12)}...
                              </span>
                              <button
                                onClick={(e) => { e.stopPropagation(); copyToClipboard(validator.validation_id); }}
                                className="text-gray-400 hover:text-gray-600 transition-colors"
                                title="Copy Validation ID"
                              >
                                <Copy size={12} />
                              </button>
                            </div>
                          )}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="flex items-center gap-2">
                          {validator.active ? (
                            <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-800">
                              <CheckCircle size={12} /> Active
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-800">
                              <XCircle size={12} /> Inactive
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="flex items-center justify-end gap-2">
                          <Activity size={16} className="text-gray-400" />
                          <span className={`text-sm font-semibold ${validator.active ? 'text-gray-900' : 'text-gray-500'}`}>{formatWeight(validator.weight)}</span>
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right">
                        <div className="flex items-center justify-end gap-2">
                          <Wallet size={16} className="text-gray-400" />
                          <span className={`text-sm ${validator.active ? 'text-gray-900' : 'text-gray-500'}`}>
                            {validator.staked_amount != null
                              ? `${formatTokenAmount(validator.staked_amount)} ${validator.staked_token ?? ''}`.trim()
                              : formatBalance(amountForValidator(validator, subnetType))}
                          </span>
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="text-xs text-gray-500 space-y-1">
                          {validator.created_time && (
                            <p>{new Date(validator.created_time).toLocaleDateString()}</p>
                          )}
                          {validator.tx_type && (
                            <p className="text-gray-400">{validator.tx_type}</p>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="p-12 text-center">
              <p className="text-gray-500">No validators found matching your search.</p>
            </div>
          )}

          {/* Show All / Show Less Button */}
          {totalValidators > 10 && filteredValidators && filteredValidators.length > 0 && (
            <div className="px-6 py-4 border-t border-gray-200 text-center">
              <button
                onClick={() => setShowAll(!showAll)}
                className="text-blue-600 hover:text-blue-800 text-sm font-medium transition-colors"
              >
                {showAll ? `Show Less` : `Show All ${totalValidators} Validators`}
              </button>
            </div>
          )}
        </div>
      </div>
    </PageTransition>
  );
}

export default SubnetValidators;
