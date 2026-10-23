import assert from 'node:assert/strict';
import { createServer, type IncomingHttpHeaders, type Server } from 'node:http';
import { createServer as createTCPServer, type Server as TCPServer, type Socket } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import type { MonitorInput } from '../../../packages/shared/src/index.js';
import { CHECK_INTERVAL_MS, Cluster, QUIET_MS, type HttpTarget, type NodeProcess } from './cluster.js';

// Keep black-box expectations independent from the runtime's delivery implementation.
export interface ChannelInput {
  name: string;
  enabled: boolean;
  config: { kind: string; [key: string]: unknown };
}
export interface Channel extends ChannelInput { id: string; revision: string; updatedAt: number; deleted?: boolean }
export interface ChannelView { channel: Channel; configConflict: boolean }
export interface DeliveryAttempt {
  id: string; nodeId: string; monitorId?: string; incidentId?: string; kind: string;
  destinationId?: string; channelId?: string; provider: string; state: string; failures: number;
  nextAttemptAt?: number; lastAttemptAt?: number; httpStatus?: number; errorCode?: string; providerId?: string;
}
export interface ProviderRequest {
  at: number; path: string; method: string; headers: IncomingHttpHeaders;
  body: Record<string, any>; fields: Record<string, string[]>;
}
export interface ProviderReply { status: number; body?: unknown; headers?: Record<string, string> }

async function listen(server: Server | TCPServer): Promise<number> {
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => { server.off('error', reject); resolve(); });
  });
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  return address.port;
}

/** Only loopback endpoints exist; failures are recorded for the test, never thrown in a request callback. */
export class ProviderSink {
  url = '';
  readonly requests: ProviderRequest[] = [];
  readonly errors: string[] = [];
  private readonly server = createServer((req, res) => {
    void (async () => {
      const chunks: Buffer[] = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        assert.ok(size <= 1_048_576, 'fixture request exceeded limit');
        chunks.push(chunk);
      }
      const raw = Buffer.concat(chunks);
      let body = {};
      const fields: Record<string, string[]> = {};
      if (req.headers['content-type']?.startsWith('multipart/form-data')) {
        const form = await new Response(raw, { headers: { 'Content-Type': req.headers['content-type'] } }).formData();
        for (const [name, value] of form) {
          assert.equal(typeof value, 'string', 'only textual provider form fields are expected');
          (fields[name] ??= []).push(String(value));
        }
      } else body = JSON.parse(raw.toString('utf8'));
      const request: ProviderRequest = {
        at: Date.now(), method: req.method ?? '', path: req.url ?? '', headers: req.headers, body, fields,
      };
      this.requests.push(request);
      const reply = this.respond(request);
      res.writeHead(reply.status, { 'Content-Type': 'application/json', ...reply.headers });
      res.end(reply.body === undefined ? '' : JSON.stringify(reply.body));
    })().catch(() => {
      // Exception text may contain credentials or addresses from an assertion.
      this.errors.push('Provider fixture rejected an invalid request');
      if (!res.headersSent) res.writeHead(400);
      res.end('{"error":"invalid fixture request"}');
    });
  });

  constructor(public respond: (request: ProviderRequest) => ProviderReply) {}

  async start(): Promise<void> { this.url = `http://127.0.0.1:${await listen(this.server)}`; }
  assertHealthy(): void { assert.deepEqual(this.errors, []); }
  async close(): Promise<void> {
    const closed = new Promise<void>((resolve, reject) => this.server.close(error => error ? reject(error) : resolve()));
    this.server.closeAllConnections();
    await closed;
  }
}

export interface SMTPMessage { at: number; from: string; recipients: string[]; data: string }

