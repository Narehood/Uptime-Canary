import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import type {
  FleetTargetReferences, HTTPMonitorOptions, JSONQueryOptions, Monitor, MonitorInput, MonitorTag, MonitorType, MonitorView, NotificationChannelView, RequestCredentials, TLSMonitorOptions,
} from '@uptime-canary/shared';
import { API_BODY_LIMIT, ApiFailure, describeFailure, jsonBytes } from '../api';
import { BROWSER_FIELD_LABEL, browserDraftFrom, browserMasked, buildBrowser, type BrowserDraft, type BrowserField } from '../browser';
import {
  CATEGORY_LABEL, CHECK_TYPES, DNS_RECORDS, SMTP_MODES, TYPED_TARGET_TYPES, checkChangeWords, checkChanges, checkType, cloneInput, inputOf, isServiceType, rangesText,
  statusCodesText, tlsApplies, typeLabel, type CheckCategory, type DnsRecord, type SmtpMode,
} from '../checks';
import { useCanary } from '../context';
import { FleetPlacementFields } from './FleetPlacementFields';
import { managedMonitor } from '../fleet';
import { pushCommands } from '../dashboard';
import { MASK, maskSecretNamed, maskedPath, maskedUrl, plural, secretNamed, shortId, shortRev, span } from '../format';
import { GAMEDIG_FIELD_LABEL, buildGameDig, gamedigDraftFrom, gamedigMasked, type GameDigDraft, type GameDigField } from '../gamedig';
import { GP_FIELD_LABEL, GP_SECTION, buildGlobalping, gpDraftFrom, gpMasked, type GpDraft, type GpField } from '../globalping';
import { groupPath } from '../model';
import {
  DNS_EXPECTED_BYTES, DNS_EXPECTED_MAX, DNS_EXPECTED_TOTAL, DNS_RECORD_SHAPE, descriptionProblem, dnsNeedsServer, extendedRecordProblem, isExtendedRecord, parseSubprotocols,
  subprotocolProblem, subprotocolsText,
} from '../networkOptions';
import { notify } from '../notices';
import { PM2_FIELD_LABEL, buildPm2, pm2DraftFrom, pm2Masked, type Pm2Draft, type Pm2Field } from '../pm2';
import { href, navigate } from '../router';
import { SERVICE_FIELDS, SERVICE_PROTOCOL, buildJsonata, buildService, jsonataDraft, maskService, serviceValues, type JsonataDraft, type ProtocolKey, type Values } from '../serviceSpecs';
import { STEAM_FIELD_LABEL, STEAM_SECTION, buildSteam, steamDraftFrom, steamMasked, type SteamDraft, type SteamField } from '../steam';
import { serviceNameProblem, systemdUnit } from '../systemService';
import { tailscaleTargetProblem } from '../tailscale';
import { Banner, Card, CopyButton, EmptyState, ErrorText, PageHead, Rev, Time } from '../ui/bits';
import {
  CandidateOrder, CheckField, Disclosure, DurationField, ErrorSummary, HeaderEditor, NodePicker, PemField, SecretField, Seg, TextAreaField, TextField, fromDraft, headerRows, toDraft,
  type DurationDraft, type HeaderRow,
} from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';
import { safeColor } from '../ui/tags';
import { BrowserFields } from './BrowserFields';
import { ProviderIcon, providerLabel, recipientCount } from './Channels';
import { GameDigFields } from './GameDigFields';
import { GlobalpingFields } from './GlobalpingFields';
import { Pm2Fields } from './Pm2Fields';
import { JsonataInputs, ServiceFields, openServiceSections } from './ServiceFields';
import { SteamFields } from './SteamFields';
import { TailscaleNotes } from './TailscaleNotes';

type Auth = 'none' | 'basic' | 'bearer' | 'oauth2';
type Proxy = 'none' | 'url' | 'env';
type Family = 'auto' | 'ipv4' | 'ipv6';
type ManualState = '' | 'up' | 'down' | 'pending';

interface RequestDraft { headers: HeaderRow[]; auth: Auth; username: string; password: string; bearer: string; proxy: Proxy; proxyUrl: string }
interface TagDraft { key: number; tagId: string; value: string; isPublic: boolean }

interface Draft {
  name: string;
  /** Private metadata, as typed: never part of the check, never published or sent in notifications. */
  description: string;
  type: MonitorType;
  /** One target per type, so trying another type doesn't lose what was typed. */
  targets: Record<string, string>;
  interval: DurationDraft;
  timeout: DurationDraft;
  enabled: boolean;
  upsideDown: boolean;
  retries: string;
  retryOn: boolean;
  retryInterval: DurationDraft;
  expected: string;
  /** Keeps the monitor's existing representation: legacy `expectedStatusCodes` or `http.statusRanges`. */
  statusMode: 'codes' | 'ranges';
  method: string;
  body: string;
  redirects: string;
  http: RequestDraft;
  ws: RequestDraft;
  /** WebSocket OAuth2 client credentials (protocol 19), separate from the HTTP check's. */
  wsOauth: OAuthDraft;
  /** WebSocket subprotocols to offer, as typed (commas or spaces), and whether the server must choose one. */
  wsProtocols: string; wsRequire: boolean;
  /** TCP only (protocol 19): complete a verified TLS handshake after connecting. */
  tcpTls: boolean;
  keywordOn: boolean; keyword: string; keywordRegex: boolean; keywordNegate: boolean;
  jsonOn: boolean; jsonPath: string; jsonMode: 'equals' | 'exists'; jsonExpected: string; jsonNegate: boolean;
  oauth: OAuthDraft;
  jsonataOn: boolean; jsonata: JsonataDraft;
  tlsServerName: string; tlsCa: string; tlsCert: string; tlsKey: string; tlsMinDays: string; tlsIgnore: boolean;
  dnsRecord: DnsRecord; dnsServer: string; dnsExpected: string;
  smtpMode: SmtpMode;
  pingCount: string; pingSize: string; pingFamily: Family;
  manualState: ManualState; manualMessage: string;
  pushToken: string; pushAllowGet: boolean;
  gp: GpDraft;
  /** The Steam game server: its address and its `steam` options. */
  steam: SteamDraft;
  /** The PM2 process: its exact name and its `pm2` options. */
  pm2: Pm2Draft;
  /** The real-browser `browser` options. The page address is a typed target. */
  browser: BrowserDraft;
  /** The GameDig game server: its address and its `gamedig` options. */
  gamedig: GameDigDraft;
  /** Per service type, so switching types keeps what was typed. */
  service: Partial<Record<MonitorType, Values>>;
  repeatOn: boolean;
  repeat: DurationDraft;
  webhookUrl: string;
  channelIds: string[];
  /**
   * Where `channelIds` came from. 'chosen': stored, copied, imported or picked, and sent as is (a new monitor sends even an
   * empty list, so the node adds no defaults). 'defaults': a new monitor's preselection of this node's enabled default
   * channels, sent as is too. 'node': a new monitor while the channel list isn't known; an empty list is then omitted and
   * the node chooses its default channels itself.
   */
  channelsMode: 'chosen' | 'defaults' | 'node';
  tags: TagDraft[];
  groupId: string;
  nodeIds: string[];
}

const METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'];
let tagKey = 0;

const requestDraft = (o: (RequestCredentials & { oauth2?: unknown }) | undefined): RequestDraft => ({
  headers: headerRows(o?.headers),
  auth: o?.oauth2 ? 'oauth2' : o?.basicAuth ? 'basic' : o?.bearerToken ? 'bearer' : 'none',
  username: o?.basicAuth?.username ?? '', password: o?.basicAuth?.password ?? '', bearer: o?.bearerToken ?? '',
  proxy: o?.proxyUrl ? 'url' : o?.useEnvironmentProxy ? 'env' : 'none', proxyUrl: o?.proxyUrl ?? '',
});

const protocolOf = (type: MonitorType): ProtocolKey | undefined => SERVICE_PROTOCOL[type];

function draftFrom(monitor: Monitor | undefined, defaultNodes: string[]): Draft {
  const h = monitor?.http;
  const t = monitor?.tls;
  const ranges = Boolean(h?.statusRanges?.length);
  const protocol = monitor ? protocolOf(monitor.type) : undefined;
  return {
    name: monitor?.name ?? '',
    description: typeof monitor?.description === 'string' ? monitor.description : '',
    type: monitor?.type ?? 'http',
    targets: monitor && TYPED_TARGET_TYPES.has(monitor.type) ? { [monitor.type]: monitor.target } : {},
    interval: toDraft(monitor?.intervalMs ?? 60_000),
    timeout: toDraft(monitor?.timeoutMs ?? 10_000),
    enabled: monitor?.enabled ?? true,
    upsideDown: Boolean(monitor?.upsideDown),
    retries: monitor?.retries ? String(monitor.retries) : '0',
    retryOn: monitor?.retryIntervalMs !== undefined && monitor.retryIntervalMs !== null,
    retryInterval: toDraft(monitor?.retryIntervalMs ?? 20_000),
    expected: (ranges ? rangesText(h!.statusRanges) : statusCodesText(monitor?.expectedStatusCodes)).replace(/–/gu, '-'),
    statusMode: ranges ? 'ranges' : 'codes',
    method: h?.method || 'GET',
    body: h?.body ?? '',
    redirects: h?.maxRedirects === undefined ? '' : String(h.maxRedirects),
    http: requestDraft(h),
    ws: requestDraft(monitor?.websocket),
    wsOauth: oauthDraft(monitor?.websocket?.oauth2),
    wsProtocols: subprotocolsText(monitor?.websocket?.subprotocols), wsRequire: Boolean(monitor?.websocket?.requireSubprotocol),
    tcpTls: monitor?.type === 'tcp' && Boolean(monitor.tls),
    keywordOn: Boolean(h?.keyword), keyword: h?.keyword?.value ?? '', keywordRegex: Boolean(h?.keyword?.regex), keywordNegate: Boolean(h?.keyword?.negate),
    oauth: oauthDraft(h?.oauth2),
    jsonataOn: Boolean(h?.jsonata), jsonata: jsonataDraft(h?.jsonata),
    jsonOn: Boolean(h?.json), jsonPath: h?.json?.path ?? '$.', jsonMode: h?.json?.existsOnly ? 'exists' : 'equals', jsonExpected: h?.json?.expected ?? '', jsonNegate: Boolean(h?.json?.negate),
    tlsServerName: t?.serverName ?? '', tlsCa: t?.caPem ?? '', tlsCert: t?.clientCertPem ?? '', tlsKey: t?.clientKeyPem ?? '',
    tlsMinDays: t?.minValidityDays ? String(t.minValidityDays) : '', tlsIgnore: Boolean(t?.ignoreVerification),
    dnsRecord: (monitor?.dns?.recordType ?? 'A') as DnsRecord, dnsServer: monitor?.dns?.server ?? '', dnsExpected: (monitor?.dns?.expected ?? []).join('\n'),
    smtpMode: (monitor?.smtp?.tlsMode ?? 'plain') as SmtpMode,
    pingCount: monitor?.ping?.count ? String(monitor.ping.count) : '', pingSize: monitor?.ping?.size !== undefined ? String(monitor.ping.size) : '',
    pingFamily: (monitor?.ping?.family ?? 'auto') as Family,
    manualState: (monitor?.manual?.state ?? '') as ManualState, manualMessage: monitor?.manual?.message ?? '',
    pushToken: monitor?.push?.token ?? '', pushAllowGet: Boolean(monitor?.push?.allowGet),
    gp: gpDraftFrom(monitor?.type === 'globalping' ? monitor.globalping : undefined, monitor?.type === 'globalping' ? monitor.target : ''),
    steam: steamDraftFrom(monitor?.type === 'steam' ? monitor.steam : undefined, monitor?.type === 'steam' ? monitor.target : ''),
    pm2: pm2DraftFrom(monitor?.type === 'pm2' ? monitor.pm2 : undefined, monitor?.type === 'pm2' ? monitor.target : ''),
    browser: browserDraftFrom(monitor?.type === 'real-browser' ? monitor.browser : undefined),
    gamedig: gamedigDraftFrom(monitor?.type === 'gamedig' ? monitor.gamedig : undefined, monitor?.type === 'gamedig' ? monitor.target : ''),
    service: monitor && protocol ? { [monitor.type]: serviceValues(protocol, monitor.service?.[protocol]) } : {},
    repeatOn: Boolean(monitor?.repeatAfterMs),
    repeat: toDraft(monitor?.repeatAfterMs || 900_000),
    webhookUrl: monitor?.webhookUrl ?? '',
    channelIds: [...(monitor?.notificationIds ?? [])],
    channelsMode: monitor ? 'chosen' : 'node',
    tags: (monitor?.tags ?? []).map(tag => ({ key: ++tagKey, tagId: tag.tagId, value: tag.value ?? '', isPublic: Boolean(tag.public) })),
    groupId: monitor?.groupId ?? '',
    nodeIds: monitor ? [...monitor.nodeIds] : defaultNodes,
  };
}

// ---------- validation: mirrors the node's rules without being stricter, so a no-op save is never blocked

