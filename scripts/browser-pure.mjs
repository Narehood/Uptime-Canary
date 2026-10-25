// Pure protocol-19 checks for the real-browser monitor kind: apps/web/src/browser.tsx (a mirror of the node's
// browsermonitor.Validate over Go 1.27's url.Parse), screenshot.ts (the private screenshot reader), the CanaryApi
// screenshot method, views/BrowserFields.tsx and views/BrowserScreenshot.tsx (rendered to static markup), checks.tsx, the
// monitor form through its cloneModel and previewModel seams, and the Configuration resolver through its resolverModel
// seam. Imports the source in place through one tsx namespace: no copy, scratch, network, browser or output files; fetch
// is a stand-in that records each request and answers only inside the API check. The expected admissions come from Go's
// net/url and net/netip and the node's rule as read in source (.canary/tools/go/src, internal/browsermonitor/config.go);
// root's contract harness sends the same kinds of cases to a real node. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/browser-pure.mjs   (exit 1 on any failure)
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
// Stand-ins installed before any source loads: storage writes and requests are recorded; only the API check answers.
const touched = [];
const recorder = name => (...args) => { touched.push([name, ...args.map(String)]); };
for (const name of ['localStorage', 'sessionStorage']) {
  Object.defineProperty(globalThis, name, { value: { getItem: () => null, setItem: recorder(`${name}.setItem`), removeItem: recorder(`${name}.removeItem`), clear: recorder(`${name}.clear`) }, configurable: true, writable: true });
}
let answer = null;
const requests = [];
globalThis.fetch = async (url, init = {}) => { requests.push({ url: String(url), init }); if (!answer) throw new Error('no network in this fixture'); return answer(String(url), init); };
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'browser-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const b = await load('browser.tsx');
const shots = await load('screenshot.ts');
const api = await load('api.ts');
const checks = await load('checks.tsx');
const fmt = await load('format.ts');
const { cloneModel, previewModel } = await load('views/MonitorForm.tsx');
const { resolverModel, withOtherFields } = await load('views/Config.tsx');
const { BrowserFields } = await load('views/BrowserFields.tsx');
const { ScreenshotFigure, BrowserScreenshotCard, screenshotGeneration } = await load('views/BrowserScreenshot.tsx');
const { CanaryContext } = await load('context.tsx');
requests.length = 0;
touched.length = 0;

