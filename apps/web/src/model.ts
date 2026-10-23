import type { GroupView, IncidentView, MonitorView, NodeView, NotificationView, Observation, Overview, Role } from '@uptime-canary/shared';

/**
 * Status shapes: up circle, down square, warn triangle, serious diamond, unknown dashed ring, pending ring with
 * clock hands, paused ring with bars, none dash.
 */
export type Tone = 'up' | 'down' | 'warn' | 'serious' | 'unknown' | 'pending' | 'paused' | 'none';

export type AssessmentKind = 'everywhere' | 'partial' | 'conflict' | 'unknown' | 'pending' | 'up' | 'paused';

export interface Assessment {
  kind: AssessmentKind;
  tone: Tone;
  label: string;
  detail: string;
  severity: number;
  failing: string[];
  passing: string[];
  /** Locations reporting `pending` (a container starting or restarting): neither up nor down. */
  pending: string[];
  unknown: string[];
  total: number;
}

/**
 * Observation states as words and shapes. `pending` arrives with service checks and isn't in every node's
 * contract yet, so states are compared as strings.
 */
export function stateTone(state: string): Tone {
  return state === 'up' ? 'up' : state === 'down' ? 'down' : state === 'pending' ? 'pending' : state === 'paused' ? 'paused' : 'unknown';
}
export function stateWord(state: string): string {
  return state === 'up' ? 'Up' : state === 'down' ? 'Down' : state === 'pending' ? 'Pending' : state === 'paused' ? 'Paused' : 'Unknown';
}
export const isPending = (state: string | undefined): boolean => state === 'pending';

export interface NodeColumn {
  id: string;
  name: string;
  url?: string;
  /** `unlisted`: a monitor names a node this endpoint has no peer entry for. */
  state: NodeView['state'] | 'unlisted';
  lastSeenAt: number | null;
}

export const canEdit = (role: Role): boolean => role === 'admin' || role === 'editor';

export const ROLES: { role: Role; label: string; can: string }[] = [
  { role: 'viewer', label: 'Viewer', can: 'Reads overviews, monitors, groups, maintenance, channels (names only), status pages and history. Never sees webhook URLs, request credentials, service settings or certificates.' },
  { role: 'editor', label: 'Editor', can: 'Also creates, edits, pauses and deletes monitors, groups, tags, channels, maintenance and status pages, sends channel tests, and resolves conflicts.' },
  { role: 'admin', label: 'Admin', can: 'Also manages accounts on this node: creates them, changes roles and tokens, resets passwords and two-factor, and deletes them.' },
];

/** Nodes in ID order: the same order nodes use as notification candidates. */
export function nodeColumns(overview: Overview): NodeColumn[] {
  const columns = new Map<string, NodeColumn>();
  for (const node of overview.nodes) columns.set(node.id, { ...node });
  if (!columns.has(overview.node.id)) {
    columns.set(overview.node.id, { id: overview.node.id, name: overview.node.name, state: 'local', lastSeenAt: overview.now });
  }
  for (const view of overview.monitors) for (const id of view.monitor.nodeIds) {
    if (!columns.has(id)) columns.set(id, { id, name: id, state: 'unlisted', lastSeenAt: null });
  }
  return [...columns.values()].sort((a, b) => a.id.localeCompare(b.id));
}

export const outOfView = (node: NodeColumn | undefined): boolean => node?.state === 'unreachable' || node?.state === 'unlisted';

export function observationFor(view: MonitorView, nodeId: string): Observation | undefined {
  return view.observations.find(observation => observation.nodeId === nodeId);
}

/** Turns the node's verdict plus per-location evidence into the wording rules from docs/design.md. */
export function assess(view: MonitorView, nodes: Map<string, NodeColumn>): Assessment {
  const ids = view.monitor.nodeIds;
  const failing = ids.filter(id => observationFor(view, id)?.state === 'down');
  const passing = ids.filter(id => observationFor(view, id)?.state === 'up');
  const pending = ids.filter(id => isPending(observationFor(view, id)?.state));
  const unknown = ids.filter(id => !failing.includes(id) && !passing.includes(id) && !pending.includes(id));
  const base = { failing, passing, pending, unknown, total: ids.length };
  if (view.state === 'paused') return { ...base, kind: 'paused', tone: 'paused', label: 'Paused', detail: '', severity: 7 };
  if (view.configConflict) return { ...base, kind: 'conflict', tone: 'serious', label: 'Unknown', detail: 'config conflict', severity: 3 };
  const notInView = unknown.length > 0 && unknown.every(id => outOfView(nodes.get(id)));
  const unknownText = (n: number): string => notInView ? `${n} not in view` : `${n} unknown`;
  if (view.state === 'down') {
    if (failing.length && failing.length === ids.length) {
      return { ...base, kind: 'everywhere', tone: 'down', label: 'Down', detail: ids.length === 1 ? `from ${failing[0]}` : 'everywhere', severity: 0 };
    }
    const detail = failing.length ? `${failing.length} of ${ids.length} locations${unknown.length ? ` · ${unknownText(unknown.length)}` : ''}${pending.length ? ` · ${pending.length} pending` : ''}` : '';
    return { ...base, kind: 'partial', tone: 'down', label: 'Down', detail, severity: 1 };
  }
  if (isPending(view.state)) {
    return { ...base, kind: 'pending', tone: 'pending', label: 'Pending', detail: `${pending.length || ids.length} of ${ids.length} starting`, severity: 5.5 };
  }
  if (view.state === 'unknown') {
    const n = unknown.length || view.unknownNodes;
    return { ...base, kind: 'unknown', tone: 'unknown', label: 'Unknown', detail: `${n} of ${ids.length} ${notInView ? 'not in view' : 'without fresh data'}`, severity: 5 };
  }
  return { ...base, kind: 'up', tone: 'up', label: 'Up', detail: '', severity: 6 };
}

