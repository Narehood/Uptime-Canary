// Pure protocol-19 checks for the GameDig monitor kind: apps/web/src/gamedig.tsx (a mirror of the node's
// gamedigmonitor.Validate: the Tailscale peer rule for the target, the browser service's Go 1.27 url.Parse port for the
// query service origin), the shared GameDig 5.3.3 catalogue metadata, views/GameDigFields.tsx (rendered to static markup),
// checks.tsx, the monitor form through its cloneModel and previewModel seams, and the Configuration resolver through its
// resolverModel seam. Imports the source in place through one tsx namespace: no copy, scratch, network, browser, game
// server, query service or output files; storage and fetch are stand-ins that record any use, and none is expected. The
// expected admissions come from the node's source as read (internal/gamedigmonitor/check.go, canary/validation.go,
// tailscalemonitor.Validate, Go's net/url); root's contract harness sends the same kinds of cases to a real, paused node.
// The 358 IDs are the pinned upstream catalogue, not 358 separately validated protocols. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/gamedig-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
// Stand-ins installed before any source loads: storage writes and requests are recorded; nothing here may use them.
const touched = [];
const recorder = name => (...args) => { touched.push([name, ...args.map(String)]); };
for (const name of ['localStorage', 'sessionStorage']) {
  Object.defineProperty(globalThis, name, { value: { getItem: () => null, setItem: recorder(`${name}.setItem`), removeItem: recorder(`${name}.removeItem`), clear: recorder(`${name}.clear`) }, configurable: true, writable: true });
}
const requests = [];
globalThis.fetch = async (url, init = {}) => { requests.push({ url: String(url), init }); throw new Error('no network in this fixture'); };
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'gamedig-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const g = await load('gamedig.tsx');
const ts = await load('tailscale.tsx');
const b = await load('browser.tsx');
const checks = await load('checks.tsx');
const fmt = await load('format.ts');
const { cloneModel, previewModel } = await load('views/MonitorForm.tsx');
const { resolverModel, withOtherFields } = await load('views/Config.tsx');
const { GameDigFields } = await load('views/GameDigFields.tsx');

