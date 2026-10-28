import assert from 'node:assert/strict';
import test from 'node:test';
import type { Monitor, MonitorInput, PublicStatusPage, StatusPage, StatusPageInput } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('public observed history requires opt-in, stays local and preserves coverage through restart and withdrawal', { timeout: 130_000 }, async t => {
  const c = new Cluster();
  t.after(() => c.cleanup());
  await c.initialize(['history-a', 'history-b']);
  await c.startAll();
  const [a, b] = c.nodes;
  assert.ok(a && b);
  const input: MonitorInput = {
    name: 'Private billing backend', type: 'manual', target: '', internalOnly: true, intervalMs: 1000, timeoutMs: 500,
    enabled: true, nodeIds: [a.id, b.id], manual: { state: 'up', message: 'Private implementation details' },
  };
  let monitor = await c.createMonitor(input);
  let selected: StatusPageInput = {
    slug: 'observed-services', title: 'Services', description: 'Explicit public history', enabled: true,
    groups: [{ id: 'services', name: 'Services', monitors: [{ monitorId: monitor.id, name: 'Billing', showCertificate: false }] }], announcements: [],
  };
  let saved = await c.api<StatusPage>(a, '/api/status-pages', { method: 'POST', body: selected });
  assert.equal(saved.status, 201, c.redact(saved.text));
  const route = `/public/status/${selected.slug}`;
  const badge = `/public/badge/${selected.slug}.svg?${new URLSearchParams({ group: 'services', monitor: 'Billing', metric: 'uptime' })}`;
  const publicView = (n = a) => c.api<PublicStatusPage>(n, route, { token: null });
  const privacy = (raw: string) => {
    for (const secret of [monitor.id, monitor.revision, input.name, input.manual!.message!, a.id, b.id, 'monitorId', 'nodeId']) {
      assert.ok(!raw.includes(secret), `Private field leaked: ${secret}`);
    }
  };
  await c.wait('default publication exposes current state without history', async () => {
    const r = await publicView(b);
    if (r.status !== 200 || r.body.state !== 'up') return false;
    assert.equal(r.body.groups[0]!.monitors[0]!.availability, undefined);
    return true;
  });
  assert.equal((await fetch(a.url + badge)).status, 404);
  selected = { ...selected, groups: [{ ...selected.groups[0]!, monitors: [{ ...selected.groups[0]!.monitors[0]!, showAvailability: true }] }] };
  saved = await c.api<StatusPage>(a, `/api/status-pages/${saved.body.id}`, { method: 'PUT', body: { ...selected, revision: saved.body.revision } });
  assert.equal(saved.status, 200, c.redact(saved.text));
  await c.wait('each answering node publishes its own explicitly selected coverage', async () => {
    for (const n of [a, b]) {
      const r = await publicView(n);
      privacy(r.text);
      const metric = r.body.groups[0]?.monitors[0]?.availability;
      if (metric?.status !== 'available' || !metric.summary?.knownMs) return false;
      assert.equal(metric.scope, 'serving-node');
      assert.equal(metric.to! - metric.from!, 86_400_000);
      assert.equal(metric.summary.availabilityPercent, 100);
      assert.ok(metric.summary.coveragePercent > 0 && metric.summary.coveragePercent < 1);
      assert.equal(metric.buckets!.reduce((sum, p) => sum + p.knownMs, 0), metric.summary.knownMs);
      assert.ok(metric.buckets!.length <= 25);
    }
    return true;
  }, 45_000);
  const image = await fetch(a.url + badge);
  assert.equal(image.status, 200);
  const svg = await image.text();
  privacy(svg);
  assert.match(svg, /100\.00% up/);
  assert.match(svg, /coverage/);
  assert.equal(image.headers.get('access-control-allow-origin'), '*');
  const changed = await c.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, manual: { state: 'down', message: 'Private outage diagnosis' }, revision: monitor.revision } });
  assert.equal(changed.status, 200, c.redact(changed.text));
  monitor = changed.body;
  await c.wait('current DOWN and historical failed time remain separate with honest coverage', async () => {
    const r = await publicView();
    privacy(r.text);
    const metric = r.body.groups[0]?.monitors[0]?.availability;
    return r.body.state === 'down' && metric?.status === 'available' && (metric.summary?.downMs ?? 0) > 0;
  }, 45_000);
  await c.wait('the restarting node has retained UP time and now observes DOWN', async () => {
    const r = await publicView(b);
    privacy(r.text);
    const metric = r.body.groups[0]?.monitors[0]?.availability;
    return r.body.state === 'down' && metric?.status === 'available' &&
      (metric.summary?.upMs ?? 0) > 0 && (metric.summary?.downMs ?? 0) > 0;
  }, 45_000);
  await c.stop(b);
  await c.start(b);
  await c.wait('persisted local history returns after process restart', async () => {
    const r = await publicView(b);
    privacy(r.text);
    const metric = r.body.groups[0]?.monitors[0]?.availability;
    // The monitor remains DOWN. Positive UP time must come from its retained
    // pre-restart history rather than newly observed samples after startup.
    return r.status === 200 && r.body.state === 'down' && metric?.status === 'available' &&
      (metric.summary?.upMs ?? 0) > 0 && (metric.summary?.downMs ?? 0) > 0;
  });
  selected.groups[0]!.monitors[0]!.showAvailability = false;
  saved = await c.api<StatusPage>(a, `/api/status-pages/${saved.body.id}`, { method: 'PUT', body: { ...selected, revision: saved.body.revision } });
  assert.equal(saved.status, 200, c.redact(saved.text));
  await c.wait('withdrawn opt-in removes JSON and metric badges on both nodes', async () => {
    for (const n of [a, b]) {
      const r = await publicView(n);
      if (r.body.groups[0]?.monitors[0]?.availability !== undefined) return false;
      if ((await fetch(n.url + badge)).status !== 404) return false;
    }
    return true;
  });
  assert.equal(c.events.length, 0, 'publication must never trigger notifications');
});
