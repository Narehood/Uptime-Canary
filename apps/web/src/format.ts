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

/** What previews show in place of a credential. */
export const MASK = '•••';

/** Shows where a webhook goes without its path or query, which often carry credentials. */
export function maskedUrl(url: string): string {
  try {
    const parsed = new URL(url);
    return parsed.pathname === '/' && !parsed.search ? parsed.origin : `${parsed.origin}/${MASK}`;
  } catch {
    return MASK;
  }
}

/** Whether an option's name suggests it holds a credential. For options the dashboard has no field list for. */
export const secretNamed = (key: string): boolean =>
  /password|secret|token|key|dsn|passphrase|community|body|headers|metadata|basicauth|proxyurl/iu.test(key) && !/^(apiversion|keyword)$/iu.test(key);

/**
 * A copy with all text under a `secretNamed` key, at any depth, replaced by the mask. Other values and the shape stay,
 * so fields the dashboard doesn't know, such as a newer node's, still preview as sent. `hidden` masks all text.
 */
export function maskSecretNamed(value: unknown, hidden = false): unknown {
  if (typeof value === 'string') return hidden ? MASK : value;
  if (typeof value !== 'object' || value === null) return value;
  if (Array.isArray(value)) return value.map(item => maskSecretNamed(item, hidden));
  return Object.fromEntries(Object.entries(value).map(([key, inner]) => [key, maskSecretNamed(inner, hidden || secretNamed(key))]));
}

/**
 * The first place in pasted JSON that still holds a placeholder as previews write it (the mask alone, or a URL ending
 * in `/` and the mask), as a dotted path like `http.basicAuth.password`, or `undefined` if none does. Text that merely
 * contains the mask, such as a name, is not a placeholder. Iterative, so deeply nested input can't overflow the stack.
 */
export function maskedPath(value: unknown): string | undefined {
  const stack: [unknown, string][] = [[value, '']];
  while (stack.length) {
    const [item, path] = stack.pop()!;
    if (typeof item === 'string') {
      if (item === MASK || item.endsWith(`/${MASK}`)) return path || '(the whole value)';
    } else if (typeof item === 'object' && item !== null) {
      const entries = Object.entries(item);
      // Pushed last to first, so the first masked value in document order is reported.
      for (let i = entries.length - 1; i >= 0; i--) {
        const [key, child] = entries[i];
        stack.push([child, Array.isArray(item) ? `${path}[${key}]` : path ? `${path}.${key}` : key]);
      }
    }
  }
  return undefined;
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
