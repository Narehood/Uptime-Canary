// Pure protocol-19 checks for default notification channels, adding a channel to chosen monitors and testing unsaved
// channel settings, as the node's source reads (canary/channel_defaults.go, channels.go, delivery_status.go, api.go):
// views/MonitorForm.tsx (defaultChannels, and the create body through the cloneModel seam), checks.tsx cloneInput,
// views/Channels.tsx (channelFormModel, and the list and form rendered with a stand-in Canary context) and the two new
// CanaryApi methods with a stand-in fetch. Imports the source in place through one tsx namespace: no copy, scratch,
// network, browser or output files, and no message is sent anywhere. Root's contract harness drives a real, paused node.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/channel-defaults-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const touched = [];
const recorder = name => (...args) => { touched.push([name, ...args.map(String)]); };
for (const name of ['localStorage', 'sessionStorage']) {
  Object.defineProperty(globalThis, name, { value: { getItem: () => null, setItem: recorder(`${name}.setItem`), removeItem: recorder(`${name}.removeItem`), clear: recorder(`${name}.clear`) }, configurable: true, writable: true });
}
let answer = () => { throw new Error('no network in this fixture'); };
const calls = [];
globalThis.fetch = async (url, init = {}) => { calls.push({ url: String(url), init }); return answer(String(url), init); };
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'channel-defaults-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const api = await load('api.ts');
const checks = await load('checks.tsx');
const { CanaryContext } = await load('context.tsx');
const { cloneModel, defaultChannels, MonitorFormView } = await load('views/MonitorForm.tsx');
const { channelFormModel, ChannelsView, ChannelFormView } = await load('views/Channels.tsx');

let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const goSource = file => readFileSync(resolve(root, 'apps/node-go/internal', file), 'utf8').replace(/\r\n/gu, '\n');
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const hook = (id, name, extra = {}) => ({ id, revision: `rev-${id}`, updatedAt: 1, name, enabled: true, config: { kind: 'webhook', webhook: { url: `https://hooks.example.test/${id}`, bearerToken: `secret-${id}` } }, ...extra });
const view = (channel, configConflict = false) => ({ channel, configConflict });

