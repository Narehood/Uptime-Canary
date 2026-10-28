// Rate-contract regression: imports source in place, no server or network I/O.
import assert from 'node:assert/strict';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { register } from 'tsx/esm/api';

process.env.TSX_DISABLE_CACHE = '1';
globalThis.React ??= React;
globalThis.document ??= { addEventListener() {} };
globalThis.fetch = () => { throw new Error('No network in the rate fixture'); };
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const scope = register({ namespace: 'monitor-rate-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const { cloneModel, MonitorFormView } = await load('views/MonitorForm.tsx');
const { CanaryContext } = await load('context.tsx');
const { checkChanges } = await load('checks.tsx');
const fresh = () => ({ ...cloneModel.draftFrom(undefined, ['a']), name: 'Private fixture', targets: { http: 'http://127.0.0.1:9' }, timeout: { amount: '70', unit: 'ms' } });
const build = draft => cloneModel.build(draft, undefined, undefined);
const duration = amount => ({ amount: String(amount), unit: 'ms' });

assert.equal(fresh().internalOnly, false, 'a new monitor requires deliberate consent');
assert.ok(build(fresh()).input, 'the one-minute default is valid');
assert.equal('internalOnly' in build(fresh()).input, false, 'unchecked consent is omitted');
for (const milliseconds of [100, 1000, 59_999]) {
  const draft = { ...fresh(), interval: duration(milliseconds) };
  assert.ok(build(draft).errors.interval, 'short interval must be refused without consent');
  const accepted = build({ ...draft, internalOnly: true });
  assert.ok(accepted.input, JSON.stringify(accepted.errors));
  assert.equal(accepted.input.intervalMs, milliseconds);
  assert.equal(accepted.input.internalOnly, true);
}
const retry = { ...fresh(), retryOn: true, retryInterval: duration(100) };
assert.ok(build(retry).errors.retryInterval, 'retries cannot bypass the minimum');
assert.equal(build({ ...retry, internalOnly: true }).input.retryIntervalMs, 100);
assert.ok(build({ ...fresh(), internalOnly: true, interval: duration(99) }).errors.interval, 'original internal lower bound is retained');
const stored = { ...build({ ...fresh(), internalOnly: true }).input, id: 'm1', revision: 'r1', updatedAt: 1 };
const initial = cloneModel.draftFrom(stored, ['a']);
assert.equal(initial.internalOnly, true, 'explicit consent survives reopening');
const removed = cloneModel.build({ ...initial, internalOnly: false }, stored, initial);
assert.ok(removed.input);
assert.equal('internalOnly' in removed.input, false, 'unchecking removes saved consent');
const legacy = { ...stored, internalOnly: undefined, intervalMs: 100 };
const legacyDraft = cloneModel.draftFrom(legacy, ['a']);
assert.equal(legacyDraft.internalOnly, false, 'legacy fast values never infer consent');
assert.ok(cloneModel.build(legacyDraft, legacy, structuredClone(legacyDraft)).errors.interval);

const pausedDraft = { ...legacyDraft, enabled: false, retryOn: true, retryInterval: duration(100) };
const paused = cloneModel.build(pausedDraft, legacy, legacyDraft);
const resumed = cloneModel.build({ ...pausedDraft, enabled: true }, legacy, legacyDraft);
const legacyPush = { ...legacy, type: 'push', target: 'push', intervalMs: 1000, push: { token: 'rate-fixture-token' } };
const reviewCases = [
  ['pausing legacy normal and retry intervals needs no consent', Boolean(paused.input && !paused.input.enabled && !paused.input.internalOnly)],
  ['resuming fast intervals still requires consent', Boolean(resumed.errors.interval && resumed.errors.retryInterval)],
  ['push consent warns when the effective heartbeat deadline changes', checkChanges(legacyPush, { ...legacyPush, internalOnly: true }).includes('intervalMs')],
  ['raising a legacy raw interval to its existing floor needs no deadline warning', !checkChanges(legacyPush, { ...legacyPush, intervalMs: 60_000 }).includes('intervalMs')],
  ['normal monitor consent remains scheduling metadata', !checkChanges(legacy, { ...legacy, internalOnly: true }).includes('intervalMs')],
];
for (const [name, passed] of reviewCases) console.log(`${passed ? 'PASS' : 'FAIL'} ${name}`);
assert.ok(reviewCases.every(([, passed]) => passed), 'review regressions must all pass');

const nodes = [{ id: 'a', name: 'Fixture node', state: 'local', local: true, available: true, lastSeenAt: 1 }];
const catalog = () => ({ list: [], map: new Map(), loading: false, unsupported: false, reload() {} });
const context = {
  role: 'admin', editable: true, admin: true, endpoint: 'a', controlPlane: false, selection: null,
  overview: { node: { id: 'a', name: 'Fixture node' }, now: 1, monitors: [], nodes: [], incidents: [], notifications: [], user: { name: 'fixture', role: 'admin' } },
  monitorMap: new Map(), nodes, nodeMap: new Map(nodes.map(node => [node.id, node])),
  api: { registeredNode: null, baseUrl: 'http://127.0.0.1:9' }, refresh() {}, groups: null, groupMap: new Map(), stillCurrent: () => true,
  channels: catalog(), tags: catalog(),
};
const html = renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: context }, React.createElement(MonitorFormView)));
assert.ok(html.includes('This monitor is used internally only, on equipment I own'));
assert.ok(html.includes('Faster checks increase network traffic'));
assert.ok(html.includes('public or third-party services'));
assert.ok(html.includes('1 min to 24 h'));
const checkbox = html.match(/<label\b[^>]*><input\b([^>]*type="checkbox"[^>]*)\/?>(?:(?!<\/label>)[\s\S])*?This monitor is used internally only, on equipment I own(?:(?!<\/label>)[\s\S])*?<\/label>/u);
assert.ok(checkbox, 'consent uses a labeled checkbox');
assert.doesNotMatch(checkbox[1], /\bchecked(?:=|\s|$)/u, 'consent starts unchecked');
console.log('PASS monitor rate: default minimum, retry minimum, explicit consent, saved edits, legacy consent, and accessible warning');
