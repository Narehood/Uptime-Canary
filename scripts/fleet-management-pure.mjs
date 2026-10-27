// Root-only, bounded same-origin fixtures. No real nodes, external requests, storage or notifications.
import assert from 'node:assert/strict';
import { resolve, dirname } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
process.env.TSX_DISABLE_CACHE = '1';
globalThis.location = { origin: 'https://dashboard.example.test', protocol: 'https:', hash: '' };
globalThis.document = { addEventListener() {}, removeEventListener() {}, hidden: false };
globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true;
const { register } = await import('tsx/esm/api');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const scope = register({ namespace: 'fleet-management-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = p => scope.import(pathToFileURL(resolve(root, 'apps/web/src', p)).href, import.meta.url);
const { CanaryApi, ApiFailure } = await load('api.ts');
const { readFleetDeployment, readFleetGroup } = await load('fleetApi.ts');
const { fleetMonitorRows, managedMonitor } = await load('fleet.ts');
const { fleetEditingRegistration, reviewedFleetHeads } = await load('fleetActions.ts');
const { readReferenceProfiles } = await load('fleetReferences.ts');
const { FleetOverviewView } = await load('views/FleetOverview.tsx');
const { FleetPlacementFields } = await load('views/FleetPlacementFields.tsx');
const { FleetNodeGroupsView } = await load('views/FleetNodeGroups.tsx');
const { FleetReferenceFields } = await load('views/FleetReferenceFields.tsx');
const { FleetGroupTargets } = await load('views/FleetGroupTargets.tsx');
const React = await import('react');
const { renderToStaticMarkup } = await import('react-dom/server');
const h = React.createElement;
const render = (view, props) => renderToStaticMarkup(h(view, props));
const A = { id: 'a'.repeat(32), nodeId: 'node-a', nodeName: 'Office', label: 'Office', local: true, state: 'answering', url: 'https://private-node-address.invalid' };
const B = { ...A, id: 'b'.repeat(32), nodeId: 'node-b', nodeName: 'Cloud', label: 'Cloud', local: false };
const now = 1_000_000;
const monitor = (id, nodeId, revision) => ({ id, revision, name: 'Service', type: 'http', target: 'https://private-monitor-target.invalid', nodeIds: [nodeId], enabled: true, intervalMs: 1000, timeoutMs: 500, updatedAt: 1 });
const source = (node, id, revision, state = 'up') => ({ node, status: 'ready', readAt: now,
  overview: { node: { id: node.nodeId, name: node.label }, user: { name: 'editor', role: 'editor' }, now,
    monitors: [{ monitor: monitor(id, node.nodeId, revision), observations: [{ monitorId: id, nodeId: node.nodeId, state, checkedAt: now, latencyMs: 9, message: state }], state, unknownNodes: 0, configConflict: false }],
    nodes: [{ id: node.nodeId, name: node.label, state: 'local', lastSeenAt: now }], incidents: [], notifications: [] } });
const deployment = () => ({ id: 'd'.repeat(32), revision: 'definition-revision', updatedAt: 1, registrationIds: [A.id, B.id], state: 'applied', monitor: monitor('d'.repeat(32), A.nodeId, 'definition-revision'),
  placements: [{ id: '1'.repeat(32), registrationId: A.id, registrationIds: [A.id], nodeIds: [A.nodeId], monitorId: 'physical-a', revision: 'revision-a', sequence: 1, state: 'applied' },
    { id: '2'.repeat(32), registrationId: B.id, registrationIds: [B.id], nodeIds: [B.nodeId], monitorId: 'physical-b', revision: 'revision-b', sequence: 1, state: 'applied' }] });
let failed = 0;
const token = 'private-made-up-dashboard-token';
async function check(name, fn) { try { await fn(); console.log(`PASS ${name}`); } catch (error) { failed++; console.error(`FAIL ${name}: ${String(error.message).replaceAll(token, '<hidden>')}`); } }

await check('one durable definition joins independent copies without treating their different revisions as a conflict', () => {
  const sources = [source(A, 'physical-a', 'revision-a'), source(B, 'physical-b', 'revision-b', 'down')];
  const [row] = fleetMonitorRows(sources, now, [deployment()]);
  assert.equal(fleetMonitorRows(sources, now, [deployment()]).length, 1);
  assert.equal(row.assessment.tone, 'down'); assert.equal(row.differingViews, false);
  assert.deepEqual(row.results.map(r => [r.nodeId, r.state]), [[A.nodeId, 'up'], [B.nodeId, 'down']]);
});
await check('unrelated equal names and IDs remain separate without explicit deployment lineage', () => {
  assert.equal(fleetMonitorRows([source(A, 'same', 'same'), source(B, 'same', 'same')], now).length, 2);
});
await check('pending placements appear before their nodes create monitors and do not claim healthy completion', () => {
  const d = deployment(); d.state = 'pending'; d.reason = 'Waiting for confirmation'; d.placements = [];
  const [row] = fleetMonitorRows([], now, [d]);
  assert.equal(row.key, `fleet:${d.id}`); assert.equal(row.results.length, 2);
  assert.equal(row.assessment.kind, 'unknown'); assert.equal(row.assessment.detail, d.reason);
});
await check('stale sources cannot supply healthy managed observations and node-local drift is retained', () => {
  const sources = [source(A, 'physical-a', 'revision-a'), source(B, 'physical-b', 'local-edit')];
  let [row] = fleetMonitorRows(sources, now, [deployment()]); assert.equal(row.differingViews, true);
  sources[0].status = 'unavailable'; [row] = fleetMonitorRows(sources, now, [deployment()]);
  assert.equal(row.results.find(r => r.nodeId === A.nodeId).state, 'unknown');
});
await check('management lookup includes the anchor and rejects removed definitions or unrelated monitors', () => {
  const d = deployment(); assert.equal(managedMonitor([d], A.id, 'physical-a'), d);
  assert.equal(managedMonitor([d], B.id, 'unrelated'), undefined);
  d.state = 'removed'; assert.equal(managedMonitor([d], A.id, 'physical-a'), undefined);
});
await check('fleet and group readers bound identities, states, references and duplicate placements', () => {
  const d = readFleetDeployment(deployment()); assert.equal(d.placements.length, 2);
  for (const bad of [{ ...deployment(), state: 'healthy' }, { ...deployment(), placements: [d.placements[0], d.placements[0]] }, { ...deployment(), references: { [A.id]: { notificationIds: ['same', 'same'] } } }]) assert.throws(() => readFleetDeployment(bad));
  assert.throws(() => readFleetGroup({ id: A.id, name: 'Group', registrationIds: [A.id, A.id], revision: 'r', updatedAt: 1 }));
  assert.equal(readFleetGroup({ id: A.id, name: 'Group', registrationIds: [A.id, B.id], revision: 'r', updatedAt: 1 }).registrationIds.length, 2);
});
await check('dashboard CRUD goes to its issuer without a node selector, retaining only same-origin browser cookies', async () => {
  const requests = []; const d = deployment(); const group = { id: A.id, name: 'Sites', registrationIds: [A.id, B.id], revision: 'r', updatedAt: 1 };
  globalThis.fetch = async (url, init) => { requests.push({ url, ...init }); return new Response(JSON.stringify(String(url).includes('node-groups') ? (init.method === 'GET' ? [group] : group) : (init.method === 'GET' ? [d] : d)), { status: 200, headers: { 'Content-Type': 'application/json' } }); };
  const api = new CanaryApi(location.origin, token, { credential: 'session', registeredNode: B.id });
  await api.nodeGroups(); await api.saveNodeGroup({ name: 'Sites', registrationIds: [A.id, B.id] }); await api.saveNodeGroup({ name: 'Sites', registrationIds: [A.id] }, group); await api.removeNodeGroup(group);
  await api.fleetMonitors(); await api.saveFleetMonitor({ requestId: d.id, monitor: d.monitor, registrationIds: d.registrationIds }); await api.retryFleetMonitor(d); await api.removeFleetMonitor(d);
  assert.equal(requests.length, 8);
  for (const request of requests) { assert.equal(new URL(request.url).origin, location.origin); assert.equal(request.credentials, 'same-origin'); assert.equal(request.headers['X-Canary-Registered-Node'], undefined); assert.equal(request.headers.Authorization, `Bearer ${token}`); assert.ok(!String(request.url).includes(token)); }
  assert.equal(JSON.parse(requests[5].body).requestId, d.id); assert.equal(JSON.parse(requests[6].body).revision, d.revision);
});
await check('resource profiles discard channel credentials and never match resources by name', async () => {
  const rows = []; const fake = { withRegisteredNode(id) { return {
    channels: async () => [{ channel: { id: `channel-${id}`, name: 'Shared name', enabled: true, config: { webhook: { url: 'https://secret-provider-address.invalid', password: 'private-provider-setting' } } } }],
    groups: async () => [], tags: async () => [] }; } };
  await readReferenceProfiles(fake, [A, B], new AbortController().signal, row => rows.push(row));
  assert.equal(rows.length, 2); assert.notEqual(rows[0].channels[0].id, rows[1].channels[0].id);
  assert.ok(!JSON.stringify(rows).includes('private-provider-setting')); assert.ok(!JSON.stringify(rows).includes('secret-provider-address'));
});
await check('reference reads are bounded to four nodes and cancellation stops queued work', async () => {
  let active = 0, peak = 0, calls = 0; const nodes = Array.from({ length: 12 }, (_, i) => ({ ...A, id: String(i).padStart(32, '0') }));
  const slow = () => new Promise(resolve => setTimeout(() => resolve([]), 1));
  const fake = { withRegisteredNode() { calls++; active++; peak = Math.max(peak, active); return { channels: slow, groups: slow, tags: slow }; } };
  await readReferenceProfiles(fake, nodes, new AbortController().signal, () => active--);
  assert.ok(peak <= 4 && peak > 1); assert.equal(calls, 12);
  calls = 0; active = 0; const controller = new AbortController();
  await readReferenceProfiles(fake, nodes, controller.signal, () => controller.abort()); assert.ok(calls <= 4);
});
await check('reference authorization failures reject the batch', async () => {
  const fake = { withRegisteredNode() { return { channels: async () => { throw new ApiFailure(401, 'Changed'); }, groups: async () => [], tags: async () => [] }; } };
  await assert.rejects(readReferenceProfiles(fake, [A, B], new AbortController().signal, () => {}), e => e.status === 401);
});
await check('central summary does not render private targets, addresses or raw read errors', () => {
  const sources = [source(A, 'physical-a', 'revision-a'), source(B, 'physical-b', 'revision-b')];
  sources[1].status = 'unavailable'; sources[1].error = new Error('private-raw-read-error');
  const html = render(FleetOverviewView, { sources, rows: fleetMonitorRows(sources, now, [deployment()]), now, loading: false, refreshing: false, search: '', onSearchChange() {}, onRefresh() {}, onOpen() {} });
  assert.match(html, /All nodes/); assert.match(html, /Office/); assert.match(html, /Cloud/);
  for (const secret of ['private-monitor-target', 'private-node-address', 'private-raw-read-error']) assert.ok(!html.includes(secret));
  assert.match(html, /Unknown/);
});
await check('Overview is a health summary and Monitors owns the full searchable inventory', () => {
  const sources = [source(A, 'physical-a', 'revision-a'), source(B, 'physical-b', 'revision-b', 'down')];
  const props = { sources, rows: fleetMonitorRows(sources, now, [deployment()]), now, loading: false, refreshing: false, search: '', onSearchChange() {}, onRefresh() {}, onOpen() {}, onEditDeployment() {} };
  const overview = render(FleetOverviewView, { ...props, view: 'overview' });
  const monitors = render(FleetOverviewView, { ...props, view: 'monitors' });
  assert.match(overview, /Needs attention/); assert.match(overview, /Nodes on this dashboard/);
  assert.ok(!overview.includes('<table')); assert.ok(!overview.includes('Edit placement'));
  assert.match(monitors, /<table/); assert.match(monitors, /Search monitors and nodes/); assert.match(monitors, /Edit placement/);
  assert.ok(!monitors.includes('Nodes on this dashboard')); assert.ok(!monitors.includes('Needs attention'));
  const paused=source(B,'physical-b','revision-b','paused'); paused.overview.monitors[0].monitor.enabled=false;
  const mixed=[sources[0],paused];
  const attention=render(FleetOverviewView,{...props,sources:mixed,rows:fleetMonitorRows(mixed,now,[deployment()])})
    .match(/<ul class="fl-res" aria-label="Nodes where Service[^"]*">([\s\S]*?)<\/ul>/)?.[1];
  assert.ok(attention?.includes('Cloud')&&attention.includes('Paused'),'unknown attention must identify the paused node');
  const pausedA=source(A,'physical-a','revision-a','paused'); pausedA.overview.monitors[0].monitor.enabled=false;
  const allPaused=[pausedA,paused];
  assert.ok(!render(FleetOverviewView,{...props,sources:allPaused,rows:fleetMonitorRows(allPaused,now,[deployment()])}).includes('class="fl-at '));
});
await check('placement checkboxes use the complete registry and keep missing selections', () => {
  const html = render(FleetPlacementFields, { nodes: [A, B], groups: [], selectedRegistrationIds: [A.id, 'missing-registration'], onChange() {} });
  assert.match(html, new RegExp(`value="${A.id}"`)); assert.match(html, new RegExp(`value="${B.id}"`)); assert.match(html, /missing-registration/);
  assert.ok(!html.includes('value="node-a"')); assert.match(html, /not|no longer/u);
});
await check('dashboard group views use registry memberships and omit editor actions for viewers', () => {
  const html = render(FleetNodeGroupsView, { nodes: [A, B], groups: [{ id: A.id, name: 'Every site', registrationIds: [A.id, B.id], revision: 'r', updatedAt: now }], loading: false, editable: false, onRefresh() {}, onSave: async () => {}, onRemove: async () => {} });
  assert.match(html, /Every site/); assert.match(html, /Office/); assert.match(html, /Cloud/);
  assert.ok(!/<button[^>]*>[^<]*New node group/u.test(html));
});
await check('retiring members stay visible and unknown until their own configuration is confirmed', () => {
  const d = deployment(); d.state = 'pending'; d.registrationIds = [A.id];
  d.placements[0].state = 'syncing'; d.placements[0].registrationIds = [A.id];
  d.placements[0].retiringRegistrationIds = [B.id]; d.placements[0].nodeIds = [A.nodeId];
  d.placements = [d.placements[0]];
  const a = source(A, 'physical-a', 'revision-a'), b = source(B, 'physical-a', 'older-revision', 'up');
  const normalized = readFleetDeployment(d);
  assert.deepEqual(normalized.placements[0].retiringRegistrationIds, [B.id]);
  const rows = fleetMonitorRows([a, b], now, [normalized]);
  assert.equal(rows.length, 1); assert.equal(rows[0].results.length, 2);
  const result = rows[0].results.find(r => r.nodeId === B.nodeId);
  assert.equal(result.state, 'unknown'); assert.match(result.message, /Removal.*confirmation/u);
  assert.ok(managedMonitor([normalized], B.id, 'physical-a'));
  assert.throws(() => readFleetDeployment({ ...d, placements: [{ ...d.placements[0], retiringRegistrationIds: [B.id, B.id] }] }));
});
await check('an old healthy result cannot confirm a pending write or a different physical revision', () => {
  for (const state of ['pending', 'syncing', 'applied']) {
    const d = deployment(); d.state = 'pending'; d.placements[0].state = state;
    const row = fleetMonitorRows([source(A, 'physical-a', 'old-physical-revision', 'up'), source(B, 'physical-b', 'revision-b')], now, [d])[0];
    const result = row.results.find(r => r.nodeId === A.nodeId);
    assert.equal(result.state, 'unknown'); assert.match(result.message, /not confirmed/u);
  }
});
await check('reference controls retain stored missing IDs, values and flags without mutating or sending them', () => {
  const base = { notificationIds: ['missing-channel'], groupId: 'missing-group', tags: [{ tagId: 'missing-tag', value: 'preserved tag value', public: false }] };
  const overrides = { [B.id]: { ...base, tags: base.tags.map(tag => Object.freeze({ ...tag })) } };
  Object.freeze(base.tags); Object.freeze(base); Object.freeze(overrides[B.id]); Object.freeze(overrides);
  const before = JSON.stringify({ base, overrides }); let writes = 0;
  const profiles = [{ registrationId: B.id, label: B.label, nodeId: B.nodeId, status: 'ready', channels: [], tags: [], groups: [] }];
  const html = render(FleetReferenceFields, { base, overrides, profiles, onChange() { writes++; }, onRetry() {} });
  for (const expected of ['missing-channel', 'missing-group', 'missing-tag', 'preserved tag value', 'Outage dependency group on this node', 'Remove']) assert.ok(html.includes(expected));
  assert.equal(writes, 0); assert.equal(JSON.stringify({ base, overrides }), before);
});
await check('unread profiles preserve choices, expose Retry and disable their controls during save', () => {
  const profiles = [{ registrationId: A.id, label: A.label, nodeId: A.nodeId, status: 'unavailable', channels: [], tags: [], groups: [] }];
  const html = render(FleetReferenceFields, { profiles, base: {}, overrides: { [A.id]: { notificationIds: [], groupId: '', tags: [] } }, disabled: true, onChange() { assert.fail('render wrote choices'); }, onRetry() {} });
  assert.match(html, /<fieldset[^>]*disabled/u); assert.match(html, /Retry/); assert.match(html, /Choices.*stay as they are/u);
  assert.match(html, /HA peers/u); assert.match(html, /independent nodes/u);
});
await check('reconciliation sends only explicitly reviewed revisions to the dashboard itself', async () => {
  const d = deployment(); let sent;
  globalThis.fetch = async (url, init) => { sent = { url, ...init }; return new Response(JSON.stringify({ ...d, canRemove: true }), { headers: { 'Content-Type': 'application/json' } }); };
  const api = new CanaryApi(location.origin, token, { credential: 'session', registeredNode: B.id });
  const observedRevisions = { [d.placements[0].id]: 'reviewed-node-revision' };
  const result = await api.reconcileFleetMonitor(d, observedRevisions);
  assert.equal(result.canRemove, true); assert.ok(String(sent.url).endsWith(`/${d.id}/reconcile`)); assert.equal(sent.method, 'POST');
  assert.equal(sent.headers['X-Canary-Registered-Node'], undefined);
  assert.deepEqual(JSON.parse(sent.body), { revision: d.revision, observedRevisions });
});
await check('review samples the action clock after a fresh read while still refusing expired, future and conflicted snapshots', () => {
  const originalNow = Date.now;
  const sources = [source(A, 'physical-a', 'revision-a'), source(B, 'physical-b', 'node-local-edit')];
  sources.forEach(s => { s.readAt = now + 500; });
  const d = deployment();
  try {
    assert.equal(reviewedFleetHeads(d, sources, now), null, 'previous timer tick cannot validate a later read');
    Date.now = () => now + 750;
    const reviewed = reviewedFleetHeads(d, sources);
    assert.equal(reviewed.revisions[d.placements[1].id], 'node-local-edit');
    Date.now = () => now + 31_000;
    assert.equal(reviewedFleetHeads(d, sources), null, 'an event cannot reuse a stale render clock');
    Date.now = () => now + 750;
    sources[0].overview.monitors[0].configConflict = true;
    assert.equal(reviewedFleetHeads(d, sources), null);
    sources[0].overview.monitors[0].configConflict = false;
    sources[0].readAt = now + 1000;
    assert.equal(reviewedFleetHeads(d, sources), null, 'genuine future timestamps remain refused');
  } finally { Date.now = originalNow; }
});
await check('edit placement skips removed or missing anchors and uses an existing active destination', () => {
  const d = deployment(), nodes = new Map([[B.id, B]]);
  d.placements[0].state = 'removed'; d.placements[0].registrationIds = [];
  d.registrationIds = [B.id];
  assert.equal(fleetEditingRegistration(d, nodes), B);
  d.placements[1].registrationId = 'missing-anchor';
  assert.equal(fleetEditingRegistration(d, nodes), B, 'an existing HA member can open the editor');
  d.placements[1].state = 'removed';
  assert.equal(fleetEditingRegistration(d, nodes), B, 'desired destinations work before placement preparation');
  d.registrationIds = [];
  assert.equal(fleetEditingRegistration(d, nodes), undefined);
  assert.equal(fleetEditingRegistration(deployment(), new Map([[A.id, A], [B.id, B]])), A);
});
await check('review can include an unsent new destination without inventing its physical revision', () => {
  const d = deployment(), sources = [source(A, 'physical-a', 'node-local-edit'), source(B, 'unrelated', 'unrelated-revision')];
  d.placements[1].sequence = 0; d.placements[1].monitorId = `fleet-${d.placements[1].id}`;
  const reviewed = reviewedFleetHeads(d, sources, now);
  assert.deepEqual(reviewed.revisions, { [d.placements[0].id]: 'node-local-edit' });
  d.placements[1].sequence = 1;
  assert.equal(reviewedFleetHeads(d, sources, now), null, 'a missing previously deployed monitor still needs retirement');
  d.placements[1].sequence = 0; sources[1].status = 'unavailable';
  assert.equal(reviewedFleetHeads(d, sources, now), null, 'an unread destination cannot be assumed empty');
});
await check('persistent group assignments and sync progress survive API decoding', () => {
  const value={...deployment(),groupIds:[A.id],pinnedRegistrationIds:[B.id],syncNotifications:true,notificationSourceRegistrationId:A.id};
  const read=readFleetDeployment(value);
  assert.deepEqual(read.groupIds,[A.id]);assert.deepEqual(read.pinnedRegistrationIds,[B.id]);assert.equal(read.syncNotifications,true);
  for(const changed of [{...value,groupIds:['not-a-group']},{...value,syncNotifications:'yes'},{...value,pinnedRegistrationIds:[B.id,B.id]}])assert.throws(()=>readFleetDeployment(changed));
  const group=readFleetGroup({id:A.id,name:'Sites',revision:'r',updatedAt:1,registrationIds:[A.id],syncState:'pending',syncReason:'Waiting for a node'});
  assert.equal(group.syncState,'pending');assert.equal(group.syncReason,'Waiting for a node');
});
await check('explicit sync uses the dashboard issuer and current group revision', async () => {
  let sent;
  const group={id:A.id,name:'Sites',revision:'saved-group',updatedAt:1,registrationIds:[A.id,B.id]};
  globalThis.fetch=async(url,init)=>{sent={url,...init};return new Response(JSON.stringify({...group,revision:'queued-group',syncState:'pending'}),{status:202,headers:{'Content-Type':'application/json'}})};
  const api=new CanaryApi(location.origin,token,{credential:'session',registeredNode:B.id});
  const result=await api.syncNodeGroup(group);
  assert.equal(new URL(sent.url).pathname,`/api/dashboard/node-groups/${group.id}/sync`);
  assert.equal(sent.headers['X-Canary-Registered-Node'],undefined);assert.deepEqual(JSON.parse(sent.body),{revision:group.revision});
  assert.equal(result.revision,'queued-group');assert.equal(result.syncState,'pending');
});
await check('node groups have their own section and individual node fields have no group shortcuts', () => {
  const group={id:A.id,name:'Sites',revision:'r',updatedAt:1,registrationIds:[A.id,B.id]};
  const html=render(FleetGroupTargets,{groups:[group],nodes:[A,B],selected:[group.id],active:true,outside:[],onChange(){},onChooseNodes(){}});
  assert.match(html,/Node groups/);assert.match(html,/Sites/);assert.match(html,new RegExp(`value="${group.id}"`));assert.match(html,/checked/);
  const nodes=render(FleetPlacementFields,{nodes:[A,B],selectedRegistrationIds:[A.id],onChange(){}});
  assert.ok(!nodes.includes('Add a node group'));assert.ok(!nodes.includes('fl-gpick'));
});
scope.unregister();
console.log(`${25 - failed}/25 fleet management groups passed`);
if (failed) process.exitCode = 1;
