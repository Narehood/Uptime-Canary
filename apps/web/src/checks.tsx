import type { ReactNode } from 'react';
import type { CertificateSummary, HTTPMonitorOptions, Monitor, MonitorInput, MonitorType, RequestCredentials } from '@uptime-canary/shared';
import { browserCanonical, browserFacts } from './browser';
import { maskedUrl, plural, span } from './format';
import { gamedigCanonical, gamedigFacts } from './gamedig';
import { gpCanonical, gpFacts } from './globalping';
import { pm2Canonical, pm2Facts } from './pm2';
import { SERVICE_PROTOCOL, serviceFacts } from './serviceSpecs';
import { steamCanonical, steamFacts } from './steam';
import { systemdUnit } from './systemService';
import { tailscaleFacts } from './tailscale';

export type CheckCategory = 'web' | 'remote' | 'game' | 'local' | 'data' | 'messaging' | 'infra' | 'manual';

export interface CheckType {
  type: MonitorType;
  label: string;
  /** Short code for compact tags. */
  short: string;
  hint: string;
  category: CheckCategory;
  /** Network checks take their target from one field; service checks build it from their options. */
  targetLabel?: string;
  placeholder?: string;
  targetHint?: string;
}

export const CATEGORY_LABEL: Record<CheckCategory, string> = {
  web: 'Web and network',
  remote: 'Remote probes (third party)',
  game: 'Game servers',
  local: 'On each node’s own system',
  data:'Databases and caches',
  messaging: 'Messaging and containers',
  infra: 'Infrastructure and RPC',
  manual: 'Passive and operator controlled',
};

