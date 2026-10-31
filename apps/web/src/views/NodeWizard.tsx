import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import type { NodeInvitation, NodeInvitationCreated, NodeOnboardingCapabilities, RegisteredNode } from '@uptime-canary/shared';
import { ApiFailure, describeFailure, isAbort } from '../api';
import { useCanary } from '../context';
import { connectionWaitingHint, invitationFailure, invitationGone, invitationInfo, labelProblem, pairingProblem, REINVITE_DEPLOYED, type ChangeFailure } from '../dashboard';
import { elapsed } from '../format';
import { useNow, useResource, useSummaryFocus, type Resource } from '../hooks';
import { notify } from '../notices';
import { bindingAccessNote, directTarget, directTls, imageReference, localBinding, nodeIdProblem, outboundConnectionAddress, PLATFORMS, schemePortNote, secretsFor, setupGuide, TLS_LOG_LINE, type DirectTarget, type GuideBlock, type NodeSecrets, type NodeSetup, type Platform } from '../onboarding';
import { Banner, Card, CopyButton, Modal, Spinner } from '../ui/bits';
import { CheckField, ErrorSummary, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

/*
 * Add node wizard, beside the existing direct form (Registry.tsx), which stays the default. Two choices: a node that's
 * already running or a new one, and whether this dashboard's server reaches the node or the node reaches this dashboard.
 *
 * A new node's administrator and cluster tokens are made in this browser and live only in the Add node dialog's state;
 * an invitation's one-time token lives only in this wizard's state. Neither is put in an address, a notice, an error, the
 * console or browser storage, and both go when the dialog closes. Previews mask them; Show, Copy and Download are the
 * deliberate ways to see or take them. Only the administrator token is ever sent, to this dashboard's server, when a new
 * direct node is verified. Once used for one node ID, the two tokens are never shown for another (onboarding.ts
 * secretsFor). The node-connection address comes from the dashboard's server, never from this page's own.
 */

export type AddPlan = { fresh: boolean; link: 'direct' | 'outbound' };
export interface Taken { nodes: readonly RegisteredNode[]; invitations: readonly NodeInvitation[] }

/** Administrators: what the wizard may offer, read from the dashboard's server when Add node opens. */
export function useOnboarding(): Resource<NodeOnboardingCapabilities> {
  const { api, connection } = useCanary();
  return useResource(`${connection.identity}|onboarding`, signal => api.onboarding(signal));
}

/** Administrators: this dashboard's invitations; refreshed while a list shows them. */
export function useInvitations(refreshMs?: number): Resource<NodeInvitation[]> {
  const { api, connection } = useCanary();
  return useResource(`${connection.identity}|invitations|${refreshMs ?? 0}`, signal => api.invitations(signal), refreshMs);
}

/** Current setup status; choosing the outbound setup flow never depends on this read. */
function outboundReason(onboarding: Resource<NodeOnboardingCapabilities>): string | null {
  const capabilities = onboarding.data;
  if (capabilities) return capabilities.outboundEnabled ? null : 'Not set up here: this dashboard’s deployment has no node-connection address.';
  return onboarding.error ? 'This dashboard’s server didn’t say whether nodes can connect to it.' : 'Checking whether nodes can connect to this dashboard…';
}

export function AddNodeChooser({ plan, onPlan, onboarding }: { plan: AddPlan; onPlan: (plan: AddPlan) => void; onboarding: Resource<NodeOnboardingCapabilities> }) {
  const id = useId();
  const reason = outboundReason(onboarding);
  return (
    <div className="add-choose">
      <fieldset className="fld full types-f">
        <legend>Node</legend>
        <div className="types two">
          <label>
            <input type="radio" name={`${id}-node`} checked={!plan.fresh} onChange={() => onPlan({ ...plan, fresh: false })} />
            <span><b><Icon name="nodes" />A node that’s already running</b><small>It keeps its ID, data, accounts and tokens.</small></span>
          </label>
          <label>
            <input type="radio" name={`${id}-node`} checked={plan.fresh} onChange={() => onPlan({ ...plan, fresh: true })} />
            <span><b><Icon name="plus" />Deploy a new node</b><small>Docker, Portainer or a Linux VM, with credentials made in this browser.</small></span>
          </label>
        </div>
      </fieldset>
      <fieldset className="fld full types-f">
        <legend>Connection</legend>
        <div className="types two">
          <label>
            <input type="radio" name={`${id}-link`} checked={plan.link === 'direct'} onChange={() => onPlan({ ...plan, link: 'direct' })} />
            <span><b><Icon name="send" />This dashboard reaches the node</b><small>Its server connects to the node’s private address, so it needs a route to the node.</small></span>
          </label>
          <label>
            <input type="radio" name={`${id}-link`} checked={plan.link === 'outbound'} onChange={() => onPlan({ ...plan, link: 'outbound' })} />
            <span><b><Icon name="plug" />The node reaches this dashboard</b><small>{reason ?? 'The node connects out over HTTPS and needs no inbound port: for nodes this dashboard can’t reach, such as behind a client VPN.'}</small></span>
          </label>
        </div>
      </fieldset>
      {onboarding.error && !onboarding.data && (plan.fresh || plan.link === 'outbound') ? (
        <Banner tone="down" title="This dashboard’s node setup couldn’t be read" actions={<button type="button" className="btn sm" onClick={onboarding.reload}><Icon name="refresh" />Try again</button>}>
          {describeFailure(onboarding.error)} Adding a running node that this dashboard reaches still works.
        </Banner>
      ) : null}
    </div>
  );
}

/** Configuration help stays available before the dashboard has a connection listener. Uses the existing guide controls. */
export function OutboundDashboardSetup({ onboarding, onClose }: { onboarding: Resource<NodeOnboardingCapabilities>; onClose: () => void }) {
  const settings: GuideBlock = {
    key: 'dashboard-connection', kind: 'command', secret: false, title: 'Configure the existing dashboard service',
    text: `environment:
  CANARY_DASHBOARD_CONNECTION_URL: https://NODE_REACHABLE_HOST:18348
  CANARY_DASHBOARD_CONNECTION_TLS_MODE: auto
  CANARY_DASHBOARD_CONNECTION_HOST: 0.0.0.0
  CANARY_DASHBOARD_CONNECTION_PORT: "8081"
ports:
  - "DASHBOARD_BIND_IP:18348:8081"`,
  };
  return (
    <>
      <Banner tone="info" title="Set up this dashboard for incoming node connections" actions={
        <button type="button" className="btn sm" onClick={onboarding.reload} disabled={onboarding.loading}>
          <Icon name="refresh" />{onboarding.loading ? 'Checking dashboard setup…' : 'Check dashboard setup'}
        </button>
      }>
        {outboundReason(onboarding)} You can configure it now, then check again without closing this menu.
        {onboarding.error ? <p role="alert">{describeFailure(onboarding.error)}</p> : null}
      </Banner>
      <p className="sec">First update the stack running this dashboard. In Portainer, edit its existing dashboard service: <code>web</code> in the supplied templates, using the <code>ghcr.io/narehood/uptime-canary-web</code> image. The remote node is configured after this step.</p>
      <p className="modal-note">Replace NODE_REACHABLE_HOST with the hostname or IP your nodes use to reach this dashboard, and DASHBOARD_BIND_IP with an IP actually assigned to its Docker host. These may differ behind NAT or a VPN; forward TCP port 18348 to the published host port where needed. Merge these entries into the service’s existing environment and ports; keep its other settings, browser port, data and identity volumes.</p>
      <ol className="gsteps"><GuideStep n={1} block={settings} real={settings} onHandled={() => {}} /></ol>
      <p className="modal-note">Update the stack to recreate the dashboard container, then select Check dashboard setup. This uses built-in HTTPS on port 18348, separate from the browser dashboard and its sign-in proxy. The nodes must be able to reach that port without interactive sign-in.</p>
      <p className="modal-note">Once the dashboard reports its connection address, this menu lets you create an invitation and shows the Docker, Compose, Portainer or Linux VM steps for the node. No invitation is created until the dashboard is ready.</p>
      <div className="modal-foot"><span /><button type="button" className="btn" onClick={onClose}>Cancel</button></div>
    </>
  );
}

/** The published image and its platforms, as the dashboard's server reports them. */
function ImageLine({ capabilities }: { capabilities: NodeOnboardingCapabilities }) {
  const platforms = capabilities.containerPlatforms;
  return (
    <p className="small muted">
      Image <span className="mono">{imageReference(capabilities.imageTag)}</span>
      {platforms.length ? <>, published for {platforms.join(', ')}. The published image doesn’t cover other kinds of hosts.</> : '. This dashboard didn’t report which platforms it’s published for.'}
    </p>
  );
}

// ----- deployment text: platform tabs and numbered blocks

/** Saves text as a file through a temporary in-memory link; nothing is stored by this page. */
export function saveText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.rel = 'noopener';
  link.hidden = true;
  document.body.append(link);
  link.click();
  link.remove();
  // Long enough for the browser to start the download, then the in-memory copy goes.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function GuideStep({ n, block, real, onHandled, disabled = false }: { n: number; block: GuideBlock; real: GuideBlock; onHandled: () => void; disabled?: boolean }) {
  const [shown, setShown] = useState(false);
  const name = block.file ?? block.title;
  if (block.kind === 'note') {
    return (
      <li className="gstep note">
        <span className="gnum" aria-hidden="true">{n}</span>
        <div className="gbody"><b>{block.title}</b><p>{block.text}</p></div>
      </li>
    );
  }
  const reveal = block.secret ? (
    <button type="button" className="btn sm" disabled={disabled} aria-pressed={shown} aria-label={`${shown ? 'Hide' : 'Show'} private values in ${name}`}
      onClick={() => { if (!shown) onHandled(); setShown(!shown); }}>
      <Icon name={shown ? 'eyeOff' : 'eye'} />{shown ? 'Hide' : 'Show'}
    </button>
  ) : null;
  const download = block.file ? (
    <button type="button" className="btn sm" disabled={disabled} aria-label={`Download ${block.file}`} onClick={() => { saveText(block.file!, real.text); if (block.secret) onHandled(); }}>
      <Icon name="download" />Download
    </button>
  ) : null;
  if (block.variables) {
    const includesInvitation = block.variables.some(variable => variable.name === 'CANARY_DASHBOARD_ENROLL_TOKEN');
    // Portainer's basic editor: one entry per row, with separate name and value fields. Copy value takes the raw value
    // alone, never NAME= or a line break; the real values stay in these handlers and show only after Show.
    return (
      <li className={`gstep${block.secret ? ' secret' : ''}`}>
        <span className="gnum" aria-hidden="true">{n}</span>
        <div className="gbody">
          <div className="gstep-h">
            <b>{block.secret ? <Icon name="lock" className="inline" /> : null}{block.title}</b>
            {reveal}
          </div>
          <p>Each card below is one entry under Environment variables in Portainer: copy its Name into the entry’s name field and its Value into the value field.</p>
          <ul className="gvars">
            {block.variables.map((variable, index) => {
              const realVariable = real.variables?.[index];
              if (!realVariable) return null;
              return (
                <li key={variable.name} className="gvar">
                  <div className="gvar-f">
                    <span className="gvar-k">Name</span>
                    <code className="gvar-v">{variable.name}</code>
                    <CopyButton value={realVariable.name} label="Copy name" ariaLabel={`Copy name for ${variable.name}`} disabled={disabled} />
                  </div>
                  <div className="gvar-f">
                    <span className="gvar-k">Value</span>
                    <code className="gvar-v">{variable.secret && !shown ? variable.value : realVariable.value}</code>
                    <CopyButton value={realVariable.value} label="Copy value" ariaLabel={`Copy value for ${variable.name}`} onCopied={variable.secret && (!includesInvitation || variable.name === 'CANARY_DASHBOARD_ENROLL_TOKEN') ? onHandled : undefined} disabled={disabled} />
                  </div>
                </li>
              );
            })}
          </ul>
          <div className="gvars-alt">
            <small className="muted">Advanced: all entries at once as NAME=value lines, for Portainer’s Advanced mode or Load variables from .env file.</small>
            <span className="row-wrap">
              <CopyButton value={real.text} label="Copy .env" ariaLabel={`Copy ${name}`} onCopied={block.secret ? onHandled : undefined} disabled={disabled} />
              {download}
            </span>
          </div>
          {block.secret ? <small className="muted">{shown ? 'Private values are showing.' : 'Private values are hidden here.'} Copy value, Copy .env and Download include them. Keep them private and delete stray copies.</small> : null}
        </div>
      </li>
    );
  }
  return (
    <li className={`gstep${block.secret ? ' secret' : ''}`}>
      <span className="gnum" aria-hidden="true">{n}</span>
      <div className="gbody">
        <div className="gstep-h">
          <b>{block.secret ? <Icon name="lock" className="inline" /> : null}{block.title}</b>
          <span className="row-wrap">
            {reveal}
            <CopyButton value={real.text} ariaLabel={`Copy ${name}`} onCopied={block.secret ? onHandled : undefined} disabled={disabled} />
            {download}
          </span>
        </div>
        <pre className="json-pre gpre">{block.secret && !shown ? block.text : real.text}</pre>
        {block.secret ? <small className="muted">{shown ? 'Private values are showing.' : 'Private values are hidden here.'} Copy and Download include them. Keep the file private and delete stray copies.</small> : null}
      </div>
    </li>
  );
}

