import { useEffect, useState } from 'react';

/** Hash routes keep the static web container simple and never put API data or credentials in the URL. */
export type Route =
  | { name: 'overview' }
  | { name: 'monitors' }
  | { name: 'monitor-new' }
  | { name: 'monitor'; id: string }
  | { name: 'monitor-edit'; id: string }
  | { name: 'fleet-monitor-edit'; id: string }
  /** A new monitor starting from a copy of `id`. Only the source's ID is in the URL, never a copied setting. */
  | { name: 'monitor-clone'; id: string }
  | { name: 'tags' }
  | { name: 'groups' }
  | { name: 'node-groups' }
  | { name: 'outage-groups' }
  | { name: 'group-new' }
  | { name: 'group-edit'; id: string }
  | { name: 'incidents' }
  | { name: 'notifications' }
  | { name: 'deliveries'; channelId?: string }
  | { name: 'receipts' }
  | { name: 'channel-new' }
  | { name: 'channel-edit'; id: string }
  | { name: 'maintenance' }
  | { name: 'maintenance-new' }
  | { name: 'maintenance-edit'; id: string }
  | { name: 'status-pages' }
  | { name: 'status-page-new' }
  | { name: 'status-page-edit'; id: string }
  /**
   * Public, credential-free status page. `nodes` are node base addresses chosen by whoever shared the link. On the
   * control plane `registration` instead names the saved node the dashboard's server reads it from.
   */
  | { name: 'public-status'; slug: string; nodes: string[]; registration?: string }
  | { name: 'nodes' }
  | { name: 'config' }
  | { name: 'accounts' }
  | { name: 'account' }
  | { name: 'not-found'; path: string };

