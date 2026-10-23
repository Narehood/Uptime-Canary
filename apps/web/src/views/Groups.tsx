import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import type { Group, GroupInput, GroupView, HealthRollup } from '@uptime-canary/shared';
import { ApiFailure, describeFailure } from '../api';
import { useCanary } from '../context';
import { plural, shortRev, span } from '../format';
import { candidateOrder, dependencyCycle, groupAncestors, groupDescendants, groupForest, groupPath, groupStateText, groupTone, parentProblem, stateTone, stateWord, type GroupNode } from '../model';
import { notify } from '../notices';
import { href, navigate } from '../router';
import { Banner, Card, Chip, EmptyState, ErrorText, Modal, PageHead, ReadOnly, Rev, StateChip, Tag, Time, nodeTone } from '../ui/bits';
import { DurationField, ErrorSummary, NodePicker, TextField, fromDraft, toDraft, type DurationDraft } from '../ui/forms';
import { Glyph, Icon } from '../ui/icons';

export function GroupsUnsupported() {
  const { endpoint } = useCanary();
  return (
    <Card>
      <EmptyState title={`${endpoint} doesn’t offer groups`}>
        Its management API has no groups, so it probably runs an older Uptime-Canary version. Connect to an updated node to define groups.
      </EmptyState>
    </Card>
  );
}

function HowGroupsWork() {
  return (
    <Card title="How grouping works" hint="One meaningful alert for a shared outage, instead of one per dependent monitor">
      <ol className="steps">
        <li><b>Group related monitors.</b> Name a group after a place or shared dependency, choose its nodes, then pick the group in each monitor’s settings.</li>
        <li><b>Choose an outage monitor.</b> An ordinary monitor that fails when the shared dependency fails, such as a ping to a site’s gateway. Give it the same channels: its alert becomes the grouped alert.</li>
        <li><b>The others wait, per destination.</b> While it has a fresh failure and its alert reached a destination, the group’s other monitors hold back alerts to that destination. They still record checks and incidents.</li>
        <li><b>Nest groups for an overview (optional).</b> A group can sit inside a parent group. The parent’s health combines the current results of everything below it. Nesting doesn’t hold back alerts or pass on maintenance: both still follow each monitor’s own group.</li>
      </ol>
      <p className="foot-note">
        Delivery comes first. Each held monitor waits at most its grace period, counted from its own outage. For any destination the outage monitor doesn’t reach (no such channel, a failed send, or an unknown, missing, paused or conflicting dependency), held alerts go out after that.
        A monitor still down after the dependency recovers alerts then; one that recovered while held doesn’t replay old alerts. Uptime-Canary doesn’t guess whether the cause is power, internet, VPN or the host; it follows the dependency you configure.
      </p>
    </Card>
  );
}

/** Current health of a group and its nested groups. Counts are current monitors, not uptime. */
export function HealthSummary({ view }: { view: GroupView }) {
  if (view.healthState === undefined) return null;
  const h: HealthRollup | undefined = view.health;
  if (!h) {
    return (
      <p className="g-health">
        <span className="nowrap"><Glyph tone="unknown" /> <b>Unknown</b></span>
        <span className="muted"> · {view.healthReason || 'The hierarchy can’t be evaluated right now.'} Counts stay hidden until it can.</span>
      </p>
    );
  }
  const nested = h.descendantCount - h.leafCount;
  const total = h.activeLeafCount;
  const bar = [['u', h.upCount, 'up'], ['d', h.downCount, 'down'], ['p', h.pendingCount, 'pending'], ['k', h.unknownCount, 'unknown']] as const;
  return (
    <div className="g-health">
      <div className="hbar" aria-hidden="true">
        {total ? bar.filter(([, n]) => n > 0).map(([cls, n]) => <i key={cls} className={cls} style={{ flexGrow: n }} />) : <i className="z" style={{ flexGrow: 1 }} />}
      </div>
      <ul className="hcounts" aria-label={`${plural(total, 'active monitor')}${nested ? ` in this group and ${plural(nested, 'nested group')}` : ''}`}>
        <li><Glyph tone="up" /><b>{h.upCount}</b> up</li>
        <li><Glyph tone="down" /><b>{h.downCount}</b> down</li>
        <li><Glyph tone="pending" /><b>{h.pendingCount}</b> pending</li>
        <li><Glyph tone="unknown" /><b>{h.unknownCount}</b> unknown</li>
        {h.pausedCount ? <li><Glyph tone="paused" /><b>{h.pausedCount}</b> paused</li> : null}
        {h.maintenanceCount ? <li><Icon name="wrench" className="inline" /><b>{h.maintenanceCount}</b> in maintenance</li> : null}
      </ul>
      <p className="muted small">
        {total ? `${plural(total, 'active monitor')}` : 'No active monitors, so the group is pending rather than up'}
        {nested ? ` across this group and ${plural(nested, 'nested group')}` : ''}. Current results, not uptime.{h.maintenanceCount ? ' Monitors in maintenance still count with their measured result.' : ''}
      </p>
    </div>
  );
}

