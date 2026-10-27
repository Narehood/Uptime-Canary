import { useSyncExternalStore } from 'react';
import type { AccountView, Overview, RegisteredNode } from '@uptime-canary/shared';
import { ApiFailure, CanaryApi, DASHBOARD_PLACE, isAbort } from './api';
import { controlPlaneMode } from './gateway';
import { rememberSignIn, type SignInIssuer } from './browserSession';
import { clearNotices, notify } from './notices';

export const POLL_MS = 5_000;
const MAX_BACKOFF_MS = 30_000;
class RestoredIssuerChanged extends Error {}

/**
 * live: the last overview request succeeded.
 * stale: the endpoint stopped answering; the last good overview stays visible and clearly marked.
 * rejected: the endpoint refused the credential (401); polling stops until the user signs in again.
 */
export type LinkState = 'live' | 'stale' | 'rejected';

/** Control plane: the saved registration monitoring requests go to. Nonsecret; kept in memory only, never stored. */
export interface NodeSelection {
  id: string;
  label: string;
  nodeId: string;
  nodeName: string;
  local: boolean;
}

export interface Connection {
  /** Nonsecret, in-memory identity. Polls and own-token adoption keep it; every successful connect or node selection replaces it. */
  identity: number;
  api: CanaryApi;
  overview: Overview;
  /** This account as the endpoint reports it (`GET /api/auth/me`); `null` on nodes without that route. */
  account: AccountView | null;
  /** Browser-clock time of the last successful overview. */
  fetchedAt: number;
  link: LinkState;
  failure: ApiFailure | null;
  failingSince: number | null;
  /** Control plane: the selected node, or null to read only the dashboard. Always null in direct and gateway mode. */
  selection: NodeSelection | null;
  /** Control plane: the selected node hasn't answered yet, so `overview` is an empty stand-in, not its data. */
  awaiting: boolean;
  /**
   * Control plane: the dashboard's clock minus the browser's, measured at sign-in, for times the dashboard issues
   * (session expiry, verification tickets). A selected node's `overview.now` is that node's clock instead. Null elsewhere.
   */
  issuerSkew: number | null;
}

/** Shown on the sign-in screen after the dashboard ends a connection, so the next step is obvious. */
export interface Farewell {
  url: string;
  name?: string;
  message: string;
  tone: 'info' | 'warn';
}

export interface SessionSnapshot {
  connection: Connection | null;
  farewell: Farewell | null;
  restoring?: boolean;
}

/**
 * Keeps a verified sign-in across reloads using tab session storage. Restoration revalidates it with its issuer;
 * passwords, MFA codes and selected-node data are not stored. Polls never overlap and stop on switch or sign-out.
 */
export class Session {
  private snapshot: SessionSnapshot = { connection: null, farewell: null };
  private readonly listeners = new Set<() => void>();
  private timer: ReturnType<typeof setTimeout> | undefined;
  private controller: AbortController | null = null;
  private generation = 0;
  private nextIdentity = 0;
  private inFlight = false;
  private queued = false;
  private waitingForVisible = false;
  private failures = 0;
  private restoreAttempted = false;
  private issuer: SignInIssuer | null = null;