/**
 * The platform tabs and their steps. The preview is built with every private value masked; Copy and Download use the
 * real text. Changing tabs hides everything again.
 */
export function SetupGuide({ setup, platform, onPlatform, onHandled, disabledReason }: {
  setup: Omit<NodeSetup, 'platform'>; platform: Platform; onPlatform: (platform: Platform) => void; onHandled: () => void; disabledReason?: string;
}) {
  const id = useId();
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  let shown: GuideBlock[] = [];
  let real: GuideBlock[] = [];
  let problem: string | null = null;
  try {
    shown = setupGuide({ ...setup, platform }, false);
    real = disabledReason ? shown : setupGuide({ ...setup, platform }, true);
  } catch (error) {
    problem = error instanceof Error ? error.message : 'These settings can’t be written safely.';
  }
  const move = (event: KeyboardEvent<HTMLButtonElement>, index: number): void => {
    const last = PLATFORMS.length - 1;
    const next = event.key === 'ArrowRight' ? (index === last ? 0 : index + 1) : event.key === 'ArrowLeft' ? (index === 0 ? last : index - 1)
      : event.key === 'Home' ? 0 : event.key === 'End' ? last : null;
    if (next === null) return;
    event.preventDefault();
    onPlatform(PLATFORMS[next].value);
    tabs.current[next]?.focus();
  };
  return (
    <div className="guide" aria-describedby={disabledReason ? `${id}-warning` : undefined}>
      {disabledReason ? <p className="small fwarn" role="alert" id={`${id}-warning`}><Glyph tone="warn" />Complete the fields above first. {disabledReason} Show, Copy and Download are disabled until setup is ready. Preview only: do not deploy these settings.</p> : null}
      <div className="gtabs" role="tablist" aria-label="Where the node runs">
        {PLATFORMS.map((entry, index) => (
          <button key={entry.value} ref={element => { tabs.current[index] = element; }} type="button" role="tab" id={`${id}-t-${entry.value}`}
            aria-selected={entry.value === platform} aria-controls={`${id}-p`} tabIndex={entry.value === platform ? 0 : -1}
            onClick={() => onPlatform(entry.value)} onKeyDown={event => move(event, index)}>
            {entry.label}
          </button>
        ))}
      </div>
      <div className="gpanel" role="tabpanel" id={`${id}-p`} aria-labelledby={`${id}-t-${platform}`}>
        {problem ? <Banner tone="down" title="These settings can’t be written">{problem}</Banner> : (
          <ol className="gsteps">
            {/* Keyed by node ID too: values shown for one ID are hidden again once the steps are for another. */}
            {shown.map((block, index) => <GuideStep key={`${platform}-${setup.nodeId}-${block.key}`} n={index + 1} block={block} real={real[index]} onHandled={onHandled} disabled={!!disabledReason} />)}
          </ol>
        )}
      </div>
    </div>
  );
}

