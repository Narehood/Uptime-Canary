import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import type { RegisteredNode } from '@uptime-canary/shared';
import { nodeStateInfo, registryOrder, shownNodeId } from '../dashboard';
import type { FleetNodeGroup, FleetNodeGroupInput, FleetNodeGroupsProps } from '../fleetViews';
import { plural } from '../format';
import { Banner, Card, EmptyState, Modal, PageHead, ReadOnly, Rev, Spinner, Time } from '../ui/bits';
import { ErrorSummary, TextField } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';
import { FleetPlacementFields } from './FleetPlacementFields';

/**
 * Saved selections of dashboard registrations, used only to fill in Run on. They aren't a node's own Groups (outage
 * dependencies and alert holds) and never peer nodes. Drafts live in this component; saving and removing go through
 * the callbacks with the group as it was opened, so the server can compare revisions. A refused callback's own text is
 * never shown, since it may carry anything.
 */

const validTime = (ms: number): boolean => Number.isFinite(ms) && ms > 0 && ms < 8.64e15;

function statusOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('status' in error)) return undefined;
  const status = (error as { status: unknown }).status;
  return typeof status === 'number' && Number.isInteger(status) ? status : undefined;
}

function failureText(error: unknown, action: 'save' | 'remove'): string {
  const status = statusOf(error);
  if (status === 409) return action === 'save' ? 'Node groups changed before this was saved. Refresh, then make your change again.' : 'This node group changed since you opened it. Refresh and check it before removing it.';
  if (status === 404) return 'This node group no longer exists. Refresh to see the current list.';
  if (status === 401) return 'Your sign-in to this dashboard is no longer valid. Sign in again.';
  if (status === 403) return 'Your account can’t change node groups.';
  if (status === 400 || status === 422) return 'The dashboard refused these values. Check the name and nodes, then try again.';
  return action === 'save' ? 'The dashboard didn’t confirm the save. Refresh to see whether it was saved.' : 'The dashboard didn’t confirm the removal. Refresh to see whether it was removed.';
}

/** Removing a selection never cascades: the registrations and every monitor stay as they are. */
const keptText = (n: number): string => n === 0 ? 'It holds no nodes, and no monitor is removed.'
  : n === 1 ? 'Its node stays registered with this dashboard, and no monitor is removed.'
  : `Its ${n} nodes stay registered with this dashboard, and no monitor is removed.`;

type Outcome = { ok: true } | { ok: false; message?: string };

function GroupEditor({ nodes, groups, base, latest, busy, saving, onSave, onCancel, onLoadLatest }: {
  nodes: readonly RegisteredNode[]; groups: readonly FleetNodeGroup[]; base?: FleetNodeGroup; latest?: FleetNodeGroup | null; busy: boolean; saving: boolean;
  onSave: (input: FleetNodeGroupInput) => Promise<Outcome>; onCancel: () => void; onLoadLatest?: () => void;
}) {
  const [name, setName] = useState(base?.name ?? '');
  const [registrationIds, setRegistrationIds] = useState<string[]>(() => [...(base?.registrationIds ?? [])]);
  const [showErrors, setShowErrors] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
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

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (busy) return;
    setShowErrors(true);
    setFailure(null);
    if (errors.name || errors.nodes) { setAttempts(n => n + 1); return; }
    const outcome = await onSave({ name: trimmed, registrationIds: [...registrationIds] });
    if (!outcome.ok && outcome.message) { setFailure(outcome.message); setAttempts(n => n + 1); }
  };

  return (
    <form className="form one fl-gform" onSubmit={event => { void submit(event); }} noValidate aria-busy={busy || undefined}>
      {removedElsewhere ? (
        <Banner title="This node group was removed elsewhere">Saving would update a group that no longer exists, so the dashboard is likely to refuse it.</Banner>
      ) : changedElsewhere ? (
        <Banner title="This node group changed while you were editing" actions={onLoadLatest ? <button type="button" className="btn sm" onClick={onLoadLatest} disabled={busy}><Icon name="refresh" />Load latest (discards your edits)</button> : undefined}>
          Your edits are based on the version you opened, so the dashboard may refuse to save them.
        </Banner>
      ) : null}
      <ErrorSummary ref={summaryRef} title={failure ? 'Not saved' : 'Check these fields'} errors={failure ? [] : listed}>
        {failure ? <p>{failure}</p> : null}
      </ErrorSummary>
      <TextField full label="Name" value={name} onChange={setName} error={visible.name} disabled={busy} autoFocus={!base}
        hint="Your own name for this set of nodes, such as a region or the sites of one customer." />
      <FleetPlacementFields legend="Nodes in this group" nodes={nodes} groups={groups} selectedRegistrationIds={registrationIds}
        onChange={setRegistrationIds} disabled={busy} error={visible.nodes} />
      <div className="full form-acts">
        <button type="submit" className="btn primary" disabled={busy}>
          <Icon name={base ? 'check' : 'plus'} />{saving ? 'Saving…' : base ? 'Save changes' : 'Create node group'}
        </button>
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>Cancel</button>
        {saving ? <span className="muted small">Waiting for the dashboard to confirm…</span> : null}
      </div>
    </form>
  );
}

