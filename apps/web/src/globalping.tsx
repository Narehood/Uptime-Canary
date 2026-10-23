import type { ReactNode } from 'react';
import type { GlobalpingMonitorOptions, JSONQueryOptions, Monitor } from '@uptime-canary/shared';
import { maskedUrl, plural, span } from './format';
import { buildJsonata, jsonataDraft, type JsonataDraft } from './serviceSpecs';
import { headerRows, type HeaderRow } from './ui/forms';

/**
 * Globalping remote measurements (peer protocol 12). One remote ping, HTTP or DNS measurement per check and node, run by
 * a third-party probe chosen by a free-form "magic" location. The node's leaf validates everything; these rules mirror
 * its shape checks without being stricter, so a stored configuration can always be saved again unchanged.
 */

export type GpMode = 'ping' | 'http' | 'dns';
export const GP_MODES: { value: GpMode; label: string; hint: string }[] = [
  { value: 'ping', label: 'Ping', hint: 'ICMP or TCP from the probe' },
  { value: 'http', label: 'HTTP', hint: 'GET, HEAD or OPTIONS from the probe' },
  { value: 'dns', label: 'DNS', hint: 'A query from the probe' },
];
export const GP_DNS_TYPES = ['A', 'AAAA', 'ANY', 'CNAME', 'DNSKEY', 'DS', 'HTTPS', 'MX', 'NS', 'NSEC', 'PTR', 'RRSIG', 'SOA', 'TXT', 'SRV', 'SVCB'] as const;
export type GpDnsType = (typeof GP_DNS_TYPES)[number];
export const GP_DEFAULT_API = 'https://api.globalping.io';

export interface GpDraft {
  mode: GpMode;
  /** One target per mode: a URL for HTTP, a host for ping, a name for DNS. Switching modes keeps what was typed. */
  targets: Record<GpMode, string>;
  location: string;
  apiUrl: string; token: string; insecure: boolean; ca: string;
  probeTimeout: string; ipVersion: '0' | '4' | '6'; failureDown: boolean;
  pingPackets: string; pingProtocol: string; pingPort: string;
  httpMethod: string; http2: boolean; headers: HeaderRow[]; statuses: string; keyword: string; invert: boolean; jsonata: JsonataDraft; ignoreTls: boolean;
  dnsType: string; dnsProtocol: string; dnsPort: string; resolver: string; regex: string; allowEmpty: boolean;
}

export type GpField = 'gpTarget' | 'gpLocation' | 'gpApi' | 'gpToken' | 'gpCa' | 'gpProbe' | 'gpIp' | 'gpPing' | 'gpHeaders' | 'gpStatuses' | 'gpKeyword' | 'gpJsonata' | 'gpDns' | 'gpRegex';
export const GP_FIELD_LABEL: Record<GpField, string> = {
  gpTarget: 'Target', gpLocation: 'Probe location', gpApi: 'Globalping API', gpToken: 'API token', gpCa: 'API CA certificates', gpProbe: 'Probe timeout',
  gpIp: 'IP version', gpPing: 'Ping options', gpHeaders: 'Request headers', gpStatuses: 'Accepted status', gpKeyword: 'Keyword', gpJsonata: 'JSONata condition',
  gpDns: 'DNS options', gpRegex: 'Answer pattern',
};
/** Which disclosure holds a field, so a failed save opens it. */
export const GP_SECTION: Partial<Record<GpField, string>> = { gpApi: 'gpApi', gpToken: 'gpApi', gpCa: 'gpApi', gpHeaders: 'gpRequest', gpKeyword: 'gpResponse', gpJsonata: 'gpResponse', gpStatuses: 'gpResponse' };