export type StateFilter = 'all' | 'failing' | 'unknown' | 'up' | 'paused';

export function matchesFilter(assessment: Assessment, filter: StateFilter): boolean {
  switch (filter) {
    case 'all': return true;
    case 'failing': return assessment.kind === 'everywhere' || assessment.kind === 'partial';
    case 'unknown': return assessment.kind === 'unknown' || assessment.kind === 'conflict' || assessment.kind === 'pending';
    case 'up': return assessment.kind === 'up';
    case 'paused': return assessment.kind === 'paused';
  }
}

export interface Assessed { view: MonitorView; a: Assessment }

export function assessAll(views: MonitorView[], nodes: Map<string, NodeColumn>): Assessed[] {
  return views.map(view => ({ view, a: assess(view, nodes) }))
    // Within a severity, monitors whose alerts are held back follow the unsuppressed ones, so root causes lead.
    .sort((x, y) => x.a.severity - y.a.severity || Number(Boolean(x.view.suppressedByGroupId)) - Number(Boolean(y.view.suppressedByGroupId))
      || x.view.monitor.name.localeCompare(y.view.monitor.name));
}

export function matchesSearch(view: MonitorView, query: string, groupName?: string, extra?: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  const m = view.monitor;
  return [m.name, m.target, m.id, m.type, ...m.nodeIds, groupName ?? '', extra ?? ''].join(' ').toLowerCase().includes(q);
}

/** Candidates try to notify in node-ID order; the server stores nodeIds sorted, but never rely on it. */
export const candidateOrder = (nodeIds: string[]): string[] => [...nodeIds].sort((a, b) => a.localeCompare(b));

/** Each destination (channel recipient or legacy webhook) gets its own down and recovery, so duplicates are per destination. */
export const receiptKey = (receipt: NotificationView): string => `${receipt.incidentId}|${receipt.kind}|${receipt.destinationId ?? receipt.channelId ?? ''}`;

/**
 * Down and recovery alerts happen once per incident and destination, so more than one receipt is a duplicate send.
 * Repeat receipts don't expose their slot, so repeats are never labelled duplicates.
 */
export function duplicateReceipts(receipts: NotificationView[]): Set<string> {
  const groups = new Map<string, NotificationView[]>();
  for (const receipt of receipts) {
    if (receipt.kind === 'repeat') continue;
    const key = receiptKey(receipt);
    groups.set(key, [...(groups.get(key) ?? []), receipt]);
  }
  const duplicates = new Set<string>();
  for (const group of groups.values()) if (group.length > 1) for (const receipt of group) duplicates.add(receipt.id);
  return duplicates;
}

export const receiptKindLabel: Record<NotificationView['kind'], string> = { down: 'Down alert', repeat: 'Repeat', recovery: 'Recovery' };

/** Closed administratively after a check change, pause or deletion. Not a recovery. */
export const retired = (incident: IncidentView): boolean => incident.resolvedAt !== null && Boolean(incident.retiredBy);

export function incidentReceipts(incident: IncidentView, receipts: NotificationView[]): NotificationView[] {
  return receipts.filter(receipt => receipt.incidentId === incident.id).sort((a, b) => a.sentAt - b.sentAt);
}

export const groupTone = (view: GroupView): Tone => view.configConflict ? 'serious' : view.state;

export function groupStateText(view: GroupView): string {
  if (view.configConflict) return 'Config conflict';
  if (!view.group.outageMonitorId) return 'No outage monitor';
  return { up: 'Dependency up', down: 'Dependency down', unknown: 'Dependency unknown', pending: 'Dependency pending' }[view.state];
}

