// Pure regressions for the dashboard control plane in the web client (apps/web/src/gateway.ts `controlPlaneMode()`,
// api.ts, session.ts, dashboard.ts, views/Registry.tsx). The contract it pins, from docs/dashboard-control-plane.md:
// sign-in, account and node-list routes always go to the dashboard itself and never carry a node selection; monitoring
// routes carry `X-Canary-Registered-Node` only while a node is selected, with the same dashboard credential; selecting
// a node signs nothing out and drops the previous selection's polls; the empty dashboard overview carries no monitoring
// data; registry and verification answers keep only their documented fields; enrollment credentials and tickets go
// nowhere but the dashboard's own routes; public pages name a registration, never a node address. The shared node list
// renders read-only for viewers (no addresses, no Add, Retry or Remove) and keeps the bundled local node unremovable.
// The dashboard server's page marker (`<html data-canary-dashboard="control-plane">`) turns the control plane on by
// itself, so a missing or false runtime flag can't make its page fall back to direct mode; without the exact marker,
// only the explicit flag does.
// Uses exported functions with a stand-in fetch and stand-in `location`/`document`, renders RegistryView (with a
// stand-in context) and the signed-out App to static markup (effects never run; useSyncExternalStore reads the client
// snapshot, as in node-login-copy-pure.mjs), and reads sources as text: no network, browser, build, copy, scratch or
// output files. Node's fetch keeps no cookies, so this pins the options handed to the browser, not the browser's cookie
// rule or the dashboard server's own checks. Direct and gateway regressions stay in their fixtures.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/dashboard-control-plane-pure.mjs   (exit 1 on any failure)
import { existsSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const web = resolve(root, 'apps/web');
const PAGE = 'https://uptime.example.test';
Object.defineProperty(globalThis, 'location', { value: { origin: PAGE, protocol: 'https:', hash: '', pathname: '/' }, configurable: true, writable: true });
globalThis.document ??= { addEventListener() {}, hidden: false };
const pageDocument = globalThis.document;
const storageWrites = [];
Object.defineProperty(globalThis, 'localStorage', { value: { getItem: () => null, setItem: (...args) => storageWrites.push(args), removeItem: (...args) => storageWrites.push(args) }, configurable: true, writable: true });
const require = createRequire(import.meta.url);
const React = require('react');
// useSession and the theme have no server snapshot; wrapped before anything else imports React, as in
// node-login-copy-pure.mjs, so the signed-out App renders with the client snapshot. Checked before any rendering.
const clientStore = React.useSyncExternalStore;
const serverSafeStore = (subscribe, getSnapshot, getServerSnapshot) => clientStore(subscribe, getSnapshot, getServerSnapshot ?? getSnapshot);
React.useSyncExternalStore = serverSafeStore;
const reactModule = await import('react');
const storeWrapped = (reactModule.useSyncExternalStore ?? reactModule.default?.useSyncExternalStore) === serverSafeStore;
const { renderToStaticMarkup } = await import('react-dom/server');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'dashboard-control-plane-pure', tsconfig: resolve(web, 'tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(web, path)).href, import.meta.url);
const gateway = await load('src/gateway.ts');
const api = await load('src/api.ts');
const sessionModule = await load('src/session.ts');
const dashboard = await load('src/dashboard.ts');
const router = await load('src/router.ts');
const badges = await load('src/badges.ts');
const { CanaryContext } = await load('src/context.tsx');
const { RegistryView } = await load('src/views/Registry.tsx');
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const HEADER = 'x-canary-registered-node';
const TOKEN = 'control-plane-review-dashboard-token';
const NODE_CREDENTIAL = 'control-plane-review-enrollment-credential';
const REG = '0f3c2b1a9d8e7f60514233a4b5c6d7e8';
const TICKET = 'Q2FuYXJ5LXJldmlldy10aWNrZXQtMDEyMzQ1Njc4OQ';
const ACCOUNT = { id: 'acct-1', name: 'review', role: 'admin', passwordEnabled: true };
// The dashboard's own overview, as dashboard_http.go writes it without a selection.
const EMPTY = { node: { id: 'dashboard', name: 'Dashboard' }, now: 1_000_000, monitors: [], nodes: [], groups: [], maintenance: [], incidents: [], notifications: [], user: { name: 'review', role: 'admin' } };
const NODE_OVERVIEW = { node: { id: 'canary-b', name: 'Canary B' }, now: 2_000_000, user: { name: 'review', role: 'admin' }, nodes: [], incidents: [], notifications: [],
  monitors: [{ monitor: { id: 'm1', name: 'API', nodeIds: ['canary-b'] }, observations: [] }] };
const ENTRY = { id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', url: 'http://100.79.12.4:3100', local: false, state: 'answering', lastReadAt: 900_000, monitorCount: 3 };
const LOCAL = { id: 'a'.repeat(32), label: 'Local node', nodeId: 'canary-local', nodeName: 'Local node', url: 'http://node:3100', local: true, state: 'not-answering', error: 'Cannot reach this node from the dashboard server' };

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const server = (url, init) => {
  const { pathname } = new URL(url);
  const selected = new Headers(init.headers).get(HEADER);
  if (pathname === '/api/overview') return json(200, selected ? NODE_OVERVIEW : EMPTY);
  if (pathname === '/api/auth/me') return json(200, ACCOUNT);
  if (pathname === '/api/accounts' && init.method === 'GET') return json(200, [ACCOUNT]);
  if ((pathname === '/api/auth/password' || pathname.startsWith('/api/accounts')) && init.method !== 'DELETE') return json(200, ACCOUNT);
  if (pathname === '/api/auth/status') return json(200, { dashboard: true, setupRequired: true, passwordConfigured: false, passwordLogin: true, tokenLogin: false, mfaSupported: true });
  if (pathname === '/api/dashboard/setup') return json(201, { id: 'owner', name: 'owner', role: 'admin' });
  if (pathname === '/api/dashboard/nodes' && init.method === 'GET') return json(200, [ENTRY, LOCAL]);
  if (pathname === '/api/dashboard/verify') return json(200, { ticket: TICKET, expiresAt: 1_120_000, node: { id: 'canary-c', name: 'Canary C' }, label: 'Office', url: 'http://10.0.8.20:3100', plainHttp: true, token: 'never-kept' });
  if (pathname === '/api/dashboard/nodes') return json(201, { ...ENTRY, id: 'c'.repeat(32), state: 'starting', credentials: 'never-kept' });
  if (/\/(check|retry)$/u.test(pathname)) return json(200, ENTRY);
  if (init.method === 'DELETE' || pathname === '/api/auth/logout' || pathname === '/api/auth/logout-all') return new Response(null, { status: 204 });
  if (pathname.startsWith('/public/status/')) return json(200, { slug: 'services', title: 'Services', groups: [] });
  return json(200, []);
};
let answer = server;
let calls = [];
globalThis.fetch = async (url, init = {}) => { calls.push({ url: String(url), init }); return answer(String(url), init); };
const record = async run => { calls = []; answer = server; await run(); return calls; };
const header = (call, name) => new Headers(call.init.headers).get(name);
const rejection = async promise => { try { await promise; } catch (error) { return error; } assert.fail('expected a failure'); };
const on = () => { globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; };
// Off is the static-hosting page: neither flag and no server marker, so a failed marker check can't leak onward.
const off = () => { delete globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; mark(undefined); };
const pathOf = call => new URL(call.url).pathname;
const MARKER = 'data-canary-dashboard';
/** The page's `<html>` with `value` as the dashboard server's marker (`null`: no attribute); `undefined` drops the element. */
const mark = value => { if (value === undefined) delete pageDocument.documentElement; else pageDocument.documentElement = { getAttribute: name => (name === MARKER ? value : null) }; };
/** Runs `run` with `globalThis.document` defined by `descriptor`, then puts the stand-in document back. */
const withDocument = (descriptor, run) => {
  Object.defineProperty(globalThis, 'document', { ...descriptor, configurable: true });
  try { run(); } finally { Object.defineProperty(globalThis, 'document', { value: pageDocument, configurable: true, writable: true }); }
};

await check('mode: only an explicit true or the server’s exact page marker turns the control plane on, and it implies gateway mode', () => {
  off();
  for (const value of [undefined, false, 'true', 1, null, {}]) {
    if (value === undefined) delete globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__; else globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = value;
    assert.equal(gateway.controlPlaneMode(), false, JSON.stringify(value));
    assert.equal(gateway.gatewayMode(), false, 'no flag, no gateway');
  }
  globalThis.__CANARY_DASHBOARD_GATEWAY__ = true;
  assert.equal(gateway.controlPlaneMode(), false, 'the gateway flag alone is not the control plane');
  on();
  assert.equal(gateway.controlPlaneMode(), true);
  assert.equal(gateway.gatewayMode(), true, 'a control plane never contacts another origin, even without the gateway flag');
  const source = readFileSync(resolve(web, 'public/canary-config.js'), 'utf8');
  assert.doesNotMatch(source, /__CANARY_DASHBOARD_CONTROL_PLANE__\s*=\s*true/u, 'static hosting keeps the control plane off');
  // The dashboard server's page marker counts only as this exact attribute value.
  off();
  for (const value of [null, '', 'gateway', 'true', 'Control-Plane', ' control-plane', 'control-plane ', 'control-plane control-plane']) {
    mark(value);
    assert.equal(gateway.controlPlaneMode(), false, `marker ${JSON.stringify(value)}`);
    assert.equal(gateway.gatewayMode(), false, `marker ${JSON.stringify(value)} without a flag is still direct mode`);
  }
  on();
  assert.equal(gateway.controlPlaneMode(), true, 'without the exact marker the explicit flag still turns it on');
  assert.equal(gateway.gatewayMode(), true);
  for (const value of [undefined, false, 'true', 0, null]) {
    off();
    mark('control-plane');
    if (value !== undefined) globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = value;
    globalThis.__CANARY_DASHBOARD_GATEWAY__ = false;
    assert.equal(gateway.controlPlaneMode(), true, `flag ${JSON.stringify(value)} can’t turn the marker off`);
    assert.equal(gateway.gatewayMode(), true, `flag ${JSON.stringify(value)}: the marker implies gateway mode`);
  }
  // Without a readable document (server rendering, fixtures, a broken page) the flags alone decide, as before.
  const documents = [
    ['no document', { value: undefined }], ['null document', { value: null }], ['no element', { value: {} }],
    ['null element', { value: { documentElement: null } }], ['no getAttribute', { value: { documentElement: {} } }],
    ['getAttribute not a function', { value: { documentElement: { getAttribute: 'control-plane' } } }],
    ['throwing document', { get() { throw new Error('no document here'); } }],
    ['throwing element', { value: { get documentElement() { throw new Error('no element here'); } } }],
    ['throwing getAttribute', { value: { documentElement: { getAttribute() { throw new Error('no attributes here'); } } } }],
  ];
  for (const [label, descriptor] of documents) withDocument(descriptor, () => {
    off();
    assert.equal(gateway.controlPlaneMode(), false, `${label}, no flag`);
    assert.equal(gateway.gatewayMode(), false, `${label}, no flag: direct mode`);
    on();
    assert.equal(gateway.controlPlaneMode(), true, `${label}, explicit flag`);
    assert.equal(gateway.gatewayMode(), true, `${label}, explicit flag: gateway mode`);
  });
  off();
});

await check('dashboard routes: sign-in, accounts and the node list never carry a selection, even with one', async () => {
  on();
  const selected = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
  const sent = await record(async () => {
    await selected.me();
    await selected.setPassword('a-new-review-password');
    await selected.accounts();
    await selected.createAccount({ name: 'viewer', role: 'viewer', password: 'a-viewer-review-password' });
    await selected.updateAccount('acct-2', { role: 'editor' });
    await selected.resetAccountPassword('acct-2', 'another-review-password', false);
    await selected.deleteAccount('acct-2');
    await selected.logoutAll();
    await selected.logout();
    await selected.registeredNodes();
    await selected.checkNode(REG);
    await selected.retryNode(REG);
  });
  assert.equal(sent.length, 12);
  for (const call of sent) {
    assert.equal(header(call, HEADER), null, `${call.init.method} ${call.url} named a node`);
    assert.equal(new URL(call.url).origin, PAGE);
    assert.equal(header(call, 'authorization'), `Bearer ${TOKEN}`);
    assert.equal(call.init.credentials, 'same-origin');
    assert.equal(call.init.redirect, 'error');
    assert.equal(call.init.referrerPolicy, 'no-referrer');
    assert.equal(call.init.cache, 'no-store');
  }
});

await check('monitoring routes: the selection rides in one header with the same dashboard credential, and only then', async () => {
  on();
  const plain = new api.CanaryApi(PAGE, TOKEN, { credential: 'session', expiresAt: 5_000_000 });
  const selected = plain.withRegisteredNode(REG);
  assert.equal(selected.credential, 'session');
  assert.equal(selected.expiresAt, 5_000_000);
  assert.equal(selected.registeredNode, REG);
  assert.ok(selected.sameCredential(plain) && plain.sameCredential(selected), 'a selection is the same sign-in, so it is never signed out');
  const sent = await record(async () => {
    await selected.overview();
    await selected.monitors();
    await selected.createMonitor({ name: 'API', type: 'http', target: 'https://service.example.test' });
    await selected.history('m1', 10);
    await selected.channels();
    await selected.tags();
    await selected.statusPages();
    await selected.groups();
  });
  assert.equal(sent.length, 8);
  for (const call of sent) {
    assert.equal(header(call, HEADER), REG, call.url);
    assert.equal(header(call, 'authorization'), `Bearer ${TOKEN}`, 'the dashboard credential, never a node one');
    assert.ok(!call.url.includes(REG), 'the selection is a header, not part of the address');
  }
  const back = selected.withRegisteredNode(null);
  const none = await record(async () => { await back.overview(); });
  assert.equal(header(none[0], HEADER), null);
});

await check('selection checks: odd registration IDs never leave the page, and direct mode can’t select one', async () => {
  on();
  for (const bad of ['', 'two words', 'a/b', '../x', 'x'.repeat(129), 'r\nX-Injected: 1']) {
    assert.throws(() => new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(bad), /registration/u, JSON.stringify(bad));
  }
  const sent = await record(async () => { await rejection(new api.CanaryApi(PAGE, TOKEN).checkNode('a/b')); });
  assert.equal(sent.length, 0);
  off();
  assert.throws(() => new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG), /node registry/u);
  assert.equal(new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(null).registeredNode, null);
});

await check('empty dashboard overview: login and accounts work with no node, and it never reads as a node’s data', async () => {
  on();
  const overview = await new api.CanaryApi(PAGE, TOKEN).overview();
  assert.deepEqual(overview.node, { id: 'dashboard', name: 'This dashboard' });
  assert.equal(overview.now, EMPTY.now);
  assert.deepEqual(overview.user, { name: 'review', role: 'admin' });
  for (const key of ['monitors', 'nodes', 'incidents', 'notifications']) assert.deepEqual(overview[key], []);
  const ignored = api.normalizeDashboardOverview({ ...EMPTY, monitors: NODE_OVERVIEW.monitors, nodes: [{ id: 'x' }] });
  assert.deepEqual(ignored.monitors, [], 'lists in the dashboard’s own overview are not shown as a node’s');
  assert.throws(() => api.normalizeDashboardOverview({ now: 1, user: { role: 'owner' } }));
  const selected = await new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG).overview();
  assert.equal(selected.node.id, 'canary-b', 'a selected node’s overview is read strictly, as before');
  assert.equal(selected.monitors.length, 1);
});

await check('registry and verification answers keep only documented fields; no credential or ticket is stored', async () => {
  on();
  storageWrites.length = 0;
  const client = new api.CanaryApi(PAGE, TOKEN);
  const list = await client.registeredNodes();
  assert.deepEqual(list.map(node => node.id), [REG, LOCAL.id]);
  assert.deepEqual(Object.keys(list[0]).sort(), ['id', 'label', 'lastReadAt', 'local', 'monitorCount', 'nodeId', 'nodeName', 'state', 'url']);
  assert.equal(list[1].error, LOCAL.error);
  assert.equal(api.normalizeRegisteredNode({ ...ENTRY, url: undefined, credentials: 'x', token: 'y' }).url, undefined, 'viewers get no address');
  assert.ok(!('credentials' in api.normalizeRegisteredNode({ ...ENTRY, credentials: 'x' })));
  assert.throws(() => api.normalizeRegisteredNode({ ...ENTRY, state: 'online' }), /could not read/u);
  answer = () => json(200, null);
  assert.deepEqual(await client.registeredNodes(), [], 'Go’s empty list');
  answer = server;
  const sent = await record(async () => {
    const verification = await client.verifyNode({ label: '  Office ', url: 'http://10.0.8.20:3100/', token: NODE_CREDENTIAL });
    assert.deepEqual(Object.keys(verification).sort(), ['expiresAt', 'label', 'node', 'plainHttp', 'ticket', 'url']);
    assert.equal(verification.ticket, TICKET);
    assert.equal(verification.plainHttp, true);
    await client.enrollNode(verification.ticket);
    await client.discardVerification(verification.ticket);
  });
  assert.deepEqual(sent.map(call => `${call.init.method} ${pathOf(call)}`), ['POST /api/dashboard/verify', 'POST /api/dashboard/nodes', `DELETE /api/dashboard/verify/${TICKET}`]);
  assert.deepEqual(JSON.parse(sent[0].init.body), { label: 'Office', url: 'http://10.0.8.20:3100', token: NODE_CREDENTIAL }, 'exactly the documented body, URL as an origin');
  assert.deepEqual(JSON.parse(sent[1].init.body), { ticket: TICKET });
  for (const call of sent) assert.equal(header(call, HEADER), null);
  assert.ok(sent.slice(1).every(call => !String(call.init.body ?? '').includes(NODE_CREDENTIAL)), 'the credential goes only to verification');
  assert.equal(api.normalizeVerification({ ticket: TICKET, expiresAt: 1, node: { id: 'n' }, label: 'L', url: 'http://n:3100', plainHttp: false }).plainHttp, true, 'plain HTTP is never reported as encrypted');
  assert.deepEqual(storageWrites, []);
});

await check('removal and retry: 204 removed, 202 still removing, and a finished retried removal reads as gone', async () => {
  on();
  const client = new api.CanaryApi(PAGE, TOKEN);
  answer = () => new Response(null, { status: 204 });
  assert.deepEqual(await client.removeNode(REG), { removed: true });
  assert.equal(await client.retryNode(REG), null);
  answer = () => json(202, { ...ENTRY, state: 'removing' });
  const pending = await client.removeNode(REG);
  assert.equal(pending.removed, false);
  assert.equal(pending.node.state, 'removing');
  answer = () => new Response('', { status: 202 });
  assert.deepEqual(await client.removeNode(REG), { removed: false, node: null }, 'an accepted removal without a body is still accepted');
  answer = () => json(202, { ...ENTRY, state: 'enrolling' });
  assert.equal((await client.enrollNode(TICKET)).state, 'enrolling', 'an interrupted enrollment is listed, not reported as added');
  answer = () => json(409, { error: 'The bundled local node is managed in deployment settings' });
  const local = await rejection(client.removeNode(LOCAL.id));
  assert.equal(api.describeFailure(local, { preferDetail: true }), 'The bundled local node is managed in deployment settings.');
});

await check('failure wording: the dashboard’s answers are its own; a refused node credential is a node error, never a sign-out', async () => {
  on();
  const client = new api.CanaryApi(PAGE, TOKEN);
  answer = () => json(401, { error: 'Authentication required' });
  const central = await rejection(client.me());
  assert.equal(central.message, 'This dashboard did not accept the sign-in.');
  assert.equal(api.describeFailure(central, { preferDetail: true }), 'Your sign-in to this dashboard is no longer valid. Sign in again.');
  answer = () => json(502, { error: 'Node credential was refused' });
  const refused = await rejection(client.withRegisteredNode(REG).overview());
  assert.equal(refused.status, 502);
  assert.equal(api.describeFailure(refused), 'The dashboard’s server couldn’t get a usable answer from this node. Node credential was refused.');
  answer = () => json(404, { error: 'Registered node not found' });
  assert.equal((await rejection(client.withRegisteredNode(REG).monitors())).status, 404);
  answer = () => json(503, { error: 'Dashboard accounts unavailable' });
  assert.equal((await rejection(client.accounts())).message, 'This dashboard can’t handle requests right now.');
  answer = () => { throw new TypeError('Failed to fetch'); };
  assert.match((await rejection(client.me())).message, /^Cannot reach this dashboard’s server\./u);
  answer = () => new Response('<!doctype html><title>Sign in</title>', { status: 200, headers: { 'Content-Type': 'text/html' } });
  assert.match((await rejection(client.registeredNodes())).message, /^This dashboard’s server sent a response this page could not read\. .*sign-in proxy/u);
});

await check('first owner: status and setup go to the dashboard without a bearer, and need no node', async () => {
  on();
  const sent = await record(async () => {
    const status = await api.fetchDashboardStatus();
    assert.equal(status.dashboard, true);
    assert.equal(status.setupRequired, true);
    assert.equal(status.tokenLogin, false);
    const owner = await api.createDashboardOwner({ name: '  owner ', password: 'a-long-owner-password', code: ' setup-code-value \n' });
    assert.deepEqual(owner, { id: 'owner', name: 'owner', role: 'admin' });
  });
  assert.deepEqual(sent.map(call => `${call.init.method ?? 'GET'} ${pathOf(call)}`), ['GET /api/auth/status', 'POST /api/dashboard/setup']);
  assert.ok(sent.every(call => header(call, 'authorization') === null && header(call, HEADER) === null && new URL(call.url).origin === PAGE));
  assert.deepEqual(JSON.parse(sent[1].init.body), { name: 'owner', password: 'a-long-owner-password', code: 'setup-code-value' });
  answer = () => json(404, { error: 'Not found' });
  assert.ok((await rejection(api.fetchDashboardStatus())) instanceof api.ApiFailure, 'a missing status is reported, never read as token-only');
  const saved = globalThis.location;
  Object.defineProperty(globalThis, 'location', { value: undefined, configurable: true, writable: true });
  const none = await record(async () => { assert.ok((await rejection(api.fetchDashboardStatus())) instanceof api.OriginRefused); });
  Object.defineProperty(globalThis, 'location', { value: saved, configurable: true, writable: true });
  assert.equal(none.length, 0, 'no page origin, no request');
});

await check('public pages: a registration selector through the dashboard, without a management credential', async () => {
  on();
  const sent = await record(async () => { await api.fetchPublicStatus(PAGE, 'services', undefined, REG); });
  assert.equal(sent[0].url, `${PAGE}/public/status/services?registration=${REG}`);
  assert.equal(header(sent[0], 'authorization'), null);
  assert.equal(header(sent[0], HEADER), null);
  assert.throws(() => api.fetchPublicStatus(PAGE, 'services', undefined, 'a b'));
  assert.equal(router.publicHref('services', [], REG), `#/status/services?registration=${REG}`);
  assert.deepEqual(router.parseRoute(`#/status/services?registration=${REG}`), { name: 'public-status', slug: 'services', nodes: [], registration: REG });
  assert.deepEqual(router.parseRoute('#/status/services?node=https%3A%2F%2Fa.example'), { name: 'public-status', slug: 'services', nodes: ['https://a.example'] }, 'direct links keep their shape');
  const image = new URL(badges.badgeUrl(PAGE, 'services', { group: 'web', monitor: 'API' }, 'uptime', REG));
  assert.deepEqual([...image.searchParams.keys()], ['group', 'monitor', 'metric', 'registration'], 'the selector goes last, after the node’s own parameters');
  assert.equal(badges.badgeUrl(PAGE, 'services'), `${PAGE}/public/badge/services.svg`, 'without a registration the address is unchanged');
});

await check('session: selecting a node keeps the sign-in, drops the old poll and waits for the node’s first answer', async () => {
  on();
  const { session } = sessionModule;
  const settle = () => new Promise(done => setTimeout(done, 5));
  calls = []; answer = server;
  await session.connect(new api.CanaryApi(PAGE, TOKEN));
  const signedIn = session.getSnapshot().connection;
  assert.equal(signedIn.selection, null);
  assert.equal(signedIn.awaiting, false);
  assert.equal(typeof signedIn.issuerSkew, 'number');
  calls = [];
  assert.equal(session.select({ id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', local: false }, signedIn), true);
  const waiting = session.getSnapshot().connection;
  assert.notEqual(waiting.identity, signedIn.identity, 'a new identity drops every late answer for the old selection');
  assert.equal(session.isCurrent(signedIn), false);
  assert.equal(waiting.awaiting, true);
  assert.deepEqual(waiting.overview.monitors, [], 'nothing is shown as the node’s data before it answers');
  assert.ok(waiting.api.sameCredential(signedIn.api));
  assert.equal(waiting.account, signedIn.account, 'the dashboard account stays');
  await settle();
  const answered = session.getSnapshot().connection;
  assert.equal(answered.awaiting, false);
  assert.equal(answered.overview.node.id, 'canary-b');
  assert.ok(calls.length >= 1 && calls.every(call => pathOf(call) === '/api/overview' && header(call, HEADER) === REG), calls.map(call => call.url).join(', '));
  assert.ok(!calls.some(call => pathOf(call).startsWith('/api/auth/')), 'selecting never signs out or in');
  assert.equal(session.select(null, signedIn), false, 'a stale caller can’t change the selection');
  assert.equal(session.select({ id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', local: false }, answered), false, 'same node, nothing to do');
  assert.equal(session.select(null, answered), true);
  assert.equal(session.getSnapshot().connection.api.registeredNode, null);
  await settle();
  assert.equal(await session.signOut(), 'forgotten');
});

const ctx = (role, list) => ({
  admin: role === 'admin', role, controlPlane: true, selection: null, select() {}, stillCurrent: () => true, nodes: [], endpoint: 'dashboard',
  overview: { ...EMPTY, node: { id: 'dashboard', name: 'This dashboard' } }, connection: { awaiting: false, issuerSkew: 0, link: 'live' }, api: {},
  registry: { list, map: new Map(list.map(node => [node.id, node])), loading: false, error: undefined, unsupported: false, reload() {} },
});
const text = markup => markup.replace(/<[^>]*>/g, ' ').replace(/&#x27;/g, '\'').replace(/&amp;/g, '&').replace(/\s+/g, ' ');
const render = (role, list, props = {}) => renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx(role, list) }, React.createElement(RegistryView, props)));

await check('node list: administrators manage it; the bundled node is never removable; viewers and editors only read', () => {
  on();
  const list = [ENTRY, LOCAL, { ...ENTRY, id: 'd'.repeat(32), label: 'Lab', nodeId: 'canary-lab', state: 'enrolling' }];
  const admin = text(render('admin', list));
  assert.match(admin, /Add node/u);
  assert.ok(admin.includes(ENTRY.url) && admin.includes(LOCAL.url), 'administrators see addresses');
  assert.equal((admin.match(/Remove/gu) ?? []).length, 2, 'Remove on the two added nodes, not on the local one');
  assert.match(admin, /Retry enrollment/u);
  assert.match(admin, /can’t be removed here/u);
  for (const role of ['viewer', 'editor']) {
    const read = text(render(role, list.map(({ url: _address, ...node }) => node)));
    assert.match(read, /Administrators add and remove nodes/u);
    for (const action of [/Add node/u, /Remove/u, /Retry/u]) assert.doesNotMatch(read, action, `${role}: ${action}`);
    assert.match(read, /Check now/u, 'any role rechecks');
    assert.match(read, /Node addresses are shown to administrators only/u);
    assert.ok(!read.includes('100.79.12.4'), 'no address for non-administrators');
  }
  const empty = text(render('viewer', []));
  assert.match(empty, /No nodes yet/u);
  assert.match(empty, /An administrator hasn’t added any nodes yet/u);
  assert.doesNotMatch(empty, /Add node/u);
  assert.match(text(render('admin', [], { home: true })), /Overview .*No nodes yet.*Add node/u);
  assert.match(text(render('admin', [{ ...LOCAL, nodeId: 'pending', state: 'enrolling' }])), /node ID not read yet.*Being added/u, 'the bundled node’s placeholder ID is not shown as an ID');
});

await check('form rules: node addresses are origins other than the dashboard; credentials and labels match the server', () => {
  on();
  assert.equal(dashboard.nodeAddressProblem('http://100.79.12.4:3100'), undefined);
  for (const bad of ['', 'node:3100', 'http://node:3100/api', 'http://user:pass@node:3100', 'ftp://node', PAGE]) assert.ok(dashboard.nodeAddressProblem(bad), bad);
  assert.equal(dashboard.credentialProblem(NODE_CREDENTIAL), undefined);
  for (const bad of ['', 'short', 'has a space in it and is long enough', 'a'.repeat(30)]) assert.ok(dashboard.credentialProblem(bad), bad);
  assert.equal(dashboard.labelProblem('Rack B'), undefined);
  assert.ok(dashboard.labelProblem(' '));
  assert.ok(dashboard.labelProblem('é'.repeat(65)), '128 bytes, as the server counts');
  assert.deepEqual(['answering', 'starting', 'not-answering'].map(state => dashboard.nodeStateInfo({ ...ENTRY, state }).readable), [true, true, true]);
  assert.deepEqual(['credential-refused', 'different-node', 'enrolling', 'removing'].map(state => dashboard.nodeStateInfo({ ...ENTRY, state }).readable), [false, false, false, false]);
});

await check('source: enrollment credentials, tickets and node selections stay outside the tab sign-in persistence', () => {
  for (const path of ['src/views/Registry.tsx', 'src/dashboard.ts', 'src/session.ts', 'src/api.ts']) {
    assert.doesNotMatch(readFileSync(resolve(web, path), 'utf8'), /localStorage|sessionStorage|indexedDB|document\.cookie/u, path);
  }
  const registry = readFileSync(resolve(web, 'src/views/Registry.tsx'), 'utf8');
  assert.match(registry, /setToken\(''\)/u, 'the enrollment credential is cleared after verification');
  assert.match(registry, /discardVerification/u, 'unused tickets are discarded');
  assert.doesNotMatch(registry, /navigate\([^)]*(ticket|token)/u);
});

await check('server marker: with the flag missing or false, the signed-out page is the dashboard’s own and no node address is contacted', async () => {
  assert.ok(storeWrapped, 'React was imported before the server-snapshot wrap, so App can’t be rendered');
  const serverSource = resolve(root, 'apps/node-go/internal/canary/dashboard_server.go');
  if (existsSync(serverSource)) assert.ok(readFileSync(serverSource, 'utf8').includes(`<html ${MARKER}="control-plane"`), 'the marker the dashboard server writes');
  const { App } = await load('src/App.tsx');
  const page = () => text(renderToStaticMarkup(React.createElement(App)));
  const PRIVATE = 'http://10.0.8.20:3100';
  assert.equal(sessionModule.session.getSnapshot().connection, null, 'signed out');
  for (const value of [undefined, false]) {
    off();
    mark('control-plane');
    if (value === false) { globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = false; globalThis.__CANARY_DASHBOARD_GATEWAY__ = false; }
    const flag = `flag ${value}`;
    let screen = '';
    const rendering = await record(async () => { screen = page(); });
    assert.equal(rendering.length, 0, `${flag}: rendering made a request`);
    assert.match(screen, /Checking this dashboard…/u, `${flag}: the dashboard’s own sign-in screen`);
    assert.match(screen, /Accounts live on this dashboard/u, flag);
    assert.doesNotMatch(screen, /Node API address|Sign in to a node/u, `${flag}: not the direct node sign-in`);
    const refused = await record(async () => {
      for (const attempt of [
        () => api.fetchAuthStatus(PRIVATE),
        () => api.passwordSignIn(PRIVATE, { name: 'review', password: 'a-long-review-password' }),
        () => new api.CanaryApi(PRIVATE, TOKEN).overview(),
        () => api.fetchPublicStatus(PRIVATE, 'services'),
      ]) {
        const error = await rejection(attempt());
        assert.ok(error instanceof api.OriginRefused, `${flag}: ${error?.message}`);
        assert.ok(error.message.includes(`its nodes only through its own server at ${PAGE}`) && error.message.endsWith(`contact ${PRIVATE}.`), error.message);
      }
    });
    assert.equal(refused.length, 0, `${flag}: a request left for the private address`);
    const onboarding = await record(async () => {
      assert.equal((await api.fetchDashboardStatus()).setupRequired, true);
      await api.createDashboardOwner({ name: 'owner', password: 'a-long-owner-password', code: 'setup-code-value' });
    });
    assert.deepEqual(onboarding.map(call => `${call.init.method ?? 'GET'} ${call.url}`), [`GET ${PAGE}/api/auth/status`, `POST ${PAGE}/api/dashboard/setup`], `${flag}: onboarding asks the dashboard itself`);
  }
  off();
  mark('gateway');
  const direct = page();
  assert.match(direct, /Node API address/u, 'a wrong marker without the flag keeps the direct sign-in');
  assert.doesNotMatch(direct, /Checking this dashboard/u);
  off();
  on();
  assert.match(page(), /Checking this dashboard…/u, 'without a marker the explicit flag still shows the dashboard’s sign-in');
  off();
});

off();
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
