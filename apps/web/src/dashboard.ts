import type { NodeInvitation, RegisteredNode, Role } from '@uptime-canary/shared';
import { ApiFailure, checkRegistration, describeFailure, normalizeApiUrl } from './api';
import { elapsed } from './format';
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
  const outbound = node.connection === 'outbound' ? outboundStateInfo(node) : null;
  if (outbound) return outbound;
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
    case 'rotating':
      // An attempt that failed leaves its reason in `error`; only then does an administrator need to look.
      return {
        tone: 'pending', label: 'Updating node access', out: true, attention: Boolean(node.error), readable: false,
        text: 'The dashboard’s server is replacing the credentials it uses on this node, and doesn’t read it until the node confirms. It saved the new ones first and keeps retrying, so a retry can’t leave extra access on the node. The node keeps monitoring and alerting on its own.',
      };
  }
}

/**
 * A node that connects to this dashboard itself: the server never dials it and keeps no address for it, so these states
 * name its connection instead. Enrolling and rotating read the same as for any node. Last connected is transport
 * activity, never a sign that monitoring is healthy.
 */
function outboundStateInfo(node: RegisteredNode): NodeStateInfo | null {
  switch (node.state) {
    case 'answering':
      return { tone: 'up', label: 'Answering', text: 'Connected. The dashboard’s server reads it over the node’s own connection.', out: false, attention: false, readable: true };
    case 'starting':
      return { tone: 'pending', label: 'Starting', text: 'It hasn’t connected since the dashboard started. Sign-in, accounts and other nodes work meanwhile.', out: false, attention: false, readable: true };
    case 'not-answering':
      return {
        tone: 'warn', label: 'Not answering', out: true, attention: true, readable: true,
        text: 'It isn’t connected to this dashboard right now, so the dashboard’s server can’t read it. A node’s own checks and alerts don’t depend on this dashboard and may still be running.',
      };
    case 'credential-refused':
      return {
        tone: 'serious', label: 'Credential refused', out: true, attention: true, readable: false,
        text: 'It connects, but refuses this dashboard’s credential, which may have been revoked on the node. The dashboard stopped reading it.',
      };
    case 'different-node':
      return {
        tone: 'serious', label: 'Different node connected', out: true, attention: true, readable: false,
        text: `A node other than ${node.nodeId || 'the one that was added'} answered over this connection. The dashboard stopped reading it so two nodes’ data don’t mix.`,
      };
    case 'removing':
      return {
        tone: 'pending', label: 'Removal unfinished', out: true, attention: true, readable: false,
        text: 'The dashboard no longer reads it. It accepts the node’s connection only so the node can confirm that this dashboard’s access there was revoked, which happens when the node connects again.',
      };
    default:
      return null;
  }
}

/**
 * The states in which the dashboard's server is changing the node's access and refuses monitoring, rechecks, edits
 * and another rotation (Go `dashboardOperationPending`). Only Retry (administrators) and Remove act on them.
 */
export const PENDING_STATES: readonly RegisteredNode['state'][] = ['enrolling', 'removing', 'rotating'];
export const pendingOperation = (state: RegisteredNode['state']): boolean => PENDING_STATES.includes(state);

export interface NodeActions {
  check: boolean;
  /** Administrators: resume the unfinished operation; its button label. */
  retry: string | null;
  edit: boolean;
  /** Edit can change the address too; the bundled node's address belongs to the deployment. */
  move: boolean;
  rotate: boolean;
  remove: boolean;
}

const RETRY_LABELS: Partial<Record<RegisteredNode['state'], string>> = { enrolling: 'Retry enrollment', removing: 'Retry removal', rotating: 'Retry access update' };

/**
 * What a node card offers, matching what the dashboard's server accepts. Viewers and editors only read and recheck.
 * Rotation is left out while a different node answers at the address, which the server refuses before changing
 * anything; editing the address is the way back there.
 */
export function nodeActions(node: RegisteredNode, admin: boolean): NodeActions {
  const pending = pendingOperation(node.state);
  const manage = admin && !pending;
  return {
    check: !pending,
    retry: admin && pending ? RETRY_LABELS[node.state] ?? 'Retry' : null,
    edit: manage,
    // An outbound node has no address here: it connects to the dashboard, so there's nothing to move.
    move: manage && !node.local && node.connection !== 'outbound' && node.url !== undefined,
    rotate: manage && node.state !== 'different-node',
    remove: admin && !node.local && node.state !== 'removing',
  };
}

