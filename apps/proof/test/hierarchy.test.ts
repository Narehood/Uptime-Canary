import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import test from 'node:test';
import type { Group, GroupView, Monitor, MonitorInput, Overview } from '../../../packages/shared/src/index.js';
import { CHECK_INTERVAL_MS, Cluster, type HttpTarget } from '../fixtures/cluster.js';

function group(overview: Overview, id: string): GroupView {
  const view = overview.groups?.find(item => item.group.id === id);
  assert.ok(view, 'configured group must be present');
  return view;
}

function monitorInput(c: Cluster, target: HttpTarget, groupId: string): MonitorInput {
  return {
    name: `Hierarchy leaf ${randomUUID()}`, type: 'http', target: target.url,
    intervalMs: CHECK_INTERVAL_MS, timeoutMs: 500, nodeIds: c.nodes.map(n => n.id),
    enabled: true, expectedStatusCodes: [200], groupId, repeatAfterMs: 0,
    webhookUrl: c.webhookUrl,
  };
}

test('hierarchy: concurrent parent cycle stays visible while probes and alerts continue, then repairs and survives restart', {
  timeout: 180_000,
}, async t => {
  const c = new Cluster();
  t.after(() => c.cleanup());
  await c.initialize([`branch-${randomUUID()}`, `branch-${randomUUID()}`]);
  await c.startAll();
  const [a, b] = c.nodes;
  assert.ok(a && b);
  const groupInput = (name: string) => ({ name, nodeIds: c.nodes.map(n => n.id), graceMs: 0 });
  let left = await c.createGroup(groupInput('User branch one'));
  let right = await c.createGroup(groupInput('User branch two'));
  const separate = await c.createGroup(groupInput('Independent branch'));
  const [target, separateTarget] = await Promise.all([c.createTarget(), c.createTarget()]);
  const leaf = await c.createMonitor(monitorInput(c, target, right.id));
  const independent = await c.createMonitor(monitorInput(c, separateTarget, separate.id));
  const fresh = (o: Overview, m: Monitor, state: string) => {
    const view = o.monitors.find(v => v.monitor.id === m.id);
    return view?.state === state && view.unknownNodes === 0 && !view.configConflict;
  };
  await c.wait('all initial groups and observations replicate', async () =>
    (await c.overviews()).every(o => fresh(o, leaf, 'up') && fresh(o, independent, 'up') &&
      group(o, left.id).healthState === 'pending' && group(o, right.id).healthState === 'up' &&
      group(o, separate.id).healthState === 'up'));

  const viewer = c.secret();
  assert.equal((await c.api(a, '/api/accounts', { method: 'POST', body: { name: 'Hierarchy reader', role: 'viewer', token: viewer } })).status, 201);
  const viewerGroups = await c.api<GroupView[]>(a, '/api/groups', { token: viewer });
  assert.equal(viewerGroups.status, 200);
  assert.equal(viewerGroups.body.find(v => v.group.id === right.id)?.health?.upCount, 1);
  assert.equal((await c.api(a, `/api/groups/${left.id}`, { method: 'PUT', token: viewer, body: { ...left, parentId: right.id } })).status, 403);

  t.diagnostic('individually valid parent edits form a cycle only after partition healing');
  c.partition([[a.id], [b.id]]);
  const first = await c.api<Group>(a, `/api/groups/${left.id}`, { method: 'PUT', body: { ...left, parentId: right.id } });
  const second = await c.api<Group>(b, `/api/groups/${right.id}`, { method: 'PUT', body: { ...right, parentId: left.id } });
  assert.equal(first.status, 200, c.redact(first.text));
  assert.equal(second.status, 200, c.redact(second.text));
  left = first.body;
  right = second.body;
  c.partition([[a.id, b.id]]);
  await c.wait('both processes retain the valid union and expose only the cyclic component as unknown', async () =>
    (await c.overviews()).every(o => {
      const l = group(o, left.id), r = group(o, right.id);
      return l.group.revision === left.revision && r.group.revision === right.revision &&
        !l.configConflict && !r.configConflict && l.healthState === 'unknown' && r.healthState === 'unknown' &&
        l.health === undefined && r.health === undefined && Boolean(l.healthReason) &&
        group(o, separate.id).healthState === 'up' && fresh(o, leaf, 'up');
    }));

  target.state = 'down';
  await c.wait('probes and exactly one connected down delivery continue despite the hierarchy cycle', async () =>
    c.alerts().filter(v => v.monitorId === leaf.id && v.kind === 'down').length === 1 &&
    (await c.overviews()).every(o => fresh(o, leaf, 'down') && group(o, right.id).healthState === 'unknown'));
  const down = c.alerts().find(v => v.monitorId === leaf.id && v.kind === 'down')!;
  await c.wait('successful down receipt reaches both nodes', async () =>
    (await c.overviews()).every(o => o.notifications.some(r => r.monitorId === leaf.id && r.kind === 'down')));

  t.diagnostic('reparenting to root restores nested health without opening a new monitor incident');
  const repaired = await c.api<Group>(a, `/api/groups/${left.id}`, { method: 'PUT', body: { ...left, parentId: '' } });
  assert.equal(repaired.status, 200, c.redact(repaired.text));
  left = repaired.body;
  await c.wait('repaired parent proof restores exact nested counts on both processes', async () =>
    (await c.overviews()).every(o => {
      const l = group(o, left.id), r = group(o, right.id);
      return l.group.revision === left.revision && l.healthState === 'down' && r.healthState === 'down' &&
        l.health?.leafCount === 1 && l.health.downCount === 1 && l.health.activeLeafCount === 1 &&
        r.health?.downCount === 1 && group(o, separate.id).healthState === 'up' &&
        o.incidents.some(i => i.id === down.incidentId && i.resolvedAt === null);
    }));
  await c.quiet('organizational repair must not send another outage alert');
  await c.stop(a);
  await c.start(a);
  await c.wait('hierarchy, original outage and observations survive process restart', async () =>
    (await c.overviews()).every(o => group(o, left.id).healthState === 'down' &&
      group(o, left.id).health?.downCount === 1 && fresh(o, leaf, 'down')));

  target.state = 'up';
  await c.wait('real recovery reaches both nested summaries and sends one recovery', async () =>
    c.alerts().filter(v => v.monitorId === leaf.id && v.kind === 'recovery').length === 1 &&
    (await c.overviews()).every(o => fresh(o, leaf, 'up') && group(o, left.id).healthState === 'up' &&
      group(o, right.id).healthState === 'up'));
  await c.quiet('connected hierarchy proof must have one down and one recovery');
  assert.equal(c.alerts().length, 2);
  assert.ok(c.alerts().every(v => v.monitorId === leaf.id && v.incidentId === down.incidentId));
});
