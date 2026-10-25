import type { ReactNode } from 'react';
import type { BrowserMonitorOptions, Monitor } from '@uptime-canary/shared';
import { MASK, maskSecretNamed, secretNamed, span } from './format';
import { SCREENSHOT_HEIGHT, SCREENSHOT_WIDTH } from './screenshot';
import { parseIPv4, parseIPv6 } from './tailscale';

/**
 * Real-browser checks (peer protocol 19). Each assigned node connects to a dedicated Chromium browser service over its
 * DevTools protocol (CDP), opens a fresh disposable browser context and private page, loads the target, and waits for the
 * main document's response and network idle, then an optional wait. Canary bundles and starts no browser: the service is
 * the operator's, with its own sandbox and network limits. The node validates everything; these rules mirror its
 * `browsermonitor.Validate` and Go's net/url parsing without being stricter, so a stored monitor can always be saved again
 * unchanged. Pure apart from the facts' JSX.
 */

/** The node's limits, in UTF-8 bytes for the endpoint, ASCII characters for the token and milliseconds for the wait. */
export const BROWSER_ENDPOINT_MAX = 2048;
export const BROWSER_BEARER_MAX = 4096;
export const BROWSER_DELAY_MAX = 30_000;
/** Browser checks one node runs at the same time, across all its monitors. Another one at that moment is pending. */
export const BROWSER_PARALLEL = 2;
/** The node's fixed result messages. The endpoint, the token, page content and the browser's own errors stay on the node. */
export const BROWSER_SUMMARY = { up: 'Browser page loaded', down: 'Browser navigation failed', pending: 'Browser service unavailable' } as const;
/** The pending causes, shared by the form note, the facts and the detail page. */
export const BROWSER_PENDING = 'the browser service can’t be reached, refuses the connection or the bearer token, or answers in a way the node doesn’t accept (a discovery answer that is unreadable or names a WebSocket on another host, port or scheme, or an unexpected DevTools reply); two browser checks are already running on this node; the page moves to an address that isn’t http:// or https://; a screenshot that was asked for can’t be taken, is larger than 192 KiB or no longer matches the page; the browser doesn’t confirm closing the private context; the check is cancelled; or the page doesn’t load and go network-idle within the timeout';

export interface BrowserDraft {
  /** As stored or typed. An unedited stored endpoint goes back byte for byte; a typed one is trimmed. */
  endpoint: string;
  /** Empty omits `bearerToken`. Kept byte for byte while unedited, like the endpoint. */
  bearer: string;
  screenshot: boolean;
  /** Whole milliseconds as typed. Empty or 0 omits `delayMs`, as Go's omitempty encoding does. */
  delay: string;
}

export type BrowserField = 'browserEndpoint' | 'browserBearer' | 'browserDelay';
export const BROWSER_FIELD_LABEL: Record<BrowserField, string> = { browserEndpoint: 'Browser endpoint', browserBearer: 'Bearer token', browserDelay: 'Wait after load' };

type Obj = Record<string, unknown>;
const str = (value: unknown): string => typeof value === 'string' ? value : '';
const isObj = (value: unknown): value is Obj => typeof value === 'object' && value !== null && !Array.isArray(value);

export function browserDraftFrom(o: BrowserMonitorOptions | undefined): BrowserDraft {
  const delay = o?.delayMs;
  return { endpoint: str(o?.endpoint), bearer: str(o?.bearerToken), screenshot: o?.screenshot === true, delay: typeof delay === 'number' && Number.isFinite(delay) && delay !== 0 ? String(delay) : '' };
}

// ---------- the node's rules: browsermonitor.Validate over Go's url.Parse (Go 1.27, strict colons for http and https)

