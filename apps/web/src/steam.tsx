import type { ReactNode } from 'react';
import type { Monitor, SteamMonitorOptions } from '@uptime-canary/shared';
import { maskedUrl, plural } from './format';
import { gpHost } from './globalping';

/**
 * Steam game server directory checks (peer protocol 17). Each assigned node resolves the target's host (IPv4 first) and
 * asks the Steam Web API, or a trusted endpoint standing in for it, whether exactly that address and port is listed in
 * Steam's game server directory. After a positive listing it may send one ICMP echo for a round trip. A listing is not a
 * direct game-protocol (A2S) query of the server. The node's leaf validates everything; these rules mirror it without
 * being stricter, so a stored configuration can always be saved again unchanged. Pure apart from the facts' JSX.
 */

export const STEAM_DEFAULT_API = 'https://api.steampowered.com';
/** What the node appends to the API base, path prefix included. */
export const STEAM_ENDPOINT = '/IGameServersService/GetServerList/v1/';
export const STEAM_KEY_MAX = 256;
export const STEAM_CA_MAX = 128 * 1024;
export const STEAM_PACKET_MAX = 65_500;
export const STEAM_PACKET_DEFAULT = 32;
/** Steam's Web API terms of use: calls per key and day. A custom endpoint may have its own limit. */
export const STEAM_DAILY_CALLS = 100_000;

export interface SteamDraft {
  /** host:port, or [IPv6]:port. */
  target: string;
  apiKey: string;
  apiUrl: string; ca: string; insecure: boolean;
  failureDown: boolean;
  /** `skipLatency` inverted: on unless the stored options skip it. */
  measure: boolean;
  /** The ICMP payload in bytes as typed. Empty omits `packetSize` (32 bytes); "0" is kept as a zero payload. */
  size: string;
}

export type SteamField = 'steamTarget' | 'steamKey' | 'steamApi' | 'steamCa' | 'steamSize';
export const STEAM_FIELD_LABEL: Record<SteamField, string> = {
  steamTarget: 'Server address', steamKey: 'Steam Web API key', steamApi: 'Steam API address', steamCa: 'Extra CA certificates for the API', steamSize: 'ICMP payload size',
};
/** Which disclosure holds a field, so a failed save opens it. */
export const STEAM_SECTION: Partial<Record<SteamField, string>> = { steamApi: 'steamApi', steamCa: 'steamApi' };

const str = (value: unknown): string => typeof value === 'string' ? value : '';

export function steamDraftFrom(o: SteamMonitorOptions | undefined, target: string): SteamDraft {
  return {
    target,
    apiKey: str(o?.apiKey), apiUrl: str(o?.apiUrl), ca: str(o?.apiCa), insecure: Boolean(o?.allowInsecureApi),
    failureDown: Boolean(o?.apiFailureIsDown), measure: !o?.skipLatency,
    size: typeof o?.packetSize === 'number' && Number.isFinite(o.packetSize) ? String(o.packetSize) : '',
  };
}

// ---------- the leaf's rules

const utf8 = (text: string): number => new TextEncoder().encode(text).length;

/** Go's net.SplitHostPort: the port follows the last colon; only a bracketed host may contain colons. */
function splitHostPort(text: string): { host: string; port: string } | undefined {
  const last = text.lastIndexOf(':');
  if (last < 0) return undefined;
  let host: string, open = 0, close = 0;
  if (text.startsWith('[')) {
    const end = text.indexOf(']');
    if (end < 0 || end + 1 !== last) return undefined;
    host = text.slice(1, end); open = 1; close = end + 1;
  } else {
    host = text.slice(0, last);
    if (host.includes(':')) return undefined;
  }
  if (text.slice(open).includes('[') || text.slice(close).includes(']')) return undefined;
  return { host, port: text.slice(last + 1) };
}

/** The game server as the node checks it: host:port or [IPv6]:port, an ASCII host name or IP, a canonical port. */
export function steamTargetProblem(value: string): string | undefined {
  const text = value.trim();
  if (!text) return 'Enter the game server’s address and port, like game.example.net:27015.';
  if (text.length > 320) return 'Use at most 320 characters.';
  const parts = splitHostPort(text);
  if (!parts) return 'Use host:port, or [IPv6]:port for an IPv6 address, like game.example.net:27015 or [2001:db8::10]:27015.';
  if (!parts.host) return 'Enter the host name or IP address before the port.';
  if (parts.host.includes('%')) return 'Remove the IPv6 zone (%…): zones aren’t supported.';
  if (/[^\x00-\x7f]/u.test(parts.host)) return 'Write international host names in punycode (xn--…), as the node checks ASCII names only.';
  if (!gpHost(parts.host, false)) return 'Use a host name (letters, digits and hyphens) or an IP address.';
  if (!/^[1-9]\d{0,4}$/u.test(parts.port) || Number(parts.port) > 65535) return 'The port is a whole number from 1 to 65535, without leading zeros.';
  return undefined;
}

