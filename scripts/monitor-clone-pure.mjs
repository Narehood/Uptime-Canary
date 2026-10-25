// Pure checks for cloning a monitor in the dashboard alone (2026-10-24 completion audit O1): checks.tsx `cloneInput`,
// the monitor form's cloneModel and previewModel seams and its exported newPushToken (views/MonitorForm.tsx), the
// `#/monitors/<id>/clone` route, and the actual MonitorFormView rendered to static markup with a stand-in context for an
// editor, a viewer, a conflicted source, an unknown type and a missing source. The contract they pin: a copy starts from
// the source's writable settings, private ones and unknown options included, deep-copied so the source never changes;
// runtime fields are dropped so the node assigns a new ID, revision and generation; a push copy gets a new token and
// never the source's; nothing is created before the form is saved; only editors and admins copy; nothing copied reaches
// the URL, browser storage, the network or the console. Imports the source in place through one tsx namespace: no
// copy, scratch, network or output files. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/monitor-clone-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
// Stand-ins installed before any source loads: writes to storage and any request are recorded, never performed.
const touched = [];
const recorder = name => (...args) => { touched.push([name, ...args.map(String)]); };
for (const name of ['localStorage', 'sessionStorage']) {
  Object.defineProperty(globalThis, name, { value: { getItem: () => null, setItem: recorder(`${name}.setItem`), removeItem: recorder(`${name}.removeItem`), clear: recorder(`${name}.clear`) }, configurable: true, writable: true });
}
globalThis.fetch = (...args) => { touched.push(['fetch', String(args[0])]); return Promise.reject(new Error('no network in this fixture')); };
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'monitor-clone-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const checks = await load('checks.tsx');
const router = await load('router.ts');
const form = await load('views/MonitorForm.tsx');
const { CanaryContext } = await load('context.tsx');
const { cloneModel, previewModel, newPushToken, MonitorFormView } = form;
touched.length = 0;

// Console output during a check is captured: React may warn, but nothing it or the form prints may carry a secret.
const logged = [];
let failed = 0;
const check = (name, fn) => {
  const saved = {};
  for (const method of ['log', 'info', 'warn', 'error', 'debug']) { saved[method] = console[method]; console[method] = (...args) => { logged.push(args.map(String).join(' ')); }; }
  let error;
  try { fn(); } catch (e) { error = e; } finally { Object.assign(console, saved); }
  if (error) { failed++; console.log(`FAIL ${name}: ${error.message}`); } else console.log(`PASS ${name}`);
};
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const canonical = value => JSON.stringify(value, (key, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.entries(v).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) : v);

