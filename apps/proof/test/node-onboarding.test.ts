import assert from 'node:assert/strict';
import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createServer as httpServer, request } from 'node:http';
import { createServer as httpsServer } from 'node:https';
import { join } from 'node:path';
import test from 'node:test';
import type { NodeInvitation, RegisteredNode } from '../../../packages/shared/src/dashboard.js';
import { Cluster, ROOT, compiledRuntime } from '../fixtures/cluster.js';

test('node-initiated pairing survives process restarts and dashboard loss without interrupting monitoring', { timeout: 120000 }, async t => {
  const trust = join(ROOT, 'apps/proof/fixtures/localhost-cert.pem');
  const nodeEnvironment: NodeJS.ProcessEnv = { SSL_CERT_FILE: trust };
  const cluster = new Cluster(nodeEnvironment);
  let dashboard: ChildProcess | undefined;
  let nodeRequests = 0, leakedCookie = false;
  async function port(): Promise<number> {
    const reservation = httpServer(); reservation.listen(0, '127.0.0.1'); await once(reservation, 'listening');
    const address = reservation.address(); assert.ok(address && typeof address !== 'string');
    await new Promise<void>(resolve => reservation.close(() => resolve())); return address.port;
  }
  async function stopDashboard(): Promise<void> {
    const child = dashboard; dashboard = undefined;
    if (!child || child.exitCode !== null || child.signalCode !== null) return;
    const stopped = once(child, 'exit'); child.kill('SIGKILL');
    await Promise.race([stopped, new Promise<never>((_, reject) => { const timer = setTimeout(() => reject(Error('dashboard did not terminate')), 5000); timer.unref(); })]);
  }
  t.after(async () => { await stopDashboard(); await cluster.cleanup(); });
  await cluster.initialize(['outbound-proof']);
  const [node] = cluster.nodes; assert.ok(node);
  const uiPort = await port(), connectionPort = await port();
  const origin = `http://127.0.0.1:${uiPort}`;
  const assets = join(cluster.directory, 'assets'), identity = join(cluster.directory, 'dashboard-identity');
  await mkdir(assets); await writeFile(join(assets, 'index.html'), '<html><body>Private proof</body></html>');
  // Only this test's loopback certificate is trusted, with real TLS verification.
  const tls = httpsServer({ cert: await readFile(trust), key: await readFile(join(ROOT, 'apps/proof/fixtures/localhost-key.pem')) }, (incoming, response) => {
    assert.ok(nodeRequests++ < 1000, 'bounded connection request evidence');
    if (incoming.headers.cookie) leakedCookie = true;
    assert.ok(['/connect/v1/enroll', '/connect/v1/open', '/connect/v1/poll', '/connect/v1/result'].includes(incoming.url || ''), 'fixed connection-only routes');
    const upstream = request({ host: '127.0.0.1', port: connectionPort, path: incoming.url, method: incoming.method, headers: incoming.headers, timeout: 22000 }, result => { response.writeHead(result.statusCode || 502, result.headers); result.pipe(response); });
    upstream.on('timeout', () => upstream.destroy());
    upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end(); });
    response.on('close', () => { if (!response.writableEnded) upstream.destroy(); });
    incoming.pipe(upstream);
  });
  tls.listen(0, '127.0.0.1'); await once(tls, 'listening');
  const tlsAddress = tls.address(); assert.ok(tlsAddress && typeof tlsAddress !== 'string');
  const connectionOrigin = `https://127.0.0.1:${tlsAddress.port}`;
  t.after(async () => { tls.closeAllConnections(); await new Promise<void>(resolve => tls.close(() => resolve())); });
  async function startDashboard(): Promise<void> {
    const environment = { ...process.env };
    for (const key of Object.keys(environment)) if (key.startsWith('CANARY_')) delete environment[key];
    dashboard = spawn(compiledRuntime(), ['dashboard'], { cwd: cluster.directory, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], env: { ...environment,
      CANARY_DASHBOARD_DATA: join(cluster.directory, 'dashboard-data'), CANARY_DASHBOARD_IDENTITY: identity,
      CANARY_DASHBOARD_ASSETS: assets, CANARY_DASHBOARD_HOST: '127.0.0.1', CANARY_DASHBOARD_PORT: String(uiPort),
      CANARY_DASHBOARD_ORIGINS: origin, CANARY_DASHBOARD_CONNECTION_HOST: '127.0.0.1', CANARY_DASHBOARD_CONNECTION_PORT: String(connectionPort), CANARY_DASHBOARD_CONNECTION_URL: connectionOrigin } });
    let bytes = 0;
    for (const stream of [dashboard.stdout!, dashboard.stderr!]) stream.on('data', (chunk: Buffer) => { bytes += chunk.length; if (bytes > 65536) dashboard?.kill('SIGKILL'); });
    await cluster.wait('dashboard readiness without a node', async () => { try { return (await fetch(origin + '/readyz', { signal: AbortSignal.timeout(500) })).ok; } catch { return false; } }, 15000);
  }
  let owner = '';
  async function api(path: string, method = 'GET', body?: unknown, selection?: string): Promise<{ status: number; body: any; text: string }> {
    const response = await fetch(origin + path, { method, headers: { ...(owner ? { Authorization: 'Bearer ' + owner } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(selection ? { 'X-Canary-Registered-Node': selection } : {}) }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(15000) });
    const text = await response.text(); return { status: response.status, body: text ? JSON.parse(text) : null, text };
  }
  await startDashboard();
  const code = (await readFile(join(identity, 'setup.code'), 'utf8')).trim(); cluster.secrets.push(code);
  const password = 'Private onboarding proof password'; cluster.secrets.push(password);
  assert.equal((await api('/api/dashboard/setup', 'POST', { name: 'owner', password, code })).status, 201);
  const login = await api('/api/auth/login', 'POST', { name: 'owner', password });
  assert.equal(login.status, 200); owner = login.body.token; cluster.secrets.push(owner);
  const created = await api('/api/dashboard/invitations', 'POST', { label: 'VPN node', nodeId: node.id });
  assert.equal(created.status, 201);
  const invitation: NodeInvitation & { token: string } = created.body; cluster.secrets.push(invitation.token);
  nodeEnvironment.CANARY_DASHBOARD_URL = connectionOrigin; nodeEnvironment.CANARY_DASHBOARD_ENROLL_TOKEN = invitation.token;
  await cluster.start(node);
  await cluster.wait('node connection awaiting explicit confirmation', async () => (await api('/api/dashboard/invitations/' + invitation.id)).body.state === 'connected', 15000);
  assert.deepEqual((await api('/api/dashboard/nodes')).body, [], 'unconfirmed pairing has no monitoring registration');
  const pending = await api('/api/dashboard/invitations/' + invitation.id);
  assert.ok(node.logs.includes(pending.body.node.fingerprint), 'administrator can compare fingerprint with production node output');
  assert.ok(!pending.text.includes(invitation.token), 'invitation secret never reappears');
  const confirmed = await api('/api/dashboard/invitations/' + invitation.id + '/confirm', 'POST');
  assert.equal(confirmed.status, 201); const row: RegisteredNode = confirmed.body;
  assert.equal(row.connection, 'outbound'); assert.equal(row.url, undefined);
  assert.equal((await api('/api/overview', 'GET', undefined, row.id)).status, 200);
  const monitor = await cluster.createMonitor({ name: 'Independent while dashboard unavailable', type: 'http', target: cluster.targetUrl,
    intervalMs: 1000, timeoutMs: 500, nodeIds: [node.id], enabled: true, expectedStatusCodes: [200], webhookUrl: cluster.webhookUrl, repeatAfterMs: 0 });
  async function freshState(state: 'up' | 'down', since: number): Promise<void> {
    await cluster.wait('independent fresh ' + state, async () => (await cluster.history(node, monitor.id)).some(sample => sample.state === state && sample.checkedAt >= since));
  }
  await freshState('up', Date.now());
  await stopDashboard();
  cluster.targetState = 'down'; await freshState('down', Date.now());
  await cluster.wait('notification while dashboard stopped', () => cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'down'));
  cluster.targetState = 'up'; await freshState('up', Date.now());
  await cluster.wait('recovery while dashboard stopped', () => cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'recovery'));
  await startDashboard();
  await cluster.wait('reconnect after dashboard restart', async () => (await api('/api/overview', 'GET', undefined, row.id)).status === 200, 15000);
  await cluster.stop(node); nodeEnvironment.CANARY_DASHBOARD_ENROLL_TOKEN = '';
  await cluster.start(node);
  await cluster.wait('retained device credential without invitation', async () => (await api('/api/overview', 'GET', undefined, row.id)).status === 200, 15000);
  assert.equal((await api('/api/dashboard/nodes')).body.length, 1, 'restart never added a duplicate registration');
  assert.equal((await api('/api/dashboard/nodes/' + row.id, 'DELETE')).status, 204);
  assert.equal((await api('/api/overview', 'GET', undefined, row.id)).status, 404);
  const accounts = await cluster.api<any[]>(node, '/api/accounts');
  assert.equal(accounts.status, 200); assert.equal(accounts.body.length, 1, 'removal revoked all three role accounts');
  assert.equal(leakedCookie, false);
  assert.ok(nodeRequests > 10, 'real requests crossed the node-initiated TLS transport');
});
