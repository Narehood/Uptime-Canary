// Pure regressions for the dashboard gateway mode (apps/web/src/gateway.ts): the dashboard container serves a
// /canary-config.js that turns the mode on, and its server forwards /api/ and /public/ to one node it is configured
// with. In that mode the browser talks only to the page's own origin: every request stays there with same-origin
// cookies and the explicit Canary bearer, and any other origin (a peer's, a typed, linked or remembered one) is refused
// before a request leaves, never re-aimed at the page. Direct mode (the source copy of the file, or no file) is
// unchanged. Uses exported api.ts functions and CanaryApi methods with a stand-in fetch, evaluates the two config files
// in empty VM contexts and reads index.html, vite.config.ts, Dockerfile.web and the web sources as text: no network,
// browser, build, copy, scratch or output files. Node's fetch keeps no cookies, so this pins the options handed to the
// browser, not the browser's own cookie rule or the server's forwarding. The image build copies only apps/, packages/
// and its scripts, so there the container-file group prints SKIP instead of running. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/dashboard-gateway-pure.mjs   (exit 1 on any failure)
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { runInNewContext } from 'node:vm';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const web = resolve(root, 'apps/web');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'dashboard-gateway-pure', tsconfig: resolve(web, 'tsconfig.json') });
const api = await scope.import(pathToFileURL(resolve(web, 'src/api.ts')).href, import.meta.url);
const gateway = await scope.import(pathToFileURL(resolve(web, 'src/gateway.ts')).href, import.meta.url);
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const PAGE = 'https://uptime.example.test';          // the dashboard's origin; its server forwards /api/ and /public/
const OTHER = 'https://canary-b.example.test:3100';  // a peer's own address
const FOREIGN = [
  OTHER,
  'http://uptime.example.test',        // same host, other scheme
  'https://uptime.example.test:8443',  // same host, other port
  'https://api.uptime.example.test',   // a subdomain
  'http://127.0.0.1:3100',             // the direct-mode default, as an old remembered address
  'http://node:3100',                  // the bundle's private node, which only the dashboard's server can reach
];
const TOKEN = 'gateway-review-account-token';
const SESSION = 'gateway-review-session-token';
const ACCOUNT = { id: 'gateway-review', name: 'review', role: 'admin' };
const OVERVIEW = { node: { id: 'gateway-a' }, now: 1, user: { name: 'review', role: 'admin' }, monitors: [] };
const PROXY_HINT = /reload the page to sign in to the proxy again/u;
const CORS_WORDING = /allows this dashboard|CANARY_CORS_ORIGINS/u;

/** The deployment's choice, as /canary-config.js leaves it on the page's global object. */
const mode = value => { if (value === undefined) delete globalThis.__CANARY_DASHBOARD_GATEWAY__; else globalThis.__CANARY_DASHBOARD_GATEWAY__ = value; };
/** The browser's `location`; undefined outside a page. */
const onPage = origin => Object.defineProperty(globalThis, 'location', { value: origin === undefined ? undefined : { origin }, configurable: true, writable: true });
const storageWrites = [];
Object.defineProperty(globalThis, 'localStorage', {
  value: { getItem: () => null, setItem: (key, value) => { storageWrites.push([key, value]); }, removeItem: key => { storageWrites.push([key, null]); } },
  configurable: true, writable: true,
});

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const node = (url, init) => {
  const path = new URL(url).pathname;
  if (init.method === 'DELETE' || path === '/api/auth/logout') return new Response(null, { status: 204 });
  if (path === '/api/auth/status') return json(200, { passwordLogin: true, passwordConfigured: true, setupRequired: false, tokenLogin: true, mfaSupported: true });
  if (path === '/api/auth/login') return json(200, { token: SESSION, expiresAt: 2_000_000_000_000, user: ACCOUNT });
  if (path === '/api/overview') return json(200, OVERVIEW);
  if (path === '/api/auth/me') return json(200, ACCOUNT);
  if (path === '/api/monitors') return json(201, { id: 'gateway-monitor' });
  if (path.startsWith('/public/status/')) return json(200, { slug: 'services', title: 'Services', sections: [] });
  return json(404, { error: 'not found' });
};
let answer = node;
let calls = [];
globalThis.fetch = async (url, init = {}) => { calls.push({ url: String(url), init }); return answer(String(url), init); };
const record = async run => { calls = []; answer = node; await run(); return calls; };
const header = (call, name) => new Headers(call.init.headers).get(name);
const rejection = async promise => { try { await promise; } catch (error) { return error; } assert.fail('expected the request to fail'); };

