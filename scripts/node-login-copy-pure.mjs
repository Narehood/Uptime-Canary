// Pure copy regressions for the sign-in screen (ConnectScreen in apps/web/src/views/Connect.tsx), rendered to static markup.
// The contract it pins: the browser signs in to one node's management API, and that node's overview includes the peers
// configured on it (their monitors, latest results and incidents), so viewing peers needs no sign-in on each of them.
// Accounts stay local to each node; unpeered nodes aren't gathered in; switching endpoints still signs in there; nothing
// promises failover. The exact visible phrase "Sign in to a node" stays on the screen because the browser auth proofs
// (browser-auth-proof-flows.mjs, browser-auth-proof-regressions.mjs) wait for it after sign-out, disconnect and reload.
// useSession has no server snapshot, so React's useSyncExternalStore is wrapped before anything imports React and server
// rendering reads the client snapshot (no connection, no farewell); the wrap is checked before rendering.
// Static markup only: effects never run (no auth-status probe, no storage write), and the source is imported in place
// through one tsx namespace: no browser, network, copy, scratch or output files. It doesn't check layout or wrapping.
// The screen is then rendered again in gateway mode (apps/web/src/gateway.ts), where the dashboard's own server reaches
// its node: no node address to type, a remembered one ignored, copy that says the sign-in is to this dashboard, and the
// account's node explained second. Those checks run after the direct-mode ones and don't change them.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/node-login-copy-pure.mjs   (exit 1 on any failure)
import { createRequire } from 'node:module';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
const require = createRequire(import.meta.url);
const React = require('react');
const clientStore = React.useSyncExternalStore;
const serverSafeStore = (subscribe, getSnapshot, getServerSnapshot) => clientStore(subscribe, getSnapshot, getServerSnapshot ?? getSnapshot);
React.useSyncExternalStore = serverSafeStore;
globalThis.React ??= React;
let fetches = 0;
globalThis.fetch = async () => { fetches++; throw new Error('the sign-in copy fixture never uses the network'); };

let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const reactModule = await import('react');
if ((reactModule.useSyncExternalStore ?? reactModule.default?.useSyncExternalStore) !== serverSafeStore) {
  console.log('FAIL setup: React was imported before the server-snapshot wrap; nothing was rendered');
  process.exit(1);
}
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'node-login-copy-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const { ConnectScreen, lastEndpoint } = await scope.import(pathToFileURL(resolve(root, 'apps/web/src/views/Connect.tsx')).href, import.meta.url);

