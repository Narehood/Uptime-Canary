import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import test from 'node:test';
import type { Overview } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('Globalping: real processes distinguish provider failures, preserve remote latency, deduplicate alerts and restart safely', {
  timeout: 120_000,
}, async t => {
  const c = new Cluster();
  t.after(() => c.cleanup());
  const token = c.secret();
  let providerStatus = 429;
  let remoteStatus = 200;
  let requests = 0;
  const measurements = new Map<string, { createdAt: string; target: string }>();
  const server = createServer(async (req, res) => {
    requests++;
    res.setHeader('Content-Type', 'application/json');
    if (req.headers.authorization !== `Bearer ${token}`) { res.writeHead(401).end('{}'); return; }
    if (providerStatus !== 200) { res.writeHead(providerStatus).end('{}'); return; }
    if (req.method === 'POST' && req.url === '/v1/measurements') {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      const input = JSON.parse(Buffer.concat(chunks).toString()) as Record<string, unknown>;
      assert.equal(input.type, 'http');
      assert.equal(input.limit, 1);
      assert.equal(input.inProgressUpdates, false);
      assert.equal(input.target, 'remote.example');
      const id = randomUUID();
      measurements.set(id, { createdAt: new Date().toISOString(), target: String(input.target) });
      res.writeHead(202).end(JSON.stringify({ id, probesCount: 1 }));
      return;
    }
    const id = req.url?.split('/').at(-1) ?? '';
    const measurement = measurements.get(id);
    if (req.method !== 'GET' || !measurement) { res.writeHead(404).end('{}'); return; }
    res.end(JSON.stringify({ id, type: 'http', target: measurement.target, createdAt: measurement.createdAt,
      status: 'finished', probesCount: 1,
      results: [{ result: { status: 'finished', statusCode: remoteStatus, rawBody: 'healthy', truncated: false,
        tls: { authorized: true }, timings: { total: 12.75 } } }],
    }));
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  t.after(async () => {
    server.closeAllConnections();
    await new Promise<void>(resolve => server.close(() => resolve()));
  });
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  await c.initialize([`remote-${randomUUID()}`, `remote-${randomUUID()}`]);
  await c.startAll();
  const [a, b] = c.nodes;
  assert.ok(a && b);
  const viewer = c.secret();
  assert.equal((await c.api(a, '/api/accounts', { method: 'POST', body: { name: 'Remote reader', role: 'viewer', token: viewer } })).status, 201);
  for (const mode of ['http', 'HTTP', ' http ', ' HTTP ']) {
    const hidden = await c.createMonitor({ name: 'Disabled private measurement', type: 'globalping',
      target: 'https://remote.example/private-proof-path?key=private-proof-query',
      intervalMs: 1_000, timeoutMs: 900, nodeIds: [a.id, b.id], enabled: false,
      globalping: { mode: mode as 'http', location: 'private-proof-location', token,
        apiUrl: `http://127.0.0.1:${address.port}`, allowInsecureApi: true, http: {} },
    });
    for (const path of ['/api/overview', '/api/monitors', `/api/monitors/${hidden.id}/conflicts`]) {
      const result = await c.api(a, path, { token: viewer });
      assert.equal(result.status, 200);
      for (const secret of ['private-proof-path', 'private-proof-query', 'private-proof-location', token]) {
        assert.ok(!result.text.includes(secret), 'viewer must not receive private remote target or options for any admitted mode spelling');
      }
    }
  }
  const monitor = await c.createMonitor({ name: 'Remote provider proof', type: 'globalping', target: 'https://remote.example/health',
    intervalMs: 1_000, timeoutMs: 900, nodeIds: [a.id, b.id], enabled: true, repeatAfterMs: 0, webhookUrl: c.webhookUrl,
    globalping: { mode: 'http', location: 'US', apiUrl: `http://127.0.0.1:${address.port}`, allowInsecureApi: true,
      token, http: { method: 'GET', keyword: 'healthy' } },
  });
  const observed = (overview: Overview, state: string) => {
    const view = overview.monitors.find(v => v.monitor.id === monitor.id);
    return Boolean(view && !view.configConflict && view.unknownNodes === 0 && view.observations.length === 2 &&
      view.observations.every(o => o.state === state));
  };
  await c.wait('provider quota stays pending on both actual processes', async () =>
    (await c.overviews()).every(o => observed(o, 'pending')));
  assert.equal(c.alerts().length, 0);
  providerStatus = 200;
  await c.wait('remote successful results and exact measured RTT replicate', async () =>
    (await c.overviews()).every(o => observed(o, 'up') && o.monitors.find(v => v.monitor.id === monitor.id)!.observations.every(
      sample => sample.latencyAvailable === true && sample.latencyMs === 12.75)));
  remoteStatus = 503;
  await c.wait('connected remote failure produces exactly one down and shared receipt', async () =>
    c.alerts().filter(v => v.kind === 'down').length === 1 && (await c.overviews()).every(o =>
      observed(o, 'down') && o.notifications.some(r => r.monitorId === monitor.id && r.kind === 'down')));
  const down = c.alerts().find(v => v.kind === 'down')!;
  const sender = c.nodes.find(node => node.id === down.nodeId)!;
  assert.ok(sender);
  await c.stop(sender);
  await c.start(sender);
  await c.wait('restarted sender preserves destination receipt and resumes remote checks', async () =>
    (await c.overviews()).every(o => observed(o, 'down') && o.notifications.some(r => r.monitorId === monitor.id && r.kind === 'down')));
  await c.quiet('restarted remote checks must not replay an accepted down');
  assert.equal(c.alerts().length, 1);
  remoteStatus = 200;
  await c.wait('real remote recovery produces one recovery on the original incident', () =>
    c.alerts().filter(v => v.kind === 'recovery').length === 1);
  await c.quiet('connected remote check has one down and one recovery');
  assert.equal(c.alerts().length, 2);
  assert.ok(c.alerts().every(v => v.incidentId === down.incidentId && v.monitorId === monitor.id));
  assert.ok(requests > 4, 'the actual processes must create and poll their own measurements');
});