let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const ch = code => String.fromCodePoint(code);
const MASK = ch(0x2022).repeat(3);
const LONE = String.fromCharCode(0xd800);
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const html = node => renderToStaticMarkup(React.createElement(React.Fragment, null, node));
/** Every <input> in rendered markup, by its attributes in any order: React 19 writes `value` last. */
const inputs = markup => [...markup.matchAll(/<input\b([^>]*?)\/?>/gu)].map(([, attrs]) => Object.fromEntries([...attrs.matchAll(/([^\s=/]+)(?:="([^"]*)")?/gu)].map(([, name, value = '']) => [name.toLowerCase(), value])));

const EARLIER = ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp', 'globalping', 'steam', 'system-service', 'pm2', 'tailscale-ping', 'postgres', 'mysql', 'sqlserver', 'oracledb',
  'mongodb', 'redis', 'docker', 'mqtt', 'rabbitmq', 'kafka-producer', 'grpc', 'sftp', 'ntp', 'snmp', 'radius', 'sip-options', 'push', 'manual'];
const OPTION_KEYS = ['http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2', 'expectedStatusCodes'];
const ENDPOINT = 'wss://chromium.review.test:9222/devtools/browser/review?token=browser-query-secret';
const BEARER = 'browser-bearer-secret';
const STORED = { endpoint: ENDPOINT, bearerToken: BEARER, screenshot: true, delayMs: 250 };
const stored = (extra = {}) => ({
  id: 'm-rb', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', name: 'Checkout page', type: 'real-browser', target: 'https://shop.example.test/checkout',
  intervalMs: 60_000, timeoutMs: 15_000, nodeIds: ['a'], enabled: true, retries: 1, browser: structuredClone(STORED), futureTop: { kept: [1, null] }, ...extra,
});

// Protocol 19 also explicitly adds GameDig right after Steam among game servers (validation.go: a `gamedig` options
// object, the target inside it), so the count moves from 31 to 32; it is filtered out of the earlier order too.
await check('catalogue: 32 unique types, the 30 earlier ones in their order, the real browser right after HTTP among web checks, plus GameDig', () => {
  const types = checks.CHECK_TYPES.map(entry => entry.type);
  assert.equal(types.length, 32);
  assert.equal(new Set(types).size, 32);
  assert.deepEqual(types.filter(type => type !== 'real-browser' && type !== 'gamedig'), EARLIER);
  assert.equal(types.indexOf('real-browser'), types.indexOf('http') + 1);
  const entry = checks.checkType('real-browser');
  assert.equal(entry.label, 'Real browser');
  assert.equal(entry.short, 'browser');
  assert.equal(entry.category, 'web');
  assert.equal(entry.targetLabel, 'Page URL');
  assert.match(entry.targetHint, /^An http:\/\/ or https:\/\/ address without credentials or #fragment\./u);
  assert.deepEqual(checks.CHECK_TYPES.filter(other => other.category === 'local').map(other => other.type), ['system-service', 'pm2', 'tailscale-ping'], 'not among the local manager clients');
  assert.equal(checks.typeLabel('real-browser'), 'Real browser');
  assert.deepEqual([...checks.NETWORK_TYPES], ['http', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp']);
  assert.deepEqual([...checks.TYPED_TARGET_TYPES], [...checks.NETWORK_TYPES, 'system-service', 'tailscale-ping', 'real-browser']);
  assert.equal(checks.isServiceType('real-browser'), false);
  assert.equal(checks.tlsApplies('real-browser', 'https://shop.example.test/', undefined), false, 'the node refuses TLS options on a browser monitor');
});

// browsermonitor.Validate: url.Parse succeeds, at most 2048 bytes, a host name, no user information, no fragment text,
// no CR/LF/NUL, scheme ws/wss/http/https, and for http/https a path of "" or "/" with no query. Go 1.27 url.Parse:
// control bytes refused; the scheme lowercased; "?" alone at the end is ForceQuery; IPv6 literals through netip.ParseAddr
// (not IPv4, %25 zones non-empty); http/https hosts with extra colons refused (urlstrictcolons=1 for go 1.27.3), other
// schemes take the last colon; ports are digits and may be empty or out of range; hosts escape only bytes from 0x80 and
// %25; paths may escape anything well-formed; a bare trailing "#" leaves no fragment. The node's JSON reading refuses
// an unpaired surrogate anywhere in the body.
const ACCEPTED = [
  'ws://127.0.0.1:9222/devtools/browser/0f4c6a2e-1b3d-4c5e-8f9a-0123456789ab', 'wss://chromium.review.test/devtools/browser/x?token=abc', 'WS://Chromium.Review.test:9222/x',
  'Https://chromium.review.test', 'http://127.0.0.1:9222', 'http://127.0.0.1:9222/', 'http://127.0.0.1:9222/?', 'https://chromium.review.test?', 'ws://127.0.0.1:9222/x#',
  'ws://[::1]:9222/x', 'ws://[::1]/x', 'ws://[fe80::1%25eth0]:9222/x', 'ws://[fe80::1%25eth%300]/', 'ws://[::ffff:127.0.0.1]:9222/', 'ws://127.0.0.1:/x', 'ws://127.0.0.1:99999/x',
  'ws://a:1:2/x', 'ws://h/a b', `ws://b${ch(0xfc)}cher.review.test/x`, 'ws://%C3%BC.review.test/', 'ws://h/%41', 'ws://h?x/y', 'ws://a]b/', 'ws://a%25b/', 'ws://a!b/',
  'ws://h', 'ws://h:9222', 'ws://h/{x}', `ws://${'a'.repeat(2042)}/`, `ws://h/${ch(0xe9).repeat(1020)}`,
];
const REFUSED = [
  ['', 'Enter the browser service’s DevTools address'],
  [`ws://${'a'.repeat(2043)}/`, 'Use at most 2,048 bytes'], [`ws://h/${ch(0xe9).repeat(1021)}`, 'Use at most 2,048 bytes'],
  [`ws://h/${LONE}`, 'unpaired surrogate'],
  ['ws://h/\n', 'control characters'], ['ws://h/\t', 'control characters'], ['ws://h\x7f/', 'control characters'], [`ws://h/${ch(0)}`, 'control characters'],
  ['ws://127.0.0.1:9222/x#frag', 'Remove the #fragment'], ['ws://h/#%zz', 'Remove the #fragment'], ['ws://h/#\n', 'Remove the #fragment'],
  [' ws://127.0.0.1:9222/', 'Start with ws:// or wss://'], ['ftp://127.0.0.1/', 'Start with ws:// or wss://'], ['9ws://h/', 'Start with ws:// or wss://'], ['://h', 'Start with ws:// or wss://'],
  ['chromium.review.test:9222', 'Start with ws:// or wss://'],
  ['ws:127.0.0.1:9222', 'Write a complete address with //'], ['ws:/127.0.0.1/', 'Write a complete address with //'],
  ['ws:///x', 'Enter the host name or address after //'], ['ws://:9222/x', 'Enter the host name or address after //'], ['ws://', 'Enter the host name or address after //'],
  ['ws://user:pw@127.0.0.1:9222/x', 'Remove user:password@'], ['ws://@127.0.0.1:9222/x', 'Remove user:password@'], ['ws://u@p@h/', 'Remove user:password@'],
  ['http://127.0.0.1:9222/json/version', 'remove the path and the query'], ['http://127.0.0.1:9222/?x=1', 'remove the path and the query'], ['http://127.0.0.1:9222/??', 'remove the path and the query'],
  ['https://h/x', 'remove the path and the query'], ['http://h?a', 'remove the path and the query'], ['http://h/%2F', 'remove the path and the query'],
  ['http://a:1:2/', 'a port is digits only'], ['ws://127.0.0.1:x/', 'a port is digits only'], ['ws://[::1]x/', 'a port is digits only'],
  ['ws://[127.0.0.1]:9222/', 'Put an IPv6 address in brackets'], ['ws://[::1/', 'Put an IPv6 address in brackets'], ['ws://a[b]/', 'Put an IPv6 address in brackets'],
  ['ws://[fe80::1%25]:9222/', 'Put an IPv6 address in brackets'], ['ws://[fe80::1%eth0]:9222/', 'Put an IPv6 address in brackets'], ['ws://[fe80::1%25eth%2F0]/', 'Put an IPv6 address in brackets'],
  ['ws://[::g]/', 'Put an IPv6 address in brackets'], ['ws://[%3A%3A1]/', 'Put an IPv6 address in brackets'],
  ['ws://a b/', 'Check the host name'], ['ws://a%41/', 'Check the host name'], ['ws://h\\x/', 'Check the host name'], ['ws://h^/', 'Check the host name'], ['ws://h%zz/', 'Check the host name'],
  ['ws://h/%zz', 'Fix the %-escapes in the path'], ['ws://h/%4', 'Fix the %-escapes in the path'],
];

await check('endpoint mirror: everything the node accepts, scheme case, discovery origins, IPv6 literals and zones, odd ports and hosts as Go parses them', () => {
  for (const endpoint of ACCEPTED) assert.equal(b.browserEndpointProblem(endpoint), undefined, JSON.stringify(endpoint));
  assert.equal(new TextEncoder().encode(ACCEPTED.at(-1)).length, 2047);
  assert.equal(ACCEPTED.at(-2).length, 2048);
});

await check('endpoint mirror: what the node refuses, each with the reason that names the problem', () => {
  for (const [endpoint, said] of REFUSED) {
    const problem = b.browserEndpointProblem(endpoint);
    assert.ok(problem, `${JSON.stringify(endpoint)} is accepted`);
    assert.ok(problem.includes(said), `${JSON.stringify(endpoint)}: ${problem}`);
  }
});

await check('token and wait limits: printable ASCII up to 4,096; whole milliseconds 0 to 30,000 and below the timeout', () => {
  for (const token of ['', 'abc', '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~', 'a'.repeat(4096), 'eyJhbGciOi.J9-_x']) assert.equal(b.browserBearerProblem(token), undefined, token.slice(0, 20));
  for (const [token, said] of [
    ['a'.repeat(4097), 'at most 4,096'], ['a b', 'no spaces'], [' a', 'no spaces'], ['a ', 'no spaces'], ['Bearer abc', 'without “Bearer ”'], [`a${ch(0xa0)}`, 'no spaces'],
    ['a\t', 'control characters'], ['a\n', 'control characters'], ['a\r', 'control characters'], [`a${ch(0)}`, 'control characters'], ['a\x7f', 'control characters'],
    [ch(0xe9), 'printable ASCII only'], [`a${LONE}`, 'printable ASCII only'],
  ]) assert.ok(b.browserBearerProblem(token)?.includes(said), `${JSON.stringify(token.slice(0, 20))}: ${b.browserBearerProblem(token)}`);
  for (const [delay, timeout] of [['', 1000], ['0', 25], ['1', 1000], ['999', 1000], ['30000', 30001], ['30000', null], ['  250 ', 1000], ['007', 1000]]) assert.equal(b.browserDelayProblem(delay, timeout), undefined, `${delay} / ${timeout}`);
  for (const [delay, timeout, said] of [
    ['1000', 1000, 'less than the timeout (1 s)'], ['30000', 30000, 'less than the timeout'], ['30001', 120000, 'from 0 to 30,000'], ['-1', 1000, 'whole number'], ['1.5', 1000, 'whole number'],
    ['1e3', 5000, 'whole number'], ['abc', 1000, 'whole number'], ['99999999999999999999', null, 'from 0 to 30,000'],
  ]) assert.ok(b.browserDelayProblem(delay, timeout)?.includes(said), `${delay} / ${timeout}: ${b.browserDelayProblem(delay, timeout)}`);
  assert.equal(b.BROWSER_ENDPOINT_MAX, 2048); assert.equal(b.BROWSER_BEARER_MAX, 4096); assert.equal(b.BROWSER_DELAY_MAX, 30_000);
});

await check('builder: a new monitor sends exactly the endpoint and the settings that are on, trimmed, in Go’s order; zero and empty are omitted', () => {
  let errors = {};
  const plain = b.buildBrowser(undefined, { endpoint: '  ws://127.0.0.1:9222/x  ', bearer: '', screenshot: false, delay: '' }, 15_000, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(plain.value), JSON.stringify({ endpoint: 'ws://127.0.0.1:9222/x' }));
  errors = {};
  const full = b.buildBrowser(undefined, { endpoint: ENDPOINT, bearer: ` ${BEARER} `, screenshot: true, delay: ' 250 ' }, 15_000, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(full.value), JSON.stringify(STORED));
  assert.equal('delayMs' in b.buildBrowser(undefined, { endpoint: ENDPOINT, bearer: '', screenshot: false, delay: '0' }, 1000, {}).value, false, 'a zero wait is omitted');
  errors = {};
  const bad = b.buildBrowser(undefined, { endpoint: 'http://127.0.0.1:9222/json/version', bearer: 'a b', screenshot: false, delay: '2000' }, 1000, errors);
  assert.deepEqual(Object.keys(errors).sort(), ['browserBearer', 'browserDelay', 'browserEndpoint']);
  assert.equal(bad.value.endpoint, 'http://127.0.0.1:9222/json/version', 'the refused value is what would have been sent');
  errors = {};
  b.buildBrowser(undefined, { endpoint: ENDPOINT, bearer: '', screenshot: false, delay: '2000' }, null, errors);
  assert.deepEqual(errors, {}, 'no timeout to compare with: the timeout field reports its own problem');
  assert.deepEqual(b.BROWSER_FIELD_LABEL, { browserEndpoint: 'Browser endpoint', browserBearer: 'Bearer token', browserDelay: 'Wait after load' });
});

await check('builder: unedited stored options go back byte for byte, key order, odd endpoint bytes and a newer node’s keys included', () => {
  const variants = [
    STORED,
    { delayMs: 250, screenshot: true, bearerToken: BEARER, endpoint: ENDPOINT },
    { endpoint: 'ws://h:9222/x ', futureMode: { nested: [1, null] } },
    { endpoint: 'http://127.0.0.1:9222/?' },
    { endpoint: ENDPOINT, bearerToken: '' },
  ];
  for (const stored of variants) {
    const before = JSON.stringify(stored);
    const draft = b.browserDraftFrom(stored);
    const errors = {};
    const built = b.buildBrowser(stored, draft, 15_000, errors);
    assert.deepEqual(errors, {}, before);
    assert.equal(JSON.stringify(built.value), before.replace(',"bearerToken":""', ''), before);
    assert.equal(JSON.stringify(stored), before, 'stored object not mutated');
  }
  const odd = { endpoint: 'ws://h:9222/x ', bearerToken: BEARER, screenshot: true, delayMs: 250 };
  const draft = b.browserDraftFrom(odd);
  assert.deepEqual(draft, { endpoint: 'ws://h:9222/x ', bearer: BEARER, screenshot: true, delay: '250' });
  assert.equal(b.buildBrowser(odd, { ...draft, delay: '500' }, 15_000, {}).value.endpoint, 'ws://h:9222/x ', 'an edit beside an unedited endpoint keeps its bytes');
  assert.equal(JSON.stringify(b.buildBrowser(odd, { ...draft, bearer: '', screenshot: false, delay: '' }, 15_000, {}).value), JSON.stringify({ endpoint: 'ws://h:9222/x ' }), 'cleared settings are removed, never sent empty');
  assert.equal(b.buildBrowser(odd, { ...draft, endpoint: ' ws://h:9222/y ' }, 15_000, {}).value.endpoint, 'ws://h:9222/y', 'a typed endpoint is trimmed');
  assert.deepEqual(b.browserDraftFrom(undefined), { endpoint: '', bearer: '', screenshot: false, delay: '' });
});

await check('generation comparison: the endpoint, token, screenshot setting and wait count, through Go’s omitempty encoding; metadata does not', () => {
  const input = (extra = {}) => checks.inputOf(stored(extra));
  assert.deepEqual(checks.checkChanges(input(), input({ name: 'Checkout 2', intervalMs: 30_000, retryIntervalMs: 5_000, tags: [{ tagId: 't' }], groupId: 'g', notificationIds: ['c'] })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ browser: { delayMs: 250, screenshot: true, bearerToken: BEARER, endpoint: ENDPOINT } })), [], 'key order');
  assert.deepEqual(checks.checkChanges(input({ browser: { endpoint: ENDPOINT } }), input({ browser: { endpoint: ENDPOINT, bearerToken: '', screenshot: false, delayMs: 0 } })), [], 'empty, false and zero equal absent');
  for (const browser of [{ ...STORED, endpoint: `${ENDPOINT}x` }, { ...STORED, bearerToken: 'other' }, { ...STORED, screenshot: false }, { ...STORED, delayMs: 251 }, { ...STORED, futureMode: 1 }]) {
    assert.deepEqual(checks.checkChanges(input(), input({ browser })), ['browser'], JSON.stringify(browser));
  }
  assert.deepEqual(checks.checkChanges(input(), input({ target: 'https://shop.example.test/cart' })), ['target']);
  assert.deepEqual(checks.checkChangeWords(['target', 'browser'], 'real-browser'), ['page URL', 'browser service, token, wait or screenshot setting']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'http'), ['target']);
});

await check('form: a new real-browser monitor sends a trimmed page URL and only its browser options', () => {
  const draft = { ...cloneModel.draftFrom(undefined, ['a']), name: 'Checkout', type: 'real-browser', targets: { 'real-browser': '  https://shop.example.test/checkout  ' }, browser: { endpoint: ' ws://127.0.0.1:9222/x ', bearer: '', screenshot: true, delay: '' } };
  const built = cloneModel.build(draft, undefined, undefined);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(built.input.target, 'https://shop.example.test/checkout');
  assert.equal(JSON.stringify(built.input.browser), JSON.stringify({ endpoint: 'ws://127.0.0.1:9222/x', screenshot: true }));
  for (const key of OPTION_KEYS) assert.equal(key in built.input, false, key);
  for (const [target, said] of [['ftp://shop.example.test/', 'Use a http:// or https:// address'], ['https://user:pw@shop.example.test/', 'Remove credentials'], ['https://shop.example.test/#top', 'Remove the #fragment'], [`https://shop.example.test/${LONE}`, 'unpaired surrogate'], ['', 'Enter the page URL.']]) {
    const refused = cloneModel.build({ ...draft, targets: { 'real-browser': target } }, undefined, undefined);
    assert.equal(refused.input, undefined, target);
    assert.ok(refused.errors.target?.includes(said), `${JSON.stringify(target)}: ${refused.errors.target}`);
  }
  const noEndpoint = cloneModel.build({ ...draft, browser: { ...draft.browser, endpoint: '' } }, undefined, undefined);
  assert.equal(noEndpoint.input, undefined);
  assert.match(noEndpoint.errors.browserEndpoint, /^Enter the browser service’s DevTools address/u);
});

await check('form: an edit loads the stored settings, a rename keeps their bytes and the generation, and the wait must stay below the timeout', () => {
  const m = stored({ browser: { delayMs: 250, screenshot: true, bearerToken: BEARER, endpoint: 'ws://h:9222/x ', futureMode: 'later' } });
  const draft = cloneModel.draftFrom(m, ['a']);
  assert.deepEqual(draft.targets, { 'real-browser': m.target });
  assert.deepEqual(draft.browser, { endpoint: 'ws://h:9222/x ', bearer: BEARER, screenshot: true, delay: '250' });
  const initial = structuredClone(draft);
  const renamed = cloneModel.build({ ...draft, name: 'Checkout renamed' }, m, initial);
  assert.ok(renamed.input, JSON.stringify(renamed.errors));
  assert.equal(JSON.stringify(renamed.input.browser), JSON.stringify(m.browser), 'stored bytes, order and a newer node’s key');
  assert.deepEqual(renamed.input.futureTop, m.futureTop);
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', ...OPTION_KEYS]) assert.equal(key in renamed.input, false, key);
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), renamed.input), [], 'a rename keeps the check generation');
  const waited = cloneModel.build({ ...draft, browser: { ...draft.browser, delay: '500' } }, m, initial);
  assert.equal(JSON.stringify(waited.input.browser), JSON.stringify({ ...m.browser, delayMs: 500 }));
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), waited.input), ['browser']);
  const short = cloneModel.build({ ...draft, timeout: { amount: '200', unit: 'ms' } }, m, initial);
  assert.equal(short.input, undefined);
  assert.match(short.errors.browserDelay, /less than the timeout \(200 ms\)/u);
});