const hasControl = (text: string): boolean => /[\x00-\x1f\x7f]/u.test(text);
const bytes = (text: string): number => new TextEncoder().encode(text).length;
const TOKEN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/u;
const MANAGED_HEADERS = new Set(['host', 'connection', 'content-length', 'transfer-encoding', 'upgrade', 'proxy-authorization', 'proxy-connection', 'trailer', 'te']);
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/u;
const isIPv6 = (text: string): boolean => text.includes(':') && /^[0-9a-fA-F:.]+$/u.test(text);
const dnsName = (text: string): boolean => {
  const name = text.replace(/\.$/u, '');
  return name.length > 0 && name.length <= 253 && name.split('.').every(label => /^[A-Za-z0-9_](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$/u.test(label));
};

export function httpUrlProblem(value: string, field: string, schemes: string[] = ['http:', 'https:']): string | undefined {
  const text = value.trim();
  if (!text) return `Enter the ${field}.`;
  if (text.length > 2048 || hasControl(text)) return `The ${field} is too long or contains control characters.`;
  let url: URL;
  try { url = new URL(text); } catch { return `Enter a complete address starting with ${schemes.map(s => `${s}//`).join(' or ')}.`; }
  if (!schemes.includes(url.protocol)) return `Use a ${schemes.map(s => `${s}//`).join(' or ')} address.`;
  if (!url.hostname) return 'The address needs a host name.';
  if (url.username || url.password) return 'Remove credentials from the address (user:password@). Use the authentication settings instead.';
  if (url.hash) return 'Remove the #fragment from the address.';
  return undefined;
}

function pingProblem(value: string): string | undefined {
  const text = value.trim();
  if (!text) return 'Enter a host name or IP address.';
  if (/^\d{1,3}(\.\d{1,3}){3}$/u.test(text)) return IPV4.test(text) ? undefined : 'That IPv4 address is out of range.';
  if (text.includes(':')) return isIPv6(text) ? undefined : 'Enter an IPv6 address without a zone or brackets.';
  if (text.length > 253 || !text.replace(/\.$/u, '').split('.').every(label => /^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/u.test(label))) {
    return 'Use a host name like gateway.example.net or an IP address, without spaces or options.';
  }
  return undefined;
}

/** host:port or [IPv6]:port, port 1–65535. `literal` requires an IP (for DNS resolvers). */
function hostPortProblem(value: string, literal = false): string | undefined {
  const text = value.trim();
  if (!text) return literal ? 'Enter an IP address and port, like 1.1.1.1:53.' : 'Enter a host and port, like service.example.com:443.';
  const match = /^\[([^\]]+)\]:(\d{1,5})$/u.exec(text) ?? /^([^:[\]\s]+):(\d{1,5})$/u.exec(text);
  if (!match) return literal ? 'Use IP:port, or [IPv6]:port for IPv6.' : 'Use host:port, or [IPv6]:port for IPv6 addresses.';
  const [, host, port] = match;
  if (Number(port) < 1 || Number(port) > 65535) return 'The port must be between 1 and 65535.';
  const ip = IPV4.test(host!) || (text.startsWith('[') && isIPv6(host!));
  if (literal ? !ip : !ip && !dnsName(host!)) return literal ? 'The resolver must be an IP address, not a host name.' : 'The host isn’t a valid name or IP address.';
  return undefined;
}

/** Accepts "200-299, 301 302" as ranges. */
function parseRanges(text: string): { ranges?: { min: number; max: number }[]; codes?: number; error?: string } {
  const tokens = text.split(/[\s,]+/u).map(t => t.trim()).filter(Boolean);
  if (!tokens.length) return {};
  const ranges: { min: number; max: number }[] = [];
  const unique = new Set<number>();
  for (const token of tokens) {
    const range = /^(\d{3})\s*[-–]\s*(\d{3})$/u.exec(token);
    const single = /^\d{3}$/u.test(token) ? Number(token) : null;
    const [min, max] = range ? [Number(range[1]), Number(range[2])] : single !== null ? [single, single] : [Number.NaN, Number.NaN];
    if (!Number.isInteger(min) || min < 100 || max > 599 || min > max) return { error: `“${token}” isn’t a status code or range between 100 and 599.` };
    ranges.push({ min, max });
    for (let code = min; code <= max; code++) unique.add(code);
  }
  if (ranges.length > 100) return { error: 'List at most 100 codes or ranges.' };
  return { ranges, codes: unique.size };
}

/** The node's bounded selector: $, .name, [0], ["quoted key"]; no wildcards, filters or scripts. */
function jsonPathProblem(path: string): string | undefined {
  if (!path.startsWith('$')) return 'Start the path with $, like $.status or $.items[0].state.';
  if (bytes(path) > 2048) return 'Use a path of at most 2048 bytes.';
  const steps = path.slice(1).match(/\.[A-Za-z_][A-Za-z0-9_]*|\[(?:0|[1-9]\d{0,6})\]|\["(?:[^"\\]|\\.)*"\]/gu) ?? [];
  if (steps.join('') !== path.slice(1)) return 'Use only .name, [index] and ["key"] steps. Wildcards, filters and expressions aren’t supported.';
  if (steps.length > 64) return 'Use at most 64 steps.';
  return undefined;
}

function jsonValueProblem(text: string): string | undefined {
  if (!text.trim()) return 'Enter the expected JSON value, like "ok", true or 3.';
  try { JSON.parse(text); return undefined; }
  catch { return /^[A-Za-z][\w\s-]*$/u.test(text.trim()) ? `Text needs double quotes: "${text.trim()}".` : 'That isn’t valid JSON. Text needs double quotes; numbers, true, false and null don’t.'; }
}

type Field =
  | 'name' | 'target' | 'interval' | 'timeout' | 'retries' | 'retryInterval' | 'expected' | 'method' | 'headers' | 'body' | 'redirects' | 'auth' | 'proxy'
  | 'keyword' | 'jsonPath' | 'jsonExpected' | 'jsonata' | 'oauth' | 'wsHeaders' | 'wsAuth' | 'wsProxy' | 'wsOauth' | 'wsProtocols' | 'description'
  | 'tlsServerName' | 'tlsCa' | 'tlsCert' | 'tlsKey' | 'tlsMinDays' | 'dnsServer' | 'dnsExpected' | 'pingCount' | 'pingSize' | 'manualMessage'
  | 'repeat' | 'webhook' | 'channels' | 'tags' | 'nodes' | 'size' | 'pushToken' | GpField | SteamField | Pm2Field | BrowserField | GameDigField;

const FIELD_LABEL: Record<Field, string> = {
  name: 'Name', target: 'Target', interval: 'Interval', timeout: 'Timeout', retries: 'Retries', retryInterval: 'Retry interval', expected: 'Accepted status', method: 'Method',
  headers: 'Headers', body: 'Request body', redirects: 'Redirects', auth: 'Authentication', proxy: 'Proxy', keyword: 'Keyword', jsonPath: 'JSON path',
  jsonExpected: 'Expected JSON value', jsonata: 'JSONata condition', oauth: 'OAuth2', wsHeaders: 'Headers', wsAuth: 'Authentication', wsProxy: 'Proxy', wsOauth: 'OAuth2',
  wsProtocols: 'Subprotocols', description: 'Description', tlsServerName: 'TLS server name',
  tlsCa: 'Trusted CA certificates', tlsCert: 'Client certificate', tlsKey: 'Client key', tlsMinDays: 'Minimum validity', dnsServer: 'Resolver',
  dnsExpected: 'Expected records', pingCount: 'Packets', pingSize: 'Payload size', manualMessage: 'Status message', repeat: 'Repeat', webhook: 'Webhook URL',
  channels: 'Notification channels', tags: 'Tags', nodes: 'Run on', size: 'Size', pushToken: 'Push token',
  ...GP_FIELD_LABEL,
  ...STEAM_FIELD_LABEL,
  ...PM2_FIELD_LABEL,
  ...BROWSER_FIELD_LABEL,
  ...GAMEDIG_FIELD_LABEL,
};

/** Which disclosure holds a field, so a failed save opens the sections that need attention. */
const SECTION_OF: Partial<Record<Field, string>> = {
  headers: 'request', body: 'request', redirects: 'request', auth: 'access', proxy: 'access', keyword: 'response', jsonPath: 'response', jsonata: 'response', oauth: 'access', jsonExpected: 'response',
  wsHeaders: 'ws', wsAuth: 'ws', wsProxy: 'ws', wsOauth: 'ws', tlsServerName: 'tls', tlsCa: 'tls', tlsCert: 'tls', tlsKey: 'tls', tlsMinDays: 'tls', webhook: 'legacy',
  ...GP_SECTION,
  ...STEAM_SECTION,
};

type Errors = Partial<Record<Field, string>>;

function headersOf(rows: HeaderRow[], errors: Errors, field: Field, websocket: boolean): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  const seen = new Set<string>();
  for (const row of rows) {
    const name = row.name.trim();
    if (!name && !row.value) continue;
    const key = name.toLowerCase();
    if (!name || !TOKEN.test(name)) { errors[field] = `“${name || '(empty)'}” isn’t a valid header name.`; continue; }
    if (seen.has(key)) { errors[field] = `${name} appears twice.`; continue; }
    if (MANAGED_HEADERS.has(key)) { errors[field] = `${name} is managed by the connection and can’t be set.`; continue; }
    if (websocket && key.startsWith('sec-websocket-')) { errors[field] = `${name} is part of the WebSocket handshake and can’t be set.`; continue; }
    if (hasControl(row.value) || row.value.length > 8192) { errors[field] = `The value for ${name} is too long or has control characters.`; continue; }
    seen.add(key);
    out[name] = row.value;
  }
  if (Object.keys(out).length > 100) errors[field] = 'Use at most 100 headers.';
  return Object.keys(out).length ? out : undefined;
}

/** Applies a request draft to an options object, keeping keys the dashboard doesn't model. */
function applyRequest<T extends RequestCredentials>(target: T, d: RequestDraft, errors: Errors, fields: { headers: Field; auth: Field; proxy: Field }, websocket: boolean): T {
  const headers = headersOf(d.headers, errors, fields.headers, websocket);
  if (headers) target.headers = headers; else delete target.headers;
  delete target.basicAuth; delete target.bearerToken;
  if (d.auth === 'basic') {
    if (d.username.includes(':') || hasControl(d.username) || hasControl(d.password)) errors[fields.auth] = 'The user name can’t contain a colon, and neither field can contain control characters.';
    target.basicAuth = { username: d.username, password: d.password };
  } else if (d.auth === 'bearer') {
    if (!d.bearer.trim()) errors[fields.auth] = 'Enter the bearer token, or choose no authentication.';
    else if (/\s/u.test(d.bearer) || hasControl(d.bearer)) errors[fields.auth] = 'Bearer tokens can’t contain spaces.';
    target.bearerToken = d.bearer.trim();
  }
  if (d.auth !== 'none' && headers && Object.keys(headers).some(name => name.toLowerCase() === 'authorization')) {
    errors[fields.auth] = 'Remove the Authorization header or choose no authentication here: only one source can be used.';
  }
  delete target.proxyUrl; delete target.useEnvironmentProxy;
  if (d.proxy === 'url') {
    const problem = httpUrlProblem(d.proxyUrl, 'proxy URL');
    if (problem) errors[fields.proxy] = problem;
    else {
      const url = new URL(d.proxyUrl.trim());
      if (url.search || (url.pathname !== '/' && url.pathname !== '')) errors[fields.proxy] = 'Use only the proxy’s scheme, host and port, without a path or query.';
    }
    target.proxyUrl = d.proxyUrl.trim();
  } else if (d.proxy === 'env') target.useEnvironmentProxy = true;
  return target;
}

const clone = <T,>(value: T | undefined): T | undefined => value === undefined || value === null ? undefined : structuredClone(value);
const emptyToUndefined = <T extends object>(value: T): T | undefined => Object.keys(value).length ? value : undefined;

interface Groups {
  expectedStatusCodes?: number[];
  http?: HTTPMonitorOptions;
  tls?: TLSMonitorOptions;
  dns?: MonitorInput['dns'];
  websocket?: MonitorInput['websocket'];
  smtp?: MonitorInput['smtp'];
  ping?: MonitorInput['ping'];
  manual?: MonitorInput['manual'];
  service?: MonitorInput['service'];
  push?: MonitorInput['push'];
  globalping?: MonitorInput['globalping'];
  steam?: MonitorInput['steam'];
  pm2?: MonitorInput['pm2'];
  browser?: MonitorInput['browser'];
  gamedig?: MonitorInput['gamedig'];
}
const GROUP_KEYS = ['expectedStatusCodes', 'http', 'tls', 'dns', 'websocket', 'smtp', 'ping', 'manual', 'service', 'push', 'globalping', 'steam', 'pm2', 'browser', 'gamedig'] as const;

/** Builds every option object for the draft's type from the draft alone, starting from the original's objects. */
function buildGroups(draft: Draft, original: Monitor | undefined, errors: Errors, serviceErrors: Record<string, string>): Groups {
  const same = original?.type === draft.type;
  const target = (draft.targets[draft.type] ?? '').trim();
  const groups: Groups = {};
  const type = draft.type;
  if (type === 'http') {
    const h: HTTPMonitorOptions = clone(same ? original?.http : undefined) ?? {};
    const parsed = parseRanges(draft.expected);
    if (parsed.error) errors.expected = parsed.error;
    delete h.statusRanges;
    if (parsed.ranges) {
      if (draft.statusMode === 'codes' && parsed.codes! <= 100) {
        const codes = new Set<number>();
        for (const range of parsed.ranges) for (let code = range.min; code <= range.max; code++) codes.add(code);
        groups.expectedStatusCodes = [...codes].sort((a, b) => a - b);
      } else h.statusRanges = parsed.ranges;
    }
    const method = draft.method.trim();
    if (!method || method.length > 32 || !TOKEN.test(method) || method.toUpperCase() === 'CONNECT') errors.method = 'Use an HTTP method name like GET or POST (not CONNECT).';
    if (method === 'GET') delete h.method; else h.method = method;
    if (draft.body) h.body = draft.body; else delete h.body;
    if (draft.redirects.trim() === '') delete h.maxRedirects;
    else {
      const n = Number(draft.redirects);
      if (!Number.isInteger(n) || n < 0 || n > 10) errors.redirects = 'Use a whole number from 0 to 10, or leave empty for the default of 5.';
      h.maxRedirects = n;
    }
    applyRequest(h, draft.http, errors, { headers: 'headers', auth: 'auth', proxy: 'proxy' }, false);
    if (draft.http.auth === 'oauth2') h.oauth2 = applyOAuth(h.oauth2, draft.oauth, target, errors, 'oauth');
    else delete h.oauth2;
    if (draft.jsonataOn) {
      const built = buildJsonata(h.jsonata, draft.jsonata);
      if (!draft.jsonata.expression) errors.jsonata = 'Enter a JSONata expression, or turn this condition off.';
      else if (built.error) errors.jsonata = built.error;
      if (built.value) h.jsonata = built.value as unknown as JSONQueryOptions;
    } else delete h.jsonata;
    if (draft.keywordOn) {
      const n = bytes(draft.keyword);
      if (!draft.keyword) errors.keyword = 'Enter the text or pattern to look for, or turn the keyword check off.';
      else if (n > 16_384 || (draft.keywordRegex && n > 1024)) errors.keyword = draft.keywordRegex ? 'Use a pattern of at most 1 KiB.' : 'Use at most 16 KiB.';
      h.keyword = { value: draft.keyword, ...(draft.keywordRegex ? { regex: true } : {}), ...(draft.keywordNegate ? { negate: true } : {}) };
    } else delete h.keyword;
    if (draft.jsonOn) {
      const problem = jsonPathProblem(draft.jsonPath);
      if (problem) errors.jsonPath = problem;
      if (draft.jsonMode === 'equals') {
        const valueProblem = jsonValueProblem(draft.jsonExpected);
        if (valueProblem) errors.jsonExpected = valueProblem;
      }
      h.json = { path: draft.jsonPath, ...(draft.jsonMode === 'equals' ? { expected: draft.jsonExpected.trim() } : { existsOnly: true }), ...(draft.jsonNegate ? { negate: true } : {}) };
    } else delete h.json;
    if (method.toUpperCase() === 'HEAD' && (draft.keywordOn || draft.jsonOn || draft.jsonataOn)) errors.method = 'HEAD responses have no body, so keyword, JSON and JSONata checks need another method.';
    groups.http = emptyToUndefined(h);
  }
  if (type === 'websocket') {
    const w: NonNullable<MonitorInput['websocket']> = clone(same ? original?.websocket : undefined) ?? {};
    applyRequest(w, draft.ws, errors, { headers: 'wsHeaders', auth: 'wsAuth', proxy: 'wsProxy' }, true);
    // OAuth2 (protocol 19) is one more authentication choice, exclusive with the others, as for HTTP.
    if (draft.ws.auth === 'oauth2') w.oauth2 = applyOAuth(w.oauth2, draft.wsOauth, target, errors, 'wsOauth');
    else delete w.oauth2;
    const protocols = parseSubprotocols(draft.wsProtocols);
    const protocolProblem = subprotocolProblem(protocols, draft.wsRequire);
    if (protocolProblem) errors.wsProtocols = protocolProblem;
    if (protocols.length) w.subprotocols = protocols; else delete w.subprotocols;
    if (draft.wsRequire) w.requireSubprotocol = true; else delete w.requireSubprotocol;
    groups.websocket = emptyToUndefined(w);
  }
  if (type === 'dns') {
    const d: NonNullable<MonitorInput['dns']> = clone(same ? original?.dns : undefined) ?? {};
    d.recordType = draft.dnsRecord;
    if (draft.dnsServer.trim()) {
      const problem = hostPortProblem(draft.dnsServer, true);
      if (problem) errors.dnsServer = problem;
      d.server = draft.dnsServer.trim();
    } else {
      delete d.server;
      if (dnsNeedsServer(draft.dnsRecord)) errors.dnsServer = `${draft.dnsRecord} checks ask one resolver directly, so enter its IP address and port, like 1.1.1.1:53.`;
    }
    const expected = draft.dnsExpected.split('\n').map(line => line.trim()).filter(Boolean);
    if (expected.length > DNS_EXPECTED_MAX) errors.dnsExpected = 'List at most 128 records.';
    const record = draft.dnsRecord;
    if (isExtendedRecord(record)) {
      // The node's own canonical rules for SRV, SOA and CAA, with its per-record and total byte limits.
      const oversized = expected.find(value => bytes(value) > DNS_EXPECTED_BYTES);
      const problem = expected.map(value => extendedRecordProblem(record, value)).find(Boolean);
      if (oversized) errors.dnsExpected = 'Each expected record is at most 8,192 bytes.';
      else if (expected.reduce((n, value) => n + bytes(value), 0) > DNS_EXPECTED_TOTAL) errors.dnsExpected = 'Expected records are at most 64 KiB together.';
      else if (problem) errors.dnsExpected = problem;
    } else {
      const bad = expected.find(value => record === 'A' ? !IPV4.test(value) : record === 'AAAA' ? !isIPv6(value) : record === 'MX' ? !/^\d{1,5}\s+\S+$/u.test(value) : false);
      if (bad) errors.dnsExpected = `“${bad}” doesn’t look like ${record === 'MX' ? 'an MX record (priority and host, like 10 mail.example.com)' : `an ${record} address`}.`;
    }
    if (expected.length) d.expected = expected; else delete d.expected;
    groups.dns = d;
  }
  if (type === 'smtp') {
    const s: NonNullable<MonitorInput['smtp']> = clone(same ? original?.smtp : undefined) ?? {};
    if (draft.smtpMode === 'plain') delete s.tlsMode; else s.tlsMode = draft.smtpMode;
    groups.smtp = emptyToUndefined(s);
  }
  if (type === 'ping') {
    const p: NonNullable<MonitorInput['ping']> = clone(same ? original?.ping : undefined) ?? {};
    if (draft.pingCount.trim()) {
      const n = Number(draft.pingCount);
      if (!Number.isInteger(n) || n < 1 || n > 10) errors.pingCount = 'Send 1 to 10 packets.';
      p.count = n;
    } else delete p.count;
    if (draft.pingSize.trim()) {
      const n = Number(draft.pingSize);
      if (!Number.isInteger(n) || n < 0 || n > 65_500) errors.pingSize = 'Use 0 to 65,500 bytes.';
      p.size = n;
    } else delete p.size;
    if (draft.pingFamily === 'auto') { if (p.family !== 'auto') delete p.family; } else p.family = draft.pingFamily;
    groups.ping = emptyToUndefined(p);
  }
  if (type === 'manual') {
    const m: NonNullable<MonitorInput['manual']> = clone(same ? original?.manual : undefined) ?? {};
    if (draft.manualState) m.state = draft.manualState; else delete m.state;
    const message = draft.manualMessage.trim();
    if (message) {
      if (bytes(message) > 256 || hasControl(message)) errors.manualMessage = 'Use at most 256 bytes on one line.';
      m.message = message;
    } else delete m.message;
    groups.manual = emptyToUndefined(m);
  }
  if (type === 'push') {
    const p = (clone(same ? original?.push : undefined) ?? {}) as NonNullable<MonitorInput['push']>;
    if (!/^[0-9a-f]{64}$/u.test(draft.pushToken)) errors.pushToken = 'Generate a token: 64 lowercase hexadecimal characters.';
    p.token = draft.pushToken;
    if (draft.pushAllowGet) p.allowGet = true; else delete p.allowGet;
    groups.push = p;
  }
  if (type === 'globalping') groups.globalping = buildGlobalping(same ? original?.globalping : undefined, draft.gp, errors).value;
  if (type === 'steam') groups.steam = buildSteam(same ? original?.steam : undefined, draft.steam, errors).value;
  if (type === 'pm2') groups.pm2 = buildPm2(same ? original?.pm2 : undefined, draft.pm2, errors).value;
  if (type === 'real-browser') groups.browser = buildBrowser(same ? original?.browser : undefined, draft.browser, fromDraft(draft.timeout), errors).value;
  if (type === 'gamedig') groups.gamedig = buildGameDig(same ? original?.gamedig : undefined, draft.gamedig, errors).value;
  const protocol = protocolOf(type);
  if (protocol) {
    const stored = same ? original?.service?.[protocol] : undefined;
    const values = draft.service[type] ?? serviceValues(protocol, undefined);
    const built = buildService(protocol, values, stored);
    Object.assign(serviceErrors, built.errors);
    // Unknown siblings of the protocol object (from a newer node) survive an edit of the same type.
    const outer = (clone(same ? original?.service : undefined) ?? {}) as Record<string, unknown>;
    outer[protocol] = built.value;
    groups.service = outer as MonitorInput['service'];
  }
  if (tlsApplies(type, target, draft.smtpMode, draft.tcpTls)) {
    const t: TLSMonitorOptions = clone(same ? original?.tls : undefined) ?? {};
    const name = draft.tlsServerName.trim();
    if (name && !IPV4.test(name) && !isIPv6(name) && !dnsName(name)) errors.tlsServerName = 'Use a host name or IP address.';
    if (name) t.serverName = name; else delete t.serverName;
    const pem = (value: string, field: Field, kind: 'certificate' | 'key'): string | undefined => {
      const text = value.trim();
      if (!text) return undefined;
      if (kind === 'certificate' ? !/-----BEGIN [A-Z0-9 ]*CERTIFICATE-----/u.test(text) : !/-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/u.test(text)) {
        errors[field] = kind === 'certificate' ? 'Paste PEM certificates (-----BEGIN CERTIFICATE-----).' : 'Paste a PEM private key (-----BEGIN … PRIVATE KEY-----).';
      }
      return value;
    };
    const ca = pem(draft.tlsCa, 'tlsCa', 'certificate');
    const cert = pem(draft.tlsCert, 'tlsCert', 'certificate');
    const key = pem(draft.tlsKey, 'tlsKey', 'key');
    if (Boolean(cert) !== Boolean(key)) errors[cert ? 'tlsKey' : 'tlsCert'] = 'Mutual TLS needs both the client certificate and its private key.';
    if (ca) t.caPem = ca; else delete t.caPem;
    if (cert) t.clientCertPem = cert; else delete t.clientCertPem;
    if (key) t.clientKeyPem = key; else delete t.clientKeyPem;
    if (draft.tlsMinDays.trim()) {
      const days = Number(draft.tlsMinDays);
      if (!Number.isFinite(days) || days < 0 || days > 3650) errors.tlsMinDays = 'Use a number of days from 0 to 3650.';
      if (days > 0) t.minValidityDays = days; else delete t.minValidityDays;
    } else delete t.minValidityDays;
    if (draft.tlsIgnore) t.ignoreVerification = true; else delete t.ignoreVerification;
    // On a TCP check the TLS object itself turns the handshake on, so even an empty one is sent: `"tls": {}` is TLS with
    // the defaults, and no object is a plain connection.
    groups.tls = type === 'tcp' ? t : emptyToUndefined(t);
  }
  return groups;
}

