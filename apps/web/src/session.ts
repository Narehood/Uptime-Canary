import { useSyncExternalStore } from 'react';
import type { AccountView, Overview } from '@uptime-canary/shared';
import { ApiFailure, CanaryApi, isAbort } from './api';
import { clearNotices, notify } from './notices';

export const POLL_MS = 5_000;
const MAX_BACKOFF_MS = 30_000;

/**
 * live: the last overview request succeeded.
 * stale: the endpoint stopped answering; the last good overview stays visible and clearly marked.
 * rejected: the endpoint refused the credential (401); polling stops until the user signs in again.
 */
export type LinkState = 'live' | 'stale' | 'rejected';

export interface Connection {
  /** Nonsecret, in-memory identity. Polls and own-token adoption keep it; every successful connect replaces it. */
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
}

/**
 * Holds the only copy of the credential (inside `CanaryApi`) in memory. Nothing is written to browser storage;
 * reloading or closing the tab forgets it and asks for a sign-in again. Polls never overlap and are aborted on
 * switch or sign-out.
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
    this.snapshot = { connection, farewell };
    for (const listener of this.listeners) listener();
  }
  private patch(changes: Partial<Connection>): void {
    if (this.snapshot.connection) this.emit({ ...this.snapshot.connection, ...changes });
  }

  /**
   * Verifies the node first; the current connection (if any) stays untouched unless the new one answers.
   * A replaced password session is signed out on its node, since this tab can never use it again. So is a new
   * password session whose connect fails or is cancelled: the caller drops it.
   */
  async connect(api: CanaryApi, signal?: AbortSignal): Promise<Overview> {
    let overview: Overview;
    let account: AccountView | null;
    try {
      [overview, account] = await Promise.all([
        api.overview(signal),
        // Only a missing route (404) means the node predates accounts; any other failure fails the connect.
        api.me(signal).catch(error => { if (error instanceof ApiFailure && error.status === 404) return null; throw error; }),
      ]);
      signal?.throwIfAborted();
    } catch (error) {
      if (api.credential === 'session' && !this.snapshot.connection?.api.sameCredential(api)) void api.logout().catch(() => undefined);
      throw error;
    }
    const previous = this.snapshot.connection;
    this.stop();
    clearNotices();
    this.emit({ identity: ++this.nextIdentity, api, overview, account, fetchedAt: Date.now(), link: 'live', failure: null, failingSince: null });
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
    this.stop();
    this.emit(null, farewell);
  }

  /** After rotating this account's own token, keep watching with the new one (the old one no longer works). */
  adoptToken(token: string, expected: Connection): void {
    const connection = this.snapshot.connection;
    if (!connection || !this.isCurrent(expected) || connection.api.credential !== 'token') return;
    // Invalidate any poll made with the replaced token without remounting the one-time token panel.
    this.stop();
    this.patch({ api: new CanaryApi(connection.api.baseUrl, token) });
    this.refresh();
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
      this.patch({ overview, fetchedAt: Date.now(), link: 'live', failure: null, failingSince: null });
    } catch (error) {
      if (generation !== this.generation || isAbort(error)) return;
      const failure = error instanceof ApiFailure ? error : new ApiFailure(0, 'The overview could not be loaded.');
      const failingSince = this.snapshot.connection?.failingSince ?? Date.now();
      if (failure.status === 401) {
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
  const endpoint = connection.overview.node.id;
  const expiresAt = connection.api.expiresAt;
  const result = await session.signOut();
  if (result === 'superseded' || session.getSnapshot().connection) return;
  clearNotices();
  if (result === 'ended') notify(`Signed out. ${endpoint} ended this session.`);
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