/** One of every request kind: public status and sign-in, protected reads and writes, session sign-out, public page. */
const everyKind = base => record(async () => {
  const token = new api.CanaryApi(base, TOKEN);
  await api.fetchAuthStatus(base);
  const signIn = await api.passwordSignIn(base, { name: 'review', password: 'gateway-review-password' });
  assert.equal(signIn.kind, 'signed-in');
  await token.overview();
  await token.me();
  await token.createMonitor({ name: 'Gateway review', type: 'http', target: 'https://service.example.test' });
  await token.deleteMonitor('gateway-monitor', 'rev-1');
  await signIn.api.logout();
  await api.fetchPublicStatus(base, 'services');
});
/** Every exported way to reach `base`, each expected to be refused; reports how many requests were attempted. */
const refusals = async base => {
  const errors = [];
  const sent = await record(async () => {
    errors.push(await rejection(api.fetchAuthStatus(base)));
    errors.push(await rejection(api.passwordSignIn(base, { name: 'review', password: 'gateway-review-password' })));
    const token = new api.CanaryApi(base, TOKEN);
    errors.push(await rejection(token.overview()));
    errors.push(await rejection(token.me()));
    errors.push(await rejection(token.createMonitor({ name: 'Gateway review', type: 'http', target: 'https://service.example.test' })));
    errors.push(await rejection(new api.CanaryApi(base, SESSION, { credential: 'session', expiresAt: 2_000_000_000_000 }).logout()));
    errors.push(await rejection(api.fetchPublicStatus(base, 'services')));
    errors.push(await rejection(new api.CanaryApi(PAGE, TOKEN).withBaseUrl(base).overview()));
  });
  return { errors, sent };
};

await check('mode: only an explicit true turns gateway mode on; a missing or odd value is direct mode', () => {
  for (const value of [undefined, false, 'true', 1, null, {}, [true]]) {
    mode(value);
    assert.equal(gateway.gatewayMode(), false, `value ${JSON.stringify(value)}`);
  }
  mode(true);
  assert.equal(gateway.gatewayMode(), true);
  for (const [location, expected] of [[undefined, null], [{ origin: 'null' }, null], [{ origin: 'file://' }, null], [{ origin: `${PAGE}/x` }, null], [{ origin: PAGE }, PAGE], [{ origin: 'http://127.0.0.1:18347' }, 'http://127.0.0.1:18347']]) {
    Object.defineProperty(globalThis, 'location', { value: location, configurable: true, writable: true });
    assert.equal(gateway.pageOrigin(), expected, JSON.stringify(location));
  }
  Object.defineProperty(globalThis, 'location', { get() { throw new Error('no location here'); }, configurable: true });
  assert.equal(gateway.pageOrigin(), null, 'a throwing location reads as no page');
  onPage(undefined);
  mode(undefined);
});

const runConfig = path => { const context = {}; runInNewContext(readFileSync(resolve(root, path), 'utf8'), context, { filename: path }); return context; };

await check('config: the source copy is direct mode and runs as a plain script before the app', () => {
  const source = runConfig('apps/web/public/canary-config.js');
  assert.deepEqual(Object.keys(source), ['__CANARY_DASHBOARD_GATEWAY__'], 'the source copy defines only the flag');
  assert.equal(source.__CANARY_DASHBOARD_GATEWAY__, false);
  const html = readFileSync(resolve(web, 'index.html'), 'utf8');
  const scripts = [...html.matchAll(/<script\b([^>]*)>/g)].map(([, attrs]) => attrs.trim()).filter(attrs => /\bsrc=/.test(attrs));
  assert.deepEqual(scripts, ['src="/canary-config.js"', 'type="module" src="/src/main.tsx"'], 'a plain, blocking config script, then the app module');
  assert.doesNotMatch(readFileSync(resolve(web, 'vite.config.ts'), 'utf8'), /publicDir/, 'Vite copies apps/web/public into the build');
});

const containerFiles = ['Dockerfile.web', 'apps/node-go/internal/canary/dashboard_server.go'];
if (containerFiles.every(path => !existsSync(resolve(root, path)))) {
  console.log(`SKIP config: server sources are not in this static-only tree`);
} else await check('config: the container runs the account service and generates gateway mode before the app loads', () => {
  const dockerfile = readFileSync(resolve(root, 'Dockerfile.web'), 'utf8');
  const server = readFileSync(resolve(root, 'apps/node-go/internal/canary/dashboard_server.go'), 'utf8');
  assert.ok(dockerfile.includes('COPY --from=build /app/apps/web/dist /app/web'));
  assert.ok(dockerfile.includes('CMD ["/usr/local/bin/canary", "dashboard"]'));
  assert.ok(server.includes('__CANARY_DASHBOARD_GATEWAY__ = true;'));
  assert.ok(server.includes('__CANARY_DASHBOARD_CONTROL_PLANE__ = true;'));
});

