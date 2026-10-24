// Pure regressions for the heartbeat commands a Push monitor's form shows (apps/web/src/dashboard.ts `pushCommands`,
// used by PushFields in apps/web/src/views/MonitorForm.tsx). Without a selected registration (direct mode, or the
// control plane with no node selected) the commands are byte for byte the ones the form showed before the control
// plane. With one, both go to the dashboard's own origin and name exactly that registration in the query, apart from
// the token path; stripping `registration`, as the dashboard's server does before the node's strict push decoder, gives
// back the direct request. The protocol stays `Authorization: Push`, never a dashboard sign-in, cookie or selection
// header; the token is the `<token>` placeholder until revealed, and GET appears only when allowed.
// Imports dashboard.ts and api.ts in place through one tsx namespace with a stand-in `location` and `document`, and
// reads MonitorForm.tsx as text for PushFields' wiring and copy: no network, browser, build, copy, scratch or output
// files. The token and session values below are synthetic, and failure messages redact them anyway. This pins the
// commands the page shows, not the dashboard server's push route, a shell or a sign-in proxy.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/push-commands-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const web = resolve(root, 'apps/web');
const PAGE = 'https://canary.example.test';
const DIRECT = 'http://127.0.0.1:3100';
const TOKEN = '0123456789abcdef'.repeat(4);
const SESSION = 'push-commands-review-dashboard-session';
const REG = '5f3c9a7e1b2d4c6e8f0a1b2c3d4e5f60';
const REG2 = 'c0ffee00c0ffee00c0ffee00c0ffee00';

// Browser storage, cookies and requests are recorded, not provided: nothing here may reach them.
const touched = [];
for (const name of ['localStorage', 'sessionStorage', 'indexedDB']) {
  Object.defineProperty(globalThis, name, { configurable: true, get: () => { touched.push(name); return undefined; } });
}
Object.defineProperty(globalThis, 'location', { value: { origin: PAGE, protocol: 'https:', hash: '', pathname: '/' }, configurable: true, writable: true });
Object.defineProperty(globalThis, 'document', {
  configurable: true, writable: true,
  value: { documentElement: null, get cookie() { touched.push('cookie'); return ''; }, set cookie(_value) { touched.push('cookie'); } },
});
globalThis.fetch = () => { touched.push('fetch'); throw new Error('no requests here'); };

