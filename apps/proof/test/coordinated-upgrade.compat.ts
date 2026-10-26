// Explicit published-baseline compatibility gate; not an ordinary fresh-install proof.
import assert from 'node:assert/strict';
import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { join } from 'node:path';
import test from 'node:test';
import { Cluster, type NodeProcess } from '../fixtures/cluster.js';
import { channelsReplicated, createChannel, webhookInput } from '../fixtures/notifications.js';
import type { Monitor, MonitorInput } from '../../../packages/shared/src/index.js';

test('published protocol18 state survives mixed refusal, coordinated upgrade and restart', { timeout: 140_000 }, async t => {
  const legacy = process.env.CANARY_LEGACY_BIN;
  assert.ok(legacy, 'Use coordinated-upgrade-validation.py with the pinned published executable');
  const cluster = new Cluster();
  t.after(() => cluster.cleanup());
  await cluster.initialize(['upgrade-a', 'upgrade-b']);
  const [a, b] = cluster.nodes as [NodeProcess, NodeProcess];
  await Promise.all(cluster.nodes.map(node => cluster.start(node, legacy)));
  const channel = await createChannel(cluster, webhookInput('Retained route', cluster.webhookUrl));
  await channelsReplicated(cluster, [channel]);
  const input: MonitorInput = { name: 'Retained outage', type: 'manual', target: 'manual', intervalMs: 1000,
    timeoutMs: 1000, nodeIds: [a.id, b.id], enabled: true, notificationIds: [channel.id], repeatAfterMs: 0,
    manual: { state: 'down', message: 'Private upgrade fixture' } };
  const monitor = await cluster.createMonitor(input);
  const viewer = cluster.secret();
  assert.equal((await cluster.api(a, '/api/accounts', { method: 'POST', body: { name: 'Retained viewer', role: 'viewer', token: viewer } })).status, 201);
  await cluster.wait('old observers converge and retain an accepted outage', async () =>
    (await cluster.overviews()).every(o => o.monitors.find(v => v.monitor.id === monitor.id)?.state === 'down') &&
    cluster.alerts().some(event => event.monitorId === monitor.id && event.kind === 'down'));
  // quiet's second argument is a webhook count, not a duration or node count.
  // As in ha.test.ts and monitor-edits.test.ts, connected peers share one
  // accepted down delivery and later one recovery. Keep QUIET_MS unchanged.
  await cluster.quiet('accepted legacy delivery converges', 1);
  const accepted = cluster.alerts().filter(event => event.monitorId === monitor.id);
  assert.equal(accepted.length, 1, 'connected legacy peers deliver one outage, not one per observer');
  assert.ok(accepted.every(event => event.kind === 'down'));
  const acceptedKeys = new Set(accepted.map(event => event.event.idempotencyKey));
  let acceptedReceiptId: string | undefined;
  await cluster.wait('accepted legacy receipt converges before stopping an observer', async () => {
    const overviews = await cluster.overviews();
    const receipt = overviews[0]!.notifications.find(value => value.monitorId === monitor.id &&
      value.kind === 'down' && value.nodeId === accepted[0]!.nodeId);
    if (!receipt?.id || !overviews.every(value => value.notifications.some(item => item.id === receipt.id))) return false;
    acceptedReceiptId = receipt.id;
    return true;
  });
  assert.ok(acceptedReceiptId);
  async function retainedReceipt(label: string) {
    await cluster.wait(label, async () => (await cluster.overviews()).every(overview =>
      overview.notifications.some(receipt => receipt.id === acceptedReceiptId && receipt.monitorId === monitor.id && receipt.kind === 'down')));
  }

  async function sequence(node: NodeProcess, peer: NodeProcess, protocol: number) {
    const response = await cluster.api<{ protocol: number; observations: { nodeId: string; sequence: number }[] }>(node, '/internal/snapshot', {
      method: 'POST', token: cluster.clusterToken, headers: { 'X-Canary-Node-Id': peer.id },
      body: { protocol, node: { id: peer.id, name: peer.id }, versions: [], groupVersions: [], resources: [], intents: [],
        observations: [], incidents: [], receipts: [], heartbeats: [], revisionEdges: [], revisionClaims: [], incidentFacts: [], evidenceLinks: [] },
    });
    assert.equal(response.status, 200, response.text);
    assert.equal(response.body.protocol, protocol);
    const values = response.body.observations.filter(sample => sample.nodeId === node.id).map(sample => sample.sequence);
    assert.ok(values.length > 0 && values.every(Number.isSafeInteger));
    return Math.max(...values);
  }
  const before = await sequence(a, b, 18);
  const exchanges = new Set(cluster.exchanges);
  await cluster.stop(a);
  const since = Date.now();
  await cluster.start(a);
  await cluster.wait('new local sample follows the upgrade', async () =>
    (await cluster.history(a, monitor.id)).some(sample => sample.nodeId === a.id && sample.checkedAt >= since && sample.state === 'down'));
  await cluster.wait('mixed live peers refuse both exchange directions', () =>
    [[a.id, b.id], [b.id, a.id]].every(([from, to]) => cluster.exchanges.some(exchange =>
      !exchanges.has(exchange) && exchange.from === from && exchange.to === to && exchange.status === 400)));
  const after = await sequence(a, b, 19);
  assert.ok(after > before && after >= 65537, 'retained sequence reservation must advance');
  assert.equal((await cluster.api(a, '/api/overview', { token: viewer })).status, 200);
  assert.equal((await cluster.api(a, '/api/monitors', { method: 'POST', body: {}, token: viewer })).status, 403);
  const added = await cluster.api<Monitor>(a, '/api/monitors', { method: 'POST', body: {
    ...input, name: 'New paused profile', type: 'tailscale-ping', target: 'private-test-peer', enabled: false,
    manual: undefined, notificationIds: [], description: 'Protocol19 metadata',
  } });
  assert.equal(added.status, 201, added.text);
  assert.ok(!(await cluster.overview(b)).monitors.some(view => view.monitor.id === added.body.id), 'old peer cannot silently accept the new profile');

  await cluster.stop(b);
  await cluster.start(b);
  await cluster.wait('both current peers converge on retained and new configuration', async () =>
    (await cluster.overviews()).every(overview => {
      const retained = overview.monitors.find(view => view.monitor.id === monitor.id);
      const fresh = overview.monitors.find(view => view.monitor.id === added.body.id);
      return retained?.state === 'down' && retained.monitor.revision === monitor.revision &&
        retained.monitor.checkGeneration === monitor.checkGeneration && !retained.configConflict &&
        fresh?.monitor.description === 'Protocol19 metadata' && fresh.monitor.enabled === false;
    }));
  await channelsReplicated(cluster, [channel]);
  await retainedReceipt('upgraded observers retain the original accepted receipt');
  await cluster.quiet('upgrade cannot resend already accepted keys', 1);
  assert.deepEqual(new Set(cluster.alerts().filter(event => event.monitorId === monitor.id).map(event => event.event.idempotencyKey)), acceptedKeys);
  await cluster.stop(a);
  await cluster.start(a);
  await cluster.wait('second current restart advances persisted sequence', () => sequence(a, b, 19).then(value => value > after));
  await retainedReceipt('the original receipt survives another current-version restart');
  assert.equal((await cluster.api(a, '/api/overview', { token: viewer })).status, 200);
  const updated = await cluster.api<Monitor>(a, '/api/monitors/' + monitor.id, { method: 'PUT', body: {
    ...input, revision: monitor.revision, manual: { state: 'up', message: 'Private recovery' },
  } });
  assert.equal(updated.status, 200, updated.text);
  await cluster.wait('retained legacy outage receives its recovery', () => cluster.alerts().some(event => event.monitorId === monitor.id && event.kind === 'recovery'));
  await cluster.quiet('coordinated upgrade settles with no old resend', 2);
  await retainedReceipt('recovery does not erase the accepted legacy receipt');
  const events = cluster.alerts().filter(event => event.monitorId === monitor.id);
  assert.deepEqual(new Set(events.filter(event => event.kind === 'down').map(event => event.event.idempotencyKey)), acceptedKeys);
  assert.equal(events.filter(event => event.kind === 'recovery').length, 1);
  t.diagnostic('Original public image executable, not a rebuilt legacy overlay; private retained SQLite/identity state only.');
});