const TLS_DRAFT_KEYS = ['tlsServerName', 'tlsCa', 'tlsCert', 'tlsKey', 'tlsMinDays'] as const;
const hasTlsDraft = (draft: Draft): boolean => TLS_DRAFT_KEYS.some(key => draft[key].trim()) || draft.tlsIgnore;

interface Built { input?: MonitorInput; errors: Errors; serviceErrors: Record<string, string> }

function build(draft: Draft, original: Monitor | undefined, initial: Draft | undefined): Built {
  const errors: Errors = {};
  const serviceErrors: Record<string, string> = {};
  const name = draft.name.trim();
  if (!name) errors.name = 'Enter a name.';
  else if (name.length > 128 || hasControl(name)) errors.name = 'Use at most 128 characters, without control characters.';
  const descriptionError = descriptionProblem(draft.description);
  if (descriptionError) errors.description = descriptionError;
  const type = draft.type;
  const typedTarget = TYPED_TARGET_TYPES.has(type);
  const typed = (draft.targets[type] ?? '').trim();
  if (typedTarget) {
    const targetProblem = type === 'http' ? httpUrlProblem(typed, 'URL')
      // The node reads a browser monitor's whole body strictly: an unpaired surrogate anywhere in it is refused.
      : type === 'real-browser' ? (/\p{Cs}/u.test(typed) ? 'The address holds a broken character (an unpaired surrogate). Type it again instead of pasting it.' : httpUrlProblem(typed, 'page URL'))
      : type === 'websocket' ? httpUrlProblem(typed, 'URL', ['ws:', 'wss:'])
      : type === 'system-service' ? serviceNameProblem(typed)
      : type === 'tailscale-ping' ? tailscaleTargetProblem(typed)
      : type === 'ping' ? pingProblem(typed)
      : type === 'dns' ? (draft.dnsRecord === 'PTR'
        ? (IPV4.test(typed) || isIPv6(typed) ? undefined : 'For PTR records, enter the IP address to look up.')
        : !typed ? 'Enter the name to look up.' : dnsName(typed) ? undefined : 'Enter a domain name like example.com.')
      : hostPortProblem(typed);
    if (targetProblem) errors.target = targetProblem;
  }
  const interval = fromDraft(draft.interval);
  const timeout = fromDraft(draft.timeout);
  if (interval === null || interval < 100 || interval > 86_400_000) errors.interval = 'Use an interval from 100 ms to 24 h.';
  if (timeout === null || timeout < 25 || timeout > 120_000) errors.timeout = 'Use a timeout from 25 ms to 120 s.';
  else if (interval !== null && timeout > interval) errors.timeout = 'The timeout can’t be longer than the interval.';
  const retries = draft.retries.trim() === '' ? 0 : Number(draft.retries);
  if (!Number.isInteger(retries) || retries < 0 || retries > 100) errors.retries = 'Use a whole number from 0 to 100.';
  const retryInterval = draft.retryOn ? fromDraft(draft.retryInterval) : null;
  if (draft.retryOn) {
    if (retryInterval === null || retryInterval < 100 || retryInterval > 86_400_000) errors.retryInterval = 'Use 100 ms to 24 h, or turn this off to use the normal interval.';
    else if (timeout !== null && timeout > retryInterval) errors.retryInterval = 'The timeout can’t be longer than the retry interval.';
  }
  const repeat = draft.repeatOn ? fromDraft(draft.repeat) : 0;
  if (draft.repeatOn && (repeat === null || repeat < 1000 || repeat > 604_800_000)) errors.repeat = 'Repeat from every 1 s to every 7 days, or turn repeats off.';
  const webhook = draft.webhookUrl.trim();
  if (webhook) { const problem = httpUrlProblem(webhook, 'webhook URL'); if (problem) errors.webhook = problem; }
  if (draft.channelIds.length > 16) errors.channels = 'Choose at most 16 channels.';
  const tagIds = draft.tags.map(tag => tag.tagId).filter(Boolean);
  if (draft.tags.some(tag => !tag.tagId)) errors.tags = 'Choose a tag for every row, or remove the empty row.';
  else if (new Set(tagIds).size !== tagIds.length) errors.tags = 'Each tag can be assigned once. Put several values in one tag value instead.';
  else if (draft.tags.length > 32) errors.tags = 'Assign at most 32 tags.';
  else if (draft.tags.some(tag => bytes(tag.value.trim()) > 128 || hasControl(tag.value))) errors.tags = 'Tag values are at most 128 bytes on one line.';
  if (!draft.nodeIds.length) errors.nodes = 'Choose at least one node to run this check.';
  else if (draft.nodeIds.length > 32) errors.nodes = 'Choose at most 32 nodes.';

  const groups = buildGroups(draft, original, errors, serviceErrors);
  // An unchanged option object is sent exactly as the node stored it, so saving other edits never changes what's
  // checked (which would close an open incident as a new check generation).
  if (original && initial && original.type === type) {
    const before = buildGroups(initial, original, {}, {});
    for (const key of GROUP_KEYS) {
      if (JSON.stringify(groups[key]) === JSON.stringify(before[key])) (groups as Record<string, unknown>)[key] = clone(original[key]);
    }
  }
  if (Object.keys(errors).length || Object.keys(serviceErrors).length) return { errors, serviceErrors };

  const input = (original ? inputOf(original) : {}) as MonitorInput;
  for (const key of ['expectedStatusCodes', 'webhookUrl', 'repeatAfterMs', 'groupId', 'http', 'tls', 'dns', 'websocket', 'smtp', 'upsideDown', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2', 'browser', 'gamedig', 'retries', 'retryIntervalMs', 'notificationIds', 'tags', 'description'] as const) delete input[key];
  // The description is metadata: sent exactly as typed (an unchanged one byte for byte), omitted when empty as Go encodes it.
  if (draft.description) input.description = draft.description;
  // A PM2 process name is compared exactly, so it is sent as typed, never trimmed.
  const target = typedTarget ? typed : type === 'globalping' ? draft.gp.targets[draft.gp.mode].trim() : type === 'steam' ? draft.steam.target.trim() : type === 'pm2' ? draft.pm2.target : type === 'gamedig' ? draft.gamedig.target.trim()
    : type === 'manual' ? 'manual' : type === 'push' ? 'push' : original?.type === type ? original.target : typeLabel(type);
  // Repeat while down: the node reads a missing value like 0 (off), but stores and returns the two apart. A monitor stored
  // without one (made through the API, and a copy of it) sends none while repeats stay off, so an edit or a copy keeps the
  // stored shape. Every other case sends the value: 0 when off, as a new monitor does.
  const repeatAfterMs = original && !draft.repeatOn && typeof original.repeatAfterMs !== 'number' ? undefined : repeat ?? 0;
  Object.assign(input, { name, type, target, intervalMs: interval!, timeoutMs: timeout!, nodeIds: [...draft.nodeIds].sort(), enabled: draft.enabled, ...(repeatAfterMs === undefined ? {} : { repeatAfterMs }) });
  for (const key of GROUP_KEYS) if (groups[key] !== undefined) (input as unknown as Record<string, unknown>)[key] = groups[key];
  if (retries) input.retries = retries;
  if (draft.retryOn && retryInterval !== null) input.retryIntervalMs = retryInterval;
  if (webhook) input.webhookUrl = webhook;
  // A create sends its channel list even when empty, so the node adds no default channels the form didn't show (and a
  // copy keeps the source's empty list). Only a new monitor whose channel list isn't known yet leaves the choice to the
  // node. An edit is unchanged: the node fills defaults only for creates.
  const creating = !original?.id;
  if (draft.channelIds.length || (creating && draft.channelsMode !== 'node')) input.notificationIds = [...draft.channelIds];
  if (draft.tags.length) {
    input.tags = draft.tags.map(tag => {
      const out: MonitorTag = { tagId: tag.tagId };
      if (tag.value.trim()) out.value = tag.value.trim();
      if (tag.isPublic) out.public = true;
      return out;
    });
  }
  if (draft.groupId) input.groupId = draft.groupId;
  if (draft.upsideDown) input.upsideDown = true;
  const size = jsonBytes({ ...input, revision: original?.revision ?? '' });
  if (size > API_BODY_LIMIT) return { errors: { size: `These settings are ${Math.ceil(size / 1024)} KiB; nodes accept up to ${API_BODY_LIMIT / 1024} KiB per monitor. Shorten the request body or certificates.` }, serviceErrors };
  return { input, errors, serviceErrors };
}

/** A preview of what will be sent, with credentials replaced unless the editor asks to see them. */
function masked(input: MonitorInput): MonitorInput {
  const copy = structuredClone(input);
  const hideRequest = (o: RequestCredentials | undefined): void => {
    if (!o) return;
    if (o.headers) o.headers = Object.fromEntries(Object.keys(o.headers).map(key => [key, '•••']));
    if (o.basicAuth) o.basicAuth = { username: o.basicAuth.username, password: '•••' };
    if (o.bearerToken) o.bearerToken = '•••';
    if (o.proxyUrl) o.proxyUrl = maskedUrl(o.proxyUrl);
  };
  hideRequest(copy.http);
  hideRequest(copy.websocket);
  if (copy.http?.body) copy.http.body = '•••';
  if (copy.http?.oauth2) copy.http.oauth2 = { ...copy.http.oauth2, clientSecret: copy.http.oauth2.clientSecret ? '•••' : '' };
  if (copy.websocket?.oauth2) copy.websocket.oauth2 = { ...copy.websocket.oauth2, clientSecret: copy.websocket.oauth2.clientSecret ? '•••' : '' };
  if (copy.tls?.clientKeyPem) copy.tls.clientKeyPem = '•••';
  if (copy.webhookUrl) copy.webhookUrl = maskedUrl(copy.webhookUrl);
  if (copy.push?.token) copy.push = { ...copy.push, token: '•••' };
  if (copy.globalping) copy.globalping = gpMasked(copy.globalping);
  if (copy.steam) copy.steam = steamMasked(copy.steam);
  if (copy.pm2) copy.pm2 = pm2Masked(copy.pm2);
  if (copy.browser) copy.browser = browserMasked(copy.browser);
  if (copy.gamedig) copy.gamedig = gamedigMasked(copy.gamedig);
  // Service fields the form doesn't know (from a newer node) are sent too, so the preview keeps them, with the text of
  // credential-named ones hidden: unknown siblings of the protocol object, and unknown fields inside a known one.
  const service = copy.service as unknown as Record<string, unknown> | undefined;
  if (service) {
    for (const key of Object.keys(service)) {
      service[key] = Object.hasOwn(SERVICE_FIELDS, key)
        ? maskUnknown(maskService(key as ProtocolKey, service[key]), SERVICE_FIELDS[key as ProtocolKey].map(field => field.key))
        : maskSecretNamed(service[key], secretNamed(key));
    }
  }
  return copy;
}

/** Applies `maskSecretNamed` to the fields of `value` that aren't `known` dotted paths, leaving known fields as they are. */
function maskUnknown(value: unknown, known: readonly string[], path = ''): unknown {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return value;
  return Object.fromEntries(Object.entries(value).map(([key, inner]) => {
    const at = path ? `${path}.${key}` : key;
    if (known.includes(at)) return [key, inner];
    if (known.some(field => field.startsWith(`${at}.`))) return [key, maskUnknown(inner, known, at)];
    return [key, maskSecretNamed(inner, secretNamed(key))];
  }));
}

/**
 * The preview masking, for scripts/frontend-review-pure.mjs. `masked` keeps its own name unexported because the
 * contract harness appends `export { draftFrom, build, masked }` to its review copy of this file.
 */
export const previewModel = { masked };

/**
 * The node refuses a monitor whose push token another live monitor already has (409, groups.go validateReferences), so
 * a token that's new in this form (generated, filled in from JSON, or copied by a clone) is checked against this node's
 * monitors before saving. The token the edited monitor is stored with isn't checked here; the node decides that case.
 */
function pushTokenRule(built: Built, draft: Draft, base: MonitorInput | undefined, selfId: string | undefined, monitors: readonly MonitorView[]): Built {
  if (draft.type !== 'push' || !draft.pushToken || draft.pushToken === base?.push?.token) return built;
  const other = monitors.find(view => view.monitor.id !== selfId && view.monitor.push?.token === draft.pushToken);
  if (!other) return built;
  return { errors: { ...built.errors, pushToken: `“${other.monitor.name}” already uses this push token, and the node refuses a second monitor with the same one. Generate another.` }, serviceErrors: built.serviceErrors };
}

/** The form model for a copy, for scripts/monitor-clone-pure.mjs (the harness appends its own exports). */
export const cloneModel = { draftFrom, build, pushTokenRule };

// ---------- form pieces

function RequestFields({ d, set, errors, prefix, disabled, websocket, oauth }: {
  d: RequestDraft; set: (change: Partial<RequestDraft>) => void; errors: Errors; prefix: 'http' | 'ws'; disabled: boolean; websocket?: boolean;
  /** HTTP only: the OAuth2 client-credentials fields, shown when that authentication is chosen. */
  oauth?: ReactNode;
}) {
  const id = useId();
  const f = prefix === 'http' ? { headers: 'headers', auth: 'auth', proxy: 'proxy' } as const : { headers: 'wsHeaders', auth: 'wsAuth', proxy: 'wsProxy' } as const;
  return (
    <>
      {websocket ? <HeaderEditor legend="Headers" rows={d.headers} onChange={headers => set({ headers })} error={errors[f.headers]} disabled={disabled} hint="Sent with the upgrade request. Values are stored on nodes and hidden from viewers." /> : null}
      <Seg legend="Authentication" name={`${id}-auth`} value={d.auth} onChange={auth => set({ auth })} disabled={disabled} full
        options={[{ value: 'none', label: 'None' }, { value: 'basic', label: 'Basic' }, { value: 'bearer', label: 'Bearer token' }, ...(oauth ? [{ value: 'oauth2' as const, label: 'OAuth2 client credentials' }] : [])]}
        hint={d.auth === 'oauth2' ? 'One source only: OAuth2 can’t be combined with Basic, a Bearer token or an Authorization header.' : undefined} />
      {d.auth === 'oauth2' ? oauth : d.auth === 'basic' ? (
        <>
          <TextField label="User name" value={d.username} onChange={username => set({ username })} disabled={disabled} autoComplete="off" />
          <SecretField label="Password" value={d.password} onChange={password => set({ password })} disabled={disabled} />
        </>
      ) : d.auth === 'bearer' ? <SecretField label="Bearer token" value={d.bearer} onChange={bearer => set({ bearer })} disabled={disabled} full /> : null}
      {errors[f.auth] ? <div className="full"><ErrorText id={`${id}-auth-e`}>{errors[f.auth]}</ErrorText></div> : null}
      <Seg legend="Proxy" name={`${id}-proxy`} value={d.proxy} onChange={proxy => set({ proxy })} disabled={disabled} full
        options={[{ value: 'none', label: 'Direct' }, { value: 'url', label: 'Proxy URL' }, { value: 'env', label: 'Node’s environment' }]}
        hint={d.proxy === 'env' ? 'Uses HTTP_PROXY, HTTPS_PROXY and NO_PROXY from each node’s environment.' : undefined} />
      {d.proxy === 'url' ? <SecretField label="Proxy URL" value={d.proxyUrl} onChange={proxyUrl => set({ proxyUrl })} disabled={disabled} full error={errors[f.proxy]} placeholder="http://proxy.internal:3128" hint="Scheme, host and port only. Stored on nodes and hidden from viewers." />
        : errors[f.proxy] ? <div className="full"><ErrorText id={`${id}-proxy-e`}>{errors[f.proxy]}</ErrorText></div> : null}
    </>
  );
}

const authSummary = (d: RequestDraft): string => `${d.auth === 'oauth2' ? 'OAuth2 client credentials' : d.auth === 'basic' ? 'Basic auth' : d.auth === 'bearer' ? 'Bearer token' : 'No authentication'} · ${d.proxy === 'url' ? 'proxy' : d.proxy === 'env' ? 'environment proxy' : 'direct'}`;
const sizeText = (text: string): string => { const n = bytes(text); return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KiB`; };

const CATEGORIES: CheckCategory[] = ['web', 'remote', 'game', 'local', 'data', 'messaging', 'infra', 'manual'];

function TypePicker({ value, onChange, disabled, name, locked }: { value: MonitorType; onChange: (type: MonitorType) => void; disabled: boolean; name: string; locked: boolean }) {
  const [open, setOpen] = useState(!locked);
  const current = checkType(value);
  if (!open) {
    return (
      <div className="fld full">
        <span className="flabel">Check type</span>
        <div className="type-now">
          <b>{current?.label ?? value}</b><span className="muted">{current?.hint}</span>
          <button type="button" className="linkbtn" onClick={() => setOpen(true)} aria-expanded={false} disabled={disabled}>Change type</button>
        </div>
      </div>
    );
  }
  return (
    <fieldset className="fld full types-f" disabled={disabled}>
      <legend>Check type</legend>
      {CATEGORIES.map(category => (
        <div key={category} className="type-cat">
          <span className="type-cat-h" aria-hidden="true">{CATEGORY_LABEL[category]}</span>
          <div className="types">
            {CHECK_TYPES.filter(entry => entry.category === category).map(entry => (
              <label key={entry.type}>
                <input type="radio" name={name} value={entry.type} checked={value === entry.type} onChange={() => onChange(entry.type)} />
                <span><b>{entry.label}</b><small>{entry.hint}</small></span>
              </label>
            ))}
          </div>
        </div>
      ))}
    </fieldset>
  );
}

/**
 * This node's default channels for a new monitor, as the node picks them when a create omits `notificationIds`: enabled
 * channels marked default, sorted by ID, at most 16. A default with a configuration conflict isn't picked (the node
 * would refuse the create until it's resolved or the list is chosen explicitly); disabled defaults are never picked.
 */
export function defaultChannels(list: readonly NotificationChannelView[] | null | undefined): { ids: string[]; conflicted: string[]; tooMany: boolean } {
  const marked = (list ?? []).filter(view => view.channel.isDefault === true && view.channel.enabled && !view.channel.deleted);
  const ids = marked.filter(view => !view.configConflict).map(view => view.channel.id).sort();
  return { ids: ids.slice(0, 16), conflicted: marked.filter(view => view.configConflict).map(view => view.channel.name), tooMany: ids.length > 16 };
}

function ChannelPicker({ selected, onChange, disabled, error, mode }: { selected: string[]; onChange: (ids: string[]) => void; disabled: boolean; error?: string; mode: Draft['channelsMode'] }) {
  const { channels, endpoint } = useCanary();
  const id = useId();
  const list = [...(channels.list ?? [])].sort((a, b) => a.channel.name.localeCompare(b.channel.name));
  const missing = selected.filter(channelId => !channels.map.has(channelId));
  const toggle = (channelId: string, on: boolean): void => onChange(on ? [...selected, channelId] : selected.filter(other => other !== channelId));
  const defaults = defaultChannels(channels.list);
  if (channels.unsupported) return null;
  return (
    <fieldset className="fld full chanpick-f" disabled={disabled} aria-describedby={`${id}-h${error ? ` ${id}-e` : ''}`} aria-invalid={error ? true : undefined}>
      <legend>Notification channels <span className="muted">({selected.length} of at most 16)</span></legend>
      {list.length || missing.length ? (
        <div className="chanpick">
          {list.map(view => {
            const c = view.channel;
            return (
              <label key={c.id} className={c.enabled ? undefined : 'off'}>
                <input type="checkbox" checked={selected.includes(c.id)} onChange={event => toggle(c.id, event.target.checked)} />
                <ProviderIcon kind={c.config.kind} />
                <span>
                  <b>{c.name}</b>
                  <span className="nm-sub">{providerLabel(c.config.kind)}{recipientCount(c) ? ` · ${plural(recipientCount(c), 'recipient')}` : ''}{c.isDefault ? ' · default for new monitors' : ''}{c.enabled ? '' : ' · disabled'}{view.configConflict ? ' · conflict' : ''}</span>
                </span>
              </label>
            );
          })}
          {missing.map(channelId => (
            <label key={channelId} className="off">
              <input type="checkbox" checked onChange={() => toggle(channelId, false)} />
              <Glyph tone="unknown" />
              <span><b className="mono">{shortId(channelId)}</b><span className="nm-sub">Not known to {endpoint} (deleted, or not synced yet)</span></span>
            </label>
          ))}
        </div>
      ) : <p className="muted small">{channels.list ? <>No channels yet. <a href={href('channels', 'new')}>Add a channel</a> for Discord, email or webhooks.</> : channels.error ? `Channels couldn’t be loaded: ${describeFailure(channels.error)}` : 'Loading channels…'}</p>}
      <small id={`${id}-h`}>Each email recipient is a separate destination with its own retries and outcome. Channels can be shared by many monitors. <a href={href('notifications')}>Manage channels</a></small>
      {mode === 'defaults' && selected.length ? <small>Preselected: this node’s enabled default channels. Clear any you don’t want; the monitor is created with exactly the channels checked here.</small> : null}
      {mode === 'node' && !channels.list ? <small>Until the channel list loads, a monitor created without a channel gets this node’s enabled default channels, if it has any.</small> : null}
      {mode !== 'chosen' && defaults.conflicted.length ? <small className="fwarn"><Glyph tone="warn" />Default {defaults.conflicted.length === 1 ? 'channel' : 'channels'} {defaults.conflicted.join(', ')} {defaults.conflicted.length === 1 ? 'has' : 'have'} a configuration conflict, so {defaults.conflicted.length === 1 ? 'it isn’t' : 'they aren’t'} preselected. Resolve it in Configuration, or check it here once resolved.</small> : null}
      {mode !== 'chosen' && defaults.tooMany ? <small className="fwarn"><Glyph tone="warn" />More than 16 default channels are enabled; only the first 16 by ID are preselected.</small> : null}
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}

function TagEditor({ rows, onChange, disabled, error }: { rows: TagDraft[]; onChange: (rows: TagDraft[]) => void; disabled: boolean; error?: string }) {
  const { tags } = useCanary();
  const id = useId();
  const catalog = [...(tags.list ?? [])].filter(view => !view.tag.deleted).sort((a, b) => a.tag.name.localeCompare(b.tag.name));
  const used = new Set(rows.map(row => row.tagId));
  const update = (key: number, change: Partial<TagDraft>): void => onChange(rows.map(row => row.key === key ? { ...row, ...change } : row));
  if (tags.unsupported) return null;
  const next = catalog.find(view => !used.has(view.tag.id));
  return (
    <fieldset className="fld full tag-f" disabled={disabled} aria-describedby={`${id}-h${error ? ` ${id}-e` : ''}`} aria-invalid={error ? true : undefined}>
      <legend>Tags <span className="muted">({rows.length || 'none'})</span></legend>
      {rows.length ? (
        <div className="tag-rows">
          {rows.map((row, index) => {
            const view = tags.map.get(row.tagId);
            return (
              <div className="tag-row" key={row.key}>
                <span className="sw-tag lg" style={{ background: safeColor(view?.tag.color) }} aria-hidden="true" />
                <select aria-label={`Tag ${index + 1}`} value={row.tagId} onChange={event => update(row.key, { tagId: event.target.value })}>
                  {!row.tagId ? <option value="">Choose a tag</option> : null}
                  {catalog.map(option => <option key={option.tag.id} value={option.tag.id} disabled={option.tag.id !== row.tagId && used.has(option.tag.id)}>{option.tag.name}</option>)}
                  {row.tagId && !view ? <option value={row.tagId}>Unknown tag {shortId(row.tagId)}</option> : null}
                </select>
                <input type="text" aria-label={`Tag ${index + 1} value (optional)`} placeholder="value (optional)" value={row.value} maxLength={128} onChange={event => update(row.key, { value: event.target.value })} />
                <label className="check tag-pub"><input type="checkbox" checked={row.isPublic} onChange={event => update(row.key, { isPublic: event.target.checked })} /><span>Public</span></label>
                <button type="button" className="icon-btn sm" onClick={() => onChange(rows.filter(other => other.key !== row.key))} aria-label={`Remove tag ${view?.tag.name ?? index + 1}`}><Icon name="close" /></button>
              </div>
            );
          })}
        </div>
      ) : null}
      <div className="row-wrap pick-acts">
        {catalog.length
          ? <button type="button" className="linkbtn" disabled={!next} onClick={() => next && onChange([...rows, { key: ++tagKey, tagId: next.tag.id, value: '', isPublic: false }])}><Icon name="plus" />Add tag</button>
          : <span className="muted">{tags.list ? 'No tags exist yet.' : 'Loading tags…'}</span>}
        <span aria-hidden="true" className="muted">·</span>
        <a href={href('tags')}>Manage tags</a>
      </div>
      <small id={`${id}-h`}>Tags label and filter monitors; changing them never changes what’s checked. “Public” only allows a status page that turns on tags for this monitor to show it.</small>
      <ErrorText id={`${id}-e`}>{error}</ErrorText>
    </fieldset>
  );
}

function JsonPanel({ input, onImport, disabled }: { input?: MonitorInput; onImport: (value: Partial<Monitor>) => string | null; disabled: boolean }) {
  const [reveal, setReveal] = useState(false);
  const [text, setText] = useState('');
  const [message, setMessage] = useState<string | null>(null);
  // The Steam API key stays masked even when credentials are shown: it is only ever visible in its own field.
  const shown = input ? JSON.stringify(reveal ? (input.steam ? { ...input, steam: steamMasked(input.steam) } : input) : masked(input), null, 2) : null;
  const apply = (): void => {
    let parsed: unknown;
    try { parsed = JSON.parse(text); } catch { setMessage('That isn’t valid JSON.'); return; }
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) { setMessage('Paste a monitor object, as the CLI and API use.'); return; }
    // A masked copy would otherwise be saved with the placeholder as the real token, password, header or body.
    const hidden = maskedPath(parsed);
    if (hidden) {
      setMessage(hidden === 'steam.apiKey'
        ? `Not filled: the Steam API key is never shown, so ${MASK} stands in for it. Remove "apiKey" and enter the key in its field after filling.`
        : `Not filled: ${hidden} is a masked placeholder (${MASK}). Copy with credentials shown, or replace each ${MASK} with the real value.`);
      return;
    }
    setMessage(onImport(parsed as Partial<Monitor>));
  };
  return (
    <>
      <div className="fld full">
        <span className="flabel">What will be sent</span>
        {shown ? <pre className="json-pre" tabIndex={0} aria-label="Monitor JSON preview">{shown}</pre> : <p className="muted small">Fix the fields above to see the JSON.</p>}
        <div className="row-wrap">
          {shown ? <CopyButton value={shown} label={reveal ? 'Copy, with credentials' : 'Copy'} /> : null}
          <button type="button" className="btn sm" aria-pressed={reveal} onClick={() => setReveal(v => !v)}><Icon name={reveal ? 'eyeOff' : 'eye'} />{reveal ? 'Hide credentials' : 'Show credentials'}</button>
        </div>
        <small>The same body the CLI’s <span className="mono">add</span> and <span className="mono">update</span> commands accept. Credentials are masked unless you show them; a node saves {MASK} as the real value, so replace each one before using a masked copy there.{input?.steam ? ' The Steam API key always stays masked here; it is visible only in its own field.' : ''}{input?.pm2?.home ? ' The PM2 home path is masked too until you show credentials.' : ''}{input?.browser ? ' The whole browser endpoint and its bearer token are masked too until you show credentials.' : ''}{input?.gamedig ? ' The whole game query service address, its token and the game token are masked too until you show credentials.' : ''}</small>
      </div>
      <TextAreaField full mono optional label="Fill the form from JSON" value={text} onChange={setText} rows={4} disabled={disabled} placeholder={'{ "name": "API", "type": "http", "target": "https://…" }'}
        hint="Fills the fields above from a monitor object. Nothing is saved until you save the form. Settings the form doesn’t show are not imported." />
      <div className="full row-wrap">
        <button type="button" className="btn sm" onClick={apply} disabled={disabled || !text.trim()}><Icon name="code" />Fill the form</button>
        {message ? <span className="small" role="status">{message}</span> : null}
      </div>
    </>
  );
}

/** 32 random bytes from the browser's CSPRNG, as 64 lowercase hex characters. */
export function newPushToken(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), byte => byte.toString(16).padStart(2, '0')).join('');
}

function PushFields({ token, allowGet, stored, onToken, onAllowGet, disabled, error, interval }: {
  token: string; allowGet: boolean; stored: string; onToken: (value: string) => void; onAllowGet: (value: boolean) => void; disabled: boolean; error?: string; interval: number | null;
}) {
  const { api, selection } = useCanary();
  const [shown, setShown] = useState(false);
  const id = useId();
  useEffect(() => { if (!token) onToken(newPushToken()); }, [token, onToken]);
  const rotated = Boolean(stored) && token !== stored;
  // Control plane: senders go through this dashboard to the selected registration; otherwise to the node itself.
  const registration = api.registeredNode;
  const label = selection && selection.id === registration ? ` (${selection.label})` : '';
  return (
    <>
      <div className="fld full">
        <label htmlFor={`${id}-token`}>Push token</label>
        <div className="secret">
          <input id={`${id}-token`} type={shown ? 'text' : 'password'} className="mono" value={token} readOnly autoComplete="off" spellCheck={false} aria-describedby={`${id}-h`} aria-invalid={error ? true : undefined} />
          <button type="button" className="icon-btn sm" onClick={() => setShown(v => !v)} aria-pressed={shown} aria-label={shown ? 'Hide push token' : 'Show push token'}><Icon name={shown ? 'eyeOff' : 'eye'} /></button>
        </div>
        <div className="row-wrap pick-acts">
          <CopyButton value={token} label="Copy token" className="linkbtn" />
          <span aria-hidden="true" className="muted">·</span>
          <button type="button" className="linkbtn" disabled={disabled} onClick={() => onToken(newPushToken())}><Icon name="refresh" />{stored ? 'Rotate token' : 'Generate another'}</button>
          {rotated ? <><span aria-hidden="true" className="muted">·</span><button type="button" className="linkbtn" disabled={disabled} onClick={() => onToken(stored)}>Keep the current token</button></> : null}
        </div>
        <small id={`${id}-h`}>32 random bytes made in this browser. It identifies this monitor to senders: treat it like a password. Viewers never see it.</small>
        {rotated ? <small className="fwarn"><Glyph tone="warn" />Saving a new token starts a new check: senders using the old token stop counting.</small> : null}
        <ErrorText id={`${id}-e`}>{error}</ErrorText>
      </div>
      <CheckField label="Also accept GET requests" checked={allowGet} onChange={onAllowGet} disabled={disabled}
        hint="For tools that can only fetch a URL. The token is then part of the URL, where proxies and logs can record it. POST with a header is safer." />
      <div className="fld full">
        <span className="flabel">Send heartbeats</span>
        <pre className="json-pre">{pushCommands(api.baseUrl, shown ? token : null, allowGet, registration)}</pre>
        <small>{registration === null ? 'Send to any configured node; HTTP success means that node durably recorded it, not that alerts were delivered.'
          : `These go through this dashboard to the selected node${label}; the dashboard’s server forwards the push token, never a sign-in or cookie. HTTP success means that node durably recorded it, not that alerts were delivered.`} <span className="mono">status</span> is up or down; <span className="mono">msg</span> and <span className="mono">ping</span> (milliseconds) are optional.{interval ? ` Without an up heartbeat for ${span(interval)}, the monitor fails like any other check.` : ''}</small>
        {registration === null ? null : (
          <small>The sending machine must reach this dashboard’s address. Behind a sign-in proxy it must also pass the proxy’s check, or send the same request without <span className="mono">registration</span> straight to the node’s own API on your private network. Don’t exempt <span className="mono">/api/</span> from the proxy for this.</small>
        )}
      </div>
    </>
  );
}

/**
 * What a system-service check reads and where. The name is its only setting. Each node reads its own OS, so the note
 * says what an ordinary container lacks rather than suggesting mounts or privileges.
 */
function SystemServiceNotes({ name }: { name: string }) {
  const unit = systemdUnit(name);
  return (
    <>
      {serviceNameProblem(name) ? null : (
        <p className="full muted small svc-units">
          Linux nodes ask systemd about <span className="mono wrap">{unit}</span>{unit === name ? '' : ' (.service added)'}; Windows nodes ask the Service Control Manager for <span className="mono wrap">{name}</span>.
        </p>
      )}
      <div className="full svc-note" role="note">
        <Icon name="nodes" className="inline" />
        <span>
          <b>Each selected node checks its own operating system, never another host.</b> It only reads the service’s state, with no credentials: nothing is started, stopped or changed. Running is up; stopped or not installed is down. A node that can’t read a service manager (none present, no permission, an unsupported OS, all its check workers busy, or a manager that doesn’t answer within the timeout) reports pending, which never recovers an outage. A node in an ordinary container, such as the Alpine image, can’t see the host’s systemd or Windows services, so it stays pending. Run the node directly on each machine whose service you want to watch, and choose those nodes under Placement.
        </span>
      </div>
      <p className="full muted small"><Icon name="clock" className="inline" />Latency isn’t measured and shows as a dash. A local query answers quickly, so a timeout of about 5 s is plenty.</p>
    </>
  );
}


type OAuthMethod = '' | 'client_secret_basic' | 'client_secret_post';
interface OAuthDraft { tokenUrl: string; clientId: string; clientSecret: string; scope: string; audience: string; method: OAuthMethod; ca: string; insecure: boolean }
type OAuth2Options = NonNullable<HTTPMonitorOptions['oauth2']>;
const oauthDraft = (o: OAuth2Options | undefined): OAuthDraft => ({
  tokenUrl: o?.tokenUrl ?? '', clientId: o?.clientId ?? '', clientSecret: o?.clientSecret ?? '', scope: o?.scope ?? '', audience: o?.audience ?? '',
  method: (o?.authMethod ?? '') as OAuthMethod, ca: o?.tokenCaPem ?? '', insecure: Boolean(o?.allowInsecureHttp),
});

/**
 * Applies the OAuth2 draft on top of the stored object, so unknown keys survive. The token endpoint and client ID are
 * required; the client secret key is always sent, even when empty, which the node accepts. `field` is where problems go:
 * the HTTP check's OAuth2 or (protocol 19) the WebSocket check's, whose monitored URL must be wss:// unless plain HTTP is
 * allowed.
 */
function applyOAuth(stored: OAuth2Options | undefined, d: OAuthDraft, target: string, errors: Errors, field: 'oauth' | 'wsOauth'): OAuth2Options {
  const local: Errors = {};
  const o = applyOAuthTo(stored, d, target, local);
  if (local.oauth) errors[field] = field === 'wsOauth' ? local.oauth.replace('both the token endpoint and the monitored URL', 'the token endpoint, and wss:// for the monitored URL,') : local.oauth;
  return o;
}

function applyOAuthTo(stored: OAuth2Options | undefined, d: OAuthDraft, target: string, errors: Errors): OAuth2Options {
  const o = (stored ? structuredClone(stored) : {}) as OAuth2Options;
  o.tokenUrl = d.tokenUrl.trim();
  o.clientId = d.clientId;
  o.clientSecret = d.clientSecret;
  for (const key of ['scope', 'audience'] as const) { const value = d[key].trim(); if (value) o[key] = value; else delete o[key]; }
  if (d.method) o.authMethod = d.method; else delete o.authMethod;
  if (d.ca.trim()) o.tokenCaPem = d.ca; else delete o.tokenCaPem;
  if (d.insecure) o.allowInsecureHttp = true; else delete o.allowInsecureHttp;
  // The node requires a token endpoint and client ID; only the client secret may legitimately be empty.
  if (!o.tokenUrl) errors.oauth = 'Enter the token endpoint.';
  else if (!o.clientId) errors.oauth = 'Enter the client ID.';
  if (o.tokenUrl) {
    let url: URL | null = null;
    try { url = new URL(o.tokenUrl); } catch { /* reported below */ }
    if (!url || (url.protocol !== 'https:' && url.protocol !== 'http:') || url.username || url.password || url.hash) errors.oauth = 'Enter the token endpoint as a complete https:// address without credentials or #fragment.';
  }
  if (!errors.oauth && !d.insecure && ((o.tokenUrl && !/^https:\/\//iu.test(o.tokenUrl)) || /^(?:http|ws):\/\//iu.test(target.trim()))) {
    errors.oauth = 'OAuth2 needs https:// for both the token endpoint and the monitored URL, unless you allow plain HTTP below.';
  }
  if (!errors.oauth && (bytes(o.clientId) > 4096 || bytes(o.clientSecret) > 8192)) errors.oauth = 'The client ID or secret is too long.';
  if (!errors.oauth && ((o.scope && (bytes(o.scope) > 4096 || hasControl(o.scope))) || (o.audience && (bytes(o.audience) > 4096 || hasControl(o.audience))))) errors.oauth = 'Scope and audience are at most 4 KiB on one line.';
  if (!errors.oauth && o.tokenCaPem && !/-----BEGIN [A-Z0-9 ]*CERTIFICATE-----/u.test(o.tokenCaPem)) errors.oauth = 'Paste PEM certificates for the token endpoint (-----BEGIN CERTIFICATE-----).';
  return o;
}

function OAuthFields({ d, set, stored, error, disabled, websocket }: { d: OAuthDraft; set: (change: Partial<OAuthDraft>) => void; stored: OAuthDraft | undefined; error?: string; disabled: boolean; websocket?: boolean }) {
  const id = useId();
  return (
    <>
      <TextField full mono label="Token endpoint" value={d.tokenUrl} onChange={tokenUrl => set({ tokenUrl })} placeholder="https://auth.example.com/oauth2/token" disabled={disabled}
        hint={websocket
          ? 'Required. The client-credentials grant runs on every check, through the same proxy setting as the upgrade; the access token is sent as Bearer with the upgrade request and never stored.'
          : 'Required. The client-credentials grant runs on every check; the access token is sent as Bearer and never stored.'} />
      <TextField mono label="Client ID" value={d.clientId} onChange={clientId => set({ clientId })} disabled={disabled} autoComplete="off" hint="Required." />
      <SecretField label="Client secret" value={d.clientSecret} onChange={clientSecret => set({ clientSecret })} disabled={disabled} storedSet={Boolean(stored?.clientSecret)}
        hint="Can be empty if the issuer allows it. Hidden from viewers." />
      <Seg legend="Send the client credentials" name={`${id}-method`} value={d.method === 'client_secret_post' ? 'post' : 'basic'} disabled={disabled} full
        // Basic is the default; keep an explicitly stored client_secret_basic rather than dropping it.
        onChange={choice => set({ method: choice === 'post' ? 'client_secret_post' : stored?.method === 'client_secret_basic' ? 'client_secret_basic' : '' })}
        options={[{ value: 'basic', label: 'Basic authorization header' }, { value: 'post', label: 'In the form body' }]}
        hint="client_secret_basic or client_secret_post, as your issuer expects." />
      <TextField label="Scope (optional)" value={d.scope} onChange={scope => set({ scope })} disabled={disabled} placeholder="read:status" />
      <TextField label="Audience (optional)" value={d.audience} onChange={audience => set({ audience })} disabled={disabled} />
      <PemField label="Token endpoint CA certificates" kind="certificate" value={d.ca} onChange={ca => set({ ca })} disabled={disabled}
        hint="Trust only for the token request, separate from the monitored URL’s TLS settings." />
      <CheckField label="Allow plain HTTP" checked={d.insecure} onChange={insecure => set({ insecure })} disabled={disabled} danger
        hint={websocket
          ? 'Needed if the token endpoint uses http:// or the monitored URL uses ws://. The client secret and token then travel unencrypted.'
          : 'Needed if the token endpoint or the monitored URL uses http://. The client secret and token then travel unencrypted.'} />
      {error ? <div className="full"><ErrorText id={`${id}-e`}>{error}</ErrorText></div> : null}
    </>
  );
}

// ---------- the view

/**
 * The edit form mounts only once the stored item is known, so its draft, initial values and base revision come from
 * that item. A route opened before the overview caught up shows "not found" and then mounts fresh, never a blank draft.
 */
export function MonitorFormView({ id, cloneOf, fleetId }: { id?: string; cloneOf?: string; fleetId?: string }) {
  const { monitorMap, editable, controlPlane, fleet } = useCanary();
  if (controlPlane && fleet && (!fleet.ready || fleet.denied)) return <Card><EmptyState title={fleet.loading ? 'Loading saved placements' : 'Saved placements could not be read'} action={fleet.loading ? undefined : <button className="btn" type="button" onClick={fleet.reload}>Try again</button>}>The saved fleet configuration must be read before this form can change a monitor.</EmptyState></Card>;
  if (fleetId && !fleet?.deployments.some(item => item.id === fleetId && item.state !== 'removed')) return <Card><EmptyState title="Fleet monitor not found">Reload All nodes to read its current placement.</EmptyState></Card>;
  const existing = id ? monitorMap.get(id) : undefined;
  const source = cloneOf ? monitorMap.get(cloneOf) : undefined;
  // A copy is taken once, when its form mounts. It's taken again only if the source, the role or the source's conflict
  // state changes, never because the source is edited meanwhile.
  const key = existing ? `edit-${existing.monitor.id}` : id ? 'missing'
    : source ? `clone-${source.monitor.id}-${editable ? 'rw' : 'ro'}-${source.configConflict ? 'conflict' : 'one'}` : cloneOf ? 'clone-missing' : 'new';
  return <MonitorFormBody key={fleetId ? `fleet-${fleetId}` : key} id={id} cloneOf={cloneOf} fleetId={fleetId} />;
}

/**
 * `id` edits that monitor. `cloneOf` creates a new monitor that starts from a copy of that one: nothing is created until
 * the form is saved, the source isn't changed, and the copy lives only in this component's state, never in the URL
 * (which names the source alone), browser storage or the console.
 */
function MonitorFormBody({ id, cloneOf, fleetId }: { id?: string; cloneOf?: string; fleetId?: string }) {
  const { overview, monitorMap, nodes, nodeMap, endpoint, editable, api, refresh, groups, groupMap, stillCurrent, channels, controlPlane, fleet, registry, selection } = useCanary();
  const existing = id ? monitorMap.get(id) : undefined;
  const source = cloneOf ? monitorMap.get(cloneOf) : undefined;
  const defaultNodes = nodes.filter(node => node.state !== 'unlisted').map(node => node.id);
  const fleetEnabled = controlPlane && Boolean(fleet) && Boolean(selection);
  const [fleetBase] = useState(() => fleetId ? fleet?.deployments.find(item => item.id === fleetId) : existing ? managedMonitor(fleet?.deployments ?? [], selection?.id, existing.monitor.id) : undefined);
  // Only editors copy: a viewer's monitor has its private settings removed. A monitor whose nodes hold different
  // versions isn't copied, because one version would be picked silently; nor is a type this dashboard doesn't know.
  const [copy] = useState<MonitorInput | undefined>(() => editable && source && !source.configConflict && checkType(source.monitor.type) ? cloneInput(source.monitor, newPushToken) : undefined);
  // The copy is the base of a create, never of an update, so it never needs an ID or revision.
  const start = fleetBase ? { ...fleetBase.monitor, id: existing?.monitor.id ?? fleetBase.monitor.id, revision: existing?.monitor.revision ?? fleetBase.revision } : existing?.monitor ?? (copy as Monitor | undefined);
  const [draft, setDraft] = useState<Draft>(() => draftFrom(start, defaultNodes));
  const [targetReferences] = useState<Record<string,FleetTargetReferences>>(() => fleetBase?.references ?? {});
  const [fleetRequestId] = useState(() => fleetEnabled ? newPushToken().slice(0, 32) : '');
  const [fleetTargets, setFleetTargets] = useState<string[]>(() => !fleetEnabled ? [] : fleetBase ? [...fleetBase.registrationIds]
    : (start?.nodeIds ?? [endpoint]).map(nodeId => registry.list?.find(row => row.nodeId === nodeId)?.id ?? `unregistered:${nodeId}`));
  const [initial, setInitial] = useState<Draft | undefined>(() => start ? draftFrom(start, defaultNodes) : undefined);
  const [baseMonitor, setBaseMonitor] = useState<Monitor | undefined>(start);
  const [version, setVersion] = useState(0);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<{ message: string; stale: boolean } | null>(null);
  const [showWebhook, setShowWebhook] = useState(false);
  const [attempts, setAttempts] = useState(0);
  const [open, setOpen] = useState<Record<string, boolean>>(() => {
    const d = draft;
    const protocol = protocolOf(d.type);
    return {
      request: d.http.headers.length > 0 || Boolean(d.body) || d.redirects !== '',
      access: d.http.auth !== 'none' || d.http.proxy !== 'none',
      response: d.keywordOn || d.jsonOn || d.jsonataOn,
      ws: d.ws.headers.length > 0 || d.ws.auth !== 'none' || d.ws.proxy !== 'none',
      tls: hasTlsDraft(d) || d.type === 'tls',
      legacy: Boolean(d.webhookUrl),
      gpApi: Boolean(d.gp.apiUrl || d.gp.token || d.gp.ca || d.gp.insecure || d.gp.failureDown),
      gpRequest: d.gp.headers.length > 0,
      gpResponse: Boolean(d.gp.statuses || d.gp.keyword || d.gp.jsonata.expression),
      steamApi: Boolean(d.steam.apiUrl || d.steam.ca || d.steam.insecure),
      ...(protocol && d.service[d.type] ? openServiceSections(protocol, d.service[d.type]!) : {}),
    };
  });
  const summaryRef = useRef<HTMLDivElement>(null);
  const formId = useId();
  const selfId = existing?.monitor.id;
  const result = useMemo(() => pushTokenRule(build(draft, baseMonitor, initial), draft, baseMonitor, selfId, overview.monitors), [draft, baseMonitor, initial, selfId, overview.monitors]);

  // Move focus to the summary after a failed submit so keyboard and screen reader users hear what to fix.
  useEffect(() => { if (attempts) summaryRef.current?.focus(); }, [attempts, failure]);
  // A new monitor (never an edit or a copy) starts with this node's enabled default channels once the list is known, and
  // follows the list while nobody has picked channels. A pick, a JSON fill or a cleared list is the user's choice and stays.
  const defaultKey = channels.list ? defaultChannels(channels.list).ids.join(' ') : null;
  useEffect(() => {
    if (defaultKey === null) return;
    setDraft(d => d.channelsMode === 'chosen' ? d : { ...d, channelIds: defaultKey ? defaultKey.split(' ') : [], channelsMode: 'defaults' });
  }, [defaultKey]);

  if (!editable) {
    return <div className="view"><PageHead title={id ? 'Edit monitor' : cloneOf ? 'Copy monitor' : 'New monitor'} /><Card><EmptyState title="Viewers can’t change monitors">Ask an editor or admin, or sign in with an editor account.</EmptyState></Card></div>;
  }
  if (id && !existing) {
    return <div className="view"><PageHead title="Edit monitor" /><Card><EmptyState title="Monitor not found">{endpoint} doesn’t know this monitor. It may have been deleted.</EmptyState></Card></div>;
  }
  if (cloneOf && !source) {
    return <div className="view"><PageHead crumbs={<a href={href('monitors')}>Monitors</a>} title="Copy monitor" /><Card><EmptyState title="Monitor not found">{endpoint} doesn’t know the monitor to copy. It may have been deleted.</EmptyState></Card></div>;
  }
  const known = existing ?? source ?? (fleetBase ? { monitor: fleetBase.monitor } : undefined);
  if (known && !checkType(known.monitor.type)) {
    return (
      <div className="view">
        <PageHead crumbs={<><a href={href('monitors')}>Monitors</a><Icon name="chevRight" /><a href={href('monitors', known.monitor.id)}>{known.monitor.name}</a></>} title={existing ? 'Edit monitor' : 'Copy monitor'} />
        <Card>
          <EmptyState title={`This dashboard doesn’t know the “${known.monitor.type}” check type`} action={<a className="btn" href={href('monitors', known.monitor.id)}><Icon name="chevLeft" />Back to the monitor</a>}>
            The node is newer than this dashboard. {existing ? 'Edit' : 'Copy'} it with the CLI or API so none of its settings are lost.
          </EmptyState>
        </Card>
      </div>
    );
  }
  if (source && !copy) {
    return (
      <div className="view">
        <PageHead crumbs={<><a href={href('monitors')}>Monitors</a><Icon name="chevRight" /><a href={href('monitors', source.monitor.id)}>{source.monitor.name}</a></>} title="Copy monitor" />
        <Card>
          <EmptyState title="Resolve the conflict before copying" action={<a className="btn" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>
            Nodes hold different versions of this monitor, so there’s no single version to copy.
          </EmptyState>
        </Card>
      </div>
    );
  }

  const { input, errors, serviceErrors } = result;
  const visibleErrors = showErrors ? errors : {};
  const visibleServiceErrors = showErrors ? serviceErrors : {};
  const set = <K extends keyof Draft>(key: K, value: Draft[K]): void => setDraft(current => ({ ...current, [key]: value }));
  const current = existing?.monitor;
  const changedElsewhere = Boolean(current && baseMonitor && current.revision !== baseMonitor.revision);
  const conflicted = Boolean(existing?.configConflict);
  const group = draft.groupId ? groupMap.get(draft.groupId) : undefined;
  const groupList = (groups ?? []).filter(view => !view.group.deleted);
  const type = draft.type;
  const target = draft.targets[type] ?? '';
  const meta = checkType(type)!;
  const protocol = protocolOf(type);
  const serviceDraft = protocol ? draft.service[type] ?? serviceValues(protocol, undefined) : undefined;
  // The stored values, for "Saving removes the stored value" and rotation notes. A copy has none: nothing is stored yet,
  // and clearing a copied value never touches the source.
  const stored = copy ? undefined : initial;
  const serviceStored = protocol && stored?.type === type ? stored.service[type] ?? {} : {};
  const tlsOn = tlsApplies(type, target, draft.smtpMode, draft.tcpTls);
  const toggle = (section: string): void => setOpen(state => ({ ...state, [section]: !state[section] }));
  const openIncident = current ? overview.incidents.find(incident => incident.monitorId === current.id && incident.resolvedAt === null) : undefined;
  const changes = current && input && baseMonitor ? checkChanges(inputOf(baseMonitor), input) : [];

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving) return;
    setShowErrors(true);
    setFailure(null);
    if (fleetEnabled && fleet?.error) { setFailure({ message: 'Refresh the saved fleet placements before saving. Your draft is kept here.', stale: false }); setAttempts(n => n + 1); return; }
    if (fleetEnabled && (!fleetTargets.length || fleetTargets.some(id => !registry.map.has(id)))) {
      setFailure({ message: 'Choose at least one registered node. Register or explicitly remove any missing selections before saving.', stale: false });
      setAttempts(n => n + 1); return;
    }
    if (!input) {
      const sections = Object.keys(errors).map(field => SECTION_OF[field as Field]).filter(Boolean) as string[];
      if (sections.length) setOpen(state => ({ ...state, ...Object.fromEntries(sections.map(section => [section, true])) }));
      setAttempts(n => n + 1);
      return;
    }
    setSaving(true);
    try {
      if (fleetEnabled) {
        await api.saveFleetMonitor({ requestId: fleetRequestId, monitor: input, registrationIds: fleetTargets,
          references: Object.fromEntries(Object.entries(targetReferences).filter(([id]) => fleetTargets.includes(id))),
          ...(!fleetBase && current && baseMonitor && selection ? { source: { registrationId: selection.id, monitorId: current.id, revision: baseMonitor.revision } } : {}) }, fleetBase || undefined);
        if (!stillCurrent()) return;
        notify(`Saved placement for “${input.name}”. Each node's confirmation appears in All nodes.`);
        fleet?.reload(); refresh(); navigate(href('overview')); return;
      }
      if (current && baseMonitor) {
        const saved = await api.updateMonitor(current.id, { ...input, revision: baseMonitor.revision });
        if (!stillCurrent()) return;
        notify(`Saved “${saved.name}” on ${endpoint} as revision ${shortRev(saved.revision)}. Peers pick it up when they next sync.`);
        refresh();
        navigate(href('monitors', saved.id));
      } else {
        const created = await api.createMonitor(input);
        if (!stillCurrent()) return;
        notify(`Created “${created.name}” on ${endpoint}${copy && source ? ` from a copy of “${source.monitor.name}”` : ''} (revision ${shortRev(created.revision)}). Assigned peers receive it when they next sync.`);
        refresh();
        navigate(href('monitors', created.id));
      }
    } catch (error) {
      if (!stillCurrent()) return;
      const stale = error instanceof ApiFailure && error.status === 409;
      setFailure({ message: stale ? fleetEnabled ? 'The saved deployment changed, is still running, or needs review. Refresh All nodes before saving again.' : 'Someone saved a newer revision first, or nodes now hold conflicting versions.' : describeFailure(error), stale });
      setAttempts(n => n + 1);
    } finally {
      setSaving(false);
    }
  };

  const loadLatest = (): void => {
    if (!current) return;
    setDraft(draftFrom(current, defaultNodes));
    setInitial(draftFrom(current, defaultNodes));
    setBaseMonitor(current);
    setVersion(v => v + 1);
    setFailure(null);
    setShowErrors(false);
  };

  const importJson = (value: Partial<Monitor>): string | null => {
    if (typeof value.type !== 'string' || !checkType(value.type)) return 'The JSON needs a known "type", such as "http".';
    // A copy fills over its own settings, as an edit fills over the stored ones; a pasted push token is still checked.
    const filled = draftFrom({ ...(current ?? copy ?? {}), ...value, nodeIds: Array.isArray(value.nodeIds) ? value.nodeIds : draft.nodeIds } as Monitor, defaultNodes);
    // On a new monitor, JSON without a channel list keeps the form's current channels (and their defaults) as they are.
    const next = !current && !copy && !Array.isArray(value.notificationIds) ? { ...filled, channelIds: draft.channelIds, channelsMode: draft.channelsMode } : filled;
    setDraft(next);
    setVersion(v => v + 1);
    setShowErrors(true);
    return `Filled from JSON${value.type !== draft.type ? ` as a ${typeLabel(value.type)} check` : ''}. Review the fields, then save.`;
  };

  const errorList = [
    ...Object.entries(visibleErrors).map(([field, message]) => `${FIELD_LABEL[field as Field]}: ${message}`),
    ...Object.values(visibleServiceErrors),
  ];
  const requestSummary = `${draft.method || 'GET'} · ${plural(draft.http.headers.filter(row => row.name.trim()).length, 'header')}${draft.body ? ` · body ${sizeText(draft.body)}` : ''} · ${draft.redirects === '' ? 'up to 5 redirects' : draft.redirects === '0' ? 'no redirects' : `up to ${draft.redirects} redirects`}`;
  const responseSummary = [draft.keywordOn ? `${draft.keywordNegate ? 'Not ' : ''}${draft.keywordRegex ? 'pattern' : 'keyword'}` : '', draft.jsonOn ? `JSON ${draft.jsonPath}` : '', draft.jsonataOn ? 'JSONata' : ''].filter(Boolean).join(' · ') || 'Status code only';
  const tlsSummary = [draft.tlsServerName ? `SNI ${draft.tlsServerName}` : '', draft.tlsCa ? 'custom CA' : 'system roots', draft.tlsCert ? 'client certificate' : '', draft.tlsMinDays ? `≥ ${draft.tlsMinDays} days` : '', draft.tlsIgnore ? 'verification off' : ''].filter(Boolean).join(' · ');
  const retryInterval = fromDraft(draft.retryInterval);
  const retries = Number(draft.retries) || 0;

  return (
    <div className="view">
      <PageHead
        crumbs={<><a href={href('monitors')}>Monitors</a>{current ? <><Icon name="chevRight" /><a href={href('monitors', current.id)}>{current.name}</a></> : copy && source ? <><Icon name="chevRight" /><a href={href('monitors', source.monitor.id)}>{source.monitor.name}</a></> : null}</>}
        title={current || fleetBase ? 'Edit monitor' : copy ? 'Copy monitor' : 'New monitor'}
        sub={fleetEnabled ? <>Saves one dashboard placement across the registered nodes chosen below. Each independent placement keeps its own monitoring history.</> : current
          ? <>Editing revision <Rev revision={baseMonitor?.revision ?? current.revision} />{changedElsewhere ? null : <>, saved <Time at={current.updatedAt} now={overview.now} /></>}. Saving writes a new revision on <span className="mono">{endpoint}</span>.</>
          : copy && source
          ? <>Creates a new monitor with its own stable ID on <span className="mono">{endpoint}</span>, starting from a copy of “{source.monitor.name}”. Nothing is created until you select Create monitor, and “{source.monitor.name}” stays as it is.</>
          : <>Creates a monitor with a new stable ID on <span className="mono">{endpoint}</span>, which shares it with the nodes you choose.</>}
      />
      {conflicted ? (
        <Banner tone="warn" title="This monitor has a configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>
          Nodes hold different versions. Edits are rejected until someone resolves the conflict.
        </Banner>
      ) : changedElsewhere ? (
        <Banner tone="warn" title={<>This monitor changed on {endpoint} while you were editing</>} actions={<button type="button" className="btn sm" onClick={loadLatest}><Icon name="refresh" />Load latest (discards your edits)</button>}>
          It’s now revision <Rev revision={current!.revision} />. Saving your version will be rejected because it’s based on <Rev revision={baseMonitor!.revision} />.
        </Banner>
      ) : null}
      <Card>
        <form id={formId} key={version} className="form" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
          <ErrorSummary ref={summaryRef} title={failure ? (failure.stale ? 'Not saved: the monitor changed or has a conflict' : 'Not saved') : 'Check these fields'} errors={failure ? [] : errorList}>
            {failure ? <p>{failure.message}{failure.stale && current ? <> <button type="button" className="linkbtn" onClick={loadLatest}>Load the latest version</button> and apply your change again.</> : null}</p> : null}
          </ErrorSummary>
          {copy ? (
            <div className="full svc-note copy-note" role="note">
              <Icon name="lock" className="inline" />
              <span>
                <b>Private connection settings are copied too.</b> Passwords, tokens, API keys, headers, request bodies, certificates and keys, game query service addresses, and proxy, webhook and browser service addresses come along with every other setting, including ones this form doesn’t show. They stay in this page until you create the copy, and then go to <span className="mono">{endpoint}</span> like any monitor’s settings. The address bar names only the monitor you copied, and the JSON preview masks them unless you show credentials. Change any that should differ.
                {copy.type === 'push' ? ' This copy has its own new push token, so heartbeats sent with the original’s token never count for it.' : ''}
              </span>
            </div>
          ) : null}

          <h3 className="fsec full">Check</h3>
          <TextField label="Name" value={draft.name} onChange={value => set('name', value)} error={visibleErrors.name} hint="Shown in alerts and lists" maxLength={128} autoFocus={!current} disabled={saving} />
          <TextAreaField full optional label="Description" value={draft.description} onChange={value => set('description', value)} error={visibleErrors.description} rows={2} disabled={saving}
            hint="Notes for the people who use this dashboard, such as what the service is and who owns it. Plain text, up to 8,192 bytes, line breaks allowed. Everyone signed in sees it, viewers included; it’s never published on a status page or sent in notifications. Changing it never starts a new check." />
          <TypePicker value={type} onChange={value => set('type', value)} disabled={saving} name={`${formId}-type`} locked={Boolean(current || copy)} />
          {current && current.type !== type ? <p className="full fwarn small"><Glyph tone="warn" />Changing the type starts a new check. An open incident closes without a recovery alert.</p> : null}

          {TYPED_TARGET_TYPES.has(type) ? (
            <TextField
              full mono label={meta.targetLabel!} type={type === 'http' || type === 'websocket' || type === 'real-browser' ? 'url' : 'text'} inputMode={type === 'http' || type === 'websocket' || type === 'real-browser' ? 'url' : 'text'}
              value={target} onChange={value => setDraft(d => ({ ...d, targets: { ...d.targets, [d.type]: value } }))} error={visibleErrors.target}
              placeholder={type === 'dns' && draft.dnsRecord === 'PTR' ? '192.0.2.10' : meta.placeholder} hint={meta.targetHint} disabled={saving}
            />
          ) : null}
          {type === 'tcp' ? (
            <CheckField label="Use TLS after connecting" checked={draft.tcpTls} onChange={value => { set('tcpTls', value); if (value) setOpen(state => ({ ...state, tls: true })); }} disabled={saving}
              hint={draft.tcpTls
                ? 'Up only when a TLS 1.2+ handshake succeeds right after the connection opens and the certificate is valid, with the settings under “TLS and certificate” below. For a protocol that upgrades to TLS later, such as SMTP STARTTLS, use its own check type.'
                : 'Off: up as soon as the TCP connection opens; nothing is sent. Turn it on for a service that speaks TLS from the first byte.'} />
          ) : null}
          {type === 'system-service' ? <SystemServiceNotes name={target.trim()} /> : null}
          {type === 'tailscale-ping' ? <TailscaleNotes /> : null}
          {type === 'real-browser' ? (
            <BrowserFields d={draft.browser} set={change => setDraft(d => ({ ...d, browser: { ...d.browser, ...change } }))} errors={visibleErrors} disabled={saving}
              storedBearer={Boolean(stored?.type === 'real-browser' && stored.browser.bearer)} timeoutMs={fromDraft(draft.timeout)} />
          ) : null}

          {protocol && serviceDraft ? (
            <ServiceFields
              protocol={protocol} values={serviceDraft} stored={serviceStored} errors={visibleServiceErrors} disabled={saving} open={open} onToggle={toggle}
              onChange={values => setDraft(d => ({ ...d, service: { ...d.service, [d.type]: values } }))}
            />
          ) : null}

          {type === 'manual' ? (
            <>
              <Seg legend="Status" name={`${formId}-manual`} value={draft.manualState} onChange={value => set('manualState', value)} disabled={saving} full
                options={[{ value: '', label: 'Not set' }, { value: 'up', label: 'Up' }, { value: 'down', label: 'Down' }, { value: 'pending', label: 'Pending' }]}
                hint={draft.manualState === '' ? 'Not set reports pending with no status. Pending never sends a recovery.' : draft.manualState === 'up' ? 'Up recovers an open manual outage and sends its recovery.' : draft.manualState === 'down' ? 'Down opens an incident and alerts like any other check (after retries, if set).' : 'Pending is neither up nor down: it doesn’t recover an outage.'} />
              <TextField full label="Status message (optional)" value={draft.manualMessage} onChange={value => set('manualMessage', value)} error={visibleErrors.manualMessage} maxLength={256} disabled={saving}
                hint="Shown with the result, like “Planned failover in progress”. Up to 256 bytes." />
              <p className="full muted small"><Icon name="hand" className="inline" />No network request is made. Changing the status or message keeps the same check, so an open incident continues or recovers instead of closing.</p>
            </>
          ) : null}

          {type === 'globalping' ? (
            <GlobalpingFields d={draft.gp} set={change => setDraft(d => ({ ...d, gp: { ...d.gp, ...change } }))} errors={visibleErrors} disabled={saving} open={open} onToggle={toggle}
              storedToken={Boolean(stored?.type === 'globalping' && stored.gp.token)} timeoutMs={fromDraft(draft.timeout)} intervalMs={fromDraft(draft.interval)} nodeCount={draft.nodeIds.length}
              onUseTimeout={ms => set('timeout', toDraft(ms))} />
          ) : null}

          {type === 'steam' ? (
            <SteamFields d={draft.steam} set={change => setDraft(d => ({ ...d, steam: { ...d.steam, ...change } }))} errors={visibleErrors} disabled={saving} open={open} onToggle={toggle}
              storedKey={stored?.type === 'steam' ? stored.steam.apiKey : ''} timeoutMs={fromDraft(draft.timeout)} intervalMs={fromDraft(draft.interval)} nodeCount={draft.nodeIds.length} />
          ) : null}

          {type === 'pm2' ? <Pm2Fields d={draft.pm2} set={change => setDraft(d => ({ ...d, pm2: { ...d.pm2, ...change } }))} errors={visibleErrors} disabled={saving} stored={initial?.type === 'pm2' ? initial.pm2.transport : undefined} /> : null}

          {type === 'gamedig' ? (
            <GameDigFields d={draft.gamedig} set={change => setDraft(d => ({ ...d, gamedig: { ...d.gamedig, ...change } }))} errors={visibleErrors} disabled={saving}
              storedGame={initial?.type === 'gamedig' ? initial.gamedig.game : undefined} storedToken={Boolean(stored?.type === 'gamedig' && stored.gamedig.token)} timeoutMs={fromDraft(draft.timeout)} />
          ) : null}

          {type === 'push' ? (
            <PushFields token={draft.pushToken} allowGet={draft.pushAllowGet} stored={stored?.type === 'push' ? stored.pushToken : ''} disabled={saving} error={visibleErrors.pushToken}
              onToken={value => set('pushToken', value)} onAllowGet={value => set('pushAllowGet', value)} interval={fromDraft(draft.interval)} />
          ) : null}

          {type === 'ping' ? (
            <>
              <TextField label="Packets per check (optional)" value={draft.pingCount} onChange={value => set('pingCount', value)} error={visibleErrors.pingCount} placeholder="1" inputMode="numeric" disabled={saving}
                hint="1 to 10. One verified reply is enough for up. The timeout covers every packet." />
              <TextField label="Payload size (optional)" value={draft.pingSize} onChange={value => set('pingSize', value)} error={visibleErrors.pingSize} placeholder="32" inputMode="numeric" disabled={saving}
                hint="0 to 65,500 bytes. Empty: 32." />
              <Seg legend="Address family" name={`${formId}-family`} value={draft.pingFamily} onChange={value => set('pingFamily', value)} disabled={saving} full
                options={[{ value: 'auto', label: 'Automatic' }, { value: 'ipv4', label: 'IPv4 only' }, { value: 'ipv6', label: 'IPv6 only' }]}
                hint="Where a node can’t send ICMP, its result is unknown: never up, never down, and it can’t recover an outage." />
            </>
          ) : null}

          {type === 'http' ? (
            <>
              <TextField mono label="Accepted status" value={draft.expected} onChange={value => set('expected', value)} error={visibleErrors.expected} placeholder="200-299" disabled={saving}
                hint={<>Codes or ranges, like 200-299, 301. Empty means any 2xx.{draft.statusMode === 'codes' && (parseRanges(draft.expected).codes ?? 0) > 100 ? ' More than 100 codes are saved as status ranges.' : ''}</>} />
              <div className="fld">
                <label htmlFor={`${formId}-method`}>Method</label>
                <div className="dur method-in">
                  <select id={`${formId}-method`} value={METHODS.includes(draft.method) ? draft.method : 'other'} onChange={event => set('method', event.target.value === 'other' ? '' : event.target.value)} disabled={saving}
                    aria-invalid={visibleErrors.method ? true : undefined} aria-describedby={visibleErrors.method ? `${formId}-method-e` : undefined}>
                    {METHODS.map(method => <option key={method} value={method}>{method}</option>)}
                    <option value="other">Other…</option>
                  </select>
                  {!METHODS.includes(draft.method) ? <input type="text" className="mono" aria-label="Custom method" value={draft.method} onChange={event => set('method', event.target.value)} disabled={saving} placeholder="PROPFIND" /> : null}
                </div>
                <ErrorText id={`${formId}-method-e`}>{visibleErrors.method}</ErrorText>
              </div>
              <Disclosure title="Request headers and body" summary={requestSummary} open={Boolean(open.request)} onToggle={() => toggle('request')}>
                <HeaderEditor legend="Headers" rows={draft.http.headers} onChange={headers => set('http', { ...draft.http, headers })} error={visibleErrors.headers} disabled={saving} hint="Values are stored on nodes and hidden from viewers." />
                <TextAreaField full mono optional label="Body" value={draft.body} onChange={value => set('body', value)} error={visibleErrors.body} rows={4} disabled={saving}
                  hint={<>Sent as is{draft.body ? ` (${sizeText(draft.body)})` : ''}. Add a Content-Type header if the service needs one. Cross-origin redirects never replay it.</>} />
                <TextField label="Follow redirects" value={draft.redirects} onChange={value => set('redirects', value)} error={visibleErrors.redirects} placeholder="5" inputMode="numeric" disabled={saving}
                  hint="0 to 10. 0 checks the first response itself. Never from HTTPS to HTTP." />
              </Disclosure>
              <Disclosure title="Authentication and proxy" summary={authSummary(draft.http)} open={Boolean(open.access)} onToggle={() => toggle('access')}>
                <RequestFields d={draft.http} set={change => set('http', { ...draft.http, ...change })} errors={visibleErrors} prefix="http" disabled={saving}
                  oauth={<OAuthFields d={draft.oauth} set={change => set('oauth', { ...draft.oauth, ...change })} stored={stored?.type === 'http' && stored.http.auth === 'oauth2' ? stored.oauth : undefined} error={visibleErrors.oauth} disabled={saving} />} />
              </Disclosure>
              <Disclosure title="Response content" summary={responseSummary} open={Boolean(open.response)} onToggle={() => toggle('response')}>
                <CheckField label="Keyword" checked={draft.keywordOn} onChange={value => set('keywordOn', value)} disabled={saving} hint="Look for text in the response body (up to 1 MiB is read)." />
                {draft.keywordOn ? (
                  <>
                    <TextField full mono label={draft.keywordRegex ? 'Pattern' : 'Text'} value={draft.keyword} onChange={value => set('keyword', value)} error={visibleErrors.keyword} disabled={saving}
                      hint={draft.keywordRegex ? <>Go RE2 syntax, for example <span className="mono">(?i)status:\s*ok</span>. No lookarounds or backreferences.</> : 'Case-sensitive, exact text.'} />
                    <CheckField full={false} label="Regular expression" checked={draft.keywordRegex} onChange={value => set('keywordRegex', value)} disabled={saving} />
                    <CheckField full={false} label="Fail when found" checked={draft.keywordNegate} onChange={value => set('keywordNegate', value)} disabled={saving} hint="Down if the body matches." />
                  </>
                ) : null}
                <CheckField label="JSON value" checked={draft.jsonOn} onChange={value => set('jsonOn', value)} disabled={saving} hint="Read one value from a JSON response with a simple path. Not JSONata: no wildcards, filters or expressions." />
                {draft.jsonOn ? (
                  <>
                    <TextField mono label="Path" value={draft.jsonPath} onChange={value => set('jsonPath', value)} error={visibleErrors.jsonPath} placeholder="$.status" disabled={saving}
                      hint={<>For example <span className="mono">$.status</span>, <span className="mono">$.items[0].state</span> or <span className="mono">$["odd key"]</span>.</>} />
                    <Seg legend="Condition" name={`${formId}-jmode`} value={draft.jsonMode} onChange={value => set('jsonMode', value)} disabled={saving}
                      options={[{ value: 'equals', label: 'Equals' }, { value: 'exists', label: 'Exists' }]} />
                    {draft.jsonMode === 'equals' ? (
                      <TextField full mono label="Expected value (JSON)" value={draft.jsonExpected} onChange={value => set('jsonExpected', value)} error={visibleErrors.jsonExpected} placeholder={'"ok"'} disabled={saving}
                        hint={<>Compared exactly, including type: <span className="mono">"ok"</span>, <span className="mono">true</span>, <span className="mono">3</span>, <span className="mono">null</span> or an object.</>} />
                    ) : null}
                    <CheckField label="Invert this condition" checked={draft.jsonNegate} onChange={value => set('jsonNegate', value)} disabled={saving}
                      hint={`${draft.jsonMode === 'equals' ? 'Down when the value equals the expected one.' : 'Down when the path exists.'} A body that isn’t valid JSON always fails.`} />
                  </>
                ) : null}
                <CheckField label="JSONata expression" checked={draft.jsonataOn} onChange={value => set('jsonataOn', value)} disabled={saving}
                  hint="Evaluate a JSONata expression on the response and compare its result. Must also pass, together with the conditions above." />
                {draft.jsonataOn ? <JsonataInputs label="JSONata condition" value={draft.jsonata} onChange={value => set('jsonata', value)} input="http" error={visibleErrors.jsonata} disabled={saving} /> : null}
              </Disclosure>
            </>
          ) : null}

          {type === 'websocket' ? (
            <>
              <TextField full mono label="Subprotocols (optional)" value={draft.wsProtocols} onChange={value => set('wsProtocols', value)} error={visibleErrors.wsProtocols} disabled={saving} placeholder="graphql-transport-ws, v2.chat"
                hint="Offered in Sec-WebSocket-Protocol, in this order, separated by commas or spaces. Up to 16 distinct names, matched exactly (case counts). Empty: none is offered, and a server that picks one fails the check." />
              <CheckField label="Require one of them" checked={draft.wsRequire} onChange={value => set('wsRequire', value)} disabled={saving}
                hint={draft.wsRequire ? 'Down unless the server picks exactly one of the offered subprotocols.' : 'Without it, a server may also pick none. Picking one that wasn’t offered, or several, always fails.'} />
              <Disclosure title="Headers, authentication and proxy" summary={`${plural(draft.ws.headers.filter(row => row.name.trim()).length, 'header')} · ${authSummary(draft.ws)}`} open={Boolean(open.ws)} onToggle={() => toggle('ws')}>
                <RequestFields d={draft.ws} set={change => set('ws', { ...draft.ws, ...change })} errors={visibleErrors} prefix="ws" disabled={saving} websocket
                  oauth={<OAuthFields d={draft.wsOauth} set={change => set('wsOauth', { ...draft.wsOauth, ...change })} stored={stored?.type === 'websocket' && stored.ws.auth === 'oauth2' ? stored.wsOauth : undefined} error={visibleErrors.wsOauth} disabled={saving} websocket />} />
              </Disclosure>
            </>
          ) : null}

          {type === 'dns' ? (
            <>
              <div className="fld">
                <label htmlFor={`${formId}-rr`}>Record type</label>
                <select id={`${formId}-rr`} value={draft.dnsRecord} onChange={event => set('dnsRecord', event.target.value as DnsRecord)} disabled={saving}>
                  {DNS_RECORDS.map(record => <option key={record} value={record}>{record}</option>)}
                </select>
              </div>
              <TextField mono label={dnsNeedsServer(draft.dnsRecord) ? 'Resolver' : 'Resolver (optional)'} value={draft.dnsServer} onChange={value => set('dnsServer', value)} error={visibleErrors.dnsServer} placeholder="1.1.1.1:53" disabled={saving}
                hint={dnsNeedsServer(draft.dnsRecord)
                  ? `IP:port, or [IPv6]:port. Required for ${draft.dnsRecord}: each node asks this resolver directly, over UDP and then TCP if the answer is truncated.`
                  : 'IP:port, or [IPv6]:port. Empty uses each node’s system resolver.'} />
              <TextAreaField full mono optional label="Expected records" value={draft.dnsExpected} onChange={value => set('dnsExpected', value)} error={visibleErrors.dnsExpected} rows={3} disabled={saving}
                placeholder={isExtendedRecord(draft.dnsRecord) ? DNS_RECORD_SHAPE[draft.dnsRecord].example : draft.dnsRecord === 'MX' ? '10 mail.example.com' : draft.dnsRecord === 'A' ? '192.0.2.10' : draft.dnsRecord === 'AAAA' ? '2001:db8::10' : draft.dnsRecord === 'TXT' ? 'v=spf1 include:example.net -all' : 'host.example.com'}
                hint={isExtendedRecord(draft.dnsRecord)
                  ? <>One per line, as <span className="mono">{DNS_RECORD_SHAPE[draft.dnsRecord].fields}</span>{draft.dnsRecord === 'CAA' ? <>, the value quoted like <span className="mono">"letsencrypt.org"</span> or unquoted; the tag is compared in lowercase</> : '; names are compared in lowercase, without a trailing dot'}. Every listed record must be in the answer; extra answers are fine. Empty: any answer passes.</>
                  : 'One per line. Every listed record must be in the answer; extra answers are fine. Empty: any answer passes.'} />
            </>
          ) : null}

          {type === 'smtp' ? (
            <Seg legend="TLS mode" name={`${formId}-smtp`} value={draft.smtpMode} onChange={value => set('smtpMode', value)} disabled={saving} full
              options={SMTP_MODES.map(mode => ({ value: mode.mode, label: mode.label }))} hint={SMTP_MODES.find(mode => mode.mode === draft.smtpMode)!.hint} />
          ) : null}

          {tlsOn ? (
            <Disclosure title="TLS and certificate" summary={tlsSummary} open={Boolean(open.tls)} onToggle={() => toggle('tls')}>
              <TextField mono label="Server name (SNI)" value={draft.tlsServerName} onChange={value => set('tlsServerName', value)} error={visibleErrors.tlsServerName} placeholder="Defaults to the target host" disabled={saving}
                hint="Override when the certificate names a different host." />
              <TextField label="Fail when fewer days remain" value={draft.tlsMinDays} onChange={value => set('tlsMinDays', value)} error={visibleErrors.tlsMinDays} placeholder="14" inputMode="numeric" disabled={saving}
                hint="Days of validity the certificate must have left. Empty: only expiry fails." />
              <PemField label="Extra trusted CA certificates" kind="certificate" value={draft.tlsCa} onChange={value => set('tlsCa', value)} error={visibleErrors.tlsCa} disabled={saving}
                hint="Added to the system roots, for private CAs. Hidden from viewers." />
              <PemField label="Client certificate (mutual TLS)" kind="certificate" value={draft.tlsCert} onChange={value => set('tlsCert', value)} error={visibleErrors.tlsCert} disabled={saving} />
              <PemField label="Client private key" kind="key" value={draft.tlsKey} onChange={value => set('tlsKey', value)} error={visibleErrors.tlsKey} disabled={saving}
                hint="Stored on nodes, never shown again here, and hidden from viewers." />
              <CheckField label="Skip certificate verification" checked={draft.tlsIgnore} onChange={value => set('tlsIgnore', value)} disabled={saving} danger
                hint="Doesn’t check the chain or host name, so an impostor could pass. Validity dates and the minimum above still apply, and results show the certificate as unverified. Prefer adding the CA above." />
            </Disclosure>
          ) : hasTlsDraft(draft) && (type === 'http' || type === 'websocket' || type === 'smtp' || type === 'tcp') ? (
            <p className="full muted small"><Icon name="info" className="inline" />TLS settings apply only to encrypted checks{type === 'smtp' ? ' (STARTTLS or implicit TLS)' : type === 'tcp' ? ' (TCP with “Use TLS after connecting”)' : ` (${type === 'websocket' ? 'wss://' : 'https://'})`}. They’ll be removed when you save.</p>
          ) : null}

          <CheckField label="Invert the result" checked={draft.upsideDown} onChange={value => set('upsideDown', value)} disabled={saving}
            hint="Up when the check fails and down when it passes, for things that should stay unreachable. Messages say “Inverted result”. Pending and unknown results aren’t inverted." />

          <h3 className="fsec full">Schedule and retries</h3>
          <DurationField label={type === 'push' ? 'Expect a heartbeat every' : 'Check every'} value={draft.interval} onChange={value => set('interval', value)} error={visibleErrors.interval}
            hint={type === 'push' ? 'The deadline: no up heartbeat within it counts as a failure. Changing it starts a new check.' : '100 ms to 24 h'} disabled={saving} />
          <DurationField label="Timeout" value={draft.timeout} onChange={value => set('timeout', value)} error={visibleErrors.timeout} hint="25 ms to 120 s, at most the interval" units={['ms', 's']} disabled={saving} />
          <TextField label="Retries before down" value={draft.retries} onChange={value => set('retries', value)} error={visibleErrors.retries} inputMode="numeric" disabled={saving}
            hint={retries ? `The first ${plural(retries, 'failed check')} report${retries === 1 ? 's' : ''} pending; failure ${retries + 1} confirms down and alerts.` : '0 to 100. 0: the first failure is down.'} />
          <div className="fld">
            <CheckField full={false} label="Check more often while failing" checked={draft.retryOn} onChange={value => set('retryOn', value)} disabled={saving} />
            {draft.retryOn
              ? <DurationField label="While failing, check every" value={draft.retryInterval} onChange={value => set('retryInterval', value)} error={visibleErrors.retryInterval} hint="100 ms to 24 h. Changing it keeps an open incident." disabled={saving} />
              : <small>Off: failed and pending checks repeat every {fromDraft(draft.interval) ? span(fromDraft(draft.interval)!) : 'interval'}.</small>}
            {draft.retryOn && retryInterval && fromDraft(draft.interval) && retryInterval > fromDraft(draft.interval)! ? <small className="muted">Slower than the normal interval: confirmation takes longer.</small> : null}
          </div>
          <CheckField label="Active" checked={draft.enabled} onChange={value => set('enabled', value)} disabled={saving} hint="Turn off to pause: no node checks it, and no alerts are sent." />

          <h3 className="fsec full">Placement</h3>
          {fleetEnabled ? <FleetPlacementFields nodes={registry.list ?? []} groups={fleet?.groups ?? []} selectedRegistrationIds={fleetTargets} onChange={setFleetTargets} disabled={saving}
            error={showErrors && !fleetTargets.length ? 'Choose at least one registered node.' : undefined} /> : <NodePicker
            legend="Run on" nodes={nodes} selected={draft.nodeIds} onChange={ids => set('nodeIds', ids)} endpoint={endpoint} now={overview.now}
            error={visibleErrors.nodes} disabled={saving}
            hint={type === 'system-service'
              ? 'Each chosen node checks the service on its own operating system, so choose nodes installed on the machines you want to watch. Out-of-view nodes receive the monitor when they reconnect.'
              : type === 'steam'
              ? 'Each chosen node resolves the host and calls the Steam API itself with the key, so every node counts against the key’s daily limit. All of them ask the same directory: more nodes tell a node’s own DNS or API path apart, not the route to the game server. Out-of-view nodes receive the monitor when they reconnect.'
              : type === 'pm2'
              ? 'Each chosen node asks its own PM2 daemon, so choose nodes on the machines that run the process, all reached through the connection and home above. Mixed Linux and Windows machines need separate monitors. Out-of-view nodes receive the monitor when they reconnect.'
              : type === 'tailscale-ping'
              ? 'Each chosen node pings the peer through its own Tailscale client and daemon, so choose nodes on machines that have both and are on the peer’s tailnet. A node that is the peer itself stays pending. Out-of-view nodes receive the monitor when they reconnect.'
              : type === 'real-browser'
              ? 'Each chosen node connects to the browser service at the endpoint itself, so every one must reach it. The page loads from the browser service’s network, not the node’s: with one shared service every node measures from the same place, so give each location its own nearby service under the same name to compare locations. Out-of-view nodes receive the monitor when they reconnect.'
              : type === 'gamedig'
              ? 'Each chosen node sends its queries to the query service at the address above, so every one must reach it. The game server is queried from the service’s network, not the node’s: with one shared service every node measures from the same place, so give each location its own nearby service under the same name to compare locations. Out-of-view nodes receive the monitor when they reconnect.'
              : 'Out-of-view nodes can be chosen; they receive the monitor when they reconnect. More locations tell a service failure from a path problem.'}
          />}
          <div className="fld full">
            <span className="flabel">Notification order if it goes down</span>
            {fleetEnabled ? <small>Existing HA peers can coordinate within their shared placement. Independent nodes run separate copies and may each send alerts. The JSON preview describes node settings; the registered-node selection above is saved separately.</small> : <><CandidateOrder ids={draft.nodeIds} nodes={nodeMap} />
            <small>Selected nodes in ID order. Nodes take turns per destination; a node that sees another’s receipt stands down. Partitions can still produce duplicates.</small></>}
          </div>

          <h3 className="fsec full">Alerts</h3>
          <ChannelPicker selected={draft.channelIds} onChange={ids => setDraft(d => ({ ...d, channelIds: ids, channelsMode: 'chosen' }))} disabled={saving} error={visibleErrors.channels} mode={draft.channelsMode} />
          <Disclosure title="Legacy webhook URL" summary={draft.webhookUrl ? maskedUrl(draft.webhookUrl) : 'None'} open={Boolean(open.legacy)} onToggle={() => toggle('legacy')}>
            <div className="fld full">
              <label htmlFor={`${formId}-hook`}>Webhook URL <span className="muted">(optional)</span></label>
              <div className="secret">
                <input
                  id={`${formId}-hook`} type={showWebhook ? 'url' : 'password'} className="mono" value={draft.webhookUrl} autoComplete="off" spellCheck={false}
                  placeholder="https://hooks.example.com/…" disabled={saving}
                  aria-invalid={visibleErrors.webhook ? true : undefined} aria-describedby={`${formId}-hook-h${visibleErrors.webhook ? ` ${formId}-hook-e` : ''}`}
                  onChange={event => set('webhookUrl', event.target.value)}
                />
                <button type="button" className="icon-btn sm" onClick={() => setShowWebhook(value => !value)} aria-pressed={showWebhook} aria-label={showWebhook ? 'Hide webhook URL' : 'Show webhook URL'}>
                  <Icon name={showWebhook ? 'eyeOff' : 'eye'} />
                </button>
              </div>
              {stored?.webhookUrl && !draft.webhookUrl ? <small className="fwarn"><Glyph tone="warn" />Saving removes the stored webhook.</small> : null}
              <small id={`${formId}-hook-h`}><Icon name="lock" className="inline" />A single JSON webhook from before channels existed. Often contains credentials; stored on nodes and never shown to viewers. Prefer a channel for new alerts.</small>
              <ErrorText id={`${formId}-hook-e`}>{visibleErrors.webhook}</ErrorText>
            </div>
          </Disclosure>
          <div className="fld">
            <CheckField full={false} label="Repeat while down" checked={draft.repeatOn} onChange={value => set('repeatOn', value)} disabled={saving} />
            {draft.repeatOn ? <DurationField label="Repeat every" value={draft.repeat} onChange={value => set('repeat', value)} error={visibleErrors.repeat} units={['s', 'min', 'h']} disabled={saving} /> : <small>Off: one down alert and one recovery per incident and destination.</small>}
          </div>
          {!draft.channelIds.length && !draft.webhookUrl.trim() ? <p className="fld muted small"><Icon name="info" className="inline" />No destination: checks and incidents are recorded without notifications.</p> : <span />}

          <h3 className="fsec full">Organize</h3>
          <TagEditor rows={draft.tags} onChange={rows => set('tags', rows)} disabled={saving} error={visibleErrors.tags} />
          {groups !== null ? (
            <div className="fld full">
              <label htmlFor={`${formId}-group`}>Group <span className="muted">(optional)</span></label>
              <select id={`${formId}-group`} value={draft.groupId} onChange={event => set('groupId', event.target.value)} disabled={saving} aria-describedby={`${formId}-group-h`}>
                <option value="">No group</option>
                {groupList.map(view => ({ view, path: groupPath(view.group.id, groupMap) })).sort((a, b) => a.path.localeCompare(b.path)).map(({ view, path }) => <option key={view.group.id} value={view.group.id}>{path}</option>)}
                {draft.groupId && !group ? <option value={draft.groupId}>Missing group ({draft.groupId.slice(0, 8)})</option> : null}
              </select>
              <small id={`${formId}-group-h`}>
                {!draft.groupId ? <>Groups let one outage alert stand in for its dependent monitors. <a href={href('groups')}>Manage groups</a></>
                  : !group ? 'This group no longer exists. Alerts are sent normally.'
                  : group.group.outageMonitorId === current?.id ? 'This monitor is the group’s outage monitor. Its own alerts are never held back by its group.'
                  : group.group.outageMonitorId ? <>While <b>{monitorMap.get(group.group.outageMonitorId)?.monitor.name ?? 'its outage monitor'}</b> is down and its own alert reached a destination, this monitor’s alerts to that destination are held back after the group’s grace period. Other destinations fail open. Checks and incidents are still recorded.</>
                  : 'This group has no outage monitor yet, so nothing is held back.'}
              </small>
            </div>
          ) : null}

          <Disclosure title="JSON (advanced)" summary="Preview, copy or fill from JSON" open={Boolean(open.json)} onToggle={() => toggle('json')}>
            <JsonPanel input={input} onImport={importJson} disabled={saving} />
          </Disclosure>

          {visibleErrors.size ? <div className="full"><ErrorText id={`${formId}-size-e`}>{visibleErrors.size}</ErrorText></div> : null}
          {changes.length ? (
            <div className={`full gen-note${openIncident ? ' warn' : ''}`} role="note">
              <Glyph tone={openIncident ? 'warn' : 'none'} />
              <span>
                {openIncident
                  ? <><b>Saving closes the open incident without a recovery alert.</b> You changed the {checkChangeWords(changes, type).join(', ')}, so this becomes a new check. The incident opened <Time at={openIncident.openedAt} now={overview.now} /> ends as “closed after config change”; a new one opens only if the new check fails.</>
                  : <>Saving starts a new check generation because you changed the {checkChangeWords(changes, type).join(', ')}. Name, interval, retry spacing, destinations, tags and group changes don’t.</>}
              </span>
            </div>
          ) : null}
          <div className="full form-acts">
            <button type="submit" className="btn primary" disabled={saving || conflicted}>
              <Icon name={current || fleetBase ? 'check' : 'plus'} />{saving ? `Saving to ${endpoint}…` : current || fleetBase ? 'Save changes' : 'Create monitor'}
            </button>
            <a className="btn" href={current ? href('monitors', current.id) : copy && source ? href('monitors', source.monitor.id) : href('monitors')}>Cancel</a>
            {saving ? <span className="muted small" role="status">Waiting for {endpoint} to acknowledge…</span> : null}
          </div>
        </form>
      </Card>
    </div>
  );
}

