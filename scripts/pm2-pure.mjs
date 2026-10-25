// Pure protocol-18 checks for the PM2 process monitor kind: apps/web/src/pm2.tsx, views/Pm2Fields.tsx (rendered to static
// markup), checks.tsx and the existing
// previewModel seam of views/MonitorForm.tsx. Imports the source in place through one tsx namespace: no copy, no scratch,
// no network, no output files. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/pm2-pure.mjs   (exit 1 on any failure)
// MonitorForm draftFrom/build and Config monitorRows aren't exported; their rename, JSON import and conflict round trips
// belong to root's contract harness, which appends those exports.
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
globalThis.React ??= reactModule.default ?? reactModule;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'pm2-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const p = await load('pm2.tsx');
const checks = await load('checks.tsx');
const specs = await load('serviceSpecs.tsx');
const fmt = await load('format.ts');
const { previewModel } = await load('views/MonitorForm.tsx');
const { Pm2Fields } = await load('views/Pm2Fields.tsx');
let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const ch = code => String.fromCodePoint(code);
const MASK = ch(0x2022).repeat(3);
const LONE = String.fromCharCode(0xd800);
const bytes = text => new TextEncoder().encode(text).length;
const EARLIER = ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp', 'globalping', 'steam', 'system-service', 'postgres', 'mysql', 'sqlserver', 'oracledb', 'mongodb',
  'redis', 'docker', 'mqtt', 'rabbitmq', 'kafka-producer', 'grpc', 'sftp', 'ntp', 'snmp', 'radius', 'sip-options', 'push', 'manual'];
const HOME = '/var/lib/canary/.pm2';
const STORED = { transport: 'unix', home: HOME, namespace: 'Review NS' };

// Protocol 19 explicitly adds the Tailscale peer ping after PM2 as a third local type (validation.go: a typed target
// with no options), so the count and local-category expectations move from 29 and two types to 30 and three.
// Protocol 19 also explicitly adds the real browser right after HTTP in the web category (validation.go: an HTTP(S)
// typed target with `browser` options), so the count moves from 30 to 31; it is filtered out of the earlier order too.
// Protocol 19 also explicitly adds GameDig right after Steam among game servers (validation.go: a `gamedig` options
// object), so the count moves from 31 to 32; it is filtered out of the earlier order too and the local types don't change.
check('catalogue: 32 unique types, the 28 earlier ones in their order, pm2 after system-service in the local category, then protocol-19 Tailscale, real browser and GameDig', () => {
  const types = checks.CHECK_TYPES.map(entry => entry.type);
  assert.equal(types.length, 32);
  assert.equal(new Set(types).size, 32);
  assert.deepEqual(types.filter(type => type !== 'pm2' && type !== 'tailscale-ping' && type !== 'real-browser' && type !== 'gamedig'), EARLIER);
  assert.equal(types.indexOf('pm2'), types.indexOf('system-service') + 1);
  const entry = checks.checkType('pm2');
  assert.equal(entry.label, 'PM2 process');
  assert.equal(entry.short, 'pm2');
  assert.equal(entry.category, 'local');
  assert.deepEqual(checks.CHECK_TYPES.filter(other => other.category === 'local').map(other => other.type), ['system-service', 'pm2', 'tailscale-ping']);
  assert.equal(entry.targetLabel, undefined, 'the PM2 section owns its process-name field');
  assert.equal(checks.typeLabel('pm2'), 'PM2 process');
  assert.equal(Object.keys(specs.SERVICE_PROTOCOL).length, 16);
});

