import type { NodeInvitation, RegisteredNode } from '@uptime-canary/shared';
import { parseIPv4, parseIPv6 } from './tailscale';

/**
 * The Add node wizard without rendering: the node ID rule, the credentials a new node gets from this browser, and the
 * deployment text for Docker run, Docker Compose, a Portainer stack and a Linux VM. Every value that reaches a command or
 * file is quoted for that format, or refused when the format can't hold it. Private values appear only in the blocks
 * marked `secret`, and only when asked to (`reveal`); otherwise each shows as the same fixed mask. Nothing here stores,
 * sends or logs anything, and nothing contacts the node-connection address: it only appears in the node's settings.
 * A new node this dashboard reaches at an https:// address serves TLS itself (CANARY_TLS_MODE=auto); a node that's already
 * running keeps the TLS setting it has. Certificate fingerprints are public and appear unmasked.
 */

/** The node image of the deploy/ templates (deploy/portainer-node.yml), at the tag this dashboard reports. */
export const NODE_IMAGE = 'ghcr.io/narehood/uptime-canary-node';
/** The source repository, for building a native node. No native release download exists. */
export const SOURCE_REPOSITORY = 'https://github.com/Narehood/Uptime-Canary.git';
/** Stands in for every private value in a preview: the same width whatever the value, so its length isn't shown either. */
export const MASK = '••••••••••••••••';

/** Go `identifier` (validation.go): a letter or digit, then letters, digits and . _ : -, at most 128; not a reserved name. */
export const NODE_ID_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$/u;
const RESERVED_IDS = ['constructor', 'prototype', '__proto__'];

/** The node ID the wizard writes into the node's settings, or binds an invitation to. The dashboard's server checks again. */
export function nodeIdProblem(id: string, taken: { nodes?: readonly RegisteredNode[]; invitations?: readonly NodeInvitation[] } = {}): string | undefined {
  if (!id) return 'Enter the node’s ID.';
  if (id.length > 128 || !NODE_ID_PATTERN.test(id)) return 'Use up to 128 letters, digits, dots, dashes, underscores or colons, starting with a letter or digit.';
  if (RESERVED_IDS.includes(id)) return 'That ID is reserved. Choose another one.';
  const listed = taken.nodes?.find(node => node.nodeId === id);
  if (listed) return `Already on this dashboard as “${listed.label}”.`;
  if (taken.invitations?.some(invitation => invitation.nodeId === id && (invitation.state === 'waiting' || invitation.state === 'connected'))) {
    return 'An invitation for this ID is already waiting. Cancel it under “Waiting to connect” first, or use another ID.';
  }
  return undefined;
}

/**
 * A new node's administrator or cluster token, made in this browser: 32 random bytes as base64url (43 characters), which
 * meets the node's `tokenValue` rule (24 to 256 printable characters, at least 8 different ones).
 */
export function newSecret(random: (bytes: Uint8Array) => Uint8Array = bytes => crypto.getRandomValues(bytes)): string {
  for (let attempt = 0; attempt < 8; attempt++) {
    const bytes = random(new Uint8Array(32));
    const text = btoa(String.fromCharCode(...bytes)).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/u, '');
    if (text.length === 43 && new Set(text).size >= 8) return text;
  }
  throw new Error('This browser couldn’t make random credentials.');
}

/** A new node's two bootstrap tokens. The node refuses equal ones, so they always differ. */
export function newNodeSecrets(random?: (bytes: Uint8Array) => Uint8Array): { admin: string; cluster: string } {
  const admin = newSecret(random);
  let cluster = newSecret(random);
  for (let attempt = 0; cluster === admin && attempt < 8; attempt++) cluster = newSecret(random);
  if (cluster === admin) throw new Error('This browser couldn’t make random credentials.');
  return { admin, cluster };
}

/**
 * A new node's administrator and cluster tokens. `nodeId` is set once they're used for a node: shown, copied, saved,
 * sent with its verification or written for its invitation. From then on they belong to that node ID only.
 */
export interface NodeSecrets { admin: string; cluster: string; nodeId?: string }

/** The pair, when it may serve `nodeId`: not used yet, or used for exactly that ID. Another node never gets it. */
export function secretsFor(secrets: NodeSecrets | null | undefined, nodeId: string): NodeSecrets | null {
  return secrets && (secrets.nodeId === undefined || secrets.nodeId === nodeId) ? secrets : null;
}

/** The Add node dialog's tokens: the pair in use, and the last pair used for another node ID. */
export interface DialogSecrets { current: NodeSecrets | null; earlier: NodeSecrets | null }

/**
 * The dialog's tokens once the node ID is `nodeId`, and used for it when `use` is set. A pair that wasn't used yet follows
 * the ID being typed, so typing makes nothing. A pair used for another ID stays with that ID: the earlier pair of
 * `nodeId` comes back, or `make` gives a new one, and the pair just left becomes the earlier one. Nothing else is kept.
 * The same state comes back when nothing changes. `make` may throw; nothing has changed then.
 */
