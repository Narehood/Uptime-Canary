// Root: bounded_validation.py -- node scripts/additional-operations-proof.mjs [--benchmark]
// Original modules are read from Git into memory, never checked out or copied to scratch.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { pathToFileURL } from 'node:url';
import { performance } from 'node:perf_hooks';
import ts from 'typescript';

const baseline = '84dffd43663f91f4a1b43204de6de5a18fdaa318';
process.env.TSX_DISABLE_CACHE = '1';
globalThis.location = { origin: 'https://fixture.example.test', protocol: 'https:' };
globalThis.document = { addEventListener() {}, hidden: false };
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'additional-operations', tsconfig: resolve('apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve('apps/web/src', file)).href, import.meta.url);
const baselineHashes = {};
async function original(file) {
  const path = `apps/web/src/${file}`;
  const source = execFileSync('git', ['show', `${baseline}:${path}`], { encoding: 'utf8', maxBuffer: 1024 * 1024 });
  baselineHashes[path] = createHash('sha256').update(source).digest('hex');
  let code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText;
  code = code.replace(/from (['"])([^'"]+)\1/gu, (_, quote, specifier) => {
    const url = specifier.startsWith('.') ? pathToFileURL(resolve(dirname(path), specifier)).href : import.meta.resolve(specifier);
    return `from ${JSON.stringify(url)}`;
  });
  return scope.import(`data:text/javascript,${encodeURIComponent(code)}`, import.meta.url);
}
const fleet = await load('fleet.ts'), oldFleet = await original('fleet.ts');
const dashboard = await load('dashboard.ts'), oldDashboard = await original('dashboard.ts');
const { maintenanceHoldingIndex } = await load('maintenanceIndex.ts');
const { assess } = await load('model.ts');
const now = 1_000_000;
const states = ['answering', 'starting', 'not-answering', 'credential-refused', 'different-node', 'enrolling', 'removing', 'rotating'];
function registration(i) {
  return { id: `r${i}`, nodeId: `n${i}`, nodeName: `Node ${i}`, label: `Node ${i}`, state: states[i % states.length], local: i === 0 };
}
function fixture(nodeCount, monitorCount, deploymentCount = monitorCount, variant = 0) {
  const sources = Array.from({ length: nodeCount }, (_, n) => {
    const node = { ...registration(n), state: 'answering' };
    return { node, status: n && (n + variant) % 7 === 0 ? 'unavailable' : 'ready', readAt: now,
      overview: { node: { id: node.nodeId, name: node.label }, now, user: { role: 'viewer' },
        nodes: [{ id: node.nodeId, name: node.label, state: 'local', lastSeenAt: now }],
        monitors: Array.from({ length: monitorCount }, (_, m) => ({
          monitor: { id: `fleet-p${m}-${n}`, name: `Service ${m % 7}`, revision: `v${m % 3}`, updatedAt: 1, nodeIds: [node.nodeId], enabled: true, type: 'http', target: 'https://fixture.example.test', intervalMs: 30000, timeoutMs: 5000 },
          state: ['up', 'down', 'pending', 'unknown', 'paused'][(m + n + variant) % 5], configConflict: (m + variant) % 19 === 0, unknownNodes: 0,
          observations: [{ monitorId: `fleet-p${m}-${n}`, nodeId: node.nodeId, state: 'up', checkedAt: now, message: 'fixture', latencyMs: 1 }],
        })), incidents: [], notifications: [] } };
  });
  const deployments = Array.from({ length: deploymentCount }, (_, m) => ({
    id: `d${m}`, revision: 'd1', updatedAt: 1, registrationIds: sources.map(s => s.node.id),
    state: variant && m % 5 === 0 ? 'removed' : variant && m % 7 === 0 ? 'pending' : 'applied',
    monitor: sources[0]?.overview.monitors[m % monitorCount]?.monitor ?? { id: `m${m}`, name: 'Absent', nodeIds: [] },
    source: variant && m % 3 === 0 ? { registrationId: 'r0', monitorId: `fleet-p${m}-0`, revision: 'v0' } : undefined,
    placements: sources.map((s, n) => ({ id: `p${m}-${n}`, registrationId: s.node.id, registrationIds: [s.node.id], nodeIds: [s.node.nodeId],
      retiringRegistrationIds: variant && n % 2 ? [sources[0].node.id] : [], monitorId: `fleet-p${m}-${n}`, sequence: 1,
      revision: `v${m % 3}`, state: variant && (m + n) % 6 === 0 ? 'removed' : variant && (m + n) % 7 === 0 ? 'pending' : 'applied' })),
  }));
  return { sources, deployments };
}
function freeze(value) { if (value && typeof value === 'object' && !Object.isFrozen(value)) { Object.freeze(value); for (const child of Object.values(value)) freeze(child); } return value; }
let comparisons = 0;
function compareFleet(input) {
  freeze(input);
  const before = oldFleet.fleetMonitorRows(input.sources, now, input.deployments);
  const after = fleet.fleetMonitorRows(input.sources, now, input.deployments);
  assert.deepEqual(after, before);
  for (let i = 0; i < after.length; i++) for (let j = 0; j < after[i].sources.length; j++) {
    assert.equal(after[i].sources[j].source, before[i].sources[j].source);
    assert.equal(after[i].sources[j].view, before[i].sources[j].view);
  }
  comparisons++;
}
for (const n of [1, 2, 4, 8]) for (const m of [1, 5, 20]) for (const variant of [0, 1, 2]) {
  for (const d of [0, Math.ceil(m / 2), m]) compareFleet(fixture(n, m, d, variant));
}
compareFleet({ sources: [], deployments: [] });
// Preserve authority/ordering for duplicate references, overlapping deployments,
// removed placements, source-only ownership and mutually confirmed HA scopes.
for (const mode of ['peers', 'duplicates', 'source-only', 'retiring', 'stale', 'opaque']) {
  const input = fixture(3, 5, 5, 1);
  if (mode === 'peers') {
    for (const source of input.sources) { source.overview.nodes = input.sources.map(s => ({ id: s.node.nodeId, name: s.node.label, state: s === source ? 'local' : 'connected', lastSeenAt: now })); source.overview.monitors.forEach((v, i) => { v.monitor.id = `shared-${i}`; }); }
    input.deployments.forEach((d, i) => { d.placements.forEach(p => { p.monitorId = `shared-${i}`; }); });
  }
  if (mode === 'duplicates') { input.deployments.push(input.deployments[0], structuredClone(input.deployments[0])); input.deployments[1].placements.push(input.deployments[1].placements[0]); }
  if (mode === 'source-only') for (const d of input.deployments) { d.source = { registrationId: 'r1', monitorId: 'fleet-p0-1', revision: 'v0' }; d.placements = []; }
  if (mode === 'retiring') for (const d of input.deployments) { d.registrationIds = ['r0']; d.placements[0].retiringRegistrationIds = ['r1', 'r2']; }
  if (mode === 'stale') { input.sources[0].readAt = now - 30_001; input.sources[1].overview.node.id = 'replacement'; }
  if (mode === 'opaque') { input.sources[0].node.id = 'r:a'; input.deployments[0].placements[0].registrationId = 'r:a'; input.sources[0].overview.monitors[0].monitor.id = 'b:c'; input.deployments[0].placements[0].monitorId = 'b:c'; }
  compareFleet(input);
}
for (const count of [0, 1, 8, 32, 64]) for (const connection of [undefined, 'outbound']) for (const local of [false, true]) for (const error of [undefined, '', 'refused']) for (const nodeId of ['pending', 'node']) {
  const nodes = freeze(Array.from({ length: count }, (_, i) => ({ ...registration(i), connection, local, error, nodeId })));
  assert.deepEqual(dashboard.registrySummary(nodes), oldDashboard.registrySummary(nodes)); comparisons++;
}
const oldMaintenanceSource = execFileSync('git', ['show', `${baseline}:apps/web/src/views/Maintenance.tsx`], { encoding: 'utf8' });
assert.ok(oldMaintenanceSource.includes('assessed.filter(({ view: monitor }) => monitor.maintenanceIds?.includes(m.id))'));
const previousHolds = (items, ids) => ids.map(id => items.filter(({ view }) => view.maintenanceIds?.includes(id)));
const currentHolds = (items, ids) => { const index = maintenanceHoldingIndex(items, ids); return ids.map(id => index.get(id) ?? []); };
function heldItems(count, windows) {
  return Array.from({ length: count }, (_, i) => { const view = { monitor: { id: `m${i}`, name: `Service ${i}`, nodeIds: [], enabled: true }, state: 'unknown', observations: [], unknownNodes: 0, maintenanceIds: windows ? [`w${i % windows}`, `w${(i + 1) % windows}`] : undefined }; return { view, a: assess(view, new Map()) }; });
}
for (const count of [0, 1, 20, 200]) for (const windowCount of [0, 1, 8, 64]) for (const visible of [0, 1, 4, 64]) {
  const items = heldItems(count, windowCount), ids = Array.from({ length: visible }, (_, i) => `w${i}`);
  if (items.length) { items[0].view.maintenanceIds = ['w0', 'w0', 'w2']; items.push(items[0]); }
  freeze(items); freeze(ids);
  const before = previousHolds(items, ids), after = currentHolds(items, ids);
  assert.deepEqual(after, before);
  before.forEach((bucket, i) => bucket.forEach((item, j) => assert.equal(after[i][j], item))); comparisons++;
}
const React = (await import('react')).default; globalThis.React = React;
const { renderToStaticMarkup } = await import('react-dom/server');
const { CanaryContext } = await load('context.tsx');
const currentView = (await load('views/Maintenance.tsx')).MaintenanceListView;
const previousView = (await original('views/Maintenance.tsx')).MaintenanceListView;
for (const maintenance of [null, [], Array.from({ length: 4 }, (_, i) => ({ maintenance: { id: `w${i}`, name: `Window ${i}`, enabled: true, allMonitors: true, revision: 'v1', schedule: { strategy: 'once', startDate: '2026-11-01', timezone: 'UTC', durationMinutes: 60 } }, active: i % 2 === 0, configConflict: i === 3 }))]) {
  for (const search of ['', 'Window 2', 'not-found']) {
    const value = { maintenance, editable: false, endpoint: 'fixture', search, assessed: heldItems(8, 4), overview: { now }, monitorMap: new Map(), groupMap: new Map() };
    const render = Component => renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value }, React.createElement(Component)));
    assert.equal(render(currentView), render(previousView)); comparisons++;
  }
}
console.log(`PASS ${comparisons} baseline comparisons, frozen input, identity/order, authority edge cases and maintenance-view markup`);

