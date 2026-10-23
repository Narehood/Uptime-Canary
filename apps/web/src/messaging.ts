// Native chat and push channels (protocol 13): Slack, Telegram, ntfy, Gotify and Pushover. Draft fields, config building
// that keeps stored values and unknown keys, masked route summaries and destination-identity hints. The node's notifyhttp
// leaf owns the exact rules; these checks only catch clear mistakes before saving. Pure: no network, no JSX.
import type { NotificationConfig } from '@uptime-canary/shared';
import { bytesOf, maskedUrl, span } from './format';

export type MessagingKind = 'slack' | 'telegram' | 'ntfy' | 'gotify' | 'pushover';
const KINDS: readonly string[] = ['slack', 'telegram', 'ntfy', 'gotify', 'pushover'];
export const isMessaging = (kind: string): kind is MessagingKind => KINDS.includes(kind);

/** Official endpoints used when no base URL is configured. Gotify is self-hosted and has none. */
export const DEFAULT_BASE = { telegram: 'https://api.telegram.org', ntfy: 'https://ntfy.sh', pushover: 'https://api.pushover.net' } as const;

export type NtfyAuth = 'none' | 'token' | 'basic';

export interface MessagingDraft {
  slackUrl: string; slackInsecure: boolean;
  tgToken: string; tgChat: string; tgThread: string; tgSilent: boolean; tgProtect: boolean; tgNoPreview: boolean; tgBase: string; tgInsecure: boolean;
  ntfyTopic: string; ntfyAuth: NtfyAuth; ntfyToken: string; ntfyUser: string; ntfyPass: string; ntfyPriority: string; ntfyTags: string; ntfyBase: string; ntfyInsecure: boolean;
  gotifyToken: string; gotifyPriority: string; gotifyBase: string; gotifyInsecure: boolean;
  poToken: string; poUser: string; poDevice: string; poPriority: string; poSound: string; poRetry: string; poExpire: string; poBase: string; poInsecure: boolean;
}

const tagText = (tags: string[] | undefined): string => (tags ?? []).join(', ');
const num = (value: number | undefined): string => value ? String(value) : '';

export function messagingDraft(c: NotificationConfig | undefined): MessagingDraft {
  const s = c?.slack, t = c?.telegram, n = c?.ntfy, g = c?.gotify, p = c?.pushover;
  return {
    slackUrl: s?.webhookURL ?? '', slackInsecure: Boolean(s?.allowInsecureHTTP),
    tgToken: t?.botToken ?? '', tgChat: t?.chatId ?? '', tgThread: num(t?.messageThreadId), tgSilent: Boolean(t?.silent), tgProtect: Boolean(t?.protectContent),
    tgNoPreview: Boolean(t?.disableLinkPreview), tgBase: t?.baseURL ?? '', tgInsecure: Boolean(t?.allowInsecureHTTP),
    ntfyTopic: n?.topic ?? '', ntfyAuth: n?.token ? 'token' : n?.username || n?.password ? 'basic' : 'none', ntfyToken: n?.token ?? '', ntfyUser: n?.username ?? '', ntfyPass: n?.password ?? '',
    ntfyPriority: num(n?.priority), ntfyTags: tagText(n?.tags), ntfyBase: n?.baseURL ?? '', ntfyInsecure: Boolean(n?.allowInsecureHTTP),
    // New Gotify channels start at 8, as in Uptime Kuma; a stored channel without a priority uses 0.
    gotifyToken: g?.appToken ?? '', gotifyPriority: g ? String(g.priority ?? 0) : '8', gotifyBase: g?.baseURL ?? '', gotifyInsecure: Boolean(g?.allowInsecureHTTP),
    poToken: p?.appToken ?? '', poUser: p?.userKey ?? '', poDevice: p?.device ?? '', poPriority: String(p?.priority ?? 0), poSound: p?.sound ?? '',
    poRetry: num(p?.retrySeconds), poExpire: num(p?.expireSeconds), poBase: p?.baseURL ?? '', poInsecure: Boolean(p?.allowInsecureHTTP),
  };
}

export const control = (text: string): boolean => /[\x00-\x1f\x7f]/u.test(text);

