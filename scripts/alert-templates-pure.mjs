// Pure checks for alert message template presets and the channel editor's sample preview: apps/web/src/alertTemplates.ts
// (grammar mirror, presets, sample events, prepared message, Discord's fixed text), views/MessageTemplates.tsx (rendered with
// React's static renderer) and views/Channels.tsx (channelFormModel and the form with a stand-in Canary context). The node
// rules they mirror are pinned by reading its source as text: delivery/validate.go, delivery/http.go, delivery/messaging.go,
// notifyhttp/providers.go and config.go, canary/notification_targets.go, notification_provider.go and delivery_status.go.
// r2 adds peer protocol 20 (root's accepted contract, .canary/alert-template-backend-v2.md): the capability gate, the eight
// added placeholders, Discord wording and cards, and the form's save rules, plus pins on root's Go change for them. Groups
// marked "(protocol 20)" need that Go change; every r1 assertion is kept except two, each commented where root's contract
// changes the node on purpose.
// Imports the web source in place through one tsx namespace: no copy, scratch, network, browser or output files, and no
// message is sent anywhere. Root runs it under the guard:
//   python scripts/bounded_validation.py -- node scripts/alert-templates-pure.mjs   (exit 1 on any failure)
import { readdirSync, readFileSync } from 'node:fs';
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
const calls = [];
globalThis.fetch = async url => { calls.push(String(url)); throw new Error('no network in this fixture'); };
globalThis.document ??= { addEventListener() {} };
const reactModule = await import('react');
const React = reactModule.default ?? reactModule;
globalThis.React ??= React;
const { register } = await import('tsx/esm/api');
const { renderToStaticMarkup } = await import('react-dom/server');
const scope = register({ namespace: 'alert-templates-pure', tsconfig: resolve(root, 'apps/web/tsconfig.json') });
const load = file => scope.import(pathToFileURL(resolve(root, 'apps/web/src', file)).href, import.meta.url);
const t = await load('alertTemplates.ts');
const messaging = await load('messaging.ts');
const { MessageTemplates } = await load('views/MessageTemplates.tsx');
const { channelFormModel: cf, ChannelFormView } = await load('views/Channels.tsx');
const { CanaryContext } = await load('context.tsx');

let failed = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.message}`); } };
const goSource = file => readFileSync(resolve(root, 'apps/node-go/internal', file), 'utf8').replace(/\r\n/gu, '\n');
const source = file => readFileSync(resolve(root, 'apps/web/src', file), 'utf8').replace(/\r\n/gu, '\n');
const raw = String.raw;
// React's static markup separates adjacent text nodes with <!-- -->; read text without them.
const plain = markup => markup.replaceAll('<!-- -->', '');
const attributes = tag => {
  const found = new Map();
  for (const [, name, value] of tag.replace(/^<[a-z]+/iu, '').matchAll(/\s([^\s"'<>\/=]+)(?:="([^"]*)")?/gu)) found.set(name.toLowerCase(), value ?? '');
  return found;
};
const count = (text, needle) => text.split(needle).length - 1;
const KINDS = ['slack', 'telegram', 'ntfy', 'gotify', 'pushover'];
const EVENTS = ['down', 'repeat', 'recovery', 'test'];
const fieldsOf = preset => ({ subject: preset.subject, text: preset.text, html: '' });
const preset = id => t.PRESETS.find(entry => entry.id === id);

await check('node grammar as read: six placeholders, unknown tokens kept, HTML values escaped, then the built-in fallbacks', () => {
  const validate = goSource('delivery/validate.go');
  const render = validate.slice(validate.indexOf('func renderTemplate('), validate.indexOf('var out strings.Builder'));
  assert.ok(render.length > 100, 'renderTemplate found');
  // r2, accepted change: peer protocol 20 adds eight names to the same values map, so every key is read whatever its value
  // expression (r1 required `m.Field`). The six keep r1's order and come first; exactly the eight follow, in any order.
  const names = [...render.matchAll(/"([a-z_]+)":\s/gu)].map(match => match[1]);
  assert.deepEqual(names.slice(0, 6), [...t.PLACEHOLDER_NAMES], 'the six, in the same order');
  assert.deepEqual(names.slice(6).sort(), [...t.ADDED_PLACEHOLDER_NAMES].sort(), 'then the eight protocol 20 names');
  assert.match(validate, /if !exists \{\n\t+value = token\n\t+\} else if escape \{\n\t+value = html\.EscapeString\(value\)/u);
  for (const line of [
    'm.Subject, err = renderTemplate(c.Templates.Subject, m, false, 512)', 'm.Text, err = renderTemplate(c.Templates.Text, m, false, MaxBodyBytes)',
    'm.HTML, err = renderTemplate(c.Templates.HTML, m, true, MaxBodyBytes)', 'm.Subject = strings.TrimSpace(m.EventKind + " " + m.MonitorName)',
    'm.Subject = "Uptime Canary notification"', 'm.Text = m.ObservationsSummary', 'm.Text = m.Subject',
  ]) assert.ok(validate.includes(line), line);
  assert.match(goSource('delivery/delivery.go'), /MaxBodyBytes\s+= 64 << 10/u);
  assert.ok(goSource('delivery/messaging.go').includes('c.Templates.HTML != ""'), 'chat and push channels refuse an HTML template');
  const http = goSource('delivery/http.go');
  assert.match(http, /if m\.HTML != "" \{\n\t+body\["htmlContent"\] = m\.HTML\n\t+\} else \{\n\t+body\["textContent"\] = m\.Text/u, 'Brevo gets one body type');
});

await check('rendering mirrors the node: one pass, exact tokens only, values never re-read, Go html.EscapeString characters', () => {
  const v = t.sampleValues('down');
  assert.equal(t.renderTemplate(t.PLACEHOLDER_NAMES.map(name => `{{${name}}}`).join('|'), v, false), t.PLACEHOLDER_NAMES.map(name => v[name]).join('|'));
  for (const kept of ['{{service_url}}', '{{ monitor_name }}', '{{}}', '{{monitor_name', '{{{monitor_name}}}', '{{Monitor_Name}}', '{monitor_name}']) assert.equal(t.renderTemplate(kept, v, false), kept, kept);
  assert.equal(t.renderTemplate('}}{{monitor_name}}{{', v, false), '}}Example API{{');
  assert.equal(t.renderTemplate('{{monitor_name}}', { ...v, monitor_name: '{{monitor_id}}' }, false), '{{monitor_id}}', 'a value is inserted once, never expanded');
  // Go's html.EscapeString (standard library) escapes exactly < > & ' and ".
  const hostile = { ...v, monitor_name: `<b>&"'x` };
  assert.equal(t.renderTemplate('<p>{{monitor_name}}</p>', hostile, true), '<p>&lt;b&gt;&amp;&#34;&#39;x</p>');
  assert.equal(t.renderTemplate('<p>{{monitor_name}}</p>', hostile, false), `<p><b>&"'x</p>`, 'text templates are not escaped');
  assert.deepEqual(t.templateTokens('{{monitor_name}} {{ monitor_name }} {{service_url}} {{monitor_name}} {{service_url}}'), { known: ['monitor_name'], literal: ['{{ monitor_name }}', '{{service_url}}'] });
});

await check('presets: Built-in, Compact and Detailed use only real placeholders and fit every chat, push and email provider with every sample event', () => {
  assert.deepEqual(t.PRESETS.map(entry => [entry.id, entry.label]), [['builtin', 'Built-in'], ['compact', 'Compact'], ['detailed', 'Detailed']]);
  assert.deepEqual(fieldsOf(preset('builtin')), { subject: '', text: '', html: '' }, 'built-in means empty templates');
  for (const entry of t.PRESETS) {
    assert.deepEqual(t.templateTokens(entry.subject + entry.text).literal, [], `${entry.id} uses only placeholders the node fills in`);
    assert.equal(/[\x00-\x1f\x7f]/u.test(entry.subject), false, `${entry.id} title is one line`);
    for (const kind of KINDS) {
      assert.equal(messaging.templateProblem(kind, entry.subject, entry.text), undefined, `${entry.id} on ${kind}`);
      for (const event of EVENTS) {
        const fit = t.chatFit(kind, t.prepareMessage(fieldsOf(entry), t.sampleValues(event), 'chat'));
        assert.ok(fit.titleFits && fit.textFits, `${entry.id} ${event} fits ${kind}: ${JSON.stringify(fit)}`);
      }
    }
    for (const event of EVENTS) assert.equal(t.prepareMessage(fieldsOf(entry), t.sampleValues(event), 'email').tooLong, undefined);
  }
  const down = t.sampleValues('down');
  assert.equal(t.prepareMessage(fieldsOf(preset('compact')), down, 'chat').subject, 'Example API: down');
  assert.equal(t.prepareMessage(fieldsOf(preset('detailed')), down, 'chat').subject, '[down] Example API');
  assert.ok(t.prepareMessage(fieldsOf(preset('detailed')), t.sampleValues('recovery'), 'chat').text.includes('Node results:\nnew-york: up - HTTP 200 checks passed\nlondon: up'));
});

