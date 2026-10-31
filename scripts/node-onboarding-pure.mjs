// Pure regressions for the Add node wizard (apps/web/src/onboarding.ts, views/NodeWizard.tsx, the Add node dialog and
// node cards in views/Registry.tsx, the invitation and onboarding calls in api.ts, the outbound wording and invitation
// helpers in dashboard.ts, and CopyButton's failure state in ui/bits.tsx).
// The contract is root's frozen node-connection design (.canary/node-connection-design.md) and the shared DTOs in
// packages/shared/src/dashboard.ts: GET /api/dashboard/onboarding gives NodeOnboardingCapabilities; GET and POST
// /api/dashboard/invitations list and create ({label,nodeId}, 201 with the token once); GET and DELETE
// /api/dashboard/invitations/:id read and cancel (204; 409 once confirmed); POST :id/confirm (no body) answers 201/202
// with the RegisteredNode. All of them are administrator routes of the dashboard itself, without a node selection.
// RegisteredNode gains `connection` ('outbound', omitted for direct) and `lastConnectedAt` (transport only); the state
// set is unchanged. Outbound nodes start with CANARY_DASHBOARD_URL and CANARY_DASHBOARD_ENROLL_TOKEN.
// It pins: the node ID rule against Go's `identifier`; credentials made in the browser against Go's `tokenValue`; the
// quoting of every format the wizard writes (POSIX shell, Compose YAML, systemd EnvironmentFile, Docker --env-file);
// that previews never hold a private value and Copy/Download text holds each only where it belongs; the container
// hardening of deploy/portainer-node.yml in Docker run and Compose; no inbound port for outbound nodes; no Docker socket,
// TLS skipping, install scripts or invented release downloads; one naming helper for every container, volume, project,
// stack, unit, state directory and file name of a new node, bound to the complete case-sensitive node ID, while a running
// node keeps the names it has; the API calls and normalizers; direct wording unchanged;
// the wording of Go's own invitation refusals (read from its handlers); the explicit fingerprint check before Add; and,
// since the browser review now configures node connections, the outbound choice and refusal when they're off.
// From r5: a running node's steps say first when it refuses to start and how to undo that; new-node steps pull the image
// and name the downloaded settings file in every Compose command; an ended invitation for a deployed node points to a
// running-node invitation; the waiting hint names every cause; a new node's tokens belong to one node ID; a pending
// create can only be stopped; and Review offers nothing once the invitation is gone. The browser review drives the
// pending create and the token binding in a real page. After R6: a new node's Docker and Compose start commands are one
// && list, so a failed chmod, pull or volume create starts nothing.
// TLS r1 (.canary/transport-tls-contract.md, shared DTOs with tlsFingerprint and connectionTlsFingerprint): a new node
// the dashboard reaches at an https:// address serves TLS itself (CANARY_TLS_MODE=auto) and is verified with the
// certificate fingerprint from its own log; plain http:// stays as before; a running node's steps never set a TLS mode;
// an invitation's dashboard certificate pin becomes CANARY_DASHBOARD_TLS_FINGERPRINT; the direct forms send a
// fingerprint only when one is entered, and only for HTTPS; certificate refusals read as such, never as a sent credential.
// TLS r2 (the same contract, .canary/update-notice-contract.md and .canary/update-ui-contract-feedback.md): CanaryApi.updates
// and checkUpdates are one GET and one POST to the dashboard itself, without a node selection or body; the overview keeps a
// node's reported build only with a whole peer protocol from 1 to 10000 (alert templates need 20), and assumes none
// otherwise; a Linux VM's unit adds CAP_NET_BIND_SERVICE only for a port below 1024, while Docker and Compose steps stay
// as they were; a portless address means 443 and the suggestion shows :3100; container steps name tls-info, and moving a
// plain HTTP node to TLS names tls-init with CANARY_TLS_MODE=off kept until the switch.
// Uses exported functions with a stand-in fetch and stand-in `location`/`document`, renders the dialog pieces and
// RegistryView to static markup (effects never run), and reads sources as text: no network, browser, build, copy,
// scratch or output files. It doesn't prove the browser, the Go server or a node; the browser review in
// scripts/dashboard-control-plane-browser.mjs drives those.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/node-onboarding-pure.mjs   (exit 1 on any failure)
import { existsSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const web = resolve(root, 'apps/web');
const PAGE = 'https://uptime.example.test';
const CONNECT = 'https://connect.example.test';
Object.defineProperty(globalThis, 'location', { value: { origin: PAGE, protocol: 'https:', hash: '', pathname: '/' }, configurable: true, writable: true });
globalThis.document ??= { addEventListener() {}, hidden: false };
const pageDocument = globalThis.document;
const storageWrites = [];
const storage = { getItem: () => null, setItem: (...args) => storageWrites.push(args), removeItem: (...args) => storageWrites.push(args) };
Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true, writable: true });
Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true, writable: true });
// Everything written to the console while this runs, so the last group can prove no private value reached it.
const logged = [];
for (const level of ['log', 'info', 'warn', 'error', 'debug']) {
  const original = console[level].bind(console);
  console[level] = (...args) => { logged.push(args.map(String).join(' ')); original(...args); };
}
const require = createRequire(import.meta.url);
const React = require('react');
const { renderToStaticMarkup } = await import('react-dom/server');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'node-onboarding-pure', tsconfig: resolve(web, 'tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(web, path)).href, import.meta.url);
const api = await load('src/api.ts');
const dashboard = await load('src/dashboard.ts');
const onboarding = await load('src/onboarding.ts');
const { CanaryContext } = await load('src/context.tsx');
const wizard = await load('src/views/NodeWizard.tsx');
const registryView = await load('src/views/Registry.tsx');
const bits = await load('src/ui/bits.tsx');
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const HEADER = 'x-canary-registered-node';
const TOKEN = 'node-onboarding-review-dashboard-token';
const REG = '0f3c2b1a9d8e7f60514233a4b5c6d7e8';
const INV = '1a2b3c4d5e6f708192a3b4c5d6e7f809';
const INVITE_TOKEN = `${INV}.cmV2aWV3LW9ubHktaW52aXRhdGlvbi1zZWNyZXQtdmFsdWU`;
const ADMIN = 'Review_admin-token-made-in-the-browser-0123';
const CLUSTER = 'Review_cluster-token-made-in-browser-456789';
const SECRETS = { admin: ADMIN, cluster: CLUSTER };
const PRIVATE = [ADMIN, CLUSTER, INVITE_TOKEN];
const SHA = 'a'.repeat(40);
const CAPS = { outboundEnabled: true, connectionUrl: CONNECT, connectionProtocol: 1, imageTag: `sha-${SHA}`, containerPlatforms: ['linux/amd64'], nativeDownloadAvailable: false };
const AMBIGUOUS_IPV4 = ['192.168.001.010', '10.1', '0x7f.0.0.1', '2130706433', '0177.0.0.1'];
const ENTRY = { id: REG, label: 'Rack B', nodeId: 'canary-b', nodeName: 'Canary B', url: 'http://100.79.12.4:3100', local: false, state: 'answering', lastReadAt: 900_000, monitorCount: 3 };
const OUTBOUND = { id: 'e'.repeat(32), label: 'Branch office', nodeId: 'canary-branch', nodeName: 'Branch', local: false, state: 'answering', connection: 'outbound', lastConnectedAt: 950_000, lastReadAt: 960_000, monitorCount: 4 };
const WAITING = { id: INV, label: 'Branch office', nodeId: 'canary-branch', connectionUrl: CONNECT, expiresAt: 1_900_000, state: 'waiting' };
const FINGERPRINT = 'SHA256:4f:1c:9e:7a:22:b0:5d:13:88:c6:e1:0f:3a:72:91:d4';
const CONNECTED = { ...WAITING, state: 'connected', node: { id: 'canary-branch', name: 'Branch', version: '0.19.0', sourceCommit: 'ABCDEF0123456789', fingerprint: FINGERPRINT } };
const STATES = ['starting', 'answering', 'not-answering', 'credential-refused', 'different-node', 'enrolling', 'removing', 'rotating'];

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const server = (url, init) => {
  const { pathname } = new URL(url);
  const method = init.method ?? 'GET';
  if (pathname === '/api/dashboard/onboarding') return json(200, { ...CAPS, extra: 'dropped' });
  if (pathname === '/api/dashboard/invitations' && method === 'GET') return json(200, [{ ...WAITING, token: INVITE_TOKEN, secret: 'dropped' }, CONNECTED]);
  if (pathname === '/api/dashboard/invitations' && method === 'POST') return json(201, { ...WAITING, token: INVITE_TOKEN, extra: 1 });
  if (pathname === `/api/dashboard/invitations/${INV}` && method === 'GET') return json(200, { ...CONNECTED, token: INVITE_TOKEN });
  if (pathname === `/api/dashboard/invitations/${INV}` && method === 'DELETE') return new Response(null, { status: 204 });
  if (pathname === `/api/dashboard/invitations/${INV}/confirm`) return json(201, { ...OUTBOUND, credentials: 'never-kept' });
  return json(404, { error: 'Not found' });
};
let answer = server;
let calls = [];
globalThis.fetch = async (url, init = {}) => { calls.push({ url: String(url), init }); return answer(String(url), init); };
const record = async (run, respond = server) => { calls = []; answer = respond; try { await run(); } finally { answer = server; } return calls; };
const header = (call, name) => new Headers(call.init.headers).get(name);
const rejection = async promise => { try { await promise; } catch (error) { return error; } assert.fail('expected a failure'); };
const on = () => { globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; };
const off = () => { delete globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; delete pageDocument.documentElement; };
const read = path => readFileSync(resolve(web, path), 'utf8');
// Static markup has no text separators; any comment is dropped without a space, in case a React version adds them.
const text = markup => markup.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]*>/g, ' ').replace(/&#x27;/g, '\'').replace(/&quot;/g, '"').replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/\s+/g, ' ');
const tags = (markup, name) => markup.match(new RegExp(`<${name}\\b[^>]*>`, 'gu')) ?? [];
// React 19's static markup keeps some JSX spellings (autoComplete, maxLength) and writes `value` last, so a tag's
// attributes are read by lowercased name, in any order. A name given twice fails: a browser keeps only the first.
const attributes = tag => {
  const found = new Map();
  for (const [, name, value] of tag.replace(/^<[a-z]+/iu, '').matchAll(/\s([^\s"'<>\/=]+)(?:="([^"]*)")?/gu)) {
    const key = name.toLowerCase();
    assert.ok(!found.has(key), `${key} appears twice in ${tag}`);
    found.set(key, value ?? '');
  }
  return found;
};
/** One node card's markup, from its heading reference to the end of its article. */
const card = (markup, id) => {
  const start = markup.indexOf(`aria-labelledby="r-${id}"`);
  assert.ok(start >= 0, `no card for ${id}`);
  return markup.slice(start, markup.indexOf('</article>', start));
};
// Any method the rendered views reach on the API during rendering is recorded; effects never run, so none should be.
const reached = [];
const idleApi = new Proxy({}, { get: (_, name) => { reached.push(String(name)); return () => new Promise(() => {}); } });
const ctx = (role, list) => ({
  admin: role === 'admin', role, controlPlane: true, selection: null, select() {}, stillCurrent: () => true, refresh() {}, nodes: [], endpoint: 'dashboard',
  overview: { node: { id: 'dashboard', name: 'This dashboard' }, now: 1_000_000, monitors: [], nodes: [], incidents: [], notifications: [], user: { name: 'review', role } },
  connection: { awaiting: false, issuerSkew: 0, link: 'live', identity: 'review' }, api: idleApi,
  registry: { list, map: new Map(list.map(node => [node.id, node])), loading: false, error: undefined, unsupported: false, reload() {} },
});
const within = (role, list, element) => renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx(role, list) }, element));
const resource = data => ({ data, error: undefined, loading: data === undefined, loadedAt: data === undefined ? undefined : 1, reload() {} });
const noop = () => {};

// Minimal readers for each format, to prove a quoted value reads back as exactly the original.
function shellWord(word) {
  let out = '';
  let rest = word;
  while (rest) {
    if (rest.startsWith('\'')) { const end = rest.indexOf('\'', 1); assert.ok(end > 0, `unterminated quote in ${word}`); out += rest.slice(1, end); rest = rest.slice(end + 1); }
    else if (rest.startsWith('\\')) { out += rest[1]; rest = rest.slice(2); }
    else assert.fail(`unquoted text in ${word}`);
  }
  return out;
}
const composeValue = quoted => {
  assert.match(quoted, /^'.*'$/su);
  const body = quoted.slice(1, -1);
  assert.doesNotMatch(body.replace(/''/gu, ''), /'/u, 'a lone quote inside a single-quoted scalar');
  assert.doesNotMatch(body.replace(/\$\$/gu, ''), /\$/u, 'a lone $ would make Compose interpolate');
  return body.replace(/''/gu, '\'').replace(/\$\$/gu, '$');
};
const systemdValue = quoted => {
  assert.match(quoted, /^".*"$/su);
  let out = '';
  const body = quoted.slice(1, -1);
  for (let i = 0; i < body.length; i++) {
    if (body[i] === '\\') { assert.ok('\\"`$'.includes(body[i + 1]), `unexpected escape in ${quoted}`); out += body[++i]; }
    else { assert.ok(!'"`$'.includes(body[i]), `unescaped ${body[i]} in ${quoted}`); out += body[i]; }
  }
  return out;
};
// An independent 32-bit FNV-1a over UTF-8 bytes in BigInt arithmetic, to check the page's Math.imul version against.
const fnv1a = value => {
  let hash = 0x811c9dc5n;
  for (const byte of Buffer.from(value, 'utf8')) hash = ((hash ^ BigInt(byte)) * 0x01000193n) & 0xffffffffn;
  return hash.toString(16).padStart(8, '0');
};
const HOSTILE = ['O\'Brien "$HOME" `id` \\ #1 ;rm -rf / && $(x)', 'plain', '$$', '\'', '"', 'é ü 日本', '%h %n'];
const FORBIDDEN = /docker\.sock|--privileged|privileged:\s*true|network_mode:\s*host|--network[ =]host|pid:\s*host|--insecure|\s-k\s|InsecureSkipVerify|TLS_SKIP|REJECT_UNAUTHORIZED|\bcurl\b|\bwget\b|\|\s*(?:ba)?sh\b|apt-get|apt install|get\.docker\.com|netbird up|tailscale up|\/releases\/|\.tar\.gz|\.zip\b|SYS_ADMIN|NET_ADMIN/u;

await check('node ID: the same rule as the node’s Go `identifier`, plus this dashboard’s nodes and waiting invitations', () => {
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/validation.go'), 'utf8');
  const pattern = /var idPattern = regexp\.MustCompile\(`([^`]+)`\)/u.exec(go)?.[1];
  assert.equal(pattern, onboarding.NODE_ID_PATTERN.source, 'the page’s pattern is Go’s');
  assert.match(go, /slices\.Contains\(\[\]string\{"constructor", "prototype", "__proto__"\}, v\)/u, 'the reserved names');
  assert.match(go, /validText\(v, field, 128\)/u, '128 bytes');
  for (const good of ['a', 'canary-office', 'node.01:eu_west', 'A'.repeat(128), '9']) assert.equal(onboarding.nodeIdProblem(good), undefined, good);
  for (const bad of ['', '-a', '.a', '_a', ':a', 'a b', ' a', 'a ', 'é', 'a/b', 'a\nb', 'A'.repeat(129), 'constructor', 'prototype', '__proto__']) assert.ok(onboarding.nodeIdProblem(bad), JSON.stringify(bad));
  assert.match(onboarding.nodeIdProblem('canary-b', { nodes: [ENTRY] }), /Already on this dashboard as “Rack B”\./u);
  assert.match(onboarding.nodeIdProblem('canary-branch', { invitations: [WAITING] }), /already waiting/u);
  assert.match(onboarding.nodeIdProblem('canary-branch', { invitations: [CONNECTED] }), /already waiting/u);
  for (const state of ['expired', 'confirmed']) assert.equal(onboarding.nodeIdProblem('canary-branch', { invitations: [{ ...WAITING, state }] }), undefined, state);
  assert.equal(onboarding.nodeIdProblem('Canary-B', { nodes: [ENTRY] }), undefined, 'IDs are case-sensitive, as on the node');
});

await check('credentials made in this browser: 43 base64url characters that meet the node’s `tokenValue` and always differ', () => {
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/validation.go'), 'utf8');
  assert.match(go, /len\(v\) < 24 \|\| len\(v\) > 256 \|\| len\(seen\) < 8/u, 'Go’s token bounds');
  const seen = new Set();
  for (let i = 0; i < 200; i++) {
    const { admin, cluster } = onboarding.newNodeSecrets();
    for (const value of [admin, cluster]) {
      assert.match(value, /^[A-Za-z0-9_-]{43}$/u);
      assert.ok(value.length >= 24 && value.length <= 256 && new Set(value).size >= 8 && /^[\x21-\x7e]+$/u.test(value), 'tokenValue');
      assert.ok(!seen.has(value), 'never repeated');
      seen.add(value);
    }
    assert.notEqual(admin, cluster);
  }
  assert.throws(() => onboarding.newSecret(bytes => bytes), /couldn’t make random credentials/u, 'all-zero bytes never pass as a token');
  let turn = 0;
  const repeating = bytes => { bytes.set(globalThis.crypto.getRandomValues(new Uint8Array(32)).map((value, index) => turn < 2 ? index * 7 : value)); turn++; return bytes; };
  const pair = onboarding.newNodeSecrets(repeating);
  assert.notEqual(pair.admin, pair.cluster, 'a repeated value is made again');
});

await check('quoting: shell, Compose YAML and systemd values read back as exactly the original; line breaks are refused', () => {
  for (const value of HOSTILE) {
    assert.equal(shellWord(onboarding.shellQuote(value)), value, `shell ${value}`);
    assert.equal(composeValue(onboarding.composeQuote(value)), value, `compose ${value}`);
    assert.equal(systemdValue(onboarding.systemdQuote(value)), value, `systemd ${value}`);
  }
  for (const bad of ['a\nb', 'a\rb', 'a\0b', 'a\tb', 'a\x7fb']) {
    for (const quote of [onboarding.shellQuote, onboarding.composeQuote, onboarding.systemdQuote]) assert.throws(() => quote(bad), /control characters/u, JSON.stringify(bad));
  }
  // Accepted naming change (r4). Go's node identity is exact and case-sensitive: validation.go's idPattern accepts both
  // cases, and dashboard_invitations.go and dashboard_registry.go compare NodeID exactly. So Node.01:EU, node.01:eu and node-01-eu
  // are three nodes, which the r1-r3 rule all named canary-node-01-eu: on one host, a later node's container, Compose
  // project, volumes, unit and private env file could replace or reuse an earlier node's. The accepted rule keeps the same
  // readable part and adds a dash and nodeIdCode() of the complete ID, so this expectation now ends in that code.
  assert.equal(onboarding.resourceName('Node.01:EU'), `canary-node-01-eu-${fnv1a('Node.01:EU')}`);
  for (const id of ['a', 'Z'.repeat(128), 'x:y.z_w-v', 'a--', 'a.']) {
    const name = onboarding.resourceName(id);
    assert.match(name, /^canary-[a-z0-9_-]*[a-z0-9]$/u, name);
    assert.ok(name.length <= 63, name);
  }
});

// Accepted behavior change (user's2026-10-26report): the URL never selects the host binding.
// Prior IP-specific cases below now supply that binding explicitly, preserving their exact output regressions.
// node-setup-fields-pure covers default wildcard bindings and different remote/local IPs on every platform.
await check('a new node’s address: HTTPS or HTTP on its own port; explicit local IP bindings stay separate from the URL', () => {
  assert.deepEqual(onboarding.directTarget(' http://100.79.12.4:3100 ', '100.79.12.4'), { url: 'http://100.79.12.4:3100', host: '100.79.12.4', listen: '100.79.12.4', port: 3100, allInterfaces: false });
  assert.deepEqual(onboarding.directTarget('http://[fd00::1]:3100/', 'fd00::1'), { url: 'http://[fd00::1]:3100', host: '[fd00::1]', listen: 'fd00::1', port: 3100, allInterfaces: false });
  assert.deepEqual(onboarding.directTarget('http://canary-b.lan:8080'), { url: 'http://canary-b.lan:8080', host: '0.0.0.0', listen: '0.0.0.0', port: 8080, allInterfaces: true });
  assert.equal(onboarding.directTarget('http://10.0.0.5', '10.0.0.5').port, 80);
  // Accepted behavior change (TLS r1, .canary/transport-tls-contract.md: "Generated NEW-node instructions should select
  // auto"). Until now a node served only plain HTTP, so this expected https:// to be refused with "put a TLS proxy in
  // front". The node now serves TLS 1.3 itself with CANARY_TLS_MODE=auto (transport_tls.go prepareTLS; runtime.go Start
  // wraps the listener in tls.NewListener), so https:// is the wizard's suggested address and gives the same target
  // shape, with 443 as its default port. Plain http:// keeps every expectation above.
  assert.deepEqual(onboarding.directTarget('https://10.0.0.5:3100', '10.0.0.5'), { url: 'https://10.0.0.5:3100', host: '10.0.0.5', listen: '10.0.0.5', port: 3100, allInterfaces: false });
  assert.equal(onboarding.directTarget('https://10.0.0.5', '10.0.0.5').port, 443);
  assert.deepEqual([onboarding.directTls(onboarding.directTarget('https://10.0.0.5:3100', '10.0.0.5')), onboarding.directTls(onboarding.directTarget('http://10.0.0.5:3100', '10.0.0.5'))], [true, false]);
  assert.match(onboarding.directTarget('ftp://10.0.0.5:3100'), /Use an https:\/\/ address, like https:\/\/100\.79\.12\.4:3100/u);
  for (const bad of ['http://u:p@10.0.0.5:3100', 'http://10.0.0.5:3100/api', 'http://10.0.0.5:3100?x=1', 'http://10.0.0.5:3100#x', '10.0.0.5:3100', '',
    'https://u:p@10.0.0.5:3100', 'https://10.0.0.5:3100/api', 'ftp://10.0.0.5:3100']) assert.equal(typeof onboarding.directTarget(bad), 'string', bad);
});

const combos = [
  { name: 'new node, the dashboard reaches it', setup: { nodeId: 'canary-office', name: 'Office', imageTag: CAPS.imageTag, secrets: SECRETS, direct: onboarding.directTarget('http://100.79.12.4:3100', '100.79.12.4') } },
  { name: 'new node, it reaches the dashboard', setup: { nodeId: 'canary-office', name: 'Office', imageTag: CAPS.imageTag, secrets: SECRETS, outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN } } },
  { name: 'running node, it reaches the dashboard', setup: { nodeId: 'canary-office', name: 'Office', imageTag: CAPS.imageTag, secrets: null, outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN } } },
];
const privateOf = setup => [setup.secrets?.admin, setup.secrets?.cluster, setup.outbound?.token].filter(Boolean);
// Every platform for each way in, masked and revealed; built in the first check that reads them.
const guides = [];

await check('deployment text: previews hold no private value; Copy and Download text holds each one, and only in private blocks', () => {
  for (const combo of combos) for (const { value: platform } of onboarding.PLATFORMS) {
    guides.push({ ...combo, platform, masked: onboarding.setupGuide({ ...combo.setup, platform }, false), real: onboarding.setupGuide({ ...combo.setup, platform }, true) });
  }
  assert.deepEqual(onboarding.PLATFORMS.map(entry => entry.label), ['Docker run', 'Docker Compose', 'Portainer stack', 'Linux VM']);
  assert.equal(guides.length, 12);
  for (const { name, platform, setup, masked, real } of guides) {
    const where = `${name}, ${platform}`;
    assert.deepEqual(masked.map(block => [block.key, block.kind, block.file, block.secret, block.title]), real.map(block => [block.key, block.kind, block.file, block.secret, block.title]), where);
    assert.equal(new Set(real.map(block => block.key)).size, real.length, `${where}: unique steps`);
    const values = privateOf(setup);
    const preview = masked.map(block => block.text).join('\n');
    for (const value of values) {
      assert.ok(!preview.includes(value), `${where}: the preview shows a private value`);
      assert.ok(real.some(block => block.secret && block.text.includes(value)), `${where}: a private value is missing from the settings`);
    }
    real.forEach((block, index) => {
      if (!block.secret) {
        assert.equal(masked[index].text, block.text, `${where}: ${block.key} isn’t private, so it isn’t masked`);
        for (const value of values) assert.ok(!block.text.includes(value), `${where}: ${block.key} isn’t marked private but holds a private value`);
      } else if (values.some(value => block.text.includes(value))) {
        assert.ok(masked[index].text.includes(onboarding.MASK), `${where}: ${block.key} shows the mask`);
      }
      if (block.file) assert.match(block.file, /^[A-Za-z0-9._-]+$/u, `${where}: file name`);
      assert.doesNotMatch(block.text, FORBIDDEN, `${where}: ${block.key}`);
      assert.ok(!block.text.includes(PAGE), `${where}: this page’s own address never appears`);
    });
    const all = real.map(block => block.text).join('\n');
    if (setup.outbound) {
      assert.ok(all.includes(CONNECT), `${where}: the server’s connection address`);
      assert.ok(real.some(block => block.key === 'after' && /Remove CANARY_DASHBOARD_ENROLL_TOKEN/u.test(block.text)), `${where}: remove the one-time variable after pairing`);
      assert.doesNotMatch(all, /\s-p\s|--publish|^\s*ports:/mu, `${where}: no inbound port`);
    } else {
      assert.ok(!real.some(block => block.key === 'after'), where);
      assert.doesNotMatch(all, /CANARY_DASHBOARD_/u, `${where}: a direct node gets no connection settings`);
    }
    if (!setup.secrets) {
      assert.doesNotMatch(all, /CANARY_(?:ADMIN|CLUSTER)_TOKEN\s*[=:]|CANARY_NODE_ID\s*[=:]/u, `${where}: a running node keeps its own ID and tokens`);
      assert.match(all, /CANARY_NODE_ID must stay canary-office/u, where);
    }
  }
});

await check('Docker run and Compose: the published image with the template’s hardening, two volumes, and a port only when the dashboard dials in', () => {
  const templatePath = resolve(root, 'deploy/portainer-node.yml');
  const template = existsSync(templatePath) ? readFileSync(templatePath, 'utf8').split(/\r?\n/u) : null;
  if (template) assert.ok(template.some(line => line.includes(`${onboarding.NODE_IMAGE}:`)), 'the template’s image');
  const hardened = ['    init: true', '    restart: unless-stopped', '    read_only: true', '      - /tmp:rw,exec,nosuid,nodev,size=64m', '      CANARY_HOST: 0.0.0.0', '      CANARY_PORT: "3100"',
    '      CANARY_DB: /data/canary.db', '      CANARY_IDENTITY_DIRECTORY: /identity', '      - canary-data:/data', '      - canary-identity:/identity', '      - ALL', '      - NET_RAW',
    '      - no-new-privileges:true', '      driver: json-file', '        max-size: "10m"', '        max-file: "3"', '  canary-data:', '  canary-identity:'];
  for (const { name, platform, setup, real } of guides) {
    const where = `${name}, ${platform}`;
    const res = onboarding.resourceName(setup.nodeId);
    const image = `${onboarding.NODE_IMAGE}:${setup.imageTag}`;
    if (platform === 'docker' && setup.secrets) {
      const run = real.find(block => block.key === 'run').text;
      for (const part of ['--init', '--restart unless-stopped', '--read-only', '--tmpfs /tmp:rw,exec,nosuid,nodev,size=64m', '--cap-drop ALL --cap-add NET_RAW', '--security-opt no-new-privileges:true',
        `--env-file ${res}.env`, `-v ${res}-data:/data -v ${res}-identity:/identity`, 'CANARY_DB=/data/canary.db', 'CANARY_IDENTITY_DIRECTORY=/identity', `docker volume create ${res}-identity`]) {
        assert.ok(run.includes(part), `${where}: ${part}`);
      }
      assert.equal(run.trim().split('\n').at(-1).trim(), image, `${where}: pulls the published image`);
      assert.equal(/-p 100\.79\.12\.4:3100:3100/u.test(run), Boolean(setup.direct), `${where}: publishes only for the dashboard to dial in`);
      const env = real.find(block => block.key === 'env').text;
      assert.match(env, /^CANARY_NODE_ID=canary-office$/mu);
      assert.match(env, new RegExp(`^CANARY_ADMIN_TOKEN=${ADMIN}$`, 'mu'));
    }
    if ((platform === 'compose' || platform === 'portainer') && setup.secrets) {
      const yaml = real.find(block => block.key === 'yaml').text;
      const lines = yaml.split('\n');
      for (const line of hardened) {
        assert.ok(lines.includes(line), `${where}: ${line.trim()}`);
        if (template) assert.ok(template.includes(line), `the template still has ${line.trim()}; update the wizard with it`);
      }
      assert.ok(lines.includes(`    image: ${image}`), `${where}: the published image`);
      assert.ok(lines.includes('      CANARY_ADMIN_TOKEN: ${CANARY_ADMIN_TOKEN:?Set CANARY_ADMIN_TOKEN}'), `${where}: tokens come from the variables`);
      assert.equal(lines.includes(`name: ${res}`), platform === 'compose', `${where}: a fixed Compose project name; Portainer names it after the stack`);
      assert.equal(lines.includes('    ports:'), Boolean(setup.direct), where);
      if (setup.direct) assert.ok(lines.includes('      - "100.79.12.4:3100:3100"'), where);
      if (setup.outbound) assert.ok(lines.includes(`      CANARY_DASHBOARD_URL: '${CONNECT}'`) && lines.includes('      CANARY_DASHBOARD_ENROLL_TOKEN: ${CANARY_DASHBOARD_ENROLL_TOKEN:-}'), where);
      const variables = real.find(block => block.key === (platform === 'compose' ? 'env' : 'variables')).text;
      for (const line of variables.split('\n').filter(line => line && !line.startsWith('#'))) assert.match(line, /^CANARY_[A-Z_]+=[A-Za-z0-9._~:+/=@%,-]+$/u, `${where}: ${line.split('=')[0]} is written verbatim`);
    }
  }
  // Accepted command syntax change after R7: an IPv6 publish address is in brackets, which a POSIX shell reads as a
  // pattern (XCU 2.13.1 bracket expression; zsh stops with "no matches found", bash passes it on only while no file
  // matches). It's now one single-quoted word, which the shell passes on as written (XCU 2.2.2), so this expects the
  // exact line -p '[fd00::1]:3100:3100' where it matched -p [fd00::1]:3100:3100. An IPv4 address or 0.0.0.0 has no
  // pattern character and stays unquoted, and a node the dashboard doesn't dial publishes no port.
  const published = setup => onboarding.setupGuide({ ...setup, platform: 'docker' }, true).find(block => block.key === 'run').text.split('\n').filter(line => /^\s*-p\s/u.test(line));
  assert.deepEqual(published({ ...combos[0].setup, direct: onboarding.directTarget('http://[fd00::1]:3100', 'fd00::1') }), ["  -p '[fd00::1]:3100:3100' \\"]);
  assert.deepEqual(published(combos[0].setup), ['  -p 100.79.12.4:3100:3100 \\']);
  assert.deepEqual(published({ ...combos[0].setup, direct: onboarding.directTarget('http://canary-b.lan:3100') }), ['  -p 0.0.0.0:3100:3100 \\']);
  assert.deepEqual(published(combos[1].setup), []);
  const named = onboarding.setupGuide({ ...combos[0].setup, direct: onboarding.directTarget('http://canary-b.lan:3100'), platform: 'compose' }, true);
  assert.ok(named.find(block => block.key === 'yaml').text.includes('      - "0.0.0.0:3100:3100"'));
});

await check('Linux VM: no download; build steps from the matching source; a hardened systemd unit with no private value', () => {
  // Measurement fix (r3). Every VM file and path is named by resourceName(nodeId), the same rule as the Docker names
  // checked above, and that rule always prefixes "canary-". So node ID canary-office gives canary-canary-office: the unit
  // renders `EnvironmentFile=/etc/uptime-canary/canary-canary-office.env`, the install command copies
  // canary-canary-office.env, and the identity is in /var/lib/canary-canary-office/identity. The first frozen run failed
  // on the line below because it wrote the bare node ID where the source writes the resource name. That was a wrong
  // expected name, not an HTML escaping or structure problem: these are plain-text blocks, never markup. The same lines
  // are still asserted exactly, now with the resource name, and the unit, env file, install step and state directory
  // must all agree on it.
  // Accepted naming change (r4): the same name now ends in a dash and nodeIdCode() of the complete, case-sensitive ID,
  // because Go compares node IDs exactly and the readable part alone gave distinct IDs one unit and state directory (see
  // the quoting group and the naming group below). The double "canary-" prefix stays as it was.
  for (const { name, setup, real } of guides.filter(guide => guide.platform === 'vm')) {
    const all = real.map(block => block.text).join('\n');
    if (setup.secrets) {
      const res = onboarding.resourceName(setup.nodeId);
      assert.equal(res, `canary-canary-office-${fnv1a('canary-office')}`, `${name}: the VM's resource name`);
      assert.match(real.find(block => block.key === 'native').text, /doesn't publish native release binaries, so there's nothing to download here/u, name);
      const build = real.find(block => block.key === 'build').text;
      assert.ok(build.includes(`git clone ${onboarding.SOURCE_REPOSITORY}`) && build.includes(`git checkout ${SHA}`), name);
      assert.ok(build.includes('go build -trimpath -o uptime-canary ./apps/node-go/cmd/canary'), name);
      const unit = real.find(block => block.key === 'unit');
      assert.equal(unit.secret, false);
      assert.equal(unit.file, `${res}.service`, `${name}: the unit file`);
      for (const line of ['DynamicUser=yes', 'NoNewPrivileges=yes', 'AmbientCapabilities=CAP_NET_RAW', 'CapabilityBoundingSet=CAP_NET_RAW', 'ProtectSystem=strict', `EnvironmentFile=/etc/uptime-canary/${res}.env`,
        `StateDirectory=${res}`, `Environment=CANARY_IDENTITY_DIRECTORY=/var/lib/${res}/identity`, `Environment=CANARY_DB=/var/lib/${res}/canary.db`, 'ExecStart=/usr/local/bin/uptime-canary serve']) assert.ok(unit.text.split('\n').includes(line), `${name}: ${line}`);
      const settingsBlock = real.find(block => block.key === 'env');
      assert.equal(settingsBlock.file, `${res}.env`, `${name}: the file the install step copies`);
      assert.ok(settingsBlock.text.startsWith(`# Uptime Canary node ${setup.nodeId}: private settings. Install as /etc/uptime-canary/${res}.env, mode 0600.\n`), name);
      const install = real.find(block => block.key === 'install').text;
      assert.ok(install.includes(`sudo install -m 0600 ${res}.env /etc/uptime-canary/${res}.env`) && install.includes(`\nrm ${res}.env\n`), name);
      assert.ok(install.includes(`sudo install -m 0644 ${res}.service /etc/systemd/system/${res}.service`) && install.includes(`sudo systemctl enable --now ${res}\n`), name);
      const env = real.find(block => block.key === 'env').text;
      assert.ok(env.includes(`CANARY_HOST="${setup.direct ? '100.79.12.4' : '127.0.0.1'}"`), `${name}: listens where the dashboard dials, or only locally`);
      for (const line of env.split('\n').filter(line => line && !line.startsWith('#'))) assert.match(line, /^CANARY_[A-Z_]+="(?:[^"\\`$]|\\[\\"`$])*"$/u, `${name}: ${line.split('=')[0]} is quoted for systemd`);
    }
    assert.doesNotMatch(all, /https?:\/\/(?!github\.com\/Narehood\/Uptime-Canary\.git|connect\.example\.test|100\.79\.12\.4)/u, `${name}: no other address, and no download`);
  }
  const latest = onboarding.setupGuide({ ...combos[0].setup, imageTag: 'latest', platform: 'vm' }, true).find(block => block.key === 'build').text;
  assert.doesNotMatch(latest, /git checkout/u);
  assert.match(latest, /# Check out the commit that image tag latest was built from before building\./u);
});