const str = (value: unknown): string => typeof value === 'string' ? value : '';
const num = (value: unknown): string => typeof value === 'number' && Number.isFinite(value) && value !== 0 ? String(value) : '';
const upper = (value: unknown, fallback: string): string => typeof value === 'string' && value ? value.toUpperCase() : fallback;
const rangesText = (ranges: unknown): string => Array.isArray(ranges)
  ? ranges.map(r => r && typeof r === 'object' ? ((r as { min: number; max: number }).min === (r as { min: number; max: number }).max ? String((r as { min: number }).min) : `${(r as { min: number }).min}-${(r as { max: number }).max}`) : '').filter(Boolean).join(', ')
  : '';

export function gpDraftFrom(o: GlobalpingMonitorOptions | undefined, target: string): GpDraft {
  const mode = (['ping', 'http', 'dns'] as const).find(m => m === str(o?.mode).trim().toLowerCase()) ?? 'http';
  const p = o?.ping, h = o?.http, d = o?.dns;
  return {
    mode,
    targets: { ping: '', http: '', dns: '', ...(o ? { [mode]: target } : {}) },
    location: str(o?.location),
    apiUrl: str(o?.apiUrl), token: str(o?.token), insecure: Boolean(o?.allowInsecureApi), ca: str(o?.apiCa),
    probeTimeout: num(o?.probeTimeoutSeconds), ipVersion: o?.ipVersion === 4 ? '4' : o?.ipVersion === 6 ? '6' : '0', failureDown: Boolean(o?.apiFailureIsDown),
    pingPackets: num(p?.packets), pingProtocol: upper(p?.protocol, 'ICMP'), pingPort: num(p?.port),
    httpMethod: upper(h?.method, 'GET'), http2: str(h?.protocol).toUpperCase() === 'HTTP2', headers: headerRows(h?.headers), statuses: rangesText(h?.acceptedStatuses),
    keyword: str(h?.keyword), invert: Boolean(h?.invertKeyword), jsonata: jsonataDraft(h?.jsonata), ignoreTls: Boolean(h?.ignoreTls),
    dnsType: upper(d?.queryType, 'A'), dnsProtocol: upper(d?.protocol, 'UDP'), dnsPort: num(d?.port), resolver: str(d?.resolver), regex: str(d?.regex), allowEmpty: Boolean(d?.allowEmptyAnswers),
  };
}

// ---------- the leaf's shape rules