await check('form: switching an HTTP monitor to a real browser drops HTTP, TLS and status options; a browser monitor never carries them', () => {
  const http = { ...stored(), type: 'http', target: 'https://shop.example.test/', expectedStatusCodes: [200], http: { method: 'POST', body: 'x' }, tls: { minValidityDays: 3 } };
  delete http.browser;
  const draft = cloneModel.draftFrom(http, ['a']);
  const switched = { ...draft, type: 'real-browser', targets: { ...draft.targets, 'real-browser': 'https://shop.example.test/' }, browser: { endpoint: 'ws://127.0.0.1:9222/x', bearer: '', screenshot: false, delay: '' } };
  const built = cloneModel.build(switched, http, cloneModel.draftFrom(http, ['a']));
  assert.ok(built.input, JSON.stringify(built.errors));
  for (const key of OPTION_KEYS) assert.equal(key in built.input, false, key);
  assert.deepEqual(built.input.browser, { endpoint: 'ws://127.0.0.1:9222/x' });
  const back = cloneModel.build({ ...switched, type: 'http' }, http, cloneModel.draftFrom(http, ['a']));
  assert.equal('browser' in (back.input ?? {}), false, 'an HTTP monitor never sends browser options');
});

await check('preview: the whole endpoint and the token are masked until shown; a masked copy is caught as a placeholder; the wait and screenshot stay', () => {
  const m = stored({ browser: { ...STORED, futureSecret: 'future-private', futureMode: 'fast' } });
  const draft = cloneModel.draftFrom(m, ['a']);
  const built = cloneModel.build(draft, m, structuredClone(draft));
  const masked = previewModel.masked(built.input);
  assert.deepEqual(masked.browser, { endpoint: MASK, bearerToken: MASK, screenshot: true, delayMs: 250, futureSecret: MASK, futureMode: 'fast' });
  const text = JSON.stringify(masked);
  for (const value of ['chromium.review.test', 'browser-query-secret', BEARER, 'future-private']) assert.equal(text.includes(value), false, value);
  assert.equal(fmt.maskedPath(JSON.parse(text)), 'browser.endpoint', 'an import of the masked copy is refused at the endpoint');
  assert.equal(JSON.stringify(built.input.browser), JSON.stringify(m.browser), 'the input itself is untouched');
  assert.deepEqual(b.browserMasked({ endpoint: ENDPOINT, screenshot: true }), { endpoint: MASK, screenshot: true }, 'no token key appears');
  assert.equal(b.browserMasked(undefined), undefined);
  assert.ok(source('views/MonitorForm.tsx').includes("{input?.browser ? ' The whole browser endpoint and its bearer token are masked too until you show credentials.' : ''}"));
});