const CERT = '-----BEGIN CERTIFICATE-----\nMIIBclonereview\n-----END CERTIFICATE-----\n';
const SECRET = {
  password: 'clone-basic-password', header: 'clone-header-secret', body: 'clone-private-body', oauth: 'clone-oauth-secret',
  key: '-----BEGIN PRIVATE KEY-----\nclone-private-key\n-----END PRIVATE KEY-----\n', webhook: 'legacy-clone-secret', redis: 'clone-redis-password',
  steam: 'ABCDEF0123456789ABCDEF0123456789', home: '/var/lib/clone-review/.pm2', token: 'a'.repeat(64), other: 'c'.repeat(64),
  // Protocol 19 real browser: the whole endpoint is private (its host, and a credential in its query), so is the token.
  browserHost: 'chromium.clone-review.test', browserQuery: 'clone-browser-query-token', browserBearer: 'clone-browser-bearer',
};
const NEW_TOKEN = 'b'.repeat(64);
const RUNTIME = { id: 'set below', revision: 'rev-7', updatedAt: 1_760_000_000_000, deleted: false, checkGeneration: 'gen-3', pushStartedAt: 1_759_000_000_000 };
const base = (id, extra) => ({ ...RUNTIME, id, intervalMs: 60_000, timeoutMs: 5_000, nodeIds: ['a', 'b'], enabled: true, repeatAfterMs: 900_000, notificationIds: ['c1'], groupId: 'g1', tags: [{ tagId: 't1', value: 'prod', public: true }], ...extra });
const SOURCES = {
  http: base('m-http', {
    name: 'API', type: 'http', target: 'https://api.example.test/health', expectedStatusCodes: [200, 204], webhookUrl: `https://hooks.example.test/${SECRET.webhook}`, retries: 2, retryIntervalMs: 10_000,
    http: { method: 'POST', body: SECRET.body, headers: { 'X-Review': SECRET.header }, basicAuth: { username: 'review', password: SECRET.password }, proxyUrl: 'http://proxy.internal.test:3128', maxRedirects: 0, keyword: { value: 'ok', negate: true }, futureOption: { nested: [1, null] } },
    tls: { serverName: 'api.example.test', clientCertPem: CERT, clientKeyPem: SECRET.key, minValidityDays: 7 }, futureTop: { kept: true },
  }),
  oauth: base('m-oauth', { name: 'Token API', type: 'http', target: 'https://api.example.test/v2', http: { oauth2: { tokenUrl: 'https://auth.example.test/token', clientId: 'clone-client', clientSecret: SECRET.oauth, scope: 'read', authMethod: 'client_secret_post', futureGrant: { x: 1 } } } }),
  redis: base('m-redis', { name: 'Cache', type: 'redis', target: '127.0.0.1:6379', service: { redis: { address: '127.0.0.1:6379', username: 'review', password: SECRET.redis, database: 2, tlsMode: 'plain' } } }),
  steam: base('m-steam', { name: 'Game', type: 'steam', target: '127.0.0.9:27015', steam: { apiKey: SECRET.steam, skipLatency: true } }),
  pm2: base('m-pm2', { name: 'Worker', type: 'pm2', target: 'api', pm2: { transport: 'unix', home: SECRET.home, namespace: 'review' } }),
  tailscale: base('m-ts', { name: 'Build server', type: 'tailscale-ping', target: 'build-server', upsideDown: true }),
  push: base('m-push', { name: 'Heartbeat job', type: 'push', target: 'push', timeoutMs: 1_000, push: { token: SECRET.token, allowGet: true } }),
  browser: base('m-browser', {
    name: 'Checkout page', type: 'real-browser', target: 'https://shop.example.test/checkout',
    browser: { endpoint: `wss://${SECRET.browserHost}:9222/devtools/browser/clone-review?token=${SECRET.browserQuery}`, bearerToken: SECRET.browserBearer, screenshot: true, delayMs: 500 },
  }),
};
const PRIVATE = [SECRET.password, SECRET.header, SECRET.body, SECRET.oauth, 'clone-private-key', SECRET.webhook, SECRET.redis, SECRET.steam, SECRET.home, SECRET.token, SECRET.browserHost, SECRET.browserQuery, SECRET.browserBearer];
const writable = m => Object.fromEntries(Object.entries(m).filter(([key, value]) => !(key in RUNTIME) && key !== 'name' && value !== null && value !== undefined));

check('cloneInput: every setting is copied, private and unknown ones included; runtime fields go; the name is marked; the source never changes', () => {
  for (const [kind, m] of Object.entries(SOURCES)) {
    if (kind === 'push') continue;
    const before = structuredClone(m);
    let calls = 0;
    const copy = checks.cloneInput(m, () => { calls++; return NEW_TOKEN; });
    assert.equal(copy.name, `${m.name} (copy)`, kind);
    for (const key of Object.keys(RUNTIME)) assert.equal(key in copy, false, `${kind} ${key}`);
    assert.deepEqual(writable(copy), writable(m), kind);
    assert.equal(calls, 0, `${kind}: only a push copy asks for a token`);
    copy.nodeIds.push('z');
    copy.tags[0].value = 'changed';
    if (copy.http?.futureOption) copy.http.futureOption.nested.push(9);
    if (copy.service) copy.service.redis.password = 'changed';
    if (copy.browser) copy.browser.delayMs = 1;
    assert.deepEqual(m, before, `${kind}: the source is unchanged`);
  }
});

check('cloneInput: a push copy gets one new token; allowGet stays; the source keeps its token and anchor', () => {
  const m = SOURCES.push, before = structuredClone(m);
  let calls = 0;
  const copy = checks.cloneInput(m, () => { calls++; return NEW_TOKEN; });
  assert.equal(calls, 1);
  assert.deepEqual(copy.push, { token: NEW_TOKEN, allowGet: true });
  assert.equal('pushStartedAt' in copy, false);
  assert.deepEqual(m, before);
  const real = [newPushToken(), newPushToken()];
  for (const token of real) assert.match(token, /^[0-9a-f]{64}$/u);
  assert.notEqual(real[0], real[1]);
  assert.notEqual(checks.cloneInput(m, newPushToken).push.token, SECRET.token);
});

