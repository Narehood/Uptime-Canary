import type { RegisteredNode, Role } from '@uptime-canary/shared';
import { checkRegistration, normalizeApiUrl } from './api';
import { pageOrigin } from './gateway';
import type { Tone } from './model';

/**
 * Control-plane wording and rules that don't render anything: registry states as the dashboard's server reports
 * them, the roles of dashboard accounts, and checks on the Add node form. Everything a node reports stays the
 * node's own report; nothing here infers a state the server didn't send.
 */

export interface NodeStateInfo {
  tone: Tone;
  label: string;
  text: string;
  /** The dashboard's server can't read it right now (hatched, like an out-of-view node). */
  out: boolean;
  /** An administrator should look at it. */
  attention: boolean;
  /** Selecting it can show monitoring data (now, or once it answers). */
  readable: boolean;
}

export function nodeStateInfo(node: RegisteredNode): NodeStateInfo {
  switch (node.state) {
    case 'answering':
      return { tone: 'up', label: 'Answering', text: 'The dashboard’s server reads it.', out: false, attention: false, readable: true };
    case 'starting':
      return { tone: 'pending', label: 'Starting', text: 'It hasn’t answered the dashboard’s server since the dashboard started. Sign-in, accounts and other nodes work meanwhile.', out: false, attention: false, readable: true };
    case 'not-answering':
      return {
        tone: 'warn', label: 'Not answering', out: true, attention: true, readable: true,
        text: `The dashboard’s server can’t reach it right now.${node.local ? ' Check this deployment’s node container.' : ''} A node’s own checks and alerts don’t depend on this dashboard and may still be running.`,
      };
    case 'credential-refused':
      return {
        tone: 'serious', label: 'Credential refused', out: true, attention: true, readable: false,
        text: 'It answers, but refuses this dashboard’s credential, which may have been revoked on the node. The dashboard stopped reading it.',
      };
    case 'different-node':
      return {
        tone: 'serious', label: 'Different node at this address', out: true, attention: true, readable: false,
        text: `This address now answers as another node, not ${node.nodeId || 'the one that was added'}. The dashboard stopped reading it so two nodes’ data don’t mix.`,
      };
    case 'enrolling':
      if (node.local && !shownNodeId(node)) {
        return {
          tone: 'pending', label: 'Being added', out: true, attention: false, readable: false,
          text: 'This deployment’s node is added automatically once it answers. Sign-in, accounts and other nodes work meanwhile.',
        };
      }
      return {
        tone: 'pending', label: 'Enrollment unfinished', out: true, attention: true, readable: false,
        text: 'Adding it hasn’t finished. The dashboard’s server keeps retrying, and it recorded each step first, so a retry doesn’t leave extra access on the node.',
      };
    case 'removing':
      return {
        tone: 'pending', label: 'Removal unfinished', out: true, attention: true, readable: false,
        text: 'The dashboard no longer reads it and keeps asking the node to revoke this dashboard’s access, but the node hasn’t confirmed that yet.',
      };
  }
}

/** The node's own ID, or null while the bundled node hasn't been identified yet (the server's `pending` placeholder). */
export function shownNodeId(node: RegisteredNode): string | null {
  if (!node.nodeId || (node.local && node.state === 'enrolling' && node.nodeId === 'pending')) return null;
  return node.nodeId;
}

export interface RegistrySummary { total: number; answering: number; notAnswering: number; attention: number; starting: number; unfinished: number }

export function registrySummary(nodes: RegisteredNode[]): RegistrySummary {
  const count = (states: RegisteredNode['state'][]): number => nodes.filter(node => states.includes(node.state)).length;
  return {
    total: nodes.length,
    answering: count(['answering']),
    notAnswering: count(['not-answering']),
    attention: nodes.filter(node => nodeStateInfo(node).attention).length,
    starting: count(['starting']),
    unfinished: count(['enrolling', 'removing']),
  };
}

