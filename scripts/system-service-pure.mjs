// Pure protocol-16 checks for the system-service monitor kind: apps/web/src/systemService.ts and checks.tsx.
// Imports the source in place: no copy, no scratch, no network, no output files. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/system-service-pure.mjs   (exit 1 on any failure)
// The form builder (MonitorForm draftFrom/build) isn't exported; its rename round trip belongs to root's contract harness.
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
const ss = await load('systemService.ts');
const checks = await load('checks.tsx');
const specs = await load('serviceSpecs.tsx');
let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const EARLIER = ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp', 'globalping', 'postgres', 'mysql', 'sqlserver', 'oracledb', 'mongodb', 'redis',
  'docker', 'mqtt', 'rabbitmq', 'kafka-producer', 'grpc', 'sftp', 'ntp', 'snmp', 'radius', 'sip-options', 'push', 'manual'];

// Protocol 17 explicitly adds Steam; retain every earlier catalogue/order assertion.
check('catalogue: 28 unique types, the 26 earlier ones in their order, plus system-service and Steam', () => {
  const types = checks.CHECK_TYPES.map(entry => entry.type);
  assert.equal(types.length, 28);
  assert.equal(new Set(types).size, 28);
  assert.ok(types.includes('steam'));
  assert.deepEqual(types.filter(type => type !== 'system-service' && type !== 'steam'), EARLIER);
  assert.equal(Object.keys(specs.SERVICE_PROTOCOL).length, 16);
});

check('catalogue entry: label, target label, examples, its own local category', () => {
  const entry = checks.checkType('system-service');
  assert.equal(entry.label, 'System service');
  assert.equal(entry.targetLabel, 'Internal service name');
  assert.equal(entry.category, 'local');
  assert.match(entry.placeholder, /sshd\.service/u);
  assert.match(entry.placeholder, /Spooler/u);
  assert.ok(checks.CATEGORY_LABEL.local);
  assert.equal(checks.CHECK_TYPES.filter(other => other.category === 'local').length, 1);
  assert.equal(checks.typeLabel('system-service'), 'System service');
});

check('typed target: system-service joins the 7 network types; not a service-protocol type', () => {
  assert.deepEqual([...checks.NETWORK_TYPES], ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp']);
  assert.deepEqual([...checks.TYPED_TARGET_TYPES], [...checks.NETWORK_TYPES, 'system-service']);
  assert.equal(checks.isServiceType('system-service'), false);
  assert.equal(checks.tlsApplies('system-service', 'sshd', undefined), false);
});

// The node's systemservice.Config.Validate, byte for byte.
const goValid = name => {
  const raw = Buffer.from(name, 'utf8');
  if (raw.length === 0 || raw.length > 256) return false;
  return [...raw].every((ch, i) => {
    const alpha = (ch >= 0x61 && ch <= 0x7a) || (ch >= 0x41 && ch <= 0x5a) || (ch >= 0x30 && ch <= 0x39) || ch === 0x5f;
    return alpha || (i !== 0 && (ch === 0x2e || ch === 0x2d || ch === 0x40));
  });
};

check('name rule equals the node rule for every code point 0–0x17F (first and later), and astral text', () => {
  const samples = [];
  for (let c = 0; c <= 0x17f; c++) { const ch = String.fromCodePoint(c); samples.push(ch, `a${ch}`, `a${ch}b`); }
  samples.push('a\u{1F600}', '\u{1F600}', 'a'.repeat(255), 'a'.repeat(256), 'a'.repeat(257), '_'.repeat(256), `a${'.'.repeat(255)}`, `a${'-'.repeat(256)}`);
  for (const name of samples) assert.equal(ss.serviceNameProblem(name) === undefined, goValid(name), JSON.stringify(name));
});

check('accepts realistic names', () => {
  for (const name of ['sshd', 'sshd.service', 'Spooler', 'getty@tty1.service', 'systemd-journald.service', 'wpa_supplicant', 'MSSQLSERVER', 'com.docker.service', 'CDPUserSvc_1a2b3', '_x', '0']) {
    assert.equal(ss.serviceNameProblem(name), undefined, name);
  }
});

check('rejects with a specific reason', () => {
  const reason = name => ss.serviceNameProblem(name) ?? '';
  assert.match(reason(''), /Enter the internal service name/u);
  assert.match(reason('Print Spooler'), /display name/u);
  assert.match(reason('sshd\t'), /no spaces/u);
  assert.match(reason('/usr/sbin/sshd'), /not a path/u);
  assert.match(reason('C:\\Windows\\x'), /ASCII|not a path/u);
  assert.match(reason('ssh*'), /Patterns/u);
  assert.match(reason('ss[hd]'), /Patterns/u);
  assert.match(reason('-sshd'), /Start with a letter/u);
  assert.match(reason('.hidden'), /Start with a letter/u);
  assert.match(reason('"sshd"'), /Remove the quotes/u);
  assert.match(reason("sshd'"), /Remove the quotes/u);
  assert.match(reason('ssh,d'), /“,” isn’t allowed/u);
  assert.match(reason('MSSQL$SQLEXPRESS'), /“\$” isn’t allowed/u);
  assert.match(reason('sshé'), /ASCII/u);
  assert.match(reason('a'.repeat(257)), /at most 256/u);
});

check('systemd unit: .service appended only when absent, case-sensitively like the node', () => {
  assert.equal(ss.systemdUnit('sshd'), 'sshd.service');
  assert.equal(ss.systemdUnit('sshd.service'), 'sshd.service');
  assert.equal(ss.systemdUnit('getty@tty1'), 'getty@tty1.service');
  assert.equal(ss.systemdUnit('sshd.Service'), 'sshd.Service.service');
  assert.equal(ss.systemdUnit('cron.timer'), 'cron.timer.service');
});

const input = (extra = {}) => ({ name: 'SSH', type: 'system-service', target: 'sshd', intervalMs: 60000, timeoutMs: 5000, nodeIds: ['b', 'a'], enabled: true, ...extra });

check('generation comparison mirrors sameCheck for type and target', () => {
  assert.deepEqual(checks.checkChanges(input(), input({ name: 'SSH daemon', intervalMs: 30000, retryIntervalMs: 5000, tags: [{ tagId: 't' }], groupId: 'g', notificationIds: ['c'], repeatAfterMs: 60000 })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ nodeIds: ['a', 'b'] })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ target: 'sshd.service' })), ['target']);
  assert.deepEqual(checks.checkChanges(input({ type: 'tcp', target: '127.0.0.1:22' }), input()), ['type', 'target']);
  assert.deepEqual(checks.checkChanges(input(), input({ timeoutMs: 10000, upsideDown: true, retries: 2, enabled: false, nodeIds: ['a'] })), ['timeoutMs', 'enabled', 'nodeIds', 'upsideDown', 'retries']);
  assert.deepEqual(checks.checkChanges(input({ upsideDown: false, retries: 0 }), input()), []);
});