/** Every check type the node implements, in the order the form offers them. */
export const CHECK_TYPES: CheckType[] = [
  { type: 'http', label: 'HTTP(S)', short: 'http', category: 'web', hint: 'Status, keyword or JSON', targetLabel: 'URL', placeholder: 'https://service.example.com/health', targetHint: 'An http:// or https:// address without credentials or #fragment.' },
  {
    type: 'real-browser', label: 'Real browser', short: 'browser', category: 'web', hint: 'Page load in your Chromium service', targetLabel: 'Page URL', placeholder: 'https://app.example.com/',
    targetHint: 'An http:// or https:// address without credentials or #fragment. The browser service below loads it, not the node.',
  },
  { type: 'ping', label: 'Ping', short: 'ping', category: 'web', hint: 'ICMP echo reply', targetLabel: 'Host or IP address', placeholder: 'gateway.example.net or 192.0.2.10', targetHint: 'A host name, IPv4 or IPv6 address. Nodes need permission to send ICMP; without it results are unknown, never up or down.' },
  { type: 'tcp', label: 'TCP port', short: 'tcp', category: 'web', hint: 'Opens a connection', targetLabel: 'Host and port', placeholder: 'db.example.net:5432', targetHint: 'host:port, or [IPv6]:port. Up when the connection opens.' },
  { type: 'tls', label: 'TLS', short: 'tls', category: 'web', hint: 'Handshake and certificate', targetLabel: 'Host and port', placeholder: 'service.example.com:443', targetHint: 'host:port. Up when a TLS 1.2+ handshake succeeds and the certificate is valid.' },
  { type: 'dns', label: 'DNS', short: 'dns', category: 'web', hint: 'Resolves a record', targetLabel: 'Name to look up', placeholder: 'example.com', targetHint: 'A domain name, like _sip._tcp.example.com for SRV. For PTR, the IP address to look up.' },
  { type: 'websocket', label: 'WebSocket', short: 'ws', category: 'web', hint: 'Upgrade and ping/pong', targetLabel: 'URL', placeholder: 'wss://stream.example.com/socket', targetHint: 'A ws:// or wss:// address. Up when the upgrade succeeds and a ping gets its pong.' },
  { type: 'smtp', label: 'SMTP', short: 'smtp', category: 'web', hint: 'Greeting, EHLO, TLS', targetLabel: 'Host and port', placeholder: 'mail.example.com:587', targetHint: 'host:port. Sends no login and no mail: greeting, EHLO and the chosen TLS mode only.' },
  { type: 'globalping', label: 'Globalping', short: 'gp', category: 'remote', hint: 'Ping, HTTP or DNS from a probe elsewhere' },
  { type: 'steam', label: 'Steam game server', short: 'steam', category: 'game', hint: 'Listed in Steam’s server directory' },
  { type: 'gamedig', label: 'GameDig game server', short: 'gamedig', category: 'game', hint: 'Game query through your GameDig service' },
  {
    type: 'system-service', label: 'System service', short: 'svc', category: 'local', hint: 'systemd or Windows service state', targetLabel: 'Internal service name', placeholder: 'sshd.service or Spooler',
    targetHint: 'The internal name, not the display name: sshd or sshd.service for systemd, Spooler (not “Print Spooler”) on Windows. Letters, digits, dots, underscores, hyphens and @; no spaces, paths or patterns.',
  },
  { type: 'pm2', label: 'PM2 process', short: 'pm2', category: 'local', hint: 'Named process online in the local PM2' },
  {
    type: 'tailscale-ping', label: 'Tailscale ping', short: 'ts', category: 'local', hint: 'Peer ping through the node’s Tailscale client', targetLabel: 'Peer name or Tailscale address', placeholder: 'build-server or 100.101.102.103',
    targetHint: 'A MagicDNS name such as build-server or build-server.example-tailnet.ts.net, or the peer’s Tailscale IPv4 or IPv6 address. One name or address only: no URL, port, brackets, zone, spaces or options.',
  },
  { type: 'postgres', label: 'PostgreSQL', short: 'pg', category: 'data', hint: 'Read-only query' },
  { type: 'mysql', label: 'MySQL', short: 'mysql', category: 'data', hint: 'Read-only query' },
  { type: 'sqlserver', label: 'SQL Server', short: 'mssql', category: 'data', hint: 'Read-only query' },
  { type: 'oracledb', label: 'Oracle', short: 'oracle', category: 'data', hint: 'SELECT in a worker' },
  { type: 'mongodb', label: 'MongoDB', short: 'mongo', category: 'data', hint: 'Ping and optional find' },
  { type: 'redis', label: 'Redis', short: 'redis', category: 'data', hint: 'AUTH, SELECT, PING' },
  { type: 'docker', label: 'Docker', short: 'docker', category: 'messaging', hint: 'Container state' },
  { type: 'mqtt', label: 'MQTT', short: 'mqtt', category: 'messaging', hint: 'Subscribe and match' },
  { type: 'rabbitmq', label: 'RabbitMQ', short: 'rabbit', category: 'messaging', hint: 'AMQP or alarms API' },
  { type: 'kafka-producer', label: 'Kafka', short: 'kafka', category: 'messaging', hint: 'Metadata or produce' },
  { type: 'grpc', label: 'gRPC', short: 'grpc', category: 'infra', hint: 'Health or unary call' },
  { type: 'sftp', label: 'SFTP', short: 'sftp', category: 'infra', hint: 'SSH login, pinned key' },
  { type: 'ntp', label: 'NTP', short: 'ntp', category: 'infra', hint: 'Stratum and offset' },
  { type: 'snmp', label: 'SNMP', short: 'snmp', category: 'infra', hint: 'GET one OID' },
  { type: 'radius', label: 'RADIUS', short: 'radius', category: 'infra', hint: 'Access-Request' },
  { type: 'sip-options', label: 'SIP', short: 'sip', category: 'infra', hint: 'OPTIONS request' },
  { type: 'push', label: 'Push', short: 'push', category: 'manual', hint: 'Your job sends heartbeats' },
  { type: 'manual', label: 'Manual', short: 'manual', category: 'manual', hint: 'You set the status' },
];

const TYPE_MAP = new Map(CHECK_TYPES.map(entry => [entry.type as string, entry]));
export const checkType = (type: string): CheckType | undefined => TYPE_MAP.get(type);
export const typeLabel = (type: string): string => TYPE_MAP.get(type)?.label ?? type.toUpperCase();
export const typeShort = (type: string): string => TYPE_MAP.get(type)?.short ?? type;
/** Network types: the target is one address the operator types. */
export const NETWORK_TYPES = new Set<MonitorType>(['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp']);
/**
 * Types whose target is a single field the operator types: an address, the name of a service on each node, a Tailscale
 * peer that each node's own client pings, or the page a browser service loads (both protocol 19).
 */
export const TYPED_TARGET_TYPES = new Set<MonitorType>([...NETWORK_TYPES, 'system-service', 'tailscale-ping', 'real-browser']);
export const isServiceType = (type: string): boolean => type in SERVICE_PROTOCOL;

