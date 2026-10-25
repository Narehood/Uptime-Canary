import type { ReactNode } from 'react';
import { GAMEDIG_GAMES, GAMEDIG_VERSION } from '@uptime-canary/shared';
import type { GameDigGame, GameDigMonitorOptions, Monitor } from '@uptime-canary/shared';
import { browserEndpointProblem } from './browser';
import { MASK, maskSecretNamed, secretNamed } from './format';
import { parseIPv4, parseIPv6, tailscaleTargetAccepted } from './tailscale';

/**
 * GameDig game-server checks (peer protocol 19). Canary's node runs no JavaScript and no game protocol code: each assigned
 * node sends one bounded query to a separate, operator-run GameDig query service over HTTPS (or plain HTTP on a private
 * network when allowed) with that service's own bearer token, and maps only its exact answer to up, down or pending. The
 * node keeps all scheduling, state, incidents and HA. One integration exposes the pinned upstream catalogue's game IDs; it
 * isn't a separately validated protocol per game. The node validates everything; these rules mirror its
 * `gamedigmonitor.Validate` without being stricter, so a stored monitor can always be saved again unchanged: the target uses
 * the Tailscale peer rule (the node calls `tailscalemonitor.Validate`), and the service address uses the same Go url.Parse
 * port as the browser service's http(s) origin. Pure apart from the facts' JSX.
 */

export { GAMEDIG_GAMES, GAMEDIG_VERSION };

/** The node's limits: UTF-8 bytes for the address and the game token, ASCII characters for the service token. */
export const GAMEDIG_ENDPOINT_MAX = 2048;
export const GAMEDIG_BEARER_MIN = 24;
export const GAMEDIG_BEARER_MAX = 256;
export const GAMEDIG_TOKEN_MAX = 1024;
export const GAMEDIG_PORT_MAX = 65_535;
/** Game queries one node runs at the same time, across all its monitors, and queries the service answers at once. Neither queues. */
export const GAMEDIG_PARALLEL = 2;
/** The node's fixed result messages. The service address, both tokens and the game server's own answer stay on the node. */
export const GAMEDIG_SUMMARY = { up: 'Game server replied', down: 'Game server query failed', pending: 'Game query service unavailable' } as const;
/** The pending causes, shared by the form note, the facts and the detail page. */
export const GAMEDIG_PENDING = 'the query service can’t be reached, presents a certificate the node doesn’t trust, refuses the service token, is already answering two queries, or answers anything but its exact reply to this query (another status, a redirect, another GameDig version, another query’s ID, or an unreadable answer); the service can’t load GameDig, doesn’t know the game, or runs out of memory or time for the query; two game queries are already running on this node; the check is cancelled; or no answer arrives within the timeout';

export interface GameDigDraft {
  /** A catalogue game ID, as stored or chosen. */
  game: string;
  /** The game server's host name or IP address. Trimmed when sent. */
  target: string;
  /** As typed. Empty or 0 omits `port` (the game's default), as Go's omitempty encoding does. */
  port: string;
  givenPortOnly: boolean;
  /** The private game credential. Sent exactly as typed, never trimmed. Empty omits `token`. */
  token: string;
  /** As stored or typed. An unedited stored address goes back byte for byte; a typed one is trimmed. */
  endpoint: string;
  /** The query service's own token. Kept byte for byte while unedited, like the address. */
  bearer: string;
  /** `allowInsecureHTTP`: needed for an http:// address. */
  insecure: boolean;
}

export type GameDigField = 'gamedigGame' | 'gamedigTarget' | 'gamedigPort' | 'gamedigToken' | 'gamedigEndpoint' | 'gamedigBearer';
export const GAMEDIG_FIELD_LABEL: Record<GameDigField, string> = {
  gamedigGame: 'Game', gamedigTarget: 'Game server address', gamedigPort: 'Game port', gamedigToken: 'Game token', gamedigEndpoint: 'Query service address', gamedigBearer: 'Query service token',
};

