// Optional compatibility proof, run by process-proof-validation.py --upgrade.
// The historical evaluator exists only in a declared test-build overlay.
// Production has no legacy-mode switch or evaluator fallback.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import test from 'node:test';
import { Cluster, type NodeProcess } from '../fixtures/cluster.js';
import { channelsReplicated, createChannel, webhookInput } from '../fixtures/notifications.js';
import type { MonitorInput } from '../../../packages/shared/src/index.js';

test('protocol upgrade retains SQLite identity and receipts, rejects mixed peers, and replaces old query semantics', { timeout: 120_000 }, async t => {
  const legacy = process.env.CANARY_LEGACY_BIN;
  assert.ok(legacy, 'Use the guarded --upgrade runner to build the declared historical fixture');
  const cluster = new Cluster();
  t.after(() => cluster.cleanup());
  let ready = true;
  const target = createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json', Connection: 'close' }).end(JSON.stringify({ ready }));
  });
  target.listen(0, '127.0.0.1'); await once(target, 'listening');
  t.after(async () => { target.closeAllConnections(); await new Promise<void>(resolve => target.close(() => resolve())); });
  const address = target.address(); assert.ok(address && typeof address !== 'string');
  await cluster.initialize(['upgrade-a', 'upgrade-b']);
  const [a, b] = cluster.nodes as [NodeProcess, NodeProcess];
  await Promise.all(cluster.nodes.map(node => cluster.start(node, legacy)));
  const channel = await createChannel(cluster, webhookInput('Retained upgrade route', cluster.webhookUrl));
  await channelsReplicated(cluster, [channel]);
  const common = { target: `http://127.0.0.1:${address.port}`, internalOnly: true, intervalMs: 3000, timeoutMs: 3000,
    nodeIds: cluster.nodes.map(node => node.id), enabled: true, notificationIds: [channel.id], repeatAfterMs: 0 };
  const query = await cluster.createMonitor({ ...common, name: 'Semantic upgrade control', type: 'http',
    http: { jsonata: { expression: 'ready ? $sort([2,1],$length(?))[0] : 2', operator: '==', expected: 2 } } });
  const manual = await cluster.createMonitor({ ...common, target: 'manual', name: 'Previously accepted down', type: 'manual',
    manual: { state: 'down', message: 'Private upgrade fixture' } });
  const viewer = cluster.secret();
  assert.equal((await cluster.api(a, '/api/accounts', { method: 'POST', body: { name: 'Retained viewer', role: 'viewer', token: viewer } })).status, 201);

  const ownState = async (node: NodeProcess, id: string, state: string, since = 0) =>
    (await cluster.history(node, id)).some(o => o.nodeId === node.id && o.state === state && o.checkedAt >= since);
  await cluster.wait('both historical evaluators actually reproduce the false healthy result', async () =>
    (await Promise.all(cluster.nodes.map(n => ownState(n, query.id, 'up')))).every(Boolean));
  await cluster.wait('old runtimes share an accepted manual down', () => cluster.alerts().some(x => x.monitorId === manual.id && x.kind === 'down'));
  await cluster.quiet('old accepted down converges before the upgrade', 1);
  const originalDown = cluster.alerts()[0]!;
  assert.equal(originalDown.monitorId, manual.id);

  async function sequence(node: NodeProcess, peer: NodeProcess, protocol: number): Promise<number> {
    const response = await cluster.api<{ protocol: number; observations: { nodeId: string; sequence: number }[] }>(node, '/internal/snapshot', {
      method: 'POST', token: cluster.clusterToken, headers: { 'X-Canary-Node-Id': peer.id },
      body: { protocol, node: { id: peer.id, name: peer.id }, versions: [], groupVersions: [], resources: [], intents: [],
        observations: [], incidents: [], receipts: [], heartbeats: [], revisionEdges: [], revisionClaims: [], incidentFacts: [], evidenceLinks: [] },
    });
    assert.equal(response.status, 200, response.text); assert.equal(response.body.protocol, protocol);
    const values = response.body.observations.filter(o => o.nodeId === node.id).map(o => o.sequence);
    assert.ok(values.length > 0 && values.every(Number.isSafeInteger));
    return Math.max(...values);
  }
  const oldSequence = await sequence(a, b, 13);
  const mixedBefore = new Set(cluster.exchanges);
  await cluster.stop(a);
  const upgradedAt = Date.now(); await cluster.start(a);
  await cluster.wait('new evaluator rejects the same finite-looking legacy result', () => ownState(a, query.id, 'down', upgradedAt));
  await cluster.wait('old and new live peers reject both exchange directions', () =>
    [[a.id, b.id], [b.id, a.id]].every(([from, to]) => cluster.exchanges.some(e =>
      !mixedBefore.has(e) && e.from === from && e.to === to && e.status === 400)));
  await cluster.wait('the historical peer continues executing its own old semantics', () => ownState(b, query.id, 'up', upgradedAt));
  const newSequence = await sequence(a, b, 14);
  assert.ok(newSequence > oldSequence && newSequence >= 65537, 'reopen must skip the old reserved block');
  assert.equal((await cluster.api(a, '/api/overview', { token: viewer })).status, 200, 'local account survives SQLite upgrade');
  assert.equal((await cluster.api(a, '/api/monitors', { method: 'POST', token: viewer, body: {} })).status, 403, 'retained viewer remains read-only');
  await cluster.wait('new genuine down is delivered during the mixed-version partition', () =>
    cluster.alerts().some(x => x.monitorId === query.id && x.kind === 'down'));

  await cluster.stop(b); await cluster.start(b);
  await cluster.wait('both current evaluators converge on down', async () =>
    (await cluster.overviews()).every(o => o.monitors.find(v => v.monitor.id === query.id)?.state === 'down'));
  await cluster.quiet('upgrade preserves accepted receipts without a resend', 2);
  for (const overview of await cluster.overviews()) {
    for (const monitor of [query, manual]) {
      const current = overview.monitors.find(v => v.monitor.id === monitor.id)!;
      assert.equal(current.monitor.revision, monitor.revision);
      assert.equal(current.monitor.checkGeneration, monitor.checkGeneration);
      assert.equal(current.configConflict, false);
    }
  }
  await channelsReplicated(cluster, [channel]);
  await cluster.stop(a); await cluster.start(a);
  await cluster.wait('current restart produces a new local sample', () => sequence(a, b, 14).then(value => value > newSequence));
  await cluster.quiet('current restart also cannot resend old accepted downs', 2);

  ready = false;
  await cluster.wait('a genuinely healthy document recovers the semantic outage', () => cluster.alerts().some(x => x.monitorId === query.id && x.kind === 'recovery'));
  const input: MonitorInput = { ...common, target: 'manual', name: manual.name, type: 'manual', manual: { state: 'up', message: 'Private upgrade recovery' } };
  assert.equal((await cluster.api(a, `/api/monitors/${manual.id}`, { method: 'PUT', body: { ...input, revision: manual.revision } })).status, 200);
  await cluster.wait('retained old down gets its one owed recovery', () => cluster.alerts().some(x => x.monitorId === manual.id && x.kind === 'recovery'));
  await cluster.quiet('both recoveries settle once', 4);
  for (const monitor of [query, manual]) {
    const events = cluster.alerts().filter(x => x.monitorId === monitor.id);
    assert.equal(events.filter(x => x.kind === 'down').length, 1);
    assert.equal(events.filter(x => x.kind === 'recovery').length, 1);
    assert.notEqual(events[0]!.event.idempotencyKey, events[1]!.event.idempotencyKey);
  }
  t.diagnostic('Controlled protocol-13 overlay, current readonly modules; same private databases/identity directories through mixed refusal, coordinated completion and restart. No original release artifact claim.');
});
