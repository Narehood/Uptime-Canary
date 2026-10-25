// Pure regressions for registry maintenance on the dashboard control plane: editing a registered node (rename, or
// following the same node to a new address) and rotating the dashboard's role credentials on it. Covers the web client
// in apps/web/src/api.ts (editNode, rotateNode, the `rotating` state), dashboard.ts (actions, edit and recovery rules,
// failure wording, selectedOperation), session.ts (refreshSelection), views/Registry.tsx (cards, EditNodeDialog,
// RotateNodeDialog) and App.tsx (selection follows the registry; the selected node's banner while its access changes).
// The contract is root's Go server (apps/node-go/internal/canary/dashboard_registry_edit.go, dashboard_http.go,
// dashboard_poll.go, dashboard_registry.go): PATCH /api/dashboard/nodes/:id {label, url?} and POST :id/rotate {} or
// {token} are administrator registry routes of the dashboard itself, never carrying X-Canary-Registered-Node; a rename
// works offline; a move is saved only after the same node ID accepts all three existing role credentials there; the
// bundled node's address belongs to the deployment; pending or concurrent changes answer 409; a rotation answers 200
// with the new row, or 202 `rotating` when the node's answer is unknown, and POST :id/retry resumes it. Every refusal
// comes before the server saves anything. The optional recovery credential goes only into the rotate body.
// Recover access (completion r7, root's retryRegisteredNode/replaceRotationRecovery): POST :id/retry also takes an
// optional {token} for a `rotating` entry. The server checks it on the same node, and that the node's three accounts
// for this dashboard still exist, before it keeps it with the saved update in place of the refused credential; then it
// retries that same update (200, or 202 while the node hasn't confirmed). It never makes another set of credentials, and
// a request without a credential keeps its old bytes (no body). Those groups follow the r6 ones below.
// Uses exported functions with a stand-in fetch and stand-in `location`/`document`, renders RegistryView, both dialogs
// (with a stand-in context) and the signed-in App to static markup (effects never run; useSyncExternalStore reads the
// client snapshot, as in dashboard-control-plane-pure.mjs), and reads sources as text: no network, browser, build,
// copy, scratch or output files. It doesn't prove the browser, the Go server or a node; the browser review in
// scripts/dashboard-control-plane-browser.mjs drives those.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/registry-maintenance-pure.mjs   (exit 1 on any failure)
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
// As in dashboard-control-plane-pure.mjs: stores without a server snapshot read the client one, wrapped before
// anything else imports React, so the signed-in App renders. Checked before that render.
const clientStore = React.useSyncExternalStore;
const serverSafeStore = (subscribe, getSnapshot, getServerSnapshot) => clientStore(subscribe, getSnapshot, getServerSnapshot ?? getSnapshot);
React.useSyncExternalStore = serverSafeStore;
const reactModule = await import('react');
const storeWrapped = (reactModule.useSyncExternalStore ?? reactModule.default?.useSyncExternalStore) === serverSafeStore;
const { renderToStaticMarkup } = await import('react-dom/server');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'registry-maintenance-pure', tsconfig: resolve(web, 'tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(web, path)).href, import.meta.url);
const api = await load('src/api.ts');
const sessionModule = await load('src/session.ts');
const dashboard = await load('src/dashboard.ts');
const { CanaryContext } = await load('src/context.tsx');
const { RegistryView, EditNodeDialog, RotateNodeDialog, RecoverAccessDialog } = await load('src/views/Registry.tsx');
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const HEADER = 'x-canary-registered-node';
const TOKEN = 'registry-maintenance-review-dashboard-token';
const RECOVERY = 'registry-maintenance-review-node-admin-credential';
const REG = '0f3c2b1a9d8e7f60514233a4b5c6d7e8';
const ACCOUNT = { id: 'acct-1', name: 'review', role: 'admin', passwordEnabled: true };
const EMPTY = { node: { id: 'dashboard', name: 'Dashboard' }, now: 1_000_000, monitors: [], nodes: [], groups: [], maintenance: [], incidents: [], notifications: [], user: { name: 'review', role: 'admin' } };
const NODE_OVERVIEW = { node: { id: 'canary-b', name: 'Canary B' }, now: 2_000_000, user: { name: 'review', role: 'admin' }, nodes: [], incidents: [], notifications: [], monitors: [] };
const ENTRY = { id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', url: 'http://100.79.12.4:3100', local: false, state: 'answering', lastReadAt: 900_000, monitorCount: 3 };
const LOCAL = { id: 'a'.repeat(32), label: 'Local node', nodeId: 'canary-local', nodeName: 'Local node', url: 'http://node:3100', local: true, state: 'answering' };
const ROTATING = { ...ENTRY, id: 'b'.repeat(32), label: 'Lab', nodeId: 'canary-lab', url: 'http://10.0.8.30:3100', state: 'rotating', error: 'Cannot reach this node from the dashboard server' };
const REFUSED = { ...ENTRY, id: 'c'.repeat(32), label: 'Edge', nodeId: 'canary-edge', url: 'http://10.0.8.40:3100', state: 'credential-refused' };
const MOVED = { ...ENTRY, id: 'd'.repeat(32), label: 'Office', nodeId: 'canary-office', url: 'http://10.0.8.50:3100', state: 'different-node' };
// Every state the dashboard's server loads (dashboard_registry.go NewDashboard).
const STATES = ['starting', 'answering', 'not-answering', 'credential-refused', 'different-node', 'enrolling', 'removing', 'rotating'];
const PENDING = ['enrolling', 'removing', 'rotating'];

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const server = (url, init) => {
  const { pathname } = new URL(url);
  const method = init.method ?? 'GET';
  const body = init.body ? JSON.parse(init.body) : {};
  if (pathname === '/api/overview') return json(200, new Headers(init.headers).get(HEADER) ? NODE_OVERVIEW : EMPTY);
  if (pathname === '/api/auth/me') return json(200, ACCOUNT);
  if (pathname === '/api/dashboard/nodes' && method === 'GET') return json(200, [ENTRY, LOCAL]);
  if (pathname.endsWith('/rotate')) return body.token ? json(202, { ...ENTRY, state: 'rotating', error: 'Cannot reach this node from the dashboard server' }) : json(200, { ...ENTRY, state: 'starting' });
  if (pathname.endsWith('/retry')) return json(200, { ...ENTRY, state: 'starting' });
  if (method === 'PATCH') return json(200, { ...ENTRY, label: body.label, url: body.url ?? ENTRY.url, state: body.url ? 'starting' : ENTRY.state });
  if (method === 'DELETE' || pathname === '/api/auth/logout') return new Response(null, { status: 204 });
  return json(200, []);
};
let answer = server;
let calls = [];
globalThis.fetch = async (url, init = {}) => { calls.push({ url: String(url), init }); return answer(String(url), init); };
const record = async (run, respond = server) => { calls = []; answer = respond; try { await run(); } finally { answer = server; } return calls; };
const header = (call, name) => new Headers(call.init.headers).get(name);
const rejection = async promise => { try { await promise; } catch (error) { return error; } assert.fail('expected a failure'); };
const on = () => { globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; };
const off = () => { delete globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; delete pageDocument.documentElement; };
const pathOf = call => new URL(call.url).pathname;
const settle = () => new Promise(done => setTimeout(done, 5));
// Static markup has no text separators; any comment is dropped without a space, in case a React version adds them.
const text = markup => markup.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]*>/g, ' ').replace(/&#x27;/g, '\'').replace(/&quot;/g, '"').replace(/&amp;/g, '&').replace(/\s+/g, ' ');
const inputs = markup => markup.match(/<input\b[^>]*>/gu) ?? [];
// HTML attribute names are ASCII case-insensitive, and React 19's static markup keeps the JSX spelling of some of them
// (autoComplete, maxLength, spellCheck, autoCapitalize), which a browser reads as autocomplete, maxlength and so on.
// So a tag's attributes are read by lowercased name. A name given twice fails: a browser keeps only the first one.
const attributes = tag => {
  const found = new Map();
  for (const [, name, value] of tag.replace(/^<[a-z]+/iu, '').matchAll(/\s([^\s"'<>\/=]+)(?:="([^"]*)")?/gu)) {
    const key = name.toLowerCase();
    assert.ok(!found.has(key), `${key} appears twice in ${tag}`);
    found.set(key, value ?? '');
  }
  return found;
};
const submit = markup => /<button type="submit"[^>]*>/u.exec(markup)?.[0] ?? assert.fail('no submit button');
/** One node card's markup, from its heading reference to the end of its article. */
const card = (markup, id) => {
  const start = markup.indexOf(`aria-labelledby="r-${id}"`);
  assert.ok(start >= 0, `no card for ${id}`);
  return markup.slice(start, markup.indexOf('</article>', start));
};
const ctx = (role, list) => ({
  admin: role === 'admin', role, controlPlane: true, selection: null, select() {}, stillCurrent: () => true, refresh() {}, nodes: [], endpoint: 'dashboard',
  overview: { ...EMPTY, node: { id: 'dashboard', name: 'This dashboard' } }, connection: { awaiting: false, issuerSkew: 0, link: 'live' }, api: {},
  registry: { list, map: new Map(list.map(node => [node.id, node])), loading: false, error: undefined, unsupported: false, reload() {} },
});
const within = (role, list, element) => renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx(role, list) }, element));
const render = (role, list) => within(role, list, React.createElement(RegistryView));
const editDialog = (node, list = [node]) => within('admin', list, React.createElement(EditNodeDialog, { node, onClose() {} }));
const rotateDialog = (node, list = [node]) => within('admin', list, React.createElement(RotateNodeDialog, { node, onClose() {} }));
const recoverDialog = (node, list = [node]) => within('admin', list, React.createElement(RecoverAccessDialog, { node, onClose() {} }));

await check('states: “rotating” is read and kept, busy like enrolling and removing, exactly as the server lists them', async () => {
  on();
  assert.equal(api.normalizeRegisteredNode({ ...ENTRY, state: 'rotating' }).state, 'rotating');
  for (const state of STATES) assert.equal(api.normalizeRegisteredNode({ ...ENTRY, state }).state, state);
  for (const bad of ['online', 'Rotating', 'rotated', '']) assert.throws(() => api.normalizeRegisteredNode({ ...ENTRY, state: bad }), /could not read/u, JSON.stringify(bad));
  const sent = await record(async () => {
    const list = await new api.CanaryApi(PAGE, TOKEN).registeredNodes();
    assert.deepEqual(list.map(node => node.state), ['answering', 'rotating'], 'loading the list keeps a rotating entry');
    assert.equal(list[1].error, ROTATING.error);
  }, () => json(200, [ENTRY, ROTATING]));
  assert.equal(sent.length, 1);
  // Go's own lists, when its source is in the checkout (dashboard_registry.go).
  const registryGo = resolve(root, 'apps/node-go/internal/canary/dashboard_registry.go');
  if (existsSync(registryGo)) {
    const go = readFileSync(registryGo, 'utf8');
    const quoted = source => [...source.matchAll(/"([a-z-]+)"/gu)].map(match => match[1]).sort();
    const pendingBody = /func dashboardOperationPending\(state string\) bool \{([^}]*)\}/u.exec(go)?.[1];
    assert.ok(pendingBody, 'dashboardOperationPending found');
    assert.deepEqual(quoted(pendingBody), [...dashboard.PENDING_STATES].sort(), 'the busy states mirror dashboardOperationPending');
    const loaded = /slices\.Contains\(\[\]string\{([^}]*)\}, row\.State\)/u.exec(go)?.[1];
    assert.ok(loaded, 'the loaded state list found');
    assert.deepEqual(quoted(loaded), [...STATES].sort(), 'every state the server loads is one the page reads');
  }
  assert.match(readFileSync(resolve(root, 'packages/shared/src/dashboard.ts'), 'utf8'), /\| 'rotating'/u, 'the shared state union names rotating');
  assert.deepEqual(STATES.map(state => dashboard.pendingOperation(state)), STATES.map(state => PENDING.includes(state)));
  const info = dashboard.nodeStateInfo({ ...ENTRY, state: 'rotating' });
  assert.deepEqual({ tone: info.tone, label: info.label, out: info.out, readable: info.readable, attention: info.attention },
    { tone: 'pending', label: 'Updating node access', out: true, readable: false, attention: false });
  assert.equal(dashboard.nodeStateInfo(ROTATING).attention, true, 'a failed attempt left a reason, so an administrator should look');
  assert.match(info.text, /doesn’t read it until the node confirms/u);
  assert.match(info.text, /keeps monitoring and alerting on its own/u);
  assert.doesNotMatch(info.text, /Retry|Remove|Edit/u, 'viewers read this text: no action names');
  assert.deepEqual(['answering', 'starting', 'not-answering'].map(state => dashboard.nodeStateInfo({ ...ENTRY, state }).readable), [true, true, true]);
  assert.deepEqual(['credential-refused', 'different-node', ...PENDING].map(state => dashboard.nodeStateInfo({ ...ENTRY, state }).readable), [false, false, false, false, false]);
  const summary = dashboard.registrySummary([ENTRY, ROTATING, { ...ENTRY, id: 'e'.repeat(32), state: 'enrolling' }, { ...ENTRY, id: 'f'.repeat(32), state: 'rotating' }]);
  assert.equal(summary.unfinished, 3);
  assert.equal(summary.attention, 2, 'the enrollment and the failed rotation; a rotation still in progress needs nobody yet');
});

