// Pure protocol-19 checks for the network options the monitor form edits: DNS SRV, SOA and CAA expectations and the
// SOA/CAA resolver requirement (apps/web/src/networkOptions.ts, mirroring internal/checks/dns_extended.go and validate.go
// as read), WebSocket subprotocol offers and OAuth2 client credentials, TLS on a TCP check, and the private monitor
// description (canary/validation.go statusText, 8,192 bytes). It drives the real form builder through the cloneModel
// and previewModel seams, the facts in checks.tsx and the Configuration resolver through resolverModel. Imports the
// source in place through one tsx namespace: no copy, scratch, network, browser, DNS server or output files; storage and
// fetch are stand-ins that record any use, and none is expected. Root's contract harness sends the same kinds of cases to
// a real, paused node. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/network-options-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
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
const scope = register({ namespace: 'network-options-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const n = await load('networkOptions.ts');
const checks = await load('checks.tsx');
const { cloneModel, previewModel } = await load('views/MonitorForm.tsx');
const { resolverModel } = await load('views/Config.tsx');

let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const ch = code => String.fromCodePoint(code);
const LONE = String.fromCharCode(0xd800);
const MASK = ch(0x2022).repeat(3);
const goSource = file => readFileSync(resolve(root, 'apps/node-go/internal', file), 'utf8').replace(/\r\n/gu, '\n');
const html = node => renderToStaticMarkup(React.createElement(React.Fragment, null, node));
const factsHtml = (m, viewer, reveal) => html(checks.checkFacts(m, { viewer, reveal }).map((fact, index) => React.createElement(React.Fragment, { key: index }, React.createElement('b', null, fact.label), ': ', fact.value, '; ')));
const fresh = (type, target, extra = {}) => ({ ...cloneModel.draftFrom(undefined, ['a']), name: `Review ${type}`, type, targets: { [type]: target }, ...extra });
const create = draft => cloneModel.build(draft, undefined, undefined);
const stored = extra => ({ id: 'm-net', revision: 'r1', updatedAt: 1, checkGeneration: 'g1', name: 'Net', intervalMs: 60_000, timeoutMs: 5000, nodeIds: ['a'], enabled: true, ...extra });
/** A stored monitor renamed through the form: draft, initial copy, build. */
const rename = m => { const d = cloneModel.draftFrom(m, ['a']); const initial = structuredClone(d); d.name += ' renamed'; return { d, built: cloneModel.build(d, m, initial) }; };
const editWith = (m, change) => { const d = cloneModel.draftFrom(m, ['a']); const initial = structuredClone(d); return cloneModel.build({ ...d, ...change(d) }, m, initial); };

// ---------- DNS

await check('DNS record types: the form offers the node’s ten, in its order, and SOA and CAA need a resolver exactly as the node requires', () => {
  const go = goSource('checks/validate.go');
  const line = go.split('\n').find(text => text.includes('rt != "A"'));
  assert.ok(line, 'the node’s record-type rule');
  assert.deepEqual([...line.matchAll(/rt != "([A-Z]+)"/gu)].map(match => match[1]), [...checks.DNS_RECORDS]);
  assert.deepEqual([...checks.DNS_RECORDS], ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'PTR', 'SRV', 'SOA', 'CAA']);
  assert.match(go, /\(rt == "SOA" \|\| rt == "CAA"\) && o\.Server == ""/u);
  for (const record of checks.DNS_RECORDS) assert.equal(n.dnsNeedsServer(record), record === 'SOA' || record === 'CAA', record);
  assert.deepEqual([...n.DNS_EXTENDED], ['SRV', 'SOA', 'CAA']);
  assert.match(goSource('checks/dns.go'), /case "SRV", "SOA", "CAA":\n\t\treturn canonicalExtendedRecord\(recordType, s\)/u);
  assert.match(goSource('checks/dns.go'), /resolver\.LookupSRV\(ctx, "", "", c\.Target\)/u, 'SRV uses the system resolver unless one is set');
});

const SRV_OK = {
  '10 5 5060 sip.example.com': '10 5 5060 sip.example.com', '010 05 5060 SIP.Example.COM.': '10 5 5060 sip.example.com', '0 0 0 .': '0 0 0 .',
  '65535 65535 65535 _sip._tcp.example': '65535 65535 65535 _sip._tcp.example', [`10\t5   5060 x.example`]: '10 5 5060 x.example',
  [`10${ch(0xa0)}5 5060 x.example`]: '10 5 5060 x.example', // Go's strings.Fields splits on Unicode spaces, NBSP included
};
const SRV_BAD = ['10 5 5060', '10 5 5060 a b', '0 0 65536 host.invalid', '0 -1 80 host.invalid', '+1 0 0 x.example', '1.5 0 0 x.example', '0x10 0 0 x.example', '1 0 0 -bad.example',
  `1 0 0 caf${ch(0xe9)}.example`, '1 0 0 ..', `1 0 0 ${'a'.repeat(64)}.example`, `1 0 0 ${'a.'.repeat(127)}aa`, ''];
await check('SRV expectations: four fields, three 16-bit numbers and a target, canonical as on the node (root’s Go refusals included)', () => {
  for (const [value, canonical] of Object.entries(SRV_OK)) {
    assert.equal(n.canonicalExtended('SRV', value), canonical, JSON.stringify(value));
    assert.equal(n.extendedRecordProblem('SRV', value), undefined, JSON.stringify(value));
  }
  for (const value of SRV_BAD) {
    assert.equal(n.canonicalExtended('SRV', value), null, JSON.stringify(value));
    assert.ok(n.extendedRecordProblem('SRV', value), JSON.stringify(value));
  }
  assert.ok(n.extendedRecordProblem('SRV', '10 5 5060').includes('priority weight port target'));
  assert.ok(n.extendedRecordProblem('SRV', '0 0 65536 host.invalid').includes('0 to 65,535'));
});

await check('SOA expectations: seven fields, two names and five 32-bit numbers, canonical as on the node', () => {
  const ok = {
    'ns1.example.com hostmaster.example.com 2026102401 7200 3600 1209600 300': 'ns1.example.com hostmaster.example.com 2026102401 7200 3600 1209600 300',
    'NS1.Example.COM. Hostmaster.Example.COM. 0001 0 0 0 4294967295': 'ns1.example.com hostmaster.example.com 1 0 0 0 4294967295',
    '. . 0 0 0 0 0': '. . 0 0 0 0 0',
  };
  for (const [value, canonical] of Object.entries(ok)) assert.equal(n.canonicalExtended('SOA', value), canonical, value);
  for (const value of ['ns.invalid host.invalid 4294967296 1 1 1 1', 'ns.invalid host.invalid 1 1 1 1', 'ns.invalid host.invalid 1 1 1 1 1 1', 'ns_bad- host.invalid 1 1 1 1 1', 'ns.invalid host.invalid -1 1 1 1 1']) {
    assert.equal(n.canonicalExtended('SOA', value), null, value);
    assert.ok(n.extendedRecordProblem('SOA', value), value);
  }
  assert.ok(n.extendedRecordProblem('SOA', 'ns.invalid host.invalid 4294967296 1 1 1 1').includes('4,294,967,295'));
});

await check('CAA expectations: flags, an alphanumeric tag compared in lowercase, and a quoted or unquoted value, canonical as on the node', () => {
  const ok = {
    '0 ISSUE letsencrypt.org': '0 issue "letsencrypt.org"', // root's Go test: the exact canonical presentation
    '0 issue "letsencrypt.org"': '0 issue "letsencrypt.org"', '128 IssueWild ";"': '128 issuewild ";"', '0 iodef mailto:security@example.com': '0 iodef "mailto:security@example.com"',
    '0 issue ""': '0 issue ""', '0 tbs unquoted text with spaces': '0 tbs "unquoted text with spaces"', [`0 issue "${'\\u00e9'}"`]: `0 issue "${ch(0xe9)}"`, [`  0   issue   "a b"  `]: '0 issue "a b"',
    [`0 ${'a'.repeat(15)} "x"`]: `0 ${'a'.repeat(15)} "x"`, [`0 issue ${'v'.repeat(8192)}`]: `0 issue "${'v'.repeat(8192)}"`,
  };
  for (const [value, canonical] of Object.entries(ok)) assert.equal(n.canonicalExtended('CAA', value), canonical, value.slice(0, 40));
  for (const value of ['256 issue "value"', '0 bad-tag "value"', '0 issue "unfinished', '0 issue "a" "b"', '0 issue', `0 ${'a'.repeat(16)} "x"`, `0 issue ${'v'.repeat(8193)}`, `0 issue "${'v'.repeat(8193)}"`, '-1 issue "x"', '0 is_sue "x"']) {
    assert.equal(n.canonicalExtended('CAA', value), null, value.slice(0, 40));
    assert.ok(n.extendedRecordProblem('CAA', value), value.slice(0, 40));
  }
  assert.ok(n.extendedRecordProblem('CAA', '0 bad-tag "value"').includes('1 to 15 ASCII letters or digits'));
  const go = goSource('checks/dns_extended.go');
  assert.match(go, /len\(fields\[1\]\) < 1 \|\| len\(fields\[1\]\) > 15/u);
  assert.match(go, /len\(value\) > 8192/u);
});

await check('form: a DNS check of SOA or CAA needs a resolver; extended expectations use the node’s rules; old types keep their checks; stored bytes survive a rename', () => {
  let built = create(fresh('dns', 'example.com', { dnsRecord: 'SOA' }));
  assert.ok(built.errors.dnsServer?.includes('SOA checks ask one resolver directly'), JSON.stringify(built.errors));
  built = create(fresh('dns', 'example.com', { dnsRecord: 'CAA', dnsServer: '1.1.1.1:53', dnsExpected: '0 issue "letsencrypt.org"\n0 ISSUE pki.goog' }));
  assert.deepEqual(built.errors, {});
  assert.equal(JSON.stringify(built.input.dns), JSON.stringify({ recordType: 'CAA', server: '1.1.1.1:53', expected: ['0 issue "letsencrypt.org"', '0 ISSUE pki.goog'] }), 'sent as typed: the node compares canonical forms');
  built = create(fresh('dns', '_sip._tcp.example.com', { dnsRecord: 'SRV', dnsExpected: '10 5 5060 sip.example.com' }));
  assert.deepEqual(built.errors, {}, 'SRV may use the system resolver');
  assert.equal(built.input.target, '_sip._tcp.example.com');
  assert.equal('server' in built.input.dns, false);
  built = create(fresh('dns', 'example.com', { dnsRecord: 'SRV', dnsExpected: '10 5 5060' }));
  assert.ok(built.errors.dnsExpected?.includes('priority weight port target'));
  built = create(fresh('dns', 'example.com', { dnsRecord: 'SOA', dnsServer: '1.1.1.1:53', dnsExpected: `ns1.example.com ${'h'.repeat(9000)} 1 1 1 1 1` }));
  assert.ok(built.errors.dnsExpected?.includes('8,192 bytes'));
  built = create(fresh('dns', 'example.com', { dnsRecord: 'A', dnsExpected: '192.0.2.300' }));
  assert.ok(built.errors.dnsExpected?.includes('an A address'), 'the existing A rule is unchanged');
  assert.equal('dnsServer' in create(fresh('dns', 'example.com', { dnsRecord: 'A' })).errors, false, 'other types still use the system resolver');
  for (const dns of [{ recordType: 'A', server: '127.0.0.1:9999', expected: ['127.0.0.1'] }, { recordType: 'SRV', expected: ['010 05 5060 SIP.Example.COM.'] }, { recordType: 'CAA', server: '[2001:db8::53]:53', expected: ['0 ISSUE letsencrypt.org'], futureFlag: { x: [1] } }]) {
    const m = stored({ type: 'dns', target: 'example.com', dns });
    const { built: renamed } = rename(m);
    assert.ok(renamed.input, JSON.stringify(renamed.errors));
    assert.equal(JSON.stringify(renamed.input.dns), JSON.stringify(dns), 'byte for byte, unknown keys included');
    assert.deepEqual(checks.checkChanges(checks.inputOf(m), renamed.input), []);
  }
  const facts = factsHtml(stored({ type: 'dns', target: 'example.com', dns: { recordType: 'SOA' } }), false, false);
  assert.ok(facts.includes('SOA checks need an explicit resolver, so the node refuses this setting'), 'a stored SOA without a resolver is named, not hidden');
});

// ---------- WebSocket

await check('WebSocket subprotocols: at most 16 distinct HTTP tokens of at most 128 bytes, case-sensitive; requiring one needs an offer (root’s Go refusals mirrored)', () => {
  const go = goSource('checks/validate.go');
  assert.match(go, /len\(o\.Subprotocols\) > 16 \|\| o\.RequireSubprotocol && len\(o\.Subprotocols\) == 0/u);
  assert.match(go, /len\(protocol\) > 128 \|\| !validToken\(protocol\) \|\| protocols\[protocol\]/u);
  assert.deepEqual(n.parseSubprotocols(' chat, v2  graphql-transport-ws,,'), ['chat', 'v2', 'graphql-transport-ws']);
  assert.equal(n.subprotocolsText(['chat', 'v2']), 'chat, v2');
  assert.deepEqual(n.parseSubprotocols(n.subprotocolsText(['a', "b!#$%&'*+-.^_`|~9"])), ['a', "b!#$%&'*+-.^_`|~9"], 'stored offers round-trip');
  for (const list of [[], ['chat'], ['chat', 'Chat'], Array.from({ length: 16 }, (_, i) => `p${i}`), ['a'.repeat(128)]]) assert.equal(n.subprotocolProblem(list, false), undefined, JSON.stringify(list));
  for (const [list, required, said] of [[['chat', 'chat'], false, 'listed twice'], [Array.from({ length: 17 }, (_, i) => `p${i}`), false, 'at most 16'], [['a'.repeat(129)], false, 'longer than 128'],
    [['chat"v2'], false, 'isn’t a valid subprotocol name'], [['v2/chat'], false, 'isn’t a valid subprotocol name'], [[`caf${ch(0xe9)}`], false, 'isn’t a valid subprotocol name'], [[], true, 'Require one of them']]) {
    assert.ok(n.subprotocolProblem(list, required)?.includes(said), `${JSON.stringify(list).slice(0, 40)}: ${n.subprotocolProblem(list, required)}`);
  }
  assert.equal(n.subprotocolProblem(['chat'], true), undefined);
  const made = create(fresh('websocket', 'wss://stream.example.com/socket', { wsProtocols: 'graphql-transport-ws, v2', wsRequire: true }));
  assert.deepEqual(made.errors, {});
  assert.equal(JSON.stringify(made.input.websocket), JSON.stringify({ subprotocols: ['graphql-transport-ws', 'v2'], requireSubprotocol: true }));
  assert.equal(create(fresh('websocket', 'wss://stream.example.com/socket')).input.websocket, undefined, 'no offers and no other option: no websocket object, as before');
  assert.ok(create(fresh('websocket', 'wss://stream.example.com/socket', { wsRequire: true })).errors.wsProtocols?.includes('Require one of them'));
  const m = stored({ type: 'websocket', target: 'wss://stream.example.com/socket', websocket: { bearerToken: 'ws-review-token', subprotocols: ['chat', 'v2'], requireSubprotocol: true, futureFrame: [1] } });
  const { built } = rename(m);
  assert.equal(JSON.stringify(built.input.websocket), JSON.stringify(m.websocket));
  const cleared = editWith(m, () => ({ wsProtocols: '', wsRequire: false }));
  assert.deepEqual(cleared.input.websocket, { bearerToken: 'ws-review-token', futureFrame: [1] }, 'the other options and unknown keys stay');
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), cleared.input), ['websocket'], 'changing the offers is a new check');
  const facts = factsHtml(m, false, false);
  assert.ok(facts.includes('the server must choose one of them') && facts.includes('chat'));
  assert.ok(factsHtml(stored({ type: 'websocket', target: 'wss://h/s' }), false, false).includes('None offered; a server that chooses one fails the check'));
});