/** The local node first, then by label: the order every list of registered nodes uses. */
export function registryOrder(nodes: RegisteredNode[]): RegisteredNode[] {
  return [...nodes].sort((a, b) => Number(b.local) - Number(a.local) || a.label.localeCompare(b.label) || a.id.localeCompare(b.id));
}

/** What each dashboard role can do. Viewers read through a node delegation that keeps node-side secret redaction. */
export const DASHBOARD_ROLES: { role: Role; label: string; can: string }[] = [
  { role: 'viewer', label: 'Viewer', can: 'Sees the nodes on this dashboard and their monitors, incidents and history. Never sees node addresses, webhook URLs, request credentials or other secrets; changes nothing.' },
  { role: 'editor', label: 'Editor', can: 'Also changes monitors, groups, tags, channels, maintenance and status pages on this dashboard’s nodes. Can’t add nodes or accounts.' },
  { role: 'admin', label: 'Admin', can: 'Also adds and removes nodes and manages this dashboard’s accounts: creates them, changes roles and tokens, resets passwords and two-factor, and deletes them.' },
];

/** Same limits as the node's own label and name checks: 128 bytes, no control characters. */
export function labelProblem(label: string): string | undefined {
  const text = label.trim();
  if (!text) return 'Name it, for example by where it runs.';
  if (new TextEncoder().encode(text).length > 128 || /[\x00-\x1f\x7f]/u.test(text)) return 'Use at most 128 characters, without control characters.';
  return undefined;
}

/**
 * The node's address as the dashboard's server reaches it: an HTTP(S) origin only. The page's own origin is refused
 * here because it is this dashboard, not a node; the server validates again and never follows redirects.
 */
export function nodeAddressProblem(input: string): string | undefined {
  if (!input.trim()) return 'Enter the node’s address.';
  let origin: string;
  try { origin = normalizeApiUrl(input); } catch (error) { return error instanceof Error ? error.message : 'Enter a full address, like http://100.79.12.4:3100.'; }
  if (origin === pageOrigin()) return 'That’s this dashboard’s own address. Enter the node’s address as the dashboard’s server reaches it.';
  return undefined;
}

/** Same rule as the server's: an account-token-shaped credential, 24 to 256 printable characters, at least 8 distinct. */
export function credentialProblem(token: string): string | undefined {
  if (!token) return 'Paste the enrollment credential from the node.';
  if (/[^\x21-\x7e]/u.test(token)) return 'Credentials contain only printable characters, without spaces.';
  if (token.length < 24 || token.length > 256 || new Set(token).size < 8) return 'Enrollment credentials are 24 to 256 varied characters, like a node’s account tokens.';
  return undefined;
}

/**
 * A Push monitor's heartbeat commands as its form shows them; `token` null prints the `<token>` placeholder. Without a
 * registration (direct mode, or no node selected) they use the node's own routes at `baseUrl`. With one, `baseUrl` is
 * this dashboard and both name the registration in the query. The dashboard's server removes it there and passes on
 * only the push token (the `Authorization: Push` header, or the token in the GET path), never a sign-in or cookie.
 */
export function pushCommands(baseUrl: string, token: string | null, allowGet: boolean, registration: string | null): string {
  const shown = token ?? '<token>';
  const selector = registration === null ? '' : `registration=${encodeURIComponent(checkRegistration(registration))}`;
  const post = selector ? `"${baseUrl}/api/push?${selector}"` : `${baseUrl}/api/push`;
  return `curl -X POST ${post} \\
  -H "Authorization: Push ${shown}" \\
  -H "Content-Type: application/json" \\
  -d '{"status":"up","msg":"backup finished","ping":120}'${allowGet ? `

# or, without a header:
curl "${baseUrl}/api/push/${shown}?${selector ? `${selector}&` : ''}status=up&msg=ok"` : ''}`;
}

/** Page titles for the sections a node answers for, used when none is selected. */
export const SECTION_TITLES: Record<string, string> = {
  overview: 'Overview', monitors: 'Monitors', groups: 'Groups', incidents: 'Incidents', notifications: 'Notifications',
  maintenance: 'Maintenance', 'status-pages': 'Status pages', config: 'Configuration',
};