const factsOf = (m, opts) => Object.fromEntries(checks.checkFacts(m, opts).map(fact => [fact.label, html(fact.value)]));
await check('facts: editors see what loads where, the hidden endpoint and token until revealed, the wait, the screenshot rule and the three outcomes; viewers see no options', () => {
  const m = stored();
  const masked = factsOf(m, { viewer: false, reveal: false });
  for (const value of Object.values(masked)) for (const secret of ['chromium.review.test', 'browser-query-secret', BEARER]) assert.equal(value.includes(secret), false, secret);
  assert.ok(masked['Loads in'].includes('A dedicated Chromium browser service') && masked['Loads in'].includes('https://shop.example.test/checkout') && masked['Loads in'].includes('Downloads are denied'));
  assert.ok(masked['Browser service'].includes('(set · hidden)') && masked['Browser service'].includes('connects to this DevTools WebSocket URL'));
  assert.equal(masked['Browser service'].includes('unencrypted'), false, 'wss:// is encrypted');
  assert.ok(masked['Bearer token'].includes('set · hidden') && masked['Bearer token'].includes('sent only to the browser service, never to the page'));
  assert.ok(masked['Wait after load'].includes('250 ms once the page is network-idle'));
  assert.ok(masked.Screenshot.includes('1024×576 PNG') && masked.Screenshot.includes('never replicated') && masked.Screenshot.includes('makes the check pending'));
  assert.ok(masked['Up when'].includes('2xx or 3xx') && masked['Up when'].includes('then the wait ends and the screenshot is taken'));
  assert.ok(masked['Down when'].includes('navigation failed') && masked['Down when'].includes('starts a download'));
  for (const said of ['can’t be reached', 'refuses the connection or the bearer token', 'another host, port or scheme', 'two browser checks are already running', 'isn’t http:// or https://', 'larger than 192 KiB', 'doesn’t confirm closing the private context', 'the check is cancelled', 'within the timeout', 'Pending is never inverted and never recovers an outage.']) {
    assert.ok(masked['Pending when'].includes(said), said);
  }
  assert.ok(masked.Latency.includes('Page-load time') && masked.Latency.includes('including the wait') && masked.Latency.includes('Not a single network round trip'));
  for (const said of Object.values(b.BROWSER_SUMMARY)) assert.ok(masked.Results.includes(said), said);
  assert.deepEqual(b.BROWSER_SUMMARY, { up: 'Browser page loaded', down: 'Browser navigation failed', pending: 'Browser service unavailable' });
  const revealed = factsOf(m, { viewer: false, reveal: true });
  assert.ok(revealed['Browser service'].includes(ENDPOINT) && revealed['Bearer token'].includes(BEARER));
  const plain = factsOf(stored({ browser: { endpoint: 'ws://127.0.0.1:9222/x', bearerToken: BEARER } }), { viewer: false, reveal: false });
  assert.ok(plain['Browser service'].includes('unencrypted, so the token travels in clear'));
  assert.equal(plain['Wait after load'].includes('None') && plain.Screenshot.includes('Off'), true);
  const discovery = factsOf(stored({ browser: { endpoint: 'https://chromium.review.test' } }), { viewer: false, reveal: false });
  assert.ok(discovery['Browser service'].includes('reads /json/version at this address') && !discovery['Browser service'].includes('unencrypted'));
  assert.ok(discovery['Bearer token'].includes('None'));
  for (const m2 of [{ ...stored(), browser: undefined }, stored()]) {
    const viewer = factsOf(m2, { viewer: true, reveal: true });
    assert.equal(viewer['Browser service and options'], '<span class="muted">Hidden from viewers</span>');
    assert.ok(viewer.Screenshots.includes('Private to editors and admins'));
    assert.equal('Bearer token' in viewer || 'Browser service' in viewer, false);
    for (const value of Object.values(viewer)) for (const secret of ['chromium.review.test', 'browser-query-secret', BEARER]) assert.equal(value.includes(secret), false, secret);
  }
});