function GroupCard({ view, issue }: { view: GroupView; issue?: GroupNode['issue'] }) {
  const { overview, monitorMap, assessmentOf, nodeMap, editable, endpoint, assessed, groups, groupMap } = useCanary();
  const [deleting, setDeleting] = useState(false);
  const g = view.group;
  const ancestors = groupAncestors(g.id, groupMap).views;
  const children = (groups ?? []).filter(other => other.group.parentId === g.id && other.group.id !== g.id).sort((a, b) => a.group.name.localeCompare(b.group.name));
  const dependency = g.outageMonitorId ? monitorMap.get(g.outageMonitorId) : undefined;
  const dependencyA = g.outageMonitorId ? assessmentOf(g.outageMonitorId) : undefined;
  const members = assessed.filter(({ view: m }) => m.monitor.groupId === g.id);
  return (
    <article className="gcard" aria-labelledby={`g-${g.id}`}>
      <div className="gcard-h">
        <div>
          {ancestors.length ? <div className="gpath">In {ancestors.map(a => a.group.name).join(' › ')}</div> : null}
          <h2 id={`g-${g.id}`}>{g.name}</h2>
          <div className="row-wrap">
            {view.healthState !== undefined ? <Chip tone={view.health ? stateTone(view.healthState) : 'unknown'} label={`Health ${stateWord(view.health ? view.healthState : 'unknown').toLowerCase()}`} /> : null}
            <Chip tone={groupTone(view)} label={groupStateText(view)} />
            {view.suppressionActive ? <Tag icon="groups" tone="hold">Holding alerts</Tag> : null}
          </div>
        </div>
        {editable ? (
          <div className="acts">
            <a className={`btn sm${view.configConflict ? ' is-disabled' : ''}`} href={view.configConflict ? undefined : href('groups', g.id, 'edit')} aria-disabled={view.configConflict || undefined}><Icon name="edit" />Edit</a>
            <button type="button" className="btn sm danger" onClick={() => setDeleting(true)} disabled={view.configConflict}><Icon name="trash" />Delete</button>
          </div>
        ) : null}
      </div>
      {view.reason && g.outageMonitorId ? <p className="g-reason">{view.reason}</p> : null}
      {view.configConflict ? <Banner tone="warn" title="Configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve</a>}>Nodes hold different versions of this group. Its dependency doesn’t hold back alerts until the conflict is resolved.</Banner> : null}
      <dl className="kv">
        {view.healthState !== undefined ? <><dt>Health</dt><dd><HealthSummary view={view} /></dd></> : null}
        {g.parentId || children.length || view.healthState !== undefined ? (
          <>
            <dt>Parent</dt>
            <dd>
              {!g.parentId ? <span className="muted">None · top level</span>
                : issue === 'missing-parent' ? <span><Glyph tone="warn" /> Missing group <span className="mono">{g.parentId.slice(0, 8)}</span> <span className="muted">· health is unknown until it’s moved to another parent or the top level</span></span>
                : issue === 'cycle' ? <span><Glyph tone="warn" /> {groupMap.get(g.parentId)?.group.name ?? g.parentId} <span className="muted">· the parents form a cycle, so health is unknown until one moves</span></span>
                : <span>{groupPath(g.parentId, groupMap)}</span>}
            </dd>
          </>
        ) : null}
        {children.length ? (
          <>
            <dt>Nested groups</dt>
            <dd>
              <ul className="members">
                {children.map(child => <li key={child.group.id}><Glyph tone={child.health ? stateTone(child.healthState ?? 'unknown') : 'unknown'} label={`Health ${stateWord(child.health ? child.healthState ?? 'unknown' : 'unknown')}`} />{child.group.name}</li>)}
              </ul>
            </dd>
          </>
        ) : null}
        <dt>Nodes</dt>
        <dd>
          <span className="nchips">
            {candidateOrder(g.nodeIds).map(id => <span key={id} className="nchip"><Glyph tone={nodeTone(nodeMap.get(id))} /><span className="mono">{id}</span></span>)}
          </span>
          <span className="muted small"> {view.reachableNodes} of {view.totalNodes} reachable from {endpoint}</span>
        </dd>
        <dt>Outage monitor</dt>
        <dd>
          {!g.outageMonitorId ? <span className="muted">None · grouping only, nothing is held back</span>
            : dependency && dependencyA ? <><a href={href('monitors', dependency.monitor.id)}>{dependency.monitor.name}</a> <StateChip a={dependencyA} /></>
            : <span className="muted">Missing monitor · alerts are sent normally</span>}
        </dd>
        <dt>Grace period</dt><dd>{g.graceMs ? span(g.graceMs) : 'None'}</dd>
        <dt>Monitors</dt>
        <dd>
          {members.length ? (
            <ul className="members">
              {members.map(({ view: m, a }) => (
                <li key={m.monitor.id}>
                  <Glyph tone={a.tone} label={a.label} />
                  <a href={href('monitors', m.monitor.id)}>{m.monitor.name}</a>
                  {m.monitor.id === g.outageMonitorId ? <span className="muted small"> outage monitor</span> : null}
                  {m.suppressedByGroupId ? <> <Tag tone="hold">held</Tag></> : null}
                </li>
              ))}
            </ul>
          ) : <span className="muted">None yet. Choose this group in a monitor’s settings.</span>}
        </dd>
        <dt>Revision</dt><dd>{view.configConflict ? 'conflicting versions' : <><Rev revision={g.revision} /> <span className="muted">saved <Time at={g.updatedAt} now={overview.now} /></span></>}</dd>
      </dl>
      {deleting ? <DeleteGroupDialog view={view} members={members.length} onClose={() => setDeleting(false)} /> : null}
    </article>
  );
}

function GroupBranch({ node }: { node: GroupNode }) {
  return (
    <div className="gtree">
      <GroupCard view={node.view} issue={node.issue} />
      {node.children.length ? <div className="gtree-kids" role="group" aria-label={`Nested in ${node.view.group.name}`}>{node.children.map(child => <GroupBranch key={child.view.group.id} node={child} />)}</div> : null}
    </div>
  );
}

export function GroupsView() {
  const { groups, editable, endpoint, search } = useCanary();
  if (groups === null) return <div className="view"><PageHead title="Groups" /><GroupsUnsupported /></div>;
  const q = search.trim().toLowerCase();
  const live = groups.filter(view => !view.group.deleted);
  const shown = live.filter(view => !q || view.group.name.toLowerCase().includes(q))
    .sort((a, b) => Number(b.suppressionActive) - Number(a.suppressionActive) || a.group.name.localeCompare(b.group.name));
  // Nest only when nothing is filtered out, so a search never hides a match inside a collapsed parent.
  const forest = !q && live.some(view => view.group.parentId) ? groupForest(live) : null;
  return (
    <div className="view">
      <PageHead
        title="Groups"
        sub={<>Defined by you, shared by every node. As known to <span className="mono">{endpoint}</span>.</>}
        actions={editable ? <a className="btn primary" href={href('groups', 'new')}><Icon name="plus" />New group</a> : <ReadOnly>Viewers can’t edit groups</ReadOnly>}
      />
      <HowGroupsWork />
      {forest ? <div className="gcards">{forest.map(node => <GroupBranch key={node.view.group.id} node={node} />)}</div>
        : shown.length ? <div className="gcards">{shown.map(view => <GroupCard key={view.group.id} view={view} issue={view.group.parentId && !groups.some(other => other.group.id === view.group.parentId) ? 'missing-parent' : undefined} />)}</div>
        : <Card><EmptyState title={groups.length ? 'Nothing matches' : 'No groups yet'} action={!groups.length && editable ? <a className="btn primary" href={href('groups', 'new')}><Icon name="plus" />New group</a> : undefined}>
            {groups.length ? 'Try another search.' : 'Without groups, every monitor alerts on its own.'}
          </EmptyState></Card>}
    </div>
  );
}

interface GroupDraft { name: string; nodeIds: string[]; outageMonitorId: string; parentId: string; grace: DurationDraft }

function groupDraft(group: Group | undefined, defaultNodes: string[]): GroupDraft {
  return {
    name: group?.name ?? '',
    nodeIds: group ? [...group.nodeIds] : defaultNodes,
    outageMonitorId: group?.outageMonitorId ?? '',
    parentId: group?.parentId ?? '',
    grace: toDraft(group?.graceMs ?? 10_000),
  };
}

const GROUP_META = new Set(['id', 'revision', 'updatedAt', 'deleted']);
/** The stored group's writable fields, including any this dashboard doesn't know yet, so an edit never drops them. */
export function groupInputOf(group: Group | undefined): Record<string, unknown> {
  const input: Record<string, unknown> = {};
  if (!group) return input;
  for (const [key, value] of Object.entries(group)) {
    if (!GROUP_META.has(key) && value !== undefined && value !== null) input[key] = typeof value === 'object' ? structuredClone(value) : value;
  }
  return input;
}

/**
 * The edit form mounts only once the stored item is known, so its draft, initial values and base revision come from
 * that item. A route opened before the overview caught up shows "not found" and then mounts fresh, never a blank draft.
 */
export function GroupFormView({ id }: { id?: string }) {
  const { groupMap } = useCanary();
  const existing = id ? groupMap.get(id) : undefined;
  return <GroupFormBody key={existing ? `edit-${existing.group.id}` : id ? 'missing' : 'new'} id={id} />;
}

function GroupFormBody({ id }: { id?: string }) {
  const { groups, groupMap, monitorMap, nodes, endpoint, overview, editable, api, refresh, assessed } = useCanary();
  const existing = id ? groupMap.get(id) : undefined;
  const [draft, setDraft] = useState<GroupDraft>(() => groupDraft(existing?.group, [endpoint]));
  const [baseRevision, setBaseRevision] = useState(existing?.group.revision);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<{ message: string; stale: boolean } | null>(null);
  const [attempts, setAttempts] = useState(0);
  const summaryRef = useRef<HTMLDivElement>(null);
  const formId = useId();
  useEffect(() => { if (attempts) summaryRef.current?.focus(); }, [attempts, failure]);

  if (groups === null) return <div className="view"><PageHead title={id ? 'Edit group' : 'New group'} /><GroupsUnsupported /></div>;
  if (!editable) return <div className="view"><PageHead title={id ? 'Edit group' : 'New group'} /><Card><EmptyState title="Viewers can’t change groups">Connect with an editor or admin account.</EmptyState></Card></div>;
  if (id && !existing) return <div className="view"><PageHead title="Edit group" /><Card><EmptyState title="Group not found">{endpoint} doesn’t know this group. It may have been deleted.</EmptyState></Card></div>;

  const current = existing?.group;
  const errors: Partial<Record<'name' | 'nodes' | 'grace' | 'parent', string>> = {};
  const name = draft.name.trim();
  if (!name) errors.name = 'Enter a name.';
  else if (name.length > 128 || /[\x00-\x1f\x7f]/u.test(name)) errors.name = 'Use at most 128 characters, without control characters.';
  if (!draft.nodeIds.length) errors.nodes = 'Choose at least one node.';
  const graceMs = fromDraft(draft.grace);
  if (graceMs === null || graceMs < 0 || graceMs > 120_000) errors.grace = 'Use a grace period from 0 to 120 s.';
  const visible = showErrors ? errors : {};
  const cycle = draft.outageMonitorId ? dependencyCycle(current?.id, draft.outageMonitorId, monitorMap, groupMap) : false;
  const below = current ? groupDescendants(current.id, groups) : new Set<string>();
  const parentChoices = groups.filter(other => !other.group.deleted && other.group.id !== current?.id && !below.has(other.group.id))
    .map(other => ({ view: other, path: groupPath(other.group.id, groupMap), problem: parentProblem(other, groupMap) }))
    .sort((a, b) => a.path.localeCompare(b.path));
  const parent = draft.parentId ? groupMap.get(draft.parentId) : undefined;
  // The node validates the whole parent chain on every save, so a stored parent that has since gone missing or into a
  // cycle blocks unrelated edits too; say so here instead of after a round trip.
  const parentIssue = !draft.parentId ? undefined
    : !parent ? 'That group no longer exists. Choose another parent or the top level.'
    : parent.group.id === current?.id || below.has(parent.group.id) ? 'A group can’t sit inside itself or one of its own nested groups. Choose another parent or the top level.'
    : parentProblem(parent, groupMap);
  if (parentIssue) errors.parent = parentIssue.endsWith('.') ? parentIssue : `The node refuses this parent: ${parentIssue}.`;
  const set = <K extends keyof GroupDraft>(key: K, value: GroupDraft[K]): void => setDraft(d => ({ ...d, [key]: value }));
  const changedElsewhere = Boolean(current && baseRevision && current.revision !== baseRevision);
  const members = current ? assessed.filter(({ view }) => view.monitor.groupId === current.id) : [];
  const candidates = [...monitorMap.values()].sort((a, b) => a.monitor.name.localeCompare(b.monitor.name));
  const chosen = draft.outageMonitorId ? monitorMap.get(draft.outageMonitorId) : undefined;

  const loadLatest = (): void => {
    if (!current) return;
    // Parent options and the stored fields follow the latest revision too.
    setDraft(groupDraft(current, [endpoint]));
    setBaseRevision(current.revision);
    setFailure(null);
    setShowErrors(false);
  };

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (saving) return;
    setShowErrors(true);
    setFailure(null);
    if (Object.keys(errors).length) { setAttempts(n => n + 1); return; }
    // Start from the stored group so fields this form doesn't show survive the edit; absent parent means top level.
    const input = { ...groupInputOf(current), name, nodeIds: [...draft.nodeIds].sort(), graceMs: graceMs! } as GroupInput;
    if (draft.outageMonitorId) input.outageMonitorId = draft.outageMonitorId; else delete input.outageMonitorId;
    if (draft.parentId) input.parentId = draft.parentId; else delete input.parentId;
    setSaving(true);
    try {
      const saved = current && baseRevision ? await api.updateGroup(current.id, { ...input, revision: baseRevision }) : await api.createGroup(input);
      notify(`${current ? 'Saved' : 'Created'} group “${saved.name}” on ${endpoint} (revision ${shortRev(saved.revision)}). Peers pick it up when they next sync.`);
      refresh();
      navigate(href('groups'));
    } catch (error) {
      const stale = error instanceof ApiFailure && error.status === 409;
      setFailure({ message: stale ? 'Someone saved a newer revision first, or nodes now hold conflicting versions.' : describeFailure(error), stale });
      setAttempts(n => n + 1);
    } finally {
      setSaving(false);
    }
  };

  const errorList = Object.entries(visible).map(([field, message]) => `${{ name: 'Name', nodes: 'Nodes', grace: 'Grace period', parent: 'Parent group' }[field as 'name']}: ${message}`);
  return (
    <div className="view">
      <PageHead
        crumbs={<a href={href('groups')}>Groups</a>}
        title={current ? `Edit ${current.name}` : 'New group'}
        sub={current ? <>Editing revision <Rev revision={baseRevision ?? current.revision} />. Saving writes a new revision on <span className="mono">{endpoint}</span>.</> : <>Creates a group on <span className="mono">{endpoint}</span>, which shares it with every peer.</>}
      />
      {existing?.configConflict ? <Banner title="This group has a configuration conflict" actions={<a className="btn sm" href={href('config')}><Icon name="merge" />Resolve in Configuration</a>}>Edits are rejected until the conflict is resolved.</Banner>
        : changedElsewhere ? <Banner title={<>This group changed on {endpoint} while you were editing</>} actions={<button type="button" className="btn sm" onClick={loadLatest}><Icon name="refresh" />Load latest (discards your edits)</button>}>Saving your version will be rejected because it’s based on an older revision.</Banner>
        : null}
      <Card>
        <form className="form" onSubmit={event => { void submit(event); }} noValidate aria-busy={saving}>
          <ErrorSummary ref={summaryRef} title={failure ? (failure.stale ? 'Not saved: the group changed or has a conflict' : 'Not saved') : 'Check these fields'} errors={failure ? [] : errorList}>
            {failure ? <p>{failure.message}{failure.stale && current ? <> <button type="button" className="linkbtn" onClick={loadLatest}>Load the latest version</button> and apply your change again.</> : null}</p> : null}
          </ErrorSummary>
          <TextField full label="Name" value={draft.name} onChange={value => set('name', value)} error={visible.name} hint="Your own name for a place or shared dependency, such as a site, uplink or rack." maxLength={128} autoFocus={!current} disabled={saving} />
          <NodePicker
            legend="Nodes in this group" nodes={nodes} selected={draft.nodeIds} onChange={ids => set('nodeIds', ids)} endpoint={endpoint} now={overview.now}
            error={visible.nodes} disabled={saving} hint="The nodes that share this location or failure domain."
          />
          <div className="fld full">
            <label htmlFor={`${formId}-dep`}>Outage monitor <span className="muted">(optional)</span></label>
            <select id={`${formId}-dep`} value={draft.outageMonitorId} onChange={event => set('outageMonitorId', event.target.value)} disabled={saving} aria-describedby={`${formId}-dep-h${cycle ? ` ${formId}-dep-e` : ''}`}>
              <option value="">None · grouping only</option>
              {candidates.map(view => <option key={view.monitor.id} value={view.monitor.id}>{view.monitor.name} · {view.monitor.type === 'manual' ? 'manual' : view.monitor.target}</option>)}
              {draft.outageMonitorId && !chosen ? <option value={draft.outageMonitorId}>Missing monitor ({draft.outageMonitorId.slice(0, 8)})</option> : null}
            </select>
            <small id={`${formId}-dep-h`}>
              {chosen ? <>While <b>{chosen.monitor.name}</b> has a fresh failure and its own alert was delivered, the group’s other monitors keep recording but hold back their alerts. Its alert is the grouped notification, and its group never holds it back.</>
                : 'Pick an existing monitor that fails when the shared dependency fails. Without one, the group only labels its monitors.'}
            </small>
            {chosen && !chosen.monitor.webhookUrl && !chosen.monitor.notificationIds?.length ? (
              <small className="fwarn"><Glyph tone="warn" />{chosen.monitor.name} has no notification destination, so it can’t send the grouped alert. The group’s other monitors will alert on their own after the grace period.</small>
            ) : chosen ? <small>Only destinations that {chosen.monitor.name} also notifies are held; other destinations of dependent monitors fail open.</small> : null}
            <ErrorText id={`${formId}-dep-e`}>{cycle ? 'This monitor belongs to a group that already depends on this group. The node rejects dependency cycles.' : undefined}</ErrorText>
          </div>
          <div className="fld full">
            <label htmlFor={`${formId}-parent`}>Parent group <span className="muted">(optional)</span></label>
            <select id={`${formId}-parent`} value={draft.parentId} onChange={event => set('parentId', event.target.value)} disabled={saving}
              aria-invalid={visible.parent ? true : undefined} aria-describedby={`${formId}-parent-h${visible.parent ? ` ${formId}-parent-e` : ''}`}>
              <option value="">None · top level</option>
              {parentChoices.map(({ view, path, problem }) => (
                <option key={view.group.id} value={view.group.id} disabled={Boolean(problem) && view.group.id !== draft.parentId}>{path}{problem ? ` (${problem})` : ''}</option>
              ))}
              {draft.parentId && !parent ? <option value={draft.parentId}>Missing group ({draft.parentId.slice(0, 8)})</option> : null}
              {parent && !parentChoices.some(choice => choice.view.group.id === parent.group.id) ? <option value={parent.group.id}>{parent.group.name} (forms a cycle)</option> : null}
            </select>
            <small id={`${formId}-parent-h`}>
              Organizes current health only: the parent’s health includes this group’s monitors. It doesn’t hold back this group’s alerts, and maintenance chosen for the parent doesn’t apply here. Both still follow each monitor’s own group.
              {current && below.size ? ` Its ${plural(below.size, 'nested group')} can’t become its parent.` : ''}
            </small>
            <ErrorText id={`${formId}-parent-e`}>{visible.parent}</ErrorText>
          </div>
          <DurationField label="Grace period" value={draft.grace} onChange={value => set('grace', value)} error={visible.grace} units={['s', 'ms']} disabled={saving}
            hint="0 to 120 s, counted from each held monitor’s own outage. Long enough for the outage monitor’s result and alert to arrive." />
          <div className="fld">
            <span className="flabel">Monitors in this group</span>
            {current ? (members.length
              ? <ul className="members">{members.map(({ view, a }) => <li key={view.monitor.id}><Glyph tone={a.tone} label={a.label} /><a href={href('monitors', view.monitor.id)}>{view.monitor.name}</a></li>)}</ul>
              : <span className="muted">None yet.</span>) : null}
            <small>Choose a monitor’s group in that monitor’s settings.</small>
          </div>
          <div className="full form-acts">
            <button type="submit" className="btn primary" disabled={saving || existing?.configConflict}>
              <Icon name={current ? 'check' : 'plus'} />{saving ? `Saving to ${endpoint}…` : current ? 'Save changes' : 'Create group'}
            </button>
            <a className="btn" href={href('groups')}>Cancel</a>
            {saving ? <span className="muted small" role="status">Waiting for {endpoint} to acknowledge…</span> : null}
          </div>
        </form>
      </Card>
    </div>
  );
}

function DeleteGroupDialog({ view, members, onClose }: { view: GroupView; members: number; onClose: () => void }) {
  const { api, endpoint, refresh } = useCanary();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const g = view.group;
  const remove = async (): Promise<void> => {
    setBusy(true); setError(null);
    try {
      await api.deleteGroup(g.id, g.revision);
      notify(`Deleted group “${g.name}” on ${endpoint}.`);
      refresh();
      onClose();
    } catch (failure) {
      setError(describeFailure(failure));
      setBusy(false);
    }
  };
  return (
    <Modal title={`Delete group ${g.name}?`} onClose={onClose} busy={busy}>
      <p>{members ? `Its ${plural(members, 'monitor')} stay as they are and alert on their own again.` : 'No monitors use this group.'} Peers keep the deletion when they sync.</p>
      {error ? <div className="errsum" role="alert"><Glyph tone="down" /><div><b>Not deleted</b><p>{error}</p></div></div> : null}
      <div className="row-wrap modal-acts">
        <button type="button" className="btn danger-solid" onClick={() => { void remove(); }} disabled={busy}><Icon name="trash" />{busy ? 'Deleting…' : 'Delete group'}</button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
      </div>
    </Modal>
  );
}
