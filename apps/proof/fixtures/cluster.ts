import assert from 'node:assert/strict';
import { spawn, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { existsSync, statSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer, request, type ClientRequest, type Server } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import type { Group, GroupInput, GroupView, Monitor, MonitorInput, Observation, Overview } from '../../../packages/shared/src/index.js';

export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
export const CHECK_INTERVAL_MS = 1_000;
export const SYNC_INTERVAL_MS = 150;
export const NOTIFICATION_DELAY_MS = 750;
export const PEER_TIMEOUT_MS = 1_500;
// Give every candidate time to wake, send, and learn a successful receipt.
export const QUIET_MS = Math.max(
  CHECK_INTERVAL_MS * 4,
  SYNC_INTERVAL_MS * 12,
  NOTIFICATION_DELAY_MS * 6 + PEER_TIMEOUT_MS,
);
export const EVENT_TIMEOUT_MS = 30_000;

/** Resolve once before opening any fixtures; never fall back to the old TS node. */
export function compiledRuntime(): string {
  const binary = resolve(ROOT, process.env.CANARY_BIN?.trim() || join('.canary', 'bin', process.platform === 'win32' ? 'canary.exe' : 'canary'));
  assert.ok(existsSync(binary) && statSync(binary).isFile(),
    `Compiled Go runtime is absent at ${binary}. Run npm run build:node first, or set CANARY_BIN to a compiled canary executable.`);
  return binary;
}

export interface HttpTarget {
  url: string;
  state: 'up' | 'down';
  requests: { at: number; state: 'up' | 'down' }[];
}

export interface NodeProcess {
  id: string;
  url: string;
  port: number;
  db: string;
  adminToken: string;
  child?: ChildProcess;
  reservation?: Server;
  logs: string;
  lastExit?: string;
  stopping: boolean;
}

interface PeerExchange {
  from: string;
  to: string;
  method: string;
  path: string;
  clusterCredential: boolean;
  status: number | null;
  requestHeaders: Record<string, string>;
}

interface PeerLink {
  from: string;
  to: string;
  url: string;
  allowed: boolean;
  server: Server;
  inflight: Set<ClientRequest>;
}

export interface WebhookEvent {
  at: number;
  method: string;
  path: string;
  idempotencyKey: string | undefined;
  body: unknown;
  parseError?: string;
}

export interface Alert {
  event: WebhookEvent;
  monitorId: string;
  incidentId: string;
  kind: 'down' | 'recovery' | 'repeat';
  nodeId: string;
  observations: Observation[];
  /** Public incident identities can coalesce on reconnect; receipt IDs stay stable. */
  receiptId?: string;
}

export interface ApiResponse<T> {
  status: number;
  body: T;
  text: string;
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : {};
}

function textField(...values: unknown[]): string | undefined {
  return values.find((value): value is string => typeof value === 'string' && value.length > 0);
}

/** The prose webhook contract permits either flat identities or identity objects. */
export function alertFrom(event: WebhookEvent): Alert {
  assert.equal(event.method, 'POST', 'webhooks must use POST');
  assert.equal(event.path, '/webhook');
  assert.equal(event.parseError, undefined, 'webhook payload must be JSON');
  assert.ok(event.idempotencyKey, 'webhook needs a stable Idempotency-Key');
  const body = record(event.body);
  const notification = record(body.notification);
  const monitorId = textField(body.monitorId, notification.monitorId, record(body.monitor).id);
  const incidentId = textField(body.incidentId, notification.incidentId, record(body.incident).id);
  const nodeId = textField(body.nodeId, notification.nodeId, record(body.node).id, record(body.sendingNode).id);
  const kind = textField(body.kind, notification.kind);
  assert.ok(monitorId, 'webhook must identify its monitor');
  assert.ok(incidentId, 'webhook must identify its incident');
  assert.ok(nodeId, 'webhook must identify its sending node');
  assert.ok(kind === 'down' || kind === 'recovery' || kind === 'repeat', 'webhook must identify its notification kind');
  assert.ok(Array.isArray(body.observations), 'webhook must include observations');
  assert.ok(body.observations.length > 0, 'webhook observations cannot be empty');
  for (const observation of body.observations) {
    assert.equal(record(observation).monitorId, monitorId);
    assert.equal(typeof record(observation).nodeId, 'string');
    assert.ok(['up', 'down', 'unknown'].includes(String(record(observation).state)));
  }
  return { event, monitorId, incidentId, nodeId, kind, observations: body.observations as Observation[] };
}

async function listen(server: Server): Promise<string> {
  await new Promise<void>((resolveListen, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.off('error', reject);
      resolveListen();
    });
  });
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  return `http://127.0.0.1:${address.port}`;
}

