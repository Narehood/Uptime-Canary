// Optional real-process proof: process-proof-validation.py --publication-upgrade.
// Historical source fragments are build inputs only; production has no legacy mode.
import assert from 'node:assert/strict';
import test from 'node:test';
import type { LocalAvailabilityHistory, Monitor, MonitorInput, PublicStatusPage, StatusPage, StatusPageInput } from '../../../packages/shared/src/index.js';
import { Cluster, type NodeProcess } from '../fixtures/cluster.js';
import { channelsReplicated, createChannel, webhookInput } from '../fixtures/notifications.js';

test('14-to-15 upgrade preserves local history and owed delivery without silently publishing availability', { timeout: 150_000 }, async t => {
  const legacy = process.env.CANARY_LEGACY_BIN;
  assert.ok(legacy, 'Use the guarded --publication-upgrade runner');
  const c = new Cluster();
  t.after(() => c.cleanup());
  await c.initialize(['publication-upgrade-a', 'publication-upgrade-b']);
  const [a, b] = c.nodes as [NodeProcess, NodeProcess];
  await Promise.all(c.nodes.map(node => c.start(node, legacy)));
  const channel = await createChannel(c, webhookInput('Retained publication-upgrade route', c.webhookUrl));
  await channelsReplicated(c, [channel]);
  const common: MonitorInput = { name: 'Private retained uptime', type: 'manual', target: 'manual',
    internalOnly: true, intervalMs: 1000, timeoutMs: 750, nodeIds: [a.id, b.id], enabled: true, repeatAfterMs: 0,
    manual: { state: 'up', message: 'Private implementation details' } };
  const from = Date.now();
  const monitor = await c.createMonitor(common);
  const downInput: MonitorInput = { ...common, name: 'Retained accepted down', notificationIds: [channel.id],
    manual: { state: 'down', message: 'Private publication-upgrade outage' } };
  const down = await c.createMonitor(downInput);
  const pageInput: StatusPageInput = { slug: 'upgrade-services', title: 'Services', description: 'Explicit publication only', enabled: true,
    groups: [{ id: 'services', name: 'Services', monitors: [{ monitorId: monitor.id, name: 'Payments', showCertificate: false }] }], announcements: [] };
  let page = await c.api<StatusPage>(a, '/api/status-pages', { method: 'POST', body: pageInput });
  assert.equal(page.status, 201, c.redact(page.text));
  const route = `/public/status/${pageInput.slug}`;
  const badge = `/public/badge/${pageInput.slug}.svg?${new URLSearchParams({ group: 'services', monitor: 'Payments', metric: 'uptime' })}`;
  const publicView = (node: NodeProcess) => c.api<PublicStatusPage>(node, route, { token: null });
  const privacy = (raw: string) => {
    for (const value of [monitor.id, monitor.revision, common.name, common.manual!.message!, a.id, b.id, 'monitorId', 'nodeId']) {
      assert.ok(!raw.includes(value), `Public availability leaked ${value}`);
    }
  };
  await c.wait('both old nodes publish current status without historical opt-in', async () => {
    for (const node of [a, b]) {
      const r = await publicView(node);
      if (r.status !== 200 || r.body.state !== 'up') return false;
      assert.equal(r.body.groups[0]!.monitors[0]!.availability, undefined);
    }
    return c.alerts().some(event => event.monitorId === down.id && event.kind === 'down');
  });
  await c.quiet('accepted down converges on protocol 14', 1);
  assert.equal((await fetch(a.url + badge)).status, 404);
  const optedIn: StatusPageInput = { ...pageInput, groups: [{ ...pageInput.groups[0]!,
    monitors: [{ ...pageInput.groups[0]!.monitors[0]!, showAvailability: true }] }] };
  const unsupported = await c.api(a, `/api/status-pages/${page.body.id}`, {
    method: 'PUT', body: { ...optedIn, revision: page.body.revision } });
  assert.equal(unsupported.status, 400, 'historical API must not silently accept the new publication flag');
  const viewer = c.secret();
  assert.equal((await c.api(a, '/api/accounts', { method: 'POST', body: { name: 'Retained viewer', role: 'viewer', token: viewer } })).status, 201);

  const to = Date.now() - 250;
  const historyPath = `/api/monitors/${monitor.id}/availability?${new URLSearchParams({ from: String(from), to: String(to), stepMs: '1000' })}`;
  const retained = new Map<string, LocalAvailabilityHistory>();
  for (const node of [a, b]) {
    const r = await c.api<LocalAvailabilityHistory>(node, historyPath);
    assert.equal(r.status, 200, c.redact(r.text));
    assert.ok(r.body.summary.knownMs > 0, 'old node must have durable known-time evidence');
    retained.set(node.id, r.body);
  }
  async function sequence(node: NodeProcess, peer: NodeProcess, protocol: number): Promise<number> {
    const r = await c.api<{ protocol: number; observations: { nodeId: string; sequence: number }[] }>(node, '/internal/snapshot', {
      method: 'POST', token: c.clusterToken, headers: { 'X-Canary-Node-Id': peer.id }, body: {
        protocol, node: { id: peer.id, name: peer.id }, versions: [], groupVersions: [], resources: [], intents: [],
        observations: [], incidents: [], receipts: [], heartbeats: [], revisionEdges: [], revisionClaims: [], incidentFacts: [], evidenceLinks: [],
      },
    });
    assert.equal(r.status, 200, c.redact(r.text));
    assert.equal(r.body.protocol, protocol);
    const values = r.body.observations.filter(o => o.nodeId === node.id).map(o => o.sequence);
    assert.ok(values.length && values.every(Number.isSafeInteger));
    return Math.max(...values);
  }
  const oldSequence = await sequence(a, b, 14);
  const mixedBefore = new Set(c.exchanges);
  await c.stop(a); await c.start(a);
  await c.wait('mixed publication protocols refuse both live exchange directions', () =>
    [[a.id, b.id], [b.id, a.id]].every(([fromId, toId]) => c.exchanges.some(e =>
      !mixedBefore.has(e) && e.from === fromId && e.to === toId && e.status === 400)));
  const defaultView = await publicView(a);
  assert.equal(defaultView.status, 200);
  assert.equal(defaultView.body.groups[0]!.monitors[0]!.availability, undefined, 'upgrade must not opt existing pages in');
  assert.equal((await fetch(a.url + badge)).status, 404);
  assert.deepEqual((await c.api<LocalAvailabilityHistory>(a, historyPath)).body.summary, retained.get(a.id)!.summary);
  assert.equal((await c.api(a, '/api/overview', { token: viewer })).status, 200);
  assert.equal((await c.api(a, '/api/monitors', { method: 'POST', token: viewer, body: {} })).status, 403);
  await c.wait('new local sequence skips the historical reserved block', async () => (await sequence(a, b, 15)) > oldSequence);

  page = await c.api<StatusPage>(a, `/api/status-pages/${page.body.id}`, { method: 'PUT', body: { ...optedIn, revision: page.body.revision } });
  assert.equal(page.status, 200, c.redact(page.text));
  assert.equal((await publicView(b)).body.groups[0]!.monitors[0]!.availability, undefined, 'refused old peer retains its own old publication policy');
  await c.wait('explicit new opt-in exposes retained measured coverage', async () => {
    const r = await publicView(a);
    privacy(r.text);
    const metric = r.body.groups[0]?.monitors[0]?.availability;
    return metric?.status === 'available' && metric.scope === 'serving-node'
      && metric.to! - metric.from! === 86_400_000 && (metric.summary?.knownMs ?? 0) > 0;
  }, 45_000);
  await c.stop(b); await c.start(b);
  await c.wait('coordinated upgrade receives explicit publication policy', async () => {
    const r = await publicView(b);
    privacy(r.text);
    return r.status === 200 && r.body.groups[0]?.monitors[0]?.availability?.status === 'available';
  });
  assert.deepEqual((await c.api<LocalAvailabilityHistory>(b, historyPath)).body.summary, retained.get(b.id)!.summary);
  await c.quiet('accepted historical down is not resent by either upgrade', 1);
  for (const overview of await c.overviews()) {
    for (const original of [monitor, down]) {
      const view = overview.monitors.find(row => row.monitor.id === original.id)!;
      assert.equal(view.monitor.revision, original.revision);
      assert.equal(view.monitor.checkGeneration, original.checkGeneration);
      assert.equal(view.configConflict, false);
    }
  }
  const recovery = await c.api<Monitor>(a, `/api/monitors/${down.id}`, { method: 'PUT',
    body: { ...downInput, manual: { state: 'up', message: 'Private post-upgrade recovery' }, revision: down.revision } });
  assert.equal(recovery.status, 200, c.redact(recovery.text));
  await c.wait('retained historical down receives its one real recovery', () => c.alerts().some(e => e.monitorId === down.id && e.kind === 'recovery'));
  await c.quiet('upgrade preserves one down and one recovery', 2);
  assert.equal(c.alerts().filter(e => e.kind === 'down').length, 1);
  assert.equal(c.alerts().filter(e => e.kind === 'recovery').length, 1);
  assert.ok(c.alerts().every(e => e.monitorId === down.id));
  t.diagnostic('Five pinned protocol-14 production fragments, one overlay-deleted added file, unchanged remaining production/module inputs; rebuilt fixture, not a published release artifact. No implicit history publication or receipt reset.');
});