export function secretsForNode(state: DialogSecrets, nodeId: string, use = false, make: () => NodeSecrets = newNodeSecrets): DialogSecrets {
  let { current, earlier } = state;
  if (earlier?.nodeId === nodeId) [current, earlier] = [earlier, current?.nodeId === undefined ? null : current];
  else if (!secretsFor(current, nodeId)) [current, earlier] = [make(), current?.nodeId === undefined ? earlier : current];
  if (use && current && current.nodeId === undefined) current = { admin: current.admin, cluster: current.cluster, nodeId };
  return current === state.current && earlier === state.earlier ? state : { current, earlier };
}

// ----- quoting. Each format gets its own rule; a value none of them can hold safely (a line break or another control
// character) is refused before any text is made.

const CONTROL = /[\x00-\x1f\x7f]/u;
function plain(value: string): string {
  if (CONTROL.test(value)) throw new Error('Values can’t contain line breaks or other control characters.');
  return value;
}

/** POSIX shell: one single-quoted word. A quote inside becomes '\'' . */
export const shellQuote = (value: string): string => `'${plain(value).replace(/'/gu, `'\\''`)}'`;

/** A Compose file's YAML: single-quoted, with '' for a quote and $$ for $, so Compose doesn't interpolate the value. */
export const composeQuote = (value: string): string => `'${plain(value).replace(/'/gu, '\'\'').replace(/\$/gu, () => '$$')}'`;

/** A systemd EnvironmentFile value: double-quoted; systemd reads \ " ` and $ literally after a backslash. */
export const systemdQuote = (value: string): string => `"${plain(value).replace(/[\\"`$]/gu, match => `\\${match}`)}"`;

/** Docker's --env-file reads a value literally to the end of its line; leading blanks would be dropped, so they're refused. */
function envFileValue(value: string): string {
  if (/^\s/u.test(plain(value))) throw new Error('Values can’t start with a space.');
  return value;
}

/** Tokens and addresses in Compose .env files and Portainer variables: characters no reader treats specially. */
const LITERAL = /^[A-Za-z0-9._~:+/=@%,-]+$/u;
function literal(value: string): string {
  if (!LITERAL.test(value)) throw new Error('This value can’t be written into a settings file safely.');
  return value;
}

/**
 * A short public code for a node ID: 32-bit FNV-1a over the ID's exact, case-sensitive UTF-8 bytes, as 8 lowercase hex
 * digits. It only keeps a new node's names apart from another node's on the same host. It isn't secret, proves nothing
 * about a node, and two different IDs can still share one, however unlikely.
 */
export function nodeIdCode(nodeId: string): string {
  let hash = 0x811c9dc5;
  for (const byte of new TextEncoder().encode(nodeId)) hash = Math.imul(hash ^ byte, 0x01000193);
  return (hash >>> 0).toString(16).padStart(8, '0');
}

/** The readable part's room: 63 characters, less "canary-", the dash and the 8-digit code. */
const READABLE_LENGTH = 63 - 'canary-'.length - 1 - 8;

/**
 * Every name a new node's steps make: its container, volumes, Compose project, Portainer stack, systemd unit, state
 * directory and file names. That's "canary-", the node ID lowercased with other characters as dashes (shortened to fit 63
 * characters), a dash and nodeIdCode(nodeId). Go compares node IDs exactly, so "Office" and "office", "a:b" and "a-b", or
 * two long IDs with the same start are different nodes, while their readable parts are the same; the code from the
 * complete ID keeps their names apart. It's made from the ID alone, never a token or invitation, so a node's names are
 * the same on every render, in every browser and after restarts.
 */
export function resourceName(nodeId: string): string {
  const readable = nodeId.toLowerCase().replace(/[^a-z0-9_-]+/gu, '-').slice(0, READABLE_LENGTH).replace(/[-_]+$/u, '');
  return `canary-${readable}-${nodeIdCode(nodeId)}`;
}

// ----- where a new node listens when this dashboard's server reaches it

export interface DirectTarget {
  /** The address the dashboard's server will use; the verification step checks it. */
  url: string;
  /** Local publish address chosen independently of url: an IP literal (IPv6 in brackets). */
  host: string;
  /** The native node's CANARY_HOST: the chosen local IP without brackets. */
  listen: string;
  port: number;
  /** A wildcard local binding: 0.0.0.0 or ::. It is never inferred from the remote node URL. */
  allInterfaces: boolean;
}

/**
 * A new node's address as the dashboard's server will reach it, on the node's own port. https://, which this wizard
 * suggests, has the node serve TLS 1.3 itself (CANARY_TLS_MODE=auto) with a certificate it makes on first start; the
 * administrator then enters that certificate's fingerprint from the node's log before the dashboard's server contacts it.
 * http:// sets the node up without encryption, as before, for a network that encrypts traffic itself.
 */
export function directTarget(input: string, bindAddress = '0.0.0.0'): DirectTarget | string {
  let url: URL;
  try { url = new URL(input.trim()); } catch { return 'Enter the full address, like https://100.79.12.4:3100.'; }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return 'Use an https:// address, like https://100.79.12.4:3100: the new node then encrypts its connections itself.';
  if (url.username || url.password || url.search || url.hash || url.pathname !== '/') return 'Use only the scheme, host and port, like https://100.79.12.4:3100.';
  const port = url.port ? Number(url.port) : url.protocol === 'https:' ? 443 : 80;
  const binding = localBinding(bindAddress);
  if (typeof binding === 'string') return binding;
  return { url: url.origin, ...binding, port };
}

