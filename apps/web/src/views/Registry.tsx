import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import type { NodeVerification, RegisteredNode } from '@uptime-canary/shared';
import { ApiFailure, describeFailure, isAbort, tlsFingerprintValue, type RegisteredNodeEdit } from '../api';
import { useCanary } from '../context';
import {
  canRecoverAccess, certificateRefused, credentialProblem, editChanges, editFailure, httpsAddress, labelProblem, nodeActions, nodeAddressProblem, nodeStateInfo,
  pendingOperation, recoveryFailure, recoveryOutcome, recoveryProblem, registryOrder, registrySummary, rotationFailure, SECTION_TITLES, shownNodeId,
  tlsFingerprintEdit, tlsFingerprintProblem, type ChangeFailure,
} from '../dashboard';
import { ago, elapsed, plural } from '../format';
import { pageOrigin } from '../gateway';
import { useNow, useSummaryFocus } from '../hooks';
import { notify } from '../notices';
import { href, navigate } from '../router';
import { session } from '../session';
import { Banner, Card, EmptyState, Modal, PageHead, Spinner, Tag, Time, nodeStateText, nodeTone } from '../ui/bits';
import { ErrorSummary, SecretField, TextField } from '../ui/forms';
import { EndpointMark, Glyph, Icon } from '../ui/icons';
import { directTls, secretsFor, secretsForNode, TLS_LOG_LINE, type DialogSecrets, type DirectTarget, type Platform } from '../onboarding';
import {
  AddNodeChooser, NewNodeDeploy, OutboundNode, PendingNodesCard, useInvitations, useOnboarding, type AddPlan, type NewNodeDraft, type Taken,
} from './NodeWizard';

/*
 * Control plane: the dashboard's shared node registry. One list for every account; administrators add, retry and
 * remove entries, every role reads and rechecks them. Only the dashboard's server ever contacts a node, so nothing
 * here sends a node address anywhere except to the dashboard itself, when an administrator verifies a new node.
 * The enrollment credential lives only in the Add node form's state and is cleared after each verification attempt,
 * on Back, on cancel and when the dialog closes. Verification tickets live only in memory; unused ones are discarded.
 * Edit and Rotate access keep the registration (its ID, any selection of it and its public links). Rotation's optional
 * recovery credential lives only in its dialog's state and is cleared as soon as it is sent, and when the dialog closes.
 * Recover access finishes a pending access update through Retry's route, never with another rotation; its optional
 * credential is handled the same way.
 * Add node also offers the wizard in NodeWizard.tsx: a new node to deploy, and nodes that connect to this dashboard
 * themselves. The direct form for a running node stays its default and works as before.
 * A direct HTTPS node may have a TLS certificate fingerprint (administrators only, like its address): the dashboard's
 * server then accepts only that certificate there. It's public, never a credential, and never the node-connection
 * fingerprint of a node that connects to this dashboard. Empty means the server's ordinary certificate checks.
 */

const period = (text: string): string => /[.!?]$/u.test(text) ? text : `${text}.`;

export function RegistryView({ home = false }: { home?: boolean }) {
  const { registry, admin, selection, connection } = useCanary();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<RegisteredNode | null>(null);
  const [editing, setEditing] = useState<RegisteredNode | null>(null);
  const [rotation, setRotation] = useState<RegisteredNode | null>(null);
  const [recovering, setRecovering] = useState<RegisteredNode | null>(null);
  const nodes = registryOrder(registry.list ?? []);
  const summary = registrySummary(nodes);
  const outbound = nodes.some(node => node.connection === 'outbound');
  const add = (): void => setAdding(true);
  const addButton = <button type="button" className="btn primary" onClick={add}><Icon name="plus" />Add node</button>;
  return (
    <div className="view">
      <PageHead
        title={home ? 'Overview' : 'Nodes'}
        sub={home
          ? 'The nodes this dashboard reads for you. Open one to see its monitors, incidents and alerts.'
          : 'Monitoring nodes this dashboard reads for every account. You sign in here, not on each node.'}
        actions={admin ? addButton : <Tag icon="lock">Administrators add and remove nodes</Tag>}
      />
      {home ? null : <DashboardPath outbound={outbound} />}
      {!home && admin ? <PendingNodesCard /> : null}
      {registry.error && !registry.list ? (
        <Banner tone="down" title="The node list couldn’t be loaded" actions={<button type="button" className="btn sm" onClick={registry.reload}><Icon name="refresh" />Try again</button>}>
          {describeFailure(registry.error)} Sign-in and accounts keep working.
        </Banner>
      ) : !registry.list ? (
        <Card><Spinner label="Loading nodes…" /></Card>
      ) : !nodes.length ? (
        <EmptyRegistry onAdd={add} />
      ) : (
        <>
          <div className="sumrow">
            {summary.answering ? <span className="chip c-up"><Glyph tone="up" /><b>{summary.answering}</b><span>answering</span></span> : null}
            {summary.notAnswering ? <span className="chip c-warn"><Glyph tone="warn" /><b>{summary.notAnswering}</b><span>not answering</span></span> : null}
            {summary.attention - summary.notAnswering ? <span className="chip c-serious"><Glyph tone="serious" /><b>{summary.attention - summary.notAnswering}</b><span>{summary.attention - summary.notAnswering === 1 ? 'needs' : 'need'} an administrator</span></span> : null}
            {summary.starting ? <span className="chip"><Glyph tone="pending" /><b>{summary.starting}</b><span>starting</span></span> : null}
            <span className="small muted">As seen by this dashboard’s server{registry.error ? '. Couldn’t refresh just now' : ''}.</span>
          </div>
          <div className="nodes-grid">
            {nodes.map(node => <NodeCard key={node.id} node={node} onRemove={setRemoving} onEdit={setEditing} onRotate={setRotation} onRecover={setRecovering} />)}
            {admin ? <button type="button" className="add-tile" onClick={add}><Icon name="plus" /><b>Add node</b><small>A running node, or a new one to deploy</small></button> : null}
          </div>
          <p className="foot-note">
            Monitor counts are each node’s own report. Adding a node here doesn’t change its peers; peer membership stays in each node’s own configuration.
            {admin ? '' : ' Node addresses are shown to administrators only.'}
            {outbound ? ' Last connected is when a node that connects to this dashboard last opened its connection; it says nothing about its checks.' : ''}
          </p>
        </>
      )}
      {!home && selection && !connection.awaiting ? <PeersCard /> : null}
      {adding ? <AddNodeDialog onClose={() => setAdding(false)} /> : null}
      {removing ? <RemoveNodeDialog node={removing} onClose={() => setRemoving(null)} /> : null}
      {editing ? <EditNodeDialog key={editing.id} node={editing} onClose={() => setEditing(null)} /> : null}
      {rotation ? <RotateNodeDialog key={rotation.id} node={rotation} onClose={() => setRotation(null)} /> : null}
      {recovering ? <RecoverAccessDialog key={recovering.id} node={recovering} onClose={() => setRecovering(null)} /> : null}
    </div>
  );
}

function DashboardPath({ outbound }: { outbound: boolean }) {
  const page = pageOrigin();
  return (
    <Card title="How this dashboard reaches nodes">
      <div className="path" role="img" aria-label="Your browser talks only to this dashboard. Its server contacts each node at the private address saved for it.">
        <span className="box"><Icon name="overview" />This browser tab</span>
        <span className="arr" aria-hidden="true">→</span>
        <span className="box ep"><EndpointMark /><span><b>This dashboard</b>{page ? <span className="mono sub">{page}</span> : null}</span></span>
        <span className="arr" aria-hidden="true">→</span>
        <span className="box"><Icon name="nodes" />Each node’s private address</span>
      </div>
      {outbound ? (
        <div className="path" role="img" aria-label="Nodes that connect to this dashboard open their own HTTPS connection to its node-connection address. The dashboard never dials them.">
          <span className="box"><Icon name="nodes" />Nodes that connect out</span>
          <span className="arr" aria-hidden="true">→</span>
          <span className="box ep"><EndpointMark /><span><b>This dashboard</b><span className="sub">its node-connection address</span></span></span>
        </div>
      ) : null}
      <p className="foot-note">
        Only the dashboard’s server contacts nodes, so LAN, NetBird and Tailscale addresses work as long as the server can reach them.
        Your browser talks to this dashboard and nothing else. Nodes keep monitoring and alerting if this dashboard stops.
        {outbound ? ' Nodes marked “Connects to this dashboard” open their own HTTPS connection to it instead, so they need no route from the dashboard.' : ''}
      </p>
    </Card>
  );
}

