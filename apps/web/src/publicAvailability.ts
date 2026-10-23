import type { ObservedTimeTotals, StatusPageMonitor } from '@uptime-canary/shared';
import { DAY, HOUR, bucketCount, duration } from './availability';

/**
 * Published observed availability (protocol 15). A status-page selection can opt in to show the last 24 hours of one
 * service as the node that answered the visitor observed it locally: hourly UTC buckets and a summary. It is never
 * merged across nodes, never an SLA, and absent unless the operator explicitly published it.
 */

/** The window is fixed: exactly 24 hours ending at `to` (so `from` is 0 only when `to` is one day after the epoch). */
export const PUBLIC_WINDOW_MS = DAY;
/** Hourly UTC buckets, including the partial ones at both edges: 25 unless the window starts on the hour. */
export const PUBLIC_MAX_BUCKETS = 25;
/** The node ends the window on a 30-second frame at or before the page's `generatedAt`. */
export const PUBLIC_FRAME_MS = 30_000;

export type PublicObserved =
  | { kind: 'available'; from: number; to: number; summary: ObservedTimeTotals; buckets: ObservedTimeTotals[] }
  /** The node published the field but can't answer now (paused, unassigned here, conflicted, not ready, over budget). */
  | { kind: 'unavailable' }
  /** Present but not in the documented shape: shown as unknown, never guessed. */
  | { kind: 'unreadable' };

const isObject = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const ms = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;

/** The latest instant a JavaScript Date can hold; `toISOString` and `Intl` throw beyond it. */
export const MAX_DATE_MS = 8_640_000_000_000_000;

/**
 * Whether a projection time (`generatedAt`) can be shown and can anchor figures: a positive safe integer of
 * milliseconds that a Date can represent. The public page shows nothing time-based for anything else.
 */
export function validProjectionTime(value: unknown): value is number {
  return ms(value) && value > 0 && value <= MAX_DATE_MS;
}

/** The node's percentages are `100 * (a / b)` in float64, the same operations as here; allow only rounding noise. */
const agrees = (value: unknown, expected: number): boolean => typeof value === 'number' && Math.abs(value - expected) <= 1e-9;

const DURATIONS = ['upMs', 'downMs', 'pendingMs', 'unknownMs', 'maintenanceMs', 'knownMs'] as const;

/** One totals object for exactly [startMs, endMs), with the node's accounting identities. Anything else is undefined. */
function readTotals(value: unknown, startMs: number, endMs: number): ObservedTimeTotals | undefined {
  if (!isObject(value) || value.startMs !== startMs || value.endMs !== endMs) return undefined;
  for (const key of DURATIONS) if (!ms(value[key])) return undefined;
  const t = value as unknown as Record<(typeof DURATIONS)[number], number>;
  const window = endMs - startMs;
  if (t.upMs + t.downMs + t.pendingMs + t.unknownMs !== window || t.knownMs !== t.upMs + t.downMs || t.maintenanceMs > window) return undefined;
  if (!agrees(value.coveragePercent, 100 * (t.knownMs / window))) return undefined;
  if (t.knownMs === 0 ? value.availabilityPercent !== null : !agrees(value.availabilityPercent, 100 * (t.upMs / t.knownMs))) return undefined;
  return {
    startMs, endMs, upMs: t.upMs, downMs: t.downMs, pendingMs: t.pendingMs, unknownMs: t.unknownMs, maintenanceMs: t.maintenanceMs, knownMs: t.knownMs,
    availabilityPercent: value.availabilityPercent as number | null, coveragePercent: value.coveragePercent as number,
  };
}

/**
 * Strictly reads a public monitor's `availability` field. `undefined` (or null) means the operator didn't publish
 * history, so nothing is drawn. Only `status: "available"` authorizes numbers; its window, every bucket boundary, every
 * total and both percentages must match the contract exactly, or the whole reading is unreadable. `generatedAt` is the
 * page's own projection time and must pass `validProjectionTime` (positive, safe, within the Date range); the window
 * ends on the 30-second frame that contains it. A missing, invalid or unrepresentable projection time makes an
 * available metric unreadable: no figures without a clock to anchor them. `unavailable` carries no range or totals
 * (its `from`/`to` are fixed zeros); one that does is unreadable instead.
 */
export function readPublicAvailability(value: unknown, generatedAt: number): PublicObserved | undefined {
  if (value === undefined || value === null) return undefined;
  if (!isObject(value) || value.scope !== 'serving-node') return { kind: 'unreadable' };
  if (value.status === 'unavailable') {
    const empty = (value.from ?? 0) === 0 && (value.to ?? 0) === 0 && value.summary === undefined && value.buckets === undefined;
    return { kind: empty ? 'unavailable' : 'unreadable' };
  }
  if (value.status !== 'available') return { kind: 'unreadable' };
  const { from, to } = value;
  if (!ms(from) || !ms(to) || to - from !== PUBLIC_WINDOW_MS || to % PUBLIC_FRAME_MS !== 0) return { kind: 'unreadable' };
  if (!validProjectionTime(generatedAt) || to > generatedAt || generatedAt - to >= PUBLIC_FRAME_MS) return { kind: 'unreadable' };
  const summary = readTotals(value.summary, from, to);
  const raw = value.buckets;
  const count = bucketCount(from, to, HOUR);
  if (!summary || !Array.isArray(raw) || count > PUBLIC_MAX_BUCKETS || raw.length !== count) return { kind: 'unreadable' };
  const buckets: ObservedTimeTotals[] = [];
  const sums = { upMs: 0, downMs: 0, pendingMs: 0, unknownMs: 0, maintenanceMs: 0, knownMs: 0 };
  for (let i = 0, cursor = from; i < count; i++) {
    const end = Math.min(to, (Math.floor(cursor / HOUR) + 1) * HOUR);
    const bucket = readTotals(raw[i], cursor, end);
    if (!bucket) return { kind: 'unreadable' };
    for (const key of DURATIONS) sums[key] += bucket[key];
    buckets.push(bucket);
    cursor = end;
  }
  if (DURATIONS.some(key => sums[key] !== summary[key])) return { kind: 'unreadable' };
  return { kind: 'available', from, to, summary, buckets };
}