/** Keep every setup action visible but disabled until this new node's required fields are valid. */
export function NodeDeploymentPreview({ capabilities, loading = false, platform, onPlatform, reason, direct = false, nodeId = '', name = '' }: {
  capabilities?: NodeOnboardingCapabilities; loading?: boolean; platform: Platform; onPlatform: (platform: Platform) => void; reason: string; direct?: boolean; nodeId?: string; name?: string;
}) {
  return (
    <>
      {capabilities ? <ImageLine capabilities={capabilities} /> : loading ? <p className="small muted" role="status">Reading this dashboard’s image tag…</p> : null}
      <SetupGuide setup={{ nodeId: nodeIdProblem(nodeId) ? 'canary-new-node' : nodeId, name: labelProblem(name) ? 'New node' : name,
        imageTag: capabilities?.imageTag ?? 'IMAGE_TAG_FROM_DASHBOARD', secrets: { admin: 'ADMIN_TOKEN', cluster: 'CLUSTER_TOKEN' },
        ...(direct ? { direct: { url: 'https://node.example.invalid:3100', host: '0.0.0.0', listen: '0.0.0.0', port: 3100, allInterfaces: true } } : { outbound: { connectionUrl: capabilities?.connectionUrl ?? 'https://DASHBOARD_IP:18348', token: 'INVITATION_TOKEN', ...(capabilities?.connectionTlsFingerprint ? { tlsFingerprint: capabilities.connectionTlsFingerprint } : {}) } }) }}
        platform={platform} onPlatform={onPlatform} onHandled={() => {}} disabledReason={reason} />
    </>
  );
}

/** A new outbound node exports nothing until its one-time invitation can be included in the first deployment. */
export function NewOutboundDeploy({ capabilities, loading = false, platform, onPlatform, nodeId, name, secrets, taken, addressError }: {
  capabilities?: NodeOnboardingCapabilities; loading?: boolean; platform: Platform; onPlatform: (platform: Platform) => void; nodeId: string; name: string;
  secrets: NodeSecrets | null; earlier?: string | null; taken: Taken; onUse: (nodeId: string) => void; addressError?: string;
}) {
  const pair = secretsFor(secrets, nodeId);
  const reason = [labelProblem(name), nodeIdProblem(nodeId, taken), addressError, !capabilities ? (loading ? 'Wait for the dashboard’s image tag to load.' : 'This dashboard’s setup is unavailable. Use Try again above.') : null,
    capabilities && !capabilities.outboundEnabled ? 'Configure the dashboard’s node-connection listener below, then choose Check dashboard setup.' : null, !pair ? NO_TOKENS : null].filter(Boolean).join(' ');
  return <NodeDeploymentPreview capabilities={capabilities} loading={loading} platform={platform} onPlatform={onPlatform}
    reason={reason || 'Choose Generate setup below to include the one-time invitation in a complete first-start deployment.'} nodeId={nodeId} name={name} />;
}

// ----- a new node that this dashboard's server reaches: deploy it, then verify it with its new administrator token

export interface NewNodeDraft { label: string; nodeId: string; address: string; bindAddress?: string; dashboardAddress?: string; platform: Platform }

/** Says that the tokens shown are this node ID's own, after a pair was used for another ID in the same dialog. */
function TokensNote({ nodeId, earlier }: { nodeId: string; earlier?: string | null }) {
  if (!earlier || earlier === nodeId) return null;
  return (
    <p className="small muted"><Icon name="key" className="inline" />Tokens belong to one node: the ones below are for <span className="mono">{nodeId}</span>. Those shown, copied, saved or sent for <span className="mono">{earlier}</span> stay with that ID.</p>
  );
}

/** Never enable setup actions using missing credentials or ones belonging to another node ID. */
const NO_TOKENS = 'Usable settings require the new node’s own administrator and cluster tokens, and this page has none for it. Try again above, or add a node that’s already running instead.';

/**
 * Where the steps open the node's port on its host: the publish or listen address field, never the Node URL. In a
 * container the node itself always listens on 0.0.0.0; the address picks the host side of Docker's port mapping.
 */
function BindingNote({ target, platform }: { target: DirectTarget; platform: Platform }) {
  const vm = platform === 'vm';
  const where = vm
    ? `The service sets CANARY_HOST="${target.listen}", so the node itself listens there.`
    : `Inside the container the node still listens on 0.0.0.0 (CANARY_HOST); this address only picks the host side of the published port.`;
  if (target.allInterfaces) {
    const family = target.host.startsWith('[') ? 'IPv6' : 'IPv4';
    return (
      <p className="small fwarn"><Glyph tone="warn" />
        <span>The commands publish port {target.port} on every {family} interface of the node host ({target.listen}). {where}{vm ? ' A wildcard listener on a Linux VM may accept the other IP version too.' : ''} {bindingAccessNote(platform)}</span>
      </p>
    );
  }
  return (
    <p className="small muted">
      The commands publish port {target.port} on {target.listen} only, so the node host itself must have that address. {where} If it belongs to a VPN that comes up after {vm ? 'the service' : 'Docker'} starts, the port can’t open on it at boot; then use 0.0.0.0. {bindingAccessNote(platform)}
    </p>
  );
}

