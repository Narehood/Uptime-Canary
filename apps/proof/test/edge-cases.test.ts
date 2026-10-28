import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import type { Socket } from 'node:net';
import test from 'node:test';
import type { Group, GroupInput, MonitorInput } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('a real scheduled HTTP timeout records failure and sends an outage notification', { timeout: 30_000 }, async t => {
  const cluster = new Cluster();
  t.after(() => cluster.cleanup());
  const sockets = new Set<Socket>();
  const hung = createServer(() => { /* Deliberately never send response headers. */ });
  hung.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  t.after(async () => {
    const closed = new Promise<void>(resolve => hung.close(() => resolve()));
    for (const socket of sockets) socket.destroy();
    hung.closeAllConnections();
    await closed;
  });
  await new Promise<void>(resolve => hung.listen(0, '127.0.0.1', resolve));
  const address = hung.address();
  assert.ok(address && typeof address !== 'string');
  await cluster.initialize(['timeout-observer']);
  await cluster.startAll();
  const node = cluster.nodes[0]!;
  const monitor = await cluster.createMonitor({
    name: 'Timed out endpoint', type: 'http', target: `http://127.0.0.1:${address.port}`,
    internalOnly: true, intervalMs: 1_000, timeoutMs: 150, nodeIds: [node.id], enabled: true,
    webhookUrl: cluster.webhookUrl, repeatAfterMs: 0,
  });
  await cluster.wait('scheduled deadline expiry becomes a down observation', async () =>
    (await cluster.history(node, monitor.id)).some(sample => sample.state === 'down' && /timeout/i.test(sample.message)), 10_000);
  await cluster.wait('the timeout creates an actual outage webhook', () =>
    cluster.alerts().some(alert => alert.monitorId === monitor.id && alert.kind === 'down'), 10_000);
});

for (const brokenDestination of [false, true]) {
  test(`group dependency with ${brokenDestination ? 'a failing' : 'no'} notification destination leaves child delivery available`, { timeout: 30_000 }, async t => {
    const cluster = new Cluster();
    t.after(() => cluster.cleanup());
    let failedProviderUrl: string | undefined;
    if (brokenDestination) {
      const provider = createServer((_req, res) => { res.writeHead(503).end(); });
      await new Promise<void>(resolve => provider.listen(0, '127.0.0.1', resolve));
      const address = provider.address();
      assert.ok(address && typeof address !== 'string');
      failedProviderUrl = `http://127.0.0.1:${address.port}`;
      t.after(async () => {
        const closed = new Promise<void>(resolve => provider.close(() => resolve()));
        provider.closeAllConnections();
        await closed;
      });
    }
    await cluster.initialize(['custom-observer']);
    await cluster.startAll();
    const node = cluster.nodes[0]!;
    const dependencyTarget = await cluster.createTarget();
    const childTarget = await cluster.createTarget();
    dependencyTarget.state = 'down';
    childTarget.state = 'down';
    const base: MonitorInput = {
      name: 'User-selected dependency', type: 'http', target: dependencyTarget.url,
      internalOnly: true, intervalMs: 1_000, timeoutMs: 500, nodeIds: [node.id], enabled: true, repeatAfterMs: 0,
      ...(failedProviderUrl ? { webhookUrl: failedProviderUrl } : {}),
    };
    const dependency = await cluster.createMonitor(base);
    const groupInput: GroupInput = { name: 'Arbitrary user group', nodeIds: [node.id], outageMonitorId: dependency.id, graceMs: 1_000 };
    const response = await cluster.api<Group>(node, '/api/groups', { method: 'POST', body: groupInput });
    assert.equal(response.status, 201);
    const child = await cluster.createMonitor({ ...base, name: 'Dependent service', target: childTarget.url, groupId: response.body.id, webhookUrl: cluster.webhookUrl });
    await cluster.wait('both dependency and child have actual down observations', async () => {
      const overview = await cluster.overview(node);
      return [dependency.id, child.id].every(id => overview.monitors.some(view => view.monitor.id === id && view.state === 'down'));
    }, 10_000);
    await cluster.wait('a working child destination receives the outage after the group grace period', () =>
      cluster.alerts().some(alert => alert.monitorId === child.id && alert.kind === 'down'), 10_000);
  });
}
