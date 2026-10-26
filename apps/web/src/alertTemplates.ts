// Alert message templates for reusable channels: the node's placeholder grammar, presets built only from it, fixed sample
// events and the message each provider family would carry. The rules mirror the node's source as read on 2026-10-25:
// delivery/validate.go (renderTemplate, prepareMessage), delivery/messaging.go and notifyhttp (title and text layout and
// limits), canary/notification_targets.go (what an alert fills in) and canary/notification_provider.go (Discord's fixed
// text). Peer protocol 20 adds eight placeholders, Discord wording and Discord cards (embeds), following root's accepted
// contract in .canary/alert-template-backend-v2.md; they're offered only when the selected node reports protocol 20 or
// later, and older nodes keep the six-placeholder presets and Discord's fixed message. Previews are computed here,
// locally: no network, no credentials, and template HTML is only ever shown as text.
// Pure: no JSX.
import { bytesOf } from './format';
import { TEXT_LIMIT, isMessaging, literalText, type MessagingKind } from './messaging';

// ---------- capability

/** Peer protocol 20 fills in the added placeholders and applies templates, and the card layout, to Discord channels. */
export const TEMPLATE_PROTOCOL = 20;
/** 'legacy': the six placeholders every node fills in, and Discord's fixed message. 'enhanced': peer protocol 20 or later. */
export type TemplateLevel = 'legacy' | 'enhanced';
export interface TemplateCapability {
  level: TemplateLevel;
  /** The peer protocol the selected node reports, when it reports one. */
  protocol?: number;
}

/**
 * What the selected node can render, from its overview's `build`. A node that doesn't report a whole protocol number gets
 * the six-placeholder grammar: nothing is inferred from an absent or malformed field.
 */
export function templateCapability(build: { peerProtocol?: unknown } | null | undefined): TemplateCapability {
  const protocol = build?.peerProtocol;
  if (typeof protocol !== 'number' || !Number.isSafeInteger(protocol) || protocol < 1) return { level: 'legacy' };
  return { level: protocol >= TEMPLATE_PROTOCOL ? 'enhanced' : 'legacy', protocol };
}

// ---------- grammar

/** The six substitutions every node makes. Anything else between braces, `{{ monitor_name }}` with spaces included, is sent as typed. */
export const PLACEHOLDER_NAMES = ['event_kind', 'monitor_name', 'monitor_id', 'incident_id', 'observations_summary', 'idempotency_key'] as const;
/** Peer protocol 20's additions, after the six. Older nodes send these tokens as typed. */
export const ADDED_PLACEHOLDER_NAMES = ['status', 'monitor_type', 'monitor_target', 'opened_at', 'resolved_at', 'downtime', 'node_results', 'sender_node'] as const;
export type PlaceholderName = typeof PLACEHOLDER_NAMES[number];
export type AddedPlaceholderName = typeof ADDED_PLACEHOLDER_NAMES[number];
export type AnyPlaceholderName = PlaceholderName | AddedPlaceholderName;
/** One message's values. The added ones are present in peer protocol 20 samples only. */
export type MessageValues = Record<PlaceholderName, string> & Partial<Record<AddedPlaceholderName, string>>;
const ENHANCED_NAMES: readonly AnyPlaceholderName[] = [...PLACEHOLDER_NAMES, ...ADDED_PLACEHOLDER_NAMES];
export const placeholderNames = (level: TemplateLevel): readonly AnyPlaceholderName[] => level === 'enhanced' ? ENHANCED_NAMES : PLACEHOLDER_NAMES;
const KNOWN: Record<TemplateLevel, ReadonlySet<string>> = { legacy: new Set(PLACEHOLDER_NAMES), enhanced: new Set(ENHANCED_NAMES) };