/** A local interface IP, never a connection URL or DNS name. Blank selects the explicit default wildcard. */
export function localBinding(input: string): Pick<DirectTarget, 'host' | 'listen' | 'allInterfaces'> | string {
  const value = input.trim() || '0.0.0.0';
  const ipv4 = parseIPv4(value);
  if (ipv4) return { host: value, listen: value, allInterfaces: ipv4.every(byte => byte === 0) };
  const bare = value.startsWith('[') && value.endsWith(']') ? value.slice(1, -1) : value;
  const ipv6 = parseIPv6(bare);
  if (ipv6) return { host: `[${bare}]`, listen: bare, allInterfaces: ipv6.every(byte => byte === 0) };
  return 'Enter an IP address that belongs to the node host, or 0.0.0.0 for all IPv4 interfaces. Do not enter a URL, host name or port.';
}

/** The new node serves HTTPS itself: its settings select CANARY_TLS_MODE=auto. */
export const directTls = (target: DirectTarget): boolean => target.url.startsWith('https:');

/** Uptime Canary's own port: a node's default CANARY_PORT, and the port in this wizard's example addresses. */
export const CANARY_PORT = 3100;

/**
 * Linux lets a process listen below port 1024 only with CAP_NET_BIND_SERVICE (net.ipv4.ip_unprivileged_port_start is
 * 1024 by default). A VM's service runs as its own unprivileged dynamic user, so its unit grants that capability for such
 * a port, and only then. Docker publishes the port itself, so its steps don't change.
 */
export const lowPort = (port: number): boolean => port >= 1 && port < 1024;

/**
 * An address without a port means its scheme's (443 for https://, 80 for http://), and the steps use that port: this says
 * so, with the same address on Uptime Canary's own port, as the examples write it. Null when the address names another
 * port.
 */
export function schemePortNote(target: DirectTarget, platform: Platform): string | null {
  const url = new URL(target.url);
  if (url.port) return null;
  url.port = String(CANARY_PORT);
  const used = platform === 'vm'
    ? `the node listens on ${target.port}${lowPort(target.port) ? ', which its service unit allows with CAP_NET_BIND_SERVICE, as it does for any port below 1024' : ''}`
    : `the commands publish port ${target.port}`;
  return `Without a port, ${url.protocol}// means port ${target.port}, so ${used}. To use Uptime Canary’s own port instead, enter ${url.origin}.`;
}

/** The line a node started with CANARY_TLS_MODE=auto logs (cmd/canary/main.go), followed by its certificate's fingerprint. */
export const TLS_LOG_LINE = 'Node TLS certificate fingerprint:';
const PIN = /^[0-9a-f]{64}$/u;

// ----- deployment text

export type Platform = 'docker' | 'compose' | 'portainer' | 'vm';
export const PLATFORMS: { value: Platform; label: string }[] = [
  { value: 'docker', label: 'Docker run' },
  { value: 'compose', label: 'Docker Compose' },
  { value: 'portainer', label: 'Portainer stack' },
  { value: 'vm', label: 'Linux VM' },
];

/** Published Docker traffic can bypass INPUT/UFW; native listeners use the host's normal firewall path. */
export function bindingAccessNote(platform: Platform): string {
  return platform === 'vm'
    ? 'Use the host’s firewall so that only the dashboard’s server reaches that port.'
    : 'Docker-published traffic can bypass host INPUT/UFW rules. An explicit bind address limits exposed interfaces; use Docker-aware filtering to limit who can connect, such as DOCKER-USER with Docker’s iptables backend.';
}

export interface NodeSetup {
  platform: Platform;
  nodeId: string;
  /** The node's own name (CANARY_NODE_NAME); the wizard uses the name on this dashboard. */
  name: string;
  imageTag: string;
  /** A new node's bootstrap tokens, never ones used for another node ID. Null: a node that's already running, which keeps its own. */
  secrets: NodeSecrets | null;
  /** This dashboard's server reaches the node here. */
  direct?: DirectTarget;
  /**
   * The node reaches this dashboard: its node-connection address and the one-time invitation token, and the fingerprint of
   * the certificate that address serves when the dashboard's own listener terminates TLS (CANARY_DASHBOARD_TLS_FINGERPRINT).
   * That pins the dashboard's certificate on the node; it isn't the node's own connection fingerprint.
   */
  outbound?: { connectionUrl: string; token: string; tlsFingerprint?: string };
}

export interface GuideBlock {
  key: string;
  title: string;
  kind: 'file' | 'command' | 'note';
  /** The suggested file name, for Download. */
  file?: string;
  /** Holds private values: masked unless revealed; Copy and Download always carry the real text. */
  secret: boolean;
  text: string;
  /** Portainer basic editor: copy names and raw values separately; masked values follow reveal just like text. */
  variables?: GuideVariable[];
}

export interface GuideVariable { name: string; value: string; secret: boolean }

export const imageReference = (tag: string): string => `${NODE_IMAGE}:${tag}`;
/** The commit a `sha-<full commit>` tag names, or null for `latest` and other tags. */
export const tagCommit = (tag: string): string | null => /^sha-([0-9a-f]{40})$/u.exec(tag)?.[1] ?? null;