await check('node rules as read: a create that omits notificationIds without a legacy webhook gets the enabled defaults, at most 16 and sorted; [] opts out; edits never', () => {
  assert.match(goSource('canary/api.go'), /if input\.NotificationIDs == nil && input\.WebhookURL == "" \{\n\t+input\.NotificationIDs, err = n\.defaultChannelIDs\(\)/u);
  const defaults = goSource('canary/channel_defaults.go');
  assert.match(defaults, /candidate = candidate \|\| channel\.IsDefault && channel\.Enabled/u);
  assert.match(defaults, /if len\(heads\) != 1 \|\| !n\.resourceVersionReady\(heads\[0\]\) \{\n\t+return nil, fail\(409/u, 'a conflicting enabled default refuses an implicit create');
  assert.match(defaults, /if len\(out\) > 16 \{\n\t+return nil, fail\(409/u);
  assert.match(defaults, /slices\.Sort\(out\)/u);
  assert.match(defaults, /len\(input\.Monitors\) < 1 \|\| len\(input\.Monitors\) > maxMonitors/u, 'apply takes one to two hundred monitors');
  assert.match(defaults, /if monitor == nil \|\| monitor\.Revision != selected\.Revision \{\n\t+return 0, nil, fail\(409/u, 'a stale monitor refuses the whole batch');
  assert.match(defaults, /return 200, map\[string\]any\{"changedIds": changed\}, nil/u);
  assert.match(goSource('canary/channels.go'), /IsDefault bool\s+`json:"isDefault,omitempty"`/u);
  assert.match(goSource('canary/channels.go'), /path == "\/api\/notification-channels\/preview-test" && method == "POST" \{\n\t+if a\.Role == "viewer" \{/u);
  assert.match(goSource('canary/delivery_status.go'), /result\["preview"\] = true/u);
  assert.match(goSource('canary/delivery_status.go'), /now\(\) < n\.lastNotificationTest\+5000/u, 'one shared five-second test throttle');
});

await check('defaults: enabled channels marked default, sorted by ID, at most 16; disabled, deleted and non-default ones never; a conflicted default is named, not picked', () => {
  const list = [
    view(hook('c3', 'Ops', { isDefault: true })), view(hook('c1', 'Pager', { isDefault: true })), view(hook('c2', 'Quiet')), view(hook('c4', 'Off', { isDefault: true, enabled: false })),
    view(hook('c5', 'Gone', { isDefault: true, deleted: true })), view(hook('c6', 'Split', { isDefault: true }), true),
  ];
  assert.deepEqual(defaultChannels(list), { ids: ['c1', 'c3'], conflicted: ['Split'], tooMany: false });
  assert.deepEqual(defaultChannels(null), { ids: [], conflicted: [], tooMany: false });
  const many = Array.from({ length: 18 }, (_, i) => view(hook(`d${String(i).padStart(2, '0')}`, `D${i}`, { isDefault: true })));
  const picked = defaultChannels(many);
  assert.equal(picked.ids.length, 16); assert.equal(picked.tooMany, true);
  assert.deepEqual(picked.ids, many.map(entry => entry.channel.id).sort().slice(0, 16));
});

const draftOf = extra => ({ ...cloneModel.draftFrom(undefined, ['a']), name: 'Review new', type: 'manual', targets: {}, ...extra });
const stored = extra => ({ id: 'm1', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', name: 'Stored', type: 'manual', target: 'manual', intervalMs: 60_000, timeoutMs: 1000, nodeIds: ['a'], enabled: false, ...extra });
await check('create body: what the form shows is what is created; only a form that never knew the channel list leaves the defaults to the node; edits are unchanged', () => {
  assert.equal(cloneModel.draftFrom(undefined, ['a']).channelsMode, 'node');
  assert.equal(cloneModel.draftFrom(stored(), ['a']).channelsMode, 'chosen');
  let built = cloneModel.build(draftOf(), undefined, undefined);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal('notificationIds' in built.input, false, 'channel list unknown: omitted, so the node picks its defaults');
  built = cloneModel.build(draftOf({ channelIds: ['c1', 'c3'], channelsMode: 'defaults' }), undefined, undefined);
  assert.deepEqual(built.input.notificationIds, ['c1', 'c3'], 'the preselected defaults, sent explicitly');
  built = cloneModel.build(draftOf({ channelIds: [], channelsMode: 'defaults' }), undefined, undefined);
  assert.deepEqual(built.input.notificationIds, [], 'a known list with no defaults: explicit and empty');
  built = cloneModel.build(draftOf({ channelIds: [], channelsMode: 'chosen' }), undefined, undefined);
  assert.deepEqual(built.input.notificationIds, [], 'a cleared selection opts out explicitly');
  built = cloneModel.build(draftOf({ channelIds: ['c2'], channelsMode: 'chosen' }), undefined, undefined);
  assert.deepEqual(built.input.notificationIds, ['c2']);
  const plain = stored();
  const d = cloneModel.draftFrom(plain, ['a']), initial = structuredClone(d); d.name = 'Renamed';
  assert.equal('notificationIds' in cloneModel.build(d, plain, initial).input, false, 'an edit without channels stays without them');
  const withOne = stored({ notificationIds: ['c1'] });
  const e = cloneModel.draftFrom(withOne, ['a']);
  assert.deepEqual(cloneModel.build(e, withOne, structuredClone(e)).input.notificationIds, ['c1']);
  const cleared = cloneModel.build({ ...e, channelIds: [], channelsMode: 'chosen' }, withOne, structuredClone(e));
  assert.equal('notificationIds' in cleared.input, false, 'clearing an edit removes them, as before (the node adds no defaults to an edit)');
  assert.deepEqual(checks.checkChanges(checks.inputOf(withOne), cleared.input), [], 'destinations never start a new check');
});

await check('clone: a copy of a monitor without channels says so explicitly, so the node never adds defaults to it; a copy with channels keeps them', () => {
  const source = stored();
  const copy = checks.cloneInput(source, () => 'f'.repeat(64));
  assert.deepEqual(copy.notificationIds, []);
  assert.equal('notificationIds' in source, false, 'the source is unchanged');
  const d = cloneModel.draftFrom(copy, ['a']);
  assert.equal(d.channelsMode, 'chosen');
  const built = cloneModel.build(d, copy, structuredClone(d));
  assert.deepEqual(built.input.notificationIds, []);
  const withOne = checks.cloneInput(stored({ notificationIds: ['c1', 'c2'] }), () => 'f'.repeat(64));
  assert.deepEqual(withOne.notificationIds, ['c1', 'c2']);
  const legacy = checks.cloneInput(stored({ webhookUrl: 'https://hooks.example.test/legacy' }), () => 'f'.repeat(64));
  assert.deepEqual(legacy.notificationIds, []);
  assert.equal(legacy.webhookUrl, 'https://hooks.example.test/legacy');
});

await check('channel form: “Default for new monitors” is stored only when on (Go’s omitempty), survives a provider change, and other stored channel keys still survive', () => {
  const channel = hook('c1', 'Pager', { isDefault: true, futureChannelKey: { x: [1] } });
  const draft = channelFormModel.channelDraft(channel);
  assert.equal(draft.isDefault, true);
  let built = channelFormModel.buildChannel({ ...draft, name: 'Pager 2' }, channel);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(built.input.isDefault, true);
  assert.deepEqual(built.input.futureChannelKey, { x: [1] });
  built = channelFormModel.buildChannel({ ...draft, isDefault: false }, channel);
  assert.equal('isDefault' in built.input, false, 'off is omitted, never false');
  const plain = hook('c2', 'Quiet');
  const plainBuilt = channelFormModel.buildChannel(channelFormModel.channelDraft(plain), plain);
  assert.equal('isDefault' in plainBuilt.input, false, 'an unchanged channel saves as before');
  const fresh = channelFormModel.channelDraft(undefined);
  assert.equal(fresh.isDefault, false);
  const moved = channelFormModel.buildChannel({ ...draft, kind: 'slack', slackUrl: 'https://hooks.slack.com/services/T0/B0/x' }, channel);
  if (moved.input) { assert.equal(moved.input.isDefault, true); assert.deepEqual(moved.input.futureChannelKey, { x: [1] }); }
  assert.match(source('views/Channels.tsx'), /const CHANNEL_KEYS = \['name', 'enabled', 'config', 'isDefault'\];/u);
});

const ACCOUNT_TOKEN = 'channel-defaults-review-token';
await check('API: an unsaved test posts the whole channel once to preview-test and must be confirmed as a preview; nothing else is sent or kept', async () => {
  const client = new api.CanaryApi('https://node.example.test:3100', ACCOUNT_TOKEN);
  const input = { name: 'Unsaved', enabled: false, config: { kind: 'webhook', webhook: { url: 'https://hooks.example.test/unsaved', bearerToken: 'unsaved-bearer-secret' } } };
  answer = () => json(202, { preview: true, attempts: [{ id: 'att-1', nodeId: 'a', kind: 'test', channelId: 'preview-x', provider: 'webhook', state: 'queued', failures: 0 }] });
  calls.length = 0;
  const reply = await client.previewChannelTest(input);
  assert.deepEqual(reply.attempts.map(attempt => attempt.id), ['att-1']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, 'https://node.example.test:3100/api/notification-channels/preview-test');
  assert.equal(calls[0].init.method, 'POST');
  assert.deepEqual(JSON.parse(calls[0].init.body), input, 'the settings as they are, disabled included');
  assert.equal(new Headers(calls[0].init.headers).get('Authorization'), `Bearer ${ACCOUNT_TOKEN}`);
  assert.equal(calls[0].init.cache, 'no-store');
  answer = () => json(202, { attempts: [] });
  await assert.rejects(client.previewChannelTest(input), /didn’t confirm a test of unsaved settings/u, 'a saved test’s answer is not taken for a preview');
  answer = () => json(429, { error: 'Wait five seconds between notification tests' });
  await assert.rejects(client.previewChannelTest(input), error => error instanceof api.ApiFailure && error.status === 429 && /five seconds/u.test(error.detail ?? ''));
  answer = () => json(403, { error: 'Insufficient permissions' });
  await assert.rejects(client.previewChannelTest(input), error => error instanceof api.ApiFailure && error.status === 403);
});

await check('API: applying a channel sends its revision and only each chosen monitor’s ID and revision, and reads back which changed', async () => {
  const client = new api.CanaryApi('https://node.example.test:3100', ACCOUNT_TOKEN);
  answer = () => json(200, { changedIds: ['m2'] });
  calls.length = 0;
  const monitors = [{ ...stored({ id: 'm1', revision: 'r1', notificationIds: ['c9'] }), name: 'private name' }, stored({ id: 'm2', revision: 'r2' })];
  assert.deepEqual(await client.applyChannel('c 1', 'rev-c1', monitors), { changedIds: ['m2'] });
  assert.equal(calls[0].url, 'https://node.example.test:3100/api/notification-channels/c%201/apply');
  assert.equal(calls[0].init.method, 'POST');
  assert.deepEqual(JSON.parse(calls[0].init.body), { revision: 'rev-c1', monitors: [{ id: 'm1', revision: 'r1' }, { id: 'm2', revision: 'r2' }] });
  answer = () => json(200, { changedIds: [] });
  assert.deepEqual(await client.applyChannel('c1', 'rev-c1', monitors), { changedIds: [] }, 'zero changes is a valid answer');
  answer = () => json(200, { changed: ['m1'] });
  await assert.rejects(client.applyChannel('c1', 'rev-c1', monitors), /couldn’t read|could not read/u);
  answer = () => json(409, { error: 'A selected monitor is stale, deleted or conflicting; refresh before applying' });
  await assert.rejects(client.applyChannel('c1', 'rev-c1', monitors), error => error instanceof api.ApiFailure && error.status === 409);
  answer = () => { throw new Error('no network in this fixture'); };
});

const NODES = [{ id: 'a', name: 'Node A', state: 'local', lastSeenAt: null }];
const catalog = (list = []) => ({ list, map: new Map(list.map(entry => [entry.channel?.id ?? entry.tag?.id, entry])), loading: false, error: undefined, unsupported: false, reload() {} });
const ctx = (role, channels, monitors = []) => {
  const views = monitors.map(monitor => ({ monitor, state: 'up', observations: [], unknownNodes: 0, configConflict: false }));
  return {
    role, editable: role !== 'viewer', admin: role === 'admin', endpoint: 'a', controlPlane: false, selection: null, search: '',
    overview: { node: { id: 'a', name: 'Node A' }, now: 1_760_000_000_000, monitors: views, nodes: [], incidents: [], notifications: [], user: { name: 'review', role } },
    monitorMap: new Map(views.map(entry => [entry.monitor.id, entry])), nodes: NODES, nodeMap: new Map(NODES.map(node => [node.id, node])),
    api: { registeredNode: null, baseUrl: 'http://node-a.test:3100' }, refresh() {}, groups: null, groupMap: new Map(), maintenanceMap: new Map(), stillCurrent: () => true,
    channels: catalog(channels), tags: catalog(),
  };
};
const renderIn = (value, element) => renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value }, element));
await check('rendered: the channel list marks defaults and offers “Add to monitors” to editors only; the form offers the default flag and “Test these settings”', () => {
  const channels = [view(hook('c1', 'Pager', { isDefault: true })), view(hook('c2', 'Quiet')), view(hook('c3', 'Off', { isDefault: true, enabled: false }))];
  const editor = renderIn(ctx('editor', channels), React.createElement(ChannelsView));
  assert.ok(editor.includes('default for new monitors') && editor.includes('default, but disabled'));
  assert.equal((editor.match(/>Add to monitors</gu) ?? []).length, 3, 'one per channel');
  assert.ok(/aria-label="Add Off to monitors"[^>]*disabled=""|disabled=""[^>]*aria-label="Add Off to monitors"/u.test(editor), 'a disabled channel can’t be added');
  const viewer = renderIn(ctx('viewer', channels), React.createElement(ChannelsView));
  assert.equal(viewer.includes('Add to monitors'), false);
  const form = renderIn(ctx('editor', channels), React.createElement(ChannelFormView, { id: 'c1' }));
  assert.ok(form.includes('Default for new monitors') && form.includes('Test these settings'));
  assert.ok(/<input type="checkbox" checked=""[^>]*\/><span>Default for new monitors/u.test(form), 'the stored flag is shown checked');
  for (const secret of ['secret-c1']) assert.equal(form.includes(`>${secret}<`), false, 'no credential is page text');
  const fresh = renderIn(ctx('editor', channels), React.createElement(ChannelFormView, {}));
  assert.ok(/<input type="checkbox"\/><span>Default for new monitors/u.test(fresh), 'off for a new channel');
});

await check('rendered: a new monitor names default channels and a conflicted default; an edit shows no default notes', () => {
  const channels = [view(hook('c1', 'Pager', { isDefault: true })), view(hook('c6', 'Split', { isDefault: true }), true)];
  const fresh = renderIn(ctx('editor', channels), React.createElement(MonitorFormView, {}));
  assert.ok(fresh.includes('default for new monitors'));
  assert.ok(fresh.includes('Default channel Split has a configuration conflict, so it isn’t preselected.'));
  const m = stored({ id: 'm-edit', notificationIds: ['c1'] });
  const edit = renderIn(ctx('editor', channels, [m]), React.createElement(MonitorFormView, { id: 'm-edit' }));
  assert.equal(edit.includes('isn’t preselected'), false);
  assert.equal(edit.includes('Preselected: this node’s enabled default channels'), false);
});

await check('source: defaults prefill only new monitors, apply never preselects or exceeds 200, and the unsaved test keeps settings in memory only', () => {
  const form = source('views/MonitorForm.tsx');
  assert.match(form, /setDraft\(d => d\.channelsMode === 'chosen' \? d : \{ \.\.\.d, channelIds: defaultKey \? defaultKey\.split\(' '\) : \[\], channelsMode: 'defaults' \}\)/u);
  assert.match(form, /onChange=\{ids => setDraft\(d => \(\{ \.\.\.d, channelIds: ids, channelsMode: 'chosen' \}\)\)\}/u, 'a pick is the user’s choice');
  const channels = source('views/Channels.tsx');
  assert.match(channels, /const \[selected, setSelected\] = useState<string\[\]>\(\[\]\);/u, 'nothing is selected until the user picks');
  assert.match(channels, /const APPLY_MAX = 200;/u);
  assert.match(channels, /\.slice\(0, APPLY_MAX\)/u);
  const dialog = channels.slice(channels.indexOf('function PreviewTestDialog'), channels.indexOf('const APPLY_MAX'));
  assert.ok(dialog.length > 500);
  assert.equal(/localStorage|sessionStorage|console\.|indexedDB|document\.cookie/u.test(dialog), false);
});

await check('no storage: rendering, building and the stand-in requests stored nothing in the browser', () => {
  assert.deepEqual(touched, []);
});

console.log(failed ? `${failed} check group(s) failed` : 'all channel default checks passed');
process.exit(failed ? 1 : 0);
