import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import type { RegisteredNode } from '@uptime-canary/shared';
import { nodeStateInfo, registryOrder, shownNodeId } from '../dashboard';
import type { FleetNodeGroup, FleetNodeGroupInput, FleetNodeGroupsProps } from '../fleetViews';
import { andList, plural } from '../format';
import { Banner, Card, EmptyState, Modal, PageHead, ReadOnly, Rev, Spinner, Time } from '../ui/bits';
import { ErrorSummary, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';
import { FleetPlacementFields } from './FleetPlacementFields';

/**
 * Saved sets of dashboard registrations whose nodes run the same monitors. Saving or syncing a group asks the dashboard
 * to copy each monitor to the members that lack it; nothing is matched by name. A group never peers nodes or combines
 * their alerts, and it isn't a node's own Groups (outage dependencies and alert holds). Drafts live in this component;
 * saving, syncing and removing go through the callbacks with the group as it was opened, so the server can compare
 * revisions. A refused callback's own text is never shown, since it may carry anything.
 */

const validTime = (ms: number): boolean => Number.isFinite(ms) && ms > 0 && ms < 8.64e15;

function statusOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('status' in error)) return undefined;
  const status = (error as { status: unknown }).status;
  return typeof status === 'number' && Number.isInteger(status) ? status : undefined;
}

type Action = 'save' | 'remove' | 'sync';

function failureText(error: unknown, action: Action): string {
  const status = statusOf(error);
  if (status === 409) {
    return action === 'save' ? 'Node groups changed before this was saved. Refresh, then make your change again.'
      : action === 'sync' ? 'This node group changed since the page loaded. Refresh, then sync it again.'
      : 'This node group changed, or one of its monitors is still being placed. Refresh, then try again.';
  }
  if (status === 404) return 'This node group no longer exists. Refresh to see the current list.';
  if (status === 401) return 'Your sign-in to this dashboard is no longer valid. Sign in again.';
  if (status === 403) return 'Your account can’t change node groups.';
  if (status === 400 || status === 422) {
    return action === 'sync' ? 'The dashboard refused to sync this group. Refresh and check its nodes.' : 'The dashboard refused these values. Check the name and nodes, then try again.';
  }
  return action === 'save' ? 'The dashboard didn’t confirm the save. Refresh to see whether it was saved.'
    : action === 'sync' ? 'The dashboard didn’t confirm the sync. Refresh to see whether it started.'
    : 'The dashboard didn’t confirm the removal. Refresh to see whether it was removed.';
}

/** Removing a group never cascades: the registrations and every monitor stay as they are. */
const keptText = (n: number): string => n === 0 ? 'It holds no nodes.'
  : n === 1 ? 'Its node stays registered with this dashboard.'
  : `Its ${n} nodes stay registered with this dashboard.`;

const SYNC: Record<NonNullable<FleetNodeGroup['syncState']> | 'none', string> = {
  pending: 'Syncing monitors…', applied: 'Monitors in sync', blocked: 'Sync stopped', none: 'Not synced yet',
};

/** The dashboard's progress copying monitors. A process, not health: accent and dashes, a warning only when it stopped. */
function SyncLine({ group, count }: { group: FleetNodeGroup; count?: number }) {
  const state = group.syncState ?? 'none';
  return (
    <div className={`fl-sync s-${state}`}>
      <span className="fl-sync-st">
        {state === 'blocked' ? <Glyph tone="warn" /> : <Icon name={state === 'applied' ? 'check' : state === 'pending' ? 'refresh' : 'clock'} />}
        {SYNC[state]}
      </span>
      {count !== undefined ? <span className="fl-sync-n">{count ? `${plural(count, 'monitor')} follow this group` : 'No monitors follow it yet'}</span> : null}
      {group.syncReason ? <span className="fl-sync-r">{group.syncReason}</span> : null}
    </div>
  );
}

type Outcome = { ok: true } | { ok: false; message?: string };

