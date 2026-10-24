import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { randomBytes } from 'node:crypto';
import test from 'node:test';
import type { Monitor, MonitorInput } from '../../../packages/shared/src/index.js';
import { Cluster } from '../fixtures/cluster.js';

test('Steam directory checks use private API fixtures, preserve HA configuration and never invent game RTT', { timeout: 90_000 }, async t => {
  const cluster = new Cluster();
  const key = 'fixture-steam-key-' + randomBytes(16).toString('hex');
  const rotatedKey = 'fixture-steam-rotated-' + randomBytes(16).toString('hex');
  const seenKeys = new Set<string>();
  const errors: string[] = [];
  let status = 429, listed = false, directoryRequests = 0;
  const api = createServer((request, response) => {
    directoryRequests++;
    const url = new URL(request.url!, 'http://127.0.0.1');
    if (request.method !== 'GET' || url.pathname !== '/directory/IGameServersService/GetServerList/v1/' ||
        url.searchParams.get('filter') !== 'addr\\127.0.0.9:27015' || url.searchParams.get('limit') !== '1' ||
        [...url.searchParams].length !== 3 || ![key, rotatedKey].includes(url.searchParams.get('key') ?? '') ||
        request.headers.authorization || request.headers.cookie) errors.push('unexpected directory request');
    seenKeys.add(url.searchParams.get('key') ?? '');
    response.writeHead(status, { 'Content-Type': 'application/json' });
    response.end(status !== 200 ? '{"error":"private provider diagnostic"}' : JSON.stringify({ response: {
      servers: listed ? [{ addr: '127.0.0.9:27015', name: 'private server metadata' }] : [],
    } }));
  });
  api.listen(0, '127.0.0.1'); await once(api, 'listening');
  t.after(async () => { await cluster.cleanup(); await new Promise<void>((resolve, reject) => api.close(error => error ? reject(error) : resolve())); });
  const address = api.address(); assert.ok(address && typeof address !== 'string');
  await cluster.initialize(['steam-a', 'steam-b']); await cluster.startAll();
  const [a, b] = cluster.nodes; assert.ok(a && b);
  let input: MonitorInput = { name: 'Private Steam directory', type: 'steam', target: '127.0.0.9:27015', intervalMs: 1_000,
    timeoutMs: 750, nodeIds: [a.id, b.id], enabled: true, steam: { apiKey: key, apiUrl: `http://127.0.0.1:${address.port}/directory`,
      allowInsecureApi: true, skipLatency: true, packetSize: 0 } };
  let monitor = await cluster.createMonitor(input);
  const settle = async (state: 'pending' | 'down' | 'up'): Promise<void> => {
    await cluster.wait(`both Steam peers report ${state} from the private directory`, async () =>
      (await cluster.overviews()).every(overview => {
        const view = overview.monitors.find(item => item.monitor.id === monitor.id);
        return view?.monitor.revision === monitor.revision && view.observations.length === 2 && view.observations.every(observation =>
          observation.state === state && observation.latencyAvailable === false && observation.latencyMs === 0 && !observation.statusCode &&
          !/private|fixture-steam/.test(observation.message));
      }));
    assert.deepEqual(errors, []);
  };
  await settle('pending'); status = 200; await settle('down'); listed = true; await settle('up');
  const generation = monitor.checkGeneration || monitor.revision;
  input = { ...input, name: 'Renamed Steam directory' };
  let saved = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, revision: monitor.revision } });
  assert.equal(saved.status, 200, cluster.redact(saved.text)); monitor = saved.body;
  assert.equal(monitor.checkGeneration || monitor.revision, generation); assert.deepEqual(monitor.steam, input.steam);
  await settle('up');
  input = { ...input, steam: { ...input.steam!, apiKey: rotatedKey } };
  saved = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}`, { method: 'PUT', body: { ...input, revision: monitor.revision } });
  assert.equal(saved.status, 200, cluster.redact(saved.text)); monitor = saved.body;
  assert.notEqual(monitor.checkGeneration || monitor.revision, generation); await settle('up');
  await cluster.stop(b); await cluster.start(b); await settle('up');
  assert.deepEqual((await cluster.overview(b)).monitors.find(v => v.monitor.id === monitor.id)?.monitor.steam, input.steam);
  assert.ok(seenKeys.has(key) && seenKeys.has(rotatedKey));
  // Two disconnected writes retain different option payloads, then require an
  // exact complete-head resolution. No provider request is sent while disabled.
  cluster.partition([[a.id], [b.id]]);
  const parent = monitor.revision;
  const branches = await Promise.all([a, b].map((node, index) => cluster.api<Monitor>(node, `/api/monitors/${monitor.id}`, {
    method: 'PUT', body: { ...input, enabled: false, name: 'Steam branch ' + index,
      steam: { ...input.steam!, packetSize: index === 0 ? 0 : 32 }, revision: parent },
  })));
  for (const branch of branches) assert.equal(branch.status, 200, cluster.redact(branch.text));
  cluster.reconnect();
  await cluster.wait('both peers retain both conflicting Steam heads', async () =>
    (await Promise.all([a, b].map(node => cluster.api<Monitor[]>(node, `/api/monitors/${monitor.id}/conflicts`))))
      .every(result => result.status === 200 && result.body.length === 2));
  // Let any pre-disable request finish, then observe more than one full
  // scheduling interval without authorizing another directory request.
  await new Promise(resolve => setTimeout(resolve, 1000));
  const disabledRequests = directoryRequests;
  await new Promise(resolve => setTimeout(resolve, 2100));
  assert.equal(directoryRequests, disabledRequests, 'disabled/conflicting monitors sent provider requests');
  const heads = await cluster.api<Monitor[]>(a, `/api/monitors/${monitor.id}/conflicts`);
  assert.deepEqual(heads.body.map(head => head.steam?.packetSize).sort((x, y) => x! - y!), [0, 32]);
  const selected = heads.body.find(head => head.steam?.packetSize === 0)!;
  const resolved = await cluster.api<Monitor>(a, `/api/monitors/${monitor.id}/resolve`, {
    method: 'POST', body: { ...selected, name: 'Resolved Steam directory', enabled: true, revisions: heads.body.map(head => head.revision) },
  });
  assert.equal(resolved.status, 200, cluster.redact(resolved.text)); monitor = resolved.body;
  assert.deepEqual(monitor.steam, input.steam); await settle('up');
  const viewer = randomBytes(32).toString('hex');
  const account = await cluster.api(a, '/api/accounts', { method: 'POST', body: { name: 'Steam viewer', role: 'viewer', token: viewer } });
  assert.equal(account.status, 201, cluster.redact(account.text));
  for (const endpoint of ['/api/overview', '/api/monitors', `/api/monitors/${monitor.id}/conflicts`]) {
    const response = await fetch(a.url + endpoint, { headers: { Authorization: 'Bearer ' + viewer } });
    assert.equal(response.status, 200); const body = await response.text();
    assert.ok(!body.includes(key) && !body.includes(rotatedKey) && !body.includes('"steam":'));
  }
  assert.equal(cluster.events.length, 0, 'no notification destination was configured');
});