check('change words: a system service target is its service name; other types unchanged', () => {
  assert.deepEqual(checks.checkChangeWords(['type', 'target'], 'system-service'), ['check type', 'service name']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'tcp'), ['target']);
  assert.deepEqual(checks.checkChangeWords(['target']), ['target']);
  assert.deepEqual(checks.checkChangeWords(['globalping']), ['Globalping options']);
});

check('inputOf keeps the target bytes and unknown future fields, drops runtime fields and nulls', () => {
  const stored = { ...input({ target: 'getty@tty1.service', upsideDown: true, retries: 3, retryIntervalMs: 7000, tags: [{ tagId: 't', value: 'x', public: true }] }),
    id: 'm1', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', webhookUrl: null, futureTop: 'kept', systemService: { future: { nested: [1, null] } } };
  const out = checks.inputOf(stored);
  assert.equal(out.target, 'getty@tty1.service');
  assert.deepEqual(out.systemService, { future: { nested: [1, null] } });
  assert.equal(out.futureTop, 'kept');
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', 'webhookUrl']) assert.equal(key in out, false, key);
  out.systemService.future.nested.push(2);
  assert.deepEqual(stored.systemService.future.nested, [1, null]);
});

const facts = (m, viewer = false) => checks.checkFacts(m, { viewer, reveal: false }).map(fact => ({ label: fact.label, html: renderToStaticMarkup(globalThis.React.createElement(globalThis.React.Fragment, null, fact.value)) }));

check('detail facts: own-OS wording, both platform names, pending causes, dash latency; same for viewers', () => {
  const editor = facts({ ...input(), id: 'm1', revision: 'r1', updatedAt: 1 });
  assert.deepEqual(editor.map(fact => fact.label), ['Read from', 'Up when', 'Down when', 'Pending when', 'Latency']);
  const read = editor[0].html;
  assert.match(read, /own operating system, never another host/u);
  assert.match(read, />sshd\.service</u);
  assert.match(read, />sshd</u);
  assert.match(editor[3].html, /ordinary container/u);
  assert.match(editor[3].html, /never recovers/u);
  assert.match(editor[4].html, /dash/u);
  assert.deepEqual(facts({ ...input(), id: 'm1', revision: 'r1', updatedAt: 1 }, true), editor);
  assert.ok(!editor.some(fact => /Hidden|•••/u.test(fact.html)));
});

check('detail facts: shared retries and inversion still apply', () => {
  const labels = facts({ ...input({ retries: 2, retryIntervalMs: 5000, upsideDown: true }), id: 'm1', revision: 'r1', updatedAt: 1 }).map(fact => fact.label);
  assert.deepEqual(labels.slice(-2), ['Retries', 'Result']);
});

console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