function GroupEditor({ nodes, base, latest, busy, saving, syncing, onSave, onSync, onCancel, onLoadLatest }: {
  nodes: readonly RegisteredNode[]; base?: FleetNodeGroup; latest?: FleetNodeGroup | null; busy: boolean; saving: boolean; syncing: boolean;
  onSave: (input: FleetNodeGroupInput) => Promise<Outcome>; onSync?: () => Promise<Outcome>; onCancel: () => void; onLoadLatest?: () => void;
}) {
  const [name, setName] = useState(base?.name ?? '');
  const [registrationIds, setRegistrationIds] = useState<string[]>(() => [...(base?.registrationIds ?? [])]);
  const [showErrors, setShowErrors] = useState(false);
  const [failure, setFailure] = useState<{ title: string; text: string } | null>(null);
  const [attempts, setAttempts] = useState(0);
  const summaryRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (attempts) summaryRef.current?.focus(); }, [attempts]);

  const trimmed = name.trim();
  const errors: { name?: string; nodes?: string } = {};
  if (!trimmed) errors.name = 'Enter a name.';
  if (!registrationIds.length) errors.nodes = 'Choose at least one node.';
  const visible: typeof errors = showErrors ? errors : {};
  const listed = [visible.name ? `Name: ${visible.name}` : '', visible.nodes ? `Nodes: ${visible.nodes}` : ''].filter(Boolean);
  const removedElsewhere = Boolean(base) && latest === null;
  const changedElsewhere = Boolean(base && latest && latest.revision !== base.revision);

  const byId = new Map(nodes.map(node => [node.id, node]));
  const saved = new Set(base?.registrationIds ?? []);
  const chosen = new Set(registrationIds);
  const dirty = !base || trimmed !== base.name || saved.size !== chosen.size || [...chosen].some(id => !saved.has(id));
  const labels = (ids: string[]): string => andList(registryOrder(ids.flatMap(id => byId.get(id) ?? [])).map(node => node.label));
  const added = base ? registrationIds.filter(id => !saved.has(id) && byId.has(id)) : [];
  const dropped = base ? [...saved].filter(id => !chosen.has(id) && byId.has(id)) : [];

  // An unchanged group is synced as saved. Sync with edits saves them first, and saving starts a sync of its own.
  const submit = async (event: FormEvent | null, sync: boolean): Promise<void> => {
    event?.preventDefault();
    if (busy) return;
    setFailure(null);
    if (sync && !dirty && onSync) {
      const outcome = await onSync();
      if (!outcome.ok && outcome.message) { setFailure({ title: 'Not synced', text: outcome.message }); setAttempts(n => n + 1); }
      return;
    }
    setShowErrors(true);
    if (errors.name || errors.nodes) { setAttempts(n => n + 1); return; }
    const outcome = await onSave({ name: trimmed, registrationIds: [...registrationIds] });
    if (!outcome.ok && outcome.message) { setFailure({ title: 'Not saved', text: outcome.message }); setAttempts(n => n + 1); }
  };

  return (
    <form className="form one fl-gform" onSubmit={event => { void submit(event, false); }} noValidate aria-busy={busy || undefined}>
      {removedElsewhere ? (
        <Banner title="This node group was removed elsewhere">Saving would update a group that no longer exists, so the dashboard is likely to refuse it.</Banner>
      ) : changedElsewhere ? (
        <Banner title="This node group changed while you were editing" actions={onLoadLatest ? <button type="button" className="btn sm" onClick={onLoadLatest} disabled={busy}><Icon name="refresh" />Load latest (discards your edits)</button> : undefined}>
          Your edits are based on the version you opened, so the dashboard may refuse to save them.
        </Banner>
      ) : null}
      {base ? <SyncLine group={latest ?? base} /> : null}
      <ErrorSummary ref={summaryRef} title={failure ? failure.title : 'Check these fields'} errors={failure ? [] : listed}>
        {failure ? <p>{failure.text}</p> : null}
      </ErrorSummary>
      <TextField full label="Name" value={name} onChange={setName} error={visible.name} disabled={busy} autoFocus={!base}
        hint="Your own name for these nodes, such as a region or the sites of one customer." />
      <FleetPlacementFields legend="Nodes in this group" nodes={nodes} selectedRegistrationIds={registrationIds}
        onChange={setRegistrationIds} disabled={busy} error={visible.nodes}
        note="Monitors on any of these nodes are copied to the others. A group doesn’t make its nodes HA peers, so each independent node checks and alerts on its own." />
      {added.length || dropped.length ? (
        <div className="full fl-gchange" role="note">
          {added.length ? <p><Icon name="copy" /><span>When you save, monitors are copied to and from {labels(added)}.</span></p> : null}
          {dropped.length ? (
            <p className="warn">
              <Glyph tone="warn" />
              <span>This group’s monitors are removed from {labels(dropped)} when you save, unless another node group keeps them there.</span>
            </p>
          ) : null}
        </div>
      ) : null}
      <div className="full form-acts">
        <button type="submit" className="btn primary" disabled={busy}>
          <Icon name={base ? 'check' : 'plus'} />{saving ? 'Saving…' : base ? 'Save changes' : 'Create and sync'}
        </button>
        {base && onSync ? (
          <button type="button" className="btn" disabled={busy} onClick={() => { void submit(null, true); }}>
            <Icon name="refresh" />{syncing ? 'Starting sync…' : 'Sync monitors'}{dirty ? <span className="vh"> (saves your changes first)</span> : null}
          </button>
        ) : null}
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>Cancel</button>
        {saving || syncing ? <span className="muted small">Waiting for the dashboard to confirm…</span>
          : base ? <span className="muted small">Saving also syncs monitors.</span> : null}
      </div>
    </form>
  );
}

