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

await check('a new node’s address: plain HTTP on its own port; an IP is published on that address only, a host name everywhere', () => {
  assert.deepEqual(onboarding.directTarget(' http://100.79.12.4:3100 '), { url: 'http://100.79.12.4:3100', host: '100.79.12.4', listen: '100.79.12.4', port: 3100, allInterfaces: false });
  assert.deepEqual(onboarding.directTarget('http://[fd00::1]:3100/'), { url: 'http://[fd00::1]:3100', host: '[fd00::1]', listen: 'fd00::1', port: 3100, allInterfaces: false });
  assert.deepEqual(onboarding.directTarget('http://canary-b.lan:8080'), { url: 'http://canary-b.lan:8080', host: '0.0.0.0', listen: '0.0.0.0', port: 8080, allInterfaces: true });
  assert.equal(onboarding.directTarget('http://10.0.0.5').port, 80);
  assert.match(onboarding.directTarget('https://10.0.0.5:3100'), /TLS proxy/u);
  for (const bad of ['http://u:p@10.0.0.5:3100', 'http://10.0.0.5:3100/api', 'http://10.0.0.5:3100?x=1', 'http://10.0.0.5:3100#x', '10.0.0.5:3100', '']) assert.equal(typeof onboarding.directTarget(bad), 'string', bad);
});