  constructor() {
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && this.waitingForVisible) {
        this.waitingForVisible = false;
        void this.poll();
      }
    });
  }

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };
  readonly getSnapshot = (): SessionSnapshot => this.snapshot;

  /** Object snapshots change on polls; a request still belongs to its original connection and API. */
  isCurrent(expected: Pick<Connection, 'identity' | 'api'>): boolean {
    const current = this.snapshot.connection;
    return current?.identity === expected.identity && current.api === expected.api;
  }

  private emit(connection: Connection | null, farewell: Farewell | null = null): void {
    if (!connection) this.issuer = null;
    this.snapshot = { connection, farewell };
    for (const listener of this.listeners) listener();
  }
  private patch(changes: Partial<Connection>): void {
    if (this.snapshot.connection) this.emit({ ...this.snapshot.connection, ...changes });
  }

  async restore(): Promise<void> {
    if (this.restoreAttempted || this.snapshot.connection) return;
    this.restoreAttempted = true;
    const saved = CanaryApi.remembered();
    if (!saved) return;
    const { api, issuer } = saved;
    const generation = this.generation;
    this.snapshot = { ...this.snapshot, restoring: true };
    for (const listener of this.listeners) listener();
    try { await this.connect(api, undefined, issuer); }
    catch (error) {
      if (generation !== this.generation) return;
      if (error instanceof RestoredIssuerChanged || error instanceof ApiFailure && error.status === 401) rememberSignIn(null);
      this.emit(null, { url: api.baseUrl, message: error instanceof RestoredIssuerChanged ? error.message : error instanceof ApiFailure && error.status === 401
        ? 'This sign-in expired or was revoked. Sign in again.'
        : 'Your saved sign-in could not be checked. Reload to retry, or sign in below.', tone: 'warn' });
    }
  }

  /**
   * Verifies the node first; the current connection (if any) stays untouched unless the new one answers.
   * A replaced password session is signed out on its node, since this tab can never use it again. So is a new
   * password session whose connect fails or is cancelled: the caller drops it.
   */
  async connect(api: CanaryApi, signal?: AbortSignal, restoredIssuer?: SignInIssuer): Promise<Overview> {
    const restoring = restoredIssuer !== undefined;
    const generation = this.generation;
    let overview: Overview;
    let account: AccountView | null;
    try {
      [overview, account] = await Promise.all([
        api.overview(signal),
        // Only a missing route (404) means the node predates accounts; any other failure fails the connect.
        api.me(signal).catch(error => { if (error instanceof ApiFailure && error.status === 404) return null; throw error; }),
      ]);
      signal?.throwIfAborted();
      if (restoring && generation !== this.generation) throw new DOMException('Sign-in superseded', 'AbortError');
      if (restoredIssuer && (overview.node.id !== restoredIssuer.nodeId || (account?.id ?? null) !== restoredIssuer.accountId)) {
        throw new RestoredIssuerChanged('The issuing node or account changed. Sign in again to choose this endpoint.');
      }
    } catch (error) {
      if (!restoring && api.credential === 'session' && !this.snapshot.connection?.api.sameCredential(api)) void api.logout().catch(() => undefined);
      throw error;
    }
    const previous = this.snapshot.connection;
    this.stop();
    clearNotices();
    const fetchedAt = Date.now();
    this.issuer = { nodeId: overview.node.id, accountId: account?.id ?? null };
    api.remember(this.issuer);
    this.emit({
      identity: ++this.nextIdentity, api, overview, account, fetchedAt, link: 'live', failure: null, failingSince: null,
      selection: null, awaiting: false, issuerSkew: controlPlaneMode() ? overview.now - fetchedAt : null,
    });
    this.schedule(POLL_MS);
    if (previous && previous.api.credential === 'session' && !previous.api.sameCredential(api)) void previous.api.logout().catch(() => undefined);
    return overview;
  }

  /**
   * Signs out: a password session is revoked on the node before the client (and with it the credential) is
   * dropped. Account tokens are only forgotten here; they stay valid on the node. Returns whether the node
   * confirmed the session ended.
   */
  async signOut(): Promise<'ended' | 'forgotten' | 'unconfirmed' | 'superseded'> {
    const connection = this.snapshot.connection;
    if (!connection) return 'forgotten';
    rememberSignIn(null);
    this.stop();
    let result: 'ended' | 'forgotten' | 'unconfirmed' = 'forgotten';
    if (connection.api.credential === 'session') {
      try { await connection.api.logout(); result = 'ended'; }
      catch (error) { result = error instanceof ApiFailure && error.status === 401 ? 'ended' : 'unconfirmed'; }
    }
    if (!this.isCurrent(connection)) return 'superseded';
    this.emit(null);
    return result;
  }

  /** Ends the connection without calling the node, after an action the node already revoked it for. */
  end(farewell: Farewell, expected: Connection): void {
    if (!this.isCurrent(expected)) return;
    rememberSignIn(null);
    this.stop();
    this.emit(null, farewell);
  }

  /** After rotating this account's own token, keep watching with the new one (the old one no longer works). */
  adoptToken(token: string, expected: Connection): void {
    const connection = this.snapshot.connection;
    if (!connection || !this.isCurrent(expected) || connection.api.credential !== 'token') return;
    // Invalidate any poll made with the replaced token without remounting the one-time token panel.
    this.stop();
    const api = connection.api.withToken(token);
    if (this.issuer) api.remember(this.issuer);
    this.patch({ api });
    this.refresh();
  }

  /**
   * Control plane: read another saved registration, or none (`null`), with the same dashboard sign-in. Nothing is
   * signed out. The connection gets a new identity, so every poll, catalog, form and late answer that belonged to the
   * previous selection is dropped; the new node's overview is asked for at once, and until it answers the overview is
   * an empty stand-in marked `awaiting`. Returns whether the selection changed.
   */
  select(node: Pick<RegisteredNode, 'id' | 'label' | 'nodeId' | 'nodeName' | 'local'> | null, expected: Connection): boolean {
    const connection = this.snapshot.connection;
    if (!controlPlaneMode() || !connection || !this.isCurrent(expected)) return false;
    if ((connection.selection?.id ?? null) === (node?.id ?? null)) return false;
    const selection: NodeSelection | null = node ? { id: node.id, label: node.label, nodeId: node.nodeId, nodeName: node.nodeName, local: node.local } : null;
    this.stop();
    this.emit({
      ...connection,
      identity: ++this.nextIdentity,
      api: connection.api.withRegisteredNode(selection?.id ?? null),
      overview: {
        node: selection ? { id: selection.nodeId || selection.label, name: selection.nodeName || selection.label } : { ...DASHBOARD_PLACE },
        now: Date.now() + (connection.issuerSkew ?? 0),
        monitors: [], nodes: [], incidents: [], notifications: [],
        user: connection.account ? { name: connection.account.name, role: connection.account.role } : connection.overview.user,
      },
      fetchedAt: Date.now(),
      link: 'live', failure: null, failingSince: null,
      selection, awaiting: true,
    });
    void this.poll();
    return true;
  }

  /**
   * Control plane: the selected registration was renamed, or moved to the same node's new address. It is the same
   * registration and the same node, so nothing is dropped: identity, polls and data stay; only the shown name and the
   * deployment flag follow the registry. Another registration or node ID is never adopted here; that takes `select`.
   * Returns whether anything changed.
   */
  refreshSelection(node: Pick<RegisteredNode, 'id' | 'label' | 'nodeId' | 'nodeName' | 'local'>, expected: Connection): boolean {
    const connection = this.snapshot.connection;
    const current = connection?.selection;
    if (!controlPlaneMode() || !connection || !current || !this.isCurrent(expected)) return false;
    if (current.id !== node.id || current.nodeId !== node.nodeId) return false;
    if (current.label === node.label && current.nodeName === node.nodeName && current.local === node.local) return false;
    this.patch({ selection: { ...current, label: node.label, nodeName: node.nodeName, local: node.local } });
    return true;
  }

  setAccount(account: AccountView, expected: Connection): void {
    if (this.isCurrent(expected)) this.patch({ account });
  }

  clearFarewell(): void {
    if (this.snapshot.farewell) this.emit(this.snapshot.connection);
  }

  /** Fetch now (after a write, or on request). Coalesces with a request already in flight. */
  refresh(): void {
    const connection = this.snapshot.connection;
    if (!connection) return;
    if (this.inFlight) { this.queued = true; return; }
    this.clearTimer();
    void this.poll();
  }

  private stop(): void {
    this.generation++;
    this.clearTimer();
    this.controller?.abort();
    this.controller = null;
    this.inFlight = false;
    this.queued = false;
    this.waitingForVisible = false;
    this.failures = 0;
  }
  private clearTimer(): void {
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
  }
  private schedule(delay: number): void {
    this.clearTimer();
    this.timer = setTimeout(() => {
      this.timer = undefined;
      if (document.hidden) { this.waitingForVisible = true; return; }
      void this.poll();
    }, delay);
  }

  private async poll(): Promise<void> {
    const connection = this.snapshot.connection;
    if (!connection || this.inFlight) return;
    const generation = this.generation;
    const controller = new AbortController();
    this.controller = controller;
    this.inFlight = true;
    let next = POLL_MS;
    let keepPolling = true;
    try {
      const overview = await connection.api.overview(controller.signal);
      if (generation !== this.generation) return;
      this.failures = 0;
      this.patch({ overview, fetchedAt: Date.now(), link: 'live', failure: null, failingSince: null, awaiting: false });
    } catch (error) {
      if (generation !== this.generation || isAbort(error)) return;
      const failure = error instanceof ApiFailure ? error : new ApiFailure(0, 'The overview could not be loaded.');
      const failingSince = this.snapshot.connection?.failingSince ?? Date.now();
      if (failure.status === 401) {
        rememberSignIn(null);
        keepPolling = false;
        this.patch({ link: 'rejected', failure, failingSince });
      } else {
        this.failures++;
        next = Math.min(POLL_MS * 2 ** (this.failures - 1), MAX_BACKOFF_MS);
        this.patch({ link: 'stale', failure, failingSince });
      }
    } finally {
      if (generation === this.generation) {
        this.inFlight = false;
        this.controller = null;
      }
    }
    if (generation !== this.generation || !keepPolling) return;
    if (this.queued) {
      this.queued = false;
      void this.poll();
      return;
    }
    this.schedule(next);
  }
}