let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const ch = code => String.fromCodePoint(code);
const MASK = ch(0x2022).repeat(3);
const LONE = String.fromCharCode(0xd800);
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const goSource = file => readFileSync(resolve(root, 'apps/node-go/internal', file), 'utf8').replace(/\r\n/gu, '\n');
const html = node => renderToStaticMarkup(React.createElement(React.Fragment, null, node));
const esc = text => text.replace(/&/gu, '&amp;').replace(/</gu, '&lt;').replace(/>/gu, '&gt;').replace(/"/gu, '&quot;').replace(/'/gu, '&#x27;');
/** Every <input> in rendered markup, by its attributes in any order: React 19 writes `value` last. */
const inputs = markup => [...markup.matchAll(/<input\b([^>]*?)\/?>/gu)].map(([, attrs]) => Object.fromEntries([...attrs.matchAll(/([^\s=/]+)(?:="([^"]*)")?/gu)].map(([, name, value = '']) => [name.toLowerCase(), value])));

// The 31 types before this change, in the form's order. GameDig is the 32nd (an intentional catalogue addition).
const EARLIER = ['http', 'real-browser', 'ping', 'tcp', 'tls', 'dns', 'websocket', 'smtp', 'globalping', 'steam', 'system-service', 'pm2', 'tailscale-ping', 'postgres', 'mysql', 'sqlserver',
  'oracledb', 'mongodb', 'redis', 'docker', 'mqtt', 'rabbitmq', 'kafka-producer', 'grpc', 'sftp', 'ntp', 'snmp', 'radius', 'sip-options', 'push', 'manual'];
const OPTION_KEYS = ['http', 'tls', 'dns', 'websocket', 'smtp', 'service', 'ping', 'manual', 'push', 'globalping', 'steam', 'pm2', 'browser', 'expectedStatusCodes'];
const BEARER = 'private-game-service-proof-0123456789';
const TOKEN = 'private-game-target-<&>-proof';
// The shape root's proof sends (apps/proof/test/gamedig.test.ts), with its loopback service address.
const STORED = { game: 'terrariatshock', port: 7777, givenPortOnly: true, token: TOKEN, endpoint: 'http://127.0.0.1:3121', bearerToken: BEARER, allowInsecureHTTP: true };
const monitor = (extra = {}) => ({
  id: 'm-gd', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', name: 'Game server', type: 'gamedig', target: 'play.example.com',
  intervalMs: 60_000, timeoutMs: 4000, nodeIds: ['a'], enabled: true, retries: 1, gamedig: structuredClone(STORED), futureTop: { kept: [1, null] }, ...extra,
});
const SECRETS = [TOKEN, esc(TOKEN), BEARER, '127.0.0.1:3121'];

await check('catalogue: 32 unique types, the 31 earlier ones in their order, GameDig right after Steam among game servers, not a typed-target type', () => {
  const types = checks.CHECK_TYPES.map(entry => entry.type);
  assert.equal(types.length, 32);
  assert.equal(new Set(types).size, 32);
  assert.deepEqual(types.filter(type => type !== 'gamedig'), EARLIER);
  assert.equal(types.indexOf('gamedig'), types.indexOf('steam') + 1);
  const entry = checks.checkType('gamedig');
  assert.equal(entry.label, 'GameDig game server');
  assert.equal(entry.short, 'gamedig');
  assert.equal(entry.category, 'game');
  assert.equal(entry.targetLabel, undefined, 'the server address is a GameDig field, beside the game and port');
  assert.deepEqual(checks.CHECK_TYPES.filter(other => other.category === 'game').map(other => other.type), ['steam', 'gamedig']);
  assert.equal(checks.TYPED_TARGET_TYPES.has('gamedig'), false);
  assert.equal(checks.isServiceType('gamedig'), false);
  assert.equal(checks.tlsApplies('gamedig', 'play.example.com', undefined), false, 'the node refuses TLS options on a GameDig monitor');
  assert.equal(checks.typeLabel('gamedig'), 'GameDig game server');
});

await check('pinned metadata: the shared catalogue is the node’s embedded GameDig 5.3.3 catalogue, and the node’s fixed messages, limits and field order match', () => {
  const catalogue = JSON.parse(goSource('gamedigmonitor/catalogue.json'));
  const upstream = JSON.parse(goSource('gamedigmonitor/upstream.json'));
  assert.equal(g.GAMEDIG_VERSION, '5.3.3');
  assert.equal(catalogue.version, '5.3.3');
  assert.equal(upstream.version, '5.3.3');
  assert.deepEqual(g.GAMEDIG_GAMES, catalogue.games, 'the dashboard lists exactly what the node embeds');
  // This pinned release's catalogue size; Go refuses a catalogue outside 100–1024 IDs at start-up.
  assert.equal(g.GAMEDIG_GAMES.length, 358);
  assert.equal(new Set(g.GAMEDIG_GAMES.map(game => game.id)).size, 358);
  for (const game of g.GAMEDIG_GAMES) {
    assert.ok(game.id && game.name, JSON.stringify(game));
    if ('defaultPort' in game) assert.ok(Number.isInteger(game.defaultPort) && game.defaultPort >= 1 && game.defaultPort <= 65_535, game.id);
  }
  assert.deepEqual(g.gamedigGame('minecraft'), { id: 'minecraft', name: 'Minecraft', defaultPort: 25565 });
  assert.deepEqual(g.gamedigGame('terrariatshock'), { id: 'terrariatshock', name: 'Terraria - TShock', defaultPort: 7777 });
  assert.equal(g.gamedigGame('alienarena')?.defaultPort, undefined, 'a game without a default port in the catalogue');
  assert.equal(g.GAMEDIG_BY_NAME.length, 358);
  assert.equal(g.gamedigFilter('terraria').every(game => /terraria/iu.test(game.name) || /terraria/iu.test(game.id)), true);
  assert.ok(g.gamedigFilter('TERRARIA').some(game => game.id === 'terrariatshock'));
  assert.equal(g.gamedigFilter('  ').length, 358);
  const go = goSource('gamedigmonitor/check.go');
  assert.match(go, /const Profile = "gamedig\/5\.3\.3"/u);
  assert.match(go, new RegExp(`const Available = "${g.GAMEDIG_SUMMARY.up}"`, 'u'));
  assert.match(go, new RegExp(`const Failed = "${g.GAMEDIG_SUMMARY.down}"`, 'u'));
  assert.match(go, new RegExp(`const Unavailable = "${g.GAMEDIG_SUMMARY.pending}"`, 'u'));
  assert.match(go, /tailscalemonitor\.Validate\(tailscalemonitor\.Config\{Name: c\.Target/u, 'the target uses the Tailscale peer rule');
  assert.match(go, /len\(c\.BearerToken\) < 24 \|\| len\(c\.BearerToken\) > 256/u);
  assert.match(go, /len\(c\.Token\) > 1024/u);
  assert.match(go, /len\(c\.Endpoint\) > 2048/u);
  assert.match(go, /c\.Port < 0 \|\| c\.Port > 65535 \|\| c\.GivenPortOnly && c\.Port == 0/u);
  assert.match(go, /var slots = make\(chan struct\{\}, 2\)/u);
  assert.match(go, /\(c\.Timeout \* 3 \/ 4\)\.Milliseconds\(\)/u);
  const tags = [...go.match(/type Options struct \{([\s\S]*?)\n\}/u)[1].matchAll(/json:"([^"]+)"/gu)].map(match => match[1]);
  assert.deepEqual(tags, ['game', 'port,omitempty', 'givenPortOnly,omitempty', 'token,omitempty', 'endpoint', 'bearerToken', 'allowInsecureHTTP,omitempty']);
  assert.deepEqual(Object.keys(g.GAMEDIG_FIELD_LABEL), ['gamedigGame', 'gamedigTarget', 'gamedigPort', 'gamedigToken', 'gamedigEndpoint', 'gamedigBearer']);
  assert.match(goSource('canary/views.go'), /m\.GameDig = nil/u, 'viewers receive no gamedig object');
});

await check('game: only exact catalogue IDs; a stored ID this catalogue lacks is kept only while unchanged', () => {
  for (const game of g.GAMEDIG_GAMES) assert.equal(g.gamedigGameProblem(game.id, undefined), undefined, game.id);
  for (const [id, said] of [['', 'Choose the game'], ['Minecraft', '“Minecraft” isn’t a game ID in GameDig 5.3.3’s catalogue'], [' minecraft', 'isn’t a game ID'], ['minecraft ', 'isn’t a game ID'],
    ['not-a-game', 'isn’t a game ID'], ['__proto__', 'isn’t a game ID'], ['constructor', 'isn’t a game ID']]) {
    assert.ok(g.gamedigGameProblem(id, undefined)?.includes(said), `${JSON.stringify(id)}: ${g.gamedigGameProblem(id, undefined)}`);
  }
  assert.equal(g.gamedigGameProblem('future-game', 'future-game'), undefined, 'a newer node’s stored game: that node decides');
  assert.ok(g.gamedigGameProblem('other-future', 'future-game'), 'another unknown ID is refused');
  assert.ok(g.gamedigGameProblem('', ''), 'an empty stored game is still missing');
  assert.equal(g.gamedigGameLabel('minecraft'), 'Minecraft (minecraft)');
  assert.equal(g.gamedigGameLabel('future-game'), 'future-game');
});

// gamedigmonitor.Validate passes the target to tailscalemonitor.Validate unchanged: 1–253 bytes; one literal IPv4 or IPv6
// address (Go net/netip) without a zone that isn't unspecified or multicast, or ASCII labels of letters, digits and inner
// hyphens with at most one trailing dot. Numeric names that netip refuses fall through to the label rule.
const TARGETS = [
  'play.example.com', 'play.example.com.', 'Minecraft-01', 'localhost', '0', 'a'.repeat(63), `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`,
  '203.0.113.10', '127.0.0.1', '255.255.255.255', '01.2.3.4', '1.2.3.256', '1.2.3', '2001:db8::10', '::1', '::ffff:203.0.113.10', '::ffff:0.0.0.0',
  '', ' play', 'play ', 'play\n', `pl${ch(0)}ay`, `play${ch(0x2028)}`, 'play.example.com:25565', '203.0.113.10:7777', '[::1]', '[::1]:25565', 'https://play.example.com', 'play/x', 'play\\x',
  'user@play', 'fe80::1%eth0', '0.0.0.0', '::', '224.0.0.1', 'ff02::1', '::ffff:224.0.0.1', '1::2::3', '12345::1', 'pl_ay', 'a'.repeat(64), `caf${ch(0xe9)}`, `pl${LONE}ay`,
  'play..example', '.play', '-play', 'play-', `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(62)}`, '--help', '$(id)', 'play;id',
];

await check('server address: exactly the Tailscale peer rule the node calls, with game-server reasons', () => {
  for (const target of TARGETS) {
    const problem = g.gamedigTargetProblem(target);
    assert.equal(problem === undefined, ts.tailscaleTargetAccepted(target), `${JSON.stringify(target)}: ${problem}`);
    if (problem) assert.doesNotMatch(problem, /Tailscale|peer|MagicDNS/u, `${JSON.stringify(target)}: ${problem}`);
  }
  assert.equal(TARGETS.filter(target => g.gamedigTargetProblem(target) === undefined).length, 17);
  for (const [target, said] of [
    ['', 'host name or IP address, like play.example.com'], ['play.example.com:25565', 'enter it in the port field below'], ['[::1]:25565', 'Remove the brackets'],
    ['https://play.example.com', 'not a URL or a path'], ['fe80::1%eth0', 'Remove the %zone'], ['user@play', 'without a user name'], [' play', 'without spaces or line breaks'],
    ['0.0.0.0', 'aren’t a game server'], ['::', 'aren’t a game server'], ['224.0.0.1', 'aren’t a game server'], ['ff02::1', 'aren’t a game server'], ['1::2::3', 'valid IPv6 address'],
    ['pl_ay', '“_” isn’t allowed'], [`caf${ch(0xe9)}`, 'xn-- form'], ['play..example', 'empty part'], ['a'.repeat(64), 'at most 63 characters'], ['-play', 'start or end with a hyphen'],
  ]) assert.ok(g.gamedigTargetProblem(target)?.includes(said), `${JSON.stringify(target)}: ${g.gamedigTargetProblem(target)}`);
});

await check('port and only-this-port: empty or 0 is the game’s default and omitted, 1–65,535 kept as a number; only-this-port needs a port; refused values reach the node as refused values', () => {
  for (const port of ['', '0', '00', '1', '25565', '65535', ' 27015 ', '00080']) assert.equal(g.gamedigPortProblem(port, false), undefined, port);
  for (const port of ['65536', '-1', '1.5', '1e3', 'abc', '0x10', '99999999999999999999', ch(0x663), '+80']) assert.ok(g.gamedigPortProblem(port, false)?.includes('whole number from 1 to 65,535'), port);
  for (const port of ['', '0', '00', ' ']) assert.ok(g.gamedigPortProblem(port, true)?.includes('Enter the port to query only that port'), JSON.stringify(port));
  assert.equal(g.gamedigPortProblem('27015', true), undefined);
  const port = (value, givenPortOnly = false) => {
    const errors = {};
    const built = g.buildGameDig(undefined, { ...g.gamedigDraftFrom(undefined, 'play.example.com'), game: 'minecraft', endpoint: 'https://h', bearer: BEARER, port: value, givenPortOnly }, errors);
    return { value: built.value, errors };
  };
  assert.equal('port' in port('').value, false);
  assert.equal('port' in port('0').value, false, 'Go encodes port 0 as absent');
  assert.equal('port' in port('00').value, false);
  assert.equal(port('00080').value.port, 80);
  assert.equal(port(' 27015 ').value.port, 27015);
  assert.equal(port('65536').value.port, 65536, 'refused, and the node refuses it too');
  assert.equal(port('abc').value.port, 'abc', 'refused text is sent as text, which the node refuses');
  assert.equal(port('0x10').value.port, '0x10', 'never converted into a number the node would accept');
  assert.deepEqual(Object.keys(port('abc').errors), ['gamedigPort']);
  assert.equal(port('27015', true).value.givenPortOnly, true);
  assert.equal('givenPortOnly' in port('27015').value, false, 'false is omitted');
  assert.deepEqual(Object.keys(port('', true).errors), ['gamedigPort']);
});

await check('game token: sent exactly as typed, at most 1,024 UTF-8 bytes, no Cc control characters (Go’s unicode.IsControl), no unpaired surrogates', () => {
  const smile = ch(0x1f600);
  for (const token of ['', 'x', ' spaced token ', 'a'.repeat(1024), ch(0xe9).repeat(512), smile.repeat(256), `a${ch(0x2028)}b`, `a${ch(0xa0)}b`, '<&>"\'', `${ch(0xfeff)}x`, `a${ch(0xad)}b`]) {
    assert.equal(g.gamedigTokenProblem(token), undefined, JSON.stringify(token.slice(0, 12)));
  }
  for (const [token, said] of [
    ['a'.repeat(1025), 'at most 1,024 bytes'], [ch(0xe9).repeat(513), 'at most 1,024 bytes'], [`${smile.repeat(256)}a`, 'at most 1,024 bytes'],
    ['a\n', 'control characters'], ['a\t', 'control characters'], ['a\r', 'control characters'], [`a${ch(0)}`, 'control characters'], ['a\x7f', 'control characters'],
    [`a${ch(0x85)}`, 'control characters'], [`a${ch(0x9f)}`, 'control characters'], [`a${LONE}`, 'unpaired surrogate'],
  ]) assert.ok(g.gamedigTokenProblem(token)?.includes(said), `${JSON.stringify(token.slice(0, 12))}: ${g.gamedigTokenProblem(token)}`);
  const built = g.buildGameDig(undefined, { ...g.gamedigDraftFrom(undefined, 'play.example.com'), game: 'minecraft', endpoint: 'https://h', bearer: BEARER, token: ' spaced token ' }, {});
  assert.equal(built.value.token, ' spaced token ', 'never trimmed');
  assert.equal('token' in g.buildGameDig(undefined, { ...g.gamedigDraftFrom(undefined, 'play.example.com'), game: 'minecraft', endpoint: 'https://h', bearer: BEARER }, {}).value, false);
});

// gamedigmonitor.Validate over Go 1.27's url.Parse: a host name, no user information, no fragment text, no query, a path
// of "" or "/", at most 2048 bytes, and https, or http with allowInsecureHTTP. The scheme is lowercased; a bare trailing
// "?" (ForceQuery) or "#" leaves no query or fragment; IPv6 literals go through netip; http(s) hosts with extra colons are
// refused; ports are digits that may be empty and aren't range-checked; hosts escape only bytes from 0x80 and %25.
const ENDPOINT_OK = [
  'https://game-query.internal:3121', 'https://game-query.internal:3121/', 'HTTPS://Game-Query.Internal', 'https://203.0.113.10:3121', 'https://[fd00::10]:3121', 'https://[::1]',
  'https://h?', 'https://h#', 'https://h/?', 'https://h:99999', 'https://h:', 'https://a]b', `https://b${ch(0xfc)}cher.example`, 'https://%C3%BC.example', `https://${'a'.repeat(2040)}`,
];
const ENDPOINT_HTTP = ['http://127.0.0.1:3121', 'http://game-query:3121/', 'Http://game-query', 'https://game-query.internal:3121'];
const ENDPOINT_REFUSED = [
  ['', 'Enter the game query service’s address'], ['http://127.0.0.1:3121', 'allow plain HTTP below'], ['HTTP://h', 'allow plain HTTP below'],
  ['https://h/v1/query', 'remove the path and the query'], ['https://h?x=1', 'remove the path and the query'], ['https://h/??', 'remove the path and the query'],
  ['https://h/%2F', 'remove the path and the query'], ['https://h/%zz', 'remove the path and the query'], ['https://h/x/', 'remove the path and the query'],
  ['https://user:pw@h', 'Remove user:password@'], ['https://@h', 'Remove user:password@'], ['https://u@p@h', 'Remove user:password@'],
  ['https://h#frag', 'Remove the #fragment'], ['https://h/#%zz', 'Remove the #fragment'],
  ['ws://h', 'Start with https://'], ['wss://h', 'Start with https://'], ['ftp://h', 'Start with https://'], [' https://h', 'Start with https://'], ['game-query.internal:3121', 'Start with https://'], ['://h', 'Start with https://'],
  ['https:h', 'complete address with //'], ['https:/h', 'complete address with //'],
  ['https://', 'Enter the host name or address after //'], ['https://:3121', 'Enter the host name or address after //'], ['https:///x', 'Enter the host name or address after //'],
  ['https://a:1:2', 'a port is digits only, like :3121'], ['https://h:x', 'a port is digits only, like :3121'], ['https://[::1]x', 'a port is digits only, like :3121'],
  ['https://[127.0.0.1]', 'like https://[fd00::10]:3121'], ['https://[::1', 'like https://[fd00::10]:3121'], ['https://a[b]', 'like https://[fd00::10]:3121'], ['https://[fe80::1%eth0]', 'like https://[fd00::10]:3121'],
  ['https://a b', 'Check the host name'], ['https://h^', 'Check the host name'], ['https://a%41', 'Check the host name'], ['https://h\\x', 'Check the host name'],
  ['https://h\n', 'control characters'], ['https://h/\t', 'control characters'], [`https://h${ch(0)}`, 'control characters'],
  [`https://h/${LONE}`, 'unpaired surrogate'], [`https://${'a'.repeat(2041)}`, 'at most 2,048 bytes'], [`https://h/${ch(0xe9).repeat(1020)}`, 'at most 2,048 bytes'],
];

await check('service address: the node’s https (or allowed http) origin rule over Go’s url.Parse, with query-service reasons and never browser wording', () => {
  for (const endpoint of ENDPOINT_OK) assert.equal(g.gamedigEndpointProblem(endpoint, false), undefined, JSON.stringify(endpoint.slice(0, 60)));
  assert.equal(new TextEncoder().encode(ENDPOINT_OK.at(-1)).length, 2048);
  for (const endpoint of ENDPOINT_HTTP) assert.equal(g.gamedigEndpointProblem(endpoint, true), undefined, `${endpoint} with plain HTTP allowed`);
  for (const [endpoint, said] of ENDPOINT_REFUSED) {
    const problem = g.gamedigEndpointProblem(endpoint, false);
    assert.ok(problem?.includes(said), `${JSON.stringify(endpoint.slice(0, 60))}: ${problem}`);
    assert.doesNotMatch(problem, /DevTools|browser|9222|ws:\/\//iu, `${JSON.stringify(endpoint.slice(0, 60))}: ${problem}`);
  }
  // One rule, shared: acceptance is the browser service's http(s) origin rule plus the scheme and plain-HTTP conditions.
  for (const endpoint of [...ENDPOINT_OK, ...ENDPOINT_HTTP, ...ENDPOINT_REFUSED.map(([value]) => value)]) {
    for (const allowHttp of [false, true]) {
      const scheme = g.gamedigEndpointScheme(endpoint);
      const expected = (scheme === 'https' || (scheme === 'http' && allowHttp)) && /^[A-Za-z]+:/u.test(endpoint) && b.browserEndpointProblem(endpoint) === undefined;
      assert.equal(g.gamedigEndpointProblem(endpoint, allowHttp) === undefined, expected, `${JSON.stringify(endpoint.slice(0, 60))} allowHttp=${allowHttp}`);
    }
  }
  assert.equal(g.gamedigEndpointScheme('HTTPS://h'), 'https');
  assert.equal(g.gamedigEndpointScheme('http://h'), 'http');
  assert.equal(g.gamedigEndpointScheme('ws://h'), 'unknown');
});

await check('service token: required, 24–256 printable ASCII characters without spaces, as the service itself requires', () => {
  for (const token of ['a'.repeat(24), 'a'.repeat(256), '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~', BEARER]) assert.equal(g.gamedigBearerProblem(token), undefined, token.slice(0, 20));
  for (const [token, said] of [
    ['', 'Enter the query service’s token'], ['a'.repeat(23), 'at least 24 characters (23 so far)'], ['a'.repeat(257), 'at most 256'], [`${'a'.repeat(23)} b`, 'no spaces'],
    [`Bearer ${'a'.repeat(24)}`, 'without “Bearer ”'], [`${'a'.repeat(24)}${ch(0xa0)}`, 'no spaces'], [`${'a'.repeat(24)}\t`, 'control characters'], [`${'a'.repeat(24)}\n`, 'control characters'],
    [`${'a'.repeat(24)}${ch(0xe9)}`, 'printable ASCII only'], [`${'a'.repeat(24)}${LONE}`, 'printable ASCII only'],
  ]) assert.ok(g.gamedigBearerProblem(token)?.includes(said), `${JSON.stringify(token.slice(0, 30))}: ${g.gamedigBearerProblem(token)}`);
  const typed = g.buildGameDig(undefined, { ...g.gamedigDraftFrom(undefined, 'play.example.com'), game: 'minecraft', endpoint: 'https://h', bearer: ` ${BEARER} ` }, {});
  assert.equal(typed.value.bearerToken, BEARER, 'a typed token is trimmed');
});

await check('builder: a new object follows Go’s field order with only what is set; unedited stored options go back byte for byte, key order and a newer node’s keys included', () => {
  let errors = {};
  const full = g.buildGameDig(undefined, { game: 'minecraft', target: ' play.example.com ', port: ' 25565 ', givenPortOnly: true, token: ' game token ', endpoint: '  https://game-query.internal:3121  ', bearer: ` ${BEARER} `, insecure: false }, errors);
  assert.deepEqual(errors, {});
  assert.equal(full.target, 'play.example.com');
  assert.equal(JSON.stringify(full.value), JSON.stringify({ game: 'minecraft', port: 25565, givenPortOnly: true, token: ' game token ', endpoint: 'https://game-query.internal:3121', bearerToken: BEARER }));
  errors = {};
  const http = g.buildGameDig(undefined, { ...g.gamedigDraftFrom(STORED, 'play.example.com') }, errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(http.value), JSON.stringify(STORED), 'every field, in Go’s order');
  const minimal = g.buildGameDig(undefined, { ...g.gamedigDraftFrom(undefined, 'play.example.com'), game: 'minecraft', endpoint: 'https://h', bearer: BEARER }, {});
  assert.deepEqual(Object.keys(minimal.value), ['game', 'endpoint', 'bearerToken']);
  const variants = [
    STORED,
    { bearerToken: BEARER, endpoint: 'http://127.0.0.1:3121', allowInsecureHTTP: true, game: 'terrariatshock', token: TOKEN, givenPortOnly: true, port: 7777 },
    { ...STORED, futureMode: { nested: [1, null] }, futureSecret: 'future-private' },
    { game: 'minecraft', endpoint: 'HTTPS://Game-Query.Internal:3121/', bearerToken: BEARER },
    { game: 'future-game', endpoint: 'https://h?', bearerToken: BEARER },
    { game: 'minecraft', endpoint: 'https://h', bearerToken: BEARER, token: ' spaced ' },
  ];
  for (const stored of variants) {
    const before = JSON.stringify(stored);
    const draft = g.gamedigDraftFrom(stored, 'play.example.com');
    errors = {};
    const built = g.buildGameDig(stored, draft, errors);
    assert.deepEqual(errors, {}, before);
    assert.equal(JSON.stringify(built.value), before, before);
    assert.equal(JSON.stringify(stored), before, 'stored object not mutated');
  }
  const draft = g.gamedigDraftFrom(STORED, 'play.example.com');
  assert.deepEqual(draft, { game: 'terrariatshock', target: 'play.example.com', port: '7777', givenPortOnly: true, token: TOKEN, endpoint: 'http://127.0.0.1:3121', bearer: BEARER, insecure: true });
  assert.equal(JSON.stringify(g.buildGameDig(STORED, { ...draft, port: '7778' }, {}).value), JSON.stringify({ ...STORED, port: 7778 }), 'an edit stays in place');
  const cleared = g.buildGameDig(STORED, { ...draft, port: '', givenPortOnly: false, token: '', endpoint: 'https://game-query.internal:3121', insecure: false }, errors = {});
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(cleared.value), JSON.stringify({ game: 'terrariatshock', endpoint: 'https://game-query.internal:3121', bearerToken: BEARER }), 'cleared settings are removed, never sent empty');
  const plain = { game: 'minecraft', endpoint: 'https://h', bearerToken: BEARER };
  assert.deepEqual(Object.keys(g.buildGameDig(plain, { ...g.gamedigDraftFrom(plain, 'p'), port: '27015' }, {}).value), ['game', 'endpoint', 'bearerToken', 'port'], 'a new key is appended');
  assert.equal(g.buildGameDig(variants[3], { ...g.gamedigDraftFrom(variants[3], 'p'), endpoint: ' https://other ' }, {}).value.endpoint, 'https://other', 'a typed address is trimmed');
  errors = {};
  const bad = g.buildGameDig(undefined, { game: 'Minecraft', target: 'play:25565', port: 'x', givenPortOnly: false, token: 'a\n', endpoint: 'https://h/v1/query', bearer: 'short', insecure: false }, errors);
  assert.deepEqual(Object.keys(errors).sort(), ['gamedigBearer', 'gamedigEndpoint', 'gamedigGame', 'gamedigPort', 'gamedigTarget', 'gamedigToken']);
  assert.equal(bad.value.endpoint, 'https://h/v1/query', 'the refused value is what would have been sent');
  assert.deepEqual(g.gamedigDraftFrom(undefined, ''), { game: '', target: '', port: '', givenPortOnly: false, token: '', endpoint: '', bearer: '', insecure: false });
});

await check('generation: every GameDig option and the server address count, through Go’s omitempty encoding; name, schedule spacing, destinations, tags and group don’t', () => {
  const input = (extra = {}) => checks.inputOf(monitor(extra));
  assert.deepEqual(checks.checkChanges(input(), input({ name: 'Game 2', intervalMs: 30_000, retryIntervalMs: 5_000, tags: [{ tagId: 't' }], groupId: 'g', notificationIds: ['c'], repeatAfterMs: 60_000 })), []);
  assert.deepEqual(checks.checkChanges(input(), input({ gamedig: { bearerToken: BEARER, allowInsecureHTTP: true, endpoint: STORED.endpoint, token: TOKEN, givenPortOnly: true, port: 7777, game: STORED.game } })), [], 'key order');
  const plain = { game: 'minecraft', endpoint: 'https://h', bearerToken: BEARER };
  assert.deepEqual(checks.checkChanges(input({ gamedig: plain }), input({ gamedig: { ...plain, port: 0, givenPortOnly: false, token: '', allowInsecureHTTP: false } })), [], '0, false and empty equal absent');
  for (const gamedig of [{ ...STORED, game: 'minecraft' }, { ...STORED, port: 7778 }, { ...STORED, givenPortOnly: false }, { ...STORED, token: 'other' }, { ...STORED, endpoint: 'http://127.0.0.1:3122' },
    { ...STORED, bearerToken: `${BEARER}x` }, { ...STORED, allowInsecureHTTP: false }, { ...STORED, futureMode: 1 }]) {
    assert.deepEqual(checks.checkChanges(input(), input({ gamedig })), ['gamedig'], JSON.stringify(gamedig));
  }
  assert.deepEqual(checks.checkChanges(input(), input({ target: 'play2.example.com' })), ['target']);
  assert.deepEqual(checks.checkChanges(input(), input({ timeoutMs: 5000 })), ['timeoutMs']);
  assert.deepEqual(checks.checkChanges(input(), input({ upsideDown: true })), ['upsideDown']);
  assert.deepEqual(checks.checkChangeWords(['target', 'gamedig'], 'gamedig'), ['game server address', 'game, port, game token or query service']);
  assert.deepEqual(checks.checkChangeWords(['target'], 'steam'), ['server address'], 'Steam’s words are unchanged');
});

await check('form: a new GameDig monitor sends a trimmed address and only its gamedig options; an edit keeps stored bytes and the generation; a port edit is a new check', () => {
  const fresh = cloneModel.draftFrom(undefined, ['a']);
  assert.deepEqual(fresh.gamedig, g.gamedigDraftFrom(undefined, ''));
  const draft = { ...fresh, name: 'Game server', type: 'gamedig', gamedig: { ...fresh.gamedig, game: 'minecraft', target: ' play.example.com ', endpoint: ' https://game-query.internal:3121 ', bearer: BEARER } };
  const built = cloneModel.build(draft, undefined, undefined);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(built.input.target, 'play.example.com');
  assert.equal(JSON.stringify(built.input.gamedig), JSON.stringify({ game: 'minecraft', endpoint: 'https://game-query.internal:3121', bearerToken: BEARER }));
  for (const key of OPTION_KEYS) assert.equal(key in built.input, false, key);
  for (const [change, field] of [[{ game: '' }, 'gamedigGame'], [{ target: 'play:25565' }, 'gamedigTarget'], [{ port: '70000' }, 'gamedigPort'], [{ endpoint: 'http://h' }, 'gamedigEndpoint'], [{ bearer: '' }, 'gamedigBearer']]) {
    const refused = cloneModel.build({ ...draft, gamedig: { ...draft.gamedig, ...change } }, undefined, undefined);
    assert.equal(refused.input, undefined, JSON.stringify(change));
    assert.ok(refused.errors[field], `${JSON.stringify(change)}: ${JSON.stringify(refused.errors)}`);
  }
  const m = monitor({ gamedig: { ...STORED, futureMode: 'later' } });
  const edit = cloneModel.draftFrom(m, ['a']);
  assert.deepEqual(edit.gamedig, { ...g.gamedigDraftFrom(STORED, 'play.example.com') });
  const initial = structuredClone(edit);
  const renamed = cloneModel.build({ ...edit, name: 'Game renamed' }, m, initial);
  assert.ok(renamed.input, JSON.stringify(renamed.errors));
  assert.equal(JSON.stringify(renamed.input.gamedig), JSON.stringify(m.gamedig), 'stored bytes, order and a newer node’s key');
  assert.equal(renamed.input.target, m.target);
  assert.deepEqual(renamed.input.futureTop, m.futureTop);
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', ...OPTION_KEYS]) assert.equal(key in renamed.input, false, key);
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), renamed.input), [], 'a rename keeps the check generation');
  const moved = cloneModel.build({ ...edit, gamedig: { ...edit.gamedig, port: '7778' } }, m, initial);
  assert.equal(JSON.stringify(moved.input.gamedig), JSON.stringify({ ...m.gamedig, port: 7778 }));
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), moved.input), ['gamedig']);
  const readdressed = cloneModel.build({ ...edit, gamedig: { ...edit.gamedig, target: ' play2.example.com ' } }, m, initial);
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), readdressed.input), ['target']);
  // A newer node's game this catalogue lacks: a rename keeps it; picking another unknown ID is refused before saving.
  const future = monitor({ gamedig: { ...STORED, game: 'future-game' } });
  const futureDraft = cloneModel.draftFrom(future, ['a']);
  assert.equal(JSON.stringify(cloneModel.build({ ...futureDraft, name: 'Future renamed' }, future, structuredClone(futureDraft)).input?.gamedig), JSON.stringify(future.gamedig));
  assert.ok(cloneModel.build({ ...futureDraft, gamedig: { ...futureDraft.gamedig, game: 'other-future' } }, future, structuredClone(futureDraft)).errors.gamedigGame);
  // Filling the form from pasted JSON (the panel builds the draft over the stored monitor): a shown option is imported.
  const pasted = cloneModel.draftFrom({ ...m, name: 'Imported', gamedig: { ...STORED, port: 9999 } }, ['a']);
  assert.equal(cloneModel.build(pasted, m, initial).input.gamedig.port, 9999);
});

