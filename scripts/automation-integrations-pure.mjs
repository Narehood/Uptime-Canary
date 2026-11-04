// Run through scripts/bounded_validation.py. In-place source imports; no sends,
// browser profiles, source copies, or generated fixtures.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
globalThis.fetch = async () => { throw Error('Network is forbidden in this fixture'); };
for (const name of ['localStorage', 'sessionStorage']) Object.defineProperty(globalThis, name, { configurable: true, value: {
  getItem: () => null, setItem: () => assert.fail('No credential persistence'), removeItem() {},
} });
const React = (await import('react')).default;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'automation-integrations', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const { channelFormModel: form, PROVIDERS } = await load('views/Channels.tsx');
const messaging = await load('messaging.ts');
let passed = 0;
const check = (name, fn) => { fn(); console.log(`PASS ${name}`); passed++; };
const channels = [
  { kind: 'homeassistant', homeassistant: { baseURL: 'https://ha.example.test', accessToken: 'private-ha-token', eventType: 'uptime_canary' } },
  { kind: 'matrix', matrix: { baseURL: 'https://matrix.example.test', accessToken: 'private-matrix-token', roomId: '!domainless_hash' } },
];
for (const config of channels) {
  const channel = { id: 'fixture', revision: 'revision', updatedAt: 1, name: 'Automation', enabled: true, config, events: ['down', 'recovery'] };
  const draft = form.channelDraft(channel);
  check(`${config.kind} provider is offered`, () => assert.equal(PROVIDERS.filter(p => p.kind === config.kind).length, 1));
  check(`${config.kind} rename keeps credentials and selected events`, () => {
    const built = form.buildChannel({ ...draft, name: 'Renamed' }, channel, 'enhanced', 22);
    assert.ok(built.input, JSON.stringify(built.errors));
    assert.deepEqual(built.input.config, config);
    assert.deepEqual(built.input.events, ['down', 'recovery']);
  });
  check(`${config.kind} masks credentials in route summary`, () => {
    const summary = JSON.stringify(messaging.messagingRoute(config));
    assert.ok(!summary.includes(config[config.kind].accessToken));
  });
  check(`${config.kind} requires the selected node capability`, () => {
    for (const protocol of [0, 21]) assert.equal(form.buildChannel(draft, channel, 'enhanced', protocol).input, undefined);
  });
  check(`${config.kind} recovery-only and default event serialization`, () => {
    assert.deepEqual(form.buildChannel({ ...draft, events: ['recovery'] }, channel, 'enhanced', 22).input.events, ['recovery']);
    assert.equal(form.buildChannel({ ...draft, events: ['down', 'repeat', 'recovery'] }, channel, 'enhanced', 22).input.events, undefined);
    for (const events of [[], ['repeat']]) assert.ok(form.buildChannel({ ...draft, events }, channel, 'enhanced', 22).errors.events);
  });
  check(`${config.kind} switching providers drops credentials`, () => {
    const built = form.buildChannel({ ...draft, kind: 'webhook', hookUrl: 'https://hook.example.test/event' }, channel, 'enhanced', 22);
    assert.ok(built.input, JSON.stringify(built.errors));
    assert.equal(built.input.config[config.kind], undefined);
  });
  check(`${config.kind} credentials rotate without changing route identity`, () => {
    const next = { ...draft, ...(config.kind === 'matrix' ? { matrixToken: 'rotated' } : { haToken: 'rotated' }) };
    assert.equal(messaging.routeKey(config.kind, next), messaging.routeKey(config.kind, draft));
    assert.notEqual(messaging.credentialKey(config.kind, next), messaging.credentialKey(config.kind, draft));
  });
}
check('domainless and legacy Matrix room IDs remain usable', () => {
  const channel = { name: 'Matrix', enabled: true, config: channels[1] };
  const draft = form.channelDraft(channel);
  for (const matrixRoom of ['!hash_no_domain', '!ops:example.test']) assert.ok(form.buildChannel({ ...draft, matrixRoom }, channel, 'enhanced', 22).input);
  for (const matrixRoom of ['!', '#alias:example.test', '!room/elsewhere', '!room?secret']) assert.ok(form.buildChannel({ ...draft, matrixRoom }, channel, 'enhanced', 22).errors.roomId);
});
check('Home Assistant event names cannot select a service path', () => {
  const channel = { name: 'HA', enabled: true, config: channels[0] };
  assert.ok(form.buildChannel({ ...form.channelDraft(channel), haEvent: '../services/light/turn_on' }, channel, 'enhanced', 22).errors.eventType);
});
check('Prometheus config scrapes the authenticated node API with a credential file', () => {
  const config = readFileSync(resolve(root, 'deploy/observability/prometheus.yml'), 'utf8');
  assert.match(config, /metrics_path: \/api\/metrics/u);
  assert.match(config, /credentials_file: \/run\/secrets\/canary_viewer_token/u);
});
console.log(`${passed} automation integration contract checks passed`);
scope.unregister();