const TMPFS = '/tmp:rw,exec,nosuid,nodev,size=64m';

/** The ordered blocks for one platform. `reveal` false gives the preview: every private value is the mask. */
export function setupGuide(setup: NodeSetup, reveal: boolean): GuideBlock[] {
  if (!NODE_ID_PATTERN.test(setup.nodeId) || setup.nodeId.length > 128) throw new Error('Enter a valid node ID first.');
  if (!/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/u.test(setup.imageTag)) throw new Error('This dashboard reported an image tag this page can’t use.');
  if (!setup.direct === !setup.outbound) throw new Error('Choose how the node and this dashboard connect.');
  if (!setup.secrets && !setup.outbound) throw new Error('A node that’s already running is added with its own address and credential.');
  if (setup.secrets && !secretsFor(setup.secrets, setup.nodeId)) throw new Error('These tokens were already used for another node ID, so they aren’t written for this one.');
  const hide = (value: string): string => reveal ? value : MASK;
  const name = plain(setup.name.trim()) || setup.nodeId;
  const res = resourceName(setup.nodeId);
  const image = imageReference(setup.imageTag);
  const out = setup.outbound;
  if (out) literal(out.token);
  // api.ts reads it as 64 lowercase hexadecimal characters; anything else never reaches a node's settings.
  if (out?.tlsFingerprint && !PIN.test(out.tlsFingerprint)) throw new Error('This dashboard reported a certificate fingerprint this page can’t use.');
  const blocks = setup.secrets
    ? newNodeBlocks(setup, { name, res, image, hide })
    : existingNodeBlocks(setup, { res, image, hide });
  if (out) {
    // The node saves the address and the pin with its connection credential and refuses to start if either changes
    // (node_connector.go prepareConnector), so both stay in its settings.
    const kept = out.tlsFingerprint ? ' Keep CANARY_DASHBOARD_URL and CANARY_DASHBOARD_TLS_FINGERPRINT as they are: the node saved both with that credential, and refuses to start if either one changes.' : '';
    blocks.push({
      key: 'after', kind: 'note', secret: false, title: 'After you add it here',
      text: `Remove CANARY_DASHBOARD_ENROLL_TOKEN from the node's settings; it works only once. The node keeps its own connection credential in its identity ${setup.platform === 'vm' ? 'directory' : 'volume'} and reconnects with that, so keep it.${kept}`,
    });
  }
  return blocks;
}

interface Parts { name?: string; res: string; image: string; hide: (value: string) => string }

/** The connection settings in order, as KEY and value: the address, the dashboard's certificate pin if any, then the token. */
function connectionSettings(out: NonNullable<NodeSetup['outbound']>, token: string): [string, string][] {
  return [
    ['CANARY_DASHBOARD_URL', out.connectionUrl],
    ...(out.tlsFingerprint ? [['CANARY_DASHBOARD_TLS_FINGERPRINT', out.tlsFingerprint] as [string, string]] : []),
    ['CANARY_DASHBOARD_ENROLL_TOKEN', token],
  ];
}

/**
 * A new node that serves HTTPS itself: where its certificate comes from and lives, how to read its fingerprint from the
 * node's own log, and what that fingerprint isn't. `log` says where the log is, `place` what holds its identity.
 */
function tlsSteps(log: string, place: 'volume' | 'directory', info?: string): string {
  // `info`: the command that prints the certificate again later (the node's tls-info, which only reads it). Only where it
  // runs as the node's own user, as the identity files require: in its container, never as root on a VM.
  const later = info ? ` Later, ${info} on its host prints it again, with the certificate and its expiry date, and changes nothing.` : '';
  return `It serves HTTPS itself (CANARY_TLS_MODE=auto), with a certificate it makes when it first starts, valid for ten years and kept in its identity ${place}. Its log (${log}) then shows a line that starts with “${TLS_LOG_LINE}”. Copy the 64 characters after it from that log yourself, where you see the node’s own output, such as your own terminal on its host, rather than from a message or page someone else could change.${later} The fingerprint isn’t secret, and it isn’t the “Node connection fingerprint” of nodes that connect to this dashboard. Keep the identity ${place}: with its data but without it, the node refuses to start rather than make another certificate.`;
}

