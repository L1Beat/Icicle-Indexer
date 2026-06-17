/**
 * Typed client for the Icicle API (https://api.l1beat.io).
 *
 * This replaces the legacy pattern of querying ClickHouse directly from the
 * browser with raw SQL. All data access should go through this module so we get
 * types, a single base URL, consistent error handling, and rate-limit-aware
 * retries (the API allows 60 req/min per IP, burst 10, refilling ~1/sec).
 */

export const API_BASE_URL = (
  import.meta.env.VITE_API_BASE_URL ?? 'https://api.l1beat.io'
).replace(/\/$/, '');

/** Error thrown for non-2xx API responses. */
export class ApiError extends Error {
  readonly status: number;
  readonly path: string;

  constructor(status: number, path: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.path = path;
  }
}

// ---------------------------------------------------------------------------
// Response envelope + domain types (mirror the Go structs in pkg/api).
// ---------------------------------------------------------------------------

/** Standard list envelope: { data, meta }. */
export interface Envelope<T> {
  data: T;
  meta?: Meta;
}

export interface Meta {
  total?: number;
  limit: number;
  offset: number;
  has_more: boolean;
  next_cursor?: string;
}

export type Granularity = 'hour' | 'day' | 'week' | 'month';

/** One available metric for a chain (GET /metrics/evm/{id}/timeseries). */
export interface AvailableMetric {
  metric_name: string;
  granularities: Granularity[];
  latest_period: string;
  data_points: number;
}

/** A single point in a metric time series. */
export interface MetricDataPoint {
  period: string;
  value: number;
}

/** Time series for one metric (GET /metrics/evm/{id}/timeseries/{metric}). */
export interface MetricSeries {
  chain_id: number;
  metric_name: string;
  granularity: Granularity;
  data: MetricDataPoint[];
}

/** Per-chain sync status (subset of the indexer status payload). */
export interface EVMChainStatus {
  chain_id: number;
  name: string;
  current_block: number;
  latest_block: number;
  blocks_behind: number;
  last_sync: string;
  is_synced: boolean;
}

export interface PChainStatus {
  current_block: number;
  latest_block: number;
  blocks_behind: number;
  last_sync: string;
  is_synced: boolean;
}

/** GET /api/v1/metrics/indexer/status — note: this endpoint is NOT enveloped. */
export interface IndexerStatus {
  healthy: boolean;
  evm: EVMChainStatus[];
  pchain?: PChainStatus;
  last_update: string;
}

/**
 * A validator (Primary Network, legacy subnet, or L1). Mirrors the Go
 * `Validator` struct: many fields are optional and only present for certain
 * validator types. Amount fields are nAVAX (1 AVAX = 1e9 nAVAX) as JSON numbers.
 */
export interface Validator {
  subnet_id: string;
  validation_id: string;
  node_id: string;
  weight: number;
  start_time: string;
  active: boolean;
  end_time?: string;
  uptime_percentage?: number;

  // Staked amount in WHOLE tokens (PoS chains only; absent for PoA). Decimal
  // string with full precision — group + append staked_token for display.
  staked_amount?: string;
  staked_token?: string;

  // L1 validator fields (absent for Primary Network / legacy)
  balance?: number;
  initial_deposit?: number;
  total_topups?: number;
  refund_amount?: number;
  fees_paid?: number;

  // Registration info (from history)
  tx_hash?: string;
  tx_type?: string;
  created_block?: number;
  created_time?: string;
  bls_public_key?: string;
  remaining_balance_owner?: string;

  // Computed (detail endpoint only)
  total_deposited?: number;
  days_remaining?: number;
  estimated_days_left?: number;
  daily_fee_burn?: number;
  network_share_percent?: number;

  // Delegation data (Primary Network, detail endpoint only)
  delegation_fee_percent?: number;
  delegator_count?: number;
  total_delegated?: number;
  total_stake?: number;

  // Primary Network enrichment (legacy subnet validators)
  primary_stake?: number;
  primary_uptime?: number;
}

/** A validator balance transaction (deposit / top-up / refund). */
export interface ValidatorDeposit {
  tx_id: string;
  tx_type: string;
  block_number: number;
  block_time: string;
  amount: number;
}

