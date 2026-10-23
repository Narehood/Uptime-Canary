import assert from 'node:assert/strict';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { Cluster } from '../fixtures/cluster.js';

test('passive heartbeats accepted by one process keep connected peers healthy, expire once, and recover across restart', { timeout: 90_000 }, async t => {
  const cluster = new Cluster();
  let feeder: AbortController | undefined;
  let feeding: Promise<void> | undefined;
  let feedError: unknown;
  const stopFeed = async (): Promise<void> => {
    feeder?.abort();
    await feeding;
    feeder = undefined;
    feeding = undefined;
    if (feedError) throw feedError;
  };
  t.after(async () => {
    try { await stopFeed(); } finally { await cluster.cleanup(); }
  });
  await cluster.initialize(['heartbeat-a', 'heartbeat-b']);
  await cluster.startAll();
  const [a, b] = cluster.nodes;
  assert.ok(a && b);
  // Cluster.secret() is generated locally and tracked by fixture redaction.
  const secret = cluster.secret();
  assert.match(secret, /^[a-f0-9]{64}$/);
  const monitor = await cluster.createMonitor({
    name: 'Passive application heartbeat', type: 'push', target: '',
    intervalMs: 3_000, timeoutMs: 500, nodeIds: [a.id, b.id], enabled: true,
    push: { token: secret }, webhookUrl: cluster.webhookUrl, repeatAfterMs: 0,
  });
  assert.equal(monitor.target, 'push');
  assert.ok(monitor.pushStartedAt && monitor.pushStartedAt > 0);
  const post = async (authorization: string, body: unknown) => {
    const r = await fetch(`${a.url}/api/push`, {
      method: 'POST', headers: { Authorization: authorization, 'Content-Type': 'application/json' },
      body: JSON.stringify(body), signal: AbortSignal.timeout(3_000),
    });
    const text = await r.text();
    assert.ok(!text.includes(secret), 'push responses cannot reflect the credential');
    return { status: r.status, text };
  };
  for (const credential of [a.adminToken, cluster.clusterToken, secret]) {
    assert.equal((await post(`Bearer ${credential}`, { status: 'up' })).status, 404, 'management/cluster Bearer scheme cannot submit a heartbeat');
  }
  const get = await fetch(`${a.url}/api/push/${secret}?status=up`);
  assert.equal(get.status, 404, 'URL credentials require explicit GET opt-in');
  assert.equal((await post(`Push ${secret}`, { status: 'up', timestamp: Date.now() })).status, 400, 'clients cannot supply timestamp/causal authority');
  const startFeed = (): void => {
    feeder = new AbortController();
    const signal = feeder.signal;
    feeding = (async () => {
      try {
        while (!signal.aborted) {
          const r = await post(`Push ${secret}`, { status: 'up', msg: `Private heartbeat ${secret}`, ping: 12.5 });
          assert.ok(r.status === 200 || r.status === 429, cluster.redact(r.text));
          await delay(500, undefined, { signal });
        }
      } catch (error) {
        if (!signal.aborted) feedError = error;
      }
    })();
  };
  const both = async (state: 'up' | 'down'): Promise<boolean> => {
    if (feedError) throw feedError;
    return (await cluster.overviews()).every(overview => {
      const view = overview.monitors.find(v => v.monitor.id === monitor.id);
      return view?.state === state && view.unknownNodes === 0 && view.observations.length === 2 &&
        view.observations.every(o => o.state === state);
    });
  };
  startFeed();
  await cluster.wait('one authenticated endpoint feeds both connected observers', () => both('up'));
  for (const node of [a, b]) {
    const history = await cluster.history(node, monitor.id);
    assert.ok(history.some(o => o.state === 'up' && o.latencyAvailable === true && o.latencyMs === 12.5));
    assert.ok(history.every(o => !o.message.includes(secret)), 'a client cannot accidentally echo its push credential into history');
  }
  await stopFeed();
  await cluster.wait('missing heartbeat becomes down at both processes', () => both('down'));
  await cluster.wait('connected expiry sends exactly one down', () => cluster.alerts().length === 1);
  await cluster.quiet('a missed heartbeat outage remains deduplicated', 1);
  const first = cluster.alerts()[0]!;
  assert.equal(first.kind, 'down');
  startFeed();
  await cluster.wait('a later causal heartbeat recovers both processes', () => both('up'));
  await cluster.wait('actual accepted heartbeat recovery sends once', () => cluster.alerts().length === 2);
  assert.equal(cluster.alerts()[1]!.kind, 'recovery');
  assert.equal(cluster.alerts()[1]!.incidentId, first.incidentId);
  await cluster.stop(b);
  await cluster.start(b);
  await cluster.wait('latest heartbeat state and configuration survive peer process restart', () => both('up'));
  await cluster.quiet('restart cannot replay the accepted heartbeat outage/recovery', 2);
  await stopFeed();
  t.diagnostic('Two compiled Go processes, authenticated Push scheme, causal replication, real expiry and webhook recovery, supplied latency, secret redaction and SIGKILL/restart. No external heartbeat or provider endpoints.');
});