function newNodeBlocks(setup: NodeSetup, { name = setup.nodeId, res, image, hide }: Parts): GuideBlock[] {
  const secrets = setup.secrets!;
  const out = setup.outbound;
  const direct = setup.direct;
  const id = setup.nodeId;
  const purpose = out ? 'its administrator and cluster tokens and its one-time invitation' : 'its administrator and cluster tokens';
  // A node this dashboard reaches at an https:// address serves TLS itself; a node that connects out keeps its default.
  const tls = direct ? directTls(direct) : false;
  switch (setup.platform) {
    case 'docker': {
      const env = [
        `# Uptime Canary node ${id}: private settings for docker run --env-file.`,
        `# Keep this file private (chmod 600). It holds ${purpose}.`,
        `CANARY_NODE_ID=${id}`,
        `CANARY_NODE_NAME=${envFileValue(name)}`,
        `CANARY_ADMIN_TOKEN=${hide(secrets.admin)}`,
        `CANARY_CLUSTER_TOKEN=${hide(secrets.cluster)}`,
        'CANARY_PEERS=[]',
        ...(tls ? ['CANARY_TLS_MODE=auto'] : []),
        ...(out ? connectionSettings(out, hide(out.token)).map(([key, value]) => `${key}=${value}`) : []),
      ];
      return [
        { key: 'env', kind: 'file', file: `${res}.env`, secret: true, title: `Save the private settings as ${res}.env`, text: `${env.join('\n')}\n` },
        { key: 'run', kind: 'command', secret: false, title: 'Pull the image, create the two volumes and start the node', text: dockerRun(res, image, direct) },
        startedNote(`docker logs ${res}`, Boolean(out), undefined, undefined, tls ? { log: `docker logs ${res}`, place: 'volume', info: `docker exec ${res} canary tls-info` } : undefined),
      ];
    }
    case 'compose':
    case 'portainer': {
      const portainer = setup.platform === 'portainer';
      const variableFields: GuideVariable[] = [
        { name: 'CANARY_ADMIN_TOKEN', value: hide(literal(secrets.admin)), secret: true },
        { name: 'CANARY_CLUSTER_TOKEN', value: hide(literal(secrets.cluster)), secret: true },
        ...(out ? [{ name: 'CANARY_DASHBOARD_ENROLL_TOKEN', value: hide(out.token), secret: true }] : []),
      ];
      const variables = variableFields.map(variable => `${variable.name}=${variable.value}`);
      const yaml = composeFile({ project: portainer ? null : res, nodeId: id, name, image, direct, tls, connection: out ?? null });
      if (portainer) {
        const started = tls ? ` ${tlsSteps('the container’s log in Portainer', 'volume')} Then choose Next and enter the fingerprint.` : '';
        return [
          { key: 'stack', kind: 'note', secret: false, title: 'Add the stack', text: `In Portainer, open Stacks and choose Add stack. Name it ${res}, choose Web editor and paste the YAML below. Keep the stack name: Portainer names the two volumes after it.` },
          { key: 'yaml', kind: 'file', file: `${res}.yml`, secret: false, title: 'Stack YAML (no private values)', text: yaml },
          { key: 'variables', kind: 'file', file: `${res}.env`, secret: true, title: 'Environment variables', text: `${variables.join('\n')}\n`, variables: variableFields },
          { key: 'deploy', kind: 'note', secret: false, title: 'Deploy', text: `Under Environment variables, add one entry for each row: copy its Name into Portainer's name field and its Value into the value field. Copy value includes only the value, without NAME= or a trailing line break. To import all entries together, use Advanced mode with Copy .env, or Load variables from .env file with the download. If this host has run ${image} before, pull it again first (under Images in Portainer), so the node doesn't start an older copy of that tag. Then choose Deploy the stack. Portainer keeps these variables with the stack, so anyone who can edit the stack in Portainer can read them.${out ? ' The node’s log in Portainer shows its connection fingerprint.' : ''}${started}` },
        ];
      }
      // One private file name throughout: the download's. Every Compose command names it with --env-file, because
      // Compose reads only a file called .env by itself. The image is pulled with docker pull: with pull_policy missing,
      // Compose refreshes only `latest` and skips any other tag the host has (`docker compose pull` too), however old.
      // One && list, so a failed chmod or pull never goes on to start the node, and logs follow only a started one.
      const settings = `${res}.env`;
      const compose = `docker compose --env-file ${settings}`;
      const up = [...startCommands([`chmod 600 ${settings}`, `docker pull ${image}`, `${compose} up -d`]), `${compose} logs node`, ''].join('\n');
      return [
        { key: 'yaml', kind: 'file', file: 'compose.yaml', secret: false, title: `Save compose.yaml in a directory of its own, such as ${res} (no private values)`, text: yaml },
        { key: 'env', kind: 'file', file: settings, secret: true, title: `Save the private settings as ${settings} next to it`, text: `# Private settings for compose.yaml. Keep this file private (chmod 600); pass it to every Compose command with --env-file.\n${variables.join('\n')}\n` },
        { key: 'up', kind: 'command', secret: false, title: 'Pull the image and start the node', text: up },
        startedNote(null, Boolean(out), undefined, `Later Compose commands for it need --env-file ${settings} too.`, tls ? { log: `${compose} logs node`, place: 'volume', info: `${compose} exec node canary tls-info` } : undefined),
      ];
    }
    case 'vm': {
      const host = direct ? direct.listen : '127.0.0.1';
      const port = direct ? direct.port : CANARY_PORT;
      const env = [
        `# Uptime Canary node ${id}: private settings. Install as /etc/uptime-canary/${res}.env, mode 0600.`,
        `CANARY_NODE_ID=${systemdQuote(id)}`,
        `CANARY_NODE_NAME=${systemdQuote(name)}`,
        `CANARY_ADMIN_TOKEN=${systemdQuote(hide(secrets.admin))}`,
        `CANARY_CLUSTER_TOKEN=${systemdQuote(hide(secrets.cluster))}`,
        'CANARY_PEERS="[]"',
        `CANARY_HOST=${systemdQuote(host)}`,
        `CANARY_PORT="${port}"`,
        ...(tls ? ['CANARY_TLS_MODE="auto"'] : []),
        ...(out ? connectionSettings(out, hide(out.token)).map(([key, value]) => `${key}=${systemdQuote(value)}`) : []),
      ];
      return [
        nativePrerequisite(setup.imageTag),
        { key: 'build', kind: 'command', secret: false, title: 'Build the node from the matching source', text: buildCommands(setup.imageTag) },
        { key: 'env', kind: 'file', file: `${res}.env`, secret: true, title: `Save the private settings as ${res}.env`, text: `${env.join('\n')}\n` },
        { key: 'unit', kind: 'file', file: `${res}.service`, secret: false, title: `Save the service as ${res}.service (no private values)`, text: systemdUnit(id, res, port) },
        {
          key: 'install', kind: 'command', secret: false, title: 'Install and start it',
          text: `sudo install -d -m 0700 /etc/uptime-canary\nsudo install -m 0600 ${res}.env /etc/uptime-canary/${res}.env\nrm ${res}.env\nsudo install -m 0644 ${res}.service /etc/systemd/system/${res}.service\nsudo systemctl daemon-reload\nsudo systemctl enable --now ${res}\njournalctl -u ${res} -f\n`,
        },
        startedNote(null, Boolean(out), `/var/lib/${res}`, undefined, tls ? { log: `journalctl -u ${res}`, place: 'directory' } : undefined),
      ];
    }
  }
}

