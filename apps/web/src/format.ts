/** Times show in the viewer's locale and time zone; every <Time> also carries the UTC instant. */
const clockFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
const shortClockFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
const dayFormat = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });

const sameDay = (a: number, b: number): boolean => new Date(a).toDateString() === new Date(b).toDateString();

export function clock(ms: number): string { return clockFormat.format(ms); }
export function shortClock(ms: number, reference: number = Date.now()): string {
  return sameDay(ms, reference) ? shortClockFormat.format(ms) : dayFormat.format(ms);
}
export function stamp(ms: number, reference: number = Date.now()): string {
  return sameDay(ms, reference) ? clockFormat.format(ms) : dayFormat.format(ms);
}
export function utc(ms: number): string {
  return `${new Date(ms).toISOString().replace('T', ' ').replace(/\.\d+Z$/u, '')} UTC`;
}

/** Compact elapsed time, e.g. "13 s", "4 min", "2 h 5 min", "3 d". */
export function elapsed(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 48) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  return `${Math.floor(h / 24)} d`;
}
export function ago(at: number, now: number): string {
  return now - at < 1500 ? 'just now' : `${elapsed(now - at)} ago`;
}

/** Configured durations, e.g. "500 ms", "30 s", "1 min 30 s", "2 h". */
export function span(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms % 1000) return `${(ms / 1000).toLocaleString(undefined, { maximumFractionDigits: 3 })} s`;
  const s = ms / 1000;
  if (s < 60) return `${s} s`;
  const parts: string[] = [];
  const d = Math.floor(s / 86_400), h = Math.floor((s % 86_400) / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
  if (d) parts.push(`${d} d`);
  if (h) parts.push(`${h} h`);
  if (m) parts.push(`${m} min`);
  if (r) parts.push(`${r} s`);
  return parts.join(' ');
}

export function latency(ms: number): string {
  // A measured round trip under a millisecond is still a measurement; never print it as "0 ms".
  if (ms < 0.5) return '<1 ms';
  return ms >= 1000 ? `${(ms / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} s` : `${Math.round(ms)} ms`;
}

export const plural = (n: number, one: string, many = `${one}s`): string => `${n} ${n === 1 ? one : many}`;

export function andList(items: string[]): string {
  if (items.length <= 1) return items.join('');
  if (items.length === 2) return `${items[0]} and ${items[1]}`;
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

export const shortRev = (revision: string): string => revision.replace(/-/gu, '').slice(0, 6);
export const shortId = (id: string): string => id.length <= 12 ? id : id.replace(/-/gu, '').slice(0, 8);

/** Shows where a webhook goes without its path or query, which often carry credentials. */
export function maskedUrl(url: string): string {
  try {
    const parsed = new URL(url);
    return parsed.pathname === '/' && !parsed.search ? parsed.origin : `${parsed.origin}/•••`;
  } catch {
    return '•••';
  }
}

/** A measured round trip. `latencyAvailable: false` marks a compatibility zero that was never measured. */
export const hasLatency = (observation: { latencyMs: number; latencyAvailable?: boolean } | undefined): boolean =>
  Boolean(observation) && observation!.latencyAvailable !== false;
/** Latency for display: a dash when nothing was measured, never "0 ms". */
export const latencyOf = (observation: { latencyMs: number; latencyAvailable?: boolean } | undefined): string =>
  hasLatency(observation) ? latency(observation!.latencyMs) : '—';

/** UTC wall time with an explicit zone, for schedules and windows. */
export function utcStamp(ms: number): string {
  return `${new Date(ms).toISOString().slice(0, 16).replace('T', ' ')} UTC`;
}
const longFormat = new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
export const longStamp = (ms: number): string => longFormat.format(ms);

export function bytesOf(text: string): number { return new TextEncoder().encode(text).length; }
export function sizeText(n: number): string { return n < 1024 ? `${n} B` : `${(n / 1024).toLocaleString(undefined, { maximumFractionDigits: 1 })} KiB`; }

export function percent(value: number): string {
  const p = value * 100;
  return `${p >= 99.95 && p < 100 ? '99.9' : p.toLocaleString(undefined, { maximumFractionDigits: p >= 10 ? 1 : 2 })}%`;
}
