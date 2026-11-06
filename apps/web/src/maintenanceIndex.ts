import type { Assessed } from './model';

/** Current holding monitors for the displayed schedules; no schedule evaluation or alert decisions. */
export function maintenanceHoldingIndex(assessed: readonly Assessed[], ids: readonly string[]): Map<string, Assessed[]> {
  const wanted = new Set(ids);
  const held = new Map<string, Assessed[]>();
  if (wanted.size === 1) {
    const id = wanted.values().next().value!;
    held.set(id, assessed.filter(({ view }) => view.maintenanceIds?.includes(id)));
    return held;
  }
  if (!wanted.size) return held;
  for (const item of assessed) {
    for (const id of new Set(item.view.maintenanceIds ?? [])) {
      if (!wanted.has(id)) continue;
      const bucket = held.get(id) ?? [];
      bucket.push(item); held.set(id, bucket);
    }
  }
  return held;
}