await check('resolver: the check row owns browser options, projects them byte for byte in both modes, and masks them unless revealed', () => {
  const m = stored({ browser: { delayMs: 250, screenshot: true, bearerToken: BEARER, endpoint: ENDPOINT } });
  const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
  for (const reveal of [false, true]) {
    const rows = resolverModel.monitorRows(ctx, reveal);
    const projected = Object.assign({}, ...rows.map(row => row.project(m)));
    assert.equal(JSON.stringify(projected.browser), JSON.stringify(m.browser), `reveal ${reveal}`);
    const row = rows.find(r => r.key === 'check');
    assert.ok(row.keys.includes('browser'));
    const markup = html(row.render(m));
    assert.ok(markup.includes('loaded in a dedicated Chromium service, then waits 250 ms, with a private screenshot'), markup);
    if (reveal) {
      assert.ok(markup.includes('browser-query-secret') && markup.includes(BEARER), 'revealed options show the endpoint and token');
      assert.equal(markup.includes('endpoint and token hidden'), false);
    } else {
      for (const secret of ['chromium.review.test', 'browser-query-secret', BEARER]) assert.equal(markup.includes(secret), false, secret);
      assert.ok(markup.includes(MASK) && markup.includes('endpoint and token hidden'));
    }
    const other = withOtherFields(rows, [m], reveal, false).find(r => r.key === '__other');
    assert.deepEqual(other?.keys, ['futureTop'], 'browser is not left to the unmasked other-fields row');
  }
  const viewerRows = resolverModel.monitorRows({ ...ctx, role: 'viewer' }, false);
  const viewerMarkup = html(viewerRows.find(r => r.key === 'check').render({ ...m, browser: undefined }));
  assert.ok(viewerMarkup.includes('loaded in a dedicated Chromium service') && !viewerMarkup.includes('hidden'));
});