export function urlProblem(raw: string, insecure: boolean, label: string, required: boolean): string | undefined {
  const text = raw.trim();
  if (!text) return required ? `Enter the ${label}.` : undefined;
  if (bytesOf(text) > 2048) return 'Use an address of at most 2,048 bytes.';
  let url: URL;
  try { url = new URL(text); } catch { return `Enter a complete https:// address.`; }
  if (url.protocol !== 'https:' && !(insecure && url.protocol === 'http:')) return url.protocol === 'http:' ? 'Use https://, or allow plain HTTP below for a private relay.' : 'Use an https:// address.';
  if (url.username || url.password) return 'Remove credentials from the address.';
  if (url.search || text.includes('#')) return 'Remove the query and #fragment.';
  return undefined;
}

export type MField = 'slackUrl' | 'botToken' | 'chatId' | 'threadId' | 'topic' | 'ntfyAuth' | 'tags' | 'serverUrl' | 'appToken' | 'userKey' | 'device' | 'sound' | 'emergency' | 'baseUrl';
export const MFIELD_LABEL: Record<MField, string> = {
  slackUrl: 'Slack webhook URL', botToken: 'Bot token', chatId: 'Chat', threadId: 'Forum topic', topic: 'Topic', ntfyAuth: 'Login', tags: 'Tags', serverUrl: 'Server URL',
  appToken: 'Application token', userKey: 'User or group key', device: 'Device', sound: 'Sound', emergency: 'Emergency repeats', baseUrl: 'API base URL',
};

const VISIBLE = /^[\x21-\x7e]+$/u;
const BOT = /^\d{1,20}:[A-Za-z0-9_-]{1,235}$/u;
const ALIAS = /^@[A-Za-z][A-Za-z0-9_]{0,31}$/u;
const CHAT = /^-?[1-9]\d{0,15}$/u;
const MAX_ID = 2 ** 52 - 1;
const TOPIC = /^[A-Za-z0-9_-]{1,64}$/u;
const KEY30 = /^[A-Za-z0-9]{30}$/u;
const DEVICE = /^[A-Za-z0-9_-]{1,25}$/u;
const SOUND = /^[A-Za-z0-9_-]{1,64}$/u;
const int = (text: string): number | undefined => /^-?\d{1,16}$/u.test(text.trim()) ? Number(text.trim()) : undefined;
const lineBreak = (text: string): boolean => /[\x00-\x1f\x7f\u2028\u2029]/u.test(text);

/** Sets a value, or removes the key for the zero values Go omits, so unchanged channels round-trip exactly. */
function put(target: Record<string, unknown>, key: string, value: unknown): void {
  if (value === undefined || value === '' || value === false || value === 0) delete target[key];
  else target[key] = value;
}