export const session = new Session();

/**
 * Sign out of the endpoint: revoke a password session on the node, then drop every credential and on-screen
 * confirmation from this tab. The node's answer decides the wording; nothing is claimed it didn't confirm.
 */
export async function signOut(): Promise<void> {
  const connection = session.getSnapshot().connection;
  if (!connection) return;
  // On the control plane the session belongs to the dashboard, whichever node is selected.
  const controlPlane = controlPlaneMode();
  const endpoint = controlPlane ? 'this dashboard' : connection.overview.node.id;
  const expiresAt = connection.api.expiresAt;
  const result = await session.signOut();
  if (result === 'superseded' || session.getSnapshot().connection) return;
  clearNotices();
  if (result === 'ended') notify(controlPlane ? 'Signed out. This dashboard ended this session. A sign-in proxy in front of it may keep its own session.' : `Signed out. ${endpoint} ended this session.`);
  else if (result === 'unconfirmed') {
    const until = expiresAt ? ` It expires on its own at ${new Date(expiresAt).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}.` : '';
    notify(`Signed out of this tab, but ${endpoint} didn’t confirm the session ended.${until}`);
  } else notify(`Disconnected from ${endpoint}. The account token was removed from this tab.`);
}

/** Drop the connection immediately without asking the node (it already rejected this credential). */
export function forget(): void {
  const connection = session.getSnapshot().connection;
  if (!connection) return;
  session.end({ url: connection.api.baseUrl, name: connection.account?.name ?? connection.overview.user.name, message: 'Signed out of this tab.', tone: 'info' }, connection);
  clearNotices();
}

export function useSession(): SessionSnapshot {
  return useSyncExternalStore(session.subscribe, session.getSnapshot);
}