await check('choosing presets: only explicit, custom text needs confirmation, email presets clear HTML, chat keeps it for the save warning, Discord and webhooks untouched', () => {
  const compact = fieldsOf(preset('compact'));
  assert.equal(t.presetOf({ subject: '', text: '', html: '' }, 'chat'), 'builtin');
  assert.equal(t.presetOf(compact, 'chat'), 'compact');
  assert.equal(t.presetOf({ ...compact, text: `${compact.text} ` }, 'chat'), 'custom', 'one edited character is custom');
  assert.equal(t.presetOf({ ...compact, html: '<p>x</p>' }, 'email'), 'custom', 'email HTML is the user’s own');
  assert.equal(t.presetOf({ ...compact, html: '<p>x</p>' }, 'chat'), 'compact', 'chat drops HTML on save, so it doesn’t count');
  assert.equal(t.replacesCustom(compact, 'chat'), false);
  assert.equal(t.replacesCustom({ subject: 'Mine', text: '', html: '' }, 'chat'), true);
  const custom = { subject: 'Mine {{monitor_name}}', text: 'Body', html: '<p>Mine</p>' };
  assert.deepEqual(t.applyPreset(custom, 'detailed', 'email'), { ...fieldsOf(preset('detailed')), html: '' });
  assert.deepEqual(t.applyPreset(custom, 'compact', 'chat'), { ...compact, html: '<p>Mine</p>' });
  assert.deepEqual(t.applyPreset(custom, 'builtin', 'chat'), { subject: '', text: '', html: '<p>Mine</p>' });
  assert.equal(t.applyPreset(custom, 'compact', 'fixed'), custom, 'templates aren’t used there, so nothing changes');
  assert.ok(t.sameFields(custom, { ...custom }) && !t.sameFields(custom, { ...custom, html: '' }));
  assert.equal(t.templateSummary('slack', compact), 'Compact preset');
  assert.equal(t.templateSummary('smtp', { subject: '', text: '', html: '' }), 'Built-in wording');
  assert.equal(t.templateSummary('smtp', custom), 'Custom subject · text · HTML');
  assert.equal(t.templateSummary('discord', custom), 'Fixed Discord message');
  assert.equal(t.templateSummary('webhook', custom), 'Fixed JSON event');
  assert.equal(t.templateFamily('mandrill'), 'email');
  assert.equal(t.templateFamily('pushover'), 'chat');
  assert.equal(t.templateFamily('discord'), 'fixed');
});

const slack = templates => ({ id: 's1', revision: 'rev-s1', updatedAt: 1, name: 'Ops Slack', enabled: true, config: { kind: 'slack', slack: { webhookURL: 'https://hooks.slack.com/services/T0AAA/B0BBB/fixture-part' }, ...(templates ? { templates } : {}) } });
await check('channel form: stored custom templates round-trip untouched; an applied preset is what saves; a provider change never rewrites the user’s text', () => {
  const own = { subject: 'Mine {{monitor_name}}', text: 'Custom body\n{{observations_summary}}' };
  const stored = slack(own);
  const draft = cf.channelDraft(stored);
  let built = cf.buildChannel({ ...draft, name: 'Ops Slack 2' }, stored);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config.templates, own, 'no preset is applied by opening or renaming');
  const applied = t.applyPreset({ subject: draft.subject, text: draft.text, html: draft.html }, 'compact', t.templateFamily('slack'));
  built = cf.buildChannel({ ...draft, ...applied }, stored);
  assert.deepEqual(built.input.config.templates, { subject: preset('compact').subject, text: preset('compact').text });
  built = cf.buildChannel({ ...draft, ...t.applyPreset(applied, 'builtin', 'chat') }, stored);
  assert.equal('templates' in built.input.config, false, 'Built-in saves no templates');
  const mail = { id: 'e1', revision: 'rev-e1', updatedAt: 1, name: 'Mail', enabled: true, config: { kind: 'smtp', smtp: { server: 'smtp.example.test', port: 587, tlsMode: 'starttls' }, sender: { email: 'alerts@example.test' }, recipients: [{ email: 'ops@example.test' }], templates: { subject: 'S {{monitor_name}}', text: 'T {{observations_summary}}', html: '<p>{{monitor_name}}</p>' } } };
  const md = cf.channelDraft(mail);
  built = cf.buildChannel({ ...md, kind: 'slack', slackUrl: 'https://hooks.slack.com/services/T0AAA/B0BBB/fixture-part' }, mail);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config.templates, { subject: 'S {{monitor_name}}', text: 'T {{observations_summary}}' }, 'only the HTML is dropped, as the form warns');
  built = cf.buildChannel({ ...md, kind: 'discord', discordUrl: 'https://discord.com/api/webhooks/123456789/fixture-part' }, mail);
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config.templates, mail.config.templates, 'kept as stored, though Discord doesn’t use them');
});

await check('sample events: the node’s four attempt kinds, made-up values in the node’s shapes, and a test exactly as the node fills it in', () => {
  assert.deepEqual(t.SAMPLE_EVENTS.map(entry => [entry.value, entry.label]), [['down', 'Down'], ['repeat', 'Still down'], ['recovery', 'Recovery'], ['test', 'Test']]);
  const provider = goSource('canary/notification_provider.go');
  for (const kind of ['recovery', 'repeat', 'test']) assert.ok(provider.includes(`a.Kind == "${kind}"`), kind);
  const targets = goSource('canary/notification_targets.go');
  assert.ok(targets.includes('lines = append(lines, o.NodeID+": "+o.State+" - "+o.Message)'));
  assert.ok(targets.includes(raw`ObservationsSummary: strings.Join(lines, "\n")`));
  const status = goSource('canary/delivery_status.go');
  assert.ok(status.includes('"kind": "test", "channelId": c.ID, "monitor": Identity{Name: "Notification test"}'), 'a test names no monitor');
  assert.ok(status.includes('"observations": []Observation{}'), 'a test has no observations');
  assert.ok(status.includes('a := attempt{ID: newID(), Kind: "test",'), 'a test attempt has no monitor or incident');
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
  for (const event of ['down', 'repeat', 'recovery']) {
    const v = t.sampleValues(event);
    assert.equal(v.event_kind, event);
    assert.equal(v.monitor_name, 'Example API');
    assert.match(v.monitor_id, uuid); assert.match(v.incident_id, uuid);
    assert.match(v.idempotency_key, /^[0-9a-f]{64}$/u, 'alert attempt IDs are digests');
    assert.equal(v.observations_summary.split('\n').length, 3, 'one line per sample node');
    for (const line of v.observations_summary.split('\n')) assert.match(line, event === 'recovery' ? /^[a-z-]+: up - HTTP 200 checks passed$/u : /^[a-z-]+: down - .+$/u);
  }
  assert.equal(new Set(['down', 'repeat', 'recovery'].map(event => t.sampleValues(event).idempotency_key)).size, 3, 'each attempt has its own ID');
  assert.deepEqual(t.sampleValues('test'), { event_kind: 'test', monitor_name: 'Notification test', monitor_id: '', incident_id: '', observations_summary: '', idempotency_key: t.SAMPLE.keys.test });
  assert.match(t.SAMPLE.keys.test, uuid);
});

