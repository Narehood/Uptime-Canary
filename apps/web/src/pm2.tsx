import type { ReactNode } from 'react';
import type { Monitor, PM2MonitorOptions } from '@uptime-canary/shared';
import { MASK, maskSecretNamed, secretNamed } from './format';

/**
 * PM2 process checks (peer protocol 18). Each assigned node asks the PM2 daemon already running on its own machine, as
 * the account Canary runs under, about every process with exactly the target's name (and namespace, if one is set). The
 * queries only read: nothing starts a daemon or controls a process. The connection is chosen explicitly, so Unix and
 * Windows nodes need separate monitors. The node validates everything; these rules mirror it without being stricter, so
 * a stored configuration can always be saved again unchanged. Pure apart from the facts' JSX.
 */

export type Pm2Transport = PM2MonitorOptions['transport'];
/** The node's limit for a process name and a namespace, in UTF-8 bytes. */
export const PM2_TEXT_MAX = 256;
/** A Unix socket path holds at most 107 bytes; the node refuses a home whose rpc.sock path is longer. */
export const PM2_SOCKET_MAX = 107;
export const PM2_SOCKET = 'rpc.sock';
/** Exact supported releases; unreviewed versions still stay pending. */
export const PM2_VERSION = '6.0.8 or 7.1.1';

export interface Pm2Draft {
  /** The exact process name, which is the monitor's target. Never trimmed: names are compared exactly. */
  target: string;
  /** As stored or imported. A connection this dashboard doesn't know is sent only on a monitor already stored with it. */
  transport: string;
  /** Unix only. Kept while the Windows pipe is chosen, so switching back restores it, but never sent with the pipe. */
  home: string;
  /** Empty omits the field: the name in every namespace. */
  namespace: string;
}

export type Pm2Field = 'pm2Target' | 'pm2Transport' | 'pm2Home' | 'pm2Namespace';
export const PM2_FIELD_LABEL: Record<Pm2Field, string> = { pm2Target: 'Process name', pm2Transport: 'Daemon connection', pm2Home: 'PM2 home', pm2Namespace: 'Namespace' };

const str = (value: unknown): string => typeof value === 'string' ? value : '';

/** The two connections this dashboard knows. A newer node may store another. */
export const pm2KnownTransport = (transport: string): boolean => transport === 'unix' || transport === 'windows-pipe';

/** A new monitor starts with a Unix socket and an empty home: the operator names an existing daemon's home. */
export function pm2DraftFrom(o: PM2MonitorOptions | undefined, target: string): Pm2Draft {
  return { target, transport: str(o?.transport) || 'unix', home: str(o?.home), namespace: str(o?.namespace) };
}

// ---------- the node's rules

const utf8 = (text: string): number => new TextEncoder().encode(text).length;

/**
 * Text the node compares exactly: valid UTF-8 (no unpaired surrogates), no control characters (Go's unicode.IsControl,
 * the Cc category), no Unicode White_Space at either end (Go's strings.TrimSpace) and a byte limit. Inner spaces and any
 * punctuation are literal.
 */
function exactTextProblem(text: string, max: number, noun: string): string | undefined {
  if (/\p{Cs}/u.test(text)) return `The ${noun} holds a broken character (an unpaired surrogate). Type it again instead of pasting it.`;
  if (/\p{Cc}/u.test(text)) return `Remove line breaks, tabs and other control characters from the ${noun}.`;
  if (/^\p{White_Space}+$/u.test(text)) return `Enter the ${noun} itself, not only spaces.`;
  if (/^\p{White_Space}|\p{White_Space}$/u.test(text)) return `Remove the spaces at the start or end: the node takes the ${noun} exactly as written and refuses them.`;
  if (utf8(text) > max) return `Use at most ${max} bytes. Letters outside ASCII take 2 to 4 bytes each.`;
  return undefined;
}

export function pm2NameProblem(name: string): string | undefined {
  if (!name) return 'Enter the process name exactly as pm2 list shows it, like api or worker-1.';
  return exactTextProblem(name, PM2_TEXT_MAX, 'name');
}