const OAUTH_SECRET = 'ws-review-oauth-client-secret';
await check('WebSocket OAuth2: one more exclusive authentication, wss:// unless plain HTTP is allowed, its secret masked in previews and never in a viewer’s facts', () => {
  const go = goSource('checks/validate.go');
  assert.match(go, /o\.OAuth2 != nil && !o\.OAuth2\.AllowInsecureHTTP && !secure/u);
  assert.match(goSource('checks/websocket.go'), /oauth2Token\(ctx, HTTPConfig\{OAuth2: o\.OAuth2, ProxyURL: o\.ProxyURL, UseEnvironmentProxy: o\.UseEnvironmentProxy\}\)/u, 'the grant uses the same proxy setting');
  const base = fresh('websocket', 'wss://stream.example.com/socket');
  const oauth = { ...base.wsOauth, tokenUrl: 'https://auth.example.com/token', clientId: 'ws-client', clientSecret: OAUTH_SECRET };
  let built = create({ ...base, ws: { ...base.ws, auth: 'oauth2' }, wsOauth: oauth });
  assert.deepEqual(built.errors, {});
  assert.deepEqual(built.input.websocket, { oauth2: { tokenUrl: 'https://auth.example.com/token', clientId: 'ws-client', clientSecret: OAUTH_SECRET } });
  built = create({ ...base, targets: { websocket: 'ws://stream.example.com/socket' }, ws: { ...base.ws, auth: 'oauth2' }, wsOauth: oauth });
  assert.ok(built.errors.wsOauth?.includes('wss:// for the monitored URL'), JSON.stringify(built.errors));
  assert.equal('oauth' in built.errors, false, 'the HTTP check’s OAuth2 field is untouched');
  built = create({ ...base, targets: { websocket: 'ws://stream.example.com/socket' }, ws: { ...base.ws, auth: 'oauth2' }, wsOauth: { ...oauth, insecure: true } });
  assert.deepEqual(built.errors, {});
  assert.equal(built.input.websocket.oauth2.allowInsecureHttp, true);
  built = create({ ...base, ws: { ...base.ws, auth: 'oauth2', headers: [{ id: 1, name: 'Authorization', value: 'Bearer x' }] }, wsOauth: oauth });
  assert.ok(built.errors.wsAuth?.includes('only one source'), JSON.stringify(built.errors));
  built = create({ ...base, ws: { ...base.ws, auth: 'oauth2' }, wsOauth: { ...oauth, clientId: '' } });
  assert.ok(built.errors.wsOauth?.includes('client ID'));
  // Choosing a bearer token drops the stored grant, so only one source is ever sent.
  const m = stored({ type: 'websocket', target: 'wss://stream.example.com/socket', websocket: { oauth2: { tokenUrl: 'https://auth.example.com/token', clientId: 'ws-client', clientSecret: OAUTH_SECRET, futureGrant: { x: 1 } }, subprotocols: ['chat'] } });
  assert.equal(cloneModel.draftFrom(m, ['a']).ws.auth, 'oauth2');
  assert.equal(JSON.stringify(rename(m).built.input.websocket), JSON.stringify(m.websocket), 'a rename keeps the grant byte for byte, unknown keys included');
  const bearer = editWith(m, d => ({ ws: { ...d.ws, auth: 'bearer', bearer: 'ws-bearer' } }));
  assert.deepEqual(bearer.input.websocket, { subprotocols: ['chat'], bearerToken: 'ws-bearer' });
  const preview = previewModel.masked(rename(m).built.input);
  assert.equal(preview.websocket.oauth2.clientSecret, MASK);
  assert.equal(preview.websocket.oauth2.clientId, 'ws-client');
  assert.equal(JSON.stringify(preview).includes(OAUTH_SECRET), false);
  const editor = factsHtml(m, false, false);
  assert.ok(editor.includes('OAuth2 token endpoint') && editor.includes('ws-client') && editor.includes('set · hidden'));
  assert.equal(editor.includes(OAUTH_SECRET), false);
  // The node's viewer projection is the authority; even if it sent the grant, a viewer's facts never render it.
  const viewer = factsHtml(m, true, false);
  for (const value of [OAUTH_SECRET, 'ws-client', 'auth.example.com', 'OAuth2 token endpoint']) assert.equal(viewer.includes(value), false, value);
  assert.ok(viewer.includes('Hidden from viewers'));
  const ctx = { groupMap: new Map(), role: 'admin', channels: { map: new Map() }, tags: { map: new Map() }, monitorMap: new Map(), endpoint: 'a' };
  const row = resolverModel.monitorRows(ctx, false).find(r => r.key === 'check');
  assert.equal(JSON.stringify(row.project(m).websocket), JSON.stringify(m.websocket), 'a conflict resolution keeps the grant');
  assert.equal(html(row.render(m)).includes(OAUTH_SECRET), false, 'the resolver masks it unless revealed');
});

