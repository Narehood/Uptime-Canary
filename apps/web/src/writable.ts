/**
 * Field preservation for editors that rebuild a stored resource from their own fields (maintenance, tags, channels).
 * A newer node may store keys this dashboard doesn't model, and a save from here must send them back unchanged or it
 * would erase them. Whether a particular node accepts such a key is that node's rule; nothing here checks it.
 *
 * Only the resource object itself is read, never the view around it (state, windows, conflict flags), and its
 * runtime-owned metadata is never echoed. A new record has no stored object, so its input carries nothing extra.
 */

/** Keys every replicated resource carries that management input never sets. The base revision is sent on its own. */
export const RESOURCE_RUNTIME_FIELDS: readonly string[] = ['id', 'revision', 'revisions', 'updatedAt', 'deleted'];

/**
 * The stored keys an editor doesn't write itself: every key of `stored` except `modeled` and `runtime`, deep-copied so
 * the edit never changes the stored object. Nulls are left out, as `inputOf` does for monitors, because nodes refuse
 * null for optional fields. Keys are defined, not assigned, so a key such as `__proto__` stays an ordinary key.
 */
export function storedExtras(stored: object | null | undefined, modeled: readonly string[], runtime: readonly string[] = RESOURCE_RUNTIME_FIELDS): Record<string, unknown> {
  const extras: Record<string, unknown> = {};
  if (!stored) return extras;
  for (const [key, value] of Object.entries(stored)) {
    if (modeled.includes(key) || runtime.includes(key) || value === null || value === undefined) continue;
    Object.defineProperty(extras, key, { value: typeof value === 'object' ? structuredClone(value) : value, enumerable: true, writable: true, configurable: true });
  }
  return extras;
}