if (process.argv.includes('--benchmark')) {
  const largeFleet = fixture(16, 200), smallFleet = fixture(2, 10);
  const items = heldItems(200, 64), ids = Array.from({ length: 64 }, (_, i) => `w${i}`);
  const nodes = Array.from({ length: 64 }, (_, i) => registration(i));
  const smallNodes = nodes.slice(0, 8);
  const workloads = [
    { operation: 'fleet inventory with placement association', primary: true, fixture: '16 registrations, 200 monitors per node, 200 deployments with 16 placements', before: () => oldFleet.fleetMonitorRows(largeFleet.sources, now, largeFleet.deployments), after: () => fleet.fleetMonitorRows(largeFleet.sources, now, largeFleet.deployments), consume: x => x.length },
    { operation: 'maintenance holding monitors', primary: true, fixture: '200 monitors, 64 visible schedules, two holds per monitor', before: () => previousHolds(items, ids), after: () => currentHolds(items, ids), consume: x => x[0].length },
    { operation: 'registered-node summary', primary: true, fixture: '64 registrations across all eight registry states', before: () => oldDashboard.registrySummary(nodes), after: () => dashboard.registrySummary(nodes), consume: x => x.attention },
    { operation: 'fleet inventory, small', primary: false, fixture: '2 registrations, 10 monitors per node, 10 deployments', before: () => oldFleet.fleetMonitorRows(smallFleet.sources, now, smallFleet.deployments), after: () => fleet.fleetMonitorRows(smallFleet.sources, now, smallFleet.deployments), consume: x => x.length },
    { operation: 'maintenance, single visible schedule', primary: false, fixture: '200 monitors, one visible schedule', before: () => previousHolds(items, ['w0']), after: () => currentHolds(items, ['w0']), consume: x => x[0].length },
    { operation: 'registry, small', primary: false, fixture: '8 registrations', before: () => oldDashboard.registrySummary(smallNodes), after: () => dashboard.registrySummary(smallNodes), consume: x => x.attention },
  ];
  const median = xs => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];
  const stable = xs => xs.length >= 12 && Math.max(...xs.slice(-4)) / Math.min(...xs.slice(-4)) < 1.15 && Math.abs(median(xs.slice(-4)) / median(xs.slice(-8, -4)) - 1) < .1;
  let checksum = 0; const reports = [];
  for (const w of workloads) {
    assert.deepEqual(w.after(), w.before(), `complete benchmark output: ${w.operation}`);
    let iterations = 1;
    const run = fn => { const start = performance.now(); for (let i = 0; i < iterations; i++) checksum += w.consume(fn()); return performance.now() - start; };
    while (iterations < 32768 && run(w.before) < 25) iterations *= 2;
    const warmBefore = [], warmAfter = [], before = [], after = [];
    for (let pair = 0; pair < 40; pair++) { if (pair % 2) { warmAfter.push(run(w.after)); warmBefore.push(run(w.before)); } else { warmBefore.push(run(w.before)); warmAfter.push(run(w.after)); } if (stable(warmBefore) && stable(warmAfter)) break; }
    for (let pair = 0; pair < 11; pair++) { if (pair % 2) { after.push(run(w.after)); before.push(run(w.before)); } else { before.push(run(w.before)); after.push(run(w.after)); } }
    reports.push({ operation: w.operation, primary: w.primary, fixture: w.fixture, iterations, warmupStable: stable(warmBefore) && stable(warmAfter), beforeMedianMs: median(before), afterMedianMs: median(after), reductionPercent: 100 * (median(before) - median(after)) / median(before), beforeSamplesMs: before, afterSamplesMs: after, warmBeforeMs: warmBefore, warmAfterMs: warmAfter });
  }
  const sourceHashes = Object.fromEntries(['fleet.ts', 'dashboard.ts', 'maintenanceIndex.ts', 'views/Maintenance.tsx'].map(file => [file, createHash('sha256').update(readFileSync(resolve('apps/web/src', file))).digest('hex')]));
  console.log(JSON.stringify({ baseline, runtime: process.version, platform: `${process.platform}/${process.arch}`, measured: 'elapsed calculation time including index construction and lookup; excludes React rendering, network and memoization gains', comparisons, baselineHashes, sourceHashes, proofSha256: createHash('sha256').update(readFileSync(new URL(import.meta.url))).digest('hex'), reports, checksum }, null, 2));
}
scope.unregister();