const utf8 = (text: string): number => new TextEncoder().encode(text).length;
const HEX2 = /^[0-9A-Fa-f]{2}$/u;
/** ASCII that Go's net/url leaves unescaped in a host or an IPv6 zone (shouldEscape(c, encodeHost) is false). */
const HOST_ASCII = /^[A-Za-z0-9_.~!$&'()*+,;=:<>"[\]-]$/u;
const validOptionalPort = (port: string): boolean => port === '' || /^:\d*$/u.test(port);

/**
 * Go's url unescape for a host, a zone or a path: null where Go returns an error, else the decoded text with each escaped
 * byte as one character (only its emptiness and ASCII matter here). A host may escape only bytes from 0x80 up, and "%25";
 * a zone may escape "%25", a space and ASCII a host allows; a path may escape anything. In a host or a zone, unescaped
 * ASCII outside the host set is refused, while other characters pass as written.
 */
function goUnescape(text: string, mode: 'host' | 'zone' | 'path'): string | null {
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]!;
    if (ch === '%') {
      const hex = text.slice(i + 1, i + 3);
      if (!HEX2.test(hex)) return null;
      const value = parseInt(hex, 16);
      if (mode === 'host' && value < 0x80 && hex !== '25') return null;
      if (mode === 'zone' && hex !== '25' && value !== 0x20 && !(value < 0x80 && HOST_ASCII.test(String.fromCharCode(value)))) return null;
      out += String.fromCharCode(value);
      i += 2;
    } else {
      if (mode !== 'path' && ch.charCodeAt(0) < 0x80 && !HOST_ASCII.test(ch)) return null;
      out += ch;
    }
  }
  return out;
}

/** Go's netip.ParseAddr: the first dot, colon or percent sign picks IPv4, IPv6 with an optional non-empty zone, or an error. */
function goParseAddr(text: string): { v6: boolean } | null {
  for (const ch of text) {
    if (ch === '.') return parseIPv4(text) ? { v6: false } : null;
    if (ch === ':') {
      const zone = text.indexOf('%');
      if (zone === text.length - 1) return null;
      return parseIPv6(zone < 0 ? text : text.slice(0, zone)) ? { v6: true } : null;
    }
    if (ch === '%') return null;
  }
  return null;
}

type Refusal = 'control' | 'fragment' | 'scheme' | 'authority' | 'user' | 'host' | 'literal' | 'port' | 'escape' | 'nohost' | 'origin';

/**
 * Go's parseHost: the host with any port, decoded, or why Go refuses it. A bracketed host must hold an IPv6 address (an
 * optional %25 zone included), never IPv4; after it only a colon and digits may follow. Otherwise one colon starts the
 * port, which is digits only and may be empty; an http or https host with more colons is refused, while ws and wss take
 * the last colon as the port's. Port numbers aren't range-checked by the parser or the node.
 */
function goHost(scheme: string, host: string): { host: string } | { refusal: Refusal } {
  const open = host.lastIndexOf('[');
  if (open > 0) return { refusal: 'literal' };
  if (open === 0) {
    const close = host.lastIndexOf(']');
    if (close < 0) return { refusal: 'literal' };
    const colonPort = host.slice(close + 1);
    if (!validOptionalPort(colonPort)) return { refusal: 'port' };
    const name = host.slice(1, close);
    const zoneAt = name.indexOf('%25');
    const head = goUnescape(zoneAt < 0 ? name : name.slice(0, zoneAt), 'host');
    const zone = zoneAt < 0 ? '' : goUnescape(name.slice(zoneAt), 'zone');
    if (head === null || zone === null || !goParseAddr(head + zone)?.v6) return { refusal: 'literal' };
    return { host: `[${head + zone}]${colonPort}` };
  }
  const first = host.indexOf(':');
  if (first >= 0) {
    const last = host.lastIndexOf(':');
    const at = last !== first && scheme !== 'http' && scheme !== 'https' ? last : first;
    if (!validOptionalPort(host.slice(at))) return { refusal: 'port' };
  }
  const decoded = goUnescape(host, 'host');
  return decoded === null ? { refusal: 'host' } : { host: decoded };
}

