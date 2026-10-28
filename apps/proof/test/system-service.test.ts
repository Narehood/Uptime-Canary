import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import test from 'node:test';
import type { Monitor, MonitorInput } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('local system-service monitoring uses the production worker and retains its configuration across peers and restart', { timeout: 90_000 }, async t => {
  const cluster = new Cluster();
  t.after(() => cluster.cleanup());
  await cluster.initialize(['service-a', 'service-b']);
  await cluster.startAll();
  const [a, b] = cluster.nodes;
  assert.ok(a && b);
  // Only a random nonexistent service is queried, read-only. No host service is
  // installed, started, stopped, or assumed to be running by this proof.
  const target = 'uptime-canary-proof-missing-' + randomBytes(16).toString('hex');
  let input: MonitorInput = { name: 'Local manager proof', type: 'system-service', target,
    internalOnly: true, intervalMs: 5_000, timeoutMs: 3_000, nodeIds: [a.id, b.id], enabled: true, repeatAfterMs: 0 };
  let monitor = await cluster.createMonitor(input);
  const settle = async (): Promise<void> => {
    await cluster.wait('both processes retain the service config and local worker results', async () =>
      (await cluster.overviews()).every(overview => {
        const view = overview.monitors.find(item => item.monitor.id === monitor.id);
        return view?.monitor.revision === monitor.revision && view.observations.length === 2 &&
          view.observations.every(observation => input.nodeIds.includes(observation.nodeId) && observation.latencyAvailable === false && (
              observation.state === 'down' && observation.message === 'Service is not installed' ||
              observation.state === 'pending' && (observation.message === 'Service status unavailable' ||
                observation.message.startsWith('service worker ') || observation.message === 'service check canceled')));
      }));
  };
  await settle();
  for (const invalid of ['--help', '../sshd', 'ssh*', 'Print Spooler', 'a;bad']) {
    const rejected = await cluster.api(a, '/api/monitors', { method: 'POST', body: { ...input, target: invalid } });
    assert.equal(rejected.status, 400, 'a service name must never become a command or pattern');
  }
  const generation = monitor.checkGeneration || monitor.revision;
  input = { ...input, name: 'Renamed local manager proof' };
  const renamed = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, revision: monitor.revision } });
  assert.equal(renamed.status, 200, cluster.redact(renamed.text)); monitor = renamed.body;
  assert.equal(monitor.checkGeneration || monitor.revision, generation);
  await settle();
  await cluster.stop(b); await cluster.start(b); await settle();
  assert.equal((await cluster.overview(b)).monitors.find(v => v.monitor.id === monitor.id)?.monitor.target, target);
  assert.equal(cluster.events.length, 0, 'this proof configures no external notification destination');
});
