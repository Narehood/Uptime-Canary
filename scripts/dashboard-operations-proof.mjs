// Root only: python scripts/bounded_validation.py -- node scripts/dashboard-operations-proof.mjs [--benchmark]
// Baseline: d78c81537a481f74bea4f648cb68bde4f72584a4 (merged PR #35).
// Frozen calculations, not old application/runtime snapshots. No cache or output files.
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

process.env.TSX_DISABLE_CACHE = '1';
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'dashboard-operations', tsconfig: resolve('apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve('apps/web/src', file)).href, import.meta.url);
const model = await load('model.ts');
const { nodeCoverage, groupCardIndex, monitorInventory, incidentReceiptIndex } = await load('dashboardDerivations.ts');
const { CHECK_TYPES } = await load('checks.tsx');
const { assess, incidentReceipts, matchesFilter, matchesSearch, isPending, outOfView, observationFor } = model;

// Original assessment; only TypeScript annotations removed.
function previousAssess(view, nodes) {
  const ids = view.monitor.nodeIds;
  const failing = ids.filter(id => observationFor(view, id)?.state === 'down');
  const passing = ids.filter(id => observationFor(view, id)?.state === 'up');
  const pending = ids.filter(id => isPending(observationFor(view, id)?.state));
  const unknown = ids.filter(id => !failing.includes(id) && !passing.includes(id) && !pending.includes(id));
  const base = { failing, passing, pending, unknown, total: ids.length };
  if (view.state === 'paused') return { ...base, kind: 'paused', tone: 'paused', label: 'Paused', detail: '', severity: 7 };
  if (view.configConflict) return { ...base, kind: 'conflict', tone: 'serious', label: 'Unknown', detail: 'config conflict', severity: 3 };
  const notInView = unknown.length > 0 && unknown.every(id => outOfView(nodes.get(id)));
  const unknownText = n => notInView ? `${n} not in view` : `${n} unknown`;
  if (view.state === 'down') {
    if (failing.length && failing.length === ids.length) {
      return { ...base, kind: 'everywhere', tone: 'down', label: 'Down', detail: ids.length === 1 ? `from ${failing[0]}` : 'everywhere', severity: 0 };
    }
    const detail = failing.length ? `${failing.length} of ${ids.length} locations${unknown.length ? ` · ${unknownText(unknown.length)}` : ''}${pending.length ? ` · ${pending.length} pending` : ''}` : '';
    return { ...base, kind: 'partial', tone: 'down', label: 'Down', detail, severity: 1 };
  }
  if (isPending(view.state)) {
    return { ...base, kind: 'pending', tone: 'pending', label: 'Pending', detail: `${pending.length || ids.length} of ${ids.length} starting`, severity: 5.5 };
  }
  if (view.state === 'unknown') {
    const n = unknown.length || view.unknownNodes;
    return { ...base, kind: 'unknown', tone: 'unknown', label: 'Unknown', detail: `${n} of ${ids.length} ${notInView ? 'not in view' : 'without fresh data'}`, severity: 5 };
  }
  return { ...base, kind: 'up', tone: 'up', label: 'Up', detail: '', severity: 6 };
}


// View baselines include every old scan and all requested counts.
function previousCoverage(assessed, nodes) {
  return nodes.map(node => {
    const assigned = assessed.filter(({ view }) => view.monitor.nodeIds.includes(node.id));
    const fresh = assigned.filter(({ view }) => view.observations.some(o => o.nodeId === node.id && o.state !== 'unknown')).length;
    return { assigned: assigned.length, fresh };
  });
}
function currentCoverage(assessed, nodes) {
  const index = nodeCoverage(assessed, nodes);
  return nodes.map(node => index.get(node.id) ?? { assigned: 0, fresh: 0 });
}
function previousGroups(groups, assessed) {
  return groups.map(({ group: g }) => ({
    children: groups.filter(other => other.group.parentId === g.id && other.group.id !== g.id)
      .sort((a, b) => a.group.name.localeCompare(b.group.name)),
    members: assessed.filter(({ view: m }) => m.monitor.groupId === g.id),
  }));
}
function currentGroups(groups, assessed) {
  const index = groupCardIndex(groups, assessed);
  return groups.map(({ group: g }) => ({ children: index.children.get(g.id) ?? [], members: index.members.get(g.id) ?? [] }));
}
function previousInventory(assessed, search, filter, catalogue, groupMap, tags, type = '', tagId = '') {
  const searched = assessed.filter(({ view }) => matchesSearch(view, search, groupMap.get(view.monitor.groupId ?? '')?.group.name,
    view.monitor.tags?.map(tag => `${tags.get(tag.tagId)?.tag.name ?? ''} ${tag.value ?? ''}`).join(' '))
    && (!type || view.monitor.type === type) && (!tagId || view.monitor.tags?.some(tag => tag.tagId === tagId)));
  const rows = searched.filter(({ a }) => matchesFilter(a, filter));
  const count = f => searched.filter(({ a }) => matchesFilter(a, f)).length;
  const types = catalogue.filter(entry => assessed.some(({ view }) => view.monitor.type === entry.type));
  return { rows, counts: { all: count('all'), failing: count('failing'), unknown: count('unknown'), up: count('up'), paused: count('paused') }, types };
}

const filters = ['all', 'failing', 'unknown', 'up', 'paused'];
const states = ['up', 'down', 'pending', 'unknown', 'paused'];
const nodeList = Array.from({ length: 16 }, (_, i) => ({ id: `n${i}`, name: `Node ${i}`, state: i % 4 ? 'connected' : 'unreachable' }));
const nodes = new Map(nodeList.map(node => [node.id, node]));
function makeViews(count, assignments = 8, nodeCount = 16, groupCount = 100) {
  return Array.from({ length: count }, (_, i) => {
    const nodeIds = Array.from({ length: assignments }, (_, j) => `n${(i + j) % nodeCount}`);
    return { monitor: { id: `m${i}`, name: `Service ${i}`, type: CHECK_TYPES[i % CHECK_TYPES.length].type,
      target: `https://service-${i}.example.test`, nodeIds, groupId: `g${i % groupCount}`,
      tags: [{ tagId: `t${i % 8}`, value: i % 2 ? 'edge' : 'core' }],
      enabled: true, intervalMs: 30000, timeoutMs: 5000, revision: 'fixture', updatedAt: 1 },
      observations: nodeIds.map((nodeId, j) => ({ nodeId, state: states[(i + j) % states.length], checkedAt: 1, latencyMs: 1 })),
      state: states[i % states.length], unknownNodes: i % 3, configConflict: i % 11 === 0 };
  });
}
const views = makeViews(1000);
const assessed = views.map(view => ({ view, a: assess(view, nodes) }));
const groups = Array.from({ length: 100 }, (_, i) => ({ group: { id: `g${i}`, name: `Group ${i % 17}`, parentId: i % 5 ? `g${Math.floor(i / 5)}` : undefined,
  nodeIds: ['n0'], revision: 'fixture', updatedAt: 1, deleted: false }, state: 'up', configConflict: false }));
const groupMap = new Map(groups.map(view => [view.group.id, view]));
const tags = new Map(Array.from({ length: 8 }, (_, i) => [`t${i}`, { tag: { id: `t${i}`, name: `Tag ${i}` } }]));
function makeReceipts(count) {
  return Array.from({ length: count }, (_, i) => ({ id: `receipt-${i}`, monitorId: `m${i % 25}`,
    incidentId: `incident-${i % 100}`, destinationId: i % 9 ? `destination-${i % 4}` : undefined,
    channelId: i % 13 ? `channel-${i % 4}` : undefined, kind: ['down', 'recovery', 'repeat'][i % 3],
    nodeId: `n${i % 16}`, sentAt: i })).reverse();
}
const receipts = makeReceipts(1000);
const freeze = value => {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value)) freeze(child);
  }
  return value;
};
// Separate frozen fixtures verify non-mutation without timing frozen-object overhead.
let comparisons = 0;
for (const length of [0, 1, 20, 1000]) {
  const data = freeze(structuredClone(views.slice(0, length)));
  for (const view of data) { assert.deepEqual(assess(view, nodes), previousAssess(view, nodes)); comparisons++; }
  const items = data.map(view => ({ view, a: assess(view, nodes) }));
  const frozenGroups = freeze(structuredClone(groups));
  assert.deepEqual(currentCoverage(items, nodeList), previousCoverage(items, nodeList));
  assert.deepEqual(currentGroups(frozenGroups, items), previousGroups(frozenGroups, items));
  for (const query of ['', '  SERVICE 2 ', 'group 1', 'EDGE', 'tag 3', 'n2', 'missing']) {
    for (const type of ['', CHECK_TYPES[0].type, 'missing']) for (const tagId of ['', 't3', 'missing']) for (const filter of filters) {
      const args = [items, query, filter, CHECK_TYPES, groupMap, tags, type, tagId];
      const expected = previousInventory(...args), actual = monitorInventory(...args);
      assert.deepEqual(actual, expected);
      actual.rows.forEach((row, i) => assert.equal(row, expected.rows[i], 'preserve row identity/order'));
      comparisons++;
    }
  }
  const list = freeze(makeReceipts(length));
  const incidentIds = Array.from({ length: 100 }, (_, i) => ({ id: `incident-${i}` }));
  const index = incidentReceiptIndex(list);
  for (const incident of incidentIds) {
    const actual = index.get(incident.id) ?? [], expected = incidentReceipts(incident, list);
    assert.deepEqual(actual, expected);
    actual.forEach((receipt, i) => assert.equal(receipt, expected[i], 'receipt identity and stable order'));
    comparisons++;
  }
}
// Older/unusual snapshots: duplicate assignments, first observation wins for assessment,
// any non-unknown observation counts as fresh coverage, missing node/group/tag and self-parent.
for (const state of states) for (const first of states) for (const second of states) {
  const view = freeze({ ...makeViews(1)[0], state, configConflict: state === 'paused',
    monitor: { ...views[0].monitor, nodeIds: ['n0', 'n0', 'missing'] },
    observations: [{ nodeId: 'n0', state: first }, { nodeId: 'n0', state: second }] });
  assert.deepEqual(assess(view, nodes), previousAssess(view, nodes));
  const items = [{ view, a: assess(view, nodes) }];
  assert.deepEqual(currentCoverage(items, [...nodeList, { id: 'missing' }]), previousCoverage(items, [...nodeList, { id: 'missing' }]));
  comparisons++;
}
const oddGroups = freeze([{ ...groups[0], group: { ...groups[0].group, parentId: 'g0' } },
  { ...groups[1], group: { ...groups[1].group, parentId: '' } },
  { ...groups[2], group: { ...groups[2].group, parentId: 'missing' } }]);