check('inputOf: pushStartedAt is runtime-owned too, so edits and copies never echo it', () => {
  const input = checks.inputOf(SOURCES.push);
  for (const key of Object.keys(RUNTIME)) assert.equal(key in input, false, key);
  assert.deepEqual(input.push, SOURCES.push.push, 'an edit keeps the stored token');
});

const copies = Object.fromEntries(Object.entries(SOURCES).map(([kind, m]) => [kind, checks.cloneInput(m, () => NEW_TOKEN)]));
const built = Object.fromEntries(Object.entries(copies).map(([kind, copy]) => {
  const draft = cloneModel.draftFrom(copy, ['a']);
  return [kind, { draft, copy, result: cloneModel.build(draft, copy, cloneModel.draftFrom(copy, ['a'])) }];
}));

check('form: the copy’s create body is every copied setting exactly, with no ID, revision or generation', () => {
  for (const [kind, { copy, result }] of Object.entries(built)) {
    assert.ok(result.input, `${kind}: ${JSON.stringify({ ...result.errors, ...result.serviceErrors })}`);
    assert.equal(canonical(result.input), canonical(copy), kind);
    for (const key of Object.keys(RUNTIME)) assert.equal(key in result.input, false, `${kind} ${key}`);
  }
  assert.equal(built.push.result.input.push.token, NEW_TOKEN);
  assert.equal(built.http.result.input.http.basicAuth.password, SECRET.password, 'copied credentials are sent, not blanked');
  assert.deepEqual(built.http.result.input.http.futureOption, { nested: [1, null] });
  assert.deepEqual(built.oauth.result.input.http.oauth2.futureGrant, { x: 1 });
  assert.equal(built.steam.result.input.steam.apiKey, SECRET.steam);
  assert.equal(built.pm2.result.input.pm2.home, SECRET.home);
  assert.equal(built.tailscale.result.input.target, 'build-server');
  assert.equal(JSON.stringify(built.browser.result.input.browser), JSON.stringify(SOURCES.browser.browser), 'browser options byte for byte, private endpoint and token included');
});

check('form: repeat while down keeps its stored shape: missing stays missing, a stored 0 stays 0, changes are sent, a new monitor sends 0', () => {
  // The 2026-10-25 browser run copied a monitor made through the API without `repeatAfterMs`, and the copy came back
  // with 0. Go keeps a *int64 with omitempty, so a missing value and 0 are stored and returned apart (both mean off).
  // Every source above repeats every 15 minutes, so this group pins the other two stored shapes as well.
  const missing = structuredClone(SOURCES.oauth); delete missing.repeatAfterMs;
  const zero = { ...structuredClone(SOURCES.oauth), repeatAfterMs: 0 };
  const sent = (monitor, change = d => d) => {
    const initial = cloneModel.draftFrom(monitor, ['a']), draft = change(structuredClone(initial)), drafted = structuredClone(draft);
    const result = cloneModel.build(draft, monitor, initial);
    assert.ok(result.input, JSON.stringify({ ...result.errors, ...result.serviceErrors }));
    assert.deepEqual(draft, drafted, 'the draft is unchanged');
    return result.input;
  };
  const on = minutes => d => ({ ...d, repeatOn: true, repeat: { amount: String(minutes), unit: 'min' } });
  const off = d => ({ ...d, repeatOn: false, repeat: { amount: '5', unit: 'min' } });
  for (const [label, m, stored] of [['missing', missing, undefined], ['0', zero, 0], ['15 min', SOURCES.oauth, 900_000]]) {
    const before = structuredClone(m);
    const copy = checks.cloneInput(m, () => NEW_TOKEN), copied = structuredClone(copy);
    assert.equal('repeatAfterMs' in copy, stored !== undefined, `${label}: cloneInput copies the stored shape`);
    assert.equal(copy.repeatAfterMs, stored, label);
    assert.equal(cloneModel.draftFrom(copy, ['a']).repeatOn, Boolean(stored), label);
    for (const [base, what] of [[copy, 'copy'], [m, 'edit']]) {
      const unchanged = sent(base, d => ({ ...d, name: `${m.name} renamed` }));
      assert.equal('repeatAfterMs' in unchanged, stored !== undefined, `${label} ${what}: an untouched repeat is sent as stored`);
      assert.equal(unchanged.repeatAfterMs, stored, `${label} ${what}`);
      assert.equal(canonical(unchanged), canonical({ ...(base === m ? checks.inputOf(m) : copy), name: `${m.name} renamed` }), `${label} ${what}: the whole body is the stored settings`);
      const left = sent(base, off);
      assert.equal('repeatAfterMs' in left, stored !== undefined, `${label} ${what}: repeats left off keep the stored shape`);
      assert.equal(left.repeatAfterMs, stored === undefined ? undefined : 0, `${label} ${what}: repeats turned off send 0`);
      assert.equal(sent(base, on(1)).repeatAfterMs, 60_000, `${label} ${what}: a new repeat is sent`);
      assert.equal(sent(base, on(30)).repeatAfterMs, 1_800_000, `${label} ${what}`);
    }
    assert.deepEqual(m, before, `${label}: the source is unchanged`);
    assert.deepEqual(copy, copied, `${label}: the copy is unchanged by building`);
  }
  const fresh = { ...cloneModel.draftFrom(undefined, ['a']), name: 'New peer', type: 'tailscale-ping', targets: { 'tailscale-ping': 'peer' } };
  const freshInput = cloneModel.build(fresh, undefined, undefined).input;
  assert.ok(freshInput);
  assert.equal(freshInput.repeatAfterMs, 0, 'a new monitor sends repeats off explicitly, as before');
  assert.equal(cloneModel.build(on(2)(fresh), undefined, undefined).input.repeatAfterMs, 120_000);
});

