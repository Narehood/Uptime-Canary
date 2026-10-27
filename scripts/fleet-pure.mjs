// Nonvisual inventory/reader regression. Root runs through bounded_validation.py, no browser or live node.
import assert from 'node:assert/strict';
import { resolve, dirname } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
process.env.TSX_DISABLE_CACHE = '1';
globalThis.location = { origin: 'https://dashboard.example.test', protocol: 'https:' };
globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true;
globalThis.document = { addEventListener() {}, hidden: false };
const { register } = await import('tsx/esm/api');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const scope = register({ namespace: 'fleet-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(root, 'apps/web/src', path)).href, import.meta.url);
const fleet = await load('fleet.ts');
const { ApiFailure, CanaryApi } = await load('api.ts');
const now = 1_000_000;
const registration = (letter, nodeId = letter) => ({ id: letter.repeat(32), nodeId, nodeName: nodeId, label: `Node ${nodeId}`, local: false, state: 'answering', url: 'https://private.invalid:3100' });
const A = registration('a'), B = registration('b');
const monitor = (id = 'same-monitor', revision = 'shared-revision', nodeIds = ['a', 'b']) => ({ id, revision, updatedAt: 1, name: 'Service', type: 'http', target: 'https://service.example.test', nodeIds, enabled: true, intervalMs: 1000, timeoutMs: 500 });
const observation = (nodeId, state = 'up') => ({ monitorId: 'same-monitor', nodeId, state, checkedAt: now, latencyMs: 12, message: state });
const source = (node, options = {}) => ({ node, status: 'ready', readAt: now, overview: { node: { id: node.nodeId, name: node.nodeName }, now, user: { name: 'viewer', role: 'viewer' }, nodes: [{ id: node.nodeId, name: node.nodeId, state: 'local', lastSeenAt: now }], monitors: [{ monitor: monitor(), state: 'up', unknownNodes: 0, configConflict: false, observations: [observation('a'), observation('b')] }], incidents: [], notifications: [] }, ...options });
const peer = (s, other) => ({ ...s, overview: { ...s.overview, nodes: [...s.overview.nodes, { id: other.node.nodeId, name: other.node.nodeId, state: 'connected', lastSeenAt: now }] } });
let failed = 0;
async function check(name, fn) { try { await fn(); console.log(`PASS ${name}`); } catch (error) { failed++; console.error(`FAIL ${name}: ${error.message}`); } }

await check('independent nodes with the same monitor ID or name remain separate', () => {
  const rows = fleet.fleetMonitorRows([source(A), source(B)], now);
  assert.equal(rows.length, 2); assert.notEqual(rows[0].key, rows[1].key);
  const unseenA = source(A), unseenB = source(B);
  unseenA.overview.nodes.push({ id: 'b', name: 'b', state: 'unreachable', lastSeenAt: null });
  unseenB.overview.nodes.push({ id: 'a', name: 'a', state: 'unreachable', lastSeenAt: null });
  assert.equal(fleet.fleetMonitorRows([unseenA, unseenB], now).length, 2, 'merely listing peers does not establish shared scope');
});

await check('known peer copies form one row with authoritative per-node results and disagreement preserved', () => {
  const a = source(A), b = source(B); b.overview.monitors[0].observations[1].state = 'down';
  const peers = [peer(a, b), peer(b, a)];
  const [row] = fleet.fleetMonitorRows(peers, now);
  assert.equal(fleet.fleetMonitorRows(peers, now).length, 1);
  assert.deepEqual(row.results.map(r => [r.nodeId, r.state, r.registrationId, r.viaPeer]), [['a', 'up', A.id, false], ['b', 'down', B.id, false]]);
  assert.equal(row.assessment.tone, 'down');
  peers[1].overview.monitors[0].monitor = monitor('same-monitor', 'other-revision');
  const [changed] = fleet.fleetMonitorRows(peers, now);
  assert.equal(changed.differingViews, true); assert.equal(changed.assessment.tone, 'serious');
});

await check('failed, pending and expired snapshots never keep an unmarked healthy result', () => {
  for (const status of ['unavailable', 'pending', 'loading']) {
    const [row] = fleet.fleetMonitorRows([source(A, { status })], now);
    assert.ok(row.results.every(result => result.state === 'unknown'));
    assert.equal(row.assessment.kind, 'unknown');
  }
  const [expired] = fleet.fleetMonitorRows([source(A)], now + fleet.FLEET_SNAPSHOT_MAX_AGE_MS + 1);
  assert.equal(expired.assessment.kind, 'unknown');
  assert.equal(fleet.fleetSourceCurrent(source(A), now - 1), false, 'a backwards browser clock is not a current snapshot');
});

await check('peer fallback labels its source, and peers disagreeing about the same node produce unknown', () => {
  const a = source(A), b = source(B), C = registration('c'), c = source(C);
  const known = [a, b, c].map(s => ({ ...s, overview: { ...s.overview, nodes: [a, b, c].map(t => ({ id: t.node.nodeId, name: t.node.nodeId, state: t.node.id === s.node.id ? 'local' : 'connected', lastSeenAt: now })) } }));
  known[1].status = 'unavailable';
  let [row] = fleet.fleetMonitorRows(known, now);
  const remote = row.results.find(result => result.nodeId === 'b');
  assert.equal(remote.state, 'up'); assert.equal(remote.viaPeer, true); assert.equal(remote.registrationId, A.id);
  known[2].overview.monitors[0].observations[1].state = 'down';
  [row] = fleet.fleetMonitorRows(known, now);
  assert.equal(row.results.find(result => result.nodeId === 'b').state, 'unknown');
});

await check('paused and empty/deleted monitor states stay explicit and source inputs are unchanged', () => {
  const a = source(A); a.overview.monitors[0].state = 'paused';
  const before = JSON.stringify(a);
  assert.equal(fleet.fleetMonitorRows([a], now)[0].assessment.kind, 'paused');
  assert.equal(JSON.stringify(a), before);
  a.overview.monitors[0].monitor.deleted = true;
  assert.equal(fleet.fleetMonitorRows([a], now).length, 0);
  assert.deepEqual(fleet.fleetMonitorRows([], now), []);
});

await check('reader limits concurrency, covers every registration once and skips pending operations', async () => {
  let active = 0, peak = 0; const calls = [];
  const nodes = Array.from({ length: 12 }, (_, i) => registration((i + 1).toString(16)));
  nodes[0].state = 'rotating';
  const api = { withRegisteredNode(id) { calls.push(id); return { async overview() { active++; peak = Math.max(peak, active); await new Promise(resolve => setTimeout(resolve, 1)); active--; return source(nodes.find(node => node.id === id)).overview; } }; } };
  const read = await fleet.readFleet(api, nodes, 'viewer', new AbortController().signal, [], undefined, () => now);
  assert.ok(peak <= 4 && peak > 1); assert.equal(calls.length, 11); assert.equal(read.length, 12);
  assert.equal(new Set(calls).size, calls.length); assert.equal(read[0].status, 'pending');
  assert.ok(read.slice(1).every(s => s.status === 'ready' && s.readAt === now));
});

await check('one failed node preserves useful inventory metadata, changed identity cannot inherit a cache', async () => {
  const api = { withRegisteredNode() { return { overview: async () => { throw new ApiFailure(502, 'Node unavailable'); } }; } };
  const old = source(A);
  const [cached] = await fleet.readFleet(api, [A], 'viewer', new AbortController().signal, [old]);
  assert.equal(cached.status, 'unavailable'); assert.equal(cached.overview, old.overview);
  const [changed] = await fleet.readFleet(api, [{ ...A, nodeId: 'replacement' }], 'viewer', new AbortController().signal, [old]);
  assert.equal(changed.overview, undefined); assert.equal(changed.readAt, null);
});

await check('identity or role mismatch is unavailable; dashboard denials reject the whole batch', async () => {
  for (const status of [401, 403]) {
    const api = { withRegisteredNode() { return { overview: async () => { throw new ApiFailure(status, 'Dashboard authorization changed'); } }; } };
    await assert.rejects(fleet.readFleet(api, [A, B], 'viewer', new AbortController().signal), e => e instanceof ApiFailure && e.status === status);
  }
  for (const overview of [source(B).overview, { ...source(A).overview, user: { name: 'admin', role: 'admin' } }]) {
    const api = { withRegisteredNode() { return { overview: async () => overview }; } };
    assert.equal((await fleet.readFleet(api, [A], 'viewer', new AbortController().signal))[0].status, 'unavailable');
  }
});

await check('pre-cancelled batches start no request and malformed registry lists are refused', async () => {
  let calls = 0;
  const api = { withRegisteredNode() { calls++; throw Error('should not be called'); } };
  const controller = new AbortController(); controller.abort();
  await assert.rejects(fleet.readFleet(api, [A], 'viewer', controller.signal), e => e.name === 'AbortError');
  assert.equal(calls, 0);
  await assert.rejects(fleet.readFleet(api, [A, A], 'viewer', new AbortController().signal), e => e instanceof ApiFailure);
  await assert.rejects(fleet.readFleet(api, Array(65).fill(A), 'viewer', new AbortController().signal), e => e instanceof ApiFailure);
});

await check('real client bindings use the page origin and the same credential with one registration header', async () => {
  const calls = [];
  globalThis.fetch = async (url, init) => { calls.push({ url: String(url), headers: new Headers(init.headers) }); return Response.json(source(A).overview); };
  const api = new CanaryApi(location.origin, 'fixture-dashboard-token');
  await fleet.readFleet(api, [A], 'viewer', new AbortController().signal);
  assert.equal(calls.length, 1); assert.equal(calls[0].url, location.origin + '/api/overview');
  assert.equal(calls[0].headers.get('X-Canary-Registered-Node'), A.id);
  assert.ok(calls[0].headers.get('Authorization') === 'Bearer fixture-dashboard-token', 'dashboard credential changed');
  assert.equal(api.registeredNode, null, 'reading the fleet never changes the selected node');
});
scope.unregister();
process.exitCode = failed ? 1 : 0;