function GroupCard({ group, nodes, editable, canEdit, canRemove, onEdit, onRemove }: {
  group: FleetNodeGroup; nodes: readonly RegisteredNode[]; editable: boolean; canEdit: boolean; canRemove: boolean; onEdit: () => void; onRemove: () => void;
}) {
  const headingId = useId();
  const byId = new Map(nodes.map(node => [node.id, node]));
  const ids = [...new Set(group.registrationIds)];
  const members = registryOrder(ids.flatMap(registrationId => byId.get(registrationId) ?? []));
  const gone = ids.filter(registrationId => !byId.has(registrationId));
  return (
    <article className="gcard fl-gcard" aria-labelledby={headingId}>
      <div className="gcard-h">
        <div>
          <h2 id={headingId}>{group.name}</h2>
          <span className="muted small">{plural(members.length, 'registered node')}{gone.length ? ` · ${gone.length} no longer registered` : ''}</span>
        </div>
        {editable ? (
          <div className="acts">
            <button type="button" className="btn sm" onClick={onEdit} disabled={!canEdit}><Icon name="edit" />Edit<span className="vh"> {group.name}</span></button>
            <button type="button" className="btn sm danger" onClick={onRemove} disabled={!canRemove}><Icon name="trash" />Remove<span className="vh"> {group.name}</span></button>
          </div>
        ) : null}
      </div>
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

export function FleetNodeGroupsView({ nodes, groups, loading, editable, error, onSave, onRemove, onRefresh }: FleetNodeGroupsProps) {
  const [editing, setEditing] = useState<{ base?: FleetNodeGroup } | null>(null);
  const [removing, setRemoving] = useState<FleetNodeGroup | null>(null);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [busy, setBusy] = useState<null | 'save' | 'remove'>(null);
  // Set before the first await, so a second click in the same frame can't start a duplicate save or removal.
  const pending = useRef(false);
  const sorted = [...groups].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  const base = editing?.base;
  const latestOf = (group: FleetNodeGroup): FleetNodeGroup | null => groups.find(other => other.id === group.id) ?? null;
  const inPlace = base !== undefined && sorted.some(group => group.id === base.id);

  const run = async (kind: 'save' | 'remove', action: () => Promise<void>): Promise<Outcome> => {
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
      hint="Saved on this dashboard. Choosing nodes here doesn’t peer them or change their monitors.">
      <GroupEditor
        nodes={nodes} groups={groups.filter(group => group.id !== base?.id)} base={base} latest={base ? latestOf(base) : undefined} busy={busy !== null} saving={busy === 'save'}
        onSave={input => save(input, base)} onCancel={() => setEditing(null)}
        onLoadLatest={base ? () => { const latest = latestOf(base); if (latest) setEditing({ base: latest }); } : undefined}
      />
    </Card>
  ) : null;
  const newButton = <button type="button" className="btn primary" onClick={() => setEditing({})} disabled={busy !== null || editing !== null}><Icon name="plus" />New node group</button>;

  return (
    <div className="view fl-groups">
      <PageHead
        title="Node groups"
        sub="Saved sets of the nodes registered with this dashboard, for choosing where monitors run."
        actions={<>
          <span className="vh" role="status">{loading ? 'Loading node groups.' : busy === 'save' ? 'Saving the node group.' : busy === 'remove' ? 'Removing the node group.' : ''}</span>
          <button type="button" className="btn" onClick={onRefresh} disabled={loading}><Icon name="refresh" />{loading ? 'Loading…' : 'Refresh'}</button>
          {editable ? newButton : <ReadOnly>Viewers can’t change node groups</ReadOnly>}
        </>}
      />
      <Banner tone="info" role="note" title="Not the same as Groups">
        Node groups belong to this dashboard and only pick nodes for Run on. They don’t peer nodes, hold back alerts or define outage dependencies; Groups on each node do that.
        Nodes picked together still run separate copies unless they’re configured as HA peers.
      </Banner>
      {error ? <Banner tone="down" role="alert" title="Node groups couldn’t be read">{error}</Banner> : null}
      {editor && !inPlace ? editor : null}
      {sorted.length ? (
        <div className="gcards fl-gcards" aria-busy={loading || undefined}>
          {sorted.map(group => editor && inPlace && base?.id === group.id ? editor : (
            <GroupCard key={group.id} group={group} nodes={nodes} editable={editable}
              canEdit={busy === null && editing === null} canRemove={busy === null}
              onEdit={() => setEditing({ base: group })} onRemove={() => { setRemoveError(null); setRemoving(group); }} />
          ))}
        </div>
      ) : loading ? <Card><div className="fl-wait"><Spinner label="Loading node groups…" /></div></Card>
        : editor ? null
        : (
          <Card>
            <EmptyState title="No node groups yet" action={editable ? newButton : undefined}>
              {editable ? 'Save a set of nodes, such as a region or the sites of one customer, to pick them together when you place a monitor.' : 'An editor or administrator can save sets of nodes here.'}
            </EmptyState>
          </Card>
        )}
      {removing && editable ? (
        <Modal title={`Remove node group ${removing.name}?`} onClose={closeRemove} busy={busy === 'remove'}>
          <p>Only this saved selection is removed. {keptText(new Set(removing.registrationIds).size)}</p>
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