await check('new-node names: one helper for every output, kept apart for IDs that differ only in case, punctuation or past the readable part, and the same on every render; a running node keeps its names', () => {
  // Go's node identity is exact and case-sensitive, so each pair below is two nodes that may share a host.
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_invitations.go'), 'utf8');
  assert.ok(go.includes('if row.NodeID == input.NodeID {'), 'Go compares invitation node IDs exactly');
  // The code is 32-bit FNV-1a: the reference above gives the published vectors, and the page's version matches it.
  for (const [value, code] of [['', '811c9dc5'], ['a', 'e40c292c']]) {
    assert.equal(fnv1a(value), code, `reference ${JSON.stringify(value)}`);
    assert.equal(onboarding.nodeIdCode(value), code, `page ${JSON.stringify(value)}`);
  }
  // The r1-r3 rule, kept here only to prove each pair really shared one name before.
  const earlier = id => `canary-${id.toLowerCase().replace(/[^a-z0-9_-]+/gu, '-')}`.slice(0, 63).replace(/[-_]+$/u, '');
  const long = 'x'.repeat(60);
  const pairs = [['Office', 'office'], ['OFFICE', 'office'], ['a:b', 'a-b'], ['a.b', 'a:b'], ['Node.01:EU', 'node-01-eu'], ['a', 'a--'], ['a', 'a.'],
    [`${long}1`, `${long}2`], ['A'.repeat(128), 'a'.repeat(128)], [`Edge.${'y'.repeat(70)}:west`, `edge-${'y'.repeat(70)}-east`]];
  const ids = [...new Set([...pairs.flat(), ...Array.from({ length: 200 }, (_, i) => [`node-${i}`, `Node-${i}`, `node.${i}`]).flat()])];
  for (const id of ids) {
    assert.equal(onboarding.nodeIdProblem(id), undefined, `${id} is a valid node ID`);
    const name = onboarding.resourceName(id);
    assert.equal(onboarding.nodeIdCode(id), fnv1a(id), id);
    assert.match(name, /^canary-[a-z0-9][a-z0-9_-]*-[0-9a-f]{8}$/u, name);
    assert.ok(name.length <= 63, name);
    assert.ok(name.endsWith(`-${fnv1a(id)}`), `${name}: the code of the complete ID`);
    const readable = name.slice('canary-'.length, -9);
    const full = id.toLowerCase().replace(/[^a-z0-9_-]+/gu, '-');
    assert.ok(full.startsWith(readable) && readable.length <= 47 && /[a-z0-9]$/u.test(readable), `${name}: the readable start of ${id}`);
    if (full.length <= 47 && /[a-z0-9]$/u.test(full)) assert.equal(readable, full, `${name}: short IDs read in full`);
    assert.equal(onboarding.resourceName(id), name, `${id}: the same name when asked again`);
  }
  assert.equal(new Set(ids.map(onboarding.resourceName)).size, ids.length, 'every ID gets its own name');
  for (const [a, b] of pairs) {
    assert.equal(earlier(a), earlier(b), `${a} and ${b} shared ${earlier(a)} before`);
    assert.notEqual(onboarding.resourceName(a), onboarding.resourceName(b), `${a} and ${b}`);
  }
  // Every name in a new node's steps (titles, text and file names) is that node's resourceName(), alone or with -data or
  // -identity; Compose's own canary-data and canary-identity keys live inside the project or stack named by it.
  const NAME = /(?<![\w.-])canary-[a-z0-9_-]+/gu;
  const namesIn = blocks => {
    const found = { tokens: new Set(), files: new Set() };
    for (const block of blocks) {
      if (block.file) found.files.add(block.file);
      for (const [token] of `${block.title}\n${block.text}`.matchAll(NAME)) found.tokens.add(token);
    }
    return found;
  };
  const freshSetup = (nodeId, platform) => [
    { nodeId, name: 'Shared host', imageTag: CAPS.imageTag, secrets: SECRETS, direct: onboarding.directTarget('http://100.79.12.4:3100', '100.79.12.4'), platform },
    { nodeId, name: 'Shared host', imageTag: CAPS.imageTag, secrets: SECRETS, outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN }, platform },
  ];
  for (const [a, b] of pairs) for (const { value: platform } of onboarding.PLATFORMS) {
    const texts = {};
    for (const id of [a, b]) {
      const res = onboarding.resourceName(id);
      const compose = platform === 'compose' || platform === 'portainer';
      const allowed = new Set([res, `${res}-data`, `${res}-identity`, ...(compose ? ['canary-data', 'canary-identity'] : [])]);
      for (const setup of freshSetup(id, platform)) {
        const blocks = onboarding.setupGuide(setup, false);
        const where = `${id}, ${platform}, ${setup.direct ? 'direct' : 'outbound'}`;
        const { tokens, files } = namesIn(blocks);
        assert.ok(tokens.has(res), `${where}: the steps use its name`);
        for (const token of tokens) assert.ok(allowed.has(token), `${where}: ${token} isn’t this node’s name`);
        for (const file of files) assert.ok(file === 'compose.yaml' || [`${res}.env`, `${res}.yml`, `${res}.service`].includes(file), `${where}: file ${file}`);
        const all = blocks.map(block => `${block.title}\n${block.text}`).join('\n');
        // Accepted command syntax change after R6: every step before docker run ends in " &&" (one list that stops at a
        // failed step), so a volume name now ends at " &&" and then the line break, rather than at the line break.
        if (platform === 'docker') for (const part of [`--name ${res} `, `docker volume create ${res}-data &&\n`, `docker volume create ${res}-identity &&\n`, `--env-file ${res}.env `]) assert.ok(all.includes(part), `${where}: ${part.trim()}`);
        if (platform === 'compose') assert.ok(all.split('\n').includes(`name: ${res}`), `${where}: the Compose project`);
        if (platform === 'portainer') assert.ok(all.includes(`Name it ${res},`), `${where}: the stack`);
        if (platform === 'vm') for (const part of [`StateDirectory=${res}\n`, `sudo systemctl enable --now ${res}\n`, `/etc/systemd/system/${res}.service\n`]) assert.ok(all.includes(part), `${where}: ${part.trim()}`);
        texts[id] = `${texts[id] ?? ''}${all}${[...files].join('\n')}`;
      }
    }
    assert.ok(!texts[b].includes(onboarding.resourceName(a)) && !texts[a].includes(onboarding.resourceName(b)), `${platform}: ${a} and ${b} never use each other’s names`);
  }
  // The same names for one node whatever else changes: its label, image tag, address, tokens, invitation, reveal and order.
  const office = { nodeId: 'Office', imageTag: CAPS.imageTag };
  const variants = [
    { ...office, name: 'Office', secrets: SECRETS, direct: onboarding.directTarget('http://100.79.12.4:3100', '100.79.12.4') },
    { ...office, name: 'Another label', imageTag: 'latest', secrets: { admin: CLUSTER, cluster: ADMIN }, direct: onboarding.directTarget('http://10.0.0.9:8080') },
    { ...office, name: 'Office', secrets: onboarding.newNodeSecrets(), outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN } },
    { ...office, name: 'Office', secrets: onboarding.newNodeSecrets(), outbound: { connectionUrl: CONNECT, token: `${'f'.repeat(32)}.b3RoZXItaW52aXRhdGlvbi1zZWNyZXQtZm9yLXRoZS1zYW1lLW5vZGU` } },
  ];
  for (const { value: platform } of onboarding.PLATFORMS) {
    const seen = new Set();
    for (const variant of [...variants, ...variants.slice().reverse()]) for (const reveal of [false, true]) {
      const { tokens, files } = namesIn(onboarding.setupGuide({ ...variant, platform }, reveal));
      seen.add(JSON.stringify([[...tokens].sort(), [...files].sort()]));
    }
    assert.equal(seen.size, 1, `${platform}: one set of names: ${[...seen].join(' | ')}`);
  }
  // A running node keeps the names it has: its steps create, rename, move or delete nothing, name only their own small
  // connection file, and say that new-node names now end in a code.
  const kept = { docker: 'container and volume names', compose: 'Compose project and volume names', portainer: 'stack and volume names', vm: 'service name and state directory' };
  for (const id of ['Office', 'canary-office']) for (const { value: platform } of onboarding.PLATFORMS) {
    const res = onboarding.resourceName(id);
    const blocks = onboarding.setupGuide({ nodeId: id, name: 'Office', imageTag: CAPS.imageTag, secrets: null, outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN }, platform }, false);
    const where = `running ${id}, ${platform}`;
    const all = blocks.map(block => `${block.title}\n${block.text}`).join('\n');
    // (Its Docker note may say "its docker run command": that's the node's own command, which keeps its name and volumes.)
    assert.doesNotMatch(all, /docker volume create|docker run -d|--name |^name: |Name it |StateDirectory=|systemctl enable|daemon-reload|\/etc\/systemd\/system/mu, `${where}: nothing new is made`);
    for (const file of namesIn(blocks).files) assert.equal(file, `${res}-connect.env`, `${where}: only its connection file is named`);
    for (const token of namesIn(blocks).tokens) assert.ok(token === `${res}-connect` || token === id, `${where}: ${token}`);
    const keep = blocks.find(block => block.key === 'keep').text;
    assert.ok(keep.includes(`Keep its ${kept[platform]} as they are. The new-node steps on this page now end those names in a short code from the node ID, so they can differ from a node deployed earlier; nothing here renames, moves or deletes them.`), `${where}: ${keep}`);
  }
  // No step, new or running, removes, renames or moves a container, volume, project or unit.
  for (const { masked, name, platform } of guides) {
    assert.doesNotMatch(masked.map(block => block.text).join('\n'), /volume (?:rm|prune)|system prune|compose down|docker (?:rm|rename)\b|systemctl (?:disable|stop)|\brm -r|\bmv\b/u, `${name}, ${platform}`);
  }
});

await check('hostile names stay literal in every format; values a format can’t hold are refused before any text is made', () => {
  const name = HOSTILE[0];
  const fresh = { ...combos[1].setup, name };
  const docker = onboarding.setupGuide({ ...fresh, platform: 'docker' }, true).find(block => block.key === 'env').text;
  assert.ok(docker.split('\n').includes(`CANARY_NODE_NAME=${name}`), 'Docker reads an env-file value literally');
  const yaml = onboarding.setupGuide({ ...fresh, platform: 'compose' }, true).find(block => block.key === 'yaml').text;
  assert.equal(composeValue(/^ {6}CANARY_NODE_NAME: (.*)$/mu.exec(yaml)[1]), name);
  const vm = onboarding.setupGuide({ ...fresh, platform: 'vm' }, true).find(block => block.key === 'env').text;
  assert.equal(systemdValue(/^CANARY_NODE_NAME=(.*)$/mu.exec(vm)[1]), name);
  assert.ok(onboarding.setupGuide({ ...fresh, name: '  Office  ', platform: 'docker' }, true)[0].text.includes('CANARY_NODE_NAME=Office\n'), 'trimmed, like the dashboard label');
  const refused = [
    [{ ...fresh, name: 'Two\nlines' }, /control characters/u],
    [{ ...fresh, name: 'Tab\there' }, /control characters/u],
    [{ ...fresh, nodeId: 'bad id' }, /valid node ID/u],
    [{ ...fresh, imageTag: 'latest --privileged' }, /image tag/u],
    [{ ...fresh, imageTag: '' }, /image tag/u],
    [{ ...fresh, outbound: { connectionUrl: CONNECT, token: `${INV}'quoted` } }, /safely/u],
    [{ ...fresh, direct: combos[0].setup.direct }, /Choose how/u],
    [{ ...fresh, outbound: undefined }, /Choose how/u],
    [{ ...combos[0].setup, secrets: null }, /own address and credential/u],
  ];
  for (const [setup, pattern] of refused) for (const { value: platform } of onboarding.PLATFORMS) assert.throws(() => onboarding.setupGuide({ ...setup, platform }, false), pattern, `${platform}: ${JSON.stringify(setup.name ?? setup.nodeId)}`);
});