assert.deepEqual(currentGroups(oddGroups, assessed), previousGroups(oddGroups, assessed));
const indexed = currentGroups(groups, assessed);
for (let i = 0; i < groups.length; i++) {
  indexed[i].children.forEach(child => assert.ok(groups.includes(child), 'original group identity'));
  indexed[i].members.forEach(item => assert.ok(assessed.includes(item), 'original member identity'));
}
assert.deepEqual(currentCoverage([], nodeList), previousCoverage([], nodeList), 'new snapshot discards old membership');
const unusual = freeze([{ ...assessed[0], view: { ...views[0], monitor: { ...views[0].monitor, groupId: 'missing', tags: [{ tagId: 'missing', value: 'legacy' }] } } }]);
for (const query of ['legacy', 'missing', '']) {
  assert.deepEqual(monitorInventory(unusual, query, 'all', CHECK_TYPES, groupMap, tags), previousInventory(unusual, query, 'all', CHECK_TYPES, groupMap, tags));
}
const tiedReceipts = freeze([receipts[2], { ...receipts[0], id: 'tie', sentAt: receipts[2].sentAt }, receipts[0], receipts[1]]
  .map(receipt => ({ ...receipt, incidentId: 'tied' })));
assert.deepEqual(incidentReceiptIndex(tiedReceipts).get('tied'), incidentReceipts({ id: 'tied' }, tiedReceipts), 'timestamp ties preserve source order');