const theme = { preference: 'system', mode: 'light', cycle() {} };
const html = renderToStaticMarkup(React.createElement(ConnectScreen, { theme }));
const decode = text => text.replace(/&#x27;/g, '\'').replace(/&quot;/g, '"').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
/** Text a reader sees, roughly as `innerText` joins it: tags become spaces, runs of space collapse. */
const visible = markup => decode(markup.replace(/<[^>]*>/g, ' ')).replace(/\s+/g, ' ').trim();
const part = (re, label) => { const hit = html.match(re); assert.ok(hit, `missing ${label}`); return hit; };

const main = part(/<main class="card connect-card" id="main">(.*?)<\/main>/s, 'sign-in card')[1];
const brand = part(/<section class="connect-brand"[^>]*>(.*?)<\/section>/s, 'brand panel')[1];
const title = visible(part(/<h1>(.*?)<\/h1>/s, 'heading')[1]);
const subtitle = visible(part(/<p class="sec">(.*?)<\/p>/s, 'subtitle')[1]);
const accountNote = visible(part(/<p class="muted small">(.*?)<\/p>/s, 'account note')[1]);
const facts = [...part(/<ul class="connect-facts">(.*?)<\/ul>/s, 'facts')[1].matchAll(/<li>(.*?)<\/li>/gs)].map(([, item]) => visible(item));
const screen = visible(html);

check('renders without the network and keeps one heading', () => {
  assert.equal(fetches, 0, 'static rendering made a request');
  assert.equal(html.match(/<h1\b/g)?.length, 1, 'expected exactly one h1');
  assert.ok(main.includes('<h1>'), 'the heading moved out of the sign-in card');
});

check('the browser proofs\' sign-in wait text is still visible', () => {
  assert.ok(screen.includes('Sign in to a node'), 'browser-auth-proof waits for "Sign in to a node" after sign-out, disconnect and reload');
  assert.ok(subtitle.startsWith('Sign in to a node once'), `the phrase should open the visible subtitle: ${subtitle}`);
  assert.ok(screen.includes('Sign in'), 'dashboard-contract-review waits for "Sign in" after a reload');
});

check('the heading and subtitle lead with one dashboard for several nodes', () => {
  assert.equal(title, 'One dashboard for a node and all its peers');
  assert.equal(subtitle, 'Sign in to a node once with a password or account token to see it and every node peered with it: their monitors, latest results and incidents. You don’t need to sign in to each peer.');
});

check('the brand panel no longer says the dashboard sees one node at a time', () => {
  assert.equal(facts.length, 3);
  assert.equal(facts[0], 'Many nodes, one view. The node you sign in to shows its peers too, each fact as that node knows it.');
  assert.ok(facts[1].includes('in this tab’s memory'), facts[1]);
  assert.ok(facts[2].startsWith('Optional.'), facts[2]);
  assert.ok(brand.includes('<b>Many nodes, one view.</b>'), 'the multi-node point should be the emphasized lead');
  for (const old of [/one node at a time/i, /on any Uptime-Canary node/i, /Talks to/]) assert.doesNotMatch(screen, old);
});

check('account locality stays, after the multi-node copy and before the form', () => {
  assert.equal(accountNote, 'Accounts are local to each node, so use an account on the node at the address below.');
  assert.doesNotMatch(title, /account/i);
  const sub = main.indexOf('<p class="sec">');
  const note = main.indexOf('<p class="muted small">');
  const form = main.indexOf('<form');
  assert.ok(sub >= 0 && sub < note && note < form, `order subtitle ${sub}, note ${note}, form ${form}`);
});

check('no copy asks for a sign-in on each peer or overclaims', () => {
  const rest = screen.replace('You don’t need to sign in to each peer.', '');
  assert.doesNotMatch(rest, /sign(ed)?[ -]in (to|on|at) (each|every|all)\b/i);
  for (const claim of [
    /fail ?over/i, /automatic(ally)? switch/i, /global status/i, /all (of )?your nodes/i, /every node anywhere/i,
    /(full|complete|whole) history/i, /accounts? (are|is) (shared|replicated|synced)/i, /same account (everywhere|on every node)/i,
    /the cluster decided/i, /exactly once/i, /guaranteed/i,
  ]) assert.doesNotMatch(screen, claim);
});

check('fields, choices and the submit button are unchanged', () => {
  for (const label of ['Node API address', 'Account name', 'Password']) assert.match(main, new RegExp(`<label for="[^"]+">${label}</label>`), label);
  for (const name of ['node', 'username', 'password']) assert.match(main, new RegExp(`<input[^>]* name="${name}"`), `input ${name}`);
  assert.equal(main.match(/type="radio"/g)?.length, 2, 'password and account-token choices');
  assert.ok(screen.includes('Password Account token'), 'choice labels');
  assert.ok(screen.includes('Base address only, for example http://127.0.0.1:3100'), 'address hint');
  assert.match(main, /<button type="submit" class="btn primary">.*?Sign in<\/button>/s);
});

// ----- Gateway mode: the page's origin is the only address; a remembered private node address is never used or shown.
const PAGE = 'https://uptime.example.test';
const REMEMBERED = 'http://10.20.30.40:3100';
const storageWrites = [];
Object.defineProperty(globalThis, 'localStorage', {
  value: { getItem: key => key === 'uptime-canary.endpoint' ? REMEMBERED : null, setItem: (key, value) => { storageWrites.push([key, value]); }, removeItem() {} },
  configurable: true, writable: true,
});
Object.defineProperty(globalThis, 'location', { value: { origin: PAGE, protocol: 'https:' }, configurable: true, writable: true });
globalThis.__CANARY_DASHBOARD_GATEWAY__ = true;
const gw = renderToStaticMarkup(React.createElement(ConnectScreen, { theme }));
const gwPart = (re, label) => { const hit = gw.match(re); assert.ok(hit, `gateway: missing ${label}`); return hit; };
const gwMain = gwPart(/<main class="card connect-card" id="main">(.*?)<\/main>/s, 'sign-in card')[1];
const gwTitle = visible(gwPart(/<h1>(.*?)<\/h1>/s, 'heading')[1]);
const gwSubtitle = visible(gwPart(/<p class="sec">(.*?)<\/p>/s, 'subtitle')[1]);
const gwAccountNote = visible(gwPart(/<p class="muted small">(.*?)<\/p>/s, 'account note')[1]);
const gwFacts = [...gwPart(/<ul class="connect-facts">(.*?)<\/ul>/s, 'facts')[1].matchAll(/<li>(.*?)<\/li>/gs)].map(([, item]) => visible(item));
const gwScreen = visible(gw);

check('gateway mode: renders without the network, with no node address to type and none remembered', () => {
  assert.equal(fetches, 0, 'static rendering made a request');
  assert.equal(gw.match(/<h1\b/g)?.length, 1, 'expected exactly one h1');
  assert.ok(gwMain.includes('<h1>'), 'the heading moved out of the sign-in card');
  assert.doesNotMatch(gw, /Node API address|Base address only/, 'no node address field or hint');
  assert.doesNotMatch(gw, /<input[^>]* name="node"|<input[^>]* type="url"/, 'no address input');
  assert.ok(!gw.includes(REMEMBERED) && !gw.includes('127.0.0.1'), 'neither a remembered nor the default node address appears');
  assert.equal(lastEndpoint(), PAGE, 'gateway mode always uses the page origin');
  globalThis.__CANARY_DASHBOARD_GATEWAY__ = false;
  assert.equal(lastEndpoint(), REMEMBERED, 'direct mode still reads the remembered address (so the stand-in storage works)');
  globalThis.__CANARY_DASHBOARD_GATEWAY__ = true;
  assert.deepEqual(storageWrites, [], 'rendering stores nothing');
});

check('gateway mode: the heading and subtitle say to sign in to this dashboard once for every peered node', () => {
  assert.equal(gwTitle, 'One dashboard for a node and all its peers');
  assert.equal(gwSubtitle, 'Sign in to this dashboard once with a password or account token to see its node and every node peered with it: their monitors, latest results and incidents. You don’t need to sign in to each peer.');
  assert.ok(gwScreen.includes('Sign in'), 'dashboard-contract-review waits for "Sign in" after a reload');
});

check('gateway mode: the account’s node comes second, and a sign-in proxy is not a Canary sign-in', () => {
  assert.equal(gwAccountNote, 'Accounts are local to each node, so use an account on the node behind this dashboard. A sign-in proxy in front of this page doesn’t sign you in here.');
  assert.doesNotMatch(gwTitle, /account/i);
  const sub = gwMain.indexOf('<p class="sec">');
  const note = gwMain.indexOf('<p class="muted small">');
  const form = gwMain.indexOf('<form');
  assert.ok(sub >= 0 && sub < note && note < form, `order subtitle ${sub}, note ${note}, form ${form}`);
  assert.doesNotMatch(gwScreen, /at the address below/, 'there is no address below');
});

check('gateway mode: the brand panel names one address for many nodes; the other facts are unchanged', () => {
  assert.equal(gwFacts.length, 3);
  assert.equal(gwFacts[0], 'Many nodes, one address. Your browser talks only to this dashboard. Its server reaches its node privately, and that node shows its peers too, each fact as it knows it.');
  assert.deepEqual(gwFacts.slice(1), facts.slice(1));
});

check('gateway mode: no copy asks for a sign-in on each peer, promises single sign-on or overclaims', () => {
  const rest = gwScreen.replace('You don’t need to sign in to each peer.', '');
  assert.doesNotMatch(rest, /sign(ed)?[ -]in (to|on|at) (each|every|all)\b/i);
  for (const claim of [
    /fail ?over/i, /automatic(ally)? switch/i, /global status/i, /all (of )?your nodes/i, /every node anywhere/i,
    /(full|complete|whole) history/i, /accounts? (are|is) (shared|replicated|synced)/i, /same account (everywhere|on every node)/i,
    /the cluster decided/i, /exactly once/i, /guaranteed/i,
    /single sign-on/i, /\bSSO\b/, /sign(ed|s)? (you )?in automatically/i, /automatically sign/i, /no (account|password|token) (is )?needed/i,
  ]) assert.doesNotMatch(gwScreen, claim);
});

check('gateway mode: the sign-in fields, choices and submit button match direct mode', () => {
  for (const label of ['Account name', 'Password']) assert.match(gwMain, new RegExp(`<label for="[^"]+">${label}</label>`), label);
  for (const name of ['username', 'password']) assert.match(gwMain, new RegExp(`<input[^>]* name="${name}"`), `input ${name}`);
  assert.equal(gwMain.match(/type="radio"/g)?.length, 2, 'password and account-token choices');
  assert.ok(gwScreen.includes('Password Account token'), 'choice labels');
  assert.match(gwMain, /<button type="submit" class="btn primary">.*?Sign in<\/button>/s);
});

delete globalThis.__CANARY_DASHBOARD_GATEWAY__;
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
