// Pure protocol-19 checks for the Tailscale peer monitor kind: apps/web/src/tailscale.tsx (a mirror of the node's
// tailscalemonitor.Validate), views/TailscaleNotes.tsx and the facts (rendered to static markup), checks.tsx, and the
// monitor form through its cloneModel and previewModel seams in views/MonitorForm.tsx. Imports the source in place
// through one tsx namespace: no copy, no scratch, no network, no client run and no output files. The expected
// admissions come from the Go rule and net/netip as read in source; root's contract harness checks the same cases
// against a real node. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/tailscale-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'tailscale-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const ts = await load('tailscale.tsx');
const checks = await load('checks.tsx');
const specs = await load('serviceSpecs.tsx');
const { cloneModel, previewModel } = await load('views/MonitorForm.tsx');
const { TailscaleNotes } = await load('views/TailscaleNotes.tsx');
let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const ch = code => String.fromCodePoint(code);
const EARLIER = ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp', 'globalping', 'steam', 'system-service', 'pm2', 'postgres', 'mysql', 'sqlserver', 'oracledb',
  'mongodb', 'redis', 'docker', 'mqtt', 'rabbitmq', 'kafka-producer', 'grpc', 'sftp', 'ntp', 'snmp', 'radius', 'sip-options', 'push', 'manual'];
// Protocol 19 adds `browser`; a Tailscale monitor must never carry it either.
const OPTION_KEYS = ['http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2', 'browser', 'expectedStatusCodes'];
// 63 + 1 + 63 + 1 + 63 + 1 + 61 = 253 bytes, and the same length with a trailing dot.
const NAME_253 = `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`;
const DOTTED_253 = `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(60)}.`;

// The node's rule: tailscalemonitor.Validate (1-253 bytes; netip.ParseAddr without zone, unspecified or multicast; else
// ASCII labels of 1-63 letters, digits and hyphens without edge hyphens and one optional trailing dot).
const ACCEPTED = [
  'peer', 'build-server', 'Build-Server', 'build-server.example-tailnet.ts.net', 'build-server.example-tailnet.ts.net.', 'a', '0', '9a', 'a-b', 'xn--caf-dma',
  'a'.repeat(63), NAME_253, DOTTED_253,
  '100.101.102.103', '127.0.0.1', '1.2.3.4', '223.255.255.255', '240.0.0.1', '255.255.255.255',
  // Not strict IPv4 for net/netip, so they are read as names, whose labels allow digits.
  '01.2.3.4', '00.0.0.0', '1.2.3.256', '1.2.3', '1.2.3.4.5', '1.2.3.4.', '0.0.0.0.', '224.0.0.1.',
  'fd7a:115c:a1e0::1', 'FD7A:115C:A1E0::1', '::1', 'fe80::1', '2001:db8::', '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7::', '1:2:3:4:5:6:1.2.3.4', '::1.2.3.4',
  '::ffff:100.64.0.1', '0000:0000:0000:0000:0000:0000:0000:0001',
  // IsUnspecified is only 0.0.0.0 or ::; the IPv4-mapped form is a different address.
  '::ffff:0.0.0.0',
];
const REFUSED = [
  '', '.', '..', '.peer', 'peer..net', 'peer.net..', '-peer', 'peer-', 'a.-b', 'a.b-', 'pe_er', '*', 'peer*',
  'peer name', ' peer', 'peer ', 'peer\t', 'peer\n', `pe${ch(0)}er`, `peer${ch(0x2028)}`, `peer${ch(0xa0)}`, `${ch(0xe9)}.peer`, `caf${ch(0xe9)}`, `pe${String.fromCharCode(0xd800)}er`,
  'a'.repeat(64), `${NAME_253}d`, `${DOTTED_253.slice(0, -1)}d.`,
  '0.0.0.0', '224.0.0.1', '230.1.2.3', '239.255.255.255',
  '::', '::0', '0:0:0:0:0:0:0:0', '::0.0.0.0', 'ff02::1', 'FF02::1', '::ffff:224.0.0.1',
  '1:2:3:4:5:6:7:8:9', '1::2::3', '12345::1', ':::1', ':1', '1:2', '1:2:3:4:5:6:7:8::', '::01.2.3.4', '1:2:3:4:5:6:7:1.2.3.4', 'g::1',
  'fe80::1%eth0', 'fe80::1%', 'peer%', '%eth0', '[::1]', '[fd7a:115c:a1e0::1]:41641', 'fd7a:115c:a1e0::1/128',
  'peer:41641', '100.101.102.103:41641', 'https://peer', 'peer/x', 'peer\\x', 'user@peer', 'peer;id', '$(id)', '`id`', 'peer&&id', '--help', '-c',
];