/**
 * Administrators, on a pending access update (the bundled node's too): Recover access finishes that saved update through
 * Retry's route, optionally with a current administrator credential from the node for when it stopped accepting the one
 * the update uses. It never starts another rotation, and Rotate access stays off while anything is pending.
 */
export const canRecoverAccess = (node: RegisteredNode, admin: boolean): boolean => admin && node.state === 'rotating';

/**
 * The PATCH body for the edit form, or null when nothing changed. The address is sent only when it was edited to name
 * another origin than the saved one; a rename alone never contacts the node. Problems are the form's field errors.
 */
export function editChanges(node: RegisteredNode, label: string, url: string, move: boolean): { input: { label: string; url?: string } | null; moving: boolean; errors: { label?: string; url?: string } } {
  const errors: { label?: string; url?: string } = {};
  const labelError = labelProblem(label);
  if (labelError) errors.label = labelError;
  let moving = false;
  let origin: string | undefined;
  if (move && url !== node.url) {
    const urlError = nodeAddressProblem(url);
    if (urlError) errors.url = urlError;
    else {
      origin = normalizeApiUrl(url);
      moving = origin !== node.url;
    }
  }
  if (errors.label || errors.url) return { input: null, moving, errors };
  const text = label.trim();
  if (text === node.label && !moving) return { input: null, moving, errors };
  return { input: moving && origin ? { label: text, url: origin } : { label: text }, moving, errors };
}

/** The rotation dialog's optional credential: empty is fine; otherwise the server's account-token rule. */
export function recoveryProblem(token: string): string | undefined {
  if (!token) return undefined;
  if (/[^\x21-\x7e]/u.test(token)) return 'Credentials contain only printable characters, without spaces.';
  if (token.length < 24 || token.length > 256 || new Set(token).size < 8) return 'Administrator credentials are 24 to 256 varied characters, like a node’s account tokens.';
  return undefined;
}

/**
 * A refused or unanswered registry change, for the edit, rotation and recovery dialogs. For an edit or a first rotation,
 * every refusal the dashboard's server sends comes before it saves anything, so it changed nothing; recovering a pending
 * update describes that update instead (`recoveryFailure`). Only a missing or unreadable answer leaves the outcome open
 * (`unknown`); `reload` asks the list to show what the server has now.
 */
export interface ChangeFailure { title: string; text: string; unknown: boolean; reload: boolean }

const NOTHING = 'Nothing was changed.';
const sentence = (text: string): string => /[.!?]$/u.test(text) ? text : `${text}.`;

/** The node's accounts for this dashboard aren't the three the saved credentials belong to (409, before anything is saved). */
const delegationRefused = (error: unknown): boolean => error instanceof ApiFailure && error.status === 409 && /delegation/iu.test(error.detail ?? '');

function commonFailure(error: unknown, verb: string): ChangeFailure | null {
  const text = describeFailure(error, { preferDetail: true });
  if (!(error instanceof ApiFailure)) return { title: `Not ${verb}`, text: /[.!?]$/u.test(text) ? `${text} ${NOTHING}` : `${text}. ${NOTHING}`, unknown: false, reload: false };
  const detail = error.detail ?? '';
  if (error.status === 0 || error.status === 200) {
    return { title: 'The result isn’t known yet', text: `${describeFailure(error)} The dashboard’s server may have finished it. The node list now shows what it has; check it before trying again.`, unknown: true, reload: true };
  }
  if (error.status === 401) return { title: 'Your sign-in to this dashboard ended', text: `Sign in again, then try again. ${NOTHING}`, unknown: false, reload: false };
  if (error.status === 403) return { title: 'Only administrators change nodes', text: `${text} ${NOTHING}`, unknown: false, reload: false };
  if (error.status === 404) return { title: 'This node is no longer on this dashboard', text: `An administrator may have removed it. ${NOTHING}`, unknown: false, reload: true };
  if (error.status === 409 && /permissions/iu.test(detail)) return { title: 'Your dashboard permissions changed', text: `${text} ${NOTHING}`, unknown: false, reload: false };
  if (error.status === 409 && /pending|already running/iu.test(detail)) {
    return { title: 'Another change to this node isn’t finished', text: `${text} Retry or finish it from the node list first. ${NOTHING}`, unknown: false, reload: true };
  }
  if (error.status === 409 && /changed/iu.test(detail)) {
    return { title: 'This node changed in the meantime', text: `Someone else, or the dashboard’s server, changed it while this was being checked. The list now shows the latest; check it, then try again. ${NOTHING}`, unknown: false, reload: true };
  }
  if (error.status === 502 && /redirect/iu.test(detail)) {
    return { title: 'The address answered with a redirect', text: `The dashboard doesn’t follow redirects, so an address can’t send it somewhere else. ${NOTHING}`, unknown: false, reload: false };
  }
  return null;
}

