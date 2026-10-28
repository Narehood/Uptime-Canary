import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer, type Socket } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import type { Monitor, MonitorInput } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

// Independent byte-level daemon fixture. This exercises the release CLI and
// its real disposable workers, not an actual PM2 implementation. Windows uses
// a Unix profile that is unsupported locally; it never opens a user's fixed
// PM2 pipe. Private Windows admission/I/O are covered by the Go worker tests.
test('PM2 production workers retain local configuration, Pending and HA lifecycle semantics', { timeout: 120_000 }, async t => {
  const cluster = new Cluster();
  const sockets = new Set<Socket>();
  const errors: string[] = [];
  let mode = 'online', requests = 0, home = '/canary-pm2-not-on-windows';
  let server: ReturnType<typeof createServer> | undefined;
  t.after(async () => {
    await cluster.cleanup();
    for (const socket of sockets) socket.destroy();
    if (server?.listening) await new Promise<void>((resolve, reject) => server!.close(error => error ? reject(error) : resolve()));
    if (process.platform === 'linux') await rm(home, { recursive: true, force: true });
  });
  if (process.platform === 'linux') {
    home = await mkdtemp(join(tmpdir(), 'cpm-'));
    assert.ok(Buffer.byteLength(join(home, 'rpc.sock')) <= 107, 'owned temporary root must fit a Unix socket');
    server = createServer(socket => {
      sockets.add(socket); socket.once('close', () => sockets.delete(socket));
      socket.setTimeout(3000, () => socket.destroy());
      let buffered = Buffer.alloc(0), calls = 0;
      socket.on('error', () => {});
      socket.on('data', data => {
        try {
          buffered = Buffer.concat([buffered, data]);
          assert.ok(buffered.length <= 1024);
          while (buffered.length >= 5) {
            assert.equal(buffered[0], 0x12);
            const first = buffered.readUInt32BE(1);
            assert.ok(first >= 2 && first <= 480);
            if (buffered.length < 9 + first) return;
            const second = buffered.readUInt32BE(5 + first), size = 9 + first + second;
            assert.ok(second >= 3 && size <= 512);
            if (buffered.length < size) return;
            const payload = buffered.subarray(5, 5 + first).toString('utf8');
            const correlation = buffered.subarray(9 + first, size).toString('utf8');
            buffered = buffered.subarray(size);
            assert.equal(payload.slice(0, 2), 'j:');
            assert.equal(correlation, `s:canary-pm2:${calls}`);
            assert.deepEqual(JSON.parse(payload.slice(2)), { type: 'call', method: calls === 0 ? 'getVersion' : 'getMonitorData', args: [{}] });
            assert.ok(calls < 2, 'no subscriptions or lifecycle methods are allowed');
            requests++; calls++;
            const list = [0, 1].map(id => ({ name: '0042', pm_id: id, pm2_env: {
              namespace: 'blue', status: id === 0 ? 'online' : mode,
              HOME: '/private-manager-home', STATUS: 'private-environment-value',
            } }));
            const result = calls === 1 ? '6.0.8' : mode === 'malformed' ? [{}] : list;
            const args = [Buffer.from('j:' + JSON.stringify({ args: [result] })), Buffer.from(correlation)];
            const parts = [Buffer.from([0x12])];
            for (const argument of args) { const length = Buffer.alloc(4); length.writeUInt32BE(argument.length); parts.push(length, argument); }
            socket.write(Buffer.concat(parts));
          }
        } catch { errors.push('unexpected private PM2 request'); socket.destroy(); }
      });
    });
    server.listen(join(home, 'rpc.sock')); await once(server, 'listening');
  }
  await cluster.initialize(['pm2-a', 'pm2-b']); await cluster.startAll();
  const [a, b] = cluster.nodes; assert.ok(a && b);
  let input: MonitorInput = { name: 'Local PM2 proof', type: 'pm2', target: '0042',
    pm2: { transport: 'unix', home, namespace: 'blue' }, internalOnly: true, intervalMs: 1000,
    timeoutMs: 750, nodeIds: [a.id, b.id], enabled: true, repeatAfterMs: 0 };
  let monitor = await cluster.createMonitor(input);
  const settle = async (state: 'up' | 'down' | 'pending', since = Date.now() - 5000): Promise<void> => {
    await cluster.wait(`both production PM2 observers report ${state}`, async () =>
      (await cluster.overviews()).every(overview => {
        assert.ok(!JSON.stringify(overview).includes('private-environment-value'));
        const view = overview.monitors.find(item => item.monitor.id === monitor.id);
        return view?.monitor.revision === monitor.revision && view.observations.length === 2 && view.observations.every(item =>
          item.state === state && item.checkedAt >= since && item.latencyAvailable === false && item.latencyMs === 0 &&
          ['PM2 process is online', 'PM2 process is not online', 'PM2 status unavailable'].includes(item.message));
      }));
    assert.deepEqual(errors, []);
  };
  await settle(process.platform === 'linux' ? 'up' : 'pending');
  if (process.platform === 'linux') {
    let since = Date.now(); mode = 'stopped'; await settle('down', since);
    await cluster.wait('a definitive non-online instance opens the outage', async () =>
      (await cluster.overviews()).every(view => view.incidents.some(item => item.monitorId === monitor.id && item.resolvedAt === null)));
    const opened = (await cluster.overview(a)).incidents.find(item => item.monitorId === monitor.id && item.resolvedAt === null)!;
    since = Date.now(); mode = 'future-state'; await settle('pending', since);
    for (const overview of await cluster.overviews()) assert.ok(overview.incidents.some(item =>
      item.monitorId === monitor.id && item.resolvedAt === null && item.openedAt === opened.openedAt), 'Pending must preserve the ongoing outage');
    since = Date.now(); mode = 'malformed'; await settle('pending', since);
    since = Date.now(); mode = 'online'; await settle('up', since);
    await cluster.wait('online evidence closes the outage', async () =>
      (await cluster.overviews()).every(view => view.incidents.filter(item => item.monitorId === monitor.id).every(item => item.resolvedAt !== null)));
    assert.ok(requests >= 8);
  }
  const generation = monitor.checkGeneration || monitor.revision;
  input = { ...input, name: 'Renamed local PM2 proof' };
  let saved = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, revision: monitor.revision } });
  assert.equal(saved.status, 200, cluster.redact(saved.text)); monitor = saved.body;
  assert.equal(monitor.checkGeneration || monitor.revision, generation);
  await settle(process.platform === 'linux' ? 'up' : 'pending');
  await cluster.stop(b); await cluster.start(b); await settle(process.platform === 'linux' ? 'up' : 'pending');
  assert.deepEqual((await cluster.overview(b)).monitors.find(item => item.monitor.id === monitor.id)?.monitor.pm2, input.pm2);
  input = { ...input, pm2: { ...input.pm2!, namespace: 'other' } };
  saved = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, revision: monitor.revision } });
  assert.equal(saved.status, 200, cluster.redact(saved.text)); monitor = saved.body;
  assert.notEqual(monitor.checkGeneration || monitor.revision, generation);
  if (process.platform === 'linux') {
    const changedAt = Date.now();
    await cluster.wait('namespace change has affirmative no-match DOWN evidence', async () =>
      (await cluster.overviews()).every(view => {
        const current = view.monitors.find(item => item.monitor.id === monitor.id);
        return current?.monitor.revision === monitor.revision && current.observations.length === 2 && current.observations.every(item =>
          item.checkedAt >= changedAt && item.state === 'down' && item.message === 'PM2 process was not found');
      }));
  } else await settle('pending');
  // Actual disconnected writes retain both typed option heads. Resolve using
  // the complete revision set, then require observations of the chosen check.
  cluster.partition([[a.id], [b.id]]);
  const parent = monitor.revision;
  const branches = await Promise.all([a, b].map((node, index) => cluster.api<Monitor>(node, `/api/monitors/${monitor.id}`, {
    method: 'PUT', body: { ...input, enabled: false, pm2: { ...input.pm2!, namespace: 'branch-' + index }, revision: parent },
  })));
  for (const branch of branches) assert.equal(branch.status, 200, cluster.redact(branch.text));
  cluster.reconnect();
  await cluster.wait('both nodes retain both PM2 configuration heads', async () =>
    (await Promise.all([a, b].map(node => cluster.api<Monitor[]>(node, `/api/monitors/${monitor.id}/conflicts`))))
      .every(response => response.status === 200 && response.body.length === 2));
  const heads = (await cluster.api<Monitor[]>(a, `/api/monitors/${monitor.id}/conflicts`)).body;
  assert.deepEqual(heads.map(head => head.pm2?.namespace).sort(), ['branch-0', 'branch-1']);
  const resolved = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}/resolve`, {
    method: 'POST', body: { ...input, pm2: { ...input.pm2!, namespace: 'blue' }, enabled: true,
      revisions: heads.map(head => head.revision) },
  });
  assert.equal(resolved.status, 200, cluster.redact(resolved.text)); monitor = resolved.body;
  assert.equal(monitor.pm2?.namespace, 'blue');
  await settle(process.platform === 'linux' ? 'up' : 'pending', Date.now());
  const viewer = randomBytes(32).toString('hex');
  assert.equal((await cluster.api(a, '/api/accounts', { method: 'POST', body: { name: 'PM2 viewer', role: 'viewer', token: viewer } })).status, 201);
  for (const endpoint of ['/api/overview', '/api/monitors', `/api/monitors/${monitor.id}/conflicts`]) {
    const response = await fetch(a.url + endpoint, { headers: { Authorization: 'Bearer ' + viewer } });
    assert.equal(response.status, 200); const raw = await response.text();
    assert.ok(!raw.includes('"pm2":') && !raw.includes(home) && !raw.includes('private-environment-value'));
  }
  assert.equal(cluster.events.length, 0, 'no external notifications were configured');
});