function existingNodeBlocks(setup: NodeSetup, { res, image, hide }: Parts): GuideBlock[] {
  const out = setup.outbound!;
  const id = setup.nodeId;
  // A running node keeps the names it was deployed with. Only the file names below come from resourceName().
  const names = { docker: 'container and volume names', compose: 'Compose project and volume names', portainer: 'stack and volume names', vm: 'service name and state directory' }[setup.platform];
  const vm = setup.platform === 'vm';
  const pin = out.tlsFingerprint;
  // It keeps the TLS setting it runs with: these steps add only the connection settings, never CANARY_TLS_MODE.
  // Before any setting changes: the node refuses to start with these settings unless CANARY_ADMIN_TOKEN is a current
  // administrator token there and no other node-connection address (or certificate pin) is saved in its identity
  // (node_connector.go, prepareConnector; main.go exits on "Invalid startup configuration"). Taking the settings out
  // again undoes it.
  const added = pin ? 'CANARY_DASHBOARD_URL, CANARY_DASHBOARD_TLS_FINGERPRINT and CANARY_DASHBOARD_ENROLL_TOKEN' : 'both CANARY_DASHBOARD_URL and CANARY_DASHBOARD_ENROLL_TOKEN';
  const keep: GuideBlock = {
    key: 'keep', kind: 'note', secret: false, title: 'Before you start',
    text: `This keeps the node's ID, data, accounts and tokens. It needs a node version that can connect out: first update it to ${vm ? 'a build of the matching source, keeping its state directory' : `${image}, keeping its volumes; pull that tag again if the host has it already`}. Its CANARY_NODE_ID must stay ${id}. Its CANARY_ADMIN_TOKEN must be a current administrator token on it, such as the token of an administrator account there: the node checks it when it starts with these settings, and uses it to set up this dashboard's access when it pairs. If that token was changed or removed since, or is empty, or the node connected through another node-connection address before, the node refuses to start (its log says “Invalid startup configuration”) and doesn't monitor until you undo the change: remove ${added} again and start it as before, with its data, identity, names and tokens as they were. Keep its ${names} as they are. The new-node steps on this page now end those names in a short code from the node ID, so they can differ from a node deployed earlier; nothing here renames, moves or deletes them.`,
  };
  const refused = '“Invalid startup configuration” if it refused to start: then undo the change as step 1 says';
  const settings = connectionSettings(out, hide(out.token));
  switch (setup.platform) {
    case 'docker':
      return [
        keep,
        { key: 'env', kind: 'file', file: `${res}-connect.env`, secret: true, title: `Save the connection settings as ${res}-connect.env`, text: settings.map(([key, value]) => `${key}=${value}\n`).join('') },
        { key: 'recreate', kind: 'note', secret: false, title: 'Recreate the container with them', text: `Put ${pin ? 'all three' : 'both'} lines in the node's env file, replacing any it has already, or add --env-file ${res}-connect.env to its docker run command if it has neither. Then stop and remove the container and start it again with the same name, volumes and other settings. Its log shows the connection fingerprint, or ${refused}.` },
      ];
    case 'compose':
    case 'portainer': {
      const portainer = setup.platform === 'portainer';
      // The token comes from a variable; the address and pin aren't private, so they're written into the YAML.
      const yaml = settings.map(([key, value]) => `      ${key}: ${key === 'CANARY_DASHBOARD_ENROLL_TOKEN' ? '${CANARY_DASHBOARD_ENROLL_TOKEN:-}' : composeQuote(value)}\n`).join('');
      const listed = settings.map(([key, value]) => `- ${key}=${key === 'CANARY_DASHBOARD_ENROLL_TOKEN' ? '${CANARY_DASHBOARD_ENROLL_TOKEN:-}' : value}`);
      return [
        keep,
        {
          key: 'yaml', kind: 'file', secret: false, title: `Add to ${portainer ? 'the stack YAML' : 'compose.yaml'}, under the node service’s environment, unless it has these settings`,
          text: yaml,
        },
        {
          key: 'list', kind: 'note', secret: false, title: 'If its environment is a list',
          text: `The lines above are for an environment written as KEY: value, indented like its other entries. In a list of - KEY=value entries, add ${listed.slice(0, -1).join(', ')}${listed.length > 2 ? ',' : ''} and ${listed[listed.length - 1]} instead.`,
        },
        { key: 'env', kind: 'file', file: `${res}-connect.env`, secret: true, title: portainer ? 'Add this environment variable to the stack, or replace its earlier value' : 'Put this line in its .env file, or the file its Compose commands pass with --env-file, replacing an earlier one', text: `CANARY_DASHBOARD_ENROLL_TOKEN=${hide(out.token)}\n`, ...(portainer ? { variables: [{ name: 'CANARY_DASHBOARD_ENROLL_TOKEN', value: hide(out.token), secret: true }] } : {}) },
        portainer
          ? { key: 'update', kind: 'note', secret: false, title: 'Update the stack', text: `Choose Update the stack, with re-pull of the image if you changed its tag or this host may have an older copy of it. The node’s log in Portainer shows its connection fingerprint, or ${refused}.` }
          : { key: 'up', kind: 'command', secret: false, title: 'Apply it, adding the --env-file option its Compose commands use, if any', text: 'docker compose up -d\ndocker compose logs node\n' },
      ];
    }
    case 'vm':
      return [
        keep,
        { key: 'env', kind: 'file', file: `${res}-connect.env`, secret: true, title: 'Add these lines to the node’s EnvironmentFile, replacing any earlier ones', text: settings.map(([key, value]) => `${key}=${systemdQuote(value)}\n`).join('') },
        { key: 'restart', kind: 'note', secret: false, title: 'Restart it', text: `Restart the node’s service, for example with sudo systemctl restart and its unit name. Its journal shows the connection fingerprint, or ${refused}.` },
      ];
  }
}