await check('prepared message mirrors the node’s fallbacks, including a test’s text falling back to its title', () => {
  const none = { subject: '', text: '', html: '' };
  let m = t.prepareMessage(none, t.sampleValues('down'), 'chat');
  assert.equal(m.subject, 'down Example API');
  assert.equal(m.text, t.sampleValues('down').observations_summary);
  assert.ok(m.builtInSubject && m.builtInText);
  m = t.prepareMessage(none, t.sampleValues('test'), 'chat');
  assert.deepEqual([m.subject, m.text], ['test Notification test', 'test Notification test']);
  m = t.prepareMessage(fieldsOf(preset('compact')), t.sampleValues('test'), 'chat');
  assert.deepEqual([m.subject, m.text], ['Notification test: test', 'Notification test: test'], 'an empty rendered text falls back');
  m = t.prepareMessage({ subject: '', text: '', html: '<p>{{monitor_name}}</p>' }, t.sampleValues('down'), 'email');
  assert.deepEqual([m.text, m.html], ['', '<p>Example API</p>'], 'with HTML there is no text fallback');
  assert.equal(t.prepareMessage({ subject: '', text: '', html: '<p>x</p>' }, t.sampleValues('down'), 'chat').html, '', 'chat never carries HTML');
  assert.equal(t.prepareMessage({ subject: 'x'.repeat(513), text: '', html: '' }, t.sampleValues('down'), 'email').tooLong, 'subject');
  // The sample summary is 163 characters, so 500 copies are 81,500 bytes, over the 65,536-byte limit.
  assert.equal(t.sampleValues('down').observations_summary.length, 163);
  assert.equal(t.prepareMessage({ subject: '', text: '{{observations_summary}}'.repeat(500), html: '' }, t.sampleValues('down'), 'email').tooLong, 'text');
});

await check('chat layout and limits as notifyhttp sends them: Slack and Telegram combine title and text; custom text over a limit fails, built-in text is shortened', () => {
  const providers = goSource('notifyhttp/providers.go');
  assert.ok(providers.includes('}{Text: escapeSlack(combinedText(m)), Parse: "none"}'));
  assert.ok(providers.includes('}{ChatID: telegramChat(p.ChatID), Text: combinedText(m),'));
  assert.ok(providers.includes('}{p.Topic, m.Title, m.Text, p.Priority, p.Tags}'));
  assert.ok(providers.includes('}{m.Title, m.Text, c.Gotify.Priority}'));
  assert.ok(providers.includes('"message": {m.Text}') && providers.includes('form.Set("title", m.Title)'));
  assert.ok(goSource('notifyhttp/config.go').includes(raw`return m.Title + "\n" + m.Text`));
  assert.deepEqual(KINDS.filter(kind => t.combinesTitle(kind)), ['slack', 'telegram']);
  const long = { subject: '', text: 'y'.repeat(1100), html: '' };
  assert.equal(t.chatFit('pushover', t.prepareMessage(long, t.sampleValues('down'), 'chat')).textFits, false, 'custom text is never shortened');
  assert.equal(t.chatFit('slack', t.prepareMessage({ ...long, subject: 'z'.repeat(251) }, t.sampleValues('down'), 'chat')).titleFits, false);
  const builtIn = t.prepareMessage({ subject: '', text: '', html: '' }, { ...t.sampleValues('down'), observations_summary: 'w'.repeat(5000) }, 'chat');
  assert.equal(t.chatFit('slack', builtIn).textFits, true, 'built-in text is shortened to fit');
  const fit = t.chatFit('slack', t.prepareMessage(fieldsOf(preset('compact')), t.sampleValues('down'), 'chat'));
  assert.equal(fit.used, [...`Example API: down\n${t.sampleValues('down').observations_summary}`].length);
  assert.deepEqual([fit.limit, fit.combined], [4000, true]);
});

await check('Discord’s fixed text is pinned to the node’s formatter, so its preview is exact for the sample values', () => {
  const provider = goSource('canary/notification_provider.go');
  for (const line of [
    raw`"Uptime Canary: %s\nMonitor: %s\nIncident: %s\nOpened: %s\nSender: %s", state, text(m.Name, 240), text(a.IncidentID, 100), time.UnixMilli(i.OpenedAt).UTC().Format(time.RFC3339), text(node.Name+" ("+node.ID+")", 120)`,
    raw`"Uptime Canary: %s\nChannel: %s\nSender: %s", state, text(m.Name, 240), text(node.Name+" ("+node.ID+")", 120)`,
    raw`"\nRecovered: %s", time.UnixMilli(*i.ResolvedAt).UTC().Format(time.RFC3339)`,
    raw`"\n%s: %s — %s", text(s.NodeID, 60), text(s.State, 16), text(s.Message, 220)`,
    'observations[:min(len(observations), 5)]', 'state := "DOWN"', 'state = "RECOVERED"', 'state = "STILL DOWN (repeat " + strconv.FormatInt(a.Slot, 10) + ")"', 'state = "TEST NOTIFICATION"',
  ]) assert.ok(provider.includes(line), line);
  assert.match(provider, /"content":\s+discordBoundText\(content\.String\(\), 2000\)/u);
  const targets = goSource('canary/notification_targets.go');
  // r2, accepted change: r1 pinned `t.ChannelID == "" || t.Provider == "discord"` skipping the template renderer. Root's
  // R2 applies templates to Discord channels on purpose, so only the legacy per-monitor URL's fixed path is pinned here;
  // the protocol 20 group below pins the Discord change itself.
  assert.ok(targets.includes('return deliverNotification('), 'legacy per-monitor webhook URLs keep the fixed path');
  assert.ok(targets.includes('if t.Config.Kind == providerDelivery.Webhook {\n\t\tmessage.APIBody = body'), 'webhooks send the JSON event');
  assert.equal(t.discordFixedText('down', 'Pager'), [
    'Uptime Canary: DOWN', 'Monitor: Example API', 'Incident: b81e4f07-2c9a-4d3e-a6f5-0e7d1c2b9a84', 'Opened: 2026-10-24T08:01:05Z', 'Sender: New York (new-york)',
    'new-york: down — HTTP status 503 is outside accepted ranges', 'london: down — HTTP status 503 is outside accepted ranges', 'home-lab: down — Check cancelled or timed out',
  ].join('\n'));
  const recovery = t.discordFixedText('recovery', 'Pager').split('\n');
  assert.deepEqual([recovery[0], recovery[5], recovery[6]], ['Uptime Canary: RECOVERED', 'Recovered: 2026-10-24T08:08:17Z', 'new-york: up — HTTP 200 checks passed']);
  assert.equal(t.discordFixedText('repeat', 'Pager').split('\n')[0], 'Uptime Canary: STILL DOWN (repeat 1)');
  assert.equal(t.discordFixedText('test', '  Pager \n Ops  '), 'Uptime Canary: TEST NOTIFICATION\nChannel: Pager Ops\nSender: New York (new-york)', 'white space collapses as in the node');
  assert.equal(t.discordFixedText('test', 'x'.repeat(300)).split('\n')[1], `Channel: ${'x'.repeat(239)}…`, 'bounded at 240 UTF-16 units');
});

