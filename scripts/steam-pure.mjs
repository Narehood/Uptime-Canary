// Pure protocol-17 checks for the Steam game server monitor kind: apps/web/src/steam.tsx and checks.tsx.
// Imports the source in place: no copy, no scratch, no network, no output files. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/steam-pure.mjs   (exit 1 on any failure)
// MonitorForm draftFrom/build/masked and Config monitorRows aren't exported; their rename, pause and conflict round trips
// belong to root's contract harness, which appends those exports.
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
globalThis.React ??= reactModule.default ?? reactModule;
const { tsImport } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const load = file => tsImport(pathToFileURL(resolve(root, 'apps/web/src', file)).href, { parentURL: import.meta.url, tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const st = await load('steam.tsx');
const checks = await load('checks.tsx');
const specs = await load('serviceSpecs.tsx');
let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const MASK = String.fromCodePoint(0x2022).repeat(3);
const KEY = 'ABCDEF0123456789ABCDEF0123456789';
const EARLIER = ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp', 'globalping', 'system-service', 'postgres', 'mysql', 'sqlserver', 'oracledb', 'mongodb', 'redis',
  'docker', 'mqtt', 'rabbitmq', 'kafka-producer', 'grpc', 'sftp', 'ntp', 'snmp', 'radius', 'sip-options', 'push', 'manual'];

// Protocol 18 adds PM2; the earlier-type order and all Steam rules stay intact.
check('catalogue: 29 unique types, the 27 earlier ones in their order, plus Steam and protocol-18 PM2', () => {
  const types = checks.CHECK_TYPES.map(entry => entry.type);
  assert.equal(types.length, 29);
  assert.equal(new Set(types).size, 29);
  assert.deepEqual(types.filter(type => type !== 'steam' && type !== 'pm2'), EARLIER);
  assert.equal(Object.keys(specs.SERVICE_PROTOCOL).length, 16);
  const entry = checks.checkType('steam');
  assert.equal(entry.label, 'Steam game server');
  assert.equal(entry.category, 'game');
  assert.equal(checks.CATEGORY_LABEL.game, 'Game servers');
  assert.equal(checks.CHECK_TYPES.filter(other => other.category === 'game').length, 1);
  assert.equal(checks.CHECK_TYPES.filter(other => other.category === 'local').length, 2);
  assert.equal(entry.targetLabel, undefined, 'the Steam form owns its address field');
});

check('typed-target sets unchanged; steam is not a service protocol and gets no TLS options', () => {
  assert.deepEqual([...checks.NETWORK_TYPES], ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp']);
  assert.deepEqual([...checks.TYPED_TARGET_TYPES], [...checks.NETWORK_TYPES, 'system-service']);
  assert.equal(checks.isServiceType('steam'), false);
  assert.equal(checks.tlsApplies('steam', 'game.example.net:27015', undefined), false);
});

check('target: host:port and [IPv6]:port as Go SplitHostPort + ICMP host rules + canonical port accept them', () => {
  for (const target of ['game.example.net:27015', '192.0.2.10:27015', '[2001:db8::10]:27015', '[::ffff:192.0.2.1]:1', 'a.b.:65535', 'xn--bcher-kva.example:27016',
    '[example.com]:27015', '  game.example.net:27015  ', 'localhost:1', 'A-1.example:2']) {
    assert.equal(st.steamTargetProblem(target), undefined, target);
  }
  const reason = target => st.steamTargetProblem(target) ?? '';
  assert.match(reason(''), /address and port/u);
  assert.match(reason('game.example.net'), /host:port/u);
  assert.match(reason('::1:27015'), /host:port/u);
  assert.match(reason('[::1]27015'), /host:port/u);
  assert.match(reason('a[b]:1'), /host:port/u);
  assert.match(reason(':27015'), /before the port/u);
  assert.match(reason('[fe80::1%eth0]:1'), /zone/u);
  assert.match(reason(`b${String.fromCodePoint(0xfc)}cher.example:1`), /punycode/u);
  assert.match(reason('under_score.example:1'), /host name/u);
  assert.match(reason('-bad.example:1'), /host name/u);
  for (const port of ['0', '027015', '65536', '+1', '', '1.0', ' 1']) assert.match(reason(`game.example.net:${port}`), /port|host:port/u, port);
  assert.match(reason(`${'a'.repeat(316)}.com:1`), /320/u);
});

check('API key: 1-256 printable non-whitespace ASCII, required', () => {
  for (const key of [KEY, '!', '~'.repeat(256), 'a-b_c.d']) assert.equal(st.steamKeyProblem(key), undefined, key);
  assert.match(st.steamKeyProblem(''), /Enter the Steam Web API key/u);
  assert.match(st.steamKeyProblem('abc def'), /spaces/u);
  assert.match(st.steamKeyProblem('abc\tdef'), /spaces/u);
  assert.match(st.steamKeyProblem(`k${String.fromCodePoint(0x43a)}`), /ASCII/u);
  assert.match(st.steamKeyProblem('a'.repeat(257)), /256/u);
});

check('API address: what the node accepts', () => {
  for (const [url, insecure] of [['', false], ['https://api.steampowered.com', false], ['https://steam-proxy.internal:8443/steam/', false], ['https://[2001:db8::1]:443/p', false],
    ['HTTPS://API.EXAMPLE.COM', false], ['https://h.example/a;b=c,d@e+f$g&h~i', false], ['https://h.example//', false], ['https://h.example/#', false], ['https://h.example/a%22b', false],
    ['https://h.example:0443', false], ['http://127.0.0.1:8080', true], ['https://h.example', true], ['https://api.example.com.', false]]) {
    assert.equal(st.steamApiProblem(url, insecure), undefined, url);
  }
});

check('API address: what the node refuses, including a dangling colon and escaped or dot-segment paths', () => {
  const reason = (url, insecure = false) => st.steamApiProblem(url, insecure) ?? '';
  assert.match(reason('http://api.example.com'), /Allow plain HTTP/u);
  assert.match(reason('ftp://api.example.com'), /https/u);
  assert.match(reason('https:api.example.com'), /complete/u);
  assert.match(reason('api.example.com'), /complete/u);
  assert.match(reason('https:///steam'), /host/u);
  assert.match(reason('https://user:pw@api.example.com'), /user:password/u);
  assert.match(reason('https://api.example.com/?key=1'), /query/u);
  assert.match(reason('https://api.example.com/?'), /query/u);
  assert.match(reason('https://api.example.com/#frag'), /fragment/u);
  assert.match(reason('https://api.example.com:/'), /port/u);
  assert.match(reason('https://api.example.com:'), /port/u);
  assert.match(reason('https://[::1]:/'), /port/u);
  assert.match(reason('https://api.example.com:0'), /port/u);
  assert.match(reason('https://api.example.com:65536'), /port/u);
  assert.match(reason('https://[fe80::1%25en0]/'), /zone|escape/u);
  assert.match(reason('https://exa_mple.com'), /host/u);
  assert.match(reason(`https://b${String.fromCodePoint(0xfc)}cher.de`), /ASCII/u);
  assert.match(reason('https://api.example.com/a b'), /spaces/u);
  assert.match(reason('https://api.example.com\\steam'), /backslash/u);
  for (const path of ['/a/../b', '/a/./b', '/a//b', '/..', '/a/.']) assert.match(reason(`https://api.example.com${path}`), /segments/u, path);
  for (const path of ['/a%2Fb', '/a%41', '/a%2f', '/a%20b', '/a%zz', '/a!b', '/a*b', "/a'b", '/a(b)']) assert.match(reason(`https://api.example.com${path}`), /%-escapes/u, path);
  // Original new fixture expected this clean method-named base prefix to be rejected.
  // The Go base/prefix contract accepts it; the metadata round-trip case below
  // checks that the interface does not add a stricter admission rule.
  assert.match(reason(`https://h.example/${'a'.repeat(2048)}`), /2,048/u);
});

const LINE = 'MIIB'.padEnd(64, 'A');
const PEM = `-----BEGIN CERTIFICATE-----\n${LINE}\n${LINE.slice(0, 20)}==\n-----END CERTIFICATE-----`;
check('API CA: PEM certificate blocks only, at most 128 KiB, any count', () => {
  for (const pem of ['', '  \n', PEM, `${PEM}\n${PEM}\r\n`, PEM.replace(/\n/gu, '\r\n'), `\n\n${PEM}\n\n`]) assert.equal(st.steamCaProblem(pem), undefined, JSON.stringify(pem.slice(0, 40)));
  assert.match(st.steamCaProblem(`-----BEGIN PRIVATE KEY-----\n${LINE}\n-----END PRIVATE KEY-----`), /PEM certificates only/u);
  assert.match(st.steamCaProblem(`${PEM}\nnote\n${PEM}`), /PEM certificates only/u);
  assert.match(st.steamCaProblem(`-----BEGIN CERTIFICATE-----\nProc-Type: 4,ENCRYPTED\n${LINE}\n-----END CERTIFICATE-----`), /PEM certificates only/u);
  assert.match(st.steamCaProblem(`-----BEGIN CERTIFICATE-----\n${LINE}-----END CERTIFICATE-----`), /PEM certificates only/u);
  const big = Array.from({ length: 2100 }, () => PEM).join('\n');
  assert.ok(new TextEncoder().encode(big).length > 128 * 1024);
  assert.match(st.steamCaProblem(big), /128 KiB/u);
});

const fresh = (change = {}) => ({ ...st.steamDraftFrom(undefined, ''), target: 'game.example.net:27015', apiKey: KEY, ...change });
check('builder: a new object sends apiKey and omits every Go-omitempty false or empty field; defaults measure, 32 bytes', () => {
  const draft = st.steamDraftFrom(undefined, '');
  assert.deepEqual(draft, { target: '', apiKey: '', apiUrl: '', ca: '', insecure: false, failureDown: false, measure: true, size: '' });
  const errors = {};
  const built = st.buildSteam(undefined, fresh(), errors);
  assert.deepEqual(errors, {});
  assert.equal(built.target, 'game.example.net:27015');
  assert.equal(JSON.stringify(built.value), JSON.stringify({ apiKey: KEY }));
  const all = st.buildSteam(undefined, fresh({ apiUrl: ' https://steam-proxy.internal/steam/ ', ca: PEM, failureDown: true, measure: false, size: '0', apiKey: ` ${KEY}\n` }), errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(all.value), JSON.stringify({ apiKey: KEY, apiUrl: 'https://steam-proxy.internal/steam/', apiCa: PEM, apiFailureIsDown: true, skipLatency: true, packetSize: 0 }));
});

check('builder: zero payload kept, empty size omitted, size bounds; errors land on their fields', () => {
  assert.equal(st.buildSteam(undefined, fresh({ size: '0' }), {}).value.packetSize, 0);
  assert.equal('packetSize' in st.buildSteam(undefined, fresh({ size: '  ' }), {}).value, false);
  assert.equal(st.buildSteam(undefined, fresh({ size: '65500' }), {}).value.packetSize, 65500);
  for (const size of ['-1', '1.5', '65501', 'abc']) { const errors = {}; st.buildSteam(undefined, fresh({ size }), errors); assert.match(errors.steamSize ?? '', /0 to 65,500/u, size); }
  const errors = {};
  st.buildSteam(undefined, fresh({ target: 'nope', apiKey: '', apiUrl: 'http://x.example', ca: 'junk' }), errors);
  assert.deepEqual(Object.keys(errors).sort(), ['steamApi', 'steamCa', 'steamKey', 'steamTarget']);
  assert.deepEqual(Object.keys(st.STEAM_FIELD_LABEL).sort(), ['steamApi', 'steamCa', 'steamKey', 'steamSize', 'steamTarget']);
  assert.equal(st.STEAM_SECTION.steamApi, 'steamApi');
  assert.equal(st.STEAM_SECTION.steamCa, 'steamApi');
});

const STORED = { apiKey: KEY, apiUrl: 'http://127.0.0.1:8080/steam', apiCa: PEM, allowInsecureApi: true, apiFailureIsDown: true, skipLatency: true, packetSize: 0, futureOption: { nested: [1, null] } };
check('builder: an unedited stored object round-trips exactly, unknown keys and key order included; edits keep siblings', () => {
  const draft = st.steamDraftFrom(STORED, '[2001:db8::10]:27015');
  assert.deepEqual(draft, { target: '[2001:db8::10]:27015', apiKey: KEY, apiUrl: STORED.apiUrl, ca: PEM, insecure: true, failureDown: true, measure: false, size: '0' });
  const errors = {};
  const built = st.buildSteam(STORED, draft, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(built.value), JSON.stringify(STORED));
  assert.equal(built.target, '[2001:db8::10]:27015');
  const edited = st.buildSteam(STORED, { ...draft, measure: true, insecure: false, apiUrl: '', size: '' }, {}).value;
  assert.equal(JSON.stringify(edited), JSON.stringify({ apiKey: KEY, apiCa: PEM, apiFailureIsDown: true, futureOption: { nested: [1, null] } }));
  assert.deepEqual(STORED.futureOption, { nested: [1, null] }, 'stored object not mutated');
});

const input = (extra = {}) => ({ name: 'CS server', type: 'steam', target: 'game.example.net:27015', intervalMs: 60000, timeoutMs: 10000, nodeIds: ['b', 'a'], enabled: true, steam: { apiKey: KEY }, ...extra });
check('generation comparison: every Steam option counts, through Go omitempty; metadata does not', () => {
  assert.deepEqual(checks.checkChanges(input(), input({ name: 'CS 2', intervalMs: 30000, retryIntervalMs: 5000, tags: [{ tagId: 't' }], groupId: 'g', notificationIds: ['c'], repeatAfterMs: 60000, nodeIds: ['a', 'b'] })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ steam: { apiKey: 'OTHER' } })), ['steam']);
  assert.deepEqual(checks.checkChanges(input(), input({ steam: { apiKey: KEY, skipLatency: false, apiFailureIsDown: false, allowInsecureApi: false, apiUrl: '', apiCa: '' } })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ steam: { apiKey: KEY, packetSize: 0 } })), ['steam']);
  assert.deepEqual(checks.checkChanges(input({ steam: { apiKey: KEY, packetSize: 32 } }), input({ steam: { apiKey: KEY } })), ['steam']);
  for (const change of [{ skipLatency: true }, { apiFailureIsDown: true }, { allowInsecureApi: true }, { apiUrl: 'https://p.example' }, { apiCa: PEM }, { futureOption: 1 }]) {
    assert.deepEqual(checks.checkChanges(input(), input({ steam: { apiKey: KEY, ...change } })), ['steam'], JSON.stringify(change));
  }
  assert.deepEqual(checks.checkChanges(input({ steam: { skipLatency: true, apiKey: KEY } }), input({ steam: { apiKey: KEY, skipLatency: true } })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ target: 'game.example.net:27016' })), ['target']);
  assert.equal(st.steamCanonical({ apiKey: '' }), st.steamCanonical({}));
  assert.deepEqual(checks.checkChangeWords(['target', 'steam'], 'steam'), ['server address', 'Steam API key or options']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'system-service'), ['service name']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'tcp'), ['target']);
});