export const SMTP_MODES = [
  { mode: 'plain', label: 'Plain', hint: 'No TLS, usually port 25' },
  { mode: 'starttls', label: 'STARTTLS', hint: 'Upgrades after EHLO, usually 587' },
  { mode: 'implicit', label: 'Implicit TLS', hint: 'TLS from the start, usually 465' },
] as const;
export type SmtpMode = (typeof SMTP_MODES)[number]['mode'];
/** The record types the node resolves; SRV, SOA and CAA are protocol 19 (SOA and CAA ask an explicit resolver). */
export const DNS_RECORDS = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'PTR', 'SRV', 'SOA', 'CAA'] as const;
export type DnsRecord = (typeof DNS_RECORDS)[number];

/**
 * TLS options apply only to encrypted checks: HTTPS, TLS, WSS, SMTP with STARTTLS or implicit TLS, and (protocol 19) a TCP
 * check with TLS turned on, which then completes a verified handshake. Without `tcpTls` a TCP check stays plain.
 */
export function tlsApplies(type: MonitorType, target: string, smtpMode: SmtpMode | undefined, tcpTls = false): boolean {
  const scheme = target.trim().toLowerCase();
  switch (type) {
    case 'http': return scheme.startsWith('https://');
    case 'websocket': return scheme.startsWith('wss://');
    case 'tls': return true;
    case 'smtp': return Boolean(smtpMode && smtpMode !== 'plain');
    case 'tcp': return tcpTls;
    default: return false;
  }
}

/** Runtime-owned fields that management input never carries. */
const RUNTIME_FIELDS = new Set(['id', 'revision', 'updatedAt', 'deleted', 'checkGeneration', 'pushStartedAt']);

/**
 * The writable part of a monitor, for PUT requests that change one field (pause/resume, manual status) and as
 * the base of edits. Everything except runtime-owned fields is kept, including option objects and fields this
 * dashboard doesn't model, so a round trip never drops configuration. Nulls are omitted: the node rejects null
 * for optional fields.
 */
export function inputOf(monitor: Monitor): MonitorInput {
  const input: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(monitor)) {
    if (RUNTIME_FIELDS.has(key) || value === null || value === undefined) continue;
    input[key] = typeof value === 'object' ? structuredClone(value) : value;
  }
  input.nodeIds = [...monitor.nodeIds];
  return input as unknown as MonitorInput;
}

/**
 * The input for a new monitor copied from `source`, for the clone form to show before anything is created. Every setting
 * comes along, private ones included (credentials, headers, bodies, certificates and keys, API keys and tokens, a PM2
 * home), and so does every option this dashboard doesn't model, all deep-copied so the source never changes. Runtime
 * fields are dropped, so the node gives the copy its own ID, revision and check generation. A push monitor gets a new
 * token from `newToken`: two monitors never share one heartbeat credential. The copy's destinations are always explicit:
 * a source without channels (stored without `notificationIds`, Go's omitempty) is copied as `[]`, so the node never adds
 * its default channels to a copy (protocol 19 fills defaults only when a create omits the list).
 */
export function cloneInput(source: Monitor, newToken: () => string): MonitorInput {
  const input = inputOf(source);
  input.name = `${source.name} (copy)`;
  if (input.push) input.push = { ...input.push, token: newToken() };
  if (!Array.isArray(input.notificationIds)) input.notificationIds = [];
  return input;
}

/**
 * Mirrors the node's `sameCheck`: these fields define what is checked. Changing any of them starts a new check
 * generation, which closes an open incident administratively, without a recovery alert. Name, schedule interval,
 * retry spacing, alert destinations, repeat, group and manual status keep the incident.
 */