type Obj = Record<string, unknown>;
const str = (value: unknown): string => typeof value === 'string' ? value : '';
const isObj = (value: unknown): value is Obj => typeof value === 'object' && value !== null && !Array.isArray(value);
const utf8 = (text: string): number => new TextEncoder().encode(text).length;

export function gamedigDraftFrom(o: GameDigMonitorOptions | undefined, target: string): GameDigDraft {
  const port = o?.port;
  return {
    game: str(o?.game), target, port: typeof port === 'number' && Number.isFinite(port) && port !== 0 ? String(port) : '', givenPortOnly: o?.givenPortOnly === true,
    token: str(o?.token), endpoint: str(o?.endpoint), bearer: str(o?.bearerToken), insecure: o?.allowInsecureHTTP === true,
  };
}

// ---------- the catalogue: literal public metadata from the pinned GameDig release, never executed

const GAME_MAP = new Map(GAMEDIG_GAMES.map(game => [game.id, game]));
/** The catalogue by name (then ID), for the picker. */
export const GAMEDIG_BY_NAME: readonly GameDigGame[] = [...GAMEDIG_GAMES].sort((a, b) => a.name.localeCompare(b.name, 'en', { sensitivity: 'base' }) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
export const gamedigGame = (id: string): GameDigGame | undefined => GAME_MAP.get(id);
/** “Minecraft (minecraft)”, or the bare ID when this dashboard's catalogue doesn't list it. */
export const gamedigGameLabel = (id: string): string => { const game = GAME_MAP.get(id); return game ? `${game.name} (${game.id})` : id; };

/** Games whose name or ID contains `query`, ignoring case, by name. An empty query lists them all. */
export function gamedigFilter(query: string): readonly GameDigGame[] {
  const q = query.trim().toLowerCase();
  return q ? GAMEDIG_BY_NAME.filter(game => game.name.toLowerCase().includes(q) || game.id.toLowerCase().includes(q)) : GAMEDIG_BY_NAME;
}

// ---------- the node's rules: gamedigmonitor.Validate

/**
 * The node accepts a catalogue ID only, compared exactly. A stored ID this dashboard's catalogue doesn't list (from a newer
 * node) is kept while unchanged, and that node decides; any other unknown ID is refused here.
 */
export function gamedigGameProblem(id: string, storedId: string | undefined): string | undefined {
  if (GAME_MAP.has(id) || (id && id === storedId)) return undefined;
  if (!id) return 'Choose the game this server runs.';
  return `“${id}” isn’t a game ID in GameDig ${GAMEDIG_VERSION}’s catalogue. Choose the game from the list.`;
}

/**
 * Why the node would refuse a game server address, or undefined when it accepts it. Acceptance is the Tailscale peer rule
 * alone (one literal IP that isn't unspecified or multicast, or an ASCII DNS name, at most 253 bytes); the message names
 * the most specific problem found, in game-server words.
 */
export function gamedigTargetProblem(target: string): string | undefined {
  if (tailscaleTargetAccepted(target)) return undefined;
  if (!target) return 'Enter the game server’s host name or IP address, like play.example.com or 203.0.113.10.';
  if (/[\s\x00-\x1f\x7f]/u.test(target)) return 'Enter one host name or address, without spaces or line breaks.';
  if (/^[A-Za-z][A-Za-z0-9+.-]*:\/\//u.test(target) || target.includes('/') || target.includes('\\')) return 'Enter only the server’s host name or address, not a URL or a path.';
  if (target.includes('%')) return 'Remove the %zone and enter the address alone.';
  if (target.includes('[') || target.includes(']')) return 'Remove the brackets and enter the IPv6 address alone. The port goes in its own field.';
  if (target.includes('@')) return 'Enter the server alone, without a user name or @.';
  // A literal address that parses, without a zone, is refused only as unspecified or multicast.
  const first = /[.:]/u.exec(target)?.[0];
  if ((first === '.' && parseIPv4(target)) || (first === ':' && parseIPv6(target))) return '0.0.0.0, :: and multicast addresses aren’t a game server. Enter the server’s own address.';
  if (target.includes(':')) {
    return /^[^:]+:\d+$/u.test(target)
      ? 'Remove the port from the address and enter it in the port field below.'
      : 'That isn’t a valid IPv6 address. Enter it without brackets, a port or a zone, like 2001:db8::10.';
  }
  if (utf8(target) > 253) return 'Use at most 253 characters.';
  if (/[^\x00-\x7f]/u.test(target)) return 'Use only ASCII letters, digits, hyphens and dots. For an international name, enter its xn-- form.';
  const bad = /[^A-Za-z0-9.-]/u.exec(target);
  if (bad) return `“${bad[0]}” isn’t allowed. Use letters, digits, hyphens and dots, like play.example.com.`;
  const labels = (target.endsWith('.') ? target.slice(0, -1) : target).split('.');
  if (labels.some(label => !label)) return 'Remove the empty part between two dots, or the extra dot at the end.';
  if (labels.some(label => label.length > 63)) return 'Each dot-separated part of the name is at most 63 characters.';
  if (labels.some(label => label.startsWith('-') || label.endsWith('-'))) return 'A part of the name can’t start or end with a hyphen.';
  return 'Enter a host name, or an IPv4 or IPv6 address.';
}

/**
 * 1–65,535, or empty (or 0) for the game's default port. Querying only the given port needs one, and so does a game this
 * catalogue lists without a default port: the node refuses that configuration when it is saved rather than running it as
 * an outage (`c.Port == 0 && games[c.Game] == 0`). `game` is the chosen ID; one this dashboard's catalogue doesn't list
 * (a stored ID from a newer node) gets no default-port rule here, so that node decides.
 */
export function gamedigPortProblem(port: string, givenPortOnly: boolean, game?: string): string | undefined {
  const text = port.trim();
  if (text && (!/^\d+$/u.test(text) || Number(text) > GAMEDIG_PORT_MAX)) return 'Use a whole number from 1 to 65,535, or leave it empty for the game’s default port.';
  if (givenPortOnly && Number(text) === 0) return 'Enter the port to query only that port, or turn off “Only query this port”.';
  const known = game === undefined ? undefined : GAME_MAP.get(game);
  if (known && !known.defaultPort && Number(text) === 0) return `GameDig’s catalogue lists no default port for ${known.name}, so the node needs this server’s port. Enter it here.`;
  return undefined;
}

/** Whether the chosen game needs a port: it's in this catalogue without a default. Unknown IDs aren't judged here. */
export const gamedigNeedsPort = (game: string): boolean => { const known = GAME_MAP.get(game); return Boolean(known && !known.defaultPort); };

/** Optional: valid text of at most 1,024 UTF-8 bytes without control characters (Go's unicode.IsControl, the Cc category). */
export function gamedigTokenProblem(token: string): string | undefined {
  if (!token) return undefined;
  if (/\p{Cs}/u.test(token)) return 'The token holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  if (/\p{Cc}/u.test(token)) return 'Remove line breaks, tabs and other control characters from the token.';
  if (utf8(token) > GAMEDIG_TOKEN_MAX) return 'Use at most 1,024 bytes. Characters outside ASCII take 2 to 4 bytes each.';
  return undefined;
}

/** Required: 24–256 printable ASCII characters without spaces (bytes 33–126), as the service itself requires. */
export function gamedigBearerProblem(token: string): string | undefined {
  if (!token) return 'Enter the query service’s token: the CANARY_GAME_QUERY_TOKEN it was started with.';
  if (/[\x00-\x1f\x7f]/u.test(token)) return 'Remove line breaks, tabs and other control characters from the token.';
  if (/\s/u.test(token)) return 'Tokens have no spaces. Paste the token alone, without “Bearer ”: the node adds that itself.';
  if (/[^\x21-\x7e]/u.test(token)) return 'Use printable ASCII only: letters, digits and punctuation. Paste the token alone, without quotes or other text.';
  if (token.length < GAMEDIG_BEARER_MIN) return `Use at least 24 characters (${token.length} so far): the service refuses shorter tokens.`;
  if (token.length > GAMEDIG_BEARER_MAX) return 'Use at most 256 characters.';
  return undefined;
}

const EXAMPLE = 'https://game-query.internal:3121';

/**
 * Game-server wording for an origin's authority that Go's url.Parse refuses: user information, a bracketed IPv6 literal
 * or a port that isn't digits. Only consulted after the shared rule refused the origin; other refusals keep its message.
 */
function authorityMessage(authority: string): string | undefined {
  if (authority.includes('@')) return 'Remove user:password@ from the address. Enter the service’s token as the query service token below.';
  if (authority.includes('[')) {
    const close = authority.lastIndexOf(']');
    return authority.startsWith('[') && close > 0 && !/^(?::\d*)?$/u.test(authority.slice(close + 1))
      ? 'After the host, a port is digits only, like :3121.'
      : 'Put an IPv6 address in brackets, like https://[fd00::10]:3121, and nothing else: an IPv4 address goes without brackets.';
  }
  const colon = authority.indexOf(':');
  return colon >= 0 && !/^:\d*$/u.test(authority.slice(colon)) ? 'After the host, a port is digits only, like :3121.' : undefined;
}

/**
 * Exactly the node's admission rule for the service address, or the first reason it refuses one: url.Parse succeeds (the
 * same Go 1.27 port the browser service's http(s) origin uses: scheme in any case, strict host colons, IPv6 literals,
 * ports as digits that aren't range-checked, a bare trailing "?" or "#" allowed); the scheme is https, or http with plain
 * HTTP allowed; there is a host; no user information, fragment text or query; at most "/" as the path; at most 2,048 bytes.
 */
export function gamedigEndpointProblem(endpoint: string, allowHttp: boolean): string | undefined {
  if (!endpoint) return `Enter the game query service’s address, like ${EXAMPLE}.`;
  if (utf8(endpoint) > GAMEDIG_ENDPOINT_MAX) return 'Use at most 2,048 bytes. Characters outside ASCII take 2 to 4 bytes each.';
  if (/\p{Cs}/u.test(endpoint)) return 'The address holds a broken character (an unpaired surrogate). Type it again instead of pasting it.';
  const hash = endpoint.indexOf('#');
  const main = hash < 0 ? endpoint : endpoint.slice(0, hash);
  if (/[\x00-\x1f\x7f]/u.test(main)) return 'Remove line breaks, tabs and other control characters.';
  if (hash >= 0 && hash < endpoint.length - 1) return 'Remove the #fragment.';
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/u.exec(main);
  const name = scheme?.[1]?.toLowerCase();
  if (!scheme || (name !== 'http' && name !== 'https')) return `Start with https://, or http:// for a service on a private network, like ${EXAMPLE}.`;
  const rest = main.slice(scheme[0].length);
  const beforeQuery = rest.includes('?') ? rest.slice(0, rest.indexOf('?')) : rest;
  if (!beforeQuery.startsWith('//')) return `Write a complete address with // and a host, like ${EXAMPLE}.`;
  const slash = beforeQuery.indexOf('/', 2);
  const authority = beforeQuery.slice(2, slash < 0 ? undefined : slash);
  const originProblem = browserEndpointProblem(`${scheme[0]}//${authority}`);
  if (originProblem) return authorityMessage(authority) ?? originProblem;
  if (browserEndpointProblem(endpoint)) return 'Enter only the service’s origin: remove the path and the query. The node adds /v1/query itself.';
  if (name === 'http' && !allowHttp) return 'Use https://, or allow plain HTTP below for a service on a private network you control.';
  return undefined;
}

/** How the node reaches the service, from the address's scheme alone, so it can be named without showing the address. */
export function gamedigEndpointScheme(endpoint: string): 'https' | 'http' | 'unknown' {
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/u.exec(endpoint)?.[1]?.toLowerCase();
  return scheme === 'https' || scheme === 'http' ? scheme : 'unknown';
}

const clone = (value: unknown): Obj => isObj(value) ? structuredClone(value) : {};
const KNOWN = new Set(['game', 'port', 'givenPortOnly', 'token', 'endpoint', 'bearerToken', 'allowInsecureHTTP']);

export interface GameDigBuilt { value: GameDigMonitorOptions; target: string }

/**
 * Builds `gamedig` on top of the stored object, so its key order and keys from a newer node survive; a new object follows
 * Go's field order. The address and the service token go back byte for byte while unedited and are trimmed once typed;
 * the game token is always sent exactly as typed. Go's omitempty fields are omitted when 0, false or empty. A refused
 * value is still what would have been sent, so the node refuses it too.
 */
export function buildGameDig(stored: GameDigMonitorOptions | undefined, d: GameDigDraft, errors: Partial<Record<GameDigField, string>>): GameDigBuilt {
  const o = clone(stored);
  const target = d.target.trim();
  const targetProblem = gamedigTargetProblem(target);
  if (targetProblem) errors.gamedigTarget = targetProblem;

  const gameProblem = gamedigGameProblem(d.game, stored ? str(stored.game) : undefined);
  if (gameProblem) errors.gamedigGame = gameProblem;
  o.game = d.game;

  const port = d.port.trim();
  const portProblem = gamedigPortProblem(port, d.givenPortOnly, d.game);
  if (portProblem) errors.gamedigPort = portProblem;
  if (!/^\d+$/u.test(port)) { if (port) o.port = port; else delete o.port; }
  else if (Number(port) !== 0) o.port = Number(port);
  else delete o.port;
  if (d.givenPortOnly) o.givenPortOnly = true; else delete o.givenPortOnly;

  const tokenProblem = gamedigTokenProblem(d.token);
  if (tokenProblem) errors.gamedigToken = tokenProblem;
  if (d.token) o.token = d.token; else delete o.token;

  const endpoint = d.endpoint === str(stored?.endpoint) ? d.endpoint : d.endpoint.trim();
  const endpointProblem = gamedigEndpointProblem(endpoint, d.insecure);
  if (endpointProblem) errors.gamedigEndpoint = endpointProblem;
  o.endpoint = endpoint;

  const bearer = d.bearer === str(stored?.bearerToken) ? d.bearer : d.bearer.trim();
  const bearerProblem = gamedigBearerProblem(bearer);
  if (bearerProblem) errors.gamedigBearer = bearerProblem;
  o.bearerToken = bearer;

  if (d.insecure) o.allowInsecureHTTP = true; else delete o.allowInsecureHTTP;
  return { value: o as unknown as GameDigMonitorOptions, target };
}

/**
 * The node compares `gamedig` through Go's JSON encoding: the game, the address and the service token are always encoded;
 * port 0, false flags and an empty game token are absent. Keys from a newer node compare as stored.
 */
export function gamedigCanonical(value: unknown): string {
  const o = isObj(value) ? value : {};
  const out: Obj = { game: str(o.game), endpoint: str(o.endpoint), bearerToken: str(o.bearerToken) };
  for (const key of Object.keys(o).sort()) {
    const inner = o[key];
    if (key === 'game' || key === 'endpoint' || key === 'bearerToken' || inner === undefined || inner === null) continue;
    if ((key === 'port' && inner === 0) || (key === 'token' && inner === '') || ((key === 'givenPortOnly' || key === 'allowInsecureHTTP') && inner === false)) continue;
    out[key] = inner;
  }
  return JSON.stringify(out);
}

/**
 * A copy for masked previews, conflict views and details: the whole service address, the service token and the game token
 * are replaced, and unknown keys from a newer node have the text of credential-named ones hidden. The game, the port and
 * both flags stay. A pasted masked copy is refused, never saved with the placeholder.
 */
export function gamedigMasked<T>(o: T): T {
  if (!isObj(o)) return o;
  const copy = structuredClone(o) as Obj;
  for (const key of Object.keys(copy)) {
    if (key === 'endpoint' || key === 'bearerToken' || key === 'token') { if (copy[key]) copy[key] = MASK; }
    else if (!KNOWN.has(key)) copy[key] = maskSecretNamed(copy[key], secretNamed(key));
  }
  return copy as unknown as T;
}

export interface GameDigFact { label: string; value: ReactNode }

const hidden = (text: string): ReactNode => <span className="muted">{text}</span>;

function queried(m: Monitor, o: GameDigMonitorOptions | undefined): GameDigFact {
  const port = typeof o?.port === 'number' && o.port ? o.port : undefined;
  return {
    label: 'Queried through',
    value: <>Your GameDig query service, never the node itself: every assigned node sends it one query{o ? <> for <b>{gamedigGameLabel(str(o.game))}</b></> : null} at <span className="mono wrap">{m.target}</span>{port ? <>, port <span className="mono">{port}</span>{o?.givenPortOnly ? ' only' : ''}</> : o ? ', the game’s default port' : ''}, and reads back only up, down or pending. GameDig {GAMEDIG_VERSION}’s own code for the game runs in the service, which reaches the game server from its own network. One query, no player list; nothing on the server is changed.</>,
  };
}

function rules(): GameDigFact[] {
  return [
    { label: 'Up when', value: 'GameDig gets the game server’s reply within the query time, with a measured round trip' },
    { label: 'Down when', value: 'GameDig reports that its query of the server failed, for example no reply in time, a refused connection, a server name that doesn’t resolve, or an answer that isn’t this game’s' },
    { label: 'Pending when', value: `The node gets no definite answer: ${GAMEDIG_PENDING}. Pending is never inverted and never recovers an outage.` },
    { label: 'Latency', value: 'The round trip GameDig reports for the reply, measured by the query service from its network, not by the node; a dash for every other result' },
    { label: 'Results', value: <>Only “{GAMEDIG_SUMMARY.up}”, “{GAMEDIG_SUMMARY.down}” or “{GAMEDIG_SUMMARY.pending}”. The service address, both tokens, the server’s name and players and GameDig’s own messages stay out of them.</> },
  ];
}

/**
 * Plain-language facts. Viewers receive no `gamedig` object, so the game, port and service rows say hidden rather than
 * implying nothing is set; the server address is the target and stays visible. The address and both tokens show only
 * when `reveal` is on.
 */
export function gamedigFacts(m: Monitor, viewer: boolean, reveal: boolean): GameDigFact[] {
  const o = m.gamedig;
  if (viewer || !o) return [queried(m, undefined), ...rules(), { label: 'Game, port and query service', value: hidden('Hidden from viewers') }];
  const game = gamedigGame(str(o.game));
  const port = typeof o.port === 'number' && o.port ? o.port : undefined;
  const endpoint = str(o.endpoint);
  const scheme = gamedigEndpointScheme(endpoint);
  return [
    queried(m, o),
    {
      label: 'Game',
      value: <>
        {game ? <>{game.name} · <span className="mono">{game.id}</span></> : <><span className="mono">{str(o.game) || '(missing)'}</span> · <span className="muted">not in this dashboard’s GameDig {GAMEDIG_VERSION} catalogue</span></>}
        {' · '}{port ? <>port <span className="mono">{port}</span>{o.givenPortOnly ? ', only this port' : ''}</> : game?.defaultPort ? <>default port <span className="mono">{game.defaultPort}</span></> : game ? <span className="warn-text">no port set, and the catalogue lists no default: the node refuses to save this setting, so add the server’s port</span> : <span className="muted">the game’s default port, as the node’s catalogue lists it</span>}
      </>,
    },
    { label: 'Game token', value: o.token ? <>{reveal ? <span className="mono wrap">{o.token}</span> : hidden('set · hidden')} · passed to the game server by the query service</> : hidden('None') },
    {
      label: 'Query service',
      value: <>
        {endpoint ? (reveal ? <span className="mono wrap">{endpoint}</span> : hidden('(set · hidden)')) : <span className="warn-text">(missing)</span>}
        {scheme === 'https' ? ' · HTTPS, verified against the node’s system trust' : scheme === 'http' ? <> · <span className="warn-text">plain HTTP, so both tokens and the server address travel unencrypted</span></> : null}
      </>,
    },
    { label: 'Query service token', value: o.bearerToken ? <>{reveal ? <span className="mono wrap">{o.bearerToken}</span> : hidden('set · hidden')} · sent only to the query service, never to the game server</> : <span className="warn-text">(missing)</span> },
    ...rules(),
  ];
}