export function NewNodeDeploy({ draft, onDraft, secrets, earlier, onboarding, taken, onNext, onUse, onCancel }: {
  draft: NewNodeDraft; onDraft: (draft: NewNodeDraft) => void; secrets: NodeSecrets | null; earlier?: string | null; onboarding: Resource<NodeOnboardingCapabilities>; taken: Taken;
  onNext: (target: DirectTarget) => void; onUse: (nodeId: string) => void; onCancel: () => void;
}) {
  const [show, setShow] = useState(false);
  const [summaryRef, failed] = useSummaryFocus();
  const capabilities = onboarding.data;
  // Only a pair that may serve this node ID; one already used for another ID is never shown for this one.
  const pair = secretsFor(secrets, draft.nodeId);
  const binding = localBinding(draft.bindAddress ?? '0.0.0.0');
  const remote = draft.address.trim() ? directTarget(draft.address) : 'Enter the node’s URL, not the dashboard’s URL.';
  const target = draft.address.trim() ? directTarget(draft.address, draft.bindAddress) : remote;
  const errors = { label: labelProblem(draft.label), nodeId: nodeIdProblem(draft.nodeId, taken), address: typeof remote === 'string' ? remote : undefined, bindAddress: typeof binding === 'string' ? binding : undefined };
  const live = (value: string, error?: string): string | undefined => show || value ? error : undefined;
  const ready = typeof target !== 'string' && !errors.label && !errors.nodeId && !errors.bindAddress;
  // An address without a port means 443 (or 80), which the steps then use: said, with the address on port 3100.
  const portNote = typeof target === 'string' ? null : schemePortNote(target, draft.platform);
  const set = (change: Partial<NewNodeDraft>): void => onDraft({ ...draft, ...change });

  const next = (event: FormEvent): void => {
    event.preventDefault();
    setShow(true);
    if (typeof target === 'string' || !ready || !capabilities || !pair) { failed(); return; }
    onNext(target);
  };

  return (
    <form className="connect-form" onSubmit={next} noValidate>
      <p className="sec">The new node gets its own administrator and cluster tokens, made in this browser. This page keeps them until the dialog closes. It sends only the administrator token, to this dashboard’s server, when you verify the node.</p>
      <ErrorSummary ref={summaryRef} title="Check these fields" errors={show ? Object.values(errors).filter(Boolean) as string[] : []} />
      <TextField label="Name on this dashboard" value={draft.label} onChange={label => set({ label })} error={live(draft.label, errors.label)} maxLength={128} autoFocus
        hint="Every account on this dashboard sees it. It also becomes the node’s own name." />
      <TextField label="Node ID" mono value={draft.nodeId} onChange={nodeId => set({ nodeId })} error={live(draft.nodeId, errors.nodeId)} maxLength={128} placeholder="canary-office"
        hint="New and unique, such as canary-office. It can’t change later; never reuse a live node’s ID." />
      <TextField label="Node URL" type="url" inputMode="url" mono value={draft.address} onChange={address => set({ address })} error={live(draft.address, errors.address)}
        placeholder="https://100.79.12.4:3100" hint="The new node’s address as the dashboard’s server reaches it, such as the node host’s LAN, NetBird or Tailscale IP, with the port the steps publish. It’s never this dashboard’s own address. Verify uses this URL unchanged; it doesn’t choose which address Docker or the node binds to. With https:// the node encrypts the connection itself; use http:// only on a network that encrypts it already." />
      <TextField label="Publish or listen address on the node host" mono value={draft.bindAddress ?? '0.0.0.0'} onChange={bindAddress => set({ bindAddress })} error={live(draft.bindAddress ?? '0.0.0.0', errors.bindAddress)}
        placeholder="0.0.0.0" maxLength={64} hint={`Separate from the Node URL. 0.0.0.0, the default, means all of the host’s IPv4 interfaces. To accept connections on one interface only, enter an IP address the Docker or VM host itself has, or :: for all IPv6 interfaces. Not a VPN, NAT or port-forward address that belongs to another machine, and no URL, host name or port. ${bindingAccessNote(draft.platform)}`} />
      {capabilities && typeof target !== 'string' && ready && pair ? (
        <>
          <ImageLine capabilities={capabilities} />
          <BindingNote target={target} platform={draft.platform} />
          {portNote ? <p className="small muted">{portNote}</p> : null}
          {directTls(target)
            ? <p className="small muted"><Icon name="lock" className="inline" />HTTPS: the node serves TLS 1.3 itself (CANARY_TLS_MODE=auto) with a self-signed certificate it makes when it first starts, valid for ten years. No certificate authority vouches for it, so the next step asks for its fingerprint, from the line “{TLS_LOG_LINE}” in the node’s log. The dashboard’s server then accepts only that certificate, and sends the node no credential until it checks out.</p>
            : <p className="small fwarn"><Glyph tone="warn" />Plain HTTP: traffic between the dashboard’s server and this node isn’t encrypted by Uptime Canary. Use an address on a network that encrypts it, such as NetBird or Tailscale.</p>}
          <TokensNote nodeId={draft.nodeId} earlier={earlier} />
          <SetupGuide setup={{ nodeId: draft.nodeId, name: draft.label, imageTag: capabilities.imageTag, secrets: pair, direct: target }} platform={draft.platform}
            onPlatform={platform => set({ platform })} onHandled={() => onUse(draft.nodeId)} />
          {draft.platform === 'vm' && capabilities.nativeDownloadAvailable ? <p className="small muted">This dashboard reports native downloads, but gives this page no address for them, so the steps above build the node instead.</p> : null}
        </>
      ) : <NodeDeploymentPreview direct capabilities={capabilities} loading={onboarding.loading} platform={draft.platform} onPlatform={platform => set({ platform })} nodeId={draft.nodeId} name={draft.label}
        reason={[...Object.values(errors), !capabilities ? (onboarding.loading ? 'Wait for the dashboard’s image tag to load.' : 'This dashboard’s setup is unavailable. Use Try again above.') : null, !pair ? NO_TOKENS : null].filter(Boolean).join(' ')} />}
      <div className="modal-foot">
        <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
        <span className="row-wrap">
          <button type="button" className="btn" onClick={onCancel}>Cancel</button>
          <button type="submit" className="btn primary"><Icon name="chevRight" />Next: verify the node</button>
        </span>
      </div>
    </form>
  );
}

// ----- a node that reaches this dashboard: invitation, settings, first-pair confirmation

const withoutToken = ({ id, label, nodeId, connectionUrl, connectionTlsFingerprint, expiresAt, state, registrationId, node }: NodeInvitationCreated): NodeInvitation =>
  ({ id, label, nodeId, connectionUrl, ...(connectionTlsFingerprint ? { connectionTlsFingerprint } : {}), expiresAt, state, ...(registrationId ? { registrationId } : {}), ...(node ? { node } : {}) });

/** The line the dashboard's server logs when its node-connection listener serves its own certificate (dashboard_server.go). */
export const DASHBOARD_TLS_LOG_LINE = 'Dashboard connection TLS certificate fingerprint:';

/**
 * The dashboard's own node-connection certificate, when its listener terminates TLS itself: the settings pin it on the
 * node with CANARY_DASHBOARD_TLS_FINGERPRINT. It's the dashboard's certificate, not the node's, and not the connection
 * fingerprint compared when the node connects. Without it, the node checks the address's certificate as usual.
 */
function ConnectionTrust({ pin }: { pin?: string }) {
  if (!pin) return <p className="small muted">Set by this dashboard’s deployment. The node must reach it over HTTPS and trust its certificate; nothing here turns certificate checks off.</p>;
  return (
    <p className="small muted">
      Set by this dashboard’s deployment, whose server serves this address with a certificate of its own. The node’s settings below pin exactly that certificate with CANARY_DASHBOARD_TLS_FINGERPRINT, so it connects only to this dashboard; nothing here turns certificate checks off. This page got the fingerprint from this dashboard’s server over your sign-in. If you reached this page over a network you don’t trust, first compare it with the line “{DASHBOARD_TLS_LOG_LINE}” in the dashboard server’s own log. It names the dashboard’s certificate: it isn’t the node’s connection fingerprint you compare when the node connects.
    </p>
  );
}