await check('form: switching an HTTP monitor to GameDig drops HTTP, TLS and status options; GameDig options never ride along on another type', () => {
  const http = { ...monitor(), type: 'http', target: 'https://play.example.com/', expectedStatusCodes: [200], http: { method: 'POST', body: 'x' }, tls: { minValidityDays: 3 } };
  delete http.gamedig;
  const draft = cloneModel.draftFrom(http, ['a']);
  const switched = { ...draft, type: 'gamedig', gamedig: { ...draft.gamedig, game: 'minecraft', target: 'play.example.com', endpoint: 'https://h', bearer: BEARER } };
  const built = cloneModel.build(switched, http, cloneModel.draftFrom(http, ['a']));
  assert.ok(built.input, JSON.stringify(built.errors));
  for (const key of OPTION_KEYS) assert.equal(key in built.input, false, key);
  assert.equal(built.input.target, 'play.example.com');
  assert.deepEqual(built.input.gamedig, { game: 'minecraft', endpoint: 'https://h', bearerToken: BEARER });
  const back = cloneModel.build({ ...switched, type: 'http', targets: { http: 'https://play.example.com/' } }, http, cloneModel.draftFrom(http, ['a']));
  assert.equal('gamedig' in (back.input ?? {}), false, 'an HTTP monitor never sends gamedig options');
});

