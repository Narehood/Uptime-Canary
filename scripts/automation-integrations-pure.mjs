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
const { channelFormModel: form, PROVIDERS, toggleAlertEvent, testSubmissionDisposition, channelDraftChanged, sameChannelSettings, PreviewOutcomeList, previewRecipientLabels } = await load('views/Channels.tsx');
const messaging = await load('messaging.ts');
const apiModel = await load('api.ts');
const routes = await load('router.ts');
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
check('event controls immediately remove dependent repeats while keeping recovery-only usable', () => {
  assert.deepEqual(toggleAlertEvent(['down', 'repeat', 'recovery'], 'down', false), ['recovery']);
  assert.deepEqual(toggleAlertEvent(['recovery'], 'repeat', true), ['recovery']);
  assert.deepEqual(toggleAlertEvent(['recovery'], 'down', true), ['down', 'recovery']);
  assert.deepEqual(toggleAlertEvent(['down', 'recovery'], 'repeat', true), ['down', 'repeat', 'recovery']);
  assert.deepEqual(toggleAlertEvent(['recovery'], 'recovery', false), []);
});
check('native target summaries identify exact routing without credentials or query data', () => {
  const ha = messaging.automationDestination({ kind: 'homeassistant', homeassistant: { baseURL: 'https://user:password@home.example.test/relay/?token=private#secret', accessToken: 'private-ha', eventType: 'office_status' } });
  assert.deepEqual(ha, { server: 'https://home.example.test/relay', targetLabel: 'Event type', target: 'office_status' });
  const matrix = messaging.automationDestination({ kind: 'matrix', matrix: { baseURL: 'https://matrix.example.test/prefix/', accessToken: 'private-matrix', roomId: '!operations:example.test' } });
  assert.deepEqual(matrix, { server: 'https://matrix.example.test/prefix', targetLabel: 'Room ID', target: '!operations:example.test' });
  assert.equal(messaging.automationDestination({ kind: 'webhook', webhook: { url: 'https://example.test/private-webhook-token' } }), undefined);
});
check('Matrix list summaries distinguish rooms on one homeserver and never include access tokens', () => {
  const base = { kind: 'matrix', matrix: { baseURL: 'https://matrix.example.test', accessToken: 'private-matrix-token', roomId: '!first:example.test' } };
  const first = messaging.messagingRoute(base);
  const second = messaging.messagingRoute({ ...base, matrix: { ...base.matrix, roomId: '!second:example.test' } });
  assert.notDeepEqual(first, second);
  assert.ok(JSON.stringify(first).includes('!first:example.test'));
  assert.ok(!JSON.stringify(first).includes('private-matrix-token'));
});
check('test submission classification never treats timeouts, proxy failures or unreadable successes as unsent', () => {
  for (const status of [0, 200, 202, 204, 408, 418, 500, 502, 503, 504]) assert.equal(testSubmissionDisposition(new apiModel.ApiFailure(status, 'fixture')), 'unconfirmed', status);
  for (const status of [400, 401, 403, 404, 409, 413, 422, 429]) assert.equal(testSubmissionDisposition(new apiModel.ApiFailure(status, 'fixture')), 'rejected', status);
  assert.equal(testSubmissionDisposition(new TypeError('network fixture')), 'unconfirmed');
});
check('test reply validation accepts real attempt records and rejects missing or ambiguous acknowledgements', () => {
  const valid = { id: 'attempt-1', nodeId: 'node-a', kind: 'test', provider: 'homeassistant', state: 'queued', failures: 0, channelId: 'ha-1' };
  assert.deepEqual(apiModel.readChannelTestAttempts([valid]), [valid]);
  for (const value of [undefined, {}, [], [valid, valid], [{ ...valid, kind: 'down' }], [{ ...valid, state: 'invented' }], [{ ...valid, failures: -1 }], [{ ...valid, id: '' }]]) assert.equal(apiModel.readChannelTestAttempts(value), undefined);
});
check('dirty state ignores recipient row keys and detects configuration and credential changes', () => {
  const original = form.channelDraft({ name: 'Mail', enabled: true, config: { kind: 'smtp', smtp: { server: 'mail.example.test', tlsMode: 'starttls' }, recipients: [{ email: 'ops@example.test', name: 'Ops' }] } });
  const same = structuredClone(original); same.recipients[0].key += 99;
  assert.equal(channelDraftChanged(same, original), false);
  for (const changed of [{ ...same, name: 'Renamed' }, { ...same, enabled: false }, { ...same, smtpPass: 'new-private-value' }, { ...same, events: ['recovery'] }]) assert.equal(channelDraftChanged(changed, original), true);
  const recipient = structuredClone(same); recipient.recipients[0].email = 'other@example.test'; assert.equal(channelDraftChanged(recipient, original), true);
});
check('channel delivery links round-trip IDs without allowing unrelated or ambiguous query parameters', () => {
  assert.deepEqual(routes.parseRoute(routes.deliveryHref()), { name: 'deliveries' });
  for (const id of ['ha-1', 'opaque ?&#/room']) assert.deepEqual(routes.parseRoute(routes.deliveryHref(id)), { name: 'deliveries', channelId: id });
  for (const hash of ['#/notifications/deliveries?channel=a&channel=b', '#/notifications/deliveries?channel=a&token=private', '#/monitors?channel=a']) assert.equal(routes.parseRoute(hash).name, 'not-found');
});
check('navigation registration holds a dirty route and releases without disturbing a newer editor', () => {
  const before = globalThis.location; globalThis.location = { hash: '#/channels/ha-1/edit' }; let proceed;
  try {
    const release = routes.registerNavigationGuard(action => { proceed = action; return true; });
    routes.navigate('#/notifications'); assert.equal(location.hash, '#/channels/ha-1/edit');
    proceed(); assert.equal(location.hash, '#/notifications');
    let switched = false; routes.requestNavigation(() => { switched = true; });
    assert.equal(switched, false, 'context changes use the same discard decision');
    proceed(); assert.equal(switched, true); assert.equal(location.hash, '#/notifications');
    const newer = routes.registerNavigationGuard(() => true); release();
    routes.navigate('#/monitors'); assert.equal(location.hash, '#/notifications');
    newer(); routes.navigate('#/monitors'); assert.equal(location.hash, '#/monitors');
  } finally { globalThis.location = before; }
});
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
check('native URL and room-ID constraints produce field errors before submission', () => {
  for (const config of channels) {
    const channel = { name: 'Invalid route', enabled: true, config };
    const draft = form.channelDraft(channel);
    for (const suffix of ['/relay/../api', '/relay/%2e/api', '/relay/.%2e/api']) {
      const change = config.kind === 'matrix' ? { matrixBase: 'https://example.test' + suffix } : { haBase: 'https://example.test' + suffix };
      assert.ok(form.buildChannel({ ...draft, ...change }, channel, 'enhanced', 22).errors.serverUrl);
    }
    if (config.kind === 'matrix') for (const matrixRoom of ['!room\u0001:example.test', '!room\u0080:example.test']) assert.ok(form.buildChannel({ ...draft, matrixRoom }, channel, 'enhanced', 22).errors.roomId);
  }
});
check('Prometheus config scrapes the authenticated node API with a credential file', () => {
  const config = readFileSync(resolve(root, 'deploy/observability/prometheus.yml'), 'utf8');
  assert.match(config, /metrics_path: \/api\/metrics/u);
  assert.match(config, /credentials_file: \/run\/secrets\/canary_viewer_token/u);
});