const CHECK_FIELDS = ['type', 'target', 'timeoutMs', 'enabled', 'nodeIds', 'expectedStatusCodes', 'upsideDown', 'retries', 'http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'push', 'globalping', 'steam', 'pm2', 'browser', 'gamedig'] as const;
const norm = (key: string, value: unknown): unknown => {
  if (key === 'nodeIds' && Array.isArray(value)) return [...value].sort();
  if ((key === 'upsideDown' || key === 'enabled') && !value) return false;
  if (key === 'retries' && !value) return 0;
  if (key === 'expectedStatusCodes' && Array.isArray(value) && !value.length) return undefined;
  // The node compares Globalping options through Go's omitempty encoding.
  if (key === 'globalping') return value === undefined || value === null ? undefined : gpCanonical(value);
  // Every Steam option counts, the API key included; the comparison follows Go's omitempty encoding.
  if (key === 'steam') return value === undefined || value === null ? undefined : steamCanonical(value);
  // The connection, home and namespace all count; an empty home or namespace equals an absent one, as in Go's encoding.
  if (key === 'pm2') return value === undefined || value === null ? undefined : pm2Canonical(value);
  // The endpoint, token, screenshot setting and wait all count; an empty token, no screenshot and no wait equal absent ones.
  if (key === 'browser') return value === undefined || value === null ? undefined : browserCanonical(value);
  // The game, port, game token, service address and token and both flags count; port 0, false and empty equal absent ones.
  if (key === 'gamedig') return value === undefined || value === null ? undefined : gamedigCanonical(value);
  return value ?? undefined;
};
export function checkChanges(before: MonitorInput, after: MonitorInput): string[] {
  const service = isServiceType(after.type) && before.type === after.type;
  const changed: string[] = CHECK_FIELDS.filter(key => {
    // The node derives service targets from their options; compare the options instead. Other targets, including a
    // system service's name, compare exactly as stored: `sshd` → `sshd.service` is a new check, as on the node.
    if (key === 'target' && service) return false;
    return JSON.stringify(norm(key, before[key])) !== JSON.stringify(norm(key, after[key]));
  });
  // The effective heartbeat deadline defines the check, matching sameCheck on the node.
  if (after.type === 'push' && before.type === 'push') {
    const interval = (m: MonitorInput): number => m.internalOnly ? m.intervalMs : Math.max(60_000, m.intervalMs);
    if (interval(before) !== interval(after)) changed.push('intervalMs');
  }
  return changed;
}
const CHECK_FIELD_WORDS: Record<string, string> = {
  type: 'check type', target: 'target', timeoutMs: 'timeout', enabled: 'active state', nodeIds: 'locations', expectedStatusCodes: 'accepted statuses',
  upsideDown: 'inverted result', retries: 'retry count', http: 'HTTP options', tls: 'TLS options', dns: 'DNS options', websocket: 'WebSocket options',
  smtp: 'SMTP options', service: `service options`, ping: 'ping options', push: 'push token or GET setting', intervalMs: 'heartbeat interval', globalping: 'Globalping options',
  steam: 'Steam API key or options', pm2: 'PM2 connection, home or namespace', browser: 'browser service, token, wait or screenshot setting',
  gamedig: 'game, port, game token or query service',
};
/** `type` is the edited monitor's type, for words that depend on it: a system service's target is its service name. */
export const checkChangeWords = (keys: string[], type?: string): string[] =>
  keys.map(key => key === 'target' && type === 'system-service' ? 'service name' : key === 'target' && type === 'steam' ? 'server address' : key === 'target' && type === 'pm2' ? 'process name'
    : key === 'target' && type === 'tailscale-ping' ? 'peer name or address' : key === 'target' && type === 'real-browser' ? 'page URL'
    : key === 'target' && type === 'gamedig' ? 'game server address' : CHECK_FIELD_WORDS[key] ?? key);

/** "200–299, 301" from a list of codes. */
export function statusCodesText(codes: number[] | undefined): string {
  if (!codes?.length) return '';
  const sorted = [...new Set(codes)].sort((a, b) => a - b);
  const parts: string[] = [];
  let start = sorted[0]!, prev = start;
  for (const code of [...sorted.slice(1), Number.NaN]) {
    if (code === prev + 1) { prev = code; continue; }
    parts.push(start === prev ? String(start) : `${start}–${prev}`);
    start = prev = code;
  }
  return parts.join(', ');
}
export const rangesText = (ranges: { min: number; max: number }[] | undefined): string =>
  (ranges ?? []).map(range => range.min === range.max ? String(range.min) : `${range.min}–${range.max}`).join(', ');

/** What a monitor accepts as a passing HTTP status, in words. */
export function acceptedStatusText(monitor: Monitor): string {
  if (monitor.http?.statusRanges?.length) return rangesText(monitor.http.statusRanges);
  if (monitor.expectedStatusCodes?.length) return statusCodesText(monitor.expectedStatusCodes);
  return 'Any 2xx (default)';
}

const bytesText = (text: string): string => {
  const n = new TextEncoder().encode(text).length;
  return n < 1024 ? `${n} B` : `${(n / 1024).toLocaleString(undefined, { maximumFractionDigits: 1 })} KiB`;
};
export const pemCount = (pem: string | undefined): number => (pem?.match(/-----BEGIN [^-]*CERTIFICATE-----/gu) ?? []).length;

export interface Fact { label: string; value: ReactNode }

export const Hidden = () => <span className="muted">Hidden from viewers</span>;
export const Secret = () => <span className="muted">set · hidden</span>;

function requestFacts(o: RequestCredentials | undefined, viewer: boolean, reveal: boolean): Fact[] {
  if (viewer) return [{ label: 'Headers, credentials and proxy', value: <Hidden /> }];
  const facts: Fact[] = [];
  const headers = Object.entries(o?.headers ?? {});
  facts.push({
    label: 'Headers',
    value: headers.length
      ? <span className="hdrs">{headers.map(([name, value]) => <span key={name} className="mono"><b>{name}</b>: {reveal ? value : '•••'}</span>)}</span>
      : <span className="muted">None</span>,
  });
  facts.push({
    label: 'Authentication',
    value: (o as HTTPMonitorOptions | undefined)?.oauth2 ? 'OAuth2 client credentials (below)'
      : o?.basicAuth ? <>Basic · user <span className="mono">{o.basicAuth.username || '(empty)'}</span>, password <Secret /></>
      : o?.bearerToken ? <>Bearer token <Secret /></>
      : headers.some(([name]) => name.toLowerCase() === 'authorization') ? 'Authorization header'
      : <span className="muted">None</span>,
  });
  facts.push({
    label: 'Proxy',
    value: o?.proxyUrl ? <span className="mono wrap">{reveal ? o.proxyUrl : maskedUrl(o.proxyUrl)}</span>
      : o?.useEnvironmentProxy ? 'The node’s environment proxy settings'
      : <span className="muted">Direct</span>,
  });
  return facts;
}

/** OAuth2 client-credentials facts for an HTTP or WebSocket check, for editors only; the client secret stays hidden. */
function oauthFacts(oauth: HTTPMonitorOptions['oauth2'] | undefined, reveal: boolean): Fact[] {
  if (!oauth) return [];
  const facts: Fact[] = [];
  facts.push({ label: 'OAuth2 token endpoint', value: <span className="mono wrap">{reveal ? oauth.tokenUrl || '(empty)' : oauth.tokenUrl ? maskedUrl(oauth.tokenUrl) : '(empty)'}</span> });
  facts.push({ label: 'OAuth2 client', value: <><span className="mono wrap">{oauth.clientId || '(empty)'}</span> · secret {oauth.clientSecret ? <Secret /> : <span className="muted">empty</span>} · {oauth.authMethod === 'client_secret_post' ? 'sent in the form body' : 'Basic header'}</> });
  if (oauth.scope || oauth.audience) facts.push({ label: 'OAuth2 grant', value: <>{oauth.scope ? <>scope <span className="mono wrap">{oauth.scope}</span></> : null}{oauth.scope && oauth.audience ? ' · ' : null}{oauth.audience ? <>audience <span className="mono wrap">{oauth.audience}</span></> : null}</> });
  facts.push({ label: 'OAuth2 trust', value: <>{oauth.tokenCaPem ? `System roots + ${plural(pemCount(oauth.tokenCaPem), 'custom CA certificate')}` : 'System roots'}{oauth.allowInsecureHttp ? <> · <span className="warn-text">plain HTTP allowed</span></> : null}</> });
  return facts;
}

/**
 * Plain-language description of what a monitor checks. Viewers receive monitors with headers, bodies,
 * credentials, proxies, PEM data and service options removed by the node, so those rows say "Hidden from
 * viewers" instead of implying nothing is configured.
 */
export function checkFacts(m: Monitor, { viewer, reveal }: { viewer: boolean; reveal: boolean }): Fact[] {
  const facts: Fact[] = [];
  if (m.type === 'http') {
    const h = m.http;
    const method = h?.method || 'GET';
    facts.push({ label: 'Request', value: <><span className="mono">{method}</span>{h?.maxRedirects === 0 ? ' · redirects not followed' : ` · follows up to ${plural(h?.maxRedirects ?? 5, 'redirect')}`}{h?.maxRedirects === undefined ? ' (default)' : ''}</> });
    facts.push(...requestFacts(h, viewer, reveal));
    if (!viewer) facts.push(...oauthFacts(h?.oauth2, reveal));
    if (!viewer) facts.push({ label: 'Body', value: h?.body ? (reveal ? <pre className="body-pre">{h.body}</pre> : <>{bytesText(h.body)} <span className="muted">· hidden</span></>) : <span className="muted">None</span> });
    facts.push({ label: 'Accepts status', value: <span className="mono">{acceptedStatusText(m)}</span> });
    const keyword = h?.keyword;
    if (keyword) {
      facts.push({ label: keyword.negate ? 'Body must not match' : 'Body must match', value: <>{keyword.regex ? 'Regular expression ' : 'Text '}<span className="mono wrap">{keyword.value}</span></> });
    }
    if (viewer) facts.push({ label: 'JSONata condition', value: <span className="muted">Hidden from viewers, if configured</span> });
    else if (h?.jsonata?.expression) facts.push({ label: 'JSONata condition', value: <><span className="mono wrap">{h.jsonata.expression}</span> {h.jsonata.operator} <span className="mono wrap">{JSON.stringify(h.jsonata.expected)}</span> <span className="muted small">· evaluated by the node’s isolated worker</span></> });
    const json = h?.json;
    if (json) {
      facts.push({
        label: 'JSON condition',
        value: <><span className="mono">{json.path}</span> {json.existsOnly ? (json.negate ? 'must not exist' : 'must exist') : <>{json.negate ? 'must not equal' : 'must equal'} <span className="mono wrap">{json.expected ?? 'null'}</span></>}</>,
      });
    }
  }
  if (m.type === 'ping') {
    const p = m.ping;
    facts.push({ label: 'Echo requests', value: <>{plural(p?.count || 1, 'packet')} · {p?.size ?? 32} byte payload · {p?.family === 'ipv4' ? 'IPv4 only' : p?.family === 'ipv6' ? 'IPv6 only' : 'IPv4 or IPv6'}</> });
    facts.push({ label: 'Up when', value: 'At least one verified reply arrives within the timeout' });
  }
  if (m.type === 'websocket') {
    const w = m.websocket;
    facts.push(...requestFacts(w, viewer, reveal));
    // OAuth2 credentials are private: never rendered for a viewer, whatever the node sends.
    if (!viewer) facts.push(...oauthFacts(w?.oauth2, reveal));
    const offered = w?.subprotocols ?? [];
    facts.push({
      label: 'Subprotocols',
      value: offered.length
        ? <><span className="hdrs">{offered.map(protocol => <span key={protocol} className="mono wrap">{protocol}</span>)}</span> <span className="muted small">· offered in this order; {w?.requireSubprotocol ? 'the server must choose one of them' : 'the server may choose one of them or none'}, and choosing any other fails the check</span></>
        : <span className="muted">None offered; a server that chooses one fails the check</span>,
    });
  }
  if (m.type === 'dns') {
    const d = m.dns;
    const record = d?.recordType ?? 'A';
    facts.push({ label: 'Record', value: <span className="mono">{record}</span> });
    facts.push({
      label: 'Resolver',
      value: d?.server ? <><span className="mono">{d.server}</span>{record === 'SOA' || record === 'CAA' ? <span className="muted small"> · asked directly over UDP, then TCP if the answer is truncated</span> : null}</>
        : record === 'SOA' || record === 'CAA' ? <span className="warn-text">Missing: {record} checks need an explicit resolver, so the node refuses this setting</span>
        : 'The node’s system resolver',
    });
    facts.push({ label: 'Expects', value: d?.expected?.length ? <span className="hdrs">{d.expected.map(value => <span key={value} className="mono wrap">{value}</span>)}</span> : 'Any answer' });
  }
  if (m.type === 'tcp') {
    facts.push({ label: 'Connection', value: m.tls ? 'TCP, then a TLS 1.2+ handshake with the certificate verified (settings below)' : <>TCP only <span className="muted small">· up when the connection opens; nothing is sent</span></> });
  }
  if (m.type === 'smtp') {
    const mode = m.smtp?.tlsMode ?? 'plain';
    facts.push({ label: 'TLS mode', value: SMTP_MODES.find(entry => entry.mode === mode)?.label ?? mode });
  }
  if (m.type === 'manual') {
    const state = m.manual?.state;
    facts.push({ label: 'Status set to', value: state ? <b>{state === 'up' ? 'Up' : state === 'down' ? 'Down' : 'Pending'}</b> : <span className="muted">Not set (pending)</span> });
    facts.push({ label: 'Message', value: m.manual?.message ? <span className="wrap">{m.manual.message}</span> : <span className="muted">None</span> });
    facts.push({ label: 'Network', value: 'No request is made. An editor sets the result.' });
  }
  if (m.type === 'push') {
    facts.push({ label: 'Push token', value: viewer || !m.push?.token ? <Hidden /> : reveal ? <span className="mono wrap">{m.push.token}</span> : <Secret /> });
    facts.push({ label: 'Accepts', value: m.push?.allowGet ? 'POST, and GET with the token in the URL' : 'POST with an Authorization: Push header' });
    facts.push({ label: 'Down when', value: <>No up heartbeat arrives within {span(m.intervalMs)}, or a heartbeat reports down</> });
  }
  if (m.tls) {
    const t = m.tls;
    if (t.serverName) facts.push({ label: 'TLS server name', value: <span className="mono">{t.serverName}</span> });
    facts.push({
      label: 'Trusts',
      value: viewer ? <>System roots · custom CAs <Hidden /></> : t.caPem ? <>System roots + {plural(pemCount(t.caPem), 'custom CA certificate')}</> : 'System roots',
    });
    if (viewer) facts.push({ label: 'Client certificate', value: <Hidden /> });
    else if (t.clientCertPem) facts.push({ label: 'Client certificate', value: <>Configured (mutual TLS) · key <Secret /></> });
    if (t.minValidityDays) facts.push({ label: 'Certificate must last', value: `At least ${plural(t.minValidityDays, 'more day')}` });
    if (t.ignoreVerification) facts.push({ label: 'Verification', value: <span className="warn-text">Off: chain and host name aren’t verified. Dates still are.</span> });
  }
  if (m.type === 'system-service') {
    facts.push({ label: 'Read from', value: <>Each assigned node’s own operating system, never another host: systemd on Linux (unit <span className="mono wrap">{systemdUnit(m.target)}</span>), the Service Control Manager on Windows (<span className="mono wrap">{m.target}</span>). Read-only; no credentials.</> });
    facts.push({ label: 'Up when', value: 'The service is running (systemd: active)' });
    facts.push({ label: 'Down when', value: 'The service manager reports it stopped, or the service isn’t installed' });
    facts.push({ label: 'Pending when', value: 'The node can’t read its service manager: none present (as in an ordinary container), no permission, an unsupported OS, no free check worker, or no answer within the timeout. Pending never recovers an outage.' });
    facts.push({ label: 'Latency', value: <span className="muted">Not measured; shown as a dash</span> });
  }
  if (isServiceType(m.type)) facts.push(...serviceFacts(m, viewer, reveal));
  if (m.type === 'globalping') facts.push(...gpFacts(m, viewer, reveal));
  if (m.type === 'steam') facts.push(...steamFacts(m, viewer, reveal));
  if (m.type === 'pm2') facts.push(...pm2Facts(m, viewer, reveal));
  if (m.type === 'tailscale-ping') facts.push(...tailscaleFacts(m));
  if (m.type === 'real-browser') facts.push(...browserFacts(m, viewer, reveal));
  if (m.type === 'gamedig') facts.push(...gamedigFacts(m, viewer, reveal));
  if (m.retries) facts.push({ label: 'Retries', value: <>{plural(m.retries, 'failed check')} tolerated as pending before down{m.retryIntervalMs ? <>, spaced {span(m.retryIntervalMs)}</> : null}</> });
  else if (m.retryIntervalMs && m.retryIntervalMs !== m.intervalMs) facts.push({ label: 'While failing', value: <>Checks every {span(m.retryIntervalMs)}</> });
  if (m.upsideDown) facts.push({ label: 'Result', value: <span className="warn-text">Inverted: up when the check fails</span> });
  return facts;
}

/** Days left on a certificate as of the node's own clock, rounded down like the node does. */
export const certificateDaysLeft = (certificate: CertificateSummary, now: number): number =>
  Math.floor((Date.parse(certificate.notAfter) - now) / 86_400_000);