/** A P-Chain transaction. `tx_data` is the raw, untyped decoded payload. */
export interface PChainTx {
  tx_id: string;
  tx_type: string;
  block_number: number;
  block_time: string;
  tx_data: Record<string, unknown>;
}

/** Headline counters for the P-Chain overview. */
export interface PChainStats {
  active_l1_subnets: number;
  active_legacy_subnets: number;
  active_chains: number;
  active_validators: number;
  recent_transactions: number;
  total_l1_fees_paid: number;
}

/** One month of L1-subnet conversions (for the creation timeline). */
export interface SubnetTimelinePoint {
  period: string;
  value: number;
}

/** A P-Chain transaction-type count (optionally windowed). */
export interface PChainTxTypeCount {
  tx_type: string;
  count: number;
}

/** A P-Chain block, summarized from its transactions. */
export interface PChainBlock {
  block_number: number;
  tx_count: number;
  block_time: string;
  block_hash?: string;
  parent_id?: string;
  proposer_id?: string;
  proposer_node_id?: string;
  block_type?: string;
}

/** Per-table on-disk size + row count (ops/storage view). */
export interface StorageTable {
  table: string;
  size_bytes: number;
  rows: number;
}

/** Aggregate stats for a chain (GET /metrics/evm/{id}/stats). */
export interface ChainMetrics {
  chain_id: number;
  chain_name: string;
  latest_block: number;
  total_blocks: number;
  total_txs: number;
  last_block_time: string;
  avg_block_time_seconds: number;
  avg_gas_used: number;
  total_gas_used: number;
}

// ---- EVM explorer types (mirror pkg/api Block / Transaction / balances) ----

export interface EvmBlock {
  chain_id: number;
  block_number: number;
  hash: string;
  parent_hash: string;
  block_time: string;
  miner: string;
  size: number;
  gas_limit: number;
  gas_used: number;
  base_fee_per_gas: number;
  tx_count: number;
}

export interface EvmTx {
  chain_id: number;
  hash: string;
  block_number: number;
  block_time: string;
  transaction_index: number;
  from: string;
  to: string | null;
  value: string;
  gas_limit: number;
  gas_price: number;
  gas_used: number;
  success: boolean;
  type: number;
}

export interface EvmInternalTx {
  tx_hash?: string;
  block_number?: number;
  block_time?: string;
  trace_index: string;
  from: string;
  to: string | null;
  value: string;
  gas_used: number;
  call_type: string;
  success: boolean;
}

export interface EvmTokenTransfer {
  token: string;
  name?: string;
  symbol?: string;
  decimals?: number;
  from: string;
  to: string;
  value: string;
  log_index: number;
}

export interface EvmTokenApproval {
  token: string;
  name?: string;
  symbol?: string;
  decimals?: number;
  owner: string;
  spender: string;
  amount: string;
  is_unlimited: boolean;
  log_index: number;
}

/** Full tx detail: the base tx fields plus traces / transfers / approvals. */
export interface EvmTxDetail extends EvmTx {
  internal_txs: EvmInternalTx[];
  token_transfers: EvmTokenTransfer[];
  approvals: EvmTokenApproval[];
}

export interface NativeBalance {
  total_in: string;
  total_out: string;
  total_gas: string;
  balance: string;
  last_updated_block: number;
  tx_count: number;
  first_tx_time?: string;
  last_tx_time?: string;
}

export interface TokenBalance {
  token: string;
  name?: string;
  symbol?: string;
  decimals?: number;
  balance: string;
  total_in: string;
  total_out: string;
  last_updated_block: number;
}

export interface SocialLink {
  name: string;
  url: string;
}

export interface NetworkToken {
  name: string;
  symbol: string;
  decimals: number;
  logo_uri?: string;
}