const combos = [
  { name: 'new node, the dashboard reaches it', setup: { nodeId: 'canary-office', name: 'Office', imageTag: CAPS.imageTag, secrets: SECRETS, direct: onboarding.directTarget('http://100.79.12.4:3100') } },
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
  assert.deepEqual(published({ ...combos[0].setup, direct: onboarding.directTarget('http://[fd00::1]:3100') }), ["  -p '[fd00::1]:3100:3100' \\"]);
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
    { nodeId, name: 'Shared host', imageTag: CAPS.imageTag, secrets: SECRETS, direct: onboarding.directTarget('http://100.79.12.4:3100'), platform },
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
    { ...office, name: 'Office', secrets: SECRETS, direct: onboarding.directTarget('http://100.79.12.4:3100') },
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

await check('node connections off: the outbound choice says why and can’t be picked, and Go’s refusal to invite reads as not set up (the browser review’s disabled branch, now here)', async () => {
  on();
  // The browser review's fixture now configures a node-connection address, so its "outbound disabled" branch doesn't run
  // there any more. Its two assertions run here instead: the chooser follows the dashboard's own answer, and the server's
  // refusal to make an invitation without the address leaves no token and reads as not set up.
  const go = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_invitations.go'), 'utf8');
  assert.match(go, /"outboundEnabled": d\.connectionEnabled && d\.connectionURL != ""/u, 'Go offers outbound only with an address');
  assert.match(go, /if !d\.connectionEnabled \|\| d\.connectionURL == "" \{\s*errorReply\(w, fail\(409, "Configure the dedicated HTTPS node connection endpoint first"\)\)/u, 'Go refuses to invite without it');
  const disabled = api.normalizeOnboarding({ ...CAPS, outboundEnabled: false, connectionUrl: undefined });
  assert.deepEqual([disabled.outboundEnabled, 'connectionUrl' in disabled], [false, false]);
  const chooser = onboarding => within('admin', [], React.createElement(wizard.AddNodeChooser, { plan: { fresh: true, link: 'direct' }, onPlan: noop, onboarding }));
  const radiosOf = markup => tags(markup, 'input').map(attributes).filter(found => found.get('type') === 'radio').map(found => found.has('disabled'));
  const off = chooser(resource(disabled));
  assert.deepEqual(radiosOf(off), [false, false, false, true], 'only the outbound choice is off');
  assert.match(text(off), /The node reaches this dashboard Not set up here: this dashboard’s deployment has no node-connection address\./u);
  const configured = chooser(resource(CAPS));
  assert.deepEqual(radiosOf(configured), [false, false, false, false], 'with an address it can be picked');
  assert.match(text(configured), /The node reaches this dashboard The node connects out over HTTPS and needs no inbound port/u);
  const unread = chooser({ ...resource(undefined), loading: false, error: new api.ApiFailure(503, 'HTTP 503', 'Unavailable') });
  assert.deepEqual(radiosOf(unread), [false, false, false, true], 'an unread answer never enables it');
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
  assert.deepEqual(radios.map(found => [found.has('checked'), found.has('disabled')]), [[true, false], [false, false], [true, false], [false, true]], 'running node and direct are chosen; outbound waits for the server');
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
  const draft = { label: 'Office', nodeId: 'canary-office', address: 'http://100.79.12.4:3100', platform: 'docker' };
  const deploy = props => within('admin', [ENTRY], React.createElement(wizard.NewNodeDeploy, { draft, onDraft: noop, secrets: SECRETS, onboarding: resource(CAPS), taken: { nodes: [ENTRY], invitations: [] }, onNext: noop, onCancel: noop, ...props }));
  const ready = deploy({});
  for (const value of PRIVATE) assert.ok(!ready.includes(value));
  assert.match(text(ready), /-p 100\.79\.12\.4:3100:3100.*Next: verify the node/u);
  assert.match(text(ready), /publish port 3100 on 100\.79\.12\.4 only/u);
  assert.match(text(deploy({ draft: { ...draft, address: '' } })), /The commands appear here once these fields are filled in\./u);
  assert.match(text(deploy({ draft: { ...draft, nodeId: 'canary-b' } })), /Already on this dashboard as “Rack B”\..*The commands appear here/u);
  assert.match(text(deploy({ onboarding: resource(undefined) })), /Reading this dashboard’s image tag…/u);
  assert.match(text(deploy({ draft: { ...draft, address: 'http://canary-b.lan:3100' } })), /publish port 3100 on every address of the host/u);
  const guard = { current: null };
  const outbound = props => within('admin', [ENTRY], React.createElement(wizard.OutboundNode, {
    fresh: true, onboarding: resource(CAPS), secrets: SECRETS, taken: { nodes: [ENTRY], invitations: [] }, platform: 'docker', onPlatform: noop, onClose: noop, onRequestClose: noop,
    onLock: noop, onBusy: noop, guard, onTakenChanged: noop, ...props,
  }));
  const identify = text(outbound({}));
  assert.ok(identify.includes(CONNECT) && /Create invitation/u.test(identify) && /No inbound port or firewall opening is needed on the node/u.test(identify));
  assert.doesNotMatch(identify, new RegExp(PAGE.replace(/[.]/gu, '\\.'), 'u'), 'the page’s own address is never offered as the connection address');
  assert.equal(typeof guard.current, 'function');
  assert.equal(guard.current(), true, 'nothing made yet, so closing needs no question');
  assert.match(text(outbound({ onboarding: resource({ ...CAPS, outboundEnabled: false, connectionUrl: undefined }) })), /Nodes can’t connect to this dashboard yet.*Not set up here/u);
  assert.match(text(outbound({ fresh: false })), /Its current CANARY_NODE_ID\. The dashboard refuses a node that connects with any other ID\./u);
  assert.deepEqual(reached, [], 'rendering asks the API nothing');
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
  assert.match(registry, /api\.verifyNode\(\{ label, url, token: sent \}, controller\.signal\);\n\s+setToken\(''\);/u, 'a pasted credential is still cleared after each verification');
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
  assert.match(source, /const pair = fresh \? secretsFor\(secrets, current\.nodeId\) : null;/u);
  assert.match(source, /if \(fresh\) onUse\(result\.nodeId\);/u, 'an invitation’s tokens are bound to its node ID');
  // Rendered: a pair used for another ID, or none, gives no steps, and never a running node's steps.
  const draft = { label: 'Office', nodeId: 'canary-office', address: 'http://100.79.12.4:3100', platform: 'docker' };
  const deploy = (secrets, earlier = null) => within('admin', [ENTRY], React.createElement(wizard.NewNodeDeploy, {
    draft, onDraft: noop, secrets, earlier, onboarding: resource(CAPS), taken: { nodes: [ENTRY], invitations: [] }, onNext: noop, onUse: noop, onCancel: noop,
  }));
  for (const secrets of [{ ...SECRETS, nodeId: 'canary-other' }, null]) {
    const markup = deploy(secrets);
    assert.doesNotMatch(markup, /--env-file|aria-label="(?:Copy|Download|Show private values in) /u, `no steps for ${JSON.stringify(secrets?.nodeId ?? null)}`);
    assert.match(text(markup), /No steps without the new node’s own administrator and cluster tokens, and this page has none for it\./u);
  }
  const own = deploy({ ...SECRETS, nodeId: 'canary-office' });
  assert.match(own, /--env-file/u);
  assert.doesNotMatch(text(own), /Tokens belong to one node/u, 'no note without another ID');
  assert.match(text(deploy(SECRETS, 'canary-a')), /Tokens belong to one node: the ones below are for canary-office \. Those shown, copied, saved or sent for canary-a stay with that ID\./u);
  // L7: an IP the host itself must have; NAT or a port forward means the host's own address in the publish line.
  assert.match(text(own), /publish port 3100 on 100\.79\.12\.4 only, so it must be an address of this host itself\. If the dashboard’s server reaches the host through NAT or a port forward, put the host’s own address, which the forward points to, in the publish line \(CANARY_HOST on a VM\)\./u);
  // L3: a new node that reaches the dashboard can't be invited without its tokens; a running node can.
  const outbound = (fresh, secrets) => within('admin', [ENTRY], React.createElement(wizard.OutboundNode, {
    fresh, onboarding: resource(CAPS), secrets, earlier: null, onUse: noop, taken: { nodes: [ENTRY], invitations: [] }, platform: 'docker', onPlatform: noop,
    onClose: noop, onRequestClose: noop, onLock: noop, onBusy: noop, guard: { current: null }, onTakenChanged: noop,
  }));
  const submit = markup => attributes(tags(markup, 'button').find(tag => attributes(tag).get('type') === 'submit'));
  assert.ok(submit(outbound(true, null)).has('disabled'), 'no invitation for a new node without tokens');
  assert.ok(!submit(outbound(true, SECRETS)).has('disabled') && !submit(outbound(false, null)).has('disabled'));
  assert.match(source, /\{token && current\.state !== 'confirmed' \? \(fresh && !pair\n\s+\? <Banner tone="down" title="No settings without the new node’s tokens">/u, 'after creation too: no steps rather than a running node’s');
  assert.doesNotMatch(source, /secrets: fresh \? secrets : null/u, 'the r1-r4 fallback to running-node steps is gone');
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

await check('nothing private reached the console or browser storage', () => {
  for (const value of PRIVATE) assert.ok(!logged.some(line => line.includes(value)), 'a private value was logged');
  assert.deepEqual(storageWrites, []);
});

off();
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