export function steamKeyProblem(key: string): string | undefined {
  if (!key) return 'Enter the Steam Web API key. Every Steam check needs one.';
  if (/\s/u.test(key)) return 'Keys have no spaces or line breaks. Paste the key alone.';
  if (/[^\x21-\x7e]/u.test(key)) return 'Keys are printable ASCII only. Paste the key alone, without quotes or other text.';
  if (key.length > STEAM_KEY_MAX) return `Use at most ${STEAM_KEY_MAX} characters.`;
  return undefined;
}

/** Characters Go's URL parser leaves unescaped in a path. Anything else makes it keep a raw path, which the node refuses. */
const PATH_PLAIN = /^[A-Za-z0-9\-._~$&+,/:;=@]$/u;
/** The decoded path, or undefined where the node would see an escaped path: only escapes Go writes itself survive. */
function decodedPath(raw: string): string | undefined {
  let path = '';
  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i]!;
    if (ch !== '%') {
      if (!PATH_PLAIN.test(ch)) return undefined;
      path += ch;
      continue;
    }
    const hex = raw.slice(i + 1, i + 3);
    if (!/^[0-9A-F]{2}$/u.test(hex)) return undefined;
    const decoded = String.fromCharCode(parseInt(hex, 16));
    if (!/^[\x21-\x7e]$/u.test(decoded) || PATH_PLAIN.test(decoded)) return undefined;
    path += decoded;
    i += 2;
  }
  return path;
}
/** Go's path.Clean for a rooted path. */
const cleanPath = (path: string): string => {
  const out: string[] = [];
  for (const segment of path.split('/')) {
    if (!segment || segment === '.') continue;
    if (segment === '..') out.pop(); else out.push(segment);
  }
  return `/${out.join('/')}`;
};

/** An optional API base: https://, or http:// with the opt-in; host, optional port and a clean path prefix only. */
export function steamApiProblem(value: string, insecure: boolean): string | undefined {
  if (!value) return undefined;
  if (value.length > 2048) return 'Use an address of at most 2,048 characters.';
  if (/[\s\\]/u.test(value)) return 'Remove spaces, line breaks and backslashes from the address.';
  if (/[^\x21-\x7e]/u.test(value)) return 'Write the address in ASCII (punycode for international host names).';
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):(.*)$/su.exec(value);
  if (!scheme) return `Enter a complete https:// address, like ${STEAM_DEFAULT_API}.`;
  const protocol = scheme[1]!.toLowerCase();
  if (protocol !== 'https' && protocol !== 'http') return 'Use an https:// address.';
  if (protocol === 'http' && !insecure) return 'Plain http:// needs “Allow plain HTTP for the API” below.';
  let rest = scheme[2]!;
  const hash = rest.indexOf('#');
  // Go drops a bare trailing "#"; any fragment text is refused.
  if (hash >= 0 && hash < rest.length - 1) return 'Remove the #fragment.';
  if (hash >= 0) rest = rest.slice(0, hash);
  if (rest.includes('?')) return 'Remove the ?query. The node adds its own parameters, the key among them.';
  if (!rest.startsWith('//')) return `Enter a complete https:// address, like ${STEAM_DEFAULT_API}.`;
  rest = rest.slice(2);
  const slash = rest.indexOf('/');
  const authority = slash < 0 ? rest : rest.slice(0, slash);
  if (authority.includes('@')) return 'Remove user:password@ from the address. The API key is the only credential, and the node adds it.';
  if (authority.includes('%')) return 'Remove the zone or %-escapes from the host.';
  let host: string, port: string | undefined;
  if (authority.startsWith('[')) {
    const end = authority.indexOf(']');
    if (end < 0) return 'Close the bracketed IPv6 address with ].';
    host = authority.slice(1, end);
    const after = authority.slice(end + 1);
    if (after && !after.startsWith(':')) return 'Only a :port may follow the bracketed address.';
    port = after ? after.slice(1) : undefined;
  } else {
    const colon = authority.lastIndexOf(':');
    host = colon < 0 ? authority : authority.slice(0, colon);
    port = colon < 0 ? undefined : authority.slice(colon + 1);
  }
  if (!host) return 'The address needs a host name.';
  if (!gpHost(host, false)) return 'Use an ASCII host name (punycode for other scripts) or an IP address for the API host.';
  // A colon must be followed by a port: the node refuses a dangling one.
  if (port !== undefined && (!/^\d+$/u.test(port) || Number(port) < 1 || Number(port) > 65535)) return 'After the colon, give a port from 1 to 65535, or remove the colon.';
  const raw = slash < 0 ? '' : rest.slice(slash);
  const path = decodedPath(raw);
  if (path === undefined) return 'The path prefix may use letters, digits and - . _ ~ $ & + , : ; = @, without %-escapes.';
  if (path && path !== '/' && cleanPath(path) !== path.replace(/\/$/u, '')) return 'Remove empty, “.” and “..” segments from the path prefix.';
  return undefined;
}

