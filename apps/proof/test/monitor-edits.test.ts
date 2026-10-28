import assert from 'node:assert/strict';
import test from 'node:test';
import type { Monitor, MonitorInput } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('editing an ongoing outage preserves its notification identity across peers and restart', { timeout: 150_000 }, async t => {
  const cluster = new Cluster();
  t.after(() => cluster.cleanup());
  await cluster.initialize(['edit-observer-a', 'edit-observer-b']);
  await cluster.startAll();
  const node = cluster.nodes[0]!;
  const other = cluster.nodes[1]!;
  const target = await cluster.createTarget();
  const firstGroup = await cluster.createGroup({ name: 'First user group', nodeIds: cluster.nodes.map(peer => peer.id), graceMs: 0 });
  const secondGroup = await cluster.createGroup({ name: 'Second user group', nodeIds: cluster.nodes.map(peer => peer.id), graceMs: 0 });
  let input: MonitorInput = {
    name: 'Continuous outage', type: 'http', target: target.url,
    internalOnly: true, intervalMs: 1_000, timeoutMs: 500, nodeIds: cluster.nodes.map(peer => peer.id),
    enabled: true, expectedStatusCodes: [200], webhookUrl: cluster.webhookUrl,
    repeatAfterMs: 0, groupId: firstGroup.id,
  };
  let monitor = await cluster.createMonitor(input);

  const fresh = async (state: 'up' | 'down', since: number): Promise<void> => {
    await cluster.wait(`both processes observe the current revision ${state}`, async () =>
      (await cluster.overviews()).every(overview => {
        const view = overview.monitors.find(item => item.monitor.id === monitor.id);
        return view?.monitor.revision === monitor.revision && view.state === state && view.unknownNodes === 0 &&
          view.observations.length === cluster.nodes.length && view.observations.every(observation =>
            observation.state === state && observation.checkedAt >= since);
      }));
  };
  await fresh('up', Date.now() - 5_000);
  const outageAt = Date.now();
  target.state = 'down';
  await fresh('down', outageAt);
  await cluster.wait('one initial outage is actually delivered', () => cluster.alerts().length > 0);
  await cluster.quiet('the connected outage initially deduplicates', 1);
  const initial = cluster.alerts()[0]!;
  assert.equal(initial.kind, 'down');
  const canonical = (await cluster.overview(node)).incidents.find(incident => incident.monitorId === monitor.id && incident.resolvedAt === null)!;
  assert.ok(canonical?.id);
  const openedAt = canonical.openedAt;

  for (const change of [
    { groupId: secondGroup.id },
    { name: 'Renamed during the same outage', internalOnly: true, intervalMs: 1_100 },
  ]) {
    input = { ...input, ...change };
    const editedAt = Date.now();
    const response = await cluster.api<Monitor>(node, `/api/monitors/${monitor.id}`, {
      method: 'PUT', body: { ...input, revision: monitor.revision },
    });
    assert.equal(response.status, 200, cluster.redact(response.text));
    assert.notEqual(response.body.revision, monitor.revision);
    monitor = response.body;
    await fresh('down', editedAt);
    await cluster.quiet('metadata edits cannot send a second initial outage', 1);
    for (const overview of await cluster.overviews()) {
      const incidents = overview.incidents.filter(incident => incident.monitorId === monitor.id);
      assert.equal(incidents.length, 1, 'an edit must not strand an old incident beside a new one');
      assert.equal(incidents[0]!.id, canonical.id);
      assert.equal(incidents[0]!.openedAt, openedAt);
      assert.equal(incidents[0]!.resolvedAt, null);
      assert.ok(incidents[0]!.notifiedAt !== null, 'delivery evidence survives edits');
    }
  }

  await cluster.stop(other);
  await cluster.start(other);
  await fresh('down', Date.now() - 5_000);
  await cluster.quiet('durable edits and late peer state cannot resurrect an initial notification', 1);

  const recoveredAt = Date.now();
  target.state = 'up';
  await fresh('up', recoveredAt);
  await cluster.wait('a real recovery is delivered for the original outage', () => cluster.alerts().some(alert => alert.kind === 'recovery'));
  await cluster.quiet('the edited outage recovers exactly once', 2);
  const recovery = cluster.alerts()[1]!;
  assert.equal(recovery.incidentId, initial.incidentId);
  for (const overview of await cluster.overviews()) {
    const incidents = overview.incidents.filter(incident => incident.monitorId === monitor.id);
    assert.equal(incidents.length, 1);
    assert.equal(incidents[0]!.id, canonical.id);
    assert.ok(incidents[0]!.resolvedAt !== null, 'the original incident really closes');
    assert.ok(incidents[0]!.recoveryNotifiedAt !== null);
  }

  const laterAt = Date.now();
  target.state = 'down';
  await fresh('down', laterAt);
  await cluster.wait('a later actual outage still sends', () => cluster.alerts().length >= 3);
  await cluster.quiet('the later outage also deduplicates', 3);
  const later = cluster.alerts()[2]!;
  assert.equal(later.kind, 'down');
  assert.notEqual(later.incidentId, initial.incidentId);
  assert.notEqual(later.event.idempotencyKey, initial.event.idempotencyKey);
});