function EmptyRegistry({ onAdd }: { onAdd: () => void }) {
  const { admin } = useCanary();
  return (
    <Card>
      <EmptyState title="No nodes yet" action={admin ? <button type="button" className="btn primary" onClick={onAdd}><Icon name="plus" />Add node</button> : undefined}>
        {admin ? 'This dashboard is ready. Add a monitoring node to see its monitors, incidents and alerts here.' : 'An administrator hasn’t added any nodes yet. Sign-in and your account work meanwhile.'}
      </EmptyState>
      {admin ? (
        <ol className="howto">
          <li><b>Run a node</b> where it can check your services. It works on its own; this dashboard is optional.</li>
          <li><b>Check the route.</b> This dashboard’s server, not your browser, has to reach the node’s address, or the node has to reach this dashboard.</li>
          <li><b>Have an enrollment credential</b> from the node, such as an admin account token, then choose Add node. Add node also has the steps to deploy a new one.</li>
        </ol>
      ) : null}
    </Card>
  );
}

function NodeCard({ node, onRemove, onEdit, onRotate, onRecover }: {
  node: RegisteredNode; onRemove: (node: RegisteredNode) => void; onEdit: (node: RegisteredNode) => void; onRotate: (node: RegisteredNode) => void;
  onRecover: (node: RegisteredNode) => void;
}) {
  const { admin, selection, select, api, registry, connection, stillCurrent, refresh } = useCanary();
  const now = useNow(5000);
  const [busy, setBusy] = useState<null | 'check' | 'retry'>(null);
  const [error, setError] = useState<string | null>(null);
  const info = nodeStateInfo(node);
  const actions = nodeActions(node, admin);
  const recover = canRecoverAccess(node, admin);
  const viewing = selection?.id === node.id;
  const dashboardNow = now + (connection.issuerSkew ?? 0);
  const unfinished = pendingOperation(node.state);
  const outbound = node.connection === 'outbound';

  const act = async (kind: 'check' | 'retry'): Promise<void> => {
    if (busy) return;
    setBusy(kind); setError(null);
    try {
      const result = kind === 'check' ? await api.checkNode(node.id) : await api.retryNode(node.id);
      if (!stillCurrent()) return;
      registry.reload();
      if (kind === 'retry') {
        // A finished retry makes the selected node readable again: read it now, not after the poll's back-off.
        if (viewing && result && !pendingOperation(result.state)) refresh();
        notify(result === null ? `Removed ${node.label}. The node confirmed this dashboard’s access was revoked.`
          : pendingOperation(result.state) ? `Retried ${node.label}, but it’s still unfinished. ${result.error ? period(result.error) : 'Try again when the node answers.'}`
          : node.state === 'rotating' ? `Updated this dashboard’s access to ${node.label}. The node confirmed the new credentials; the old ones no longer work there.`
          : `${node.label} is ${nodeStateInfo(result).label.toLowerCase()}.`);
      }
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure, { preferDetail: true }));
      // A failed attempt records its reason on the entry.
      if (kind === 'retry') registry.reload();
    } finally {
      if (stillCurrent()) setBusy(null);
    }
  };

  return (
    <article className={`ncard rnode${viewing ? ' ep' : ''}${info.out ? ' out' : ''}${info.tone === 'serious' ? ' attn' : ''}`} aria-labelledby={`r-${node.id}`}>
      <div className="nc-h">
        <div>
          <h3 id={`r-${node.id}`}>{node.label}</h3>
          <span className="mono">{shownNodeId(node) ?? 'node ID not read yet'}</span>
        </div>
        <Glyph tone={info.tone} label={info.label} />
      </div>
      {node.local || viewing || outbound ? (
        <span className="row-wrap">
          {node.local ? <Tag tone="accent">Added automatically</Tag> : null}
          {outbound ? <Tag icon="plug">Connects to this dashboard</Tag> : null}
          {viewing ? <Tag icon="eye">Viewing</Tag> : null}
        </span>
      ) : null}
      <p className="nc-state"><b>{info.label}.</b> {info.text}{node.error ? <> <span className="muted">{period(node.error)}</span></> : null}</p>
      <dl className="kv">
        {node.url ? <><dt>Address</dt><dd className="mono">{node.url}</dd></> : null}
        {/* Administrators only, like the address. The Edit dialog shows all 64 characters. */}
        {node.tlsFingerprint ? <><dt>TLS certificate</dt><dd><span className="mono">{node.tlsFingerprint.slice(0, 12)}…{node.tlsFingerprint.slice(-12)}</span> <span className="muted">· pinned</span></dd></> : null}
        {outbound ? <><dt>Last connected</dt><dd>{node.lastConnectedAt ? <><Time at={node.lastConnectedAt} now={dashboardNow} /> <span className="muted">· {ago(node.lastConnectedAt, dashboardNow)}</span></> : <span className="muted">Not yet</span>}</dd></> : null}
        {node.nodeName && node.nodeName !== node.nodeId && node.nodeName !== node.label ? <><dt>Node name</dt><dd>{node.nodeName}</dd></> : null}
        <dt>Last read</dt><dd>{node.lastReadAt ? <><Time at={node.lastReadAt} now={dashboardNow} /> <span className="muted">· {ago(node.lastReadAt, dashboardNow)}</span></> : <span className="muted">Not yet</span>}</dd>
        <dt>Monitors</dt><dd>{node.monitorCount === undefined ? <span className="muted">Not read yet</span> : <>{node.monitorCount}{node.state === 'answering' ? '' : <span className="muted"> · last known</span>}</>}</dd>
      </dl>
      <div className="nc-acts row-wrap">
        {viewing
          ? <a className="btn sm primary" href={href('overview')}><Icon name="overview" />Its overview</a>
          : info.readable ? <button type="button" className="btn sm primary" onClick={() => { select(node); navigate(href('overview')); }} aria-label={`Open ${node.label}`}><Icon name="eye" />Open</button> : null}
        {actions.check ? <button type="button" className="btn sm" onClick={() => { void act('check'); }} disabled={busy !== null}><Icon name="refresh" />{busy === 'check' ? 'Checking…' : 'Check now'}</button> : null}
        {actions.retry ? <button type="button" className="btn sm" onClick={() => { void act('retry'); }} disabled={busy !== null}><Icon name="repeat" />{busy === 'retry' ? 'Retrying…' : actions.retry}</button> : null}
        {recover ? <button type="button" className="btn sm" onClick={() => onRecover(node)} disabled={busy !== null} aria-label={`Recover access to ${node.label}`}><Icon name="key" />Recover access</button> : null}
        {actions.edit ? <button type="button" className="btn sm" onClick={() => onEdit(node)} disabled={busy !== null} aria-label={`Edit ${node.label}`}><Icon name="edit" />Edit</button> : null}
        {actions.rotate ? <button type="button" className="btn sm" onClick={() => onRotate(node)} disabled={busy !== null} aria-label={`Rotate access for ${node.label}`}><Icon name="key" />Rotate access</button> : null}
        {actions.remove ? <button type="button" className="btn sm danger" onClick={() => onRemove(node)} disabled={busy !== null}><Icon name="trash" />Remove</button> : null}
      </div>
      {node.local ? <p className="small muted">Set by this deployment’s configuration, so it can’t be removed here, and its address changes only there.</p> : null}
      {admin && node.state === 'credential-refused' ? <p className="small muted">To give it new credentials, use Rotate access with a current administrator credential from the node.</p> : null}
      {recover && node.error ? <p className="small muted">If the node stopped accepting the credential this update uses, Recover access finishes the same update with a current administrator credential from the node.</p> : null}
      {admin && node.state === 'different-node' && actions.move ?<p className="small muted">If <span className="mono">{node.nodeId}</span> moved, edit this entry’s address. The dashboard’s server checks that the same node answers there before saving.</p> : null}
      {admin && node.state === 'not-answering' && node.tlsFingerprint && actions.move ? <p className="small muted">This entry accepts only the certificate with its saved fingerprint. If the node’s certificate was replaced, enter the new fingerprint with Edit; the dashboard’s server checks that the same node answers with it before saving.</p> : null}
      {!admin && unfinished ? <p className="small muted">{node.state === 'rotating' ? 'An administrator can retry it.' : 'An administrator can retry or remove it.'}</p> : null}
      {error ? <p className="small fwarn" role="alert"><Glyph tone="down" />{error}</p> : null}
    </article>
  );
}