const pngBytes = (width = 1024, height = 576, total = 300, signature = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]) => {
  const out = Buffer.alloc(total, 7);
  Buffer.from([...signature, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52]).copy(out, 0);
  out.writeUInt32BE(width, 16);
  out.writeUInt32BE(height, 20);
  return out;
};
const png = (...args) => pngBytes(...args).toString('base64');
const SHOT = { monitorId: 'm-rb', nodeId: 'a', checkGeneration: 'g1', checkedAt: 1_760_000_000_000, current: true, mimeType: 'image/png', data: png() };

await check('screenshot reader: a checked 1024×576 PNG of at most 192 KiB for exactly this monitor; anything else is unreadable', () => {
  assert.deepEqual(shots.readScreenshot(SHOT, 'm-rb'), SHOT);
  assert.deepEqual(shots.readScreenshot({ ...SHOT, current: false }, 'm-rb'), { ...SHOT, current: false });
  for (const total of [301, 302, 196_608]) assert.ok(shots.readScreenshot({ ...SHOT, data: png(1024, 576, total) }, 'm-rb'), `${total} bytes`);
  assert.equal(shots.SCREENSHOT_MAX_BASE64, 262_144);
  assert.equal(png(1024, 576, 196_608).length, 262_144);
  const extra = shots.readScreenshot({ ...SHOT, futureField: 'x' }, 'm-rb');
  assert.equal('futureField' in extra, false, 'fields a newer node adds are left out');
  for (const [label, value] of [
    ['another monitor', { ...SHOT, monitorId: 'm-other' }], ['another type', { ...SHOT, mimeType: 'image/jpeg' }], ['a string current', { ...SHOT, current: 'true' }],
    ['time 0', { ...SHOT, checkedAt: 0 }], ['negative time', { ...SHOT, checkedAt: -1 }], ['fractional time', { ...SHOT, checkedAt: 1.5 }], ['time past Date', { ...SHOT, checkedAt: 8_640_000_000_000_001 }],
    ['string time', { ...SHOT, checkedAt: '1760000000000' }], ['empty node', { ...SHOT, nodeId: '' }], ['node with a line break', { ...SHOT, nodeId: 'a\nb' }], ['empty generation', { ...SHOT, checkGeneration: '' }],
    ['no data', { ...SHOT, data: '' }], ['null data', { ...SHOT, data: null }], ['data with a line break', { ...SHOT, data: `${SHOT.data.slice(0, 40)}\n${SHOT.data.slice(40)}` }],
    ['unpadded length', { ...SHOT, data: SHOT.data.slice(0, -1) }], ['URL-safe alphabet', { ...SHOT, data: `${SHOT.data.slice(0, -4)}-_AA` }],
    ['width 1025', { ...SHOT, data: png(1025) }], ['height 577', { ...SHOT, data: png(1024, 577) }], ['a JPEG', { ...SHOT, data: png(1024, 576, 300, [0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 0x4a, 0x46]) }],
    ['over 192 KiB', { ...SHOT, data: png(1024, 576, 196_609) }], ['not an object', 'x'], ['an array', [SHOT]], ['null', null],
  ]) assert.equal(shots.readScreenshot(value, 'm-rb'), undefined, label);
  assert.equal(shots.screenshotSrc(SHOT), `data:image/png;base64,${SHOT.data}`);
});

