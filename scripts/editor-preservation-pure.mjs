// Pure checks that the maintenance, tag and channel editors keep what a newer node stores and this dashboard doesn't
// model (2026-10-24 completion audit F1-F3): apps/web/src/writable.ts and the save models of views/Maintenance.tsx,
// views/Tags.tsx and views/Channels.tsx, through their exported model seams. A save starts from the stored object minus
// runtime-owned metadata, then replaces only the keys the form writes; a new record carries nothing extra; nothing from
// the surrounding view (state, windows, conflict flags) is read. Whether a given node accepts such a key is that node's
// rule: these checks make no claim about any node. Imports the source in place through one tsx namespace: no copy,
// scratch, network or output files. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/editor-preservation-pure.mjs   (exit 1 on any failure)
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

process.env.TSX_DISABLE_CACHE = '1';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
globalThis.React ??= reactModule.default ?? reactModule;
const { register } = await import('tsx/esm/api');
const scope = register({ namespace: 'editor-preservation-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const writable = await load('writable.ts');
const { tagFormModel } = await load('views/Tags.tsx');
const { channelFormModel } = await load('views/Channels.tsx');
const { maintenanceFormModel } = await load('views/Maintenance.tsx');
let failed = 0;
const check = (name, fn) => { try { fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const RUNTIME = { id: 'res-1', revision: 'rev-2', revisions: ['rev-1', 'rev-2'], updatedAt: 1_760_000_000_000, deleted: false };

check('storedExtras: drops runtime metadata, modeled keys and nulls; deep-copies the rest; nothing for a new record', () => {
  assert.deepEqual([...writable.RESOURCE_RUNTIME_FIELDS], ['id', 'revision', 'revisions', 'updatedAt', 'deleted']);
  const stored = { ...RUNTIME, name: 'n', future: { nested: [1, null] }, flag: false, count: 0, text: '', gone: null, missing: undefined };
  const before = structuredClone(stored);
  const extras = writable.storedExtras(stored, ['name']);
  assert.deepEqual(extras, { future: { nested: [1, null] }, flag: false, count: 0, text: '' }, 'falsy values other than null are kept');
  extras.future.nested.push(2);
  assert.deepEqual(stored, before, 'the stored object never changes');
  assert.deepEqual(writable.storedExtras(undefined, ['name']), {});
  assert.deepEqual(writable.storedExtras(null, ['name']), {});
  assert.deepEqual(writable.storedExtras({ id: 'kept', x: 1 }, [], []), { id: 'kept', x: 1 }, 'an explicit runtime list replaces the default');
});

check('storedExtras: a stored "__proto__" key stays an ordinary key and changes no prototype', () => {
  const stored = JSON.parse('{"__proto__":{"polluted":true},"name":"n"}');
  const extras = writable.storedExtras(stored, ['name']);
  assert.equal(Object.getPrototypeOf(extras), Object.prototype);
  assert.ok(Object.hasOwn(extras, '__proto__'));
  assert.equal(JSON.stringify(extras), '{"__proto__":{"polluted":true}}');
  assert.equal({}.polluted, undefined);
});

// F2: Tags.tsx used to send only { name, color }.
check('tags: a new tag sends exactly its name and color', () => {
  assert.equal(JSON.stringify(tagFormModel.tagInput(' Prod ', '#2A78D6', undefined)), '{"name":"Prod","color":"#2a78d6"}');
});

check('tags: an edit keeps stored keys this dashboard doesn’t model, never runtime metadata or nulls', () => {
  const stored = { ...RUNTIME, name: 'Prod', color: '#2a78d6', futureIcon: { glyph: 'server', sizes: [16, null] }, futureNull: null };
  const before = structuredClone(stored);
  const unedited = tagFormModel.tagInput('Prod', '#2a78d6', stored);
  assert.deepEqual(unedited, { name: 'Prod', color: '#2a78d6', futureIcon: { glyph: 'server', sizes: [16, null] } });
  const renamed = tagFormModel.tagInput('Production', '#0CA30C', stored);
  assert.deepEqual(renamed, { name: 'Production', color: '#0ca30c', futureIcon: { glyph: 'server', sizes: [16, null] } });
  renamed.futureIcon.sizes.push(32);
  assert.deepEqual(stored, before);
  const proto = tagFormModel.tagInput('Prod', '#2a78d6', JSON.parse('{"__proto__":{"x":1},"name":"Prod","color":"#2a78d6"}'));
  assert.equal(Object.getPrototypeOf(proto), Object.prototype);
  assert.equal(JSON.stringify(proto), '{"name":"Prod","color":"#2a78d6","__proto__":{"x":1}}');
});

// F3: Channels.tsx used to send only { name, enabled, config }; config already kept unknown keys.
const channel = () => ({
  ...RUNTIME, name: 'Ops hook', enabled: true, isDefault: true, futureMeta: { order: [2, 1], note: null }, futureNull: null,
  config: { kind: 'webhook', webhook: { url: 'https://hooks.example.test/uptime', bearerToken: 'review-hook-key' }, futureConfig: { x: 1 } },
});

check('channels: a new channel sends exactly name, enabled and config', () => {
  const draft = { ...channelFormModel.channelDraft(undefined), name: 'New hook', kind: 'webhook', hookUrl: 'https://hooks.example.test/new' };
  const built = channelFormModel.buildChannel(draft, undefined);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(Object.keys(built.input), ['name', 'enabled', 'config']);
});

check('channels: an unedited or renamed save keeps channel-level and config-level unknown keys, never runtime metadata', () => {
  const stored = channel();
  const before = structuredClone(stored);
  const unedited = channelFormModel.buildChannel(channelFormModel.channelDraft(stored), stored);
  assert.ok(unedited.input, JSON.stringify(unedited.errors));
  assert.deepEqual(unedited.input, { name: 'Ops hook', enabled: true, config: stored.config, isDefault: true, futureMeta: { order: [2, 1], note: null } });
  for (const key of Object.keys(RUNTIME)) assert.equal(key in unedited.input, false, key);
  const renamed = channelFormModel.buildChannel({ ...channelFormModel.channelDraft(stored), name: 'Ops hook renamed', enabled: false }, stored);
  assert.deepEqual(renamed.input, { name: 'Ops hook renamed', enabled: false, config: stored.config, isDefault: true, futureMeta: { order: [2, 1], note: null } });
  renamed.input.futureMeta.order.push(3);
  assert.deepEqual(stored, before);
});

check('channels: a provider change keeps channel-level keys and drops the old provider’s', () => {
  const stored = channel();
  const draft = { ...channelFormModel.channelDraft(stored), kind: 'discord', discordUrl: 'https://discord.com/api/webhooks/123456789/review-fixture' };
  const built = channelFormModel.buildChannel(draft, stored);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config, { kind: 'discord', webhook: { url: 'https://discord.com/api/webhooks/123456789/review-fixture' } });
  assert.equal(built.input.isDefault, true);
  assert.deepEqual(built.input.futureMeta, { order: [2, 1], note: null });
});

// F1: Maintenance.tsx used to build { name, enabled, selection, schedule } from scratch.
const maintenance = () => ({
  ...RUNTIME, name: 'Patch window', enabled: true, allMonitors: true, publicTitle: 'Scheduled patching', statusPageIds: ['page-1'], futureNull: null,
  schedule: { strategy: 'weekly', timezone: 'Europe/Berlin', startDate: '2026-10-01', endDate: '2027-09-30', startTime: '02:00', durationMinutes: 90, weekdays: [2, 4], futureSkip: ['2026-12-24'] },
});
const { mDraft, buildMaintenance } = maintenanceFormModel;

check('maintenance: a new schedule sends only the keys the form writes, as before', () => {
  const draft = { ...mDraft(undefined), name: 'Night work', scope: 'all' };
  for (const built of [buildMaintenance(draft), buildMaintenance(draft, undefined)]) {
    assert.ok(built.input, JSON.stringify(built.errors));
    assert.deepEqual(Object.keys(built.input).sort(), ['allMonitors', 'enabled', 'name', 'schedule']);
    assert.deepEqual(Object.keys(built.input.schedule).sort(), ['durationMinutes', 'endDate', 'startDate', 'startTime', 'strategy', 'timezone', 'weekdays']);
  }
});

check('maintenance: an unedited or renamed save keeps unknown top-level and schedule keys, never runtime metadata or nulls', () => {
  const stored = maintenance();
  const before = structuredClone(stored);
  const expected = { name: 'Patch window', enabled: true, allMonitors: true, publicTitle: 'Scheduled patching', statusPageIds: ['page-1'], schedule: stored.schedule };
  const unedited = buildMaintenance(mDraft(stored), stored);
  assert.ok(unedited.input, JSON.stringify(unedited.errors));
  assert.deepEqual(unedited.input, expected);
  const renamed = buildMaintenance({ ...mDraft(stored), name: 'Patch window renamed' }, stored);
  assert.deepEqual(renamed.input, { ...expected, name: 'Patch window renamed' });
  for (const key of [...Object.keys(RUNTIME), 'futureNull']) assert.equal(key in renamed.input, false, key);
  renamed.input.schedule.futureSkip.push('2026-12-31');
  renamed.input.statusPageIds.push('page-2');
  assert.deepEqual(stored, before);
  const fewer = buildMaintenance({ ...mDraft(stored), weekdays: [4] }, stored);
  assert.deepEqual(fewer.input.schedule, { ...stored.schedule, weekdays: [4] }, 'an edit within the strategy keeps its unknown keys');
});

check('maintenance: a strategy change drops the old strategy’s keys, modeled or not, and keeps top-level ones', () => {
  const stored = maintenance();
  const daily = buildMaintenance({ ...mDraft(stored), strategy: 'daily' }, stored);
  assert.ok(daily.input, JSON.stringify(daily.errors));
  assert.deepEqual(daily.input.schedule, { strategy: 'daily', timezone: 'Europe/Berlin', startDate: '2026-10-01', endDate: '2027-09-30', durationMinutes: 90, startTime: '02:00' });
  assert.equal(daily.input.publicTitle, 'Scheduled patching');
  assert.deepEqual(daily.input.statusPageIds, ['page-1']);
  const chosen = buildMaintenance({ ...mDraft(stored), scope: 'some', monitorIds: ['m1'] }, stored);
  assert.equal('allMonitors' in chosen.input, false, 'the selection is the form’s own: switching to chosen monitors drops allMonitors');
  assert.deepEqual(chosen.input.monitorIds, ['m1']);
});

check('sources: each editor saves through its model with the version it edits', () => {
  assert.ok(source('views/Tags.tsx').includes('const input = tagInput(name, color, tag);'));
  const m = source('views/Maintenance.tsx');
  assert.ok(m.includes('buildMaintenance(draft, base)'));
  assert.ok(m.includes('setDraft(mDraft(current)); setBase(current);'), 'Load latest also replaces the base');
  assert.equal(m.includes('setBaseRevision'), false);
  // Completion r5 (accepted) added the channel default flag: Go's ChannelInput.IsDefault `json:"isDefault,omitempty"`
  // (canary/channels.go; parseChannel admits the key) and shared NotificationChannelInput.isDefault?. The composite gained one
  // spread, written only when on, between config and the stored extras. The extras still come from storedExtras over the same
  // original and CHANNEL_KEYS; CHANNEL_KEYS now lists isDefault, so a stored flag never overrides the draft's. The flag's
  // behavior (off omitted, kept across a provider change) is asserted in scripts/channel-defaults-pure.mjs.
  const c = source('views/Channels.tsx');
  assert.ok(c.includes('{ name, enabled: draft.enabled, config, ...(draft.isDefault ? { isDefault: true } : {}), ...storedExtras(original, CHANNEL_KEYS) }'),
    'the channel save builds name, enabled, config and the on-only default flag, then the stored extras');
  assert.ok(c.includes("const CHANNEL_KEYS = ['name', 'enabled', 'config', 'isDefault'];"), 'the modeled keys include the default flag');
});

console.log(failed ? `${failed} FAILED` : 'ALL PASSED');
process.exit(failed ? 1 : 0);