// Exercise the real four view callers with private in-memory context. No DOM,
// network, clipboard, storage or writes; this checks SSR wiring, not browser UX.
const React = (await import('react')).default;
globalThis.React = React;
globalThis.document ??= { addEventListener() {} };
const { renderToStaticMarkup } = await import('react-dom/server');
const { CanaryContext } = await load('context.tsx');
const { NodesView } = await load('views/Nodes.tsx');
const { GroupsView } = await load('views/Groups.tsx');
const { MonitorsView } = await load('views/Monitors.tsx');
const { IncidentsView, IncidentRow } = await load('views/Incidents.tsx');
const compactViews = makeViews(12, 3, 4), compactNodes = nodeList.slice(0, 4);
const compactAssessed = compactViews.map(view => ({ view, a: assess(view, nodes) }));
const incidents = Array.from({ length: 5 }, (_, i) => ({ id: `incident-${i}`, monitorId: `m${i}`, monitorName: `Service ${i}`,
  openedAt: 100, resolvedAt: i % 2 ? 1000 : null, notifiedAt: 200, recoveryNotifiedAt: null }));
const context = {
  endpoint: 'n0', editable: false, role: 'viewer', search: '', nodes: compactNodes, nodeMap: nodes,
  assessed: compactAssessed, monitorMap: new Map(compactViews.map(view => [view.monitor.id, view])),
  assessmentOf: id => compactAssessed.find(item => item.view.monitor.id === id)?.a,
  groups, groupMap, maintenanceMap: new Map(), tags: { list: [...tags.values()], map: tags },
  api: { baseUrl: 'http://fixture.invalid', registeredNode: null }, controlPlane: false,
  overview: { node: compactNodes[0], now: 2000, monitors: compactViews, nodes: compactNodes, incidents, notifications: receipts },
};
const render = (Component, value = context, props = {}) => renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value }, React.createElement(Component, props)));
const nodeMarkup = render(NodesView);
for (const count of previousCoverage(compactAssessed, compactNodes)) assert.ok(nodeMarkup.includes(`${count.fresh} of ${count.assigned}`), 'node view renders derived coverage');
assert.equal((render(MonitorsView).match(/class="rowlink"/gu) ?? []).length, compactAssessed.length, 'monitor view retains every row');
assert.ok(render(GroupsView).includes('Service 1'), 'group index reaches nested member cards');
assert.ok(render(GroupsView, { ...context, groups: null }).includes('offer groups'), 'unsupported-group early return remains valid');
assert.equal((render(IncidentsView).match(/class="inc"/gu) ?? []).length, 3, 'incident view keeps default open scope');
const receiptIndex = incidentReceiptIndex(receipts);
for (const incident of incidents) {
  assert.equal(render(IncidentRow, context, { incident }), render(IncidentRow, context, { incident, receipts: receiptIndex.get(incident.id) ?? [] }), 'indexed and legacy receipt paths render identically');
}
console.log('PASS four view callers and exact incident-row markup with indexed/legacy receipts');
console.log(`PASS ${comparisons} exact before/after comparisons across five operations, plus identity, ordering and frozen-input checks`);