/** A real SMTP envelope/DATA exchange with final acceptance, using explicit local plaintext without credentials. */
export class SMTPSink {
  port = 0;
  readonly messages: SMTPMessage[] = [];
  readonly commands: string[] = [];
  private readonly sockets = new Set<Socket>();
  private readonly server = createTCPServer(socket => {
    this.sockets.add(socket);
    socket.on('close', () => this.sockets.delete(socket));
    socket.on('error', () => {});
    socket.setTimeout(10_000, () => socket.destroy());
    socket.write('220 localhost proof SMTP\r\n');
    let buffer = '', from = '', recipients: string[] = [], data: string[] | undefined;
    socket.on('data', chunk => {
      buffer += chunk.toString('utf8');
      if (buffer.length > 1_048_576) { socket.destroy(); return; }
      for (let end = buffer.indexOf('\r\n'); end >= 0; end = buffer.indexOf('\r\n')) {
        const line = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        if (data) {
          if (line === '.') {
            this.messages.push({ at: Date.now(), from, recipients: [...recipients], data: data.join('\r\n') });
            data = undefined;
            socket.write('250 2.0.0 accepted by local proof fixture\r\n');
          } else data.push(line.startsWith('..') ? line.slice(1) : line);
          continue;
        }
        this.commands.push(line);
        if (line.startsWith('EHLO ')) socket.write('250-localhost\r\n250 SIZE 1048576\r\n');
        else if (line.startsWith('MAIL FROM:')) { from = line.slice(10); recipients = []; socket.write('250 sender accepted\r\n'); }
        else if (line.startsWith('RCPT TO:')) { recipients.push(line.slice(8)); socket.write('250 recipient accepted\r\n'); }
        else if (line === 'DATA') { data = []; socket.write('354 send message data\r\n'); }
        else if (line === 'RSET') { recipients = []; socket.write('250 reset\r\n'); }
        else if (line === 'QUIT') socket.end('221 goodbye\r\n');
        else socket.write('500 unexpected fixture command\r\n');
      }
    });
  });
  async start(): Promise<void> { this.port = await listen(this.server); }
  async close(): Promise<void> {
    const closed = new Promise<void>((resolve, reject) => this.server.close(error => error ? reject(error) : resolve()));
    for (const socket of this.sockets) socket.destroy();
    await closed;
  }
}

export function monitorInput(cluster: Cluster, name: string, notificationIds: string[], target?: HttpTarget): MonitorInput {
  return {
    name, type: 'http', target: target?.url ?? cluster.targetUrl,
    intervalMs: CHECK_INTERVAL_MS, timeoutMs: 500, nodeIds: cluster.nodes.map(node => node.id),
    enabled: true, expectedStatusCodes: [200], notificationIds, repeatAfterMs: 0,
  };
}

export function webhookInput(name: string, url: string, bearerToken = ''): ChannelInput {
  return { name, enabled: true, config: { kind: 'webhook', webhook: { url, bearerToken, allowInsecureHTTP: true } } };
}

export function emailInput(kind: string, name: string, providerConfig: object, recipients = ['alpha@example.test']): ChannelInput {
  return {
    name, enabled: true, config: {
      kind, sender: { email: 'canary@example.test', name: 'Local Canary' },
      recipients: recipients.map(email => ({ email })), [kind]: providerConfig,
      templates: {
        subject: '{{event_kind}} / {{monitor_name}}',
        text: '{{event_kind}}\n{{monitor_name}}\n{{monitor_id}}\n{{incident_id}}\n{{idempotency_key}}\n{{observations_summary}}',
      },
    },
  };
}

export async function createChannel(cluster: Cluster, input: ChannelInput, node = cluster.nodes[0]!): Promise<Channel> {
  const response = await cluster.api<Channel>(node, '/api/notification-channels', { method: 'POST', body: input });
  assert.equal(response.status, 201, cluster.redact(response.text));
  assert.ok(response.body.id && response.body.revision);
  return response.body;
}

export async function attempts(cluster: Cluster, node: NodeProcess, query = ''): Promise<DeliveryAttempt[]> {
  const response = await cluster.api<DeliveryAttempt[]>(node, `/api/notification-deliveries?limit=1000${query ? `&${query}` : ''}`);
  assert.equal(response.status, 200);
  assert.ok(Array.isArray(response.body));
  return response.body;
}

export async function channelsReplicated(cluster: Cluster, channels: Channel[]): Promise<void> {
  await cluster.wait('channel revisions replicate between independent SQLite nodes', async () =>
    (await Promise.all(cluster.nodes.map(node => cluster.api<ChannelView[]>(node, '/api/notification-channels'))))
      .every(response => response.status === 200 && channels.every(channel => response.body.some(view =>
        view.channel.id === channel.id && view.channel.revision === channel.revision && !view.configConflict))));
}

export async function observed(cluster: Cluster, monitorId: string, state: 'up' | 'down', nodes = cluster.nodes): Promise<void> {
  await cluster.wait(`real probes observe ${state}`, async () => (await cluster.overviews(nodes)).every(overview => {
    const view = overview.monitors.find(item => item.monitor.id === monitorId);
    return view?.state === state && nodes.every(node => view.observations.some(o => o.nodeId === node.id && o.state === state));
  }));
}

/** Check throughout the candidate failover windows, rather than taking one count snapshot. */
export async function stable(cluster: Cluster, label: string, check: () => void, duration = QUIET_MS): Promise<void> {
  const deadline = Date.now() + duration;
  do {
    check();
    for (const node of cluster.nodes) assert.ok(node.stopping || !node.lastExit, `${label}: runtime exited`);
    await delay(Math.min(100, Math.max(1, deadline - Date.now())));
  } while (Date.now() < deadline);
  check();
}

export function assertSafe(text: string, forbidden: string[]): void {
  for (const value of forbidden.filter(Boolean)) assert.ok(!text.includes(value), 'API exposed private provider configuration or response text');
}