const utf8 = (text: string): number => new TextEncoder().encode(text).length;
const hasControl = (text: string): boolean => /\p{Cc}/u.test(text);
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/u;
/** IPv6 as Go's net.ParseIP accepts it (no zone, no brackets), checked with the URL parser's strict IPv6 grammar. */
const isIPv6 = (text: string): boolean => {
  if (!text.includes(':') || !/^[0-9a-fA-F:.]+$/u.test(text)) return false;
  try { new URL(`http://[${text}]/`); return true; } catch { return false; }
};
const isIP = (text: string): boolean => IPV4.test(text) || isIPv6(text);
/** The authority and path exactly as typed. The browser's URL parser would punycode hosts and resolve "..", which the node doesn't. */
const rawParts = (text: string): { host: string; path: string } => {
  const rest = text.replace(/^[A-Za-z][A-Za-z0-9+.-]*:\/\//u, '');
  const end = rest.search(/[/?#]/u);
  const authority = end < 0 ? rest : rest.slice(0, end);
  const path = end < 0 ? '' : rest.slice(end).split(/[?#]/u)[0]!;
  return { host: authority.replace(/^.*@/u, ''), path };
};
/** ASCII DNS labels (punycode for other scripts), at most 253 bytes, or an IP literal; DNS names may contain "_". */
export function gpHost(text: string, underscore: boolean): boolean {
  if (!text || text.length > 253) return false;
  if (isIP(text)) return true;
  const label = underscore ? /^[A-Za-z0-9_](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$/u : /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/u;
  return text.replace(/\.$/u, '').split('.').every(part => label.test(part));
}
const HEADER_NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/u;

/** The target for the mode as the node will check it, or a problem. */
export function gpTargetProblem(mode: GpMode, value: string): string | undefined {
  const text = value.trim();
  if (mode === 'http') {
    if (!text) return 'Enter the URL the probe should request.';
    if (text.length > 4096 || hasControl(text) || text.includes('\\')) return 'The URL is too long or contains control characters or backslashes.';
    let url: URL;
    try { url = new URL(text); } catch { return 'Enter a complete http:// or https:// address.'; }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'Use an http:// or https:// address.';
    if (url.username || url.password) return 'Remove credentials from the address. Use an Authorization header instead.';
    if (text.includes('#')) return 'Remove the #fragment from the address.';
    if (/[^\x00-\x7f]/u.test(rawParts(text).host)) return 'Write international host names in punycode (xn--…), as the node checks ASCII names only.';
    const host = url.hostname.replace(/^\[|\]$/gu, '');
    if (!gpHost(host, false)) return 'Use an ASCII host name (punycode for other scripts) or an IP address.';
    return undefined;
  }
  if (!text) return mode === 'ping' ? 'Enter the host name or IP address to ping.' : 'Enter the name to query.';
  if (/[/:]{2}|\s/u.test(text) && !isIP(text)) return 'Enter a bare host name or IP address, without a scheme, port or path.';
  if (!gpHost(text, mode === 'dns')) return mode === 'dns' ? 'Use a domain name like example.com (underscores allowed) or an IP address.' : 'Use a host name like example.com or an IP address.';
  return undefined;
}

/** "200-299, 301" → ranges, as the leaf wants: at most 32 inclusive ranges within 100–599. */
export function gpRanges(text: string): { ranges?: { min: number; max: number }[]; error?: string } {
  const tokens = text.split(/[\s,]+/u).filter(Boolean);
  if (!tokens.length) return {};
  const ranges: { min: number; max: number }[] = [];
  for (const token of tokens) {
    const m = /^(\d{3})(?:[-–](\d{3}))?$/u.exec(token);
    const min = m ? Number(m[1]) : Number.NaN, max = m ? Number(m[2] ?? m[1]) : Number.NaN;
    if (!m || min < 100 || max > 599 || min > max) return { error: `“${token}” isn’t a status code or range between 100 and 599.` };
    ranges.push({ min, max });
  }
  if (ranges.length > 32) return { error: 'Use at most 32 codes or ranges.' };
  return { ranges };
}

function apiProblem(value: string, insecure: boolean): string | undefined {
  if (!value) return undefined;
  if (value.length > 2048 || hasControl(value) || value.includes('\\')) return 'The API address is too long or contains control characters.';
  let url: URL;
  try { url = new URL(value); } catch { return 'Enter a complete https:// address, like https://api.globalping.io.'; }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return 'Use an https:// address.';
  if (url.protocol === 'http:' && !insecure) return 'Plain http:// needs “Allow plain HTTP for the API” below.';
  if (url.username || url.password || url.search || value.includes('#') || value.endsWith('?')) return 'Use only the scheme, host, port and an optional path prefix, without credentials, query or fragment.';
  if (!gpHost(url.hostname.replace(/^\[|\]$/gu, ''), false)) return 'The API host isn’t a valid host name or IP address.';
  const path = rawParts(value).path;
  if (/[^\x00-\x7f]/u.test(rawParts(value).host)) return 'Write the API host in ASCII (punycode for international names).';
  if (path.includes('//') || path.includes('%') || path.split('/').some(part => part === '.' || part === '..' || !/^[A-Za-z0-9\-._~]*$/u.test(part))) return 'The path prefix may use letters, digits and - . _ ~ only, without .. or //.';
  if (/\/v1\/measurements\/?$/u.test(path)) return 'Leave out /v1/measurements; the node adds it.';
  return undefined;
}

function caProblem(pem: string): string | undefined {
  const text = pem.trim();
  if (!text) return undefined;
  if (utf8(pem) > 128 * 1024) return 'Use at most 128 KiB of certificates.';
  const blocks = text.split(/(?<=-----END CERTIFICATE-----)\s*/u).filter(Boolean);
  if (blocks.length > 32) return 'Use at most 32 certificates.';
  if (!blocks.every(block => /^-----BEGIN CERTIFICATE-----\r?\n[A-Za-z0-9+/=\r\n]+-----END CERTIFICATE-----$/u.test(block.trim()))) return 'Paste PEM certificates only (-----BEGIN CERTIFICATE-----), without keys, headers or other text.';
  return undefined;
}

/** RE2 rejects these JavaScript-only constructs; the node would refuse the whole configuration. */
function re2Problem(pattern: string): string | undefined {
  if (utf8(pattern) > 4096) return 'Use a pattern of at most 4,096 bytes.';
  if (/\(\?<?[=!]/u.test(pattern)) return 'RE2 has no lookahead or lookbehind.';
  if (/\\[1-9]|\\k</u.test(pattern)) return 'RE2 has no backreferences.';
  return undefined;
}

type Obj = Record<string, unknown>;
const clone = (value: unknown): Obj => value && typeof value === 'object' && !Array.isArray(value) ? structuredClone(value) as Obj : {};
/** Sets an enum, keeping the stored spelling when it means the same, and omitting the default when nothing was stored. */
function putEnum(o: Obj, key: string, value: string, fallback: string): void {
  if (typeof o[key] === 'string' && (o[key] as string).toUpperCase() === value) return;
  if (value === fallback && o[key] === undefined) return;
  if (value === fallback) delete o[key]; else o[key] = value;
}
function putFlag(o: Obj, key: string, on: boolean): void { if (on) o[key] = true; else delete o[key]; }
function putText(o: Obj, key: string, value: string): void { if (value) o[key] = value; else delete o[key]; }

export interface GpBuilt { value: GlobalpingMonitorOptions; target: string }

/**
 * Builds `globalping` on top of the stored object (same check type only), so keys and spellings the form didn't
 * change survive. Exactly one mode object is sent, `{}` when every mode option is a default.
 */
export function buildGlobalping(stored: GlobalpingMonitorOptions | undefined, d: GpDraft, errors: Partial<Record<GpField, string>>): GpBuilt {
  const o = clone(stored);
  const target = d.targets[d.mode].trim();
  const targetProblem = gpTargetProblem(d.mode, target);
  if (targetProblem) errors.gpTarget = targetProblem;

  if (str(o.mode).trim().toLowerCase() !== d.mode) o.mode = d.mode;
  const location = d.location.trim();
  if (!location) errors.gpLocation = 'Enter where the probe should run, like “Germany”, “AS13335” or “aws-eu-west-1”.';
  else if (utf8(location) > 256 || hasControl(location)) errors.gpLocation = 'Use at most 256 bytes on one line.';
  // An unedited location is resent exactly; the node passes it to Globalping as is.
  if (d.location !== str(stored?.location)) o.location = location;

  const api = d.apiUrl.trim();
  const apiIssue = apiProblem(api, d.insecure);
  if (apiIssue) errors.gpApi = apiIssue;
  putText(o, 'apiUrl', api);
  if (d.token && (d.token.length > 4096 || !/^[\x21-\x7e]+$/u.test(d.token))) errors.gpToken = 'Tokens are printable ASCII without spaces, at most 4,096 characters.';
  putText(o, 'token', d.token);
  putFlag(o, 'allowInsecureApi', d.insecure);
  const ca = d.ca.trim() ? d.ca : '';
  const caIssue = caProblem(ca);
  if (caIssue) errors.gpCa = caIssue;
  else if (ca && /^http:/iu.test(api)) errors.gpCa = 'Custom API certificates need an https:// API address.';
  putText(o, 'apiCa', ca);

  if (d.probeTimeout.trim()) {
    const n = Number(d.probeTimeout);
    if (!Number.isInteger(n) || n < 5 || n > 30) errors.gpProbe = 'Use 5 to 30 seconds, or leave empty for Globalping’s default.';
    o.probeTimeoutSeconds = n;
  } else delete o.probeTimeoutSeconds;

  const literal = d.mode === 'http' ? (() => { try { return new URL(target).hostname.replace(/^\[|\]$/gu, ''); } catch { return ''; } })() : target;
  if (d.ipVersion !== '0') {
    if (isIP(literal)) errors.gpIp = 'An IP address already fixes the family. Choose “Either” for an IP target.';
    o.ipVersion = Number(d.ipVersion);
  } else delete o.ipVersion;
  putFlag(o, 'apiFailureIsDown', d.failureDown);

  for (const other of ['ping', 'http', 'dns'] as const) if (other !== d.mode) delete o[other];
  const m = clone(stored && str(stored.mode).trim().toLowerCase() === d.mode ? stored[d.mode] : undefined);
  if (d.mode === 'ping') {
    if (d.pingPackets.trim()) {
      const n = Number(d.pingPackets);
      if (!Number.isInteger(n) || n < 1 || n > 16) errors.gpPing = 'Send 1 to 16 packets, or leave empty for 3.';
      m.packets = n;
    } else delete m.packets;
    putEnum(m, 'protocol', d.pingProtocol, 'ICMP');
    if (d.pingProtocol === 'TCP' && d.pingPort.trim()) {
      const n = Number(d.pingPort);
      if (!Number.isInteger(n) || n < 1 || n > 65535) errors.gpPing = 'Use a TCP port from 1 to 65535, or leave empty for 80.';
      m.port = n;
    } else delete m.port;
  }
  if (d.mode === 'http') {
    putEnum(m, 'method', d.httpMethod, 'GET');
    if (d.http2) {
      if (!/^https:/iu.test(target)) errors.gpTarget ??= 'HTTP/2 needs an https:// URL.';
      if (str(m.protocol).toUpperCase() !== 'HTTP2') m.protocol = 'HTTP2';
    } else delete m.protocol;
    const headers: Record<string, string> = {};
    const seen = new Set<string>();
    let size = 0;
    for (const row of d.headers) {
      const name = row.name.trim();
      if (!name && !row.value) continue;
      const lower = name.toLowerCase();
      if (!name || utf8(name) > 256 || !HEADER_NAME.test(name)) { errors.gpHeaders = `“${name || '(empty)'}” isn’t a valid header name.`; continue; }
      if (seen.has(lower)) { errors.gpHeaders = `${name} appears twice.`; continue; }
      if (lower === 'host' || lower === 'user-agent') { errors.gpHeaders = `${name} is set by the probe and can’t be overridden.`; continue; }
      if (/[\x00-\x08\x0a-\x1f\x7f]/u.test(row.value)) { errors.gpHeaders = `The value for ${name} contains control characters.`; continue; }
      seen.add(lower);
      size += utf8(name) + utf8(row.value);
      headers[name] = row.value;
    }
    if (seen.size > 32) errors.gpHeaders = 'Use at most 32 headers.';
    else if (size > 32 * 1024) errors.gpHeaders = 'Headers can total at most 32 KiB.';
    if (seen.size) m.headers = headers; else delete m.headers;
    const parsed = gpRanges(d.statuses);
    if (parsed.error) errors.gpStatuses = parsed.error;
    if (parsed.ranges) m.acceptedStatuses = parsed.ranges; else delete m.acceptedStatuses;
    if (d.keyword) {
      if (utf8(d.keyword) > 4096) errors.gpKeyword = 'Use a keyword of at most 4,096 bytes.';
      m.keyword = d.keyword;
    } else delete m.keyword;
    if (d.invert && !d.keyword) errors.gpKeyword = 'Enter the keyword that must be absent, or turn “Fail when found” off.';
    putFlag(m, 'invertKeyword', d.invert);
    if (d.jsonata.expression) {
      const built = buildJsonata(m.jsonata, d.jsonata);
      if (built.error) errors.gpJsonata = built.error;
      if (built.value) m.jsonata = built.value as unknown as JSONQueryOptions;
    } else delete m.jsonata;
    putFlag(m, 'ignoreTls', d.ignoreTls);
  }
  if (d.mode === 'dns') {
    putEnum(m, 'queryType', d.dnsType, 'A');
    putEnum(m, 'protocol', d.dnsProtocol, 'UDP');
    if (d.dnsPort.trim()) {
      const n = Number(d.dnsPort);
      if (!Number.isInteger(n) || n < 1 || n > 65535) errors.gpDns = 'Use a port from 1 to 65535, or leave empty for 53.';
      m.port = n;
    } else delete m.port;
    const resolver = d.resolver.trim();
    if (resolver && !gpHost(resolver, false)) errors.gpDns = 'The resolver is a host name or IP address, without a port, brackets or URL.';
    putText(m, 'resolver', resolver);
    if (d.regex) { const issue = re2Problem(d.regex); if (issue) errors.gpRegex = issue; }
    putText(m, 'regex', d.regex);
    putFlag(m, 'allowEmptyAnswers', d.allowEmpty);
  }
  o[d.mode] = m;
  return { value: o as unknown as GlobalpingMonitorOptions, target };
}

/**
 * The node compares `globalping` through Go's JSON encoding (`omitempty`), so a change from an absent key to its zero
 * value, or a reordering, isn't a new check. Spelling and every other value still count.
 */
export function gpCanonical(value: unknown): string {
  const walk = (v: unknown, depth: number, key?: string): unknown => {
    if (Array.isArray(v)) return v.length ? v.map(item => walk(item, depth + 1)) : undefined;
    if (v && typeof v === 'object') {
      const entries = Object.entries(v as Obj).sort(([a], [b]) => a.localeCompare(b));
      const out: Obj = {};
      for (const [k, inner] of entries) {
        // JSONata's expected value is raw JSON and acceptedStatuses bounds are always encoded, including zero values.
        if (key === 'jsonata' || key === 'acceptedStatuses') { out[k] = inner; continue; }
        const w = walk(inner, depth + 1, k);
        if (w !== undefined) out[k] = w;
      }
      // Map-valued headers are omitted when empty; mode objects stay as {}.
      if (key === 'headers' && !Object.keys(out).length) return undefined;
      return out;
    }
    if (v === false || v === 0 || v === '' || v === null || v === undefined) return depth === 0 ? v : (key === 'mode' || key === 'location' ? v : undefined);
    return v;
  };
  return JSON.stringify(walk(value ?? null, 0) ?? null);
}

/** A copy with the API token and request header values masked, for previews. */
export function gpMasked(o: GlobalpingMonitorOptions): GlobalpingMonitorOptions {
  const copy = structuredClone(o);
  if (copy.token) copy.token = '•••';
  if (copy.http?.headers) copy.http.headers = Object.fromEntries(Object.keys(copy.http.headers).map(name => [name, '•••']));
  return copy;
}

export interface GpFact { label: string; value: ReactNode }

/** Plain-language facts. Viewers receive no `globalping` object at all, and an HTTP target reduced to scheme://host/. */
export function gpFacts(m: Monitor, viewer: boolean, reveal: boolean): GpFact[] {
  const o = m.globalping;
  if (viewer || !o) {
    return [
      { label: 'Measured by', value: 'Globalping, a third-party probe network' },
      { label: 'Probe, provider and conditions', value: <span className="muted">Hidden from viewers</span> },
    ];
  }
  const mode = str(o.mode).toLowerCase();
  const facts: GpFact[] = [
    { label: 'Measured by', value: <>Globalping {mode === 'http' ? 'HTTP' : mode === 'dns' ? 'DNS' : 'ping'} from one probe matching <span className="mono wrap">{o.location}</span></> },
    { label: 'Globalping API', value: <><span className="mono wrap">{o.apiUrl ? (reveal ? o.apiUrl : maskedUrl(o.apiUrl)) : `${GP_DEFAULT_API} (default)`}</span>{o.allowInsecureApi ? <> · <span className="warn-text">plain HTTP allowed</span></> : null}{o.apiCa ? ` · system roots + ${plural((o.apiCa.match(/-----BEGIN CERTIFICATE-----/gu) ?? []).length, 'custom CA certificate')}` : ''}</> },
    { label: 'API token', value: o.token ? (reveal ? <span className="mono wrap">{o.token}</span> : <span className="muted">set · hidden</span>) : <span className="muted">None (anonymous quota)</span> },
    { label: 'Probe timeout', value: o.probeTimeoutSeconds ? span(o.probeTimeoutSeconds * 1000) : <span className="muted">Globalping’s default</span> },
    { label: 'IP version', value: o.ipVersion === 4 ? 'IPv4 only' : o.ipVersion === 6 ? 'IPv6 only' : 'Either' },
    { label: 'If Globalping fails', value: o.apiFailureIsDown ? <span className="warn-text">Down (API, quota and probe failures count against the target)</span> : 'Pending: API, quota and probe failures say nothing about the target' },
  ];
  if (mode === 'ping') {
    const p = o.ping ?? {};
    const protocol = upper(p.protocol, 'ICMP');
    facts.push({ label: 'Ping', value: <>{plural(p.packets || 3, 'packet')} · {protocol}{protocol === 'TCP' ? ` port ${p.port || 80}` : ''}</> });
    facts.push({ label: 'Up when', value: 'At least one reply arrives (partial loss is still up); complete loss is down' });
  }
  if (mode === 'http') {
    const h = o.http ?? {};
    const headers = Object.entries(h.headers ?? {});
    facts.push({ label: 'Request', value: <><span className="mono">{upper(h.method, 'GET')}</span>{str(h.protocol).toUpperCase() === 'HTTP2' ? ' · HTTP/2' : ''}{h.ignoreTls ? <> · <span className="warn-text">TLS not verified</span></> : ''}</> });
    facts.push({ label: 'Headers', value: headers.length ? <span className="hdrs">{headers.map(([name, value]) => <span key={name} className="mono"><b>{name}</b>: {reveal ? value : '•••'}</span>)}</span> : <span className="muted">None</span> });
    facts.push({ label: 'Accepts status', value: <span className="mono">{h.acceptedStatuses?.length ? rangesText(h.acceptedStatuses).replace(/-/gu, '–') : 'Any 2xx (default)'}</span> });
    if (h.keyword) facts.push({ label: h.invertKeyword ? 'Body must not contain' : 'Body must contain', value: <span className="mono wrap">{h.keyword}</span> });
    if (h.jsonata?.expression) facts.push({ label: 'JSONata condition', value: <><span className="mono wrap">{h.jsonata.expression}</span> {h.jsonata.operator} <span className="mono wrap">{JSON.stringify(h.jsonata.expected)}</span> <span className="muted small">· on the remote body, by the node’s isolated worker</span></> });
  }
  if (mode === 'dns') {
    const d = o.dns ?? {};
    facts.push({ label: 'Query', value: <><span className="mono">{upper(d.queryType, 'A')}</span> over {upper(d.protocol, 'UDP')} port {d.port || 53} · {d.resolver ? <>resolver <span className="mono">{d.resolver}</span></> : 'the probe’s resolver'}</> });
    facts.push({ label: 'Up when', value: <>{d.regex ? <>An answer value matches <span className="mono wrap">{d.regex}</span> (case-insensitive)</> : d.allowEmptyAnswers ? 'NOERROR, even with no answers' : 'NOERROR with at least one answer'}</> });
  }
  facts.push({ label: 'Shared with Globalping', value: <span className="muted">The target, location{mode === 'http' ? ', path, query and headers' : ''} go to Globalping and its probe. The token and API CA stay on the nodes.</span> });
  return facts;
}