export function editFailure(error: unknown, node: RegisteredNode, moving: boolean): ChangeFailure {
  const common = commonFailure(error, 'saved');
  if (common) return common;
  const failure = error as ApiFailure;
  const detail = failure.detail ?? '';
  const text = describeFailure(error, { preferDetail: true });
  const done = (title: string, body: string, reload = false): ChangeFailure => ({ title, text: `${body} ${NOTHING}`, unknown: false, reload });
  if (failure.status === 409 && /deployment/iu.test(detail)) return done('This address is set by the deployment', `${text} You can still rename it here.`, true);
  if (failure.status === 409 && /already registered/iu.test(detail)) return done('Another entry already uses this address', text);
  if (moving && failure.status === 502 && /different node/iu.test(detail)) {
    return done('A different node answers at this address', `It isn’t ${node.nodeId}, so the entry stays where it was. To read that node too, add it as its own node.`);
  }
  if (moving && failure.status === 502 && /refused|role|identity/iu.test(detail)) {
    return done('This address doesn’t accept this dashboard’s access', `${text} The node there must accept all three of the credentials this dashboard already uses on ${node.nodeId}, so the entry stays where it was.`);
  }
  if (moving && (failure.status === 502 || failure.status === 504)) {
    return done('The dashboard’s server couldn’t check the new address', `${text} Your browser wasn’t involved, so it doesn’t matter whether this computer can reach it.`);
  }
  return done('Not saved', text);
}

/**
 * A refused or unanswered first rotation request. The dashboard's server checks the credential it rotates with and the
 * node's three accounts for this dashboard before it saves an update, and an update it saved answers 202, so every
 * refusal here changed nothing. Only a missing or unreadable answer leaves the outcome open.
 */
export function rotationFailure(error: unknown, node: RegisteredNode, recovery: boolean): ChangeFailure {
  // Ahead of the shared 409 wording, which would read "delegation changed" as a concurrent edit of the entry.
  if (delegationRefused(error)) {
    return {
      title: 'This dashboard’s accounts on the node changed',
      text: `${describeFailure(error, { preferDetail: true })} Rotating replaces the credentials of the three accounts this dashboard already has on ${node.nodeId}; it doesn’t recreate or repair them. ${NOTHING}`,
      unknown: false, reload: false,
    };
  }
  const common = commonFailure(error, 'rotated');
  if (common) {
    return common.unknown
      ? { ...common, text: `${describeFailure(error)} The dashboard’s server may have saved the new credentials. The node list now shows what it has: an unfinished update shows as “Updating node access”, with Retry and Recover access.` }
      : common;
  }
  const failure = error as ApiFailure;
  const detail = failure.detail ?? '';
  const text = describeFailure(error, { preferDetail: true });
  const done = (title: string, body: string, reload = false): ChangeFailure => ({ title, text: `${body} ${NOTHING}`, unknown: false, reload });
  if (failure.status === 409 && /upgrade/iu.test(detail)) return done('This node can’t rotate dashboard access yet', text);
  if (recovery && failure.status === 502 && /refused/iu.test(detail)) {
    return done('The node refused that administrator credential', `It has to be a current administrator credential on ${node.nodeId} itself. This page has already forgotten it; paste it again to retry.`);
  }
  if (recovery && failure.status === 502 && /role/iu.test(detail)) {
    return done('That credential isn’t an administrator’s', `${text} Use an administrator credential from ${node.nodeId}. This page has already forgotten it.`);
  }
  // Without a recovery credential, the server checked the administrator credential this dashboard already holds there.
  if (failure.status === 502 && /refused/iu.test(detail)) {
    return done('The node refuses this dashboard’s credential', `${text} It may have been revoked on ${node.nodeId}. To rotate anyway, paste a current administrator credential from ${node.nodeId}.`);
  }
  if (failure.status === 502 && /delegation|role/iu.test(detail)) return done('This dashboard’s accounts on the node couldn’t be confirmed', text);
  if (failure.status === 502 && /different node/iu.test(detail)) {
    return done('A different node answers at this address', `${text} If ${node.nodeId} moved, edit the entry’s address first.`);
  }
  if (failure.status === 502 || failure.status === 504) {
    return done('The dashboard’s server couldn’t reach this node', `${text} Rotating needs the node to answer; try again when it does.`);
  }
  return done('Not rotated', text);
}