const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'push-commands-pure', tsconfig: resolve(web, 'tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(web, path)).href, import.meta.url);
const api = await load('src/api.ts');
const dashboard = await load('src/dashboard.ts');

let failed = 0;
const redact = text => [TOKEN, SESSION].reduce((out, value) => out.split(value).join('[synthetic credential]'), String(text));
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${redact(e?.message)}`); } };

// Every call goes through here, so a helper that logs is caught without anything it logged being printed.
const METHODS = ['log', 'info', 'warn', 'error', 'debug', 'trace'];
let consoleCalls = 0;
const commands = (...args) => {
  const saved = METHODS.map(method => console[method]);
  METHODS.forEach(method => { console[method] = () => { consoleCalls++; }; });
  try { return dashboard.pushCommands(...args); } finally { METHODS.forEach((method, i) => { console[method] = saved[i]; }); }
};
const controlPlane = on => {
  if (on) globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true;
  else delete globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__;
};

// PushFields' template before the control plane, copied verbatim (`api.baseUrl` is `base`). React joined its two text
// children, so the page showed their concatenation.
const original = (base, token, shown, allowGet) => `curl -X POST ${base}/api/push \\
  -H "Authorization: Push ${shown ? token : '<token>'}" \\
  -H "Content-Type: application/json" \\
  -d '{"status":"up","msg":"backup finished","ping":120}'` + (allowGet ? `

# or, without a header:
curl "${base}/api/push/${shown ? token : '<token>'}?status=up&msg=ok"` : '');
const LEGACY_PLACEHOLDER_GET = [
  'curl -X POST http://127.0.0.1:3100/api/push \\',
  '  -H "Authorization: Push <token>" \\',
  '  -H "Content-Type: application/json" \\',
  `  -d '{"status":"up","msg":"backup finished","ping":120}'`,
  '',
  '# or, without a header:',
  'curl "http://127.0.0.1:3100/api/push/<token>?status=up&msg=ok"',
].join('\n');
const HEADER_AND_BODY = [
  `  -H "Authorization: Push ${TOKEN}" \\`,
  '  -H "Content-Type: application/json" \\',
  `  -d '{"status":"up","msg":"backup finished","ping":120}'`,
];

/** The POST and (when present) GET URLs, parsed from the command lines the way a shell would quote them. */
function parse(text) {
  const lines = text.split('\n');
  const post = /^curl -X POST "([^"\s]+)" \\$/u.exec(lines[0]);
  assert.ok(post, `the POST line is one quoted URL: ${lines[0]}`);
  const get = lines.length > 4 ? /^curl "([^"\s]+)"$/u.exec(lines[6]) : null;
  if (lines.length > 4) assert.ok(get, `the GET line is one quoted URL: ${lines[6]}`);
  return { lines, post: new URL(post[1]), get: get ? new URL(get[1]) : null };
}

await check('direct mode and no selection: the commands are byte for byte the earlier ones', () => {
  controlPlane(false);
  const direct = new api.CanaryApi(`${DIRECT}/`, SESSION);
  assert.equal(direct.registeredNode, null);
  assert.equal(commands(direct.baseUrl, null, true, direct.registeredNode), LEGACY_PLACEHOLDER_GET, 'the exact earlier bytes, placeholder and GET');
  for (const base of [DIRECT, 'https://node-a.example.test', 'http://[fd7a:115c:a1e0::3]:3100', PAGE]) {
    for (const shown of [false, true]) {
      for (const allowGet of [false, true]) {
        assert.equal(commands(base, shown ? TOKEN : null, allowGet, null), original(base, TOKEN, shown, allowGet), `${base} shown=${shown} get=${allowGet}`);
      }
    }
  }
  controlPlane(true);
  const none = new api.CanaryApi(PAGE, SESSION, { credential: 'session', registeredNode: REG }).withRegisteredNode(null);
  assert.equal(none.registeredNode, null, 'the control plane without a selected node');
  assert.equal(commands(none.baseUrl, null, true, none.registeredNode), original(PAGE, TOKEN, false, true), 'no selection: the earlier commands at the dashboard');
  controlPlane(false);
});

await check('selected registration: both URLs are the dashboard’s own origin with exactly that registration', () => {
  controlPlane(true);
  for (const page of [PAGE, 'http://192.168.1.20:18347']) {
    const selected = new api.CanaryApi(`${page}/`, SESSION, { credential: 'session', registeredNode: REG });
    assert.equal(selected.registeredNode, REG);
    assert.match(REG, /^[0-9a-f]{32}$/u, 'shaped like the server’s registration IDs');
    const text = commands(selected.baseUrl, TOKEN, true, selected.registeredNode);
    const { lines, post, get } = parse(text);
    assert.equal(lines.length, 7);
    for (const url of [post, get]) {
      assert.equal(url.origin, page, url.href);
      assert.equal(url.username + url.password + url.hash, '', url.href);
      assert.deepEqual(url.searchParams.getAll('registration'), [REG], `${url.href}: one registration`);
    }
    assert.equal(post.pathname, '/api/push');
    assert.equal(post.search, `?registration=${REG}`);
    assert.equal(get.pathname, `/api/push/${TOKEN}`, 'the registration stays out of the token path');
    assert.equal(get.search, `?registration=${REG}&status=up&msg=ok`);
    assert.deepEqual([...get.searchParams.keys()], ['registration', 'status', 'msg']);
    const urls = text.match(/https?:\/\/[^\s"]+/gu);
    assert.equal(urls.length, 2, 'no other address in the commands');
    for (const url of urls) assert.equal(new URL(url).origin, page, url);
  }
  controlPlane(false);
});

await check('selected registration: removing it gives back the direct request, header and body unchanged', () => {
  controlPlane(true);
  const selected = new api.CanaryApi(PAGE, SESSION, { credential: 'session', registeredNode: REG });
  const text = commands(selected.baseUrl, TOKEN, true, selected.registeredNode);
  const { lines, post, get } = parse(text);
  assert.deepEqual(lines.slice(1, 4), HEADER_AND_BODY, 'the same Push header, content type and body');
  assert.deepEqual(JSON.parse(/^ {2}-d '(.*)'$/u.exec(lines[3])[1]), { status: 'up', msg: 'backup finished', ping: 120 });
  assert.deepEqual(lines.slice(4, 6), ['', '# or, without a header:']);
  const rest = url => { url.searchParams.delete('registration'); return `${url.origin}${url.pathname}${url.search}`; };
  assert.equal(rest(post), `${PAGE}/api/push`, 'the node’s push route with no query left');
  assert.equal(rest(get), `${PAGE}/api/push/${TOKEN}?status=up&msg=ok`, 'the node’s GET push with only its own query');
  const stripped = text.replace(`"${PAGE}/api/push?registration=${REG}"`, `${PAGE}/api/push`).replace(`?registration=${REG}&`, '?');
  assert.equal(stripped, original(PAGE, TOKEN, true, true), 'only the selector differs from the direct commands');
  assert.equal(text.match(/Authorization:/gu).length, 1, 'one credential, the push token');
  assert.equal(text.match(/ -H /gu).length, 2, 'only the Push and content-type headers');
  assert.match(text, /-H "Authorization: Push [0-9a-f]{64}" \\/u);
  assert.doesNotMatch(text, /Bearer|Cookie|--cookie|\s-b\s|X-Canary|registered-node/iu, 'no dashboard credential, cookie or selection header');
  assert.ok(!text.includes(SESSION), 'the dashboard sign-in never appears');
  const postOnly = commands(selected.baseUrl, TOKEN, false, selected.registeredNode);
  assert.equal(postOnly, lines.slice(0, 4).join('\n'), 'without GET allowed, only the POST');
  assert.doesNotMatch(postOnly, /# or|\/api\/push\//u);
  controlPlane(false);
});

await check('masked by default: the placeholder until revealed, in both modes', () => {
  controlPlane(true);
  const selected = new api.CanaryApi(PAGE, SESSION, { credential: 'session', registeredNode: REG });
  for (const [base, registration] of [[DIRECT, null], [selected.baseUrl, selected.registeredNode]]) {
    const text = commands(base, null, true, registration);
    assert.equal(text.split('<token>').length - 1, 2, `${registration ?? 'direct'}: header and GET path`);
    assert.doesNotMatch(text, /[0-9a-f]{64}/u, `${registration ?? 'direct'}: no token`);
    assert.match(text, /-H "Authorization: Push <token>" \\/u);
  }
  const { get } = parse(commands(selected.baseUrl, null, true, selected.registeredNode));
  assert.equal(get.searchParams.get('registration'), REG, 'the placeholder doesn’t disturb the selector');
  controlPlane(false);
});

await check('registrations: only one the dashboard could have issued, encoded as a single query value', () => {
  for (const bad of ['', ' ', 'a b', `${REG}&status=down`, `${REG}?x`, '../a', 'a/b', '#a', 'é', 'a'.repeat(129), `${REG}\n`]) {
    assert.throws(() => commands(PAGE, TOKEN, true, bad), /isn’t a node registration on this dashboard/u, JSON.stringify(bad));
  }
  const { post, get } = parse(commands(PAGE, TOKEN, true, 'node:1.a_b-c'));
  assert.equal(post.search, '?registration=node%3A1.a_b-c');
  for (const url of [post, get]) assert.deepEqual(url.searchParams.getAll('registration'), ['node:1.a_b-c']);
  assert.equal(get.searchParams.get('status'), 'up');
});

await check('selection: the commands follow the API object’s registration, the one monitoring requests use', () => {
  controlPlane(true);
  const first = new api.CanaryApi(PAGE, SESSION, { credential: 'session', registeredNode: REG });
  const second = first.withRegisteredNode(REG2);
  const text = commands(second.baseUrl, TOKEN, true, second.registeredNode);
  assert.ok(text.includes(REG2) && !text.includes(REG), 'only the newly selected registration');
  const { post, get } = parse(text);
  for (const url of [post, get]) assert.deepEqual(url.searchParams.getAll('registration'), [REG2]);
  const replaced = first.withToken('push-commands-review-replaced');
  assert.equal(commands(replaced.baseUrl, TOKEN, false, replaced.registeredNode), commands(PAGE, TOKEN, false, REG), 'a replaced dashboard token keeps the selection and changes nothing shown');
  controlPlane(false);
});

await check('PushFields: one generator, masked by default, new copy only with a selection, rotation unchanged', () => {
  const source = readFileSync(resolve(web, 'src/views/MonitorForm.tsx'), 'utf8').replace(/\r\n/gu, '\n');
  const start = source.indexOf('function PushFields(');
  assert.ok(start > 0, 'PushFields');
  const body = source.slice(start, source.indexOf('\n}\n', start));
  assert.match(source, /^import \{ pushCommands \} from '\.\.\/dashboard';$/mu);
  assert.ok(!source.includes('/api/push') && !source.includes('Authorization: Push'), 'no second copy of the commands in the form');
  assert.ok(body.includes('const [shown, setShown] = useState(false);'), 'the token starts hidden');
  assert.ok(body.includes('const registration = api.registeredNode;'), 'the selection monitoring requests use');
  assert.ok(body.includes('<pre className="json-pre">{pushCommands(api.baseUrl, shown ? token : null, allowGet, registration)}</pre>'), 'the placeholder unless shown');
  assert.ok(body.includes('selection && selection.id === registration ? ` (${selection.label})` : \'\''), 'labelled only with the matching selection');
  for (const kept of [
    '>Push token</label>', 'treat it like a password. Viewers never see it.', "{stored ? 'Rotate token' : 'Generate another'}", 'Keep the current token',
    'Saving a new token starts a new check: senders using the old token stop counting.', '<CheckField label="Also accept GET requests"',
    'hint="For tools that can only fetch a URL. The token is then part of the URL, where proxies and logs can record it. POST with a header is safer."',
  ]) assert.ok(body.includes(kept), kept);
  assert.ok(body.includes("{registration === null ? 'Send to any configured node; HTTP success means that node durably recorded it, not that alerts were delivered.'"), 'direct copy unchanged');
  assert.ok(body.includes('} <span className="mono">status</span> is up or down; <span className="mono">msg</span> and <span className="mono">ping</span> (milliseconds) are optional.'));
  assert.match(body, /: `These go through this dashboard to the selected node\$\{label\}; the dashboard’s server forwards the push token, never a sign-in or cookie\. /u);
  const proxy = /\{registration === null \? null : \(\n\s*<small>([^\n]*)<\/small>\n\s*\)\}/u.exec(body);
  assert.ok(proxy, 'the access note appears only with a selected registration');
  for (const said of ['must reach this dashboard’s address', 'Behind a sign-in proxy it must also pass the proxy’s check', 'without <span className="mono">registration</span> straight to the node’s own API', 'Don’t exempt <span className="mono">/api/</span> from the proxy']) {
    assert.ok(proxy[1].includes(said), said);
  }
  assert.doesNotMatch(body, /Bearer|bypass|allowlist|whitelist/iu, 'no other credential and no proxy bypass');
  for (const path of ['src/views/MonitorForm.tsx', 'src/dashboard.ts']) {
    assert.doesNotMatch(readFileSync(resolve(web, path), 'utf8'), /localStorage|sessionStorage|indexedDB|document\.cookie/u, `${path}: no browser storage`);
  }
});

await check('no browser storage, cookies, requests or console output from any command above', () => {
  assert.deepEqual(touched, []);
  assert.equal(consoleCalls, 0);
});

controlPlane(false);
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