/** Go's URL.Hostname: the host without a trailing :digits port and without the brackets of an IPv6 literal. */
function goHostname(host: string): string {
  let name = host;
  const colon = name.lastIndexOf(':');
  if (colon >= 0 && validOptionalPort(name.slice(colon))) name = name.slice(0, colon);
  return name.startsWith('[') && name.endsWith(']') ? name.slice(1, -1) : name;
}

/**
 * Exactly the node's admission rule for an endpoint, or the first reason it refuses one: url.Parse succeeds; the scheme is
 * ws, wss, http or https (any case); there is a host name; no user information and no fragment text (a bare trailing "#"
 * passes, as in Go); and an http or https address is an origin with at most "/" as its path and no query. The byte limit,
 * control characters and unpaired surrogates are checked before this.
 */
function endpointRefusal(endpoint: string): Refusal | undefined {
  const hash = endpoint.indexOf('#');
  const main = hash < 0 ? endpoint : endpoint.slice(0, hash);
  if (/[\x00-\x1f\x7f]/u.test(main)) return 'control';
  if (hash >= 0 && hash < endpoint.length - 1) return 'fragment';
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/u.exec(main);
  const name = scheme?.[1]?.toLowerCase() ?? '';
  if (!['ws', 'wss', 'http', 'https'].includes(name)) return 'scheme';
  let rest = main.slice(scheme![0].length);
  let query = '';
  if (rest.endsWith('?') && rest.indexOf('?') === rest.length - 1) rest = rest.slice(0, -1);
  else if (rest.includes('?')) { query = rest.slice(rest.indexOf('?') + 1); rest = rest.slice(0, rest.indexOf('?')); }
  if (!rest.startsWith('//')) return 'authority';
  let authority = rest.slice(2);
  let path = '';
  const slash = authority.indexOf('/');
  if (slash >= 0) { path = authority.slice(slash); authority = authority.slice(0, slash); }
  const at = authority.lastIndexOf('@');
  const parsed = goHost(name, at < 0 ? authority : authority.slice(at + 1));
  if ('refusal' in parsed) return parsed.refusal;
  // Go either fails to parse the user information or sets url.User; the node refuses both.
  if (at >= 0) return 'user';
  const decoded = goUnescape(path, 'path');
  if (decoded === null) return 'escape';
  if (!goHostname(parsed.host)) return 'nohost';
  if ((name === 'http' || name === 'https') && ((decoded !== '' && decoded !== '/') || query !== '')) return 'origin';
  return undefined;
}

/** What node validators read from Go's url.Parse result, for other absolute links (protocol 19 status-page links). */
export interface GoUrl { scheme: string; hostname: string; user: boolean; opaque: string; rawQuery: string; forceQuery: boolean; fragment: string }

/**
 * Go 1.27's url.Parse for a link with a scheme, or null where it returns an error: control bytes anywhere; a bad
 * %-escape in the fragment or path; a host Go's parseHost refuses (strict colons for http and https, bracketed IPv6
 * only). Text without a scheme also gives null; the node's link rules refuse it anyway. The scheme is lowercased, as
 * Go does. User information is reported, never decoded: the node refuses it either way.
 */
export function goParseUrl(text: string): GoUrl | null {
  if (/[\x00-\x1f\x7f]/u.test(text)) return null;
  const hash = text.indexOf('#');
  const main = hash < 0 ? text : text.slice(0, hash);
  const fragment = goUnescape(hash < 0 ? '' : text.slice(hash + 1), 'path');
  if (fragment === null) return null;
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/u.exec(main);
  if (!scheme) return null;
  const name = scheme[1]!.toLowerCase();
  let rest = main.slice(scheme[0].length);
  let rawQuery = '';
  let forceQuery = false;
  if (rest.endsWith('?') && rest.indexOf('?') === rest.length - 1) { forceQuery = true; rest = rest.slice(0, -1); }
  else if (rest.includes('?')) { rawQuery = rest.slice(rest.indexOf('?') + 1); rest = rest.slice(0, rest.indexOf('?')); }
  if (!rest.startsWith('/')) return { scheme: name, hostname: '', user: false, opaque: rest, rawQuery, forceQuery, fragment };
  let hostname = '';
  let user = false;
  if (rest.startsWith('//')) {
    let authority = rest.slice(2);
    let path = '';
    const slash = authority.indexOf('/');
    if (slash >= 0) { path = authority.slice(slash); authority = authority.slice(0, slash); }
    const at = authority.lastIndexOf('@');
    user = at >= 0;
    const parsed = goHost(name, at < 0 ? authority : authority.slice(at + 1));
    if ('refusal' in parsed) return null;
    hostname = goHostname(parsed.host);
    rest = path;
  }
  if (goUnescape(rest, 'path') === null) return null;
  return { scheme: name, hostname, user, opaque: '', rawQuery, forceQuery, fragment };
}