/**
 * Mirrors the server's cycle rule as an early hint only: group G depending on monitor M is a cycle when
 * following M's group's dependency chain leads back to G. A monitor inside G itself is allowed.
 */
export function dependencyCycle(groupId: string | undefined, outageMonitorId: string, monitors: Map<string, MonitorView>, groups: Map<string, GroupView>): boolean {
  if (!groupId) return false;
  const seen = new Set<string>();
  let current: string | undefined = monitors.get(outageMonitorId)?.monitor.groupId;
  while (current && current !== groupId && !seen.has(current)) {
    seen.add(current);
    const next: string | undefined = groups.get(current)?.group.outageMonitorId;
    if (!next) return false;
    current = monitors.get(next)?.monitor.groupId;
    if (current === groupId) return true;
  }
  return false;
}

// ---------- group hierarchy (protocol 11). Parent links organize current health only: alert holds and maintenance
// still follow each monitor's own group, and nothing here changes configuration.

export type HierarchyIssue = 'missing-parent' | 'cycle';
export interface GroupNode { view: GroupView; children: GroupNode[]; issue?: HierarchyIssue }

const parentOf = (view: GroupView | undefined): string | undefined => view?.group.parentId || undefined;

/** Whether following parents from `id` comes back to it. */
export function inParentCycle(id: string, groups: Map<string, GroupView>): boolean {
  const seen = new Set<string>();
  for (let current = parentOf(groups.get(id)); current; current = parentOf(groups.get(current))) {
    if (current === id) return true;
    if (seen.has(current)) return false;
    seen.add(current);
  }
  return false;
}

/** Roots first, children nested and sorted by name. Groups with a missing parent or in a cycle show as roots, flagged. */
export function groupForest(groups: GroupView[]): GroupNode[] {
  const map = new Map(groups.map(view => [view.group.id, view]));
  const nodes = new Map(groups.map(view => [view.group.id, { view, children: [] } as GroupNode]));
  const roots: GroupNode[] = [];
  for (const node of nodes.values()) {
    const parent = parentOf(node.view);
    if (!parent) roots.push(node);
    else if (!nodes.has(parent)) { node.issue = 'missing-parent'; roots.push(node); }
    else if (inParentCycle(node.view.group.id, map)) { node.issue = 'cycle'; roots.push(node); }
    else nodes.get(parent)!.children.push(node);
  }
  const sort = (list: GroupNode[]): GroupNode[] => {
    list.sort((a, b) => a.view.group.name.localeCompare(b.view.group.name));
    for (const node of list) sort(node.children);
    return list;
  };
  return sort(roots);
}

/** Ancestors from the top down, excluding the group itself; stops at a missing parent or a repeat. */
export function groupAncestors(id: string, groups: Map<string, GroupView>): { views: GroupView[]; issue?: HierarchyIssue | 'conflict' } {
  const views: GroupView[] = [];
  const seen = new Set<string>([id]);
  for (let current = parentOf(groups.get(id)); current; current = parentOf(groups.get(current))) {
    if (seen.has(current)) return { views: views.reverse(), issue: 'cycle' };
    seen.add(current);
    const view = groups.get(current);
    if (!view) return { views: views.reverse(), issue: 'missing-parent' };
    views.push(view);
    if (view.configConflict) return { views: views.reverse(), issue: 'conflict' };
  }
  return { views: views.reverse() };
}

/** "Site › Rack › Group" for selects and labels. */
export function groupPath(id: string, groups: Map<string, GroupView>): string {
  const view = groups.get(id);
  if (!view) return `missing group ${id.slice(0, 8)}`;
  return [...groupAncestors(id, groups).views.map(v => v.group.name), view.group.name].join(' › ');
}

/** Every group below `id`, following current parent links (cycle-safe). */
export function groupDescendants(id: string, groups: GroupView[]): Set<string> {
  const out = new Set<string>();
  const queue = [id];
  while (queue.length) {
    const current = queue.shift()!;
    for (const view of groups) {
      if (parentOf(view) === current && !out.has(view.group.id) && view.group.id !== id) { out.add(view.group.id); queue.push(view.group.id); }
    }
  }
  return out;
}

/**
 * Why the node would refuse `candidate` as a parent: it must exist, and it and every ancestor must have a single
 * version. Returns undefined when it looks acceptable; the node still decides.
 */
export function parentProblem(candidate: GroupView, groups: Map<string, GroupView>): string | undefined {
  if (candidate.configConflict) return 'conflicting versions';
  const chain = groupAncestors(candidate.group.id, groups);
  if (chain.issue === 'conflict') return 'an ancestor has conflicting versions';
  if (chain.issue === 'missing-parent') return 'its parent is missing';
  if (chain.issue === 'cycle') return 'its parents form a cycle';
  return undefined;
}
