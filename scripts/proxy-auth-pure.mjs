// Pure request-contract regressions for a dashboard and node that share one origin behind a cookie-gated sign-in
// proxy (dashboard at `/`, node at `/api/`). Requests send cookies only to the page's own origin and keep the
// explicit bearer; redirects, cache and referrer stay off; cancellation and timeouts keep their meaning; an ended
// proxy session is named as a possible cause only at the page's own origin.
// Uses only exported api.ts functions and CanaryApi methods with a stand-in fetch: no network, browser, copy, scratch
// or output files. Node's fetch keeps no cookies, so this pins the options handed to the browser, not the browser's
// own same-origin cookie rule. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/proxy-auth-pure.mjs   (exit 1 on any failure)
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'proxy-auth-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const api = await scope.import(pathToFileURL(resolve(root, 'apps/web/src/api.ts')).href, import.meta.url);
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const PAGE = 'https://uptime.example.test';          // the dashboard's origin; its node answers at /api/ there
const OTHER = 'https://canary-b.example.test:3100';  // a node on its own origin
const TOKEN = 'proxy-review-account-token';
const SESSION = 'proxy-review-session-token';
const ACCOUNT = { id: 'proxy-review', name: 'review', role: 'admin' };
const OVERVIEW = { node: { id: 'proxy-a' }, now: 1, user: { name: 'review', role: 'admin' }, monitors: [] };
const PROXY_HINT = /reload the page to sign in to the proxy again/u;
const CORS_WORDING = /allows this dashboard/u;

/** The browser's `location`; undefined outside a page. */
const onPage = origin => Object.defineProperty(globalThis, 'location', { value: origin === undefined ? undefined : { origin }, configurable: true, writable: true });

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const node = (url, init) => {
  const path = new URL(url).pathname;
  if (init.method === 'DELETE' || path === '/api/auth/logout') return new Response(null, { status: 204 });
  if (path === '/api/auth/status') return json(200, { passwordLogin: true, passwordConfigured: true, setupRequired: false, tokenLogin: true, mfaSupported: true });
  if (path === '/api/auth/login') return json(200, { token: SESSION, expiresAt: 2_000_000_000_000, user: ACCOUNT });
  if (path === '/api/overview') return json(200, OVERVIEW);
  if (path === '/api/auth/me') return json(200, ACCOUNT);
  if (path === '/api/monitors') return json(201, { id: 'proxy-monitor' });
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
  const signIn = await api.passwordSignIn(base, { name: 'review', password: 'proxy-review-password' });
  assert.equal(signIn.kind, 'signed-in');
  await token.overview();
  await token.me();
  await token.createMonitor({ name: 'Proxy review', type: 'http', target: 'https://service.example.test' });
  await token.deleteMonitor('proxy-monitor', 'rev-1');
  await signIn.api.logout();
  await api.fetchPublicStatus(base, 'services');
});

await check('credentials: every request kind at the page origin uses same-origin, so a sign-in proxy gets its own cookie', async () => {
  onPage(PAGE);
  const sent = await everyKind(PAGE);
  assert.equal(sent.length, 8);
  for (const call of sent) {
    assert.ok(call.url.startsWith(`${PAGE}/`), call.url);
    assert.equal(call.init.credentials, 'same-origin', call.url);
  }
});

await check('Authorization: protected requests carry the explicit bearer; public ones carry none', async () => {
  onPage(PAGE);
  const sent = await everyKind(PAGE);
  const expected = [null, null, `Bearer ${TOKEN}`, `Bearer ${TOKEN}`, `Bearer ${TOKEN}`, `Bearer ${TOKEN}`, `Bearer ${SESSION}`, null];
  assert.deepEqual(sent.map(call => header(call, 'authorization')), expected, 'a proxy cookie never replaces the node credential');
  for (const call of sent) assert.equal(header(call, 'content-type'), call.init.body === undefined ? null : 'application/json', call.url);
});

await check('public auth status: a bare GET of /api/auth/status at the node origin, read as before', async () => {
  onPage(PAGE);
  let status;
  const [call, ...rest] = await record(async () => { status = await api.fetchAuthStatus(`${PAGE}/`); });
  assert.equal(rest.length, 0);
  assert.equal(call.url, `${PAGE}/api/auth/status`);
  assert.equal(call.init.method, 'GET');
  assert.equal(call.init.body, undefined);
  assert.equal(header(call, 'authorization'), null);
  assert.equal(call.init.credentials, 'same-origin');
  assert.deepEqual(status, { passwordLogin: true, passwordConfigured: true, setupRequired: false, tokenLogin: true, mfaSupported: true });
  answer = () => json(404, { error: 'not found' });
  assert.deepEqual(await api.fetchAuthStatus(PAGE), { passwordLogin: false, passwordConfigured: false, setupRequired: false, tokenLogin: true, mfaSupported: false }, 'older nodes stay token-only');
});

await check('cross-origin node: requests go to its own origin with same-origin credentials, never include', async () => {
  onPage(PAGE);
  const sent = await everyKind(OTHER);
  sent.push(...await record(async () => { await new api.CanaryApi(PAGE, TOKEN).withBaseUrl(OTHER).overview(); }));
  assert.equal(sent.length, 9);
  for (const call of sent) {
    assert.ok(call.url.startsWith(`${OTHER}/`), `${call.url} must not fall back to the page origin`);
    // The browser attaches cookies for same-origin credentials only to the page's own origin, so none reach this node.
    assert.equal(call.init.credentials, 'same-origin', call.url);
  }
  assert.equal(header(sent.at(-1), 'authorization'), `Bearer ${TOKEN}`, 'a reused account token still travels explicitly');
});

