/** Shared display formatters for the EVM explorer pages. */

/**
 * Format a raw integer-string amount with `decimals` decimal places, using
 * BigInt so large values (wei > 2^53) don't lose precision. Trims trailing
 * zeros and thousands-separates the integer part.
 */
export function formatUnits(value: string | number, decimals: number, maxFrac = 6): string {
  if (value === undefined || value === null || value === '') return '0';
  let s = String(value);
  let neg = false;
  if (s.startsWith('-')) {
    neg = true;
    s = s.slice(1);
  }
  let bi: bigint;
  try {
    bi = BigInt(s);
  } catch {
    return String(value);
  }
  const base = 10n ** BigInt(decimals);
  const intPart = (bi / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  let frac = (bi % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '');
  return `${neg ? '-' : ''}${intPart}${frac ? '.' + frac : ''}`;
}

/** Native C-Chain AVAX is 18 decimals (wei). */
export const formatAvax = (wei: string | number): string => formatUnits(wei, 18);

/** Truncate a 0x hash for display: 0x1234abcd…ef5678. */
export function shortHash(hash: string, lead = 8): string {
  if (!hash) return '';
  if (hash.length <= lead + 8) return hash;
  return `${hash.slice(0, lead + 2)}…${hash.slice(-6)}`;
}

/** Truncate an address: 0x1234…5678. */
export function shortAddr(addr: string | null | undefined): string {
  if (!addr) return '—';
  if (addr.length <= 12) return addr;
  return `${addr.slice(0, 6)}…${addr.slice(-4)}`;
}

/** Absolute timestamp, local. */
export function formatTimestamp(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString();
}

/** Relative "x ago" / "in x" from an ISO timestamp. */
export function timeAgo(iso: string | undefined): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '—';
  const sec = Math.round((Date.now() - t) / 1000);
  const abs = Math.abs(sec);
  const suffix = sec >= 0 ? 'ago' : 'from now';
  if (abs < 60) return `${abs}s ${suffix}`;
  if (abs < 3600) return `${Math.round(abs / 60)}m ${suffix}`;
  if (abs < 86400) return `${Math.round(abs / 3600)}h ${suffix}`;
  return `${Math.round(abs / 86400)}d ${suffix}`;
}

/** Format a gas price in wei as gwei. */
export function formatGwei(wei: string | number): string {
  return `${formatUnits(wei, 9, 2)} gwei`;
}