await check('preview: the whole service address, the service token and the game token are masked until shown; a masked copy is caught as a placeholder', () => {
  const m = monitor({ gamedig: { ...STORED, futureSecret: 'future-private', futureMode: 'fast' } });
  const draft = cloneModel.draftFrom(m, ['a']);
  const built = cloneModel.build(draft, m, structuredClone(draft));
  const masked = previewModel.masked(built.input);
  assert.deepEqual(masked.gamedig, { game: 'terrariatshock', port: 7777, givenPortOnly: true, token: MASK, endpoint: MASK, bearerToken: MASK, allowInsecureHTTP: true, futureSecret: MASK, futureMode: 'fast' });
  const text = JSON.stringify(masked);
  for (const value of [TOKEN, BEARER, '127.0.0.1:3121', 'future-private']) assert.equal(text.includes(value), false, value);
  assert.equal(fmt.maskedPath(JSON.parse(text)), 'gamedig.token', 'an import of the masked copy is refused');
  assert.equal(fmt.maskedPath({ gamedig: g.gamedigMasked({ game: 'minecraft', endpoint: 'https://h', bearerToken: BEARER }) }), 'gamedig.endpoint');
  assert.equal(JSON.stringify(built.input.gamedig), JSON.stringify(m.gamedig), 'the input itself is untouched');
  assert.deepEqual(g.gamedigMasked({ game: 'minecraft', endpoint: 'https://h', bearerToken: BEARER }), { game: 'minecraft', endpoint: MASK, bearerToken: MASK }, 'no token key appears');
  assert.equal(g.gamedigMasked(undefined), undefined);
  assert.ok(source('views/MonitorForm.tsx').includes("{input?.gamedig ? ' The whole game query service address, its token and the game token are masked too until you show credentials.' : ''}"));
  assert.ok(source('views/MonitorDetail.tsx').includes('m.gamedig?.endpoint || m.gamedig?.bearerToken || m.gamedig?.token'), 'the details page offers to reveal them');
});