/** Optional: empty means every namespace. */
export function pm2NamespaceProblem(namespace: string): string | undefined {
  return namespace ? exactTextProblem(namespace, PM2_TEXT_MAX, 'namespace') : undefined;
}

/** Go's path.Join(home, "rpc.sock") for a clean absolute home. */
export const pm2SocketPath = (home: string): string => home === '/' ? `/${PM2_SOCKET}` : `${home}/${PM2_SOCKET}`;

/** Go's path.Clean for a rooted path. */
const cleanPath = (path: string): string => {
  const out: string[] = [];
  for (const segment of path.split('/')) {
    if (!segment || segment === '.') continue;
    if (segment === '..') out.pop(); else out.push(segment);
  }
  return `/${out.join('/')}`;
};

/** A Unix home: one clean absolute POSIX path, written literally, whose rpc.sock path fits in 107 bytes. */
export function pm2HomeProblem(home: string): string | undefined {
  if (!home) return 'Enter the PM2 home of the daemon to check, like /home/deploy/.pm2.';
  // The socket rule below is the tighter byte limit for every home but "/".
  const text = exactTextProblem(home, Number.POSITIVE_INFINITY, 'path');
  if (text) return text;
  if (home.includes('\\')) return 'Use forward slashes: a Unix path has no backslashes. For Windows nodes, choose the Windows named pipe instead.';
  if (/[~$]/u.test(home)) return 'Write the full path without ~ or $VARIABLES, like /home/deploy/.pm2. The node never expands them and refuses both characters.';
  if (!home.startsWith('/')) return 'Use an absolute path starting with /, like /home/deploy/.pm2.';
  if (cleanPath(home) !== home) return 'Remove the trailing slash and any empty, “.” or “..” segments, like /home/deploy/.pm2.';
  if (utf8(pm2SocketPath(home)) > PM2_SOCKET_MAX) return `Too long: the node opens ${PM2_SOCKET} inside the home, and a Unix socket path holds at most ${PM2_SOCKET_MAX} bytes, so the home can have at most ${PM2_SOCKET_MAX - PM2_SOCKET.length - 1}.`;
  return undefined;
}

type Obj = Record<string, unknown>;
const clone = (value: unknown): Obj => value && typeof value === 'object' && !Array.isArray(value) ? structuredClone(value) as Obj : {};
const KNOWN = new Set(['transport', 'home', 'namespace']);

export interface Pm2Built { value: PM2MonitorOptions; target: string }

/**
 * Builds `pm2` on top of the stored object, so key order and keys from a newer node survive. The node refuses an empty
 * `namespace` and any `home` with the Windows pipe, so both are omitted rather than sent empty. A connection this
 * dashboard doesn't know goes back only as the stored monitor already has it: choosing a known one instead drops the
 * keys that came with the unknown one, and an unknown one typed or imported over anything else is a field error.
 */
export function buildPm2(stored: PM2MonitorOptions | undefined, d: Pm2Draft, errors: Partial<Record<Pm2Field, string>>): Pm2Built {
  const o = clone(stored);
  const storedTransport = str(o.transport);
  const nameProblem = pm2NameProblem(d.target);
  if (nameProblem) errors.pm2Target = nameProblem;

  // Keys from a newer node's connection belong to it, and the node refuses keys it doesn't know.
  if (pm2KnownTransport(d.transport) && !pm2KnownTransport(storedTransport)) {
    for (const key of Object.keys(o)) if (!KNOWN.has(key)) delete o[key];
  }
  o.transport = d.transport;
  if (d.transport === 'unix') {
    const homeProblem = pm2HomeProblem(d.home);
    if (homeProblem) errors.pm2Home = homeProblem;
    o.home = d.home;
  } else if (d.transport === 'windows-pipe') delete o.home;
  else {
    if (d.transport !== storedTransport) errors.pm2Transport = `Choose the Unix socket or the Windows named pipe. This dashboard doesn’t know the connection “${d.transport}”, and it keeps one it doesn’t know only on a monitor already stored with it.`;
    // A newer node's connection keeps its stored home, like its other keys: the form shows no home with it, and an
    // import changes only the settings the form shows.
  }

  const namespaceProblem = pm2NamespaceProblem(d.namespace);
  if (namespaceProblem) errors.pm2Namespace = namespaceProblem;
  if (d.namespace) o.namespace = d.namespace; else delete o.namespace;
  return { value: o as unknown as PM2MonitorOptions, target: d.target };
}

