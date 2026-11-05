import type { GroupView, NotificationView } from '@uptime-canary/shared';
import { matchesFilter, matchesSearch, type Assessed, type StateFilter } from './model';

/** Assignment membership is counted once even if an older endpoint repeats an ID. */
export function nodeCoverage(assessed: readonly Assessed[], nodes: readonly { id: string }[]) {
  const coverage = new Map<string, { assigned: number; fresh: number }>();
  for (const node of nodes) coverage.set(node.id, { assigned: 0, fresh: 0 });
  for (const { view } of assessed) {
    const ids = view.monitor.nodeIds;
    for (let i = 0; i < ids.length; i++) {
      const id = ids[i]!;
      const counts = coverage.get(id);
      if (!counts || ids.indexOf(id) !== i) continue;
      counts.assigned++;
      if (view.observations.some(o => o.nodeId === id && o.state !== 'unknown')) counts.fresh++;
    }
  }
  return coverage;
}

/** Own the bucket arrays: sorting children never mutates the API catalogue. */
export function groupCardIndex(groups: readonly GroupView[], assessed: readonly Assessed[]) {
  const children = new Map<string, GroupView[]>(), members = new Map<string, Assessed[]>();
  for (const view of groups) {
    const parent = view.group.parentId;
    if (parent === undefined || parent === view.group.id) continue;
    const bucket = children.get(parent);
    if (bucket) bucket.push(view); else children.set(parent, [view]);
  }
  for (const bucket of children.values()) bucket.sort((a, b) => a.group.name.localeCompare(b.group.name));
  for (const item of assessed) {
    const id = item.view.monitor.groupId;
    if (id === undefined) continue;
    const bucket = members.get(id);
    if (bucket) bucket.push(item); else members.set(id, [item]);
  }
  return { children, members };
}
export type GroupCardIndex = ReturnType<typeof groupCardIndex>;

/** Each owned bucket keeps the original receipt objects and stable timestamp order. */
export function incidentReceiptIndex(receipts: readonly NotificationView[]) {
  const index = new Map<string, NotificationView[]>();
  for (const receipt of receipts) {
    const bucket = index.get(receipt.incidentId);
    if (bucket) bucket.push(receipt); else index.set(receipt.incidentId, [receipt]);
  }
  for (const bucket of index.values()) bucket.sort((a, b) => a.sentAt - b.sentAt);
  return index;
}

/** Counts use the search/type/tag scope, not the selected state chip. */
export function monitorInventory<T extends { type: string }>(assessed: readonly Assessed[], search: string,
  filter: StateFilter, catalogue: readonly T[],
  groups: ReadonlyMap<string, { group: { name: string } }>,
  tags: ReadonlyMap<string, { tag: { name: string } }>, type = '', tagId = '') {
  const counts = { all: 0, failing: 0, unknown: 0, up: 0, paused: 0 };
  const rows: Assessed[] = [], present = new Set<string>();
  const q = search.trim().toLowerCase();
  for (const item of assessed) {
    const { view, a } = item, m = view.monitor;
    present.add(m.type);
    if (q && !matchesSearch(view, q, groups.get(m.groupId ?? '')?.group.name,
      m.tags?.map(tag => `${tags.get(tag.tagId)?.tag.name ?? ''} ${tag.value ?? ''}`).join(' '))) continue;
    if ((type && m.type !== type) || (tagId && !m.tags?.some(tag => tag.tagId === tagId))) continue;
    counts.all++;
    switch (a.kind) {
      case 'everywhere': case 'partial': counts.failing++; break;
      case 'unknown': case 'conflict': case 'pending': counts.unknown++; break;
      case 'up': counts.up++; break;
      case 'paused': counts.paused++; break;
    }
    if (matchesFilter(a, filter)) rows.push(item);
  }
  return { rows, counts, types: catalogue.filter(entry => present.has(entry.type)) };
}