const factsOf = (m, opts) => Object.fromEntries(checks.checkFacts(m, opts).map(fact => [fact.label, html(fact.value)]));
await check('facts: editors see where the query runs, the game and port, hidden credentials until revealed and the three outcomes; viewers see no options', () => {
  const m = monitor();
  const masked = factsOf(m, { viewer: false, reveal: false });
  for (const value of Object.values(masked)) for (const secret of SECRETS) assert.equal(value.includes(secret), false, secret);
  assert.ok(masked['Queried through'].includes('Your GameDig query service, never the node itself') && masked['Queried through'].includes('Terraria - TShock (terrariatshock)'));
  assert.ok(masked['Queried through'].includes('play.example.com') && masked['Queried through'].includes('7777') && masked['Queried through'].includes(' only') && masked['Queried through'].includes('GameDig 5.3.3’s own code'));
  assert.ok(masked.Game.includes('Terraria - TShock') && masked.Game.includes('only this port'));
  assert.ok(masked['Game token'].includes('set · hidden') && masked['Query service token'].includes('set · hidden') && masked['Query service token'].includes('never to the game server'));
  assert.ok(masked['Query service'].includes('(set · hidden)') && masked['Query service'].includes('plain HTTP, so both tokens and the server address travel unencrypted'));
  assert.ok(masked['Up when'].includes('reply') && masked['Down when'].includes('query of the server failed'));
  for (const said of ['can’t be reached', 'certificate the node doesn’t trust', 'refuses the service token', 'already answering two queries', 'another GameDig version', 'can’t load GameDig',
    'runs out of memory', 'two game queries are already running on this node', 'the check is cancelled', 'within the timeout', 'Pending is never inverted and never recovers an outage.']) {
    assert.ok(masked['Pending when'].includes(said), said);
  }
  assert.ok(masked.Latency.includes('measured by the query service') && masked.Latency.includes('not by the node'));
  for (const said of Object.values(g.GAMEDIG_SUMMARY)) assert.ok(masked.Results.includes(said), said);
  assert.deepEqual(g.GAMEDIG_SUMMARY, { up: 'Game server replied', down: 'Game server query failed', pending: 'Game query service unavailable' });
  const revealed = factsOf(m, { viewer: false, reveal: true });
  assert.ok(revealed['Query service'].includes('http://127.0.0.1:3121') && revealed['Query service token'].includes(BEARER) && revealed['Game token'].includes(esc(TOKEN)));
  const https = factsOf(monitor({ gamedig: { game: 'minecraft', endpoint: 'https://game-query.internal:3121', bearerToken: BEARER } }), { viewer: false, reveal: false });
  assert.ok(https['Query service'].includes('HTTPS, verified against the node’s system trust') && https['Game token'].includes('None') && https.Game.includes('default port') && https.Game.includes('25565'));
  assert.ok(factsOf(monitor({ gamedig: { game: 'alienarena', endpoint: 'https://h', bearerToken: BEARER } }), { viewer: false, reveal: false }).Game.includes('no port set, and the catalogue lists no default'));
  assert.ok(factsOf(monitor({ gamedig: { ...STORED, game: 'future-game' } }), { viewer: false, reveal: false }).Game.includes('not in this dashboard’s GameDig 5.3.3 catalogue'));
  for (const m2 of [{ ...m, gamedig: undefined }, m]) {
    const viewer = factsOf(m2, { viewer: true, reveal: true });
    assert.equal(viewer['Game, port and query service'], '<span class="muted">Hidden from viewers</span>');
    assert.equal('Game token' in viewer || 'Query service' in viewer || 'Game' in viewer, false);
    assert.ok(viewer['Queried through'].includes('play.example.com'), 'the server address is the target, visible to viewers');
    for (const value of Object.values(viewer)) for (const secret of [...SECRETS, 'terrariatshock']) assert.equal(value.includes(secret), false, secret);
  }
});