await check('card actions: each role is offered exactly what the dashboard’s server accepts in each state', () => {
  const retries = { enrolling: 'Retry enrollment', removing: 'Retry removal', rotating: 'Retry access update' };
  for (const state of STATES) {
    const pending = PENDING.includes(state);
    const remote = dashboard.nodeActions({ ...ENTRY, state }, true);
    assert.deepEqual(remote, {
      check: !pending, retry: pending ? retries[state] : null, edit: !pending, move: !pending,
      rotate: !pending && state !== 'different-node', remove: state !== 'removing',
    }, `admin, ${state}`);
    const local = dashboard.nodeActions({ ...LOCAL, state }, true);
    assert.deepEqual(local, {
      check: !pending, retry: pending ? retries[state] : null, edit: !pending, move: false,
      rotate: !pending && state !== 'different-node', remove: false,
    }, `admin, bundled node, ${state}`);
    const { url: _address, ...unlisted } = ENTRY;
    assert.deepEqual(dashboard.nodeActions({ ...unlisted, state }, false), { check: !pending, retry: null, edit: false, move: false, rotate: false, remove: false }, `reader, ${state}`);
  }
});

await check('edit rules: a rename alone sends no address; only an edited, different origin is a move; the bundled node never moves', () => {
  on();
  assert.deepEqual(dashboard.editChanges(ENTRY, 'Rack B', ENTRY.url, true), { input: null, moving: false, errors: {} }, 'nothing changed');
  assert.deepEqual(dashboard.editChanges(ENTRY, '  Rack B ', ENTRY.url, true).input, null, 'spaces alone change nothing');
  assert.deepEqual(dashboard.editChanges(ENTRY, ' Rack C ', ENTRY.url, true), { input: { label: 'Rack C' }, moving: false, errors: {} });
  assert.deepEqual(dashboard.editChanges(ENTRY, 'Rack B', `${ENTRY.url}/`, true), { input: null, moving: false, errors: {} }, 'the same origin with a slash isn’t a move');
  assert.deepEqual(dashboard.editChanges(ENTRY, 'Rack B', 'HTTP://100.79.12.9:3100/', true), { input: { label: 'Rack B', url: 'http://100.79.12.9:3100' }, moving: true, errors: {} });
  assert.deepEqual(dashboard.editChanges(ENTRY, 'Rack C', 'https://rack-b.example.test', true), { input: { label: 'Rack C', url: 'https://rack-b.example.test' }, moving: true, errors: {} });
  assert.deepEqual(dashboard.editChanges(LOCAL, 'Bundled', 'http://elsewhere:3100', false), { input: { label: 'Bundled' }, moving: false, errors: {} }, 'the deployment’s address is never sent');
  for (const bad of ['', 'node:3100', 'http://node:3100/api', 'http://user:pass@node:3100', 'http://node:3100?x=1', 'ftp://node', PAGE]) {
    const result = dashboard.editChanges(ENTRY, 'Rack B', bad, true);
    assert.ok(result.errors.url, bad);
    assert.equal(result.input, null, bad);
  }
  for (const bad of [' ', 'é'.repeat(65), 'tab\there']) assert.ok(dashboard.editChanges(ENTRY, bad, ENTRY.url, true).errors.label, JSON.stringify(bad));
});

