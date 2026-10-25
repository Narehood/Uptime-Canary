import type {
  AccountInput, AccountView, AuthenticationStatus, AuthenticatorEnrollment, Group, GroupInput, GroupView, Monitor, MonitorInput,
  MonitorView, Observation, Overview, PasswordLoginInput, PasswordLoginResult, Role,
  LocalAvailabilityHistory, LocalHistoryStatistics, NotificationChannel, NotificationChannelInput, NotificationChannelView,
  DeliveryAttemptView, Maintenance, MaintenanceInput, MaintenanceView,
  StatusPage, StatusPageInput, StatusPageView, PublicStatusPage, Tag, TagInput, TagView,
  DashboardOwnerInput, NodeVerification, NodeVerificationInput, RegisteredNode, BrowserScreenshot,
} from '@uptime-canary/shared';
import { readAvailability } from './availability';
import { controlPlaneMode, gatewayMode, pageOrigin } from './gateway';
import { readScreenshot } from './screenshot';

const REQUEST_TIMEOUT_MS = 15_000;
const SIGN_OUT_TIMEOUT_MS = 5_000;
/** Enrollment, retry and removal write to the node through the dashboard's server, so they get longer. */
const NODE_CHANGE_TIMEOUT_MS = 30_000;
const ERROR_BODY_LIMIT = 4_096;

/**
 * Control plane: names the saved registration a monitoring request is for. The dashboard's server resolves it to the
 * stored address and the signed-in role's credential; the browser never sends a node address or a node credential.
 */
export const REGISTERED_NODE_HEADER = 'X-Canary-Registered-Node';
const REGISTRATION_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u;

/** A registration ID the dashboard issued; anything else could never select a node, so it never leaves the page. */
export function checkRegistration(id: string): string {
  if (!REGISTRATION_ID.test(id)) throw new Error('That isn’t a node registration on this dashboard.');
  return id;
}
/** The node's management API rejects monitor and group request bodies above 256 KiB with 413. */
export const API_BODY_LIMIT = 262_144;

export class ApiFailure extends Error {
  /** `detail` is the node's own `{ error }` text, sanitized; nodes never put secrets in it. */
  constructor(public readonly status: number, message: string, public readonly detail?: string) {
    super(message);
    this.name = 'ApiFailure';
  }
}

export function normalizeApiUrl(input: string): string {
  let url: URL;
  try { url = new URL(input.trim()); }
  catch { throw new Error('Enter a complete HTTP or HTTPS node address.'); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new Error('Use a node base address without credentials, a path, or query parameters.');
  }
  return url.origin;
}

/** Account tokens are printable ASCII without spaces; anything else could never authenticate. */
export function checkToken(token: string): string | null {
  if (!token) return 'Enter the account token for this node.';
  if (/[^\x21-\x7e]/u.test(token)) return 'Tokens contain only printable characters, without spaces.';
  return null;
}

/** Mirrors the node's password rule: 12 to 128 characters, without NUL or line breaks. */
export function passwordProblem(password: string): string | undefined {
  const length = [...password].length;
  if (!password) return 'Enter a password.';
  if (length < 12) return `Use at least 12 characters (${length} so far).`;
  if (length > 128) return 'Use at most 128 characters.';
  if (/[\0\r\n]/u.test(password)) return 'Passwords can’t contain line breaks.';
  return undefined;
}

/** Six digits from an authenticator app; spaces are tolerated while typing and removed. */
export const cleanCode = (code: string): string => code.replace(/\s+/gu, '');
export const codeProblem = (code: string): string | undefined => /^\d{6}$/u.test(cleanCode(code)) ? undefined : 'Enter the 6-digit code from your authenticator app.';

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const list = <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : [];
const isRole = (value: unknown): value is Role => value === 'admin' || value === 'editor' || value === 'viewer';

/**
 * Go encodes empty slices as `null`, and older nodes omit `groups` entirely. Normalize lists so views never
 * crash, but keep "groups absent" distinguishable from "no groups" so the UI can say the node lacks the feature.
 */
export function normalizeOverview(value: unknown): Overview {
  if (!isRecord(value) || !isRecord(value.node) || typeof value.node.id !== 'string' || typeof value.now !== 'number'
      || !isRecord(value.user) || !isRole(value.user.role)) {
    throw new ApiFailure(200, 'This address answered, but not like an Uptime-Canary node.');
  }
  const monitors = list<Record<string, unknown>>(value.monitors).filter(view => isRecord(view) && isRecord(view.monitor)).map(view => ({
    ...view,
    observations: list<Observation>(view.observations),
    monitor: { ...(view.monitor as Monitor), nodeIds: list<string>((view.monitor as Record<string, unknown>).nodeIds) },
  })) as unknown as MonitorView[];
  const overview: Overview = {
    node: { id: value.node.id, name: typeof value.node.name === 'string' ? value.node.name : value.node.id },
    now: value.now,
    monitors,
    nodes: list(value.nodes),
    incidents: list(value.incidents),
    notifications: list(value.notifications),
    user: { name: typeof value.user.name === 'string' ? value.user.name : 'account', role: value.user.role },
  };
  if ('groups' in value) overview.groups = normalizeGroups(value.groups);
  if ('maintenance' in value) overview.maintenance = list<MaintenanceView>(value.maintenance);
  return overview;
}

/** Stands in for a node in the dashboard's own overview, which reports no node: nothing is read from one. */
export const DASHBOARD_PLACE = { id: 'dashboard', name: 'This dashboard' } as const;

/**
 * Control plane, no node selected: the dashboard's own overview, used for its clock and the signed-in account's role
 * while no node needs to answer. It carries no monitoring data; any lists in it are ignored rather than shown as a node's.
 */
export function normalizeDashboardOverview(value: unknown): Overview {
  if (!isRecord(value) || typeof value.now !== 'number' || !isRecord(value.user) || !isRole(value.user.role)) {
    throw new ApiFailure(200, 'This address answered, but not like an Uptime Canary dashboard.');
  }
  return {
    node: { ...DASHBOARD_PLACE }, now: value.now, monitors: [], nodes: [], incidents: [], notifications: [],
    user: { name: typeof value.user.name === 'string' ? value.user.name : 'account', role: value.user.role },
  };
}