test('published dashboard accounts and encrypted node registration survive server and node upgrades', { timeout: 90_000 }, async t => {
  const legacy = process.env.CANARY_LEGACY_BIN;
  const current = process.env.CANARY_BIN;
  assert.ok(legacy && current, 'Use the published-baseline upgrade runner');
  const cluster = new Cluster();
  let dashboard: ChildProcess | undefined;
  const stopDashboard = async () => {
    if (!dashboard || dashboard.exitCode !== null || dashboard.signalCode !== null) return;
    const exited = once(dashboard, 'exit');
    dashboard.kill('SIGKILL');
    await Promise.race([exited, new Promise((_, reject) => {
      const timer = setTimeout(() => reject(Error('Owned dashboard did not stop')), 5000);
      timer.unref();
    })]);
  };
  t.after(async () => { await stopDashboard(); await cluster.cleanup(); });
  await cluster.initialize(['dashboard-upgrade-node']);
  const node = cluster.nodes[0]!;
  await cluster.start(node, legacy);
  const reservation = createServer();
  reservation.listen(0, '127.0.0.1');
  await once(reservation, 'listening');
  const address = reservation.address();
  assert.ok(address && typeof address !== 'string');
  const port = address.port;
  await new Promise<void>((resolve, reject) => reservation.close(error => error ? reject(error) : resolve()));
  const base = 'http://127.0.0.1:' + port;
  const identity = join(cluster.directory, 'dashboard-identity');
  const data = join(cluster.directory, 'dashboard-data');
  const assets = join(cluster.directory, 'dashboard-assets');
  await mkdir(assets, { mode: 0o700 });
  await writeFile(join(assets, 'index.html'), '<!doctype html><html><body><div id="root"></div></body></html>');
  const startDashboard = async (executable: string) => {
    const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('CANARY_') && key !== 'NODE_OPTIONS'));
    dashboard = spawn(executable, ['dashboard'], { cwd: cluster.directory, stdio: 'ignore', env: {
      ...environment, CANARY_DASHBOARD_DATA: data, CANARY_DASHBOARD_IDENTITY: identity, CANARY_DASHBOARD_ASSETS: assets,
      CANARY_DASHBOARD_HOST: '127.0.0.1', CANARY_DASHBOARD_PORT: String(port), CANARY_DASHBOARD_ORIGINS: base, CANARY_UPDATE_CHECKS: 'false',
      CANARY_LOCAL_NODE_URL: node.url, CANARY_LOCAL_NODE_TOKEN: node.adminToken,
    } });
    dashboard.on('error', () => {});
    await cluster.wait('owned dashboard process answers', async () => {
      assert.equal(dashboard?.exitCode, null, 'owned dashboard exited');
      try { return (await fetch(base + '/api/auth/status', { signal: AbortSignal.timeout(2000) })).ok; } catch { return false; }
    });
  };
  const api = async (path: string, method = 'GET', body?: unknown, token?: string, registration?: string) => {
    const response = await fetch(base + path, { method, redirect: 'error', signal: AbortSignal.timeout(5000), headers: {
      ...(token ? { Authorization: 'Bearer ' + token } : {}),
      ...(registration ? { 'X-Canary-Registered-Node': registration } : {}),
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}) });
    const text = await response.text();
    return { status: response.status, value: text ? JSON.parse(text) : undefined };
  };
  await startDashboard(legacy);
  const code = (await readFile(join(identity, 'setup.code'), 'utf8')).trim();
  const password = 'Private retained dashboard owner password';
  assert.equal((await api('/api/dashboard/setup', 'POST', { name: 'upgrade-owner', password, code })).status, 201);
  const login = await api('/api/auth/login', 'POST', { name: 'upgrade-owner', password });
  assert.equal(login.status, 200);
  const session = login.value.token as string;
  assert.ok(session);
  let registration = '';
  await cluster.wait('published dashboard enrolls its local node', async () => {
    const rows = await api('/api/dashboard/nodes', 'GET', undefined, session);
    assert.equal(rows.status, 200);
    const saved = rows.value.find((row: { nodeId: string; state: string }) => row.nodeId === node.id && row.state === 'answering');
    if (!saved) return false;
    registration = saved.id;
    return true;
  });
  const created = await api('/api/monitors', 'POST', { name: 'Dashboard-owned upgrade check', type: 'manual', target: 'manual',
    intervalMs: 1000, timeoutMs: 1000, enabled: true, nodeIds: [node.id], notificationIds: [], manual: { state: 'up' },
  }, session, registration);
  assert.equal(created.status, 201);
  const monitorID = created.value.id as string;
  await stopDashboard();
  await startDashboard(current);
  assert.equal((await api('/api/auth/status')).value.setupRequired, false, 'upgrade must not reopen first-owner setup');
  assert.equal((await api('/api/auth/me', 'GET', undefined, session)).status, 200, 'existing unexpired dashboard session survives ordinary upgrade');
  assert.equal((await api('/api/auth/login', 'POST', { name: 'upgrade-owner', password })).status, 200, 'owner password survives');
  const retained = await api('/api/dashboard/nodes', 'GET', undefined, session);
  assert.equal(retained.value.length, 1);
  assert.equal(retained.value[0].id, registration);
  assert.equal(retained.value[0].nodeId, node.id);
  const oldOverview = await api('/api/overview', 'GET', undefined, session, registration);
  assert.equal(oldOverview.status, 200, 'new server decrypts and uses the original delegated bundle');
  assert.ok(oldOverview.value.monitors.some((view: { monitor: { id: string } }) => view.monitor.id === monitorID));
  assert.equal((await api('/api/dashboard/nodes/' + registration + '/rotate', 'POST', {}, session)).status, 409, 'old node must refuse unsupported rotation before journaling');
  assert.notEqual((await api('/api/dashboard/nodes', 'GET', undefined, session)).value[0].state, 'rotating');
  await cluster.stop(node);
  await cluster.start(node);
  const rotated = await api('/api/dashboard/nodes/' + registration + '/rotate', 'POST', {}, session);
  assert.ok(rotated.status === 200 || rotated.status === 202, 'current node supports credential rotation');
  await cluster.wait('rotation finishes on the original registration without signing out', async () => {
    const rows = await api('/api/dashboard/nodes', 'GET', undefined, session);
    assert.equal(rows.status, 200);
    assert.equal(rows.value.length, 1);
    assert.equal(rows.value[0].id, registration);
    if (rows.value[0].state !== 'answering') return false;
    const overview = await api('/api/overview', 'GET', undefined, session, registration);
    return overview.status === 200 && overview.value.monitors.some((view: { monitor: { id: string } }) => view.monitor.id === monitorID);
  });
  t.diagnostic('Published old dashboard and node executable, retained encrypted registry and accounts, private state; no user deployment contacted.');
});