const REFUSED: Record<Refusal, string> = {
  control: 'Remove line breaks, tabs and other control characters.',
  fragment: 'Remove the #fragment.',
  scheme: 'Start with ws:// or wss:// for the DevTools WebSocket URL, or http:// or https:// for the browser’s DevTools address.',
  authority: 'Write a complete address with // and a host, like ws://chromium.internal:9222/devtools/browser/… or http://chromium.internal:9222.',
  user: 'Remove user:password@ from the address. If the service needs a credential, enter it as the bearer token below.',
  host: 'Check the host name: it can’t contain spaces or characters such as \\ ^ ` { | }, and only characters outside ASCII may be %-escaped there.',
  literal: 'Put an IPv6 address in brackets, like [::1]:9222, and nothing else: an IPv4 address goes without brackets.',
  port: 'After the host, a port is digits only, like :9222.',
  escape: 'Fix the %-escapes in the path: each % needs two hexadecimal digits after it.',
  nohost: 'Enter the host name or address after //, before any port.',
  origin: 'An http:// or https:// address is only the browser’s DevTools origin: remove the path and the query. The node asks /json/version there itself. For a full WebSocket URL, use ws:// or wss://.',
};

export function browserEndpointProblem(endpoint: string): string | undefined {
  if (!endpoint) return 'Enter the browser service’s DevTools address: its WebSocket URL, like ws://chromium.internal:9222/devtools/browser/…, or its origin, like http://chromium.internal:9222.';
  if (utf8(endpoint) > BROWSER_ENDPOINT_MAX) return `Use at most ${BROWSER_ENDPOINT_MAX.toLocaleString('en-US')} bytes. Characters outside ASCII take 2 to 4 bytes each.`;
  if (/\p{Cs}/u.test(endpoint)) return 'The address holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  const refusal = endpointRefusal(endpoint);
  return refusal ? REFUSED[refusal] : undefined;
}

/** Optional: printable ASCII without spaces (bytes 33–126), at most 4,096 of them. */
export function browserBearerProblem(token: string): string | undefined {
  if (!token) return undefined;
  if (/[\x00-\x1f\x7f]/u.test(token)) return 'Remove line breaks, tabs and other control characters from the token.';
  if (/\s/u.test(token)) return 'Tokens have no spaces. Paste the token alone, without “Bearer ”: the node adds that itself.';
  if (/[^\x21-\x7e]/u.test(token)) return 'Use printable ASCII only: letters, digits and punctuation. Paste the token alone, without quotes or other text.';
  if (token.length > BROWSER_BEARER_MAX) return `Use at most ${BROWSER_BEARER_MAX.toLocaleString('en-US')} characters.`;
  return undefined;
}

/** Optional: whole milliseconds from 0 to 30,000, and shorter than the timeout when one is known. */
export function browserDelayProblem(delay: string, timeoutMs: number | null): string | undefined {
  const text = delay.trim();
  if (!text) return undefined;
  if (!/^\d+$/u.test(text) || Number(text) > BROWSER_DELAY_MAX) return 'Use a whole number of milliseconds from 0 to 30,000, or leave it empty for no wait.';
  if (timeoutMs !== null && Number(text) >= timeoutMs) return `Use less than the timeout (${span(timeoutMs)}): the wait counts toward it, and the page has to load first.`;
  return undefined;
}