// Rendered checks for the protocol 22 controls and the Grafana dashboard. React's static renderer with a stand-in Canary
// context, as in scripts/alert-templates-pure.mjs: nothing is clicked, sent, stored or written, and no browser runs.
const { renderToStaticMarkup } = await import('react-dom/server');
const { CanaryContext } = await load('context.tsx');
const { ChannelFormView, ChannelsView, eventsSummary } = await load('views/Channels.tsx');
const { MessagingFields } = await load('views/MessagingFields.tsx');
// React's static markup separates adjacent text nodes with <!-- -->; text() also drops tags, so attribute values aren't text.
const plain = markup => markup.replaceAll('<!-- -->', '');
const text = markup => plain(markup).replace(/<[^>]*>/gu, ' ');
const attributes = tag => {
  const found = new Map();
  for (const [, name, value] of String(tag).replace(/^<[a-z]+/iu, '').matchAll(/\s([^\s"'<>\/=]+)(?:="([^"]*)")?/gu)) found.set(name.toLowerCase(), value ?? '');
  return found;
};
const inputWith = (markup, needle) => {
  const tag = markup.match(new RegExp(`<input[^>]*${needle}[^>]*>`, 'u'))?.[0];
  assert.ok(tag, `an input with ${needle} renders`);
  return attributes(tag);
};
// The UI council refinement replaces provider radio cards with native select options.
// Keep the original protocol gating assertions against those real controls.
const providerOption = (markup, kind) => {
  const tag = markup.match(/<option\b[^>]*>/gu)?.find(tag => tag.includes(`value="${kind}"`));
  assert.ok(tag, `provider option ${kind} renders`);
  return attributes(tag);
};
const openTag = markup => markup.slice(0, markup.indexOf('>') + 1);
const catalog = (list = []) => ({ list, map: new Map(list.map(entry => [entry.channel.id, entry])), loading: false, error: undefined, unsupported: false, reload() {} });
const NODES = [{ id: 'a', name: 'Node A', state: 'local', lastSeenAt: null }];
const ctx = (list, peerProtocol) => ({
  role: 'editor', editable: true, admin: false, endpoint: 'a', controlPlane: false, selection: null, search: '',
  overview: { ...(peerProtocol ? { build: { name: 'Uptime-Canary', version: 'unknown', peerProtocol } } : {}), node: { id: 'a', name: 'Node A' }, now: 1_760_000_000_000, monitors: [], nodes: [], incidents: [], notifications: [], user: { name: 'review', role: 'editor' } },
  monitorMap: new Map(), nodes: NODES, nodeMap: new Map(NODES.map(node => [node.id, node])),
  api: { registeredNode: null, baseUrl: 'http://node-a.test:3100' }, refresh() {}, groups: null, groupMap: new Map(), maintenanceMap: new Map(), stillCurrent: () => true,
  channels: catalog(list), tags: catalog(),
});
const render = (element, list, peerProtocol) => plain(renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx(list, peerProtocol) }, element)));
const saved = (config, events) => ({ id: `${config.kind}-1`, revision: 'r1', updatedAt: 1, name: 'Automation', enabled: true, config, ...(events ? { events } : {}) });
const renderForm = (channel, peerProtocol) => render(React.createElement(ChannelFormView, channel ? { id: channel.id } : {}), channel ? [{ channel, configConflict: false }] : [], peerProtocol);
const eventsFieldset = markup => {
  const at = markup.indexOf('<legend>Alert events</legend>');
  assert.ok(at > 0, 'the alert events fieldset renders');
  return markup.slice(markup.lastIndexOf('<fieldset', at), markup.indexOf('</fieldset>', at));
};
const eventBoxes = fieldset => Object.fromEntries(['Down alerts', 'Repeats', 'Recoveries'].map(label => {
  const at = fieldset.indexOf(`>${label}<`);
  assert.ok(at > 0, `${label} checkbox renders`);
  const start = fieldset.lastIndexOf('<input', at);
  return [label, attributes(fieldset.slice(start, fieldset.indexOf('>', start) + 1))];
}));