check('form: a copy never takes the source’s push token, even pasted in; a stored duplicate never blocks an edit', () => {
  const views = Object.values(SOURCES).map(monitor => ({ monitor }));
  const { draft, copy, result } = built.push;
  assert.equal(cloneModel.pushTokenRule(result, draft, copy, undefined, views), result, 'the new token passes');
  const pasted = { ...draft, pushToken: SECRET.token };
  const refused = cloneModel.pushTokenRule(cloneModel.build(pasted, copy, cloneModel.draftFrom(copy, ['a'])), pasted, copy, undefined, views);
  assert.equal(refused.input, undefined);
  // Expectation changed 2026-10-25: the earlier text said the node would accept the duplicate and then refuse heartbeats
  // for both. A local write is refused instead: canary/groups.go validateReferences answers 409 "Push token is already
  // assigned" when any other live monitor head holds the token (replicated collisions fail closed separately).
  assert.match(refused.errors.pushToken, /^“Heartbeat job” already uses this push token, and the node refuses a second monitor with the same one\. Generate another\.$/u);
  assert.equal(refused.errors.pushToken.includes(SECRET.token), false, 'the message never shows the token');
  // An edit keeps a token the monitor is stored with, even if another monitor already has it.
  const twin = { ...SOURCES.push, id: 'm-twin', name: 'Twin' };
  const edit = cloneModel.draftFrom(SOURCES.push, ['a']);
  const editBuilt = cloneModel.build(edit, SOURCES.push, cloneModel.draftFrom(SOURCES.push, ['a']));
  assert.equal(cloneModel.pushTokenRule(editBuilt, edit, SOURCES.push, SOURCES.push.id, [...views, { monitor: twin }]), editBuilt);
  const other = { ...SOURCES.push, id: 'm-other', name: 'Other job', push: { token: SECRET.other } };
  const taken = { ...edit, pushToken: SECRET.other };
  assert.match(cloneModel.pushTokenRule(cloneModel.build(taken, SOURCES.push, edit), taken, SOURCES.push, SOURCES.push.id, [...views, { monitor: other }]).errors.pushToken, /^“Other job” already uses/u);
  const fresh = { ...cloneModel.draftFrom(undefined, ['a']), name: 'New job', type: 'push', pushToken: SECRET.other };
  assert.match(cloneModel.pushTokenRule(cloneModel.build(fresh, undefined, undefined), fresh, undefined, undefined, [...views, { monitor: other }]).errors.pushToken, /^“Other job” already uses/u);
  const http = built.http;
  assert.equal(cloneModel.pushTokenRule(http.result, http.draft, http.copy, undefined, views), http.result, 'other types are untouched');
});