await check('API: onboarding and invitation routes go to the dashboard itself, without a node selection; only creation returns the token', async () => {
  on();
  const client = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
  let capabilities, list, created, one, confirmed;
  const sent = await record(async () => {
    capabilities = await client.onboarding();
    list = await client.invitations();
    created = await client.createInvitation({ label: '  Branch office  ', nodeId: 'canary-branch' });
    one = await client.invitation(INV);
    assert.equal(await client.cancelInvitation(INV), undefined);
    confirmed = await client.confirmInvitation(INV);
  });
  assert.deepEqual(sent.map(call => `${call.init.method ?? 'GET'} ${call.url}`), [
    `GET ${PAGE}/api/dashboard/onboarding`, `GET ${PAGE}/api/dashboard/invitations`, `POST ${PAGE}/api/dashboard/invitations`,
    `GET ${PAGE}/api/dashboard/invitations/${INV}`, `DELETE ${PAGE}/api/dashboard/invitations/${INV}`, `POST ${PAGE}/api/dashboard/invitations/${INV}/confirm`,
  ]);
  for (const call of sent) {
    assert.equal(header(call, HEADER), null, `${call.url}: never a node selection`);
    assert.equal(header(call, 'authorization'), `Bearer ${TOKEN}`);
    assert.equal(call.init.redirect, 'error');
    assert.equal(call.init.credentials, 'same-origin');
  }
  assert.deepEqual(JSON.parse(sent[2].init.body), { label: 'Branch office', nodeId: 'canary-branch' });
  const custom = await record(() => client.createInvitation({ label: 'Branch office', nodeId: 'canary-branch', connectionUrl: 'https://Connect.Example.test:443/' }));
  assert.equal(JSON.parse(custom[0].init.body).connectionUrl, CONNECT, 'an explicit node-reachable address is canonicalized and sent to the dashboard');
  const refusedAddresses = await record(async () => {
    for (const host of AMBIGUOUS_IPV4) {
      const failure = await rejection(client.createInvitation({ label: 'Branch office', nodeId: 'canary-branch', connectionUrl: `https://${host}` }));
      assert.equal(failure.status, 400, 'ambiguous IP addresses are refused before sending an invitation request');
    }
  });
  assert.equal(refusedAddresses.length, 0);
  for (const index of [0, 1, 3, 4, 5]) {
    assert.equal(sent[index].init.body, undefined, `${sent[index].url}: no body`);
    assert.equal(header(sent[index], 'content-type'), null);
  }
  assert.deepEqual(capabilities, CAPS, 'unknown fields dropped');
  assert.ok(!JSON.stringify(list).includes(INVITE_TOKEN) && !JSON.stringify(one).includes(INVITE_TOKEN), 'statuses never keep a token');
  assert.ok(!('secret' in list[0]));
  assert.equal(created.token, INVITE_TOKEN);
  assert.ok(!('extra' in created));
  assert.equal(list[1].node.sourceCommit, 'abcdef0123456789');
  assert.deepEqual([confirmed.connection, confirmed.lastConnectedAt, 'credentials' in confirmed], ['outbound', 950_000, false]);
  const refused = await record(async () => {
    for (const attempt of [() => client.invitation('../escape'), () => client.cancelInvitation('a b'), () => client.confirmInvitation('')]) {
      assert.match((await rejection(Promise.resolve().then(attempt))).message, /isn’t a node registration/u);
    }
  });
  assert.equal(refused.length, 0, 'odd IDs never leave the page');
  const failure = await rejection(record(() => client.createInvitation({ label: 'x', nodeId: 'canary-x' }), () => json(409, { error: 'Node ID is already registered' })));
  assert.ok(failure instanceof api.ApiFailure);
  assert.deepEqual([failure.status, failure.detail], [409, 'Node ID is already registered']);
});

await check('API normalizers: an HTTPS connection origin or none; Docker tag grammar; known states only; tokens only from creation', () => {
  const caps = value => api.normalizeOnboarding({ ...CAPS, ...value });
  assert.deepEqual(caps({}), CAPS);
  assert.equal(caps({ connectionUrl: 'https://Connect.Example.test:443/' }).connectionUrl, CONNECT, 'canonical origin');
  for (const host of AMBIGUOUS_IPV4) {
    const address = `https://${host}`;
    assert.equal(api.connectionOrigin(address), null, 'an IPv4 address is never silently rewritten');
    assert.equal(caps({ connectionUrl: address }).outboundEnabled, false);
    assert.throws(() => api.normalizeInvitation({ ...WAITING, connectionUrl: address }), /could not read/u);
  }
  for (const url of ['http://connect.example.test', 'https://connect.example.test/connect', 'https://u:p@connect.example.test', 'https://connect.example.test?x', '', 7, undefined]) {
    const read = caps({ connectionUrl: url });
    assert.deepEqual([read.outboundEnabled, 'connectionUrl' in read], [false, false], String(url));
  }
  assert.deepEqual([caps({ outboundEnabled: false }).outboundEnabled, 'connectionUrl' in caps({ outboundEnabled: false })], [false, false], 'disabled keeps no address');
  assert.throws(() => caps({ connectionProtocol: 2 }), /protocol this page doesn’t know/u);
  for (const tag of ['latest --privileged', '', 'a:b', '-latest', 'x'.repeat(129), 7]) assert.throws(() => caps({ imageTag: tag }), /could not read/u, String(tag));
  assert.deepEqual(caps({ containerPlatforms: ['linux/amd64', 'Linux/AMD64', 'x', 'linux/arm64/v8', 3] }).containerPlatforms, ['linux/amd64', 'linux/arm64/v8']);
  assert.equal(caps({ nativeDownloadAvailable: 'yes' }).nativeDownloadAvailable, false);
  for (const bad of [{ id: 'x/y' }, { state: 'invited' }, { connectionUrl: 'http://connect.example.test' }, { expiresAt: 'soon' }, { nodeId: 'a b' }]) {
    assert.throws(() => api.normalizeInvitation({ ...WAITING, ...bad }), /could not read/u, JSON.stringify(bad));
  }
  const cleaned = api.normalizeInvitation({ ...CONNECTED, label: 'Branch\x00 office', node: { ...CONNECTED.node, name: 'Branch\nnode', sourceCommit: 'not-a-commit' } });
  assert.deepEqual([cleaned.label, cleaned.node.name], ['Branch  office', 'Branch node'], 'control characters become spaces');
  assert.ok(!('sourceCommit' in cleaned.node));
  assert.ok(!('node' in api.normalizeInvitation({ ...CONNECTED, node: { id: 'canary-branch', name: 'x' } })), 'no fingerprint, no node facts');
  for (const token of ['short', `${INV} space`, `${INV}"quote`, `${INV}'quote`, 7]) assert.throws(() => api.normalizeInvitationCreated({ ...WAITING, token }), /could not read/u, String(token));
  assert.equal(api.normalizeRegisteredNode({ ...OUTBOUND, connection: 'sideways' }).connection, undefined);
  assert.equal(api.normalizeRegisteredNode({ ...ENTRY, connection: 'direct' }).connection, 'direct');
  assert.ok(!('connection' in api.normalizeRegisteredNode(ENTRY)), 'legacy entries stay as they were');
  for (const at of [0, -1, Number.NaN, '950000']) assert.ok(!('lastConnectedAt' in api.normalizeRegisteredNode({ ...OUTBOUND, lastConnectedAt: at })), String(at));
});

await check('registry wording: outbound entries name their own connection; direct wording, readability and actions are unchanged', () => {
  on();
  for (const state of STATES) {
    const direct = dashboard.nodeStateInfo({ ...ENTRY, state });
    assert.deepEqual(dashboard.nodeStateInfo({ ...ENTRY, state, connection: 'direct' }), direct, `${state}: an explicit direct entry reads as before`);
    const out = dashboard.nodeStateInfo({ ...OUTBOUND, state });
    assert.deepEqual([out.tone, out.out, out.attention, out.readable], [direct.tone, direct.out, direct.attention, direct.readable], state);
    assert.doesNotMatch(out.text, /reach it|address/iu, `${state}: the dashboard never dials an outbound node`);
    assert.doesNotMatch(out.text, /Retry|Remove|Edit/u, `${state}: viewers read this text`);
    if (state === 'enrolling' || state === 'rotating') assert.equal(out.text, direct.text, state);
  }
  assert.equal(dashboard.nodeStateInfo(ENTRY).text, 'The dashboard’s server reads it.');
  assert.match(dashboard.nodeStateInfo({ ...ENTRY, state: 'not-answering' }).text, /^The dashboard’s server can’t reach it right now\./u);
  assert.equal(dashboard.nodeStateInfo({ ...ENTRY, state: 'different-node' }).label, 'Different node at this address');
  assert.equal(dashboard.nodeStateInfo({ ...OUTBOUND, state: 'different-node' }).label, 'Different node connected');
  assert.match(dashboard.nodeStateInfo({ ...OUTBOUND, state: 'not-answering' }).text, /isn’t connected to this dashboard right now.*may still be running/u);
  assert.match(dashboard.nodeStateInfo({ ...OUTBOUND, state: 'removing' }).text, /only so the node can confirm/u);
  assert.deepEqual(dashboard.nodeActions(OUTBOUND, true), { check: true, retry: null, edit: true, move: false, rotate: true, remove: true });
  assert.equal(dashboard.nodeActions({ ...OUTBOUND, url: 'http://10.0.0.9:3100' }, true).move, false, 'never moved, even with an address');
  assert.deepEqual(dashboard.nodeActions(OUTBOUND, false), { check: true, retry: null, edit: false, move: false, rotate: false, remove: false });
  assert.equal(dashboard.nodeActions(ENTRY, true).move, true, 'direct entries still move');
  assert.equal(dashboard.nodeActions({ ...OUTBOUND, state: 'rotating' }, true).retry, 'Retry access update');
});

await check('invitations: status wording, the confirmation check, and failures that never overstate what happened', () => {
  on();
  const now = 1_000_000;
  const waiting = dashboard.invitationInfo(WAITING, now);
  assert.deepEqual([waiting.label, waiting.open, waiting.text], ['Waiting for first connection', true, 'It works once, and ends in 15 min.']);
  assert.match(dashboard.invitationInfo({ ...WAITING, expiresAt: now - 1 }, now).text, /time is up/u);
  assert.deepEqual([dashboard.invitationInfo(CONNECTED, now).label, dashboard.invitationInfo(CONNECTED, now).open], ['Connected, waiting for confirmation', true]);
  assert.match(dashboard.invitationInfo(CONNECTED, now).text, /reads nothing from it and sets up no access there/u);
  assert.deepEqual([dashboard.invitationInfo({ ...WAITING, state: 'expired' }, now).open, dashboard.invitationInfo({ ...WAITING, state: 'confirmed' }, now).label], [false, 'Added']);
  assert.equal(dashboard.pairingProblem(CONNECTED), null);
  assert.match(dashboard.pairingProblem(WAITING), /hasn’t connected/u);
  assert.match(dashboard.pairingProblem({ ...CONNECTED, node: undefined }), /hasn’t reported/u);
  assert.match(dashboard.pairingProblem({ ...CONNECTED, node: { ...CONNECTED.node, id: 'canary-other' } }), /reported canary-other, not canary-branch\. Don’t add it/u);
  const fail = (status, detail, action) => dashboard.invitationFailure(new api.ApiFailure(status, `HTTP ${status}`, detail), action);
  for (const status of [0, 200]) {
    const lost = fail(status, undefined, 'create');
    assert.deepEqual([lost.title, lost.unknown, lost.reload], ['The result isn’t known yet', true, true]);
    assert.match(lost.text, /its token can’t be shown again: cancel it there and make another/u);
    assert.doesNotMatch(lost.text, /Nothing was/u);
  }
  for (const status of [0, 502, 504]) {
    const lost = fail(status, 'The node did not answer', 'confirm');
    assert.deepEqual([lost.unknown, lost.reload], [true, true], String(status));
    assert.match(lost.text, /may have added the node.*Enrollment unfinished/u);
  }
  const expect = (status, detail, action, title, nothing) => {
    const failure = fail(status, detail, action);
    assert.equal(failure.title, title, `${action} ${status} ${detail}`);
    if (nothing) assert.ok(failure.text.endsWith(nothing), `${action} ${status}: ${failure.text}`);
    else assert.doesNotMatch(failure.text, /Nothing was/u, `${action} ${status}`);
    return failure;
  };
  expect(401, 'Authentication required', 'create', 'Your sign-in to this dashboard ended', 'Nothing was created.');
  expect(403, 'Administrator role required', 'create', 'Only administrators add nodes', 'Nothing was created.');
  expect(404, 'Not found', 'create', 'Node connections aren’t available here', 'Nothing was created.');
  expect(409, 'Node ID is already registered', 'create', 'This node ID is already in use here', 'Nothing was created.');
  expect(409, 'Dashboard permissions changed', 'create', 'Your dashboard permissions changed', 'Nothing was created.');
  expect(429, 'Too many invitations', 'create', 'Too many invitations are waiting', 'Nothing was created.');
  expect(503, 'Node connections are not configured', 'create', 'Node connections aren’t set up on this dashboard', 'Nothing was created.');
  assert.equal(expect(409, 'Invitation already confirmed', 'cancel', 'This node was already added', null).reload, true);
  assert.match(fail(409, 'Invitation already confirmed', 'cancel').text, /never takes a node off the list/u);
  assert.equal(expect(404, 'Not found', 'cancel', 'This invitation is no longer on this dashboard', null).reload, true);
  assert.equal(fail(0, undefined, 'cancel').unknown, true);
  expect(409, 'Invitation expired', 'confirm', 'This invitation expired', 'Nothing was added.');
  assert.equal(expect(409, 'Invitation already confirmed', 'confirm', 'Already added', null).reload, true);
  assert.equal(expect(409, 'Invitation is not connected', 'confirm', 'This invitation changed meanwhile', 'Nothing was added.').reload, true);
  expect(403, 'Administrator role required', 'confirm', 'Only administrators add nodes', 'Nothing was added.');
  assert.equal(dashboard.invitationFailure(new Error('That isn’t a node registration on this dashboard.'), 'create').title, 'Not created');
});

await check('the dashboard’s real invitation refusals, in Go’s own words, read as what happened: never an ID in use or an earlier addition', () => {
  on();
  // Each detail is read from the Go handlers first, so a changed message fails here rather than falling to another title.
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_invitations.go'), 'utf8');
  const http = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_http.go'), 'utf8');
  const fail = (status, detail, action) => dashboard.invitationFailure(new api.ApiFailure(status, `HTTP ${status}`, detail), action);
  const cases = [
    ['create', 409, 'Configure the dedicated HTTPS node connection endpoint first', 'Node connections aren’t set up on this dashboard', 'Nothing was created.'],
    ['create', 409, 'An invitation already exists for this node ID', 'This node ID is already in use here', 'Nothing was created.'],
    ['create', 409, 'This node identity is already registered', 'This node ID is already in use here', 'Nothing was created.'],
    ['create', 409, 'Dashboard node or invitation capacity exceeded', 'No room for another node', 'Nothing was created.'],
    ['create', 403, 'Dashboard administrator permission is required', 'Only administrators add nodes', 'Nothing was created.'],
    ['cancel', 409, 'This invitation is already paired; use Remove node to revoke access', 'This node was already added', null],
    ['cancel', 404, 'Node invitation not found', 'This invitation is no longer on this dashboard', null],
    ['confirm', 409, 'A current connected invitation must be confirmed', 'This invitation changed meanwhile', 'Nothing was added.'],
    ['confirm', 409, 'Dashboard node capacity exceeded', 'No room for another node', 'Nothing was added.'],
    ['confirm', 409, 'This node identity is already registered', 'Already added', null],
    ['confirm', 409, 'Node invitation is no longer authorized', 'This invitation is no longer valid', 'Nothing was added.'],
    ['confirm', 503, 'Pending pairing credentials are unavailable', 'The dashboard’s server can’t add it right now', 'Nothing was added.'],
    ['confirm', 400, 'Confirm pairing takes no request body', 'Not added', 'Nothing was added.'],
  ];
  for (const [action, status, detail, title, nothing] of cases) {
    assert.ok(go.includes(`fail(${status}, "${detail}")`), `Go still answers ${status} "${detail}"`);
    const failure = fail(status, detail, action);
    assert.equal(failure.title, title, `${action} ${status} ${detail}`);
    if (nothing) assert.ok(failure.text.endsWith(nothing), `${action} ${status}: ${failure.text}`);
    else assert.doesNotMatch(failure.text, /Nothing was/u, `${action} ${status}`);
  }
  // Go's "must be confirmed" means expired, cancelled or no longer connected; before r3 its "confirmed" read as "Already added".
  const changed = fail(409, 'A current connected invitation must be confirmed', 'confirm');
  assert.deepEqual([changed.reload, changed.unknown], [true, false]);
  assert.doesNotMatch(`${changed.title} ${changed.text}`, /Already|already added/u);
  assert.match(fail(409, 'Dashboard node or invitation capacity exceeded', 'create').text, /Cancel unused invitations under “Waiting to connect”, or remove a node\./u);
  assert.ok(http.includes('fail(409, "Dashboard permissions changed; sign in again")'), 'Go’s changed-session answer');
  for (const action of ['create', 'cancel', 'confirm']) assert.equal(fail(409, 'Dashboard permissions changed; sign in again', action).title, 'Your dashboard permissions changed', action);
});

await check('node connections off: setup remains selectable while Go still refuses invitations until configured', async () => {
  on();
  // The user explicitly requested access to setup before the endpoint is configured. This supersedes only the old
  // disabled-radio expectation; the server must still refuse invitations without a connection address.
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_invitations.go'), 'utf8');
  assert.match(go, /"outboundEnabled": d\.connectionEnabled && d\.connectionURL != ""/u, 'Go offers outbound only with an address');
  assert.match(go, /if !d\.connectionEnabled \|\| d\.connectionURL == "" \{\s*errorReply\(w, fail\(409, "Configure the dedicated HTTPS node connection endpoint first"\)\)/u, 'Go refuses to invite without it');
  const disabled = api.normalizeOnboarding({ ...CAPS, outboundEnabled: false, connectionUrl: undefined });
  assert.deepEqual([disabled.outboundEnabled, 'connectionUrl' in disabled], [false, false]);
  const chooser = onboarding => within('admin', [], React.createElement(wizard.AddNodeChooser, { plan: { fresh: true, link: 'direct' }, onPlan: noop, onboarding }));
  const radiosOf = markup => tags(markup, 'input').map(attributes).filter(found => found.get('type') === 'radio').map(found => found.has('disabled'));
  const off = chooser(resource(disabled));
  assert.deepEqual(radiosOf(off), [false, false, false, false], 'setup is available before an address is configured');
  assert.match(text(off), /The node reaches this dashboard Not set up here: this dashboard’s deployment has no node-connection address\./u);
  const configured = chooser(resource(CAPS));
  assert.deepEqual(radiosOf(configured), [false, false, false, false], 'with an address it can be picked');
  assert.match(text(configured), /The node reaches this dashboard The node connects out over HTTPS and needs no inbound port/u);
  const unread = chooser({ ...resource(undefined), loading: false, error: new api.ApiFailure(503, 'HTTP 503', 'Unavailable') });
  assert.deepEqual(radiosOf(unread), [false, false, false, false], 'a failed read does not block access to setup help');
  assert.match(text(unread), /didn’t say whether nodes can connect to it\..*This dashboard’s node setup couldn’t be read.*Adding a running node that this dashboard reaches still works\./u);
  const client = new api.CanaryApi(PAGE, TOKEN);
  let created;
  const refused = await rejection(record(async () => { created = await client.createInvitation({ label: 'Branch office', nodeId: 'canary-branch' }); },
    () => json(409, { error: 'Configure the dedicated HTTPS node connection endpoint first' })));
  assert.equal(created, undefined, 'no invitation and no token');
  assert.ok(refused instanceof api.ApiFailure);
  assert.deepEqual([refused.status, refused.detail], [409, 'Configure the dedicated HTTPS node connection endpoint first']);
  const failure = dashboard.invitationFailure(refused, 'create');
  assert.equal(failure.title, 'Node connections aren’t set up on this dashboard');
  assert.ok(failure.text.endsWith('Nothing was created.'), failure.text);
  assert.doesNotMatch(failure.text, /in use|token/u);
});

await check('Add node opens on the running-node form, exactly as before, with the new choices above it', () => {
  on();
  reached.length = 0;
  const markup = within('admin', [ENTRY], React.createElement(registryView.AddNodeDialog, { onClose: noop }));
  const words = text(markup);
  for (const phrase of ['A node that’s already running', 'Deploy a new node', 'This dashboard reaches the node', 'The node reaches this dashboard', 'Checking whether nodes can connect to this dashboard…',
    'Name on this dashboard', 'Node address', 'Enrollment credential', 'Verify node']) assert.ok(words.includes(phrase), phrase);
  const radios = tags(markup, 'input').map(attributes).filter(found => found.get('type') === 'radio');
  assert.deepEqual(radios.map(found => [found.has('checked'), found.has('disabled')]), [[true, false], [false, false], [true, false], [false, false]], 'running node and direct remain chosen; outbound setup is also available');
  assert.equal(tags(markup, 'input').map(attributes).filter(found => found.get('type') === 'password').length, 1, 'one credential field');
  assert.doesNotMatch(words, /Create invitation|Next: verify the node|Waiting to connect/u);
  assert.match(markup, /<dialog[^>]*class="modal wide"/u, 'the running-node form keeps its width');
  assert.deepEqual(reached, [], 'rendering asks the API nothing');
});

await check('wizard pieces render masked: no private value in markup; tabs, Show, Copy and Download are labelled for each file', () => {
  on();
  for (const { value: platform, label } of onboarding.PLATFORMS) {
    const markup = within('admin', [], React.createElement(wizard.SetupGuide, { setup: combos[1].setup, platform, onPlatform: noop, onHandled: noop }));
    for (const value of PRIVATE) assert.ok(!markup.includes(value), `${platform}: a private value is in the markup`);
    assert.ok(markup.includes(onboarding.MASK), platform);
    const tabs = tags(markup, 'button').map(attributes).filter(found => found.get('role') === 'tab');
    assert.equal(tabs.length, 4);
    assert.deepEqual(tabs.filter(found => found.get('aria-selected') === 'true').map(found => found.get('tabindex')), ['0'], platform);
    assert.match(markup, new RegExp(`aria-selected="true"[^>]*>${label}<`, 'u'), platform);
    const buttons = tags(markup, 'button').map(attributes);
    const files = onboarding.setupGuide({ ...combos[1].setup, platform }, false).filter(block => block.file);
    for (const block of files) {
      const name = block.file;
      assert.ok(buttons.some(found => found.get('aria-label') === `Download ${name}`), `${platform}: Download ${name}`);
      assert.ok(buttons.some(found => found.get('aria-label') === `Copy ${name}`), `${platform}: Copy ${name}`);
      if (block.secret) assert.ok(buttons.some(found => found.get('aria-label') === `Show private values in ${name}` && found.get('aria-pressed') === 'false'), `${platform}: Show ${name}`);
    }
  }
  const draft = { label: 'Office', nodeId: 'canary-office', address: 'http://100.79.12.4:3100', bindAddress: '100.79.12.4', platform: 'docker' };
  const deploy = props => within('admin', [ENTRY], React.createElement(wizard.NewNodeDeploy, { draft, onDraft: noop, secrets: SECRETS, onboarding: resource(CAPS), taken: { nodes: [ENTRY], invitations: [] }, onNext: noop, onCancel: noop, ...props }));
  const ready = deploy({});
  for (const value of PRIVATE) assert.ok(!ready.includes(value));
  assert.match(text(ready), /-p 100\.79\.12\.4:3100:3100.*Next: verify the node/u);
  assert.match(text(ready), /publish port 3100 on 100\.79\.12\.4 only/u);
  assert.match(text(deploy({ draft: { ...draft, address: '' } })), /Complete the fields above first.*Show, Copy and Download are disabled/u);
  assert.match(text(deploy({ draft: { ...draft, nodeId: 'canary-b' } })), /Complete the fields above first.*Already on this dashboard as “Rack B”\./u);
  assert.match(text(deploy({ onboarding: resource(undefined) })), /Reading this dashboard’s image tag…/u);
  assert.match(text(deploy({ draft: { ...draft, address: 'http://canary-b.lan:3100' } })), /publish port 3100 on 100\.79\.12\.4 only/u);
  const guard = { current: null };
  const outbound = props => within('admin', [ENTRY], React.createElement(wizard.OutboundNode, {
    draft: { label: '', nodeId: '', address: '', platform: 'docker' }, onDraft: noop,
    fresh: true, onboarding: resource(CAPS), secrets: SECRETS, taken: { nodes: [ENTRY], invitations: [] }, platform: 'docker', onPlatform: noop, onClose: noop, onRequestClose: noop,
    onLock: noop, onBusy: noop, guard, onTakenChanged: noop, ...props,
  }));
  const identify = text(outbound({}));
  const retained = outbound({ draft, onDraft: noop, platform: 'portainer', secrets: { ...SECRETS, nodeId: draft.nodeId } });
  const alternate = { ...draft, dashboardAddress: 'https://alternate.example.test:18348' };
  const changedAddress = props => outbound({ draft: alternate, secrets: { ...SECRETS, nodeId: draft.nodeId }, ...props });
  const unpinned = changedAddress({});
  assert.match(text(unpinned), /A different address requires this dashboard’s certificate fingerprint/u);
  const generateButton = markup => (markup.match(/<button\b[^>]*>[\s\S]*?<\/button>/gu) ?? []).find(button => text(button).trim() === 'Generate setup');
  assert.ok(attributes(tags(generateButton(unpinned), 'button')[0]).has('disabled'), 'an unpinned alternate destination cannot generate setup');
  const pinned = changedAddress({ onboarding: resource({ ...CAPS, connectionTlsFingerprint: 'b'.repeat(64) }) });
  assert.ok(!attributes(tags(generateButton(pinned), 'button')[0]).has('disabled'), 'a pinned alternate destination can generate complete setup');
  assert.ok(tags(retained, 'input').map(attributes).some(input => input.get('value') === draft.nodeId), 'switching from direct setup retains the entered Node ID');
  assert.ok(tags(retained, 'button').map(attributes).some(button => button.get('aria-label') === 'Copy value for CANARY_ADMIN_TOKEN' && !button.has('disabled')), 'the complete retained draft enables export; the first action generates its invitation');
  assert.ok(identify.includes(CONNECT) && /Generate setup/u.test(identify) && /No inbound port or firewall opening is needed on the node/u.test(identify));
  assert.match(identify, /Complete the fields above first.*Show, Copy and Download are disabled/u, 'the fresh outbound flow explains why its visible actions are disabled');
  assert.doesNotMatch(identify, new RegExp(PAGE.replace(/[.]/gu, '\\.'), 'u'), 'the page’s own address is never offered as the connection address');
  assert.equal(typeof guard.current, 'function');
  assert.equal(guard.current(), true, 'nothing made yet, so closing needs no question');
  assert.match(text(outbound({ onboarding: resource({ ...CAPS, outboundEnabled: false, connectionUrl: undefined }) })), /Set up this dashboard for incoming node connections.*Not set up here/u);
  assert.match(text(outbound({ onboarding: resource({ ...CAPS, outboundEnabled: false, connectionUrl: undefined }) })), /Name on this dashboard.*Node ID/u, 'identity fields are available before endpoint setup');
  for (const { value: platform } of onboarding.PLATFORMS) {
    const beforeSetup = outbound({ platform, onboarding: resource({ ...CAPS, outboundEnabled: false, connectionUrl: undefined }) });
    const incompleteDirect = deploy({ draft: { ...draft, label: '', nodeId: '', address: '', platform } });
    const incompleteOutbound = outbound({ platform });
    for (const [direction, markup] of [['direct', incompleteDirect], ['outbound', incompleteOutbound]]) {
      const actions = tags(markup, 'button').map(attributes).filter(button => /^(?:Show private values in|Copy |Download )/u.test(button.get('aria-label') ?? ''));
      assert.ok(actions.length >= 3, `${platform} ${direction}: setup actions remain visible before required fields are entered`);
      assert.ok(actions.every(button => button.has('disabled')), `${platform} ${direction}: incomplete setup actions are disabled`);
    }
    assert.equal(tags(beforeSetup, 'button').map(attributes).filter(found => found.get('role') === 'tab').length, 4, `${platform}: deployment choices before dashboard readiness`);
    assert.doesNotMatch(text(beforeSetup), /Create invitation/u, 'no invitation action without an endpoint');
    const preview = within('admin', [], React.createElement(wizard.NodeDeploymentPreview, { capabilities: CAPS, platform, onPlatform: noop, reason: 'Enter a name and a unique Node ID.' }));
    assert.match(text(preview), /Complete the fields above first.*Preview only: do not deploy these settings/u, `${platform}: missing prerequisites and preview state are explained`);
    assert.ok(preview.includes(onboarding.MASK), `${platform}: disabled secret previews stay masked`);
    const controls = tags(preview, 'button').map(attributes);
    assert.ok(controls.filter(found => found.get('role') !== 'tab').every(found => found.has('disabled')), `${platform}: every preview action is disabled`);
    assert.ok(controls.filter(found => found.get('role') === 'tab').every(found => !found.has('disabled')), `${platform}: platform selection remains enabled`);
    assert.ok(controls.find(found => found.get('id')?.endsWith(`-t-${platform}`)).get('aria-selected') === 'true', `${platform}: selected preview method`);
    for (const value of PRIVATE) assert.ok(!preview.includes(value), `${platform}: preview uses no real node or invitation credentials`);
    const blocks = onboarding.setupGuide({ nodeId: 'canary-outbound', name: 'Branch', imageTag: CAPS.imageTag, secrets: SECRETS,
      outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN }, platform }, false);
    assert.doesNotMatch(blocks.map(block => block.text).join('\n'), /\s-p\s|--publish|^\s*ports:/mu, `${platform}: outbound nodes publish no inbound port`);
  }
  const unreadPreview = outbound({ onboarding: resource(undefined) });
  assert.match(text(unreadPreview), /Reading this dashboard’s image tag/u, 'an unread image tag is explained while setup actions remain disabled');
  const failedRead = { ...resource(undefined), loading: false, error: new Error('failed') };
  for (const markup of [outbound({ onboarding: failedRead }), deploy({ onboarding: failedRead })]) {
    assert.doesNotMatch(text(markup), /Reading this dashboard’s image tag|Wait for the dashboard’s image tag to load/u, 'a failed capability read is not announced as still loading');
  }
  const existingWithoutEndpoint = outbound({ fresh: false, onboarding: resource({ ...CAPS, outboundEnabled: false, connectionUrl: undefined }) });
  assert.equal(tags(existingWithoutEndpoint, 'button').map(attributes).filter(found => found.get('role') === 'tab').length, 0, 'running nodes are not offered a fresh deployment');
  assert.match(text(outbound({ fresh: false })), /Its current CANARY_NODE_ID\. The dashboard refuses a node that connects with any other ID\./u);
  assert.doesNotMatch(text(outbound({ fresh: false })), /Preview only: do not deploy these settings/u, 'running nodes retain their existing deployment');
  assert.deepEqual(reached, [], 'rendering asks the API nothing');
});