await check('gateway requests: every kind stays at the page origin with same-origin cookies, the explicit bearer, no redirects, cache or referrer', async () => {
  mode(true); onPage(PAGE);
  storageWrites.length = 0;
  const sent = await everyKind(PAGE);
  assert.equal(sent.length, 8);
  for (const call of sent) {
    assert.equal(new URL(call.url).origin, PAGE, call.url);
    assert.ok(/^\/(api|public)\//u.test(new URL(call.url).pathname), `${call.url} is a path the dashboard's server forwards`);
    assert.equal(call.init.credentials, 'same-origin', call.url);
    assert.equal(call.init.redirect, 'error', call.url);
    assert.equal(call.init.cache, 'no-store', call.url);
    assert.equal(call.init.referrerPolicy, 'no-referrer', call.url);
    assert.ok(!call.url.includes(TOKEN) && !call.url.includes(SESSION), 'credentials never go into an address');
  }
  const expected = [null, null, `Bearer ${TOKEN}`, `Bearer ${TOKEN}`, `Bearer ${TOKEN}`, `Bearer ${TOKEN}`, `Bearer ${SESSION}`, null];
  assert.deepEqual(sent.map(call => header(call, 'authorization')), expected, 'the dashboard origin never stands in for the Canary credential');
  assert.deepEqual(storageWrites, [], 'credentials stay in memory');
});

await check('foreign origins: refused before any request, explicitly, never re-aimed at the page origin', async () => {
  mode(true); onPage(PAGE);
  for (const base of FOREIGN) {
    const { errors, sent } = await refusals(base);
    assert.equal(sent.length, 0, `${base}: ${sent.map(call => call.url).join(', ')}`);
    assert.equal(errors.length, 8);
    for (const error of errors) {
      assert.ok(error instanceof api.OriginRefused, `${base}: ${error}`);
      assert.ok(!(error instanceof api.ApiFailure), 'not a node answer, so never read as token-only or stale data');
      assert.equal(error.name, 'OriginRefused');
      assert.equal(error.origin, new URL(base).origin);
      assert.ok(error.message.includes(PAGE) && error.message.includes(new URL(base).origin), error.message);
      assert.ok(!error.message.includes(TOKEN) && !error.message.includes(SESSION), 'no credential in the refusal');
      assert.equal(api.describeFailure(error), error.message);
    }
  }
  assert.throws(() => api.fetchPublicStatus(OTHER, 'Not a slug'), /Invalid status page address/u, 'slug checks still come first');
});

await check('no page origin: a gateway dashboard outside a web page contacts nothing, not even its own address', async () => {
  mode(true); onPage(undefined);
  const { errors, sent } = await refusals(PAGE);
  assert.equal(sent.length, 0);
  for (const error of errors) {
    assert.ok(error instanceof api.OriginRefused, String(error));
    assert.match(error.message, /no web address/u);
  }
  onPage('null');
  assert.ok((await rejection(new api.CanaryApi(PAGE, TOKEN).overview())) instanceof api.OriginRefused, 'an opaque origin is no page origin');
});

await check('direct mode: the source config and a missing config keep contacting the typed node, as before', async () => {
  onPage(PAGE);
  for (const value of [false, undefined]) {
    mode(value);
    const sent = await everyKind(OTHER);
    assert.equal(sent.length, 8, `mode ${value}`);
    for (const call of sent) {
      assert.ok(call.url.startsWith(`${OTHER}/`), call.url);
      assert.equal(call.init.credentials, 'same-origin', call.url);
    }
    answer = () => { throw new TypeError('Failed to fetch'); };
    assert.match((await rejection(new api.CanaryApi(OTHER, TOKEN).overview())).message, CORS_WORDING, 'cross-origin failures keep their wording');
  }
});

await check('gateway failures: a down server, an unset or unreachable node read as the dashboard server\'s, never CORS', async () => {
  mode(true); onPage(PAGE);
  answer = () => { throw new TypeError('Failed to fetch'); };
  const down = await rejection(new api.CanaryApi(PAGE, TOKEN).overview());
  assert.ok(down instanceof api.ApiFailure && down.status === 0, String(down));
  assert.match(down.message, /^Cannot reach this dashboard’s server\. /u);
  assert.match(down.message, PROXY_HINT);
  assert.doesNotMatch(down.message, CORS_WORDING);
  for (const detail of ['Dashboard node upstream is not configured.', 'The dashboard cannot reach its configured node.']) {
    answer = () => json(503, { error: detail });
    const unavailable = await rejection(new api.CanaryApi(PAGE, TOKEN).overview());
    assert.ok(unavailable instanceof api.ApiFailure && unavailable.status === 503);
    assert.equal(api.describeFailure(unavailable), `The node can’t handle requests right now. ${detail}`);
    assert.ok((await rejection(api.fetchAuthStatus(PAGE))) instanceof api.ApiFailure, 'an unavailable node is not read as a token-only node');
  }
  const plain = status => () => new Response('upstream failed', { status, headers: { 'Content-Type': 'text/plain' } });
  answer = plain(502);
  assert.equal((await rejection(new api.CanaryApi(PAGE, TOKEN).overview())).message, 'The dashboard’s server couldn’t get an answer from its node.');
  answer = plain(504);
  assert.equal((await rejection(new api.CanaryApi(PAGE, TOKEN).overview())).message, 'The dashboard’s server couldn’t get an answer from its node in time.');
  answer = () => new Response('<!doctype html><title>Sign in</title>', { status: 200, headers: { 'Content-Type': 'text/html' } });
  const login = await rejection(new api.CanaryApi(PAGE, TOKEN).overview());
  assert.match(login.message, /^The node sent a response this dashboard could not read\. /u);
  assert.match(login.message, PROXY_HINT);
  mode(false);
  answer = plain(502);
  assert.equal((await rejection(new api.CanaryApi(PAGE, TOKEN).overview())).message, 'The node returned HTTP 502.', 'direct mode keeps its wording');
});

await check('cancellation and timeouts: unchanged in gateway mode', async () => {
  mode(true); onPage(PAGE);
  let sentSignal;
  answer = (url, init) => new Promise((_, reject) => {
    sentSignal = init.signal;
    const fail = () => reject(init.signal.reason ?? new DOMException('aborted', 'AbortError'));
    if (init.signal.aborted) fail(); else init.signal.addEventListener('abort', fail, { once: true });
  });
  const controller = new AbortController();
  const pending = rejection(new api.CanaryApi(PAGE, TOKEN).overview(controller.signal));
  controller.abort();
  assert.ok(api.isAbort(await pending), 'caller cancellation stays an AbortError');
  assert.equal(sentSignal.aborted, true, 'the caller signal reaches fetch');

  const realTimeout = Object.getOwnPropertyDescriptor(AbortSignal, 'timeout');
  const timers = [];
  Object.defineProperty(AbortSignal, 'timeout', { ...realTimeout, value: ms => { const timer = new AbortController(); timers.push({ ms, timer }); return timer.signal; } });
  try {
    const refused = await rejection(new api.CanaryApi(OTHER, TOKEN).overview());
    assert.ok(refused instanceof api.OriginRefused);
    assert.equal(timers.length, 0, 'a refused request starts no timer');
    const slow = rejection(new api.CanaryApi(PAGE, TOKEN).overview());
    const slowLogout = rejection(new api.CanaryApi(PAGE, SESSION, { credential: 'session', expiresAt: 2_000_000_000_000 }).logout());
    assert.deepEqual(timers.map(t => t.ms), [15_000, 5_000], 'requests are bounded; sign-out is shorter');
    for (const { timer } of timers) timer.abort(new DOMException('timed out', 'TimeoutError'));
    assert.match((await slow).message, /did not answer within 15 seconds/u);
    assert.match((await slowLogout).message, /did not answer within 5 seconds/u);
  } finally {
    Object.defineProperty(AbortSignal, 'timeout', realTimeout);
  }
});

await check('source: api.ts is the only web module that opens requests, so its origin check covers every one', () => {
  const files = [];
  const walk = dir => { for (const entry of readdirSync(dir, { withFileTypes: true })) { const path = join(dir, entry.name); if (entry.isDirectory()) walk(path); else if (/\.(ts|tsx)$/u.test(entry.name)) files.push(path); } };
  walk(resolve(web, 'src'));
  assert.ok(files.length > 20, `found ${files.length} sources`);
  const opener = /\bfetch\s*\(|new\s+(XMLHttpRequest|EventSource|WebSocket)\b|\.sendBeacon\s*\(|\bimport\s*\(\s*['"`]https?:/u;
  const offenders = files.filter(path => !path.endsWith(join('src', 'api.ts')) && opener.test(readFileSync(path, 'utf8')));
  assert.deepEqual(offenders, []);
  const source = readFileSync(resolve(web, 'src/api.ts'), 'utf8');
  assert.equal(source.match(/\bfetch\s*\(/gu)?.length, 1, 'one fetch call, inside request()');
});

mode(undefined);
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