function GroupCard({ group, nodes, count, editable, canEdit, canAct, syncing, syncError, onEdit, onRemove, onSync }: {
  group: FleetNodeGroup; nodes: readonly RegisteredNode[]; count?: number; editable: boolean; canEdit: boolean; canAct: boolean; syncing: boolean;
  syncError?: string; onEdit: () => void; onRemove: () => void; onSync?: () => void;
}) {
  const headingId = useId();
  const byId = new Map(nodes.map(node => [node.id, node]));
  const ids = [...new Set(group.registrationIds)];
  const members = registryOrder(ids.flatMap(registrationId => byId.get(registrationId) ?? []));
  const gone = ids.filter(registrationId => !byId.has(registrationId));
  return (
    <article className="gcard fl-gcard" aria-labelledby={headingId}>
      <div className="gcard-h">
        <div className="fl-gcard-t">
          <h2 id={headingId}>{group.name}</h2>
          <span className="muted small">{plural(members.length, 'registered node')}{gone.length ? ` · ${gone.length} no longer registered` : ''}</span>
        </div>
        {editable ? (
          <div className="acts">
            {onSync ? <button type="button" className="btn sm" onClick={onSync} disabled={!canAct}><Icon name="refresh" />{syncing ? 'Starting…' : 'Sync monitors'}<span className="vh"> in {group.name}</span></button> : null}
            <button type="button" className="btn sm" onClick={onEdit} disabled={!canEdit}><Icon name="edit" />Edit<span className="vh"> {group.name}</span></button>
            <button type="button" className="btn sm danger" onClick={onRemove} disabled={!canAct}><Icon name="trash" />Remove<span className="vh"> {group.name}</span></button>
          </div>
        ) : null}
      </div>
      <SyncLine group={group} count={count} />
      {syncError ? <p className="ferr fl-gerr" role="alert"><Glyph tone="down" /><span>Not synced. {syncError}</span></p> : null}
      {members.length || gone.length ? (
        <ul className="fl-members" aria-label={`Nodes in ${group.name}`}>
          {members.map(node => {
            const info = nodeStateInfo(node);
            return (
              <li key={node.id} className={info.out ? 'out' : undefined}>
                <b>{node.label}</b><span className="mono">{shownNodeId(node) ?? 'node ID not read yet'}</span><span className="muted">{info.label}</span>
              </li>
            );
          })}
          {gone.map(registrationId => (
            <li key={registrationId} className="gone"><Glyph tone="warn" /><span>No longer registered</span><span className="mono">{registrationId}</span></li>
          ))}
        </ul>
      ) : <p className="muted small fl-gnone">No nodes in this group.</p>}
      <p className="fl-gmeta"><Rev revision={group.revision} />{validTime(group.updatedAt) ? <span className="muted">saved <Time at={group.updatedAt} /></span> : null}</p>
    </article>
  );
}