const NODE_STATES: readonly RegisteredNode['state'][] = ['starting', 'answering', 'not-answering', 'credential-refused', 'different-node', 'enrolling', 'removing', 'rotating'];
const cleanText = (text: string, limit: number): string => text.replace(/[\x00-\x1f\x7f]+/gu, ' ').trim().slice(0, limit);
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);

/** One registry entry. Unknown fields are dropped; the dashboard never sends node credentials, and none are kept. */
export function normalizeRegisteredNode(value: unknown): RegisteredNode {
  if (!isRecord(value) || typeof value.id !== 'string' || !REGISTRATION_ID.test(value.id) || typeof value.label !== 'string'
      || typeof value.state !== 'string' || !(NODE_STATES as readonly string[]).includes(value.state)) {
    throw new ApiFailure(200, 'This dashboard sent a node entry this page could not read.');
  }
  const nodeId = typeof value.nodeId === 'string' ? cleanText(value.nodeId, 128) : '';
  const node: RegisteredNode = {
    id: value.id,
    label: cleanText(value.label, 128) || nodeId || 'Unnamed node',
    nodeId,
    nodeName: typeof value.nodeName === 'string' && value.nodeName.trim() ? cleanText(value.nodeName, 128) : nodeId,
    local: value.local === true,
    state: value.state as RegisteredNode['state'],
  };
  if (typeof value.url === 'string' && value.url) node.url = cleanText(value.url, 512);
  if (finite(value.lastReadAt) && value.lastReadAt > 0) node.lastReadAt = value.lastReadAt;
  if (finite(value.monitorCount) && value.monitorCount >= 0) node.monitorCount = Math.floor(value.monitorCount);
  if (typeof value.error === 'string' && cleanText(value.error, 240)) node.error = cleanText(value.error, 240);
  return node;
}

/** What the dashboard's server found at an address. The ticket is an opaque, short-lived handle kept only in memory. */
export function normalizeVerification(value: unknown): NodeVerification {
  if (!isRecord(value) || typeof value.ticket !== 'string' || !/^[\x21-\x7e]{1,256}$/u.test(value.ticket) || !finite(value.expiresAt)
      || !isRecord(value.node) || typeof value.node.id !== 'string' || !value.node.id || typeof value.url !== 'string') {
    throw new ApiFailure(200, 'This dashboard sent a verification this page could not read.');
  }
  const url = cleanText(value.url, 512);
  const id = cleanText(value.node.id, 128);
  return {
    ticket: value.ticket,
    expiresAt: value.expiresAt,
    node: { id, name: typeof value.node.name === 'string' && value.node.name.trim() ? cleanText(value.node.name, 128) : id },
    label: typeof value.label === 'string' ? cleanText(value.label, 128) : '',
    url,
    // Either the server's word or the address itself: plain HTTP is never reported as encrypted.
    plainHttp: value.plainHttp === true || url.startsWith('http:'),
  };
}

function normalizeGroups(value: unknown): GroupView[] {
  return list<Record<string, unknown>>(value).filter(view => isRecord(view) && isRecord(view.group)).map(view => ({
    ...view,
    group: { ...(view.group as Group), nodeIds: list<string>((view.group as Record<string, unknown>).nodeIds) },
  })) as unknown as GroupView[];
}

function normalizeAccount(value: unknown): AccountView {
  if (!isRecord(value) || typeof value.id !== 'string' || typeof value.name !== 'string' || !isRole(value.role)) {
    throw new ApiFailure(200, 'The node sent an account this dashboard could not read.');
  }
  const account: AccountView = { id: value.id, name: value.name, role: value.role };
  if (typeof value.passwordEnabled === 'boolean') account.passwordEnabled = value.passwordEnabled;
  if (typeof value.mfaEnabled === 'boolean') account.mfaEnabled = value.mfaEnabled;
  return account;
}

async function errorDetail(response: Response): Promise<string | undefined> {
  try {
    const reader = response.body?.getReader();
    if (!reader) return undefined;
    const chunks: Uint8Array[] = [];
    let size = 0;
    while (size < ERROR_BODY_LIMIT) {
      const { done, value } = await reader.read();
      if (done || !value) break;
      chunks.push(value);
      size += value.byteLength;
    }
    void reader.cancel().catch(() => undefined);
    const bytes = new Uint8Array(Math.min(size, ERROR_BODY_LIMIT));
    let offset = 0;
    for (const chunk of chunks) {
      const part = chunk.subarray(0, bytes.length - offset);
      bytes.set(part, offset);
      offset += part.length;
    }
    const parsed: unknown = JSON.parse(new TextDecoder().decode(bytes));
    if (!isRecord(parsed) || typeof parsed.error !== 'string') return undefined;
    const text = parsed.error.replace(/[\x00-\x1f\x7f]+/gu, ' ').trim().slice(0, 240);
    return text || undefined;
  } catch {
    return undefined;
  }
}

const STATUS_MESSAGES: Record<number, string> = {
  400: 'The node rejected these values.',
  401: 'This node did not accept the sign-in.',
  403: 'This account or dashboard origin does not have permission for this action.',
  404: 'The requested item no longer exists on this node.',
  409: 'The configuration changed or has conflicting versions. Reload it before saving.',
  413: 'The request is larger than this node accepts.',
  429: 'The node is limiting attempts right now. Wait a moment and try again.',
  503: 'The node can’t handle requests right now.',
};

/**
 * Control plane, the dashboard's own routes (sign-in, accounts, the node list): these answers come from the dashboard,
 * not from a node.
 */
const DASHBOARD_STATUS_MESSAGES: Record<number, string> = {
  400: 'This dashboard rejected these values.',
  401: 'This dashboard did not accept the sign-in.',
  403: 'Your dashboard account doesn’t have permission for this action.',
  404: 'This dashboard doesn’t have that item, or it was just removed.',
  409: 'This changed on the dashboard in the meantime. Reload and try again.',
  413: 'The request is larger than this dashboard accepts.',
  429: 'This dashboard is limiting attempts right now. Wait a moment and try again.',
  502: 'The dashboard’s server couldn’t get a usable answer from the node.',
  503: 'This dashboard can’t handle requests right now.',
  504: 'The dashboard’s server couldn’t get an answer from the node in time.',
};
/**
 * Control plane, a registered node's routes. The dashboard answers 404 for an unknown registration and 502 when the
 * node refuses the dashboard's credential (never 401, which would mean this dashboard sign-in ended).
 */