/** How the node reaches the service, from the endpoint's scheme alone, so it can be named without showing the address. */
export function browserEndpointKind(endpoint: string): { kind: 'websocket' | 'discovery' | 'unknown'; encrypted: boolean } {
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/u.exec(endpoint)?.[1]?.toLowerCase() ?? '';
  return { kind: scheme === 'ws' || scheme === 'wss' ? 'websocket' : scheme === 'http' || scheme === 'https' ? 'discovery' : 'unknown', encrypted: scheme === 'wss' || scheme === 'https' };
}

const clone = (value: unknown): Obj => isObj(value) ? structuredClone(value) : {};
const KNOWN = new Set(['endpoint', 'bearerToken', 'screenshot', 'delayMs']);

export interface BrowserBuilt { value: BrowserMonitorOptions }

/**
 * Builds `browser` on top of the stored object, so its key order and keys from a newer node survive. The endpoint and the
 * token go back byte for byte while unedited and are trimmed once typed. Go's omitempty fields are omitted when empty,
 * false or 0. `timeoutMs` is the monitor's timeout, which the wait must stay below; null skips that comparison.
 */
export function buildBrowser(stored: BrowserMonitorOptions | undefined, d: BrowserDraft, timeoutMs: number | null, errors: Partial<Record<BrowserField, string>>): BrowserBuilt {
  const o = clone(stored);
  const endpoint = d.endpoint === str(stored?.endpoint) ? d.endpoint : d.endpoint.trim();
  const endpointProblem = browserEndpointProblem(endpoint);
  if (endpointProblem) errors.browserEndpoint = endpointProblem;
  o.endpoint = endpoint;

  const bearer = d.bearer === str(stored?.bearerToken) ? d.bearer : d.bearer.trim();
  const bearerProblem = browserBearerProblem(bearer);
  if (bearerProblem) errors.browserBearer = bearerProblem;
  if (bearer) o.bearerToken = bearer; else delete o.bearerToken;

  if (d.screenshot) o.screenshot = true; else delete o.screenshot;

  const delay = d.delay.trim();
  const delayProblem = browserDelayProblem(delay, timeoutMs);
  if (delayProblem) errors.browserDelay = delayProblem;
  if (delay && Number(delay) !== 0) o.delayMs = Number(delay); else delete o.delayMs;
  return { value: o as unknown as BrowserMonitorOptions };
}

/**
 * The node compares `browser` through Go's JSON encoding: `endpoint` is always encoded; an empty token, false screenshot
 * and zero wait are absent. Keys from a newer node compare as stored.
 */
export function browserCanonical(value: unknown): string {
  const o = isObj(value) ? value : {};
  const out: Obj = { endpoint: str(o.endpoint) };
  for (const key of Object.keys(o).sort()) {
    const inner = o[key];
    if (key === 'endpoint' || inner === undefined || inner === null) continue;
    if ((key === 'bearerToken' && inner === '') || (key === 'screenshot' && inner === false) || (key === 'delayMs' && inner === 0)) continue;
    out[key] = inner;
  }
  return JSON.stringify(out);
}

/**
 * A copy for masked previews and conflict views: the whole endpoint and the token are replaced (an endpoint can carry a
 * credential in its path or query), and unknown keys from a newer node have the text of credential-named ones hidden. The
 * screenshot setting and the wait stay. A pasted masked copy is refused, never saved with the placeholder.
 */
export function browserMasked<T>(o: T): T {
  if (!isObj(o)) return o;
  const copy = structuredClone(o) as Obj;
  for (const key of Object.keys(copy)) {
    if (key === 'endpoint' || key === 'bearerToken') { if (copy[key]) copy[key] = MASK; }
    else if (!KNOWN.has(key)) copy[key] = maskSecretNamed(copy[key], secretNamed(key));
  }
  return copy as unknown as T;
}