/** Writes only the active provider object, copied from the stored one so keys this form doesn't model survive. */
export function buildMessaging(kind: MessagingKind, d: MessagingDraft, same: NotificationConfig | undefined, config: NotificationConfig, errors: Partial<Record<MField, string>>): void {
  const token = (value: string, field: MField, what: string): string => {
    if (!value) errors[field] = `Enter the ${what}.`;
    else if (value.length > 512 || !VISIBLE.test(value)) errors[field] = `Paste the ${what} without spaces or line breaks (at most 512 characters).`;
    return value;
  };
  const base = (raw: string, insecure: boolean, label: string, field: MField = 'baseUrl', required = false): string => {
    const problem = urlProblem(raw, insecure, label, required);
    if (problem) errors[field] = problem;
    return raw.trim();
  };
  switch (kind) {
    case 'slack': {
      const url = base(d.slackUrl, d.slackInsecure, 'Slack webhook URL', 'slackUrl', true);
      if (!errors.slackUrl && new URL(url).pathname.endsWith('/')) errors.slackUrl = 'Paste the complete webhook URL from Slack, without a trailing slash: https://hooks.slack.com/services/…';
      const o: Record<string, unknown> = { ...(same?.slack ?? {}), webhookURL: url };
      put(o, 'allowInsecureHTTP', d.slackInsecure);
      config.slack = o as NonNullable<NotificationConfig['slack']>;
      break;
    }
    case 'telegram': {
      if (!d.tgToken) errors.botToken = 'Enter the bot token.';
      else if (!BOT.test(d.tgToken)) errors.botToken = 'Paste the token from @BotFather as is, like 123456789:AAE…';
      const chat = d.tgChat.trim();
      if (!chat) errors.chatId = 'Enter a chat ID or a public @channelname.';
      else if (!ALIAS.test(chat) && !(CHAT.test(chat) && Math.abs(Number(chat)) <= MAX_ID)) errors.chatId = 'Use a numeric chat ID like -1001234567890, or a public @channelname (a letter, then up to 31 letters, digits or _).';
      const thread = d.tgThread.trim() ? int(d.tgThread) : 0;
      if (thread === undefined || thread < 0 || thread > MAX_ID || d.tgThread.trim() && !thread) errors.threadId = 'Use the topic’s positive ID, or leave it empty for the main chat.';
      const o: Record<string, unknown> = { ...(same?.telegram ?? {}), botToken: d.tgToken, chatId: chat };
      put(o, 'messageThreadId', thread);
      put(o, 'silent', d.tgSilent); put(o, 'protectContent', d.tgProtect); put(o, 'disableLinkPreview', d.tgNoPreview);
      put(o, 'baseURL', base(d.tgBase, d.tgInsecure, 'Bot API server URL')); put(o, 'allowInsecureHTTP', d.tgInsecure);
      config.telegram = o as NonNullable<NotificationConfig['telegram']>;
      break;
    }
    case 'ntfy': {
      const topic = d.ntfyTopic.trim();
      if (!TOPIC.test(topic)) errors.topic = topic ? 'Use 1–64 letters, digits, - or _.' : 'Enter the topic.';
      const o: Record<string, unknown> = { ...(same?.ntfy ?? {}), topic };
      if (d.ntfyAuth === 'token') {
        if (!d.ntfyToken) errors.ntfyAuth = 'Enter the access token, or choose another login.';
        else if (d.ntfyToken.length > 512 || !VISIBLE.test(d.ntfyToken)) errors.ntfyAuth = 'Paste the access token without spaces or line breaks (at most 512 characters).';
        put(o, 'token', d.ntfyToken); delete o.username; delete o.password;
      } else if (d.ntfyAuth === 'basic') {
        if (!d.ntfyUser || !d.ntfyPass) errors.ntfyAuth = 'Enter both a user name and a password.';
        else if (d.ntfyUser.includes(':') || bytesOf(d.ntfyUser) > 128 || lineBreak(d.ntfyUser)) errors.ntfyAuth = 'Use a user name of at most 128 bytes on one line, without “:”.';
        else if (bytesOf(d.ntfyPass) > 512 || lineBreak(d.ntfyPass)) errors.ntfyAuth = 'Use a password of at most 512 bytes on one line.';
        delete o.token; put(o, 'username', d.ntfyUser); put(o, 'password', d.ntfyPass);
      } else { delete o.token; delete o.username; delete o.password; }
      put(o, 'priority', Number(d.ntfyPriority) || 0);
      // Unchanged tag text keeps the stored array exactly; edited text is split on commas, which tags can't contain.
      if (d.ntfyTags !== tagText(same?.ntfy?.tags)) {
        const tags = d.ntfyTags.split(',').map(tag => tag.trim()).filter(Boolean);
        if (tags.length > 16) errors.tags = 'Use at most 16 tags.';
        else if (tags.some(tag => bytesOf(tag) > 64 || lineBreak(tag))) errors.tags = 'Each tag is at most 64 bytes on one line.';
        put(o, 'tags', tags.length ? tags : undefined);
      }
      put(o, 'baseURL', base(d.ntfyBase, d.ntfyInsecure, 'ntfy server URL', 'serverUrl')); put(o, 'allowInsecureHTTP', d.ntfyInsecure);
      config.ntfy = o as NonNullable<NotificationConfig['ntfy']>;
      break;
    }
    case 'gotify': {
      const url = base(d.gotifyBase, d.gotifyInsecure, 'Gotify server URL', 'serverUrl', true);
      const o: Record<string, unknown> = { ...(same?.gotify ?? {}), appToken: token(d.gotifyToken, 'appToken', 'application token'), baseURL: url };
      put(o, 'priority', Number(d.gotifyPriority) || 0); put(o, 'allowInsecureHTTP', d.gotifyInsecure);
      config.gotify = o as NonNullable<NotificationConfig['gotify']>;
      break;
    }
    case 'pushover': {
      const key = (value: string, field: MField, what: string): string => {
        if (!value) errors[field] = `Enter the ${what}.`;
        else if (!KEY30.test(value)) errors[field] = `The ${what} is exactly 30 letters and digits.`;
        return value;
      };
      const o: Record<string, unknown> = { ...(same?.pushover ?? {}), appToken: key(d.poToken, 'appToken', 'API token'), userKey: key(d.poUser, 'userKey', 'user or group key') };
      const device = d.poDevice.trim(), sound = d.poSound.trim(), priority = Number(d.poPriority) || 0;
      if (device && !DEVICE.test(device)) errors.device = 'Use one device name: up to 25 letters, digits, - or _.';
      if (sound && !SOUND.test(sound)) errors.sound = 'Use a sound name of up to 64 letters, digits, - or _.';
      put(o, 'device', device); put(o, 'priority', priority); put(o, 'sound', sound);
      if (priority === 2) {
        const retry = int(d.poRetry), expire = int(d.poExpire);
        if (retry === undefined || retry < 30 || retry > 10_800) errors.emergency = 'Repeat every 30 to 10,800 seconds.';
        else if (expire === undefined || expire < 1 || expire > 10_800) errors.emergency = 'Stop repeating after at most 10,800 seconds (3 hours).';
        else if (retry > expire) errors.emergency = 'The repeat interval can’t be longer than the time it keeps repeating.';
        put(o, 'retrySeconds', retry); put(o, 'expireSeconds', expire);
      } else { delete o.retrySeconds; delete o.expireSeconds; }
      put(o, 'baseURL', base(d.poBase, d.poInsecure, 'API base URL')); put(o, 'allowInsecureHTTP', d.poInsecure);
      config.pushover = o as NonNullable<NotificationConfig['pushover']>;
      break;
    }
  }
}