/**
 * Go's bytes.TrimSpace whitespace (unicode.IsSpace, the Unicode White_Space property). Unlike JavaScript's trim and \s it
 * includes NEL and excludes the BOM, so a CA the node accepts as blank stays editable and a BOM is never blank.
 */
const goBlank = (text: string): boolean => /^\p{White_Space}*$/u.test(text);
const goTrim = (text: string): string => text.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
/** A PEM block as Go's pem.Decode reads it: base64 lines may hold only spaces, tabs, CR and LF besides the alphabet. */
const CERTIFICATE = /^-----BEGIN CERTIFICATE-----[ \t]*\r?\n[A-Za-z0-9+/= \t\r\n]*\n-----END CERTIFICATE-----$/u;
/** PEM certificates only, at most 128 KiB in total, the byte bound first as on the node. The node parses each one; this checks the shape. */
export function steamCaProblem(pem: string): string | undefined {
  if (utf8(pem) > STEAM_CA_MAX) return 'Use at most 128 KiB of certificates.';
  const text = goTrim(pem);
  if (!text) return undefined;
  const blocks = text.split(/(?<=-----END CERTIFICATE-----)\p{White_Space}*/u).filter(Boolean);
  if (!blocks.every(block => CERTIFICATE.test(block))) return 'Paste PEM certificates only (-----BEGIN CERTIFICATE-----), without keys, headers or other text.';
  return undefined;
}

type Obj = Record<string, unknown>;
const clone = (value: unknown): Obj => value && typeof value === 'object' && !Array.isArray(value) ? structuredClone(value) as Obj : {};
function putFlag(o: Obj, key: string, on: boolean): void { if (on) o[key] = true; else delete o[key]; }
function putText(o: Obj, key: string, value: string): void { if (value) o[key] = value; else delete o[key]; }

export interface SteamBuilt { value: SteamMonitorOptions; target: string }

/**
 * Builds `steam` on top of the stored object, so keys from a newer node survive. Go's omitempty fields are omitted
 * when false or empty; `packetSize` is a pointer on the node, so 0 is kept and only an empty field omits it.
 */
export function buildSteam(stored: SteamMonitorOptions | undefined, d: SteamDraft, errors: Partial<Record<SteamField, string>>): SteamBuilt {
  const o = clone(stored);
  const target = d.target.trim();
  const targetProblem = steamTargetProblem(target);
  if (targetProblem) errors.steamTarget = targetProblem;

  const key = d.apiKey.trim();
  const keyProblem = steamKeyProblem(key);
  if (keyProblem) errors.steamKey = keyProblem;
  o.apiKey = key;

  const api = d.apiUrl.trim();
  const apiProblem = steamApiProblem(api, d.insecure);
  if (apiProblem) errors.steamApi = apiProblem;
  putText(o, 'apiUrl', api);
  // Sent as typed. An unedited stored value goes back byte for byte, Go-whitespace-only included; an edited value that
  // is only whitespace is omitted.
  const ca = d.ca === str(stored?.apiCa) || !goBlank(d.ca) ? d.ca : '';
  const caProblem = steamCaProblem(ca);
  if (caProblem) errors.steamCa = caProblem;
  putText(o, 'apiCa', ca);
  putFlag(o, 'allowInsecureApi', d.insecure);
  putFlag(o, 'apiFailureIsDown', d.failureDown);
  putFlag(o, 'skipLatency', !d.measure);

  const size = d.size.trim();
  if (size) {
    const n = Number(size);
    if (!Number.isInteger(n) || n < 0 || n > STEAM_PACKET_MAX) errors.steamSize = 'Use 0 to 65,500 bytes, or leave empty for 32.';
    o.packetSize = n;
  } else delete o.packetSize;
  return { value: o as unknown as SteamMonitorOptions, target };
}