check('preview: the copy’s JSON preview masks every private value', () => {
  for (const [kind, { result }] of Object.entries(built)) {
    const text = JSON.stringify(previewModel.masked(result.input));
    for (const value of [...PRIVATE, NEW_TOKEN]) assert.equal(text.includes(value), false, `${kind} preview shows ${value.slice(0, 12)}`);
  }
});

check('route: #/monitors/<id>/clone carries the source ID alone; nothing else parses as a copy', () => {
  assert.deepEqual(router.parseRoute('#/monitors/m-http/clone'), { name: 'monitor-clone', id: 'm-http' });
  assert.equal(router.href('monitors', 'm-http', 'clone'), '#/monitors/m-http/clone');
  for (const hash of ['#/monitors/m-http/clone/x', '#/monitors/m-http/clone?name=API', '#/monitors/m-http/copy']) assert.equal(router.parseRoute(hash).name, 'not-found', hash);
  assert.equal(router.sectionOf({ name: 'monitor-clone', id: 'm-http' }), 'monitors');
  assert.ok(source('App.tsx').includes("case 'monitor-clone': return <MonitorFormView key={`clone-${route.id}`} cloneOf={route.id} />;"));
});

check('detail page: the Clone action exists only among the editor actions and waits while versions conflict', () => {
  const detail = source('views/MonitorDetail.tsx');
  const start = detail.indexOf('actions={editable ? (');
  const end = detail.indexOf(') : <ReadOnly />}', start);
  assert.ok(start > 0 && end > start, 'the editor-only action block');
  assert.equal(detail.split("href('monitors', m.id, 'clone')").length - 1, 1, 'one Clone link');
  const block = detail.slice(start, end);
  assert.ok(block.includes("href={view.configConflict ? undefined : href('monitors', m.id, 'clone')} aria-disabled={view.configConflict || undefined}"));
  assert.ok(block.includes('<Icon name="copy" />Clone</a>'));
});

const NODES = [{ id: 'a', name: 'Node A', state: 'local', lastSeenAt: null }, { id: 'b', name: 'Node B', state: 'connected', lastSeenAt: 1_759_999_000_000 }];
const created = [];
const catalog = () => ({ list: [], map: new Map(), loading: false, error: undefined, unsupported: false, reload() {} });
const ctx = (role, monitors) => {
  const list = monitors.map(({ monitor, configConflict = false }) => ({ monitor, state: 'up', observations: [], unknownNodes: 0, configConflict }));
  return {
    role, editable: role !== 'viewer', admin: role === 'admin', endpoint: 'a', controlPlane: false, selection: null,
    overview: { node: { id: 'a', name: 'Node A' }, now: 1_760_000_000_000, monitors: list, nodes: [], incidents: [], notifications: [], user: { name: 'review', role } },
    monitorMap: new Map(list.map(view => [view.monitor.id, view])), nodes: NODES, nodeMap: new Map(NODES.map(node => [node.id, node])),
    api: { registeredNode: null, baseUrl: 'http://node-a.test:3100', createMonitor: (...args) => { created.push(args); return new Promise(() => {}); }, updateMonitor: (...args) => { created.push(args); return new Promise(() => {}); } },
    refresh() {}, groups: null, groupMap: new Map(), stillCurrent: () => true, channels: catalog(), tags: catalog(),
  };
};
const render = (role, cloneOf, monitors = Object.values(SOURCES).map(monitor => ({ monitor }))) =>
  renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx(role, monitors) }, React.createElement(MonitorFormView, { cloneOf })));

check('rendered: an editor gets a new-monitor form from the copy, says private settings are copied, and creates nothing', () => {
  for (const role of ['editor', 'admin']) {
    const html = render(role, 'm-http');
    for (const said of ['Copy monitor', 'Private connection settings are copied too.', 'value="API (copy)"', 'Create monitor', 'Nothing is created until you select Create monitor', 'stays as it is', 'href="#/monitors/m-http"', 'Change type']) {
      assert.ok(html.includes(said), `${role}: ${said}`);
    }
    assert.equal(html.includes('Save changes'), false);
    assert.equal(html.includes('Saving removes the stored'), false, 'a copy has no stored values to remove');
    assert.equal(html.includes('own new push token'), false);
  }
  assert.deepEqual(created, [], 'nothing is created before save');
});