export const PLACEHOLDER_MEANING: Record<AnyPlaceholderName, string> = {
  event_kind: 'down, repeat (still down), recovery or test',
  monitor_name: 'The monitor’s name',
  monitor_id: 'The monitor’s ID (empty in tests)',
  incident_id: 'The incident’s ID (empty in tests)',
  observations_summary: 'One line per node: its ID, state and check message (empty in tests)',
  idempotency_key: 'This attempt’s ID, kept across its retries',
  status: 'DOWN, STILL DOWN, UP or TEST',
  monitor_type: 'The check type, like http or ping (empty in tests)',
  monitor_target: 'Where the check points, never credentials: scheme, host and port for web checks, host and port for ping, TCP, DNS and similar. Empty when there’s no safe form, and in tests.',
  opened_at: 'When the outage began, UTC (empty in tests)',
  resolved_at: 'When it recovered, UTC. Recoveries only.',
  downtime: 'How long it was down, like 7m 12s. Recoveries only.',
  node_results: 'One line per node with its round trip when measured (empty in tests)',
  sender_node: 'The name of the node that sends the alert',
};

/** Go's html.EscapeString: exactly these five characters, as the node escapes values in HTML templates. */
export const escapeHtml = (text: string): string =>
  text.replace(/[&'<>"]/gu, c => c === '&' ? '&amp;' : c === '\'' ? '&#39;' : c === '<' ? '&lt;' : c === '>' ? '&gt;' : '&#34;');

/** Peer protocol 20 Discord values: a backslash before each of \ * _ ~ ` | > # - [ ] ( ), so Discord shows them as typed. */
export const escapeDiscord = (text: string): string => text.replace(/[\\*_~`|>#\-\[\]()]/gu, c => `\\${c}`);

/** How filled-in values are escaped: false for plain text, true for HTML (Go's html.EscapeString), 'discord' for Discord Markdown. */
export type ValueEscape = boolean | 'discord';

/**
 * The node's scan: from each "{{" to the next "}}" is one token. A known name is replaced by its value, once (a value is
 * never read as template source); any other token, and an unclosed "{{", stays exactly as typed.
 */
function scan(source: string, token: (name: string, raw: string) => string): string {
  let out = '';
  let rest = source;
  while (rest) {
    const start = rest.indexOf('{{');
    if (start < 0) { out += rest; break; }
    out += rest.slice(0, start);
    rest = rest.slice(start);
    const end = rest.indexOf('}}');
    if (end < 0) { out += rest; break; }
    out += token(rest.slice(2, end), rest.slice(0, end + 2));
    rest = rest.slice(end + 2);
  }
  return out;
}

/** delivery/validate.go renderTemplate, without its size limit (see prepareMessage). An older node knows only the six names. */
export function renderTemplate(source: string, values: MessageValues, escape: ValueEscape, level: TemplateLevel = 'legacy'): string {
  const known = KNOWN[level];
  return scan(source, (name, raw) => {
    if (!known.has(name)) return raw;
    const value = values[name as AnyPlaceholderName] ?? '';
    return escape === 'discord' ? escapeDiscord(value) : escape ? escapeHtml(value) : value;
  });
}

/** The tokens a template holds, split into real placeholders and look-alikes the node sends as typed. */
export function templateTokens(source: string, level: TemplateLevel = 'legacy'): { known: AnyPlaceholderName[]; literal: string[] } {
  const names = KNOWN[level];
  const known: AnyPlaceholderName[] = [];
  const literal: string[] = [];
  scan(source, (name, raw) => {
    if (names.has(name)) { if (!known.includes(name as AnyPlaceholderName)) known.push(name as AnyPlaceholderName); } else if (!literal.includes(raw)) literal.push(raw);
    return raw;
  });
  return { known, literal };
}

const ADDED_TOKEN = new RegExp(`\\{\\{(?:${ADDED_PLACEHOLDER_NAMES.join('|')})\\}\\}`, 'gu');
/** Peer protocol 20 templates without the added placeholders, so the six-name literal bound in messaging.ts applies to them. */
export const withoutAddedPlaceholders = (source: string): string => source.replace(ADDED_TOKEN, '');

/**
 * Added placeholders a draft would newly store on a node older than peer protocol 20, which would send them as typed. Tokens
 * the stored channel already holds don't count, so a stored channel always saves again unchanged.
 */
export function unsupportedPlaceholders(fields: TemplateFields, stored: { subject?: string; text?: string; html?: string } | undefined, level: TemplateLevel): AddedPlaceholderName[] {
  if (level === 'enhanced') return [];
  const used = (sources: string[]): Set<string> => new Set(sources.flatMap(source => templateTokens(source, 'enhanced').known));
  const before = used([stored?.subject ?? '', stored?.text ?? '', stored?.html ?? '']);
  const now = used([fields.subject, fields.text, fields.html]);
  return ADDED_PLACEHOLDER_NAMES.filter(name => now.has(name) && !before.has(name));
}

// ---------- provider families

/**
 * Email takes a subject, text and HTML; chat and push take a title and plain text; Discord on peer protocol 20 takes a title
 * and text, as a message or a card. Discord on older nodes, and webhooks, ignore templates.
 */
export type TemplateFamily = 'chat' | 'email' | 'discord' | 'fixed';
const EMAIL: ReadonlySet<string> = new Set(['smtp', 'mailgun', 'brevo', 'mandrill']);
export const templateFamily = (kind: string, level: TemplateLevel = 'legacy'): TemplateFamily =>
  isMessaging(kind) ? 'chat' : EMAIL.has(kind) ? 'email' : kind === 'discord' && level === 'enhanced' ? 'discord' : 'fixed';

/** Slack and Telegram get one plain-text message: the title, a line break, then the text. ntfy, Gotify and Pushover keep them apart. */
export const combinesTitle = (kind: MessagingKind): boolean => kind === 'slack' || kind === 'telegram';

export interface TemplateFields { subject: string; text: string; html: string }

// ---------- presets (only placeholders the selected node fills in)

export type PresetId = 'builtin' | 'compact' | 'detailed';
export interface Preset {
  id: PresetId; label: string; summary: string; subject: string; text: string;
  /** Discord's own description where it differs. */
  discord?: string;
}

const DETAILED_TEXT = [
  'Monitor: {{monitor_name}}',
  'Event: {{event_kind}}',
  '',
  'Node results:',
  '{{observations_summary}}',
  '',
  'Incident: {{incident_id}}',
  'Monitor ID: {{monitor_id}}',
].join('\n');

/** The six-placeholder presets, for nodes older than peer protocol 20 (and nodes that don't report a protocol). */
export const PRESETS: readonly Preset[] = [
  { id: 'builtin', label: 'Built-in', summary: 'Empty templates: the node writes the event and monitor name, then each node’s result.', subject: '', text: '' },
  { id: 'compact', label: 'Compact', summary: 'Name and event, then one line per node. Suits chat and push.', subject: '{{monitor_name}}: {{event_kind}}', text: '{{observations_summary}}' },
  { id: 'detailed', label: 'Detailed', summary: 'Labelled lines: monitor, event, every node’s result and the incident reference.', subject: '[{{event_kind}}] {{monitor_name}}', text: DETAILED_TEXT },
];

const ENHANCED_DETAILED_TEXT = [
  'Monitor: {{monitor_name}}',
  'Target: {{monitor_target}}',
  'Opened: {{opened_at}}',
  'Resolved: {{resolved_at}}',
  'Downtime: {{downtime}}',
  '',
  'Node results:',
  '{{node_results}}',
  '',
  'Incident: {{incident_id}}',
  'Sent by: {{sender_node}}',
].join('\n');

/** Peer protocol 20 presets: the same three, with the status word, target, times, downtime and latency per node. */
export const ENHANCED_PRESETS: readonly Preset[] = [
  {
    id: 'builtin', label: 'Built-in', summary: 'Empty templates: the node writes the event and monitor name, then each node’s result.', subject: '', text: '',
    discord: 'Empty templates: Canary’s fixed message, or as a card, the status and monitor name over the same fixed lines.',
  },
  {
    id: 'compact', label: 'Compact', summary: 'Status and name, then one line per location with its latency. A multi-location summary for chat, push and cards.',
    subject: '{{status}}: {{monitor_name}}', text: '{{node_results}}',
  },
  {
    id: 'detailed', label: 'Detailed', summary: 'Outage and recovery details: target, opened and resolved times, downtime, every location with its latency, incident and sender. Recovery lines stay empty on down alerts.',
    subject: '[{{status}}] {{monitor_name}}', text: ENHANCED_DETAILED_TEXT,
  },
];
export const presetsFor = (level: TemplateLevel): readonly Preset[] => level === 'enhanced' ? ENHANCED_PRESETS : PRESETS;
export const presetLabel = (id: PresetId): string => PRESETS.find(preset => preset.id === id)?.label ?? id;

const matching = (presets: readonly Preset[], fields: TemplateFields, family: TemplateFamily): PresetId | undefined => {
  const html = family === 'email' ? fields.html : '';
  return presets.find(preset => preset.subject === fields.subject && preset.text === fields.text && !html)?.id;
};

/**
 * Which of the node's presets the fields hold exactly, or 'custom'. Chat, push and Discord channels never keep an HTML
 * template (saving drops it, with a warning), so it doesn't count there; for email a preset means no HTML.
 */
export function presetOf(fields: TemplateFields, family: TemplateFamily, level: TemplateLevel = 'legacy'): PresetId | 'custom' {
  return matching(presetsFor(level), fields, family) ?? 'custom';
}

/** On a peer protocol 20 node: a six-placeholder preset the channel still holds. It's Canary's wording, not the user's. */
export function earlierPresetOf(fields: TemplateFields, family: TemplateFamily, level: TemplateLevel = 'legacy'): PresetId | undefined {
  if (level !== 'enhanced' || presetOf(fields, family, level) !== 'custom') return undefined;
  return matching(PRESETS, fields, family);
}

/** Only an explicit choice applies a preset, and replacing the user's own text needs a second, explicit confirmation. */
export const replacesCustom = (fields: TemplateFields, family: TemplateFamily, level: TemplateLevel = 'legacy'): boolean =>
  presetOf(fields, family, level) === 'custom' && !earlierPresetOf(fields, family, level);

/**
 * The fields after applying a preset. Email presets are plain text, so they clear the HTML template. Chat, push and Discord
 * keep whatever HTML the draft holds for the existing "saving drops it" warning. Fixed channels don't use templates.
 */
export function applyPreset(fields: TemplateFields, id: PresetId, family: TemplateFamily, level: TemplateLevel = 'legacy'): TemplateFields {
  const preset = presetsFor(level).find(entry => entry.id === id);
  if (!preset || family === 'fixed') return fields;
  return { subject: preset.subject, text: preset.text, html: family === 'email' ? '' : fields.html };
}

export const sameFields = (a: TemplateFields, b: TemplateFields): boolean => a.subject === b.subject && a.text === b.text && a.html === b.html;

/** The closed disclosure's summary. `embed` is Discord's card layout (peer protocol 20). */
export function templateSummary(kind: string, fields: TemplateFields, level: TemplateLevel = 'legacy', embed = false): string {
  const family = templateFamily(kind, level);
  if (family === 'fixed') return kind === 'discord' ? 'Fixed Discord message' : 'Fixed JSON event';
  const card = family === 'discord' && embed ? ' · card' : '';
  const preset = presetOf(fields, family, level);
  if (preset === 'builtin') return `Built-in wording${card}`;
  if (preset !== 'custom') return `${presetLabel(preset)} preset${card}`;
  const earlier = earlierPresetOf(fields, family, level);
  if (earlier) return `Earlier ${presetLabel(earlier)} preset${card}`;
  const parts = [fields.subject ? (family === 'email' ? 'subject' : 'title') : '', fields.text ? 'text' : '', family === 'email' && fields.html ? 'HTML' : ''].filter(Boolean);
  return parts.length ? `Custom ${parts.join(' · ')}${card}` : `Built-in wording${card}`;
}

// ---------- sample events (made up, clearly labelled in the editor)

export type SampleEvent = 'down' | 'repeat' | 'recovery' | 'test';
/** Each value is an attempt kind the node sends as {{event_kind}} (canary/notification_provider.go). */
export const SAMPLE_EVENTS: readonly { value: SampleEvent; label: string }[] = [
  { value: 'down', label: 'Down' }, { value: 'repeat', label: 'Still down' }, { value: 'recovery', label: 'Recovery' }, { value: 'test', label: 'Test' },
];

export interface SampleObservation {
  nodeId: string; state: 'up' | 'down'; message: string;
  /** The measured round trip; absent when there's none, as after a timeout. */
  latencyMs?: number;
}
const OPENED_AT = Date.UTC(2026, 9, 24, 8, 1, 5);
const RESOLVED_AT = Date.UTC(2026, 9, 24, 8, 8, 17);
export const SAMPLE = {
  monitorName: 'Example API',
  monitorId: '3f6c2a1e-8b4d-4c7a-9e21-5d0b7f3a9c41',
  incidentId: 'b81e4f07-2c9a-4d3e-a6f5-0e7d1c2b9a84',
  /** Peer protocol 20 values. A web check's target is its origin only: no user, path, query or fragment. */
  monitorType: 'http',
  monitorTarget: 'https://api.example.com',
  /** The node that sends; Discord's fixed text names it. */
  sender: { id: 'new-york', name: 'New York' },
  openedAt: OPENED_AT,
  resolvedAt: RESOLVED_AT,
  /** When each sample event happens: a Discord card shows it. The repeat comes five minutes in, before the recovery. */
  eventAt: { down: OPENED_AT, repeat: OPENED_AT + 5 * 60_000, recovery: RESOLVED_AT, test: Date.UTC(2026, 9, 24, 9, 0, 0) } satisfies Record<SampleEvent, number>,
  down: [
    { nodeId: 'new-york', state: 'down', message: 'HTTP status 503 is outside accepted ranges', latencyMs: 182.4 },
    { nodeId: 'london', state: 'down', message: 'HTTP status 503 is outside accepted ranges', latencyMs: 240.6 },
    { nodeId: 'home-lab', state: 'down', message: 'Check cancelled or timed out' },
  ] as SampleObservation[],
  up: [
    { nodeId: 'new-york', state: 'up', message: 'HTTP 200 checks passed', latencyMs: 95.5 },
    { nodeId: 'london', state: 'up', message: 'HTTP 200 checks passed', latencyMs: 88.2 },
    { nodeId: 'home-lab', state: 'up', message: 'HTTP 200 checks passed', latencyMs: 12 },
  ] as SampleObservation[],
  /** Alert attempt IDs are 64-hex digests; a test's is a random UUID. */
  keys: {
    down: 'a3f1c9e27b5d40868e2f1d7c9b0a6e4f3c8d2b7a1e9f05c64d3b8a2e7f1c0d96',
    repeat: '5c0e8a1f3d7b2964e1a0f8c37d2b5e904f6a1c8e2b9d7031c5e8f2a46b1d9e07',
    recovery: '0e9b4d2a7c1f6e38b5a20d9c4e7f13a68d2c5b0f9a1e6c473f8b0d2e5c7a4e91',
    test: 'd2c7e9a4-1b3f-4e6a-8c0d-7f5b2a9e1c36',
  } satisfies Record<SampleEvent, string>,
};

export const sampleObservations = (event: SampleEvent): SampleObservation[] => event === 'test' ? [] : event === 'recovery' ? SAMPLE.up : SAMPLE.down;

/** canary/notification_targets.go: "<node ID>: <state> - <message>", one line per observation. */
export const observationsSummary = (observations: SampleObservation[]): string => observations.map(o => `${o.nodeId}: ${o.state} - ${o.message}`).join('\n');

/** Peer protocol 20 {{node_results}}: the summary's lines, each followed by " (<n> ms)", rounded, when a round trip was measured. */
export const nodeResults = (observations: SampleObservation[]): string =>
  observations.map(o => `${o.nodeId}: ${o.state} - ${o.message}${o.latencyMs === undefined ? '' : ` (${Math.round(o.latencyMs)} ms)`}`).join('\n');

/** Peer protocol 20 {{status}}. */
export const STATUS_WORD: Record<SampleEvent, string> = { down: 'DOWN', repeat: 'STILL DOWN', recovery: 'UP', test: 'TEST' };

/** RFC 3339 in UTC with whole seconds, as Go's time.RFC3339 formats a UTC time (fractions are cut, not rounded). */
export const rfc3339 = (ms: number): string => new Date(ms).toISOString().replace(/\.\d{3}Z$/u, 'Z');

/** Peer protocol 20 {{downtime}}: whole seconds (floored), then the two largest non-zero units of d, h, m and s; "0s" for none. */
export function formatDowntime(ms: number): string {
  let seconds = Number.isFinite(ms) ? Math.max(0, Math.floor(ms / 1000)) : 0;
  const parts: string[] = [];
  for (const [unit, size] of [['d', 86_400], ['h', 3600], ['m', 60], ['s', 1]] as const) {
    const amount = Math.floor(seconds / size);
    seconds -= amount * size;
    if (amount && parts.length < 2) parts.push(`${amount}${unit}`);
  }
  return parts.join(' ') || '0s';
}

/**
 * What the node fills in. A test (saved or unsaved settings) is "Notification test" with no monitor, incident or
 * observations; on peer protocol 20 it also carries the status word and the sending node's name, and nothing else.
 */
export function sampleValues(event: SampleEvent, level: TemplateLevel = 'legacy'): MessageValues {
  const values: MessageValues = event === 'test'
    ? { event_kind: 'test', monitor_name: 'Notification test', monitor_id: '', incident_id: '', observations_summary: '', idempotency_key: SAMPLE.keys.test }
    : {
      event_kind: event, monitor_name: SAMPLE.monitorName, monitor_id: SAMPLE.monitorId, incident_id: SAMPLE.incidentId,
      observations_summary: observationsSummary(sampleObservations(event)), idempotency_key: SAMPLE.keys[event],
    };
  if (level === 'legacy') return values;
  const alert = event !== 'test';
  return {
    ...values,
    status: STATUS_WORD[event],
    monitor_type: alert ? SAMPLE.monitorType : '',
    monitor_target: alert ? SAMPLE.monitorTarget : '',
    opened_at: alert ? rfc3339(SAMPLE.openedAt) : '',
    resolved_at: event === 'recovery' ? rfc3339(SAMPLE.resolvedAt) : '',
    downtime: event === 'recovery' ? formatDowntime(SAMPLE.resolvedAt - SAMPLE.openedAt) : '',
    node_results: nodeResults(sampleObservations(event)),
    sender_node: SAMPLE.sender.name,
  };
}

// ---------- the message the node builds

export interface PreparedMessage {
  subject: string; text: string; html: string;
  /** Built-in wording, which chat and push shorten to fit; custom text is sent exactly or the attempt fails. */
  builtInSubject: boolean; builtInText: boolean;
  /** A rendered part over the node's limit (512-byte subject, 64 KiB text or HTML): the attempt fails. */
  tooLong?: 'subject' | 'text' | 'html';
}

/** delivery/validate.go prepareMessage for an alert the node builds: rendered templates, then the built-in fallbacks. */
export function prepareMessage(fields: TemplateFields, values: MessageValues, family: TemplateFamily, level: TemplateLevel = 'legacy'): PreparedMessage {
  let subject = renderTemplate(fields.subject, values, false, level);
  let text = renderTemplate(fields.text, values, false, level);
  const html = family === 'email' ? renderTemplate(fields.html, values, true, level) : '';
  const tooLong = bytesOf(subject) > 512 ? 'subject' : bytesOf(text) > 65_536 ? 'text' : bytesOf(html) > 65_536 ? 'html' : undefined;
  if (!subject) subject = `${values.event_kind} ${values.monitor_name}`.trim() || 'Uptime Canary notification';
  if (!text && !html) text = values.observations_summary || subject;
  return { subject, text, html, builtInSubject: !fields.subject, builtInText: !fields.text, ...(tooLong ? { tooLong } : {}) };
}

const codePoints = (text: string): number => [...text].length;
const lineBreak = (text: string): boolean => /[\x00-\x1f\x7f-\x9f]/u.test(text) || text.includes(String.fromCharCode(0x2028)) || text.includes(String.fromCharCode(0x2029));

export interface ChatFit {
  titleChars: number; titleFits: boolean;
  used: number; limit: number; unit: string; combined: boolean; textFits: boolean;
}

/** notifyhttp's limits for one message. Built-in wording is shortened to fit, so it always fits. */
export function chatFit(kind: MessagingKind, message: PreparedMessage): ChatFit {
  const rule = TEXT_LIMIT[kind];
  const titleChars = codePoints(message.subject);
  const titleFits = message.builtInSubject || (titleChars <= 250 && bytesOf(message.subject) <= 1000 && !lineBreak(message.subject));
  const used = rule.cost(rule.combined ? `${message.subject}\n${message.text}` : message.text);
  const textFits = message.builtInText || (used <= rule.limit && bytesOf(message.text) <= 16_384);
  return { titleChars, titleFits, used, limit: rule.limit, unit: rule.unit, combined: rule.combined, textFits };
}

// ---------- Discord's fixed message (every node; the only Discord wording before peer protocol 20)

/** canary/notification_provider.go discordBoundText: at most `limit` UTF-16 units, cut on a whole character, then "…". */
function discordBound(value: string, limit: number): string {
  let units = 0, cut = 0, offset = 0;
  for (const character of value) {
    if (units <= limit - 1) cut = offset;
    if (units + character.length > limit) return `${value.slice(0, cut)}…`;
    units += character.length;
    offset += character.length;
  }
  return value;
}

/** The fixed text's field rule: controls become spaces, runs of white space collapse, then the field's bound applies. */
const field = (value: string, limit: number): string => discordBound(value.replace(/[\x00-\x1f\x7f-\x9f]/gu, ' ').split(/\s+/u).filter(Boolean).join(' '), limit);

/** formatDiscordNotification's content for a sample event. A test names the channel instead of a monitor. */
export function discordFixedText(event: SampleEvent, channelName: string): string {
  const state = event === 'recovery' ? 'RECOVERED' : event === 'repeat' ? 'STILL DOWN (repeat 1)' : event === 'test' ? 'TEST NOTIFICATION' : 'DOWN';
  const sender = field(`${SAMPLE.sender.name} (${SAMPLE.sender.id})`, 120);
  if (event === 'test') return discordBound(`Uptime Canary: ${state}\nChannel: ${field(channelName, 240)}\nSender: ${sender}`, 2000);
  let content = `Uptime Canary: ${state}\nMonitor: ${field(SAMPLE.monitorName, 240)}\nIncident: ${field(SAMPLE.incidentId, 100)}\nOpened: ${rfc3339(SAMPLE.openedAt)}\nSender: ${sender}`;
  if (event === 'recovery') content += `\nRecovered: ${rfc3339(SAMPLE.resolvedAt)}`;
  const observations = sampleObservations(event);
  for (const o of observations.slice(0, 5)) content += `\n${field(o.nodeId, 60)}: ${field(o.state, 16)} — ${field(o.message, 220)}`;
  if (observations.length > 5) content += `\n(${observations.length - 5} more observations)`;
  return discordBound(content, 2000);
}

// ---------- Discord wording and cards (peer protocol 20)

/** Card edge colors as the node sends them (0xRRGGBB), one per event. */
export const EMBED_COLOR: Record<SampleEvent, number> = { down: 0xd03b3b, repeat: 0xfab219, recovery: 0x0ca30c, test: 0x2a78d6 };
/** Discord's limits in UTF-16 units: message content, card title and card text. */
export const DISCORD_LIMIT = { content: 2000, title: 256, description: 4096 } as const;

export interface DiscordPart {
  /** What the node sends: filled-in values carry Markdown escapes. */
  sent: string;
  /** What Discord shows for it: escapes hidden, the user's own Markdown left as typed (the preview doesn't format it). */
  shown: string;
  /** UTF-16 units of `sent`, which Discord's limits count. */
  units: number;
  limit: number;
  /** Canary's own wording, cut to fit; the user's wording is never cut. */
  builtIn: boolean;
}
export interface DiscordMessage {
  embed: boolean;
  /** The plain layout's content. */
  content?: DiscordPart;
  /** The card layout. */
  title?: DiscordPart; description?: DiscordPart; color?: number; timestamp?: string;
  /** The user's wording over Discord's limit after substitution: the attempt fails with invalid_message. */
  tooLong?: 'content' | 'title' | 'description';
  /** Every part rendered empty for this event. */
  empty: boolean;
}

const discordPart = (sent: string, shown: string, limit: number, builtIn: boolean): DiscordPart => ({ sent, shown, units: sent.length, limit, builtIn });

/**
 * The Discord message for a sample event, by root's accepted protocol 20 contract. Plain: empty templates keep the fixed
 * message byte for byte; otherwise the rendered title, a line break and the rendered text, leaving out an empty part.
 * Card: an empty title template uses "<STATUS> <monitor name>" and an empty text template the fixed message, each cut to
 * fit; the user's parts are sent exactly. Values are Markdown-escaped, template text is sent as typed, and mentions never
 * ping (allowed_mentions parse is empty in both layouts).
 */
export function discordMessage(fields: TemplateFields, event: SampleEvent, channelName: string, embed: boolean): DiscordMessage {
  const values = sampleValues(event, 'enhanced');
  const sent = (source: string): string => renderTemplate(source, values, 'discord', 'enhanced');
  const shown = (source: string): string => renderTemplate(source, values, false, 'enhanced');
  const fixed = discordFixedText(event, channelName);
  if (!embed) {
    if (!fields.subject && !fields.text) return { embed, content: discordPart(fixed, fixed, DISCORD_LIMIT.content, true), empty: false };
    const join = (title: string, text: string): string => [title, text].filter(Boolean).join('\n');
    const content = discordPart(join(sent(fields.subject), sent(fields.text)), join(shown(fields.subject), shown(fields.text)), DISCORD_LIMIT.content, false);
    return { embed, content, empty: !content.sent, ...(content.units > content.limit ? { tooLong: 'content' as const } : {}) };
  }
  const builtInTitle = field(`${values.status} ${values.monitor_name}`, DISCORD_LIMIT.title);
  const title = fields.subject ? discordPart(sent(fields.subject), shown(fields.subject), DISCORD_LIMIT.title, false) : discordPart(builtInTitle, builtInTitle, DISCORD_LIMIT.title, true);
  const description = fields.text ? discordPart(sent(fields.text), shown(fields.text), DISCORD_LIMIT.description, false) : discordPart(fixed, fixed, DISCORD_LIMIT.description, true);
  const tooLong = title.units > title.limit ? 'title' as const : description.units > description.limit ? 'description' as const : undefined;
  return { embed, title, description, color: EMBED_COLOR[event], timestamp: rfc3339(SAMPLE.eventAt[event]), empty: !title.sent && !description.sent, ...(tooLong ? { tooLong } : {}) };
}

const units = (count: number): string => count.toLocaleString('en-US');
/** Fixed Discord text that can never fit, even with every placeholder empty. The user's wording is never cut. */
export function discordTemplateProblem(subject: string, text: string, embed: boolean): string | undefined {
  const title = literalText(withoutAddedPlaceholders(subject)).length;
  const body = literalText(withoutAddedPlaceholders(text)).length;
  if (embed) {
    if (title > DISCORD_LIMIT.title) return `The card title’s fixed text alone uses ${units(title)} of ${units(DISCORD_LIMIT.title)} UTF-16 units.`;
    if (body > DISCORD_LIMIT.description) return `The card text’s fixed text alone uses ${units(body)} of ${units(DISCORD_LIMIT.description)} UTF-16 units.`;
    return undefined;
  }
  const used = title + body + (title && body ? 1 : 0);
  return used > DISCORD_LIMIT.content ? `The fixed text alone uses ${units(used)} of ${units(DISCORD_LIMIT.content)} UTF-16 units. Title and text together: at most 2,000 UTF-16 units after substitution.` : undefined;
}
