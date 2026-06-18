/**
 * react-query hooks over the typed API client in ./api.
 *
 * Keeping the hooks separate from the raw fetchers means the fetchers stay
 * framework-agnostic (usable in tests / non-React code) while components get
 * caching, retries, and request cancellation for free.
 */
import { useQuery } from '@tanstack/react-query';
import {
  getIndexerStatus,
  listMetrics,
  getMetricSeries,
  listValidators,
  getValidator,
  getValidatorDeposits,
  getPChainTx,
  listPChainTxs,
  listChains,
  getPChainStats,
  getSubnetTimeline,
  getPChainTxTypes,
  listPChainBlocks,
  getPChainBlock,
  getStorageStats,
  hasMetricsToken,
  getChainStats,
  listEvmBlocks,
  getEvmBlock,
  listEvmTxs,
  getEvmTx,
  getAddressTxs,
  getAddressInternalTxs,
  getAddressBalances,
  getAddressNativeBalance,
  type AvailableMetric,
  type ChainInfo,
  type Granularity,
  type IndexerStatus,
  type MetricSeries,
  type PChainTx,
  type PChainStats,
  type SubnetTimelinePoint,
  type PChainTxTypeCount,
  type PChainBlock,
  type StorageTable,
  type Validator,
  type ValidatorDeposit,
  type ChainMetrics,
  type EvmBlock,
  type EvmTx,
  type EvmTxDetail,
  type EvmInternalTx,
  type TokenBalance,
  type NativeBalance,
} from './api';

/** Indexer health + per-chain sync status. Doubles as the chain list source. */
export function useIndexerStatus() {
  return useQuery<IndexerStatus>({
    queryKey: ['indexer-status'],
    queryFn: ({ signal }) => getIndexerStatus(signal),
    staleTime: 30 * 1000,
    refetchInterval: 60 * 1000,
  });
}

/** Metrics that have data for a chain. */
export function useAvailableMetrics(chainId: number) {
  return useQuery<AvailableMetric[]>({
    queryKey: ['metrics', chainId],
    queryFn: ({ signal }) => listMetrics(chainId, signal),
    staleTime: 5 * 60 * 1000,
    gcTime: 10 * 60 * 1000,
  });
}

/** Time series for a single metric at a given granularity / window. */
export function useMetricSeries(
  chainId: number,
  metric: string,
  granularity: Granularity,
  from: string,
  enabled = true,
) {
  return useQuery<MetricSeries>({
    queryKey: ['metric-series', chainId, metric, granularity, from],
    queryFn: ({ signal }) =>
      getMetricSeries(chainId, metric, { granularity, from, limit: 1000 }, signal),
    enabled,
    staleTime: 30 * 1000,
  });
}

/** Validators for a subnet (polls so active/balance stay fresh). */
export function useValidators(subnetId: string | undefined, enabled = true) {
  return useQuery<Validator[]>({
    queryKey: ['validators', subnetId],
    queryFn: ({ signal }) => listValidators({ subnetId }, signal),
    enabled: enabled && !!subnetId,
    staleTime: 30 * 1000,
    refetchInterval: 30 * 1000,
  });
}

/** A single validator with computed fee-burn / delegation fields. */
export function useValidator(
  id: string | undefined,
  subnetId: string | undefined,
  enabled = true,
) {
  return useQuery<Validator>({
    queryKey: ['validator', id, subnetId],
    queryFn: ({ signal }) => getValidator(id as string, subnetId, signal),
    enabled: enabled && !!id,
    staleTime: 30 * 1000,
  });
}

/** Balance transactions (deposits / top-ups / refunds) for a validator. */
export function useValidatorDeposits(id: string | undefined, enabled = true) {
  return useQuery<ValidatorDeposit[]>({
    queryKey: ['validator-deposits', id],
    queryFn: ({ signal }) => getValidatorDeposits(id as string, {}, signal),
    enabled: enabled && !!id,
    staleTime: 30 * 1000,
  });
}

/** A single P-Chain transaction by ID. */
export function usePChainTx(txId: string | undefined, enabled = true) {
  return useQuery<PChainTx>({
    queryKey: ['pchain-tx', txId],
    queryFn: ({ signal }) => getPChainTx(txId as string, signal),
    enabled: enabled && !!txId,
    staleTime: 60 * 1000,
  });
}

/** Recent P-Chain transactions (optionally filtered by type / subnet / block). */
export function usePChainTxs(
  params: { txType?: string; subnetId?: string; blockNumber?: number; limit?: number } = {},
  enabled = true,
) {
  return useQuery<PChainTx[]>({
    queryKey: ['pchain-txs', params.txType, params.subnetId, params.blockNumber, params.limit],
    queryFn: ({ signal }) => listPChainTxs(params, signal),
    enabled,
    staleTime: 15 * 1000,
  });
}

/** Chains / subnets registry with validator stats. */
export function useChains(
  params: { chainType?: string; subnetId?: string; active?: boolean; limit?: number } = {},
  enabled = true,
) {
  return useQuery<ChainInfo[]>({
    queryKey: ['chains', params.chainType, params.subnetId, params.active, params.limit],
    queryFn: ({ signal }) => listChains(params, signal),
    enabled,
    staleTime: 60 * 1000,
  });
}

/** P-Chain overview counters. */
export function usePChainStats() {
  return useQuery<PChainStats>({
    queryKey: ['pchain-stats'],
    queryFn: ({ signal }) => getPChainStats(signal),
    staleTime: 30 * 1000,
    refetchInterval: 30 * 1000,
  });
}

