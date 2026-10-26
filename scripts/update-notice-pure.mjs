// Pure regressions for the control-plane update notice (apps/web/src/updates.ts, views/Updates.tsx and its use in the
// App.tsx shell). The contract is .canary/update-notice-contract.md with the shared DTOs in packages/shared/src/updates.ts:
// GET /api/dashboard/updates (every signed-in dashboard role) and POST /api/dashboard/updates/check (administrators) both
// answer UpdateStatus. GET never makes the server read a registry; the browser never asks a registry or GitHub, and
// nothing pulls, installs or changes an image tag.
// It pins: the reader (every field checked, the dashboard first, commits only as hexadecimal, image names only from the
// fixed official list); that no row claims "current", "newer" or an update its own commits and image can't support; the
// counts and the notice (only available updates; ahead, unknown, unavailable and checks off stay quiet); that every link is
// fixed or a compare link of two validated full commits; that every role reads the status and only administrators get
// Check now; the wording (the source comparison's limit, no digest claim, nothing installed, pinned tags kept); the shared
// feed (one read a minute at most across renders and remounts, waiting while hidden or rejected, cancelled when left,
// stopped on 404) and the manual check (one request however often pressed, never re-sent, the newest answer kept); the two
// API routes; and that only a signed-in control plane asks anything or shows the entry.
// r2 (.canary/update-image-build-metadata-v2.md): the official publisher stamps a build time into each image
// (`currentBuiltAt`/`latestBuiltAt`, epoch ms). For the same commit a later latest build is an update, an equal one
// current and an earlier one newer; without both valid times the same commit is unknown. Different commits ignore build
// times. Pinned here: those claims, invalid and implausible times, the rebuilt-image card (both build times in UTC and
// local time, no compare link, no claim about what changed inside), the notice and a dismissed banner coming back for a
// newer build, and honest unknowns for images without build times. Two r1 expectations changed with that contract;
// they're marked "r2 expectation change" where they are.
// Uses exported functions with stand-in timers, a stand-in fetch and stand-in `location`/`document`, renders components and
// the signed-in App to static markup (effects never run), and reads sources as text: no network, browser, build, copy,
// scratch or output files. CanaryApi.updates and checkUpdates are root's (through own()); until they exist the API group
// fails and says so. It doesn't prove the browser, the Go server or a registry.
// Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/update-notice-pure.mjs   (exit 1 on any failure)
import { existsSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const web = resolve(root, 'apps/web');
const PAGE = 'https://uptime.example.test';
Object.defineProperty(globalThis, 'location', { value: { origin: PAGE, protocol: 'https:', hash: '', pathname: '/' }, configurable: true, writable: true });
globalThis.document ??= { addEventListener() {}, removeEventListener() {}, hidden: false, visibilityState: 'visible' };
const storageWrites = [];
const storage = { getItem: () => null, setItem: (...args) => storageWrites.push(args), removeItem: (...args) => storageWrites.push(args) };
Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true, writable: true });
Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true, writable: true });
const require = createRequire(import.meta.url);
const React = require('react');
// As in registry-maintenance-pure.mjs: stores without a server snapshot read the client one, wrapped before anything
// else imports React, so the signed-in App renders. Checked before that render.
const clientStore = React.useSyncExternalStore;
const serverSafeStore = (subscribe, getSnapshot, getServerSnapshot) => clientStore(subscribe, getSnapshot, getServerSnapshot ?? getSnapshot);
React.useSyncExternalStore = serverSafeStore;
const reactModule = await import('react');
const storeWrapped = (reactModule.useSyncExternalStore ?? reactModule.default?.useSyncExternalStore) === serverSafeStore;
const { renderToStaticMarkup } = await import('react-dom/server');
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'update-notice-pure', tsconfig: resolve(web, 'tsconfig.json') });
const load = path => scope.import(pathToFileURL(resolve(web, path)).href, import.meta.url);
const api = await load('src/api.ts');
const sessionModule = await load('src/session.ts');
const updates = await load('src/updates.ts');
const view = await load('src/views/Updates.tsx');
const format = await load('src/format.ts');
const { MAX_DATE_MS } = await load('src/publicAvailability.ts');
let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };

const HEADER = 'x-canary-registered-node';
const TOKEN = 'update-notice-review-dashboard-token';
const REG = '0f3c2b1a9d8e7f60514233a4b5c6d7e8';
const REG2 = '1a2b3c4d5e6f708192a3b4c5d6e7f809';
const REG3 = 'c'.repeat(32);
const OLD = '34442cc040f2b00f17ba45b3a6bfb338d0552b14';
const NEW = '358ede0'.padEnd(40, 'b');
const DEV = 'abcdef1'.padEnd(40, 'c');
const LONG_OLD = 'e'.repeat(64);
const LONG_NEW = 'f'.repeat(64);
const WEB = updates.OFFICIAL_IMAGES.dashboard;
const NODE = updates.OFFICIAL_IMAGES.node;
const HOUR = 3_600_000;
const NOW = Date.now();
const CHECKED = NOW - 3 * HOUR;
// Whole seconds, like the publisher's RFC 3339 stamps. BUILT is an older build, REBUILT a newer one of the same source,
// both before the check that read them.
const BUILT = Math.floor((NOW - 30 * HOUR) / 1000) * 1000;
const REBUILT = Math.floor((NOW - 6 * HOUR) / 1000) * 1000;
const STATUS = {
  enabled: true, intervalHours: 6, state: 'ok', checkedAt: CHECKED, nextCheckAt: NOW + 3 * HOUR + 30_000,
  images: [
    { component: 'node', registrationId: REG, label: 'Rack B', image: `${NODE}:latest`, currentCommit: OLD.toUpperCase(), latestCommit: NEW, status: 'update-available' },
    // r2: equal build times keep this same-commit row current under the accepted build-time rule. The same row without
    // them, as r1 sent it, is now unknown (provenance group).
    { component: 'dashboard', label: 'Dashboard', image: WEB, currentCommit: NEW, latestCommit: NEW, currentBuiltAt: BUILT, latestBuiltAt: BUILT, status: 'current' },
    { component: 'node', registrationId: REG2, label: 'Lab', image: NODE, currentCommit: DEV, latestCommit: NEW, status: 'ahead' },
    { component: 'node', label: 'Old node', image: NODE, latestCommit: NEW, status: 'unknown' },
    { component: 'node', registrationId: REG3, label: 'Edge', image: NODE, currentCommit: OLD, status: 'unavailable' },
  ],
};
const BASE = { enabled: true, intervalHours: 6, state: 'ok', images: [] };
const ACCOUNT = { id: 'acct-1', name: 'review', role: 'admin', passwordEnabled: true };
const EMPTY = { node: { id: 'dashboard', name: 'Dashboard' }, now: NOW, monitors: [], nodes: [], groups: [], maintenance: [], incidents: [], notifications: [], user: { name: 'review', role: 'admin' } };

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const server = url => {
  const { pathname } = new URL(url);
  if (pathname === '/api/overview') return json(200, EMPTY);
  if (pathname === '/api/auth/me') return json(200, ACCOUNT);
  if (pathname === '/api/dashboard/updates') return json(200, STATUS);
  if (pathname === '/api/dashboard/updates/check') return json(202, { ...STATUS, state: 'checking' });
  return json(200, []);
};
let calls = [];
globalThis.fetch = async (url, init = {}) => { calls.push({ url: String(url), init }); return server(String(url)); };
const record = async run => { calls = []; await run(); return calls; };
const header = (call, name) => new Headers(call.init.headers).get(name);
const on = () => { globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__ = true; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; };
const off = () => { delete globalThis.__CANARY_DASHBOARD_CONTROL_PLANE__; delete globalThis.__CANARY_DASHBOARD_GATEWAY__; };
// Static markup has no text separators; any comment is dropped without a space, in case a React version adds them.
const text = markup => markup.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]*>/g, ' ').replace(/&#x27;/g, '\'').replace(/&quot;/g, '"').replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/\s+/g, ' ');
// HTML attribute names are ASCII case-insensitive and React 19 keeps some JSX spellings (referrerPolicy), so a tag's
// attributes are read by lowercased name. A name given twice fails: a browser keeps only the first one.
const attributes = tag => {
  const found = new Map();
  for (const [, name, value] of tag.replace(/^<[a-z]+/iu, '').matchAll(/\s([^\s"'<>\/=]+)(?:="([^"]*)")?/gu)) {
    const key = name.toLowerCase();
    assert.ok(!found.has(key), `${key} appears twice in ${tag}`);
    found.set(key, value ?? '');
  }
  return found;
};
const buttons = markup => [...markup.matchAll(/<button\b[^>]*>[\s\S]*?<\/button>/gu)].map(([whole]) => ({
  attrs: attributes(whole.match(/^<button\b[^>]*>/u)[0]),
  label: text(whole.replace(/^<button\b[^>]*>/u, '').replace(/<\/button>$/u, '')).trim(),
}));
const links = markup => [...markup.matchAll(/<a\b[^>]*>/gu)].map(([tag]) => attributes(tag));
const shell = (report, extra = {}) => ({ key: 'review-feed', snapshot: { ...updates.NO_UPDATES, report, ...extra }, check() {} });
const read = status => updates.readUpdateStatus(status);
const dialog = (status, admin, extra = {}) => renderToStaticMarkup(React.createElement(view.UpdatesDialog, { updates: shell(status === null ? null : read(status), extra), admin, skew: 0, onClose() {} }));
const rowOf = fields => read({ enabled: true, state: 'ok', images: [{ component: 'node', registrationId: REG, label: 'Rack B', image: NODE, ...fields }] }).rows[0];
// The same source on both sides, read by a check at CHECKED unless the envelope says otherwise.
const sameAnswer = (fields, envelope = {}) => ({ enabled: true, state: 'ok', checkedAt: CHECKED, ...envelope, images: [{ component: 'node', registrationId: REG, label: 'Rack B', image: NODE, currentCommit: NEW, latestCommit: NEW, ...fields }] });
const sameRow = (fields, envelope) => read(sameAnswer(fields, envelope)).rows[0];
const esc = value => value.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&');
// A build time as the card shows it: the UTC instant, then local time labelled "your time" (text() folds any spaces).
const shownBuild = at => `${esc(format.utc(at))} ${esc(format.longStamp(at).replace(/\s+/gu, ' '))} your time`;

// Stand-in timers: `advance` runs every timer due up to the new time, in order, and lets answers settle in between.
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise(done => setImmediate(done)); };
const timeline = () => {
  let now = 5_000_000;
  let id = 0;
  const queue = new Map();
  return {
    timers: { now: () => now, set: (run, ms) => { queue.set(++id, { at: now + ms, run }); return id; }, clear: handle => { queue.delete(handle); } },
    get now() { return now; },
    due: () => [...queue.values()].map(entry => entry.at - now).sort((a, b) => a - b),
    async advance(ms) {
      const end = now + ms;
      for (;;) {
        const next = [...queue.entries()].filter(([, entry]) => entry.at <= end).sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
        if (!next) break;
        queue.delete(next[0]);
        now = next[1].at;
        next[1].run();
        await flush();
      }
      now = end;
      await flush();
    },
  };
};
/** The dashboard's two routes as the feed sees them; `state` decides each answer when it's asked. */
const stand = () => {
  const log = { read: [], check: [] };
  const state = { hidden: false, read: () => BASE, check: () => ({ ...BASE, state: 'checking' }) };
  const src = {
    paused: () => state.hidden,
    read: signal => { log.read.push(signal); return Promise.resolve().then(() => state.read(signal)); },
    check: signal => { log.check.push(signal); return Promise.resolve().then(() => state.check(signal)); },
  };
  return { src, log, state };
};
const hang = signal => new Promise((_, reject) => signal.addEventListener('abort', () => reject(new DOMException('Request cancelled', 'AbortError')), { once: true }));
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };

await check('reader: a complete answer is read field by field; the dashboard comes first and nodes keep their order', () => {
  const report = read(STATUS);
  assert.deepEqual([report.enabled, report.intervalHours, report.state, report.checkedAt, report.nextCheckAt], [true, 6, 'ok', STATUS.checkedAt, STATUS.nextCheckAt]);
  assert.deepEqual(report.rows.map(row => [row.key, row.label, row.status]), [
    ['dashboard', 'Dashboard', 'current'],
    [`node:${REG}`, 'Rack B', 'update-available'],
    [`node:${REG2}`, 'Lab', 'ahead'],
    ['node#3', 'Old node', 'unknown'],
    [`node:${REG3}`, 'Edge', 'unavailable'],
  ]);
  const rack = report.rows[1];
  assert.equal(rack.currentCommit, OLD, 'hexadecimal commits are lowercased');
  assert.equal(rack.image, NODE, 'the official name from the fixed list; the tag isn’t shown');
  assert.equal(rack.registrationId, REG);
  assert.equal(report.rows[3].currentCommit, undefined);
  // The fixed names are the published images the deployment templates run.
  assert.ok(readFileSync(resolve(root, 'deploy/portainer-dashboard.yml'), 'utf8').includes(`${WEB}:`), WEB);
  assert.ok(readFileSync(resolve(root, 'deploy/portainer-node.yml'), 'utf8').includes(`${NODE}:`), NODE);
  assert.deepEqual(updates.OFFICIAL_IMAGES, { dashboard: 'ghcr.io/narehood/uptime-canary-web', node: 'ghcr.io/narehood/uptime-canary-node' });
});

await check('reader: anything that isn’t an update status is refused, and odd fields are dropped or cleaned, never shown as sent', () => {
  for (const bad of [null, [], 'ok', 1, {}, { ...STATUS, enabled: 'yes' }, { ...STATUS, state: 'upgrading' }, { ...STATUS, state: undefined }, { ...STATUS, images: {} }, { ...STATUS, images: 'none' }]) {
    assert.equal(read(bad), null, JSON.stringify(bad));
  }
  assert.deepEqual(read({ enabled: true, state: 'not-checked', images: null }).rows, [], 'Go encodes an empty list as null');
  assert.deepEqual(read({ enabled: true, state: 'not-checked' }).rows, []);
  const odd = read({
    ...STATUS, intervalHours: 6.5, checkedAt: -1, nextCheckAt: 1e300,
    images: [
      { component: 'dashboard', label: `Dash${String.fromCharCode(0)}board\n\x07next`, image: WEB, currentCommit: NEW, latestCommit: NEW, status: 'current' },
      { component: 'dashboard', label: 'Second dashboard', image: WEB, status: 'current' },
      { component: 'node', registrationId: REG, label: '   ', image: NODE, status: 'current' },
      { component: 'node', registrationId: REG, label: 'Same registration', image: NODE, status: 'current' },
      { component: 'node', registrationId: 'bad id/../x', label: 'Bad ID', image: NODE, status: 'surprise' },
      { component: 'peer', label: 'Not a component', status: 'current' },
      'not a row',
      null,
      { component: 'node', registrationId: REG2, label: 'x'.repeat(300), image: NODE, status: 'unknown' },
    ],
  });
  assert.equal(odd.intervalHours, null);
  assert.equal('checkedAt' in odd, false);
  assert.equal('nextCheckAt' in odd, false);
  assert.deepEqual(odd.rows.map(row => row.key), ['dashboard', `node:${REG}`, 'node#4', `node:${REG2}`], 'repeats, unknown components and non-rows are left out');
  assert.equal(odd.rows[0].label, 'Dash board next', 'control characters become spaces');
  assert.equal(odd.rows[1].label, 'Unnamed node');
  assert.deepEqual([odd.rows[2].registrationId, odd.rows[2].status], [undefined, 'unknown'], 'a malformed registration and an unknown status');
  assert.equal(odd.rows[3].label.length, 128);
  const many = read({ enabled: true, state: 'ok', images: Array.from({ length: updates.MAX_UPDATE_ROWS + 5 }, (_, i) => ({ component: 'node', label: `n${i}`, status: 'unknown' })) });
  assert.equal(many.rows.length, updates.MAX_UPDATE_ROWS);
});

await check('claims: no row is current, newer or an update unless its own commits and official image support it; checks off compare nothing', () => {
  // r2 expectation change (accepted in .canary/update-image-build-metadata-v2.md): r1 read the same commit as current on
  // its own. A newer build of the same source keeps the commit, so "current" now also needs equal build times, and these
  // rowOf answers carry none. The two cases marked "r2" changed from 'current' to 'unknown'; their dated twins are in the
  // build-time group. The same commit without build times was already unknown for an update or a newer build.
  const cases = [
    [{ status: 'current', latestCommit: NEW }, 'unknown', 'no running commit'],
    [{ status: 'current', currentCommit: NEW }, 'unavailable', 'the latest image wasn’t read'],
    [{ status: 'current', currentCommit: OLD, latestCommit: NEW }, 'unknown', 'different commits aren’t current'],
    [{ status: 'current', currentCommit: NEW.slice(0, 7), latestCommit: NEW }, 'unknown', 'r2: a short form of the same commit, without build times (r1: current)'],
    [{ status: 'update-available', currentCommit: NEW, latestCommit: NEW }, 'unknown', 'the same commit without build times shows nothing newer'],
    [{ status: 'update-available', latestCommit: NEW }, 'unknown', 'no running commit'],
    [{ status: 'ahead', currentCommit: DEV }, 'unavailable', 'no latest commit'],
    [{ status: 'ahead', currentCommit: NEW, latestCommit: NEW }, 'unknown', 'the same commit without build times isn’t newer'],
    [{ status: 'current', currentCommit: NEW, latestCommit: NEW, image: 'ghcr.io/someone/uptime-canary-node' }, 'unknown', 'another publisher'],
    [{ status: 'current', currentCommit: NEW, latestCommit: NEW, image: `${NODE}-fork:latest` }, 'unknown', 'a look-alike name'],
    [{ status: 'current', currentCommit: NEW, latestCommit: NEW, image: undefined }, 'unknown', 'no image'],
    [{ status: 'current', currentCommit: 'javascript:alert(1)', latestCommit: NEW }, 'unknown', 'not a commit'],
    [{ status: 'current', currentCommit: 'g'.repeat(40), latestCommit: 'g'.repeat(40) }, 'unknown', 'not hexadecimal'],
    [{ status: 'banana', currentCommit: NEW, latestCommit: NEW }, 'unknown', 'a status this page doesn’t know'],
    [{ status: 'update-available', currentCommit: OLD, latestCommit: NEW }, 'update-available', 'kept'],
    [{ status: 'ahead', currentCommit: DEV, latestCommit: NEW }, 'ahead', 'kept'],
    [{ status: 'current', currentCommit: NEW, latestCommit: NEW, image: `${NODE}@sha256:${'0'.repeat(64)}` }, 'unknown', 'r2: the digest form, without build times (r1: current)'],
    [{ status: 'unavailable', currentCommit: OLD }, 'unavailable', 'kept'],
  ];
  for (const [fields, expected, why] of cases) assert.equal(rowOf(fields).status, expected, `${why}: ${JSON.stringify(fields)}`);
  assert.equal(rowOf({ status: 'current', currentCommit: 'javascript:alert(1)', latestCommit: NEW }).currentCommit, undefined);
  assert.equal(read({ enabled: true, state: 'ok', images: [{ component: 'dashboard', label: 'Dashboard', image: NODE, currentCommit: NEW, latestCommit: NEW, status: 'current' }] }).rows[0].status, 'unknown', 'the node image isn’t the dashboard’s');
  for (const off of [{ ...STATUS, enabled: false }, { ...STATUS, state: 'disabled' }]) {
    const report = read(off);
    assert.deepEqual([report.enabled, report.state], [false, 'disabled']);
    assert.ok(report.rows.every(row => row.status === 'unknown' && updates.rowStatus(row, report).kind === 'off'), 'nothing is compared');
    assert.equal(updates.updateNotice(report), null);
  }
});

await check('counts and the notice: only available updates count; current, newer, unknown, unavailable and off stay quiet', () => {
  const report = read(STATUS);
  assert.deepEqual(updates.updateCounts(report), { available: 1, current: 1, ahead: 1, unknown: 1, unavailable: 1, total: 5 });
  assert.deepEqual(updates.updateCounts(null), { available: 0, current: 0, ahead: 0, unknown: 0, unavailable: 0, total: 0 });
  const notice = updates.updateNotice(report);
  assert.equal(notice.count, 1);
  assert.equal(notice.title, 'An update is available for Rack B');
  assert.equal(notice.text, 'A newer official image was published. Nothing is pulled or installed automatically; the details say what changed and how to update.');
  const dashboardOnly = read({ ...BASE, images: [{ component: 'dashboard', label: 'Dashboard', image: WEB, currentCommit: OLD, latestCommit: NEW, status: 'update-available' }] });
  assert.equal(updates.updateNotice(dashboardOnly).title, 'An update is available for the dashboard');
  const two = read({ ...STATUS, images: [STATUS.images[0], { ...STATUS.images[1], currentCommit: OLD, status: 'update-available' }] });
  assert.equal(updates.updateNotice(two).title, 'Updates are available for 2 components');
  assert.match(updates.updateNotice(two).text, /^Newer official images were published\./u);
  for (const quiet of [null, read(BASE), read({ ...STATUS, images: STATUS.images.slice(1) }), read({ ...STATUS, state: 'checking', images: STATUS.images.slice(1) })]) {
    assert.equal(updates.updateNotice(quiet), null, JSON.stringify(quiet?.rows));
  }
  // A newer image changes the signature, so a dismissed notice returns; the server's order doesn't.
  const moved = read({ ...STATUS, images: [{ ...STATUS.images[0], latestCommit: DEV }, ...STATUS.images.slice(1)] });
  assert.notEqual(updates.updateNotice(moved).signature, notice.signature);
  assert.equal(updates.updateNotice(read({ ...STATUS, images: [...STATUS.images].reverse() })).signature, notice.signature);
  // The shell: the entry carries the count in its name and badge; the banner shows only for updates and isn't a live alert.
  const entry = report => renderToStaticMarkup(React.createElement(view.UpdatesButton, { updates: shell(report), onOpen() {} }));
  const [busy] = buttons(entry(report));
  assert.deepEqual([busy.attrs.get('type'), busy.attrs.get('aria-haspopup'), busy.attrs.get('aria-label'), busy.label], ['button', 'dialog', 'Software updates: 1 update available', '1']);
  const [calm] = buttons(entry(read({ ...STATUS, images: STATUS.images.slice(1) })));
  assert.deepEqual([calm.attrs.get('aria-label'), calm.label], ['Software updates', '']);
  assert.equal(buttons(entry(null))[0].attrs.get('aria-label'), 'Software updates', 'there before the first answer, so the details stay reachable');
  assert.equal(renderToStaticMarkup(React.createElement(view.UpdatesButton, { updates: { ...shell(null), snapshot: { ...updates.NO_UPDATES, unsupported: true } }, onOpen() {} })), '', 'a server without the route shows nothing');
  const banner = report => renderToStaticMarkup(React.createElement(view.UpdateBanner, { updates: shell(report), onOpen() {} }));
  const shown = banner(report);
  assert.match(text(shown), /An update is available for Rack B A newer official image was published\./u);
  assert.deepEqual(buttons(shown).map(button => button.label), ['Details', 'Not now']);
  assert.doesNotMatch(shown, /role="(alert|status)"/u, 'discreet: not announced as an alert');
  for (const quiet of [null, read(BASE), read({ ...STATUS, enabled: false }), read({ ...STATUS, images: STATUS.images.slice(1) })]) assert.equal(banner(quiet), '');
});

await check('links: every link is fixed, or a compare link of two validated full commits; answer text never reaches an address or raw markup', () => {
  const fixed = ['https://github.com/Narehood/Uptime-Canary/blob/main/docs/upgrades.md', 'https://github.com/Narehood/Uptime-Canary/packages', 'https://github.com/Narehood/Uptime-Canary/actions/workflows/container-images.yml'];
  assert.deepEqual(Object.values(updates.UPDATE_LINKS), fixed);
  const hostile = {
    enabled: true, state: 'ok', checkedAt: NOW,
    images: [
      { component: 'node', registrationId: REG, label: '<img src=x onerror=alert(1)>', image: 'https://evil.example/pkg', currentCommit: OLD, latestCommit: NEW, status: 'update-available', url: 'https://evil.example/', link: 'javascript:alert(1)' },
      { component: 'dashboard', label: 'Dashboard', image: WEB, currentCommit: 'javascript:alert(1)', latestCommit: `https://evil.example/${NEW}`, status: 'update-available' },
      { component: 'node', registrationId: REG2, label: 'Rack C', image: NODE, currentCommit: OLD, latestCommit: NEW, status: 'update-available' },
      { component: 'node', registrationId: REG3, label: 'Short commit', image: NODE, currentCommit: OLD.slice(0, 7), latestCommit: NEW, status: 'update-available' },
      { component: 'node', registrationId: 'd'.repeat(32), label: 'SHA-256', image: NODE, currentCommit: LONG_OLD, latestCommit: LONG_NEW, status: 'update-available' },
    ],
  };
  const markup = dialog(hostile, true);
  const found = links(markup);
  const compare = /^https:\/\/github\.com\/Narehood\/Uptime-Canary\/compare\/(?:[0-9a-f]{40}|[0-9a-f]{64})\.\.\.(?:[0-9a-f]{40}|[0-9a-f]{64})$/u;
  for (const link of found) {
    const href = link.get('href');
    assert.ok(fixed.includes(href) || compare.test(href), href);
    assert.equal(link.get('target'), '_blank', href);
    assert.deepEqual(link.get('rel').split(' ').sort(), ['noopener', 'noreferrer'], href);
    assert.equal(link.get('referrerpolicy'), 'no-referrer', href);
  }
  assert.deepEqual(found.map(link => link.get('href')).filter(href => compare.test(href)), [
    `https://github.com/Narehood/Uptime-Canary/compare/${OLD}...${NEW}`,
    `https://github.com/Narehood/Uptime-Canary/compare/${LONG_OLD}...${LONG_NEW}`,
  ], 'only rows with an official image and two full commits; a short commit isn’t linked');
  assert.deepEqual(found.map(link => link.get('href')).filter(href => fixed.includes(href)), fixed);
  assert.doesNotMatch(markup, /evil\.example|javascript:|<img/u);
  assert.match(markup, /&lt;img src=x onerror=alert\(1\)&gt;/u, 'a label is text');
  for (const [, title] of markup.matchAll(/\btitle="([^"]*)"/gu)) assert.ok(/^[0-9a-f]{7,64}$/u.test(title) || / UTC$/u.test(title), title);
  const rendered = text(markup);
  assert.match(rendered, new RegExp(`Rack C Node · ${NODE} Update available Running source ${OLD.slice(0, 12)} Latest official ${NEW.slice(0, 12)}`, 'u'));
  assert.match(rendered, /<img src=x onerror=alert\(1\)> Node Unknown Running source/u, 'the unofficial image isn’t named');
});

await check('roles: every role reads the components, commits and times; only administrators get Check now, and never while checks are off or running', () => {
  for (const admin of [true, false]) {
    const markup = dialog(STATUS, admin);
    const screen = text(markup);
    for (const label of ['Dashboard', 'Rack B', 'Lab', 'Old node', 'Edge']) assert.match(screen, new RegExp(` ${label} (Dashboard|Node)`, 'u'), label);
    assert.match(screen, new RegExp(`Running source ${OLD.slice(0, 12)} Latest official ${NEW.slice(0, 12)}`, 'u'));
    assert.match(screen, /Last check .*· 3 h ago Next check .*· in 3 h How often Every 6 hours, by this dashboard’s server\. Browsers only read the result it stored\./u);
    assert.match(screen, /Old node Node · \S+ Unknown Running source Not reported Latest official 358ede0b/u);
    assert.match(screen, /Edge Node · \S+ Couldn’t check Running source 34442cc040f2 Latest official Not read/u);
    const actions = buttons(markup).map(button => button.label);
    assert.equal(actions.includes('Check now'), admin, `admin ${admin}: ${actions}`);
    assert.ok(actions.includes('Close'));
    if (admin) assert.equal(buttons(markup).find(button => button.label === 'Check now').attrs.has('disabled'), false);
    else assert.match(screen, /An administrator can ask for a check sooner\./u);
  }
  const offMarkup = dialog({ ...STATUS, enabled: false }, true);
  assert.ok(!buttons(offMarkup).some(button => /Check/u.test(button.label)), 'checks off: nothing to start');
  assert.match(text(offMarkup), /Update checks are off .* Next check None: update checks are off/u);
  const running = buttons(dialog({ ...STATUS, state: 'checking' }, true)).find(button => button.label === 'Checking…');
  assert.ok(running && running.attrs.has('disabled'), 'a running check can’t be started again');
  const sendingMarkup = dialog(STATUS, true, { sending: true });
  assert.ok(buttons(sendingMarkup).find(button => button.label === 'Asking…').attrs.has('disabled'));
  assert.match(text(sendingMarkup), /Asking the dashboard’s server to check…/u);
  const refused = text(dialog(STATUS, true, { checkFailure: new api.ApiFailure(429, 'This dashboard is limiting attempts right now. Wait a moment and try again.') }));
  assert.match(refused, /A check ran recently\. Manual checks are limited to one every 15 minutes; scheduled checks still run\./u);
  const first = dialog(null, true);
  assert.match(text(first), /Reading the update status… .*Reading the stored status…/u);
  assert.ok(!buttons(first).some(button => /Check/u.test(button.label)), 'nothing to start before the first answer');
});

await check('wording: the source comparison’s limit, no digest claim, nothing installed, pinned tags kept, no downgrade, honest unknowns', () => {
  const screen = text(dialog(STATUS, true));
  assert.match(screen, /Nothing here pulls, installs or restarts anything, and pinned tags or digests are never changed\./u);
  assert.match(screen, /Back up first\. Then pull the new image and recreate each container with its existing volumes: a node’s identity and database, the dashboard’s data and identity\./u);
  assert.match(screen, /Update every node and the dashboard together to the same source\. With a pinned tag or digest, choose the new one yourself\./u);
  // r2 expectation change (accepted in .canary/update-image-build-metadata-v2.md): r1 said a rebuild of the same source
  // isn't detected. The publisher's build times now detect a newer build of the same source; what changed inside it,
  // the running digest, and images without build times stay unknown, and the copy says so.
  assert.match(screen, /What this check compares\. It compares the source commit each image was built from\. When the commits match, it compares the build time the official publisher stamped into each image, so a newer build of the same source shows as an update; without that time on both images \(older images and custom builds don’t have it\) the result is unknown\. It can’t see what changed inside a rebuilt image, such as its base image, and the exact image digest each component runs isn’t known to this check\./u);
  assert.doesNotMatch(screen, /isn’t detected here|security|bit-for-bit|identical image/iu, 'no stale r1 limit, and no claim about what a rebuild fixes');
  assert.doesNotMatch(screen, /digest (is|was) (known|verified|checked)|updates? (itself|automatically)|will (pull|install)/iu);
  const report = read(STATUS);
  const ahead = updates.rowStatus(report.rows[2], report);
  assert.deepEqual([ahead.kind, ahead.label], ['ahead', 'Newer than latest']);
  assert.match(ahead.text, /There’s nothing to install, and an older image isn’t suggested\./u);
  assert.doesNotMatch(ahead.text, /update available|downgrade to|install the/iu);
  assert.match(updates.rowStatus(report.rows[3], report).text, /doesn’t report the source it was built from \(an older image or a build from source\)/u);
  assert.match(updates.rowStatus(rowOf({ status: 'current', image: 'ghcr.io/someone/else', currentCommit: NEW, latestCommit: NEW }), report).text, /No official image is named for it/u);
  const titles = Object.fromEntries(['not-checked', 'checking', 'unavailable', 'disabled'].map(state => [state, updates.updateSummary({ ...updates.NO_UPDATES, report: read({ ...BASE, state }) }).title]));
  assert.deepEqual(titles, { 'not-checked': 'Not checked yet', checking: 'Checking now', unavailable: 'The last check couldn’t finish', disabled: 'Update checks are off' });
  assert.equal(updates.updateSummary({ ...updates.NO_UPDATES, report: read({ ...STATUS, images: STATUS.images.slice(1, 3) }) }).title, 'Up to date');
  assert.deepEqual(updates.updateSummary({ ...updates.NO_UPDATES, report: read({ ...STATUS, images: STATUS.images.slice(1) }) }), { title: 'No update found', text: '2 components couldn’t be compared; see below.' });
  // A failed read keeps the last answer, says so, and when it was read.
  const stale = text(dialog(STATUS, false, { failure: new api.ApiFailure(503, 'This dashboard can’t handle requests right now.'), readAt: Date.now() - 2 * 60_000 }));
  assert.match(stale, /Couldn’t refresh this status\. This dashboard can’t handle requests right now\. What’s shown was read 2 min ago\./u);
  assert.match(stale, /Rack B/u);
  const never = text(dialog(null, false, { failure: new api.ApiFailure(503, 'This dashboard can’t handle requests right now.') }));
  assert.match(never, /Couldn’t read the update status This dashboard can’t handle requests right now\./u);
});

await check('feed: one read at start, none on remounts or extra watchers within the gap, then by state; hidden or rejected pages wait; leaving cancels', async () => {
  const t = timeline();
  const { src, log, state } = stand();
  const feed = new updates.UpdateFeed(t.timers);
  let release = feed.watch(src);
  assert.deepEqual(t.due(), [0], 'the first read is due at once');
  await t.advance(0);
  assert.equal(log.read.length, 1);
  assert.equal(feed.current().report.state, 'ok');
  assert.deepEqual(t.due(), [updates.UPDATE_IDLE_MS], 'then every five minutes while nothing runs');
  // A node selection remounts the shell: it leaves and returns in the same turn, so the feed, timer and answer stay.
  release();
  release();
  release = feed.watch(src);
  const second = feed.watch(src);
  await t.advance(0);
  assert.equal(log.read.length, 1, 'no read on a remount or for a second watcher');
  assert.deepEqual(t.due(), [updates.UPDATE_IDLE_MS]);
  second();
  await t.advance(updates.UPDATE_IDLE_MS);
  assert.equal(log.read.length, 2);
  state.read = () => ({ ...BASE, state: 'checking' });
  await t.advance(updates.UPDATE_IDLE_MS);
  assert.deepEqual([log.read.length, t.due()], [3, [updates.UPDATE_MIN_GAP_MS]], 'a minute while a check runs');
  state.read = () => ({ ...BASE, nextCheckAt: t.now + 2 * 60_000 });
  await t.advance(updates.UPDATE_MIN_GAP_MS);
  assert.deepEqual([log.read.length, t.due()], [4, [3 * 60_000]], 'a minute after the next scheduled check');
  assert.equal(updates.nextReadDelay(read({ ...BASE, nextCheckAt: 10 * HOUR }), 10 * HOUR - 10_000), 70_000, 'a minute after a check due in ten seconds');
  assert.equal(updates.nextReadDelay(read({ ...BASE, nextCheckAt: 10 * HOUR }), 5 * HOUR), updates.UPDATE_IDLE_MS, 'never later than five minutes');
  assert.equal(updates.nextReadDelay(read({ ...BASE, nextCheckAt: 10 * HOUR }), 11 * HOUR), updates.UPDATE_IDLE_MS, 'a past next check doesn’t speed reads up');
  assert.equal(updates.nextReadDelay(read({ ...BASE, enabled: false, nextCheckAt: 10 * HOUR }), 10 * HOUR - 10_000), updates.UPDATE_IDLE_MS);
  // Hidden page or rejected sign-in: the due read is skipped, and happens once the page is visible again.
  state.read = () => BASE;
  const before = log.read.length;
  state.hidden = true;
  await t.advance(t.due()[0]);
  assert.equal(log.read.length, before, 'nothing asked while paused');
  feed.wake();
  await t.advance(0);
  assert.equal(log.read.length, before, 'still paused');
  state.hidden = false;
  feed.wake();
  await t.advance(0);
  assert.equal(log.read.length, before + 1, 'the skipped read happens when the page is visible');
  feed.wake();
  await t.advance(0);
  assert.equal(log.read.length, before + 1, 'waking only repeats a skipped read');
  // Leaving for good cancels the read in flight and every timer; a cancelled read isn't a failure.
  state.read = hang;
  await t.advance(t.due()[0]);
  const inFlight = log.read.at(-1);
  assert.equal(inFlight.aborted, false);
  release();
  await t.advance(0);
  assert.equal(inFlight.aborted, true, 'cancelled');
  assert.deepEqual(t.due(), [], 'no timer left');
  assert.equal(feed.current().failure, null);
  // Coming back still keeps a minute after the last request.
  state.read = () => BASE;
  release = feed.watch(src);
  assert.deepEqual(t.due(), [updates.UPDATE_MIN_GAP_MS]);
  await t.advance(updates.UPDATE_MIN_GAP_MS);
  assert.equal(log.read.length, before + 3);
  release();
  await t.advance(0);
  assert.deepEqual(t.due(), []);
});

await check('feed: a failed read keeps the last answer and its time; an unreadable answer is a failure; 404 stops asking', async () => {
  const t = timeline();
  const { src, log, state } = stand();
  const feed = new updates.UpdateFeed(t.timers);
  state.read = () => STATUS;
  const release = feed.watch(src);
  await t.advance(0);
  const { report, readAt } = feed.current();
  assert.equal(report.rows.length, 5);
  state.read = () => { throw new api.ApiFailure(503, 'This dashboard can’t handle requests right now.'); };
  await t.advance(updates.UPDATE_IDLE_MS);
  assert.deepEqual([feed.current().report, feed.current().readAt, feed.current().failure?.status], [report, readAt, 503]);
  state.read = () => ({ enabled: 'maybe' });
  await t.advance(updates.UPDATE_IDLE_MS);
  assert.equal(feed.current().report, report);
  assert.deepEqual([feed.current().failure?.status, feed.current().failure?.message], [200, 'This dashboard’s server sent an update status this page couldn’t read.']);
  state.read = () => STATUS;
  await t.advance(updates.UPDATE_IDLE_MS);
  assert.equal(feed.current().failure, null, 'the next good answer clears it');
  state.read = () => { throw new api.ApiFailure(404, 'This dashboard doesn’t have that item, or it was just removed.'); };
  await t.advance(t.due()[0]);
  const reads = log.read.length;
  assert.deepEqual([feed.current().unsupported, feed.current().failure, t.due()], [true, null, []], 'an older server: nothing more is asked');
  assert.equal(updates.updateSummary(feed.current()).title, 'Updates aren’t reported here', 'an open details dialog says so instead of waiting');
  await t.advance(updates.UPDATE_IDLE_MS * 3);
  const again = feed.watch(src);
  await t.advance(updates.UPDATE_IDLE_MS);
  await feed.checkNow();
  assert.deepEqual([log.read.length, log.check.length], [reads, 0]);
  again();
  release();
  await t.advance(0);
});

await check('manual request: sent once however often it’s pressed, never re-sent after a refusal, its answer beats an older read, cancelled on leaving', async () => {
  const t = timeline();
  const { src, log, state } = stand();
  const feed = new updates.UpdateFeed(t.timers);
  await feed.checkNow();
  assert.equal(log.check.length, 0, 'nothing is sent without a watcher');
  const release = feed.watch(src);
  await t.advance(0);
  const gate = deferred();
  state.check = () => gate.promise;
  const first = feed.checkNow();
  const second = feed.checkNow();
  await flush();
  assert.deepEqual([log.check.length, feed.current().sending, feed.current().checkFailure], [1, true, null], 'pressed twice, sent once');
  gate.resolve({ ...BASE, state: 'checking' });
  await Promise.all([first, second]);
  assert.deepEqual([feed.current().sending, feed.current().report.state], [false, 'checking']);
  assert.deepEqual(t.due(), [updates.UPDATE_MIN_GAP_MS], 'the result is read again after a minute, never sooner');
  // A read already on its way when the check is answered is older: it doesn't replace the answer.
  const slow = deferred();
  state.read = () => slow.promise;
  await t.advance(updates.UPDATE_MIN_GAP_MS);
  state.check = () => ({ ...BASE, state: 'checking', intervalHours: 12 });
  await feed.checkNow();
  slow.resolve({ ...BASE, state: 'ok' });
  await t.advance(0);
  assert.deepEqual([feed.current().report.state, feed.current().report.intervalHours], ['checking', 12]);
  assert.deepEqual(t.due(), [updates.UPDATE_MIN_GAP_MS]);
  // A refusal is shown once and never sent again on its own; reads go on.
  state.read = () => BASE;
  state.check = () => { throw new api.ApiFailure(429, 'This dashboard is limiting attempts right now. Wait a moment and try again.'); };
  await feed.checkNow();
  const sent = log.check.length;
  const reads = log.read.length;
  assert.equal(feed.current().checkFailure?.status, 429);
  await t.advance(updates.UPDATE_IDLE_MS * 4);
  assert.equal(log.check.length, sent, 'not re-sent');
  assert.ok(log.read.length > reads, 'reads continue');
  state.check = () => ({ ...BASE, state: 'checking' });
  await feed.checkNow();
  assert.equal(feed.current().checkFailure, null, 'the next attempt clears the old refusal');
  assert.equal(updates.checkProblem(new api.ApiFailure(409, 'x')), 'A check is already running. Its result appears here when it finishes.');
  assert.equal(updates.checkProblem(new api.ApiFailure(409, 'Request refused', 'Update checks are disabled in deployment settings')), 'Update checks are off in this dashboard’s deployment settings.');
  assert.equal(updates.checkProblem(new api.ApiFailure(429, 'x')), 'A check ran recently. Manual checks are limited to one every 15 minutes; scheduled checks still run.');
  assert.equal(updates.checkProblem(new api.ApiFailure(403, 'x')), 'Only administrators of this dashboard can start a check.');
  assert.equal(updates.checkProblem(new api.ApiFailure(404, 'x')), 'This dashboard’s server doesn’t offer update checks.');
  const other = new api.ApiFailure(503, 'This dashboard can’t handle requests right now.');
  assert.equal(updates.checkProblem(other), api.describeFailure(other));
  // Leaving cancels a check in flight; nothing reports it as failed.
  state.check = hang;
  const pending = feed.checkNow();
  await flush();
  release();
  await t.advance(0);
  await pending;
  assert.equal(log.check.at(-1).aborted, true);
  assert.deepEqual([feed.current().sending, feed.current().checkFailure, t.due()], [false, null, []]);
});

await check('API: both routes go to the dashboard itself with the sign-in and never with a node selection', async () => {
  on();
  try {
    const client = new api.CanaryApi(PAGE, TOKEN).withRegisteredNode(REG);
    assert.equal(typeof client.updates, 'function', 'CanaryApi.updates(signal?) through own() (root adds it)');
    assert.equal(typeof client.checkUpdates, 'function', 'CanaryApi.checkUpdates(signal?) through own() (root adds it)');
    let answer;
    const reading = await record(async () => { answer = await client.updates(); });
    assert.equal(reading.length, 1);
    assert.deepEqual([reading[0].url, reading[0].init.method ?? 'GET', header(reading[0], 'authorization'), header(reading[0], HEADER)], [`${PAGE}/api/dashboard/updates`, 'GET', `Bearer ${TOKEN}`, null]);
    assert.equal(read(answer).rows.length, 5, 'the answer reads as an update status');
    const asking = await record(() => client.checkUpdates());
    assert.equal(asking.length, 1);
    assert.deepEqual([asking[0].url, asking[0].init.method, header(asking[0], 'authorization'), header(asking[0], HEADER)], [`${PAGE}/api/dashboard/updates/check`, 'POST', `Bearer ${TOKEN}`, null]);
    // One feed per sign-in: node selections share it; another account or session doesn't.
    const plain = new api.CanaryApi(PAGE, TOKEN);
    const key = apiValue => updates.updateFeedKey({ api: apiValue, account: ACCOUNT });
    assert.equal(key(plain), key(client));
    assert.notEqual(updates.updateFeedKey({ api: plain, account: { ...ACCOUNT, id: 'acct-2' } }), key(plain));
    assert.notEqual(key(new api.CanaryApi(PAGE, TOKEN, { credential: 'session', expiresAt: NOW + 1000 })), key(new api.CanaryApi(PAGE, TOKEN, { credential: 'session', expiresAt: NOW + 2000 })));
  } finally {
    off();
  }
});

await check('shell: only a signed-in control plane has the entry; rendering asks nothing; direct and gateway pages get no feed', async () => {
  assert.ok(storeWrapped, 'React was imported before the server-snapshot wrap, so App can’t be rendered');
  const { App } = await load('src/App.tsx');
  const { session } = sessionModule;
  const probe = connection => {
    const out = {};
    const Probe = () => { out.value = view.useUpdates(connection); return null; };
    renderToStaticMarkup(React.createElement(Probe));
    return out.value;
  };
  const fake = { api: { baseUrl: PAGE, credential: 'token', expiresAt: null }, account: ACCOUNT };
  location.hash = '#/nodes';
  try {
    off();
    assert.equal(probe(fake), null, 'direct mode');
    globalThis.__CANARY_DASHBOARD_GATEWAY__ = true;
    assert.equal(probe(fake), null, 'gateway mode without the control plane');
    off();
    on();
    const feedState = probe(fake);
    assert.deepEqual([feedState.key, feedState.snapshot], [updates.updateFeedKey(fake), updates.NO_UPDATES]);
    assert.equal(session.getSnapshot().connection, null);
    const signedOut = await record(async () => { assert.doesNotMatch(renderToStaticMarkup(React.createElement(App)), /Software updates|upd-/u, 'signed out'); });
    assert.equal(signedOut.length, 0);
    await session.connect(new api.CanaryApi(PAGE, TOKEN));
    let markup = '';
    const rendering = await record(async () => { markup = renderToStaticMarkup(React.createElement(App)); });
    assert.equal(rendering.length, 0, 'rendering made a request');
    const entry = buttons(markup).filter(button => button.attrs.get('aria-label') === 'Software updates');
    assert.equal(entry.length, 1, 'one entry in the shell');
    assert.equal(entry[0].attrs.get('aria-haspopup'), 'dialog');
    assert.doesNotMatch(text(markup), /update is available|Updates are available/u, 'no notice before an answer');
    assert.equal(await session.signOut(), 'forgotten');
  } finally {
    if (session.getSnapshot().connection) await session.signOut();
    location.hash = '';
    off();
  }
  const app = readFileSync(resolve(web, 'src/App.tsx'), 'utf8');
  assert.match(app, /const updates = useUpdates\(connection\);/u);
  assert.match(app, /\{updates \? <UpdatesButton updates=\{updates\}/u);
  assert.match(app, /\{updates && connection\.link !== 'rejected' \? <UpdateBanner updates=\{updates\}/u);
  assert.match(app, /\{showingUpdates && updates \? <UpdatesDialog updates=\{updates\} admin=\{ctx\.admin\} skew=\{connection\.issuerSkew \?\? 0\}/u);
});

await check('source: no storage, no requests of its own, no registry or GitHub calls, no install commands; the stylesheet loads only with the feature', () => {
  const helper = readFileSync(resolve(web, 'src/updates.ts'), 'utf8');
  const component = readFileSync(resolve(web, 'src/views/Updates.tsx'), 'utf8');
  for (const [name, source] of [['updates.ts', helper], ['Updates.tsx', component]]) {
    assert.doesNotMatch(source, /localStorage|sessionStorage|indexedDB|document\.cookie|\bfetch\(|XMLHttpRequest|WebSocket|EventSource|sendBeacon|window\.open|dangerouslySetInnerHTML|innerHTML/u, name);
    assert.doesNotMatch(source, /ghcr\.io\/v2|ghcr\.io\/token|api\.github\.com|docker (pull|run|compose)|watchtower/iu, name);
  }
  assert.ok(existsSync(resolve(web, 'src/updates.css')));
  const css = readFileSync(resolve(web, 'src/updates.css'), 'utf8');
  assert.doesNotMatch(css, /text-transform|@import|url\(/u, 'no transformed copy and no outside resources');
  // App.tsx is rendered under Node by several fixtures, so nothing it imports may load a stylesheet statically.
  for (const [name, source] of [['App.tsx', readFileSync(resolve(web, 'src/App.tsx'), 'utf8')], ['Updates.tsx', component], ['updates.ts', helper]]) {
    assert.doesNotMatch(source, /^import\s+(?:[^;]*from\s+)?['"][^'"]+\.css['"]/mu, name);
  }
  assert.match(component, /useEffect\(\(\) => \{\s+if \(!feed\) return;[\s\S]{0,200}void import\('\.\.\/updates\.css'\)/u, 'loaded inside the control-plane effect');
  assert.deepEqual(storageWrites, [], 'nothing was written to browser storage');
});

await check('rebuild provenance: equal source requires both build times and refuses inconsistent or implausible claims', () => {
  for (const [latestBuiltAt, expected] of [[BUILT, 'current'], [REBUILT, 'update-available'], [BUILT - HOUR, 'ahead']]) {
    const row = sameRow({ currentBuiltAt: BUILT, latestBuiltAt, status: expected });
    assert.equal(row.status, expected);
    for (const wrong of ['current', 'update-available', 'ahead'].filter(value => value !== expected))
      assert.equal(sameRow({ currentBuiltAt: BUILT, latestBuiltAt, status: wrong }).status, 'unknown');
  }
  for (const invalid of [undefined, null, 0, -1, NaN, Infinity, '2026-10-25', MAX_DATE_MS + 1]) {
    assert.equal(sameRow({ currentBuiltAt: invalid, latestBuiltAt: REBUILT, status: 'update-available' }).status, 'unknown');
    assert.equal(sameRow({ currentBuiltAt: BUILT, latestBuiltAt: invalid, status: 'update-available' }).status, 'unknown');
  }
  assert.equal(sameRow({ currentBuiltAt: BUILT, latestBuiltAt: CHECKED + updates.BUILD_CLOCK_SLACK_MS + 1, status: 'update-available' }).status, 'unknown');
  assert.equal(sameRow({ currentBuiltAt: BUILT, latestBuiltAt: REBUILT, status: 'update-available' }, { checkedAt: undefined }).status, 'unknown');
});

await check('rebuilt image notices change with build time and show both times without a meaningless same-source compare link', () => {
  const first = sameAnswer({ currentBuiltAt: BUILT, latestBuiltAt: REBUILT, status: 'update-available' });
  const second = sameAnswer({ currentBuiltAt: BUILT, latestBuiltAt: REBUILT + HOUR, status: 'update-available' });
  const notice = updates.updateNotice(read(first));
  assert.ok(notice);
  assert.notEqual(notice.signature, updates.updateNotice(read(second)).signature, 'a dismissed older rebuild would hide a newer rebuild');
  const markup = dialog(first, true);
  const shown = text(markup);
  assert.match(shown, new RegExp(shownBuild(BUILT), 'u'));
  assert.match(shown, new RegExp(shownBuild(REBUILT), 'u'));
  assert.match(shown, /newer build of the same source/u);
  assert.ok(links(markup).every(link => !link.get('href')?.includes('/compare/')), 'same source got a compare link');
});

off();
await scope.unregister();
console.log(failed ? `${failed} FAILED` : 'ALL PASS');
process.exitCode = failed ? 1 : 0;