/**
 * The node compares `pm2` through Go's JSON encoding: `transport` is always encoded, an empty `home` or `namespace` is
 * absent. Keys from a newer node compare as stored.
 */
export function pm2Canonical(value: unknown): string {
  const o = value && typeof value === 'object' && !Array.isArray(value) ? value as Obj : {};
  const out: Obj = { transport: str(o.transport) };
  for (const key of Object.keys(o).sort()) {
    const inner = o[key];
    if (key === 'transport' || inner === undefined || inner === null) continue;
    if ((key === 'home' || key === 'namespace') && inner === '') continue;
    out[key] = inner;
  }
  return JSON.stringify(out);
}

/**
 * A copy for masked previews: the home path is replaced, and unknown keys (from a newer node) have the text of
 * credential-named ones hidden. The process name and namespace are identifiers and stay; so does the connection.
 */
export function pm2Masked<T>(o: T): T {
  if (!o || typeof o !== 'object' || Array.isArray(o)) return o;
  const copy = structuredClone(o) as unknown as Obj;
  for (const key of Object.keys(copy)) {
    if (key === 'home') { if (copy.home) copy.home = MASK; }
    else if (!KNOWN.has(key)) copy[key] = maskSecretNamed(copy[key], secretNamed(key));
  }
  return copy as unknown as T;
}

export interface Pm2Fact { label: string; value: ReactNode }

const RULES: Pm2Fact[] = [
  { label: 'Up when', value: 'Every matching process is online' },
  {
    label: 'Down when',
    value: <>Any matching process is stopped, stopping, launching, errored or waiting to restart, or reports <span className="mono">one-launch-status</span>; or the daemon lists no process with that name in the chosen namespace (in any namespace when none is set)</>,
  },
  {
    label: 'Pending when',
    value: `The node gets no definite answer: no daemon at that connection; a daemon run by another account than Canary’s, or on Windows a pipe whose present owner or permissions don’t fit; no permission to open the socket; a connection its system doesn’t use, or a system other than Linux and Windows (macOS and other Unix systems always stay pending); a daemon that isn’t PM2 ${PM2_VERSION}; an answer it can’t read; a state it doesn’t know; check workers that are busy or fail; or no answer within the timeout. A definite down on one process outweighs an unknown one. Pending is never inverted and never recovers an outage.`,
  },
  { label: 'Latency', value: <span className="muted">Not measured; shown as a dash</span> },
];

/** Plain-language facts. Viewers receive no `pm2` object; the process name is the target and stays visible. */
export function pm2Facts(m: Monitor, viewer: boolean, reveal: boolean): Pm2Fact[] {
  const o = m.pm2;
  const read = (namespace: ReactNode): Pm2Fact => ({
    label: 'Read from',
    value: <>Each assigned node’s own PM2 daemon, never another machine’s, as the account Canary runs under: every process named exactly <span className="mono wrap">{m.target}</span>{namespace}. Read-only; nothing is started, stopped or restarted.</>,
  });
  if (viewer || !o) return [read(null), ...RULES, { label: 'Connection and namespace', value: <span className="muted">Hidden from viewers</span> }];
  const transport = str(o.transport);
  return [
    read(o.namespace ? <> in namespace <span className="mono wrap">{o.namespace}</span></> : ' in any namespace'),
    {
      label: 'Connection',
      value: transport === 'unix'
        ? <>Unix socket <span className="mono">{PM2_SOCKET}</span> in the PM2 home {o.home ? (reveal ? <span className="mono wrap">{o.home}</span> : <span className="muted">(set · hidden)</span>) : <span className="warn-text">(missing)</span>} · Linux nodes, asking only a daemon whose effective user ID is Canary’s own</>
        : transport === 'windows-pipe'
        ? 'The PM2 daemon’s fixed local named pipe · Windows nodes, asking only a daemon running as Canary’s own account'
        : <>“{transport}” <span className="muted">(not known to this dashboard)</span></>,
    },
    ...RULES,
  ];
}