export function FleetNodeGroupsView({ nodes, groups, monitorCounts, loading, editable, error, onSave, onRemove, onSync, onRefresh, onOpenDependencies }: FleetNodeGroupsProps) {
  const [editing, setEditing] = useState<{ base?: FleetNodeGroup } | null>(null);
  const [removing, setRemoving] = useState<FleetNodeGroup | null>(null);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [syncError, setSyncError] = useState<{ id: string; message: string } | null>(null);
  const [busy, setBusy] = useState<null | Action>(null);
  const [syncingId, setSyncingId] = useState<string | null>(null);
  // Set before the first await, so a second click in the same frame can't start a duplicate save, sync or removal.
  const pending = useRef(false);
  const sorted = [...groups].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  const base = editing?.base;
  const latestOf = (group: FleetNodeGroup): FleetNodeGroup | null => groups.find(other => other.id === group.id) ?? null;
  const inPlace = base !== undefined && sorted.some(group => group.id === base.id);

  const run = async (kind: Action, action: () => Promise<void>): Promise<Outcome> => {
    if (pending.current) return { ok: false };
    pending.current = true;
    setBusy(kind);
    try {
      await action();
      return { ok: true };
    } catch (failure) {
      return { ok: false, message: failureText(failure, kind) };
    } finally {
      pending.current = false;
      setBusy(null);
    }
  };
  const save = async (input: FleetNodeGroupInput, existing: FleetNodeGroup | undefined): Promise<Outcome> => {
    const outcome = await run('save', () => existing ? onSave(input, existing) : onSave(input));
    if (outcome.ok) setEditing(null);
    return outcome;
  };
  const sync = async (group: FleetNodeGroup): Promise<Outcome> => {
    if (!onSync) return { ok: false };
    setSyncError(null);
    setSyncingId(group.id);
    const outcome = await run('sync', () => onSync(group));
    setSyncingId(null);
    if (outcome.ok && editing?.base?.id === group.id) setEditing(null);
    return outcome;
  };
  const syncCard = async (group: FleetNodeGroup): Promise<void> => {
    const outcome = await sync(group);
    if (!outcome.ok && outcome.message) setSyncError({ id: group.id, message: outcome.message });
  };
  const closeRemove = (): void => { if (busy !== 'remove') { setRemoving(null); setRemoveError(null); } };
  const confirmRemove = async (group: FleetNodeGroup): Promise<void> => {
    setRemoveError(null);
    const outcome = await run('remove', () => onRemove(group));
    if (outcome.ok) {
      setRemoving(null);
      if (editing?.base?.id === group.id) setEditing(null);
    } else if (outcome.message) setRemoveError(outcome.message);
  };

  const editor = editable && editing ? (
    <Card key={base ? `edit\n${base.id}\n${base.revision}` : 'new'} className="fl-editor" title={base ? `Edit ${base.name}` : 'New node group'}
      hint={base ? undefined : 'Creating the group copies the monitors on its nodes to each other, then keeps them in sync.'}>
      <GroupEditor
        nodes={nodes} base={base} latest={base ? latestOf(base) : undefined} busy={busy !== null} saving={busy === 'save'} syncing={busy === 'sync'}
        onSave={input => save(input, base)} onSync={base && onSync ? () => sync(base) : undefined} onCancel={() => setEditing(null)}
        onLoadLatest={base ? () => { const latest = latestOf(base); if (latest) setEditing({ base: latest }); } : undefined}
      />
    </Card>
  ) : null;
  const newButton = <button type="button" className="btn primary" onClick={() => setEditing({})} disabled={busy !== null || editing !== null}><Icon name="plus" />New node group</button>;

  return (
    <div className="view fl-groups">
      <PageHead
        title="Node groups"
        sub="Nodes in a group run the same monitors, kept in sync by this dashboard."
        actions={<>
          <span className="vh" role="status">{loading ? 'Loading node groups.' : busy === 'save' ? 'Saving the node group.' : busy === 'sync' ? 'Starting a sync.' : busy === 'remove' ? 'Removing the node group.' : ''}</span>
          <button type="button" className="btn" onClick={onRefresh} disabled={loading}><Icon name="refresh" />{loading ? 'Loading…' : 'Refresh'}</button>
          {editable ? newButton : <ReadOnly>Viewers can’t change node groups</ReadOnly>}
        </>}
      />
      <Banner tone="info" role="note" title="How node groups sync"
        actions={onOpenDependencies ? <button type="button" className="btn sm" onClick={onOpenDependencies}><Icon name="groups" />Outage dependencies</button> : undefined}>
        Every node in a group gets the group’s monitors, with their notification channels and tags. Creating or syncing a group also copies
        the monitors already on its nodes to the others; nothing is merged by name, and each monitor keeps its history. A group doesn’t make
        its nodes HA peers, so each independent node checks and alerts on its own. Outage dependencies stay on each node.
      </Banner>
      {error ? <Banner tone="down" role="alert" title="Node groups couldn’t be read">{error}</Banner> : null}
      {editor && !inPlace ? editor : null}
      {sorted.length ? (
        <div className="gcards fl-gcards" aria-busy={loading || undefined}>
          {sorted.map(group => editor && inPlace && base?.id === group.id ? editor : (
            <GroupCard key={group.id} group={group} nodes={nodes} count={monitorCounts?.[group.id]} editable={editable}
              canEdit={busy === null && editing === null} canAct={busy === null} syncing={syncingId === group.id}
              syncError={syncError?.id === group.id ? syncError.message : undefined}
              onEdit={() => setEditing({ base: group })} onRemove={() => { setRemoveError(null); setRemoving(group); }}
              onSync={onSync ? () => { void syncCard(group); } : undefined} />
          ))}
        </div>
      ) : loading ? <Card><div className="fl-wait"><Spinner label="Loading node groups…" /></div></Card>
        : editor ? null
        : (
          <Card>
            <EmptyState title="No node groups yet" action={editable ? newButton : undefined}>
              {editable ? 'Put nodes in a group to run the same monitors on all of them. Monitors already on those nodes are copied to the others.' : 'An editor or administrator can create node groups here.'}
            </EmptyState>
          </Card>
        )}
      {removing && editable ? (
        <Modal title={`Remove node group ${removing.name}?`} onClose={closeRemove} busy={busy === 'remove'}>
          <p>Every monitor stays on the nodes it’s on now, with its history; monitors just stop following this group. {keptText(new Set(removing.registrationIds).size)}</p>
          {removeError ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not removed</b><p>{removeError}</p></div></div> : null}
          <div className="row-wrap modal-acts">
            <button type="button" className="btn danger-solid" onClick={() => { void confirmRemove(removing); }} disabled={busy !== null}>
              <Icon name="trash" />{busy === 'remove' ? 'Removing…' : 'Remove node group'}
            </button>
            <button type="button" className="btn" onClick={closeRemove} disabled={busy === 'remove'}>Cancel</button>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}