/** What the connected node proved: its ID, name, version and the fingerprint of its connection credential. */
export function PairingFacts({ invitation }: { invitation: NodeInvitation }) {
  const node = invitation.node;
  if (!node) return null;
  return (
    <dl className="kv pairing-kv">
      <dt>Node ID</dt><dd><span className="mono">{node.id}</span>{node.id === invitation.nodeId ? <span className="muted"> · the ID this invitation expects</span> : null}</dd>
      {node.name !== node.id ? <><dt>Its name</dt><dd>{node.name}</dd></> : null}
      <dt>Version</dt><dd>{node.version || <span className="muted">Not reported</span>}{node.sourceCommit ? <span className="mono muted"> · {node.sourceCommit.slice(0, 12)}</span> : null}</dd>
      <dt>Fingerprint</dt><dd className="mono fp">{node.fingerprint}</dd>
    </dl>
  );
}

function addedNotice(node: RegisteredNode): string {
  return node.state === 'enrolling'
    ? `${node.label} is listed, but setting up this dashboard’s access on it hasn’t finished. It shows as “Enrollment unfinished”, with Retry.`
    : `${node.label} added. The dashboard’s server reads it over the node’s own connection.`;
}

/** The label of the explicit check that Add waits for. */
const FINGERPRINT_CHECK = 'This fingerprint matches the one in the node’s log';

/**
 * The confirmation card's text and buttons, shared by the wizard and the Review dialog. Add stays off until the
 * administrator ticks that the fingerprint matches the node's own log, so a node is never added on its ID alone. Callers
 * key the card by invitation and fingerprint, so a different fingerprint starts unticked.
 */
function PairingCard({ invitation, busy, onConfirm, onReject }: { invitation: NodeInvitation; busy: boolean; onConfirm: () => void; onReject: () => void }) {
  const problem = pairingProblem(invitation);
  const [compared, setCompared] = useState(false);
  return (
    <section className="pairing" aria-label="Confirm the first connection">
      <h3><Glyph tone="warn" />Is this your node?</h3>
      {problem ? <Banner tone="down" title="Don’t add this node">{problem}</Banner> : <PairingFacts invitation={invitation} />}
      <p className="small muted">The node prints the same fingerprint in its log when it connects. Compare them before you add it. Until you do, the dashboard reads nothing from the node and sets up no access there. It shows no source address, because it can’t prove one.</p>
      {problem ? null : <CheckField label={FINGERPRINT_CHECK} hint="Its log line starts with “Node connection fingerprint”." checked={compared} onChange={setCompared} disabled={busy} />}
      <div className="row-wrap">
        {problem ? null : <button type="button" className="btn primary" onClick={onConfirm} disabled={busy || !compared}><Icon name="check" />{busy ? 'Adding…' : `Add “${invitation.label}”`}</button>}
        <button type="button" className="btn danger" onClick={onReject} disabled={busy}><Icon name="close" />Not my node: cancel invitation</button>
      </div>
    </section>
  );
}

/** Polls one invitation's status while it can still change. `nonce` asks again at once. */
function useInvitationStatus(id: string | null, stop: boolean, nonce: number): { status: NodeInvitation | null; failure: unknown; loading: boolean } {
  const { api } = useCanary();
  const [status, setStatus] = useState<NodeInvitation | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    if (!id || stop) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    const poll = async (): Promise<void> => {
      setLoading(true);
      try {
        const next = await api.invitation(id, controller.signal);
        if (controller.signal.aborted) return;
        failures = 0;
        setStatus(next);
        setFailure(null);
        if (next.state === 'expired' || next.state === 'confirmed') return;
        timer = setTimeout(() => { void poll(); }, next.state === 'connected' ? 5000 : 2000);
      } catch (error) {
        if (controller.signal.aborted || isAbort(error)) return;
        if (error instanceof ApiFailure && error.status === 404) { setFailure(error); return; }
        failures++;
        if (failures >= 2) setFailure(error);
        timer = setTimeout(() => { void poll(); }, Math.min(30_000, 2000 * 2 ** failures));
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    };
    void poll();
    return () => { controller.abort(); if (timer) clearTimeout(timer); };
  }, [api, id, stop, nonce]);
  return { status, failure, loading };
}