/** How a pending access update behaves, whatever one attempt to finish it did. */
const STILL_PENDING = 'The update saved earlier stays pending until the node confirms it, and the dashboard’s server keeps retrying it.';

/**
 * A refused or unanswered attempt to finish a pending access update (Recover access). Unlike a first rotation, the update
 * already exists: its new credentials were saved before, and the dashboard's server keeps retrying them. So these never
 * say nothing changed. With a recovery credential, the server checks it on the node before keeping it with the saved
 * update, and once it kept it answers 200 or 202 instead of a refusal; a refusal therefore means it didn't keep it. A
 * plain retry did contact the node and records why it failed on the entry. A missing or unreadable answer may have kept
 * the credential and finished the update.
 */
export function recoveryFailure(error: unknown, node: RegisteredNode, recovery: boolean): ChangeFailure {
  const text = describeFailure(error, { preferDetail: true });
  const forgotten = recovery ? ' This page has already forgotten the credential.' : '';
  const pending = (title: string, body: string, reload = false): ChangeFailure => ({
    title, text: `${body} ${STILL_PENDING}${recovery ? ' The dashboard’s server didn’t keep that credential, and this page has already forgotten it.' : ''}`, unknown: false, reload,
  });
  if (!(error instanceof ApiFailure)) return { title: 'Not sent', text: `${sentence(text)} ${STILL_PENDING}${forgotten}`, unknown: false, reload: false };
  const detail = error.detail ?? '';
  if (error.status === 0 || error.status === 200) {
    return {
      title: 'The result isn’t known yet',
      text: `${describeFailure(error)} The dashboard’s server may have ${recovery ? 'kept that credential, and may have ' : ''}finished the update. The node list now shows what it has: while it shows “Updating node access”, the update is still pending.${forgotten}`,
      unknown: true, reload: true,
    };
  }
  if (error.status === 401) return pending('Your sign-in to this dashboard ended', 'Sign in again, then try again.');
  if (error.status === 403) return pending('Only administrators change nodes', text);
  if (error.status === 404) return { title: 'This node is no longer on this dashboard', text: `An administrator may have removed it, so there’s no update left to finish.${forgotten}`, unknown: false, reload: true };
  if (error.status === 409 && /permissions/iu.test(detail)) return pending('Your dashboard permissions changed', text);
  if (error.status === 409 && /idle pending|already running/iu.test(detail)) {
    return {
      title: 'This update is busy or already finished',
      text: `${text} The dashboard’s server may be retrying it on its own right now, or it may have just finished. The node list now shows the latest; if it still shows “Updating node access”, try again in a moment.${forgotten}`,
      unknown: false, reload: true,
    };
  }
  if (error.status === 409 && /delegation/iu.test(detail)) {
    return pending('This dashboard’s accounts on the node changed', `${text} Recovering finishes the update for the three accounts this dashboard already has on ${node.nodeId}; it doesn’t recreate or repair them.`);
  }
  if (error.status === 409 && /changed/iu.test(detail)) {
    return { title: 'This node changed in the meantime', text: `Someone else, or the dashboard’s server, changed it while this was being checked. The list now shows the latest; check it, then try again.${forgotten}`, unknown: false, reload: true };
  }
  if (error.status === 409) {
    return { title: 'Not finished', text: `${text} The node list now shows the latest: if the update already finished, there’s nothing left to do.${forgotten}`, unknown: false, reload: true };
  }
  // Ahead of the credential tests: the server's redirect refusal also says "refused".
  if (error.status === 502 && /redirect/iu.test(detail)) {
    return pending('The address answered with a redirect', 'The dashboard doesn’t follow redirects, so an address can’t send it somewhere else.');
  }
  if (recovery && error.status === 502 && /refused/iu.test(detail)) {
    return pending('The node refused that administrator credential', `It has to be a current administrator credential on ${node.nodeId} itself. Paste one to try again.`);
  }
  if (recovery && error.status === 502 && /unexpected role/iu.test(detail)) {
    return pending('That credential isn’t an administrator’s', `${text} Use an administrator credential from ${node.nodeId}.`);
  }
  // A plain retry reached the node, which didn't take the saved update with the credential it carries.
  if (!recovery && error.status === 502 && /not accepted|refused/iu.test(detail)) {
    return pending('The node didn’t accept the saved update', `${text} If it no longer accepts the credential this update uses, paste a current administrator credential from ${node.nodeId} and try again.`);
  }
  if (error.status === 502 && /delegation|role/iu.test(detail)) return pending('This dashboard’s accounts on the node couldn’t be confirmed', text);
  if (error.status === 502 && /different node/iu.test(detail)) return pending('A different node answers at this address', text);
  if (error.status === 502 || error.status === 504) {
    return pending(recovery ? 'The dashboard’s server couldn’t check that credential' : 'The dashboard’s server couldn’t finish the update', `${text} Finishing needs the node to answer.`);
  }
  return pending('Not finished', text);
}