const REGISTERED_NODE_MESSAGES: Record<number, string> = {
  401: 'This dashboard did not accept the sign-in.',
  502: 'The dashboard’s server couldn’t get a usable answer from this node.',
  504: 'The dashboard’s server couldn’t get an answer from this node in time.',
};

interface RequestOptions { method?: string; body?: unknown; token?: string; signal?: AbortSignal; timeoutMs?: number; registeredNode?: string }

/**
 * A node at the dashboard's own origin may sit behind the same sign-in proxy, which lets requests through with its
 * own session cookie. When that session ends, the proxy redirects (refused here) or answers with its login page.
 * Neither can be told apart from other failures, so the message names it as one possibility.
 */
const PROXY_HINT = 'If a sign-in proxy protects this address, its session may have ended: reload the page to sign in to the proxy again.';
const atPageOrigin = (baseUrl: string): boolean => globalThis.location?.origin === baseUrl;

/** Behind a gateway these come from the dashboard's server when its node doesn't answer, so they don't blame the node's API. */
const GATEWAY_STATUS_MESSAGES: Record<number, string> = {
  502: 'The dashboard’s server couldn’t get an answer from its node.',
  504: 'The dashboard’s server couldn’t get an answer from its node in time.',
};

/**
 * A gateway dashboard reaches its node only through its own server, so it refuses any other origin before a request
 * leaves the browser: a peer's address, a typed or linked node address, or an old remembered one. The address is
 * never re-aimed at the page's origin, which could make it mean a different node.
 */
export class OriginRefused extends Error {
  constructor(public readonly origin: string) {
    super(refusal(origin));
    this.name = 'OriginRefused';
  }
}

function refusal(origin: string): string {
  const page = pageOrigin();
  const nodes = controlPlaneMode() ? 'its nodes' : 'its node';
  return page
    ? `This dashboard reaches ${nodes} only through its own server at ${page}, so it doesn’t contact ${origin}.`
    : `This dashboard reaches ${nodes} only through its own server, and this page has no web address to reach it at.`;
}

/** The dashboard's own origin, or a refusal when the page has none. */
function dashboardOrigin(): string {
  const page = pageOrigin();
  if (!page) throw new OriginRefused('this page');
  return page;
}

function refuseForeignOrigin(url: string): void {
  let origin: string | null = null;
  try { origin = new URL(url).origin; } catch { /* not an address: refused below */ }
  if (origin === null || origin !== pageOrigin()) throw new OriginRefused(origin ?? url);
}

/**
 * Every request: explicit bearer, no redirects, no referrer, no cache, bounded time. Cookies go only to the page's
 * own origin (a sign-in proxy's session); the node ignores them and still requires its own credential. Browsers send
 * none to a node on any other origin. In gateway mode nothing goes to any other origin at all. On the control plane a
 * monitoring request may also name a saved registration; the credential is still the dashboard sign-in's.
 */