// ---------- TCP with TLS

await check('TCP: plain by default; TLS turns on a verified handshake, sent as a TLS object (an empty one included), and only then do TLS settings apply', () => {
  assert.match(goSource('checks/tls.go'), /if c\.Type == TCP && c\.TLS == nil \{\n\t\treturn up\("TCP connection established"\)/u);
  assert.match(goSource('checks/validate.go'), /secure = c\.Type == TLS \|\| c\.Type == TCP && c\.TLS != nil/u);
  assert.equal(checks.tlsApplies('tcp', 'db.example.net:5432', undefined), false, 'the three-argument call is unchanged');
  assert.equal(checks.tlsApplies('tcp', 'db.example.net:5432', undefined, true), true);
  assert.equal(checks.tlsApplies('tls', 'h:443', undefined, false), true);
  let built = create(fresh('tcp', 'db.example.net:5432'));
  assert.deepEqual(built.errors, {});
  assert.equal('tls' in built.input, false);
  built = create(fresh('tcp', 'db.example.net:5432', { tcpTls: true }));
  assert.equal(JSON.stringify(built.input.tls), '{}', 'an empty TLS object means TLS with the defaults');
  built = create(fresh('tcp', 'db.example.net:5432', { tcpTls: true, tlsServerName: 'db.internal', tlsMinDays: '14' }));
  assert.deepEqual(built.input.tls, { serverName: 'db.internal', minValidityDays: 14 });
  built = create(fresh('tcp', 'db.example.net:5432', { tcpTls: false, tlsServerName: 'db.internal' }));
  assert.equal('tls' in built.input, false, 'TLS settings without TLS are dropped, as before');
  built = create(fresh('tcp', 'db.example.net:5432', { tcpTls: true, tlsServerName: 'bad name' }));
  assert.ok(built.errors.tlsServerName);
  const plain = stored({ type: 'tcp', target: 'db.example.net:5432' });
  assert.equal(cloneModel.draftFrom(plain, ['a']).tcpTls, false);
  assert.equal('tls' in rename(plain).built.input, false);
  const empty = stored({ type: 'tcp', target: 'db.example.net:5432', tls: {} });
  assert.equal(cloneModel.draftFrom(empty, ['a']).tcpTls, true);
  const renamed = rename(empty).built;
  assert.equal(JSON.stringify(renamed.input.tls), '{}');
  assert.deepEqual(checks.checkChanges(checks.inputOf(empty), renamed.input), []);
  const off = editWith(empty, () => ({ tcpTls: false }));
  assert.equal('tls' in off.input, false);
  assert.deepEqual(checks.checkChanges(checks.inputOf(empty), off.input), ['tls'], 'turning TLS off is a new check');
  const on = editWith(plain, () => ({ tcpTls: true }));
  assert.deepEqual(checks.checkChanges(checks.inputOf(plain), on.input), ['tls']);
  assert.ok(factsHtml(plain, false, false).includes('TCP only'));
  assert.ok(factsHtml(empty, false, false).includes('TLS 1.2+ handshake with the certificate verified'));
  assert.equal(checks.tlsApplies('smtp', 'h:25', 'plain', true), false, 'no STARTTLS-on-TCP: the TCP flag changes only TCP');
});

// ---------- description

await check('description: optional text of at most 8,192 bytes with line breaks, as the node’s statusText allows; never part of the check', () => {
  assert.match(goSource('canary/validation.go'), /statusText\(m\.Description, 8192, true, true\)/u);
  assert.match(goSource('canary/types.go'), /Description\s+string\s+`json:"description,omitempty"`/u);
  for (const text of ['', 'Owner: payments team', 'line one\nline two\n', 'a'.repeat(8192), ch(0xe9).repeat(4096), '   ', `C1 ${ch(0x85)} allowed`, `${ch(0x1f600)} emoji`]) {
    assert.equal(n.descriptionProblem(text), undefined, JSON.stringify(text.slice(0, 20)));
  }
  for (const [text, said] of [['a'.repeat(8193), '8,192 bytes'], [ch(0xe9).repeat(4097), '8,192 bytes'], ['tab\there', 'control characters'], ['cr\rhere', 'control characters'], [`del${ch(0x7f)}`, 'control characters'],
    [`nul${ch(0)}`, 'control characters'], [`a${LONE}b`, 'unpaired surrogate']]) {
    assert.ok(n.descriptionProblem(text)?.includes(said), `${JSON.stringify(text.slice(0, 12))}: ${n.descriptionProblem(text)}`);
  }
  const typed = 'Runs the checkout API.\nOwner: payments  ';
  let built = create(fresh('http', 'https://api.example.com/health', { description: typed }));
  assert.equal(built.input.description, typed, 'sent exactly as typed');
  built = create(fresh('http', 'https://api.example.com/health'));
  assert.equal('description' in built.input, false);
  built = create(fresh('http', 'https://api.example.com/health', { description: 'x\ty' }));
  assert.ok(built.errors.description);
  const m = stored({ type: 'http', target: 'https://api.example.com/health', description: 'Stored notes\n<b>not markup</b>' });
  assert.equal(cloneModel.draftFrom(m, ['a']).description, m.description);
  assert.equal(rename(m).built.input.description, m.description);
  const edited = editWith(m, () => ({ description: 'New notes' }));
  assert.equal(edited.input.description, 'New notes');
  assert.deepEqual(checks.checkChanges(checks.inputOf(m), edited.input), [], 'a description is metadata: no new check generation');
  const removed = editWith(m, () => ({ description: '' }));
  assert.equal('description' in removed.input, false);
  const copy = checks.cloneInput(m, () => 'f'.repeat(64));
  assert.equal(copy.description, m.description, 'a copy keeps the description');
  const ctx = { groupMap: new Map(), role: 'viewer', channels: { map: new Map() }, tags: { map: new Map() }, monitorMap: new Map(), endpoint: 'a' };
  const row = resolverModel.monitorRows(ctx, false).find(r => r.key === 'description');
  assert.ok(row, 'conflicts show the description in its own row');
  assert.deepEqual(row.project(m), { description: m.description });
  const shown = html(row.render(m));
  assert.ok(shown.includes('&lt;b&gt;not markup&lt;/b&gt;') && !shown.includes('<b>not'), 'plain text, never markup');
});

await check('no storage or network: loading, building and rendering touched nothing', () => {
  assert.deepEqual(touched, []);
  assert.deepEqual(requests, []);
});

console.log(failed ? `${failed} check group(s) failed` : 'all network option checks passed');
process.exit(failed ? 1 : 0);