const renderTemplates = props => plain(renderToStaticMarkup(React.createElement(MessageTemplates, {
  providerLabel: 'Slack', icon: null, channelName: 'Pager', saving: false, formId: 'f1', onChange() {}, ...props,
})));
const radios = (markup, name) => [...markup.matchAll(/<input[^>]*>/gu)].map(match => attributes(match[0])).filter(found => found.get('name') === name);
await check('rendered editor: presets, a clearly labelled sample preview, and every user or sample string escaped, never rendered as HTML', () => {
  const hostile = { subject: '{{monitor_name}} alert', text: 'Look: <img src=x onerror=alert(1)> {{ monitor_name }}\n{{observations_summary}}', html: '' };
  let markup = renderTemplates({ kind: 'slack', fields: hostile });
  assert.equal(markup.includes('<img'), false, 'no tag from the template reaches the page');
  assert.ok(markup.includes('Example API alert\nLook: &lt;img src=x onerror=alert(1)&gt; {{ monitor_name }}\nnew-york: down - HTTP status 503'), 'Slack preview: title, line break, text');
  for (const text of ['Presets', 'Now: your own wording', 'Preview', 'Sample data', 'Sample event', '· illustration', 'Sample down alert', 'Sample values', 'Made up for this preview',
    'Sent as typed, not replaced:', '{{ monitor_name }}', 'Slack gets them as one plain-text message', 'Title: 17 of 250 characters']) assert.ok(markup.includes(text), text);
  assert.equal(count(markup, 'aria-label="Use the '), 3, 'every preset is offered while the text is custom');
  assert.deepEqual(radios(markup, 'f1-pv-event').map(found => [found.get('value'), found.has('checked')]), [['down', true], ['repeat', false], ['recovery', false], ['test', false]]);
  for (const label of ['Down', 'Still down', 'Recovery', 'Test']) assert.ok(markup.includes(`<span>${label}</span>`), label);
  markup = renderTemplates({ kind: 'slack', fields: fieldsOf(preset('compact')) });
  assert.ok(markup.includes('Now: Compact'));
  assert.equal(count(markup, 'In use</span>'), 1);
  assert.deepEqual([...markup.matchAll(/aria-label="Use the ([A-Za-z-]+) preset"/gu)].map(match => match[1]), ['Built-in', 'Detailed']);
  assert.ok(markup.includes('Example API: down\nnew-york: down'));
  markup = renderTemplates({ kind: 'ntfy', providerLabel: 'ntfy', fields: fieldsOf(preset('detailed')) });
  assert.ok(markup.includes('<div class="tpl-msg-title">[down] Example API</div>'), 'ntfy keeps a separate title');
  const html = '<script>alert(1)</script><b>{{monitor_name}}</b>';
  markup = renderTemplates({ kind: 'smtp', providerLabel: 'SMTP', fields: { ...fieldsOf(preset('detailed')), html } });
  assert.equal(markup.includes('<script'), false);
  assert.ok(markup.includes('HTML part, shown as source') && markup.includes('&lt;script&gt;alert(1)&lt;/script&gt;&lt;b&gt;Example API&lt;/b&gt;'));
  assert.ok(markup.includes('Now: your own wording'), 'an HTML template makes it custom');
  assert.ok(markup.includes('Both parts are sent'));
  markup = renderTemplates({ kind: 'brevo', providerLabel: 'Brevo', fields: { ...fieldsOf(preset('detailed')), html } });
  assert.ok(markup.includes('Brevo gets only the HTML part when there is one.'));
});

await check('rendered editor: Discord shows the fixed message without template fields, keeps stored text visible until cleared; webhooks explain the JSON event', () => {
  const none = { subject: '', text: '', html: '' };
  let markup = renderTemplates({ kind: 'discord', providerLabel: 'Discord', fields: none });
  assert.ok(markup.includes('Discord channels send Canary’s fixed message'));
  assert.ok(markup.includes('Uptime Canary: DOWN\nMonitor: Example API'));
  assert.equal(markup.includes('<textarea'), false);
  assert.equal(markup.includes('aria-label="Use the '), false, 'no presets where templates aren’t used');
  assert.equal(markup.includes('Sample values'), false);
  markup = renderTemplates({ kind: 'discord', providerLabel: 'Discord', fields: { subject: '', text: 'Old text {{monitor_name}}', html: '' } });
  assert.ok(markup.includes('Text (unused)') && markup.includes('Old text {{monitor_name}}') && markup.includes('Clear unused templates'));
  markup = renderTemplates({ kind: 'webhook', providerLabel: 'Webhook', fields: none });
  assert.ok(markup.includes('Webhook channels receive Canary’s JSON event'));
  assert.equal(markup.includes('Preview'), false);
});

const catalog = (list = []) => ({ list, map: new Map(list.map(entry => [entry.channel?.id ?? entry.tag?.id, entry])), loading: false, error: undefined, unsupported: false, reload() {} });
const NODES = [{ id: 'a', name: 'Node A', state: 'local', lastSeenAt: null }];
// r2: an optional `build` is the overview's reported binary identity (peer protocol 20); without it the context is r1's.
const ctx = (channels, build) => ({
  role: 'editor', editable: true, admin: false, endpoint: 'a', controlPlane: false, selection: null, search: '',
  overview: { ...(build ? { build } : {}), node: { id: 'a', name: 'Node A' }, now: 1_760_000_000_000, monitors: [], nodes: [], incidents: [], notifications: [], user: { name: 'review', role: 'editor' } },
  monitorMap: new Map(), nodes: NODES, nodeMap: new Map(NODES.map(node => [node.id, node])),
  api: { registeredNode: null, baseUrl: 'http://node-a.test:3100' }, refresh() {}, groups: null, groupMap: new Map(), maintenanceMap: new Map(), stillCurrent: () => true,
  channels: catalog(channels), tags: catalog(),
});
await check('rendered channel form: stored templates open the section with their preset named; a channel without templates keeps it closed', () => {
  const compact = slack({ subject: preset('compact').subject, text: preset('compact').text });
  let markup = plain(renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx([{ channel: compact, configConflict: false }]) }, React.createElement(ChannelFormView, { id: 's1' }))));
  assert.ok(markup.includes('Message templates') && markup.includes('Compact preset') && markup.includes('Sample data'));
  assert.equal(markup.includes('fixture-part<'), false, 'no credential is page text');
  markup = plain(renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx([{ channel: slack(), configConflict: false }]) }, React.createElement(ChannelFormView, { id: 's1' }))));
  assert.ok(markup.includes('Built-in wording'));
  assert.equal(markup.includes('Sample data'), false, 'the closed disclosure renders no preview');
});

// ---------- r2: peer protocol 20

const ENHANCED = { level: 'enhanced', protocol: 20 };
const NONE = { subject: '', text: '', html: '' };
const enhanced = id => fieldsOf(t.ENHANCED_PRESETS.find(entry => entry.id === id));
const DURL = 'https://discord.com/api/webhooks/123456789/fixture-part';
const discordChannel = (webhook = {}, templates) => ({ id: 'd1', revision: 'rev-d1', updatedAt: 1, name: 'Ops Discord', enabled: true, config: { kind: 'discord', webhook: { url: DURL, ...webhook }, ...(templates ? { templates } : {}) } });
const mailChannel = () => ({ id: 'e1', revision: 'rev-e1', updatedAt: 1, name: 'Mail', enabled: true, config: { kind: 'smtp', smtp: { server: 'smtp.example.test', port: 587, tlsMode: 'starttls' }, sender: { email: 'alerts@example.test' }, recipients: [{ email: 'ops@example.test' }], templates: { subject: 'S {{monitor_name}}', text: 'T {{observations_summary}}', html: '<p>{{monitor_name}}</p>' } } });
const DOWN_RESULTS = [
  'new-york: down - HTTP status 503 is outside accepted ranges (182 ms)',
  'london: down - HTTP status 503 is outside accepted ranges (241 ms)',
  'home-lab: down - Check cancelled or timed out',
].join('\n');
const UP_RESULTS = ['new-york: up - HTTP 200 checks passed (96 ms)', 'london: up - HTTP 200 checks passed (88 ms)', 'home-lab: up - HTTP 200 checks passed (12 ms)'].join('\n');

await check('capability: only a reported whole peer protocol of 20 or later enables the added grammar; anything else keeps the six placeholders', () => {
  assert.equal(t.TEMPLATE_PROTOCOL, 20);
  for (const build of [undefined, null, {}]) assert.deepEqual(t.templateCapability(build), { level: 'legacy' }, 'an older node reports no build, and nothing is guessed');
  assert.deepEqual(t.templateCapability({ peerProtocol: 19 }), { level: 'legacy', protocol: 19 });
  assert.deepEqual(t.templateCapability({ peerProtocol: 20 }), { level: 'enhanced', protocol: 20 });
  assert.deepEqual(t.templateCapability({ peerProtocol: 21 }), { level: 'enhanced', protocol: 21 });
  for (const odd of ['20', 20.5, Number.NaN, Number.POSITIVE_INFINITY, -20, 0, true, null]) assert.deepEqual(t.templateCapability({ peerProtocol: odd }), { level: 'legacy' }, String(odd));
  assert.deepEqual([...t.ADDED_PLACEHOLDER_NAMES], ['status', 'monitor_type', 'monitor_target', 'opened_at', 'resolved_at', 'downtime', 'node_results', 'sender_node']);
  assert.deepEqual(t.placeholderNames('legacy'), [...t.PLACEHOLDER_NAMES]);
  assert.deepEqual(t.placeholderNames('enhanced'), [...t.PLACEHOLDER_NAMES, ...t.ADDED_PLACEHOLDER_NAMES]);
  assert.deepEqual(t.presetsFor('legacy'), t.PRESETS);
  assert.deepEqual(['discord', 'webhook', 'slack', 'smtp'].map(kind => t.templateFamily(kind, 'enhanced')), ['discord', 'fixed', 'chat', 'email'], 'webhooks keep the JSON event');
  const v = t.sampleValues('down', 'enhanced');
  assert.equal(t.renderTemplate('{{status}} {{monitor_name}}', v, false), '{{status}} Example API', 'an older node sends the added tokens as typed');
  assert.equal(t.renderTemplate('{{status}} {{monitor_name}}', v, false, 'enhanced'), 'DOWN Example API');
  assert.deepEqual(t.templateTokens('{{status}} {{monitor_name}}'), { known: ['monitor_name'], literal: ['{{status}}'] });
  assert.deepEqual(t.templateTokens('{{status}} {{monitor_name}} {{ status }}', 'enhanced'), { known: ['status', 'monitor_name'], literal: ['{{ status }}'] });
  assert.equal(t.renderTemplate('<i>{{monitor_target}}</i>', { ...v, monitor_target: '<b>&' }, true, 'enhanced'), '<i>&lt;b&gt;&amp;</i>', 'added values are HTML-escaped too');
});