/**
 * The node compares `steam` through Go's JSON encoding: apiKey is always encoded (absent means empty), false and empty
 * omitempty fields are absent, and packetSize is a pointer, so 0 differs from absent. Keys from a newer node compare as stored.
 */
export function steamCanonical(value: unknown): string {
  const o = value && typeof value === 'object' && !Array.isArray(value) ? value as Obj : {};
  const out: Obj = { apiKey: str(o.apiKey) };
  for (const key of Object.keys(o).sort()) {
    const inner = o[key];
    if (key === 'apiKey' || inner === undefined || inner === null) continue;
    if (['apiUrl', 'apiCa', 'allowInsecureApi', 'apiFailureIsDown', 'skipLatency'].includes(key) && (inner === false || inner === '')) continue;
    out[key] = inner;
  }
  return JSON.stringify(out);
}

/** A copy with the API key masked. Used everywhere outside the key's own field, including revealed previews. */
export function steamMasked<T>(o: T): T {
  if (!o || typeof o !== 'object' || Array.isArray(o)) return o;
  const copy = structuredClone(o) as unknown as Obj;
  if (copy.apiKey) copy.apiKey = '•••';
  return copy as unknown as T;
}

export interface SteamFact { label: string; value: ReactNode }

const listing = (m: Monitor): SteamFact => ({
  label: 'Steam directory',
  value: <>Is <span className="mono wrap">{m.target}</span> listed in Steam’s game server directory? Each node resolves the host (IPv4 first) and asks for that exact address and port. A directory listing, not a game-protocol query of the server itself.</>,
});

/** Plain-language facts. Viewers receive no `steam` object; the target isn't secret and stays visible. */
export function steamFacts(m: Monitor, viewer: boolean, reveal: boolean): SteamFact[] {
  const o = m.steam;
  if (viewer || !o) {
    return [
      listing(m),
      { label: 'Up when', value: 'Steam’s directory lists the server’s address and port' },
      { label: 'ICMP latency', value: <span className="muted">Only from an optional echo after a positive listing, if configured; otherwise a dash. Never the Steam API’s response time.</span> },
      { label: 'API, key and options', value: <span className="muted">Hidden from viewers</span> },
    ];
  }
  const size = typeof o.packetSize === 'number' ? o.packetSize : STEAM_PACKET_DEFAULT;
  return [
    listing(m),
    { label: 'Up when', value: 'A valid directory answer lists exactly the resolved address and port' },
    { label: 'Down when', value: <>A valid directory answer doesn’t list it (an empty list included), or the host name doesn’t resolve{o.apiFailureIsDown ? <>, or the Steam API fails (<span className="warn-text">opted in</span>)</> : null}</> },
    {
      label: 'If the Steam API fails',
      value: o.apiFailureIsDown
        ? <span className="warn-text">Down: an unreachable or timed-out API, an error status and an unreadable answer count against the server, though they may not be its fault. A check the node cancels stays pending.</span>
        : 'Pending: an unreachable or timed-out API, an error status and an unreadable answer say nothing about the server. Pending never recovers an outage.',
    },
    {
      label: 'ICMP latency',
      value: o.skipLatency
        ? <span className="muted">Not measured (skipped); shown as a dash</span>
        : <>One echo request ({plural(size, 'byte')} of payload) to the resolved address after a positive listing. Shown only when a reply arrives; otherwise a dash, and the listing still counts. Never the Steam API’s response time.</>,
    },
    { label: 'Steam API', value: <><span className="mono wrap">{o.apiUrl ? (reveal ? o.apiUrl : maskedUrl(o.apiUrl)) : `${STEAM_DEFAULT_API} (default)`}</span>{o.allowInsecureApi ? <> · <span className="warn-text">plain HTTP allowed</span></> : null}{o.apiCa ? ` · system roots + ${plural((o.apiCa.match(/-----BEGIN CERTIFICATE-----/gu) ?? []).length, 'custom CA certificate')}` : ''}</> },
    { label: 'API key', value: o.apiKey ? <span className="muted">set · always hidden</span> : <span className="warn-text">missing</span> },
    { label: 'Sent to the API', value: <span className="muted">Every assigned node makes its own request with the resolved address and port, and the API key as a URL query parameter, to {o.apiUrl ? 'your configured endpoint' : 'Steam'}.</span> },
  ];
}