export function parseRoute(hash: string): Route {
  const raw = hash.replace(/^#/u, '') || '/overview';
  const [path = '', query = ''] = raw.split('?', 2) as [string, string?];
  const parts = path.split('/').filter(Boolean).map(part => {
    try { return decodeURIComponent(part); } catch { return part; }
  });
  const [section, id, action] = parts;
  if (section === 'status' && id && parts.length === 2) {
    const params = new URLSearchParams(query);
    const nodes = params.getAll('node').filter(Boolean).slice(0, 8);
    const registration = params.get('registration');
    return registration ? { name: 'public-status', slug: id, nodes, registration } : { name: 'public-status', slug: id, nodes };
  }
  if (section === 'notifications' && id === 'deliveries' && parts.length === 2 && query) {
    const params = new URLSearchParams(query);
    const channelId = params.get('channel');
    if (channelId && [...params.keys()].every(key => key === 'channel') && params.getAll('channel').length === 1) return { name: 'deliveries', channelId };
  }
  if (query) return { name: 'not-found', path: raw };
  switch (section) {
    case 'fleet-monitors':
      if (id && action === 'edit' && parts.length === 3) return { name: 'fleet-monitor-edit', id };
      break;
    case undefined:
    case 'overview': return parts.length <= 1 ? { name: 'overview' } : { name: 'not-found', path: raw };
    case 'monitors':
      if (!id) return { name: 'monitors' };
      if (id === 'new' && !action) return { name: 'monitor-new' };
      if (!action) return { name: 'monitor', id };
      if (action === 'edit' && parts.length === 3) return { name: 'monitor-edit', id };
      if (action === 'clone' && parts.length === 3) return { name: 'monitor-clone', id };
      break;
    case 'groups':
      if (!id) return { name: 'groups' };
      if (id === 'new' && !action) return { name: 'group-new' };
      if (action === 'edit' && parts.length === 3) return { name: 'group-edit', id };
      break;
    case 'notifications':
      if (!id) return { name: 'notifications' };
      if (id === 'deliveries' && !action) return { name: 'deliveries' };
      if (id === 'receipts' && !action) return { name: 'receipts' };
      break;
    case 'channels':
      if (id === 'new' && !action) return { name: 'channel-new' };
      if (id && action === 'edit' && parts.length === 3) return { name: 'channel-edit', id };
      break;
    case 'maintenance':
      if (!id) return { name: 'maintenance' };
      if (id === 'new' && !action) return { name: 'maintenance-new' };
      if (action === 'edit' && parts.length === 3) return { name: 'maintenance-edit', id };
      break;
    case 'status-pages':
      if (!id) return { name: 'status-pages' };
      if (id === 'new' && !action) return { name: 'status-page-new' };
      if (action === 'edit' && parts.length === 3) return { name: 'status-page-edit', id };
      break;
    case 'outage-groups': case 'node-groups': case 'incidents': case 'nodes': case 'config': case 'accounts': case 'account': case 'tags':
      if (parts.length === 1) return { name: section };
      break;
  }
  return { name: 'not-found', path: raw };
}

export const href = (...parts: string[]): string => `#/${parts.map(encodeURIComponent).join('/')}`;

/**
 * Shareable public status link. The node address is public information; no credential ever enters it. A control-plane
 * link names a saved registration instead, which is a public selector, not a credential.
 */
export function publicHref(slug: string, nodes: string[], registration?: string): string {
  const query = new URLSearchParams();
  for (const node of nodes) query.append('node', node);
  if (registration) query.set('registration', registration);
  const text = query.toString();
  return `#/status/${encodeURIComponent(slug)}${text ? `?${text}` : ''}`;
}

/** Only a public channel ID enters this link; delivery data stays in the existing private view. */
export const deliveryHref = (channelId?: string): string => `${href('notifications', 'deliveries')}${channelId ? `?channel=${encodeURIComponent(channelId)}` : ''}`;

type NavigationGuard = (proceed: () => void) => boolean;
let navigationGuard: NavigationGuard | undefined;

/** The channel editor owns the registration. Returning true holds the route and its mounted draft. */
export function registerNavigationGuard(guard: NavigationGuard): () => void {
  navigationGuard = guard;
  return () => { if (navigationGuard === guard) navigationGuard = undefined; };
}

/** Deliberate context changes can discard the same draft as route changes. Forced authentication changes are unaffected. */
export function requestNavigation(proceed: () => void): void {
  if (!navigationGuard?.(proceed)) proceed();
}

export function navigate(target: string): void {
  if (location.hash === target) return;
  requestNavigation(() => { location.hash = target; });
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  useEffect(() => {
    const indexKey = 'canaryRouteIndex';
    const historyIndex = (): number | undefined => {
      const value: unknown = history.state?.[indexKey];
      return typeof value === 'number' && Number.isSafeInteger(value) ? value : undefined;
    };
    const stamp = (index: number): void => history.replaceState({ ...history.state, [indexKey]: index }, '');
    let acceptedHash = location.hash;
    let acceptedIndex = historyIndex() ?? 0;
    stamp(acceptedIndex);
    let restoring: { after?: () => void } | null = null;
    let indexedTraversal = true;
    const commit = (target: string, index: number): void => {
      acceptedHash = target;
      acceptedIndex = index;
      stamp(index);
      setRoute(parseRoute(target));
    };
    const update = (): void => {
      const target = location.hash;
      if (target === acceptedHash) {
        const after = restoring?.after;
        restoring = null;
        after?.();
        return;
      }
      const reportedIndex = historyIndex();
      const targetIndex = reportedIndex ?? acceptedIndex + 1;
      const traversal = indexedTraversal && reportedIndex !== undefined && targetIndex !== acceptedIndex;
      const proceed = (): void => {
        if (traversal) {
          const move = (): void => history.go(targetIndex - acceptedIndex);
          // Discard may be chosen before the browser finishes restoring the editor's entry.
          if (restoring) restoring.after = move; else move();
        } else {
          history.replaceState(history.state, '', `${location.pathname}${location.search}${target}`);
          commit(target, targetIndex);
        }
      };
      // Consult the editor BEFORE React can update/unmount it. Back/Forward restore the actual history entry,
      // so Keep editing also preserves the next Back destination, rather than overwriting that entry's URL.
      if (navigationGuard?.(proceed)) {
        if (traversal) {
          stamp(targetIndex);
          restoring = {};
          history.go(acceptedIndex - targetIndex);
        } else {
          // Legacy/unindexed entries and direct hash edits have no reliable traversal offset. Hold the mounted
          // editor and URL; stop using offsets after this gap, since their distance can no longer be trusted.
          if (reportedIndex === undefined) indexedTraversal = false;
          history.replaceState(history.state, '', `${location.pathname}${location.search}${acceptedHash}`);
        }
        return;
      }
      commit(target, targetIndex);
    };
    const click = (event: MouseEvent): void => {
      if (!navigationGuard || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null;
      if (!anchor || anchor.hasAttribute('download') || anchor.target && anchor.target !== '_self') return;
      const target = new URL(anchor.href, location.href);
      if (target.origin !== location.origin || target.pathname !== location.pathname || target.search !== location.search || !target.hash) return;
      event.preventDefault();
      navigate(target.hash);
    };
    window.addEventListener('hashchange', update);
    document.addEventListener('click', click);
    return () => {
      window.removeEventListener('hashchange', update);
      document.removeEventListener('click', click);
    };
  }, []);
  return route;
}

/** Top-level section for rail highlighting. */
export function sectionOf(route: Route): string {
  switch (route.name) {
    case 'monitor': case 'monitor-new': case 'monitor-edit': case 'fleet-monitor-edit': case 'monitor-clone': case 'tags': return 'monitors';
    case 'group-new': case 'group-edit': case 'outage-groups': case 'node-groups': return 'groups';
    case 'deliveries': case 'receipts': case 'channel-new': case 'channel-edit': return 'notifications';
    case 'maintenance-new': case 'maintenance-edit': return 'maintenance';
    case 'status-page-new': case 'status-page-edit': return 'status-pages';
    case 'not-found': case 'public-status': return '';
    default: return route.name;
  }
}