check('pause/manual input keeps the steam object, its unknown keys and unknown siblings; drops runtime fields and nulls', () => {
  const stored = { ...input({ steam: structuredClone(STORED), upsideDown: true, retries: 2 }), id: 'm1', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', webhookUrl: null, steamFuture: { x: true } };
  const out = checks.inputOf(stored);
  assert.deepEqual(out.steam, STORED);
  assert.deepEqual(out.steamFuture, { x: true });
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', 'webhookUrl']) assert.equal(key in out, false, key);
  out.steam.futureOption.nested.push(2);
  assert.deepEqual(stored.steam.futureOption.nested, [1, null]);
});

check('masking: the API key alone is replaced, in a copy', () => {
  const masked = st.steamMasked(STORED);
  assert.equal(masked.apiKey, MASK);
  assert.deepEqual({ ...masked, apiKey: KEY }, STORED);
  assert.equal(STORED.apiKey, KEY);
  assert.equal(st.steamMasked({ apiKey: '' }).apiKey, '');
  assert.equal(st.steamMasked(undefined), undefined);
  assert.ok(!JSON.stringify(st.steamMasked(STORED)).includes(KEY));
});

const monitor = (steam, extra = {}) => ({ ...input({ steam, ...extra }), id: 'm1', revision: 'r1', updatedAt: 1 });
const facts = (m, viewer = false, reveal = false) => checks.checkFacts(m, { viewer, reveal }).map(fact => ({ label: fact.label, html: renderToStaticMarkup(globalThis.React.createElement(globalThis.React.Fragment, null, fact.value)) }));
check('detail facts: directory membership and ICMP labelled; the key never rendered, even revealed', () => {
  const editor = facts(monitor(STORED), false, true);
  assert.deepEqual(editor.map(fact => fact.label), ['Steam directory', 'Up when', 'Down when', 'If the Steam API fails', 'ICMP latency', 'Steam API', 'API key', 'Sent to the API']);
  assert.match(editor[0].html, /not a game-protocol query/u);
  assert.match(editor[0].html, />game\.example\.net:27015</u);
  assert.match(editor[3].html, /Down/u);
  assert.match(editor[4].html, /Not measured/u);
  assert.match(editor[5].html, /plain HTTP allowed/u);
  assert.match(editor[5].html, /127\.0\.0\.1:8080\/steam/u, 'revealed API address');
  assert.ok(editor.every(fact => !fact.html.includes(KEY)), 'key never rendered');
  const measured = facts(monitor({ apiKey: KEY, packetSize: 0 }));
  assert.match(measured.find(fact => fact.label === 'ICMP latency').html, /0 bytes of payload/u);
  assert.match(measured.find(fact => fact.label === 'ICMP latency').html, /Never the Steam API/u);
  assert.match(measured.find(fact => fact.label === 'If the Steam API fails').html, /Pending/u);
  assert.match(facts(monitor({ apiKey: KEY })).find(fact => fact.label === 'ICMP latency').html, /32 bytes/u);
  assert.match(facts(monitor({ apiKey: KEY })).find(fact => fact.label === 'Steam API').html, /api\.steampowered\.com \(default\)/u);
  assert.ok(!facts(monitor(STORED), false, false).find(fact => fact.label === 'Steam API').html.includes('/steam'), 'masked API path');
});

check('viewer facts: the address stays, options are hidden; shared retries and inversion still apply', () => {
  const { steam: _hidden, ...viewerMonitor } = monitor(STORED);
  const viewer = facts(viewerMonitor, true);
  assert.deepEqual(viewer.map(fact => fact.label), ['Steam directory', 'Up when', 'ICMP latency', 'API, key and options']);
  assert.match(viewer[3].html, /Hidden from viewers/u);
  assert.deepEqual(facts(monitor(STORED), true), viewer, 'viewer wording ignores any options present');
  const labels = facts(monitor({ apiKey: KEY }, { retries: 2, retryIntervalMs: 5000, upsideDown: true })).map(fact => fact.label);
  assert.deepEqual(labels.slice(-2), ['Retries', 'Result']);
});


check('Go-admitted API prefixes remain editable without changing stored options', () => {
  for (const suffix of ['/IGameServersService/GetServerList/v1/', '/internal/IGameServersService/GetServerList/v1', '/igameserversservice/getserverlist/V1/']) {
    const stored = { apiKey: KEY, apiUrl: 'https://example.test' + suffix };
    const draft = st.steamDraftFrom(stored, '127.0.0.9:27015');
    const errors = {};
    const built = st.buildSteam(stored, draft, errors);
    assert.deepEqual(errors, {}, suffix);
    assert.deepEqual(built.value, stored, 'metadata edit keeps the entire API options object');
    assert.equal(built.target, '127.0.0.9:27015');
  }
});

check('Go-whitespace-only CA remains editable and byte-preserved', () => {
  const stored = { apiKey: KEY, apiCa: String.fromCodePoint(0x85) };
  const errors = {};
  const built = st.buildSteam(stored, st.steamDraftFrom(stored, '127.0.0.9:27015'), errors);
  assert.deepEqual(errors, {});
  assert.deepEqual(built.value, stored);
  assert.notEqual(st.steamCaProblem(' '.repeat(st.STEAM_CA_MAX + 1)), undefined, 'the byte limit applies before empty-whitespace handling');
  assert.notEqual(st.steamCaProblem(String.fromCodePoint(0xfeff)), undefined, 'BOM is not Go whitespace or a certificate');
});

console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