await check('api: one plain GET of /api/monitors/<id>/screenshot with the bearer and no query; 404 is none; a checked answer is the shot; anything else fails', async () => {
  const client = new api.CanaryApi('http://node-a.test:3100', 'review-account-token');
  const reply = (status, body) => () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  try {
    requests.length = 0;
    answer = reply(404, { error: 'Browser screenshot unavailable' });
    assert.equal(await client.screenshot('m-rb'), null);
    assert.equal(requests.length, 1);
    const [{ url, init }] = requests;
    assert.equal(url, 'http://node-a.test:3100/api/monitors/m-rb/screenshot');
    assert.equal(init.method, 'GET'); assert.equal(init.body, undefined);
    assert.deepEqual(init.headers, { Authorization: 'Bearer review-account-token' });
    assert.equal(init.redirect, 'error'); assert.equal(init.cache, 'no-store'); assert.equal(init.referrerPolicy, 'no-referrer');
    answer = reply(200, SHOT);
    assert.deepEqual(await client.screenshot('m-rb'), SHOT);
    answer = reply(200, { ...SHOT, monitorId: 'm-other' });
    await assert.rejects(client.screenshot('m-rb'), error => error instanceof api.ApiFailure && error.status === 200 && /could not read/u.test(error.message));
    answer = reply(200, { ...SHOT, data: png(800) });
    await assert.rejects(client.screenshot('m-rb'), error => error instanceof api.ApiFailure && error.status === 200);
    answer = reply(403, { error: 'Insufficient permissions' });
    await assert.rejects(client.screenshot('m-rb'), error => error instanceof api.ApiFailure && error.status === 403);
    answer = reply(503, { error: 'Browser screenshot unavailable' });
    await assert.rejects(client.screenshot('m-rb'), error => error instanceof api.ApiFailure && error.status === 503);
    requests.length = 0;
    answer = reply(404, {});
    assert.equal(await client.screenshot('m/x?y'), null);
    assert.equal(requests[0].url, 'http://node-a.test:3100/api/monitors/m%2Fx%3Fy/screenshot', 'the ID is escaped, never a path or query');
  } finally {
    answer = null;
    requests.length = 0;
  }
});

const draftOf = (extra = {}) => ({ endpoint: ENDPOINT, bearer: BEARER, screenshot: true, delay: '250', ...extra });
const fields = (d, extra = {}) => renderToStaticMarkup(React.createElement(BrowserFields, { d, set() {}, errors: {}, disabled: false, storedBearer: false, timeoutMs: 15_000, ...extra }));
await check('form fields: no bundled browser, a dedicated trusted service, hidden endpoint and token inputs, the wait and screenshot rules; no script or command settings', () => {
  const markup = fields(draftOf());
  for (const said of [
    'Each check loads the page in a dedicated Chromium browser service that you run.', 'opens a fresh private browser context and page', 'Downloads are denied', 'adds no scripts and runs no commands',
    'Pending is never inverted and never recovers an outage.', 'Canary doesn’t include or start a browser.', 'No node image bundles Chrome or Chromium',
    'never someone’s everyday browser or profile', 'anyone who can use the endpoint controls that browser', '<span class="mono">/json/version</span>', 'at most 2,048 bytes',
    'Printable ASCII without spaces, at most 4,096 characters', '0 to 30,000, and less than the timeout (15 s)', '1024×576 PNG, at most 192 KiB', 'never replicated, published on status pages or shown to viewers',
    'Each node runs at most 2 browser checks at once',
  ]) assert.ok(markup.includes(said), said);
  assert.equal((markup.match(/role="note"/gu) ?? []).length, 2);
  assert.doesNotMatch(markup, /<(select|textarea)\b/u);
  const all = inputs(markup);
  assert.equal(all.length, 4, 'endpoint, token, wait and screenshot');
  const [endpoint, bearer, delay, shot] = all;
  assert.equal(endpoint.type, 'password'); assert.equal(endpoint.value, ENDPOINT);
  assert.equal(bearer.type, 'password'); assert.equal(bearer.value, BEARER);
  assert.equal(delay.type, 'text'); assert.equal(delay.value, '250'); assert.equal(delay.inputmode, 'numeric');
  assert.equal(shot.type, 'checkbox'); assert.ok('checked' in shot);
  assert.equal(markup.split(ENDPOINT).length - 1, 1, 'the endpoint is only the hidden field’s value');
  assert.equal(markup.split(BEARER).length - 1, 1, 'the token is only the hidden field’s value');
  assert.equal(markup.includes('travels in clear'), false, 'wss:// is encrypted');
  const plainWs = fields(draftOf({ endpoint: 'ws://127.0.0.1:9222/x' }));
  assert.ok(plainWs.includes('travels in clear') && plainWs.includes('use wss://'));
  const plainHttp = fields(draftOf({ endpoint: 'http://127.0.0.1:9222' }));
  assert.ok(plainHttp.includes('travels in clear') && plainHttp.includes('use https://'));
  assert.equal(fields(draftOf({ endpoint: 'ws://127.0.0.1:9222/x', bearer: '' })).includes('travels in clear'), false, 'no token, nothing in clear');
  assert.ok(fields(draftOf({ bearer: '' }), { storedBearer: true }).includes('Saving removes the stored value.'));
  assert.ok(fields(draftOf(), { errors: { browserEndpoint: 'Bad endpoint.' } }).includes('Bad endpoint.'));
});