/** Enriched chain/subnet record from the unified /chains endpoint. */
export interface ChainInfo {
  chain_id: string;
  chain_name: string;
  vm_id: string;
  created_block: number;
  created_time: string;
  subnet_id: string;
  chain_type: string;
  converted_block?: number;
  converted_time?: string;
  validator_manager_address?: string;
  validator_manager_owner?: string;
  name?: string;
  description?: string;
  logo_url?: string;
  website_url?: string;
  evm_chain_id?: number;
  categories?: string[];
  socials?: SocialLink[];
  rpc_url?: string;
  explorer_url?: string;
  sybil_resistance_type?: string;
  network_token?: NetworkToken;
  network?: string;
  is_active: boolean;
  validator_count?: number;
  active_validators?: number;
  total_staked?: number;
  // Total staked in WHOLE tokens (PoS chains only). Decimal string; pair with
  // network_token.symbol for display.
  total_staked_tokens?: string;
  total_fees_paid?: number;
}

// ---------------------------------------------------------------------------
// Core fetch helper with 429-aware retry.
// ---------------------------------------------------------------------------

const MAX_RETRIES = 4;
const BASE_BACKOFF_MS = 1200;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

interface RequestOptions {
  /** AbortSignal, typically supplied by react-query. */
  signal?: AbortSignal;
  /** Query params; undefined/null values are skipped. */
  query?: Record<string, string | number | boolean | undefined | null>;
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = new URL(API_BASE_URL + path);
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== null) {
        url.searchParams.set(key, String(value));
      }
    }
  }
  return url.toString();
}

/**
 * Fetch JSON from the API. Retries on 429 (honoring Retry-After when present)
 * and on transient 5xx, then throws ApiError. Returns the parsed body as-is —
 * callers that expect the { data } envelope should use apiGetData().
 */
async function apiFetch<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const url = buildUrl(path, opts.query);

  let lastErr: unknown;
  for (let attempt = 0; attempt <= MAX_RETRIES; attempt++) {
    let res: Response;
    try {
      res = await fetch(url, {
        headers: { Accept: 'application/json' },
        signal: opts.signal,
      });
    } catch (err) {
      // Network error / abort — abort should not be retried.
      if (opts.signal?.aborted) throw err;
      lastErr = err;
      if (attempt < MAX_RETRIES) {
        await sleep(BASE_BACKOFF_MS * (attempt + 1));
        continue;
      }
      throw err;
    }

    if (res.ok) {
      return (await res.json()) as T;
    }

    // Retry on rate-limit and transient server errors.
    if ((res.status === 429 || res.status >= 500) && attempt < MAX_RETRIES) {
      const retryAfter = Number(res.headers.get('Retry-After'));
      const waitMs = Number.isFinite(retryAfter) && retryAfter > 0
        ? retryAfter * 1000
        : BASE_BACKOFF_MS * (attempt + 1);
      await sleep(waitMs);
      continue;
    }

    throw new ApiError(res.status, path, `${res.status} ${res.statusText} for ${path}`);
  }

  // Exhausted retries on network error.
  throw lastErr instanceof Error
    ? lastErr
    : new ApiError(0, path, `Request failed for ${path}`);
}

/** Fetch an enveloped endpoint and return the unwrapped `data`. */
async function apiGetData<T>(path: string, opts?: RequestOptions): Promise<T> {
  const body = await apiFetch<Envelope<T>>(path, opts);
  return body.data;
}

// ---------------------------------------------------------------------------
// Endpoint functions.
// ---------------------------------------------------------------------------

/** Overall indexer health + per-chain sync status. Not enveloped. */
export function getIndexerStatus(signal?: AbortSignal): Promise<IndexerStatus> {
  return apiFetch<IndexerStatus>('/api/v1/metrics/indexer/status', { signal });
}

/** List the metrics that have data for a chain. */
export function listMetrics(
  chainId: number,
  signal?: AbortSignal,
): Promise<AvailableMetric[]> {
  return apiGetData<AvailableMetric[]>(
    `/api/v1/metrics/evm/${chainId}/timeseries`,
    { signal },
  );
}