/** `part / whole` in hundredths of a percent, truncated like the badge: 9998 is "99.98%", and only part === whole is 10000. */
export function basisPoints(part: number, whole: number): number {
  return Math.floor((part * 10000) / whole);
}

/** "99.98%": two decimals in the visitor's locale, from truncated basis points. */
export function percentText(basis: number): string {
  return `${(basis / 100).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;
}

export interface ObservedFigures {
  /** Up share of known time in basis points; null when nothing was known (shown as unknown, never 0 % or 100 %). */
  availability: number | null;
  /** Known share of the window in basis points. */
  coverage: number;
  windowMs: number;
}

/** The two figures a page row and the badge show, from the millisecond totals rather than the node's floats. */
export function observedFigures(summary: ObservedTimeTotals): ObservedFigures {
  const windowMs = summary.endMs - summary.startMs;
  return { availability: summary.knownMs > 0 ? basisPoints(summary.upMs, summary.knownMs) : null, coverage: basisPoints(summary.knownMs, windowMs), windowMs };
}

/** One bucket or summary in words, for tooltips, the hourly table's screen-reader twin and the row's label. */
export function observedWords(t: ObservedTimeTotals): string {
  const f = observedFigures(t);
  const parts = [`up ${duration(t.upMs)}`, `down ${duration(t.downMs)}`];
  if (t.pendingMs) parts.push(`pending ${duration(t.pendingMs)}`);
  if (t.unknownMs) parts.push(`unknown ${duration(t.unknownMs)}`);
  const availability = f.availability === null ? 'availability unknown (no known time)' : `${percentText(f.availability)} available`;
  return `${parts.join(', ')}; ${availability}; ${percentText(f.coverage)} coverage${t.maintenanceMs ? `; maintenance ${duration(t.maintenanceMs)}, still counted` : ''}`;
}

/** Availability as shown: "99.98%", or "unknown" when nothing was known (never 0 % or 100 %). */
export function availabilityText(t: ObservedTimeTotals): string {
  const { availability } = observedFigures(t);
  return availability === null ? 'unknown' : percentText(availability);
}

/** What the hourly strip shows, in words, for its accessible name. The hourly table has every figure. */
export function stripWords(buckets: ObservedTimeTotals[]): string {
  const down = buckets.filter(bucket => bucket.downMs > 0).length;
  const unknown = buckets.filter(bucket => bucket.unknownMs > 0).length;
  const maintenance = buckets.filter(bucket => bucket.maintenanceMs > 0).length;
  const parts = [down ? `down time in ${down} of ${buckets.length} hourly periods` : 'no down time observed'];
  if (unknown) parts.push(`unknown time in ${unknown}`);
  if (maintenance) parts.push(`maintenance in ${maintenance}`);
  return `Hourly observed time, oldest first: ${parts.join(', ')}.`;
}

// ---------- publishing (status-page editor)

/** Fields the editor owns on one selection. Anything else, such as a field from a newer node, is kept as stored. */
export const SELECTION_KEYS = ['monitorId', 'name', 'showCertificate', 'showTags', 'showAvailability'] as const;

export interface SelectionFields { monitorId: string; name: string; showCertificate: boolean; showTags: boolean; showAvailability: boolean }

/** A copy of every field not in `known`, so a save sends back what this dashboard doesn't edit. */
export function otherFields(value: object, known: readonly string[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, field] of Object.entries(value)) if (!known.includes(key)) out[key] = structuredClone(field);
  return out;
}

export function selectionFields(sel: StatusPageMonitor): SelectionFields {
  return { monitorId: sel.monitorId, name: sel.name, showCertificate: sel.showCertificate, showTags: sel.showTags === true, showAvailability: sel.showAvailability === true };
}

/**
 * The stored selection after an edit: unknown fields kept, the name trimmed, and the opt-in flags written only when on.
 * A page saved without opting in therefore has exactly the bytes an older dashboard would send.
 */
export function writeSelection(fields: SelectionFields, other: Record<string, unknown>): StatusPageMonitor {
  return {
    ...other, monitorId: fields.monitorId, name: fields.name.trim(), showCertificate: fields.showCertificate,
    ...(fields.showTags ? { showTags: true } : {}), ...(fields.showAvailability ? { showAvailability: true } : {}),
  };
}