export function OutboundNode({ fresh, draft, onDraft, onboarding, secrets, earlier, onUse, taken, platform: draftPlatform, onPlatform: onDraftPlatform, onClose, onRequestClose, onLock, onBusy, guard, onTakenChanged }: {
  fresh: boolean; onboarding: Resource<NodeOnboardingCapabilities>; secrets: NodeSecrets | null; earlier?: string | null; onUse: (nodeId: string) => void; taken: Taken; platform: Platform;
  draft: NewNodeDraft; onDraft: (draft: NewNodeDraft) => void;
  onPlatform: (platform: Platform) => void; onClose: () => void; onRequestClose: () => void; onLock: (locked: boolean) => void; onBusy: (busy: boolean) => void;
  guard: RefObject<(() => boolean) | null>; onTakenChanged: () => void;
}) {
  const { api, registry, connection, stillCurrent } = useCanary();
  const capabilities = onboarding.data;
  const [existingLabel, setExistingLabel] = useState('');
  const [existingNodeId, setExistingNodeId] = useState('');
  const [existingPlatform, setExistingPlatform] = useState<Platform>('docker');
  const [existingConnectionAddress, setExistingConnectionAddress] = useState<string | null>(null);
  const addressInput = (fresh ? draft.dashboardAddress : existingConnectionAddress) ?? capabilities?.connectionUrl ?? '';
  const setConnectionAddress = (value: string): void => { if (fresh) onDraft({ ...draft, dashboardAddress: value }); else setExistingConnectionAddress(value); };
  const connectionUrl = outboundConnectionAddress(addressInput, capabilities?.connectionUrl ?? '');
  const platform = fresh ? draftPlatform : existingPlatform;
  const onPlatform = (platform: Platform): void => { if (fresh) onDraftPlatform(platform); else setExistingPlatform(platform); };
  const label = fresh ? draft.label : existingLabel;
  const nodeId = fresh ? draft.nodeId : existingNodeId;
  const setLabel = (label: string): void => { if (fresh) onDraft({ ...draft, label }); else setExistingLabel(label); };
  const setNodeId = (nodeId: string): void => { if (fresh) onDraft({ ...draft, nodeId }); else setExistingNodeId(nodeId); };
  const [show, setShow] = useState(false);
  useEffect(() => setShow(false), [fresh]);
  const [busy, setBusy] = useState<null | 'create' | 'cancel' | 'confirm'>(null);
  const [failure, setFailure] = useState<ChangeFailure | null>(null);
  const [invitation, setInvitation] = useState<NodeInvitation | null>(null);
  const [invitationBootstrap, setInvitationBootstrap] = useState<NodeSecrets | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [handled, setHandled] = useState(false);
  const [asking, setAsking] = useState<null | 'close' | 'cancel'>(null);
  const [added, setAdded] = useState<RegisteredNode | null>(null);
  const [nonce, setNonce] = useState(0);
  const [summaryRef, failed] = useSummaryFocus();
  const attempt = useRef<AbortController | null>(null);
  const okRef = useRef<HTMLElement>(null);
  const now = useNow(1000) + (connection.issuerSkew ?? 0);
  const polled = useInvitationStatus(invitation?.id ?? null, added !== null, nonce);
  const current = polled.status && polled.status.id === invitation?.id ? polled.status : invitation;

  useEffect(() => () => { attempt.current?.abort(); guard.current = null; }, [guard]);
  // While a create waits, the dashboard's server may already have saved the invitation, whose token only its answer
  // carries. Closing, Escape or the other choices would unmount this and drop that answer, so they're all off until it
  // comes back; Stop is the one way out, and it says the invitation may exist.
  useEffect(() => { onLock(invitation !== null || busy === 'create'); }, [invitation, busy, onLock]);
  useEffect(() => { onBusy(busy !== null); }, [busy, onBusy]);
  useEffect(() => { if (added) okRef.current?.focus(); }, [added]);
  // Closing with an invitation whose token wasn't shown, copied or saved asks first: the token can't be shown again.
  guard.current = () => {
    if (busy !== null) return false;
    const settled = current?.state === 'confirmed' || current?.state === 'expired' || invitationGone(polled.failure);
    if (!invitation || !token || handled || added || settled) return true;
    setAsking('close');
    return false;
  };

  const addressProblem = !connectionUrl ? 'Enter the dashboard’s reachable IP, hostname or full HTTPS connection address. IPv4 needs four decimal fields without leading zeros.'
    : capabilities?.connectionUrl && !capabilities.connectionTlsFingerprint && connectionUrl !== capabilities.connectionUrl
      ? 'A different address requires this dashboard’s certificate fingerprint. Use the configured address, or enable native TLS on the dashboard’s node-connection listener first.' : undefined;
  const errors = { label: labelProblem(label), nodeId: nodeIdProblem(nodeId, taken), connectionUrl: addressProblem };
  const live = (value: string, error?: string): string | undefined => show || value ? error : undefined;
  const pair = secretsFor(secrets, nodeId);

  const create = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy || !capabilities?.outboundEnabled || !capabilities.connectionUrl) return;
    setShow(true); setFailure(null);
    if (errors.label || errors.nodeId || errors.connectionUrl || !connectionUrl) { failed(); return; }
    // A new node's settings need its own tokens; without them nothing is created (the dialog says why above).
    if (fresh && !pair) { failed(); return; }
    const controller = new AbortController();
    attempt.current = controller;
    setBusy('create');
    try {
      // The default uses the server's exact configured origin, including its
      // IPv6 spelling. Only an actual alternate destination needs an override.
      const result = await api.createInvitation({ label, nodeId, ...(connectionUrl === capabilities.connectionUrl ? {} : { connectionUrl }) }, controller.signal);
      if (!stillCurrent()) return;
      setInvitation(withoutToken(result));
      setInvitationBootstrap(fresh && pair ? { ...pair, nodeId } : null);
      setToken(result.token);
      setHandled(false); setShow(false);
      // From here the new node's tokens belong to this invitation's node ID (a pair used for another ID is replaced).
      if (fresh) onUse(result.nodeId);
      onTakenChanged();
    } catch (error) {
      if (!stillCurrent()) return;
      onTakenChanged();
      setFailure(isAbort(error)
        ? { title: 'Stopped', text: 'The dashboard’s server may have made the invitation anyway. If it’s listed under “Waiting to connect”, its token can’t be shown again: cancel it there and make another.', unknown: true, reload: true }
        : invitationFailure(error, 'create'));
      failed();
    } finally {
      if (attempt.current === controller) attempt.current = null;
      setBusy(null);
    }
  };

  const cancel = async (): Promise<void> => {
    if (!invitation || busy) return;
    setBusy('cancel'); setFailure(null);
    try {
      await api.cancelInvitation(invitation.id);
      if (!stillCurrent()) return;
      setToken(null);
      onTakenChanged();
      notify(`Cancelled the invitation for ${invitation.label}. This dashboard set up no access on that node. ${REINVITE_DEPLOYED}`);
      onClose();
    } catch (error) {
      if (!stillCurrent()) return;
      const next = invitationFailure(error, 'cancel');
      if (next.reload) { onTakenChanged(); registry.reload(); setNonce(n => n + 1); }
      setFailure(next); setAsking(null);
    } finally {
      if (stillCurrent()) setBusy(null);
    }
  };

  const confirm = async (): Promise<void> => {
    if (!invitation || busy) return;
    setBusy('confirm'); setFailure(null);
    try {
      const node = await api.confirmInvitation(invitation.id);
      if (!stillCurrent()) return;
      setToken(null);
      setAdded(node);
      registry.reload(); onTakenChanged();
      notify(addedNotice(node));
    } catch (error) {
      if (!stillCurrent()) return;
      const next = invitationFailure(error, 'confirm');
      if (next.reload) { registry.reload(); onTakenChanged(); setNonce(n => n + 1); }
      setFailure(next);
    } finally {
      if (stillCurrent()) setBusy(null);
    }
  };

  const identityFields = <>
    <TextField label="Name on this dashboard" value={label} onChange={setLabel} error={live(label, errors.label)} maxLength={128} disabled={busy !== null} autoFocus
      hint={fresh ? 'Every account on this dashboard sees it. It also becomes the node’s own name.' : 'Every account on this dashboard sees it. The node keeps its own name.'} />
    <TextField label="Node ID" mono value={nodeId} onChange={setNodeId} error={live(nodeId, errors.nodeId)} maxLength={128} disabled={busy !== null} placeholder="canary-office"
      hint={fresh ? 'New and unique, such as canary-office. It can’t change later; never reuse a live node’s ID.' : 'Its current CANARY_NODE_ID. The dashboard refuses a node that connects with any other ID.'} />
    <TextField label="Dashboard IP or HTTPS address" mono value={addressInput} onChange={setConnectionAddress} error={live(addressInput, errors.connectionUrl)} maxLength={2048} disabled={busy !== null}
      placeholder="https://192.168.10.10:18348" hint={`The address this node can reach. An IP or hostname keeps the configured connection port; a full HTTPS address can specify another port. It must route to this dashboard’s node-connection listener. A different address requires this dashboard’s certificate fingerprint from native TLS. Configured address: ${capabilities?.connectionUrl ?? 'not configured yet'}.`} />
  </>;
  const deployment = fresh ? <NewOutboundDeploy capabilities={capabilities} loading={onboarding.loading} platform={platform} onPlatform={onPlatform} nodeId={nodeId} name={label}
    secrets={secrets} earlier={earlier} taken={taken} onUse={onUse} addressError={errors.connectionUrl} /> : null;

  if (!capabilities?.outboundEnabled || !capabilities.connectionUrl) {
    return <>
      {fresh && !invitation ? <form className="connect-form" onSubmit={event => event.preventDefault()} noValidate>{identityFields}{deployment}</form> : null}
      <OutboundDashboardSetup onboarding={onboarding} onClose={onRequestClose} />
    </>;
  }

  const failureBox = failure ? <div className="errsum" role="alert" ref={summaryRef} tabIndex={-1}><Glyph tone="down" /><div><b>{failure.title}</b><p>{failure.text}</p></div></div> : null;

  if (added) {
    return (
      <>
        <div className="banner b-ok" role="status">
          <Glyph tone="up" />
          <div>
            <b className="bt" tabIndex={-1} ref={okRef}>{added.state === 'enrolling' ? `${added.label} is listed` : `${added.label} added`}</b>
            <div className="bb">{added.state === 'enrolling'
              ? 'Setting up this dashboard’s access on it hasn’t finished. It shows as “Enrollment unfinished”, and the dashboard’s server keeps trying while the node is connected.'
              : 'The dashboard’s server reads it over the node’s own connection, with access of its own for each dashboard role.'}</div>
          </div>
        </div>
        <p className="modal-note">Now remove CANARY_DASHBOARD_ENROLL_TOKEN from the node’s settings: it works only once. The node keeps its connection credential in its identity {platform === 'vm' ? 'directory' : 'volume'}, so keep that.</p>
        <div className="modal-foot"><span /><button type="button" className="btn primary" onClick={onClose}>Done</button></div>
      </>
    );
  }

  if (!invitation || !current) {
    return (
      <form className="connect-form" onSubmit={event => { void create(event); }} noValidate aria-busy={busy === 'create'}>
        <p className="sec">{fresh ? 'The new node' : 'The node'} opens an HTTPS connection to this dashboard’s node-connection address and keeps it open; this dashboard never dials it. No inbound port or firewall opening is needed on the node.</p>
        <dl className="kv">
          <dt>Node-connection address</dt><dd className="mono">{capabilities.connectionUrl}</dd>
          {capabilities.connectionTlsFingerprint ? <><dt>Its certificate fingerprint</dt><dd className="mono">{capabilities.connectionTlsFingerprint}</dd></> : null}
        </dl>
        <ConnectionTrust pin={capabilities.connectionTlsFingerprint} />
        {failure ? failureBox : <ErrorSummary ref={summaryRef} title="Check these fields" errors={show ? Object.values(errors).filter(Boolean) as string[] : []} />}
        {identityFields}
        {deployment}
        {fresh ? <p className="modal-note">Generate one complete deployment, then paste its commands or stack YAML and environment settings and start the node. If it is already deployed, choose “A node that’s already running” to preserve its credentials and identity.</p> : null}
        <div className="modal-foot">
          <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
          <span className="row-wrap">
            {busy === 'create'
              ? <button type="button" className="btn" onClick={() => attempt.current?.abort()}>Stop</button>
              : <button type="button" className="btn" onClick={onRequestClose}>Cancel</button>}
            <button type="submit" className="btn primary" disabled={busy !== null || Boolean(errors.connectionUrl) || (fresh && (!pair || Object.values(errors).some(Boolean)))}><Icon name="key" />{busy === 'create' ? 'Creating…' : fresh ? 'Generate setup' : 'Create invitation'}</button>
          </span>
        </div>
      </form>
    );
  }

  const info = invitationInfo(current, now);
  const left = current.expiresAt - now;
  const gone = invitationGone(polled.failure);
  let status: ReactNode;
  if (gone) status = <Banner tone="warn" title="This invitation is no longer on this dashboard">It may have been cancelled meanwhile. Nothing was added.</Banner>;
  else if (current.state === 'waiting') {
    status = (
      <div className="wiz-status">
        <Spinner label={`Waiting for ${current.nodeId} to connect…`} />
        <span className="small muted">{info.text} Pairing only adds it to this dashboard; it doesn’t change what the node monitors.</span>
        <span className="small muted">{connectionWaitingHint(imageReference(capabilities.imageTag), current.connectionUrl)}</span>
      </div>
    );
  } else if (current.state === 'connected') status = <PairingCard key={`${current.id}|${current.node?.fingerprint ?? ''}`} invitation={current} busy={busy !== null} onConfirm={() => { void confirm(); }} onReject={() => setAsking('cancel')} />;
  else if (current.state === 'expired') status = <Banner tone="warn" title="This invitation expired">Nothing was added. Cancel it, then make another one. {REINVITE_DEPLOYED}</Banner>;
  else status = <Banner tone="info" title="Already added">Another administrator confirmed this node; it’s on the node list.</Banner>;

  return (
    <>
      <div className="banner b-ok" role="status">
        <Glyph tone="up" />
        <div>
          <b className="bt">Invitation ready for <span className="mono">{current.nodeId}</span></b>
          <div className="bb">{token ? 'Its one-time token is in the private settings below, and this is the only time it’s shown.' : 'Its token was shown when it was made.'} It works once{left > 0 ? `, and ends in ${elapsed(left)}` : ''}.</div>
        </div>
      </div>
      <dl className="kv">
        <dt>Name on this dashboard</dt><dd>{current.label}</dd>
        <dt>Node-connection address</dt><dd className="mono">{current.connectionUrl}</dd>
        {current.connectionTlsFingerprint ? <><dt>Its certificate fingerprint</dt><dd className="mono">{current.connectionTlsFingerprint}</dd></> : null}
      </dl>
      {token && current.state !== 'confirmed' ? (
          <>
            <ImageLine capabilities={capabilities} />
            {fresh ? <p className="modal-note">This is the complete configuration for a new node’s first start, including its own credentials and this invitation. Paste the commands or stack YAML and environment settings together, then start it. Keep its data and identity storage.</p> : null}
            {/* The invitation's own address and pin, which the dashboard's server bound to it; never the page's own. */}
            {fresh && !secretsFor(invitationBootstrap, current.nodeId) ? <Banner tone="down" title="This new node’s setup is unavailable">Cancel this invitation and generate it again, or choose an already-running node if it was previously deployed.</Banner> : <SetupGuide setup={{ nodeId: current.nodeId, name: current.label, imageTag: capabilities.imageTag, secrets: fresh ? secretsFor(invitationBootstrap, current.nodeId) : null,
              outbound: { connectionUrl: current.connectionUrl, token, ...(current.connectionTlsFingerprint ? { tlsFingerprint: current.connectionTlsFingerprint } : {}) } }}
              platform={platform} onPlatform={onPlatform} onHandled={() => { setHandled(true); }} />}
          </>
        ) : null}
      <div aria-live="polite">{status}</div>
      {!gone && (current.state === 'waiting' || current.state === 'connected') ? (
        <button type="button" className="btn sm" onClick={() => setNonce(n => n + 1)} disabled={busy !== null || polled.loading}>
          <Icon name="refresh" />{polled.loading ? 'Checking for connection…' : 'Check for connection'}
        </button>
      ) : null}
      {polled.failure && !gone ? (
        <p className="small fwarn" role="alert"><Glyph tone="warn" />Can’t read the invitation’s status: {describeFailure(polled.failure)} <button type="button" className="linkbtn" onClick={() => setNonce(n => n + 1)}>Try now</button></p>
      ) : null}
      {asking === 'close' ? (
        <Banner tone="warn" title="Close without saving the invitation?" actions={<>
          <button type="button" className="btn sm" onClick={onClose}>Close anyway</button>
          <button type="button" className="btn sm" onClick={() => setAsking(null)}>Keep it open</button>
        </>}>
          Its token is shown only in this dialog. The invitation stays under “Waiting to connect” until it expires; cancel it there and make another if you need its settings.
        </Banner>
      ) : null}
      {asking === 'cancel' ? (
        <Banner tone="warn" title="Cancel this invitation?" actions={<>
          <button type="button" className="btn sm danger-solid" onClick={() => { void cancel(); }} disabled={busy !== null}>{busy === 'cancel' ? 'Cancelling…' : 'Cancel invitation'}</button>
          <button type="button" className="btn sm" onClick={() => setAsking(null)} disabled={busy !== null}>Keep it</button>
        </>}>
          The node can’t use it afterwards. This dashboard set up no access on the node, so nothing there needs undoing.
        </Banner>
      ) : null}
      {failureBox}
      <div className="modal-foot">
        <span className="small muted"><Icon name="lock" className="inline" />Administrators only</span>
        <span className="row-wrap">
          {current.state !== 'confirmed' && !gone && asking !== 'cancel' ? <button type="button" className="btn danger" onClick={() => setAsking('cancel')} disabled={busy !== null}>Cancel invitation</button> : null}
          <button type="button" className="btn" onClick={onRequestClose} disabled={busy !== null}>Close</button>
        </span>
      </div>
    </>
  );
}