if (process.argv.includes('--benchmark')) {
  let checksum = 0;
  const median = values => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)];
  const stable = values => {
    const recent = values.slice(-4), preceding = values.slice(-8, -4);
    return values.length >= 12 && Math.max(...recent) / Math.min(...recent) < 1.15
      && Math.abs(median(recent) / median(preceding) - 1) < 0.1;
  };
  const workloads = [];
  for (const size of [1000, 50]) {
    const list = size === 1000 ? views : makeViews(size, 3, 4, 10);
    const items = size === 1000 ? assessed : list.map(view => ({ view, a: assess(view, nodes) }));
    const catalogue = size === 1000 ? groups : groups.slice(0, 10);
    const nodeScope = size === 1000 ? nodeList : nodeList.slice(0, 4);
    const notificationList = size === 1000 ? receipts : makeReceipts(size);
    const incidentScope = Array.from({ length: size === 1000 ? 100 : 10 }, (_, i) => ({ id: `incident-${i}` }));
    workloads.push(
      { operation: 'health assessment', size, primary: size === 1000, before: () => list.map(view => previousAssess(view, nodes)), after: () => list.map(view => assess(view, nodes)), consume: rows => rows[0]?.total ?? 0 },
      { operation: 'node coverage', size, primary: size === 1000, before: () => previousCoverage(items, nodeScope), after: () => currentCoverage(items, nodeScope), consume: rows => rows[0]?.assigned ?? 0 },
      { operation: 'group membership', size, primary: size === 1000, before: () => previousGroups(catalogue, items), after: () => currentGroups(catalogue, items), consume: rows => rows[0]?.members.length ?? 0 },
      { operation: 'monitor inventory', size, primary: size === 1000, before: i => previousInventory(items, '', filters[i % 5], CHECK_TYPES, groupMap, tags), after: i => monitorInventory(items, '', filters[i % 5], CHECK_TYPES, groupMap, tags), consume: result => result.rows.length + result.counts.all },
      { operation: 'incident receipt association', size, primary: size === 1000,
        before: () => incidentScope.map(incident => incidentReceipts(incident, notificationList)),
        after: () => { const index = incidentReceiptIndex(notificationList); return incidentScope.map(incident => index.get(incident.id) ?? []); },
        consume: result => result[0]?.length ?? 0 },
    );
  }
  for (const query of ['service', 'not-found']) workloads.push({ operation: `monitor inventory search: ${query}`, size: 1000, primary: false,
    before: i => previousInventory(assessed, query, filters[i % 5], CHECK_TYPES, groupMap, tags),
    after: i => monitorInventory(assessed, query, filters[i % 5], CHECK_TYPES, groupMap, tags), consume: result => result.rows.length + result.counts.all });
  const reports = [];
  for (const workload of workloads) {
    // Calibrate once against baseline to ~25ms per sample; use exactly the same
    // iteration count for both versions and disclose it. Calibration isn't measured.
    let iterations = 1;
    const run = fn => {
      const start = performance.now();
      for (let i = 0; i < iterations; i++) checksum += workload.consume(fn(i));
      return performance.now() - start;
    };
    while (iterations < 8192 && run(workload.before) < 25) iterations *= 2;
    const warmBefore = [], warmAfter = [];
    for (let pair = 0; pair < 40; pair++) {
      if (pair % 2) { warmAfter.push(run(workload.after)); warmBefore.push(run(workload.before)); }
      else { warmBefore.push(run(workload.before)); warmAfter.push(run(workload.after)); }
      if (stable(warmBefore) && stable(warmAfter)) break;
    }
    const before = [], after = [];
    for (let pair = 0; pair < 11; pair++) {
      if (pair % 2) { after.push(run(workload.after)); before.push(run(workload.before)); }
      else { before.push(run(workload.before)); after.push(run(workload.after)); }
    }
    const old = median(before), current = median(after);
    reports.push({ operation: workload.operation, primary: workload.primary, size: workload.size, iterations, pairs: 11,
      warmupPairs: warmBefore.length, warmupStable: stable(warmBefore) && stable(warmAfter),
      beforeMedianMs: old, afterMedianMs: current, reductionPercent: 100 * (old - current) / old,
      beforeSamplesMs: before, afterSamplesMs: after, warmBeforeMs: warmBefore, warmAfterMs: warmAfter });
  }
  const sourceHashes = Object.fromEntries(['model.ts', 'dashboardDerivations.ts', 'views/Nodes.tsx', 'views/Groups.tsx', 'views/Monitors.tsx', 'views/Incidents.tsx']
    .map(file => [file, createHash('sha256').update(readFileSync(resolve('apps/web/src', file))).digest('hex')]));
  console.log(JSON.stringify({ baseline: 'd78c81537a481f74bea4f648cb68bde4f72584a4', runtime: process.version,
    platform: `${process.platform}/${process.arch}`, measured: 'elapsed derivation time including index construction and lookups; excludes React rendering, networking and memoization gains',
    sourceHashes, proofSha256: createHash('sha256').update(readFileSync(new URL(import.meta.url))).digest('hex'), comparisons, reports, checksum }, null, 2));
}
scope.unregister();