/** The selected node's own view of its peers. Peers are read here only if they were added as nodes themselves. */
function PeersCard() {
  const { selection, nodes, endpoint, overview, registry } = useCanary();
  if (!selection) return null;
  const added = new Set((registry.list ?? []).map(node => node.nodeId));
  const peers = nodes.filter(node => node.id !== endpoint && node.state !== 'unlisted');
  return (
    <Card title={<>Peers {selection.label} reports</>} hint={<>As known to <span className="mono">{endpoint}</span>. Each node keeps its own checks, history and alerts.</>}>
      {peers.length ? (
        <div className="peer-pick">
          <ul>
            {peers.map(peer => (
              <li key={peer.id}>
                <Glyph tone={nodeTone(peer)} label={peer.state === 'connected' ? 'connected' : 'out of view'} />
                <span className="grow"><b className="mono">{peer.id}</b> <span className="muted">{peer.name !== peer.id ? peer.name : ''}</span><span className="sub">{nodeStateText(peer, endpoint, overview.now)}</span></span>
                {added.has(peer.id) ? <Tag>On this dashboard</Tag> : <span className="small muted">Not added</span>}
              </li>
            ))}
          </ul>
        </div>
      ) : <p className="muted">No peers are configured on {endpoint}.</p>}
      <p className="foot-note">To read a peer here, add it as its own node. Adding it doesn’t change peer membership.</p>
    </Card>
  );
}