await check('outbound first-start exports include bootstrap credentials and the real invitation in one deployment', () => {
  on();
  const nodeId = 'canary-office';
  const setup = { nodeId, name: 'Office', imageTag: CAPS.imageTag, secrets: { ...SECRETS, nodeId }, outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN } };
  for (const { value: platform } of onboarding.PLATFORMS) {
    const masked = onboarding.setupGuide({ ...setup, platform }, false);
    const real = onboarding.setupGuide({ ...setup, platform }, true);
    const all = real.map(block => block.text).join('\n');
    assert.ok(all.includes(ADMIN) && all.includes(CLUSTER), `${platform}: actual bootstrap credentials in exported settings`);
    assert.ok(!all.includes(onboarding.MASK), `${platform}: exporting never copies a mask`);
    assert.ok(all.includes(CONNECT) && all.includes(INVITE_TOKEN), `${platform}: the first export contains its real dashboard address and invitation`);
    assert.doesNotMatch(all, /NODE_REACHABLE_HOST|INVITATION_TOKEN|--publish|\s-p\s|^\s*ports:/mu, `${platform}: no fake connector values or inbound mapping`);
    for (const token of [ADMIN, CLUSTER]) assert.ok(!masked.map(block => block.text).join('\n').includes(token), `${platform}: preview remains masked`);
    const markup = within('admin', [], React.createElement(wizard.NewOutboundDeploy, {
      capabilities: CAPS, nodeId, name: 'Office', secrets: setup.secrets,
      platform, onPlatform: noop, onUse: noop, taken: { nodes: [], invitations: [] },
      prepare: async () => setup,
    }));
    const buttons = tags(markup, 'button').map(attributes);
    assert.ok(buttons.some(button => (button.get('aria-label') ?? '').startsWith('Show private values in ')), `${platform}: Show stays visible before an invitation`);
    assert.ok(buttons.some(button => (button.get('aria-label') ?? '').startsWith('Copy ')), `${platform}: Copy is available`);
    assert.ok(buttons.some(button => (button.get('aria-label') ?? '').startsWith('Download ')), `${platform}: Download is available`);
    assert.ok(buttons.filter(button => button.get('role') !== 'tab').every(button => !button.has('disabled')), `${platform}: valid fields enable actions that prepare a complete deployment on demand`);
    for (const token of PRIVATE) assert.ok(!markup.includes(token), `${platform}: no private value in masked markup`);
    assert.throws(() => onboarding.setupGuide({ ...setup, secrets: { ...SECRETS, nodeId: 'different-node' }, platform }, true), /already used/u);
    assert.throws(() => onboarding.setupGuide({ ...setup, direct: onboarding.directTarget('https://node.example.test:3100'), platform }, true), /Choose how/u);
    const existing = onboarding.setupGuide({ ...setup, secrets: null, platform }, true).map(block => block.text).join('\n');
    assert.ok(!existing.includes(ADMIN) && !existing.includes(CLUSTER), `${platform}: an existing deployment keeps its own bootstrap credentials`);
  }
  for (const [input, expected] of [
    ['192.0.2.10', 'https://192.0.2.10:18348'],
    ['office.example', 'https://office.example:18348'],
    ['office.example:443', 'https://office.example'],
    ['office.example:9443', 'https://office.example:9443'],
    ['office.example:9443/', 'https://office.example:9443'],
    ['[2001:db8::10]', 'https://[2001:db8::10]:18348'],
    ['[2001:db8::10]:443', 'https://[2001:db8::10]'],
    ['https://office.example:9443/', 'https://office.example:9443'],
    ['http://office.example', null],
    ['https://user:secret@office.example', null],
    ['https://office.example/path', null],
  ]) assert.equal(onboarding.outboundConnectionAddress(input, 'https://configured.example:18348'), expected);
  for (const host of AMBIGUOUS_IPV4) {
    for (const input of [host, `https://${host}`]) assert.equal(onboarding.outboundConnectionAddress(input, 'https://configured.example:18348'), null, `${input}: no rewritten destination`);
  }
});

await check('outbound setup explains which stack to configure and offers a refresh before invitations are possible', () => {
  on();
  const setup = onboarding => within('admin', [], React.createElement(wizard.OutboundDashboardSetup, { onboarding, onClose: noop }));
  const refreshButton = markup => {
    const button = (markup.match(/<button\b[^>]*>[\s\S]*?<\/button>/gu) ?? []).find(button => /^Check(?:ing)? dashboard setup/u.test(text(button).trim()));
    assert.ok(button, 'the dashboard setup refresh button is present');
    return attributes(tags(button, 'button')[0]);
  };
  const off = setup(resource({ ...CAPS, outboundEnabled: false, connectionUrl: undefined }));
  const words = text(off);
  for (const phrase of ['existing dashboard service: web', 'ghcr.io/narehood/uptime-canary-web', 'CANARY_DASHBOARD_CONNECTION_URL: https://NODE_REACHABLE_HOST:18348',
    'CANARY_DASHBOARD_CONNECTION_TLS_MODE: auto', 'CANARY_DASHBOARD_CONNECTION_HOST: 0.0.0.0', 'DASHBOARD_BIND_IP:18348:8081',
    'These may differ behind NAT or a VPN',
    'keep its other settings, browser port, data and identity volumes', 'Check dashboard setup', 'No invitation is created until the dashboard is ready']) assert.ok(words.includes(phrase), phrase);
  assert.equal(refreshButton(off).has('disabled'), false, 'setup can be checked now');
  const loading = setup(resource(undefined));
  assert.match(text(loading), /Checking dashboard setup…/u);
  assert.equal(refreshButton(loading).has('disabled'), true, 'one check at a time');
  const failedRead = setup({ ...resource(undefined), loading: false, error: new api.ApiFailure(503, 'HTTP 503', 'Unavailable') });
  assert.match(text(failedRead), /Check dashboard setup/u);
  assert.equal(refreshButton(failedRead).has('disabled'), false, 'a failed check can be retried');
  assert.match(failedRead, /role="alert"/u, 'a failed check is visible and can be retried');
  for (const markup of [off, loading, failedRead]) {
    for (const value of PRIVATE) assert.ok(!markup.includes(value), 'dashboard prerequisites need no node credentials');
    assert.ok(!text(markup).includes(PAGE), 'the browser origin is never guessed as a connection origin');
    assert.doesNotMatch(text(markup), /Create invitation/u, 'setup help cannot create an invitation prematurely');
  }
  assert.deepEqual(reached, [], 'opening setup help performs no writes');
});

await check('first-pair confirmation shows what the node proved, and refuses to offer a node with another ID', () => {
  on();
  // Measurement fix (r3). The views only ever get invitations from api.ts, whose normalizeInvitation lowercases a source
  // commit (the API group above pins 'ABCDEF0123456789' -> 'abcdef0123456789'). The first frozen run rendered the raw
  // fixture value instead, so React wrote `<dd>0.19.0<span class="mono muted"> · <!-- -->ABCDEF012345</span></dd>`, which
  // text() reads as '0.19.0 · ABCDEF012345'. The expected '0.19.0 · abcdef012345' was right; the input skipped the
  // normalizer. Nothing in that markup is escaped (React escapes only & < > " ' in text, and text() decodes those), and its
  // structure is as asserted: the version, then the commit's first 12 characters in their own span. Every invitation
  // below goes through the normalizer first, as in the page.
  const normalized = invitation => api.normalizeInvitation(invitation);
  const markup = invitation => within('admin', [], React.createElement(wizard.PairingDialog, { invitation: normalized(invitation), onClose: noop, onChanged: noop }));
  const review = invitation => text(markup(invitation));
  assert.equal(normalized(CONNECTED).node.sourceCommit, 'abcdef0123456789', 'what the dialog receives');
  const connected = markup(CONNECTED);
  const shown = text(connected);
  for (const phrase of ['Is this your node?', 'canary-branch · the ID this invitation expects', 'Branch', '0.19.0 · abcdef012345', FINGERPRINT, 'Not my node: cancel invitation',
    'The node prints the same fingerprint in its log']) assert.ok(shown.includes(phrase), phrase);
  assert.match(connected, /<dd>0\.19\.0<span class="mono muted"> · (?:<!-- -->)?abcdef012345<\/span><\/dd>/u, 'the version and short commit, as rendered');
  assert.match(connected, /Add “Branch office”<\/button>/u, 'the confirm button');
  // Add waits for an explicit check that the fingerprint matches the node's own log; rendering starts unticked.
  const checks = tags(connected, 'input').map(attributes).filter(found => found.get('type') === 'checkbox');
  assert.equal(checks.length, 1, 'one fingerprint check');
  assert.ok(!checks[0].has('checked') && !checks[0].has('disabled'), 'unticked and usable');
  assert.ok(shown.includes('This fingerprint matches the one in the node’s log Its log line starts with “Node connection fingerprint”.'), 'what to compare, and where');
  // The opening tag of the button whose content (its icon, then the words) ends in the Add label, with no other button
  // in between.
  const add = /(<button\b[^>]*>)(?:(?!<\/?button\b)[\s\S])*?Add “Branch office”<\/button>/u.exec(connected)?.[1];
  assert.ok(add, 'one Add button');
  assert.ok(attributes(add).has('disabled'), 'Add is off until the fingerprint check is ticked');
  assert.doesNotMatch(shown, /source address:|from \d+\.\d+\.\d+\.\d+/u, 'no source address claim');
  const otherInvitation = { ...CONNECTED, node: { ...CONNECTED.node, id: 'canary-other' } };
  assert.match(review(otherInvitation), /Don’t add this node.*reported canary-other, not canary-branch/u);
  assert.doesNotMatch(markup(otherInvitation), /Add “Branch office”<\/button>/u, 'no confirm button for another ID');
  assert.equal(tags(markup(otherInvitation), 'input').map(attributes).filter(found => found.get('type') === 'checkbox').length, 0, 'nothing to tick for another ID');
  assert.match(review(WAITING), /Waiting for first connection.*isn’t waiting for a confirmation any more/u);
  const source = readFileSync(resolve(web, 'src/views/NodeWizard.tsx'), 'utf8');
  assert.match(source, /disabled=\{busy \|\| !compared\}/u, 'Add needs the tick in both the wizard and the Review dialog');
  assert.equal((source.match(/<PairingCard key=\{`\$\{current\.id\}\|\$\{current\.node\?\.fingerprint \?\? ''\}`\}/gu) ?? []).length, 2, 'a different fingerprint starts unticked');
});