/**
 * What an answered Recover access means. 200 with a settled entry: the node confirmed the saved credentials. A pending
 * entry (202 when a recovery credential was kept): the update still waits for the node. Null (204): the entry is gone,
 * because a removal that started meanwhile finished.
 */
export function recoveryOutcome(node: RegisteredNode, result: RegisteredNode | null, recovery: boolean): { finished: boolean; text: string } {
  if (result === null) {
    return { finished: true, text: `${node.label} is no longer on this dashboard: a removal that started meanwhile finished, and the node confirmed this dashboard’s access was revoked.` };
  }
  if (pendingOperation(result.state)) {
    const reason = result.error ? ` ${sentence(result.error)}` : '';
    const shown = nodeStateInfo(result).label;
    return {
      finished: false,
      text: recovery && result.state === 'rotating'
        ? `The dashboard’s server kept that administrator credential with the saved update for ${result.label}, but the node hasn’t confirmed the update yet.${reason} It still shows as “${shown}”; the server keeps retrying with that credential, and Retry tries again now.`
        : `Retried ${result.label}, but it isn’t confirmed yet.${reason} It still shows as “${shown}”; the dashboard’s server keeps retrying.`,
    };
  }
  return { finished: true, text: `Updated this dashboard’s access to ${result.label}. The node confirmed the saved credentials; the old ones no longer work there.` };
}

/**
 * Control plane, the selected node's monitoring failed: the registry entry when an operation is changing its access
 * (the server then refuses to read it, 409), so the page can say so instead of reporting a conflict or an outage.
 */
