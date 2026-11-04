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
    assert.ok(markup.includes('id-note'), `${kind} names what picks the destination`);
  }
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
  for (const kind of ['homeassistant', 'matrix']) assert.equal(inputWith(markup, `value="${kind}"`).has('disabled'), false, kind);
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
    for (const kind of ['homeassistant', 'matrix']) assert.ok(inputWith(markup, `value="${kind}"`).has('disabled'), `${kind} gated at ${protocol}`);
    for (const kind of ['discord', 'slack', 'webhook']) assert.equal(inputWith(markup, `value="${kind}"`).has('disabled'), false, kind);
  }
  const stale = renderForm(saved(channels[0]), 21);
  const radio = inputWith(stale, 'value="homeassistant"');
  assert.ok(radio.has('checked') && !radio.has('disabled'), 'a stored provider stays selected');
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
  assert.match(source, /Automations that trigger on it run and can switch real devices/u);
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
console.log(`${passed} automation integration contract checks passed`);
scope.unregister();