// ---------- message limits (the leaf's, before network I/O)

const KNOWN_PLACEHOLDER = /\{\{(?:event_kind|monitor_name|monitor_id|incident_id|observations_summary|idempotency_key)\}\}/gu;
/** Template text without known placeholders: a lower bound on what any substitution produces. */
export const literalText = (template: string): string => template.replace(KNOWN_PLACEHOLDER, '');
const codePoints = (text: string): number => [...text].length;
const slackCost = (text: string): number => codePoints(text) + 4 * (text.split('&').length - 1) + 3 * (text.split(/[<>]/u).length - 1);
const utf16 = (text: string): number => text.length;

export const TITLE_LIMIT = 'One line, at most 250 characters (1,000 bytes) after substitution.';
export const TEXT_LIMIT: Record<MessagingKind, { limit: number; cost: (text: string) => number; combined: boolean; unit: string; rule: string }> = {
  slack: { limit: 4000, cost: slackCost, combined: true, unit: 'characters', rule: 'Title and text together: at most 4,000 characters after Slack escaping (& counts as 5, < and > as 4).' },
  telegram: { limit: 4096, cost: utf16, combined: true, unit: 'UTF-16 units', rule: 'Title and text together: at most 4,096 UTF-16 units (most emoji count as 2).' },
  ntfy: { limit: 4096, cost: bytesOf, combined: false, unit: 'bytes', rule: 'Text: at most 4,096 bytes.' },
  gotify: { limit: 16_384, cost: bytesOf, combined: false, unit: 'bytes', rule: 'Text: at most 16 KiB.' },
  pushover: { limit: 1024, cost: codePoints, combined: false, unit: 'characters', rule: 'Text: at most 1,024 characters.' },
};

/** Fixed text that can never fit, even with every placeholder empty. Custom templates are never shortened. */
export function templateProblem(kind: MessagingKind, subject: string, text: string): string | undefined {
  const title = literalText(subject);
  if (codePoints(title) > 250 || bytesOf(title) > 1000) return 'The title is longer than 250 characters before substitution.';
  if (!text) return undefined;
  const rule = TEXT_LIMIT[kind];
  const used = rule.cost(literalText(text)) + (rule.combined ? rule.cost(title) + 1 : 0);
  return used > rule.limit ? `The fixed text alone uses ${used.toLocaleString()} of ${rule.limit.toLocaleString()} ${rule.unit}. ${rule.rule}` : undefined;
}

// ---------- presentation (never a credential, topic, chat or user key)

export const NTFY_PRIORITIES = [
  { value: '', label: 'Server default (3)' }, { value: '1', label: '1 · min' }, { value: '2', label: '2 · low' },
  { value: '3', label: '3 · default' }, { value: '4', label: '4 · high' }, { value: '5', label: '5 · urgent' },
];
export const GOTIFY_PRIORITIES = Array.from({ length: 11 }, (_, n) => ({ value: String(n), label: n === 0 ? '0 · lowest' : n === 10 ? '10 · highest' : String(n) }));
export const PUSHOVER_PRIORITIES = [
  { value: '-2', label: '−2 · lowest, no alert' }, { value: '-1', label: '−1 · low, quiet' }, { value: '0', label: '0 · normal' },
  { value: '1', label: '1 · high, bypasses quiet hours' }, { value: '2', label: '2 · emergency, repeats until acknowledged' },
];