await check('protocol 20 sample values: status words, an origin-only target, UTC times, downtime, latency per node and the sender; a test fills in only status and sender', () => {
  const down = t.sampleValues('down', 'enhanced');
  assert.deepEqual(Object.keys(down).sort(), [...t.PLACEHOLDER_NAMES, ...t.ADDED_PLACEHOLDER_NAMES].sort());
  assert.deepEqual(EVENTS.map(event => t.sampleValues(event, 'enhanced').status), ['DOWN', 'STILL DOWN', 'UP', 'TEST']);
  for (const event of ['down', 'repeat', 'recovery']) {
    const v = t.sampleValues(event, 'enhanced');
    for (const name of t.PLACEHOLDER_NAMES) assert.equal(v[name], t.sampleValues(event)[name], `${event}: {{${name}}} unchanged, observations_summary byte for byte`);
    assert.deepEqual([v.monitor_type, v.monitor_target, v.opened_at, v.sender_node], ['http', 'https://api.example.com', '2026-10-24T08:01:05Z', 'New York']);
  }
  // Privacy: a web check's target is its origin only, never a user, path, query or fragment.
  const target = new URL(down.monitor_target);
  assert.equal(down.monitor_target, `${target.protocol}//${target.host}`);
  assert.deepEqual([target.username, target.password, target.pathname, target.search, target.hash], ['', '', '/', '', '']);
  const repeat = t.sampleValues('repeat', 'enhanced');
  assert.deepEqual([down.resolved_at, down.downtime, repeat.resolved_at, repeat.downtime], ['', '', '', ''], 'recovery only');
  const recovery = t.sampleValues('recovery', 'enhanced');
  assert.deepEqual([recovery.resolved_at, recovery.downtime], ['2026-10-24T08:08:17Z', '7m 12s']);
  assert.equal(down.node_results, DOWN_RESULTS, 'rounded whole milliseconds, left out with its parentheses when nothing was measured');
  assert.equal(recovery.node_results, UP_RESULTS, '95.5 rounds to 96, as Go’s math.Round does');
  assert.equal(repeat.node_results, DOWN_RESULTS);
  for (const v of [down, recovery]) {
    const summary = v.observations_summary.split('\n');
    v.node_results.split('\n').forEach((line, index) => assert.ok(line === summary[index] || (line.startsWith(`${summary[index]} (`) && / \(\d+ ms\)$/u.test(line)), line));
  }
  assert.deepEqual(t.sampleValues('test', 'enhanced'), { ...t.sampleValues('test'), status: 'TEST', monitor_type: '', monitor_target: '', opened_at: '', resolved_at: '', downtime: '', node_results: '', sender_node: 'New York' }, 'monitor_name stays “Notification test”');
  for (const event of EVENTS) {
    const v = t.sampleValues(event, 'enhanced');
    for (const name of t.ADDED_PLACEHOLDER_NAMES) {
      if (name === 'node_results') assert.ok(Buffer.byteLength(v[name]) <= 16_384, `${event} node_results`);
      else assert.ok(Buffer.byteLength(v[name]) <= 512 && !/[\x00-\x1f\x7f]/u.test(v[name]), `${event} ${name}: one line, at most 512 bytes`);
    }
  }
  for (const [ms, text] of [[0, '0s'], [-5000, '0s'], [999, '0s'], [45_000, '45s'], [432_000, '7m 12s'], [432_999, '7m 12s'], [3_600_000, '1h'], [4_020_000, '1h 7m'], [4_021_000, '1h 7m'], [86_401_000, '1d 1s'], [90_061_000, '1d 1h'], [172_800_000, '2d']])
    assert.equal(t.formatDowntime(ms), text, `${ms} ms`);
  assert.equal(t.rfc3339(Date.UTC(2026, 9, 24, 8, 1, 5, 999)), '2026-10-24T08:01:05Z', 'fractions are cut, not rounded');
});

await check('Discord escaping (protocol 20): a backslash before each Markdown character in filled-in values, never in the template’s own text, values inserted once', () => {
  for (const c of '\\*_~`|>#-[]()') assert.equal(t.escapeDiscord(c), `\\${c}`, c);
  for (const c of '.:/@<!=+"\'&{}$%^ ') assert.equal(t.escapeDiscord(c), c, `${c} unchanged`);
  const v = { ...t.sampleValues('down', 'enhanced'), monitor_name: 'my_api [prod] #1 - *beta*' };
  assert.equal(t.renderTemplate('**{{monitor_name}}** - (x)', v, 'discord', 'enhanced'), '**my\\_api \\[prod\\] \\#1 \\- \\*beta\\*** - (x)');
  assert.equal(t.renderTemplate('{{monitor_name}}', { ...v, monitor_name: '{{status}}' }, 'discord', 'enhanced'), '{{status}}', 'a value is never read as a template');
  assert.equal(t.renderTemplate('{{node_results}}', v, 'discord', 'enhanced').split('\n')[0], 'new\\-york: down \\- HTTP status 503 is outside accepted ranges \\(182 ms\\)');
  assert.equal(t.renderTemplate('{{monitor_target}}', v, 'discord', 'enhanced'), 'https://api.example.com', 'nothing in an origin is escaped');
});