// ----- the Nodes page: invitations waiting for their node (administrators only)

/** Shown only while an administrator has invitations that aren't confirmed yet. Renders nothing while loading. */
export function PendingNodesCard() {
  const invitations = useInvitations(10_000);
  const { connection } = useCanary();
  const now = useNow(5000) + (connection.issuerSkew ?? 0);
  const [reviewing, setReviewing] = useState<NodeInvitation | null>(null);
  const [cancelling, setCancelling] = useState<NodeInvitation | null>(null);
  if (invitations.error && !invitations.data) {
    // A dashboard without node connections answers 404: there's nothing to show.
    if (invitations.error instanceof ApiFailure && invitations.error.status === 404) return null;
    return <p className="small muted" role="status">Invitations waiting to connect couldn’t be loaded. {describeFailure(invitations.error)}</p>;
  }
  const open = (invitations.data ?? []).filter(invitation => invitation.state !== 'confirmed');
  if (!open.length) return null;
  return (
    <Card title="Waiting to connect" hint="Invitations for nodes that connect to this dashboard themselves. Only administrators see them. A node joins the list once an administrator confirms its first connection.">
      <div className="peer-pick invites">
        <ul>
          {open.map(invitation => {
            const info = invitationInfo(invitation, now);
            return (
              <li key={invitation.id}>
                <Glyph tone={info.tone} label={info.label} />
                <span className="grow">
                  <b>{invitation.label}</b> <span className="mono muted">{invitation.nodeId}</span>
                  <span className="sub">{info.label}. {info.text}</span>
                </span>
                <span className="row-wrap">
                  {invitation.state === 'connected' ? <button type="button" className="btn sm primary" onClick={() => setReviewing(invitation)} aria-label={`Review ${invitation.label}`}><Icon name="eye" />Review</button> : null}
                  <button type="button" className="btn sm" onClick={() => setCancelling(invitation)} aria-label={`Cancel invitation for ${invitation.label}`}>Cancel</button>
                </span>
              </li>
            );
          })}
        </ul>
      </div>
      {reviewing ? <PairingDialog key={reviewing.id} invitation={reviewing} onClose={() => setReviewing(null)} onChanged={invitations.reload} /> : null}
      {cancelling ? <CancelInvitationDialog key={cancelling.id} invitation={cancelling} onClose={() => setCancelling(null)} onChanged={invitations.reload} /> : null}
    </Card>
  );
}