/** `tls`: a new node this dashboard reaches over HTTPS, with where its log is, what holds its identity, and how to read the certificate again. */
function startedNote(logs: string | null, outbound: boolean, state?: string, extra?: string, tls?: { log: string; place: 'volume' | 'directory'; info?: string }): GuideBlock {
  const where = logs ? ` Its log (${logs}) shows how it started.` : '';
  const after = `${state ? ` Keep ${state}: it holds the node's data and identity.` : ''}${extra ? ` ${extra}` : ''}`;
  return {
    key: 'started', kind: 'note', secret: false, title: 'When it runs',
    text: outbound
      ? `It connects to this dashboard on its own and waits for you to confirm it here.${where} Its log also prints the connection fingerprint you compare then.${after}`
      : tls
        ? `${tlsSteps(tls.log, tls.place, tls.info)} Then choose Next and enter the fingerprint: the dashboard's server checks the node at its address, accepting only that certificate, with the administrator token made here, and you confirm adding it.${after}`
        : `Then choose Next: the dashboard's server checks the node at its address with the administrator token made here, and you confirm adding it.${where}${after}`,
  };
}

function dockerRun(res: string, image: string, direct?: DirectTarget): string {
  // An IPv6 host is in brackets, which bash and zsh read as a glob; quoted, the shell passes it to Docker as written.
  const published = direct ? `${direct.host}:${direct.port}:3100` : '';
  const publish = direct ? [`  -p ${direct.host.startsWith('[') ? shellQuote(published) : published} \\`] : [];
  // One && list with docker run at its end: a step that fails stops it before anything starts.
  return [
    ...startCommands([
      `chmod 600 ${res}.env`,
      // docker run starts a copy of the tag the host already has, however old; pulling first gets the one named here.
      `docker pull ${image}`,
      `docker volume create ${res}-data`,
      `docker volume create ${res}-identity`,
    ]),
    `docker run -d --name ${res} \\`,
    '  --init --restart unless-stopped --read-only \\',
    `  --tmpfs ${TMPFS} \\`,
    '  --cap-drop ALL --cap-add NET_RAW \\',
    '  --security-opt no-new-privileges:true \\',
    '  --log-driver json-file --log-opt max-size=10m --log-opt max-file=3 \\',
    `  --env-file ${res}.env \\`,
    '  -e CANARY_HOST=0.0.0.0 -e CANARY_PORT=3100 \\',
    '  -e CANARY_DB=/data/canary.db -e CANARY_IDENTITY_DIRECTORY=/identity \\',
    `  -v ${res}-data:/data -v ${res}-identity:/identity \\`,
    ...publish,
    `  ${image}`,
    '',
  ].join('\n');
}

// Steps that must succeed before the next line runs, as one POSIX && list: a line ending in && makes the shell read on,
// and each command after it runs only if everything before it succeeded. So a failed chmod, pull or volume create stops
// the pasted block there, before an older copy of the tag can start, and leaves an interactive shell open (set -e or exit
// would close it).
function startCommands(steps: string[]): string[] {
  return steps.map(step => `${step} &&`);
}

