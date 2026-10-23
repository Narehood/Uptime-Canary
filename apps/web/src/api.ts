import type {
  AccountInput, AccountView, AuthenticationStatus, AuthenticatorEnrollment, Group, GroupInput, GroupView, Monitor, MonitorInput,
  MonitorView, Observation, Overview, PasswordLoginInput, PasswordLoginResult, Role,
  LocalAvailabilityHistory, LocalHistoryStatistics, NotificationChannel, NotificationChannelInput, NotificationChannelView,
  DeliveryAttemptView, Maintenance, MaintenanceInput, MaintenanceView,
  StatusPage, StatusPageInput, StatusPageView, PublicStatusPage, Tag, TagInput, TagView,
} from '@uptime-canary/shared';
import { readAvailability } from './availability';

const REQUEST_TIMEOUT_MS = 15_000;
const SIGN_OUT_TIMEOUT_MS = 5_000;
const ERROR_BODY_LIMIT = 4_096;
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

interface RequestOptions { method?: string; body?: unknown; token?: string; signal?: AbortSignal; timeoutMs?: number }

/** Every request: explicit bearer (never cookies), no redirects, no referrer, no cache, bounded time. */
async function request<T>(baseUrl: string, path: string, { method = 'GET', body, token, signal, timeoutMs = REQUEST_TIMEOUT_MS }: RequestOptions = {}): Promise<T> {
  const timeout = AbortSignal.timeout(timeoutMs);
  const headers: Record<string, string> = {};
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
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
  } catch {
    if (signal?.aborted) throw new DOMException('Request cancelled', 'AbortError');
    if (timeout.aborted) throw new ApiFailure(0, `The node did not answer within ${Math.round(timeoutMs / 1000)} seconds.`);
    throw new ApiFailure(0, 'Cannot reach this node. Check the address, that the node is running, and that it allows this dashboard’s origin.');
  }
  if (!response.ok) {
    const detail = await errorDetail(response);
    throw new ApiFailure(response.status, STATUS_MESSAGES[response.status] ?? `The node returned HTTP ${response.status}.`, detail);
  }
  if (response.status === 204) return undefined as T;
  try {
    return await response.json() as T;
  } catch {
    if (signal?.aborted) throw new DOMException('Request cancelled', 'AbortError');
    throw new ApiFailure(response.status, 'The node sent a response this dashboard could not read.');
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
  /** Absolute expiry of a password session (node clock, epoch ms). Account tokens don't expire. */
  readonly expiresAt: number | null;
  constructor(baseUrl: string, private readonly token: string, options: { credential?: Credential; expiresAt?: number } = {}) {
    this.baseUrl = normalizeApiUrl(baseUrl);
    this.credential = options.credential ?? 'token';
    this.expiresAt = options.expiresAt ?? null;
  }

  /** Same account token for another node, without exposing the token to callers. Sessions are node-local. */
  withBaseUrl(baseUrl: string): CanaryApi {
    if (this.credential !== 'token') throw new Error('Password sessions only work on the node that issued them.');
    return new CanaryApi(baseUrl, this.token);
  }

  /** Whether `other` carries the same credential (used to avoid signing out a session that's still in use). */
  sameCredential(other: CanaryApi): boolean { return other.token === this.token && other.baseUrl === this.baseUrl; }

  private call<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal, timeoutMs?: number): Promise<T> {
    return request<T>(this.baseUrl, path, { method, body, token: this.token, signal, timeoutMs });
  }

  async overview(signal?: AbortSignal): Promise<Overview> {
    return normalizeOverview(await this.call<unknown>('/api/overview', 'GET', undefined, signal));
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
  async me(signal?: AbortSignal): Promise<AccountView> { return normalizeAccount(await this.call<unknown>('/api/auth/me', 'GET', undefined, signal)); }
  /** First setup (authorized by an account token) sends only `password`; later changes add the current password and a code if 2FA is on. */
  async setPassword(password: string, currentPassword?: string, code?: string): Promise<AccountView> {
    const body: { password: string; currentPassword?: string; code?: string } = { password };
    if (currentPassword) body.currentPassword = currentPassword;
    if (code) body.code = cleanCode(code);
    return normalizeAccount(await this.call<unknown>('/api/auth/password', 'POST', body));
  }
  async startAuthenticator(currentPassword: string): Promise<AuthenticatorEnrollment> {
    const value = await this.call<unknown>('/api/auth/totp/setup', 'POST', { currentPassword });
    if (!isRecord(value) || typeof value.secret !== 'string' || typeof value.uri !== 'string' || typeof value.expiresAt !== 'number') {
      throw new ApiFailure(200, 'The node sent an enrollment this dashboard could not read.');
    }
    return { secret: value.secret, uri: value.uri, expiresAt: value.expiresAt };
  }
  async confirmAuthenticator(currentPassword: string, code: string): Promise<AccountView> {
    return normalizeAccount(await this.call<unknown>('/api/auth/totp/confirm', 'POST', { currentPassword, code: cleanCode(code) }));
  }
  async disableAuthenticator(currentPassword: string, code: string): Promise<AccountView> {
    return normalizeAccount(await this.call<unknown>('/api/auth/totp/disable', 'POST', { currentPassword, code: cleanCode(code) }));
  }
  /** Ends this password session on the node. Short timeout: signing out must not hang the tab. */
  logout(): Promise<void> { return this.call('/api/auth/logout', 'POST', undefined, undefined, SIGN_OUT_TIMEOUT_MS); }
  logoutAll(): Promise<void> { return this.call('/api/auth/logout-all', 'POST'); }

  // ----- accounts on this node (admins)
  async accounts(signal?: AbortSignal): Promise<AccountView[]> {
    return list<unknown>(await this.call<unknown>('/api/accounts', 'GET', undefined, signal)).map(normalizeAccount);
  }
  async createAccount(input: AccountInput): Promise<AccountView> {
    const body: AccountInput = { name: input.name, role: input.role };
    if (input.password) body.password = input.password;
    if (input.token) body.token = input.token;
    return normalizeAccount(await this.call<unknown>('/api/accounts', 'POST', body));
  }
  /** Sends only the fields that change. The node revokes the account's password sessions. */
  async updateAccount(id: string, changes: { name?: string; role?: Role; token?: string }): Promise<AccountView> {
    const body: { name?: string; role?: Role; token?: string } = {};
    if (changes.name !== undefined) body.name = changes.name;
    if (changes.role !== undefined) body.role = changes.role;
    if (changes.token !== undefined) body.token = changes.token;
    return normalizeAccount(await this.call<unknown>(`/api/accounts/${encodeURIComponent(id)}`, 'PUT', body));
  }
  async resetAccountPassword(id: string, password: string, resetMfa: boolean): Promise<AccountView> {
    const body: { password: string; resetMfa?: boolean } = { password };
    if (resetMfa) body.resetMfa = true;
    return normalizeAccount(await this.call<unknown>(`/api/accounts/${encodeURIComponent(id)}/password`, 'POST', body));
  }
  deleteAccount(id: string): Promise<void> { return this.call(`/api/accounts/${encodeURIComponent(id)}`, 'DELETE'); }
}

/**
 * One readable sentence for a failure. Identity and account routes answer with specific reasons ("Current password
 * is incorrect", "Cannot remove the last administrator"), so `preferDetail` leads with the node's own words.
 */
export function describeFailure(error: unknown, options: { preferDetail?: boolean } = {}): string {
  if (error instanceof ApiFailure) {
    if (options.preferDetail && error.detail) {
      if (error.status === 401 && /^authentication required$/iu.test(error.detail)) return 'Your sign-in is no longer valid on this node. Sign in again.';
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

/** Public publication data is fetched without a management/session bearer. */
export function fetchPublicStatus(baseUrl: string, slug: string, signal?: AbortSignal): Promise<PublicStatusPage> {
  if (slug.length < 1 || slug.length > 64 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/u.test(slug)) throw new Error('Invalid status page address.');
  return request(normalizeApiUrl(baseUrl), `/public/status/${encodeURIComponent(slug)}`, { signal });
}