await check('redirects: refused, never followed; no cache or referrer; a refused redirect names the proxy only at the page origin', async () => {
  onPage(PAGE);
  for (const call of [...await everyKind(PAGE), ...await everyKind(OTHER)]) {
    assert.equal(call.init.redirect, 'error', call.url);
    assert.equal(call.init.cache, 'no-store', call.url);
    assert.equal(call.init.referrerPolicy, 'no-referrer', call.url);
  }
  // A browser given redirect 'error' rejects a 302 to the identity provider with a TypeError, like a network failure.
  const loginRedirect = (url, init) => {
    if (init.redirect === 'error') throw new TypeError('Failed to fetch');
    return new Response('<!doctype html><title>Sign in</title>', { status: 200, headers: { 'Content-Type': 'text/html' } });
  };
  answer = loginRedirect;
  const same = await rejection(new api.CanaryApi(PAGE, TOKEN).overview());
  assert.ok(same instanceof api.ApiFailure && same.status === 0, String(same));
  assert.match(same.message, PROXY_HINT);
  assert.doesNotMatch(same.message, CORS_WORDING, 'a same-origin request has no CORS cause');
  assert.match(api.describeFailure(same), PROXY_HINT, 'the stale-data banner and sign-in form show the hint');
  const signIn = await rejection(api.passwordSignIn(PAGE, { name: 'review', password: 'proxy-review-password' }));
  assert.ok(signIn instanceof api.ApiFailure && signIn.status === 0);
  assert.match(signIn.message, PROXY_HINT);
  const other = await rejection(new api.CanaryApi(OTHER, TOKEN).overview());
  assert.ok(other instanceof api.ApiFailure && other.status === 0);
  assert.match(other.message, CORS_WORDING);
  assert.doesNotMatch(other.message, PROXY_HINT, 'a node elsewhere is never behind the page origin proxy');
  onPage(undefined);
  assert.doesNotMatch((await rejection(new api.CanaryApi(PAGE, TOKEN).overview())).message, PROXY_HINT, 'outside a page there is no page origin');
});

await check('proxy login page: an HTML answer at the page origin names the proxy; elsewhere the old message stays', async () => {
  onPage(PAGE);
  answer = () => new Response('<!doctype html><title>Sign in</title>', { status: 200, headers: { 'Content-Type': 'text/html' } });
  const same = await rejection(new api.CanaryApi(PAGE, TOKEN).overview());
  assert.ok(same instanceof api.ApiFailure && same.status === 200);
  assert.match(same.message, /^The node sent a response this dashboard could not read\. /u);
  assert.match(same.message, PROXY_HINT);
  const probe = await rejection(api.fetchAuthStatus(PAGE));
  assert.ok(probe instanceof api.ApiFailure, 'a login page is not read as a token-only node');
  assert.match(probe.message, PROXY_HINT);
  const other = await rejection(new api.CanaryApi(OTHER, TOKEN).overview());
  assert.equal(other.message, 'The node sent a response this dashboard could not read.');
});

await check('cancellation and timeouts: still AbortError and timeout failures at the page origin, never the proxy hint', async () => {
  onPage(PAGE);
  let sentSignal;
  answer = (url, init) => new Promise((_, reject) => {
    sentSignal = init.signal;
    const fail = () => reject(init.signal.reason ?? new DOMException('aborted', 'AbortError'));
    if (init.signal.aborted) fail(); else init.signal.addEventListener('abort', fail, { once: true });
  });
  const controller = new AbortController();
  const pending = rejection(new api.CanaryApi(PAGE, TOKEN).overview(controller.signal));
  controller.abort();
  const cancelled = await pending;
  assert.ok(api.isAbort(cancelled), `caller cancellation must stay an AbortError, got ${cancelled}`);
  assert.equal(sentSignal.aborted, true, 'the caller signal reaches fetch');
  const early = new AbortController();
  early.abort();
  assert.ok(api.isAbort(await rejection(api.fetchAuthStatus(PAGE, early.signal))), 'an already-cancelled probe');

  // Controllable stand-ins for the request timers, so the bound is read and fired without waiting for it.
  const realTimeout = Object.getOwnPropertyDescriptor(AbortSignal, 'timeout');
  const timers = [];
  Object.defineProperty(AbortSignal, 'timeout', { ...realTimeout, value: ms => { const timer = new AbortController(); timers.push({ ms, timer }); return timer.signal; } });
  try {
    const slow = rejection(new api.CanaryApi(PAGE, TOKEN).overview());
    const slowLogout = rejection(new api.CanaryApi(PAGE, SESSION, { credential: 'session', expiresAt: 2_000_000_000_000 }).logout());
    assert.deepEqual(timers.map(t => t.ms), [15_000, 5_000], 'requests are bounded; sign-out is shorter');
    for (const { timer } of timers) timer.abort(new DOMException('timed out', 'TimeoutError'));
    const late = await slow;
    assert.ok(late instanceof api.ApiFailure && late.status === 0, String(late));
    assert.match(late.message, /did not answer within 15 seconds/u);
    assert.doesNotMatch(late.message, PROXY_HINT);
    assert.match((await slowLogout).message, /did not answer within 5 seconds/u);
  } finally {
    Object.defineProperty(AbortSignal, 'timeout', realTimeout);
  }
});

await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