check('typed-target sets: network types unchanged, protocol 19 adds tailscale-ping and then real-browser; pm2 is not a service protocol and gets no TLS options', () => {
  assert.deepEqual([...checks.NETWORK_TYPES], ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp']);
  assert.deepEqual([...checks.TYPED_TARGET_TYPES].filter(type => type !== 'real-browser'), [...checks.NETWORK_TYPES, 'system-service', 'tailscale-ping']);
  assert.equal([...checks.TYPED_TARGET_TYPES].at(-1), 'real-browser');
  assert.equal(checks.isServiceType('pm2'), false);
  assert.equal(checks.tlsApplies('pm2', 'api', undefined), false);
});

check('process name: exact text, as Go exactText(name, 256) admits it; numbers and punctuation are literal', () => {
  for (const name of ['api', '3', '0042', 'api worker', 'a/b\\c$~*?[]"\'`', `${ch(0x1f680)} launch`, 'a'.repeat(256), ch(0xe9).repeat(128),
    `${ch(0xfeff)}api`, `api${ch(0x200b)}`, `x${ch(0xa0)}y`]) {
    assert.equal(p.pm2NameProblem(name), undefined, JSON.stringify(name));
  }
  const reason = name => p.pm2NameProblem(name) ?? '';
  assert.match(reason(''), /exactly as pm2 list/u);
  for (const name of [' api', 'api ', `api${ch(0xa0)}`, `${ch(0x3000)}api`, `api${ch(0x2028)}`]) assert.match(reason(name), /start or end/u, JSON.stringify(name));
  assert.match(reason('   '), /not only spaces/u);
  for (const name of ['a\tb', 'a\nb', `a${ch(0x7f)}`, `a${ch(0x85)}`, `a${ch(0x9b)}b`, `${ch(0)}a`]) assert.match(reason(name), /control characters/u, JSON.stringify(name));
  assert.match(reason(`a${LONE}b`), /unpaired surrogate/u);
  assert.match(reason('a'.repeat(257)), /256 bytes/u);
  assert.match(reason(ch(0xe9).repeat(129)), /256 bytes/u);
  assert.equal(bytes(ch(0xe9).repeat(128)), 256);
});

check('namespace: optional; when given, the same exact-text rule', () => {
  assert.equal(p.pm2NamespaceProblem(''), undefined);
  for (const namespace of ['default', 'Prod', 'team a/b', 'n'.repeat(256)]) assert.equal(p.pm2NamespaceProblem(namespace), undefined, namespace);
  assert.match(p.pm2NamespaceProblem(' '), /not only spaces/u);
  assert.match(p.pm2NamespaceProblem('prod '), /start or end/u);
  assert.match(p.pm2NamespaceProblem('a\nb'), /control/u);
  assert.match(p.pm2NamespaceProblem('n'.repeat(257)), /256 bytes/u);
});

check('Unix home: a clean absolute literal path whose rpc.sock path fits in 107 bytes', () => {
  for (const home of ['/', HOME, '/home/deploy/.pm2', '/srv/pm2 home', `/opt/${ch(0xfc)}ber`, `/${'a'.repeat(97)}`, `/${ch(0xe9).repeat(48)}`, '/a.b/.c', '/x/...']) {
    assert.equal(p.pm2HomeProblem(home), undefined, home);
  }
  assert.equal(p.pm2SocketPath('/'), '/rpc.sock');
  assert.equal(p.pm2SocketPath(HOME), `${HOME}/rpc.sock`);
  assert.equal(bytes(p.pm2SocketPath(`/${'a'.repeat(97)}`)), 107);
  const reason = home => p.pm2HomeProblem(home) ?? '';
  assert.match(reason(''), /Enter the PM2 home/u);
  assert.match(reason('relative/.pm2'), /absolute/u);
  assert.match(reason('.pm2'), /absolute/u);
  for (const home of ['~/.pm2', '/home/~deploy/.pm2', '$HOME/.pm2', '/home/$USER/.pm2', '/a$', '~']) assert.match(reason(home), /~ or \$VARIABLES/u, home);
  for (const home of ['C:\\Users\\canary\\.pm2', '/home/x\\y', '\\\\.\\pipe\\rpc.sock']) assert.match(reason(home), /backslashes/u, home);
  for (const home of ['/home/deploy/.pm2/', '//home', '/home//deploy', '/home/./x', '/home/../x', '/..', '/.']) assert.match(reason(home), /trailing slash|segments/u, home);
  assert.match(reason(' /home/x'), /start or end/u);
  assert.match(reason('/home/x '), /start or end/u);
  assert.match(reason('/home/x\n'), /control/u);
  assert.match(reason(`/home/${LONE}`), /unpaired surrogate/u);
  assert.match(reason(`/${'a'.repeat(98)}`), /107 bytes.*at most 98/u);
  assert.match(reason(`/${ch(0xe9).repeat(49)}`), /107 bytes/u);
  assert.match(reason(`/${'a'.repeat(300)}`), /107 bytes/u, 'a long home names the socket limit, not a raw text limit');
});

const fresh = (change = {}) => ({ ...p.pm2DraftFrom(undefined, ''), target: 'api', ...change });
check('builder: a new draft is Unix with a blank home that must be entered; namespace and Windows home omitted', () => {
  assert.deepEqual(p.pm2DraftFrom(undefined, ''), { target: '', transport: 'unix', home: '', namespace: '' });
  let errors = {};
  p.buildPm2(undefined, fresh(), errors);
  assert.deepEqual(Object.keys(errors), ['pm2Home'], 'no environment discovery or assumed user home');
  errors = {};
  const built = p.buildPm2(undefined, fresh({ home: '/home/deploy/.pm2' }), errors);
  assert.deepEqual(errors, {});
  assert.equal(built.target, 'api');
  assert.equal(JSON.stringify(built.value), JSON.stringify({ transport: 'unix', home: '/home/deploy/.pm2' }));
  assert.equal(JSON.stringify(p.buildPm2(undefined, fresh({ home: HOME, namespace: 'Review NS' }), {}).value), JSON.stringify(STORED));
  assert.equal(JSON.stringify(p.buildPm2(undefined, fresh({ transport: 'windows-pipe' }), {}).value), JSON.stringify({ transport: 'windows-pipe' }));
});

check('builder: the name is never trimmed; every error lands on its own field', () => {
  const errors = {};
  const built = p.buildPm2(undefined, fresh({ target: ' api', home: '~/.pm2', namespace: 'prod ' }), errors);
  assert.equal(built.target, ' api');
  assert.deepEqual(Object.keys(errors).sort(), ['pm2Home', 'pm2Namespace', 'pm2Target']);
  // The 2026-10-24 r3 review added pm2Transport for an unknown connection that isn't the stored one (see below).
  assert.deepEqual(Object.keys(p.PM2_FIELD_LABEL).sort(), ['pm2Home', 'pm2Namespace', 'pm2Target', 'pm2Transport']);
  assert.equal(p.buildPm2(undefined, fresh({ target: '0042', home: HOME }), {}).target, '0042', 'numeric names stay literal strings');
});

check('transport switch: the Windows pipe omits and skips the home; switching back restores it exactly', () => {
  const draft = p.pm2DraftFrom(STORED, 'api');
  const windows = { ...draft, transport: 'windows-pipe' };
  let errors = {};
  const pipe = p.buildPm2(STORED, windows, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(pipe.value), JSON.stringify({ transport: 'windows-pipe', namespace: 'Review NS' }));
  assert.equal('home' in pipe.value, false, 'the node refuses any home, even an empty one, with the pipe');
  errors = {};
  p.buildPm2(STORED, { ...windows, home: 'relative' }, errors);
  assert.deepEqual(errors, {}, 'a hidden home is not validated');
  assert.equal(windows.home, HOME, 'the draft keeps the typed home while it is hidden');
  errors = {};
  const back = p.buildPm2(STORED, { ...windows, transport: 'unix' }, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(back.value), JSON.stringify(STORED));
  errors = {};
  p.buildPm2({ transport: 'windows-pipe' }, { ...p.pm2DraftFrom({ transport: 'windows-pipe' }, 'api'), transport: 'unix' }, errors);
  assert.match(errors.pm2Home ?? '', /Enter the PM2 home/u, 'Windows to Unix asks for the home');
});

check('namespace: blank omits the field, never sends an empty one', () => {
  const cleared = p.buildPm2(STORED, { ...p.pm2DraftFrom(STORED, 'api'), namespace: '' }, {}).value;
  assert.equal(JSON.stringify(cleared), JSON.stringify({ transport: 'unix', home: HOME }));
  assert.equal('namespace' in p.buildPm2(undefined, fresh({ home: HOME }), {}).value, false);
  const errors = {};
  p.buildPm2(undefined, fresh({ home: HOME, namespace: '  ' }), errors);
  assert.match(errors.pm2Namespace ?? '', /not only spaces/u);
});

// The last two variants aren't admitted by this node (it refuses unknown keys and connections); they stand for a newer
// node's stored options, which an unedited save must not change.
check('no-op round trip: stored options come back byte for byte with key order, newer-node keys and connections included', () => {
  const variants = [
    STORED,
    { namespace: 'n', home: '/', transport: 'unix' },
    { transport: 'windows-pipe' },
    { transport: 'windows-pipe', namespace: `team ${ch(0x1f680)}` },
    { transport: 'unix', home: HOME, futureOption: { nested: [1, null] } },
    { transport: 'tcp-future', home: '/x', port: 9615 },
  ];
  for (const stored of variants) {
    const before = JSON.stringify(stored);
    const draft = p.pm2DraftFrom(stored, '0042');
    const errors = {};
    const built = p.buildPm2(stored, draft, errors);
    assert.deepEqual(errors, {}, before);
    assert.equal(JSON.stringify(built.value), before);
    assert.equal(built.target, '0042');
    assert.equal(JSON.stringify(stored), before, 'stored object not mutated');
  }
  assert.equal(p.pm2DraftFrom({ transport: 'tcp-future' }, 'api').transport, 'tcp-future', 'a newer connection is kept, not replaced by unix');
});

const FUTURE = { transport: 'tcp-future', home: '/x', port: 9615, futureOption: { nested: [1, null] } };
check('unknown connection: kept with its keys only as stored; choosing a known one drops them; typed or imported over anything else is a field error', () => {
  const before = JSON.stringify(FUTURE);
  const draft = p.pm2DraftFrom(FUTURE, 'api');
  let errors = {};
  const renamed = p.buildPm2(FUTURE, { ...draft, namespace: 'prod' }, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(renamed.value), JSON.stringify({ ...FUTURE, namespace: 'prod' }), 'an edit beside the stored unknown connection keeps its keys');
  errors = {};
  const unix = p.buildPm2(FUTURE, { ...draft, transport: 'unix' }, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(unix.value), JSON.stringify({ transport: 'unix', home: '/x' }), 'the newer connection’s keys go; the home stays and is checked');
  errors = {};
  p.buildPm2(FUTURE, { ...draft, transport: 'unix', home: 'x' }, errors);
  assert.match(errors.pm2Home ?? '', /absolute/u);
  const pipe = p.buildPm2(FUTURE, { ...draft, transport: 'windows-pipe', namespace: 'prod' }, {}).value;
  assert.equal(JSON.stringify(pipe), JSON.stringify({ transport: 'windows-pipe', namespace: 'prod' }));
  // The 2026-10-24 r6 review: the form shows no home with an unknown connection, and an import changes only the settings
  // the form shows, so a JSON import with the same unknown connection keeps the stored home, whatever home it carries.
  const homeless = { transport: 'tcp-future', port: 9615 };
  for (const [stored, json, expected, label] of [
    [FUTURE, { transport: 'tcp-future', home: '/elsewhere', port: 1 }, FUTURE, 'a different home'],
    [FUTURE, { transport: 'tcp-future' }, FUTURE, 'no home'],
    [FUTURE, { transport: 'tcp-future', home: '', namespace: 'prod' }, { ...FUTURE, namespace: 'prod' }, 'an empty home beside a namespace the form shows'],
    [homeless, { transport: 'tcp-future', home: '/pasted' }, homeless, 'a home over a stored connection without one'],
  ]) {
    const was = JSON.stringify(stored);
    errors = {};
    const built = p.buildPm2(stored, p.pm2DraftFrom(JSON.parse(JSON.stringify(json)), 'api'), errors);
    assert.deepEqual(errors, {}, label);
    assert.equal(JSON.stringify(built.value), JSON.stringify(expected), label);
    assert.equal(JSON.stringify(stored), was, `${label}: stored object not mutated`);
  }
  assert.equal(JSON.stringify(FUTURE), before, 'stored object not mutated');
  for (const [stored, label] of [[STORED, 'imported over a stored Unix monitor'], [undefined, 'a new monitor'], [FUTURE, 'a different unknown over a stored unknown']]) {
    errors = {};
    const built = p.buildPm2(stored, { ...p.pm2DraftFrom(stored, 'api'), transport: 'quic-future' }, errors);
    assert.deepEqual(Object.keys(errors), ['pm2Transport'], label);
    assert.match(errors.pm2Transport, /^Choose the Unix socket or the Windows named pipe\./u, label);
    assert.ok(errors.pm2Transport.includes(`${ch(0x201c)}quic-future${ch(0x201d)}`), label);
    assert.equal(built.value.transport, 'quic-future', 'the refused value is what would have been sent');
  }
  assert.equal(p.pm2KnownTransport('unix'), true);
  assert.equal(p.pm2KnownTransport('windows-pipe'), true);
  for (const transport of ['', 'Unix', 'tcp-future']) assert.equal(p.pm2KnownTransport(transport), false, transport);
});

const input = (extra = {}) => ({ name: 'API', type: 'pm2', target: 'api', intervalMs: 61_500, timeoutMs: 1_234, nodeIds: ['b', 'a'], enabled: true, pm2: structuredClone(STORED), ...extra });
check('generation comparison: name, connection, home and namespace count, through Go omitempty; metadata does not', () => {
  assert.deepEqual(checks.checkChanges(input(), input({ name: 'API 2', intervalMs: 30_000, retryIntervalMs: 2_500, tags: [{ tagId: 't' }], groupId: 'g', notificationIds: ['c'], repeatAfterMs: 60_000, nodeIds: ['a', 'b'] })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ pm2: { namespace: 'Review NS', home: HOME, transport: 'unix' } })), [], 'key order');
  assert.deepEqual(checks.checkChanges(input({ pm2: { transport: 'unix', home: HOME } }), input({ pm2: { transport: 'unix', home: HOME, namespace: '' } })), [], 'empty equals absent');
  for (const pm2 of [{ transport: 'windows-pipe', namespace: 'Review NS' }, { ...STORED, home: '/srv/.pm2' }, { ...STORED, namespace: 'review ns' }, { transport: 'unix', home: HOME }, { ...STORED, futureOption: 1 }]) {
    assert.deepEqual(checks.checkChanges(input(), input({ pm2 })), ['pm2'], JSON.stringify(pm2));
  }
  assert.deepEqual(checks.checkChanges(input(), input({ target: 'API' })), ['target'], 'names are case-sensitive');
  assert.deepEqual(checks.checkChanges(input(), input({ upsideDown: true })), ['upsideDown']);
  assert.deepEqual(checks.checkChangeWords(['target', 'pm2'], 'pm2'), ['process name', 'PM2 connection, home or namespace']);
  assert.deepEqual(checks.checkChangeWords(['target', 'steam'], 'steam'), ['server address', 'Steam API key or options']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'system-service'), ['service name']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'tcp'), ['target']);
  assert.equal(p.pm2Canonical({ transport: 'unix', home: HOME, namespace: '' }), p.pm2Canonical({ home: HOME, transport: 'unix' }));
});