/** Fetch the time series for a single metric. */
export function getMetricSeries(
  chainId: number,
  metric: string,
  params: { granularity: Granularity; from?: string; to?: string; limit?: number },
  signal?: AbortSignal,
): Promise<MetricSeries> {
  return apiGetData<MetricSeries>(
    `/api/v1/metrics/evm/${chainId}/timeseries/${encodeURIComponent(metric)}`,
    {
      signal,
      query: {
        granularity: params.granularity,
        from: params.from,
        to: params.to,
        limit: params.limit,
      },
    },
  );
}

/**
 * List validators, optionally filtered by subnet / active status. The server
 * handles Primary Network / legacy / L1 differences and Primary Network
 * enrichment for legacy subnets. Validators are a bounded set, so we fetch
 * generously (cap is 5000 server-side).
 */
export function listValidators(
  params: { subnetId?: string; active?: boolean; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<Validator[]> {
  return apiGetData<Validator[]>('/api/v1/data/validators', {
    signal,
    query: {
      subnet_id: params.subnetId,
      active: params.active,
      limit: params.limit ?? 5000,
      offset: params.offset,
    },
  });
}

/**
 * Get a single validator by validation ID or node ID. Pass subnetId to
 * disambiguate a node that validates multiple subnets. Includes computed
 * fee-burn / delegation fields the list endpoint omits.
 */
export function getValidator(
  id: string,
  subnetId?: string,
  signal?: AbortSignal,
): Promise<Validator> {
  return apiGetData<Validator>(
    `/api/v1/data/validators/${encodeURIComponent(id)}`,
    { signal, query: { subnet_id: subnetId } },
  );
}

/** Get balance transactions (deposits, top-ups, refunds) for a validator. */
export function getValidatorDeposits(
  id: string,
  params: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<ValidatorDeposit[]> {
  return apiGetData<ValidatorDeposit[]>(
    `/api/v1/data/validators/${encodeURIComponent(id)}/deposits`,
    { signal, query: { limit: params.limit ?? 100, offset: params.offset } },
  );
}

/** Get a single P-Chain transaction by ID (includes full untyped tx_data). */
export function getPChainTx(txId: string, signal?: AbortSignal): Promise<PChainTx> {
  return apiGetData<PChainTx>(
    `/api/v1/data/pchain/txs/${encodeURIComponent(txId)}`,
    { signal },
  );
}

/** List P-Chain transactions (most recent first). */
export function listPChainTxs(
  params: {
    txType?: string;
    subnetId?: string;
    blockNumber?: number;
    limit?: number;
    offset?: number;
  } = {},
  signal?: AbortSignal,
): Promise<PChainTx[]> {
  return apiGetData<PChainTx[]>('/api/v1/data/pchain/txs', {
    signal,
    query: {
      tx_type: params.txType,
      subnet_id: params.subnetId,
      block_number: params.blockNumber,
      limit: params.limit,
      offset: params.offset,
    },
  });
}

/** P-Chain overview counters (active subnets, validators, fees, 7d volume). */
export function getPChainStats(signal?: AbortSignal): Promise<PChainStats> {
  return apiGetData<PChainStats>('/api/v1/data/pchain/stats', { signal });
}

/** Monthly L1-subnet conversion counts (oldest first). */
export function getSubnetTimeline(signal?: AbortSignal): Promise<SubnetTimelinePoint[]> {
  return apiGetData<SubnetTimelinePoint[]>('/api/v1/data/pchain/subnet-timeline', { signal });
}

/** P-Chain transaction-type counts. Pass `days` to window the last N days. */
export function getPChainTxTypes(
  params: { days?: number } = {},
  signal?: AbortSignal,
): Promise<PChainTxTypeCount[]> {
  return apiGetData<PChainTxTypeCount[]>('/api/v1/data/pchain/tx-types', {
    signal,
    query: { days: params.days },
  });
}

/** Recent P-Chain blocks (newest first). */
export function listPChainBlocks(
  params: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<PChainBlock[]> {
  return apiGetData<PChainBlock[]>('/api/v1/data/pchain/blocks', {
    signal,
    query: { limit: params.limit, offset: params.offset },
  });
}

/** A single P-Chain block by number (with proposer / parent info). */
export function getPChainBlock(blockNumber: number, signal?: AbortSignal): Promise<PChainBlock> {
  return apiGetData<PChainBlock>(`/api/v1/data/pchain/blocks/${blockNumber}`, { signal });
}

/** Per-table storage stats (size + row counts), largest first. */
export function getStorageStats(signal?: AbortSignal): Promise<StorageTable[]> {
  return apiGetData<StorageTable[]>('/api/v1/metrics/storage', { signal });
}

// ---- EVM explorer endpoints ----

/** Aggregate stats (block/tx totals, avg block time, gas) for a chain. */
export function getChainStats(chainId: number, signal?: AbortSignal): Promise<ChainMetrics> {
  return apiGetData<ChainMetrics>(`/api/v1/metrics/evm/${chainId}/stats`, { signal });
}

/** Recent EVM blocks (newest first). */
export function listEvmBlocks(
  chainId: number,
  params: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<EvmBlock[]> {
  return apiGetData<EvmBlock[]>(`/api/v1/data/evm/${chainId}/blocks`, {
    signal,
    query: { limit: params.limit, offset: params.offset },
  });
}

/** A single EVM block by number. */
export function getEvmBlock(
  chainId: number,
  blockNumber: number,
  signal?: AbortSignal,
): Promise<EvmBlock> {
  return apiGetData<EvmBlock>(`/api/v1/data/evm/${chainId}/blocks/${blockNumber}`, { signal });
}

/** List EVM transactions (newest first); pass blockNumber to list one block's txs. */
export function listEvmTxs(
  chainId: number,
  params: { blockNumber?: number; limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<EvmTx[]> {
  return apiGetData<EvmTx[]>(`/api/v1/data/evm/${chainId}/txs`, {
    signal,
    query: { block_number: params.blockNumber, limit: params.limit, offset: params.offset },
  });
}

/** A single EVM transaction with traces, token transfers, and approvals. */
export function getEvmTx(
  chainId: number,
  hash: string,
  signal?: AbortSignal,
): Promise<EvmTxDetail> {
  return apiGetData<EvmTxDetail>(
    `/api/v1/data/evm/${chainId}/txs/${encodeURIComponent(hash)}`,
    { signal },
  );
}

/** Transactions for an address (from or to). */
export function getAddressTxs(
  chainId: number,
  address: string,
  params: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<EvmTx[]> {
  return apiGetData<EvmTx[]>(
    `/api/v1/data/evm/${chainId}/address/${encodeURIComponent(address)}/txs`,
    { signal, query: { limit: params.limit, offset: params.offset } },
  );
}

/** Internal (trace) transactions touching an address. */
export function getAddressInternalTxs(
  chainId: number,
  address: string,
  params: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<EvmInternalTx[]> {
  return apiGetData<EvmInternalTx[]>(
    `/api/v1/data/evm/${chainId}/address/${encodeURIComponent(address)}/internal-txs`,
    { signal, query: { limit: params.limit, offset: params.offset } },
  );
}

/** ERC-20 token balances for an address. */
export function getAddressBalances(
  chainId: number,
  address: string,
  params: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<TokenBalance[]> {
  return apiGetData<TokenBalance[]>(
    `/api/v1/data/evm/${chainId}/address/${encodeURIComponent(address)}/balances`,
    { signal, query: { limit: params.limit, offset: params.offset } },
  );
}

/** Native (AVAX) balance + activity summary for an address. */
export function getAddressNativeBalance(
  chainId: number,
  address: string,
  signal?: AbortSignal,
): Promise<NativeBalance> {
  return apiGetData<NativeBalance>(
    `/api/v1/data/evm/${chainId}/address/${encodeURIComponent(address)}/native`,
    { signal },
  );
}

/** List chains / subnets (unified registry + validator stats). */
export function listChains(
  params: { chainType?: string; subnetId?: string; active?: boolean; limit?: number } = {},
  signal?: AbortSignal,
): Promise<ChainInfo[]> {
  return apiGetData<ChainInfo[]>('/api/v1/data/chains', {
    signal,
    query: {
      chain_type: params.chainType,
      subnet_id: params.subnetId,
      active: params.active,
      limit: params.limit,
    },
  });
}