const view = (extra = {}) => ({ monitor: stored(), state: 'up', observations: [], unknownNodes: 0, configConflict: false, ...extra });
const figure = (shot, v = view(), endpoint = 'a') => renderToStaticMarkup(React.createElement(ScreenshotFigure, { shot, view: v, endpoint, now: 1_760_000_100_000 }));
await check('screenshot figure: shown only for this monitor, node and check generation; an older capture says so and never stands for current health', () => {
  const latest = figure(SHOT);
  assert.ok(latest.includes('Latest capture') && latest.includes('’s latest fresh check of this monitor.') && latest.includes('it isn’t proof that the monitor is healthy'));
  const [img] = [...latest.matchAll(/<img\b([^>]*?)\/?>/gu)].map(([, attrs]) => Object.fromEntries([...attrs.matchAll(/([^\s=/]+)(?:="([^"]*)")?/gu)].map(([, name, value = '']) => [name.toLowerCase(), value])));
  assert.equal(img.src, shots.screenshotSrc(SHOT));
  assert.ok(img.src.startsWith('data:image/png;base64,'));
  assert.equal(img.width, '1024'); assert.equal(img.height, '576');
  assert.equal(img.alt, 'Screenshot of https://shop.example.test/checkout, taken by a');
  assert.equal((latest.match(/<img\b/gu) ?? []).length, 1);
  const older = figure({ ...SHOT, current: false });
  assert.ok(older.includes('Older capture') && older.includes('Don’t read it as the page’s current state') && !older.includes('Latest capture'));
  assert.ok(older.includes('<img'), 'an older capture is still shown, labelled with its time and node');
  for (const [label, markup] of [
    ['another node', figure(SHOT, view(), 'b')], ['another monitor', figure({ ...SHOT, monitorId: 'm-other' })],
    ['another generation', figure(SHOT, view({ monitor: stored({ checkGeneration: 'g2' }) }))],
  ]) {
    assert.equal(markup.includes('<img'), false, label);
    assert.equal(markup.includes(SHOT.data.slice(0, 40)), false, label);
  }
  assert.equal(screenshotGeneration(view()), 'g1');
  assert.equal(screenshotGeneration(view({ monitor: { ...stored(), checkGeneration: undefined } })), 'r1', 'older nodes: the revision is the generation');
});

const asked = [];
const context = (role, nodeId = 'a') => ({
  api: { screenshot: (...args) => { asked.push(args); return new Promise(() => {}); } }, connection: { identity: 7 }, endpoint: nodeId, overview: { now: 1_760_000_100_000 },
  editable: role !== 'viewer', role,
});
const card = (role, monitor = stored(), nodeId = 'a') =>
  renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: context(role, nodeId) }, React.createElement(BrowserScreenshotCard, { view: view({ monitor }) })));
await check('screenshot card: editors and admins only, for real-browser monitors with screenshots on, on a node that runs the check; viewers get nothing', () => {
  for (const role of ['editor', 'admin']) {
    const markup = card(role);
    assert.ok(markup.includes('Screenshot') && markup.includes('Loading the newest screenshot from a') && markup.includes('Private to editors and admins') && markup.includes('never replicated to other nodes'), role);
    assert.equal(markup.includes('<img'), false);
  }
  assert.equal(card('viewer'), '', 'viewers get no card');
  assert.equal(card('viewer', { ...stored(), browser: undefined }), '');
  assert.equal(card('editor', stored({ browser: { endpoint: ENDPOINT } })), '', 'screenshots off: no card');
  assert.equal(card('editor', { ...stored(), type: 'http', browser: undefined }), '');
  const elsewhere = card('editor', stored({ nodeIds: ['b'] }));
  assert.ok(elsewhere.includes('a doesn’t run this check') && elsewhere.includes('keeps only the screenshots it took itself'));
  assert.deepEqual(asked, [], 'nothing is requested while rendering');
  const cardSource = source('views/BrowserScreenshot.tsx');
  assert.ok(cardSource.includes('const key = `${connection.identity}|screenshot|${m.id}|${generation}|${asks}`;'), 'an answer belongs to one connection, monitor and generation');
  assert.ok(cardSource.includes('if (asks && before.key === key && before.latest !== latest) reload();'));
  const detail = source('views/MonitorDetail.tsx');
  assert.equal(detail.split("{m.type === 'real-browser' && m.browser?.screenshot === true && editable ? <BrowserScreenshotCard view={view} /> : null}").length - 1, 1);
});

await check('sources: one data: URL source and one image, no storage, console or extra request paths; no raw separators, NEL or BOM in new or edited files', () => {
  const files = readdirSync(resolve(root, 'apps/web/src'), { recursive: true }).map(String).map(file => file.replace(/\\/gu, '/')).filter(file => /\.(ts|tsx)$/u.test(file));
  assert.deepEqual(files.filter(file => source(file).includes('data:image/png')), ['screenshot.ts'], 'one place builds the data: URL');
  assert.deepEqual(files.filter(file => source(file).includes('screenshotSrc(')), ['views/BrowserScreenshot.tsx'], 'one place shows it');
  assert.equal(source('views/BrowserScreenshot.tsx').split('<img ').length - 1, 1);
  assert.ok(source('views/BrowserScreenshot.tsx').includes('src={screenshotSrc(shot)}'));
  assert.ok(source('api.ts').includes("`/api/monitors/${encodeURIComponent(id)}/screenshot`, 'GET'"));
  for (const file of ['browser.tsx', 'screenshot.ts', 'views/BrowserFields.tsx', 'views/BrowserScreenshot.tsx']) {
    assert.doesNotMatch(source(file), /localStorage|sessionStorage|indexedDB|document\.cookie|\bconsole\.[a-z]+\s*\(|\bfetch\s*\(/u, file);
  }
  const bad = [0x2028, 0x2029, 0x85, 0xfeff].map(ch);
  for (const file of ['browser.tsx', 'screenshot.ts', 'tailscale.tsx', 'api.ts', 'checks.tsx', 'styles.css', 'views/BrowserFields.tsx', 'views/BrowserScreenshot.tsx', 'views/MonitorForm.tsx', 'views/MonitorDetail.tsx', 'views/Config.tsx']) {
    const text = readFileSync(resolve(root, 'apps/web/src', file), 'utf8');
    for (const char of bad) assert.equal(text.includes(char), false, `${file} contains U+${char.codePointAt(0).toString(16).padStart(4, '0')}`);
  }
});

await check('no browser storage writes or requests outside the API check', () => {
  assert.deepEqual(touched, []);
  assert.deepEqual(requests, []);
});

console.log(failed ? `${failed} FAILED` : 'ALL PASSED');
process.exit(failed ? 1 : 0);