function composeFile({ project, nodeId, name, image, direct, tls, connection }: {
  project: string | null; nodeId: string; name: string; image: string; direct?: DirectTarget; tls: boolean; connection: NodeSetup['outbound'] | null;
}): string {
  const lines = [
    `# Uptime Canary node ${nodeId}, from this dashboard's Add node wizard. It pulls the published image; nothing is built.`,
    `# Keep the ${project ? 'project' : 'stack'} name and both volumes: they hold the node's data and its identity.`,
    ...(project ? [`name: ${project}`] : []),
    'services:',
    '  node:',
    `    image: ${image}`,
    '    pull_policy: missing',
    '    init: true',
    '    restart: unless-stopped',
    '    read_only: true',
    '    tmpfs:',
    `      - ${TMPFS}`,
    '    environment:',
    `      CANARY_NODE_ID: ${composeQuote(nodeId)}`,
    `      CANARY_NODE_NAME: ${composeQuote(name)}`,
    '      CANARY_ADMIN_TOKEN: ${CANARY_ADMIN_TOKEN:?Set CANARY_ADMIN_TOKEN}',
    '      CANARY_CLUSTER_TOKEN: ${CANARY_CLUSTER_TOKEN:?Set CANARY_CLUSTER_TOKEN}',
    '      CANARY_PEERS: \'[]\'',
    '      CANARY_HOST: 0.0.0.0',
    '      CANARY_PORT: "3100"',
    '      CANARY_DB: /data/canary.db',
    '      CANARY_IDENTITY_DIRECTORY: /identity',
    ...(tls ? ['      CANARY_TLS_MODE: auto'] : []),
    ...(connection ? [
      `      CANARY_DASHBOARD_URL: ${composeQuote(connection.connectionUrl)}`,
      ...(connection.tlsFingerprint ? [`      CANARY_DASHBOARD_TLS_FINGERPRINT: ${composeQuote(connection.tlsFingerprint)}`] : []),
      '      CANARY_DASHBOARD_ENROLL_TOKEN: ${CANARY_DASHBOARD_ENROLL_TOKEN:-}',
    ] : []),
    ...(direct ? ['    ports:', `      - "${direct.host}:${direct.port}:3100"`] : []),
    '    volumes:',
    '      - canary-data:/data',
    '      - canary-identity:/identity',
    '    cap_drop:',
    '      - ALL',
    '    cap_add:',
    '      - NET_RAW',
    '    security_opt:',
    '      - no-new-privileges:true',
    '    logging:',
    '      driver: json-file',
    '      options:',
    '        max-size: "10m"',
    '        max-file: "3"',
    'volumes:',
    '  canary-data:',
    '  canary-identity:',
    '',
  ];
  return lines.join('\n');
}

function nativePrerequisite(tag: string): GuideBlock {
  return {
    key: 'native', kind: 'note', secret: false, title: 'No native download',
    text: `Uptime Canary doesn't publish native release binaries, so there's nothing to download here. The simplest way on a VM is Docker Engine, installed the way your Linux distribution documents it, with the Docker run or Docker Compose tab. For a native node, build it yourself from the source that matches this dashboard's image tag (${tag}), with the Go version that go.mod names, as below.`,
  };
}

function buildCommands(tag: string): string {
  const commit = tagCommit(tag);
  return [
    `git clone ${SOURCE_REPOSITORY}`,
    'cd Uptime-Canary',
    commit ? `git checkout ${commit}` : `# Check out the commit that image tag ${tag} was built from before building.`,
    'CGO_ENABLED=0 go build -trimpath -o uptime-canary ./apps/node-go/cmd/canary',
    'sudo install -m 0755 uptime-canary /usr/local/bin/uptime-canary',
    '',
  ].join('\n');
}

function systemdUnit(nodeId: string, res: string, port: number): string {
  // The service keeps its dynamic user and gains only CAP_NET_BIND_SERVICE, and that only for a port below 1024.
  const capabilities = lowPort(port) ? 'CAP_NET_RAW CAP_NET_BIND_SERVICE' : 'CAP_NET_RAW';
  return [
    '[Unit]',
    `Description=Uptime Canary node ${nodeId}`,
    'Wants=network-online.target',
    'After=network-online.target',
    '',
    '[Service]',
    'Type=simple',
    'DynamicUser=yes',
    `StateDirectory=${res}`,
    'StateDirectoryMode=0700',
    `EnvironmentFile=/etc/uptime-canary/${res}.env`,
    `Environment=CANARY_DB=/var/lib/${res}/canary.db`,
    `Environment=CANARY_IDENTITY_DIRECTORY=/var/lib/${res}/identity`,
    'ExecStart=/usr/local/bin/uptime-canary serve',
    'Restart=on-failure',
    'RestartSec=5',
    ...(lowPort(port) ? [`# CANARY_PORT ${port} is below 1024: CAP_NET_BIND_SERVICE lets this service's own unprivileged user listen on it.`] : []),
    `AmbientCapabilities=${capabilities}`,
    `CapabilityBoundingSet=${capabilities}`,
    'NoNewPrivileges=yes',
    'ProtectSystem=strict',
    'ProtectHome=yes',
    'PrivateTmp=yes',
    '',
    '[Install]',
    'WantedBy=multi-user.target',
    '',
  ].join('\n');
}