export interface BrowserFact { label: string; value: ReactNode }

const hidden = (text: string): ReactNode => <span className="muted">{text}</span>;

const loads = (m: Monitor): BrowserFact => ({
  label: 'Loads in',
  value: <>A dedicated Chromium browser service, over its DevTools protocol (CDP). For each check, every assigned node opens a fresh private browser context and page there, loads <span className="mono wrap">{m.target}</span> and closes the context again. Downloads are denied and only http:// and https:// requests are allowed. The page’s own scripts run as in any browser; the check adds none and runs no commands.</>,
});

function rules(o: BrowserMonitorOptions | undefined): BrowserFact[] {
  return [
    { label: 'Up when', value: `The main document answers with a 2xx or 3xx status and the page is network-idle${o?.delayMs ? ', then the wait ends' : ''}${o?.screenshot ? ' and the screenshot is taken' : ''}` },
    { label: 'Down when', value: 'The browser reports that the navigation failed (for example a DNS, connection or certificate error), the address starts a download, or the main document answers with any other status' },
    { label: 'Pending when', value: `The node gets no definite answer: ${BROWSER_PENDING}. Pending is never inverted and never recovers an outage.` },
    { label: 'Latency', value: `Page-load time, from the start of navigation until the page is network-idle${o?.delayMs ? ', including the wait' : ''}. Not a single network round trip; a dash when the navigation failed.` },
  ];
}

const RESULTS: BrowserFact = {
  label: 'Results',
  value: <>Only “{BROWSER_SUMMARY.up}”, “{BROWSER_SUMMARY.down}” or “{BROWSER_SUMMARY.pending}”, with the page’s HTTP status when one arrived. The endpoint, the token and the browser’s own messages stay on the node.</>,
};

/**
 * Plain-language facts. Viewers receive no `browser` object, so its rows say hidden rather than implying nothing is set;
 * the page address is the target and stays visible. The endpoint and the token show only when `reveal` is on.
 */
export function browserFacts(m: Monitor, viewer: boolean, reveal: boolean): BrowserFact[] {
  const o = m.browser;
  if (viewer || !o) {
    return [loads(m), ...rules(undefined), RESULTS, { label: 'Browser service and options', value: hidden('Hidden from viewers') }, { label: 'Screenshots', value: hidden('Private to editors and admins, when turned on') }];
  }
  const endpoint = str(o.endpoint);
  const { kind, encrypted } = browserEndpointKind(endpoint);
  const delay = typeof o.delayMs === 'number' ? o.delayMs : 0;
  return [
    loads(m),
    {
      label: 'Browser service',
      value: <>
        {endpoint ? (reveal ? <span className="mono wrap">{endpoint}</span> : hidden('(set · hidden)')) : <span className="warn-text">(missing)</span>}
        {kind === 'websocket' ? ' · connects to this DevTools WebSocket URL' : kind === 'discovery' ? ' · reads /json/version at this address, then connects only to a WebSocket it names on the same host and port' : null}
        {kind !== 'unknown' && !encrypted && o.bearerToken ? <> · <span className="warn-text">unencrypted, so the token travels in clear</span></> : null}
      </>,
    },
    { label: 'Bearer token', value: o.bearerToken ? <>{reveal ? <span className="mono wrap">{o.bearerToken}</span> : hidden('set · hidden')} · sent only to the browser service, never to the page</> : hidden('None') },
    { label: 'Wait after load', value: delay ? `${span(delay)} once the page is network-idle` : hidden('None') },
    {
      label: 'Screenshot',
      value: o.screenshot
        ? `A ${SCREENSHOT_WIDTH}×${SCREENSHOT_HEIGHT} PNG of each loaded page, at most 192 KiB. Each node keeps only its own newest one for this monitor, privately: never replicated, on status pages or shown to viewers. A capture that fails makes the check pending.`
        : hidden('Off'),
    },
    ...rules(o),
    RESULTS,
  ];
}