await check('node cards: an outbound entry has a tag and last connection, no address row and no move; direct cards are as before', () => {
  on();
  const markup = within('admin', [ENTRY, OUTBOUND], React.createElement(registryView.RegistryView));
  const out = text(card(markup, OUTBOUND.id));
  for (const phrase of ['Connects to this dashboard', 'Answering. Connected. The dashboard’s server reads it over the node’s own connection.', 'Last connected', 'Check now', 'Rotate access', 'Remove']) assert.ok(out.includes(phrase), phrase);
  assert.doesNotMatch(card(markup, OUTBOUND.id), />Address</u);
  assert.match(card(markup, ENTRY.id), />Address</u);
  assert.doesNotMatch(card(markup, ENTRY.id), /Connects to this dashboard|Last connected/u);
  assert.match(markup, /aria-label="Nodes that connect to this dashboard open their own HTTPS connection to its node-connection address\./u);
  assert.match(text(markup), /Last connected is when a node that connects to this dashboard last opened its connection; it says nothing about its checks\./u);
  assert.equal((text(markup).match(/Remove/gu) ?? []).length, 2, 'Remove on both added nodes');
  assert.doesNotMatch(text(markup), /Waiting to connect/u, 'the invitations card shows only once loaded');
  const plain = text(within('admin', [ENTRY], React.createElement(registryView.RegistryView)));
  assert.doesNotMatch(plain, /node-connection address|Last connected|Connects to this dashboard/u, 'nothing changes without outbound nodes');
  const viewer = within('viewer', [ENTRY, OUTBOUND].map(({ url: _address, ...node }) => node), React.createElement(registryView.RegistryView));
  assert.doesNotMatch(viewer, />Add node<|>Remove<|Waiting to connect|100\.79\.12\.4/u);
  const edit = within('admin', [OUTBOUND], React.createElement(registryView.EditNodeDialog, { node: OUTBOUND, onClose: noop }));
  assert.equal(tags(edit, 'input').length, 1, 'only the name');
  assert.match(text(edit), /This node connects to the dashboard itself, so its entry has no address to edit\. Renaming works even while it isn’t connected\./u);
  assert.doesNotMatch(text(edit), /Node address|bundled node/u);
  const remove = node => text(within('admin', [node], React.createElement(registryView.RemoveNodeDialog, { node, onClose: noop })));
  assert.match(remove(OUTBOUND), /stops using the node’s connection for anything else at once, and asks the node over it to revoke.*A change sent to the node just before may still finish there\./u);
  assert.match(remove({ ...OUTBOUND, state: 'not-answering' }), /isn’t connected right now.*Removal unfinished.*may still finish there/u);
  assert.match(remove(ENTRY), /The dashboard’s server also asks the node to revoke the access this dashboard set up there\./u);
  assert.doesNotMatch(remove(ENTRY), /connection/u, 'direct removal wording is unchanged');
});

await check('sources: no storage, console, page address or navigation in the wizard; routes through own(); copy failure is explicit', () => {
  for (const path of ['src/onboarding.ts', 'src/views/NodeWizard.tsx']) {
    assert.doesNotMatch(read(path), /\bfetch\(|localStorage|sessionStorage|indexedDB|document\.cookie|console\.|navigate\(|href\(|pageOrigin|location\.|window\.open/u, path);
  }
  for (const path of ['src/views/Registry.tsx', 'src/dashboard.ts', 'src/api.ts']) assert.doesNotMatch(read(path), /localStorage|sessionStorage|indexedDB|document\.cookie/u, path);
  const wizardSource = read('src/views/NodeWizard.tsx');
  for (const [, body] of wizardSource.matchAll(/notify\(([^;]*)\);/gu)) assert.doesNotMatch(body, /\btoken\b|secrets|\.admin\b|\.cluster\b/u, `notice: ${body}`);
  assert.match(wizardSource, /URL\.revokeObjectURL\(url\)/u, 'the in-memory download link is revoked');
  const apiSource = read('src/api.ts');
  const method = name => {
    const start = apiSource.search(new RegExp(`\\n  (?:async )?${name}\\(`, 'u'));
    assert.ok(start > 0, name);
    return apiSource.slice(start, apiSource.indexOf('\n  }\n', start + 1));
  };
  for (const name of ['onboarding', 'invitations', 'createInvitation', 'invitation', 'cancelInvitation', 'confirmInvitation']) {
    assert.match(method(name), /this\.own/u, name);
    assert.doesNotMatch(method(name), /this\.call|fetch\(/u, name);
  }
  assert.match(method('confirmInvitation'), /this\.own<unknown>\(path, 'POST', undefined, signal, NODE_CHANGE_TIMEOUT_MS\)/u);
  const bits = read('src/ui/bits.tsx');
  assert.equal((bits.match(/setCopied\('failed'\)/gu) ?? []).length, 2, 'no clipboard, and a refused copy');
  assert.match(bits, /Couldn’t copy/u);
  const registry = read('src/views/Registry.tsx');
  assert.match(registry, /useState<AddPlan>\(\{ fresh: false, link: 'direct' \}\)/u, 'the running-node form is the default');
  assert.match(registry, /if \(!plan\.fresh && plan\.link === 'direct'\) body = <DirectAddForm onClose=\{onClose\} onBusy=\{hold\} onStage=\{lock\} \/>;/u);
  // Accepted change (TLS r1): the verification also passes the optional TLS certificate fingerprint field. api.verifyNode
  // leaves it out of the body when it's empty (the TLS API group below pins that body), and the pasted credential is
  // still cleared on the line right after.
  assert.match(registry, /api\.verifyNode\(\{ label, url, token: sent, tlsFingerprint: pin \}, controller\.signal\);\n\s+setToken\(''\);/u, 'a pasted credential is still cleared after each verification');
  assert.match(registry, /discardVerification/u);
});

// ----- r5 (review .canary/node-onboarding-ui-final-review.md M1-M4, L1 and L3-L7). Every group below is new; no earlier
// expectation changed.

await check('a running node’s steps: before any setting changes, they say when the node refuses to start and how to undo it; settings it has already are replaced, never added twice', () => {
  // Go's own refusals: prepareConnector (node_connector.go) refuses a CANARY_ADMIN_TOKEN that isn't a current local
  // administrator, and a saved identity for another node-connection address; canary.New returns the error and main.go
  // exits with it, so under a restart policy the node keeps failing and doesn't monitor.
  const connector = readFileSync(resolve(root, 'apps/node-go/internal/canary/node_connector.go'), 'utf8');
  const main = readFileSync(resolve(root, 'apps/node-go/cmd/canary/main.go'), 'utf8');
  assert.ok(connector.includes('errors.New("The node invitation requires a current local administrator token")'), 'Go refuses a token that isn’t a current administrator');
  assert.ok(connector.includes('errors.New("Retained node connection identity does not match this node and dashboard")'), 'Go refuses another saved connection address');
  assert.ok(main.includes('"Uptime-Canary: Invalid startup configuration:"'), 'main.go exits with these words');
  const image = onboarding.imageReference(CAPS.imageTag);
  const refused = '“Invalid startup configuration” if it refused to start: then undo the change as step 1 says.';
  for (const { value: platform } of onboarding.PLATFORMS) {
    const blocks = onboarding.setupGuide({ ...combos[2].setup, platform }, false);
    const where = `running node, ${platform}`;
    const res = onboarding.resourceName(combos[2].setup.nodeId);
    assert.equal(blocks[0].key, 'keep', `${where}: the warning is step 1`);
    assert.ok(blocks.findIndex(block => /CANARY_DASHBOARD_URL[=:]/u.test(block.text)) > 0, `${where}: every setting comes after it`);
    const keep = blocks[0].text;
    for (const phrase of [
      'Its CANARY_ADMIN_TOKEN must be a current administrator token on it, such as the token of an administrator account there: the node checks it when it starts with these settings',
      'If that token was changed or removed since, or is empty, or the node connected through another node-connection address before, the node refuses to start (its log says “Invalid startup configuration”) and doesn\'t monitor until you undo the change',
      'remove both CANARY_DASHBOARD_URL and CANARY_DASHBOARD_ENROLL_TOKEN again and start it as before, with its data, identity, names and tokens as they were.',
      platform === 'vm' ? 'first update it to a build of the matching source, keeping its state directory.' : `first update it to ${image}, keeping its volumes; pull that tag again if the host has it already.`,
    ]) assert.ok(keep.includes(phrase), `${where}: ${phrase}`);
    const step = key => blocks.find(block => block.key === key);
    if (platform === 'docker') {
      assert.ok(step('recreate').text.startsWith(`Put both lines in the node's env file, replacing any it has already, or add --env-file ${res}-connect.env to its docker run command if it has neither.`), where);
      assert.ok(step('recreate').text.endsWith(refused), `${where}: what a refusal looks like, and where its undo is`);
    }
    if (platform === 'compose' || platform === 'portainer') {
      const portainer = platform === 'portainer';
      const yaml = step('yaml');
      assert.equal(yaml.title, `Add to ${portainer ? 'the stack YAML' : 'compose.yaml'}, under the node service’s environment, unless it has these settings`, where);
      // The KEY: value lines and the list form name the same two settings with the same values.
      const mapping = Object.fromEntries(yaml.text.trimEnd().split('\n').map(line => /^ {6}([A-Z_]+): (.*)$/u.exec(line)).map(found => [found[1], found[2]]));
      assert.deepEqual(Object.keys(mapping), ['CANARY_DASHBOARD_URL', 'CANARY_DASHBOARD_ENROLL_TOKEN']);
      assert.equal(composeValue(mapping.CANARY_DASHBOARD_URL), CONNECT);
      const list = step('list');
      assert.deepEqual([list.kind, list.secret, list.title], ['note', false, 'If its environment is a list'], where);
      assert.ok(list.text.includes(`In a list of - KEY=value entries, add - CANARY_DASHBOARD_URL=${CONNECT} and - CANARY_DASHBOARD_ENROLL_TOKEN=${mapping.CANARY_DASHBOARD_ENROLL_TOKEN} instead.`), `${where}: ${list.text}`);
      assert.equal(mapping.CANARY_DASHBOARD_ENROLL_TOKEN, '${CANARY_DASHBOARD_ENROLL_TOKEN:-}');
      assert.equal(step('env').title, portainer ? 'Add this environment variable to the stack, or replace its earlier value' : 'Put this line in its .env file, or the file its Compose commands pass with --env-file, replacing an earlier one', where);
      if (portainer) {
        assert.ok(step('update').text.startsWith('Choose Update the stack, with re-pull of the image if you changed its tag or this host may have an older copy of it.'), where);
        assert.ok(step('update').text.endsWith(refused), where);
      } else {
        assert.deepEqual([step('up').title, step('up').text], ['Apply it, adding the --env-file option its Compose commands use, if any', 'docker compose up -d\ndocker compose logs node\n'], where);
      }
    }
    if (platform === 'vm') {
      assert.equal(step('env').title, 'Add these lines to the node’s EnvironmentFile, replacing any earlier ones', where);
      assert.ok(step('restart').text.endsWith(refused), where);
    }
  }
});

await check('new-node steps pull the named image before starting it, and every command names the private file the page downloads', () => {
  for (const { name, platform, setup, real, masked } of guides.filter(guide => guide.setup.secrets)) {
    const where = `${name}, ${platform}`;
    const res = onboarding.resourceName(setup.nodeId);
    const image = onboarding.imageReference(setup.imageTag);
    const env = real.find(block => block.key === (platform === 'portainer' ? 'variables' : 'env'));
    assert.equal(env.file, `${res}.env`, `${where}: the private file’s download name`);
    const all = masked.map(block => `${block.title}\n${block.text}`).join('\n');
    // Round trip: every file a command or title names is the downloaded one, and no bare .env stands in for it.
    // (Portainer loads variables from a file of any name; its own label says ".env file".)
    for (const [, file] of all.matchAll(/(?:--env-file|chmod 600|private settings as) (\S+?)(?=[\s,]|$)/gmu)) assert.equal(file, env.file, `${where}: ${file}`);
    if (platform !== 'portainer') assert.doesNotMatch(all, /(?<![\w-])\.env\b/u, `${where}: no other settings file`);
    if (platform === 'docker') {
      const lines = real.find(block => block.key === 'run').text.split('\n');
      // Accepted command syntax change after R6: the same first two steps, each now ending in " &&", so that a failed
      // chmod or pull stops the block before docker run (the next group reads it as a shell would).
      assert.deepEqual(lines.slice(0, 2), [`chmod 600 ${env.file} &&`, `docker pull ${image} &&`], `${where}: pulls first, so an older copy of the tag isn’t started`);
      assert.ok(lines.indexOf(`  --env-file ${env.file} \\`) > lines.findIndex(line => line.startsWith('docker run -d ')), where);
      assert.equal(real.find(block => block.key === 'run').title, 'Pull the image, create the two volumes and start the node');
    }
    if (platform === 'compose') {
      assert.equal(env.title, `Save the private settings as ${env.file} next to it`, where);
      const compose = `docker compose --env-file ${env.file}`;
      // docker pull, not `docker compose pull`: with the file's pull_policy missing, Compose skips any tag the host has
      // except `latest` (Compose pkg/compose/pull.go shouldPullImage: "Image is already present locally").
      // Accepted command syntax change after R6: the same four commands in the same order with the same file, as one &&
      // list, so a failed chmod or pull never goes on to `up` an older copy of the tag, and logs follow only a started node.
      assert.equal(real.find(block => block.key === 'up').text, `chmod 600 ${env.file} &&\ndocker pull ${image} &&\n${compose} up -d &&\n${compose} logs node\n`, `${where}: pull, then start with the downloaded file`);
      assert.ok(real.find(block => block.key === 'started').text.endsWith(`Later Compose commands for it need --env-file ${env.file} too.`), where);
      const yaml = real.find(block => block.key === 'yaml');
      assert.ok(yaml.title.includes(`in a directory of its own, such as ${res}`) && yaml.text.split('\n').includes('    pull_policy: missing'), `${where}: its own directory, and the template’s pull policy`);
    }
    if (platform === 'portainer') assert.ok(real.find(block => block.key === 'deploy').text.includes(`If this host has run ${image} before, pull it again first (under Images in Portainer), so the node doesn't start an older copy of that tag.`), where);
  }
  // Compose pulls `latest` even with pull_policy missing (Compose file reference, pull_policy), and docker run doesn't
  // pull a tag the host has. The steps pull explicitly either way and never claim Compose reuses an older `latest`.
  for (const { masked, name, platform } of guides) assert.doesNotMatch(masked.map(block => block.text).join('\n'), /Compose[^\n.]*(?:cached|reuses|older copy)/u, `${name}, ${platform}`);
});

await check('new-node start commands stop at the first step that fails: nothing starts after a failed chmod, pull or volume create, and the shell stays open', () => {
  // A small reader for these blocks after POSIX (XCU 2.2.1: a backslash before the newline joins the lines; 2.9.3: a
  // newline ends a list unless the line ends in &&, and in `a && b` b runs only if a succeeded). It handles only plain
  // words, so it first proves the block has no quoting, expansion, pattern or other operator it would misread.
  const shellCommands = text => {
    const joined = text.replace(/\\\n/gu, '');
    assert.doesNotMatch(joined, /['"`$\\;|<>(){}#*?[\]!]/u, `only plain words: ${text}`);
    const commands = [];
    let list = 0;
    for (const line of joined.split('\n')) {
      if (!line.trim()) continue;
      const chained = line.endsWith(' &&');
      const command = chained ? line.slice(0, -3) : line;
      assert.doesNotMatch(command, /&/u, `no other operator: ${line}`);
      const words = command.trim().split(/\s+/u);
      commands.push({ list, words, line: words.join(' ') });
      if (!chained) list++;
    }
    assert.ok(commands.length > 0 && commands.at(-1).list < list, `the block ends its last list: ${text}`);
    return commands;
  };
  // What runs when the command at `fails` fails and every other succeeds (-1: none fails): in an && list the commands
  // after a failed one don't run; the next list runs either way.
  const ran = (commands, fails) => {
    let stopped = -1;
    return commands.filter((command, index) => {
      if (command.list === stopped) return false;
      if (index === fails) stopped = command.list;
      return true;
    }).map(command => command.line);
  };
  const setups = [
    ...combos.filter(combo => combo.setup.secrets).map(combo => combo.setup),
    { nodeId: 'Node.01:EU', name: 'Shared host', imageTag: 'latest', secrets: SECRETS, outbound: { connectionUrl: CONNECT, token: INVITE_TOKEN } },
  ];
  for (const setup of setups) for (const platform of ['docker', 'compose']) {
    const where = `${setup.nodeId}, ${setup.direct ? 'direct' : 'outbound'}, ${platform}`;
    const res = onboarding.resourceName(setup.nodeId);
    const image = onboarding.imageReference(setup.imageTag);
    const block = onboarding.setupGuide({ ...setup, platform }, true).find(found => found.key === (platform === 'docker' ? 'run' : 'up'));
    assert.deepEqual([block.kind, block.secret], ['command', false], where);
    const commands = shellCommands(block.text);
    assert.ok(commands.every(command => command.list === 0), `${where}: one && list: ${block.text}`);
    // These commands in this order: chmod and pull first, docker run (or compose up, then its logs) last.
    const compose = ['docker', 'compose', '--env-file', `${res}.env`];
    const steps = platform === 'docker'
      ? [['chmod', '600', `${res}.env`], ['docker', 'pull', image], ['docker', 'volume', 'create', `${res}-data`], ['docker', 'volume', 'create', `${res}-identity`], ['docker', 'run', '-d', '--name', res]]
      : [['chmod', '600', `${res}.env`], ['docker', 'pull', image], [...compose, 'up', '-d'], [...compose, 'logs', 'node']];
    assert.deepEqual(commands.map((command, index) => platform === 'docker' && index === steps.length - 1 ? command.words.slice(0, steps[index].length) : command.words), steps, where);
    const start = platform === 'docker' ? commands.length - 1 : commands.length - 2;
    const pull = commands.findIndex(command => command.words[1] === 'pull');
    assert.ok(pull >= 0 && pull < start, `${where}: pulls before it starts`);
    if (platform === 'docker') {
      assert.equal(commands[start].words.at(-1), image, `${where}: docker run starts the image just pulled`);
      assert.equal(commands[start].words.includes('-p'), Boolean(setup.direct), `${where}: a port only for the dashboard to dial in`);
    }
    // Each way it can fail: what came before ran, the failed step ran, and nothing after it did.
    commands.forEach((command, fails) => assert.deepEqual(ran(commands, fails), commands.slice(0, fails + 1).map(found => found.line), `${where}: ${command.line} fails`));
    for (let fails = 0; fails < start; fails++) assert.ok(!ran(commands, fails).includes(commands[start].line), `${where}: nothing starts after ${commands[fails].line} fails`);
    if (platform === 'compose') assert.ok(!ran(commands, start).includes(commands.at(-1).line), `${where}: no logs after a failed up`);
    assert.deepEqual(ran(commands, -1), commands.map(command => command.line), `${where}: all of it runs when each step succeeds`);
    // Nothing that would end the user's interactive shell (set -e, exit, exec) or run on after a failure (||).
    assert.doesNotMatch(block.text, /\bset -e\b|^\s*(?:exit|exec)\b|\|\|/mu, where);
    // The R6 form had these lines without &&, each a list of its own, so after a failed pull a pasted block went on to
    // start the copy of the tag the host already had. The reader finds that, so its passes above mean something.
    const separate = shellCommands(block.text.replace(/ &&$/gmu, ''));
    assert.equal(separate.length, commands.length, where);
    assert.ok(ran(separate, pull).includes(commands[start].line), `${where}: the R6 form starts it after a failed pull`);
  }
});

await check('an ended invitation for a deployed node says to invite it again as a running node; a waiting node’s hint names every reason it may not connect', () => {
  on();
  const now = 1_000_000;
  const again = dashboard.REINVITE_DEPLOYED;
  assert.ok(again.startsWith('If the node is already deployed, make the next invitation with “A node that’s already running” and the same node ID, then replace only CANARY_DASHBOARD_ENROLL_TOKEN in its settings.'), again);
  assert.match(again, /Don’t deploy it again as a new node: its data keeps its first tokens, so new ones would stop it from starting\./u);
  // It names the chooser's own words.
  const chooser = text(within('admin', [], React.createElement(wizard.AddNodeChooser, { plan: { fresh: true, link: 'outbound' }, onPlan: noop, onboarding: resource(CAPS) })));
  assert.ok(chooser.includes('A node that’s already running It keeps its ID, data, accounts and tokens.'), chooser);
  assert.ok(dashboard.invitationInfo({ ...WAITING, state: 'expired' }, now).text.endsWith(again), 'the expired status, in the wizard and under “Waiting to connect”');
  for (const detail of ['Invitation expired', 'Node invitation is no longer authorized']) {
    const failure = dashboard.invitationFailure(new api.ApiFailure(409, 'HTTP 409', detail), 'confirm');
    assert.ok(failure.text.includes(again) && failure.text.endsWith('Nothing was added.'), `${detail}: ${failure.text}`);
  }
  const source = read('src/views/NodeWizard.tsx');
  assert.equal((source.match(/notify\(`Cancelled the invitation for \$\{invitation\.label\}\. This dashboard set up no access on that node\. \$\{REINVITE_DEPLOYED\}`\);/gu) ?? []).length, 2, 'both cancel notices');
  assert.match(source, /title="This invitation expired">Nothing was added\. Cancel it, then make another one\. \{REINVITE_DEPLOYED\}<\/Banner>/u, 'the wizard’s expired banner');
  // The running-node steps it points to replace only the invitation settings and keep the node's own ID and tokens
  // (the deployment-text group pins that they write no CANARY_ADMIN_TOKEN, CANARY_CLUSTER_TOKEN or CANARY_NODE_ID).
  for (const { value: platform } of onboarding.PLATFORMS) {
    const keys = onboarding.setupGuide({ ...combos[2].setup, platform }, true).filter(block => block.secret).flatMap(block => block.text.split('\n').filter(Boolean).map(line => line.split('=')[0]));
    assert.ok(keys.every(key => key === 'CANARY_DASHBOARD_URL' || key === 'CANARY_DASHBOARD_ENROLL_TOKEN') && keys.includes('CANARY_DASHBOARD_ENROLL_TOKEN'), `${platform}: ${keys}`);
  }
  // Waiting: a missing fingerprint line has several causes, an older image only one of them.
  const image = onboarding.imageReference(CAPS.imageTag);
  const hint = dashboard.connectionWaitingHint(image, CONNECT);
  for (const part of ['No “Node connection fingerprint” line in the node’s log? Then it isn’t connecting:', `its image or build may predate node connections (for Docker, pull ${image} again and recreate it)`,
    'its settings may lack CANARY_DASHBOARD_URL or CANARY_DASHBOARD_ENROLL_TOKEN', 'or it refused to start (its log says “Invalid startup configuration”)',
    `“node connection unavailable” in its log means it can’t finish connecting: check that it reaches ${CONNECT} and trusts its certificate.`]) assert.ok(hint.includes(part), part);
  const main = readFileSync(resolve(root, 'apps/node-go/cmd/canary/main.go'), 'utf8');
  const connector = readFileSync(resolve(root, 'apps/node-go/internal/canary/node_connector.go'), 'utf8');
  assert.ok(main.includes('"Node connection fingerprint (compare before confirming): %s\\n"'), 'the line the node prints when it starts with a connection address');
  assert.ok(connector.includes('"Uptime-Canary node connection unavailable; retrying'), 'the line a node that can’t connect logs');
  assert.match(source, /connectionWaitingHint\(imageReference\(capabilities\.imageTag\), current\.connectionUrl\)/u, 'shown while it waits');
  assert.doesNotMatch(source, /keeps monitoring on its own/u, 'no claim that a node that may not have started is monitoring');
});

await check('a new node’s tokens belong to one node ID: typing, labels and platforms keep them; another ID gets its own, the ID left gets its pair back, and no ID shows another’s', () => {
  on();
  let made = 0;
  const make = () => { made++; return onboarding.newNodeSecrets(); };
  const first = onboarding.newNodeSecrets();
  let state = { current: first, earlier: null };
  // Typing an ID before the tokens were used keeps the one pair: it follows the ID and nothing is made.
  for (const id of ['c', 'ca', 'canary-a']) {
    assert.equal(onboarding.secretsForNode(state, id, false, make), state, `${id}: the same state`);
    assert.equal(onboarding.secretsFor(state.current, id), first, id);
  }
  // Used for canary-a (shown, copied, saved, verified with or written for an invitation): it belongs to that ID now.
  state = onboarding.secretsForNode(state, 'canary-a', true, make);
  assert.deepEqual([state.current.admin, state.current.cluster, state.current.nodeId, state.earlier], [first.admin, first.cluster, 'canary-a', null]);
  assert.equal(onboarding.secretsForNode(state, 'canary-a', true, make), state, 'using it again changes nothing');
  assert.equal(made, 0, 'typing and using made nothing');
  assert.equal(onboarding.secretsFor(state.current, 'canary-b'), null, 'another ID never gets it');
  assert.equal(onboarding.secretsFor(state.current, 'Canary-A'), null, 'IDs are compared exactly');
  // Another ID: a new pair, and canary-a's stays in reserve.
  state = onboarding.secretsForNode(state, 'canary-b', false, make);
  assert.equal(made, 1);
  assert.ok(state.current.admin !== first.admin && state.current.cluster !== first.cluster && state.current.nodeId === undefined, 'a new pair, not used yet');
  assert.equal(state.earlier.admin, first.admin);
  // Back to canary-a: its own pair comes back (its saved settings still match); the unused pair goes.
  state = onboarding.secretsForNode(state, 'canary-a', false, make);
  assert.deepEqual([state.current.admin, state.current.nodeId, state.earlier, made], [first.admin, 'canary-a', null, 1]);
  // Used for canary-b, then on to canary-c: only the pair just left is kept, so the reserve stays one pair.
  state = onboarding.secretsForNode(state, 'canary-b', true, make);
  const second = state.current;
  assert.deepEqual([made, second.nodeId, state.earlier.nodeId], [2, 'canary-b', 'canary-a']);
  state = onboarding.secretsForNode(state, 'canary-c', false, make);
  assert.deepEqual([made, state.current.nodeId, state.earlier.admin], [3, undefined, second.admin]);
  state = onboarding.secretsForNode(state, 'canary-b', false, make);
  assert.deepEqual([made, state.current.admin, state.earlier], [3, second.admin, null], 'canary-b gets its pair back');
  state = onboarding.secretsForNode(state, 'canary-a', false, make);
  assert.ok(made === 4 && state.current.admin !== first.admin && state.earlier.admin === second.admin, 'canary-a’s pair was let go, so it gets a new one');
  // A browser that can't make random values changes nothing: the pair used for another ID still isn't offered.
  const before = state;
  const used = onboarding.secretsForNode(state, 'canary-a', true, make);
  assert.throws(() => onboarding.secretsForNode(used, 'canary-z', false, () => { throw new Error('This browser couldn’t make random credentials.'); }), /couldn’t make random credentials/u);
  assert.equal(onboarding.secretsFor(used.current, 'canary-z'), null);
  assert.equal(before.current.nodeId, undefined, 'states are never changed in place');
  // The deployment text itself refuses tokens used for another ID, on every platform and both ways in.
  for (const combo of combos.filter(entry => entry.setup.secrets)) for (const { value: platform } of onboarding.PLATFORMS) {
    assert.throws(() => onboarding.setupGuide({ ...combo.setup, secrets: { ...SECRETS, nodeId: 'canary-other' }, platform }, false), /already used for another node ID/u, `${combo.name}, ${platform}`);
    // Its own ID's tokens are written whatever the label, image tag or platform.
    for (const variant of [{}, { name: 'Another label' }, { imageTag: 'latest' }]) {
      const real = onboarding.setupGuide({ ...combo.setup, ...variant, secrets: { ...SECRETS, nodeId: combo.setup.nodeId }, platform }, true).map(block => block.text).join('\n');
      assert.ok(real.includes(ADMIN) && real.includes(CLUSTER), `${combo.name}, ${platform}, ${JSON.stringify(variant)}`);
    }
  }
  // The dialog asks only when the ID changes, binds on Next, and the views filter by their own ID.
  const registry = read('src/views/Registry.tsx');
  assert.match(registry, /if \(next\.nodeId !== draft\.nodeId\) tokensFor\(next\.nodeId\);\n\s+setDraft\(next\);/u, 'one draft change: the ID and its tokens together');
  assert.match(registry, /onNext=\{target => \{ tokensFor\(draft\.nodeId, true\); setDeployed\(target\); \}\}/u);
  assert.match(registry, /const verifying = deployed \? secretsFor\(pair, draft\.nodeId\) : null;/u, 'verification sends only this ID’s token');
  const source = read('src/views/NodeWizard.tsx');
  assert.match(source, /const pair = secretsFor\(secrets, draft\.nodeId\);/u);
  assert.match(source, /const pair = secretsFor\(secrets, nodeId\);/u, 'bootstrap deployment uses only its own ID’s credentials');
  assert.match(source, /if \(fresh\) onUse\(result\.nodeId\);/u, 'an invitation’s tokens are bound to its node ID');
  // User requested visible disabled actions even without prerequisites. A missing or wrong-ID pair never enables them.
  const draft = { label: 'Office', nodeId: 'canary-office', address: 'http://100.79.12.4:3100', bindAddress: '100.79.12.4', platform: 'docker' };
  const deploy = (secrets, earlier = null) => within('admin', [ENTRY], React.createElement(wizard.NewNodeDeploy, {
    draft, onDraft: noop, secrets, earlier, onboarding: resource(CAPS), taken: { nodes: [ENTRY], invitations: [] }, onNext: noop, onUse: noop, onCancel: noop,
  }));
  for (const secrets of [{ ...SECRETS, nodeId: 'canary-other' }, null]) {
    const markup = deploy(secrets);
    const actions = tags(markup, 'button').map(attributes).filter(button => /^(?:Copy|Download|Show private values in) /u.test(button.get('aria-label') ?? ''));
    assert.ok(actions.length > 0 && actions.every(button => button.has('disabled')), 'missing or wrong-ID credentials never enable setup actions');
    for (const value of PRIVATE) assert.ok(!markup.includes(value), 'another node’s credential is absent even from a disabled preview');
    assert.match(text(markup), /Usable settings require the new node’s own administrator and cluster tokens, and this page has none for it\./u);
  }
  const own = deploy({ ...SECRETS, nodeId: 'canary-office' });
  assert.match(own, /--env-file/u);
  assert.doesNotMatch(text(own), /Tokens belong to one node/u, 'no note without another ID');
  assert.match(text(deploy(SECRETS, 'canary-a')), /Tokens belong to one node: the ones below are for canary-office \. Those shown, copied, saved or sent for canary-a stay with that ID\./u);
  // Accepted setup correction (user's2026-10-26report): the explicit local binding is independent of the node URL.
  assert.match(text(own), /publish port 3100 on 100\.79\.12\.4 only/u);
  assert.match(text(own), /the node host itself must have that address/u);
  assert.match(text(own), /Not a VPN, NAT or port-forward address that belongs to another machine/u);
  // Accepted first-start flow: create the invitation before exporting a fresh deployment.
  // Existing nodes still export connection-only settings and never replace retained bootstrap credentials.
  const outbound = (fresh, secrets, draft = { label: '', nodeId: '', address: '', platform: 'docker' }) => within('admin', [ENTRY], React.createElement(wizard.OutboundNode, {
    draft, onDraft: noop,
    fresh, onboarding: resource(CAPS), secrets, earlier: null, onUse: noop, taken: { nodes: [ENTRY], invitations: [] }, platform: 'docker', onPlatform: noop,
    onClose: noop, onRequestClose: noop, onLock: noop, onBusy: noop, guard: { current: null }, onTakenChanged: noop,
  }));
  const submit = markup => attributes(tags(markup, 'button').find(tag => attributes(tag).get('type') === 'submit'));
  assert.ok(submit(outbound(true, null)).has('disabled'), 'no invitation for a new node without tokens');
  assert.ok(submit(outbound(true, SECRETS)).has('disabled'), 'missing required identity fields keep setup generation disabled');
  const ready = outbound(true, SECRETS, { label: 'Office', nodeId: 'canary-office', address: '', platform: 'docker' });
  assert.ok(!submit(ready).has('disabled'), 'a complete form can generate its first-start setup without a prior bootstrap export');
  assert.match(text(ready), /Generate setup/u);
  assert.ok(!submit(outbound(false, null)).has('disabled'), 'a running node uses its retained credentials');
  assert.match(source, /secrets: fresh \? secretsFor\(invitationBootstrap, current\.nodeId\) : null,/u, 'only this newly generated deployment receives its captured own-ID bootstrap pair; an existing node gets connection-only settings');
  for (const pair of [{ ...SECRETS, nodeId: 'canary-other' }, null]) {
    const markup = within('admin', [], React.createElement(wizard.NewOutboundDeploy, { capabilities: CAPS, nodeId: 'canary-office', name: 'Office', secrets: pair,
      platform: 'docker', onPlatform: noop, onUse: noop, taken: { nodes: [], invitations: [] } }));
    const actions = tags(markup, 'button').map(attributes).filter(button => /^(?:Copy|Download|Show private values in) /u.test(button.get('aria-label') ?? ''));
    assert.ok(actions.length > 0 && actions.every(button => button.has('disabled')), 'preparation never enables exporting missing or another ID’s credentials');
    for (const value of PRIVATE) assert.ok(!markup.includes(value), 'preparation includes no other node’s credential');
  }
});

await check('a pending create can only be stopped: no choices, Close or Escape until it answers; a dialog the browser closes opens again; Review offers nothing once the invitation is gone', () => {
  on();
  const source = read('src/views/NodeWizard.tsx');
  assert.match(source, /useEffect\(\(\) => \{ onLock\(invitation !== null \|\| busy === 'create'\); \}, \[invitation, busy, onLock\]\);/u, 'the choices hide while it creates');
  assert.match(source, /useEffect\(\(\) => \{ onBusy\(busy !== null\); \}, \[busy, onBusy\]\);/u, 'Close and Escape are off while it creates');
  assert.match(source, /guard\.current = \(\) => \{\n\s+if \(busy !== null\) return false;/u);
  assert.match(source, /\{busy === 'create'\n\s+\? <button type="button" className="btn" onClick=\{\(\) => attempt\.current\?\.abort\(\)\}>Stop<\/button>/u, 'Stop stays');
  assert.match(source, /\{ title: 'Stopped', text: 'The dashboard’s server may have made the invitation anyway\. If it’s listed under “Waiting to connect”, its token can’t be shown again: cancel it there and make another\.', unknown: true, reload: true \}/u, 'and says the outcome is unknown');
  const registry = read('src/views/Registry.tsx');
  assert.match(registry, /<Modal title="Add node" onClose=\{requestClose\} busy=\{busy\}/u);
  assert.match(registry, /\{locked \|\| deployed \? null : <AddNodeChooser plan=\{plan\} onPlan=\{choose\} onboarding=\{onboarding\} \/>\}/u);
  // A busy Modal's own Close is off; Escape is refused by its cancel handler.
  const modal = busy => renderToStaticMarkup(React.createElement(bits.Modal, { title: 'Add node', onClose: noop, busy }, 'Body'));
  const closeButton = markup => attributes(tags(markup, 'button').find(tag => attributes(tag).get('aria-label') === 'Close'));
  assert.ok(closeButton(modal(true)).has('disabled') && !closeButton(modal(false)).has('disabled'));
  // HTML close watchers honour a prevented cancel event only after user activation, so a second Escape closes a
  // modal dialog anyway. The Modal opens itself again then, and treats that as a close request unless it's busy.
  const ui = read('src/ui/bits.tsx');
  assert.match(ui, /onCancel=\{event => \{ event\.preventDefault\(\); if \(!busy\) close\.current\(\); \}\}/u);
  assert.match(ui, /const reopen = \(\): void => \{\r?\n\s+if \(dialog\.open \|\| !dialog\.isConnected\) return;\r?\n\s+dialog\.showModal\(\);\r?\n\s+if \(!holding\.current\) close\.current\(\);\r?\n\s+\};\r?\n\s+dialog\.addEventListener\('close', reopen\);/u);
  assert.match(ui, /return \(\) => \{\r?\n\s+dialog\.removeEventListener\('close', reopen\);\r?\n\s+if \(dialog\.open\) dialog\.close\(\);/u, 'removed before the dialog closes itself on unmount');
  // L5: after a 404 the Review dialog shows that the invitation is gone and offers no Add.
  assert.equal(dashboard.invitationGone(new api.ApiFailure(404, 'HTTP 404', 'Node invitation not found')), true);
  for (const other of [new api.ApiFailure(409, 'HTTP 409', 'x'), new api.ApiFailure(0, 'unreachable'), new Error('Node invitation not found'), null, undefined]) assert.equal(dashboard.invitationGone(other), false, String(other));
  const review = source.slice(source.indexOf('export function PairingDialog'), source.indexOf('function CancelInvitationBody'));
  assert.match(review, /const gone = invitationGone\(polled\.failure\);/u);
  assert.match(review, /if \(busy \|\| gone\) return;/u, 'confirming does nothing then');
  assert.match(review, /\{gone\n\s+\? <Banner tone="warn" title="This invitation is no longer on this dashboard">It may have been cancelled meanwhile\. Nothing was added\.<\/Banner>\n\s+: current\.state === 'connected'/u, 'gone comes before any confirmation');
  assert.match(review, /\{rejecting && !gone \? null : <div className="modal-foot">/u, 'and Close stays');
});

// ----- TLS r1 (.canary/transport-tls-contract.md). Two expectations above changed, each with its reason: https:// for a
// new node, and the verifyNode call in Registry.tsx. Every group below is new.

const NODE_PIN = '0123456789abcdef'.repeat(4);
const OTHER_PIN = 'fedcba9876543210'.repeat(4);
const DASH_PIN = '5a'.repeat(32);
const colons = pin => pin.match(/../gu).join(':');
const shape = blocks => blocks.map(block => [block.key, block.kind, block.file, block.secret, block.title]);
const tlsSetting = line => /^\s*CANARY_TLS_MODE\s*[=:]/u.test(line);

await check('TLS: a new node the dashboard reaches at an https:// address serves TLS itself on every platform, and its steps say where the fingerprint is; plain http:// is as before', () => {
  // The Go facts the steps describe, read from the source so a changed rule fails here.
  const main = readFileSync(resolve(root, 'apps/node-go/cmd/canary/main.go'), 'utf8');
  const cli = readFileSync(resolve(root, 'apps/node-go/internal/canary/cli.go'), 'utf8');
  const transport = readFileSync(resolve(root, 'apps/node-go/internal/canary/transport_tls.go'), 'utf8');
  const nodetls = readFileSync(resolve(root, 'apps/node-go/internal/nodetls/tls.go'), 'utf8');
  assert.equal(onboarding.TLS_LOG_LINE, 'Node TLS certificate fingerprint:');
  assert.ok(main.includes(`fmt.Fprintln(os.Stdout, "${onboarding.TLS_LOG_LINE}", fingerprint)`), 'the line the node logs');
  assert.ok(cli.includes('get("CANARY_TLS_MODE")'), 'the setting the node reads');
  assert.ok(transport.includes('"CANARY_TLS_MODE must be auto or off"'), 'auto is the value it accepts');
  assert.ok(transport.includes('"Retained TLS identity is missing; restore the original identity volume"'), 'a lost identity stops it from starting');
  assert.match(nodetls, /AddDate\(10,\s*0,\s*0\)/u, 'ten years');
  assert.match(nodetls, /MinVersion:\s*tls\.VersionTLS13/u, 'TLS 1.3');
  const https = { ...combos[0].setup, direct: onboarding.directTarget('https://100.79.12.4:3100', '100.79.12.4') };
  const settingLine = { docker: 'CANARY_TLS_MODE=auto', compose: '      CANARY_TLS_MODE: auto', portainer: '      CANARY_TLS_MODE: auto', vm: 'CANARY_TLS_MODE="auto"' };
  const settingBlock = { docker: 'env', compose: 'yaml', portainer: 'yaml', vm: 'env' };
  for (const { value: platform } of onboarding.PLATFORMS) {
    const where = `https, ${platform}`;
    const res = onboarding.resourceName(https.nodeId);
    const logs = { docker: `docker logs ${res}`, compose: `docker compose --env-file ${res}.env logs node`, portainer: 'the container’s log in Portainer', vm: `journalctl -u ${res}` }[platform];
    const masked = onboarding.setupGuide({ ...https, platform }, false);
    const real = onboarding.setupGuide({ ...https, platform }, true);
    const plain = onboarding.setupGuide({ ...combos[0].setup, platform }, true);
    assert.deepEqual(shape(real), shape(plain), `${where}: the same steps as plain HTTP`);
    assert.deepEqual(shape(masked), shape(real), where);
    const preview = masked.map(block => block.text).join('\n');
    for (const value of [ADMIN, CLUSTER]) {
      assert.ok(!preview.includes(value), `${where}: the preview shows a private value`);
      assert.ok(real.some(block => block.secret && block.text.includes(value)), `${where}: a private value is missing from the settings`);
    }
    real.forEach((block, index) => {
      if (!block.secret) assert.equal(masked[index].text, block.text, `${where}: ${block.key} isn’t private, so it isn’t masked`);
      assert.doesNotMatch(block.text, FORBIDDEN, `${where}: ${block.key}`);
      assert.ok(!block.text.includes(PAGE), `${where}: this page’s own address never appears`);
    });
    // One TLS setting, in the block the node reads its settings from, shown in the preview: it isn't private.
    assert.deepEqual(real.flatMap(block => block.text.split('\n').filter(tlsSetting).map(line => [block.key, line])), [[settingBlock[platform], settingLine[platform]]], where);
    assert.ok(masked.find(block => block.key === settingBlock[platform]).text.split('\n').includes(settingLine[platform]), `${where}: the preview shows it`);
    assert.doesNotMatch(real.map(block => block.text).join('\n'), /CANARY_DASHBOARD_|TLS_FINGERPRINT/u, `${where}: no connection setting or pin for a node the dashboard dials`);
    assert.deepEqual(plain.flatMap(block => block.text.split('\n').filter(tlsSetting)), [], `${where}: plain HTTP sets no TLS mode`);
    assert.doesNotMatch(plain.map(block => `${block.title}\n${block.text}`).join('\n'), /Node TLS certificate fingerprint|CANARY_TLS_MODE/u, `${where}: nor mentions one`);
    // What the node runs or installs is the same; only its settings and the notes differ.
    for (const key of ['run', 'up', 'unit', 'install', 'build', 'stack', 'native']) assert.equal(real.find(block => block.key === key)?.text, plain.find(block => block.key === key)?.text, `${where}: ${key} unchanged`);
    if (platform === 'docker') {
      const env = real.find(block => block.key === 'env').text.split('\n');
      assert.equal(env.indexOf('CANARY_TLS_MODE=auto'), env.indexOf('CANARY_PEERS=[]') + 1, where);
      assert.ok(real.find(block => block.key === 'run').text.includes('  -p 100.79.12.4:3100:3100 \\'), `${where}: the same published port`);
    }
    if (platform === 'compose' || platform === 'portainer') {
      const yaml = real.find(block => block.key === 'yaml').text.split('\n');
      assert.equal(yaml.indexOf('      CANARY_TLS_MODE: auto'), yaml.indexOf('      CANARY_IDENTITY_DIRECTORY: /identity') + 1, where);
      assert.ok(yaml.includes('      - "100.79.12.4:3100:3100"'), where);
    }
    if (platform === 'vm') {
      const env = real.find(block => block.key === 'env').text.split('\n');
      assert.ok(env.includes('CANARY_HOST="100.79.12.4"'), `${where}: listens where the dashboard dials`);
      assert.equal(env.indexOf('CANARY_TLS_MODE="auto"'), env.indexOf('CANARY_PORT="3100"') + 1, where);
    }
    // Where the fingerprint is, how to copy it, what it isn't, and what keeps the certificate.
    const note = real.find(block => block.key === (platform === 'portainer' ? 'deploy' : 'started')).text;
    const place = platform === 'vm' ? 'directory' : 'volume';
    for (const part of [
      `It serves HTTPS itself (CANARY_TLS_MODE=auto), with a certificate it makes when it first starts, valid for ten years and kept in its identity ${place}.`,
      `Its log (${logs}) then shows a line that starts with “Node TLS certificate fingerprint:”.`,
      'Copy the 64 characters after it from that log yourself, where you see the node’s own output, such as your own terminal on its host, rather than from a message or page someone else could change.',
      'The fingerprint isn’t secret, and it isn’t the “Node connection fingerprint” of nodes that connect to this dashboard.',
      `Keep the identity ${place}: with its data but without it, the node refuses to start rather than make another certificate.`,
      'Then choose Next and enter the fingerprint',
    ]) assert.ok(note.includes(part), `${where}: ${part}`);
    if (platform !== 'portainer') assert.match(note, /accepting only that certificate, with the administrator token made here, and you confirm adding it\./u, where);
  }
});

await check('TLS: an invitation’s dashboard certificate pin becomes CANARY_DASHBOARD_TLS_FINGERPRINT on every platform, for new and running nodes, and stays after pairing; without one nothing changes', () => {
  const connector = readFileSync(resolve(root, 'apps/node-go/internal/canary/node_connector.go'), 'utf8');
  const cli = readFileSync(resolve(root, 'apps/node-go/internal/canary/cli.go'), 'utf8');
  assert.ok(cli.includes('get("CANARY_DASHBOARD_TLS_FINGERPRINT")'), 'the setting the node reads');
  assert.ok(connector.includes('validateTLSOrigin(origin, o.DashboardTLSFingerprint)'), 'checked with the connection address');
  assert.ok(connector.includes('c.identity.TLSFingerprint != pin'), 'saved with the connection identity: another pin later stops it from starting');
  const token = '${CANARY_DASHBOARD_ENROLL_TOKEN:-}';
  const changed = {
    new: { docker: ['env', 'after'], compose: ['yaml', 'after'], portainer: ['yaml', 'after'], vm: ['env', 'after'] },
    running: { docker: ['keep', 'env', 'recreate', 'after'], compose: ['keep', 'yaml', 'list', 'after'], portainer: ['keep', 'yaml', 'list', 'after'], vm: ['keep', 'env', 'after'] },
  };
  for (const [kind, base] of [['new', combos[1].setup], ['running', combos[2].setup]]) for (const { value: platform } of onboarding.PLATFORMS) {
    const where = `${kind} node, ${platform}`;
    const setup = { ...base, outbound: { ...base.outbound, tlsFingerprint: DASH_PIN } };
    const masked = onboarding.setupGuide({ ...setup, platform }, false);
    const real = onboarding.setupGuide({ ...setup, platform }, true);
    const unpinned = onboarding.setupGuide({ ...base, platform }, true);
    assert.deepEqual(shape(real), shape(unpinned), `${where}: the same steps`);
    const preview = masked.map(block => block.text).join('\n');
    for (const value of privateOf(setup)) {
      assert.ok(!preview.includes(value), `${where}: the preview shows a private value`);
      assert.ok(real.some(block => block.secret && block.text.includes(value)), `${where}: a private value is missing`);
    }
    assert.ok(preview.includes(DASH_PIN), `${where}: the pin isn’t private, so the preview shows it`);
    real.forEach(block => assert.doesNotMatch(block.text, FORBIDDEN, `${where}: ${block.key}`));
    // Only the blocks that hold the settings or speak of them change.
    real.forEach((block, index) => { if (!changed[kind][platform].includes(block.key)) assert.equal(block.text, unpinned[index].text, `${where}: ${block.key} unchanged`); });
    const step = key => real.find(block => block.key === key).text;
    const consecutive = (text, lines) => {
      const all = text.split('\n');
      const at = all.indexOf(lines[0]);
      assert.ok(at >= 0, `${where}: ${lines[0]}`);
      assert.deepEqual(all.slice(at, at + lines.length), lines, `${where}: in this order`);
    };
    if (kind === 'new') {
      if (platform === 'docker') consecutive(step('env'), [`CANARY_DASHBOARD_URL=${CONNECT}`, `CANARY_DASHBOARD_TLS_FINGERPRINT=${DASH_PIN}`, `CANARY_DASHBOARD_ENROLL_TOKEN=${INVITE_TOKEN}`]);
      if (platform === 'compose' || platform === 'portainer') {
        consecutive(step('yaml'), [`      CANARY_DASHBOARD_URL: '${CONNECT}'`, `      CANARY_DASHBOARD_TLS_FINGERPRINT: '${DASH_PIN}'`, `      CANARY_DASHBOARD_ENROLL_TOKEN: ${token}`]);
        assert.equal(composeValue(/^ {6}CANARY_DASHBOARD_TLS_FINGERPRINT: (.*)$/mu.exec(step('yaml'))[1]), DASH_PIN);
      }
      if (platform === 'vm') {
        consecutive(step('env'), [`CANARY_DASHBOARD_URL="${CONNECT}"`, `CANARY_DASHBOARD_TLS_FINGERPRINT="${DASH_PIN}"`, `CANARY_DASHBOARD_ENROLL_TOKEN="${INVITE_TOKEN}"`]);
        assert.equal(systemdValue(/^CANARY_DASHBOARD_TLS_FINGERPRINT=(.*)$/mu.exec(step('env'))[1]), DASH_PIN);
      }
    } else {
      assert.ok(step('keep').includes('remove CANARY_DASHBOARD_URL, CANARY_DASHBOARD_TLS_FINGERPRINT and CANARY_DASHBOARD_ENROLL_TOKEN again and start it as before'), `${where}: the undo names all three`);
      if (platform === 'docker') {
        assert.equal(step('env'), `CANARY_DASHBOARD_URL=${CONNECT}\nCANARY_DASHBOARD_TLS_FINGERPRINT=${DASH_PIN}\nCANARY_DASHBOARD_ENROLL_TOKEN=${INVITE_TOKEN}\n`, where);
        assert.ok(step('recreate').startsWith('Put all three lines in the node\'s env file, replacing any it has already,'), where);
      }
      if (platform === 'compose' || platform === 'portainer') {
        assert.equal(step('yaml'), `      CANARY_DASHBOARD_URL: '${CONNECT}'\n      CANARY_DASHBOARD_TLS_FINGERPRINT: '${DASH_PIN}'\n      CANARY_DASHBOARD_ENROLL_TOKEN: ${token}\n`, where);
        assert.ok(step('list').endsWith(`In a list of - KEY=value entries, add - CANARY_DASHBOARD_URL=${CONNECT}, - CANARY_DASHBOARD_TLS_FINGERPRINT=${DASH_PIN}, and - CANARY_DASHBOARD_ENROLL_TOKEN=${token} instead.`), `${where}: ${step('list')}`);
      }
      if (platform === 'vm') assert.equal(step('env'), `CANARY_DASHBOARD_URL="${CONNECT}"\nCANARY_DASHBOARD_TLS_FINGERPRINT="${DASH_PIN}"\nCANARY_DASHBOARD_ENROLL_TOKEN="${INVITE_TOKEN}"\n`, where);
      const keys = real.filter(block => block.secret).flatMap(block => block.text.split('\n').filter(Boolean).map(line => line.split('=')[0]));
      assert.ok(keys.every(key => ['CANARY_DASHBOARD_URL', 'CANARY_DASHBOARD_TLS_FINGERPRINT', 'CANARY_DASHBOARD_ENROLL_TOKEN'].includes(key)), `${where}: ${keys}`);
    }
    const after = step('after');
    assert.ok(after.startsWith('Remove CANARY_DASHBOARD_ENROLL_TOKEN from the node\'s settings; it works only once.'), where);
    assert.ok(after.endsWith(' Keep CANARY_DASHBOARD_URL and CANARY_DASHBOARD_TLS_FINGERPRINT as they are: the node saved both with that credential, and refuses to start if either one changes.'), `${where}: ${after}`);
    // A node that connects out keeps its own TLS mode, new or running.
    assert.deepEqual(real.flatMap(block => block.text.split('\n').filter(tlsSetting)), [], `${where}: no TLS mode`);
    // Without a pin, nothing about one.
    assert.doesNotMatch(unpinned.map(block => block.text).join('\n'), /TLS_FINGERPRINT|Keep CANARY_DASHBOARD_URL and/u, `${where}: unpinned`);
  }
  // Anything but 64 lowercase hexadecimal characters is refused before any text is made ('' means none).
  for (const bad of ['A'.repeat(64), 'abc', `${DASH_PIN}\n`, `${DASH_PIN} `, 'g'.repeat(64), colons(DASH_PIN), `${DASH_PIN}0`]) {
    for (const base of [combos[1].setup, combos[2].setup]) for (const { value: platform } of onboarding.PLATFORMS) {
      assert.throws(() => onboarding.setupGuide({ ...base, outbound: { ...base.outbound, tlsFingerprint: bad }, platform }, false), /certificate fingerprint this page can’t use/u, `${platform}: ${JSON.stringify(bad)}`);
    }
  }
  assert.deepEqual(shape(onboarding.setupGuide({ ...combos[1].setup, outbound: { ...combos[1].setup.outbound, tlsFingerprint: '' }, platform: 'docker' }, true)), shape(onboarding.setupGuide({ ...combos[1].setup, platform: 'docker' }, true)));
});

await check('TLS API: fingerprints are read strictly, sent only when entered and only for HTTPS, and come back only as 64 lowercase hexadecimal characters', async () => {
  on();
  // Go's rule: a complete SHA-256 hash in hex, lowercased; nothing else, and only with an https:// address.
  const nodetls = readFileSync(resolve(root, 'apps/node-go/internal/nodetls/tls.go'), 'utf8');
  const transport = readFileSync(resolve(root, 'apps/node-go/internal/canary/transport_tls.go'), 'utf8');
  assert.match(nodetls, /hex\.DecodeString\(value\)/u);
  assert.match(nodetls, /len\(decoded\) != sha256\.Size/u);
  assert.match(nodetls, /strings\.ToLower\(value\)/u);
  assert.ok(transport.includes('"Use a complete 64-character SHA256 TLS certificate fingerprint"') && transport.includes('"A TLS fingerprint requires an HTTPS address"'));
  const value = api.tlsFingerprintValue;
  for (const [entered, expected] of [['', ''], ['   ', ''], [NODE_PIN, NODE_PIN], [NODE_PIN.toUpperCase(), NODE_PIN], [` ${NODE_PIN}\n`, NODE_PIN], [colons(NODE_PIN), NODE_PIN], [colons(NODE_PIN).toUpperCase(), NODE_PIN]]) {
    assert.equal(value(entered), expected, JSON.stringify(entered));
  }
  for (const bad of [NODE_PIN.slice(1), `${NODE_PIN}0`, `g${NODE_PIN.slice(1)}`, `SHA256:${NODE_PIN}`, `${NODE_PIN.slice(0, 32)} ${NODE_PIN.slice(32)}`, colons(NODE_PIN).slice(3), colons(NODE_PIN).replace(/:/gu, '-'), `sha256 Fingerprint=${colons(NODE_PIN)}`]) {
    assert.equal(value(bad), null, bad);
  }
  const client = new api.CanaryApi(PAGE, TOKEN);
  const verification = extra => json(200, { ticket: 'verify-ticket-0123456789', expiresAt: 1_120_000, node: { id: 'canary-b', name: 'Canary B' }, label: 'Rack B', url: 'https://100.79.12.4:3100', plainHttp: false, ...extra });
  const input = { label: ' Rack B ', url: 'https://100.79.12.4:3100/', token: ADMIN };
  let got;
  // Without a fingerprint the body is exactly { label, url, token }, as before.
  for (const extra of [{}, { tlsFingerprint: '' }, { tlsFingerprint: '  ' }]) {
    const sent = await record(async () => { got = await client.verifyNode({ ...input, ...extra }); }, () => verification({}));
    assert.deepEqual(JSON.parse(sent[0].init.body), { label: 'Rack B', url: 'https://100.79.12.4:3100', token: ADMIN }, JSON.stringify(extra));
    assert.ok(!('tlsFingerprint' in got), 'none read back');
  }
  const pinned = await record(async () => { got = await client.verifyNode({ ...input, tlsFingerprint: colons(NODE_PIN).toUpperCase() }); }, () => verification({ tlsFingerprint: NODE_PIN.toUpperCase() }));
  assert.deepEqual(JSON.parse(pinned[0].init.body), { label: 'Rack B', url: 'https://100.79.12.4:3100', token: ADMIN, tlsFingerprint: NODE_PIN }, 'the form Go accepts');
  assert.equal(got.tlsFingerprint, NODE_PIN, 'read back lowercase');
  assert.ok(!('tlsFingerprint' in api.normalizeVerification({ ticket: 'x', expiresAt: 1, node: { id: 'n' }, label: 'L', url: 'https://n:3100', tlsFingerprint: 'not-a-pin' })), 'a broken one is dropped, so the form sees a mismatch');
  // Refused before anything leaves the page: a fingerprint for plain HTTP, or one that isn't a fingerprint.
  const refused = await record(async () => {
    assert.match((await rejection(client.verifyNode({ ...input, url: 'http://100.79.12.4:3100', tlsFingerprint: NODE_PIN }))).message, /needs an https:\/\/ address/u);
    assert.equal((await rejection(client.verifyNode({ ...input, tlsFingerprint: 'abc' }))).message, api.TLS_FINGERPRINT_FORMAT);
    assert.equal((await rejection(client.editNode(REG, { label: 'Rack B', tlsFingerprint: 'abc' }))).message, api.TLS_FINGERPRINT_FORMAT);
  });
  assert.equal(refused.length, 0, 'no request');
  // Editing: the fingerprint only when given; '' clears it.
  const saved = () => json(200, { ...ENTRY, url: 'https://100.79.12.4:3100', tlsFingerprint: NODE_PIN });
  const edits = await record(async () => {
    got = await client.editNode(REG, { label: 'Rack B', tlsFingerprint: NODE_PIN.toUpperCase() });
    await client.editNode(REG, { label: 'Rack B', tlsFingerprint: '' });
    await client.editNode(REG, { label: 'Rack B' });
    await client.editNode(REG, { label: 'Rack B', url: 'https://100.79.12.9:3100', tlsFingerprint: OTHER_PIN });
  }, saved);
  assert.deepEqual(edits.map(call => [call.init.method, JSON.parse(call.init.body)]), [
    ['PATCH', { label: 'Rack B', tlsFingerprint: NODE_PIN }], ['PATCH', { label: 'Rack B', tlsFingerprint: '' }], ['PATCH', { label: 'Rack B' }],
    ['PATCH', { label: 'Rack B', url: 'https://100.79.12.9:3100', tlsFingerprint: OTHER_PIN }],
  ]);
  assert.equal(got.tlsFingerprint, NODE_PIN);
  // Registry entries: administrators get it with the address; viewers and editors get neither, and nothing is made up.
  assert.equal(api.normalizeRegisteredNode({ ...ENTRY, tlsFingerprint: NODE_PIN.toUpperCase() }).tlsFingerprint, NODE_PIN);
  for (const bad of ['', 'abc', 7, null, colons(NODE_PIN)]) assert.ok(!('tlsFingerprint' in api.normalizeRegisteredNode({ ...ENTRY, tlsFingerprint: bad })), JSON.stringify(bad));
  assert.deepEqual(Object.keys(api.normalizeRegisteredNode({ ...ENTRY, url: undefined })).sort(), ['id', 'label', 'lastReadAt', 'local', 'monitorCount', 'nodeId', 'nodeName', 'state'], 'a viewer’s entry');
  // Capabilities: the listener's own pin only with a usable address; a broken one turns the outbound choice off.
  const caps = extra => api.normalizeOnboarding({ ...CAPS, ...extra });
  assert.deepEqual(caps({ connectionTlsFingerprint: DASH_PIN.toUpperCase() }), { ...CAPS, connectionTlsFingerprint: DASH_PIN });
  for (const none of ['', null, undefined]) assert.deepEqual(caps({ connectionTlsFingerprint: none }), CAPS, String(none));
  for (const bad of ['nope', 7, colons(DASH_PIN)]) {
    const seen = caps({ connectionTlsFingerprint: bad });
    assert.deepEqual([seen.outboundEnabled, 'connectionUrl' in seen, 'connectionTlsFingerprint' in seen], [false, false, false], String(bad));
  }
  assert.ok(!('connectionTlsFingerprint' in caps({ outboundEnabled: false, connectionUrl: undefined, connectionTlsFingerprint: DASH_PIN })), 'no pin without an address');
  // Invitations: bound to the pin like the address; a broken one makes the invitation unreadable.
  assert.equal(api.normalizeInvitation({ ...WAITING, connectionTlsFingerprint: DASH_PIN }).connectionTlsFingerprint, DASH_PIN);
  assert.ok(!('connectionTlsFingerprint' in api.normalizeInvitation({ ...WAITING, connectionTlsFingerprint: '' })));
  assert.ok(!('connectionTlsFingerprint' in api.normalizeInvitation(WAITING)), 'without one, as before');
  for (const bad of ['nope', 7, `${DASH_PIN}0`]) assert.throws(() => api.normalizeInvitation({ ...WAITING, connectionTlsFingerprint: bad }), /could not read/u, String(bad));
  const created = api.normalizeInvitationCreated({ ...WAITING, connectionTlsFingerprint: DASH_PIN, token: INVITE_TOKEN });
  assert.deepEqual([created.connectionTlsFingerprint, created.token], [DASH_PIN, INVITE_TOKEN]);
  // The JSON names in Go's handlers, and the server's own redaction for viewers and editors.
  const goHttp = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_http.go'), 'utf8');
  const goEdit = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_registry_edit.go'), 'utf8');
  const goRegistry = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_registry.go'), 'utf8');
  const goInvitations = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_invitations.go'), 'utf8');
  assert.ok(goHttp.includes('dashboardJSON(b, &in, "label", "url", "token", "tlsFingerprint")'), 'verify takes tlsFingerprint');
  assert.ok(goEdit.includes('dashboardJSON(raw, &input, "label", "url", "tlsFingerprint")'), 'edit takes tlsFingerprint');
  assert.match(goEdit, /TLSFingerprint\s+\*string\s+`json:"tlsFingerprint"`/u, 'left out keeps the saved one; "" replaces it');
  assert.match(goRegistry, /TLSFingerprint\s+string\s+`json:"tlsFingerprint,omitempty"`/u);
  assert.ok(goRegistry.includes('v.TLSFingerprint = ""'), 'viewers and editors get no pin');
  assert.ok(goInvitations.includes('json:"connectionTlsFingerprint,omitempty"') && goInvitations.includes('out["connectionTlsFingerprint"] = d.connectionTLSFingerprint'));
  // The created invitation keeps its pin once the token is taken out.
  const withoutToken = /const withoutToken = \(\{ ([^}]*) \}: NodeInvitationCreated\): NodeInvitation =>\n\s+\(\{ ([^\n]*) \}\);/u.exec(read('src/views/NodeWizard.tsx'));
  assert.ok(withoutToken, 'withoutToken');
  assert.ok(withoutToken[1].split(', ').includes('connectionTlsFingerprint') && withoutToken[2].includes('...(connectionTlsFingerprint ? { connectionTlsFingerprint } : {})'), withoutToken[0]);
  assert.match(read('src/views/NodeWizard.tsx'), /outbound: \{ connectionUrl: current\.connectionUrl, token, \.\.\.\(current\.connectionTlsFingerprint \? \{ tlsFingerprint: current\.connectionTlsFingerprint \} : \{\}\) \}/u, 'the settings use the invitation’s own pin');
});

await check('TLS rules and failures: empty means ordinary certificate checks, a fingerprint needs HTTPS, a new node needs one; certificate refusals read as such and never claim a credential went out', () => {
  on();
  const pinned = { ...ENTRY, url: 'https://100.79.12.4:3100', tlsFingerprint: NODE_PIN };
  assert.equal(dashboard.tlsFingerprintProblem('', 'https://n.example.test'), undefined, 'empty: ordinary checks');
  assert.match(dashboard.tlsFingerprintProblem('', 'https://n.example.test', true), /^Enter the fingerprint from the new node’s log\./u);
  assert.equal(dashboard.tlsFingerprintProblem(colons(NODE_PIN), 'https://n.example.test', true), undefined);
  assert.match(dashboard.tlsFingerprintProblem(NODE_PIN, 'http://n.example.test:3100'), /needs an https:\/\/ address\. Clear it to use plain HTTP\./u);
  assert.equal(dashboard.tlsFingerprintProblem(NODE_PIN, 'not an address'), undefined, 'the address field reports its own problem');
  assert.equal(dashboard.tlsFingerprintProblem('abc', 'https://n.example.test'), api.TLS_FINGERPRINT_FORMAT);
  assert.deepEqual(dashboard.tlsFingerprintEdit(pinned, NODE_PIN.toUpperCase(), pinned.url), { value: NODE_PIN, changed: false }, 'the same one in capitals');
  assert.deepEqual(dashboard.tlsFingerprintEdit(pinned, '', pinned.url), { value: '', changed: true }, 'cleared');
  assert.deepEqual(dashboard.tlsFingerprintEdit(pinned, OTHER_PIN, pinned.url), { value: OTHER_PIN, changed: true }, 'replaced');
  assert.deepEqual(dashboard.tlsFingerprintEdit(ENTRY, '', ENTRY.url), { value: '', changed: false });
  assert.deepEqual(dashboard.tlsFingerprintEdit(ENTRY, NODE_PIN, 'https://100.79.12.4:3100'), { value: NODE_PIN, changed: true });
  const toHttp = dashboard.tlsFingerprintEdit(pinned, NODE_PIN, 'http://100.79.12.9:3100');
  assert.deepEqual([toHttp.value, toHttp.changed], [NODE_PIN, false]);
  assert.match(toHttp.error, /needs an https:\/\/ address/u, 'a plain HTTP move clears it explicitly');
  assert.deepEqual(dashboard.tlsFingerprintEdit(pinned, 'zz', pinned.url), { value: '', changed: false, error: api.TLS_FINGERPRINT_FORMAT });
  // Go's general connection failure names the certificate too, so only its explicit refusals count as one.
  const registry = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_registry.go'), 'utf8');
  const nodetls = readFileSync(resolve(root, 'apps/node-go/internal/nodetls/tls.go'), 'utf8');
  const generic = 'Cannot reach this node from the dashboard server; check its address, certificate and permissions';
  // The transport classifier moved to its own Go helper without changing these generic/certificate refusals.
  // Keep the original exact 502 wording and also require nodeCall to use that classifier.
  const transportErrors = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_connection_errors.go'), 'utf8');
  assert.ok(registry.includes('dashboardNodeConnectionError(err)') && transportErrors.includes(`fail(502, "${generic}")`) && registry.includes('fail(502, "Cannot authenticate the node certificate")'), 'Go’s 502 wording');
  assert.ok(nodetls.includes('errors.New("Cannot authenticate the Canary TLS certificate")'));
  for (const detail of ['Cannot authenticate the node certificate', 'Cannot authenticate the Canary TLS certificate', 'Node TLS certificate does not match the saved fingerprint', 'TLS fingerprint mismatch']) assert.equal(dashboard.certificateRefused(detail), true, detail);
  for (const detail of [generic, 'Cannot reach this node from the dashboard server', 'Node credential was refused', 'Different node at this address', '']) assert.equal(dashboard.certificateRefused(detail), false, detail);
  const fail = (status, detail) => new api.ApiFailure(status, `HTTP ${status}`, detail);
  const edit = (detail, node, moving, pinChanged) => dashboard.editFailure(fail(502, detail), node, moving, pinChanged);
  const explicit = edit('Cannot authenticate the node certificate', pinned, false, true);
  assert.equal(explicit.title, 'The node’s certificate wasn’t accepted');
  assert.equal(explicit.text, 'Cannot authenticate the node certificate. Check the fingerprint against the line “Node TLS certificate fingerprint:” in the node’s own log. The dashboard’s server sent the node no credential, so the entry stays as it was. Nothing was changed.');
  assert.match(edit('Cannot authenticate the node certificate', pinned, true, false).text, /The new address has to present the certificate this entry already accepts\./u);
  const unreachable = edit(generic, pinned, false, true);
  assert.equal(unreachable.title, 'The dashboard’s server couldn’t check the node with that certificate');
  assert.ok(unreachable.text.endsWith('Your browser wasn’t involved, so it doesn’t matter whether this computer can reach it. A certificate that doesn’t match the fingerprint entered here ends the check too, before any credential is sent. Nothing was changed.'), unreachable.text);
  assert.ok(edit(generic, pinned, true, false).text.endsWith('This entry accepts only the certificate with its saved fingerprint, at the new address too. Nothing was changed.'));
  // Without any fingerprint, the move wording is exactly what it was.
  assert.deepEqual([edit(generic, ENTRY, true).title, edit(generic, ENTRY, true).text], ['The dashboard’s server couldn’t check the new address', `${generic}. Your browser wasn’t involved, so it doesn’t matter whether this computer can reach it. Nothing was changed.`]);
  assert.equal(edit('Different node at this address', pinned, false, true).title, 'A different node answers at this address');
  assert.equal(edit('Node credential was refused', pinned, false, true).title, 'The node doesn’t accept this dashboard’s access');
  assert.equal(edit('Node credential was refused', pinned, true, false).title, 'This address doesn’t accept this dashboard’s access');
  assert.equal(edit('Cannot authenticate the node certificate', pinned, false, false).title, 'Not saved', 'a rename never contacts the node');
  // Verification.
  const verify = (detail, generated, trust) => registryView.verifyFailure(fail(502, detail), generated, trust);
  const refusedPin = verify('Cannot authenticate the node certificate', false, 'pinned');
  assert.deepEqual([refusedPin.title, refusedPin.text], ['The node’s certificate wasn’t accepted', 'Cannot authenticate the node certificate. Copy the fingerprint again from the line “Node TLS certificate fingerprint:” in the node’s own log, and check the address. The dashboard’s server sent the node no credential. Nothing was saved.']);
  assert.match(verify(generic, true, 'pinned').text, /With a fingerprint, the dashboard’s server accepts only that certificate: a node that presents another one ends here too, before any credential is sent\. Nothing was saved\.$/u);
  assert.match(verify(generic, false, 'ca').text, /A node with a self-signed certificate of its own, such as one started with CANARY_TLS_MODE=auto, needs its certificate fingerprint here\. Nothing was saved\.$/u);
  for (const trust of ['none', undefined]) {
    const plain = verify(generic, false, trust);
    assert.deepEqual([plain.title, plain.text], ['The dashboard’s server couldn’t verify this node', `${generic}. Your browser wasn’t involved, so it doesn’t matter whether this computer can reach the address. Nothing was saved.`], 'plain HTTP wording as before');
  }
  assert.equal(verify('Node credential was refused', true, 'pinned').title, 'The node refused its new administrator token', 'a refused token is still that');
});

await check('TLS views: new-node steps point to the fingerprint in the node’s log; the direct form asks for it only for HTTPS and is unchanged otherwise; pins show to administrators only', () => {
  on();
  reached.length = 0;
  const label = markup => [...markup.matchAll(/<label\b[^>]*>([^<]*)<\/label>/gu)].map(found => found[1]);
  const field = (markup, name) => {
    const at = markup.indexOf(`>${name}</label>`);
    if (at < 0) return null;
    const tag = /<input\b[^>]*>/u.exec(markup.slice(at))?.[0];
    return tag ? attributes(tag) : null;
  };
  // A new node at an https:// address.
  const draft = { label: 'Office', nodeId: 'canary-office', address: 'https://100.79.12.4:3100', bindAddress: '100.79.12.4', platform: 'docker' };
  const deploy = props => within('admin', [ENTRY], React.createElement(wizard.NewNodeDeploy, { draft, onDraft: noop, secrets: SECRETS, onboarding: resource(CAPS), taken: { nodes: [ENTRY], invitations: [] }, onNext: noop, onUse: noop, onCancel: noop, ...props }));
  const tls = deploy({});
  for (const value of PRIVATE) assert.ok(!tls.includes(value));
  assert.ok(text(tls).includes('HTTPS: the node serves TLS 1.3 itself (CANARY_TLS_MODE=auto) with a self-signed certificate it makes when it first starts, valid for ten years. No certificate authority vouches for it, so the next step asks for its fingerprint, from the line “Node TLS certificate fingerprint:” in the node’s log.'), text(tls));
  assert.doesNotMatch(text(tls), /Plain HTTP/u);
  assert.match(text(tls), /CANARY_TLS_MODE=auto.*-p 100\.79\.12\.4:3100:3100.*Next: verify the node/u);
  assert.equal(field(tls, 'Node URL').get('placeholder'), 'https://100.79.12.4:3100', 'HTTPS is the suggested form');
  assert.match(text(deploy({ draft: { ...draft, address: 'http://100.79.12.4:3100' } })), /Plain HTTP: traffic between the dashboard’s server and this node isn’t encrypted by Uptime Canary\./u);
  // The direct form, for a running node: no fingerprint field until the address is HTTPS.
  const form = props => within('admin', [ENTRY], React.createElement(registryView.DirectAddForm, { onClose: noop, onBusy: noop, onStage: noop, ...props }));
  const FIELD = 'TLS certificate fingerprint';
  for (const url of ['', 'http://100.79.12.4:3100']) {
    const markup = form({ initialUrl: url });
    assert.deepEqual(label(markup), ['Name on this dashboard', 'Node address', 'Enrollment credential'], `${JSON.stringify(url)}: the form as before`);
  }
  const running = form({ initialUrl: 'https://canary-b.example.test' });
  assert.deepEqual(label(running), ['Name on this dashboard', 'Node address', FIELD, 'Enrollment credential']);
  const pinField = field(running, FIELD);
  assert.deepEqual([pinField.get('value'), pinField.get('autocomplete'), pinField.get('type'), pinField.get('placeholder')], ['', 'off', 'text', 'Empty: checked against trusted certificate authorities']);
  for (const part of ['it never accepts just any certificate', 'Adding the node doesn’t change its own TLS setting.', 'Copy the 64 characters after “Node TLS certificate fingerprint:” from the node’s own log',
    'It isn’t secret, and it isn’t the “Node connection fingerprint” of a node that connects to this dashboard.']) assert.ok(text(running).includes(part), part);
  assert.equal(tags(running, 'input').map(attributes).filter(found => found.get('type') === 'password').length, 1, 'still one credential field');
  // A new node's last step: the fingerprint is required, and its token never reaches the markup.
  const fresh = form({ credential: ADMIN, expectedId: 'canary-office', initialLabel: 'Office', initialUrl: 'https://100.79.12.4:3100', pinRequired: true, onBack: noop });
  assert.deepEqual(label(fresh), ['Name on this dashboard', 'Node address', FIELD]);
  assert.equal(field(fresh, FIELD).get('placeholder'), '64 hexadecimal characters from the node’s log');
  assert.ok(text(fresh).includes('with the administrator token made for it; your browser doesn’t. Verifying changes nothing on the node. It accepts only the certificate with the fingerprint below, and sends the token only once that certificate checks out.'), text(fresh));
  assert.ok(!fresh.includes(ADMIN));
  const freshPlain = form({ credential: ADMIN, expectedId: 'canary-office', initialLabel: 'Office', initialUrl: 'http://100.79.12.4:3100', onBack: noop });
  assert.deepEqual(label(freshPlain), ['Name on this dashboard', 'Node address'], 'a plain HTTP new node as before');
  assert.doesNotMatch(text(freshPlain), /It accepts only the certificate/u);
  // Node cards: the pin for administrators (shortened; Edit shows all of it), and the hint when it may have changed.
  const pinnedEntry = { ...ENTRY, url: 'https://100.79.12.4:3100', tlsFingerprint: NODE_PIN };
  const short = `${NODE_PIN.slice(0, 12)}…${NODE_PIN.slice(-12)}`;
  const admin = within('admin', [pinnedEntry, OUTBOUND], React.createElement(registryView.RegistryView));
  assert.ok(text(card(admin, ENTRY.id)).includes(`TLS certificate ${short} · pinned`), text(card(admin, ENTRY.id)));
  assert.doesNotMatch(text(card(admin, OUTBOUND.id)), /TLS certificate/u);
  assert.match(text(card(within('admin', [{ ...pinnedEntry, state: 'not-answering' }], React.createElement(registryView.RegistryView)), ENTRY.id)), /If the node’s certificate was replaced, enter the new fingerprint with Edit/u);
  assert.doesNotMatch(text(within('admin', [ENTRY], React.createElement(registryView.RegistryView))), /TLS certificate|certificate was replaced/u, 'nothing new without a pin');
  for (const role of ['viewer', 'editor']) {
    const markup = within(role, [pinnedEntry].map(({ url: _address, tlsFingerprint: _pin, ...node }) => node), React.createElement(registryView.RegistryView));
    assert.doesNotMatch(markup, new RegExp(`TLS certificate|${NODE_PIN.slice(0, 12)}`, 'u'), role);
  }
  // Edit: the full pin, and a field to change it for HTTPS; plain HTTP entries keep their two fields and get the
  // migration note; the bundled node's pin is the deployment's.
  const edit = node => within('admin', [node], React.createElement(registryView.EditNodeDialog, { node, onClose: noop }));
  const editPinned = edit(pinnedEntry);
  assert.deepEqual(label(editPinned), ['Name on this dashboard', 'Node address', FIELD]);
  assert.equal(field(editPinned, FIELD).get('value'), NODE_PIN);
  assert.ok(text(editPinned).includes('Change it only when the node’s certificate was replaced.') && text(editPinned).includes('it never accepts just any certificate'), text(editPinned));
  const submit = markup => attributes(tags(markup, 'button').find(tag => attributes(tag).get('type') === 'submit'));
  assert.ok(submit(editPinned).has('disabled'), 'nothing changed yet');
  assert.equal(field(edit({ ...ENTRY, url: 'https://100.79.12.4:3100' }), FIELD).get('value'), '', 'an HTTPS entry without a pin');
  const editPlain = edit(ENTRY);
  assert.deepEqual(label(editPlain), ['Name on this dashboard', 'Node address']);
  assert.ok(text(editPlain).includes('To have this node encrypt its own connections, start it with CANARY_TLS_MODE=auto: it then serves only HTTPS. At the same time, change its peers’ CANARY_PEERS entries for it to its https:// address with its fingerprint, and enter both here.'), text(editPlain));
  const local = { ...ENTRY, id: 'a'.repeat(32), local: true, url: 'https://canary-node:3100', tlsFingerprint: NODE_PIN };
  const editLocal = edit(local);
  assert.deepEqual(label(editLocal), ['Name on this dashboard']);
  assert.ok(text(editLocal).includes(`TLS certificate ${NODE_PIN}`) && text(editLocal).includes('This deployment’s settings manage the bundled node’s address and its certificate fingerprint, so it changes there, not here.'), text(editLocal));
  assert.deepEqual(label(edit(OUTBOUND)), ['Name on this dashboard'], 'an outbound entry has neither');
  // Outbound: the dashboard's own certificate pin, when its listener terminates TLS, with what it is and isn't.
  const server = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_server.go'), 'utf8');
  assert.ok(server.includes(`fmt.Fprintln(log, "${wizard.DASHBOARD_TLS_LOG_LINE}", connectionPin)`), 'the line the dashboard logs');
  const outbound = capabilities => text(within('admin', [ENTRY], React.createElement(wizard.OutboundNode, {
    draft: { label: '', nodeId: '', address: '', platform: 'docker' }, onDraft: noop,
    fresh: true, onboarding: resource(capabilities), secrets: SECRETS, earlier: null, onUse: noop, taken: { nodes: [ENTRY], invitations: [] }, platform: 'docker', onPlatform: noop,
    onClose: noop, onRequestClose: noop, onLock: noop, onBusy: noop, guard: { current: null }, onTakenChanged: noop,
  })));
  const withPin = outbound({ ...CAPS, connectionTlsFingerprint: DASH_PIN });
  for (const part of [`Its certificate fingerprint ${DASH_PIN}`, 'pin exactly that certificate with CANARY_DASHBOARD_TLS_FINGERPRINT', 'first compare it with the line “Dashboard connection TLS certificate fingerprint:” in the dashboard server’s own log.',
    'It names the dashboard’s certificate: it isn’t the node’s connection fingerprint you compare when the node connects.']) assert.ok(withPin.includes(part), part);
  const withoutPin = outbound(CAPS);
  assert.ok(withoutPin.includes('Set by this dashboard’s deployment. The node must reach it over HTTPS and trust its certificate; nothing here turns certificate checks off.'), 'as before');
  assert.doesNotMatch(withoutPin, /CANARY_DASHBOARD_TLS_FINGERPRINT/u, 'an unpinned listener never exports a fingerprint setting');
  assert.ok(withoutPin.includes('A different address requires this dashboard’s certificate fingerprint from native TLS.'), 'alternate destinations explain their trust prerequisite');
  assert.deepEqual(reached, [], 'rendering asks the API nothing');
});

// ----- TLS r2 (.canary/transport-tls-contract.md, .canary/update-notice-contract.md and
// .canary/update-ui-contract-feedback.md). No earlier expectation changed; every group below is new.

const OVERVIEW = { node: { id: 'canary-b', name: 'Canary B' }, now: 1_000_000, monitors: [], nodes: [], incidents: [], notifications: [], user: { name: 'review', role: 'admin' } };
const BUILD = { name: 'Uptime-Canary', version: 'v0.20.0', moduleVersion: '(devel)', goVersion: 'go1.27.0', os: 'linux', arch: 'amd64', sourceCommit: 'ab'.repeat(20), imageRevision: 'cd'.repeat(20), imageBuiltAt: '2026-10-25T00:00:00Z', modified: false, peerProtocol: 20 };
const UPDATES = { enabled: true, intervalHours: 6, state: 'ok', checkedAt: 900_000, nextCheckAt: 1_900_000, images: [{ component: 'dashboard', label: 'Dashboard', image: 'ghcr.io/narehood/uptime-canary-web', status: 'unknown' }], extra: 'passed on' };

await check('TLS r2 updates API: updates is one GET and checkUpdates one POST, both to the dashboard itself with the sign-in, never with a node selection or a body; answers and refusals pass on as sent', async () => {
  // Root's routes (dashboard_updates.go): GET answers from the cache; POST takes no body or query, and answers 202, 409 or 429.
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_updates.go'), 'utf8');
  assert.match(go, /r\.URL\.Path\s*==\s*"\/api\/dashboard\/updates"\s*&&\s*r\.Method\s*==\s*"GET"/u);
  assert.match(go, /r\.URL\.Path\s*==\s*"\/api\/dashboard\/updates\/check"\s*&&\s*r\.Method\s*==\s*"POST"/u);
  assert.ok(go.includes('"Update checks do not accept a request body"') && go.includes('"Update checks do not accept query parameters"'));
  // Measure the same refusal status/message after gofmt; whitespace is not API behavior.
  assert.match(go, /fail\(409,\s*"An update check is already running"\)/u, 'its running refusal');
  assert.match(go, /fail\(429,/u, 'its cooldown refusal');
  on();
  try {
    const client = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
    let cached, scheduled;
    const sent = await record(async () => { cached = await client.updates(); scheduled = await client.checkUpdates(); },
      (url, init) => init.method === 'POST' ? json(202, { ...UPDATES, state: 'checking' }) : json(200, UPDATES));
    assert.deepEqual(sent.map(call => `${call.init.method} ${call.url}`), [`GET ${PAGE}/api/dashboard/updates`, `POST ${PAGE}/api/dashboard/updates/check`], 'one request each');
    for (const call of sent) {
      assert.deepEqual([header(call, 'authorization'), header(call, HEADER), header(call, 'content-type'), call.init.body], [`Bearer ${TOKEN}`, null, null, undefined], call.url);
      assert.deepEqual([call.init.redirect, call.init.credentials, call.init.cache, call.init.referrerPolicy], ['error', 'same-origin', 'no-store', 'no-referrer'], call.url);
    }
    assert.deepEqual(cached, UPDATES, 'passed on as sent: updates.ts readUpdateStatus reads it');
    assert.deepEqual(scheduled, { ...UPDATES, state: 'checking' });
    // A refusal is a failure with the server's own words, and nothing is sent again.
    for (const [status, detail] of [[409, 'An update check is already running'], [429, 'Update checks are cached; wait at least fifteen minutes between manual checks'], [403, 'Insufficient permissions'], [404, 'Not found']]) {
      let failure;
      const once = await record(async () => { failure = await rejection(client.checkUpdates()); }, () => json(status, { error: detail }));
      assert.equal(once.length, 1, `${status}: never retried`);
      assert.ok(failure instanceof api.ApiFailure, String(status));
      assert.deepEqual([failure.status, failure.detail], [status, detail]);
    }
    const absent = await rejection(record(() => client.updates(), () => json(404, { error: 'Not found' })));
    assert.equal(absent.status, 404, 'a dashboard without the route: the notice hides itself');
    // Both through own(), as the contract and the update notice ask.
    const source = read('src/api.ts');
    for (const [name, line] of [['updates', 'return this.own<unknown>(\'/api/dashboard/updates\', \'GET\', undefined, signal);'], ['checkUpdates', 'return this.own<unknown>(\'/api/dashboard/updates/check\', \'POST\', undefined, signal);']]) {
      const start = source.indexOf(`\n  async ${name}(signal?: AbortSignal): Promise<unknown> {\n`);
      assert.ok(start > 0, `CanaryApi.${name}(signal?)`);
      assert.deepEqual(source.slice(start, source.indexOf('\n  }\n', start + 1)).trim().split('\n').slice(1).map(text => text.trim()), [line], name);
    }
  } finally {
    off();
  }
});

await check('TLS r2 build: the overview keeps a node’s reported build only with a whole peer protocol from 1 to 10000, and only its known fields; missing or broken, it’s left out and no protocol is assumed', async () => {
  const { templateCapability, TEMPLATE_PROTOCOL } = await load('src/alertTemplates.ts');
  // Go's identity (build_identity.go), in a node's overview (types.go, views.go), and the shared type.
  const go = file => readFileSync(resolve(root, 'apps/node-go/internal/canary', file), 'utf8');
  const identity = go('build_identity.go');
  const shared = readFileSync(resolve(root, 'packages/shared/src/index.ts'), 'utf8');
  const goNames = [...identity.matchAll(/json:"(\w+)/gu)].map(found => found[1]);
  const sharedNames = [.../export interface BinaryIdentity \{([^}]*)\}/u.exec(shared)[1].matchAll(/^\s*(\w+)\??:/gmu)].map(found => found[1]);
  assert.deepEqual(sharedNames, goNames, 'the shared type has Go’s fields');
  assert.deepEqual(Object.keys(api.normalizeBuild(BUILD)).sort(), [...goNames].sort(), 'a complete build keeps each of them');
  assert.match(go('types.go'), /Build\s+\*BinaryIdentity\s+`json:"build,omitempty"`/u);
  assert.ok(go('views.go').includes('Build: Ptr(LocalBinaryIdentity())'), 'a node reports its own');
  assert.ok(identity.includes('len(revision) == 40') && identity.includes('len(value) != 40'), 'Go writes 40-digit commits');
  const protocol = Number(/const peerProtocol = (\d+)/u.exec(go('wire_pages.go'))?.[1]);
  assert.ok(protocol >= TEMPLATE_PROTOCOL && protocol <= api.MAX_PEER_PROTOCOL, `peerProtocol ${protocol}`);
  const overview = build => api.normalizeOverview(build === undefined ? OVERVIEW : { ...OVERVIEW, build });
  // Valid: kept with its known fields only, so the selected node's channels offer protocol 20's templates.
  const kept = overview({ ...BUILD, extra: 'dropped', token: 'never-kept' });
  assert.deepEqual(kept.build, BUILD);
  assert.deepEqual(templateCapability(kept.build), { level: 'enhanced', protocol: 20 });
  // Missing, as from an older node: the overview as before, and the six-placeholder grammar.
  const older = overview(undefined);
  assert.deepEqual(older, OVERVIEW);
  assert.deepEqual(templateCapability(older.build), { level: 'legacy' });
  // Broken: left out entirely; protocol 20 is never assumed.
  const { peerProtocol: _protocol, ...unnumbered } = BUILD;
  for (const bad of [unnumbered, ...['20', 20.5, 0, -20, 10_001, Number.NaN, Number.POSITIVE_INFINITY, 2 ** 53, true, null].map(peerProtocol => ({ ...BUILD, peerProtocol })), null, 'Uptime-Canary', 20, [BUILD], []]) {
    const seen = overview(bad);
    assert.ok(!('build' in seen), JSON.stringify(bad));
    assert.deepEqual(templateCapability(seen.build), { level: 'legacy' }, `${JSON.stringify(bad)}: no protocol`);
  }
  for (const edge of [1, 19, 10_000]) assert.equal(overview({ ...BUILD, peerProtocol: edge }).build.peerProtocol, edge);
  assert.deepEqual(templateCapability(overview({ ...BUILD, peerProtocol: 19 }).build), { level: 'legacy', protocol: 19 });
  // Known fields, read safely: text cleaned and bounded, commits only in Go's form, `modified` only beside a commit.
  const nul = String.fromCharCode(0);
  const odd = overview({ name: `Uptime${nul}Canary`, version: 'v'.repeat(300), moduleVersion: 7, goVersion: null, os: 'linux\n', arch: ['amd64'], sourceCommit: 'AB'.repeat(20), imageRevision: 'abc1234', modified: 'false', peerProtocol: 20 }).build;
  assert.deepEqual(odd, { name: 'Uptime Canary', version: 'v'.repeat(128), moduleVersion: '', goVersion: '', os: 'linux', arch: '', sourceCommit: 'ab'.repeat(20), modified: null, peerProtocol: 20 });
  const unsure = overview({ ...BUILD, sourceCommit: 'not-a-commit', modified: true }).build;
  assert.deepEqual(['sourceCommit' in unsure, unsure.modified], [false, null], 'no commit: modification unknown, never clean or changed');
  assert.equal(overview({ ...BUILD, modified: true }).build.modified, true);
  // Through the API: the selected node's build comes through the dashboard; the dashboard's own overview has none.
  on();
  try {
    let seen;
    const through = await record(async () => { seen = await new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG).overview(); }, () => json(200, { ...OVERVIEW, build: BUILD }));
    assert.deepEqual([through.length, header(through[0], HEADER)], [1, REG]);
    assert.deepEqual(seen.build, BUILD, 'the selected node’s build');
    await record(async () => { seen = await new api.CanaryApi(PAGE, TOKEN).overview(); }, () => json(200, { ...OVERVIEW, node: { id: 'dashboard', name: 'Dashboard' }, build: BUILD }));
    assert.ok(!('build' in seen), 'the dashboard’s own overview stands for no node, so it carries no node’s build');
  } finally {
    off();
  }
  const own = /out := Overview\{Node: Identity\{"dashboard", "Dashboard"\}[^\n]*/u.exec(go('dashboard_http.go'))?.[0];
  assert.ok(own && !own.includes('Build'), 'Go sends none there either');
  let direct;
  await record(async () => { direct = await new api.CanaryApi(PAGE, TOKEN).overview(); }, () => json(200, { ...OVERVIEW, build: BUILD }));
  assert.deepEqual(direct.build, BUILD, 'a node signed in to directly');
});

await check('TLS r2 low ports: a Linux VM unit adds CAP_NET_BIND_SERVICE only for a port below 1024 and keeps its unprivileged dynamic user; Docker and Compose steps don’t change; a portless address means 443 and the suggestion shows :3100', () => {
  assert.ok(readFileSync(resolve(root, 'apps/node-go/internal/canary/cli.go'), 'utf8').includes('get("CANARY_PORT")'), 'a VM node listens on CANARY_PORT itself');
  const unitOf = blocks => blocks.find(block => block.key === 'unit').text;
  const vm = address => onboarding.setupGuide({ ...combos[0].setup, direct: onboarding.directTarget(address), platform: 'vm' }, true);
  const r1 = unitOf(guides.find(guide => guide.platform === 'vm' && guide.setup === combos[0].setup).real);
  const HIGH = ['AmbientCapabilities=CAP_NET_RAW', 'CapabilityBoundingSet=CAP_NET_RAW'];
  const LOW = ['AmbientCapabilities=CAP_NET_RAW CAP_NET_BIND_SERVICE', 'CapabilityBoundingSet=CAP_NET_RAW CAP_NET_BIND_SERVICE'];
  assert.ok(HIGH.every(line => r1.split('\n').includes(line)), 'r1’s unit, on port 3100');
  for (const [address, port] of [['https://100.79.12.4', 443], ['https://100.79.12.4:443', 443], ['http://100.79.12.4', 80], ['https://100.79.12.4:1023', 1023], ['https://100.79.12.4:1', 1], ['https://canary-b.lan', 443], ['https://[fd00::1]', 443]]) {
    const blocks = vm(address);
    const unit = unitOf(blocks).split('\n');
    const comment = `# CANARY_PORT ${port} is below 1024: CAP_NET_BIND_SERVICE lets this service's own unprivileged user listen on it.`;
    const at = unit.indexOf(comment);
    assert.ok(at > 0, `${address}: the unit says why`);
    assert.deepEqual(unit.slice(at + 1, at + 3), LOW, address);
    // Otherwise r1's unit line for line: the same dynamic user, no User= or root, nothing else added or dropped.
    assert.deepEqual(unit.filter(line => line !== comment).map(line => LOW.includes(line) ? HIGH[LOW.indexOf(line)] : line), r1.split('\n'), address);
    assert.ok(unit.includes('DynamicUser=yes') && unit.includes('NoNewPrivileges=yes'), address);
    assert.doesNotMatch(unit.join('\n'), /^(?:User|Group|SupplementaryGroups)=|\broot\b|CAP_SYS_|CAP_NET_ADMIN/mu, address);
    assert.ok(blocks.find(block => block.key === 'env').text.split('\n').includes(`CANARY_PORT="${port}"`), `${address}: the node listens there itself`);
  }
  for (const address of ['https://100.79.12.4:1024', 'https://100.79.12.4:3100', 'http://100.79.12.4:8080', 'https://100.79.12.4:65535']) assert.equal(unitOf(vm(address)), r1, `${address}: r1’s unit`);
  assert.equal(unitOf(onboarding.setupGuide({ ...combos[1].setup, platform: 'vm' }, true)), r1, 'a node that connects out listens on 3100 locally');
  assert.deepEqual([0, 1, 80, 443, 1023, 1024, 3100, 65535].map(onboarding.lowPort), [false, true, true, true, true, false, false, false]);
  // Docker publishes the port itself: host port 443 to the node's 3100, and nothing else differs.
  const at443 = { ...combos[0].setup, direct: onboarding.directTarget('https://100.79.12.4', '100.79.12.4') };
  const at3100 = { ...combos[0].setup, direct: onboarding.directTarget('https://100.79.12.4:3100', '100.79.12.4') };
  for (const platform of ['docker', 'compose', 'portainer']) {
    const low = onboarding.setupGuide({ ...at443, platform }, true);
    const high = onboarding.setupGuide({ ...at3100, platform }, true);
    assert.deepEqual(shape(low), shape(high), platform);
    const all = low.map(block => block.text).join('\n');
    assert.ok(all.includes(platform === 'docker' ? '  -p 100.79.12.4:443:3100 \\' : '      - "100.79.12.4:443:3100"'), `${platform}: host port 443`);
    low.forEach((block, index) => assert.equal(block.text.replaceAll('100.79.12.4:443:3100', '100.79.12.4:3100:3100'), high[index].text, `${platform}: ${block.key} differs only in the published port`));
    assert.doesNotMatch(all, /NET_BIND_SERVICE/u, platform);
  }
  // A portless address keeps its meaning (443, or 80), never a silent 3100; the note says so with the :3100 form.
  assert.equal(onboarding.CANARY_PORT, 3100);
  assert.deepEqual(onboarding.directTarget('https://100.79.12.4', '100.79.12.4'), { url: 'https://100.79.12.4', host: '100.79.12.4', listen: '100.79.12.4', port: 443, allInterfaces: false });
  const note = (address, platform = 'docker') => onboarding.schemePortNote(onboarding.directTarget(address), platform);
  assert.equal(note('https://100.79.12.4'), 'Without a port, https:// means port 443, so the commands publish port 443. To use Uptime Canary’s own port instead, enter https://100.79.12.4:3100.');
  assert.equal(note('https://100.79.12.4:443'), note('https://100.79.12.4'), 'an explicit :443 is the same address');
  assert.equal(note('https://100.79.12.4', 'vm'), 'Without a port, https:// means port 443, so the node listens on 443, which its service unit allows with CAP_NET_BIND_SERVICE, as it does for any port below 1024. To use Uptime Canary’s own port instead, enter https://100.79.12.4:3100.');
  assert.equal(note('http://canary-b.lan', 'compose'), 'Without a port, http:// means port 80, so the commands publish port 80. To use Uptime Canary’s own port instead, enter http://canary-b.lan:3100.');
  assert.match(note('https://[fd00::1]'), /enter https:\/\/\[fd00::1\]:3100\.$/u);
  for (const named of ['https://100.79.12.4:3100', 'https://100.79.12.4:8443', 'http://100.79.12.4:3100', 'https://100.79.12.4:1023']) assert.equal(note(named), null, named);
  for (const problem of ['nonsense', 'ftp://10.0.0.5:3100', 'https://10.0.0.5:3100/api']) assert.match(onboarding.directTarget(problem), /like https:\/\/100\.79\.12\.4:3100/u, problem);
  // Rendered: the note beside the publish note, on each platform's terms; none for an address with its port.
  on();
  try {
    reached.length = 0;
    const draft = { label: 'Office', nodeId: 'canary-office', address: 'https://100.79.12.4', bindAddress: '100.79.12.4', platform: 'docker' };
    const deploy = change => text(within('admin', [ENTRY], React.createElement(wizard.NewNodeDeploy, { draft: { ...draft, ...change }, onDraft: noop, secrets: SECRETS, onboarding: resource(CAPS), taken: { nodes: [ENTRY], invitations: [] }, onNext: noop, onUse: noop, onCancel: noop })));
    const docker = deploy({});
    assert.ok(docker.includes(note('https://100.79.12.4')) && docker.includes('The commands publish port 443 on 100.79.12.4 only') && docker.includes('-p 100.79.12.4:443:3100'), docker);
    const onVm = deploy({ platform: 'vm' });
    assert.ok(onVm.includes(note('https://100.79.12.4', 'vm')) && onVm.includes('AmbientCapabilities=CAP_NET_RAW CAP_NET_BIND_SERVICE'), onVm);
    for (const address of ['https://100.79.12.4:3100', 'http://100.79.12.4:3100']) assert.doesNotMatch(deploy({ address }), /Without a port/u, address);
    assert.deepEqual(reached, [], 'rendering asks the API nothing');
  } finally {
    off();
  }
});

await check('TLS r2 tls-info and tls-init: container steps say how to print the certificate again as the node’s own user, a VM’s don’t; moving a plain HTTP node to TLS keeps CANARY_TLS_MODE=off until the switch', () => {
  const go = file => readFileSync(resolve(root, 'apps/node-go/internal', file), 'utf8');
  const cli = go('canary/cli.go');
  const health = go('canary/tls_health.go');
  const transport = go('canary/transport_tls.go');
  assert.match(cli, /args\[0\] == "tls-info"/u, 'tls-info is a node command');
  assert.match(health, /"fingerprint":\s*nodetls\.CertificateFingerprint\(leaf\)/u, 'it prints the fingerprint');
  assert.match(cli, /"tls-init"/u, 'tls-init is a node command: root wires InitializeTLS into RunCLI');
  assert.match(health, /func InitializeTLS\(/u);
  assert.match(health, /options\.TLSMode\s*=\s*"off"/u, 'it serves nothing while it makes the certificate');
  // Same inherited-TLS behavior; gofmt places this branch on multiple lines.
  assert.match(transport, /if retained\s*\{\s*mode = "auto"\s*\}/u, 'an empty setting with a kept certificate means auto');
  assert.match(transport, /mode == "" \|\| mode == "off"/u, 'off stays plain HTTP');
  assert.ok(go('origin/connection_linux.go').includes('stat.Uid != uint32(os.Geteuid())'), 'the identity files must belong to the user reading them, so a VM gets no command');
  // The image's command and user, when this runs with the repository (the web image's build has no Dockerfile.node).
  const imagePath = resolve(root, 'Dockerfile.node');
  if (existsSync(imagePath)) {
    const image = readFileSync(imagePath, 'utf8');
    assert.ok(image.includes('COPY --from=build /out/canary /usr/local/bin/canary') && /^USER canary$/mu.test(image), 'canary, run as the image’s own user');
  }
  const https = { ...combos[0].setup, direct: onboarding.directTarget('https://100.79.12.4:3100', '100.79.12.4') };
  const res = onboarding.resourceName(https.nodeId);
  const later = command => `Later, ${command} on its host prints it again, with the certificate and its expiry date, and changes nothing.`;
  const info = { docker: later(`docker exec ${res} canary tls-info`), compose: later(`docker compose --env-file ${res}.env exec node canary tls-info`) };
  const words = blocks => blocks.map(block => `${block.title}\n${block.text}`).join('\n');
  for (const { value: platform } of onboarding.PLATFORMS) {
    const blocks = onboarding.setupGuide({ ...https, platform }, true);
    if (info[platform]) {
      const started = blocks.find(block => block.key === 'started').text;
      const at = started.indexOf(info[platform]);
      assert.ok(at > started.indexOf('someone else could change.') && at < started.indexOf('The fingerprint isn’t secret'), `${platform}: after the log, before what it isn’t: ${started}`);
      assert.equal((words(blocks).match(/tls-info/gu) ?? []).length, 1, `${platform}: once`);
    } else {
      assert.doesNotMatch(words(blocks), /tls-info|tls-init/u, platform);
    }
    for (const combo of combos) assert.doesNotMatch(words(onboarding.setupGuide({ ...combo.setup, platform }, true)), /tls-info|tls-init/u, `${combo.name}, ${platform}`);
  }
  // The registry's forms.
  on();
  try {
    reached.length = 0;
    const edit = node => text(within('admin', [node], React.createElement(registryView.EditNodeDialog, { node, onClose: noop })));
    const plain = edit(ENTRY);
    let at = -1;
    for (const part of [
      'To have this node encrypt its own connections, start it with CANARY_TLS_MODE=auto: it then serves only HTTPS.',
      'and enter both here.',
      'To read the fingerprint before that, first set CANARY_TLS_MODE=off in its settings: once a node has its certificate, leaving the setting empty starts it with HTTPS.',
      'Then stop it and run its tls-init command once with those settings, its data and the same user, such as canary tls-init in a one-off container of its image with its volumes: that makes its ten-year certificate and prints the fingerprint, without serving, checks or alerts.',
      'Start it again as before; it stays on plain HTTP until you set auto.',
      'Nothing on this page changes the node’s settings.',
    ]) {
      const next = plain.indexOf(part);
      assert.ok(next > at, `in this order: ${part}`);
      at = next;
    }
    for (const node of [{ ...ENTRY, url: 'https://100.79.12.4:3100', tlsFingerprint: NODE_PIN }, { ...ENTRY, url: 'https://100.79.12.4:3100' }, OUTBOUND]) {
      assert.doesNotMatch(edit(node), /tls-init|CANARY_TLS_MODE=off/u, node.url ?? node.connection);
    }
    const form = props => text(within('admin', [ENTRY], React.createElement(registryView.DirectAddForm, { onClose: noop, onBusy: noop, onStage: noop, ...props })));
    for (const shown of [form({ initialUrl: 'https://canary-b.example.test' }), form({ credential: ADMIN, expectedId: 'canary-office', initialLabel: 'Office', initialUrl: 'https://100.79.12.4:3100', pinRequired: true, onBack: noop })]) {
      const log = shown.indexOf('Copy the 64 characters after “Node TLS certificate fingerprint:” from the node’s own log');
      const command = shown.indexOf('A node in a container also prints it with canary tls-info run inside it, such as with docker exec.');
      assert.ok(log >= 0 && command > log, shown);
    }
    assert.doesNotMatch(form({ initialUrl: 'http://100.79.12.4:3100' }), /tls-info/u, 'no fingerprint field, no help for it');
    assert.deepEqual(reached, [], 'rendering asks the API nothing');
  } finally {
    off();
  }
});

await check('nothing private reached the console or browser storage', () => {
  for (const value of PRIVATE) assert.ok(!logged.some(line => line.includes(value)), 'a private value was logged');
  assert.deepEqual(storageWrites, []);
});

off();
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