await check('recovery credential: optional, and otherwise the server’s account-token rule (24 to 256 printable characters, 8 distinct)', () => {
  assert.equal(dashboard.recoveryProblem(''), undefined, 'empty: rotate with the dashboard’s own credential');
  for (const good of [RECOVERY, 'abcdefgh'.repeat(3), 'abcdefgh'.repeat(32), '!#$%&()*+,-./:;<=>?@[]^_{|}~']) assert.equal(dashboard.recoveryProblem(good), undefined, good);
  for (const bad of ['abcdefghijklmnopqrstuvw', 'abcdefgh'.repeat(32) + 'x', 'abcdefg'.repeat(4), 'has a space in it, long enough', `é${'abcdefgh'.repeat(3)}`, `tab\t${'abcdefgh'.repeat(3)}`]) {
    assert.ok(dashboard.recoveryProblem(bad), JSON.stringify(bad));
  }
});

await check('edit requests: one PATCH to the dashboard’s registry route, never naming a node, with exactly the documented body', async () => {
  on();
  const selected = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
  const sent = await record(async () => {
    const renamed = await selected.editNode(REG, { label: '  Rack B (east) ' });
    assert.deepEqual([renamed.id, renamed.label, renamed.url], [REG, 'Rack B (east)', ENTRY.url], 'registration kept');
    const moved = await selected.editNode(REG, { label: 'Rack B', url: 'http://100.79.12.9:3100/' });
    assert.deepEqual([moved.id, moved.nodeId, moved.url, moved.state], [REG, 'canary-b', 'http://100.79.12.9:3100', 'starting']);
  });
  assert.deepEqual(sent.map(call => `${call.init.method} ${pathOf(call)}`), [`PATCH /api/dashboard/nodes/${REG}`, `PATCH /api/dashboard/nodes/${REG}`]);
  assert.deepEqual(sent.map(call => JSON.parse(call.init.body)), [{ label: 'Rack B (east)' }, { label: 'Rack B', url: 'http://100.79.12.9:3100' }]);
  for (const call of sent) {
    assert.equal(header(call, HEADER), null, 'registry routes never carry the node selection');
    assert.equal(new URL(call.url).origin, PAGE);
    assert.equal(header(call, 'authorization'), `Bearer ${TOKEN}`);
    assert.equal(header(call, 'content-type'), 'application/json');
    assert.deepEqual([call.init.credentials, call.init.redirect, call.init.cache, call.init.referrerPolicy], ['same-origin', 'error', 'no-store', 'no-referrer']);
  }
  const refused = await record(async () => {
    await rejection(selected.editNode('a/b', { label: 'x' }));
    await rejection(selected.editNode(REG, { label: 'x', url: 'ftp://node' }));
    assert.ok((await rejection(new api.CanaryApi('http://10.0.8.20:3100', TOKEN).editNode(REG, { label: 'x' }))) instanceof api.OriginRefused, 'a node address is never contacted');
    assert.ok((await rejection(new api.CanaryApi('http://10.0.8.20:3100', TOKEN).rotateNode(REG))) instanceof api.OriginRefused);
  });
  assert.equal(refused.length, 0, 'odd IDs, bad addresses and other origins never leave the page');
});

await check('rotation requests: {} without a recovery credential, {token} with one; 200, 202 and Retry all read back', async () => {
  on();
  const selected = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
  const sent = await record(async () => {
    const done = await selected.rotateNode(REG);
    assert.deepEqual([done.id, done.state], [REG, 'starting']);
    const unconfirmed = await selected.rotateNode(REG, RECOVERY);
    assert.deepEqual([unconfirmed.id, unconfirmed.state, unconfirmed.url], [REG, 'rotating', ENTRY.url], 'a 202 is listed as rotating, address kept');
    const resumed = await selected.retryNode(REG);
    assert.equal(resumed.state, 'starting');
  });
  assert.deepEqual(sent.map(call => `${call.init.method} ${pathOf(call)}`), [`POST /api/dashboard/nodes/${REG}/rotate`, `POST /api/dashboard/nodes/${REG}/rotate`, `POST /api/dashboard/nodes/${REG}/retry`]);
  assert.equal(sent[0].init.body, '{}');
  assert.deepEqual(JSON.parse(sent[1].init.body), { token: RECOVERY });
  for (const call of sent) {
    assert.equal(header(call, HEADER), null);
    assert.equal(header(call, 'authorization'), `Bearer ${TOKEN}`, 'the dashboard sign-in, never the recovery credential');
    assert.ok(!call.url.includes(RECOVERY));
    assert.ok(![...new Headers(call.init.headers).values()].some(value => value.includes(RECOVERY)));
  }
  assert.ok(sent.filter(call => String(call.init.body ?? '').includes(RECOVERY)).length === 1, 'the recovery credential is in one request body only');
  assert.deepEqual(storageWrites, []);
});