await check('resolver: the check row owns gamedig options, projects them byte for byte in both modes, and masks them unless revealed', () => {
  const m = monitor({ gamedig: { bearerToken: BEARER, endpoint: STORED.endpoint, allowInsecureHTTP: true, game: 'terrariatshock', token: TOKEN, givenPortOnly: true, port: 7777 } });
  const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() } };
  for (const reveal of [false, true]) {
    const rows = resolverModel.monitorRows(ctx, reveal);
    const projected = Object.assign({}, ...rows.map(row => row.project(m)));
    assert.equal(JSON.stringify(projected.gamedig), JSON.stringify(m.gamedig), `reveal ${reveal}`);
    assert.equal(projected.target, m.target);
    const row = rows.find(r => r.key === 'check');
    assert.ok(row.keys.includes('gamedig'));
    const markup = html(row.render(m));
    assert.ok(markup.includes('queried through a GameDig service') && markup.includes('terrariatshock') && markup.includes(', port 7777 only'), markup);
    if (reveal) {
      assert.ok(markup.includes(BEARER) && markup.includes('127.0.0.1:3121'), 'revealed options show the address and tokens');
      assert.equal(markup.includes('hidden'), false);
    } else {
      for (const secret of SECRETS) assert.equal(markup.includes(secret), false, secret);
      assert.ok(markup.includes(MASK) && markup.includes('service address and tokens hidden'));
    }
    const other = withOtherFields(rows, [m], reveal, false).find(r => r.key === '__other');
    assert.deepEqual(other?.keys, ['futureTop'], 'gamedig is not left to the unmasked other-fields row');
  }
  const viewerRows = resolverModel.monitorRows({ ...ctx, role: 'viewer' }, false);
  const viewerMarkup = html(viewerRows.find(r => r.key === 'check').render({ ...m, gamedig: undefined }));
  assert.ok(viewerMarkup.includes('queried through a GameDig service') && !viewerMarkup.includes('hidden') && !viewerMarkup.includes('terrariatshock'));
});