async function request<T>(baseUrl: string, path: string, { method = 'GET', body, token, signal, timeoutMs = REQUEST_TIMEOUT_MS, registeredNode }: RequestOptions = {}): Promise<T> {
  const gateway = gatewayMode();
  const controlPlane = controlPlaneMode();
  if (gateway) refuseForeignOrigin(`${baseUrl}${path}`);
  if (registeredNode !== undefined && !controlPlane) throw new Error('Only a dashboard with a node registry can select a registered node.');
  const throughNode = registeredNode !== undefined;
  const headers: Record<string, string> = {};
  if (registeredNode !== undefined) headers[REGISTERED_NODE_HEADER] = checkRegistration(registeredNode);
  const timeout = AbortSignal.timeout(timeoutMs);
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch(`${baseUrl}${path}`, {
      method,
      headers,
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      redirect: 'error',
      cache: 'no-store',
      credentials: 'same-origin',
      referrerPolicy: 'no-referrer',
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
  } catch {
    if (signal?.aborted) throw new DOMException('Request cancelled', 'AbortError');
    const seconds = Math.round(timeoutMs / 1000);
    if (timeout.aborted) {
      throw new ApiFailure(0, !controlPlane ? `The node did not answer within ${seconds} seconds.`
        : throughNode ? `This node’s answer did not arrive through the dashboard’s server within ${seconds} seconds.`
        : `This dashboard’s server did not answer within ${seconds} seconds.`);
    }
    if (gateway) throw new ApiFailure(0, `Cannot reach this dashboard’s server. Check that it’s running. ${PROXY_HINT}`);
    if (atPageOrigin(baseUrl)) throw new ApiFailure(0, `Cannot reach this node. Check that it’s running. ${PROXY_HINT}`);
    throw new ApiFailure(0, 'Cannot reach this node. Check the address, that the node is running, and that it allows this dashboard’s origin.');
  }
  if (!response.ok) {
    const detail = await errorDetail(response);
    const message = (controlPlane ? (throughNode ? REGISTERED_NODE_MESSAGES : DASHBOARD_STATUS_MESSAGES)[response.status] : undefined)
      ?? (gateway ? GATEWAY_STATUS_MESSAGES[response.status] : undefined) ?? STATUS_MESSAGES[response.status];
    const fallback = controlPlane && !throughNode ? `This dashboard returned HTTP ${response.status}.` : `The node returned HTTP ${response.status}.`;
    throw new ApiFailure(response.status, message ?? fallback, detail);
  }
  if (response.status === 204) return undefined as T;
  try {
    return await response.json() as T;
  } catch {
    if (signal?.aborted) throw new DOMException('Request cancelled', 'AbortError');
    const unreadable = controlPlane && !throughNode ? 'This dashboard’s server sent a response this page could not read.' : 'The node sent a response this dashboard could not read.';
    throw new ApiFailure(response.status, atPageOrigin(baseUrl) ? `${unreadable} ${PROXY_HINT}` : unreadable);
  }
}

/**
 * Public: which sign-in methods a node offers. Older nodes without password sign-in answer 404 (or 401, since
 * every other `/api` path needs a token); both are reported as token-only.
 */
export async function fetchAuthStatus(baseUrl: string, signal?: AbortSignal): Promise<AuthenticationStatus> {
  try {
    const value = await request<unknown>(normalizeApiUrl(baseUrl), '/api/auth/status', { signal });
    if (!isRecord(value)) throw new ApiFailure(200, 'This address answered, but not like an Uptime-Canary node.');
    return {
      passwordLogin: value.passwordLogin === true,
      passwordConfigured: value.passwordConfigured === true,
      setupRequired: value.setupRequired === true,
      tokenLogin: value.tokenLogin !== false,
      mfaSupported: value.mfaSupported === true,
    };
  } catch (error) {
    if (error instanceof ApiFailure && (error.status === 404 || error.status === 401)) {
      return { passwordLogin: false, passwordConfigured: false, setupRequired: false, tokenLogin: true, mfaSupported: false };
    }
    throw error;
  }
}

export type SignIn = { kind: 'mfa' } | { kind: 'signed-in'; api: CanaryApi; user: AccountView };

/** Public password sign-in. A correct password with two-factor enabled asks for a code before any session exists. */
export async function passwordSignIn(baseUrl: string, input: PasswordLoginInput, signal?: AbortSignal): Promise<SignIn> {
  const base = normalizeApiUrl(baseUrl);
  const body: PasswordLoginInput = { name: input.name.trim(), password: input.password };
  if (input.code) body.code = cleanCode(input.code);
  const value = await request<PasswordLoginResult | unknown>(base, '/api/auth/login', { method: 'POST', body, signal });
  if (isRecord(value) && value.mfaRequired === true) return { kind: 'mfa' };
  if (!isRecord(value) || typeof value.token !== 'string' || typeof value.expiresAt !== 'number') {
    throw new ApiFailure(200, 'The node sent a sign-in response this dashboard could not read.');
  }
  return { kind: 'signed-in', api: new CanaryApi(base, value.token, { credential: 'session', expiresAt: value.expiresAt }), user: normalizeAccount(value.user) };
}

/** Control plane: the dashboard's own sign-in status. `dashboard` is true only when its server owns the accounts. */
export interface DashboardAuthStatus extends AuthenticationStatus { dashboard: boolean }

/**
 * Public: whether this dashboard still needs its first owner, and which sign-ins it offers. Asked of the dashboard
 * itself, which answers without any node. Unlike a node's status, a failure is reported, never read as token-only.
 */
export async function fetchDashboardStatus(signal?: AbortSignal): Promise<DashboardAuthStatus> {
  const value = await request<unknown>(dashboardOrigin(), '/api/auth/status', { signal });
  if (!isRecord(value)) throw new ApiFailure(200, 'This address answered, but not like an Uptime Canary dashboard.');
  return {
    dashboard: value.dashboard === true,
    setupRequired: value.setupRequired === true,
    passwordLogin: value.passwordLogin === true,
    passwordConfigured: value.passwordConfigured === true,
    tokenLogin: value.tokenLogin === true,
    mfaSupported: value.mfaSupported === true,
  };
}

/**
 * Public, once: creates the dashboard's first owner (an administrator). The setup code comes from the operator's
 * private server setup file or log and proves who runs the server. No session is issued: sign in afterwards.
 */
export async function createDashboardOwner(input: DashboardOwnerInput, signal?: AbortSignal): Promise<AccountView> {
  const body: DashboardOwnerInput = { name: input.name.trim(), password: input.password, code: input.code.trim() };
  return normalizeAccount(await request<unknown>(dashboardOrigin(), '/api/dashboard/setup', { method: 'POST', body, signal }));
}

/** `removing`: the dashboard stopped reading it, but the node's revocation isn't confirmed yet (202). */
export type NodeRemoval = { removed: true } | { removed: false; node: RegisteredNode | null };

/** A registry edit: always the label; `url` only to follow the same node to a new address. */
export interface RegisteredNodeEdit { label: string; url?: string }

export type Credential = 'token' | 'session';

/** Byte size of a JSON request body, as the node measures it. */
export const jsonBytes = (value: unknown): number => new TextEncoder().encode(JSON.stringify(value)).length;

/**
 * The credential (an account token or a 12-hour password session) stays inside this client instance in memory:
 * never in browser storage, URLs, logs or error text.
 */
export class CanaryApi {
  readonly baseUrl: string;
  readonly credential: Credential;
  /** Absolute expiry of a password session (issuer's clock, epoch ms). Account tokens don't expire. */
  readonly expiresAt: number | null;
  /**
   * Control plane only: the saved registration monitoring requests are for, or null for none. Sign-in, account and
   * node-list requests never carry it, so they always reach the dashboard's own accounts.
   */
  readonly registeredNode: string | null;
  constructor(baseUrl: string, private readonly token: string, options: { credential?: Credential; expiresAt?: number; registeredNode?: string } = {}) {
    this.baseUrl = normalizeApiUrl(baseUrl);
    this.credential = options.credential ?? 'token';
    this.expiresAt = options.expiresAt ?? null;
    if (options.registeredNode !== undefined && !controlPlaneMode()) throw new Error('Only a dashboard with a node registry can select a registered node.');
    this.registeredNode = options.registeredNode === undefined ? null : checkRegistration(options.registeredNode);
  }

  /** Same account token for another node, without exposing the token to callers. Sessions are node-local. */
  withBaseUrl(baseUrl: string): CanaryApi {
    if (this.credential !== 'token') throw new Error('Password sessions only work on the node that issued them.');
    return new CanaryApi(baseUrl, this.token);
  }

  /**
   * Control plane: the same dashboard credential (session or token, with its expiry), aimed at another saved
   * registration, or at none. Nothing is signed out or in: the dashboard sign-in is unchanged.
   */
  withRegisteredNode(id: string | null): CanaryApi {
    return new CanaryApi(this.baseUrl, this.token, {
      credential: this.credential,
      ...(this.expiresAt === null ? {} : { expiresAt: this.expiresAt }),
      ...(id === null ? {} : { registeredNode: id }),
    });
  }

  /** After this account's own token was replaced: the new token, keeping any node selection. */
  withToken(token: string): CanaryApi {
    return new CanaryApi(this.baseUrl, token, this.registeredNode === null ? {} : { registeredNode: this.registeredNode });
  }

  /** Whether `other` carries the same credential (used to avoid signing out a session that's still in use). A node selection doesn't count. */
  sameCredential(other: CanaryApi): boolean { return other.token === this.token && other.baseUrl === this.baseUrl; }

  /** Monitoring routes: to the selected registration when there is one. */
  private call<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal, timeoutMs?: number): Promise<T> {
    return request<T>(this.baseUrl, path, { method, body, token: this.token, signal, timeoutMs, ...(this.registeredNode === null ? {} : { registeredNode: this.registeredNode }) });
  }
  /** Identity, account and node-list routes: always the issuer of this credential, whatever node is selected. */
  private own<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal, timeoutMs?: number): Promise<T> {
    return request<T>(this.baseUrl, path, { method, body, token: this.token, signal, timeoutMs });
  }

  async overview(signal?: AbortSignal): Promise<Overview> {
    const value = await this.call<unknown>('/api/overview', 'GET', undefined, signal);
    return controlPlaneMode() && this.registeredNode === null ? normalizeDashboardOverview(value) : normalizeOverview(value);
  }
  monitors(signal?: AbortSignal): Promise<MonitorView[]> { return this.call('/api/monitors', 'GET', undefined, signal); }
  createMonitor(input: MonitorInput): Promise<Monitor> { return this.call('/api/monitors', 'POST', input); }
  updateMonitor(id: string, input: MonitorInput & { revision: string }): Promise<Monitor> {
    return this.call(`/api/monitors/${encodeURIComponent(id)}`, 'PUT', input);
  }
  deleteMonitor(id: string, revision: string): Promise<void> {
    return this.call(`/api/monitors/${encodeURIComponent(id)}`, 'DELETE', { revision });
  }
  async history(id: string, limit = 100, signal?: AbortSignal): Promise<Observation[]> {
    return list(await this.call<unknown>(`/api/monitors/${encodeURIComponent(id)}/history?limit=${limit}`, 'GET', undefined, signal));
  }
  async historyWindow(id: string, from: number, to: number, limit = 1000, signal?: AbortSignal): Promise<Observation[]> {
    const query = new URLSearchParams({ from: String(from), to: String(to), limit: String(limit) });
    return list(await this.call<unknown>(`/api/monitors/${encodeURIComponent(id)}/history?${query}`, 'GET', undefined, signal));
  }
  statistics(id: string, from: number, to: number, signal?: AbortSignal): Promise<LocalHistoryStatistics> {
    const query = new URLSearchParams({ from: String(from), to: String(to) });
    return this.call(`/api/monitors/${encodeURIComponent(id)}/statistics?${query}`, 'GET', undefined, signal);
  }
  /** Observed time on this node only (protocol 11). Omitting `to` lets the node use its own clock; it rejects a future `to`. */
  async availability(id: string, query: { from?: number; to?: number; stepMs?: number }, signal?: AbortSignal): Promise<LocalAvailabilityHistory> {
    const params = new URLSearchParams();
    for (const key of ['from', 'to', 'stepMs'] as const) if (query[key] !== undefined) params.set(key, String(Math.floor(query[key]!)));
    const value = await this.call<unknown>(`/api/monitors/${encodeURIComponent(id)}/availability${params.toString() ? `?${params}` : ''}`, 'GET', undefined, signal);
    const history = readAvailability(value);
    if (!history) throw new ApiFailure(200, 'The node sent observed-time history this dashboard could not read.');
    return history;
  }
  /**
   * Editors and admins (protocol 19): the newest private screenshot this node took itself for a real-browser monitor, or
   * null when it has none for the monitor's current check generation (404; older nodes answer 404 too). No query: the
   * node refuses one. The answer must be a checked PNG for exactly this monitor.
   */
  async screenshot(id: string, signal?: AbortSignal): Promise<BrowserScreenshot | null> {
    let value: unknown;
    try {
      value = await this.call<unknown>(`/api/monitors/${encodeURIComponent(id)}/screenshot`, 'GET', undefined, signal);
    } catch (error) {
      if (error instanceof ApiFailure && error.status === 404 && !signal?.aborted) return null;
      throw error;
    }
    const shot = readScreenshot(value, id);
    if (!shot) throw new ApiFailure(200, 'The node sent a screenshot this dashboard could not read.');
    return shot;
  }
  async channels(signal?: AbortSignal): Promise<NotificationChannelView[]> {
    return list(await this.call<unknown>('/api/notification-channels', 'GET', undefined, signal));
  }
  createChannel(input: NotificationChannelInput): Promise<NotificationChannel> { return this.call('/api/notification-channels', 'POST', input); }
  updateChannel(id: string, input: NotificationChannelInput & { revision: string }): Promise<NotificationChannel> {
    return this.call(`/api/notification-channels/${encodeURIComponent(id)}`, 'PUT', input);
  }
  deleteChannel(id: string, revision: string): Promise<void> { return this.call(`/api/notification-channels/${encodeURIComponent(id)}`, 'DELETE', { revision }); }
  async channelConflicts(id: string, signal?: AbortSignal): Promise<NotificationChannel[]> {
    return list(await this.call<unknown>(`/api/notification-channels/${encodeURIComponent(id)}/conflicts`, 'GET', undefined, signal));
  }
  resolveChannel(id: string, input: NotificationChannelInput & { revisions: string[] }): Promise<NotificationChannel> {
    return this.call(`/api/notification-channels/${encodeURIComponent(id)}/resolve`, 'POST', input);
  }
  testChannel(id: string, revision: string): Promise<{ attempts: DeliveryAttemptView[] }> {
    return this.call(`/api/notification-channels/${encodeURIComponent(id)}/test`, 'POST', { revision });
  }
  /**
   * Editors and admins (protocol 19): one test send with settings that aren't saved, disabled ones included. The node
   * saves nothing but the attempts' local outcomes, shares the saved test's throttle (one batch, 5 s apart) and answers
   * 202 with `preview: true`. The settings travel only in this request body; nothing here stores or logs them.
   */
  async previewChannelTest(input: NotificationChannelInput, signal?: AbortSignal): Promise<{ attempts: DeliveryAttemptView[] }> {
    const value = await this.call<unknown>('/api/notification-channels/preview-test', 'POST', input, signal);
    const reply = typeof value === 'object' && value !== null ? value as { preview?: unknown; attempts?: unknown } : {};
    if (reply.preview !== true || !Array.isArray(reply.attempts)) throw new ApiFailure(200, 'The node didn’t confirm a test of unsaved settings. Nothing was saved.');
    return { attempts: reply.attempts as DeliveryAttemptView[] };
  }
  /**
   * Editors and admins (protocol 19): adds this channel to the named monitors, keeping their other destinations, in one
   * all-or-nothing batch of at most 200 at the revisions shown. A stale channel or monitor, a conflict or a monitor that
   * already has 16 channels refuses the whole batch (409). `changedIds` lists the monitors that didn't have it yet; it
   * doesn't start a new check generation.
   */
  async applyChannel(id: string, revision: string, monitors: { id: string; revision: string }[]): Promise<{ changedIds: string[] }> {
    const value = await this.call<unknown>(`/api/notification-channels/${encodeURIComponent(id)}/apply`, 'POST', { revision, monitors: monitors.map(m => ({ id: m.id, revision: m.revision })) });
    const changed = typeof value === 'object' && value !== null ? (value as { changedIds?: unknown }).changedIds : undefined;
    if (!Array.isArray(changed) || !changed.every(item => typeof item === 'string')) throw new ApiFailure(200, 'The node applied the channel but sent a reply this dashboard could not read. Refresh to see the monitors.');
    return { changedIds: changed as string[] };
  }
  async deliveries(options: { monitorId?: string; incidentId?: string; limit?: number } = {}, signal?: AbortSignal): Promise<DeliveryAttemptView[]> {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(options)) if (value !== undefined) query.set(key, String(value));
    return list(await this.call<unknown>(`/api/notification-deliveries?${query}`, 'GET', undefined, signal));
  }
  async maintenance(signal?: AbortSignal): Promise<MaintenanceView[]> { return list(await this.call<unknown>('/api/maintenance', 'GET', undefined, signal)); }
  createMaintenance(input: MaintenanceInput): Promise<Maintenance> { return this.call('/api/maintenance', 'POST', input); }
  updateMaintenance(id: string, input: MaintenanceInput & { revision: string }): Promise<Maintenance> {
    return this.call(`/api/maintenance/${encodeURIComponent(id)}`, 'PUT', input);
  }
  deleteMaintenance(id: string, revision: string): Promise<void> { return this.call(`/api/maintenance/${encodeURIComponent(id)}`, 'DELETE', { revision }); }
  async maintenanceConflicts(id: string, signal?: AbortSignal): Promise<Maintenance[]> { return list(await this.call<unknown>(`/api/maintenance/${encodeURIComponent(id)}/conflicts`, 'GET', undefined, signal)); }
  resolveMaintenance(id: string, input: MaintenanceInput & { revisions: string[] }): Promise<Maintenance> {
    return this.call(`/api/maintenance/${encodeURIComponent(id)}/resolve`, 'POST', input);
  }
  async statusPages(signal?: AbortSignal): Promise<StatusPageView[]> { return list(await this.call<unknown>('/api/status-pages', 'GET', undefined, signal)); }
  createStatusPage(input: StatusPageInput): Promise<StatusPage> { return this.call('/api/status-pages', 'POST', input); }
  updateStatusPage(id: string, input: StatusPageInput & { revision: string }): Promise<StatusPage> { return this.call(`/api/status-pages/${encodeURIComponent(id)}`, 'PUT', input); }
  deleteStatusPage(id: string, revision: string): Promise<void> { return this.call(`/api/status-pages/${encodeURIComponent(id)}`, 'DELETE', { revision }); }
  async statusPageConflicts(id: string, signal?: AbortSignal): Promise<StatusPage[]> { return list(await this.call<unknown>(`/api/status-pages/${encodeURIComponent(id)}/conflicts`, 'GET', undefined, signal)); }
  resolveStatusPage(id: string, input: StatusPageInput & { revisions: string[] }): Promise<StatusPage> { return this.call(`/api/status-pages/${encodeURIComponent(id)}/resolve`, 'POST', input); }
  async tags(signal?: AbortSignal): Promise<TagView[]> { return list(await this.call<unknown>('/api/tags', 'GET', undefined, signal)); }
  createTag(input: TagInput): Promise<Tag> { return this.call('/api/tags', 'POST', input); }
  updateTag(id: string, input: TagInput & { revision: string }): Promise<Tag> { return this.call(`/api/tags/${encodeURIComponent(id)}`, 'PUT', input); }
  deleteTag(id: string, revision: string): Promise<void> { return this.call(`/api/tags/${encodeURIComponent(id)}`, 'DELETE', { revision }); }
  async tagConflicts(id: string, signal?: AbortSignal): Promise<Tag[]> { return list(await this.call<unknown>(`/api/tags/${encodeURIComponent(id)}/conflicts`, 'GET', undefined, signal)); }
  resolveTag(id: string, input: TagInput & { revisions: string[] }): Promise<Tag> { return this.call(`/api/tags/${encodeURIComponent(id)}/resolve`, 'POST', input); }
  async conflicts(id: string, signal?: AbortSignal): Promise<Monitor[]> {
    return list(await this.call<unknown>(`/api/monitors/${encodeURIComponent(id)}/conflicts`, 'GET', undefined, signal));
  }
  resolveMonitor(id: string, input: MonitorInput & { revisions: string[] }): Promise<Monitor> {
    return this.call(`/api/monitors/${encodeURIComponent(id)}/resolve`, 'POST', input);
  }

  async groups(signal?: AbortSignal): Promise<GroupView[]> {
    return normalizeGroups(await this.call<unknown>('/api/groups', 'GET', undefined, signal));
  }
  createGroup(input: GroupInput): Promise<Group> { return this.call('/api/groups', 'POST', input); }
  updateGroup(id: string, input: GroupInput & { revision: string }): Promise<Group> {
    return this.call(`/api/groups/${encodeURIComponent(id)}`, 'PUT', input);
  }
  deleteGroup(id: string, revision: string): Promise<void> {
    return this.call(`/api/groups/${encodeURIComponent(id)}`, 'DELETE', { revision });
  }
  async groupConflicts(id: string, signal?: AbortSignal): Promise<Group[]> {
    return list(await this.call<unknown>(`/api/groups/${encodeURIComponent(id)}/conflicts`, 'GET', undefined, signal));
  }
  resolveGroup(id: string, input: GroupInput & { revisions: string[] }): Promise<Group> {
    return this.call(`/api/groups/${encodeURIComponent(id)}/resolve`, 'POST', input);
  }

  // ----- this account (every role). Bodies carry only the fields each route documents; unused ones are omitted.
  async me(signal?: AbortSignal): Promise<AccountView> { return normalizeAccount(await this.own<unknown>('/api/auth/me', 'GET', undefined, signal)); }
  /** First setup (authorized by an account token) sends only `password`; later changes add the current password and a code if 2FA is on. */
  async setPassword(password: string, currentPassword?: string, code?: string): Promise<AccountView> {
    const body: { password: string; currentPassword?: string; code?: string } = { password };
    if (currentPassword) body.currentPassword = currentPassword;
    if (code) body.code = cleanCode(code);
    return normalizeAccount(await this.own<unknown>('/api/auth/password', 'POST', body));
  }
  async startAuthenticator(currentPassword: string): Promise<AuthenticatorEnrollment> {
    const value = await this.own<unknown>('/api/auth/totp/setup', 'POST', { currentPassword });
    if (!isRecord(value) || typeof value.secret !== 'string' || typeof value.uri !== 'string' || typeof value.expiresAt !== 'number') {
      throw new ApiFailure(200, 'The node sent an enrollment this dashboard could not read.');
    }
    return { secret: value.secret, uri: value.uri, expiresAt: value.expiresAt };
  }
  async confirmAuthenticator(currentPassword: string, code: string): Promise<AccountView> {
    return normalizeAccount(await this.own<unknown>('/api/auth/totp/confirm', 'POST', { currentPassword, code: cleanCode(code) }));
  }
  async disableAuthenticator(currentPassword: string, code: string): Promise<AccountView> {
    return normalizeAccount(await this.own<unknown>('/api/auth/totp/disable', 'POST', { currentPassword, code: cleanCode(code) }));
  }
  /** Ends this password session on the node. Short timeout: signing out must not hang the tab. */
  logout(): Promise<void> { return this.own('/api/auth/logout', 'POST', undefined, undefined, SIGN_OUT_TIMEOUT_MS); }
  logoutAll(): Promise<void> { return this.own('/api/auth/logout-all', 'POST'); }

  // ----- accounts on this node (admins); on the control plane, this dashboard's accounts
  async accounts(signal?: AbortSignal): Promise<AccountView[]> {
    return list<unknown>(await this.own<unknown>('/api/accounts', 'GET', undefined, signal)).map(normalizeAccount);
  }
  async createAccount(input: AccountInput): Promise<AccountView> {
    const body: AccountInput = { name: input.name, role: input.role };
    if (input.password) body.password = input.password;
    if (input.token) body.token = input.token;
    return normalizeAccount(await this.own<unknown>('/api/accounts', 'POST', body));
  }
  /** Sends only the fields that change. The node revokes the account's password sessions. */
  async updateAccount(id: string, changes: { name?: string; role?: Role; token?: string }): Promise<AccountView> {
    const body: { name?: string; role?: Role; token?: string } = {};
    if (changes.name !== undefined) body.name = changes.name;
    if (changes.role !== undefined) body.role = changes.role;
    if (changes.token !== undefined) body.token = changes.token;
    return normalizeAccount(await this.own<unknown>(`/api/accounts/${encodeURIComponent(id)}`, 'PUT', body));
  }
  async resetAccountPassword(id: string, password: string, resetMfa: boolean): Promise<AccountView> {
    const body: { password: string; resetMfa?: boolean } = { password };
    if (resetMfa) body.resetMfa = true;
    return normalizeAccount(await this.own<unknown>(`/api/accounts/${encodeURIComponent(id)}/password`, 'POST', body));
  }
  deleteAccount(id: string): Promise<void> { return this.own(`/api/accounts/${encodeURIComponent(id)}`, 'DELETE'); }

  // ----- control plane: the dashboard's shared node registry. Any role reads and rechecks; administrators enroll, edit,
  // rotate, retry and remove. Node credentials go to the dashboard's server only in `verifyNode` and as the optional
  // recovery credential of `rotateNode` and `retryNode`, and never come back.
  async registeredNodes(signal?: AbortSignal): Promise<RegisteredNode[]> {
    const value = await this.own<unknown>('/api/dashboard/nodes', 'GET', undefined, signal);
    if (value !== null && !Array.isArray(value)) throw new ApiFailure(200, 'This dashboard sent a node list this page could not read.');
    return list<unknown>(value).map(normalizeRegisteredNode);
  }
  /** Read-only: the dashboard's server contacts the address and proves the node's identity. Nothing changes on the node. */
  async verifyNode(input: NodeVerificationInput, signal?: AbortSignal): Promise<NodeVerification> {
    const body: NodeVerificationInput = { label: input.label.trim(), url: normalizeApiUrl(input.url), token: input.token };
    return normalizeVerification(await this.own<unknown>('/api/dashboard/verify', 'POST', body, signal));
  }
  /** Drops an unused verification. Expiry drops it too, so a failure here leaves nothing behind for long. */
  discardVerification(ticket: string): Promise<void> {
    return this.own(`/api/dashboard/verify/${encodeURIComponent(ticket)}`, 'DELETE', undefined, undefined, SIGN_OUT_TIMEOUT_MS);
  }
  async enrollNode(ticket: string, signal?: AbortSignal): Promise<RegisteredNode> {
    return normalizeRegisteredNode(await this.own<unknown>('/api/dashboard/nodes', 'POST', { ticket }, signal, NODE_CHANGE_TIMEOUT_MS));
  }
  async checkNode(id: string, signal?: AbortSignal): Promise<RegisteredNode> {
    return normalizeRegisteredNode(await this.own<unknown>(`/api/dashboard/nodes/${encodeURIComponent(checkRegistration(id))}/check`, 'POST', undefined, signal));
  }
  /**
   * Renames the entry; with `url`, also moves it to the same node's new address. A rename needs no node. A move is
   * saved only after the dashboard's server proves the same node ID answers there and accepts all three of this
   * dashboard's existing role credentials, so the registration ID, its credentials and its public links stay. It can't
   * point the entry at a different node. The bundled node's address belongs to the deployment (409).
   */
  async editNode(id: string, changes: RegisteredNodeEdit, signal?: AbortSignal): Promise<RegisteredNode> {
    const path = `/api/dashboard/nodes/${encodeURIComponent(checkRegistration(id))}`;
    const body: RegisteredNodeEdit = { label: changes.label.trim() };
    if (changes.url !== undefined) body.url = normalizeApiUrl(changes.url);
    return normalizeRegisteredNode(await this.own<unknown>(path, 'PATCH', body, signal, NODE_CHANGE_TIMEOUT_MS));
  }
  /**
   * Replaces this dashboard's three role credentials on the node; the old ones, and node sessions made with them, stop
   * working there. `recovery` is a current administrator credential on this same node, needed only when it no longer
   * accepts the dashboard's own; it goes in this one request body and nowhere else. A `rotating` answer (202) means the
   * replacement is saved and the node hasn't confirmed it yet: the server keeps retrying, and `retryNode` resumes it.
   */
  async rotateNode(id: string, recovery?: string, signal?: AbortSignal): Promise<RegisteredNode> {
    const path = `/api/dashboard/nodes/${encodeURIComponent(checkRegistration(id))}/rotate`;
    const body: { token?: string } = {};
    if (recovery) body.token = recovery;
    return normalizeRegisteredNode(await this.own<unknown>(path, 'POST', body, signal, NODE_CHANGE_TIMEOUT_MS));
  }
  /**
   * Null when the retried removal finished and the entry is gone (204). An unfinished enrollment, removal or rotation
   * resumes; without `recovery` the request has no body, exactly as before. `recovery` applies only to a pending access
   * update (`rotating`): a current administrator credential on this same node, for when it stopped accepting the one the
   * saved update uses. The dashboard's server checks it there and that the node's three accounts for this dashboard still
   * exist, keeps it with the saved update in place of that credential, and retries the same update; it never makes
   * other credentials. 200: the node confirmed it. 202 `rotating`: the credential is kept, and the node hasn't confirmed
   * yet. A refusal comes before it keeps anything. It goes in this one request body and nowhere else.
   */
  async retryNode(id: string, recovery?: string, signal?: AbortSignal): Promise<RegisteredNode | null> {
    const path = `/api/dashboard/nodes/${encodeURIComponent(checkRegistration(id))}/retry`;
    const value = await this.own<unknown>(path, 'POST', recovery ? { token: recovery } : undefined, signal, NODE_CHANGE_TIMEOUT_MS);
    return value === undefined ? null : normalizeRegisteredNode(value);
  }
  /** 204: removed and the node confirmed revocation. 202: no longer read, revocation pending (shown as `removing`). */
  async removeNode(id: string, signal?: AbortSignal): Promise<NodeRemoval> {
    let value: unknown;
    try {
      value = await this.own<unknown>(`/api/dashboard/nodes/${encodeURIComponent(checkRegistration(id))}`, 'DELETE', undefined, signal, NODE_CHANGE_TIMEOUT_MS);
    } catch (error) {
      // An accepted removal without a readable entry is still accepted; the list shows its state.
      if (error instanceof ApiFailure && error.status === 202) return { removed: false, node: null };
      throw error;
    }
    if (value === undefined) return { removed: true };
    try { return { removed: false, node: normalizeRegisteredNode(value) }; } catch { return { removed: false, node: null }; }
  }
}

