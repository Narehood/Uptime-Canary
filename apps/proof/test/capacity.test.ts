import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import type { Monitor } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';
import { attempts, channelsReplicated, createChannel, monitorInput, webhookInput } from '../fixtures/notifications.js';

// This is a fixed workload and recovery proof, not a maximum-capacity or SLA claim.
test('64 monitors retain bounded history and drain a destination backlog across two-node restart', { timeout: 240_000 }, async t => {
  const count = 64;
  const historyLimit = 8;
  const cluster = new Cluster({ CANARY_HISTORY_LIMIT: String(historyLimit), CANARY_HISTORY_RETENTION_MS: '60000' });
  const requests: { route: string; monitor: string; source: string; sender: string; kind: string; key: string; accepted: boolean }[] = [];
  const failures: string[] = [];
  let blocked = true, active = 0, peakActive = 0, phase = 'setup';
  const sink = createServer((request, response) => {
    void (async () => {
      let body = '', bytes = 0;
      for await (const chunk of request) {
        bytes += chunk.length;
        if (bytes > 64 * 1024) throw Error('oversized fixture request');
        body += chunk.toString('utf8');
      }
      const payload = JSON.parse(body) as { monitorId?: string; incidentId?: string; node?: { id?: string }; kind?: string };
      const key = request.headers['idempotency-key'];
      if (request.method !== 'POST' || !['/fast', '/backlog'].includes(request.url ?? '') ||
          !payload.monitorId || !payload.incidentId || !payload.node?.id ||
          !['down', 'recovery'].includes(payload.kind ?? '') || typeof key !== 'string') {
        throw Error('invalid fixture request');
      }
      active++; peakActive = Math.max(peakActive, active);
      try {
        // Slow responses contend for delivery workers without using an external provider.
        if (request.url === '/backlog') await delay(75);
        const accepted = request.url !== '/backlog' || !blocked;
        requests.push({ route: request.url!, monitor: payload.monitorId, source: payload.incidentId, sender: payload.node.id,
          kind: payload.kind!, key, accepted });
        response.writeHead(accepted ? 204 : 429, accepted ? {} : { 'Retry-After': '4' }).end();
      } finally { active--; }
    })().catch(() => {
      failures.push('fixture rejected request');
      if (!response.headersSent) response.writeHead(400);
      response.end();
    });
  });
  sink.listen(0, '127.0.0.1'); await once(sink, 'listening');
  t.after(async () => {
    const keys = new Map<string, number>();
    for (const request of requests.filter(value => value.accepted)) {
      const identity = `${request.route}/${request.key}`;
      keys.set(identity, (keys.get(identity) ?? 0) + 1);
    }
    t.diagnostic(JSON.stringify({ phase, requests: requests.length, rejected: requests.filter(request => !request.accepted).length,
      acceptedIdentities: keys.size, repeatedAcceptedIdentities: [...keys.values()].filter(value => value > 1).length,
      repeatedAcceptedRequests: [...keys.values()].reduce((sum, value) => sum + value - 1, 0),
      accepted: Object.fromEntries(['/fast', '/backlog'].flatMap(route => ['down', 'recovery'].map(kind =>
        [`${route}/${kind}`, requests.filter(request => request.accepted && request.route === route && request.kind === kind).length]))),
      fixtureErrors: failures.length }));
    await cluster.cleanup();
    const closed = new Promise<void>((resolve, reject) => sink.close(error => error ? reject(error) : resolve()));
    sink.closeAllConnections(); await closed;
  });
  const address = sink.address(); assert.ok(address && typeof address !== 'string');
  await cluster.initialize(['capacity-a', 'capacity-b']); await cluster.startAll();
  const channels = await Promise.all(['fast', 'backlog'].map(route =>
    createChannel(cluster, webhookInput(route, `http://127.0.0.1:${address.port}/${route}`))));
  await channelsReplicated(cluster, channels);
  const monitors: Monitor[] = [];
  for (let offset = 0; offset < count; offset += 4) {
    monitors.push(...await Promise.all(Array.from({ length: 4 }, (_, index) =>
      cluster.createMonitor(monitorInput(cluster, `Capacity ${offset + index}`, channels.map(channel => channel.id))))));
  }
  const identities = new Set(monitors.map(monitor => monitor.id));
  const settled = async (state: 'up' | 'down') => (await cluster.overviews()).every(overview =>
    monitors.every(monitor => {
      const view = overview.monitors.find(item => item.monitor.id === monitor.id);
      return view?.state === state && cluster.nodes.every(node => view.observations.some(observation =>
        observation.nodeId === node.id && observation.state === state));
    }));
  await cluster.wait('all 128 observing-node slots are up', () => settled('up'), 45_000);
  phase = 'outage';
  const outageAt = performance.now(); cluster.targetState = 'down';
  await cluster.wait('all 128 observing-node slots are down', () => settled('down'), 45_000);
  const delivered = (route: string, kind: string) => new Set(requests.filter(request =>
    request.accepted && request.route === route && request.kind === kind).map(request => request.monitor));
  const quietAfterReceiptExchange = async (route?: string) => {
    // Acknowledged requests can still be in flight when another actor fails
    // over. Allow the existing 5-second send deadline, then require every exact
    // accepted identity in both peers' durable receipt views before quietness.
    await delay(5_000);
    await cluster.wait('both nodes know every accepted receipt identity', async () => {
      const keys = new Set(requests.filter(request => request.accepted && (!route || request.route === route)).map(request => request.key));
      return (await cluster.overviews()).every(overview => keys.size > 0 && [...keys].every(key =>
        overview.notifications.some(receipt => receipt.id === key)));
    }, 45_000);
    const baseline = requests.filter(request => request.accepted && (!route || request.route === route)).length;
    await delay(4_000);
    assert.equal(requests.filter(request => request.accepted && (!route || request.route === route)).length, baseline,
      'accepted receipt exchange stops subsequent sends');
  };
  await cluster.wait('each fast destination accepts while the other destination is rate limited', () =>
    delivered('/fast', 'down').size === count && new Set(requests.filter(request =>
      request.route === '/backlog' && request.kind === 'down' && !request.accepted).map(request => request.monitor)).size === count, 45_000);
  assert.equal(delivered('/backlog', 'down').size, 0);
  const outageToFastMs = performance.now() - outageAt;
  phase = 'api-reads';

  const apiLatencies: number[] = [];
  for (let round = 0; round < 12; round++) {
    for (const node of cluster.nodes) {
      const start = performance.now();
      const response = await fetch(node.url + (round % 2 ? '/api/metrics' : '/api/overview'), {
        headers: { Authorization: `Bearer ${node.adminToken}` }, signal: AbortSignal.timeout(10_000),
      });
      assert.equal(response.status, 200); await response.text();
      apiLatencies.push(performance.now() - start);
    }
  }
  await quietAfterReceiptExchange('/fast');
  const before = await Promise.all(cluster.nodes.map(async node => ({ node, values: (await attempts(cluster, node))
    .filter(attempt => attempt.kind === 'down' && attempt.failures > 0 && attempt.channelId === channels[1]!.id) })));
  assert.ok(before.some(entry => entry.values.length > 0), 'failed attempts are durable before restart');
  phase = 'restart';
  await Promise.all(cluster.nodes.map(node => cluster.stop(node)));
  await cluster.startAll();
  for (const { node, values } of before) {
    const restored = await attempts(cluster, node);
    for (const attempt of values) {
      const current = restored.find(candidate => candidate.id === attempt.id);
      assert.ok(current, 'restart retains the exact queued attempt identity');
      assert.ok(current.failures >= attempt.failures);
      assert.ok((current.nextAttemptAt ?? 0) >= (attempt.nextAttemptAt ?? 0));
    }
  }
  const releaseAt = performance.now(); blocked = false;
  phase = 'backlog-drain';
  // Historical fixture 978078a assumed 45 seconds from provider release.
  // The accepted policy persists a 60-second backoff on every fifth failure
  // (notifications.go). Release cannot bypass it. Keep the 45-second drain
  // window after allowing that backoff; do not change the production policy.
  await cluster.wait('all rate-limited destinations drain after restart and release', () =>
    delivered('/backlog', 'down').size === count, 60_000 + 45_000);
  const backlogDrainMs = performance.now() - releaseAt;
  phase = 'history';
  await cluster.wait('local raw histories reach the configured count cap', async () =>
    (await cluster.history(cluster.nodes[0]!, monitors[0]!.id)).length === historyLimit, 20_000);
  for (const node of cluster.nodes) {
    for (const monitor of monitors) {
      const history = await cluster.history(node, monitor.id);
      assert.ok(history.length > 0 && history.length <= historyLimit);
      assert.ok(history.every(observation => observation.monitorId === monitor.id && observation.nodeId === node.id));
    }
  }
  cluster.targetState = 'up';
  phase = 'recovery';
  await cluster.wait('all observing-node slots recover', () => settled('up'), 45_000);
  await cluster.wait('all 128 destination recoveries are accepted', () =>
    delivered('/fast', 'recovery').size === count && delivered('/backlog', 'recovery').size === count, 45_000);
  await quietAfterReceiptExchange();
  const accepted = requests.filter(request => request.accepted);
  // Historical fixtures 978078a/6c44d41 assumed globally exactly-once provider
  // acceptance. The existing HA contract permits independent source delivery
  // while proof/receipt pages lag, plus ambiguous-send retries. Require every
  // destination and stable identity, and enforce quietness after convergence.
  for (const monitor of identities) for (const route of ['/fast', '/backlog']) for (const kind of ['down', 'recovery']) {
    const values = requests.filter(request => request.monitor === monitor && request.route === route && request.kind === kind);
    assert.ok(values.some(value => value.accepted), 'every monitor/destination accepts down and recovery');
    for (const source of new Set(values.map(value => value.source))) {
      assert.equal(new Set(values.filter(value => value.source === source).map(value => value.key)).size, 1,
        'retries and failover preserve the exact original source/destination delivery identity');
    }
  }
  assert.ok(requests.every(request => identities.has(request.monitor)));
  assert.deepEqual(failures, []);
  assert.ok(peakActive <= 16, 'two eight-worker node pools bound incident-provider concurrency');
  apiLatencies.sort((a, b) => a - b);
  t.diagnostic(JSON.stringify({ monitors: count, nodes: 2, observingSlots: count * 2, destinationsPerMonitor: 2,
    historyLimit, apiReads: apiLatencies.length, apiP95Ms: Math.round(apiLatencies[Math.ceil(apiLatencies.length * 0.95) - 1]!),
    apiMaxMs: Math.round(apiLatencies.at(-1)!), outageToFastMs: Math.round(outageToFastMs),
    backlogDrainMs: Math.round(backlogDrainMs), accepted: accepted.length, providerPeakConcurrent: peakActive }));
  phase = 'complete';
});