await check('failure wording: every refusal says nothing changed and never suggests removing and re-adding; only a missing answer is unknown', async () => {
  on();
  const client = new api.CanaryApi(PAGE, TOKEN);
  const failWith = async (status, error, run) => { answer = () => json(status, { error }); try { return await rejection(run()); } finally { answer = server; } };
  const edit = async (status, error, moving = true) => dashboard.editFailure(await failWith(status, error, () => client.editNode(REG, moving ? { label: 'Rack B', url: 'http://100.79.12.9:3100' } : { label: 'Rack C' })), ENTRY, moving);
  const rotate = async (status, error, recovery = false) => dashboard.rotationFailure(await failWith(status, error, () => client.rotateNode(REG, recovery ? RECOVERY : undefined)), ENTRY, recovery);
  const unreachable = 'Cannot reach this node from the dashboard server; check its address, certificate and permissions';
  const cases = [
    [await edit(409, 'Finish the pending node operation first'), 'Another change to this node isn’t finished', true],
    [await edit(409, 'Registered node changed; refresh before editing'), 'This node changed in the meantime', true],
    [await edit(409, 'The bundled node address is managed in deployment settings'), 'This address is set by the deployment', true],
    [await edit(409, 'This node address is already registered'), 'Another entry already uses this address', false],
    [await edit(409, 'Dashboard permissions changed; sign in again'), 'Your dashboard permissions changed', false],
    [await edit(502, 'Different node at this address'), 'A different node answers at this address', false],
    [await edit(502, 'Node credential was refused'), 'This address doesn’t accept this dashboard’s access', false],
    [await edit(502, 'Node delegation identity changed'), 'This address doesn’t accept this dashboard’s access', false],
    [await edit(502, 'The node redirected the dashboard; redirects are refused'), 'The address answered with a redirect', false],
    [await edit(502, unreachable), 'The dashboard’s server couldn’t check the new address', false],
    [await edit(504, 'The dashboard node request timed out'), 'The dashboard’s server couldn’t check the new address', false],
    [await edit(400, 'Use the monitoring node address, not this dashboard address'), 'Not saved', false],
    [await edit(400, 'Invalid node label', false), 'Not saved', false],
    [await edit(401, 'Authentication required'), 'Your sign-in to this dashboard ended', false],
    [await edit(403, 'Insufficient permissions'), 'Only administrators change nodes', false],
    [await edit(404, 'Registered node not found'), 'This node is no longer on this dashboard', true],
    [await rotate(409, 'Upgrade this monitoring node before rotating its dashboard credentials'), 'This node can’t rotate dashboard access yet', false],
    [await rotate(409, 'Finish the pending node operation first'), 'Another change to this node isn’t finished', true],
    [await rotate(409, 'Registered node changed; refresh before rotating'), 'This node changed in the meantime', true],
    [await rotate(502, 'Node credential was refused', true), 'The node refused that administrator credential', false],
    [await rotate(502, 'Node credential has an unexpected role', true), 'That credential isn’t an administrator’s', false],
    [await rotate(502, 'Different node at this address'), 'A different node answers at this address', false],
    [await rotate(502, unreachable), 'The dashboard’s server couldn’t reach this node', false],
    [await rotate(504, 'The dashboard node request timed out'), 'The dashboard’s server couldn’t reach this node', false],
    [await rotate(503, 'Saved node credentials unavailable'), 'Not rotated', false],
    [await rotate(400, 'Tokens require 24-256 high entropy printable characters', true), 'Not rotated', false],
    [await rotate(401, 'Authentication required'), 'Your sign-in to this dashboard ended', false],
  ];
  for (const [failure, title, reload] of cases) {
    assert.equal(failure.title, title, failure.text);
    assert.equal(failure.unknown, false, title);
    assert.equal(failure.reload, reload, `${title}: reload`);
    assert.ok(failure.text.endsWith('Nothing was changed.'), `${title}: ${failure.text}`);
    assert.doesNotMatch(failure.text, /remove (it|the)|add it again|re-?add|re-?enrol/iu, title);
  }
  assert.match(cases[19][0].text, /already forgotten it; paste it again/u);
  assert.notEqual((await rotate(502, 'Node credential was refused')).title, 'The node refused that administrator credential', 'without a recovery credential, none is blamed');
  assert.equal(dashboard.editFailure(new Error('Enter a complete HTTP or HTTPS node address.'), ENTRY, true).text, 'Enter a complete HTTP or HTTPS node address. Nothing was changed.', 'refused before any request');
  answer = () => { throw new TypeError('Failed to fetch'); };
  const lostEdit = dashboard.editFailure(await rejection(client.editNode(REG, { label: 'Rack C' })), ENTRY, false);
  const lostRotation = dashboard.rotationFailure(await rejection(client.rotateNode(REG)), ENTRY, false);
  answer = () => json(202, { state: 'not a node entry' });
  const unreadable = dashboard.rotationFailure(await rejection(client.rotateNode(REG)), ENTRY, false);
  answer = server;
  for (const failure of [lostEdit, lostRotation, unreadable]) {
    assert.deepEqual([failure.title, failure.unknown, failure.reload], ['The result isn’t known yet', true, true]);
    assert.doesNotMatch(failure.text, /Nothing was changed/u, 'a lost answer may have changed it');
  }
  assert.match(lostRotation.text, /“Updating node access”, with Retry/u);
});