await check('protocol 20 presets: the same three, only grammar the node fills in, every provider and event fits, and an earlier preset isn’t the user’s own wording', () => {
  assert.deepEqual(t.ENHANCED_PRESETS.map(entry => [entry.id, entry.label]), t.PRESETS.map(entry => [entry.id, entry.label]));
  assert.deepEqual(fieldsOf(t.ENHANCED_PRESETS[0]), NONE, 'built-in means empty templates');
  for (const entry of t.PRESETS) assert.deepEqual(t.templateTokens(entry.subject + entry.text, 'enhanced').known.filter(name => t.ADDED_PLACEHOLDER_NAMES.includes(name)), [], `older ${entry.id} uses the six only`);
  for (const entry of t.ENHANCED_PRESETS) {
    assert.deepEqual(t.templateTokens(entry.subject + entry.text, 'enhanced').literal, [], `${entry.id} uses only placeholders the node fills in`);
    assert.equal(/[\x00-\x1f\x7f]/u.test(entry.subject), false, `${entry.id} title is one line`);
    for (const kind of KINDS) {
      assert.equal(messaging.templateProblem(kind, t.withoutAddedPlaceholders(entry.subject), t.withoutAddedPlaceholders(entry.text)), undefined, `${entry.id} on ${kind}`);
      for (const event of EVENTS) {
        const fit = t.chatFit(kind, t.prepareMessage(fieldsOf(entry), t.sampleValues(event, 'enhanced'), 'chat', 'enhanced'));
        assert.ok(fit.titleFits && fit.textFits, `${entry.id} ${event} fits ${kind}: ${JSON.stringify(fit)}`);
      }
    }
    for (const event of EVENTS) {
      assert.equal(t.prepareMessage(fieldsOf(entry), t.sampleValues(event, 'enhanced'), 'email', 'enhanced').tooLong, undefined);
      for (const embed of [false, true]) {
        const message = t.discordMessage(fieldsOf(entry), event, 'Pager', embed);
        assert.deepEqual([message.tooLong, message.empty], [undefined, false], `${entry.id} ${event} Discord ${embed ? 'card' : 'message'}`);
      }
    }
    for (const embed of [false, true]) assert.equal(t.discordTemplateProblem(entry.subject, entry.text, embed), undefined);
  }
  const down = t.sampleValues('down', 'enhanced');
  let m = t.prepareMessage(enhanced('compact'), down, 'chat', 'enhanced');
  assert.deepEqual([m.subject, m.text], ['DOWN: Example API', DOWN_RESULTS], 'the multi-location shape');
  m = t.prepareMessage(enhanced('detailed'), t.sampleValues('recovery', 'enhanced'), 'email', 'enhanced');
  assert.equal(m.subject, '[UP] Example API');
  assert.equal(m.text, ['Monitor: Example API', 'Target: https://api.example.com', 'Opened: 2026-10-24T08:01:05Z', 'Resolved: 2026-10-24T08:08:17Z', 'Downtime: 7m 12s', '', 'Node results:', UP_RESULTS, '',
    'Incident: b81e4f07-2c9a-4d3e-a6f5-0e7d1c2b9a84', 'Sent by: New York'].join('\n'));
  assert.ok(t.prepareMessage(enhanced('detailed'), down, 'email', 'enhanced').text.includes('Resolved: \nDowntime: \n'), 'recovery lines stay empty on a down alert, as the preset says');
  const earlier = fieldsOf(preset('compact'));
  assert.equal(t.presetOf(earlier, 'chat', 'enhanced'), 'custom');
  assert.equal(t.earlierPresetOf(earlier, 'chat', 'enhanced'), 'compact');
  assert.equal(t.earlierPresetOf(earlier, 'chat'), undefined, 'on an older node it is the current preset');
  assert.equal(t.replacesCustom(earlier, 'chat', 'enhanced'), false, 'Canary’s earlier wording is replaced without asking; Undo still restores it');
  assert.equal(t.replacesCustom({ subject: 'Mine', text: '', html: '' }, 'discord', 'enhanced'), true, 'the user’s own wording still needs confirmation');
  assert.equal(t.templateSummary('slack', earlier, 'enhanced'), 'Earlier Compact preset');
  assert.equal(t.templateSummary('slack', enhanced('compact'), 'enhanced'), 'Compact preset');
  assert.equal(t.templateSummary('slack', enhanced('compact')), 'Custom title · text', 'an older node doesn’t know it as a preset');
  assert.equal(t.templateSummary('discord', enhanced('detailed'), 'enhanced', true), 'Detailed preset · card');
  assert.equal(t.templateSummary('discord', NONE, 'enhanced', true), 'Built-in wording · card');
  assert.equal(t.templateSummary('discord', { ...NONE, html: '<p>x</p>' }, 'enhanced'), 'Built-in wording', 'Discord drops HTML on save, so it doesn’t count');
  assert.equal(t.templateSummary('discord', enhanced('compact'), 'legacy', true), 'Fixed Discord message');
  const mine = { subject: 'Mine', text: 'x', html: '<p>x</p>' };
  assert.deepEqual(t.applyPreset(mine, 'compact', 'discord', 'enhanced'), { ...enhanced('compact'), html: '<p>x</p>' }, 'kept for the save warning');
  assert.deepEqual(t.applyPreset(mine, 'detailed', 'email', 'enhanced'), { ...enhanced('detailed'), html: '' });
  assert.deepEqual(t.applyPreset(mine, 'compact', 'chat'), { ...fieldsOf(preset('compact')), html: '<p>x</p>' }, 'an older node gets the six-placeholder preset');
});

await check('Discord message (protocol 20): empty templates keep the fixed text byte for byte; the user’s wording is title, line break, text with escaped values; cards carry the node’s colors and event time; over-limit wording fails, never cut', () => {
  for (const event of EVENTS) {
    const plainMessage = t.discordMessage(NONE, event, 'Pager', false);
    assert.deepEqual([plainMessage.content.sent, plainMessage.content.builtIn, plainMessage.embed], [t.discordFixedText(event, 'Pager'), true, false]);
    const card = t.discordMessage(NONE, event, 'Pager', true);
    assert.equal(card.description.sent, t.discordFixedText(event, 'Pager'), 'a built-in card’s text is the fixed message');
    assert.equal(card.title.sent, `${t.STATUS_WORD[event]} ${event === 'test' ? 'Notification test' : 'Example API'}`);
    assert.deepEqual([card.color, card.timestamp], [t.EMBED_COLOR[event], t.rfc3339(t.SAMPLE.eventAt[event])]);
  }
  assert.deepEqual(EVENTS.map(event => t.EMBED_COLOR[event]), [0xD03B3B, 0xFAB219, 0x0CA30C, 0x2A78D6]);
  assert.deepEqual(EVENTS.map(event => t.discordMessage(NONE, event, 'Pager', true).timestamp), ['2026-10-24T08:01:05Z', '2026-10-24T08:06:05Z', '2026-10-24T08:08:17Z', '2026-10-24T09:00:00Z']);
  assert.deepEqual(t.DISCORD_LIMIT, { content: 2000, title: 256, description: 4096 });
  let m = t.discordMessage(enhanced('compact'), 'down', 'Pager', false);
  assert.equal(m.content.sent, `DOWN: Example API\n${t.escapeDiscord(DOWN_RESULTS)}`);
  assert.equal(m.content.shown, `DOWN: Example API\n${DOWN_RESULTS}`, 'Discord hides the escapes');
  assert.equal(m.content.units, m.content.sent.length);
  assert.equal(t.discordMessage(enhanced('compact'), 'test', 'Pager', false).content.sent, 'TEST: Notification test', 'an empty part is left out with its line break');
  assert.equal(t.discordMessage({ ...NONE, text: 'Only {{status}}' }, 'down', 'Pager', false).content.sent, 'Only DOWN');
  m = t.discordMessage(enhanced('compact'), 'recovery', 'Pager', true);
  assert.deepEqual([m.title.shown, m.description.shown, m.color, m.title.builtIn, m.description.builtIn], ['UP: Example API', UP_RESULTS, 0x0ca30c, false, false]);
  m = t.discordMessage({ ...NONE, text: '{{node_results}}' }, 'down', 'Pager', true);
  assert.deepEqual([m.title.sent, m.title.builtIn, m.description.builtIn], ['DOWN Example API', true, false], 'each empty card template uses its built-in part');
  assert.equal(t.discordMessage({ ...NONE, subject: 'Card {{status}}' }, 'repeat', 'Pager', true).description.sent, t.discordFixedText('repeat', 'Pager'));
  const x = n => 'x'.repeat(n);
  assert.equal(t.discordMessage({ ...NONE, text: x(2000) }, 'down', 'Pager', false).tooLong, undefined);
  assert.equal(t.discordMessage({ ...NONE, text: x(2001) }, 'down', 'Pager', false).tooLong, 'content');
  assert.equal(t.discordMessage({ ...NONE, subject: x(1000), text: x(1000) }, 'down', 'Pager', false).tooLong, 'content', 'the line break counts');
  assert.equal(t.discordMessage({ ...NONE, text: `${'😀'.repeat(1000)}x` }, 'down', 'Pager', false).tooLong, 'content', 'UTF-16 units: this emoji is two');
  const room = t.DISCORD_LIMIT.content - t.escapeDiscord(DOWN_RESULTS).length;
  m = t.discordMessage({ ...NONE, text: `{{node_results}}${x(room + 1)}` }, 'down', 'Pager', false);
  assert.deepEqual([m.tooLong, m.content.units], ['content', 2001], 'the limit counts the escaped text as sent');
  assert.ok(m.content.shown.length < 2000);
  assert.equal(t.discordMessage({ subject: x(256), text: 'x', html: '' }, 'down', 'Pager', true).tooLong, undefined);
  assert.equal(t.discordMessage({ subject: x(257), text: 'x', html: '' }, 'down', 'Pager', true).tooLong, 'title');
  assert.equal(t.discordMessage({ subject: 'x', text: x(4097), html: '' }, 'down', 'Pager', true).tooLong, 'description');
  for (const embed of [false, true]) for (const event of EVENTS) assert.equal(t.discordMessage(NONE, event, 'Ops '.repeat(200), embed).tooLong, undefined, 'Canary’s own wording is cut to fit');
  const blank = { subject: '{{resolved_at}}', text: '{{downtime}}', html: '' };
  assert.deepEqual([t.discordMessage(blank, 'down', 'Pager', false).empty, t.discordMessage(blank, 'down', 'Pager', true).empty], [true, true]);
  m = t.discordMessage(blank, 'recovery', 'Pager', false);
  assert.deepEqual([m.content.sent, m.content.shown], ['2026\\-10\\-24T08:08:17Z\n7m 12s', '2026-10-24T08:08:17Z\n7m 12s'], 'every filled-in value is escaped, times too');
  assert.match(t.discordTemplateProblem(x(257), '', true), /257 of 256/u);
  assert.equal(t.discordTemplateProblem(`${x(250)}{{monitor_target}}{{status}}`, '', true), undefined, 'placeholders aren’t fixed text');
  assert.match(t.discordTemplateProblem('', x(4097), true), /4,097 of 4,096/u);
  assert.match(t.discordTemplateProblem(x(1000), x(1000), false), /2,001 of 2,000/u);
  assert.equal(t.discordTemplateProblem(x(1000), x(999), false), undefined);
  assert.equal(t.discordTemplateProblem('', '{{node_results}}'.repeat(500), false), undefined);
});