// Protocol 19 also explicitly adds the real browser right after HTTP in the web category (validation.go: an HTTP(S)
// typed target with `browser` options), so the count moves from 30 to 31; it is filtered out of the earlier order too.
// Protocol 19 also explicitly adds GameDig right after Steam among game servers (validation.go: a `gamedig` options
// object whose target uses this same Tailscale peer rule), so the count moves from 31 to 32; it is filtered out too.
check('catalogue: 32 unique types, the 29 earlier ones in their order, tailscale-ping third in the local category, plus the real browser and GameDig', () => {
  const types = checks.CHECK_TYPES.map(entry => entry.type);
  assert.equal(types.length, 32);
  assert.equal(new Set(types).size, 32);
  assert.deepEqual(types.filter(type => type !== 'tailscale-ping' && type !== 'real-browser' && type !== 'gamedig'), EARLIER);
  assert.equal(types.indexOf('tailscale-ping'), types.indexOf('pm2') + 1);
  assert.deepEqual(checks.CHECK_TYPES.filter(entry => entry.category === 'local').map(entry => entry.type), ['system-service', 'pm2', 'tailscale-ping']);
  const entry = checks.checkType('tailscale-ping');
  assert.equal(entry.label, 'Tailscale ping');
  assert.equal(entry.short, 'ts');
  assert.equal(entry.targetLabel, 'Peer name or Tailscale address');
  assert.match(entry.targetHint, /no URL, port, brackets, zone, spaces or options/u);
  assert.equal(checks.typeLabel('tailscale-ping'), 'Tailscale ping');
  assert.equal(Object.keys(specs.SERVICE_PROTOCOL).length, 16);
});