export function selectedOperation(row: RegisteredNode | undefined, failure: { status: number; detail?: string } | null): RegisteredNode | 'unknown' | null {
  if (row && pendingOperation(row.state)) return row;
  if (failure?.status === 409 && /not complete/iu.test(failure.detail ?? '')) return 'unknown';
  return null;
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
    unfinished: count(['enrolling', 'removing', 'rotating']),
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
  { role: 'admin', label: 'Admin', can: 'Also adds, edits and removes nodes, rotates the access this dashboard has on them, and manages this dashboard’s accounts: creates them, changes roles and tokens, resets passwords and two-factor, and deletes them.' },
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

// ----- invitations of nodes that connect to this dashboard (administrators only). They aren't registry entries: a node
// joins the list only when an administrator confirms its first connection.

export interface InvitationInfo { tone: Tone; label: string; text: string; /** Still open: it can connect or be confirmed. */ open: boolean }

/**
 * After an invitation ends unconfirmed (expired or cancelled): a node already deployed with it keeps the tokens it
 * first started with in its data, so deploying it again as a new node, with new tokens, would stop it from starting.
 */
export const REINVITE_DEPLOYED = 'If the node is already deployed, make the next invitation with “A node that’s already running” and the same node ID, then replace only CANARY_DASHBOARD_ENROLL_TOKEN in its settings. Don’t deploy it again as a new node: its data keeps its first tokens, so new ones would stop it from starting.';

/**
 * Why a node may not have connected while its invitation waits. The node prints its connection fingerprint only when
 * it starts with CANARY_DASHBOARD_URL, so a missing line has more than one cause; none of them is singled out.
 */
export function connectionWaitingHint(image: string, connectionUrl: string): string {
  return `No “Node connection fingerprint” line in the node’s log? Then it isn’t connecting: its image or build may predate node connections (for Docker, pull ${image} again and recreate it), its settings may lack CANARY_DASHBOARD_URL or CANARY_DASHBOARD_ENROLL_TOKEN, or it refused to start (its log says “Invalid startup configuration”). With the line, “node connection unavailable” in its log means it can’t finish connecting: check that it reaches ${connectionUrl} and trusts its certificate.`;
}

/** The dashboard's server answered that the invitation doesn't exist (any more): it was cancelled or cleared. */
export const invitationGone = (failure: unknown): boolean => failure instanceof ApiFailure && failure.status === 404;

/** An invitation as the dashboard's server reports it; `now` is on the server's clock. Its token is never part of this. */
export function invitationInfo(invitation: NodeInvitation, now: number): InvitationInfo {
  switch (invitation.state) {
    case 'waiting': {
      const left = invitation.expiresAt - now;
      return { tone: 'pending', label: 'Waiting for first connection', open: true, text: left > 0 ? `It works once, and ends in ${elapsed(left)}.` : 'Its time is up; the dashboard’s server marks it expired.' };
    }
    case 'connected':
      return { tone: 'warn', label: 'Connected, waiting for confirmation', open: true, text: 'The node proved it holds this invitation. Compare its ID and fingerprint, then add it. Until then the dashboard reads nothing from it and sets up no access there.' };
    case 'expired':
      return { tone: 'serious', label: 'Expired', open: false, text: `Nothing was added. Cancel it, then make another invitation if you still want this node. ${REINVITE_DEPLOYED}` };
    case 'confirmed':
      return { tone: 'up', label: 'Added', open: false, text: 'Confirmed by an administrator; the node is on the node list now.' };
  }
}

/**
 * Whether the confirmation can be offered: the node connected, reported its identity, and the ID it proved is the one the
 * invitation expects. The server refuses any other ID; a mismatch here means the page shouldn't offer to add it either.
 */
export function pairingProblem(invitation: NodeInvitation): string | null {
  if (invitation.state !== 'connected') return 'The node hasn’t connected with this invitation yet.';
  if (!invitation.node) return 'The dashboard’s server hasn’t reported the node’s identity yet.';
  if (invitation.node.id !== invitation.nodeId) return `The node reported ${invitation.node.id}, not ${invitation.nodeId}. Don’t add it; cancel this invitation.`;
  return null;
}

const NOT_CREATED = 'Nothing was created.';
const NOT_ADDED = 'Nothing was added.';

/**
 * A refused or unanswered invitation request. Creating returns the only copy of the token, so an unknown outcome says
 * that an invitation made meanwhile can't show it again. Cancelling never takes a registered node off the list, and
 * confirming either adds the node or changes nothing, except when the answer is lost.
 */
export function invitationFailure(error: unknown, action: 'create' | 'cancel' | 'confirm'): ChangeFailure {
  const text = describeFailure(error, { preferDetail: true });
  const nothing = action === 'create' ? NOT_CREATED : action === 'confirm' ? NOT_ADDED : 'Nothing was cancelled.';
  const done = (title: string, body: string, reload = false): ChangeFailure => ({ title, text: `${sentence(body)} ${nothing}`, unknown: false, reload });
  if (!(error instanceof ApiFailure)) return done(action === 'create' ? 'Not created' : action === 'confirm' ? 'Not added' : 'Not cancelled', text);
  const detail = error.detail ?? '';
  if (error.status === 0 || error.status === 200 || (action === 'confirm' && (error.status === 502 || error.status === 504))) {
    const may = action === 'create'
      ? 'The dashboard’s server may have made the invitation. If it’s listed under “Waiting to connect”, its token can’t be shown again: cancel it there and make another.'
      : action === 'confirm'
        ? 'The dashboard’s server may have added the node. The node list now shows what it has: an unfinished enrollment shows there as “Enrollment unfinished”, with Retry.'
        : 'The dashboard’s server may have cancelled it. The list now shows what it has.';
    return { title: 'The result isn’t known yet', text: `${describeFailure(error)} ${may}`, unknown: true, reload: true };
  }
  if (error.status === 401) return done('Your sign-in to this dashboard ended', 'Sign in again, then try again.');
  if (error.status === 403) return done('Only administrators add nodes', text);
  if (error.status === 404) {
    return action === 'create'
      ? done('Node connections aren’t available here', `${text} This dashboard’s server may predate them.`)
      : { title: 'This invitation is no longer on this dashboard', text: 'It may have been cancelled or cleared meanwhile.', unknown: false, reload: true };
  }
  if (error.status === 429) return done('Too many invitations are waiting', `${text} Cancel unused ones under “Waiting to connect”, or wait until they expire.`);
  if (error.status === 503) return done(action === 'confirm' ? 'The dashboard’s server can’t add it right now' : 'Node connections aren’t set up on this dashboard', text);
  if (error.status === 409 && /permissions/iu.test(detail)) return done('Your dashboard permissions changed', text);
  // The 409s below are Go's own words (dashboard_invitations.go). Creating without the dedicated node-connection address
  // and either capacity limit aren't an ID in use.
  if (error.status === 409 && /connection endpoint/iu.test(detail)) return done('Node connections aren’t set up on this dashboard', `${text} Its operator sets the node-connection address in the dashboard’s deployment.`);
  if (error.status === 409 && /capacity/iu.test(detail)) {
    return done('No room for another node', `${text} ${action === 'create' ? 'Cancel unused invitations under “Waiting to connect”, or remove a node.' : 'Remove a node first.'}`);
  }
  if (action === 'cancel' && error.status === 409) {
    return { title: 'This node was already added', text: 'Its first connection was confirmed, so it’s on the node list now. Cancelling an invitation never takes a node off the list; its card does that.', unknown: false, reload: true };
  }
  if (action === 'confirm' && error.status === 409 && /expired/iu.test(detail)) return done('This invitation expired', `Cancel it and make another one. ${REINVITE_DEPLOYED}`, true);
  // "A current connected invitation must be confirmed": it expired, was cancelled or isn't connected any more. Its
  // "confirmed" doesn't mean an administrator already added the node.
  if (action === 'confirm' && error.status === 409 && /must be confirmed/iu.test(detail)) return done('This invitation changed meanwhile', `${text} Its status now shows what the dashboard’s server has.`, true);
  if (action === 'confirm' && error.status === 409 && /no longer authorized/iu.test(detail)) {
    return done('This invitation is no longer valid', `The administrator account that made it changed or was removed. Cancel it and make another one. ${REINVITE_DEPLOYED}`, true);
  }
  if (action === 'confirm' && error.status === 409 && /already|confirmed|registered/iu.test(detail)) {
    return { title: 'Already added', text: `${sentence(text)} The node list shows it.`, unknown: false, reload: true };
  }
  if (action === 'confirm' && error.status === 409) return done('This invitation changed meanwhile', `${text} Its status now shows what the dashboard’s server has.`, true);
  if (action === 'create' && error.status === 409) return done('This node ID is already in use here', `${text} A node or a waiting invitation already has it.`);
  return done(action === 'create' ? 'Not created' : action === 'confirm' ? 'Not added' : 'Not cancelled', text);
}

/** Page titles for the sections a node answers for, used when none is selected. */
export const SECTION_TITLES: Record<string, string> = {
  overview: 'Overview', monitors: 'Monitors', groups: 'Groups', incidents: 'Incidents', notifications: 'Notifications',
  maintenance: 'Maintenance', 'status-pages': 'Status pages', config: 'Configuration',
};