await check('session: a rename or move follows the selection without a new identity, poll or sign-in; nothing else is adopted', async () => {
  on();
  const { session } = sessionModule;
  calls = []; answer = server;
  await session.connect(new api.CanaryApi(PAGE, TOKEN));
  const signedIn = session.getSnapshot().connection;
  assert.equal(session.select({ id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', local: false }, signedIn), true);
  await settle();
  const viewing = session.getSnapshot().connection;
  assert.deepEqual([viewing.awaiting, viewing.overview.node.id], [false, 'canary-b']);
  calls = [];
  assert.equal(session.refreshSelection({ ...ENTRY, label: 'Rack B (moved)', url: 'http://100.79.12.9:3100' }, viewing), true);
  const renamed = session.getSnapshot().connection;
  assert.equal(renamed.identity, viewing.identity, 'same registration: nothing that belonged to it is dropped');
  assert.equal(renamed.api, viewing.api, 'same client and selection header');
  assert.equal(renamed.overview, viewing.overview, 'the node’s data stays');
  assert.deepEqual(renamed.selection, { id: REG, label: 'Rack B (moved)', nodeId: 'canary-b', nodeName: 'Canary B', local: false }, 'no address is kept in the selection');
  assert.ok(session.isCurrent(viewing), 'late answers for this selection still count');
  assert.equal(calls.length, 0, 'no poll restart, sign-in or sign-out');
  assert.equal(session.refreshSelection({ ...ENTRY, label: 'Rack B (moved)' }, renamed), false, 'nothing changed');
  assert.equal(session.refreshSelection({ ...ENTRY, id: 'f'.repeat(32), label: 'Other' }, renamed), false, 'another registration takes select()');
  assert.equal(session.refreshSelection({ ...ENTRY, nodeId: 'canary-x', label: 'Other node' }, renamed), false, 'another node ID is never adopted');
  assert.equal(session.getSnapshot().connection.selection.label, 'Rack B (moved)');
  off();
  assert.equal(session.refreshSelection({ ...ENTRY, label: 'Direct' }, renamed), false, 'not in direct mode');
  on();
  assert.equal(session.select(null, renamed), true);
  const none = session.getSnapshot().connection;
  assert.equal(session.refreshSelection({ ...ENTRY, label: 'Late' }, renamed), false, 'a stale caller changes nothing');
  assert.equal(session.refreshSelection({ ...ENTRY, label: 'Late' }, none), false, 'no selection to follow');
  await settle();
  assert.equal(await session.signOut(), 'forgotten');
});

await check('node cards: administrators get Edit and Rotate access where the server accepts them, pending entries only Retry, and no remove-and-add advice', () => {
  on();
  const list = [ENTRY, LOCAL, ROTATING, REFUSED, MOVED];
  const markup = render('admin', list);
  const of = node => card(markup, node.id);
  for (const node of [ENTRY, LOCAL, REFUSED, MOVED]) assert.match(of(node), new RegExp(`aria-label="Edit ${node.label}"`, 'u'), node.label);
  for (const node of [ENTRY, LOCAL, REFUSED]) assert.match(of(node), new RegExp(`aria-label="Rotate access for ${node.label}"`, 'u'), node.label);
  assert.doesNotMatch(of(MOVED), /aria-label="Rotate access for/u, 'refused by the server while another node answers there');
  assert.match(text(of(MOVED)), /If canary-office moved, edit this entry’s address\. The dashboard’s server checks that the same node answers there before saving\./u);
  assert.match(text(of(REFUSED)), /To give it new credentials, use Rotate access with a current administrator credential from the node\./u);
  assert.doesNotMatch(of(LOCAL), />Remove</u);
  assert.match(text(of(LOCAL)), /can’t be removed here, and its address changes only there/u);
  const busy = of(ROTATING);
  assert.match(text(busy), /Updating node access\. The dashboard’s server is replacing the credentials it uses on this node/u);
  assert.match(text(busy), /Cannot reach this node from the dashboard server\./u, 'the failed attempt’s reason');
  assert.match(text(busy), /Retry access update/u);
  assert.doesNotMatch(busy, /aria-label="(Edit|Rotate access for|Open) Lab"|Check now/u, 'busy: no edit, rotation, recheck or opening');
  assert.match(busy, />Remove</u, 'removal stays possible, as the server allows');
  assert.doesNotMatch(text(markup), /add it again|re-?add|remove the old entry/iu);
});

await check('node cards for viewers and editors: the state only, no addresses and no change actions', () => {
  on();
  const list = [ENTRY, LOCAL, ROTATING, REFUSED, MOVED];
  for (const role of ['viewer', 'editor']) {
    const read = render(role, list.map(({ url: _address, ...node }) => node));
    for (const pattern of [/aria-label="Edit /u, /aria-label="Rotate access/u, />Edit</u, />Rotate access</u, />Retry/u, />Remove</u, />Add node</u]) assert.doesNotMatch(read, pattern, `${role}: ${pattern}`);
    assert.match(text(card(read, ROTATING.id)), /Updating node access\..*An administrator can retry it\./u, role);
    assert.match(text(card(read, ENTRY.id)), /Check now/u, `${role} rechecks`);
    for (const node of list) assert.ok(!read.includes(node.url), `${role}: ${node.url}`);
    assert.doesNotMatch(text(read), /use Rotate access|edit this entry’s address/u, `${role}: administrator advice`);
    assert.match(text(read), /Administrators add and remove nodes/u);
  }
});

await check('edit dialog: name and address prefilled, nothing to save until something changes, the bundled node’s address read-only', () => {
  on();
  const remote = editDialog(ENTRY);
  assert.match(remote, /value="Rack B"/u);
  assert.match(remote, /value="http:\/\/100\.79\.12\.4:3100"/u);
  assert.match(text(remote), /Edit “Rack B”/u);
  assert.match(text(remote), /The registration stays the same, with its ID, its public status links/u);
  assert.match(text(remote), /accepts all three of this dashboard’s access credentials\. It can’t point this entry at a different node/u);
  assert.match(submit(remote), / disabled=""/u, 'nothing changed yet');
  for (const tag of inputs(remote)) assert.equal(attributes(tag).get('autocomplete'), 'off', tag);
  assert.equal(inputs(remote).length, 2);
  const local = editDialog(LOCAL);
  assert.doesNotMatch(text(local), /Node address/u);
  assert.equal(inputs(local).length, 1, 'only the name');
  assert.ok(local.includes(LOCAL.url), 'shown, not editable');
  assert.match(text(local), /This deployment’s settings manage the bundled node’s address, so it changes there, not here\. You can still rename it\./u);
  const busy = editDialog(ENTRY, [{ ...ENTRY, state: 'rotating' }]);
  assert.match(text(busy), /Updating node access Another change to this node is running/u);
  assert.match(submit(busy), / disabled=""/u);
  const meanwhile = editDialog(ENTRY, [{ ...ENTRY, label: 'Rack C' }]);
  assert.match(text(meanwhile), /This entry changed while the form was open It’s now “Rack C” at http:\/\/100\.79\.12\.4:3100 \. Fields you didn’t edit here keep those newer values/u);
  assert.doesNotMatch(text(remote + local), /remov|re-?add|add it again/iu);
});

await check('rotate dialog: explains the swap, keeps identity and sign-in, and offers an empty optional credential password managers leave alone', () => {
  on();
  const remote = rotateDialog(ENTRY);
  const words = text(remote);
  for (const phrase of [
    /Rotate access for “Rack B”\?/u,
    /makes new credentials for the three accounts it uses on canary-b \(administrator, editor and viewer\)/u,
    /The old credentials, and any node sessions made with them, stop working there\./u,
    /Nothing else changes: the node keeps its ID, its own accounts, peers, cluster secret, monitors and history, and you stay signed in here\./u,
    /shows it as “Updating node access”; the node keeps monitoring and alerting on its own/u,
    /if its answer is lost, a retry finishes this same change/u,
    /Node administrator credential \(optional\)/u,
    /this page forgets it as soon as it’s sent/u,
  ]) assert.match(words, phrase);
  const fields = inputs(remote);
  assert.equal(fields.length, 1);
  const field = attributes(fields[0]);
  assert.deepEqual([field.get('type'), field.get('value'), field.get('autocomplete')], ['password', '', 'off'], fields[0]);
  assert.doesNotMatch(submit(remote), / disabled=""/u);
  assert.doesNotMatch(words, /deployment’s settings is separate|refuses this dashboard’s current credentials/u);
  assert.match(text(rotateDialog(LOCAL)), /This replaces only this dashboard’s own access to the bundled node\. The administrator token in this deployment’s settings is separate and stays as it is\./u);
  const refused = text(rotateDialog(REFUSED));
  assert.match(refused, /The node refuses this dashboard’s current credentials Paste a current administrator credential from canary-edge below\./u);
  assert.match(submit(rotateDialog(ENTRY, [{ ...ENTRY, state: 'rotating' }])), / disabled=""/u, 'one operation at a time');
  assert.doesNotMatch(words + refused, /remov|re-?add|add it again|enrol/iu, 'rotation never sends anyone to removal or re-enrollment');
});

await check('wiring: the new calls go only through api.ts; the recovery credential is cleared before it is sent and never stored; the selection follows the registry', () => {
  const apiSource = readFileSync(resolve(web, 'src/api.ts'), 'utf8');
  const method = name => { const start = apiSource.indexOf(`async ${name}(`); assert.ok(start > 0, name); return apiSource.slice(start, apiSource.indexOf('\n  }\n', start)); };
  assert.match(method('editNode'), /this\.own<unknown>\(path, 'PATCH', body, signal, NODE_CHANGE_TIMEOUT_MS\)/u);
  assert.match(method('rotateNode'), /this\.own<unknown>\(path, 'POST', body, signal, NODE_CHANGE_TIMEOUT_MS\)/u);
  for (const name of ['editNode', 'rotateNode']) assert.doesNotMatch(method(name), /this\.call|fetch\(/u, name);
  for (const path of ['src/views/Registry.tsx', 'src/dashboard.ts', 'src/App.tsx', 'src/session.ts']) {
    assert.doesNotMatch(readFileSync(resolve(web, path), 'utf8'), /\bfetch\(|localStorage|sessionStorage|indexedDB|document\.cookie|console\./u, path);
  }
  const registry = readFileSync(resolve(web, 'src/views/Registry.tsx'), 'utf8');
  const rotation = registry.slice(registry.indexOf('export function RotateNodeDialog'));
  const cleared = rotation.indexOf("setToken('')");
  assert.ok(cleared > 0 && rotation.indexOf('api.rotateNode(') > cleared, 'cleared before the request leaves');
  assert.doesNotMatch(rotation, /navigate\(|href\(/u, 'never part of an address');
  assert.doesNotMatch(registry, /remove it and add it again|remove the old entry/u);
  const app = readFileSync(resolve(web, 'src/App.tsx'), 'utf8');
  assert.match(app, /session\.refreshSelection\(selectedRow, connection\)/u);
  assert.match(app, /if \(wasPending\.current && !selectedPending\) session\.refresh\(\);/u);
  assert.match(app, /selectedOperation\(registry\.map\.get\(selection\.id\), failure\)/u);
  assert.equal(dashboard.selectedOperation(ROTATING, null), ROTATING);
  assert.equal(dashboard.selectedOperation({ ...ENTRY, state: 'removing' }, { status: 502, detail: 'x' }).state, 'removing');
  assert.equal(dashboard.selectedOperation(ENTRY, { status: 409, detail: 'This node operation is not complete' }), 'unknown');
  assert.equal(dashboard.selectedOperation(undefined, { status: 409, detail: 'This node enrollment or removal is not complete' }), 'unknown');
  assert.equal(dashboard.selectedOperation(ENTRY, { status: 409, detail: 'Select a registered monitoring node first' }), null);
  assert.equal(dashboard.selectedOperation(ENTRY, { status: 502, detail: 'Node credential was refused' }), null);
  assert.equal(dashboard.selectedOperation(ENTRY, null), null);
});

await check('selected node: while its access changes, the page says so instead of a conflict, an outage or a sign-in prompt', async () => {
  assert.ok(storeWrapped, 'React was imported before the server-snapshot wrap, so App can’t be rendered');
  on();
  const { session } = sessionModule;
  calls = []; answer = server;
  await session.connect(new api.CanaryApi(PAGE, TOKEN));
  assert.equal(session.select({ id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', local: false }, session.getSnapshot().connection), true);
  await settle();
  assert.equal(session.getSnapshot().connection.awaiting, false);
  // What the dashboard's server answers for a selected node whose operation is pending (dashboard_http.go proxyNode).
  answer = (url, init) => pathOf({ url }) === '/api/overview' && new Headers(init.headers).get(HEADER) ? json(409, { error: 'This node operation is not complete' }) : server(url, init);
  session.refresh();
  await settle();
  const stale = session.getSnapshot().connection;
  assert.deepEqual([stale.link, stale.failure?.status, stale.selection?.id], ['stale', 409, REG]);
  location.hash = '#/nodes';
  try {
    const { App } = await load('src/App.tsx');
    const screen = text(renderToStaticMarkup(React.createElement(App)));
    assert.match(screen, /Rack B: a change to its access isn’t finished/u);
    assert.match(screen, /The dashboard’s server doesn’t read it until an administrator’s change to its access finishes\./u);
    assert.match(screen, /Showing the last data the dashboard’s server read from it/u);
    assert.match(screen, /Your sign-in and the other nodes aren’t affected\./u);
    assert.doesNotMatch(screen, /conflicting versions|Reload it before saving|isn’t answering \(|Sign in again/u, 'not a conflict, an outage or a sign-out');
  } finally {
    location.hash = '';
    answer = server;
  }
  assert.equal(await session.signOut(), 'forgotten');
});

// ----- Recover access (completion r7): finish a pending access update, optionally with a node administrator credential.
// A late refusal of the credential the saved update uses (dashboard_rotation_recovery_test.go
// TestDashboardRotationLateAuthorityLossCanRecoverSameJournal) leaves the entry `rotating` with this reason.
const STUCK = { ...ROTATING, error: 'Node delegation was not accepted; check enrollment credentials and node version' };
const LOCAL_ROTATING = { ...LOCAL, state: 'rotating' };

await check('recovery requests: a plain Retry keeps its exact old request; a recovery credential goes only into {token}; 200, 202 and 204 read back with the registration kept', async () => {
  on();
  const selected = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
  const replies = [
    () => json(200, { ...ENTRY, state: 'starting' }),
    () => json(200, { ...ENTRY, state: 'starting' }),
    () => json(202, { ...ENTRY, state: 'rotating', error: 'Cannot reach this node from the dashboard server' }),
    () => json(200, { ...ENTRY, state: 'starting' }),
    () => new Response(null, { status: 204 }),
  ];
  const sent = await record(async () => {
    const plain = await selected.retryNode(REG);
    assert.deepEqual([plain.id, plain.state], [REG, 'starting']);
    const empty = await selected.retryNode(REG, '');
    assert.deepEqual([empty.id, empty.state], [REG, 'starting'], 'an empty credential is no credential');
    const kept = await selected.retryNode(REG, RECOVERY);
    assert.deepEqual([kept.id, kept.nodeId, kept.url, kept.state, kept.error], [REG, 'canary-b', ENTRY.url, 'rotating', 'Cannot reach this node from the dashboard server'],
      '202: the credential is kept and the same update still waits for the node');
    const confirmed = await selected.retryNode(REG, RECOVERY);
    assert.deepEqual([confirmed.id, confirmed.nodeId, confirmed.state], [REG, 'canary-b', 'starting']);
    assert.equal(await selected.retryNode(REG), null, 'a finished removal still reads as gone');
  }, () => replies.shift()());
  assert.equal(sent.length, 5);
  // The keys and headers request() has always sent for Retry: no body key, no content type, no node selection.
  const OLD_INIT = ['cache', 'credentials', 'headers', 'method', 'redirect', 'referrerPolicy', 'signal'];
  for (const [index, call] of sent.entries()) {
    assert.equal(`${call.init.method} ${pathOf(call)}`, `POST /api/dashboard/nodes/${REG}/retry`);
    assert.equal(new URL(call.url).origin, PAGE);
    assert.equal(new URL(call.url).search, '');
    assert.deepEqual([call.init.credentials, call.init.redirect, call.init.cache, call.init.referrerPolicy], ['same-origin', 'error', 'no-store', 'no-referrer']);
    if (index === 2 || index === 3) {
      assert.deepEqual(Object.keys(call.init).sort(), [...OLD_INIT, 'body'].sort());
      assert.deepEqual(call.init.headers, { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, 'the dashboard sign-in, never the recovery credential');
      assert.equal(call.init.body, `{"token":"${RECOVERY}"}`);
    } else {
      assert.deepEqual(Object.keys(call.init).sort(), OLD_INIT, `request ${index}: no body at all, as before`);
      assert.deepEqual(call.init.headers, { Authorization: `Bearer ${TOKEN}` }, `request ${index}: exactly the old headers`);
    }
    assert.ok(!call.url.includes(RECOVERY));
  }
  const refused = await record(async () => {
    await rejection(selected.retryNode('a/b', RECOVERY));
    assert.ok((await rejection(new api.CanaryApi('http://10.0.8.20:3100', TOKEN).retryNode(REG, RECOVERY))) instanceof api.OriginRefused, 'a node address is never contacted');
  });
  assert.equal(refused.length, 0, 'odd IDs and other origins never leave the page');
  assert.deepEqual(storageWrites, []);
});

await check('recover action: administrators only, on a pending access update only, the bundled node included; Rotate access stays off there', () => {
  const { url: _address, ...unlisted } = ENTRY;
  for (const state of STATES) {
    for (const node of [ENTRY, LOCAL]) {
      assert.equal(dashboard.canRecoverAccess({ ...node, state }, true), state === 'rotating', `admin, ${node.label}, ${state}`);
      if (state === 'rotating') assert.equal(dashboard.nodeActions({ ...node, state }, true).rotate, false, 'no second rotation while one is pending');
    }
    assert.equal(dashboard.canRecoverAccess({ ...unlisted, state }, false), false, `reader, ${state}`);
  }
});

await check('recovery failure wording: refusals describe the saved update instead of saying nothing changed; a busy or finished update reloads; only a missing answer is unknown', async () => {
  on();
  const client = new api.CanaryApi(PAGE, TOKEN);
  const failWith = async (status, error, run) => { answer = () => json(status, { error }); try { return await rejection(run()); } finally { answer = server; } };
  const recover = async (status, error, recovery) => dashboard.recoveryFailure(await failWith(status, error, () => client.retryNode(REG, recovery ? RECOVERY : undefined)), STUCK, recovery);
  // [status, the dashboard server's message, with a credential, title, reload, says the saved update stays pending].
  // Messages: dashboard_registry_edit.go (retryRegisteredNode, replaceRotationRecovery, rotationPreflight),
  // dashboard_http.go (finishEnrollment, currentActor), dashboard_registry.go (nodeCall, nodeActor).
  const cases = [
    [502, 'Node credential was refused', true, 'The node refused that administrator credential', false, true],
    [502, 'Node credential has an unexpected role', true, 'That credential isn’t an administrator’s', false, true],
    [502, 'Cannot verify the node\'s dashboard delegation', true, 'This dashboard’s accounts on the node couldn’t be confirmed', false, true],
    [409, 'The node\'s dashboard delegation is incomplete; restore its retained state before rotating access', true, 'This dashboard’s accounts on the node changed', false, true],
    [409, 'The node\'s dashboard delegation changed; restore its retained state before rotating access', true, 'This dashboard’s accounts on the node changed', false, true],
    [409, 'Recovery credentials apply only to an idle pending access update', true, 'This update is busy or already finished', true, false],
    [409, 'This node operation is already running', false, 'This update is busy or already finished', true, false],
    [409, 'Registered node changed; refresh before recovering access', true, 'This node changed in the meantime', true, false],
    [409, 'Dashboard permissions changed; sign in again', true, 'Your dashboard permissions changed', false, true],
    [502, 'Different node at this address', true, 'A different node answers at this address', false, true],
    [502, 'The node redirected the dashboard; redirects are refused', true, 'The address answered with a redirect', false, true],
    [502, 'The node redirected the dashboard; redirects are refused', false, 'The address answered with a redirect', false, true],
    [502, 'Cannot reach this node from the dashboard server', true, 'The dashboard’s server couldn’t check that credential', false, true],
    [504, 'The dashboard node request timed out', true, 'The dashboard’s server couldn’t check that credential', false, true],
    [502, 'Node delegation was not accepted; check enrollment credentials and node version', false, 'The node didn’t accept the saved update', false, true],
    [502, 'Cannot reach this node from the dashboard server', false, 'The dashboard’s server couldn’t finish the update', false, true],
    [503, 'Saved node credentials unavailable', true, 'Not finished', false, true],
    [400, 'Tokens require 24-256 high entropy printable characters', true, 'Not finished', false, true],
    [401, 'Authentication required', true, 'Your sign-in to this dashboard ended', false, true],
    [403, 'Insufficient permissions', true, 'Only administrators change nodes', false, true],
    [404, 'Registered node not found', true, 'This node is no longer on this dashboard', true, false],
  ];
  for (const [status, error, recovery, title, reload, stillPending] of cases) {
    const failure = await recover(status, error, recovery);
    const label = `${status} ${error}`;
    assert.equal(failure.title, title, `${label}: ${failure.text}`);
    assert.equal(failure.unknown, false, label);
    assert.equal(failure.reload, reload, `${label}: reload`);
    assert.doesNotMatch(failure.text, /Nothing was changed|nothing changed/iu, `${label}: the saved update exists either way`);
    assert.doesNotMatch(failure.text, /remove (it|the)|add it again|re-?add|re-?enrol|rotate access/iu, label);
    assert.equal(failure.text.includes('The update saved earlier stays pending until the node confirms it, and the dashboard’s server keeps retrying it.'), stillPending, `${label}: ${failure.text}`);
    if (recovery) assert.match(failure.text, /[Tt]his page has already forgotten (it|the credential)\./u, label);
    if (recovery && stillPending) assert.match(failure.text, /The dashboard’s server didn’t keep that credential/u, label);
    if (!recovery) assert.doesNotMatch(failure.text, /didn’t keep|forgotten/u, `${label}: no credential was sent`);
  }
  assert.match((await recover(502, 'Node credential was refused', true)).text, /^It has to be a current administrator credential on canary-lab itself\. Paste one to try again\./u);
  assert.match((await recover(502, 'Node delegation was not accepted; check enrollment credentials and node version', false)).text, /paste a current administrator credential from canary-lab and try again\./u,
    'a plain attempt the node refused points to the credential field');
  assert.match((await recover(409, 'The node\'s dashboard delegation is incomplete; restore its retained state before rotating access', true)).text, /it doesn’t recreate or repair them\./u);
  answer = () => { throw new TypeError('Failed to fetch'); };
  const lost = dashboard.recoveryFailure(await rejection(client.retryNode(REG, RECOVERY)), STUCK, true);
  const lostPlain = dashboard.recoveryFailure(await rejection(client.retryNode(REG)), STUCK, false);
  answer = () => json(202, { state: 'not a node entry' });
  const unreadable = dashboard.recoveryFailure(await rejection(client.retryNode(REG, RECOVERY)), STUCK, true);
  answer = server;
  for (const failure of [lost, lostPlain, unreadable]) {
    assert.deepEqual([failure.title, failure.unknown, failure.reload], ['The result isn’t known yet', true, true]);
    assert.doesNotMatch(failure.text, /Nothing was changed|didn’t keep/u, 'a lost answer may have kept the credential and finished the update');
    assert.match(failure.text, /while it shows “Updating node access”, the update is still pending/u);
  }
  assert.match(lost.text, /may have kept that credential, and may have finished the update/u);
  assert.match(lostPlain.text, /may have finished the update/u);
  assert.doesNotMatch(lostPlain.text, /kept that credential|forgotten/u);
  const notSent = dashboard.recoveryFailure(new api.OriginRefused('http://10.0.8.20:3100'), STUCK, true);
  assert.deepEqual([notSent.title, notSent.unknown, notSent.reload], ['Not sent', false, false]);
  assert.doesNotMatch(notSent.text, /Nothing was changed/u);
});

await check('first rotation wording after the server’s checks: every refusal is still before anything is saved; the node’s accounts and a refused own credential are named, not read as a concurrent edit or an outage', async () => {
  on();
  const client = new api.CanaryApi(PAGE, TOKEN);
  const failWith = async (status, error, run) => { answer = () => json(status, { error }); try { return await rejection(run()); } finally { answer = server; } };
  const rotate = async (status, error, recovery = false) => dashboard.rotationFailure(await failWith(status, error, () => client.rotateNode(REG, recovery ? RECOVERY : undefined)), ENTRY, recovery);
  const own = await rotate(502, 'Node credential was refused');
  assert.deepEqual([own.title, own.unknown, own.reload], ['The node refuses this dashboard’s credential', false, false]);
  assert.match(own.text, /It may have been revoked on canary-b\. To rotate anyway, paste a current administrator credential from canary-b\. Nothing was changed\.$/u);
  for (const detail of ['The node\'s dashboard delegation is incomplete; restore its retained state before rotating access', 'The node\'s dashboard delegation changed; restore its retained state before rotating access']) {
    const failure = await rotate(409, detail);
    assert.deepEqual([failure.title, failure.unknown, failure.reload], ['This dashboard’s accounts on the node changed', false, false], `${detail}: not a concurrent edit of the entry`);
    assert.match(failure.text, /it doesn’t recreate or repair them\. Nothing was changed\.$/u);
  }
  for (const detail of ['Cannot verify the node\'s dashboard delegation', 'Node delegation identity changed', 'Node credential has an unexpected role']) {
    const failure = await rotate(502, detail);
    assert.deepEqual([failure.title, failure.text.endsWith('Nothing was changed.')], ['This dashboard’s accounts on the node couldn’t be confirmed', true], detail);
  }
  for (const recovery of [false, true]) {
    const failure = await rotate(502, 'The node redirected the dashboard; redirects are refused', recovery);
    assert.deepEqual([failure.title, failure.text.endsWith('Nothing was changed.')], ['The address answered with a redirect', true], `redirect, recovery ${recovery}: not a refused credential`);
  }
  answer = () => { throw new TypeError('Failed to fetch'); };
  assert.match(dashboard.rotationFailure(await rejection(client.rotateNode(REG)), ENTRY, false).text, /“Updating node access”, with Retry and Recover access\.$/u);
  answer = server;
  assert.match(readFileSync(resolve(web, 'src/views/Registry.tsx'), 'utf8'), /its card has Retry and Recover access\./u, 'the 202 notice names both');
});

await check('recovery outcome: 200 confirms the saved update; 202 says the credential was kept and the update still waits, never that nothing changed; 204 reads as a finished removal', () => {
  const confirmed = dashboard.recoveryOutcome(STUCK, { ...ROTATING, state: 'starting', error: undefined }, true);
  assert.deepEqual(confirmed, { finished: true, text: 'Updated this dashboard’s access to Lab. The node confirmed the saved credentials; the old ones no longer work there.' });
  const kept = dashboard.recoveryOutcome(STUCK, { ...ROTATING, error: 'Cannot reach this node from the dashboard server' }, true);
  assert.deepEqual(kept, {
    finished: false,
    text: 'The dashboard’s server kept that administrator credential with the saved update for Lab, but the node hasn’t confirmed the update yet. Cannot reach this node from the dashboard server. It still shows as “Updating node access”; the server keeps retrying with that credential, and Retry tries again now.',
  });
  const plain = dashboard.recoveryOutcome(STUCK, { ...ROTATING, error: undefined }, false);
  assert.deepEqual(plain, { finished: false, text: 'Retried Lab, but it isn’t confirmed yet. It still shows as “Updating node access”; the dashboard’s server keeps retrying.' });
  const gone = dashboard.recoveryOutcome(STUCK, null, false);
  assert.equal(gone.finished, true);
  assert.match(gone.text, /^Lab is no longer on this dashboard: a removal that started meanwhile finished/u);
  for (const outcome of [confirmed, kept, plain, gone]) assert.doesNotMatch(outcome.text, /nothing (was )?changed/iu);
});

await check('recovery keeps the session: the selection, its identity and client stay; the registry route names no node; no sign-in or sign-out', async () => {
  on();
  const { session } = sessionModule;
  calls = []; answer = server;
  await session.connect(new api.CanaryApi(PAGE, TOKEN));
  assert.equal(session.select({ id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', local: false }, session.getSnapshot().connection), true);
  await settle();
  const viewing = session.getSnapshot().connection;
  assert.equal(viewing.awaiting, false);
  calls = [];
  answer = (url, init) => pathOf({ url }).endsWith('/retry') ? json(202, { ...ENTRY, state: 'rotating', error: 'Cannot reach this node from the dashboard server' }) : server(url, init);
  try {
    const kept = await viewing.api.retryNode(REG, RECOVERY);
    assert.deepEqual([kept.id, kept.nodeId, kept.state], [REG, 'canary-b', 'rotating']);
    const after = session.getSnapshot().connection;
    assert.equal(after.identity, viewing.identity, 'same registration: nothing that belonged to it is dropped');
    assert.equal(after.api, viewing.api, 'same client and selection header');
    assert.deepEqual(after.selection, viewing.selection);
    assert.ok(session.isCurrent(viewing), 'late answers for this selection still count');
    assert.equal(session.refreshSelection(kept, after), false, 'same registration, name and node: nothing to adopt');
    assert.equal(dashboard.selectedOperation(kept, null), kept, 'still pending: the page explains the selected node instead of reporting an outage');
    assert.equal(dashboard.selectedOperation({ ...kept, state: 'starting' }, null), null, 'confirmed: read as usual');
    const registryCalls = calls.filter(call => pathOf(call).startsWith('/api/dashboard/'));
    assert.deepEqual(registryCalls.map(call => `${call.init.method} ${pathOf(call)}`), [`POST /api/dashboard/nodes/${REG}/retry`]);
    assert.equal(header(registryCalls[0], HEADER), null, 'the registry route never carries the selection');
    assert.ok(!calls.some(call => /^\/api\/auth\/(login|logout)/u.test(pathOf(call))), 'no sign-in or sign-out');
  } finally {
    answer = server;
  }
  await settle();
  assert.equal(await session.signOut(), 'forgotten');
});

await check('node cards: administrators get Recover access beside Retry on a pending access update, the bundled node included, never Rotate access there; viewers and editors get neither', () => {
  on();
  const list = [
    ENTRY, STUCK, LOCAL_ROTATING, REFUSED, MOVED,
    { ...ENTRY, id: 'e'.repeat(32), label: 'Joining', nodeId: 'canary-join', url: 'http://10.0.8.60:3100', state: 'enrolling' },
    { ...ENTRY, id: 'f'.repeat(32), label: 'Leaving', nodeId: 'canary-leave', url: 'http://10.0.8.70:3100', state: 'removing' },
  ];
  const markup = render('admin', list);
  for (const node of [STUCK, LOCAL_ROTATING]) {
    const one = card(markup, node.id);
    assert.match(one, new RegExp(`aria-label="Recover access to ${node.label}"`, 'u'), node.label);
    assert.match(text(one), /Retry access update/u, `${node.label}: ordinary Retry stays`);
    assert.doesNotMatch(one, /aria-label="Rotate access for/u, `${node.label}: no second rotation while one is pending`);
  }
  assert.match(text(card(markup, STUCK.id)), /If the node stopped accepting the credential this update uses, Recover access finishes the same update with a current administrator credential from the node\./u);
  assert.doesNotMatch(text(card(markup, LOCAL_ROTATING.id)), /If the node stopped accepting/u, 'no failed attempt yet, so no advice');
  for (const node of list.filter(entry => entry.state !== 'rotating')) assert.doesNotMatch(card(markup, node.id), /Recover access/u, node.label);
  for (const role of ['viewer', 'editor']) {
    const read = render(role, list.map(({ url: _address, ...node }) => node));
    assert.doesNotMatch(read, /Recover access|If the node stopped accepting/u, role);
    assert.match(text(card(read, STUCK.id)), /An administrator can retry it\./u, role);
  }
});

await check('recover dialog: finishes the saved update and makes no other credentials; one empty optional credential password managers leave alone; nothing to finish once the update is over', () => {
  on();
  const remote = recoverDialog(STUCK);
  const words = text(remote);
  for (const phrase of [
    /Recover access to “Lab”/u,
    /This finishes the access update the dashboard’s server already saved for canary-lab/u,
    /the same new credentials for the three accounts it uses there \(administrator, editor and viewer\)\. It doesn’t make another set\./u,
    /you stay signed in here/u,
    /first checks it on the node without changing anything, and checks that the three accounts this dashboard uses there still exist; it doesn’t recreate missing ones\./u,
    /Only then does it keep the credential, encrypted, with the saved update until the node confirms\./u,
    /Left empty, this retries with what the server already has, like Retry\./u,
    /Last attempt: Node delegation was not accepted; check enrollment credentials and node version\./u,
    /Node administrator credential \(optional\)/u,
    /this page forgets it as soon as it’s sent/u,
    /Finish update/u,
  ]) assert.match(words, phrase);
  const fields = inputs(remote);
  assert.equal(fields.length, 1);
  const field = attributes(fields[0]);
  assert.deepEqual([field.get('type'), field.get('value'), field.get('autocomplete')], ['password', '', 'off'], fields[0]);
  assert.doesNotMatch(submit(remote), / disabled=""/u);
  // The last attempt's reason is the server's own text ("check enrollment credentials"), so re-enrollment is matched as such.
  assert.doesNotMatch(words, /Rotate access|rotating|remov|re-?add|add it again|re-?enrol/iu, 'finishing never sends anyone to a second rotation, removal or re-enrollment');
  const local = text(recoverDialog(LOCAL_ROTATING));
  assert.match(local, /This finishes only this dashboard’s own access to the bundled node\. The administrator token in this deployment’s settings is separate and stays as it is\./u);
  assert.doesNotMatch(local, /Last attempt/u);
  const finished = recoverDialog(STUCK, [{ ...STUCK, state: 'starting', error: undefined }]);
  assert.match(text(finished), /There’s nothing left to finish The update isn’t pending any more; the node list shows “Starting” now\. No credential is needed\./u);
  assert.equal(inputs(finished).length, 0, 'no credential field when there is nothing to finish');
  assert.doesNotMatch(finished, /<button type="submit"/u);
  const removing = recoverDialog(STUCK, [{ ...STUCK, state: 'removing' }]);
  assert.match(text(removing), /Removal unfinished This entry isn’t updating its access any more, so there’s nothing to finish here\./u);
  assert.equal(inputs(removing).length, 0);
  assert.doesNotMatch(removing, /<button type="submit"/u);
});

await check('recover wiring: one retry with the credential through api.ts, cleared before it leaves; never a rotation; nothing stored or put in an address', () => {
  const apiSource = readFileSync(resolve(web, 'src/api.ts'), 'utf8');
  const start = apiSource.indexOf('async retryNode(');
  assert.ok(start > 0, 'retryNode');
  const method = apiSource.slice(start, apiSource.indexOf('\n  }\n', start));
  assert.match(method, /this\.own<unknown>\(path, 'POST', recovery \? \{ token: recovery \} : undefined, signal, NODE_CHANGE_TIMEOUT_MS\)/u);
  assert.doesNotMatch(method, /this\.call|fetch\(/u);
  const registry = readFileSync(resolve(web, 'src/views/Registry.tsx'), 'utf8');
  const begin = registry.indexOf('export function RecoverAccessDialog');
  assert.ok(begin > 0, 'RecoverAccessDialog');
  const recover = registry.slice(begin);
  const cleared = recover.indexOf("setToken('')");
  assert.ok(cleared > 0 && recover.indexOf('api.retryNode(opened.id, recovery || undefined)') > cleared, 'cleared before the request leaves');
  assert.doesNotMatch(recover, /rotateNode|navigate\(|href\(/u, 'never a second rotation, never part of an address');
  const nodeCard = registry.slice(registry.indexOf('function NodeCard'), registry.indexOf('function PeersCard'));
  assert.match(nodeCard, /const recover = canRecoverAccess\(node, admin\);/u);
  assert.match(nodeCard, /onClick=\{\(\) => onRecover\(node\)\}/u, 'the card only opens the dialog');
  assert.doesNotMatch(nodeCard, /retryNode\([^)]*,/u, 'the card’s own Retry never sends a credential');
  assert.match(registry, /<RecoverAccessDialog key=\{recovering\.id\} node=\{recovering\}/u);
});

off();
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