await check('clone: the copy carries every GameDig setting, private ones included, deep-copied; its create body keeps them byte for byte and the source never changes', () => {
  const src = monitor({ gamedig: { ...STORED, futureMode: { nested: [1] } } });
  const before = JSON.stringify(src);
  const copy = checks.cloneInput(src, () => 'f'.repeat(64));
  assert.equal(copy.name, 'Game server (copy)');
  assert.equal(JSON.stringify(copy.gamedig), JSON.stringify(src.gamedig));
  assert.notEqual(copy.gamedig, src.gamedig);
  for (const key of ['id', 'revision', 'updatedAt', 'checkGeneration', 'push']) assert.equal(key in copy, false, key);
  copy.gamedig.futureMode.nested.push(2);
  assert.equal(JSON.stringify(src), before, 'the source is unchanged');
  const fresh = checks.cloneInput(src, () => 'f'.repeat(64));
  const draft = cloneModel.draftFrom(fresh, ['a']);
  const built = cloneModel.build(draft, fresh, structuredClone(draft));
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.equal(JSON.stringify(built.input.gamedig), JSON.stringify(src.gamedig));
  assert.equal(built.input.target, src.target);
  assert.equal('revision' in built.input || 'id' in built.input, false, 'a create body');
});

await check('rendered fields: what runs where, the 358-game picker, masked private inputs, no free-form GameDig options, and the plain-HTTP and default-port notes', () => {
  const render = (d, extra = {}) => html(React.createElement(GameDigFields, { d, set() {}, errors: {}, disabled: false, storedGame: undefined, storedToken: false, timeoutMs: 4000, ...extra }));
  const stored = g.gamedigDraftFrom(STORED, 'play.example.com');
  const markup = render(stored, { storedGame: 'terrariatshock', storedToken: true });
  for (const said of ['Each check asks your GameDig query service to query the game server.', 'GameDig 5.3.3’s own code for the chosen game runs in the service', 'Pending is never inverted and never recovers an outage.',
    'No other GameDig options, scripts or modules can be set.', 'Canary doesn’t include GameDig.', 'Dockerfile.game-query', 'answers 2 queries at a time and refuses more rather than queueing them',
    'anyone holding its token can make it send game queries to any address its network reaches', 'catalogue of 358 game IDs', 'Canary hasn’t tested every game in this list.', 'Default port: 7777.',
    'Game server address', 'Only query this port', 'three quarters of the timeout (3 s)', 'This address isn’t encrypted (http://)', 'Not a node or cluster token.', 'follows no redirects']) {
    assert.ok(markup.includes(said), said);
  }
  const fields = inputs(markup);
  const secretInputs = fields.filter(input => input.type === 'password');
  assert.equal(secretInputs.length, 3, 'game token, service address and service token');
  assert.deepEqual(secretInputs.map(input => input.value).sort(), [BEARER, STORED.endpoint, esc(TOKEN)].sort());
  assert.ok(fields.some(input => input.type === 'search'), 'a search narrows the game list');
  assert.equal(/<textarea/u.test(markup), false, 'no free-form options');
  const options = [...markup.matchAll(/<option\b[^>]*>/gu)];
  assert.equal(options.length, 358);
  assert.ok(/<option value="terrariatshock" selected="">/u.test(markup), 'the stored game is selected');
  assert.equal(markup.includes('lists no default port'), false);
  const fresh = render(g.gamedigDraftFrom(undefined, ''));
  assert.ok(fresh.includes('<option value="" selected="">Choose a game…</option>') && [...fresh.matchAll(/<option\b/gu)].length === 359);
  assert.equal(fresh.includes('Saving removes the stored value'), false);
  assert.equal(fresh.includes('This address isn’t encrypted'), false);
  assert.ok(render({ ...stored, game: 'alienarena' }).includes('GameDig’s catalogue lists no default port for this game'));
  const future = render({ ...stored, game: 'future-game' }, { storedGame: 'future-game' });
  assert.ok(future.includes('future-game (not in this dashboard’s catalogue)') && future.includes('Kept as stored'));
  const httpsFlag = render({ ...stored, endpoint: 'https://game-query.internal:3121' });
  assert.ok(httpsFlag.includes('This address is https://, so it doesn’t need it.') && !httpsFlag.includes('This address isn’t encrypted'));
  const failed = render(stored, { errors: { gamedigGame: 'Choose the game this server runs.', gamedigEndpoint: 'Use https://, or allow plain HTTP below' } });
  assert.ok(failed.includes('Choose the game this server runs.') && failed.includes('aria-invalid="true"') && failed.includes('Use https://, or allow plain HTTP below'));
  assert.ok(render(stored, { storedToken: true, d: { ...stored, token: '' } }).includes('Saving removes the stored value'), 'an emptied stored game token is removed on save');
});

