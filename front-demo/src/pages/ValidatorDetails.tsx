import { useQuery } from '@tanstack/react-query';
import { createClient } from '@clickhouse/client-web';
import { useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import PageTransition from '../components/PageTransition';
import { useClickhouseUrl } from '../hooks/useClickhouseUrl';
import {
  ArrowLeft,
  Server,
  CheckCircle,
  XCircle,
  Activity,
  Copy,
  Key,
  Hash,
  Calendar,
  TrendingDown,
  Hourglass,
  ExternalLink,
  Clock,
  Wallet,
  Shield,
  Plus,
  ArrowUpCircle,
} from 'lucide-react';

// Constant burn rate for L1 validators: 512 nAVAX/sec = 44,236,800 nAVAX/day
const DAILY_BURN_NAVAX = 512 * 86400;

interface ValidatorData {
  validation_id: string;
  node_id: string;
  subnet_id: string;
  weight: string;
  balance: string;
  start_time: string;
  end_time: string;
  uptime_percentage: number;
  active: boolean;
  last_updated: string;
  // From l1_validator_state (correct computed values)
  initial_deposit: string;
  total_topups: string;
  refund_amount: string;
  fees_paid: string;
  // From l1_validator_history
  created_tx_type?: string;
  created_tx_id?: string;
  created_block?: number;
  created_time?: string;
  initial_balance?: string;
  initial_weight?: string;
  bls_public_key?: string;
  remaining_balance_owner?: string;
  // Primary Network data (for legacy subnet validators)
  primary_stake?: string;
  primary_uptime?: number;
}

interface SubnetInfo {
  subnet_id: string;
  subnet_type: string;
  name?: string;
}

interface BalanceTransaction {
  tx_id: string;
  tx_type: string;
  block_height: number;
  block_time: string;
  amount: string;
  effect: 'deposit' | 'top-up' | 'refund';
  refund_address?: string;
}

function ValidatorDetails() {
  const { subnetId, nodeId } = useParams<{ subnetId: string; nodeId: string }>();
  const { url } = useClickhouseUrl();

  const clickhouse = useMemo(() => createClient({
    url,
    username: "anonymous",
  }), [url]);

  // Fetch subnet info
  const { data: subnetInfo } = useQuery<SubnetInfo>({
    queryKey: ['subnet-info', subnetId, url],
    queryFn: async () => {
      const result = await clickhouse.query({
        query: `
          SELECT
            s.subnet_id,
            s.subnet_type,
            NULLIF(r.name, '') as name
          FROM subnets AS s FINAL
          LEFT JOIN l1_registry AS r FINAL ON s.subnet_id = r.subnet_id
          WHERE s.subnet_id = {subnetId:String}
          LIMIT 1
        `,
        format: 'JSONEachRow',
        query_params: { subnetId },
      });
      const data = await result.json<SubnetInfo[]>();
      return data[0];
    },
  });

  // Fetch validator details - pull from l1_validator_state (correct data) + l1_validator_history (registration info)
  const { data: validator, isLoading, error } = useQuery<ValidatorData>({
    queryKey: ['validator-details', subnetId, nodeId, url],
    queryFn: async () => {
      const result = await clickhouse.query({
        query: `
          SELECT
            v.validation_id,
            COALESCE(NULLIF(v.node_id, ''), {nodeId:String}) as node_id,
            v.subnet_id,
            toString(v.weight) as weight,
            toString(v.balance) as balance,
            formatDateTime(v.start_time, '%Y-%m-%d %H:%i:%s') as start_time,
            formatDateTime(v.end_time, '%Y-%m-%d %H:%i:%s') as end_time,
            v.uptime_percentage,
            v.active,
            formatDateTime(v.last_updated, '%Y-%m-%d %H:%i:%s') as last_updated,
            toString(v.initial_deposit) as initial_deposit,
            toString(v.total_topups) as total_topups,
            toString(v.refund_amount) as refund_amount,
            toString(v.fees_paid) as fees_paid,
            h.created_tx_type,
            h.created_tx_id,
            h.created_block,
            formatDateTime(h.created_time, '%Y-%m-%d %H:%i:%s') as created_time,
            toString(COALESCE(h.initial_balance, 0)) as initial_balance,
            toString(COALESCE(h.initial_weight, 0)) as initial_weight,
            h.bls_public_key,
            h.remaining_balance_owner,
            toString(COALESCE(pn.weight, 0)) as primary_stake,
            COALESCE(pn.uptime_percentage, 0) as primary_uptime
          FROM (SELECT * FROM l1_validator_state FINAL WHERE subnet_id = {subnetId:String} AND node_id = {nodeId:String} LIMIT 1) v
          LEFT JOIN (SELECT * FROM l1_validator_history FINAL WHERE subnet_id = {subnetId:String} AND node_id = {nodeId:String} LIMIT 1) h
            ON v.validation_id = h.validation_id
          LEFT JOIN (
            SELECT node_id, weight, uptime_percentage
            FROM l1_validator_state FINAL
            WHERE subnet_id = '11111111111111111111111111111111LpoYY'
              AND node_id = {nodeId:String}
            LIMIT 1
          ) pn ON v.node_id = pn.node_id
          LIMIT 1
        `,
        format: 'JSONEachRow',
        query_params: { subnetId, nodeId },
      });
      const data = await result.json<ValidatorData[]>();
      if (data.length === 0) {
        throw new Error('Validator not found');
      }
      return data[0];
    },
  });

  // Fetch balance transactions
  const { data: balanceTransactions, error: balanceTxError, isLoading: loadingBalanceTx } = useQuery<BalanceTransaction[]>({
    queryKey: ['validator-balance-txs', validator?.validation_id, validator?.node_id, subnetId, url],
    queryFn: async () => {
      if (!validator) return [];

      const hasValidationId = validator.validation_id && validator.validation_id.length > 0;
      const nodeIdEscaped = validator.node_id.replace(/'/g, "\\'");
      const subnetIdEscaped = subnetId?.replace(/'/g, "\\'") || '';

      const depositsQuery = hasValidationId
        ? `
          SELECT tx_id, tx_type, block_number as block_height,
            formatDateTime(block_time, '%Y-%m-%d %H:%i:%s') as block_time,
            toString(amount) as amount, '' as refund_address
          FROM l1_validator_balance_txs FINAL
          WHERE validation_id = '${validator.validation_id.replace(/'/g, "\\'")}'
          ORDER BY block_number ASC
        `
        : `
          SELECT tx_id, tx_type, block_number as block_height,
            formatDateTime(block_time, '%Y-%m-%d %H:%i:%s') as block_time,
            toString(amount) as amount, '' as refund_address
          FROM l1_validator_balance_txs FINAL
          WHERE node_id = '${nodeIdEscaped}' AND subnet_id = '${subnetIdEscaped}'
          ORDER BY block_number ASC
        `;

      const depositsResult = await clickhouse.query({ query: depositsQuery, format: 'JSONEachRow' });
      const depositTxs = await depositsResult.json<{tx_id: string; tx_type: string; block_height: number; block_time: string; amount: string; refund_address: string}[]>();

      // Fetch refund transactions
      const refundQuery = hasValidationId
        ? `
          SELECT tx_id, 'DisableL1Validator' as tx_type, block_number as block_height,
            formatDateTime(block_time, '%Y-%m-%d %H:%i:%s') as block_time,
            toString(refund_amount) as amount, refund_address
          FROM l1_validator_refunds
          WHERE validation_id = '${validator.validation_id.replace(/'/g, "\\'")}'
        `
        : `
          SELECT r.tx_id, 'DisableL1Validator' as tx_type, r.block_number as block_height,
            formatDateTime(r.block_time, '%Y-%m-%d %H:%i:%s') as block_time,
            toString(r.refund_amount) as amount, r.refund_address
          FROM l1_validator_refunds r
          JOIN l1_validator_history h FINAL ON r.validation_id = h.validation_id AND h.p_chain_id = r.p_chain_id
          WHERE h.node_id = '${nodeIdEscaped}' AND h.subnet_id = '${subnetIdEscaped}'
        `;

      const refundResult = await clickhouse.query({ query: refundQuery, format: 'JSONEachRow' });
      const refundTxs = await refundResult.json<{tx_id: string; tx_type: string; block_height: number; block_time: string; amount: string; refund_address: string}[]>();

      const allTxs = [...depositTxs, ...refundTxs].sort((a, b) => a.block_height - b.block_height);

      return allTxs.map(tx => ({
        ...tx,
        effect: tx.tx_type === 'DisableL1Validator' ? 'refund' as const :
                tx.tx_type === 'IncreaseL1ValidatorBalance' ? 'top-up' as const : 'deposit' as const,
      }));
    },
    enabled: !!validator?.node_id,
  });

  const topUps = balanceTransactions?.filter(tx => tx.effect === 'top-up') || [];

  // Use values from l1_validator_state (correct computed values from the indexer)
  const initialDeposit = parseFloat(validator?.initial_deposit || '0');
  const totalTopUps = parseFloat(validator?.total_topups || '0');
  const currentBalance = parseFloat(validator?.balance || '0');
  const refundAmount = parseFloat(validator?.refund_amount || '0');
  const feesPaid = parseFloat(validator?.fees_paid || '0');
  const totalFunded = initialDeposit + totalTopUps;

  const isPrimaryNetwork = subnetId === '11111111111111111111111111111111LpoYY';
  const isLegacy = subnetInfo?.subnet_type === 'legacy';
  const isL1 = !isPrimaryNetwork && !isLegacy;

  // Daily burn rate: constant for L1 validators, not applicable for Primary Network/legacy
  const dailyBurnRate = isL1 ? DAILY_BURN_NAVAX : 0;
  const daysUntilEmpty = dailyBurnRate > 0 && currentBalance > 0 ? currentBalance / dailyBurnRate : 0;

  const daysActive = validator?.start_time && validator.start_time !== '1970-01-01 00:00:00'
    ? Math.max(1, Math.floor((Date.now() - new Date(validator.start_time).getTime()) / (1000 * 60 * 60 * 24)))
    : 0;

  const formatWeight = (weight: string) => {
    const num = parseFloat(weight);
    if (num >= 1e9) return `${(num / 1e9).toFixed(2)}B`;
    if (num >= 1e6) return `${(num / 1e6).toFixed(2)}M`;
    if (num >= 1e3) return `${(num / 1e3).toFixed(2)}K`;
    return num.toFixed(0);
  };

  const formatBalanceRaw = (balance: string) => {
    const num = parseFloat(balance);
    return (num / 1e9).toLocaleString(undefined, { maximumFractionDigits: 6 });
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
  };

  if (isLoading) {
    return (
      <div className="p-8 flex items-center justify-center min-h-[400px]">
        <p className="text-gray-500">Loading validator details...</p>
      </div>
    );
  }

  if (error || !validator) {
    return (
      <div className="p-8 text-center">
        <h2 className="text-2xl font-bold text-gray-900">Validator Not Found</h2>
        <p className="text-gray-500 mt-2">The validator could not be found in this subnet.</p>
        <Link to={`/p-chain/subnet/${subnetId}`} className="text-blue-600 hover:text-blue-800 mt-4 inline-block">
          Back to Subnet
        </Link>
      </div>
    );
  }

  // Fallback node_id to URL param if query returns empty
  const displayNodeId = validator.node_id || nodeId || '';
  const hasPrimaryData = validator.primary_stake !== undefined && parseFloat(validator.primary_stake || '0') > 0;
  const hasCurrentState = validator.validation_id && validator.validation_id.length > 0;
  const hasUptime = validator.uptime_percentage > 0 || (isLegacy && hasPrimaryData);

  return (
    <PageTransition>
      <div className="p-8 space-y-6 max-w-5xl mx-auto">
        {/* Breadcrumb Navigation */}
        <div className="flex items-center gap-2 text-sm text-gray-500">
          <Link to="/p-chain/overview" className="hover:text-gray-900 transition-colors">
            P-Chain
          </Link>
          <span>/</span>
          <Link to={`/p-chain/subnet/${subnetId}`} className="hover:text-gray-900 transition-colors">
            {subnetInfo?.name || subnetId?.substring(0, 12) + '...'}
          </Link>
          <span>/</span>
          <span className="text-gray-900">Validator</span>
        </div>

        {/* Back Button */}
        <Link
          to={`/p-chain/subnet/${subnetId}`}
          className="inline-flex items-center gap-2 text-gray-600 hover:text-gray-900 transition-colors"
        >
          <ArrowLeft size={20} />
          Back to Validators
        </Link>

        {/* Header Card */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden">
          <div className="p-6 border-b border-gray-100">
            <div className="flex items-start justify-between">
              <div className="flex items-center gap-4">
                <div className={`p-3 rounded-xl ${validator.active ? 'bg-green-100' : 'bg-red-100'}`}>
                  <Server size={28} className={validator.active ? 'text-green-600' : 'text-red-600'} />
                </div>
                <div>
                  <h1 className="text-2xl font-bold text-gray-900">Validator Details</h1>
                  <p className="text-sm text-gray-500 font-mono mt-1">{displayNodeId}</p>
                </div>
              </div>
              <div className={`px-4 py-2 rounded-full text-sm font-semibold flex items-center gap-2 ${
                validator.active
                  ? 'bg-green-100 text-green-800 border border-green-200'
                  : 'bg-red-100 text-red-800 border border-red-200'
              }`}>
                {validator.active ? <CheckCircle size={16} /> : <XCircle size={16} />}
                {validator.active ? 'Active' : 'Inactive'}
              </div>
            </div>
          </div>

          {/* Quick Stats */}
          <div className={`grid grid-cols-2 ${isLegacy && hasPrimaryData ? 'md:grid-cols-5' : hasUptime ? 'md:grid-cols-4' : 'md:grid-cols-3'} divide-x divide-y md:divide-y-0 divide-gray-100`}>
            {isLegacy && hasPrimaryData && (
              <div className="p-6 text-center">
                <p className="text-sm text-gray-500 mb-1">Staked</p>
                <p className="text-2xl font-bold text-gray-900">{formatBalanceRaw(validator.primary_stake || '0')} AVAX</p>
              </div>
            )}
            <div className="p-6 text-center">
              <p className="text-sm text-gray-500 mb-1">Weight</p>
              <p className="text-2xl font-bold text-gray-900">{formatWeight(validator.weight)}</p>
            </div>
            {isL1 && (
              <div className="p-6 text-center">
                <p className="text-sm text-gray-500 mb-1">Balance</p>
                <p className="text-2xl font-bold text-gray-900">{formatBalanceRaw(validator.balance)} AVAX</p>
              </div>
            )}
            {hasUptime && (
              <div className="p-6 text-center">
                <p className="text-sm text-gray-500 mb-1">Uptime</p>
                <p className="text-2xl font-bold text-gray-900">
                  {isLegacy && hasPrimaryData
                    ? `${(validator.primary_uptime! * 100).toFixed(2)}%`
                    : `${(validator.uptime_percentage * 100).toFixed(2)}%`}
                </p>
              </div>
            )}
            <div className="p-6 text-center">
              <p className="text-sm text-gray-500 mb-1">Days Active</p>
              <p className="text-2xl font-bold text-gray-900">{daysActive > 0 ? daysActive : '-'}</p>
            </div>
          </div>
        </div>

        {/* Identifiers Section */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
          <h2 className="text-lg font-bold text-gray-900 mb-4 flex items-center gap-2">
            <Key size={20} className="text-gray-400" />
            Identifiers
          </h2>
          <div className="space-y-4">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 p-4 bg-gray-50 rounded-lg">
              <span className="text-sm font-medium text-gray-600 flex-shrink-0">Node ID</span>
              <div className="flex items-center gap-2 min-w-0">
                <span className="text-sm font-mono text-gray-900 bg-white px-3 py-1.5 rounded border border-gray-200 break-all">
                  {displayNodeId}
                </span>
                <button onClick={() => copyToClipboard(displayNodeId)} className="p-2 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors flex-shrink-0" title="Copy">
                  <Copy size={16} />
                </button>
              </div>
            </div>

            {validator.validation_id && (
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 p-4 bg-gray-50 rounded-lg">
                <span className="text-sm font-medium text-gray-600 flex-shrink-0">Validation ID</span>
                <div className="flex items-center gap-2 min-w-0">
                  <span className="text-sm font-mono text-gray-900 bg-white px-3 py-1.5 rounded border border-gray-200 break-all">
                    {validator.validation_id}
                  </span>
                  <button onClick={() => copyToClipboard(validator.validation_id)} className="p-2 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors flex-shrink-0" title="Copy">
                    <Copy size={16} />
                  </button>
                </div>
              </div>
            )}

            {validator.bls_public_key && validator.bls_public_key.length > 2 && (
              <div className="flex flex-col gap-2 p-4 bg-gray-50 rounded-lg">
                <span className="text-sm font-medium text-gray-600">BLS Public Key</span>
                <div className="flex items-start gap-2">
                  <code className="text-xs font-mono text-gray-900 bg-white px-3 py-2 rounded border border-gray-200 break-all flex-1">
                    {validator.bls_public_key}
                  </code>
                  <button onClick={() => copyToClipboard(validator.bls_public_key || '')} className="p-2 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors flex-shrink-0" title="Copy">
                    <Copy size={16} />
                  </button>
                </div>
              </div>
            )}

            {validator.remaining_balance_owner && validator.remaining_balance_owner.length > 2 && (
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 p-4 bg-gray-50 rounded-lg">
                <span className="text-sm font-medium text-gray-600">Remaining Balance Owner</span>
                <div className="flex items-center gap-2">
                  <code className="text-sm font-mono text-gray-900 bg-white px-3 py-1.5 rounded border border-gray-200 break-all">
                    {validator.remaining_balance_owner}
                  </code>
                  <button onClick={() => copyToClipboard(validator.remaining_balance_owner || '')} className="p-2 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors" title="Copy">
                    <Copy size={16} />
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Current State Section */}
        {hasCurrentState && (
          <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
            <h2 className="text-lg font-bold text-gray-900 mb-4 flex items-center gap-2">
              <Activity size={20} className="text-gray-400" />
              Current State
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {isLegacy && hasPrimaryData && (
                <div className="p-4 bg-blue-50 rounded-lg">
                  <div className="flex items-center gap-2 text-sm text-blue-600 mb-1">
                    <Wallet size={14} />
                    Primary Network Stake
                  </div>
                  <p className="text-xl font-bold text-blue-700">{formatBalanceRaw(validator.primary_stake || '0')} AVAX</p>
                </div>
              )}
              <div className="p-4 bg-gray-50 rounded-lg">
                <div className="flex items-center gap-2 text-sm text-gray-500 mb-1">
                  <Shield size={14} />
                  {isLegacy ? 'Subnet Weight' : 'Weight'}
                </div>
                <p className="text-xl font-bold text-gray-900">{formatWeight(validator.weight)}</p>
                <p className="text-xs text-gray-400 mt-1">{parseFloat(validator.weight).toLocaleString()}</p>
              </div>
              {isL1 && (
                <div className="p-4 bg-gray-50 rounded-lg">
                  <div className="flex items-center gap-2 text-sm text-gray-500 mb-1">
                    <Wallet size={14} />
                    Balance
                  </div>
                  <p className="text-xl font-bold text-gray-900">{formatBalanceRaw(validator.balance)} AVAX</p>
                  <p className="text-xs text-gray-400 mt-1">{parseFloat(validator.balance).toLocaleString()} nAVAX</p>
                </div>
              )}
              {hasUptime && (
                <div className="p-4 bg-gray-50 rounded-lg">
                  <div className="flex items-center gap-2 text-sm text-gray-500 mb-1">
                    <Activity size={14} />
                    Uptime
                  </div>
                  <p className="text-xl font-bold text-gray-900">
                    {isLegacy && hasPrimaryData
                      ? `${(validator.primary_uptime! * 100).toFixed(2)}%`
                      : `${(validator.uptime_percentage * 100).toFixed(2)}%`}
                  </p>
                </div>
              )}
              <div className="p-4 bg-gray-50 rounded-lg">
                <div className="flex items-center gap-2 text-sm text-gray-500 mb-1">
                  <Clock size={14} />
                  Last Updated
                </div>
                <p className="text-lg font-semibold text-gray-900">
                  {new Date(validator.last_updated).toLocaleString()}
                </p>
              </div>
            </div>
          </div>
        )}

        {/* Time Period Section */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
          <h2 className="text-lg font-bold text-gray-900 mb-4 flex items-center gap-2">
            <Calendar size={20} className="text-gray-400" />
            Time Period
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="p-4 bg-gray-50 rounded-lg">
              <p className="text-sm text-gray-500 mb-1">Start Time</p>
              <p className="text-lg font-semibold text-gray-900">
                {validator.start_time && validator.start_time !== '1970-01-01 00:00:00'
                  ? new Date(validator.start_time).toLocaleString()
                  : '-'}
              </p>
            </div>
            <div className="p-4 bg-gray-50 rounded-lg">
              <p className="text-sm text-gray-500 mb-1">End Time</p>
              <p className="text-lg font-semibold text-gray-900">
                {validator.end_time && validator.end_time !== '1970-01-01 00:00:00'
                  ? new Date(validator.end_time).toLocaleString()
                  : 'No end time set'}
              </p>
            </div>
          </div>
        </div>

        {/* Fee Tracking Section - only for L1 validators */}
        {isL1 && (totalFunded > 0 || currentBalance > 0 || (balanceTransactions && balanceTransactions.length > 0)) && (
          <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
            <h2 className="text-lg font-bold text-gray-900 mb-4 flex items-center gap-2">
              <TrendingDown size={20} className="text-gray-400" />
              Fee Tracking
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-6">
              <div className="p-4 bg-gray-50 rounded-lg">
                <p className="text-sm text-gray-500 mb-1">Initial Deposit</p>
                <p className="text-xl font-bold text-gray-900">
                  {formatBalanceRaw(initialDeposit.toString())} AVAX
                </p>
              </div>
              <div className="p-4 bg-blue-50 rounded-lg">
                <p className="text-sm text-blue-600 mb-1 flex items-center gap-1">
                  <Plus size={14} /> Total Top-ups
                </p>
                <p className="text-xl font-bold text-blue-700">
                  {formatBalanceRaw(totalTopUps.toString())} AVAX
                </p>
                <p className="text-xs text-blue-500 mt-1">
                  {topUps.length} transaction(s)
                </p>
              </div>
              <div className="p-4 bg-gray-50 rounded-lg">
                <p className="text-sm text-gray-500 mb-1">Total Funded</p>
                <p className="text-xl font-bold text-gray-900">
                  {formatBalanceRaw(totalFunded.toString())} AVAX
                </p>
              </div>
              <div className="p-4 bg-red-50 rounded-lg">
                <p className="text-sm text-red-600 mb-1">Fees Paid</p>
                <p className="text-xl font-bold text-red-600">
                  {formatBalanceRaw(feesPaid.toString())} AVAX
                </p>
              </div>
              {refundAmount > 0 && (
                <div className="p-4 bg-orange-50 rounded-lg">
                  <p className="text-sm text-orange-600 mb-1">Refund Amount</p>
                  <p className="text-xl font-bold text-orange-600">
                    {formatBalanceRaw(refundAmount.toString())} AVAX
                  </p>
                </div>
              )}
              <div className="p-4 bg-green-50 rounded-lg">
                <p className="text-sm text-green-600 mb-1">Current Balance</p>
                <p className="text-xl font-bold text-green-600">
                  {formatBalanceRaw(validator.balance)} AVAX
                </p>
              </div>
              <div className="p-4 bg-orange-50 rounded-lg">
                <p className="text-sm text-orange-600 mb-1">Daily Burn Rate</p>
                <p className="text-xl font-bold text-orange-600">
                  {(DAILY_BURN_NAVAX / 1e9).toFixed(6)} AVAX/day
                </p>
                <p className="text-xs text-orange-400 mt-1">512 nAVAX/sec</p>
              </div>
              <div className="p-4 bg-gray-50 rounded-lg">
                <p className="text-sm text-gray-500 mb-1 flex items-center gap-1">
                  <Hourglass size={14} /> Days Until Empty
                </p>
                <p className={`text-xl font-bold ${
                  daysUntilEmpty > 0 && daysUntilEmpty < 30 ? 'text-red-600' :
                  daysUntilEmpty > 0 && daysUntilEmpty < 90 ? 'text-orange-600' : 'text-gray-900'
                }`}>
                  {daysUntilEmpty > 0 ? Math.floor(daysUntilEmpty) : '-'} days
                </p>
              </div>
            </div>

            {/* Balance Transactions */}
            {balanceTransactions && balanceTransactions.length > 0 && (
              <div className="border-t border-gray-200 pt-4">
                <h3 className="text-sm font-semibold text-gray-700 uppercase tracking-wide mb-3 flex items-center gap-2">
                  <ArrowUpCircle size={16} className="text-blue-500" />
                  Balance Transactions ({balanceTransactions.length})
                </h3>
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-gray-200">
                        <th className="text-left py-2 px-3 font-medium text-gray-600">Transaction</th>
                        <th className="text-left py-2 px-3 font-medium text-gray-600">Type</th>
                        <th className="text-left py-2 px-3 font-medium text-gray-600">Block</th>
                        <th className="text-left py-2 px-3 font-medium text-gray-600">Time</th>
                        <th className="text-right py-2 px-3 font-medium text-gray-600">Balance Effect</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100">
                      {balanceTransactions.map((tx) => (
                        <tr key={tx.tx_id + tx.block_height} className={`hover:bg-gray-50 ${tx.effect === 'refund' ? 'bg-red-50' : ''}`}>
                          <td className="py-2 px-3">
                            <Link to={`/p-chain/tx/${tx.tx_id}`} className="font-mono text-blue-600 hover:text-blue-800 flex items-center gap-1">
                              {tx.tx_id.substring(0, 16)}...
                              <ExternalLink size={12} />
                            </Link>
                          </td>
                          <td className="py-2 px-3">
                            <span className={`px-2 py-1 rounded-full text-xs font-medium ${
                              tx.tx_type === 'ConvertSubnetToL1' ? 'bg-purple-100 text-purple-800' :
                              tx.tx_type === 'RegisterL1Validator' ? 'bg-blue-100 text-blue-800' :
                              tx.tx_type === 'IncreaseL1ValidatorBalance' ? 'bg-green-100 text-green-800' :
                              tx.tx_type === 'DisableL1Validator' ? 'bg-red-100 text-red-800' :
                              'bg-gray-100 text-gray-800'
                            }`}>
                              {tx.tx_type === 'ConvertSubnetToL1' ? 'Initial Deposit' :
                               tx.tx_type === 'RegisterL1Validator' ? 'Registration' :
                               tx.tx_type === 'IncreaseL1ValidatorBalance' ? 'Top-up' :
                               tx.tx_type === 'DisableL1Validator' ? 'Disabled (Refund)' :
                               tx.tx_type}
                            </span>
                            {tx.refund_address && (
                              <div className="text-xs text-gray-500 mt-1">
                                To: <span className="font-mono">{tx.refund_address}</span>
                              </div>
                            )}
                          </td>
                          <td className="py-2 px-3">
                            <Link to={`/p-chain/block/${tx.block_height}`} className="text-blue-600 hover:text-blue-800">
                              #{tx.block_height.toLocaleString()}
                            </Link>
                          </td>
                          <td className="py-2 px-3 text-gray-600">
                            {tx.block_time ? new Date(tx.block_time).toLocaleString() : '-'}
                          </td>
                          <td className={`py-2 px-3 text-right font-semibold ${
                            tx.effect === 'refund' ? 'text-orange-600' : 'text-green-600'
                          }`}>
                            {tx.effect === 'refund'
                              ? parseFloat(tx.amount) > 0
                                ? `${formatBalanceRaw(tx.amount)} AVAX refunded`
                                : <span className="text-gray-500 italic">No refund (balance exhausted)</span>
                              : `+${formatBalanceRaw(tx.amount)} AVAX`
                            }
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Registration History Section */}
        {(validator.created_tx_type || validator.created_tx_id) && (
          <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
            <h2 className="text-lg font-bold text-gray-900 mb-4 flex items-center gap-2">
              <Hash size={20} className="text-gray-400" />
              Registration History
            </h2>
            <div className="space-y-4">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 p-4 bg-gray-50 rounded-lg">
                <span className="text-sm font-medium text-gray-600">Registration Type</span>
                <span className={`px-3 py-1.5 rounded-full text-sm font-medium ${
                  validator.created_tx_type === 'ConvertSubnetToL1' ? 'bg-purple-100 text-purple-800' :
                  validator.created_tx_type === 'RegisterL1Validator' ? 'bg-blue-100 text-blue-800' :
                  'bg-gray-100 text-gray-800'
                }`}>
                  {validator.created_tx_type || 'Unknown'}
                </span>
              </div>

              {validator.created_tx_id && (
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 p-4 bg-gray-50 rounded-lg">
                  <span className="text-sm font-medium text-gray-600">Creation Transaction</span>
                  <div className="flex items-center gap-2">
                    <Link
                      to={`/p-chain/tx/${validator.created_tx_id}`}
                      className="text-sm font-mono text-blue-600 hover:text-blue-800 bg-white px-3 py-1.5 rounded border border-gray-200 flex items-center gap-2"
                    >
                      {validator.created_tx_id.substring(0, 20)}...
                      <ExternalLink size={14} />
                    </Link>
                    <button onClick={() => copyToClipboard(validator.created_tx_id || '')} className="p-2 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors" title="Copy">
                      <Copy size={16} />
                    </button>
                  </div>
                </div>
              )}

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="p-4 bg-gray-50 rounded-lg">
                  <p className="text-sm text-gray-500 mb-1">Created at Block</p>
                  <p className="text-lg font-semibold text-gray-900">
                    {validator.created_block ? (
                      <Link to={`/p-chain/block/${validator.created_block}`} className="text-blue-600 hover:text-blue-800">
                        #{validator.created_block.toLocaleString()}
                      </Link>
                    ) : '-'}
                  </p>
                </div>
                <div className="p-4 bg-gray-50 rounded-lg">
                  <p className="text-sm text-gray-500 mb-1">Created Time</p>
                  <p className="text-lg font-semibold text-gray-900">
                    {validator.created_time && validator.created_time !== '1970-01-01 00:00:00'
                      ? new Date(validator.created_time).toLocaleString()
                      : '-'}
                  </p>
                </div>
                {validator.initial_balance && parseFloat(validator.initial_balance) > 0 && (
                  <div className="p-4 bg-gray-50 rounded-lg">
                    <p className="text-sm text-gray-500 mb-1">Initial Balance</p>
                    <p className="text-lg font-semibold text-gray-900">
                      {formatBalanceRaw(validator.initial_balance)} AVAX
                    </p>
                  </div>
                )}
                {validator.initial_weight && parseFloat(validator.initial_weight) > 0 && (
                  <div className="p-4 bg-gray-50 rounded-lg">
                    <p className="text-sm text-gray-500 mb-1">Initial Weight</p>
                    <p className="text-lg font-semibold text-gray-900">
                      {formatWeight(validator.initial_weight)}
                    </p>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </PageTransition>
  );
}

export default ValidatorDetails;