check('rendered: a push copy shows a new hidden token, never the source’s, and no rotation warning', () => {
  const html = render('editor', 'm-push');
  assert.ok(html.includes('This copy has its own new push token, so heartbeats sent with the original’s token never count for it.'));
  assert.equal(html.includes(SECRET.token), false, 'the source token is nowhere in the page');
  // React 19's server renderer writes an <input>'s value after all its other attributes (react-dom-server pushInput keeps
  // value, defaultValue, checked and name back), so the markup is `type="password" class="mono" readOnly="" ... value="…"`.
  // The 2026-10-24 matcher expected value right after class and never matched. Find the field by its attributes instead.
  const inputs = [...html.matchAll(/<input\b([^>]*?)\/?>/gu)].map(([, attrs]) => Object.fromEntries([...attrs.matchAll(/([^\s=/]+)(?:="([^"]*)")?/gu)].map(([, name, value = '']) => [name.toLowerCase(), value])));
  const fields = inputs.filter(attrs => attrs.id?.endsWith('-token'));
  assert.equal(fields.length, 1, 'one push token field');
  const [field] = fields;
  assert.equal(field.type, 'password', 'the token field is hidden');
  assert.equal(field.class, 'mono');
  assert.ok('readonly' in field, 'the token is generated, not typed');
  const token = field.value;
  assert.match(token ?? '', /^[0-9a-f]{64}$/u, 'the generated token, in the hidden field');
  assert.notEqual(token, SECRET.token);
  assert.equal(html.split(token).length - 1, 1, 'the token appears only as the hidden field’s value');
  assert.ok(html.includes('Generate another'));
  for (const said of ['Rotate token', 'Keep the current token', 'Saving a new token starts a new check']) assert.equal(html.includes(said), false, said);
  assert.ok(html.includes('&lt;token&gt;'), 'the commands show a placeholder until the token is shown');
  assert.deepEqual(created, []);
});

check('rendered: viewers, conflicted, unknown and missing sources get no form and no copied value', () => {
  const viewer = render('viewer', 'm-http');
  assert.ok(viewer.includes('Copy monitor') && viewer.includes('Viewers can’t change monitors'));
  const conflict = render('editor', 'm-http', [{ monitor: SOURCES.http, configConflict: true }]);
  assert.ok(conflict.includes('Resolve the conflict before copying') && conflict.includes('no single version to copy'));
  const future = render('editor', 'm-future', [{ monitor: { ...SOURCES.http, id: 'm-future', type: 'future-kind' } }]);
  assert.ok(future.includes('doesn’t know the “future-kind” check type') && future.includes('Copy it with the CLI or API'));
  const missing = render('editor', 'm-gone');
  assert.ok(missing.includes('doesn’t know the monitor to copy'));
  for (const [label, html] of Object.entries({ viewer, conflict, future, missing })) {
    assert.equal(html.includes('Create monitor'), false, label);
    assert.equal(/<(input|textarea|select)\b/u.test(html), false, `${label}: no fields`);
    for (const value of PRIVATE) assert.equal(html.includes(value), false, `${label} shows ${value.slice(0, 12)}`);
  }
  assert.deepEqual(created, []);
});

check('no browser storage writes, requests, creates or secrets in console output during any check above', () => {
  assert.deepEqual(touched, []);
  assert.deepEqual(created, []);
  for (const line of logged) for (const value of [...PRIVATE, NEW_TOKEN]) assert.equal(line.includes(value), false, `console output carried ${value.slice(0, 12)}`);
  const sources = ['checks.tsx', 'views/MonitorForm.tsx', 'router.ts'].map(source).join('\n');
  assert.doesNotMatch(sources, /localStorage|sessionStorage|indexedDB|document\.cookie|\bconsole\.[a-z]+\s*\(/u, 'the copy path never stores or logs');
});

console.log(`${logged.length} console message(s) were captured while checking; none carried a private value.`);
console.log(failed ? `${failed} FAILED` : 'ALL PASSED');
process.exit(failed ? 1 : 0);