const slackChannel = templates => slack(templates);
await check('channel form (protocol 20): the card layout is saved only when chosen and only to a protocol 20 node; Discord drops HTML; newly typed protocol 20 placeholders never go to an older node', () => {
  let stored = discordChannel();
  let draft = cf.channelDraft(stored);
  assert.equal(draft.discordEmbed, false);
  let built = cf.buildChannel(draft, stored, 'enhanced');
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config, { kind: 'discord', webhook: { url: DURL } }, 'an unchanged channel saves as before');
  assert.deepEqual(cf.buildChannel({ ...draft, discordEmbed: true }, stored, 'enhanced').input.config.webhook, { url: DURL, embed: true });
  assert.deepEqual(cf.buildChannel({ ...draft, discordEmbed: true }, stored).input.config.webhook, { url: DURL }, 'never added for an older or unknown node');
  stored = discordChannel({ embed: true });
  draft = cf.channelDraft(stored);
  assert.equal(draft.discordEmbed, true);
  assert.deepEqual(cf.buildChannel(draft, stored, 'enhanced').input.config.webhook, { url: DURL, embed: true });
  assert.deepEqual(cf.buildChannel({ ...draft, discordEmbed: false }, stored, 'enhanced').input.config.webhook, { url: DURL }, 'turned off, the key goes, as omitempty stores it');
  assert.deepEqual(cf.buildChannel(draft, stored).input.config.webhook, { url: DURL, embed: true }, 'an older reading keeps what the node itself stores');
  const own = { subject: 'Mine {{monitor_name}}', text: 'Body {{observations_summary}}' };
  const fromSlack = slackChannel(own);
  built = cf.buildChannel({ ...cf.channelDraft(fromSlack), kind: 'discord', discordUrl: DURL }, fromSlack, 'enhanced');
  assert.ok(built.input, JSON.stringify(built.errors));
  assert.deepEqual(built.input.config, { kind: 'discord', webhook: { url: DURL }, templates: own }, 'a provider change keeps the wording and never turns on the card layout');
  const mail = mailChannel();
  built = cf.buildChannel({ ...cf.channelDraft(mail), kind: 'discord', discordUrl: DURL }, mail, 'enhanced');
  assert.deepEqual(built.input.config.templates, { subject: 'S {{monitor_name}}', text: 'T {{observations_summary}}' }, 'Discord takes a title and plain text, so the HTML is dropped, as the form warns');
  const plainSlack = slackChannel();
  const pd = cf.channelDraft(plainSlack);
  const added = { ...pd, subject: '{{status}}: {{monitor_name}}', text: '{{node_results}}' };
  built = cf.buildChannel(added, plainSlack);
  assert.equal(built.input, undefined);
  assert.match(built.errors.templates, /^\{\{status\}\}, \{\{node_results\}\} are filled in only by nodes on peer protocol 20 or later; this node would send them as typed/u);
  assert.ok(cf.buildChannel(added, plainSlack, 'enhanced').input);
  const literalStored = slackChannel({ subject: 'Literal {{status}}' });
  assert.ok(cf.buildChannel({ ...cf.channelDraft(literalStored), name: 'Renamed' }, literalStored).input, 'a stored channel always saves again unchanged');
  assert.match(cf.buildChannel({ ...cf.channelDraft(literalStored), text: '{{downtime}}' }, literalStored).errors.templates, /^\{\{downtime\}\} is filled in only/u);
  assert.ok(cf.buildChannel({ ...pd, html: '{{status}}' }, plainSlack).input, 'Slack drops HTML, so its tokens are never saved');
  const near = { ...pd, subject: `${'x'.repeat(240)}{{node_results}}` };
  assert.ok(cf.buildChannel(near, plainSlack, 'enhanced').input, 'on protocol 20 an added placeholder is a value, not fixed text');
  assert.match(cf.buildChannel(near, plainSlack).errors.templates, /longer than 250 characters/u, 'an older node sends it as typed, so it counts');
  const dc = cf.channelDraft(discordChannel());
  assert.match(cf.buildChannel({ ...dc, discordEmbed: true, subject: 'y'.repeat(257) }, discordChannel(), 'enhanced').errors.templates, /257 of 256/u);
  assert.match(cf.buildChannel({ ...dc, text: 'z'.repeat(2001) }, discordChannel(), 'enhanced').errors.templates, /2,001 of 2,000/u);
  assert.ok(cf.buildChannel({ ...dc, text: 'z'.repeat(2001) }, discordChannel()).input, 'an older node doesn’t use Discord templates, so Discord’s limit isn’t theirs');
});

const renderEnhanced = props => renderTemplates({ capability: ENHANCED, nodeName: 'node-a', ...props });
await check('rendered editor (protocol 20): the added placeholders, Discord’s layout control, plain and card previews with the escaped text as sent, nothing rendered as HTML', () => {
  let markup = renderEnhanced({ kind: 'slack', fields: enhanced('compact') });
  for (const text of ['{{status}}', '{{node_results}}', '{{sender_node}}', 'Now: Compact', `DOWN: Example API\n${DOWN_RESULTS}`, 'One line per node with its round trip when measured', 'https://api.example.com'])
    assert.ok(markup.includes(text), text);
  assert.equal(markup.includes('need peer protocol'), false, 'no upgrade note');
  assert.equal(markup.includes('Discord layout'), false);
  markup = renderEnhanced({ kind: 'slack', fields: fieldsOf(preset('compact')) });
  assert.ok(markup.includes('Now: earlier Compact preset'));
  assert.equal(count(markup, 'aria-label="Use the '), 3);
  markup = renderEnhanced({ kind: 'discord', providerLabel: 'Discord', fields: NONE });
  assert.deepEqual(radios(markup, 'f1-dc-layout').map(found => [found.get('value'), found.has('checked')]), [['plain', true], ['card', false]], 'off unless chosen');
  for (const text of ['Discord layout', 'Plain message', '<textarea', 'Uptime Canary: DOWN\nMonitor: Example API', 'Discord formats Markdown in your own wording', 'Sample values', '{{monitor_target}}', 'Canary’s fixed message, or as a card'])
    assert.ok(markup.includes(text), text);
  assert.equal(markup.includes('Discord channels send Canary’s fixed message'), false);
  assert.equal(markup.includes('Text as sent'), false, 'nothing of the user’s to show escaped');
  assert.equal(count(markup, 'aria-label="Use the '), 2, 'Built-in is in use');
  markup = renderEnhanced({ kind: 'discord', providerLabel: 'Discord', fields: enhanced('compact'), embed: true });
  assert.deepEqual(radios(markup, 'f1-dc-layout').map(found => [found.get('value'), found.has('checked')]), [['plain', false], ['card', true]]);
  assert.ok(markup.includes('<figure class="tpl-msg tpl-msg-card">'));
  assert.ok(markup.includes(`<div class="tpl-dc-card ev-down"><div class="tpl-dc-title">DOWN: Example API</div><div class="tpl-dc-desc">${DOWN_RESULTS}</div><div class="tpl-dc-time">2026-10-24 08:01:05 UTC</div></div>`));
  assert.ok(markup.includes('Text as sent') && markup.includes(t.escapeDiscord(DOWN_RESULTS)));
  assert.ok(markup.includes(`Title: 17 of 256 UTF-16 units · Text: ${t.escapeDiscord(DOWN_RESULTS).length} of 4,096 UTF-16 units.`));
  markup = renderEnhanced({ kind: 'discord', providerLabel: 'Discord', fields: { subject: '<b>{{monitor_name}}</b>', text: 'Look: <img src=x onerror=alert(1)>', html: '<script>x</script>' }, embed: true });
  assert.equal(markup.includes('<img') || markup.includes('<script') || markup.includes('<b>Example API'), false, 'no tag from the template reaches the page');
  assert.ok(markup.includes('&lt;b&gt;Example API&lt;/b&gt;') && markup.includes('Discord gets plain text, so saving drops the HTML template.'));
  markup = renderEnhanced({ kind: 'discord', providerLabel: 'Discord', fields: { ...NONE, text: 'x'.repeat(2001) } });
  assert.ok(markup.includes('this alert would fail: the message is over Discord’s limit'));
  markup = renderEnhanced({ kind: 'discord', providerLabel: 'Discord', fields: { subject: '{{resolved_at}}', text: '{{downtime}}', html: '' } });
  assert.ok(markup.includes('The title and text are both empty for this event'));
  markup = renderEnhanced({ kind: 'webhook', providerLabel: 'Webhook', fields: NONE });
  assert.ok(markup.includes('Webhook channels receive Canary’s JSON event') && !markup.includes('peer protocol') && !markup.includes('Preview'), 'webhooks are unchanged');
});