check('rendered Home Assistant fields: masked token, event type and repeat-safe automation guidance', () => {
  const markup = renderForm(saved(channels[0], ['down', 'recovery']), 22);
  for (const label of ['Home Assistant URL', 'Long-lived access token', 'Event type', 'Allow plain HTTP for this server']) assert.ok(markup.includes(label), label);
  assert.equal(inputWith(markup, 'value="private-ha-token"').get('type'), 'password');
  assert.equal(text(markup).includes('private-ha-token'), false, 'the token is never page text');
  assert.ok(markup.includes('value="https://ha.example.test"') && markup.includes('value="uptime_canary"'));
  const words = text(markup);
  for (const phrase of ['light.turn_on', 'toggle', 'trigger.event.data.kind', 'event type pick the destination']) assert.ok(words.includes(phrase), phrase);
});
check('rendered Matrix fields: masked token, internal room ID and unencrypted joined rooms only', () => {
  const markup = renderForm(saved(channels[1]), 22);
  for (const label of ['Homeserver URL', 'Access token', 'Room ID']) assert.ok(markup.includes(label), label);
  assert.equal(inputWith(markup, 'value="private-matrix-token"').get('type'), 'password');
  assert.equal(text(markup).includes('private-matrix-token'), false, 'the token is never page text');
  assert.ok(markup.includes('value="!domainless_hash"'));
  const words = text(markup);
  for (const phrase of ['unencrypted', 'already joined', 'never logs in', 'room ID pick the destination']) assert.ok(words.includes(phrase), phrase);
  assert.equal(/E2EE|encrypted rooms are supported|password login/iu.test(words), false, 'no login or encryption claim');
});
check('new provider fields warn before removing a stored secret and show their own errors', () => {
  const draft = form.channelDraft({ name: 'Blank', enabled: true, config: { kind: 'homeassistant' } });
  const fields = (kind, errors) => plain(renderToStaticMarkup(React.createElement(MessagingFields, {
    kind, draft, patch() {}, errors, stored: () => true, saving: false, formId: 'f', apiOpen: false, onApiToggle() {},
  })));
  for (const [kind, errors] of [['homeassistant', { serverUrl: 'E-server', accessToken: 'E-token', eventType: 'E-event' }], ['matrix', { serverUrl: 'E-server', accessToken: 'E-token', roomId: 'E-room' }]]) {
    const markup = fields(kind, errors);
    assert.ok(markup.includes('Saving removes the stored value.'), kind);
    for (const message of Object.values(errors)) assert.ok(markup.includes(message), `${kind} ${message}`);
    assert.ok(text(markup).includes('Destination.'), `${kind} still explains destination identity inside its contextual reference disclosure`);
  }
});
check('compound ntfy and Pushover errors identify and describe the corrective input once', () => {
  const cases = [
    { kind: 'ntfy', changes: { ntfyAuth: 'basic', ntfyUser: 'fixture-user', ntfyPass: '' }, key: 'ntfyAuth', problem: messaging.ntfyLoginProblem, label: 'Password' },
    { kind: 'pushover', changes: { poPriority: '2', poRetry: '60', poExpire: '30' }, key: 'emergency', problem: messaging.pushoverEmergencyProblem, label: 'Keep repeating for (seconds)' },
  ];
  for (const item of cases) {
    const draft = { ...form.channelDraft(undefined), ...item.changes }, issue = item.problem(draft);
    const markup = plain(renderToStaticMarkup(React.createElement(MessagingFields, { kind: item.kind, draft, patch() {}, errors: { [item.key]: issue.message }, stored: () => false, saving: false, formId: 'f', apiOpen: false, onApiToggle() {} })));
    const labels = [...markup.matchAll(/<label[^>]*for="([^"]+)"[^>]*>([\s\S]*?)<\/label>/gu)];
    const label = labels.find(match => text(match[2]).trim() === item.label);
    assert.ok(label, item.label);
    const attrs = attributes([...markup.matchAll(/<input\b[^>]*>/gu)].find(match => attributes(match[0]).get('id') === label[1])[0]);
    assert.equal(attrs.get('aria-invalid'), 'true');
    const id = attrs.get('aria-describedby').split(' ').find(id => id.endsWith('-e'));
    assert.ok(id && markup.includes(`id="${id}"`));
    assert.equal(text(markup).split(issue.message).length - 1, 1, 'one shared message, linked to the corrective field');
  }
});
check('plain SMTP keeps retained credentials reachable and offers explicit draft removal', () => {
  const config = { kind: 'smtp', smtp: { server: 'smtp.example.test', tlsMode: 'plain', username: 'fixture-user', password: 'private-fixture-password' } };
  const markup = renderForm(saved(config), 22);
  assert.ok(text(markup).includes('Remove SMTP credentials'));
  assert.ok(markup.includes('value="fixture-user"'));
  assert.equal(inputWith(markup, 'value="private-fixture-password"').get('type'), 'password');
  assert.ok(!text(markup).includes('private-fixture-password'));
});
check('multi-destination preview results retain distinct full identities and fallback attempt IDs', () => {
  const attempts = [
    { id: 'attempt-one', provider: 'smtp', state: 'accepted', destinationId: 'destination-shared-prefix-one' },
    { id: 'attempt-two', provider: 'smtp', state: 'failed', destinationId: 'destination-shared-prefix-two', errorCode: 'recipient_rejected' },
    { id: 'attempt-three', provider: 'smtp', state: 'unknown' },
  ];
  const markup = text(renderToStaticMarkup(React.createElement(PreviewOutcomeList, { attempts })));
  for (const identity of ['Destination destination-shared-prefix-one', 'Destination destination-shared-prefix-two', 'Attempt attempt-three']) assert.ok(markup.includes(identity), identity);
});
check('email preview labels follow acknowledgement IDs through later outcome reordering', () => {
  const config = { kind: 'smtp', recipients: [{ email: 'z-last@example.test' }, { email: 'a-first@example.test' }] };
  const initial = [{ id: 'first', provider: 'smtp', state: 'queued' }, { id: 'second', provider: 'smtp', state: 'queued' }];
  const recipientLabels = previewRecipientLabels(config, initial);
  assert.equal(recipientLabels.get('first'), 'z-last@example.test');
  const attempts = [{ ...initial[1], state: 'failed' }, { ...initial[0], state: 'accepted' }];
  const markup = renderToStaticMarkup(React.createElement(PreviewOutcomeList, { attempts, recipientLabels }));
  const rows = [...markup.matchAll(/<li[^>]*>([\s\S]*?)<\/li>/gu)].map(match => text(match[1]));
  assert.ok(rows[0].includes('a-first@example.test') && rows[0].includes('Failed'));
  assert.ok(rows[1].includes('z-last@example.test') && rows[1].includes('Accepted'));
  assert.equal(previewRecipientLabels(config, initial.slice(1)).size, 0, 'ambiguous response counts get IDs, not guessed addresses');
  assert.equal(previewRecipientLabels(config, initial.map(item => ({ ...item, provider: 'webhook' }))).size, 0);
});
check('alert events render the stored selection, editable on protocol 22', () => {
  let fieldset = eventsFieldset(renderForm(saved(channels[0], ['down', 'recovery']), 22));
  assert.equal(attributes(openTag(fieldset)).has('disabled'), false);
  let boxes = eventBoxes(fieldset);
  assert.ok(boxes['Down alerts'].has('checked') && !boxes.Repeats.has('checked') && boxes.Recoveries.has('checked'));
  assert.ok(text(fieldset).includes('Needs down alerts'));
  boxes = eventBoxes(eventsFieldset(renderForm(undefined, 22)));
  assert.ok(Object.values(boxes).every(box => box.has('checked')), 'a new channel starts with all three');
  const markup = renderForm(undefined, 22);
  for (const kind of ['homeassistant', 'matrix']) assert.equal(providerOption(markup, kind).has('disabled'), false, kind);
  assert.equal(text(markup).includes('Needs peer protocol'), false);
});
check('older or unreported protocol: read-only events with an upgrade note, and the new providers gated', () => {
  for (const protocol of [21, undefined]) {
    const markup = renderForm(undefined, protocol);
    const fieldset = eventsFieldset(markup);
    assert.ok(attributes(openTag(fieldset)).has('disabled'), `events locked at ${protocol}`);
    const words = text(fieldset);
    assert.ok(words.includes('Choosing events needs peer protocol 22') && words.includes('Upgrade every node in the cluster together'));
    assert.ok(words.includes(protocol ? 'reports peer protocol 21' : 'report a peer protocol'));
    for (const kind of ['homeassistant', 'matrix']) assert.ok(providerOption(markup, kind).has('disabled'), `${kind} gated at ${protocol}`);
    for (const kind of ['discord', 'slack', 'webhook']) assert.equal(providerOption(markup, kind).has('disabled'), false, kind);
  }
  const stale = renderForm(saved(channels[0]), 21);
  assert.ok(text(stale).includes('Home Assistant') && text(stale).includes('Change provider'), 'a stored provider remains identified while its chooser is collapsed');
  assert.ok(text(stale).includes('Home Assistant needs peer protocol 22'));
});
check('channel list summarises a narrowed event selection and shows no credential', () => {
  assert.equal(eventsSummary(undefined), undefined);
  assert.equal(eventsSummary(['down', 'repeat', 'recovery']), undefined);
  assert.equal(eventsSummary(['down', 'recovery']), 'down alerts and recoveries only');
  assert.equal(eventsSummary(['recovery']), 'recoveries only');
  const markup = render(React.createElement(ChannelsView), [{ channel: saved(channels[0], ['down', 'recovery']), configConflict: false }, { channel: saved(channels[1]), configConflict: false }], 22);
  assert.equal(text(markup).split('down alerts and recoveries only').length - 1, 1);
  for (const secret of ['private-ha-token', 'private-matrix-token']) assert.equal(markup.includes(secret), false, secret);
});
check('cached channel data has a local refresh warning and retry in both list and editor, including uncertain absence', () => {
  const channel = saved(channels[0]);
  const stale = ctx([{ channel, configConflict: false }], 22);
  stale.channels.error = new apiModel.ApiFailure(503, 'fixture refresh failure');
  const markup = element => plain(renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: stale }, element)));
  for (const element of [React.createElement(ChannelsView), React.createElement(ChannelFormView, { id: channel.id })]) {
    const html = markup(element);
    assert.ok(text(html).includes('Channels could not be refreshed'));
    assert.ok(text(html).includes('Showing the last loaded channel data'));
    assert.ok(text(html).includes('Retry refresh'));
  }
  const missing = markup(React.createElement(ChannelFormView, { id: 'not-in-cached-list' }));
  assert.ok(text(missing).includes('Channel could not be confirmed'));
  assert.ok(!text(missing).includes('Channel not found'));
  stale.channels.list = undefined;
  stale.channels.map = new Map();
  const unloaded = markup(React.createElement(ChannelFormView, { id: 'unloaded' }));
  assert.ok(text(unloaded).includes('Channels could not be loaded'));
  assert.ok(text(unloaded).includes('Retry loading'));
  assert.ok(!text(unloaded).includes('Loading the channel from'));
});
check('unknown stored Home Assistant and Matrix keys survive an unrelated edit', () => {
  for (const config of channels) {
    const extended = { ...config, [config.kind]: { ...config[config.kind], futureOption: 'kept' } };
    const channel = saved(extended);
    const built = form.buildChannel({ ...form.channelDraft(channel), name: 'Renamed' }, channel, 'enhanced', 22);
    assert.ok(built.input, JSON.stringify(built.errors));
    assert.deepEqual(built.input.config, extended);
    assert.equal(built.input.events, undefined, 'the default selection is not written');
  }
});
check('both test confirmations warn that a Home Assistant test fires a real event', () => {
  const source = readFileSync(resolve(root, 'apps/web/src/views/Channels.tsx'), 'utf8');
  assert.equal(source.split('<HaTestWarning eventType=').length - 1, 2);
  // Council copy is shorter; the real-device, test-kind and duplicate warning remains explicit.
  assert.match(source, /test event can switch real devices/u);
  assert.match(source, /kind=test/u);
  assert.match(source, /Duplicates are possible: use repeat-safe actions/u);
});
check('Grafana dashboard: classic JSON, scoped variables and the exact exported metric names', () => {
  const dash = JSON.parse(readFileSync(resolve(root, 'deploy/observability/grafana-dashboard.json'), 'utf8'));
  assert.equal(dash.id, null);
  assert.ok(typeof dash.uid === 'string' && dash.schemaVersion >= 36 && Array.isArray(dash.panels));
  const vars = Object.fromEntries(dash.templating.list.map(variable => [variable.name, variable]));
  assert.equal(vars.DS_PROMETHEUS.type, 'datasource');
  assert.equal(vars.DS_PROMETHEUS.query, 'prometheus');
  assert.equal(vars.DS_PROMETHEUS.current.value, 'uptime-canary-prometheus');
  assert.match(readFileSync(resolve(root, 'deploy/observability/grafana-datasource.yml'), 'utf8'), /uid: uptime-canary-prometheus/u);
  assert.equal(vars.job.current.value, 'uptime-canary');
  assert.equal(Boolean(vars.view_node.multi || vars.view_node.includeAll), false, 'one viewing node at a time');
  assert.match(vars.view_node.query, /job=~"\$job"/u);
  assert.match(vars.monitor_id.query, /job=~"\$job", view_node="\$view_node"/u);
  const exported = new Set([...readFileSync(resolve(root, 'apps/node-go/internal/canary/metrics.go'), 'utf8').matchAll(/help\("([a-z_]+)"/gu)].map(([, name]) => `uptime_canary_${name}`));
  assert.equal(exported.size, 6);
  const panels = dash.panels.filter(panel => panel.type !== 'row');
  const used = new Set();
  for (const panel of panels) {
    assert.equal(panel.datasource.uid, '${DS_PROMETHEUS}', panel.title);
    assert.equal(String(panel.fieldConfig?.defaults?.unit ?? '').startsWith('percent'), false, `${panel.title}: no percentage`);
    for (const target of panel.targets) {
      const expr = target.expr;
      assert.equal(target.datasource.uid, '${DS_PROMETHEUS}');
      assert.equal(/\b(sum|avg|avg_over_time|increase|rate|absent)\b|\* *100|%|or\s+vector\(/u.test(expr), false, `${panel.title}: no summed views, SLA arithmetic or filled gaps`);
      assert.equal(/\{\{(?!monitor_id\}\}|probe_node\}\}|peer_node\}\}|instance\}\}|state\}\}|verified\}\})/u.test(target.legendFormat), false, `${panel.title}: legends are IDs`);
      for (const [selector, name, labels] of expr.matchAll(/(uptime_canary_[a-z_]+)\{([^}]*)\}/gu)) {
        assert.ok(exported.has(name), `${name} is exported by metrics.go`);
        used.add(name);
        assert.match(labels, /job=~"\$job", view_node="\$view_node"/u, selector);
        if (name !== 'uptime_canary_peer_reachable') assert.match(labels, /monitor_id=~"\$monitor_id"/u, selector);
      }
      if (!expr.includes('uptime_canary_')) assert.equal(expr, 'up{job=~"$job"}', 'only Prometheus up is outside the exporter');
    }
  }
  assert.deepEqual([...used].sort(), [...exported].sort(), 'every exported metric is charted');
  const timelines = panels.filter(panel => panel.type === 'state-timeline' && /_state\{/u.test(panel.targets[0].expr));
  assert.equal(timelines.length, 2);
  for (const panel of timelines) {
    const expr = panel.targets[0].expr;
    const by = expr.match(/^max by \(([^)]*)\)/u)[1].split(',').map(label => label.trim());
    assert.ok(!by.includes('state') && by.includes('view_node') && by.includes('monitor_id'), `${panel.title}: the state label is encoded, then dropped`);
    const codes = [...expr.matchAll(/state="([a-z]+)"\} \* (\d)/gu)].map(([, state, code]) => [state, code]);
    const options = panel.fieldConfig.defaults.mappings[0].options;
    assert.equal(new Set(codes.map(([, code]) => code)).size, codes.length, 'one code per state');
    assert.deepEqual(Object.keys(options).sort(), codes.map(([, code]) => code).sort(), 'every mapping is a state code');
    for (const [state, code] of codes) assert.equal(options[code].text.toLowerCase(), state);
    for (const state of ['up', 'down', 'unknown', 'pending']) assert.ok(codes.some(([name]) => name === state), `${panel.title}: ${state}`);
    assert.equal('0' in options, false, 'no zero-means-down coding');
    assert.equal(panel.fieldConfig.defaults.custom.spanNulls, false, 'gaps stay gaps');
  }
  assert.ok(timelines.some(panel => panel.targets[0].expr.includes('uptime_canary_monitor_state') && panel.targets[0].expr.includes('state="paused"')));
  const scrape = panels.find(panel => panel.targets.some(target => target.expr === 'up{job=~"$job"}'));
  assert.ok(scrape && /not monitored-site health/u.test(scrape.description), 'exporter reachability is labelled apart from site health');
});
check('save readback compares writable settings and credentials, ignoring resource metadata and default encoding', () => {
  const input = { name: 'Automation', enabled: true, config: channels[0] };
  const stored = { ...input, id: 'stored', revision: 'revision', updatedAt: 1, isDefault: false, events: ['recovery', 'down', 'repeat'] };
  assert.ok(sameChannelSettings(stored, input));
  assert.ok(!sameChannelSettings(stored, { ...input, config: { ...channels[0], homeassistant: { ...channels[0].homeassistant, accessToken: 'different-private-value' } } }));
  for (const change of [{ enabled: false }, { name: 'Other' }, { events: ['recovery'] }, { isDefault: true }, { futureKey: 1 }]) assert.ok(!sameChannelSettings(stored, { ...input, ...change }));
  assert.ok(sameChannelSettings({ ...stored, futureKey: { a: 1, b: 2 } }, { ...input, futureKey: { b: 2, a: 1 } }));
});
// The actual API helpers run against bounded in-memory HTTP responses, never network or a database.
{
  const previousFetch = globalThis.fetch, previousLocation = globalThis.location;
  const channel = { id: 'stored', revision: 'new-revision', updatedAt: 1, name: 'Automation', enabled: true, config: channels[0] };
  let response;
  globalThis.location = new URL('https://dashboard.example.test');
  globalThis.fetch = async () => new Response(JSON.stringify(response), { status: 200, headers: { 'Content-Type': 'application/json' } });
  try {
    const client = new apiModel.CanaryApi('https://node.example.test', 'private-fixture-token');
    response = channel; assert.equal((await client.createChannel(channel)).id, 'stored');
    assert.equal((await client.updateChannel('stored', channel)).revision, 'new-revision');
    for (response of [null, {}, { ...channel, id: '' }, { ...channel, revision: '' }, { ...channel, config: {} }, { ...channel, deleted: true }, { ...channel, events: [['down']] }]) {
      await assert.rejects(() => client.createChannel(channel), error => error instanceof apiModel.ApiFailure && error.status === 200);
    }
    response = { ...channel, id: 'another-channel' };
    await assert.rejects(() => client.updateChannel('stored', channel), error => error instanceof apiModel.ApiFailure && error.status === 200);
    for (response of [null, {}, [{ channel }], [{ channel, configConflict: false }, { channel, configConflict: false }]]) {
      await assert.rejects(() => client.channels(), error => error instanceof apiModel.ApiFailure && error.status === 200);
    }
    response = [{ channel, configConflict: false }]; assert.equal((await client.channels())[0].channel.id, 'stored');
    response = []; assert.deepEqual(await client.channels(), []);
    check('save acknowledgements and catalogue reconciliation reject unreadable identities and malformed lists', () => {});
  } finally { globalThis.fetch = previousFetch; globalThis.location = previousLocation; }
}
console.log(`${passed} automation integration contract checks passed`);
scope.unregister();