check('pause/manual input keeps the pm2 object, its unknown keys and odd durations; drops runtime fields and nulls', () => {
  const stored = { ...input({ pm2: { ...structuredClone(STORED), futureOption: { nested: [1] } }, retries: 2, retryIntervalMs: 2_500, upsideDown: true }), id: 'm1', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', webhookUrl: null };
  const out = checks.inputOf(stored);
  assert.deepEqual(out.pm2, stored.pm2);
  for (const [key, value] of [['intervalMs', 61_500], ['timeoutMs', 1_234], ['retryIntervalMs', 2_500], ['retries', 2], ['target', 'api']]) assert.equal(out[key], value, key);
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', 'webhookUrl']) assert.equal(key in out, false, key);
  out.pm2.futureOption.nested.push(2);
  assert.deepEqual(stored.pm2.futureOption.nested, [1]);
});

check('masking: the home path alone is replaced, in a copy; credential-named newer keys are hidden', () => {
  const masked = p.pm2Masked({ ...STORED, futureToken: 'private', futureMode: 'fast' });
  assert.deepEqual(masked, { transport: 'unix', home: MASK, namespace: 'Review NS', futureToken: MASK, futureMode: 'fast' });
  assert.equal(STORED.home, HOME);
  assert.deepEqual(p.pm2Masked({ transport: 'windows-pipe' }), { transport: 'windows-pipe' }, 'no home key appears');
  assert.equal(p.pm2Masked(undefined), undefined);
});

check('JSON preview and import: the masked preview hides the home and is refused on import; the revealed copy fills the draft exactly', () => {
  const sent = input({ upsideDown: true, retries: 2 });
  const preview = previewModel.masked(sent);
  assert.equal(preview.pm2.home, MASK);
  assert.equal(preview.pm2.namespace, 'Review NS');
  assert.equal(preview.target, 'api');
  assert.ok(!JSON.stringify(preview).includes(HOME));
  assert.equal(sent.pm2.home, HOME, 'the preview masks a copy');
  assert.equal(fmt.maskedPath(JSON.parse(JSON.stringify(preview))), 'pm2.home', 'a masked copy is never imported as the real home');
  const revealed = JSON.parse(JSON.stringify(sent));
  assert.equal(fmt.maskedPath(revealed), undefined);
  const draft = p.pm2DraftFrom(revealed.pm2, revealed.target);
  assert.deepEqual(draft, { target: 'api', transport: 'unix', home: HOME, namespace: 'Review NS' });
  const built = p.buildPm2(undefined, draft, {});
  assert.equal(JSON.stringify(built.value), JSON.stringify(STORED));
  const windows = p.buildPm2(undefined, p.pm2DraftFrom({ transport: 'windows-pipe', home: '/pasted' }, 'api'), {}).value;
  assert.equal(JSON.stringify(windows), JSON.stringify({ transport: 'windows-pipe' }), 'an imported home is not sent with the pipe');
});

const monitor = (pm2, extra = {}) => ({ ...input({ pm2, ...extra }), id: 'm1', revision: 'r1', updatedAt: 1 });
const facts = (m, viewer = false, reveal = false) => checks.checkFacts(m, { viewer, reveal }).map(fact => ({ label: fact.label, html: renderToStaticMarkup(globalThis.React.createElement(globalThis.React.Fragment, null, fact.value)) }));
check('detail facts: exact name, namespace and rules; the home only when revealed; no latency', () => {
  const revealed = facts(monitor(STORED), false, true);
  assert.deepEqual(revealed.map(fact => fact.label), ['Read from', 'Connection', 'Up when', 'Down when', 'Pending when', 'Latency']);
  assert.match(revealed[0].html, />api</u);
  assert.match(revealed[0].html, /Review NS/u);
  assert.match(revealed[0].html, /Read-only/u);
  assert.ok(revealed[1].html.includes(HOME), 'revealed home');
  assert.match(revealed[3].html, /stopped, stopping, launching, errored or waiting to restart/u);
  assert.match(revealed[4].html, /6\.0\.8/u);
  assert.match(revealed[4].html, /never inverted/u);
  assert.match(revealed[5].html, /Not measured/u);
  const hidden = facts(monitor(STORED));
  assert.ok(hidden.every(fact => !fact.html.includes(HOME)), 'home hidden until revealed');
  assert.match(hidden[1].html, /set · hidden/u);
  assert.match(facts(monitor({ transport: 'unix', home: HOME })).at(0).html, /in any namespace/u);
  assert.match(facts(monitor({ transport: 'windows-pipe' })).find(fact => fact.label === 'Connection').html, /named pipe/u);
  assert.match(facts(monitor({ transport: 'tcp-future' })).find(fact => fact.label === 'Connection').html, /not known to this dashboard/u);
});

check('viewer facts: the name stays, connection and namespace are hidden; shared retries and inversion still apply', () => {
  const { pm2: _hidden, ...viewerMonitor } = monitor(STORED);
  const viewer = facts(viewerMonitor, true);
  assert.deepEqual(viewer.map(fact => fact.label), ['Read from', 'Up when', 'Down when', 'Pending when', 'Latency', 'Connection and namespace']);
  assert.match(viewer[0].html, />api</u);
  assert.match(viewer[5].html, /Hidden from viewers/u);
  assert.deepEqual(facts(monitor(STORED), true), viewer, 'viewer wording ignores any options present');
  assert.ok(viewer.every(fact => !fact.html.includes(HOME) && !fact.html.includes('Review NS')));
  const labels = facts(monitor(STORED, { retries: 2, retryIntervalMs: 2_500, upsideDown: true })).map(fact => fact.label);
  assert.deepEqual(labels.slice(-2), ['Retries', 'Result']);
});

// Copy from the 2026-10-24 r3 review, checked against the node: Linux compares the daemon's SO_PEERCRED user ID with its
// own effective one; the Windows pipe policy limits instance-creation and security-changing rights to Canary's account,
// SYSTEM and Administrators; the six PM2 statuses that count as down; worker capacity and failures stay pending.
const form = (d, stored, errors = {}) => renderToStaticMarkup(globalThis.React.createElement(Pm2Fields, { d: { ...p.pm2DraftFrom(undefined, 'api'), ...d }, set() {}, errors, disabled: false, stored }));
check('form help and facts: the account and container rules, the Windows pipe prerequisite, all six down states and every pending cause', () => {
  const unix = form({ home: HOME }, 'unix');
  assert.ok(unix.includes('Each selected node asks the PM2 daemon on its own machine'));
  assert.match(unix, /asks only a daemon whose effective user ID is Canary.s own: a daemon run by another account stays pending even when its socket is reachable/u);
  assert.match(unix, /the same account rule applies to the daemon.s user ID as mapped into the container/u);
  assert.match(unix, /for a node in a container, the path where that home is mounted inside the container/u);
  assert.ok(unix.includes(`${HOME}/rpc.sock`), 'the socket every node opens');
  assert.ok(!unix.includes('PM2 on Windows uses one fixed local pipe'));
  const pipe = form({ transport: 'windows-pipe', home: HOME }, 'unix');
  assert.ok(pipe.includes('PM2 on Windows uses one fixed local pipe'));
  assert.match(pipe, /From the pipe.s first creation, only Canary.s account, SYSTEM and Administrators may have been able to create instances of it or change its security/u);
  assert.match(pipe, /not which clients connect to it/u);
  assert.match(pipe, /can.t see their history/u);
  assert.match(pipe, /tightening a pipe that was once more open isn.t enough/u);
  assert.ok(!pipe.includes('PM2_HOME') && !pipe.includes(HOME), 'no home field or path for the pipe');
  const kept = form({ transport: 'tcp-future', home: '/x' }, 'tcp-future');
  assert.ok(kept.includes('This monitor is stored with the connection') && kept.includes('tcp-future') && kept.includes('kept as stored'));
  assert.ok(!kept.includes('can’t be saved'));
  for (const stored of ['unix', undefined, 'quic-future']) {
    const refused = form({ transport: 'tcp-future' }, stored);
    assert.ok(refused.includes('can’t be saved') && !refused.includes('kept as stored'), String(stored));
  }
  const error = 'Choose the Unix socket or the Windows named pipe.';
  assert.ok(form({ transport: 'tcp-future' }, 'unix', { pm2Transport: error }).includes(error), 'the field error is shown');
  assert.ok(!form({ transport: 'tcp-future' }, 'unix').includes(error));

  const shown = facts(monitor(STORED), false, true);
  const fact = label => shown.find(item => item.label === label).html;
  assert.match(fact('Down when'), /stopped, stopping, launching, errored or waiting to restart, or reports <span class="mono">one-launch-status<\/span>/u);
  assert.match(fact('Down when'), /no process with that name in the chosen namespace \(in any namespace when none is set\)/u);
  for (const cause of ['no daemon at that connection', 'a daemon run by another account than Canary', 'a pipe whose present owner or permissions don', 'no permission to open the socket',
    'a connection its system doesn', 'a system other than Linux and Windows (macOS and other Unix systems always stay pending)', 'isn’t PM2 6.0.8', 'an answer it can', 'a state it doesn',
    'check workers that are busy or fail', 'no answer within the timeout']) assert.ok(fact('Pending when').includes(cause), cause);
  assert.match(fact('Connection'), /effective user ID is Canary.s own/u);
  assert.match(facts(monitor({ transport: 'windows-pipe' })).find(item => item.label === 'Connection').html, /running as Canary.s own account/u);
});

console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