/** Review a connected node from the list: the same facts and choices as the wizard's last step. */
export function PairingDialog({ invitation: opened, onClose, onChanged }: { invitation: NodeInvitation; onClose: () => void; onChanged: () => void }) {
  const { api, registry, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ChangeFailure | null>(null);
  const [rejecting, setRejecting] = useState(false);
  const [nonce, setNonce] = useState(0);
  const polled = useInvitationStatus(opened.id, false, nonce);
  const current = polled.status ?? opened;
  // Once the server says the invitation is gone, its last status (or the list's) no longer offers anything to add.
  const gone = invitationGone(polled.failure);

  const confirm = async (): Promise<void> => {
    if (busy || gone) return;
    setBusy(true); setFailure(null);
    try {
      const node = await api.confirmInvitation(current.id);
      if (!stillCurrent()) return;
      registry.reload(); onChanged();
      notify(`${addedNotice(node)} Remove CANARY_DASHBOARD_ENROLL_TOKEN from its settings now; it works only once.`);
      onClose();
    } catch (error) {
      if (!stillCurrent()) return;
      const next = invitationFailure(error, 'confirm');
      if (next.reload) { registry.reload(); onChanged(); setNonce(n => n + 1); }
      setFailure(next);
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  };

  return (
    <Modal title={<>Add “{current.label}”?</>} onClose={onClose} busy={busy} wide>
      {gone
        ? <Banner tone="warn" title="This invitation is no longer on this dashboard">It may have been cancelled meanwhile. Nothing was added.</Banner>
        : current.state === 'connected'
          ? (rejecting ? <CancelInvitationBody invitation={current} onClose={onClose} onBack={() => setRejecting(false)} onChanged={onChanged} />
            : <PairingCard key={`${current.id}|${current.node?.fingerprint ?? ''}`} invitation={current} busy={busy} onConfirm={() => { void confirm(); }} onReject={() => setRejecting(true)} />)
          : <Banner tone="info" title={invitationInfo(current, Date.now()).label}>This invitation isn’t waiting for a confirmation any more. The list shows what the dashboard’s server has now.</Banner>}
      {failure ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>{failure.title}</b><p>{failure.text}</p></div></div> : null}
      {rejecting && !gone ? null : <div className="modal-foot"><span /><button type="button" className="btn" onClick={onClose} disabled={busy}>Close</button></div>}
    </Modal>
  );
}

function CancelInvitationBody({ invitation, onClose, onBack, onChanged }: { invitation: NodeInvitation; onClose: () => void; onBack?: () => void; onChanged: () => void }) {
  const { api, registry, stillCurrent } = useCanary();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ChangeFailure | null>(null);
  const cancel = async (): Promise<void> => {
    if (busy) return;
    setBusy(true); setFailure(null);
    try {
      await api.cancelInvitation(invitation.id);
      if (!stillCurrent()) return;
      onChanged();
      notify(`Cancelled the invitation for ${invitation.label}. This dashboard set up no access on that node. ${REINVITE_DEPLOYED}`);
      onClose();
    } catch (error) {
      if (!stillCurrent()) return;
      const next = invitationFailure(error, 'cancel');
      if (next.reload) { onChanged(); registry.reload(); }
      setFailure(next);
      setBusy(false);
    }
  };
  return (
    <>
      <p>The node <span className="mono">{invitation.nodeId}</span> can’t use this invitation afterwards. This dashboard set up no access on the node, so nothing there needs undoing, and nothing leaves the node list.</p>
      {failure ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>{failure.title}</b><p>{failure.text}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void cancel(); }} disabled={busy}>{busy ? 'Cancelling…' : 'Cancel invitation'}</button>
        <button type="button" className="btn" onClick={onBack ?? onClose} disabled={busy}>Keep it</button>
      </div>
    </>
  );
}

export function CancelInvitationDialog({ invitation, onClose, onChanged }: { invitation: NodeInvitation; onClose: () => void; onChanged: () => void }) {
  return (
    <Modal title={<>Cancel the invitation for “{invitation.label}”?</>} onClose={onClose}>
      <CancelInvitationBody invitation={invitation} onClose={onClose} onChanged={onChanged} />
    </Modal>
  );
}