/**
 * One readable sentence for a failure. Identity and account routes answer with specific reasons ("Current password
 * is incorrect", "Cannot remove the last administrator"), so `preferDetail` leads with the node's own words.
 */
export function describeFailure(error: unknown, options: { preferDetail?: boolean } = {}): string {
  if (error instanceof ApiFailure) {
    if (options.preferDetail && error.detail) {
      if (error.status === 401 && /^authentication required$/iu.test(error.detail)) {
        return controlPlaneMode() ? 'Your sign-in to this dashboard is no longer valid. Sign in again.' : 'Your sign-in is no longer valid on this node. Sign in again.';
      }
      return sentence(error.detail);
    }
    return error.detail && error.status !== 401 ? `${error.message} ${sentence(error.detail)}` : error.message;
  }
  if (error instanceof Error && error.message) return error.message;
  return 'Something went wrong.';
}

function sentence(text: string): string {
  const trimmed = text.charAt(0).toUpperCase() + text.slice(1);
  return /[.!?]$/u.test(trimmed) ? trimmed : `${trimmed}.`;
}

export const isAbort = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';

/**
 * Public publication data is fetched without a management/session bearer (and, like every request, cookies only at the page's own origin).
 * On the control plane `registration` names the saved node the dashboard's server reads the page from; it is a
 * public selector, never a credential, and the server adds no management credential for public routes.
 */
export function fetchPublicStatus(baseUrl: string, slug: string, signal?: AbortSignal, registration?: string): Promise<PublicStatusPage> {
  if (slug.length < 1 || slug.length > 64 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/u.test(slug)) throw new Error('Invalid status page address.');
  const query = registration === undefined ? '' : `?${new URLSearchParams({ registration: checkRegistration(registration) })}`;
  return request(normalizeApiUrl(baseUrl), `/public/status/${encodeURIComponent(slug)}${query}`, { signal });
}

/**
 * The page's public RSS feed (protocol 19): its published announcements only, never incidents or history. A node serves
 * it at /public/status/<slug>/rss.xml and refuses any query there; on the control plane the dashboard's server reads it
 * from the saved node that `registration` names, so the selector is the only query. Nothing here fetches it.
 */
export function publicRssUrl(baseUrl: string, slug: string, registration?: string): string {
  if (slug.length < 1 || slug.length > 64 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/u.test(slug)) throw new Error('Invalid status page address.');
  const query = registration === undefined ? '' : `?${new URLSearchParams({ registration: checkRegistration(registration) })}`;
  return `${normalizeApiUrl(baseUrl)}/public/status/${encodeURIComponent(slug)}/rss.xml${query}`;
}