// Root's r5 admission change: a catalogue game without a default port needs an explicit nonzero port, even without
// "only this port", so a missing default is refused when saved instead of running as an outage. The service's decoder
// refuses the same query. A stored ID this catalogue doesn't list stays the node's decision.
await check('missing catalogue default: the form refuses an omitted or 0 port exactly where the node and the service do, and says so', () => {
  const go = goSource('gamedigmonitor/check.go');
  assert.match(go, /c\.Port == 0 && games\[c\.Game\] == 0/u, 'the node refuses an omitted port without a catalogue default');
  const service = readFileSync(resolve(root, 'services/game-query/server.mjs'), 'utf8');
  assert.match(service, /!input\.port && !games\.get\(input\.game\)/u, 'the query service refuses the same query');
  const missing = g.GAMEDIG_GAMES.filter(game => !game.defaultPort);
  assert.equal(missing.length, 71, 'the pinned 5.3.3 catalogue lists 71 IDs without a default port');
  for (const game of g.GAMEDIG_GAMES) {
    const refused = !game.defaultPort;
    for (const port of ['', '0', '00', ' ']) {
      const problem = g.gamedigPortProblem(port, false, game.id);
      assert.equal(Boolean(problem), refused, `${game.id} ${JSON.stringify(port)}`);
      if (refused) assert.ok(problem.includes(`lists no default port for ${game.name}`), problem);
    }
    assert.equal(g.gamedigPortProblem('12345', false, game.id), undefined, `${game.id}: an explicit port satisfies it`);
    assert.equal(g.gamedigPortProblem('12345', true, game.id), undefined);
    assert.equal(g.gamedigNeedsPort(game.id), refused, game.id);
  }
  assert.ok(g.gamedigPortProblem('', true, 'alienarena').includes('Enter the port to query only that port'), 'the only-this-port message comes first');
  assert.equal(g.gamedigPortProblem('', false, 'future-game'), undefined, 'an ID this catalogue lacks: the node decides');
  assert.equal(g.gamedigNeedsPort('future-game'), false);
  assert.equal(g.gamedigPortProblem('', false), undefined, 'without a game the old two-argument rule is unchanged');
  const draft = { ...g.gamedigDraftFrom(undefined, 'play.example.com'), endpoint: 'https://h', bearer: BEARER };
  let errors = {};
  const omitted = g.buildGameDig(undefined, { ...draft, game: 'alienarena' }, errors);
  assert.deepEqual(Object.keys(errors), ['gamedigPort']);
  assert.equal('port' in omitted.value, false, 'refused, and sent as the node would refuse it');
  errors = {};
  assert.equal(g.buildGameDig(undefined, { ...draft, game: 'alienarena', port: '0' }, errors).value.port, undefined);
  assert.deepEqual(Object.keys(errors), ['gamedigPort']);
  errors = {};
  assert.equal(g.buildGameDig(undefined, { ...draft, game: 'alienarena', port: ' 27901 ' }, errors).value.port, 27901);
  assert.deepEqual(errors, {});
  errors = {};
  g.buildGameDig(undefined, { ...draft, game: 'minecraft' }, errors);
  assert.deepEqual(errors, {}, 'a game with a default still takes an empty port');
  // A future node's stored options save unchanged: its game ID isn't judged here, and nothing else moves.
  const future = { game: 'future-game', endpoint: 'https://game-query.internal:3121', bearerToken: BEARER, futureMode: { nested: [1] } };
  errors = {};
  const kept = g.buildGameDig(future, g.gamedigDraftFrom(future, 'play.example.com'), errors);
  assert.deepEqual(errors, {});
  assert.equal(JSON.stringify(kept.value), JSON.stringify(future));
  const render = d => html(React.createElement(GameDigFields, { d, set() {}, errors: {}, disabled: false, storedGame: undefined, storedToken: false, timeoutMs: 4000 }));
  const required = render({ ...draft, game: 'alienarena' });
  assert.ok(required.includes('Required for this game: it has no default port in GameDig’s catalogue.'));
  assert.ok(/<label[^>]*>Game port<\/label>/u.test(required), 'no “(optional)” on the port for this game');
  assert.ok(render({ ...draft, game: 'minecraft' }).includes('Game port (optional)'));
  const factsOf = m => html(g.gamedigFacts(m, false, false).map((fact, index) => React.createElement(React.Fragment, { key: index }, fact.value)));
  const factsText = game => factsOf(monitor({ gamedig: { game, endpoint: 'https://h', bearerToken: BEARER } }));
  assert.ok(factsText('alienarena').includes('the node refuses to save this setting, so add the server’s port'));
  assert.ok(factsText('minecraft').includes('default port <span class="mono">25565</span>'));
  assert.ok(factsOf(monitor()).includes('a server name that doesn’t resolve'), 'a DNS failure is a completed failed query: down');
});

await check('no storage, network or clock-based state: loading, building and rendering touched nothing', () => {
  assert.deepEqual(touched, []);
  assert.deepEqual(requests, []);
});

console.log(failed ? `${failed} check group(s) failed` : 'all GameDig checks passed');
process.exit(failed ? 1 : 0);