export function pushoverPriorityText(p: NotificationConfig['pushover']): string {
  const priority = p?.priority ?? 0;
  if (priority === 2) return `emergency, every ${span((p?.retrySeconds ?? 0) * 1000)} for up to ${span((p?.expireSeconds ?? 0) * 1000)}`;
  return PUSHOVER_PRIORITIES.find(option => option.value === String(priority))?.label.replace(/^\S+ · /u, '') ?? `priority ${priority}`;
}

/** A masked route for lists and conflict review: the API origin plus non-secret options. */
export function messagingRoute(c: NotificationConfig): { where: string; facts: string[] } | undefined {
  const facts = (...items: (string | false | undefined)[]): string[] => items.filter((item): item is string => Boolean(item));
  switch (c.kind) {
    case 'slack': return { where: c.slack?.webhookURL ? maskedUrl(c.slack.webhookURL) : 'no URL', facts: [] };
    case 'telegram': {
      const t = c.telegram;
      return { where: maskedUrl(t?.baseURL || DEFAULT_BASE.telegram), facts: facts(t?.messageThreadId ? 'one forum topic' : 'one chat', t?.silent && 'silent', t?.protectContent && 'protected') };
    }
    case 'ntfy': {
      const n = c.ntfy;
      return { where: maskedUrl(n?.baseURL || DEFAULT_BASE.ntfy), facts: facts('one topic', n?.token ? 'access token' : n?.username ? 'user login' : 'no login', Boolean(n?.priority) && `priority ${n?.priority}`) };
    }
    case 'gotify': {
      const g = c.gotify;
      return { where: g?.baseURL ? maskedUrl(g.baseURL) : 'no server', facts: [`priority ${g?.priority ?? 0}`] };
    }
    case 'pushover': {
      const p = c.pushover;
      return { where: maskedUrl(p?.baseURL || DEFAULT_BASE.pushover), facts: facts(p?.device && `device ${p.device}`, pushoverPriorityText(p)) };
    }
  }
  return undefined;
}

// ---------- destination identity (mirrors the leaf's route digest inputs, without hashing)

const route = (raw: string, fallback = ''): string => {
  const text = raw.trim() || fallback;
  try { const u = new URL(text); return `${u.protocol}//${u.host}${u.pathname.replace(/\/+$/u, '')}`; } catch { return text; }
};

/** What picks the destination. A change means a new destination; owed alerts and receipts don't move. */
export function routeKey(kind: MessagingKind, d: MessagingDraft): string {
  switch (kind) {
    case 'slack': {
      try {
        const u = new URL(d.slackUrl.trim()), parts = u.pathname.split('/');
        if (parts.length === 5 && parts[1] === 'services' && /^T[A-Z0-9]{1,63}$/u.test(parts[2]) && /^B[A-Z0-9]{1,63}$/u.test(parts[3])) return `${u.protocol}//${u.host}${parts.slice(0, 4).join('/')}`;
      } catch { /* not a URL yet */ }
      return route(d.slackUrl);
    }
    case 'telegram': return [route(d.tgBase, DEFAULT_BASE.telegram), d.tgChat.trim().toLowerCase(), String(Number(d.tgThread.trim()) || 0)].join('\n');
    case 'ntfy': return [route(d.ntfyBase, DEFAULT_BASE.ntfy), d.ntfyTopic.trim()].join('\n');
    case 'gotify': return route(d.gotifyBase);
    case 'pushover': return [route(d.poBase, DEFAULT_BASE.pushover), d.poUser, d.poDevice.trim()].join('\n');
  }
}

/** Credentials only: rotating them keeps the destination and its receipts. */
export function credentialKey(kind: MessagingKind, d: MessagingDraft): string {
  switch (kind) {
    case 'slack': return d.slackUrl.trim();
    case 'telegram': return d.tgToken;
    case 'ntfy': return [d.ntfyAuth, d.ntfyToken, d.ntfyUser, d.ntfyPass].join('\n');
    case 'gotify': return d.gotifyToken;
    case 'pushover': return d.poToken;
  }
}
