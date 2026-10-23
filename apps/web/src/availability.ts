import type { LocalAvailabilityHistory, ObservedTimeTotals } from '@uptime-canary/shared';

/**
 * Observed-time availability (protocol 11): how long the answering node held a fresh local result in each state.
 * Separate from the sample ratio (`sampleAvailability`, 0..1); these percentages are 0..100 and never an SLA.
 */

export const MINUTE = 60_000;
export const HOUR = 3_600_000;
export const DAY = 86_400_000;
/** The node's limits: at most 90 days per query and 4,096 epoch-aligned buckets, including clipped edges. */
export const MAX_RANGE_MS = 90 * DAY;
export const MAX_BUCKETS = 4096;
/** Bucket sizes the dashboard offers. The node accepts any step from 1 ms to 365 days. */
export const STEPS = [MINUTE, 5 * MINUTE, 15 * MINUTE, 30 * MINUTE, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY] as const;
/** About this many columns stay readable in a card-wide chart. */
const TARGET_COLUMNS = 120;

/** Same arithmetic as the node: buckets split at Unix-epoch multiples of the step; partial edges count. */
export function bucketCount(from: number, to: number, step: number): number {
  return Math.floor((to - 1) / step) - Math.floor(from / step) + 1;
}

/** Steps the node would accept for this range, smallest first. */
export function allowedSteps(from: number, to: number): number[] {
  return STEPS.filter(step => bucketCount(from, to, step) <= MAX_BUCKETS);
}

/** The smallest offered step that keeps the chart near TARGET_COLUMNS columns. */
export function autoStep(from: number, to: number): number {
  return STEPS.find(step => bucketCount(from, to, step) <= TARGET_COLUMNS) ?? STEPS[STEPS.length - 1]!;
}

export interface AvailabilityPlan { from: number; to: number; stepMs: number; clipped: boolean }

/**
 * Builds a query the node accepts. `nodeNow` is the node's own clock (its latest overview), because the node rejects
 * a `to` beyond its present instead of clipping it. Returns an explanation when the range can't be asked for.
 */
export function planAvailability(from: number, to: number, nodeNow: number, step: number | 'auto'): AvailabilityPlan | { error: string } {
  if (!Number.isFinite(from) || !Number.isFinite(to)) return { error: 'Choose a start and an end.' };
  const end = Math.min(to, nodeNow);
  if (end <= from) return { error: from >= nodeNow ? 'The range starts after the node’s current time, so nothing has been observed yet.' : 'The end must be after the start.' };
  const start = Math.max(0, from);
  if (end - start > MAX_RANGE_MS) return { error: 'Observed time can be read for at most 90 days at once. Shorten the range.' };
  const stepMs = step === 'auto' ? autoStep(start, end) : step;
  if (bucketCount(start, end, stepMs) > MAX_BUCKETS) return { error: 'That bucket size makes more than 4,096 buckets for this range. Choose a larger one.' };
  return { from: Math.floor(start), to: Math.floor(end), stepMs, clipped: end < to };
}

/** Durations as exact as their size warrants: "0 s", "45 s", "12 min 5 s", "3 h 12 min", "2 d 4 h". */
export function duration(ms: number): string {
  if (ms <= 0) return '0 s';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60), h = Math.floor(m / 60), d = Math.floor(h / 24);
  if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}

/** Step sizes as words: "5 min", "1 h", "1 d". */
export const stepText = (step: number): string => step % DAY === 0 ? `${step / DAY} d` : step % HOUR === 0 ? `${step / HOUR} h` : `${step / MINUTE} min`;

/**
 * A 0..100 percentage, truncated rather than rounded so any failure stays visible: 99.9996 shows "99.999 %", never
 * "100 %", and a tiny nonzero share shows "<0.01 %", never "0 %". `null` (no known time) is a dash.
 */
export function pct(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return '—';
  if (value >= 100) return '100%';
  if (value <= 0) return '0%';
  const digits = value >= 99 ? 3 : value >= 10 ? 1 : 2;
  const factor = 10 ** digits;
  const shown = Math.floor(value * factor) / factor;
  return shown === 0 ? `<${(1 / factor).toLocaleString()}%` : `${shown.toLocaleString(undefined, { maximumFractionDigits: digits })}%`;
}

const num = (value: unknown): number | undefined => typeof value === 'number' && Number.isFinite(value) ? value : undefined;

/** Strictly reads one totals object; anything malformed makes the whole response unreadable rather than guessed. */
function totals(value: unknown): ObservedTimeTotals | undefined {
  if (typeof value !== 'object' || value === null) return undefined;
  const v = value as Record<string, unknown>;
  const fields = ['startMs', 'endMs', 'upMs', 'downMs', 'pendingMs', 'unknownMs', 'maintenanceMs', 'knownMs', 'coveragePercent'] as const;
  const out: Record<string, number | null> = {};
  for (const key of fields) {
    const n = num(v[key]);
    if (n === undefined || n < 0) return undefined;
    out[key] = n;
  }
  const percent = v.availabilityPercent;
  if (percent !== null && (num(percent) === undefined || (percent as number) < 0 || (percent as number) > 100)) return undefined;
  if (out.endMs! <= out.startMs!) return undefined;
  return { ...(out as unknown as ObservedTimeTotals), availabilityPercent: percent as number | null };
}

export function readAvailability(value: unknown): LocalAvailabilityHistory | undefined {
  if (typeof value !== 'object' || value === null) return undefined;
  const v = value as Record<string, unknown>;
  const summary = totals(v.summary);
  // The node always returns at least one bucket for a valid range; none, or too many, is malformed.
  const buckets = Array.isArray(v.buckets) && v.buckets.length >= 1 && v.buckets.length <= MAX_BUCKETS ? v.buckets.map(totals) : undefined;
  const from = num(v.from), to = num(v.to), stepMs = num(v.stepMs), recordLimit = num(v.recordLimit), retentionMs = num(v.retentionMs);
  const retainedFrom = v.retainedFrom === null ? null : num(v.retainedFrom);
  if (!summary || !buckets || buckets.some(bucket => !bucket) || from === undefined || to === undefined || stepMs === undefined
    || recordLimit === undefined || retentionMs === undefined || retainedFrom === undefined
    || typeof v.monitorId !== 'string' || typeof v.nodeId !== 'string') return undefined;
  return {
    monitorId: v.monitorId, nodeId: v.nodeId, from, to, stepMs, retainedFrom, recordLimit, retentionMs,
    summary, buckets: buckets as ObservedTimeTotals[],
  };
}

/** Shares of a bucket's own (possibly clipped) duration; they sum to 1 by the node's accounting identity. */
export function shares(t: ObservedTimeTotals): { up: number; down: number; pending: number; unknown: number; maintenance: number } {
  const window = Math.max(1, t.endMs - t.startMs);
  return { up: t.upMs / window, down: t.downMs / window, pending: t.pendingMs / window, unknown: t.unknownMs / window, maintenance: t.maintenanceMs / window };
}