await check('rendered editor (older or unknown node): six placeholders, Discord’s fixed message and a factual upgrade note, never a guessed version', () => {
  let markup = renderTemplates({ kind: 'discord', providerLabel: 'Discord', fields: NONE, capability: { level: 'legacy', protocol: 19 }, nodeName: 'node-a' });
  assert.ok(markup.includes('Discord channels send Canary’s fixed message') && markup.includes('need peer protocol 20 or later. node-a reports peer protocol 19.'));
  assert.equal(markup.includes('Discord layout'), false);
  markup = renderTemplates({ kind: 'discord', providerLabel: 'Discord', fields: NONE, embed: true });
  assert.ok(markup.includes('This node doesn’t report its peer protocol.'));
  assert.equal(markup.includes('tpl-dc-card'), false, 'no card for a node that can’t send one');
  markup = renderTemplates({ kind: 'slack', fields: { subject: '{{status}}: {{monitor_name}}', text: '', html: '' }, nodeName: 'node-a' });
  assert.ok(markup.includes('node-a doesn’t report its peer protocol, so the presets use the six above.'));
  assert.ok(markup.includes('without spaces. {{status}} needs peer protocol 20 or later. node-a doesn’t report its peer protocol.'));
  assert.ok(markup.includes('{{status}}: Example API'), 'the preview shows the token as the older node sends it');
  assert.equal(markup.includes('{{node_results}}'), false, 'no added placeholder is offered');
});

const BUILD = { name: 'Uptime-Canary', version: 'unknown', moduleVersion: '(devel)', goVersion: 'go1.25.0', os: 'linux', arch: 'amd64', modified: null, peerProtocol: 20 };
const channelForm = (channel, build) => plain(renderToStaticMarkup(React.createElement(CanaryContext.Provider, { value: ctx([{ channel, configConflict: false }], build) }, React.createElement(ChannelFormView, { id: channel.id }))));
await check('rendered channel form (protocol 20): the selected node’s reported build picks the grammar, and a stored card layout opens the section', () => {
  const stored = discordChannel({ embed: true }, { subject: enhanced('compact').subject, text: enhanced('compact').text });
  let markup = channelForm(stored, BUILD);
  assert.ok(markup.includes('Compact preset · card') && markup.includes('<div class="tpl-dc-card ev-down">') && markup.includes('Sample data'));
  assert.equal(markup.includes('fixture-part<'), false, 'no credential is page text');
  markup = channelForm(stored, { ...BUILD, peerProtocol: 19 });
  assert.ok(markup.includes('Fixed Discord message') && markup.includes('a reports peer protocol 19.'));
  assert.equal(markup.includes('tpl-dc-card'), false);
  markup = channelForm(discordChannel({ embed: true }));
  assert.ok(markup.includes('Fixed Discord message') && markup.includes('a doesn’t report its peer protocol.'), 'no build: the r1 editor');
  markup = channelForm(discordChannel(), BUILD);
  assert.ok(markup.includes('Built-in wording'));
  assert.equal(markup.includes('Sample data'), false, 'nothing stored: the section stays closed');
});

const goFiles = dir => readdirSync(resolve(root, 'apps/node-go/internal', dir)).filter(name => name.endsWith('.go') && !name.endsWith('_test.go')).map(name => goSource(`${dir}/${name}`)).join('\n');
await check('node source for peer protocol 20 (root’s change, pinned so the previews can’t drift): the gate, the reported build, webhook.embed, status words, card colors', () => {
  const protocol = Number(/const peerProtocol = (\d+)/u.exec(goSource('canary/wire_pages.go'))?.[1]);
  assert.ok(protocol >= t.TEMPLATE_PROTOCOL, `peerProtocol ${protocol}`);
  assert.match(goSource('canary/types.go'), /Build\s+\*BinaryIdentity\s+`json:"build,omitempty"`/u);
  assert.match(goSource('canary/build_identity.go'), /PeerProtocol\s+int\s+`json:"peerProtocol"`/u);
  assert.match(goSource('delivery/delivery.go'), /type WebhookConfig struct \{[^}]*\n\tEmbed\s+bool\s+`json:"embed,omitempty"`/u, 'webhook.embed, left out when false');
  const go = `${goFiles('canary')}\n${goFiles('delivery')}`;
  for (const word of ['"STILL DOWN"', '"UP"', '"TEST"', '"embeds"', '"allowed_mentions"']) assert.ok(go.includes(word), word);
  for (const event of EVENTS) {
    const color = t.EMBED_COLOR[event];
    assert.match(go, new RegExp(`\\b(?:0x${color.toString(16).padStart(6, '0')}|${color})\\b`, 'iu'), `${event} card color`);
  }
});

await check('source (protocol 20): the card colors in CSS are the node’s, the layout changes only through its own control, and the save is capability-gated', () => {
  const css = source('styles.css');
  for (const event of EVENTS) assert.ok(css.includes(`.tpl-dc-card.ev-${event} { --embed: #${t.EMBED_COLOR[event].toString(16).padStart(6, '0')}; }`), event);
  const channels = source('views/Channels.tsx');
  for (const line of [
    'if (draft.discordEmbed) config.webhook.embed = true; else delete config.webhook.embed;', 'discordEmbed: c?.kind === \'discord\' && c.webhook?.embed === true,',
    'const capability = templateCapability(overview.build);', 'const { input, errors } = buildChannel(draft, base, capability.level);', 'onEmbed={value => set(\'discordEmbed\', value)}',
  ]) assert.ok(channels.includes(line), line);
  assert.equal(count(channels, 'config.webhook.embed = true'), 1);
  assert.equal(count(source('views/MessageTemplates.tsx'), 'onEmbed?.('), 1, 'only the layout control calls it');
  assert.equal(/embed/u.test(source('alertTemplates.ts').slice(source('alertTemplates.ts').indexOf('export function applyPreset('), source('alertTemplates.ts').indexOf('export const sameFields'))), false, 'presets never touch the layout');
});

await check('source: the preview is local and text-only, and the channel save is unchanged', () => {
  for (const file of ['alertTemplates.ts', 'views/MessageTemplates.tsx']) {
    const text = source(file);
    assert.equal(/dangerouslySetInnerHTML|innerHTML|<iframe|srcDoc|fetch\(|useCanary|localStorage|sessionStorage|indexedDB|console\./u.test(text), false, file);
    for (const code of [0x2028, 0x2029, 0x85, 0xfeff]) assert.equal(text.includes(String.fromCharCode(code)), false, `${file} has no raw U+${code.toString(16)}`);
  }
  const channels = source('views/Channels.tsx');
  for (const line of ['if (draft.subject) templates.subject = draft.subject;', 'if (draft.text) templates.text = draft.text;', 'if (draft.html && !messaging) templates.html = draft.html;', 'if (Object.keys(templates).length) config.templates = templates;'])
    assert.ok(channels.includes(line), line);
  assert.deepEqual(touched, [], 'nothing stored in the browser');
  assert.deepEqual(calls, [], 'nothing fetched');
});

console.log(failed ? `${failed} check group(s) failed` : 'all alert template checks passed');
process.exit(failed ? 1 : 0);