async function closeServer(server: Server): Promise<void> {
  const closed = new Promise<void>((resolveClose, reject) => {
    server.close((error) => {
      if (error && (error as NodeJS.ErrnoException).code !== 'ERR_SERVER_NOT_RUNNING') reject(error);
      else resolveClose();
    });
  });
  server.closeAllConnections();
  await closed;
}

export class Cluster {
  readonly nodes: NodeProcess[] = [];
  readonly events: WebhookEvent[] = [];
  readonly exchanges: PeerExchange[] = [];
  readonly targetRequests: number[] = [];
  readonly targets: HttpTarget[] = [];
  readonly secrets: string[] = [];
  readonly clusterToken = this.secret();
  targetState: 'up' | 'down' = 'up';
  targetUrl = '';
  webhookUrl = '';
  directory = '';
  private readonly servers: Server[] = [];
  private readonly links: PeerLink[] = [];
  private readonly rawLogs = new Map<string, string>();
  private binary = '';
  private closed = false;

  constructor(private readonly childEnvironment: NodeJS.ProcessEnv = {}) {}

  secret(): string {
    const token = randomBytes(32).toString('hex');
    this.secrets.push(token);
    return token;
  }

  redact(value: string): string {
    let safe = value;
    for (const secret of [...this.secrets, this.webhookUrl].filter(Boolean)) {
      safe = safe.replaceAll(secret, '[REDACTED]');
    }
    return safe.replace(/(Bearer\s+)[^\s"',}]+/gi, '$1[REDACTED]');
  }

  async initialize(ids = ['proof-a', 'proof-b', 'proof-c']): Promise<void> {
    this.binary = compiledRuntime();
    this.directory = await mkdtemp(join(tmpdir(), 'uptime-canary-proof-'));

    const target = createServer((req, res) => {
      if (req.url !== '/service') {
        res.writeHead(404).end();
        return;
      }
      this.targetRequests.push(Date.now());
      res.writeHead(this.targetState === 'up' ? 200 : 503, { 'Content-Type': 'text/plain', 'Cache-Control': 'no-store' });
      res.end(this.targetState);
    });
    this.servers.push(target);
    this.targetUrl = `${await listen(target)}/service`;

    const sink = createServer((req, res) => {
      const chunks: Buffer[] = [];
      let bytes = 0;
      req.on('data', (chunk: Buffer) => {
        bytes += chunk.length;
        if (bytes > 1_048_576) req.destroy(new Error('proof webhook exceeds 1 MiB'));
        else chunks.push(chunk);
      });
      req.on('error', () => res.destroy());
      req.on('end', () => {
        let body: unknown;
        let parseError: string | undefined;
        try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
        catch { parseError = 'invalid JSON'; }
        const header = req.headers['idempotency-key'];
        this.events.push({
          at: Date.now(), method: req.method ?? '', path: req.url ?? '',
          idempotencyKey: typeof header === 'string' ? header : undefined,
          body, ...(parseError ? { parseError } : {}),
        });
        res.writeHead(202, { 'Content-Type': 'application/json' }).end('{"accepted":true}');
      });
    });
    this.servers.push(sink);
    this.webhookUrl = `${await listen(sink)}/webhook`;

    // Reserve every runtime port until that particular child is ready to start.
    for (const id of ids) {
      const reservation = createServer();
      this.servers.push(reservation);
      const url = await listen(reservation);
      this.nodes.push({
        id, url, port: Number(new URL(url).port), db: join(this.directory, `${id}.sqlite`),
        adminToken: this.secret(), reservation, logs: '', stopping: false,
      });
    }
    for (const from of this.nodes) {
      for (const to of this.nodes) {
        if (from !== to) await this.createLink(from, to);
      }
    }
  }

  /** Each target has a separate real listener and can fail independently. */
  async createTarget(): Promise<HttpTarget> {
    const target: HttpTarget = { url: '', state: 'up', requests: [] };
    const server = createServer((req, res) => {
      if (req.url !== '/service') { res.writeHead(404).end(); return; }
      target.requests.push({ at: Date.now(), state: target.state });
      res.writeHead(target.state === 'up' ? 200 : 503, { 'Content-Type': 'text/plain', 'Cache-Control': 'no-store' });
      res.end(target.state);
    });
    this.servers.push(server);
    target.url = `${await listen(server)}/service`;
    this.targets.push(target);
    return target;
  }

  private async createLink(from: NodeProcess, to: NodeProcess): Promise<void> {
    const inflight = new Set<ClientRequest>();
    const link: PeerLink = { from: from.id, to: to.id, url: '', allowed: true, server: createServer(), inflight };
    link.server.on('request', (req, res) => {
      const exchange: PeerExchange = {
        from: from.id, to: to.id, method: req.method ?? 'GET',
        path: new URL(req.url ?? '/', to.url).pathname,
        clusterCredential: req.headers.authorization === `Bearer ${this.clusterToken}`, status: null,
        requestHeaders: Object.fromEntries(Object.entries(req.headers)
          .filter(([name, value]) => value !== undefined && ![
            'authorization', 'proxy-authorization', 'cookie', 'host', 'connection', 'content-length', 'transfer-encoding',
          ].includes(name))
          .map(([name, value]) => [name, Array.isArray(value) ? value.join(', ') : value!])),
      };
      this.exchanges.push(exchange);
      if (this.exchanges.length > 300) this.exchanges.shift();
      if (!link.allowed) {
        exchange.status = 503;
        res.writeHead(503, { 'Content-Type': 'application/json' }).end('{"error":"proof peer link blocked"}');
        req.resume();
        return;
      }
      const upstream = request(new URL(req.url ?? '/', to.url), {
        method: req.method,
        headers: { ...req.headers, host: new URL(to.url).host },
      }, (reply) => {
        exchange.status = reply.statusCode ?? 502;
        res.writeHead(exchange.status, reply.headers);
        reply.on('error', () => res.destroy());
        reply.pipe(res);
      });
      inflight.add(upstream);
      upstream.on('close', () => inflight.delete(upstream));
      upstream.on('error', () => {
        exchange.status ??= 502;
        if (!res.headersSent) res.writeHead(502).end();
        else res.destroy();
      });
      req.on('error', () => upstream.destroy());
      req.on('aborted', () => upstream.destroy());
      res.on('close', () => { if (!res.writableFinished) upstream.destroy(); });
      req.pipe(upstream);
    });
    this.servers.push(link.server);
    link.url = await listen(link.server);
    this.links.push(link);
  }

  async start(node: NodeProcess, binary = this.binary): Promise<void> {
    assert.ok(binary && existsSync(binary) && statSync(binary).isFile(), 'Call Cluster.initialize() with a valid compiled runtime.');
    assert.ok(!node.child || node.child.exitCode !== null || node.child.signalCode !== null, `${node.id} already runs`);
    if (node.reservation) {
      await closeServer(node.reservation);
      node.reservation = undefined;
    }
    const env = { ...process.env };
    for (const key of Object.keys(env)) if (key.startsWith('CANARY_')) delete env[key];
    const peers = this.links.filter((link) => link.from === node.id).map((link) => ({
      id: link.to, name: link.to, url: link.url,
    }));
    node.stopping = false;
    node.lastExit = undefined;
    const child = spawn(binary, ['serve'], {
      cwd: ROOT, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
      env: {
        ...env, ...this.childEnvironment,
        CANARY_NODE_ID: node.id, CANARY_NODE_NAME: node.id,
        CANARY_HOST: '127.0.0.1', CANARY_PORT: String(node.port), CANARY_DB: node.db,
        CANARY_ADMIN_TOKEN: node.adminToken, CANARY_CLUSTER_TOKEN: this.clusterToken,
        CANARY_PEERS: JSON.stringify(peers), CANARY_SYNC_INTERVAL_MS: String(SYNC_INTERVAL_MS),
        CANARY_NOTIFICATION_DELAY_MS: String(NOTIFICATION_DELAY_MS),
        CANARY_PEER_TIMEOUT_MS: String(PEER_TIMEOUT_MS), CANARY_CORS_ORIGINS: 'http://127.0.0.1',
      },
    });
    node.child = child;
    const capture = (chunk: Buffer) => {
      // Join chunks before redacting so a token split across writes is still hidden.
      const raw = ((this.rawLogs.get(node.id) ?? '') + chunk.toString('utf8')).slice(-20_000);
      this.rawLogs.set(node.id, raw);
      node.logs = this.redact(raw).slice(-10_000);
    };
    child.stdout?.on('data', capture);
    child.stderr?.on('data', capture);
    child.on('error', (error) => { node.lastExit = this.redact(error.message); });
    child.on('exit', (code, signal) => { node.lastExit = `exit=${code} signal=${signal}`; });
    await this.wait(`${node.id} public /healthz`, async () => {
      const response = await fetch(`${node.url}/healthz`, { signal: AbortSignal.timeout(750) });
      return response.status === 200;
    }, 20_000);
  }

  async startAll(): Promise<void> {
    const starts = await Promise.allSettled(this.nodes.map((node) => this.start(node)));
    const failure = starts.find((result) => result.status === 'rejected');
    if (failure?.status === 'rejected') throw failure.reason;
  }

  async stop(node: NodeProcess): Promise<void> {
    node.stopping = true;
    const child = node.child;
    if (!child || child.pid === undefined || child.exitCode !== null || child.signalCode !== null) return;
    const exited = new Promise<void>((resolveExit) => {
      child.once('exit', () => resolveExit());
      child.once('error', () => resolveExit());
    });
    // A real process death, with no graceful receipt handoff to its peers.
    child.kill('SIGKILL');
    await Promise.race([
      exited,
      delay(5_000, undefined, { ref: false }).then(() => { throw new Error(`Could not terminate ${node.id}`); }),
    ]);
  }

  partition(groups: string[][]): void {
    assert.deepEqual(groups.flat().slice().sort(), this.nodes.map((node) => node.id).sort());
    for (const link of this.links) {
      link.allowed = groups.some((group) => group.includes(link.from) && group.includes(link.to));
      if (!link.allowed) for (const upstream of link.inflight) upstream.destroy();
    }
  }

  reconnect(): void {
    for (const link of this.links) link.allowed = true;
  }

  async api<T>(node: NodeProcess, path: string, options: {
    method?: string; body?: unknown; token?: string | null; headers?: Record<string, string>;
  } = {}): Promise<ApiResponse<T>> {
    const token = options.token === undefined ? node.adminToken : options.token;
    const response = await fetch(`${node.url}${path}`, {
      method: options.method ?? 'GET', signal: AbortSignal.timeout(2_000),
      headers: {
        ...options.headers,
        ...(token === null ? {} : { Authorization: `Bearer ${token}` }),
        ...(options.body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
    });
    const raw = await response.text();
    let body: unknown;
    try { body = raw ? JSON.parse(raw) : undefined; }
    catch { throw new Error(this.redact(`${node.id} ${path}: expected JSON, got ${raw.slice(0, 300)}`)); }
    return { status: response.status, body: body as T, text: raw };
  }

  async overview(node: NodeProcess): Promise<Overview> {
    const response = await this.api<Overview>(node, '/api/overview');
    assert.equal(response.status, 200, `${node.id}: overview status`);
    // Groups are optional for an empty overview; all original public arrays remain required.
    for (const key of ['monitors', 'nodes', 'incidents', 'notifications'] as const) {
      assert.ok(Array.isArray(response.body[key]), `${node.id}: overview.${key} must be an array`);
    }
    assert.ok(response.body.groups === undefined || Array.isArray(response.body.groups),
      `${node.id}: overview.groups, when present, must be an array`);
    return response.body;
  }

  async overviews(nodes = this.nodes): Promise<Overview[]> {
    return Promise.all(nodes.map((node) => this.overview(node)));
  }

  async history(node: NodeProcess, monitorId: string): Promise<Observation[]> {
    const response = await this.api<Observation[]>(node, `/api/monitors/${monitorId}/history?limit=100`);
    assert.equal(response.status, 200, `${node.id}: history status`);
    assert.ok(Array.isArray(response.body));
    return response.body;
  }

  async createMonitor(input: MonitorInput, node = this.nodes[0]!): Promise<Monitor> {
    const response = await this.api<Monitor>(node, '/api/monitors', { method: 'POST', body: input });
    assert.equal(response.status, 201, this.redact(`create monitor: ${response.text}`));
    assert.ok(response.body.id);
    assert.ok(response.body.revision);
    return response.body;
  }

  async groups(node = this.nodes[0]!): Promise<GroupView[]> {
    const response = await this.api<GroupView[]>(node, '/api/groups');
    assert.equal(response.status, 200, `${node.id}: groups status`);
    assert.ok(Array.isArray(response.body), `${node.id}: groups must be an array`);
    return response.body;
  }

  async createGroup(input: GroupInput, node = this.nodes[0]!): Promise<Group> {
    const response = await this.api<Group>(node, '/api/groups', { method: 'POST', body: input });
    assert.equal(response.status, 201, this.redact(`create group: ${response.text}`));
    assert.ok(response.body.id);
    assert.ok(response.body.revision);
    return response.body;
  }

  async wait(label: string, predicate: () => Promise<boolean> | boolean, timeout = EVENT_TIMEOUT_MS): Promise<void> {
    const deadline = Date.now() + timeout;
    let lastError = '';
    while (Date.now() < deadline) {
      if (this.closed) throw new Error(`Proof fixture closed while waiting for ${label}`);
      for (const node of this.nodes) {
        if (node.child && !node.stopping && (node.lastExit || node.child.exitCode !== null || node.child.signalCode !== null)) {
          throw new Error(this.redact(`Unexpected runtime death while waiting for ${label}\n${this.diagnostics()}`));
        }
      }
      try { if (await predicate()) return; }
      catch (error) { lastError = error instanceof Error ? error.message : String(error); }
      await delay(100);
    }
    throw new Error(this.redact(`Timed out after ${timeout}ms waiting for ${label}${lastError ? `\nLast condition error: ${lastError}` : ''}\n${this.diagnostics()}`));
  }

  async quiet(label: string, count = this.events.length, duration = QUIET_MS): Promise<void> {
    const deadline = Date.now() + duration;
    while (Date.now() < deadline) {
      if (this.closed) throw new Error(`Proof fixture closed during ${label}`);
      assert.equal(this.events.length, count, this.redact(`${label}: unexpected extra webhook\n${this.diagnostics()}`));
      for (const node of this.nodes) {
        assert.ok(node.stopping || !node.lastExit, `${label}: ${node.id} exited: ${node.lastExit}`);
      }
      await delay(Math.min(100, Math.max(1, deadline - Date.now())));
    }
    assert.equal(this.events.length, count, this.redact(`${label}: extra webhook at end of observation window\n${this.diagnostics()}`));
  }

  alerts(since = 0): Alert[] {
    return this.events.slice(since).map(alertFrom);
  }

  successfulPeerExchange(): PeerExchange | undefined {
    return this.exchanges.find((exchange) => exchange.clusterCredential && exchange.status !== null && exchange.status >= 200 && exchange.status < 300);
  }

  diagnostics(): string {
    const webhookSummary = this.events.map((event) => {
      const body = record(event.body);
      return { at: event.at, kind: body.kind ?? record(body.notification).kind, monitorId: body.monitorId,
        incidentId: body.incidentId, nodeId: body.nodeId, parseError: event.parseError };
    });
    return this.redact(JSON.stringify({
      targetState: this.targetState, targetRequests: this.targetRequests.length, webhooks: webhookSummary,
      targets: this.targets.map(({ url, state, requests }) => ({ url, state, requests: requests.length })),
      links: this.links.map(({ from, to, allowed }) => ({ from, to, allowed })),
      peerExchanges: this.exchanges.slice(-12).map(({ requestHeaders: _headers, ...exchange }) => exchange),
      nodes: this.nodes.map((node) => ({ id: node.id, pid: node.child?.pid, stopped: node.stopping, exit: node.lastExit, logs: node.logs })),
    }, null, 2));
  }

  async cleanup(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    const errors: unknown[] = [];
    const stops = await Promise.allSettled(this.nodes.map((node) => this.stop(node)));
    for (const result of stops) if (result.status === 'rejected') errors.push(result.reason);
    for (const link of this.links) for (const upstream of link.inflight) upstream.destroy();
    const closes = await Promise.allSettled(this.servers.map(closeServer));
    for (const result of closes) if (result.status === 'rejected') errors.push(result.reason);
    if (this.directory) {
      try { await rm(this.directory, { recursive: true, force: true, maxRetries: 8, retryDelay: 100 }); }
      catch (error) { errors.push(error); }
    }
    if (errors.length) throw new Error(this.redact(`Proof cleanup failed: ${errors.map(String).join('; ')}`));
  }
}