/** Monthly L1-subnet conversion counts. */
export function useSubnetTimeline() {
  return useQuery<SubnetTimelinePoint[]>({
    queryKey: ['subnet-timeline'],
    queryFn: ({ signal }) => getSubnetTimeline(signal),
    staleTime: 60 * 1000,
  });
}

/** P-Chain transaction-type counts, optionally windowed to the last N days. */
export function usePChainTxTypes(days?: number) {
  return useQuery<PChainTxTypeCount[]>({
    queryKey: ['pchain-tx-types', days],
    queryFn: ({ signal }) => getPChainTxTypes({ days }, signal),
    staleTime: 30 * 1000,
  });
}

/** Recent P-Chain blocks. */
export function usePChainBlocks(params: { limit?: number } = {}) {
  return useQuery<PChainBlock[]>({
    queryKey: ['pchain-blocks', params.limit],
    queryFn: ({ signal }) => listPChainBlocks(params, signal),
    staleTime: 15 * 1000,
  });
}

/** A single P-Chain block by number. */
export function usePChainBlock(blockNumber: number | undefined, enabled = true) {
  return useQuery<PChainBlock>({
    queryKey: ['pchain-block', blockNumber],
    queryFn: ({ signal }) => getPChainBlock(blockNumber as number, signal),
    enabled: enabled && blockNumber !== undefined && !Number.isNaN(blockNumber),
    staleTime: 60 * 1000,
  });
}

/** Per-table storage stats. Operator-only — only fires when the metrics token is
 *  configured (VITE_METRICS_TOKEN); otherwise the endpoint is gated and would 401. */
export function useStorageStats() {
  return useQuery<StorageTable[]>({
    queryKey: ['storage-stats'],
    queryFn: ({ signal }) => getStorageStats(signal),
    enabled: hasMetricsToken,
    staleTime: 60 * 1000,
    refetchInterval: 60 * 1000,
  });
}

// ---- EVM explorer hooks ----

/** Aggregate stats for a chain (block/tx totals, avg block time, gas). */
export function useChainStats(chainId: number) {
  return useQuery<ChainMetrics>({
    queryKey: ['chain-stats', chainId],
    queryFn: ({ signal }) => getChainStats(chainId, signal),
    staleTime: 30 * 1000,
    refetchInterval: 30 * 1000,
  });
}

/** Recent EVM blocks (polls for new blocks). Kept at 15s to stay within the
 *  API's per-IP rate limit when combined with the other dashboard polls. */
export function useEvmBlocks(chainId: number, limit = 25) {
  return useQuery<EvmBlock[]>({
    queryKey: ['evm-blocks', chainId, limit],
    queryFn: ({ signal }) => listEvmBlocks(chainId, { limit }, signal),
    staleTime: 12 * 1000,
    refetchInterval: 15 * 1000,
  });
}

/** A single EVM block by number. */
export function useEvmBlock(chainId: number, blockNumber: number | undefined, enabled = true) {
  return useQuery<EvmBlock>({
    queryKey: ['evm-block', chainId, blockNumber],
    queryFn: ({ signal }) => getEvmBlock(chainId, blockNumber as number, signal),
    enabled: enabled && blockNumber !== undefined && !Number.isNaN(blockNumber),
    staleTime: 60 * 1000,
  });
}

/** Recent EVM transactions, or one block's transactions when blockNumber is set. */
export function useEvmTxs(
  chainId: number,
  params: { blockNumber?: number; limit?: number } = {},
  enabled = true,
) {
  return useQuery<EvmTx[]>({
    queryKey: ['evm-txs', chainId, params.blockNumber, params.limit],
    queryFn: ({ signal }) => listEvmTxs(chainId, params, signal),
    enabled,
    staleTime: 5 * 1000,
  });
}

/** A single EVM transaction with traces, token transfers, and approvals. */
export function useEvmTx(chainId: number, hash: string | undefined, enabled = true) {
  return useQuery<EvmTxDetail>({
    queryKey: ['evm-tx', chainId, hash],
    queryFn: ({ signal }) => getEvmTx(chainId, hash as string, signal),
    enabled: enabled && !!hash,
    staleTime: 60 * 1000,
  });
}

/** Transactions for an address. */
export function useAddressTxs(chainId: number, address: string | undefined, limit = 25) {
  return useQuery<EvmTx[]>({
    queryKey: ['address-txs', chainId, address, limit],
    queryFn: ({ signal }) => getAddressTxs(chainId, address as string, { limit }, signal),
    enabled: !!address,
    staleTime: 10 * 1000,
  });
}

/** Internal (trace) transactions for an address. */
export function useAddressInternalTxs(chainId: number, address: string | undefined, limit = 25) {
  return useQuery<EvmInternalTx[]>({
    queryKey: ['address-internal-txs', chainId, address, limit],
    queryFn: ({ signal }) => getAddressInternalTxs(chainId, address as string, { limit }, signal),
    enabled: !!address,
    staleTime: 10 * 1000,
  });
}

/** ERC-20 token balances for an address. */
export function useAddressBalances(chainId: number, address: string | undefined) {
  return useQuery<TokenBalance[]>({
    queryKey: ['address-balances', chainId, address],
    queryFn: ({ signal }) => getAddressBalances(chainId, address as string, { limit: 100 }, signal),
    enabled: !!address,
    staleTime: 30 * 1000,
  });
}

/** Native AVAX balance + activity summary for an address. */
export function useAddressNativeBalance(chainId: number, address: string | undefined) {
  return useQuery<NativeBalance>({
    queryKey: ['address-native', chainId, address],
    queryFn: ({ signal }) => getAddressNativeBalance(chainId, address as string, signal),
    enabled: !!address,
    staleTime: 30 * 1000,
  });
}