check('typed target: network types unchanged, tailscale-ping after system-service, then real-browser; no service protocol and no TLS options', () => {
  assert.deepEqual([...checks.NETWORK_TYPES], ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp']);
  assert.deepEqual([...checks.TYPED_TARGET_TYPES].filter(type => type !== 'real-browser'), [...checks.NETWORK_TYPES, 'system-service', 'tailscale-ping']);
  assert.equal([...checks.TYPED_TARGET_TYPES].at(-1), 'real-browser');
  assert.equal(checks.isServiceType('tailscale-ping'), false);
  assert.equal(checks.tlsApplies('tailscale-ping', 'peer', undefined), false);
});

check('admission mirror: names, literal addresses and their limits as the node accepts them', () => {
  for (const target of ACCEPTED) {
    assert.equal(ts.tailscaleTargetAccepted(target), true, JSON.stringify(target));
    assert.equal(ts.tailscaleTargetProblem(target), undefined, JSON.stringify(target));
  }
  assert.equal(new TextEncoder().encode(NAME_253).length, 253);
  assert.equal(new TextEncoder().encode(DOTTED_253).length, 253);
});

check('admission mirror: what the node refuses, each with a reason', () => {
  for (const target of REFUSED) {
    assert.equal(ts.tailscaleTargetAccepted(target), false, JSON.stringify(target));
    assert.equal(typeof ts.tailscaleTargetProblem(target), 'string', JSON.stringify(target));
  }
  assert.equal(new Set([...ACCEPTED, ...REFUSED]).size, ACCEPTED.length + REFUSED.length, 'no case listed twice');
});

check('reasons name the actual problem', () => {
  const reason = target => ts.tailscaleTargetProblem(target);
  assert.match(reason(''), /^Enter the peer’s MagicDNS name or Tailscale address/u);
  assert.match(reason('peer name'), /without spaces or line breaks/u);
  assert.match(reason('https://peer'), /not a URL or a path/u);
  assert.match(reason('--help'), /not a command-line option/u);
  assert.match(reason('fe80::1%eth0'), /%zone/u);
  assert.match(reason('[::1]'), /brackets/u);
  assert.match(reason('user@peer'), /without a user name/u);
  assert.match(reason('0.0.0.0'), /aren’t a peer/u);
  assert.match(reason('::'), /aren’t a peer/u);
  assert.match(reason('ff02::1'), /multicast/u);
  assert.match(reason('::ffff:224.0.0.1'), /multicast/u);
  assert.match(reason('peer:41641'), /Remove the port/u);
  assert.match(reason('1::2::3'), /isn’t a valid IPv6 address/u);
  assert.match(reason(`${NAME_253}d`), /at most 253 characters/u);
  assert.match(reason(`caf${ch(0xe9)}`), /xn--/u);
  assert.match(reason('pe_er'), /“_” isn’t allowed/u);
  assert.match(reason('peer..net'), /empty part/u);
  assert.match(reason('a'.repeat(64)), /at most 63 characters/u);
  assert.match(reason('peer-'), /start or end with a hyphen/u);
});

const stored = (extra = {}) => ({
  id: 'm-ts', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', name: 'Build server', type: 'tailscale-ping', target: 'build-server.example-tailnet.ts.net',
  intervalMs: 61_500, timeoutMs: 4_321, nodeIds: ['b', 'a'], enabled: false, repeatAfterMs: 0, retries: 2, retryIntervalMs: 9_000, upsideDown: true,
  notificationIds: ['c1'], tags: [{ tagId: 't1', value: 'lab', public: true }], groupId: 'g-lab', futureTop: { kept: [1, null] }, ...extra,
});

check('form: a new Tailscale monitor sends a trimmed target and no options object', () => {
  const draft = { ...cloneModel.draftFrom(undefined, ['a']), name: 'Peer', type: 'tailscale-ping', targets: { 'tailscale-ping': '  peer  ' } };
  const built = cloneModel.build(draft, undefined, undefined);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(built.input.target, 'peer');
  for (const key of OPTION_KEYS) assert.equal(key in built.input, false, key);
  assert.deepEqual(built.input.nodeIds, ['a']);
  for (const target of ['peer:41641', '0.0.0.0', 'fe80::1%eth0', '']) {
    const refused = cloneModel.build({ ...draft, targets: { 'tailscale-ping': target } }, undefined, undefined);
    assert.equal(refused.input, undefined, target);
    assert.equal(refused.errors.target, ts.tailscaleTargetProblem(target.trim()), target);
  }
});

check('form: an edit loads the stored target and a rename keeps it, the durations, retries, inversion and unknown fields', () => {
  const m = stored();
  const draft = cloneModel.draftFrom(m, ['a']);
  assert.deepEqual(draft.targets, { 'tailscale-ping': m.target });
  const initial = structuredClone(draft);
  const built = cloneModel.build({ ...draft, name: 'Build server renamed' }, m, initial);
  assert.ok(built.input, JSON.stringify(built.errors));
  for (const [key, value] of [['target', m.target], ['intervalMs', 61_500], ['timeoutMs', 4_321], ['retries', 2], ['retryIntervalMs', 9_000], ['upsideDown', true], ['groupId', 'g-lab']]) {
    assert.deepEqual(built.input[key], value, key);
  }
  assert.deepEqual(built.input.futureTop, m.futureTop);
  assert.deepEqual(built.input.nodeIds, ['a', 'b']);
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', ...OPTION_KEYS]) assert.equal(key in built.input, false, key);
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), built.input), [], 'a rename keeps the check generation');
  const moved = cloneModel.build({ ...draft, targets: { 'tailscale-ping': '100.101.102.103' } }, m, initial);
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), moved.input), ['target']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'tailscale-ping'), ['peer name or address']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'tcp'), ['target']);
});

check('JSON import: a pasted object fills the target and ordinary fields; options and unknown keys don’t come along', () => {
  const m = stored();
  const pasted = { type: 'tailscale-ping', target: 'other-peer', tailscale: { path: 'C:/elsewhere/tailscale.exe' }, http: { method: 'POST' }, service: { redis: { address: 'x:1' } }, pastedTop: 1 };
  // As the form's importJson does: the pasted object over the stored one, read through draftFrom; the build's base stays the stored monitor.
  const draft = cloneModel.draftFrom({ ...m, ...pasted }, ['a']);
  const built = cloneModel.build(draft, m, cloneModel.draftFrom(m, ['a']));
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(built.input.target, 'other-peer');
  for (const key of ['tailscale', 'pastedTop', ...OPTION_KEYS]) assert.equal(key in built.input, false, key);
  assert.deepEqual(built.input.futureTop, m.futureTop, 'the stored monitor’s own unknown field stays');
});

check('JSON preview: the masked copy has nothing to hide and keeps the target', () => {
  const m = stored();
  const draft = cloneModel.draftFrom(m, ['a']);
  const built = cloneModel.build(draft, m, structuredClone(draft));
  const preview = previewModel.masked(built.input);
  assert.deepEqual(preview, built.input);
  assert.equal(preview.target, m.target);
});