/** Compact registry list for choosing a node: on monitoring pages without a selection and in the top bar's picker. */
function NodeRows({ onPick }: { onPick?: () => void }) {
  const { registry, selection, select, admin } = useCanary();
  if (registry.error && !registry.list) {
    return <Banner tone="down" title="The node list couldn’t be loaded" actions={<button type="button" className="btn sm" onClick={registry.reload}><Icon name="refresh" />Try again</button>}>{describeFailure(registry.error)}</Banner>;
  }
  if (!registry.list) return <Spinner label="Loading nodes…" />;
  const nodes = registryOrder(registry.list);
  if (!nodes.length) {
    return (
      <EmptyState title="No nodes yet" action={admin ? <a className="btn primary" href={href('nodes')} onClick={onPick}><Icon name="plus" />Add a node</a> : undefined}>
        {admin ? 'Add a monitoring node on the Nodes page to see its monitors here.' : 'An administrator hasn’t added any nodes yet.'}
      </EmptyState>
    );
  }
  return (
    <div className="peer-pick">
      <ul>
        {nodes.map(node => {
          const info = nodeStateInfo(node);
          const current = selection?.id === node.id;
          return (
            <li key={node.id}>
              <Glyph tone={info.tone} label={info.label} />
              <span className="grow">
                <b>{node.label}</b> <span className="mono muted">{shownNodeId(node) ?? ''}</span>{node.local ? <> <Tag tone="accent">local</Tag></> : null}
                <span className="sub">{info.label}{node.monitorCount !== undefined ? ` · ${plural(node.monitorCount, 'monitor')}${node.state === 'answering' ? '' : ' last known'}` : ''}</span>
              </span>
              {current ? <Tag icon="eye">Viewing</Tag>
                : info.readable ? <button type="button" className="btn sm" onClick={() => { onPick?.(); select(node); }} aria-label={`Open ${node.label}`}>Open</button>
                : <span className="small muted">Can’t be read</span>}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export function ChooseNodeView({ section }: { section: string }) {
  return (
    <div className="view">
      <PageHead title={SECTION_TITLES[section] ?? 'Choose a node'} sub="Monitoring lives on each node. Choose one and this dashboard’s server reads it for you." />
      <Card title="Choose a node" actions={<a className="btn sm" href={href('nodes')}><Icon name="nodes" />Nodes</a>}>
        <NodeRows />
      </Card>
    </div>
  );
}

/** A selected node that hasn't answered yet: never shown as "no monitors". */
export function NodePendingView() {
  const { selection, connection } = useCanary();
  if (!selection) return null;
  const head = <PageHead title={selection.label} sub={<span className="mono">{selection.nodeId}</span>} />;
  if (connection.link !== 'stale') return <div className="view">{head}<Card><Spinner label={`Reading ${selection.label} through the dashboard’s server…`} /></Card></div>;
  return (
    <div className="view">
      {head}
      <Card>
        <EmptyState title={`Nothing from ${selection.label} yet`} action={<div className="row-wrap">
          <button type="button" className="btn" onClick={() => session.refresh()}><Icon name="refresh" />Try now</button>
          <a className="btn" href={href('nodes')}><Icon name="nodes" />Nodes</a>
        </div>}>
          {connection.failure ? `${describeFailure(connection.failure)} ` : ''}The dashboard’s server keeps trying. Sign-in, accounts and other nodes keep working.
        </EmptyState>
      </Card>
    </div>
  );
}

export function NodePickerDialog({ onClose }: { onClose: () => void }) {
  const { selection, select } = useCanary();
  return (
    <Modal title="Choose a node" onClose={onClose} wide>
      <p className="sec">The same sign-in reads every node on this dashboard. Choosing one changes only what this tab shows; monitoring and alerts continue on every node.</p>
      <NodeRows onPick={onClose} />
      <div className="modal-foot">
        <a className="btn" href={href('nodes')} onClick={onClose}><Icon name="nodes" />Manage nodes</a>
        {selection ? <button type="button" className="btn" onClick={() => { onClose(); select(null); }}><Icon name="overview" />All nodes</button> : null}
      </div>
    </Modal>
  );
}

// ----- Add node: verify (read-only), then confirm. Nothing is enrolled until the administrator confirms.

type Failure = { title: string; text: string };

/**
 * `trust` is how the server was asked to check the node's certificate: only the fingerprint entered ('pinned'), against
 * the authorities it trusts ('ca', an HTTPS address without one), or not at all ('none', plain HTTP).
 */
export function verifyFailure(error: unknown, generated = false, trust: 'pinned' | 'ca' | 'none' = 'none'): Failure {
  const text = describeFailure(error, { preferDetail: true });
  if (!(error instanceof ApiFailure)) return { title: 'Not verified', text };
  const detail = error.detail ?? '';
  if (error.status === 502 && /credential/iu.test(detail)) {
    return generated
      ? { title: 'The node refused its new administrator token', text: 'Check that it started with the settings from the previous step, on new, empty volumes: a node that already has accounts ignores CANARY_ADMIN_TOKEN. Nothing was saved.' }
      : { title: 'The node refused this credential', text: 'It may be mistyped, revoked, or not an admin credential on that node. Paste it again to retry. Nothing was saved.' };
  }
  // The TLS handshake failed, before any credential went to the node.
  if (error.status === 502 && certificateRefused(detail)) {
    return {
      title: 'The node’s certificate wasn’t accepted',
      text: `${text} ${trust === 'pinned' ? `Copy the fingerprint again from the line “${TLS_LOG_LINE}” in the node’s own log, and check the address.` : 'It isn’t one the dashboard’s server trusts.'} The dashboard’s server sent the node no credential. Nothing was saved.`,
    };
  }
  if (error.status === 502 && /different node/iu.test(detail)) return { title: 'A different node answers at this address', text: `${text} Nothing was saved.` };
  if (error.status === 502 && /redirect/iu.test(detail)) {
    return { title: 'The address answered with a redirect', text: 'The dashboard doesn’t follow redirects, so an address can’t send it somewhere else. Enter the node’s own address. Nothing was saved.' };
  }
  if (error.status === 0 || error.status === 502 || error.status === 504) {
    // Go's message for any failed connection also names the certificate, so nothing here says which one it was.
    const tls = trust === 'pinned'
      ? ' With a fingerprint, the dashboard’s server accepts only that certificate: a node that presents another one ends here too, before any credential is sent.'
      : trust === 'ca' ? ' A node with a self-signed certificate of its own, such as one started with CANARY_TLS_MODE=auto, needs its certificate fingerprint here.' : '';
    return { title: 'The dashboard’s server couldn’t verify this node', text: `${text} Your browser wasn’t involved, so it doesn’t matter whether this computer can reach the address.${tls} Nothing was saved.` };
  }
  if (error.status === 401) return { title: 'Your sign-in to this dashboard ended', text: 'Sign in again, then add the node. Nothing was saved.' };
  if (error.status === 403) return { title: 'Only administrators add nodes', text };
  if (error.status === 409 && /already/iu.test(detail)) return { title: 'Already on this dashboard', text: `${text} If the node moved, edit the existing entry’s address instead; that keeps its registration and public links.` };
  if (error.status === 409) return { title: 'This node can’t be added yet', text: `${text} Nothing was saved.` };
  if (error.status === 429) return { title: 'Too many verifications are waiting', text: `${text} Unused verifications expire after 2 minutes.` };
  return { title: 'Not verified', text: `${text} Nothing was saved.` };
}

/**
 * Add node: one dialog, with the direct form for a running node as its default. Above it, administrators choose a new
 * node instead, or a node that connects to this dashboard (NodeWizard.tsx). The choice hides once a flow has something
 * in progress: a verification, a deployment to verify or an invitation. A new node's credentials are made the first
 * time a new node is chosen and live in this dialog's state until it closes. They follow the node ID being typed until
 * they're used for one (shown, copied, saved, verified with or written for an invitation); another ID then gets its own
 * pair, and the pair of the one ID left before comes back with that ID (onboarding.ts secretsForNode).
 */
export function AddNodeDialog({ onClose }: { onClose: () => void }) {
  const { registry } = useCanary();
  const onboarding = useOnboarding();
  const invitations = useInvitations();
  const [plan, setPlan] = useState<AddPlan>({ fresh: false, link: 'direct' });
  const [secrets, setSecrets] = useState<DialogSecrets>({ current: null, earlier: null });
  // The latest pair, for handlers that run before the next render (an invitation's answer, a draft change).
  const held = useRef(secrets);
  const [secretFailure, setSecretFailure] = useState<string | null>(null);
  const [draft, setDraft] = useState<NewNodeDraft>({ label: '', nodeId: '', address: '', bindAddress: '0.0.0.0', platform: 'docker' });
  const [platform, setPlatform] = useState<Platform>('docker');
  const [deployed, setDeployed] = useState<DirectTarget | null>(null);
  const [locked, setLocked] = useState(false);
  const [busy, setBusy] = useState(false);
  // An outbound flow refuses to close while its invitation's token wasn't shown, copied or saved; it asks instead.
  const guard = useRef<(() => boolean) | null>(null);
  const lock = useCallback((value: boolean) => setLocked(value), []);
  const hold = useCallback((value: boolean) => setBusy(value), []);
  const taken: Taken = { nodes: registry.list ?? [], invitations: invitations.data ?? [] };
  const requestClose = (): void => { if (!guard.current || guard.current()) onClose(); };

  /** The tokens once the node ID is `nodeId` (and used for it when `use`); made only when none may serve it. */
  const tokensFor = (nodeId: string, use = false): void => {
    try {
      const next = secretsForNode(held.current, nodeId, use);
      if (next !== held.current) { held.current = next; setSecrets(next); }
      setSecretFailure(null);
    } catch (error) {
      // Nothing changed: a pair used for another ID still isn't shown for this one, so its steps stay hidden.
      setSecretFailure(error instanceof Error ? error.message : 'This browser couldn’t make random credentials.');
    }
  };
  const changeDraft = (next: NewNodeDraft): void => {
    // Only a different node ID can change the tokens; a label, address or platform never does.
    if (next.nodeId !== draft.nodeId) tokensFor(next.nodeId);
    setDraft(next);
  };

  const choose = (next: AddPlan): void => {
    if (next.fresh && !held.current.current) tokensFor(draft.nodeId);
    setLocked(false);
    setPlan(next);
  };

  // A new node's pair, as each flow may show it, and the other ID whose pair this dialog also keeps.
  const pair = secrets.current;
  const earlier = secrets.earlier?.nodeId ?? null;
  const verifying = deployed ? secretsFor(pair, draft.nodeId) : null;
  let body: ReactNode;
  if (!plan.fresh && plan.link === 'direct') body = <DirectAddForm onClose={onClose} onBusy={hold} onStage={lock} />;
  else if (plan.link === 'direct') {
    body = deployed && verifying
      ? <DirectAddForm key="new" onClose={onClose} onBusy={hold} onStage={lock} credential={verifying.admin} expectedId={draft.nodeId} initialLabel={draft.label.trim()} initialUrl={deployed.url}
        pinRequired={directTls(deployed)} onBack={() => setDeployed(null)} />
      : <NewNodeDeploy draft={draft} onDraft={changeDraft} secrets={pair} earlier={earlier} onboarding={onboarding} taken={taken}
        onNext={target => { tokensFor(draft.nodeId, true); setDeployed(target); }} onUse={nodeId => tokensFor(nodeId, true)} onCancel={onClose} />;
  } else {
    body = <OutboundNode fresh={plan.fresh} onboarding={onboarding} secrets={pair} earlier={earlier} onUse={nodeId => tokensFor(nodeId, true)} taken={taken} platform={platform} onPlatform={setPlatform}
      onClose={onClose} onRequestClose={requestClose} onLock={lock} onBusy={hold} guard={guard} onTakenChanged={invitations.reload} />;
  }
  return (
    <Modal title="Add node" onClose={requestClose} busy={busy} wide xwide={plan.fresh || plan.link === 'outbound'}>
      {locked || deployed ? null : <AddNodeChooser plan={plan} onPlan={choose} onboarding={onboarding} />}
      {plan.fresh && secretFailure ? (
        <Banner tone="down" title="No credentials for a new node" actions={<button type="button" className="btn sm" onClick={() => tokensFor('')}><Icon name="refresh" />Try again</button>}>
          {secretFailure} Use a browser with secure random numbers, or set up the node yourself and add it as a running node.
        </Banner>
      ) : null}
      {body}
    </Modal>
  );
}

interface DirectOptions {
  onClose: () => void;
  onBusy: (busy: boolean) => void;
  /** True while a verified node waits for confirmation: the dialog then hides its other choices. */
  onStage: (locked: boolean) => void;
  /** A new node: the administrator token this browser made for it, used instead of a pasted credential. */
  credential?: string;
  /** The new node's ID; a different node at the address is shown and can't be added from here. */
  expectedId?: string;
  initialLabel?: string;
  initialUrl?: string;
  /**
   * A new node that serves HTTPS with its own self-signed certificate (CANARY_TLS_MODE=auto): its fingerprint, from its
   * log, is required, because no certificate authority vouches for that certificate.
   */
  pinRequired?: boolean;
  /** Back to the new node's deployment steps. */
  onBack?: () => void;
}

/** Where the fingerprint comes from, and what it isn't. */
const PIN_SOURCE = `Copy the 64 characters after “${TLS_LOG_LINE}” from the node’s own log, where you see its output yourself, such as your own terminal on its host, not from a message or page someone else could change. A node in a container also prints it with canary tls-info run inside it, such as with docker exec. It isn’t secret, and it isn’t the “Node connection fingerprint” of a node that connects to this dashboard.`;

/**
 * Verify, then confirm: the form a running node is added with, and a new direct node's last step. The TLS certificate
 * fingerprint field shows for an https:// address (or while it holds a value); left empty, nothing about it is sent and
 * the form works exactly as before.
 */
export function DirectAddForm({ onClose, onBusy, onStage, credential, expectedId, initialLabel = '', initialUrl = '', pinRequired = false, onBack }: DirectOptions) {
  const { api, registry, connection, stillCurrent } = useCanary();
  const [label, setLabel] = useState(initialLabel);
  const [url, setUrl] = useState(initialUrl);
  const [pin, setPin] = useState('');
  // The fingerprint the last verification sent (normalized), to compare with the one the server reports back.
  const [sentPin, setSentPin] = useState('');
  const [token, setToken] = useState('');
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState<null | 'verify' | 'add'>(null);
  const [failure, setFailure] = useState<Failure | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [verified, setVerified] = useState<NodeVerification | null>(null);
  const [spent, setSpent] = useState(false);
  const [summaryRef, failed] = useSummaryFocus();
  const okRef = useRef<HTMLElement>(null);
  const attempt = useRef<AbortController | null>(null);
  const ticket = useRef<string | null>(null);
  const apiRef = useRef(api);
  apiRef.current = api;
  const now = useNow(1000);

  // Closing for any reason (Cancel, Escape, sign-out, another node selected) stops a running request and drops an
  // unused ticket. The credential is state of this dialog only, so it goes with it.
  useEffect(() => () => {
    attempt.current?.abort();
    const outstanding = ticket.current;
    ticket.current = null;
    if (outstanding) void apiRef.current.discardVerification(outstanding).catch(() => undefined);
  }, []);
  useEffect(() => { if (verified) okRef.current?.focus(); }, [verified]);
  useEffect(() => { onBusy(busy === 'add'); }, [busy, onBusy]);
  useEffect(() => { onStage(verified !== null); }, [verified, onStage]);

  // A new node's own administrator token stays with the dialog, so a failed check can simply be tried again.
  const generated = credential !== undefined;
  const sent = credential ?? token;
  const pinShown = pinRequired || httpsAddress(url) || pin !== '';
  const errors = {
    label: labelProblem(label), url: nodeAddressProblem(url), tlsFingerprint: pinShown ? tlsFingerprintProblem(pin, url, pinRequired) : undefined, token: credentialProblem(sent),
  };
  const visible: Partial<typeof errors> = show ? errors : {};
  const discard = (): void => {
    const outstanding = ticket.current;
    ticket.current = null;
    if (outstanding) void api.discardVerification(outstanding).catch(() => undefined);
  };

  const verify = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy) return;
    setShow(true); setFailure(null); setNotice(null);
    if (Object.values(errors).some(Boolean)) { failed(); return; }
    const controller = new AbortController();
    attempt.current = controller;
    const trust = !httpsAddress(url) ? 'none' : tlsFingerprintValue(pin) ? 'pinned' : 'ca';
    setSentPin(tlsFingerprintValue(pin) ?? '');
    setBusy('verify');
    try {
      // An empty fingerprint isn't sent: the body is then exactly { label, url, token }, as before.
      const result = await api.verifyNode({ label, url, token: sent, tlsFingerprint: pin }, controller.signal);
      setToken('');
      if (controller.signal.aborted || !stillCurrent()) { void api.discardVerification(result.ticket).catch(() => undefined); return; }
      ticket.current = result.ticket;
      setShow(false); setSpent(false);
      setVerified(result);
    } catch (error) {
      // The request may have reached the server either way, so the page lets go of the credential.
      setToken('');
      if (isAbort(error)) { setNotice(generated ? 'Stopped. Nothing was saved.' : 'Stopped. Nothing was saved. Paste the credential again to verify.'); return; }
      if (!stillCurrent()) return;
      setFailure(verifyFailure(error, generated, trust));
      failed();
    } finally {
      if (attempt.current === controller) attempt.current = null;
      setBusy(null);
    }
  };

  const back = (): void => {
    discard();
    setVerified(null); setFailure(null); setShow(false);
    if (generated) setNotice(spent ? null : 'Going back discarded that verification.');
    else setNotice(spent ? 'Paste the credential again to verify.' : 'Going back discarded that verification. Paste the credential again to verify.');
  };

  const add = async (): Promise<void> => {
    if (!verified || busy || spent) return;
    const used = verified.ticket;
    // Enrolling uses the ticket up, whatever the outcome; it is never discarded or sent again.
    ticket.current = null;
    setSpent(true);
    const controller = new AbortController();
    attempt.current = controller;
    setBusy('add'); setFailure(null);
    try {
      const node = await api.enrollNode(used, controller.signal);
      if (!stillCurrent()) return;
      registry.reload();
      notify(node.state === 'enrolling'
        ? `${node.label} is listed, but enrolling it didn’t finish. Retry it from Nodes.`
        : `${node.label} added. The dashboard’s server reads it from now on.`);
      onClose();
    } catch (error) {
      if (isAbort(error) || !stillCurrent()) return;
      registry.reload();
      const refused = error instanceof ApiFailure && error.status >= 400 && error.status < 500 && error.status !== 408;
      const reason = describeFailure(error, { preferDetail: true });
      setFailure(refused
        ? { title: 'Not added', text: /verify again/iu.test(reason) ? reason : `${reason} Go back and verify again.` }
        : { title: 'The result isn’t known yet', text: `${describeFailure(error)} The dashboard may have finished adding it. Check the node list: an unfinished enrollment shows there as “Enrollment unfinished”, with Retry and Remove.` });
    } finally {
      if (attempt.current === controller) attempt.current = null;
      setBusy(null);
    }
  };

  const remaining = verified ? verified.expiresAt - (now + (connection.issuerSkew ?? 0)) : 0;
  const expired = Boolean(verified) && remaining <= 0;
  const errorList = failure ? [] : Object.values(visible).filter(Boolean) as string[];

  let body: ReactNode;
  if (!verified) {
    body = (
      <form className="connect-form" onSubmit={event => { void verify(event); }} noValidate aria-busy={busy === 'verify'}>
        <p className="sec">{generated
          ? <>The dashboard’s server contacts the new node <span className="mono">{expectedId}</span> at this address with the administrator token made for it; your browser doesn’t. Verifying changes nothing on the node.{pinRequired ? ' It accepts only the certificate with the fingerprint below, and sends the token only once that certificate checks out.' : ''}</>
          : 'The dashboard’s server contacts the node at this address; your browser doesn’t. A private, NetBird or Tailscale address is fine as long as the server can reach it. Verifying changes nothing on the node.'}</p>
        {notice ? <Banner tone="info" title={notice} /> : null}
        <ErrorSummary ref={summaryRef} title={failure ? failure.title : 'Check these fields'} errors={errorList}>{failure ? <p>{failure.text}</p> : null}</ErrorSummary>
        <TextField label="Name on this dashboard" value={label} onChange={setLabel} error={visible.label} maxLength={128} disabled={busy !== null} autoFocus
          hint="Every account on this dashboard sees it. The node keeps its own ID." />
        <TextField label="Node address" type="url" inputMode="url" mono value={url} onChange={setUrl} error={visible.url} disabled={busy !== null} placeholder="http://100.79.12.4:3100"
          hint="Scheme, host and port, as the dashboard’s server reaches it. HTTPS certificates are checked and redirects aren’t followed." />
        {pinShown ? (
          <TextField label="TLS certificate fingerprint" mono value={pin} onChange={setPin} error={visible.tlsFingerprint} disabled={busy !== null} maxLength={200}
            placeholder={pinRequired ? '64 hexadecimal characters from the node’s log' : 'Empty: checked against trusted certificate authorities'}
            hint={pinRequired
              ? `${PIN_SOURCE} The dashboard’s server then accepts only this certificate at the address.`
              : `Only for a node whose certificate no authority the dashboard’s server trusts has signed, such as one started with CANARY_TLS_MODE=auto. ${PIN_SOURCE} With it, the server accepts only that certificate. Empty, it checks the certificate and host name against the authorities it trusts; it never accepts just any certificate. Adding the node doesn’t change its own TLS setting.`} />
        ) : null}
        {generated ? (
          <dl className="kv"><dt>Enrollment credential</dt><dd>The administrator token made for <span className="mono">{expectedId}</span> in the previous step. It goes only to the dashboard’s server.</dd></dl>
        ) : (
          <SecretField label="Enrollment credential" value={token} onChange={setToken} error={visible.token} disabled={busy !== null}
            hint="From the node, for example an admin account token. It goes only to the dashboard’s server; this page forgets it after each verification." />
        )}
        <div className="modal-foot">
          <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
          <span className="row-wrap">
            {onBack && busy === null ? <button type="button" className="btn" onClick={onBack}><Icon name="chevLeft" />Back</button> : null}
            {busy === 'verify'
              ? <button type="button" className="btn" onClick={() => attempt.current?.abort()}>Stop</button>
              : <button type="button" className="btn" onClick={onClose}>Cancel</button>}
            <button type="submit" className="btn primary" disabled={busy !== null}><Icon name="check" />{busy === 'verify' ? 'Verifying…' : failure ? 'Verify again' : 'Verify node'}</button>
          </span>
        </div>
      </form>
    );
  } else {
    const name = verified.label || label.trim();
    // A new node is expected by its ID; another node answering there is never added from this step.
    const mismatch = expectedId !== undefined && verified.node.id !== expectedId;
    // The server reports the fingerprint it will save; anything but the one sent is never added from here.
    const pinMismatch = (verified.tlsFingerprint ?? '') !== sentPin;
    body = (
      <>
        <div className="banner b-ok" role="status">
          <Glyph tone="up" />
          <div>
            <b className="bt" tabIndex={-1} ref={okRef}>Verified by the dashboard’s server</b>
            <div className="bb">The node proved it is <span className="mono">{verified.node.id}</span>. {generated
              ? 'The administrator token made here went to the dashboard’s server with this check; this page keeps its own copy only until this dialog closes.'
              : 'The credential is on the dashboard’s server now; this page no longer holds it.'} Nothing has changed on the node yet.</div>
          </div>
        </div>
        {mismatch ? <Banner tone="down" title="A different node answers at this address">It proved it is <span className="mono">{verified.node.id}</span>, not the new node <span className="mono">{expectedId}</span>, so it can’t be added from here. Go back and check the address. Nothing was saved.</Banner> : null}
        {pinMismatch ? <Banner tone="down" title="The certificate fingerprint isn’t the one entered">The dashboard’s server reported a different certificate fingerprint than the one this page sent, so the node can’t be added from here. Go back and verify again. Nothing was saved.</Banner> : null}
        <dl className="kv">
          <dt>Name on this dashboard</dt><dd>{name}</dd>
          <dt>Node ID</dt><dd className="mono">{verified.node.id}</dd>
          {verified.node.name !== verified.node.id ? <><dt>Node name</dt><dd>{verified.node.name}</dd></> : null}
          <dt>Address</dt><dd className="mono">{verified.url}</dd>
          {verified.tlsFingerprint ? <><dt>TLS certificate</dt><dd className="mono">{verified.tlsFingerprint}</dd></> : null}
          <dt>Verification</dt><dd>{expired ? 'Expired' : `Ends in ${elapsed(remaining)}`}</dd>
        </dl>
        {verified.tlsFingerprint ? <p className="small muted"><Icon name="lock" className="inline" />The dashboard’s server accepted only the certificate with this fingerprint there, before it sent the credential, and keeps using only that one for this node.</p> : null}
        {!verified.plainHttp && !verified.tlsFingerprint ? <p className="small muted"><Icon name="lock" className="inline" />HTTPS: the dashboard’s server checked the node’s certificate and host name against the certificate authorities it trusts.</p> : null}
        {verified.plainHttp ? <p className="small fwarn"><Glyph tone="warn" />Plain HTTP: traffic between the dashboard’s server and this node isn’t encrypted by Uptime Canary. Use HTTPS, or a network that encrypts it, such as NetBird or Tailscale.</p> : null}
        <p className="modal-note">Adding it lets every account on this dashboard read this node, and editors and administrators change its monitoring. The dashboard’s server sets up its own access on the node for each role. It doesn’t join the node to other nodes’ peers or change its identity, credentials or cluster secret.</p>
        {expired && !spent ? <Banner tone="warn" title="This verification expired">Nothing was added. Go back and verify again.</Banner> : null}
        {failure ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>{failure.title}</b><p>{failure.text}</p></div></div> : null}
        <div className="modal-foot">
          <button type="button" className="btn" onClick={back} disabled={busy !== null}><Icon name="chevLeft" />Back</button>
          <span className="row-wrap">
            <button type="button" className="btn" onClick={onClose} disabled={busy !== null}>{spent ? 'Close' : 'Cancel'}</button>
            {(spent && !busy) || mismatch || pinMismatch ? null : <button type="button" className="btn primary" onClick={() => { void add(); }} disabled={busy !== null || expired}><Icon name="plus" />{busy === 'add' ? 'Adding…' : `Add “${name}”`}</button>}
          </span>
        </div>
      </>
    );
  }
  return body;
}

export function RemoveNodeDialog({ node, onClose }: { node: RegisteredNode; onClose: () => void }) {
  const { api, registry, selection, select, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // An outbound node's connection stops carrying anything but that revocation as soon as the entry leaves the list.
  const note = node.connection === 'outbound'
    ? `${node.state === 'answering'
      ? 'The dashboard stops using the node’s connection for anything else at once, and asks the node over it to revoke the access this dashboard set up there.'
      : 'The node isn’t connected right now. The dashboard stops using its connection at once, except so the node can confirm that this dashboard’s access there was revoked; until it connects again, the entry stays as “Removal unfinished”.'} A change sent to the node just before may still finish there.`
    : node.state === 'answering'
    ? 'The dashboard’s server also asks the node to revoke the access this dashboard set up there.'
    : node.state === 'different-node'
      ? <>This address now answers as a different node, so the dashboard won’t send it anything. Revoke this dashboard’s access on <span className="mono">{node.nodeId}</span> wherever it runs now.</>
      : 'The node isn’t accepting the dashboard’s requests right now, so its access may not be revoked yet. The entry then stays as “Removal unfinished” until the dashboard can confirm it.';
  const remove = async (): Promise<void> => {
    if (busy) return;
    setBusy(true); setError(null);
    try {
      const result = await api.removeNode(node.id);
      if (!stillCurrent()) return;
      notify(result.removed
        ? `Removed ${node.label}. The node confirmed this dashboard’s access was revoked.`
        : `${node.label} is no longer read, but the node hasn’t confirmed that this dashboard’s access was revoked. It shows as “Removal unfinished”; retry when the node answers.`);
      onClose();
      if (selection?.id === node.id) select(null);
      else registry.reload();
    } catch (failure) {
      if (!stillCurrent()) return;
      setError(describeFailure(failure, { preferDetail: true }));
      setBusy(false);
    }
  };
  return (
    <Modal title={<>Remove “{node.label}”?</>} onClose={onClose} busy={busy}>
      <p>This dashboard stops reading <span className="mono">{node.nodeId}</span> for every account. The node keeps running, checking and alerting, with its own accounts, data and peers.</p>
      <p className="modal-note">{note}</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not removed</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Removing…' : 'Remove node'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}

// ----- Edit and Rotate access: administrators only. Both keep the registration, so its ID, any selection of it and its
// public links stay. The list keeps showing (and refreshing) the saved entry while a dialog is open; neither dialog
// changes what the list shows until the dashboard's server answers.

/**
 * Rename the entry, follow the same node to a new address, or change the TLS certificate it must present there (for
 * example after its certificate was replaced). Never a way to point it at another node. An address or certificate change
 * is saved only after the dashboard's server checks the same node with it, using all three of its credentials.
 */
export function EditNodeDialog({ node: opened, onClose }: { node: RegisteredNode; onClose: () => void }) {
  const { api, registry, stillCurrent } = useCanary();
  const node = registry.map.get(opened.id) ?? opened;
  const [label, setLabel] = useState(opened.label);
  const [url, setUrl] = useState(opened.url ?? '');
  const [pin, setPin] = useState(opened.tlsFingerprint ?? '');
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ChangeFailure | null>(null);
  const [summaryRef, failed] = useSummaryFocus();

  // The bundled node's address belongs to the deployment; the server refuses to move it (409), or to change its
  // certificate fingerprint, which the deployment's settings set too.
  const movable = !node.local && opened.connection !== 'outbound' && opened.url !== undefined;
  const pending = pendingOperation(node.state);
  const pinMeanwhile = node.tlsFingerprint !== opened.tlsFingerprint;
  const changedMeanwhile = node.label !== opened.label || node.url !== opened.url || pinMeanwhile;
  const { input, moving, errors } = editChanges(opened, label, url, movable);
  // The fingerprint field shows for an HTTPS address, and stays while it holds a value, so a plain HTTP move clears it
  // explicitly rather than drop a saved fingerprint unseen.
  const pinShown = movable && (httpsAddress(url) || pin !== '' || Boolean(opened.tlsFingerprint));
  const trust = pinShown ? tlsFingerprintEdit(opened, pin, url) : null;
  const pinChanged = Boolean(trust?.changed);
  const fieldErrors: { label?: string; url?: string; tlsFingerprint?: string } = trust?.error ? { ...errors, tlsFingerprint: trust.error } : errors;
  const invalid = Boolean(fieldErrors.label || fieldErrors.url || fieldErrors.tlsFingerprint);
  // Fields left as they were keep a newer saved value: an address-only edit doesn't undo a rename made meanwhile, and a
  // fingerprint is sent only when it changed here.
  const keptLabel = label.trim() === opened.label ? node.label : label.trim();
  const renamed: RegisteredNodeEdit | null = input ? { ...input, label: label === opened.label ? node.label : input.label } : null;
  const body: RegisteredNodeEdit | null = pinChanged && !invalid && trust ? { ...(renamed ?? { label: keptLabel }), tlsFingerprint: trust.value } : renamed;
  const unchanged = !body && !invalid;
  const visible: { label?: string; url?: string; tlsFingerprint?: string } = show ? fieldErrors : {};

  const save = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || pending) return;
    setShow(true); setFailure(null);
    if (invalid) { failed(); return; }
    if (!body) return;
    setBusy(true);
    try {
      const saved = await api.editNode(opened.id, body);
      if (!stillCurrent()) return;
      registry.reload();
      notify(moving
        ? `${saved.label} is now read at ${saved.url ?? body.url}. The dashboard’s server confirmed it’s the same node, ${saved.nodeId}.`
        : pinChanged
          ? body.tlsFingerprint
            ? `${saved.label} now has to present the certificate with the new fingerprint. The dashboard’s server confirmed it’s the same node, ${saved.nodeId}.`
            : `${saved.label}’s certificate is now checked against the certificate authorities the dashboard’s server trusts. It confirmed it’s the same node, ${saved.nodeId}.`
          : `Renamed to ${saved.label}. Every account on this dashboard sees the new name.`);
      onClose();
    } catch (error) {
      if (isAbort(error) || !stillCurrent()) return;
      const next = editFailure(error, node, moving, pinChanged);
      if (next.reload) registry.reload();
      setFailure(next);
      failed();
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  };

  return (
    <Modal title={<>Edit “{opened.label}”</>} onClose={onClose} busy={busy} wide>
      <form className="connect-form" onSubmit={event => { void save(event); }} noValidate aria-busy={busy}>
        <p className="sec">Every account on this dashboard sees these changes. The registration stays the same, with its ID, its public status links and anyone’s current view of it.</p>
        {changedMeanwhile ? (
          <Banner tone="info" title="This entry changed while the form was open">
            It’s now “{node.label}”{node.url ? <> at <span className="mono">{node.url}</span></> : null}.{pinMeanwhile ? ' Its TLS certificate fingerprint changed too.' : ''} Fields you didn’t edit here keep those newer values when you save.
          </Banner>
        ) : null}
        {pending ? <Banner tone="warn" title={nodeStateInfo(node).label}>Another change to this node is running. Wait until it finishes, then save.</Banner> : null}
        <ErrorSummary ref={summaryRef} title={failure ? failure.title : 'Check these fields'} errors={failure ? [] : Object.values(visible).filter(Boolean) as string[]}>
          {failure ? <p>{failure.text}</p> : null}
        </ErrorSummary>
        <TextField label="Name on this dashboard" value={label} onChange={setLabel} error={visible.label} maxLength={128} disabled={busy} autoFocus
          hint="Only this dashboard’s name for it. The node keeps its own ID and name." />
        {movable ? (
          <TextField label="Node address" type="url" inputMode="url" mono value={url} onChange={setUrl} error={visible.url} disabled={busy} placeholder="http://100.79.12.4:3100"
            hint={<>Change it only when <span className="mono">{opened.nodeId}</span> itself moved. Before saving, the dashboard’s server checks that the node answering there is the same one and accepts all three of this dashboard’s access credentials. It can’t point this entry at a different node; add that one as its own node.</>} />
        ) : opened.connection === 'outbound' ? (
          <p className="small muted">This node connects to the dashboard itself, so its entry has no address to edit. Renaming works even while it isn’t connected.</p>
        ) : (
          <>
            <dl className="kv">
              <dt>Address</dt><dd className="mono">{node.url ?? 'Not shown'}</dd>
              {node.tlsFingerprint ? <><dt>TLS certificate</dt><dd className="mono">{node.tlsFingerprint}</dd></> : null}
            </dl>
            <p className="small muted">This deployment’s settings manage the bundled node’s address{node.tlsFingerprint ? ' and its certificate fingerprint' : ''}, so it changes there, not here. You can still rename it.</p>
          </>
        )}
        {pinShown ? (
          <TextField label="TLS certificate fingerprint" mono value={pin} onChange={setPin} error={visible.tlsFingerprint} disabled={busy} maxLength={200}
            placeholder="Empty: checked against trusted certificate authorities"
            hint={<>The one certificate the dashboard’s server accepts at this address, for a node whose certificate no authority it trusts has signed, such as one started with CANARY_TLS_MODE=auto. Change it only when the node’s certificate was replaced. {PIN_SOURCE} Empty, the server checks the certificate and host name against the authorities it trusts; it never accepts just any certificate. Before saving a change, it checks that <span className="mono">{opened.nodeId}</span> answers with that certificate and accepts all three of this dashboard’s access credentials, which it sends only once the certificate checks out.</>} />
        ) : null}
        {movable && !pinShown ? (
          <p className="small muted">To have this node encrypt its own connections, start it with CANARY_TLS_MODE=auto: it then serves only HTTPS. At the same time, change its peers’ CANARY_PEERS entries for it to its https:// address with its fingerprint, and enter both here. To read the fingerprint before that, first set CANARY_TLS_MODE=off in its settings: once a node has its certificate, leaving the setting empty starts it with HTTPS. Then stop it and run its tls-init command once with those settings, its data and the same user, such as canary tls-init in a one-off container of its image with its volumes: that makes its ten-year certificate and prints the fingerprint, without serving, checks or alerts. Start it again as before; it stays on plain HTTP until you set auto. Nothing on this page changes the node’s settings.</p>
        ) : null}
        {moving ? <p className="small muted">The dashboard’s server contacts the new address when you save. Renaming alone works even while the node isn’t answering.</p> : null}
        {!moving && pinChanged ? <p className="small muted">The dashboard’s server contacts the node with the new certificate setting when you save. Renaming alone works even while the node isn’t answering.</p> : null}
        {moving && body?.url?.startsWith('http:') ? <p className="small fwarn"><Glyph tone="warn" />Plain HTTP: traffic between the dashboard’s server and this node isn’t encrypted by Uptime Canary. Use HTTPS, or a network that encrypts it, such as NetBird or Tailscale.</p> : null}
        <div className="modal-foot">
          <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
          <span className="row-wrap">
            <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
            <button type="submit" className="btn primary" disabled={busy || pending || unchanged}><Icon name="check" />{busy ? (moving ? 'Checking the new address…' : pinChanged ? 'Checking the node…' : 'Saving…') : 'Save changes'}</button>
          </span>
        </div>
      </form>
    </Modal>
  );
}

/**
 * Replace the three role credentials this dashboard uses on the node. Nothing else on the node changes, and the
 * dashboard sign-in is untouched. The optional credential is an administrator on this same node, for when the node no
 * longer accepts the dashboard's own; it is sent once and this page forgets it before the answer arrives.
 */
export function RotateNodeDialog({ node: opened, onClose }: { node: RegisteredNode; onClose: () => void }) {
  const { api, registry, selection, stillCurrent, refresh } = useCanary();
  const node = registry.map.get(opened.id) ?? opened;
  const [token, setToken] = useState('');
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ChangeFailure | null>(null);
  const [summaryRef, failed] = useSummaryFocus();
  const problem = recoveryProblem(token);
  const pending = pendingOperation(node.state);

  const rotate = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || pending) return;
    setShow(true); setFailure(null);
    if (problem) { failed(); return; }
    const recovery = token;
    // Sent once, whatever the answer: the page lets go of it before the request leaves.
    setToken(''); setShow(false);
    setBusy(true);
    try {
      const result = await api.rotateNode(opened.id, recovery || undefined);
      if (!stillCurrent()) return;
      registry.reload();
      const unconfirmed = pendingOperation(result.state);
      // The selected node answers again at once instead of after the poll's back-off.
      if (selection?.id === opened.id && !unconfirmed) refresh();
      notify(unconfirmed
        ? `The new credentials for ${result.label} are saved, but the node hasn’t confirmed them yet. It shows as “Updating node access”; the dashboard’s server keeps retrying, and its card has Retry and Recover access.`
        : `Rotated this dashboard’s access to ${result.label}. The node confirmed the new credentials; the old ones no longer work there.`);
      onClose();
    } catch (error) {
      if (!stillCurrent()) return;
      const next = rotationFailure(error, node, recovery !== '');
      if (next.reload) registry.reload();
      setFailure(next);
      failed();
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  };

  return (
    <Modal title={<>Rotate access for “{node.label}”?</>} onClose={onClose} busy={busy} wide>
      <form className="connect-form" onSubmit={event => { void rotate(event); }} noValidate aria-busy={busy}>
        <p>The dashboard’s server makes new credentials for the three accounts it uses on <span className="mono">{node.nodeId}</span> (administrator, editor and viewer) and has the node swap them in. The old credentials, and any node sessions made with them, stop working there.</p>
        <p className="modal-note">Nothing else changes: the node keeps its ID, its own accounts, peers, cluster secret, monitors and history, and you stay signed in here. Until the node confirms, this dashboard doesn’t read it and shows it as “Updating node access”; the node keeps monitoring and alerting on its own. The new credentials are saved, encrypted, before the node is asked, so if its answer is lost, a retry finishes this same change.</p>
        {node.local ? <p className="small muted">This replaces only this dashboard’s own access to the bundled node. The administrator token in this deployment’s settings is separate and stays as it is.</p> : null}
        {node.state === 'credential-refused' ? (
          <Banner tone="warn" title="The node refuses this dashboard’s current credentials">
            Paste a current administrator credential from <span className="mono">{node.nodeId}</span> below. Without one, the node may refuse the new credentials too, and the update then stays unfinished.
          </Banner>
        ) : null}
        {pending ? <Banner tone="warn" title={nodeStateInfo(node).label}>Another change to this node is running. Wait until it finishes.</Banner> : null}
        <ErrorSummary ref={summaryRef} title={failure ? failure.title : 'Check this field'} errors={failure || !show || !problem ? [] : [problem]}>
          {failure ? <p>{failure.text}</p> : null}
        </ErrorSummary>
        <SecretField label="Node administrator credential" optional value={token} onChange={setToken} error={show ? problem : undefined} disabled={busy}
          hint="Only needed when the node no longer accepts this dashboard’s credentials. It must be an administrator on this same node, such as its admin account token. The dashboard’s server checks it there first and keeps it only until the node confirms; this page forgets it as soon as it’s sent." />
        <div className="modal-foot">
          <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
          <span className="row-wrap">
            <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
            <button type="submit" className="btn primary" disabled={busy || pending}><Icon name="key" />{busy ? 'Rotating…' : 'Rotate access'}</button>
          </span>
        </div>
      </form>
    </Modal>
  );
}

/**
 * Finish a pending access update: the same saved credentials, never another set. The optional credential is an
 * administrator on this same node, for when it stopped accepting the one the saved update uses, for example because it
 * was revoked there after the update was saved. It goes once through Retry's route, and this page forgets it before the
 * answer arrives. The entry, its selection and the dashboard sign-in stay as they are; only the list is reloaded.
 */
export function RecoverAccessDialog({ node: opened, onClose }: { node: RegisteredNode; onClose: () => void }) {
  const { api, registry, selection, select, stillCurrent, refresh } = useCanary();
  const node = registry.map.get(opened.id) ?? opened;
  const [token, setToken] = useState('');
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ChangeFailure | null>(null);
  const [summaryRef, failed] = useSummaryFocus();
  const problem = recoveryProblem(token);
  // The list keeps refreshing while this is open: the update may finish, or the entry may start being removed.
  const updating = node.state === 'rotating';
  const info = nodeStateInfo(node);

  const finish = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || !updating) return;
    setShow(true); setFailure(null);
    if (problem) { failed(); return; }
    const recovery = token;
    // Sent once, whatever the answer: the page lets go of it before the request leaves.
    setToken(''); setShow(false);
    setBusy(true);
    try {
      const result = await api.retryNode(opened.id, recovery || undefined);
      if (!stillCurrent()) return;
      const outcome = recoveryOutcome(node, result, recovery !== '');
      notify(outcome.text);
      onClose();
      if (result === null && selection?.id === opened.id) select(null);
      else registry.reload();
      // A confirmed update makes the selected node readable again: read it now, not after the poll's back-off.
      if (result && outcome.finished && selection?.id === opened.id) refresh();
    } catch (error) {
      if (!stillCurrent()) return;
      const next = recoveryFailure(error, node, recovery !== '');
      if (next.reload) registry.reload();
      setFailure(next);
      failed();
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  };

  return (
    <Modal title={<>Recover access to “{node.label}”</>} onClose={onClose} busy={busy} wide>
      <form className="connect-form" onSubmit={event => { void finish(event); }} noValidate aria-busy={busy}>
        <p>This finishes the access update the dashboard’s server already saved for <span className="mono">{node.nodeId}</span>: the same new credentials for the three accounts it uses there (administrator, editor and viewer). It doesn’t make another set. Nothing else changes: the node keeps its ID, its own accounts, peers, monitors and history, and you stay signed in here.</p>
        <p className="modal-note">Add a credential when the node stopped accepting the one this update uses, for example because it was revoked there after the update was saved. The dashboard’s server first checks it on the node without changing anything, and checks that the three accounts this dashboard uses there still exist; it doesn’t recreate missing ones. Only then does it keep the credential, encrypted, with the saved update until the node confirms. Left empty, this retries with what the server already has, like Retry.</p>
        {node.local ? <p className="small muted">This finishes only this dashboard’s own access to the bundled node. The administrator token in this deployment’s settings is separate and stays as it is.</p> : null}
        {updating && node.error ? <p className="small muted">Last attempt: {period(node.error)}</p> : null}
        {updating ? null : pendingOperation(node.state)
          ? <Banner tone="warn" title={info.label}>This entry isn’t updating its access any more, so there’s nothing to finish here. The node list shows what’s running now.</Banner>
          : <Banner tone="info" title="There’s nothing left to finish">The update isn’t pending any more; the node list shows “{info.label}” now. No credential is needed.</Banner>}
        <ErrorSummary ref={summaryRef} title={failure ? failure.title : 'Check this field'} errors={failure || !show || !problem ? [] : [problem]}>
          {failure ? <p>{failure.text}</p> : null}
        </ErrorSummary>
        {updating ? (
          <SecretField label="Node administrator credential" optional value={token} onChange={setToken} error={show ? problem : undefined} disabled={busy}
            hint="From this same node, such as its admin account token. It goes only to the dashboard’s server, in this one request; this page forgets it as soon as it’s sent." />
        ) : null}
        <div className="modal-foot">
          <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
          <span className="row-wrap">
            <button type="button" className="btn" onClick={onClose} disabled={busy}>{updating ? 'Cancel' : 'Close'}</button>
            {updating ? <button type="submit" className="btn primary" disabled={busy}><Icon name="check" />{busy ? 'Finishing…' : 'Finish update'}</button> : null}
          </span>
        </div>
      </form>
    </Modal>
  );
}