const facts = (m, viewer = false) => checks.checkFacts(m, { viewer, reveal: false }).map(fact => ({ label: fact.label, html: renderToStaticMarkup(React.createElement(React.Fragment, null, fact.value)) }));

check('facts: how it asks, the three outcomes, latency only for a pong, fixed results; the same for viewers', () => {
  const m = stored({ upsideDown: false, retries: 0, retryIntervalMs: undefined });
  for (const viewer of [false, true]) {
    const list = facts(m, viewer);
    assert.deepEqual(list.map(fact => fact.label), ['Asked through', 'Up when', 'Down when', 'Pending when', 'Latency', 'Results']);
    const html = Object.fromEntries(list.map(fact => [fact.label, fact.html]));
    for (const said of ['tailscale ping', m.target, '/usr/bin/tailscale', '/usr/local/bin/tailscale', 'Program Files\\Tailscale\\tailscale.exe', 'rather than ICMP', 'No settings or credentials']) {
      assert.ok(html['Asked through'].includes(said), said);
    }
    assert.match(html['Up when'], /Exactly one valid pong comes back: direct, through a DERP relay or through a peer relay/u);
    assert.match(html['Down when'], /confirms its one ping timed out with no reply/u);
    for (const said of ['no Tailscale client at the fixed path', 'other than Linux and Windows', 'isn’t running, isn’t signed in', 'isn’t a peer, or is this node itself', 'doesn’t recognize', 'no free check worker', 'a cancelled check', 'no answer within the timeout', 'Pending is never inverted and never recovers an outage']) {
      assert.ok(html['Pending when'].includes(said), said);
    }
    assert.match(html.Latency, /for the pong; a dash for every other result/u);
    for (const said of [ts.TAILSCALE_SUMMARY.up, ts.TAILSCALE_SUMMARY.down, ts.TAILSCALE_SUMMARY.pending, 'stay on the node']) assert.ok(html.Results.includes(said), said);
  }
  assert.deepEqual(ts.TAILSCALE_SUMMARY, { up: 'Tailscale peer replied', down: 'Tailscale peer did not reply', pending: 'Tailscale peer status unavailable' });
  const inverted = facts(stored()).map(fact => fact.label);
  assert.ok(inverted.includes('Retries') && inverted.includes('Result'), 'the ordinary retry and inversion facts still apply');
});

check('form note: own client, fixed paths, container and Alpine prerequisites, timeout split; no settings rendered', () => {
  const html = renderToStaticMarkup(React.createElement(TailscaleNotes));
  for (const said of [
    'Each selected node asks its own Tailscale client, never another machine’s.', '<span class="mono">tailscale ping</span>', 'not an ICMP ping', 'starts no daemon and never falls back to ICMP or another check',
    'Up is exactly one pong, direct or through a DERP or peer relay', 'down is the client confirming that its one ping timed out with no reply', 'Pending is never inverted and never recovers an outage.',
    'Each node needs the Tailscale client and a running, signed-in daemon.', '/usr/bin/tailscale', '/usr/local/bin/tailscale', 'Program Files\\Tailscale\\tailscale.exe',
    'No other path is used, and none can be set here.', 'Other operating systems stay pending.', 'The ordinary Alpine node image has neither the client nor the daemon',
    'a container gets no access to the host’s Tailscale by itself', 'the client, a daemon and the permission to use it', 'three quarters of the timeout', 'If the whole timeout passes first, the result is pending.',
  ]) assert.ok(html.includes(said), said);
  assert.equal((html.match(/role="note"/gu) ?? []).length, 2);
  assert.doesNotMatch(html, /<(input|select|textarea|button)\b/u, 'the note offers no path, command, socket or credential setting');
});

check('sources: no raw line or paragraph separators, NEL or BOM in the new or edited files', () => {
  const bad = [0x2028, 0x2029, 0x85, 0xfeff].map(ch);
  for (const file of ['tailscale.tsx', 'views/TailscaleNotes.tsx', 'checks.tsx', 'views/MonitorForm.tsx', 'views/MonitorDetail.tsx', 'views/Config.tsx']) {
    const text = readFileSync(resolve(root, 'apps/web/src', file), 'utf8');
    for (const char of bad) assert.equal(text.includes(char), false, `${file} contains U+${char.codePointAt(0).toString(16).padStart(4, '0')}`);
  }
});

console.log(failed ? `${failed} FAILED` : 'ALL PASSED');
process.exit(failed ? 1 : 0);
